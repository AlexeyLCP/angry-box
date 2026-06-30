package chain

// awgpresets_gen.go — AmneziaWG obfuscation parameter generator, ported from
// VPN/orchestrator/app/services/awg_presets.py (itself ported from
// pumbaX/awg-multi-script gen_awg_params).
//
// Three profiles (lite/standard/pro) with concrete Jc/Jmin/Jmax/S1-S4 ranges
// and 4-quadrant H1-H4 generation per the official AmneziaWG manual.
//
// Invariants enforced:
//   - Jmin < Jmax (else Jmax = Jmin + rand(100,500))
//   - |S1+56 - S2| >= 10 (strengthened from manual's "S1+56 != S2")
//   - H1-H4 are unique non-overlapping ranges in [5, 2^31-1], width >= 1000
//   - H1-H4 upper bound is 2^31-1 (Windows client signed-int32 compatibility)

import (
	"crypto/rand"
	"fmt"
	"math/big"

	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// AWGProfileName identifies an AWG obfuscation profile.
type AWGProfileName string

const (
	AWGProfileLite     AWGProfileName = "lite"
	AWGProfileStandard AWGProfileName = "standard"
	AWGProfilePro      AWGProfileName = "pro"
)

// awgProfileRange is an inclusive [lo, hi] random range.
type awgProfileRange struct{ Lo, Hi int }

var awgProfiles = map[AWGProfileName]map[string]awgProfileRange{
	AWGProfileLite: {
		"Jc": {3, 5}, "Jmin": {5, 15}, "Jmax": {45, 55},
		"S1": {97, 107}, "S2": {17, 27}, "S3": {16, 26}, "S4": {4, 10},
	},
	AWGProfileStandard: {
		"Jc": {5, 8}, "Jmin": {30, 80}, "Jmax": {100, 250},
		"S1": {30, 80}, "S2": {30, 80}, "S3": {15, 32}, "S4": {10, 20},
	},
	AWGProfilePro: {
		"Jc": {4, 16}, "Jmin": {50, 256}, "Jmax": {300, 1000},
		"S1": {15, 150}, "S2": {15, 150}, "S3": {8, 64}, "S4": {6, 31},
	},
}

// AWGParams holds generated AmneziaWG obfuscation parameters. Jc/Jmin/Jmax/
// S1-S4 are ints; H1-H4 are "lo-hi" range strings (per the AWG manual and the
// sing-box amnezia schema).
type AWGParams struct {
	JC   int
	JMIN int
	JMAX int
	S1   int
	S2   int
	S3   int
	S4   int
	H1   string
	H2   string
	H3   string
	H4   string
}

// randRange returns an inclusive random int in [lo, hi].
func randRange(lo, hi int) int {
	if lo > hi {
		lo, hi = hi, lo
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(hi-lo+1)))
	if err != nil {
		// crypto/rand should not fail; fall back to a deterministic-ish value.
		return lo
	}
	return lo + int(n.Int64())
}

// genQuadrantPair returns a "lo-hi" range string within [qmin, qmax] with a
// width of at least 1000 (the manual requires non-trivially-wide H ranges).
func genQuadrantPair(qmin, qmax int) string {
	span := qmax - qmin
	lo := randRange(qmin, qmin+span/3)
	hi := randRange(qmin+2*span/3, qmax)
	if hi-lo < 1000 {
		hi = min(lo+1000+randRange(0, 10000), qmax)
	}
	return fmt.Sprintf("%d-%d", lo, hi)
}

// GenAWGParams generates AmneziaWG obfuscation parameters for the given profile,
// enforcing all four manual invariants. Unknown profiles fall back to "pro".
func GenAWGParams(profile AWGProfileName) AWGParams {
	p, ok := awgProfiles[profile]
	if !ok {
		p = awgProfiles[AWGProfilePro]
	}

	params := AWGParams{
		JC:   randRange(p["Jc"].Lo, p["Jc"].Hi),
		JMIN: randRange(p["Jmin"].Lo, p["Jmin"].Hi),
		JMAX: randRange(p["Jmax"].Lo, p["Jmax"].Hi),
		S1:   randRange(p["S1"].Lo, p["S1"].Hi),
		S2:   randRange(p["S2"].Lo, p["S2"].Hi),
		S3:   randRange(p["S3"].Lo, p["S3"].Hi),
		S4:   randRange(p["S4"].Lo, p["S4"].Hi),
	}

	// Invariant 1: Jmin < Jmax.
	if params.JMIN >= params.JMAX {
		params.JMAX = params.JMIN + randRange(100, 500)
	}

	// Invariant 2: |S1+56 - S2| >= 10 (strengthened from "S1+56 != S2").
	s1Plus56 := params.S1 + 56
	for i := 0; i < 10; i++ {
		if abs(s1Plus56-params.S2) >= 10 {
			break
		}
		params.S2 = randRange(p["S2"].Lo, p["S2"].Hi)
	}
	if s1Plus56 == params.S2 {
		params.S2 += 10
	}

	// Invariant 3 & 4: H1-H4 in 4 fixed non-overlapping quadrants of [5, 2^31-1].
	quadrants := [4][2]int{
		{5, 536870911},           // 5 .. 2^29-1
		{536870912, 1073741823},  // 2^29 .. 2^30-1
		{1073741824, 1610612735}, // 2^30 .. 3*2^29-1
		{1610612736, 2147483647}, // 3*2^29 .. 2^31-1 (Windows signed-int32 limit)
	}
	params.H1 = genQuadrantPair(quadrants[0][0], quadrants[0][1])
	params.H2 = genQuadrantPair(quadrants[1][0], quadrants[1][1])
	params.H3 = genQuadrantPair(quadrants[2][0], quadrants[2][1])
	params.H4 = genQuadrantPair(quadrants[3][0], quadrants[3][1])

	return params
}

// ToAWGConfLines serializes params to AWG .conf-style lines (uppercase keys) for
// kernel awg-quick configs. Returns the lines joined by newlines.
func ToAWGConfLines(params AWGParams) string {
	return fmt.Sprintf("Jc = %d\nJmin = %d\nJmax = %d\nS1 = %d\nS2 = %d\nS3 = %d\nS4 = %d\nH1 = %s\nH2 = %s\nH3 = %s\nH4 = %s",
		params.JC, params.JMIN, params.JMAX, params.S1, params.S2, params.S3, params.S4,
		params.H1, params.H2, params.H3, params.H4)
}

// ToSingboxAmnezia serializes params to a sing-box JSON "amnezia" block
// (lowercase keys, matching awg_presets.to_singbox_amnezia). itime defaults to
// 50 when zero.
func ToSingboxAmnezia(params AWGParams, itime int) *config.AmneziaOptions {
	if itime == 0 {
		itime = 50
	}
	return &config.AmneziaOptions{
		JC:    params.JC,
		JMIN:  params.JMIN,
		JMAX:  params.JMAX,
		S1:    params.S1,
		S2:    params.S2,
		S3:    params.S3,
		S4:    params.S4,
		H1:    params.H1,
		H2:    params.H2,
		H3:    params.H3,
		H4:    params.H4,
		ITime: itime,
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}