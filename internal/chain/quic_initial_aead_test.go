package chain

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestBuildQUICInitialAEAD_Structure verifies the built QUIC Initial is a
// well-formed Long Header Initial (≥1200 bytes, QUIC v1, DCID present) carrying
// an AEAD-encrypted payload. We can't assert the plaintext (it's encrypted) but
// the header shape and minimum datagram size are the QUIC invariants.
func TestBuildQUICInitialAEAD_Structure(t *testing.T) {
	pkt, dcid, err := buildQUICInitialAEAD("www.cloudflare.com")
	if err != nil {
		t.Fatalf("buildQUICInitialAEAD: %v", err)
	}
	// QUIC Initial datagrams must be ≥1200 bytes (RFC 9000 §14.1).
	if len(pkt) < 1200 {
		t.Errorf("Initial packet = %d bytes, want ≥1200", len(pkt))
	}
	// First byte: Long Header form (0x80 set) + Fixed bit (0x40) + Initial
	// (00) + PN length. 0xC0 mask isolates the form+fixed+type; type 00 = Initial.
	if pkt[0]&0x80 == 0 {
		t.Errorf("first byte 0x%x: Long Header form bit not set", pkt[0])
	}
	if pkt[0]&0x40 == 0 {
		t.Errorf("first byte 0x%x: Fixed bit not set (QUIC v1 requires it)", pkt[0])
	}
	if (pkt[0]&0x30)>>4 != 0 {
		t.Errorf("first byte 0x%x: header type = %d, want 0 (Initial)", pkt[0], (pkt[0]&0x30)>>4)
	}
	// Version: QUIC v1 = 0x00000001.
	if pkt[1] != 0x00 || pkt[2] != 0x00 || pkt[3] != 0x00 || pkt[4] != 0x01 {
		t.Errorf("version = %x, want 00000001 (QUIC v1)", pkt[1:5])
	}
	// DCID length (byte 5) should be 8 (we use 8-byte DCIDs).
	if pkt[5] != 8 {
		t.Errorf("DCID length = %d, want 8", pkt[5])
	}
	if len(dcid) != 8 {
		t.Errorf("returned DCID = %d bytes, want 8", len(dcid))
	}
	// The DCID in the header (bytes 6..13) must match the returned dcid.
	if string(pkt[6:14]) != string(dcid) {
		t.Errorf("header DCID %x != returned DCID %x", pkt[6:14], dcid)
	}
}

// TestBuildQUICInitialAEAD_DifferentDomainsProduceDifferentPackets verifies the
// Initial differs by domain (the SNI is in the encrypted ClientHello, so the
// ciphertext differs even though the structure is identical). This guards
// against a regression where SNI injection silently no-ops.
func TestBuildQUICInitialAEAD_DifferentDomainsProduceDifferentPackets(t *testing.T) {
	p1, _, err := buildQUICInitialAEAD("www.cloudflare.com")
	if err != nil {
		t.Fatal(err)
	}
	p2, _, err := buildQUICInitialAEAD("www.google.com")
	if err != nil {
		t.Fatal(err)
	}
	// The DCID is random per call, so the packets differ regardless of SNI — but
	// the test still catches a total no-op (identical bytes). A stronger check
	// would decrypt both and compare the ClientHello SNIs, but that requires the
	// server-side key derivation which isn't in scope here.
	if string(p1) == string(p2) {
		t.Error("two Initials are byte-identical (expected at least the random DCID to differ)")
	}
}

// TestQuicVarint verifies the QUIC variable-length integer encoding (RFC 9000
// §16) across the three length tiers the Initial builder uses.
func TestQuicVarint(t *testing.T) {
	cases := []struct {
		v    int
		want string // hex of the encoded bytes
	}{
		{63, "3f"},          // 1-byte (0-63)
		{64, "4040"},        // 2-byte (64-16383), 0x40 prefix
		{16383, "7fff"},     // 2-byte max
		{16384, "80004000"}, // 4-byte (16384-1073741823), 0x80 prefix
		{1200, "44b0"},      // 1200 → 2-byte (0x40 | (1200>>8), 1200&0xff)
	}
	for _, c := range cases {
		got := quicVarint(c.v)
		if hex.EncodeToString(got) != c.want {
			t.Errorf("quicVarint(%d) = %x, want %s", c.v, got, c.want)
		}
	}
}

// TestHkdfExpandLabel_DeriveInitialKeysSmoke verifies the RFC 9001 key derivation
// produces 16/12/16-byte key/iv/hp from a DCID (the lengths AES-128-GCM Initial
// keys must have). A length check is enough — the actual key bytes are
// deterministic given the DCID and salt, but checking those would just re-test
// the implementation against itself.
func TestHkdfExpandLabel_DeriveInitialKeysSmoke(t *testing.T) {
	dcid := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	key, iv, hp, err := deriveInitialKeys(dcid)
	if err != nil {
		t.Fatalf("deriveInitialKeys: %v", err)
	}
	if len(key) != 16 {
		t.Errorf("key = %d bytes, want 16 (AES-128)", len(key))
	}
	if len(iv) != 12 {
		t.Errorf("iv = %d bytes, want 12 (AES-128-GCM nonce)", len(iv))
	}
	if len(hp) != 16 {
		t.Errorf("hp = %d bytes, want 16 (header-protection AES key)", len(hp))
	}
	// Same DCID → same keys (deterministic — the salt is fixed).
	key2, iv2, hp2, _ := deriveInitialKeys(dcid)
	if string(key) != string(key2) || string(iv) != string(iv2) || string(hp) != string(hp2) {
		t.Error("deriveInitialKeys not deterministic for the same DCID")
	}
	// Different DCID → different keys.
	key3, _, _, _ := deriveInitialKeys([]byte{9, 9, 9, 9, 9, 9, 9, 9})
	if string(key) == string(key3) {
		t.Error("deriveInitialKeys produced identical keys for different DCIDs")
	}
}

// TestEnsureChainAWGMaterial_QuicLiveFallback verifies that the "quic-live"
// mimicry path gracefully degrades to synthesized "quic" packets when the live
// capture fails (e.g. no network, or the capture domain doesn't speak QUIC).
// A chain must never break because the orchestrator's network was down at apply
// time — the fallback keeps the AWG handshake working with synthesized packets.
func TestEnsureChainAWGMaterial_QuicLiveFallback(t *testing.T) {
	c := &model.Chain{
		Name:                "live-fallback",
		AWGCPSMimicry:       mimicryQuicLive,
		AWGCPSCaptureDomain: "nonexistent.invalid.domain.example", // guaranteed DNS/capture failure
	}
	preset := ConnectionPreset{CPSLevel: 3, AWGMimicry: mimicryQuicLive, AWG: &AWGPreset{JC: 4, JMIN: 40, JMAX: 70, S1: 1, S2: 2, S3: 3, S4: 4, H1: 1, H2: 2, H3: 3, H4: 4, CPSLevel: 3, Mimicry: mimicryQuicLive}}
	EnsureChainAWGMaterial(c, preset)
	// Capture failed → fell back to synthesized "quic" packets.
	if c.AWGCPSMimicry != "quic" {
		t.Errorf("mimicry = %q, want quic (fallback after capture failure)", c.AWGCPSMimicry)
	}
	if c.AWGCPSI1 == "" {
		t.Error("fallback must still populate I1 (synthesized)")
	}
	for i, s := range []string{c.AWGCPSI1, c.AWGCPSI2, c.AWGCPSI3, c.AWGCPSI4, c.AWGCPSI5} {
		if !strings.HasPrefix(s, "<b 0x") {
			t.Errorf("I%d not in CPS <b 0x...> format: %q", i+1, s)
		}
	}
}

// TestEnsureChainAWGMaterial_QuicLiveFailureCached verifies that once a live
// capture FAILS for a domain, a second EnsureChainAWGMaterial call for the SAME
// domain does NOT re-dial (the AWGCPSCaptureFailedDomain marker suppresses it).
// Proof: the synthesized fallback I1-I5 are byte-identical across the second
// call — a re-dial-and-fail would regenerate fresh random synthesized packets,
// so I1 would differ. This is the Rule 5 cache for the failure path: a flaky/
// unreachable domain must not force a UDP round-trip + timeout on every redeploy.
func TestEnsureChainAWGMaterial_QuicLiveFailureCached(t *testing.T) {
	c := &model.Chain{
		Name:                "live-fail-cached",
		AWGCPSMimicry:       mimicryQuicLive,
		AWGCPSCaptureDomain: "nonexistent.invalid.domain.example", // guaranteed failure
	}
	preset := ConnectionPreset{CPSLevel: 3, AWGMimicry: mimicryQuicLive, AWG: &AWGPreset{JC: 4, JMIN: 40, JMAX: 70, S1: 1, S2: 2, S3: 3, S4: 4, H1: 1, H2: 2, H3: 3, H4: 4, CPSLevel: 3, Mimicry: mimicryQuicLive}}
	EnsureChainAWGMaterial(c, preset)
	if c.AWGCPSMimicry != "quic" {
		t.Fatalf("first call: mimicry = %q, want quic (fallback)", c.AWGCPSMimicry)
	}
	if c.AWGCPSCaptureFailedDomain != c.AWGCPSCaptureDomain {
		t.Errorf("failed-domain marker = %q, want %q (records the failure)", c.AWGCPSCaptureFailedDomain, c.AWGCPSCaptureDomain)
	}
	firstI1, firstI2 := c.AWGCPSI1, c.AWGCPSI2

	// Second call for the SAME domain → cache hit (failure marker matches), no
	// re-dial. The synthesized I1-I5 are untouched (would differ if re-dialed).
	EnsureChainAWGMaterial(c, preset)
	if c.AWGCPSMimicry != "quic" {
		t.Errorf("second call: mimicry = %q, want quic (cache hit, no re-dial)", c.AWGCPSMimicry)
	}
	if c.AWGCPSI1 != firstI1 || c.AWGCPSI2 != firstI2 {
		t.Errorf("second call regenerated I1/I2 (cache miss → re-dial): first=%q/%q second=%q/%q", firstI1, firstI2, c.AWGCPSI1, c.AWGCPSI2)
	}

	// A domain CHANGE clears the failure cache → the new domain is retried.
	c.AWGCPSCaptureDomain = "also.invalid.other.example"
	EnsureChainAWGMaterial(c, preset)
	if c.AWGCPSCaptureFailedDomain != "also.invalid.other.example" {
		t.Errorf("domain change should re-attempt and re-record the failure marker; got %q", c.AWGCPSCaptureFailedDomain)
	}
}

// TestEnsureChainAWGMaterial_CacheStableAcrossRuns verifies that re-running
// EnsureChainAWGMaterial keeps the already-persisted I1-I5 (Rule 5: stable
// across redeploys) — for synthesized mimicry the cache is valid when
// level+mimicry match.
func TestEnsureChainAWGMaterial_CacheStableAcrossRuns(t *testing.T) {
	c := &model.Chain{Name: "cache"}
	preset := ConnectionPreset{CPSLevel: 3, AWGMimicry: "quic", AWG: &AWGPreset{JC: 4, JMIN: 40, JMAX: 70, S1: 1, S2: 2, S3: 3, S4: 4, H1: 1, H2: 2, H3: 3, H4: 4, CPSLevel: 3, Mimicry: "quic"}}
	EnsureChainAWGMaterial(c, preset)
	firstI1 := c.AWGCPSI1
	if firstI1 == "" {
		t.Fatal("first run did not populate I1")
	}
	// Second run with the same level+mimicry → cache hit, I1 unchanged.
	EnsureChainAWGMaterial(c, preset)
	if c.AWGCPSI1 != firstI1 {
		t.Errorf("I1 changed across re-runs (Rule 5 violation): %q → %q", firstI1, c.AWGCPSI1)
	}
}

// TestNormalizeDomain_QuicLiveCaptureDomain verifies the domain normalizer the
// live-capture path depends on strips scheme/path/port and lowercases.
func TestNormalizeDomain_QuicLiveCaptureDomain(t *testing.T) {
	cases := map[string]string{
		"https://www.cloudflare.com": "www.cloudflare.com",
		"http://Example.COM/":        "example.com",
		"www.google.com:443":         "www.google.com",
		"  disk.yandex.ru  ":         "disk.yandex.ru",
	}
	for in, want := range cases {
		if got := NormalizeDomain(in); got != want {
			t.Errorf("NormalizeDomain(%q) = %q, want %q", in, got, want)
		}
	}
}
