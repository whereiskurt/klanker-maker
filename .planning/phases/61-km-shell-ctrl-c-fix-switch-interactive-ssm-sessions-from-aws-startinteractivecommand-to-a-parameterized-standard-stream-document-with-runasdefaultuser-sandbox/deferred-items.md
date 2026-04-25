# Deferred Items — Phase 61 Plan 02

## Pre-existing Test Failures (out of scope, not caused by 61-02 changes)

### TestShellCmd_StoppedSandbox, TestShellCmd_UnknownSubstrate, TestShellCmd_MissingInstanceID

**Root cause:** `shell.go` RunE explicitly discards the error from `runShell` via `_ = runShell(...)`.
This is intentional — km shell is designed to always return nil from cobra to avoid printing
error messages when the user exits the shell normally (aws ssm start-session itself prints the error).
These tests expect `root.Execute()` to return the error, but the cobra command discards it by design.

**Status:** Pre-existing before phase 61. Confirmed by running tests against the production commit
without test changes.

**Remediation:** Either (a) export `RunShell` as a testable function, or (b) change the RunE to
propagate errors and let the CLI handle UX differently. Track as a separate cleanup task.

### TestShellDockerContainerName, TestShellDockerNoRootFlag

**Root cause:** Pre-existing test assertions about `-u` flag presence/absence in docker exec args
vs. `--login` shell launch. Not related to SSM changes.

**Status:** Pre-existing before phase 61.
