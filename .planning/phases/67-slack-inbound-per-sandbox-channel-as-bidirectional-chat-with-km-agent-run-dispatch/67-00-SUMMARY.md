---
phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
plan: "00"
subsystem: dependencies-and-test-scaffolding
tags: [wave-0, sqs-sdk, requirements, test-stubs, prerequisites]
dependency_graph:
  requires: []
  provides:
    - go.mod SQS SDK anchor (github.com/aws/aws-sdk-go-v2/service/sqs v1.42.27)
    - REQUIREMENTS.md REQ-SLACK-IN-* entries (8 IDs, Phase 67 traceability)
    - 6 Wave 0 test stub files seeded for Plans 67-01 through 67-08
  affects:
    - go.mod
    - go.sum
    - .planning/REQUIREMENTS.md
    - pkg/slack/bridge/events_handler_test.go
    - pkg/profile/validate_slack_inbound_test.go
    - pkg/compiler/userdata_slack_inbound_test.go
    - internal/app/cmd/create_slack_inbound_test.go
    - internal/app/cmd/destroy_slack_inbound_test.go
    - internal/app/cmd/doctor_slack_inbound_test.go
tech_stack:
  added:
    - github.com/aws/aws-sdk-go-v2/service/sqs v1.42.27 (indirect until Wave 1 imports it directly)
  patterns:
    - Go test stub pattern: t.Skip("Wave 0 stub — Plan 67-NN") for forward-declared test functions
    - Requirements traceability: 8 REQ-SLACK-IN-* IDs registered before implementing plans reference them
key_files:
  created:
    - pkg/slack/bridge/events_handler_test.go
    - pkg/profile/validate_slack_inbound_test.go
    - pkg/compiler/userdata_slack_inbound_test.go
    - internal/app/cmd/create_slack_inbound_test.go
    - internal/app/cmd/destroy_slack_inbound_test.go
    - internal/app/cmd/doctor_slack_inbound_test.go
  modified:
    - go.mod (SQS SDK dep added as indirect)
    - go.sum (SQS package hashes added)
    - .planning/REQUIREMENTS.md (Slack Inbound section + 8 traceability rows)
decisions:
  - SQS dep kept as indirect in go.mod (no production code imports it yet; go mod tidy would prune it — added via go get without tidy to anchor it for Wave 1+ plans)
  - Stub test files use internal package names (package bridge, profile, compiler, cmd) matching the non-_test source files in each directory, consistent with existing builtins_test.go and inherit_test.go patterns in pkg/profile/
metrics:
  duration: 366s
  completed: "2026-05-02"
  tasks: 2
  files: 9
---

# Phase 67 Plan 00: Wave 0 Prerequisites Summary

**One-liner:** SQS SDK v1.42.27 anchored in go.mod and 8 REQ-SLACK-IN-* requirements registered with 6 t.Skip stub test files seeding Wave 1+ compile baseline.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Add SQS SDK dep + register REQUIREMENTS.md | b0e98ab | go.mod, go.sum, REQUIREMENTS.md |
| 2 | Create six test stub files | 9ae756d | 6 test files (all packages) |

## What Was Built

**Task 1 — SQS SDK + Requirements:**

- `github.com/aws/aws-sdk-go-v2/service/sqs v1.42.27` added to go.mod as indirect dependency. It remains indirect until Plan 67-05 imports `sqsiface` directly in `pkg/slack/bridge/aws_adapters.go`. The dep is anchored so `go get` in Wave 1+ plans will find a consistent version rather than resolving @latest again.
- `.planning/REQUIREMENTS.md` gains a new "### Slack Inbound (Bidirectional Chat — Phase 67)" section with 8 `REQ-SLACK-IN-*` bullet definitions (SCHEMA, DDB, EVENTS, DELIVERY, POLLER, LIFECYCLE, OBSERVABILITY, INIT), each with a one-sentence description of what must be true for that requirement to be complete.
- Traceability table updated with 8 new rows mapping each `REQ-SLACK-IN-*` to Phase 67 Planned. Coverage count updated from 81 to 89 total v1 requirements.

**Task 2 — Test Stub Files:**

Six new test files, one per implementing plan's primary package:

| File | Package | Stubs | Target Plan |
|------|---------|-------|-------------|
| `pkg/slack/bridge/events_handler_test.go` | `bridge` | 7 | 67-03, 67-05 |
| `pkg/profile/validate_slack_inbound_test.go` | `profile` | 4 | 67-01 |
| `pkg/compiler/userdata_slack_inbound_test.go` | `compiler` | 4 | 67-04 |
| `internal/app/cmd/create_slack_inbound_test.go` | `cmd` | 4 | 67-06, 67-07 |
| `internal/app/cmd/destroy_slack_inbound_test.go` | `cmd` | 3 | 67-07 |
| `internal/app/cmd/doctor_slack_inbound_test.go` | `cmd` | 3 | 67-08 |

All 25 stub functions use `t.Skip("Wave 0 stub — Plan 67-NN")`. Running `go test -run TestEventsHandler|TestValidate_SlackInbound|...` returns `ok` (not FAIL) for all four packages. Wave 1+ plans replace stub bodies via Edit without needing to create the files from scratch.

## Verification Results

- `go build ./...` — PASS (no errors)
- `grep "service/sqs" go.mod` — matches `v1.42.27 // indirect`
- `grep -c "REQ-SLACK-IN-" .planning/REQUIREMENTS.md` — 16 (8 definitions + 8 traceability rows)
- `go test ./pkg/slack/bridge/... ./pkg/profile/... ./pkg/compiler/... ./internal/app/cmd/... -count=1` — 4 packages `ok`, 0 `FAIL`

## Deviations from Plan

None — plan executed exactly as written. The SQS dep lands as `// indirect` rather than a direct entry because no Go source file imports it yet; this is expected and consistent with Go module conventions. `go mod tidy` in a later plan will promote it to direct once an import exists.

## Wave 0 Readiness

Phase 67 Wave 1+ plans can now:
1. Reference `REQ-SLACK-IN-*` IDs in their `requirements:` frontmatter without "unknown requirement" errors.
2. Run `go test -run TestEventsHandler|TestValidate_SlackInbound|...` for verify steps — stubs return SKIP (green baseline), not compilation errors.
3. Import `github.com/aws/aws-sdk-go-v2/service/sqs` types without a `go get` step (dep is already in go.sum).

## Self-Check: PASSED

- [x] go.mod contains `github.com/aws/aws-sdk-go-v2/service/sqs v1.42.27`
- [x] go.sum contains SQS package hashes
- [x] REQUIREMENTS.md has 16 occurrences of REQ-SLACK-IN- (8 defs + 8 traceability)
- [x] All 6 stub files exist on disk
- [x] Commits b0e98ab and 9ae756d present in git log
- [x] go build ./... passes
- [x] go test returns ok for all 4 target packages
