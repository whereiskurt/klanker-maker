#!/usr/bin/env bash
#
# migrate-profile-to-v1alpha2.sh — convenience wrapper around the
# km-profile-migrate Go tool. Mechanically upgrades a v1alpha1 SandboxProfile
# to v1alpha2 (Phase 92 hard breaking change).
#
# Usage:
#   scripts/migrate-profile-to-v1alpha2.sh <in.yaml>            # prints to stdout
#   scripts/migrate-profile-to-v1alpha2.sh <in.yaml> <out.yaml> # writes out.yaml
#   scripts/migrate-profile-to-v1alpha2.sh <in.yaml> -i         # in-place (overwrite in.yaml)
#
# The tool is idempotent — re-running on a v1alpha2 file is a no-op pass-through.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <in.yaml> [<out.yaml>|-i]" >&2
  exit 2
fi

IN="$1"
OUT="${2:--}"

# NOTE: Go's flag package stops parsing flags at the first positional arg, so
# the -o flag must precede the input path.
if [[ "$OUT" == "-i" ]]; then
  TMP="$(mktemp)"
  ( cd "$REPO_ROOT" && go run ./cmd/km-profile-migrate "$IN" ) > "$TMP"
  mv "$TMP" "$IN"
  echo "migrated in place: $IN" >&2
else
  ( cd "$REPO_ROOT" && go run ./cmd/km-profile-migrate -o "$OUT" "$IN" )
fi
