#!/bin/bash
# build-ipk.sh — unified router package builder for Angry-BOX (Keenetic Entware
# + OpenWrt opkg). Cross-compiles the orchestrator (pure Go, CGO off), strips
# symbols, optionally UPX-compresses, and assembles an .ipk with the
# platform-specific service wiring:
#
#   Keenetic (-kn): /opt/bin + Entware S99 init + NDMS hook scripts
#                   (/opt/etc/ndm/{iflayerchanged,ifcreated,ifdestroyed,ifipchanged}.d/)
#                   forwarding interface events to the panel's loopback API.
#   OpenWrt:        /usr/bin + procd-style init, no NDMS hooks.
#
# Usage:
#   ./scripts/build-ipk.sh <version> <target> [output_dir]
#   target: keenetic-mipsel-3.4 | keenetic-mips-3.4 | keenetic-aarch64-3.10
#           openwrt-mipsel_24kc | openwrt-aarch64_cortex-a53
# Env:
#   SKIP_UPX=1  — skip UPX compression (debug)
#   COMMIT/DATE — injected into -X main.commit/main.date when set
set -euo pipefail

VERSION=${1:-}
TARGET=${2:-}
OUT=${3:-./release}
[ -z "$VERSION" ] || [ -z "$TARGET" ] && { echo "Usage: $0 <version> <target> [outdir]"; exit 1; }

COMMIT=${COMMIT:-dev}
DATE=${DATE:-$(date -u +%Y-%m-%d)}

GOARCH=""; GOMIPS=""; PKGARCH=""; FLAVOR=""
case "$TARGET" in
  keenetic-mipsel-3.4|mipsel-3.4)     GOARCH=mipsle; GOMIPS=softfloat; PKGARCH=mipsel-3.4-kn;      FLAVOR=keenetic ;;
  keenetic-mips-3.4|mips-3.4)         GOARCH=mips;   GOMIPS=softfloat; PKGARCH=mips-3.4-kn;        FLAVOR=keenetic ;;
  keenetic-aarch64-3.10|aarch64-3.10) GOARCH=arm64;                    PKGARCH=aarch64-3.10-kn;    FLAVOR=keenetic ;;
  openwrt-mipsel_24kc|mipsel_24kc)    GOARCH=mipsle; GOMIPS=softfloat; PKGARCH=mipsel_24kc;        FLAVOR=openwrt ;;
  openwrt-aarch64_cortex-a53|aarch64_cortex-a53) GOARCH=arm64;         PKGARCH=aarch64_cortex-a53; FLAVOR=openwrt ;;
  *) echo "Unknown target: $TARGET"; exit 1 ;;
esac

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(dirname "$SCRIPT_DIR")"
cd "$ROOT"

OUT=$(mkdir -p "$OUT" && cd "$OUT" && pwd -P)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
BIN="$WORK/angry-box"

echo "==> go build $VERSION for linux/$GOARCH $GOMIPS ($FLAVOR)"
MIPSFLAGS=""
[ -n "$GOMIPS" ] && MIPSFLAGS="GOMIPS=$GOMIPS"
env GOOS=linux GOARCH="$GOARCH" $MIPSFLAGS CGO_ENABLED=0 \
  go build -trimpath \
  -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
  -o "$BIN" ./cmd/angry-box

RAW_SIZE=$(stat -c%s "$BIN")
echo "==> stripped binary: $((RAW_SIZE/1024/1024)) MiB"

if [ "${SKIP_UPX:-0}" != "1" ] && command -v upx >/dev/null 2>&1; then
  echo "==> upx --best --lzma"
  upx --best --lzma "$BIN" >/dev/null 2>&1 || echo "    (upx failed — shipping uncompressed)"
fi
echo "==> final binary: $(( $(stat -c%s "$BIN") /1024/1024 )) MiB"

# Keep the raw binary alongside the .ipk (CI smoke-tests it under qemu-user;
# also handy for manual scp installs).
mkdir -p dist
cp "$BIN" "dist/angry-box-${PKGARCH}"

PKG_DIR="$WORK/pkg"
mkdir -p "$PKG_DIR/CONTROL"

if [ "$FLAVOR" = "keenetic" ]; then
  mkdir -p "$PKG_DIR/opt/bin" "$PKG_DIR/opt/etc/init.d" "$PKG_DIR/opt/etc/angry-box"
  cp "$BIN" "$PKG_DIR/opt/bin/angry-box"; chmod 755 "$PKG_DIR/opt/bin/angry-box"
  cp "$SCRIPT_DIR/S99angry-box" "$PKG_DIR/opt/etc/init.d/S99angry-box"; chmod 755 "$PKG_DIR/opt/etc/init.d/S99angry-box"
  for hook in iflayerchanged ifcreated ifdestroyed ifipchanged; do
    mkdir -p "$PKG_DIR/opt/etc/ndm/${hook}.d"
    cp "$SCRIPT_DIR/ndm-hook.sh" "$PKG_DIR/opt/etc/ndm/${hook}.d/50-angry-box.sh"
    chmod 755 "$PKG_DIR/opt/etc/ndm/${hook}.d/50-angry-box.sh"
  done
  BINPATH=/opt/bin/angry-box
  INIT=/opt/etc/init.d/S99angry-box
else
  mkdir -p "$PKG_DIR/usr/bin" "$PKG_DIR/etc/init.d" "$PKG_DIR/etc/angry-box"
  cp "$BIN" "$PKG_DIR/usr/bin/angry-box"; chmod 755 "$PKG_DIR/usr/bin/angry-box"
  cp "$SCRIPT_DIR/openwrt-init.d" "$PKG_DIR/etc/init.d/angry-box"; chmod 755 "$PKG_DIR/etc/init.d/angry-box"
  BINPATH=/usr/bin/angry-box
  INIT=/etc/init.d/angry-box
fi

cat > "$PKG_DIR/CONTROL/control" <<EOF
Package: angry-box
Version: ${VERSION}
Architecture: ${PKGARCH}
Maintainer: Alexey LCP <alexey@lucx.io>
Section: net
Priority: optional
Description: SSH-only orchestrator for anti-DPI proxy infrastructure (sing-box-extended nodes: AmneziaWG / VLESS Reality+XHTTP / MTProxy). Control plane — binds loopback only.
Depends: libc, libgcc, ca-bundle
EOF

cat > "$PKG_DIR/CONTROL/postinst" <<EOF
#!/bin/sh
set -e
mkdir -p /opt/etc/angry-box /etc/angry-box 2>/dev/null || true
chmod 755 "$BINPATH" "$INIT" 2>/dev/null || true
for hook in iflayerchanged ifcreated ifdestroyed ifipchanged; do
  chmod 755 "/opt/etc/ndm/\${hook}.d/50-angry-box.sh" 2>/dev/null || true
done
"$INIT" enable 2>/dev/null || true
"$INIT" start  2>/dev/null || true
IP=\$(ip -4 addr show br0 2>/dev/null | awk '/inet /{split(\$2,a,"/"); print a[1]; exit}')
[ -z "\$IP" ] && IP="<router-ip>"
echo "==================================================="
echo "  Angry-BOX installed. Panel: http://\${IP}:9080"
echo "  (loopback only — reach it via the router's admin"
echo "   session or an SSH tunnel)"
echo "==================================================="
exit 0
EOF
chmod 755 "$PKG_DIR/CONTROL/postinst"

cat > "$PKG_DIR/CONTROL/prerm" <<EOF
#!/bin/sh
"$INIT" stop    2>/dev/null || true
"$INIT" disable 2>/dev/null || true
exit 0
EOF
chmod 755 "$PKG_DIR/CONTROL/prerm"

cd "$PKG_DIR"
tar --owner=0 --group=0 -czf control.tar.gz CONTROL
if [ "$FLAVOR" = "keenetic" ]; then
  tar --owner=0 --group=0 -czf data.tar.gz opt
else
  tar --owner=0 --group=0 -czf data.tar.gz usr etc
fi
echo "2.0" > debian-binary

IPK="${OUT}/angry-box_${VERSION}_${PKGARCH}.ipk"
rm -f "$IPK"
ar -r -c "$IPK" debian-binary
ar -r "$IPK" control.tar.gz
ar -r "$IPK" data.tar.gz

echo "==> created $IPK"
ls -lh "$IPK"
