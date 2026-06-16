---
phase: 115-generic-github-webhook-event-prompt-router
plan: "05"
subsystem: github-bridge
tags: [github, manifest, doctor, event-router, wave-3, operator-ux]
dependency_graph:
  requires:
    - "115-02 (GithubEventRule struct + Events field in GithubConfig)"
    - "115-04 (checkGitHubEventsValid implemented as deviation; init.go export)"
  provides:
    - "internal/app/cmd/github.go: config-derived default_events union + metadata:read in RunGitHubManifest"
    - "internal/app/cmd/doctor.go: GetGithubEvents getter + checkGitHubEventsValid registered in km doctor run sequence"
  affects:
    - "Phase 115 Plan 06 (E2E docs — operator surfaces now complete)"
tech_stack:
  added: []
  patterns:
    - "Config-derived default_events union: eventsSet map[string]bool seeded with issue_comment, augmented from cfg.Github.Events on: types, sorted for determinism"
    - "Implied permission injection: eventsSet[repository] → defaultPermissions[metadata]=read"
    - "DoctorConfigProvider interface extension: GetGithubEvents() []appcfg.GithubEventRule getter + appConfigAdapter implementation"
    - "Doctor registration pattern: githubEvents := cfg.GetGithubEvents() captured in closure, appended after checkGitHubCommandsSSMParam in the GitHub checks section"
key_files:
  created: []
  modified:
    - path: "internal/app/cmd/github.go"
      purpose: "RunGitHubManifest: replaced hardcoded DefaultEvents with config-derived union; added metadata:read for repository event type"
    - path: "internal/app/cmd/doctor.go"
      purpose: "Added GetGithubEvents to DoctorConfigProvider interface + appConfigAdapter; registered checkGitHubEventsValid in the GitHub doctor group after checkGitHubCommandsSSMParam"
    - path: "internal/app/cmd/doctor_test.go"
      purpose: "Added GetGithubEvents() stubs to testConfig and testDoctorConfig mocks to satisfy the updated DoctorConfigProvider interface"
decisions:
  - "Config-derived manifest events (not hardcoded list): cfg already passes through RunGitHubManifest, so iterating cfg.Github.Events is zero-cost and always in sync with what the operator configured (per RESEARCH.md Open Question 2 recommendation)"
  - "sort.Strings for determinism: map iteration order is non-deterministic in Go; sorted slice ensures stable manifest output so golden tests won't flake"
  - "metadata:read on repository event: documented GitHub requirement — the repository webhook payload includes private fields that need this scope; added only when repository is in eventsSet"
  - "GetGithubEvents added to DoctorConfigProvider (not accessed via cfg.Github.Events directly): consistent with how all other github.* fields are accessed in RunDoctor; keeps the interface as the single seam for testing"
metrics:
  duration: "829s"
  completed_date: "2026-06-15"
  tasks_completed: 2
  tasks_total: 2
  files_created: 0
  files_modified: 3
---

# Phase 115 Plan 05: Manifest Event Union + Doctor Wiring Summary

Config-derived `default_events` union (issue_comment + all configured `on:` types) with `metadata:read` injection for repository events; `checkGitHubEventsValid` wired into the live `km doctor` run sequence.

## What Was Done

### Task 1: Manifest default_events union + implied scopes (github.go)

Replaced the hardcoded `DefaultEvents: []string{"issue_comment"}` at `github.go:109` with a config-derived union in `RunGitHubManifest`:

1. Seeds `eventsSet := map[string]bool{"issue_comment": true}` (always present)
2. Iterates `cfg.Github.Events` and adds each `rule.On` to the set (dormant-by-default: empty events leaves output unchanged from Phase 114)
3. Collects keys + `sort.Strings` for deterministic ordering
4. Builds `defaultPermissions` from the fixed base set, then adds `"metadata": "read"` when `eventsSet["repository"]` is true
5. Assigns `DefaultEvents: defaultEvents` and `DefaultPermissions: defaultPermissions` to the manifest payload

No new imports needed — `sort` was already imported.

### Task 2: checkGitHubEventsValid wired into km doctor (doctor.go + doctor_test.go)

The `checkGitHubEventsValid` function was implemented in Plan 04 as a Rule 3 blocking deviation (compile-fail), but was NOT registered in the doctor run sequence (the function existed but was never called from `RunDoctor`). This task completes the wiring:

1. Added `GetGithubEvents() []appcfg.GithubEventRule` to the `DoctorConfigProvider` interface (alongside `GetGithubPeerBridges`)
2. Added `appConfigAdapter.GetGithubEvents()` returning `a.cfg.Github.Events`
3. In the GitHub doctor registration block, after `checkGitHubCommandsSSMParam`, added:
   ```go
   githubEvents := cfg.GetGithubEvents()
   checks = append(checks, func(_ context.Context) CheckResult {
       return checkGitHubEventsValid(githubEvents, githubRepos, githubDefaultProfile, configDir)
   })
   ```
4. Added `GetGithubEvents() []appcfg.GithubEventRule { return nil }` stubs to both `testConfig` and `testDoctorConfig` in `doctor_test.go` to satisfy the updated interface

## Verification Results

```
go test ./internal/app/cmd/... -run 'TestRunGitHubManifest' -timeout 60s -count=1
→ ok  github.com/whereiskurt/klanker-maker/internal/app/cmd  0.766s  EXIT=0

go test ./internal/app/cmd/... -run 'TestCheckGitHubEventsValid' -timeout 60s -count=1
→ ok  github.com/whereiskurt/klanker-maker/internal/app/cmd  0.963s  EXIT=0

go test ./internal/app/cmd/... -run 'TestRunGitHubManifest|TestCheckGitHubEventsValid' -timeout 60s -count=1
→ ok  github.com/whereiskurt/klanker-maker/internal/app/cmd  0.756s  EXIT=0

go test ./internal/app/cmd/... -timeout 600s -count=1
→ ok  github.com/whereiskurt/klanker-maker/internal/app/cmd  433.741s  EXIT=0
```

Full internal/app/cmd suite GREEN (no regressions).

## Deviations from Plan

### Pre-existing Deviation from Plan 04 (acknowledged, no action needed)

**checkGitHubEventsValid was already implemented in Plan 04** (Rule 3 blocking deviation — compile-fail on undefined function prevented Plan 04's verification gate). This plan's Task 2 was therefore reduced to:

- Verifying the function was NOT yet registered in the doctor run sequence (confirmed: only defined, never called)
- Adding `GetGithubEvents` to the interface + adapter + test stubs
- Wiring the registration call into the run sequence

The function itself (SKIP/WARN/OK logic) was not touched — it was complete and all 5 subtests were already GREEN.

None — plan executed as scoped. The Task 2 scope reduction was documented in the `<critical_notes>` of the plan prompt.

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| Task 1 | a70b39d2 | feat(115-05): config-derived default_events union + metadata:read in RunGitHubManifest |
| Task 2 | 1691e155 | feat(115-05): wire checkGitHubEventsValid into km doctor run sequence |

## Self-Check: PASSED

- FOUND: internal/app/cmd/github.go
- FOUND: internal/app/cmd/doctor.go
- FOUND: internal/app/cmd/doctor_test.go
- FOUND commit a70b39d2 (Task 1)
- FOUND commit 1691e155 (Task 2)
- FOUND: eventsSet["repository"] metadata:read logic in github.go
- FOUND: checkGitHubEventsValid registration (githubEvents := cfg.GetGithubEvents()) in doctor.go
- FOUND: GetGithubEvents in DoctorConfigProvider interface
