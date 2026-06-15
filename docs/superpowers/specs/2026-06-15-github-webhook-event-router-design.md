# Design: Generic GitHub webhook event → prompt router

**Date:** 2026-06-15
**Status:** Approved (design) — pending implementation plan
**Author:** brainstormed with operator

## Problem

The km GitHub bridge today is entirely **human-gated and PR-scoped**: GitHub
delivers only `issue_comment` events, and a turn fires only when an allowlisted
person @-mentions the bot in a PR comment. `github.commands:` lets operators map
a `/token` (typed by a human) to a prompt template.

There is no way to react to **autonomous, non-comment webhook events** — e.g.
"any new repository created in our org should trigger a specific agent prompt."
This requires a second ingress class into the bridge and a generic mapping from
*webhook event type* → *agent prompt*.

## Goals

- A generic **event → prompt** router: map any GitHub webhook event type
  (`repository`, `push`, `release`, …) to a prompt that runs in a sandbox.
- First use case: `repository`/`created` → fixed onboarding/audit prompt for any
  new org repo.
- Reuse the existing dispatch machinery (envelope → cold-create/warm → poller →
  agent) and the existing template mechanism. Minimal new surface.
- Safe by default: no matching rule ⇒ nothing fires (byte-identical to today).

## Non-goals

- Per-actor allowlists for autonomous events (no meaningful `sender` to gate;
  org+glob is the trust boundary). Optional `allowSenders:` is a future add-on.
- Opinionated outcomes (PR vs Slack vs issue). The router is outcome-agnostic;
  the prompt + `km-*` helpers decide.
- Topic-based / file-based opt-out at creation time (an empty new repo can't
  carry one). Topic opt-out is a noted future add-on for content-bearing events.

## Decisions (from brainstorming)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Scope | **Generic event→prompt router** (not just new-repo) |
| 2 | Gating | **Org + repo-glob `match:`** fires by default |
| 3 | Opt-out | **Config-side `exclude:` glob** (works on empty repos); topic opt-out deferred |
| 4 | Outcome | **Outcome-agnostic** — prompt + `km-github`/`km-slack` helpers decide |
| 5 | Rule selection | **First-match** (deterministic, mirrors `Resolve`) |
| 6 | Storm control | **Cooldown opt-in per rule, default off** (`cooldownSeconds: 0`) |

## Core idea

This is `github.commands:` **turned inside-out**:

- A *command* maps a `/token` (human-typed in a PR) → prompt template.
- An *event rule* maps a **webhook event type** (delivered autonomously by
  GitHub) → prompt template.

Everything downstream is shared: template expansion → `GitHubEnvelope` →
cold-create/warm dispatch → sandbox poller → agent. The only genuinely new code
is **(a)** an event-type branch in the handler and **(b)** the event-rule matcher
+ optional storm guard.

## Config surface — new `github.events:` block

```yaml
github:
  repos: [...]      # unchanged — issue_comment routing
  commands: {...}   # unchanged — /token → prompt
  events:           # NEW
    - on: repository           # GitHub webhook event type (x-github-event)
      actions: [created]       # optional; default = all actions for that type
      match: "myorg/*"         # repo glob (required) — org/scope boundary (decision 2)
      exclude:                 # optional opt-out globs (decision 3)
        - "myorg/archive-*"
        - "myorg/*-fork"
      profile: profiles/onboard.yaml   # profile for cold-create
      alias: ""                # optional — reuse a long-lived sandbox instead of cold-create
      agent: claude            # optional — claude | codex
      cooldownSeconds: 0       # optional storm guard; default 0 = off (decision 6)
      prompt: |
        A new repo {{repo}} was created in our org. Clone it and open a PR
        adding our standard CI workflow + SECURITY.md.
```

**Template vars** injected from the payload: `{{repo}}`, `{{event}}`,
`{{action}}`, `{{sender}}`, `{{default_branch}}`, `{{html_url}}`.

**Go surface:** mirrors `GithubCommandEntry` (`internal/app/config/config.go:118`).
New `GithubEventRule` struct + a slice on `GithubConfig`. New bridge Lambda env
var `KM_GITHUB_EVENTS` (JSON), populated exactly like `KM_GITHUB_REPOS`
(`cmd/km-github-bridge/main.go:106-126`). Reuses `ExpandTemplate`
(`pkg/github/bridge/commands.go:357`).

> Config-key plumbing: new keys must also be added to the v2→v merge-list in
> `config.Load()`, not just the struct + getter, or the value is silently
> ignored. (Known footgun.)

## Data flow

1. GitHub delivers `repository`/`created` to the bridge Function URL.
2. HMAC verify + delivery-GUID dedup — **unchanged**
   (`pkg/github/bridge/webhook_handler.go:179-347`).
3. **New branch** at `webhook_handler.go:194`: `issue_comment` → existing path;
   any other `x-github-event` → `EventRouter`.
4. `EventRouter` parses a minimal payload for that event type (repo full name,
   action, sender, default branch), then **first-match** against
   `github.events:` where: `on` == event type, `action` ∈ `actions` (or
   `actions` empty), repo matches `match`, repo does **not** match any `exclude`.
5. No rule matches → drop with 200 OK (byte-identical to today's unknown-event
   behavior — the safe default).
6. Match → expand prompt template → build a `GitHubEnvelope`
   (`pkg/github/bridge/payload.go:59`) with `Kind: "<event-type>"`,
   `Number: 0`, `Body: <expanded prompt>`, `Agent: rule.agent`.
7. Dispatch: rule names a running/stopped `alias` → warm/resume enqueue; else
   **cold-create** via EventBridge `SandboxCreate`
   (`pkg/github/bridge/aws_adapters.go:445`) — the common case for new repos.
8. Sandbox poller drains the envelope (already keys on `Source: "github"`), runs
   the agent with `Body` as the prompt; the prompt + `km-*` helpers decide the
   outcome.

## Gating & safety

- **Org boundary = trust boundary.** The GitHub App installation scopes which
  org's events arrive; `match:` narrows further. Deny-by-default survives: no
  rule ⇒ nothing fires.
- **`exclude:` glob** is evaluated in the bridge, so it works on a brand-new
  empty repo (no file/topic required). Topic-based opt-out is a future add-on for
  content-bearing events (`push`/`release`).
- **No actor allowlist** for autonomous events — intentional (decision 2).
  Optional `allowSenders:` may be added later.

## Storm control

The delivery-GUID dedup only catches GitHub's **retries**. For high-frequency
event types (`push`), an opt-in **per-(event, repo, action) cooldown** uses the
existing nonces table — the same pattern as the Phase 96/101 routers
(`router-cooldown:{...}` TTL key). Default `cooldownSeconds: 0` (off);
`repository` rules leave it off. Within a configured window, repeated matching
events drop with a `github_event_cooldown` log line, preventing a cold-create
storm.

## Manifest & deploy surface

- `km github manifest` (`internal/app/cmd/github.go:109`) gains the **union of
  event types** referenced in `github.events:` (e.g.
  `["issue_comment", "repository"]`) plus any scopes those events imply
  (`repository` needs `metadata: read`; pushing PRs needs the existing
  `contents:write` opt-in — see `docs/github-app-permissions.md`). Requires
  **re-installing the App** to subscribe to the new event + grant scopes.
- **Deploy:** `make build-lambdas` + `km init --github` (or full
  `km init --dry-run=false`) for the new `KM_GITHUB_EVENTS` env block — **not
  `--sidecars`** (env-block change requires a terragrunt apply).
- **Sandbox poller:** minor change to tolerate `Number: 0` / non-`issue_comment`
  `Kind`. Cold-created sandboxes get the new poller for free; long-lived `alias:`
  sandboxes need `km destroy && km create`.

## Testing

- **Unit:** event matcher (match/exclude globs + action filter), first-match
  ordering, cooldown gate, template expansion — modeled on
  `pkg/github/bridge/resolve_test.go` and `commands_test.go`.
- **Live UAT:** create a throwaway repo in the org, confirm a sandbox
  cold-creates and the prompt runs end-to-end. (Poller-bash / sandbox-side paths
  are invisible to Go goldens — only live UAT catches their bugs.)

## Open items for the plan

- Exact `GithubEventRule` field validation rules + `km doctor` checks (malformed
  glob, unknown event type, reserved-token collisions).
- Whether the manifest's event subscription is derived from config or a
  hardcoded list bumped per supported event type.
- Nonces-table key format for the cooldown (`gh-event-cooldown:{event}:{repo}:{action}`).
