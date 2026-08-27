package chain

// awg3_mode_test.go — unit tests for the AWG 3.0 header-protection mode
// (opt-in per-inbound toggle, AGENTS #5). Covers the four guarantees the
// render path must hold when AWG3Mode is on:
//   1. the userspace `type:"awg"` endpoint JSON carries header_protection_key
//      (base64) + content_padding_addition + rekey_after_time;
//   2. S1-S4 are raised to >= 12 (HeaderCipherNonceSize=12) regardless of the
//      preset's values;
//   3. one peer per qualified user is emitted (multi-peer), keyed on
//      AWGPublicKey/AWGAddress;
//   4. the kernel path is skipped — RenderNodeAWGConfs emits NO awg0.conf for
//      an AWG3-mode inbound, and the TUN overlay is not needed.
//   5. the client .conf carries HPK/CPM/RAT inline in [Interface] (the
//      AmneziaWG app + userspace amneziawg-go parse them natively).
// The live E2E (§36) already verified the real handshake + egress on n1; these
// tests pin the render contract so a refactor can't silently regress it.

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// awg3MaterialForTest builds an AWG3-on material + a low-S preset so the
// raise-to-12 guard is observable. NOTE: ConnectionPreset.AWG is a *AWGPreset
// shared across all callers of GetDefaultPreset (GetPreset returns the value
// but the pointer field is shared) — we must clone AWG before mutating its
// S fields, or the global preset is poisoned for every later test in the
// package (breaks TestMigrateV2_RenderEquivalence_AWGEntry, which reads the
// default preset's S values for byte-equivalence).
func awg3MaterialForTest(t *testing.T) (*AWGObfsMaterial, ConnectionPreset) {
	t.Helper()
	ib := &model.NodeInbound{Protocol: "awg", Port: 51841, AWG3Mode: true}
	preset := GetDefaultPreset()
	// Clone AWG so we never mutate the shared global AWG pointer.
	awgCopy := *preset.AWG
	awgCopy.S1, awgCopy.S2, awgCopy.S3, awgCopy.S4 = 5, 8, 10, 11
	preset.AWG = &awgCopy
	EnsureInboundAWGMaterial(ib, preset)
	mat := InboundAWGObfsMaterial(ib)
	if mat == nil || !mat.AWG3Mode || mat.HeaderProtectionKey == "" {
		t.Fatalf("AWG3 material not generated: %+v", mat)
	}
	return mat, preset
}

func TestAWG3Mode_RendersHPK(t *testing.T) {
	mat, preset := awg3MaterialForTest(t)
	users := []model.User{{
		Active: true, AWGPublicKey: "PUBKEY1", AWGAddress: "10.8.0.2/32",
	}}
	epJSON, _, err := buildAWGUserInboundMulti(51841, "awg3-in", &preset, "", users, mat)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := string(epJSON)
	// HPK is hex in the material, base64 in the JSON (sing-box endpoint.go
	// decodes base64 → hex for the UAPI). Verify both the field is present and
	// the base64 round-trips back to the persisted hex.
	if !strings.Contains(s, `"header_protection_key":`) {
		t.Error("endpoint JSON must carry header_protection_key")
	}
	hpkB64 := extractJSONString(s, "header_protection_key")
	if hpkB64 == "" {
		t.Fatal("header_protection_key value not found")
	}
	raw, err := base64.StdEncoding.DecodeString(hpkB64)
	if err != nil {
		t.Fatalf("HPK is not valid base64: %v", err)
	}
	if hex.EncodeToString(raw) != mat.HeaderProtectionKey {
		t.Errorf("HPK round-trip mismatch: json→hex %q != material %q", hex.EncodeToString(raw), mat.HeaderProtectionKey)
	}
	if !strings.Contains(s, `"content_padding_addition":`) {
		t.Error("endpoint JSON must carry content_padding_addition")
	}
	if !strings.Contains(s, `"rekey_after_time":`) {
		t.Error("endpoint JSON must carry rekey_after_time")
	}
}

func TestAWG3Mode_S1S4RaisedTo12(t *testing.T) {
	mat, preset := awg3MaterialForTest(t) // preset S1-S4 = 5,8,10,11
	users := []model.User{{Active: true, AWGPublicKey: "PUBKEY1", AWGAddress: "10.8.0.2/32"}}
	epJSON, _, err := buildAWGUserInboundMulti(51841, "awg3-in", &preset, "", users, mat)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := string(epJSON)
	for _, key := range []string{`"s1":`, `"s2":`, `"s3":`, `"s4":`} {
		if !strings.Contains(s, key) {
			t.Errorf("endpoint JSON must carry %s", key)
		}
	}
	s1 := extractJSONInt(s, "s1")
	s2 := extractJSONInt(s, "s2")
	s3 := extractJSONInt(s, "s3")
	s4 := extractJSONInt(s, "s4")
	for _, v := range []int{s1, s2, s3, s4} {
		if v < 12 {
			t.Errorf("S value %d < 12 (header protection needs >= 12)", v)
		}
	}
}

func TestAWG3Mode_MultiPeer(t *testing.T) {
	mat, preset := awg3MaterialForTest(t)
	users := []model.User{
		{Active: true, AWGPublicKey: "PUBKEY1", AWGAddress: "10.8.0.2/32"},
		{Active: true, AWGPublicKey: "PUBKEY2", AWGAddress: "10.8.0.3/32"},
		{Active: false, AWGPublicKey: "PUBKEY3", AWGAddress: "10.8.0.4/32"}, // inactive — skipped
	}
	epJSON, _, err := buildAWGUserInboundMulti(51841, "awg3-in", &preset, "", users, mat)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	s := string(epJSON)
	if c := strings.Count(s, `"public_key":`); c != 2 {
		t.Errorf("expected 2 peer public_keys (active users only), got %d", c)
	}
	if !strings.Contains(s, "10.8.0.2/32") || !strings.Contains(s, "10.8.0.3/32") {
		t.Error("both active users' allowed IPs must be present")
	}
	if strings.Contains(s, "10.8.0.4/32") {
		t.Error("inactive user must NOT be a peer")
	}
}

func TestAWG3Mode_KernelPathSkipped(t *testing.T) {
	// An AWG3-mode standalone inbound must NOT render a kernel awg0/awg1.conf
	// (it renders as a userspace endpoint in the merged config instead).
	ib := &model.NodeInbound{
		Protocol: "awg", Port: 51841, Tag: "sa-0-awg",
		ServerPrivKey: "SRVPRIV", AWG3Mode: true,
	}
	preset := GetDefaultPreset()
	EnsureInboundAWGMaterial(ib, preset)
	files, _ := RenderNodeAWGConfs(
		&model.NodeInfo{Inbounds: []model.NodeInbound{*ib}},
		nil, nil, nil,
	)
	for _, f := range files {
		if strings.Contains(f.Path, "awg0") || strings.Contains(f.Path, "awg1") {
			t.Errorf("AWG3-mode inbound must NOT emit a kernel conf: %s", f.Path)
		}
	}
	// TUN overlay must not be needed for a node with only an AWG3 inbound.
	ni := &model.NodeInfo{Inbounds: []model.NodeInbound{*ib}}
	if awgTUNOverlayNeeded(nil, ni) {
		t.Error("AWG3-mode inbound must NOT trigger the TUN overlay")
	}
}

func TestAWG3Mode_NotRaisedWhenOff(t *testing.T) {
	// AWG3 off: S1-S4 must stay at the preset's values (no raise). Guards
	// against the raise logic firing for non-AWG3 endpoints. Clone AWG so we
	// don't poison the shared global preset.
	ib := &model.NodeInbound{Protocol: "awg", Port: 51841, AWG3Mode: false}
	preset := GetDefaultPreset()
	awgCopy := *preset.AWG
	awgCopy.S1 = 5
	preset.AWG = &awgCopy
	EnsureInboundAWGMaterial(ib, preset)
	mat := InboundAWGObfsMaterial(ib)
	if mat == nil {
		t.Fatal("material expected for a CPS preset even without AWG3")
	}
	if mat.AWG3Mode {
		t.Error("AWG3Mode must be off")
	}
	users := []model.User{{Active: true, AWGPublicKey: "PUBKEY1", AWGAddress: "10.8.0.2/32"}}
	epJSON, _, err := buildAWGUserInboundMulti(51841, "awg3-in", &preset, "", users, mat)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if strings.Contains(string(epJSON), `"header_protection_key"`) {
		t.Error("AWG3-off endpoint must NOT carry header_protection_key")
	}
}

func TestAWG3ClientConf_HasHPK(t *testing.T) {
	// The client .conf must carry HPK/CPM/RAT inline in [Interface], before
	// [Peer] — matching the amneziawg-go UAPI ordering (device-level amnezia/
	// AWG3 fields BEFORE public_key, verified live §36). AWG3 off must omit
	// them.
	ib := &model.NodeInbound{Protocol: "awg", Port: 51841, AWG3Mode: true}
	preset := GetDefaultPreset()
	EnsureInboundAWGMaterial(ib, preset)
	mat := InboundAWGObfsMaterial(ib)
	conf := renderAWGQuickConf("1.2.3.4", 51841, "CLIENTPRIV", "SERVERPUB", "10.8.0.2/32", &preset, mat, ib.EffectiveAWGVersion(), "")
	if !strings.Contains(conf, "HeaderProtectionKey = ") {
		t.Errorf("client .conf must carry HeaderProtectionKey:\n%s", conf)
	}
	if !strings.Contains(conf, "ContentPaddingAddition = ") || !strings.Contains(conf, "RekeyAfterTime = ") {
		t.Errorf("client .conf must carry CPM + RAT:\n%s", conf)
	}
	// Ordering: HPK must sit in [Interface], before [Peer].
	hpkIdx := strings.Index(conf, "HeaderProtectionKey")
	peerIdx := strings.Index(conf, "[Peer]")
	if hpkIdx < 0 || peerIdx < 0 || hpkIdx > peerIdx {
		t.Errorf("HeaderProtectionKey must be in [Interface] before [Peer] (hpk=%d peer=%d):\n%s", hpkIdx, peerIdx, conf)
	}
	// Off case.
	ib2 := &model.NodeInbound{Protocol: "awg", Port: 51841, AWG3Mode: false}
	EnsureInboundAWGMaterial(ib2, preset)
	conf2 := renderAWGQuickConf("1.2.3.4", 51841, "CLIENTPRIV", "SERVERPUB", "10.8.0.2/32", &preset, InboundAWGObfsMaterial(ib2), ib2.EffectiveAWGVersion(), "")
	if strings.Contains(conf2, "HeaderProtectionKey") {
		t.Errorf("AWG3-off client .conf must NOT carry HeaderProtectionKey:\n%s", conf2)
	}
}

// extractJSONString / extractJSONInt are tiny JSON field pullers (avoid pulling
// in encoding/json + struct unmarshalling for one-off assertions).
func extractJSONString(s, key string) string {
	needle := `"` + key + `":"`
	i := strings.Index(s, needle)
	if i < 0 {
		return ""
	}
	rest := s[i+len(needle):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func extractJSONInt(s, key string) int {
	needle := `"` + key + `":`
	i := strings.Index(s, needle)
	if i < 0 {
		return -1
	}
	rest := s[i+len(needle):]
	rest = strings.TrimLeft(rest, " ")
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return -1
	}
	n := 0
	for _, c := range rest[:end] {
		n = n*10 + int(c-'0')
	}
	return n
}