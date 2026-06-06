# Phase 97: GitHub comment-trigger MVP — Research

**Researched:** 2026-06-06
**Domain:** GitHub App webhook bridge (Lambda) → per-repo aliased sandbox dispatch; GitHub-shaped twin of the Slack inbound path
**Confidence:** HIGH (codebase reuse map is exact, file:line verified; external GitHub API facts cross-checked against GitHub docs)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Trigger & event model**
- Trigger = `issue_comment` with `action == "created"` (PRs are issues for comments). Distinguish a PR comment via presence of `payload.issue.pull_request`. NOT `pull_request`, NOT `pull_request_review_comment` (deferred).
- Field mapping: number = `payload.issue.number`; comment text = `payload.comment.body`; installation id = `payload.installation.id`; repo = `payload.repository.full_name`; sender = `payload.comment.user.login`.
- Command grammar = free-form prompt: everything after the @-mention is passed straight to claude/codex (mirrors Slack inbound). No inline directives; profile/alias come from config.
- @-mention (not "assign the bot") — GitHub has no API to put an App bot in Reviewers/Assignees. Machine-user PAT rejected.

**Webhook security invariants (non-negotiable)**
1. Verify `X-Hub-Signature-256` = HMAC-SHA256 of the raw request body under the webhook secret BEFORE parsing; constant-time compare.
2. Ack fast (<~10s), work async — return 200, real work runs in the sandbox.
3. Loop prevention — drop `comment.user.type == "Bot"` / the App's own sender; only `action == "created"`.
4. Authenticate back as the installation — App JWT → installation access token (already in `pkg/github/token.go`).
5. Idempotency — dedupe on `X-GitHub-Delivery` GUID via the nonces table.

**Sandbox keying & routing**
- Default per-repo, long-lived sandbox: alias = `gh-{owner}-{repo}` when the matched config entry omits `alias`. `teardownPolicy: stop` keeps it warm; idle-timeout hibernates.
- Config-driven: `github.repos:` in `km-config.yaml` maps `match` (exact `owner/repo` or glob `owner/*`) → `{alias, profile, allow[]}`. Resolution: exact before glob, first-match-wins.
- Profile falls back to `github.default_profile` when an entry omits `profile`.
- Shared-alias across repos supported by config shape but hardening + worktree isolation is Phase 98; Phase 97 just must not preclude it.

**Authorization**
- Explicit GitHub-login allowlist per repo (`allow: [...]`). Deny-by-default. Non-allowlisted `sender.login` is silently ignored (no reaction, no comment, no dispatch). Pure config check (no GitHub API call needed).

**Dispatch mechanism (full Slack twin)**
- Per-sandbox `github-inbound` FIFO queue, provisioned at `km create` when `spec.notification.github.inbound.enabled: true`. Clone of `create_slack_inbound.go` (queue + `km-sandboxes.github_inbound_queue_url` DDB attr + SSM `{prefix}sandbox/{id}/github-inbound-queue-url` + `KM_GITHUB_INBOUND_QUEUE_URL` env; rollback on failure; destroy cleanup).
- Warm: bridge enqueues `{source:github, repo, number, kind, comment_id, html_url, branch, head_sha, sender, body}`; source-aware sandbox poller drains + dispatches.
- Cold: bridge publishes a `SandboxCreate` EventBridge event carrying the pending prompt via the Phase 86 prompt-queue; create-handler provisions and the carried prompt is drained on first boot. Bridge never blocks on create.

**Response model**
- Sandbox posts back via `km-github` helper using the per-sandbox installation token (SSM `{prefix}sandbox/{id}/github-token`, scoped via `sourceAccess.github.allowedRepos`, now requesting added write permissions).
- Bridge posts a fast 👀 reaction as ACK (mints an installation token at the Lambda via `pkg/github/token.go`).
- Poller builds a GitHub context preamble (repo/PR/branch/head + worktree-per-PR guidance), appends the free-form body, dispatches via the existing tmux agent-run path.

**Deploy / rollout**
- `make build-lambdas` (clean) → `km init --dry-run=false` (new Lambda + EventBridge + env block ⇒ full apply, NOT `--sidecars`).
- `spec.notification.github.inbound` is a schema change ⇒ also `km init --sidecars` so create-handler picks it up.
- Existing sandboxes need `km destroy && km create`.
- GitHub side: bump App permissions + add `issue_comment` webhook + set URL/secret (`km github manifest`), re-approve installation.

**Project conventions to honor**
- New `km-config.yaml` keys must be added to the v2→v merge-list in `config.Load()`.
- Any km command running terragrunt must call `ExportConfigEnvVars(cfg)` first.
- New per-sandbox DDB attrs must round-trip through struct+marshal+unmarshal.
- `apiVersion: klankermaker.ai/v1alpha2`; `spec.notification:` is the typed block.
- Rebuild with `make build` (ldflags), not bare `go build`.
- New TF modules must NOT declare `required_providers` (root.hcl owns providers).

### Claude's Discretion
- Exact Go package layout for the bridge (`cmd/km-github-bridge/`, `pkg/github/bridge/`).
- Terraform module structure for the new Lambda + Function URL + EventBridge + IAM (mirror `lambda-slack-bridge`).
- Wave/plan decomposition and test strategy (table-driven unit tests; mocked AWS interfaces).
- Whether `km-github` is a separate binary or a subcommand surface; envelope JSON exact schema.

### Deferred Ideas (OUT OF SCOPE — Phase 98+)
- `km-github check` (check runs) + `km-github pr create` verbs.
- Thread/session continuity table `(repo, number) → {sandbox_id, agent_session_id}` + thread-bypass.
- Shared-alias hardening (worktree-per-PR isolation, doctor overlap/collision warnings).
- `pull_request_review_comment` inline-diff trigger.
- Multi-install / federated GitHub relay.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| GH-APP-SCOPE | Extend `klanker-maker` App: write scopes + `issue_comment` webhook; `km github manifest`; webhook-secret/bot-login/bridge-url at `/km/config/github/*` | App scope table (§ GitHub App Changes); `configure_github.go` SSM-key pattern (§ Standard Stack); manifest format (§ Code Examples) |
| GH-BRIDGE-VERIFY | Lambda verifies `X-Hub-Signature-256` HMAC-SHA256 over raw body (constant-time) before parsing; reuses `pkg/github/token.go` for token mint | `verifySlackSignature` template (events_handler.go:705); `GenerateGitHubAppJWT`/`ExchangeForInstallationToken` (token.go:54,119) |
| GH-BRIDGE-AUTH | Loop guard, `X-GitHub-Delivery` dedupe via nonces table, mention detection, deny-by-default allowlist (silent ignore) | `isBotLoop` (events_handler.go:552); `nonceStoreAdapter`/`DynamoNonceStore.Reserve` (main.go:591); mention scan pattern (events_handler.go:337) |
| GH-BRIDGE-ROUTE | Resolve `owner/repo → {alias,profile}`; `alias-index` GSI lookup; warm enqueue / cold `SandboxCreate`; 👀 + 200 | `ResolveSandboxAliasDynamo` (sandbox_dynamo.go:571); `PutSandboxCreateEvent` (eventbridge.go:37); reactions API (§ External Facts) |
| GH-INBOUND-Q | `spec.notification.github.inbound.enabled *bool`; `km create` provisions FIFO + DDB attr + SSM + env; absent/false ⇒ zero artifacts; destroy cleans up | Clone `create_slack_inbound.go` + `destroy_slack_inbound.go`; `sqs.go` helpers; metadata round-trip (§ Pitfalls) |
| GH-POLLER | Source-aware poller drains github queue, builds preamble, dispatches via tmux agent-run | userdata.go:1517-2063 slack poller block; dispatch fork (userdata.go:1889) |
| GH-HELPER | `km-github` helper (per-sandbox token): `comment`, `review` verbs | `cmd/km-slack/main.go` dispatch table; git-askpass token read (userdata.go:528); reviews API (§ External Facts) |
| GH-PROFILE | Lean built-in `github-review` profile; validates via `km validate` | Profile sketch (§ Architecture); `profiles/` + `scripts/validate-all-profiles.sh` |
| GH-CLI | `km github init/manifest/status`; `github.repos:` round-trips config load + `km init` env export with drift WARN | merge-list (config.go:421); `ExportTerragruntEnvVars` (init.go:772) — structured value needs JSON serialization (§ Config Plumbing) |
| GH-DOCTOR | Doctor checks: App configured, webhook secret, bot-login, bridge URL, allowlist resolvability + overlap warnings | doctor.go check pattern; resolution logic (§ Architecture) |
| GH-E2E | `@klanker-maker review this PR` ⇒ 👀 ⇒ Claude runs (warm+cold) ⇒ review posted | manual real-AWS+GitHub (§ Validation Architecture) |
</phase_requirements>

## Summary

Phase 97 is **~90% assembly of existing in-tree building blocks**, not green-field engineering. The Slack inbound path (Phase 67/91/95/96) is a near-exact structural twin: a Function-URL Lambda that (1) HMAC-verifies a raw body, (2) loop-guards and dedupes via a nonces table, (3) resolves an external identifier to a sandbox via a DynamoDB GSI, (4) enqueues to a per-sandbox FIFO queue (or cold-creates), and (5) posts a fast ack reaction. Every one of those mechanisms exists and is unit-tested. The GitHub App auth half (JWT → installation token, per-sandbox SSM token, git credential helper) shipped in Phase 13 and is reusable verbatim. The dominant work is cloning these with GitHub-shaped types and wiring a new `github.repos:` config surface.

Three things genuinely differ from the Slack twin and deserve plan attention: **(a)** the `github.repos:` config value is a **list-of-objects**, not a scalar — it must be JSON-serialized into a single Lambda env var (the Slack keys are all scalars/comma-joined strings); **(b)** the **cold-create path's "carry the pending prompt"** is *not* what the Phase 86 prompt-queue actually does today — Phase 86 pushes prompts **operator-side over SSM after create**, and `SandboxCreateDetail` has **no prompt field**, so the bridge cannot ride the existing mechanism unchanged (see Pitfall 1); **(c)** routing is **config-driven repo→alias resolution at the bridge**, whereas Slack resolves channel→sandbox purely via a GSI — the bridge must own the `match`/glob/allowlist logic.

**Primary recommendation:** Clone `cmd/km-slack-bridge` → `cmd/km-github-bridge` and `pkg/slack/bridge` → `pkg/github/bridge` (handler + narrow interfaces + AWS adapters), reuse `pkg/github/token.go` verbatim for both the bridge ACK token and the per-sandbox token, clone `create_slack_inbound.go`/`destroy_slack_inbound.go` for the `github-inbound` queue, extend the userdata poller to be source-aware, and add a new `infra/modules/lambda-github-bridge` mirroring `lambda-slack-bridge` plus an `eventbridge:PutEvents` grant for cold-create. Decide the cold-create prompt-carry mechanism explicitly in planning (recommended: a `pending_prompt` field on `SandboxCreateDetail` drained on first boot via a tiny create-handler hook, OR a bridge-side enqueue to the queue right after the cold `SandboxCreate` — the queue survives because `MessageRetentionPeriod=1209600`/14d).

## Standard Stack

### Reuse Map — clone/extend these (file:line verified)

| Building block | Location (file:line) | Action |
|---|---|---|
| Bridge Lambda entrypoint (env wiring, path dispatch, base64 body, header lowercasing) | `cmd/km-slack-bridge/main.go` (whole file; `handle` at :380, `init` at :72, `nonceStoreAdapter` at :591) | Clone → `cmd/km-github-bridge/main.go` |
| Webhook handler (verify → loop-guard → dedupe → resolve → enqueue → ack) | `pkg/slack/bridge/events_handler.go` (`Handle` at :164, `isBotLoop` at :552, `verifySlackSignature` at :705) | Clone → `pkg/github/bridge/webhook_handler.go` |
| Narrow handler interfaces (SQSSender, EventNonceStore, Reactor, SandboxBy*Fetcher) | `pkg/slack/bridge/events_interfaces.go` (:71-163) | Clone → `pkg/github/bridge/interfaces.go` |
| AWS adapters (SSM secret fetcher w/ cache, DynamoNonceStore, SQSAdapter) | `pkg/slack/bridge/aws_adapters.go`; `DynamoNonceStore.Reserve` + `ErrNonceReplayed` | Reuse adapters / clone secret fetcher |
| GitHub App JWT mint | `pkg/github/token.go:54` `GenerateGitHubAppJWT(appClientID, privateKeyPEM)` | Reuse VERBATIM (bridge ACK + per-sandbox) |
| Installation token exchange | `pkg/github/token.go:119` `ExchangeForInstallationToken(ctx, jwt, installationID, repos, perms)` | Reuse VERBATIM |
| Permission compilation | `pkg/github/token.go:197` `CompilePermissions([]string) map[string]string` (clone/fetch→contents:read, push→contents:write) | EXTEND: add issues/pull_requests/checks write mappings |
| Per-sandbox token write | `pkg/github/token.go:226` `WriteTokenToSSM(...)` → `/{prefix}/sandbox/{id}/github-token` SecureString | Reuse VERBATIM |
| Per-sandbox token generate (create step 13a) | `internal/app/cmd/create.go:2391` `generateAndStoreGitHubToken(...)`; called at :1310 gated on `SourceAccess.GitHub` non-empty `AllowedRepos` | EXTEND: pass write permissions (currently passes `nil` perms at :1317) |
| Installation-ID resolution from allowedRepos | `internal/app/cmd/create.go:2312` `resolveInstallationID` | Reuse VERBATIM |
| Git credential helper (token from SSM at git time) | `pkg/compiler/userdata.go:514-571` (`km-git-askpass`, `km-git-credential-helper`) | Reuse — `git push` already works |
| Per-sandbox SQS FIFO provisioning + rollback | `internal/app/cmd/create_slack_inbound.go` (`provisionSlackInboundQueue` :92, `rollbackSlackInboundQueue` :194) | Clone → `create_github_inbound.go` |
| Per-sandbox SQS teardown | `internal/app/cmd/destroy_slack_inbound.go` (`drainSlackInbound` :82) | Clone → `destroy_github_inbound.go` |
| SQS FIFO helpers + naming | `pkg/aws/sqs.go` (`SlackInboundQueueName` :44, `CreateSlackInboundQueue` :57 — FIFO, ContentBasedDedup=false, vis=30s, retention=14d, `DeleteSlackInboundQueue` :92) | Add `GitHubInboundQueueName` (or generalize) |
| SSM param path helper | `pkg/aws/identity.go:115` `SandboxParameterPath(prefix, id, suffix)` | Reuse |
| Sandbox-side inbound poller | `pkg/compiler/userdata.go:1517-2063` (slack poller heredoc) + systemd unit :2343 | EXTEND: source-aware drain of github queue |
| Alias→sandbox_id GSI resolver | `pkg/aws/sandbox_dynamo.go:571` `ResolveSandboxAliasDynamo(...)` (queries `alias-index`, Limit=2 for dup detection) | Reuse for warm lookup |
| EventBridge SandboxCreate publish | `pkg/aws/eventbridge.go:37` `PutSandboxCreateEvent`; detail `SandboxCreateDetail` at :21 | Reuse + EXTEND detail for prompt carry (Pitfall 1) |
| Remote create handler (consumes SandboxCreate) | `cmd/create-handler/main.go:44` `CreateEvent`; builds `km create` subprocess args at :200 | EXTEND if prompt carried in detail |
| Nonces table (replay/cooldown via TTL) | `km-slack-bridge-nonces` (`DynamoNonceStore`) | Reuse for `github-delivery:{guid}` dedup |
| Sandbox helper precedent | `cmd/km-slack/main.go` (dispatch table :48-72, runPost :87) | Template → `cmd/km-github/main.go` |
| Bridge TF module | `infra/modules/lambda-slack-bridge/v1.0.0/{main,variables,outputs}.tf` | Clone → `lambda-github-bridge` + EventBridge PutEvents + alias-index Query |
| GitHub App config command + SSM keys | `internal/app/cmd/configure_github.go` (`{prefix}config/github/{app-client-id,private-key,installation-id}` at :210-243) | EXTEND: add webhook-secret, bot-login, bridge-url |
| Config plumbing (scalar example) | `internal/app/config/config.go` (`SlackConfig` :24, merge-list :421, Load :511) | Pattern; github needs list-of-objects (§ Config Plumbing) |
| Env export + drift WARN | `internal/app/cmd/init.go:772` `ExportTerragruntEnvVars` (slack keys :869-927) | Pattern; github.repos as JSON env var |
| Profile NotificationSpec | `pkg/profile/types.go:80` `NotificationSpec` (Slack at :118, Inbound `*bool` tri-state at :147) | ADD `Github *NotificationGitHubSpec` with `Inbound.Enabled *bool` |
| Profile JSON schema | `pkg/profile/schemas/sandbox_profile.schema.json:629` `notification` block | ADD `github` sibling of `slack` |
| Sandbox metadata round-trip | `pkg/aws/metadata.go:11` struct + `sandbox_dynamo.go` marshal :388 / unmarshal :257 / copy :139 | ADD `GithubInboundQueueURL` in ALL THREE spots |

### Versions / dependencies (all already in go.mod)
| Library | Purpose | Notes |
|---|---|---|
| `github.com/golang-jwt/jwt/v5` | App JWT RS256 signing | Used by token.go:22 |
| `github.com/aws/aws-lambda-go` | Lambda runtime + Function URL events | Used by km-slack-bridge |
| `github.com/aws/aws-sdk-go-v2/service/{dynamodb,sqs,ssm,eventbridge}` | AWS clients | All present |
| stdlib `crypto/hmac`, `crypto/sha256`, `encoding/hex` | `X-Hub-Signature-256` verify | Identical primitives to `verifySlackSignature` |

**No new external dependencies required.**

## Architecture Patterns

### Recommended package layout (Claude's discretion — recommended)
```
cmd/km-github-bridge/main.go      # Lambda entrypoint: env wiring, Function URL dispatch, base64/header norm
cmd/km-github/main.go             # sandbox-side helper: comment, review verbs
pkg/github/bridge/
  webhook_handler.go              # WebhookHandler.Handle (clone of EventsHandler.Handle)
  interfaces.go                   # narrow interfaces (mockable)
  aws_adapters.go                 # DDB alias resolver, SQS sender, SSM secret fetcher, reactor
  resolve.go                      # github.repos match/glob/allowlist resolution (pure, table-testable)
  payload.go                      # issue_comment payload structs
pkg/github/token.go               # EXTEND CompilePermissions only
pkg/aws/sqs.go                    # ADD GitHubInboundQueueName
internal/app/cmd/
  create_github_inbound.go        # clone of create_slack_inbound.go
  destroy_github_inbound.go       # clone of destroy_slack_inbound.go
  github.go                       # km github init/manifest/status (+ configure_github.go extend)
infra/modules/lambda-github-bridge/v1.0.0/
profiles/github-review.yaml
```

### Pattern 1: HMAC verify-before-parse, constant-time (the security boundary)
GitHub's `X-Hub-Signature-256` is `sha256=<hexdigest>` of HMAC-SHA256(raw body, webhook-secret). This is *simpler* than Slack's (no timestamp/version-string base — just HMAC the raw body). Mirror `verifySlackSignature` (events_handler.go:705) but:
- Compute over the **exact raw bytes** (Lambda Function URL base64-decode first — see main.go:382 `decodeBase64Body`).
- Use `hmac.Equal` for constant-time compare (NOT `==`).
- Expected format `sha256=` prefix; reject missing/absent.
```go
// Source: derived from pkg/slack/bridge/events_handler.go:705 + GitHub docs
func verifyGitHubSignature(secret, sigHeader string, rawBody []byte) error {
    if !strings.HasPrefix(sigHeader, "sha256=") {
        return fmt.Errorf("missing or wrong-format signature")
    }
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(rawBody)
    expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
    if !hmac.Equal([]byte(expected), []byte(sigHeader)) {
        return fmt.Errorf("signature mismatch")
    }
    return nil
}
```
**Note:** GitHub does NOT send a timestamp header, so there is no replay window check like Slack's 300s skew — replay protection comes solely from the `X-GitHub-Delivery` GUID dedup (nonces table). This is fine: GUID dedup is the canonical GitHub idempotency mechanism.

### Pattern 2: Handle() ordering (clone of events_handler.go:164, GitHub-shaped)
1. Read raw body + headers (`x-hub-signature-256`, `x-github-delivery`, `x-github-event`).
2. **Verify signature first** (over raw body). Bad/absent → 401.
3. Parse `issue_comment` payload. `action != "created"` → 200 drop.
4. **Loop guard:** `comment.user.type == "Bot"` OR `comment.user.login == bot-login` → 200 drop (clone `isBotLoop` :552; here it's a pure field check — no auth.test needed since bot-login is cached config).
5. **PR check:** `issue.pull_request` absent → 200 drop (MVP is PR-only).
6. **Mention detection:** `comment.body` contains `@{bot-login}` → else 200 drop.
7. **Authorize:** `sender.login ∈ repo allowlist` → else 200 **silent** (no reaction, no comment).
8. **Dedup:** `Reserve("github-delivery:"+guid, ttl)`; replayed → 200.
9. **Resolve** `owner/repo → {alias, profile}` (exact-before-glob, first-match-wins).
10. **Lookup** sandbox by alias via `ResolveSandboxAliasDynamo`. Found+warm → enqueue github-inbound FIFO. Not found → publish `SandboxCreate` (cold) carrying prompt.
11. **Mint installation token, POST 👀 reaction** on the comment, return 200 (within ack window).

Keep the Slack lesson: **return 200 on internal errors** (SQS/DDB failures) — 5xx makes GitHub redeliver with a NEW delivery GUID that bypasses dedup (events_handler.go:158-163 rationale applies identically; GitHub redelivers failed deliveries).

### Pattern 3: Config-driven resolution (NEW — pure function, table-testable)
```
resolve(owner/repo, []RepoEntry) -> (alias, profile, allow[], matched bool)
  1. iterate entries in declared order
  2. exact match (entry.match == "owner/repo") wins immediately
  3. else glob match (entry.match == "owner/*") — first wins
  4. alias := entry.alias ?? "gh-"+owner+"-"+repo
  5. profile := entry.profile ?? github.default_profile
```
Make this a pure function in `pkg/github/bridge/resolve.go` so it is exhaustively table-tested without AWS.

### Pattern 4: Per-sandbox FIFO provisioning (clone create_slack_inbound.go:92)
The struct-of-deps DI pattern (`slackInboundDeps`) with injected `UpdateSandboxAttr`/`PutSSMParameter` funcs is the template. The github clone:
- `inbound := notificationGitHubInbound(profile)` — gate on `Enabled == &true`; nil/false → return `("", nil)` (zero artifacts).
- `queueName := awspkg.GitHubInboundQueueName(prefix, id)` → `{prefix}-github-inbound-{id}.fifo`.
- write `github_inbound_queue_url` DDB attr; SSM `{prefix}sandbox/{id}/github-inbound-queue-url`; env `KM_GITHUB_INBOUND_QUEUE_URL`.
- explicit best-effort rollback on each post-create failure.

### Anti-Patterns to Avoid
- **Hand-rolling the App JWT / installation token exchange** — `pkg/github/token.go` is complete and tested (PKCS#1+PKCS#8, 10-min expiry, 60s drift, repo short-name stripping, wildcard handling).
- **`==` for signature compare** — must be `hmac.Equal` (timing attack).
- **5xx on transport error** — causes GitHub redelivery storms with fresh GUIDs.
- **Parsing before verifying** — verify over raw bytes first; never trust pre-parse fields.
- **Forgetting the merge-list / round-trip / providers rules** — see Pitfalls.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---|---|---|---|
| App JWT (RS256) | Custom jwt signer | `github.GenerateGitHubAppJWT` (token.go:54) | Handles PKCS#1/#8, exp/iat per GitHub docs |
| Installation token | Custom POST to access_tokens | `github.ExchangeForInstallationToken` (token.go:119) | Repo short-name stripping, wildcard, 201 handling |
| Per-sandbox token storage | Custom SSM put | `github.WriteTokenToSSM` (token.go:226) | KMS SecureString, prefix-scoped path |
| SQS FIFO queue | Raw CreateQueue | `awspkg.CreateSlackInboundQueue` shape (sqs.go:57) | FIFO+dedup attrs, idempotent QueueNameExists handling |
| Alias→sandbox | Custom scan | `ResolveSandboxAliasDynamo` (sandbox_dynamo.go:571) | GSI query + duplicate detection |
| Dedup/replay | Custom TTL store | `DynamoNonceStore.Reserve` + `nonceStoreAdapter` | Conditional-write, `ErrNonceReplayed` |
| HMAC verify scaffolding | New crypto | `verifySlackSignature` shape (events_handler.go:705) | Constant-time, format checks |
| git push auth | Custom credential | `km-git-credential-helper` (userdata.go:556) | Reads per-sandbox token at clone time |

**Key insight:** This domain's risk is not novel cryptography or AWS plumbing — it is *correctly cloning* the Slack twin's hard-won edge-case handling (200-on-error, base64 body, lowercase headers, Lambda freeze → synchronous reaction, idempotent queue creation). Copy the patterns, don't reinvent them.

## Common Pitfalls

### Pitfall 1: The Phase 86 prompt-queue is operator-side post-create — it does NOT ride EventBridge
**What goes wrong:** The design says cold-create "publishes a `SandboxCreate` EventBridge event carrying the pending prompt via the Phase 86 prompt-queue." But `SandboxCreateDetail` (eventbridge.go:21) and `CreateEvent` (create-handler/main.go:46) have **no prompt field**, and Phase 86's actual mechanism (`create_prompt.go`) pushes prompts **operator-side, over SSM `RunShellScript`, AFTER `runCreate`/`runCreateRemote` returns** (`doStep16PromptPush` :359 explicitly notes "The create-handler Lambda is UNTOUCHED"). The bridge is a Lambda with no operator shell and cannot run the Phase 86 post-create push.
**Why it happens:** The design conflates "Phase 86 prompt-queue" (the on-box `/workspace/.km-agent/queue/` drain) with "a way to carry a prompt through cold create." They are different layers.
**How to avoid — pick one in planning (recommend B):**
- **(A)** Add a `pending_prompt` (or `github_envelope`) string field to `SandboxCreateDetail`/`CreateEvent`; create-handler writes it to the on-box queue dir (or the github-inbound queue) at the end of provisioning. Requires create-handler change ⇒ `km init --sidecars`.
- **(B, recommended)** Bridge publishes the plain `SandboxCreate` (alias + profile) AND, because `create_github_inbound.go` provisions the FIFO queue with the **deterministic name** `{prefix}-github-inbound-{id}.fifo` and 14-day retention, the bridge enqueues the envelope to that queue right after the cold create. *Problem:* the bridge does not know the new sandbox_id yet (it's minted inside create-handler). So (B) needs the queue keyed by **alias** for the cold case, or create-handler to enqueue. **Cleanest:** create-handler, after it knows the sandbox_id and has provisioned the queue, drains a carried envelope from the EventBridge detail into the new queue (effectively (A) but the payload is the github envelope, not a bare prompt). The poller then drains it normally on first boot.
**Warning sign:** A plan that says "carry the prompt via Phase 86" with no `SandboxCreateDetail` field change — it will silently no-op on cold create.

### Pitfall 2: `github.repos:` is a list-of-objects — needs JSON env serialization
**What goes wrong:** Slack config keys (`mention_only`, `react_always`, `default_router`, `peer_bridges`) are scalars or a comma-joined string. `ExportTerragruntEnvVars` (init.go:869-927) emits each as a plain `os.Setenv`. The `github.repos:` block is structured (`[{match, alias, profile, allow[]}]`) and cannot be a scalar env var.
**How to avoid:** Serialize the resolved `github.repos` (plus `default_profile`) to **JSON** and export as a single env var (e.g. `KM_GITHUB_REPOS=<json>`); the bridge `json.Unmarshal`s it at cold start. The TF module passes it as one `string` variable into the Lambda `environment.variables` block (mirror `lambda-slack-bridge/main.tf:315-343`). Keep the env-wins drift-WARN pattern (compare JSON strings).
**Warning sign:** Trying to model repos as multiple numbered env vars or HCL-encoded blobs — recall token.go:259's cautionary tale where an HCL-encoded TF variable broke `json.Unmarshal` in a Lambda.

### Pitfall 3: AWS Lambda freezes on return — reaction MUST be synchronous
**What goes wrong:** Posting the 👀 reaction in a goroutine to "return 200 faster" fails: the runtime freezes when `Handle` returns, the in-flight HTTP deadline elapses during freeze, and the call times out on next thaw (Phase 75.2 UAT lesson, events_handler.go:473-484).
**How to avoid:** Mint the token and POST the reaction **synchronously** before returning 200, with a bounded ~10s context. GitHub's webhook ack window is generous (~10s), and the delivery-GUID dedup absorbs any retry. Lambda timeout: set to 60s like the Slack bridge (lambda-slack-bridge/main.tf:309).

### Pitfall 4: Config key merge-list (project rule)
**What goes wrong:** Adding `github:` to the `Config` struct + a getter is NOT enough — if `"github"` (and any nested dotted keys you read via `v2.IsSet`) is not in the v2→v merge-list (config.go:421-432), the value from `km-config.yaml` is silently dropped.
**How to avoid:** Add `"github"` to the merge-list. Since `github.repos` is a list-of-objects, prefer reading the whole `github` subtree via `mapstructure` into a struct (the Slack scalar pattern uses `v.IsSet("slack.mention_only")` per-key; a structured block reads cleaner via `v.UnmarshalKey("github", &cfg.Github)` after ensuring `"github"` is merged). Add a `config_test.go` "footgun" test mirroring `TestLoadSlackPeerBridges_Set` (config_test.go:880).

### Pitfall 5: Per-sandbox DDB attribute round-trip (project rule)
**What goes wrong:** `github_inbound_queue_url` written at create but stripped on pause/resume/extend/ttl because those do a full-row PutItem from the struct.
**How to avoid:** Add `GithubInboundQueueURL` in ALL THREE places: the struct (`metadata.go:11`, next to `SlackInboundQueueURL` :48), the marshal (`sandbox_dynamo.go:388`), the unmarshal (`sandbox_dynamo.go:257`), and the copy at :139. This is the documented `project_sandboxmetadata_lossy_roundtrip` memory.

### Pitfall 6: Write-scoped per-sandbox token
**What goes wrong:** `generateAndStoreGitHubToken` is called at create.go:1317 with `permissions=nil`, so `CompilePermissions(nil)` yields an empty map and the token defaults to the App's read scopes only — `km-github review` (needs pull_requests:write) and `comment` (needs issues:write) will 403.
**How to avoid:** EXTEND `CompilePermissions` (token.go:197) to map the new verbs (e.g. always request `issues:write`, `pull_requests:write` for github-inbound sandboxes), and pass them at the call site. The token is repo-scoped via `allowedRepos` already. Confirm during E2E that the installation actually grants these after the operator re-approves the App.

### Pitfall 7: New TF module must not declare providers (project rule)
The new `lambda-github-bridge` module must NOT include `required_providers` — `root.hcl`'s `generate "provider"` stanza is the single source (`project_terragrunt_providers_in_root`). Also include `"show"` in `mock_outputs_allowed_terraform_commands` on any terragrunt.hcl with `dependency` blocks (`project_terragrunt_show_needs_mocks`) so the destroy-class gate parses on fresh installs.

## Code Examples

### GitHub App scope additions (GH-APP-SCOPE)
| Permission | Level | Why |
|---|---|---|
| Issues | Read & write | `issue_comment` rides on Issues; write lets bot reply + react on the comment |
| Pull requests | Read & write | read diff, submit reviews via POST .../reviews |
| Contents | Read & write | push commits/branches (was read-only in Phase 13) |
| Checks | Read & write | (Phase 98 verb; declare scope now to avoid a second re-approval) |
| Webhook event | `issue_comment` | subscribe; set Webhook URL = bridge Function URL, secret = generated |

### Reactions API (👀 ACK) — external fact
```
POST /repos/{owner}/{repo}/issues/comments/{comment_id}/reactions
Headers: Authorization: Bearer <installation-token>; Accept: application/vnd.github+json; X-GitHub-Api-Version: 2022-11-28
Body: {"content":"eyes"}
Permission: issues: write   # treat already-reacted (no special error) as idempotent success
```

### Reviews API (km-github review) — external fact
```
POST /repos/{owner}/{repo}/pulls/{pull_number}/reviews
Body: {"event":"APPROVE|REQUEST_CHANGES|COMMENT", "body":"...", "commit_id":"<optional sha>",
       "comments":[{"path":"file","line":N,"body":"..."}]}   # comments optional for MVP
Permission: pull_requests: write   # body required for REQUEST_CHANGES/COMMENT; blank event => PENDING
```

### Comment API (km-github comment)
```
POST /repos/{owner}/{repo}/issues/{issue_number}/comments   Body: {"body":"..."}   Permission: issues: write
```

### App manifest (`km github manifest`) — mirror `km slack manifest`
Render a JSON manifest with the scope table above + `default_events: ["issue_comment"]` + `hook_attributes.url` = bridge Function URL. Operator pastes into GitHub "From manifest" or edits the existing App, then re-approves.

### Lean `github-review` profile (GH-PROFILE)
```yaml
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: { name: github-review, prefix: gh }
spec:
  lifecycle:  { ttl: "2h", idleTimeout: "20m", teardownPolicy: stop }
  runtime:    { substrate: ec2, spot: true, instanceType: t3.medium, region: us-east-1 }
  execution:  { workingDir: /workspace, initCommands: [] }
  sourceAccess:
    mode: allowlist
    github: { allowedRepos: ["whereiskurt/*"], allowedRefs: ["main","develop","*"] }
  network:
    enforcement: proxy
    egress: { allowedDNSSuffixes: [".github.com",".githubusercontent.com",".amazonaws.com"] }
  iam:        { roleSessionDuration: "2h", allowedRegions: [us-east-1] }
  agent:
    default: claude
    claude:
      trustedDirectories: [/workspace]
      tools: { autoApprove: [Bash, Read, Write, Edit, Glob, Grep, WebFetch] }
  notification:
    github: { inbound: { enabled: true } }
```
Validate via `scripts/validate-all-profiles.sh` (add to the 20-file inventory).

### Profile schema/type additions
- `pkg/profile/types.go`: add `Github *NotificationGitHubSpec` to `NotificationSpec` (:80); `NotificationGitHubSpec{ Inbound *NotificationGitHubInboundSpec }`; `NotificationGitHubInboundSpec{ Enabled *bool }` (mirror `NotificationSlackInboundSpec` :147, tri-state `*bool`).
- `pkg/profile/schemas/sandbox_profile.schema.json`: add `github` object under `notification` (:629), sibling of `slack` (:670), with `inbound.enabled` boolean.
- Add a `mergeNotificationGitHub` field-by-field merge (mirror `mergeNotificationSpec`).

## State of the Art

| Old Approach | Current Approach | When | Impact |
|---|---|---|---|
| `spec.cli.notify*` | `spec.notification:` typed block | Phase 92 | New field goes under `notification.github`, not `cli` |
| `spec.identity:` | `spec.iam:` | Phase 92 | `allowedSecretPaths` lives in `iam` |
| `apiVersion v1alpha1` | `v1alpha2` (strict) | Phase 92 | github-review profile must declare v1alpha2 |
| GitHub App Contents:read only (Phase 13) | + Issues/PRs/Contents/Checks write (Phase 97) | This phase | One App re-approval required |
| Slack-only inbound bridge | + GitHub bridge twin | This phase | Reuse, don't fork shared mechanisms |

**No deprecated GitHub API surfaces involved.** `X-GitHub-Api-Version: 2022-11-28` is current (token.go already sets it).

## Open Questions

1. **Cold-create prompt carry mechanism** (Pitfall 1). What we know: Phase 86 is operator-side/post-create; `SandboxCreateDetail` has no prompt field. What's unclear: whether to extend the EventBridge detail (recommended) or have the bridge enqueue post-create. Recommendation: add a carried-envelope field to `SandboxCreateDetail`/`CreateEvent` and have create-handler drain it into the new github-inbound queue after provisioning (requires `km init --sidecars`).
2. **Bridge → installation token IAM at the Lambda.** The bridge ACK reaction needs the App private key. What we know: `lambda-slack-bridge` grants `ssm:GetParameter` only on the bot-token/signing-secret paths. What's unclear: the github bridge needs `ssm:GetParameter` on `/{prefix}/config/github/{app-client-id,private-key,installation-id}` + KMS Decrypt. Recommendation: add those statements to the new module's IAM (straightforward clone of the SSM/KMS statements at main.tf:54-88).
3. **Alias-index for cold create.** What we know: warm lookup uses `alias-index`; the new IAM needs `dynamodb:Query` on `.../index/alias-index` (Slack module queries `slack_channel_id-index` at main.tf:180 — swap the index name). What's unclear: nothing blocking. Recommendation: grant Query on alias-index + the base table.
4. **`km-github` binary vs subcommand** (operator discretion). Recommendation: separate `cmd/km-github` binary symlinked into `/usr/local/bin` like km-slack (userdata.go:1113), mirroring the precedent.
5. **bot-login source.** Slack caches `bot_user_id` via auth.test. GitHub's bot login is `{app-slug}[bot]`; recommend `km github init` resolves + caches it at `{prefix}config/github/bot-login` (no per-request API call needed for the mention scan — pure string contains).

## Validation Architecture

Nyquist validation is ENABLED (`.planning/config.json` → `workflow.nyquist_validation: true`).

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing`, table-driven (project convention; see `pkg/slack/bridge/*_test.go`) |
| Config file | none (Go modules); `go.mod` present |
| Quick run command | `go test ./pkg/github/... ./cmd/km-github-bridge/... ./internal/app/cmd/... -run GitHub -count=1` |
| Full suite command | `go test ./... -count=1` then `make build` (ldflags) |
| Profile gate | `scripts/validate-all-profiles.sh` (must include github-review.yaml) |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| GH-BRIDGE-VERIFY | HMAC-SHA256 over raw body, constant-time; bad/absent sig → 401; valid → proceed | unit (table) | `go test ./pkg/github/bridge -run TestVerifyGitHubSignature -x` | ❌ Wave 0 |
| GH-BRIDGE-AUTH | loop guard (Bot/self), delivery-GUID dedup, mention detect, allowlist silent-drop | unit (table, mocked nonces) | `go test ./pkg/github/bridge -run TestHandle_Auth -x` | ❌ Wave 0 |
| GH-BRIDGE-ROUTE | exact-before-glob resolution; alias default; warm enqueue vs cold SandboxCreate; 👀 + 200 | unit (pure resolve + mocked DDB/SQS/EB/reactor) | `go test ./pkg/github/bridge -run 'TestResolve|TestHandle_Route' -x` | ❌ Wave 0 |
| GH-INBOUND-Q | enabled→FIFO+DDB attr+SSM+env provisioned; disabled→zero artifacts; rollback; destroy cleanup | unit (mocked SQS/DDB/SSM via deps struct) | `go test ./internal/app/cmd -run GitHubInbound -x` | ❌ Wave 0 |
| GH-POLLER | source-aware drain; preamble built from github envelope; dispatch invoked | unit (userdata render assertion, mirror userdata_slack_inbound_test.go) | `go test ./pkg/compiler -run GitHubInbound -x` | ❌ Wave 0 |
| GH-HELPER | comment/review build correct request to mocked GitHub (httptest via GitHubAPIBaseURL var) | unit | `go test ./cmd/km-github -run 'TestComment|TestReview' -x` | ❌ Wave 0 |
| GH-PROFILE | github-review validates | unit + script | `go test ./pkg/profile -run GitHubReview` ; `scripts/validate-all-profiles.sh` | ❌ Wave 0 |
| GH-CLI | github.repos round-trips config load (merge-list); JSON env export + drift WARN | unit (footgun test like TestLoadSlackPeerBridges_Set) | `go test ./internal/app/config -run GitHubRepos -x` | ❌ Wave 0 |
| GH-APP-SCOPE | manifest renders scopes + issue_comment; SSM keys written | unit (mocked SSM via github.MockSSMClient) | `go test ./internal/app/cmd -run 'GitHubManifest|GitHubInit' -x` | ❌ Wave 0 |
| GH-DOCTOR | checks present/absent for App config, webhook secret, bot-login, bridge URL, resolvability/overlap | unit (mocked SSM/config) | `go test ./internal/app/cmd -run GitHubDoctor -x` | ❌ Wave 0 |
| **Dormant invariant** | absent `github:` block ⇒ byte-identical (no bridge env, no profile field effect, no queue) | unit | `go test ./internal/app/config -run GitHubAbsent` + `pkg/compiler -run GitHubInboundDisabled` | ❌ Wave 0 |
| GH-E2E | `@klanker-maker review this PR` on real PR ⇒ 👀 ⇒ Claude runs (warm + cold) ⇒ review posted | manual (real AWS + GitHub) | manual UAT runbook | n/a (manual) |

### Sampling Rate
- **Per task commit:** the relevant package `go test ./<pkg> -run <Req> -count=1`.
- **Per wave merge:** `go test ./... -count=1` + `make build` + `scripts/validate-all-profiles.sh`.
- **Phase gate:** full suite green + manual GH-E2E (warm and cold paths) before `/gsd:verify-work`.

### Wave 0 Gaps
- [ ] `pkg/github/bridge/webhook_handler_test.go` — verify/auth/route (GH-BRIDGE-*)
- [ ] `pkg/github/bridge/resolve_test.go` — exact/glob/allowlist table (GH-BRIDGE-ROUTE)
- [ ] `internal/app/cmd/create_github_inbound_test.go` — provisioning + rollback (GH-INBOUND-Q)
- [ ] `pkg/compiler/userdata_github_inbound_test.go` — poller render + dormant (GH-POLLER)
- [ ] `cmd/km-github/main_test.go` — comment/review against httptest (GH-HELPER)
- [ ] `internal/app/config/config_github_test.go` — merge-list footgun + JSON env (GH-CLI)
- [ ] `internal/app/cmd/github_test.go` — init/manifest/status/doctor (GH-APP-SCOPE/GH-CLI/GH-DOCTOR)
- [ ] Dormant-when-unconfigured byte-identity tests (config + compiler)
- [ ] github-review.yaml added to `scripts/validate-all-profiles.sh` inventory
- Test doubles available: `github.MockSSMClient` (token.go:366), `GitHubAPIBaseURL` httptest var (token.go:27), Slack bridge mock adapters as templates.

## Sources

### Primary (HIGH confidence)
- Codebase (file:line verified): `cmd/km-slack-bridge/main.go`, `pkg/slack/bridge/{events_handler,events_interfaces,aws_adapters}.go`, `pkg/github/token.go`, `internal/app/cmd/{create,create_slack_inbound,destroy_slack_inbound,create_prompt,configure_github,init}.go`, `internal/app/config/config.go`, `pkg/aws/{sqs,eventbridge,sandbox_dynamo,metadata,identity}.go`, `pkg/compiler/userdata.go`, `pkg/profile/types.go`, `pkg/profile/schemas/sandbox_profile.schema.json`, `infra/modules/lambda-slack-bridge/v1.0.0/main.tf`, `infra/modules/github-token/v1.0.0/main.tf`, `cmd/create-handler/main.go`, `cmd/km-slack/main.go`.
- CLAUDE.md + `.planning/{REQUIREMENTS,STATE}.md`; MEMORY.md (round-trip, merge-list, providers, sidecars, env-export rules).
- GitHub docs — webhook events & payloads (issue_comment fields, `X-Hub-Signature-256` = HMAC-SHA256 hex of body, `X-GitHub-Delivery`, `X-GitHub-Event`): https://docs.github.com/en/webhooks/webhook-events-and-payloads
- GitHub docs — Create a review for a pull request (`POST /repos/{owner}/{repo}/pulls/{pull_number}/reviews`, event/body/comments/commit_id): https://docs.github.com/en/rest/pulls/reviews

### Secondary (MEDIUM confidence)
- GitHub REST reactions endpoint shape (`POST /repos/{owner}/{repo}/issues/comments/{comment_id}/reactions`, `content:"eyes"`, issues:write) — established API, page body not fully fetchable; cross-checked with token.go's existing GitHub API usage conventions (Accept/api-version headers).

## Metadata

**Confidence breakdown:**
- Reuse map / standard stack: HIGH — every analog located at exact file:line and read.
- Architecture / patterns: HIGH — direct clones of tested Slack bridge code.
- Pitfalls: HIGH — derived from in-tree code + documented project memories; Pitfall 1 (prompt-carry) verified against actual Phase 86 source.
- External GitHub API facts: HIGH for webhook signature/payload + reviews; MEDIUM for reactions endpoint exact body (well-known but page not fully rendered).

**Research date:** 2026-06-06
**Valid until:** 2026-07-06 (stable — internal codebase + stable GitHub REST v2022-11-28; re-verify if Slack bridge refactors land)
