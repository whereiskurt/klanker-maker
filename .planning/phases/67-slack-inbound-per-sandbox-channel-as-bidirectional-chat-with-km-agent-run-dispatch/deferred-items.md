# Deferred Items — Phase 67

Out-of-scope discoveries from plan execution. These are NOT addressed by the
current plan but should be picked up later (a new gap-closure plan or follow-up
phase).

## Pre-existing test failure: TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime

**Discovered during:** Plan 67-11 execution (2026-05-03)
**File:** pkg/compiler/userdata_notify_test.go:472
**Status:** Pre-existing — failing on `git stash`-clean working tree before any 67-11 edits applied

**Failure:**
```
--- FAIL: TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime (0.00s)
    userdata_notify_test.go:480: user-data must NOT contain KM_SLACK_BRIDGE_URL at compile time (Plan 08 injects at runtime)
```

**Root cause:** The cloud-init Slack runtime block at `pkg/compiler/userdata.go:175-215`
(written by Phase 67 cloud-init SSM-fetch) emits the literal
`export KM_SLACK_BRIDGE_URL="$BRIDGE_URL"` inside the userdata template — which
contains the substring `KM_SLACK_BRIDGE_URL=`. The test was authored under the
Phase 63 Plan 08 invariant that bridge-url should ONLY be SSM-fetched at runtime
inside the sandbox, not baked into compile-time userdata. Phase 67's cloud-init
runtime fetch (writing to /etc/profile.d/km-slack-runtime.sh on first boot) is
arguably still "runtime" — the compile-time userdata is a SHELL SCRIPT that
performs the fetch — but the test's substring check doesn't distinguish.

**Why deferred:**
- 67-11 is scoped to the inbound-poller post path + Stop hook gate; the
  cloud-init runtime block is unrelated.
- The pre-existing failure is reproducible on the parent commit (5f6b571)
  before any 67-11 edits applied.

**Resolution options for follow-up:**
1. Update the test to assert the substring only inside the env-file heredoc
   (KM_NOTIFY_ENV_EOF block), not the cloud-init SSM-fetch heredoc (RUNTIMEEOF).
2. Update the test comment + name to reflect the new Phase 67 reality
   (bridge-url IS part of the rendered template, but only as part of a
   runtime SSM-fetch script that still consults SSM on first boot).
3. Drop the test entirely — Phase 63 Plan 08's "runtime injection only" model
   has been superseded by Phase 67's cloud-init-driven pattern.

**Recommendation:** Option 1 — narrow the assertion to KM_NOTIFY_ENV_EOF block.

**Impact on 67-11:** None. All four new 67-11 tests pass; the four pre-existing
SlackInbound tests still pass; `make build` succeeds.
