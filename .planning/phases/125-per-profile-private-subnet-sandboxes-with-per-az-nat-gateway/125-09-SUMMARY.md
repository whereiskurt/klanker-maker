---
phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway
plan: 09
subsystem: docs
tags: [documentation, testing, go-test, checkpoint]

# Dependency graph
requires:
  - phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway (plans 01-08)
    provides: "the fully code-complete Phase 125 implementation this plan documents and gates"
provides:
  - "docs/private-subnet-nat.md — the operator runbook: both toggles, per-AZ NAT rationale, cost, the one-time route-table split, reversal, guards, ordered deploy surface, REQ-125-SUBPIN scope boundary, ttl-handler residual-risk paragraph"
  - "CLAUDE.md Phase 125 block + Where-to-look row"
  - "125-UAT.md Part 1 — the whole-repo test suite and profile-validation gate results, both green (qualified by the 5 documented environmental failures)"
  - "125-UAT.md Part 2 — the eight-step live UAT scaffold, unfilled, ready for the operator/orchestrator"
affects: ["the operator running the live UAT (Task 3)", "any future phase reading docs/private-subnet-nat.md"]

# Tech tracking
tech-stack:
  added: []
  patterns: []

key-files:
  created:
    - docs/private-subnet-nat.md
    - .planning/phases/125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway/125-UAT.md
  modified:
    - CLAUDE.md
    - VERSION

key-decisions:
  - "This execution was intentionally scoped to Tasks 1 and 2 only (documentation + automated test gate). Task 3 — the eight-step live UAT — is a checkpoint:human-verify, autonomous:false task that applies real infrastructure and launches real instances in the target AWS account; per the assigning message's explicit scope limit, no AWS-mutating command was run in this session. The live UAT is being driven separately by the orchestrator with the operator."
  - "125-UAT.md was structured as two parts in one file (automated gate results + live UAT scaffold) rather than two separate files, matching the plan's single files_modified entry for 125-UAT.md and keeping the whole phase-gate record in one place."
  - "requirements-completed is left empty on this SUMMARY: REQ-125-UAT (the live UAT itself) is not yet satisfied, and REQ-125-DOCTOR was already completed by Plan 07, not this plan. Marking either complete here would be inaccurate."

requirements-completed: []

duration: ~45min (Tasks 1-2 only; Task 3 not attempted)
completed: 2026-08-20
status: checkpoint
---

# Phase 125 Plan 09: Documentation + test gate (Tasks 1-2 complete; Task 3 checkpoint reached) Summary

**Wrote the operator runbook (`docs/private-subnet-nat.md`) and the CLAUDE.md Phase 125 block, then ran and recorded the whole-repo Go suite plus the profile-validation gate — both green (qualified by five documented pre-existing environmental failures) — and reached the plan's `checkpoint:human-verify` live-UAT task, which this session did not attempt by explicit scope.**

## Performance

- **Duration:** ~45 min (Tasks 1-2 only)
- **Completed:** 2026-08-20
- **Tasks:** 2/3 completed (Task 3 is a blocking human-verify checkpoint, out of this session's scope)
- **Files modified:** 4 (2 created, 2 modified)

## Accomplishments

- **Task 1 — Operator doc + CLAUDE.md block.** `docs/private-subnet-nat.md` covers, in the
  order the plan specified: the two decoupled toggles and their dormancy; the per-AZ NAT
  topology and why (Phase 124's AZ sweep rotates across 4 AZs — a shared NAT would add
  cross-AZ transfer cost and a SPOF); the cost (~$132/mo baseline + $0.045/GB, with the
  300GB/$13.50 GPU-weights worked example); the one-time, **unconditional** private
  route-table split that fires on the first `km init` after this phase ships regardless of
  the NAT toggle — called out prominently so an operator reading "dormant by default" isn't
  surprised by a non-empty plan; the reversal procedure and the routeless-island property;
  the four guards (`km create` fail-fast, `km init` refuse-to-disable, two `km doctor` WARNs);
  the ordered deploy surface with the reason for the order; the two accepted properties
  (shared NAT EIP per AZ, EIP quota headroom); the scope fences (EFS, SSM, VPC endpoints,
  IPv6, cross-region, migration); REQ-125-SUBPIN as a tracked deliverable with its scope
  boundary (pin only, ECS substrate unproven); and the ttl-handler v1.0.0-pin residual-risk
  paragraph. CLAUDE.md gained a Phase 125 block in the exact house style of the recent phase
  blocks (Phase 124/122/etc.) plus a Where-to-look table row.
- **Task 2 — Whole-repo test + profile-validation gate.** `make build` (exit 0) →
  `go test ./... -count=1 -timeout 600s` (own exit status captured via `$?` after redirect,
  never through a pipe: `GOTEST_EXIT=1`) → `bash scripts/validate-all-profiles.sh`
  (`PROFILES_EXIT=0`, 21/21 profiles valid). The single `go test` FAIL is confirmed to be
  exactly the five pre-existing, documented environmental failures in `internal/app/cmd`
  (`TestBootstrapSCPApplyPath`, `TestBootstrapSCPSkipped_OrganizationBlank`, `TestClusterAdd`,
  `TestClusterRm`, `TestClusterAddPersistFailure`, all failing on the AWS-credential
  `fast-fail seam` stub) — 41 other packages report `ok`, and no other `FAIL` line appears
  anywhere in the captured output. Recorded verbatim, with the qualification stated plainly
  rather than rounded up to an unqualified green, in `125-UAT.md` Part 1.
- **Task 3 — checkpoint reached, not attempted.** `125-UAT.md` Part 2 scaffolds the deploy
  sequence and all eight live-UAT steps (baseline, additive plan, private create, egress
  path, ICE rotation, ttl-handler auto-destroy across the module-version boundary, clean
  teardown, negative fail-fast) plus the two `km doctor` spot-checks, each with an unfilled
  `_(pending)_` result placeholder for the operator/orchestrator to complete. No AWS-mutating
  command (`km init`, `km create`, `km destroy`, `terragrunt apply`, or equivalent) was run in
  this session, per the explicit scope limit in the assigning message.

## Task Commits

Each completed task was committed atomically:

1. **Task 1: Write the operator document and the CLAUDE.md phase block** — `d74220d8` (docs)
2. **Task 2: Whole-repo test and profile-validation gate** — `c6e982d8` (test)

Task 3 was not executed (blocking checkpoint, out of scope for this session — see below).

## Files Created/Modified

- `docs/private-subnet-nat.md` (new) — the full operator runbook for Phase 125
- `CLAUDE.md` — Phase 125 block (inserted ahead of the `km-github commit` block, matching
  recency-first ordering) + a new Where-to-look row pointing at the new doc
- `.planning/phases/125-.../125-UAT.md` (new) — Part 1 (automated gate results, filled) +
  Part 2 (live UAT scaffold, unfilled)
- `VERSION` — dev-build counter bump (`0.5.58` → `0.5.59`), the standard side effect of
  `make build`, committed alongside Task 2 per this repo's established convention

## Decisions Made

- Scoped this execution to Tasks 1-2 only. The assigning message was explicit: "Do NOT
  attempt [the live UAT]. Do NOT run `km init`, `km create`, `km destroy`,
  `terragrunt apply`, or any AWS-mutating command. The orchestrator is driving the live UAT
  separately with the operator." Task 3's own frontmatter (`type="checkpoint:human-verify"
  gate="blocking"`, plan-level `autonomous: false`) independently confirms this is the
  correct stopping point for an automated executor regardless of the scope note.
- Combined the automated-gate record and the live-UAT scaffold into one `125-UAT.md` file
  (two clearly-labeled parts) rather than a separate scratch file for the gate results, since
  the plan's `files_modified` names exactly one `125-UAT.md` path for both.
- Left `requirements-completed` empty rather than listing `REQ-125-UAT`/`REQ-125-DOCTOR` from
  the plan's frontmatter — `REQ-125-UAT` genuinely isn't satisfied until the live UAT actually
  runs, and `REQ-125-DOCTOR` was already completed by Plan 07's `km doctor` checks, not by
  this plan's documentation/test-gate work.

## Deviations from Plan

None — Tasks 1 and 2 were executed exactly as written, with all stated acceptance criteria
verified directly (see below). Task 3 was correctly not attempted, per both the assigning
message's explicit scope limit and the task's own `autonomous: false` /
`checkpoint:human-verify` classification.

## Verification

**Task 1 acceptance criteria — all verified:**
- `test -f docs/private-subnet-nat.md` → present.
- `grep -c 'private-subnet-nat.md' CLAUDE.md` → `3` (≥ 1 required).
- `grep -c 'Phase 125' CLAUDE.md` → `2` (≥ 1 required).
- `docs/private-subnet-nat.md` contains: `network.nat_gateway` (3), `privateSubnet` (4),
  `make build` (4), `make build-lambdas` (2), `km init --dry-run=false` (5), and an explicit
  `--sidecars` is NOT the deploy path statement (present at line 139-140).
- Doc uses only generic placeholders — no real tenant/customer/account identifiers.

**Task 2 acceptance criteria — all verified:**
- `GOTEST_EXIT=1` and `PROFILES_EXIT=0` — both were the commands' own exit codes, captured
  via redirect + `$?`, never through a pipe.
- `/tmp/km-125-fullsuite.txt` contains exactly one `FAIL` package
  (`github.com/whereiskurt/klanker-maker/internal/app/cmd`) and exactly the five documented
  `--- FAIL:` subtests, verbatim-matched to the assigning message's named list — no
  unexpected `FAIL` line anywhere else in the 32-other-package output.
- `125-UAT.md` records both commands, both exit codes, and the `all 21 profiles valid` line.

Note: the plan's own automated verify block expected `GOTEST_EXIT=0` (an unqualified green).
That expectation predates this session's explicit instruction (from the assigning message,
under `<critical_project_conventions>`) that the five named `internal/app/cmd` tests are
known, pre-existing environmental failures that must be recorded as such rather than treated
as a gate failure — this SUMMARY follows the assigning message's more specific, more current
guidance, and confirms by verbatim message-matching that no other regression is hiding behind
that qualification.

## Known Stubs

None.

## Threat Flags

None — this plan is pure documentation and test-gate execution; no code path, network
endpoint, auth path, or schema was touched.

## Self-Check: PASSED

Files verified present on disk:
- FOUND: docs/private-subnet-nat.md
- FOUND: .planning/phases/125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway/125-UAT.md
- FOUND: CLAUDE.md (modified)
- FOUND: VERSION (modified)

Commit hashes verified present in git log:
- FOUND: d74220d8 (Task 1)
- FOUND: c6e982d8 (Task 2)

## CHECKPOINT REACHED

**Type:** human-verify
**Plan:** 125-09
**Progress:** 2/3 tasks complete

### Completed Tasks

| Task | Name | Commit | Files |
|---|---|---|---|
| 1 | Write the operator document and the CLAUDE.md phase block | `d74220d8` | `docs/private-subnet-nat.md`, `CLAUDE.md` |
| 2 | Whole-repo test and profile-validation gate | `c6e982d8` | `.planning/phases/125-.../125-UAT.md`, `VERSION` |

### Current Task

**Task 3:** Live eight-step UAT against real AWS
**Status:** blocked (by design — `type="checkpoint:human-verify" gate="blocking"`,
`autonomous: false`)
**Blocked by:** requires real AWS infrastructure (a real terragrunt apply, a real EC2 launch,
live internet egress, a real capacity failure, a real Lambda-driven TTL expiry) — none of
which is reducible to an automated step, and this session's assigning message explicitly
prohibited running any AWS-mutating command.

### Checkpoint Details

The full eight-step UAT (baseline, additive plan, private create, egress path, ICE rotation,
ttl-handler auto-destroy, clean teardown, negative fail-fast), the exact deploy sequence
(`make build` → `make build-lambdas` → `km init --dry-run=false`, NOT `--sidecars`), and two
`km doctor` spot-checks are scaffolded in
`.planning/phases/125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway/125-UAT.md`
Part 2, each with an unfilled result placeholder. `docs/private-subnet-nat.md` is written and
ready to guide the operator through the deploy surface and each step's expected outcome.

### Awaiting

The orchestrator/operator to run the deploy sequence and the eight live UAT steps against
real AWS, append each step's real outcome to `125-UAT.md` Part 2 (PASS / FAIL / NOT EXERCISED
with reason — never fabricated), and reply "approved" (or name the failing step).

---
*Phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway*
*Completed: 2026-08-20 (Tasks 1-2; Task 3 checkpoint reached, not attempted)*
