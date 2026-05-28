#!/bin/sh
#
# Wrapper for Keenetic (mipsel) .ipk packaging.
# Delegates to the generic build-opkg.sh script.
#
# Usage: same as before for backward compatibility.

exec "$(dirname "$0")/build-opkg.sh" "$@" mipsel_24kc