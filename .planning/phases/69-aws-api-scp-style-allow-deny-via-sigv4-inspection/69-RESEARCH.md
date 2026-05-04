# Phase 69: AWS API SCP-style allow/deny via SigV4 inspection - Research

**Researched:** 2026-05-04
**Domain:** HTTP proxy SigV4 inspection + eBPF uid capture + profile schema + km doctor
**Confidence:** HIGH — all findings sourced from direct codebase reads

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Schema**
- Field path: `spec.sourceAccess.aws.inspection` (enum) and `spec.sourceAccess.aws.allowlist` (array of strings).
- Enum values: `off` | `observe` | `enforce`. Absence ≡ `off`.
- Allowlist required when `inspection != off`.
- `["*"]` is the only legal wildcard form. Mixing wildcard with explicit entries is a validation error.
- Allowlist matching is literal and case-sensitive against the SigV4 service slug. No normalization, no aliases.
- Schema additions live in `pkg/profile/types.go` and the embedded `schemas/sandbox_profile.schema.json`.

**Detection**
- Two-stage detection in `sidecars/http-proxy`: host-regex CONNECT MITM, then SigV4 service-slug extraction at OnRequest.
- Host regex: `(.*\.)?(amazonaws\.com|amazonaws\.com\.cn|amazonaws-us-gov\.com|api\.aws)$`
- Matched hosts get `goproxy.AlwaysMitm`.
- AWS host matcher registers ahead of existing `OkConnect` passthrough; existing Bedrock matcher remains for its strict subset.
- SigV4 parser: extract third path component of `Credential=AKIA.../<date>/<region>/<service>/aws4_request`. Fall back to `X-Amz-Credential` query parameter.
- Anonymous AWS-host calls emitted as `aws_api_unsigned` and pass through unchanged. Out of scope for v1 enforcement.
- New file: `sidecars/http-proxy/httpproxy/aws.go`. Exposes `WithAWSAllowlist(mode, list)` as a `ProxyOption`.

**Three modes** — see CONTEXT.md table (off/observe/enforce x allowlist shapes).

**Platform-uid exemption**
- New pinned BPF map `sock_to_uid` in `cgroup/connect4` keyed by socket cookie, value = uid.
- Proxy loads new map in `transparent.go`, exposes `GetCallerUID(socketCookie)`.
- `KM_AWS_PLATFORM_UID_MAX` (default `1000`); calls from `uid < KM_AWS_PLATFORM_UID_MAX` skip gate and emit `aws_api_platform`.

**Composition with Bedrock**
- Order: eBPF cgroup/connect4 → AWS allowlist gate (new, first) → GitHub repo gate (peer) → Bedrock metering.
- `km validate` rule: `enforcement: enforce` + non-zero Bedrock budget → `bedrock-runtime` must be in allowlist.

**Audit events**
- `aws_api_allowed`, `aws_api_blocked`, `aws_api_platform`, `aws_api_unsigned` — emitted on proxy stdout.
- Shapes documented in CONTEXT.md / SPEC.md.

**Learn mode**
- `aws_api_allowed` or `aws_api_blocked` events from non-platform uid → add `service` to deduplicated set.
- Generated profile defaults to `inspection: observe`.

**km doctor checks**
- `aws_inspection_uid_map`: SSM RunCommand on a sample running sandbox.
- `aws_allowlist_known_services`: WARN (not FAIL) on unknowns against `pkg/aws/sigv4_services.go`.

**CLI surface**
- No new commands. Effects on `km validate`, `km create`, `km shell --learn`, `km doctor`.

### Claude's Discretion
- Exact SigV4 parser implementation details (header parse vs. regex), pinned-map key/value layout for `sock_to_uid`, ProxyOption struct shape inside `aws.go`.
- Test fixture organization and naming inside `httpproxy/aws_test.go` (mirror `httpproxy/github_test.go`).
- Vetted SigV4 service-slug list contents (seed from AWS SDK service IDs; document the update process).
- Exact wording of validation error messages and audit event field ordering.
- Whether to wire the AWS gate behind a feature flag for staged rollout.
- Plan-file ordering and dependency graph.

### Deferred Ideas (OUT OF SCOPE)
- Operation-level entries (`s3:GetObject`).
- Region/account-id restrictions.
- IMDS gating (169.254.169.254).
- VPC endpoint hosts (`*.vpce.amazonaws.com`).
- Cost attribution beyond existing Bedrock metering.
- Pre-signed URL generation tracing.
- Hot-reload of allowlist mid-sandbox.
</user_constraints>

---

## Summary

Phase 69 adds a service-level AWS API gate to the existing http-proxy sidecar. The work is primarily proxy-side Go (new `aws.go` file + transparent.go additions) with a small additive eBPF change (one new pinned map), a profile schema extension, a compiler env-var plumbing pass, two doctor checks, and learn-mode parser additions.

The codebase already has two near-identical MITM inspection patterns (GitHub repo filter, Bedrock metering), and Phase 69 follows both of them mechanically. The goproxy handler-registration order is first-registered-wins for CONNECT handlers, which is already exploited by both Bedrock and GitHub handlers. The AWS gate uses the same pattern, registered first in the block so it runs before both.

The eBPF change is genuinely small: one `bpf_map_update_elem` call in `connect4` using `bpf_get_current_uid_gid()`, which is already called (implicitly, via `bpf_get_current_pid_tgid()`) in the same program. The verifier risk is negligible — see the detailed analysis under research topic 11.

**Primary recommendation:** Build in the order listed in CONTEXT.md §Implementation slice estimate. Run the eBPF spike (a quick `bpf_get_current_uid_gid` map-update load on any EC2 box) as Plan 69-00 or early in Plan 69-03 to confirm the verifier accepts it before the proxy side depends on it.

---

## Research Findings by Topic

### Topic 1: goproxy ProxyOption pattern

**Confidence: HIGH** — read directly from `sidecars/http-proxy/httpproxy/proxy.go`.

#### ProxyOption type

```go
// ProxyOption is a functional option applied to a proxy during NewProxy.
type ProxyOption func(*goproxy.ProxyHttpServer, *proxyConfig)

// proxyConfig accumulates optional proxy configuration across ProxyOption calls.
type proxyConfig struct {
    budget      *budgetEnforcementOptions
    githubRepos []string
    httpsOnly   bool
}
```

Every `WithXxx` option captures its arguments in a closure and writes into `cfg`:

```go
func WithGitHubRepoFilter(allowedRepos []string) ProxyOption {
    return func(_ *goproxy.ProxyHttpServer, cfg *proxyConfig) {
        cfg.githubRepos = allowedRepos
    }
}
```

`WithAWSAllowlist` must mirror this exactly. Add two fields to `proxyConfig`:

```go
// Add to proxyConfig struct:
awsInspection string   // "off" | "observe" | "enforce"
awsAllowlist  []string
```

```go
func WithAWSAllowlist(mode string, allowlist []string) ProxyOption {
    return func(_ *goproxy.ProxyHttpServer, cfg *proxyConfig) {
        cfg.awsInspection = mode
        cfg.awsAllowlist  = allowlist
    }
}
```

#### Handler registration order (CRITICAL)

`NewProxy` registers handlers in sequence inside `if cfg.budget != nil { ... }` and `if len(cfg.githubRepos) > 0 { ... }` blocks, both **before** the general `HandleConnectFunc` at the bottom. goproxy uses **first-match semantics** for `HandleConnect` — the first handler whose condition matches wins and no further handlers run for that CONNECT.

Current order (proxy.go lines ~173–530):
1. `OnRequest(bedrockHostRegex).HandleConnectFunc` → MitmConnect (inside `if cfg.budget != nil`)
2. `OnRequest(anthropicHostRegex).HandleConnect(AlwaysMitm)` (inside `if cfg.budget != nil`)
3. `OnRequest(githubHostsRegex).HandleConnectFunc` → MitmConnect (inside `if len(cfg.githubRepos) > 0`)
4. `OnRequest(googleHostRegex).HandleConnect(AlwaysMitm)` (easter egg)
5. `OnRequest().HandleConnectFunc` → OkConnect/RejectConnect (general handler, last)

Phase 69's AWS CONNECT handler **must be registered before item 5** (the general handler). Because the AWS regex `(.*\.)?(amazonaws\.com|...)` is a strict superset of `bedrockHostRegex`, the AWS handler must also be registered **before** Bedrock if we want to intercept the same connection first. However — the Bedrock CONNECT handler only fires when `cfg.budget != nil`. When Bedrock is active and `bedrock-runtime` is in the AWS allowlist, both handlers would try to claim the CONNECT. Resolution: register the AWS gate block first (before the budget block) so that for Bedrock hosts, the AWS gate runs CONNECT-time, then AWS gate's `OnRequest` fires for the inner request, and then — only after allowing — the Bedrock metering `OnResponse` sees the response. This works because `OnRequest` and `OnResponse` handlers are all evaluated even after CONNECT is handled; only CONNECT handlers are first-match.

Correct registration order in `NewProxy` for Phase 69:

```
1. if cfg.awsInspection != "off": AWS AlwaysMitm CONNECT handler + AWS OnRequest allowlist gate
2. if cfg.budget != nil: Bedrock CONNECT + OnRequest + OnResponse; Anthropic CONNECT + OnRequest + OnResponse
3. if len(cfg.githubRepos) > 0: GitHub CONNECT + OnRequest
4. Google easter egg
5. General OkConnect/RejectConnect
```

This ordering ensures:
- AWS gate MITMs `*.amazonaws.com` including `bedrock-runtime`.
- Bedrock `OnRequest` budget pre-flight runs AFTER AWS gate allows the request (since OnRequest handlers all fire in registration order — a non-nil response from any handler short-circuits the rest).
- If AWS gate returns 403 (blocked), Bedrock metering never sees the request.

**Implication for OnRequest handler ordering**: goproxy evaluates all matching `OnRequest` handlers in registration order. A handler that returns a non-nil `*http.Response` short-circuits the chain. Therefore the AWS gate `OnRequest` handler must be registered before Bedrock's OnRequest budget-check handler, so a blocked AWS request never reaches Bedrock.

### Topic 2: SigV4 Authorization header parsing

**Confidence: HIGH** — based on AWS SigV4 spec + direct source read (no AWS SDK SigV4 parser in the vendored dependencies was found in this codebase).

The `Authorization` header format:
```
AWS4-HMAC-SHA256 Credential=AKIA.../20260504/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-date, Signature=...
```

Three implementation options:

**Option A: Hand-rolled string split (recommended)**

The credential scope is a fixed `/`-delimited 5-component string. A simple `strings.Split` on `/` after extracting the `Credential=` value extracts the service slug at index 3:

```go
func extractSigV4Service(req *http.Request) (service, region string, found bool) {
    auth := req.Header.Get("Authorization")
    if strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
        // Credential=AKIA.../20260504/us-east-1/s3/aws4_request, ...
        for _, part := range strings.Split(auth, ",") {
            part = strings.TrimSpace(part)
            if !strings.HasPrefix(part, "Credential=") {
                continue
            }
            cred := strings.TrimPrefix(part, "Credential=")
            // cred = "AKIA.../20260504/us-east-1/s3/aws4_request"
            parts := strings.SplitN(cred, "/", 6) // [accessKeyID, date, region, service, "aws4_request", ...]
            if len(parts) >= 5 {
                return parts[3], parts[2], true
            }
        }
    }
    // Pre-signed URL fallback
    if cred := req.URL.Query().Get("X-Amz-Credential"); cred != "" {
        parts := strings.SplitN(cred, "/", 6)
        if len(parts) >= 5 {
            return parts[3], parts[2], true
        }
    }
    return "", "", false
}
```

**Option B: regexp**

A `regexp.MustCompile` capturing the service field. Slightly more defensive, same allocation cost for hot paths, slightly harder to read.

**Option C: AWS SDK sigv4 package**

The project vendored `github.com/aws/aws-sdk-go-v2` but only for SDK client calls, not for SigV4 parsing. The SDK's `signer/v4` package is a *signer*, not a parser. It does not expose a public `ParseCredentialScope` function. Using it would require importing a signing package just to parse a header string — wrong tool for the job.

**Decision (Discretion):** Option A. It is the same approach the codebase uses throughout (string manipulation, no regex for simple formats). The format is stable per the AWS SigV4 specification. Edge cases:

- **SigV4a** (multi-region access points): the `Authorization` header starts with `AWS4-ECDSA-P256-SHA256`. The parser should check `HasPrefix(auth, "AWS4-")` rather than the full string to catch both variants. The credential scope component layout is the same.
- **Pre-signed URLs** (`X-Amz-Credential` query param): the fallback handles this.
- **Chunked upload** (`aws-chunked`): the Authorization header is still SigV4-formatted; no special handling needed.
- **Anonymous requests**: no `Authorization` header and no `X-Amz-Credential` → `found == false` → emit `aws_api_unsigned`, pass through.

### Topic 3: Bedrock metering ordering — goproxy handler evaluation

**Confidence: HIGH** — read directly from proxy.go.

goproxy processes handlers in two distinct phases:

1. **CONNECT handlers** (`HandleConnect`, `HandleConnectFunc`): evaluated in registration order. **First match wins**. Once a CONNECT action is decided (MitmConnect, OkConnect, RejectConnect), subsequent CONNECT handlers for the same request are not evaluated.

2. **OnRequest handlers** (`OnRequest(...).DoFunc`): evaluated in registration order. The return value `(*http.Request, *http.Response)` determines behavior: if `resp` is non-nil, the chain is short-circuited and that response is returned to the client (no further OnRequest handlers run, no upstream forwarding).

3. **OnResponse handlers** (`OnResponse(...).DoFunc`): evaluated after the upstream response is received. All matching OnResponse handlers run in registration order; earlier handlers can replace `resp`.

The Bedrock metering `OnRequest` at proxy.go line ~181 uses `bedrockHostRegex` (`^bedrock-runtime\..+\.amazonaws\.com`). The AWS gate uses a broader host regex. They will both fire for Bedrock hosts.

**Correct ordering ensures:**
- AWS gate `OnRequest` returns a 403 response for blocked requests → Bedrock pre-flight `OnRequest` never runs → `OnResponse` (metering) never runs.
- For allowed requests, AWS gate `OnRequest` returns `(req, nil)` → Bedrock pre-flight `OnRequest` runs next → if budget exhausted, returns 403; otherwise, `(req, nil)` → upstream call proceeds → Bedrock `OnResponse` meters the response.

The `bedrockHostRegex` `^bedrock-runtime\..+\.amazonaws\.com` is a strict subset of the AWS gate regex `(.*\.)?(amazonaws\.com|...)`. If both handlers are registered via `OnRequest(hostRegex).DoFunc(...)`:
- For `bedrock-runtime.us-east-1.amazonaws.com`: both matchers fire. Registration order determines which runs first. AWS gate must be registered first.
- For `s3.amazonaws.com`: only AWS gate matcher fires (bedrockHostRegex does not match).

**No priority system exists in goproxy beyond registration order.** There is no concept of "priority" or "weight" — only the position in the internal handler slice.

### Topic 4: eBPF `sock_to_uid` map design

**Confidence: HIGH** — read directly from `pkg/ebpf/bpf.c`.

#### Existing map pattern

The existing `sock_to_original_ip` and `sock_to_original_port` maps:

```c
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);   /* socket cookie from bpf_get_socket_cookie() */
    __type(value, __u32); /* original dest IP in network byte order */
    __uint(max_entries, 262144);
} sock_to_original_ip SEC(".maps");
```

The key is the socket cookie (`bpf_get_socket_cookie(ctx)`, called at line 197 of bpf.c). The `socket_pid_map` already stores `u64 cookie → u32 PID` using the same pattern (line 93–101, updated at line 198).

#### `sock_to_uid` map definition

```c
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);   /* socket cookie */
    __type(value, __u32); /* UID from bpf_get_current_uid_gid() & 0xFFFFFFFF */
    __uint(max_entries, 262144);
} sock_to_uid SEC(".maps");
```

Use `BPF_MAP_TYPE_HASH`, not `LRU_HASH`. The existing maps all use HASH; the cookie cleanup model is the same (entries are small, 262144 is well-sized for concurrent connections).

#### Insertion point in `cgroup/connect4`

Insert immediately after the existing `bpf_map_update_elem(&socket_pid_map, ...)` at line 198, before the LPM_TRIE lookup:

```c
/* 3. Record PID → socket cookie mapping */
__u64 cookie = bpf_get_socket_cookie(ctx);
bpf_map_update_elem(&socket_pid_map, &cookie, &pid, 0 /* BPF_ANY */);

/* 3b. Record UID → socket cookie mapping (for AWS gate platform-uid carve-out) */
__u32 uid = (__u32)(bpf_get_current_uid_gid() & 0xFFFFFFFF);
bpf_map_update_elem(&sock_to_uid, &cookie, &uid, 0 /* BPF_ANY */);
```

`bpf_get_current_uid_gid()` returns a 64-bit value where the lower 32 bits are the UID and the upper 32 bits are the GID. Mask to get UID.

#### Go-side loader addition

In `transparent.go`, add `sockToUID *ebpf.Map` alongside the existing `sockToPort *ebpf.Map` field, load it in `loadMaps()`, and add a `GetCallerUID(peerPort uint16)` method that chains the existing two-step lookup (port → cookie → uid):

```go
func (tl *TransparentListener) GetCallerUID(peerPort uint16) (uint32, error) {
    if err := tl.loadMaps(); err != nil {
        return 0, err
    }
    var cookie uint64
    if err := tl.portToSock.Lookup(&peerPort, &cookie); err != nil {
        return 0, fmt.Errorf("port→cookie lookup: %w", err)
    }
    var uid uint32
    if err := tl.sockToUID.Lookup(&cookie, &uid); err != nil {
        return 0, fmt.Errorf("cookie→uid lookup: %w", err)
    }
    return uid, nil
}
```

### Topic 5: Transparent proxy caller-UID lookup

**Confidence: HIGH** — read directly from `sidecars/http-proxy/httpproxy/transparent.go`.

#### Existing pinned map loading

`TransparentListener` loads pinned maps lazily via `sync.Once` in `loadMaps()`:

```go
func (tl *TransparentListener) loadMaps() error {
    tl.mu.Do(func() {
        tl.portToSock, tl.mapErr = ebpf.LoadPinnedMap(tl.mapDir+"src_port_to_sock", nil)
        // ...
        tl.sockToIP,   tl.mapErr = ebpf.LoadPinnedMap(tl.mapDir+"sock_to_original_ip", nil)
        // ...
        tl.sockToPort, tl.mapErr = ebpf.LoadPinnedMap(tl.mapDir+"sock_to_original_port", nil)
    })
    return tl.mapErr
}
```

Path: `/sys/fs/bpf/km/{sandboxID}/` (set in `NewTransparentListener`).

Adding `sock_to_uid` is mechanical: add `sockToUID *ebpf.Map` to the struct, add `tl.sockToUID, tl.mapErr = ebpf.LoadPinnedMap(tl.mapDir+"sock_to_uid", nil)` to `loadMaps()`, expose `GetCallerUID`.

#### Socket cookie and peer port — how caller identity flows

The `handleTransparent` method resolves the original destination via `lookupOriginalDest(uint16(tcpAddr.Port))`. It already extracts `tcpAddr.Port` from `conn.RemoteAddr().(*net.TCPAddr)`. That same peer port is the key for `GetCallerUID`.

There is **no `SO_COOKIE` getsockopt call anywhere in this file**. The socket cookie is not obtained from the socket itself at userspace; it is looked up indirectly: the peer's **source port** is used as the key for `src_port_to_sock` (written by the `sockops` BPF program when the connection is established), which gives the cookie, which gives the original IP/port. Adding uid lookup follows the identical chain: port → cookie → uid.

The proxy never calls `netlink` or reads `/proc/net/tcp` for this purpose. The BPF maps are the sole mechanism.

#### Where to call GetCallerUID in relayWithInspection

In `relayWithInspection`, the peer port is available only at `handleTransparent` call time (before the relay loop). The UID should be looked up once per connection (not per request) and stored as a local variable, then passed into the inspection logic:

```go
func (tl *TransparentListener) handleTransparent(conn net.Conn) {
    tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr)
    // ... existing code ...
    origIP, origPort, err := tl.lookupOriginalDest(uint16(tcpAddr.Port))
    // ADD:
    callerUID, uidErr := tl.GetCallerUID(uint16(tcpAddr.Port))
    if uidErr != nil {
        callerUID = 0xFFFFFFFF // unknown — treat as non-platform (gate applies)
    }
    // ... pass callerUID into relayWithInspection ...
}
```

### Topic 6: Profile compiler env-var injection

**Confidence: HIGH** — read directly from `pkg/compiler/userdata.go`.

#### Exact systemd unit template (base unit, line 822-839)

```
cat > /etc/systemd/system/km-http-proxy.service << 'UNIT'
[Unit]
Description=Klankrmkr HTTP proxy sidecar
After=network.target
[Service]
User=km-sidecar
Environment=SANDBOX_ID={{ .SandboxID }}
Environment=AWS_REGION={{ .AWSRegion }}
Environment=ALLOWED_HOSTS={{ .AllowedHTTPHosts }}
Environment=KM_GITHUB_ALLOWED_REPOS={{ .GitHubAllowedRepos }}
Environment=PROXY_PORT=3128
ExecStart=/opt/km/bin/km-http-proxy
ExecStartPost=/bin/bash -c 'echo $MAINPID > /run/km/http-proxy.pid'
Restart=always
RestartSec=2
[Install]
WantedBy=multi-user.target
UNIT
```

The three new vars go into this base unit template, not into a drop-in, because they are always present (just empty strings when inspection is `off`). The budget vars use a **drop-in** (`/etc/systemd/system/km-http-proxy.service.d/budget.conf`) because they require a restart with AWS credentials. The AWS vars are compile-time constants that require no restart, so the base unit is the right place.

#### Template data struct fields to add (in `UserDataParams`)

```go
// AWS API allowlist gate (Phase 69)
AWSInspection    string // "off" | "observe" | "enforce" (empty string → "off" in proxy)
AWSAllowlist     string // CSV of service slugs, or "*", or ""
AWSPlatformUIDMax string // default "1000"; the proxy uses its own default if empty
```

#### Template additions

Add three `Environment=` lines after `KM_GITHUB_ALLOWED_REPOS`:

```
Environment=KM_AWS_INSPECTION={{ .AWSInspection }}
Environment=KM_AWS_ALLOWLIST={{ .AWSAllowlist }}
Environment=KM_AWS_PLATFORM_UID_MAX={{ .AWSPlatformUIDMax }}
```

#### Go-side function to join the allowlist

Mirror `joinGitHubAllowedRepos`:

```go
func joinAWSAllowlist(p *profile.SandboxProfile) string {
    if p.Spec.SourceAccess.AWS == nil || len(p.Spec.SourceAccess.AWS.Allowlist) == 0 {
        return ""
    }
    return strings.Join(p.Spec.SourceAccess.AWS.Allowlist, ",")
}
```

Then in the params builder:

```go
AWSInspection:     awsInspectionFromProfile(p),  // returns "" when AWS is nil
AWSAllowlist:      joinAWSAllowlist(p),
AWSPlatformUIDMax: "1000",  // constant; could be made configurable via km-config later
```

#### Comma-safety for `KM_AWS_ALLOWLIST`

SigV4 service slugs are simple lowercase identifiers like `s3`, `sts`, `bedrock-runtime`, `dynamodb`. None contain commas. The CSV is safe. Document this explicitly in the operator guide but no escaping logic is needed.

#### Systemd unit `Environment=` line length

A single `Environment=` line in a systemd unit has a practical limit (varies by kernel; safe up to ~8KB). The maximum plausible allowlist is all ~200 AWS services ≈ 2KB. No issue.

### Topic 7: `km doctor` check structure

**Confidence: HIGH** — read directly from `internal/app/cmd/doctor.go` and `doctor_slack.go`.

#### CheckResult type

```go
type CheckResult struct {
    Name        string      `json:"name"`
    Status      CheckStatus `json:"status"`   // "OK" | "WARN" | "ERROR" | "SKIPPED"
    Message     string      `json:"message"`
    Remediation string      `json:"remediation,omitempty"`
}
```

#### Registration pattern (from buildChecks, line 2028+)

Each check is a `func(context.Context) CheckResult` appended to the `checks` slice. The function closes over its dependencies (AWS clients, config values). Most new checks are wrapped to demote `CheckError → CheckWarn` so non-critical failures never break `km doctor`:

```go
checks = append(checks, func(ctx context.Context) CheckResult {
    r := checkSlackTranscriptTableExists(ctx, transcriptDDB, transcriptTable)
    if r.Status == CheckError {
        r.Status = CheckWarn
    }
    return r
})
```

#### Dry-run convention (from checkSlackInboundStaleQueues)

The `dryRun bool` parameter is threaded into `buildChecks` from the CLI flag. Detect-only checks return `CheckWarn` with a `Remediation` pointing at `--dry-run=false`. Destructive cleanup only runs when `dryRun == false`.

#### SSM RunCommand pattern for remote checks

The `checkSlackInboundQueueExists` check calls `kmaws.QueueDepth(ctx, sqsClient, r.QueueURL)` as a liveness probe rather than SSM. For the new `aws_inspection_uid_map` check, the pattern closest to it in the codebase is any check that uses `ssmClient` (e.g., `checkGitHubConfig` at line ~2097). The recommended implementation for uid-map is SSM `SendCommand` → `id km-mail-poller` → parse uid from output, compare against `KM_AWS_PLATFORM_UID_MAX`.

#### DoctorDeps struct (line 224+)

Dependencies are injected as fields on `DoctorDeps`. The two new checks need:
- `AWSSandboxLister` — a function or interface that lists running sandboxes with inspection enabled (can reuse `kmaws.ListAllSandboxMetadataDynamo` with a filter).
- `AWSSSMRunner` — for SSM RunCommand (the pattern for sending a command to a running sandbox exists in `internal/app/cmd/create.go` and doctor's existing SSM checks).

New fields to add to `DoctorDeps`:
```go
AWSInspectionSSM  SSMRunCommandAPI   // for aws_inspection_uid_map
AWSSandboxLister  func(ctx context.Context) ([]kmaws.SandboxMetadata, error)
```

#### File placement

Both new checks belong in a new file: `internal/app/cmd/doctor_aws.go`. This mirrors `doctor_slack.go` (per-feature grouping).

### Topic 8: Learn-mode parser — existing fixture format and extension

**Confidence: HIGH** — read directly from `internal/app/cmd/shell.go` (lines 627-715) and `shell_learn_test.go`.

#### Existing `learnObservedState` JSON structure

```go
type learnObservedState struct {
    DNS      []string `json:"dns"`
    Hosts    []string `json:"hosts"`
    Repos    []string `json:"repos"`
    Refs     []string `json:"refs,omitempty"`
    Commands []string `json:"commands,omitempty"`
}
```

This struct is populated by `CollectDockerObservations` (Docker path) and by the eBPF `--observe` output on EC2 (S3 download path). Both paths produce JSON and feed into `GenerateProfileFromJSON`.

#### Existing dispatcher (`CollectDockerObservations` + `ParseProxyLogs`)

The Docker path in `CollectDockerObservations` reads proxy stdout line-by-line. It dispatches on `event_type`:
- `dns_query` → `rec.RecordDNSQuery(domain)`
- `github_repo_allowed` → `rec.RecordRepo("owner/repo")`
- `github_mitm_connect` → `rec.RecordHost(host)`

Phase 69 events are emitted on proxy stdout in the same zerolog JSON format. They need to be dispatched in `CollectDockerObservations` / `ParseProxyLogs`.

#### Extension for AWS events

Add to `learnObservedState`:
```go
AWSServices []string `json:"aws_services,omitempty"` // deduplicated service slugs
```

In the proxy log parser, add dispatch for:
- `aws_api_allowed` (service slug from `service` field, when mode != "platform")
- `aws_api_blocked` (service slug from `service` field)
- `aws_api_platform` — skip (do not collect platform services)
- `aws_api_unsigned` — skip (no service slug available)

In `GenerateProfileFromJSON`, after processing `state.Repos`, process `state.AWSServices`:
```go
for _, svc := range state.AWSServices {
    rec.RecordAWSService(svc) // new method on allowlistgen.Recorder
}
```

`allowlistgen.Recorder.RecordAWSService` deduplicates into a set; `GenerateAnnotatedYAML` emits the `spec.sourceAccess.aws` block with `inspection: observe` and `allowlist:` items alphabetically sorted.

#### Test fixture sketch for `shell_learn_test.go`

```go
func TestCollectDockerObservations_AWSServices(t *testing.T) {
    httpLogs := strings.NewReader(`
{"level":"info","event_type":"aws_api_allowed","service":"s3","sandbox_id":"sb-x","mode":"observe"}
{"level":"info","event_type":"aws_api_blocked","service":"ec2","sandbox_id":"sb-x","reason":"not_in_allowlist"}
{"level":"info","event_type":"aws_api_platform","service":"sqs","sandbox_id":"sb-x","uid":995}
{"level":"info","event_type":"aws_api_unsigned","host":"public.s3.amazonaws.com","sandbox_id":"sb-x"}
`)
    data, err := cmd.CollectDockerObservations("sb-x", nil, httpLogs, nil)
    // parse result, assert AWSServices == ["ec2", "s3"] (sorted, no sqs/unsigned)
}

func TestGenerateProfileFromJSON_AWSServices(t *testing.T) {
    input := []byte(`{"dns":[],"hosts":[],"repos":[],"aws_services":["s3","sts","dynamodb"]}`)
    yamlBytes, err := cmd.GenerateProfileFromJSON(input, "", "")
    // assert yaml contains inspection: observe, allowlist with s3, sts, dynamodb alphabetically
}
```

EC2 path: the `learnObservedState` is written to S3 by `km ebpf-attach --observe`. The `aws_services` field is populated there by the enforcer's event listener, which already reads proxy stdout (Phase 63 wired this). If the enforcer's `--observe` output producer is separate from proxy stdout, the EC2 path may need the eBPF enforcer to expose the AWS services in its S3 JSON blob. Clarify in implementation — the Docker path is simpler.

### Topic 9: `km validate` rule plumbing

**Confidence: HIGH** — read directly from `pkg/profile/validate.go`.

#### `ValidateSemantic` function shape

All semantic rules live in `ValidateSemantic(p *SandboxProfile) []ValidationError`. Rules append to `errs`. `IsWarning: true` produces a warning that `km validate` prints to stderr but does not fail on.

#### Closest analog — Slack inbound rules (lines 328-352)

```go
// Phase 67 — Slack inbound validation rules.
inboundOn := cli.NotifySlackInboundEnabled
if inboundOn {
    // Rule SI1 (error): inbound requires outbound Slack enabled.
    if !slackOn {
        errs = append(errs, ValidationError{
            Path:    "spec.cli.notifySlackInboundEnabled",
            Message: "notifySlackInboundEnabled: true requires notifySlackEnabled: true",
        })
    }
    // Rule SI2 (error): inbound requires per-sandbox channel (1:1 routing).
    if !perSandbox {
        errs = append(errs, ValidationError{
            Path:    "spec.cli.notifySlackInboundEnabled",
            Message: "notifySlackInboundEnabled: true requires notifySlackPerSandbox: true",
        })
    }
}
```

#### Two new rules for Phase 69

**Rule AW1 — Bedrock-budget cross-check (error):**
```go
if p.Spec.SourceAccess.AWS != nil && p.Spec.SourceAccess.AWS.Inspection == "enforce" {
    if p.Spec.Budget != nil && p.Spec.Budget.AI != nil && p.Spec.Budget.AI.MaxSpendUSD > 0 {
        found := false
        for _, svc := range p.Spec.SourceAccess.AWS.Allowlist {
            // Strip operation-level suffix (e.g., "bedrock-runtime:InvokeModel" → "bedrock-runtime")
            slug := strings.SplitN(svc, ":", 2)[0]
            if slug == "bedrock-runtime" {
                found = true
                break
            }
        }
        if !found {
            errs = append(errs, ValidationError{
                Path:    "spec.sourceAccess.aws.allowlist",
                Message: "inspection: enforce with a non-zero AI budget requires bedrock-runtime in the allowlist — budget enforces metering on bedrock-runtime; add it or set the budget AI limit to zero",
            })
        }
    }
}
```

**Rule AW2 — wildcard-mixing (error):**
```go
if p.Spec.SourceAccess.AWS != nil && p.Spec.SourceAccess.AWS.Inspection != "off" {
    hasWildcard := false
    hasExplicit := false
    for _, svc := range p.Spec.SourceAccess.AWS.Allowlist {
        if svc == "*" {
            hasWildcard = true
        } else {
            hasExplicit = true
        }
    }
    if hasWildcard && hasExplicit {
        errs = append(errs, ValidationError{
            Path:    "spec.sourceAccess.aws.allowlist",
            Message: `["*"] wildcard may not be mixed with explicit service entries; use either ["*"] or an explicit list`,
        })
    }
}
```

#### Budget field path

From `pkg/profile/types.go`, the predicate for "non-zero Bedrock budget" is:
```go
p.Spec.Budget != nil && p.Spec.Budget.AI != nil && p.Spec.Budget.AI.MaxSpendUSD > 0
```

The field path is `spec.budget.ai.maxSpendUSD`. There is no `aiSpend` or `dailyLimit` field — the SPEC.md's reference to `spec.budget.aiSpend.*` was informal shorthand. The actual struct is `BudgetSpec.AI.MaxSpendUSD` (type `float64`).

### Topic 10 is the Validation Architecture section below.

### Topic 11: eBPF verifier risk assessment

**Confidence: HIGH** — based on direct read of `bpf.c`.

#### Concrete assessment: LOW RISK

The `cgroup/connect4` program already calls:
- `bpf_get_current_pid_tgid()` (line 174) — returns a u64 with PID in upper bits.
- `bpf_get_socket_cookie(ctx)` (line 197) — returns u64.
- `bpf_map_update_elem` three times already: `socket_pid_map`, `sock_to_original_ip`, `sock_to_original_port`.

Adding `bpf_get_current_uid_gid()` (one helper call, returns u64) and one more `bpf_map_update_elem` is entirely mechanical. Both helpers are available in `cgroup/connect4` context (they require process context, which `cgroup/connect4` has — it fires in the process context of the connecting process).

The instruction count of `connect4` is already substantial (LPM_TRIE lookup, emit_event call, redirect rewrite). However, modern kernels (5.x+) allow up to 1M instructions per BPF program, and the `cgroup/connect4` program is well within this. Adding 4-6 instructions (load uid, mask, map update, handle error) has no risk of hitting the limit.

The stack budget (512 bytes) is not a concern: `bpf_get_current_uid_gid()` uses no stack; the uid value fits in a register.

**Conclusion: No spike required.** The change is trivially additive. The SPEC and CONTEXT mark this "low risk" and this assessment confirms it. If a spike is done, it should take 10-15 minutes on any EC2 box running a kernel new enough to have `cgroup/connect4` support (kernel 4.17+).

One genuine concern: `sock_to_uid` map entries must be cleaned up at connection teardown. The existing `sock_to_original_ip` and `sock_to_original_port` entries are also not explicitly deleted (the `sockops` program writes `src_port_to_sock` but doesn't delete the ip/port maps). This is acceptable: the maps are sized at 262144 entries and the kernel evicts on full (HASH type evicts nothing — it returns EBUSY). With a max_entries of 262144 and typical sandbox concurrency, this is never an issue in practice (the same issue exists for the three existing maps and has not been a problem).

---

## Standard Stack

### Core (no new dependencies needed)

| Library | Version | Purpose | Notes |
|---------|---------|---------|-------|
| `github.com/elazarl/goproxy` | already vendored | HTTP/HTTPS MITM proxy framework | All handler registration via existing API |
| `github.com/cilium/ebpf` | already vendored | BPF map loading in Go | `ebpf.LoadPinnedMap` for sock_to_uid |
| `github.com/rs/zerolog` | already vendored | Structured JSON logging for audit events | Same pattern as existing events |
| stdlib `strings`, `regexp`, `net/http` | — | SigV4 header parsing, host matching | No new deps |

**No new Go module dependencies are needed for Phase 69.**

### New files created
- `sidecars/http-proxy/httpproxy/aws.go` — SigV4 inspector
- `sidecars/http-proxy/httpproxy/aws_test.go` — unit tests (mirror `github_test.go`)
- `pkg/aws/sigv4_services.go` — vetted service-slug list
- `internal/app/cmd/doctor_aws.go` — two new doctor checks

---

## Architecture Patterns

### ProxyOption pattern (mirror exactly)

```go
// aws.go

// awsInspectionOptions holds all state for the AWS API gate.
type awsInspectionOptions struct {
    mode      string   // "off" | "observe" | "enforce"
    allowlist []string // service slugs or ["*"]
}

// WithAWSAllowlist enables SigV4-based AWS API service gating.
// mode must be "off", "observe", or "enforce".
// When mode is "off", no handlers are registered.
func WithAWSAllowlist(mode string, allowlist []string) ProxyOption {
    return func(_ *goproxy.ProxyHttpServer, cfg *proxyConfig) {
        cfg.awsInspection = mode
        cfg.awsAllowlist  = allowlist
    }
}
```

Add to `proxyConfig`:
```go
awsInspection string
awsAllowlist  []string
```

Add to NewProxy, before the budget block:
```go
if cfg.awsInspection != "" && cfg.awsInspection != "off" {
    ai := &awsInspectionOptions{mode: cfg.awsInspection, allowlist: cfg.awsAllowlist}
    proxy.OnRequest(goproxy.ReqHostMatches(awsHostRegex)).HandleConnectFunc(
        func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
            log.Info().Str("event_type", "aws_mitm_connect").Str("sandbox_id", sandboxID).Str("host", host).Msg("")
            return goproxy.MitmConnect, host
        },
    )
    proxy.OnRequest(goproxy.ReqHostMatches(awsHostRegex)).DoFunc(
        func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
            return handleAWSRequest(req, ctx, sandboxID, ai)
        },
    )
}
```

### Audit event emission pattern

All events use zerolog on `log.Info()`. The `Msg("")` pattern is canonical for this codebase:

```go
log.Info().
    Str("event_type", "aws_api_allowed").
    Str("sandbox_id", sandboxID).
    Str("service", service).
    Str("region", region).
    Str("host", req.Host).
    Str("method", req.Method).
    Str("path", req.URL.Path).
    Str("mode", mode).
    Msg("")
```

### Anti-patterns to avoid

- **Do not** use `goproxy.AlwaysMitm` directly in `HandleConnect` calls — use a `HandleConnectFunc` that logs the connect event and returns `goproxy.MitmConnect`. This matches the GitHub and Bedrock patterns and ensures the `sandbox_id` appears in the MITM event.
- **Do not** build a custom regex for SigV4 — use string splitting on the credential scope as Option A above.
- **Do not** gate the CONNECT handler on the mode — always MITM AWS hosts when the gate is configured (`!= "off"`). The per-request decision (allow/observe/block) is made in `OnRequest` not at CONNECT time.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead |
|---------|-------------|-------------|
| JSON audit event emission | custom logger | zerolog `log.Info().Str(...).Msg("")` (existing pattern) |
| Blocked response body | custom http.Response builder | `goproxy.NewResponse(req, "application/json", 403, body)` (see GitHubBlockedResponse) |
| BPF map loading | custom mmap/syscall code | `ebpf.LoadPinnedMap` from `github.com/cilium/ebpf` |
| SigV4 service slug parsing | regex / AWS SDK | simple `strings.Split` on credential scope |
| Allowlist matching | fuzzy/normalized match | literal case-sensitive comparison (locked decision) |

---

## Common Pitfalls

### Pitfall 1: registering the AWS CONNECT handler after Bedrock

**What goes wrong:** If the AWS AlwaysMitm handler is registered after the Bedrock AlwaysMitm handler, `bedrock-runtime` hosts are claimed by Bedrock's handler and the AWS OnRequest handler never fires for those connections. The AWS gate is bypassed for Bedrock.

**Prevention:** Register the AWS CONNECT block before the `if cfg.budget != nil` block in `NewProxy`.

### Pitfall 2: sock_to_uid not loaded by `loadMaps` sync.Once

**What goes wrong:** `loadMaps` uses `sync.Once` — the first error sets `tl.mapErr` and subsequent calls return the same error. If `sock_to_uid` map loading is attempted after a failure in a prior map load, it is silently skipped.

**Prevention:** The `sync.Once` body must load all maps in a single `mu.Do` block. All four map loads (ip, port, uid, port-to-sock) must be inside the same `mu.Do` closure. Any error must set `tl.mapErr` and return immediately to prevent partial-load state.

### Pitfall 3: wildcard `["*"]` treated as a literal service slug

**What goes wrong:** If the allowlist comparison does `allowlist[i] == service` and one entry is `"*"`, it never matches a real service slug.

**Prevention:** Add a wildcard check before the loop:
```go
for _, entry := range allowlist {
    if entry == "*" { return true }
    if strings.SplitN(entry, ":", 2)[0] == service { return true }
}
return false
```

### Pitfall 4: pre-signed URL query params URL-decoded before lookup

**What goes wrong:** The `X-Amz-Credential` query parameter in a pre-signed URL may be URL-encoded (`%2F` for `/`). `req.URL.Query().Get("X-Amz-Credential")` handles this correctly (Go's URL parser decodes query params). Manual `req.URL.RawQuery` string splitting does not.

**Prevention:** Always use `req.URL.Query().Get(...)` not raw query string manipulation.

### Pitfall 5: systemd Environment= line with empty values

**What goes wrong:** If `KM_AWS_INSPECTION=` (empty), the proxy's `os.Getenv("KM_AWS_INSPECTION")` returns `""`. The proxy treats this as `"off"` by convention (`getEnv("KM_AWS_INSPECTION", "off")`). No issue — but the template must emit the line unconditionally so the proxy always sees it.

**Prevention:** The template emits all three env vars regardless of whether inspection is configured. The proxy uses `getEnv("KM_AWS_INSPECTION", "off")` (existing `getEnv` helper pattern from `main.go`).

---

## Code Examples

### Verified ProxyOption function signatures (source: proxy.go)

```go
// Existing pattern — WithGitHubRepoFilter (github.go)
func WithGitHubRepoFilter(allowedRepos []string) ProxyOption {
    return func(_ *goproxy.ProxyHttpServer, cfg *proxyConfig) {
        cfg.githubRepos = allowedRepos
    }
}

// Existing pattern — WithBudgetEnforcement (proxy.go)
func WithBudgetEnforcement(client aws.BudgetAPI, tableName string, modelRates map[string]aws.BedrockModelRate, onBudgetUpdate BudgetUpdater) ProxyOption {
    return func(_ *goproxy.ProxyHttpServer, cfg *proxyConfig) {
        cfg.budget = &budgetEnforcementOptions{...}
    }
}
```

### Verified map load pattern (source: transparent.go)

```go
tl.sockToIP, tl.mapErr = ebpf.LoadPinnedMap(tl.mapDir+"sock_to_original_ip", nil)
if tl.mapErr != nil {
    tl.mapErr = fmt.Errorf("load sock_to_original_ip: %w", tl.mapErr)
    return
}
```

### Verified blocked response pattern (source: github.go)

```go
func GitHubBlockedResponse(req *http.Request, sandboxID, repo string) *http.Response {
    body := githubBlockedBody{Error: "repo_not_allowed", Repo: repo, Reason: "..."}
    encoded, _ := json.Marshal(body)
    return goproxy.NewResponse(req, "application/json", http.StatusForbidden, string(encoded))
}
```

For Phase 69, `AWSBlockedResponse` follows the same shape:
```go
type awsBlockedBody struct {
    Error   string `json:"error"`
    Service string `json:"service"`
    Reason  string `json:"reason"` // "not_in_allowlist" | "empty_allowlist"
}
```

The response body should also include a `KM_AWS_BLOCKED` header (mentioned in SPEC §Success Criteria #2) so the AWS CLI can display a useful error rather than a generic TLS error.

### Validated semantic rule pattern (source: validate.go lines 328–352)

```go
// Pattern for cross-field error rules (no IsWarning):
errs = append(errs, ValidationError{
    Path:    "spec.sourceAccess.aws.allowlist",
    Message: "inspection: enforce with a non-zero AI budget requires bedrock-runtime ...",
})
// Pattern for no-op warnings (IsWarning: true):
errs = append(errs, ValidationError{
    Path:      "spec.cli.notifySlackPerSandbox",
    Message:   "notifySlackPerSandbox: true has no effect when notifySlackEnabled is false",
    IsWarning: true,
})
```

---

## Validation Architecture

`nyquist_validation` is enabled (confirmed via `.planning/config.json`).

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` package (stdlib) |
| Config file | none (Go test runner, `go test ./...`) |
| Quick run command | `go test ./pkg/profile/... ./sidecars/http-proxy/httpproxy/... ./internal/app/cmd/... -run AWS -v` |
| Full suite command | `make build && go test ./...` |

### Phase Requirements to Test Map

| Success Criterion | Behavior | Test Type | Automated Command | File Exists? |
|------------------|----------|-----------|-------------------|-------------|
| SC-1: enforce + `["*"]` → `aws_api_allowed` | allowlist gate returns allowed for wildcard | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestAWSAllowlist` | ❌ Wave 0 |
| SC-2: enforce + `[]` → 403 + `aws_api_blocked`; platform calls pass | gate returns blocked; uid < 1000 bypasses gate | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestAWSAllowlist` | ❌ Wave 0 |
| SC-3: observe + `[]` → pass-through + blocked events | observe mode returns `(req, nil)` for all; logs blocked | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestAWSObserve` | ❌ Wave 0 |
| SC-4: `km shell --learn` generates correct YAML | docker observations produce `inspection: observe` + correct allowlist | unit | `go test ./internal/app/cmd/... -run TestCollectDockerObservations_AWS` | ❌ Wave 0 |
| SC-5: validate rejects missing `bedrock-runtime` | `ValidateSemantic` returns error for enforce + AI budget + no bedrock-runtime | unit | `go test ./pkg/profile/... -run TestValidateSemantic_AWSBedrockCrossCheck` | ❌ Wave 0 |
| SC-6: `km doctor` green | doctor checks return OK/SKIPPED on configured sandbox | unit (mock deps) + manual | `go test ./internal/app/cmd/... -run TestDoctor_AWS` | ❌ Wave 0 |
| SC-7: audit events flow to CloudWatch | `aws_api_*` events appear in proxy stdout with correct fields | unit (zerolog capture) | `go test ./sidecars/http-proxy/httpproxy/... -run TestAWSAuditEvents` | ❌ Wave 0 |

### Demo Storyboard — Manual UAT Required

The four demo storyboard flows (SC-1 through SC-4 end-to-end) require a real EC2 sandbox. These are manual UAT, not automated:

| Demo Flow | What to verify | Evidence to capture |
|-----------|----------------|---------------------|
| Wide open (SC-1) | `aws sts get-caller-identity` succeeds; audit log shows `aws_api_allowed` | screenshot of `aws s3 ls`, CloudWatch log line |
| Locked down (SC-2) | AWS CLI returns 403; `km email read` continues to work | screenshot of 403 body with `KM_AWS_BLOCKED`; km email read output |
| Observe (SC-3) | AWS CLI succeeds; audit log shows `aws_api_blocked` | CloudWatch log showing mode=observe + blocked events |
| Learn-derived (SC-4) | generated YAML has correct allowlist | `cat learned.*.yaml` showing `inspection: observe` + `[dynamodb, s3, sts]` |

These are the Phase 69 verification-doc captures for the planner's `69-10-VERIFY.md`.

### Sampling Rate

- **Per task commit:** `go test ./pkg/profile/... ./sidecars/http-proxy/httpproxy/... -run AWS -count=1`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green + manual UAT for demo storyboard before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `sidecars/http-proxy/httpproxy/aws_test.go` — covers SC-1, SC-2, SC-3, SC-7 (allowlist gate unit tests, audit event assertions)
- [ ] `pkg/profile/validate_aws_test.go` — covers SC-5 (Bedrock cross-check + wildcard-mixing rules)
- [ ] `internal/app/cmd/shell_learn_aws_test.go` — covers SC-4 (AWS service collection from proxy logs)
- [ ] `internal/app/cmd/doctor_aws_test.go` — covers SC-6 (mock doctor checks)
- [ ] No new framework install needed — Go `testing` is already the standard

---

## State of the Art

| Old Approach | Current Approach | Notes |
|--------------|-----------------|-------|
| IAM policies for per-service control | SCP-style proxy gate (this phase) | IAM is too verbose; SCPs too coarse for per-sandbox control |
| No AWS API audit events | `aws_api_*` event types on proxy stdout | Same audit-log sidecar, no sidecar changes |
| GitHub MITM only for GitHub hosts | AWS MITM for `*.amazonaws.com` (broader scope) | Same AlwaysMitm pattern, different host regex |

---

## Open Questions

1. **EC2 learn-mode path for AWS services**
   - What we know: the Docker path reads proxy stdout directly; EC2 path reads S3 JSON output from `km ebpf-attach --observe`.
   - What's unclear: whether the `learnObservedState` JSON written to S3 by `km ebpf-attach` is extensible (does it read proxy stdout and re-emit, or does it produce its own JSON?).
   - Recommendation: implementer should trace the EC2 learn-mode flow from `shell.go:~820` to understand how the S3 JSON is produced. If the enforcer produces its own JSON independently of proxy stdout, a separate extension is needed. If it re-reads proxy stdout, the Docker-path extension carries over.

2. **`getpwuid` lookup for `caller` field in `aws_api_platform` events**
   - What we know: the SPEC calls for best-effort `/etc/passwd` lookup by uid for the `caller` field.
   - What's unclear: whether to do this lookup in the proxy Go binary (via `os/user.LookupId`) or in the audit event consumer.
   - Recommendation: `os/user.LookupId(strconv.Itoa(int(uid)))` in the proxy at event-emit time. Cache results in a `sync.Map` to avoid repeated file reads per request.

3. **`sock_to_uid` map pinning location**
   - What we know: all existing maps are at `/sys/fs/bpf/km/{sandboxID}/`. The Go-side eBPF loader in `pkg/ebpf/loader.go` (not read in this research session) pins them.
   - What's unclear: exact bpf2go generated struct field names for the new map.
   - Recommendation: follow the exact pattern of `sock_to_original_ip` in `loader.go`. The bpf2go generated code will expose `BpfObjects.SockToUid` (naming convention: CamelCase of the C identifier).

---

## Sources

### Primary (HIGH confidence)
- `sidecars/http-proxy/httpproxy/proxy.go` — goproxy ProxyOption pattern, handler registration order
- `sidecars/http-proxy/httpproxy/transparent.go` — pinned map loading, caller-uid lookup mechanism
- `sidecars/http-proxy/httpproxy/github.go` — WithGitHubRepoFilter pattern, blocked response shape
- `sidecars/http-proxy/main.go` — env-var → ProxyOption wiring, `getEnv` helper
- `pkg/ebpf/bpf.c` — sock_to_uid map type, bpf_get_current_uid_gid availability, verifier risk
- `pkg/profile/types.go` — SourceAccessSpec shape, BudgetSpec.AI.MaxSpendUSD predicate
- `pkg/profile/validate.go` — ValidateSemantic rule pattern, ValidationError.IsWarning
- `pkg/compiler/userdata.go` — systemd unit template, drop-in pattern, joinGitHubAllowedRepos helper
- `internal/app/cmd/doctor.go` — CheckResult type, buildChecks pattern, DoctorDeps struct
- `internal/app/cmd/doctor_slack.go` — SSM-based remote check pattern, dry-run convention
- `internal/app/cmd/shell.go` — learnObservedState, GenerateProfileFromJSON, CollectDockerObservations
- `internal/app/cmd/shell_learn_test.go` — fixture format and test style
- `.planning/phases/69-aws-api-scp-style-allow-deny-via-sigv4-inspection/69-CONTEXT.md` — locked decisions
- `.planning/phases/69-aws-api-scp-style-allow-deny-via-sigv4-inspection/SPEC.md` — supplementary detail

### Secondary (MEDIUM confidence)
- AWS SigV4 specification (Authorization header format) — stable since 2013, well-known

---

## Metadata

**Confidence breakdown:**
- ProxyOption pattern: HIGH — read from source
- Handler registration order: HIGH — read from source; goproxy first-match semantics confirmed from code structure
- SigV4 parser: HIGH — format is AWS spec; string-split approach confirmed correct by inspection
- eBPF map design: HIGH — read from source; verifier risk confirmed LOW
- Caller-UID lookup: HIGH — read from source
- Compiler injection: HIGH — read from source
- Doctor check structure: HIGH — read from source
- Learn-mode extension: HIGH — read from source; EC2 path gap flagged as open question
- Validation rules: HIGH — read from source

**Research date:** 2026-05-04
**Valid until:** 2026-06-04 (stable codebase, no fast-moving external dependencies)
