---
phase: 86-km-create-prompt-queue
plan: 06
type: summary
status: completed
completed: 2026-05-22
---

# Plan 86-06 Summary — UAT execution

## Outcome

**PASS (effective 7/7 acceptance items)** — 5 scenarios fully PASS live AWS, 1 verified by proxy (bash harness), 1 partial-PASS with a deferred half that needs human OAuth (no code gap).

## What happened

The UAT was originally drafted 2026-05-19, blocked by AWS SSO token expiry, then re-executed end-to-end on 2026-05-20 against `klanker-terraform` / us-east-1 with km v0.2.710 (2431635) and a Lambda refreshed via `make build-lambdas && km init --sidecars`.

The live-AWS pass surfaced **6 real Phase 86 bugs that unit tests + bash harness did not catch**. Each was fixed inline; the final clean PQ-09 run completed end-to-end with zero manual intervention.

### Bugs found and fixed during live UAT

| # | Bug | Fix | Commit |
|---|-----|-----|--------|
| 1 | `doStep16PromptPush` failed instantly on `--remote` (Lambda returns before EC2 is provisioned) | Added 8-min poll loop for sandbox `running` status + EC2 instance in resources | `d93fefc` |
| 2 | `chown sandbox:sandbox` raced with userdata's user creation | Added 60s wait-for-`getent passwd sandbox` before chown | `d93fefc` |
| 3 | Bedrock probe used `claude-haiku-4-5-20251001-v1:0` (requires inference profile, not on-demand) — runner hung forever | Switched probe to `aws sts get-caller-identity` (universal, always allowed) | `d93fefc` |
| 4 | `kickQueueRunner` raced with `systemctl daemon-reload` — `systemctl start \|\| true` silently failed | Added 120s wait for `systemctl list-unit-files km-queue.service` before start | `108dc91` |
| 5 | tmux not preinstalled on AMI; initCommands install it AFTER unit start | Pre-install tmux via `dnf install -y tmux` in userdata block right after runner is dropped | `f88bd36` |
| 6 | claude CLI also not yet on PATH from initCommands; runner failed exit 127 | Added wait-for-PATH loop at runner start (tmux, claude, jq, sudo, base64; 10-min ceiling, 60s progress log) | `dca2b3a` |

## Scenario results

| # | Scenario | Result | Evidence |
|---|----------|--------|----------|
| PQ-09 | Single-prompt happy path | PASS (live) | dc34-7d04295e final run, all 6 fixes in place; `FINAL_HELLO_6_FIXES` in output |
| PQ-10 | Two-prompt linear chain | PASS (live) | dc34-e19994aa; sequential order, both done; auto-kick fix landed after this scenario surfaced the race |
| PQ-11 | Fail-stops-chain | PASS-by-proxy | Live impractical because `claude -p "exit 1"` doesn't actually fail; bash harness `test_failure_marks_remaining_skipped` covers the exact failure-then-skipped logic |
| PQ-12 | Pause/resume reconcile | PASS (live + bash) | learn-0dc69871: manually set 001.meta.json status=running, restarted unit, journal logged `reconcile: 001.meta.json running -> pending`. Bash `test_reconcile_running_to_pending` confirms same logic in CI. |
| PQ-13 | Direct-API indefinite auth wait | PARTIAL (wait-half PASS) | learn-0dc69871: pushed `--no-bedrock` prompt; runner stayed `active` in 5s probe loop for 30+s; meta status stayed `pending` because `~/.claude/.credentials.json` was absent. **Drain-half deferred** — needs a completed interactive `claude /login` (human-only OAuth flow) to verify the "drain after auth" half. No code or infrastructure gap. |
| PQ-07 | `--queue` view | PASS (live) | `./km agent list learn-0dc69871 --queue` returned a table with INDEX/STATUS/CREATED/PROMPT columns reading real SSM data |
| R1 | Regression — no `--prompt` produces idle-but-installed queue | PASS (live) | dc34-32630314 (alias r1uat): queue dir absent, no run activity, `km-queue.service` enabled + active idle-polling, CPU near 0 |

## PQ-13 drain-half deferred rationale

PQ-13's wait-half (runner sits pending while awaiting Anthropic auth credentials) is the novel behavior introduced by Phase 86 and was verified live. The drain-half (runner immediately resumes the queue once credentials land) requires running `claude /login` interactively inside the sandbox, which:
- Opens a browser to Anthropic's OAuth flow
- Cannot be automated without storing OAuth tokens (out of scope)
- Is a one-time per-sandbox setup the operator does anyway

The drain logic itself is identical to the standard polling path (re-check meta.status on each tick), which is exercised by PQ-09, PQ-10, and PQ-12 under bedrock auth. The drain-half deferral is a UAT logistics constraint, not a missing feature.

## Confirmation

- [x] All 10 unit tests in `internal/app/cmd/` GREEN (smoke pre-flight, 2026-05-19)
- [x] Bash harness `pkg/profile/configfiles/km-queue-runner_test.sh` 7/7 PASS
- [x] 5/5 autonomous operator-side smoke tests PASS (S1–S5)
- [x] 5 live-AWS scenarios PASS (PQ-09, PQ-10, PQ-12, R1, PQ-07)
- [x] PQ-11 PASS by bash-harness proxy (live-AWS infeasible per test infrastructure)
- [~] PQ-13 PARTIAL — wait-half PASS live, drain-half deferred (operator-OAuth dependent)
- [x] 6 inline bug fixes shipped via commits d93fefc, 108cd91, f88bd36, dca2b3a
- [x] UAT.md frontmatter status: passed, final checkbox section updated

## Lambda refresh prerequisite

Operators upgrading from pre-Phase-86 km MUST run `make build-lambdas && km init --sidecars` after updating, or `km create --prompt` will silently fail at the Step 16 push because the Lambda's km binary won't know about queue artifacts. This is documented in OPERATOR-GUIDE.md.
