---
phase: 101-github-bridge-orphan-repo-helpful-reply-front-door-posts-guidance-when-no-install-owns-the-repo-claim-aware-scatter-gather-slack-phase-96-analog
plan: "01"
subsystem: config-surface
tags: [github, config, terraform, tdd]
dependency_graph:
  requires: []
  provides: [GithubConfig.DefaultRouter, KM_GITHUB_DEFAULT_ROUTER, github_default_router-tf-var]
  affects: [lambda-github-bridge, km-init, km-config-yaml]
tech_stack:
  added: []
  patterns: [tri-state-bool-config, env-wins-drift-warn, tdd-red-green, in-place-tf-module-edit]
key_files:
  created: []
  modified:
    - internal/app/config/config.go
    - internal/app/config/config_test.go
    - internal/app/cmd/init.go
    - internal/app/cmd/init_test.go
    - infra/live/use1/lambda-github-bridge/terragrunt.hcl
    - infra/modules/lambda-github-bridge/v1.1.0/variables.tf
    - infra/modules/lambda-github-bridge/v1.1.0/main.tf
decisions:
  - "No new merge-list entry for github.default_router — decoded by existing UnmarshalKey(\"github\") call (proven by TestLoadGithubDefaultRouter_Set, mirrors Phase 100 PeerBridges precedent)"
  - "In-place v1.1.0 TF module edit with additive default=\"false\" var — no version bump (Phase 100 precedent)"
  - "tri-state *bool for dormancy: nil=absent/dormant, true=front-door active, false=explicit off"
metrics:
  duration: 213s
  completed_date: "2026-06-08"
  tasks_completed: 3
  files_modified: 7
---

# Phase 101 Plan 01: GitHub Default Router Config Surface Summary

**One-liner:** `github.default_router: true` tri-state config toggle plumbed end-to-end from km-config.yaml through GithubConfig.DefaultRouter (*bool) to KM_GITHUB_DEFAULT_ROUTER Lambda env var via terragrunt, dormant by default (Phase 100 byte-identical).

## What Was Built

Config surface for the Phase 101 front-door orphan-repo router toggle. A single `*bool` field on `GithubConfig` (auto-decoded by the existing `UnmarshalKey("github")` call — no new merge-list entry), a `KM_GITHUB_DEFAULT_ROUTER` env export in `km init` (with env-wins drift WARN, cloned from the `KM_SLACK_DEFAULT_ROUTER` block), and the terragrunt → TF var → Lambda env-block plumbing.

Nothing consumes the env var yet (Plan 03 reads `os.Getenv("KM_GITHUB_DEFAULT_ROUTER")` in `cmd/km-github-bridge/main.go`).

## Tasks Completed

| Task | Name | Type | Commit | Files |
|------|------|------|--------|-------|
| 1 | RED — config round-trip + init export/drift tests | tdd-red | bd61d6af | config_test.go, init_test.go |
| 2 | GREEN — GithubConfig.DefaultRouter field + init.go export | tdd-green | 3d200a29 | config.go, init.go |
| 3 | TF plumbing — terragrunt + var + Lambda env block | auto | 009d463f | terragrunt.hcl, variables.tf, main.tf |

## Verification

- `go test ./internal/app/config/... ./internal/app/cmd/... -run GithubDefaultRouter -count=1` — GREEN (5 sub-tests)
- No `"github.default_router"` string literal in the merge-list (only in doc-comments; `"github"` entry at line 579 covers the whole block)
- `terraform validate` passes on `infra/modules/lambda-github-bridge/v1.1.0`
- `KM_GITHUB_DEFAULT_ROUTER` present in Lambda env block in main.tf
- `github_default_router = get_env("KM_GITHUB_DEFAULT_ROUTER", "false")` in terragrunt.hcl

## Key Decisions

1. **No new merge-list entry** — `github.default_router` is decoded atomically by the existing `"github"` merge entry + `v.UnmarshalKey("github", &cfg.Github)` call. Adding a `"github.default_router"` entry would be redundant (proven by TestLoadGithubDefaultRouter_Set). This is identical to the Phase 100 PeerBridges precedent (RESEARCH Pitfall 6).

2. **In-place v1.1.0 edit** — The `lambda-github-bridge/v1.1.0` module was edited in place with an additive `default="false"` variable. No `source =` version bump needed (Phase 100 established this precedent for additive dormant-by-default env vars).

3. **Tri-state `*bool`** — nil = absent/dormant (no export, terragrunt default "false"), `&true` = front-door active, `&false` = explicit off. This matches the `KM_SLACK_DEFAULT_ROUTER` pattern exactly.

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

Files exist: config.go, config_test.go, init.go, init_test.go, terragrunt.hcl, variables.tf, main.tf — all FOUND.
Commits exist: bd61d6af (RED tests), 3d200a29 (GREEN implementation), 009d463f (TF plumbing) — all FOUND.
