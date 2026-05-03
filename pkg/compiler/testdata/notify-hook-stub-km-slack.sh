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
    echo '{"ok":true,"ts":"1700000000.000100"}'
    ;;
  upload|record-mapping|*)
    :
    ;;
esac
exit 0
