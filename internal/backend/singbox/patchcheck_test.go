//go:build patchcheck

package singbox

// patchcheck_test.go — a build-tag-gated regression test that verifies the
// patches/ apply cleanly against the pinned upstream sources (sing-box-extended
// + wireguard-go). On an upstream bump that drifts the patched files' context,
// `git apply --check` fails loudly — alerting BEFORE a broken tarball is built.
//
// Run explicitly (needs network + git, NOT in the normal `go test` suite):
//
//	go test -tags=patchcheck -run TestPatches_ApplyCleanly ./internal/backend/singbox/ -v -timeout=300s
//
// The pinned versions live as Go consts here (the single source of truth for the
// patchcheck test) and are mirrored in scripts/build-singbox.sh. When bumping,
// update BOTH + re-run this test to confirm the patches still apply (resolve
// context drift by re-generating the patch against the new source).
//
// See docs/PATCHES.md for the full rebasing procedure.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Pinned upstream versions — keep in sync with scripts/build-singbox.sh
// (SBX_VERSION / WG_TAG defaults) and internal/backend/singbox/singbox.go
// (singBoxVersion const, which is the deploy-time pin for the binary).
const (
	patchcheckSBXVersion = "v1.13.14-extended-2.5.0"
	patchcheckWGTag      = "v0.0.2-beta.1-extended-1.4.3"

	sbxRepo = "https://github.com/shtorm-7/sing-box-extended.git"
	wgRepo  = "https://github.com/shtorm-7/wireguard-go.git"
)

// repoRoot returns the angry-box repo root (the test working dir is the package
// dir; patches/ is two levels up).
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// internal/backend/singbox → repo root is 3 levels up.
	root := filepath.Join(wd, "..", "..", "..")
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(abs, "patches")); err != nil {
		t.Fatalf("patches/ not found at %s — repoRoot miscomputed", abs)
	}
	return abs
}

// cloneShallow clones a single branch of repo into dest.
func cloneShallow(t *testing.T, repo, tag, dest string) {
	t.Helper()
	cmd := exec.Command("git", "clone", "--depth", "1", "--branch", tag, repo, dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone %s@%s: %v\n%s", repo, tag, err, out)
	}
}

// applyCheck runs `git apply --check <patch>` in dir; returns nil if it applies
// cleanly. --check does NOT modify the tree.
func applyCheck(dir, patch string) error {
	cmd := exec.Command("git", "apply", "--check", patch)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &applyErr{patch: patch, out: out, err: err}
	}
	return nil
}

type applyErr struct {
	patch string
	out   []byte
	err   error
}

func (e *applyErr) Error() string {
	return "git apply --check " + e.patch + " failed: " + e.err.Error() + "\n" + strings.TrimSpace(string(e.out))
}

// TestPatches_ApplyCleanly verifies each patch in patches/ still applies against
// its pinned upstream source. On failure, inspect the error output for the
// reject hunks (context drift) and update the patch per docs/PATCHES.md.
func TestPatches_ApplyCleanly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available — patchcheck requires git")
	}
	root := repoRoot(t)
	tmp := t.TempDir()

	// sing-box-extended → fallback-round-robin.patch
	t.Run("sing-box-extended/fallback-round-robin", func(t *testing.T) {
		sbxDir := filepath.Join(tmp, "sing-box-extended")
		cloneShallow(t, sbxRepo, patchcheckSBXVersion, sbxDir)
		patch := filepath.Join(root, "patches", "fallback-round-robin.patch")
		if err := applyCheck(sbxDir, patch); err != nil {
			t.Errorf("patch no longer applies cleanly against %s@%s — context drift on upstream bump:\n%v\nSee docs/PATCHES.md for the rebase procedure.", sbxRepo, patchcheckSBXVersion, err)
		}
	})

	// wireguard-go → wireguard-go-awg-overlap.patch
	t.Run("wireguard-go/awg-overlap", func(t *testing.T) {
		wgDir := filepath.Join(tmp, "wireguard-go-patched")
		cloneShallow(t, wgRepo, patchcheckWGTag, wgDir)
		patch := filepath.Join(root, "patches", "wireguard-go-awg-overlap.patch")
		if err := applyCheck(wgDir, patch); err != nil {
			t.Errorf("patch no longer applies cleanly against %s@%s — context drift on upstream bump:\n%v\nSee docs/PATCHES.md for the rebase procedure.", wgRepo, patchcheckWGTag, err)
		}
	})
}

// TestPatchcheckVersionsMatchSingBoxConst is a non-network sanity check (runs
// without the patchcheck build tag too in a future split) that the version consts
// here match the singBoxVersion const used at deploy time — so a bump that
// forgets one place fails the test.
func TestPatchcheckVersionsMatchSingBoxConst(t *testing.T) {
	// singBoxVersion in singbox.go is the deploy-time pin (e.g.
	// "1.13.14-extended-2.5.0" — no leading "v"). patchcheckSBXVersion has a
	// leading "v". Compare the stripped forms.
	want := strings.TrimPrefix(patchcheckSBXVersion, "v")
	if singBoxVersion != want {
		t.Errorf("patchcheck sing-box version (%s) != singBoxVersion const (%s) — bump both together", want, singBoxVersion)
	}
}