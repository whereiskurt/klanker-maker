# Phase 11: Sandbox Auto-Destroy & Metadata Wiring — Research

**Researched:** 2026-03-22
**Domain:** Go — wiring existing packages together; no new external libraries required
**Confidence:** HIGH (all findings from direct codebase inspection)

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| PROV-03 | `km list` returns accurate sandbox data from the same bucket `km create` writes to | Fix: thread `cfg.StateBucket` into `newRealLister`; remove `defaultStateBucket` const |
| PROV-04 | `km status` shows correct metadata from the same source as `km list` | Same bucket fix as PROV-03; `runStatus` already uses `defaultStateBucket` via `newRealFetcher` |
| PROV-05 | Sandbox auto-destroys after TTL expires | Fix: TTL Lambda must call `runner.Destroy()` after artifact upload; requires Lambda to reconstruct sandbox dir or use SSM/Step Functions |
| PROV-06 | Sandbox auto-destroys after idle timeout with no activity | Fix: `OnIdle` in audit-log sidecar calls `cancel()` only; must also call `ExecuteTeardown` via subprocess or AWS API |
</phase_requirements>

---

## Summary

Phase 11 closes four integration gaps identified in the v1.0 milestone audit. All gaps are wiring failures — the underlying machinery (terragrunt runner, lifecycle.ExecuteTeardown, S3 metadata, DI interfaces) is fully implemented and tested. No new packages, dependencies, or AWS services are needed.

**Gap 1 — Metadata bucket mismatch (PROV-03, PROV-04):** `create.go` writes `metadata.json` to `cfg.StateBucket` (gated on `KM_STATE_BUCKET` env var). `list.go` and `status.go` construct their real listers/fetchers using the hardcoded `const defaultStateBucket = "tf-km-state"`. The fix is to thread `cfg` into `runList` and `runStatus` so they construct the real lister/fetcher using `cfg.StateBucket` instead of the constant. When `cfg.StateBucket` is empty (operator hasn't set `KM_STATE_BUCKET`), the command should return an actionable error rather than silently querying the wrong bucket.

**Gap 2 — TTL Lambda doesn't destroy (PROV-05):** The Lambda uploads artifacts and sends notification but has a comment "delegated to a separate cleanup job — the Lambda purpose is artifact preservation and notification." That cleanup job does not exist. The Lambda already has an S3 client and the profile in hand. The cleanest approach is to add a `TeardownFunc` field to `TTLHandler` (DI, consistent with existing narrow-interface patterns) that invokes the actual destroy. In production `main()`, the teardown function should call `km destroy` as a subprocess (`exec.Command`). This matches how `destroy.go` and `runner.go` work — terragrunt is always invoked as a child process. The Lambda's IAM role must have permissions to assume the `klanker-terraform` profile, OR the Lambda is invoked with that role directly. Since Lambdas run with execution roles, the simplest approach is to invoke `terragrunt destroy` directly inside the Lambda binary (the Lambda already uses `KM_ARTIFACTS_BUCKET` env var; it needs `KM_SANDBOX_DIR` or equivalent to reconstruct the sandbox dir). **The key constraint:** the TTL Lambda binary does NOT have the `km` CLI binary. It must call `terragrunt` directly or use Go SDK calls. The correct design is to add a `TeardownFunc func(ctx context.Context, sandboxID string) error` callback to `TTLHandler` and implement it in `main()` by running `terragrunt destroy` using the same pattern as `pkg/terragrunt.Runner`.

**Gap 3 — Idle auto-destroy not triggered (PROV-06):** `IdleDetector.OnIdle` fires `cancel()` which exits the audit-log sidecar process. The EC2 instance keeps running. The audit-log sidecar runs INSIDE the sandbox EC2 instance — it cannot call `terragrunt destroy` from inside the sandbox (no repo, no credentials). The correct design is: `OnIdle` should signal the host/controller rather than self-exit. On EC2, the sidecar can call `lifecycle.ExecuteTeardown` by invoking `km destroy` via an SSM command or, more practically, by having the sidecar POST to an EventBridge endpoint with an "idle-destroy" event that triggers the TTL Lambda (or a new Lambda). The simplest correct approach given the existing architecture is: the sidecar sends a "sandbox_idle" event to EventBridge via AWS SDK, which triggers the same TTL Lambda (or a thin wrapper). Alternatively: the sidecar calls `aws ssm send-command` to invoke `km destroy <sandboxID>` on the EC2 instance itself — this works because the instance has the IAM role with SSM access. **Recommended approach:** The sidecar publishes an EventBridge event (`km:sandbox-idle`) with the `sandboxID`. The TTL Lambda (already subscribed to EventBridge) handles this event type by calling `ExecuteTeardown` with policy=destroy. This reuses the TTL Lambda infrastructure with minimal new code.

**Gap 4 (out of Phase 11 scope):** BUDG-08 ECS re-provisioning is deferred to Phase 12 per the audit and requirements traceability.

**Primary recommendation:** Fix all four gaps by wiring existing code — no new packages or AWS services needed. The TTL Lambda needs a `TeardownFunc` DI field populated in `main()` with a terragrunt destroy invocation. The idle path needs to publish an EventBridge event instead of (or in addition to) calling `cancel()`. The list/status bucket mismatch is a one-line config threading fix.

---

## Standard Stack

### Core (already present — no new dependencies)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `pkg/lifecycle` | local | `ExecuteTeardown`, `IdleDetector` | Already implements all teardown policies |
| `pkg/terragrunt` | local | `Runner.Destroy()` | Already wraps `terragrunt destroy -auto-approve` |
| `pkg/aws` | local | `DeleteTTLSchedule`, `UploadArtifacts`, `SendLifecycleNotification` | All pieces already exist |
| `github.com/aws/aws-sdk-go-v2/service/eventbridge` | existing | Publish EventBridge event from idle sidecar | Already in go.sum via budget-enforcer |
| `internal/app/config` | local | `Config.StateBucket` | Config already has the correct bucket value |

### No New Dependencies

This phase is entirely wiring existing packages. `go.mod` does not need changes.

## Architecture Patterns

### Pattern 1: Config Threading (PROV-03 / PROV-04 fix)

**What:** Pass `cfg *config.Config` into `runList` and `runStatus` so the real lister/fetcher reads `cfg.StateBucket` instead of `defaultStateBucket`.

**Current broken code in `list.go`:**
```go
const defaultStateBucket = "tf-km-state"  // WRONG — hardcoded

func runList(cmd *cobra.Command, lister SandboxLister, jsonOutput, useTagScan bool) error {
    if lister == nil {
        // ...
        lister = newRealLister(awsCfg, defaultStateBucket)  // bug: uses constant
    }
```

**Fixed pattern:**
```go
// runList now accepts cfg to read StateBucket
func runList(cmd *cobra.Command, cfg *config.Config, lister SandboxLister, ...) error {
    if lister == nil {
        bucket := cfg.StateBucket
        if bucket == "" {
            return fmt.Errorf("KM_STATE_BUCKET is not set — run 'km configure' to set the state bucket")
        }
        lister = newRealLister(awsCfg, bucket)
    }
```

The same fix applies to `runStatus` for `newRealFetcher`. The existing `NewListCmdWithLister` and `NewStatusCmdWithFetcher` signatures already accept `cfg *config.Config` — they just don't pass it to the `runList`/`runStatus` functions.

**Test impact:** Existing tests inject a fake lister/fetcher — they are unaffected. New tests verify the real path uses `cfg.StateBucket`.

### Pattern 2: TTL Lambda TeardownFunc DI (PROV-05 fix)

**What:** Add `TeardownFunc func(ctx context.Context, sandboxID string) error` to `TTLHandler`. Populate in `main()` with a closure that runs `terragrunt destroy` using the Runner pattern.

**Key design constraints discovered in code:**
- The Lambda binary does NOT include the `km` CLI binary — only the Lambda handler binary is deployed
- The Lambda does NOT have the local `infra/live/` sandbox directory tree — it runs in AWS
- `runner.Destroy()` requires a local `sandboxDir` with a valid `terragrunt.hcl` — unavailable in Lambda
- The Lambda DOES have the sandbox profile YAML from S3 and the `sandboxID`

**Correct implementation:** The `TeardownFunc` in the Lambda `main()` must:
1. Download the profile from S3 (already done in Step 2 of `HandleTTLEvent`)
2. Create a temporary directory and write a minimal `service.hcl` (same pattern as `destroy.go` Step 4 for missing sandbox dirs)
3. Run `terragrunt destroy -auto-approve` in that temp dir
4. Clean up the temp dir

This mirrors the `runDestroy` pattern for missing sandbox directories (lines 100-119 in `destroy.go`).

**TTLHandler struct after fix:**
```go
type TTLHandler struct {
    S3Client      S3GetPutAPI
    SESClient     SESV2API
    Scheduler     SchedulerAPI
    Bucket        string
    OperatorEmail string
    Domain        string
    // NEW: TeardownFunc is called after artifact upload to destroy sandbox resources.
    // If nil, teardown is skipped (backward compatible with existing tests).
    TeardownFunc  func(ctx context.Context, sandboxID string) error
}
```

**In HandleTTLEvent, add Step 5.5 after schedule deletion:**
```go
// Step 5.5: Destroy sandbox resources (PROV-05).
if h.TeardownFunc != nil {
    if teardownErr := h.TeardownFunc(ctx, sandboxID); teardownErr != nil {
        log.Error().Err(teardownErr).Str("sandbox_id", sandboxID).
            Msg("sandbox teardown failed after TTL expiry")
        return teardownErr
    }
    log.Info().Str("sandbox_id", sandboxID).Msg("sandbox resources destroyed after TTL expiry")
}
```

**Existing tests are unaffected** because `TeardownFunc` is nil in all existing test setups — the nil check preserves backward compatibility.

### Pattern 3: Idle Sidecar EventBridge Publishing (PROV-06 fix)

**What:** When `IdleDetector.OnIdle` fires inside the audit-log sidecar, instead of only calling `cancel()`, the sidecar publishes an EventBridge "sandbox_idle" event. A Lambda (the existing TTL handler, updated to handle both event shapes, or a new thin idle-handler Lambda) receives this and calls `ExecuteTeardown`.

**Why EventBridge (not SSM send-command):**
- EventBridge is already used in this project (TTL scheduling via EventBridge Scheduler)
- The sidecar already has AWS credentials via the EC2 instance profile
- SSM send-command would require `ssm:SendCommand` + polling which is more complex
- EventBridge PutEvents is fire-and-forget — sidecar can then `cancel()` and exit

**EventBridge event shape:**
```json
{
  "source": "km.sandbox",
  "detail-type": "SandboxIdle",
  "detail": {
    "sandbox_id": "sb-aabbccdd"
  }
}
```

**In `audit-log/cmd/main.go`, the `newIdleDetector` callback becomes:**
```go
func(id string) {
    log.Warn().Str("sandbox_id", id).Msg("audit-log: sandbox idle timeout reached, publishing idle event")
    publishIdleEvent(ctx, id, region)  // NEW: publishes EventBridge event
    cancel()  // existing: exits sidecar
}
```

**`publishIdleEvent` uses EventBridge PutEvents SDK call.** The EC2 instance profile needs `events:PutEvents` permission — this must be added to the sandbox IAM role in the compiler.

**EventBridge rule:** A new EventBridge rule listens for `source = "km.sandbox"` and `detail-type = "SandboxIdle"`, invoking either a new `idle-handler` Lambda or the existing TTL Lambda (extended to handle both `TTLEvent` and `IdleEvent`).

**Simpler alternative (single Lambda approach):** Extend the TTL Lambda to handle an `IdleEvent` shape. The EventBridge rule targets the same Lambda. The Lambda checks which event type was received and calls the same teardown logic.

**Recommended approach:** Single Lambda, extended to handle both event types. This avoids deploying a new Lambda function.

### Pattern 4: Idle Handler Lambda or Extended TTL Lambda

**If extending TTL Lambda:**
```go
// TTLLambdaEvent can be either a TTLEvent or IdleEvent — both have sandbox_id
type TTLLambdaEvent struct {
    SandboxID  string `json:"sandbox_id"`
    EventType  string `json:"event_type,omitempty"` // "ttl" or "idle"; empty = "ttl" for backward compat
}
```

The handler checks `EventType` and calls the same teardown path. Artifact upload + notification + schedule deletion + `TeardownFunc` all apply to both TTL and idle paths.

### Recommended Project Structure (unchanged)

```
cmd/ttl-handler/
  main.go              # Add TeardownFunc, handle IdleEvent shape
  main_test.go         # Add tests for teardown path, idle event path

internal/app/cmd/
  list.go              # Thread cfg.StateBucket into runList
  status.go            # Thread cfg.StateBucket into runStatus
  list_test.go         # Add test: real path uses cfg.StateBucket
  status_test.go       # Add test: real path uses cfg.StateBucket

sidecars/audit-log/cmd/
  main.go              # Add publishIdleEvent call in OnIdle callback

pkg/aws/ or pkg/events/
  idle_event.go        # publishIdleEvent(ctx, sandboxID, region) — EventBridge PutEvents

infra/modules/sandbox-iam/
  main.tf              # Add events:PutEvents to sandbox instance IAM role
```

### Anti-Patterns to Avoid

- **Don't call `km destroy` as a subprocess from the Lambda:** The Lambda binary is a standalone binary deployed without the `km` CLI. Use `pkg/terragrunt.Runner` logic directly (create temp dir, write minimal HCL, exec terragrunt).
- **Don't block the sidecar's `cancel()` call waiting for destroy confirmation:** The sidecar publishes the idle event and exits. The Lambda handles actual destroy asynchronously.
- **Don't skip the nil check on `TeardownFunc`:** Existing tests pass `nil` — the nil guard preserves backward compatibility.
- **Don't use a separate `defaultStateBucket` const:** Delete it entirely; if `cfg.StateBucket` is empty, return a clear error.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Destroying sandbox from Lambda | Custom AWS SDK teardown sequence | `terragrunt.Runner` pattern with temp dir | Terraform state management, resource ordering, and idempotency are handled by Terragrunt |
| Detecting idle from sidecar | Polling instance metadata or tags | EventBridge PutEvents (fire-and-forget) | Decouples sidecar from destroy logic; sidecar has no network to the km control plane |
| Bucket configuration | Hard-coded bucket name | `cfg.StateBucket` from `config.Load()` | Config already reads `KM_STATE_BUCKET` env var |
| New lifecycle orchestration | Step Functions or new state machine | Existing `lifecycle.ExecuteTeardown` | Already handles all teardown policies with correct callback ordering |

## Common Pitfalls

### Pitfall 1: Missing TeardownFunc in Lambda main() Populates Nil
**What goes wrong:** If `TeardownFunc` is not populated in `main()`, the nil check silently skips teardown. The Lambda "works" (no error) but never destroys.
**How to avoid:** Add a startup check — if `KM_TEARDOWN_ENABLED=true` (or similar env var), assert `TeardownFunc != nil` before registering with `lambda.Start`.
**Warning signs:** Lambda logs show "TTL handler completed" but no "sandbox resources destroyed" log line.

### Pitfall 2: Temp Dir Terragrunt Lacks site.hcl Inheritance
**What goes wrong:** The Lambda creates a temp `service.hcl` but terragrunt expects to walk up and find `site.hcl` with account IDs, state backend config, etc. Without the repo's `infra/live/use1/site.hcl`, `terragrunt destroy` fails with missing config.
**Why it happens:** `terragrunt.hcl` uses `find_in_parent_folders()` to locate `site.hcl`. The temp dir has no parent hierarchy.
**How to avoid:** The Lambda must either (a) write a complete, self-contained `terragrunt.hcl` without parent folder references, or (b) reconstruct a partial repo-like directory tree. Option (a) is correct: write a `terragrunt.hcl` that inlines the backend config using `remote_state` directly (no `find_in_parent_folders`). Inspect the existing `infra/live/use1/site.hcl` to understand what must be inlined.
**Warning signs:** `Error: could not find a site.hcl in any of the parent directories` in Lambda CloudWatch logs.

### Pitfall 3: EventBridge PutEvents Requires events:PutEvents on Instance Role
**What goes wrong:** The sidecar calls `events:PutEvents` but the sandbox EC2 instance profile doesn't have that permission. The call fails silently (logged as warning), idle sidecar exits, but no destroy event is published.
**Why it happens:** The compiler generates IAM policies from the profile; `events:PutEvents` is not currently in the baseline.
**How to avoid:** Add `events:PutEvents` to the generated instance profile in `pkg/compiler`. This is a minimal permission (no resource-level narrowing needed for PutEvents to the default event bus).
**Warning signs:** CloudWatch shows `AccessDeniedException` when sidecar calls PutEvents; no idle destroy events appear in EventBridge.

### Pitfall 4: cfg.StateBucket Empty Error Must Be Actionable
**What goes wrong:** When `KM_STATE_BUCKET` is not set, `km list` and `km status` return a cryptic error like "S3 access denied on empty bucket name".
**How to avoid:** Check `cfg.StateBucket == ""` before constructing the real lister/fetcher and return: `"KM_STATE_BUCKET is not configured — set it in km-config.yaml or via the KM_STATE_BUCKET environment variable"`.
**Warning signs:** Generic AWS SDK error instead of actionable config error.

### Pitfall 5: Lambda Destroy Must Delete TTL Schedule Before Terraform Destroy
**What goes wrong:** TTL schedule fires, Lambda starts destroy. If schedule is not deleted first, a second Lambda invocation fires mid-destroy, causing concurrent terragrunt operations against the same state.
**Why it happens:** EventBridge Scheduler fires once at the scheduled time, but retries on Lambda error. Concurrent invocations are possible.
**How to avoid:** Keep the existing Step 5 (DeleteTTLSchedule) BEFORE the new teardown step. This is already correctly ordered in the current `HandleTTLEvent` sequence — just ensure `TeardownFunc` call comes after.

## Code Examples

### Current Broken Pattern: Hardcoded Bucket
```go
// Source: internal/app/cmd/list.go (current, broken)
const defaultStateBucket = "tf-km-state"

func runList(cmd *cobra.Command, lister SandboxLister, jsonOutput, useTagScan bool) error {
    if lister == nil {
        lister = newRealLister(awsCfg, defaultStateBucket) // always "tf-km-state"
    }
```

### Fixed Pattern: Config-Driven Bucket
```go
// internal/app/cmd/list.go (fixed)
func runList(cmd *cobra.Command, cfg *config.Config, lister SandboxLister, jsonOutput, useTagScan bool) error {
    if lister == nil {
        bucket := cfg.StateBucket
        if bucket == "" {
            return fmt.Errorf("state bucket not configured: set KM_STATE_BUCKET or state_bucket in km-config.yaml")
        }
        awsProfile := "klanker-terraform"
        awsCfg, err := kmaws.LoadAWSConfig(cmd.Context(), awsProfile)
        if err != nil {
            return fmt.Errorf("load AWS config: %w", err)
        }
        lister = newRealLister(awsCfg, bucket)
    }
    // rest unchanged
```

### TTL Handler TeardownFunc Population in main()
```go
// cmd/ttl-handler/main.go — new teardownFunc using terragrunt runner pattern
teardownFn := func(ctx context.Context, sandboxID string) error {
    // Create temp dir with minimal service.hcl for state resolution
    tmpDir, err := os.MkdirTemp("", "km-ttl-"+sandboxID)
    if err != nil {
        return fmt.Errorf("create temp dir for destroy: %w", err)
    }
    defer os.RemoveAll(tmpDir)

    // Write self-contained terragrunt.hcl (inlines backend config, no parent folders)
    hcl := buildLambdaTerragruntHCL(sandboxID, region, stateBucket)
    if err := os.WriteFile(filepath.Join(tmpDir, "terragrunt.hcl"), []byte(hcl), 0o644); err != nil {
        return fmt.Errorf("write terragrunt.hcl for destroy: %w", err)
    }

    cmd := exec.CommandContext(ctx, "terragrunt", "destroy", "-auto-approve")
    cmd.Dir = tmpDir
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    return cmd.Run()
}
```

### EventBridge Publish from Idle Sidecar
```go
// pkg/aws/idle_event.go (new)
// PublishSandboxIdleEvent sends a SandboxIdle event to EventBridge default bus.
func PublishSandboxIdleEvent(ctx context.Context, client EventBridgeAPI, sandboxID string) error {
    detail := fmt.Sprintf(`{"sandbox_id":%q,"event_type":"idle"}`, sandboxID)
    _, err := client.PutEvents(ctx, &eventbridge.PutEventsInput{
        Entries: []ebtypes.PutEventsRequestEntry{
            {
                Source:       aws.String("km.sandbox"),
                DetailType:   aws.String("SandboxIdle"),
                Detail:       aws.String(detail),
                EventBusName: aws.String("default"),
            },
        },
    })
    return err
}
```

## State of the Art

| Old Approach | Current Approach | Notes |
|--------------|------------------|-------|
| Hardcoded bucket constant | `cfg.StateBucket` from config | This phase closes the gap |
| TTL Lambda "delegates to cleanup job" | TTL Lambda calls teardown directly | This phase closes the gap |
| `OnIdle` calls `cancel()` only | `OnIdle` publishes EventBridge event + `cancel()` | This phase closes the gap |

## Open Questions

1. **Terragrunt site.hcl reconstruction in Lambda**
   - What we know: `terragrunt destroy` in the Lambda will fail if `find_in_parent_folders("site.hcl")` can't find the parent hierarchy
   - What's unclear: Whether the Lambda can write a fully self-contained `terragrunt.hcl` that bypasses parent folder lookups entirely, or whether it needs a different backend config
   - Recommendation: The planner should inspect `infra/live/use1/site.hcl` and `infra/live/use1/sandboxes/*/terragrunt.hcl` to determine what must be inlined. The destroy path in `destroy.go` (lines 100-119) shows the pattern for reconstructing a minimal sandbox dir — the Lambda needs the equivalent but self-contained.

2. **EventBridge rule and idle-handler Lambda IAM**
   - What we know: An EventBridge rule for `SandboxIdle` events must target the TTL Lambda (or a new Lambda)
   - What's unclear: Whether the existing TTL Lambda's IAM execution role has permissions for the full sandbox destroy (EC2 terminate, ECS stop, Terraform state access)
   - Recommendation: Check `infra/live/use1/ttl-handler/terragrunt.hcl` for the Lambda's IAM policy to confirm it can run `terragrunt destroy`.

3. **ECS idle detection**
   - What we know: The idle detector runs in the audit-log sidecar on EC2 (polls CloudWatch). For ECS, the audit-log sidecar is also in the Fargate task.
   - What's unclear: Whether an ECS Fargate task's sidecar container can call EventBridge PutEvents (it can, if the task role has `events:PutEvents`)
   - Recommendation: The ECS compiler path must also include `events:PutEvents` in the task IAM role.

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` (stdlib) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./cmd/ttl-handler/... ./internal/app/cmd/... ./pkg/lifecycle/... -run TestTTL\|TestList\|TestStatus\|TestIdle -v` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| PROV-03 | `km list` real path uses `cfg.StateBucket`, not hardcoded const | unit | `go test ./internal/app/cmd/... -run TestListCmd_RealBucketFromConfig -v` | ❌ Wave 0 |
| PROV-03 | `km list` returns error when `cfg.StateBucket` is empty | unit | `go test ./internal/app/cmd/... -run TestListCmd_EmptyStateBucketError -v` | ❌ Wave 0 |
| PROV-04 | `km status` real path uses `cfg.StateBucket`, not hardcoded const | unit | `go test ./internal/app/cmd/... -run TestStatusCmd_RealBucketFromConfig -v` | ❌ Wave 0 |
| PROV-05 | TTL Lambda calls `TeardownFunc` after artifact upload | unit | `go test ./cmd/ttl-handler/... -run TestHandleTTLEvent_CallsTeardownFunc -v` | ❌ Wave 0 |
| PROV-05 | TTL Lambda teardown is skipped when `TeardownFunc` is nil (backward compat) | unit | `go test ./cmd/ttl-handler/... -run TestHandleTTLEvent_NoTeardownWhenNil -v` | ❌ Wave 0 |
| PROV-05 | TTL Lambda returns error when teardown fails | unit | `go test ./cmd/ttl-handler/... -run TestHandleTTLEvent_TeardownFailureReturnsError -v` | ❌ Wave 0 |
| PROV-06 | `OnIdle` publishes EventBridge idle event (via injected publisher) | unit | `go test ./pkg/aws/... -run TestPublishSandboxIdleEvent -v` | ❌ Wave 0 |
| PROV-06 | Idle sidecar invokes EventBridge publish before cancel() | unit | `go test ./sidecars/audit-log/... -run TestIdlePublishesEvent -v` | ❌ Wave 0 |

### Sampling Rate

- **Per task commit:** `go test ./cmd/ttl-handler/... ./internal/app/cmd/... -v`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `internal/app/cmd/list_test.go` — add `TestListCmd_RealBucketFromConfig` and `TestListCmd_EmptyStateBucketError`
- [ ] `internal/app/cmd/status_test.go` — add `TestStatusCmd_RealBucketFromConfig`
- [ ] `cmd/ttl-handler/main_test.go` — add teardown-related tests (TeardownFunc called, nil guard, error return)
- [ ] `pkg/aws/idle_event.go` + test — `PublishSandboxIdleEvent` with narrow `EventBridgeAPI` interface
- [ ] `sidecars/audit-log/cmd/main.go` — idle sidecar test for EventBridge publish (integration-level, may be manual-only)

*Existing test files: `cmd/ttl-handler/main_test.go`, `internal/app/cmd/list_test.go`, `internal/app/cmd/status_test.go`, `pkg/lifecycle/idle_test.go` all exist — new tests are additions to existing files.*

---

## Sources

### Primary (HIGH confidence — direct codebase inspection)

- `cmd/ttl-handler/main.go` — TTL Lambda implementation, missing teardown step confirmed
- `pkg/lifecycle/idle.go` — `IdleDetector.OnIdle` fires `cancel()` only, confirmed
- `sidecars/audit-log/cmd/main.go` — idle callback wiring, `cancel()` only, confirmed
- `internal/app/cmd/list.go` — `const defaultStateBucket = "tf-km-state"` hardcoded, confirmed
- `internal/app/cmd/status.go` — uses `defaultStateBucket` in `newRealFetcher`, confirmed
- `internal/app/cmd/create.go` — writes metadata to `cfg.StateBucket` (gated), confirmed
- `internal/app/config/config.go` — `StateBucket` field present, loaded from `KM_STATE_BUCKET` env, confirmed
- `pkg/lifecycle/teardown.go` — `ExecuteTeardown` + `TeardownCallbacks` pattern, confirmed
- `pkg/terragrunt/runner.go` — `Runner.Destroy()` pattern for subprocess execution, confirmed
- `internal/app/cmd/destroy.go` — minimal sandbox dir reconstruction pattern (lines 100-119), confirmed
- `.planning/v1.0-MILESTONE-AUDIT.md` — audit evidence for all four gaps, confirmed

### Secondary (MEDIUM confidence)
- `go.mod` — confirmed no `aws-sdk-go-v2/service/eventbridge` in direct deps; needs to be added or checked in go.sum

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries are already present in the codebase
- Architecture: HIGH — all patterns derived from direct code reading, not assumptions
- Pitfalls: HIGH — site.hcl reconstruction pitfall and PutEvents IAM pitfall are both derived from the existing code structure

**Research date:** 2026-03-22
**Valid until:** 2026-04-22 (stable Go + AWS SDK v2 ecosystem)
