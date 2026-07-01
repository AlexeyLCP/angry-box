package xray

// checksum_test.go pins the fail-closed integrity contract for the Xray
// release zip. Until checksums are pinned (see CTO-review H4 — the xray
// backend is not wired into the factory), Deploy must refuse to install an
// unverified binary rather than warn-and-skip, otherwise a compromised release
// yields a root backdoor on every deployed node (CTO-review M1).

import "testing"

func TestChecksumForArch_EmptyUntilPinnedFailsClosed(t *testing.T) {
	// No arch has a pinned checksum yet — every arch must fail closed.
	for _, arch := range []string{"amd64", "arm64", "mips"} {
		if _, err := checksumForArch(arch); err == nil {
			t.Errorf("arch %q: expected fail-closed error (no pinned checksum), got nil", arch)
		}
	}
}