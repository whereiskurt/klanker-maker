# Sandbox instance role: least privilege (region Deny, scoped SSM, no authz-row writes) — design

**Date:** 2026-10-17. **Status:** approved in chat (operator: "full, canary ir-bot"); spec awaiting
operator review. **Branch:** `feat/sandbox-role-least-privilege`. **Census:** every runtime AWS
call the box makes, with caller and today's coverage, is in the session census (archived at the
operator's scratchpad as `region-lock-census.md`; the tables below are derived from it).

## 1. Problem

Three holes in `infra/modules/ec2spot/v1.7.0`, all pre-existing, all reachable from any sandbox
(every one of the 32 shipped profiles extends `base/platform`, which sets `iam.allowedRegions`):

1. **`ec2spot_region_lock` is `Allow Action=* Resource=*` conditioned on `aws:RequestedRegion`.**
   Named as a region *lock*, it is a region-scoped *grant of everything*: every in-region
   DynamoDB table, S3 object, SSM parameter, SES identity, log group. Sixteen of the box's 25
   runtime calls have no explicit grant and ride on it. The forged-thread-row hole in
   `docs/slack-default-route.md` §6.4 is one consequence.
2. **`AmazonSSMManagedInstanceCore` grants `ssm:GetParameter` and `ssm:GetParameters` on `*`**
   (verified live, policy v2), and `ec2spot_github_token` grants `kms:Decrypt` through SSM on
   every key in the account. Any sandbox can read and decrypt every SSM parameter in the
   account: other sandboxes' signing keys and GitHub tokens, the Slack bot token, the GitHub App
   key, `/{p}/access/*`, sibling installs' parameters. Independent of the region lock.
3. **The Slack poller writes the authorization row itself.** `userdata.go` `UpdateItem … SET
   sandbox_id = :sb …` on `{p}-slack-threads` every turn; the GitHub and H1 pollers do the same
   on their threads tables. The bridges honour `sandbox_id` on those rows to route and
   authorize. CLAUDE.md's "written only by bridge-side components" is false today.

## 2. Decisions

| Decision | Choice | Why |
|---|---|---|
| Region lock | **Deny** `NotAction` [the SCP `DenyOutsideRegion` exemption list + `bedrock-mantle:*`, `aws-marketplace:*`] with `StringNotEquals aws:RequestedRegion = allowed_regions`. The Allow-star is removed. | A Deny restricts; an Allow grants. Bedrock cross-region inference profiles route to other regions, so `bedrock:*` must stay exempt exactly as the SCP already does. |
| Everything else the box calls | **Explicit grants scoped to the sandbox** (`{id}`) or the install (`{p}`), one `aws_iam_role_policy` per concern, from the census (§3). Nothing is granted that no code calls. | Least privilege; the census is the evidence, the guard tests (§5) keep it mechanical. |
| SSM parameters | **Replace the managed Core policy with an inline copy minus `ssm:GetParameter*`** (agent, messages, inventory, association actions keep `*`). Parameter reads are explicit per path: `/{p}/sandbox/{id}/*`, `/{p}/slack/bridge-url`, `/{p}/config/remote-create/safe-phrase`, `/{p}/config/h1/api-username`, `/{p}/config/h1/api-token`, `iam.allowedSecretPaths`, `spec.otp.secrets` (+ `ssm:DeleteParameter` on those). `kms:Decrypt` via SSM is conditioned on `kms:EncryptionContext:PARAMETER_ARN` `StringLike` the same ARNs. | The managed policy is where the account-wide read comes from; the per-path grants that already exist become the whole rule. SSM SecureString decrypts carry the parameter ARN as KMS encryption context, so the KMS grant can be pinned to the same paths without naming keys. |
| Threads-table writes (Slack, GitHub, H1) | **Keep the pollers' writes, but condition them**: `dynamodb:UpdateItem`/`GetItem` with `ForAllValues:StringEquals dynamodb:Attributes` = each poller's non-authz attribute set (Slack: `claude_session_id, agent_type, last_assistant_msg, last_turn_ts, last_processed_event_ts, ttl_expiry, channel_id, thread_ts`), `dynamodb:ReturnValues` ∈ {NONE, UPDATED_OLD, UPDATED_NEW}, `dynamodb:Select` = SPECIFIC_ATTRIBUTES for reads. **`sandbox_id` is not in any set**, so the box cannot write it; the poller's update expression drops `sandbox_id` (the bridge wrote it at dispatch). A row lacking `sandbox_id` already reads as unrouted (`LookupSandbox` returns "" ⇒ no authz, no routing). | Closes the forged-row hole with IAM alone, no new bridge action and no poller protocol change. A box can still touch non-authz fields of another thread's row (a nuisance, not a bypass); the bridge action that removes the box's table access entirely is the follow-on, recorded in §8. |
| Slack stream-messages table | Keep `dynamodb:PutItem` on `{slack_stream_messages_table}` as today. | Not an authz table. |
| Module versioning | New immutable `infra/modules/ec2spot/v1.8.0`; pin bumped at its three sites (`locals.substrate_module_versions`, `pkg/compiler/ec2spot_timeout_test.go`, `pkg/terragrunt/substrate_version_pin_test.go`). | Repo rule. |
| Table/queue names with `km-` fallbacks | The compiler emits every table and queue name the box uses into the env unconditionally; the `km-` fallbacks in the scripts and sidecars become an error path, not a default. | A `{p}`-scoped grant cannot cover a `km-*` fallback on a non-`km` install; the allow-star hid that. |
| Operator-authored `aws` calls in `initCommands` | Not granted. `km validate` WARNs when an `initCommands` line contains `aws ` and names a bucket/table/parameter outside the grants it can see (best-effort regex); docs say to add the need as `iam.allowedSecretPaths` (SSM) or ask for a profile grant field. | Cannot be enumerated; the warning makes the silent break loud. The shipped `gpu-*` `aws s3 sync` of `models/*` is covered by the explicit `s3:GetObject`+`ListBucket` on `models/*`. |
| Mail | `s3:GetObject`/`ListBucket` on `mail/*` (shared, as today). | The receipt rule writes one prefix; narrowing needs per-recipient keys (§8). |
| MITM CA key | `sidecars/*` stays readable (as today); the CA key's per-sandbox future is §8. | Not this phase. |
| Canary | `ir-bot` (`profiles/ir-bot.yaml`) only, recreated after the apply; go/no-go from §6 before any other recreate. Existing boxes keep v1.7.0 roles until recreate (the role is created per sandbox at create). | Operator decision. |

## 3. Grants in v1.8.0 (per policy)

`{b}` artifacts bucket, `{p}` prefix, `{id}` sandbox id, `{r}` region, `{a}` account.

| Policy | Actions | Resource / condition |
|---|---|---|
| `region_deny` | `Deny NotAction [iam:*, sts:*, organizations:*, support:*, health:*, trustedadvisor:*, cloudfront:*, waf:*, shield:*, budgets:*, ce:*, cur:*, globalaccelerator:*, networkmanager:*, pricing:*, bedrock:*, bedrock-mantle:*, aws-marketplace:*]` | `*`; `StringNotEquals aws:RequestedRegion = allowed_regions` |
| `ssm_core` (inline copy of the managed policy) | the managed policy's actions **minus** `ssm:GetParameter`, `ssm:GetParameters` | `*` |
| `ssm_params` | `ssm:GetParameter` | `parameter/{p}/sandbox/{id}/*`, `parameter/{p}/slack/bridge-url`, `parameter/{p}/config/remote-create/safe-phrase`, `parameter/{p}/config/h1/api-username`, `parameter/{p}/config/h1/api-token`, each `allowedSecretPaths`, each `otp.secrets` path; `ssm:DeleteParameter` on the `otp.secrets` paths |
| `ssm_kms` | `kms:Decrypt` | `key/*`; `kms:ViaService = ssm.{r}.amazonaws.com` AND `StringLike kms:EncryptionContext:PARAMETER_ARN` = the same parameter ARNs |
| `artifacts_read` | `s3:GetObject` | `{b}/sidecars/*`, `{b}/binaries/*`, `{b}/artifacts/{id}/*`, `{b}/slack-inbound/{id}/*`, `{b}/rsync/*`, `{b}/mail/*`, `{b}/models/*`, `{b}/sandboxes/{id}/secrets.enc.yaml` |
| `artifacts_list` | `s3:ListBucket` | `arn:aws:s3:::{b}`; `StringLike s3:prefix` [`mail/*`, `mail/`, `artifacts/{id}/*`, `models/*`] |
| `artifacts_write` | `s3:PutObject` | `{b}/{transcripts,captures,learn,execs,artifacts,traces,logs,metrics}/{id}/*`; `s3:DeleteObject` on `{b}/artifacts/{id}/clone-workspace.tar.gz` |
| `audit_logs` | `logs:CreateLogGroup, PutRetentionPolicy, CreateLogStream, PutLogEvents, GetLogEvents` | `log-group:/{p}/sandboxes/{id}/*` (covers the group and its streams) |
| `ses_send` | `ses:SendEmail, ses:SendRawEmail` | `identity/{email_subdomain}.{domain}` (+ configuration set if any); `StringLike ses:FromAddress` = `{id}@*` and the alias forms km-send uses (implementer confirms from code) |
| `inbound_sqs` | `sqs:ReceiveMessage, DeleteMessage, ChangeMessageVisibility, GetQueueAttributes, GetQueueUrl` | the four `{p}-{slack,github,h1,webhook}-inbound-{id}.fifo` (each gated on its inbound flag as today) |
| `threads_rw` | `dynamodb:GetItem, UpdateItem` | `table/{p}-slack-threads`, `table/{p}-github-threads`, `table/{p}-h1-threads`; conditions per §2 (attribute sets per poller, `ReturnValues`, `Select`) |
| `identities_read` | `dynamodb:GetItem` | `table/{p}-identities` |
| `budget_quota` | `dynamodb:GetItem, UpdateItem, Query` on `{p}-budgets` (+index), `GetItem` on `{p}-sandboxes` (as today); `dynamodb:UpdateItem` on `table/{p}-action-quota` | |
| `stream_ddb`, `eventbridge`, `bedrock`, `sandbox_secrets_kms`, `fence_self_assume` | unchanged from v1.7.0 | |

Removed: `ec2spot_region_lock` (Allow), the managed `AmazonSSMManagedInstanceCore` attachment,
and `ec2spot_github_token`'s unconditioned `kms:Decrypt` (folded into `ssm_kms`).

## 4. Code changes outside the module

- `pkg/compiler`: emit every table/queue name env unconditionally; the poller update
  expressions drop `sandbox_id` (Slack; check GitHub/H1 for the same) and use
  `--return-values NONE`; GetItems on threads tables use `--projection-expression`
  (needed for `dynamodb:Select = SPECIFIC_ATTRIBUTES`); frozen golden hand-patched.
- `sidecars/http-proxy` / `pkg/quota`: no change if the key schema already lets the box only
  write its own `{id}`-keyed items (implementer checks `dynamodb:LeadingKeys` applicability and
  adds it when the partition key is the sandbox id).
- `pkg/profile`: `km validate` WARN for `aws ` in `initCommands` (best-effort).
- `pkg/terragrunt` guards: `TestSandboxRole_HasNoWriteGrantOnSlackThreadsTable` becomes
  `…_ThreadsWriteGrantExcludesSandboxID` (asserts the attribute condition exists and lacks
  `sandbox_id`); a new name-agnostic guard pairs every `aws <svc>` CLI call and SDK client in
  the census sources with a grant in v1.8.0 (same shape as `TestTTLHandlerModule_EveryEnvTableHasAnIAMGrant`);
  the three pin sites.
- Docs: `docs/operational-gotchas.md` entry; `docs/slack-default-route.md` §6.4 rewritten
  (hole closed, how); CLAUDE.md bullet; `docs/sandbox-secrets.md`/`brokered-secrets.md` one
  line each on the SSM scope; memory `project_ec2spot_region_lock_is_allow_star` → fixed.

## 5. Tests

- Module: `terraform validate` in CI-less form (the existing module tests parse HCL); the
  guard tests above.
- Live negative controls on the canary (the only proof that matters, see
  `project_cross_account_gpu_launch`: a simulator says what a policy SAYS, not what AWS ENFORCES):
  from `ir-bot`, as uid `sandbox` (via `km shell`): `aws ssm get-parameter` on another sandbox's
  signing key ⇒ AccessDenied; `aws dynamodb update-item` on `km-slack-threads` setting
  `sandbox_id` ⇒ AccessDenied; `aws dynamodb scan km-sandboxes` ⇒ AccessDenied; `aws s3 ls
  s3://{b}/sandboxes/` ⇒ AccessDenied; `aws sts get-caller-identity --region eu-west-1` ⇒ ok
  (exempt); `aws s3 ls --region eu-west-1 s3://{b}/sidecars/` ⇒ AccessDenied (region deny).
- Live positive controls on the canary: boot completes (sidecars fetched, `SANDBOX_READY`),
  audit events in CloudWatch, a Slack turn in a routed thread and in `#sb-`, a `km-slack dm`,
  `km-send` email delivered, a quota increment visible in `{p}-action-quota`, Claude on
  Bedrock answers (cross-region inference profile), `km stop`/`km resume`, idle detector
  `GetLogEvents` in CloudTrail.

## 6. Rollout and go/no-go

1. `make build` → `make build-lambdas` → `km init --dry-run=false` (pin bump + any env
   emission change). Existing sandboxes are untouched: the role is created per sandbox.
2. `km destroy ir-bot && km create profiles/ir-bot.yaml ir-bot` (or a cold-create mention).
3. Go/no-go = all §5 live controls green and `km doctor` clean. No-go ⇒ repin v1.7.0
   (one-line revert of the three sites) and recreate the canary.
4. Then the operator recreates other sandboxes at their pace; `km doctor` gains a WARN listing
   running sandboxes whose role still has the `region-lock` inline policy (the old name),
   so the fleet's state is visible.

## 7. Half-deploys

New module pin without `make build` ⇒ nothing (the pin is read by the compiler in the
binary that creates). New binary without the module dir ⇒ `km create` fails at terragrunt
init naming the missing source (loud). A box created under v1.8.0 whose profile uses an
`initCommands` `aws` call the grants do not cover ⇒ that step fails under `set -e` in
`km-init.sh`, reported as `init_failed` (loud since 2026-09-17).

## 8. Out of scope, recorded

A bridge action that removes the box's access to the threads tables entirely; per-recipient
mail keys so `mail/*` can narrow; a per-sandbox MITM CA; `bedrock:CountTokens` if Claude Code
turns out to call it (watch the canary's proxy log); SSM agent self-update bucket access if
the deny ever surfaces it (CloudTrail on the canary).

## 9. Live UAT — canary `ir-bot` (2026-10-17/18, go/no-go)

Deploy: `make build` → `make build-lambdas` → `km init --dry-run=false` (28 modules, 10 changed, 0
destroyed; one transient Lambda upload 408 and one SSO-expiry hang on the way, both retried). Canary:
`km destroy sl-40741914 --remote --yes` → `km create profiles/ir-bot.yaml ir-bot` → `irbot-136169c8`
(t3.large on-demand, us-east-1a). Role `km-ec2spot-ssm-irbot-136169c8-use1`: no managed policy
attached, 17 inline policies, `region-deny` present, **aggregate 8,479 of 10,240 characters** (no
fence/sops/secret paths on this profile). No Session Manager preferences document exists in the
region, so the inline Core copy needs nothing extra.

Negative controls, run as uid `sandbox` on the box (all **AccessDenied** as required): another
sandbox's `signing-key`; `/{p}/slack/bot-token`; `UpdateItem … SET sandbox_id` on `km-slack-threads`;
`Scan km-sandboxes`; `s3 ls …/sandboxes/`; `s3 ls --region eu-west-1 …/sidecars/` (region Deny).
`sts get-caller-identity --region eu-west-1` succeeded (exempt).

Positive controls (all green): boot `SANDBOX_READY` with every sidecar fetched; the role created its
own CloudWatch log group and `audit` stream; `logs:GetLogEvents` on its own group; bridge-url and
own-channel SSM reads; sidecar read from S3; own session-attribute `UpdateItem` on the threads table;
Claude on Bedrock answered (`canary-ok`, 200 via the MITM proxy, tokens metered into `km-budgets`);
`km-send` delivered (SES MessageId returned); `km-slack post` to `#sb-`; `km-slack dm` to the operator;
a Slack turn in the owned channel and one through the default route both answered in ~10 s and both
thread rows carry the bridge's `sandbox_id`/`last_dispatch_ts`/`created_at` next to the poller's
`claude_session_id`/`agent_type`/`last_processed_event_ts`. `km doctor` after the apply: 0 errors;
"Sandbox role shape" runs. Not exercisable on this profile: action quotas (no `limits`).
Stop/resume: `km stop` → stopped → `km resume` → SSM Online in ~2 min, all four sidecar units active, poller re-read its queue URL from SSM, `/var/lib/km/init-done` persisted (no boot gate on resume).

**Verdict: GO.** Existing boxes stay on the v1.7.0 role until recreated (`km doctor` names them).
