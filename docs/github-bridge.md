# GitHub Bridge Guide

> **App permissions:** for a per-scope breakdown of the GitHub App permissions and
> webhook events km requests (and the `contents:write` push opt-in), see
> `docs/github-app-permissions.md`.

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
10. [Phase 100 — Federated relay (one App, many installs)](#phase-100--federated-relay-one-app-many-installs)
11. [Phase 101 — Orphan-repo helpful reply (front-door default router)](#phase-101--orphan-repo-helpful-reply-front-door-default-router)
12. [Phase 115 — Generic event→prompt router](#phase-115--generic-eventprompt-router)
13. [Troubleshooting](#troubleshooting)
14. [See Also](#see-also)

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

### Pattern B — one GitHub App, federated relay (Phase 100, implemented)

If you want a **single bot identity** across both installs (one App, one place to
manage), use the federated relay — the GitHub analog of Slack's Phase 95
`slack.peer_bridges` (`docs/slack-notifications.md` § Phase 95). One App's webhook
points at a **front-door** install whose bridge relays webhooks for repos it doesn't
own to peer bridges; the install whose `github.repos:` matches the repo processes it.

See **§ Phase 100 — Federated relay (one App, many installs)** below for the full
runbook, config surface, deploy sequence, and `km doctor` peer checks.

### What multi-install does NOT do

Routing **by command on the same repo** to different prefixes (e.g. `/patch` →
`sec`, `/review` → `kph` on one repo) is not possible. A command's `alias`/`profile`
(Phase 99) resolve inside the handling bridge's own prefix; there is no cross-prefix
dispatch.

### Scale: install the App on selected repos, not all

> **The single biggest scale lever:** install the App on **selected repositories
> only**, never "All repositories." GitHub delivers an `issue_comment` webhook for
> *every* comment on *every* issue/PR in the repos the App is installed on — even
> when the bot is never @-mentioned. Scoping the installation to the handful of
> repos you actually wire into `github.repos:` means the other repos generate **zero
> deliveries** (no Lambda invocations, no dropped-event noise). Bot-visible noise is
> already nil regardless (deny-by-default + @-mention gate; 👀 only on dispatch), but
> installation scope is what keeps webhook/invocation volume low on a large org.

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
- `--bot-login` — override the bot login handle (default `km-{prefix}-github-bridge[bot]`)
- `--bridge-url` — bridge Lambda URL to store in SSM (set after `km init` provides the function URL)
- `--force` — re-run even if bot-login is already cached (rotates the webhook secret)

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

> **Phase 105 scoped shortcut (config-key edits only):** if you only edited a `github.*`
> config key in `km-config.yaml` (e.g. `github.default_router`, `github.repos`,
> `github.commands`) and do NOT need new resources or a code-zip rebuild, you can use:
> ```bash
> km init --github --dry-run=false   # applies lambda-github-bridge only (env+IAM)
> ```
> This completes in seconds and has zero drift — a subsequent `km init --plan` shows the
> bridge as a no-op. For code changes (`make build-lambdas`) or new TF resources, use the
> full `km init --dry-run=false`.

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
| Peer bridges | `GitHub peer bridges` | (Phase 100) `github.peer_bridges` entries are well-formed `https://` URLs and none is this install's own bridge URL (self-loop). SKIPPED when `github.peer_bridges` is empty |

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

## Phase 100 — Federated relay (one App, many installs)

Phase 100 lets a **single GitHub App** serve multiple `resource_prefix` installs.
A GitHub App has exactly one webhook URL; that URL points at one install — the
**front door**. The front-door bridge relays webhooks for repos it does not own to
its peer bridges, and the install whose `github.repos:` matches the repo processes
the comment. This is the GitHub analog of the **shipped** Slack Phase 95 relay
(`slack.peer_bridges`, `docs/slack-notifications.md` § Phase 95), simplified to
fire-and-forget (no orphan-repo reply — that is deferred to Phase 101).

### Model: front door + full symmetry

- **One App, one webhook URL → one front-door install.** GitHub delivers every
  `issue_comment` event to that single URL.
- **Full symmetry.** Each install lists *every other* install's bridge Function URL
  in `github.peer_bridges`. Whichever install GitHub happens to deliver to acts as
  the front door for that delivery; symmetry means any install can be the front
  door, so you don't have to pick one statically.
- **Owner processes; front door just relays.** On a delivery the front-door bridge
  runs `Resolve(owner/repo)` against its own `github.repos:`. **Matched** → it
  processes locally (full path: thread-lookup, @-mention gate, allowlist, dedupe,
  dispatch, and the single 👀 reaction). **Not matched** → it broadcasts the raw
  webhook verbatim to every URL in `github.peer_bridges`.
- **The owner posts the single 👀.** The front door on a miss **does not react** —
  it only relays. Exactly one install (the owner) reacts.

### What is forwarded (verbatim)

The relay POSTs the **raw request body** unchanged, plus these headers verbatim:

| Header | Purpose |
|---|---|
| `X-Hub-Signature-256` | HMAC — each peer re-verifies with its own copy of the **same** App webhook secret. GitHub signatures carry no timestamp → no skew window. |
| `X-GitHub-Event` | Event type (`issue_comment`). |
| `X-GitHub-Delivery` | Delivery GUID — each install dedupes in its **own** `{prefix}` nonces store. |
| `X-KM-Relayed: 1` | **Added by the relay.** Single-hop loop guard. |

Because the body is byte-identical, the same `X-Hub-Signature-256` verifies at the
peer — no re-signing, no shared signing key beyond the App webhook secret each
install already holds.

### Single-hop loop guard

`X-KM-Relayed: 1` is the **entire** loop guard. A relayed request is **terminal**:

| `X-KM-Relayed` | `Resolve()` matched? | Action |
|---|---|---|
| absent | yes | process locally (thread / @-mention / auth / dedupe / dispatch / 👀) |
| absent | **no** | broadcast raw webhook to all `github.peer_bridges`, return 200 (if `peer_bridges` empty → 200 no-op) |
| present | yes | process locally |
| present | **no** | **drop** (`github_relay_no_owner` log line), return 200 — **never re-relay** |

A relayed event that no peer owns is dropped with `github_relay_no_owner` and **no
reaction** (the helpful orphan reply is deferred to Phase 101). Self-loops (this
install's own URL in its `peer_bridges`) cost one wasted hop but cannot loop — the
relayed copy carries `X-KM-Relayed: 1` and is terminal. `km doctor` WARNs on the
self-loop anyway (see below).

### Config surface

Opt in by adding `github.peer_bridges` to `km-config.yaml` — a list of the **other**
installs' GitHub bridge Function URLs:

```yaml
github:
  repos:
    - match: "kph-org/*"
      profile: github-review
  # Phase 100: federated relay. List EVERY OTHER install's GitHub bridge
  # Function URL (km github status → bridge-url). Absent/empty ⇒ federation off.
  peer_bridges:
    - https://sec000.lambda-url.us-east-1.on.aws/
```

`github.peer_bridges` → `KM_GITHUB_PEER_BRIDGES` (comma-joined) is exported to the
bridge Lambda by `km init`. Find each install's URL with `km github status`
(`bridge-url`).

### Deploy sequence

Federation is an **env-block** change (`KM_GITHUB_PEER_BRIDGES`), so it requires a
full terragrunt apply — **NOT** `km init --sidecars`:

```bash
# On EACH install where github.peer_bridges changed:

# 1. CLEAN rebuild of all Lambda zips. `make build` alone does NOT rebuild the
#    bridge zip (memory: project_km_init_skips_existing_lambda_zips); a stale zip
#    would ship without the relay code.
make build-lambdas

# 2. Full terragrunt apply — updates the Lambda environment.variables block with
#    KM_GITHUB_PEER_BRIDGES. `km init --sidecars` rebuilds the zip + cold-starts
#    but does NOT touch the env block (memory: project_km_init_lambdas_doesnt_deploy),
#    so the relay would stay silently off.
km init --dry-run=false

# 3. Verify.
km doctor          # → "GitHub peer bridges" OK / WARN
```

> **NOT `--sidecars`.** The `KM_GITHUB_PEER_BRIDGES` env var lives in the
> `lambda-github-bridge` module's `environment.variables` block, which only updates
> on a full `km init --dry-run=false` apply. Use `make build-lambdas` (clean) +
> `km init --dry-run=false` — see memory `feedback_km_init_full_apply`.
>
> **Phase 105 scoped shortcut (config-key edits only):** once the relay is already
> deployed and you only need to update `github.peer_bridges` in `km-config.yaml`:
> ```bash
> km init --github --dry-run=false   # applies lambda-github-bridge only (env+IAM)
> ```
> Code changes still need `make build-lambdas` + full `km init --dry-run=false`.

The `lambda-github-bridge` module is edited **in place at `v1.1.0`** (additive env
var, `default=""`, backward-compatible) — no version bump, no `source =` change.

### Dormancy / byte-identity invariant

When `github.peer_bridges` is **absent or empty**:

- `KM_GITHUB_PEER_BRIDGES` is empty; the bridge's relayer is `nil`.
- The `Resolve()`-miss path returns 200 with no broadcast — **byte-identical** to
  Phase 97/98 (the `Resolve()` reorder is a pure scale fix and produces identical
  dispatch outcomes whether or not federation is on).
- **No SandboxProfile schema change** ⇒ **no `km init --sidecars`, no sandbox
  recreate.** Existing sandboxes are untouched.
- `km doctor` SKIPs the `GitHub peer bridges` check silently.

### km doctor — peer bridges

`km doctor` adds a **`GitHub peer bridges`** check (gated on `github.repos` being
configured), mirroring the Slack `checkSlackPeerBridges`:

| Condition | Result |
|---|---|
| `github.peer_bridges` empty | **SKIPPED** — federation off |
| Any entry malformed (bad URL / no scheme or host) | **WARN** — naming the bad entry |
| Any entry == this install's own bridge URL (self-loop) | **WARN** — remove it |
| All entries well-formed, no self-loop | **OK** |

The own bridge URL is read from SSM `{prefix}config/github/bridge-url` (the same
param `GitHub Bridge URL` reads). When that param is unavailable the self-loop check
degrades gracefully (skipped) but the malformed-URL check still runs.

```
⚠ GitHub peer bridges
  self-loop detected in github.peer_bridges:
  https://kph000.lambda-url.us-east-1.on.aws/ is this install's own bridge URL — remove it
  → Remediation: Remove this install's own bridge URL from km-config.yaml
    github.peer_bridges. Run `km init --dry-run=false` after fixing.
```

### Correctness invariant (documented, not enforced)

> **Each repo must be owned by exactly one install** — one `github.repos:` entry
> across the whole fleet. Per-sandbox routing is safe by construction; for shared
> repos, register the repo on exactly one install. Two installs both matching the
> same repo would both dispatch (double-processing). This is **documented, not
> enforced** by the relay — `km doctor` validates URL hygiene, not cross-install
> ownership uniqueness.

The live two-install/one-App verification is in the phase UAT runbook
(`.planning/phases/100-*/100-UAT.md`).

---

## Phase 99.1 — Inbound poison-message DLQ (FIFO wedge fix)

The per-sandbox github-inbound FIFO queue (`{prefix}-github-inbound-{sandbox-id}.fifo`)
delivers comment-trigger envelopes to the sandbox poller, which dispatches an agent turn.
Before Phase 99.1, a *poison message* — an envelope whose agent turn fails every time —
would **head-of-line-block** its FIFO message group **forever**: SQS will not deliver a
later message in the same group until the failed one is acknowledged, and a poller restart
does not clear it (only a queue purge does). Surfaced in the Phase 99 UAT.

**Fix:** a shared **per-install FIFO dead-letter queue** plus a `RedrivePolicy` on the
source queues:

- **Shared DLQs** (one pair per install, not per sandbox), created at `km init` by the
  `sqs-inbound-dlq` Terraform module:
  - `{prefix}-github-inbound-dlq.fifo`
  - `{prefix}-slack-inbound-dlq.fifo`
- **RedrivePolicy** on every per-sandbox inbound FIFO queue:
  `maxReceiveCount = 3` → after 3 failed receives, SQS auto-evicts the poison envelope
  to the matching DLQ, **unblocking the FIFO group** so subsequent turns flow.
- DLQs are FIFO (a FIFO source queue can only redrive to a FIFO DLQ) with **14-day
  retention** so an operator has time to inspect or redrive poison messages.
- No SandboxProfile schema change; no poller-shell change; the source-queue
  `RedrivePolicy` is injected purely at the SQS-attribute layer.

### km doctor — inbound DLQ depth

`km doctor` reports an **Inbound DLQ depth** check:

| State | Condition |
|---|---|
| **SKIP** | No SQS client configured, OR neither shared DLQ exists (dormant — inbound never provisioned). |
| **OK** | One or both DLQs present and **empty** (no poison messages). |
| **WARN** | At least one DLQ holds **> 0** messages — names the count and points at `aws sqs receive-message` / `purge-queue` / redrive. |

A WARN means an agent turn failed 3× for some envelope — inspect the source poller logs.

```bash
# Inspect a poison message before deciding to redrive or purge:
aws sqs receive-message --queue-url \
  https://sqs.<region>.amazonaws.com/<account>/<prefix>-github-inbound-dlq.fifo
```

### Deploy sequence (Phase 99.1)

```bash
# 1. Rebuild the km binary (carries the new regionalModules() entry — a stale km
#    silently skips the sqs-inbound-dlq module). Memory: project_make_build_precedes_km_init.
make build

# 2. Clean-rebuild the Lambda zips (avoids the km-init-skips-existing-zips trap).
make build-lambdas

# 3. Full terragrunt apply — creates the two shared DLQs + the source-queue RedrivePolicy
#    is applied to NEW queues on the next km create.
#    NOT --sidecars: a new Terraform module + IAM require a full apply (env-block/IAM
#    changes are invisible to --sidecars). Memory: feedback_km_init_full_apply.
km init --dry-run=false

# 4. Existing sandboxes do NOT gain the RedrivePolicy retroactively (no silent backfill) —
#    their already-created queues keep the pre-99.1 attributes. Recreate to attach redrive:
km destroy <sandbox-id> --remote --yes && km create profiles/github-review.yaml --alias <alias>
```

**No `cmd/create-handler/main.go` change was required:** the create-handler Lambda only
*drains* envelopes into a pre-existing queue — it never creates the queue, so the
RedrivePolicy injection happens entirely in the `km create` warm path + the shared module.

**IAM:** no new grant. The existing `{prefix}-github-inbound-*.fifo` /
`{prefix}-slack-inbound-*.fifo` operator-policy wildcards already match `-dlq.fifo`
(Create/Delete/GetQueueAttributes/SetQueueAttributes/ListQueues/TagQueue). The sandbox EC2
role needs **no** DLQ grant — the poller never reads the DLQ (SQS moves poison messages
there automatically).

### Dormant invariant (99.1)

When no inbound integration is configured, the shared DLQs are never provisioned, no
source queue carries a `RedrivePolicy`, the doctor check **SKIPs**, and runtime is
**byte-identical** to pre-Phase-99.1.

---

## Phase 101 — Orphan-repo helpful reply (front-door default router)

> **Phase 101 (2026-06-08) — Orphan-repo helpful reply (complete):**
> When the shared bot is @-mentioned in a PR or issue comment on a repo **no install
> owns**, the front-door install posts ONE guidance comment naming how to wire the
> repo (`github.repos:` in `km-config.yaml` + `km init`). Dormant by default — set
> `github.default_router: true` on the front-door install to activate. GitHub analog
> of the Slack Phase 96 default router (`docs/slack-notifications.md` § Phase 96).

Phase 100 shipped a fire-and-forget relay: when no install owns a relayed repo, the
event was silently dropped (`github_relay_no_owner` log). Phase 101 closes that gap
— the front door detects a **true orphan** (zero claims from any peer) and posts a
single helpful guidance comment on the PR or issue.

### Mechanism: claim-aware scatter-gather

Phase 101 upgrades Phase 100's fire-and-forget `Broadcast` to a **claim-aware
scatter-gather** — the same pattern the Slack Phase 96 default router uses:

1. Front door runs `Resolve(owner/repo)` → **no match** → it broadcasts the raw
   webhook verbatim to every URL in `github.peer_bridges` (Phase 100 behavior).
2. **Each relayed-to peer** returns `200 {"claimed": bool}` JSON:
   - If the peer **owns** the repo (matched + dispatched): `{"claimed": true}`.
   - If the peer does **not** own the repo: `{"claimed": false}`.
3. The front door **tallies** the claim results:
   - **Any `claimed:true` from any peer** → the owner handled it → front door does
     nothing. No guidance comment.
   - **Zero `claimed:true` from all peers** → true orphan → front door posts ONE
     guidance comment on the PR/issue.

### Rollout-safe mixed fleet

A peer still on **Phase-100 code** returns a plain `200 "ok"` body (no JSON). The
front-door tally **treats any legacy/non-JSON/HTTP-error/timeout response as
`claimed:true`** — it NEVER produces a false "nobody owns this". Upgrade peers in any
order; no coordinated cutover required. This mirrors Slack Phase 96's "Legacy
Phase-95 peer responses treated as claimed:true" invariant.

### Guidance comment content

When a true orphan is detected, the front door posts a comment like:

> No klanker sandbox is bound to `acme/widgets`. To enable the bot here, an operator
> must add this repo under `github.repos:` in `km-config.yaml` and run
> `km init --dry-run=false`. See `docs/github-bridge.md`.

The comment is posted using the GitHub App installation token
(`InstallationCommenter.PostComment`) with the payload's `Installation.ID` — the
same credential path used for PR reviews in Phase 99.

### Cooldown

To avoid comment spam on a busy PR (multiple @-mentions in a short window), Phase 101
imposes a **per-(repo, PR/issue number) cooldown of 3600 seconds** via the shared
nonces table:

- Cooldown key: `gh-router-cooldown:{owner}/{repo}#{number}` (e.g.
  `gh-router-cooldown:acme/widgets#42`).
- Uses `DynamoGitHubNonceStore.CheckAndStore` — the same conditional-put-with-TTL
  path the Phase 100 deduplication uses.
- While the cooldown key is active, a second orphan @-mention on the same PR posts
  **no second comment**.
- No new DynamoDB tables; the existing `{prefix}-bridge-nonces` table is reused.

### Config surface

Set `github.default_router: true` on the **front-door install only** in
`km-config.yaml`:

```yaml
# km-config.yaml — FRONT-DOOR INSTALL ONLY
github:
  default_router: true   # Phase 101: post guidance comment on unowned-repo @-mentions
  peer_bridges:          # Phase 100: list every other install's bridge URL
    - https://sec000.lambda-url.us-east-1.on.aws/
```

`github.default_router` → `KM_GITHUB_DEFAULT_ROUTER` (bool string `"true"` or
`"false"`) is exported to the bridge Lambda by `km init`. Absent or `false` →
`KM_GITHUB_DEFAULT_ROUTER` is `"false"` → **byte-identical to Phase 100** (no
claim-gather overhead, no comment, terminal drop on unowned relay).

### Dormancy / byte-identity invariant

When `github.default_router` is **absent or false**:

- `KM_GITHUB_DEFAULT_ROUTER` is `"false"`; the orphan-post code path is never
  entered.
- Phase 100 fire-and-forget behavior is preserved: unowned relayed events are dropped
  with `github_relay_no_owner` and 200.
- **No SandboxProfile schema change** ⇒ **no `km init --sidecars`, no sandbox
  recreate.**

### Deploy sequence (Phase 101)

`github.default_router` → `KM_GITHUB_DEFAULT_ROUTER` is an **env-block change**, so
it requires a full terragrunt apply on the **front-door install** — **NOT** `km init
--sidecars`:

```bash
# 1. Edit km-config.yaml on the FRONT-DOOR install:
#    github:
#      default_router: true
#      peer_bridges: [...]   # Phase 100 — already set

# 2. CLEAN rebuild of all Lambda zips.
#    Memory: project_km_init_skips_existing_lambda_zips — must clean before km init.
make build-lambdas

# 3. Full terragrunt apply — updates the Lambda environment.variables block with
#    KM_GITHUB_DEFAULT_ROUTER. NOT --sidecars: env-block changes require full apply.
#    Memory: feedback_km_init_full_apply — use km init --dry-run=false.
km init --dry-run=false

# 4. Verify the env var reached the Lambda:
aws lambda get-function-configuration \
  --function-name ${KM_RESOURCE_PREFIX}-github-bridge \
  --query 'Environment.Variables.KM_GITHUB_DEFAULT_ROUTER'
# Expected: "true"

# 5. Run km doctor (optional — no dedicated doctor check for default_router,
#    mirroring Slack 96 which also ships no doctor check).
km doctor
```

> **Peers do NOT need `github.default_router: true`** — only the front-door install.
> However, peers MUST be on Phase-101 code to emit `{"claimed": bool}` JSON. If a
> peer is still on Phase-100 code, its plain-200 response is tallied as `claimed:true`
> (rollout-safe) — no false orphan comment, but the peer-claim signal is absent until
> it upgrades. Upgrade peers at your own pace.

> **NOT `--sidecars`:** the `KM_GITHUB_DEFAULT_ROUTER` env var lives in the
> `lambda-github-bridge` module's `environment.variables` block, which only updates on
> a full `km init --dry-run=false` apply. See memory `feedback_km_init_full_apply` and
> `project_km_init_lambdas_doesnt_deploy`.
>
> **Phase 105 scoped shortcut (config-key edits only):** for a pure `github.default_router`
> flip with no code change needed:
> ```bash
> km init --github --dry-run=false   # applies lambda-github-bridge only (env+IAM)
> ```
> Completes in seconds; `km init --plan` afterward shows no drift.

**No SandboxProfile schema change ⇒ no sandbox recreate.**

### Troubleshooting

**Guidance comment never appears:**

1. Verify `github.default_router: true` is set on the **front-door** install, NOT
   a peer.
2. Confirm `KM_GITHUB_DEFAULT_ROUTER=true` reached the Lambda:
   ```bash
   aws lambda get-function-configuration \
     --function-name ${KM_RESOURCE_PREFIX}-github-bridge \
     --query 'Environment.Variables.KM_GITHUB_DEFAULT_ROUTER'
   ```
3. Check that the GitHub App is **installed on the target repo** (not just authorized
   by the org). `Installation.ID == 0` means the App cannot post comments — install
   it on the repo first.
4. Check for an active cooldown: the comment fires only once per (repo, number) per
   3600s. If the cooldown key `gh-router-cooldown:{owner}/{repo}#{number}` is set,
   wait for it to expire (TTL 3600s) or delete it from DDB.
5. Confirm the comment was not an unqualified message (no @-mention). The mention gate
   runs before `Resolve()`, so the orphan path only fires on confirmed @-mentions.

**A false orphan comment appeared (owned repo got a guidance comment):**

This should not happen — see Rollout-safe mixed fleet above. If it does:
1. Confirm the owning peer is returning `{"claimed": true}` (Phase-101 code).
2. Check Lambda logs on the front door for the `claimResults` tally.
3. If the peer is returning a non-2xx error or timing out, its response is tallied as
   `claimed:true` — check peer health first.

**See also:** `docs/slack-notifications.md` § Phase 96 for the Slack analog
(`slack.default_router`), which this phase mirrors.

---

## Phase 102 — Agent verbs (/claude, /codex)

> **Phase 102 (2026-06-08) — GitHub bridge agent verbs: /claude and /codex select the
> per-thread agent in a PR comment (Slack Phase 70 analog).**
>
> An @-mention in a PR/issue comment may now include `/claude` or `/codex` anywhere
> in the body (code-stripped, ≤1 agent verb per comment). The verb selects the agent
> for the entire thread and is persisted in `km-github-threads` as `agent_type`.
> Subsequent turns in the same thread use the stored type unless overridden. Back-compat:
> a comment with no verb is byte-identical to Phase 101 behavior.
> No SandboxProfile schema change. No new Terraform resources.

### Syntax and composition

A PR/issue comment may include `/claude` or `/codex` **anywhere** in the body, in
combination with a Phase 99 `/command` or as a free-form turn:

```
@km-bot /claude review the auth module        # agent verb only (free-form body)
@km-bot /codex /patch fix the flaky tests     # agent verb + command composition
@km-bot /codex                                # switches the thread to Codex, no prompt
@km-bot just look at this                     # no verb → thread's stored agent_type applies
```

**Parsing rules:**

1. `StripCode` removes fenced `` ``` `` blocks and `` `inline code` `` spans before
   scanning so `/claude` or `/codex` inside a code example is never recognized.
2. Agent verbs are intercepted **before** the Phase 99 command map — they are on a
   separate axis. `/codex /patch fix X` resolves to `AgentVerb=codex, Known=[patch]`.
3. Two distinct agent verbs in the same comment (`/claude /codex`) → the bridge posts
   a "Specify one agent" error reply and returns 200 without dispatching.
4. Same verb twice (`/codex /codex`) → deduplicated; treated as one `/codex`. No error.
5. Unknown slash tokens (`/frobnicate`) are silently ignored (lenient passthrough).

### Per-thread persistence and precedence

When the bridge dispatches a turn, it writes `agent_type` to the `km-github-threads`
row (the same DDB item that records `sandbox_id` and `session_id` for Phase 97/98
thread continuity). On subsequent turns:

| Condition | Effective agent |
|---|---|
| Comment has `/claude` or `/codex` verb | verb wins (overrides thread row + profile default) |
| No verb; thread row has `agent_type` | thread row's `agent_type` |
| No verb; no thread row (fresh PR) | profile `spec.agent.default` (default: `claude`) |

Switching agents mid-thread (e.g. from `/claude` to `/codex`) updates the thread row
so future turns inherit the new agent. There is no "cross-agent session handoff"
ceremony in the GitHub path (unlike the Slack Phase 70 handoff) — GitHub turns are
always discrete, single-turn dispatches.

### Codex-capable-profile precondition

`/codex` routes to the **sandbox**. If the sandbox was created from a profile that
does not install Codex (e.g. the lean `github-review.yaml` default), the poller posts
a helpful error comment and acks the queue message:

> This sandbox's profile has no Codex; /codex is unavailable here.

To use `/codex`, the operator must:
1. Create a Codex-capable profile that installs Codex CLI (`spec.initCommands` or a
   pre-baked AMI) and sets `spec.agent.default: codex`.
2. Add the repo entry with that profile: `github.repos[].profile: profiles/my-codex-profile.yaml`.
3. Recreate the sandbox: `km destroy <id> && km create profiles/my-codex-profile.yaml --alias gh-myrepo`.

The lean `profiles/github-review.yaml` ships as a Claude-only baseline; it has no
Codex installation and does NOT set `spec.agent.default: codex`.

### Reserved tokens and km doctor

`/help`, `/claude`, and `/codex` are **reserved built-in verbs**. Defining a
`github.commands` entry with one of these names would shadow the built-in and confuse
users. `km doctor` WARNs on each reserved name found in `github.commands`:

```
WARN  GitHub commands config   command "claude" shadows the reserved built-in /claude verb — rename to avoid unexpected behavior
```

The remediation is to rename the command key (e.g. `claude-review: …`).

### /help extension

The bridge's built-in `/help` reply now advertises the available agent verbs in
addition to the Phase 99 command list:

```
**Available agents:**
- /claude — dispatch this thread to Claude
- /codex  — dispatch this thread to Codex

**Current thread agent:** `codex`    ← shown only for known threads with a stored agent_type

**Available commands:**

- /patch  — apply the smallest fix
- /review — read-only review, inline findings

**Default:** /review (used when no command is specified)
```

When the thread has no row yet (fresh PR), the "Current thread agent" line is omitted.

### Back-compatibility

A comment with no `/claude` or `/codex` token is **byte-identical** to Phase 101
behavior:

- `AgentVerb` is `""` → the envelope's `Agent` field is `""` → the poller uses the
  profile-default agent type → the thread row's `agent_type` is written as the profile
  default, same as Phase 101.
- No change to `X-KM-Relayed` semantics, claim-gather, cooldown, or nonce deduplication.

### Deploy surface

Phase 102 adds:

- **Bridge Lambda (`km-github-bridge`)** — new agent-verb parsing + conflict reply.
- **Source-aware poller** (sandbox-side userdata) — `THREAD_AGENT_TYPE` env var, D6
  Codex-guard check.

Phase 102 touches **NO** new Terraform resources and **NO** SandboxProfile schema fields.

```bash
# 1. CLEAN rebuild of all Lambda zips.
#    Phase 102 modifies the bridge Lambda (pkg/github/bridge/) AND the compiled
#    userdata (pkg/compiler/userdata.go → poller script embedded in create-handler).
#    Rebuild both:
make build-lambdas

# 2. Full terragrunt apply — redeploys bridge + create-handler Lambda.
#    NOT --sidecars: this is a code change to Lambdas managed by their TF module.
#    --sidecars rebuilds the km binary and cold-starts sidecars but does NOT
#    update the Lambda code (memory: feedback_km_init_full_apply).
#    NOTE: for a subsequent pure config-key edit (no code change), use Phase 105
#    shortcut: km init --github --dry-run=false (applies bridge module only).
km init --dry-run=false

# 3. Existing sandboxes must be recreated to gain the new poller
#    (which carries the THREAD_AGENT_TYPE env var and D6 guard).
#    The bridge Lambda update is instant — new agent-verb parsing fires on the NEXT
#    webhook delivery without sandbox recreate.
km destroy <sandbox-id> --remote --yes
km create profiles/github-review.yaml --alias gh-myrepo

# 4. Verify.
km doctor
```

**What needs `km destroy && km create`:** The source-aware poller is embedded in the
userdata bootstrap (compiled by the create-handler Lambda). Only **newly created**
sandboxes get the Phase 102 poller. Existing sandboxes continue running the Phase 101
poller — they dispatch correctly but do not read `THREAD_AGENT_TYPE` from the envelope
and do not emit the D6 Codex-guard comment.

**NOT required:** `km init --sidecars` — there is no SandboxProfile schema change.
Agent-type persistence is schema-on-write: the DDB `agent_type` column was added in
Phase 102 Plan 02 and is written by the bridge; no TF migration required.

### Two-install / one-App UAT: agent-verb end-to-end

After deploying (bridge + poller, sandbox recreated):

**A. Agent verb selects Claude:**
1. Post `@km-bot /claude look at the auth fix` on an open PR in a configured repo.
2. Bridge emits 👀; envelope carries `"agent": "claude"`.
3. Poller writes `agent_type=claude` to `km-github-threads`; dispatches Claude.
4. Claude posts a review via `km-github review`.

**B. Agent verb selects Codex (Codex-capable sandbox):**
1. Post `@km-bot /codex /patch fix the flaky test` on the same PR.
2. Bridge emits 👀; envelope carries `"agent": "codex"`, `"body"` = args without `/codex /patch`.
3. Poller writes `agent_type=codex`; dispatches Codex.
4. Codex applies the patch and posts a review.

**C. Thread continuity:**
1. Reply to the same PR thread with `@km-bot what did you change?` (no verb).
2. Bridge looks up the thread row; carries stored `agent_type` in the envelope.
3. Same agent (Codex from step B) handles the follow-up.

**D. Agent switch mid-thread:**
1. Reply with `@km-bot /claude review what Codex did`.
2. Bridge parses `/claude`; `agent_type=claude` is written over the prior `codex` row.
3. Claude handles this turn and future turns (until another verb appears).

**E. Conflict error:**
1. Post `@km-bot /claude /codex do something`.
2. Bridge detects `AgentVerbConflict=true`; posts "Specify one agent" reply; returns 200. No dispatch.

**F. Codex on Claude-only sandbox (D6 guard):**
1. Post `@km-bot /codex review this` on a PR whose sandbox was created from `github-review.yaml`.
2. Bridge emits 👀; poller checks `command -v codex` → not found.
3. Poller posts "This sandbox's profile has no Codex; /codex is unavailable here." and acks.

**G. km doctor reserved-verb check:**
1. Add `claude: {prompt: "Custom prompt."}` to `github.commands` in `km-config.yaml`.
2. Run `km doctor`.
3. Expect `WARN GitHub commands config` mentioning `"claude"` shadows the reserved verb.

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

---

## Phase 99 — Config-defined /commands

> **Phase 99 (2026-06-07) — Config-defined /command dispatch (complete):**
> Operators declare named commands in `km-config.yaml github.commands:`. Each command
> maps a `/verb` in a PR comment to a prompt template (inline or `@file`), an optional
> alias override, and an optional allow list. A `default_command` fires when no `/verb`
> is present. The bridge reads the command map from SSM at cold start — the deploy
> footprint is unchanged (no new Lambda, no new DDB table, no sandbox recreate).

### The github.commands Config Surface

Add a `commands:` block under `github:` in `km-config.yaml`:

```yaml
github:
  default_command: review          # install-wide default when no /verb in comment
  repos:
    - match: my-org/*
      alias: gh-myorg
      profile: profiles/github-review.yaml
      allow: [alice, bob]
      default_command: triage      # per-repo override (beats install-wide)

  commands:
    review:
      description: "Read-only review — posts inline findings as a PR review"
      alias: gh-myorg              # optional: route to a different sandbox than repo default
      profile: ""                  # optional: override profile for cold-create
      allow: [alice, bob, carol]   # optional: inner gate (must also pass repo.allow)
      prompt: |
        You are a careful code reviewer. Review this pull request for correctness,
        performance, and style. Use {{args}} as extra context if provided.
        Post your findings as a structured PR review via `km-github review`.

    patch:
      description: "Apply the smallest safe fix and push a commit"
      alias: gh-myorg-dev          # route to a dedicated dev sandbox
      profile: profiles/github-dev.yaml
      prompt: "@gh-patch.txt"   # @file — bare name → profiles/gh-patch.txt (see search path below)

    triage:
      description: "Classify severity, reproduce, and label the issue"
      prompt: "Triage this issue. Classify severity (P0-P3), add labels, reproduce if possible. {{args}}"
```

**Fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `description` | no | Human-readable label shown in `km github status` and `/help` reply |
| `alias` | no | Sandbox alias override (default: repo's `alias`) |
| `profile` | no | SandboxProfile path override for cold-create (default: repo's `profile`) |
| `allow` | no | Inner gate — sender must be in BOTH `repo.allow` AND `command.allow` |
| `prompt` | yes | Template text or `@file` reference. `{{args}}` is replaced with the comment text after the `/verb` |

**Routing (Decision D2):** command settings override repo settings. Resolution:
- `alias` = `command.alias || repo.alias`
- `profile` = `command.profile || repo.profile || github.default_profile`

### `@file` Prompt References

`prompt: "@path/to/file.txt"` reads the file at `km init` time on the operator's workstation.
The file contents are inlined into the SSM parameter before the Lambda ever reads it — the
Lambda never reads the filesystem (no `/tmp`, no extraction; the resolution is purely
operator-side).

**Search path** (resolved against the `km-config.yaml` directory, **not** the shell CWD):

1. `<config-dir>/<path>` — explicit form, e.g. `@profiles/gh-review.txt` or `@sub/dir/x.txt`
2. `<config-dir>/profiles/<path>` — fallback, so a bare `@gh-review.txt` resolves to
   `profiles/gh-review.txt` without spelling out the prefix

`profiles/` is the conventional home for command prompt templates, so the bare form is the
recommended style (`prompt: "@default.github.prompt.txt"` → `profiles/default.github.prompt.txt`).
The explicit `@profiles/...` form keeps working unchanged.

**Rules:**
- `@file` → inlined at `km init` time; missing on the whole search path = hard `km init` error
  + `km doctor` WARN (the WARN lists every path searched)
- `@@text` → escaped literal `@text` (no file read)
- Inline text (no `@` prefix) → used as-is

### `{{args}}` Template Variable

The only template variable is `{{args}}`. After stripping the `@mention` and the `/command`
token from the comment body, the remaining text replaces every occurrence of `{{args}}` in the
prompt. If no remaining text, `{{args}}` is replaced with an empty string.

Example: comment `@km-bot /review please focus on error handling` → `{{args}}` = `please focus on error handling`.

### Command Dispatch Rules

**Command located anywhere:** The `/command` token can appear anywhere in the comment body
(not anchored after the mention). Code blocks are stripped first to avoid false positives
(`/usr/bin/patch` in a shell block does not trigger `/patch`). (Decision D3)

**Multiple commands → error reply:** If two or more distinct commands appear in one comment,
the bridge posts a polite error reply and does NOT dispatch. Repeated identical commands are
deduped and treated as one. (Decision D5)

**Unknown `/token` → lenient passthrough:** A `/token` not in the command map is treated as
plain text — the comment body dispatches as free-form (or via `default_command`). No
unknown-command error reply. (Decision D6)

**Auth intersection (Decision D7):**
1. `repo.allow` is the **outer gate** — failing it causes a silent drop (no reply). This is
   the Phase 97 behavior, unchanged.
2. `command.allow` is the **inner gate** — the sender must pass `repo.allow` first; if they
   pass repo.allow but fail command.allow, the bridge posts a polite "not authorized for this
   command" reply.

### `default_command`

`github.default_command` (install-wide) and `repos[].default_command` (per-repo) specify the
command key dispatched when no `/verb` is present in the comment. Resolution order:
1. Per-repo `repos[].default_command` (if set)
2. Install-wide `github.default_command` (if set)
3. Unset → free-form passthrough (the raw mention body is dispatched as-is, Phase 97/98 behavior)

Both values must name a key in `github.commands` — an undefined name is a hard `km doctor`
ERROR (not a WARN).

### `/help` Built-in

`/help` is a reserved built-in. Posting `/help` in a PR comment causes the bridge to reply
with a formatted list of all configured commands, their descriptions, and the effective
default for the repo. `/help` never dispatches to a sandbox. Defining a user command named
`help` in `km-config.yaml` shadows the built-in and triggers a `km doctor` WARN.

### km doctor Command Checks

`km doctor` adds the following checks to the existing GitHub check group. All checks are
**dormant (SKIPPED) when `github.commands` is absent** — byte-identical to pre-Phase-99.

| Check | Level | Trigger |
|-------|-------|---------|
| `@file` prompt missing/unreadable | WARN | command.prompt is `@file` but file not found on the search path (config-dir, then config-dir/profiles); the WARN lists every path searched |
| Profile unresolvable | WARN | command.profile path does not exist |
| `help` shadowed | WARN | user defines a command named `help` (reserved built-in) |
| Command↔repo alias overlap | WARN | command.alias equals a repo alias (explicit or auto-derived) |
| Undefined `default_command` (top-level) | ERROR | `github.default_command` names a key not in `github.commands` |
| Undefined `default_command` (per-repo) | ERROR | `repos[].default_command` names a key not in `github.commands` |
| SSM commands param absent | WARN | `github.commands` configured but `{prefix}/config/github/commands` not in SSM |

**Stale SSM param:** Removing `github.commands` from `km-config.yaml` on a subsequent
`km init --dry-run=false` will NOT automatically delete the SSM param (SSM writes are
additive). `km doctor` will no longer show the SSM-present WARN (it's dormant when commands
is absent), but the stale param remains in SSM until manually deleted:

```bash
aws ssm delete-parameter --name "$(km env | grep KM_RESOURCE_PREFIX | cut -d= -f2 | tr -d /)km/config/github/commands"
# or equivalently:
aws ssm delete-parameter --name "/km/config/github/commands"
```

### km github status Command Listing

`km github status` extends its output to list all configured commands (read from SSM at
runtime) and the effective default command per repo:

```
GitHub bridge config (prefix: /km/):
  webhook-secret:  [set]
  bot-login:       km-bot[bot]
  bridge-url:      https://abc123.lambda-url.us-east-1.on.aws/
  app-client-id:   Iv1.abc123
  installation-id: 99999999
  commands (2):
    /patch  — apply the smallest safe fix [→ alias:gh-myorg-dev profile:profiles/github-dev.yaml]
    /review — read-only review, inline findings [→ alias:gh-myorg]
  default_command: review (install-wide)
  repos (2):
    my-org/*                                 default_command: triage (per-repo)
    my-org/backend                           default_command: review (install-wide fallback)
```

When no commands are configured, the extra block is omitted — output is identical to
pre-Phase-99.

### Phase 99 Deploy Sequence

> **DEPLOY SURFACE VERIFICATION** (per `feedback_verify_deploy_surface_not_just_code`)
>
> Phase 99 ships zero new Terraform modules. The km-github-bridge Lambda already exists from
> Phase 97 and is already in `lambdaBuilds()`. The only deploy steps are:

```bash
# 1. Rebuild the bridge zip with Phase 99 command-pass code.
make build-lambdas

# 2. Upload the bridge zip + write the SSM commands param.
km init --dry-run=false
```

**That is all.** Specifically:

- **NOT required: `km init --sidecars`** — there is no SandboxProfile schema change in Phase 99.
  `--sidecars` rebuilds binaries and cold-starts the Lambda but does NOT update the Lambda
  environment block or the SSM commands param. Using `--sidecars` alone would leave the bridge
  running Phase 98 code without the command-pass slot.

- **NOT required: `make build`** — the km operator binary has no new `regionalModules()` entry
  (no new DDB table, no new Lambda from the km CLI perspective). `make build` rebuilds the
  operator CLI only; it does NOT rebuild the bridge Lambda zip. Use `make build-lambdas` to
  rebuild the Lambda.

- **NOT required: sandbox recreate (`km destroy && km create`)** — Phase 99 has no
  sandbox-side changes. The command pass runs in the bridge Lambda, not in the sandbox.
  Existing sandboxes benefit automatically on next webhook delivery after the Lambda code is
  updated.

**What `km init --dry-run=false` does for Phase 99:**
1. Calls `PublishGitHubCommandsToSSM` → resolves all `@file` prompts → assembles the
   `CommandSet` envelope (`{"commands": {...}, "default_command": "..."}`) → **base64-encodes
   it** → writes to SSM `{prefix}/config/github/commands` as a plain String (not SecureString).
   The base64 step is required: SSM rejects any value containing `{{...}}` (it reserves that for
   its own `{{ssm:...}}` reference syntax), and command templates use the `{{args}}` placeholder.
   The bridge's `SSMCommandsFetcher` and `km github status` base64-decode on read (with a
   raw-JSON fallback for robustness). The value is config, not a secret — base64 is encoding,
   not encryption; an operator inspecting the raw SSM param sees base64, but `km github status`
   renders the human-readable view.
2. Uploads the rebuilt `km-github-bridge.zip` to the Lambda function code (same as Phase 97
   deploy step 2).
3. Emits a drift WARN if the SSM commands param already exists with a different value
   (informational — the new yaml-derived value always wins; compared on decoded JSON).

**Cross-check against Plans 03/04:**
- Plan 03 (`PublishGitHubCommandsToSSM`): SSM write confirmed — `putSSMParam(ctx, ssmClient, prefix+"config/github/commands", commandsJSON, ParameterTypeString, "", overwrite=true)`. No discrepancy.
- Plan 04 (bridge cold-start): `SSMCommandsFetcher` reads `{prefix}/config/github/commands` at cold start (15-minute cache). ParameterNotFound → empty map → bridge runs dormant. No discrepancy.
- `lambdaBuilds()` list: km-github-bridge was added in Phase 97 (`init.go:1876`). Phase 99 does NOT modify this list — confirmed. `make build-lambdas` picks it up automatically.

**No discrepancies found between Phase 99 docs deploy claims and Plans 03/04 implementation.**

### Phase 99 E2E Verification Checklist

After completing the deploy sequence above, verify:

**A. COMMAND DISPATCH** — Post `@km-bot /review` on an open PR in a configured repo. Confirm:
- The bridge emits 👀 ACK immediately.
- The agent receives the command prompt (with `{{args}}` replaced by any trailing text).
- A PR review is posted by the agent via `km-github review`.

**B. DEFAULT COMMAND** — Post `@km-bot please check this` (no `/verb`) on a repo with
`default_command: triage`. Confirm the triage prompt is used, not the raw mention body.

**C. /HELP** — Post `@km-bot /help` on a PR. Confirm the bridge replies with a comment
listing all configured commands and the effective default for the repo. No sandbox dispatch.

**D. MULTI-COMMAND ERROR** — Post `@km-bot /review /patch` on a PR. Confirm the bridge
posts an error reply (not a dispatch, not a silent drop).

**E. UNKNOWN VERB** — Post `@km-bot /frobnicate this PR`. Confirm the comment dispatches
as free-form (lenient passthrough) — no error reply, no `/help` spam.

**F. COMMAND AUTH** — Configure a command with a narrow `allow: [alice]`. Post as `bob`
(who is in `repo.allow` but not `command.allow`). Confirm a "not authorized for this
command" reply is posted.

**G. `km doctor`** — Run `km doctor` after deploying. Confirm:
- All command checks show OK (green).
- SSM commands param check shows OK (param present).
- Removing the SSM param manually and re-running shows WARN with `km init` remediation.

**H. `km github status`** — Run `km github status`. Confirm the commands section appears
with all configured commands, their descriptions/targets, and the effective default per repo.

---

## Phase 106 — Resume-hint on bridge replies (post-on-mint)

> **Phase 106 (2026-06-11) — Session-resume hint on GitHub bridge replies (post-on-mint).**
>
> After a bridge agent turn completes, the sandbox-side poller posts ONE additional
> collapsed `<details>` comment carrying the operator resume handle. The hint fires only
> when the session id is new or changed (post-on-mint semantics), so a stable thread
> produces exactly one hint comment — typically on the first turn. Slack is
> deliberately excluded.

### What the hint contains

The collapsed `<details>` fold includes:

- **Sandbox id** (`$SANDBOX_ID`) — for context.
- **Run-from directory: `/workspace`** — the session transcript lives at
  `/home/sandbox/.claude/projects/-workspace/<session-id>.jsonl`, but `--resume` keys
  off the current working directory, so the resume command **must** be run from
  `/workspace` (not `/home/sandbox`).
- **Agent-correct resume command** — branched on `EFFECTIVE_AGENT`:
  - Claude: `claude --resume <session-id>`
  - Codex: `codex exec resume <session-id>`
- **The minted session id** — the freshly issued or resumed id from this turn.

Because PR comments are visible to all repo collaborators, the hint is wrapped in a
`<details>` fold (collapsed by default). The session ids themselves are not exploitable
without AWS/SSM access to the sandbox, so no redaction is applied.

### Post-on-mint semantics

The hint block is gated by a comparison of `NEW_GITHUB_SESSION` against the previously
stored session id (`${GITHUB_SESSION:-}`). It fires only when the value is non-empty
**and** differs from the stored value — i.e., on:

1. **First turn** — no stored session id; `NEW_GITHUB_SESSION` is always new.
2. **Gap-E cross-box re-mint** — a new sandbox was cold-created for the same alias;
   the session id changes.

Common case: exactly **one** hint comment per thread. If the same sandbox handles
all turns (warm path), subsequent turns produce no additional hint.

### Robustness

The hint post is best-effort — it runs as:

```bash
/opt/km/bin/km-github comment --repo "$REPO" --number "$NUMBER" --body "$HINT_BODY" || true
```

The `|| true` guard ensures a transient API error (rate-limit, network blip) never
blocks the SQS ack. The agent's main output is unaffected.

### Deploy surface

Phase 106 is a `pkg/compiler/userdata.go` change embedded in the **create-handler
Lambda zip**. There are no new Terraform resources, no SandboxProfile schema changes,
no new DDB columns, and no changes to bridge Lambdas or IAM.

```bash
# 1. Rebuild the create-handler Lambda zip (userdata.go is embedded here).
#    NOT --sidecars (--sidecars only re-uploads sidecar binaries, not the create-handler zip).
#    NOT km init --github/--h1 (those refresh bridge env+IAM only, not the create-handler zip).
make build-lambdas

# 2. Full terragrunt apply — uploads the new create-handler zip.
km init --dry-run=false

# 3. Existing sandboxes must be recreated to gain the new poller.
km destroy <sandbox-id> --remote --yes
km create profiles/github-review.yaml --alias gh-myrepo

# 4. Verify.
km doctor
```

**Bridge Lambdas / IAM / Terraform:** UNAFFECTED. No scoped `km init --github` step required.

**Slack poller:** EXCLUDED — byte-identical to pre-Phase-106. Operators can ask the
agent to share its session interactively in chat.

## Phase 108 — Per-turn idempotency guard (no duplicate PR comments)

> **Phase 108 (2026-06-12) — GitHub bridge per-turn idempotency guard.**
>
> A single @-mention could make the sandbox agent post **two byte-identical** PR
> comments (or reviews) seconds apart. The fix is a per-turn idempotency guard at the
> `km-github` helper layer — the single chokepoint every post flows through.

### Symptom

One `@bot`-mention → two identical issue comments ~5 s apart. Because the two posts are
*byte-identical* (not two independent generations, which would differ in prose), they
came from **one** agent generation published **twice** — the agent called
`km-github comment` (or `review`) twice in the same turn.

### Root cause

Both posts hit GitHub's comment/review API with the same body, and **GitHub issue
comments and reviews are not idempotent**. The agent double-posts because:

1. **Conflicting posting mandates.** The poller hard-codes a "post your reply
   (REQUIRED)" directive into every prompt. When the user *also* invokes a skill whose
   workflow tells the agent to post a PR review, the agent composes the body once and
   publishes it twice — once per instruction source.
2. **Self-retry of a call that secretly succeeded.** The agent's first
   `km-github comment` Bash call looks like it failed (slow GitHub response / ambiguous
   exit) so it re-runs it; the first had actually posted. ~5 s is classic retry timing.

This is **not** SQS redelivery (a redelivered envelope re-invokes `claude -p`, takes
minutes, and produces *different* prose), nor federated double-ownership (two installs →
two different sandboxes → two different runs → different text). The 300 s→1800 s
visibility guard (`pkg/compiler/userdata.go`) is unrelated and working.

### The guard — hidden marker + pre-post duplicate check

When posting a comment or review, the helper:

1. Embeds an invisible HTML-comment marker keyed to the current turn:
   `\n\n<!-- km-turn:$KM_GITHUB_TURN_ID -->` (HTML comments do not render in GitHub
   markdown). This is **separate** from the visible `<sub>🤖 via Claude</sub>`
   attribution footer (Phase 102 follow-up) — that footer stays.
2. **Before** posting, GETs the issue's existing comments (for `comment`) or the PR's
   reviews (for `review`) and scans each body for the same marker. If found, it
   **no-ops** (exit 0, logs `duplicate suppressed (km-turn:… already posted)`).
3. Otherwise appends the marker to the body and POSTs as before.

**Per-turn, not per-PR.** The marker is keyed on the poller's `RUN_ID` (one value per
dispatched turn), so two *separate* legitimate mentions on the same PR each compute a
different marker and each post. A bodyless `APPROVE` review gets the marker as its whole
body, so even APPROVE is idempotent.

**Fail-open.** If the pre-post duplicate-check GET errors (ratelimit / 5xx / transport),
the helper logs it and **posts anyway** — a failed *read* must never strand a legitimate
*reply*. The scan paginates (`per_page=100`, following `Link: rel="next"`) so a comment
posted seconds earlier — which sorts last — is still found.

### `KM_GITHUB_TURN_ID` plumbing

The poller exports `KM_GITHUB_TURN_ID='$RUN_ID'` inline into all four agent-dispatch
`sudo -u sandbox bash -lc` blocks of `km-github-inbound-poller` (codex resume, codex
first-turn, claude main, claude `--resume` retry), mirroring how `KM_GITHUB_REPLY_AGENT`
is exported. An **empty** `KM_GITHUB_TURN_ID` (every manual `km-github` invocation)
disables both the marker append and the duplicate-check ⇒ byte-identical to
pre-Phase-108.

### Files

- `pkg/github/marker.go` *(new)* — `TurnMarker(id)`, `CommentMarkerExists(...)`,
  `ReviewMarkerExists(...)` (paginated list + marker scan; fail-open contract).
- `cmd/km-github/main.go` — `runCommentWith` / `runReviewWith` take a `turnID`: pre-post
  check + marker append. `KM_GITHUB_TURN_ID` read in the outer `runComment` / `runReview`.
- `pkg/compiler/userdata.go` — export `KM_GITHUB_TURN_ID` into the four poller dispatch
  blocks.

No SandboxProfile schema change, no new TF resource, no new DDB column, no bridge Lambda
or IAM change. Slack and HackerOne pollers untouched.

### Deploy sequence (Phase 108)

The change spans **both** the create-handler-compiled userdata (the env-var export)
**and** the `km-github` sidecar binary (the marker logic). A full `km init` covers both:
it applies the new create-handler zip **and** rebuilds+uploads the `km-github` sidecar
via `buildAndUploadSidecars` (the helper is delivered to the box from
`s3://<artifacts>/sidecars/km-github`, **not** by `make sidecars`).

```bash
# 1. Rebuild the create-handler Lambda zip (embeds the new userdata).
make build-lambdas

# 2. Full terragrunt apply — uploads the new create-handler zip AND re-uploads the
#    km-github sidecar binary. NOT --sidecars (that skips the create-handler zip);
#    NOT km init --github (that refreshes only the bridge Lambda env+IAM).
km init --dry-run=false

# 3. Existing sandboxes must be recreated to gain the new userdata env-var export
#    and to download the freshly-uploaded km-github sidecar at boot.
km destroy <sandbox-id> --remote --yes
km create profiles/github-review.yaml --alias gh-myrepo

# 4. Verify.
km doctor
```

### Verification

- **Unit:** given a fake GitHub API that already returns a comment/review containing
  `<!-- km-turn:ABC -->`, `runCommentWith` / `runReviewWith` with `turnID=ABC` must
  **not** POST (skip + exit 0); with a fresh/absent turn id they POST (marker appended);
  a failing duplicate-check GET still POSTs (fail-open). Covered in
  `cmd/km-github/marker_cmd_test.go` and `pkg/github/marker_test.go`.
- **Manual:** drive the agent to post the same body twice in one turn → exactly one
  comment lands. Two separate mentions → two comments.

## Phase 109 — Self-heal orphaned `stopped` alias rows (resume-or-cold-create)

> **Phase 109 (2026-06-12) — GitHub bridge self-heals an orphaned `stopped` row.**
>
> When a `{prefix}-sandboxes` row lingers with `status=stopped` but its EC2 instance
> is **gone** (terminated out from under km), the resume path used to log a non-fatal
> error and enqueue to a per-sandbox FIFO with **no live poller** — the message stranded
> and the bot silently no-op'd on cold start. The fix: detect "no resumable instance",
> delete the stale row, and **cold-create** instead.

### Symptom

A PR @-mention on a long-idle alias produced:

```
INFO  github-bridge: auto-resume alias=github-bot sandbox_id=github-e903e795 status=stopped
ERROR github-bridge: auto-resume failed (non-fatal; enqueue continues)
      err="github-bridge: no stopped/stopping EC2 instances found for sandbox github-e903e795 (tag sec:sandbox-id)"
```

The comment was then enqueued to `sec-github-inbound-github-e903e795.fifo` (a dead
queue) and never answered. Cold-create was unreachable because the `stopped` row still
held the alias, so `ResolveByAliasWithStatus` returned `(id, "stopped", nil)` —
`err == nil` skipped the cold-create branch. The orphaned row permanently shadowed
cold-create.

### The fix — terminal vs transient resume failure

`EC2Resumer.StartSandbox` now wraps an exported sentinel on the terminal "no instance"
path:

```go
var ErrNoResumableInstance = errors.New("github-bridge: no resumable EC2 instance")
// len(found)==0 → fmt.Errorf("...: %w", ErrNoResumableInstance)
```

A transient `DescribeInstances` / `StartInstances` API error is **not** wrapped — it keeps
the log-non-fatal + enqueue behavior so the FIFO redelivers once the box recovers.

`WebhookHandler.Handle` branches the resume failure with `errors.Is`:

- **`ErrNoResumableInstance`** → `StatusWriter.DeleteSandboxRow(sandboxID)` (clears the
  stale row so the alias becomes absent — avoiding the **ambiguous-alias trap** where a
  second row under the same alias would make every future comment resolve as ambiguous),
  then `Publisher.PutSandboxCreate(...)`. **No enqueue, no thread upsert.**
- **any other (transient) error** → unchanged: log non-fatal, enqueue, FIFO redelivers.
- **success** → unchanged: `SetStatusRunning` + enqueue.

A genuinely `stopped`/`paused` (hibernated) instance still reports `stopped`/`stopping`
to the filter and resumes exactly as before — only the no-instance case self-heals.

### Files

- `pkg/github/bridge/aws_adapters.go` — `ErrNoResumableInstance` sentinel + wrap in
  `StartSandbox`; `DynamoSandboxStatusWriter.DeleteSandboxRow` (single `DeleteItem` keyed
  by `sandbox_id`; `DynamoUpdateItemClient` widened with `DeleteItem`).
- `pkg/github/bridge/interfaces.go` — `SandboxStatusWriter` extended with `DeleteSandboxRow`.
- `pkg/github/bridge/webhook_handler.go` — the resume-branch `errors.Is` fork.
- `infra/modules/lambda-github-bridge/v1.1.0/main.tf` — `dynamodb:DeleteItem` added to the
  `DDBSandboxesUpdateItem` statement (still no `PutItem`).

### Deploy sequence (Phase 109)

Pure bridge-Lambda code + one IAM statement. **No SandboxProfile schema change → no
sandbox recreate.**

```bash
make build-lambdas           # rebuild the bridge Lambda zip
km init --dry-run=false      # full apply — picks up the new DeleteItem IAM statement
                             # (NOT --sidecars; --github also suffices ONLY if you accept
                             #  it refreshes env+IAM, which it does — IAM change is covered)
km doctor
```

### Verification

- **Unit:** `EC2Resumer.StartSandbox` with no matching instances returns an error for
  which `errors.Is(err, ErrNoResumableInstance)` is true; a `DescribeInstances` API error
  does **not** match. The handler test asserts the orphan path deletes the row + cold-creates
  and does **not** enqueue/upsert; genuinely-stopped and transient-error paths still enqueue.
  Covered in `pkg/github/bridge/aws_adapters_test.go` and
  `pkg/github/bridge/webhook_handler_phase109_test.go`.
- **Manual:** orphan a test alias (terminate its instance, leave `status=stopped`),
  @-mention on a PR → the bridge logs `orphaned stopped row … cold-creating`, a new instance
  is created, and no message strands in the old FIFO.

### Out of scope

The orphaned per-sandbox FIFO queue and lingering management Lambdas (`token-refresher`,
`budget-enforcer`) are **not** GC'd here — only the DDB row is deleted. They become harmless
garbage flagged by `km doctor`'s stale-resource check; full cleanup is `km destroy <id>`.
The H1 bridge (`pkg/h1/bridge`) carries the **identical fix** (ported in lockstep — see
`docs/h1-bridge.md` § Phase 109).

---

## Phase 115 — Generic event→prompt router

> **Phase 115 (2026-06-16) — Generic GitHub webhook event→prompt router (complete):**
> Adds a second ingress class to `km-github-bridge`. The existing `issue_comment`
> path is **byte-identical** after this phase. New: autonomous webhook events
> (`repository`, `push`, `release`, …) map to a prompt run in a sandbox via
> first-match rules in `github.events:`. Dormant by default — absent `github.events:`
> in `km-config.yaml` → `KM_GITHUB_EVENTS` unset → byte-identical to Phase 114.

### What it does

Phase 115 adds an autonomous, event-driven ingress path alongside the human-gated
`issue_comment` path. Where Phase 97 requires a human to `@km-bot` in a PR comment,
Phase 115 lets you configure rules that fire automatically when GitHub delivers any
supported webhook event to the bridge. The first use case is new-repository
onboarding: when a new repo is created in your org, the bridge cold-creates a sandbox
and runs a prompt to open a bootstrap PR.

**Key design constraints:**
- No actor allowlist for autonomous events (the event itself is the trigger).
- `exclude:` glob is the primary opt-out (works on brand-new empty repos, before any
  code exists).
- First-match rule selection (mirrors `Resolve()` in `github.repos:`).
- Opt-in per-(event, repo, action) cooldown, default off (`cooldownSeconds: 0`).
- No 👀 reaction for autonomous events — no originating comment to react to.
- Delivery-GUID dedup runs BEFORE rule matching (prevents duplicate cold-creates on
  GitHub retries with fresh GUIDs).

### Config shape — `github.events:`

```yaml
github:
  events:
    - on: repository            # GitHub event type (required)
      actions: [created]        # Action filter — empty means any action
      match: "your-org/*"       # Org+repo glob, exact-before-glob first-match
      exclude:                  # Opt-out globs (applied after match)
        - "your-org/km-e2e-skip-*"
        - "your-org/terraform-*"
      profile: profiles/github-review.yaml   # SandboxProfile to cold-create
      alias: gh-onboard         # Optional: target a long-lived alias sandbox
      agent: claude             # Optional: agent override (claude | codex)
      cooldownSeconds: 3600     # Optional: per-(event,repo,action) dedup window (0=off)
      prompt: |
        A new repo {{repo}} was created by {{sender}} (default branch
        {{default_branch}}). Clone it and open a PR adding a minimal CI
        workflow. URL: {{html_url}}
```

**Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `on` | string | yes | GitHub event type (`repository`, `push`, `release`, …) |
| `actions` | []string | no | Action filter; empty = any action |
| `match` | string | yes | `owner/repo` glob (exact-before-glob, first-match) |
| `exclude` | []string | no | Opt-out globs applied after `match` passes |
| `profile` | string | no | SandboxProfile path for cold-create |
| `alias` | string | no | Long-lived alias (warm path when running, cold-create when absent) |
| `agent` | string | no | Agent override (`claude` \| `codex`); falls back to profile default |
| `cooldownSeconds` | int | no | Cooldown window in seconds (0 = off); uses nonces table |
| `prompt` | string | yes | Prompt template (supports six template vars, see below) |

**Template vars (available in `prompt:`):**

| Var | Value |
|-----|-------|
| `{{repo}}` | `owner/repo` full name |
| `{{event}}` | GitHub event type (same as `on:`) |
| `{{action}}` | GitHub event action (e.g. `created`) |
| `{{sender}}` | Login of the user/app that triggered the event |
| `{{default_branch}}` | Repository default branch (e.g. `main`) |
| `{{html_url}}` | Repository or entity HTML URL |

### Gating model

The bridge applies rules in order (first-match wins):

1. `on:` must match the `X-GitHub-Event` header.
2. `actions:` must match the payload `action` field (if non-empty; empty = any).
3. `match:` glob must match `owner/repo` (exact-before-glob, mirrors `Resolve()`).
4. `exclude:` globs are checked last — any match suppresses the rule.
5. No-match → 200 drop (logged but no sandbox dispatch).

**Org-glob example:** `match: "your-org/*"` fires for any repo in your org.
**Opt-out example:** `exclude: ["your-org/km-e2e-skip-*"]` prevents that prefix from
triggering; on a brand-new empty repo the exclude fires before any code exists.

### Cooldown

When `cooldownSeconds > 0`, the bridge writes a per-(event, repo, action) key to
the nonces DynamoDB table using a conditional PutItem. The key format is
`gh-event-cooldown:{event}:{repo}:{action}`. A second delivery of the same
(event, repo, action) within the window is silently dropped (200 OK, logged).

Delivery-GUID dedup runs **before** cooldown: a GitHub retry with a new GUID still
hits the cooldown gate.

The nonces table is the same table used for `issue_comment` delivery dedup and the
Phase 101 orphan-reply cooldown — no new infrastructure.

### Dormant-by-default invariant

Absent `github.events:` in `km-config.yaml` → `KM_GITHUB_EVENTS` is not set →
`webhookHandler.EventRules` is nil → non-`issue_comment` events are silently dropped
with a 200 → byte-identical to Phase 114. The bridge log reads:
`github-bridge: ignoring non-issue_comment event event=repository`.

When `github.events:` is configured → Lambda cold-start log reads:
`km-github-bridge: loaded event routing config rule_count=N`.

### Sandbox-side poller (GH-EVENT-POLLER)

The `km-github-inbound-poller` bash in `pkg/compiler/userdata.go` (rendered into
every sandbox with `notification.github.inbound.enabled: true`) was extended in
Phase 115 to tolerate event-rule envelopes (`Number=0`, `Kind` != `issue_comment`):

- **Validation:** only `REPO` is required; `NUMBER=0` is valid (bash `"0"` is
  non-empty, so the pre-Phase-115 `[ -z "$NUMBER" ]` guard was already lenient, but
  the intent was wrong — the check is now `[ -z "$REPO" ]` only).
- **Session-continuity lookup:** skipped when `NUMBER=0`. Each event-rule dispatch
  is a fresh session; keying on `(repo, 0)` would incorrectly merge unrelated events.
- **Preamble:** branched on `KIND`. `issue_comment` (or empty KIND for back-compat)
  produces the existing `[GitHub Comment Trigger]` preamble with worktree-per-PR
  guidance and `git fetch origin pull/${NUMBER}/head`. All other Kinds produce an
  `[GitHub Event Trigger]` preamble with repo + event type + action + sender + URL
  + the expanded prompt — **no `pull/0/head`** fetch.
- **Session writeback:** also skipped when `NUMBER=0` (no PR thread to persist).

**Known limitation:** event-rule dispatches have no cross-event session continuity.
Each event fires a fresh agent session. Sandboxes with `alias:` can still be
long-lived (alias: warm path reuses the running sandbox), but consecutive events on
the same repo/alias do NOT resume the previous session.

### km doctor

`km doctor` adds a `GitHub Events Config` check in the GitHub group:

- **SKIP** when `github.events` is absent or empty.
- **WARN** on malformed `match:` glob, missing `on:` field, reserved event name
  collision with `github.commands`, or unreachable `@file` prompt.

### Deploy sequence (Phase 115)

```bash
# 1. Update km-config.yaml with github.events: rules.
# 2. Rebuild the bridge Lambda binary:
make build-lambdas

# 3. Update the Lambda env block (NOT --sidecars — env block requires full apply):
km init --github
# OR: km init --dry-run=false (full apply)

# 4. Subscribe the App to new event types:
km github manifest       # prints manifest JSON (default_events = union of issue_comment + every on:)
# Paste into GitHub App → App Manifest → Save → Re-install
# Required when adding a new 'on:' event type that the App was not subscribed to.
# 'repository' event requires metadata:read permission (included by default).

# 4a. INSTALL SCOPE (critical for repository/created):
# A 'repository'/'created' webhook is delivered ONLY to an App installed on the
# organization with "All repositories" access. A brand-new repo cannot be in a
# "selected repositories" set at creation time, so a selected-repos install
# receives NOTHING for repo-create events.
#   GitHub → Org → Settings → GitHub Apps → <App> → Configure →
#   Repository access → "All repositories" → Save.
# NOTE: this is the OPPOSITE of the comment-trigger guidance elsewhere in this doc,
# which recommends "Only select repositories" for least privilege. If you use the
# event router for repo-create, you must install org-wide.

# 5. Verify the env reached the Lambda:
# Inspect bridge Lambda logs for: "loaded event routing config rule_count=N"

km doctor
```

**NOT required:**
- `km init --sidecars` — `--sidecars` rebuilds binary zips but does NOT update the
  Lambda env block; `KM_GITHUB_EVENTS` stays stale.
- `km init --slack` — only updates the Slack bridge.

**Cold-created sandboxes** get the new poller free (userdata.go change is included in
the create-handler zip). **Long-lived alias sandboxes** need `km destroy && km create`
to pick up the Phase 115 poller (`KIND`-branched preamble).

### Verification

- Bridge logs `"loaded event routing config rule_count=N"` on cold-start when
  `KM_GITHUB_EVENTS` is set and valid.
- Positive: create a throwaway repo matching a configured rule → `km list` shows a
  new cold-created sandbox → `km shell <id>` and inspect the agent preamble:
  it must read `[GitHub Event Trigger]` with no `PR: #0` and no
  `git fetch origin pull/0/head`.
- Negative (exclude): create a repo matching an `exclude:` glob → bridge logs
  `no matching event rule` → no sandbox cold-creates.
- Dedup: redeliver the same webhook (GitHub App → Advanced → Redeliver) → bridge
  logs `event cooldown suppressed` (if cooldown configured) or `duplicate delivery
  suppressed` (GUID dedup) → no second sandbox.

See `.planning/phases/115-generic-github-webhook-event-prompt-router/115-UAT.md` for
the full live E2E runbook.
