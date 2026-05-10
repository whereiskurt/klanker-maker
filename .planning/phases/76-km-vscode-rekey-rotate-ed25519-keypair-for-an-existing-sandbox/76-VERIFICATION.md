---
phase: 76-km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox
verified: 2026-05-10T02:16:14Z
status: passed
score: 14/14 must-haves verified
re_verification: false
---

# Phase 76: km vscode rekey — Verification Report

**Phase Goal:** Add `km vscode rekey <sandbox-id>` command to rotate the per-sandbox ed25519 keypair on an existing sandbox without requiring `km destroy && km create`. Solves three pain points: (1) baked-AMI sandboxes started with stale baked-in keys, (2) cross-laptop bootstrap when operator switches machines, (3) post-incident key rotation.
**Verified:** 2026-05-10T02:16:14Z
**Status:** passed
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `km vscode rekey <sandbox-id>` is a registered cobra subcommand | VERIFIED | `km vscode rekey --help` prints full help; `newVSCodeRekeyCmd` registered in `newVSCodeCmdInternal` at vscode.go:53 |
| 2 | `--force` and `--yes` boolean flags are present | VERIFIED | vscode.go:249-250; `TestVSCodeRekey_FlagsExist` PASS |
| 3 | Four ordered pre-flight gates fire: fetch → EC2 running → lock → SSM probe | VERIFIED | runVSCodeRekey at vscode.go:254-302; gates verified by 6 unit tests PASS |
| 4 | Lock check bypassed with `--force`; lock check NOT called at all | VERIFIED | checkSandboxLock var indirection in lock.go:132; `TestVSCodeRekey_Locked_WithForce` PASS (callCount==0 assertion) |
| 5 | Keypair generated to scratch paths; pre-existing scratch overwritten | VERIFIED | sshkey.GenerateAndWrite to `.new`/`.pub.new` at vscode.go:335; `TestVSCodeRekey_OverwritesScratch` PASS |
| 6 | SSM install script includes `set -e` + `restorecon … || true` guard | VERIFIED | vscode.go:342-353; `command -v restorecon ... || true` present |
| 7 | Readback verification byte-for-byte with `\r\n` stripping | VERIFIED | vscode.go:361-375; `strings.TrimRight(firstLine, "\r\n")`; `TestVSCodeRekey_VerifyMismatch` PASS |
| 8 | Verification mismatch: hard error + local files UNCHANGED | VERIFIED | vscode.go:374; rename only after match; `TestVSCodeRekey_VerifyMismatch` asserts original bytes unchanged PASS |
| 9 | Atomic rename: `.pub.new` before `.new` (private key) | VERIFIED | vscode.go:381 before 384; `TestVSCodeRekey_RenameOrdering` PASS |
| 10 | Confirmation prompt with fingerprint; bypassed on `--yes` | VERIFIED | vscode.go:312-330; `TestVSCodeRekey_YesFlag` PASS (no "[y/N]" in output); `TestVSCodeRekey_ConfirmNo` PASS |
| 11 | 'n' answer = clean abort (nil return, no key changes) | VERIFIED | vscode.go:326-329 `return nil`; `TestVSCodeRekey_ConfirmNo` asserts files unchanged PASS |
| 12 | Cross-laptop bootstrap path works (local key absent) | VERIFIED | `localKeyAbsent` bool path at vscode.go:304-309; `TestVSCodeRekey_CrossLaptop` PASS (keys created, "created" in output) |
| 13 | All 5 output step markers print in order on success path | VERIFIED | vscode.go:285,302,339,376,393,394; `TestVSCodeRekey_OutputMarkers` PASS (all 7 marker assertions ordered) |
| 14 | CLAUDE.md and docs/vscode.md updated with rekey documentation | VERIFIED | CLAUDE.md has 3 references; docs/vscode.md has "Rotating a sandbox key" section with all required content |

**Score:** 14/14 truths verified

---

### Required Artifacts

| Artifact | Provides | Status | Details |
|----------|---------|--------|---------|
| `internal/app/cmd/vscode.go` | `newVSCodeRekeyCmd`, `runVSCodeRekey`, `ec2DescribeAPI`, `pubkeyFingerprint` | VERIFIED | All four symbols present; complete 7-step implementation; no TODO markers remaining |
| `internal/app/cmd/lock.go` | `var checkSandboxLock = CheckSandboxLock` indirection | VERIFIED | lock.go:132 — comment + single-line var declaration |
| `internal/app/cmd/vscode_test.go` | 16 `TestVSCodeRekey_*` tests all PASS (0 SKIP, 0 FAIL) | VERIFIED | Test run: 16 PASS, 0 SKIP, 0 FAIL; also contains `vsCodeEC2Mock`, `sequencedSSMMock`, `rekeyInstallSpyMock`, `extractPubkeyFromInstallScript`, `seedRekeyTestKeys` |
| `CLAUDE.md` | `km vscode rekey` command entry + "Rotating a sandbox key (Phase 76)" subsection | VERIFIED | 3 occurrences of "km vscode rekey"; "Rotating a sandbox key" section present |
| `docs/vscode.md` | "Rotating a sandbox key" section with three pain-point scenarios | VERIFIED | All 4 grep checks pass: "Rotating a sandbox key", "old key until reconnect", "cross-laptop bootstrap", "pre-Phase-73" |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `newVSCodeCmdInternal` | `newVSCodeRekeyCmd` | `parent.AddCommand(newVSCodeRekeyCmd(...))` | WIRED | vscode.go:53 — rekey registered alongside start/status |
| `runVSCodeRekey` | `ec2Client.DescribeInstances` | EC2 running-state gate | WIRED | vscode.go:266-283 — filter on `tag:km:sandbox-id` AND `instance-state-name=running` |
| `runVSCodeRekey` | `checkSandboxLock` (package var) | `if !force { checkSandboxLock(...) }` | WIRED | vscode.go:288-292 — uses var not direct function call |
| `runVSCodeRekey` | `sendSSMAndWait` + `parseVSCodeStatus` | vsCodeStatusScript pre-flight | WIRED | vscode.go:295-301 — reuses existing Phase 73 script verbatim |
| `runVSCodeRekey` | `sshkey.GenerateAndWrite` | scratch-path keygen | WIRED | vscode.go:335 — `pkg/sshkey.GenerateAndWrite(privNewPath, pubNewPath, "km-"+sandboxID)` |
| `runVSCodeRekey` | `sendSSMAndWait` (second call) | SSM install + readback | WIRED | vscode.go:355 — single round-trip script with `=== READBACK ===` marker |
| `runVSCodeRekey` | `os.Rename` x2 | atomic .pub-first commit | WIRED | vscode.go:381 (pub) then 384 (priv) — correct ordering verified |
| `pubkeyFingerprint` | `gossh.ParseAuthorizedKey` + `gossh.FingerprintSHA256` | vscode.go:407-411 | WIRED | `golang.org/x/crypto/ssh` imported at vscode.go:19 |
| `newVSCodeRekeyCmd` RunE | real `ec2.NewFromConfig(awsCfg)` | production EC2 client init | WIRED | vscode.go:241-245 — `kmaws.LoadAWSConfig` then `ec2.NewFromConfig` |
| `km vscode rekey --help` | Long help text includes active-session note | binary surface | WIRED | Binary output confirmed: "Active VS Code Remote-SSH sessions stay connected with the old key until you reconnect." |

---

### Requirements Coverage

The requirement IDs for Phase 76 are defined locally in the PLAN frontmatter files rather than in the project-level `.planning/REQUIREMENTS.md` (which stops at SLCK/REQ-SLACK-IN- series). All 14 phase-local requirement IDs are accounted for across the four plans:

| Requirement ID | Source Plan | Evidence | Status |
|----------------|------------|----------|--------|
| REKEY-TESTSTUBS | 76-00 | 16 `TestVSCodeRekey_*` stubs appended in Wave 0; all skips converted to PASS by Wave 2 | SATISFIED |
| REKEY-CLI-SURFACE | 76-01 | `newVSCodeRekeyCmd` with `Use="rekey <sandbox-id>"`, `--force`, `--yes` | SATISFIED |
| REKEY-PREFLIGHT-EC2 | 76-01 | EC2 DescribeInstances gate; "not running" error pointing at `km resume` | SATISFIED |
| REKEY-PREFLIGHT-LOCK | 76-01 | `checkSandboxLock` var indirection; "locked. Use --force" error; Locked_WithForce verifies zero calls | SATISFIED |
| REKEY-PREFLIGHT-SSM | 76-01 | `vsCodeStatusScript` + `parseVSCodeStatus` reused verbatim; 3 error paths tested | SATISFIED |
| REKEY-KEY-CLASSIFICATION | 76-02 | `localKeyAbsent` bool; normal-rotation and cross-laptop paths both tested | SATISFIED |
| REKEY-CONFIRMATION-PROMPT | 76-02 | Prompt with fingerprint; `yes` bypass; `n` → `Aborted.` + `return nil` | SATISFIED |
| REKEY-KEYGEN-SCRATCH | 76-02 | `sshkey.GenerateAndWrite` to `.new`/`.pub.new`; pre-existing scratch overwritten | SATISFIED |
| REKEY-SSM-PUSH-VERIFY | 76-02 | `set -e` + `restorecon || true` + `=== READBACK ===`; `TrimRight("\r\n")`; byte-for-byte comparison | SATISFIED |
| REKEY-ATOMIC-COMMIT | 76-02 | `.pub.new → .pub` before `.new → priv`; scratch files absent post-commit (RenameOrdering test) | SATISFIED |
| REKEY-OUTPUT-MARKERS | 76-02 | All 5 step markers + "Rekey complete" in order; OutputMarkers test PASS | SATISFIED |
| REKEY-DOCS-CLAUDE-MD | 76-03 | 3 occurrences in CLAUDE.md; "Rotating a sandbox key (Phase 76)" subsection | SATISFIED |
| REKEY-DOCS-VSCODE-MD | 76-03 | Full "Rotating a sandbox key" section; all three pain-point scenarios; runbook table | SATISFIED |
| PHASE-73-DEPENDENCY | 76-01,76-03 | `vsCodeStatusScript`, `parseVSCodeStatus`, `extractResourceID`, `sendSSMAndWait`, `resolveVSCodeDeps` reused without redeclaration | SATISFIED |

**Note:** REKEY-* and PHASE-73-DEPENDENCY are phase-local IDs defined in the PLAN frontmatter; they are not registered in `.planning/REQUIREMENTS.md`. This is a known project pattern for phase-specific requirements.

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/app/cmd/vscode.go` | — | No TODO markers, no stubs, no empty returns | None | Clean |
| `internal/app/cmd/vscode_test.go` | 402 | Comment "All 16 tests start with t.Skip..." (comment only, not in test bodies) | Info | Documentation artifact; no live t.Skip calls in any TestVSCodeRekey_* function body |

No blocker anti-patterns found. The comment at line 402 is a Wave 0 historical note in the section header — all 16 test bodies are fully uncommented and actively executing.

---

### Human Verification Required

#### 1. Real-sandbox round-trip

**Test:** Run `km vscode rekey <real-running-sandbox-id>` against an actual EC2 sandbox  
**Expected:** Prompts for confirmation, generates new keypair, installs via SSM, prints all 5 step markers, `km vscode start` with new key connects successfully  
**Why human:** Tests use mocks for SSM/EC2; real SELinux `restorecon` behavior, real SSM latency, and real VS Code reconnect behavior cannot be validated in unit tests

#### 2. Baked-AMI scenario

**Test:** `km shell --learn --ami`, bake AMI, `km create` from AMI, run `km vscode rekey`  
**Expected:** Rekey overwrites the stale baked-in authorized_keys; `km vscode start` with new key connects  
**Why human:** Requires a baked AMI with a deliberately stale key — cannot simulate in unit tests

#### 3. Active VS Code session continuity

**Test:** With VS Code connected via Remote-SSH, run `km vscode rekey --yes`, then attempt to send a file in the existing VS Code window  
**Expected:** Existing session stays connected; only new connections require the new key  
**Why human:** sshd session persistence requires a real authenticated SSH connection

#### 4. Cross-laptop flow

**Test:** On a second machine with no `~/.km/keys/<sandbox-id>*` files, run `km vscode rekey <id> --yes`  
**Expected:** Keys are created, `km vscode start` connects with the new key  
**Why human:** Requires a real sandbox and a machine that never ran `km create` for that sandbox

---

### Gaps Summary

No gaps found. All 14 observable truths verified. All required artifacts are substantive and wired. All 16 unit tests pass. The km binary is built and `km vscode rekey --help` produces correct output. CLAUDE.md and docs/vscode.md contain all required content.

---

_Verified: 2026-05-10T02:16:14Z_
_Verifier: Claude (gsd-verifier)_
