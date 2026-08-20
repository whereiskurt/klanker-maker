---
phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway
plan: 07
subsystem: infra
tags: [km-doctor, dynamodb, nat-gateway, network-placement]

requires:
  - phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway (plan 03)
    provides: network.nat_gateway install-level config toggle + GetNATGatewayEnabled() accessor
  - phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway (plan 04)
    provides: SandboxMetadata.NetworkPlacement DynamoDB chokepoint
provides:
  - "checkNATIdle / checkPrivateWithoutNAT — two pure, table-tested km doctor checks (internal/app/cmd/doctor_network.go)"
  - "DoctorConfigProvider.GetNATGatewayEnabled() bool accessor"
  - "DoctorDeps.NetworkListSandboxMetadata production wiring (reuses the existing Codex-scanner DDB client)"
affects: ["km doctor operator output", "any future phase reading NetworkPlacement for reporting"]

tech-stack:
  added: []
  patterns:
    - "Shared running-and-private filter helper (runningPrivateSandboxIDs) mirrors the natDisableGuard literal ('private' + 'running') from Plan 06, so the two guard points (init-time refuse, doctor-time report) can never drift on what counts as a dangerous private sandbox."
    - "Metadata-fetch failure degrades to CheckSkipped (not CheckError/CheckWarn) at the registration-closure layer, matching the natDisableGuardPreApply fail-open precedent from Plan 06 — a doctor check that cannot read state should not report a false alarm."

key-files:
  created:
    - internal/app/cmd/doctor_network.go
    - internal/app/cmd/doctor_network_test.go
  modified:
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go

key-decisions:
  - "Added a new DoctorDeps.NetworkListSandboxMetadata field + wiring rather than reusing deps.SlackScanner or deps.CodexListSandboxes directly, because both existing listers filter (SlackChannelID != \"\" / agent==codex profile match) before returning — the NAT checks need every row's raw NetworkPlacement and Status untouched. The wiring itself still reuses the existing ddbClientForInbound *dynamodb.Client and sandboxTableForCodex table-name variable already constructed for the Codex scanner in initRealDepsWithExisting, so no new AWS client is created."
  - "GetNATGatewayEnabled() bool was added to the DoctorConfigProvider interface (delegating to the Plan 03 cfg.GetNATGatewayEnabled() accessor) and to both test config doubles (testConfig, testDoctorConfig; default false), following the exact pattern of the Phase 124 GetCapacityTableName() addition."
  - "The plan's literal acceptance-criteria grep count ('grep -c checkNATIdle doctor.go prints 1') was honored exactly by keeping the two function names out of any doubled comment line — no code was distorted to hit the number, but it was a close call: the first draft's DoctorDeps field comment happened to name both functions on one line, pushing the count to 2. Trimmed the comment to name the phase instead of the function names."

patterns-established:
  - "km doctor NAT-network checks: pure predicate in doctor_network.go + closure registration + nil-safe deps wiring, directly modeled on checkSlackPeerBridges (doctor_slack.go) end to end — any future 'toggle X enabled but nothing depends on it' / 'thing Y exists but toggle X is off' pair of checks can copy this shape."

requirements-completed: [REQ-125-DOCTOR]

coverage:
  - id: D1
    description: "km doctor WARNs, naming the approximate $132/month cost and that it is safe to disable, when network.nat_gateway is enabled but zero running private sandboxes depend on it"
    requirement: "REQ-125-DOCTOR"
    verification:
      - kind: unit
        ref: "internal/app/cmd/doctor_network_test.go#TestDoctorNetworkNATIdle (Test 1, Test 7a, Test 7b)"
        status: pass
    human_judgment: false
  - id: D2
    description: "checkNATIdle returns OK when NAT is enabled and at least one running private sandbox exists, and SKIPPED (silent) when NAT was never enabled"
    requirement: "REQ-125-DOCTOR"
    verification:
      - kind: unit
        ref: "internal/app/cmd/doctor_network_test.go#TestDoctorNetworkNATIdle (Test 2, Test 3)"
        status: pass
    human_judgment: false
  - id: D3
    description: "km doctor WARNs, naming the affected sandbox ids, when a running private sandbox exists but network.nat_gateway is off (broken egress); SKIPS when NAT is on (condition cannot occur) or no running private sandboxes exist"
    requirement: "REQ-125-DOCTOR"
    verification:
      - kind: unit
        ref: "internal/app/cmd/doctor_network_test.go#TestDoctorNetworkPrivateWithoutNAT (Test 4, Test 5, Test 6)"
        status: pass
    human_judgment: false
  - id: D4
    description: "Both checks exclude private-but-not-running rows and rows with an empty NetworkPlacement (pre-125 sandboxes, implicitly public) from every count"
    requirement: "REQ-125-DOCTOR"
    verification:
      - kind: unit
        ref: "internal/app/cmd/doctor_network_test.go (Test 7a/7b/7c/7d across both test functions)"
        status: pass
    human_judgment: false
  - id: D5
    description: "Both checks are registered in km doctor's buildChecks as read-only closures — a metadata-fetch error degrades to SKIPPED, and neither closure ever calls Delete/Put/Update on DynamoDB"
    requirement: "REQ-125-DOCTOR"
    verification:
      - kind: unit
        ref: "go test ./internal/app/cmd/ -run 'TestDoctor' -count=1 -timeout 600s"
        status: pass
      - kind: other
        ref: "manual read of the two registration closures in internal/app/cmd/doctor.go (buildChecks) confirms no Delete/Put/Update call"
        status: pass
    human_judgment: false

duration: 25min
completed: 2026-08-19
status: complete
---

# Phase 125 Plan 07: km doctor NAT-network checks Summary

**Two pure, table-tested `km doctor` checks — idle-NAT cost warning and private-sandbox-without-NAT egress warning — registered read-only against live sandbox metadata, silent on any Phase 124 install.**

## Performance

- **Duration:** 25min
- **Tasks:** 2 completed
- **Files modified:** 4 (2 created, 2 modified)

## Accomplishments
- `checkNATIdle` and `checkPrivateWithoutNAT` — two pure functions (`(natEnabled bool, metas []kmaws.SandboxMetadata) CheckResult`), no AWS client or `context.Context` in either signature, table-tested with all 7 behaviour cases from the plan (including the two exclusion cases: private-but-stopped, running-but-empty-placement).
- Both checks registered in `km doctor`'s `buildChecks` via the same closure-registration shape as `checkSlackPeerBridges`, reading `cfg.GetNATGatewayEnabled()` and fetching sandbox metadata through a new nil-safe `DoctorDeps.NetworkListSandboxMetadata` field.
- Production wiring reuses the existing `ddbClientForInbound` DynamoDB client and `sandboxTableForCodex` table-name variable (already constructed for the Phase 70 Codex scanner) — no new AWS client is created.
- `DoctorConfigProvider` gained `GetNATGatewayEnabled() bool`, implemented on `appConfigAdapter` and both test config doubles.

## Task Commits

Each task was committed atomically:

1. **Task 1: Write the two pure doctor check functions and their table tests** - `62b52022` (feat)
2. **Task 2: Register both checks in doctor.go** - `0757a559` (feat)

**Plan metadata:** (this commit)

## Files Created/Modified
- `internal/app/cmd/doctor_network.go` - `checkNATIdle`, `checkPrivateWithoutNAT`, shared `runningPrivateSandboxIDs` filter
- `internal/app/cmd/doctor_network_test.go` - `TestDoctorNetworkNATIdle`, `TestDoctorNetworkPrivateWithoutNAT` (7 behaviour cases across both)
- `internal/app/cmd/doctor.go` - `DoctorConfigProvider.GetNATGatewayEnabled()`, `appConfigAdapter` impl, `DoctorDeps.NetworkListSandboxMetadata` field + production wiring, two check registrations in `buildChecks`
- `internal/app/cmd/doctor_test.go` - `GetNATGatewayEnabled() bool { return false }` added to `testConfig` and `testDoctorConfig`

## Decisions Made
- New `DoctorDeps.NetworkListSandboxMetadata` field instead of reusing `SlackScanner`/`CodexListSandboxes` — both existing listers pre-filter rows before returning, but the NAT checks need every row's raw `NetworkPlacement`/`Status` untouched. Reused the underlying DDB client and table-name variable rather than constructing new ones.
- `GetNATGatewayEnabled() bool` added to `DoctorConfigProvider` (delegating to the Plan 03 `cfg.GetNATGatewayEnabled()`), following the exact `GetCapacityTableName()` precedent.
- Trimmed a `DoctorDeps` field comment that originally named both `checkNATIdle` and `checkPrivateWithoutNAT` on one line (which would have made the plan's literal `grep -c checkNATIdle doctor.go == 1` acceptance criterion fail with 2) down to naming the phase instead — no code logic was distorted, only comment wording, to hit the literal count honestly rather than reinterpreting the criterion.

## Deviations from Plan

None - plan executed exactly as written (one cosmetic comment-wording adjustment to satisfy a literal grep-count acceptance criterion, documented above; no logic distortion).

## Issues Encountered
None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

`km doctor` now reports on NAT/private-sandbox drift in both dangerous directions, read-only, silent on installs that have never touched either Phase 125 toggle. No blockers for subsequent Phase 125 plans; `runningPrivateSandboxIDs`'s filter literal (`"private"` + `"running"`) is available for reuse by any future check needing the same private-and-running predicate.

---
*Phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway*
*Completed: 2026-08-19*
