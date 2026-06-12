# Phase 107: Reconcile 22 stale internal/app/cmd unit tests - Context

**Gathered:** 2026-06-11
**Status:** Ready for planning
**Source:** Roadmap (highly detailed) + research-driven escalation decisions

<domain>
## Phase Boundary

Reconcile 22 deterministically-failing `internal/app/cmd` unit tests so
`go test ./internal/app/cmd/ -count=1` reaches a clean, trustworthy green.
Production behavior is the source of truth; stale assertions are updated to
match what the code does today. Research classified the 22 as **19 STALE-TEST**
(pure test edits) + **3 ESCALATE** (real production gap — see decision below).

In scope: `internal/app/cmd/*_test.go` edits for the 19 stale tests, plus ONE
approved production-code fix for the 3 shell escalations (kept in its own plan).
Out of scope: any other production change; touching green tests; new features.
</domain>

<decisions>
## Implementation Decisions

### Shell pre-flight escalation (TEST-3/4/5) — LOCKED: Fix production code
- `NewShellCmdWithFetcher` (`shell.go`) `RunE` returns `nil` unconditionally in
  the non-learn path, silently swallowing pre-flight errors. Result:
  `km shell <stopped|unknown-substrate|missing-instance>` exits 0 with no output
  — a real operator-facing usability regression (intentional `return nil` from
  commit `aaf76a4f`, meant only to suppress SSM session-exit noise).
- **Decision:** Apply the small production fix — return `runErr` when it is a
  pre-flight error (stopped / unsupported substrate / missing instance ARN);
  continue to swallow only session-exit errors from the actual `execFn` call.
  Follow the `newShellCmdWithSSM` (`shell.go:211`) pattern.
- **This is a deliberate, user-approved flagged escalation**, NOT a silent test
  rewrite. Isolate it in its own PLAN + its own commit so the test-hygiene work
  stays cleanly separable from the production change.
- After the fix, TEST-3/4/5 should pass against the *real* (now-correct)
  behavior — assert the pre-flight error is returned, do not weaken them.

### TEST-21 `TestLoadEFSOutputs_NotExist` — LOCKED: Relax to err==nil only
- `LoadEFSOutputs` now falls back to `fetchAndCacheOutputs` (S3) when the local
  `efs/outputs.json` is missing, so it legitimately returns a real `fs-...` ID.
- **Decision:** Drop the empty-string assertion; assert only `err == nil`
  (return value unconstrained — empty OR a valid `fs-...` id). Matches the new
  S3-fallback contract. Do NOT add environment-isolation complexity.

### All other 18 stale tests — update assertion to current behavior
- Per the research per-test triage table (107-RESEARCH.md): email SSM key paths
  gained the `km/` resource prefix; uninit `wantOrder` must reflect
  `regionalModules()` == 22; state-bucket guards only fire on
  `ResourceNotFoundException` now that DynamoDB is primary; docker shell uses
  `bash --login` + always `-u sandbox`; plus the individual misc fixes.

### Guardrails (from phase goal)
- No production code change ANYWHERE except the one approved shell fix.
- Read `go test`'s OWN exit code, never a piped exit ([[feedback_check_go_test_exit_not_pipe]]).
- Use `-timeout 600s` for the full-package run (many tests hit real AWS).
- Green tests must stay green: `TestScoped*` (10), `TestRunInitPlan_ModuleOrder` (count=22).

### Claude's Discretion
- Plan/wave decomposition (research says all 14 files are independent → fan out).
- Exact assertion wording per stale test (follow the research fix prescriptions).
</decisions>

<specifics>
## Specific Ideas

- 14 independent `*_test.go` files; no shared-fixture edits (`fakeFetcher` is read
  only by the shell tests and is NOT modified).
- The 3 shell escalations share one root cause and one production fix.
</specifics>

<deferred>
## Deferred Ideas

None — phase scope is fully covered. (No new tests beyond reconciling the 22;
that's `/gsd:add-tests` territory if desired later.)
</deferred>

---

*Phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests*
*Context gathered: 2026-06-11 (roadmap + escalation decisions)*
