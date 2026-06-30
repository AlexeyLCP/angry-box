package chain

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var hRangeRe = regexp.MustCompile(`^(\d+)-(\d+)$`)

// TestGenAWGParams_Invariants verifies all four manual invariants across all
// profiles and many runs.
func TestGenAWGParams_Invariants(t *testing.T) {
	for _, profile := range []AWGProfileName{AWGProfileLite, AWGProfileStandard, AWGProfilePro} {
		for run := 0; run < 200; run++ {
			p := GenAWGParams(profile)

			// Invariant 1: Jmin < Jmax.
			if p.JMIN >= p.JMAX {
				t.Errorf("%s run %d: Jmin(%d) >= Jmax(%d)", profile, run, p.JMIN, p.JMAX)
			}

			// Invariant 2: |S1+56 - S2| >= 10.
			s1Plus56 := p.S1 + 56
			if abs(s1Plus56-p.S2) < 10 {
				t.Errorf("%s run %d: |S1+56(%d)-S2(%d)| = %d < 10", profile, run, s1Plus56, p.S2, abs(s1Plus56-p.S2))
			}

			// Invariant 3 & 4: H1-H4 are non-overlapping ranges in [5, 2^31-1].
			hs := [4]string{p.H1, p.H2, p.H3, p.H4}
			quadrantLo := [4]int{5, 536870912, 1073741824, 1610612736}
			quadrantHi := [4]int{536870911, 1073741823, 1610612735, 2147483647}
			for i, h := range hs {
				m := hRangeRe.FindStringSubmatch(h)
				if m == nil {
					t.Errorf("%s run %d: H%d not lo-hi: %q", profile, run, i+1, h)
					continue
				}
				lo, _ := strconv.Atoi(m[1])
				hi, _ := strconv.Atoi(m[2])
				if lo < quadrantLo[i] || hi > quadrantHi[i] {
					t.Errorf("%s run %d: H%d = %d-%d outside quadrant %d-%d", profile, run, i+1, lo, hi, quadrantLo[i], quadrantHi[i])
				}
				if hi-lo < 1000 {
					t.Errorf("%s run %d: H%d width %d < 1000", profile, run, i+1, hi-lo)
				}
			}
		}
	}
}

// TestGenAWGParams_UnknownProfileFallsBackToPro ensures an unknown profile
// does not panic and still produces valid invariants.
func TestGenAWGParams_UnknownProfileFallsBackToPro(t *testing.T) {
	p := GenAWGParams("nonexistent")
	if p.JMIN >= p.JMAX {
		t.Errorf("fallback to pro: Jmin(%d) >= Jmax(%d)", p.JMIN, p.JMAX)
	}
}

// TestToAWGConfLines verifies uppercase keys and ordering for kernel awg-quick.
func TestToAWGConfLines(t *testing.T) {
	p := AWGParams{JC: 4, JMIN: 50, JMAX: 837, S1: 118, S2: 114, S3: 54, S4: 21,
		H1: "1-2", H2: "3-4", H3: "5-6", H4: "7-8"}
	s := ToAWGConfLines(p)
	for _, key := range []string{"Jc", "Jmin", "Jmax", "S1", "S2", "S3", "S4", "H1", "H2", "H3", "H4"} {
		if !strings.Contains(s, key+" = ") {
			t.Errorf("conf lines missing %s", key)
		}
	}
	if !strings.HasPrefix(s, "Jc = 4") {
		t.Errorf("expected first line Jc = 4, got: %s", s)
	}
}

// TestToSingboxAmnezia verifies lowercase keys and itime default.
func TestToSingboxAmnezia(t *testing.T) {
	p := AWGParams{JC: 4, JMIN: 50, JMAX: 837, S1: 118, S2: 114, S3: 54, S4: 21,
		H1: "1-2", H2: "3-4", H3: "5-6", H4: "7-8"}
	amn := ToSingboxAmnezia(p, 0) // itime 0 → default 50
	if amn.ITime != 50 {
		t.Errorf("itime default: got %d, want 50", amn.ITime)
	}
	if amn.JC != 4 || amn.JMIN != 50 || amn.JMAX != 837 {
		t.Errorf("jc/jmin/jmax mismatch: %+v", amn)
	}
	if amn.H1 != "1-2" || amn.H4 != "7-8" {
		t.Errorf("h1-h4 string mismatch: %+v", amn)
	}
}