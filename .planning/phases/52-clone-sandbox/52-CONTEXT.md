# Phase 52: km clone — Context

**Gathered:** 2026-04-12
**Status:** Ready for planning

<domain>
## Phase Boundary

Add `km clone` CLI command that duplicates a running sandbox. Creates a new sandbox from the source's stored profile, copies workspace and rsyncFileList paths, provisions a fully independent identity. Does NOT add snapshot/AMI cloning, shared budgets, or cross-region cloning.

</domain>

<decisions>
## Implementation Decisions

### What gets copied
- `/workspace` directory is copied via SSM + rsync through S3 staging
- Any paths defined in `spec.execution.rsyncFileList` are also copied
- `/home/sandbox` is NOT copied — regenerated fresh from the profile's userdata
- Environment variables are NOT copied — all KM_* env vars and profile-derived env are regenerated fresh
- System packages are NOT copied — come from profile's `initCommands`
- Copy mechanism: SSM runs tar/rsync on source → uploads to S3 staging → clone downloads on boot

### CLI syntax & flags
- Syntax: `km clone <source> [new-alias]` — second positional arg is alias
- Also accepts `--alias <name>` flag (same effect as positional)
- Source can be sandbox ID or alias (resolved via DynamoDB alias-index GSI, fallback to sandbox_id)
- `--no-copy` flag: skip workspace/rsync copy, create fresh sandbox from same profile
- `--count N` flag: clone N copies with auto-suffixed aliases (e.g., `km clone kph1 --count 3 --alias wrkr` creates wrkr-1, wrkr-2, wrkr-3)
- No profile overrides — clone uses exact same `.km-profile.yaml` from S3

### Source sandbox state
- Source must be running (error with suggestion to `km resume` if paused/stopped)
- Live copy — no freeze/pause of source during rsync
- Workspace may change during copy; acceptable for typical use cases

### Clone identity
- Fully independent: new sandbox ID, new Ed25519 keypair, new email address
- Fresh TTL from profile (not inherited remaining TTL)
- Fresh budget from profile (independent compute/AI spend tracking)
- New GitHub token, new safe phrase, new SES identity
- DynamoDB metadata includes `cloned_from` field pointing to source sandbox ID
- `cloned_from` visible in `km list --wide`

### Claude's Discretion
- S3 staging key structure for workspace copy artifacts
- rsync flags and exclusion patterns (e.g., skip .git/objects if large)
- Error handling for partial copy failures (retry vs fail)
- Progress output format during copy

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `compiler.GenerateSandboxID(prefix)`: generates new sandbox IDs — reuse for clone
- `compiler.Compile(profile, sandboxID, ...)`: compiles profile into Terragrunt artifacts — reuse entire create pipeline
- `awspkg.ReadSandboxMetadataDynamo()`: look up source sandbox metadata
- `awspkg.WriteSandboxMetadataDynamo()`: write clone metadata (needs `cloned_from` field added)
- `awspkg.ResolveSandboxAlias()`: resolve alias to sandbox ID via GSI
- S3 profile retrieval: `artifacts/{sandbox-id}/.km-profile.yaml` already stored by km create

### Established Patterns
- Cobra command pattern: `NewCloneCmd(cfg)` in `internal/app/cmd/clone.go`
- Alias resolution: DynamoDB alias-index GSI lookup, fallback to direct sandbox_id
- SSM command execution: used by `km shell`, `km agent` — pattern for running commands on source sandbox
- DynamoDB metadata: `sandboxItemDynamo` struct with `dynamodbav` tags

### Integration Points
- `internal/app/cmd/root.go`: register `NewCloneCmd`
- `pkg/aws/metadata.go`: add `ClonedFrom` field to `SandboxMetadata`
- `pkg/aws/sandbox_dynamo.go`: add `cloned_from` to `sandboxItemDynamo`
- `internal/app/cmd/list.go`: show `cloned_from` in `--wide` output
- S3 staging: new prefix like `artifacts/{clone-id}/staging/` for workspace copy

</code_context>

<specifics>
## Specific Ideas

- User's primary use case: `km clone kph1 --alias kph2` or `km clone kph1 kph2` — quick duplication of a working sandbox
- Multi-clone for fan-out: `km clone kph1 --count 3 --alias wrkr` creates wrkr-1, wrkr-2, wrkr-3
- `--no-copy` as a fast "create from same profile" shortcut — avoids needing to find the profile YAML file

</specifics>

<deferred>
## Deferred Ideas

- AMI snapshot-based cloning (full system state including packages) — separate phase if needed
- Cross-region cloning — would need S3 cross-region copy + multi-region support
- Clone from paused/stopped sandbox (auto-resume, copy, re-pause)
- Shared budget pools across cloned sandboxes
- `--freeze` flag to pause source during copy for consistency guarantee

</deferred>

---

*Phase: 52-clone-sandbox*
*Context gathered: 2026-04-12*
