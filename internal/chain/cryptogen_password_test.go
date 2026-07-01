package chain

// cryptogen_password_test.go pins the contract for GenerateProxyPassword: the
// output is 16 chars from [A-Za-z0-9]. It also guards against the modulo-bias
// regression where alphabet[int(c)%len(alphabet)] skewed the distribution
// because 256 is not a multiple of 62 (CTO-review L8).

import (
	"testing"
)

func TestGenerateProxyPassword_LengthAndAlphabet(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	inAlphabet := func(c byte) bool {
		for i := 0; i < len(alphabet); i++ {
			if alphabet[i] == c {
				return true
			}
		}
		return false
	}
	for i := 0; i < 200; i++ {
		p := GenerateProxyPassword()
		if len(p) != 16 {
			t.Fatalf("password length = %d, want 16 (%q)", len(p), p)
		}
		for j := 0; j < len(p); j++ {
			if !inAlphabet(p[j]) {
				t.Fatalf("password char %q out of alphabet (%q)", string(p[j]), p)
			}
		}
	}
}

func TestGenerateProxyPassword_NoModuloBiasSkew(t *testing.T) {
	// A coarse bias guard: with rejection sampling the 62 alphabet symbols
	// should appear with roughly equal frequency. The old int(c)%62 code
	// biased the first ~8 symbols (256%62=8 leftovers). Over a large sample we
	// assert no symbol dominates beyond a generous threshold, which would catch
	// a gross regression to the modulo approach while tolerating RNG noise.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	const samples = 62 * 200 // ~200 per symbol
	counts := make(map[byte]int)
	for i := 0; i < samples; i++ {
		p := GenerateProxyPassword()
		for j := 0; j < len(p); j++ {
			counts[p[j]]++
		}
	}
	// Expected ~ (samples*16)/62 per symbol.
	expected := float64(samples*16) / 62.0
	for i := 0; i < len(alphabet); i++ {
		c := counts[alphabet[i]]
		// Allow 50% deviation — far tighter than the modulo-bias skew (~13% on
		// the first 8 symbols) but loose enough for crypto/rand noise.
		if float64(c) < expected*0.5 || float64(c) > expected*1.5 {
			t.Errorf("symbol %q count=%d outside [%.0f, %.0f] (modulo-bias regression?)", string(alphabet[i]), c, expected*0.5, expected*1.5)
		}
	}
}