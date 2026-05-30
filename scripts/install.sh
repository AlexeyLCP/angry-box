#!/bin/sh
#
# Angry-BOX installer — installs the orchestrator on Linux (systemd) or Keenetic (NDMS).
#
# License: PolyForm Noncommercial License 1.0.0
# Permitted use: personal, non-commercial, educational, and scientific purposes only.
# Any commercial use is prohibited. No warranty. Use at your own risk.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh | sh
#
#   # Or with options:
#   sh install.sh --version X.Y.Z
#   sh install.sh --local ./angry-box          # install from local binary
#   sh install.sh --no-start                    # don't start the service
#   sh install.sh --uninstall                   # remove angry-box
#
set -e

# ─── Defaults ──────────────────────────────────────────────────────────────────

VERSION="${VERSION:-latest}"
LOCAL_BIN=""
NO_START=false
UNINSTALL=false
USER_MODE=false
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

# ─── Windows detection (this script is Unix-only) ──────────────────────────────

case "$(uname -s)" in
    *MINGW*|*MSYS*|*CYGWIN*|*Windows*)
        echo "ERROR: This installer does not support native Windows."
        echo ""
        echo "Please download the Windows package instead:"
        echo "  https://github.com/alexeylcp/angry-box/releases"
        echo ""
        echo "Look for: angry-box-*-windows-amd64.zip or .exe"
        exit 1
        ;;
esac

# ─── Privilege detection and User Mode ─────────────────────────────────────────

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

# ─── Resolve version (especially "latest") ─────────────────────────────────────

resolve_version() {
    if [ "$VERSION" != "latest" ]; then
        RESOLVED_VERSION="$VERSION"
        return
    fi

    echo "==> Resolving latest version from GitHub API..."

    API_URL="https://api.github.com/repos/${GITHUB_REPO}/releases/latest"

    # Try jq first (cleanest)
    if command -v jq >/dev/null 2>&1; then
        TAG=$(curl -fsSL "$API_URL" 2>/dev/null | jq -r '.tag_name' 2>/dev/null || true)
    else
        # Fallback: basic parsing
        TAG=$(curl -fsSL "$API_URL" 2>/dev/null | \
              grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | \
              head -1 | cut -d'"' -f4 || true)
    fi

    if [ -z "$TAG" ] || [ "$TAG" = "null" ]; then
        echo "ERROR: Could not determine latest version from GitHub API."
        echo "Please specify an explicit version with --version X.Y.Z"
        exit 1
    fi

    # Strip leading 'v' if present
    RESOLVED_VERSION="${TAG#v}"
    echo "    Latest version is: $RESOLVED_VERSION"
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

    ARCHIVE="angry-box-${VERSION}-${TARGET}.tar.gz"
    URL="${BASE_URL}/v${VERSION}/${ARCHIVE}"

    TMPDIR=$(mktemp -d)

    if command -v curl >/dev/null 2>&1; then
        curl -fsSL -L "$URL" -o "$TMPDIR/$ARCHIVE" || {
            echo "ERROR: Failed to download $URL"
            echo "Check that version v${RESOLVED_VERSION} exists and target ${TARGET} is available."
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

    # The tarball contains a subdirectory (e.g. angry-box-X.Y.Z-linux-amd64/)
    # We need to find the binary inside it.
    BINARY_PATH=$(find "$TMPDIR" -type f -name "angry-box" | head -1)

    if [ -z "$BINARY_PATH" ]; then
        echo "ERROR: Could not find 'angry-box' binary inside the archive."
        echo "Archive contents:"
        find "$TMPDIR" -maxdepth 2 -type f | head -20
        rm -rf "$TMPDIR"
        exit 1
    fi

    cp "$BINARY_PATH" "$INSTALL_PATH"
    chmod +x "$INSTALL_PATH"
    rm -rf "$TMPDIR"
}

# ─── Install: directories ──────────────────────────────────────────────────────

install_dirs() {
    echo "==> Creating directories..."
    mkdir -p "$CONFIG_DIR"
    mkdir -p "$DATA_DIR"
    mkdir -p "$LOG_DIR"

    # Create or upgrade store.json to v0.5.0 format
    if [ ! -f "$CONFIG_DIR/store.json" ]; then
        cat > "$CONFIG_DIR/store.json" << 'STORE_EOF'
{
  "hosts": [],
  "chains": [],
  "users": [],
  "settings": {
    "metrics_interval": 240
  },
  "node_infos": [],
  "metrics": []
}
STORE_EOF
        echo "    Created default store.json (v0.5.0)"
    elif ! grep -q '"users"' "$CONFIG_DIR/store.json" 2>/dev/null; then
        echo "    Upgrading store.json to v0.5.0 format..."
        if command -v python3 >/dev/null 2>&1; then
            python3 -c "
import json
d = json.load(open('$CONFIG_DIR/store.json'))
d.setdefault('users', [])
d.setdefault('settings', {'metrics_interval': 240})
d.setdefault('node_infos', [])
d.setdefault('metrics', [])
json.dump(d, open('$CONFIG_DIR/store.json','w'), indent=2)
"
        elif command -v jq >/dev/null 2>&1; then
            jq '. + {users: [], settings: {metrics_interval: 240}, node_infos: [], metrics: []}' \
                "$CONFIG_DIR/store.json" > "${CONFIG_DIR}/store.json.tmp" && \
                mv "${CONFIG_DIR}/store.json.tmp" "$CONFIG_DIR/store.json"
        fi
        echo "    Store upgraded"
    fi

    # Create a minimal modern config.toml if it doesn't exist
    if [ ! -f "$CONFIG_DIR/config.toml" ]; then
        cat > "$CONFIG_DIR/config.toml" << 'CONFIG_EOF'
# Angry-BOX configuration
# See documentation for all available options.

listen_addr = "0.0.0.0:8090"
log_level = "info"

# storage_file = "store.json"   # default
CONFIG_EOF
        echo "    Created default config.toml"
    fi
}

# ─── Install: service ──────────────────────────────────────────────────────────

install_service() {
    if [ "$IS_KEENETIC" = true ]; then
        echo "==> Installing Keenetic init script..."
        cat > /opt/etc/init.d/S99angry-box << 'INIT_EOF'
#!/bin/sh
# Angry-BOX init script for Keenetic / Entware

BIN="/opt/bin/angry-box"
STORE="/opt/etc/angry-box/store.json"
PID="/var/run/angry-box.pid"

start() {
    if [ -f "$PID" ]; then
        pid=$(cat "$PID" 2>/dev/null)
        if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
            echo "angry-box already running"
            return 0
        fi
        rm -f "$PID"
    fi
    mkdir -p "$(dirname "$STORE")" 2>/dev/null || true
    "$BIN" serve -listen 0.0.0.0:8090 -file "$STORE" >/dev/null 2>&1 &
    echo $! > "$PID"
    echo "angry-box started"
}

stop() {
    if [ -f "$PID" ]; then
        kill "$(cat "$PID" 2>/dev/null)" 2>/dev/null || true
        rm -f "$PID"
    fi
    echo "angry-box stopped"
}

case "$1" in
    start)   start ;;
    stop)    stop ;;
    restart) stop; sleep 1; start ;;
    *)       echo "Usage: $0 {start|stop|restart}"; exit 1 ;;
esac
exit 0
INIT_EOF
        chmod +x /opt/etc/init.d/S99angry-box
    else
        if [ "$USER_MODE" = true ]; then
            echo "==> Installing user systemd service..."
            mkdir -p "$HOME/.config/systemd/user"
            cat > "$HOME/.config/systemd/user/angry-box.service" << UNIT_EOF
[Unit]
Description=Angry-BOX proxy orchestrator
After=network.target

[Service]
Type=simple
ExecStart=$INSTALL_PATH serve -listen 0.0.0.0:8090 -file $CONFIG_DIR/store.json
Restart=on-failure
RestartSec=5
LimitNOFILE=1048576
WorkingDirectory=$CONFIG_DIR

[Install]
WantedBy=default.target
UNIT_EOF
            systemctl --user daemon-reload
            systemctl --user enable angry-box
        else
            echo "==> Installing systemd service..."
            cat > /etc/systemd/system/angry-box.service << UNIT_EOF
[Unit]
Description=Angry-BOX proxy orchestrator
After=network.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/angry-box serve -listen 0.0.0.0:8090 -file /etc/angry-box/store.json
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
    fi
}

# ─── Start ─────────────────────────────────────────────────────────────────────

start_service() {
    if [ "$IS_KEENETIC" = true ]; then
        echo "==> Starting Angry-BOX..."
        /opt/etc/init.d/S99angry-box start
    else
        echo "==> Starting Angry-BOX..."
        if [ "$USER_MODE" = true ]; then
            systemctl --user start angry-box
            sleep 1
            if ! systemctl --user is-active --quiet angry-box; then
                echo "WARNING: Service failed to start."
                systemctl --user status angry-box --no-pager -n 20 || true
            fi
        else
            systemctl start angry-box
            sleep 1
            if ! systemctl is-active --quiet angry-box; then
                echo "WARNING: Service failed to start."
                journalctl -u angry-box -n 25 --no-pager || true
            fi
        fi
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
        echo "  Config:     /opt/etc/angry-box/config.toml"
        echo "  Data:       /opt/var/lib/angry-box/"
        echo "  Logs:       /opt/var/log/angry-box.log"
        echo ""
        echo "  Control:"
        echo "    /opt/etc/init.d/S99angry-box {start|stop|restart}"
        echo ""
    else
        if [ "$USER_MODE" = true ]; then
            echo "  Binary:     $INSTALL_PATH"
            echo "  Config:     $CONFIG_DIR/config.toml"
            echo "  Data:       $DATA_DIR/"
            echo ""
            echo "  Control (user mode):"
            echo "    systemctl --user status angry-box"
            echo "    systemctl --user {start|stop|restart} angry-box"
            echo ""
        else
            echo "  Binary:     /usr/local/bin/angry-box"
            echo "  Config:     /etc/angry-box/config.toml"
            echo "  Data:       /var/lib/angry-box/"
            echo ""
            echo "  Control:"
            echo "    systemctl status angry-box"
            echo "    systemctl {start|stop|restart} angry-box"
            echo ""
        fi
    fi

    echo "  Quick start:"
    echo "    angry-box host add mynode --addr <IP> --user root --key ~/.ssh/id_ed25519"
    echo "    angry-box deploy -addr <IP> -key ~/.ssh/id_ed25519"
    echo ""
    echo "  API:  http://localhost:8090/health"
    echo ""
    echo "  For routers (Keenetic / OpenWRT), prefer direct .ipk installation from Releases:"
    echo "    opkg install angry-box_${VERSION}_mipsel_24kc.ipk         # Keenetic MIPS"
    echo "    opkg install angry-box_${VERSION}_aarch64_cortex-a53.ipk  # Keenetic/OpenWRT aarch64"
    echo ""
    echo "  License: PolyForm Noncommercial License 1.0.0"
    echo "  Permitted: personal, non-commercial, educational, scientific use only."
    echo "  No warranty. Use at your own risk."
    echo ""
}

# ─── Main ──────────────────────────────────────────────────────────────────────

detect_platform

# ─── Privilege & User Mode decision (must be early) ────────────────────────────
if [ "$IS_KEENETIC" = false ]; then
    if [ "$(id -u)" -ne 0 ]; then
        echo ""
        echo "You are not running as root."
        echo "System-wide installation requires sudo."
        echo ""
        echo "Options:"
        echo "  1. Re-run with: sudo $0 $*"
        echo "  2. Install only for current user (no sudo needed)"
        echo ""
        printf "Install for current user only? [Y/n] "
        read -r answer < /dev/tty 2>/dev/null || {
            echo ""
            echo "ERROR: Cannot read from terminal (piped install?)."
            echo "Please download and run the script directly:"
            echo "  wget https://raw.githubusercontent.com/alexeylcp/angry-box/main/scripts/install.sh"
            echo "  sh install.sh"
            exit 1
        }
        case "$answer" in
            [nN]*)
                echo "Exiting. Run with sudo for system-wide install."
                exit 0
                ;;
            *)
                USER_MODE=true
                echo "==> Using user-local installation mode"
                ;;
        esac
    fi
fi

# Resolve "latest" early so the rest of the script uses a concrete version
resolve_version
VERSION="$RESOLVED_VERSION"

# ─── Determine installation paths (respect USER_MODE) ──────────────────────────

if [ "$IS_KEENETIC" = true ]; then
    INSTALL_PATH="/opt/bin/angry-box"
    CONFIG_DIR="/opt/etc/angry-box"
    DATA_DIR="/opt/var/lib/angry-box"
    LOG_DIR="/opt/var/log"
elif [ "$USER_MODE" = true ]; then
    INSTALL_PATH="$HOME/.local/bin/angry-box"
    CONFIG_DIR="$HOME/.config/angry-box"
    DATA_DIR="$HOME/.local/share/angry-box"
    LOG_DIR="$HOME/.local/log"
    mkdir -p "$HOME/.local/bin" "$HOME/.config" "$HOME/.local/share" "$HOME/.local/log" 2>/dev/null || true
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

# ─── Public IP warning (skip for routers) ────────────────────────────────────
# Check if any local network interface has a public IP directly assigned.
# We examine the machine's own interfaces, not an external service —
# an external service only sees the NAT gateway, not whether this
# specific machine is directly reachable from the internet.
if [ "$IS_KEENETIC" != true ]; then
    HAS_PUBLIC_IFACE=false
    PUBLIC_ADDRS=""
    if command -v ip >/dev/null 2>&1; then
        PUBLIC_ADDRS=$(ip -4 addr show scope global 2>/dev/null | grep -oP 'inet \K[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' || true)
    elif command -v ifconfig >/dev/null 2>&1; then
        PUBLIC_ADDRS=$(ifconfig 2>/dev/null | grep -oP 'inet \K[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | grep -v '127\.' || true)
    fi
    if [ -n "$PUBLIC_ADDRS" ]; then
        for addr in $PUBLIC_ADDRS; do
            case "$addr" in
                127.*|10.*|192.168.*|172.1[6-9].*|172.2[0-9].*|172.3[0-1].*|0.*|169.254.*|100.6[4-9].*|100.[7-9][0-9].*|100.1[0-1][0-9].*|100.12[0-7].*)
                    # Private / loopback / link-local / CGNAT — skip
                    ;;
                *)
                    HAS_PUBLIC_IFACE=true
                    break
                    ;;
            esac
        done
    fi
    if [ "$HAS_PUBLIC_IFACE" = true ]; then
        echo ""
        echo "===================================================================="
        echo "  INFO: This machine has a public IP address."
        echo "  The Web UI will be reachable from the internet."
        echo ""
        echo "  The Web UI is now password-protected by default."
        echo "  On first run, a random password is generated for 'admin'."
        echo "  You can find this password in the system logs:"
        echo ""
        if [ "$USER_MODE" = true ]; then
            echo "    systemctl --user status angry-box -n 50"
        else
            echo "    systemctl status angry-box -n 50"
            echo "    journalctl -u angry-box -n 50 --no-pager"
        fi
        echo "===================================================================="
        echo ""
    fi
fi

# Stop existing service before replacing binary
echo "==> Stopping existing service (if any)..."
if [ "$IS_KEENETIC" = true ]; then
    /opt/etc/init.d/S99angry-box stop 2>/dev/null || true
else
    if [ "$USER_MODE" = true ]; then
        systemctl --user stop angry-box 2>/dev/null || true
    else
        systemctl stop angry-box 2>/dev/null || true
    fi
fi

# Wait for the process to fully exit (up to 5 seconds)
# This prevents "Text file busy" when overwriting the running binary
for i in 1 2 3 4 5; do
    if ! pgrep -x angry-box >/dev/null 2>&1; then
        break
    fi
    sleep 1
done 2>/dev/null || true

install_binary
install_dirs
install_service

if [ "$NO_START" != true ]; then
    start_service
else
    echo "==> Skipping service start (--no-start)"
fi

print_done
