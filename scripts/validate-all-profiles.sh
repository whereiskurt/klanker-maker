#!/usr/bin/env bash
# scripts/validate-all-profiles.sh — Phase 92 hard gate (updated Phase 117 Plan 03).
#
# Validates every concrete leaf profile in the 21-file Profile Inventory using
# `km validate`. Exits non-zero on any failure. Single source of truth for the
# inventory.
#
# profiles/base/ is intentionally EXCLUDED: abstract base fragments live there
# and deliberately omit required fields that concrete leaves supply via extends.
# km validate already prints a "SKIP" message for metadata.abstract:true files;
# this script skips them entirely to keep output clean and avoid false failures.
# (The base/ directory is created by Phase 117 Plan 04 and may not exist yet;
# the guard below is a no-op when the directory is absent.)
#
# Usage: bash scripts/validate-all-profiles.sh
# Requires: km binary built (./km) — call `make build` first if needed.

set -euo pipefail

KM_BIN="${KM_BIN:-./km}"
if [[ ! -x "$KM_BIN" ]]; then
  echo "ERROR: km binary not found at $KM_BIN. Run 'make build' first." >&2
  exit 2
fi

# Print skip lines for any profiles/base/*.yaml fragments.
# These are abstract base definitions (metadata.abstract: true) that must NOT
# be validated standalone — they are only valid when merged into a leaf profile.
# Guard: the profiles/base/ directory may not exist yet (created by Phase 117 Plan 04).
if [ -d profiles/base ]; then
  for frag in profiles/base/*.yaml; do
    # nullglob-style guard: skip if the glob matched nothing
    [ -e "$frag" ] || continue
    printf '  skip  %s (base fragment — validated only when merged into a leaf)\n' "$frag"
  done
fi

PROFILES=(
  profiles/ao.yaml
  profiles/codex.yaml
  profiles/dc34.yaml
  profiles/desktop.yaml
  profiles/dc34.ami.yaml
  profiles/example-additional-snapshots.yaml
  profiles/github-review.yaml
  profiles/goose.yaml
  profiles/learn.v2.yaml
  profiles/learn.v2.chatty.yaml
  profiles/learn.v2.codex.yaml
  profiles/learn.v2.polite.yaml
  profiles/locked.yaml
  profiles/locked.ami.yaml
  pkg/profile/builtins/ao.yaml
  pkg/profile/builtins/codex.yaml
  pkg/profile/builtins/goose.yaml
  pkg/profile/builtins/hardened.yaml
  pkg/profile/builtins/learn.yaml
  pkg/profile/builtins/open-dev.yaml
  pkg/profile/builtins/restricted-dev.yaml
  pkg/profile/builtins/sealed.yaml
)

fail=0
for p in "${PROFILES[@]}"; do
  if "$KM_BIN" validate "$p" >/tmp/km-validate-$$.out 2>&1; then
    printf '  ok    %s\n' "$p"
  else
    printf '  FAIL  %s\n' "$p" >&2
    cat /tmp/km-validate-$$.out >&2
    fail=1
  fi
done
rm -f /tmp/km-validate-$$.out

if [[ $fail -ne 0 ]]; then
  echo "" >&2
  echo "validate-all-profiles: at least one profile failed km validate" >&2
  exit 1
fi
echo "validate-all-profiles: all ${#PROFILES[@]} profiles valid"
