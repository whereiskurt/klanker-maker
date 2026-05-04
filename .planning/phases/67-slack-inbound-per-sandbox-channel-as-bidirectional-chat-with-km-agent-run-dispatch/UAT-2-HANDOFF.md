# Phase 67 UAT — Round 2 Handoff (2026-05-03)

**Branch:** `gsd/phase-67-slack-inbound`
**Status:** Bidirectional Slack ↔ Claude loop validated end-to-end on a
fresh-create sandbox (l8) with **no hot-patches required**.

## What was achieved this session

The full round trip works on a freshly-created sandbox:

```
Slack message (#sb-l8) → Bridge Lambda /events → SQS FIFO →
sandbox systemd poller → claude -p (with OAuth creds) →
Stop hook → /opt/km/bin/km-slack post --subject "" →
Bridge → Slack reply in-thread (no bold header, no chrome footer)
```

Session continuity works (DDB `claude_session_id` lookup; `--resume`
on follow-ups). Round-trip latency ~11s. Sustained back-and-forth chat
worked cleanly during UAT.

## Architectural change committed

Phase 63's Step 11d injected `KM_SLACK_CHANNEL_ID` and `KM_SLACK_BRIDGE_URL`
into the running sandbox's `/etc/profile.d/km-notify-env.sh` via
`ssm:SendCommand`. That action is **denied by an org-level SCP**
(`p-cvd490xt`) for the application account, so the env vars never
landed and the Stop hook had no way to post replies back to Slack.

Replaced with the same pattern used for the Phase 67-06 queue URL fix:
- `km create` (Lambda) writes `/sandbox/{id}/slack-channel-id` to SSM
  Parameter Store. Bridge URL is already at `/km/slack/bridge-url`.
- Cloud-init bootstrap polls both paths with retry/backoff and writes
  `/etc/profile.d/km-slack-runtime.sh`. Gated by new `NotifySlackEnabled`
  template field.
- Sandbox EC2 IAM gained `ssm:GetParameter` on `/km/slack/bridge-url`
  (the `/sandbox/{id}/*` prefix already covered the channel id).
- `runStep11dInject` simplified to a single `PutParameter` call; old
  retry-loop and instance-id resolution removed.
- `destroy_slack.go` cleans up the new SSM parameter at teardown.
- All Phase 63 tests (Step11d_*) rewritten for the new behavior.

## Commits since UAT started

| Commit | Subject |
|--------|---------|
| `181a21a` | create-handler IAM: `sqs:CreateQueue/DeleteQueue` |
| `8b601c7` | create-handler IAM: `ssm:SendCommand` (now redundant) |
| `e7e0b3e` | Architectural: queue URL via SSM Parameter Store |
| `106d495` | Poller: chown RUN_DIR, chmod PROMPT_FILE, drop `--bare`, `\|\| true` |
| `4c2eedd` | Slack body without footer chrome + source all `/etc/profile.d/*.sh` |
| `1b9e083` | Drop `--subject` from km-notify-hook Slack branch |
| `5579601` | Architectural: channel-id via SSM Parameter Store |
| `b78e1e2` | km-slack accepts empty `--subject`; bridge skips bold header |

## Validated on l8 (fresh create)

- ✅ SSM Parameter Store paths populated automatically:
  - `/sandbox/lrn2-f9d1bdf4/slack-channel-id` = `C0B0VSKMDBR`
  - `/sandbox/lrn2-f9d1bdf4/slack-inbound-queue-url` = full SQS URL
- ✅ `/etc/profile.d/km-slack-runtime.sh` written by cloud-init with all
  4 vars (`KM_SLACK_CHANNEL_ID`, `KM_SLACK_BRIDGE_URL`, `AWS_REGION`,
  `AWS_DEFAULT_REGION`).
- ✅ Poller sources every `/etc/profile.d/*.sh` before invoking claude
  (covers Slack runtime + km-identity + km-zz-goose-otel).
- ✅ Inbound dispatch works without ssm:SendCommand.
- ✅ Reply path posts cleanly to Slack — no bold subject header, no
  `---/Attach:/Results:` footer.
- ✅ km doctor (3 Phase 67 checks): all green.

## G-list — gaps still open

| ID | Severity | What | Where to fix |
|----|----------|------|--------------|
| G3 | low | `dynamodb-slack-threads` not in `km init` apply list | `internal/app/cmd/init.go` |
| G4 | low | Plan 67-10 generated `test/e2e/slack/profiles/inbound-e2e.yaml` with wrong `apiVersion` (`klanker.io/v1` vs `klankermaker.ai/v1alpha1`) and `ttlMinutes` instead of `ttl` | `test/e2e/slack/profiles/inbound-e2e.yaml` |
| G5 | medium | Phase 67 rollback after queue-create failure didn't archive Slack channel (Plan 67-06 spec required this; impl missing) | `internal/app/cmd/create.go` rollback block |
| G6 | low | `km destroy` archive step is skipped when bot can't post final message (`not_in_channel`) — archive should still proceed (separate API call) | `internal/app/cmd/destroy_slack.go` |
| G7 | low | `terragrunt apply` from raw shell silently deploys empty `artifact_bucket_arn` because Terragrunt config reads `KM_ARTIFACTS_BUCKET` env var. Should fail-fast when unset. | `infra/live/.../terragrunt.hcl` |
| G8 | medium | Phase 67 inbound provisioning is fatal-on-injection-failure. Phase 63 mirrors as non-fatal with retry. Can now make Phase 67 non-fatal too (architectural fix landed). | `internal/app/cmd/create.go` Step 11e |
| G9 | medium | create-handler IAM missing `dynamodb:PutItem` on `km-slack-threads`. The thread anchor write at create time fails with AccessDenied. Non-fatal (empty session_id has same effect as missing row), but should be granted. | `infra/modules/create-handler/v1.0.0/main.tf` |
| G10 | low | `instance_id` not written to DDB sandbox row at create time → km doctor flags running instances as orphan EC2 | likely Phase 14 sandbox identity wiring |
| G11 | low | `test.example.com` domain placeholder leaks somewhere into the email-config flow when no `KM_ROUTE53_ZONE_ID` is set. Cosmetic. | TBD — chase the placeholder |
| G12 | medium | **Fresh `--remote` create requires interactive `claude login` inside the sandbox** before Slack inbound replies can succeed. Local rsync of `~/.claude` doesn't apply to remote creates. Options: env-var passthrough, stash creds in SSM during create, or document as a one-time bootstrap. | `internal/app/cmd/create.go` or new bootstrap step |
| G13 | low | The redundant `aws_iam_role_policy.ssm_send_command` on the create-handler is no longer needed after the architectural fix — leftover from earlier UAT mid-iteration. Safe to remove. | `infra/modules/create-handler/v1.0.0/main.tf` |
| G14 | low | First few attempted runs after `claude login` exit 127 (claude not in PATH). Possibly a npm-install timing thing or PATH-source race. Eventually self-resolves. Worth a fast-fail diagnostic in `km doctor`. | TBD |
| G15 | low | Sometimes Claude's reply lands on the **previous** message instead of the latest — possibly FIFO ordering with rapid back-to-back Slack posts, or `KM_SLACK_THREAD_TS` reuse across the wrong message. Investigate. | `pkg/compiler/userdata.go` poller, or `pkg/slack/bridge/handler.go` |

(G1 and G2 were the SQS/SendCommand IAM gaps fixed in `181a21a` /
`8b601c7`. G2's grant is now superseded by the SSM Parameter Store
architectural change → will be removed alongside G13.)

## Other UAT observations (not gaps, recording for context)

- The **email branch** of the Stop hook still posts `[sandbox-id] idle`
  emails to the operator inbox alongside the Slack reply. Each Claude
  turn → one email + one Slack post. Some operators may want to disable
  email when Slack is on (`notifyEmailEnabled: false` in profile).
- `km destroy l7 --remote --yes` archived its Slack channel cleanly (no
  G6 manifestation that round). Likely G6 only triggers when bot is
  already removed from the channel.
- The unrelated commit `16bff1a` ("Slack transcript streaming design")
  was made by a parallel session and is **not** part of Phase 67 UAT
  scope.

## Resume tomorrow

```bash
# Validate clean room
git checkout gsd/phase-67-slack-inbound
git pull origin gsd/phase-67-slack-inbound
make build
go test ./...

# Plan gap closure
/gsd:plan-phase 67 --gaps
```

VERIFICATION.md will need to be updated with the new commits before
plan-phase can read the gap list. Worth running `/gsd:verify-work 67`
first to refresh the verification report.

## Pre-PR cleanup checklist (post-gap-closure)

- [ ] G9: grant create-handler `dynamodb:PutItem` on slack-threads table
- [ ] G13: remove redundant `aws_iam_role_policy.ssm_send_command` from
      create-handler module
- [ ] G3: add dynamodb-slack-threads to `km init` apply list
- [ ] G4: fix `test/e2e/slack/profiles/inbound-e2e.yaml` schema
- [ ] G5: add Slack channel archive to inbound-rollback path
- [ ] G6: decouple destroy archive from final-post success
- [ ] G7: terragrunt fail-fast on empty `KM_ARTIFACTS_BUCKET`
- [ ] G8: make Phase 67 inbound provisioning non-fatal
- [ ] G10: persist `instance_id` to DDB at create time (Phase 14
      territory; check if it's a Phase 67 surfacing bug)
- [ ] G11: trace `test.example.com` placeholder; remove leak
- [ ] G12: decide on cred-distribution strategy for remote creates
- [ ] G14: fast-fail diagnostic for `claude` PATH
- [ ] G15: trace FIFO ordering vs `KM_SLACK_THREAD_TS` re-use
- [ ] Update CLAUDE.md and `docs/slack-notifications.md` with the new
      SSM Parameter Store paths and the empty-subject convention
- [ ] Squash + open PR against `main`
