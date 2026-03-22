# Phase 4: Lifecycle Hardening, Artifacts & Email - Context

**Gathered:** 2026-03-22
**Status:** Ready for planning

<domain>
## Phase Boundary

Make sandboxes production-safe for real agent workloads: enforce filesystem access restrictions at the OS level, preserve artifacts on exit and on spot interruption, scrub secrets from audit logs, and provide a full email layer (per-sandbox address, send/receive, operator lifecycle notifications, cross-sandbox orchestration). No ConfigUI — that's Phase 5.

</domain>

<decisions>
## Implementation Decisions

### Filesystem Enforcement
- EC2: `mount --bind -o ro` in user-data.sh at boot — kernel-native, no extra binaries, uses existing root access in bootstrap script
- ECS: `readonlyRootFilesystem: true` on the main container in the ECS task definition; `writablePaths` from the profile are mounted as tmpfs volumes (or EFS if persistence needed). Enforced by the container runtime.
- Write attempts to read-only paths return EROFS — OS-level error, not application-level. No special interception needed; error surfaces in agent stderr which the audit-log sidecar already captures.
- No dedicated `filesystem_violation` audit event type — EROFS in stderr is sufficient. Keeps the audit-log sidecar simple.

### Artifact Upload
- Profile schema gets a new `spec.artifacts` section: `paths` (list of glob/dir strings to upload), `maxSizeMB` (per-file size limit), `replicationRegion` (optional secondary AWS region for S3 replication)
- Upload trigger: exit-only — called by the teardown flow (`pkg/lifecycle/teardown.go`) before Terraform destroy. Same trigger for TTL expiry, idle timeout, and `km destroy`.
- Oversized files: skip the file, emit a `skipped_artifact` warning event in the audit log with filename and size, continue uploading remaining files. No hard failure.
- S3 path for artifacts: `s3://km-sandbox-artifacts-ea554771/artifacts/{sandbox-id}/`
- Multi-region replication: S3 bucket replication rule added by the Terraform module when `replicationRegion` is set in the profile. No SDK-level copy logic.

### Spot Interruption Handling
- Handler lives in user-data.sh as a background poll loop: check `http://169.254.169.254/latest/meta-data/spot/termination-time` every 5 seconds
- On detection: run the same artifact upload script used at normal exit, then allow the instance to terminate naturally (no forced kill)
- Reuses existing upload logic — no fifth sidecar binary
- ECS Fargate spot: handled via ECS task state change event (EventBridge rule watching for `TASK_STOPPING` with `stopCode: SpotInterruption`) → triggers artifact upload Lambda before task is reclaimed

### Secret Redaction
- Redaction happens at audit-log capture time, before any write to CloudWatch or S3 — nothing sensitive leaves the sandbox in log form
- Secret identification: two layers
  1. SSM parameter values from the profile (fetched at sidecar startup via the sandbox IAM role)
  2. Regex pattern library: AWS access key prefix (`AKIA[A-Z0-9]{16}`), JWT Bearer tokens (`Bearer [A-Za-z0-9\-._~+/]+=*`), hex strings ≥40 chars (`[0-9a-f]{40,}`)
- Replacement: `[REDACTED]` — literal string, grep-friendly, obvious in logs
- Redaction applies to the `detail` field of AuditEvent — not to structural fields like sandbox_id, event_type, timestamp

### Email Architecture
- Per-sandbox address format: `{sandbox-id}@sandboxes.klankermaker.ai` (e.g. `sb-a1b2c3d4@sandboxes.klankermaker.ai`)
  - Uses `sandboxes.klankermaker.ai` subdomain — separate MX record from any future human mail on root domain
  - Sandbox ID is directly parseable from the address
- Sending from sandboxes: SES API via IAM role — `ses:SendEmail` permission scoped to the sandbox's own address as the `From` address. Agent uses AWS SDK or SES SMTP endpoint (`email-smtp.<region>.amazonaws.com:587`). No relay sidecar.
- Inbound email for cross-sandbox orchestration: SES receipt rule stores to S3 at `km-sandbox-artifacts-ea554771/mail/{sandbox-id}/inbox/`. Agent polls S3 for new objects using `ListObjectsV2` with a prefix. Simple, no Lambda, no SQS per sandbox.
- Operator lifecycle notifications: SES sends email on all four events:
  - TTL expiry / sandbox destroyed
  - Idle timeout triggered
  - Spot interruption detected
  - Error / abnormal exit (non-zero exit status or crash)
- Operator email address: configurable via `KM_OPERATOR_EMAIL` env var (set at `km create` time or in global config)

### Claude's Discretion
- Exact regex patterns in the redaction library (beyond the three discussed)
- Artifact upload Go implementation (S3 multipart vs simple PutObject, concurrency)
- SES domain verification and DKIM setup details (Route53 DNS records)
- ECS Fargate spot interruption Lambda implementation details
- How `spec.artifacts` section integrates with profile inheritance (child overrides parent, same semantics as other sections)
- Operator notification email template/formatting

</decisions>

<specifics>
## Specific Ideas

- Artifact bucket already exists: `km-sandbox-artifacts-ea554771` (provisioned in Phase 2)
- Domain already exists: `klankermaker.ai` (Route53 hosted zone provisioned in Phase 1)
- `pkg/aws/spot.go` already has `spotTerminationTimeout` and `GetSpotInstanceID` — extend rather than replace
- `pkg/lifecycle/teardown.go` already has the `ExecuteTeardown` function — artifact upload step hooks in here before Terraform destroy
- `sidecars/audit-log/auditlog.go` already has `AuditEvent` struct and `Destination` interface — redaction filter wraps the destination, doesn't modify the struct

</specifics>

<code_context>
## Existing Code Insights

### Reusable Assets
- `pkg/profile/types.go`: `FilesystemPolicy` struct with `ReadOnlyPaths`/`WritablePaths` already defined — enforcement code in user-data and service_hcl just needs to read these fields
- `pkg/compiler/userdata.go`: EC2 user-data template — add bind mount block and spot poll loop here
- `pkg/compiler/service_hcl.go`: ECS task definition — add `readonlyRootFilesystem` and tmpfs volume mounts per writablePaths
- `pkg/lifecycle/teardown.go`: `ExecuteTeardown()` — artifact upload step inserts before `terraform destroy`
- `pkg/aws/spot.go`: `GetSpotInstanceID()` — exists; ECS Fargate spot handling is a new addition
- `sidecars/audit-log/auditlog.go`: `Destination` interface — redaction wraps any destination with a `RedactingDestination` decorator

### Established Patterns
- Profile compiler reads `SandboxProfile` fields and writes them into templates (userdata.go, service_hcl.go) — same pattern for new `spec.artifacts` fields
- `pkg/aws/` package for AWS SDK helpers — add SES helpers, S3 artifact uploader here
- Cobra command constructor `NewXxxCmd(cfg *config.Config)` — no new CLI commands in this phase, but `km create` output may show the assigned email address

### Integration Points
- `pkg/profile/types.go`: add `ArtifactsSpec` struct and `Artifacts ArtifactsSpec` field to `PolicySpec` or top-level spec
- `pkg/compiler/userdata.go`: bind mounts + spot poll loop + artifact upload script
- `pkg/compiler/service_hcl.go`: readonlyRootFilesystem + tmpfs volumes + SES IAM grant
- `sidecars/audit-log/auditlog.go`: `RedactingDestination` wraps existing destinations
- `pkg/lifecycle/teardown.go`: artifact upload before destroy
- `internal/app/cmd/create.go`: provision SES email address, output it to operator

</code_context>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 04-lifecycle-hardening-artifacts-email*
*Context gathered: 2026-03-22*
