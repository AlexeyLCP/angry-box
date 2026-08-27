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
		{"amd64 returns value", "amd64", false},
		{"arm64 returns value", "arm64", false},
		// A missing/empty checksum (any arch not pinned, or pinned empty) MUST
		// fail closed — singBoxChecksums[unknown] reads "" and the check rejects
		// it, so an unpinned arch can never install an unverified binary.
		{"unpinned arch fails fail-closed", "mips", true},
		{"empty-string arch fails fail-closed", "", true},
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

// TestSupportedNodeArchs pins the product contract: angry-box captures NODES on
// amd64/arm64 only (the PANEL installs everywhere — incl. MIPS/armv7 routers —
// but a node must run a patched sing-box build we publish, and we publish only
// amd64/arm64). Every supported node arch must also have a download URL + a
// pinned checksum so a supported arch can actually deploy.
func TestSupportedNodeArchs(t *testing.T) {
	want := map[string]bool{"amd64": true, "arm64": true}
	if len(supportedNodeArchs) != len(want) {
		t.Fatalf("supportedNodeArchs = %v, want exactly %v", supportedNodeArchs, want)
	}
	for arch := range want {
		if !supportedNodeArchs[arch] {
			t.Errorf("arch %q must be a supported node arch", arch)
		}
		if singBoxDownloadURLs[arch] == "" {
			t.Errorf("supported node arch %q has no download URL", arch)
		}
		if singBoxChecksums[arch] == "" {
			t.Errorf("supported node arch %q has no pinned checksum", arch)
		}
	}
	for _, notNode := range []string{"mips", "mipsle", "arm", "386", "riscv64"} {
		if supportedNodeArchs[notNode] {
			t.Errorf("arch %q must NOT be a supported node arch", notNode)
		}
	}
}