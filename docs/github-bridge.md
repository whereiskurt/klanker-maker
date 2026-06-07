# GitHub Bridge Guide

> **Phase 97 (2026-06-06) — GitHub comment-trigger bridge (complete):**
> When an allowlisted GitHub login @-mentions the bot in a pull-request comment,
> the km-github-bridge Lambda emits a 👀 reaction within the ack window, dispatches
> the comment to the per-repo sandbox (warm path: FIFO enqueue; cold path:
> EventBridge SandboxCreate), and the sandbox agent posts a PR review via
> `km-github review`. Dormant by default — requires `github.repos:` in
> `km-config.yaml` and `km github init` to activate.

The GitHub bridge extends the km sandbox platform with a GitHub-native agent
trigger: one `@km-bot review this PR` comment in a pull request sends the full
PR diff to a Claude Code agent in a km sandbox, which reads the diff, runs its
analysis, and posts a structured PR review back to GitHub — all within the km
security boundary.

## Table of Contents

1. [Overview](#overview)
2. [Architecture](#architecture)
3. [GitHub App Scope Table](#github-app-scope-table)
4. [Config Surface — github.repos](#config-surface----githubrepos)
5. [The github-review Profile](#the-github-review-profile)
6. [CLI Reference](#cli-reference)
7. [Deploy Sequence](#deploy-sequence)
8. [Dormant Invariant](#dormant-invariant)
9. [km doctor GitHub Checks](#km-doctor-github-checks)
10. [Troubleshooting](#troubleshooting)
11. [See Also](#see-also)

---

## Overview

The GitHub comment-trigger bridge is the GitHub-shaped twin of the Slack inbound
path. The high-level flow is:

1. A collaborator comments `@km-bot review this PR` on any open PR in a
   configured repo.
2. GitHub delivers the `issue_comment` webhook to the km-github-bridge Lambda
   function URL.
3. The bridge HMAC-verifies the payload, deduplicates by `X-GitHub-Delivery`
   GUID, checks the commenter is in the per-repo allow-list, confirms it's a
   PR comment (not an issue comment), and extracts the mention body.
4. The bridge resolves the repo `owner/repo` to an `{alias, profile, allow}`
   tuple from `github.repos` in `km-config.yaml`.
5. **Warm path:** a sandbox for the alias exists and has a github-inbound FIFO
   queue → the comment envelope is sent to the queue → the source-aware poller
   inside the sandbox drains the envelope and dispatches a Claude turn.
6. **Cold path:** no sandbox for the alias → the bridge fires an EventBridge
   `SandboxCreate` event with the alias + profile + carried envelope → the
   create-handler provisions a new sandbox → the queue is drained on first
   poller tick.
7. The bridge posts a 👀 reaction to the comment immediately on step 4 so the
   commenter sees an ACK before the agent finishes.
8. The agent reads the PR diff via `km-github pr-files`, runs its analysis, and
   posts a review via `km-github review`.

**Key design decisions:**
- No new Slack OAuth scopes — the bridge is GitHub-only.
- Deny-by-default allowlist per repo — silent ignore for non-allowed logins.
- GUID dedupe at the nonces DynamoDB table — redeliver never double-dispatches.
- Dormant when `github.repos` is empty — byte-identical to pre-Phase-97 behavior.

---

## Architecture

```
GitHub PR comment
    │ issue_comment webhook
    ▼
km-github-bridge Lambda
 (function URL, HMAC-verified)
    │
    ├─ Warm path: alias sandbox exists
    │    └── SQS FIFO github-inbound queue ──▶ source-aware poller
    │                                              (inside sandbox EC2)
    │                                               └── km agent run
    │
    └─ Cold path: no sandbox
         └── EventBridge SandboxCreate
               └── create-handler Lambda
                     ├── provision EC2
                     └── drain carried envelope into queue on boot
                               └── source-aware poller
                                    └── km agent run

sandbox agent
    ├── km-github pr-files   (fetch PR diff via GitHub App token)
    ├── Claude Code          (analysis turn)
    └── km-github review     (post PR review to GitHub)
```

SSM parameters (per-install, under `/{prefix}/config/github/`):
- `app-client-id` — GitHub App client ID
- `private-key` — GitHub App RSA private key (KMS SecureString)
- `installation-id` / `installations/{owner}` — App installation IDs
- `webhook-secret` — HMAC signing secret for webhook delivery verification
- `bot-login` — GitHub App bot login name (e.g. `km-bot[bot]`)
- `bridge-url` — Lambda function URL written by `km init`

---

## GitHub App Scope Table

The GitHub App requires the following **repository permissions** (granted during
App creation via `km github manifest`):

| Permission | Level | Required by |
|---|---|---|
| `issues` | write | Post 👀 reaction to issue comments |
| `pull_requests` | write | Read PR metadata, post PR reviews |
| `contents` | read | Read PR diff / file content |
| `checks` | write | (reserved for future check-run support) |

**Webhook event:**

| Event | Required by |
|---|---|
| `issue_comment` | Trigger on PR and issue comments |

> Note: `issue_comment` fires on both issue and PR comments. The bridge filters
> for PR comments only (the payload includes `pull_request` linkage). Issue
> comments from allowlisted logins are silently ignored.

---

## Config Surface — github.repos

Add a `github:` block to `km-config.yaml`:

```yaml
github:
  default_profile: profiles/github-review.yaml   # fallback when per-repo profile absent

  repos:
    # Exact match: "owner/repo"
    - match: my-org/my-service
      alias: gh-my-org-my-service    # optional; default: "gh-{owner}-{repo}"
      profile: profiles/github-review.yaml
      allow:
        - alice
        - bob

    # Glob match: "owner/*" (all repos in an org, first-wins)
    - match: my-org/*
      profile: profiles/github-review.yaml
      allow:
        - alice
```

**Field reference:**

| Field | Type | Required | Description |
|---|---|---|---|
| `match` | string | yes | `owner/repo` exact match or `owner/*` glob. Exact wins over glob regardless of declaration order. |
| `alias` | string | no | Sandbox alias used when creating a cold sandbox. Defaults to `gh-{owner}-{repo}`. |
| `profile` | string | no | Path to SandboxProfile YAML. Defaults to `github.default_profile`. Cold-create fails if neither is set. |
| `allow` | []string | no | GitHub logins that may trigger dispatch. Deny-by-default: unlisted logins are silently ignored. |

**Resolution order:**
1. Exact match (`entry.Match == repo.full_name`) — first exact wins.
2. Glob match (`owner/*`) — first glob wins in declaration order.
3. No match → bridge returns 200 and logs `no config for repo`.

---

## Multi-install (multiple `resource_prefix` environments)

The GitHub bridge is **per-install**. Each `resource_prefix` (e.g. `kph`, `sec`)
runs its own `{prefix}-github-bridge` Lambda with its own Function URL, its own SSM
App config under `/{prefix}/config/github/`, its own `github.repos:` in its own
`km-config.yaml`, and dispatches only into its own `{prefix}-sandboxes` table.
**A bridge cannot dispatch into another prefix's sandboxes.**

A GitHub App has exactly **one** webhook URL, so serving two installs requires one
of two patterns:

### Pattern A — two GitHub Apps (supported today)

Run a separate GitHub App per install. This is the right choice when each
environment owns a **disjoint** set of repos.

1. Create two Apps (e.g. *klanker-kph* and *klanker-sec*). `km github manifest`
   renders a manifest for each.
2. Run `km github init` in each install — each stores its own
   App creds + webhook secret in its own `/{prefix}/config/github/` SSM paths.
3. Point each App's **Webhook URL** at that install's bridge Function URL
   (`km github status` → `bridge-url`).
4. Install App-*kph* on the repos `kph` owns; App-*sec* on the repos `sec` owns.
5. Each `km-config.yaml` lists only its own repos in `github.repos:`.

Routing-by-repo is then determined by **which App is installed on which repo** —
a comment on a `kph` repo reaches only the `kph` App → `kph` bridge → `kph`
sandboxes. Zero shared infrastructure.

> **Invariant:** a given repo should be owned by exactly **one** install (one App
> installation + one matching `github.repos:` entry). Registering the same repo in
> two installs is ambiguous.

### Pattern B — one GitHub App, federated relay

If you want a **single bot identity** across both installs (one App, one place to
manage), you need a federated relay — the GitHub analog of Slack's Phase 95
`slack.peer_bridges` (`docs/slack-notifications.md` § Phase 95): one App's webhook
points at a "front-door" install whose bridge relays repos it doesn't own to peer
bridges until the owning install handles it.

**This is not yet implemented** — see the design spec
`docs/superpowers/specs/2026-06-07-github-bridge-peer-relay-design.md` (Phase 100).

### What multi-install does NOT do

Routing **by command on the same repo** to different prefixes (e.g. `/patch` →
`sec`, `/review` → `kph` on one repo) is not possible. A command's `alias`/`profile`
(Phase 99) resolve inside the handling bridge's own prefix; there is no cross-prefix
dispatch.

---

## The github-review Profile

`profiles/github-review.yaml` is the lean built-in profile for GitHub PR review
sandboxes. Key properties:

| Property | Value |
|---|---|
| TTL | 2h |
| Idle timeout | 20m |
| Teardown policy | stop (DDB record preserved for status queries) |
| Instance type | t3.medium (spot) |
| Network enforcement | proxy |
| Egress | GitHub, GitHub raw content, AWS, Anthropic API |
| Agent | claude (Bash/Read/Write/Edit/Glob/Grep/WebFetch auto-approved) |
| GitHub inbound | `notification.github.inbound.enabled: true` |

```yaml
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: github-review
spec:
  lifecycle:
    ttl: "2h"
    idleTimeout: "20m"
    teardownPolicy: stop
  notification:
    github:
      inbound:
        enabled: true   # provisions the per-sandbox github-inbound FIFO queue
  agent:
    claude:
      tools:
        autoApprove: [Bash, Read, Write, Edit, Glob, Grep, WebFetch]
```

The `notification.github.inbound.enabled: true` field is what tells the
create-handler to provision the per-sandbox github-inbound FIFO SQS queue
and write its URL to the `km-sandboxes` DDB row so the bridge can find it
on the warm path.

---

## CLI Reference

### km github init

Bootstrap GitHub integration: fetch the App's bot login from the GitHub API
(requires the App private key in SSM), write `bot-login` to SSM, and confirm
the webhook secret is set. Idempotent.

```bash
km github init
```

Optional flags:
- `--bot-token` — override SSM-sourced bot token for initial setup
- `--force` — re-run even if bot-login is already cached

### km github manifest

Generate a GitHub App manifest JSON suitable for pasting into the GitHub
"Create App from manifest" flow. Outputs JSON to stdout.

```bash
km github manifest > /tmp/km-gh.json
# Paste into GitHub → Settings → Developer settings → GitHub Apps → New → From manifest
```

The generated manifest includes:
- All required repository permissions (issues/write, pull_requests/write, contents/read)
- The `issue_comment` webhook event subscription
- Webhook URL set to the bridge Lambda function URL (read from SSM `bridge-url`)
- The webhook secret (read from SSM `webhook-secret`)

### km github status

Print the current GitHub App configuration from SSM (webhook secret is redacted):

```bash
km github status
```

### km-github (sandbox-side helper)

The `km-github` binary is injected into sandboxes that have GitHub App access.
Available subcommands:

```bash
# Fetch pull-request file list and diffs
km-github pr-files --owner <org> --repo <name> --pr <number>

# Post a PR review (approve / request changes / comment)
km-github review --owner <org> --repo <name> --pr <number> \
  --event REQUEST_CHANGES \
  --body "Found a null-pointer dereference in auth.go line 42."

# Post a comment on a PR or issue
km-github comment --owner <org> --repo <name> --issue <number> \
  --body "Analysis complete."
```

`km-github` uses the per-sandbox short-lived GitHub App token (provisioned by
the token-refresher Lambda, stored in SSM, fetched by the credential helper
sidecar) — not a personal access token.

---

## Deploy Sequence

Run this sequence **once** after Phase 97 ships and after any subsequent
`github.repos` change:

```bash
# Step 1: CLEAN build of all Lambda zips (avoids km-init-skips-existing-zips trap).
#   Memory: project_km_init_skips_existing_zips — must clean before km init.
make build-lambdas

# Step 2: Full terragrunt apply — new Lambda + EventBridge + env block.
#   NOT --sidecars: env-block changes require a full apply.
#   Memory: feedback_km_init_full_apply — use km init --dry-run=false.
km init --dry-run=false

# Step 3: Deploy schema field so create-handler picks up notification.github.inbound.
#   This is the fast binary-only path (no terragrunt env-block changes).
km init --sidecars

# Step 4: Update the GitHub App.
km github manifest > /tmp/km-gh.json
# Paste into GitHub → App settings → App Manifest → Update manifest → Save.
# Add the issue_comment webhook event if not already present.
# Confirm the Webhook URL = bridge function URL and Webhook Secret match SSM.
# Re-approve the App installation on the target org(s).

# Step 5: (One-time or after App reinstall) Cache the bot login.
km github init

# Step 6: Configure github.repos in km-config.yaml and re-deploy env block.
# Edit km-config.yaml: add github: block with repos entries.
km init --dry-run=false   # re-exports KM_GITHUB_REPOS to the bridge Lambda

# Step 7: Verify.
km github status
km doctor   # expect: GitHub App Config OK, webhook secret OK, bot login OK, bridge URL OK

# Step 8: For existing sandboxes, recreate to gain the github-inbound queue + poller.
km destroy <sandbox-id> --remote --yes && km create profiles/github-review.yaml --alias <alias>
```

> **Why `km init --dry-run=false` and NOT `km init --sidecars` for Steps 2 and 6:**
> The bridge Lambda's `environment.variables` block (which carries `KM_GITHUB_REPOS`,
> `KM_GITHUB_WEBHOOK_SECRET`, etc.) is owned by the `lambda-github-bridge` Terraform
> module and only updates on a full terragrunt apply. `--sidecars` rebuilds binaries and
> forces a Lambda cold-start but does NOT update the env block.

---

## Dormant Invariant

When `github.repos` is empty (or the `github:` block is absent from `km-config.yaml`):

- `KM_GITHUB_REPOS` is NOT exported to the bridge Lambda environment.
- The bridge Lambda returns 200 for any webhook with no dispatch.
- `km doctor` skips all GitHub checks silently (no spurious WARN).
- No new DDB tables, SQS queues, or EventBridge rules are created.
- All existing Slack/email/other functionality is byte-identical to pre-Phase-97.

This mirrors the Slack `peer_bridges` dormant invariant (Phase 95): absent or empty
config block = zero behavioral change.

---

## km doctor GitHub Checks

After a successful deploy, `km doctor` reports the following checks under
the GitHub bridge group:

| Check | Name | Green condition |
|---|---|---|
| App configured | `GitHub App Config` | `app-client-id` present in SSM; ≥1 installation key |
| Webhook secret | `GitHub Webhook Secret` | `webhook-secret` present in SSM |
| Bot login cached | `GitHub Bot Login` | `bot-login` non-empty in SSM |
| Bridge URL | `GitHub Bridge URL` | `bridge-url` present and `https://` prefixed |
| Repos resolvable | `GitHub Repos Resolvable` | Each `github.repos` entry has a profile (or `default_profile` fallback); no match-overlap |

**All checks are WARN (not ERROR) when missing** — GitHub integration is opt-in.
The entire group is **silently skipped** when `github.repos` is empty AND
`app-client-id` is absent in SSM.

Match-overlap WARN example:
```
⚠ GitHub Repos Resolvable
  1 resolvability issue(s): match-overlap: "org/repo-a" matches both
  entry[0] and entry[1] — entry[1] will never be reached
  → Remediation: Set profile: on each repos entry or set github.default_profile
```

---

## Troubleshooting

### 👀 reaction not appearing

1. Check CloudWatch logs for the `km-github-bridge` Lambda:
   ```bash
   aws logs tail /aws/lambda/km-github-bridge --follow
   ```
2. Verify the webhook is being delivered: GitHub → App settings → Advanced → Recent Deliveries.
3. Check the webhook URL matches `km github status` → `bridge-url`.
4. Confirm the webhook secret matches `km github status` → `webhook-secret` (redacted but present).
5. Confirm the commenter's GitHub login is in the repo's `allow:` list.
6. Run `km doctor` — look for WARN on `GitHub Webhook Secret` or `GitHub Bot Login`.

### PR review never posted

1. Check the sandbox's agent log: `km agent results <sandbox-id>`.
2. Verify the sandbox has `notification.github.inbound.enabled: true` in its profile.
3. Check the github-inbound queue exists: `km doctor` → no WARN on the sandbox's queue.
4. Confirm `km-github` is available inside the sandbox: `km shell <id> -- which km-github`.
5. Check the sandbox's GitHub App token is valid: the token-refresher Lambda logs should
   show a successful token write within the last 45 minutes.

### Cold-create never provisions

1. Verify `KM_GITHUB_REPOS` is set in the bridge Lambda env: `km github status`.
2. Check EventBridge rule exists: `aws events list-rules --name-prefix km-github`.
3. Check the create-handler Lambda logs for the `SandboxCreate` event.
4. Confirm the profile in `github.repos` is a valid, accessible path: `km validate <profile>`.

### Duplicate reviews posted

This indicates the GUID dedupe is not firing. Check:
1. The nonces DynamoDB table (`km-bridge-nonces`) is accessible by the bridge Lambda.
2. The `X-GitHub-Delivery` header is present on webhook deliveries (it always is for
   GitHub-originated webhooks; absent on custom test requests).

### km doctor reports GitHub checks WARN on unconfigured install

Expected if `app-client-id` is present in SSM but `github.repos` is empty.
The bridge is partially configured. Either:
- Remove the SSM parameter if you're not using GitHub integration, or
- Add `github.repos:` to `km-config.yaml` to complete configuration.

---

## See Also

- `docs/github.md` — GitHub App token lifecycle (per-sandbox short-lived tokens, refresher Lambda)
- `profiles/github-review.yaml` — built-in lean review profile
- `CLAUDE.md` § Phase 97 — feature summary and deploy notes
- `km github init`, `km github manifest`, `km github status` — CLI commands
- `km doctor` — platform health checks including GitHub bridge group

---

## Phase 98 — Richer write-backs, continuity, shared-alias, auto-resume, cold-create

> **Phase 98 (2026-06-07) — GitHub bridge expansion (complete):**
> New `km-github check` and `km-github pr create` sandbox-side verbs; thread/session
> continuity via the `km-github-threads` DynamoDB table; shared alias (multiple repos to
> one sandbox); stopped-sandbox auto-resume via EC2 StartInstances; and fixed cold-create
> with S3-staged profiles and SOPS-injected Claude credentials.

### New sandbox-side verbs

Two new `km-github` subcommands are available in Phase 98:

```bash
# Post a GitHub check run (e.g. "analysis complete" or "review failed")
km-github check \
  --owner <org> \
  --repo <name> \
  --name "km-review" \
  --conclusion success \
  --summary "Code review complete — no blocking issues found." \
  --head-sha <commit-sha>

# Open a new pull request from inside a sandbox worktree
km-github pr create \
  --owner <org> \
  --repo <name> \
  --title "Fix null-pointer in auth.go" \
  --base main \
  --head fix/null-auth \
  --body "Resolves the NPE identified in PR #42 review."
# Prints the new PR URL to stdout, e.g.:
#   https://github.com/my-org/my-service/pull/99
```

**Conclusions for `km-github check`:** `success`, `failure`, `neutral`, `cancelled`,
`skipped`, `timed_out`, `action_required`.

**Worktree-per-PR guidance:** The agent preamble instructs the sandbox to create an
isolated git worktree for each PR it works on:

```bash
# Example: work on PR #42 in an isolated worktree
git worktree add /workspace/pr-42 -b fix/pr-42-branch origin/main
cd /workspace/pr-42
# ... make changes ...
km-github pr create --owner org --repo repo --title "..." --base main --head fix/pr-42-branch
```

This pattern prevents worktree conflicts when a shared-alias sandbox handles multiple
repo reviews concurrently.

---

### Thread continuity and thread-bypass

Phase 98 introduces the `km-github-threads` DynamoDB table to track per-PR agent sessions.

**How it works:**

1. On first dispatch to a PR (from bridge: `owner/repo#<number>`), the bridge creates a
   thread record `{repo_key, pr_number} → {sandbox_id, agent_session_id, last_comment_id}`.
2. On subsequent @-mentions in the SAME PR/issue thread, the bridge looks up the record and
   re-dispatches to the same `agent_session_id` — the Claude turn continues where it left off,
   with full prior-turn context.
3. Thread-bypass: once a thread record exists for a PR, follow-up comments in that PR dispatch
   WITHOUT requiring a fresh @-mention. This mirrors the Slack Phase 91.3 thread-bypass
   behavior: threads are 1:1 conversations with the bot — re-@-mentioning was unnatural.

**Table schema:**

| Key | Type | Description |
|---|---|---|
| `repo_key` (PK) | string | `owner/repo` |
| `pr_number` (SK) | number | PR or issue number |
| `sandbox_id` | string | Current aliased sandbox ID |
| `agent_session_id` | string | Claude session ID for resume |
| `last_comment_id` | number | Last dispatched comment ID (idempotency) |
| `ttl` | number | Unix epoch; record expires 7 days after last activity |

**IAM:** The bridge Lambda has `dynamodb:GetItem`, `dynamodb:PutItem`, and
`dynamodb:UpdateItem` on the `km-github-threads` table.

---

### Shared alias — multiple repos, one sandbox

When two or more `github.repos` entries share the same explicit `alias`, all matching repos
dispatch to the same sandbox. The sandbox handles them in separate git worktrees.

```yaml
github:
  repos:
    - match: my-org/frontend
      alias: gh-shared        # explicit shared alias
      profile: profiles/github-review.yaml
      allow: [alice]

    - match: my-org/backend
      alias: gh-shared        # same alias → same sandbox
      profile: profiles/github-review.yaml
      allow: [alice]
```

**km doctor warnings:**

| Situation | Warning |
|---|---|
| Entry without explicit alias whose default (`gh-{owner}-{repo}`) equals another entry's explicit alias | `alias collision: "gh-myorg-myrepo" — entry[0] default alias matches entry[1] explicit alias` |
| Exact match entry shadowed by a glob that also covers it | `overlapping match: "org/repo" matches both entry[0] and entry[1] — entry[1] will never be reached` |
| Two entries with the SAME explicit alias | No warning — intentional shared-sandbox pattern. |

To intentionally share a sandbox across repos, always set `alias:` explicitly on all
sharing entries. Do NOT rely on the default `gh-{owner}-{repo}` alias for shared-sandbox
setups.

---

### Auto-resume — stopped sandbox woken by @-mention

A stopped or paused aliased sandbox is automatically resumed when an allowlisted @-mention
arrives.

**Flow:**

1. Bridge resolves `owner/repo` → alias via `github.repos`.
2. Bridge queries the `alias-index` GSI on `km-sandboxes` — finds a STOPPED record.
3. Bridge calls `ec2:StartInstances` on the instance (guarded by `km:managed=true` tag
   condition on the IAM policy).
4. Bridge enqueues the comment envelope to the sandbox's `github-inbound` FIFO queue
   (queue URL preserved in the DDB row across stop/start cycles).
5. After boot, the source-aware poller inside the sandbox drains the queued prompt and
   dispatches a Claude turn — no manual `km resume` required.

**Configure-once, stop, GitHub wakes it.** This pattern is ideal for cost-sensitive
review sandboxes: configure the sandbox once, let it idle-stop after the TTL, and
GitHub activity auto-resumes it.

> Note: the bridge ensures only a SINGLE StartInstances call per delivery GUID (GUID
> dedupe fires before the EC2 call). A sandbox already starting (state = PENDING) is
> treated as warm — the envelope is enqueued and will drain once boot completes.

CloudWatch log evidence of a successful auto-resume:
```
INFO  bridge: sandbox stopped; resuming alias=gh-org-repo instance_id=i-0abc123
INFO  bridge: StartInstances OK; enqueued prompt to fifo queue
```

---

### Cold-create with S3-staged profile and SOPS auth

Phase 98 fixes the cold-create path that was broken in Phase 97.

**What was broken:** The bridge generated a valid `sandbox_id` but used a wrong
`artifact_prefix` (double-slash path) and the cold box couldn't self-authenticate without
Bedrock credentials.

**What Phase 98 does:**

- `km init` now calls `PreStageGitHubProfiles` which uploads each `github.repos` profile to
  `s3://<artifacts_bucket>/github-profiles/<slug>/profile.yaml` before any apply.
- If `spec.secrets.sopsFile` is set in the profile, `km init` also uploads the SOPS-encrypted
  secrets bundle to `s3://<artifacts_bucket>/github-profiles/<slug>/secrets.enc.yaml`.
- The bridge generates `artifact_prefix = github-profiles/<slug>` (no double-slash).
- The create-handler Lambda provisions the cold box using the S3-staged profile and injects
  the SOPS bundle at boot (Phase 89 mechanism), giving the cold box Claude credentials without
  Bedrock.

**Operator step — encrypt a SOPS bundle with Claude credentials:**

```bash
# 1. Create a plaintext secrets file (NOT committed to git):
cat > /tmp/github-review-secrets.yaml <<'EOF'
claude:
  # From `claude auth login` — ~/.claude/.credentials.json
  access_token: "<your-claude-oauth-access-token>"
  refresh_token: "<your-claude-oauth-refresh-token>"
  # Optional: scoped to the review profile
  organization_id: "<optional>"
EOF

# 2. Encrypt with the shared SOPS KMS key (get the key ARN from `km info`):
SOPS_KMS_ARN=$(km info --json | jq -r '.platform.sops_kms_key_arn')
sops --kms "$SOPS_KMS_ARN" --encrypt /tmp/github-review-secrets.yaml \
  > profiles/github-review-secrets.enc.yaml

# 3. Reference in profiles/github-review.yaml:
cat >> profiles/github-review.yaml <<'EOF'
spec:
  secrets:
    sopsFile: profiles/github-review-secrets.enc.yaml
EOF

# 4. Re-run km init to pre-stage the encrypted bundle to S3:
km init --dry-run=false
```

The cold box decrypts the bundle at boot (KMS key ARN is in the profile) and writes the
Claude OAuth credentials to `~/.claude/.credentials.json`. The poller can then dispatch
Claude turns directly without Bedrock.

---

### Phase 98 deploy sequence (complete)

This section supersedes the Phase 97 deploy sequence above. Run this in order:

```bash
# Step 1: Rebuild the km OPERATOR BINARY — REQUIRED, do not skip.
#   Phase 98 added 'dynamodb-github-threads' to regionalModules(), PreStageGitHubProfiles,
#   and new doctor checks — these live in the km binary. If you run a stale km, 'km init'
#   uses the OLD module list and silently SKIPS the new DDB table; lambda-github-bridge
#   then falls back to its mock dependency outputs (fake account 000000000000 in table_arn),
#   so the bridge's thread-continuity IAM points at a non-existent ARN and DDB calls get
#   AccessDenied at runtime. 'make build' != 'make build-lambdas' — you need BOTH.
make build           # rebuilds ./km with the new regionalModules() entry (ldflags-stamped)

# Step 2: CLEAN build of all Lambda zips.
#   Memory: project_km_init_skips_existing_zips — 'make build-lambdas' rebuilds from the
#   hardcoded lambdaBuilds() list (the km-github-bridge zip). This is SEPARATE from Step 1:
#   Step 1 builds the operator binary, Step 2 builds the Lambda zips. Run both.
make build-lambdas

# Step 3: Full terragrunt apply — new dynamodb-github-threads table + bridge IAM/env.
#   This applies ALL modules including the new DDB table and the v1.1.0 bridge module.
#   NOT --sidecars: env-block + IAM + new DDB table require a full terragrunt apply.
#   Memory: feedback_km_init_full_apply — use km init --dry-run=false.
#   FIRST-APPLY NOTE: terragrunt applies dynamodb-github-threads BEFORE lambda-github-bridge
#   (the bridge depends on it). If you ever see "dynamodb-github-threads ... has no outputs,
#   but mock outputs provided" during the bridge apply, the table did NOT apply first (usually
#   a stale km from skipping Step 1) — re-run this command once the table exists so the bridge
#   re-resolves the REAL table_arn (real account) into its IAM policy.
km init --dry-run=false

# Step 4: Refresh create-handler and source-aware poller binaries.
#   --sidecars is safe here: no env-block changes, only binary refresh.
km init --sidecars

# Step 5: Verify km doctor.
#   Expect: dynamodb-github-threads OK, lambda-github-bridge v1.1.0 IAM OK.
#   No unexpected alias-collision WARNs for your config.
#   Spot-check the bridge IAM did NOT bake the mock ARN:
#     aws iam get-role-policy --role-name km-github-bridge-role \
#       --policy-name km-github-bridge-dynamodb-github-threads \
#       --query 'PolicyDocument.Statement[].Resource'
#   The account in the ARN must be your REAL account, not 000000000000.
km doctor

# Step 6 (cold-create only): Encrypt and pre-stage the SOPS bundle.
#   See "Cold-create with S3-staged profile and SOPS auth" above.
#   Re-run 'km init --dry-run=false' after adding spec.secrets.sopsFile to the profile.

# Step 7: Existing sandboxes must be recreated to gain the new queue, poller, and verbs.
km destroy <sandbox-id> --remote --yes && km create profiles/github-review.yaml --alias <alias>
```

> **Why `km init --dry-run=false` and NOT `km init --sidecars` for Steps 2 and 5:**
> `km init --sidecars` rebuilds binaries and forces Lambda cold-starts but does NOT apply
> Terraform. The new `dynamodb-github-threads` table, the bridge v1.1.0 IAM grants
> (EC2/DDB threads), and the Lambda env block (`KM_GITHUB_THREADS_TABLE`, `KM_ARTIFACTS_BUCKET`)
> are all Terraform-managed resources — they only appear after a full `km init --dry-run=false`.

---

### Phase 98 troubleshooting

#### Follow-up comment dispatched without agent context (continuity not working)

1. Verify `km-github-threads` table exists: `aws dynamodb describe-table --table-name km-github-threads`.
2. Check bridge Lambda env: `km github status` → look for `KM_GITHUB_THREADS_TABLE`.
3. Confirm the table was applied: `km init --dry-run=false` re-applies if the module was skipped.
4. Look for the thread record: the bridge logs `event=thread_created` on first dispatch and
   `event=thread_resumed` on subsequent dispatches.

#### Stopped sandbox not resuming (auto-resume not working)

1. Check CloudWatch logs for `km-github-bridge` Lambda: look for `StartInstances` or
   `bridge: sandbox stopped; resuming`.
2. Verify the IAM policy includes `ec2:StartInstances` on the instance:
   `aws iam simulate-principal-policy` or check the bridge Lambda role in the Console.
3. Confirm the DDB row still carries `github_inbound_queue_url` (preserved across stop/start).
4. If the sandbox was fully destroyed (not just stopped), no DDB row exists → cold-create
   path fires instead.

#### Cold-create: box boots but can't auth Claude (SOPS bundle missing)

1. Verify the SOPS bundle was pre-staged: `aws s3 ls s3://<artifacts_bucket>/github-profiles/<slug>/`.
2. Confirm `spec.secrets.sopsFile` is set in the profile and points to the `.enc.yaml` file.
3. Re-run `km init --dry-run=false` after adding `sopsFile` — the pre-stage step runs on every `km init`.
4. Confirm the SOPS KMS key ARN in the profile matches the install's KMS key (`km info`).

#### km doctor WARN on alias collision for intentional shared-sandbox

If you want two repos to share one sandbox (worktree-per-PR pattern), set `alias:` explicitly
on BOTH entries. Auto-default aliases that happen to match an explicit alias trigger a
`alias collision` WARN even if the behavior is what you want.

```yaml
# Correct: explicit shared alias on both entries → no WARN
- match: my-org/frontend
  alias: gh-myorg-shared
  ...
- match: my-org/backend
  alias: gh-myorg-shared
  ...

# Wrong: one explicit, one auto-default → WARN "alias collision"
- match: my-org/gh-myorg-myrepo    # auto-default: gh-myorg-gh-myorg-myrepo ← not a collision
  ...
- match: my-org/frontend
  alias: gh-myorg-frontend         # explicit
  ...
```

---

### Phase 98 E2E verification checklist (GH-X-E2E)

Run this checklist against a real repo where the GitHub App is installed after completing
the deploy sequence above:

**A. CONTINUITY** — @-mention the bot on a PR; after it replies, post a follow-up WITHOUT
re-mentioning. Confirm the reply references prior-turn context and that `km-github-threads`
has a row for `{owner/repo, pr_number}` with a non-empty `agent_session_id`.

**B. WRITE-BACKS** — Trigger a request that causes the agent to run `km-github check` and
`km-github pr create`. Confirm the check run AND the new PR appear in the GitHub UI with
the correct conclusion and title.

**C. SHARED-ALIAS** — Configure two `github.repos` entries with the same `alias:`. @-mention
in each repo. Confirm both dispatches land on the SAME sandbox (check `km list`) in
separate worktrees (distinct `/workspace/pr-<N>` paths visible via `km agent results`).

**D. AUTO-RESUME** — Stop an aliased sandbox (`km pause <id>` or let idle-stop fire). @-mention
its repo. Confirm CloudWatch logs show `StartInstances OK` and the prompt drains after boot
with no manual `km resume`. Confirm no duplicate cold-create (check `km list` — only one
sandbox for the alias).

**E. COLD-CREATE** (optional but recommended) — @-mention a repo whose alias has NO sandbox.
Confirm the create-handler provisions from the S3-staged profile, the box self-authenticates
via the SOPS Claude credentials, and a PR review posts — fully automated.

**No regression** — Confirm the Phase 97 warm-path comment-trigger review still works on a
repo with an existing running sandbox (no code changed for the warm path).
