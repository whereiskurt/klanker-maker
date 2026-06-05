# Phase 95: Slack federated bridge relay — Context

**Gathered:** 2026-06-05
**Status:** Ready for planning
**Source:** Brainstorm + approved design spec (`docs/superpowers/specs/2026-06-05-slack-federated-bridge-relay-design.md`)

<domain>
## Phase Boundary

**Delivers:** One Slack App's single Events Request URL can serve many km
installs (`resource_prefix`) and many operators in one AWS account/region. The
operator points the App at any one install's bridge ("front door"); a bridge
that receives a message for a channel it doesn't own relays the verbatim event
to sibling bridges via a static per-install URL list, and the owning install
processes it normally.

**In scope:**
- `slack.peer_bridges []string` config key (km-config.yaml) + full plumbing to
  the bridge Lambda env (`KM_SLACK_PEER_BRIDGES`), mirroring `slack.mention_only`.
- `PeerRelayer`/`HTTPPeerRelayer` + the broadcast-on-miss decision logic in the
  bridge `EventsHandler`.
- `km doctor` peer-bridge validity / self-loop / empty-list checks.
- Docs + CLAUDE.md note.

**Out of scope (locked):**
- Cross-account / cross-region federation.
- Shared SSM credentials or a shared DynamoDB install registry (both rejected in
  brainstorm).
- Multi-hop relay chains (single-hop broadcast chosen).
- Any change to `km slack init`, SandboxProfile schema, or sandbox provisioning.
  Existing sandboxes need NO recreate.
</domain>

<decisions>
## Implementation Decisions (LOCKED from brainstorm)

### Discovery mechanism
- Static per-install list `slack.peer_bridges` in km-config.yaml (the OTHER
  installs' bridge `/events` URLs). NOT a shared registry table. Key named
  `peer_bridges` (avoid `eventBridges` — collides with AWS EventBridge).

### Credentials
- Each install keeps its own per-prefix SSM paths, unchanged. Operator pastes
  the SAME App's xoxb + signing secret into each install's normal `km slack
  init`. One App ⇒ same credential VALUES, stored per-install. No shared SSM.

### Relay topology + loop guard
- Single-hop broadcast on a local miss. `X-KM-Relayed: 1` header is the entire
  loop guard. A relayed request is TERMINAL: processed if owned, dropped
  (`slack_relay_no_owner`) otherwise — NEVER re-relayed. Loops structurally
  impossible.

### Decision table (after signature verify, at the FetchByChannel site)
| X-KM-Relayed? | Owns channel? | Action |
|---|---|---|
| absent | yes | process locally (today's path) |
| absent | no  | broadcast raw event to all peer_bridges, return 200 |
| present | yes | process locally |
| present | no  | drop (log slack_relay_no_owner), return 200 |

### Relay mechanics
- Forward verbatim body + `X-Slack-Signature` + `X-Slack-Request-Timestamp` +
  `X-KM-Relayed: 1`, HTTP POST to each peer `/events`.
- Parallel, bounded context (~2.5s), SYNCHRONOUS before returning 200 (Lambda
  freezes env after return → no fire-and-forget goroutines). Peer count tiny so
  3s Slack ack window holds. Failing peer is logged, non-fatal.
- Future: swap to async `lambda:Invoke` if peer count grows (out of scope).

### Plumbing (mirror slack.mention_only EXACTLY — known-good pattern)
1. `internal/app/config/config.go` — `SlackConfig.PeerBridges []string`
   (`mapstructure:"peer_bridges"`); add `"slack.peer_bridges"` to the v2→v
   merge-list (~line 399) or value is silently dropped
   (`project_config_key_merge_list`); populate from `v.GetStringSlice` when
   `v.IsSet("slack.peer_bridges")`.
2. `internal/app/cmd/init.go` — export `KM_SLACK_PEER_BRIDGES` (comma-joined)
   when len>0, with env-wins drift WARN (mirror the MentionOnly/ReactAlways
   blocks ~line 870-895).
3. `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` — `slack_peer_bridges =
   get_env("KM_SLACK_PEER_BRIDGES", "")` (~line 97).
4. `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` — new string var
   `slack_peer_bridges` (default "").
5. `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` — add
   `KM_SLACK_PEER_BRIDGES = var.slack_peer_bridges` to Lambda env (~line 326).
6. `cmd/km-slack-bridge/main.go` — parse env (split "," / trim / drop empty),
   build `PeerRelayer`, inject into `EventsHandler` (nil ⇒ federation off).

### Injection point
- `pkg/slack/bridge/events_handler.go:189` — the `FetchByChannel(ctx,
  msg.Channel)` miss currently returns 200; relay logic goes there. Relayed-
  header read happens after `verifySlackSignature` (line 154).
- `EventsHandler` gains `Relayer PeerRelayer` (nil-safe). New interface in
  `events_interfaces.go`. New `pkg/slack/bridge/relayer.go` + `_test.go`.

### km doctor
- WARN on malformed peer URL, self-loop (URL == own bridge URL), empty
  peer_bridges on the Slack Request-URL host install.
</decisions>

<specifics>
## Specific Ideas / References

- Correctness invariant: channel name/alias uniqueness across ALL installs and
  operators. Per-sandbox `#sb-{id}` and single-owner channels route
  unambiguously; multi-install shared channels (e.g. `#km-notifications`) stay
  notify-only (no single owner → not federated-inbound routed).
- Deploy: Lambda env-block change ⇒ `make build-lambdas` (clean) + `km init
  --dry-run=false` (NOT `--sidecars`). Same as `slack.mention_only`
  (`project_km_init_lambdas_doesnt_deploy`,
  `project_km_init_skips_existing_lambda_zips`).
- Template to copy: everything `slack.mention_only` / `slack.react_always`
  touches is the proven end-to-end pattern; grep `MentionOnly` / `mention_only`
  / `slack_mention_only` to find every touchpoint.
- Existing verify path: `verifySlackSignature` at `events_handler.go:460`
  (HMAC-SHA256, ±5-min window). A relayed request must pass it with the shared
  secret.
</specifics>

<deferred>
## Deferred Ideas

- Async `lambda:Invoke` transport (only if peer count outgrows synchronous
  broadcast).
- `km slack peers` convenience command (nice-to-have; include only if it fits a
  plan cheaply, else defer).
- Reachability HTTP probe of peer `/events` in `km doctor` (optional).
- Cross-account/region federation.
</deferred>

---

*Phase: 95-slack-federated-bridge-relay-one-app-many-prefixes*
*Context gathered: 2026-06-05 from approved design spec*
