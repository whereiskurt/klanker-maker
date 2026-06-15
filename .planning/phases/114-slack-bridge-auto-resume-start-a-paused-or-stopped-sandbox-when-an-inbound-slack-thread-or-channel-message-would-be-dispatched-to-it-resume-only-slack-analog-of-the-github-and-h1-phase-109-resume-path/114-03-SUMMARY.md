---
phase: 114-slack-bridge-auto-resume
plan: "03"
subsystem: cmd/km-slack-bridge + infra/modules/lambda-slack-bridge + docs
tags: [ec2-resume, slack-bridge, iam, phase-114, wiring, docs]
dependency_graph:
  requires:
    - Plan 01: EC2Resumer, DynamoSandboxStatusWriter, ErrNoResumableInstance (pkg/slack/bridge)
    - Plan 02: EventsHandler.Resumer/StatusWriter/OrphanHinter fields + synchronous step-9
  provides:
    - Production Lambda wiring: initEC2Client + EC2Resumer + DynamoSandboxStatusWriter + orphan DDBPauseHinter
    - aws_iam_role_policy.ec2_resume on lambda-slack-bridge role (ec2:DescribeInstances + ec2:StartInstances)
    - docs/slack-notifications.md § Phase 114 operator runbook
  affects:
    - cmd/km-slack-bridge/main.go (EC2 client + wiring)
    - infra/modules/lambda-slack-bridge/v1.0.0/main.tf (additive IAM policy)
    - docs/slack-notifications.md (new § Phase 114 section)
tech_stack:
  added:
    - github.com/aws/aws-sdk-go-v2/service/ec2 (new import to cmd/km-slack-bridge)
  patterns:
    - initEC2Client package-level var initialized in init() — same pattern as initDDB/initSSMC/initS3Client/initSQSClient
    - wireEventsHandler() assigns Resumer/StatusWriter/OrphanHinter after PauseHinter block
    - aws_iam_role_policy.ec2_resume mirrors lambda-github-bridge/v1.1.0/main.tf:205-239 verbatim
    - PauseHinter HintText updated to resume-aware "waking up" message
key_files:
  modified:
    - cmd/km-slack-bridge/main.go
    - infra/modules/lambda-slack-bridge/v1.0.0/main.tf
    - docs/slack-notifications.md
decisions:
  - "initEC2Client constructed in init() alongside other AWS clients (cfg is local to init(); not available in wireEventsHandler)"
  - "Unconditional wiring — no env guard; IAM grant is always present after ec2_resume policy applies; binary is fail-soft without the grant (UnauthorizedOperation falls through)"
  - "PauseHinter HintText updated from 'message queued, run km resume' to 'waking up' — semantically correct now that the bridge auto-resumes"
  - "OrphanHinter is a distinct DDBPauseHinter from PauseHinter — same 1h cooldown, different text, wired to eventsHandler.OrphanHinter"
  - "No module version bump on lambda-slack-bridge/v1.0.0 — additive policy edit in place (same pattern as GitHub bridge additions)"
  - "Deploy surface: make build-lambdas + km init --slack (NOT --sidecars) — IAM role only updates on full terragrunt apply"
metrics:
  duration: "666s"
  completed_date: "2026-06-15"
  tasks_completed: 2
  files_modified: 3
  files_created: 0
---

# Phase 114 Plan 03: Lambda Wiring, IAM, and Docs Summary

**One-liner:** Wired EC2Resumer + DynamoSandboxStatusWriter + orphan DDBPauseHinter into the km-slack-bridge Lambda main.go, granted ec2:StartInstances (resource-prefix-scoped) on the bridge IAM role, and documented the feature + deploy surface in docs/slack-notifications.md § Phase 114.

## What Was Built

### Task 1: EC2 client + wiring in cmd/km-slack-bridge/main.go (6ceb6591)

**`cmd/km-slack-bridge/main.go`** — three changes:

1. **New import:** `"github.com/aws/aws-sdk-go-v2/service/ec2"` added to the AWS SDK import group (alphabetically between `dynamodb` and `s3`).

2. **New package-level var:** `initEC2Client *ec2.Client` added to the `var` block alongside `initDDB`, `initSSMC`, etc. Constructed in `init()` immediately after the DDB/SSM clients:
   ```go
   initEC2Client = ec2.NewFromConfig(cfg)
   ```

3. **wireEventsHandler() additions** — after the PauseHinter block (line ~324), three assignments added:
   ```go
   eventsHandler.Resumer = &bridge.EC2Resumer{Client: initEC2Client, ResourcePrefix: prefix}
   eventsHandler.StatusWriter = &bridge.DynamoSandboxStatusWriter{Client: initDDB, TableName: sandboxesTable}
   eventsHandler.OrphanHinter = &bridge.DDBPauseHinter{...HintText: "Couldn't auto-resume..."}
   ```

4. **PauseHinter HintText updated** from `"Sandbox is paused; message queued. Run \`km resume\` to wake it up."` to `"Sandbox is waking up — your message is queued and will be answered shortly."` — semantically accurate now that the bridge auto-resumes.

### Task 2: IAM policy + docs (b20c9fde)

**`infra/modules/lambda-slack-bridge/v1.0.0/main.tf`** — additive `aws_iam_role_policy.ec2_resume` inserted after `dynamodb_sandboxes_pause_hint` (line ~215):
- `EC2DescribeInstances`: `ec2:DescribeInstances` on `*` (Describe has no resource-level conditions)
- `EC2StartInstances`: `ec2:StartInstances` on `arn:aws:ec2:{region}:{account}:instance/*` conditioned on `aws:ResourceTag/km:resource-prefix == var.resource_prefix`

Near-verbatim mirror of `infra/modules/lambda-github-bridge/v1.1.0/main.tf:205-239`.
No new TF variable — `var.resource_prefix` already existed at `variables.tf:61`.

**`docs/slack-notifications.md`** — new `## Phase 114 — Slack bridge auto-resume` section appended (after Phase 111), covering:
- What it does (resume-only, Slack analog of GitHub/H1 Phase-109)
- Trigger gate (fires only at step-9 paused branch, after mention-only/enqueue)
- Wake UX (two hint variants: waking-up vs orphan/degraded)
- Synchronous design note (Phase 75.2 Lambda-freeze lesson; 15s sub-context)
- Back-compat invariant (nil Resumer = byte-identical to pre-Phase-114)
- IAM grants table (Describe on *, Start conditioned on resource-prefix tag)
- Deploy surface: `make build-lambdas` + `km init --slack` (**NOT `--sidecars`**)
- No schema change, no sandbox recreate
- E2E UAT checklist (paused, stopped, orphan, warm regression, mention-only guard)
- Troubleshooting table

## Verification

```
go build ./cmd/km-slack-bridge/...  → ok (exit 0)
go vet ./cmd/km-slack-bridge/...    → ok (exit 0)
make build                          → km v0.4.968 (exit 0)
go test ./... -count=1 -timeout 600s → all ok (exit 0, full green suite)
grep "Phase 114" docs/slack-notifications.md → 3 matches
grep "ec2:StartInstances" infra/modules/lambda-slack-bridge/v1.0.0/main.tf → 1 match
```

## Deviations from Plan

None — plan executed exactly as written. All field names, struct references, and IAM policy shape match the plan spec verbatim.

## Self-Check: PASSED

- FOUND: cmd/km-slack-bridge/main.go (EC2 client + Resumer/StatusWriter/OrphanHinter wiring)
- FOUND: infra/modules/lambda-slack-bridge/v1.0.0/main.tf (ec2_resume policy)
- FOUND: docs/slack-notifications.md (§ Phase 114 section)
- FOUND commit: 6ceb6591 (Task 1 — EC2 wiring)
- FOUND commit: b20c9fde (Task 2 — IAM policy + docs)
