# Phase 48: Profile Override Flags for km create — Research

**Researched:** 2026-04-07
**Domain:** CLI flag parsing, profile mutation, lifecycle scheduling, idle detection loop
**Confidence:** HIGH — all findings from direct codebase inspection

## Summary

Phase 48 adds `--ttl` and `--idle` flags to `km create` so operators can override `spec.lifecycle.ttl` and `spec.lifecycle.idleTimeout` at the command line without editing the YAML profile. The critical user requirement is that `--ttl 0` means "no self-destroy": on each idle interval the sandbox hibernates/stops instead of destroying, and after activity resumes the detector re-arms and hibernates again on the next idle — an indefinite hibernate-on-idle loop rather than a one-shot destroy.

The implementation touches four layers: (1) CLI flag declaration in `NewCreateCmd`, (2) profile field mutation before `compiler.Compile`, (3) TTL EventBridge schedule creation (skip entirely when TTL=0), and (4) the audit-log sidecar's idle-detection loop (must not `cancel()` after one idle when TTL=0 — must re-arm).

**Primary recommendation:** Mutate `resolvedProfile.Spec.Lifecycle` fields after flag parse, before `compiler.Compile`. For `--ttl 0` specifically, skip EventBridge schedule creation and inject a special `IDLE_ACTION=hibernate` env var into the sidecar so the sidecar loops rather than exiting.

---

## Standard Stack

This phase is Go CLI work only. No new libraries required — all patterns already exist in the codebase.

| Component | Location | Purpose |
|-----------|----------|---------|
| Cobra flag API | `github.com/spf13/cobra` | `cmd.Flags().StringVar(...)` for `--ttl` / `--idle` |
| `time.ParseDuration` | stdlib | Parse "3h", "30m" into duration |
| `profile.LifecycleSpec` | `pkg/profile/types.go` | Struct fields to mutate |
| `compiler.Compile` | `pkg/compiler/` | Consumes mutated profile |
| `parseIdleTimeoutMinutes` | `pkg/compiler/userdata.go:1562` | Converts duration string to int minutes for sidecar |
| `awspkg.CreateTTLSchedule` | `pkg/aws/` | Called when TTL != 0 |
| `PublishSandboxIdleEvent` | `pkg/aws/idle_event.go:71` | Fires EventBridge on idle |
| `IdleDetector.Run` | `pkg/lifecycle/idle.go:69` | In-sandbox idle poll loop |

---

## Architecture Patterns

### How `km create` Currently Works (Relevant Path)

```
NewCreateCmd (create.go:79)
  flags declared: --on-demand, --alias, --substrate, --no-bedrock, etc.
  RunE dispatches to runCreateRemote OR runCreate

runCreate (create.go:167)
  Step 2-3: Parse + resolve profile inheritance
  Step 4:   Generate sandboxID
  resolvedProfile.Spec.Lifecycle.TTL is now immutable
  Step 7:   compiler.Compile(resolvedProfile, ...) — userdata baked
  Step 11:  WriteSandboxMetadataDynamo — stores IdleTimeout, TTLExpiry
  Step 12:  Create EventBridge TTL schedule (if ttlExpiry != nil)
```

The correct mutation point is **after profile resolution (Step 3) and before compiler.Compile (Step 7)**. This is the same pattern used for `--on-demand` (mutates `resolvedProfile.Spec.Runtime.Spot = false`) and `--no-bedrock` (mutates `resolvedProfile.Spec.Execution.UseBedrock = false`).

### Pattern: Flag → Profile Mutation Before Compile

```go
// Existing pattern (create.go:229-234)
substrate := resolvedProfile.Spec.Runtime.Substrate
if substrateOverride != "" {
    substrate = substrateOverride
    resolvedProfile.Spec.Runtime.Substrate = substrateOverride
}
spot := resolvedProfile.Spec.Runtime.Spot && !onDemand
```

New flags follow the same pattern:

```go
// After Step 3 validation, before Step 7:
if ttlOverride != "" {
    resolvedProfile.Spec.Lifecycle.TTL = ttlOverride
}
if idleOverride != "" {
    resolvedProfile.Spec.Lifecycle.IdleTimeout = idleOverride
}
// Re-parse ttlExpiry after mutation (currently computed at create.go:537-545)
```

### How TTL Expiry Becomes an EventBridge Schedule

In `create.go:537-545`:
```go
var ttlExpiry *time.Time
if resolvedProfile.Spec.Lifecycle.TTL != "" {
    if d, parseErr := time.ParseDuration(resolvedProfile.Spec.Lifecycle.TTL); parseErr == nil {
        t := now.Add(d)
        ttlExpiry = &t
    }
}
```

Then at `create.go:702`:
```go
if ttlExpiry != nil && ttlLambdaARN != "" && schedulerRoleARN != "" {
    schedInput := compiler.BuildTTLScheduleInput(sandboxID, *ttlExpiry, ...)
    awspkg.CreateTTLSchedule(ctx, schedulerClient, schedInput)
}
```

For `--ttl 0`: `time.ParseDuration("0")` succeeds (returns 0), so `now.Add(0)` = now, which would create an immediately-firing schedule. **This is wrong.** The fix: treat TTL=0 (or TTL="" after override) as "no schedule". Check for `d == 0` and leave `ttlExpiry = nil`.

### How Idle Detection Works (Current — One-Shot)

The audit-log sidecar (`sidecars/audit-log/cmd/main.go:87-108`) starts an `IdleDetector`. When idle is detected, the `OnIdle` callback:
1. Publishes a `SandboxIdle` EventBridge event → ttl-handler Lambda → `handleDestroy` or `handleStop` (depending on `teardownPolicy`)
2. Calls `cancel()` which stops the entire audit-log sidecar process

The `IdleDetector.Run` loop (`pkg/lifecycle/idle.go:69`) fires `OnIdle` **once then returns**. The sidecar exits after that.

### TTL=0 Behavior Requirement

When `--ttl 0`:
- No TTL EventBridge schedule created
- On idle: hibernate the sandbox (stop with hibernate flag), NOT destroy
- After the sandbox is hibernated/stopped, the idle detection must restart when the sandbox resumes
- This is a **loop**: hibernate on idle → activity resumes → detect idle again → hibernate again

The current code terminates the sidecar after one idle event. For TTL=0 the loop must continue.

**Implementation approach options:**

**Option A — IDLE_ACTION env var in userdata**
- In `userDataParams` add `IdleAction string` ("hibernate" when TTL=0, "stop" when teardownPolicy=stop, "destroy" otherwise)
- Sidecar reads `IDLE_ACTION`; when "hibernate" it does NOT call `cancel()` and re-arms the detector
- Requires sidecar code change

**Option B — Store TTL=0 flag in DynamoDB metadata**
- Store `TTLIsZero bool` in `SandboxMetadata` / `sandboxItemDynamo`
- ttl-handler reads this flag; when set, sends `stop` (hibernate) event instead of `destroy`, and does NOT delete the idle schedule
- No sidecar change needed, but idle detection still only fires once per sidecar process lifetime

**Option C — Hybrid: sidecar loops when IDLE_ACTION=hibernate**
- Most faithful to the stated requirement (the sidecar re-detects activity and hibernates again)
- Requires: sidecar change + compiler change to pass the env var

Option A/C is the correct implementation for the stated requirement. The sidecar `cancel()` call must be conditional.

### How `runCreateRemote` Must Pass Overrides

`runCreateRemote` (`create.go:1452`) also parses and resolves the profile. Flag overrides must be applied in **both** `runCreate` and `runCreateRemote`. Currently `runCreateRemote` has its own copy of Steps 1-7 and does not accept the override parameters.

**The function signature must change** to accept `ttlOverride` and `idleOverride`:

```go
func runCreateRemote(cfg *config.Config, profilePath string, onDemand bool, noBedrock bool, awsProfile string, aliasOverride string, ttlOverride string, idleOverride string) error
```

Or alternatively, overrides are applied to the profile YAML before it is uploaded to S3, so the create-handler Lambda picks up the already-mutated profile. The simpler approach is to mutate `resolvedProfile` in `runCreateRemote` the same way as `runCreate`.

### DynamoDB Metadata Storage

`SandboxMetadata` in `pkg/aws/sandbox_dynamo.go` stores `IdleTimeout string` (line 52). When `--idle` overrides the profile value, the metadata written at Step 11 already uses `resolvedProfile.Spec.Lifecycle.IdleTimeout` — so mutation before compile is sufficient. No DynamoDB schema change needed.

`TTLExpiry *time.Time` is also derived from the (mutated) profile at Step 11. No schema change needed.

### Semantic Validation Guard

`ValidateSemantic` (`pkg/profile/validate.go:217`) enforces: TTL must not be shorter than idleTimeout. After applying flag overrides, the code must re-run `profile.ValidateSemantic(resolvedProfile)` (or an inline check) to catch conflicts like `--ttl 1h --idle 2h`.

Exception: `--ttl 0` is the special "no TTL" sentinel. The validation rule `if ttl < idle` is only meaningful when `ttl > 0`. The guard should be: when TTL=0, skip this check.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead |
|---------|-------------|-------------|
| Duration parsing | Custom parser | `time.ParseDuration` (handles "3h", "30m", "1h30m") |
| Flag registration | Manual arg parsing | Cobra `cmd.Flags().StringVar` |
| EventBridge schedule | Direct API call | `awspkg.CreateTTLSchedule` (existing) |
| Sidecar idle callback | New idle implementation | Extend `IdleDetector.Run` and `OnIdle` callback |

---

## Common Pitfalls

### Pitfall 1: TTL=0 Creates Immediate EventBridge Schedule
**What goes wrong:** `time.ParseDuration("0")` = 0 duration, `now.Add(0)` = now, `ttlExpiry != nil` → schedule fires immediately → sandbox destroyed right after creation.
**Prevention:** After parsing TTL, check `d == 0` and treat as "no TTL" (leave `ttlExpiry = nil`). The sentinel value for "no TTL" should be `--ttl 0` or `--ttl ""`.

### Pitfall 2: Semantic Validation Fails After Override
**What goes wrong:** Profile has `ttl: 24h, idleTimeout: 4h`. Operator passes `--idle 48h` → semantic check fires: "ttl (24h) must not be shorter than idleTimeout (48h)".
**Prevention:** Re-run `profile.ValidateSemantic(resolvedProfile)` after applying overrides. Display clear error: "conflict: --idle 48h exceeds ttl 24h from profile. Add --ttl >= 48h".

### Pitfall 3: runCreateRemote Does Not Apply Overrides
**What goes wrong:** `runCreateRemote` has its own profile-parsing code. Overrides applied only in `runCreate` are silently lost on the remote path (default for EC2/ECS).
**Prevention:** Add `ttlOverride`, `idleOverride` parameters to `runCreateRemote` and apply the same mutations after resolution.

### Pitfall 4: Idle Loop Exits Sidecar on First Fire (TTL=0 Case)
**What goes wrong:** Current `OnIdle` calls `cancel()` which stops the audit-log sidecar. For `--ttl 0`, the sandbox is hibernated, the user resumes it, but idle detection never restarts because the sidecar exited.
**Prevention:** When `IDLE_ACTION=hibernate`, the `OnIdle` callback must NOT call `cancel()`. The `IdleDetector` must be re-created or reset after the hibernate action completes, so it re-arms for the next idle period.

### Pitfall 5: Profile YAML Stored to S3 Without Overrides
**What goes wrong:** `create.go:609` uploads the original `profilePath` file bytes to S3, not the mutated profile. The ttl-handler Lambda reads this stored profile for teardown policy and hibernation checks — it will see the un-overridden TTL/idle values.
**Prevention:** When overrides are applied, serialize `resolvedProfile` back to YAML before the S3 upload at Step 11b, or store a separate `--overrides.json` alongside. Simplest: marshal `resolvedProfile` and upload that instead of `profileYAML`.

### Pitfall 6: `--ttl 0` Accepted by `time.ParseDuration` But Not Meaningful as Duration
**What goes wrong:** "0" is a valid Go duration that equals zero. Code must specifically check for zero value as the no-TTL sentinel, not just check `err != nil`.
**Prevention:** `if ttlOverride == "0" || ttlOverride == "0s"` → set TTL to "" (empty = no schedule).

---

## Code Examples

### Flag Registration (pattern from existing flags)
```go
// In NewCreateCmd, after existing flag declarations:
var ttlOverride string
var idleOverride string
cmd.Flags().StringVar(&ttlOverride, "ttl", "",
    "Override spec.lifecycle.ttl (e.g. 3h, 30m). Use 0 to disable auto-destroy (hibernate on idle instead).")
cmd.Flags().StringVar(&idleOverride, "idle", "",
    "Override spec.lifecycle.idleTimeout (e.g. 30m, 2h).")
```

### Profile Mutation (after Step 3, before Step 7)
```go
// Apply TTL override — "0" means no auto-destroy
if ttlOverride != "" {
    if ttlOverride == "0" || ttlOverride == "0s" {
        resolvedProfile.Spec.Lifecycle.TTL = "" // empty = no TTL schedule
    } else {
        if _, err := time.ParseDuration(ttlOverride); err != nil {
            return fmt.Errorf("invalid --ttl value %q: %w", ttlOverride, err)
        }
        resolvedProfile.Spec.Lifecycle.TTL = ttlOverride
    }
}
if idleOverride != "" {
    if _, err := time.ParseDuration(idleOverride); err != nil {
        return fmt.Errorf("invalid --idle value %q: %w", idleOverride, err)
    }
    resolvedProfile.Spec.Lifecycle.IdleTimeout = idleOverride
}
// Re-validate semantic constraints after override
if semanticErrs := profile.ValidateSemantic(resolvedProfile); len(semanticErrs) > 0 {
    for _, e := range semanticErrs {
        fmt.Fprintf(os.Stderr, "ERROR: flag override conflict: %s\n", e.Error())
    }
    return fmt.Errorf("flag overrides conflict with profile values")
}
```

### userDataParams Extension for Idle Loop (TTL=0 case)
```go
// In userDataParams struct (pkg/compiler/userdata.go):
// IdleAction controls what the sidecar does when idle is detected.
// "destroy": publish idle event and exit (current default)
// "hibernate": publish stop event, re-arm detector, do NOT exit
IdleAction string
```

Template injection:
```
{{- if .IdleAction }}
Environment=IDLE_ACTION={{ .IdleAction }}
{{- end }}
```

### Sidecar OnIdle Callback for TTL=0 Loop (pseudo-code)
```go
// sidecars/audit-log/cmd/main.go — extended idle setup
idleAction := envOr("IDLE_ACTION", "destroy")

var startDetector func()
startDetector = func() {
    detector := newIdleDetector(sandboxID, idleMinutes, cwClient, cwLogGroup, "audit", func(id string) {
        if ebClient != nil {
            if idleAction == "hibernate" {
                kmaws.PublishSandboxCommand(ctx, ebClient, id, "stop", ...)
                // Re-arm: start a new detector after a delay for the instance to hibernate+resume
                time.AfterFunc(5*time.Minute, startDetector)
            } else {
                kmaws.PublishSandboxIdleEvent(ctx, ebClient, id)
                cancel() // exit as before
            }
        }
    })
    go detector.Run(ctx)
}
startDetector()
```

### S3 Profile Upload Must Use Mutated Profile
```go
// Step 11b — serialize mutated resolvedProfile, not raw profileYAML
import "github.com/goccy/go-yaml"
mutatedYAML, marshalErr := yaml.Marshal(resolvedProfile)
if marshalErr == nil {
    s3Client.PutObject(ctx, &s3.PutObjectInput{
        Bucket: aws.String(artifactBucket),
        Key:    aws.String("artifacts/" + sandboxID + "/.km-profile.yaml"),
        Body:   bytes.NewReader(mutatedYAML),
    })
}
```

---

## State of the Art

| Old Approach | Current Approach | Notes |
|--------------|-----------------|-------|
| Always destroy on idle | One-shot destroy or stop based on teardownPolicy | TTL=0 hibernate loop is new |
| Fixed profile values | Override at create time with flags | Phase 48 adds this |

---

## Open Questions

1. **What is the exact sentinel for "no TTL"?**
   - What we know: `--ttl 0` is the stated requirement. `time.ParseDuration("0")` = 0 (valid). TTL="" in the profile already means "no schedule".
   - What's unclear: Should `--ttl 0` set `Spec.Lifecycle.TTL = ""` or `"0"` in the profile stored to S3?
   - Recommendation: Set to `""` to be consistent with existing "no TTL" semantics. The stored profile should look the same as a profile authored without a TTL.

2. **How does the idle re-arm know the sandbox has resumed?**
   - What we know: The sidecar exits on cancel. When EC2 restarts after hibernation, the sidecar process is started fresh by systemd (Restart=always). So the sidecar already re-arms naturally on EC2 resume.
   - What's unclear: Does `Restart=always` in the systemd unit restart the sidecar after hibernation-triggered exit? If `cancel()` is NOT called (Option C), the detector continues running but the EC2 instance is stopped — there are no more CW events to read. The detector would keep polling and immediately find stale events from before hibernation, potentially re-triggering before the sandbox has done real work.
   - Recommendation: Do NOT call `cancel()` on hibernate. After sending the hibernate event, reset the detector's `startTime` and `lastEventTime` reference point so it waits for genuinely new activity. Alternatively, track a "last idle event time" and require new events after that timestamp.

3. **Does `--ttl` override need to be threaded through `km at` / `runCreateRemote`?**
   - What we know: `km at create` schedules a remote create by uploading the profile to S3 and calling the create-handler Lambda. The Lambda calls `km create --sandbox-id ... --local` which calls `runCreate`, not `NewCreateCmd`. Flags from the original `km at` command are NOT forwarded to the Lambda invocation.
   - Recommendation: For Phase 48, scope to direct `km create` only (local and remote-dispatch paths). The `km at create` path is out of scope — it uses pre-baked profile YAML.

4. **Validation: should `--ttl 0` with `--idle X` be accepted?**
   - What we know: When TTL=0, the semantic validation rule (TTL >= idleTimeout) does not apply.
   - Recommendation: Skip the TTL >= idleTimeout check when TTL=0. Both values are valid independently.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing + testify (stdlib `testing` package) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./internal/app/cmd/ ./pkg/compiler/ ./pkg/lifecycle/ -run TestCreate -v` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map
| Req | Behavior | Test Type | Automated Command |
|-----|----------|-----------|-------------------|
| --ttl flag | `--ttl 3h` sets `Spec.Lifecycle.TTL = "3h"` in resolved profile before compile | unit | `go test ./internal/app/cmd/ -run TestCreateTTLOverride -v` |
| --idle flag | `--idle 30m` sets `Spec.Lifecycle.IdleTimeout = "30m"` | unit | `go test ./internal/app/cmd/ -run TestCreateIdleOverride -v` |
| --ttl 0 no-schedule | TTL=0 → `ttlExpiry = nil`, no EventBridge schedule created | unit | `go test ./internal/app/cmd/ -run TestCreateTTLZeroNoSchedule -v` |
| conflict guard | `--idle 48h` with profile `ttl: 24h` → error | unit | `go test ./internal/app/cmd/ -run TestCreateOverrideConflict -v` |
| profile S3 upload | Mutated profile (with TTL override) stored to S3, not raw YAML | unit | `go test ./internal/app/cmd/ -run TestCreateMutatedProfileS3Upload -v` |
| sidecar idle action | `IDLE_ACTION=hibernate` → sidecar re-arms, does not exit | unit | `go test ./sidecars/audit-log/... -run TestIdleActionHibernate -v` |
| compiler IdleAction | TTL=0 profile sets `IdleAction = "hibernate"` in userdata params | unit | `go test ./pkg/compiler/ -run TestIdleActionParam -v` |

### Wave 0 Gaps
- [ ] `internal/app/cmd/create_override_test.go` — covers --ttl, --idle, TTL=0, conflict guard
- [ ] `pkg/compiler/userdata_idle_action_test.go` — covers IdleAction param in userDataParams
- [ ] `sidecars/audit-log/cmd/idle_action_test.go` — covers IDLE_ACTION=hibernate loop

---

## Sources

### Primary (HIGH confidence)
All findings are from direct codebase inspection — no external sources needed for this phase.

- `internal/app/cmd/create.go` — full create flow, flag declarations, profile mutation patterns
- `pkg/profile/types.go` — LifecycleSpec, all profile struct fields
- `pkg/compiler/userdata.go` — userDataParams, IdleTimeoutMinutes, parseIdleTimeoutMinutes
- `pkg/lifecycle/idle.go` — IdleDetector.Run, one-shot behavior
- `sidecars/audit-log/cmd/main.go` — IDLE_TIMEOUT_MINUTES wiring, OnIdle callback, cancel() call
- `pkg/aws/idle_event.go` — PublishSandboxIdleEvent, PublishSandboxCommand
- `cmd/ttl-handler/main.go` — HandleTTLEvent, lookupTeardownPolicy, handleStop, handleDestroy
- `pkg/aws/sandbox_dynamo.go` — SandboxMetadata, stored fields

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all existing Go + Cobra patterns, no new libraries
- Architecture: HIGH — direct code read of every affected function
- Pitfalls: HIGH — derived from reading actual code paths, not assumptions

**Research date:** 2026-04-07
**Valid until:** 2026-05-07 (stable codebase)
