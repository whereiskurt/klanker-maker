# Phase 115: Generic GitHub webhook event → prompt router - Research

**Researched:** 2026-06-15
**Domain:** Go, AWS Lambda, GitHub webhooks, km-github-bridge
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Build the **generic event→prompt router** (not a one-off new-repo feature). New-repo (`repository`/`created`) is the first rule.
- Org + repo-glob `match:` fires by default. No actor allowlist for autonomous events.
- Config-side `exclude:` glob is the primary opt-out (works on brand-new empty repos).
- Outcome-agnostic router — prompt + `km-*` helpers decide.
- First-match rule selection (deterministic, mirrors `Resolve`).
- Opt-in per-(event, repo, action) cooldown, default `cooldownSeconds: 0` (off). Uses the existing nonces table.
- Config shape is the `github.events:` block (see CONTEXT.md).
- Template vars: `{{repo}}`, `{{event}}`, `{{action}}`, `{{sender}}`, `{{default_branch}}`, `{{html_url}}`.
- New bridge env var `KM_GITHUB_EVENTS` (JSON), populated like `KM_GITHUB_REPOS`.
- New config key MUST be added to the v2→v merge-list in `config.Load()`.
- Deploy surface: `make build-lambdas` + `km init --github` (or full `km init --dry-run=false`). NOT `--sidecars`.
- `km github manifest` regenerated + App re-install to subscribe new events.

### Claude's Discretion
- Exact `GithubEventRule` field validation rules and `km doctor` check wording/severity.
- Whether manifest event subscription derives from config or a hardcoded supported-events list.
- Nonces-table key format for the cooldown (suggested `gh-event-cooldown:{event}:{repo}:{action}`).
- Internal package layout (new `event_router.go` / `events.go` vs extending existing files).

### Deferred Ideas (OUT OF SCOPE)
- Per-actor `allowSenders:` gating for autonomous events.
- Topic-based / file-based opt-out.
- Router-baked outcomes (PR vs Slack vs issue policy per event).
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| GH-EVENT-CONFIG | `github.events:` YAML block + `GithubEventRule` struct in config; new key in v2→v merge-list | config.go merge-list at line ~700 uses "github" key atomically — `Events []GithubEventRule` slots into `GithubConfig` and is decoded by the existing `UnmarshalKey("github", &cfg.Github)` call |
| GH-EVENT-ROUTER | `EventRouter` pure function: first-match across rules (on+actions+match glob+exclude globs) | Reuse `isGlob`+`path.Match` from `resolve.go`; exact same two-pass pattern; lives in new `event_router.go` |
| GH-EVENT-GATING | Org+glob `match:` fires by default; `exclude:` glob opt-out; no-match → 200 drop | Seam is `webhook_handler.go:194-197` — replace the `issue_comment`-only filter with a two-branch switch |
| GH-EVENT-TEMPLATE | Expand `{{repo}}`, `{{event}}`, `{{action}}`, `{{sender}}`, `{{default_branch}}`, `{{html_url}}` | `ExpandTemplate` at `commands.go:357` does simple `strings.ReplaceAll`; new `ExpandEventTemplate` applies the same pattern for six vars |
| GH-EVENT-DISPATCH | Reuse `GitHubEnvelope` + `PutSandboxCreate` / FIFO `Send` dispatch paths | `payload.go:75` `GitHubEnvelope`; `aws_adapters.go:445` `PutSandboxCreate`; warm path via `SQS.Send` + `Resolver.GitHubQueueURL` |
| GH-EVENT-COOLDOWN | Opt-in per-(event,repo,action) cooldown using existing nonces table | `DynamoGitHubNonceStore.CheckAndStore` + key `gh-event-cooldown:{event}:{repo}:{action}` — same pattern as `gh-router-cooldown:` in Phase 96/101 |
| GH-EVENT-MANIFEST | `km github manifest` gains union of configured event types + implied scopes | `github.go:109` `DefaultEvents: []string{"issue_comment"}` — extend to merge configured event types |
| GH-EVENT-POLLER | Sandbox poller tolerates `Number==0` and non-`issue_comment` `Kind` for event envelopes | `userdata.go:2200` validation rejects missing `repo/number`; Phase 115 needs conditional logic: `Number==0` is valid for event envelopes; preamble must branch on `Kind` |
| GH-EVENT-DOCTOR | `km doctor` check for `github.events:` rules (malformed globs, missing profiles, reserved tokens) | Mirrors `checkGitHubCommandsValid` at `doctor.go:1466`; new `checkGitHubEventsValid` function |
| GH-EVENT-DOCS | `docs/github-bridge.md` § Phase 115 operator guide | Doc-only; no code findings needed |
| GH-EVENT-E2E | Live UAT: create a throwaway repo → confirm cold-create → prompt runs | Poller bash is invisible to Go goldens — only live UAT can catch parsing bugs |
</phase_requirements>

---

## Summary

Phase 115 adds a second ingress class to the existing `km-github-bridge` Lambda. Today the bridge is entirely human-gated: it only reacts to `issue_comment` events where an allowlisted person @-mentions the bot. This phase introduces a first-match `EventRouter` that fires on autonomous webhook events (`repository`, `push`, `release`, …) by mapping each `(event-type, action, repo-glob)` triple to a prompt that runs in a sandbox.

The design is **genuinely additive**: the `issue_comment` path is byte-identical after this phase. The branch point is `webhook_handler.go:193-197` — a four-line block that currently filters to `issue_comment` only. Phase 115 replaces that with a two-branch switch: `issue_comment` → existing 11-step path unchanged; anything else → `EventRouter`. All downstream machinery (template expansion, `GitHubEnvelope`, `PutSandboxCreate`, FIFO enqueue) is reused without modification.

The config surface (`github.events:`) is decoded atomically by the existing `UnmarshalKey("github", &cfg.Github)` call — no new merge-list entry type is required (same pattern as `github.commands`, `github.peer_bridges`, and `github.default_router`). A single new `"github"` merge-list entry already covers the whole `github:` block. The `KM_GITHUB_EVENTS` env var export follows the identical pattern as `KM_GITHUB_REPOS` in `init.go`.

**Primary recommendation:** Implement `EventRouter` as a pure function in a new `pkg/github/bridge/event_router.go`; add `GithubEventRule` to `GithubConfig` in `internal/app/config/config.go`; insert the event-type branch in `Handle()` after Step 1 (HMAC verify) and Step 7 (delivery GUID dedup, which already runs before the event-type check needs to move); update the poller for `Number==0` tolerance; wire `KM_GITHUB_EVENTS` in `init.go` and the manifest.

---

## Standard Stack

### Core (all already present — no new dependencies)

| Library / Package | Version | Purpose | Why Standard |
|---|---|---|---|
| `path` (stdlib) | Go 1.21+ | Glob matching via `path.Match` | Already used in `resolve.go:61` for `github.repos:` globs |
| `encoding/json` (stdlib) | Go 1.21+ | Marshal `KM_GITHUB_EVENTS` payload + parse in Lambda | Already used throughout bridge |
| `strings` (stdlib) | Go 1.21+ | Template var replacement (`strings.ReplaceAll`) | Already used in `ExpandTemplate` |
| AWS SDK v2 DynamoDB | v1.x | Nonce store for cooldown | Already wired; `DynamoGitHubNonceStore.CheckAndStore` |
| AWS SDK v2 EventBridge | v1.x | Cold-create dispatch | Already used; `EventBridgeAdapter.PutSandboxCreate` |
| AWS SDK v2 SQS | v1.x | Warm dispatch | Already used; `GitHubSQSAdapter.Send` |

**Installation:** No new packages. All dependencies are already present.

---

## Architecture Patterns

### Recommended File Layout

New files:
```
pkg/github/bridge/
├── event_router.go          # GithubEventRule (bridge-local), EventRouter func, ExpandEventTemplate
├── event_router_test.go     # table-driven tests for match/exclude/action/template/cooldown
```

Modified files:
```
pkg/github/bridge/
├── webhook_handler.go       # Insert event-type branch after HMAC verify + dedup
internal/app/config/
├── config.go                # Add GithubEventRule struct + Events []GithubEventRule to GithubConfig
internal/app/cmd/
├── init.go                  # Add KM_GITHUB_EVENTS export block (mirrors KM_GITHUB_REPOS ~line 1624)
├── github.go                # Extend DefaultEvents in RunGitHubManifest (line 109)
├── doctor.go                # Add checkGitHubEventsValid function + wire into GitHub group
pkg/compiler/
├── userdata.go              # Extend km-github-inbound-poller to tolerate Number==0
```

### Pattern 1: Event-type branch in `Handle()` (GH-EVENT-GATING)

**What:** Replace the current 4-line `issue_comment`-only guard at `webhook_handler.go:193-197` with a two-branch switch. The existing path is byte-identical; the new path delegates to `EventRouter`.

**When to use:** This is the only correct seam. HMAC verify (Step 1, line ~180) MUST run before the branch. The delivery GUID dedup (Step 7, line ~335) is currently inside the `issue_comment` path — it must also run for event rules to prevent storm on retries.

**Exact seam:**

```go
// Source: pkg/github/bridge/webhook_handler.go:193-197 (current code to replace)
// eventType := req.Headers["x-github-event"]
// if eventType != "issue_comment" {
//     h.log().Info("github-bridge: ignoring non-issue_comment event", "event", eventType)
//     return WebhookResponse{StatusCode: 200, Body: "ok"}
// }
```

**New structure:**

```go
// Source: pkg/github/bridge/webhook_handler.go (proposed Phase 115 change)
eventType := req.Headers["x-github-event"]

if eventType != "issue_comment" {
    // New branch: autonomous event → EventRouter (if configured)
    if h.EventRouter != nil {
        return h.handleEventRoute(ctx, req, eventType)
    }
    h.log().Info("github-bridge: ignoring non-issue_comment event", "event", eventType)
    return WebhookResponse{StatusCode: 200, Body: "ok"}
}
// existing issue_comment path continues below — byte-identical
```

The `handleEventRoute` helper handles delivery-GUID dedup (reusing `h.Nonces`), parses a minimal generic payload, calls `EventRouter.Match`, expands template, builds envelope, and dispatches.

**Critical ordering:** Dedup must run inside `handleEventRoute` BEFORE routing, using the same `h.Nonces` store and `GitHubDeliveryNoncePrefix`. This prevents a storm of cold-creates on GitHub retries.

### Pattern 2: EventRouter (GH-EVENT-ROUTER)

**What:** Pure function (no IO) that takes a parsed minimal event payload and a `[]EventRule` and returns the first-match rule or `nil`.

```go
// Source: pkg/github/bridge/event_router.go (new file)
type EventRule struct {
    On              string   `json:"on"`
    Actions         []string `json:"actions,omitempty"`
    Match           string   `json:"match"`
    Exclude         []string `json:"exclude,omitempty"`
    Profile         string   `json:"profile,omitempty"`
    Alias           string   `json:"alias,omitempty"`
    Agent           string   `json:"agent,omitempty"`
    CooldownSeconds int      `json:"cooldown_seconds,omitempty"`
    Prompt          string   `json:"prompt"`
}

type EventPayload struct {
    Repo          string // "owner/repo"
    Action        string // e.g. "created"
    Sender        string
    DefaultBranch string
    HTMLURL       string
}

// MatchEventRule returns the first rule whose (on, actions, match, exclude) all pass.
// Resolution: on==eventType AND (actions empty OR action in actions) AND
// path.Match(rule.Match, repo) AND NOT any path.Match(excl, repo).
// Exact match before glob (same two-pass logic as Resolve in resolve.go).
func MatchEventRule(eventType string, payload EventPayload, rules []EventRule) *EventRule {
    // Pass 1: exact match only on rule.Match.
    // Pass 2: glob match, first-wins.
}
```

This is a pure function: exhaustively unit-testable without mocks.

### Pattern 3: Template expansion (GH-EVENT-TEMPLATE)

**What:** `ExpandTemplate` in `commands.go:357` replaces `{{args}}` using `strings.ReplaceAll`. New `ExpandEventTemplate` applies the same simple approach for six named vars.

```go
// Source: pkg/github/bridge/event_router.go (new)
func ExpandEventTemplate(tmpl string, p EventPayload, eventType string) string {
    r := strings.NewReplacer(
        "{{repo}}", p.Repo,
        "{{event}}", eventType,
        "{{action}}", p.Action,
        "{{sender}}", p.Sender,
        "{{default_branch}}", p.DefaultBranch,
        "{{html_url}}", p.HTMLURL,
    )
    return r.Replace(tmpl)
}
```

No `text/template` — same decision as `ExpandTemplate` (single replacer, no ambiguity risk).

### Pattern 4: Envelope construction for event rules (GH-EVENT-DISPATCH)

**What:** `GitHubEnvelope` (`payload.go:75`) is reused with `Number=0`, `Kind=<eventType>`, `CommentID=0`, `Body=<expanded prompt>`, `Agent=rule.Agent`.

**Exact struct fields needed:**

```go
// Source: pkg/github/bridge/payload.go:75
type GitHubEnvelope struct {
    Source        string `json:"source"`         // "github"
    Repo          string `json:"repo"`            // "owner/repo"
    Number        int    `json:"number"`          // 0 for event rules
    Kind          string `json:"kind"`            // eventType e.g. "repository"
    CommentID     int64  `json:"comment_id"`      // 0 for event rules
    HTMLURL       string `json:"html_url"`
    Sender        string `json:"sender"`
    Body          string `json:"body"`            // expanded prompt
    InstallID     string `json:"install_id"`
    DefaultBranch string `json:"default_branch,omitempty"`
    Agent         string `json:"agent,omitempty"` // from rule.Agent
}
```

`Number=0` is an integer zero-value, so `omitempty` on `Number` would SUPPRESS it (it's an `int`, not `*int`). The struct currently lacks `omitempty` on `Number` — this is correct. However the **poller** in `userdata.go:2200` validates `[ -z "$NUMBER" ]` which is false for `0` (bash: `0` is non-empty string). So the poller validation check passes — but `NUMBER=0` means the PR-context preamble with `#$NUMBER` and `pull/${NUMBER}/head` would be wrong. The poller needs a branch on `Kind` to build an event-context preamble instead of a PR-context preamble when `Kind != "issue_comment"`.

### Pattern 5: Cooldown using nonces table (GH-EVENT-COOLDOWN)

**What:** Same `DynamoGitHubNonceStore.CheckAndStore` used for delivery-GUID dedup, but with a longer TTL and a different key prefix.

```go
// Source: derived from aws_adapters.go:200 + orphan_reply.go cooldown pattern
cooldownKey := fmt.Sprintf("gh-event-cooldown:%s:%s:%s", eventType, repo, action)
alreadySeen, err := h.Nonces.CheckAndStore(ctx, cooldownKey, rule.CooldownSeconds)
if err != nil {
    h.log().Error("github-bridge: event cooldown nonce error (fail-open)", "err", err)
    // fail-open: proceed
} else if alreadySeen {
    h.log().Info("github-bridge: event cooldown suppressed", "event", eventType, "repo", repo)
    return WebhookResponse{StatusCode: 200, Body: "ok"}
}
```

The nonces table key format `gh-event-cooldown:{event}:{repo}:{action}` (Claude's discretion) follows the existing `gh-router-cooldown:{channel}` pattern from `orphan_reply.go`. The delivery-GUID dedup MUST run before the cooldown check so GitHub retries don't exhaust cooldown slots.

### Pattern 6: `KM_GITHUB_EVENTS` export in `init.go` (GH-EVENT-CONFIG)

**What:** Mirror the `KM_GITHUB_REPOS` export block at `init.go:1624-1650`. The gate is `len(cfg.Github.Events) > 0`.

```go
// Source: internal/app/cmd/init.go:1624 (KM_GITHUB_REPOS precedent)
if len(cfg.Github.Events) > 0 {
    type githubEventsPayload struct {
        Events []config.GithubEventRule `json:"events"`
    }
    payload := githubEventsPayload{Events: cfg.Github.Events}
    jsonBytes, err := json.Marshal(payload)
    if err == nil {
        yamlGithubEvents := string(jsonBytes)
        if envVal := os.Getenv("KM_GITHUB_EVENTS"); envVal != "" && envVal != yamlGithubEvents {
            fmt.Fprintf(os.Stderr, "WARN: KM_GITHUB_EVENTS=%s (env) overrides km-config.yaml github.events=...\n", envVal)
        } else if envVal == "" {
            os.Setenv("KM_GITHUB_EVENTS", yamlGithubEvents) //nolint:errcheck
        }
    }
}
```

The terragrunt.hcl for `lambda-github-bridge` gains a `get_env("KM_GITHUB_EVENTS", "")` variable, mirroring `KM_GITHUB_REPOS`.

### Pattern 7: `GithubConfig` extension (GH-EVENT-CONFIG)

**What:** Add `Events []GithubEventRule` to `GithubConfig` in `config.go:146`. The existing `UnmarshalKey("github", &cfg.Github)` call at the end of `Load()` decodes it automatically. No new merge-list entry type needed — "github" is already in the merge-list at line ~699.

```go
// Source: internal/app/config/config.go:146 (GithubConfig struct)
type GithubEventRule struct {
    On              string   `mapstructure:"on"               yaml:"on"               json:"on"`
    Actions         []string `mapstructure:"actions"          yaml:"actions,omitempty" json:"actions,omitempty"`
    Match           string   `mapstructure:"match"            yaml:"match"            json:"match"`
    Exclude         []string `mapstructure:"exclude"          yaml:"exclude,omitempty" json:"exclude,omitempty"`
    Profile         string   `mapstructure:"profile"          yaml:"profile,omitempty" json:"profile,omitempty"`
    Alias           string   `mapstructure:"alias"            yaml:"alias,omitempty"  json:"alias,omitempty"`
    Agent           string   `mapstructure:"agent"            yaml:"agent,omitempty"  json:"agent,omitempty"`
    CooldownSeconds int      `mapstructure:"cooldown_seconds" yaml:"cooldownSeconds,omitempty" json:"cooldown_seconds,omitempty"`
    Prompt          string   `mapstructure:"prompt"           yaml:"prompt"           json:"prompt"`
}

// In GithubConfig, add:
Events []GithubEventRule `mapstructure:"events" yaml:"events,omitempty" json:"events,omitempty"`
```

All fields carry `mapstructure` tags — required by viper's `UnmarshalKey`; untagged fields are silently ignored (the known footgun from `GithubCommandEntry` doc at `config.go:112`).

### Pattern 8: `DefaultRouter`/`EventRouter` field on `WebhookHandler`

**What:** Add `EventRouter []EventRule` to `WebhookHandler`. When nil/empty, the handler is byte-identical (dormant-by-default). Populated at cold-start from `KM_GITHUB_EVENTS`.

```go
// Source: pkg/github/bridge/webhook_handler.go:74 (WebhookHandler struct)
// EventRules is the parsed github.events config (set at cold-start from KM_GITHUB_EVENTS).
// When nil or empty, Handle() is byte-identical to Phase 114 for all non-issue_comment events.
EventRules []EventRule
```

### Pattern 9: Lambda cold-start wiring in `cmd/km-github-bridge/main.go`

**What:** Mirror the `KM_GITHUB_REPOS` parse block at `main.go:106-127`. Parse `KM_GITHUB_EVENTS` JSON into `[]bridge.EventRule`.

```go
// Source: cmd/km-github-bridge/main.go:106 (KM_GITHUB_REPOS precedent)
var eventRules []bridge.EventRule
if raw := os.Getenv("KM_GITHUB_EVENTS"); raw != "" {
    var ecfg struct {
        Events []bridge.EventRule `json:"events"`
    }
    if err := json.Unmarshal([]byte(raw), &ecfg); err != nil {
        slog.Warn("km-github-bridge: failed to parse KM_GITHUB_EVENTS; event routing dormant", "err", err)
    } else {
        eventRules = ecfg.Events
        slog.Info("km-github-bridge: loaded event routing config", "rule_count", len(eventRules))
    }
}
// Wire: webhookHandler.EventRules = eventRules
```

### Pattern 10: Manifest extension (GH-EVENT-MANIFEST)

**What:** `RunGitHubManifest` at `github.go:87` hardcodes `DefaultEvents: []string{"issue_comment"}` at line ~109. Extend to merge in event types from `cfg.Github.Events`.

```go
// Source: internal/app/cmd/github.go:109
// Current:
DefaultEvents: []string{"issue_comment"},

// Phase 115 — derive union from configured rules + always include issue_comment:
eventsSet := map[string]bool{"issue_comment": true}
for _, rule := range cfg.Github.Events {
    if rule.On != "" {
        eventsSet[rule.On] = true
    }
}
// Also add implied permissions: "repository" event needs metadata:read
if eventsSet["repository"] {
    payload.DefaultPermissions["metadata"] = "read"
}
```

### Pattern 11: Poller tolerance for `Number==0` (GH-EVENT-POLLER)

**What:** The poller at `userdata.go:2200` validates `[ -z "$REPO" ] || [ -z "$NUMBER" ]`. For event envelopes, `NUMBER=0`. In bash, `0` is a non-empty string so the check passes. But the PR-context preamble references `#$NUMBER` and `pull/${NUMBER}/head` — wrong for non-PR events.

The poller must branch on `KIND` (parsed from envelope):

```bash
# Source: pkg/compiler/userdata.go:~2188 (poller envelope parse)
# Add:
KIND=$(echo "$BODY" | jq -r '.kind // empty')

# Then replace the fixed PR-context preamble:
if [ "$KIND" = "issue_comment" ]; then
    PREAMBLE="[GitHub Comment Trigger]
Repository: $REPO
PR: #$NUMBER
..."
else
    PREAMBLE="[GitHub Event Trigger]
Repository: $REPO
Event: $KIND / $ACTION
Sender: $SENDER
URL: $HTML_URL

--- Task ---
$COMMENT_BODY"
fi
```

The session continuity DDB lookup (`km-github-threads` by `repo`+`number`) also uses `NUMBER`. For event envelopes with `Number=0`, a repo can have multiple rules firing at different times — per-rule continuity keyed on `(repo, 0)` would incorrectly merge sessions. The simplest approach: skip the DDB session-continuity lookup when `NUMBER=0` (treat every event-rule dispatch as a fresh session). Document this as a known limitation — event-rule sandboxes can still be `alias:` long-lived, just without cross-event session continuity.

The `[ -z "$REPO" ] || [ -z "$NUMBER" ]` guard must be loosened to `[ -z "$REPO" ]` — `NUMBER` can be `0` for event envelopes.

### Anti-Patterns to Avoid

- **Shared event-rule and issue_comment dedup key prefix:** Always use a distinct prefix (`gh-event-cooldown:`) so cooldown keys never collide with delivery-GUID dedup (`github-delivery:`) or orphan cooldown (`gh-router-cooldown:`).
- **Running dedup AFTER rule match:** Dedup the delivery GUID BEFORE calling `MatchEventRule` — otherwise a GitHub retry with a new GUID bypasses the cooldown if no rule matched on the first delivery.
- **Widening `GitHubEnvelope.Number` to `*int`:** The struct is serialized to JSON and parsed by the poller bash script. Changing the type would break the existing wire format for issue_comment envelopes. Use `0` and branch on `Kind` instead.
- **Adding a separate "github.events" merge-list entry:** The existing `"github"` merge-list entry at `config.go:699` decodes the whole `github:` block atomically via `UnmarshalKey("github", &cfg.Github)`. Adding a sibling `"github.events"` entry is a no-op or causes parse-order issues (the `GithubCommandEntry` doc explicitly warns about this at `config.go:112-117`).

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Glob matching for `match:`/`exclude:` | Custom wildcard code | `path.Match` from stdlib (already used in `resolve.go:61`) | Correct `*`, `?`, `[...]` semantics; already proven in `github.repos:` globs |
| Template expansion for event vars | `text/template` engine | Simple `strings.NewReplacer` (same pattern as `ExpandTemplate` in `commands.go:357`) | No injection risk; six fixed vars; `text/template` parses `{{` as directives which would break existing prompt text |
| Per-(event,repo,action) cooldown | Custom TTL store | `DynamoGitHubNonceStore.CheckAndStore` with prefix `gh-event-cooldown:` | Conditional PutItem = atomic check-and-set; already battle-tested in delivery dedup and orphan cooldown |
| Delivery dedup for event webhooks | Separate nonce table | Reuse the existing nonces table (`DynamoGitHubNonceStore`) with `github-delivery:` prefix | Same table already used for issue_comment delivery dedup; no new infra |
| Envelope serialization | New wire format | Reuse `GitHubEnvelope` JSON with `Number=0`, `Kind=<eventType>` | Poller already parses this struct; minimal change to tolerate `Number==0` |

---

## Common Pitfalls

### Pitfall 1: Dedup vs cooldown ordering
**What goes wrong:** If delivery-GUID dedup runs AFTER rule matching, a GitHub retry with a NEW `X-GitHub-Delivery` header (GitHub generates new GUIDs on retries) bypasses the cooldown window for the same real-world event.
**Why it happens:** Forgetting that GitHub retries use fresh GUIDs.
**How to avoid:** ALWAYS run delivery-GUID dedup first (Step 7 equivalent), then cooldown, then route.
**Warning signs:** Multiple cold-creates for the same `repository`/`created` event.

### Pitfall 2: `github.events` merge-list double-entry
**What goes wrong:** Adding `"github.events"` as a separate key in the v2→v merge-list causes no error but either no-ops or causes a parse-order race (the `GithubCommandEntry` doc at `config.go:112-117` documents this explicitly).
**Why it happens:** Cargo-culting from `slack.mention_only` which uses a dotted sub-key, without noticing that `github` is a struct decoded via `UnmarshalKey` (not a flat scalar).
**How to avoid:** The existing `"github"` entry covers all sub-fields of `GithubConfig`. Do NOT add `"github.events"`.
**Warning signs:** `km github status` shows zero event rules despite `km-config.yaml` having entries.

### Pitfall 3: Poller `Number==0` validation failure
**What goes wrong:** The current poller guard `[ -z "$REPO" ] || [ -z "$NUMBER" ]` passes for `NUMBER=0` (bash: `"0"` is non-empty). But the PR preamble emits `PR: #0` and `pull/0/head` — confusing the agent.
**Why it happens:** The poller was written for PR-only context and `Number` was always ≥1.
**How to avoid:** Parse `KIND` from envelope, branch preamble on `KIND`. Relax validation to `[ -z "$REPO" ]` only. Skip DDB session-continuity lookup when `NUMBER==0`.
**Warning signs:** Agent tries to `git fetch origin pull/0/head` and fails.

### Pitfall 4: `KM_GITHUB_EVENTS` env var not updating
**What goes wrong:** Running `km init --sidecars` instead of `km init --github` does NOT update the Lambda env block (`KM_GITHUB_EVENTS`). The Lambda keeps serving stale event rules (or none).
**Why it happens:** `--sidecars` rebuilds binary zips but does NOT run terragrunt apply, so the Lambda environment variables block is not updated. This is identical to the `KM_GITHUB_REPOS` footgun documented in `project_km_init_lambdas_doesnt_deploy.md`.
**How to avoid:** Always `make build-lambdas` + `km init --github` (or `--dry-run=false`) after changing `github.events:` in km-config.yaml.
**Warning signs:** `km github status` shows events configured but the Lambda logs show `KM_GITHUB_EVENTS not set; event routing dormant`.

### Pitfall 5: GitHub App not subscribed to new event types
**What goes wrong:** Adding `on: repository` to `github.events:` without regenerating the App manifest and re-installing means GitHub never sends `repository` events to the bridge Function URL.
**Why it happens:** The GitHub App's webhook subscriptions are set at install time from the manifest; updating the manifest JSON requires re-installing (or manually editing the App settings).
**How to avoid:** After Phase 115, always run `km github manifest` → update GitHub App settings → re-install to subscribe to the new event types. `km doctor` should warn when `github.events` is configured but the App's subscribed events don't include those types (though live App subscription check requires a GitHub API call — the check is advisory).
**Warning signs:** `repository`/`created` webhooks never arrive; the bridge logs nothing.

### Pitfall 6: Terragrunt `github-bridge` module not re-applied
**What goes wrong:** The `lambda-github-bridge` terragrunt module's `terragrunt.hcl` needs a new `get_env("KM_GITHUB_EVENTS", "")` variable. If the module is not edited before `km init`, the env var is never passed to the Lambda even if the init exports it.
**Why it happens:** The pattern used by `KM_GITHUB_REPOS` requires both the `init.go` export AND the `terragrunt.hcl` `get_env()` call. Missing one half silently drops the value.
**How to avoid:** Edit `infra/live/use1/lambda-github-bridge/terragrunt.hcl` to add `github_events = get_env("KM_GITHUB_EVENTS", "")` and wire it through the module variable to the Lambda environment. This is the same two-file change done for `KM_GITHUB_REPOS` (init.go export + terragrunt.hcl get_env).

---

## Code Examples

### Current Handle() event-type branch (to be replaced)

```go
// Source: pkg/github/bridge/webhook_handler.go:192-197
// Only process issue_comment events (the X-GitHub-Event header).
eventType := req.Headers["x-github-event"]
if eventType != "issue_comment" {
    h.log().Info("github-bridge: ignoring non-issue_comment event", "event", eventType)
    return WebhookResponse{StatusCode: 200, Body: "ok"}
}
```

### Resolve() pattern (exact-before-glob first-match, to be replicated in EventRouter)

```go
// Source: pkg/github/bridge/resolve.go:51-70
func Resolve(fullName string, entries []RepoEntry, defaultProfile string) (alias, profile string, allow []string, matched bool) {
    // Pass 1: exact matches only.
    for _, e := range entries {
        if e.Match == fullName {
            return buildResult(fullName, e, defaultProfile)
        }
    }
    // Pass 2: glob matches, first-wins.
    for _, e := range entries {
        if isGlob(e.Match) {
            ok, err := path.Match(e.Match, fullName)
            if err == nil && ok {
                return buildResult(fullName, e, defaultProfile)
            }
        }
    }
    return "", "", nil, false
}
```

### ExpandTemplate (model for ExpandEventTemplate)

```go
// Source: pkg/github/bridge/commands.go:357-367
func ExpandTemplate(template, args string) string {
    const placeholder = "{{args}}"
    if strings.Contains(template, placeholder) {
        return strings.ReplaceAll(template, placeholder, args)
    }
    if args == "" {
        return template
    }
    return template + "\n" + args
}
```

### Nonce CheckAndStore (cooldown reuse)

```go
// Source: pkg/github/bridge/aws_adapters.go:200-221
func (s *DynamoGitHubNonceStore) CheckAndStore(ctx context.Context, key string, ttlSeconds int) (bool, error) {
    ttlExpiry := time.Now().Unix() + int64(ttlSeconds)
    _, err := s.Client.PutItem(ctx, &dynamodb.PutItemInput{
        TableName: awssdk.String(s.TableName),
        Item: map[string]dynamodbtypes.AttributeValue{
            "nonce": &dynamodbtypes.AttributeValueMemberS{Value: key},
            "ttl_expiry": &dynamodbtypes.AttributeValueMemberN{
                Value: strconv.FormatInt(ttlExpiry, 10),
            },
        },
        ConditionExpression: awssdk.String("attribute_not_exists(nonce)"),
    })
    // ConditionalCheckFailedException → already seen (true, nil)
}
```

### KM_GITHUB_REPOS export pattern in init.go (to mirror for KM_GITHUB_EVENTS)

```go
// Source: internal/app/cmd/init.go:1624-1650
if len(cfg.Github.Repos) > 0 {
    type githubExportPayload struct {
        Repos          []config.GithubRepoEntry `json:"repos"`
        DefaultProfile string                   `json:"default_profile,omitempty"`
    }
    payload := githubExportPayload{...}
    jsonBytes, _ := json.Marshal(payload)
    yamlGithubRepos := string(jsonBytes)
    if envVal := os.Getenv("KM_GITHUB_REPOS"); envVal != "" && envVal != yamlGithubRepos {
        fmt.Fprintf(os.Stderr, "WARN: KM_GITHUB_REPOS=... (env) overrides km-config.yaml\n")
    } else if envVal == "" {
        os.Setenv("KM_GITHUB_REPOS", yamlGithubRepos)
    }
}
```

### checkGitHubCommandsValid doctor pattern (model for checkGitHubEventsValid)

```go
// Source: internal/app/cmd/doctor.go:1466-1524
func checkGitHubCommandsValid(
    commands map[string]appcfg.GithubCommandEntry,
    defaultCommand string, repos []appcfg.GithubRepoEntry,
    defaultProfile string, configDir string,
) CheckResult {
    name := "GitHub Commands Config"
    if len(commands) == 0 {
        return CheckResult{Name: name, Status: CheckSkipped,
            Message: "no github.commands configured — skipping command validation"}
    }
    var warnings []string
    // 1. @file prompts exist; 2. Profile resolvable; 3. Reserved tokens; 4. default_command ref
    // Returns SKIPPED/WARN/ERROR
}
```

### Mock pattern from handle_test.go (model for event_router_test.go)

```go
// Source: pkg/github/bridge/handle_test.go:22-57
type mockNonceStore struct {
    seen map[string]bool
    err  error
}
func (m *mockNonceStore) CheckAndStore(_ context.Context, key string, _ int) (bool, error) {
    if m.err != nil { return false, m.err }
    if m.seen[key] { return true, nil }
    m.seen[key] = true
    return false, nil
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|---|---|---|---|
| issue_comment only | issue_comment only (Phase 115 adds event routing) | Phase 97 → Phase 115 | Phase 115 is the first time non-comment events are handled |
| Manual `km create` for new repo onboarding | Automated event-driven sandbox cold-create | Phase 115 | Zero-human-touch new-repo onboarding |
| All `github.repos:` globs use `path.Match` | Same `path.Match` reused for `github.events:` match/exclude | Phase 97 (established) → Phase 115 (extended) | Consistent glob semantics across both config blocks |

**No deprecated patterns in this change.** Phase 115 is purely additive.

---

## Open Questions

1. **Minimal generic payload struct for non-`issue_comment` events**
   - What we know: `repository`/`created` has `repository.full_name`, `repository.default_branch`, `sender.login`, `action`. `push` has `repository` + `ref` + `sender`. The subset needed (repo, action, sender, default_branch, html_url) varies.
   - What's unclear: Whether to parse a `GenericEventPayload` with optional fields, or use `json.RawMessage` + targeted extraction per event type.
   - Recommendation: Use a single `GenericEventPayload` struct with the union of needed fields (all optional): `Repository RepositoryField`, `Action string`, `Sender *UserField`, `Installation InstallField`. The existing `RepositoryField` (already in `payload.go:54`) provides `FullName` and `DefaultBranch`. `HTMLURL` for `repository`/`created` is at `repository.html_url` — add it to `RepositoryField`. This avoids per-event-type branching in the parse step.

2. **Whether `km github manifest` derives events from config or a hardcoded list**
   - What we know: CONTEXT.md says this is Claude's discretion. The current manifest hardcodes `["issue_comment"]`.
   - What's unclear: Config-derived is more correct (manifest always matches config) but requires `cfg` to be threaded into `RunGitHubManifest` (it already takes `cfg *config.Config`). Hardcoded list requires manual bumping per phase.
   - Recommendation: Config-derived union. `cfg.Github.Events` is already available; iterate to collect `on:` values and merge with `"issue_comment"`. This is the correct long-term behavior.

3. **`RepositoryField.HTMLURL` field addition**
   - What we know: `RepositoryField` in `payload.go:54` currently only has `FullName` and `DefaultBranch`. The `{{html_url}}` template var needs `repository.html_url` from the webhook payload.
   - What's unclear: Whether `html_url` is at `repository.html_url` or elsewhere for all event types.
   - Recommendation: Add `HTMLURL string \`json:"html_url"\`` to `RepositoryField`. For `repository`/`created`, GitHub sends it at the `repository` level. For `push`, it is also `repository.html_url`. This covers the common cases.

---

## Validation Architecture

> `workflow.nyquist_validation` is `true` in `.planning/config.json` — this section is required.

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` package (standard) |
| Config file | None (no separate config file) |
| Quick run command | `go test ./pkg/github/bridge/... -run TestEventRouter -timeout 30s` |
| Full suite command | `go test ./pkg/github/bridge/... ./internal/app/cmd/... ./internal/app/config/... -timeout 600s -count=1` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| GH-EVENT-CONFIG | `GithubEventRule` loads from yaml via viper UnmarshalKey | unit | `go test ./internal/app/config/... -run TestLoad.*Github -timeout 30s` | ❌ Wave 0 — `config_test.go` needs new test cases |
| GH-EVENT-ROUTER | `MatchEventRule`: exact-before-glob, first-match, action filter, exclude globs | unit | `go test ./pkg/github/bridge/... -run TestMatchEventRule -timeout 30s` | ❌ Wave 0 — `event_router_test.go` |
| GH-EVENT-GATING | `Handle()` dispatches to `handleEventRoute` when EventRules non-empty, drops when empty | unit | `go test ./pkg/github/bridge/... -run TestHandle.*EventRoute -timeout 30s` | ❌ Wave 0 — `webhook_handler_phase115_test.go` |
| GH-EVENT-TEMPLATE | `ExpandEventTemplate` replaces all six vars; unknown vars left as-is | unit | `go test ./pkg/github/bridge/... -run TestExpandEventTemplate -timeout 30s` | ❌ Wave 0 — `event_router_test.go` |
| GH-EVENT-DISPATCH | `handleEventRoute` calls `PutSandboxCreate` for no-alias cold path; `SQS.Send` for alias warm path | unit | `go test ./pkg/github/bridge/... -run TestHandle.*EventDispatch -timeout 30s` | ❌ Wave 0 — `webhook_handler_phase115_test.go` |
| GH-EVENT-COOLDOWN | Cooldown gate: second delivery within window returns 200 without dispatch | unit | `go test ./pkg/github/bridge/... -run TestHandle.*Cooldown -timeout 30s` | ❌ Wave 0 — `webhook_handler_phase115_test.go` |
| GH-EVENT-MANIFEST | `RunGitHubManifest` includes `repository` in `default_events` when configured | unit | `go test ./internal/app/cmd/... -run TestRunGitHubManifest -timeout 30s` | ❌ Wave 0 — extend `github_test.go` |
| GH-EVENT-POLLER | Poller tolerates `Number==0`; builds event preamble when `Kind != "issue_comment"` | manual-only | N/A — bash userdata is invisible to Go unit tests | N/A |
| GH-EVENT-DOCTOR | `checkGitHubEventsValid`: SKIP when empty, WARN on bad glob/missing profile, WARN on reserved event type | unit | `go test ./internal/app/cmd/... -run TestCheckGitHubEventsValid -timeout 30s` | ❌ Wave 0 — extend `doctor_test.go` or new file |
| GH-EVENT-DOCS | `docs/github-bridge.md` § Phase 115 section present | manual-only | N/A | N/A |
| GH-EVENT-E2E | Create throwaway repo → bridge cold-creates → prompt runs to completion | manual-only | Live UAT: `km create profiles/github-review.yaml --alias gh-e2e` + create repo in org | N/A |

### Sampling Rate

- **Per task commit:** `go test ./pkg/github/bridge/... -run TestEventRouter -timeout 30s`
- **Per wave merge:** `go test ./pkg/github/bridge/... ./internal/app/cmd/... ./internal/app/config/... -timeout 600s -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/github/bridge/event_router_test.go` — covers GH-EVENT-ROUTER + GH-EVENT-TEMPLATE
- [ ] `pkg/github/bridge/webhook_handler_phase115_test.go` — covers GH-EVENT-GATING, GH-EVENT-DISPATCH, GH-EVENT-COOLDOWN
- [ ] `internal/app/config/config_test.go` additions — covers GH-EVENT-CONFIG (load + merge-list)
- [ ] `internal/app/cmd/doctor_test.go` additions (or new file) — covers GH-EVENT-DOCTOR

**GH-EVENT-POLLER and GH-EVENT-E2E are manual-only** (userdata bash + live GitHub webhook). Flag in UAT checklist.

---

## Sources

### Primary (HIGH confidence)

- Direct source reading: `pkg/github/bridge/webhook_handler.go` — full Handle() flow, exact HMAC verify + dedup + branch point at lines 179-197
- Direct source reading: `pkg/github/bridge/commands.go:357` — `ExpandTemplate` signature
- Direct source reading: `pkg/github/bridge/resolve.go:51-70` — `Resolve()` exact-before-glob first-match pattern
- Direct source reading: `pkg/github/bridge/payload.go:75` — `GitHubEnvelope` exact fields
- Direct source reading: `pkg/github/bridge/aws_adapters.go:200-221,445` — `CheckAndStore` + `PutSandboxCreate`
- Direct source reading: `internal/app/config/config.go:118-196,646-714` — `GithubConfig`, `GithubCommandEntry`, merge-list
- Direct source reading: `cmd/km-github-bridge/main.go:106-127,263-293` — env var parsing + `WebhookHandler` wiring
- Direct source reading: `internal/app/cmd/init.go:1624-1686` — `KM_GITHUB_REPOS`/`KM_GITHUB_PEER_BRIDGES`/`KM_GITHUB_DEFAULT_ROUTER` export pattern
- Direct source reading: `internal/app/cmd/github.go:87-128` — `RunGitHubManifest`, `DefaultEvents`
- Direct source reading: `internal/app/cmd/doctor.go:922-999,1136-1180,1445-1524` — GitHub doctor check patterns
- Direct source reading: `pkg/github/bridge/handle_test.go` — mock patterns for test doubles
- Direct source reading: `pkg/compiler/userdata.go:2175-2460` — poller envelope parsing + NUMBER validation

### Secondary (MEDIUM confidence)

- CONTEXT.md decisions (operator-brainstormed design spec) — all design decisions
- `docs/superpowers/specs/2026-06-15-github-webhook-event-router-design.md` — full design spec

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries already present and in use
- Architecture patterns: HIGH — derived from reading actual source, not documentation
- Pitfalls: HIGH — derived from in-code comments (footgun notes in config.go:112-117, project memory entries in CLAUDE.md)

**Research date:** 2026-06-15
**Valid until:** 2026-07-15 (stable codebase, 30-day window)
