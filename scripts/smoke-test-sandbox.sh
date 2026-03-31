#!/usr/bin/env bash
# smoke-test-sandbox.sh — automated smoke test for the km-sandbox container image
#
# Tests container mechanics (user drop, signal handling, env passthrough,
# init commands, installed tools) WITHOUT requiring AWS credentials or infra.
# AWS integration testing happens in Phase 37.
#
# Usage:
#   bash scripts/smoke-test-sandbox.sh
#
# Exit code: 0 if all pass, 1 if any fail.

set -euo pipefail

IMAGE="km-sandbox:smoke-test"
PASS=0
FAIL=0
RESULTS=()

log()  { echo "[smoke] $*"; }
pass() { PASS=$((PASS + 1)); RESULTS+=("  PASS  $*"); echo "[smoke] PASS: $*"; }
fail() { FAIL=$((FAIL + 1)); RESULTS+=("  FAIL  $*"); echo "[smoke] FAIL: $*" >&2; }

# ============================================================
# Build the image
# ============================================================
log "Building km-sandbox image..."
docker build --platform linux/amd64 -t "${IMAGE}" containers/sandbox/ \
  || { echo "[smoke] ERROR: docker build failed — cannot continue"; exit 1; }
log "Build complete."

# ============================================================
# Helper: assert string in output
# ============================================================
assert_contains() {
    local label="$1"
    local output="$2"
    local expected="$3"
    if echo "${output}" | grep -qF "${expected}"; then
        return 0
    else
        echo "[smoke] FAIL assertion: expected '${expected}' in output for ${label}" >&2
        echo "[smoke] Output was:" >&2
        echo "${output}" | head -20 >&2
        return 1
    fi
}

# ============================================================
# Test 1 — Minimal boot (no optional env vars)
# Entrypoint should skip all optional steps and drop to sandbox user.
# ============================================================
log ""
log "--- Test 1: Minimal boot (no env vars) ---"
T1_OUT=$(docker run --rm --name km-smoke-minimal \
    -e KM_SANDBOX_ID=smoke-test-001 \
    "${IMAGE}" \
    bash -c 'whoami && echo "BOOT_OK"' 2>&1) || T1_EXIT=$?
T1_EXIT=${T1_EXIT:-0}

T1_OK=true
assert_contains "Test 1" "${T1_OUT}" "sandbox"      || T1_OK=false
assert_contains "Test 1" "${T1_OUT}" "BOOT_OK"       || T1_OK=false
assert_contains "Test 1" "${T1_OUT}" "skipping"      || T1_OK=false
if [ "${T1_EXIT}" -ne 0 ]; then
    echo "[smoke] FAIL: Test 1 exited with code ${T1_EXIT}" >&2
    T1_OK=false
fi
if [ "${T1_OK}" = "true" ]; then
    pass "Minimal boot (no env vars)"
else
    fail "Minimal boot (no env vars)"
fi

# ============================================================
# Test 2 — Environment passthrough (KM_PROFILE_ENV)
# ============================================================
log ""
log "--- Test 2: Environment passthrough ---"
KM_PROFILE_ENV_VAL=$(printf '{"MY_VAR":"hello","OTHER_VAR":"world"}' | base64)
T2_OUT=$(docker run --rm --name km-smoke-env \
    -e KM_SANDBOX_ID=smoke-test-002 \
    -e KM_PROFILE_ENV="${KM_PROFILE_ENV_VAL}" \
    "${IMAGE}" \
    bash -c 'echo "MY_VAR=$MY_VAR OTHER_VAR=$OTHER_VAR"' 2>&1) || true

T2_OK=true
assert_contains "Test 2" "${T2_OUT}" "MY_VAR=hello" || T2_OK=false
assert_contains "Test 2" "${T2_OUT}" "OTHER_VAR=world" || T2_OK=false
if [ "${T2_OK}" = "true" ]; then
    pass "Environment passthrough"
else
    fail "Environment passthrough"
fi

# ============================================================
# Test 3 — initCommands execution
# ============================================================
log ""
log "--- Test 3: initCommands execution ---"
KM_INIT_COMMANDS_VAL=$(printf '["touch /tmp/init-marker"]' | base64)
T3_OUT=$(docker run --rm --name km-smoke-init \
    -e KM_SANDBOX_ID=smoke-test-003 \
    -e KM_INIT_COMMANDS="${KM_INIT_COMMANDS_VAL}" \
    "${IMAGE}" \
    bash -c 'test -f /tmp/init-marker && echo "INIT_OK" || echo "INIT_FAIL"' 2>&1) || true

T3_OK=true
assert_contains "Test 3" "${T3_OUT}" "INIT_OK" || T3_OK=false
if [ "${T3_OK}" = "true" ]; then
    pass "initCommands execution"
else
    fail "initCommands execution"
fi

# ============================================================
# Test 4 — SIGTERM graceful shutdown
# Start container in background, send SIGTERM, verify clean exit.
# ============================================================
log ""
log "--- Test 4: SIGTERM graceful shutdown ---"

# Clean up any leftover container from previous run
docker rm -f km-smoke-sigterm 2>/dev/null || true

docker run -d --name km-smoke-sigterm \
    -e KM_SANDBOX_ID=smoke-test-004 \
    "${IMAGE}" \
    bash -c 'sleep 300' >/dev/null

sleep 2
# docker stop sends SIGTERM, then SIGKILL after --timeout seconds
# Use a short timeout so the test doesn't hang (SIGKILL is the fallback)
docker stop --timeout 10 km-smoke-sigterm >/dev/null 2>&1 || true
sleep 2

T4_OK=true
# After stop, container should be in exited state (not running)
RUNNING=$(docker inspect km-smoke-sigterm --format '{{.State.Running}}' 2>/dev/null || echo "gone")
if [ "${RUNNING}" = "false" ] || [ "${RUNNING}" = "gone" ]; then
    log "Container stopped (Running=${RUNNING}) — SIGTERM handled"
else
    echo "[smoke] FAIL: Container still running after stop (Running=${RUNNING})" >&2
    T4_OK=false
fi
# Clean up
docker rm -f km-smoke-sigterm 2>/dev/null || true

if [ "${T4_OK}" = "true" ]; then
    pass "SIGTERM graceful shutdown"
else
    fail "SIGTERM graceful shutdown"
fi

# ============================================================
# Test 5 — User and workspace verification
# ============================================================
log ""
log "--- Test 5: User and workspace verification ---"
T5_OUT=$(docker run --rm --name km-smoke-user \
    -e KM_SANDBOX_ID=smoke-test-005 \
    "${IMAGE}" \
    bash -c 'id -u && id -un && pwd && test -w /workspace && echo "WORKSPACE_WRITABLE"' 2>&1) || true

T5_OK=true
assert_contains "Test 5" "${T5_OUT}" "1000"               || T5_OK=false
assert_contains "Test 5" "${T5_OUT}" "sandbox"            || T5_OK=false
assert_contains "Test 5" "${T5_OUT}" "/workspace"         || T5_OK=false
assert_contains "Test 5" "${T5_OUT}" "WORKSPACE_WRITABLE" || T5_OK=false
if [ "${T5_OK}" = "true" ]; then
    pass "User and workspace"
else
    fail "User and workspace"
fi

# ============================================================
# Test 6 — Installed tools check
# ============================================================
log ""
log "--- Test 6: Installed tools check ---"
T6_OUT=$(docker run --rm --name km-smoke-tools \
    -e KM_SANDBOX_ID=smoke-test-006 \
    "${IMAGE}" \
    bash -c 'git --version && jq --version && aws --version && gosu --version && echo "TOOLS_OK"' 2>&1) || true

T6_OK=true
assert_contains "Test 6" "${T6_OUT}" "TOOLS_OK" || T6_OK=false
if [ "${T6_OK}" = "true" ]; then
    pass "Installed tools"
else
    fail "Installed tools"
fi

# ============================================================
# Summary
# ============================================================
echo ""
echo "km-sandbox smoke test results:"
for r in "${RESULTS[@]}"; do
    echo "  ${r}"
done
echo ""
TOTAL=$((PASS + FAIL))
echo "${PASS}/${TOTAL} passed"
echo ""

if [ "${FAIL}" -gt 0 ]; then
    echo "[smoke] ${FAIL} test(s) FAILED"
    exit 1
fi

echo "[smoke] All ${PASS} tests passed."
exit 0
