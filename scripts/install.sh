#!/usr/bin/env bash
# install.sh — LucX Universal Installer
#
# One-liner: curl -sSL https://raw.githubusercontent.com/AlexeyLCP/lucx/main/scripts/install.sh | bash
#
# Auto-detects: architecture, Keenetic NDMS vs standard Linux
# Supports: --port, --version, --update, --uninstall, --help

set -euo pipefail

# ══════════════════════════════════════════════════════════════
# Constants
# ══════════════════════════════════════════════════════════════

readonly REPO="AlexeyLCP/lucx"
readonly APP="lucx-core"
readonly INSTALL_BASE="/opt/lucx"
readonly KEENETIC_BIN="/opt/bin"
readonly KEENETIC_INIT="/opt/etc/init.d"
readonly KEENETIC_RUN="/opt/var/run"
readonly KEENETIC_ETC="/opt/etc"

# Defaults — overridden by flags
PORT="${LU_PORT:-8744}"
VERSION_SPEC=""
DO_UPDATE=false
DO_UNINSTALL=false

# Colors
if [[ -t 1 ]]; then
    readonly C_RESET='\033[0m'
    readonly C_BOLD='\033[1m'
    readonly C_DIM='\033[2m'
    readonly C_RED='\033[31m'
    readonly C_GREEN='\033[32m'
    readonly C_YELLOW='\033[33m'
    readonly C_BLUE='\033[34m'
    readonly C_CYAN='\033[36m'
    readonly C_WHITE='\033[37m'
else
    readonly C_RESET='' C_BOLD='' C_DIM='' C_RED='' C_GREEN='' C_YELLOW='' C_BLUE='' C_CYAN='' C_WHITE=''
fi

# Output helpers
info()    { echo -e "${C_BLUE}ℹ${C_RESET} $*"; }
success() { echo -e "${C_GREEN}✓${C_RESET} $*"; }
warn()    { echo -e "${C_YELLOW}⚠${C_RESET} $*"; }
error()   { echo -e "${C_RED}✗${C_RESET} $*" >&2; }
step()    { echo -e "${C_CYAN}→${C_RESET} ${C_BOLD}$*${C_RESET}"; }
banner()  { echo -e "${C_BOLD}${C_CYAN}$*${C_RESET}"; }

# ══════════════════════════════════════════════════════════════
# Platform detection
# ══════════════════════════════════════════════════════════════

is_keenetic() {
    # NDMS-specific paths and commands
    [[ -d /opt/ndms ]]       && return 0
    [[ -d /opt/etc/init.d ]] && [[ -f /opt/bin/opkg || -f /opt/bin/ndm ]] && return 0
    command -v ndm &>/dev/null && ndm version &>/dev/null 2>&1 && return 0

    # Kernel / device-tree check
    if [[ -f /proc/device-tree/model ]]; then
        grep -qi 'keenetic' /proc/device-tree/model 2>/dev/null && return 0
    fi

    # NDMS leaves its signature in several files
    [[ -f /opt/etc/ndms/version ]] && return 0

    return 1
}

detect_arch() {
    local arch
    arch="$(uname -m)"

    case "$arch" in
        x86_64|amd64)
            echo "amd64"
            ;;
        aarch64|arm64)
            echo "arm64"
            ;;
        armv7l|armv7|armv6l)
            echo "armv7"
            ;;
        mips)
            # Detect endianness
            local endian
            endian="$(detect_mips_endian)"
            if [[ "$endian" == "little" ]]; then
                echo "mipsel"
            else
                echo "mips"
            fi
            ;;
        mips64)
            local endian
            endian="$(detect_mips_endian)"
            if [[ "$endian" == "little" ]]; then
                echo "mipsel"
            else
                echo "mips"
            fi
            ;;
        *)
            echo "unknown:$arch"
            ;;
    esac
}

detect_mips_endian() {
    # Method 1: readelf on /bin/sh
    if command -v readelf &>/dev/null; then
        local data
        data="$(readelf -h /bin/sh 2>/dev/null | grep "Data:" | head -1)"
        if echo "$data" | grep -q "little"; then
            echo "little"; return
        elif echo "$data" | grep -q "big"; then
            echo "big"; return
        fi
    fi

    # Method 2: hexdump test
    # MIPSel stores 0x00000001 as 01 00 00 00; MIPS BE as 00 00 00 01
    echo -n I | od -o | head -n1 | awk '{print $2}' | grep -q '^0*1$' && { echo "little"; return; }

    # Fallback: assume little for modern Keenetic
    echo "little"
}

map_arch_to_target() {
    local arch="$1"
    local keenetic="$2"

    case "$arch" in
        amd64)
            echo "linux-amd64"
            ;;
        arm64)
            echo "linux-arm64"
            ;;
        armv7)
            echo "linux-armv7"
            ;;
        mipsel)
            if [[ "$keenetic" == "true" ]]; then
                echo "keenetic-mipsel"
            else
                echo "keenetic-mipsel"  # default to keenetic target (works on OpenWrt too)
            fi
            ;;
        mips)
            echo "openwrt-mips"
            ;;
        *)
            echo ""
            ;;
    esac
}

# ══════════════════════════════════════════════════════════════
# Version resolution
# ══════════════════════════════════════════════════════════════

resolve_version() {
    if [[ -n "$VERSION_SPEC" ]]; then
        # Strip leading 'v' if present, then re-add for GitHub tag
        local v="${VERSION_SPEC#v}"
        echo "v${v}"
        return
    fi

    step "Resolving latest version from GitHub..."

    local latest
    if command -v curl &>/dev/null; then
        latest="$(curl -sSf --max-time 10 \
            "https://api.github.com/repos/${REPO}/releases/latest" \
            2>/dev/null | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
    elif command -v wget &>/dev/null; then
        latest="$(wget -qO- --timeout=10 \
            "https://api.github.com/repos/${REPO}/releases/latest" \
            2>/dev/null | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
    fi

    if [[ -z "$latest" ]]; then
        error "Could not resolve latest version from GitHub."
        error "Try specifying manually: curl ... | bash -s -- --version v0.1.0"
        exit 1
    fi

    echo "$latest"
}

# ══════════════════════════════════════════════════════════════
# Download & Verify
# ══════════════════════════════════════════════════════════════

download_and_verify() {
    local version="$1"
    local target="$2"
    local version_no_v="${version#v}"
    local tarball="${APP}-${version_no_v}-${target}.tar.gz"
    local url="https://github.com/${REPO}/releases/download/${version}/${tarball}"
    local tmpdir
    tmpdir="$(mktemp -d -t lucx-install-XXXXXX)"
    trap "rm -rf '$tmpdir'" EXIT

    step "Downloading ${tarball}..."
    info "URL: ${url}"

    if command -v curl &>/dev/null; then
        curl -sSfL --max-time 120 -o "$tmpdir/$tarball" "$url" || {
            error "Download failed. Check that release ${version} exists."
            exit 1
        }
    elif command -v wget &>/dev/null; then
        wget -q --timeout=120 -O "$tmpdir/$tarball" "$url" || {
            error "Download failed. Check that release ${version} exists."
            exit 1
        }
    else
        error "Neither curl nor wget found. Install one of them first."
        exit 1
    fi

    # SHA256 verification — attempt to download checksums
    local checksums_url="https://github.com/${REPO}/releases/download/${version}/${APP}-${version_no_v}-checksums.txt"
    local checksums_file="$tmpdir/checksums.txt"

    if curl -sSf --max-time 10 -o "$checksums_file" "$checksums_url" 2>/dev/null || \
       wget -q --timeout=10 -O "$checksums_file" "$checksums_url" 2>/dev/null; then
        step "Verifying SHA256..."
        if command -v sha256sum &>/dev/null; then
            (cd "$tmpdir" && sha256sum -c --ignore-missing "$checksums_file" 2>/dev/null) || {
                warn "SHA256 verification failed — continuing anyway (checksum file may be stale)"
            }
        else
            warn "sha256sum not available — skipping verification"
        fi
    else
        info "No checksums file found — skipping verification"
    fi

    # Extract
    step "Extracting..."
    tar xzf "$tmpdir/$tarball" -C "$tmpdir"
    echo "$tmpdir"
}

# ══════════════════════════════════════════════════════════════
# Standard Linux install
# ══════════════════════════════════════════════════════════════

install_standard_linux() {
    local tmpdir="$1"

    step "Installing to ${INSTALL_BASE}..."

    # Stop existing service
    if systemctl is-active --quiet lucx-core 2>/dev/null; then
        info "Stopping existing lucx-core service..."
        systemctl stop lucx-core 2>/dev/null || true
    fi

    # Create directories
    mkdir -p "$INSTALL_BASE" "${INSTALL_BASE}/data"

    # Backup existing binary
    if [[ -f "${INSTALL_BASE}/${APP}" ]]; then
        cp "${INSTALL_BASE}/${APP}" "${INSTALL_BASE}/${APP}.bak" 2>/dev/null || true
        info "Existing binary backed up to ${APP}.bak"
    fi

    # Install binary
    cp "$tmpdir"/*/"${APP}" "${INSTALL_BASE}/${APP}" 2>/dev/null || \
        cp "$tmpdir/${APP}" "${INSTALL_BASE}/${APP}" 2>/dev/null || {
        error "Binary not found in downloaded archive"
        exit 1
    }
    chmod +x "${INSTALL_BASE}/${APP}"

    # Generate JWT secret if not exists
    local jwt_file="${INSTALL_BASE}/data/.jwt-secret"
    if [[ ! -f "$jwt_file" ]]; then
        if command -v openssl &>/dev/null; then
            openssl rand -hex 32 > "$jwt_file"
        else
            head -c 32 /dev/urandom | od -A n -t x1 | tr -d ' \n' > "$jwt_file"
        fi
        chmod 600 "$jwt_file"
    fi
    local jwt_secret
    jwt_secret="$(cat "$jwt_file")"

    # Create systemd service
    step "Creating systemd service..."

    cat > /etc/systemd/system/lucx-core.service << EOF
[Unit]
Description=LucX — Multi-Hop Xray Orchestrator
Documentation=https://github.com/${REPO}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=${INSTALL_BASE}/${APP} -listen 127.0.0.1:${PORT} -db ${INSTALL_BASE}/data/lucx.db -jwt-secret ${jwt_secret}
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=lucx-core

# Security hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=${INSTALL_BASE}/data
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload

    # Start
    step "Starting lucx-core..."
    systemctl enable lucx-core 2>/dev/null || true
    systemctl start lucx-core

    sleep 2
    if systemctl is-active --quiet lucx-core; then
        success "Service is running"
    else
        warn "Service may not have started. Check: journalctl -u lucx-core -n 20"
    fi

    echo ""
    banner "LucX installed successfully!"
    echo ""
    echo -e "  ${C_BOLD}Install path:${C_RESET} ${INSTALL_BASE}/${APP}"
    echo -e "  ${C_BOLD}Data dir:${C_RESET}    ${INSTALL_BASE}/data"
    echo -e "  ${C_BOLD}Service:${C_RESET}     systemctl {start,stop,restart} lucx-core"
    echo -e "  ${C_BOLD}Logs:${C_RESET}       journalctl -u lucx-core -f"
    echo -e "  ${C_BOLD}Web UI:${C_RESET}     http://localhost:${PORT}"
    echo ""
    echo -e "  ${C_BOLD}Reverse proxy:${C_RESET} Put nginx/Caddy with TLS in front of :${PORT}"
    echo ""
}

# ══════════════════════════════════════════════════════════════
# Keenetic NDMS install
# ══════════════════════════════════════════════════════════════

install_keenetic() {
    local tmpdir="$1"

    echo ""
    banner "  Keenetic NDMS Detected"
    echo ""
    info "Installing for Keenetic NDMS (Network Device Management System)"

    # Check prerequisites
    if [[ ! -d "$KEENETIC_BIN" ]]; then
        error "Keenetic paths not found. Is Entware/OPKG installed?"
        error "Install Entware first: https://help.keenetic.com/hc/en-us/articles/360021888880"
        exit 1
    fi

    step "Installing to ${KEENETIC_BIN}/${APP}..."

    # Stop existing instance
    if pgrep -x "$APP" &>/dev/null; then
        info "Stopping existing lucx-core..."
        killall "$APP" 2>/dev/null || true
        sleep 1
    fi
    if [[ -x "${KEENETIC_INIT}/S99lucx" ]]; then
        "${KEENETIC_INIT}/S99lucx" stop 2>/dev/null || true
    fi

    # Ensure directories
    mkdir -p "$KEENETIC_BIN" "$KEENETIC_INIT" "$KEENETIC_RUN"

    # Backup existing
    if [[ -f "${KEENETIC_BIN}/${APP}" ]]; then
        cp "${KEENETIC_BIN}/${APP}" "${KEENETIC_BIN}/${APP}.bak" 2>/dev/null || true
        info "Existing binary backed up to ${APP}.bak"
    fi

    # Install binary
    cp "$tmpdir"/*/"${APP}" "${KEENETIC_BIN}/${APP}" 2>/dev/null || \
        cp "$tmpdir/${APP}" "${KEENETIC_BIN}/${APP}" 2>/dev/null || {
        error "Binary not found in downloaded archive"
        exit 1
    }
    chmod +x "${KEENETIC_BIN}/${APP}"

    # JWT secret
    local jwt_file="${KEENETIC_RUN}/lucx-jwt.secret"
    if [[ ! -f "$jwt_file" ]]; then
        if command -v openssl &>/dev/null; then
            openssl rand -hex 32 > "$jwt_file" 2>/dev/null || \
                head -c 32 /dev/urandom | od -A n -t x1 | tr -d ' \n' > "$jwt_file"
        else
            head -c 32 /dev/urandom | od -A n -t x1 | tr -d ' \n' > "$jwt_file"
        fi
        chmod 600 "$jwt_file"
    fi
    local jwt_secret
    jwt_secret="$(cat "$jwt_file")"

    # NDMS init script
    step "Creating NDMS init script (S99lucx)..."

    cat > "${KEENETIC_INIT}/S99lucx" << NDMSINIT
#!/bin/sh
# LucX daemon for Keenetic NDMS
# Auto-starts on boot (S99* in /opt/etc/init.d/)
# Manual: /opt/etc/init.d/S99lucx {start|stop|restart|status}

PATH="/opt/bin:/opt/sbin:/bin:/sbin:/usr/bin:/usr/sbin"
APP="lucx-core"
PORT="${PORT}"
DB="${KEENETIC_RUN}/lucx.db"
JWT_SECRET="${jwt_secret}"
PID_FILE="${KEENETIC_RUN}/lucx.pid"
LOG_FILE="${KEENETIC_RUN}/lucx.log"

start() {
    if [ -f "\$PID_FILE" ] && kill -0 "\$(cat "\$PID_FILE")" 2>/dev/null; then
        echo "LucX is already running (PID \$(cat "\$PID_FILE"))"
        return 0
    fi

    echo "Starting LucX..."
    /opt/bin/\$APP -listen :\${PORT} -db \${DB} -jwt-secret \${JWT_SECRET} \
        >> \${LOG_FILE} 2>&1 &
    echo \$! > "\$PID_FILE"

    sleep 2
    if kill -0 "\$(cat "\$PID_FILE")" 2>/dev/null; then
        echo "LucX started (PID \$(cat "\$PID_FILE"))"
        echo "Web UI: http://\$(ip route get 1.1.1.1 2>/dev/null | grep -oP 'src \K\S+' || echo '<keenetic-ip>'):\${PORT}"
    else
        echo "LucX failed to start. Check \${LOG_FILE}"
        return 1
    fi
}

stop() {
    if [ -f "\$PID_FILE" ]; then
        local pid="\$(cat "\$PID_FILE")"
        if kill -0 "\$pid" 2>/dev/null; then
            echo "Stopping LucX (PID \$pid)..."
            kill "\$pid" 2>/dev/null
            sleep 1
            kill -0 "\$pid" 2>/dev/null && kill -9 "\$pid" 2>/dev/null || true
            rm -f "\$PID_FILE"
            echo "LucX stopped"
        else
            rm -f "\$PID_FILE"
        fi
    fi
    # Fallback: kill any remaining
    killall \$APP 2>/dev/null || true
}

status() {
    if [ -f "\$PID_FILE" ] && kill -0 "\$(cat "\$PID_FILE")" 2>/dev/null; then
        echo "LucX is running (PID \$(cat "\$PID_FILE"))"
        return 0
    else
        echo "LucX is not running"
        return 1
    fi
}

case "\$1" in
    start)   start ;;
    stop)    stop ;;
    restart) stop; sleep 1; start ;;
    status)  status ;;
    *)       echo "Usage: \$0 {start|stop|restart|status}"; exit 1 ;;
esac
NDMSINIT

    chmod +x "${KEENETIC_INIT}/S99lucx"

    # NDMS package registration (optional — for Web UI awareness)
    if [[ -d "${KEENETIC_ETC}/ndms/pkg.d" ]]; then
        cat > "${KEENETIC_ETC}/ndms/pkg.d/lucx.json" << 'NDMSPKG'
{
    "name": "lucx-core",
    "display": "LucX Proxy Orchestrator",
    "description": "Personal multi-hop Xray proxy orchestrator",
    "version": "__VERSION__"
}
NDMSPKG
        info "Registered in NDMS package list"
    fi

    # Start service
    step "Starting LucX..."
    "${KEENETIC_INIT}/S99lucx" start || true

    echo ""
    banner "LucX installed on Keenetic!"
    echo ""
    echo -e "  ${C_BOLD}Binary:${C_RESET}      ${KEENETIC_BIN}/${APP}"
    echo -e "  ${C_BOLD}Init script:${C_RESET}  ${KEENETIC_INIT}/S99lucx"
    echo -e "  ${C_BOLD}Database:${C_RESET}     ${KEENETIC_RUN}/lucx.db"
    echo -e "  ${C_BOLD}Logs:${C_RESET}        ${KEENETIC_RUN}/lucx.log"
    echo -e "  ${C_BOLD}PID file:${C_RESET}     ${KEENETIC_RUN}/lucx.pid"
    echo -e "  ${C_BOLD}Web UI:${C_RESET}      http://<keenetic-ip>:${PORT}"
    echo ""
    echo -e "  ${C_BOLD}Control:${C_RESET}"
    echo -e "    ${KEENETIC_INIT}/S99lucx {start|stop|restart|status}"
    echo ""
    echo -e "  ${C_BOLD}Start on boot:${C_RESET} automatic (S99* in init.d)"
    echo ""
    warn "Change the default password! Add to start command:"
    echo -e "    ${C_DIM}-jwt-secret \"\$(openssl rand -hex 32)\"${C_RESET}"
    echo ""
}

# ══════════════════════════════════════════════════════════════
# Update
# ══════════════════════════════════════════════════════════════

do_update() {
    local keenetic="$1"

    step "Updating LucX..."

    if [[ "$keenetic" == "true" ]]; then
        if [[ ! -f "${KEENETIC_BIN}/${APP}" ]]; then
            error "LucX is not installed. Run without --update to install."
            exit 1
        fi
        info "Existing install found at ${KEENETIC_BIN}/${APP}"
        info "Stopping service..."
        "${KEENETIC_INIT}/S99lucx" stop 2>/dev/null || killall "$APP" 2>/dev/null || true
    else
        if [[ ! -f "${INSTALL_BASE}/${APP}" ]]; then
            error "LucX is not installed. Run without --update to install."
            exit 1
        fi
        info "Existing install found at ${INSTALL_BASE}/${APP}"
        info "Stopping service..."
        systemctl stop lucx-core 2>/dev/null || true
    fi

    # Normal install flow continues (downloads and overwrites)
}

# ══════════════════════════════════════════════════════════════
# Uninstall
# ══════════════════════════════════════════════════════════════

do_uninstall() {
    echo ""
    banner "LucX Uninstaller"
    echo ""

    local keenetic
    if is_keenetic; then
        keenetic=true
        info "Keenetic NDMS detected"
    else
        keenetic=false
        info "Standard Linux detected"
    fi

    echo ""
    warn "This will remove LucX and all its data."
    echo -n "Continue? [y/N] "
    read -r confirm
    if [[ ! "$confirm" =~ ^[Yy]$ ]]; then
        info "Uninstall cancelled."
        exit 0
    fi

    if [[ "$keenetic" == "true" ]]; then
        step "Stopping service..."
        "${KEENETIC_INIT}/S99lucx" stop 2>/dev/null || killall "$APP" 2>/dev/null || true

        step "Removing files..."
        rm -f "${KEENETIC_BIN}/${APP}"
        rm -f "${KEENETIC_BIN}/${APP}.bak"
        rm -f "${KEENETIC_INIT}/S99lucx"
        rm -f "${KEENETIC_RUN}/lucx.db"
        rm -f "${KEENETIC_RUN}/lucx.pid"
        rm -f "${KEENETIC_RUN}/lucx.log"
        rm -f "${KEENETIC_RUN}/lucx-jwt.secret"
        rm -f "${KEENETIC_ETC}/ndms/pkg.d/lucx.json"
    else
        step "Stopping service..."
        systemctl stop lucx-core 2>/dev/null || true
        systemctl disable lucx-core 2>/dev/null || true

        step "Removing files..."
        rm -f /etc/systemd/system/lucx-core.service
        systemctl daemon-reload 2>/dev/null || true
        rm -rf "$INSTALL_BASE"
    fi

    success "LucX has been uninstalled."
    exit 0
}

# ══════════════════════════════════════════════════════════════
# Usage
# ══════════════════════════════════════════════════════════════

usage() {
    cat << EOF

${C_BOLD}LucX Universal Installer${C_RESET}

${C_DIM}One-liner:${C_RESET}
  curl -sSL https://raw.githubusercontent.com/${REPO}/main/scripts/install.sh | bash

${C_BOLD}Flags:${C_RESET}
  ${C_CYAN}--port PORT${C_RESET}       Listen port (default: ${PORT})
  ${C_CYAN}--version VERSION${C_RESET}  Install specific version (e.g. --version v0.1.0)
  ${C_CYAN}--update${C_RESET}           Update existing installation
  ${C_CYAN}--uninstall${C_RESET}        Remove LucX and all data
  ${C_CYAN}--help${C_RESET}             Show this message

${C_BOLD}Examples:${C_RESET}
  curl -sSL .../install.sh | bash                    ${C_DIM}# Install latest${C_RESET}
  curl -sSL .../install.sh | bash -s -- --port 8080  ${C_DIM}# Custom port${C_RESET}
  curl -sSL .../install.sh | bash -s -- --update     ${C_DIM}# Update to latest${C_RESET}
  curl -sSL .../install.sh | bash -s -- --uninstall  ${C_DIM}# Remove${C_RESET}

${C_BOLD}Environment:${C_RESET}
  ${C_CYAN}LU_PORT${C_RESET}           Default port (overridden by --port)

EOF
    exit 0
}

# ══════════════════════════════════════════════════════════════
# Main
# ══════════════════════════════════════════════════════════════

main() {
    # Parse flags
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --port)
                PORT="$2"; shift 2 ;;
            --version)
                VERSION_SPEC="$2"; shift 2 ;;
            --update)
                DO_UPDATE=true; shift ;;
            --uninstall)
                DO_UNINSTALL=true; shift ;;
            --help|-h)
                usage ;;
            --)
                shift; break ;;
            -*)
                error "Unknown flag: $1"
                echo "Use --help for usage"
                exit 1 ;;
            *)
                warn "Ignoring unknown argument: $1"
                shift ;;
        esac
    done

    # Banner
    echo ""
    echo -e "${C_BOLD}${C_CYAN}  ╔══════════════════════════════╗${C_RESET}"
    echo -e "${C_BOLD}${C_CYAN}  ║   LucX Universal Installer   ║${C_RESET}"
    echo -e "${C_BOLD}${C_CYAN}  ║   Multi-Hop Xray Orchestrator║${C_RESET}"
    echo -e "${C_BOLD}${C_CYAN}  ╚══════════════════════════════╝${C_RESET}"
    echo ""

    # Uninstall
    if [[ "$DO_UNINSTALL" == "true" ]]; then
        do_uninstall
    fi

    # Platform detection
    step "Detecting platform..."
    local keenetic=false
    local arch

    if is_keenetic; then
        keenetic=true
        success "Keenetic NDMS router detected"
    else
        success "Standard Linux detected"
    fi

    # Architecture detection
    arch="$(detect_arch)"
    if [[ "$arch" == unknown:* ]]; then
        error "Unsupported architecture: ${arch#unknown:}"
        error "LucX supports: amd64, arm64, armv7, mips, mipsel"
        exit 1
    fi
    info "Architecture: ${arch}"

    local target
    target="$(map_arch_to_target "$arch" "$keenetic")"
    if [[ -z "$target" ]]; then
        error "Cannot map architecture '$arch' to a release target"
        exit 1
    fi
    info "Release target: ${target}"

    # Check for root
    if [[ "$(id -u)" -ne 0 ]]; then
        error "This script must be run as root (use sudo or run on router as root)"
        if [[ "$keenetic" == "true" ]]; then
            error "Keenetic: SSH as root or use 'sudo' if available"
        fi
        exit 1
    fi

    # Update mode
    if [[ "$DO_UPDATE" == "true" ]]; then
        do_update "$keenetic"
    fi

    # Resolve version
    local version
    version="$(resolve_version)"
    info "Version: ${version}"

    # Download & extract
    local tmpdir
    tmpdir="$(download_and_verify "$version" "$target")"

    # Install
    echo ""
    if [[ "$keenetic" == "true" ]]; then
        install_keenetic "$tmpdir"
    else
        install_standard_linux "$tmpdir"
    fi

    # Cleanup
    rm -rf "$tmpdir"
    trap - EXIT

    echo ""
    echo -e "${C_GREEN}${C_BOLD}  ✓ Installation complete!${C_RESET}"
    echo ""
}

main "$@"
