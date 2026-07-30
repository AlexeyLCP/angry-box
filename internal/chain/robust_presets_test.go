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

// TestGroupPresets_RobustBucketFirst pins the dropdown grouping contract: the
// robust AWG presets (Jc<=10, budget-VPS handshake-friendly) land in a distinct
// "AWG · Robust" bucket that renders FIRST (so the operator sees the
// recommended default before the handshake-killing Jc=120 stealth presets,
// AGENTS #17). Jc is carried through so the dropdown can show it inline.
func TestGroupPresets_RobustBucketFirst(t *testing.T) {
	opts := ListPresetsDetailed()
	if len(opts) == 0 {
		t.Fatal("ListPresetsDetailed returned no presets")
	}
	groups := GroupPresets(opts)
	if len(groups) == 0 {
		t.Fatal("GroupPresets returned no groups")
	}
	// First group must be the Robust bucket.
	if groups[0].Label != "AWG · Robust (бюджетные VPS)" {
		t.Fatalf("first group = %q, want AWG · Robust bucket first", groups[0].Label)
	}
	// Every option in the robust bucket has Jc<=10 and Robust=true.
	for _, o := range groups[0].Options {
		if !o.Robust {
			t.Errorf("%q in robust bucket but Robust=false", o.Name)
		}
		if o.Jc == 0 || o.Jc > 10 {
			t.Errorf("%q in robust bucket has Jc=%d (want 1..10)", o.Name, o.Jc)
		}
	}
	// The 5 known robust presets are all present.
	robustNames := map[string]bool{}
	for _, o := range groups[0].Options {
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
	for _, o := range groups[0].Options {
		if o.Jc == 120 {
			t.Errorf("stealth preset %q (Jc=120) leaked into robust bucket", o.Name)
		}
	}
}
