# Phase 101: GitHub bridge orphan-repo helpful reply — Research

**Researched:** 2026-06-08
**Domain:** Go Lambda webhook routing; claim-aware scatter-gather relay; GitHub App installation-token comment posting; DDB nonce-table cooldown; km config/init/Terraform env pipeline
**Confidence:** HIGH — this phase upgrades Phase 100 (shipped) to the claim-aware shape that the **shipped** Slack Phase 96 already implements. Every analog file located and read in the live tree with verified line numbers. Only ONE genuinely new GitHub API surface (orphan PR/issue comment), and it reuses an already-wired client (`InstallationCommenter.PostComment`).

## Summary

Phase 100 shipped a **fire-and-forget** `HTTPPeerRelayer.Broadcast(ctx, rawBody []byte, ghHeaders) error` on the GitHub bridge (`pkg/github/bridge/relayer.go`). When the front door does not own a repo, it broadcasts the webhook to peers and returns 200; if no peer owns it, the human gets nothing (`github_relay_no_owner`). Phase 101 closes that gap by porting the **Slack Phase 96 claim-aware scatter-gather** machinery — which is live and complete in `pkg/slack/bridge/{relayer.go,events_handler.go,events_interfaces.go}` — onto the GitHub bridge.

The mechanism is a near-mechanical port with **one simplification vs Slack 96**: the GitHub orphan reply does **not** need a cross-install "running repos" list. Slack 96 lists running sandbox channels (`<#CID>` mentions) because a human can *join* a channel. The GitHub analog (per the ROADMAP description) just posts ONE guidance comment naming `github.repos:` wiring — there is nothing for the human to "join". So the peer claim response collapses to a bare `{"claimed":bool}` with **no channels payload**, and there is **no `RunningChannelLister` analog** to build. This removes the entire `SandboxChannelInfo` / `RunningChannels` / aggregate-list surface from the port.

The five moving parts: (1) **relayer** return type changes from `error` to `([]PeerClaimResult, error)` with rollout-safe legacy→claimed:true parsing; (2) **peer-side claim response** — the relayed `!matched` drop returns `{claimed:false}` and the relayed-owned dispatch returns `{claimed:true}`; (3) **front-door tally + orphan comment** via `maybePostOrphanReply` reusing the already-wired `InstallationCommenter`; (4) **cooldown** keyed `gh-router-cooldown:{owner}/{repo}#{number}` reusing the GitHub nonces store's existing conditional-put-with-TTL (`DynamoGitHubNonceStore.CheckAndStore`); (5) **`github.default_router` toggle** plumbed exactly like `slack.default_router` / `github.peer_bridges` (struct field auto-decoded by `UnmarshalKey("github",…)`, env `KM_GITHUB_DEFAULT_ROUTER`, terragrunt `get_env`, TF var + Lambda env).

**Primary recommendation:** Change `PeerRelayer.Broadcast` to `([]PeerClaimResult, error)` (Slack 96 shape, minus `Channels`), make the relayed `!matched` and relayed-owned paths return `{claimed:bool}` JSON, add a `maybePostGitHubOrphanComment` gated on `DefaultRouter && !anyClaimed && <mention already known> && cooldown`, reuse `InstallationCommenter.PostComment` with `payload.Installation.ID`, and plumb `github.default_router` via the already-proven Phase-100 config/init/TF path. No schema change, no sandbox recreate; deploy `make build-lambdas` + `km init --dry-run=false`.

## User Constraints

No CONTEXT.md exists for this phase (the phase dir contains only a `.gitkeep`). The authoritative constraints are the ROADMAP phase description + the Slack 96 design spec (the explicit analog) + Phase 100 RESEARCH (the file this upgrades). Treat these as locked:

### Locked Decisions (from ROADMAP description + Slack 96 spec analog)
- **Claim-aware scatter-gather, not fire-and-forget.** Upgrade Phase 100's `Broadcast(...) error` to return per-peer claim results. Each relayed-to peer returns `200 {claimed:bool}`; the front door tallies. **Zero claims ⇒ true orphan ⇒ post ONE guidance comment. Any claim ⇒ owner handled it ⇒ no comment.**
- **Rollout-safe mixed fleet (HARD invariant).** A peer still on Phase-100 code returns a plain `200` with `"ok"` body (no JSON). The front-door tally MUST treat any legacy/non-JSON/HTTP-error/timeout response as **`claimed:true`** — never produce a false "nobody owns this". This mirrors Slack 96's "Legacy Phase-95 peer responses treated as claimed:true".
- **One guidance comment on the PR/issue (GH-ORPHAN-REPLY).** Front-door-only. Content explains no sandbox is bound to the repo and how to wire it (`github.repos:` in `km-config.yaml`). NO cross-install repo list (unlike Slack's channel list — there is nothing to "join" on GitHub).
- **Per-(repo,number) cooldown (GH-ORPHAN-COOLDOWN)** via the nonces table, reusing Phase 96's `{prefix}-router-cooldown` key pattern. Key shape: `gh-router-cooldown:{owner}/{repo}#{number}`. One comment per busy PR, not one per @-mention.
- **Front-door-only toggle `github.default_router: true`** in `km-config.yaml`. Dormant by default ⇒ **byte-identical to Phase 100** when off (fire-and-forget broadcast, terminal drop, no comment, no claim-gather overhead).
- **No SandboxProfile schema change ⇒ no `km init --sidecars`, no sandbox recreate.** Deploy = `make build-lambdas` (clean) + `km init --dry-run=false`. (Env-block change requires full terragrunt apply, NOT `--sidecars`.)
- **No new merge-list entry.** `github.default_router` is a `github.*` key auto-decoded by the existing `v.UnmarshalKey("github", &cfg.Github)`; the `"github"` merge entry already exists (`config.go:551`). Prove with a round-trip test — do NOT add a `"github.default_router"` merge entry (Phase 100 established this for `github.peer_bridges`).

### Claude's Discretion
- Exact guidance-comment wording (recommend a clear single paragraph: "No klanker sandbox is bound to `owner/repo`. To enable the bot here, an operator must add this repo under `github.repos:` in `km-config.yaml` and run `km init`. See `docs/github-bridge.md`.").
- Cooldown TTL (recommend 3600s to match Slack 96's `RouterCooldown.Reserve(…, 3600)`).
- Whether `maybePostGitHubOrphanComment` lives in `webhook_handler.go` or a new `orphan_reply.go` file in the same package (recommend new file for review locality, mirroring Slack's co-location in `events_handler.go`).
- Whether to reuse the existing `WebhookHandler.Commenter` field (already present, line 133) for the orphan post (recommend YES — it is already `InstallationCommenter`).

### Deferred Ideas (OUT OF SCOPE — do not research/build)
- Cross-install "running repos / running sandboxes" list in the comment (no GitHub analog of "join a channel").
- Agentic self-serve `km create` from a comment ("@bot spin me up a box").
- Cross-prefix dispatch on the same repo; cross-account/region federation; multi-hop relay; shared install registry.
- A `km doctor` check for `default_router` — **Slack 96 shipped NO doctor check for `slack.default_router`** (verified: `grep default_router internal/app/cmd/doctor*.go` → empty). Do not invent one; GH-ORPHAN has no doctor requirement.

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| GH-ORPHAN-CLAIM | Upgrade Phase-100 fire-and-forget broadcast to claim-aware scatter-gather: peers return `{claimed:bool}`, front door tallies; zero claims ⇒ orphan, any claim ⇒ owner handled | Mapping rows 1–4 (relayer return type, `peerRelayResponse`, peer-side claim emit, front-door tally); Slack template `relayer.go:65`, `events_handler.go:275-300`, `events_interfaces.go:26,44` |
| GH-ORPHAN-REPLY | Zero claims ⇒ front door posts ONE guidance comment on the PR/issue naming `github.repos:` wiring | Mapping rows 5–6; reuse `InstallationCommenter.PostComment` (`aws_adapters.go:1022`) already wired as `WebhookHandler.Commenter` (line 133); `payload.Installation.ID` carries install ID on relayed webhook |
| GH-ORPHAN-COOLDOWN | Per-(repo,number) cooldown via nonces table, key `gh-router-cooldown:{owner}/{repo}#{number}`, TTL 3600s | Mapping row 7; reuse `DynamoGitHubNonceStore.CheckAndStore` (`aws_adapters.go:190`) — identical conditional-put-with-TTL semantics to Slack's `Reserve`; Slack template `events_handler.go:618`, `cmd/km-slack-bridge/main.go:618` |
| GH-ORPHAN-ROLLOUT | Front-door-only `github.default_router: true`; dormant by default ⇒ byte-identical to Phase 100; legacy/no-body peer 200 ⇒ claimed:true | Mapping rows 8–13 (config/init/TF); rollout-safety parse in `relayer.go:149-158`; dormancy gate `maybePostOrphanReply:601` |
| GH-ORPHAN-E2E | End-to-end live verification (two installs, one App, unowned repo) | Decision-table + tally + cooldown unit tests + manual UAT; Validation Architecture below |

## Standard Stack

No new third-party libraries. Everything is in-repo:

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go stdlib `net/http`, `sync`, `context` | go 1.25.5 | Bounded parallel scatter-gather | Already in `pkg/github/bridge/relayer.go` (Phase 100) and `pkg/slack/bridge/relayer.go` (Phase 96) |
| Go stdlib `encoding/json` | go 1.25.5 | Parse `{claimed:bool}` peer response; marshal claim verdict | Slack `peerRelayResponse` (`relayer.go:19`); GitHub already imports it |
| GitHub App installation token (in-repo) | — | Mint token → POST orphan comment | `InstallationCommenter.PostComment` (`aws_adapters.go:1022`) already minted+wired for Phase 99 command replies |
| DynamoDB nonces table | — | Per-(repo,number) cooldown | `DynamoGitHubNonceStore.CheckAndStore` (`aws_adapters.go:190`), shared `{prefix}-slack-bridge-nonces` table |
| viper (`internal/app/config`) | — | `github.default_router` decode | `UnmarshalKey("github", &cfg.Github)` (`config.go:675`) auto-decodes new `github.*` fields |

**Installation:** none. `make build-lambdas` (clean) rebuilds the bridge zip; `km init --dry-run=false` deploys the env-block change. `km-github-bridge` IS in `lambdaBuilds()` (it deploys today).

## Architecture Patterns

### THE core deliverable — file-by-file Slack-96 → GitHub mapping (verified live line numbers)

| # | Concern | Slack 96 source (verified) | GitHub target (current state) | Needed change |
|---|---------|---------------------------|-------------------------------|---------------|
| 1 | Peer-response struct | `relayer.go:19` `peerRelayResponse{Claimed bool; Channels []SandboxChannelInfo}` | `pkg/github/bridge/relayer.go` (no such struct) | NEW `peerRelayResponse{ Claimed bool \`json:"claimed"\` }` — **drop `Channels`** (no repo list in GitHub orphan comment) |
| 2 | Claim result type | `events_interfaces.go:26` `PeerClaimResult{PeerURL; Claimed; Channels}` | `pkg/github/bridge/interfaces.go` (no such type) | NEW `PeerClaimResult{ PeerURL string; Claimed bool }` — **drop `Channels`** |
| 3 | Relayer interface | `events_interfaces.go:44` `Broadcast(ctx, rawBody string, h) ([]PeerClaimResult, error)` | `interfaces.go:10-16` `Broadcast(ctx, rawBody []byte, h) error` (fire-and-forget) | CHANGE return type to `([]PeerClaimResult, error)`. Keep `rawBody []byte` (GitHub bytes). Update interface doc (remove the "do not add claim-result slice" note that Phase 100 left). |
| 4 | Relayer impl: tally + parse | `relayer.go:65-165` (`Broadcast` collects `resultCh`, `postToPeer` parses JSON; legacy/error/non-2xx ⇒ `Claimed:true`) | `relayer.go:65-120` (current: fire-and-forget, `resp.Body.Close()` only) | REWRITE to collect `[]PeerClaimResult`: read each peer body, `json.Unmarshal` into `peerRelayResponse`; **rollout safety** — transport err / non-2xx / unparseable ⇒ `Claimed:true`. Keep `wg.Wait()` (Lambda freeze), keep `relayBroadcastTimeout`/`X-KM-Relayed:1`/header forwarding **unchanged**. |
| 5 | Peer-side claim emit (miss) | `events_handler.go:246-274` relayed-miss returns `{claimed:false, channels:[...]}` JSON | `webhook_handler.go:228-234` relayed-miss returns plain `"ok"` | CHANGE: when `relayed && !matched`, return `200 {claimed:false}` JSON (content-type application/json). **No channels.** |
| 6 | Peer-side claim emit (owned) | `events_handler.go:518-532` relayed-owned returns `{claimed:true}` JSON | `webhook_handler.go:505` final `return 200 "ok"` (matched/dispatched path) | ADD: when the request carried `x-km-relayed` AND matched/dispatched, return `200 {claimed:true}` JSON. Non-relayed (real GitHub delivery) owned response stays plain `"ok"`. |
| 7 | Front-door tally + orphan post | `events_handler.go:275-300` capture `[]PeerClaimResult`, `anyClaimed` loop, `if !anyClaimed { maybePostOrphanReply(...) }` | `webhook_handler.go:235-242` front-door `Broadcast` branch (ignores result) | CHANGE: capture `claimResults`, tally `anyClaimed`; `if !anyClaimed { h.maybePostGitHubOrphanComment(ctx, payload) }`. Return 200 regardless. |
| 8 | Orphan-post method | `events_handler.go:599-700` `maybePostOrphanReply` (DefaultRouter gate, channel guard, mention gate, cooldown Reserve, build list, PostMessage) | (new) `pkg/github/bridge/orphan_reply.go` | NEW `maybePostGitHubOrphanComment(ctx, payload)`: gate on `h.DefaultRouter`; **skip the re-mention gate** (the front door already passed mention/PR filters before Resolve — see note below); cooldown via `h.OrphanCooldown.CheckAndStore`; reuse `h.Commenter.PostComment` with `payload.Installation.ID`, `OwnerFromFullName`, `RepoFromFullName`, `payload.Issue.Number`. |
| 9 | Handler field: DefaultRouter | `events_handler.go:103` `DefaultRouter bool` | `webhook_handler.go` `WebhookHandler` struct (~line 141) | NEW `DefaultRouter bool` field (default false ⇒ dormant) |
| 10 | Handler field: cooldown store | `events_handler.go:110` `RouterCooldown RouterCooldownStore` | `WebhookHandler` struct | NEW `OrphanCooldown` field. **Reuse `DeliveryNonceStore`** (its `CheckAndStore` already does conditional-put+TTL) — no new interface needed; OR add a thin `OrphanCooldownStore` for clarity. Recommend reuse `DeliveryNonceStore` semantics (replayed=true ⇒ cooldown active ⇒ skip). |
| 11 | Config struct field | `config.go:66` `SlackConfig.DefaultRouter *bool` | `config.go` `GithubConfig` (struct at line ~146) | NEW `DefaultRouter *bool \`mapstructure:"default_router" yaml:"default_router,omitempty"\`` — **NO new merge-list entry** (UnmarshalKey github covers it) |
| 12 | Population | `config.go:673-675` `if v.IsSet("slack.default_router"){...}` | `config.go:675` `v.UnmarshalKey("github", &cfg.Github)` | **Already populated** by existing UnmarshalKey. No new population block. Prove with round-trip test. |
| 13 | init.go env export | `init.go:1076-1090` `KM_SLACK_DEFAULT_ROUTER` (bool, drift WARN) | `init.go` ~after line 1134 (`KM_GITHUB_PEER_BRIDGES` block) | NEW: export `KM_GITHUB_DEFAULT_ROUTER` from `cfg.Github.DefaultRouter` (`*bool`, only when non-nil, `strconv.FormatBool`, env-wins drift WARN). Copy the Slack `KM_SLACK_DEFAULT_ROUTER` block verbatim, rename. |
| 14 | Terragrunt get_env | `lambda-slack-bridge/terragrunt.hcl:121` `slack_default_router = get_env("KM_SLACK_DEFAULT_ROUTER","false")` | `lambda-github-bridge/terragrunt.hcl` inputs (after line 95 `github_peer_bridges`) | NEW: `github_default_router = get_env("KM_GITHUB_DEFAULT_ROUTER","false")` |
| 15 | TF variable | `lambda-slack-bridge/v1.0.0/variables.tf:142` `variable "slack_default_router"` | `lambda-github-bridge/v1.1.0/variables.tf` (after `github_peer_bridges` at line 62) | NEW: `variable "github_default_router" { type=string default="false" }` |
| 16 | TF env block | `lambda-slack-bridge/v1.0.0/main.tf:341` `KM_SLACK_DEFAULT_ROUTER = var.slack_default_router` | `lambda-github-bridge/v1.1.0/main.tf:287` (after `KM_GITHUB_PEER_BRIDGES`) | NEW: `KM_GITHUB_DEFAULT_ROUTER = var.github_default_router` in `environment.variables` |
| 17 | cmd main.go wire | `cmd/km-slack-bridge/main.go:294-301` (`if env=="true" { DefaultRouter=true; RunningChannels=...; RouterCooldown=adapter }`) | `cmd/km-github-bridge/main.go` after relayer build (~line 244, before/at `WebhookHandler{}`) | NEW: `if os.Getenv("KM_GITHUB_DEFAULT_ROUTER")=="true" { webhookHandler.DefaultRouter = true; webhookHandler.OrphanCooldown = nonceStore }`. **No RunningChannels analog.** `nonceStore` (`DynamoGitHubNonceStore`, already built at main.go:173) is the cooldown store. |
| 18 | relayer + handler tests | `relayer_test.go`, `events_handler_test.go:1853-2200` (orphan happy-path, claim-short-circuit, non-mention, off, empty-list, cooldown, bot-loop) | `pkg/github/bridge/relayer_test.go`, new `webhook_handler_phase101_test.go` | NEW table-driven tests — see Validation Architecture |
| 19 | config test | `config_test.go:880` `TestLoadSlackPeerBridges_Set` | `internal/app/config/config_test.go` | NEW `TestLoadGithubDefaultRouter_Set` — round-trip + nil-when-absent + prove-no-merge-entry |
| 20 | init test | (Slack export tested in init test file) | `internal/app/cmd/init_test.go` | NEW `KM_GITHUB_DEFAULT_ROUTER` export + drift WARN test |

### How `Broadcast`'s signature changes (the explicit answer)

**Phase 100 (current, `interfaces.go:10`):**
```go
Broadcast(ctx context.Context, rawBody []byte, ghHeaders map[string]string) error
```
**Phase 101 (target — Slack 96 shape, minus Channels):**
```go
Broadcast(ctx context.Context, rawBody []byte, ghHeaders map[string]string) ([]PeerClaimResult, error)
```
This is a **return-type change to the existing method**, not a new method (matches how Slack 96 evolved `Broadcast` from Phase 95). The `rawBody []byte` parameter stays (GitHub uses bytes; Slack uses string — keep the GitHub convention). All call sites are internal:
- `webhook_handler.go:239` front-door branch — change `if bErr := h.Relayer.Broadcast(...)` to `claimResults, bErr := h.Relayer.Broadcast(...)` and add the tally.
- `cmd/km-github-bridge/main.go` builds `*HTTPPeerRelayer` (unchanged constructor; only the method body changes).
- All `fakePeerRelayer` test doubles update to return `([]PeerClaimResult, error)` (Slack template: `events_handler_test.go:1403`).

### The peer-side claim response contract (rows 5–6)

A relayed request (`x-km-relayed:1`) is processed by a peer. The peer's verdict:
- **relayed + Resolve() miss** (`webhook_handler.go:228-234`): return `200 {"claimed":false}` (was plain `"ok"`). This is the "I don't own it" signal.
- **relayed + Resolve() match → dispatched** (after the matched path, `webhook_handler.go:505`): return `200 {"claimed":true}`. This is "I own it; I handled it".
- A peer that **never reaches the claim emit** (Phase-100 code) returns plain `200 "ok"` → front door's `json.Unmarshal` fails → **`Claimed:true`** (rollout safety).

> **Subtlety:** Slack's relayed-owned `{claimed:true}` is emitted at `events_handler.go:518-532` (after dispatch, before the final return). The GitHub equivalent sits just before `webhook_handler.go:505 return WebhookResponse{200,"ok"}`. Guard it with `if req.Headers["x-km-relayed"] != ""` so non-relayed (real GitHub) deliveries keep their plain `"ok"` body — GitHub doesn't read the body, but byte-identity for the non-relayed owned path is part of the dormancy contract.

### The orphan-comment method (rows 7–8) — gating logic

Slack's `maybePostOrphanReply` (`events_handler.go:599`) gates on: (1) `DefaultRouter`, (2) non-empty channel, (3) **bot @-mention**, (4) cooldown. **The GitHub mention gate is already satisfied** by the front door's pre-Resolve filters — BUT note the Phase-100 reorder: `Resolve()` runs at `webhook_handler.go:222` **before** the @-mention filter (`webhook_handler.go:280`). So on the `!matched` front-door path, the @-mention check has **NOT** run yet. The orphan comment should fire only for messages that @-mention the bot (don't spam every unowned-repo comment). Two options:

- **Option A (recommended):** In `maybePostGitHubOrphanComment`, re-run `ContainsMention(payload.Comment.Body, botLogin)` (cheap, pure) as the GitHub analog of Slack's mention gate. Mirrors Slack exactly (Slack also re-checks the mention inside `maybePostOrphanReply` at `events_handler.go:614`).
- Option B: hoist the mention check before the relay branch — rejected (changes Phase-100 dispatch ordering / byte-identity).

So the GitHub orphan gate is: (1) `h.DefaultRouter`, (2) `ContainsMention(comment.Body, botLogin)`, (3) cooldown `CheckAndStore("gh-router-cooldown:"+owner/repo#number", 3600)` returns `(false, nil)` (first time), (4) `h.Commenter != nil`. On any gate fail/error → silent return; Handle still returns 200.

### Cooldown reuses the GitHub nonce store directly (row 10)

Slack built a `routerCooldownAdapter` (`cmd/km-slack-bridge/main.go:607-618`) wrapping `DynamoNonceStore.Reserve` with a `"router-cooldown:"` prefix. The GitHub bridge's `DynamoGitHubNonceStore.CheckAndStore(ctx, key, ttlSeconds)` (`aws_adapters.go:190`) **already** does the identical conditional `PutItem attribute_not_exists(nonce)` + `ttl_expiry` — returning `(true, nil)` on replay. So:
- Cooldown key: `"gh-router-cooldown:" + owner + "/" + repo + "#" + strconv.Itoa(number)`.
- `seen, err := h.OrphanCooldown.CheckAndStore(ctx, key, 3600)`; `seen==true` ⇒ cooldown active ⇒ skip the comment; `seen==false` ⇒ first in window ⇒ post.
- **No new adapter, no new interface, no new table.** Wire `webhookHandler.OrphanCooldown = nonceStore` (the same `*DynamoGitHubNonceStore` already built at `main.go:173`). The `gh-router-cooldown:` prefix isolates it from `github-delivery:` keys in the shared `{prefix}-slack-bridge-nonces` table.

### `github.default_router` plumbing (rows 11–17) — proven Phase-100 path

Identical to the `github.peer_bridges` pipeline that Phase 100 just shipped:
1. Struct field on `GithubConfig` (`*bool` tri-state, mirrors `SlackConfig.DefaultRouter`).
2. **No merge-list entry** — `UnmarshalKey("github",…)` at `config.go:675` already decodes it (the `"github"` merge entry at `config.go:551` is sufficient). Prove with `TestLoadGithubDefaultRouter_Set`.
3. `init.go` exports `KM_GITHUB_DEFAULT_ROUTER` (copy the `KM_SLACK_DEFAULT_ROUTER` block at `init.go:1076-1090`; `*bool` → only export when non-nil; env-wins drift WARN).
4. `terragrunt.hcl` `github_default_router = get_env("KM_GITHUB_DEFAULT_ROUTER","false")`.
5. TF `variable "github_default_router"` (default `"false"`) + `KM_GITHUB_DEFAULT_ROUTER = var.github_default_router` in the Lambda env block (edit `v1.1.0` in place — Phase 100 added `github_peer_bridges` to `v1.1.0` in place; same precedent).
6. `cmd/km-github-bridge/main.go` reads `os.Getenv("KM_GITHUB_DEFAULT_ROUTER") == "true"` and sets `webhookHandler.DefaultRouter = true` + `webhookHandler.OrphanCooldown = nonceStore`.

### Dormancy invariant (GH-ORPHAN-ROLLOUT)

When `github.default_router` is absent/false:
- `KM_GITHUB_DEFAULT_ROUTER` unset/`"false"` ⇒ `DefaultRouter=false`.
- The relayer return-type change is invisible behaviorally: the front-door branch still calls `Broadcast`, but with `DefaultRouter=false` the tally result is **discarded** (no `maybePostGitHubOrphanComment` call). To preserve "no claim-gather overhead" literally, **short-circuit**: only parse/tally when `h.DefaultRouter` is true. Recommend: relayer always returns `[]PeerClaimResult` (cheap), but `Handle()` only loops/tally + posts when `h.DefaultRouter`. The relayer's body-read adds one `io.ReadAll` per peer per orphan webhook — negligible, and the dormant install (no peers OR default_router off) is unaffected because the front-door relay branch only runs on a `!matched` non-relayed request anyway.
- Peers still return plain `"ok"` unless upgraded — and even an upgraded peer returning `{claimed:...}` is harmless to a front door with `DefaultRouter=false` (result discarded). **Byte-identical observable behavior to Phase 100.**

### Anti-Patterns to Avoid
- **Porting `Channels`/`SandboxChannelInfo`/`RunningChannelLister`.** The GitHub orphan comment has NO repo list. Drop the entire channel-aggregation surface — it is Slack-specific (humans join channels; they don't "join" repos).
- **Adding a `"github.default_router"` merge-list entry.** Redundant — `UnmarshalKey("github",…)` covers it. Prove with a test (Phase 100 Pitfall 2).
- **Inventing a `km doctor` check.** Slack 96 shipped none for `slack.default_router`; GH-ORPHAN has no doctor requirement.
- **Re-running the mention gate by hoisting it before Resolve.** Keep Phase-100 ordering; re-check `ContainsMention` inside the orphan method instead (Slack does exactly this).
- **Async comment post.** Lambda freezes on return — post synchronously with a bounded context (Slack `maybePostOrphanReply` uses a 5s `context.WithTimeout`).
- **Building a new cooldown table/interface.** Reuse `DynamoGitHubNonceStore.CheckAndStore` with a `gh-router-cooldown:` prefix.
- **Deploying with `--sidecars`.** Env-block change needs `km init --dry-run=false`.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Post a PR/issue comment as the App | New token-mint + REST call | `InstallationCommenter.PostComment` (`aws_adapters.go:1022`), already wired as `WebhookHandler.Commenter` (line 133) | Phase 99 already mints installation token + POSTs comments; `payload.Installation.ID` is on the relayed webhook |
| Per-(repo,number) cooldown | New DDB table / Reserve helper | `DynamoGitHubNonceStore.CheckAndStore` (`aws_adapters.go:190`) with `gh-router-cooldown:` prefix | Identical conditional-put+TTL to Slack's `Reserve`; shared `{prefix}-slack-bridge-nonces` table |
| Claim-aware scatter-gather | New relay engine | Copy `pkg/slack/bridge/relayer.go:65-165` (`Broadcast` + `postToPeer`), drop `Channels` | Production-proven (Slack 96); rollout-safe legacy→claimed:true already encoded |
| `default_router` config/env/TF pipeline | New plumbing | Copy the `github.peer_bridges` Phase-100 path (rows 11–17) | End-to-end pattern shipped 2 commits ago; merge-list deviation already proven |
| Front-door tally + orphan gate | New decision logic | Copy `events_handler.go:275-300` + `maybePostOrphanReply:599-700` (minus channel list) | Gates (DefaultRouter, mention, cooldown) and "any claim ⇒ silent" are already correct |

**Key insight:** Every primitive exists in shipped code. Phase 101 is COPYING the Slack 96 claim machinery onto the GitHub bridge with ONE deletion (the channel/repo list) and reusing two already-wired GitHub clients (`InstallationCommenter`, `DynamoGitHubNonceStore`).

## Common Pitfalls

### Pitfall 1: Installation ID on the relayed webhook
**What goes wrong:** The orphan comment needs an installation token, which needs `installationID`. On the **front door**, the repo is unowned — but the webhook payload still carries `payload.Installation.ID` (it's the App install on the repo's owner, present on every `issue_comment` delivery).
**Why it's fine:** The front door relays the **verbatim** body; it has the full payload including `Installation`. `InstallIDString(payload.Installation.ID)` + `InstallationCommenter.PostComment` works exactly as the Phase 99 command-reply path.
**How to avoid:** Use `payload.Installation.ID` directly (confirmed `payload.go:21` `InstallField`). Add a defensive `if payload.Installation.ID == 0 { skip }` guard.
**Warning signs:** 404/401 on comment POST → wrong install ID or App not installed on that repo (legitimately can't comment — log + skip).

### Pitfall 2: Mention gate skipped due to Phase-100 reorder
**What goes wrong:** Phase 100 moved `Resolve()` BEFORE the @-mention filter. On the `!matched` front-door path the mention check never ran, so posting on every unowned-repo comment would spam non-mention comments.
**How to avoid:** Re-check `ContainsMention(payload.Comment.Body, botLogin)` inside `maybePostGitHubOrphanComment` (Slack does the analog at `events_handler.go:614`). Only post when the comment actually @-mentions the bot.
**Warning signs:** Orphan comments appearing on PR comments that never tagged the bot.

### Pitfall 3: Lambda freeze on async post (inherited)
**What goes wrong:** Posting the orphan comment in a goroutine after `Handle` returns → context elapses on freeze, comment never lands.
**How to avoid:** Post SYNCHRONOUSLY with a bounded `context.WithTimeout(ctx, 5*time.Second)`, then return 200 (Slack `maybePostOrphanReply:695`). The relayer's `wg.Wait()` already covers the broadcast leg.

### Pitfall 4: False orphan from a slow/legacy peer (the safety invariant)
**What goes wrong:** A peer times out or is on Phase-100 code (plain `"ok"`); if treated as "not claimed", the front door posts a false "nobody owns this" while an install actually owns the repo.
**How to avoid:** In `postToPeer`, ANY transport error / non-2xx / `json.Unmarshal` failure ⇒ `PeerClaimResult{Claimed:true}` (Slack `relayer.go:149-158`). Test with a legacy-`"ok"` peer and a timing-out peer both ⇒ no comment.
**Warning signs:** Orphan comment posted while a known owner exists in the fleet.

### Pitfall 5: env-block change deployed with `--sidecars`
**What goes wrong:** `km init --sidecars` rebuilds the zip but does NOT update the Lambda `environment.variables` block → `KM_GITHUB_DEFAULT_ROUTER` never reaches the Lambda.
**How to avoid:** Deploy = `make build-lambdas` (clean) + `km init --dry-run=false`. Document in CLAUDE.md Phase 101 note + `docs/github-bridge.md`. (`project_km_init_lambdas_doesnt_deploy`, `feedback_km_init_full_apply`.)

### Pitfall 6: Redundant merge-list entry / silent config drop (inverted footgun)
**What goes wrong:** Following `project_config_key_merge_list` blindly, a planner adds a `"github.default_router"` merge entry + population block, conflicting with the existing `UnmarshalKey("github",…)`.
**How to avoid:** Add ONLY the struct field. `TestLoadGithubDefaultRouter_Set` proves the value round-trips with no new merge entry (Phase 100 established this for `github.peer_bridges`).

## Code Examples

### Relayer Broadcast → claim-aware (simplified Slack, no Channels)
```go
// Source: pkg/slack/bridge/relayer.go:65-165 (Phase 96), drop Channels.
func (r *HTTPPeerRelayer) Broadcast(ctx context.Context, rawBody []byte, ghHeaders map[string]string) ([]PeerClaimResult, error) {
    if len(r.PeerURLs) == 0 { return nil, nil }
    bctx, cancel := context.WithTimeout(ctx, relayBroadcastTimeout)
    defer cancel()
    resultCh := make(chan PeerClaimResult, len(r.PeerURLs))
    var wg sync.WaitGroup
    for _, peer := range r.PeerURLs {
        wg.Add(1)
        go func(u string) {
            defer wg.Done()
            res, err := r.postToPeer(bctx, u, rawBody, ghHeaders)
            if err != nil { // transport/ctx error → conservative claim
                slog.Warn("km-github-bridge: relay peer error", "peer", u, "err", err, "event", "github_relay_peer_error")
                resultCh <- PeerClaimResult{PeerURL: u, Claimed: true}
                return
            }
            resultCh <- res
        }(peer)
    }
    wg.Wait(); close(resultCh) // MUST wait — Lambda freeze
    var out []PeerClaimResult
    for res := range resultCh { out = append(out, res) }
    return out, nil
}

// postToPeer: headers unchanged from Phase 100 (X-Hub-Signature-256, X-GitHub-Event,
// X-GitHub-Delivery, X-KM-Relayed:1). Then read body and parse:
//   non-2xx OR unparseable OR legacy "ok" ⇒ Claimed:true (rollout safety).
//   {claimed:false} ⇒ Claimed:false.
```

### Front-door tally + orphan post (webhook_handler.go front-door branch)
```go
// Source: pkg/slack/bridge/events_handler.go:275-300, adapted (no channels).
if h.Relayer != nil {
    claimResults, bErr := h.Relayer.Broadcast(ctx, req.RawBody, req.Headers)
    if bErr != nil { h.log().Warn("github-bridge: relay broadcast partial failure", "err", bErr, "repo", payload.Repository.FullName) }
    if h.DefaultRouter { // dormant when false → no overhead, Phase-100 byte-identical
        anyClaimed := false
        for _, r := range claimResults { if r.Claimed { anyClaimed = true; break } }
        if !anyClaimed { h.maybePostGitHubOrphanComment(ctx, payload, botLogin) }
    }
} else {
    h.log().Info("github-bridge: no repo config, silent drop", "repo", payload.Repository.FullName)
}
return WebhookResponse{StatusCode: 200, Body: "ok"}
```

### Orphan comment method (new orphan_reply.go)
```go
// Source: pattern from pkg/slack/bridge/events_handler.go:599-700, minus channel list.
func (h *WebhookHandler) maybePostGitHubOrphanComment(ctx context.Context, payload IssueCommentPayload, botLogin string) {
    if !h.DefaultRouter { return }                                   // gate 1
    if !ContainsMention(payload.Comment.Body, botLogin) { return }    // gate 2 (Phase-100 reorder skipped it)
    if h.Commenter == nil || payload.Installation.ID == 0 { return }
    owner := OwnerFromFullName(payload.Repository.FullName)
    repo  := RepoFromFullName(payload.Repository.FullName)
    key := fmt.Sprintf("gh-router-cooldown:%s/%s#%d", owner, repo, payload.Issue.Number)
    if h.OrphanCooldown != nil {                                      // gate 3: cooldown
        seen, err := h.OrphanCooldown.CheckAndStore(ctx, key, 3600)
        if err != nil { h.log().Warn("github-bridge: orphan cooldown check failed; skipping", "err", err); return }
        if seen { h.log().Debug("github-bridge: orphan cooldown active; suppressing", "repo", payload.Repository.FullName, "number", payload.Issue.Number); return }
    }
    body := fmt.Sprintf("No klanker sandbox is bound to `%s`. To enable the bot here, an operator must add this repo under `github.repos:` in `km-config.yaml` and run `km init`. See the github-bridge runbook.", payload.Repository.FullName)
    cctx, cancel := context.WithTimeout(ctx, 5*time.Second); defer cancel()
    if err := h.Commenter.PostComment(cctx, InstallIDString(payload.Installation.ID), owner, repo, payload.Issue.Number, body); err != nil {
        h.log().Warn("github-bridge: orphan comment post failed", "repo", payload.Repository.FullName, "err", err)
    }
}
```

### Peer-side claim emit (webhook_handler.go)
```go
// relayed + miss (replace the plain "ok" at line 233):
if relayed {
    h.log().Warn("github-bridge: relay miss — no owner for relayed delivery", "repo", payload.Repository.FullName, "event", "github_relay_no_owner")
    return jsonClaim(false) // 200 {"claimed":false}
}
// relayed + owned (just before final return at line 505):
if req.Headers["x-km-relayed"] != "" { return jsonClaim(true) } // 200 {"claimed":true}
return WebhookResponse{StatusCode: 200, Body: "ok"}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| GitHub relay = fire-and-forget (Phase 100) | GitHub relay = claim-aware scatter-gather (Phase 96 analog) | This phase (101) | Front door learns orphan vs owned |
| Orphan repo @-mention silently dropped (`github_relay_no_owner`) | Front door posts ONE guidance comment | This phase | Human gets actionable response |
| `PeerRelayer.Broadcast(...) error` | `Broadcast(...) ([]PeerClaimResult, error)` | This phase | Return-type change; all call sites internal |

**Deprecated/outdated:**
- The `interfaces.go:8-9` note "do not add a claim-result slice return here" was the Phase-100 deferral marker — it is now SUPERSEDED. Update that doc when changing the signature.

## Open Questions

1. **Reuse `DeliveryNonceStore` for cooldown, or add an `OrphanCooldownStore` interface?**
   - What we know: `DynamoGitHubNonceStore.CheckAndStore` already provides exact conditional-put+TTL semantics; Slack added a thin adapter only because its `RouterCooldownStore.Reserve` had an error-return contract vs `Reserve` directly.
   - Recommendation: Reuse `DeliveryNonceStore` (set `OrphanCooldown DeliveryNonceStore` field = the same `nonceStore`). One fewer interface; `seen==true ⇒ suppress` reads cleanly. If the planner wants a self-documenting type, a one-method `OrphanCooldownStore` alias is acceptable but not required.

2. **Comment wording / link.** The exact guidance text and whether to link `docs/github-bridge.md` (the bot can't render a relative repo path). Recommend a plain-text instruction with the literal `github.repos:` and `km init`. Discretion.

3. **Module version: edit `v1.1.0` in place vs bump.** Phase 100 added `github_peer_bridges` to `v1.1.0` in place; this is the same additive `default="false"` env var. Recommend in-place edit of `v1.1.0` (no live `source =` change). Confirm against project module-versioning convention during planning.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (table-driven), go 1.25.5 |
| Config file | none (standard `go test`) |
| Quick run command | `go test ./pkg/github/bridge/... -run 'Orphan|Relay|Claim' -count=1` |
| Full suite command | `go test ./pkg/github/bridge/... ./internal/app/config/... ./internal/app/cmd/... -count=1` |

> `make test` deliberately excludes `internal/app/cmd` and `cmd/km-*` (Makefile:75). Run those packages directly; the relayer + handler tests live in `pkg/github/bridge` which IS covered by `make test`.

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| GH-ORPHAN-CLAIM | `Broadcast` returns `[]PeerClaimResult`; front door tally: any claim ⇒ no post, zero ⇒ post | unit | `go test ./pkg/github/bridge/... -run 'ClaimTally' -count=1` | ❌ Wave 0 |
| GH-ORPHAN-CLAIM | peer-side: relayed+miss ⇒ `{claimed:false}`; relayed+owned ⇒ `{claimed:true}`; non-relayed owned ⇒ plain `ok` | unit | `go test ./pkg/github/bridge/... -run 'PeerClaim' -count=1` | ❌ Wave 0 |
| GH-ORPHAN-REPLY | zero claims + mention + cooldown-clear ⇒ exactly ONE `PostComment`; non-mention ⇒ none; `Commenter==nil` ⇒ none | unit | `go test ./pkg/github/bridge/... -run 'OrphanComment' -count=1` | ❌ Wave 0 |
| GH-ORPHAN-COOLDOWN | second orphan within window ⇒ suppressed; after window ⇒ posts; key = `gh-router-cooldown:{owner}/{repo}#{number}` | unit | `go test ./pkg/github/bridge/... -run 'OrphanCooldown' -count=1` | ❌ Wave 0 |
| GH-ORPHAN-ROLLOUT | legacy `"ok"` peer ⇒ Claimed:true (no false orphan); timeout peer ⇒ Claimed:true; `DefaultRouter=false` ⇒ no post + no tally | unit | `go test ./pkg/github/bridge/... -run 'Rollout|RelayerLegacy|DefaultRouterOff' -count=1` | ❌ Wave 0 |
| GH-ORPHAN-ROLLOUT | `github.default_router` round-trips; nil when absent; no new merge entry | unit | `go test ./internal/app/config/... -run GithubDefaultRouter -count=1` | ❌ Wave 0 |
| GH-ORPHAN-ROLLOUT | `KM_GITHUB_DEFAULT_ROUTER` export + env-wins drift WARN | unit | `go test ./internal/app/cmd/... -run GithubDefaultRouter -count=1` | ❌ Wave 0 |
| GH-ORPHAN-E2E | two installs, one App, unowned repo @-mention ⇒ exactly one guidance comment from front door; owned repo ⇒ no comment; second mention within window ⇒ no comment | manual UAT | n/a (documented runbook) | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/github/bridge/... -count=1` (relayer + handler — fast, no AWS)
- **Per wave merge:** `go test ./pkg/github/bridge/... ./internal/app/config/... ./internal/app/cmd/... -count=1`
- **Phase gate:** full suite green + `make build-lambdas` succeeds + Terraform validate on `lambda-github-bridge/v1.1.0` before `/gsd:verify-work`.

### Wave 0 Gaps
- [ ] `pkg/github/bridge/relayer_test.go` — extend: claim-result tally, legacy-`"ok"`→Claimed:true, non-2xx→Claimed:true, timeout→Claimed:true, `{claimed:false}` parse — covers GH-ORPHAN-CLAIM, GH-ORPHAN-ROLLOUT
- [ ] `pkg/github/bridge/webhook_handler_phase101_test.go` — peer-side claim emit (relayed miss/owned/non-relayed), front-door tally short-circuit, orphan happy-path, non-mention skip, `DefaultRouter=false` silent, cooldown suppress/expire, `Commenter==nil` skip, `Installation.ID==0` skip — covers GH-ORPHAN-CLAIM, GH-ORPHAN-REPLY, GH-ORPHAN-COOLDOWN, GH-ORPHAN-ROLLOUT
- [ ] Test double: `fakePeerRelayer.Broadcast` must return `([]PeerClaimResult, error)`; `fakeCommentPoster` capturing PostComment calls (check existing `CommentPoster` test double in `webhook_handler_phase99_test.go`); `fakeCooldown` (or fake `DeliveryNonceStore`) with configurable `seen`
- [ ] `internal/app/config/config_test.go` — `TestLoadGithubDefaultRouter_Set` (round-trip + nil-when-absent + prove-no-merge-entry) — covers GH-ORPHAN-ROLLOUT
- [ ] `internal/app/cmd/init_test.go` — `KM_GITHUB_DEFAULT_ROUTER` export + drift WARN — covers GH-ORPHAN-ROLLOUT
- [ ] UAT doc (two-install, one-App, unowned-repo) — covers GH-ORPHAN-E2E
- [ ] (No doctor test — Slack 96 shipped no `default_router` doctor check; none required here.)

*Framework already installed; no new test deps.*

## Sources

### Primary (HIGH confidence — read in full from the live tree)
- `pkg/slack/bridge/relayer.go` — claim-aware `Broadcast`+`postToPeer`, rollout-safe legacy→Claimed:true (the engine to port, minus Channels)
- `pkg/slack/bridge/events_handler.go:240-300,500-700` — relayed-miss `{claimed:false}` emit, relayed-owned `{claimed:true}` emit, front-door tally, `maybePostOrphanReply` gates
- `pkg/slack/bridge/events_interfaces.go:13-69` — `PeerClaimResult`, `PeerRelayer`, `RouterCooldownStore` (drop `SandboxChannelInfo`/`RunningChannelLister` for GitHub)
- `pkg/github/bridge/relayer.go` — current fire-and-forget `Broadcast` (Phase 100) to upgrade
- `pkg/github/bridge/webhook_handler.go` — Phase-100 Resolve-reorder + `!matched` relay branch (line 222-249), final 200 (line 505); orphan insertion points
- `pkg/github/bridge/interfaces.go:10-16,67-74,102-119,132-162` — `PeerRelayer` (signature to change), `DeliveryNonceStore` (cooldown reuse), `GitHubReactor`, `CommentPoster`
- `pkg/github/bridge/aws_adapters.go:182-211` `DynamoGitHubNonceStore.CheckAndStore`; `:1020-1067` `InstallationCommenter.PostComment` (orphan comment client)
- `pkg/github/bridge/payload.go:21,46-48` — `Installation InstallField` (install ID on relayed webhook)
- `cmd/km-github-bridge/main.go:173,204-272` — nonceStore + commenter + relayer build + `WebhookHandler{}` wiring (where to set DefaultRouter + OrphanCooldown)
- `internal/app/config/config.go:54-66,146-176,551,675` — `SlackConfig.DefaultRouter` template, `GithubConfig` (where field goes), merge-list `"github"`, `UnmarshalKey("github",…)`
- `internal/app/cmd/init.go:1076-1090,1120-1135` — `KM_SLACK_DEFAULT_ROUTER` + `KM_GITHUB_PEER_BRIDGES` export templates
- `infra/live/use1/lambda-github-bridge/terragrunt.hcl:95` + `infra/modules/lambda-github-bridge/v1.1.0/{variables.tf:62,main.tf:287}` — github_peer_bridges plumbing (template for github_default_router)
- `infra/modules/lambda-slack-bridge/v1.0.0/{variables.tf:142,main.tf:341}` — `slack_default_router` TF template
- `cmd/km-slack-bridge/main.go:294-301,607-618` — DefaultRouter + cooldown adapter wiring template
- `docs/superpowers/specs/2026-06-05-slack-default-router-design.md` — the authoritative analog design spec
- `.planning/phases/100-.../100-RESEARCH.md` — Phase 100 file map this upgrades

### Secondary (MEDIUM)
- CLAUDE.md Phase 95/96/97/98/100 bridge sections; REQUIREMENTS.md Phase 96 SLACK-RTR IDs (analog requirement shape)
- MEMORY.md: `project_config_key_merge_list`, `project_km_init_lambdas_doesnt_deploy`, `project_km_init_skips_existing_lambda_zips`, `feedback_verify_deploy_surface_not_just_code`, `feedback_km_init_full_apply`

### Tertiary (LOW)
- None — entirely internal-codebase mechanical port; no external/web sources needed.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — zero new deps; every primitive (relay engine, comment client, nonce store, config pipeline) exists in shipped code
- File mapping: HIGH — every Slack-96 source AND every GitHub target located and read; line numbers verified against the live tree
- Claim/cooldown/config plumbing: HIGH — Slack 96 is shipped end-to-end; Phase 100 already proved the github.* config→env→TF path and the merge-list deviation
- "No repo list" simplification: HIGH — ROADMAP description explicitly says "names github.repos: wiring guidance" (no channel/repo list); Slack's channel list exists only because humans join channels
- No-doctor-check: HIGH — verified `grep default_router internal/app/cmd/doctor*.go` is empty; Slack 96 shipped none
- Pitfalls: HIGH — all derive from documented project gotchas + the shipped Slack relayer's own PITFALL notes

**Research date:** 2026-06-08
**Valid until:** 2026-07-08 (stable — internal codebase, no fast-moving external deps)
