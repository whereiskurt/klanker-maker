# Phase 100: GitHub bridge federated relay — Research

**Researched:** 2026-06-08
**Domain:** Go Lambda webhook routing; km config plumbing; Terragrunt/Terraform env-var pipeline; HMAC relay
**Confidence:** HIGH (this phase ports a SHIPPED phase — Slack 95 — and the design spec is exhaustive; every analog file was located and read in the live tree)

## Summary

Phase 100 is a near-mechanical port of the **shipped** Slack Phase 95 federated relay (`pkg/slack/bridge/relayer.go` + the `slack.peer_bridges` config pipeline) onto the GitHub bridge (`pkg/github/bridge/`). One GitHub App has exactly one webhook URL; each `resource_prefix` install runs its own `{prefix}-github-bridge` Lambda. The "front door" install relays verbatim webhooks it does not own to sibling bridges via `X-KM-Relayed: 1` single-hop broadcast, and the install whose `github.repos:` matches processes it. The relay engine, config struct, merge handling, init.go env export, Terragrunt `get_env`, TF variable, Lambda env block, and `km doctor` peer check all have a directly-readable Slack precedent in this repo.

There are two important **deviations from a literal Slack port** that the planner must honor:

1. **Merge-list:** Unlike `slack.peer_bridges` (which needed its OWN `"slack.peer_bridges"` merge-list entry because Slack config is decoded field-by-field via `GetString/GetStringSlice`), the GitHub block is decoded as a single structured `v.UnmarshalKey("github", &cfg.Github)` and the `"github"` key is **already** in the merge-list (`config.go:551`). Adding `PeerBridges []string` to `GithubConfig` is picked up automatically — **no new merge-list entry is required.** The spec's literal "step 2" (add `"github.peer_bridges"` to the merge-list) is WRONG for this codebase; a config round-trip test is still mandatory to PROVE this.
2. **Relayer is fire-and-forget (Phase 95 style), NOT claim-aware (Phase 96 style).** The current `pkg/slack/bridge/relayer.go` carries Phase 96 `PeerClaimResult` scatter-gather machinery. The GitHub relayer must be the SIMPLER Phase-95-era shape: `Broadcast(ctx, rawBody, headers) error` with no claim parsing (orphan-repo reply is deferred to Phase 101). Use the Slack relayer's **structure** (synchronous `wg.Wait()`, bounded context, parallel POSTs, `X-KM-Relayed` header) but drop the response-body parsing.

**Primary recommendation:** Copy the Slack 95 pipeline file-by-file per the mapping table below, simplify the relayer to fire-and-forget, and make the `Resolve()` reorder in `webhook_handler.go` unconditional (it is also the 700-repo scale fix). The current `Resolve()` is at `webhook_handler.go:232` (step 6), AFTER the thread-lookup (4b, line 211) and mention filter (5, line 225) — the reorder moves it before both.

## User Constraints

No CONTEXT.md exists for this phase (`.planning/phases/100-.../` contains only `.gitkeep` siblings; no `*-CONTEXT.md`). The authoritative constraint source is the design spec `docs/superpowers/specs/2026-06-07-github-bridge-peer-relay-design.md` (read in full) plus the ROADMAP phase description. Treat these as locked:

### Locked Decisions (from design spec)
- **Opt-in, env-driven relay** via new `github.peer_bridges: []string` in `km-config.yaml` → `KM_GITHUB_PEER_BRIDGES` (comma-joined). Absent/empty ⇒ federation off ⇒ byte-identical to Phase 97/98.
- **Single-hop broadcast.** `X-KM-Relayed: 1` is the entire loop guard. A relayed request is terminal: process if owned, drop (`github_relay_no_owner`) otherwise, NEVER re-relay.
- **Verbatim forward** of body + `X-Hub-Signature-256` + `X-GitHub-Event` + `X-GitHub-Delivery` + add `X-KM-Relayed: 1`. Each peer re-verifies HMAC with its own copy of the SAME App webhook secret (GitHub sigs carry no timestamp → no skew window).
- **Synchronous + parallel broadcast** under a bounded context (~5s; GitHub ack window ~10s). Lambda freezes on return, so fire-and-forget goroutines are unreliable — `wg.Wait()` before returning.
- **`Resolve()` reorder is UNCONDITIONAL** (applies even with `peer_bridges` empty). It must produce BYTE-IDENTICAL dispatch outcomes and removes a wasted `LookupSandbox` DDB GetItem per PR comment on unowned/unconfigured repos.
- **Owner posts the single 👀.** Front door on a miss just relays — no reaction.
- **Each install dedupes forwarded `X-GitHub-Delivery` in its OWN `{prefix}-nonces`.**
- **NO SandboxProfile schema change** ⇒ no `km init --sidecars`, no sandbox recreate. Deploy = `make build-lambdas` (clean) + `km init --dry-run=false`.

### Claude's Discretion
- Whether to bump the Lambda module to `v1.2.0` or edit `v1.1.0` in place (see Architecture Patterns — recommend in-place edit of `v1.1.0`, the version the live unit references).
- Exact bounded-context duration (spec says ~5s; recommend a named const e.g. `relayBroadcastTimeout = 5 * time.Second`).
- Optional `km github peers` CLI nicety (spec says defer if it doesn't fit).

### Deferred Ideas (OUT OF SCOPE — do not research/build)
- **Orphan-repo helpful PR comment** (no install owns the repo → post a guidance comment). This is the Phase-96 analog requiring claim-aware scatter-gather. **Deferred to Phase 101.** Do NOT bring the `PeerClaimResult`/`Channels`/`maybePostOrphanReply` machinery across.
- Cross-prefix dispatch on the same repo (command-based routing to different prefixes).
- Cross-account / cross-region federation.
- Shared App creds in shared SSM; shared DynamoDB install registry; multi-hop relay chains.

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| GH-FED-CONFIG | `github.peer_bridges` config → `KM_GITHUB_PEER_BRIDGES` env, end-to-end | Mapping table rows 1–7; merge-list deviation noted; `init.go:1067` Slack export is the template |
| GH-FED-RELAY | Verbatim broadcast to peer bridges | `pkg/github/bridge/relayer.go` (new) modeled on `pkg/slack/bridge/relayer.go` (simplified to fire-and-forget); wired in `webhook_handler.go` `!matched` branch |
| GH-FED-REORDER | `Resolve()` ahead of mention/thread filter (unconditional scale fix) | Current order mapped: thread-lookup line 211, mention line 225, Resolve line 232; reorder + dispatch-byte-identity tests |
| GH-FED-LOOPGUARD | `X-KM-Relayed: 1` single-hop terminal | Slack decision table at `events_handler.go:234-300`; GitHub decision table from spec (4 rows) |
| GH-FED-VERIFY | Peer re-verifies HMAC with shared secret | `VerifyGitHubSignature` (`webhook_handler.go:464`) is unchanged and already runs first in `Handle()`; relay forwards `X-Hub-Signature-256` verbatim |
| GH-FED-DOCTOR | peer-URL-validity / self-loop / empty-on-front-door | `checkSlackPeerBridges` (`doctor_slack.go:586`) is the template; own bridge URL from SSM `{prefix}/config/github/bridge-url` (`doctor.go:1059`) |
| GH-FED-SCALE | No wasted `LookupSandbox` for unowned repos (700-repo fix) | Same reorder as GH-FED-REORDER; test asserts zero DDB read for unowned repo with `Threads` configured |
| GH-FED-E2E | End-to-end live verification | Decision-table unit tests + manual UAT (two installs, one App); see Validation Architecture |

## Standard Stack

No new third-party libraries. Everything uses the existing in-repo stack:

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go stdlib `net/http` | go 1.25.5 | Relay POST transport | Slack `HTTPPeerRelayer` already uses it; `http.Client` reused from cold-start |
| Go stdlib `sync`, `context` | go 1.25.5 | Bounded synchronous broadcast | `sync.WaitGroup` + `context.WithTimeout` — exact Slack relayer pattern |
| Go stdlib `crypto/hmac`, `crypto/sha256` | go 1.25.5 | HMAC re-verify on relayed requests | `VerifyGitHubSignature` already implemented, unchanged |
| `aws-lambda-go` | (in go.mod) | Function URL request/response | Already used by `cmd/km-github-bridge/main.go` |
| viper (via `internal/app/config`) | (in go.mod) | Config decode | `UnmarshalKey("github", …)` already decodes the github block |

**Installation:** none. `make build-lambdas` (clean) rebuilds the bridge zip; `km init --dry-run=false` deploys.

## Architecture Patterns

### File-by-file mapping: Slack 95 → GitHub 100 (THE core deliverable)

| # | Concern | Slack source (verified) | GitHub target | Current GitHub state → needed change |
|---|---------|------------------------|----------------|--------------------------------------|
| 1 | Config struct field | `config.go:54` `SlackConfig.PeerBridges []string` | `config.go` `GithubConfig` (struct at line 146) | Add `PeerBridges []string \`mapstructure:"peer_bridges" yaml:"peer_bridges,omitempty"\`` |
| 2 | Merge-list entry | `config.go:544` `"slack.peer_bridges"` | `config.go:551` `"github"` | **NO new entry needed** — `UnmarshalKey("github",…)` (line 675) decodes the whole struct; the `"github"` merge entry already exists. DEVIATION from spec step 2. |
| 3 | Population | `config.go:649` `if v.IsSet("slack.peer_bridges") { cfg.Slack.PeerBridges = v.GetStringSlice(...) }` | `config.go:675` `v.UnmarshalKey("github", &cfg.Github)` | **Already populated** by the existing UnmarshalKey — new field is decoded automatically. No new population block. |
| 4 | init.go env export | `init.go:1067-1074` (KM_SLACK_PEER_BRIDGES, `strings.Join`, env-wins WARN) | `init.go` ~after line 1118 (KM_GITHUB_REPOS block) | NEW: export `KM_GITHUB_PEER_BRIDGES` from `cfg.Github.PeerBridges` (gate `len>0`, env-wins drift WARN) — copy the Slack block verbatim, rename vars |
| 5 | Terragrunt get_env | `lambda-slack-bridge/terragrunt.hcl:116` `slack_peer_bridges = get_env("KM_SLACK_PEER_BRIDGES","")` | `lambda-github-bridge/terragrunt.hcl` inputs block (after line 90) | NEW: `github_peer_bridges = get_env("KM_GITHUB_PEER_BRIDGES","")` |
| 6 | TF variable | `lambda-slack-bridge/v1.0.0/variables.tf:133` `variable "slack_peer_bridges"` | `lambda-github-bridge/v1.1.0/variables.tf` | NEW: `variable "github_peer_bridges" { type=string default="" }` |
| 7 | TF env block | `lambda-slack-bridge/v1.0.0/main.tf:339` `KM_SLACK_PEER_BRIDGES = var.slack_peer_bridges` | `lambda-github-bridge/v1.1.0/main.tf:286` (last env line `KM_ARTIFACTS_PREFIX`) | NEW: `KM_GITHUB_PEER_BRIDGES = var.github_peer_bridges` in the `environment.variables` block |
| 8 | cmd main.go parse + wire | `cmd/km-slack-bridge/main.go:268-282` (split on `,`, trim, build `HTTPPeerRelayer`, inject) | `cmd/km-github-bridge/main.go` ~line 210 (before WebhookHandler construct) | NEW: read `KM_GITHUB_PEER_BRIDGES`, build `bridge.HTTPPeerRelayer{PeerURLs, HTTPClient}`, set `webhookHandler.Relayer` |
| 9 | Relayer interface | `pkg/slack/bridge/events_interfaces.go:44` `PeerRelayer` | `pkg/github/bridge/interfaces.go` | NEW: `PeerRelayer interface { Broadcast(ctx, rawBody []byte, ghHeaders map[string]string) error }` — SIMPLER than Slack (no `[]PeerClaimResult`) |
| 10 | Relayer impl | `pkg/slack/bridge/relayer.go` (whole file) | `pkg/github/bridge/relayer.go` (new) | NEW: `HTTPPeerRelayer` — copy Slack structure, DROP `peerRelayResponse`/`PeerClaimResult` parsing; return plain `error` |
| 11 | Handler field | `events_handler.go:80` `Relayer PeerRelayer` | `webhook_handler.go` `WebhookHandler` struct (line 61) | NEW: `Relayer PeerRelayer` field (nil ⇒ federation off) |
| 12 | Decision branch | `events_handler.go:234-300` (`x-km-relayed` check + `Relayer.Broadcast`) | `webhook_handler.go` `!matched` branch (line 233) | Reorder + relay branch (see below) |
| 13 | doctor check | `doctor_slack.go:586` `checkSlackPeerBridges` + wiring `doctor.go:3927-3941` | `internal/app/cmd/doctor.go` (add `checkGitHubPeerBridges`) | NEW: mirror the Slack check; own URL from SSM `{prefix}config/github/bridge-url` (already read by `checkGitHubBridgeURL`, line 1059) |
| 14 | config test | `config_test.go:880` `TestLoadSlackPeerBridges_Set` | `internal/app/config/config_test.go` | NEW: `TestLoadGithubPeerBridges_Set` — round-trip + nil-when-absent + (deviation) prove no separate merge entry needed |

### The `Resolve()` reorder (GH-FED-REORDER + GH-FED-SCALE)

**Current `Handle()` order** (`webhook_handler.go`, verified line numbers):
```
Step 1  sig verify            line 150  (VerifyGitHubSignature) — UNCHANGED, stays first
        event-type filter     line 163  (issue_comment only)
Step 2  parse payload         line 170
        action!="created"     line 175
Step 3  loop guard (Bot)      line 186
Step 4  PR-only filter        line 194
Step 4b thread LookupSandbox  line 211  ← DDB GetItem (the wasted read on unowned repos)
Step 5  @-mention filter      line 225
Step 6  Resolve(owner/repo)   line 232  ← ownership match (pure config, no I/O)
        allowlist             line 238
Step 7  dedupe delivery GUID  line 246
        command pass          line 270
        dispatch (3-way)      line 338
Step 10 👀 reaction           line 446
```

**Target order:** move `Resolve()` (line 232) up to **immediately after the PR-only filter (line 198), before the thread-lookup (4b)**. Then:
- `!matched` → relay branch (broadcast if `Relayer != nil` and not already relayed; else 200 as today).
- `matched` → run thread-lookup (4b), mention (5), allowlist, dedupe, command-pass, dispatch, 👀 — EXACTLY as today.

This makes the thread `LookupSandbox` DDB read and the mention scan run only on the owned path. **Byte-identical dispatch invariant holds** because a thread row only ever exists for a repo this install owns (rows are written on dispatch, which requires `matched`), so skipping the lookup for unowned repos loses nothing.

> **Subtlety to preserve:** `Resolve()` returns `(alias, profile, allow, matched)`. The allowlist check (line 238) and command pass (line 270) consume `alias`/`profile`/`allow`. After the reorder, `Resolve()` runs earlier but its return values must still flow into the SAME downstream code. Keep the allowlist + dedupe + dispatch block intact on the matched path; only the POSITION of the `Resolve()` call and the `!matched` early-exit change.

### The relay decision table (GH-FED-LOOPGUARD)

From the design spec; implement as a table-driven test:

| `X-KM-Relayed` header | `Resolve()` matched? | Action |
|---|---|---|
| absent | yes | process locally (full path: thread/mention/auth/dedupe/dispatch/👀) |
| absent | **no** | broadcast raw webhook to all `peer_bridges`, return 200 (only if `Relayer != nil`; else 200 no-op) |
| present | yes | process locally |
| present | no | **drop** (log `github_relay_no_owner`), return 200 — NEVER re-relay |

Header key is read from `req.Headers["x-km-relayed"]` (Lambda Function URL headers are lowercased by `lowercaseHeaders` in `cmd/km-github-bridge/main.go:313`). The check must run AFTER signature verify (so the loop guard is authenticated) — which it naturally does, since sig verify is step 1 and stays first.

### Relayer impl pattern (simplified from Slack)

```go
// Source: pattern from pkg/slack/bridge/relayer.go (Phase 95), simplified —
// NO PeerClaimResult parsing (orphan reply deferred to Phase 101).
type HTTPPeerRelayer struct {
    PeerURLs   []string
    HTTPClient *http.Client
}

// Broadcast POSTs rawBody verbatim to all peers in parallel under a bounded
// context. SYNCHRONOUS: wg.Wait() before return (Lambda freezes on return —
// see Slack relayer PITFALL 4). A failing peer is logged, non-fatal.
func (r *HTTPPeerRelayer) Broadcast(ctx context.Context, rawBody []byte, ghHeaders map[string]string) error {
    if len(r.PeerURLs) == 0 { return nil }
    bctx, cancel := context.WithTimeout(ctx, relayBroadcastTimeout) // ~5s
    defer cancel()
    var wg sync.WaitGroup
    for _, peer := range r.PeerURLs {
        wg.Add(1)
        go func(u string) {
            defer wg.Done()
            req, _ := http.NewRequestWithContext(bctx, http.MethodPost, u, bytes.NewReader(rawBody))
            req.Header.Set("Content-Type", "application/json")
            // Forward GitHub auth + routing headers verbatim:
            req.Header.Set("X-Hub-Signature-256", ghHeaders["x-hub-signature-256"])
            req.Header.Set("X-GitHub-Event",      ghHeaders["x-github-event"])
            req.Header.Set("X-GitHub-Delivery",   ghHeaders["x-github-delivery"])
            req.Header.Set("X-KM-Relayed", "1") // loop guard — TERMINAL at peer
            resp, err := client.Do(req)
            if err != nil { slog.Warn("github-bridge: relay peer error", "peer", u, "err", err, "event", "github_relay_peer_error"); return }
            resp.Body.Close()
        }(peer)
    }
    wg.Wait() // MUST wait — Lambda freeze
    return nil
}
```

### Module versioning

The live unit `infra/live/use1/lambda-github-bridge/terragrunt.hcl:32` sources `infra/modules/lambda-github-bridge/v1.1.0`. **Recommendation: edit `v1.1.0` in place** (add the variable + env line) rather than mint `v1.2.0` — this is an additive, backward-compatible env var with a `default=""`, and the Slack module added `slack_peer_bridges` to its existing `v1.0.0` in place (no version bump). A version bump would require touching the live `source =` line too. Confirm with the project's module-versioning convention during planning, but the precedent (Slack) is in-place.

### Anti-Patterns to Avoid
- **Bringing Phase 96 scatter-gather across.** The current Slack `relayer.go` has `peerRelayResponse`, `PeerClaimResult`, `Channels`, and `maybePostOrphanReply`. NONE of that belongs in Phase 100 — it's the deferred Phase 101 orphan reply. Copy the Phase-95-era SHAPE, not the current file verbatim.
- **Adding a `"github.peer_bridges"` merge-list entry.** Redundant and misleading — the `"github"` UnmarshalKey already covers it. (The spec's literal step 2 is wrong for this repo; verify with a test.)
- **Fire-and-forget goroutine without `wg.Wait()`.** Lambda freezes on return; the broadcast must complete synchronously.
- **Re-relaying a request that carries `X-KM-Relayed: 1`.** Single-hop only.
- **Posting 👀 on the front-door miss.** Only the owning install reacts.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| HMAC re-verify on relayed request | A new verify path | `VerifyGitHubSignature` (`webhook_handler.go:464`), unchanged | Already constant-time; relayed body is verbatim so the same sig verifies |
| Synchronous bounded broadcast | Custom goroutine orchestration | Copy `sync.WaitGroup`+`context.WithTimeout` from `pkg/slack/bridge/relayer.go` | Proven Lambda-freeze-safe pattern in production (Phase 95) |
| Config env export + drift WARN | New export helper | Copy `init.go:1067` Slack block | env-wins drift WARN semantics already standardized |
| Peer-URL/self-loop doctor check | New validation logic | Copy `checkSlackPeerBridges` (`doctor_slack.go:586`) | Identical URL-parse + self-loop semantics |
| Delivery-GUID dedupe | New nonce table | Existing `{prefix}-slack-bridge-nonces` via `DeliveryNonceStore` (`webhook_handler.go:246`) | Each install already dedupes its own deliveries; relayed events dedupe at the peer |

**Key insight:** Every moving part of this phase already exists in shipped code — the work is COPYING with two simplifications (drop Phase 96 claim machinery; skip the redundant merge-list entry) and one structural reorder.

## Common Pitfalls

### Pitfall 1: Lambda freeze on async broadcast
**What goes wrong:** A `go r.Broadcast(...)` without waiting returns 200, Lambda freezes, in-flight POSTs never complete.
**Why:** AWS Lambda freezes the execution environment the instant the handler returns.
**How to avoid:** `Broadcast` is synchronous internally (`wg.Wait()`); `Handle()` calls it and only then returns 200. Exactly as the Slack relayer documents (PITFALL 4 in its source).
**Warning signs:** Relayed events sporadically not delivered to peers; flaky under load.

### Pitfall 2: Silent config drop (the project footgun, inverted)
**What goes wrong:** Operator sets `github.peer_bridges` but the field stays nil.
**Why:** The `project_config_key_merge_list` footgun — BUT here the inverse risk: a planner who blindly follows the spec adds a redundant `"github.peer_bridges"` merge entry AND a redundant population block, possibly conflicting with the existing `UnmarshalKey("github",…)`.
**How to avoid:** Add ONLY the struct field. Write `TestLoadGithubPeerBridges_Set` to PROVE the value round-trips via the existing UnmarshalKey with no new merge entry.
**Warning signs:** Test passes only after adding a redundant merge entry → investigate; the existing `"github"` entry should suffice.

### Pitfall 3: Reorder changes dispatch behavior
**What goes wrong:** Moving `Resolve()` up accidentally reorders the allowlist/dedupe/dispatch or drops a thread-bypass case.
**Why:** The thread-bypass (Phase 98) lets a no-mention known-thread follow-up dispatch on the OWNED path; if `Resolve()` short-circuits before reaching the matched path's thread-lookup, an owned thread follow-up could be wrongly dropped.
**How to avoid:** Reorder only moves the `!matched` early-exit forward. ALL matched-path logic (thread-lookup 4b, mention 5, allowlist, dedupe, command-pass, dispatch, 👀) stays in the same relative order on the matched branch. Test: a no-mention known-thread follow-up on an OWNED repo still dispatches; on a PEER-owned repo it relays (front door) and the peer dispatches it.
**Warning signs:** Phase 98 thread-bypass tests fail after the reorder.

### Pitfall 4: env-block change deployed with `--sidecars`
**What goes wrong:** `km init --sidecars` rebuilds the zip + cold-starts but does NOT update the Lambda `environment.variables` block, so `KM_GITHUB_PEER_BRIDGES` never reaches the Lambda.
**Why:** `project_km_init_lambdas_doesnt_deploy` — env-block changes require a full terragrunt apply.
**How to avoid:** Deploy = `make build-lambdas` (clean) + `km init --dry-run=false`. Document in CLAUDE.md Phase 100 note and `docs/github-bridge.md`.
**Warning signs:** Relay silently off after deploy; `KM_GITHUB_PEER_BRIDGES` empty in Lambda console.

### Pitfall 5: stale lambda zip (`project_km_init_skips_existing_lambda_zips`)
**What goes wrong:** `make build` alone doesn't rebuild the bridge zip; a stale zip ships without the new relayer code.
**How to avoid:** `make build-lambdas` (clean) before `km init`. The km-github-bridge IS in the `lambdaBuilds()` list (it deploys today), so this is the standard rebuild — just don't skip it.

## Code Examples

### Decision branch in Handle() (after reorder)
```go
// Source: pattern from pkg/slack/bridge/events_handler.go:234-300 (Phase 95),
// adapted to GitHub (no claim parsing).
// ── after PR-only filter (line ~198), BEFORE thread-lookup ──
alias, profile, allow, matched := Resolve(payload.Repository.FullName, h.Entries, h.DefaultProfile)
if !matched {
    relayed := req.Headers["x-km-relayed"] != ""
    if relayed {
        // TERMINAL: relayed + no owner → drop, never re-relay.
        h.log().Warn("github-bridge: relay miss — no owner for relayed delivery",
            "repo", payload.Repository.FullName, "event", "github_relay_no_owner")
        return WebhookResponse{StatusCode: 200, Body: "ok"}
    }
    if h.Relayer != nil {
        if err := h.Relayer.Broadcast(ctx, req.RawBody, req.Headers); err != nil {
            h.log().Warn("github-bridge: relay broadcast partial failure", "err", err,
                "repo", payload.Repository.FullName)
        }
    } else {
        h.log().Info("github-bridge: no repo config, silent drop", "repo", payload.Repository.FullName)
    }
    return WebhookResponse{StatusCode: 200, Body: "ok"}
}
// matched → fall through to thread-lookup (4b), mention (5), allowlist, dedupe,
// command-pass, dispatch, 👀 — UNCHANGED from today.
```

### doctor check (mirror Slack)
```go
// Source: pattern from internal/app/cmd/doctor_slack.go:586 checkSlackPeerBridges
func checkGitHubPeerBridges(peerBridges []string, ownBridgeURL string) CheckResult {
    // identical url.Parse + scheme/host + self-loop logic;
    // own URL from SSM {prefix}config/github/bridge-url (doctor.go:1059 reads it already).
}
// Wiring near doctor.go:3438 GitHub bridge check group; gate on githubConfigured.
// Spec also wants: WARN if this install hosts the App webhook (bridge-url == App's
// configured webhook) but peer_bridges is empty — best-effort, may be SKIPPED if the
// App's configured webhook isn't locally knowable.
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| One GitHub App per install | One App, many installs via static relay | This phase (100) | Operators run `kph`+`sec` off one App |
| `LookupSandbox` DDB read per PR comment org-wide | Resolve-miss short-circuits before DDB read | This phase (reorder) | 700-repo scale fix; fewer DDB reads |
| Slack relayer = Phase 96 claim-aware | GitHub relayer = Phase 95 fire-and-forget | This phase | Simpler; orphan reply deferred to 101 |

**Deprecated/outdated:**
- The design spec's line references (e.g. "`webhook_handler.go:216`" for the resolve miss) are from an earlier draft. The LIVE code resolves at line 232 (step 6), with the `!matched` 200 at line 233-237. Use the live line numbers in this RESEARCH, not the spec's.

## Open Questions

1. **Module version bump vs in-place edit**
   - What we know: Live unit sources `v1.1.0`; Slack added its peer var to `v1.0.0` in place.
   - What's unclear: Whether project convention mandates a version bump for any module change.
   - Recommendation: Edit `v1.1.0` in place (additive, `default=""`, backward-compatible). If the plan-checker flags it, bump to `v1.2.0` and update the live `source =` line + any module-inventory list.

2. **"App webhook host" doctor WARN (empty peer_bridges on front door)**
   - What we know: The install hosting the webhook should have `peer_bridges` set.
   - What's unclear: The bridge cannot know whether GitHub is configured to deliver to IT specifically (that's GitHub-App-side state).
   - Recommendation: Implement the malformed-URL + self-loop WARNs (deterministic, local). Make the "front-door-with-empty-peers" WARN best-effort or SKIP if undeterminable; do not block the phase on it.

3. **`internal/app/cmd` tests excluded from `make test`**
   - What we know: `make test` excludes `internal/app/cmd` and `cmd/km-slack`. The init.go/doctor.go tests run via other targets.
   - Recommendation: Run `go test ./internal/app/cmd/...` and `go test ./pkg/github/bridge/...` explicitly in the validation harness (see below).

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (table-driven), go 1.25.5 |
| Config file | none (standard `go test`) |
| Quick run command | `go test ./pkg/github/bridge/... -run Relay -count=1` |
| Full suite command | `go test ./pkg/github/bridge/... ./internal/app/config/... ./internal/app/cmd/... -count=1` |

> Note: `make test` deliberately excludes `internal/app/cmd` and `cmd/km-*` (see Makefile:75). For this phase, run those packages directly; the relayer + handler tests live in `pkg/github/bridge` which IS covered by `make test`.

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| GH-FED-CONFIG | `github.peer_bridges` round-trips; nil when absent; no new merge entry | unit | `go test ./internal/app/config/... -run GithubPeerBridges -count=1` | ❌ Wave 0 (`TestLoadGithubPeerBridges_Set`) |
| GH-FED-CONFIG | `KM_GITHUB_PEER_BRIDGES` export + env-wins drift WARN | unit | `go test ./internal/app/cmd/... -run GithubPeerBridges -count=1` | ❌ Wave 0 |
| GH-FED-RELAY | Relayer builds correct headers, POSTs all peers, parallel, bounded, tolerates failing peer | unit | `go test ./pkg/github/bridge/... -run Relayer -count=1` | ❌ Wave 0 (`pkg/github/bridge/relayer_test.go`) |
| GH-FED-REORDER | `{relayed?, matched?}` 4-row decision table | unit | `go test ./pkg/github/bridge/... -run DecisionTable -count=1` | ❌ Wave 0 (add to `webhook_handler_phase100_test.go`) |
| GH-FED-REORDER | no-mention known-thread follow-up: owned→dispatch, peer-owned→relay | unit | `go test ./pkg/github/bridge/... -run Reorder -count=1` | ❌ Wave 0 |
| GH-FED-LOOPGUARD | relayed+miss never invokes relayer; never re-relays | unit | `go test ./pkg/github/bridge/... -run LoopGuard -count=1` | ❌ Wave 0 |
| GH-FED-VERIFY | relayed request with forwarded headers passes `VerifyGitHubSignature` | unit | `go test ./pkg/github/bridge/... -run RelayedVerify -count=1` | ❌ Wave 0 |
| GH-FED-SCALE | federation OFF + Threads set: unowned-repo PR comment does ZERO `LookupSandbox` DDB read; owned repo still reads | unit (mock counts calls) | `go test ./pkg/github/bridge/... -run NoWastedRead -count=1` | ❌ Wave 0 |
| GH-FED-DOCTOR | malformed URL / self-loop / empty → correct WARN/OK/SKIP | unit | `go test ./internal/app/cmd/... -run GithubPeerBridges -count=1` | ❌ Wave 0 |
| GH-FED-E2E | two installs, one App: comment on `sec`-owned repo delivered via `kph` front door routes correctly; exactly one 👀 | manual UAT | n/a (documented runbook) | ❌ Wave 0 (UAT doc) |

### Sampling Rate
- **Per task commit:** `go test ./pkg/github/bridge/... -count=1` (relayer + handler — fast, no AWS)
- **Per wave merge:** `go test ./pkg/github/bridge/... ./internal/app/config/... ./internal/app/cmd/... -count=1`
- **Phase gate:** full suite green + `make build-lambdas` succeeds + Terraform validate on the edited module (`make test-phase-84-1-terraform-validate` covers TF validate) before `/gsd:verify-work`.

### Wave 0 Gaps
- [ ] `pkg/github/bridge/relayer_test.go` — `HTTPPeerRelayer` unit tests (headers, parallel, bounded ctx, failing peer) — covers GH-FED-RELAY, GH-FED-VERIFY
- [ ] `pkg/github/bridge/webhook_handler_phase100_test.go` — decision table, reorder correctness, loop guard, no-wasted-read — covers GH-FED-REORDER, GH-FED-LOOPGUARD, GH-FED-SCALE
- [ ] `internal/app/config/config_test.go` — `TestLoadGithubPeerBridges_Set` (+ nil-when-absent + prove-no-merge-entry) — covers GH-FED-CONFIG
- [ ] `internal/app/cmd/init_test.go` (or existing init test file) — `KM_GITHUB_PEER_BRIDGES` export + drift WARN — covers GH-FED-CONFIG
- [ ] `internal/app/cmd/doctor_github_test.go` — `checkGitHubPeerBridges` cases — covers GH-FED-DOCTOR
- [ ] UAT doc (two-install, one-App live verification) — covers GH-FED-E2E
- [ ] Mock for `SandboxAliasResolver`/`GitHubThreadStore` must support call-count assertion for the no-wasted-read test (check existing test mocks in `handle_test.go` — likely already countable; if not, extend)

*Framework already installed; no new test deps.*

## Sources

### Primary (HIGH confidence — read in full from the live tree)
- `docs/superpowers/specs/2026-06-07-github-bridge-peer-relay-design.md` — authoritative design + files-touched table
- `pkg/github/bridge/webhook_handler.go` — current `Handle()` ordering (verified line numbers)
- `pkg/github/bridge/interfaces.go` — where `PeerRelayer` interface goes
- `cmd/km-github-bridge/main.go` — Lambda wiring (where to build/inject `Relayer`)
- `pkg/slack/bridge/relayer.go` — the relay engine template (note: simplify away Phase 96 claim machinery)
- `pkg/slack/bridge/events_handler.go:234-300` — decision-branch template
- `pkg/slack/bridge/events_interfaces.go:44` — `PeerRelayer` interface template
- `cmd/km-slack-bridge/main.go:264-282` — env-parse + relayer-build template
- `internal/app/config/config.go` — `GithubConfig` (line 146), merge-list (551), UnmarshalKey github (675), Slack peer field (54), Slack population (649)
- `internal/app/config/config_test.go:880` — `TestLoadSlackPeerBridges_Set` template
- `internal/app/cmd/init.go:1067` — `KM_SLACK_PEER_BRIDGES` export template; `:1100` KM_GITHUB_REPOS export
- `infra/live/use1/lambda-github-bridge/terragrunt.hcl` — inputs block + module source `v1.1.0`
- `infra/modules/lambda-github-bridge/v1.1.0/{variables.tf,main.tf:272}` — TF var + env block
- `infra/modules/lambda-slack-bridge/v1.0.0/{variables.tf:133,main.tf:339}` — Slack TF peer template
- `internal/app/cmd/doctor_slack.go:586` — `checkSlackPeerBridges` template
- `internal/app/cmd/doctor.go:1059,3429,3927` — github bridge-url SSM read + github check gating + slack peer wiring
- `.planning/config.json` — `nyquist_validation: true`
- `Makefile:75,220` — `test` and `build-lambdas` targets

### Secondary (MEDIUM)
- CLAUDE.md Phase 95/96/97/98 sections; MEMORY.md gotchas (`project_config_key_merge_list`, `project_km_init_lambdas_doesnt_deploy`, `project_km_init_skips_existing_lambda_zips`)

### Tertiary (LOW)
- None — no external/web sources needed; this phase is entirely internal-codebase mechanical.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — zero new deps; every primitive exists in shipped code
- Architecture / file mapping: HIGH — every analog file located and read; line numbers verified against live tree
- Reorder correctness: HIGH — current order mapped precisely; byte-identity argument validated against Phase 98 thread-bypass semantics
- Merge-list deviation: HIGH — confirmed `UnmarshalKey("github",…)` + existing `"github"` merge entry; spec's literal step 2 is wrong for this repo
- Pitfalls: HIGH — all derive from documented project gotchas + the shipped Slack relayer's own PITFALL notes

**Research date:** 2026-06-08
**Valid until:** 2026-07-08 (stable — internal codebase, no fast-moving external deps)
