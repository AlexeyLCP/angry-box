package chain

import (
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/ssh"
)

func TestDeriveKeyFingerprint(t *testing.T) {
	privPEM, _, err := ssh.GenerateSSHKeypair()
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	fp, err := DeriveKeyFingerprint(privPEM)
	if err != nil {
		t.Fatalf("DeriveKeyFingerprint: %v", err)
	}
	if !strings.HasPrefix(fp, "…") {
		t.Errorf("fingerprint should start with ellipsis, got %q", fp)
	}
	if len(fp) != 9 { // "…" + 8 hex chars
		t.Errorf("fingerprint length: got %d, want 9 (%q)", len(fp), fp)
	}
}

func TestDeriveKeyFingerprint_Invalid(t *testing.T) {
	if _, err := DeriveKeyFingerprint("not a key"); err == nil {
		t.Errorf("expected error for invalid PEM")
	}
}

func TestDeriveKeyFingerprint_Empty(t *testing.T) {
	if _, err := DeriveKeyFingerprint(""); err == nil {
		t.Errorf("expected error for empty input")
	}
}