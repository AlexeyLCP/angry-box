#!/bin/sh
#
# Angry-BOX installer — installs the orchestrator on Linux (systemd) or Keenetic (NDMS).
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh
#
#   # Or with options:
#   sh install.sh --version 0.1.0
#   sh install.sh --local ./angry-box          # install from local binary
#   sh install.sh --no-start                    # don't start the service
#   sh install.sh --uninstall                   # remove angry-box
#
set -e

# ─── Defaults ──────────────────────────────────────────────────────────────────

VERSION="${VERSION:-0.2.0}"
LOCAL_BIN=""
NO_START=false
UNINSTALL=false
GITHUB_REPO="alexeylcp/angry-box"
BASE_URL="https://github.com/${GITHUB_REPO}/releases/download"

# ─── Parse args ────────────────────────────────────────────────────────────────

while [ $# -gt 0 ]; do
    case "$1" in
        --version)  VERSION="$2"; shift ;;
        --local)    LOCAL_BIN="$2"; shift ;;
        --no-start) NO_START=true ;;
        --uninstall) UNINSTALL=true ;;
        *)          echo "Unknown option: $1"; exit 1 ;;
    esac
    shift
done

# ─── Detect platform ───────────────────────────────────────────────────────────

detect_platform() {
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64)  TARGET="linux-amd64" ;;
        aarch64) TARGET="linux-arm64" ;;
        armv7l)  TARGET="linux-armv7" ;;
        mips*)   TARGET="keenetic-mipsel" ;;
        *)
            echo "Unsupported architecture: $ARCH"
            echo "Supported: x86_64, aarch64, armv7l, mips"
            exit 1
            ;;
    esac

    # Detect Keenetic by checking if /opt is the main filesystem root.
    if [ -d /opt/etc/init.d ] || [ -f /opt/etc/ndm/version ]; then
        IS_KEENETIC=true
    else
        IS_KEENETIC=false
    fi
}

# ─── Uninstall ─────────────────────────────────────────────────────────────────

do_uninstall() {
    echo "==> Uninstalling Angry-BOX..."

    if [ "$IS_KEENETIC" = true ]; then
        /opt/etc/init.d/S99angry-box stop 2>/dev/null || true
        rm -f /opt/etc/init.d/S99angry-box
        rm -f /opt/bin/angry-box
        rm -rf /opt/etc/angry-box
        rm -rf /opt/var/log/angry-box
    else
        systemctl stop angry-box 2>/dev/null || true
        systemctl disable angry-box 2>/dev/null || true
        rm -f /etc/systemd/system/angry-box.service
        systemctl daemon-reload 2>/dev/null || true
        rm -f /usr/local/bin/angry-box
        rm -rf /etc/angry-box
        rm -rf /var/lib/angry-box
    fi

    echo "Angry-BOX removed."
    exit 0
}

# ─── Install: get binary ───────────────────────────────────────────────────────

install_binary() {
    if [ -n "$LOCAL_BIN" ]; then
        echo "==> Installing from local binary: $LOCAL_BIN"
        if [ ! -f "$LOCAL_BIN" ]; then
            echo "ERROR: $LOCAL_BIN not found"
            exit 1
        fi
        cp "$LOCAL_BIN" "$INSTALL_PATH"
        chmod +x "$INSTALL_PATH"
        return
    fi

    echo "==> Downloading Angry-BOX ${VERSION} for ${TARGET}..."

    if [ "$VERSION" = "latest" ]; then
        ARCHIVE="angry-box-${TARGET}.tar.gz"
        URL="https://github.com/${GITHUB_REPO}/releases/latest/download/${ARCHIVE}"
    else
        ARCHIVE="angry-box-${VERSION}-${TARGET}.tar.gz"
        URL="${BASE_URL}/v${VERSION}/${ARCHIVE}"
    fi

    TMPDIR=$(mktemp -d)

    if command -v curl >/dev/null 2>&1; then
        curl -fsSL -L "$URL" -o "$TMPDIR/$ARCHIVE" || {
            echo "ERROR: Failed to download $URL"
            echo "Check that version v${VERSION} exists and target ${TARGET} is available."
            rm -rf "$TMPDIR"
            exit 1
        }
    elif command -v wget >/dev/null 2>&1; then
        wget -q --show-progress "$URL" -O "$TMPDIR/$ARCHIVE" || {
            echo "ERROR: Failed to download $URL"
            rm -rf "$TMPDIR"
            exit 1
        }
    else
        echo "ERROR: curl or wget required"
        rm -rf "$TMPDIR"
        exit 1
    fi

    tar xzf "$TMPDIR/$ARCHIVE" -C "$TMPDIR"
    cp "$TMPDIR/angry-box" "$INSTALL_PATH"
    chmod +x "$INSTALL_PATH"
    rm -rf "$TMPDIR"
}

# ─── Install: directories ──────────────────────────────────────────────────────

install_dirs() {
    echo "==> Creating directories..."
    mkdir -p "$CONFIG_DIR"
    mkdir -p "$DATA_DIR"
    mkdir -p "$LOG_DIR"

    # Create default store if it doesn't exist.
    if [ ! -f "$CONFIG_DIR/store.json" ]; then
        echo '{"hosts":[],"chains":[]}' > "$CONFIG_DIR/store.json"
    fi
}

# ─── Install: service ──────────────────────────────────────────────────────────

install_service() {
    if [ "$IS_KEENETIC" = true ]; then
        echo "==> Installing Keenetic init script..."
        cat > /opt/etc/init.d/S99angry-box << 'INIT_EOF'
#!/bin/sh
PATH=/opt/bin:/opt/sbin:/sbin:/bin:/usr/sbin:/usr/bin
DAEMON="/opt/bin/angry-box"
ARGS="serve --listen :8090 --file /opt/etc/angry-box/store.json"
PIDFILE="/opt/var/run/angry-box.pid"
LOGFILE="/opt/var/log/angry-box.log"

start() {
    echo "Starting Angry-BOX..."
    mkdir -p /opt/etc/angry-box /opt/var/run /opt/var/log
    $DAEMON $ARGS >> $LOGFILE 2>&1 &
    echo $! > $PIDFILE
}
stop() {
    echo "Stopping Angry-BOX..."
    if [ -f $PIDFILE ]; then
        kill $(cat $PIDFILE) 2>/dev/null
        rm -f $PIDFILE
    fi
}
case "$1" in
    start)   start ;;
    stop)    stop ;;
    restart) stop; sleep 1; start ;;
esac
exit 0
INIT_EOF
        chmod +x /opt/etc/init.d/S99angry-box
    else
        echo "==> Installing systemd service..."
        cat > /etc/systemd/system/angry-box.service << UNIT_EOF
[Unit]
Description=Angry-BOX proxy orchestrator
After=network.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/angry-box serve --listen :8090 --file /etc/angry-box/store.json
Restart=on-failure
RestartSec=5
LimitNOFILE=1048576
WorkingDirectory=/etc/angry-box

[Install]
WantedBy=multi-user.target
UNIT_EOF
        systemctl daemon-reload
        systemctl enable angry-box
    fi
}

# ─── Start ─────────────────────────────────────────────────────────────────────

start_service() {
    if [ "$IS_KEENETIC" = true ]; then
        echo "==> Starting Angry-BOX..."
        /opt/etc/init.d/S99angry-box start
    else
        echo "==> Starting Angry-BOX..."
        systemctl start angry-box
    fi
}

# ─── Print instructions ────────────────────────────────────────────────────────

print_done() {
    echo ""
    echo "=============================================="
    echo " Angry-BOX ${VERSION} installed successfully!"
    echo "=============================================="
    echo ""

    if [ "$IS_KEENETIC" = true ]; then
        echo "  Binary:     /opt/bin/angry-box"
        echo "  Config:     /opt/etc/angry-box/"
        echo "  Data:       /opt/var/lib/angry-box/"
        echo "  Logs:       /opt/var/log/angry-box.log"
        echo ""
        echo "  Control:"
        echo "    /opt/etc/init.d/S99angry-box {start|stop|restart}"
        echo ""
    else
        echo "  Binary:     /usr/local/bin/angry-box"
        echo "  Config:     /etc/angry-box/"
        echo "  Data:       /var/lib/angry-box/"
        echo ""
        echo "  Control:"
        echo "    systemctl status angry-box"
        echo "    systemctl {start|stop|restart} angry-box"
        echo ""
    fi

    echo "  Quick start:"
    echo "    angry-box host add mynode --addr <IP> --user root --key ~/.ssh/id_ed25519"
    echo "    angry-box deploy -addr <IP> -key ~/.ssh/id_ed25519"
    echo ""
    echo "  API:  http://localhost:8090/health"
    echo ""
    echo "  For routers (Keenetic / OpenWRT), prefer direct .ipk installation:"
    echo "    opkg install angry-box_0.2.0_mipsel_24kc.ipk        # Keenetic"
    echo "    opkg install angry-box_0.2.0_aarch64_cortex-a53.ipk # OpenWRT aarch64"
    echo ""
}

# ─── Main ──────────────────────────────────────────────────────────────────────

detect_platform

if [ "$IS_KEENETIC" = true ]; then
    INSTALL_PATH="/opt/bin/angry-box"
    CONFIG_DIR="/opt/etc/angry-box"
    DATA_DIR="/opt/var/lib/angry-box"
    LOG_DIR="/opt/var/log"
else
    INSTALL_PATH="/usr/local/bin/angry-box"
    CONFIG_DIR="/etc/angry-box"
    DATA_DIR="/var/lib/angry-box"
    LOG_DIR="/var/log"
fi

if [ "$UNINSTALL" = true ]; then
    do_uninstall
fi

echo "==> Detected: $TARGET ($([ "$IS_KEENETIC" = true ] && echo "Keenetic" || echo "Linux"))"
echo "==> Installing to: $INSTALL_PATH"

install_binary
install_dirs
install_service

if [ "$NO_START" != true ]; then
    start_service
else
    echo "==> Skipping service start (--no-start)"
fi

print_done
