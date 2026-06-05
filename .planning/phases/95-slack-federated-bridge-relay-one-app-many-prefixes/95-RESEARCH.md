# Phase 95: Slack Federated Bridge Relay — Research

**Researched:** 2026-06-05
**Domain:** Slack Events API bridge relay — Go Lambda, config plumbing, Terraform module, km doctor
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Static per-install list `slack.peer_bridges` in km-config.yaml (the OTHER installs' bridge `/events` URLs). NOT a shared registry table. Key named `peer_bridges` (avoid `eventBridges` — collides with AWS EventBridge).
- Each install keeps its own per-prefix SSM paths, unchanged. Operator pastes the SAME App's xoxb + signing secret into each install's normal `km slack init`. One App = same credential VALUES, stored per-install. No shared SSM.
- Single-hop broadcast on a local miss. `X-KM-Relayed: 1` header is the entire loop guard. A relayed request is TERMINAL: processed if owned, dropped (`slack_relay_no_owner`) otherwise — NEVER re-relayed. Loops structurally impossible.
- Decision table (after signature verify, at the FetchByChannel site):
  | X-KM-Relayed? | Owns channel? | Action |
  |---|---|---|
  | absent | yes | process locally (today's path) |
  | absent | no  | broadcast raw event to all peer_bridges, return 200 |
  | present | yes | process locally |
  | present | no  | drop (log slack_relay_no_owner), return 200 |
- Forward verbatim body + `X-Slack-Signature` + `X-Slack-Request-Timestamp` + `X-KM-Relayed: 1`, HTTP POST to each peer `/events`.
- Parallel, bounded context (~2.5s), SYNCHRONOUS before returning 200. Peer count tiny so 3s Slack ack window holds. Failing peer is logged, non-fatal.
- Plumbing mirrors `slack.mention_only` EXACTLY.

### Claude's Discretion
- `km slack peers` convenience command (nice-to-have; include only if it fits a plan cheaply, else defer).
- Reachability HTTP probe of peer `/events` in `km doctor` (optional).

### Deferred Ideas (OUT OF SCOPE)
- Async `lambda:Invoke` transport (only if peer count outgrows synchronous broadcast).
- `km slack peers` convenience command (nice-to-have; include only if it fits a plan cheaply, else defer).
- Reachability HTTP probe of peer `/events` in `km doctor` (optional).
- Cross-account/region federation.
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| SLACK-FED-CFG | `slack.peer_bridges []string` in km-config.yaml: `SlackConfig.PeerBridges` field, merge-list entry, tri-state population from `v.GetStringSlice`; absent => nil => federation off | Config plumbing pattern documented with file:line — mirrors MentionOnly/ReactAlways |
| SLACK-FED-PLUMB | `init.go` exports `KM_SLACK_PEER_BRIDGES` (comma-joined) when set, with env-wins drift WARN; terragrunt.hcl `get_env` → TF `slack_peer_bridges` var → Lambda `KM_SLACK_PEER_BRIDGES` env; bridge parses env into peer URL slice | All four plumbing layers documented with file:line |
| SLACK-FED-RELAY | `PeerRelayer`/`HTTPPeerRelayer` broadcasts verbatim body + headers + `X-KM-Relayed:1` to all peers in parallel, bounded context (~2.5s), synchronous before 200; failing peer logged non-fatal; injected nil-safe into `EventsHandler` | HTTP client pattern from initHTTPClient confirmed; interface/field injection pattern documented |
| SLACK-FED-LOOP | Decision table at the `FetchByChannel` miss site (events_handler.go:189); relayed request is terminal; loop structurally impossible | Injection point confirmed at line 189; X-KM-Relayed read after verifySlackSignature at line 154 |
| SLACK-FED-VERIFY | Relayed request passes peer's `verifySlackSignature` with shared signing secret (forwarded body+timestamp unchanged, ±5-min window) | verifySlackSignature at events_handler.go:460; HMAC-SHA256, ±300s window confirmed |
| SLACK-FED-DOCTOR | `km doctor` WARNs on malformed peer URL, self-loop, empty peer_bridges on front-door install | doctor_slack.go pattern documented; check function structure confirmed |
| SLACK-FED-E2E | Two installs; message in install B's channel delivered to A's bridge is relayed to and processed by B | Architecture is correct by construction; E2E is manual UAT |
</phase_requirements>

---

## Summary

Phase 95 adds opt-in federated relay to the existing `km-slack-bridge` Lambda. The entire
implementation is a **brownfield extension** of a proven pattern: `slack.mention_only` /
`slack.react_always` end-to-end. Every touchpoint in the plumbing is well-understood and the
planner's primary job is to copy the pattern precisely.

The only genuinely new code is `pkg/slack/bridge/relayer.go` (a new file, ~100 lines), a nil-safe
`Relayer PeerRelayer` field on `EventsHandler`, and the four-row decision table added to the
`Handle` method's `FetchByChannel` miss branch. Config plumbing and Terraform module changes are
mechanical copies of the `slack.mention_only` / `slack.react_always` edits.

**Primary recommendation:** Copy `slack.mention_only` end-to-end, with two differences:
(1) use `v.GetStringSlice` + `strings.Join(urls, ",")` instead of `strconv.FormatBool`; and
(2) add the decision table at `events_handler.go:189` rather than at the top of `Handle`.

---

## Standard Stack

### Core (all already in the repo — no new dependencies)

| Library | Purpose | Location |
|---------|---------|----------|
| `net/http` | Outbound HTTP POST to peer bridges | Already imported in `cmd/km-slack-bridge/main.go` via `initHTTPClient` |
| `context.WithTimeout` | Bound the parallel broadcast (~2.5s) | Standard library — pattern at `events_handler.go:403` (`10*time.Second` reactor) |
| `strings.Join` / `strings.Split` | Comma-join peer URLs for env var; split at cold-start | Standard library — `strings.Join` in `internal/app/cmd/init.go:2407` |
| `strconv` | Bool formatting for `MentionOnly`/`ReactAlways` drift WARN | `internal/app/cmd/init.go:22` imports it; PeerBridges uses `strings.Join`, not strconv |

### No new external dependencies

The `HTTPPeerRelayer` uses `initHTTPClient` (the existing `*http.Client` built in `init()`).
No new Go modules.

---

## Architecture Patterns

### 1. Config Plumbing Pattern (the template to copy)

**File:** `internal/app/config/config.go`

**SlackConfig struct** (lines 24-44):
```go
type SlackConfig struct {
    MentionOnly *bool `mapstructure:"mention_only" yaml:"mention_only,omitempty"`
    ReactAlways *bool `mapstructure:"react_always" yaml:"react_always,omitempty"`
    // ADD:
    PeerBridges []string `mapstructure:"peer_bridges" yaml:"peer_bridges,omitempty"`
}
```

**v2→v merge-list** (lines 398-401) — the CRITICAL footgun:
```go
// Phase 91.1: nested key for the polite-bot install-level default.
"slack.mention_only",
// Phase 91.4: nested key for the first-only reactor install-level default.
"slack.react_always",
// ADD Phase 95:
"slack.peer_bridges",
```
If this line is missing, `v2.Get("slack.peer_bridges")` is never merged into `v`, and the field
stays nil regardless of what is in km-config.yaml. This is the known footgun documented in
`project_config_key_merge_list`.

**Population block** (lines 476-491) — currently ends with `ReactAlways`. ADD after it:
```go
// Phase 95: slack.peer_bridges is a []string. Only populated when explicitly set —
// absent yaml key => nil slice => federation off (EventsHandler.Relayer stays nil).
if v.IsSet("slack.peer_bridges") {
    cfg.Slack.PeerBridges = v.GetStringSlice("slack.peer_bridges")
}
```

Note: `[]string` does NOT use the `*bool` tri-state pattern. Use `v.GetStringSlice` directly; nil
slice (`cfg.Slack.PeerBridges == nil`) is the "federation off" sentinel. No pointer needed.

---

### 2. init.go Env Export Pattern

**File:** `internal/app/cmd/init.go`

**ExportTerragruntEnvVars function**, lines 869-897 (the MentionOnly + ReactAlways blocks).
The `PeerBridges` block follows the same structure but uses `strings.Join` instead of
`strconv.FormatBool`:

```go
// Phase 95: KM_SLACK_PEER_BRIDGES — comma-joined list of sibling bridge /events URLs.
// Consumed by infra/live/use1/lambda-slack-bridge/terragrunt.hcl
// get_env("KM_SLACK_PEER_BRIDGES", ""). Only export when the operator has explicitly
// set slack.peer_bridges in km-config.yaml. Empty list => omit => terragrunt default ""
// applies => federation off.
if len(cfg.Slack.PeerBridges) > 0 {
    yamlPeerBridges := strings.Join(cfg.Slack.PeerBridges, ",")
    if envVal := os.Getenv("KM_SLACK_PEER_BRIDGES"); envVal != "" && envVal != yamlPeerBridges {
        fmt.Fprintf(os.Stderr, "WARN: KM_SLACK_PEER_BRIDGES=%s (env) overrides km-config.yaml slack.peer_bridges=%s\n", envVal, yamlPeerBridges)
    } else if envVal == "" {
        os.Setenv("KM_SLACK_PEER_BRIDGES", yamlPeerBridges) //nolint:errcheck
    }
}
```

Key differences from MentionOnly:
- Gate is `len(cfg.Slack.PeerBridges) > 0` (not `!= nil`) — empty explicitly-set slice also skips.
- Value is `strings.Join(cfg.Slack.PeerBridges, ",")` — no `strconv.FormatBool`.
- Drift comparison is a direct string-vs-string on the joined form.

---

### 3. Terragrunt + TF Module Pattern

**File:** `infra/live/use1/lambda-slack-bridge/terragrunt.hcl`

Current Slack block (lines 94-109):
```hcl
slack_mention_only = get_env("KM_SLACK_MENTION_ONLY", "false")
slack_bot_user_id  = get_env("KM_SLACK_BOT_USER_ID", "")
slack_react_always = get_env("KM_SLACK_REACT_ALWAYS", "true")
```

Add after line 109 (before the `tags` block):
```hcl
# Phase 95 — federated relay peer list. Comma-joined /events URLs of sibling installs.
# Empty/absent => no relay (federation off). Requires km init --dry-run=false to deploy.
slack_peer_bridges = get_env("KM_SLACK_PEER_BRIDGES", "")
```

**File:** `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf`

After the `slack_react_always` variable (around line 130):
```hcl
# Phase 95: federated relay peer URLs (comma-joined). Empty => federation off.
variable "slack_peer_bridges" {
  description = "Comma-joined /events URLs of sibling km installs for federated relay (Phase 95). Empty string = federation off."
  type        = string
  default     = ""
}
```

**File:** `infra/modules/lambda-slack-bridge/v1.0.0/main.tf`

Inside the `environment { variables = { ... } }` block (currently ends at line 329 with
`KM_SLACK_REACT_ALWAYS`). Add:
```hcl
# Phase 95 — federated relay peer list
KM_SLACK_PEER_BRIDGES = var.slack_peer_bridges
```

---

### 4. Bridge Cold-Start Env Read and PeerRelayer Injection

**File:** `cmd/km-slack-bridge/main.go`

The existing `wireEventsHandler()` function (starting at line 204) builds and assigns all
`eventsHandler` fields. The `WireMentionOnly` function (line 310) is the model for a new peer
relayer wiring function.

Add after the existing `WireMentionOnly` call (line 262):

```go
// Phase 95: federated relay. Parse KM_SLACK_PEER_BRIDGES; build HTTPPeerRelayer.
// nil Relayer => federation off => byte-identical to today's handle path.
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
            HTTPClient: initHTTPClient, // reuse existing shared client
        }
        slog.Info("km-slack-bridge: federated relay enabled", "peer_count", len(peers))
    }
}
```

`initHTTPClient` is already the package-level `*http.Client` built in `init()` (line 106-111) with
a 10-second timeout and `CheckRedirect: ErrUseLastResponse`. For relay POSTs, the timeout is
overridden by the `context.WithTimeout(~2.5s)` passed into `Broadcast`.

---

### 5. EventsHandler Relay Field and Decision Table

**File:** `pkg/slack/bridge/events_handler.go`

Add `Relayer PeerRelayer` field to `EventsHandler` struct (after `Reactor Reactor` ~line 41):
```go
// Phase 95: federated relay. When non-nil and FetchByChannel returns empty (unknown
// channel), Broadcast is called with the verbatim request body and Slack headers.
// nil => federation off => unknown-channel path returns 200 as today.
Relayer PeerRelayer
```

**Injection point** at the `FetchByChannel` miss, currently lines 189-197:
```go
info, err := h.Sandboxes.FetchByChannel(ctx, msg.Channel)
if err != nil {
    h.log().Error("events: channel lookup", "err", err, "channel", msg.Channel)
    return EventsResponse{StatusCode: 200, Body: "ok"}
}
if info.SandboxID == "" || info.QueueURL == "" {
    h.log().Warn("events: unknown channel or inbound disabled", "channel", msg.Channel)
    return EventsResponse{StatusCode: 200, Body: "ok"}  // ← REPLACE THIS RETURN
}
```

Replace the unknown-channel `return` with the four-row decision table. The relay-marker header
is already available in `req.Headers` (passed into `Handle` as `EventsRequest.Headers` — keys
lowercased by the adapter in main.go `lowercaseHeaders`). Read it before `FetchByChannel` so the
`present+no` drop path doesn't even need to call `FetchByChannel` — but AFTER `verifySlackSignature`
(line 154) so the loop guard is authenticated.

Full decision table implementation (replaces the two lines from 194-197):
```go
if info.SandboxID == "" || info.QueueURL == "" {
    // Phase 95: broadcast-on-miss or drop-on-relay.
    if req.Headers["x-km-relayed"] != "" {
        // TERMINAL: relayed request + no owner => drop, never re-relay.
        h.log().Warn("events: relay miss — no owner for relayed message",
            "channel", msg.Channel, "event", "slack_relay_no_owner")
        return EventsResponse{StatusCode: 200, Body: "ok"}
    }
    if h.Relayer != nil {
        // Broadcast raw event to all peer bridges (synchronous, bounded 2.5s).
        if err := h.Relayer.Broadcast(ctx, req.Body, req.Headers); err != nil {
            h.log().Warn("events: relay broadcast partial failure", "err", err,
                "channel", msg.Channel)
        }
    } else {
        h.log().Warn("events: unknown channel or inbound disabled", "channel", msg.Channel)
    }
    return EventsResponse{StatusCode: 200, Body: "ok"}
}
// present + yes: process locally (fall through — today's path unchanged).
```

The `present+yes` case (relayed request, but this install owns the channel) falls through
automatically — `req.Headers["x-km-relayed"]` is checked only on a miss.

---

### 6. PeerRelayer Interface and HTTPPeerRelayer

**New file:** `pkg/slack/bridge/relayer.go`

**Interface declaration** belongs in `events_interfaces.go` (the file that already declares all
other bridge interfaces — `SQSSender`, `Reactor`, etc.):
```go
// PeerRelayer broadcasts a raw Slack Events API request to sibling km-install bridges.
// Used by EventsHandler when FetchByChannel finds no local owner (Phase 95).
// Implementations MUST:
//   - Forward the verbatim body unchanged (Slack HMAC covers body+timestamp).
//   - Include X-Slack-Signature, X-Slack-Request-Timestamp from slackHeaders.
//   - Add X-KM-Relayed: 1 to the forwarded request.
//   - POST to all peers in parallel, bounded by a ~2.5s context.
//   - Return nil when ALL peers succeed, or an error summarizing failures.
//   - Always return promptly (the caller returns 200 regardless).
type PeerRelayer interface {
    Broadcast(ctx context.Context, rawBody string, slackHeaders map[string]string) error
}
```

**HTTPPeerRelayer** in `relayer.go`:
```go
type HTTPPeerRelayer struct {
    PeerURLs   []string
    HTTPClient *http.Client
}
```

`Broadcast` must:
1. Create a `context.WithTimeout(ctx, 2500*time.Millisecond)` child context.
2. Launch one goroutine per peer URL.
3. Each goroutine: `http.NewRequestWithContext(broadcastCtx, "POST", peerURL, bytes.NewReader([]byte(rawBody)))`, then set headers:
   - `Content-Type: application/json` (Slack sends JSON)
   - `X-Slack-Signature: slackHeaders["x-slack-signature"]`
   - `X-Slack-Request-Timestamp: slackHeaders["x-slack-request-timestamp"]`
   - `X-KM-Relayed: 1`
4. Execute the POST; read and discard the response body; log non-2xx as a warning.
5. Use a `sync.WaitGroup` (or collect errors into a channel) to wait for all goroutines before returning. Must wait before returning so the 2.5s bound is honoured synchronously.
6. Aggregate per-peer errors; return a combined error if any failed (caller logs WARN, returns 200 regardless).

**HTTP client note:** Use `initHTTPClient` (already a 10s global `*http.Client` with custom
`CheckRedirect`). The per-broadcast context timeout of 2.5s overrides the client-level 10s timeout
at the request level. No new `http.Client` needed.

---

### 7. km doctor Check Pattern

**File:** `internal/app/cmd/doctor_slack.go` (where `checkSlackTokenValidity`,
`checkSlackInboundQueueExists`, etc. live — confirmed by grep)

**File:** `internal/app/cmd/doctor_slack_transcript.go` (`checkSlackBotUserIDCached`, lines 393-427
— the cleanest recent example with nil-guard skip + WARN shape)

New function `checkSlackPeerBridges` follows the `checkSlackBotUserIDCached` pattern:
- Accept `cfg DoctorConfigProvider` (or the raw `[]string` from `cfg.Slack.PeerBridges`).
- Return `CheckSkipped` when `len(peerBridges) == 0` (federation not configured).
- Return `CheckWarn` for each of: malformed URL (`url.Parse` error), self-loop (URL == own bridge URL from SSM `{ssmPrefix}slack/bridge-url`), or empty list on an install that appears to be the Slack Request-URL host.
- Return `CheckOK` when all URLs are syntactically valid and none self-loop.

The doctor check is added to `doctor.go` (where Slack checks are gated on `slackSSMStore != nil`
around line 3063) following the same anonymous-closure pattern used by all other checks:
```go
checks = append(checks, func(ctx context.Context) CheckResult {
    r := checkSlackPeerBridges(cfg.Slack.PeerBridges, bridgeURL)
    if r.Status == CheckError {
        r.Status = CheckWarn
    }
    return r
})
```

The `bridgeURL` can be read from SSM `{ssmPrefix}slack/bridge-url` (already fetched by
`checkSlackTokenValidity`); pass it in as a parameter to avoid a second SSM call.

---

### 8. EventsRequest and EventsResponse Struct Shape

**File:** `pkg/slack/bridge/events_types.go` (lines 61-71)

```go
type EventsRequest struct {
    Headers map[string]string // adapter MUST lowercase keys
    Body    string
}

type EventsResponse struct {
    StatusCode int
    Body       string
    Headers    map[string]string
}
```

`Headers` is already `map[string]string` with lowercased keys (enforced by `lowercaseHeaders` in
`main.go`). `X-KM-Relayed: 1` is read as `req.Headers["x-km-relayed"]` — no struct changes needed.

---

### 9. Test-Mock Pattern in events_handler_test.go

The existing mock pattern (lines 19-57) shows all fakes implement the interface directly as
`*fakeXxx` structs with a single method. For `PeerRelayer`:

```go
type fakePeerRelayer struct {
    mu      sync.Mutex
    calls   []fakeRelayCall
    err     error // if non-nil, Broadcast returns this
}
type fakeRelayCall struct {
    body    string
    headers map[string]string
}
func (f *fakePeerRelayer) Broadcast(ctx context.Context, rawBody string, h map[string]string) error {
    f.mu.Lock()
    f.calls = append(f.calls, fakeRelayCall{rawBody, h})
    f.mu.Unlock()
    return f.err
}
```

Table-driven test helper (see `TestEventsHandler_MentionOnly` at line 993 for the existing
decision-table test shape — it is exactly the pattern to copy for the four-row relay decision
table).

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead |
|---------|-------------|-------------|
| HTTP client for relay POST | A new `http.Client` | `initHTTPClient` (already configured with CheckRedirect + 10s timeout) |
| Config merge | Manual yaml parsing | Viper's `v.GetStringSlice("slack.peer_bridges")` after the merge-list entry |
| Header lowercasing | Manual string manipulation | Already done by `lowercaseHeaders()` in `main.go` |
| Parallel broadcast | `sync.Mutex` + serial loop | `sync.WaitGroup` + goroutine per peer, bounded by context.WithTimeout(2.5s) |
| Loop guard logic | Complex state machine | Single header check `req.Headers["x-km-relayed"] != ""` — three characters |

---

## Common Pitfalls

### Pitfall 1: Missing merge-list entry (the #1 footgun)
**What goes wrong:** `slack.peer_bridges` in km-config.yaml is silently ignored. `cfg.Slack.PeerBridges` stays nil even when the key is present in the file.
**Why it happens:** The v2→v merge loop (config.go lines 364-408) only copies keys explicitly listed. Adding the struct field and population block without adding `"slack.peer_bridges"` to the list is a complete no-op.
**How to avoid:** Add `"slack.peer_bridges"` at line 401 (after `"slack.react_always"`). The `TestLoadSlackMentionOnly_True` test structure (config_test.go:731) is the template for the regression test that catches this.
**Warning signs:** Config test for `PeerBridges` with yaml set passes nil check — the merge-list entry is missing.

### Pitfall 2: `km init --sidecars` does NOT deploy Lambda env changes
**What goes wrong:** Operator runs `km init --sidecars` after setting `slack.peer_bridges` in km-config.yaml. The bridge binary is rebuilt and cold-started, but `KM_SLACK_PEER_BRIDGES` is NOT in the Lambda environment. Federation appears silently broken.
**Why it happens:** `--sidecars` rebuilds binaries and forces a cold-start but does NOT update the Lambda `environment.variables` Terraform block. The env block requires a full `terragrunt apply`.
**How to avoid:** Deploy with `make build-lambdas && km init --dry-run=false`. Document in phase UAT checklist.

### Pitfall 3: Stale Lambda zip skipping
**What goes wrong:** `km init` deploys the old bridge binary without the new relay code.
**Why it happens:** `buildLambdaZips` skips any `build/*.zip` that already exists. Running `km init` without first cleaning the build cache reuses the stale zip.
**How to avoid:** Always `make build-lambdas` (which cleans) before `km init --dry-run=false`.

### Pitfall 4: Broadcast must be SYNCHRONOUS before returning 200
**What goes wrong:** Relay goroutines are spawned fire-and-forget; handler returns 200 immediately. Lambda freezes the execution environment — in-flight goroutines have their context cancelled and the relay never completes.
**Why it happens:** The comment at `events_handler.go:105-110` explains this precisely: "AWS Lambda freezes the runtime when Handle returns. A goroutine still mid-retry has its wall-clock context elapse during the freeze."
**How to avoid:** `Broadcast` uses `sync.WaitGroup.Wait()` to collect all goroutines BEFORE returning. Use the 2.5s context to bound the wait, not a fire-and-forget goroutine.

### Pitfall 5: PauseHinter IS a goroutine (do not copy this to relay)
**What goes wrong:** Developer notices `PauseHinter` uses `go func()` (events_handler.go:356) and copies that pattern for the relay broadcast.
**Why it happens:** PauseHinter is explicitly fire-and-forget (it posts a hint message; losing one hint is acceptable). The relay is NOT acceptable to lose (the Slack message would be dropped with no processing).
**How to avoid:** Relay broadcast is synchronous. PauseHinter goroutine is an explicit one-off exception documented in the code comments.

### Pitfall 6: Self in peer_bridges causes wasted relay hop (not a loop)
**What goes wrong:** An operator accidentally lists their own bridge URL in `slack.peer_bridges`. A self-relayed event arrives with `X-KM-Relayed: 1` and if owned, is processed locally. If not owned, it is dropped. No infinite loop — but the doctor check should WARN so operators can fix the config.
**Why it happens:** `X-KM-Relayed: 1` is the terminal signal — even a self-loop arrives as a relayed request and is handled by the drop-or-process logic, not by re-broadcasting.
**How to avoid:** `km doctor` WARN when any peer URL matches the install's own bridge URL (from SSM `{ssmPrefix}slack/bridge-url`).

### Pitfall 7: url_verification must NOT be relayed
**What goes wrong:** `url_verification` events (sent by Slack once during App setup) are relayed to peer bridges, which respond with their own `challenge` echo — confusing the operator during setup.
**Why it happens:** The relay broadcast site is after `verifySlackSignature` but the `url_verification` short-circuit is BEFORE `verifySlackSignature` (events_handler.go:135). So `url_verification` never reaches the relay injection point. This is structural — no code change needed, but document it so the planner does not add a special case.
**Warning signs:** None required — the existing `url_verification` short-circuit at line 135 already returns before the relay site.

---

## Code Examples

### Verified relay header read and decision table

```go
// Source: events_handler.go — the FetchByChannel miss site (currently lines 194-197)
// Replace the current early return with:
if info.SandboxID == "" || info.QueueURL == "" {
    if req.Headers["x-km-relayed"] != "" {
        // TERMINAL drop — relayed + no owner
        h.log().Warn("events: relay miss — no owner",
            "channel", msg.Channel, "event", "slack_relay_no_owner")
        return EventsResponse{StatusCode: 200, Body: "ok"}
    }
    if h.Relayer != nil {
        if err := h.Relayer.Broadcast(ctx, req.Body, req.Headers); err != nil {
            h.log().Warn("events: relay broadcast partial failure",
                "err", err, "channel", msg.Channel)
        }
    } else {
        h.log().Warn("events: unknown channel or inbound disabled",
            "channel", msg.Channel)
    }
    return EventsResponse{StatusCode: 200, Body: "ok"}
}
// present+yes falls through to today's processing path
```

### Verified verifySlackSignature (called by relayed requests too)

```go
// Source: events_handler.go:460 — unchanged; relayed request passes this with
// forwarded body + X-Slack-Request-Timestamp + X-Slack-Signature headers.
func verifySlackSignature(signingSecret, tsHeader, rawBody, sigHeader string, now time.Time) error {
    // ±300s skew window; HMAC-SHA256; v0= prefix check
    // ...
}
```

### Verified config merge-list and population (Phase 91.1 template)

```go
// Source: internal/app/config/config.go lines 398-401 (merge-list) + 476-483 (population)

// merge-list entry (add after "slack.react_always"):
"slack.peer_bridges",

// population (add after ReactAlways block at line 491):
if v.IsSet("slack.peer_bridges") {
    cfg.Slack.PeerBridges = v.GetStringSlice("slack.peer_bridges")
}
```

### Verified init.go env export (Phase 91.1 template for *bool; adapt for []string)

```go
// Source: internal/app/cmd/init.go lines 875-882 (MentionOnly block — the template)
// Adaptation for PeerBridges ([]string, comma-joined):
if len(cfg.Slack.PeerBridges) > 0 {
    yamlPeerBridges := strings.Join(cfg.Slack.PeerBridges, ",")
    if envVal := os.Getenv("KM_SLACK_PEER_BRIDGES"); envVal != "" && envVal != yamlPeerBridges {
        fmt.Fprintf(os.Stderr, "WARN: KM_SLACK_PEER_BRIDGES=%s (env) overrides km-config.yaml slack.peer_bridges=%s\n",
            envVal, yamlPeerBridges)
    } else if envVal == "" {
        os.Setenv("KM_SLACK_PEER_BRIDGES", yamlPeerBridges) //nolint:errcheck
    }
}
```

### Verified config_test.go test shape (for PeerBridges test)

```go
// Source: internal/app/config/config_test.go:731 — TestLoadSlackMentionOnly_True template
func TestLoadSlackPeerBridges_Set(t *testing.T) {
    dir := t.TempDir()
    writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
slack:
    peer_bridges:
      - https://abc123.lambda-url.us-east-1.on.aws/events
      - https://def456.lambda-url.us-east-1.on.aws/events
`)
    // chdir to dir; cfg.Slack.PeerBridges should be len==2
}

func TestLoadSlackPeerBridges_Absent(t *testing.T) {
    // yaml omits the slack block; cfg.Slack.PeerBridges should be nil
}
```

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) — `go test ./...` |
| Config file | none (table-driven, standard Go test files) |
| Quick run command | `go test ./internal/app/config/... ./pkg/slack/bridge/... ./internal/app/cmd/... -run TestLoad\|TestEvents -v` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| SLACK-FED-CFG | `slack.peer_bridges` round-trips through merge + population; absent => nil; set => populated slice | unit | `go test ./internal/app/config/... -run TestLoadSlackPeerBridges -v` | Wave 0 gap |
| SLACK-FED-CFG | Env-wins drift WARN fires when `KM_SLACK_PEER_BRIDGES` env differs from yaml | unit | `go test ./internal/app/config/... -run TestSlackPeerBridgesDriftWarn -v` | Wave 0 gap |
| SLACK-FED-PLUMB (config half) | `ExportTerragruntEnvVars` sets `KM_SLACK_PEER_BRIDGES` when PeerBridges non-empty | unit | included in config test or init_test | Wave 0 gap |
| SLACK-FED-RELAY | `HTTPPeerRelayer.Broadcast` builds correct headers (sig, ts, relay marker); POSTs to all peers in parallel; honours bounded context timeout; logs + tolerates failing peer | unit | `go test ./pkg/slack/bridge/... -run TestPeerRelayer -v` | Wave 0 gap — `relayer_test.go` is a new file |
| SLACK-FED-LOOP | `{relayed?, localHit?}` four-row decision table: process, broadcast, drop, process | unit | `go test ./pkg/slack/bridge/... -run TestEventsHandler_FederatedRelay -v` | Wave 0 gap — add to `events_handler_test.go` |
| SLACK-FED-VERIFY | Relayed request with forwarded headers passes `verifySlackSignature` with shared secret | unit | `go test ./pkg/slack/bridge/... -run TestVerifySlackSignature_Relayed -v` | Wave 0 gap — small test in existing or new file |
| SLACK-FED-DOCTOR | `checkSlackPeerBridges` returns WARN on malformed URL, self-loop, and empty-on-front-door | unit | `go test ./internal/app/cmd/... -run TestCheckSlackPeerBridges -v` | Wave 0 gap — add to `doctor_slack.go` or `doctor_slack_test.go` |
| SLACK-FED-E2E | Two installs; relay cross-install | manual UAT | — | Manual only — needs real AWS+Slack |

### Sampling Rate

- **Per task commit:** `go test ./internal/app/config/... ./pkg/slack/bridge/... -run TestLoadSlackPeerBridges\|TestPeerRelayer\|TestEventsHandler_Federated -v`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `internal/app/config/config_test.go` — `TestLoadSlackPeerBridges_Set`, `_Absent`, `_EnvWinsDriftWarn` (same file as existing MentionOnly tests)
- [ ] `pkg/slack/bridge/relayer_test.go` — new file; covers `HTTPPeerRelayer.Broadcast` headers, parallel, timeout, failing-peer tolerance
- [ ] `pkg/slack/bridge/events_handler_test.go` — four table-driven rows for the federated decision table + nil-Relayer path (add to existing test file)
- [ ] `internal/app/cmd/doctor_slack.go` or `doctor_slack_test.go` — `TestCheckSlackPeerBridges` covering malformed-URL, self-loop, empty-on-host

---

## Files Touched (Implementation Map)

| File | Change | Confidence |
|------|--------|------------|
| `internal/app/config/config.go` | `SlackConfig.PeerBridges []string`; merge-list `"slack.peer_bridges"`; `v.GetStringSlice` population | HIGH — verified against MentionOnly pattern |
| `internal/app/config/config_test.go` | Three new tests: set, absent, drift-warn | HIGH |
| `internal/app/cmd/init.go` | `KM_SLACK_PEER_BRIDGES` export with comma-join + drift WARN (after ReactAlways block ~line 895) | HIGH |
| `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` | `slack_peer_bridges = get_env("KM_SLACK_PEER_BRIDGES", "")` after line 109 | HIGH |
| `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` | `variable "slack_peer_bridges"` after existing Phase 91 vars (after line 130) | HIGH |
| `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` | `KM_SLACK_PEER_BRIDGES = var.slack_peer_bridges` in Lambda env block after line 329 | HIGH |
| `cmd/km-slack-bridge/main.go` | Parse `KM_SLACK_PEER_BRIDGES`; build `HTTPPeerRelayer`; inject `eventsHandler.Relayer` in `wireEventsHandler` after `WireMentionOnly` call | HIGH |
| `pkg/slack/bridge/relayer.go` (new) | `HTTPPeerRelayer` struct + `Broadcast` implementation | HIGH |
| `pkg/slack/bridge/relayer_test.go` (new) | Unit tests for `HTTPPeerRelayer` | HIGH |
| `pkg/slack/bridge/events_interfaces.go` | `PeerRelayer` interface declaration | HIGH |
| `pkg/slack/bridge/events_handler.go` | `Relayer PeerRelayer` field on `EventsHandler`; four-row decision table at lines 194-197 | HIGH |
| `pkg/slack/bridge/events_handler_test.go` | Four-row table-driven test + nil-relayer test | HIGH |
| `internal/app/cmd/doctor_slack.go` | `checkSlackPeerBridges` function | HIGH |
| `internal/app/cmd/doctor.go` | Wire `checkSlackPeerBridges` into Slack checks block (~line 3175) | HIGH |
| `docs/slack-notifications.md` | New § Phase 95 federated relay | HIGH |
| `CLAUDE.md` | Phase 95 note | HIGH |

---

## State of the Art

| Old Approach | Current Approach | Impact |
|--------------|------------------|--------|
| One Slack App per km install (one SQS endpoint per App) | One App, N installs share it via static relay list | Operators with multiple installs no longer need N separate Slack App registrations |
| Unknown-channel events silently dropped | Unknown-channel events broadcast to sibling bridges | Cross-install per-sandbox channels route correctly |

**No deprecated patterns involved.** This is additive only.

---

## Open Questions

1. **Own-bridge URL detection in km doctor**
   - What we know: The own bridge URL is stored in SSM at `{ssmPrefix}slack/bridge-url` and is already fetched by `checkSlackTokenValidity` (doctor_slack.go:81).
   - What's unclear: Whether to pass the already-fetched URL into `checkSlackPeerBridges` or re-fetch. Re-fetch is safe (same SSM cache). Passing it avoids a second fetch.
   - Recommendation: Accept `bridgeURL string` as a parameter; the caller passes the already-fetched value or empty string.

2. **`km slack peers` convenience command**
   - What we know: Listed as "deferred" in CONTEXT.md.
   - What's unclear: Whether it fits cheaply into a Wave 1 plan or requires its own wave.
   - Recommendation: Assess during planning Wave 1. If it is a simple `fmt.Println(cfg.Slack.PeerBridges)` wrapper, include. Otherwise defer.

---

## Sources

### Primary (HIGH confidence)
All findings are verified against the actual source files in `/Users/khundeck/working/klankrmkr`.

- `internal/app/config/config.go:24-44` — `SlackConfig` struct (MentionOnly, ReactAlways fields)
- `internal/app/config/config.go:364-408` — v2→v merge-list
- `internal/app/config/config.go:476-491` — tri-state population block
- `internal/app/config/config.go:428-466` — cfg struct construction (GetStringSlice for []string fields)
- `internal/app/config/config_test.go:731-860` — MentionOnly and ReactAlways test shape
- `internal/app/cmd/init.go:869-897` — `ExportTerragruntEnvVars` MentionOnly + ReactAlways blocks
- `infra/live/use1/lambda-slack-bridge/terragrunt.hcl:94-109` — `get_env` pattern for Slack vars
- `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf:106-130` — Phase 91 variable declarations
- `infra/modules/lambda-slack-bridge/v1.0.0/main.tf:307-330` — Lambda environment block
- `cmd/km-slack-bridge/main.go:72-333` — cold-start init, wireEventsHandler, WireMentionOnly
- `pkg/slack/bridge/events_handler.go:27-72` — EventsHandler struct fields
- `pkg/slack/bridge/events_handler.go:125-413` — Handle method (all steps)
- `pkg/slack/bridge/events_handler.go:189-197` — FetchByChannel miss site (relay injection point)
- `pkg/slack/bridge/events_handler.go:154` — verifySlackSignature call site
- `pkg/slack/bridge/events_handler.go:460-486` — verifySlackSignature implementation
- `pkg/slack/bridge/events_interfaces.go:1-101` — all interface declarations
- `pkg/slack/bridge/events_types.go:61-71` — EventsRequest / EventsResponse structs
- `pkg/slack/bridge/events_handler_test.go:252+` — test structure and mock pattern
- `pkg/slack/bridge/aws_adapters.go:292-333` — SlackPosterAdapter HTTP POST pattern
- `internal/app/cmd/doctor_slack_transcript.go:393-427` — checkSlackBotUserIDCached (doctor check shape)
- `internal/app/cmd/doctor_slack.go:64-` — checkSlackTokenValidity (full check example)
- `docs/superpowers/specs/2026-06-05-slack-federated-bridge-relay-design.md` — approved design spec
- `.planning/phases/95-.../95-CONTEXT.md` — locked decisions

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — no new external dependencies; all patterns verified against live source
- Architecture: HIGH — injection point, struct shape, and decision table all verified line-by-line
- Pitfalls: HIGH — merge-list footgun, `--sidecars` vs `--dry-run=false`, and Lambda freeze patterns all verified in prior phase commentary and source code

**Research date:** 2026-06-05
**Valid until:** 2026-07-05 (stable Go codebase; no fast-moving dependencies)
