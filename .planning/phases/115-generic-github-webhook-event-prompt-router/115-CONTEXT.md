# Phase 115: Generic GitHub webhook event → prompt router - Context

**Gathered:** 2026-06-15
**Status:** Ready for planning
**Source:** Brainstormed design spec (`docs/superpowers/specs/2026-06-15-github-webhook-event-router-design.md`)

<domain>
## Phase Boundary

Add a **second ingress class** to the existing `km-github-bridge` Lambda. Today
the bridge only handles `issue_comment` events (human @-mentions in PR comments).
This phase makes the bridge react to **autonomous, non-comment webhook events**
(`repository`, `push`, `release`, …) by mapping each event type + repo-glob scope
to an agent prompt that runs in a sandbox. First use case: a new repo created in
the org (`repository`/`created`) triggers a configured onboarding/audit prompt.

**In scope:** event-type branch in the webhook handler; the `github.events:`
config block + env plumbing; the first-match `EventRouter` (gating, exclude,
template expansion); envelope construction + reuse of existing dispatch; opt-in
cooldown; manifest event/scope additions; sandbox poller tolerance for
non-`issue_comment` envelopes; `km doctor` rule checks; docs; unit tests + a
live E2E.

**Out of scope (deferred):** per-actor `allowSenders:` allowlists; topic-based /
file-based opt-out (an empty new repo can't carry one — config-side `exclude:`
glob only); opinionated outcomes (router stays outcome-agnostic — the prompt +
`km-*` helpers decide PR vs issue vs Slack vs nothing).
</domain>

<decisions>
## Implementation Decisions (LOCKED — from brainstorming)

### Scope & abstraction
- Build the **generic event→prompt router** (not a one-off new-repo feature).
  New-repo (`repository`/`created`) is the first rule, not the whole feature.
- Frame it as `github.commands:` **turned inside-out**: a command maps a
  human-typed `/token` → prompt; an event rule maps a webhook event type → prompt.
  Reuse the downstream machinery (template expansion, envelope, dispatch, poller).

### Gating
- **Org + repo-glob `match:` fires by default.** The GitHub App installation is
  the org/trust boundary; `match:` narrows. No matching rule ⇒ drop with 200 OK
  (byte-identical to today's unknown-event behavior — the safe default).
- **No actor allowlist** for autonomous events (no meaningful `sender` to gate).
  Optional `allowSenders:` is explicitly deferred.

### Opt-out
- **Config-side `exclude:` glob** is the primary opt-out, evaluated in the bridge
  so it works on a brand-new empty repo. Topic-based opt-out for content-bearing
  events (`push`/`release`) is a noted future add-on, not in this phase.

### Outcome
- **Outcome-agnostic router.** The router's job is "match event → run this prompt
  in a sandbox." The prompt text + whichever `km-github`/`km-slack` helpers it
  invokes decide what gets produced.

### Rule selection
- **First-match** (deterministic, mirrors `Resolve`), not all-matching-rules.

### Storm control
- **Opt-in per-(event, repo, action) cooldown**, default `cooldownSeconds: 0`
  (off). `repository` rules leave it off; noisy types (`push`) opt in. Uses the
  existing nonces table (same pattern as the Phase 96/101 routers).

### Config shape
```yaml
github:
  events:
    - on: repository           # x-github-event type
      actions: [created]       # optional; empty = all actions
      match: "myorg/*"         # required repo glob
      exclude: ["myorg/archive-*"]   # optional opt-out globs
      profile: profiles/onboard.yaml
      alias: ""                # optional — reuse a long-lived sandbox
      agent: claude            # optional — claude | codex
      cooldownSeconds: 0       # optional storm guard, default off
      prompt: |
        A new repo {{repo}} was created. Clone it and open a PR adding CI.
```
- Template vars: `{{repo}}`, `{{event}}`, `{{action}}`, `{{sender}}`,
  `{{default_branch}}`, `{{html_url}}`.
- New bridge env var `KM_GITHUB_EVENTS` (JSON), populated like `KM_GITHUB_REPOS`.
  New config key MUST be added to the v2→v merge-list in `config.Load()`.

### Reuse anchors (from codebase map)
- Event-type branch point: `pkg/github/bridge/webhook_handler.go:194`.
- Template expansion: `pkg/github/bridge/commands.go:357` (`ExpandTemplate`).
- Envelope: `pkg/github/bridge/payload.go:59` (`GitHubEnvelope`); set
  `Kind=<event>`, `Number=0`, `Body=<prompt>`, `Agent=rule.agent`.
- Cold-create dispatch: `pkg/github/bridge/aws_adapters.go:445`
  (`PutSandboxCreate`).
- Config struct: `internal/app/config/config.go:118` (`GithubCommandEntry` is the
  model for `GithubEventRule`).
- Manifest events: `internal/app/cmd/github.go:109` (`DefaultEvents`).

### Deploy surface
- `make build-lambdas` + `km init --github` (or full `km init --dry-run=false`)
  for the new `KM_GITHUB_EVENTS` env block — **NOT `--sidecars`**.
- `km github manifest` regenerated + **App re-install** to subscribe to the new
  event(s) and grant scopes (`repository` → `metadata: read`; PR-push reuses the
  existing `contents:write` opt-in).
- Cold-created sandboxes get the new poller for free; long-lived `alias:`
  sandboxes need `km destroy && km create`.

### Claude's Discretion
- Exact `GithubEventRule` field validation rules and the `km doctor` check
  wording/severity.
- Whether the manifest's event subscription is derived from config or a
  hardcoded supported-events list bumped per phase.
- Nonces-table key format for the cooldown
  (suggested `gh-event-cooldown:{event}:{repo}:{action}`).
- Internal package layout (new `event_router.go` / `events.go` vs extending
  existing files).
</decisions>

<specifics>
## Specific Ideas

- The first real-world rule the operator wants: "any new repo in our org → run a
  fixed onboarding prompt" (e.g. scaffold CI + SECURITY.md and open a PR, or audit
  and post to Slack — chosen by the prompt text).
- Keep the `issue_comment` path **byte-identical** — this is purely additive.
- Match the existing bridge's fail-soft / deny-by-default posture: anything
  unrecognized drops with 200.
</specifics>

<deferred>
## Deferred Ideas

- Per-actor `allowSenders:` gating for autonomous events.
- Topic-based / file-based opt-out (for content-bearing events once a repo has
  content).
- Router-baked outcomes (PR vs Slack vs issue policy per event) — stays
  prompt-driven.
</deferred>

---

*Phase: 115-generic-github-webhook-event-prompt-router*
*Context gathered: 2026-06-15 via brainstormed design spec*
