# Phase 61: km shell Ctrl+C fix - Context

**Gathered:** 2026-04-25
**Status:** Ready for planning
**Source:** Design conversation (operator + Claude, 2026-04-25)

<domain>
## Phase Boundary

This phase fixes the bug where Ctrl+C inside an `aws ssm start-session` opened by `km shell` (non-root) or any `km agent` interactive subcommand tears down the SSM session instead of sending SIGINT to the foreground process in the remote shell.

Root cause (verified empirically against `learn-d8530b6b` on 2026-04-25): the four affected callsites use `--document-name AWS-StartInteractiveCommand`, whose `sessionType: InteractiveCommands` is documented to terminate when the user "terminates the command (for example, Ctrl+c or Ctrl+d)." The `Standard_Stream` sessionType used by the default `SSM-SessionManagerRunShell` document forwards Ctrl+C as a PTY byte instead, exactly like SSH — which is why the root path (`shell.go:179`, no `--document-name`) does not have this bug.

Affected callsites (all four use `AWS-StartInteractiveCommand` + `sudo -u sandbox -i [bash -c "..."]`):
- `internal/app/cmd/shell.go:214` — `km shell` non-root
- `internal/app/cmd/agent.go:300` — `km agent --claude`
- `internal/app/cmd/agent.go:373` — `km agent attach`
- `internal/app/cmd/agent.go:532` — `km agent run --interactive`
</domain>

<decisions>
## Implementation Decisions

### Approach: parameterized custom Standard_Stream document
- **Locked:** Create one custom SSM Session document `KM-Sandbox-Session` per region with `sessionType: Standard_Stream`, `runAsEnabled: true`, `runAsDefaultUser: sandbox`, and a parameterized `shellProfile.linux: "{{ command }}"` (default empty → opens the sandbox user's shell).
- **Locked:** Use the new doc for all four affected callsites; pass the inner command (env-sourcing, exec claude, tmux ops) via the `command` parameter rather than as the AWS-Start... command body.
- **Locked:** Drop the `sudo -u sandbox -i` wrapper from each callsite. `runAsDefaultUser` handles the user switch; layering `sudo` on top is redundant with runAs.
- **Rejected:** Modifying the global `SSM-SessionManagerRunShell` preferences doc to set runAs=sandbox. Reason: it's per-region/account and would affect every operator's `aws ssm start-session` to any instance in this account, including non-km use cases.
- **Rejected:** SSH-over-SSM via `AWS-StartSSHSession`. Reason: requires sshd + key management on the instance; heavier than needed when a custom Session doc solves the problem.

### Infrastructure
- **Locked:** Place the new SSM doc in a new regional Terragrunt module `infra/modules/ssm-session-doc/v1.0.0/` using `aws_ssm_document` resource. Plug into `regionalModules()` in `internal/app/cmd/init.go` so existing operators get the doc on next `km init <region>`.
- **Locked:** No imperative `ssmClient.CreateDocument` from `km init`. The Terragrunt path is consistent with the rest of the regional plumbing.
- **Locked:** IAM update — add `arn:aws:ssm:<region>:<acct>:document/KM-Sandbox-Session` to the operator role's `ssm:StartSession` resources. Inspect `internal/app/cmd/bootstrap.go:414` to determine current resource scope and extend it. (Default `SSM-SessionManagerRunShell` doesn't show up there because it's special-cased; custom docs need explicit grants.)

### Root path
- **Locked:** Do not touch the root path (`shell.go:179`). It already uses the default doc with `Standard_Stream` semantics — Ctrl+C works correctly today.
- **Locked:** No symmetrical `KM-Root-Session` doc. Symmetry isn't worth the additional surface area when the root path already works.

### Backwards compatibility
- **Operator decision required during planning:** When a sandbox lives in a region whose `KM-Sandbox-Session` doc isn't yet provisioned (e.g., the operator hasn't re-run `km init` after upgrading km), the CLI must either (a) fall back to the old `AWS-StartInteractiveCommand` path with a deprecation warning, or (b) fail fast with a clear "run `km init <region>` to provision the new SSM document" message. **Lean: (b) fail fast** — the regional infra invariant is normally maintained, and silent fallback would mask the bug we're fixing. Planner should confirm.
- **Locked:** No version-flagged migration. Single swap at the CLI; the doc is provisioned at init time per region.

### Tests
- **Locked:** Update `internal/app/cmd/shell_test.go` and `internal/app/cmd/agent_test.go` assertions that key off `AWS-StartInteractiveCommand` or specific substrings in the start-session command. Tests should assert the new doc name where applicable, and the absence of `sudo -u sandbox -i` in shell/agent invocations.
- **Locked:** A new test for the regional module / `regionalModules()` ordering ensuring the SSM doc module is included.

## Out of Scope (deliberately deferred)

### eBPF cgroup placement for SSM sessions
- **Why deferred:** Verified empirically that current production `sudo -u sandbox -i` SSM sessions are NOT in `/sys/fs/cgroup/km.slice/km-<id>.scope` either — the `/usr/local/bin/km-sandbox-shell` wrapper's cgroup write fails silently due to cgroup v2 cross-slice delegation rules. So the proposed change is at parity with current behavior, not a regression. Profiles using `enforcement: ebpf` (no iptables fallback) currently do not get cgroup-attached BPF programs applied to SSM sessions; this is a pre-existing bug worth its own investigation.
- **Captured in:** `.planning/todos/pending/ssm-sessions-skip-ebpf-cgroup.md`.

### Root path Ctrl+C / runAs
- Already works. Touching it would expand surface area without value.

### Docker substrate path
- Uses `docker exec`, not SSM. Unaffected by this phase.

### Claude's Discretion
- Exact text of the `shellProfile.linux` template (must accept empty command for interactive shell case AND non-empty for command-on-exit case in agent paths).
- Whether the Terraform/Terragrunt module exposes the doc name as an output for CLI consumption, or whether the CLI hardcodes `KM-Sandbox-Session`.
- Quoting/escaping strategy for the `command` parameter values (the existing code uses `strings.ReplaceAll(s, ` + "`\"`" + `, ` + "`\\\"`" + `)` in some spots; planner should pick one approach consistently).
</decisions>

<specifics>
## Specific Ideas

- Document content shape (planner should validate exact agent grammar — the schema below is a starting point, not gospel):

  ```json
  {
    "schemaVersion": "1.0",
    "description": "KM sandbox session: runs as sandbox user under Standard_Stream PTY",
    "sessionType": "Standard_Stream",
    "parameters": {
      "command": { "type": "String", "default": "" }
    },
    "inputs": {
      "runAsEnabled": true,
      "runAsDefaultUser": "sandbox",
      "shellProfile": { "linux": "{{ command }}" },
      "idleSessionTimeout": "20"
    }
  }
  ```

- New regional module location: `infra/modules/ssm-session-doc/v1.0.0/{main.tf,variables.tf,outputs.tf}`. Plug into `regionalModules()` in `init.go:83-145` between existing entries (ordering: no dependencies, can go after `s3-replication` and before `create-handler`).
- Existing test patterns to follow: `shell_test.go:71` asserts `start-session` substring; `agent_test.go:104,355,750,810` assert document names and command structure.
- `bootstrap.go:414` is where `ssm:SendCommand`, `ssm:StartSession` are granted today — IAM extension point.
- `userdata.go:1437-1459` (the broken cgroup wrapper) is NOT touched in this phase. It stays in place for the docker substrate path and as a no-op for SSM sessions until the cgroup gap is fixed in a future phase.

## CLI invocation matrix (post-change)

| Callsite | --document-name | --parameters |
|----------|----------------|--------------|
| `km shell` non-root (shell.go:214) | `KM-Sandbox-Session` | (none, opens shell) |
| `km agent --claude` (agent.go:300) | `KM-Sandbox-Session` | `{"command":["source /etc/profile.d/km-profile-env.sh; source /etc/profile.d/km-identity.sh; cd /workspace; <noBedrockPrefix>exec <claudeCmd>"]}` |
| `km agent attach` (agent.go:373) | `KM-Sandbox-Session` | `{"command":["tmux attach-session -t $(tmux list-sessions -F '#{session_name}' \| grep km-agent \| tail -1) \|\| echo No agent tmux sessions found"]}` |
| `km agent run --interactive` (agent.go:532) | `KM-Sandbox-Session` | `{"command":["tmux new-session -s '<sessionName>' '/tmp/km-agent-run.sh; exec bash'"]}` |
| `km shell --root` (shell.go:179) | (unchanged, no override) | (unchanged) |
</specifics>

<deferred>
## Deferred Ideas

- **eBPF cgroup placement for SSM-initiated sandbox sessions** — pre-existing gap, captured in `.planning/todos/pending/ssm-sessions-skip-ebpf-cgroup.md`. Likely needs `systemd-run --scope --slice=km.slice` or a setuid `km-cgroup-join` helper. Independent investigation.
- **Symmetric `KM-Root-Session` doc for the root path** — current root path works fine via the default doc. Add only if a consistent UX is desired in a future phase.
- **Per-environment SSM session preferences** (idle timeout, log group routing) — could fold into `KM-Sandbox-Session` but not needed for the Ctrl+C fix.
</deferred>

## UAT (Goal-Backward Verification)

1. Fresh `km init us-east-1` provisions `KM-Sandbox-Session` doc; `aws ssm describe-document --name KM-Sandbox-Session --region us-east-1` returns Active.
2. `km shell <id>` (non-root) lands as sandbox user (`whoami` returns `sandbox`). Run `sleep 100`, press Ctrl+C: the sleep is interrupted with `^C` and a new prompt appears. Session stays open.
3. `km agent --claude <id>`: while Claude is generating, Ctrl+C interrupts the generation (Claude shows interrupt indicator) and Claude continues to accept input. Pressing Ctrl+D exits Claude and ends the SSM session normally.
4. `km agent attach <id>` against a sandbox with an active tmux session: Ctrl+C inside the pane sends SIGINT to whatever's running in the pane. Ctrl-B d detaches from tmux and ends the SSM session.
5. `km agent run <id> --prompt "..." --interactive`: tmux session opens, Ctrl+C interrupts the running command in the pane (does not close SSM), Ctrl-B d detaches.
6. `km shell --root <id>` unchanged — Ctrl+C still interrupts foreground processes (regression check).
7. `internal/app/cmd/shell_test.go` and `internal/app/cmd/agent_test.go` pass with updated assertions.

---

*Phase: 61-km-shell-ctrl-c-fix*
*Context gathered: 2026-04-25 from design conversation*
