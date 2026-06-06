# Phase 97: GitHub comment-trigger MVP — Context

**Gathered:** 2026-06-06
**Status:** Ready for planning
**Source:** Brainstorm (this session) + design spec `docs/superpowers/specs/2026-06-06-github-app-bridge-pr-review-design.md` + operator-provided spec `klanker-maker-github-app-pr-review-spec.md`

<domain>
## Phase Boundary

**Delivers:** An operator @-mentions the existing `klanker-maker` GitHub App in a PR/issue
**comment**; a new `km-github-bridge` Lambda authorizes and dispatches the free-form request
to an **aliased per-repo sandbox** (creating it cold if absent), where Claude reviews the PR
and posts back a review via a new sandbox-side `km-github` helper. This is the GitHub-shaped
twin of the Slack inbound path (Phase 67/91/95/96).

**In scope (Phase 97):**
- `km-github-bridge` Lambda: HMAC-SHA256 `X-Hub-Signature-256` verify (raw body, constant-time),
  loop guard (`action==created`, drop `comment.user.type==Bot`/self), `X-GitHub-Delivery` dedupe
  (nonces table), bot-login mention detection, deny-by-default per-repo login allowlist,
  repo→{alias,profile} resolution, `alias-index` lookup, warm-enqueue / cold-create, 👀 ack, 200.
- `github.repos:` config block in `km-config.yaml` + env plumbing to the bridge Lambda.
- `spec.notification.github.inbound.enabled` profile field + per-sandbox `github-inbound` FIFO
  queue + source-aware poller.
- `km-github` sandbox helper: `comment` + `review` verbs (installation token).
- Lean built-in `github-review` profile.
- `km github init/manifest/status` operator commands + `km doctor` checks.
- Extend the existing GitHub App: add `issues`/`pull_requests`/`contents`/`checks` write scopes
  + `issue_comment` webhook subscription (one reconfigure).

**Out of scope (Phase 98 / deferred):** `km-github check`/`pr create` verbs, thread/session
continuity, shared-alias across repos, `pull_request_review_comment` (inline diff) trigger,
auto-review-every-PR, machine-user reviewer assignment, multi-install/federated GitHub relay.

</domain>

<decisions>
## Implementation Decisions (locked)

### Trigger & event model
- Trigger = **`issue_comment` with `action == "created"`** (PRs are issues for comments).
  Distinguish a PR comment via presence of `payload.issue.pull_request`. NOT `pull_request`,
  NOT `pull_request_review_comment` (deferred).
- Field mapping: number = `payload.issue.number`; comment text = `payload.comment.body`;
  installation id = `payload.installation.id`; repo = `payload.repository.full_name`;
  sender = `payload.comment.user.login`.
- Command grammar = **free-form prompt**: everything after the @-mention is passed straight to
  claude/codex (mirrors Slack inbound). No inline directives; profile/alias come from config.
- Rationale for @-mention (not "assign the bot"): GitHub has no permission to put an App bot in
  the Reviewers/Assignees dropdown or the request-reviewers API. Machine-user PAT rejected
  (burns a seat, long-lived creds).

### Webhook security invariants (non-negotiable, from operator spec)
1. Verify `X-Hub-Signature-256` = HMAC-SHA256 of the **raw request body** under the webhook
   secret BEFORE parsing; constant-time compare. (Same shape as the Slack `X-Slack-Signature`
   check in `pkg/slack/bridge`.)
2. Ack fast (<~10s), work async — return 200, real work runs in the sandbox.
3. Loop prevention — drop `comment.user.type == "Bot"` / the App's own sender; only
   `action == "created"`.
4. Authenticate back as the installation — App JWT → installation access token (already in
   `pkg/github/token.go`).
5. Idempotency — dedupe on `X-GitHub-Delivery` GUID via the nonces table.

### Sandbox keying & routing
- Default **per-repo, long-lived** sandbox: alias = `gh-{owner}-{repo}` when the matched config
  entry omits `alias`. `teardownPolicy: stop` keeps it warm; idle-timeout hibernates.
- Config-driven: `github.repos:` in `km-config.yaml` maps `match` (exact `owner/repo` or glob
  `owner/*`) → `{alias, profile, allow[]}`. Resolution: **exact before glob, first-match-wins**.
- Profile falls back to `github.default_profile` when an entry omits `profile`.
- Shared-alias across repos (several entries → one box) is supported by the config shape but the
  hardening + worktree isolation is Phase 98; Phase 97 just must not preclude it.

### Authorization
- **Explicit GitHub-login allowlist** per repo (`allow: [...]`). Deny-by-default. A comment from a
  non-allowlisted `sender.login` is **silently ignored** (no reaction, no comment, no dispatch) —
  the bot is invisible to unauthorized users. Authorization is a pure config check (no GitHub API
  call needed).

### Dispatch mechanism (full Slack twin)
- Per-sandbox `github-inbound` FIFO queue, provisioned at `km create` when
  `spec.notification.github.inbound.enabled: true`. Clone of `create_slack_inbound.go`
  (queue + `km-sandboxes.github_inbound_queue_url` DDB attr + SSM
  `{prefix}sandbox/{id}/github-inbound-queue-url` + `KM_GITHUB_INBOUND_QUEUE_URL` env; rollback on
  failure; destroy cleanup).
- Warm: bridge enqueues `{source:github, repo, number, kind, comment_id, html_url, branch,
  head_sha, sender, body}` to the queue; the source-aware sandbox poller drains + dispatches.
- Cold (no sandbox for alias): bridge publishes a `SandboxCreate` EventBridge event carrying the
  pending prompt via the Phase 86 prompt-queue; create-handler provisions (queue + poller + token)
  and the carried prompt is drained on first boot. Bridge never blocks on the minutes-long create.

### Response model
- The **sandbox posts back** via the `km-github` helper using the **per-sandbox installation
  token** (SSM `{prefix}sandbox/{id}/github-token`, minted by `generateAndStoreGitHubToken` scoped
  via `sourceAccess.github.allowedRepos`, now requesting the added write permissions).
- Bridge posts a fast 👀 reaction as ACK (mints an installation token at the Lambda via
  `pkg/github/token.go`).
- Poller builds a GitHub context preamble (repo/PR/branch/head + **worktree-per-PR** guidance for
  concurrent PRs in a long-lived box), then appends the free-form body and dispatches to the agent
  via the existing tmux agent-run path.

### Reuse (do NOT rebuild)
- GitHub App auth already exists: `km configure github` → `/km/config/github/{app-client-id,
  private-key,installation-id}`; `pkg/github/token.go` mints App JWT → installation token;
  `generateAndStoreGitHubToken` (create.go step 13a) writes the per-sandbox token; git credential
  helper (`pkg/compiler/userdata.go:514-571`) already does `git push`.
- Slack bridge is the structural template: `cmd/km-slack-bridge/main.go`, `pkg/slack/bridge/`
  (HMAC verify, nonce dedup, channel→sandbox lookup, SQS dispatch, 👀 ack, EventBridge relay).
- Per-sandbox SQS provisioning: `internal/app/cmd/create_slack_inbound.go`,
  `destroy_slack_inbound.go`. Nonces table for dedup/cooldown. `alias-index` GSI on `km-sandboxes`.
  Remote create via `cmd/create-handler/main.go` + `runCreateRemote` (carries prompt-queue).

### Deploy / rollout
- `make build-lambdas` (clean — avoid the "init skips existing zips" trap) →
  `km init --dry-run=false` (new Lambda + EventBridge + env block ⇒ full apply, NOT `--sidecars`).
- `spec.notification.github.inbound` is a schema change ⇒ also `km init --sidecars` so
  create-handler picks it up.
- Existing sandboxes need `km destroy && km create` to gain the queue + poller + helper +
  write-scoped token.
- GitHub side: bump App permissions + add `issue_comment` webhook + set URL/secret
  (`km github manifest`), then re-approve the installation.

### Project conventions to honor (from CLAUDE.md + memory)
- New `km-config.yaml` keys must be added to the v2→v merge-list in `config.Load()`
  (`project_config_key_merge_list`), not just struct+getter.
- Any km command running terragrunt must call `ExportConfigEnvVars(cfg)` first
  (`project_terragrunt_env_export`).
- New per-sandbox DDB attrs must round-trip through struct+marshal+unmarshal or
  resume/extend/ttl-handler strip them (`project_sandboxmetadata_lossy_roundtrip`).
- `apiVersion: klankermaker.ai/v1alpha2`; `spec.notification:` is the typed block (Phase 92).
- Rebuild with `make build` (ldflags), not bare `go build`.
- New TF modules must NOT declare `required_providers` (root.hcl owns providers).

### Claude's Discretion
- Exact Go package layout for the bridge (`cmd/km-github-bridge/`, `pkg/github/bridge/`).
- Terraform module structure for the new Lambda + Function URL + EventBridge + IAM (mirror
  `lambda-slack-bridge`).
- Wave/plan decomposition and test strategy (table-driven unit tests like the Slack bridge;
  mocked AWS interfaces).
- Whether `km-github` is a separate binary or a subcommand surface; envelope JSON exact schema.

</decisions>

<specifics>
## Specific Ideas

- Design spec: `docs/superpowers/specs/2026-06-06-github-app-bridge-pr-review-design.md` (full
  architecture, reuse map with file:line, App scope table, config surface, lean profile,
  CLI surface, deploy sequence, open questions).
- Operator-provided spec: `klanker-maker-github-app-pr-review-spec.md` (event model, webhook
  invariants, posting via `POST /repos/{owner}/{repo}/pulls/{n}/reviews`).
- Lean `github-review` profile sketch and `github.repos:` YAML example are in the design spec.

</specifics>

<deferred>
## Deferred Ideas (Phase 98+)

- `km-github check` (check runs) + `km-github pr create` (open PR / push branch) verbs.
- Thread/session continuity table `(repo, number) → {sandbox_id, agent_session_id}` + thread-bypass.
- Shared-alias hardening (worktree-per-PR isolation, doctor overlap/collision warnings).
- `pull_request_review_comment` inline-diff trigger.
- Multi-install / federated GitHub relay (Phase 95 analog).

</deferred>

---

*Phase: 97-github-comment-trigger-mvp*
*Context gathered: 2026-06-06 via brainstorm + design spec*
