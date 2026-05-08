---
phase: 73-km-vscode-remote-session-via-ssm
plan: "04"
subsystem: compiler
tags: [userdata, vscode, ssh, sshd, authorized_keys, selinux, restorecon, ec2, cloud-init]

requires:
  - phase: 73-00
    provides: profile.IsVSCodeEnabled helper in pkg/profile/types.go
  - phase: 73-02
    provides: Wave 0 VSCode test stubs in userdata_test.go

provides:
  - NetworkConfig.VSCodeSSHPubKey field in service_hcl.go
  - userDataParams.VSCodeEnabled + VSCodeSSHPubKey fields in userdata.go
  - generateUserData validation when VSCodeEnabled=true + pubkey empty (Pitfall 4)
  - Conditional cloud-init bash block for sshd + authorized_keys + restorecon
  - Four passing TestUserDataVSCode* tests (no skips)

affects:
  - 73-05 (create.go pubkey wiring reads NetworkConfig.VSCodeSSHPubKey)
  - 73-06 (SSH config for RemoteForward reads from same pubkey path)

tech-stack:
  added: []
  patterns:
    - "Loud-fail validation: EC2 compile path (non-nil network) validates VSCodeEnabled+pubkey before template render"
    - "Template column-0 rule: heredoc variables at column 0 to prevent sshd key rejection"
    - "Defensive restorecon: command -v restorecon guard for cross-distro compatibility"

key-files:
  created: []
  modified:
    - pkg/compiler/service_hcl.go
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go
    - pkg/compiler/compiler_test.go

key-decisions:
  - "Validation scoped to non-nil network: when network==nil (ECS/Docker/tests), skip VSCodeSSHPubKey check to avoid breaking 66+ existing tests"
  - "VSCodeSSHPubKey at column 0 in template heredoc: prevents sshd silent rejection of keys with leading whitespace"
  - "restorecon wrapped with command -v: defensively handles Ubuntu AMIs that lack policycoreutils"
  - "testNetwork() helper updated with VSCodeSSHPubKey: ensures all compiler_test.go EC2 tests satisfy new validation"

patterns-established:
  - "Phase 73 pubkey flow: sshkey.GenerateAndWrite -> create.go -> NetworkConfig.VSCodeSSHPubKey -> userDataParams -> template"
  - "Template gate: {{- if .VSCodeEnabled }} before any pubkey rendering — ECS/Docker substrates get empty string, template skips"

requirements-completed:
  - GOAL-1
  - GOAL-3

duration: 6min
completed: 2026-05-08
---

# Phase 73 Plan 04: Userdata VSCode Block Summary

**Conditional cloud-init block enabling sshd + ed25519 authorized_keys + SELinux restorecon in EC2 userdata, wired through NetworkConfig and userDataParams with loud-fail validation for missing pubkeys**

## Performance

- **Duration:** 6 min
- **Started:** 2026-05-08T00:02:38Z
- **Completed:** 2026-05-08T00:08:25Z
- **Tasks:** 4
- **Files modified:** 4

## Accomplishments

- Added `VSCodeSSHPubKey string` to `NetworkConfig` in service_hcl.go — the operator-time pubkey bundle mirrors `ArtifactsBucket` semantics
- Added `VSCodeEnabled bool` + `VSCodeSSHPubKey string` to `userDataParams` struct; `generateUserData` populates both from `profile.IsVSCodeEnabled()` and `network.VSCodeSSHPubKey`
- Loud-fail validation (Pitfall 4): EC2 path (non-nil network) returns error when VSCodeEnabled=true and pubkey is empty, with `km init --sidecars` remediation hint
- Conditional template block after SlackInboundEnabled section: enables sshd, creates `~/.ssh/`, writes `authorized_keys` at column 0 inside heredoc (Pitfall 3), applies `restorecon` defensively
- All four `TestUserDataVSCode*` tests pass with zero skips

## Task Commits

1. **Task 1: Add VSCodeSSHPubKey to NetworkConfig** - `ecf54c0` (feat)
2. **Task 2: userDataParams fields + generateUserData validation** - `e100aa5` (feat)
3. **Task 3: Insert conditional bash block into userdata template** - `9d00230` (feat)
4. **Task 4: Activate all four TestUserDataVSCode* tests** - `8dea0aa` (test)

## Files Created/Modified

- `pkg/compiler/service_hcl.go` — `NetworkConfig.VSCodeSSHPubKey string` field added
- `pkg/compiler/userdata.go` — `userDataParams` gains two fields; `generateUserData` gains assignments + validation; template gains `{{- if .VSCodeEnabled }}` block
- `pkg/compiler/userdata_test.go` — Three stubs replaced with real test bodies + fourth `TestUserDataVSCodeMissingKeyErrors` added; EFS tests updated with `VSCodeSSHPubKey`
- `pkg/compiler/compiler_test.go` — `testNetwork()` helper updated with `VSCodeSSHPubKey` so existing EC2 compiler tests satisfy new validation

## Decisions Made

- **Validation scoped to non-nil network path:** When `network==nil` (legacy callers, ECS/Docker, and unit tests that use `nil` network), the VSCodeSSHPubKey validation is skipped. The template's `{{- if .VSCodeEnabled }}` gate still prevents any rendering without a pubkey. This avoids breaking 66+ existing tests that pass `nil` for network.
- **Column-0 placement (Pitfall 3 mitigation):** `{{ .VSCodeSSHPubKey }}` sits at column 0 in the template source string inside the heredoc. Go's `text/template` emits leading whitespace verbatim; sshd silently rejects keys with leading whitespace.
- **Defensive restorecon (Pitfall 5 mitigation):** Wrapped with `command -v restorecon >/dev/null 2>&1 && ... || true` so the cloud-init script doesn't fail on Ubuntu AMIs lacking `policycoreutils`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] EFS test NetworkConfig structs missing VSCodeSSHPubKey**
- **Found during:** Task 2 (adding validation to generateUserData)
- **Issue:** `TestUserDataEFSMount` and `TestUserDataEFSCustomMountPoint` pass non-nil `NetworkConfig` without `VSCodeSSHPubKey`, causing the new validation to fail them
- **Fix:** Added `VSCodeSSHPubKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 km-test-key"` to their network configs
- **Files modified:** `pkg/compiler/userdata_test.go`
- **Verification:** Both EFS tests pass after fix
- **Committed in:** `e100aa5` (Task 2 commit)

**2. [Rule 1 - Bug] compiler_test.go testNetwork() helper missing VSCodeSSHPubKey**
- **Found during:** Task 2 (running full test suite after validation added)
- **Issue:** `testNetwork()` in `compiler_test.go` creates `NetworkConfig` without `VSCodeSSHPubKey`, breaking `TestUserDataPrePushHookPresent` and 2 other EC2 compile tests
- **Fix:** Added `VSCodeSSHPubKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 km-test-key"` to `testNetwork()` helper
- **Files modified:** `pkg/compiler/compiler_test.go`
- **Verification:** All previously passing EC2 compile tests pass after fix
- **Committed in:** `e100aa5` (Task 2 commit)

---

**Total deviations:** 2 auto-fixed (both Rule 1 — direct consequence of new validation)
**Impact on plan:** Both fixes necessary for correctness. No scope creep.

## Issues Encountered

Four pre-existing test failures confirmed to exist before Plan 73-04 (verified via `git stash`):
- `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`
- `TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime`
- `TestUserDataKMTracingServicectlStart`
- `TestGitHubUserDataGITASKPASS`

Documented in `deferred-items.md`. Not regressions from this plan.

## User Setup Required

None — this is a compile-time change. Plan 73-05 wires `create.go` to call `sshkey.GenerateAndWrite` and set `network.VSCodeSSHPubKey` before `Compile()`.

## Next Phase Readiness

- Wave 2 Plan 73-05 can now set `network.VSCodeSSHPubKey` and trust that `generateUserData` renders the `authorized_keys` block correctly
- `{{- if .VSCodeEnabled }}` template gate is in place; ECS/Docker substrates are unaffected
- Pitfalls 3, 4, and 5 from 73-RESEARCH.md are mitigated at the compiler layer

---
*Phase: 73-km-vscode-remote-session-via-ssm*
*Completed: 2026-05-08*
