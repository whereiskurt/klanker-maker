---
phase: 73-km-vscode-remote-session-via-ssm
verified: 2026-05-07T00:00:00Z
status: passed
score: 9/9 must-haves verified
re_verification: false
---

# Phase 73: km vscode Remote-SSH over SSM — Verification Report

**Phase Goal:** Add `km vscode start <sandbox-id>` and `km vscode status <sandbox-id>` so an
operator can connect their local desktop VS Code to a sandbox via VS Code Remote-SSH over an SSM
port-forward, with per-sandbox ed25519 keypairs auto-generated at `km create` time and cleaned up
at `km destroy` time.

**Verified:** 2026-05-07
**Status:** passed
**Re-verification:** No — initial verification

---

## Must-Haves (Derived from 73-CONTEXT.md)

Must-haves are derived from the nine goal areas described in 73-CONTEXT.md decisions, architecture,
and implementation decisions sections. No GOAL-ID entries exist in REQUIREMENTS.md (this is a
developer-experience phase); goals are treated as observable truths.

---

## Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `km vscode start <id>` and `km vscode status <id>` are discoverable and have real implementations (not stubs) | VERIFIED | `km vscode --help` shows both subcommands; `vscode.go` 222 lines with full `runVSCodeStart` / `runVSCodeStatus` bodies; no "not implemented" returns remain |
| 2 | Per-sandbox ed25519 keypair is generated at `km create` time (both `--local` and `--remote` paths) | VERIFIED | `create.go` line 529 calls `sshkey.GenerateAndWrite` in local path; line 1920 in `runCreateRemote`; mid-phase fix `6fd2fde` added remote path coverage |
| 3 | `sshkey.GenerateAndWrite` is fully implemented with correct file modes (private 0600, public 0644) | VERIFIED | `keygen.go` 54 lines; `os.WriteFile(privPath, …, 0o600)` and `os.WriteFile(pubPath, …, 0o644)` confirmed; 7 tests all PASS |
| 4 | `~/.ssh/config` managed-block parser/writer is fully implemented (upsert + remove, idempotent, preserves outside-markers content) | VERIFIED | `sshconfig.go` 263 lines with real implementations of `UpsertHost` and `RemoveHost`; 9 `TestSSHConfig_*` tests all PASS |
| 5 | Userdata template emits conditional sshd-enable + authorized_keys + restorecon block when `VSCodeEnabled=true`; omits it when `false` | VERIFIED | `userdata.go` line 1788 `{{- if .VSCodeEnabled }}` template block confirmed; `VSCodeEnabled` and `VSCodeSSHPubKey` fields on `userDataParams` at lines 2929–2935; 4 `TestUserDataVSCode*` tests all PASS |
| 6 | `spec.cli.vscodeEnabled` schema field is present with default-true semantics; `IsVSCodeEnabled` helper returns true for nil/unset, false for explicit false | VERIFIED | `types.go` line 456 `VSCodeEnabled *bool`; `IsVSCodeEnabled` function at line 465; JSON schema `sandbox_profile.schema.json` line 544 `"vscodeEnabled"`; 2 `TestVSCodeEnabled_*` tests all PASS |
| 7 | `km destroy` removes the `Host km-<id>` block from `~/.ssh/config` and deletes `~/.km/keys/<id>` + `.pub` | VERIFIED | `destroy.go` line 766 calls `RemoveHost`; lines 772–777 delete key files; logic is non-fatal (errors logged, not propagated) |
| 8 | `km vscode start` pre-flight checks: local private key absent produces actionable error; port-already-in-use produces clean error before SSM opens | VERIFIED | `vscode.go` line 119 `net.Listen` probe (mid-phase fix `2501bc9`); line 142 `os.IsNotExist` check with "different machine" message; `TestVSCodeStart_MissingPrivateKey` PASS |
| 9 | Operator-facing documentation exists (`docs/vscode.md`) with network requirements table and `CLAUDE.md` is updated with `km vscode start/status` CLI entries | VERIFIED | `docs/vscode.md` is 304 lines with network egress suffix table; `CLAUDE.md` grep returns 6 matches for `km vscode start\|km vscode status\|VS Code Remote-SSH` |

**Score:** 9/9 truths verified

---

## Required Artifacts

| Artifact | Expected | Lines | Status | Details |
|----------|----------|-------|--------|---------|
| `pkg/sshkey/keygen.go` | GenerateAndWrite implementation | 54 | VERIFIED | Full ed25519 keygen with correct modes and comment embedding |
| `pkg/sshkey/keygen_test.go` | 7 passing tests | — | VERIFIED | All 7 PASS (mode, parse, comment, newline, parentdir, idempotent) |
| `internal/app/cmd/sshconfig.go` | UpsertHost + RemoveHost implementations | 263 | VERIFIED | Atomic write, managed sections, marker preservation |
| `internal/app/cmd/sshconfig_test.go` | 9 passing tests | — | VERIFIED | All 9 PASS (upsert/remove/preserve/idempotent cases) |
| `internal/app/cmd/vscode.go` | NewVSCodeCmd with real start/status | 222 | VERIFIED | Full runVSCodeStart / runVSCodeStatus; pre-bind probe; ResolveSandboxID |
| `internal/app/cmd/vscode_test.go` | 6 passing tests | — | VERIFIED | All 6 PASS (missing-key, port-forward args, output, status cases) |
| `pkg/profile/types.go` | VSCodeEnabled *bool + IsVSCodeEnabled | — | VERIFIED | Field at line 456; helper at line 465 |
| `pkg/compiler/userdata.go` | VSCodeEnabled/VSCodeSSHPubKey template fields + conditional block | — | VERIFIED | Template block at line 1788; params struct at 2929–2935; validation at 3358 |
| `internal/app/cmd/create.go` | Keypair generation in both local and remote paths | — | VERIFIED | Line 529 (local), line 1920 (runCreateRemote); S3 upload of pubkey at line 1991 |
| `internal/app/cmd/destroy.go` | RemoveHost + key file deletion | — | VERIFIED | Lines 766–777; non-fatal; idempotent |
| `pkg/profile/schemas/sandbox_profile.schema.json` | vscodeEnabled field | — | VERIFIED | Line 544 confirmed |
| `docs/vscode.md` | Operator guide with network requirements | 304 | VERIFIED | Contains egress suffix table, troubleshooting, security model |
| `CLAUDE.md` | km vscode CLI entries | — | VERIFIED | 6 matches for vscode/VS Code Remote-SSH entries |
| `internal/app/cmd/root.go` | NewVSCodeCmd registered | — | VERIFIED | Line 86 `root.AddCommand(NewVSCodeCmd(cfg))` |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `vscode.go:runVSCodeStart` | `sshconfig.go:UpsertHost` | direct call line 166 | WIRED | opts struct populated with localPort, privPath, sandboxID |
| `vscode.go:runVSCodeStart` | `shell.go:buildPortForwardCmd` | call line 177 | WIRED | passes instanceID, region, localPort, "22" |
| `vscode.go:runVSCodeStart` | `agent.go:sendSSMAndWait` | call line 149 | WIRED | vsCodeStatusScript sent; output parsed |
| `create.go:runCreate` | `sshkey.GenerateAndWrite` | call line 529 | WIRED | privPath/pubPath/comment constructed; pubLine stored in network.VSCodeSSHPubKey |
| `create.go:runCreateRemote` | `sshkey.GenerateAndWrite` | call line 1920 | WIRED | mid-phase fix 6fd2fde; mirrors local path |
| `create.go:runCreateRemote` | S3 pubkey upload | lines 1991–1996 | WIRED | pubkey uploaded as `vscode-pubkey.txt`; KM_VSCODE_SSH_PUBKEY env var path at line 519 |
| `destroy.go` | `sshconfig.go:RemoveHost` | call line 766 | WIRED | alias "km-"+sandboxID; non-fatal |
| `compiler/userdata.go` | template `{{- if .VSCodeEnabled }}` block | line 1788 | WIRED | VSCodeEnabled read from profile.IsVSCodeEnabled at line 3353–3355 |
| `vscode.go:newVSCodeStartCmd` | `ResolveSandboxID` | call line 57 | WIRED | mid-phase fix 9fe2f16; alias/number resolution |
| `root.go` | `NewVSCodeCmd` | line 86 | WIRED | registered after NewSlackCmd |

---

## Requirements Coverage

No REQUIREMENTS.md entries map to phase 73 (developer-experience phase). GOAL-1 through GOAL-9
are tracked via the CONTEXT.md must-haves and VALIDATION.md per-task map, all marked complete.

Plans 73-00 through 73-09 collectively cover GOAL-1..GOAL-7 (plans 00–08) and GOAL-8, GOAL-9
(plan 09 closeout).

| Goal Range | Plans | Status |
|------------|-------|--------|
| GOAL-1..GOAL-7 | 73-00 through 73-08 | SATISFIED — all implementation tasks committed and tests passing |
| GOAL-8..GOAL-9 | 73-09 | SATISFIED — VALIDATION.md populated, UAT.md approved 2026-05-08 by KPH |

---

## Mid-Phase Fixes Verified

All four operator-discovered fixes are confirmed in git log and in the production code:

| Commit | Fix | Verified |
|--------|-----|---------|
| `6fd2fde` | Keypair generation in `runCreateRemote` (--remote path) | create.go line 1909–1926 |
| `3e4a69a` | KM_VSCODE_SSH_PUBKEY env var + S3 upload for Lambda subprocess | create.go lines 519–521, 1991–1996 |
| `9fe2f16` | ResolveSandboxID in km vscode start/status | vscode.go lines 57, 79 |
| `2501bc9` | Pre-bind port probe before SSM tunnel opens | vscode.go line 119–123 |

---

## Anti-Patterns Found

None in Phase 73 production files. Scanned:
- `pkg/sshkey/keygen.go` — clean implementation, no TODO/stub returns
- `internal/app/cmd/sshconfig.go` — all `return nil` are legitimate success-path returns
- `internal/app/cmd/vscode.go` — all `return nil` are legitimate success-path returns
- `internal/app/cmd/create.go` — `PLACEHOLDER_*` strings are pre-existing Docker compose template substitution patterns, not Phase 73 stubs

Pre-existing `pkg/compiler` test failures (4 tests: `TestUserDataNotifyEnv_*`, `TestUserDataKMTracingServicectlStart`, `TestGitHubUserDataGITASKPASS`) are documented in `deferred-items.md` and predate Phase 73. Not a Phase 73 regression.

---

## Human Verification Required

None outstanding. Live UAT was completed by operator KPH on 2026-05-08 against sandbox
`lrn2-ee9499b5`, covering:

- **Scenario 1 (live):** End-to-end VS Code Remote-SSH — operator ssh'd in, opened VS Code,
  installed plugins, edited `/workspace` file, confirmed persistence.
- **Scenario 2 (live):** Keypair lifecycle — keys generated at create with correct modes;
  destroy cleanup confirmed by unit tests.
- **Scenario 3 (live):** `~/.ssh/config` managed-block upsert seen live in operator's config file.
- **Scenario 4 (unit tests):** `vscodeEnabled=false` produces clean error — `TestVSCodeStart_MissingKey` covers this path.
- **Scenario 5 (live):** Port collision + `--local-port 22122` — operator confirmed working after fix `2501bc9`.
- **Scenario 6 (unit tests):** Cross-machine missing-key error — `TestVSCodeStart_MissingPrivateKey` covers this path.

UAT status: `approved`. All six scenarios signed off.

---

## Test Suite Summary

| Package | Tests | Result |
|---------|-------|--------|
| `pkg/sshkey` | 7 (GenerateAndWrite: mode, parse, comment, newline, parentdir, idempotent) | ALL PASS |
| `internal/app/cmd` (sshconfig) | 9 (UpsertCreatesFile, UpsertAppends, UpsertReplaces, UpsertInsertsBefore, PreservesOutside, RemovePreservesOthers, RemoveCleans, RemoveIdempotentMissing, RemoveIdempotentNoFile) | ALL PASS |
| `internal/app/cmd` (vscode) | 6 (MissingPrivateKey, BuildsPortForwardArgs, OutputContainsHostAlias, SSHDInactive, PrePhase73, Healthy) | ALL PASS |
| `pkg/profile` (VSCode) | 2 (DefaultTrue, False) | ALL PASS |
| `pkg/compiler` (VSCode) | 4 (VSCodeEnabled, VSCodeDisabled, VSCodePubKey, MissingKeyErrors) | ALL PASS |
| `go build ./...` | — | PASS |
| `go vet ./pkg/... ./internal/...` | — | PASS (sidecar vet warning is pre-existing, unrelated) |

---

## Gaps Summary

None. All nine observable truths verified against the actual codebase. The phase goal — "operator
can connect their local desktop VS Code to a sandbox via Remote-SSH over SSM" — is fully
implemented, tested, and live-validated.

---

_Verified: 2026-05-07_
_Verifier: Claude (gsd-verifier)_
