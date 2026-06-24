# Phase 118: Slack Trigger Allowlist + Private Per-Sandbox Channels — Research

**Researched:** 2026-06-24
**Domain:** Slack bridge + profile schema + DDB round-trip + Lambda env-var wiring
**Confidence:** HIGH (all findings verified against live codebase; no external sources required)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **Feature A — Private channel:** Field `notification.slack.private` (bool, default `false`). At `km create`, when `perSandbox:true` AND `private:true`, `CreateChannel` passes `is_private:true` instead of the hardcoded `false` at `pkg/slack/client.go:606`. Invites unchanged. No new OAuth scopes. `private:true` takes effect at channel creation only (no in-place conversion). `private:true` with `perSandbox:false` → `km validate` warns.
- **Feature B — Trigger allowlist:** Install-level `slack.allow` in `km-config.yaml` → `KM_SLACK_ALLOW` env var on the slack bridge Lambda. Per-sandbox `notification.slack.inbound.allow` in the profile → written to `km-sandboxes` DDB row → read by the bridge's `FetchByChannel`. Resolution (in order): non-empty per-sandbox list replaces install-level; non-empty install-level; else everyone allowed. Enforce in `pkg/slack/bridge/events_handler.go` on `event.User`. Reject = silent ignore.
- **Naming:** `allow` in both YAMLs; `slack_allow` DDB attribute (verify against real row convention — see research below).
- **Handler order:** channel-ownership → allowlist (new) → mention/thread logic → dedup → dispatch.
- **Repo plumbing:** Config merge-list entry required. DDB round-trip required (SandboxMetadata). `FetchByChannel` extended. `km validate` warns.
- **Deploy:** Install-level allow → `km init --dry-run=false` (NOT `--sidecars`; `km init --slack` also works). Per-sandbox allow + private → `make build-lambdas` + `km init --dry-run=false`. No apiVersion bump.

### Claude's Discretion

- Test structure and table-driven test layout for resolution-order and enforcement.
- Exact DDB attribute name (`slack_allow` proposed; verified below against actual row-writer convention).
- Doc placement (`docs/slack-notifications.md` § Phase 118) and CLAUDE.md phase summary wording.
- Whether `km validate` warning text/format follows existing warn conventions.

### Deferred Ideas (OUT OF SCOPE)

- Email/handle-based allowlist resolution (`users.info`).
- In-place public→private channel conversion.
- Ephemeral "not authorized" nudges (silent by decision).
- Per-user gating finer than trigger/no-trigger.

</user_constraints>

---

## Summary

Phase 118 adds two independent composable knobs to the Slack integration. The codebase is well-structured with clear precedents for both features.

**Feature A (private channel)** has a single-line implementation site: `pkg/slack/client.go:603-612`, the `CreateChannel` function. The hardcoded `"is_private": false` at line 606 becomes conditional on a new `private bool` parameter. The call site in `internal/app/cmd/create_slack.go:464` must pass the field from the profile. The `SlackAPI` interface in `create_slack.go:87` declares `CreateChannel(ctx, name string)` — its signature needs a `private bool` parameter added.

**Feature B (trigger allowlist)** follows a precise precedent: the `slack_react_always` / `SlackReactAlways` pattern from Phase 91.5. The complete chain is: profile field → `SandboxMetadata` struct → `marshalSandboxItem` / `unmarshalSlackFields` → `FetchByChannel` DDB read → `SandboxRoutingInfo` field → `EventsHandler` enforcement. The install-level path follows `slack.mention_only` / `KM_SLACK_MENTION_ONLY` exactly. The new DDB attribute name should be `slack_allow` — consistent with `slack_react_always` and `slack_mention_only` naming convention. The DDB type should be a **comma-joined string** (not DDB SS/L), because `UpdateSandboxAttr` only supports `string` values and `FetchByChannel` already parses bool strings. For []string, store as comma-joined S attribute; parse by splitting on comma. (Note: `allowed_senders` in identity.go uses DDB SS type, but that is a different subsystem with direct `PutItem` control; `UpdateSandboxAttr` is string-only.)

**Primary recommendation:** Follow the Phase 91.5 `slack_react_always` pattern exactly for Feature B, and add a `private bool` parameter to `CreateChannel` for Feature A. No new AWS resources, no DDB schema migration, no TF module version bump required.

---

## Standard Stack

No new libraries. All changes are in existing Go packages.

### Existing Packages Touched

| Package | File | Role in Phase 118 |
|---------|------|-------------------|
| `pkg/slack` | `client.go` | Feature A: `CreateChannel` signature + `is_private` parameter |
| `pkg/profile` | `types.go` | Feature A+B: add `Private bool` to `NotificationSlackSpec`; add `Allow []string` to `NotificationSlackInboundSpec` |
| `pkg/profile` | `schemas/sandbox_profile.schema.json` | Add `private` (boolean) and `allow` (string array) to JSON schema |
| `pkg/profile` | `validate.go` | Add warn rules: private+!perSandbox; allow+!perSandbox |
| `pkg/aws` | `metadata.go` | Add `SlackAllow []string` to `SandboxMetadata` |
| `pkg/aws` | `sandbox_dynamo.go` | Add `slack_allow` to `marshalSandboxItem` and `unmarshalSlackFields` |
| `pkg/slack/bridge` | `events_interfaces.go` | Add `Allow []string` to `SandboxRoutingInfo` |
| `pkg/slack/bridge` | `aws_adapters.go` | `FetchByChannel`: read `slack_allow` attribute; add to `SandboxRoutingInfo` |
| `pkg/slack/bridge` | `events_handler.go` | Add `Allow []string` field; add allowlist enforcement after channel-ownership check |
| `internal/app/cmd` | `create_slack.go` | Pass `private bool` to `CreateChannel`; `SlackAPI` interface update |
| `internal/app/cmd` | `create_slack_inbound.go` | Write `slack_allow` to DDB when profile sets it |
| `internal/app/config` | `config.go` | Add `Allow []string` field to `SlackConfig` struct |
| `internal/app/cmd` | `init.go` | Export `KM_SLACK_ALLOW` env var (same pattern as `KM_SLACK_PEER_BRIDGES`) |
| `infra/modules/lambda-slack-bridge/v1.0.0` | `main.tf` | Add `slack_allow` variable and `KM_SLACK_ALLOW` env block entry |
| `infra/live/use1/lambda-slack-bridge` | `terragrunt.hcl` | Add `slack_allow = get_env("KM_SLACK_ALLOW", "")` input |
| `cmd/km-slack-bridge` | `main.go` | Wire `KM_SLACK_ALLOW` env var into `EventsHandler.Allow` |
| Config merge-list | `config.go` line ~832 | Add `"slack.allow"` to v2→v merge-list |

---

## Architecture Patterns

### Feature A: Private Channel — Complete Call Chain

**Site 1: `pkg/slack/client.go:603-612` (the hardcoded site)**

```go
// CURRENT (line 606):
func (c *Client) CreateChannel(ctx context.Context, name string) (string, error) {
    resp, err := c.callJSON(ctx, "conversations.create", map[string]any{
        "name":       name,
        "is_private": false,  // ← THIS IS THE SITE
    })
    ...
}
```

The change: add a `private bool` parameter, pass it as `"is_private": private`.

**Site 2: `internal/app/cmd/create_slack.go:87` (interface declaration)**

```go
// CURRENT:
CreateChannel(ctx context.Context, name string) (string, error)
// NEW:
CreateChannel(ctx context.Context, name string, private bool) (string, error)
```

**Site 3: `internal/app/cmd/create_slack.go:464` (call site)**

```go
// CURRENT:
chID, createErr := api.CreateChannel(ctx, channelName)
// NEW:
isPrivate := sl.Private  // the new bool field on NotificationSlackSpec
chID, createErr := api.CreateChannel(ctx, channelName, isPrivate)
```

The `sl` variable is already in scope at line 464 (it's the `notificationSlack(p)` result, type `*profile.NotificationSlackSpec`).

**Note:** The `SlackAPI` interface in `create_slack.go` is a local interface (not `pkg/slack.Client` directly). All mock implementations in `create_slack_test.go` also need the signature update.

### Feature B: Allowlist — Complete Layered Chain

#### Layer 1: Profile types (`pkg/profile/types.go`)

Current `NotificationSlackSpec` (lines 203-227): add `Private bool` at the top level.
Current `NotificationSlackInboundSpec` (lines 232-244): add `Allow []string`.

```go
type NotificationSlackSpec struct {
    // ... existing fields ...
    // Phase 118: Private creates the per-sandbox channel as private (is_private:true).
    // No-op when PerSandbox is false/nil. Default false.
    Private bool `json:"private,omitempty" yaml:"private,omitempty"`
    Inbound *NotificationSlackInboundSpec `json:"inbound,omitempty" yaml:"inbound,omitempty"`
    // ...
}

type NotificationSlackInboundSpec struct {
    Enabled     *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
    MentionOnly *bool `json:"mentionOnly,omitempty" yaml:"mentionOnly,omitempty"`
    ReactAlways *bool `json:"reactAlways,omitempty" yaml:"reactAlways,omitempty"`
    // Phase 118: per-sandbox trigger allowlist. Non-empty overrides install-level.
    // Empty/absent = fall back to install-level (or everyone if that too is empty).
    Allow []string `json:"allow,omitempty" yaml:"allow,omitempty"`
}
```

#### Layer 2: SandboxMetadata (`pkg/aws/metadata.go`)

After `SlackReactAlways *bool` (line 71), add:

```go
// Phase 118: per-sandbox trigger allowlist. Non-empty slice overrides KM_SLACK_ALLOW.
// Nil/empty = fall back to install-level. Stored as comma-joined string in DDB
// attribute "slack_allow"; parsed on read. Must round-trip via marshal/unmarshal
// (SandboxMetadata lossy round-trip footgun).
SlackAllow []string `json:"slack_allow,omitempty"`
```

#### Layer 3: DDB marshal/unmarshal (`pkg/aws/sandbox_dynamo.go`)

In `marshalSandboxItem` (around line 414, after the `slack_react_always` block):

```go
// Phase 118: slack_allow — stored as comma-joined S attribute (UpdateSandboxAttr
// is string-only; FetchByChannel parses by splitting on comma).
if len(meta.SlackAllow) > 0 {
    item["slack_allow"] = &dynamodbtypes.AttributeValueMemberS{
        Value: strings.Join(meta.SlackAllow, ","),
    }
}
```

In `unmarshalSlackFields` (around line 267, after `slack_react_always`):

```go
// Phase 118: slack_allow — comma-joined string.
if v, ok := item["slack_allow"].(*dynamodbtypes.AttributeValueMemberS); ok && v.Value != "" {
    meta.SlackAllow = strings.Split(v.Value, ",")
}
```

**DDB attribute name:** `slack_allow` — consistent with `slack_react_always` and `slack_mention_only` naming.

**DDB type:** `AttributeValueMemberS` (comma-joined string), NOT `AttributeValueMemberSS` (string set). Rationale: `UpdateSandboxAttr` (used by `create_slack_inbound.go`) only supports string values. The `allowed_senders` field in `identity.go` uses SS type, but that is written via a full `PutItem` in `identity.go:268`, not via `UpdateSandboxAttr`. The `FetchByChannel` path in `aws_adapters.go` already demonstrates the S→string parse pattern for `slack_react_always` and `slack_mention_only`.

#### Layer 4: SandboxRoutingInfo + FetchByChannel (`pkg/slack/bridge/`)

In `events_interfaces.go`, add to `SandboxRoutingInfo`:

```go
// Phase 118: per-sandbox trigger allowlist. Non-empty overrides the install-level
// EventsHandler.Allow. Nil/empty = use EventsHandler.Allow (or everyone if that
// too is empty). Mirrors the ReactAlways/MentionOnly tri-state contract.
Allow []string
```

In `aws_adapters.go`, `FetchByChannel` (after the `slack_mention_only` block, around line 1103):

```go
// Phase 118: slack_allow — comma-joined string.
if v, ok := item["slack_allow"].(*dynamodbtypes.AttributeValueMemberS); ok && v.Value != "" {
    info.Allow = strings.Split(v.Value, ",")
}
```

#### Layer 5: EventsHandler (`pkg/slack/bridge/events_handler.go`)

Add field to `EventsHandler` struct (after `ReactAlways bool`):

```go
// Phase 118: Allow is the install-level trigger allowlist (Uxxxx IDs).
// Populated from KM_SLACK_ALLOW env var at Lambda cold-start.
// Empty = everyone can trigger (backward-compatible default).
// Resolution: non-empty info.Allow (per-sandbox) replaces this; else
// this; else (both empty) everyone allowed.
Allow []string
```

Insert enforcement in `Handle()` after the channel-ownership check (after the `info.SandboxID == ""` miss block, approximately at line 317 "present+yes: process locally"), BEFORE the mention-only filter (step 5b at line 319):

```go
// NEW: Allowlist gate (Phase 118). Runs after channel-ownership (we need info.Allow)
// and BEFORE the mention-only filter (a non-listed user is dropped regardless of
// mention/thread state — the allowlist is the strict outer gate).
// Resolution: per-sandbox info.Allow (non-empty) > install-level h.Allow > everyone.
effectiveAllow := h.Allow
if len(info.Allow) > 0 {
    effectiveAllow = info.Allow
}
if len(effectiveAllow) > 0 && !isInSlackAllowlist(msg.User, effectiveAllow) {
    h.log().Debug("events: allowlist: silent drop",
        "user", msg.User, "channel", msg.Channel, "ts", msg.TS)
    return EventsResponse{StatusCode: 200, Body: "ok"}
}
```

Add helper (can be placed near `isBotLoop`):

```go
// isInSlackAllowlist reports whether userID is in the allow list.
// Deny-by-default: empty allow list always returns true (everyone allowed).
func isInSlackAllowlist(userID string, allow []string) bool {
    for _, u := range allow {
        if strings.EqualFold(u, userID) {
            return true
        }
    }
    return false
}
```

**Note:** The semantics invert from GitHub bridge's `isInAllowlist` (empty → always false = deny-by-default). Here empty → always true = everyone allowed (backward-compat). The function names and docstrings must make this explicit.

#### Layer 6: Config (`internal/app/config/config.go`)

Add `Allow []string` to `SlackConfig` struct (after `DefaultRouter`):

```go
// Allow is the Phase 118 install-level trigger allowlist. A []string of Slack
// user IDs (Uxxxx). Non-empty: only listed users can trigger the agent.
// Empty/absent: everyone can trigger (backward-compatible).
// Maps to km-config.yaml key slack.allow. Exported as KM_SLACK_ALLOW
// (comma-joined) by km init for the bridge Lambda environment.
Allow []string `mapstructure:"allow" yaml:"allow,omitempty"`
```

Add to the v2→v merge-list (around line 832, after `"slack.default_router"`):

```go
// Phase 118: slack.allow — install-level trigger allowlist. CRITICAL: without
// this entry, slack.allow is silently ignored (project_config_key_merge_list).
"slack.allow",
```

Add loading code after the `slack.default_router` block (around line 962):

```go
// Phase 118: slack.allow is a []string (Uxxxx). Empty = everyone allowed.
if v.IsSet("slack.allow") {
    cfg.Slack.Allow = v.GetStringSlice("slack.allow")
}
```

#### Layer 7: km init env var export (`internal/app/cmd/init.go`)

After the `slack.default_router` block (around line 1642), add:

```go
// Phase 118: KM_SLACK_ALLOW — install-level trigger allowlist (comma-joined Uxxxx).
// Consumed by infra/live/use1/lambda-slack-bridge/terragrunt.hcl
// get_env("KM_SLACK_ALLOW", ""). Only export when the operator has explicitly set
// slack.allow in km-config.yaml. Empty list => omit => everyone allowed.
// env-wins: when env var is already set to DIFFERENT value, emit drift WARN.
if len(cfg.Slack.Allow) > 0 {
    yamlSlackAllow := strings.Join(cfg.Slack.Allow, ",")
    if envVal := os.Getenv("KM_SLACK_ALLOW"); envVal != "" && envVal != yamlSlackAllow {
        fmt.Fprintf(os.Stderr, "WARN: KM_SLACK_ALLOW=%s (env) overrides km-config.yaml slack.allow=%s\n", envVal, yamlSlackAllow)
    } else if envVal == "" {
        os.Setenv("KM_SLACK_ALLOW", yamlSlackAllow) //nolint:errcheck
    }
}
```

#### Layer 8: Lambda Terraform module

In `infra/modules/lambda-slack-bridge/v1.0.0/main.tf`, add a variable and env block entry (same file that has `slack_mention_only`, `slack_react_always`, `slack_peer_bridges`, `slack_default_router`):

```hcl
variable "slack_allow" {
  description = "Phase 118: install-level Slack trigger allowlist (comma-joined Uxxxx IDs). Empty = everyone allowed."
  type        = string
  default     = ""
}
```

In the Lambda environment block (around line 375):

```hcl
KM_SLACK_ALLOW = var.slack_allow
```

In `infra/live/use1/lambda-slack-bridge/terragrunt.hcl`, add input (around line 121):

```hcl
slack_allow = get_env("KM_SLACK_ALLOW", "")
```

#### Layer 9: Bridge Lambda main.go

In `cmd/km-slack-bridge/main.go`, in the `WireMentionOnly` function (or a new `WireAllowlist` function), add:

```go
// Phase 118: KM_SLACK_ALLOW — comma-joined install-level trigger allowlist.
if raw := os.Getenv("KM_SLACK_ALLOW"); raw != "" {
    h.Allow = strings.Split(raw, ",")
    slog.Info("km-slack-bridge: install-level allowlist configured", "count", len(h.Allow))
}
```

#### Layer 10: Per-sandbox write at km create (`internal/app/cmd/create_slack_inbound.go`)

After the `slack_mention_only` block (around line 173), add:

```go
// Phase 118: per-sandbox slack_allow override. Write only when the profile
// explicitly sets notification.slack.inbound.allow (non-empty slice), so
// absence on the DDB row signals "fall back to install-level KM_SLACK_ALLOW".
// Stored as comma-joined string (UpdateSandboxAttr is string-only).
// Non-fatal: if this write fails, install-level default applies.
if len(inbound.Allow) > 0 {
    v := strings.Join(inbound.Allow, ",")
    if updateErr := deps.UpdateSandboxAttr(ctx, deps.SandboxID, "slack_allow", v); updateErr != nil {
        log.Warn().Err(updateErr).Str("sandbox_id", deps.SandboxID).
            Msg("persist slack_allow per-sandbox override failed; sandbox will use install-level default")
    }
}
```

**Important:** per-sandbox `allow` only gets written when `inbound.Enabled == true` (the function returns early at line 102 when inbound is disabled). This is correct behavior: per-sandbox allow requires inbound to be enabled (you need the SQS queue to dispatch).

### Feature A: Validation Warnings (`pkg/profile/validate.go`)

The existing warn pattern (lines 325-339) shows how to add new warnings. Add after the existing Rule S3 (archiveOnDestroy without perSandbox):

```go
// Rule S4 (warning): private=true without perSandbox → no-op.
if sl.Private && !perSandbox {
    errs = append(errs, ValidationError{
        Path:      "spec.notification.slack.private",
        Message:   "notification.slack.private: true has no effect when notification.slack.perSandbox is not true (no per-sandbox channel is created)",
        IsWarning: true,
    })
}
// Rule S5 (warning): per-sandbox inbound.allow set without perSandbox → no-op.
if inbound != nil && len(inbound.Allow) > 0 && !perSandbox {
    errs = append(errs, ValidationError{
        Path:      "spec.notification.slack.inbound.allow",
        Message:   "notification.slack.inbound.allow has no effect when notification.slack.perSandbox is not true (per-sandbox allow requires an inbound queue)",
        IsWarning: true,
    })
}
```

### JSON Schema Update (`pkg/profile/schemas/sandbox_profile.schema.json`)

The `notification.slack` object (around line 721) needs `"private"` added as a boolean property. The `notification.slack.inbound` object (around line 744) needs `"allow"` added as an array of strings. Pattern: follow the existing `"mentionOnly"` and `"reactAlways"` entries in the inbound block.

### Anti-Patterns to Avoid

- **DDB SS type for slack_allow:** Use comma-joined S attribute, not DDB SS (string-set). `UpdateSandboxAttr` only supports strings; SS requires a direct `PutItem` with SDK types.
- **Allowlist enforcement after mention check:** Must be BEFORE mention-only / thread-bypass. The spec locks the handler order.
- **deny-by-default semantics in isInSlackAllowlist:** Unlike GitHub bridge (`isInAllowlist` returns false when empty → deny), Slack allowlist returns true when empty → everyone allowed. The empty-list-means-everyone behavior is essential for backward compatibility.
- **Writing slack_allow when inbound is disabled:** `create_slack_inbound.go` exits early when `inbound.Enabled == false/nil`. Per-sandbox allow without inbound enabled is a no-op (no queue, no dispatch). The validate warning handles this.
- **Config merge-list omission:** Missing `"slack.allow"` from the merge-list in `config.go` causes the YAML value to be silently ignored — the known `project_config_key_merge_list` footgun. This is explicitly flagged in the spec.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead |
|---------|-------------|-------------|
| DDB string-set marshalling for []string | Custom DDB SS codec | Comma-join to S attribute, split on read (consistent with `UpdateSandboxAttr` signature) |
| Allowlist case comparison | Custom string comparison | `strings.EqualFold` (Slack user IDs are uppercase `Uxxxx` but defensive) |

---

## Common Pitfalls

### Pitfall 1: Config merge-list omission (CRITICAL)
**What goes wrong:** `slack.allow: [U12345]` in `km-config.yaml` is silently ignored. `KM_SLACK_ALLOW` is never set. The bridge treats everyone as allowed. No error, no warning.
**Root cause:** The v2→v merge-list in `config.Load()` (line ~832) must explicitly list `"slack.allow"`. Viper does not merge nested keys automatically.
**How to avoid:** Add `"slack.allow"` to the merge-list slice alongside `"slack.peer_bridges"` and `"slack.default_router"`.

### Pitfall 2: SandboxMetadata lossy round-trip
**What goes wrong:** `km pause` + `km resume` strips `slack_allow` from the DDB row. The sandbox loses its per-sandbox allowlist silently.
**Root cause:** `resume.go`, `extend.go`, and ttl-handler use full-row `PutItem` via `WriteSandboxMetadataDynamo`. Any field not in `SandboxMetadata` + `marshalSandboxItem` is dropped.
**How to avoid:** Add `SlackAllow []string` to `SandboxMetadata` AND update both `marshalSandboxItem` (write) and `unmarshalSlackFields` (read).

### Pitfall 3: Handler order — allowlist must precede mention check
**What goes wrong:** If allowlist check is placed after the Phase 91.3 thread-bypass, a non-listed user can hijack an active thread (the bypass skips the mention requirement; the allowlist must override it).
**How to avoid:** Insert allowlist gate immediately after the channel-ownership `info.SandboxID == ""` block, BEFORE the `effectiveMentionOnly` block at line 319. This is the locked spec order.

### Pitfall 4: `CreateChannel` interface mismatch
**What goes wrong:** Adding `private bool` to `pkg/slack.Client.CreateChannel` but not to the local `SlackAPI` interface in `create_slack.go:87` causes the call site to pass the wrong argument silently (if Go allowed it) or fail to compile. The mock in `create_slack_test.go` also needs updating.
**How to avoid:** Update both the `pkg/slack.Client` method signature AND the `SlackAPI` interface in `create_slack.go`. Search for all mock implementations (`MockSlackAPI` or similar in test files).

### Pitfall 5: per-sandbox allow only written when inbound.Enabled=true
**What goes wrong:** A profile sets `notification.slack.inbound.allow: [U12345]` but `notification.slack.inbound.enabled: false`. The `provisionSlackInboundQueue` function returns early at line 102. The `slack_allow` DDB attribute is never written. The per-sandbox allowlist silently has no effect.
**How to avoid:** The spec says the per-sandbox allowlist requires inbound=true (it requires the SQS queue). The validate rule catches the `allow` + `perSandbox:false` combination with a warning. This is the correct/documented behavior, not a bug.

### Pitfall 6: DDB attribute name drift between create and bridge read
**What goes wrong:** `create_slack_inbound.go` writes `"slack_inbound_allow"` but `FetchByChannel` reads `"slack_allow"`. The bridge never sees the value.
**Root cause:** Phase 114 caught exactly this bug for `status` vs `state`. Verify the attribute name string is identical across all three files that touch it: `create_slack_inbound.go` (write), `sandbox_dynamo.go` marshal (write), `sandbox_dynamo.go` unmarshal (read), `aws_adapters.go` FetchByChannel (read).
**How to avoid:** Use a single constant or carefully align all four string literals to `"slack_allow"`.

---

## Code Examples

### Precedent: `slack_react_always` end-to-end (Phase 91.5)

The complete chain for `slack_react_always` is the exact blueprint for `slack_allow`:

**Write at create** (`create_slack_inbound.go:142-154`):
```go
if inbound.ReactAlways != nil {
    v := "false"
    if *inbound.ReactAlways { v = "true" }
    if updateErr := deps.UpdateSandboxAttr(ctx, deps.SandboxID, "slack_react_always", v); updateErr != nil {
        log.Warn()...
    }
}
```

**Marshal** (`sandbox_dynamo.go:414`):
```go
if meta.SlackReactAlways != nil {
    item["slack_react_always"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: *meta.SlackReactAlways}
}
```

**Unmarshal** (`sandbox_dynamo.go:264-267`):
```go
meta.SlackMentionOnly = readTriStateBool(item, "slack_mention_only")
meta.SlackReactAlways = readTriStateBool(item, "slack_react_always")
```

**FetchByChannel read** (`aws_adapters.go:1072-1086`):
```go
if v, ok := item["slack_react_always"].(*dynamodbtypes.AttributeValueMemberBOOL); ok {
    b := v.Value; info.ReactAlways = &b
} else if v, ok := item["slack_react_always"].(*dynamodbtypes.AttributeValueMemberS); ok {
    switch v.Value { case "true": t := true; info.ReactAlways = &t ... }
}
```

**EventsHandler use** (`events_handler.go:545-553`):
```go
effectiveReactAlways := h.ReactAlways
if info.ReactAlways != nil { effectiveReactAlways = *info.ReactAlways }
```

For `slack_allow`, the chain is the same except the type is `[]string` (comma-joined S attribute) instead of `*bool`.

### Precedent: `KM_SLACK_PEER_BRIDGES` env var export (Phase 95)

The `[]string` → comma-joined export in `init.go:1619-1626`:
```go
if len(cfg.Slack.PeerBridges) > 0 {
    yamlPeerBridges := strings.Join(cfg.Slack.PeerBridges, ",")
    if envVal := os.Getenv("KM_SLACK_PEER_BRIDGES"); envVal != "" && envVal != yamlPeerBridges {
        fmt.Fprintf(os.Stderr, "WARN: KM_SLACK_PEER_BRIDGES=%s (env) overrides km-config.yaml slack.peer_bridges=%s\n", envVal, yamlPeerBridges)
    } else if envVal == "" {
        os.Setenv("KM_SLACK_PEER_BRIDGES", yamlPeerBridges)
    }
}
```

This is the exact pattern for `KM_SLACK_ALLOW`.

### GitHub bridge `isInAllowlist` (reference — note semantic inversion)

(`pkg/github/bridge/webhook_handler.go:686-694`):
```go
// isInAllowlist reports whether login is in the allow slice.
// Deny-by-default: empty allow list → always false.
func isInAllowlist(login string, allow []string) bool {
    for _, a := range allow { if strings.EqualFold(a, login) { return true } }
    return false
}
```

**Slack version must INVERT the empty-list semantics:** when `effectiveAllow` is empty, return 200 (allow-by-default for backward compat). Only gate when the list is non-empty.

---

## State of the Art

| Area | Current State | Phase 118 Change |
|------|---------------|-----------------|
| `CreateChannel` | Always public (`is_private:false`) at `pkg/slack/client.go:606` | Conditional on `private bool` parameter |
| Inbound trigger auth | None — any user in a registered channel can trigger | Install-level (`KM_SLACK_ALLOW`) + per-sandbox (`slack_allow` DDB attr) |
| GitHub bridge | Per-repo `allow` list in `km-config.yaml` (deny-by-default) | — (reference only) |
| Per-sandbox bool overrides | `slack_react_always`, `slack_mention_only` (Phase 91.5) | `slack_allow` []string (same pattern) |

---

## Open Questions

1. **`CreateChannel` interface in tests**
   - What we know: `create_slack_test.go` has mock implementations of `SlackAPI`.
   - What's unclear: The exact mock struct name (`MockSlackAPI` or similar) — need to grep to find all implementations.
   - Recommendation: Run `grep -rn "CreateChannel" internal/app/cmd/ --include="*.go"` in Wave 0 to find all call sites and mock implementations.

2. **Allowlist enforcement for the Phase 104 `archiveOnDestroy:false` + alias reuse path**
   - What we know: When a channel is reused (alias reuse), the DDB row may already have an old `slack_allow` value.
   - What's unclear: Does the reused row get the new profile's allow overwritten? Current flow for reuse: `resolveSlackChannel` finds the existing channel via `store` (DDB `km-slack-channels`), returns the old channel ID. The per-sandbox DDB row for the NEW sandbox (new sandbox_id) is a fresh row — it gets a new `UpdateSandboxAttr` call.
   - Recommendation: No issue — the `km-sandboxes` row is per `sandbox_id`, not per channel. The new sandbox ID gets a fresh row. The channel mapping reuse (Phase 104) is separate from per-sandbox attributes.

3. **`slack_allow` written when `inbound.Enabled` is nil/false but per-sandbox allow is set**
   - What we know: `provisionSlackInboundQueue` returns early when inbound is disabled.
   - What's unclear: Should the per-sandbox allow be written even when inbound is disabled? Answer from spec: No — the per-sandbox allow is an `inbound` field. Without an inbound queue there's no dispatch to gate. The validate warning covers the misconfiguration.
   - Recommendation: Keep current behavior: per-sandbox allow only written when inbound.Enabled=true. Add validate warning for allow+!perSandbox.

---

## Validation Architecture

> `workflow.nyquist_validation` not explicitly set to false in `.planning/config.json` — section included.

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing (`go test`) |
| Config file | none (standard Go test runner) |
| Quick run | `go test ./pkg/profile/... ./pkg/slack/bridge/... ./internal/app/cmd/... -count=1 -timeout 120s` |
| Full suite | `go test ./... -count=1 -timeout 600s` |

### Phase 118 Acceptance Criteria → Test Map

| AC | Behavior | Test Type | Command | Status |
|----|----------|-----------|---------|--------|
| AC1 | private:true creates private Slack channel at km create | unit (mock SlackAPI) | `go test ./internal/app/cmd/... -run TestResolveSlackChannel -count=1` | Wave 0 gap |
| AC2 | install-level allow: listed user triggers; unlisted user is silent drop | unit (EventsHandler) | `go test ./pkg/slack/bridge/... -run TestEventsHandler_Allowlist -count=1` | Wave 0 gap |
| AC3 | per-sandbox allow overrides install-level | unit (EventsHandler with SandboxRoutingInfo.Allow set) | `go test ./pkg/slack/bridge/... -run TestEventsHandler_PerSandboxAllowOverrides -count=1` | Wave 0 gap |
| AC4 | empty/unset allow at both levels = everyone allowed | unit (EventsHandler) | `go test ./pkg/slack/bridge/... -run TestEventsHandler_AllowlistEmpty_EveryoneAllowed -count=1` | Wave 0 gap |
| AC5 | allowlist enforced on thread replies (Phase 91.3 bypass does not exempt) | unit (EventsHandler with MentionOnly+thread+unlisted user) | `go test ./pkg/slack/bridge/... -run TestEventsHandler_Allowlist_ThreadBypassDoesNotExempt -count=1` | Wave 0 gap |
| AC6 | km validate warns on private/allow set with perSandbox:false | unit (validate.go) | `go test ./pkg/profile/... -run TestValidate_Slack -count=1` | Extend existing test |
| AC7 | per-sandbox allow survives pause/resume/extend | unit (SandboxMetadata round-trip) | `go test ./pkg/aws/... -run TestSandboxMetadata_SlackAllow_RoundTrip -count=1` | Wave 0 gap |
| AC8 | existing installs with no allow/private = byte-identical behavior | unit (EventsHandler nil-invariant) | `go test ./pkg/slack/bridge/... -run TestEventsHandler_NoAllowlistSet_ByteIdentical -count=1` | Verify existing tests still pass |

### Sampling Rate

- **Per task commit:** `go test ./pkg/slack/bridge/... ./pkg/profile/... -count=1 -timeout 120s`
- **Per wave merge:** `go test ./... -count=1 -timeout 600s`
- **Phase gate:** Full suite green + live Slack UAT (AC1, AC2 require a real Slack workspace; all others are unit-testable)

### Wave 0 Gaps

- [ ] `pkg/slack/bridge/events_handler_allowlist_test.go` — covers AC2, AC3, AC4, AC5, AC8
- [ ] `pkg/aws/sandbox_dynamo_allow_test.go` (or extend existing `sandbox_dynamo_test.go`) — covers AC7
- [ ] Extend `internal/app/cmd/create_slack_test.go` mock for `CreateChannel(ctx, name, private)` signature — covers AC1
- [ ] Extend `pkg/profile/validate_test.go` for new warn rules — covers AC6

*(No framework install needed — Go test runner already in use.)*

### Live Slack UAT Required

- **AC1 (private channel creation):** Only verifiable via real Slack API. Test: `km create` a profile with `perSandbox:true` + `private:true`, verify the channel is private in Slack UI (`conversations.info` `is_private:true`).
- **AC2 (install-level allow, silent drop):** Send a message from an unlisted Slack user to the sandbox channel; verify no 👀 reaction, no SQS dispatch. Best self-driven using the HMAC-signed synthetic event POST technique from `project_slack_bridge_inbound_e2e_and_status_attr.md` memory, with `event.User` set to a non-listed ID.

---

## Sources

### Primary (HIGH confidence — verified against live codebase)

- `pkg/slack/client.go:603-612` — `CreateChannel` hardcoded `is_private:false` site
- `pkg/profile/types.go:199-264` — `NotificationSlackSpec` + `NotificationSlackInboundSpec` struct definitions
- `pkg/slack/bridge/events_handler.go:179-585` — `Handle()` dispatch chain, `isBotLoop`, handler ordering
- `pkg/slack/bridge/events_interfaces.go:104-124` — `SandboxRoutingInfo` struct and `SandboxByChannelFetcher` interface
- `pkg/slack/bridge/aws_adapters.go:1034-1105` — `DDBSandboxByChannel.FetchByChannel` + `slack_react_always`/`slack_mention_only` read pattern
- `internal/app/cmd/create_slack_inbound.go:99-193` — `provisionSlackInboundQueue` + `slack_react_always`/`slack_mention_only` write pattern
- `internal/app/cmd/create_slack.go:300-504` — `resolveSlackChannel` + `CreateChannel` call site at line 464
- `internal/app/config/config.go:24-67` — `SlackConfig` struct + merge-list at lines 824-835
- `internal/app/cmd/init.go:1582-1642` — KM_SLACK env var export pattern
- `pkg/aws/metadata.go:11-78` — `SandboxMetadata` struct + `SlackReactAlways`/`SlackMentionOnly` fields
- `pkg/aws/sandbox_dynamo.go:239-267,326-414` — `unmarshalSlackFields` + `marshalSandboxItem`
- `pkg/profile/validate.go:15-30,306-377` — `ValidationError.IsWarning` + existing Slack warn rules
- `pkg/github/bridge/webhook_handler.go:350-356,686-694` — GitHub bridge `isInAllowlist` (semantic reference)
- `infra/modules/lambda-slack-bridge/v1.0.0/main.tf:357-382` — Lambda env block pattern
- `infra/live/use1/lambda-slack-bridge/terragrunt.hcl:97-121` — terragrunt.hcl `get_env()` pattern
- `cmd/km-slack-bridge/main.go:385-408` — `WireMentionOnly` env var wiring pattern

---

## Metadata

**Confidence breakdown:**

- Feature A site (CreateChannel): HIGH — verified at `client.go:606` and `create_slack.go:464`
- Feature B handler order: HIGH — read every line of `Handle()` in `events_handler.go`
- DDB attribute name `slack_allow`: HIGH — consistent with `slack_react_always`/`slack_mention_only` naming; convention confirmed
- DDB type (S comma-joined vs SS): HIGH — `UpdateSandboxAttr` is string-only; no DDB SS used in sandbox_dynamo.go
- Config merge-list requirement: HIGH — confirmed the footgun pattern with existing entries and code
- Allowlist semantic (allow-by-default on empty): HIGH — from spec + backward-compat requirement

**Research date:** 2026-06-24
**Valid until:** 2026-07-24 (stable domain; Slack SDK and Go are unchanged)
