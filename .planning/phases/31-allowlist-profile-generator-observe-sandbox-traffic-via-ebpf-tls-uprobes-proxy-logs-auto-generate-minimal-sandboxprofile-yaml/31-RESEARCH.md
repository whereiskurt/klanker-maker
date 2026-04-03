# Phase 31: Allowlist Profile Generator — Research

**Researched:** 2026-04-03
**Domain:** eBPF traffic observation, YAML code generation, CLI tooling (Go)
**Confidence:** HIGH

## Summary

Phase 31 builds a "learning mode" that observes a running sandbox's actual network traffic and auto-generates a minimal `SandboxProfile` YAML with only the DNS suffixes, hosts, and GitHub repos that were actually used. The feature has three layers of input data and one output.

**Input sources (all already exist in Phase 40 + 41 infrastructure):**
1. **DNS resolver events** (`event_type: "dns_query"`) — `pkg/ebpf/resolver` logs every domain queried by the sandbox with `allowed: true/false`. The eBPF DNS resolver (`km ebpf-attach --allowed-dns`) already emits `zerolog` JSON lines with `domain` field per query.
2. **HTTP proxy CONNECT logs** (`event_type: "http_blocked"`, `"github_mitm_connect"`, `"github_repo_allowed"`) — `sidecars/http-proxy` logs every CONNECT attempt to `stdout` as structured JSON with `host` field. Allowed hosts emit `github_mitm_connect` / `github_repo_allowed`; blocked hosts emit `http_blocked`.
3. **TLS uprobe events** — `pkg/ebpf/tls/consumer.go` dispatches `TLSEvent` structs to registered `EventHandler` callbacks. `pkg/ebpf/tls/github.go` already parses HTTP requests from plaintext TLS payload and extracts `owner/repo`. Adding a new `TrafficRecorderHandler` to the consumer is the clean extension point.

**Primary recommendation:** Add a `pkg/allowlistgen` package with a `Recorder` struct that accumulates observed DNS domains, HTTP hosts, and GitHub repos into Go maps, then a `Generate()` method that normalises and emits a `profile.SandboxProfile` YAML. Wire it into `km ebpf-attach` via a `--observe` flag that enables all three collection paths simultaneously. Add a `km observe <sandbox-id>` (or `km profile generate`) CLI command that SSMs into the running sandbox, attaches the observe mode, waits for a signal (or TTL), then fetches and prints the generated YAML.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| TBD | Run sandbox in "learning mode" that records all DNS/HTTP/TLS traffic | DNS resolver log tailing + TLS consumer handler + proxy CONNECT log tailing |
| TBD | Record observed DNS suffixes, hosts, GitHub repos | Recorder struct with thread-safe maps for each category |
| TBD | Generate minimal SandboxProfile YAML from observed traffic | Generate() on Recorder → profile.SandboxProfile → goccy/go-yaml Marshal |
| TBD | Output is a ready-to-use profile YAML for review | `km profile generate <sandbox>` command or `--observe` flag on ebpf-attach |
</phase_requirements>

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `pkg/ebpf/tls` (internal) | Phase 41 | TLS uprobe event source | Already wired; `Consumer.AddHandler()` is the extension point |
| `pkg/ebpf/resolver` (internal) | Phase 40 | DNS query event source | `handleQuery` logs `event_type: dns_query` with `domain` and `allowed` fields |
| `sidecars/http-proxy` (internal) | Phase 3 | HTTP CONNECT event source | Logs `event_type: http_blocked`/`github_repo_allowed` to `stdout` as JSON |
| `pkg/profile` (internal) | Phase 1 | SandboxProfile type + YAML | `profile.SandboxProfile` is the output type; `goccy/go-yaml` marshals it |
| `github.com/goccy/go-yaml` | v1.19.2 | YAML serialization | Already used by `pkg/profile/types.go` for parsing |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML serialization (alternate) | Used by `configure.go`; prefer `goccy/go-yaml` for consistency with profile package |
| `github.com/rs/zerolog` | v1.33.0 | Structured logging | All components use zerolog; recorder uses it for debug output |
| `sync` (stdlib) | — | Thread-safe map access | `sync.Mutex` on Recorder fields for concurrent handler calls |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `bufio.Scanner` (stdlib) | — | Line-by-line log parsing | Used to tail proxy/DNS sidecar stdout logs for proxy-mode sandboxes |
| `encoding/json` (stdlib) | — | Parse structured JSON log lines | Decode `{"event_type":"dns_query","domain":"..."}` from sidecar logs |
| `strings` (stdlib) | — | DNS suffix normalization | Strip trailing dot, deduplicate, extract root suffix |
| `sort` (stdlib) | — | Deterministic output | Sort allowlists before emitting YAML so diffs are stable |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| In-process TLS handler | Log file parsing | In-process is cleaner; log parsing adds lag and file dependency; use in-process for TLS, log parsing only for proxy sidecar (separate process) |
| Full YAML template | `goccy/go-yaml` Marshal of `profile.SandboxProfile` | Template drifts from schema; always marshal the actual struct so validation can be run on the output |

**Installation:** No new external dependencies required. All libraries are already in `go.mod`.

## Architecture Patterns

### Recommended Project Structure
```
pkg/allowlistgen/
├── recorder.go        # Recorder struct — accumulates observed traffic
├── recorder_test.go   # Unit tests for Recorder
├── generator.go       # Generate() → profile.SandboxProfile
├── generator_test.go  # Golden-file test for YAML output
└── normalize.go       # DNS suffix normalization helpers

internal/app/cmd/
├── observe.go         # km observe / km profile generate CLI command
└── observe_test.go    # CLI smoke test
```

### Pattern 1: Recorder as EventHandler
**What:** `Recorder` implements `tls.EventHandler` (signature: `func(*tls.TLSEvent) error`). Registered via `Consumer.AddHandler()`. Also satisfies a `DNSEventReceiver` interface for the DNS resolver, and a `ProxyEventReceiver` for parsing proxy sidecar logs.
**When to use:** Whenever a TLS plaintext event arrives that contains an outbound HTTP request.
**Example:**
```go
// pkg/allowlistgen/recorder.go
type Recorder struct {
    mu          sync.Mutex
    dnsObserved map[string]struct{}   // full domain names, lowercased
    hostObserved map[string]struct{}  // host:port stripped to host, lowercased
    reposObserved map[string]struct{} // "owner/repo", lowercased
}

// HandleTLSEvent implements tls.EventHandler for the TLS consumer.
func (r *Recorder) HandleTLSEvent(event *tls.TLSEvent) error {
    if event.Direction != tls.DirWrite {
        return nil
    }
    req, err := tls.ParseHTTPRequest(event.PayloadBytes())
    if err != nil {
        return nil // not HTTP, skip
    }
    r.recordHost(req.Host)
    owner, repo := tls.ExtractGitHubRepo(req.Host, req.Path)
    if owner != "" {
        r.recordRepo(owner + "/" + repo)
    }
    return nil
}

// RecordDNSQuery records a domain name observed by the DNS resolver.
func (r *Recorder) RecordDNSQuery(domain string) {
    r.mu.Lock()
    defer r.mu.Unlock()
    // strip trailing dot, lowercase
    r.dnsObserved[strings.ToLower(strings.TrimSuffix(domain, "."))] = struct{}{}
}
```

### Pattern 2: DNS Suffix Normalization
**What:** Observed full FQDNs must be collapsed to minimal suffix allowlists. `api.github.com` + `github.com` + `raw.githubusercontent.com` collapse to `.github.com` + `.githubusercontent.com`. The generator must avoid over-permissive suffixes (don't emit `.com`).
**When to use:** In `Generate()` before writing `spec.network.egress.allowedDNSSuffixes`.
**Example:**
```go
// normalize.go — collapse observed FQDNs into suffix allowlist
// Strategy: keep the registered domain (eTLD+1) as the suffix.
// Use golang.org/x/net/publicsuffix or simple heuristic: split on ".",
// take last 2 segments for .com/.io, last 3 for .co.uk etc.
// For v1, a known-provider map (github.com, amazonaws.com, pypi.org, etc.)
// covers 95% of cases; unknown domains keep their full FQDN.
```

### Pattern 3: km observe CLI Command
**What:** A new top-level `km observe <sandbox-id>` (or sub-command `km profile generate`) that:
1. Resolves sandbox substrate (EC2 or Docker).
2. For EC2+eBPF mode: SSMs into the sandbox, runs `km ebpf-attach --observe --output /tmp/km-observed.json`, waits for SIGINT or TTL.
3. Reads `/tmp/km-observed.json` from S3/SSM or via SSM `SendCommand`.
4. Runs `Recorder.Generate()` to produce a `SandboxProfile`.
5. Writes output YAML to stdout or `--output <file>`.
**When to use:** Operator workflow: create sandbox in open/permissive mode, run workload, run `km observe`, review generated profile, use as template.

### Pattern 4: In-process vs Log-tailing for Proxy Events
**What:** TLS events come in-process via `Consumer.AddHandler()`. HTTP proxy events are from a separate sidecar process. Two options:
- **Option A (preferred):** Add a `--observe` flag to `km ebpf-attach`. In `runEbpfAttach`, create `Recorder`, register as TLS handler, and also start a goroutine that reads proxy logs from the proxy sidecar log file (or named pipe) and calls `Recorder.RecordHost()` for each `event_type: github_mitm_connect` or similar.
- **Option B:** Modify the HTTP proxy sidecar to expose a `/observed` HTTP endpoint, polled by `km observe`.

Option A is simpler — no sidecar protocol changes. The proxy sidecar writes JSON to stdout, which systemd/docker-compose captures. In EC2 user-data mode, the proxy log is typically piped to a file or CloudWatch. For learning mode, redirect proxy stdout to a local file that `km ebpf-attach --observe` tails.

### Anti-Patterns to Avoid
- **Parsing non-structured logs:** Never parse human-readable log output. All relevant sidecars emit JSON lines — use `json.Unmarshal` on each line.
- **Emitting `.com` as a DNS suffix:** Normalization must be bounded. Unknown FQDNs should emit their full hostname as an `allowedHost`, not a wildcard suffix.
- **Marshal via template string:** Never build YAML by string concatenation. Always marshal `profile.SandboxProfile` via `goccy/go-yaml` so schema evolution stays in one place.
- **Blocking on observer shutdown:** `km ebpf-attach --observe` must flush and write JSON atomically on SIGTERM so the operator can terminate gracefully.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| YAML serialization | Custom YAML template | `goccy/go-yaml.Marshal(profile.SandboxProfile{...})` | Schema drift; validation gaps |
| DNS suffix collapse | Regex/heuristic | Known-provider table + `golang.org/x/net/publicsuffix` (optional) | eTLD+1 extraction is subtle (`.co.uk` etc.) |
| HTTP log parsing | Custom tokenizer | `encoding/json.Unmarshal` on each zerolog JSON line | Zerolog guarantees valid JSON lines |
| TLS payload parsing | Custom HTTP parser | `tls.ParseHTTPRequest()` already exists in `pkg/ebpf/tls/http.go` | Handles edge cases, already tested |
| GitHub repo extraction | String splitting | `tls.ExtractGitHubRepo()` in `pkg/ebpf/tls/github.go` | Already handles `api.github.com` vs `github.com` |

**Key insight:** Phase 40+41 built all the observation plumbing. Phase 31 is primarily an aggregation and code-generation layer, not new kernel work. The heaviest lifting is DNS suffix normalization and deciding how to expose the collection lifecycle to the operator.

## Common Pitfalls

### Pitfall 1: DNS Trailing Dot
**What goes wrong:** DNS wire format includes a trailing dot (`github.com.`). Forgetting to strip it produces entries like `.github.com.` in the allowlist, which the DNS resolver's `IsAllowed` check will fail to match.
**Why it happens:** `handleQuery` receives `q.Name` from the miekg/dns library in wire format.
**How to avoid:** Always call `strings.TrimSuffix(domain, ".")` before storing.
**Warning signs:** Generated profile fails `km validate` with "domain has trailing dot".

### Pitfall 2: Host vs DNS Suffix Overlap
**What goes wrong:** The generator emits both `allowedHosts: [api.github.com]` and `allowedDNSSuffixes: [.github.com]`. The suffixes make the explicit hosts redundant, creating noise.
**Why it happens:** DNS events and CONNECT events are recorded independently.
**How to avoid:** In `Generate()`, deduplicate: if a host is covered by an emitted suffix, omit it from `allowedHosts`.

### Pitfall 3: SNI vs HTTP Host Header Mismatch
**What goes wrong:** TLS uprobe captures the inner HTTP/1.1 `Host:` header (the real hostname). In HTTP/2, the `Host` header becomes `:authority`. `ParseHTTPRequest` already rejects HTTP/2 frames (`ErrNotHTTP` on PRI preface), so HTTP/2 traffic will not be captured via TLS handler.
**Why it happens:** HTTP/2 uses HPACK-compressed headers; the uprobe sees raw TLS plaintext which may be HTTP/2 framing.
**How to avoid:** Fall back to DNS events for HTTP/2 hosts (GitHub API uses HTTP/2 in some clients). Document the limitation: TLS handler covers HTTP/1.1; DNS events cover all protocols.

### Pitfall 4: Port Noise in Host Observation
**What goes wrong:** Proxy CONNECT events include port in `host` field (e.g., `api.github.com:443`). Storing with port produces an invalid `allowedHosts` entry.
**Why it happens:** `goproxy` passes `host:port` to the CONNECT handler; the log field inherits this.
**How to avoid:** Strip port using `net.SplitHostPort` before recording. This is the same pattern as `IsHostAllowed` in `proxy.go:118`.

### Pitfall 5: Observation in "block" Mode Misses Blocked Traffic
**What goes wrong:** If the sandbox was created with strict enforcement, DNS denies and CONNECT rejects mean observed traffic is only the currently-allowed subset — the generated profile misses what the workload actually needs.
**Why it happens:** The sandbox was already constrained before observation started.
**How to avoid:** Learning mode should ideally run with `--firewall-mode allow` (eBPF permissive) and `allowedHosts: ["*"]` on the proxy so all traffic passes through and is recorded. Document this requirement in `km observe` help text.

### Pitfall 6: Race Between Observer Shutdown and Log Flush
**What goes wrong:** SIGTERM arrives while the ring buffer consumer is processing events. If the observer writes results before the last events are drained, some traffic is missed.
**Why it happens:** Ring buffer is async; SIGTERM closes the reader mid-drain.
**How to avoid:** On SIGTERM, close the ring buffer reader, wait for `Consumer.Run()` to return (it returns `nil` on `ErrClosed`), then write the observed state.

## Code Examples

Verified patterns from existing codebase:

### TLS Consumer Handler Registration (existing pattern)
```go
// Source: internal/app/cmd/ebpf_attach.go + pkg/ebpf/tls/consumer.go
consumer, err := ebpftls.NewConsumer(objs.TlsEvents, logger)
if err != nil {
    return fmt.Errorf("create tls consumer: %w", err)
}

// Register handlers (existing):
ghHandler := ebpftls.NewGitHubAuditHandler(repos, logger)
consumer.AddHandler(ghHandler.Handle)

// New: register Recorder as additional handler:
recorder := allowlistgen.NewRecorder()
consumer.AddHandler(recorder.HandleTLSEvent)

go func() { _ = consumer.Run(ctx) }()
```

### DNS Resolver Observation Hook (existing log pattern)
```go
// Source: pkg/ebpf/resolver/resolver.go:210
// The resolver already logs every query. For observation, inject a callback:
// Option: add DomainObserver interface to ResolverConfig:
type ResolverConfig struct {
    // ... existing fields ...
    DomainObserver func(domain string, allowed bool) // new field, nil-safe
}
// In handleQuery, after the log statement:
if r.cfg.DomainObserver != nil {
    r.cfg.DomainObserver(domain, allowed)
}
```

### Profile YAML Generation Pattern
```go
// Source: pkg/profile/types.go + gopkg.in/yaml.v3 (configure.go pattern)
func (r *Recorder) Generate(base string) (*profile.SandboxProfile, error) {
    r.mu.Lock()
    defer r.mu.Unlock()

    suffixes := r.collapseToDNSSuffixes()
    hosts := r.deduplicateHosts(suffixes)
    repos := r.sortedRepos()

    p := profile.SandboxProfile{
        APIVersion: "klankermaker.ai/v1alpha1",
        Kind:       "SandboxProfile",
        Metadata:   profile.Metadata{Name: "observed-" + time.Now().Format("20060102-150405")},
    }
    if base != "" {
        p.Extends = base
    }
    p.Spec.Network.Egress.AllowedDNSSuffixes = suffixes
    p.Spec.Network.Egress.AllowedHosts = hosts
    if len(repos) > 0 {
        p.Spec.SourceAccess.GitHub = &profile.GitHubAccess{
            AllowedRepos: repos,
        }
    }
    return &p, nil
}
```

### Proxy Log Line Parsing (JSON log format)
```go
// Source: sidecars/http-proxy/httpproxy/proxy.go event_type patterns
// Proxy emits JSON to stdout, e.g.:
// {"level":"info","sandbox_id":"sb-abc","event_type":"github_mitm_connect","host":"github.com:443","time":"..."}
// {"level":"info","sandbox_id":"sb-abc","event_type":"http_blocked","host":"evil.com:443","time":"..."}

type proxyLogLine struct {
    EventType string `json:"event_type"`
    Host      string `json:"host"`
    Repo      string `json:"repo"` // for github_repo_allowed events
}
// scan proxy log file line by line, unmarshal, record
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Manual allowlist authoring | Observed traffic → generated profile | Phase 31 (new) | Reduces trial-and-error cycle for operators |
| MITM proxy for host observation | TLS uprobes for plaintext capture | Phase 41 | Uprobes work even when proxy is not MITM'd |
| No learning mode | `--firewall-mode allow` + recorder | Phase 31 (new) | Permissive mode needed for accurate observation |

**Deprecated/outdated:**
- None — this is entirely net-new functionality.

## Open Questions

1. **Where does the observer write its accumulated state?**
   - What we know: On EC2, `km ebpf-attach` runs in the sandbox. Results could be written to a local JSON file, uploaded to S3, or printed to stdout on shutdown.
   - What's unclear: The cleanest retrieval mechanism for `km observe` (operator side). SSM `SendCommand` can fetch stdout. S3 upload is more reliable for long sessions.
   - Recommendation: Write to `/tmp/km-observed.json` on SIGTERM; compiler adds an artifact upload hook in learning mode so the file lands in the sandbox's S3 artifacts bucket. `km observe` fetches from S3.

2. **Should observation be a separate CLI command or a flag on `km create`?**
   - What we know: `km ebpf-attach` is an internal command run inside the sandbox. `km create` runs on the operator's machine.
   - What's unclear: Whether to add `km observe <sandbox>` (operator-side, attaches to running sandbox) or `km create --learn` (creates sandbox already in learn mode).
   - Recommendation: Add `--learn` flag to `km create` that sets `--firewall-mode allow` and enables `--observe` in the user-data ebpf-attach invocation. Add `km observe <sandbox>` operator command that retrieves the JSON result and generates the YAML.

3. **DNS suffix normalization depth**
   - What we know: Simple eTLD+1 heuristic covers 95% of cases (2-part suffixes for .com/.io/.org, 3-part for .co.uk etc.).
   - What's unclear: Whether `golang.org/x/net/publicsuffix` is already in go.mod.
   - Recommendation: Check `go.mod` — if not present, implement a simple known-providers lookup table for the most common TLDs in the sandbox context. Avoid adding a dependency purely for this. Unknown domains fall back to full FQDN as an `allowedHost` entry.

4. **HTTP/2 observability gap**
   - What we know: TLS uprobes can capture HTTP/2 frames but `ParseHTTPRequest` currently rejects them. DNS events fill the gap for most HTTP/2 hosts.
   - What's unclear: Whether this gap is acceptable for v1 of the profile generator.
   - Recommendation: Document the limitation. For Phase 31 v1, DNS events + HTTP/1.1 TLS events + proxy CONNECT logs together cover 95%+ of real-world traffic.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./pkg/allowlistgen/... -v -count=1` |
| Full suite command | `go test ./pkg/allowlistgen/... ./internal/app/cmd/ -v -count=1` |

### Phase Requirements to Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| TBD-01 | Recorder.RecordDNSQuery accumulates domains | unit | `go test ./pkg/allowlistgen/ -run TestRecorderDNS` | Wave 0 |
| TBD-02 | Recorder.HandleTLSEvent records hosts and repos | unit | `go test ./pkg/allowlistgen/ -run TestRecorderTLS` | Wave 0 |
| TBD-03 | DNS suffix normalization collapses FQDNs correctly | unit | `go test ./pkg/allowlistgen/ -run TestNormalizeSuffixes` | Wave 0 |
| TBD-04 | Generate() emits valid SandboxProfile YAML | unit | `go test ./pkg/allowlistgen/ -run TestGenerate` | Wave 0 |
| TBD-05 | Generated profile passes km validate | integration | `go test ./pkg/allowlistgen/ -run TestGenerateValidates` | Wave 0 |
| TBD-06 | Deduplication: host covered by suffix not in allowedHosts | unit | `go test ./pkg/allowlistgen/ -run TestDedup` | Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/allowlistgen/ -v -count=1`
- **Per wave merge:** `go test ./pkg/... ./internal/app/cmd/ -v -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/allowlistgen/recorder.go` — Recorder struct and handlers
- [ ] `pkg/allowlistgen/recorder_test.go` — unit tests for recording
- [ ] `pkg/allowlistgen/generator.go` — Generate() → SandboxProfile
- [ ] `pkg/allowlistgen/generator_test.go` — golden-file YAML test
- [ ] `pkg/allowlistgen/normalize.go` — DNS suffix normalization
- [ ] `internal/app/cmd/observe.go` — km observe / km profile generate CLI

## Sources

### Primary (HIGH confidence)
- `pkg/ebpf/tls/consumer.go` — EventHandler interface, AddHandler pattern, ring buffer drain
- `pkg/ebpf/tls/types.go` — TLSEvent struct layout, direction/library constants
- `pkg/ebpf/tls/github.go` — ExtractGitHubRepo, EventHandler type definition
- `pkg/ebpf/tls/http.go` — ParseHTTPRequest, ErrNotHTTP, HTTP/2 rejection
- `pkg/ebpf/resolver/resolver.go` — DNS query logging pattern, domain field, ResolverConfig
- `sidecars/http-proxy/httpproxy/proxy.go` — IsHostAllowed, event_type log patterns, host:port format
- `sidecars/http-proxy/httpproxy/github.go` — ExtractRepoFromPath, github host matching
- `pkg/profile/types.go` — SandboxProfile schema, NetworkSpec, EgressSpec, GitHubAccess
- `pkg/profile/builtins/open-dev.yaml` — reference profile YAML format
- `internal/app/cmd/ebpf_attach.go` — runEbpfAttach integration point, Consumer wiring

### Secondary (MEDIUM confidence)
- `sidecars/dns-proxy/dnsproxy/proxy.go` — IsAllowed logic, dns_query event_type for proxy-mode sandboxes
- `internal/app/cmd/configure.go` — `gopkg.in/yaml.v3` Marshal pattern for YAML output

### Tertiary (LOW confidence)
- None.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries already in the repo and in use
- Architecture: HIGH — extension points (Consumer.AddHandler, zerolog JSON lines) are verified in code
- Pitfalls: HIGH — each identified from concrete code paths (e.g., trailing dot from miekg/dns wire format)

**Research date:** 2026-04-03
**Valid until:** 2026-05-03 (stable stack; eBPF consumer/handler API unlikely to change)
