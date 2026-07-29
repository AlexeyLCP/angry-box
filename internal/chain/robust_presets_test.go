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
