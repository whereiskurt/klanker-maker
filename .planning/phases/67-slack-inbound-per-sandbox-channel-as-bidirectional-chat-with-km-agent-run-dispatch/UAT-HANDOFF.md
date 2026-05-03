# Phase 67 UAT — Mid-Run Handoff (2026-05-02)

**Branch:** `gsd/phase-67-slack-inbound`

## Where we are

UAT in progress. Stopped at Step 5 (`km create`) on attempt 4 (alias `l5`)
after discovering an architectural gap: Phase 67's queue-URL injection
uses `ssm:SendCommand`, which is blocked by an org-level SCP
(`p-cvd490xt`) on the application account.

## What's already verified ✅

- Step 1 (Terraform): `km-slack-threads` table created; `km-sandboxes` GSI
  `slack_channel_id-index` added; `lambda-slack-bridge` IAM has 4 new
  policies (DDB threads, DDB sandboxes pause-hint, SQS inbound, SSM
  signing-secret).
- Step 2 (signing secret): persisted at `/km/slack/signing-secret` SSM
  SecureString.
- Step 3 (Slack Events URL + scopes): operator pasted
  `https://6ov5pfv6ml3fjo66liqljsyazi0hsaad.lambda-url.us-east-1.on.aws/events`
  and Slack reported Verified. Bot has `channels:history` + `groups:history`.
- Step 4 (`km doctor`): all 26 checks passed, three new Phase 67 checks
  all green.
- Phase 67 SQS create works (verified during attempt #4 — queue
  `km-slack-inbound-lrn2-9996b429.fifo` was created before the SCP block hit).

## Gaps committed during UAT

| Commit | Fix |
|--------|-----|
| `181a21a` | create-handler IAM: `sqs:CreateQueue` (Phase 67-06 missed this) |
| `8b601c7` | create-handler IAM: `ssm:SendCommand` (won't help — see below) |

## Gap that needs Option A (architectural fix)

**Root cause:** `provisionSlackInboundQueue` in
`internal/app/cmd/create_slack_inbound.go:127` calls
`deps.InjectEnvVar(...)` which uses `ssm:SendCommand` to push
`KM_SLACK_INBOUND_QUEUE_URL` into the running sandbox's
`/etc/profile.d/km-notify-env.sh`. The org SCP `p-cvd490xt` denies
`ssm:SendCommand` for application-account roles regardless of IAM.

**Fix plan (clean room after `/compact`):**

1. **`internal/app/cmd/create_slack_inbound.go`** (~30 lines):
   - Add `PutSSMParameter func(ctx, name, value string) error` to
     `slackInboundDeps` struct.
   - Replace line 127 `deps.InjectEnvVar(ctx, deps.InstanceID, "KM_SLACK_INBOUND_QUEUE_URL", queueURL)`
     with
     `deps.PutSSMParameter(ctx, "/sandbox/" + deps.SandboxID + "/slack-inbound-queue-url", queueURL)`.
   - Update success log line to say "Parameter Store" instead of
     "sandbox env".
   - Keep `InjectEnvVar` and `InstanceID` for now (other consumers may
     still use them; can be cleaned up in a follow-up).

2. **`internal/app/cmd/create.go`** (~5 lines around line 889):
   - Construct a `productionSSMParamStore` (already used elsewhere) and
     wire `PutSSMParameter: store.Put` into the `slackInboundDeps`
     literal.

3. **`pkg/compiler/userdata.go`** (poller bash script around line 873):
   - When `KM_SLACK_INBOUND_QUEUE_URL` env var is empty, fall back to
     `aws ssm get-parameter --name /sandbox/${KM_SANDBOX_ID}/slack-inbound-queue-url`.
   - Set retry/backoff so a brief race (SSM param not yet written) is
     non-fatal.

4. **Sandbox EC2 IAM** (`infra/modules/ec2spot/v1.0.0/main.tf`):
   - Verify the existing SSM Parameter Store policy covers
     `/sandbox/{sandbox-id}/*`. If not, add it (likely already there
     from Phase 14 sandbox identity work — verify, don't duplicate).

5. **`internal/app/cmd/create_slack_inbound_test.go`**:
   - Replace `InjectEnvVar` mock with `PutSSMParameter` mock in fake
     deps.

6. **Cleanup at destroy:** `internal/app/cmd/destroy_slack_inbound.go`
   should `ssm:DeleteParameter` on `/sandbox/{id}/slack-inbound-queue-url`
   alongside the queue delete + thread row cleanup. Probably 5 lines.

## Other gaps logged for `/gsd:plan-phase 67 --gaps` after UAT

- **G1 (committed):** create-handler missing `sqs:CreateQueue` — fixed
  in `181a21a`.
- **G2 (committed but blocked by SCP):** create-handler missing
  `ssm:SendCommand` — fixed in `8b601c7` but redundant once Option A
  lands.
- **G3:** `dynamodb-slack-threads` not in `km init` apply list
  (`internal/app/cmd/init.go`).
- **G4:** Plan 67-10 generated `test/e2e/slack/profiles/inbound-e2e.yaml`
  with the wrong `apiVersion` (`klanker.io/v1` vs the project's
  `klankermaker.ai/v1alpha1`) and uses `ttlMinutes` instead of `ttl`.
- **G5:** Phase 67 rollback after queue-create failure didn't archive
  the Slack channel (Plan 67-06 spec required this; impl missing).
  Caused 3 orphan channels in workspace: `#sb-l2`, `#sb-l4`, `#sb-l5`.
- **G6:** `km destroy` archive step is skipped when the bot can't post
  the final message (`not_in_channel`). Archive should still proceed —
  it's a separate API call.
- **G7:** `terragrunt apply` from raw shell (vs. `km init`) silently
  deploys empty `artifact_bucket_arn` because the Terragrunt config
  reads `KM_ARTIFACTS_BUCKET` env var. We hit this — corrupted the
  create-handler S3 IAM policy. Recovery: re-run with env var set.
  Possible follow-up: fail-fast in Terragrunt when env is empty.
- **G8 (architectural):** Phase 67 inbound provisioning is fatal-on-
  injection-failure. Phase 63 mirrors it as non-fatal with retry. Make
  Phase 67 non-fatal too (warn + continue; the env-file placeholder is
  acceptable for the queue-URL since the poller can read SSM as
  fallback after Option A).

## Resume sequence after architectural fix

```bash
make build
./km init --dry-run=false   # redeploys create-handler Lambda with new code
./km create profiles/learn.v2.yaml --alias l6 --remote
```

Watch for:
```
✓ Slack: created inbound queue km-slack-inbound-lrn2-XXXXXX.fifo
✓ Slack: wrote queue URL to SSM Parameter Store /sandbox/lrn2-XXXXXX/slack-inbound-queue-url
✓ Slack: posted ready announcement (ts=...)
```

Then proceed to UAT Step 6 (first Slack message → expect Claude reply
in-thread within 60s).

## Orphan resources to manually clean up post-UAT

- Slack channels (archive from Slack UI — bot can't, see G6):
  `#sb-l2`, `#sb-l4`, `#sb-l5` (and any from later attempts)
- DDB rows: cleared by `km destroy` (already done for l2/l3/l4/l5)
- SQS queues: cleared by `km destroy` rollback (verify with
  `aws sqs list-queues --queue-name-prefix km-slack-inbound`)
