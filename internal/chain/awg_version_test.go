package chain

import (
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestEffectiveAWGVersion pins the legacy-AWG3Mode ↔ AWGVersion reconciliation
// (model.awg_version.go). A pre-version-field store (v0.8.10) set AWG3Mode=true
// and must keep resolving to "3"; an empty version defaults to "2" (the current
// kernel+CPS baseline). AWGVersion wins when AWG3Mode is false. Bogus values
// fall back to "2" rather than producing an unknown version downstream.
func TestEffectiveAWGVersion(t *testing.T) {
	cases := []struct {
		name    string
		awg3    bool
		version string
		want    string
	}{
		{"legacy AWG3Mode on", true, "", model.AWGVersion3},
		{"explicit v3", false, model.AWGVersion3, model.AWGVersion3},
		{"both set (alias)", true, model.AWGVersion3, model.AWGVersion3},
		{"v2 explicit", false, model.AWGVersion2, model.AWGVersion2},
		{"v1.5 explicit", false, model.AWGVersion1x, model.AWGVersion1x},
		{"empty defaults to v2", false, "", model.AWGVersion2},
		{"bogus falls back to v2", false, "9.9", model.AWGVersion2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prof := model.InboundProfile{AWG3Mode: c.awg3, AWGVersion: c.version}
			if got := prof.EffectiveAWGVersion(); got != c.want {
				t.Fatalf("EffectiveAWGVersion = %q, want %q", got, c.want)
			}
			ib := model.NodeInbound{AWG3Mode: c.awg3, AWGVersion: c.version}
			if got := ib.EffectiveAWGVersion(); got != c.want {
				t.Fatalf("NodeInbound.EffectiveAWGVersion = %q, want %q", got, c.want)
			}
		})
	}
}

// TestIsKnownAWGVersion guards the canonical version set used by the UI
// validator + preset resolver.
func TestIsKnownAWGVersion(t *testing.T) {
	for _, v := range []string{model.AWGVersion1x, model.AWGVersion2, model.AWGVersion3} {
		if !model.IsKnownAWGVersion(v) {
			t.Errorf("IsKnownAWGVersion(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "1", "2.0", "3.0", "9.9"} {
		if model.IsKnownAWGVersion(v) {
			t.Errorf("IsKnownAWGVersion(%q) = true, want false", v)
		}
	}
}

// TestPresetSupportsAWGVersion pins the preset↔version compatibility contract:
// a v3 inbound needs a v3 preset (S1-S4>=12 + HPK-ready H1-H4); a v1.5/v2
// inbound accepts any non-v3 preset but rejects a v3 preset (which carries the
// HPK-specific H1-H4 minimization that is suboptimal without HPK).
func TestPresetSupportsAWGVersion(t *testing.T) {
	v3Preset := ConnectionPreset{Name: "x", AWG: &AWGPreset{Version: model.AWGVersion3}}
	v2PresetExplicit := ConnectionPreset{Name: "x", AWG: &AWGPreset{Version: model.AWGVersion2}}
	v2PresetImplicit := ConnectionPreset{Name: "x", AWG: &AWGPreset{Version: ""}} // legacy = v2
	nonAWG := ConnectionPreset{Name: "x"}

	if PresetSupportsAWGVersion(v3Preset, model.AWGVersion3) != true {
		t.Error("v3 preset should support v3")
	}
	if PresetSupportsAWGVersion(v3Preset, model.AWGVersion2) != false {
		t.Error("v3 preset must NOT support v2 (HPK-specific H1-H4 minimization)")
	}
	if PresetSupportsAWGVersion(v2PresetExplicit, model.AWGVersion3) != false {
		t.Error("v2 preset must NOT support v3 (S1-S4 may be < 12 → HPK rejected)")
	}
	if PresetSupportsAWGVersion(v2PresetExplicit, model.AWGVersion2) != true {
		t.Error("v2 preset should support v2")
	}
	if PresetSupportsAWGVersion(v2PresetImplicit, model.AWGVersion2) != true {
		t.Error("legacy preset (Version='') should support v2")
	}
	if PresetSupportsAWGVersion(v2PresetImplicit, model.AWGVersion1x) != true {
		t.Error("legacy preset (Version='') should support v1.5")
	}
	if PresetSupportsAWGVersion(nonAWG, model.AWGVersion3) != false {
		t.Error("non-AWG preset must not claim AWG version support")
	}
}

// TestResolveStandaloneAWGPreset_VersionFallback verifies that a v3 inbound
// whose explicit preset is a v2-only preset gets the v3 default (so it never
// silently renders a v2 preset whose S1-S4 may be < 12 → HPK rejected), and
// that a v3 inbound with no preset at all gets the v3 default rather than the
// global default.
func TestResolveStandaloneAWGPreset_VersionFallback(t *testing.T) {
	// v3 inbound paired (incorrectly) with a v2-only preset name.
	ib := &model.NodeInbound{
		Protocol:    "awg",
		AWGVersion:  model.AWGVersion3,
		Obfuscation: "maximum_stealth_2026_awg", // v2 preset
	}
	got := ResolveStandaloneAWGPreset(ib)
	if got.AWG == nil || got.AWG.Version != model.AWGVersion3 {
		t.Fatalf("v3 inbound with v2 preset: resolved AWG.Version = %q, want %q (fallback to v3 default)",
			func() string {
				if got.AWG == nil {
					return "<nil>"
				}
				return got.AWG.Version
			}(), model.AWGVersion3)
	}
	// v3 inbound with no preset → v3 default.
	ib2 := &model.NodeInbound{Protocol: "awg", AWGVersion: model.AWGVersion3}
	got2 := ResolveStandaloneAWGPreset(ib2)
	if got2.AWG == nil || got2.AWG.Version != model.AWGVersion3 {
		t.Fatal("v3 inbound with no preset should resolve to the v3 default")
	}
	// v2 inbound with no preset → v2 default.
	ib3 := &model.NodeInbound{Protocol: "awg", AWGVersion: model.AWGVersion2}
	got3 := ResolveStandaloneAWGPreset(ib3)
	if got3.AWG == nil || got3.AWG.Version == model.AWGVersion3 {
		t.Fatal("v2 inbound with no preset should resolve to the v2 default, not a v3 preset")
	}
}

// TestResolveStandaloneAWGPreset_CompatiblePresetKept verifies the resolver
// does NOT touch a preset that already matches the inbound's version.
func TestResolveStandaloneAWGPreset_CompatiblePresetKept(t *testing.T) {
	ib := &model.NodeInbound{
		Protocol:    "awg",
		AWGVersion:  model.AWGVersion3,
		Obfuscation: "maximum_stealth_2026_awg3", // v3 preset — compatible
	}
	got := ResolveStandaloneAWGPreset(ib)
	if got.Name != "maximum_stealth_2026_awg3" {
		t.Fatalf("compatible v3 preset should be kept as-is, got %q", got.Name)
	}
}

// TestAWG3Material_GeneratedForVersion3WithoutLegacyBool verifies the new
// AWGVersion path (without the legacy AWG3Mode bool) still triggers HPK
// material generation + reconstruction — the v0.8.10 toggle was the only entry
// before; now AWGVersion="3" must do the same.
func TestAWG3Material_GeneratedForVersion3WithoutLegacyBool(t *testing.T) {
	// AWGVersion="3", AWG3Mode=false — pure new-path inbound.
	ib := &model.NodeInbound{
		Protocol:   "awg",
		AWGVersion: model.AWGVersion3,
	}
	ensureInboundAWG3Material(ib)
	if ib.AWG3HeaderProtectionKey == "" {
		t.Fatal("AWGVersion=3 (no legacy bool) must still generate HeaderProtectionKey")
	}
	// Reconstructed material must carry the AWG3 fields.
	m := InboundAWGObfsMaterial(ib)
	if m == nil || !m.AWG3Mode || m.HeaderProtectionKey == "" {
		t.Fatal("InboundAWGObfsMaterial must reconstruct AWG3 fields for a v3 inbound")
	}
}

// TestAWG3Material_NotGeneratedForVersion2 verifies v2/v1.5 inbounds do NOT
// get AWG3 material (HPK is a v3-only feature).
func TestAWG3Material_NotGeneratedForVersion2(t *testing.T) {
	for _, v := range []string{model.AWGVersion2, model.AWGVersion1x} {
		ib := &model.NodeInbound{Protocol: "awg", AWGVersion: v}
		ensureInboundAWG3Material(ib)
		if ib.AWG3HeaderProtectionKey != "" {
			t.Errorf("version %q must not generate AWG3 material, got HPK=%q", v, ib.AWG3HeaderProtectionKey)
		}
	}
}

// TestAWGVersion_PropagatedThroughProfileMaterial verifies ApplyProfileMaterialToInbound
// copies the profile's AWGVersion onto the materialized inbound (the version
// must travel with the material so the per-node render picks the right path).
func TestAWGVersion_PropagatedThroughProfileMaterial(t *testing.T) {
	preset := MustGetPreset("maximum_stealth_2026_awg3")
	prof := &model.InboundProfile{
		Protocol:    "awg",
		AWGVersion:  model.AWGVersion3,
		Obfuscation: "maximum_stealth_2026_awg3",
	}
	ib := &model.NodeInbound{Protocol: "awg"}
	ApplyProfileMaterialToInbound(ib, prof, preset)
	if ib.AWGVersion != model.AWGVersion3 {
		t.Fatalf("ApplyProfileMaterialToInbound: ib.AWGVersion = %q, want %q", ib.AWGVersion, model.AWGVersion3)
	}
	if ib.EffectiveAWGVersion() != model.AWGVersion3 {
		t.Fatal("materialized inbound must resolve to v3")
	}
}

// TestAWG3PresetS1S4_AtLeast12 pins the header-protection nonce constraint:
// every shipped AWG 3.0 preset has S1-S4 >= 12 (HeaderCipherNonceSize), so a v3
// inbound never starts with a preset that the kernel/userspace would reject
// when HPK is set.
func TestAWG3PresetS1S4_AtLeast12(t *testing.T) {
	for _, name := range []string{
		"maximum_stealth_2026_awg3", "russia_2026_awg3", "iran_2026_awg3", "china_2026_awg3",
	} {
		p, ok := GetPreset(name)
		if !ok {
			t.Fatalf("AWG 3.0 preset %q not loaded", name)
		}
		if p.AWG == nil {
			t.Fatalf("%q: nil AWG block", name)
		}
		for _, s := range []int{p.AWG.S1, p.AWG.S2, p.AWG.S3, p.AWG.S4} {
			if s < 12 {
				t.Errorf("%q: S value %d < 12 (HeaderCipherNonceSize, mandatory for HPK)", name, s)
			}
		}
		if p.AWG.Version != model.AWGVersion3 {
			t.Errorf("%q: AWG.Version = %q, want %q", name, p.AWG.Version, model.AWGVersion3)
		}
	}
}

// TestListPresetsDetailed_AWGVersionField verifies the UI descriptor carries
// the version so the dropdown can bucket AWG 3.0 presets separately. Legacy
// (Version='') AWG presets resolve to "2"; the v3 presets carry "3".
func TestListPresetsDetailed_AWGVersionField(t *testing.T) {
	opts := ListPresetsDetailed()
	byName := map[string]string{}
	for _, o := range opts {
		byName[o.Name] = o.Version
	}
	if v := byName["maximum_stealth_2026_awg3"]; v != model.AWGVersion3 {
		t.Errorf("maximum_stealth_2026_awg3 descriptor Version = %q, want %q", v, model.AWGVersion3)
	}
	if v := byName["maximum_stealth_2026_awg"]; v != model.AWGVersion2 {
		t.Errorf("maximum_stealth_2026_awg (legacy, Version='') descriptor Version = %q, want %q (default)", v, model.AWGVersion2)
	}
	// Presets with no AWG section (Reality-only / XHTTP-only) carry no version.
	if p, ok := GetPreset("maximum_stealth_2026_reality"); ok && p.AWG == nil {
		if v := byName["maximum_stealth_2026_reality"]; v != "" {
			t.Errorf("Reality-only preset should have empty Version, got %q", v)
		}
	}
}

// TestAWG3PresetDescription_NonEmpty is a documentation anchor: the v3 presets
// ship a human-readable description (shown in the presets table via TPreset).
func TestAWG3PresetDescription_NonEmpty(t *testing.T) {
	for _, name := range []string{
		"maximum_stealth_2026_awg3", "russia_2026_awg3", "iran_2026_awg3", "china_2026_awg3",
	} {
		p, ok := GetPreset(name)
		if !ok {
			continue
		}
		if strings.TrimSpace(p.Description) == "" {
			t.Errorf("%q: empty description", name)
		}
	}
}
