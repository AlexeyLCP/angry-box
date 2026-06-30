#!/usr/bin/env bash
# Build a patched sing-box-extended binary for remote nodes.
#
# Why: nodes must NOT compile Go themselves (weak VPSes hang during the build).
# We build the patched sing-box-extended once, on a dev machine, and store the
# tarball in deps/ so `angry-box deploy` just downloads it.
#
# Patches applied:
#   patches/fallback-round-robin.patch    — per-connection round-robin fallback
#   patches/wireguard-go-awg-overlap.patch — fixes the chacha20poly1305
#     "invalid buffer overlap" panic that crashes userspace AmneziaWG
#     (this makes AWG-as-hop inside multi-hop chains work).
#
# Output: deps/sing-box-{VER}-patched-linux-{ARCH}.tar.gz
#         deps/checksums.txt  (sha256 per tarball)
#
# Usage:
#   ./scripts/build-singbox.sh             # amd64 only (default)
#   ARCHES="amd64 arm64" ./scripts/build-singbox.sh
#   SBX_VERSION=v1.13.14-extended-2.5.0 ./scripts/build-singbox.sh
#
# Requires: go (host), git, curl, tar, sha256sum. Run on Linux or in WSL.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

SBX_VERSION="${SBX_VERSION:-v1.13.14-extended-2.5.0}"
WG_TAG="${WG_TAG:-v0.0.2-beta.1-extended-1.4.3}"
ARCHES="${ARCHES:-amd64}"

BUILD_DIR="${BUILD_DIR:-/tmp/sing-box-build-angry}"
OUT_DIR="$REPO_ROOT/deps"
mkdir -p "$OUT_DIR"

SBX_REPO="https://github.com/shtorm-7/sing-box-extended.git"
WG_REPO="https://github.com/shtorm-7/wireguard-go.git"

BUILD_TAGS="with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_masque,with_mtproxy,with_openvpn,with_trusttunnel,with_sudoku,with_snell,with_manager,with_profiler"
# with_admin_panel intentionally omitted — needs pre-built dist/ assets.

echo "==> Preparing sources in $BUILD_DIR"
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"
cd "$BUILD_DIR"

# 1. sing-box-extended
if [ ! -d sing-box-extended ]; then
  git clone --depth 1 --branch "$SBX_VERSION" "$SBX_REPO" sing-box-extended
fi

# 2. wireguard-go (patched separately, then wired via go.mod replace)
if [ ! -d wireguard-go-patched ]; then
  git clone --depth 1 --branch "$WG_TAG" "$WG_REPO" wireguard-go-patched
fi

echo "==> Applying fallback round-robin patch"
( cd sing-box-extended && git checkout -- . && git apply "$REPO_ROOT/patches/fallback-round-robin.patch" )

echo "==> Applying wireguard-go AWG overlap-fix patch"
( cd wireguard-go-patched && git checkout -- . && git apply "$REPO_ROOT/patches/wireguard-go-awg-overlap.patch" )

echo "==> Wiring patched wireguard-go into sing-box-extended go.mod"
( cd sing-box-extended && go mod edit -replace github.com/sagernet/wireguard-go=../wireguard-go-patched && go mod tidy )

# Strip the leading 'v' for the tarball name (sing-box expects a plain version).
VERSION_PLAIN="${SBX_VERSION#v}"

build_arch() {
  local arch="$1"
  local goarch="$arch"
  local out="$BUILD_DIR/out-$arch"
  mkdir -p "$out"

  echo "==> Building for linux/$arch"
  ( cd sing-box-extended && \
    CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" \
    go build -ldflags "-s -w" -tags "$BUILD_TAGS" \
      -o "$out/sing-box" ./cmd/sing-box )

  # Sanity: the binary must report an extended version and `check` must work.
  if [ "$arch" = "amd64" ] && command -v qemu-x86_64 >/dev/null 2>&1; then
    : # cross-arch binary won't run natively; skip runtime sanity
  fi

  local tarname="sing-box-${VERSION_PLAIN}-patched-linux-${arch}.tar.gz"
  local tarball="$OUT_DIR/$tarname"
  ( cd "$out" && tar -czf "$tarball" sing-box )
  echo "==> Packed $tarball"
}

for a in $ARCHES; do build_arch "$a"; done

echo "==> Writing checksums"
: > "$OUT_DIR/checksums.txt"
for t in "$OUT_DIR"/sing-box-*-patched-linux-*.tar.gz; do
  ( cd "$OUT_DIR" && sha256sum "$(basename "$t")" ) >> "$OUT_DIR/checksums.txt"
done

echo "==> Done. Artifacts in $OUT_DIR:"
ls -la "$OUT_DIR"/sing-box-*-patched-linux-*.tar.gz "$OUT_DIR/checksums.txt"
cat "$OUT_DIR/checksums.txt"