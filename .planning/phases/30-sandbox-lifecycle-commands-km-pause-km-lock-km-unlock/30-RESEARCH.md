# Phase 30: sandbox-lifecycle-commands-km-pause-km-lock-km-unlock - Research

**Researched:** 2026-03-28
**Domain:** Go CLI commands, EC2 lifecycle, S3 metadata mutation
**Confidence:** HIGH

## Summary

Phase 30 adds three new commands: `km pause`, `km lock`, and `km unlock`. All three follow the established pattern seen in `km stop` and `km extend`: resolve sandbox ID via `ResolveSandboxID`, load AWS config, operate on AWS resources, and update `SandboxMetadata` in S3.

`km pause` calls `StopInstances` with `Hibernate: true`. The EC2 SDK v1.296.0 (already in go.mod) includes the `Hibernate *bool` field on `StopInstancesInput`. **Critical constraint:** hibernation must have been enabled at instance launch via `HibernationOptions.Configured = true` in the Terraform module. This is not currently set in the `ec2spot` module. If `Hibernate: true` is passed to an instance not configured for hibernation, AWS performs a normal stop instead — so `km pause` can safely fall back: it attempts hibernate, and if the instance state after the API call matches "stopped" (not "hibernated"), it notes the fallback. This makes `km pause` behaviorally equivalent to `km stop` in practice for current instances, but semantically distinct (intent is preserve-RAM state).

`km lock` and `km unlock` operate purely on the S3 metadata.json. A `Locked bool` field is added to `SandboxMetadata`. Lock enforcement is checked at the start of `runDestroy`, `runStop`, and `runPause` (and optionally budget changes). Unlock requires confirmation, mirroring the destroy confirmation pattern.

**Primary recommendation:** Implement pause as `StopInstances{Hibernate: true}` with status "paused" in metadata. Implement lock/unlock as read-modify-write of `metadata.json` with a `Locked` field, enforced in the existing command entry points.

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/ec2` | v1.296.0 | `StopInstances` with `Hibernate` flag | Already in go.mod; `Hibernate *bool` confirmed present |
| `github.com/aws/aws-sdk-go-v2/service/s3` | v1.97.1 | Read-modify-write of metadata.json | Same pattern as `km extend` |
| `github.com/spf13/cobra` | v1.8.1 | Command definition | Project-wide CLI framework |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/aws/aws-sdk-go-v2/service/eventbridge` | v1.45.22 | `--remote` dispatch | `km pause --remote` (consistent with stop/destroy) |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| S3 metadata for lock state | DynamoDB km-identities | DynamoDB is overkill; S3 metadata.json is already the source of truth for sandbox state; no additional infra needed |
| Hibernate flag | Custom "pause" logic | EC2 SDK already supports Hibernate; no hand-rolling needed |

**Installation:** No new dependencies. All libraries are already in `go.mod`.

## Architecture Patterns

### Recommended Project Structure

New files follow exact project conventions:

```
internal/app/cmd/
├── pause.go               # km pause command
├── pause_test.go          # unit tests
├── lock.go                # km lock command
├── lock_test.go
├── unlock.go              # km unlock command
├── unlock_test.go
└── help/
    ├── pause.txt          # embedded help text (required by helpText())
    ├── lock.txt
    └── unlock.txt

pkg/aws/
└── sandbox.go             # add WriteSandboxMetadata() helper + Locked field to SandboxMetadata
pkg/aws/metadata.go        # add Locked bool field to SandboxMetadata struct
```

### Pattern 1: Command Skeleton (matches km stop exactly)

**What:** Every lifecycle command follows the `NewXxxCmd` / `NewXxxCmdWithPublisher` / `runXxx` split.
**When to use:** All three new commands.

```go
// Source: internal/app/cmd/stop.go (project pattern)
func NewPauseCmd(cfg *config.Config) *cobra.Command {
    return NewPauseCmdWithPublisher(cfg, nil)
}

func NewPauseCmdWithPublisher(cfg *config.Config, pub RemoteCommandPublisher) *cobra.Command {
    var remote bool
    cmd := &cobra.Command{
        Use:          "pause <sandbox-id | #number>",
        Short:        "Pause a sandbox's EC2 instance (hibernate, preserving RAM state)",
        Long:         helpText("pause"),
        Args:         cobra.ExactArgs(1),
        SilenceUsage: true,
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := cmd.Context()
            if ctx == nil { ctx = context.Background() }
            sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
            if err != nil { return err }
            if remote {
                publisher := pub
                if publisher == nil { publisher = newRealRemotePublisher(cfg) }
                return publisher.PublishSandboxCommand(ctx, sandboxID, "pause")
            }
            return runPause(ctx, cfg, sandboxID)
        },
    }
    cmd.Flags().BoolVar(&remote, "remote", false, "Dispatch pause to Lambda via EventBridge")
    return cmd
}
```

### Pattern 2: Metadata Read-Modify-Write (matches km extend exactly)

**What:** Read existing metadata from S3, mutate fields, write back with `PutObject`.
**When to use:** `km pause` (status update), `km lock`, `km unlock`.

```go
// Source: internal/app/cmd/extend.go (project pattern)
meta, err := awspkg.ReadSandboxMetadata(ctx, s3Client, cfg.StateBucket, sandboxID)
if err != nil { return fmt.Errorf("read sandbox metadata: %w", err) }

meta.Status = "paused"  // or meta.Locked = true/false
metaJSON, _ := json.Marshal(meta)
_, putErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
    Bucket:      aws.String(cfg.StateBucket),
    Key:         aws.String("tf-km/sandboxes/" + sandboxID + "/metadata.json"),
    Body:        bytes.NewReader(metaJSON),
    ContentType: aws.String("application/json"),
})
```

### Pattern 3: Lock Enforcement Guard

**What:** At the start of `runDestroy`, `runStop`, `runPause`, read metadata and fail if `Locked == true`.
**When to use:** Any destructive/modifying command that lock is meant to block.

```go
// To add at the top of runDestroy / runStop / runPause
if cfg.StateBucket != "" {
    awsCfg, _ := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
    s3Client := s3.NewFromConfig(awsCfg)
    if meta, err := awspkg.ReadSandboxMetadata(ctx, s3Client, cfg.StateBucket, sandboxID); err == nil {
        if meta.Locked {
            return fmt.Errorf("sandbox %s is locked — run 'km unlock %s' first", sandboxID, sandboxID)
        }
    }
}
```

### Pattern 4: EC2 Hibernate API Call

**What:** `StopInstances` with `Hibernate: aws.Bool(true)`. Falls back silently to stop if not hibernate-enabled.
**When to use:** `km pause` only.

```go
// Source: aws-sdk-go-v2/service/ec2@v1.296.0/api_op_StopInstances.go
_, err := ec2Client.StopInstances(ctx, &ec2.StopInstancesInput{
    InstanceIds: []string{instanceID},
    Hibernate:   aws.Bool(true),
})
```

### Pattern 5: Unlock Confirmation (matches km destroy)

**What:** Require `[y/N]` confirmation before unlock, unless `--yes` is passed.
**When to use:** `km unlock` only.

```go
// Source: internal/app/cmd/destroy.go (project pattern)
if !yes {
    fmt.Printf("Unlock sandbox %s? This will allow destroy/stop/budget changes. [y/N] ", sandboxID)
    var answer string
    fmt.Scanln(&answer)
    if answer != "y" && answer != "Y" && answer != "yes" {
        fmt.Println("Aborted.")
        return nil
    }
}
```

### Anti-Patterns to Avoid

- **Adding lock state anywhere other than metadata.json:** S3 metadata.json is the sole source of truth for sandbox state. DynamoDB or SSM would create dual state concerns.
- **Using `Locked` as a hard AWS-level block:** The lock is a CLI-level guard, not an IAM deny. It prevents operator accidents, not external API calls.
- **Omitting help text file:** `helpText("pause")` panics at startup if `help/pause.txt` is missing — the embed is compiled in.
- **Forgetting to register commands in root.go:** All three commands need `root.AddCommand(NewXxxCmd(cfg))` in `NewRootCmd`.
- **Checking lock after expensive operations:** Lock check must be the first thing in `runDestroy`/`runStop`/`runPause`, before AWS API calls or terragrunt.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Hibernate API call | Custom shutdown/reboot sequence | `ec2.StopInstancesInput{Hibernate: aws.Bool(true)}` | SDK already has it; AWS handles the RAM snapshot |
| Lock persistence | Custom DynamoDB table or SSM parameter | `metadata.json` `Locked bool` field | Metadata.json is already read by `km list`/`km status`; no new infra |
| Sandbox ID resolution | Parsing args directly | `ResolveSandboxID(ctx, cfg, args[0])` | Handles IDs, aliases, and `#number` references |

**Key insight:** The three new commands are thin orchestrators. Pause delegates to EC2, lock/unlock delegate to S3. No new infrastructure, no new AWS resources, no new DynamoDB tables.

## Common Pitfalls

### Pitfall 1: Hibernate Requires Instance to Have Been Launched with Hibernation Enabled
**What goes wrong:** `StopInstances{Hibernate: true}` on an instance NOT configured for hibernation results in a normal stop (AWS silently downgrades), not an error. The metadata will show "paused" but the instance RAM was not preserved.
**Why it happens:** EC2 hibernation must be opted into at launch time via `HibernationOptions.Configured = true`. The current `ec2spot` Terraform module does NOT enable this.
**How to avoid:** Phase 30 scope decision: `km pause` calls with `Hibernate: true` as intent, accepts the fallback. The status written to metadata should be `"paused"` (not `"hibernated"`) to avoid confusion. Document in help text that RAM preservation requires hibernation-enabled instances.
**Warning signs:** If you want true RAM preservation, a future phase must add `hibernation = true` to the ec2spot Terraform module AND the instance type must support it.

### Pitfall 2: Lock Guard Must Be Added to All Blocked Commands
**What goes wrong:** Adding `Locked` to metadata but forgetting to check it in `runDestroy` means the lock doesn't actually protect against destroy.
**Why it happens:** Lock enforcement is scattered across multiple `runXxx` functions. Missing one means partial protection.
**How to avoid:** Explicitly add guard to: `runDestroy` (Step 1, before AWS calls), `runStop`, `runPause`, and optionally `newBudgetAddCmd` (for budget changes).
**Warning signs:** Test `km destroy sb-xxx` after `km lock sb-xxx` — should fail with "is locked" error.

### Pitfall 3: Paused Sandbox Can Still Be Auto-Destroyed by TTL Lambda
**What goes wrong:** Operator pauses a sandbox expecting it to remain paused, but the TTL schedule fires and destroys it.
**Why it happens:** `km pause` does not extend or cancel the TTL schedule. The TTL Lambda doesn't know about the paused state.
**How to avoid:** For Phase 30, document this clearly: `km pause` does NOT affect TTL. If TTL is a concern, the operator should also run `km extend`. A future phase could add TTL suspension to the pause flow.
**Warning signs:** Sandbox disappears after pause when TTL was near expiry.

### Pitfall 4: Missing help/*.txt Causes Panic
**What goes wrong:** Build succeeds but `km pause` panics at command registration time with "missing embedded help file: pause.txt".
**Why it happens:** `helpText("pause")` uses `embed.FS` — the panic happens at startup, not at command invocation.
**How to avoid:** Create `internal/app/cmd/help/pause.txt`, `lock.txt`, and `unlock.txt` before wiring commands into root.go.
**Warning signs:** Any invocation of `km` (not just `km pause`) panics.

### Pitfall 5: S3 StateBucket Not Configured
**What goes wrong:** `km lock`/`km unlock` silently skip the lock write because `cfg.StateBucket == ""`.
**Why it happens:** `km extend` pattern: skip silently if no state bucket. For lock/unlock, silent skip is wrong — the operator expects a hard failure.
**How to avoid:** Lock and unlock must return an error (not skip) when `cfg.StateBucket == ""`, because the operation is meaningless without persistence.

## Code Examples

Verified patterns from official sources:

### EC2 Hibernate Stop (SDK v1.296.0)
```go
// Source: /Users/khundeck/go/pkg/mod/github.com/aws/aws-sdk-go-v2/service/ec2@v1.296.0/api_op_StopInstances.go
_, err := ec2Client.StopInstances(ctx, &ec2.StopInstancesInput{
    InstanceIds: []string{instanceID},
    Hibernate:   aws.Bool(true),
    // If instance is not hibernate-enabled, AWS performs normal stop
})
```

### Metadata Read-Modify-Write for Lock (project pattern from extend.go)
```go
// Source: internal/app/cmd/extend.go lines 82-141
meta, err := awspkg.ReadSandboxMetadata(ctx, s3Client, cfg.StateBucket, sandboxID)
if err != nil {
    return fmt.Errorf("read sandbox metadata: %w", err)
}
meta.Locked = true
metaJSON, _ := json.Marshal(meta)
_, putErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
    Bucket:      aws.String(cfg.StateBucket),
    Key:         aws.String("tf-km/sandboxes/" + sandboxID + "/metadata.json"),
    Body:        bytes.NewReader(metaJSON),
    ContentType: aws.String("application/json"),
})
```

### SandboxMetadata Extension (metadata.go)
```go
// Source: pkg/aws/metadata.go (current)
// Add Locked and LockedAt fields:
type SandboxMetadata struct {
    SandboxID   string     `json:"sandbox_id"`
    // ... existing fields ...
    Status      string     `json:"status,omitempty"`
    Locked      bool       `json:"locked,omitempty"`       // NEW: lock/unlock guard
    LockedAt    *time.Time `json:"locked_at,omitempty"`    // NEW: audit trail
}
```

### Test Pattern (fakePublisher from stop_test.go)
```go
// Source: internal/app/cmd/stop_test.go lines 14-30
// All three new commands reuse the same fakePublisher already defined in stop_test.go.
// For lock/unlock tests, use a fake S3 client interface (same approach as extend_test.go).
```

### Command Registration (root.go)
```go
// Source: internal/app/cmd/root.go
root.AddCommand(NewPauseCmd(cfg))
root.AddCommand(NewLockCmd(cfg))
root.AddCommand(NewUnlockCmd(cfg))
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| N/A — new commands | `km pause` as `StopInstances{Hibernate: true}` | Phase 30 | No RAM preservation today unless ec2spot module updated |
| Implicit lock (none) | Explicit `Locked` field in S3 metadata.json | Phase 30 | Operator safety gate against accidental destroy |

**Deprecated/outdated:**
- None — this is all new functionality.

## Open Questions

1. **Should `km lock` block budget changes?**
   - What we know: Lock intent is "prevent accidental destroy/stop/budget changes."
   - What's unclear: Whether blocking budget top-up is desired (operator may want to add budget even when locked).
   - Recommendation: Block `km destroy`, `km stop`, `km pause` for certain. For `km budget add`, make it a `--force` override. Planner should decide and document.

2. **Should `km pause` also check for and cancel the TTL schedule?**
   - What we know: `km stop` does NOT cancel TTL. `km destroy` cancels TTL.
   - What's unclear: Whether pausing should extend or freeze the TTL.
   - Recommendation: Phase 30 scope: `km pause` does NOT touch TTL (consistent with `km stop`). Document this limitation.

3. **Should `km lock` persist across a `km stop` / restart cycle?**
   - What we know: `km stop` does not touch metadata.json.
   - What's unclear: Whether lock should survive a stop+start or be cleared on resume.
   - Recommendation: Lock persists in metadata.json until explicitly unlocked. This is natural given the S3-based design.

4. **Should the lock guard be enforced by the TTL Lambda / budget enforcer?**
   - What we know: TTL Lambda (`cmd/ttl-handler/main.go`) and budget enforcer call `StopInstances` directly — they bypass CLI guards.
   - What's unclear: Whether locking should also prevent Lambda-triggered stops.
   - Recommendation: Phase 30 scope: lock is a CLI-only guard. Lambda-level lock enforcement requires reading metadata in Lambda, which is a larger change. Document as future work.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) |
| Config file | none — `go test ./...` convention |
| Quick run command | `go test ./internal/app/cmd/... -run TestPause -v` |
| Full suite command | `go test ./internal/app/cmd/... ./pkg/aws/...` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| PAUSE-01 | `km pause --remote` dispatches EventBridge "pause" event | unit | `go test ./internal/app/cmd/... -run TestPauseCmd_RemotePublishesCorrectEvent -v` | Wave 0 |
| PAUSE-02 | `km pause` calls StopInstances with Hibernate=true | unit | `go test ./internal/app/cmd/... -run TestPause_HibernateFlag -v` | Wave 0 |
| PAUSE-03 | `km pause` updates status to "paused" in metadata | unit | `go test ./internal/app/cmd/... -run TestPause_UpdatesMetadataStatus -v` | Wave 0 |
| LOCK-01 | `km lock` sets Locked=true in metadata.json | unit | `go test ./internal/app/cmd/... -run TestLockCmd_SetsLockedField -v` | Wave 0 |
| LOCK-02 | `km destroy` on locked sandbox returns error | unit | `go test ./internal/app/cmd/... -run TestDestroy_BlockedByLock -v` | Wave 0 |
| LOCK-03 | `km stop` on locked sandbox returns error | unit | `go test ./internal/app/cmd/... -run TestStop_BlockedByLock -v` | Wave 0 |
| UNLOCK-01 | `km unlock` sets Locked=false in metadata.json | unit | `go test ./internal/app/cmd/... -run TestUnlockCmd_ClearsLockedField -v` | Wave 0 |
| UNLOCK-02 | `km unlock` requires confirmation without --yes | unit | `go test ./internal/app/cmd/... -run TestUnlock_RequiresConfirmation -v` | Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/cmd/... -run 'TestPause|TestLock|TestUnlock' -v`
- **Per wave merge:** `go test ./internal/app/cmd/... ./pkg/aws/...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `internal/app/cmd/pause_test.go` — covers PAUSE-01, PAUSE-02, PAUSE-03
- [ ] `internal/app/cmd/lock_test.go` — covers LOCK-01, LOCK-02, LOCK-03
- [ ] `internal/app/cmd/unlock_test.go` — covers UNLOCK-01, UNLOCK-02
- [ ] `internal/app/cmd/help/pause.txt` — required by `helpText("pause")` embed
- [ ] `internal/app/cmd/help/lock.txt` — required by `helpText("lock")` embed
- [ ] `internal/app/cmd/help/unlock.txt` — required by `helpText("unlock")` embed

## Sources

### Primary (HIGH confidence)
- `/Users/khundeck/go/pkg/mod/github.com/aws/aws-sdk-go-v2/service/ec2@v1.296.0/api_op_StopInstances.go` — `Hibernate *bool` field confirmed in `StopInstancesInput`
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/stop.go` — command skeleton pattern
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/extend.go` — metadata read-modify-write pattern
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/destroy.go` — confirmation pattern, lock guard insertion points
- `/Users/khundeck/working/klankrmkr/pkg/aws/metadata.go` — `SandboxMetadata` struct (no Locked field today)
- `/Users/khundeck/working/klankrmkr/pkg/aws/sandbox.go` — `ReadSandboxMetadata`, `DeleteSandboxMetadata` functions
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/root.go` — command registration location
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/help.go` — embedded help file requirement
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/remote_publisher.go` — `RemoteCommandPublisher` interface

### Secondary (MEDIUM confidence)
- EC2 hibernation prerequisites (from SDK docstrings): instance must have been launched with `HibernationOptions.Configured = true`; spot instances support hibernation only when `InstanceInterruptionBehavior = hibernate`
- `/Users/khundeck/go/pkg/mod/github.com/aws/aws-sdk-go-v2/service/ec2@v1.296.0/types/enums.go` — `InstanceStateName` enum confirms "stopped" but no separate "hibernated" state name

### Tertiary (LOW confidence)
- None.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries confirmed present in go.mod; SDK API confirmed in module cache
- Architecture: HIGH — all patterns directly sourced from existing project commands
- Pitfalls: HIGH — derived from first-principles analysis of the existing codebase plus EC2 SDK behavior

**Research date:** 2026-03-28
**Valid until:** 2026-06-28 (stable domain: EC2 SDK, Go stdlib, project conventions)
