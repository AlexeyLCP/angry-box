#!/usr/bin/env bash
# Build the userspace AmneziaWG-go daemon (feat/awg3) binary for remote nodes.
#
# Why: the kernel amneziawg module (the default AWG backend) requires a patched
# kernel / DKMS build that is not always available — OpenVZ, restricted
# kernels, containers, macOS clients. The userspace amneziawg-go daemon is the
# fallback AWG backend. It also carries the AWG3 obfuscation fields
# (HeaderProtectionKey / ContentPaddingMultiple / RekeyAfterTime) that the
# kernel module does NOT parse (feat/awg3 is not merged upstream).
#
# We build once on a dev machine and store the tarball in deps/ so
# `angry-box deploy` just downloads it (weak VPSes never compile Go — same
# contract as build-singbox.sh).
#
# Patches applied (optional, only if patches/amneziawg-go-*.patch exists):
#   none currently — feat/awg3 @ SHA 898bc6b8 builds clean. If a patch is
#   needed later, drop it in patches/ and add a git-apply step here (mirror
#   build-singbox.sh's patch handling).
#
# Output: deps/amneziawg-go-{VER}-linux-{ARCH}.tar.gz
#         deps/amneziawg-go-checksums.txt  (sha256 per tarball, separate from
#         the sing-box checksums.txt to keep the two release artifacts distinct)
#
# Usage:
#   ./scripts/build-amneziawg-go.sh             # amd64 only (default)
#   ARCHES="amd64 arm64" ./scripts/build-amneziawg-go.sh
#   AWGGO_REF=898bc6b8 ./scripts/build-amneziawg-go.sh
#
# Requires: go (host), git, curl, tar, sha256sum. Run on Linux or in WSL.
#
# Pin: AWGGO_REF is a commit SHA (NOT a branch) — feat/awg3 is not merged
# upstream and the API of the AWG3 fields can change between commits. Pin the
# exact SHA so a bump is a deliberate act. The SHA is mirrored in
# internal/backend/singbox/singbox.go (amneziaWGGoVersion) and
# internal/backend/singbox/patchcheck_test.go (patchcheckAWGGORef) — update all
# three together (see docs/PATCHES.md).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

# feat/awg3 branch HEAD at the time of the n1 spike (§30 PROGRESS.md).
AWGGO_REF="${AWGGO_REF:-898bc6b83b9ed8148b170bf85c5f953201ff2120}"
ARCHES="${ARCHES:-amd64}"

BUILD_DIR="${BUILD_DIR:-/tmp/amneziawg-go-build-angry}"
OUT_DIR="$REPO_ROOT/deps"
mkdir -p "$OUT_DIR"

AWGGO_REPO="https://github.com/amnezia-vpn/amneziawg-go.git"

echo "==> Preparing amneziawg-go source in $BUILD_DIR"
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"
cd "$BUILD_DIR"

# Shallow-fetch the pinned commit. --depth 1 with a SHA needs a fetch of the
# ref into FETCH_HEAD; clone the branch then checkout the SHA to be robust.
git clone --quiet --no-checkout "$AWGGO_REPO" amneziawg-go
( cd amneziawg-go && git fetch --quiet origin "$AWGGO_REF" && git checkout --quiet "$AWGGO_REF" )

# Apply any optional patches against amneziawg-go. Currently none — feat/awg3
# @ 898bc6b8 builds clean. If a patch is added later, drop it in patches/ and
# uncomment the apply block (mirror build-singbox.sh).
# for p in "$REPO_ROOT"/patches/amneziawg-go-*.patch; do
#   [ -e "$p" ] || continue
#   echo "==> Applying $(basename "$p")"
#   ( cd amneziawg-go && git apply "$p" )
# done

# Version string for the tarball name. amneziawg-go does not tag releases on
# feat/awg3; use the short SHA so the artifact is traceable to the pinned commit.
VERSION_PLAIN="$(cd amneziawg-go && git rev-parse --short HEAD)"

build_arch() {
  local arch="$1"
  local goarch="$arch"
  local out="$BUILD_DIR/out-$arch"
  mkdir -p "$out"

  echo "==> Building for linux/$arch (amneziawg-go @ $VERSION_PLAIN)"
  ( cd amneziawg-go && \
    CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" \
    go build -ldflags "-s -w" \
      -o "$out/amneziawg-go" . )

  # The binary is a single executable named amneziawg-go. The entrypoint is
  # ./main.go (not ./cmd/amneziawg-go) per the repo layout.
  local tarname="amneziawg-go-${VERSION_PLAIN}-linux-${arch}.tar.gz"
  local tarball="$OUT_DIR/$tarname"
  ( cd "$out" && tar -czf "$tarball" amneziawg-go )
  echo "==> Packed $tarball"
}

for a in $ARCHES; do build_arch "$a"; done

echo "==> Writing checksums"
# Separate checksum file so the sing-box release artifacts stay untouched.
CHECKSUMS="$OUT_DIR/amneziawg-go-checksums.txt"
: > "$CHECKSUMS"
for t in "$OUT_DIR"/amneziawg-go-*-linux-*.tar.gz; do
  ( cd "$OUT_DIR" && sha256sum "$(basename "$t")" ) >> "$CHECKSUMS"
done

echo "==> Done. Artifacts in $OUT_DIR:"
ls -la "$OUT_DIR"/amneziawg-go-*-linux-*.tar.gz "$CHECKSUMS"
cat "$CHECKSUMS"
echo ""
echo "==> Next: publish the tarball(s) to the GitHub release the deploy code"
echo "    pins (gh release upload v0.1.0 deps/amneziawg-go-*.tar.gz) and"
echo "    mirror the short SHA + sha256 into:"
echo "      internal/backend/singbox/singbox.go (amneziaWGGoVersion/URLs/Checksums)"
echo "      internal/backend/singbox/patchcheck_test.go (patchcheckAWGGORef)"