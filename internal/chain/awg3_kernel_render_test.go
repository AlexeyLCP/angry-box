package chain

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// validHexKey is a deterministic 32-byte hex string (64 chars) for tests so the
// HPK hex→base64 conversion is stable. GenerateAWG3Material would use random
// bytes and make assertions on the exact base64 flaky.
const validHexKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// hexKeyBase64 returns the base64 form the kernel conf expects, derived from the
// hex key (mirrors awg3HPKHexToBase64). Computed in-test rather than hardcoded
// so a copy-paste typo can't desync the assertion from the converter.
func hexKeyBase64(t *testing.T, hexKey string) string {
	t.Helper()
	b, err := hex.DecodeString(hexKey)
	if err != nil {
		t.Fatalf("bad test hex key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// TestWriteAWG3ConfLines pins the kernel-AWG3 awg0.conf [Interface] contract
// (AGENTS #5 revision): HeaderProtectionKey is emitted as base64 (the hex-persisted
// form converted), ContentPaddingAddition + RekeyAfterTime as "lo-hi" ranges, in
// [Interface] before [Peer]. An invalid HPK emits nothing (fail-closed).
func TestWriteAWG3ConfLines(t *testing.T) {
	mat := &AWGObfsMaterial{
		AWG3Mode:               true,
		HeaderProtectionKey:    validHexKey,
		ContentPaddingAddition: "1-16",
		RekeyAfterTime:         "90-110",
	}
	var b strings.Builder
	writeAWG3ConfLines(&b, mat)
	out := b.String()

	// HPK base64 form of the hex key (deterministic).
	wantHPK := hexKeyBase64(t, validHexKey)
	if !strings.Contains(out, "HeaderProtectionKey = "+wantHPK+"\n") {
		t.Errorf("HeaderProtectionKey line missing/wrong\ngot:\n%s\nwant contains: HeaderProtectionKey = %s", out, wantHPK)
	}
	if !strings.Contains(out, "ContentPaddingAddition = 1-16\n") {
		t.Errorf("ContentPaddingAddition missing\ngot:\n%s", out)
	}
	if !strings.Contains(out, "RekeyAfterTime = 90-110\n") {
		t.Errorf("RekeyAfterTime missing\ngot:\n%s", out)
	}
}

// TestWriteAWG3ConfLines_InvalidHPKEmitsNothing — a malformed HPK (wrong length
// / non-hex) MUST emit nothing rather than a broken key that awg-quick rejects.
func TestWriteAWG3ConfLines_InvalidHPKEmitsNothing(t *testing.T) {
	for _, bad := range []string{"", "nothex", "00"} {
		var b strings.Builder
		writeAWG3ConfLines(&b, &AWGObfsMaterial{HeaderProtectionKey: bad})
		if b.String() != "" {
			t.Errorf("invalid HPK %q emitted %q (want empty)", bad, b.String())
		}
	}
}

// TestWriteAWG3ConfLines_OmitsEmptyOptional — CPM/RAT are optional; when empty
// they are not emitted, but HPK still is.
func TestWriteAWG3ConfLines_OmitsEmptyOptional(t *testing.T) {
	var b strings.Builder
	writeAWG3ConfLines(&b, &AWGObfsMaterial{HeaderProtectionKey: validHexKey})
	out := b.String()
	if !strings.Contains(out, "HeaderProtectionKey = ") {
		t.Errorf("HPK should still emit when CPM/RAT empty\ngot:\n%s", out)
	}
	if strings.Contains(out, "ContentPaddingAddition") || strings.Contains(out, "RekeyAfterTime") {
		t.Errorf("empty CPM/RAT should not emit\ngot:\n%s", out)
	}
}

// TestRenderServerAWGConf_AWG3InInterface pins that RenderServerAWGConf emits
// the HPK/CPM/RAT block inside [Interface] and BEFORE [Peer] (awg setconf parses
// device-level fields only in [Interface]). This is the kernel-AWG3 server conf
// shape the new amnezia-box kernel module (PR #192) + tools v3.0 accept.
func TestRenderServerAWGConf_AWG3InInterface(t *testing.T) {
	conf := RenderServerAWGConf(AWGServerConfParams{
		ServerPrivateKey: "priv",
		ListenPort:       51820,
		TunnelAddress:    "10.8.0.1/24",
		Amnezia:          &config.AmneziaOptions{JC: 120, JMIN: 50, JMAX: 1000, S1: 24, S2: 24, S3: 24, S4: 24, H1: "12", H2: "13", H3: "14", H4: "15"},
		AWG3:             &AWGObfsMaterial{HeaderProtectionKey: validHexKey, ContentPaddingAddition: "1-16", RekeyAfterTime: "90-110"},
		Peers:            []AWGServerPeer{{PublicKey: "peerpub", AllowedIPs: "10.8.0.2/32"}},
	})
	peerIdx := strings.Index(conf, "[Peer]")
	hpkIdx := strings.Index(conf, "HeaderProtectionKey")
	if peerIdx < 0 || hpkIdx < 0 {
		t.Fatalf("missing [Peer] or HeaderProtectionKey in conf:\n%s", conf)
	}
	if hpkIdx > peerIdx {
		t.Fatalf("HeaderProtectionKey must be BEFORE [Peer] (device-level):\n%s", conf)
	}
	// S1-S4 present (>= 12 for HPK), H1-H4 present (unique), all before [Peer].
	for _, k := range []string{"S1 = 24", "S4 = 24", "H1 = 12", "H4 = 15", "ContentPaddingAddition = 1-16", "RekeyAfterTime = 90-110"} {
		idx := strings.Index(conf, k)
		if idx < 0 || idx > peerIdx {
			t.Errorf("%q missing or after [Peer] in conf:\n%s", k, conf)
		}
	}
}

// TestRenderServerAWGConf_NoAWG3WhenNil — a non-v3 (AWG 1.5/2.0) server conf
// must NOT carry any HPK/CPM/RAT lines.
func TestRenderServerAWGConf_NoAWG3WhenNil(t *testing.T) {
	conf := RenderServerAWGConf(AWGServerConfParams{
		ServerPrivateKey: "priv",
		Amnezia:          &config.AmneziaOptions{JC: 4, S1: 0, S2: 0},
	})
	for _, bad := range []string{"HeaderProtectionKey", "ContentPaddingAddition", "RekeyAfterTime"} {
		if strings.Contains(conf, bad) {
			t.Errorf("non-AWG3 conf must not contain %q:\n%s", bad, conf)
		}
	}
}

// TestAWGVersionMajor pins the version-major parser used by capability detection.
func TestAWGVersionMajor(t *testing.T) {
	cases := map[string]int{
		"3.0.20260731-04": 3,
		"1.0.20260611":    1,
		"3":               3,
		"":                0,
		"unknown":         0,
		"v3.0":            0, // caller strips the "v" prefix; raw "v3" is not digits-first
	}
	for in, want := range cases {
		if got := awgVersionMajor(in); got != want {
			t.Errorf("awgVersionMajor(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestAWGKernelVersionSupportsHPK pins the kernel-module version gate: PR #192
// modules report >= 3.0; the legacy module is 1.0.x.
func TestAWGKernelVersionSupportsHPK(t *testing.T) {
	for _, v := range []string{"3.0.20260731-04", "3.1.0", "3"} {
		if !awgKernelVersionSupportsHPK(v) {
			t.Errorf("awgKernelVersionSupportsHPK(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"1.0.20260611", "2.0.0", "", "unknown"} {
		if awgKernelVersionSupportsHPK(v) {
			t.Errorf("awgKernelVersionSupportsHPK(%q) = true, want false", v)
		}
	}
}

// TestAWGToolsVersionSupportsHPK pins the userspace amneziaawg-tools version
// gate: v3.0.20260730+ has the HeaderProtectionKey keyword (config.c); the
// legacy v1.0.x rejects the line.
func TestAWGToolsVersionSupportsHPK(t *testing.T) {
	good := []string{
		"amneziawg-tools v3.0.20260730 - https://amnezia.org",
		"v3.0.20260730",
		"v3.1.5",
	}
	for _, v := range good {
		if !awgToolsVersionSupportsHPK(v) {
			t.Errorf("awgToolsVersionSupportsHPK(%q) = false, want true", v)
		}
	}
	bad := []string{
		"amneziawg-tools v1.0.20260618-2 - https://amnezia.org",
		"v1.0.20260618-2",
		"",
		"awg: command not found",
	}
	for _, v := range bad {
		if awgToolsVersionSupportsHPK(v) {
			t.Errorf("awgToolsVersionSupportsHPK(%q) = true, want false", v)
		}
	}
}

// TestKernelAWG3EnabledFor gates the render branches off the runtime-only
// NodeInfo flag. nil-safe for the dry-run / preview render path.
func TestKernelAWG3EnabledFor(t *testing.T) {
	if kernelAWG3EnabledFor(nil) {
		t.Error("kernelAWG3EnabledFor(nil) = true, want false")
	}
	if kernelAWG3EnabledFor(&model.NodeInfo{}) {
		t.Error("kernelAWG3EnabledFor(NodeInfo{}) = true, want false")
	}
	if !kernelAWG3EnabledFor(&model.NodeInfo{KernelAWG3Supported: true}) {
		t.Error("kernelAWG3EnabledFor(KernelAWG3Supported=true) = false, want true")
	}
}

// TestInboundAWG3MaterialForKernel — only a v3 inbound yields AWG3 material for
// the kernel-render AWGServerConfParams.AWG3 field; non-v3 returns nil.
func TestInboundAWG3MaterialForKernel(t *testing.T) {
	if got := inboundAWG3MaterialForKernel(nil); got != nil {
		t.Error("nil inbound must yield nil material")
	}
	v2 := &model.NodeInbound{Protocol: "awg", AWGVersion: model.AWGVersion2}
	if got := inboundAWG3MaterialForKernel(v2); got != nil {
		t.Error("v2 inbound must yield nil material (no HPK)")
	}
	v3 := &model.NodeInbound{Protocol: "awg", AWGVersion: model.AWGVersion3, AWG3HeaderProtectionKey: validHexKey}
	got := inboundAWG3MaterialForKernel(v3)
	if got == nil {
		t.Fatal("v3 inbound must yield material")
	}
	if got.HeaderProtectionKey != validHexKey {
		t.Errorf("material HPK = %q, want %q", got.HeaderProtectionKey, validHexKey)
	}
}
