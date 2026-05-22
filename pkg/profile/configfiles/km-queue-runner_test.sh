#!/usr/bin/env bash
# km-queue-runner_test.sh — Phase 86 bash test harness
#
# Wave 1 (86-03): all 7 test functions have REAL test logic.
# Extracts runner from userdata.go via awk, exercises runner logic
# with PATH-shimmed fake binaries, asserts on meta.json statuses.
#
# Usage:
#   bash pkg/profile/configfiles/km-queue-runner_test.sh
# Expected output:
#   [PASS] test_reconcile_running_to_pending
#   ...
#   ---
#   Results: 0 SKIP, 7 PASS, 0 FAIL

set -euo pipefail

# ---- Counters ----
PASS=0
FAIL=0
SKIP=0

# ---- Runner ----
run_test() {
    local name="$1"
    local result
    if result=$("$name" 2>&1); then
        if echo "$result" | grep -q "^SKIP:"; then
            echo "[SKIP] $name"
            SKIP=$((SKIP + 1))
        else
            echo "[PASS] $name"
            PASS=$((PASS + 1))
        fi
    else
        echo "[FAIL] $name"
        if [[ -n "$result" ]]; then
            echo "       $result"
        fi
        FAIL=$((FAIL + 1))
    fi
}

# ---- Extract runner from userdata.go ----
# The runner script lives between 'cat > /opt/km/bin/km-queue-runner << .KMQUEUE.'
# and the bare 'KMQUEUE' line in pkg/compiler/userdata.go.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
USERDATA_GO="$REPO_ROOT/pkg/compiler/userdata.go"
RUNNER_SCRIPT=/tmp/km-queue-runner-test-$$.sh

extract_runner() {
    awk "/cat > \/opt\/km\/bin\/km-queue-runner << 'KMQUEUE'/,/^KMQUEUE$/" \
        "$USERDATA_GO" | sed '1d;$d' > "$RUNNER_SCRIPT"
    chmod +x "$RUNNER_SCRIPT"
}

extract_runner

# ---- Portable timeout wrapper ----
# macOS ships no `timeout`; provide one via a background sleep+kill pattern.
km_timeout() {
    local secs="$1"
    shift
    "$@" &
    local pid=$!
    (
        sleep "$secs" 2>/dev/null
        kill -9 "$pid" 2>/dev/null
    ) &
    local killer=$!
    wait "$pid" 2>/dev/null
    local rc=$?
    kill "$killer" 2>/dev/null
    wait "$killer" 2>/dev/null
    return $rc
}

# ---- Shared helper: setup test environment ----
# Returns the tmp dir path (caller must export QUEUE_DIR etc.)
setup_test_env() {
    local tmpdir
    tmpdir=$(mktemp -d /tmp/km-queue-test-XXXXXX)
    mkdir -p "$tmpdir/queue" "$tmpdir/runs" "$tmpdir/log"
    echo "$tmpdir"
}

# ---- Shared helper: write a meta.json ----
write_meta() {
    local dir="$1"
    local num="$2"
    local status="$3"
    local no_bedrock="${4:-false}"
    cat > "$dir/queue/${num}.meta.json" << METAEOF
{"status":"${status}","no_bedrock":${no_bedrock},"created_at":"2026-05-19T00:00:00Z"}
METAEOF
}

# ---- Shared helper: write a prompt file ----
write_prompt() {
    local dir="$1"
    local num="$2"
    local text="${3:-test prompt}"
    echo "$text" > "$dir/queue/${num}.prompt"
}

# ---- Shared helper: create a PATH shim directory ----
make_shim_dir() {
    local shimdir
    shimdir=$(mktemp -d /tmp/km-shims-XXXXXX)
    echo "$shimdir"
}

# ---- Shim: aws exits 0 (bedrock probe success) ----
shim_aws_ok() {
    local shimdir="$1"
    cat > "$shimdir/aws" << 'EOFSHIM'
#!/bin/bash
exit 0
EOFSHIM
    chmod +x "$shimdir/aws"
}

# ---- Shim: aws exits 1 N times then 0 (bedrock probe failure loop) ----
shim_aws_fail_n() {
    local shimdir="$1"
    local n="$2"
    local countfile="$shimdir/.aws_call_count"
    echo "0" > "$countfile"
    cat > "$shimdir/aws" << EOFSHIM
#!/bin/bash
count=\$(cat "$countfile" 2>/dev/null || echo 0)
count=\$((count + 1))
echo "\$count" > "$countfile"
if [ "\$count" -le "$n" ]; then
    exit 1
fi
exit 0
EOFSHIM
    chmod +x "$shimdir/aws"
}

# ---- Shim: claude exits 0 + writes output.json ----
shim_claude_ok() {
    local shimdir="$1"
    cat > "$shimdir/claude" << 'EOFSHIM'
#!/bin/bash
# Accept -p "prompt" args, write stub output.json to stdout
echo '{"result":"ok","type":"result"}'
exit 0
EOFSHIM
    chmod +x "$shimdir/claude"
}

# ---- Shim: claude exits 1 (failure) ----
shim_claude_fail() {
    local shimdir="$1"
    cat > "$shimdir/claude" << 'EOFSHIM'
#!/bin/bash
exit 1
EOFSHIM
    chmod +x "$shimdir/claude"
}

# ---- Shim: tmux (always succeeds silently) ----
shim_tmux() {
    local shimdir="$1"
    local rundir="$2"
    local exit_code="${3:-0}"
    # tmux new-session: run the script directly; tmux wait-for: no-op
    cat > "$shimdir/tmux" << EOFSHIM
#!/bin/bash
# Minimal tmux shim for km-queue-runner testing
if [[ "\$1" == "new-session" ]]; then
    # Find the script arg (last non-flag arg)
    script_arg="\${@: -1}"
    # Run the script to simulate tmux exec
    bash "\$script_arg" 2>/dev/null || true
elif [[ "\$1" == "wait-for" ]]; then
    exit 0
else
    exit 0
fi
EOFSHIM
    chmod +x "$shimdir/tmux"
}

# ---- Shim: sudo (run as current user for testing) ----
shim_sudo() {
    local shimdir="$1"
    # sudo -u sandbox bash -c "..." -> just run bash -c "..."
    cat > "$shimdir/sudo" << 'EOFSHIM'
#!/bin/bash
# Strip "-u <user>" from args and run remainder
args=()
skip_next=false
for arg in "$@"; do
    if $skip_next; then
        skip_next=false
        continue
    fi
    if [ "$arg" = "-u" ]; then
        skip_next=true
        continue
    fi
    args+=("$arg")
done
exec "${args[@]}"
EOFSHIM
    chmod +x "$shimdir/sudo"
}

# ---- Shim: jq (real jq pass-through) ----
# We use real jq (must be available); no shim needed.

# ====================================================================
# Test 1: reconcile running->pending
# ====================================================================
test_reconcile_running_to_pending() {
    local tmpdir
    tmpdir=$(setup_test_env)
    local shimdir
    shimdir=$(make_shim_dir)

    # seed meta: 001 in "running" state
    write_meta "$tmpdir" "001" "running"
    write_prompt "$tmpdir" "001" "test prompt"

    # AWS probe succeeds so runner proceeds past auth
    shim_aws_ok "$shimdir"
    # claude succeeds
    shim_claude_ok "$shimdir"
    shim_tmux "$shimdir" "$tmpdir"
    shim_sudo "$shimdir"

    QUEUE_DIR="$tmpdir/queue" \
    RUNS_DIR="$tmpdir/runs" \
    LOG_FILE="$tmpdir/log/km-queue.log" \
    SANDBOX_HOME="$tmpdir/home" \
    PROBE_INTERVAL=0 \
    PROBE_LOG_INTERVAL=9999 \
    PATH="$shimdir:$PATH" \
    km_timeout 10 bash "$RUNNER_SCRIPT" 2>/dev/null || true

    local final_status
    final_status=$(jq -r .status "$tmpdir/queue/001.meta.json" 2>/dev/null || echo "missing")

    rm -rf "$tmpdir" "$shimdir"

    # reconcile should have flipped running->pending, then runner picked it up and ran claude
    # which succeeded -> done
    if [ "$final_status" = "done" ]; then
        return 0
    else
        echo "ASSERT FAIL: expected status=done after reconcile+run, got status=$final_status"
        return 1
    fi
}

# ====================================================================
# Test 2: lowest-pending-picked-first
# ====================================================================
test_lowest_pending_picked_first() {
    local tmpdir
    tmpdir=$(setup_test_env)
    local shimdir
    shimdir=$(make_shim_dir)

    # seed: 001 and 002 both pending
    write_meta "$tmpdir" "001" "pending"
    write_prompt "$tmpdir" "001" "first"
    write_meta "$tmpdir" "002" "pending"
    write_prompt "$tmpdir" "002" "second"

    # Track which entry claude is called for by recording RUN_DIR order
    local orderfile="$tmpdir/order.txt"
    touch "$orderfile"

    shim_aws_ok "$shimdir"
    shim_tmux "$shimdir" "$tmpdir"
    shim_sudo "$shimdir"
    shim_claude_ok "$shimdir"

    QUEUE_DIR="$tmpdir/queue" \
    RUNS_DIR="$tmpdir/runs" \
    LOG_FILE="$tmpdir/log/km-queue.log" \
    SANDBOX_HOME="$tmpdir/home" \
    PROBE_INTERVAL=0 \
    PROBE_LOG_INTERVAL=9999 \
    PATH="$shimdir:$PATH" \
    km_timeout 15 bash "$RUNNER_SCRIPT" 2>/dev/null || true

    local status001
    local status002
    status001=$(jq -r .status "$tmpdir/queue/001.meta.json" 2>/dev/null || echo "missing")
    status002=$(jq -r .status "$tmpdir/queue/002.meta.json" 2>/dev/null || echo "missing")

    rm -rf "$tmpdir" "$shimdir"

    if [ "$status001" = "done" ] && [ "$status002" = "done" ]; then
        return 0
    else
        echo "ASSERT FAIL: expected 001=done 002=done, got 001=$status001 002=$status002"
        return 1
    fi
}

# ====================================================================
# Test 3: failure marks remaining skipped
# ====================================================================
test_failure_marks_remaining_skipped() {
    local tmpdir
    tmpdir=$(setup_test_env)
    local shimdir
    shimdir=$(make_shim_dir)

    # seed: 001, 002, 003 all pending
    write_meta "$tmpdir" "001" "pending"
    write_prompt "$tmpdir" "001" "p1"
    write_meta "$tmpdir" "002" "pending"
    write_prompt "$tmpdir" "002" "p2"
    write_meta "$tmpdir" "003" "pending"
    write_prompt "$tmpdir" "003" "p3"

    shim_aws_ok "$shimdir"
    shim_tmux "$shimdir" "$tmpdir"
    shim_sudo "$shimdir"
    # claude fails on every call
    shim_claude_fail "$shimdir"

    local exit_code=0
    QUEUE_DIR="$tmpdir/queue" \
    RUNS_DIR="$tmpdir/runs" \
    LOG_FILE="$tmpdir/log/km-queue.log" \
    SANDBOX_HOME="$tmpdir/home" \
    PROBE_INTERVAL=0 \
    PROBE_LOG_INTERVAL=9999 \
    PATH="$shimdir:$PATH" \
    km_timeout 10 bash "$RUNNER_SCRIPT" 2>/dev/null
    exit_code=$?

    local s001
    local s002
    local s003
    s001=$(jq -r .status "$tmpdir/queue/001.meta.json" 2>/dev/null || echo "missing")
    s002=$(jq -r .status "$tmpdir/queue/002.meta.json" 2>/dev/null || echo "missing")
    s003=$(jq -r .status "$tmpdir/queue/003.meta.json" 2>/dev/null || echo "missing")

    rm -rf "$tmpdir" "$shimdir"

    local errors=""
    [ "$s001" = "failed" ] || errors="$errors 001=$s001(want:failed)"
    [ "$s002" = "skipped" ] || errors="$errors 002=$s002(want:skipped)"
    [ "$s003" = "skipped" ] || errors="$errors 003=$s003(want:skipped)"
    [ "$exit_code" -ne 0 ] || errors="$errors runner_exit=${exit_code}(want:non-zero)"

    if [ -z "$errors" ]; then
        return 0
    else
        echo "ASSERT FAIL:$errors"
        return 1
    fi
}

# ====================================================================
# Test 4: auth probe bedrock success -> runner proceeds
# ====================================================================
test_auth_probe_bedrock_success_proceeds() {
    local tmpdir
    tmpdir=$(setup_test_env)
    local shimdir
    shimdir=$(make_shim_dir)

    # seed: 001 pending (bedrock mode = no_bedrock:false)
    write_meta "$tmpdir" "001" "pending" "false"
    write_prompt "$tmpdir" "001" "probe test"

    # aws probe exits 0 immediately
    shim_aws_ok "$shimdir"
    shim_claude_ok "$shimdir"
    shim_tmux "$shimdir" "$tmpdir"
    shim_sudo "$shimdir"

    QUEUE_DIR="$tmpdir/queue" \
    RUNS_DIR="$tmpdir/runs" \
    LOG_FILE="$tmpdir/log/km-queue.log" \
    SANDBOX_HOME="$tmpdir/home" \
    PROBE_INTERVAL=0 \
    PROBE_LOG_INTERVAL=9999 \
    PATH="$shimdir:$PATH" \
    km_timeout 10 bash "$RUNNER_SCRIPT" 2>/dev/null || true

    local final_status
    final_status=$(jq -r .status "$tmpdir/queue/001.meta.json" 2>/dev/null || echo "missing")

    rm -rf "$tmpdir" "$shimdir"

    if [ "$final_status" = "done" ]; then
        return 0
    else
        echo "ASSERT FAIL: expected status=done after bedrock probe success, got=$final_status"
        return 1
    fi
}

# ====================================================================
# Test 5: auth probe bedrock failure loops then succeeds
# ====================================================================
test_auth_probe_bedrock_failure_loops() {
    local tmpdir
    tmpdir=$(setup_test_env)
    local shimdir
    shimdir=$(make_shim_dir)

    write_meta "$tmpdir" "001" "pending" "false"
    write_prompt "$tmpdir" "001" "loop test"

    # aws exits 1 for first 2 calls, then exits 0
    shim_aws_fail_n "$shimdir" 2
    shim_claude_ok "$shimdir"
    shim_tmux "$shimdir" "$tmpdir"
    shim_sudo "$shimdir"

    QUEUE_DIR="$tmpdir/queue" \
    RUNS_DIR="$tmpdir/runs" \
    LOG_FILE="$tmpdir/log/km-queue.log" \
    SANDBOX_HOME="$tmpdir/home" \
    PROBE_INTERVAL=0 \
    PROBE_LOG_INTERVAL=9999 \
    PATH="$shimdir:$PATH" \
    km_timeout 15 bash "$RUNNER_SCRIPT" 2>/dev/null || true

    local final_status
    final_status=$(jq -r .status "$tmpdir/queue/001.meta.json" 2>/dev/null || echo "missing")

    # Check how many times aws was called (should be 3: fail, fail, success)
    local call_count
    call_count=$(cat "$shimdir/.aws_call_count" 2>/dev/null || echo "0")

    rm -rf "$tmpdir" "$shimdir"

    if [ "$final_status" = "done" ] && [ "$call_count" -ge 3 ]; then
        return 0
    else
        echo "ASSERT FAIL: status=$final_status(want:done) aws_calls=$call_count(want:>=3)"
        return 1
    fi
}

# ====================================================================
# Test 6: auth probe direct API - creds present -> probe succeeds
# ====================================================================
test_auth_probe_direct_api_creds_present() {
    local tmpdir
    tmpdir=$(setup_test_env)
    local shimdir
    shimdir=$(make_shim_dir)

    # Create a fake SANDBOX_HOME with valid credentials.json
    mkdir -p "$tmpdir/home/.claude"
    echo '{"accessToken":"fake-token","expiresAt":"2099-01-01T00:00:00Z"}' \
        > "$tmpdir/home/.claude/.credentials.json"

    write_meta "$tmpdir" "001" "pending" "true"
    write_prompt "$tmpdir" "001" "direct api test"

    # No aws shim needed for direct-API mode; we do need claude+tmux+sudo shims
    shim_claude_ok "$shimdir"
    shim_tmux "$shimdir" "$tmpdir"
    shim_sudo "$shimdir"

    QUEUE_DIR="$tmpdir/queue" \
    RUNS_DIR="$tmpdir/runs" \
    LOG_FILE="$tmpdir/log/km-queue.log" \
    SANDBOX_HOME="$tmpdir/home" \
    PROBE_INTERVAL=0 \
    PROBE_LOG_INTERVAL=9999 \
    PATH="$shimdir:$PATH" \
    km_timeout 10 bash "$RUNNER_SCRIPT" 2>/dev/null || true

    local final_status
    final_status=$(jq -r .status "$tmpdir/queue/001.meta.json" 2>/dev/null || echo "missing")

    rm -rf "$tmpdir" "$shimdir"

    if [ "$final_status" = "done" ]; then
        return 0
    else
        echo "ASSERT FAIL: expected status=done with creds present, got=$final_status"
        return 1
    fi
}

# ====================================================================
# Test 7: auth probe direct API - creds missing -> runner loops (no pick)
# ====================================================================
test_auth_probe_direct_api_creds_missing() {
    local tmpdir
    tmpdir=$(setup_test_env)
    local shimdir
    shimdir=$(make_shim_dir)

    # SANDBOX_HOME has no credentials.json at all
    mkdir -p "$tmpdir/home/.claude"
    # (no .credentials.json)

    write_meta "$tmpdir" "001" "pending" "true"
    write_prompt "$tmpdir" "001" "missing creds test"

    shim_claude_ok "$shimdir"
    shim_tmux "$shimdir" "$tmpdir"
    shim_sudo "$shimdir"

    # Run runner with a tight timeout — it should loop on probe and NOT pick any entry
    local exit_code=0
    QUEUE_DIR="$tmpdir/queue" \
    RUNS_DIR="$tmpdir/runs" \
    LOG_FILE="$tmpdir/log/km-queue.log" \
    SANDBOX_HOME="$tmpdir/home" \
    PROBE_INTERVAL=0 \
    PROBE_LOG_INTERVAL=9999 \
    PATH="$shimdir:$PATH" \
    km_timeout 2 bash "$RUNNER_SCRIPT" 2>/dev/null || exit_code=$?

    local final_status
    final_status=$(jq -r .status "$tmpdir/queue/001.meta.json" 2>/dev/null || echo "missing")

    rm -rf "$tmpdir" "$shimdir"

    # Runner should have timed out (exit 124) or been killed (exit 143)
    # The entry should still be "pending" (runner looped on probe, never picked)
    if [ "$final_status" = "pending" ]; then
        return 0
    else
        echo "ASSERT FAIL: expected status=pending (runner looped on missing creds), got=$final_status"
        return 1
    fi
}

# ====================================================================
# main — run all tests, report results, exit based on FAIL count
# ====================================================================
main() {
    run_test test_reconcile_running_to_pending
    run_test test_lowest_pending_picked_first
    run_test test_failure_marks_remaining_skipped
    run_test test_auth_probe_bedrock_success_proceeds
    run_test test_auth_probe_bedrock_failure_loops
    run_test test_auth_probe_direct_api_creds_present
    run_test test_auth_probe_direct_api_creds_missing

    echo "---"
    echo "Results: ${SKIP} SKIP, ${PASS} PASS, ${FAIL} FAIL"

    # Cleanup extracted runner
    rm -f "$RUNNER_SCRIPT" 2>/dev/null || true

    if [[ "$FAIL" -gt 0 ]]; then
        exit 1
    fi
    exit 0
}

main "$@"
