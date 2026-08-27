package chain

// awg_version_perversion_test.go — pins the per-AWG-version (1.5/2/3) config
// generation contract ported from lucx-ui: 1.5 drops S3/S4 + I1-I5 and uses
// single-int H1-H4; 2 adds S3/S4 + I1-I5 (CPS); 3 adds header protection.
// Server and client emission must agree so the handshake matches.

import (
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// TestAWGVersionAtLeast pins the version ordering used by every emission gate.
func TestAWGVersionAtLeast(t *testing.T) {
	cases := []struct {
		v, floor string
		want     bool
	}{
		{"1.5", model.AWGVersion1x, true},
		{"1.5", model.AWGVersion2, false},
		{"1.5", model.AWGVersion3, false},
		{"2", model.AWGVersion1x, true},
		{"2", model.AWGVersion2, true},
		{"2", model.AWGVersion3, false},
		{"3", model.AWGVersion1x, true},
		{"3", model.AWGVersion2, true},
		{"3", model.AWGVersion3, true},
		{"3.1", model.AWGVersion3, true},
		{"3.1", model.AWGVersion31, true},
		{"3", model.AWGVersion31, false},
		// empty/unknown normalize to "2"
		{"", model.AWGVersion2, true},
		{"", model.AWGVersion3, false},
	}
	for _, c := range cases {
		if got := model.AWGVersionAtLeast(c.v, c.floor); got != c.want {
			t.Errorf("AWGVersionAtLeast(%q,%q)=%v want %v", c.v, c.floor, got, c.want)
		}
	}
}

// TestGenAWGParamsForVersion_HForm pins the H1-H4 form per version: 1.5 uses
// single integers (awg-quick 1.x rejects "lo-hi"), 2/3 use "lo-hi" ranges.
func TestGenAWGParamsForVersion_HForm(t *testing.T) {
	p15 := GenAWGParamsForVersion(AWGProfileStandard, model.AWGVersion1x)
	for i, h := range []string{p15.H1, p15.H2, p15.H3, p15.H4} {
		if strings.Contains(h, "-") {
			t.Errorf("1.5 H%d must be a single int, got range %q", i+1, h)
		}
	}
	p2 := GenAWGParamsForVersion(AWGProfileStandard, model.AWGVersion2)
	for i, h := range []string{p2.H1, p2.H2, p2.H3, p2.H4} {
		if !strings.Contains(h, "-") {
			t.Errorf("2.0 H%d must be a lo-hi range, got %q", i+1, h)
		}
	}
}

// v3Material builds a CPS material with AWG3 header-protection fields merged in.
func v3Material(t *testing.T) AWGObfsMaterial {
	t.Helper()
	m := GenerateAWGObfsMaterialForVersion(2, "quic", model.AWGVersion3)
	a3 := GenerateAWG3Material()
	m.AWG3Mode = true
	m.HeaderProtectionKey = a3.HeaderProtectionKey
	m.ContentPaddingAddition = a3.ContentPaddingAddition
	m.RekeyAfterTime = a3.RekeyAfterTime
	return m
}

// TestRenderAWGQuickConf_PerVersionFields pins the client .conf field set per
// version (mirror lucx-ui filterAwgObfuscation): 1.5 drops S3/S4 + I1-I5; 2 has
// them but no HPK; 3 adds HPK/CPM/RAT.
func TestRenderAWGQuickConf_PerVersionFields(t *testing.T) {
	preset := GetDefaultPreset()

	// 1.5: no S3/S4, no I1-I5, no HPK.
	m15 := GenerateAWGObfsMaterialForVersion(2, "quic", model.AWGVersion1x)
	c15 := renderAWGQuickConf("1.2.3.4", 51820, "PRIV", "PUB", "10.8.0.2/32", &preset, &m15, model.AWGVersion1x, "")
	if strings.Contains(c15, "S3 = ") || strings.Contains(c15, "I1 = ") {
		t.Errorf("1.5 client conf must NOT carry S3/I1-I5:\n%s", c15)
	}
	if !strings.Contains(c15, "S1 = ") || !strings.Contains(c15, "H1 = ") {
		t.Errorf("1.5 client conf must carry S1/H1:\n%s", c15)
	}

	// 2: S3/S4 + I1-I5 present, no HPK.
	m2 := GenerateAWGObfsMaterialForVersion(2, "quic", model.AWGVersion2)
	c2 := renderAWGQuickConf("1.2.3.4", 51820, "PRIV", "PUB", "10.8.0.2/32", &preset, &m2, model.AWGVersion2, "")
	if !strings.Contains(c2, "S3 = ") || !strings.Contains(c2, "I1 = ") {
		t.Errorf("2.0 client conf must carry S3 + I1-I5:\n%s", c2)
	}
	if strings.Contains(c2, "HeaderProtectionKey") {
		t.Errorf("2.0 client conf must NOT carry HPK:\n%s", c2)
	}

	// 3: HPK present.
	m3 := v3Material(t)
	c3 := renderAWGQuickConf("1.2.3.4", 51820, "PRIV", "PUB", "10.8.0.2/32", &preset, &m3, model.AWGVersion3, "")
	if !strings.Contains(c3, "HeaderProtectionKey = ") {
		t.Errorf("3.0 client conf must carry HPK:\n%s", c3)
	}
}

// TestWriteAmneziaConfLines_15DropsS34 pins server-side parity: a 1.5 server
// conf omits S3/S4 (awg-quick 1.x rejects them); 2/3 write the full S1-S4.
func TestWriteAmneziaConfLines_15DropsS34(t *testing.T) {
	amn := config.AmneziaOptions{JC: 5, JMIN: 30, JMAX: 100, S1: 100, S2: 50, S3: 20, S4: 10, H1: "1-2", H2: "3-4", H3: "5-6", H4: "7-8"}

	var b15 strings.Builder
	writeAmneziaConfLines(&b15, &amn, false, model.AWGVersion1x)
	if strings.Contains(b15.String(), "S3 = ") {
		t.Errorf("1.5 server conf must NOT carry S3:\n%s", b15.String())
	}
	if !strings.Contains(b15.String(), "S1 = ") {
		t.Errorf("1.5 server conf must carry S1:\n%s", b15.String())
	}

	var b2 strings.Builder
	writeAmneziaConfLines(&b2, &amn, false, model.AWGVersion2)
	if !strings.Contains(b2.String(), "S3 = ") {
		t.Errorf("2.0 server conf must carry S3:\n%s", b2.String())
	}
}
