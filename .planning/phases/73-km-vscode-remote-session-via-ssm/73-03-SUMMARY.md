---
phase: 73-km-vscode-remote-session-via-ssm
plan: 03
subsystem: ssh
tags: [ssh, config, atomic-write, parser, tdd]

# Dependency graph
requires:
  - phase: 73-km-vscode-remote-session-via-ssm
    provides: Wave 0 stubs for UpsertHost and RemoveHost in sshconfig.go
provides:
  - Production UpsertHost: four insertion scenarios (no-file, no-markers, has-alias, no-alias)
  - Production RemoveHost: idempotent removal with marker cleanup when block empties
  - atomicWrite: temp-file + rename safe file modification helper
  - managedSections: splits ~/.ssh/config into before/inside/after regions
  - parseHostBlocks: splits inside region into per-alias Host blocks
  - renderHostBlock: canonical SSH Host entry with fixed IdentitiesOnly/StrictHostKeyChecking/etc. defaults
  - Nine passing TestSSHConfig_* tests covering all behavioral invariants
affects:
  - 73-06 (km vscode start calls UpsertHost)
  - 73-07 (km destroy calls RemoveHost)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Managed-block parser: split file into before/inside/after; parse inside into Host blocks; reassemble"
    - "Atomic write: CreateTemp in same dir + Rename (same-filesystem guarantee)"
    - "Fixed SSH defaults (IdentitiesOnly, StrictHostKeyChecking, UserKnownHostsFile, ServerAliveInterval) baked into renderHostBlock, not exposed as HostOptions fields"
    - "TDD: production code and tests activated together; all tests green before commit"

key-files:
  created: []
  modified:
    - internal/app/cmd/sshconfig.go
    - internal/app/cmd/sshconfig_test.go

key-decisions:
  - "Atomic write uses os.CreateTemp + os.Rename (not ioutil.WriteFile) for existing-file modification; new-file creation uses os.WriteFile directly — simpler and correct since there is nothing to atomically replace"
  - "managedSections uses bytes.SplitAfter to preserve trailing newlines per line, avoiding off-by-one errors in region boundaries"
  - "HostBlock parsing accumulates indented continuation lines until the next Host line or EOF — no look-ahead needed"
  - "RemoveHost drops both markers when the last block is removed; before/after content is joined with newline separator only when both are non-empty"
  - "Fixed SSH defaults (IdentitiesOnly yes, StrictHostKeyChecking no, UserKnownHostsFile /dev/null, ServerAliveInterval 30) are locked into renderHostBlock per 73-CONTEXT.md POC lessons — not operator-configurable"

patterns-established:
  - "SSH config managed-block: delimited by beginMarker/endMarker constants; content outside markers is never read for parsing decisions and never modified"
  - "Idempotency: RemoveHost returns nil for missing file, missing markers, or missing alias — no write occurs"
  - "Test fixtures use writeFixture helper writing canonical SSH config strings to t.TempDir()"

requirements-completed:
  - GOAL-2
  - GOAL-6
  - GOAL-7

# Metrics
duration: 9min
completed: 2026-05-07
---

# Phase 73 Plan 03: SSH Config Managed-Block Parser/Writer Summary

**Atomic ~/.ssh/config managed-block parser with UpsertHost/RemoveHost, temp+rename writes, and nine passing tests covering all four insertion and three removal scenarios**

## Performance

- **Duration:** 9 min
- **Started:** 2026-05-07T23:50:53Z
- **Completed:** 2026-05-07T23:59:44Z
- **Tasks:** 3
- **Files modified:** 2

## Accomplishments
- Replaced Wave 0 stubs with production UpsertHost and RemoveHost implementations (263 lines)
- managedSections splits the file into three regions using bytes.SplitAfter, preserving trailing newlines exactly
- atomicWrite uses os.CreateTemp + os.Rename for corruption-safe file modification on macOS/Linux
- All nine TestSSHConfig_* tests pass green with no SKIPs; covers byte-for-byte preservation of outside-marker content

## Task Commits

Each task was committed atomically:

1. **Task 1+2: Implement UpsertHost + RemoveHost in sshconfig.go** - `d0c5101` (feat)
2. **Task 3: Activate nine TestSSHConfig tests** - `5381743` (test)

## Files Created/Modified
- `internal/app/cmd/sshconfig.go` - Production UpsertHost, RemoveHost, managedSections, parseHostBlocks, renderHostBlock, atomicWrite, ensureTrailingNewline (replaces Wave 0 stub)
- `internal/app/cmd/sshconfig_test.go` - Nine fixture-driven tests; t.Skip dropped; writeFixture helper added

## Decisions Made
- Wrote both UpsertHost and RemoveHost in a single file pass (they share all helpers) and committed together as Task 1+2 rather than making two commits to the same file with no intermediate green state
- atomicWrite creates temp file in the same directory as the target so Rename is always on the same filesystem (POSIX atomic guarantee)
- New-file path uses os.WriteFile (not the temp+rename dance) because there is nothing to atomically replace
- Fixed SSH defaults not exposed as HostOptions fields — they are constants embedded in renderHostBlock per the locked interface spec

## Deviations from Plan

None - plan executed exactly as written. Tasks 1 and 2 both modify sshconfig.go and share all helpers; they were implemented in a single pass and committed together.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- `UpsertHost(configPath, alias, opts)` is ready for Plan 73-06 (`km vscode start`) to call
- `RemoveHost(configPath, alias)` is ready for Plan 73-07 (`km destroy`) to call
- Both functions are in `internal/app/cmd/sshconfig.go`, package `cmd`
- No blockers or concerns

---
*Phase: 73-km-vscode-remote-session-via-ssm*
*Completed: 2026-05-07*
