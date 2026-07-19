#!/bin/sh /etc/rc.common
# OpenWrt procd init script for angry-box.

START=99
STOP=10
USE_PROCD=1

PROG=/usr/bin/angry-box
CONF_DIR=/etc/angry-box

start_service() {
    mkdir -p "$CONF_DIR" /var/log
    # At-rest encryption master key — auto-generate on first start (store.json
    # carries SSH private keys + AWG/Reality secrets).
    if [ ! -f "$CONF_DIR/store.json.key" ]; then
        ( umask 077 && head -c 32 /dev/urandom > "$CONF_DIR/store.json.key" )
        chmod 600 "$CONF_DIR/store.json.key"
    fi
    procd_open_instance angry-box
    # Bind loopback only — the control plane carries SSH private keys; reach
    # the panel via SSH tunnel (ssh -L 9080:127.0.0.1:9080 root@router).
    procd_set_param command "$PROG" serve --listen 127.0.0.1:9080 --file "$CONF_DIR/store.json"
    procd_set_param respawn 3600 5 5
    procd_set_param stdout 1
    procd_set_param stderr 1
    procd_close_instance
}
