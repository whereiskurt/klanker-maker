---
phase: 116-km-check-serverless-check-runner
plan: "04"
subsystem: checks
tags: [python, lambda, boto3, pytest, eventbridge, s3, km-check]

requires:
  - phase: 116-03
    provides: scaffolding terraform modules (dynamodb-checks, check-runner-role) + Go config plumbing for checks.triggers

provides:
  - _km_check_bootstrap.py: Lambda handler shipped in every check zip (env-build, subprocess exec, S3 capture, when_py eval, CheckDispatch emit)
  - KM_CHECK_TRIGGER.schema.md: canonical env-var JSON schema (single source of truth for 116-05 CLI bake + 116-06 ttl-handler consumer)
  - conftest.py + test_bootstrap.py: pytest unit tests with fully stubbed boto3 (no real AWS SDK needed)

affects:
  - 116-05 (km check deploy CLI — bakes KM_CHECK_TRIGGER from checks.triggers config)
  - 116-06 (ttl-handler CheckDispatch consumer — reads Detail keys defined here)
  - 116-07 (km check CLI commands)
  - 116-08 (UAT — live behavior; embedded Python is invisible to Go goldens per project_skill_bash_needs_live_uat)

tech-stack:
  added: [pytest 9.1.0 (local test runner; not a Lambda dep)]
  patterns:
    - "Lambda handler is the bootstrap; author snippets are plain Python programs (no handler signature)"
    - "Env build: later wins (static Lambda env -> SSM secrets -> per-run event['env'])"
    - "Non-JSON stdout guard: capture-only, never triggers, hard-coded (not configurable)"
    - "when_py wrapped as def _pred(out): <body>; fail-closed on exception (safe for dispatch)"
    - "CheckDispatch Detail keys: snake_case, event_type/check_name/alias/prompt/profile_name/on_absent/reason/cooldown_seconds/auto_start"
    - "boto3 module injected into sys.modules at test-collection time (no real AWS SDK required locally)"

key-files:
  created:
    - profiles/checks/_bootstrap/_km_check_bootstrap.py
    - profiles/checks/_bootstrap/KM_CHECK_TRIGGER.schema.md
    - profiles/checks/_bootstrap/conftest.py
    - profiles/checks/_bootstrap/test_bootstrap.py

key-decisions:
  - "KM_CHECK_TRIGGER.schema.md is the SINGLE SOURCE OF TRUTH for the trigger env-var contract; 116-05 and 116-06 are consumers, never definers"
  - "CheckDispatch Detail uses check_name (not check) for the run-identifying field; matches existing EventBridge envelope snake_case convention"
  - "auto_start: true is always set in CheckDispatch Detail so ttl-handler auto-resumes stopped/paused sandboxes"
  - "when_py predicate is fail-closed: any exception -> not triggered (safe for dispatch side-effects)"
  - "Non-JSON stdout guard is unconditional — no configuration knob; always capture-only"
  - "boto3 is a Lambda runtime dep only; local tests stub the entire module via sys.modules injection (no pip install boto3 needed)"
  - "cooldown_seconds passed through in CheckDispatch Detail; enforcement is Stage B (ttl-handler nonces table), not bootstrap"

patterns-established:
  - "Lambda runtime deps (boto3) are mocked via sys.modules injection in conftest.py; tests need no AWS SDK locally"
  - "when_py body wrapped with textwrap.indent into def _pred(out): for safe exec isolation"

requirements-completed: []

duration: 3min
completed: "2026-06-18"
---

# Phase 116 Plan 04: km check Bootstrap Handler + KM_CHECK_TRIGGER Schema Summary

**Python Lambda bootstrap with when_py predicate eval, S3 capture, and CheckDispatch emission; canonical KM_CHECK_TRIGGER env-var JSON schema defined as single cross-plan contract; 3/3 pytest tests GREEN with fully-stubbed boto3**

## Performance

- **Duration:** 3 min
- **Started:** 2026-06-18T00:37:10Z
- **Completed:** 2026-06-18T00:40:35Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments

- Authored `_km_check_bootstrap.py` (218 lines) — the actual Lambda entrypoint for every check; implements env-build (static→SSM→per-run, later wins), subprocess exec, S3 capture, when_py predicate evaluation (fail-closed), and CheckDispatch emission
- Defined `KM_CHECK_TRIGGER.schema.md` — canonical JSON schema for the env var baked at deploy time; single source of truth for Plans 116-05 (CLI bake) and 116-06 (ttl-handler consumer)
- TDD tests (3/3 GREEN): dispatch (JSON stdout + truthy predicate → CheckDispatch emitted with expanded prompt), notjson (non-JSON stdout → S3-only, no trigger), env (static→SSM→per-run precedence verified via subprocess env kwarg)

## KM_CHECK_TRIGGER Schema (authoritative — copy to 116-05/06)

```json
{
  "check":            "<check name>",
  "when_py":          "<Python predicate body>",
  "alias":            "<target sandbox alias — REQUIRED>",
  "prompt":           "<template: {{reason}} + {{out.<field>}}>",
  "profile":          "<profile slug for cold-create>",
  "on_absent":        "cold-create | skip",
  "cooldown_seconds": 0
}
```

Empty/absent `when_py` → no trigger (capture-only). `@file` resolved operator-side at deploy; Lambda env always has inline string.

## CheckDispatch Detail (authoritative cross-plan contract)

```json
{
  "event_type":       "check-dispatch",
  "check_name":       "<KM_CHECK_NAME>",
  "alias":            "<trigger.alias>",
  "prompt":           "<fully expanded prompt>",
  "profile_name":     "<trigger.profile or empty string>",
  "on_absent":        "<cold-create | skip>",
  "reason":           "<predicate reason or empty string>",
  "cooldown_seconds": 0,
  "auto_start":       true
}
```

Source: `km.sandbox`, DetailType: `CheckDispatch`. Stage B (116-06 ttl-handler) routes on `event_type == "check-dispatch"`.

## Task Commits

1. **Task 1: Define KM_CHECK_TRIGGER schema + write bootstrap handler** - `ec70484e` (feat)
2. **Task 2 (TDD GREEN): pytest unit tests for the bootstrap (boto3 stubbed)** - `849807a6` (test)

## Files Created/Modified

- `profiles/checks/_bootstrap/_km_check_bootstrap.py` — Lambda handler: env-build, subprocess exec, S3 put_object, when_py eval, CheckDispatch put_events (218 lines)
- `profiles/checks/_bootstrap/KM_CHECK_TRIGGER.schema.md` — canonical trigger env-var JSON schema + CheckDispatch Detail contract
- `profiles/checks/_bootstrap/conftest.py` — pytest fixtures: fake boto3 module injected into sys.modules; FakeS3/SSM/EventsClient; FakeContext; BoTo3ClientFactory
- `profiles/checks/_bootstrap/test_bootstrap.py` — 3 tests: dispatch / notjson / env precedence

## Decisions Made

- `check_name` (not `check`) is the identifying field in CheckDispatch Detail — matches snake_case convention used elsewhere in the event envelope
- `auto_start: true` is always embedded so ttl-handler knows to resume stopped/paused sandboxes without a separate flag
- boto3 stubbed via `sys.modules` injection (not `mock.patch`) because the module is imported at handler import time; `monkeypatch.setitem(sys.modules, "boto3", fake_module)` is the reliable pattern
- `when_py` wrapping uses `textwrap.indent` for clean body indentation (avoids off-by-one if the body has blank lines)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] boto3 not installed locally; conftest approach changed**
- **Found during:** Task 2 RED phase
- **Issue:** `conftest.py` tried to `import boto3` inside fixtures; boto3 is a Lambda runtime dep not present locally; tests errored with `ModuleNotFoundError: No module named 'boto3'`
- **Fix:** Replaced `import boto3` in conftest with `sys.modules["boto3"] = fake_boto3_module` injection at fixture time (before the bootstrap module is imported); `monkeypatch.setitem` ensures cleanup after each test
- **Files modified:** `profiles/checks/_bootstrap/conftest.py`
- **Verification:** `python3 -m pytest profiles/checks/_bootstrap/ -q` → 3 passed
- **Committed in:** `849807a6` (Task 2 test commit)

---

**Total deviations:** 1 auto-fixed (1 blocking — boto3 not locally installed)
**Impact on plan:** Necessary fix; the final approach is cleaner (no real AWS SDK dep in tests). No scope creep.

## Issues Encountered

None beyond the boto3 stub deviation above.

## User Setup Required

None — no external service configuration required for this plan. Live behavior is verified in Plan 116-08 UAT.

## Next Phase Readiness

- Plan 116-05 (CLI `km check deploy`): can bake `KM_CHECK_TRIGGER` from `checks.triggers` config using the schema defined here
- Plan 116-06 (ttl-handler CheckDispatch consumer): can implement the `check-dispatch` event_type switch case using the Detail contract defined here
- The non-JSON stdout guard is unconditional — 116-06 never receives a CheckDispatch for non-JSON output (contract guarantee)
- NOTE: live behavior (real Lambda exec, real S3 put_object, real EventBridge) is verified in Plan 116-08 UAT; embedded Python is invisible to Go goldens (project_skill_bash_needs_live_uat)

---
*Phase: 116-km-check-serverless-check-runner*
*Completed: 2026-06-18*
