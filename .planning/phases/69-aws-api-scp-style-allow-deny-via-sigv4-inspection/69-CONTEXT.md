# Phase 69: AWS API SCP-style allow/deny via SigV4 inspection - Context

**Gathered:** 2026-05-04
**Status:** Ready for planning
**Source:** Brainstorming session 2026-05-04 → SPEC.md

<domain>
## Phase Boundary

This phase delivers a service-level AWS API allow/deny gate inside the existing `sidecars/http-proxy`, driven by a new field on the SandboxProfile. The gate inspects SigV4 signatures on outbound HTTPS traffic to AWS endpoints, matches the service slug against an operator-declared allowlist, and either passes, logs, or blocks the call. Platform sidecars (system-uid users) are exempt by design.

**In scope:**
- New profile fields under `spec.sourceAccess.aws`: `inspection` (off/observe/enforce), `allowlist` ([service slugs] or `["*"]`)
- Host-regex CONNECT-time MITM on `*.amazonaws.com`, `*.amazonaws.com.cn`, `*.amazonaws-us-gov.com`, `*.api.aws`
- SigV4 `Authorization` header parsing (with `X-Amz-Credential` query-string fallback for pre-signed URLs)
- Three new audit event types: `aws_api_allowed`, `aws_api_blocked`, `aws_api_platform` (plus `aws_api_unsigned` pass-through marker)
- eBPF additions: new pinned `sock_to_uid` map populated by `cgroup/connect4`
- Platform-uid carve-out in the proxy: calls from uid below `KM_AWS_PLATFORM_UID_MAX` (default 1000) bypass the allowlist gate
- Composition with existing Bedrock metering: AWS gate runs first; `bedrock-runtime` requires explicit allowlist entry under `enforce`
- `km validate` cross-check: enforce mode + non-zero Bedrock budget requires `bedrock-runtime` in allowlist
- Learn mode: `km shell --learn` populates the new field with `inspection: observe`
- Two new `km doctor` checks: `aws_inspection_uid_map`, `aws_allowlist_known_services`
- Documentation: `docs/aws-allowlist.md` operator guide + CLAUDE.md update

**Out of scope (deferred to follow-up phases):**
- Operation-level entries (`s3:GetObject`) — schema accepts but ignores past-colon content; parsers are next phase
- Region pinning, account-id restrictions, STS AssumeRole-target restrictions
- IMDS gating (lives in eBPF, not the HTTP proxy)
- VPC endpoint hosts (`*.vpce.amazonaws.com`)
- Pricing / cost attribution beyond existing Bedrock metering
- Hot-reload of allowlist mid-sandbox (require `km destroy` + `km create`, matches existing GitHub allowlist behavior)
- Pre-signed URL generation tracing (only consumption is observable)

</domain>

<decisions>
## Implementation Decisions

### Schema
- Field path: `spec.sourceAccess.aws.inspection` (enum) and `spec.sourceAccess.aws.allowlist` (array of strings).
- Enum values: `off` | `observe` | `enforce`. Absence ≡ `off`.
- Allowlist required when `inspection != off`.
- `["*"]` is the only legal wildcard form. Mixing wildcard with explicit entries is a validation error.
- Allowlist matching is literal and case-sensitive against the SigV4 service slug. No normalization, no aliases.
- Schema additions live in `pkg/profile/types.go` and the embedded `schemas/sandbox_profile.schema.json`.
- Shorthand `awsInspection` / `awsAllowlist` (used in CLI examples and audit-log discussion) maps to the nested YAML form and to env vars `KM_AWS_INSPECTION` / `KM_AWS_ALLOWLIST`.

### Detection
- Two-stage detection in `sidecars/http-proxy`: host-regex CONNECT MITM, then SigV4 service-slug extraction at OnRequest.
- Host regex: `(.*\.)?(amazonaws\.com|amazonaws\.com\.cn|amazonaws-us-gov\.com|api\.aws)$`.
- Matched hosts get `goproxy.AlwaysMitm` so the inner request's `Authorization` header is readable.
- AWS host matcher registers ahead of the existing `OkConnect` passthrough; existing Bedrock matcher remains for its strict subset.
- SigV4 parser: extract third path component of `Credential=AKIA.../<date>/<region>/<service>/aws4_request`. Fall back to `X-Amz-Credential` query parameter for pre-signed/legacy.
- Anonymous AWS-host calls (no SigV4, no `X-Amz-Credential`) are emitted as `aws_api_unsigned` and pass through unchanged. Out of scope for v1 enforcement.
- New file: `sidecars/http-proxy/httpproxy/aws.go`. Exposes `WithAWSAllowlist(mode, list)` as a `ProxyOption` (matches existing functional-options pattern in `httpproxy/proxy.go`).

### Three modes
| Inspection | Allowlist | Behavior |
|---|---|---|
| `off` (default) | (any) | No gating, no events. Existing Bedrock metering unchanged. |
| `observe` | `["*"]` | Every AWS call logged as `aws_api_allowed`. No blocking. |
| `observe` | `[]` | Every AWS call logged as `aws_api_blocked` (reason `empty_allowlist`). No blocking. |
| `observe` | `[s3, sts]` | Calls logged as allowed/blocked based on match. No blocking. |
| `enforce` | `["*"]` | Wildcard pass-through; events emitted for audit. |
| `enforce` | `[]` | All AWS calls 403'd by the proxy. |
| `enforce` | `[s3, sts]` | Listed services pass (with allowed events); rest 403'd (with blocked events). |

### Platform-uid exemption
- Extend `pkg/ebpf/bpf.c` `cgroup/connect4` hook to capture `bpf_get_current_uid_gid()` into a new pinned BPF map `sock_to_uid` keyed by socket cookie.
- Existing `sock_to_original_ip` / `sock_to_original_port` maps establish the pattern; the new map is additive.
- Proxy loads the new pinned map in `transparent.go` and exposes `GetCallerUID(socketCookie)` parallel to the existing original-IP lookup.
- New env var `KM_AWS_PLATFORM_UID_MAX` (default `1000`) defines the boundary. Calls from `uid < KM_AWS_PLATFORM_UID_MAX` skip the allowlist gate and emit `aws_api_platform`.
- Best-effort `caller` field via `getpwuid` lookup against `/etc/passwd`; absence is graceful (log uid alone).
- `km doctor` `aws_inspection_uid_map` check verifies on a sample running sandbox that a known platform user (e.g., `km-mail-poller`) has uid below the threshold.

### Composition with Bedrock
- Order of inspectors: eBPF cgroup/connect4 redirect → AWS allowlist gate (new) → GitHub repo gate (peer, runs only on GitHub hosts) → Bedrock metering.
- AWS allowlist gate is composable, not exclusive. Bedrock metering keeps its current behavior verbatim. Only change: an upstream allowlist might 403 the request before Bedrock metering ever sees it.
- `km validate` rule: when `inspection: enforce` and `spec.budget.aiSpend.*` is non-zero (or Bedrock budget plumbing is otherwise active), `bedrock-runtime` MUST be present in `awsAllowlist`. Validator error names the missing entry.

### Audit events
- New event types emitted on proxy stdout, consumed by audit-log sidecar (no sidecar changes):
  - `aws_api_allowed`: `{sandbox_id, service, region, host, method, path, mode}`
  - `aws_api_blocked`: same shape + `reason: not_in_allowlist | empty_allowlist`
  - `aws_api_platform`: `{sandbox_id, service, region, host, uid, caller}` (no mode — fires regardless of mode)
  - `aws_api_unsigned`: `{sandbox_id, host, method, path}` (passthrough marker)

### Learn mode
- `internal/app/cmd/shell_learn.go`: on `aws_api_allowed` or `aws_api_blocked` events from a non-platform uid, deduplicate the `service` slug into the generated profile's `spec.sourceAccess.aws.allowlist`.
- Generated profile defaults to `inspection: observe` (not `enforce`) so the operator reviews the inferred list before flipping.
- Test fixtures added to `internal/app/cmd/shell_learn_test.go` matching the existing parser test pattern.

### km doctor checks
- `aws_inspection_uid_map`: if active region has at least one inspection-enabled sandbox, sample one running instance, look up `km-mail-poller`'s uid via SSM RunCommand (existing pattern in other checks), confirm `uid < KM_AWS_PLATFORM_UID_MAX`. WARN if user missing; FAIL if uid above threshold.
- `aws_allowlist_known_services`: for each profile referenced by an active sandbox, parse `awsAllowlist`, validate each entry against a vetted list of common SigV4 service slugs in `pkg/aws/sigv4_services.go`. WARN (not FAIL) on unknowns.
- Both checks honor `--all-regions` and existing dry-run / cleanup conventions.

### CLI surface
- No new commands. Effects on existing commands:
  - `km validate` enforces the Bedrock-budget cross-check and the wildcard-mixing rule.
  - `km create` reads `spec.sourceAccess.aws.*` and emits `KM_AWS_INSPECTION` + `KM_AWS_ALLOWLIST` (CSV) + `KM_AWS_PLATFORM_UID_MAX` env vars into the http-proxy systemd unit.
  - `km shell --learn` generates the new field.
  - `km doctor` runs the new checks.

### Claude's Discretion
- Exact SigV4 parser implementation details (header parse vs. regex), pinned-map key/value layout for `sock_to_uid`, ProxyOption struct shape inside `aws.go`.
- Test fixture organization and naming inside `httpproxy/aws_test.go` (mirror `httpproxy/github_test.go`).
- Vetted SigV4 service-slug list contents (seed from AWS SDK service IDs; document the update process).
- Exact wording of validation error messages and audit event field ordering.
- Whether to wire the AWS gate behind a feature flag for staged rollout (operator preference; default on once the phase ships).
- Plan-file ordering and dependency graph (orchestrator-level decision; planner produces).

</decisions>

<specifics>
## Specific Ideas

### Demo storyboard (acceptance flow)
1. **Wide open.** Profile A: `inspection: enforce`, `awsAllowlist: ["*"]`. Inside the sandbox: `aws sts get-caller-identity`, `aws s3 ls`, `aws dynamodb list-tables`. All succeed. Audit log streams `aws_api_allowed` for each.
2. **Locked down.** Profile B: same as A but `awsAllowlist: []`. Same three commands all return 403. Audit log streams `aws_api_blocked` with `reason=empty_allowlist`. Concurrently `km email read <sandbox>` continues to work — proves platform exemption.
3. **Observe.** Profile C: `inspection: observe`, `awsAllowlist: []`. AWS CLI succeeds end-to-end; audit log shows everything as blocked. Operator inventories the services, copies into allowlist, flips to enforce.
4. **Learn-derived.** `km shell --learn` against a permissive profile, run `aws s3 ls`, `aws dynamodb list-tables`, `aws sts get-caller-identity`, exit. Generated YAML contains `inspection: observe` + `allowlist: [dynamodb, s3, sts]` (alphabetized).

### Implementation slice estimate (rough breakdown for planner)
1. Schema + validation (pkg/profile + JSON Schema + validator unit tests including Bedrock cross-check and wildcard-mixing rule)
2. Compiler + userdata: plumb `KM_AWS_INSPECTION`, `KM_AWS_ALLOWLIST`, `KM_AWS_PLATFORM_UID_MAX` env vars through `pkg/compiler/` into the http-proxy systemd unit
3. eBPF uid map: `pkg/ebpf/bpf.c` change + Go-side map loader in `transparent.go`. Small, additive. ⚠️ Verifier risk — exercise early with a small spike before slices that depend on it.
4. Proxy AWS inspector: new `sidecars/http-proxy/httpproxy/aws.go` with SigV4 parser, allowlist matcher, platform-uid bypass, four audit event emitters. Mirror `httpproxy/github_test.go` test patterns.
5. Proxy wiring: `sidecars/http-proxy/main.go` reads env vars, calls `WithAWSAllowlist(...)`. CONNECT regex registered ahead of existing handlers.
6. Bedrock cross-check: `pkg/profile/validate.go` rule.
7. Learn-mode integration: `internal/app/cmd/shell_learn.go` parser additions + fixtures.
8. `km doctor` checks: two new checks in the existing doctor framework.
9. Documentation: new `docs/aws-allowlist.md` operator guide; CLAUDE.md profile-fields table addition.
10. End-to-end smoke: real EC2 sandbox running through the four-flow demo storyboard; capture logs as evidence in the phase verification doc.

### Risks & open notes for planner
- **eBPF verifier behavior** when adding the uid map. Low-medium risk. A 30-minute spike on a real cgroup BPF reload should land before any slice depends on it. Could be Plan 69-00.
- **SigV4 parser edge cases**: unsigned anonymous S3, sigv4a multi-region access points, pre-signed URLs with encoded credential scope. v1 stance: log unsigned, never block, never count toward observe. Document explicitly in operator guide.
- **Service-name drift**: AWS occasionally renames services. Doctor check warns rather than fails for this reason.
- **Bedrock-budget cross-check ergonomics**: validator must produce a clear actionable error message ("budget enforces metering on bedrock-runtime; add it or set the budget to zero").
- **Multi-tenant assumption**: per-sandbox env vars are read at proxy startup. Hot-reload not in scope; profile changes require `km destroy` + `km create`. Matches existing GitHub allowlist behavior.

### File-system / module touchpoints (for planner dependency graph)
- `pkg/profile/types.go` (schema struct)
- `pkg/profile/schemas/sandbox_profile.schema.json` (embedded JSON Schema)
- `pkg/profile/validate.go` (Bedrock cross-check + wildcard-mixing rule)
- `pkg/compiler/userdata.go` (env var injection into systemd unit)
- `pkg/ebpf/bpf.c` (sock_to_uid map population)
- `pkg/ebpf/loader.go` (map pinning + loading on Go side)
- `pkg/aws/sigv4_services.go` (new file: vetted service-slug list)
- `sidecars/http-proxy/main.go` (option wiring)
- `sidecars/http-proxy/httpproxy/proxy.go` (host regex + handler registration order)
- `sidecars/http-proxy/httpproxy/aws.go` (new file: parser + matcher + emitters)
- `sidecars/http-proxy/httpproxy/transparent.go` (caller-uid lookup)
- `internal/app/cmd/shell_learn.go` (learn-mode parser additions)
- `internal/app/cmd/doctor*.go` (two new checks)
- `docs/aws-allowlist.md` (new operator guide)
- `CLAUDE.md` (profile-fields table addition)

</specifics>

<deferred>
## Deferred Ideas

- **Operation-level allowlist entries** (`s3:GetObject`, `dynamodb:Query`). Schema accepts the colon-suffixed form from day 1 but ignores past-colon content. Follow-up phase wires per-service operation parsers (HTTP method+path for REST services, `X-Amz-Target` for JSON-RPC, query-string `Action=` for query protocol).
- **Region/account-id pinning** (e.g., STS AssumeRole into specific accounts only, deny ec2 outside us-east-1). Same forward-compat — schema is grouped under `aws` so sibling fields like `allowedRegions` slot cleanly.
- **IMDS gating** (169.254.169.254). Lives in eBPF, not the HTTP proxy. Separate phase.
- **VPC endpoint hosts** (`*.vpce.amazonaws.com`). Not yet in the host regex. Additive extension if a future operator needs them.
- **Cost attribution / pricing pipeline** beyond existing Bedrock metering. Audit events have enough fingerprint for a downstream cost system, but no new DynamoDB schema in this phase.
- **Pre-signed URL generation tracing** outside the sandbox is invisible to the proxy. Out of scope by physics.
- **Hot-reload of allowlist mid-sandbox**. Operators expect `km destroy` + `km create` for profile changes; matches existing GitHub allowlist behavior.

</deferred>

---

*Phase: 69-aws-api-scp-style-allow-deny-via-sigv4-inspection*
*Context gathered: 2026-05-04 via brainstorming session → SPEC.md*
