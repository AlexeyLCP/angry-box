package chain

import (
	"strings"
	"testing"
	"unicode/utf8"

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
	if n := utf8.RuneCountInString(fp); n != 9 { // "…" (1 rune) + 8 hex chars
		t.Errorf("fingerprint length: got %d, want 9 (%q)", n, fp)
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