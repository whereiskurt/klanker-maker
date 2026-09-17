#!/bin/sh
# Phase 133: exec an ABSOLUTE path, never the bare name — resolving by name here
# would re-find this shim on PATH and recurse.
# NOTE: km-secretsd selftest (shimTarget) parses this KM_REAL= line to verify the target exists. Keep the literal path here, at column 0.
KM_REAL="/usr/bin/claude"
# Prefer the claude the box has NOW. An in-place update (claude self-updating
# into nvm's prefix) lands a newer copy elsewhere on PATH while the baked target
# still exists, so a "baked path missing" fallback never fires. The shim dir is
# stripped from PATH first so this can never resolve to the shim itself.
KM_LIVE="$(PATH="$(echo "$PATH" | tr ':' '\n' | grep -v '^/opt/km/shims$' | paste -sd: -)" command -v 'claude' 2>/dev/null)"
[ -x "$KM_LIVE" ] && KM_REAL="$KM_LIVE"
[ -x "$KM_REAL" ] || { echo "km-shim: cannot locate the real claude" >&2; exit 127; }
exec /opt/km/bin/km-env exec --as 'claude' -- "$KM_REAL" "$@"
