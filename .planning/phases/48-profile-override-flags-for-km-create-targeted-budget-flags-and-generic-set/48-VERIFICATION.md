---
phase: 48-profile-override-flags-for-km-create-targeted-budget-flags-and-generic-set
verified: 2026-04-07T21:35:00Z
status: passed
score: 9/9 must-haves verified
re_verification: false
---

# Phase 48: Profile Override Flags Verification Report

**Phase Goal:** Add --ttl and --idle CLI override flags to km create, with TTL=0 meaning "never destroy, hibernate on idle with looping idle detection"
**Verified:** 2026-04-07T21:35:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | km create profile.yaml --ttl 3h sets TTL to 3h regardless of profile value | VERIFIED | `applyLifecycleOverrides` in create.go:1932 sets `p.Spec.Lifecycle.TTL = ttlOverride`; flag registered on line 165 |
| 2 | km create profile.yaml --idle 30m sets idle timeout to 30m regardless of profile value | VERIFIED | `applyLifecycleOverrides` sets `p.Spec.Lifecycle.IdleTimeout = idleOverride`; flag registered on line 167 |
| 3 | km create profile.yaml --ttl 0 creates sandbox with no TTL EventBridge schedule | VERIFIED | TTL=0/0s sets `p.Spec.Lifecycle.TTL = ""` (line 1936); existing TTL expiry guard `if TTL != ""` at create.go:536 means `ttlExpiry` stays nil, no EventBridge schedule created |
| 4 | km create --idle 48h with profile ttl=24h produces a clear error | VERIFIED | `ValidateSemantic` re-runs after overrides (create.go:1954); prints "flag override conflict" and returns error |
| 5 | runCreateRemote path applies --ttl and --idle overrides identically to runCreate | VERIFIED | `applyLifecycleOverrides` called at create.go:1582 in runCreateRemote, identical to runCreate call at line 254 |
| 6 | S3 stored profile reflects overridden values, not original YAML | VERIFIED | `yaml.Marshal(resolvedProfile)` branch at create.go:624-626 when `ttlOverride != "" \|\| idleOverride != ""`; same pattern in runCreateRemote at line 1625 |
| 7 | When TTL=0, idle detection triggers hibernate/stop instead of destroy | VERIFIED | `idleActionFromProfile` returns "hibernate" when TTL="" and IdleTimeout set; `IDLE_ACTION=hibernate` written to userdata; sidecar calls `PublishSandboxCommand(ctx, ebClient, id, "stop")` not `PublishSandboxIdleEvent` |
| 8 | After hibernate, idle detection re-arms and hibernates again on next idle | VERIFIED | `startIdleDetector` is a recursive closure in main.go:98-113; `time.AfterFunc(2*time.Minute, startIdleDetector)` fires after each hibernate cycle; sidecar does NOT call `cancel()` |
| 9 | When TTL is not 0, idle detection behaves exactly as before (one-shot destroy + exit) | VERIFIED | Default path in `buildIdleCallback` (main.go:260+) calls `PublishSandboxIdleEvent` + `cancel()` when `idleAction != "hibernate"`; all existing tests pass |

**Score:** 9/9 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/cmd/create.go` | --ttl and --idle flag declarations, profile mutation, TTL=0 guard, S3 upload fix | VERIFIED | Flags at lines 165-168; `applyLifecycleOverrides` function at line 1932; called in both runCreate (line 254) and runCreateRemote (line 1582); mutated YAML upload at lines 624-626 and 1625-1627 |
| `internal/app/cmd/create_override_test.go` | Unit tests for override flags, TTL=0, conflict detection | VERIFIED | 5 test functions covering: TTL/idle mutation patterns, S3 upload, validate.go comment, binary flag registration, runCreateRemote signature — all pass |
| `pkg/profile/validate.go` | ValidateSemantic skips TTL >= idle check when TTL is empty | VERIFIED | Line 217: `// TTL="" means no auto-destroy (--ttl 0 sentinel); skip TTL >= idle check.`; rule condition: `if p.Spec.Lifecycle.TTL != "" && p.Spec.Lifecycle.IdleTimeout != ""` already skips when TTL="" |
| `pkg/compiler/userdata.go` | IdleAction field in userDataParams, IDLE_ACTION env var in systemd unit template | VERIFIED | `IdleAction string` field at line 1618; `idleActionFromProfile` function at line 1578; template conditional `{{- if .IdleAction }}` at line 427; `IDLE_ACTION={{ .IdleAction }}` at line 428 |
| `sidecars/audit-log/cmd/main.go` | IDLE_ACTION=hibernate path: PublishSandboxCommand stop, reset detector, do NOT cancel | VERIFIED | `buildIdleCallback` at line 259; hibernate path calls `PublishSandboxCommand(ctx, ebClient, id, "stop")`; calls `rearmFn()` not `cancel()`; `startIdleDetector` closure with AfterFunc re-arm |
| `pkg/compiler/userdata_idle_action_test.go` | Unit test for IdleAction param propagation | VERIFIED | 7 test functions covering template rendering with hibernate/empty IdleAction, idleActionFromProfile, and full generateUserData integration — all pass |
| `sidecars/audit-log/cmd/idle_action_test.go` | Unit test for IDLE_ACTION=hibernate loop behavior | VERIFIED | 7 test functions covering: hibernate calls stop, hibernate re-arms, default calls idle event, default does not re-arm, nil EB client, "destroy" equivalent to empty, AfterFunc scheduling — all pass |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| create.go flag parse | resolvedProfile.Spec.Lifecycle mutation | `applyLifecycleOverrides(resolvedProfile, ttlOverride, idleOverride)` at create.go:254 and 1582 | WIRED | Both runCreate and runCreateRemote call the helper after profile resolution; pattern `ttlOverride\|idleOverride` confirmed at lines 91-92, 138, 140 |
| create.go TTL=0 | ttlExpiry = nil (no EventBridge schedule) | TTL set to "" triggers existing `if TTL != ""` guard at create.go:536 | WIRED | `applyLifecycleOverrides` sets TTL to "" for "0"/"0s"; ttlExpiry computation skips when TTL=="" |
| create.go (S3 upload) | S3 PutObject with mutated YAML | `yaml.Marshal(resolvedProfile)` when overrides applied | WIRED | Pattern `yaml\.Marshal.*resolvedProfile` confirmed at both create paths (lines 626, 1626) |
| create.go via compiler.Compile | userdata.go IdleAction=hibernate | `applyLifecycleOverrides` sets TTL=""; `compiler.Compile` calls `generateUserData(p,...)` which calls `idleActionFromProfile(p)` returning "hibernate" | WIRED | `idleActionFromProfile` at userdata.go:1578 checks `TTL == "" && IdleTimeout != ""`; integrated test `TestIdleActionGenerateUserDataTTLZero` passes |
| pkg/compiler/userdata.go | sidecars/audit-log/cmd/main.go | IDLE_ACTION env var injected into systemd unit; sidecar reads `os.Getenv("IDLE_ACTION")` at main.go:94 | WIRED | Template conditional at userdata.go:427-429; sidecar reads at main.go:94 |
| sidecars/audit-log/cmd/main.go | pkg/aws/idle_event.go (PublishSandboxCommand) | stop event instead of idle event when IDLE_ACTION=hibernate | WIRED | `buildIdleCallback` at main.go:265 calls `kmaws.PublishSandboxCommand(ctx, ebClient, id, "stop")` |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|---------|
| --ttl flag on km create | 48-01 | `--ttl <duration>` flag overrides spec.lifecycle.ttl | SATISFIED | Flag registered at create.go:165; appears in `km create --help` output |
| --idle flag on km create | 48-01 | `--idle <duration>` flag overrides idle timeout | SATISFIED | Flag registered at create.go:167; appears in `km create --help` output |
| --ttl 0 disables auto-destroy | 48-01 | TTL=0 sets TTL="" preventing EventBridge schedule | SATISFIED | create.go:1934-1936 handles "0" and "0s"; ttlExpiry stays nil |
| S3 profile upload uses mutated profile | 48-01 | S3 upload serializes mutated resolvedProfile when overrides applied | SATISFIED | yaml.Marshal branch at create.go:624 and 1625 |
| --ttl 0 hibernates on idle instead of destroying | 48-02 | IDLE_ACTION=hibernate triggers stop command and re-arms | SATISFIED | Full chain: create.go → compiler → userdata IDLE_ACTION=hibernate → sidecar stop+rearm |
| Idle detector loops when TTL=0 | 48-02 | After hibernate, detector re-arms via AfterFunc | SATISFIED | `startIdleDetector` closure with `time.AfterFunc(2*time.Minute, startIdleDetector)` at main.go:103 |

### Anti-Patterns Found

No anti-patterns found. No TODO/FIXME/placeholder comments in modified files. No stub implementations. All functions have substantive logic.

### Human Verification Required

None. All key behaviors are programmatically verifiable and all tests pass.

## Test Results Summary

```
internal/app/cmd (TestApplyLifecycleOverrides_*):       5/5 PASS
pkg/compiler (TestIdleAction*):                          8/8 PASS
sidecars/audit-log/cmd (TestIdleAction*, TestAfterFunc): 7/7 PASS
km binary build:                                        OK
audit-log binary build:                                 OK
km create --help shows --ttl and --idle:                OK
```

## Gaps Summary

No gaps. All 9 observable truths are verified. All artifacts exist, are substantive, and are correctly wired. Both binaries build. All 20 phase-specific tests pass.

---

_Verified: 2026-04-07T21:35:00Z_
_Verifier: Claude (gsd-verifier)_
