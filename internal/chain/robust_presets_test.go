package chain

import "testing"

func TestRobustPresets_LoadAndLowJc(t *testing.T) {
	for _, name := range []string{
		"russia_2026_awg_robust",
		"iran_2026_awg_robust",
		"china_2026_awg_robust",
		"maximum_stealth_2026_awg_robust",
		"pro_2026_awg_robust",
	} {
		p, ok := GetPreset(name)
		if !ok {
			t.Errorf("robust preset %q not loaded", name)
			continue
		}
		if p.Protocol != "awg" {
			t.Errorf("%q: protocol=%q want awg", name, p.Protocol)
		}
		if p.AWG == nil {
			t.Errorf("%q: nil AWG block", name)
			continue
		}
		if p.AWG.JC > 10 {
			t.Errorf("%q: jc=%d, robust must be <=10 (handshake-killer is Jc=120, AGENTS #17)", name, p.AWG.JC)
		}
		// listed under the awg protocol dropdown
		found := false
		for _, n := range ListPresetsForProtocol("awg") {
			if n == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q not in ListPresetsForProtocol(awg)", name)
		}
	}
}

// TestGroupPresets_Order pins the dropdown grouping contract: the AWG 3.0
// (header protection, max stealth) bucket renders FIRST, then the Robust AWG
// bucket (Jc<=10, budget-VPS handshake-friendly), then AWG 2.0 Stealth
// (Jc=120). The operator sees the strongest presets first. Jc + Version are
// carried through so the dropdown can show them inline. AGENTS #17 (robust) +
// #5 revision (AWG 3.0).
func TestGroupPresets_Order(t *testing.T) {
	opts := ListPresetsDetailed()
	if len(opts) == 0 {
		t.Fatal("ListPresetsDetailed returned no presets")
	}
	groups := GroupPresets(opts)
	if len(groups) == 0 {
		t.Fatal("GroupPresets returned no groups")
	}
	// First group must be the AWG 3.0 (header protection) bucket.
	if groups[0].Label != "AWG · 3.0 (header protection)" {
		t.Fatalf("first group = %q, want AWG · 3.0 (header protection) bucket first", groups[0].Label)
	}
	// Every option in the AWG 3.0 bucket is version 3 + S1-S4>=12-ready.
	for _, o := range groups[0].Options {
		if o.Version != "3" {
			t.Errorf("%q in AWG 3.0 bucket but Version=%q", o.Name, o.Version)
		}
	}
	// The known AWG 3.0 presets are present.
	v3Names := map[string]bool{}
	for _, o := range groups[0].Options {
		v3Names[o.Name] = true
	}
	for _, want := range []string{
		"maximum_stealth_2026_awg3", "russia_2026_awg3", "iran_2026_awg3", "china_2026_awg3",
	} {
		if !v3Names[want] {
			t.Errorf("AWG 3.0 preset %q missing from 3.0 bucket", want)
		}
	}

	// Find the Robust bucket (2nd).
	var robust *PresetGroup
	for i := range groups {
		if groups[i].Label == "AWG · Robust (бюджетные VPS)" {
			robust = &groups[i]
			break
		}
	}
	if robust == nil {
		t.Fatal("AWG · Robust bucket missing")
	}
	// Every option in the robust bucket has Jc<=10 and Robust=true.
	for _, o := range robust.Options {
		if !o.Robust {
			t.Errorf("%q in robust bucket but Robust=false", o.Name)
		}
		if o.Jc == 0 || o.Jc > 10 {
			t.Errorf("%q in robust bucket has Jc=%d (want 1..10)", o.Name, o.Jc)
		}
	}
	// The 5 known robust presets are all present.
	robustNames := map[string]bool{}
	for _, o := range robust.Options {
		robustNames[o.Name] = true
	}
	for _, want := range []string{
		"russia_2026_awg_robust", "iran_2026_awg_robust", "china_2026_awg_robust",
		"maximum_stealth_2026_awg_robust", "pro_2026_awg_robust",
	} {
		if !robustNames[want] {
			t.Errorf("robust preset %q missing from robust bucket", want)
		}
	}
	// Stealth AWG presets (Jc=120) must NOT be in the robust bucket.
	for _, o := range robust.Options {
		if o.Jc == 120 {
			t.Errorf("stealth preset %q (Jc=120) leaked into robust bucket", o.Name)
		}
	}
}
