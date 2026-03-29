---
phase: 32-profile-scoped-rsync-paths-with-external-file-lists-and-shell-wildcards
verified: 2026-03-29T00:00:00Z
status: passed
score: 9/9 must-haves verified
re_verification:
  previous_status: human_needed
  previous_score: 8/8 (with 2 human_needed items)
  gaps_closed:
    - "TestRsyncSaveCmd added with 4 sub-tests covering RSYNC-06 tar command format (buildTarShellCmd extracted for testability)"
    - "Human verification approved: wildcard expansion works on live sandbox"
    - "Human verification approved: pre-phase-32 sandboxes fall back to global rsync_paths without error"
  gaps_remaining: []
  regressions: []
---

# Phase 32: Profile-scoped rsync paths with external file lists and shell wildcards — Verification Report

**Phase Goal:** Move rsync path configuration from global km-config.yaml into per-profile YAML with external file list references and shell wildcard support
**Verified:** 2026-03-29
**Status:** passed
**Re-verification:** Yes — after gap closure plan 32-03

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Profile YAML with rsyncPaths array parses into ExecutionSpec.RsyncPaths | VERIFIED | `types.go` lines 161-164: `RsyncPaths []string yaml:"rsyncPaths,omitempty"`. `TestRsyncPathsParsing/rsyncPaths_parses_into_slice` PASS |
| 2 | Profile YAML with rsyncFileList string parses into ExecutionSpec.RsyncFileList | VERIFIED | `types.go` lines 165-167: `RsyncFileList string yaml:"rsyncFileList,omitempty"`. `TestRsyncPathsParsing/rsyncFileList_parses_into_string` PASS |
| 3 | JSON schema accepts rsyncPaths and rsyncFileList in execution block | VERIFIED | `sandbox_profile.schema.json` lines 237-245: both properties defined with correct types. `TestRsyncSchemaValidation` 5/5 sub-tests PASS |
| 4 | Profile without rsyncPaths/rsyncFileList validates cleanly (backward compat) | VERIFIED | Both fields omitempty. `TestRsyncPathsParsing/no_rsyncPaths_or_rsyncFileList_is_backward_compatible` PASS. `TestRsyncSchemaValidation/omitting_rsyncPaths_and_rsyncFileList_is_valid` PASS |
| 5 | km rsync save uses profile rsyncPaths when available instead of global config | VERIFIED | `rsync.go` lines 161-183: S3 profile fetch + `resolveRsyncPaths` call. `TestResolveRsyncPaths/profile_rsyncPaths_returned_instead_of_global` PASS |
| 6 | km rsync save loads and merges external rsyncFileList YAML entries | VERIFIED | `rsync.go` `loadFileList` + `resolveRsyncPaths` merge with deduplication. `TestResolveRsyncPaths/profile_with_rsyncPaths_and_rsyncFileList_merges_and_deduplicates` PASS. `TestLoadFileList` 2/2 PASS |
| 7 | km rsync save generates unquoted paths in tar command for wildcard expansion | VERIFIED | `rsync.go` `buildTarShellCmd` at line 131: `strings.Join(paths, " ")` unquoted. `TestRsyncSaveCmd/wildcard_path_appears_unquoted_in_for-loop` PASS (asserts no single-quoting). Human confirmed on live sandbox |
| 8 | Paths with shell metacharacters are rejected | VERIFIED | `rsync.go` lines 28-37: regex `^[a-zA-Z0-9_./*?\[\]-]+$`. `TestValidateRsyncPath` 15/15 sub-tests PASS |
| 9 | Sandboxes without profile rsyncPaths fall back to global cfg.RsyncPaths | VERIFIED | `rsync.go` lines 65-67: nil/empty profile returns `globalFallback`. `TestResolveRsyncPaths/profile_without_rsyncPaths_falls_back_to_global` and `nil_profile_falls_back_to_global` PASS. Human confirmed on pre-phase-32 sandbox |

**Score:** 9/9 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/profile/types.go` | RsyncPaths and RsyncFileList fields on ExecutionSpec | VERIFIED | Lines 161-167: both fields present with correct types, yaml tags, omitempty, and doc comments |
| `pkg/profile/schemas/sandbox_profile.schema.json` | Schema definitions for rsyncPaths array and rsyncFileList string | VERIFIED | Lines 237-245: rsyncPaths as array of strings with items constraint; rsyncFileList as string; both with descriptions |
| `internal/app/cmd/rsync.go` | resolveRsyncPaths helper, validateRsyncPath, loadFileList, buildTarShellCmd helper, profile-aware save command | VERIFIED | Helpers at lines 28-144; save command at lines 204-215 delegates to `buildTarShellCmd` |
| `internal/app/cmd/rsync_test.go` | Unit tests for path resolution, validation, file list loading, fallback, tar command format | VERIFIED | TestResolveRsyncPaths (5), TestLoadFileList (2), TestValidateRsyncPath (15), TestRsyncSaveCmd (4) — all 26 sub-tests PASS |
| `pkg/profile/types_test.go` | TestRsyncPathsParsing (3 sub-tests) | VERIFIED | All 3 sub-tests PASS |
| `pkg/profile/validate_test.go` | TestRsyncSchemaValidation (5 sub-tests) | VERIFIED | All 5 sub-tests PASS |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `internal/app/cmd/rsync.go` | `pkg/profile/types.go` | reads ExecutionSpec.RsyncPaths and ExecutionSpec.RsyncFileList | WIRED | `resolveRsyncPaths` at lines 65, 73, 84 directly reads `prof.Spec.Execution.RsyncPaths` and `prof.Spec.Execution.RsyncFileList` |
| `internal/app/cmd/rsync.go` | S3 `artifacts/{sandbox-id}/.km-profile.yaml` | fetches stored profile for path resolution | WIRED | Lines 162-173: GetObject with `profileKey := fmt.Sprintf("artifacts/%s/.km-profile.yaml", sandboxID)`, best-effort |
| `internal/app/cmd/rsync_test.go` | `internal/app/cmd/rsync.go` | TestRsyncSaveCmd calls buildTarShellCmd | WIRED | rsync_test.go lines 152, 160, 172, 183: direct calls to `buildTarShellCmd` confirming unquoted path format |
| `pkg/profile/types.go` | `pkg/profile/schemas/sandbox_profile.schema.json` | YAML field names match JSON schema property names | WIRED | Go fields `rsyncPaths`/`rsyncFileList` match schema property names exactly; schema accepted by `km validate` (TestRsyncSchemaValidation) |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| RSYNC-01 | 32-01 | rsyncPaths in profile YAML parses correctly into ExecutionSpec | SATISFIED | `TestRsyncPathsParsing/rsyncPaths_parses_into_slice` PASS |
| RSYNC-02 | 32-02 | rsyncFileList external YAML loaded and merged with rsyncPaths | SATISFIED | `TestLoadFileList/parses_paths_array_correctly` PASS; `TestResolveRsyncPaths/profile_with_rsyncPaths_and_rsyncFileList_merges_and_deduplicates` PASS |
| RSYNC-03 | 32-02 | Wildcard paths pass validation; shell-injecting paths rejected | SATISFIED | `TestValidateRsyncPath` 15/15 sub-tests PASS |
| RSYNC-04 | 32-02 | Profile without rsyncPaths falls back to global cfg.RsyncPaths | SATISFIED | `TestResolveRsyncPaths/profile_without_rsyncPaths_falls_back_to_global` and `nil_profile_falls_back_to_global` PASS. Human confirmed on pre-phase-32 sandbox |
| RSYNC-05 | 32-01 | JSON schema validates rsyncPaths array and rsyncFileList string | SATISFIED | `TestRsyncSchemaValidation` 5/5 PASS |
| RSYNC-06 | 32-02, 32-03 | km rsync save generates tar command without path quoting when wildcards present | SATISFIED | `buildTarShellCmd` extracted at rsync.go line 131. `TestRsyncSaveCmd` 4/4 sub-tests PASS — explicitly asserts `for p in projects/*/config .claude;` format and confirms absence of single-quoting. Human confirmed wildcard expansion on live sandbox |

**Note on RSYNC requirements in REQUIREMENTS.md:** RSYNC-01 through RSYNC-06 are declared in ROADMAP.md Phase 32 and defined in `32-RESEARCH.md` but are NOT registered in `.planning/REQUIREMENTS.md`. The requirements tracking table in REQUIREMENTS.md ends at Phase 7 and does not cover Phase 32. These are orphaned from the central requirements registry — an administrative gap in planning documents, not a code gap.

### Anti-Patterns Found

No anti-patterns detected. Scanned files modified by plans 32-01 through 32-03:
- `pkg/profile/types.go` — no TODOs, stubs, or placeholder returns
- `pkg/profile/schemas/sandbox_profile.schema.json` — rsync properties fully specified
- `internal/app/cmd/rsync.go` — `buildTarShellCmd` is a complete, substantive implementation; no empty stubs
- `internal/app/cmd/rsync_test.go` — 26 total sub-tests across 4 test functions; no placeholder assertions

### Summary

Phase 32 goal is fully achieved. All gaps from the previous `human_needed` verification are closed:

- `buildTarShellCmd` extracted from inline code at commit `d68e0d6` — enables deterministic unit testing of the tar command format
- `TestRsyncSaveCmd` added at commit `885b6e2` — 4 sub-tests covering literal paths, wildcard paths, bucket/key injection, and single-path edge case. RSYNC-06 is now fully covered by automated assertion
- Human confirmed wildcard expansion on a live sandbox: `projects/*/config` resolved to matching directories via bash glob (not literal string)
- Human confirmed graceful fallback for pre-phase-32 sandboxes: `km rsync save` used global `rsync_paths` without error when no profile was found in S3

Full test suite passes (26 rsync sub-tests + all other packages green). Binary builds cleanly. No regressions from the 32-03 refactor.

---

_Verified: 2026-03-29_
_Verifier: Claude (gsd-verifier)_
