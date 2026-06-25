---
phase: 119-slack-inbound-per-thread-parallelism
plan: 05
subsystem: infra
tags: [slack, sqs, fifo, concurrency, poller, bridge, profile]

# Dependency graph
requires:
  - phase: 119-02
    provides: bridge threadTS FIFO grouping + queue VisibilityTimeout 1800s
  - phase: 119-03
    provides: km validate WARN (cap>1 without perSandbox/inbound) + KM_SLACK_MAX_CONCURRENCY env emission
  - phase: 119-04
    provides: bounded-concurrent ack-after poller (semaphore, heartbeat, last_processed_event_ts dedup)
provides:
  - learn.v2.parallel.yaml demo profile (maxConcurrentThreads:3)
  - live synthetic-HMAC E2E proving all six runtime assertions
  - docs/slack-notifications.md § Phase 119 operator section
  - CLAUDE.md + klanker:slack SKILL Phase 119 surfaces
affects: [slack-inbound, future per-thread worktree isolation]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Live synthetic-HMAC /events E2E to exercise bash poller logic invisible to Go goldens"
    - "Side-effect verification (RUN_DIRs / SQS in-flight / DDB / bridge logs) over UI reactions"

key-files:
  created:
    - profiles/learn.v2.parallel.yaml
    - .planning/phases/119-slack-inbound-per-thread-parallelism/119-UAT.md
    - .planning/phases/119-slack-inbound-per-thread-parallelism/km119_send_event.sh
  modified:
    - docs/slack-notifications.md
    - skills/slack/SKILL.md
    - CLAUDE.md
    - .claude-plugin/plugin.json
    - .claude-plugin/marketplace.json
    - internal/app/cmd/create_slack_inbound_test.go
    - internal/app/cmd/create_github_inbound_test.go

key-decisions:
  - "Verified the bash concurrency via live deploy + synthetic-HMAC E2E (Research Pitfall 6) — the only gate that catches poller-script bugs"
  - "Used the auth-gated fast claude exit (Not logged in, ~300ms) to cleanly observe 3 overlapping RUN_DIRs — agent auth is not required to prove dispatch parallelism"
  - "Bumped klanker plugin 0.4.11 -> 0.4.12 because SKILL.md gained user-facing content"

patterns-established:
  - "Emit-only-when->1: a profile knob whose env var is omitted at the default value keeps dormancy byte-identical"

requirements-completed: [P119-B, P119-C, P119-D, P119-E, P119-F]

# Metrics
duration: ~60min
completed: 2026-06-25
---

# Phase 119 Plan 05: Demo profile + live E2E + docs Summary

**Live synthetic-HMAC E2E proved all six per-thread-parallelism assertions against a deployed cap=3 sandbox (3 overlapping RUN_DIRs from one poller PID, serial-within-thread, cap enforcement, 1800s heartbeat, dormancy regression, event_id dedup), plus the cap=3 demo profile and three doc surfaces.**

## Performance

- **Duration:** ~60 min (incl. two cold-create retries on a transient TF-registry timeout + a real ~8 min terragrunt apply)
- **Tasks:** 3 (Task 1 pre-committed by prior agent; Tasks 2–3 this session)
- **Files modified:** 9 (3 created, 6 modified)

## Accomplishments
- Deployed Phase 119 end-to-end: `make build-lambdas` + `km init --dry-run=false` (lambda-slack-bridge + create-handler carry the threadTS grouping + new poller userdata).
- Drove the NON-OPTIONAL live E2E: six assertions PASS with evidence captured in 119-UAT.md. Parallelism proven by 3 distinct-thread events minting 3 RUN_DIRs in a 4-second window from one poller parent PID (34452) with distinct `-$$-$RANDOM` suffixes.
- Documented Phase 119 across docs/slack-notifications.md (§ Phase 119), klanker:slack SKILL, and CLAUDE.md (summary block + Where-to-look row); bumped the plugin version.

## Task Commits

1. **Task 1: Demo profile + build/deploy** - `f18944df` (feat) — committed by the prior agent (profile created + validated clean).
2. **Task 2: Live synthetic-HMAC E2E (six assertions) + stale-test fix** - `f9ce28e3` (test)
3. **Task 3: Docs (slack-notifications, SKILL, CLAUDE) + plugin bump** - `18fc31f8` (docs)

**Plan metadata:** (this SUMMARY + STATE/ROADMAP) committed separately.

## Files Created/Modified
- `profiles/learn.v2.parallel.yaml` - cap=3 demo profile (Task 1).
- `.planning/phases/119-*/119-UAT.md` - six-assertion live E2E evidence.
- `.planning/phases/119-*/km119_send_event.sh` - Phase-114-style signed /events helper.
- `docs/slack-notifications.md` - § Phase 119 operator section.
- `skills/slack/SKILL.md` - per-thread parallelism note.
- `CLAUDE.md` - Phase 119 summary block + Where-to-look row.
- `.claude-plugin/{plugin,marketplace}.json` - version 0.4.11 → 0.4.12.
- `internal/app/cmd/create_{slack,github}_inbound_test.go` - VisibilityTimeout 30→1800 (Phase 119 base raise).

## Decisions Made
- The live E2E is the real gate (the concurrency logic is bash, invisible to Go goldens). Drove it via signed synthetic `/events` POSTs.
- Verified via side effects (RUN_DIRs, SQS in-flight, DDB, bridge logs), NOT the 👀 reaction (fabricated ts → message_not_found is harmless).
- The dispatched `claude -p` returns "Not logged in" (no operator OAuth on the box) — this does NOT invalidate the dispatch/semaphore/RUN_DIR/heartbeat/dedup assertions, and the fast exit made the parallel window easy to observe.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Stale `VisibilityTimeout=30` test assertions**
- **Found during:** Task 2 (full-suite gate)
- **Issue:** `create_slack_inbound_test.go:198` and `create_github_inbound_test.go:98` still asserted `"30"`; Phase 119-02 raised the production `inboundQueueAttrs` base to `"1800"` (`pkg/aws/sqs.go:135`) but missed these two create-handler tests.
- **Fix:** Updated both assertions to `"1800"` with a Phase-119 comment.
- **Files modified:** internal/app/cmd/create_slack_inbound_test.go, internal/app/cmd/create_github_inbound_test.go
- **Verification:** `go test ./internal/app/cmd/ -run TestCreate_{Slack,GitHub}InboundQueueProvisioned` → exit 0.
- **Committed in:** f9ce28e3 (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 bug — stale test assertion from the Phase-119 queue base raise).
**Impact on plan:** Necessary to keep the full suite green; no scope creep.

## Issues Encountered
- **Transient Terraform-registry timeout during cold-create** (twice): the create-handler Lambda failed to download `hashicorp/aws v6.46.0` from `registry.terraform.io` (6 retries, `Client.Timeout`). Environment/network artifact (provider not pre-cached in the Lambda toolchain tarball), NOT a Phase-119 defect — a retry succeeded. Flagged in 119-UAT.md as a separate infra-hardening candidate (pre-bundle the provider plugin cache).
- **Stale orphaned `km-sandboxes` row** (`parallel-769afe41`, failed first create with `teardownPolicy: stop`) shared the reused `slack_channel_id`, so the bridge's first `FetchByChannel` routed event A1 to the dead sandbox. Deleted the orphan row; routing then resolved correctly. Operational artifact of `stop` policy + `archiveOnDestroy:false` channel reuse, not a Phase-119 bug.
- **Auth gate (informational):** `km agent auth --claude` is an operator browser-OAuth flow; the box ran turns as "Not logged in". Worked around by verifying dispatch via side effects (the concurrency assertions do not require a real agent answer).
- **Cosmetic poller noise:** `[: : integer expression expected` at poller line 122 on empty-batch long-poll (pre-existing; harmless; candidate `COUNT=${COUNT:-0}` follow-up).
- **Pre-existing full-suite failure (out of scope):** `TestDestroyCmd_InvalidSandboxID` (`internal/app/cmd/destroy_test.go:186`) fails on the `go test ./...` gate. **Confirmed pre-existing** — it FAILs identically on commit `8b288a2a` (Phase 119-04 completion, before any 119-05 work). It is in the `km destroy` sandbox-ID validation, **unrelated** to Phase 119 (Slack inbound). Logged to `deferred-items.md` per the SCOPE BOUNDARY rule. With that one test skipped, `internal/app/cmd` is green (`ok 450.886s`) and every other package passes — so the suite is green modulo this documented pre-existing failure.
- **Concurrent executor (informational):** a second executor instance (Sonnet) ran plan 119-05 in parallel and committed `8dad401c` (alternate live-E2E evidence, same six assertions PASS) + `0a466705` (SUMMARY/STATE/ROADMAP). Histories are interleaved and coherent; all my commits (`f9ce28e3`, `18fc31f8`, `bd2c7a74`) are present, and the on-disk files (test fixes, plugin 0.4.12, docs) are correct.

## Full-suite gate

`go test ./... -count=1 -timeout 600s` → the ONLY failure is the pre-existing
`TestDestroyCmd_InvalidSandboxID` (above). Re-ran `internal/app/cmd` with that test skipped:
`ok 450.886s` (EXIT 0). All other packages green. The two Phase-119 stale-assertion test fixes
(`create_{slack,github}_inbound_test.go`) pass.

## User Setup Required
None - no external service configuration required (operator browser-OAuth for an agent answer is optional; not needed for the parallelism feature itself).

## Next Phase Readiness
- Phase 119 complete and live-verified. Per-thread worktree isolation (for safe concurrent repo-mutating turns) is the documented deferred follow-up.
- Existing sandboxes need `km destroy && km create` to gain the new poller; new inbound queues get the 1800s base, existing ones are covered by the heartbeat.

---
*Phase: 119-slack-inbound-per-thread-parallelism*
*Completed: 2026-06-25*

## Self-Check: PASSED

- Created files verified present: profiles/learn.v2.parallel.yaml, 119-UAT.md, km119_send_event.sh, 119-05-SUMMARY.md
- Commits verified in git log: f18944df (Task 1), f9ce28e3 (Task 2), 18fc31f8 (Task 3)
