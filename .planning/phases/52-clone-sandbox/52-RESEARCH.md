# Phase 52: km clone — Research

**Researched:** 2026-04-10
**Domain:** Go CLI (Cobra), AWS SSM, S3 staging, DynamoDB metadata, sandbox provisioning pipeline
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- `/workspace` directory is copied via SSM + rsync through S3 staging
- Any paths defined in `spec.execution.rsyncFileList` are also copied
- `/home/sandbox` is NOT copied — regenerated fresh from the profile's userdata
- Environment variables are NOT copied — all KM_* env vars and profile-derived env are regenerated fresh
- System packages are NOT copied — come from profile's `initCommands`
- Copy mechanism: SSM runs tar on source → uploads to S3 staging → clone downloads on boot
- Syntax: `km clone <source> [new-alias]` — second positional arg is alias
- Also accepts `--alias <name>` flag (same effect as positional)
- Source can be sandbox ID or alias (resolved via DynamoDB alias-index GSI, fallback to sandbox_id)
- `--no-copy` flag: skip workspace/rsync copy, create fresh sandbox from same profile
- `--count N` flag: clone N copies with auto-suffixed aliases
- No profile overrides — clone uses exact same `.km-profile.yaml` from S3
- Source must be running (error with suggestion to `km resume` if paused/stopped)
- Live copy — no freeze/pause of source during rsync
- Clone gets fully independent identity: new sandbox ID, Ed25519 keypair, email, TTL, budget, GitHub token, safe phrase, SES identity
- DynamoDB metadata includes `cloned_from` field pointing to source sandbox ID
- `cloned_from` visible in `km list --wide`

### Claude's Discretion
- S3 staging key structure for workspace copy artifacts
- rsync flags and exclusion patterns (e.g., skip .git/objects if large)
- Error handling for partial copy failures (retry vs fail)
- Progress output format during copy

### Deferred Ideas (OUT OF SCOPE)
- AMI snapshot-based cloning (full system state including packages)
- Cross-region cloning
- Clone from paused/stopped sandbox (auto-resume, copy, re-pause)
- Shared budget pools across cloned sandboxes
- `--freeze` flag to pause source during copy for consistency guarantee
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| CLONE-01 | `km clone <source> [new-alias]` CLI command — resolves source, provisions clone sandbox | Cobra command pattern established; `compiler.GenerateSandboxID` + `compiler.Compile` pipeline reusable |
| CLONE-02 | Workspace copy via SSM tar → S3 staging → clone boot download | `buildTarShellCmd` + `pollSSMCommand` patterns in rsync.go directly applicable; boot userdata bootstrap mechanism is the integration point |
| CLONE-03 | `--no-copy`, `--count N`, `--alias` flags with multi-clone alias auto-suffixing | Flag patterns from create.go; multi-clone requires serial or parallel `runCreate` loop |
| CLONE-04 | `cloned_from` field added to DynamoDB metadata; visible in `km list --wide` | `sandboxItemDynamo` struct, `SandboxMetadata` struct, `SandboxRecord` struct, `marshalSandboxItem` / `unmarshalSandboxItem`, `printSandboxTable` all need targeted additions |
| CLONE-05 | Source sandbox state validation (must be running; clear error if paused/stopped) | `ReadSandboxMetadataDynamo` already returns status field; error path well-defined |
</phase_requirements>

## Summary

Phase 52 adds `km clone` — a command that duplicates a running sandbox by: fetching the source's stored profile from S3, running the full `km create` provisioning pipeline to mint a new independent sandbox identity, and (unless `--no-copy`) staging the source's `/workspace` (and any `rsyncFileList` paths) through S3 so the clone downloads it at boot time.

The implementation is heavily reuse-based. The workspace copy mechanism is a direct adaptation of the existing `rsync.go` `buildTarShellCmd` + `pollSSMCommand` pattern, redirected to a clone-specific S3 staging key. The provisioning pipeline is the existing `runCreate` function called with the source's stored profile bytes — no new Terraform or profile machinery is required. The only genuinely new datamodel work is adding `ClonedFrom` to `SandboxMetadata`, `SandboxRecord`, `sandboxItemDynamo`, the marshal/unmarshal functions, and the `--wide` list display.

Multi-clone (`--count N`) runs the same single-clone path N times in a serial loop, generating auto-suffixed aliases (`wrkr-1`, `wrkr-2`, …). The S3 staging archive is uploaded once from the source and each clone's userdata download command references the same object; after all clones are provisioned the staging key is deleted.

**Primary recommendation:** Build `clone.go` by composing `ResolveSandboxID` → `ReadSandboxMetadataDynamo` (status check) → S3 profile fetch → `buildTarShellCmd` + SSM (workspace stage) → `runCreate`-equivalent loop (provision each clone) → cleanup staging object. Wire `ClonedFrom` through metadata structs and `--wide` list output.

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/spf13/cobra` | existing | CLI command + flag parsing | All km commands use this |
| `github.com/aws/aws-sdk-go-v2/service/ssm` | existing | SSM SendCommand to run tar on source | Used by shell, agent, rsync commands |
| `github.com/aws/aws-sdk-go-v2/service/s3` | existing | S3 GetObject/PutObject for staging artifact | Used throughout |
| `github.com/aws/aws-sdk-go-v2/service/dynamodb` | existing | Read source metadata + write clone metadata | Used by all commands |
| `github.com/goccy/go-yaml` | existing | Parse stored .km-profile.yaml bytes | Used by rsync.go profile fetch pattern |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `compiler.GenerateSandboxID` | internal | Generate new clone sandbox ID | Called once per clone |
| `compiler.Compile` | internal | Compile profile into Terragrunt artifacts | Called as part of existing `runCreate` flow |
| `awspkg.ReadSandboxMetadataDynamo` | internal | Fetch source metadata (status, profile name) | Pre-flight state check |
| `awspkg.WriteSandboxMetadataDynamo` | internal | Write clone metadata with `cloned_from` | After clone is provisioned |
| `awspkg.ResolveSandboxAliasDynamo` | internal | Resolve source alias → sandbox ID | Already called via `ResolveSandboxID` |

**Installation:** No new dependencies required — all libraries already in go.mod.

## Architecture Patterns

### Recommended Project Structure
```
internal/app/cmd/
├── clone.go           # NewCloneCmd, runClone, buildWorkspaceStagingCmd
├── clone_test.go      # unit tests for flag parsing, alias generation, staging key format
pkg/aws/
├── metadata.go        # add ClonedFrom field to SandboxMetadata
├── sandbox.go         # add ClonedFrom field to SandboxRecord
├── sandbox_dynamo.go  # add cloned_from to sandboxItemDynamo, marshal/unmarshal
internal/app/cmd/
├── list.go            # add cloned_from column to --wide output
├── root.go            # register NewCloneCmd
```

### Pattern 1: Command Structure (Cobra)
**What:** `NewCloneCmd(cfg)` creates the command; `runClone(...)` is the extracted RunE logic for testability.
**When to use:** All km commands follow this pattern (see `NewListCmdWithLister`, `NewAgentCmdWithDeps`).
**Example:**
```go
// Source: internal/app/cmd/agent.go pattern
func NewCloneCmd(cfg *config.Config) *cobra.Command {
    return NewCloneCmdWithDeps(cfg, nil, nil)
}

func NewCloneCmdWithDeps(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
    var alias string
    var count int
    var noCopy bool

    cmd := &cobra.Command{
        Use:          "clone <source> [new-alias]",
        Short:        "Duplicate a running sandbox with workspace copy",
        Args:         cobra.RangeArgs(1, 2),
        SilenceUsage: true,
        RunE: func(cmd *cobra.Command, args []string) error {
            if len(args) == 2 && alias == "" {
                alias = args[1]
            }
            return runClone(cmd, cfg, fetcher, ssmClient, args[0], alias, count, noCopy)
        },
    }
    cmd.Flags().StringVar(&alias, "alias", "", "Alias for the clone")
    cmd.Flags().IntVar(&count, "count", 1, "Number of clones to create")
    cmd.Flags().BoolVar(&noCopy, "no-copy", false, "Skip workspace copy, provision fresh from profile")
    return cmd
}
```

### Pattern 2: Workspace Staging via SSM + S3
**What:** Run tar on source via SSM to upload workspace to a staging S3 key; clone's userdata downloads it at boot.
**When to use:** The copy step whenever `--no-copy` is not set and source is EC2.
**Example:**
```go
// Source: internal/app/cmd/rsync.go — buildTarShellCmd pattern
// Staging key convention (discretionary — recommended):
// artifacts/{clone-id}/staging/workspace.tar.gz
func buildWorkspaceStagingCmd(paths []string, bucket, cloneID string) string {
    stagingKey := fmt.Sprintf("artifacts/%s/staging/workspace.tar.gz", cloneID)
    return buildTarShellCmd(paths, bucket, stagingKey)
}
```

The tar command already handles `/workspace` as an absolute path — the existing `buildTarShellCmd` cds to the sandbox user's `$SHELL_HOME` and archives relative paths. For `/workspace` (an absolute path outside `$HOME`), the staging command must `cd /` and tar `workspace/` directly, or use a separate tar invocation. This is a key deviation from the rsync pattern.

**Concrete approach (discretionary):**
```bash
# SSM command for workspace staging
cd / && tar czf /tmp/km-clone-workspace.tar.gz workspace/ && \
aws s3 cp /tmp/km-clone-workspace.tar.gz "s3://{bucket}/artifacts/{clone-id}/staging/workspace.tar.gz" && \
echo "CLONE_STAGE_OK: $(du -sh /tmp/km-clone-workspace.tar.gz | cut -f1)"
```

### Pattern 3: Clone Boot Download (Userdata Integration)
**What:** The clone's EC2 userdata must download the staging archive and extract it before the agent starts.
**When to use:** When workspace staging was performed (i.e., `--no-copy` not set, EC2 substrate).
**Mechanism:** The staging S3 key is passed to the compiler as an additional userdata parameter — or alternatively the clone's boot script checks for a well-known S3 key `artifacts/{sandbox-id}/staging/workspace.tar.gz` and downloads it if present. The "check at boot" approach avoids adding new compiler parameters.

**Recommended approach (discretionary):** Inject a `workspace_staging_key` variable into the Terragrunt inputs for the clone. The userdata script (already templated by the compiler) gains a conditional block:
```bash
if [ -n "${workspace_staging_key}" ]; then
  aws s3 cp "s3://${artifacts_bucket}/${workspace_staging_key}" /tmp/km-workspace.tar.gz
  tar xzf /tmp/km-workspace.tar.gz -C / && rm /tmp/km-workspace.tar.gz
fi
```

However, this requires compiler changes. The simpler alternative: always write the staging key to `artifacts/{sandbox-id}/staging/workspace.tar.gz` and have userdata unconditionally check for that well-known key. No new compiler variable needed — userdata already knows its own `sandbox_id` and `artifacts_bucket`.

### Pattern 4: Multi-Clone Loop
**What:** For `--count N`, iterate N times: generate alias with suffix, run single clone.
**When to use:** When `--count > 1`.
```go
for i := 1; i <= count; i++ {
    cloneAlias := fmt.Sprintf("%s-%d", baseAlias, i)
    if err := runSingleClone(ctx, cfg, sourceID, sourceProfile, stagingKey, cloneAlias); err != nil {
        return fmt.Errorf("clone %d/%d (%s): %w", i, count, cloneAlias, err)
    }
}
```

### Pattern 5: cloned_from Metadata Field
**What:** Add `ClonedFrom string` to `SandboxMetadata` and downstream structs.
**When to use:** Always set when a sandbox is created via `km clone`.

Four locations need changes (all in lockstep):
1. `pkg/aws/metadata.go` — `SandboxMetadata.ClonedFrom string json:"cloned_from,omitempty"`
2. `pkg/aws/sandbox.go` — `SandboxRecord.ClonedFrom string json:"cloned_from,omitempty"`
3. `pkg/aws/sandbox_dynamo.go` — `sandboxItemDynamo.ClonedFrom string dynamodbav:"cloned_from,omitempty"` + add to `unmarshalSandboxItem` + `marshalSandboxItem` + `toSandboxMetadata` + `metadataToRecord`
4. `internal/app/cmd/list.go` — add `CLONED FROM` column to `--wide` header and row format

### Anti-Patterns to Avoid
- **Calling `km create` as a subprocess:** The create pipeline is a Go function (`runCreate`). Call it directly, not via `exec.Command("km", "create", ...)` — subprocess loses AWS config context and adds process overhead.
- **Storing workspace copy inside rsync/ prefix:** Use a clone-specific staging prefix (`artifacts/{clone-id}/staging/`) to avoid name collisions with user-named rsync snapshots and to enable easy cleanup.
- **Hardcoding `/workspace` only:** The profile's `spec.execution.rsyncFileList` paths must also be staged — use the existing `resolveRsyncPaths` function.
- **Not cleaning up staging artifact:** The staging S3 object should be deleted after the clone successfully boots (or at clone time on `--no-copy`). A well-known key that persists forever wastes storage and could be confused with a valid rsync snapshot.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Sandbox ID generation | Custom UUID/random logic | `compiler.GenerateSandboxID(prefix)` | Ensures prefix pattern, hex suffix, uniqueness guarantee |
| Alias resolution | DynamoDB scan / S3 scan | `ResolveSandboxID(ctx, cfg, ref)` in `sandbox_ref.go` | Already handles GSI, pattern match, number reference — 3 lookup strategies |
| SSM command execution + polling | Custom poll loop | `pollSSMCommand(ctx, ssmClient, commandID, instanceID, marker, name)` in `rsync.go` | 30-iteration poll with 2s sleep already written and tested |
| Workspace tar construction | Custom shell string builder | `buildTarShellCmd(paths, bucket, s3Key)` adapted for /workspace | Validates paths, handles missing-path skip, outputs RSYNC_OK marker |
| Profile fetch from S3 | Raw S3 GetObject + parse | Pattern in `rsync.go:180`, `agent.go:1070`, `destroy.go:852` — copy verbatim | Consistent error handling, same S3 key format `artifacts/{id}/.km-profile.yaml` |
| DynamoDB marshal/unmarshal | `attributevalue.MarshalMap` | Manual `marshalSandboxItem` / `unmarshalSandboxItem` | Existing code manually builds AttributeValue map to guarantee Number type for TTL — adding `cloned_from` means extending the existing manual functions, not introducing auto-marshal |
| Status validation | Re-implementing sandbox state check | `ReadSandboxMetadataDynamo` returns `Status` field — compare to "running" | Already handles all status strings: "running", "stopped", "paused", "failed" |

**Key insight:** The entire clone command is composition of existing functions. The only net-new code is: (1) the `buildWorkspaceStagingCmd` variant that handles absolute `/workspace` path, (2) the `runClone` orchestration loop, (3) the `ClonedFrom` struct fields and DynamoDB serialization, and (4) the `--wide` list column.

## Common Pitfalls

### Pitfall 1: `/workspace` is not under `$HOME`
**What goes wrong:** `buildTarShellCmd` cds to `$SHELL_HOME` (i.e., `/home/sandbox`) before tarring. `/workspace` is an absolute path at the filesystem root, so `cd /home/sandbox && tar czf ... workspace` silently produces an empty archive.
**Why it happens:** rsync.go's `buildTarShellCmd` was designed for paths relative to `$HOME`. The clone command copies both home-relative rsync paths AND `/workspace`.
**How to avoid:** Use separate tar commands: one `cd / && tar czf ... workspace/` for `/workspace`, another for `$HOME`-relative paths. Or tar them all from `/` using absolute paths.
**Warning signs:** Staging archive is suspiciously small (< 1KB) yet source has files in `/workspace`.

### Pitfall 2: DynamoDB manual marshal/unmarshal requires extending existing functions
**What goes wrong:** Adding `ClonedFrom` to `sandboxItemDynamo` with `dynamodbav` tag does NOT automatically marshal/unmarshal — `sandbox_dynamo.go` uses entirely manual attribute map construction, not `attributevalue.MarshalMap`.
**Why it happens:** The struct tags exist as documentation, but `marshalSandboxItem` builds the `map[string]AttributeValue` manually, and `unmarshalSandboxItem` manually extracts each field. New fields must be added to all four locations: struct definition, marshal function, unmarshal function, `toSandboxMetadata` conversion.
**How to avoid:** The planner must create tasks for all four marshal/unmarshal change points explicitly. Forgetting any one means `cloned_from` either silently drops on write or never reads back.
**Warning signs:** `cloned_from` appears in Go struct but `km list --wide` shows blank — the field reads back as empty string because unmarshal was not updated.

### Pitfall 3: Alias collision in multi-clone
**What goes wrong:** `km clone src --count 3 --alias wrkr` attempts to create `wrkr-1`, `wrkr-2`, `wrkr-3` — but if `wrkr-1` already exists (leftover from a previous run), the DynamoDB GSI alias-index will have a conflict.
**Why it happens:** `km create` does not check for pre-existing aliases before provisioning. The alias is written to DynamoDB after provisioning; a duplicate will silently overwrite the GSI index entry.
**How to avoid:** Before the multi-clone loop, resolve each target alias via `ResolveSandboxAliasDynamo` and fail early if any alias is already taken.
**Warning signs:** `km list --wide` shows two sandboxes with the same alias, or alias resolution returns the wrong sandbox.

### Pitfall 4: Staging artifact cleanup on partial failure
**What goes wrong:** When `--count 3` and clone 2 fails, the staging object (`artifacts/{clone-id}/staging/workspace.tar.gz`) remains in S3 indefinitely.
**Why it happens:** The staging upload happens once before the provisioning loop; cleanup must happen after the loop (or on error).
**How to avoid:** Wrap the entire clone operation in a defer that deletes the staging object on any return path. Use a single staging key derived from the *source* sandbox ID, not per-clone: `artifacts/{source-id}/staging/clone-{timestamp}.tar.gz`. This way one upload serves all N clones and there is one cleanup target.
**Warning signs:** S3 bucket accumulates `staging/` prefixes over time, especially after interrupted clones.

### Pitfall 5: Source sandbox status check must use DynamoDB, not S3
**What goes wrong:** Checking S3 `metadata.json` for status may return stale state — S3 metadata is written once at create time; pause/stop/resume updates go to DynamoDB only (via `UpdateSandboxStatusDynamo`).
**Why it happens:** The DynamoDB switchover (Phase 11+) made DynamoDB the authoritative status store; S3 metadata.json is legacy/read path only.
**How to avoid:** Use `ReadSandboxMetadataDynamo` exclusively for the pre-flight status check.
**Warning signs:** `km clone` succeeds even though source is paused — because S3 metadata still says "running" from initial provisioning.

### Pitfall 6: ECS substrate workspace copy
**What goes wrong:** The workspace staging path uses SSM `SendCommand` targeting an EC2 instance ID. ECS Fargate tasks do not have EC2 instance IDs registered in SSM.
**Why it happens:** `km rsync save` also has this limitation — it calls `extractResourceID(rec.Resources, ":instance/")` which fails for ECS.
**How to avoid:** For ECS substrate, workspace copy is not supported via SSM. Detect substrate from metadata and either skip workspace copy (warn user) or return a clear error.
**Warning signs:** `extractResourceID` returns "find instance: not found" error for ECS sandboxes.

## Code Examples

Verified patterns from the actual codebase:

### Profile fetch from S3 (HIGH confidence — verbatim from rsync.go:180)
```go
// Source: internal/app/cmd/rsync.go:180
profileKey := fmt.Sprintf("artifacts/%s/.km-profile.yaml", sandboxID)
s3Client := s3.NewFromConfig(awsCfg)
profObj, profErr := s3Client.GetObject(ctx, &s3.GetObjectInput{
    Bucket: awssdk.String(cfg.ArtifactsBucket),
    Key:    awssdk.String(profileKey),
})
if profErr == nil {
    defer profObj.Body.Close()
    profData, readErr := io.ReadAll(profObj.Body)
    if readErr == nil {
        storedProfile, _ = profile.Parse(profData)
    }
}
```

### Source metadata fetch + status check (HIGH confidence)
```go
// Source: pkg/aws/sandbox_dynamo.go:ReadSandboxMetadataDynamo
tableName := cfg.SandboxTableName
if tableName == "" {
    tableName = "km-sandboxes"
}
meta, err := awspkg.ReadSandboxMetadataDynamo(ctx, dynamoClient, tableName, sourceID)
if err != nil {
    return fmt.Errorf("read source metadata: %w", err)
}
if meta.Status != "running" {
    return fmt.Errorf("source sandbox %s is not running (status: %s) — run 'km resume %s' first", sourceID, meta.Status, sourceID)
}
```

### SSM workspace staging command (HIGH confidence — adapted from rsync.go:131)
```go
// For /workspace (absolute path) — cd to / and tar workspace directory
func buildWorkspaceStagingCmd(bucket, stagingKey string) string {
    return fmt.Sprintf(
        `cd / && `+
        `if [ -d workspace ]; then `+
        `tar czf /tmp/km-clone-workspace.tar.gz workspace/ && `+
        `aws s3 cp /tmp/km-clone-workspace.tar.gz "s3://%s/%s" && `+
        `echo "CLONE_STAGE_OK: $(du -sh /tmp/km-clone-workspace.tar.gz | cut -f1)"; `+
        `else echo "CLONE_STAGE_EMPTY: no /workspace directory"; fi`,
        bucket, stagingKey,
    )
}
```

### cloned_from in sandboxItemDynamo marshal (HIGH confidence — extend existing pattern)
```go
// Source: pkg/aws/sandbox_dynamo.go:marshalSandboxItem — add to existing function
if meta.ClonedFrom != "" {
    item["cloned_from"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.ClonedFrom}
}
```

### cloned_from in unmarshalSandboxItem (HIGH confidence — extend existing pattern)
```go
// Source: pkg/aws/sandbox_dynamo.go:unmarshalSandboxItem — add to existing function
if v, ok := item["cloned_from"]; ok {
    if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
        d.ClonedFrom = sv.Value
    }
}
```

### --wide list column addition (HIGH confidence — extend printSandboxTable)
```go
// Source: internal/app/cmd/list.go:printSandboxTable
// Header (wide mode) — add CLONED FROM at end:
fmt.Fprintf(out, "%-3s %-8s  %-*s %-16s %-10s %-12s %-10s %-6s %-6s %s\n",
    "#", "ALIAS", idWidth, "SANDBOX ID", "PROFILE", "SUBSTRATE", "REGION", "STATUS", "TTL", "IDLE", "CLONED FROM")
// Row (wide mode):
clonedFrom := truncCol(r.ClonedFrom, 12)
if clonedFrom == "" { clonedFrom = "-" }
```

### Multi-clone alias auto-suffix (HIGH confidence — pure Go)
```go
for i := 1; i <= count; i++ {
    cloneAlias := fmt.Sprintf("%s-%d", baseAlias, i)
    if err := runSingleClone(ctx, cfg, sourceID, profileBytes, stagingKey, cloneAlias); err != nil {
        return fmt.Errorf("clone %d/%d (%s): %w", i, count, cloneAlias, err)
    }
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| S3 metadata.json as authoritative status | DynamoDB `km-sandboxes` table | Phase 11 | Status check for "running" MUST use DynamoDB, not S3 |
| Direct rsync/scp for file transfer | SSM SendCommand → S3 staging | Phase (rsync feature) | No direct network path to sandbox needed; works within VPC restrictions |
| Manual attributevalue.MarshalMap | Manual map construction in sandbox_dynamo.go | Phase 11 | Any new DynamoDB field requires adding to all 4 marshal/unmarshal locations manually |

**Deprecated/outdated:**
- `pkg/aws/sandbox.go` S3-based `readMetadataRecord`: still used as fallback but DynamoDB is authoritative for status — do not use S3 for the pre-flight running check in clone.

## Open Questions

1. **ECS workspace copy support**
   - What we know: SSM `SendCommand` requires EC2 instance IDs; ECS Fargate has no SSM instance registration
   - What's unclear: Whether any ECS exec mechanism is exposed in the existing codebase that could substitute
   - Recommendation: Return a clear error for ECS substrate clones with `--no-copy` as the workaround, and note it in the command's help text

2. **Boot-time workspace download injection**
   - What we know: The compiler generates EC2 userdata from profile + Terragrunt inputs; existing userdata downloads secrets and profile env vars
   - What's unclear: Whether it's simpler to (a) inject a `workspace_staging_key` Terragrunt input variable via a thin compiler addition, or (b) have userdata check for a well-known S3 key using the clone's own sandbox ID at boot
   - Recommendation: Option (b) — check well-known key `artifacts/{sandbox-id}/staging/workspace.tar.gz` at boot. Avoids compiler changes, and the key's presence/absence implicitly signals whether a workspace copy was staged. Planner can confirm.

3. **rsyncFileList staging for /workspace copy**
   - What we know: `resolveRsyncPaths` returns home-relative paths; `/workspace` is absolute
   - What's unclear: Whether the staging command should merge `/workspace` + rsyncFileList into one tar, or two separate archives with separate SSM commands
   - Recommendation: One SSM command, two tar operations piped into one archive. Or: separate archives, sequential SSM commands. Simpler to implement as two sequential SSM invocations with clear `CLONE_STAGE_OK` markers each.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing + testify (existing) |
| Config file | none — standard `go test ./...` |
| Quick run command | `go test ./internal/app/cmd/ -run TestClone -v` |
| Full suite command | `go test ./internal/app/cmd/ ./pkg/aws/ -count=1` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| CLONE-01 | `km clone src alias` resolves source, invokes clone pipeline | unit | `go test ./internal/app/cmd/ -run TestCloneCmd -v` | ❌ Wave 0 |
| CLONE-02 | Workspace staging command builds correct S3 key and tar path | unit | `go test ./internal/app/cmd/ -run TestBuildWorkspaceStagingCmd -v` | ❌ Wave 0 |
| CLONE-03 | `--count N` generates correct auto-suffixed aliases; `--no-copy` skips staging | unit | `go test ./internal/app/cmd/ -run TestCloneFlags -v` | ❌ Wave 0 |
| CLONE-04 | `cloned_from` field marshals/unmarshals through DynamoDB item map correctly | unit | `go test ./pkg/aws/ -run TestClonedFromMarshal -v` | ❌ Wave 0 |
| CLONE-05 | Non-running source returns error with suggestion | unit | `go test ./internal/app/cmd/ -run TestCloneSourceNotRunning -v` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/cmd/ -run TestClone -v`
- **Per wave merge:** `go test ./internal/app/cmd/ ./pkg/aws/ -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `internal/app/cmd/clone_test.go` — covers CLONE-01, CLONE-02, CLONE-03, CLONE-05
- [ ] `pkg/aws/sandbox_dynamo_clone_test.go` OR extend `sandbox_dynamo_test.go` — covers CLONE-04

## Sources

### Primary (HIGH confidence)
- `internal/app/cmd/rsync.go` — `buildTarShellCmd`, `pollSSMCommand`, `resolveRsyncPaths`, S3 profile fetch pattern
- `internal/app/cmd/create.go` — `runCreate` flow, flag patterns, profile fetch from S3
- `pkg/aws/sandbox_dynamo.go` — `sandboxItemDynamo`, `marshalSandboxItem`, `unmarshalSandboxItem`, `ReadSandboxMetadataDynamo`, `WriteSandboxMetadataDynamo`, `ResolveSandboxAliasDynamo`
- `pkg/aws/metadata.go` — `SandboxMetadata` struct
- `pkg/aws/sandbox.go` — `SandboxRecord` struct
- `internal/app/cmd/sandbox_ref.go` — `ResolveSandboxID` pattern
- `internal/app/cmd/list.go` — `printSandboxTable`, `--wide` column layout
- `internal/app/cmd/root.go` — command registration pattern
- `.planning/phases/52-clone-sandbox/52-CONTEXT.md` — locked decisions

### Secondary (MEDIUM confidence)
- `internal/app/cmd/agent.go` — `SSMSendAPI` interface, `NewAgentCmdWithDeps` DI pattern
- `internal/app/cmd/status.go` — `SandboxFetcher` interface, `BudgetFetcher` interface

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries already in codebase; no new dependencies
- Architecture: HIGH — all patterns directly derived from existing working code
- Pitfalls: HIGH — identified from direct code inspection of the four marshal/unmarshal locations, rsync.go path handling, and DynamoDB vs S3 status authority
- Open questions: MEDIUM — boot-time injection approach is discretionary per CONTEXT.md

**Research date:** 2026-04-10
**Valid until:** 2026-05-10 (stable Go + AWS SDK codebase; no fast-moving dependencies)
