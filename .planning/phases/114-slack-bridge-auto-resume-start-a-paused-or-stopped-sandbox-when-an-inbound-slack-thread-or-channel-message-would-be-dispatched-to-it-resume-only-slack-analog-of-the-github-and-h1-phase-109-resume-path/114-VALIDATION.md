---
phase: 114
slug: slack-bridge-auto-resume
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-15
---

# Phase 114 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (table-driven) |
| **Config file** | none — existing Go module |
| **Quick run command** | `go test ./pkg/slack/bridge/... -count=1 -timeout 300s` |
| **Full suite command** | `go test ./... -count=1 -timeout 600s` |
| **Estimated runtime** | ~10s (pkg/slack/bridge) · ~3–5min (full suite) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/slack/bridge/... -count=1 -timeout 300s`
- **After every plan wave:** Run `go test ./... -count=1 -timeout 600s` (whole-repo suite is green as of 2026-06-13 — a FAIL means a real regression). **Check the command's own exit code, not a piped `tail`** (memory: `feedback_check_go_test_exit_not_pipe`).
- **Before `/gsd:verify-work`:** Full suite green + `make build` + `make build-lambdas` succeed.
- **Max feedback latency:** ~15 seconds (pkg-scoped run)

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 114-01-01 | 01 | 1 | EC2Resumer + ErrNoResumableInstance port | unit | `go test ./pkg/slack/bridge/ -run EC2Resumer -count=1` | ❌ W0 | ⬜ pending |
| 114-01-02 | 01 | 1 | SandboxStatusWriter.SetStatusRunning (UpdateItem, not PutItem) | unit | `go test ./pkg/slack/bridge/ -run StatusWriter -count=1` | ❌ W0 | ⬜ pending |
| 114-02-01 | 02 | 2 | EventsHandler step-9 resume branch (paused→resume+flip+enqueue) | unit | `go test ./pkg/slack/bridge/ -run Resume -count=1` | ❌ W0 | ⬜ pending |
| 114-02-02 | 02 | 2 | Orphan path: ErrNoResumableInstance→degraded hint, no flip, still enqueue | unit | `go test ./pkg/slack/bridge/ -run Orphan -count=1` | ❌ W0 | ⬜ pending |
| 114-02-03 | 02 | 2 | Back-compat: nil Resumer byte-identical (pause-hint only) | unit | `go test ./pkg/slack/bridge/ -run NilResumer -count=1` | ❌ W0 | ⬜ pending |
| 114-02-04 | 02 | 2 | StartSandbox called SYNCHRONOUSLY (not frozen in goroutine) | unit | `go test ./pkg/slack/bridge/ -run Resume -count=1` | ❌ W0 | ⬜ pending |
| 114-03-01 | 03 | 3 | Wiring: EC2 client + Resumer + StatusWriter assigned in main.go | build | `go build ./cmd/km-slack-bridge/...` | ✅ | ⬜ pending |
| 114-03-02 | 03 | 3 | IAM: ec2:DescribeInstances + ec2:StartInstances grant added | manual | terragrunt plan / `km init --slack --dry-run` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/slack/bridge/aws_adapters_resume_test.go` — EC2Resumer + SetStatusRunning unit tests (mock EC2 + DDB API)
- [ ] `pkg/slack/bridge/events_handler_resume_test.go` — handler resume-branch tests (mock Resumer/StatusWriter), mirror `pkg/github/bridge/webhook_handler_phase109_test.go`

*Existing test infrastructure (go test + the github phase109 test as a template) covers all phase requirements; no new framework install.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Real EC2 `StartInstances` succeeds under the new IAM grant | ec2_resume policy | Go goldens cannot exercise the deployed Lambda role / real EC2 API (memory: deploy-surface needs live UAT) | After `make build-lambdas` + `km init --slack`: `km pause <slack-sandbox>`, post in its channel, observe `StartInstances` in CloudWatch bridge logs + instance starting in AWS console. |
| `km-sandboxes.state` flips to `running` after resume | SetStatusRunning | Requires live DDB write from the deployed Lambda | `km list` / `km status <id>` shows running after the posted message. |
| Agent reply lands after poller drains the pre-pause FIFO backlog | end-to-end | Requires real poller boot + SQS drain | Observe the agent's threaded reply in Slack; confirm via `km otel <id>`. |
| Both `paused` (hibernate) and `stopped` states resume | scope: both states | Two distinct lifecycle states; only live AWS exercises them | Repeat the UAT once after `km pause` and once after `km stop`. |
| No resume on running sandbox / non-dispatched message | trigger gate | Negative path needs live confirmation | Post in a running sandbox's channel and a non-mention in a mention-only channel; confirm no `StartInstances`. |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
