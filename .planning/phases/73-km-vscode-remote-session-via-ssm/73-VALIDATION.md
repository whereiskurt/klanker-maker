---
phase: 73
slug: km-vscode-remote-session-via-ssm
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-06
---

# Phase 73 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (Go 1.21+) |
| **Config file** | go.mod / go.sum |
| **Quick run command** | `go test ./internal/app/cmd/ -run TestVscode -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | quick ~5s, full ~90s |

---

## Sampling Rate

- **After every task commit:** Run quick command (vscode-scoped tests)
- **After every plan wave:** Run full suite
- **Before `/gsd:verify-work`:** Full suite must be green + manual smoke test passing
- **Max feedback latency:** 90 seconds

---

## Per-Task Verification Map

(Populated by gsd-planner during plan generation; planner will produce this table from PLAN.md tasks. Initial placeholder template below.)

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 73-00-01 | 00 | 0 | wave-0-stubs | wave-0 | `go test ./internal/app/cmd/ -run TestVscode -count=1` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/app/cmd/vscode_test.go` — failing stubs for `start`, `stop`, `status` command construction (SSM call shape, flag parsing, error messages)
- [ ] `pkg/profile/types_test.go` (or `validate_test.go`) — failing stubs for `vscodeEnabled` default-true semantics + nil-CLI handling
- [ ] `pkg/compiler/userdata_test.go` — failing stubs for the conditional userdata block (emits unit/wrapper/dir when `VSCodeEnabled: true`, omits when `false`, handles nil CLI as default-true)

*Wave 0 is mandatory because the phase introduces new code surface (new file `vscode.go`, new schema field, new userdata template branch). Stubs lock the contract before implementation.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end VS Code session against a live sandbox | Phase goal | Requires real EC2 + SSM + browser | 1. `make build && km init --sidecars` 2. Create profile with `code` install in `initCommands`, `vscodeEnabled: true` 3. `km create profiles/vscode-smoke.yaml` 4. `km vscode start <sb>` 5. Open URL in browser, verify token works 6. Edit a file in `/workspace`, save 7. `km shell <sb> -- cat /workspace/<edited-file>` confirms persistence 8. `km vscode stop <sb>` 9. `km vscode status <sb>` returns inactive |
| Token rotation across `start` calls | Auth & security model | Verifies fresh credential per session | 1. `km vscode start <sb>` → record token1 2. `km vscode stop <sb>` 3. `km vscode start <sb>` → record token2 4. Assert token1 ≠ token2 |
| `vscodeEnabled: false` produces a clean error | CLI surface decision | Requires sandbox provisioned with flag false | 1. Create profile with `vscodeEnabled: false` 2. `km create` 3. `km vscode start <sb>` should fail with "VS Code not enabled in this sandbox's profile" hint, not a raw systemd error |
| Local port collision behavior | Edge cases | Requires another process on operator's laptop bound to 8443 | 1. `nc -l 8443 &` on operator laptop 2. `km vscode start <sb>` → expect bind error 3. `km vscode start <sb> --local-port 9443` → succeeds 4. `kill %1` cleanup |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (vscode.go, profile schema, userdata template)
- [ ] No watch-mode flags
- [ ] Feedback latency < 90s
- [ ] `nyquist_compliant: true` set in frontmatter (after planner populates Per-Task Verification Map)

**Approval:** pending
