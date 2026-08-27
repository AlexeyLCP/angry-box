#!/usr/bin/env bash
# Build the amnezia-box sing-box binary for remote nodes.
#
# amnezia-box is our base sing-box (a fork of sing-box 1.14 alpha carrying the
# AWG3 userspace endpoint type:"awg" + amneziawg-go feat/awg3). Our fork
# AlexeyLCP/amnezia-box adds the ports we need from sing-box-extended: mtproxy
# (build-tag with_mtproxy) and fallback round-robin (committed to the fork's
# tree, not applied as a patch here). amneziawg-go is pinned in the fork's
# go.mod (hoaxisr/amneziawg-go/v3 @ ae4523c — has the InputPackets API that
# transport/awg/port.go needs); we do NOT clone wireguard-go separately.
#
# Why: nodes must NOT compile Go themselves (weak VPSes hang during the build).
# We build amnezia-box once on a dev machine and store the tarball in deps/ so
# `angry-box deploy` just downloads it.
#
# Output: deps/sing-box-{SHA}-amnezia-linux-{ARCH}.tar.gz
#         deps/checksums.txt  (sha256 per tarball)
#
# Usage:
#   ./scripts/build-singbox.sh                              # amd64 + arm64 (default)
#   ARCHES="amd64" ./scripts/build-singbox.sh               # a subset
#   ABX_REF=3c554273 ./scripts/build-singbox.sh             # pin a fork commit
#
# Requires: go >= 1.26 (mtg-multi needs go 1.26), git, tar, sha256sum. Run on
# Linux or in WSL.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

# AlexeyLCP/amnezia-box fork ref. Pin a commit SHA (NOT a branch) so the
# artifact is reproducible; a bump is a deliberate act mirrored into
# internal/backend/singbox/singbox.go (singBoxVersion) and patchcheck_test.go.
# Default: mtproxy+fallback+trusttunnel on hoaxisr AWG 3.1 (922fc605).
# Override via env.
ABX_REF="${ABX_REF:-922fc605}"
# Nodes are amd64/arm64 only (see supportedNodeArchs) — build both by default.
ARCHES="${ARCHES:-amd64 arm64}"

BUILD_DIR="${BUILD_DIR:-/tmp/sing-box-build-angry}"
OUT_DIR="$REPO_ROOT/deps"
mkdir -p "$OUT_DIR"

# Our fork carries mtproxy + fallback; the upstream hoaxisr/amnezia-box does NOT.
ABX_REPO="https://github.com/AlexeyLCP/amnezia-box.git"

# Build tags. amnezia-box has with_awg (AWG3 endpoint) natively; with_mtproxy is
# our port. Drop the old sing-box-extended canary tags (with_masque,
# with_sudoku, with_manager, with_profiler). with_trusttunnel is our port from
# sing-box-extended. snell is unconditional in amnezia-box (no tag needed).
BUILD_TAGS="with_gvisor,with_quic,with_wireguard,with_utls,with_awg,with_mtproxy,with_trusttunnel,with_acme,with_clash_api,with_tailscale,with_openvpn"

echo "==> Preparing amnezia-box source in $BUILD_DIR"
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"
cd "$BUILD_DIR"

# Clone our fork and check out the pinned commit. GitHub does NOT allow
# shallow-fetching an arbitrary SHA (only branch/tag tips), so we fetch the
# full history (no --depth) then checkout the SHA — fast enough for a dev build.
git clone --quiet --no-checkout "$ABX_REPO" amnezia-box
( cd amnezia-box && git fetch --quiet origin && git checkout --quiet "$ABX_REF" )

# No patches to apply: fallback round-robin is committed to the fork's tree, and
# the wireguard-go awg-overlap fix is no longer relevant (AWG3 runs through
# amneziawg-go, not the shtorm-7 wireguard-go userspace path that panicked). The
# amneziawg-go pin (hoaxisr/amneziawg-go/v3 @ e32b3b0) lives in the fork's
# go.mod replace — `go mod download` / `go build` resolves it.

# Short SHA for the tarball name (traceable to the pinned fork commit).
VERSION_PLAIN="$(cd amnezia-box && git rev-parse --short HEAD)"

build_arch() {
  local arch="$1"
  local goarch="$arch"
  local out="$BUILD_DIR/out-$arch"
  mkdir -p "$out"

  echo "==> Building for linux/$arch (amnezia-box @ $VERSION_PLAIN)"
  ( cd amnezia-box && \
    CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOFLAGS=-mod=mod \
    go build -ldflags "-s -w" -tags "$BUILD_TAGS" \
      -o "$out/sing-box" ./cmd/sing-box )

  local tarname="sing-box-${VERSION_PLAIN}-amnezia-linux-${arch}.tar.gz"
  local tarball="$OUT_DIR/$tarname"
  ( cd "$out" && tar -czf "$tarball" sing-box )
  echo "==> Packed $tarball"
}

for a in $ARCHES; do build_arch "$a"; done

echo "==> Writing checksums"
: > "$OUT_DIR/checksums.txt"
for t in "$OUT_DIR"/sing-box-*-amnezia-linux-*.tar.gz; do
  ( cd "$OUT_DIR" && sha256sum "$(basename "$t")" ) >> "$OUT_DIR/checksums.txt"
done

echo "==> Done. Artifacts in $OUT_DIR:"
ls -la "$OUT_DIR"/sing-box-*-amnezia-linux-*.tar.gz "$OUT_DIR/checksums.txt"
cat "$OUT_DIR/checksums.txt"
echo ""
echo "==> Next: publish the tarball(s) to the GitHub release the deploy code"
echo "    pins (gh release upload <release> deps/sing-box-*-amnezia-*.tar.gz)"
echo "    and mirror the short SHA + sha256 into:"
echo "      internal/backend/singbox/singbox.go (singBoxVersion/URLs/Checksums)"
echo "      internal/backend/singbox/patchcheck_test.go (patchcheckABXRef)"
echo "    See docs/PATCHES.md for the full rebase procedure."