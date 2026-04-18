---
phase: 55-learn-mode-command-capture
plan: 02
subsystem: ebpf
tags: [go, learn-mode, shell-commands, ebpf, docker, userdata, tdd, audit-log]

# Dependency graph
requires:
  - phase: 55-learn-mode-command-capture
    plan: 01
    provides: RecordCommand/Commands API on Recorder with dedup and first-seen order
affects:
  - EC2 learn sessions (km shell --learn on ec2/ec2spot substrates)
  - Docker learn sessions (km shell --learn on docker substrate)

provides:
  - observedState.Commands field in ebpf_attach.go (EC2 learn path)
  - readLearnCommands helper reads /run/km/learn-commands.log at flush time
  - learnObservedState.Commands field in shell.go (shared JSON format)
  - GenerateProfileFromJSON feeds JSON commands into Recorder for profile generation
  - ParseAuditLogCommands parses JSON-line audit-log container output for command events
  - CollectDockerObservations accepts auditLogs reader; includes commands in output state
  - userdata.go creates learn-commands.log and installs _km_learn PROMPT_COMMAND hook

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Conditional PROMPT_COMMAND in Go template heredoc via {{ if .LearnMode }} guard"
    - "Tab-separated log format: timestamp\\tuser\\tcommand for learn-commands.log"
    - "Nil-safe io.Reader parameter pattern for optional Docker container log sources"

key-files:
  created: []
  modified:
    - internal/app/cmd/ebpf_attach.go
    - internal/app/cmd/shell.go
    - internal/app/cmd/shell_test.go
    - internal/app/cmd/shell_learn_test.go
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go

key-decisions:
  - "readLearnCommands deduplicates log entries before feeding into Recorder — ensures no double-counting when both PROMPT_COMMAND hook and recorder have same command"
  - "CollectDockerObservations nil-safe auditLogs parameter allows existing callers to pass nil without breaking (updated shell_learn_test.go existing caller)"
  - "Container name fmt.Sprintf('km-%s-audit-log', sandboxID) confirmed against compose.go template km-{{ .SandboxID }}-audit-log before implementation"
  - "Go template directives work inside single-quoted heredocs because template executes first, shell heredoc quoting only affects runtime variable expansion"

patterns-established:
  - "Conditional shell hook pattern: define function + PROMPT_COMMAND in same {{ if .LearnMode }} block inside heredoc"
  - "Audit-log JSON line parsing: struct with EventType/Detail for extensible event routing"

requirements-completed: [LEARN-CMD-04, LEARN-CMD-05, LEARN-CMD-06, LEARN-CMD-07]

# Metrics
duration: 25min
completed: 2026-04-18
---

# Phase 55 Plan 02: Learn Mode Command Capture — EC2/Docker Integration Summary

**End-to-end command capture wired for both EC2 (PROMPT_COMMAND hook + learn-commands.log read at eBPF flush) and Docker (audit-log container JSON-line parsing via ParseAuditLogCommands) learn sessions**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-04-18T12:37:46Z
- **Completed:** 2026-04-18T12:59:56Z
- **Tasks:** 3
- **Files modified:** 6

## Accomplishments
- Added Commands field (json:omitempty) to both observedState (ebpf_attach.go) and learnObservedState (shell.go)
- readLearnCommands parses /run/km/learn-commands.log (timestamp\tuser\tcommand format) with first-seen dedup
- flushObservedState merges learn-commands.log entries into Recorder before building final state snapshot
- GenerateProfileFromJSON feeds Commands from JSON into rec.RecordCommand for profile InitCommands population
- ParseAuditLogCommands reads JSON-line audit-log container output, extracts event_type=="command" entries
- CollectDockerObservations extended to accept auditLogs io.Reader; Docker learn path reads km-{sandboxID}-audit-log container
- userdata.go: creates /run/km/learn-commands.log (chmod 666) and installs _km_learn() PROMPT_COMMAND hook when LearnMode=true

## Task Commits

Each task was committed atomically:

1. **Task 1: Add Commands field to state structs, wire GenerateProfileFromJSON and JSON round-trip** - `6f6a11c` (feat)
2. **Task 2: Add ParseAuditLogCommands, extend CollectDockerObservations for command capture** - `f5ddf6a` (feat)
3. **Task 3: Add learn-commands.log PROMPT_COMMAND hook to userdata template with test** - `f3042a7` (feat)

## Files Created/Modified
- `internal/app/cmd/ebpf_attach.go` - Added Commands to observedState, readLearnCommands helper, flushObservedState reads log
- `internal/app/cmd/shell.go` - Added Commands to learnObservedState, GenerateProfileFromJSON feeds commands, ParseAuditLogCommands, CollectDockerObservations extended with auditLogs parameter + Docker path reads audit-log container
- `internal/app/cmd/shell_test.go` - 3 new tests: TestLearnObservedStateJSONRoundTrip, TestGenerateProfileFromJSONWithCommands, TestGenerateProfileFromJSONNoCommands + 3 Task 2 tests: TestParseAuditLogCommands, TestParseAuditLogCommands_MalformedLines, TestCollectDockerObservationsWithAuditLogs
- `internal/app/cmd/shell_learn_test.go` - Updated existing TestCollectDockerObservations for new 4-argument signature
- `pkg/compiler/userdata.go` - Learn-commands.log creation (chmod 666), _km_learn() function, conditional PROMPT_COMMAND
- `pkg/compiler/userdata_test.go` - 2 new tests: TestUserDataLearnCommandsLog, TestUserDataLearnCommandsLogAbsent

## Decisions Made
- readLearnCommands feeds log commands into Recorder for dedup rather than appending directly — preserves first-seen semantic and avoids duplicates between PROMPT_COMMAND-captured and recorder-captured paths
- CollectDockerObservations nil-safe auditLogs parameter preserves backward compatibility without an adapter shim
- _km_learn uses same history-based command extraction as _km_audit for consistency; writes simpler tab-separated format (not JSON) since it's consumed by readLearnCommands on the operator side, not the audit-log sidecar

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Updated existing TestCollectDockerObservations for new signature**
- **Found during:** Task 2 (extend CollectDockerObservations)
- **Issue:** shell_learn_test.go had existing test calling CollectDockerObservations with 3 args; new 4-arg signature caused build failure
- **Fix:** Added nil as 4th argument to existing test call
- **Files modified:** internal/app/cmd/shell_learn_test.go
- **Verification:** go test ./internal/app/cmd/... -run TestCollectDockerObservations passes
- **Committed in:** f5ddf6a (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - bug fix for updated function signature)
**Impact on plan:** Necessary correctness fix, no scope creep.

## Issues Encountered
- The cmd package full test suite has a pre-existing 60s timeout on heartbeat agent integration tests that causes `go test ./internal/app/cmd/...` to take 60+ seconds. All new tests pass when run with targeted `-run` patterns. Not caused by this plan's changes.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Complete end-to-end command capture implemented for both EC2 and Docker paths
- EC2: PROMPT_COMMAND hook writes to /run/km/learn-commands.log; eBPF enforcer reads and merges at SIGUSR1/shutdown
- Docker: audit-log container JSON lines parsed for command events; included in CollectDockerObservations output
- GenerateProfileFromJSON feeds commands into Recorder; Generator (from Plan 01) populates InitCommands in generated profile YAML
- All requirements LEARN-CMD-04 through LEARN-CMD-07 satisfied

---
*Phase: 55-learn-mode-command-capture*
*Completed: 2026-04-18*
