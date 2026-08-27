//go:build patchcheck

package singbox

// patchcheck_test.go — a build-tag-gated regression test that pins the amnezia-box
// fork commit + amneziawg-go commit our sing-box binary is built from, and asserts
// they match the deploy-time consts in singbox.go.
//
// amnezia-box (our fork AlexeyLCP/amnezia-box, a fork of hoaxisr/amnezia-box
// sing-box 1.14 alpha) carries AWG3 (type:"awg" endpoint + amneziawg-go feat/awg3)
// + our ports from sing-box-extended (mtproxy + fallback round-robin, committed
// to the fork's tree — no patches/ to apply). amneziawg-go is pinned in the fork's
// go.mod (hoaxisr/amneziawg-go/v3 @ e32b3b0 — InputPackets API for
// transport/awg/port.go).
//
// There are NO apply-cleanly subtests anymore — the old sing-box-extended patches
// (fallback-round-robin.patch, wireguard-go-awg-overlap.patch) are obsolete:
// fallback is committed to the fork, and the overlap fix is irrelevant (AWG3
// runs through amneziawg-go, not the shtorm-7 wireguard-go userspace path that
// panicked). The remaining test is the version-match sanity check.
//
// Run explicitly (no network needed for the version-match test):
//
//	go test -tags=patchcheck -run TestPatchcheckVersionsMatchSingBoxConst ./internal/backend/singbox/ -v
//
// The pinned versions live as Go consts here and are mirrored in
// scripts/build-singbox.sh (ABX_REF default) + internal/backend/singbox/singbox.go
// (singBoxVersion, amneziaWGGoVersion). See docs/PATCHES.md for the rebase flow.

import (
	"strings"
	"testing"
)

// Pinned versions — keep in sync with scripts/build-singbox.sh (ABX_REF default)
// and internal/backend/singbox/singbox.go (singBoxVersion / amneziaWGGoVersion
// consts, the deploy-time pins). Bump all three together.
const (
	// patchcheckABXRef is the full SHA of the AlexeyLCP/amnezia-box fork commit
	// we build from. singBoxVersion is its 8-char short SHA.
	patchcheckABXRef = "922fc6051b152bfa46f2deaf1e77eb21b2e5a041"
	abxRepo          = "https://github.com/AlexeyLCP/amnezia-box.git"

	// patchcheckAWGGORef is the full SHA of the hoaxisr/amneziawg-go/v3 commit
	// the fork's go.mod pins (InputPackets API, module path /v3).
	// amneziaWGGoVersion is its 7-char short SHA.
	patchcheckAWGGORef = "ae4523cffd89c0001edb3341c0b881fbf0159fa5"
	awggoRepo          = "https://github.com/hoaxisr/amneziawg-go.git"
)

// TestPatchcheckVersionsMatchSingBoxConst is a non-network sanity check that the
// version consts here match the deploy-time consts in singbox.go — so a bump
// that forgets one place fails the test.
func TestPatchcheckVersionsMatchSingBoxConst(t *testing.T) {
	// singBoxVersion (singbox.go) is the short SHA (git's abbreviated form,
	// 7+ chars — git extends to 8 when 7 is ambiguous) of the amnezia-box fork
	// commit; patchcheckABXRef is the full 40-char SHA. Compare the prefix of
	// the same length as singBoxVersion (so a 7- or 8-char short form both pass).
	if !strings.HasPrefix(patchcheckABXRef, singBoxVersion) {
		t.Errorf("patchcheck amnezia-box ref (%s) must start with singBoxVersion const (%s) — bump both together", patchcheckABXRef, singBoxVersion)
	}
}

// TestPatchcheckAWGGORefMatchesConst is the non-network sanity check for the
// amneziawg-go awg3 pin. patchcheckAWGGORef is the full 40-char commit SHA;
// amneziaWGGoVersion in singbox.go is the 7-char short SHA. A bump that forgets
// one place fails this test.
func TestPatchcheckAWGGORefMatchesConst(t *testing.T) {
	want := patchcheckAWGGORef[:7]
	if amneziaWGGoVersion != want {
		t.Errorf("patchcheck amneziawg-go ref short (%s) != amneziaWGGoVersion const (%s) — bump both together", want, amneziaWGGoVersion)
	}
}

// strings is imported for future patch-applicability subtests (if a patch
// against amnezia-box or amneziawg-go is ever added, the applyErr/cloneShallow
// helpers + a TestPatches_ApplyCleanly subtest will be reintroduced here).
var _ = strings.TrimSpace