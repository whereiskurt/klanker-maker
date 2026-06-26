---
phase: 120
slug: profiles-reset-and-os-layered-fragment-library
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-26
---

# Phase 120 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> This phase is a contract-preserving refactor: the PRIMARY validation is that
> existing byte-identity + golden tests stay GREEN after files move and test path
> constants update. No new product behavior → existing infrastructure covers it.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | `go test` (Go stdlib testing) + bash (`validate-all-profiles.sh`) + `km validate` |
| **Config file** | none — existing Go test suite + repo Makefile |
| **Quick run command** | `go test ./pkg/compiler/... ./pkg/profile/... -count=1 -timeout 600s` |
| **Full suite command** | `make build && go test ./pkg/compiler/... ./pkg/profile/... ./internal/app/cmd/... -count=1 -timeout 600s; bash scripts/validate-all-profiles.sh` |
| **Estimated runtime** | ~90–180 seconds (compiler+profile fast; cmd suite slower) |

---

## Sampling Rate

- **After every task commit:** Run the quick command for the package touched
  (`go test ./pkg/compiler/...` after a test-path edit; `km validate <leaf>` after a fragment/leaf edit).
- **After every plan wave:** Run the full suite command.
- **Before `/gsd:verify-work`:** Full suite green + `validate-all-profiles.sh` exit 0 + `km validate` of all 4 leaves no-WARN.
- **Max feedback latency:** ~180 seconds.
- **CRITICAL:** capture the command's OWN exit code — never `go test … | tail` (a piped tail returns 0 and masks a FAIL). Use `; echo $?` or `set -o pipefail`.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 120-01-* | 01 | 1 | Author base/os + toolchain + plugin + slack fragments | validate | `make build && km validate` (via leaves in plan 02) | ✅ existing | ⬜ pending |
| 120-02-* | 02 | 2 | Author 4 composed leaves (learner/desktop/github/h1) | validate | `km validate profiles/{learner,desktop,github,h1}.yaml` (exit 0, no WARN) | ✅ existing | ⬜ pending |
| 120-03-* | 03 | 2 | `git mv` retired demos + frozen fixtures → testdata/profiles/ | byte-identity | `go test ./pkg/compiler/... -run ByteIdentity -count=1` | ✅ existing | ⬜ pending |
| 120-03-* | 03 | 2 | Update 6 test-path constants in lockstep | unit/golden | `go test ./pkg/compiler/... ./pkg/profile/... -count=1 -timeout 600s` | ✅ existing | ⬜ pending |
| 120-04-* | 04 | 3 | Rewrite validate-all-profiles.sh inventory + base/os skip | integration | `bash scripts/validate-all-profiles.sh; echo $?` (exit 0) | ✅ existing | ⬜ pending |
| 120-04-* | 04 | 3 | km-config.yaml path swaps (4 refs) | grep-assert | `grep -n 'profiles/learn.v2\|profiles/github-review\|profiles/h1-triage' km-config.yaml` (no hits) | ✅ existing | ⬜ pending |
| 120-05-* | 05 | 3 | learner functionally matches learn.v2 (REVIEW gate) | manual-diff | compiled-userdata diff (see Manual-Only) | n/a | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Existing infrastructure covers all phase requirements. No new test framework, no Wave 0 stubs.
The validating tests (`TestUserdataLearnV2Phase92ByteIdentity`, `agent_claude_golden_test`,
`agent_codex_golden_test`, `github_review_secrets_test`, `validate-all-profiles.sh`) already exist;
this phase only relocates their inputs and updates path constants.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| `learner` functionally matches `learn.v2.yaml` | Design "functional match" | Composed leaf is deliberately NOT byte-identical (fragment ordering + intentional plugin-enable delta) — a hard byte assertion would be wrong | Compile userdata for new `profiles/learner.yaml` and archived `testdata/profiles/learn.v2.yaml` (via the compiler test harness or a scratch `km` invocation); diff; EVERY difference must be explainable as (a) fragment union ordering or (b) the documented plugin-enable choice — NOT accidental toolchain/version drift. Reviewer signs off. |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or are the single documented manual-diff review
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (none — existing infra)
- [ ] No watch-mode flags
- [ ] Feedback latency < 180s
- [ ] `nyquist_compliant: true` set in frontmatter (planner/checker to flip when plans align)

**Approval:** pending
