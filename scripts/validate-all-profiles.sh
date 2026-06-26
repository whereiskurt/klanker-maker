#!/usr/bin/env bash
# scripts/validate-all-profiles.sh — Phase 92 hard gate (updated Phase 120 Plan 04).
#
# Validates every concrete leaf profile in the 13-entry Profile Inventory using
# `km validate`. Exits non-zero on any failure. Single source of truth for the
# inventory (4 composed leaves + 8 pkg/profile/builtins entries).
#
# profiles/base/** is intentionally EXCLUDED (recursive): abstract base fragments
# live there (including the new profiles/base/os/ subdir) and deliberately omit
# required fields that concrete leaves supply via extends. km validate already prints
# a "SKIP" message for metadata.abstract:true files; this script skips them entirely
# to keep output clean and avoid false failures.
# (The base/ directory is created by Phase 117 Plan 04; base/os/ by Phase 120 Plan 01.)
#
# Usage: bash scripts/validate-all-profiles.sh
# Requires: km binary built (./km) — call `make build` first if needed.

set -euo pipefail

KM_BIN="${KM_BIN:-./km}"
if [[ ! -x "$KM_BIN" ]]; then
  echo "ERROR: km binary not found at $KM_BIN. Run 'make build' first." >&2
  exit 2
fi

# Print skip lines for all profiles/base/**/*.yaml fragments (recursive).
# This covers both profiles/base/*.yaml (existing knob fragments) AND the new
# profiles/base/os/*.yaml subdir introduced in Phase 120 Plan 01.
# These are abstract base definitions (metadata.abstract: true) that must NOT
# be validated standalone — they are only valid when merged into a leaf profile.
# Guard: the profiles/base/ directory may not exist yet on fresh checkouts.
if [ -d profiles/base ]; then
  while IFS= read -r frag; do
    printf '  skip  %s (base fragment — validated only when merged into a leaf)\n' "$frag"
  done < <(find profiles/base -name '*.yaml' | sort)
fi

PROFILES=(
  profiles/learner.yaml
  profiles/desktop.yaml
  profiles/github.yaml
  profiles/h1.yaml
  profiles/spot.yaml
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
