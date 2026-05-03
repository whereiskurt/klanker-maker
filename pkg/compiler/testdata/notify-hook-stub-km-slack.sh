#!/usr/bin/env bash
# Stub for tests: records subcommand + flags + stdin body to KM_SLACK_STUB_LOG.
# Mirrors pkg/compiler/testdata/notify-hook-stub-km-send.sh.
#
# Plan 68-09 notify-hook script tests put this on PATH as `km-slack` and assert
# on the captured calls. `post` returns a deterministic ts so the auto-thread-
# parent caching path can be exercised; `upload` and `record-mapping` exit 0
# silently.
set -euo pipefail
log="${KM_SLACK_STUB_LOG:-/tmp/km-slack-stub.calls}"
sub="${1:-}"
shift || true
{
  echo "---"
  echo "subcommand: ${sub}"
  echo "args: $*"
} >> "$log"
case "${sub}" in
  post)
    # Real km-slack post writes "km-slack: posted ts=<...>" to stderr; mirror
    # that contract so the notify-hook's `grep -oE 'ts=[0-9.]+'` capture works
    # under both real and stub binaries. Also keep the legacy stdout JSON for
    # any caller that scrapes it. Each invocation increments the ts so the
    # auto-thread-parent caching test can distinguish parent vs streaming
    # posts.
    counter_file="${KM_SLACK_STUB_LOG:-/tmp/km-slack-stub.calls}.counter"
    n=0
    [[ -f "$counter_file" ]] && n=$(cat "$counter_file" 2>/dev/null || echo 0)
    n=$((n + 1))
    echo "$n" > "$counter_file"
    ts="1700000000.0001${n}"
    echo "km-slack: posted ts=${ts}" >&2
    echo "{\"ok\":true,\"ts\":\"${ts}\"}"
    ;;
  upload)
    # Real km-slack upload writes "km-slack upload: ok ts=<...>" to stderr.
    counter_file="${KM_SLACK_STUB_LOG:-/tmp/km-slack-stub.calls}.counter"
    n=0
    [[ -f "$counter_file" ]] && n=$(cat "$counter_file" 2>/dev/null || echo 0)
    n=$((n + 1))
    echo "$n" > "$counter_file"
    ts="1700000000.0009${n}"
    echo "km-slack upload: ok ts=${ts}" >&2
    ;;
  record-mapping|*)
    :
    ;;
esac
exit 0
