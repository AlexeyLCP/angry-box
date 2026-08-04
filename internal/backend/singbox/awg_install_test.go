package singbox

// awg_install_test.go — pins the AWG3 install-path helpers (Phase 2): the
// tools-version gate that decides whether to rebuild amneziawg-tools from
// upstream master so awg/awg-quick parse HeaderProtectionKey.

import "testing"

func TestAwgToolsMajorAtLeast3(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"amneziawg-tools v3.0.20260730 - https://amnezia.org", true},
		{"v3.0.20260730", true},
		{"3.1.0", true},
		{"amneziawg-tools v1.0.20260618-2", false},
		{"v2.9.9", false},
		{"", false},
		{"unknown", false},
	}
	for _, c := range cases {
		if got := awgToolsMajorAtLeast3(c.in); got != c.want {
			t.Errorf("awgToolsMajorAtLeast3(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
