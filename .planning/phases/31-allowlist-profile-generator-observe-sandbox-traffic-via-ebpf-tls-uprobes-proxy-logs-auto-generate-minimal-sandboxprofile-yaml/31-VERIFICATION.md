---
phase: 31-allowlist-profile-generator
verified: 2026-04-03T00:00:00Z
status: passed
score: 10/10 must-haves verified
gaps: []
human_verification:
  - test: "Run km shell --learn against a live Docker sandbox and verify profile YAML is written to CWD"
    expected: "observed-profile.yaml created in CWD with allowedDNSSuffixes, allowedHosts, and allowedRepos matching actual sandbox traffic"
    why_human: "Requires a running Docker sandbox and docker logs output; cannot be verified without infrastructure"
  - test: "Run km ebpf-attach --observe inside an EC2 sandbox and verify S3 upload"
    expected: "learn/{sandboxID}/{timestamp}.json appears in KM_ARTIFACTS_BUCKET; local /tmp/km-observed.json written atomically"
    why_human: "Requires a live sandbox with eBPF TLS uprobes attached; linux-only build path not exercised in CI on macOS"
---

# Phase 31: Allowlist Profile Generator Verification Report

**Phase Goal:** Run a sandbox in "learning mode" that records all observed DNS, HTTP, and TLS traffic from Phase 41 uprobe events and Phase 40 eBPF audit logs, then generates a minimal SandboxProfile YAML with only the DNS suffixes, hosts, and GitHub repos actually used. Must work on BOTH EC2 (eBPF) and Docker (proxy logs) substrates. Output is a ready-to-use profile YAML that can be reviewed and applied.
**Verified:** 2026-04-03
**Status:** passed
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| #  | Truth | Status | Evidence |
|----|-------|--------|----------|
| 1  | Recorder accumulates DNS domains, HTTP hosts, and GitHub repos from concurrent handler calls | VERIFIED | `pkg/allowlistgen/recorder.go` — three `map[string]struct{}` fields protected by `sync.Mutex`; `TestRecorderConcurrent` passes with `-race` |
| 2  | DNS suffix normalization collapses FQDNs to eTLD+1 suffixes without over-permissive results | VERIFIED | `pkg/allowlistgen/normalize.go` uses `publicsuffix.EffectiveTLDPlusOne`; skips bare TLDs on error; `TestNormalizeSuffixes_NoOverPermissive` and `TestNormalizeSuffixes_CoUK` pass |
| 3  | Generate() produces a valid SandboxProfile YAML with sorted, deduplicated allowlists | VERIFIED | `pkg/allowlistgen/generator.go` — `Generate()` + `GenerateYAML()`; `TestGenerateValidates` calls `profile.Validate()` and passes |
| 4  | Hosts already covered by an emitted DNS suffix are omitted from allowedHosts | VERIFIED | `deduplicateHosts()` in `generator.go`; `TestGenerate_HostDedup` and `TestGenerate_Basic` pass |
| 5  | DNS resolver calls DomainObserver callback for every query when set | VERIFIED | `pkg/ebpf/resolver/resolver.go` line 223-224: nil-safe callback fires with `strings.TrimSuffix(domain, ".")` and `allowed` bool |
| 6  | ebpf-attach --observe flag enables the Recorder as a TLS handler and writes observed JSON on shutdown | VERIFIED | `internal/app/cmd/ebpf_attach.go`: `--observe` flag registered; `allowlistgen.NewRecorder()` created; `tlsConsumer.AddHandler(recorder.HandleTLSEvent)` at line 388; atomic write (`.tmp` + `os.Rename`) at lines 459-463 |
| 7  | ebpf-attach --observe uploads observed JSON to S3 at a timestamped learn/ key on shutdown | VERIFIED | `ebpf_attach.go` lines 466-491: `learn/{sandboxID}/{timestamp}.json` key; `KM_ARTIFACTS_BUCKET` env var; non-fatal on failure |
| 8  | km shell --learn on EC2 substrates fetches observed JSON from S3 and generates profile YAML on the host | VERIFIED | `shell.go` `fetchEC2ObservedJSON()` at line 575: `ListObjectsV2` on `learn/{sandboxID}/` prefix, lexicographic max key, `GetObject` download; feeds `GenerateProfileFromJSON` |
| 9  | km shell --learn on Docker substrates parses dns-proxy and http-proxy container logs and generates profile YAML on the host | VERIFIED | `shell.go` lines 527-544: `docker logs km-{id}-km-dns-proxy` + `docker logs km-{id}-km-http-proxy`; `CollectDockerObservations` → `ParseProxyLogs` → `GenerateProfileFromJSON`; `TestCollectDockerObservations` passes |
| 10 | ParseProxyLogs correctly extracts DNS queries, hosts, and repos from zerolog JSON lines | VERIFIED | `pkg/allowlistgen/proxylog.go` — all 7 `TestParseProxy*` / `TestParseDNS*` / `TestParseHTTP*` tests pass |

**Score:** 10/10 truths verified

---

## Required Artifacts

### Plan 01 Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/allowlistgen/recorder.go` | `NewRecorder`, `RecordDNSQuery`, `RecordHost`, `RecordRepo`, `DNSDomains`, `Hosts`, `Repos` | VERIFIED | 104 lines; all methods present; `sync.Mutex` guards three maps |
| `pkg/allowlistgen/recorder_tls.go` | `HandleTLSEvent` satisfying `tls.EventHandler` | VERIFIED | 34 lines; `//go:build linux`; processes `DirWrite` only; calls `ParseHTTPRequest` + `ExtractGitHubRepo` |
| `pkg/allowlistgen/normalize.go` | `CollapseToDNSSuffixes` using `publicsuffix.EffectiveTLDPlusOne` | VERIFIED | 35 lines; skips bare TLDs to prevent over-permissive suffixes; sorted output |
| `pkg/allowlistgen/generator.go` | `Generate()`, `GenerateYAML()`, `GenerateYAMLFromRecorder()` | VERIFIED | 181 lines; HTTPS-implies-DNS union; host deduplication; schema-valid scaffold |
| `pkg/allowlistgen/recorder_test.go` | Unit tests for Recorder | VERIFIED | 82 lines; 7 tests including `TestRecorderConcurrent` |
| `pkg/allowlistgen/recorder_tls_test.go` | TLS handler tests | VERIFIED | 71 lines; `//go:build linux`; tests `DirWrite`, `DirRead`, non-HTTP payloads |
| `pkg/allowlistgen/normalize_test.go` | DNS normalization tests | VERIFIED | 80 lines; 7 tests covering PSL edge cases |
| `pkg/allowlistgen/generator_test.go` | Generator tests with profile validation | VERIFIED | 202 lines; 8 tests including `TestGenerateValidates` |

### Plan 02 Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/ebpf/resolver/resolver.go` | `DomainObserver func(domain string, allowed bool)` field on `ResolverConfig` | VERIFIED | Lines 57-60: field added; lines 223-224: nil-safe callback in `handleQuery` |
| `internal/app/cmd/ebpf_attach.go` | `--observe` flag, Recorder wiring, S3 upload on shutdown | VERIFIED | `--observe` + `--observe-output` flags at lines 91-94; `NewRecorder()` at line 156; `AddHandler` at line 388; `DomainObserver` at line 258; S3 upload at lines 466-491 |
| `internal/app/cmd/shell.go` | `--learn` / `--learn-output` flags; substrate-aware `runLearnPostExit` | VERIFIED | Flags at lines 138-139; `runLearnPostExit` at line 494; EC2 branch at line 520; Docker branch at lines 527-550; ECS unsupported message at line 557 |
| `pkg/allowlistgen/proxylog.go` | `ParseProxyLogs(dnsLogs, httpLogs io.Reader, recorder *Recorder) error` | VERIFIED | 94 lines; `parseDNSProxyLogs` + `parseHTTPProxyLogs`; feeds Recorder methods |
| `pkg/allowlistgen/proxylog_test.go` | 7 proxy log tests | VERIFIED | 141 lines; all 7 `TestParseProxy*` tests pass |
| `internal/app/cmd/shell_learn_test.go` | `--learn` flag, `GenerateProfileFromJSON`, `CollectDockerObservations` tests | VERIFIED | 137 lines; 4 tests pass without AWS credentials or Docker |

---

## Key Link Verification

### Plan 01 Key Links

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `pkg/allowlistgen/recorder_tls.go` | `pkg/ebpf/tls` | `HandleTLSEvent` satisfies `tls.EventHandler` signature | WIRED | `func (r *Recorder) HandleTLSEvent(event *tls.TLSEvent) error` — exact signature match |
| `pkg/allowlistgen/generator.go` | `pkg/profile` | Populates `profile.SandboxProfile` struct fields | WIRED | `profile.SandboxProfile`, `profile.Spec`, `profile.EgressSpec` etc. throughout generator.go |

### Plan 02 Key Links

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `internal/app/cmd/ebpf_attach.go` | `pkg/allowlistgen` | `allowlistgen.NewRecorder()` created and wired | WIRED | Line 24: `import "github.com/whereiskurt/klankrmkr/pkg/allowlistgen"`; line 156: `NewRecorder()`; line 388: `AddHandler`; line 258: `DomainObserver` |
| `internal/app/cmd/shell.go` | `pkg/allowlistgen` | EC2: `GenerateProfileFromJSON`; Docker: `ParseProxyLogs` via `CollectDockerObservations` | WIRED | Line 19: import; lines 456-471: `GenerateProfileFromJSON` creates Recorder + calls `GenerateYAML`; lines 479-490: `CollectDockerObservations` calls `ParseProxyLogs` |
| `pkg/allowlistgen/proxylog.go` | `pkg/allowlistgen/recorder.go` | `ParseProxyLogs` feeds `RecordDNSQuery`, `RecordHost`, `RecordRepo` | WIRED | Lines 57, 79, 88-90: all three Recorder methods called from proxylog parsers |
| `pkg/ebpf/resolver/resolver.go` | `pkg/allowlistgen` (indirect) | `DomainObserver` callback invoked in `handleQuery` | WIRED | Lines 223-224 in `handleQuery`; `ebpf_attach.go` wires `recorder.RecordDNSQuery` as the callback |

---

## Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| AGEN-01 | 31-02 | DNS resolver DomainObserver callback for learning mode | SATISFIED | `ResolverConfig.DomainObserver` field added; callback fires in `handleQuery`; wired to `recorder.RecordDNSQuery` in ebpf-attach |
| AGEN-02 | 31-01 | Thread-safe Recorder accumulating DNS/host/repo observations with eTLD+1 normalization | SATISFIED | `pkg/allowlistgen/recorder.go` + `normalize.go`; 22 tests pass including race detector |
| AGEN-03 | 31-01 | `Generate()` produces schema-valid SandboxProfile YAML with host deduplication | SATISFIED | `pkg/allowlistgen/generator.go`; `TestGenerateValidates` calls `profile.Validate()` and passes |
| AGEN-04 | 31-02 | `km shell --learn` works on EC2 (S3 fetch) and Docker (proxy log parse) substrates | SATISFIED | `--learn` flag on `shell.go`; EC2 path: `fetchEC2ObservedJSON` (S3 + SSM fallback); Docker path: `CollectDockerObservations` + `ParseProxyLogs`; both write profile YAML to host |

**Note on REQUIREMENTS.md:** AGEN-01 through AGEN-04 are defined in ROADMAP.md (Phase 31 requirements block) and referenced in both PLAN frontmatters, but are not yet added to the `REQUIREMENTS.md` traceability table or requirement definitions section. These IDs appear to have been assigned during planning after the REQUIREMENTS.md was last updated. The implementations satisfy the intent described in ROADMAP.md and the RESEARCH file's phase requirement descriptions.

---

## Anti-Patterns Found

No blockers or warnings found. Scan of all six modified/created implementation files found:
- No TODO/FIXME/XXX/PLACEHOLDER comments
- No empty `return nil` / `return {}` stub implementations
- No console.log-only handlers
- ECS unsupported message is intentional documented behavior (not a stub), consistent with plan spec

---

## Human Verification Required

### 1. Docker --learn end-to-end

**Test:** Create a Docker sandbox, run `km shell --learn <sandbox-id>`, execute some network traffic (e.g. `curl https://api.github.com`), exit the shell, observe `observed-profile.yaml` in CWD.
**Expected:** YAML file contains `.github.com` in `allowedDNSSuffixes`; `octocat/hello-world` type entry in `allowedRepos` if a GitHub API call was made; file passes `km validate observed-profile.yaml`.
**Why human:** Requires a running Docker sandbox and live `docker logs` output from `km-{id}-km-dns-proxy` and `km-{id}-km-http-proxy` containers.

### 2. EC2 ebpf-attach --observe end-to-end

**Test:** Create an EC2 sandbox with eBPF TLS uprobes enabled, start `km ebpf-attach --observe`, exercise network traffic, send SIGTERM/SIGINT, verify `/tmp/km-observed.json` is written and `learn/{sandboxID}/{timestamp}.json` exists in `KM_ARTIFACTS_BUCKET`.
**Expected:** JSON file contains populated `dns`, `hosts`, `repos` arrays; S3 object exists at the expected key.
**Why human:** Requires a live EC2 sandbox on Linux with eBPF/uprobe support; `ebpf_attach.go` has `//go:build linux && amd64` constraint.

### 3. EC2 km shell --learn with S3 fetch

**Test:** After completing the ebpf-attach --observe test above, run `km shell --learn <sandbox-id>` from an operator machine, exit the shell, observe `observed-profile.yaml`.
**Expected:** Profile generated from the S3 learn session; `km validate observed-profile.yaml` passes.
**Why human:** Depends on live EC2 sandbox and S3 objects from the previous test; requires AWS credentials and a live sandbox.

---

## Gaps Summary

No gaps. All 10 truths verified, all 14 artifacts exist and are substantive (not stubs), all 6 key links are wired, all 4 AGEN requirements are satisfied by working code. Tests pass across all layers: 22 unit tests for `pkg/allowlistgen/` (including race detector), 7 proxy log tests, 4 shell learn tests, macOS and linux/amd64 builds clean, `go vet` clean.

The only open items are the human verification tests which require live infrastructure.

---

_Verified: 2026-04-03_
_Verifier: Claude (gsd-verifier)_
