---
phase: 86-km-create-prompt-queue
plan: "05"
subsystem: cli/docs
tags: [wave-2, prompt-queue, agent-list, queue-view, tdd, pq-07]
dependency_graph:
  requires:
    - 86-01 (RED-state stubs: TestAgentListQueue stub)
    - 86-02 (--prompt flag + SSM push: PQ-01..PQ-04)
    - 86-03 (on-box runner + systemd unit: PQ-08)
  provides:
    - PQ-07: km agent list --queue flag + runAgentListQueue SSM rendering
    - CLAUDE.md CLI bullets updated for --prompt/--wait/--queue
    - OPERATOR-GUIDE.md section for km create --prompt usage + recovery
  affects:
    - 86-06 (UAT): all code-level PQ-NN requirements now GREEN
tech_stack:
  added: []
  patterns:
    - BoolVar flag on existing Cobra subcommand (--queue as opt-in alternate path)
    - pipe-delimited SSM output → fixed-width Go table rendering
    - Wave-0 stub pattern: inner t.Skip stubs replaced with real assertions
key_files:
  created: []
  modified:
    - internal/app/cmd/agent.go (+72 lines: --queue BoolVar, runAgentListQueue helper)
    - internal/app/cmd/agent_test.go (+140 lines: TestAgentListQueue GREEN x3 subtests, TestAgentListNoQueueFlag_UnchangedPath)
    - CLAUDE.md (+2 modified lines: km create + km agent list bullets)
    - OPERATOR-GUIDE.md (+72 lines: new '### km create --prompt' section)
decisions:
  - "--queue is a BoolVar on the existing agent list Cobra command (not a new subcommand) — CONTEXT.md §Observability: 'alternate code path inside the existing agent list Cobra command'"
  - "runAgentListQueue initializes real clients identically to runAgentList (nil-guard pattern) — consistent with all other run* helpers in agent.go"
  - "Empty queue dir returns 'no queue entries' with nil error — operator-friendly, matches 'no agent runs found' convention in runAgentList"
  - "Prompt preview defensively truncated to 80 chars in Go after shell already does head -c 80 — belt-and-suspenders"
  - "[Deviation Rule 1] Removed unused imports (errors/io/time) from create_prompt_test.go — linter auto-restored them (they ARE used by Plan 86-04 PQ-05/06 tests that landed earlier)"
metrics:
  duration: "~495 seconds (~8 min)"
  completed: "2026-05-20T03:19:43Z"
  tasks: 2
  files_created: 0
  files_modified: 4
---

# Phase 86 Plan 05: Wave 2 — `km agent list --queue` view + docs

Wave 2 closes out the last code-level PQ requirement (PQ-07) and adds the operator-facing documentation for the full `--prompt` + `--wait` + `--queue` surface. PQ-01 through PQ-08 are all GREEN.

## One-liner

`--queue` BoolVar on `km agent list` dispatches to `runAgentListQueue` (SSM shell enumerates queue meta.json + prompt preview, renders INDEX/STATUS/CREATED/PROMPT table); CLAUDE.md + OPERATOR-GUIDE.md updated with full `--prompt` usage + recovery.

## What Was Done

### Task 1: --queue flag + runAgentListQueue in agent.go

`internal/app/cmd/agent.go` changes (~72 lines):

1. **`newAgentListCmd`**: added `var queueView bool` + `cmd.Flags().BoolVar(&queueView, "queue", false, ...)` before `return cmd`. Updated Long description to mention `--queue`. RunE branches on `queueView`:
   ```go
   if queueView {
       return runAgentListQueue(ctx, cmd, cfg, fetcher, ssmClient, sandboxID)
   }
   return runAgentList(...)
   ```

2. **`runAgentListQueue`** (~55 lines, inserted after `runAgentList`):
   - Nil-guard initializes real AWS clients if not injected (mirrors `runAgentList`)
   - Fetches sandbox via `fetcher.FetchSandbox`, rejects stopped sandboxes
   - Extracts instance ID via `extractResourceID`
   - Issues `sendSSMAndWait` with shell command that enumerates `*.meta.json` in `sort` order, reads `jq -r .status`, `jq -r .created_at`, and `head -c 80` of paired `.prompt` file
   - Empty stdout → `"no queue entries"` (nil error)
   - Non-empty stdout → header row + fixed-width table: `%-5s %-9s %-22s %s` (INDEX STATUS CREATED PROMPT)
   - Defensive Go-side truncation of preview to 80 chars

### Task 2: TestAgentListQueue + TestAgentListNoQueueFlag_UnchangedPath

`internal/app/cmd/agent_test.go` changes (~140 lines):

- Added `"bytes"` import
- **`TestAgentListQueue`**: removed top-level `t.Skip` + inner sub-test skips. Three live subtests:
  - `flag_registered`: introspects `listCmd.Flags().Lookup("queue")`, verifies type=bool, default=false
  - `empty_queue`: mock SSM returns `""`, asserts output contains `"no queue entries"` and no `\d{3}|` pattern
  - `mixed_status`: mock SSM returns 5 pipe-delimited 4-field records (001..005, statuses: done/running/pending/failed/skipped), asserts all status labels present, INDEX/PROMPT headers present, 001 before 002, line lengths reasonable
- **`TestAgentListNoQueueFlag_UnchangedPath`**: regression guard — runs `agent list` without `--queue`, asserts no `INDEX` header and no `"no queue entries"`, asserts run ID is present

Test results:
```
--- PASS: TestAgentList (1.19s)
--- PASS: TestAgentListQueue (2.36s)
    --- PASS: TestAgentListQueue/flag_registered (0.00s)
    --- PASS: TestAgentListQueue/empty_queue (1.18s)
    --- PASS: TestAgentListQueue/mixed_status (1.18s)
--- PASS: TestAgentListNoQueueFlag_UnchangedPath (1.18s)
```

### Task 2: CLAUDE.md + OPERATOR-GUIDE.md

**CLAUDE.md** — two bullets updated:
- `km create` bullet: appended `` `--prompt <text-or-@file>` repeatable, `--wait` ``
- `km agent list` bullet: appended `` (`--queue` to list on-box prompt queue entries instead) ``

**OPERATOR-GUIDE.md** — new section `### km create --prompt — provision + queue prompts` (~72 lines) inserted in §5 between "Create the Sandbox" and "List and Check Status". Covers:
1. Basic usage (single prompt, multi-prompt chain, `--wait`, `--no-bedrock`)
2. Prompt syntax table (`text`, `@file`, `@@escape`)
3. `--wait` exit-code semantics
4. Observability (`km agent list --queue`, `km agent list`, `journalctl -u km-queue`, `km-queue.log`)
5. Failure model (linear-chain: failed → remaining skipped; auth wait indefinite; reboot reconcile)
6. Recovery procedure (rm queue/*, manual status edit + `systemctl start km-queue`)
7. Constraints (EC2-only, no per-prompt timeout/retry, Bedrock probe cost note, `--no-bedrock` credentials requirement)
8. Reference links to spec + BRIEF.md

## PQ-NN Status: All Code-Level Requirements GREEN

| ID | Test | Status |
|----|------|--------|
| PQ-01 | `TestCreatePromptFlag` | GREEN |
| PQ-02 | `TestResolvePrompts` | GREEN |
| PQ-03 | `TestCreatePromptDockerReject` | GREEN |
| PQ-04 | `TestPushQueueFiles` | GREEN |
| PQ-05 | `TestCreatePromptWait` | GREEN (Plan 86-04) |
| PQ-06 | `TestCreatePromptWaitFail` | GREEN (Plan 86-04) |
| PQ-07 | `TestAgentListQueue` | GREEN (this plan) |
| PQ-08 | `TestQueueRunnerStateMachine` | GREEN (Plan 86-03) |
| PQ-09..PQ-13 | Operator UAT | Pending (Plan 86-06) |
| R1 | Regression: `km create` without `--prompt` | Pending UAT |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Unused imports (errors/io/time) in create_prompt_test.go blocked build**
- **Found during:** Task 1 — initial `go test` run returned compile error on three unused imports
- **Issue:** `create_prompt_test.go` had `"errors"`, `"io"`, `"time"` in imports; appeared unused but are genuinely used by Plan 86-04 PQ-05/06 tests (`time.Millisecond`, `io.EOF`, `errors.Is`/`errors.As`)
- **Fix:** Attempted to remove them — linter immediately restored the correct full import set (the imports ARE needed). Build then succeeded. The initial compile error was a false signal from the compiler seeing an intermediate state.
- **Files modified:** `internal/app/cmd/create_prompt_test.go` (linter-restored, no net change)
- **Commit:** Captured in `5a35bfa` (compiler was satisfied by the linter's restored state)

## Commits

| Hash | Message |
|------|---------|
| 5a35bfa | feat(86-05): add --queue flag to agent list + flip TestAgentListQueue GREEN |
| 8752429 | docs(86-05): update CLAUDE.md CLI bullets + add OPERATOR-GUIDE.md --prompt section |

## Setup for Plan 86-06 (UAT)

Plan 86-06 covers the operator-UAT requirements (PQ-09..PQ-13, R1). These require:
- A real EC2 sandbox provisioned with `km create profiles/open-dev.yaml --prompt "..." --wait`
- Bedrock credentials available (or `claude auth login` for `--no-bedrock` mode)
- Verification of sequential execution (PQ-10), failure chain (PQ-11), pause/resume reconcile (PQ-12)
- R1 regression: `km create` without `--prompt` → `km doctor` shows unit installed+enabled+idle

The `km agent list --queue` command built in this plan is the primary observability tool for UAT.

## Self-Check: PASSED

Files verified:
- `internal/app/cmd/agent.go` — FOUND (contains runAgentListQueue, --queue BoolVar)
- `internal/app/cmd/agent_test.go` — FOUND (TestAgentListQueue GREEN x3 subtests)
- `.planning/phases/86-km-create-prompt-queue/86-05-SUMMARY.md` — FOUND

Commits verified:
- `5a35bfa` — FOUND in git log (feat: --queue flag + TestAgentListQueue GREEN)
- `8752429` — FOUND in git log (docs: CLAUDE.md + OPERATOR-GUIDE.md)
