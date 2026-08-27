#!/usr/bin/env bash
# Build the caddy binary for the node "spinal cord" utility bundle.
#
# Caddy owns ports 80/443 on a utility node: the layer4 app does raw-TCP SNI
# routing (own domains -> the internal HTTPS listener, protocol subdomains ->
# sing-box inbounds, default -> Reality), while the standard apps serve the
# fakesite, the pushed subscription statics and the panel relay. layer4 is NOT
# in stock caddy — we build with xcaddy (--with github.com/caddyserver/layer4).
#
# Why: nodes never compile anything themselves (weak VPSes). We build once on
# a dev machine, publish the tarball as a GitHub release asset, and
# chain.InstallCaddy downloads it (sha256-verified).
#
# Output: deps/caddy-caddy-{CADDY_REF}-layer4-linux-{ARCH}.tar.gz
#         appends to deps/checksums.txt (sha256 per tarball)
#
# Usage:
#   ./scripts/build-caddy.sh                      # amd64 only (default)
#   ARCHES="amd64 arm64" ./scripts/build-caddy.sh
#   CADDY_REF=v2.9.1 ./scripts/build-caddy.sh     # pin the caddy version
#
# Requires: go, xcaddy (go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest),
# tar, sha256sum. Run on Linux or in WSL.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

# Pin the caddy version; a bump is a deliberate act mirrored into
# internal/chain/utility_install.go (caddyBuildVersion + caddyChecksums).
CADDY_REF="${CADDY_REF:-v2.9.1}"
ARCHES="${ARCHES:-amd64}"

BUILD_DIR="${BUILD_DIR:-/tmp/caddy-build-angry}"
OUT_DIR="$REPO_ROOT/deps"
mkdir -p "$OUT_DIR" "$BUILD_DIR"

command -v xcaddy >/dev/null 2>&1 || {
  echo "ERROR: xcaddy not found. Install: go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest" >&2
  exit 1
}

build_arch() {
  local arch="$1"
  local out="$BUILD_DIR/out-$arch"
  mkdir -p "$out"

  echo "==> Building caddy $CADDY_REF + layer4 for linux/$arch"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
    xcaddy build "$CADDY_REF" \
      --with github.com/caddyserver/layer4 \
      --output "$out/caddy"

  local tarname="caddy-${CADDY_REF#v}-layer4-linux-${arch}.tar.gz"
  local tarball="$OUT_DIR/$tarname"
  ( cd "$out" && tar -czf "$tarball" caddy )
  echo "==> Packed $tarball"
}

for a in $ARCHES; do build_arch "$a"; done

echo "==> Appending checksums"
for t in "$OUT_DIR"/caddy-*-layer4-linux-*.tar.gz; do
  ( cd "$OUT_DIR" && sha256sum "$(basename "$t")" ) >> "$OUT_DIR/checksums.txt"
done

echo "==> Done. Artifacts in $OUT_DIR:"
ls -la "$OUT_DIR"/caddy-*-layer4-linux-*.tar.gz
tail -n 5 "$OUT_DIR/checksums.txt"
echo ""
echo "==> Next: publish the tarball(s) to the GitHub release the installer pins"
echo "    (gh release upload <release> deps/caddy-*-layer4-*.tar.gz) and mirror"
echo "    the sha256 into internal/chain/utility_install.go (caddyChecksums)."
