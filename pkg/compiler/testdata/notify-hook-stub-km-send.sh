#!/bin/bash
# Stub km-send for Phase 62 hook script tests.
# Activated via PATH override in pkg/compiler/notify_hook_script_test.go (Plan 03).
# Records the invocation and the body file contents to $KM_NOTIFY_TEST_LOG
# so the Go test can assert on subject, body, --to, etc.
#
# Usage (from Go test):
#   tmp := t.TempDir()
#   stub := filepath.Join(tmp, "km-send")
#   // copy testdata/notify-hook-stub-km-send.sh -> stub
#   os.Chmod(stub, 0755)
#   os.Setenv("PATH", tmp+":"+os.Getenv("PATH"))
#   os.Setenv("KM_NOTIFY_TEST_LOG", filepath.Join(tmp, "calls.log"))
#
# Exit code is controlled by KM_NOTIFY_TEST_FAIL: when set to "1", returns 1
# (used to verify hook always exits 0 even on send failure).

set -u
log="${KM_NOTIFY_TEST_LOG:-/dev/null}"

# Capture args verbatim, one per line.
{
  printf '=== km-send call ===\n'
  printf 'args: %s\n' "$*"
  for arg in "$@"; do
    printf '  arg: %s\n' "$arg"
  done

  # Find --body <file> and dump its contents.
  body_file=""
  prev=""
  for arg in "$@"; do
    if [[ "$prev" == "--body" ]]; then
      body_file="$arg"
      break
    fi
    prev="$arg"
  done
  if [[ -n "$body_file" && -f "$body_file" ]]; then
    printf 'body_file: %s\n' "$body_file"
    printf 'body_contents_begin\n'
    cat "$body_file"
    printf '\nbody_contents_end\n'
  else
    printf 'body_file: (none or missing)\n'
  fi
  printf '=== end ===\n\n'
} >> "$log"

if [[ "${KM_NOTIFY_TEST_FAIL:-0}" == "1" ]]; then
  exit 1
fi
exit 0
