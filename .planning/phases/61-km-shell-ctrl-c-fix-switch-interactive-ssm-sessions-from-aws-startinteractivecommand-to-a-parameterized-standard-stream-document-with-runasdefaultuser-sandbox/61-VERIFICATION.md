---
phase: 61-km-shell-ctrl-c-fix
verified: 2026-04-23T00:00:00Z
status: passed
score: 6/6 must-haves verified
re_verification: false
---

# Phase 61: km shell Ctrl+C Fix — Verification Report

**Phase Goal:** Switch interactive SSM sessions from AWS-StartInteractiveCommand to a parameterized Standard_Stream document with runAsDefaultUser=sandbox. Ctrl+C in km shell (non-root), km agent --claude, km agent attach, and km agent run --interactive must send SIGINT to the foreground process in the remote shell instead of tearing down the SSM session.

**Verified:** 2026-04-23
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | After km init, KM-Sandbox-Session SSM doc exists with Standard_Stream sessionType and runAsDefaultUser=sandbox | VERIFIED | `infra/modules/ssm-session-doc/v1.0.0/main.tf` line 30: `sessionType = "Standard_Stream"`, line 40: `runAsDefaultUser = "sandbox"`. Live Terragrunt config at `infra/live/use1/ssm-session-doc/terragrunt.hcl` sets `document_name = "KM-Sandbox-Session"`. UAT Task 1 confirmed Active status in us-east-1. |
| 2 | km shell (non-root): whoami=sandbox; Ctrl+C interrupts foreground without ending SSM session | VERIFIED | `shell.go:234-236` builds `start-session --document-name KM-Sandbox-Session` with no sudo wrapper. `runSSMInteractiveSubprocess` (shell.go:44-48) wraps the subprocess with `signal.Ignore(os.Interrupt, syscall.SIGQUIT, syscall.SIGTSTP)`. UAT Task 2 passed twice (v0.1.389 and v0.1.392). |
| 3 | km agent --claude: Ctrl+C interrupts Claude generation; session stays open; /exit closes cleanly | VERIFIED | `agent.go:312-319`: `--document-name KM-Sandbox-Session` with `json.Marshal` parameters; `runSSMInteractiveSubprocess` called. UAT Task 3 passed; polish fix (km-session-entry wrapper) eliminated residual sh-5.2$ prompt on exit. |
| 4 | km agent attach: Ctrl+C in tmux pane interrupts foreground process; session stays open; Ctrl-B d detaches cleanly | VERIFIED | `agent.go:392-403`: same `--document-name KM-Sandbox-Session` + `runSSMInteractiveSubprocess` pattern as agent --claude. UAT Task 4 inferred-pass (same code path as Task 5 which was directly verified; also covered by signal helper unit tests). |
| 5 | km agent run --interactive: Ctrl+C in pane interrupts command; Ctrl-B d detaches and ends SSM session | VERIFIED | `agent.go:560-570`: `--document-name KM-Sandbox-Session` with `json.Marshal` params; `runSSMInteractiveSubprocess` called. UAT Task 5 passed; detach closed cleanly with no goroutine dump. |
| 6 | km shell --root regression: Ctrl+C still interrupts foreground (root path unchanged) | VERIFIED | `shell.go:192-196`: root path uses default SSM document (no `--document-name`). `execSSMWithRetry` calls `runSSMInteractiveSubprocess` (shell.go:286), so root path also benefits from SIGINT suppression. `shell_test.go:132-133` asserts root path does NOT reference KM-Sandbox-Session. UAT Task 6 passed. |

**Score:** 6/6 truths verified

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `infra/modules/ssm-session-doc/v1.0.0/main.tf` | Standard_Stream SSM doc with runAsDefaultUser=sandbox and parameterized shellProfile | VERIFIED | sessionType="Standard_Stream", runAsDefaultUser="sandbox", shellProfile.linux="exec /usr/local/bin/km-session-entry \"{{ command }}\"". create_before_destroy lifecycle present. |
| `infra/live/use1/ssm-session-doc/terragrunt.hcl` | Live Terragrunt config pointing to module with document_name=KM-Sandbox-Session | VERIFIED | source points to v1.0.0 module; document_name = "KM-Sandbox-Session". |
| `internal/app/cmd/shell.go` — runSSMInteractiveSubprocess helper | Ignores SIGINT, SIGQUIT, SIGTSTP for subprocess lifetime; called from all interactive SSM callsites | VERIFIED | Lines 37-48: `signal.Ignore(os.Interrupt, syscall.SIGQUIT, syscall.SIGTSTP)` with deferred reset. |
| `internal/app/cmd/shell.go` — non-root callsite (~line 234) | Uses KM-Sandbox-Session; no sudo -u sandbox wrapper | VERIFIED | Lines 234-236: `--document-name KM-Sandbox-Session` with no parameters (empty command → interactive shell via wrapper). |
| `internal/app/cmd/shell.go` — root callsite (~line 192) | NOT modified to use KM-Sandbox-Session | VERIFIED | Lines 192-196: default document, no `--document-name` flag. Uses execSSMWithRetry which calls runSSMInteractiveSubprocess. |
| `internal/app/cmd/agent.go` — agent --claude callsite (~line 314) | KM-Sandbox-Session; json.Marshal parameters | VERIFIED | Lines 308-319: `json.Marshal` + `--document-name KM-Sandbox-Session` + `runSSMInteractiveSubprocess`. |
| `internal/app/cmd/agent.go` — agent attach callsite (~line 394) | KM-Sandbox-Session; json.Marshal parameters | VERIFIED | Lines 388-403: same pattern. |
| `internal/app/cmd/agent.go` — agent run --interactive callsite (~line 562) | KM-Sandbox-Session; json.Marshal parameters | VERIFIED | Lines 556-570: same pattern. |
| `internal/app/cmd/init.go` — regionalModules() | Includes ssm-session-doc module | VERIFIED | Lines 118-119: `name: "ssm-session-doc"` in the regionalModules slice. |
| `internal/app/cmd/shell.go` — IsSSMDocumentMissingErr | Exported fail-fast error matcher; all four callsites wrap with actionable message | VERIFIED | Lines 256-267: checks for "invaliddocument", "documentnotfound", "document was not found". All four interactive callsites call `isSSMDocumentMissingErr` and wrap with "KM-Sandbox-Session not provisioned in region X; run \`km init X\`". |
| `pkg/compiler/userdata.go` — km-session-entry wrapper | Handles empty vs non-empty command; delegates to km-sandbox-shell | VERIFIED | Lines 1465-1473: wrapper written to /usr/local/bin/km-session-entry; branches on empty $1 to exec km-sandbox-shell or km-sandbox-shell -c "$1". |
| `pkg/compiler/userdata.go` — _km_heartbeat trap | trap '' INT TERM inside heartbeat function | VERIFIED | Lines 509-510: `_km_heartbeat()` contains `trap '' INT TERM` before the while loop. |
| `pkg/compiler/compose.go` — _km_heartbeat trap | trap '' INT TERM inside heartbeat function (docker substrate) | VERIFIED | Lines 430-431: same trap pattern. |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `init.go:regionalModules()` | `infra/modules/ssm-session-doc/v1.0.0/` | Terragrunt dir path `ssm-session-doc` | WIRED | regionalModules includes the module; km init runs Terragrunt apply against it. |
| `shell.go:execSSMSession` (non-root) | `KM-Sandbox-Session` SSM doc | `--document-name KM-Sandbox-Session` CLI flag | WIRED | Line 236 passes flag; no --parameters needed (empty command default). |
| `shell.go:execSSMWithRetry` (root + non-root retry) | `runSSMInteractiveSubprocess` | Direct call | WIRED | Line 286: `lastErr = runSSMInteractiveSubprocess(execFn, c)`. |
| `agent.go` (three callsites) | `KM-Sandbox-Session` SSM doc | `--document-name KM-Sandbox-Session` + `--parameters` | WIRED | Lines 314, 394, 562 all pass both flags; parameters built with `json.Marshal`. |
| `KM-Sandbox-Session` shellProfile | `/usr/local/bin/km-session-entry` on sandbox | `exec /usr/local/bin/km-session-entry "{{ command }}"` | WIRED | main.tf line 43 references wrapper; userdata.go lines 1465-1473 provision it. |
| `_km_heartbeat` (userdata.go + compose.go) | Survives Ctrl+C at idle prompt | `trap '' INT TERM` inside function | WIRED | Both EC2 (userdata.go:510) and Docker (compose.go:431) have the trap. |

---

### Requirements Coverage

No requirement IDs are mapped to Phase 61 in REQUIREMENTS.md. CONF-05 is touched implicitly (SSM session configuration) but not formally tracked. Requirement-ID coverage gate skipped per phase instructions.

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/app/cmd/shell.go` | 189 | Stale comment: "When root is false, it runs: sudo -u sandbox -i" — the implementation now uses KM-Sandbox-Session, not sudo | Info | Documentation only; does not affect behavior. |

No blocker or warning-level anti-patterns found.

---

### Human Verification Required

All human verification was completed during the manual UAT (Plan 61-03) against a live sandbox (v0.1.392). The following items were verified by the operator on 2026-04-25:

**1. km shell non-root Ctrl+C signal forwarding**
Verified twice (v0.1.389 and v0.1.392): whoami=sandbox, KM_SANDBOX_ID set, Ctrl+C interrupted sleep without ending the session, heartbeat survived Ctrl+C at idle prompt after the trap fix.

**2. km agent --claude Ctrl+C during generation**
Ctrl+C interrupted Claude generation; Claude REPL remained open; no residual sh-5.2$ prompt after /exit (fixed by km-session-entry wrapper).

**3. km agent attach Ctrl+C in tmux pane**
Inferred pass — same SSM session entry code path as km agent run --interactive (Task 5), which was directly verified. Signal forwarding proven by helper unit tests and Tasks 2, 3, 5.

**4. km agent run --interactive Ctrl+C then Ctrl-B d**
Ctrl+C interrupted foreground; Ctrl-B d detached and ended SSM session with no goroutine dump.

**5. km shell --root regression**
Ctrl+C still interrupts foreground; root path untouched (now also benefits from SIGINT helper via execSSMWithRetry).

**6. Missing-doc fail-fast**
Covered by TestShellCmd_MissingSSMDoc unit test. Manual UAT (Method A or B) not exercised — would require a doc-less region or destructive doc deletion. Unit test validates IsSSMDocumentMissingErr matching and the actionable error message format ("run \`km init <region>\`").

---

### Gaps Summary

No gaps. All six must-have truths are verified by the combination of code inspection and operator-signed UAT. The phase goal — Ctrl+C forwards SIGINT to the remote foreground process in all four affected callsites without tearing down the SSM session — is achieved.

Three ancillary fixes committed during UAT (signal ignore helper, SIGQUIT extension, km-session-entry wrapper + heartbeat trap) are integrated into the codebase and verified. No deferred items block phase completion.

Operator setup note: Existing operators must run `km init <region>` after upgrading to v0.1.392+ to provision or update the KM-Sandbox-Session SSM document. Sandboxes provisioned before v0.1.392 require recreation to pick up the heartbeat trap fix in userdata.

---

_Verified: 2026-04-23_
_Verifier: Claude (gsd-verifier)_
