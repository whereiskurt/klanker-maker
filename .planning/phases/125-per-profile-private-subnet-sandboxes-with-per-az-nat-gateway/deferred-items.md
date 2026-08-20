# Deferred items — Phase 125

Out-of-scope findings discovered during execution, logged per the executor's
scope-boundary rule (fix only what the current task's changes touch).

## 125-02 — `TestRunner_Apply_ContextCancellationKillsSubprocess` fails in this environment

- **Found during:** Task 3 verification (`go test ./pkg/terragrunt/ ./pkg/compiler/ -count=1`)
- **Symptom:** `pkg/terragrunt/runner_test.go` — `Apply took ~5s to return after cancel —
  expected sub-2s response`. Fails consistently (reproduced 2x in isolation), not
  intermittent.
- **Unrelated to this plan's changes:** the file was last touched in phase 84.4
  (`5e10e370`, `56f6e1bc`), long before Phase 125. Neither `pkg/terragrunt/runner.go`
  nor `runner_test.go` was modified by any task in this plan.
- **Likely cause (not investigated further — out of scope):** the test spawns a fake
  terragrunt binary running `sleep 5` and asserts a context-cancel kills the subprocess
  group within 2s; the kill appears not to propagate in this worktree's execution
  environment (possibly a process-group/signal-delivery difference under the current
  sandbox). Same category as the previously-noted "environmental" AWS SSO test
  failures (`InvalidGrantException`) — see
  `project_cmd_suite_pre_existing_failures` / `project_full_suite_known_red_packages`
  memory entries.
- **Action taken:** none. Not fixed per the executor's scope boundary (pre-existing
  failure in a file unrelated to this task's `<files>`). All substrate-version-pin and
  compiler tests introduced/touched by this plan are green in isolation.
