---
phase: 28-github-repo-level-mitm-filtering-in-http-proxy
plan: "02"
subsystem: http-proxy
tags: [proxy, github, mitm, repo-filter, compiler, userdata, ecs, tdd]
dependency_graph:
  requires: [28-01]
  provides: [github-repo-filter-e2e-wiring]
  affects: [sidecars/http-proxy, pkg/compiler]
tech_stack:
  added: []
  patterns: [tdd-red-green, env-var-propagation, nil-safe-helpers]
key_files:
  created: []
  modified:
    - sidecars/http-proxy/main.go
    - pkg/compiler/userdata.go
    - pkg/compiler/service_hcl.go
    - pkg/compiler/userdata_test.go
    - pkg/compiler/service_hcl_test.go
decisions:
  - "Single CSV helper per compiler file: joinGitHubAllowedRepos in userdata.go and joinGitHubAllowedReposCSV in service_hcl.go — same nil-safe pattern as existing joinAllowedRefs, returns empty string when GitHub config is nil or AllowedRepos is empty"
  - "GitHubAllowedReposCSV (string) added to ecsHCLParams alongside existing GitHubAllowedRepos ([]string for Lambda token inputs) — two distinct fields for two distinct uses"
metrics:
  duration: 420s
  completed_date: "2026-03-28"
  tasks_completed: 1
  files_changed: 5
---

# Phase 28 Plan 02: GitHub Repo Filter E2E Wiring Summary

**One-liner:** KM_GITHUB_ALLOWED_REPOS wired end-to-end from profile YAML through EC2 userdata and ECS service.hcl compiler templates to proxy main.go `WithGitHubRepoFilter` option.

## What Was Built

Wired the GitHub repo allowlist from the SandboxProfile through both compiler substrates to the proxy sidecar runtime:

1. **main.go**: Reads `KM_GITHUB_ALLOWED_REPOS` env var, parses CSV to `[]string`, appends `httpproxy.WithGitHubRepoFilter(repos)` to `proxyOpts` when non-empty. Logs `event_type: github_repo_filter_enabled` with `allowed_repos` field at startup.

2. **pkg/compiler/userdata.go**: Added `GitHubAllowedRepos string` field to `userDataParams`, `joinGitHubAllowedRepos(p)` nil-safe helper, and `Environment=KM_GITHUB_ALLOWED_REPOS={{ .GitHubAllowedRepos }}` line after `ALLOWED_HOSTS` in the km-http-proxy systemd unit template.

3. **pkg/compiler/service_hcl.go**: Added `GitHubAllowedReposCSV string` field to `ecsHCLParams`, `joinGitHubAllowedReposCSV(p)` nil-safe helper, and `{ name = "KM_GITHUB_ALLOWED_REPOS", value = "{{ .GitHubAllowedReposCSV }}" }` in the km-http-proxy ECS container environment block.

4. **Tests (TDD)**: Four tests added — EC2 userdata with/without GitHub config, ECS service.hcl with/without GitHub config.

## Files

**Modified:**
- `/Users/khundeck/working/klankrmkr/sidecars/http-proxy/main.go` — GitHub repo filter env var reading and `WithGitHubRepoFilter` wiring
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go` — `GitHubAllowedRepos` field, `joinGitHubAllowedRepos` helper, systemd template line
- `/Users/khundeck/working/klankrmkr/pkg/compiler/service_hcl.go` — `GitHubAllowedReposCSV` field, `joinGitHubAllowedReposCSV` helper, ECS environment entry
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata_test.go` — `TestUserDataGitHubAllowedRepos`, `TestUserDataGitHubAllowedReposEmpty`
- `/Users/khundeck/working/klankrmkr/pkg/compiler/service_hcl_test.go` — `TestECSServiceHCLGitHubAllowedRepos`, `TestECSServiceHCLGitHubAllowedReposEmpty`

## Decisions Made

1. **Nil-safe CSV helpers per file:** `joinGitHubAllowedRepos` in `userdata.go` and `joinGitHubAllowedReposCSV` in `service_hcl.go` follow the same pattern as the existing `joinAllowedRefs` — return `""` when `GitHub` is nil or `AllowedRepos` is empty. This is the backward-compatible path.

2. **Two distinct fields in ecsHCLParams:** `GitHubAllowedRepos []string` (already existed for the Lambda github-token HCL block) and `GitHubAllowedReposCSV string` (new, for the proxy container env var). Kept separate to avoid ambiguity between HCL list notation and plain CSV.

## Test Results

```
TestUserDataGitHubAllowedRepos         PASS  (systemd unit contains KM_GITHUB_ALLOWED_REPOS=myorg/myrepo,other/repo)
TestUserDataGitHubAllowedReposEmpty    PASS  (no non-empty value when GitHub config absent)
TestECSServiceHCLGitHubAllowedRepos    PASS  (container env contains KM_GITHUB_ALLOWED_REPOS with CSV value)
TestECSServiceHCLGitHubAllowedReposEmpty PASS (backward compat without GitHub config)
All other compiler tests                PASS
All proxy/httpproxy tests               PASS
go vet ./sidecars/http-proxy/... ./pkg/compiler/... CLEAN
km build: PASS
km create profiles/claude-dev.yaml: compiler ran, 22864-byte bootstrap script generated and uploaded to S3
```

## Deviations from Plan

None - plan executed exactly as written.

## Self-Check: PASSED
