---
phase: 30-sandbox-lifecycle-commands-km-pause-km-lock-km-unlock
verified: 2026-03-28T00:00:00Z
status: passed
score: 14/14 must-haves verified
re_verification: false
---

# Phase 30: Sandbox Lifecycle Commands Verification Report

**Phase Goal:** Working km pause, km lock, and km unlock commands with lock enforcement guards
**Verified:** 2026-03-28
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| #  | Truth                                                                 | Status     | Evidence                                                                                   |
|----|-----------------------------------------------------------------------|------------|--------------------------------------------------------------------------------------------|
| 1  | km pause --remote dispatches EventBridge 'pause' event with correct sandbox ID | VERIFIED | `TestPauseCmd_RemotePublishesCorrectEvent` passes; `PublishSandboxCommand(ctx, sandboxID, "pause")` confirmed in pause.go:49 |
| 2  | km pause calls EC2 StopInstances with Hibernate=true on tagged instances | VERIFIED | pause.go:94 `Hibernate: aws.Bool(true)` in StopInstances call; filter by `tag:km:sandbox-id` at pause.go:79 |
| 3  | km pause updates metadata.json status to 'paused'                    | VERIFIED | pause.go:111 `meta.Status = "paused"` followed by PutObject at pause.go:113-118           |
| 4  | SandboxMetadata struct has Locked and LockedAt fields                 | VERIFIED | metadata.go:23-24 `Locked bool` (json:"locked,omitempty") and `LockedAt *time.Time` (json:"locked_at,omitempty") |
| 5  | km lock sets Locked=true and LockedAt in metadata.json               | VERIFIED | lock.go:81-83 `meta.Locked = true; now := time.Now(); meta.LockedAt = &now` then PutObject |
| 6  | km unlock sets Locked=false in metadata.json                         | VERIFIED | unlock.go:92-93 `meta.Locked = false; meta.LockedAt = nil` then PutObject                 |
| 7  | km unlock requires y/N confirmation without --yes                    | VERIFIED | unlock.go:82-89: prompt printed and Scanln used when `!yes`; `--yes` flag skips it        |
| 8  | km destroy on locked sandbox returns 'is locked' error               | VERIFIED | destroy.go:135 `CheckSandboxLock(ctx, cfg, sandboxID)` called at Step 2b before tag discovery; error format: "sandbox %s is locked" |
| 9  | km stop on locked sandbox returns 'is locked' error                  | VERIFIED | stop.go:58 `CheckSandboxLock(ctx, cfg, sandboxID)` at top of runStop before any EC2 calls |
| 10 | km pause on locked sandbox returns 'is locked' error                 | VERIFIED | pause.go:65 `CheckSandboxLock(ctx, cfg, sandboxID)` called after StateBucket check, before EC2 calls |
| 11 | All three new commands are registered in root.go                     | VERIFIED | root.go:68-70: `NewPauseCmd(cfg)`, `NewLockCmd(cfg)`, `NewUnlockCmd(cfg)` all AddCommand'd |
| 12 | README.md command table includes km pause, km lock, km unlock        | VERIFIED | README.md:148-151: all three commands present with descriptions                            |
| 13 | docs/user-manual.md has sections for km pause, km lock, km unlock    | VERIFIED | ToC entries at lines 13-15; full sections at lines 354, 388, 429 with flags and examples  |
| 14 | CLAUDE.md CLI section lists km pause, km lock, km unlock             | VERIFIED | CLAUDE.md:14-16: all three commands listed with descriptions                               |

**Score:** 14/14 truths verified

---

### Required Artifacts

| Artifact                                   | Expected                                          | Status     | Details                                             |
|--------------------------------------------|---------------------------------------------------|------------|-----------------------------------------------------|
| `pkg/aws/metadata.go`                      | SandboxMetadata with Locked bool and LockedAt *time.Time | VERIFIED | Lines 23-24: both fields present with omitempty tags |
| `internal/app/cmd/pause.go`                | km pause command (NewPauseCmd, NewPauseCmdWithPublisher, runPause) | VERIFIED | All three functions exported; 126 lines; substantive implementation |
| `internal/app/cmd/pause_test.go`           | Unit tests for pause --remote path               | VERIFIED | 3 tests: RemotePublishesCorrectEvent, RemotePublishFailure, RemoteInvalidSandboxID — all pass |
| `internal/app/cmd/help/pause.txt`          | Embedded help text for km pause                  | VERIFIED | 16 lines with usage, examples, flags                |
| `internal/app/cmd/lock.go`                 | km lock command (NewLockCmd, CheckSandboxLock)   | VERIFIED | 122 lines; exports NewLockCmd, NewLockCmdWithPublisher, CheckSandboxLock |
| `internal/app/cmd/unlock.go`               | km unlock command (NewUnlockCmd, --yes flag)     | VERIFIED | 109 lines; exports NewUnlockCmd, NewUnlockCmdWithPublisher |
| `internal/app/cmd/lock_test.go`            | Unit tests for lock behavior                     | VERIFIED | 4 tests: RemotePublishesCorrectEvent, RemoteInvalidSandboxID, RequiresStateBucket, CheckSandboxLock_FailOpenEmptyBucket — all pass |
| `internal/app/cmd/unlock_test.go`          | Unit tests for unlock behavior                   | VERIFIED | 3 tests: RemotePublishesCorrectEvent, RemoteInvalidSandboxID, RequiresStateBucket — all pass |
| `internal/app/cmd/help/lock.txt`           | Embedded help text for km lock                   | VERIFIED | 15 lines with usage, examples, flags                |
| `internal/app/cmd/help/unlock.txt`         | Embedded help text for km unlock                 | VERIFIED | File present (verified by ls)                       |
| `internal/app/cmd/root.go`                 | Registration of pause, lock, unlock commands     | VERIFIED | Lines 68-70: NewPauseCmd, NewLockCmd, NewUnlockCmd all registered |
| `README.md`                                | Command table with pause/lock/unlock entries     | VERIFIED | Lines 148-151: three rows added to sandbox lifecycle table |
| `docs/user-manual.md`                      | User-facing docs for pause/lock/unlock           | VERIFIED | Full sections at 354, 388, 429 with ToC entries at 13-15 |
| `CLAUDE.md`                                | CLI quick-reference with pause/lock/unlock       | VERIFIED | Lines 14-16 in CLI section                         |

---

### Key Link Verification

| From                             | To                           | Via                                        | Status   | Details                                                    |
|----------------------------------|------------------------------|--------------------------------------------|----------|------------------------------------------------------------|
| `internal/app/cmd/pause.go`      | `pkg/aws/metadata.go`        | ReadSandboxMetadata + PutObject for status | WIRED    | pause.go:107 `ReadSandboxMetadata`, line 111 `meta.Status = "paused"`, lines 113-118 PutObject |
| `internal/app/cmd/pause.go`      | `ec2.StopInstances`          | Hibernate: aws.Bool(true)                  | WIRED    | pause.go:92-97: StopInstancesInput with `Hibernate: aws.Bool(true)` |
| `internal/app/cmd/lock.go`       | `pkg/aws/sandbox.go`         | ReadSandboxMetadata + PutObject Locked=true | WIRED   | lock.go:71 ReadSandboxMetadata, line 81 `meta.Locked = true`, lines 86-93 PutObject |
| `internal/app/cmd/destroy.go`    | `pkg/aws/sandbox.go`         | ReadSandboxMetadata lock check             | WIRED    | destroy.go:135 `CheckSandboxLock(ctx, cfg, sandboxID)` at Step 2b |
| `internal/app/cmd/root.go`       | `internal/app/cmd/pause.go`  | AddCommand(NewPauseCmd(cfg))               | WIRED    | root.go:68-70 all three AddCommand calls confirmed         |

---

### Requirements Coverage

| Requirement | Source Plan | Description (derived from plan)                                                | Status    | Evidence                                    |
|-------------|-------------|--------------------------------------------------------------------------------|-----------|---------------------------------------------|
| PAUSE-01    | 30-01       | km pause --remote dispatches EventBridge 'pause' event                         | SATISFIED | TestPauseCmd_RemotePublishesCorrectEvent passes; pause.go:49 |
| PAUSE-02    | 30-01       | km pause calls StopInstances with Hibernate=true                               | SATISFIED | pause.go:94 `Hibernate: aws.Bool(true)`      |
| PAUSE-03    | 30-01       | km pause updates metadata.json status to 'paused'                             | SATISFIED | pause.go:111 + PutObject                    |
| LOCK-01     | 30-02       | km lock sets Locked=true and LockedAt in S3 metadata                          | SATISFIED | lock.go:81-83 + PutObject                   |
| LOCK-02     | 30-02       | km destroy/stop/pause blocked when sandbox is locked                           | SATISFIED | CheckSandboxLock called in all three runXxx functions |
| LOCK-03     | 30-02       | km lock registered in root.go; km --help shows lock                           | SATISFIED | root.go:69 `NewLockCmd(cfg)` registered     |
| UNLOCK-01   | 30-02       | km unlock sets Locked=false in S3 metadata, requires confirmation              | SATISFIED | unlock.go:82-89 (confirmation) + lines 92-93 (Locked=false) |
| UNLOCK-02   | 30-02       | km unlock registered in root.go; km --help shows unlock                       | SATISFIED | root.go:70 `NewUnlockCmd(cfg)` registered   |

**Note on REQUIREMENTS.md coverage:** PAUSE-01 through UNLOCK-02 are phase-local requirement IDs defined in plan frontmatter and referenced in ROADMAP.md line 721. They are not yet added to the requirements-to-phase mapping table in REQUIREMENTS.md. This is a documentation gap (the table at the bottom of REQUIREMENTS.md does not include Phase 30 entries) but does not affect goal achievement.

---

### Anti-Patterns Found

No blockers or warnings found. All `return nil` occurrences in the new files are legitimate early-exits (already-locked guard, aborted confirmation, fail-open lock check) — not empty implementations.

| File | Pattern | Assessment |
|------|---------|------------|
| `lock.go` (multiple `return nil`) | Early-exit guards and fail-open | INFO — expected behavior, not stubs |

---

### Human Verification Required

Two behaviors cannot be verified programmatically:

#### 1. EC2 Hibernate API Call

**Test:** Run `km pause <sandbox-id>` against a running EC2 sandbox instance.
**Expected:** Instance transitions to "stopped" state with hibernate flag; RAM state preserved on EBS if hibernation-enabled; falls back to normal stop if not hibernate-enabled.
**Why human:** Requires a live EC2 instance; the unit tests only cover the --remote EventBridge path.

#### 2. Lock State Persists Across km Commands

**Test:** Run `km lock <sandbox-id>`, then attempt `km stop <sandbox-id>` or `km destroy <sandbox-id>`.
**Expected:** stop and destroy both return "sandbox ... is locked" error. Run `km unlock <sandbox-id>`, confirm with "y", then retry destroy — should proceed normally.
**Why human:** CheckSandboxLock is fail-open (returns nil when StateBucket is empty or AWS unreachable), so live S3 interaction is needed to confirm the guard fires correctly with real metadata.

---

## Build and Test Summary

- `go test ./internal/app/cmd/... -count=1`: **PASS** (75 seconds; all tests green including existing)
- `go build ./cmd/km/`: **PASS** (clean compilation)
- Tests added in this phase: 10 tests across pause_test.go, lock_test.go, unlock_test.go — all pass
- No regressions in any existing command tests

---

## Gaps Summary

No gaps. All 14 must-have truths are verified. All artifacts exist, are substantive, and are wired. All 8 requirement IDs claimed in plan frontmatter are satisfied by the codebase. The binary builds cleanly and all 10 new tests pass without regressions.

The only open item is the documentation gap in REQUIREMENTS.md where Phase 30 is not listed in the requirements-to-phase mapping table. This is administrative and does not affect the working state of the commands.

---

_Verified: 2026-03-28_
_Verifier: Claude (gsd-verifier)_
