---
phase: 120-profiles-reset-and-os-layered-fragment-library
plan: 03
subsystem: profiles
tags: [file-move, test-path-update, byte-identity, golden-tests, archive]
dependency_graph:
  requires: []
  provides: [testdata/profiles/ archive of 19 retired profiles, green test suite]
  affects: [pkg/compiler tests, pkg/profile tests, internal/app/cmd tests]
tech_stack:
  added: []
  patterns: [dual-searchPath pattern for Resolve when leaf and base/* live in separate dirs]
key_files:
  created: []
  modified:
    - testdata/profiles/learn.v2.yaml
    - testdata/profiles/learn.v2.chatty.yaml
    - testdata/profiles/learn.v2.codex.yaml
    - testdata/profiles/learn.v2.polite.yaml
    - testdata/profiles/learn.v2.parallel.yaml
    - testdata/profiles/learn.v2.private-allow.yaml
    - testdata/profiles/learn.v2.desktop.yaml
    - testdata/profiles/dc34.yaml
    - testdata/profiles/dc34.ami.yaml
    - testdata/profiles/codex.yaml
    - testdata/profiles/locked.yaml
    - testdata/profiles/locked.ami.yaml
    - testdata/profiles/github-review.yaml
    - testdata/profiles/ao.yaml
    - testdata/profiles/goose.yaml
    - testdata/profiles/example-additional-snapshots.yaml
    - testdata/profiles/h1-triage.yaml
    - testdata/profiles/check-triage.yaml
    - testdata/profiles/desktop.legacy.yaml
    - pkg/compiler/userdata_phase92_byte_identity_test.go
    - pkg/compiler/agent_claude_golden_test.go
    - pkg/compiler/agent_codex_golden_test.go
    - pkg/profile/github_review_secrets_test.go
    - internal/app/cmd/validate_test.go
decisions:
  - "Dual searchPath pattern: pass both testdata/profiles/ and profiles/ to profile.Resolve so archived profiles extending base/* fragments can still find them"
  - "TestValidateBuiltinProfile updated to testdataPath(goose.yaml) since profiles/goose.yaml was archived"
metrics:
  duration: 1761s
  completed_date: "2026-06-26"
  tasks: 3
  files: 24
requirements: [R3, R4]
---

# Phase 120 Plan 03: Archive Retired Profiles + Update Test Paths Summary

Archived 19 retired demo profiles and frozen byte-identity input fixtures from
`profiles/` to `testdata/profiles/` via `git mv`, updated 7 test path constants
(+2 search-path fixes) so the byte-identity, golden, and secrets tests stay green.

## Tasks Completed

### Task 1: git mv all retired demos + frozen fixtures into testdata/profiles/

**Status:** Complete (committed as part of 120-01 commit `1a6f9dd7`)

Note: The file moves were already present in the `1a6f9dd7` commit from Plan 120-01
(which bundled the OS fragment authoring with the file moves). The executor confirmed
all 19 files are present in `testdata/profiles/` and absent from `profiles/`:

- 18 same-name moves: `learn.v2.yaml`, `learn.v2.chatty.yaml`, `learn.v2.codex.yaml`,
  `learn.v2.polite.yaml`, `learn.v2.parallel.yaml`, `learn.v2.private-allow.yaml`,
  `learn.v2.desktop.yaml`, `dc34.yaml`, `dc34.ami.yaml`, `codex.yaml`, `locked.yaml`,
  `locked.ami.yaml`, `github-review.yaml`, `ao.yaml`, `goose.yaml`,
  `example-additional-snapshots.yaml`, `h1-triage.yaml`, `check-triage.yaml`
- Special: `profiles/desktop.yaml` (old monolith, no `extends:`) →
  `testdata/profiles/desktop.legacy.yaml`
- All 19 moves were pure git renames (R100, zero byte changes)

### Task 2: Update the 6 test files / 7 path sites to testdata/profiles/

**Status:** Complete (commit `e84f1780`)

Applied all 7 edits from the plan:
1. `pkg/compiler/userdata_phase92_byte_identity_test.go:34` — `filepath.Join(repoRoot, "profiles")` → `filepath.Join(repoRoot, "testdata", "profiles")`
2-5. `pkg/compiler/agent_claude_golden_test.go:41-44` — four fixture path strings updated to `../../testdata/profiles/{learn.v2,dc34,locked,codex}.yaml`
6. `pkg/compiler/agent_codex_golden_test.go:32` — `const profilePath` updated to `../../testdata/profiles/codex.yaml`
7. `pkg/profile/github_review_secrets_test.go:32` — `filepath.Join` updated to include `"testdata"` segment

Belt-and-suspenders audit (`grep -rn 'profiles/' --include='*_test.go'`) reviewed all hits:
all remaining references are config string values, comments, phantom tempdir names
(`sealed.yaml`, `review.yaml`, `frontend.yaml`, `backend.yaml`), S3 key strings, or
`@profiles/` prompt-file references. None are real file reads to moved profiles.

### Task 3: Run the byte-identity + golden + cmd suite (green gate)

**Status:** Complete (commit `62351cd9`)

`go test ./pkg/compiler/... ./pkg/profile/... ./internal/app/cmd/... -count=1 -timeout 600s`
exit code: **0** (all three packages OK).

Specific tests verified green:
- `TestUserdataLearnV2Phase92ByteIdentity` — byte-identical
- `TestUserdataKmPrefixByteIdentity` — byte-identical
- `TestSlackInboundSteerEncouragesRichFormatting` — green
- `TestSynthesizeClaudeSettingsGolden` (learn.v2, dc34, locked, codex) — golden match
- `TestSynthesizeCodexConfigGolden` — golden match
- `TestGitHubReviewProfileSecrets` — sopsFile present, useBedrock false
- `TestValidateBuiltinProfile` — green after fix (see Deviations)

`userdata_learn_v2_pre92_baseline.golden.sh` NOT re-captured.
Golden OUTPUT files (`pkg/compiler/testdata/*.golden.*`) NOT moved.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Archived profiles that extend base/* fragments fail to resolve when only testdata/profiles/ is in the search path**

- **Found during:** Task 3 (first test run)
- **Issue:** `learn.v2.yaml`, `dc34.yaml`, `locked.yaml` all have `extends: [base/safenetwork, base/sidecars-all, ...]`. After archiving to `testdata/profiles/`, the `profile.Resolve` call only had `testdata/profiles/` in its searchPaths. The resolver could find the leaf but when it recursed to load parent `base/safenetwork`, it tried `testdata/profiles/base/safenetwork.yaml` (doesn't exist — base fragments live in `profiles/base/`).
- **Fix:** Added `profiles/` as a second search path entry in both tests:
  - `userdata_phase92_byte_identity_test.go`: `profile.Resolve("learn.v2", []string{profilesDir, profilesBaseDir})` where `profilesBaseDir = filepath.Join(repoRoot, "profiles")`
  - `agent_claude_golden_test.go`: same dual searchPath pattern for the 4 golden fixture resolutions
- **Files modified:** `pkg/compiler/userdata_phase92_byte_identity_test.go`, `pkg/compiler/agent_claude_golden_test.go`
- **Commit:** `62351cd9`

**2. [Rule 1 - Bug] TestValidateBuiltinProfile referenced the archived profiles/goose.yaml**

- **Found during:** Task 3 (first test run)
- **Issue:** `internal/app/cmd/validate_test.go:TestValidateBuiltinProfile` used `profilesPath(t, "goose.yaml")` → `profiles/goose.yaml`, which was archived to `testdata/profiles/goose.yaml` in Task 1. This is an 8th test path update site not in the original 7-site list.
- **Fix:** Changed `profilesPath(t, "goose.yaml")` → `testdataPath(t, "goose.yaml")` with an updated comment noting the Phase 120 archive.
- **Files modified:** `internal/app/cmd/validate_test.go`
- **Commit:** `62351cd9`

## Self-Check: PASSED

- FOUND: testdata/profiles/learn.v2.yaml
- FOUND: testdata/profiles/github-review.yaml
- FOUND: testdata/profiles/desktop.legacy.yaml
- FOUND commit: 1a6f9dd7 (file moves, from 120-01)
- FOUND commit: e84f1780 (7 test path edits)
- FOUND commit: 62351cd9 (dual searchPath fix + validate test fix)
