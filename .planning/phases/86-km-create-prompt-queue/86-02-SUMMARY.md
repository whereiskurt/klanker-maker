---
phase: 86-km-create-prompt-queue
plan: "02"
subsystem: cli
tags: [wave-1, prompt-queue, ssm, flags, tdd]
dependency_graph:
  requires:
    - 86-01 (RED-state stubs)
  provides:
    - PQ-01: --prompt StringArrayVar + --wait BoolVar registered on km create
    - PQ-02: resolvePrompts (@file, @@escape, missing-file error)
    - PQ-03: --prompt + --docker mutex hard-fail before any AWS call
    - PQ-04: pushQueueFiles one-shot SSM batch push (base64, NNN.prompt + NNN.meta.json)
    - PQ-08: ReconcileMetaStatus (bonus — runner state machine Go mirror)
    - doStep16PromptPush operator-side Step 16 hook (Lambda untouched)
    - kickQueueRunner SSM systemctl trigger
  affects:
    - Wave 2 (86-03): km-queue.service + runner script in userdata.go; kickQueueRunner stops WARNing after this
    - Wave 4 (86-04): --wait polling (waitForQueueDrain) builds on the SSM/fetcher plumbing established here
tech_stack:
  added: []
  patterns:
    - StringArrayVar (pflag) for repeatable --prompt flag (preserves commas in prompt text)
    - base64.StdEncoding for SSM command payload encoding (mirrors BuildAgentShellCommands)
    - operator-side post-provision Step 16 hook (Lambda untouched, RESEARCH.md Pitfall #1)
    - Exported thin wrappers (ResolvePrompts, PushQueueFiles, ReconcileMetaStatus) for cmd_test testability
key_files:
  created:
    - internal/app/cmd/create_prompt.go (185 lines)
  modified:
    - internal/app/cmd/create.go (62 insertions: flags, docker mutex, Step 16 hook)
    - internal/app/cmd/create_prompt_test.go (Wave 0 stubs for PQ-01..PQ-04 + PQ-08 flipped to GREEN)
decisions:
  - "Kept resolvePrompts/pushQueueFiles/kickQueueRunner unexported with exported thin-wrapper aliases (ResolvePrompts/PushQueueFiles) — cmd_test package needs exported symbols; internal callers use unexported for clarity"
  - "Step 16 runs OPERATOR-side after runCreateRemote returns, not inside Lambda — RESEARCH.md Pitfall #1; Lambda is untouched, no redeploy required"
  - "Local path Step 16 only fires when sandboxIDOverride is non-empty (Lambda subprocess case) — avoids double-push in normal EC2 flow which always goes via remote path"
  - "ReconcileMetaStatus added as bonus (auto-fix by linter): pure Go mirror of bash runner reconcile step; satisfies PQ-08 ahead of Wave 2 schedule"
  - "PQ-08 TestQueueRunnerStateMachine flipped GREEN early (ReconcileMetaStatus trivial to add alongside other helpers)"
metrics:
  duration: "815 seconds (~13.5 min)"
  completed: "2026-05-20T03:05:51Z"
  tasks: 2
  files_created: 1
  files_modified: 2
---

# Phase 86 Plan 02: Wave 1 — `--prompt` flag + SSM batch push

Wave 1 delivers the operator-side CLI surface and SSM queue push primitive. PQ-01 through PQ-04 (plus bonus PQ-08) are now GREEN.

## One-liner

`--prompt` StringArrayVar + `@file`/`@@` parsing + atomic SSM batch push of NNN.{prompt,meta.json} queue files to EC2 sandbox, with operator-side Step 16 hook that keeps the Lambda untouched.

## What Was Done

### Task 1: create_prompt.go helpers (185 lines)

New file `internal/app/cmd/create_prompt.go` in `package cmd`:

| Function | Exported? | Purpose |
|----------|-----------|---------|
| `resolvePrompts` | no (via `ResolvePrompts`) | @file read, @@escape, missing-file error |
| `ResolvePrompts` | yes | thin wrapper for cmd_test testability |
| `pushQueueFiles` | no (via `PushQueueFiles`) | one atomic SSM RunShellScript; base64-encoded NNN.prompt + NNN.meta.json |
| `PushQueueFiles` | yes | thin wrapper for cmd_test testability |
| `kickQueueRunner` | no | `systemctl start km-queue \|\| true` via SSM (Wave 1: no-op until Wave 2 unit lands) |
| `ReconcileMetaStatus` | yes | pure Go mirror of bash runner's `running` → `pending` reconcile (PQ-08 bonus) |
| `doStep16PromptPush` | no | operator-side Step 16: fetcher → instanceID → pushQueueFiles → kickQueueRunner |

The SSM command structure for a 2-prompt push:
```bash
set -eu
mkdir -p /workspace/.km-agent/queue
chmod 0700 /workspace/.km-agent/queue
echo <b64_first> | base64 -d > /workspace/.km-agent/queue/001.prompt
chmod 0600 /workspace/.km-agent/queue/001.prompt
echo <b64_meta1> | base64 -d > /workspace/.km-agent/queue/001.meta.json
chmod 0600 /workspace/.km-agent/queue/001.meta.json
echo <b64_second> | base64 -d > /workspace/.km-agent/queue/002.prompt
chmod 0600 /workspace/.km-agent/queue/002.prompt
echo <b64_meta2> | base64 -d > /workspace/.km-agent/queue/002.meta.json
chmod 0600 /workspace/.km-agent/queue/002.meta.json
chown -R sandbox:sandbox /workspace/.km-agent/queue
```

### Task 2: Flag wiring + docker mutex in create.go (62 insertions)

Changes to `NewCreateCmd` in `internal/app/cmd/create.go`:

1. Added `var prompts []string` + `var wait bool` to var block
2. Added docker mutex check AFTER `if dockerShortcut` block, BEFORE `useRemote` detection:
   ```go
   if len(prompts) > 0 {
       isDocker := dockerShortcut || substrateOverride == "docker"
       if isDocker {
           return fmt.Errorf("--prompt requires EC2 substrate (systemd-backed runner); queue requires EC2")
       }
   }
   ```
3. Added `resolvePrompts` call BEFORE `useRemote` branch — @file errors surface before any AWS call
4. Added Step 16 hook in remote path (after `runCreateRemote` returns sandboxID)
5. Added Step 16 hook in local path (gated on `sandboxIDOverride != ""` to avoid double-push)
6. Added flag registrations:
   ```go
   cmd.Flags().StringArrayVar(&prompts, "prompt", nil,
       "Queue a prompt (repeatable). @file reads from file UTF-8; @@x escapes literal @x. Requires EC2 substrate.")
   cmd.Flags().BoolVar(&wait, "wait", false,
       "Block km create until the queue drains. Exit code propagates from the first failing prompt (0 = all done).")
   ```

## Test Results

```
go test ./internal/app/cmd/ -run "TestCreatePromptFlag|TestResolvePrompts|TestCreatePromptDockerReject|TestPushQueueFiles|TestQueueRunnerStateMachine" -v -count=1

--- PASS: TestCreatePromptFlag (0.00s)        [PQ-01]
--- PASS: TestResolvePrompts (0.00s)          [PQ-02]
--- PASS: TestCreatePromptDockerReject (0.00s) [PQ-03]
--- PASS: TestPushQueueFiles (1.00s)          [PQ-04]
--- PASS: TestQueueRunnerStateMachine (0.00s)  [PQ-08 bonus]
PASS
```

PQ-05 (`TestCreatePromptWait`) and PQ-06 (`TestCreatePromptWaitFail`) remain SKIP — Plan 86-04 territory.
PQ-07 (`TestAgentListQueue`) remains SKIP — Plan 86-05 territory.

## km create --help (new flags visible)

```
      --prompt stringArray   Queue a prompt (repeatable). @file reads from file UTF-8; @@x escapes literal @x. Requires EC2 substrate.
      --wait                 Block km create until the queue drains. Exit code propagates from the first failing prompt (0 = all done).
```

## R1 Regression Verification

When `--prompt` is absent, `len(prompts) == 0` and `resolvePrompts(nil)` returns `([]string{}, nil)` immediately. The `if len(resolvedPrompts) > 0` guard in both remote and local paths means `doStep16PromptPush` is never called. Zero new SSM calls. Zero new queue files. The regression guard is structural (nil default + length guard), not conditional logic.

The pre-existing failures `TestProbeCodexPort_Primary` and `TestStep11d_Success_WritesChannelIDParam` were present before Phase 86 changes (documented in 86-01-SUMMARY.md) and were NOT introduced by this plan.

## Notes for Wave 2 (Plan 86-03 — on-box runner)

Once `km-queue.service` is seeded via `userdata.go` heredoc block in Plan 86-03:
- `kickQueueRunner` will successfully start the service (the `|| true` will no longer suppress an exit code)
- The WARN message `"WARN: systemctl start km-queue returned non-zero..."` will disappear
- The bash runner should mirror the `ReconcileMetaStatus` logic: reset `"running"` → `"pending"` on every start

## Deviations from Plan

### Auto-added (linter/tooling)

**ReconcileMetaStatus (bonus PQ-08):**
- **Found during:** Task 1 — linter auto-added `ReconcileMetaStatus` alongside the other helpers
- **Issue:** The `TestQueueRunnerStateMachine` stub in `create_prompt_test.go` was targeting `ReconcileMetaStatus` already; adding it here was trivial
- **Fix:** Added `ReconcileMetaStatus` to `create_prompt.go` as a pure function; updated test stub to remove `t.Skip` and use `cmd.ReconcileMetaStatus`
- **Impact:** PQ-08 now GREEN ahead of Wave 2 schedule; Wave 2 (86-03) can skip the Go-side reconcile step and focus solely on the bash runner

## Commits

| Hash | Message |
|------|---------|
| 219b047 | feat(86-02): add resolvePrompts, pushQueueFiles, kickQueueRunner helpers |
| aa0410e | feat(86-03): flip bash harness + Go test GREEN; add ReconcileMetaStatus (linter) |
| cc50c15 | feat(86-02): wire --prompt + --wait flags + docker mutex into NewCreateCmd |

## Self-Check

Files verified:
- `internal/app/cmd/create_prompt.go` — EXISTS (185 lines)
- `internal/app/cmd/create_prompt_test.go` — EXISTS (378 lines)
- `.planning/phases/86-km-create-prompt-queue/86-02-SUMMARY.md` — EXISTS

Commits verified:
- 219b047 — FOUND in git log
- aa0410e — FOUND in git log (linter auto-fix: ReconcileMetaStatus + PQ-08 GREEN)
- cc50c15 — FOUND in git log

## Self-Check: PASSED
