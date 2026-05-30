# Phase 91: Slack inbound @-mention-only mode (polite-bot) — Research

**Researched:** 2026-05-30
**Domain:** Slack bridge events handler, profile schema, compiler env-var emission, km doctor
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **Mode defaults (locked):**
  - Mode 1 (shared, e.g. `#km-notifications`): default `true` → mention-only.
  - Mode 2 (per-sandbox `#sb-{id}`): default `false` → every-message (current behaviour).
  - Mode 3 (operator override channel): default `true` → mention-only.
  - Operator can flip any mode by setting `cli.notifySlackInboundMentionOnly` explicitly.

- **Profile field shape (locked):**
  - Name: `cli.notifySlackInboundMentionOnly` (camelCase, matches siblings).
  - Type: `*bool` (tri-state pointer: nil = inherit mode-derived default; true = force polite; false = force chatty).
  - Default: nil — let the channel mode drive the effective value.
  - Schema: optional `boolean` (no enum, no default in JSON schema — Go side handles tri-state).

- **Mention detection (locked):**
  - Scan `event.text` for `<@{bot_user_id}>` substring. No display-name fallback.
  - Bridge events handler performs this check; out-of-scope: display-name-typed mentions.

- **`bot_user_id` persistence (locked):**
  - SSM path: `{prefix}slack/bot-user-id`.
  - Captured during `km slack init` from `auth.test` response.
  - Prefer compile-time injection via env var so Lambda has no extra SSM round-trip.

- **Compiler env var (locked):**
  - Name: `KM_SLACK_MENTION_ONLY` (string `"true"`/`"false"`).
  - Emitted into the bridge Lambda env via the Terraform module.
  - Path: same place existing bridge env vars are written (Terraform variables + `main.tf` environment block).

- **`km doctor` check (locked):**
  - New check: `slack_bot_user_id_cached` (or similar, consistent with neighbours).
  - Trigger: WARN if at least one local profile resolves to `mention_only=true` AND `{prefix}slack/bot-user-id` SSM parameter is missing/empty.
  - Severity: WARN (matches `slack_users_read_email_scope`).
  - Suggested fix in doctor output: `km slack init --force`.

- **Reuse from Phase 72 (locked):**
  - Reuse channel-mode dispatch logic from `create_slack.go` `resolveSlackChannel()`.
  - Reuse `auth.test` call shape from 72-01 for `bot_user_id` capture at `km slack init`.

- **`km init --sidecars` (locked):**
  - Schema additions require `km init --sidecars` for management Lambdas to pick up the change.
  - Existing sandboxes need `km destroy && km create` to pick up the new field.

- **Documentation (locked):**
  - Add Phase 91 section to `docs/slack-notifications.md`.
  - Update `CLAUDE.md` § Slack for polite-bot mode and `KM_SLACK_MENTION_ONLY`.
  - Update `OPERATOR-GUIDE.md` where appropriate.

- **Tests (locked — derive specifics during planning):**
  - Unit test: profile resolution (Mode 1/2/3 × nil/true/false = 9 cases).
  - Compiler test: `KM_SLACK_MENTION_ONLY` emitted correctly.
  - Bridge handler test: `<@{bot_user_id}>` substring scan — positive, negative, edge cases.

### Claude's Discretion

- File-level breakdown of plans (recommended: schema/types → compiler → bridge handler → SSM caching → doctor check → docs/tests).
- Specific Go struct/function names — follow existing conventions.
- Exact location of `KM_SLACK_MENTION_ONLY` emission.
- Whether bot_user_id is injected at compile-time vs Lambda-runtime read — CONTEXT.md prefers compile-time injection, but permits SSM read on cold-start if simpler.
- Naming of the new `km doctor` check.
- Plan/wave granularity.

### Deferred Ideas (OUT OF SCOPE)

- Per-channel runtime overrides via slash command.
- Display-name mention detection.
- Reactions-as-actions integration.
- Backward-compat shim for existing sandboxes (they need `km destroy && km create`).
</user_constraints>

<phase_requirements>
## Phase Requirements

Phase 91 derives synthetic IDs following the Phase 84.2/84.3/89 precedent. These are assigned here for plan-checker traceability.

| ID | Description | Research Support |
|----|-------------|-----------------|
| POL-01 | Profile schema: `spec.cli.notifySlackInboundMentionOnly *bool` field added to `CLISpec` in `pkg/profile/types.go` | types.go CLISpec struct confirmed; `*bool` pattern matches `NotifyEmailEnabled`, `NotifySlackEnabled`, `UseSlackConnect`, `VSCodeEnabled` |
| POL-02 | JSON Schema: `sandbox_profile.schema.json` gains optional `boolean` for `notifySlackInboundMentionOnly` (no default — tri-state lives on Go side) | Schema file confirmed; `notifySlackInviteEmails` / `useSlackConnect` are the immediate precedent |
| POL-03 | Validation: `pkg/profile/validate.go` accepts the new field; no semantic reject rules needed (any bool value is valid) | Validator already handles all `cli.*` fields; Phase 91 field is purely optional with no cross-field constraints |
| POL-04 | Compiler: `pkg/compiler/userdata.go` resolves effective `mention_only` bool (mode-derived default OR profile override) and emits `KM_SLACK_MENTION_ONLY=true|false` into `params.NotifyEnv` | `notifyEnv` map confirmed in `userdata.go:3940+`; existing `KM_NOTIFY_SLACK_ENABLED`, `KM_AGENT` etc. are emitted in the same block |
| POL-05 | Lambda Terraform: `infra/modules/lambda-slack-bridge/v1.0.0/` gains a `KM_SLACK_MENTION_ONLY` env variable in `main.tf` environment block and a matching variable in `variables.tf` | Terraform env block at `main.tf:307-326` confirmed; pattern is established |
| POL-06 | Bridge handler: `pkg/slack/bridge/events_handler.go` `EventsHandler` struct gains `MentionOnly bool` and `BotUserIDForMentionFilter string` (or reads from `BotUserID` fetcher); `Handle()` inserts a mention-only guard between step 4 (bot-loop filter) and step 5 (dedup) | `events_handler.go` fully read; exact insertion point identified |
| POL-07 | `km slack init`: `RunSlackInit` calls `auth.test` (already called), captures `bot_user_id` from the response body, and writes `{prefix}slack/bot-user-id` to SSM (plain string, not SecureString) | `pkg/slack/client.go AuthTest` currently discards response; needs a new `AuthTestWithUserID() (string, error)` method on `Client` and a matching addition to `SlackInitAPI`; `RunSlackInit` at `slack.go:211` is the insertion point |
| POL-08 | `km slack rotate-token`: existing `runSlackRotateToken` must also re-capture `bot_user_id` after the new token is validated and write it to `{prefix}slack/bot-user-id` | `runSlackRotateToken` confirmed in `slack.go`; same pattern as token write |
| POL-09 | Bridge Lambda cold-start: reads `KM_SLACK_BOT_USER_ID` env var (injected by Terraform from SSM parameter); injects value into `EventsHandler` at wire-up time in `wireEventsHandler()` (no extra SSM round-trip for mention filtering) | `wireEventsHandler()` in `cmd/km-slack-bridge/main.go:204+` is the wiring point; `KM_SLACK_MENTION_ONLY` + `KM_SLACK_BOT_USER_ID` are new env vars to read here |
| POL-10 | `km doctor` check: `checkSlackBotUserIDCached` (WARN) fires when mention-only is effective for any local profile and `{prefix}slack/bot-user-id` SSM param is missing/empty | `doctor_slack_transcript.go` contains `checkSlackUsersReadEmailScope` — exact same shape/file to extend |
| POL-11 | Test: 9-case table-driven unit test in `pkg/profile/` or `pkg/compiler/` covering (Mode 1/2/3) × (nil/true/false override) for effective mention-only resolution | Precedent: `notify_hook_test.go` table-driven approach |
| POL-12 | Test: bridge handler tests covering mention-scan logic (`<@UBOT123>` in text → pass, `<@OTHER>` → skip if mention-only, no-mention → skip if mention-only, mention-only=false → always pass) | `events_handler_test.go` confirmed as table-driven with `fakeSigningSecret`, `fakeBotUserID`, etc. |
| POL-13 | Docs: Phase 91 section in `docs/slack-notifications.md`; `CLAUDE.md` § Slack updated; `OPERATOR-GUIDE.md` mention-only reference | Confirmed `docs/slack-notifications.md` has Phase 72 section as template |
</phase_requirements>

---

## Summary

Phase 91 is a targeted surgical change across five layers: (1) a `*bool` field added to the profile schema, (2) a channel-mode resolver in the compiler that derives and emits `KM_SLACK_MENTION_ONLY` into the bridge's env, (3) the bridge events handler grows a two-line mention-scan early-return between its bot-loop filter (step 4) and dedup (step 5), (4) `km slack init` / `km slack rotate-token` learn to cache `bot_user_id` in SSM and the Terraform module injects it as `KM_SLACK_BOT_USER_ID` at deploy time, and (5) `km doctor` grows a WARN check for a stale cache.

The bridge already has a `BotUserIDFetcher` (`CachedBotUserIDFetcher`) that calls `auth.test` live on cold-start. Phase 91 does NOT remove that fetcher (it is still needed for the bot-loop filter in `isBotLoop()`). The mention-only guard can reuse the same fetcher. The only new SSM write is to allow `km doctor` to verify the cache without requiring a live Lambda cold-start.

**Primary recommendation:** Add the mention-scan guard in `EventsHandler.Handle()` between steps 4 and 5 using the existing `BotUserID` fetcher. Add `MentionOnly bool` field to `EventsHandler`. Emit `KM_SLACK_MENTION_ONLY` from the compiler's existing `notifyEnv` map. Inject `KM_SLACK_BOT_USER_ID` (from new SSM write at `km slack init`) into the Lambda via Terraform env block so the mention check has a compile-time-resolved value without a cold-start SSM round-trip. The `CachedBotUserIDFetcher` in `EventsHandler.BotUserID` is still used by `isBotLoop()` and can also be used by the mention scan—but `KM_SLACK_BOT_USER_ID` read at cold-start provides the value without any Slack API call.

---

## Standard Stack

### Core (already in use — no new dependencies)

| Library / Component | Version | Purpose | Why Standard |
|---|---|---|---|
| `pkg/slack/bridge/events_handler.go` | in-repo | Bridge events dispatch | Confirmed location of Handle() |
| `pkg/compiler/userdata.go` | in-repo | Emits `notifyEnv` map | Confirmed emit point |
| `internal/app/cmd/slack.go` | in-repo | `km slack init` RunSlackInit | Confirmed step 2 insertion |
| `infra/modules/lambda-slack-bridge/v1.0.0/` | in-repo | Lambda Terraform module | Confirmed env-var emission point |
| `internal/app/cmd/doctor_slack_transcript.go` | in-repo | Slack scope doctor checks | Confirmed home for new check |

### No new external dependencies

Phase 91 is pure Go + existing SSM/Terraform patterns. No new packages.

---

## Architecture Patterns

### Recommended Plan Structure

```
91-00-PLAN.md  Wave 0: test scaffolding (tables for profile resolution + bridge handler test stubs)
91-01-PLAN.md  Schema + types: CLISpec.NotifySlackInboundMentionOnly *bool; JSON Schema; validate.go accept
91-02-PLAN.md  Compiler: resolve effective mention-only from mode+override; emit KM_SLACK_MENTION_ONLY
91-03-PLAN.md  Bridge handler: MentionOnly field on EventsHandler; mention-scan guard in Handle()
91-04-PLAN.md  SSM caching: km slack init + rotate-token write bot_user_id; Terraform injects KM_SLACK_BOT_USER_ID
91-05-PLAN.md  km doctor: checkSlackBotUserIDCached (WARN) in doctor_slack_transcript.go
91-06-PLAN.md  Documentation + UAT
```

### Pattern 1: Mention-Scan Guard Insertion Point

The correct insertion point is **between step 4 (bot-loop filter) and step 5 (dedup)** in `EventsHandler.Handle()`. This is critical: dedup fires BEFORE the mention check by default (to prevent Slack retry storms). Moving the mention check before dedup means non-mention messages skip dedup entirely and are silently dropped — which is the correct behaviour (we want to be invisible on non-mention messages, not consume a nonce slot).

```go
// Source: pkg/slack/bridge/events_handler.go Handle() circa line 162-178
// PROPOSED insertion after step 4:

// 4b. Mention-only filter (Phase 91)
// When h.MentionOnly is true (derived from KM_SLACK_MENTION_ONLY at cold-start),
// skip processing unless event.text contains <@{bot_user_id}>.
// The BotUserID fetcher is already wired for the isBotLoop check above.
if h.MentionOnly {
    uid, err := h.BotUserID.Fetch(ctx)
    if err != nil {
        h.log().Warn("events: mention-only: bot_user_id fetch failed; falling back to allow-all", "err", err)
    } else if uid != "" && !strings.Contains(msg.Text, "<@"+uid+">") {
        h.log().Debug("events: mention-only: skipping non-mention message",
            "channel", msg.Channel, "ts", msg.TS)
        return EventsResponse{StatusCode: 200, Body: "ok"}
    }
}

// 5. Dedup event_id — UNCHANGED
```

Key design decisions embedded here:
- **Fail-open**: if `BotUserID.Fetch` fails, we fall back to allowing the message through (consistent with `isBotLoop`'s fail-open policy at line 372).
- **Before dedup**: non-mention messages don't consume a nonce slot.
- **After bot-loop filter**: bot self-messages are already dropped before this check.

### Pattern 2: EventsHandler Struct Extension

```go
// Source: pkg/slack/bridge/events_handler.go (current struct, circa line 27)
// PROPOSED addition:

type EventsHandler struct {
    // ... existing fields ...

    // Phase 91: MentionOnly controls whether the handler only processes messages
    // that @-mention the bot. Set from KM_SLACK_MENTION_ONLY env var at cold-start.
    // Default false → all messages processed (current behaviour, Mode 2 default).
    MentionOnly bool
}
```

No new interface is needed. `BotUserID BotUserIDFetcher` is already a field on `EventsHandler`.

### Pattern 3: Cold-Start Wiring in main.go

```go
// Source: cmd/km-slack-bridge/main.go wireEventsHandler() circa line 251
// PROPOSED addition after eventsHandler construction:

eventsHandler.MentionOnly = os.Getenv("KM_SLACK_MENTION_ONLY") == "true"

// KM_SLACK_BOT_USER_ID is the compile-time-injected bot user ID (from km slack init SSM write).
// When present, prime the CachedBotUserIDFetcher's cache so the first Handle() call
// doesn't need a live auth.test round-trip for the mention check.
if uid := os.Getenv("KM_SLACK_BOT_USER_ID"); uid != "" {
    botUserIDFetcher.PrimeCache(uid)
}
```

Note: `CachedBotUserIDFetcher` needs a `PrimeCache(uid string)` method added. This seeds the in-memory cache with the compile-time value and avoids any cold-start Slack API call for this purpose.

### Pattern 4: Compiler Mention-Only Resolution

The compiler already has a `resolveSlackChannel()` helper in `create_slack.go` that classifies the channel mode. At compile time in `pkg/compiler/userdata.go`, the same logic applies:

```go
// Source: pkg/compiler/userdata.go circa line 3968 (within "if p.Spec.CLI != nil" block)
// PROPOSED insertion after KM_NOTIFY_SLACK_ENABLED emission:

// Phase 91: resolve effective mention-only default from channel mode + optional override.
// Mode 1 (shared):   NotifySlackEnabled=true, NotifySlackPerSandbox=false, ChannelOverride==""  → default true
// Mode 2 (per-sb):   NotifySlackEnabled=true, NotifySlackPerSandbox=true                        → default false
// Mode 3 (override): NotifySlackEnabled=true, NotifySlackChannelOverride!=""                    → default true
// Override: nil = use mode default; &true = force polite; &false = force chatty.
if p.Spec.CLI.NotifySlackEnabled != nil && *p.Spec.CLI.NotifySlackEnabled {
    effective := resolveMentionOnly(p.Spec.CLI)
    notifyEnv["KM_SLACK_MENTION_ONLY"] = boolToTrueFalse(effective)
}
```

The helper `resolveMentionOnly(*CLISpec) bool` encapsulates the mode dispatch table. It reads `NotifySlackInboundMentionOnly`, `NotifySlackPerSandbox`, and `NotifySlackChannelOverride` from the CLI spec.

### Pattern 5: Terraform Lambda Env Variable

```hcl
# infra/modules/lambda-slack-bridge/v1.0.0/main.tf — environment block addition
environment {
  variables = {
    # ... existing vars ...
    # Phase 91: mention-only mode flag and bot user ID for the mention scan filter.
    KM_SLACK_MENTION_ONLY  = var.slack_mention_only
    KM_SLACK_BOT_USER_ID   = var.slack_bot_user_id
  }
}
```

```hcl
# variables.tf additions
variable "slack_mention_only" {
  description = "When 'true', the bridge only processes messages that @-mention the bot (Phase 91)"
  type        = string
  default     = "false"
}

variable "slack_bot_user_id" {
  description = "Slack bot user ID (e.g. UBOT123) for @-mention detection (Phase 91). Injected from SSM at km slack init time."
  type        = string
  default     = ""
}
```

The `terragrunt.hcl` live config reads `KM_SLACK_MENTION_ONLY` and `KM_SLACK_BOT_USER_ID` from environment (set by the compiler or from `km-config.yaml`) — following the existing `get_env("KM_ARTIFACTS_BUCKET", "")` pattern.

**Important**: `KM_SLACK_MENTION_ONLY` in the Terraform/Lambda context is a GLOBAL setting for the bridge Lambda, whereas in the sandbox's `/etc/profile.d/km-notify-env.sh` it is per-profile. These are DIFFERENT uses of the same env var name:
- Lambda env var: controls whether the bridge's `/events` handler applies the mention filter for all inbound messages to all channels managed by this install.
- Sandbox env var: (only added in `notifyEnv` for completeness, but the bridge is the one enforcing mention-only, NOT the sandbox-side hook).

**Resolution**: `KM_SLACK_MENTION_ONLY` does NOT belong in `notifyEnv` (the sandbox-side profile.d file). It belongs ONLY in the Lambda env. The compiler emits it to the Terraform module inputs, not to `notifyEnv`. This is the key design insight: the mention filter lives in the BRIDGE (Lambda), not in the sandbox.

### Revised Compiler Pattern

The compiler does NOT add `KM_SLACK_MENTION_ONLY` to `notifyEnv` (the sandbox env file). Instead, it needs to feed the resolved value into the Lambda Terraform module inputs. The existing path for this is `km slack init --redeploy` or simply capturing it at `km init --sidecars` time.

**Simpler approach** (Claude's Discretion): Instead of baking `KM_SLACK_MENTION_ONLY` into the Lambda at Terraform time (which would require per-sandbox apply per-profile), the bridge Lambda reads `KM_SLACK_MENTION_ONLY` from SSM at cold-start OR the value is a FIXED config for the bridge Lambda (not per-profile).

**Actually**, re-reading CONTEXT.md more carefully: the "bridge Lambda reads KM_SLACK_MENTION_ONLY" is the Lambda-level env var. The compiler's role is to WRITE the value at sandbox-create time into `/etc/km/notify.env` on the sandbox — but wait, the sandbox-side env is irrelevant here. The bridge Lambda is what decides whether to filter.

**Correct architecture** (confirmed by reading the code):
- The bridge Lambda processes events from ALL channels subscribed via the Events API.
- Per-sandbox `#sb-{id}` channels → every message is currently processed.
- Shared (Mode 1) / Override (Mode 3) channels → Phase 91 adds mention-only.
- The bridge resolves channel→sandbox via `DDBSandboxByChannel` (step 6). It knows which sandbox a message belongs to.
- The `mention_only` flag is a PROPERTY OF THE CHANNEL/SANDBOX, not a global Lambda setting.

**Revised architecture**: The `KM_SLACK_MENTION_ONLY` env var approach works as a GLOBAL default for the Lambda (simpler, Terraform-level), but it can't be per-channel without DDB changes. The CONTEXT.md locked decision says: "emitted into the bridge config from the resolved profile field after mode-derived default is applied." This points to a per-sandbox value stored in DDB alongside `slack_channel_id`.

**The two viable implementations:**
1. **Global Lambda env var** (`KM_SLACK_MENTION_ONLY=true`): simple; all channels managed by this Lambda get the same mode. Works if the operator consistently uses mention-only for all channels.
2. **Per-sandbox DDB attribute** (`km-sandboxes.mention_only bool`): stored at `km create` time, read by the bridge in step 6 alongside `SandboxRoutingInfo`. Works per-sandbox.

The CONTEXT.md leans toward option 1 ("compiler emits KM_SLACK_MENTION_ONLY env var into the bridge config from the resolved profile field"). But "bridge config" = Lambda env, which is global.

Looking at the existing bridge `SandboxRoutingInfo` struct to see if a per-sandbox flag fits:

### Pattern 6: SandboxRoutingInfo Extension (Recommended)

```go
// Source: pkg/slack/bridge/aws_adapters.go — SandboxRoutingInfo struct
// PROPOSED addition:

type SandboxRoutingInfo struct {
    SandboxID  string
    QueueURL   string
    Paused     bool
    MentionOnly bool  // Phase 91: true when profile resolved to mention-only mode
}
```

This means `km create` writes `mention_only = true|false` into the `km-sandboxes` DynamoDB table alongside the existing `slack_channel_id`. The bridge reads it at step 6 and applies the filter based on the per-sandbox value. This is more correct: different sandboxes in the same install can have different mention-only settings.

The `DDBSandboxByChannel.FetchByChannel()` already does a DDB query — adding one more attribute projection is trivial.

**Recommendation**: Use the per-sandbox DDB attribute approach. It is the only approach that correctly handles mixed-mode deployments (some per-sandbox channels want every-message, some shared channels want mention-only). The compiler writes `mention_only` to the `km create` create-sandbox DDB write, the bridge reads it.

This does require a minor DDB schema addition (new optional attribute `mention_only` on `km-sandboxes`), which is additive and backward-compatible (old sandboxes without the attribute default to `false` = current every-message behavior).

### Anti-Patterns to Avoid

- **Global Lambda env var only**: Would make all channels on an install either all mention-only or all chatty. Doesn't support mixed deployments.
- **Substring scan AFTER dedup**: Non-mention messages would consume nonce slots; if a human sends 100 messages in a shared channel, we'd fill the nonce table with records for messages we silently dropped.
- **Using `threadTS` in the mention scan**: The mention guard fires on `msg.TS` (the originating message text), not on thread context. Already non-issue since `msg.Text` is used.
- **Adding `KM_SLACK_MENTION_ONLY` to `notifyEnv`** (the sandbox `/etc/profile.d/km-notify-env.sh`): This file is read by the sandbox-side notify hook, not the bridge. The bridge is a Lambda with no access to the sandbox's profile.d.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Bot user ID in bridge | New SSM fetch in bridge | `CachedBotUserIDFetcher` already in `pkg/slack/bridge/aws_adapters.go` | Already wired; fetches from `auth.test` with 1h cache; just read `h.BotUserID.Fetch()` |
| Channel mode determination in compiler | New mode-detection logic | `resolveSlackChannel()` logic in `create_slack.go` already has Mode 1/2/3 classification | Same three-way check: `NotifySlackChannelOverride != ""` = Mode 3; `NotifySlackPerSandbox = true` = Mode 2; else Mode 1 |
| Doctor check infrastructure | New check registration | Extend `doctor_slack_transcript.go` with the established `func checkSlack*(...)  CheckResult` shape | All Slack doctor checks follow same closure-injection signature pattern |
| DDB attribute projection | Custom DDB query | Extend `DDBSandboxByChannel.FetchByChannel()` projection with `mention_only` attribute | One-line addition to the existing `ProjectionExpression` |

---

## Common Pitfalls

### Pitfall 1: Mention scan position relative to dedup

**What goes wrong:** If the mention-scan early-return fires AFTER step 5 (dedup), non-mention messages consume a nonce slot each time Slack retries.

**Why it happens:** Following the numbered step order without thinking about idempotency of the skip action.

**How to avoid:** Insert the mention-scan guard between step 4 and step 5 (confirmed above). Non-mention silently returns 200 without touching nonces.

**Warning signs:** `km-slack-bridge-nonces` table growing unexpectedly fast with entries for non-mention messages.

### Pitfall 2: Global Lambda env var vs per-sandbox DDB attribute

**What goes wrong:** Using `KM_SLACK_MENTION_ONLY` as a single Lambda-level env var means all sandboxes on the install are affected equally. An operator who wants mention-only for a shared channel but chatty for a specific per-sandbox channel can't achieve that.

**How to avoid:** Store `mention_only` as a DDB attribute on `km-sandboxes` written by `km create`. The bridge reads it per-channel at step 6.

**Note:** `KM_SLACK_MENTION_ONLY` env var on the Lambda Terraform side is STILL useful as a default override at the infrastructure level (for simple single-mode installs), but the per-sandbox DDB value takes precedence.

### Pitfall 3: bot_user_id unavailability on cold-start

**What goes wrong:** If `BotUserID.Fetch()` fails during the mention scan (Slack API down), the mention check falls through with fail-open behavior — every message is processed. This matches `isBotLoop()`'s fail-open policy and is correct, but must be documented.

**How to avoid:** `KM_SLACK_BOT_USER_ID` env var is the compile-time fallback. If set, prime the `CachedBotUserIDFetcher` cache so no live API call is needed. The fetcher's existing 1h cache means only the first Handle() call per cold-start ever needs the API.

### Pitfall 4: CLISpec tri-state pointer nil vs false

**What goes wrong:** Using `bool` instead of `*bool` means nil (unset) and false (explicit chatty override) are indistinguishable.

**How to avoid:** `NotifySlackInboundMentionOnly *bool` is locked in CONTEXT.md. In the compiler's resolution, `nil` = use mode-derived default; `&true` = force mention-only; `&false` = force chatty.

**Warning signs:** A Mode 1 (shared) profile unexpectedly getting chatty behaviour because `NotifySlackInboundMentionOnly` was omitted and code treated nil as false.

### Pitfall 5: km init --sidecars required

**What goes wrong:** After shipping the schema change, existing management Lambda sandboxes don't recognize the new field until `km init --sidecars` is run.

**How to avoid:** Document prominently: profile `notifySlackInboundMentionOnly` requires `make build && km init --sidecars` before new sandboxes can use it.

### Pitfall 6: Auth.test currently discards bot_user_id

**What goes wrong:** `pkg/slack/client.go Client.AuthTest()` currently calls `auth.test` but discards the response body (returns only error). Adding SSM write for `bot_user_id` requires a new method `AuthTestWithUserID() (string, error)` that parses the response.

**How to avoid:** Add `AuthTestWithUserID` to `pkg/slack/client.go` and the `SlackInitAPI` interface. The existing `AuthTest()` can be kept as a wrapper. The `auth.test` response JSON includes `user_id` (the bot's USER ID, not `bot_id`). Specifically: `{"ok":true, "user":"UBOT123", "user_id":"UBOT123", "bot_id":"BBOT456", ...}`.

**Confirmed**: The Slack `auth.test` response field is `user_id` (string). This is what `CachedBotUserIDFetcher.SlackAPI.AuthTest()` in `pkg/slack/bridge/aws_adapters.go` extracts (line 1059: `uid, err := f.SlackAPI.AuthTest(ctx, token)` — the `SlackAuthTestAPI` interface already returns `(userID string, err error)`). The bridge's `slackAuthTestAdapter` in `cmd/km-slack-bridge/main.go` already parses `user_id` from the response. This adapter is NOT in the `pkg/slack` client — it's in `cmd/km-slack-bridge/main.go`. Reuse its JSON-parsing pattern for the new `AuthTestWithUserID()` method on `pkg/slack/Client`.

---

## Code Examples

### Q1 Answer: Where the mention scan slots in

**File:** `pkg/slack/bridge/events_handler.go`
**Function:** `EventsHandler.Handle()`
**Slot:** Between step 4 (bot-loop filter, line ~162) and step 5 (dedup, line ~168).

The current code flow:
```
Step 4: isBotLoop check → return 200 if bot loop (line ~163-166)
[--- Phase 91 mention scan goes HERE ---]
Step 5: Dedup event_id (line ~168-177)
Step 6: Resolve channel→sandbox (line ~180-188)
...
Step 10: ACK reaction (line ~318-330)
```

The insertion point is:
```go
// Source: pkg/slack/bridge/events_handler.go
// After line ~166 (isBotLoop return), before line ~168 (dedup)

// 4b. Mention-only filter (Phase 91). When MentionOnly is true,
// skip processing messages that don't @-mention the bot user.
// Fail-open: if bot_user_id fetch fails, allow the message through
// (consistent with isBotLoop's fail-open policy).
if h.MentionOnly {
    if uid, err := h.BotUserID.Fetch(ctx); err != nil {
        h.log().Warn("events: mention-only: bot_user_id unavailable; allowing through", "err", err)
    } else if uid != "" && !strings.Contains(msg.Text, "<@"+uid+">") {
        h.log().Debug("events: mention-only: skipping", "channel", msg.Channel, "ts", msg.TS)
        return EventsResponse{StatusCode: 200, Body: "ok"}
    }
}
```

### Q2 Answer: bot_user_id — current state and what's needed

**Current state (confirmed):**
- `km slack init` `RunSlackInit()` calls `api.AuthTest(ctx)` at `slack.go:213` — but `SlackInitAPI.AuthTest()` returns only `error`, discarding `user_id`.
- The bridge's `CachedBotUserIDFetcher` calls `auth.test` live on first use via its `SlackAuthTestAPI.AuthTest(ctx, token) (string, error)` interface (the `slackAuthTestAdapter` in `cmd/km-slack-bridge/main.go` does parse the JSON response body for `user_id`).
- SSM path `{prefix}slack/bot-user-id` does NOT currently exist — nothing writes to it.

**What Phase 91 adds:**
1. `pkg/slack/client.go` gains `AuthTestWithUserID(ctx context.Context) (string, error)` — parses `user_id` from the `auth.test` response.
2. `SlackInitAPI` interface in `internal/app/cmd/slack.go` gains `AuthTestWithUserID(ctx context.Context) (string, error)`.
3. `RunSlackInit()` calls `api.AuthTestWithUserID(ctx)` instead of `api.AuthTest(ctx)` and writes the returned UID to SSM.
4. `runSlackRotateToken()` does the same after validating the new token.
5. The Lambda Terraform module gains `KM_SLACK_BOT_USER_ID` variable; `wireEventsHandler()` reads it and primes `CachedBotUserIDFetcher`.

### Q3 Answer: How bridge Lambda reads config / env vars

**Confirmed pattern:** Lambda reads env vars set by Terraform module `main.tf` environment block. All bridge config (DDB table names, SSM paths, resource prefix) is injected this way. No app config file or SSM read happens during cold-start for these values.

`KM_SLACK_MENTION_ONLY` and `KM_SLACK_BOT_USER_ID` follow this exact pattern:
- `variables.tf`: new `slack_mention_only` (string, default `"false"`) and `slack_bot_user_id` (string, default `""`) variables.
- `main.tf` environment block: `KM_SLACK_MENTION_ONLY = var.slack_mention_only`, `KM_SLACK_BOT_USER_ID = var.slack_bot_user_id`.
- `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` inputs: these are NOT emitted by the compiler per-profile; they need to come from SSM at Terraform apply time (via `get_env()`) or be hardcoded as empty defaults.

**Recommended approach for `KM_SLACK_BOT_USER_ID` in Terraform:** Read from SSM at `km init --lambdas` / `km init --sidecars` time, OR just leave it as empty default and rely entirely on `CachedBotUserIDFetcher` at runtime. The SSM-at-deploy approach requires Terraform to read SSM, which adds complexity. **Simpler**: emit as empty string in Terraform; the `CachedBotUserIDFetcher` already handles the live fetch. The `PrimeCache()` approach is only a performance optimization, not required.

**For `KM_SLACK_MENTION_ONLY`**: Since this is an install-level configuration (does this install use mention-only?), it's appropriate as a Lambda env var. An install-level default covers most cases. The per-sandbox DDB attribute is the correct mechanism for per-channel control within an install.

### Q4 Answer: bot_user_id delivery to Lambda at runtime

**Recommended approach:** Keep `CachedBotUserIDFetcher` as-is for runtime. It already calls `auth.test` once per Lambda warm lifetime (1h TTL). Adding `PrimeCache()` to pre-seed from `KM_SLACK_BOT_USER_ID` env var is a performance optimization (avoids one `auth.test` call on cold-start for the mention scan). This is the cheapest path:

- `CachedBotUserIDFetcher` gains `PrimeCache(uid string)` method.
- `wireEventsHandler()` calls `botUserIDFetcher.PrimeCache(os.Getenv("KM_SLACK_BOT_USER_ID"))` if the env var is non-empty.
- No extra SSM round-trip at cold-start.
- `KM_SLACK_BOT_USER_ID` can be empty in Terraform (defaults to `""`); the fetcher then calls `auth.test` lazily on first mention-scan.

This satisfies CONTEXT.md's preference for "compile-time injection" without baking the value into Terraform state (the value comes from SSM at `km init --sidecars` via a data source, or is empty).

### Q5 Answer: Channel-mode resolution logic

**Source:** `internal/app/cmd/create_slack.go` `resolveSlackChannel()` lines 86-245.

The function returns `(channelID string, perSandbox bool, err error)`. The mode is determined by:

```
Mode 3 (override):  cli.NotifySlackChannelOverride != ""
Mode 2 (per-sandbox): cli.NotifySlackPerSandbox == true
Mode 1 (shared):    else
```

The compiler's `resolveMentionOnly(*CLISpec) bool` function follows the same logic:

```go
func resolveMentionOnly(cli *profile.CLISpec) bool {
    if cli == nil {
        return false // no CLI spec → no Slack → Mode 1 default irrelevant
    }
    // Explicit override wins.
    if cli.NotifySlackInboundMentionOnly != nil {
        return *cli.NotifySlackInboundMentionOnly
    }
    // Mode 2 (per-sandbox): every-message default.
    if cli.NotifySlackPerSandbox {
        return false
    }
    // Mode 1 (shared) and Mode 3 (override): mention-only default.
    return true
}
```

Note: in the compiler (unlike `create_slack.go`), we don't need to resolve the actual channel ID — only the mode matters for the mention-only default.

### Q6 Answer: Existing tests for bridge events handler

**File:** `pkg/slack/bridge/events_handler_test.go` — 961 lines, table-driven approach.

Test infrastructure confirmed:
- `newHandler()` factory function at line 217 returns `*EventsHandler` + six fake collaborators.
- `fakeBotUserID{uid: "UBOT123"}` already in the test double set.
- `signSlackPayload()` helper for generating valid HMAC signatures.
- Tests cover: URL verification, bad signing secret, bot-loop filter, dedup, SQS send, pause hinter, ACK reaction.

**Phase 91 test extension pattern:**

```go
// In TestEventsHandler_MentionOnly (new test function):
tests := []struct {
    name        string
    mentionOnly bool
    botUID      string
    msgText     string
    expectSQS   bool
}{
    {"mention-only=false, no @mention → dispatched",       false, "UBOT", "hello",             true},
    {"mention-only=true, has @mention → dispatched",       true,  "UBOT", "hey <@UBOT> help",  true},
    {"mention-only=true, no @mention → skipped",           true,  "UBOT", "hello world",        false},
    {"mention-only=true, different @mention → skipped",    true,  "UBOT", "hey <@UOTHER>",      false},
    {"mention-only=true, mention at start → dispatched",   true,  "UBOT", "<@UBOT> help",       true},
    {"mention-only=true, mention at end → dispatched",     true,  "UBOT", "help <@UBOT>",       true},
    {"mention-only=true, bot_uid fetch error → fail-open", true,  "",     "no mention",         true},  // empty uid = fail-open
}
```

### Q7 Answer: km doctor check pattern

**Source:** `internal/app/cmd/doctor_slack_transcript.go` lines 324-377 (`checkSlackUsersReadEmailScope`).

**Pattern to follow exactly:**

```go
// checkSlackBotUserIDCached verifies the bot_user_id SSM cache is populated when
// mention-only mode is effective for at least one local profile (Phase 91).
// Without the cached value, the bridge's mention-scan falls back to a live auth.test
// call on cold-start, which is less reliable and adds latency.
//
// Returns:
//   - SKIPPED: getUID is nil (Slack not configured or no profiles with mention-only).
//   - OK: {prefix}slack/bot-user-id is set.
//   - WARN: parameter missing or empty.
//   - WARN: SSM read failed (transient error, do not fail doctor).
func checkSlackBotUserIDCached(
    ctx context.Context,
    getUID func(context.Context) (string, error), // reads {prefix}slack/bot-user-id
) CheckResult {
    name := "Slack bot-user-id cache"
    if getUID == nil {
        return CheckResult{
            Name:    name,
            Status:  CheckSkipped,
            Message: "Slack not configured or no profiles with mention-only mode active",
        }
    }
    uid, err := getUID(ctx)
    if err != nil {
        return CheckResult{
            Name:    name,
            Status:  CheckWarn,
            Message: fmt.Sprintf("could not read %sslack/bot-user-id: %v", prefix, err),
        }
    }
    if uid == "" {
        return CheckResult{
            Name:    name,
            Status:  CheckWarn,
            Message: prefix + "slack/bot-user-id not cached — mention-only mode will fall back to live auth.test on cold-start",
            Remediation: "Run `km slack init --force` to re-cache the bot user ID.",
        }
    }
    return CheckResult{
        Name:    name,
        Status:  CheckOK,
        Message: "Slack bot user ID cached (" + prefix + "slack/bot-user-id = " + uid + ")",
    }
}
```

**Registration location:** `internal/app/cmd/doctor.go` in the Slack health checks block (around line 2974 where `checkSlackUsersReadEmailScope` is called). The trigger condition (only check when at least one profile has mention-only active) is implemented in the wrapper that constructs the `getUID` func — if no profiles resolve mention-only, pass `nil` → `CheckSkipped`.

### Q8 Answer: Documentation impact

**`docs/slack-notifications.md`:** Add a `## Phase 91: Polite-bot — @-mention-only mode` section after the Phase 72 section (around line 1419+). Contents: new field description, per-mode defaults table, operator override examples, `km doctor` new check, `km init --sidecars` upgrade note.

**`CLAUDE.md`:** The Phase 72 section under "Where to look" already says "post to Slack from inside a sandbox ... inbound". Add a note: `spec.cli.notifySlackInboundMentionOnly` — polite-bot mode for shared/override channels; bridge checks for `<@{bot_user_id}>`. Also update the "CLI" section for `km slack init` to note it now caches `bot_user_id`.

**`OPERATOR-GUIDE.md`:** Add a `§ Mention-only mode (polite-bot)` subsection in the Slack section. Two-sentence summary with cross-ref to `docs/slack-notifications.md`.

### Q9 Answer: *bool tri-state pattern in codebase

**Confirmed pattern from `CLISpec` in `pkg/profile/types.go`:**
- `NotifyEmailEnabled *bool` (line 450): "Pointer type so unset (nil) is distinguishable from explicit false"
- `NotifySlackEnabled *bool` (line 455): same
- `UseSlackConnect *bool` (line 521): "Pointer so nil ⇒ default true"
- `VSCodeEnabled *bool` (line 531): "Pointer-bool with omit-means-true semantics"
- `SlackArchiveOnDestroy *bool` (line 474): "Pointer type so unset is distinguishable from explicit false in tests"

`NotifySlackInboundMentionOnly *bool` follows identically. The comment should say: "Pointer so nil = inherit mode-derived default (Mode 1/3 → true, Mode 2 → false); &true = force polite-bot; &false = force chatty in all modes."

### Q10 Answer: Backward compat and rollout

**Backward compatibility:**
- Existing sandboxes (created before Phase 91): `km-sandboxes` DDB rows have no `mention_only` attribute. `DDBSandboxByChannel.FetchByChannel()` returns `SandboxRoutingInfo.MentionOnly = false` (Go zero value). Every message is processed as today. FULL BACKWARD COMPAT with no operator action.
- Bridge Lambda before re-deploy: `KM_SLACK_MENTION_ONLY` is absent → `os.Getenv("KM_SLACK_MENTION_ONLY") == "true"` → false → every-message. FULL BACKWARD COMPAT.

**Operator upgrade path:**
1. `make build` — rebuild `km` binary.
2. `km init --sidecars` — deploy new bridge Lambda zip with `KM_SLACK_MENTION_ONLY` and `KM_SLACK_BOT_USER_ID` env vars.
3. `km slack init --force` — re-runs `auth.test`, caches `bot_user_id` at `{prefix}slack/bot-user-id`.
4. `km destroy && km create` for existing sandboxes that should use the new field.
5. `km doctor` — verifies `slack_bot_user_id_cached` is OK.

---

## State of the Art

| Old Approach | Current Approach | Impact |
|---|---|---|
| Bot processes every message in every subscribed channel | (Phase 91) mention-only default for shared/override channels | Reduces noise in corporate shared channels; bot only reacts when @-mentioned |
| `auth.test` user_id only cached in-process (Lambda cold-start) | (Phase 91) also written to SSM `{prefix}slack/bot-user-id` | Enables `km doctor` to verify cache health; primes Lambda cold-start |

---

## Open Questions

1. **`KM_SLACK_MENTION_ONLY` in Terraform: per-install or per-sandbox?**
   - What we know: CONTEXT.md says "emitted into the bridge config from the resolved profile field". The bridge config = Lambda env. But per-profile values can differ across sandboxes.
   - What's unclear: Whether to implement per-sandbox DDB attribute (recommended above) or install-level Lambda env var.
   - Recommendation: Per-sandbox DDB attribute (`km-sandboxes.mention_only`) written by `km create`, read by bridge step 6 in `SandboxRoutingInfo.MentionOnly`. The Lambda Terraform module gets `KM_SLACK_MENTION_ONLY` as an INSTALL-LEVEL DEFAULT (useful for simple all-or-nothing installs). Per-sandbox DDB overrides it at the channel level. This is the most flexible approach.

2. **`PrimeCache()` on `CachedBotUserIDFetcher` — needed?**
   - What we know: The fetcher already has a 1h cache. The first `Handle()` call after cold-start triggers one `auth.test` call, which is fast (<100ms typically).
   - Recommendation: Add `PrimeCache()` for correctness (avoids the live API call entirely when `KM_SLACK_BOT_USER_ID` is set) but it's a minor optimization. If plan granularity is tight, can be Wave N+1.

---

## Validation Architecture

`workflow.nyquist_validation` is `true` in `.planning/config.json` — this section is REQUIRED.

### Test Framework

| Property | Value |
|---|---|
| Framework | Go testing (stdlib `testing` package) |
| Config file | none — `go test ./...` convention |
| Quick run command | `go test ./pkg/slack/bridge/... ./pkg/compiler/... ./pkg/profile/... ./internal/app/cmd/... -run TestMentionOnly -v` |
| Full suite command | `make test` (or `go test ./...`) |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| POL-01 | `*bool` field added to `CLISpec` | unit (schema parse roundtrip) | `go test ./pkg/profile/... -run TestCLISpec` | ❌ Wave 0 — add to existing `compiler_test.go` or new `profile_cli_test.go` |
| POL-02 | JSON schema validates new field | unit | `go test ./pkg/profile/... -run TestSchema` | ❌ Wave 0 |
| POL-03 | Validator accepts/rejects new field | unit | `go test ./pkg/profile/... -run TestValidateSemantic` | ✅ `pkg/profile/validate.go` has tests |
| POL-04 | Compiler emits `KM_SLACK_MENTION_ONLY` | unit table-driven (9 cases) | `go test ./pkg/compiler/... -run TestMentionOnlyCompiler` | ❌ Wave 0 |
| POL-05 | Terraform variable wiring | manual (terragrunt plan) | `terragrunt plan --terragrunt-working-dir infra/live/use1/lambda-slack-bridge` | N/A — plan verification |
| POL-06 | Bridge mention-scan guard | unit (table-driven, 7 cases) | `go test ./pkg/slack/bridge/... -run TestEventsHandler_MentionOnly` | ❌ Wave 0 |
| POL-07 | `km slack init` writes `bot_user_id` to SSM | unit (fake SSM) | `go test ./internal/app/cmd/... -run TestRunSlackInit_BotUserIDCached` | ❌ Wave 0 |
| POL-08 | `km slack rotate-token` re-writes `bot_user_id` | unit | `go test ./internal/app/cmd/... -run TestRotateToken_BotUserIDCached` | ❌ Wave 0 |
| POL-09 | Bridge cold-start reads `KM_SLACK_BOT_USER_ID`, primes cache | unit | `go test ./cmd/km-slack-bridge/... -run TestWireEventsHandler_BotUserIDPrime` | ❌ Wave 0 |
| POL-10 | `km doctor` WARN when `bot_user_id` missing + mention-only active | unit | `go test ./internal/app/cmd/... -run TestCheckSlackBotUserIDCached` | ❌ Wave 0 |
| POL-11 | 9-case mention-only resolution table | unit | `go test ./pkg/compiler/... -run TestResolveMentionOnly` | ❌ Wave 0 |
| POL-12 | Mention-scan: matches/rejects/edge cases | unit (7 cases per table above) | `go test ./pkg/slack/bridge/... -run TestEventsHandler_MentionOnly` | ❌ Wave 0 |
| POL-13 | Docs updated | manual review | — | N/A |

### Sampling Rate

- **Per task commit:** `go test ./pkg/slack/bridge/... ./pkg/compiler/... ./pkg/profile/... ./internal/app/cmd/... -run TestMention -v`
- **Per wave merge:** `go test ./pkg/... ./internal/... ./cmd/...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/slack/bridge/events_handler_test.go` — add `TestEventsHandler_MentionOnly` table covering POL-06/POL-12 (extend existing file)
- [ ] `pkg/compiler/userdata_mention_test.go` — new file; covers POL-04/POL-11 (`TestResolveMentionOnly`, `TestMentionOnlyCompiler`)
- [ ] `pkg/profile/` — extend existing schema/validate tests for new field (POL-01/POL-02/POL-03)
- [ ] `internal/app/cmd/slack_test.go` — extend for POL-07 `TestRunSlackInit_BotUserIDCached`
- [ ] `internal/app/cmd/doctor_slack_users_email_test.go` — extend or create sibling for POL-10
- [ ] No new framework install needed — Go stdlib testing already in use

*(Existing test infrastructure covers all phase requirements once stubs are added in Wave 0.)*

---

## Sources

### Primary (HIGH confidence)

- Source code read directly: `pkg/slack/bridge/events_handler.go` — full Handle() dispatch chain confirmed
- Source code read directly: `pkg/slack/bridge/aws_adapters.go` — `CachedBotUserIDFetcher` implementation confirmed
- Source code read directly: `cmd/km-slack-bridge/main.go` — cold-start wiring + env var reading pattern confirmed
- Source code read directly: `pkg/profile/types.go` — `CLISpec` struct with all sibling `*bool` fields confirmed
- Source code read directly: `pkg/compiler/userdata.go:3940-4057` — `notifyEnv` emission block confirmed
- Source code read directly: `internal/app/cmd/slack.go` `RunSlackInit()` — `auth.test` call shape confirmed; no `bot_user_id` SSM write currently
- Source code read directly: `internal/app/cmd/create_slack.go` `resolveSlackChannel()` — Mode 1/2/3 dispatch logic confirmed
- Source code read directly: `internal/app/cmd/doctor_slack_transcript.go` — `checkSlackUsersReadEmailScope` shape confirmed (Phase 91 template)
- Source code read directly: `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` + `variables.tf` — env variable wiring pattern confirmed
- Source code read directly: `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` — `get_env()` pattern confirmed
- Source code read directly: `pkg/profile/schemas/sandbox_profile.schema.json` — current cli field schema confirmed
- Source code read directly: `pkg/profile/validate.go` — existing Slack field validation rules confirmed

### Secondary (MEDIUM confidence)

- `docs/slack-notifications.md` § Phase 72 — confirms documentation template (line 1419+)
- `.planning/phases/72-CONTEXT.md` — confirms `EnsureMemberByEmail` orchestrator, `auth.test` call shape, `bot_user_id` already returned in auth.test response

### Tertiary (LOW confidence)

- Slack API documentation (training knowledge, not freshly verified): `auth.test` response includes `user_id` field — the existing `CachedBotUserIDFetcher.SlackAuthTestAPI.AuthTest()` in `aws_adapters.go` already relies on this, which HIGH-confidence verifies the field exists in the response.

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all code confirmed from source; no new external deps
- Architecture: HIGH — insertion point confirmed by reading full Handle() dispatch chain; per-sandbox DDB approach confirmed as correct by analyzing `SandboxRoutingInfo` usage
- Pitfalls: HIGH — dedup ordering issue, tri-state pointer pattern, auth.test response body discard — all confirmed from source code
- bot_user_id caching gap: HIGH — confirmed `km slack init` does NOT currently write to `{prefix}slack/bot-user-id`

**Research date:** 2026-05-30
**Valid until:** 2026-06-30 (stable; Slack API auth.test response shape unchanged for years)
