---
phase: 73-km-vscode-remote-session-via-ssm
plan: "08"
subsystem: docs
tags: [vscode, documentation, operator-guide, ssh, ssm]
dependency_graph:
  requires: [73-05, 73-06, 73-07]
  provides: [operator-guide-vscode, claude-md-vscode-surface]
  affects: [CLAUDE.md, docs/vscode.md]
tech_stack:
  added: []
  patterns: [operator-guide, table-heavy-reference, code-block-lifecycle]
key_files:
  created:
    - docs/vscode.md
  modified:
    - CLAUDE.md
decisions:
  - "docs/vscode.md follows slack-notifications.md structural pattern: ToC, section headers, tables, code blocks"
  - "Network requirements (egress suffix table) included per 73-CONTEXT.md vscode-server network requirements"
  - "CLAUDE.md additions placed: CLI bullets after km slack rotate-token; section after Phase 68 block before Architecture"
metrics:
  duration_seconds: 158
  completed_date: "2026-05-08"
  tasks_completed: 2
  files_modified: 2
requirements: [GOAL-8]
---

# Phase 73 Plan 08: Documentation — VS Code Operator Guide Summary

One-liner: Operator-facing VS Code Remote-SSH guide (294-line docs/vscode.md) plus CLAUDE.md CLI surface for `km vscode start | status`.

## What Was Built

### Task 1: docs/vscode.md (294 lines)

New file covering the full operator journey, structured to match `docs/slack-notifications.md`:

| Section | Content |
|---|---|
| How it works | One-line architecture diagram, SSM tunnel + keypair overview |
| One-time setup | Install Remote-SSH extension, `make build && km init --sidecars` |
| Per-sandbox lifecycle | Full `km create → km vscode start → F1 connect → km destroy` workflow |
| Profile field | `vscodeEnabled` bool* table |
| CLI commands | `start` (with `--local-port`) and `status` descriptions |
| Operator state on disk | Key paths + modes table, sample ssh-config block |
| Sandbox-side state | Four POC lessons: enable+start sshd, sandbox ownership, restorecon, IdentitiesOnly |
| Network requirements | Egress suffix table (7 entries), telemetry hygiene, learn-mode discovery recipe |
| Troubleshooting | 6 failure modes with exact error messages and resolutions |
| Limitations | Per-machine keys, one operator per sandbox, no stop command, host-key trust deferred |
| Security model | 6-row table (SSM transport, ed25519 auth, blast radius, key storage, port exposure, pubkey scope) |

### Task 2: CLAUDE.md (52 lines added)

**CLI section** (line 35-36): Two new bullets inserted after `km slack rotate-token`:
- `km vscode start <sandbox-id>` — open SSM port-forward + ssh-config Host entry
- `km vscode status <sandbox-id>` — check sshd state + authorized_keys presence

**New section** (line 301-350): `## VS Code Remote-SSH (Phase 73)` containing:
- Profile field table (`vscodeEnabled`)
- Operator state table (3 paths)
- One-time setup code block (`make build && km init --sidecars`)
- Per-sandbox workflow code block (4 commands)
- Important workflow notes (4 bullets: km init requirement, retroactive provisioning, cross-machine gap, one operator limit)
- Link to `docs/vscode.md`

## Verification Results

| Check | Result |
|---|---|
| `make build` succeeds | PASS (v0.2.552, commit 583662e) |
| `wc -l docs/vscode.md` >= 80 | PASS (294 lines) |
| `grep -c "km vscode" CLAUDE.md` >= 3 | PASS (5 occurrences) |
| All required sections present in docs/vscode.md | PASS |
| Existing CLAUDE.md sections intact | PASS (Slack Notifications, Architecture, etc.) |

## Key Links Established

- `docs/vscode.md` references `km vscode start` (implemented in Plan 73-06)
- `CLAUDE.md` links to `docs/vscode.md` for full guide
- `docs/vscode.md` links to design spec (`docs/superpowers/specs/2026-05-06-km-vscode-design.md`)

## Commits

| Hash | Message |
|---|---|
| 10d1f3e | docs(73-08): add docs/vscode.md operator guide for VS Code Remote-SSH |
| 583662e | docs(73-08): add km vscode CLI entries and VS Code Remote-SSH section to CLAUDE.md |

## Deviations from Plan

None - plan executed exactly as written.

## Self-Check: PASSED

| Item | Status |
|---|---|
| `docs/vscode.md` exists | FOUND |
| `CLAUDE.md` exists | FOUND |
| `73-08-SUMMARY.md` exists | FOUND |
| commit 10d1f3e exists | FOUND |
| commit 583662e exists | FOUND |
