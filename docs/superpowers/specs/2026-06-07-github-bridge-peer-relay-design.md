# GitHub bridge federated relay — one App, many installs (Phase 100)

> **Status:** design approved 2026-06-07. Lands as **Phase 100**. Depends on
> Phase 97/98 (the bridge). Independent of Phase 99 (commands) — either order.
>
> **Direct ancestor:** `2026-06-05-slack-federated-bridge-relay-design.md` (Slack
> Phase 95). This is that mechanism ported to GitHub; read it for the shared
> rationale. This spec documents only the GitHub-specific deltas in full.

## Problem

A GitHub App has exactly **one** webhook URL. Today every km install
(`resource_prefix`) runs its own `{prefix}-github-bridge` Lambda with its own
Function URL, its own SSM App config under `/{prefix}/config/github/`, and its own
`github.repos:` — so each install effectively needs its **own** GitHub App.
Operators running several installs in one AWS account (e.g. `kph` and `sec`) want
**one** App to serve all of them, routing each repo's events to the install that
owns it.

## Goal

Allow **one GitHub App to serve many `resource_prefix` installs in one AWS
account.** The operator points the App's single webhook at *any one* install's
bridge ("the front door"). When that bridge receives an event for a repo it does
not own, it **relays** the raw webhook to sibling bridges; the install whose
`github.repos:` matches the repo processes it normally.

### Correctness invariant

**Each repo is owned by exactly one install.** Ownership = a repo matched by that
install's `github.repos:` (`Resolve()` returns `matched`). If two installs match
the same repo (e.g. overlapping `owner/*` globs), both claim ownership and both
dispatch (duplicate). Per-repo ownership across installs must be unique. `km doctor`
cannot cross-check peers' configs, so this is documented, not enforced (mirrors the
Slack channel-uniqueness invariant).

## Non-goals

- The **orphan-repo helpful reply** (no install owns the repo → post a guidance PR
  comment). That is the Phase-96 analog and needs claim-aware scatter-gather instead
  of fire-and-forget broadcast. **Deferred to a future Phase 101.**
- Routing **by command on the same repo** to different prefixes (`/patch` → `sec`,
  `/review` → `kph`). A command's `alias`/`profile` (Phase 99) resolve inside the
  handling bridge's own prefix; cross-prefix dispatch is out of scope entirely.
- Cross-account or cross-region federation (same account + region only).
- Shared App creds in shared SSM (rejected, as in Slack 95 — each install keeps its
  own per-prefix SSM paths; the operator pastes the **same** App's creds + webhook
  secret into each install's `km github init`). One App ⇒ same credential *values*,
  stored per-install.
- A shared DynamoDB install registry (rejected — discovery is a static config list).
- Multi-hop relay chains (rejected — single-hop broadcast; see Loop guard).
- Any change to the SandboxProfile schema or sandbox provisioning. Existing
  sandboxes need **no recreate**.

## Why this works without shared credentials

Every install holds the **same** App's webhook secret (operator pastes it into each
`km github init`). So any bridge can verify GitHub's `X-Hub-Signature-256` HMAC, and
a bridge that receives a **relayed** raw request re-verifies the *same* signature
with its own copy of the secret — the body is forwarded verbatim. GitHub signatures
carry no timestamp, so there is no skew window to worry about; replay protection is
the per-install `X-GitHub-Delivery` dedupe.

## Architecture

Opt-in, per-install, config-driven relay layer over the **existing** per-install
bridges. Unconfigured ⇒ byte-identical to Phase 97/98.

### Config surface (`km-config.yaml`)

```yaml
github:
  peer_bridges:                      # NEW — static list of OTHER installs' bridge URLs
    - https://abc123.lambda-url.us-east-1.on.aws/
    - https://def456.lambda-url.us-east-1.on.aws/
```

- The install hosting the App webhook lists every *other* install's bridge Function
  URL. For full symmetry (repoint the App at any install after a teardown), every
  install lists every other install.
- Empty / absent ⇒ federation off ⇒ today's behaviour exactly.
- GitHub bridge Function URLs have a single route, so the peer URL is the bridge
  root (no `/events` suffix — that was Slack-specific).

### Plumbing (mirrors `slack.peer_bridges` end-to-end)

1. `internal/app/config/config.go` — `GithubConfig` gains
   `PeerBridges []string` (`mapstructure:"peer_bridges" yaml:"peer_bridges,omitempty"`).
2. **Merge-list** — add `"github.peer_bridges"` to the v2→v merge key list, or the
   value is silently dropped ([[project_config_key_merge_list]]).
3. **Population** — populate `cfg.Github.PeerBridges` from
   `v.GetStringSlice("github.peer_bridges")` when set.
4. `internal/app/cmd/init.go` — export `KM_GITHUB_PEER_BRIDGES=<comma-joined>` when
   the list is non-empty, with the env-wins drift WARN (alongside the existing
   `KM_GITHUB_REPOS` export — same pattern).
5. `infra/live/use1/lambda-github-bridge/terragrunt.hcl` — add
   `github_peer_bridges = get_env("KM_GITHUB_PEER_BRIDGES", "")`.
6. `infra/modules/lambda-github-bridge/vX/variables.tf` — new
   `variable "github_peer_bridges" { type = string, default = "" }`.
7. `infra/modules/lambda-github-bridge/vX/main.tf` — add
   `KM_GITHUB_PEER_BRIDGES = var.github_peer_bridges` to the Lambda `environment`.
8. `cmd/km-github-bridge/main.go` — read `KM_GITHUB_PEER_BRIDGES`, split on `,`,
   trim, drop empties; build a `PeerRelayer`; inject into `WebhookHandler`.

> Env var (not SSM): the peer list is tiny, so it stays consistent with
> `KM_GITHUB_REPOS` / `KM_SLACK_PEER_BRIDGES`. (Phase 99 commands went to SSM only
> because inlined `@file` templates can be large; that reasoning does not apply
> here.)

### Runtime decision (in `WebhookHandler.Handle`, `pkg/github/bridge/`)

The relay sits at the **`Resolve()` ownership miss** (`webhook_handler.go:216`,
today's `!matched` → 200 "no repo config"). The one structural change from today:

**Ownership resolution must move ahead of the mention/thread filter for the relay
decision.** Today the order is loop-guard → PR-filter → thread-bypass (step 4b) →
mention (step 5) → `Resolve()` (step 6). A peer-owned *known-thread follow-up with
no @-mention* (Phase 98 thread-bypass) would be dropped at step 5 on the front door
before it ever reached `Resolve()`. So:

- After signature-verify + parse + loop-guard + PR-filter, run `Resolve(owner/repo)`
  to determine **local ownership** (pure config match, no I/O).
- The mention filter, thread-bypass, auth, dedupe, and dispatch run **only on the
  locally-owned (matched) path** — unchanged from today.
- Each peer that receives a relayed request re-runs the **entire** `Handle()`,
  including its own thread-bypass against its own `km-github-threads`.

| `X-KM-Relayed` header | `Resolve()` matched? | Action |
|---|---|---|
| absent | yes | process locally (today's full path: mention/thread/auth/dedupe/dispatch/👀) |
| absent | **no** | **broadcast** raw webhook to all `peer_bridges`, return 200 |
| present | yes | process locally |
| present | no | **drop** (log `github_relay_no_owner`), return 200 — never re-relay |

`X-KM-Relayed: 1` is the entire loop guard. A relayed request is **terminal**:
processed if owned, dropped otherwise — never re-broadcast. Maximum one hop.

### Relay mechanics

- **What is forwarded:** verbatim request body + `X-Hub-Signature-256` +
  `X-GitHub-Event` + `X-GitHub-Delivery` headers + `X-KM-Relayed: 1`.
- **Transport:** HTTP POST to each peer's bridge Function URL.
- **Concurrency / timing:** broadcast runs **synchronously** before the handler
  returns 200 (Lambda freezes after return, so fire-and-forget goroutines are
  unreliable — same constraint as the Slack relay and the 👀 reaction). POSTs run in
  parallel under a bounded context (~5 s; GitHub's ack window is ~10 s, roomier than
  Slack's 3 s). A relay POST failure is logged, non-fatal; the front door still
  returns 200.
- **Future-proofing:** if peer count grows, swap to async `lambda:Invoke` without
  touching the decision table. Out of scope here.

### Dedupe and the 👀 reaction

- Each install dedupes the forwarded `X-GitHub-Delivery` in its **own**
  `{prefix}-nonces` table. The front door dedupes only events it processes locally;
  relayed events are deduped by the peer. GitHub redelivery (5xx) carries a *new*
  GUID, so as today the bridge returns 200 even on internal error.
- The **owner** posts the 👀 reaction (it mints the installation token and
  dispatches). The front door on a miss just relays — **no** reaction. Exactly one
  👀, from the owning install.

### New code units

- `pkg/github/bridge/relayer.go` (new) — `PeerRelayer` interface + `HTTPPeerRelayer`
  impl: `Broadcast(ctx, rawBody []byte, ghHeaders map[string]string) error`.
  Preserves the GitHub headers, adds `X-KM-Relayed: 1`, POSTs to all peers in
  parallel under a bounded context, aggregates/logs errors.
- `WebhookHandler` gains a `Relayer PeerRelayer` field (nil ⇒ federation off ⇒ a
  resolve-miss returns 200 as today, no broadcast).
- `WebhookHandler.Handle` reordered so `Resolve()` ownership is checked before the
  mention/thread filter, with the relay branch on `!matched`.

## CLI / operator surface

- **`km doctor`** additions (mirror the Slack peer checks):
  - WARN if any `github.peer_bridges` URL is malformed or equals this install's own
    bridge URL (self-loop).
  - WARN if this install hosts the App webhook (its `bridge-url` is the App's
    configured webhook) but `peer_bridges` is empty.
  - Optional: HTTP reachability probe of each peer.
- **Optional nicety:** `km github peers` prints the configured relay targets (defer
  if it doesn't fit the plan).

## Deploy / rollout

- Env-block change on the bridge Lambda ⇒ `km init --dry-run=false` (full apply),
  **not** `km init --sidecars` ([[project_km_init_lambdas_doesnt_deploy]]).
- `make build-lambdas` (clean) before `km init`
  ([[project_km_init_skips_existing_lambda_zips]]).
- No SandboxProfile schema change ⇒ **existing sandboxes need no recreate.**

## Operator flow (end state)

1. Create one GitHub App; obtain App creds + webhook secret. `km github manifest`
   renders the manifest.
2. `km github init` on each install, using the **same** App creds + webhook secret
   (each stores them in its own per-prefix SSM paths).
3. Install the App on every repo across all installs.
4. Choose one install as the front door; set the App **Webhook URL** to that
   install's bridge Function URL (`km github status` → `bridge-url`).
5. In that install's `km-config.yaml`, set `github.peer_bridges` to the other
   installs' bridge URLs. (For symmetry, set every install's list to all others.)
6. Each install lists only its own repos in `github.repos:` (unique ownership).
7. `make build-lambdas && km init --dry-run=false` on the affected install(s).
8. A comment on any repo routes to the owning install, regardless of which install's
   bridge GitHub delivers to.

## Testing

- **Decision table (table-driven unit):** `{relayed?, matched?}` →
  `{process, broadcast, drop}` — all four rows.
- **Reorder correctness:** a no-mention known-thread follow-up for a **peer-owned**
  repo relays (is not dropped at the mention filter); a no-mention non-thread
  comment for a peer-owned repo also relays (peer drops it at its own mention
  filter).
- **Loop guard:** relayed + miss never invokes the relayer.
- **Relayer:** builds correct headers (preserves `X-Hub-Signature-256`,
  `X-GitHub-Event`, `X-GitHub-Delivery`; adds `X-KM-Relayed: 1`); POSTs to all
  peers; parallel; honours bounded context; logs + tolerates a failing peer.
- **Peer-side re-verify:** a relayed request with forwarded headers passes
  `VerifyGitHubSignature` with the shared secret.
- **No-owner:** non-relayed miss with empty `peer_bridges` returns 200 and does not
  broadcast; non-relayed miss with peers broadcasts.
- **Config:** `github.peer_bridges` round-trips through merge + population; absent ⇒
  nil; env-wins drift WARN on `KM_GITHUB_PEER_BRIDGES` mismatch.
- **No double 👀 / no double dispatch:** front door on a miss posts no reaction and
  does not enqueue; only the owning peer reacts + dispatches.

## Edge cases

- **No owner anywhere:** all peers drop; front door logs `github_relay_no_owner`;
  200 returned. (The helpful-reply for this case is the deferred Phase 101.)
- **Front door also owns the repo:** local match ⇒ processes, no broadcast.
- **Self in `peer_bridges`:** doctor WARNs; at runtime a self-relayed request
  arrives with `X-KM-Relayed: 1` and is dropped on miss — no loop, but a wasted hop.
- **`ping` event (App setup):** delivered only to the actual webhook URL; the bridge
  already 200s non-`issue_comment` events; never relayed.

## Files touched (implementation surface)

| File | Change |
|---|---|
| `internal/app/config/config.go` | `GithubConfig.PeerBridges`, merge-list entry, population |
| `internal/app/config/config_test.go` | round-trip + drift tests |
| `internal/app/cmd/init.go` | export `KM_GITHUB_PEER_BRIDGES` + drift WARN |
| `infra/live/use1/lambda-github-bridge/terragrunt.hcl` | `github_peer_bridges = get_env(...)` |
| `infra/modules/lambda-github-bridge/vX/variables.tf` | `github_peer_bridges` var |
| `infra/modules/lambda-github-bridge/vX/main.tf` | `KM_GITHUB_PEER_BRIDGES` env |
| `cmd/km-github-bridge/main.go` | parse env, build `PeerRelayer`, inject |
| `pkg/github/bridge/relayer.go` (new) | `PeerRelayer` + `HTTPPeerRelayer` |
| `pkg/github/bridge/relayer_test.go` (new) | relayer unit tests |
| `pkg/github/bridge/webhook_handler.go` | reorder `Resolve()` ahead of mention/thread; relay branch on `!matched` |
| `pkg/github/bridge/webhook_handler_test.go` | decision-table + reorder + loop-guard tests |
| `pkg/github/bridge/interfaces.go` | `PeerRelayer` interface decl |
| `internal/app/cmd/doctor.go` | peer-bridge validity / self-loop / empty-list checks |
| `docs/github-bridge.md` | § Multi-install Pattern B → mark implemented |
| `CLAUDE.md` | Phase 100 note |

## Open questions

None — all resolved during brainstorming (mirrors Slack 95; deltas enumerated above).
