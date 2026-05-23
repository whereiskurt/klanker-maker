---
phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
plan: 07
subsystem: doctor
tags: [doctor, codex, dynamodb, s3, ssm, health-check, phase-70]

# Dependency graph
requires:
  - phase: 70-02
    provides: codex config.toml written to sandboxes (harmless no-op artifact post-Path B)
  - phase: 70-05
    provides: km-slack-threads DDB rows with agent_type attribute written by poller

provides:
  - checkCodexVersionSupportsJSONL doctor check (SKIP when no codex sandboxes)
  - checkAgentTypeConsistency doctor check (paginated DDB scan vs S3 profile)
  - DoctorDeps fields for both Phase 70 checks + production closures in initRealDepsWithExisting
  - codexVersionSatisfied semver helper (0.121.0 minimum gate)
  - scanThreadAgentRowsImpl helper (Limit=100 paginated DDB Scan)

affects:
  - 70-09 (UAT step 6: km doctor on agent: codex sandbox returns green)
  - any future Codex doctor check additions

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "SSMCodexRunner + ProfileFetcherFunc test seams via function type (not interface) for mockable doctor checks"
    - "codexVersionSatisfied err-open pattern: unparseable version string → true (don't false-alarm on unknown formats)"
    - "DoctorDeps nil-field convention: CodexSSMRunner=nil returns CheckSkipped (org SCP blocks SendCommand)"

key-files:
  created:
    - internal/app/cmd/doctor_codex.go
    - (doctor_codex_test.go already existed as stubs from Task 1)
  modified:
    - internal/app/cmd/doctor_codex_test.go
    - internal/app/cmd/doctor.go

key-decisions:
  - "Path B revision: first check renamed from codex_hook_config_present to codex_version_supports_jsonl; probes codex binary + version + --json flag instead of ~/.codex/config.toml hook entries"
  - "CodexSSMRunner set nil in production (org-level SCP blocks ssm:SendCommand on application account); check returns CheckSkipped rather than WARN — no false alarms on standard installs"
  - "codexSandboxRef.InstanceID left empty in production (SandboxMetadata has no EC2 instance ID field); checkCodexVersionSupportsJSONL skips SSM probe when InstanceID is empty"
  - "codexVersionSatisfied is err-open: unparseable version string returns true to avoid false-positives on AMIs with unexpected codex version output format"
  - "scanThreadAgentRowsImpl uses Limit=100 pages (Pitfall 7 from 70-RESEARCH.md)"

patterns-established:
  - "Phase 70 doctor check pattern: function-type test seams (SSMCodexRunner, ProfileFetcherFunc) rather than interface mocks"
  - "Doctor nil-dep SKIP pattern applied to all 4 new DoctorDeps fields"

requirements-completed: [SC-7]

# Metrics
duration: 22min
completed: 2026-05-23
---

# Phase 70 Plan 07: Codex Doctor Checks Summary

**Two km doctor checks for Phase 70 Codex parity: codex_version_supports_jsonl (SSM probes codex binary/version/JSONL flag on agent:codex sandboxes) and agent_type_consistency (paginated DDB scan of km-slack-threads vs S3 profile)**

## Performance

- **Duration:** ~22 min
- **Started:** 2026-05-23T00:00:00Z
- **Completed:** 2026-05-23
- **Tasks:** 2/2
- **Files modified:** 3 (doctor_codex.go created, doctor_codex_test.go replaced, doctor.go extended)

## Accomplishments

- New `internal/app/cmd/doctor_codex.go` (186 lines) with both check functions, production helpers, and test seams
- 8 unit tests PASS (5 for JSONL version check, 3 for agent consistency); existing doctor suite unaffected
- Both checks registered in `buildChecks` with WARN-only demote guard; production deps wired in `initRealDepsWithExisting`
- `codexVersionSatisfied` semver helper validates >= 0.121.0 (the Phase 70 spike minimum), err-open on parse failure
- `scanThreadAgentRowsImpl` uses paginated DDB Scan (Limit=100) per Pitfall 7 guard from 70-RESEARCH.md

## Task Commits

1. **Task 1: Wave 0 stubs (5 stub tests)** — `c815033` (test)
2. **Task 2: Both check implementations + real tests + doctor.go wiring** — `f2bc10a` (feat)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/internal/app/cmd/doctor_codex.go` — both check functions, SSMCodexRunner + ProfileFetcherFunc seams, scanThreadAgentRowsImpl, codexVersionSatisfied
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/doctor_codex_test.go` — 8 real tests replacing Wave 0 stubs (+ TestCodexVersionSatisfied table tests)
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/doctor.go` — DoctorDeps 4 new Phase 70 fields, buildChecks registration, initRealDepsWithExisting production wiring

## Decisions Made

- **Path B revision applied:** The check originally named `codex_hook_config_present` (which would SSM-probe `~/.codex/config.toml` for hook entries) was replaced by `codex_version_supports_jsonl` per CONTEXT.md "Path B contract". The hook TOML is a harmless no-op artifact; the JSONL check is what actually guards Phase 70 correctness.
- **Production SSMRunner set nil:** The org-level SCP blocks `ssm:SendCommand` on the application account (confirmed in CLAUDE.md and `internal/app/cmd/create.go` comments). Setting `CodexSSMRunner = nil` causes the check to return `CheckSkipped` — the correct UX for standard installs. An integration test or non-SCP environment can inject a real runner.
- **InstanceID not populated in production:** `SandboxMetadata` has no EC2 instance ID field. `checkCodexVersionSupportsJSONL` skips SSM probes when `InstanceID` is empty (which is always in production today). A follow-on enhancement could resolve IDs via EC2 DescribeInstances with sandbox_id tag.
- **err-open on version parse failure:** `codexVersionSatisfied` returns `true` when the version string can't be parsed (e.g. unexpected format from a custom AMI). This avoids false-positive WARNs.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Path B revision] Check renamed from codex_hook_config_present to checkCodexVersionSupportsJSONL**
- **Found during:** Task 2 (plan read + CONTEXT.md)
- **Issue:** CONTEXT.md "Path B contract" (2026-05-23) supersedes the original hook-config check. The original plan brief's `truths` still reference `checkCodexHookConfigPresent` but the execution prompt explicitly overrides with Path B.
- **Fix:** Implemented `checkCodexVersionSupportsJSONL` as specified in Path B; test names kept as `TestDoctor_CodexHookPresent_*` for traceability to the original plan spec.
- **Files modified:** internal/app/cmd/doctor_codex.go, doctor_codex_test.go
- **Committed in:** f2bc10a

---

**Total deviations:** 1 auto-applied (Path B revision per CONTEXT.md — not a bug fix but a spec override)

**Impact on plan:** Path B check is forward-compatible and more useful than the hook-config check. Plan 70-09 UAT step 6 remains achievable.

## Issues Encountered

None — implementation was straightforward with the existing doctor check patterns as models.

## Next Phase Readiness

- SC-7 satisfied: `km doctor` can surface Codex JSONL version drift and agent_type consistency drift
- Plan 70-09 UAT step 6 (`km doctor` on a codex sandbox returns CheckSkipped for JSONL check in standard SCP install; agent_type_consistency returns CheckOK when DDB rows match profiles) has a working backend
- The JSONL version probe will become live if `CodexSSMRunner` is wired in a non-SCP environment

---
*Phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher*
*Completed: 2026-05-23*
