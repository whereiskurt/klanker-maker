---
phase: 55-learn-mode-command-capture
verified: 2026-04-18T13:30:00Z
status: passed
score: 9/9 must-haves verified
---

# Phase 55: Learn Mode Command Capture Verification Report

**Phase Goal:** Extend learn mode to capture shell commands executed by both root and sandbox users during `km shell --learn` sessions. Commands are included in the observed state JSON alongside DNS/hosts/repos, and appear in the generated profile output as `spec.execution.initCommands` suggestions. Captures via bash PROMPT_COMMAND history logging or eBPF exec tracing, covering both interactive and scripted commands.
**Verified:** 2026-04-18T13:30:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| #  | Truth                                                                                          | Status     | Evidence                                                                                      |
|----|-----------------------------------------------------------------------------------------------|------------|-----------------------------------------------------------------------------------------------|
| 1  | Recorder.RecordCommand() deduplicates commands while preserving first-seen order              | VERIFIED   | `pkg/allowlistgen/recorder.go` lines 140-151; dual map+slice structure; 6 passing tests      |
| 2  | Recorder.Commands() returns ordered slice of unique commands (non-nil when empty)             | VERIFIED   | `recorder.go` lines 157-163; returns copy of `commandOrdered`; TestRecordCommand_EmptySliceNotNil passes |
| 3  | Generator populates InitCommands in profile from recorded commands                            | VERIFIED   | `pkg/allowlistgen/generator.go` lines 117-120; `Generate()` sets `p.Spec.Execution.InitCommands = cmds`; TestGenerateWithCommands passes |
| 4  | GenerateAnnotatedYAML emits command comment block with suggested initCommands                 | VERIFIED   | `generator.go` lines 199-204; emits "# Commands observed (suggested initCommands — review before use):"; TestGenerateAnnotatedYAMLWithCommands passes |
| 5  | observedState and learnObservedState both carry Commands field (JSON omitempty)               | VERIFIED   | `ebpf_attach.go` line 134; `shell.go` line 440; both have `Commands []string \`json:"commands,omitempty"\`` |
| 6  | GenerateProfileFromJSON feeds Commands from JSON into Recorder via RecordCommand              | VERIFIED   | `shell.go` lines 466-468; iterates `state.Commands` calling `rec.RecordCommand(cmd)`; TestGenerateProfileFromJSONWithCommands passes |
| 7  | userdata.go creates learn-commands.log (chmod 666) and installs _km_learn PROMPT_COMMAND hook when LearnMode=true | VERIFIED | `userdata.go` lines 421-497; conditional `{{- if .LearnMode }}` guard; TestUserDataLearnCommandsLog and TestUserDataLearnCommandsLogAbsent pass |
| 8  | flushObservedState reads /run/km/learn-commands.log and merges commands into recorder before building state | VERIFIED | `ebpf_attach.go` lines 527-531; calls `readLearnCommands` and feeds into `recorder.RecordCommand`; `recorder.Commands()` then used at line 537 |
| 9  | Docker learn path reads audit-log container logs and parses command events via ParseAuditLogCommands | VERIFIED | `shell.go` lines 568-592; reads `km-{id}-audit-log` container, passes to `CollectDockerObservations` with `auditBuf`; TestCollectDockerObservationsWithAuditLogs passes |

**Score:** 9/9 truths verified

### Required Artifacts

| Artifact                                        | Provides                                                            | Status     | Details                                                                                        |
|-------------------------------------------------|---------------------------------------------------------------------|------------|-----------------------------------------------------------------------------------------------|
| `pkg/allowlistgen/recorder.go`                  | RecordCommand and Commands methods on Recorder                      | VERIFIED   | Lines 140-163; `func (r *Recorder) RecordCommand` and `func (r *Recorder) Commands()` exist; substantive; used by generator and integration layer |
| `pkg/allowlistgen/generator.go`                 | InitCommands population and command annotation comments             | VERIFIED   | Lines 117-120, 199-204; `InitCommands` populated; annotated YAML block emitted                |
| `pkg/allowlistgen/recorder_test.go`             | Unit tests for command recording                                    | VERIFIED   | Lines 84-175; 6 TestRecordCommand_* functions all passing                                     |
| `pkg/allowlistgen/generator_test.go`            | Unit tests for command generation output                            | VERIFIED   | Lines 241-331; 5 TestGenerate*Command* functions all passing                                  |
| `internal/app/cmd/ebpf_attach.go`               | observedState.Commands, readLearnCommands, flushObservedState       | VERIFIED   | Lines 134, 137-169, 523-538; Commands field, helper, and flush wiring all present             |
| `internal/app/cmd/shell.go`                     | learnObservedState.Commands, GenerateProfileFromJSON, ParseAuditLogCommands, CollectDockerObservations | VERIFIED | Lines 440, 466-468, 484-500, 508-523, 568-592; all functions substantive and wired |
| `pkg/compiler/userdata.go`                      | learn-commands.log file creation and _km_learn PROMPT_COMMAND hook  | VERIFIED   | Lines 421-497; conditional on `.LearnMode`; correctly guards both file creation and hook install |

### Key Link Verification

| From                              | To                                    | Via                                                          | Status      | Details                                                           |
|-----------------------------------|---------------------------------------|--------------------------------------------------------------|-------------|-------------------------------------------------------------------|
| `pkg/allowlistgen/generator.go`   | `pkg/allowlistgen/recorder.go`        | `r.Commands()` call in `Generate` and `GenerateAnnotatedYAML` | WIRED      | `r.Commands()` called at lines 117 and 168 of generator.go       |
| `internal/app/cmd/ebpf_attach.go` | `pkg/allowlistgen/recorder.go`        | `recorder.Commands()` in `flushObservedState`                | WIRED       | Line 537: `Commands: recorder.Commands()`                         |
| `internal/app/cmd/shell.go`       | `pkg/allowlistgen/recorder.go`        | `rec.RecordCommand()` in `GenerateProfileFromJSON`           | WIRED       | Line 467: `rec.RecordCommand(cmd)` in loop over `state.Commands`  |
| `pkg/compiler/userdata.go`        | `/run/km/learn-commands.log`          | PROMPT_COMMAND `_km_learn` hook writes to log file           | WIRED       | Lines 423-424 create file; line 492 appends to it inside `_km_learn` |
| `internal/app/cmd/shell.go`       | `pkg/compiler/compose.go`             | audit-log container name `km-%s-audit-log` matches template  | WIRED       | Line 570 uses `fmt.Sprintf("km-%s-audit-log", sandboxID)` matching compose template |

### Requirements Coverage

| Requirement | Source Plan | Description (derived from PLAN)                                       | Status      | Evidence                                                    |
|-------------|-------------|-----------------------------------------------------------------------|-------------|-------------------------------------------------------------|
| LEARN-CMD-01 | 55-01      | RecordCommand/Commands API with dedup and first-seen order            | SATISFIED   | recorder.go lines 140-163; 6 passing tests                  |
| LEARN-CMD-02 | 55-01      | Generator populates InitCommands from recorded commands               | SATISFIED   | generator.go lines 117-120; TestGenerateWithCommands passes  |
| LEARN-CMD-03 | 55-01      | GenerateAnnotatedYAML emits command comment block                     | SATISFIED   | generator.go lines 199-204; TestGenerateAnnotatedYAMLWithCommands passes |
| LEARN-CMD-04 | 55-02      | observedState/learnObservedState carry Commands field; JSON round-trip works | SATISFIED | Both structs verified at ebpf_attach.go:134 and shell.go:440 |
| LEARN-CMD-05 | 55-02      | GenerateProfileFromJSON feeds commands into Recorder                  | SATISFIED   | shell.go lines 466-468; TestGenerateProfileFromJSONWithCommands passes |
| LEARN-CMD-06 | 55-02      | Docker path parses audit-log container; ParseAuditLogCommands works   | SATISFIED   | shell.go lines 484-523, 568-592; TestParseAuditLogCommands passes |
| LEARN-CMD-07 | 55-02      | userdata creates learn-commands.log and _km_learn hook for EC2        | SATISFIED   | userdata.go lines 421-497; TestUserDataLearnCommandsLog/Absent pass |

**Note on REQUIREMENTS.md:** The LEARN-CMD-01 through LEARN-CMD-07 IDs appear only in PLAN frontmatter. The central `.planning/REQUIREMENTS.md` does not contain these entries. This is a documentation gap in REQUIREMENTS.md, not a functional issue — the requirements are fully implemented and tested. No orphaned Phase 55 requirements were found in REQUIREMENTS.md because that file does not reference Phase 55 at all.

### Anti-Patterns Found

No anti-patterns found in modified files. No TODO/FIXME/HACK/PLACEHOLDER comments in command or learn-related code paths. No stub implementations. No empty handlers.

### Build Verification

`make build` succeeds: `Built: km v0.1.364 (8ce2417)`.

### Test Execution Results

All tests pass:
- `go test ./pkg/allowlistgen/... -run "TestRecordCommand|TestGenerate.*Command"` — 11 tests PASS
- `go test ./internal/app/cmd/... -run "TestLearnObservedState|TestGenerateProfileFromJSON|TestParseAuditLog|TestCollectDockerObservationsWith"` — 6 tests PASS
- `go test ./pkg/compiler/... -run "TestUserDataLearnCommands"` — 2 tests PASS

### Commit Verification

All documented commit hashes confirmed in git log:
- `b1b73c6` — feat(55-01): add RecordCommand/Commands to Recorder
- `d0834a0` — feat(55-01): extend Generator to emit commands in InitCommands and annotated YAML
- `6f6a11c` — feat(55-02): add Commands field to state structs, wire GenerateProfileFromJSON
- `f5ddf6a` — feat(55-02): add ParseAuditLogCommands, extend CollectDockerObservations for command capture
- `f3042a7` — feat(55-02): add learn-commands.log PROMPT_COMMAND hook to userdata template

### Human Verification Required

#### 1. EC2 end-to-end learn session command capture

**Test:** Run `km create profiles/learn.yaml` to provision an EC2 sandbox, connect via `km shell --learn <id>`, execute several commands (e.g. `apt install curl`, `git clone ...`), exit the shell, and inspect the generated `observed-profile.yaml`.
**Expected:** The generated profile contains an `initCommands` section listing the commands executed, with deduplication and first-seen order preserved.
**Why human:** Requires live EC2 sandbox, SSM session, SIGUSR1 flush, and S3 round-trip — cannot be verified programmatically.

#### 2. Docker substrate end-to-end command capture

**Test:** Run `km create` with a Docker substrate and `--learn`, execute commands inside the sandbox, exit, and inspect the generated profile.
**Expected:** Commands from the `km-{id}-audit-log` container appear in the generated profile's `initCommands`.
**Why human:** Requires a running Docker daemon and sandbox containers; cannot be verified without a real environment.

#### 3. Both root and sandbox user capture

**Test:** In a learn session, run commands as both `root` (sudo) and as the `sandbox` user. Verify both appear in the generated `initCommands`.
**Expected:** Commands from both users captured without duplication of the same command.
**Why human:** Multi-user privilege boundary verification requires a live sandbox environment.

---

_Verified: 2026-04-18T13:30:00Z_
_Verifier: Claude (gsd-verifier)_
