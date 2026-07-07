package singbox

// checksum_test.go pins the fail-closed integrity contract for the patched
// sing-box tarball. A missing/empty checksum for an architecture MUST produce
// an error rather than silently skipping verification, otherwise a compromised
// release/mirror yields a root backdoor on every deployed node (CTO-review M1).

import "testing"

// TestChecksumForArch is a table-driven test covering known arch (returns
// value), unknown arch (fail-closed error), and empty-checksum arch
// (fail-closed error). Replaces three separate funcs (CTO-review §13).
func TestChecksumForArch(t *testing.T) {
	cases := []struct {
		name    string
		arch    string
		wantErr bool
	}{
		{"known arch returns value", "amd64", false},
		{"unknown arch fails fail-closed", "mips", true},
		{"empty checksum fails fail-closed", "arm64", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checksumForArch(tc.arch)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected fail-closed error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == "" {
				t.Fatal("checksum must not be empty")
			}
		})
	}
}