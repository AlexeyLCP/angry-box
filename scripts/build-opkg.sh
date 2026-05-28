#!/bin/bash
#
# Universal opkg / .ipk builder for Angry-BOX
# Supports Keenetic (mipsel_24kc) and OpenWRT (aarch64_cortex-a53, arm64, etc.)
#
# Usage:
#   ./scripts/build-opkg.sh <binary> <arch> <version> [output_dir]
#
# Example (after cross-build):
#   ./scripts/build-opkg.sh dist/angry-box-keenetic-mipsel mipsel_24kc 0.2.0 ./release
#
set -euo pipefail

BIN=${1:-}
ARCH=${2:-}
VERSION=${3:-0.2.0}
OUT=${4:-./release}

if [[ -z "$BIN" || -z "$ARCH" ]]; then
  echo "Usage: $0 <binary> <arch> <version> [outdir]"
  echo "  arch examples: mipsel_24kc  aarch64_cortex-a53  arm64"
  exit 1
fi

if [[ ! -f "$BIN" ]]; then
  echo "Binary not found: $BIN"
  exit 1
fi

# Resolve OUT to absolute path *before* any cd, so relative paths from caller work correctly
OUT=$(cd "$(dirname "$OUT")" && pwd -P)/$(basename "$OUT")
mkdir -p "$OUT"
PKG_NAME="angry-box_${VERSION}_${ARCH}"
PKG_DIR="/tmp/${PKG_NAME}"
rm -rf "$PKG_DIR"
mkdir -p "$PKG_DIR"/{CONTROL,usr/bin,opt/etc/angry-box,etc/init.d}

# Copy binary
cp "$BIN" "$PKG_DIR/usr/bin/angry-box"
chmod 755 "$PKG_DIR/usr/bin/angry-box"

# Control file (opkg metadata)
cat > "$PKG_DIR/CONTROL/control" <<EOF
Package: angry-box
Version: ${VERSION}
Architecture: ${ARCH}
Maintainer: Alexey LCP <alexey@lucx.io>
Section: net
Priority: optional
Description: Lightweight SSH-only orchestrator for sing-box (primary) and xray on remote nodes / routers.
Depends: libc, libgcc, ca-bundle
EOF

# Post-install script (creates dirs, enables service on Keenetic/OpenWRT)
cat > "$PKG_DIR/CONTROL/postinst" <<'POSTINST'
#!/bin/sh
set -e

mkdir -p /opt/etc/angry-box /etc/angry-box 2>/dev/null || true
chmod 755 /usr/bin/angry-box 2>/dev/null || chmod 755 /opt/bin/angry-box 2>/dev/null || true

# Try to register as service (Entware / OpenWRT style)
if [ -x /etc/init.d/angry-box ]; then
  /etc/init.d/angry-box enable || true
fi

echo "Angry-BOX installed. Run 'angry-box --help' or configure via web UI on :8090"
exit 0
POSTINST
chmod 755 "$PKG_DIR/CONTROL/postinst"

# Minimal init script (S99 for Keenetic Entware compatibility)
cat > "$PKG_DIR/etc/init.d/S99angry-box" <<'INIT'
#!/bin/sh
# Entware / Keenetic init script for angry-box

BIN="/usr/bin/angry-box"
[ -x /opt/bin/angry-box ] && BIN="/opt/bin/angry-box"

case "$1" in
  start)
    $BIN serve --config /opt/etc/angry-box/config.toml >/dev/null 2>&1 &
    echo $! > /var/run/angry-box.pid
    ;;
  stop)
    [ -f /var/run/angry-box.pid ] && kill $(cat /var/run/angry-box.pid) 2>/dev/null || true
    rm -f /var/run/angry-box.pid
    ;;
  *)
    echo "Usage: $0 {start|stop}"
    exit 1
    ;;
esac
INIT
chmod 755 "$PKG_DIR/etc/init.d/S99angry-box"

# Build the .ipk (ar + gzip, standard opkg format)
cd "$PKG_DIR"
tar --owner=0 --group=0 -czf control.tar.gz CONTROL
tar --owner=0 --group=0 -czf data.tar.gz usr etc
echo "2.0" > debian-binary

IPK="${OUT}/angry-box_${VERSION}_${ARCH}.ipk"

# Robust .ipk creation for CI runners (GitHub Actions, etc.)
# Using sequential 'ar r' after ensuring the archive exists often works better
# than a single multi-file command on some binutils versions.
touch "$IPK"
ar r "$IPK" debian-binary || { echo "ERROR: ar failed on debian-binary"; ls -l "$IPK"; exit 1; }
ar r "$IPK" control.tar.gz || { echo "ERROR: ar failed on control.tar.gz"; exit 1; }
ar r "$IPK" data.tar.gz    || { echo "ERROR: ar failed on data.tar.gz"; exit 1; }

rm -f control.tar.gz data.tar.gz debian-binary
rm -rf "$PKG_DIR"

echo "Created $IPK"
ls -lh "$IPK"
