---
phase: 106-session-resume-hint-on-github-hackerone-bridge-replies-post-on-mint
plan: "04"
subsystem: documentation
tags: [docs-only, phase-106, github-bridge, h1-bridge, resume-hint, post-on-mint, internal-only, deploy-surface]
dependency_graph:
  requires: [RESUME-HINT-GITHUB, RESUME-HINT-H1]
  provides: [RESUME-HINT-DOCS]
  affects: [docs/github-bridge.md, docs/h1-bridge.md, CLAUDE.md, skills/init/SKILL.md]
tech_stack:
  added: []
  patterns: [doc-phase-note, reverse-chronological-CLAUDE, skills-rollout-template]
key_files:
  created: []
  modified:
    - docs/github-bridge.md
    - docs/h1-bridge.md
    - CLAUDE.md
    - skills/init/SKILL.md
decisions:
  - "Documented /workspace as the locked run-from directory in both bridge docs (session transcript lives at /home/sandbox/.claude/projects/-workspace/<id>.jsonl but --resume keys off CWD)"
  - "H1 internal-only safety property called out prominently in h1-bridge.md Phase 106 section (no --reply-to-researcher — researcher visibility is a P0 bug)"
  - "SKILL.md rollout template scoped --sidecars to sidecar binaries only; create-handler-embedded userdata edits (Phase 106 class) routed to make build-lambdas + km init --dry-run=false"
  - "Phase 106 CLAUDE.md note inserted above Phase 105 (reverse-chronological order)"
metrics:
  duration: 131s
  completed: "2026-06-12T03:02:03Z"
  tasks_completed: 3
  files_modified: 4
---

# Phase 106 Plan 04: Operator documentation — resume-hint deploy surface + SKILL.md correction

Phase 106 operator documentation: Phase 106 sections in both bridge docs, phase note in CLAUDE.md, and corrected rollout-sequence guidance in skills/init/SKILL.md. All four files are mutually consistent with the shipped behavior from Plans 02/03.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Add Phase 106 sections to github-bridge.md and h1-bridge.md | d6f420d4 | docs/github-bridge.md, docs/h1-bridge.md |
| 2 | Add Phase 106 note to CLAUDE.md | 1e874c24 | CLAUDE.md |
| 3 | Correct create-handler-userdata deploy guidance in skills/init/SKILL.md | 3a75f525 | skills/init/SKILL.md |

## What Was Built

**Task 1:** Appended `## Phase 106 — Resume-hint on bridge replies (post-on-mint)` to the end of both bridge docs.

- `docs/github-bridge.md`: Documents the collapsed `<details>` hint fold visible to repo collaborators, post-on-mint semantics (fires only on `NEW_GITHUB_SESSION != ${GITHUB_SESSION:-}`), run-from `/workspace`, agent-correct resume commands (`claude --resume` / `codex exec resume`), best-effort `|| true` guard, and deploy sequence (`make build-lambdas` + `km init --dry-run=false` + recreate). Notes Slack is excluded.
- `docs/h1-bridge.md`: Same structure plus prominent **INTERNAL-only safety property** section explaining that the hint is posted via `km-h1 comment` without `--reply-to-researcher` — it lands on the internal/team comment track and is never visible to the external HackerOne researcher.

**Task 2:** Inserted Phase 106 (complete) note above the Phase 105 note in CLAUDE.md (line 21 vs 30), consistent with reverse-chronological order. The note covers post-on-mint semantics, `/workspace` run-from, H1 internal-only property, Slack exclusion, pure userdata change, and the exact deploy surface.

**Task 3:** Corrected the `## Rollout sequence template` section in `skills/init/SKILL.md`:

- Old inaccurate line: `km init --sidecars  # if sidecars/* or userdata template changed`
- Scoped `--sidecars` to sidecar binaries only (what it actually does).
- Added `make build-lambdas` + `km init --dry-run=false` as the correct path for create-handler-embedded userdata edits (Phase 106 class: `pkg/compiler/userdata.go` changes).
- Added an explanatory note distinguishing `--sidecars` vs `build-lambdas`+full-init vs scoped `--github`/`--h1`.
- Cited Phase 106 as the concrete example of this change class.
- Consistent with the Fast-path boundary table already present at ~line 97-100.

## Verification Results

| Check | Result |
|-------|--------|
| `grep '## Phase 106' docs/github-bridge.md` | FOUND (line 1747) |
| `grep '## Phase 106' docs/h1-bridge.md` | FOUND (line 435) |
| `/workspace` in github-bridge.md Phase 106 section | FOUND |
| `/workspace` in h1-bridge.md Phase 106 section | FOUND |
| `make build-lambdas` in github-bridge.md | FOUND |
| `internal` (INTERNAL-only property) in h1-bridge.md | FOUND |
| `grep 'Phase 106' CLAUDE.md` | FOUND (line 21, above Phase 105 at line 30) |
| `grep 'make build-lambdas' CLAUDE.md` | FOUND |
| `grep 'create-handler' skills/init/SKILL.md` | FOUND (corrected guidance) |
| `grep 'make build-lambdas' skills/init/SKILL.md` | FOUND |
| Old inaccurate `--sidecars ... userdata template changed` line removed | CONFIRMED |

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- `docs/github-bridge.md` — FOUND
- `docs/h1-bridge.md` — FOUND
- `CLAUDE.md` — FOUND
- `skills/init/SKILL.md` — FOUND
- Commit d6f420d4 — FOUND
- Commit 1e874c24 — FOUND
- Commit 3a75f525 — FOUND
- Phase 106 note above Phase 105 in CLAUDE.md — CONFIRMED (line 21 vs 30)
- `/workspace` in both bridge doc Phase 106 sections — CONFIRMED
- INTERNAL-only property in h1-bridge.md — CONFIRMED
- `--sidecars` no longer claims to cover userdata template changes in SKILL.md — CONFIRMED
