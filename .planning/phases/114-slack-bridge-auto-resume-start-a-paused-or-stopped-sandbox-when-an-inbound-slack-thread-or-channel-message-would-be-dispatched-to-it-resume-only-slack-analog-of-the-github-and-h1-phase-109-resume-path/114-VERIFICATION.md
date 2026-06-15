---
phase: 114-slack-bridge-auto-resume
verified: 2026-06-15T00:00:00Z
status: passed
score: 13/13 must-haves verified
human_verification:
  - test: "km pause <sandbox-id> on a running Slack-bound sandbox, then @-mention it in its Slack channel"
    expected: "Bridge calls ec2:StartInstances; Slack thread shows 'Sandbox is waking up' hint; after boot, the on-box poller drains the enqueued message and the agent replies"
    why_human: "Requires live AWS EC2 + Slack credentials, real IAM grant (ec2_resume policy applied via km init --slack), and a real km-slack-bridge Lambda deployment"
  - test: "Manually terminate a sandbox's EC2 instance (leaving the DDB row in paused state), then @-mention it in Slack"
    expected: "Bridge posts 'Couldn't auto-resume this sandbox (the instance is gone)' hint; SetStatusRunning NOT called; message still enqueued in SQS"
    why_human: "Requires a deliberately orphaned DDB row and live AWS to observe the orphan-hinter path vs the resume path"
  - test: "km stop <sandbox-id> (stopped state, not paused), then @-mention it in Slack"
    expected: "Same auto-resume behavior as paused — bridge starts the stopped instance"
    why_human: "Verifies the stopped-state filter (stopped + stopping) hits the same code path; requires live EC2"
  - test: "Send a non-mention message to the sandbox Slack channel while mention_only mode is active"
    expected: "No StartInstances call; box is NOT woken; message NOT enqueued (mention-only guard fires at step 5b before step 9)"
    why_human: "Requires a live configured bridge with KM_SLACK_MENTION_ONLY=true and a paused sandbox"
  - test: "Send a message to a running sandbox's Slack channel after Phase 114 deploy"
    expected: "Warm path unchanged: enqueue + 200; NO StartSandbox call; no regression in latency or behavior"
    why_human: "Warm-path regression check; requires live bridge with real traffic"
---

# Phase 114: Slack Bridge Auto-Resume Verification Report

**Phase Goal:** When an inbound Slack thread/channel message would be dispatched to a paused/stopped sandbox, the km-slack-bridge Lambda starts the EC2 instance (resume-only; the message is already enqueued; the on-box poller drains it on boot). Slack analog of the GitHub/H1 Phase-109 resume path. No cold-create, no budget logic, no schema change.

**Verified:** 2026-06-15T00:00:00Z
**Status:** passed (all automated checks passed; live E2E completed 2026-06-15 on install `km`)

### Live E2E results (2026-06-15)

Run against a `learn.v2.polite` sandbox (`learnpolite-2de158d3`, channel `C0BAHSYH4LB`)
using HMAC-signed synthetic Slack Events webhooks (bot messages are dropped by the bridge,
so a signed human-impersonating `event_callback` is the only self-driven trigger).

- **#1 paused → resume:** PASS — `ec2:StartInstances` fired under the new grant; DDB
  `status` → `running`; "Sandbox is waking up…" hint posted; box booted; on-box poller
  (`km-slack-inbound-poller.service`) drained the FIFO and dispatched the agent.
- **#2 stopped → resume:** PASS — `km stop` (`status=stopped`) then trigger; instance
  relaunched (LaunchTime 17:29:19Z), `status` → `running`. Second waking hint correctly
  suppressed by the 1h `DDBPauseHinter` cooldown (`last_pause_hint_ts`).
- **#4 warm regression (running):** PASS — message to a running box triggered no resume,
  no hint, no status change.
- **Bug found + fixed during E2E:** `FetchByChannel` read the wrong km-sandboxes attribute
  (`state` instead of `status`), so `info.Paused` was always false in production — a latent
  Phase-67 bug that also silently disabled the pause-hint. Fixed (read `status`), test
  rewritten table-driven; bridge rebuilt + redeployed. Commit on branch.

**Orthogonal observation (not a Phase-114 defect):** the agent turn failed with `claude`
exit 127 (binary not found on this learn.v2.polite box), so no agent *reply* posted. The
resume feature's responsibility — wake the box and let the poller drain the queue —
completed correctly. Item #3 (orphan degraded hint) and #5 (mention-only guard) remain
covered by unit tests; not separately exercised live.
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | EC2Resumer.sandboxIDTagKey() returns hardcoded "km:sandbox-id" (Phase-109 fix carried) | VERIFIED | `aws_adapters.go:1521-1533`: explicit `return "km:sandbox-id"` with Phase-109 comment; ResourcePrefix is inert |
| 2 | ErrNoResumableInstance sentinel exists and is wrapped on zero-instance path | VERIFIED | `aws_adapters.go:1510`: `var ErrNoResumableInstance = errors.New("slack-bridge: no resumable EC2 instance")`; wrapped at line 1578 via `%w` |
| 3 | DynamoSandboxStatusWriter.SetStatusRunning uses UpdateItem, never PutItem | VERIFIED | `aws_adapters.go:1676-1695`: only UpdateItem call; comment at 1656 explicitly excludes PutItem; test `TestDynamoSandboxStatusWriter_UsesUpdateItem` asserts putCalled==false |
| 4 | No DeleteSandboxRow on DynamoSandboxStatusWriter (Slack has no cold-create) | VERIFIED | `aws_adapters.go:1665-1695`: struct has only SetStatusRunning; no DeleteSandboxRow method anywhere in slack bridge aws_adapters.go |
| 5 | SandboxResumer + SandboxStatusWriter interfaces exist in the slack bridge package | VERIFIED | `events_interfaces.go:170-185`: both interfaces declared with correct method signatures |
| 6 | Step-9 paused branch calls h.Resumer.StartSandbox SYNCHRONOUSLY (not in goroutine) | VERIFIED | `events_handler.go:470-521`: synchronous block with explicit "SYNCHRONOUS (not a goroutine)" comment; `TestEventsHandler_PausedSandbox_ResumeIsSynchronous` asserts call counts immediately after Handle returns with NO sleep |
| 7 | On errors.Is(ErrNoResumableInstance): OrphanHinter called, SetStatusRunning NOT called | VERIFIED | `events_handler.go:487-495`: ErrNoResumableInstance branch calls OrphanHinter only; else branch (line 496+) handles SetStatusRunning |
| 8 | On transient error: SetStatusRunning called optimistically, PauseHinter called, no crash | VERIFIED | `events_handler.go:496-511`: else branch (both success and transient error) calls StatusWriter + PauseHinter |
| 9 | nil Resumer is byte-identical to pre-Phase-114 (pause-hint only) | VERIFIED | `events_handler.go:513-519`: nil Resumer falls through to `else if h.PauseHinter != nil` branch; `TestEventsHandler_NilResumer_PauseHintOnly` confirms |
| 10 | Message enqueue at step 8 is unchanged and happens before step 9 in all paths | VERIFIED | `events_handler.go:460-467`: SQS send at step 8; step 9 starts at line 470; all 6 resume tests assert `len(sqs.sends) == 1` |
| 11 | EC2 client constructed in init() and Resumer/StatusWriter/OrphanHinter wired in main.go | VERIFIED | `cmd/km-slack-bridge/main.go:67,84`: `initEC2Client *ec2.Client` var + `ec2.NewFromConfig(cfg)` in init(); wiring at lines 334-352 |
| 12 | IAM ec2_resume policy grants ec2:DescribeInstances on * and ec2:StartInstances conditioned on km:resource-prefix tag | VERIFIED | `infra/modules/lambda-slack-bridge/v1.0.0/main.tf:223-249`: additive `aws_iam_role_policy.ec2_resume`; resource_prefix var at variables.tf:61 (pre-existing, no new var added); no new DDB grant |
| 13 | docs/slack-notifications.md has Phase 114 section documenting make build-lambdas + km init --slack (NOT --sidecars) | VERIFIED | `docs/slack-notifications.md:2629-2705`: full section present; line 2692-2693 shows `make build-lambdas` + `km init --slack`; line 2698 explicitly says "NOT --sidecars" |

**Score:** 13/13 truths verified

### Required Artifacts

| Artifact | Status | Details |
|----------|--------|---------|
| `pkg/slack/bridge/events_interfaces.go` | VERIFIED | SandboxResumer (line 174) + SandboxStatusWriter (line 183) declared; no DeleteSandboxRow |
| `pkg/slack/bridge/aws_adapters.go` | VERIFIED | EC2StartAPI, ErrNoResumableInstance, EC2Resumer (hardcoded km:sandbox-id), DynamoSandboxStatusWriter (UpdateItem-only) all present |
| `pkg/slack/bridge/aws_adapters_resume_test.go` | VERIFIED | 304 lines; 5 tests: tag-key, no-instances sentinel, stopped-instance starts, transient-error non-sentinel, UpdateItem-no-PutItem |
| `pkg/slack/bridge/events_handler.go` | VERIFIED | EventsHandler.Resumer / .StatusWriter / .OrphanHinter fields (lines 117,121,125); synchronous step-9 resume-or-hint branch (lines 470-521) |
| `pkg/slack/bridge/events_handler_resume_test.go` | VERIFIED | 449 lines; 6 tests including the synchronous-guard test with no sleep |
| `cmd/km-slack-bridge/main.go` | VERIFIED | initEC2Client constructed in init(); EC2Resumer + DynamoSandboxStatusWriter + second DDBPauseHinter wired in wireEventsHandler; updated wake hint text |
| `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` | VERIFIED | aws_iam_role_policy.ec2_resume block present; uses existing var.resource_prefix (no new TF var); attached to aws_iam_role.slack_bridge.id |
| `docs/slack-notifications.md` | VERIFIED | Section at line 2629; covers trigger gate, wake UX, synchronous design, back-compat, IAM, deploy surface |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `EC2Resumer.sandboxIDTagKey()` | `tag filter on DescribeInstances` | hardcoded "km:sandbox-id" | VERIFIED | `aws_adapters.go:1521-1533`; test `TestEC2Resumer_UsesKmSandboxIdTag` asserts with non-km ResourcePrefix |
| `DynamoSandboxStatusWriter.SetStatusRunning` | `km-sandboxes row status attribute` | UpdateItem with `SET #st = :running` | VERIFIED | `aws_adapters.go:1677-1690`; test asserts UpdateExpression, ExpressionAttributeNames, ExpressionAttributeValues |
| `EventsHandler.Handle step-9 paused branch` | `h.Resumer.StartSandbox(ctx, info.SandboxID)` | synchronous call before 200 return | VERIFIED | `events_handler.go:485-486`; no goroutine; synchronous-guard test asserts without sleep |
| `successful/transient resume` | `h.StatusWriter.SetStatusRunning(ctx, info.SandboxID)` | fail-soft synchronous call | VERIFIED | `events_handler.go:502-505`; else branch (success or transient) |
| `ErrNoResumableInstance` | `h.OrphanHinter.PostIfCooldownExpired` | errors.Is branch, no status flip | VERIFIED | `events_handler.go:487-495` |
| `cmd/km-slack-bridge/main.go init()` | `initEC2Client = ec2.NewFromConfig(cfg)` | package-level var alongside initDDB | VERIFIED | `main.go:67,84` |
| `wireEventsHandler()` | `eventsHandler.Resumer / .StatusWriter / .OrphanHinter` | EC2Resumer + DynamoSandboxStatusWriter + DDBPauseHinter | VERIFIED | `main.go:334-352` |
| `aws_iam_role_policy.ec2_resume` | `aws_iam_role.slack_bridge.id` | additive role policy, resource-prefix tag condition | VERIFIED | `main.tf:223-249` |

### Requirements Coverage

No requirements formally registered in REQUIREMENTS.md for this phase. All must_haves drawn from PLAN frontmatter (Plans 01, 02, 03). All 13 must-haves verified.

### Anti-Patterns Found

None. No TODO/FIXME/placeholder comments found in modified files. No stub implementations. No empty returns. All paths covered by tests.

### Human Verification Required

#### 1. Happy-Path Auto-Resume (Paused Sandbox)

**Test:** `km pause <sandbox-id>` on a running Slack-bound sandbox, then @-mention it in its Slack channel.
**Expected:** Bridge calls `ec2:StartInstances`; Slack thread shows "Sandbox is waking up — your message is queued and will be answered shortly."; after EC2 boot completes and the on-box poller starts, the agent reads the queued message and replies in the thread.
**Why human:** Requires live AWS EC2 + Slack credentials, deployed Lambda binary (post `make build-lambdas`), and IAM grant applied via `km init --slack --dry-run=false`.

#### 2. Stopped-State Auto-Resume

**Test:** `km stop <sandbox-id>` (EC2 `stopped` state, not hibernated), then @-mention in Slack.
**Expected:** Same resume behavior as paused — bridge starts the stopped instance; SQS already enqueued the message; agent replies after boot.
**Why human:** Requires live EC2 in stopped state; validates the `stopped` + `stopping` filter in `StartSandbox`.

#### 3. Orphan Row Degraded Hint

**Test:** Manually terminate the sandbox's EC2 instance out-of-band (leaving the DDB row with `status=paused`), then @-mention in Slack.
**Expected:** Bridge posts "Couldn't auto-resume this sandbox (the instance is gone). Ask an operator to recreate it with `km create`."; `SetStatusRunning` NOT called; message still enqueued in SQS queue; no bridge crash.
**Why human:** Requires a deliberately orphaned DDB row and live AWS to observe the `ErrNoResumableInstance` → OrphanHinter path.

#### 4. Mention-Only Guard (Idle Chatter Does Not Wake Box)

**Test:** With `KM_SLACK_MENTION_ONLY=true`, send a non-@-mention message to the sandbox's Slack channel while the sandbox is paused.
**Expected:** Bridge does not call `StartInstances`; box is not woken; no SQS enqueue; no hint posted.
**Why human:** Requires live bridge with mention-only mode active and a paused sandbox; verifies the trigger gate at step 5b fires before step 9.

#### 5. Warm-Path Regression (Running Sandbox Unaffected)

**Test:** Send messages to a running sandbox's Slack channel after Phase 114 deploy.
**Expected:** Warm path unchanged: message enqueued, 👀 reaction, 200 response; no `StartSandbox` call; no regression in latency or behavior.
**Why human:** Production regression check for the most common code path; requires live bridge traffic.

### Gaps Summary

No gaps. All automated checks passed:

- `go test ./pkg/slack/bridge/ -count=1 -timeout 300s` → **GREEN** (`ok github.com/whereiskurt/klanker-maker/pkg/slack/bridge 5.455s`)
- All 13 must-haves from Plans 01/02/03 frontmatter verified against actual codebase
- The hardcoded `km:sandbox-id` tag key (Phase-109 fix) is present and proven by `TestEC2Resumer_UsesKmSandboxIdTag`
- No DeleteSandboxRow / no cold-create path (Slack bridge correctly omits the GitHub/H1 orphan-delete step)
- Synchronous execution guard proven by `TestEventsHandler_PausedSandbox_ResumeIsSynchronous` (no sleep, call counts asserted immediately after Handle returns)
- IAM policy is additive, no new TF variable, no new DDB grant, no schema change, no sandbox recreate required

Awaiting operator-driven live E2E UAT per `114-VALIDATION.md`.

---

_Verified: 2026-06-15T00:00:00Z_
_Verifier: Claude (gsd-verifier)_
