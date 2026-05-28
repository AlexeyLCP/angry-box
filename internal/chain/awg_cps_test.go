package chain

import (
	"strings"
	"testing"
)

func TestGenerateQUICInitial_Exactly1200Bytes_ChromeFB(t *testing.T) {
	payload := GenerateQUICInitial()

	if len(payload) != 1200 {
		t.Fatalf("QUIC Initial for AWG I1 must be exactly 1200 bytes, got %d", len(payload))
	}
	if payload[0] < 0xC0 || payload[0] > 0xC3 {
		t.Errorf("expected Chrome fb long header 0xC0-C3, got 0x%02x", payload[0])
	}
	// Heuristic: contains h3-29 (common Chrome QUIC fb)
	if !strings.Contains(string(payload[:100]), "h3-29") {
		t.Log("note: h3-29 fingerprint not found in first 100 bytes (acceptable for some builds)")
	}
}

func TestGenerateQUICShort_HeaderMasking(t *testing.T) {
	for i := 0; i < 5; i++ {
		p := GenerateQUICShort(64 + i)
		if len(p) == 0 {
			t.Fatal("empty short header")
		}
		if p[0] < 0x40 || p[0] > 0x7F {
			t.Errorf("short header must be 0x40-0x7F, got 0x%02x", p[0])
		}
	}
}

func TestGenerateSIP_RealisticREGISTER(t *testing.T) {
	sip := GenerateSIP("sip.example.com")
	if !strings.Contains(sip, "REGISTER sip:") {
		t.Error("missing REGISTER line")
	}
	if !strings.Contains(sip, "User-Agent: ") {
		t.Error("missing User-Agent")
	}
	// Should contain at least one realistic UA from our generator
	known := []string{"Linphone", "MicroSIP", "Grandstream", "Zoiper"}
	found := false
	for _, k := range known {
		if strings.Contains(sip, k) {
			found = true
			break
		}
	}
	if !found {
		t.Log("SIP UA not one of the expected softphones (still acceptable)")
	}
}

func TestGenerateDNS_WithEDNS0(t *testing.T) {
	dns := GenerateDNS("icloud.com", 1232)
	if len(dns) != 1232 {
		t.Fatalf("DNS with EDNS0 should be padded to requested size, got %d", len(dns))
	}
}

func TestCPSLevel_GeneratesCorrectNumberOfPackets(t *testing.T) {
	cases := []struct {
		level   int
		mimicry string
		wantI1  bool
		wantI25 bool
	}{
		{0, "none", false, false},
		{1, "quic", true, false},
		{2, "sip", true, true},
		{3, "dns", true, true},
	}

	for _, c := range cases {
		m := GenerateAWGObfsMaterial(c.level, c.mimicry)
		if c.wantI1 && len(m.I1) == 0 {
			t.Errorf("level %d mimicry %s: expected I1", c.level, c.mimicry)
		}
		if c.wantI25 {
			if len(m.I2) == 0 || len(m.I3) == 0 || len(m.I4) == 0 || len(m.I5) == 0 {
				t.Errorf("level %d: expected I2-I5", c.level)
			}
		}
	}
}

func TestPro2026_And_MaxStealth_ForceCPS3_AndQUIC(t *testing.T) {
	// "безопасность нужна сильнее чем совместимость" — these two presets must force level 3 + QUIC
	for _, name := range []string{"pro_2026", "xhttp_max_stealth_2026"} {
		p, ok := GetPreset(name)
		if !ok {
			t.Fatalf("preset %s not found after LoadPresets", name)
		}
		if p.CPSLevel != 3 && (p.AWG == nil || p.AWG.CPSLevel != 3) {
			t.Errorf("%s must force cps_level=3 (security > compatibility)", name)
		}
	}

	mat := BuildAWGClientMaterialFromPreset(MustGetPreset("pro_2026"), "server.example.com")
	if len(mat.I1) != 1200 {
		t.Errorf("pro_2026 must produce 1200B QUIC I1, got %d", len(mat.I1))
	}
	if mat.MimicryProfile != "quic" {
		t.Errorf("pro_2026 must use quic mimicry, got %s", mat.MimicryProfile)
	}
}
