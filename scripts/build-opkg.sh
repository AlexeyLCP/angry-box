#!/bin/sh
#
# Generic .ipk builder for OpenWRT / Entware / Keenetic
#
# Usage:
#   ./scripts/build-opkg.sh <binary> <version> <architecture> [output_dir]
#
# Examples:
#   ./scripts/build-opkg.sh dist/angry-box-keenetic-mipsel 0.2.0 mipsel_24kc dist
#   ./scripts/build-opkg.sh dist/angry-box-linux-arm64   0.2.0 aarch64_cortex-a53 dist
#
# Supported architectures (common for OpenWRT/Entware):
#   mipsel_24kc, aarch64_cortex-a53, arm_cortex-a7, etc.

set -e

if [ $# -lt 3 ]; then
    echo "Usage: $0 <binary> <version> <architecture> [output_dir]"
    echo ""
    echo "Examples:"
    echo "  $0 dist/angry-box-keenetic-mipsel 0.2.0 mipsel_24kc"
    echo "  $0 dist/angry-box-linux-arm64     0.2.0 aarch64_cortex-a53"
    exit 1
fi

BINARY="$1"
VERSION="$2"
ARCH="$3"
OUTDIR="${4:-dist}"

if [ ! -f "$BINARY" ]; then
    echo "Error: binary not found: $BINARY"
    exit 1
fi

mkdir -p "$OUTDIR"

PKG_NAME="angry-box"
MAINTAINER="Alexey L. <github@alexeylcp>"
DESCRIPTION="Lightweight proxy orchestrator for sing-box and xray with advanced obfuscation."

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# --- Prepare data directory (standard OpenWRT/Entware layout) ---
DATA="$WORK/data"
mkdir -p "$DATA/opt/bin" "$DATA/opt/etc/init.d"

cp "$BINARY" "$DATA/opt/bin/angry-box"
chmod 755 "$DATA/opt/bin/angry-box"

# Install init script if present
if [ -f "scripts/S99angry-box" ]; then
    cp scripts/S99angry-box "$DATA/opt/etc/init.d/S99angry-box"
    chmod 755 "$DATA/opt/etc/init.d/S99angry-box"
else
    echo "Warning: scripts/S99angry-box not found"
fi

# --- Create control file ---
CONTROL="$WORK/control"
mkdir -p "$CONTROL"

cat > "$CONTROL/control" << EOF
Package: $PKG_NAME
Version: $VERSION
Depends: 
Section: net
Architecture: $ARCH
Maintainer: $MAINTAINER
Description: $DESCRIPTION
EOF

cat > "$CONTROL/postinst" << 'EOF'
#!/bin/sh
set -e

BIN="/opt/bin/angry-box"
INIT="/opt/etc/init.d/S99angry-box"

[ -f "$BIN" ] && chmod 755 "$BIN"
[ -f "$INIT" ] && chmod 755 "$INIT"

echo "Angry-BOX installed. Run '$INIT start' to begin."
echo "To enable autostart: $INIT enable"
EOF
chmod 755 "$CONTROL/postinst"

# --- Create tarballs ---
tar -C "$DATA" -czf "$WORK/data.tar.gz" .
tar -C "$CONTROL" -czf "$WORK/control.tar.gz" .

echo "2.0" > "$WORK/debian-binary"

# --- Build .ipk ---
IPK_NAME="${PKG_NAME}_${VERSION}_${ARCH}.ipk"
ar -r "$OUTDIR/$IPK_NAME" "$WORK/debian-binary" "$WORK/control.tar.gz" "$WORK/data.tar.gz" >/dev/null

echo "Created: $OUTDIR/$IPK_NAME"
ls -lh "$OUTDIR/$IPK_NAME"