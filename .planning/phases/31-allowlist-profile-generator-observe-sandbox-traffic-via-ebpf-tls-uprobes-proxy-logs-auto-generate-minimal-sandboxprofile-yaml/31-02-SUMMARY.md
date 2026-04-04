---
phase: 31-allowlist-profile-generator
plan: "02"
subsystem: allowlistgen, ebpf-attach, shell
tags: [ebpf, dns-observer, learn-mode, docker, proxy-logs, profile-generation, s3]
dependency_graph:
  requires:
    - pkg/allowlistgen (Recorder, ParseProxyLogs, GenerateYAML — from Plan 01)
    - pkg/ebpf/resolver (ResolverConfig.DomainObserver — added here)
    - pkg/ebpf/tls (Consumer.AddHandler, EventHandler)
    - pkg/aws (LoadAWSConfig, S3 client)
    - aws-sdk-go-v2/service/s3 (PutObject, ListObjectsV2, GetObject)
  provides:
    - pkg/ebpf/resolver.ResolverConfig.DomainObserver callback
    - pkg/allowlistgen.ParseProxyLogs (Docker log parser)
    - internal/app/cmd: --observe flag on ebpf-attach (EC2 learn mode)
    - internal/app/cmd: --learn / --learn-output flags on km shell (both substrates)
    - internal/app/cmd.GenerateProfileFromJSON (exported helper)
    - internal/app/cmd.CollectDockerObservations (exported helper)
  affects:
    - internal/app/cmd/ebpf_attach.go (observe flag, S3 upload on shutdown)
    - internal/app/cmd/shell.go (learn flag, substrate-aware post-exit generation)
    - pkg/ebpf/resolver/resolver.go (DomainObserver hook in handleQuery)
tech_stack:
  added: []
  patterns:
    - TDD (RED then GREEN for Task 2 shell --learn)
    - linux build constraint preserved on ebpf_attach.go
    - Exported helpers (GenerateProfileFromJSON, CollectDockerObservations) for test DI
    - Nil-safe DomainObserver callback pattern in DNS resolver
    - Atomic file write (write to .tmp then os.Rename)
key_files:
  created:
    - pkg/allowlistgen/proxylog.go
    - pkg/allowlistgen/proxylog_test.go
    - internal/app/cmd/shell_learn_test.go
  modified:
    - pkg/ebpf/resolver/resolver.go (DomainObserver field + handleQuery hook)
    - internal/app/cmd/ebpf_attach.go (--observe flag, Recorder wiring, S3 upload on shutdown)
    - internal/app/cmd/shell.go (--learn/--learn-output flags, runLearnPostExit, helpers)
decisions:
  - "DomainObserver added to ResolverConfig struct rather than interface to avoid breaking callers"
  - "GenerateProfileFromJSON and CollectDockerObservations exported (capital letter) so cmd_test package can test without AWS/Docker"
  - "observe flag modifies resolverCfg before passing to NewResolver (not after) — correct ordering"
  - "EC2 path uses S3 ListObjectsV2 + lexicographic max for latest session key (no index needed)"
  - "Docker path calls uploadLearnSession for S3 symmetry with EC2; failure is non-fatal"
  - "ECS substrate returns graceful unsupported message rather than error (future work)"
metrics:
  duration: "378s (~6 min)"
  completed: "2026-04-04"
  tasks: 2
  files: 6
requirements: [AGEN-01, AGEN-04]
---

# Phase 31 Plan 02: Wiring — DNS Observer, ebpf-attach --observe, km shell --learn Summary

DNS resolver DomainObserver callback + ebpf-attach --observe learning mode (EC2/TLS) + Docker ParseProxyLogs + km shell --learn post-exit profile generation for both substrates.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | DNS DomainObserver, ebpf-attach --observe with S3 upload, ParseProxyLogs | 8b1c273 | resolver.go, ebpf_attach.go, proxylog.go, proxylog_test.go |
| 2 (RED) | Failing tests for --learn flag and profile generation | 48c028e | shell_learn_test.go |
| 2 (GREEN) | km shell --learn substrate-aware post-exit profile generation | 16a1413 | shell.go |

## What Was Built

### DNS Resolver DomainObserver (`pkg/ebpf/resolver/resolver.go`)

Added `DomainObserver func(domain string, allowed bool)` field to `ResolverConfig`. In `handleQuery`, after the existing zerolog log statement, the callback is invoked with the query domain (trailing dot stripped) and whether it was allowed. The callback is nil-safe — no behavior change when not set.

### ebpf-attach Learning Mode (`internal/app/cmd/ebpf_attach.go`)

Two new flags: `--observe` (bool) and `--observe-output` (string, default `/tmp/km-observed.json`).

When `--observe` is set:
1. `allowlistgen.NewRecorder()` created at startup
2. Recorder registered as TLS handler via `tlsConsumer.AddHandler(recorder.HandleTLSEvent)` (linux-only; after BedrockAuditHandler)
3. `resolverCfg.DomainObserver` wired to `recorder.RecordDNSQuery` (all queries, allowed and denied)
4. On shutdown (after TLS + audit consumers closed): recorder state marshaled to `observedState` JSON
5. Atomic write to `--observe-output` (write `.tmp`, then `os.Rename`)
6. S3 upload to `learn/{sandboxID}/{timestamp}.json` via `KM_ARTIFACTS_BUCKET` env var; failures are logged as warnings (non-fatal)

### Docker Proxy Log Parser (`pkg/allowlistgen/proxylog.go`)

`ParseProxyLogs(dnsLogs, httpLogs io.Reader, recorder *Recorder) error` — parses zerolog JSON from Docker proxy containers:
- DNS proxy: `dns_query` events → `RecordDNSQuery` (all, allowed and denied)
- HTTP proxy: `github_repo_allowed` → `RecordRepo`; any event with non-empty `host` field → `RecordHost` (port stripped via `net.SplitHostPort`)

Both readers may be nil (skipped). Individual malformed/non-JSON lines are silently skipped.

### km shell --learn Flag (`internal/app/cmd/shell.go`)

Two new flags: `--learn` (bool) and `--learn-output` (string, default `observed-profile.yaml`).

When `--learn` is set, after the shell exits, `runLearnPostExit` branches on substrate:

**EC2/ec2spot/ec2demand:** `fetchEC2ObservedJSON` uses S3 `ListObjectsV2` on prefix `learn/{sandboxID}/`, finds the lexicographically latest key, downloads it.

**Docker:** Runs `docker logs km-{id}-km-dns-proxy` and `docker logs km-{id}-km-http-proxy`, passes output to `CollectDockerObservations` which feeds `ParseProxyLogs` into a new Recorder, serializes to `observedState` JSON. Then calls `uploadLearnSession` to upload to S3 at `learn/{sandboxID}/{timestamp}.json` (non-fatal on failure).

**ECS:** Prints unsupported message and returns nil.

Both EC2 and Docker paths converge at `GenerateProfileFromJSON` → writes profile YAML to `--learn-output` → prints: `"Generated SandboxProfile: {path}\nReview and apply with: km validate {path}"`.

### Exported Test Helpers

`GenerateProfileFromJSON(data []byte, base string) ([]byte, error)` and `CollectDockerObservations(sandboxID string, dnsLogs, httpLogs io.Reader) ([]byte, error)` are exported so `cmd_test` package can test profile generation without AWS credentials or a running Docker daemon.

## Deviations from Plan

None — plan executed exactly as written. The `runShell` return value is intentionally ignored (shell may exit with non-zero on Ctrl+D) and profile generation proceeds regardless of shell exit code, as specified.

## Self-Check: PASSED

All 6 files exist. All 3 task commits verified (8b1c273, 48c028e, 16a1413). 11 tests pass (7 proxy log + 4 shell learn). km binary builds on macOS. GOOS=linux GOARCH=amd64 build of internal/app/cmd passes.
