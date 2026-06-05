# Slack Federated Bridge Relay — Design Spec

**Date:** 2026-06-05
**Status:** Approved (brainstorm complete)
**Phase:** 95
**Branch:** `phase-95-slack-federated-bridge-relay`

## Problem

A Slack App has exactly **one** Events Request URL. Today every km install
(`resource_prefix`) runs its own `{prefix}-slack-bridge` Lambda with its own
Function URL, its own bot token, and its own signing secret — so each install
effectively needs its **own** Slack App. Operators running several installs in
one AWS account (and/or several operators sharing one account) want to install
**one** Slack App and have all installs share it.

## Goal

Allow **one Slack App to serve many `resource_prefix` installs and many
operators in a single AWS account.** The operator points the App's single Events
Request URL at *any one* install's bridge ("the front door"). When that bridge
receives a message for a channel it does not own, it **relays** the raw event to
sibling bridges; the install that owns the channel processes it normally.

### Correctness invariant

**Channel name / alias uniqueness across all installs and operators in the
account.** Routing is by `FetchByChannel(channel_id)` against each install's own
`{prefix}-sandboxes` table. If two installs map the *same* Slack channel, both
would claim ownership and both would process the message (duplicate). Therefore:
per-sandbox channels (`#sb-{id}`) and any single-owner channel are safe;
multi-install **shared** channels (e.g. `#km-notifications`) have no single
owner and remain **notify-only** (outbound) — federated *inbound* is not routed
there.

## Non-goals

- Cross-account or cross-region federation (same account + region only).
- Shared bot token / shared signing secret in shared SSM (rejected in
  brainstorm — each install keeps its own per-prefix SSM paths; the operator
  pastes the **same** App's xoxb + signing secret into each install's normal
  `km slack init`). One App ⇒ same credential *values*, stored per-install.
- A shared DynamoDB install registry (rejected — discovery is a static config
  list, see below).
- Multi-hop relay chains (rejected — single-hop broadcast, see Loop Guard).
- Any change to `km slack init`, the SandboxProfile schema, or sandbox
  provisioning. Existing sandboxes need **no recreate**.

## Why this works without shared credentials

Every install holds the **same** App's signing secret (operator pastes it into
each `km slack init`). So:
1. Any bridge can verify Slack's inbound HMAC signature.
2. A bridge that receives a **relayed** raw request can re-verify the *same*
   signature with its own copy of the secret — the body and
   `X-Slack-Request-Timestamp` are forwarded verbatim and remain inside the
   ±5-minute window. The relayed request flows through the peer's normal
   `/events` verification path unchanged.

## Architecture

Opt-in, per-install, config-driven relay layer over the **existing**
per-install bridges. When unconfigured, behaviour is byte-identical to today.

### Config surface (`km-config.yaml`)

```yaml
slack:
  peer_bridges:                     # NEW — static list of OTHER installs' bridge /events URLs
    - https://abc123.lambda-url.us-east-1.on.aws/events
    - https://def456.lambda-url.us-east-1.on.aws/events
```

- Key name `peer_bridges` (NOT `eventBridges` — avoids confusion with the AWS
  EventBridge service).
- The install hosting the Slack Request URL lists every *other* install's bridge
  URL. For full symmetry (repoint Slack at any install after a teardown), every
  install lists every other install.
- Empty / absent ⇒ federation off ⇒ today's behaviour exactly.

### Plumbing (mirrors `slack.mention_only` end-to-end)

1. **`internal/app/config/config.go`** — `SlackConfig` gains
   `PeerBridges []string` (`mapstructure:"peer_bridges" yaml:"peer_bridges,omitempty"`).
2. **Merge-list** — add `"slack.peer_bridges"` to the v2→v merge key list
   (~line 399) or the value is silently dropped (known footgun:
   [[project_config_key_merge_list]]).
3. **Population** — after the merge, populate `cfg.Slack.PeerBridges` from
   `v.GetStringSlice("slack.peer_bridges")` when `v.IsSet(...)`.
4. **`internal/app/cmd/init.go`** — alongside the `MentionOnly` / `ReactAlways`
   blocks (~line 870-895), export
   `KM_SLACK_PEER_BRIDGES=<comma-joined>` when `len(cfg.Slack.PeerBridges) > 0`,
   with the same env-wins drift WARN.
5. **`infra/live/use1/lambda-slack-bridge/terragrunt.hcl`** — add
   `slack_peer_bridges = get_env("KM_SLACK_PEER_BRIDGES", "")` (~line 97).
6. **`infra/modules/lambda-slack-bridge/v1.0.0/variables.tf`** — new
   `variable "slack_peer_bridges" { type = string, default = "" }`.
7. **`infra/modules/lambda-slack-bridge/v1.0.0/main.tf`** — add
   `KM_SLACK_PEER_BRIDGES = var.slack_peer_bridges` to the Lambda `environment`
   block (~line 326).
8. **`cmd/km-slack-bridge/main.go`** — read `KM_SLACK_PEER_BRIDGES`, split on
   `,`, trim, drop empties; build a `PeerRelayer`; inject into `EventsHandler`.

### Runtime decision table (`pkg/slack/bridge/events_handler.go`)

After signature verification (`events_handler.go:154`) and message extraction,
read the relay marker header, then branch on local ownership at the
`FetchByChannel` site (`events_handler.go:189`):

| `X-KM-Relayed` header | Owns channel? | Action |
|---|---|---|
| absent | yes | process locally (today's path) |
| absent | **no** | **broadcast** raw event to all `peer_bridges`, return 200 |
| present | yes | process locally |
| present | no | **drop** (log `slack_relay_no_owner`), return 200 — never re-relay |

`X-KM-Relayed: 1` is the entire loop guard. A relayed request is **terminal**:
processed if owned, dropped otherwise — never re-broadcast. Maximum one hop;
loops are structurally impossible.

### Relay mechanics

- **What is forwarded:** the verbatim request body + original
  `X-Slack-Signature` and `X-Slack-Request-Timestamp` headers + `X-KM-Relayed: 1`.
- **Transport:** HTTP POST to each peer's `/events` Function URL.
- **Concurrency / timing:** broadcast is performed **synchronously** before the
  handler returns 200 (Lambda freezes the execution env after return, so
  fire-and-forget goroutines are unreliable). POSTs run in parallel bounded by a
  context timeout (~2.5 s) so the front door still satisfies Slack's 3-second
  ack window. Peer count is tiny and each peer only hands off (SQS enqueue + 👀),
  so this is comfortable. A relay POST failure is logged, non-fatal; the front
  door still returns 200.
- **Future-proofing:** if peer count ever grows, the relay swaps to async
  `lambda:Invoke` without touching the decision table. Out of scope for Phase 95.

### New code units

- `pkg/slack/bridge/relayer.go` (new) — `PeerRelayer` interface +
  `HTTPPeerRelayer` impl: `Broadcast(ctx, rawBody string, slackHeaders map[string]string) error`.
  Preserves Slack signature headers, adds `X-KM-Relayed: 1`, POSTs to all peers
  in parallel with a bounded context, aggregates/logs errors.
- `EventsHandler` gains a `Relayer PeerRelayer` field (nil ⇒ federation off ⇒
  miss returns 200 as today, no broadcast).

## CLI / operator surface

- **`km doctor`** additions:
  - WARN if any `slack.peer_bridges` URL is malformed or points at this
    install's own bridge URL (self-loop).
  - WARN if this install appears to host the Slack Request URL but
    `peer_bridges` is empty (federation misconfigured).
  - Optional: reachability probe (HTTP) of each peer `/events`.
- **Optional nicety:** `km slack peers` prints the configured relay targets.
  (Low cost; include if it fits the plan, otherwise defer.)

## Deploy / rollout

- Env-block change on the bridge Lambda ⇒ requires `km init --dry-run=false`
  (full apply), **not** `km init --sidecars` (which rebuilds binaries but does
  not update the Lambda `environment` block) — identical constraint to
  `slack.mention_only` ([[project_km_init_lambdas_doesnt_deploy]]).
- `make build-lambdas` (clean) before `km init`, or stale Lambda zips are
  redeployed ([[project_km_init_skips_existing_lambda_zips]]).
- No SandboxProfile schema change ⇒ **existing sandboxes need no recreate.**

## Operator flow (end state)

1. Install one Slack App; obtain xoxb + signing secret.
2. `km slack init` on each install, pasting the **same** xoxb + signing secret
   (each install stores them in its own per-prefix SSM paths, as today).
3. Choose one install as the front door; set the Slack App Events Request URL to
   that install's `{bridge-url}/events`.
4. In that install's `km-config.yaml`, set `slack.peer_bridges` to the other
   installs' `/events` URLs. (For symmetry, set every install's list to all
   others.)
5. `make build-lambdas && km init --dry-run=false` on the affected install(s).
6. Inbound messages in any install's per-sandbox channel route correctly,
   regardless of which install's bridge Slack delivers to.

## Testing

- **Decision table (table-driven unit):** `{relayed?, localHit?}` →
  `{process, broadcast, drop}` — all four rows.
- **Loop guard:** relayed + miss never invokes the relayer.
- **Relayer:** builds correct headers (preserves `X-Slack-Signature` +
  `X-Slack-Request-Timestamp`, adds `X-KM-Relayed: 1`); POSTs to all peers;
  parallel; honours bounded context timeout; logs + tolerates a failing peer.
- **Peer-side re-verify:** a relayed request with forwarded headers passes
  `verifySlackSignature` with the shared secret.
- **Bounded-await timing:** handler returns within budget even when a peer is
  slow (context timeout fires).
- **No-owner:** non-relayed miss with empty `peer_bridges` returns 200 and does
  not broadcast (federation off); non-relayed miss with peers broadcasts.
- **Config:** `slack.peer_bridges` round-trips through merge + population;
  absent ⇒ nil; env-wins drift WARN on `KM_SLACK_PEER_BRIDGES` mismatch.

## Edge cases

- **No owner anywhere:** all peers drop; front door logged `slack_relay_no_owner`;
  200 returned. Correct (channel belongs to nobody).
- **Front door also owns a shared channel:** local hit ⇒ processes, no
  broadcast. This is the shared-channel ambiguity — documented as notify-only.
- **`url_verification` challenge:** only the actual Request URL bridge receives
  it; responds directly; never relayed (challenge has no Slack signature flow
  concern and no channel).
- **Self in `peer_bridges`:** doctor WARNs; at runtime a self-relayed message
  arrives with `X-KM-Relayed: 1` and is dropped on miss, so no infinite loop —
  but it wastes a hop, hence the doctor check.

## Files touched (implementation surface)

| File | Change |
|---|---|
| `internal/app/config/config.go` | `SlackConfig.PeerBridges`, merge-list entry, population |
| `internal/app/config/config_test.go` | config round-trip + drift tests |
| `internal/app/cmd/init.go` | export `KM_SLACK_PEER_BRIDGES` + drift WARN |
| `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` | `slack_peer_bridges = get_env(...)` |
| `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` | `slack_peer_bridges` var |
| `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` | `KM_SLACK_PEER_BRIDGES` env |
| `cmd/km-slack-bridge/main.go` | parse env, build `PeerRelayer`, inject |
| `pkg/slack/bridge/relayer.go` (new) | `PeerRelayer` + `HTTPPeerRelayer` |
| `pkg/slack/bridge/relayer_test.go` (new) | relayer unit tests |
| `pkg/slack/bridge/events_handler.go` | relay marker + miss→broadcast decision table |
| `pkg/slack/bridge/events_handler_test.go` | decision-table + loop-guard tests |
| `pkg/slack/bridge/events_interfaces.go` | `PeerRelayer` interface decl |
| `internal/app/cmd/doctor.go` | peer-bridge validity / self-loop / empty-list checks |
| `docs/slack-notifications.md` | new § Phase 95 federated relay |
| `CLAUDE.md` | Phase 95 note |
