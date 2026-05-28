#!/bin/sh
#
# Build a proper .ipk package for Keenetic / Entware (mipsel)
#
# Usage:
#   ./scripts/build-keenetic-opkg.sh <binary> <version> [output_dir]
#
# Example:
#   ./scripts/build-keenetic-opkg.sh dist/angry-box-keenetic-mipsel 0.2.0 dist

set -e

if [ $# -lt 2 ]; then
    echo "Usage: $0 <binary> <version> [output_dir]"
    exit 1
fi

BINARY="$1"
VERSION="$2"
OUTDIR="${3:-dist}"

if [ ! -f "$BINARY" ]; then
    echo "Error: binary not found: $BINARY"
    exit 1
fi

mkdir -p "$OUTDIR"

PKG_NAME="angry-box"
ARCH="mipsel_24kc"
MAINTAINER="Alexey L. <github@alexeylcp>"
DESCRIPTION="Lightweight proxy orchestrator for sing-box and xray with advanced obfuscation."

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# --- Prepare data directory ---
DATA="$WORK/data"
mkdir -p "$DATA/opt/bin" "$DATA/opt/etc/init.d"

cp "$BINARY" "$DATA/opt/bin/angry-box"
chmod 755 "$DATA/opt/bin/angry-box"

# Install the init script (we expect S99angry-box to exist in the repo)
if [ -f "scripts/S99angry-box" ]; then
    cp scripts/S99angry-box "$DATA/opt/etc/init.d/S99angry-box"
    chmod 755 "$DATA/opt/etc/init.d/S99angry-box"
else
    echo "Warning: scripts/S99angry-box not found, package will be missing init script"
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