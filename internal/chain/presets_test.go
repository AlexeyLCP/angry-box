package chain

import (
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestDefaultPresetsLoaded(t *testing.T) {
	names := ListPresets()
	if len(names) == 0 {
		t.Fatal("expected built-in presets to be loaded")
	}

	for _, want := range []string{"russia_2026", "iran_2026", "china_2026", "maximum_stealth_2026"} {
		if _, ok := GetPreset(want); !ok {
			t.Errorf("built-in preset %q not found", want)
		}
	}
}

func TestGetDefaultPreset(t *testing.T) {
	p := GetDefaultPreset()
	if p.Name == "" {
		t.Error("default preset has empty name")
	}
}

func TestSetDefaultProfileAndGetEffective(t *testing.T) {
	original := GetDefaultPresetName()
	defer SetDefaultProfile(original)

	SetDefaultProfile("china_2026")
	if GetDefaultPresetName() != "china_2026" {
		t.Error("SetDefaultProfile did not change default")
	}

	// Effective should respect chain override
	c := &model.Chain{ObfuscationProfile: "russia_2026"}
	eff := GetEffectivePreset(c)
	if eff.Name != "russia_2026" {
		t.Errorf("expected russia_2026 from chain override, got %s", eff.Name)
	}

	// Without override uses global
	c2 := &model.Chain{}
	eff2 := GetEffectivePreset(c2)
	if eff2.Name != "china_2026" {
		t.Errorf("expected china_2026 as global default, got %s", eff2.Name)
	}
}

func TestLoadPresetsExternalOverride(t *testing.T) {
	original := GetDefaultPresetName()
	defer SetDefaultProfile(original)

	extra := []ConnectionPreset{
		{
			Name: "china_2026",
			XHTTP: &XHTTPPreset{
				Methods: []string{"CUSTOM"},
				Paths:   []string{"/custom/test"},
			},
		},
		{
			Name: "my_custom_2026",
			AWG: &AWGPreset{JC: 99, JMIN: 100, JMAX: 200},
		},
	}

	if err := LoadPresets(extra); err != nil {
		t.Fatalf("LoadPresets failed: %v", err)
	}

	// Override existing
	p := MustGetPreset("china_2026")
	if len(p.XHTTP.Methods) != 1 || p.XHTTP.Methods[0] != "CUSTOM" {
		t.Error("external preset did not override china_2026 XHTTP methods")
	}

	// New one added
	if _, ok := GetPreset("my_custom_2026"); !ok {
		t.Error("custom external preset was not added")
	}
}

func TestLoadPresetsNoAccumulation(t *testing.T) {
	// Call multiple times and ensure we don't accumulate old externals
	extra1 := []ConnectionPreset{{Name: "temp_profile_1"}}
	extra2 := []ConnectionPreset{{Name: "temp_profile_2"}}

	_ = LoadPresets(extra1)
	_ = LoadPresets(extra2)

	if _, ok := GetPreset("temp_profile_1"); ok {
		t.Error("temp_profile_1 should have been cleared on second LoadPresets")
	}
	if _, ok := GetPreset("temp_profile_2"); !ok {
		t.Error("temp_profile_2 should exist after second load")
	}
}

func TestAWGPresetValuesFromProfiles(t *testing.T) {
	for _, name := range []string{"russia_2026", "china_2026"} {
		p, ok := GetPreset(name)
		if !ok || p.AWG == nil {
			t.Errorf("profile %s missing AWG section", name)
			continue
		}
		if p.AWG.JC <= 0 || p.AWG.JMIN <= 0 || p.AWG.JMAX <= 0 {
			t.Errorf("profile %s has invalid AWG jc/jmin/jmax", name)
		}
	}
}
