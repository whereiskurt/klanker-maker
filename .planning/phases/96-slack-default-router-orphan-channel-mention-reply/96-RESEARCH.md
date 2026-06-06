# Phase 96: Slack Default Router — Research

**Researched:** 2026-06-05
**Domain:** Slack bridge relay extension — claim-aware scatter-gather + orphan-channel threaded reply
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Why only the front door:**
One App => only the front-door bridge receives raw Slack events; peers only see
relayed copies. So only the front door can detect a true orphan and reply.
`slack.default_router` is effectively a front-door capability toggle; setting
it on a non-front-door install is a no-op.

**Core: broadcast → claim-aware scatter-gather:**
A relayed-request handler returns `200 {json}` instead of `200 "ok"`:
- owner: `{ "claimed": true }`
- non-owner: `{ "claimed": false, "channels": [{"id","alias","profile"}, ...] }`
  where channels = this peer's running sandboxes (`km-sandboxes` `state=running`
  AND `slack_channel_id` present).
- Front door includes its OWN running channels in the aggregate.
- Tally: any `claimed:true` => owner handled it => NO router reply. Zero claims =>
  true orphan.
- ROLLOUT SAFETY (critical): a peer on Phase-95 code returns legacy plain `"ok"`.
  Front door treats any unparseable/legacy/HTTP-error response as `claimed:true`
  (conservative — never post a false "no sandbox here"). Mixed fleet safe.

**Trigger (ALL three required):**
1. message @-mentions the bot (reuse Phase 91 `<@{bot_user_id}>` detection), AND
2. front-door `FetchByChannel` misses locally, AND
3. scatter-gather returns zero claims.

**Reply:**
- Threaded on the triggering message (`thread_ts` = msg ts).
- Content: "no sandbox bound" + convention `#sb-{alias}-{profile}` + running
  channels (front door + all peers) rendered as `<#CID>` mentions. Empty list
  => guidance-only variant.
- Per-channel cooldown: reuse the existing pause-hint cooldown (default 3600s).
- In-channel `chat:write` only — NO new Slack scopes, NO `app_mention`.

**Config plumbing (mirror slack.mention_only EXACTLY):**
- `internal/app/config/config.go`: `SlackConfig.DefaultRouter *bool`
  (`mapstructure:"default_router"`); add `"slack.default_router"` to v2->v
  merge-list (footgun: `project_config_key_merge_list`); populate via
  `v.GetBool` when `v.IsSet`.
- `internal/app/cmd/init.go`: export `KM_SLACK_DEFAULT_ROUTER` (bool) with
  env-wins drift WARN (mirror MentionOnly/ReactAlways block).
- `terragrunt.hcl`: `slack_default_router = get_env("KM_SLACK_DEFAULT_ROUTER", "false")`.
- TF module variables.tf + main.tf: `slack_default_router` var -> Lambda env.
- `cmd/km-slack-bridge/main.go`: read env; wire router (default-off, nil-safe).

**Anti-loop:**
The bot's reply is filtered by the existing self-message / `isBotLoop` guard
(events_handler.go ~176-180); it cannot re-trigger the router.

### Claude's Discretion

(None specified — all material choices are locked above.)

### Deferred Ideas (OUT OF SCOPE)

- Agentic self-serve create via EventBridge (north star; separate phase).
- Non-member channel handling (`app_mention` + `chat:write.public`).
- DM fallback (`im:write`).
- Caching the running-channel enumeration (only if perf shows a need; cooldown
  already bounds frequency).
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| SLACK-RTR-CFG | `slack.default_router *bool` in `km-config.yaml`: `SlackConfig.DefaultRouter` struct field, v2->v merge-list entry, tri-state population; absent/false => router off (Phase 95 behavior) | config.go lines 24-54 pattern (MentionOnly/ReactAlways); merge-list lines 410-417; population lines 496-515; init.go export lines 875-913 |
| SLACK-RTR-GATHER | Relay upgraded to claim-aware scatter-gather: relayed-request handler returns `200 {claimed:bool, channels:[...]}` JSON; non-owner peer returns its running sandbox channels; front door parses + tallies; legacy `"ok"`/HTTP-error => `claimed:true` | relayer.go (full): `Broadcast` discards response body (line 134); must be changed to read + parse; `postToPeer` returns `error`; interface must return `[]PeerClaimResult` |
| SLACK-RTR-ORPHAN | True-orphan detection: front-door `FetchByChannel` miss + zero peer claims => orphan; any claim => owner handled, no router action | events_handler.go lines 202-233: the miss site; current path returns 200 unconditionally after broadcast |
| SLACK-RTR-REPLY | On orphan + bot-mention + member channel + `default_router:true`: post exactly one threaded reply listing running sandbox channels aggregated across front door + all peers | `SlackPosterAdapter.PostMessage` (aws_adapters.go line 352): already supports `thread_ts`; `EventsHandler.Slack` field already wired to `initPoster` in main.go line 327 |
| SLACK-RTR-COOLDOWN | Per-channel cooldown (reuse pause-hint mechanism, default 3600s): one reply per window, not per mention | `DDBPauseHinter` pattern (aws_adapters.go lines 1171-1275); DESIGN ISSUE: orphan channel has no km-sandboxes row; cooldown must use a different key strategy — see Section 5 below |
| SLACK-RTR-SAFE | `default_router` defaults false => byte-identical to Phase 95; non-mention messages and bot's own reply never trigger the router; config drift WARN on `KM_SLACK_DEFAULT_ROUTER` | `isBotLoop` (lines 469-495): handles bot-self-message; `WireMentionOnly` pattern (main.go lines 339-353) for env read |
| SLACK-RTR-E2E | Two installs + one Slack App: @-mention in an unowned member channel => one threaded reply listing both installs' running channels; repeat within cooldown => no second reply; owned channel => no router reply (manual; real AWS + Slack) | UAT only — cannot be automated |
</phase_requirements>

---

## Summary

Phase 96 builds directly on Phase 95's federated relay by upgrading the broadcast from fire-and-forget to claim-aware scatter-gather. The key code sites are all in `pkg/slack/bridge/`: `relayer.go` (Broadcast must now return per-peer results), `events_handler.go` (the miss site at lines 202-233 must consume gather results and gate the orphan reply), and `aws_adapters.go` (needs a new running-channels lister and a cooldown store that works without a km-sandboxes row).

The largest design decision that the spec does not fully resolve is where to store the per-channel router-reply cooldown timestamp for an orphan channel — by definition, orphan channels have no row in `km-sandboxes`. The recommended solution is to write a TTL-keyed item to the existing `km-slack-bridge-nonces` table with a `router-cooldown:{channel_id}` key and a `ttl_expiry` of `now + 3600`. This reuses an already-provisioned table, requires no new infrastructure, and integrates cleanly with the existing `DynamoNonceStore.Reserve` / `CheckAndStore` interface — a `Reserve` on that key succeeds the first time (post the reply), and returns `ErrNonceReplayed` on subsequent mentions within the window (suppress).

The config plumbing is a three-line addition per the Phase 95 `PeerBridges` pattern, and the `*bool` wiring mirrors `MentionOnly`/`ReactAlways` exactly. With `default_router` absent or false, behavior is byte-identical to Phase 95 — every code path is gated behind the nil/false check.

**Primary recommendation:** Implement in three waves: (1) relayer scatter-gather contract change + tests; (2) events_handler orphan-detection + reply posting; (3) config plumbing + infra vars + main.go wiring + docs.

---

## Standard Stack

### Core (AS-MERGED Phase 95 code)

| File | Role | AS-MERGED state |
|------|------|----------------|
| `pkg/slack/bridge/relayer.go` | `HTTPPeerRelayer.Broadcast` — POST to peers, discard response body | Response body discarded at line 134: `_, _ = io.Copy(io.Discard, resp.Body)`. MUST change to read + parse |
| `pkg/slack/bridge/events_interfaces.go` | `PeerRelayer` interface | `Broadcast(ctx, rawBody string, slackHeaders map[string]string) error` — return type must change to include claim results |
| `pkg/slack/bridge/events_handler.go` | Miss site + mention detection | Miss at lines 202-233; mention scan at lines 266-275 (`<@{uid}>` in `msg.Text`) |
| `pkg/slack/bridge/aws_adapters.go` | `SlackPosterAdapter`, `DDBPauseHinter`, `DDBSandboxByChannel` | `PostMessage` at line 352; pause-hint pattern at lines 1171-1275; `FetchByChannel` at line 962 |
| `cmd/km-slack-bridge/main.go` | Cold-start wiring | Phase 95 relay wired at lines 268-282; Poster wired at line 327; `WireMentionOnly` at lines 339-353 |
| `internal/app/config/config.go` | `SlackConfig` struct + merge-list + population | `MentionOnly` at line 33; `ReactAlways` at line 43; `PeerBridges` at line 54; merge-list at lines 410-417; population at lines 496-515 |
| `internal/app/cmd/init.go` | `KM_SLACK_*` export block | Lines 875-913 contain `MentionOnly`, `ReactAlways`, `PeerBridges` export blocks |
| `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` | `get_env` inputs | Phase 95 line 116: `slack_peer_bridges = get_env("KM_SLACK_PEER_BRIDGES", "")` |
| `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` | TF variable declarations | `slack_peer_bridges` at line 133; `slack_mention_only` at line 106 |
| `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` | Lambda env block | `KM_SLACK_PEER_BRIDGES = var.slack_peer_bridges` at line 331 |

---

## Architecture Patterns

### 1. Scatter-Gather: Upgrading `Broadcast` Return Type

**What (AS-BUILT relayer.go):**
`HTTPPeerRelayer.Broadcast` (line 53) returns `error`. `postToPeer` (line 100) builds the HTTP request, calls `client.Do`, and at line 134 discards the entire response body. The current interface in `events_interfaces.go` line 17 declares `Broadcast(...) error`.

**What must change for Phase 96:**

The `PeerRelayer` interface must change to return per-peer claim results:

```
// In events_interfaces.go:
type PeerClaimResult struct {
    PeerURL  string
    Claimed  bool
    Channels []SandboxChannelInfo // empty when Claimed=true
}

type SandboxChannelInfo struct {
    ID      string `json:"id"`
    Alias   string `json:"alias"`
    Profile string `json:"profile"`
}

type PeerRelayer interface {
    Broadcast(ctx context.Context, rawBody string, slackHeaders map[string]string) ([]PeerClaimResult, error)
}
```

**`postToPeer` must change to:**
1. Read the response body instead of discarding it.
2. Attempt `json.Unmarshal` into `struct{ Claimed bool; Channels []SandboxChannelInfo }`.
3. If unmarshal fails OR body is `"ok"` (legacy plain-string) OR HTTP status is non-2xx: return `PeerClaimResult{Claimed: true}` (rollout safety — conservative).
4. Otherwise return the parsed result.

**Rollout-safety rule (LOCKED):** Any legacy/error/unparseable response maps to `claimed:true`. This means a mixed-version fleet (some peers on Phase 95, some on Phase 96) never produces a false orphan reply — the front door sees at least one "claim" and stays silent.

**`errCh` pattern:** The existing `chan peerErr` in `Broadcast` should become `chan PeerClaimResult`. The `WaitGroup` and bounded 2.5s context stay unchanged — this is a critical Lambda pitfall (PITFALL 4 in the existing RESEARCH.md; lines 84-86 in relayer.go: `wg.Wait()` MUST be called before returning).

**Relayed-request handler response (peer side):**
When a peer processes a relayed event, the `EventsResponse.Body` is currently `"ok"`. The peer must now return a JSON-encoded `{claimed, channels}` struct. `EventsResponse.Body` is already a `string` (events_types.go line 67) that main.go's `handle` function passes through verbatim to `LambdaFunctionURLResponse.Body` (line 383). No adapter change needed — just set `Body` to `json.Marshal(result)` and `Headers: {"content-type":"application/json"}`.

Two cases from the peer's perspective:
- **Relayed + owns channel (the `present+yes` cell):** Already falls through to the normal SQS dispatch path. At the end of `Handle` it returns `EventsResponse{StatusCode:200, Body:"ok"}` (line 451). This must change to `Body: json.Marshal({claimed:true})`.
- **Relayed + misses (the `present+no` cell):** Returns at line 220 with `Body:"ok"`. This must change to: query local running sandboxes, return `Body: json.Marshal({claimed:false, channels:[...]})`.

### 2. The Miss Site in `events_handler.go` (Lines 202-233 AS-MERGED)

```go
// AS-MERGED (lines 202-233):
if info.SandboxID == "" || info.QueueURL == "" {
    if req.Headers["x-km-relayed"] != "" {
        // TERMINAL: relayed + no owner => drop
        h.log().Warn("events: relay miss — no owner for relayed message", ...)
        return EventsResponse{StatusCode: 200, Body: "ok"}  // CHANGE: return {claimed:false, channels:[...]}
    }
    if h.Relayer != nil {
        if err := h.Relayer.Broadcast(ctx, req.Body, req.Headers); err != nil { ... }
        // CHANGE: capture []PeerClaimResult, tally, gate orphan reply
    } else {
        h.log().Warn("events: unknown channel or inbound disabled", ...)
    }
    return EventsResponse{StatusCode: 200, Body: "ok"}
}
```

**Phase 96 changes to this block:**

**Relayed-miss path (lines 215-220):** When `x-km-relayed` is present AND the channel is not owned:
- Query local running sandbox channels (new `RunningChannelLister` interface).
- Return `EventsResponse{StatusCode:200, Body: json.Marshal({claimed:false, channels:[...]}), Headers:{"content-type":"application/json"}}`.

**Front-door miss path (lines 222-233):** When no `x-km-relayed` header:
- Call `h.Relayer.Broadcast(ctx, req.Body, req.Headers)` — now returns `([]PeerClaimResult, error)`.
- Tally: if ANY result has `Claimed:true`, return 200 (owner handled it — today's behavior).
- If zero claims (true orphan):
  - Gate on `h.DefaultRouter` bool being true (wired from `KM_SLACK_DEFAULT_ROUTER` env).
  - Gate on bot-@-mention: re-run the `<@{bot_user_id}>` check on `msg.Text` (same logic as lines 267-270 in the mention-only filter, but here it's a prerequisite for the reply, not a filter-out).
  - Gate on per-channel cooldown.
  - If all gates pass: aggregate channels from (front-door own running channels + all peers' `channels` fields) and post threaded reply.
- Return 200 regardless.

**Where the mention check lives (lines 266-275, AS-MERGED):**
```go
uid, err := h.BotUserID.Fetch(ctx)
if err != nil { ... }
else if uid != "" && !strings.Contains(msg.Text, "<@"+uid+">") {
    return EventsResponse{StatusCode: 200, Body: "ok"}
}
```
The Phase 96 mention gate uses the same `h.BotUserID.Fetch(ctx)` + `strings.Contains(msg.Text, "<@"+uid+">")` pattern. `h.BotUserID` is already wired in `wireEventsHandler` (main.go line 242) and primed by `WireMentionOnly` (line 341: `fetcher.PrimeCache(uid)`). No new dependencies needed.

**`isBotLoop` self-message guard (lines 469-495, AS-MERGED):**
The bot's own threaded reply is a message with `BotID != ""` (Slack sets `bot_id` on all bot posts). `isBotLoop` returns `true` immediately at line 472 when `m.BotID != ""`. This runs at line 185, BEFORE the miss site at line 202. The router reply cannot re-trigger the router. This is a structural guarantee — no additional guard needed.

### 3. `EventsResponse` + `EventsRequest` Shapes

**EventsRequest** (events_types.go lines 59-64):
```go
type EventsRequest struct {
    Headers map[string]string // adapter MUST lowercase keys
    Body    string
}
```
Header keys are already lowercased by `lowercaseHeaders` in main.go (line 407). The `x-km-relayed` header check at events_handler.go line 215 reads `req.Headers["x-km-relayed"]` — this works because the adapter lowercases.

**EventsResponse** (events_types.go lines 66-71):
```go
type EventsResponse struct {
    StatusCode int
    Body       string            // JSON-encoded body or plain text
    Headers    map[string]string
}
```
`Body` is a plain `string`. In main.go's `handle` function (line 383), `resp.Body` is passed verbatim as `LambdaFunctionURLResponse.Body` — no re-encoding. Setting `Body` to a JSON string and `Headers: map[string]string{"content-type": "application/json"}` is sufficient. The existing `url_verification` path (events_handler.go lines 144-149) uses this pattern already.

### 4. Posting a Threaded Reply

**`SlackPosterAdapter.PostMessage`** (aws_adapters.go lines 349-379):
- Signature: `PostMessage(ctx context.Context, channel, subject, body, threadTS string) (string, error)`
- When `threadTS != ""`, sets `"thread_ts": threadTS` in the payload (line 364).
- An empty `subject` renders body alone (comment at line 350-351: "An empty subject renders the body alone (no bold header)").
- Returns the message `ts` on success.

**How Poster is injected into EventsHandler:**
In main.go, `wireEventsHandler` (line 204) sets `eventsHandler.Slack = initPoster` at line 327. `initPoster` is a `*bridge.SlackPosterAdapter` constructed at `init()` line 115. The `EventsHandler.Slack` field is declared at line 57 of events_handler.go as `Slack SlackPoster`. The router can reuse `h.Slack.PostMessage(...)` directly.

**Thread anchor for the reply:**
The triggering message's `ts` (not `thread_ts`) is the correct `thread_ts` argument. For a top-level message, `msg.ThreadTS == ""` and `msg.TS` is the anchor. For a reply in an existing thread, `msg.ThreadTS` is the anchor. Use:
```go
replyThreadTS := msg.ThreadTS
if replyThreadTS == "" {
    replyThreadTS = msg.TS
}
```
This matches the existing `threadTS` computation at events_handler.go lines 290-294.

**Reply text format:**
```
No sandbox is bound to this channel. To work with a bot, join one of its channels — they're named #sb-{alias}-{profile}. Currently running:
• <#C123> — orc (patch)  • <#C456> — wrkr (hardened) …
```
`<#CHANNELID>` is Slack's channel mention syntax (no name needed — Slack resolves it per-viewer). When the aggregate list is empty, omit the list and give guidance-only. Rendered as plain mrkdwn using `PostMessage` (mrkdwn is set `true` in the payload at line 362).

### 5. Cooldown: The Orphan-Channel Problem

**The problem:** `DDBPauseHinter` (aws_adapters.go lines 1171-1275) stores `last_pause_hint_ts` on the `km-sandboxes` row keyed by `sandbox_id`. An orphan channel has NO row in `km-sandboxes` — there is no `sandbox_id` to key on.

**Options evaluated:**

| Option | Pros | Cons |
|--------|------|------|
| Write `last_router_reply_ts` to `km-sandboxes` on a synthetic/orphan row | Same table | Row would need to be created (dangerous, pollutes table with fake rows) |
| New DDB table `km-slack-router-cooldowns` | Clean | New infra, new IAM grants, new TF module |
| `km-slack-bridge-nonces` with `router-cooldown:{channel_id}` key + TTL | Zero new infra; reuses existing table + IAM; TTL is the cooldown | Reserve succeeds on first call (post), ErrNonceReplayed suppresses within TTL window |
| `km-slack-threads` with a synthetic row | Reuses existing table | Table schema is (channel_id, thread_ts) — repurposing is awkward |

**Recommendation: use `km-slack-bridge-nonces` with a TTL-keyed entry.**

The `DynamoNonceStore.Reserve(ctx, nonce, ttlSeconds)` call (aws_adapters.go line 142):
- Inserts `{nonce: "router-cooldown:{channel_id}", ttl_expiry: now+3600}` with `attribute_not_exists(nonce)`.
- Returns `nil` on first insertion (cooldown not previously set => post the reply, cooldown starts).
- Returns `ErrNonceReplayed` when TTL hasn't expired (cooldown active => suppress).

The bridge Lambda already has IAM grants to write to the nonces table (it's used for event dedup). The `nonceStoreAdapter` in main.go wraps `DynamoNonceStore` to provide `CheckAndStore(bool)`. The handler can call `initNonces.Reserve(ctx, "router-cooldown:"+msg.Channel, 3600)` directly — but cleaner is to inject an `EventNonceStore` interface field onto `EventsHandler` for the cooldown (or a separate `RouterCooldownStore` interface) that the planner can wire at `wireEventsHandler`.

**Nonce table schema** (as provisioned, from aws_adapters.go lines 143-156):
- PK: `nonce` (String)
- `ttl_expiry` (Number, Unix epoch) — DDB TTL attribute
- TTL enabled on `ttl_expiry` column
- No additional GSIs needed

**Interface to declare** (in events_interfaces.go):
```go
// RouterCooldownStore gates the per-channel router-reply cooldown.
// Reserve returns nil (first call → reply is permitted) or ErrNonceReplayed
// (within the cooldown window → suppress the reply).
type RouterCooldownStore interface {
    Reserve(ctx context.Context, channelID string, cooldownSeconds int) error
}
```
Wire in main.go as `eventsHandler.RouterCooldown = &routerCooldownAdapter{inner: initNonces}` where the adapter wraps `DynamoNonceStore.Reserve` with the `"router-cooldown:"` key prefix.

### 6. Listing Running Sandbox Channels

**`km-sandboxes` table attributes** (from `pkg/aws/sandbox_dynamo.go` line 47 + bridge adapter line 980-1024):
- PK: `sandbox_id` (String)
- `slack_channel_id` (String) — written at create time; absent when Slack inbound not configured
- `state` (String): `running`, `starting`, `paused`, `stopped`, `failed`, `nocap`, etc.
- `alias` (String, optional) — absent when empty (GSI pollution prevention)
- `profile_name` (String)

**Existing GSI:** `slack_channel_id-index` (GSI on `slack_channel_id`) — used by `DDBSandboxByChannel.FetchByChannel`. This is a point-query (known channel ID → sandbox). The new query is the OTHER direction: "give me all sandboxes where state=running AND slack_channel_id is present."

**No GSI on `state`** — this is a Scan with FilterExpression. At typical sandbox counts (tens, not thousands), a Scan is acceptable. The design spec notes: "Caching the running-channel enumeration (only if perf shows a need; cooldown already bounds frequency)."

**New DDB query (Scan with filter):**
```go
// Filter: attribute_exists(slack_channel_id) AND #s = :running
// ExpressionAttributeNames: {"#s": "state"} (state is a DDB reserved word)
// ExpressionAttributeValues: {":running": "running"}
// ProjectionExpression: "sandbox_id, slack_channel_id, alias, profile_name"
// With pagination (LastEvaluatedKey loop)
```

**New interface to declare** (in events_interfaces.go):
```go
type SandboxChannelInfo struct {
    ID      string `json:"id"`
    Alias   string `json:"alias"`
    Profile string `json:"profile"`
}

// RunningChannelLister enumerates sandboxes with state=running and a bound
// Slack channel. Used by the scatter-gather handler to build the channel list
// for the router reply (Phase 96).
type RunningChannelLister interface {
    ListRunning(ctx context.Context) ([]SandboxChannelInfo, error)
}
```

Note: `state` is a DDB reserved word. The Scan MUST use ExpressionAttributeNames `{"#s": "state"}` and `FilterExpression: "attribute_exists(slack_channel_id) AND #s = :running"`. Pagination: loop on `LastEvaluatedKey` until nil — same pattern as `checkOrphanedDDBRows` in Phase 94.

**New adapter** (in aws_adapters.go):
```go
type DDBRunningChannelLister struct {
    Client    DDBScanAPI   // new narrow interface: Scan only
    TableName string
}
```
`DDBScanAPI` is a new narrow interface:
```go
type DDBScanAPI interface {
    Scan(ctx context.Context, in *dynamodb.ScanInput, opts ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}
```
A `*dynamodb.Client` satisfies this. The existing Lambda IAM policy already grants `dynamodb:Scan` on `km-sandboxes` (it must — `km doctor` uses it; and the Phase 94 `checkOrphanedDDBRows` uses Scan on the sandboxes table via `pkg/aws`). Verify in `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` that `dynamodb:Scan` is in the policy before relying on it.

**Cross-install aggregation in the handler:**
The front door's own running channels come from its local `h.RunningChannels.ListRunning(ctx)` call. The peers' channels come from each peer's `PeerClaimResult.Channels` field in the gather result. The handler merges both slices, deduplicates by channel ID (in case of misconfiguration where a channel appears in two installs — channel-name uniqueness invariant means this shouldn't happen, but defensive dedup is cheap), and renders the merged list in the reply.

### 7. Config Plumbing (Mirror `slack.mention_only` EXACTLY)

All five touchpoints with file:line references, AS-MERGED:

**`internal/app/config/config.go`:**
- `SlackConfig` struct: lines 24-55. Add `DefaultRouter *bool` after `PeerBridges` at line 54:
  ```go
  DefaultRouter *bool `mapstructure:"default_router" yaml:"default_router,omitempty"`
  ```
- Merge-list: lines 410-417. Add `"slack.default_router"` after line 417 (`"slack.peer_bridges"`).
- Population block: lines 496-515. Add after line 515 (the `peer_bridges` block):
  ```go
  if v.IsSet("slack.default_router") {
      val := v.GetBool("slack.default_router")
      cfg.Slack.DefaultRouter = &val
  }
  ```

**`internal/app/cmd/init.go`:**
- Export block ends at line 913 (closing brace of the `ExportTerragruntEnvVars` function or its equivalent block). Add after line 913 (the PeerBridges export block), mirroring the MentionOnly pattern at lines 875-882:
  ```go
  if cfg.Slack.DefaultRouter != nil {
      yamlSlackDefaultRouter := strconv.FormatBool(*cfg.Slack.DefaultRouter)
      if envVal := os.Getenv("KM_SLACK_DEFAULT_ROUTER"); envVal != "" && envVal != yamlSlackDefaultRouter {
          fmt.Fprintf(os.Stderr, "WARN: KM_SLACK_DEFAULT_ROUTER=%s (env) overrides km-config.yaml slack.default_router=%s\n", envVal, yamlSlackDefaultRouter)
      } else if envVal == "" {
          os.Setenv("KM_SLACK_DEFAULT_ROUTER", yamlSlackDefaultRouter)
      }
  }
  ```

**`infra/live/use1/lambda-slack-bridge/terragrunt.hcl`:**
- Currently ends at line 122 (closing `}`). Add after line 116 (the `slack_peer_bridges` entry):
  ```
  # Phase 96 — default-router toggle. Operator sets slack.default_router: true
  # in km-config.yaml for the front-door install only. Default false = dormant.
  # Requires km init --dry-run=false (NOT --sidecars) — same constraint as mention_only.
  slack_default_router = get_env("KM_SLACK_DEFAULT_ROUTER", "false")
  ```

**`infra/modules/lambda-slack-bridge/v1.0.0/variables.tf`:**
- Add after line 136 (closing `}` of `slack_peer_bridges`):
  ```hcl
  variable "slack_default_router" {
    description = "When 'true', the front-door bridge posts a helpful threaded reply in orphan channels after a scatter-gather finds no owner (Phase 96). Default 'false' = dormant."
    type        = string
    default     = "false"
  }
  ```

**`infra/modules/lambda-slack-bridge/v1.0.0/main.tf`:**
- Add after line 331 (`KM_SLACK_PEER_BRIDGES = var.slack_peer_bridges`):
  ```
  KM_SLACK_DEFAULT_ROUTER = var.slack_default_router
  ```

**`cmd/km-slack-bridge/main.go`:**
- In `wireEventsHandler`, after the Phase 95 relay wiring block (lines 268-282), add:
  ```go
  // Phase 96: default router. Off by default; only meaningful on the front-door install.
  if os.Getenv("KM_SLACK_DEFAULT_ROUTER") == "true" {
      eventsHandler.DefaultRouter = true
      slog.Info("km-slack-bridge: default-router enabled")
  }
  ```
- `eventsHandler.DefaultRouter` is a new `bool` field on `EventsHandler` (not `*bool` — the handler reads `h.DefaultRouter` as a plain bool; `false` is the zero value and means "off").
- Also wire `eventsHandler.RunningChannels` (the `RunningChannelLister`) and `eventsHandler.RouterCooldown` (the `RouterCooldownStore`) in `wireEventsHandler`.

### 8. Cold-Start Wiring in `cmd/km-slack-bridge/main.go`

**Existing pattern (Phase 95)** at lines 268-282:
```go
if raw := os.Getenv("KM_SLACK_PEER_BRIDGES"); raw != "" {
    var peers []string
    for _, u := range strings.Split(raw, ",") {
        if u = strings.TrimSpace(u); u != "" {
            peers = append(peers, u)
        }
    }
    if len(peers) > 0 {
        eventsHandler.Relayer = &bridge.HTTPPeerRelayer{
            PeerURLs:   peers,
            HTTPClient: initHTTPClient,
        }
        slog.Info("km-slack-bridge: federated relay enabled", "peer_count", len(peers))
    }
}
```

**Phase 96 additions** (after the relay block):
```go
// Phase 96: default router
if os.Getenv("KM_SLACK_DEFAULT_ROUTER") == "true" {
    eventsHandler.DefaultRouter = true
    eventsHandler.RunningChannels = &bridge.DDBRunningChannelLister{
        Client:    initDDB,
        TableName: sandboxesTable,
    }
    eventsHandler.RouterCooldown = &routerCooldownAdapter{inner: initNonces}
    slog.Info("km-slack-bridge: default-router enabled")
}
```

`routerCooldownAdapter` is a private type in `main.go` (analogous to `nonceStoreAdapter` at lines 571-584) that wraps `DynamoNonceStore.Reserve` with the `"router-cooldown:"` prefix.

**Bot user ID:** Already available via `eventsHandler.BotUserID` (wired at line 242). The router's mention check reuses the same `h.BotUserID.Fetch(ctx)` call.

**`initNonces` is package-level** (declared at line 70, constructed at line 94 in `init()`). It is directly accessible in `wireEventsHandler`.

### 9. Anti-Patterns to Avoid

- **DO NOT** start a goroutine for the router reply — the existing pause-hint hinter (steps 9 in Handle) does this, but Phase 96's reply must complete before Handle returns (same reasoning as Phase 75.2: goroutines are frozen when Lambda returns; any mid-reply HTTP call has its deadline elapse during freeze). The reply is fast (one `chat.postMessage`) and the overall Lambda timeout is 60s, so staying synchronous is safe.
- **DO NOT** call `h.Relayer.Broadcast` and then immediately check for the router. The handler must wait for `Broadcast` to return with results (it already does `wg.Wait()` synchronously).
- **DO NOT** add `dynamodb:Scan` to the bridge IAM policy if it's already there. Verify first in `infra/modules/lambda-slack-bridge/v1.0.0/main.tf`.
- **DO NOT** post the reply if `msg.Channel == ""` — defensive nil guard.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Per-channel cooldown for orphan channels | Custom DDB table | `km-slack-bridge-nonces` table + `DynamoNonceStore.Reserve` with `"router-cooldown:{channel}"` key | Zero new infra; nonces table already IAM-granted; TTL is exactly the cooldown mechanism |
| Bot-mention detection | Custom regex | `strings.Contains(msg.Text, "<@"+uid+">")` (line 270) | Exact Slack mention format; already tested |
| Threaded reply | Custom HTTP | `h.Slack.PostMessage(ctx, channel, "", text, threadTS)` (line 352) | Already wired, tested, handles rate limits |
| Scatter-gather parallelism | `errgroup` or channels | Existing `sync.WaitGroup` + bounded `context.WithTimeout(ctx, 2500ms)` pattern in relayer.go | Pattern already proven; Lambda-safe |

---

## Common Pitfalls

### Pitfall 1: Merge-List Footgun (`project_config_key_merge_list`)
**What goes wrong:** Adding `SlackConfig.DefaultRouter` to the struct and populating it from `v.IsSet("slack.default_router")` but forgetting to add `"slack.default_router"` to the v2->v merge-list (config.go lines 410-417). The value is then silently ignored — `km-config.yaml slack.default_router: true` has no effect.
**How to avoid:** The merge-list entry (`"slack.default_router"` at the appropriate position in the list) MUST be added in the same commit as the struct field. See MEMORY entry `project_config_key_merge_list`.
**Warning sign:** `slack.default_router: true` in yaml + `km init` produces no `KM_SLACK_DEFAULT_ROUTER` export.

### Pitfall 2: `km init --sidecars` Does Not Deploy Env-Block Changes
**What goes wrong:** After adding `KM_SLACK_DEFAULT_ROUTER` to the Lambda `environment.variables` block (main.tf), running `km init --sidecars` rebuilds the binary and forces a cold-start but does NOT update the `environment` block via terragrunt. The Lambda still reads the old (absent) env var.
**How to avoid:** Use `make build-lambdas` (clean) + `km init --dry-run=false` for any Lambda env-block change. See MEMORY entries `project_km_init_lambdas_doesnt_deploy` and `project_km_init_skips_existing_lambda_zips`.
**Warning sign:** `KM_SLACK_DEFAULT_ROUTER` not visible in Lambda console environment tab after deploy.

### Pitfall 3: Goroutine-in-Lambda Frozen Context
**What goes wrong:** Posting the router reply in a goroutine (like the pause-hint does in step 9). When Handle returns 200, Lambda freezes the execution environment. The goroutine is mid-HTTP-call and its deadline elapses during the freeze. On the next thaw it resumes to find `context deadline exceeded`.
**How to avoid:** Post the router reply synchronously before returning, with a bounded context (`context.WithTimeout(ctx, 5*time.Second)`). The reply is a single `chat.postMessage` call — well within the Lambda 60s timeout.
**Warning sign:** Router reply never appears in Slack; `km-slack-bridge` logs show successful tally but no reply post.

### Pitfall 4: `wg.Wait()` Must Precede Return in `Broadcast`
This is already documented in relayer.go lines 84-86 (PITFALL 4 from Phase 95 RESEARCH.md). The Phase 96 change to `Broadcast`'s return type must preserve this guarantee: `wg.Wait()` before reading results, `wg.Wait()` before returning.

### Pitfall 5: `state` is a DDB Reserved Word
**What goes wrong:** `FilterExpression: "state = :running"` returns a validation error from DynamoDB.
**How to avoid:** Use `ExpressionAttributeNames: map[string]string{"#s": "state"}` and `FilterExpression: "attribute_exists(slack_channel_id) AND #s = :running"`.
**Warning sign:** `DDBRunningChannelLister.ListRunning` returns a `ValidationException` at runtime.

### Pitfall 6: Mixed-Fleet Ordering
**What goes wrong:** Deploying the front door with Phase 96 before deploying peers. Front door calls `Broadcast`, gets a parsed JSON response from a Phase-96 peer that says `claimed:false` + channels. Then the legacy Phase-95 front door tries to post a router reply because it doesn't know how to read the JSON. Actually the risk is the reverse: a Phase-96 front door receives a legacy `"ok"` from a Phase-95 peer and must treat it as `claimed:true`. The rollout-safety rule handles this conservatively.
**How to avoid:** The `claimed:true` conservative rule means order doesn't matter for correctness — only for completeness of the channels list. Deploy all installs before relying on cross-install channel lists in the reply. Document in deploy notes.

### Pitfall 7: `dynamodb:Scan` IAM Grant
**What goes wrong:** `DDBRunningChannelLister.ListRunning` gets an `AccessDeniedException` at runtime because the Lambda execution role lacks `dynamodb:Scan` on `km-sandboxes`.
**How to avoid:** Check `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` for the IAM policy statement covering `km-sandboxes`. If `dynamodb:Scan` is absent, add it in the same wave as the adapter code.

---

## Code Examples

### Scatter-Gather Result Type (events_interfaces.go addition)

```go
// Source: Phase 96 design spec + relayer.go POST pattern
type PeerClaimResult struct {
    PeerURL  string             // for logging
    Claimed  bool               // true = peer owns the channel (or legacy/error)
    Channels []SandboxChannelInfo // only populated when Claimed=false
}
```

### Legacy-Response Safety in `postToPeer` (relayer.go)

```go
// Source: Phase 96 design spec rollout-safety rule
// Replace io.Discard with:
bodyBytes, _ := io.ReadAll(resp.Body)
var result peerRelayResponse
if err := json.Unmarshal(bodyBytes, &result); err != nil {
    // Legacy "ok" or non-JSON body — treat as claimed=true (conservative)
    return PeerClaimResult{PeerURL: u, Claimed: true}, nil
}
return PeerClaimResult{PeerURL: u, Claimed: result.Claimed, Channels: result.Channels}, nil
```

### Nonces-as-Cooldown Adapter (main.go)

```go
// Source: analogous to nonceStoreAdapter at main.go lines 571-584
type routerCooldownAdapter struct {
    inner *bridge.DynamoNonceStore
}

func (r *routerCooldownAdapter) Reserve(ctx context.Context, channelID string, cooldownSeconds int) error {
    return r.inner.Reserve(ctx, "router-cooldown:"+channelID, cooldownSeconds)
}
```
Returns `nil` => first call => post the reply. Returns `bridge.ErrNonceReplayed` => cooldown active => suppress.

### Running Sandbox Channels Scan (aws_adapters.go)

```go
// Source: Phase 96 design; "state" reserved word pattern from pkg/aws/sandbox_dynamo.go
out, err := f.Client.Scan(ctx, &dynamodb.ScanInput{
    TableName:                 awssdk.String(f.TableName),
    FilterExpression:          awssdk.String("attribute_exists(slack_channel_id) AND #s = :running"),
    ExpressionAttributeNames:  map[string]string{"#s": "state"},
    ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
        ":running": &dynamodbtypes.AttributeValueMemberS{Value: "running"},
    },
    ProjectionExpression:      awssdk.String("slack_channel_id, alias, profile_name"),
    ExclusiveStartKey:         lastKey, // pagination loop
})
```

### Mention Check (events_handler.go orphan-reply gate)

```go
// Source: events_handler.go lines 267-270 (mention-only filter), reused in orphan gate
uid, fetchErr := h.BotUserID.Fetch(ctx)
if fetchErr != nil {
    h.log().Warn("events: router: bot_user_id fetch failed; skipping reply", "err", fetchErr)
    return EventsResponse{StatusCode: 200, Body: "ok"}
}
if uid == "" || !strings.Contains(msg.Text, "<@"+uid+">") {
    return EventsResponse{StatusCode: 200, Body: "ok"} // not a mention — silent
}
```

---

## State of the Art

| Old Approach | Current Approach | Changed | Impact |
|--------------|------------------|---------|--------|
| Phase 95: `Broadcast` returns `error`, ignores peer responses | Phase 96: `Broadcast` returns `[]PeerClaimResult`; peers return JSON | This phase | Enables orphan detection |
| Phase 95: relayed miss returns `"ok"` | Phase 96: relayed miss returns `{claimed:false, channels:[...]}` | This phase | Peers contribute channel lists |
| Per-channel cooldown stored on `km-sandboxes` row (pause-hint pattern) | Per-channel cooldown stored in nonces table with TTL | This phase | Supports channels with no km-sandboxes row |

**Deprecated/outdated:**
- Phase 95's `postToPeer` discarding the response body: replaced with JSON parse + legacy safety rule.

---

## Open Questions

1. **`dynamodb:Scan` on the bridge Lambda role**
   - What we know: `DDBRunningChannelLister` needs `dynamodb:Scan` on `km-sandboxes`; the existing bridge Lambda IAM policy covers `GetItem`, `PutItem`, `Query` on `km-sandboxes` (confirmed by FetchByChannel usage).
   - What's unclear: Whether `dynamodb:Scan` is already in the policy or needs to be added.
   - Recommendation: Planner should check `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` IAM resource block and add `dynamodb:Scan` if absent. This is a one-line terraform change.

2. **`PeerRelayer` interface change is a breaking change for existing test doubles**
   - What we know: `fakePeerRelayer` in `events_handler_test.go` (line 1389) implements `PeerRelayer`. Changing `Broadcast` return type requires updating this test double.
   - What's unclear: Whether any other code outside `pkg/slack/bridge/` implements `PeerRelayer`.
   - Recommendation: Search for other `PeerRelayer` implementations before changing the interface. Currently only `HTTPPeerRelayer` and `fakePeerRelayer` are known.

3. **Reply deduplication across the 2.5s window**
   - What we know: Slack may send duplicate events within the ack window (it retries if 3s elapses). The nonces-based event dedup (step 5 in Handle) guards against duplicate SQS enqueues for owned channels.
   - What's unclear: Whether the orphan reply path needs its own event dedup (beyond the router-cooldown). If Slack resends the same event within 3s, `env.EventID` dedup at line 280 fires before we reach the miss site — the second firing is deduped before the relay is invoked.
   - Recommendation: No additional event dedup is needed for the router. The existing event_id dedup at line 280 handles Slack's retry, and the cooldown handles intentional re-mentions.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing (`go test ./pkg/slack/bridge/...`) |
| Config file | none — Go stdlib test runner |
| Quick run command | `go test ./pkg/slack/bridge/... -run TestEventsHandler -count=1` |
| Full suite command | `go test ./pkg/slack/bridge/... -count=1 -race` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| SLACK-RTR-CFG | `slack.default_router` round-trips through config load + merge-list; absent => nil; drift WARN fires | unit | `go test ./internal/app/config/... -run TestSlack -count=1` | ✅ (config_test.go — add new cases) |
| SLACK-RTR-GATHER | Mixed peer responses tally correctly; legacy `"ok"` => claimed:true; HTTP error => claimed:true; 2.5s timeout honored | unit | `go test ./pkg/slack/bridge/... -run TestPeerRelayer -count=1` | ✅ (relayer_test.go — extend) |
| SLACK-RTR-ORPHAN | zero claims + miss => orphan; any claim => not orphan; relayed-miss returns JSON with claimed:false+channels | unit | `go test ./pkg/slack/bridge/... -run TestEventsHandler_DefaultRouter -count=1` | ❌ Wave 0 |
| SLACK-RTR-REPLY | orphan + mention + default_router:true => PostMessage called with correct threadTS + channel list; empty list => guidance-only text | unit | `go test ./pkg/slack/bridge/... -run TestEventsHandler_DefaultRouter -count=1` | ❌ Wave 0 |
| SLACK-RTR-COOLDOWN | second mention within cooldown window => no second reply; after TTL => replies again | unit | `go test ./pkg/slack/bridge/... -run TestEventsHandler_RouterCooldown -count=1` | ❌ Wave 0 |
| SLACK-RTR-SAFE | default_router=false/absent => silent (Phase 95 behavior byte-identical); non-mention in orphan channel => no reply; bot's own reply filtered by isBotLoop | unit | `go test ./pkg/slack/bridge/... -run TestEventsHandler_NilRelayer\|TestEventsHandler_DefaultRouter_Off -count=1` | ❌ Wave 0 (NilRelayer test ✅) |
| SLACK-RTR-E2E | Two installs + one Slack App: @-mention in unowned channel => one reply; repeat within cooldown => no second reply; owned channel => no reply | manual UAT | N/A | N/A |

### Sampling Rate
- **Per task commit:** `go test ./pkg/slack/bridge/... -count=1 -race`
- **Per wave merge:** `go test ./... -count=1 -race`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/slack/bridge/events_handler_test.go` — add `TestEventsHandler_DefaultRouter_*` table (orphan detection, mention gate, cooldown, reply formatting, default_router=false silent, claim short-circuit, legacy-peer safety)
- [ ] `pkg/slack/bridge/relayer_test.go` — add gather-tally tests: `TestPeerRelayer_GatherClaims`, `TestPeerRelayer_LegacyResponseClaimedTrue`, `TestPeerRelayer_HTTPErrorClaimedTrue`, `TestPeerRelayer_MixedResults`
- [ ] `pkg/slack/bridge/aws_adapters_test.go` or new `running_channels_test.go` — `DDBRunningChannelLister` pagination + filter tests
- [ ] `internal/app/config/config_test.go` — add `slack.default_router` round-trip + merge-list regression case

*(Existing test infrastructure covers all other phase requirements; only the above new test functions are needed.)*

---

## Sources

### Primary (HIGH confidence)

- `pkg/slack/bridge/relayer.go` — full file read; `Broadcast` signature (line 53), `postToPeer` body-discard (line 134), `wg.Wait()` contract (line 85)
- `pkg/slack/bridge/events_handler.go` — full file read; miss site (lines 202-233), mention scan (lines 266-275), `isBotLoop` (lines 469-495), `SlackPoster` field (line 57), `Relayer` field (lines 73-80)
- `pkg/slack/bridge/events_interfaces.go` — full file read; `PeerRelayer` interface (lines 17-19), `SlackPoster` embedded via `EventsHandler.Slack`
- `pkg/slack/bridge/events_types.go` — full file read; `EventsRequest`/`EventsResponse` shapes (lines 59-71)
- `pkg/slack/bridge/aws_adapters.go` — lines 259-380 (`SlackPosterAdapter`), lines 852-1026 (`DDBQueryGetPutAPI`, `DDBSandboxByChannel`), lines 1153-1275 (`DDBPauseHinter`)
- `cmd/km-slack-bridge/main.go` — full file read; Phase 95 relay wiring (lines 268-282), `eventsHandler.Slack` wiring (line 327), `WireMentionOnly` (lines 339-353)
- `internal/app/config/config.go` — lines 1-55 (`SlackConfig`), lines 410-417 (merge-list), lines 496-515 (population)
- `internal/app/cmd/init.go` — lines 875-913 (export block)
- `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` — full file read; Phase 95 `slack_peer_bridges` at line 116
- `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` — lines 106-136 (`slack_mention_only`, `slack_react_always`, `slack_peer_bridges`)
- `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` — lines 319-331 (Lambda env block)
- `pkg/slack/bridge/relayer_test.go` — full file read; `httptest.Server` pattern, `capturedRequest`, bounded-timeout test shape
- `pkg/aws/sandbox_dynamo.go` — lines 47-55 (DynamoDB attribute names: `state`, `alias`, `profile_name`, `slack_channel_id`)
- Design spec: `docs/superpowers/specs/2026-06-05-slack-default-router-design.md`
- Context: `.planning/phases/96-slack-default-router-orphan-channel-mention-reply/96-CONTEXT.md`
- Requirements: `.planning/REQUIREMENTS.md` Phase 96 section (SLACK-RTR-*)

### Secondary (MEDIUM confidence)

- `.planning/REQUIREMENTS.md` Phase 95 section (SLACK-FED-*) — confirms Phase 95 intent and current state
- `docs/superpowers/specs/2026-06-05-slack-federated-bridge-relay-design.md` — Phase 95 design spec

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all code read directly from AS-MERGED Phase 95 source
- Architecture: HIGH — all patterns derived from existing code with file:line citations
- Pitfalls: HIGH — config footgun from MEMORY, Lambda goroutine from Phase 75.2 UAT, DDB reserved word from Phase 94

**Research date:** 2026-06-05
**Valid until:** 2026-07-05 (stable Go codebase; no external API changes needed)
