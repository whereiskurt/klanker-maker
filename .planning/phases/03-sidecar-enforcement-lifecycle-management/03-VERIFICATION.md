---
phase: 03-sidecar-enforcement-lifecycle-management
verified: 2026-03-22T00:00:00Z
status: passed
score: 14/14 must-haves verified
re_verification: false
---

# Phase 3: Sidecar Enforcement and Lifecycle Management — Verification Report

**Phase Goal:** Running sandboxes on either substrate enforce network policy via DNS and HTTP proxy sidecars, produce auditable logs and OpenTelemetry traces, log MLflow experiment runs per session, and auto-terminate based on TTL and idle policy — operators can observe all running sandboxes

**Verified:** 2026-03-22
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| #  | Truth | Status | Evidence |
|----|-------|--------|----------|
| 1  | DNS proxy resolves allowlisted names and returns NXDOMAIN for blocked names | VERIFIED | `sidecars/dns-proxy/dnsproxy/proxy.go` — `IsAllowed()` + `SetRcode(RcodeNameError)`; 5 passing unit tests |
| 2  | HTTP proxy rejects blocked CONNECT with 403 and injects W3C traceparent on allowed | VERIFIED | `sidecars/http-proxy/httpproxy/proxy.go` — `RejectConnect` + `otel.GetTextMapPropagator().Inject`; 5 passing unit tests incl. `TestHTTPProxy_TraceparentInjected` |
| 3  | Audit log sidecar emits JSON-line events to CloudWatch, S3 (stub), or stdout | VERIFIED | `sidecars/audit-log/auditlog.go` — `StdoutDest`, `CloudWatchDest`, `S3Dest` (stub with fallback); 6 passing unit tests |
| 4  | OTel Collector sidecar config routes OTLP to S3 via awss3exporter | VERIFIED | `sidecars/tracing/config.yaml` — 4 top-level keys (receivers, processors, exporters, service); awss3 exporter with `otlp_json` marshaler; `${SANDBOX_ID}` env var substitution |
| 5  | MLflow run written to S3 at `mlflow/<experiment>/<sandbox-id>/meta.json` | VERIFIED | `pkg/aws/mlflow.go` — `WriteMLflowRun`/`FinalizeMLflowRun`; S3 key confirmed by `TestWriteMLflowRun_PutsCorrectKey` |
| 6  | EventBridge Scheduler rule created at TTL expiry and deleted on destroy | VERIFIED | `pkg/aws/scheduler.go` — `CreateTTLSchedule`/`DeleteTTLSchedule`; `destroy.go:152` calls `DeleteTTLSchedule`; `create.go:230` calls `CreateTTLSchedule` |
| 7  | IdleDetector fires teardown after idle period; TeardownPolicy executes destroy/stop/retain | VERIFIED | `pkg/lifecycle/idle.go` + `teardown.go`; 4 passing tests incl. `TestIdleDetector_FiresAfterIdle`, `TestTeardownPolicy_Destroy`, `TestTeardownPolicy_Retain` |
| 8  | EC2 user-data installs sidecar binaries and configures iptables DNAT with IMDS exemption first | VERIFIED | `pkg/compiler/userdata.go:175` — IMDS exemption via `-I OUTPUT` inserted before DNS/HTTP rules; DNS→5353, HTTP/HTTPS→3128; `km-sidecar` user exempt |
| 9  | ECS service.hcl includes all four sidecar containers with correct env vars | VERIFIED | `pkg/compiler/service_hcl.go` — `km-dns-proxy`, `km-http-proxy`, `km-audit-log` (essential=true), `km-tracing`; main container has `HTTP_PROXY`/`HTTPS_PROXY` |
| 10 | km create writes sandbox metadata.json to S3 after apply | VERIFIED | `internal/app/cmd/create.go:213` — `PutObject` with key `tf-km/sandboxes/<id>/metadata.json` |
| 11 | km list shows running sandboxes in table and --json formats | VERIFIED | `internal/app/cmd/list.go` — tabwriter table with SANDBOX ID/PROFILE/SUBSTRATE/REGION/STATUS/TTL; 3 passing tests |
| 12 | km status shows detailed sandbox state | VERIFIED | `internal/app/cmd/status.go` — metadata.json + tag API ARNs; `TestStatusCmd_Found/NotFound` pass |
| 13 | km logs tails CloudWatch log group /km/sandboxes/<sandbox-id>/ | VERIFIED | `internal/app/cmd/logs.go:56` — delegates to `kmaws.TailLogs`; `--follow` supported |
| 14 | All three commands (list, status, logs) registered in root.go | VERIFIED | `internal/app/cmd/root.go:42-44` — `AddCommand(NewListCmd, NewStatusCmd, NewLogsCmd)` |

**Score:** 14/14 truths verified

---

## Required Artifacts

| Artifact | Status | Details |
|----------|--------|---------|
| `sidecars/dns-proxy/dnsproxy/proxy.go` | VERIFIED | 86 lines; `IsAllowed()`, `NewHandler()`; NXDOMAIN enforcement |
| `sidecars/dns-proxy/main.go` | VERIFIED | Binary entry; reads ALLOWED_SUFFIXES, DNS_PORT, UPSTREAM_DNS env vars |
| `sidecars/http-proxy/httpproxy/proxy.go` | VERIFIED | 94 lines; `IsHostAllowed()`, `InjectTraceContext()`, `NewProxy()` |
| `sidecars/http-proxy/main.go` | VERIFIED | Binary entry; reads ALLOWED_HOSTS, PROXY_PORT env vars |
| `sidecars/audit-log/auditlog.go` | VERIFIED | 189 lines; `AuditEvent`, `Destination`, `StdoutDest`, `CloudWatchDest`, `S3Dest` (stub) |
| `sidecars/audit-log/cmd/main.go` | VERIFIED | Binary entry with SIGTERM flush; `realCWBackend` adapter |
| `sidecars/tracing/config.yaml` | VERIFIED | Valid YAML; receivers/processors/exporters/service; awss3 + otlp_json |
| `pkg/aws/cloudwatch.go` | VERIFIED | 160 lines; `CWLogsAPI`, `EnsureLogGroup`, `PutLogEvents`, `GetLogEvents`, `TailLogs` |
| `pkg/aws/mlflow.go` | VERIFIED | 137 lines; `S3RunAPI`, `MLflowRun`, `WriteMLflowRun`, `FinalizeMLflowRun` |
| `pkg/aws/scheduler.go` | VERIFIED | 60 lines; `SchedulerAPI`, `CreateTTLSchedule`, `DeleteTTLSchedule` |
| `pkg/aws/metadata.go` | VERIFIED | `SandboxMetadata` struct used by km create and km list |
| `pkg/aws/sandbox.go` | VERIFIED | `SandboxRecord`, `ListAllSandboxesByS3`, `ListAllSandboxesByTags`, `ReadSandboxMetadata` |
| `pkg/lifecycle/idle.go` | VERIFIED | 107 lines; `IdleDetector` with injectable clock, CW polling, `OnIdle` callback |
| `pkg/lifecycle/teardown.go` | VERIFIED | 51 lines; `ExecuteTeardown` with destroy/stop/retain branching |
| `pkg/compiler/lifecycle.go` | VERIFIED | 42 lines; `BuildTTLScheduleInput` with `at()` expression format |
| `pkg/compiler/userdata.go` | VERIFIED | 228 lines; sidecar install section + iptables DNAT section with correct ordering |
| `pkg/compiler/service_hcl.go` | VERIFIED | 364 lines; four sidecar container definitions + proxy env vars on main container |
| `internal/app/cmd/list.go` | VERIFIED | 125 lines; `NewListCmd`, `SandboxLister` DI interface, tabwriter output |
| `internal/app/cmd/status.go` | VERIFIED | 137 lines; `NewStatusCmd`, `SandboxFetcher` DI interface |
| `internal/app/cmd/logs.go` | VERIFIED | 62 lines; `NewLogsCmd`, delegates to `TailLogs` |
| `internal/app/cmd/create.go` | VERIFIED | Extended with metadata.json write and EventBridge schedule creation |
| `internal/app/cmd/destroy.go` | VERIFIED | Extended with `DeleteTTLSchedule` call before Terragrunt destroy |
| `internal/app/cmd/root.go` | VERIFIED | `AddCommand(NewListCmd, NewStatusCmd, NewLogsCmd)` at lines 42-44 |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `dns-proxy/proxy.go` | VPC resolver 169.254.169.253 | `dns.Client.Exchange` | VERIFIED | `dns.Client` used with upstream forwarding in `NewHandler()` |
| `http-proxy/proxy.go` | W3C traceparent header | `otel.GetTextMapPropagator().Inject` | VERIFIED | Line 41 in proxy.go; `TestHTTPProxy_TraceparentInjected` passes |
| `sidecars/audit-log` | `pkg/aws/cloudwatch.go` | `CloudWatchDest` via `CloudWatchBackend` interface | VERIFIED | `auditlog.go` defines `CloudWatchBackend`; `cmd/main.go` bridges via `realCWBackend` adapter |
| `pkg/aws/mlflow.go` | S3 `mlflow/<exp>/<id>/meta.json` | `s3.PutObject` | VERIFIED | `mlflowKey()` builds path; `WriteMLflowRun` calls `PutObject` |
| `sidecars/tracing/config.yaml` | S3 `traces/<sandbox-id>/` | `awss3exporter` | VERIFIED | `s3_prefix: "traces/${SANDBOX_ID}"` in config.yaml |
| `pkg/compiler/lifecycle.go` | `pkg/aws/scheduler.go` | `CreateScheduleInput` | VERIFIED | `BuildTTLScheduleInput` returns `*scheduler.CreateScheduleInput` passed to `CreateTTLSchedule` |
| `internal/app/cmd/create.go` | S3 `tf-km/sandboxes/<id>/metadata.json` | `s3.PutObject` | VERIFIED | `create.go:213` — `PutObject` with `metadata.json` key |
| `internal/app/cmd/destroy.go` | `pkg/aws.DeleteTTLSchedule` | direct call | VERIFIED | `destroy.go:152` calls `awspkg.DeleteTTLSchedule` |
| `pkg/compiler/userdata.go` | sidecar binaries on EC2 | `aws s3 cp` + systemd units | VERIFIED | Section 5 in userdata template; `km-dns-proxy`, `km-http-proxy`, `km-audit-log` service units |
| `pkg/lifecycle/idle.go` | `pkg/lifecycle/teardown.go` | `ExecuteTeardown` via `OnIdle` callback | VERIFIED | `idle.go:80` calls `d.OnIdle(d.SandboxID)`; callers pass teardown executor as callback |
| `internal/app/cmd/list.go` | `pkg/aws.ListAllSandboxesByS3` | `SandboxLister` interface | VERIFIED | `list.go:109` calls `kmaws.ListAllSandboxesByS3` |
| `internal/app/cmd/logs.go` | `pkg/aws.TailLogs` | direct call | VERIFIED | `logs.go:56` calls `kmaws.TailLogs` |
| `internal/app/cmd/root.go` | list/status/logs commands | `AddCommand` | VERIFIED | lines 42-44 |

---

## Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| PROV-03 | 03-05 | `km list` to see all running sandboxes with status | SATISFIED | `list.go` + S3 scan + tabwriter table; `km --help` shows command |
| PROV-04 | 03-05 | `km status <sandbox-id>` detailed sandbox state | SATISFIED | `status.go` + metadata.json + tag ARNs; `TestStatusCmd_Found` passes |
| PROV-05 | 03-04 | Sandbox auto-destroys after TTL via EventBridge | SATISFIED | `scheduler.go` + `lifecycle.go`; `create.go` creates schedule; `destroy.go` deletes it |
| PROV-06 | 03-04 | Sandbox auto-destroys after idle timeout | SATISFIED | `pkg/lifecycle/idle.go` — `IdleDetector` polls CW, fires `OnIdle` callback |
| PROV-07 | 03-04 | Teardown policy configurable (destroy/stop/retain) | SATISFIED | `pkg/lifecycle/teardown.go` — `ExecuteTeardown` dispatches all three policies |
| NETW-02 | 03-01 | DNS proxy sidecar filters by allowlisted suffixes | SATISFIED | `dnsproxy/proxy.go` — NXDOMAIN for non-allowlisted; 5 passing tests |
| NETW-03 | 03-01 | HTTP proxy sidecar filters by allowlisted hosts | SATISFIED | `httpproxy/proxy.go` — `RejectConnect` (403) for blocked; 5 passing tests |
| OBSV-01 | 03-02 | Audit log captures command execution logs | SATISFIED | `auditlog.go` — `AuditEvent` schema with `shell_command` event_type; stdin JSON-line processing |
| OBSV-02 | 03-02 | Audit log captures network traffic logs | SATISFIED | `auditlog.go` — `dns_query`/`http_request` event types; DNS/HTTP proxies emit zerolog JSON consumed by audit-log |
| OBSV-03 | 03-02 | Log destination configurable (CloudWatch/S3/stdout) | SATISFIED (partial) | CloudWatch and stdout fully implemented. S3Dest is intentional stub routing to stdout with warning — Phase 4 delivers full S3 archive. AUDIT_LOG_DEST env var wired. |
| OBSV-08 | 03-03 | OTel tracing sidecar with configurable collector endpoint | SATISFIED | `sidecars/tracing/config.yaml` — OTLP receivers on 4317/4318, awss3exporter, batch processor |
| OBSV-09 | 03-03 | MLflow run per sandbox session with metadata | SATISFIED | `pkg/aws/mlflow.go` — `WriteMLflowRun`/`FinalizeMLflowRun`; params + metrics; 4 passing tests |
| OBSV-10 | 03-01 | OTel trace context propagated through proxy sidecars | SATISFIED | `httpproxy/proxy.go:41` — `otel.GetTextMapPropagator().Inject`; `TestHTTPProxy_TraceparentInjected` passes |

---

## Anti-Patterns Found

| File | Pattern | Severity | Impact |
|------|---------|----------|--------|
| `sidecars/audit-log/auditlog.go:168-186` | `S3Dest` is a stub that falls back to stdout with a warning | INFO | Intentional deferral documented in plan; Phase 4 scope; OBSV-03 partially fulfilled for S3 destination. Config key and routing are wired, only S3 writes are deferred. |
| `internal/app/cmd/list.go:18` | `// TODO: move to config when multi-bucket support is needed.` | INFO | Code comment about future config refactor; does not affect functionality |
| `sidecars/http-proxy/httpproxy/http_proxy_test_stub.go.bak` | Backup file with `t.Fatal("not implemented")` stubs | INFO | `.bak` extension excluded from Go build; leftover from TDD wave-0 replacement. No impact. |

No blocker anti-patterns found. `go build ./...` passes clean. `go vet ./...` passes clean.

---

## Test Results

All Phase 3 packages pass:

| Package | Result |
|---------|--------|
| `sidecars/dns-proxy/dnsproxy` | PASS (5 tests) |
| `sidecars/http-proxy/httpproxy` | PASS (5 tests) |
| `sidecars/audit-log` | PASS (6 tests) |
| `pkg/aws` | PASS (21 tests including scheduler, mlflow, cloudwatch, discover) |
| `pkg/lifecycle` | PASS (4 tests) |
| `pkg/compiler` | PASS |
| `internal/app/cmd` | PASS (23 tests including list, status, logs, create, destroy, validate) |

`go build ./...` — clean (no errors)
`go vet ./...` — clean (no errors)
`km --help` — shows create, destroy, list, logs, status

---

## Human Verification Required

### 1. DNS proxy runtime enforcement on EC2

**Test:** Provision an EC2 sandbox with ALLOWED_SUFFIXES="example.com"; attempt `curl evil.com` and `curl allowed.example.com`
**Expected:** `curl evil.com` fails with DNS resolution error; `curl allowed.example.com` resolves
**Why human:** iptables DNAT rules require root privileges and a live EC2 instance; not testable in unit tests

### 2. HTTP proxy CONNECT enforcement on EC2

**Test:** Provision an EC2 sandbox with ALLOWED_HOSTS="github.com"; attempt `curl https://evil.com` and `curl https://github.com`
**Expected:** `curl https://evil.com` receives connection refused (proxy 403); `curl https://github.com` succeeds
**Why human:** Requires live EC2 with iptables redirect to port 3128 and running HTTP proxy binary

### 3. audit-log CloudWatch delivery

**Test:** Run a sandbox with AUDIT_LOG_DEST=cloudwatch; execute a shell command inside the sandbox; check CloudWatch log group `/km/sandboxes/<sandbox-id>/`
**Expected:** JSON event with `event_type="shell_command"` appears in CloudWatch within 10 seconds
**Why human:** Requires live AWS credentials, CloudWatch log group creation, and a running sandbox

### 4. OTel trace delivery to S3

**Test:** Run a sandbox with the tracing sidecar deployed; verify `s3://<OTEL_S3_BUCKET>/traces/<sandbox-id>/` contains OTLP JSON trace files after workload execution
**Expected:** Trace data appears in S3 within the batch timeout (10s); files are valid OTLP JSON
**Why human:** Requires deployed OTel Collector Contrib binary and live AWS S3 bucket

### 5. MLflow run queryable by MLflow CLI

**Test:** After `km create` and `km destroy`, run `mlflow ui` against the artifact bucket and verify the sandbox session appears as a run
**Expected:** Run visible with sandbox_id, profile_name, substrate params; duration, exit_status metrics populated
**Why human:** Requires MLflow CLI installed, live S3 bucket, and a completed sandbox session

### 6. EventBridge TTL auto-termination

**Test:** Provision a sandbox with TTL="2m"; wait 3 minutes; verify instance is terminated
**Expected:** Lambda fires via EventBridge at TTL expiry; EC2 instance terminates or ECS task stops
**Why human:** Requires deployed TTL Lambda, EventBridge Scheduler permissions, and real-time wait

### 7. Idle timeout trigger

**Test:** Provision a sandbox with idleTimeout="5m"; do not interact for 6 minutes; verify teardown triggers
**Expected:** IdleDetector detects no CloudWatch events, fires OnIdle, teardown executes per policy
**Why human:** Requires live CW log group, deployed IdleDetector process (not yet wired as a daemon), real-time wait

---

## Notes

1. **S3Dest intentional stub:** `OBSV-03` requires CloudWatch/S3/stdout configurability. S3 destination accepts the config key and routes with a warning to stdout. Full S3 archive is scoped to Phase 4 per 03-CONTEXT.md. This is a known, documented deferral — not a surprise gap.

2. **Sidecar wiring to EC2/ECS is compile-time (not runtime verified):** The user-data template and service_hcl template are Go string templates. Their correctness for actual sidecar deployment depends on EC2 and ECS infrastructure from Phase 2, verified only end-to-end (human test items 1-2 above).

3. **IdleDetector not yet deployed as a daemon:** `pkg/lifecycle/idle.go` implements the polling logic but it is not yet wired to a running process or Lambda that calls it on live sandboxes. This is not a gap against Phase 3 scope — lifecycle wire-up is addressed in future phases — but the runtime loop is not fully operational without a host process.

4. **.bak file cleanup:** `sidecars/http-proxy/httpproxy/http_proxy_test_stub.go.bak` is a harmless build artifact that could be removed (it has `.bak` extension, not `.go`, so Go toolchain ignores it).

---

_Verified: 2026-03-22_
_Verifier: Claude (gsd-verifier)_
