#!/bin/sh
# 50-angry-box.sh — NDMS hook forwarder for Angry-BOX (Keenetic).
#
# Installed by the angry-box .ipk into the four NDMS hook directories:
#   /opt/etc/ndm/iflayerchanged.d/
#   /opt/etc/ndm/ifcreated.d/
#   /opt/etc/ndm/ifdestroyed.d/
#   /opt/etc/ndm/ifipchanged.d/
# The event type is derived from the directory name at invocation time.
# Forwards the interface event to the panel's loopback API (the panel binds
# 127.0.0.1 — the hook never leaves the router). BusyBox-portable: firmware
# wget with -T timeout (no long options, no curl, no grep -P).

HOOK_TYPE=$(basename "$(dirname "$0")" .d)

PORT=$(sed -n 's/.*"port"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' \
    /opt/etc/angry-box/settings.json 2>/dev/null | head -n 1)
[ -z "$PORT" ] && PORT="9080"

BODY="type=${HOOK_TYPE}&id=${id}&system_name=${system_name}&layer=${layer}&level=${level}&address=${address}&up=${up}&connected=${connected}"

wget -q -T 3 -O /dev/null --post-data "$BODY" "http://127.0.0.1:${PORT}/api/hooks/ndm" 2>/dev/null || true
exit 0
