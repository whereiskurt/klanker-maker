# km v0.5.7

_Released 2026-06-25 · 90 commits since v0.5.6 · 167 files changed (+14,728 / −5,275)_

Policy-driven sandbox platform. This release adds **composable profile inheritance**, **Slack trigger allowlisting + private per-sandbox channels**, and **per-thread Slack inbound parallelism**, plus richer Slack rendering and an eBPF resolver stability fix.

## Install

```bash
# macOS (Apple Silicon)
curl -fsSL https://github.com/whereiskurt/klanker-maker/releases/download/v0.5.7/km_v0.5.7_darwin_arm64.tar.gz | tar -xz
# then move `km` onto your PATH; `bin/terraform` + `bin/terragrunt` are bundled
```

Operators still install `aws` CLI, `session-manager-plugin`, and `docker` (Docker-substrate only).

---

## Major additions

### 🧬 Composable multi-parent profile inheritance (Phase 117)
`extends:` now accepts an **ordered list** of parents, not just a single string. Profiles resolve a full DAG (left→right, child last) via a uniform `deepMerge`: maps recurse, scalars take the child/right-most value, and every list field concats-and-dedups. The old hand-written per-block merger zoo is gone, replaced by one generic engine over a YAML round-trip.
- **Abstract fragments** (`metadata.abstract: true`) live in `profiles/base/` — `km validate` skips them, `km create` rejects them.
- New `execution.initCommandsAppend` appends leaf-specific install steps after the merged `initCommands`.
- Diamond inheritance is memoized; cycles rejected; max depth 10.
- Shipped a fragment library (`profiles/base/{safenetwork,sidecars-all,observability-learn,budget-standard,artifacts-workspace,iam-us-east-1,agent-claude-all-tools,email-strict}.yaml`); `learn.v2.*` and `dc34.yaml` now compose them.
- **Deploy:** `make build` only (resolved at `km create` time, no infra change).

### 🔐 Slack trigger allowlist + private per-sandbox channels (Phase 118)
Two independent, composable Slack additions — both **dormant by default**.
- **Private channels:** `notification.slack.private: true` creates the per-sandbox channel as `is_private:true` (no new OAuth scopes — `groups:*` already requested). Effective only with `perSandbox:true`.
- **Trigger allowlist:** gate *who can trigger the bot* by Slack user ID. Install-level `slack.allow` (→ `KM_SLACK_ALLOW`) and per-sandbox `notification.slack.inbound.allow` (→ `slack_allow` row attr). Non-empty per-sandbox **replaces** install-level; empty ⇒ everyone allowed (backward-compatible). Enforced before the mention-only filter and the thread-bypass; rejection is a **silent ignore**.
- `km doctor` now checks `groups:read` and surfaces the private-channel blind spot.
- **Deploy:** `make build-lambdas` + `km init --dry-run=false`; existing sandboxes need `km destroy && km create`.

### ⚡ Slack inbound per-thread parallelism (Phase 119)
Different Slack threads to the same sandbox now run **in parallel**, while messages within a thread stay **serial + ordered** — bounded by an operator concurrency cap. **Dormant by default** (cap=1 ≡ prior serial behavior).
- **Bridge:** SQS FIFO `MessageGroupId` changed from sandbox-id → `threadTS`, giving parallel-across-threads / serial-within-thread for free.
- **Poller:** new `notification.slack.inbound.maxConcurrentThreads` (*int) → `KM_SLACK_MAX_CONCURRENCY`; the poller dispatches each turn in a bounded-concurrent subshell (counting semaphore), **acks after completion** (required for ordering), with a `ChangeMessageVisibility` heartbeat and a `last_processed_event_ts` idempotency guard. Inbound queue base VisibilityTimeout raised to 1800s.
- `km validate` WARNs on cap>1 without `perSandbox` + `inbound.enabled`.
- Verified by a live synthetic-HMAC E2E (6 assertions: parallelism, ordering, cap, heartbeat/no-dup, dormancy, dedup).
- **Caveat:** parallel turns share `/workspace` — suited for conversational / read-mostly fan-out, not concurrent repo-mutating turns.
- **Deploy:** `make build-lambdas` + `km init --dry-run=false`; existing sandboxes need `km destroy && km create`.

### 🎨 Richer Slack rendering
- Inbound replies steered toward **blocks-rich** (native GFM tables + headings + inline markdown styles in table cells and prose URLs).
- **Fix:** guard `blocks-rich` against `invalid_blocks` silently dropping replies (Phase 112.1 — empty-cell vector + defense-in-depth re-post without blocks).

### 🛡️ eBPF resolver IP-lifetime floor
- Resolved IPs are now retained for a minimum lifetime (default 10m, `--min-ip-lifetime` flag + `clampTTL` helper) — fixes premature eviction that slowed vscode-server and other long-lived connections.

---

## Features

- **slack:** steer inbound replies toward blocks-rich (GFM tables + headings)
- **slack:** render inline markdown as rich_text styles in table cells + prose URLs
- **profile (117):** `ExtendsField` union type (string | list) + DAG resolve + generic `deepMerge`/`concatDedup` engine (memoized, diamond-idempotent)
- **profile (117):** fragment marker + `initCommandsAppend` + JSON schema updates; `profiles/base/` fragment library; `learn.v2.*`/`dc34` refactored to compose them
- **slack (118):** thread `private` bool through `CreateChannel`; per-sandbox `slack_allow` round-trip; install-level `KM_SLACK_ALLOW` → bridge allowlist; enforcement gate (AC2/AC3/AC5)
- **slack (119):** group FIFO by `threadTS`; `MaxConcurrentThreads` field + schema; emit `KM_SLACK_MAX_CONCURRENCY`; bounded-concurrent ack-after poller; raise queue VisibilityTimeout to 1800s
- **doctor:** check `groups:read` + surface private-channel blind spot (Phase 118)
- **ebpf:** retain resolved IPs for a minimum lifetime (default 10m); `--min-ip-lifetime` flag + `clampTTL` helper

## Bug fixes

- **slack:** guard blocks-rich against `invalid_blocks` dropping replies
- **slack:** render inline markdown as rich_text styles in table cells + prose URLs
- **117:** fix all `Extends` call sites to use `ExtendsField.IsSet()/List()` (incl. allowlistgen test)
- **119-02:** update stale VisibilityTimeout (30s→1800s) + group-id test assertions

## Documentation

- README slimmed; deep docs moved to the Wiki
- New/updated: `OPERATOR-GUIDE.md` § Composable inheritance; `docs/slack-notifications.md` §§ Phase 118, Phase 119; `klanker:slack` SKILL
- Plugin version bumped **0.4.11 → 0.4.12** (skill content change — clients cache the old version otherwise)

---

## Upgrade notes

- **No `apiVersion` bump** — profiles stay `klankermaker.ai/v1alpha2`.
- All three headline features are **dormant by default**: an unchanged `km-config.yaml` + profiles behave byte-identically to v0.5.6.
- To adopt Phase 118/119 sandbox-side behavior, existing sandboxes must be recreated (`km destroy && km create`); the bridge/install-level pieces take effect on `km init --dry-run=false`.
