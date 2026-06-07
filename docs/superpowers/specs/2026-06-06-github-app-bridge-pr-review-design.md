# GitHub App bridge — comment-triggered sandbox dispatch (`km github`)

**Date:** 2026-06-06
**Status:** Design — pending phase planning
**Branch:** `phase-97-github-app-bridge`
**Author input:** brainstorm (this session) + `klanker-maker-github-app-pr-review-spec.md` (operator-provided)

## Goal

Let an operator invoke the existing `klanker-maker` GitHub App by **@-mentioning the
bot in a GitHub comment** on a PR (or issue), and have the platform dispatch the
free-form request to an **aliased sandbox** — creating that sandbox first if it does
not exist — where Claude/Codex runs the request and posts back to GitHub (review
comments, pushed commits, opened PRs, check runs) via a new sandbox-side `km-github`
helper.

This is the GitHub-shaped twin of the Slack inbound path (Phase 67/91/95/96) and a
sibling of the Phase 96 "agentic self-serve create" north star, but reached through
GitHub webhooks instead of Slack events.

### Why @-mention (not "assign the bot")

There is **no permission** that lets a GitHub App's bot user (`klanker-maker[bot]`)
appear in the **Reviewers** or **Assignees** dropdown, or be passed to
`POST /repos/{owner}/{repo}/pulls/{n}/requested_reviewers` — that endpoint accepts
only human logins and team slugs. (GitHub Copilot-as-reviewer is a first-party special
case unavailable to custom Apps.) A **machine user** (regular account + PAT) *could* be
assigned, but it burns a seat and uses a long-lived PAT instead of short-lived
installation tokens. **Rejected.** The design substitutes "@-mention in a comment" for
"assign the bot."

## What already exists (reuse map)

This feature is mostly *assembly* of existing building blocks. Confirmed in-tree:

| Building block | Location | Reuse |
|---|---|---|
| GitHub App config (app-client-id, private-key, installation-id in SSM `/km/config/github/*`) | `internal/app/cmd/configure_github.go` | Extend: add `webhook-secret`, `bot-login`, `bridge-url` keys |
| App JWT → installation token mint | `pkg/github/token.go` (`GenerateGitHubAppJWT`) | Reuse verbatim for bridge ACK + per-sandbox token |
| Per-sandbox installation token (scoped to `allowedRepos`, SSM `{prefix}sandbox/{id}/github-token`, KMS-encrypted) | `internal/app/cmd/create.go` (`generateAndStoreGitHubToken`, step 13a) | Reuse; request the added write permissions |
| Git credential helper (`km-git-askpass`, `km-git-credential-helper` read the per-sandbox token) | `pkg/compiler/userdata.go:514-571` | Reuse; `git push` already works |
| `sourceAccess.github.allowedRepos` schema | `pkg/profile/types.go:403-410` | Reuse for token scoping |
| Per-sandbox SQS FIFO inbound queue (provision, DDB attr, SSM publish, rollback) | `internal/app/cmd/create_slack_inbound.go`, `destroy_slack_inbound.go` | Clone to `create_github_inbound.go` |
| Sandbox-side inbound poller (generated in userdata) | `pkg/compiler/userdata.go` (slack inbound block) | Extend to be source-aware / add a github queue drain |
| Webhook bridge Lambda (HMAC verify, nonce dedup, channel→sandbox lookup, SQS dispatch, 👀 ack, EventBridge relay) | `cmd/km-slack-bridge/main.go`, `pkg/slack/bridge/` | Template for `cmd/km-github-bridge/main.go`, `pkg/github/bridge/` |
| Nonces table (replay/cooldown via TTL keys) | `km-slack-bridge-nonces` | Reuse for `github-delivery:{guid}` dedup |
| Remote create via EventBridge → create-handler Lambda (carries prompt-queue) | `cmd/create-handler/main.go`, `internal/app/cmd/create.go` (`runCreateRemote`) | Reuse for the cold-create path |
| `alias-index` GSI on `km-sandboxes` | `infra/modules/dynamodb-sandboxes/` | Reuse to resolve repo→alias→sandbox_id |
| Slack helper precedent (`km-slack`) | sandbox-side CLI | Template for `km-github` |

**Net new:** `km-github-bridge` Lambda, `pkg/github/bridge`, per-sandbox `github-inbound`
queue + poller extension, `km-github` sandbox helper, `github.repos:` config block + env
plumbing, App scope/webhook additions, `km github` operator subcommands, doctor checks.

## Decisions (from brainstorm)

1. **Sandbox key:** default **per-repo, long-lived** (alias `gh-{owner}-{repo}`); operator
   may point **several repos at one shared alias**. Resolution is config-driven, not purely
   auto-derived.
2. **Config home:** operator-controlled `github.repos:` block in `km-config.yaml`
   (deny-by-default, commenters cannot change routing). Reaches the Lambda env via
   `km init --dry-run=false` — same path as the Slack env block.
3. **Command grammar:** **free-form prompt** — everything after the @-mention is passed
   straight to claude/codex, mirroring Slack inbound. Profile/alias come from config, not
   inline.
4. **Bot actions (App scopes):** post comments/reviews, **push commits to PR branch**,
   **open PRs / push branches**, **set commit status / checks**.
5. **Authorization:** **explicit GitHub-login allowlist** per repo. Deny-by-default; a
   comment from a non-allowlisted `sender.login` is silently ignored.
6. **Response model:** the **sandbox posts back via a `km-github` helper** using the
   per-sandbox installation token; the bridge injects repo/PR/comment context into the
   prompt. Bridge adds a fast 👀 reaction as ACK.
7. **Dispatch mechanism:** **full Slack twin** — per-sandbox `github-inbound` FIFO queue +
   source-aware sandbox poller; durable retries; cold-create carries the pending prompt.

## Event model (from operator-provided spec)

- **Primary trigger:** `issue_comment` with `action == "created"`. PR conversation
  comments fire `issue_comment` (PRs are issues for comment purposes). Distinguish a PR
  comment from a plain-issue comment by the presence of `payload.issue.pull_request`.
  - PR/issue number = `payload.issue.number`
  - comment text to scan = `payload.comment.body`
  - installation id = `payload.installation.id`
  - repo = `payload.repository.full_name`; sender = `payload.comment.user.login`
- **Out of scope for MVP (deferred):** `pull_request_review_comment` (inline diff-line
  comments), bare `pull_request`/`issues` (open/label) events, `app_mention` analog.
  Member-channel analog: the App receives events only for repos where it is installed.

## Architecture

```
GitHub comment "@klanker-maker review this PR"
        │  issue_comment webhook (X-Hub-Signature-256)
        ▼
┌──────────────────────────────────────────────────────────────┐
│ km-github-bridge Lambda (Function URL)                         │
│  1. verify HMAC-SHA256 over RAW body (constant-time)           │  ← security boundary
│  2. action==created? comment.user.type!="Bot"? else drop       │  ← loop prevention
│  3. dedupe on X-GitHub-Delivery GUID (nonces table TTL)        │  ← idempotency
│  4. mention of bot-login in comment.body? else drop            │
│  5. authorize sender.login ∈ repo allowlist? else silent drop  │  ← deny-by-default
│  6. resolve repo → {alias, profile} from github.repos config   │
│  7. lookup sandbox by alias (alias-index GSI)                  │
│       warm → enqueue to github-inbound FIFO                    │
│       cold → publish SandboxCreate{prompt-queue: pending msg}  │
│  8. mint installation token; POST 👀 reaction; return 200      │  ← ack < 10s
└──────────────────────────────────────────────────────────────┘
        │ SQS FIFO (per-sandbox github-inbound)        │ EventBridge (cold)
        ▼                                              ▼
┌─────────────────────────┐                  ┌───────────────────────┐
│ sandbox-side poller     │◄─────────────────│ create-handler Lambda │
│ (source-aware drain)    │  enqueue pending │ km create → provisions│
│  build prompt preamble  │  after provision │ queue + poller + token│
│  dispatch claude/codex  │                  └───────────────────────┘
│  (tmux agent run)       │
└─────────────────────────┘
        │ agent calls km-github (per-sandbox installation token)
        ▼
   GitHub: review comment / push / open PR / check run
```

### Bridge authorization & resolution (step 5–7 detail)

- **Allowlist** is per repo entry in `github.repos:`; an empty/missing entry for a repo ⇒
  no dispatch (deny-by-default). Silent ignore (no comment, no reaction) so the bot is
  invisible to unauthorized users.
- **Resolution** maps `owner/repo` (exact first, then glob `owner/*`) → `{alias, profile,
  allow[]}`. Default `alias = gh-{owner}-{repo}` if the matched entry omits `alias`;
  default `profile = github.default_profile`.
- **Cold create** publishes the existing `SandboxCreate` EventBridge detail with the
  pending prompt carried in the prompt-queue field (Phase 86 `km-create-prompt-queue`
  mechanism). create-handler provisions the sandbox (which creates the `github-inbound`
  queue + poller + per-sandbox token), then enqueues the carried prompt so the poller
  drains it on first boot. No bridge-side blocking on the minutes-long create.

### Per-sandbox inbound (mirrors Slack inbound)

- New profile field **`spec.notification.github.inbound.enabled`** (`*bool`, default
  false). When true, `km create`:
  - creates SQS FIFO `{prefix}-github-inbound-{id}.fifo`
  - writes `km-sandboxes.github_inbound_queue_url`
  - publishes SSM `{prefix}sandbox/{id}/github-inbound-queue-url`
  - injects `KM_GITHUB_INBOUND_QUEUE_URL` into the sandbox env file
- This is a **schema change** ⇒ deploy needs `km init --sidecars` so the create-handler
  picks up the field (per the project's "schema change → km init --sidecars" rule).
- **Envelope** enqueued by the bridge:
  ```json
  {"source":"github","repo":"owner/repo","number":42,"kind":"pr",
   "comment_id":123,"html_url":"https://github.com/...#issuecomment-123",
   "branch":"feat-x","head_sha":"abc…","sender":"whereiskurt","body":"<free-form>"}
  ```

### Sandbox poller + prompt preamble

The existing inbound poller is made **source-aware** (drains the github queue too). For a
github envelope it builds a preamble, e.g.:

> You are responding to a GitHub @-mention on **PR #42** of `owner/repo` (branch
> `feat-x`, head `abc1234`). Use the `km-github` CLI to reply — `km-github review`,
> `km-github comment`, `km-github check`, `km-github pr create`. To inspect the PR, fetch
> it into a **dedicated git worktree** (`git fetch origin pull/42/head` →
> `git worktree add`). The operator's request follows.

…then appends the free-form `body` and dispatches to the configured agent via the
existing tmux agent-run path. The worktree-per-PR instruction keeps concurrent PRs
isolated in a long-lived per-repo sandbox.

### `km-github` sandbox-side helper (twin of `km-slack`)

A CLI on the box using the per-sandbox installation token (already at SSM
`{prefix}sandbox/{id}/github-token`):

- `km-github comment --body … [--reply-to <comment_id>]` → issue/PR comment
- `km-github review --event APPROVE|COMMENT|REQUEST_CHANGES --body … [--comments @file]`
  → `POST /repos/{owner}/{repo}/pulls/{n}/reviews`
- `km-github check --name … --conclusion success|failure|neutral --summary …`
  → check run
- `km-github pr create --title … --base … --head … [--body …]`
- (git push already works via the credential helper)

The agent is *told* (preamble) which verbs exist and decides comment vs push vs PR vs
check from the request. The installation token must carry the new write permissions
(below), so `generateAndStoreGitHubToken` requests them.

### Thread / session continuity

A `(repo, number) → {sandbox_id, agent_session_id}` mapping so follow-up @-mentions in
the same PR/issue continue the **same agent session**, and replies in a known thread can
bypass the re-mention requirement (mirrors Phase 91.3 thread-bypass). Implementation:
generalize `km-slack-threads` to an agent-agnostic thread table or add a sibling
`km-github-threads`. (MVP may ship without continuity — see Phasing.)

## GitHub App changes

The existing `klanker-maker` App (today: `Contents: read` for git clone) gains:

| Permission | Why |
|---|---|
| **Issues: Read & write** | `issue_comment` rides on Issues; write lets the bot reply |
| **Pull requests: Read & write** | read diff, submit reviews, open PRs |
| **Contents: Read & write** | push commits / branches (was read-only) |
| **Checks: Read & write** | post check runs (optional verb) |
| (Reactions write via the above) | 👀 ack on the triggering comment |

**Webhook:** subscribe to **Issue comment**; set the **Webhook URL** to the
`km-github-bridge` Function URL and the **Webhook secret** to a generated value stored at
SSM `/km/config/github/webhook-secret`.

These are operator actions in the GitHub App settings (or via `km github manifest`). After
changing permissions the operator re-approves the installation.

## Webhook security invariants (from operator-provided spec)

1. **Verify signature first.** `X-Hub-Signature-256 = HMAC-SHA256(raw body, webhook-secret)`.
   Verify over the **exact raw bytes** before parsing; constant-time compare. (Identical
   shape to the existing Slack `X-Slack-Signature` check.)
2. **Ack fast, work async.** Return 200 within ~10s; the real work runs in the sandbox.
3. **Loop prevention.** Drop `comment.user.type == "Bot"` / the App's own sender, and only
   act on `action == "created"`. (The bot's own replies fire `issue_comment` too.)
4. **Authenticate back as the installation.** App JWT (App ID + private key) → installation
   access token (`POST /app/installations/{installation_id}/access_tokens`) → ~1h token for
   GitHub API calls. Already implemented in `pkg/github/token.go`.
5. **Idempotency.** Dedupe on `X-GitHub-Delivery` GUID (nonces table) so a redelivery does
   not launch a second run.

## Config surface (`km-config.yaml`)

```yaml
github:
  default_profile: github-review
  repos:
    - match: "whereiskurt/*"        # exact "owner/repo" or glob "owner/*"
      alias: gh-shared-review        # several repos → one shared sandbox (optional)
      profile: github-review         # optional; falls back to default_profile
      allow: [whereiskurt, teammate1]
    - match: "whereiskurt/klankrmkr"
      allow: [whereiskurt]           # alias defaults to gh-whereiskurt-klankrmkr
```

- Absent `github:` block ⇒ feature **fully dormant** (byte-identical to today; bridge not
  even deployed unless configured).
- Config load must add `github` to the v2→v merge-list (per the project's "config key
  merge-list" rule) or the block is silently ignored.
- `km init` exports the resolved config to the bridge Lambda env (env-wins drift WARN
  preserved); full apply required for env-block changes.

## A lean `github-review` profile (built-in)

```yaml
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: { name: github-review, prefix: gh }
spec:
  lifecycle:    { ttl: "2h", idleTimeout: "20m", teardownPolicy: stop }
  runtime:      { substrate: ec2, spot: true, instanceType: t3.medium, region: us-east-1 }
  execution:    { workingDir: /workspace, initCommands: [] }   # AMI-baked or minimal
  sourceAccess:
    mode: allowlist
    github: { allowedRepos: ["whereiskurt/*"], allowedRefs: ["main","develop","*"] }
  network:
    enforcement: proxy
    egress: { allowedDNSSuffixes: [".github.com",".githubusercontent.com",".amazonaws.com"] }
  iam:          { roleSessionDuration: "2h", allowedRegions: [us-east-1] }
  agent:
    default: claude
    claude:
      trustedDirectories: [/workspace]
      tools: { autoApprove: [Bash, Read, Write, Edit, Glob, Grep, WebFetch] }
  notification:
    github: { inbound: { enabled: true } }
```

`teardownPolicy: stop` keeps a long-lived per-repo box warm (fast subsequent @-mentions)
while idle-timeout hibernates it. AMI-baked variant for fastest cold spin-up.

## Operator CLI (`km github`)

Mirrors `km slack`:

- `km configure github` (exists) — store app-client-id / private-key / installation-id
- `km github init` — generate + store `webhook-secret`, cache `bot-login`, record
  `bridge-url`
- `km github manifest [--app-name]` — render an App manifest (scopes + issue_comment
  webhook + URL) to paste into GitHub "From manifest"
- `km github status` — print SSM-backed config + configured repos
- `km github test` — end-to-end smoke through the bridge
- `km github rotate-secret --webhook-secret <new>` — rotate + cold-start the bridge
- `km doctor` — add `github_app_configured`, `github_webhook_secret_present`,
  `github_bot_login_cached`, `github_bridge_url`, repo-allowlist sanity, per-repo
  alias/profile resolvability.

## Deploy & rollout

- `make build-lambdas` (clean — avoids the "init skips existing zips" trap) then
  `km init --dry-run=false` (new Lambda + EventBridge + env block require a **full apply**,
  not `--sidecars`).
- The `spec.notification.github.inbound` schema field is compiled by create-handler ⇒ also
  `km init --sidecars` to refresh it.
- Existing sandboxes need `km destroy && km create` to gain the `github-inbound` queue +
  poller + `km-github` helper + write-scoped token.
- GitHub-side: bump App permissions + add the issue_comment webhook + set URL/secret
  (`km github manifest`), then re-approve the installation.

## Phasing (proposed — for GSD roadmap)

The full vision is large; deliver in coherent slices. Proposed:

- **Phase 97 — GitHub comment-trigger MVP (PR review).** App scope/webhook + secret;
  `km-github-bridge` Lambda (verify → loop-guard → dedupe → mention → allowlist →
  resolve → enqueue/cold-create → 👀); `github.repos:` config + env plumbing;
  `spec.notification.github.inbound` + per-sandbox `github-inbound` queue/poller;
  `km-github comment`/`review`; `github-review` profile; `km github init/manifest/status`;
  doctor checks. **Success = `@klanker-maker review` on a PR ⇒ Claude reviews ⇒ review
  comment posted**, with a warm per-repo sandbox and a working cold-create path.
- **Phase 98 — richer write-backs + continuity.** `km-github check`, `km-github pr
  create`, push-commit verb hardening; thread/session continuity table; shared-alias
  across repos; thread-bypass for follow-ups. (Could split further during planning.)

Phase 97 is itself the operator-provided spec's scope (comment-triggered PR review);
Phase 98 is the brainstorm's expansion (push/PR/checks/sharing/continuity).

## Open questions / risks

- **Token permission breadth.** The per-sandbox installation token currently scopes to
  `allowedRepos` with the App's permissions. Confirm `generateAndStoreGitHubToken` can
  request `issues/pull_requests/contents/checks: write` and that the helper's API calls
  succeed; a single shared App installation token vs per-sandbox token for *commenting* is
  a sub-decision (per-sandbox preferred for least-privilege/audit).
- **Concurrent PRs in one long-lived sandbox.** Mitigated by worktree-per-PR preamble; the
  agent is responsible. Watch for cross-PR contamination in early UAT.
- **Bridge ACK reaction needs an installation token at the Lambda.** Acceptable (Lambda has
  `/km/config/github/private-key` access); alternative is to skip the reaction and post a
  "working…" comment (noisier). Keep 👀.
- **Glob ambiguity / alias collisions.** Two config entries matching one repo: resolve
  exact-before-glob, first-match-wins; `km doctor` warns on overlap. Alias uniqueness
  across repos is intentional for the shared-sandbox case.
- **Multi-install / federation.** Out of scope for v1; one front-door App per install.
  Cross-install relay (Phase 95 analog) deferred.

## Non-goals (v1)

Auto-review-every-PR; inline diff-line comment trigger (`pull_request_review_comment`);
issue→PR automation beyond `km-github pr create`; Slack↔GitHub cross-posting; machine-user
reviewer assignment; non-installed-repo events.
