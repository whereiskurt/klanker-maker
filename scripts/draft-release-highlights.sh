#!/usr/bin/env bash
#
# draft-release-highlights.sh — auto-draft the "Major additions highlighted"
# section for a release from the per-phase summary blocks added to CLAUDE.md
# since the last release tag.
#
# It is a DRAFT generator, not a publisher: the output is a starting point you
# trim (tighten wording, add emoji, drop minor/fix phases, fold in the
# non-phase changes listed in the trailing HTML comment).
#
# Usage:
#   scripts/draft-release-highlights.sh                # diff since last tag → stdout
#   scripts/draft-release-highlights.sh v0.5.6         # diff since an explicit tag
#   scripts/draft-release-highlights.sh --write        # overwrite docs/RELEASE-HIGHLIGHTS.md
#   scripts/draft-release-highlights.sh v0.5.6 --write
#
# Mechanism it feeds: docs/RELEASE-HIGHLIGHTS.md → $KM_RELEASE_HIGHLIGHTS
# (workflow) → .goreleaser.yaml release.header. See docs/release.md § Release highlights.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

CLAUDE_MD="CLAUDE.md"
OUT_FILE="docs/RELEASE-HIGHLIGHTS.md"
WRITE=0
LAST_TAG=""

for arg in "$@"; do
  case "$arg" in
    --write) WRITE=1 ;;
    -h|--help) sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    v*|[0-9]*) LAST_TAG="$arg" ;;
    *) echo "unknown arg: $arg" >&2; exit 2 ;;
  esac
done

if [[ -z "$LAST_TAG" ]]; then
  LAST_TAG="$(git describe --tags --abbrev=0 2>/dev/null || true)"
fi
if [[ -z "$LAST_TAG" ]]; then
  echo "no tags found; pass an explicit base ref (e.g. the first commit)" >&2
  exit 1
fi

# Phase headers ADDED to CLAUDE.md since the tag (a genuinely new phase block,
# not just extra bullets appended to an existing one — the header line itself
# must be a '+' in the diff). Read without mapfile for bash 3.2 (macOS) support.
NEW_HEADERS=()
while IFS= read -r line; do
  [[ -n "$line" ]] && NEW_HEADERS+=("$line")
done < <(
  git diff "$LAST_TAG"..HEAD -- "$CLAUDE_MD" 2>/dev/null \
    | grep -E '^\+\*\*Phase [0-9]' \
    | sed 's/^+//'
)

# Build "num<TAB>markdown-line" rows so we can sort newest-first by phase number.
rows=""
for hdr in ${NEW_HEADERS[@]+"${NEW_HEADERS[@]}"}; do
  num="$(sed -E 's/^\*\*Phase ([0-9.]+) .*/\1/' <<<"$hdr")"
  title="$(sed -E 's/^\*\*Phase [0-9.]+ \([^)]*\) — //; s/ \(complete\):\*\*$//; s/:\*\*$//' <<<"$hdr")"

  # First bullet of that phase block = best one-line summary source.
  bullet="$(awk -v h="$hdr" '
    index($0, h) == 1 { f = 1; next }
    f && /^- / { sub(/^- /, ""); print; exit }
  ' "$CLAUDE_MD")"

  # Strip bold markers; truncate to the first sentence; hard-cap length.
  summary="$(sed -E 's/\*\*//g' <<<"$bullet")"
  summary="${summary%%. *}"
  if [[ ${#summary} -gt 240 ]]; then
    summary="${summary:0:237}..."
  fi

  if [[ -n "$summary" ]]; then
    rows+="${num}	**${title} (Phase ${num})** — ${summary}"$'\n'
  else
    rows+="${num}	**${title} (Phase ${num})** — TODO summarise"$'\n'
  fi
done

# Candidate non-phase changes: feat/fix commits whose scope is NOT a phase
# (e.g. feat(slack), feat(ebpf), fix(doctor)) — these have no CLAUDE.md block
# and must be folded in by hand.
candidates="$(
  git log "$LAST_TAG"..HEAD --pretty='%s' 2>/dev/null \
    | grep -E '^(feat|fix)' \
    | grep -vE '^(feat|fix)\([0-9]+(\.[0-9]+)?(-[0-9]+)?\)' \
    | sed 's/^/  - /' || true
)"

render() {
  cat <<EOF
<!--
  AUTO-DRAFTED by scripts/draft-release-highlights.sh from CLAUDE.md phase
  blocks added since ${LAST_TAG}. This is a DRAFT — before tagging:
    • tighten each line and add an emoji (match prior releases' style)
    • drop minor / pure-fix phases that aren't headline-worthy
    • fold in the non-phase changes listed at the bottom of this comment
  Contents are injected verbatim into the release notes (see
  docs/release.md § Release highlights). HTML comments are hidden in GitHub's
  rendered view, so this note is safe to leave in.
-->
## ✨ Major additions highlighted

EOF

  if [[ -n "${rows//[$'\n']/}" ]]; then
    # newest phase first (numeric sort handles decimals: 119>118>117>112.1), then number
    printf '%s' "$rows" | sort -t$'\t' -k1,1rn | cut -f2- | awk 'NF { printf "%d. %s\n", NR, $0 }'
  else
    echo "_No new phase blocks in CLAUDE.md since ${LAST_TAG}. Add highlights by hand._"
  fi

  if [[ -n "$candidates" ]]; then
    cat <<EOF

<!--
  Candidate non-phase changes since ${LAST_TAG} (feat/fix not tied to a phase
  block) — review and fold any headline-worthy ones into the list above:
$candidates
-->
EOF
  fi
}

if [[ "$WRITE" -eq 1 ]]; then
  render > "$OUT_FILE"
  echo "wrote $OUT_FILE (draft from CLAUDE.md phase blocks since $LAST_TAG) — review & trim before tagging" >&2
else
  render
fi
