#!/usr/bin/env bash
# Build a patched sing-box-extended WINDOWS binary for use as a local e2e client.
# Mirrors build-singbox.sh but targets GOOS=windows and emits a .exe (no tarball).
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
SBX_VERSION="${SBX_VERSION:-v1.13.14-extended-2.5.0}"
WG_TAG="${WG_TAG:-v0.0.2-beta.1-extended-1.4.3}"
BUILD_DIR="${BUILD_DIR:-/tmp/sing-box-build-angry}"
OUT="$REPO_ROOT/deps/sing-box.exe"
mkdir -p "$BUILD_DIR"
cd "$BUILD_DIR"
if [ ! -d sing-box-extended ]; then
  git clone --depth 1 --branch "$SBX_VERSION" https://github.com/shtorm-7/sing-box-extended.git sing-box-extended
fi
if [ ! -d wireguard-go-patched ]; then
  git clone --depth 1 --branch "$WG_TAG" https://github.com/shtorm-7/wireguard-go.git wireguard-go-patched
fi
( cd sing-box-extended && git checkout -- . && git apply "$REPO_ROOT/patches/fallback-round-robin.patch" )
( cd wireguard-go-patched && git checkout -- . && git apply "$REPO_ROOT/patches/wireguard-go-awg-overlap.patch" )
( cd sing-box-extended && go mod edit -replace github.com/sagernet/wireguard-go=../wireguard-go-patched && go mod tidy )
BUILD_TAGS="with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_masque,with_mtproxy,with_openvpn,with_trusttunnel,with_sudoku,with_snell,with_manager,with_profiler"
echo "==> Building sing-box-extended for windows/amd64"
( cd sing-box-extended && \
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -ldflags "-s -w" -tags "$BUILD_TAGS" -o "$OUT" ./cmd/sing-box )
echo "==> Done: $OUT"
"$OUT" version | head -2 || true
