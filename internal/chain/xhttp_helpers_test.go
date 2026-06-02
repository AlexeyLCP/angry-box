package chain

import (
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// ─── RandRange ────────────────────────────────────────────────────────────────

func TestRandRange_SameValue(t *testing.T) {
	for i := 0; i < 10; i++ {
		v := RandRange(5, 5)
		if v != 5 {
			t.Errorf("RandRange(5,5) = %d", v)
		}
	}
}

func TestRandRange_Range(t *testing.T) {
	for i := 0; i < 50; i++ {
		v := RandRange(10, 20)
		if v < 10 || v > 20 {
			t.Errorf("RandRange(10,20) = %d out of range", v)
		}
	}
}

func TestRandRange_Swapped(t *testing.T) {
	// Swapped min/max should still work
	for i := 0; i < 50; i++ {
		v := RandRange(20, 10)
		if v < 10 || v > 20 {
			t.Errorf("RandRange(20,10) = %d out of range", v)
		}
	}
}

func TestRandRange_ZeroRange(t *testing.T) {
	v := RandRange(0, 0)
	if v != 0 {
		t.Errorf("RandRange(0,0) = %d", v)
	}
}

// ─── GeneratePadding ──────────────────────────────────────────────────────────

func TestGeneratePadding(t *testing.T) {
	for i := 0; i < 10; i++ {
		p := GeneratePadding(10, 20)
		if len(p) < 10 || len(p) > 20 {
			t.Errorf("GeneratePadding(10,20) length = %d", len(p))
		}
	}
	// Same min/max
	p := GeneratePadding(15, 15)
	if len(p) != 15 {
		t.Errorf("GeneratePadding(15,15) length = %d", len(p))
	}
}

// ─── GenerateRealisticHeaders ─────────────────────────────────────────────────

func TestGenerateRealisticHeaders(t *testing.T) {
	h := GenerateRealisticHeaders("example.com")
	if h == nil {
		t.Fatal("headers is nil")
	}
	// Must have standard headers
	if _, ok := h["User-Agent"]; !ok {
		t.Error("missing User-Agent")
	}
	if _, ok := h["Accept"]; !ok {
		t.Error("missing Accept")
	}
	// Content-Type is added by the transport builder, not by GenerateRealisticHeaders itself
	// Referer should contain host
	if ref, ok := h["Referer"]; ok {
		if len(ref) > 0 && !strings.Contains(ref[0], "example.com") {
			t.Errorf("Referer missing host: %s", ref[0])
		}
	}
}

func TestGenerateRealisticHeaders_EmptyHost(t *testing.T) {
	h := GenerateRealisticHeaders("")
	if h == nil {
		t.Fatal("headers is nil")
	}
	if _, ok := h["User-Agent"]; !ok {
		t.Error("missing User-Agent")
	}
}

// ─── GenerateXMUX ─────────────────────────────────────────────────────────────

func TestGenerateXMUX(t *testing.T) {
	xmux := GenerateXMUX()
	if xmux == nil {
		t.Fatal("xmux is nil")
	}
	if !xmux.Enabled {
		t.Error("XMUX should be enabled")
	}
	if xmux.MaxConcurrency == "" {
		t.Error("MaxConcurrency is empty")
	}
	if !strings.Contains(xmux.MaxConcurrency, "-") {
		t.Errorf("MaxConcurrency should be a range: %s", xmux.MaxConcurrency)
	}
	if xmux.KeepAlive != "30s" {
		t.Errorf("KeepAlive = %s", xmux.KeepAlive)
	}
}

// ─── ApplyXHTTPObfuscation ────────────────────────────────────────────────────

func TestApplyXHTTPObfuscation_WithPreset(t *testing.T) {
	transport := &config.TransportOptions{
		Type: "http",
		Host: []string{"test.com"},
		Path: "/api",
	}
	preset := &XHTTPPreset{
		Hosts:   []string{"test.com"},
		Methods: []string{"POST"},
		Paths:   []string{"/api"},
	}
	ApplyXHTTPObfuscation(transport, preset)
	// With headers preset, transport.Headers should be set to realistic ones
	if len(transport.Headers) == 0 {
		t.Error("transport.Headers should be populated")
	}
}

func TestApplyXHTTPObfuscation_NilTransport(t *testing.T) {
	// Should not panic
	ApplyXHTTPObfuscation(nil, &XHTTPPreset{})
}

func TestApplyXHTTPObfuscation_NilPreset(t *testing.T) {
	transport := &config.TransportOptions{Type: "http"}
	ApplyXHTTPObfuscation(transport, nil)
	// Should not modify
}

func TestApplyXHTTPObfuscation_NilBoth(t *testing.T) {
	// Should not panic
	ApplyXHTTPObfuscation(nil, nil)
}

// ─── GenerateXHTTPMode ────────────────────────────────────────────────────────

func TestGenerateXHTTPMode(t *testing.T) {
	tests := []struct {
		level int
		want  string
	}{
		{0, "packet-up"},
		{1, "packet-up"},
		{2, "auto"},
		{3, "stream-up"},
		{4, "stream-up"},
		{5, "stream-up"},
	}
	for _, tt := range tests {
		got := GenerateXHTTPMode(tt.level)
		if got != tt.want {
			t.Errorf("GenerateXHTTPMode(%d) = %q, want %q", tt.level, got, tt.want)
		}
	}
}

// ─── GenerateXHTTPExtra ───────────────────────────────────────────────────────

func TestGenerateXHTTPExtra(t *testing.T) {
	// Level 0: basic
	extra := GenerateXHTTPExtra(0, "test.com")
	if extra == nil {
		t.Fatal("extra is nil")
	}
	if extra.Mode == "" {
		t.Error("Mode is empty")
	}
	if extra.XPaddingBytes == "" {
		t.Error("XPaddingBytes is empty")
	}
	if extra.XMUX != nil {
		t.Error("XMUX should be nil for level 0")
	}
}

func TestGenerateXHTTPExtra_HighStealth(t *testing.T) {
	// Level 2: should include XMUX
	extra := GenerateXHTTPExtra(2, "test.com")
	if extra.XMUX == nil {
		t.Error("XMUX should be set for level 2")
	}
	if !extra.XMUX.Enabled {
		t.Error("XMUX should be enabled")
	}
}

func TestGenerateXHTTPExtra_MaxStealth(t *testing.T) {
	// Level 3: should include fragmentation
	extra := GenerateXHTTPExtra(3, "test.com")
	if extra.Fragmentation == nil {
		t.Error("Fragmentation should be set for level 3")
	}
	if !extra.Fragmentation.Enabled {
		t.Error("Fragmentation should be enabled")
	}
}

// ─── GenerateRealisticPreamble ────────────────────────────────────────────────

func TestGenerateRealisticPreamble(t *testing.T) {
	preamble := GenerateRealisticPreamble("example.com")
	if len(preamble) != 3 {
		t.Errorf("preamble length = %d, want 3", len(preamble))
	}
	for _, p := range preamble {
		if !strings.Contains(p, "example.com") {
			t.Errorf("preamble entry missing host: %s", p)
		}
		if !strings.HasPrefix(p, "https://") {
			t.Errorf("preamble entry not https: %s", p)
		}
	}
}

// ─── clamped ──────────────────────────────────────────────────────────────────

func TestClamp(t *testing.T) {
	tests := []struct{ v, min, max, want int }{
		{5, 0, 10, 5},
		{-5, 0, 10, 0},
		{15, 0, 10, 10},
		{0, 0, 3, 0},
		{3, 0, 3, 3},
	}
	for _, tt := range tests {
		got := clamp(tt.v, tt.min, tt.max)
		if got != tt.want {
			t.Errorf("clamp(%d,%d,%d) = %d, want %d", tt.v, tt.min, tt.max, got, tt.want)
		}
	}
}

// ─── randUint32 ───────────────────────────────────────────────────────────────

func TestRandUint32(t *testing.T) {
	// Just verify it doesn't panic
	for i := 0; i < 10; i++ {
		v := randUint32()
		_ = v
	}
}

// ─── Host extraction edge cases ───────────────────────────────────────────────

func TestExtractHost_EdgeCases(t *testing.T) {
	tests := []struct{ input, want string }{
		{"[::1]:22", "[::1]"},
		{"", ""},
		{"host:port:extra:stuff", "host:port:extra"},
	}
	for _, tt := range tests {
		got := extractHost(tt.input)
		if got != tt.want {
			t.Errorf("extractHost(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ─── AWG material generation edge cases ──────────────────────────────────────

func TestGenerateAWGObfsMaterial_Level0(t *testing.T) {
	m := GenerateAWGObfsMaterial(0, "quic")
	if len(m.I1) != 0 {
		t.Error("I1 should be empty for level 0")
	}
}

func TestGenerateAWGObfsMaterial_None(t *testing.T) {
	m := GenerateAWGObfsMaterial(3, "none")
	if len(m.I1) != 0 {
		t.Error("I1 should be empty for mimicry 'none'")
	}
}

func TestGenerateAWGObfsMaterial_SIP(t *testing.T) {
	m := GenerateAWGObfsMaterial(2, "sip")
	if len(m.I1) == 0 {
		t.Error("I1 should not be empty for SIP level 2")
	}
	if !strings.Contains(string(m.I1), "REGISTER") {
		t.Error("SIP I1 should contain REGISTER")
	}
}

func TestGenerateAWGObfsMaterial_DNS(t *testing.T) {
	m := GenerateAWGObfsMaterial(2, "dns")
	if len(m.I1) == 0 {
		t.Error("I1 should not be empty for DNS level 2")
	}
}

func TestGenerateAWGObfsMaterial_Level1(t *testing.T) {
	// Level 1: only I1, no I2-I5
	m := GenerateAWGObfsMaterial(1, "quic")
	if len(m.I1) == 0 {
		t.Error("I1 should be set for level 1")
	}
	// With level 1, I2-I5 should NOT be set
	if len(m.I2) != 0 || len(m.I3) != 0 || len(m.I4) != 0 || len(m.I5) != 0 {
		t.Error("I2-I5 should be empty for level 1")
	}
}

func TestGenerateAWGObfsMaterial_Level2(t *testing.T) {
	m := GenerateAWGObfsMaterial(2, "quic")
	if len(m.I1) == 0 {
		t.Error("I1 should be set")
	}
	if len(m.I2) == 0 {
		t.Error("I2 should be set for level 2")
	}
}

func TestGenerateAWGObfsMaterial_DefaultMimicry(t *testing.T) {
	// Unknown mimicry should fall back to QUIC
	m := GenerateAWGObfsMaterial(2, "unknown_mimicry")
	if len(m.I1) == 0 {
		t.Error("I1 should be set (fallback to QUIC)")
	}
}

func TestBuildAmneziaSection_WithCPSInPreset(t *testing.T) {
	awg := &AWGPreset{
		JC: 3, JMIN: 30, JMAX: 60,
		S1: 5, S2: 10, H1: 1, H2: 2, H3: 3, H4: 4,
	}
	// CPS from top-level preset, not awg sub-field
	preset := ConnectionPreset{CPSLevel: 2, AWGMimicry: "quic"}
	section := BuildAmneziaSection(awg, &preset)
	if section.I1 == "" {
		t.Error("I1 should be set when CPS is in preset")
	}
}

func TestBuildAmneziaSection_WithCPSInAWG(t *testing.T) {
	awg := &AWGPreset{
		JC: 3, JMIN: 30, JMAX: 60,
		CPSLevel: 2, Mimicry: "quic",
		S1: 5, S2: 10, H1: 1, H2: 2, H3: 3, H4: 4,
	}
	// CPS from awg sub-field, no top-level CPS
	preset := ConnectionPreset{}
	section := BuildAmneziaSection(awg, &preset)
	if section.I1 == "" {
		t.Error("I1 should be set when CPS is in AWG preset")
	}
}

// ─── BuildAWGClientMaterialFromPreset ─────────────────────────────────────────

func TestBuildAWGClientMaterialFromPreset_Pro2026(t *testing.T) {
	preset := ConnectionPreset{Name: "pro_2026"}
	mat := BuildAWGClientMaterialFromPreset(preset, "server1")
	if mat.CPSLevel != 3 {
		t.Errorf("pro_2026 should force CPS level 3, got %d", mat.CPSLevel)
	}
	if mat.MimicryProfile != "quic" {
		t.Errorf("pro_2026 should force quic, got %s", mat.MimicryProfile)
	}
}

func TestBuildAWGClientMaterialFromPreset_MaxStealth(t *testing.T) {
	preset := ConnectionPreset{Name: "xhttp_max_stealth_2026"}
	mat := BuildAWGClientMaterialFromPreset(preset, "server1")
	if mat.CPSLevel != 3 {
		t.Errorf("max_stealth should force CPS level 3, got %d", mat.CPSLevel)
	}
}

func TestBuildAWGClientMaterialFromPreset_NoCPS(t *testing.T) {
	preset := ConnectionPreset{Name: "basic"}
	mat := BuildAWGClientMaterialFromPreset(preset, "server1")
	if mat.CPSLevel != 0 {
		t.Errorf("basic should have CPS level 0, got %d", mat.CPSLevel)
	}
}

// ─── QUIC Initial packet structure ────────────────────────────────────────────

func TestQUICInitial_Structure(t *testing.T) {
	pkt := GenerateQUICInitial()
	if len(pkt) != 1200 {
		t.Fatalf("QUIC Initial length = %d, want 1200", len(pkt))
	}
	// Long header: first byte should have top bits set (0xC0-0xC3 range for Chrome)
	if pkt[0]&0xC0 != 0xC0 {
		t.Errorf("QUIC long header bits wrong: 0x%02x", pkt[0])
	}
	// Version field should be present
	version := uint32(pkt[1])<<24 | uint32(pkt[2])<<16 | uint32(pkt[3])<<8 | uint32(pkt[4])
	if version != 0x00000001 {
		t.Errorf("QUIC version = 0x%08x, want 0x00000001", version)
	}
}

func TestQUICShort_Header(t *testing.T) {
	for i := 0; i < 5; i++ {
		size := 48 + i*20
		pkt := GenerateQUICShort(size)
		if len(pkt) != size {
			t.Errorf("QUIC Short length = %d, want %d", len(pkt), size)
		}
		// Short header: first byte should NOT have top bits set (0x00-0x7F range)
		if pkt[0]&0x80 != 0 {
			t.Errorf("QUIC short header has top bit set: 0x%02x", pkt[0])
		}
	}
}

func TestQUICShort_MinSize(t *testing.T) {
	// Below 32 should be bumped to 32
	pkt := GenerateQUICShort(10)
	if len(pkt) < 32 {
		t.Errorf("QUIC Short min size: got %d", len(pkt))
	}
}

// ─── DNS packet structure ─────────────────────────────────────────────────────

func TestDNS_MinSize(t *testing.T) {
	pkt := GenerateDNS("test.com", 10)
	if len(pkt) < 64 {
		t.Errorf("DNS min size: got %d", len(pkt))
	}
}

func TestDNS_ContainsQuery(t *testing.T) {
	pkt := GenerateDNS("example.com", 1232)
	s := string(pkt)
	if !strings.Contains(s, "example") {
		t.Error("DNS packet should contain query name")
	}
	if !strings.Contains(s, "com") {
		t.Error("DNS packet should contain query TLD")
	}
}

// ─── randInt ──────────────────────────────────────────────────────────────────

func TestRandInt_Range(t *testing.T) {
	for i := 0; i < 30; i++ {
		v := randInt(10, 20)
		if v < 10 || v > 20 {
			t.Errorf("randInt(10,20) = %d", v)
		}
	}
}

// ─── XHTTP extra edge cases ──────────────────────────────────────────────────

func TestXHTTPExtra_Level1(t *testing.T) {
	extra := GenerateXHTTPExtra(1, "test.com")
	if extra.XMUX != nil {
		t.Error("XMUX should be nil for level 1")
	}
	if extra.Fragmentation != nil {
		t.Error("Fragmentation should be nil for level 1")
	}
}
