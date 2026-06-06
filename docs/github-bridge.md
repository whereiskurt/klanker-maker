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
