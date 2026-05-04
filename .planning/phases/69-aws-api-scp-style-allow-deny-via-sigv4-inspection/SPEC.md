# Phase 69 — AWS API SCP-style allow/deny via SigV4 inspection

**Status:** draft (pre-plan). Hand to `/gsd:plan-phase` once the operator approves the brief.
**Author:** brainstormed 2026-05-04
**Depends on:** Phase 6 (Bedrock metering + http-proxy MITM), Phase 40 (eBPF cgroup egress + transparent proxy maps), Phase 62/63 (audit-log sidecar event consumption)

## Notation

Throughout this brief, `awsInspection` is shorthand for `spec.sourceAccess.aws.inspection` and `awsAllowlist` is shorthand for `spec.sourceAccess.aws.allowlist`. The same names map to the env vars `KM_AWS_INSPECTION` and `KM_AWS_ALLOWLIST` exposed to the http-proxy at runtime. Profile YAML uses the nested form; CLI examples and audit-log discussion use the shorthand.

## Goal

Operators can declare a service-level AWS allowlist on a SandboxProfile and the http-proxy will allow, log-only, or block AWS API calls (any service that uses SigV4) made by the sandbox user, while platform sidecars (km-mail-poller, km-slack-inbound-poller, OTEL exporter, metadata sync) remain unaffected. The control surface is intentionally SCP-shaped — set the list, choose a mode, get a uniform gate over `*.amazonaws.com` — and is forward-compatible with operation-level entries (`s3:GetObject`) in a follow-up phase.

## Why

Today the http-proxy gates GitHub by repo and meters Bedrock by token, but AWS API traffic from the sandbox is otherwise wide open. A sandbox with default IAM can call STS AssumeRole, S3, DynamoDB, EC2, etc., bounded only by the IAM policies attached to its instance role. SCPs at the org level are too coarse to pin individual sandboxes, IAM is too verbose to maintain per-profile, and the existing audit log has no `aws_api_*` events to even tell an operator what services a sandbox touched. This phase closes that gap with a single, profile-driven knob and a clean audit trail.

## Success criteria

A reviewer can verify each as TRUE end-to-end on a real EC2 sandbox.

1. A profile with `spec.sourceAccess.aws.inspection: enforce` and `awsAllowlist: ["*"]` lets `aws sts get-caller-identity` succeed inside the sandbox; the audit log shows an `aws_api_allowed` event with `service=sts`, `mode=enforce`.
2. A profile with `inspection: enforce` and `awsAllowlist: []` causes `aws sts get-caller-identity` to receive HTTP 403 from the proxy with a clear `KM_AWS_BLOCKED` body; the audit log shows `aws_api_blocked` with `reason=empty_allowlist`. Concurrently, `km email read <sandbox>` (driven by `km-mail-poller` inside the sandbox) continues to succeed and emits `aws_api_platform` events for SES + S3.
3. A profile with `inspection: observe` and `awsAllowlist: []` lets all AWS CLI calls through unmodified, but the audit log shows every call as `aws_api_blocked` (mode=observe). This is the "inventory before enforce" workflow.
4. `km shell --learn` against a profile with broad egress, after the operator runs `aws s3 ls`, `aws dynamodb list-tables`, `aws sts get-caller-identity`, generates a profile with `inspection: observe` and `allowlist` containing the exact set `[dynamodb, s3, sts]`.
5. A profile with `inspection: enforce`, `awsAllowlist: ["s3"]`, and a non-zero Bedrock budget fails `km validate` with a message naming `bedrock-runtime` as the missing entry.
6. `km doctor` runs the two new checks (`aws_inspection_uid_map`, `aws_allowlist_known_services`) and reports them green on a fresh region with one inspection-enabled sandbox in each of the three modes.
7. `aws_api_allowed`, `aws_api_blocked`, and `aws_api_platform` events flow through the audit-log sidecar to its configured destination (CloudWatch Logs in default deployments) with `sandbox_id`, `service`, `region`, `host`, `method`, `path`, `mode`, and (for platform events) `uid` + `caller` fields populated.

## Approach

### Schema additions

Under `spec.sourceAccess.aws` (parallel to `spec.sourceAccess.github`):

```yaml
spec:
  sourceAccess:
    aws:
      inspection: off | observe | enforce   # default off; absence == off
      allowlist:                             # SigV4 service-name slugs (or "*")
        - sts
        - s3
        - dynamodb
        - bedrock-runtime
```

Validation rules:
- `inspection` enum: `off`, `observe`, `enforce`. Absence ≡ `off`.
- `allowlist` is required when `inspection != off`.
- `["*"]` is the only legal use of the wildcard. Mixing wildcard with explicit entries is a validation error.
- When `inspection: enforce` and the profile sets a non-zero Bedrock budget (`spec.budget.aiSpend.*`), the validator requires `bedrock-runtime` in the allowlist. Catches the most common footgun at `km validate` time before a sandbox ever provisions.
- Allowlist entries are matched literally and case-sensitively against the SigV4 service slug. No normalization, no aliases.

Schema lives in `pkg/profile/types.go` and the embedded `schemas/sandbox_profile.schema.json`.

### Detection mechanism

Two stages, both inside `sidecars/http-proxy`:

**Stage 1 — host filter at CONNECT time.** Add a regex `(.*\.)?(amazonaws\.com|amazonaws\.com\.cn|amazonaws-us-gov\.com|api\.aws)$` matched ahead of the existing `OkConnect` passthrough. Matched hosts get `goproxy.AlwaysMitm`. The Bedrock matcher already AlwaysMitm's its host subset; the AWS matcher is the broader superset and runs first. If the host matches the AWS regex but not Bedrock, only the AWS gate runs. If both match, AWS gate runs first; on allow, Bedrock metering proceeds.

**Stage 2 — SigV4 service extraction at OnRequest.** Parse the `Authorization` header:

```
Authorization: AWS4-HMAC-SHA256 Credential=AKIA.../20260504/us-east-1/s3/aws4_request, ...
```

Extract the third path component of the credential scope — the service slug. Fall back to the `X-Amz-Credential` query parameter for pre-signed URLs and legacy query auth. If neither is present, the request is treated as "non-AWS-on-AWS-host" (e.g., S3 anonymous reads of a public bucket); emit `aws_api_unsigned` and pass through unchanged in all modes — out of scope for v1.

The proxy's policy module is the new file `sidecars/http-proxy/httpproxy/aws.go`, exposing `WithAWSAllowlist(mode, list)` as a `ProxyOption` (matches the existing functional-options pattern; no plug-in framework introduced).

### Platform-uid exemption

The sandbox runs platform sidecars as system users (uids < 1000) and the agent/operator workload as the `sandbox` user (uid ≥ 1000). The new gate must apply only to the latter.

Mechanics:
1. Extend `pkg/ebpf/bpf.c` to capture `bpf_get_current_uid_gid()` on `cgroup/connect4` and stash it in a new pinned BPF map `sock_to_uid` keyed by socket cookie. The existing `sock_to_original_ip` / `sock_to_original_port` maps already establish the pattern; the new map is additive and small.
2. The proxy already loads pinned maps in `transparent.go`. Add a `GetCallerUID(socketCookie)` lookup parallel to the existing original-IP lookup.
3. New env var `KM_AWS_PLATFORM_UID_MAX` (default `1000`) defines the boundary. Calls from `uid < KM_AWS_PLATFORM_UID_MAX` skip the allowlist gate and emit `aws_api_platform` with the `uid` and resolved `caller` (best-effort lookup via `getpwuid` from `/etc/passwd`; graceful fallback if absent).
4. `km doctor`'s `aws_inspection_uid_map` check verifies on a sample running sandbox that a known platform user (e.g., the `km-mail-poller` system user created in userdata) has a uid below the threshold. Drift between the userdata template and the env var would be caught here.

### Composition with existing inspectors

| Stage | Order | Purpose |
|---|---|---|
| eBPF cgroup/connect4 | 1 | CIDR allow/deny + redirect to proxy 127.0.0.1:3128 (unchanged). |
| Proxy AWS allowlist gate (new) | 2 | Service-level allow/deny for SigV4 traffic; emits `aws_api_*` events. |
| Proxy GitHub repo gate | 2 (peer) | Existing; runs on GitHub hosts only. |
| Proxy Bedrock metering | 3 | Runs only after AWS gate allows; existing token metering and budget logic unchanged. |

The AWS allowlist gate is composable, not exclusive. The Bedrock metering path keeps its current behavior verbatim — the only change is that an upstream allowlist might 403 the request before metering ever sees it.

### Audit events

New event types, emitted on proxy stdout, consumed by audit-log sidecar (Phase 3 plumbing, no changes there):

```json
{"event_type":"aws_api_allowed","sandbox_id":"sb-x","service":"s3","region":"us-east-1","host":"s3.us-east-1.amazonaws.com","method":"GET","path":"/mybucket/key","mode":"enforce"}
{"event_type":"aws_api_blocked","sandbox_id":"sb-x","service":"ec2","region":"us-east-1","host":"ec2.us-east-1.amazonaws.com","method":"POST","path":"/","mode":"enforce","reason":"not_in_allowlist"}
{"event_type":"aws_api_platform","sandbox_id":"sb-x","service":"sqs","region":"us-east-1","host":"sqs.us-east-1.amazonaws.com","uid":995,"caller":"km-slack-inbound-poller"}
{"event_type":"aws_api_unsigned","sandbox_id":"sb-x","host":"public-bucket.s3.us-east-1.amazonaws.com","method":"GET","path":"/key"}
```

Reasons for `aws_api_blocked`: `not_in_allowlist`, `empty_allowlist`. Future ops-level work may add `not_in_operations`.

### Learn-mode integration

`km shell --learn` already parses proxy stdout events from S3 to populate generated profile fields. Extend `internal/app/cmd/shell_learn.go`:

- On any `aws_api_allowed` or `aws_api_blocked` event from a non-platform uid, add the `service` slug to the deduplicated set tracked for `spec.sourceAccess.aws.allowlist`.
- The generated profile sets `inspection: observe` (not `enforce`) so the operator reviews the inferred list before flipping. Documented in the file's leading comment block.
- No changes to the existing test fixture format; add new fixtures under `internal/app/cmd/shell_learn_test.go` for the AWS event types.

### `km doctor` checks

Two new checks under the existing doctor framework:

- `aws_inspection_uid_map` — if the active region has at least one inspection-enabled sandbox, sample one running instance, look up `km-mail-poller`'s uid via SSM `RunCommand` (already used by other doctor checks), confirm `uid < KM_AWS_PLATFORM_UID_MAX`. WARN if the sandbox is missing the system user; FAIL if the user exists but uid is above threshold.
- `aws_allowlist_known_services` — for each profile referenced by an active sandbox, parse `awsAllowlist`, validate each entry against a vetted list of common SigV4 service slugs (shipped as `pkg/aws/sigv4_services.go`, derived from the AWS SDK service IDs). WARN (not FAIL) on unknowns so AWS adding a new service doesn't break the platform.

Both checks honor `--all-regions` and the existing dry-run / cleanup conventions.

### CLI surface

No new commands. Effects of existing commands:

- `km validate` enforces the Bedrock-budget cross-check.
- `km create` reads `spec.sourceAccess.aws.*` and emits `KM_AWS_INSPECTION` + `KM_AWS_ALLOWLIST` (CSV) + `KM_AWS_PLATFORM_UID_MAX` env vars into the http-proxy systemd unit. No userdata template changes beyond env var additions.
- `km shell --learn` generates the new field.
- `km doctor` runs the new checks.

## Out of scope

- **Operation-level entries** (`s3:GetObject`). Schema already accepts `service:operation` strings without breaking; parsers are a follow-up phase. v1 ignores anything past the first `:`.
- **Region pinning and account-id restrictions** (e.g., STS AssumeRole into specific accounts). Same forward-compat — the schema is grouped under `aws` so new sibling fields like `allowedRegions` slot in cleanly.
- **IMDS gating** (169.254.169.254). IMDS doesn't go through the proxy; eBPF would have to enforce. Track separately.
- **VPC endpoints** (`*.vpce.amazonaws.com`). Not yet in the host regex; if a future operator needs them, the regex extends additively. Out of scope for v1.
- **Pricing / cost attribution** beyond existing Bedrock metering. The audit events have enough fingerprint for a downstream cost pipeline, but no new DynamoDB schema in this phase.
- **Pre-signed URL generation** done outside the sandbox is invisible to us; only consumption from inside is observable.

## Demo storyboard

For a Phase 69 readout, run these four flows in sequence:

1. **Wide open.** Profile A: `inspection: enforce`, `awsAllowlist: ["*"]`. Inside the sandbox: `aws sts get-caller-identity`, `aws s3 ls`, `aws dynamodb list-tables`. All succeed. Show the audit log streaming `aws_api_allowed` for each.
2. **Locked down.** Profile B: same as A but `awsAllowlist: []`. Inside: same three commands. All return 403 from the proxy. Show audit log streaming `aws_api_blocked` with `reason=empty_allowlist`. Concurrently demo `km email read <sandbox>` continuing to work — proves platform exemption.
3. **Observe.** Profile C: `inspection: observe`, `awsAllowlist: []`. AWS CLI works, audit log shows everything as blocked. Operator inventories the services, copies them into the allowlist, flips to `enforce`.
4. **Learn-derived.** `km shell --learn` against a permissive profile, run a few AWS CLI calls, exit. Show generated YAML containing `inspection: observe` and an allowlist of exactly the services that were touched.

## Implementation slice estimate

Rough breakdown for `/gsd:plan-phase` to refine. Work is heavily proxy-side; eBPF change is small and additive.

1. **Schema + validation.** `pkg/profile/types.go`, embedded JSON schema, validator unit tests including the Bedrock cross-check.
2. **Compiler + userdata.** Plumb the three env vars through `pkg/compiler/` and into the http-proxy systemd unit. No userdata template additions beyond env vars.
3. **eBPF uid map.** `pkg/ebpf/bpf.c` + Go-side map loader in `transparent.go`. New pinned map `sock_to_uid`; minimal verifier impact.
4. **Proxy AWS inspector.** New `sidecars/http-proxy/httpproxy/aws.go` with SigV4 parser, allowlist matcher, platform-uid bypass, three audit event emitters. Pure unit-testable; mirror `httpproxy/github_test.go` test patterns.
5. **Proxy wiring.** `sidecars/http-proxy/main.go` reads env vars, calls `WithAWSAllowlist(...)`. CONNECT regex registered ahead of existing handlers.
6. **Bedrock cross-check.** `pkg/profile/validate.go` rule.
7. **Learn-mode integration.** `internal/app/cmd/shell_learn.go` parser additions + fixtures.
8. **`km doctor` checks.** Two new checks in the existing doctor framework.
9. **Documentation.** New `docs/aws-allowlist.md` operator guide; CLAUDE.md profile-fields table addition.
10. **End-to-end smoke.** Real EC2 sandbox running through the demo storyboard's four flows; capture logs as evidence in the phase verification doc.

Eight to ten plan files in the standard `69-NN-PLAN.md` shape feels about right. The eBPF change is the only place where the verifier could surprise us — worth a small spike (Plan 69-00 or a research note) before the slice that depends on it.

## Risks & open questions

- **eBPF verifier behavior when adding the uid map.** Low risk (uid capture is the simplest of the BPF helpers in use here), but should be exercised early in the phase, not in the last plan. A 30-minute spike on a real cgroup BPF reload would de-risk this.
- **SigV4 parser edge cases.** Unsigned anonymous S3, sigv4a (multi-region access points), pre-signed URLs with encoded credential scope. The proposal here is "log unsigned, never block, never count toward observe" — explicit in code and documented in the operator guide so it doesn't surprise an auditor.
- **Service-name drift.** AWS occasionally renames services (`ses` vs `email`, `bedrock` vs `bedrock-runtime`). The `aws_allowlist_known_services` doctor check warns rather than fails for exactly this reason; the vetted list lives in version control and gets updated as a normal commit when AWS adds something.
- **Bedrock-budget cross-check ergonomics.** Some operators set a budget but don't actually call Bedrock. The validator will refuse those profiles unless they include `bedrock-runtime`. Acceptable cost; clearer error message ("budget enforces metering on bedrock-runtime; add it or set the budget to zero") softens it.
- **Multi-tenant assumption.** Per-sandbox env vars are read at proxy startup. Hot-reload of the allowlist mid-sandbox is not in scope; profile changes require `km destroy` + `km create`. Matches existing GitHub allowlist behavior — operators expect this.

## Out-of-band notes

- Phase number 69 assumes 67/67.1/68 stay where they are in the roadmap. If a quick task lands at 68.x ahead of this, renumber accordingly.
- This is a standalone phase; no dependency on Phase 68 (Slack transcripts) or Phase 67 (Slack inbound). Could ship in any order relative to those.
- The forward-compat path to operation-level entries is real: when the time comes, the parser in `aws.go` extracts the operation per-service (HTTP method+path for REST services, `X-Amz-Target` for JSON-RPC, query-string `Action=` for query protocol). v1 ignores it; v2 wires it into a refined matcher. No schema migration needed.
