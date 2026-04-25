---
phase: 61
slug: km-shell-ctrl-c-fix-switch-interactive-ssm-sessions-from-aws-startinteractivecommand-to-a-parameterized-standard-stream-document-with-runasdefaultuser-sandbox
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-25
---

# Phase 61 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` (stdlib) |
| **Config file** | none |
| **Quick run command** | `go test ./internal/app/cmd/... -run "TestShell\|TestAgent\|TestRunInit\|TestRegionalModules" -v` |
| **Full suite command** | `go test ./internal/app/cmd/... -v` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/... -run "TestShell\|TestAgent\|TestRunInit\|TestRegionalModules" -v`
- **After every plan wave:** Run `go test ./internal/app/cmd/... -v`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

(Tasks numbered after planner produces PLAN.md. Below is the requirement→test mapping the planner must honor; each task plan must wire to one of these test commands or declare manual UAT.)

| Behavior | Test Type | Automated Command | File | Status |
|----------|-----------|-------------------|------|--------|
| `infra/modules/ssm-session-doc/v1.0.0/` Terraform module valid | unit | `terraform validate` (in CI / dev) | `infra/modules/ssm-session-doc/v1.0.0/main.tf` | ⬜ pending |
| `KM-Sandbox-Session` doc schema accepted by AWS | manual | `aws ssm describe-document --name KM-Sandbox-Session` post-init | n/a | ⬜ pending |
| `regionalModules()` includes `ssm-session-doc` | unit | `go test ./internal/app/cmd/... -run TestRegionalModulesIncludesSSMDoc` | `init_test.go` | ⬜ pending |
| `TestRunInitWithRunnerAllModules` expects 7 modules | unit | `go test ./internal/app/cmd/... -run TestRunInitWithRunnerAllModules` | `init_test.go:92` | ⬜ pending |
| `TestRunInitSkipsSESWithoutZoneID` expects 6 modules | unit | `go test ./internal/app/cmd/... -run TestRunInitSkipsSESWithoutZoneID` | `init_test.go:159` | ⬜ pending |
| `km shell` non-root uses `KM-Sandbox-Session`, no `sudo -u sandbox -i` | unit | `go test ./internal/app/cmd/... -run TestShellCmd_EC2` | `shell_test.go` | ⬜ pending |
| `km shell --root` still passes no `--document-name` (regression guard) | unit | `go test ./internal/app/cmd/... -run TestShellCmd_EC2_Root` | `shell_test.go` (new test) | ⬜ pending |
| `km agent --claude` uses `KM-Sandbox-Session`, no `sudo` | unit | `go test ./internal/app/cmd/... -run TestAgentCmd_BackwardCompat` | `agent_test.go` | ⬜ pending |
| `km agent attach` uses `KM-Sandbox-Session`, no `sudo` | unit | `go test ./internal/app/cmd/... -run TestAgentAttach` | `agent_test.go` | ⬜ pending |
| `km agent run --interactive` uses `KM-Sandbox-Session`, no `sudo` | unit | `go test ./internal/app/cmd/... -run TestAgentInteractive` | `agent_test.go` | ⬜ pending |
| `--parameters` JSON built via `encoding/json.Marshal` (no manual escape) | unit | `go test ./internal/app/cmd/... -run TestAgentParametersEscaping` | `agent_test.go` (new test) | ⬜ pending |
| Operator IAM grants `ssm:StartSession` on `KM-Sandbox-Session` doc ARN | unit | `go test ./internal/app/cmd/... -run TestBootstrapPolicyIncludesSSMDoc` | `bootstrap_test.go` (new test) | ⬜ pending |
| Backwards compat: missing doc region fails fast with actionable error | unit | `go test ./internal/app/cmd/... -run TestShellCmd_MissingSSMDoc` | `shell_test.go` (new test) | ⬜ pending |
| Ctrl+C forwards SIGINT to remote foreground process (does not terminate session) | manual | UAT steps 2-5 in 61-CONTEXT.md | n/a (manual) | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Test infrastructure already exists (Go stdlib + existing test files). Wave 0 work:

- [ ] `infra/modules/ssm-session-doc/v1.0.0/main.tf`, `variables.tf`, `outputs.tf`, `terragrunt.hcl` template — new module skeleton must exist before CLI changes can reference it consistently
- [ ] Test stubs in `shell_test.go`, `agent_test.go`, `init_test.go`, `bootstrap_test.go` for the new behaviors above (red tests first, in TDD spirit, before implementation)

If none of those new tests can be written until the planner specifies the helper APIs, Wave 0 is just the Terraform module skeleton.

---

## Manual-Only Verifications

| Behavior | Why Manual | Test Instructions |
|----------|------------|-------------------|
| Ctrl+C forwards SIGINT to remote process inside `km shell` | Requires interactive PTY + AWS SSM round-trip; can't be unit-tested | Run `km shell <id>`, execute `sleep 100`, press Ctrl+C — expect `^C` and a new prompt; session must NOT exit |
| Ctrl+C interrupts Claude generation in `km agent --claude` | Same as above + requires Claude Code installed on sandbox | Start agent session, ask Claude to count to 1000, press Ctrl+C — expect interrupt indicator; Claude continues to accept input |
| Ctrl+C inside `km agent attach` tmux pane affects pane (not session) | Requires running tmux session on sandbox | Run `km agent run <id> --prompt "sleep 60"` to spawn tmux, then `km agent attach <id>`; press Ctrl+C — expect sleep killed, prompt restored; session intact until Ctrl-B d |
| Ctrl+C inside `km agent run --interactive` tmux pane | Same as above | Run `km agent run <id> --prompt "sleep 60" --interactive`; press Ctrl+C — expect sleep killed, prompt visible |
| `aws ssm describe-document --name KM-Sandbox-Session --region <r>` returns Active after `km init <r>` | End-to-end AWS resource creation | Run `km init us-east-1`; then `aws ssm describe-document --name KM-Sandbox-Session --region us-east-1 --profile klanker-terraform` — expect `Status: Active` and `SessionType: Standard_Stream` |
| `km shell --root` still works (regression check) | Behavioral; covered by unit test for command construction but signal-forwarding is interactive | Run `km shell --root <id>`, execute `sleep 100`, press Ctrl+C — same expectation as #1 |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify (manual UAT tasks must be flanked by automated tasks)
- [ ] Wave 0 covers all MISSING references (new module skeleton + test stubs)
- [ ] No watch-mode flags in test commands
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter (after planner approval)

**Approval:** pending
