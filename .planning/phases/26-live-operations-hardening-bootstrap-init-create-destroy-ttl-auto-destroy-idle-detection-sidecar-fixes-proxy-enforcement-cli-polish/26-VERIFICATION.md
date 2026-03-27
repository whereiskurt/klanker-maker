---
phase: 26-live-operations-hardening
verified: 2026-03-27T06:20:00Z
status: human_needed
score: 11/11 must-haves verified
re_verification:
  previous_status: gaps_found
  previous_score: 10/11
  gaps_closed:
    - "km create populates MaxLifetime in SandboxMetadata — CheckMaxLifetime() enforcement in km extend is now functional for real sandboxes"
  gaps_remaining: []
  regressions: []
human_verification:
  - test: "Verify km completion bash produces a usable tab-completion script"
    expected: "Sourcing 'source <(km completion bash)' in a bash session enables tab completion for km subcommands"
    why_human: "Cannot test interactive tab-completion programmatically"
  - test: "Verify --remote flag end-to-end with live AWS"
    expected: "km destroy --remote <sandbox-id> publishes EventBridge event and the km-ttl-handler Lambda executes the destroy"
    why_human: "Requires live AWS EventBridge + Lambda infrastructure; unit tests only verify mock dispatch"
---

# Phase 26: Live Operations Hardening Verification Report

**Phase Goal:** Harden the platform after extensive live testing (~60 commits). Fix remaining test failures, backfill critical-path test coverage, polish CLI UX (aliases, completion, help text, color), test --remote flag, audit multi-region code, and implement max lifetime cap.
**Verified:** 2026-03-27
**Status:** human_needed (all automated checks pass; 2 items need live environment verification)
**Re-verification:** Yes — after gap closure (Plan 26-05)

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | `go test ./...` passes with no test failures from init ordering or status timestamp | VERIFIED | TestRunInitWithRunnerAllModules PASS, TestStatusCmd_Found PASS — full suite green (all packages ok) |
| 2 | No production code path contains hardcoded us-east-1 (except valid API constraints) | VERIFIED | No hardcoded us-east-1 in destroy.go or doctor.go; remaining instances are valid (Pricing API endpoint, S3 LocationConstraint, CLI flag defaults, monitor hint with defensive fallback) |
| 3 | km extend cannot exceed MaxLifetime cap defined in profile | VERIFIED | create.go line 363: `MaxLifetime: resolvedProfile.Spec.Lifecycle.MaxLifetime`; TestRunCreate_MaxLifetime PASS, TestRunCreate_MaxLifetime_JSON PASS (both subtests); CheckMaxLifetime() enforcement now triggered for real sandboxes |
| 4 | km ls works as alias for km list | VERIFIED | list.go line 37: `Aliases: []string{"ls"}` |
| 5 | km sh works as alias for km shell | VERIFIED | shell.go line 38: `Aliases: []string{"sh"}` |
| 6 | km completion bash outputs valid bash completion script | VERIFIED | root.go lines 73-85: completion subcommand with GenBashCompletion/GenZshCompletion wired |
| 7 | km extend --help and km stop --help show usage examples | VERIFIED | help/extend.txt and help/stop.txt both contain "Examples:" section |
| 8 | Newer commands (extend, stop) use consistent color and formatting | VERIFIED | extend.go and stop.go use ANSI constants for sandbox IDs (green) and progress (yellow) |
| 9 | km destroy --remote dispatches EventBridge event with correct sandbox ID and event type | VERIFIED | TestDestroyCmd_RemotePublishesCorrectEvent PASS — RemoteCommandPublisher interface wired in destroy.go |
| 10 | km extend --remote and km stop --remote dispatch correct EventBridge events | VERIFIED | TestExtendCmd_RemotePublishesCorrectEvent PASS, TestStopCmd_RemotePublishesCorrectEvent PASS |
| 11 | km list shows failed/partial sandboxes with distinct status indicator | VERIFIED | colorizeListStatus() in list.go: "failed" red, "partial"/"killed" yellow, "running" green — 4 list status tests PASS |

**Score:** 11/11 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/cmd/init_test.go` | Fixed module ordering assertion | VERIFIED | Line 74: `["network","dynamodb-budget","dynamodb-identities","s3-replication","ttl-handler","ses"]` |
| `internal/app/cmd/status_test.go` | Fixed timestamp assertion | VERIFIED | Line 141: checks for "2026-03-22" (date only, not RFC3339) |
| `internal/app/cmd/destroy.go` | Dynamic region in monitor hint | VERIFIED | Monitor hint now uses remote_publisher.go which uses cfg.PrimaryRegion with defensive fallback |
| `internal/app/cmd/doctor.go` | Dynamic region fallbacks | VERIFIED | Lines 961, 1027, 1053: all use cfg.GetPrimaryRegion() |
| `pkg/profile/types.go` | MaxLifetime field in LifecycleSpec | VERIFIED | Lines 121-124: `MaxLifetime string` with yaml/json tags |
| `internal/app/cmd/extend.go` | MaxLifetime enforcement | VERIFIED | Lines 97-98, 151-170: CheckMaxLifetime() called and implemented |
| `internal/app/cmd/extend_test.go` | Tests for max lifetime cap | VERIFIED | 4 tests: WithinCap, ExceedsCap, NoCapSet, ExpiredSandboxRespectsCap — all PASS |
| `internal/app/cmd/root.go` | Shell completion subcommand | VERIFIED | Lines 73-85: `completion [bash|zsh]` with GenBashCompletion/GenZshCompletion |
| `internal/app/cmd/list.go` | ls alias + failed/partial status | VERIFIED | Line 37: ls alias; lines 129-155: colorizeListStatus() with failed/partial handling |
| `internal/app/cmd/shell.go` | sh alias | VERIFIED | Line 38: `Aliases: []string{"sh"}` |
| `internal/app/cmd/help/extend.txt` | Extend command help text with examples | VERIFIED | "Examples:" at line 6 |
| `internal/app/cmd/help/stop.txt` | Stop command help text with examples | VERIFIED | "Examples:" at line 7 |
| `internal/app/cmd/destroy_test.go` | Tests for --remote destroy path | VERIFIED | 3 remote tests: RemotePublishesCorrectEvent, RemotePublishFailure, RemoteInvalidSandboxID — all PASS |
| `internal/app/cmd/stop_test.go` | Tests for stop command including --remote | VERIFIED | 3 remote tests all PASS |
| `internal/app/cmd/remote_publisher.go` | RemoteCommandPublisher interface | VERIFIED | Interface + realRemotePublisher implementation |
| `internal/app/cmd/create.go` | Populate MaxLifetime in SandboxMetadata | VERIFIED | Line 363: `MaxLifetime: resolvedProfile.Spec.Lifecycle.MaxLifetime` — confirmed by grep and test suite |
| `internal/app/cmd/create_test.go` | Tests verifying MaxLifetime written to metadata | VERIFIED | TestRunCreate_MaxLifetime (source-level) PASS; TestRunCreate_MaxLifetime_JSON (present_when_set + omitted_when_empty subtests) PASS |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `extend.go` | `pkg/profile/types.go` | `LifecycleSpec.MaxLifetime` field | WIRED | extend.go calls CheckMaxLifetime(meta, ...) which reads meta.MaxLifetime; metadata field exists |
| `destroy.go` | `remote_publisher.go` | `NewDestroyCmdWithPublisher` + `PublishSandboxCommand` | WIRED | destroy.go line 54 delegates to publisher.PublishSandboxCommand |
| `extend.go` | `remote_publisher.go` | `publishRemoteCommand` | WIRED | extend.go line 53: publisher.PublishSandboxCommand with eventType "extend" and duration |
| `list.go` | `pkg/aws/s3.go` | `ListAllSandboxesByS3` | WIRED | list.go line 124: awsSandboxLister.ListSandboxes calls kmaws.ListAllSandboxesByS3 |
| `create.go` | `pkg/aws/metadata.go` | `MaxLifetime` population | WIRED | create.go line 363: `MaxLifetime: resolvedProfile.Spec.Lifecycle.MaxLifetime`; SandboxMetadata.MaxLifetime has json:"max_lifetime,omitempty" tag; test confirms marshal output |

### Requirements Coverage

Phase 26 declares requirements [HARD-01, HARD-02, HARD-03, HARD-04, HARD-05, HARD-06]. These identifiers are referenced only in ROADMAP.md and plan frontmatter — they are not defined in REQUIREMENTS.md. No HARD-0x entries exist in the Traceability table. This is an orphaned requirement class: the IDs are used as plan-internal tracking labels but are not formally defined with descriptions in the requirements document.

| Requirement | Source Plan | Description (inferred from plan objective) | Status |
|-------------|-------------|---------------------------------------------|--------|
| HARD-01 | 26-02 | Fix pre-existing test failures (init ordering, status timestamp) | SATISFIED |
| HARD-02 | 26-03 | CLI UX: shell completion, aliases, help text, consistent colors | SATISFIED |
| HARD-03 | 26-04 | Test and fix --remote flag for destroy/extend/stop | SATISFIED |
| HARD-04 | 26-02 | Audit and fix hardcoded region references | SATISFIED |
| HARD-05 | 26-02 + 26-05 | Implement MaxLifetime cap in km extend; populate from profile at create time | SATISFIED — create.go line 363 sets field; 2 tests confirm; end-to-end chain complete |
| HARD-06 | 26-04 | Failed/partial sandbox status in km list + test backfill | SATISFIED |

**Orphaned requirements note:** HARD-01 through HARD-06 are not defined in REQUIREMENTS.md and have no entry in the Traceability table. They function as internal phase-level tracking IDs only. No REQUIREMENTS.md update was made to formally define or trace them.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/app/cmd/remote_publisher.go` | 45 | `us-east-1` fallback | Info | Defensive fallback for empty cfg.PrimaryRegion in monitor hint (not an API call). Allowed per plan decision. |

No blocker or warning anti-patterns remain. The MaxLifetime dead-letter anti-pattern from initial verification has been resolved.

### Human Verification Required

#### 1. Shell Tab Completion

**Test:** In a bash session, run `source <(km completion bash)`, then type `km ` and press Tab.
**Expected:** Shell offers subcommand completions (list, create, destroy, etc.)
**Why human:** Interactive terminal behavior cannot be verified programmatically.

#### 2. --remote Flag End-to-End

**Test:** With a running sandbox, run `km destroy --remote <sandbox-id>` and monitor CloudWatch logs for the km-ttl-handler Lambda.
**Expected:** EventBridge event published, Lambda receives it, sandbox is torn down, metadata.json deleted.
**Why human:** Requires live AWS EventBridge + Lambda infrastructure. Unit tests only verify mock dispatch — the real EventBridge routing rules and Lambda permissions were established in Phase 26's ad-hoc sprint but are not re-tested here.

### Re-Verification Summary

**Gap closed:** HARD-05 MaxLifetime dead letter.

Plan 26-05 added a single-line fix to `cmd/create.go` line 363:

```go
MaxLifetime: resolvedProfile.Spec.Lifecycle.MaxLifetime,
```

This completes the end-to-end MaxLifetime enforcement chain:
- `pkg/profile/types.go` — `LifecycleSpec.MaxLifetime` field (profile source)
- `cmd/create.go` line 363 — writes value into `SandboxMetadata` at provisioning time (NEW)
- `pkg/aws/metadata.go` — persists as `max_lifetime` JSON field in S3 (`omitempty` handles absent case)
- `cmd/extend.go` — `CheckMaxLifetime()` reads the persisted value and enforces the cap

Two tests were added to `create_test.go`: `TestRunCreate_MaxLifetime` (source-level assertion) and `TestRunCreate_MaxLifetime_JSON` (omitempty marshal semantics — two subtests). All 4 MaxLifetime enforcement tests in `extend_test.go` continue to pass. Full suite (`go test ./... -count=1`) green across all 16 packages.

All 11 observable truths are now fully verified. Phase 26 goal is achieved at the automated verification level. Two human verification items remain (shell completion UX and live AWS --remote path), unchanged from initial verification.

---

_Initial verification: 2026-03-27_
_Re-verification: 2026-03-27 (after Plan 26-05 gap closure)_
_Verifier: Claude (gsd-verifier)_
