# Phase 26: Live Operations Hardening - Context

**Gathered:** 2026-03-27
**Status:** Ready for planning

<domain>
## Phase Boundary

Harden the platform after extensive ad-hoc live testing (~60 commits, 2026-03-24 through 2026-03-26). Core paths work end-to-end (bootstrap, init, create, destroy, TTL auto-destroy, idle detection, sidecars). This phase focuses on: fixing remaining gaps, backfilling critical-path test coverage, CLI UX refinements, and destroy reliability verification. NOT adding new capabilities — hardening what exists.

**Pre-existing ad-hoc work:** Phase 26 already has a SUMMARY.md documenting all changes from the live testing sprint. Planning should build on top of that work, not redo it.

</domain>

<decisions>
## Implementation Decisions

### Remaining Gaps
- Multi-region: code audit only — grep for us-east-1 hardcoding and region assumptions, fix in code. Do NOT run live tests in a second region (defer full multi-region testing).
- Edge cases: fix what's feasible — prioritize highest-impact gaps and defer the rest. No specific known bugs from the sprint — focus on paths that weren't exercised.
- Failed create leaves partial infra — user runs km destroy manually. No auto-rollback.
- km list should show failed/partial sandboxes with distinct status indicator.

### Test Coverage
- Critical paths only — happy path for create/destroy/list + specific bugs that were fixed.
- Cover all four areas: compiler output, CLI commands, Lambda handlers, plus Claude's discretion on highest-value tests.
- Fix the 2 pre-existing test failures (init module ordering, status timestamp format).
- Lambda handler test infrastructure: Claude decides (mocked SDK vs localstack based on existing codebase patterns).

### CLI UX Refinement
- km logs: both audit and boot streams via --stream flag (already has --stream "audit" default). Add --follow for live tail (already implemented). Accept both sandbox number (#1) and sandbox ID.
- --remote flag (destroy/extend/stop via EventBridge+Lambda): include testing and fixing in this phase — wired but untested.
- Add shell completion (bash/zsh) — Cobra has built-in support.
- Add aliases: km ls (list), km sh (shell), plus Claude picks others based on frequency.
- All new commands (extend, stop, shell, logs) need proper --help text with examples.
- Consistent output styling for newer commands (extend, stop, logs) to match established patterns.
- More color in output — section headers, sandbox IDs, profile names for scannability.
- km-config.yaml is sufficient for defaults — no separate CLI defaults file needed.
- Progress dots + elapsed time for km create is sufficient — no step indicators needed.

### Destroy Reliability
- TTL auto-destroy: verified end-to-end (create → idle timeout → Lambda destroy → clean state). Stable at 1536MB Lambda memory.
- km destroy (manual): reliably cleans everything up.
- Idle detection keeps sandbox alive past TTL — heartbeat prevents premature destruction.
- Hard cap on max lifetime exists — Claude should verify and test the enforcement code.
- State drift handling (terraform state out of sync): Claude should verify Lambda behavior.
- No alerting for failed destroys — defer alerting/monitoring to a future phase.

### Claude's Discretion
- Exact test selection — Claude identifies highest-value test gaps based on code complexity and risk
- Lambda handler test approach (mocked SDK vs localstack)
- Max lifetime enforcement — verify the code and add tests if needed
- Terraform state drift handling in Lambda destroy — verify and document behavior
- Additional CLI aliases beyond km ls and km sh
- Color scheme details (what gets colored, which colors)

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `internal/app/cmd/logs.go`: km logs already implemented with --follow and --stream flags, ResolveSandboxID supports #number references
- `internal/app/cmd/extend.go`, `stop.go`, `shell.go`: new commands from sprint — need help text and test coverage
- `pkg/aws/logs.go`: TailLogs function for CloudWatch streaming
- Cobra command pattern: all commands follow NewXxxCmd(cfg) → RunE pattern with consistent flag handling

### Established Patterns
- Test pattern in internal/app/cmd: interface-based mocks for AWS clients (BudgetAPI, EC2StartAPI, etc.)
- Compiler tests: loadTestProfile() + testNetwork() + strings.Contains() assertions
- Lambda handlers: standalone Go binaries in infra/lambdas/ — currently no test files

### Integration Points
- ResolveSandboxID: central resolver for #number → sandbox-id, used across list/status/destroy/extend/stop/shell/logs
- km-config.yaml: single source of truth for region, accounts, domain, artifacts bucket
- S3 metadata.json: sandbox lifecycle state — created by km create, deleted by destroy Lambda

</code_context>

<specifics>
## Specific Ideas

- Sidecar boot is clean and reliable on fresh create — no babysitting needed
- EventBridge idle→TTL chain is the most confident part of the system
- km destroy handles orphaned resources from failed creates
- The sprint established output patterns (section headers with ── separators, "done" indicators) — newer commands should match

</specifics>

<deferred>
## Deferred Ideas

- Full multi-region live testing (init + create + destroy in us-west-2) — separate phase
- Destroy failure alerting (SNS + CloudWatch alarms) — monitoring/alerting phase
- ECS substrate testing — separate phase
- Auto-rollback on failed create — too complex for hardening, separate phase

</deferred>

---

*Phase: 26-live-operations-hardening-bootstrap-init-create-destroy-ttl-auto-destroy-idle-detection-sidecar-fixes-proxy-enforcement-cli-polish*
*Context gathered: 2026-03-27*
