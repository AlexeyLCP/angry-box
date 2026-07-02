package chain

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/singbox/config"
	"golang.org/x/crypto/curve25519"
)

// ─── generateHopParams ────────────────────────────────────────────────────────

func TestGenerateHopParams(t *testing.T) {
	preset := GetDefaultPreset()
	p, err := generateHopParams(443, &preset)
	if err != nil {
		t.Fatalf("generateHopParams: %v", err)
	}
	if p.Port != 443 {
		t.Errorf("port = %d, want 443", p.Port)
	}
	if p.PrivateKey == "" {
		t.Error("PrivateKey is empty")
	}
	if p.ShortID == "" {
		t.Error("ShortID is empty")
	}
	if p.UUID == "" {
		t.Error("UUID is empty")
	}
	if p.ServerName == "" {
		t.Error("ServerName is empty")
	}
	// UUID must be valid format
	if len(p.UUID) != 36 {
		t.Errorf("UUID length = %d, want 36", len(p.UUID))
	}
	// ShortID must be hex
	if _, err := hex.DecodeString(p.ShortID); err != nil {
		t.Errorf("ShortID not valid hex: %v", err)
	}
	// PrivateKey must be valid base64 (32 bytes)
	privBytes, err := base64.RawURLEncoding.DecodeString(p.PrivateKey)
	if err != nil {
		t.Errorf("PrivateKey not valid base64: %v", err)
	}
	if len(privBytes) != 32 {
		t.Errorf("PrivateKey length = %d, want 32", len(privBytes))
	}
}

func TestGenerateHopParams_DeterministicServerName(t *testing.T) {
	preset := GetDefaultPreset()
	// Multiple calls should produce different UUIDs but same ServerName from preset
	p1, _ := generateHopParams(443, &preset)
	p2, _ := generateHopParams(443, &preset)
	if p1.UUID == p2.UUID {
		t.Error("UUIDs should be different")
	}
	if p1.ShortID == p2.ShortID {
		t.Error("ShortIDs should be different")
	}
	// ServerName should be the same (from preset)
	if p1.ServerName != p2.ServerName {
		t.Errorf("ServerNames differ: %q vs %q", p1.ServerName, p2.ServerName)
	}
}

// ─── publicKeyB64 ─────────────────────────────────────────────────────────────

func TestPublicKeyB64(t *testing.T) {
	h := &hopParams{
		PrivateKey: "eE2tO7r8Ff_3hWwK-Qv6RzL0X1sP_bN4mD5Y8Vj_AQA",
	}
	pub, err := h.publicKeyB64()
	if err != nil {
		t.Fatalf("publicKeyB64: %v", err)
	}
	if pub == "" {
		t.Error("public key is empty")
	}
	// Verify it's valid base64 and 32 bytes
	pubBytes, err := base64.RawURLEncoding.DecodeString(pub)
	if err != nil {
		t.Errorf("public key not valid base64: %v", err)
	}
	if len(pubBytes) != 32 {
		t.Errorf("public key length = %d, want 32", len(pubBytes))
	}
}

func TestPublicKeyB64_InvalidKey(t *testing.T) {
	h := &hopParams{PrivateKey: "!!invalid!!"}
	_, err := h.publicKeyB64()
	if err == nil {
		t.Error("expected error for invalid key")
	}
}

func TestPublicKeyB64_WrongLength(t *testing.T) {
	h := &hopParams{PrivateKey: base64.RawURLEncoding.EncodeToString([]byte("short"))}
	_, err := h.publicKeyB64()
	if err == nil {
		t.Error("expected error for wrong length key")
	}
}

// ─── buildXHTTPTransportOutbound ──────────────────────────────────────────────

func TestBuildXHTTPTransportOutbound(t *testing.T) {
	p := &hopParams{
		Port:       443,
		UUID:       "12345678-1234-1234-1234-123456789012",
		ServerName: "cloudflare.com",
		PrivateKey: "eE2tO7r8Ff_3hWwK-Qv6RzL0X1sP_bN4mD5Y8Vj_AQA",
		ShortID:    "abcdef1234567890",
	}
	preset := ConnectionPreset{
		XHTTP: &XHTTPPreset{
			Methods: []string{"POST"},
			Paths:   []string{"/api/v2/test"},
			Hosts:   []string{"cloudflare.com"},
			Headers: map[string][]string{"X-Test": {"value"}},
		},
		Reality: &RealityPreset{Fingerprints: []string{"firefox"}},
	}

	out, err := buildXHTTPTransportOutbound(p, "1.2.3.4", "test-out", &preset)
	if err != nil {
		t.Fatalf("buildXHTTPTransportOutbound: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["type"] != "vless" {
		t.Errorf("type = %v", m["type"])
	}
	if m["tag"] != "test-out" {
		t.Errorf("tag = %v", m["tag"])
	}
	if m["server"] != "1.2.3.4" {
		t.Errorf("server = %v", m["server"])
	}
	// Verify transport
	tr, ok := m["transport"].(map[string]any)
	if !ok {
		t.Fatal("transport missing")
	}
	if tr["type"] != "http" {
		t.Errorf("transport.type = %v", tr["type"])
	}
}

func TestBuildXHTTPTransportOutbound_NoPreset(t *testing.T) {
	p := &hopParams{
		Port:       443,
		UUID:       "uuid-x",
		ServerName: "example.com",
		PrivateKey: "eE2tO7r8Ff_3hWwK-Qv6RzL0X1sP_bN4mD5Y8Vj_AQA",
		ShortID:    "abcdef1234567890",
	}
	preset := ConnectionPreset{} // empty

	out, err := buildXHTTPTransportOutbound(p, "1.2.3.4", "test-out", &preset)
	if err != nil {
		t.Fatalf("buildXHTTPTransportOutbound (no preset): %v", err)
	}
	if len(out) == 0 {
		t.Error("output is empty")
	}
}

// ─── buildAWGUserInbound ─────────────────────────────────────────────────────

func TestBuildAWGUserInbound(t *testing.T) {
	preset := ConnectionPreset{
		AWG: &AWGPreset{JC: 4, JMIN: 40, JMAX: 70, H1: 1, H2: 2, H3: 3, H4: 4},
	}
	ep, pubKey, err := buildAWGUserInbound(12345, "test-uuid", "awg-in", &preset, "", "client-pub-key-here")
	if err != nil {
		t.Fatalf("buildAWGUserInbound: %v", err)
	}
	if len(ep) == 0 {
		t.Error("endpoint is empty")
	}
	if pubKey == "" {
		t.Error("public key is empty")
	}

	var m map[string]any
	if err := json.Unmarshal(ep, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["type"] != "wireguard" {
		t.Errorf("type = %v", m["type"])
	}
	if m["tag"] != "awg-in" {
		t.Errorf("tag = %v", m["tag"])
	}
	// Verify peers
	peers, ok := m["peers"].([]any)
	if !ok || len(peers) == 0 {
		t.Fatal("peers missing or empty")
	}
}

func TestBuildAWGUserInbound_WithServerKey(t *testing.T) {
	priv, pub, _ := GenerateWireGuardKeypair()
	preset := ConnectionPreset{
		AWG: &AWGPreset{JC: 5, JMIN: 50, JMAX: 80, H1: 1, H2: 2, H3: 3, H4: 4},
	}
	_, pubKey, err := buildAWGUserInbound(12345, "uuid", "awg-in", &preset, priv, "client-pub")
	if err != nil {
		t.Fatalf("buildAWGUserInbound: %v", err)
	}
	if pubKey != pub {
		t.Errorf("pubKey mismatch: got %q, want %q", pubKey, pub)
	}
}

func TestBuildAWGUserInbound_NoPreset(t *testing.T) {
	// nil AWG preset should use defaults
	var preset ConnectionPreset
	ep, pubKey, err := buildAWGUserInbound(12345, "uuid", "awg-in", &preset, "", "client-pub")
	if err != nil {
		t.Fatalf("buildAWGUserInbound (no preset): %v", err)
	}
	if len(ep) == 0 {
		t.Error("endpoint is empty")
	}
	if pubKey == "" {
		t.Error("public key is empty")
	}
}

// ─── deriveWireGuardPublicFromPrivate ─────────────────────────────────────────

func TestDeriveWireGuardPublicFromPrivate(t *testing.T) {
	priv, pub, _ := GenerateWireGuardKeypair()
	derived, err := deriveWireGuardPublicFromPrivate(priv)
	if err != nil {
		t.Fatalf("deriveWireGuardPublicFromPrivate: %v", err)
	}
	if derived != pub {
		t.Errorf("derived pubkey doesn't match: %q vs %q", derived, pub)
	}
}

func TestDeriveWireGuardPublicFromPrivate_Invalid(t *testing.T) {
	_, err := deriveWireGuardPublicFromPrivate("not-base64!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestDeriveWireGuardPublicFromPrivate_WrongLength(t *testing.T) {
	_, err := deriveWireGuardPublicFromPrivate(base64.StdEncoding.EncodeToString([]byte("short")))
	if err == nil {
		t.Error("expected error for wrong length")
	}
}

// ─── GenerateWireGuardKeypair ─────────────────────────────────────────────────

func TestGenerateWireGuardKeypair_Valid(t *testing.T) {
	priv, pub, err := GenerateWireGuardKeypair()
	if err != nil {
		t.Fatalf("GenerateWireGuardKeypair: %v", err)
	}
	if priv == "" || pub == "" {
		t.Fatal("empty keys")
	}

	// Verify private key can be decoded (32 bytes)
	privBytes, err := base64.StdEncoding.DecodeString(priv)
	if err != nil {
		t.Fatalf("decode priv: %v", err)
	}
	if len(privBytes) != 32 {
		t.Errorf("priv length = %d", len(privBytes))
	}

	// Verify public key matches private
	var privArr, pubArr [32]byte
	copy(privArr[:], privBytes)
	curve25519.ScalarBaseMult(&pubArr, &privArr)
	expectedPub := base64.StdEncoding.EncodeToString(pubArr[:])
	if pub != expectedPub {
		t.Error("public key doesn't match private")
	}
}

func TestGenerateWireGuardKeypair_Unique(t *testing.T) {
	p1, b1, _ := GenerateWireGuardKeypair()
	p2, b2, _ := GenerateWireGuardKeypair()
	if p1 == p2 || b1 == b2 {
		t.Error("keys should be unique")
	}
}

// ─── GenerateStableTUICUserCreds ──────────────────────────────────────────────

func TestGenerateStableTUICUserCreds(t *testing.T) {
	uuid, password := GenerateStableTUICUserCreds()
	if uuid == "" || password == "" {
		t.Fatal("empty creds")
	}
	if uuid != password {
		t.Error("TUIC UUID should equal password")
	}
	if len(uuid) != 36 {
		t.Errorf("UUID length = %d", len(uuid))
	}
	// Must be valid UUID format
	parts := strings.Split(uuid, "-")
	if len(parts) != 5 {
		t.Errorf("UUID format wrong: %s", uuid)
	}
}

// ─── generateStableUUID ───────────────────────────────────────────────────────

func TestGenerateStableUUID(t *testing.T) {
	u1 := generateStableUUID()
	u2 := generateStableUUID()
	if u1 == "" || u2 == "" {
		t.Fatal("empty UUID")
	}
	if u1 == u2 {
		t.Error("UUIDs should be unique")
	}
	if len(u1) != 36 {
		t.Errorf("UUID length = %d", len(u1))
	}
}

// ─── BuildXHTTPTransportInboundForStandalone ──────────────────────────────────

func TestBuildXHTTPTransportInboundForStandalone(t *testing.T) {
	preset := ConnectionPreset{
		XHTTP: &XHTTPPreset{
			Methods: []string{"GET"},
			Paths:   []string{"/api/custom"},
			Hosts:   []string{"myhost.com"},
			Headers: map[string][]string{"X-Custom": {"yes"}},
		},
	}
	out := BuildXHTTPTransportInboundForStandalone(443, "uuid-1", "priv-key", "shortid", "myhost.com", &preset)

	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["type"] != "vless" {
		t.Errorf("type = %v", m["type"])
	}
	if m["tag"] != "transport-in" {
		t.Errorf("tag = %v", m["tag"])
	}
	if m["listen_port"].(float64) != 443 {
		t.Errorf("port = %v", m["listen_port"])
	}
}

func TestBuildXHTTPTransportInboundForStandalone_NoPreset(t *testing.T) {
	preset := ConnectionPreset{} // empty
	// shortID must be at least 4 chars for the fallback path generation
	out := BuildXHTTPTransportInboundForStandalone(8443, "uuid-2", "pk", "abcdef1234567890", "host.com", &preset)

	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["type"] != "vless" {
		t.Error("should be vless type even without preset")
	}
}

// ─── BuildAmneziaSection ─────────────────────────────────────────────────────

func TestBuildAmneziaSection(t *testing.T) {
	awg := &AWGPreset{
		JC: 4, JMIN: 40, JMAX: 70,
		S1: 10, S2: 20, H1: 1, H2: 2, H3: 3, H4: 4,
	}
	preset := ConnectionPreset{
		CPSLevel:   2,
		AWGMimicry: "quic",
	}
	section := BuildAmneziaSection(awg, &preset)
	if section == nil {
		t.Fatal("section is nil")
	}
	if section.JC != 4 || section.JMIN != 40 || section.JMAX != 70 {
		t.Errorf("basic fields wrong: %+v", section)
	}
	// With CPS level 2 + quic, I1 should be set
	if section.I1 == "" {
		t.Error("I1 should be set for CPS level 2")
	}
}

func TestBuildAmneziaSection_NoCPS(t *testing.T) {
	awg := &AWGPreset{JC: 4, JMIN: 40, JMAX: 70}
	var preset ConnectionPreset // no CPS
	section := BuildAmneziaSection(awg, &preset)
	if section.I1 != "" {
		t.Error("I1 should be empty for CPS level 0")
	}
}

func TestBuildAmneziaSection_NilPreset(t *testing.T) {
	awg := &AWGPreset{JC: 4, JMIN: 40, JMAX: 70}
	section := BuildAmneziaSection(awg, nil)
	if section == nil {
		t.Fatal("section is nil")
	}
	if section.JC != 4 {
		t.Errorf("JC = %d", section.JC)
	}
}

// ─── GenerateSelfSignedCert ───────────────────────────────────────────────────

func TestGenerateSelfSignedCert(t *testing.T) {
	cert, key, err := GenerateSelfSignedCert("test.example.com")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert: %v", err)
	}
	if cert == "" || key == "" {
		t.Fatal("empty cert or key")
	}
	if !strings.Contains(cert, "-----BEGIN CERTIFICATE-----") {
		t.Error("cert missing PEM header")
	}
	if !strings.Contains(cert, "-----END CERTIFICATE-----") {
		t.Error("cert missing PEM footer")
	}
	if !strings.Contains(key, "-----BEGIN PRIVATE KEY-----") {
		t.Error("key missing PEM header")
	}
	if !strings.Contains(key, "-----END PRIVATE KEY-----") {
		t.Error("key missing PEM footer")
	}
}

func TestGenerateSelfSignedCert_DifferentHosts(t *testing.T) {
	c1, _, _ := GenerateSelfSignedCert("a.example.com")
	c2, _, _ := GenerateSelfSignedCert("b.example.com")
	if c1 == c2 {
		t.Error("certs for different hosts should differ")
	}
}

// ─── extractHost ──────────────────────────────────────────────────────────────

func TestExtractHost(t *testing.T) {
	tests := []struct{ input, want string }{
		{"1.2.3.4:22", "1.2.3.4"},
		{"example.com:2222", "example.com"},
		{"192.168.1.1", "192.168.1.1"},
		{"no-port", "no-port"},
		// Bracketed IPv6 with port: brackets must be stripped.
		{"[2001:db8::1]:22", "2001:db8::1"},
		{"[::1]:22", "::1"},
		// Bare IPv6 without a port must round-trip whole (not split at last ':').
		{"2001:db8::1", "2001:db8::1"},
		{"::1", "::1"},
	}
	for _, tt := range tests {
		got := extractHost(tt.input)
		if got != tt.want {
			t.Errorf("extractHost(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ─── safeSNILabel ─────────────────────────────────────────────────────────────

func TestSafeSNILabel(t *testing.T) {
	tests := []struct{ input, want string }{
		{"www.microsoft.com", "www"},
		{"cloudflare.com", "cloudflare"},
		{"short", "short"},
		{"very-long-hostname-that-exceeds-16-chars", "very-long-hostna"},
	}
	for _, tt := range tests {
		got := safeSNILabel(tt.input)
		if got != tt.want {
			t.Errorf("safeSNILabel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ─── IntRange.Value ───────────────────────────────────────────────────────────

func TestIntRange_Value(t *testing.T) {
	r := IntRange{Min: 10, Max: 10}
	if r.Value() != 10 {
		t.Errorf("equal min/max: got %d", r.Value())
	}

	r2 := IntRange{Min: 5, Max: 10}
	for i := 0; i < 20; i++ {
		v := r2.Value()
		if v < 5 || v > 10 {
			t.Errorf("Value out of range: %d", v)
		}
	}
}

// ─── Backend interface check ──────────────────────────────────────────────────

func TestBackendMethods(t *testing.T) {
	// Verify key generation functions are callable
	_, _, err := GenerateWireGuardKeypair()
	if err != nil {
		t.Fatalf("GenerateWireGuardKeypair: %v", err)
	}

	uuid, pass := GenerateStableTUICUserCreds()
	if uuid == "" || pass == "" {
		t.Fatal("GenerateStableTUICUserCreds returned empty")
	}
}

// ─── Hop params integrity ─────────────────────────────────────────────────────

func TestHopParams_Clamping(t *testing.T) {
	// Generate a keypair and verify clamping works (X25519 requirement)
	priv, pub, _ := GenerateWireGuardKeypair()
	privBytes, _ := base64.StdEncoding.DecodeString(priv)

	// Clamping: priv[0] &= 248; priv[31] &= 127; priv[31] |= 64
	if privBytes[0]&^248 != 0 {
		t.Error("private key not properly clamped (byte 0)")
	}
	if privBytes[31]&128 != 0 {
		t.Error("private key not properly clamped (byte 31 high bit)")
	}
	if privBytes[31]&64 == 0 {
		t.Error("private key not properly clamped (byte 31 bit 6)")
	}

	// Verify this clamped key produces the same public key
	var privArr, pubArr [32]byte
	copy(privArr[:], privBytes)
	curve25519.ScalarBaseMult(&pubArr, &privArr)
	expectedPub := base64.StdEncoding.EncodeToString(pubArr[:])
	if pub != expectedPub {
		t.Error("clamped key doesn't match public")
	}
}

// ─── NewApplier ───────────────────────────────────────────────────────────────

func TestNewApplier(t *testing.T) {
	a := NewApplier(nil)
	if a == nil {
		t.Fatal("NewApplier returned nil")
	}
	// factory can be nil — NewApplier doesn't validate
}

// ─── Build XHTTP outbound with invalid keys ───────────────────────────────────

func TestBuildXHTTPTransportOutbound_InvalidKey(t *testing.T) {
	p := &hopParams{
		PrivateKey: "!!!bad-key!!!",
		UUID:       "uuid",
		ShortID:    "shortid",
		Port:       443,
		ServerName: "test.com",
	}
	preset := ConnectionPreset{
		XHTTP: &XHTTPPreset{Methods: []string{"GET"}, Paths: []string{"/"}, Hosts: []string{"test.com"}},
	}
	_, err := buildXHTTPTransportOutbound(p, "1.2.3.4", "test", &preset)
	if err == nil {
		t.Error("expected error for invalid key")
	}
}

// ─── ResolveServerName ────────────────────────────────────────────────────────

func TestResolveServerName(t *testing.T) {
	tests := []struct {
		name   string
		preset ConnectionPreset
		want   string
	}{
		{"reality", ConnectionPreset{Reality: &RealityPreset{ServerNames: []string{"reality.com"}}}, "reality.com"},
		{"xhttp", ConnectionPreset{XHTTP: &XHTTPPreset{Hosts: []string{"xhttp.com"}}}, "xhttp.com"},
		{"both", ConnectionPreset{Reality: &RealityPreset{ServerNames: []string{"r.com"}}, XHTTP: &XHTTPPreset{Hosts: []string{"x.com"}}}, "r.com"},
		{"none", ConnectionPreset{}, "www.microsoft.com"},
	}
	for _, tt := range tests {
		got := ResolveServerName(&tt.preset)
		if got != tt.want {
			t.Errorf("ResolveServerName(%s) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// ─── BuildDNSWithDetour ──────────────────────────────────────────────────────

func TestBuildDNSWithDetour(t *testing.T) {
	dns := BuildDNSWithDetour("chain-out", []string{".ru", ".local"})
	if dns == nil {
		t.Fatal("dns is nil")
	}
	if len(dns.Servers) != 2 {
		t.Errorf("servers = %d, want 2", len(dns.Servers))
	}
	if dns.Servers[0].Detour != "chain-out" {
		t.Errorf("first server detour = %s", dns.Servers[0].Detour)
	}
	if dns.Servers[1].Detour != "direct-out" {
		t.Errorf("second server detour = %s", dns.Servers[1].Detour)
	}
	if len(dns.Rules) != 1 {
		t.Errorf("rules = %d, want 1", len(dns.Rules))
	}
	if dns.Final != "dns-chain" {
		t.Errorf("final = %s", dns.Final)
	}
}

func TestBuildDNSWithDetour_NoDomains(t *testing.T) {
	dns := BuildDNSWithDetour("out", nil)
	if dns == nil {
		t.Fatal("dns is nil")
	}
	if len(dns.Rules) != 0 {
		t.Errorf("rules = %d, want 0 for empty domains", len(dns.Rules))
	}
}

// ─── BuildDNSSection ─────────────────────────────────────────────────────────

func TestBuildDNSSection(t *testing.T) {
	dns := BuildDNSSection("my-chain-out")
	if dns == nil {
		t.Fatal("dns is nil")
	}
	if len(dns.Servers) != 2 {
		t.Errorf("servers = %d", len(dns.Servers))
	}
	if dns.Final != "dns-remote" {
		t.Errorf("final = %s", dns.Final)
	}
	// Should have domain rules for ru, su, etc.
	if len(dns.Rules) < 1 {
		t.Error("expected domain rules")
	}
}

// ─── BuildStrategyOutbound ────────────────────────────────────────────────────

func TestBuildStrategyOutbound_EmptyTags(t *testing.T) {
	out := BuildStrategyOutbound("urltest", nil)
	if out != nil {
		t.Error("should return nil for empty tags")
	}
	out2 := BuildStrategyOutbound("urltest", []string{})
	if out2 != nil {
		t.Error("should return nil for empty slice")
	}
}

func TestBuildStrategyOutbound_UnknownStrategy(t *testing.T) {
	out := BuildStrategyOutbound("unknown", []string{"tag1"})
	if out != nil {
		t.Error("should return nil for unknown strategy")
	}
}

// ─── NeedsBlock ───────────────────────────────────────────────────────────────

func TestNeedsBlock_WithBlockRules(t *testing.T) {
	preset := ConnectionPreset{}
	preset.Routing.BlockGeoSite = []string{"category-ads"}
	roles := []chainRole{{Preset: preset}}
	if !needsBlock(roles) {
		t.Error("needsBlock should be true when preset has block rules")
	}
}

func TestNeedsBlock_NoBlockRules(t *testing.T) {
	preset := ConnectionPreset{}
	roles := []chainRole{{Preset: preset}}
	if needsBlock(roles) {
		t.Error("needsBlock should be false without block rules")
	}
}

func TestNeedsBlock_EmptyRoles(t *testing.T) {
	if needsBlock(nil) {
		t.Error("needsBlock should be false for nil")
	}
	if needsBlock([]chainRole{}) {
		t.Error("needsBlock should be false for empty")
	}
}

// ─── Config type validation ──────────────────────────────────────────────────

func TestConfigTagTypeRoundTrip(t *testing.T) {
	// Verify VLESSInbound and VLESSOutbound round-trip through JSON
	cfg := config.SingboxConfig{
		Log: &config.LogOptions{Level: "info"},
		Inbounds: []json.RawMessage{
			json.RawMessage(`{"type":"vless","tag":"test-in","listen":"0.0.0.0","listen_port":443,"users":[{"name":"u","uuid":"uuid"}]}`),
		},
		Outbounds: []json.RawMessage{
			json.RawMessage(`{"type":"direct","tag":"direct-out"}`),
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal SingboxConfig: %v", err)
	}
	var back config.SingboxConfig
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal SingboxConfig: %v", err)
	}
	if len(back.Inbounds) != 1 || len(back.Outbounds) != 1 {
		t.Error("round-trip lost data")
	}
}
