---
phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions
plan: 11
subsystem: quota
tags: [gap-closure, alr-01, breached_at, ddb-stream, alerter, tdd]
gap_closure: true
dependency_graph:
  requires: []
  provides: [breached_at-write, on_breach-write, alr-01-production-path]
  affects: [cmd/km-quota-alerter, sidecars/http-proxy, pkg/slack/bridge, pkg/h1/bridge]
tech_stack:
  added: []
  patterns: [if_not_exists-idempotency, two-phase-ddb-write, fail-soft-error-propagation]
key_files:
  modified:
    - pkg/quota/quota.go
    - pkg/quota/quota_test.go
decisions:
  - "Second UpdateItem pattern (not fold-into-ADD): DynamoDB cannot reference the post-increment count inside the same expression that produces it via ADD, so a count-conditioned SET requires a separate write"
  - "if_not_exists on both breached_at and on_breach: first-breach-only idempotency; concurrent writers collapse to one set"
  - "ExpressionAttributeNames placeholders (#ba, #ob): avoids any reserved-word risk for breached_at/on_breach"
  - "Fail-soft: breach-write errors are returned but do not suppress the already-committed increment count"
  - "findBreachWrite checks ExpressionAttributeNames VALUES (not UpdateExpression string) because implementation uses #ba/#ob placeholders"
metrics:
  duration: 158s
  completed_date: "2026-06-27"
  tasks_completed: 2
  files_modified: 2
---

# Phase 121 Plan 11: breached_at + on_breach Write on First Breach (GAP 1 Closure) Summary

**One-liner:** Adds a second conditional `UpdateItem` after each window breach so DDB Stream carries `breached_at`/`on_breach` in `NewImage`, restoring the `km-quota-alerter` ALR-01 production path that was dead because `atomicIncrement` never wrote those attrs.

## What Was Built

### Gap Closed

GAP 1 from `121-VERIFICATION.md`: `pkg/quota.atomicIncrement` only issued `ADD #c :one` (plus optional TTL SET) and never wrote `breached_at` or `on_breach`. Every Stream record the alerter received lacked `breached_at`, so `handleRecord` returned `nil` at its first-breach guard (`if _, hasBreachedAt := newImg["breached_at"]; !hasBreachedAt { return nil }`) — no SES email or Slack control-channel notice ever fired in production.

### Implementation

**`pkg/quota/quota.go`:**

- Added `setBreached(ctx, client, tableName, sandboxID, action, w, now, onBreach) error` helper that issues a second `UpdateItem` on the same row key with:
  ```
  SET #ba = if_not_exists(#ba, :now), #ob = if_not_exists(#ob, :policy)
  ```
  where `#ba → "breached_at"`, `#ob → "on_breach"`, `:now = N(now.Unix())`, `:policy = S(string(onBreach))`.
- `setBreached` is called from `Record`'s per-window loop immediately after `atomicIncrement` returns `count > w.limit`.
- `if_not_exists` on both attrs provides first-breach-only idempotency — concurrent writers under a hard loop all succeed (DDB applies one; subsequent calls are no-ops that return the existing value).
- Fail-soft: a `setBreached` error is returned to the caller without suppressing the already-committed increment count. The breach write happening after the ADD means the ADD result is always preserved.
- Non-breaching increments (`count <= w.limit`) call no `setBreached` — byte-identical to before.

**`pkg/quota/quota_test.go`:**

Three new tests drive the REAL `Record` (not a faked Stream event) against `fakeQuotaClient`:

1. `TestRecord_WritesBreachedAt_OnTrip`: `countReturn=2 > limit=1` → asserts a breach-write `UpdateItem` exists with `ExpressionAttributeNames` mapping to `"breached_at"` and `"on_breach"`, `:now` as a Number, `:policy="warn"`, `if_not_exists` in UpdateExpression, and correct row key (`sb-breach#github_comment`, `hour#…`).
2. `TestRecord_NoBreachWrite_WhenUnderLimit`: `countReturn=1 <= limit=5` → asserts NO `UpdateItem` has `breached_at` or `on_breach` in its `ExpressionAttributeNames`.
3. `TestRecord_OnBreachPolicyPropagates`: `BreachFreeze`, `countReturn=2 > limit=1` → asserts `:policy="freeze"`.

Helper `findBreachWrite` searches `ExpressionAttributeNames` map values (not the `UpdateExpression` string, which only contains `#ba`/`#ob` placeholders) — a key design decision discovered during the RED→GREEN transition.

## Test Results

```
ok  github.com/whereiskurt/klanker-maker/pkg/quota          (all 8 tests GREEN)
ok  github.com/whereiskurt/klanker-maker/cmd/km-quota-alerter (unchanged, GREEN)
```

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] findBreachWrite must check ExpressionAttributeNames values, not UpdateExpression string**

- **Found during:** Task 2 GREEN phase
- **Issue:** The plan spec said `findBreachWrite` should check `strings.Contains(expr, "breached_at")` in the UpdateExpression. But the implementation uses `ExpressionAttributeNames` placeholders (`#ba → "breached_at"`, `#ob → "on_breach"`), so the expression only contains `#ba`/`#ob` — never the literal attr names. Tests stayed RED after GREEN implementation.
- **Fix:** Rewrote `findBreachWrite` to iterate `c.ExpressionAttributeNames` values and check for `"breached_at"` and `"on_breach"` as map values.
- **Files modified:** `pkg/quota/quota_test.go` (`findBreachWrite` helper)
- **Commit:** `c4d55eb6` (included in Task 2 GREEN commit)

## Self-Check: PASSED

| Check | Result |
|-------|--------|
| `pkg/quota/quota.go` exists | FOUND |
| `pkg/quota/quota_test.go` exists | FOUND |
| Commit `2c6a5faa` (RED test) exists | FOUND |
| Commit `c4d55eb6` (GREEN impl) exists | FOUND |
| `setBreached` in `quota.go` | FOUND |
| `TestRecord_WritesBreachedAt_OnTrip` in `quota_test.go` | FOUND |
| `go test ./pkg/quota/... -count=1` GREEN | PASSED |
| `go test ./cmd/km-quota-alerter/... -count=1` GREEN | PASSED |
