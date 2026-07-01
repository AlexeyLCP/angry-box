package singbox

// checksum_test.go pins the fail-closed integrity contract for the patched
// sing-box tarball. A missing/empty checksum for an architecture MUST produce
// an error rather than silently skipping verification, otherwise a compromised
// release/mirror yields a root backdoor on every deployed node (CTO-review M1).

import "testing"

func TestChecksumForArch_KnownArchReturnsValue(t *testing.T) {
	got, err := checksumForArch("amd64")
	if err != nil {
		t.Fatalf("amd64 should have a checksum, got error: %v", err)
	}
	if got == "" {
		t.Fatal("amd64 checksum must not be empty")
	}
}

func TestChecksumForArch_UnknownArchFails(t *testing.T) {
	if _, err := checksumForArch("mips"); err == nil {
		t.Error("unknown arch must fail fail-closed, not silently skip verification")
	}
}

func TestChecksumForArch_EmptyChecksumFails(t *testing.T) {
	// arm64 currently has an empty checksum in the registry — fail-closed
	// means this returns an error, not a silent skip with a warning.
	if _, err := checksumForArch("arm64"); err == nil {
		t.Error("empty checksum must fail fail-closed, not silently skip verification")
	}
}