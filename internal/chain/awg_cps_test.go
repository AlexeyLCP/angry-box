package chain

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestGenerateQUICInitial_Exactly1200Bytes(t *testing.T) {
	for i := 0; i < 5; i++ {
		h := GenerateQUICInitial("")
		b, err := hex.DecodeString(h)
		if err != nil {
			t.Fatalf("invalid hex: %v", err)
		}
		if len(b) != 1200 {
			t.Errorf("QUIC Initial must be exactly 1200 bytes (Chrome fingerprint), got %d", len(b))
		}
		// First byte should be 0xC0 or 0xC3 (Long Header Initial, Chrome-like)
		if b[0] != 0xC0 && b[0] != 0xC3 {
			t.Errorf("expected fb 0xC0/C3, got 0x%02x", b[0])
		}
	}
}

func TestGenerateSIP_HasRealisticHeaders(t *testing.T) {
	s := GenerateSIP()
	if !strings.Contains(s, "User-Agent:") {
		t.Error("SIP REGISTER missing User-Agent (DPI will flag as scanner)")
	}
	if !strings.Contains(s, "Allow: INVITE, ACK") {
		t.Error("SIP REGISTER missing full Allow list (incomplete SIP = suspicious)")
	}
	if !strings.Contains(s, "REGISTER sip:") {
		t.Error("not a REGISTER")
	}
}

func TestGenerateDNS_HasEDNS0(t *testing.T) {
	h := GenerateDNS("example.com")
	b, _ := hex.DecodeString(h)
	// Last 10 bytes should contain OPT RR (00 00 29 ...)
	if len(b) < 20 {
		t.Fatal("dns too short")
	}
	foundOPT := false
	for i := 0; i < len(b)-4; i++ {
		if b[i] == 0x00 && b[i+1] == 0x00 && b[i+2] == 0x29 {
			foundOPT = true
			break
		}
	}
	if !foundOPT {
		t.Error("DNS query missing EDNS0 OPT-RR (0x0029) — legacy resolver pattern")
	}
}

func TestGenerateCPS_Levels(t *testing.T) {
	i1, i2, i3, _, _, m := GenerateCPS(3, "quic")
	if m != "quic" {
		t.Errorf("expected quic, got %s", m)
	}
	if i1 == "" {
		t.Error("I1 must be present for level 3 quic")
	}
	if i2 == "" || i3 == "" {
		t.Error("I2/I3 should be present for full level 3")
	}

	i1, i2, _, _, _, _ = GenerateCPS(1, "sip")
	if i1 == "" {
		t.Error("SIP level 1 must produce I1")
	}
	if i2 != "" {
		t.Error("SIP level 1 should not produce I2 (large packet)")
	}

	ii1, _, _, _, _, _ := GenerateCPS(0, "")
	if ii1 != "" {
		t.Error("level 0 must produce empty I*")
	}
}

func TestEnforceAWGInvariants_JminJmaxAndS1S2(t *testing.T) {
	jc, jmin, jmax, s1, s2, s3, s4, h1, h2, h3, h4 := 5, 300, 200, 100, 150, 10, 5, 100, 600000000, 1200000000, 1800000000
	EnforceAWGInvariants(&jc, &jmin, &jmax, &s1, &s2, &s3, &s4, &h1, &h2, &h3, &h4)
	if jmin >= jmax {
		t.Error("jmin must be < jmax after enforce")
	}
	diff := (s1 + 56) - s2
	if diff < 0 {
		diff = -diff
	}
	if diff < 10 {
		t.Errorf("S1+56 vs S2 gap too small (%d), violates AWG manual + pumbaX rule", diff)
	}
}

func TestFormatI1_DNSUsesR2Prefix(t *testing.T) {
	s := FormatI1("dns", "deadbeef")
	if !strings.HasPrefix(s, "<r 2><b 0x") {
		t.Errorf("dns I1 must use <r 2> randomizer prefix, got %s", s)
	}
}
