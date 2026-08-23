---
phase: 126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin
plan: 10
subsystem: docs
tags: [docs, testing, terraform-validate, checkpoint]

requires:
  - phase: 126-01
    provides: "launch_accounts config block, RuntimeSpec.LaunchAccount schema field, km validate gates"
  - phase: 126-02
    provides: "cross-account provider override mechanism"
  - phase: 126-03
    provides: "AssumeRoleConfig, SandboxMetadata.LaunchAccount, namespaced capacity store"
  - phase: 126-04
    provides: "gpu-launcher-account/v1.0.0 Terraform module"
  - phase: 126-05
    provides: "km account add"
  - phase: 126-06
    provides: "create-flow cross-account wiring, VPC id fix"
  - phase: 126-07
    provides: "km account register/list/rm"
  - phase: 126-08
    provides: "km destroy + ttl-handler cross-account teardown"
  - phase: 126-09
    provides: "km doctor cross-account link checks"
provides:
  - "docs/cross-account-capacity-borrowing.md — the complete operator runbook"
  - "CLAUDE.md Phase 126 block, Where-to-look row, four km account CLI entries"
  - "skills/user/SKILL.md cross-reference; plugin version bumped 0.4.12 -> 0.4.13"
  - "126-UAT.md — green pre-flight gate record + the live-verification checklist (pending human execution)"
affects: []

tech-stack:
  added: []
  patterns:
    - "Operator-runbook doc structure mirrored from docs/private-subnet-nat.md: scope, toggles/commands, security model, cost, reversal, deploy surface, in that order"

key-files:
  created:
    - docs/cross-account-capacity-borrowing.md
    - .planning/phases/126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin/126-UAT.md
  modified:
    - CLAUDE.md
    - skills/user/SKILL.md
    - .claude-plugin/plugin.json
    - .claude-plugin/marketplace.json

key-decisions:
  - "skills/user/SKILL.md gained one cross-reference line (not a full command-tour section) pointing at the new runbook, matching the existing one-line klanker:cluster cross-reference precedent for a similarly-shaped cross-account feature — not the fuller Getting-Started-style treatment the skill gives km create/agent run."
  - "ROADMAP.md's Phase 126 entry (plan count + all ten one-line objectives) was already fully backfilled before this plan ran (last touched by 126-09's own commit ffbd8551) — Task 1's ROADMAP acceptance criterion was already satisfied; the remaining plan-count/checkbox bump for 126-10 itself is applied via the standard state_updates step below, not hand-edited."
  - "126-UAT.md's Live verification section is written entirely as a pending checklist with blank Actual columns — no step is marked passed, no simulated verdict is fabricated, matching the team-lead's explicit instruction not to invent UAT outcomes. This mirrors the same gap 126-06/126-07/126-08/126-09 each independently flagged: no launch_accounts link exists anywhere in this project's history, so nothing about the live cross-account path has ever run against real AWS."

requirements-completed: []

coverage:
  - id: D1
    description: "docs/cross-account-capacity-borrowing.md exists and its deploy section names the build/apply commands explicitly, stating which kind of change needs which"
    requirement: "REQ-126-UAT"
    verification:
      - kind: other
        ref: "test -f docs/cross-account-capacity-borrowing.md"
        status: pass
      - kind: manual_procedural
        ref: "§ Deploy surface table: three rows (operator-binary-only / remote-create Lambda+init / target-account terraform via km account add), each naming its exact command sequence and why"
        status: pass
    human_judgment: false
  - id: D2
    description: "CLAUDE.md contains a Phase 126 block and a Where-to-look row referencing the new doc; the CLI list gains the four km account entries"
    requirement: "REQ-126-ENROLL"
    verification:
      - kind: other
        ref: "grep -q 'cross-account-capacity-borrowing.md' CLAUDE.md"
        status: pass
    human_judgment: false
  - id: D3
    description: "The ROADMAP Phase 126 entry lists ten plans with objectives and no longer says the phase is unplanned"
    requirement: "REQ-126-DOCTOR"
    verification:
      - kind: other
        ref: "manual inspection of .planning/ROADMAP.md lines 291-420 — all ten 126-NN-PLAN.md entries present with one-line objectives (pre-existing as of commit ffbd8551, confirmed unchanged by this plan)"
        status: pass
    human_judgment: false
  - id: D4
    description: "The seven pre-flight gate commands are recorded in 126-UAT.md with individually-captured exit codes, including the three excluded-package invocations, and are green"
    requirement: "REQ-126-UAT"
    verification:
      - kind: other
        ref: "126-UAT.md § Pre-flight gate table; each exit code captured via echo \"EXIT=$?\" immediately after the command, never through a pipe"
        status: pass
    human_judgment: false
  - id: D5
    description: "The live cross-account verification (8-row simulate-principal-policy matrix + 18-step procedure) is checkpointed for human execution, not fabricated"
    requirement: "REQ-126-UAT"
    human_judgment: true
    rationale: "Requires real target-account admin credentials, real home-account credentials, and live GPU vCPU quota headroom in the target account -- none of which exist in this execution environment. This is the designated checkpoint task (Task 3, type checkpoint:human-verify, gate=blocking) and is reported here as reached, not completed."
    verification: []

duration: ~50min
completed: 2026-08-23
status: complete
---

# Phase 126 Plan 10: Operator runbook, project documentation, full-suite gate, and the live UAT checkpoint Summary

**Closes the phase's documentation and automated-verification surface — a complete operator runbook (`docs/cross-account-capacity-borrowing.md`), CLAUDE.md/skill/plugin updates, and a green seven-command pre-flight gate recorded in `126-UAT.md` — then reaches the human-gated live cross-account checkpoint (real two-account AWS credentials + GPU quota) without fabricating any result.**

## Performance

- **Duration:** ~50 min
- **Tasks:** 2 of 3 completed autonomously; Task 3 reached and reported as a checkpoint (by design — `autonomous: false`)
- **Files modified:** 5 modified, 3 created (2 docs + 1 SUMMARY)

## Accomplishments

- **`docs/cross-account-capacity-borrowing.md`** — the complete operator runbook, covering:
  scope (what the feature buys and explicitly does not — no second control plane, no
  multi-region, no home-account metering/messaging on a linked box); the two-credential
  enrollment sequence end to end with real commands (`km account add` against the target
  account, `km account register` against home); what the link record contains and where the
  external id actually lives (SSM SecureString, never the plaintext in `km-config.yaml`); the
  profile field and the `--launch-account` create-time override, including the
  explicit-empty-forces-home semantics; how region/subnets/VPC/security-group all come from
  the link (plus the known SecurityGroupID-unused residual gap carried over from 126-06); the
  capacity/quota gate reading the linked account with a fail-closed second assumed-role
  config; both teardown paths (operator `km destroy --remote --yes` and unattended TTL
  expiry); the four `km doctor` checks and what each finding means; the security model stated
  plainly (SCP-exemption, the launcher policy as the entire boundary, ExternalId as the
  confused-deputy mitigation, the read-only-both-directions data flow) plus the operator-
  requested management-account caveat; the cost note (an unreaped box bills silently in an
  account `km list` never enumerates); reversal via `km account rm`; and a three-row deploy-
  surface table distinguishing operator-binary-only changes from remote-create Lambda+init
  changes from target-account terraform provisioned by `km account add` alone.
- **CLAUDE.md** gained a Phase 126 block in the established newest-first style (inserted
  ahead of the Phase-125-era egress-deny-lists block, since this phase is the most recent), a
  "Where to look" table row, and four new `km account add/register/list/rm` CLI list entries.
- **`skills/user/SKILL.md`** gained one cross-reference line pointing at the new runbook,
  matching the existing `klanker:cluster` one-line cross-reference precedent. **Plugin version
  bumped `0.4.12` → `0.4.13`** in both `.claude-plugin/plugin.json` and
  `.claude-plugin/marketplace.json`, since the skill's content changed.
- **`126-UAT.md`** created with two sections: a **Pre-flight gate** (all seven commands run,
  each exit code captured directly and individually — never through a pipe — and recorded
  verbatim, verdict GREEN), and a **Live verification** checklist (prerequisites, the 8-row
  `simulate-principal-policy` containment matrix, and an 18-step ordered live procedure) laid
  out entirely as a pending checklist for the human-gated Task 3 checkpoint — no step is
  marked passed and no verdict is fabricated.
- ROADMAP's Phase 126 entry (plan count, all ten one-line plan objectives) was found already
  fully backfilled by the time this plan ran (last touched in commit `ffbd8551`, part of
  126-09's close) — this plan's own Task 1 acceptance criterion for the ROADMAP was therefore
  already satisfied without further edits; the standard end-of-plan `state_updates` step below
  applies the final 10/10 count and checkbox.

## Task Commits

1. **Task 1: operator runbook and project documentation** — `280b4ec4` (docs)
2. **Task 2: full-suite gate across every package this phase touched** — `12f574ce` (test)
3. **Task 3: live cross-account verification** — **CHECKPOINT REACHED, not executed.** See
   below.

_No TDD RED/GREEN split — this is a documentation and verification-gate plan; every acceptance
criterion for Tasks 1 and 2 was checked directly (`grep`/`test -f`/exit-code capture) before
each commit._

## Files Created/Modified

- `docs/cross-account-capacity-borrowing.md` — new: the full operator runbook.
- `CLAUDE.md` — Phase 126 block, Where-to-look row, four CLI list entries.
- `skills/user/SKILL.md` — one cross-reference line to the new runbook.
- `.claude-plugin/plugin.json` / `.claude-plugin/marketplace.json` — version `0.4.12` →
  `0.4.13`.
- `.planning/phases/126-.../126-UAT.md` — new: pre-flight gate record + live-verification
  checklist.

## Pre-flight gate — result

| # | Command | Exit code |
|---|---|---|
| 1 | `make test` | 0 |
| 2 | `go test ./internal/app/cmd/... -timeout 600s -count=1` | 1 (the 5 pre-declared known-preexisting failures only — `TestBootstrapSCPApplyPath`, `TestBootstrapSCPSkipped_OrganizationBlank`, `TestClusterAdd`, `TestClusterRm`, `TestClusterAddPersistFailure` — nothing else) |
| 3 | `go test ./cmd/ttl-handler/... -timeout 600s -count=1` | 0 |
| 4 | `go test ./pkg/compiler/... -timeout 600s -count=1` | 0 |
| 5 | `bash scripts/validate-all-profiles.sh` | 0 |
| 6 | `terraform init -backend=false && terraform validate && terraform fmt -check -recursive .` for both `infra/modules/gpu-launcher-account/v1.0.0` and `infra/modules/ttl-handler/v1.0.0` | 0 (all six sub-invocations; ttl-handler's `validate` carries one pre-existing-pattern deprecation warning on `aws_region.current.name`, not new to this phase, not a failure) |
| 7 | `go build ./...` | 0 |

Full detail, including the verbatim FAIL list confirming exactly the 5 pre-declared
subtests and nothing else, is in `126-UAT.md` § Pre-flight gate.

## Decisions Made

See `key-decisions` in the frontmatter above — duplicated here for scanning convenience:

1. `skills/user/SKILL.md` gained a one-line cross-reference (not a fuller command-tour
   section), matching the existing `klanker:cluster` precedent for a comparably-shaped
   cross-account feature.
2. ROADMAP's Phase 126 entry was already fully backfilled before this plan started
   (commit `ffbd8551`) — no further hand-edit was made beyond what `state_updates` applies at
   close.
3. `126-UAT.md`'s Live verification section is a pending checklist with every "Actual" column
   left blank — no fabricated outcomes, per the explicit team-lead instruction.

## Deviations from Plan

None — Tasks 1 and 2 executed exactly as specified. Task 3 is reached as designed
(`type="checkpoint:human-verify" gate="blocking"`, `autonomous: false` at the plan level) and
is reported below as a checkpoint, not completed — this is the plan's own specified behavior,
not a deviation.

## Known Stubs

None in the shipped documentation or gate record. The Live verification section of
`126-UAT.md` is *intentionally* a pending checklist, not a stub — it is the designated
human-gated checkpoint artifact, explicitly called out as such in both this SUMMARY and the
checkpoint report below.

## Issues Encountered

None. All seven pre-flight gate commands ran cleanly on the first attempt; no auto-fix was
needed.

## User Setup Required

**Yes — this is the checkpoint.** See the CHECKPOINT REACHED report accompanying this plan's
completion for the full prerequisites (two AWS credential sets, target-account GPU quota
headroom, the deploy sequence from the new runbook) and the ordered live procedure.

## Next Phase Readiness

- Every prior plan's code, Terraform, and doctor-check surface is now documented in one place
  (`docs/cross-account-capacity-borrowing.md`) and cross-linked from CLAUDE.md and the
  operator skill.
- The pre-flight gate is green across every package this phase touched, including the three
  the default `make test` target excludes.
- The only remaining work in this phase is the live verification itself (Task 3) — it cannot
  be completed by an executor agent; it requires a human operator with real target-account
  admin credentials, real home-account credentials, and confirmed GPU vCPU quota headroom in
  the target account and region.

---
*Phase: 126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin*
*Completed: 2026-08-23*

## Self-Check: PASSED

- `docs/cross-account-capacity-borrowing.md` — FOUND
- `.planning/phases/126-.../126-UAT.md` — FOUND
- `CLAUDE.md` contains `cross-account-capacity-borrowing.md` — FOUND (grep confirmed)
- `.claude-plugin/plugin.json` / `.claude-plugin/marketplace.json` both show `0.4.13` — FOUND
- Commit `280b4ec4` (Task 1) — FOUND in `git log --oneline`
- Commit `12f574ce` (Task 2) — FOUND in `git log --oneline`
