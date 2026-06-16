---
phase: 115-generic-github-webhook-event-prompt-router
plan: "04"
subsystem: github-bridge
tags: [init, terragrunt, config-export, github-events, deploy-surface, wave-3]
dependency_graph:
  requires:
    - "115-02 (GithubEventRule struct + Events field in GithubConfig)"
    - "115-03 (handleEventRoute wired, KM_GITHUB_EVENTS cold-start parse in main.go)"
  provides:
    - "internal/app/cmd/init.go: KM_GITHUB_EVENTS export block in ExportTerragruntEnvVars"
    - "infra/live/use1/lambda-github-bridge/terragrunt.hcl: github_events_json = get_env(KM_GITHUB_EVENTS, '')"
    - "infra/modules/lambda-github-bridge/v1.1.0/variables.tf: github_events_json variable"
    - "infra/modules/lambda-github-bridge/v1.1.0/main.tf: KM_GITHUB_EVENTS in Lambda env block"
    - "internal/app/cmd/doctor.go: checkGitHubEventsValid (pure-config validation)"
  affects:
    - "Phase 115 Plan 05 (doctor wiring — checkGitHubEventsValid already implemented here)"
    - "Phase 115 Plan 06 (E2E docs — deploy surface now complete)"
tech_stack:
  added: []
  patterns:
    - "KM_GITHUB_EVENTS export: JSON {events:[...]} marshalled from cfg.Github.Events (mirrors KM_GITHUB_REPOS at init.go:1632)"
    - "env-wins drift WARN: env var already set + differs from yaml → WARN to stderr, env wins"
    - "Dormant-when-empty: len(cfg.Github.Events)==0 skips export, Lambda gets empty default from get_env"
    - "In-place module edit at v1.1.0: additive variable with default='' (same pattern as Phase 100 github_peer_bridges)"
    - "filepath.Match for glob validation in checkGitHubEventsValid (path package not yet imported in doctor.go)"
key_files:
  created: []
  modified:
    - path: "internal/app/cmd/init.go"
      purpose: "KM_GITHUB_EVENTS export block in ExportTerragruntEnvVars (after KM_GITHUB_DEFAULT_ROUTER)"
    - path: "internal/app/cmd/doctor.go"
      purpose: "checkGitHubEventsValid function: empty->SKIPPED, malformed-glob->WARN, missing-profile->WARN, valid->OK"
    - path: "infra/live/use1/lambda-github-bridge/terragrunt.hcl"
      purpose: "github_events_json = get_env(KM_GITHUB_EVENTS, '') input added next to github_default_router"
    - path: "infra/modules/lambda-github-bridge/v1.1.0/variables.tf"
      purpose: "github_events_json variable (type=string, default='') added after github_default_router"
    - path: "infra/modules/lambda-github-bridge/v1.1.0/main.tf"
      purpose: "KM_GITHUB_EVENTS = var.github_events_json added to Lambda environment.variables block"
decisions:
  - "Added checkGitHubEventsValid in Plan 04 (not Plan 05): Wave-0 RED scaffold in doctor_test.go refs undefined function, preventing package compilation and blocking TestExport/TestRunInit (Rule 3). Implemented full validation (not just a nil stub) since test expectations were clear."
  - "Used filepath.Match for glob validation (path package not imported in doctor.go; filepath has identical semantics for bracket-expression error detection)"
  - "In-place module edit at v1.1.0 per Phase 100 precedent — additive variable with default='' never changes existing deployments"
metrics:
  duration: "296s"
  completed_date: "2026-06-15"
  tasks_completed: 2
  tasks_total: 2
  files_created: 0
  files_modified: 5
---

# Phase 115 Plan 04: KM_GITHUB_EVENTS Deploy-Surface Plumbing Summary

Both halves of the KM_GITHUB_EVENTS deploy pipeline wired: init.go export block + terragrunt get_env + module variable + Lambda environment entry, with drift-WARN parity and dormant-when-empty behavior mirroring KM_GITHUB_REPOS.

## What Was Done

### Task 1: KM_GITHUB_EVENTS export in ExportTerragruntEnvVars (init.go + doctor.go)

Added the KM_GITHUB_EVENTS export block to `internal/app/cmd/init.go` immediately after the `KM_GITHUB_DEFAULT_ROUTER` block (~line 1686), mirroring the `KM_GITHUB_REPOS` pattern at line 1632:

- Gate: `len(cfg.Github.Events) > 0` — absent `github.events:` block leaves `KM_GITHUB_EVENTS` unset (Lambda sees empty default from `get_env`, event routing stays dormant, byte-identical to Phase 114)
- Payload: inline `githubEventsPayload{Events: []config.GithubEventRule}` marshalled to JSON `{"events":[...]}`
- env-wins drift WARN: when `KM_GITHUB_EVENTS` is already set to a different value, prints `WARN: KM_GITHUB_EVENTS=<env> overrides km-config.yaml github.events=<yaml>` to stderr and leaves env var unchanged
- When env var is empty: `os.Setenv("KM_GITHUB_EVENTS", yamlGithubEvents)` sets it for downstream terragrunt `get_env()` call

Also added `checkGitHubEventsValid` to `internal/app/cmd/doctor.go` (see Deviations).

### Task 2: Terragrunt get_env + module variable + Lambda env entry

Three files edited in place (all additive, no version bump):

1. **`infra/live/use1/lambda-github-bridge/terragrunt.hcl`**: Added `github_events_json = get_env("KM_GITHUB_EVENTS", "")` input next to `github_default_router` (line 100). Comment explains the Phase 115 source and dormant-when-empty behavior.

2. **`infra/modules/lambda-github-bridge/v1.1.0/variables.tf`**: Added `variable "github_events_json"` with `type = string`, `default = ""`, and a description documenting the KM_GITHUB_EVENTS source and dormant semantics. Mirrors `github_repos_json` at line 50.

3. **`infra/modules/lambda-github-bridge/v1.1.0/main.tf`**: Added `KM_GITHUB_EVENTS = var.github_events_json` in the `environment.variables` block next to `KM_GITHUB_DEFAULT_ROUTER`. `terraform fmt -check` passes clean.

## Verification Results

```
go build ./internal/app/cmd/...
→ BUILD: OK

go test ./internal/app/cmd/... -run 'TestExport|TestRunInit|TestCheckGitHubEventsValid' -timeout 120s -count=1
→ ok  github.com/whereiskurt/klanker-maker/internal/app/cmd  21.424s  TEST_EXIT=0

cd infra/modules/lambda-github-bridge/v1.1.0 && terraform fmt -check .
→ FMT_EXIT=0

grep WIRED-OK check:
→ variables.tf: FOUND github_events_json
→ main.tf: FOUND KM_GITHUB_EVENTS
→ terragrunt.hcl: FOUND KM_GITHUB_EVENTS
→ WIRED-OK
```

No new `regionalModules()` entry added (module edited in place at v1.1.0) — `TestRunInitPlan_ModuleOrder` count unchanged.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Added checkGitHubEventsValid in Plan 04 (not Plan 05)**
- **Found during:** Task 1 verify (`go test ./internal/app/cmd/... -run 'TestExport|TestRunInit'`)
- **Issue:** `internal/app/cmd/doctor_test.go` (Wave 0 Phase 115 RED scaffold, lines 2390-2467) references `checkGitHubEventsValid` which was undefined. The entire `internal/app/cmd` package failed to compile, blocking `TestExport*` and `TestRunInit*` from running — a hard blocker for Task 1's verify gate.
- **Fix:** Implemented `checkGitHubEventsValid(rules []appcfg.GithubEventRule, repos []appcfg.GithubRepoEntry, defaultProfile string, configDir string) CheckResult` in `doctor.go` with full validation: empty→SKIPPED, malformed-match-glob→WARN (via `filepath.Match` error), malformed-exclude-glob→WARN, missing-profile-file→WARN, all-clear→OK. All 5 `TestCheckGitHubEventsValid` subtests pass.
- **Note:** Used `filepath.Match` (already imported as `path/filepath`) rather than `path.Match` (not yet imported) — identical error semantics for malformed bracket expressions.
- **Files modified:** `internal/app/cmd/doctor.go`
- **Commits:** `ee329e33` (included with Task 1 init.go changes)

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| Task 1 | ee329e33 | feat(115-04): KM_GITHUB_EVENTS export in ExportTerragruntEnvVars (init.go) |
| Task 2 | 7dd7521a | feat(115-04): terragrunt get_env + module var + Lambda env for KM_GITHUB_EVENTS |

## Self-Check: PASSED
