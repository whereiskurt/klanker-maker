---
phase: 73-km-vscode-remote-session-via-ssm
plan: 05
subsystem: infra
tags: [ssh, vscode, keypair, ed25519, create, operator]

requires:
  - phase: 73-01
    provides: pkg/sshkey.GenerateAndWrite implementation (ed25519 keygen)
  - phase: 73-02
    provides: profile.IsVSCodeEnabled helper + CLISpec.VSCodeEnabled field
  - phase: 73-04
    provides: NetworkConfig.VSCodeSSHPubKey field + userdata template block

provides:
  - Per-sandbox ed25519 keypair generation in km create, gated by profile.IsVSCodeEnabled
  - NetworkConfig.VSCodeSSHPubKey populated before compiler.Compile in both --remote and --local paths
  - ~/.km/keys/sb-<id> and ~/.km/keys/sb-<id>.pub written on operator laptop at create time
  - Stderr confirmation line: "  ✓ VS Code keypair written to ~/.km/keys/sb-<id>"

affects:
  - 73-06 (km vscode start/status — relies on ~/.km/keys/sb-<id> existing after create)
  - 73-08 (integration closeout — verifies end-to-end keypair-to-userdata flow)

tech-stack:
  added: []
  patterns:
    - "Fail-fast before AWS provisioning: keypair generation (Step 6d) runs before compiler.Compile (Step 7), ensuring disk errors abort cleanly with no orphan sandboxes"
    - "Single runCreate code path handles both --remote and --local; keypair is always generated locally on the operator's laptop"

key-files:
  created: []
  modified:
    - internal/app/cmd/create.go

key-decisions:
  - "Inserted keypair generation as Step 6d between Slack resolution (6c) and compiler.Compile (Step 7) to guarantee fail-fast before any AWS resource creation"
  - "create.go has a single runCreate function (not split into runCreateRemote/runCreateLocal), so one insertion point covers both paths"
  - "learn.yaml profile validate failure is pre-existing (Slack schema issue); validated with codex.yaml instead — build passes cleanly"

patterns-established:
  - "Step numbering: Phase-specific steps in runCreate use decimal suffixes (6a, 6b, 6c, 6d) to slot in without renumbering later steps"

requirements-completed:
  - GOAL-1

duration: 7min
completed: 2026-05-08
---

# Phase 73 Plan 05: km create Keypair Generation Summary

**ed25519 keypair generated at ~/.km/keys/sb-<id> in km create Step 6d, pubkey threaded into NetworkConfig.VSCodeSSHPubKey before compiler.Compile embeds it into userdata authorized_keys**

## Performance

- **Duration:** 7 min
- **Started:** 2026-05-08T00:14:14Z
- **Completed:** 2026-05-08T00:21:00Z
- **Tasks:** 1
- **Files modified:** 1

## Accomplishments
- Added `pkg/sshkey` import to `internal/app/cmd/create.go`
- Inserted Step 6d after Slack resolution (Step 6c) and before compiler.Compile (Step 7)
- When `profile.IsVSCodeEnabled(resolvedProfile.Spec.CLI)` is true: generates ed25519 keypair via `sshkey.GenerateAndWrite`, sets `network.VSCodeSSHPubKey`, prints stderr confirmation
- When false: no keypair, no files written, `VSCodeSSHPubKey` stays empty (template block omitted by compiler)
- `make build` succeeds; `codex.yaml` validates clean

## Integration Point

**Function:** `runCreate` (single function, not split into remote/local variants)
**File:** `internal/app/cmd/create.go`
**Insertion:** Between line 510 (end of Slack Step 6c block) and the Step 7 comment
**Line:** 512-530 (after edit)

Both `--remote` (EC2 + management Lambda path) and `--local` (Docker) paths flow through `runCreate`. The keypair is always generated locally on the operator's laptop. The pubkey is already embedded in `artifacts.UserData` by the time `compiler.Compile` returns — the management Lambda receives compiled userdata containing the key, not the key separately.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add Phase 73 keypair generation step to runCreate** - `a3e3680` (feat)

**Plan metadata:** (to be added below after final commit)

## Files Created/Modified
- `internal/app/cmd/create.go` - Added sshkey import + Step 6d keypair generation block (21 lines added)

## Decisions Made
- Keypair generation placed at Step 6d (after Slack, before Compile) to ensure fail-fast before any AWS resource creation
- Single insertion point is sufficient: `runCreate` is a single function, not split into remote/local variants
- `learn.yaml` validate failure confirmed pre-existing before changes (Slack schema properties not in JSON schema validator); used `codex.yaml` for smoke-test validation

## Deviations from Plan

None — plan executed exactly as written. The plan correctly anticipated a single `runCreate` function path and the exact variable names (`sandboxID`, `network`, `resolvedProfile`).

## Issues Encountered

- `km validate profiles/learn.yaml` returns non-zero due to a pre-existing Slack property schema issue (properties like `notifySlackEnabled` not registered in the JSON schema validator). Confirmed pre-existing by stashing changes and verifying same failure on clean HEAD. Used `km validate profiles/codex.yaml` for smoke verification.

## Self-Check: PASSED

- `internal/app/cmd/create.go` — FOUND
- `73-05-SUMMARY.md` — FOUND
- commit `a3e3680` — FOUND

## Next Phase Readiness
- Wave 3 Plan 73-06 (`km vscode start/status`) can rely on `~/.km/keys/sb-<id>` existing for any sandbox created with this updated binary
- Plan 73-08 integration closeout can perform end-to-end verification: create sandbox → check keypair file exists → confirm pubkey appears in userdata authorized_keys block

---
*Phase: 73-km-vscode-remote-session-via-ssm*
*Completed: 2026-05-08*
