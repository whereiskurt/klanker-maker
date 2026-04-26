---
phase: 56-learn-mode-ami-snapshot-and-lifecycle-management
plan: 05
subsystem: cmd,allowlistgen
tags: [cobra, ami, ec2, learn-mode, testing, shell, allowlistgen]

# Dependency graph
requires:
  - phase: 56-learn-mode-ami-snapshot-and-lifecycle-management
    plan: 04
    provides: BakeFromSandbox exported helper in internal/app/cmd/ami.go
  - phase: 55-learn-mode-command-capture
    plan: 01
    provides: RecordCommand/Commands on Recorder; runLearnPostExit base
provides:
  - --ami flag on km shell --learn
  - runLearnPostExit bakeAMI bool param; bake fires BEFORE flushEC2Observations
  - Recorder.RecordAMI(amiID string) + Recorder.AMI() string
  - Generate() emits Spec.Runtime.AMI when recorder.amiID is non-empty
  - GenerateProfileFromJSON(data, base, amiID string) — third param is new
affects:
  - Any caller of GenerateProfileFromJSON (all 5 updated to pass amiID="")
  - shell_ami_test.go (consumed from Phase 56-06 pre-commit)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Package-level function vars (bakeFromSandboxFn, flushEC2ObservationsFn, fetchEC2ObservedJSONFn) for DI without interface changes"
    - "Internal test package (package cmd) accesses unexported runLearnPostExit and injectable vars directly"
    - "RecordAMI follows RecordCommand pattern: mutex-lock + TrimSpace + ignore empty"

key-files:
  created:
    - internal/app/cmd/shell_ami_test.go
  modified:
    - pkg/allowlistgen/recorder.go
    - pkg/allowlistgen/generator.go
    - pkg/allowlistgen/generator_test.go
    - internal/app/cmd/shell.go
    - internal/app/cmd/shell_learn_test.go
    - internal/app/cmd/shell_test.go

key-decisions:
  - "bakeFromSandboxFn/flushEC2ObservationsFn/fetchEC2ObservedJSONFn as package-level vars — simplest injection path for package cmd internal tests without interface proliferation"
  - "shell_ami_test.go in package cmd (not cmd_test) — required to access unexported runLearnPostExit and package-level vars"
  - "GenerateProfileFromJSON third param amiID string — additive, all existing callers updated to pass empty string"
  - "bake fires BEFORE flush (CONTEXT.md locked decision) — snapshot captures operator-shaped state; flush runs after against stable instance"
  - "Bake failure exits non-zero but still writes profile — operator is unblocked; bakeErr wraps original for context"

# Metrics
duration: 45min
completed: 2026-04-26
---

# Phase 56 Plan 05: km shell --learn --ami Integration Summary

**--ami flag wired onto km shell --learn; BakeFromSandbox fires before eBPF flush; GenerateProfileFromJSON propagates AMI ID into spec.runtime.ami via Recorder; 13 new tests (6 allowlistgen + 7 shell) all passing**

## Performance

- **Duration:** ~45 min
- **Started:** 2026-04-26T20:57:00Z
- **Completed:** 2026-04-26T21:10:00Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments

### Task 1: RecordAMI + Generate emits Spec.Runtime.AMI

- `amiID string` field added to `Recorder` struct (unexported, mutex-protected)
- `RecordAMI(amiID string)` — trims whitespace, ignores empty/whitespace-only, mutex-locked
- `AMI() string` accessor — mutex-locked read
- `Generate()` emits `p.Spec.Runtime.AMI` after the InitCommands block when `r.AMI()` is non-empty
- `GenerateAnnotatedYAML()` inherits AMI automatically (calls `Generate` internally)
- 6 tests: `TestRecordAMI_StoresValue`, `TestRecordAMI_TrimsAndIgnoresEmpty`, `TestGenerate_WithAMI`, `TestGenerate_WithoutAMI_DoesNotSetField`, `TestGenerateAnnotatedYAML_WithAMI_RoundTripsThroughYAML`, `TestGenerate_WithAMIAndInitCommands_BothPresent`

### Task 2: --ami flag + runLearnPostExit integration

- `--ami` boolean flag registered on `km shell` (requires `--learn`, returns clear error otherwise)
- `runLearnPostExit` gains `bakeAMI bool` parameter
- Bake step in EC2 branch fires BEFORE `flushEC2ObservationsFn` (CONTEXT.md locked order)
- Three injectable package-level vars: `bakeFromSandboxFn`, `flushEC2ObservationsFn`, `fetchEC2ObservedJSONFn`
- Docker/ECS substrates: `--ami` silently skipped with a warning log (no failure)
- Bake failure: profile written without `ami:` field, `runLearnPostExit` returns non-nil error wrapping original
- `GenerateProfileFromJSON` signature updated: `(data []byte, base, amiID string) ([]byte, error)` — calls `rec.RecordAMI(amiID)` when non-empty
- All 5 existing call sites updated to pass `amiID=""`: `shell_test.go` (3 sites), `shell_learn_test.go` (2 sites)
- 7 tests in `shell_ami_test.go`: call-order assertion, AMI injection into YAML, bake failure path, no-ami regression, docker substrate skip, `--ami`-without-`--learn` error, `GenerateProfileFromJSON` direct test

## Public Surface Changes

```go
// pkg/allowlistgen — package allowlistgen
func (r *Recorder) RecordAMI(amiID string)  // new
func (r *Recorder) AMI() string              // new

// internal/app/cmd — package cmd (signature change, third param added)
func GenerateProfileFromJSON(data []byte, base, amiID string) ([]byte, error)
```

## Substrate Behavior Table

| Substrate | --ami=true | Notes |
|-----------|-----------|-------|
| ec2 / ec2spot / ec2demand | AMI baked BEFORE flush | CONTEXT.md locked ordering |
| docker | Skipped with warning | AMI requires EC2 instance |
| ecs | Skipped with warning | AMI requires EC2 instance |

## GenerateProfileFromJSON Call Sites Enumerated (per plan step 1)

Before the signature change, all callers were grepped:
1. `internal/app/cmd/shell.go:511` — definition (updated)
2. `internal/app/cmd/shell.go:671` — internal call in `runLearnPostExit` (updated to pass `amiID`)
3. `internal/app/cmd/shell_learn_test.go:64` — test (updated to pass `""`)
4. `internal/app/cmd/shell_learn_test.go:89` — test (updated to pass `""`)
5. `internal/app/cmd/shell_test.go:393` — test (updated to pass `""`)
6. `internal/app/cmd/shell_test.go:416` — test (updated to pass `""`)
7. `internal/app/cmd/shell_test.go:438` — test (updated to pass `""`)

Total: 5 test call sites + 1 production call site. Well below the 5-caller gate for split.

## Bake Failure Semantics

When `--ami` is passed and `BakeFromSandbox` returns an error:
1. Warning is logged: `"AMI bake failed — generating profile without ami field"`
2. eBPF flush continues (profile generation is not blocked)
3. Profile YAML is written to `learnOutput` without the `ami:` field
4. `runLearnPostExit` returns `fmt.Errorf("AMI bake failed: %w (profile generated without ami field)", bakeErr)`
5. Process exits non-zero

## Test Count and Status

**Task 1 — pkg/allowlistgen: 6 new tests, all passing**
1. TestRecordAMI_StoresValue — PASS
2. TestRecordAMI_TrimsAndIgnoresEmpty — PASS
3. TestGenerate_WithAMI — PASS
4. TestGenerate_WithoutAMI_DoesNotSetField — PASS
5. TestGenerateAnnotatedYAML_WithAMI_RoundTripsThroughYAML — PASS
6. TestGenerate_WithAMIAndInitCommands_BothPresent — PASS

**Task 2 — internal/app/cmd: 7 new tests, all passing**
1. TestRunLearnPostExit_AMIFlag_BakesBeforeFlush — PASS (verifies CONTEXT.md timing decision)
2. TestRunLearnPostExit_AMIFlag_InjectsAMIIntoGeneratedYAML — PASS
3. TestRunLearnPostExit_AMIFlag_BakeFailureWritesProfileWithoutAMI — PASS
4. TestRunLearnPostExit_NoAMIFlag_BehavesAsBefore — PASS (Phase 55 regression guard)
5. TestRunLearnPostExit_AMIFlag_DockerSubstrate_SkippedWithWarning — PASS
6. TestNewShellCmd_AMIFlagWithoutLearn_Errors — PASS
7. TestGenerateProfileFromJSON_WithAMIID_EmitsRuntimeAMI — PASS

## Task Commits

1. **Task 1: recorder.go + generator.go + generator_test.go** — `caf08cb` (feat)
2. **Task 2: shell.go + shell_learn_test.go + shell_test.go** — `83c4101` (feat)

## Files Created/Modified

- `pkg/allowlistgen/recorder.go` — added `amiID` field, `RecordAMI()`, `AMI()`
- `pkg/allowlistgen/generator.go` — `Generate()` emits `Spec.Runtime.AMI` when recorder has amiID
- `pkg/allowlistgen/generator_test.go` — 6 new tests
- `internal/app/cmd/shell.go` — `--ami` flag, `runLearnPostExit` bakeAMI param, injectable vars, `GenerateProfileFromJSON` signature
- `internal/app/cmd/shell_learn_test.go` — updated 2 call sites with `amiID=""`
- `internal/app/cmd/shell_test.go` — updated 3 call sites with `amiID=""`
- `internal/app/cmd/shell_ami_test.go` — 7 tests (file pre-exists from Phase 56-06 pre-commit)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] fetchEC2ObservedJSON needed injectable var for test isolation**
- **Found during:** Task 2 test writing
- **Issue:** Without a way to inject a stub for `fetchEC2ObservedJSON`, tests for `runLearnPostExit` would fail with "no artifacts bucket" or real S3 calls, making Tests 2-4 non-functional
- **Fix:** Added `fetchEC2ObservedJSONFn = fetchEC2ObservedJSON` as a third injectable package-level var; updated the EC2 branch in `runLearnPostExit` to use `fetchEC2ObservedJSONFn`
- **Files modified:** `internal/app/cmd/shell.go`

### Pre-existing Failures (out of scope, logged to deferred-items.md)

- `TestShellCmd_MissingInstanceID` — pre-existing design issue (`_ = runShell(...)` discards error by design); unrelated to Plan 56-05
- `TestUnlockCmd_RequiresStateBucket` — pre-existing AWS SSO credentials test flakiness

## Operator Action Item

After merging Phase 56, run `make build` to refresh the local km binary. The new `--ami` flag on `km shell` is not reachable until the binary is rebuilt.

**Usage example:**
```bash
km shell --learn --ami sb-abc123
# → bakes AMI, generates learned.sb-abc123.YYYYMMDDHHMMSS.yaml with:
#   spec:
#     runtime:
#       ami: ami-xxxxxxxxxxxxxxx
#     execution:
#       initCommands: [...]   # Phase 55 commands preserved
```

## Self-Check

Files exist:
- `pkg/allowlistgen/recorder.go` — confirmed (modified)
- `pkg/allowlistgen/generator.go` — confirmed (modified)
- `pkg/allowlistgen/generator_test.go` — confirmed (modified)
- `internal/app/cmd/shell.go` — confirmed (modified)
- `internal/app/cmd/shell_ami_test.go` — confirmed (exists, pre-committed in 56-06)

Commits exist:
- `caf08cb` — Task 1 feat commit
- `83c4101` — Task 2 feat commit

## Self-Check: PASSED

---
*Phase: 56-learn-mode-ami-snapshot-and-lifecycle-management*
*Completed: 2026-04-26*
