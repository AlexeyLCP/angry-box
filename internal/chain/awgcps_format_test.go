package chain

import (
	"strings"
	"testing"
)

// TestCPSString_FormatAndPadding verifies the <b 0x...> format and that
// odd-length hex is padded to even (sing-box wireguard-go rejects odd hex).
func TestCPSString_FormatAndPadding(t *testing.T) {
	// 1 byte → 2 hex chars (even) → "<b 0xAB>"
	got := CPSString([]byte{0xAB}, 0)
	if got != "<b 0xab>" {
		t.Errorf("1-byte: got %q, want <b 0xab>", got)
	}

	// 3 bytes → 6 hex chars (even) → "<b 0xabcdef>" style
	got = CPSString([]byte{0xAB, 0xCD, 0xEF}, 0)
	if got != "<b 0xabcdef>" {
		t.Errorf("3-byte: got %q, want <b 0xabcdef>", got)
	}
}

// TestCPSString_RepeatPrefix verifies the dns-profile <r N> prefix.
func TestCPSString_RepeatPrefix(t *testing.T) {
	got := CPSString([]byte{0x01, 0x02}, 2)
	if got != "<r 2><b 0x0102>" {
		t.Errorf("dns: got %q, want <r 2><b 0x0102>", got)
	}
}

// TestCPSString_OddHexPadding: a payload whose hex length is odd must be padded
// with a leading zero. We can't make hex.EncodeToString produce odd output
// directly (it always pairs), so test evenHex directly.
func TestEvenHex_Padding(t *testing.T) {
	if evenHex("abc") != "0abc" {
		t.Errorf("odd: got %q, want 0abc", evenHex("abc"))
	}
	if evenHex("abcd") != "abcd" {
		t.Errorf("even: got %q, want abcd", evenHex("abcd"))
	}
}

// TestCPSMaterialStrings_OrderAndEmpty verifies I1..I5 ordering and that empty
// payloads yield "".
func TestCPSMaterialStrings_OrderAndEmpty(t *testing.T) {
	m := AWGObfsMaterial{
		I1: []byte{0x11, 0x22},
		I2: []byte{0x33},
		// I3-I5 nil/empty
	}
	strs := CPSMaterialStrings(m)
	if strs[0] != "<b 0x1122>" {
		t.Errorf("I1: got %q", strs[0])
	}
	if strs[1] != "<b 0x33>" {
		t.Errorf("I2: got %q", strs[1])
	}
	if strs[2] != "" || strs[3] != "" || strs[4] != "" {
		t.Errorf("empty I3-I5 should be empty: %q %q %q", strs[2], strs[3], strs[4])
	}
}

// TestCPSString_AllProfilesProduceValid: run the existing material generator
// across profiles and ensure every non-empty Ix round-trips to a <b 0x...> form.
func TestCPSString_AllProfilesProduceValid(t *testing.T) {
	for _, mimicry := range []string{"quic", "sip", "dns", "tls"} {
		for level := 1; level <= 3; level++ {
			m := GenerateAWGObfsMaterial(level, mimicry)
			strs := CPSMaterialStrings(m)
			for i, s := range strs {
				if s == "" {
					continue
				}
				if !strings.HasPrefix(s, "<b 0x") || !strings.HasSuffix(s, ">") {
					t.Errorf("%s lvl%d I%d: malformed %q", mimicry, level, i+1, s)
				}
				// hex portion must be even-length.
				inner := strings.TrimSuffix(strings.TrimPrefix(s, "<b 0x"), ">")
				if len(inner)%2 != 0 {
					t.Errorf("%s lvl%d I%d: odd hex %q", mimicry, level, i+1, inner)
				}
			}
		}
	}
}