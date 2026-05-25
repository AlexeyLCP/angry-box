#!/usr/bin/env bash
# package-keenetic.sh — Create .ipk package and NDMS-ready tarball for Keenetic routers
#
# Usage: ./scripts/package-keenetic.sh [version]
#   version defaults to: git describe --tags --always --dirty

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILD_DIR="$ROOT/build"
APP="lucx-core"
VERSION="${1:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo 'dev')}"

# ── Input binaries ──
BIN_MIPSEL="$BUILD_DIR/${APP}-keenetic-mipsel"
BIN_MIPS="$BUILD_DIR/${APP}-openwrt-mips"

if [[ ! -f "$BIN_MIPSEL" ]]; then
    echo "ERROR: $BIN_MIPSEL not found. Run 'make keenetic' first."
    exit 1
fi

echo "=== LucX Keenetic Packager ==="
echo "Version: $VERSION"
echo ""

# ══════════════════════════════════════════════════════
# .ipk package (mipsel — primary Keenetic)
# ══════════════════════════════════════════════════════

create_ipk() {
    local arch="$1"      # mipsel or mips
    local bin_path="$2"
    local ipk_name="${APP}_${VERSION}_${arch}.ipk"
    local ipk_dir="$BUILD_DIR/ipk-${arch}"

    echo "── Creating .ipk: $ipk_name ──"

    rm -rf "$ipk_dir"
    mkdir -p "$ipk_dir/data/opt/bin"
    mkdir -p "$ipk_dir/data/opt/etc/init.d"
    mkdir -p "$ipk_dir/data/opt/var/run"
    mkdir -p "$ipk_dir/control"

    # Binary
    cp "$bin_path" "$ipk_dir/data/opt/bin/${APP}"
    chmod 755 "$ipk_dir/data/opt/bin/${APP}"

    # init.d script (NDMS-compatible)
    cat > "$ipk_dir/data/opt/etc/init.d/S99lucx" << 'INIT'
#!/bin/sh
# LucX daemon for Keenetic NDMS
PATH="/opt/bin:/opt/sbin:/bin:/sbin:/usr/bin:/usr/sbin"

start() {
    echo "Starting LucX..."
    /opt/bin/lucx-core -listen :8744 -db /opt/var/run/lucx.db &
}

stop() {
    echo "Stopping LucX..."
    killall lucx-core 2>/dev/null || true
}

case "$1" in
    start)   start ;;
    stop)    stop ;;
    restart) stop; sleep 1; start ;;
    *)       echo "Usage: $0 {start|stop|restart}"; exit 1 ;;
esac
INIT
    chmod 755 "$ipk_dir/data/opt/etc/init.d/S99lucx"

    # control file
    cat > "$ipk_dir/control/control" << EOF
Package: lucx-core
Version: ${VERSION}
Architecture: ${arch}
Maintainer: LucX Project
Description: Personal Multi-Hop Xray Orchestrator
Section: net
Priority: optional
Depends: libc
Source: https://github.com/AlexeyLCP/lucx
EOF

    # postinst script — runs after install
    cat > "$ipk_dir/control/postinst" << 'POSTINST'
#!/bin/sh
echo "LucX installed. Start with: /opt/etc/init.d/S99lucx start"
echo "Web UI: http://<router-ip>:8744"
echo "Default password: lucx-dev-secret-change-me"
POSTINST
    chmod 755 "$ipk_dir/control/postinst"

    # prerm script — runs before uninstall
    cat > "$ipk_dir/control/prerm" << 'PRERM'
#!/bin/sh
/opt/etc/init.d/S99lucx stop 2>/dev/null || true
PRERM
    chmod 755 "$ipk_dir/control/prerm"

    # debian-binary
    echo "2.0" > "$ipk_dir/debian-binary"

    # Package
    cd "$ipk_dir"
    tar czf "control.tar.gz" -C control .
    tar czf "data.tar.gz" -C data .
    tar czf "../${ipk_name}" ./debian-binary ./control.tar.gz ./data.tar.gz
    cd - > /dev/null

    rm -rf "$ipk_dir"
    echo "  → $BUILD_DIR/$ipk_name"
    ls -lh "$BUILD_DIR/$ipk_name"
}

# ══════════════════════════════════════════════════════
# NDMS-ready tarball (with install script)
# ══════════════════════════════════════════════════════

create_ndms_tarball() {
    local target="$1"    # keenetic-mipsel or openwrt-mips
    local bin_path="$2"
    local tar_name="${APP}-${VERSION}-${target}"
    local tar_dir="$BUILD_DIR/${tar_name}"

    echo ""
    echo "── Creating NDMS tarball: ${tar_name}.tar.gz ──"

    rm -rf "$tar_dir"
    mkdir -p "$tar_dir"

    cp "$bin_path" "$tar_dir/${APP}"
    chmod 755 "$tar_dir/${APP}"

    # install.sh — user runs this on the router
    cat > "$tar_dir/install.sh" << 'SCRIPT'
#!/bin/sh
# LucX NDMS Install Script
set -e

BIN_DIR="${BIN_DIR:-/opt/bin}"
CONF_DIR="${CONF_DIR:-/opt/var/run}"
INIT_DIR="${INIT_DIR:-/opt/etc/init.d}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "LucX NDMS Installer"
echo "===================="
echo "Bin dir:  $BIN_DIR"
echo "Conf dir: $CONF_DIR"
echo "Init dir: $INIT_DIR"
echo ""

mkdir -p "$BIN_DIR" "$CONF_DIR" "$INIT_DIR"

# Install binary
cp "$SCRIPT_DIR/lucx-core" "$BIN_DIR/lucx-core"
chmod +x "$BIN_DIR/lucx-core"
echo "✓ Binary installed: $BIN_DIR/lucx-core"

# Install init script
cat > "$INIT_DIR/S99lucx" << 'INIT'
#!/bin/sh
PATH="/opt/bin:/opt/sbin:/bin:/sbin:/usr/bin:/usr/sbin"

start() {
    echo "Starting LucX..."
    /opt/bin/lucx-core -listen :8744 -db /opt/var/run/lucx.db &
}

stop() {
    echo "Stopping LucX..."
    killall lucx-core 2>/dev/null || true
}

case "$1" in
    start)   start ;;
    stop)    stop ;;
    restart) stop; sleep 1; start ;;
    *)       echo "Usage: $0 {start|stop|restart}"; exit 1 ;;
esac
INIT
chmod +x "$INIT_DIR/S99lucx"
echo "✓ Init script: $INIT_DIR/S99lucx"

echo ""
echo "Installation complete!"
echo ""
echo "Start LucX:  $INIT_DIR/S99lucx start"
echo "Web UI:      http://<router-ip>:8744"
echo "Password:    lucx-dev-secret-change-me"
echo ""
echo "⚠️  Change password with -jwt-secret flag:"
echo "   killall lucx-core"
echo "   /opt/bin/lucx-core -listen :8744 -db /opt/var/run/lucx.db -jwt-secret \"\$(openssl rand -hex 32)\" &"
SCRIPT
    chmod +x "$tar_dir/install.sh"

    # README for manual install
    cat > "$tar_dir/KEENETIC.txt" << 'DOC'
LucX on Keenetic — Quick Guide
===============================

Requirements:
  - Keenetic router with OPKG/Entware installed
  - At least 20 MB free space on /opt
  - SSH access to the router

Automatic install:
  $ chmod +x install.sh && ./install.sh

Manual install:
  1. Copy binary:
     $ cp lucx-core /opt/bin/lucx-core
     $ chmod +x /opt/bin/lucx-core

  2. Test run:
     $ /opt/bin/lucx-core -listen :8744 -db /opt/var/run/lucx.db

  3. Auto-start: copy S99lucx to /opt/etc/init.d/

NDMS paths:
  /opt/bin/           — user binaries
  /opt/etc/init.d/    — startup scripts (S99* runs on boot)
  /opt/var/run/       — runtime data (databases, sockets)

Logs:
  LucX logs to stdout. Redirect with:
  $ /opt/bin/lucx-core ... > /opt/var/log/lucx.log 2>&1 &

OPKG install (.ipk):
  $ opkg install lucx-core_<version>_mipsel.ipk
DOC

    chmod 755 "$tar_dir"
    tar czf "$BUILD_DIR/${tar_name}.tar.gz" -C "$BUILD_DIR" "$tar_name"
    rm -rf "$tar_dir"

    echo "  → $BUILD_DIR/${tar_name}.tar.gz"
    ls -lh "$BUILD_DIR/${tar_name}.tar.gz"
}

# ══════════════════════════════════════════════════════
# Main
# ══════════════════════════════════════════════════════

# mipsel — primary Keenetic
create_ipk "mipsel" "$BIN_MIPSEL"
create_ndms_tarball "keenetic-mipsel" "$BIN_MIPSEL"

# mips big endian — older Keenetic / OpenWrt
if [[ -f "$BIN_MIPS" ]]; then
    create_ipk "mips" "$BIN_MIPS"
    create_ndms_tarball "openwrt-mips" "$BIN_MIPS"
fi

echo ""
echo "=== Done ==="
echo "Packages:"
ls -lh "$BUILD_DIR"/*.ipk "$BUILD_DIR"/*.tar.gz 2>/dev/null || echo "  (no packages found)"
