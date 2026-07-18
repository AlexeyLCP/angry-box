package singbox

import (
	"context"
	"strings"
	"testing"
)

// TestInstallPatchedBinary_MirrorFallback verifies the download script tries
// the primary URL plus every ANGRY_BINARY_MIRRORS entry in order (P2c —
// GitHub release assets are a SPOF / unreachable from some RU networks), and
// that every candidate is still gated on the pinned sha256.
func TestInstallPatchedBinary_MirrorFallback(t *testing.T) {
	t.Setenv("ANGRY_BINARY_URL", "https://primary.example/sb.tar.gz")
	t.Setenv("ANGRY_BINARY_MIRRORS", "https://m1.example/sb.tar.gz, https://m2.example/sb.tar.gz ,")
	client := newFakeSSH(fakeRule{substring: "uname -m", outs: []string{"x86_64\n"}})
	if err := installPatchedBinary(context.Background(), client, false); err != nil {
		t.Fatalf("installPatchedBinary: %v", err)
	}
	if len(client.commands) != 2 {
		t.Fatalf("want 2 commands (uname + install), got %d: %v", len(client.commands), client.commands)
	}
	script := client.commands[1]
	for _, u := range []string{"https://primary.example/sb.tar.gz", "https://m1.example/sb.tar.gz", "https://m2.example/sb.tar.gz"} {
		if !strings.Contains(script, "'"+u+"'") {
			t.Errorf("install script missing mirror %q:\n%s", u, script)
		}
	}
	if !strings.Contains(script, "for u in ") || !strings.Contains(script, "sha256sum -c -") {
		t.Errorf("install script must loop mirrors and verify sha256 per candidate:\n%s", script)
	}
	if !strings.Contains(script, "all sing-box download mirrors failed") {
		t.Errorf("install script must fail closed when every mirror fails:\n%s", script)
	}
}

// TestInstallPatchedBinary_RejectsShellInjection verifies an operator-supplied
// URL with shell metacharacters is refused before it reaches a root shell on
// the node (same class as CodeRabbit M1 on the AWG tarball URL).
func TestInstallPatchedBinary_RejectsShellInjection(t *testing.T) {
	t.Setenv("ANGRY_BINARY_URL", "https://x.example/a.tar.gz'; rm -rf / ;'")
	client := newFakeSSH(fakeRule{substring: "uname -m", outs: []string{"x86_64\n"}})
	err := installPatchedBinary(context.Background(), client, false)
	if err == nil {
		t.Fatal("expected error for URL with shell injection, got nil")
	}
	if !strings.Contains(err.Error(), "metacharacters") {
		t.Errorf("error should mention metacharacters: %v", err)
	}
	if len(client.commands) != 1 {
		t.Fatalf("install must not run after URL rejection; ran %d commands", len(client.commands))
	}
}
