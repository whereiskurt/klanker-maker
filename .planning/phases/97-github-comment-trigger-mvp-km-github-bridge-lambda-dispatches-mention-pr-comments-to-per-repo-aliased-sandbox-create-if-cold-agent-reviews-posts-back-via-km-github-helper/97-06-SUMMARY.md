---
phase: 97-github-comment-trigger-mvp
plan: "06"
subsystem: github-bridge-doctor-runbook
tags: [doctor, github-bridge, runbook, e2e-gate]
dependency_graph:
  requires: [97-01, 97-04, 97-05]
  provides: [github-bridge-doctor-checks, github-bridge-runbook]
  affects: [km-doctor, docs]
tech_stack:
  added: []
  patterns:
    - DoctorConfigProvider interface extension with GetGithubRepos/GetGithubDefaultProfile
    - checkGitHubWebhookSecret/checkGitHubBotLoginCached/checkGitHubBridgeURL/checkGitHubReposResolvable
    - silent-skip gate (no repos + no app-client-id SSM key = all 4 checks skipped)
    - match-overlap detection (pure config scan, no AWS calls)
key_files:
  created:
    - internal/app/cmd/doctor_github_test.go
    - docs/github-bridge.md
  modified:
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go
    - CLAUDE.md
decisions:
  - All four GitHub bridge doctor checks are WARN-level (not ERROR) — GitHub is opt-in
  - Silent-skip gate mirrors the Slack inbound skip pattern (no repos + SSM probe absent = SKIPPED)
  - checkGitHubReposResolvable is a pure config function with no AWS calls (fast, no SSM)
  - match-overlap: duplicate Match values in github.repos WARN (shadowed entry)
  - HTTPS check in checkGitHubBridgeURL catches misconfigured function URLs
metrics:
  duration: 1042s
  completed_date: "2026-06-06"
  tasks_completed: 2
  tasks_total: 3
  files_modified: 5
  files_created: 2
status: checkpoint
checkpoint_task: 3
checkpoint_type: human-verify
---

# Phase 97 Plan 06: Doctor Checks + Runbook + E2E UAT Summary

**One-liner:** GitHub bridge doctor checks (webhook-secret/bot-login/bridge-url/repos-resolvable) with silent-skip gate when unconfigured + full operator runbook in docs/github-bridge.md.

## Tasks Completed

| Task | Description | Commit | Status |
|---|---|---|---|
| 1 | km doctor GitHub bridge checks + 17 tests | d82fbe20 | Done |
| 2 | docs/github-bridge.md runbook + CLAUDE.md entry | fea74f35 | Done |
| 3 | Manual E2E UAT (warm + cold + negative) | — | **CHECKPOINT** |

## What Was Built

### Task 1: km doctor GitHub bridge checks

Added 4 new check functions to `internal/app/cmd/doctor.go`:

- **checkGitHubWebhookSecret** — verifies `/{prefix}/config/github/webhook-secret` exists in SSM; WARN when missing, remediation → `km github init`
- **checkGitHubBotLoginCached** — verifies `/{prefix}/config/github/bot-login` exists and is non-empty; WARN when missing
- **checkGitHubBridgeURL** — verifies `/{prefix}/config/github/bridge-url` is a valid `https://` URL; WARN when missing or non-HTTPS
- **checkGitHubReposResolvable** — pure config check: each `github.repos` entry must resolve to a profile (or `github.default_profile` fallback); detects match-overlap (two entries with identical Match pattern)

**Silent-skip gate:** When `github.repos` is empty AND `app-client-id` is absent in SSM, all 4 checks return `CheckSkipped` with no WARN — mirrors the Slack inbound skip pattern. Pre-existing `checkGitHubConfig` gated the same way.

Extended `DoctorConfigProvider` interface with `GetGithubRepos()` and `GetGithubDefaultProfile()`. Added adapter methods in `appConfigAdapter`. Updated `testDoctorConfig` and `testConfig` stubs in `doctor_test.go`.

**Tests:** 17 unit tests in `doctor_github_test.go` covering all check functions (OK/WARN/SKIPPED/NotHTTPS paths) and the unconfigured-skip integration case.

### Task 2: docs/github-bridge.md operator runbook

Complete operator runbook at `docs/github-bridge.md` covering:
- Architecture overview (warm/cold dispatch paths, SSM parameters)
- GitHub App scope table (issues:write, pull_requests:write, contents:read, issue_comment webhook)
- Config surface: `github.repos` field reference (match/alias/profile/allow), resolution order
- `profiles/github-review.yaml` summary
- CLI reference: `km github init/manifest/status` and sandbox-side `km-github comment/review/pr-files`
- Full 8-step deploy sequence with memory notes (build-lambdas clean, km init full vs --sidecars)
- Dormant invariant note
- km doctor GitHub check table
- Troubleshooting for missing 👀, never-posted review, cold-create failure, duplicate reviews

Added Phase 97 entry to CLAUDE.md (Phase summary + "Where to look" row).

## Deviations from Plan

None - plan executed exactly as written (2 autonomous tasks completed).

Pre-existing out-of-scope test failure documented in `deferred-items.md`:
- `TestGoSourceNamesUseResourcePrefix` in `pkg/hygiene` — 5 hardcoded `km-` sites in `doctor_artifacts.go` and `doctor_log_groups.go` (not touched by Plan 06); pre-dates this plan.

## Checkpoint: Task 3 (E2E UAT)

Task 3 is `type="checkpoint:human-verify"` — a live-AWS + live-GitHub end-to-end test that cannot be automated. The operator must:

1. Deploy (Steps 1-6 from the deploy sequence in `docs/github-bridge.md`)
2. Run the WARM path test
3. Run the COLD path test
4. Run the NEGATIVE checks
5. Type "approved" to resume (or describe what failed for --gaps planning)

See the checkpoint return message below for exact steps.

## Self-Check: PASSED

Created files confirmed present:
- internal/app/cmd/doctor_github_test.go ✓
- docs/github-bridge.md ✓

Commits confirmed:
- d82fbe20 feat(97-06): km doctor GitHub bridge checks + tests ✓
- fea74f35 docs(97-06): github-bridge.md operator runbook + CLAUDE.md Phase 97 entry ✓
