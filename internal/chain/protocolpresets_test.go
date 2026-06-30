package chain

import "testing"

func TestGetProtocolPresetsCatalog_Filled(t *testing.T) {
	c := GetProtocolPresetsCatalog()
	if len(c.RealitySNIDomains) != 11 {
		t.Errorf("expected 11 REALITY SNI domains, got %d", len(c.RealitySNIDomains))
	}
	if len(c.RealityFingerprints) != 10 {
		t.Errorf("expected 10 fingerprints, got %d", len(c.RealityFingerprints))
	}
	if _, ok := c.SSCiphers["2022-blake3-aes-128-gcm"]; !ok {
		t.Error("SS_CIPHERS missing 2022-blake3-aes-128-gcm")
	}
	if len(c.ObfuscationLevels) != 4 || c.ObfuscationLevels[0] != "maximum" {
		t.Errorf("obfuscation levels: %v", c.ObfuscationLevels)
	}
}

func TestGetObfuscationLevel_Fallback(t *testing.T) {
	max := GetObfuscationLevel("maximum")
	if max.Transport != "xhttp" || max.Mode != "packet-up" {
		t.Errorf("maximum level wrong: %+v", max)
	}
	// unknown → falls back to maximum
	unk := GetObfuscationLevel("nonexistent")
	if unk.Transport != "xhttp" {
		t.Errorf("unknown level should fall back to maximum, got %+v", unk)
	}
	// case-insensitive
	if GetObfuscationLevel("HIGH").Transport != "xhttp" {
		t.Error("case-insensitive lookup broken")
	}
}

func TestRoutingPresets(t *testing.T) {
	all := GetRoutingPresets("")
	if len(all) == 0 {
		t.Fatal("no routing presets")
	}
	// telegram is in social
	social := GetRoutingPresets("social")
	found := false
	for _, p := range social {
		if p.ID == "telegram" {
			found = true
			if len(p.Domains) == 0 {
				t.Error("telegram preset has no domains")
			}
		}
	}
	if !found {
		t.Error("telegram not found in social category")
	}
	// ads is the only one with action reject
	domains := GetRoutingPresetDomains("ads")
	if domains != nil && len(domains) != 0 {
		// ads has empty domains (action reject)
		t.Errorf("ads preset should have empty domains, got %v", domains)
	}
	if p, ok := GetRoutingPreset("ads"); !ok || p.Action != "reject" {
		t.Error("ads preset should have action=reject")
	}
}

func TestNormalizeDomain(t *testing.T) {
	cases := map[string]string{
		"https://www.cloudflare.com/path": "www.cloudflare.com",
		"http://google.com:80":           "google.com",
		"WWW.Microsoft.COM":              "www.microsoft.com",
		"dl.google.com/something":        "dl.google.com",
		"  ozon.ru  ":                     "ozon.ru",
	}
	for in, want := range cases {
		if got := NormalizeDomain(in); got != want {
			t.Errorf("NormalizeDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsValidDomain(t *testing.T) {
	valid := []string{"www.microsoft.com", "google.com", "dl.google.com", "ozon.ru"}
	for _, d := range valid {
		if !IsValidDomain(d) {
			t.Errorf("%s should be valid", d)
		}
	}
	invalid := []string{"", "not_a_domain", "localhost", "-bad.com", "a.b", "1.2.3"}
	for _, d := range invalid {
		if IsValidDomain(d) {
			t.Errorf("%q should be invalid", d)
		}
	}
}

func TestCaptureQUICSignature_InvalidDomain(t *testing.T) {
	res := CaptureQUICSignature("not_a_domain", 0)
	if res.OK || res.Source != "error" {
		t.Errorf("invalid domain should yield error result, got %+v", res)
	}
	if res.Warning == "" {
		t.Error("invalid domain should have a warning")
	}
}

func TestCaptureQUICSignature_EmptyDomain(t *testing.T) {
	res := CaptureQUICSignature("   ", 0)
	if res.OK {
		t.Error("empty domain should not be OK")
	}
}

func TestCryptoGenerators(t *testing.T) {
	priv, pub, err := GenerateRealityKeypair()
	if err != nil || priv == "" || pub == "" {
		t.Fatalf("reality keypair: priv=%q pub=%q err=%v", priv, pub, err)
	}
	if priv == pub {
		t.Error("priv should differ from pub")
	}
	if len(GenerateRealityShortID())%2 != 0 {
		t.Error("short_id must be even-length hex")
	}
	ids := GenerateRealityShortIDs(8)
	if len(ids) != 8 || ids[0] != "" {
		t.Errorf("short_ids: %v", ids)
	}
	if len(GenerateTrojanPassword()) < 20 {
		t.Error("trojan password too short")
	}
	if GenerateSSPassword("2022-blake3-aes-128-gcm") == "" {
		t.Error("ss password empty")
	}
	if len(GenerateMTProxySecret()) != 32 {
		t.Errorf("mtproxy secret should be 32 hex chars, got %d", len(GenerateMTProxySecret()))
	}
	full, err := MTProxyFullSecret("deadbeefdeadbeefdeadbeefdeadbeef", "disk.yandex.ru")
	if err != nil {
		t.Fatal(err)
	}
	if len(full) < 2+32 || full[:2] != "ee" {
		t.Errorf("mtproxy full secret malformed: %s", full)
	}
	if _, err := MTProxyFullSecret("tooshort", "x"); err == nil {
		t.Error("mtproxy secret should validate length")
	}
}