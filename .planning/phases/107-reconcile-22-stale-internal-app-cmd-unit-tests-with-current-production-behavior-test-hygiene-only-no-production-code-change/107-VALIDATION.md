---
phase: 107
slug: reconcile-22-stale-internal-app-cmd-unit-tests
status: approved
nyquist_compliant: true
wave_0_complete: true
created: 2026-06-11
---

# Phase 107 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> This phase's deliverable IS test correctness, so the tests are both the work
> product and the validation instrument.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none (Go std test) |
| **Quick run command** | `go test ./internal/app/cmd/ -count=1 -run '<subsystem regex>' -timeout 600s; echo "EXIT=$?"` |
| **Full suite command** | `go test ./internal/app/cmd/ -count=1 -timeout 600s; echo "EXIT=$?"` |
| **Estimated runtime** | ~400–600 s full suite (many tests make real AWS calls) |

> CRITICAL: read `go test`'s OWN exit code (`; echo "EXIT=$?"`, or `set -o pipefail`),
> NEVER a piped exit. A piped `| tail` exit is exactly what masked these 22 failures
> in Phase 105 ([[feedback_check_go_test_exit_not_pipe]]). Do NOT use the default
> 120 s timeout for the full package — it will spuriously time out on AWS calls.

---

## Sampling Rate

- **After every task commit:** Run the quick command scoped to that subsystem's
  `-run` regex; the targeted test(s) must show `ok` / `PASS` and `EXIT=0`.
- **After every plan wave:** Run the full suite command; the *failing set* must
  shrink and no NEW failures may appear.
- **Before `/gsd:verify-work`:** Full suite green — `ok ... internal/app/cmd` + `EXIT=0`.
- **Max feedback latency:** ~600 s (full suite); ~5–30 s per scoped subsystem run.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 107-shell-docker | shell-docker | 1 | TEST-HYGIENE-SHELL | unit | `go test ./internal/app/cmd/ -run 'TestShellDocker' -count=1 -timeout 600s` | ✅ | ⬜ pending |
| 107-email | email | 1 | TEST-HYGIENE-EMAIL | unit | `go test ./internal/app/cmd/ -run 'TestEmail(Send|Read)' -count=1 -timeout 600s` | ✅ | ⬜ pending |
| 107-uninit | uninit | 1 | TEST-HYGIENE-UNINIT | unit | `go test ./internal/app/cmd/ -run 'TestUninit' -count=1 -timeout 600s` | ✅ | ⬜ pending |
| 107-statebucket | statebucket | 1 | TEST-HYGIENE-STATEBUCKET | unit | `go test ./internal/app/cmd/ -run 'Test(List|Lock|Unlock|Status)Cmd' -count=1 -timeout 600s` | ✅ | ⬜ pending |
| 107-create | create | 1 | TEST-HYGIENE-CREATE | unit | `go test ./internal/app/cmd/ -run 'TestCreateDocker|TestApplyLifecycleOverrides' -count=1 -timeout 600s` | ✅ | ⬜ pending |
| 107-misc | misc | 1 | TEST-HYGIENE-MISC | unit | `go test ./internal/app/cmd/ -run 'TestRunAgentAuthClaude|TestAtList|TestLoadEFSOutputs|TestLearnOutputPath' -count=1 -timeout 600s` | ✅ | ⬜ pending |
| 107-shell-escalation | shell-escalation | 2 | TEST-HYGIENE-SHELL-FIX | unit + prod fix | `go test ./internal/app/cmd/ -run 'TestShellCmd_' -count=1 -timeout 600s` | ✅ | ⬜ pending |
| 107-green-gate | green-gate | 3 | TEST-HYGIENE-GREEN | full suite | `go test ./internal/app/cmd/ -count=1 -timeout 600s; echo EXIT=$?` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

*All target tests already EXIST (they currently fail) — this is reconcile-not-create.
No Wave 0 test scaffolding is required.*

---

## Wave 0 Requirements

*Existing infrastructure covers all phase requirements — all 22 tests already
exist and run. No stubs, fixtures, or framework install needed.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| No silent production-code edits | TEST-HYGIENE-TRIAGE | A diff-shape rule, not a unit test | `git diff --stat` on completion: only `*_test.go` files change, EXCEPT the single approved `shell.go` pre-flight-error fix (its own commit). Any other non-test `.go` change is a scope violation. |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (N/A — none)
- [ ] No watch-mode flags
- [ ] Feedback latency < 600s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** approved 2026-06-11 (plan-checker VERIFICATION PASSED, first iteration)
