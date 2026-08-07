#!/usr/bin/env bash
# Build the amnezia-box sing-box WINDOWS binary for use as a local e2e client.
# Mirrors build-singbox.sh but targets GOOS=windows and emits a .exe (no tarball).
# amnezia-box (our fork AlexeyLCP/amnezia-box) is our base sing-box — see
# build-singbox.sh for the full rationale (no patches/ to apply; fallback is in
# the fork's tree; amneziawg-go is pinned in the fork's go.mod).
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"
ABX_REF="${ABX_REF:-3c554273}"
BUILD_DIR="${BUILD_DIR:-/tmp/sing-box-build-angry}"
OUT="$REPO_ROOT/deps/sing-box.exe"
mkdir -p "$BUILD_DIR"
cd "$BUILD_DIR"
if [ ! -d amnezia-box ]; then
  git clone --quiet --no-checkout https://github.com/AlexeyLCP/amnezia-box.git amnezia-box
fi
( cd amnezia-box && git fetch --quiet origin && git checkout --quiet "$ABX_REF" )
BUILD_TAGS="with_gvisor,with_quic,with_wireguard,with_utls,with_awg,with_mtproxy,with_acme,with_clash_api,with_tailscale,with_openvpn"
echo "==> Building amnezia-box @ $ABX_REF for windows/amd64"
( cd amnezia-box && \
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOFLAGS=-mod=mod \
  go build -ldflags "-s -w" -tags "$BUILD_TAGS" -o "$OUT" ./cmd/sing-box )
echo "==> Done: $OUT"
"$OUT" version | head -2 || true