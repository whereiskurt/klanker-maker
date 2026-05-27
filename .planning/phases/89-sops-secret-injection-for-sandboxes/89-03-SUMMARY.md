---
phase: 89
plan: 03
subsystem: cli/init-configure
tags: [sops, s3-upload, gitignore, km-init, km-configure, tdd]
dependency_graph:
  requires: []
  provides:
    - s3://${bucket}/binaries/sops (S3 key contract consumed by 89-05 userdata)
    - .gitignore /secrets/* + !/secrets/*.enc.yaml convention
  affects:
    - internal/app/cmd/init.go (buildAndUploadSidecars gains sops upload step)
    - internal/app/cmd/configure.go (runConfigure gains gitignore append step)
tech_stack:
  added: []
  patterns:
    - PATH-shim test pattern (executable shell scripts for aws/curl)
    - tempdir round-trip test pattern for filesystem helpers
    - line-anchored gitignore matching (TrimSpace + map lookup)
key_files:
  created:
    - internal/app/cmd/configure_secrets_test.go
  modified:
    - internal/app/cmd/init.go
    - internal/app/cmd/init_test.go
    - internal/app/cmd/configure.go
decisions:
  - "Use exec.Command(curl, ...) directly (not bash -c) so PATH shims work deterministically in tests"
  - "Line-anchored gitignore matching (TrimSpace + map) instead of strings.Contains to prevent false hits on partial-match lines like 'unrelated/secrets/*foo'"
  - "Export FetchAndUploadSops/EnsureSecretsGitignore wrappers for _test package access — keeps production functions unexported"
  - "klanker-terraform literal used directly per project-wide convention (19 occurrences in init.go after this plan); no cfg.GetAWSProfile() method exists today"
metrics:
  duration: 833s
  completed_date: "2026-05-27"
  tasks_completed: 2
  files_modified: 4
---

# Phase 89 Plan 03: SOPS Binary Distribution + .gitignore Convention Summary

Two small Wave-0 pre-conditions for Phase 89 SOPS secret injection: the sops binary upload path through `km init --sidecars`, and the operator-repo .gitignore convention through `km configure`.

## What Was Built

### Task 1: fetchAndUploadSops + init.go wiring

`fetchAndUploadSops(buildDir, bucket)` downloads sops v3.13.1 linux/amd64 from GitHub releases (cached in `build/sops`) and uploads to `s3://{bucket}/binaries/sops` via `aws s3 cp --profile klanker-terraform`.

Key design decisions:
- Uses `exec.Command("curl", ...)` directly (not `bash -c` wrapper) so PATH shim tests intercept the lookup deterministically
- S3 key `binaries/sops` is the exact contract consumed by 89-05's userdata block
- Wired into `buildAndUploadSidecars` immediately after `fetchAndUploadOtelcolContrib`
- Returns a hard error (not a warn-and-continue) unlike otelcol-contrib — a missing sops binary causes 89-05's userdata to 404, so it's a fatal failure

WARNING 6 closure: verified `klanker-terraform` is the project-wide convention. Pre-existing count was 17 occurrences in init.go (plan said 25+, actual count is 17; the convention is the same regardless). After this plan the count is 19 (added FetchAndUploadSops wrapper + fetchAndUploadSops internal). No `cfg.GetAWSProfile()` method exists today. Code-adjacent comment documents the constraint for future refactor sweep.

### Task 2: ensureSecretsGitignore + configure.go wiring

`ensureSecretsGitignore(repoRoot)` idempotently appends two lines to `.gitignore`:
```
# Phase 89: SOPS-encrypted secrets (km configure)
/secrets/*
!/secrets/*.enc.yaml
```

Key design: line-anchored matching using `strings.TrimSpace` + map lookup per line, not `strings.Contains`. This prevents a substring false-hit where `unrelated/secrets/*foo` would incorrectly satisfy the `/secrets/*` requirement.

Wired into `runConfigure` after the `km-config.yaml` write step, using `outDir` (the effective output directory) as the repo root — the same path the config file is written into.

## Test Approach

**fetchAndUploadSops tests** (3 subtests): Use `t.Setenv("PATH", shimDir+os.PathListSeparator+os.Getenv("PATH"))` with executable shell scripts named `aws` and `curl` in a tempdir. Pattern mirrors `makeFakeTerragruntForBootstrap` in bootstrap_test.go.
- `UsesCacheWhenPresent`: pre-create `build/sops`, assert no curl call + exactly 1 aws s3 cp with `--profile klanker-terraform`
- `DownloadFailureReturnsError`: curl shim exits 1, assert non-nil error, aws NOT called
- `UploadFailureReturnsError`: pre-stage sops, aws shim exits 1, assert non-nil error

**ensureSecretsGitignore tests** (5 subtests): tempdir round-trips, no real filesystem outside temp.
- `EmptyGitignore`: missing .gitignore created with comment + both lines
- `IdempotentReRun`: second call is a no-op (byte-equal content)
- `BothLinesAlreadyPresent`: pre-write both lines, file unchanged after call
- `FirstLinePresentSecondMissing`: only missing line appended, no duplicate of first
- `PartialMatchAvoidsFalseHit`: `unrelated/secrets/*foo` does NOT satisfy `/secrets/*` requirement — forced correct line-anchored behavior

## Notable Deviation from RESEARCH.md

RESEARCH.md's gitignore prototype used `strings.Contains(body, line)` for presence detection. This has a known pitfall: the line `!/secrets/*.enc.yaml` contains `/secrets/*` as a substring, causing a false hit when checking whether `/secrets/*` is present.

The `PartialMatchAvoidsFalseHit` test was written specifically to force the correct behavior, requiring line-anchored matching (exact line equality after `TrimSpace`). Production code uses a `map[string]bool` built from per-line splits. This is documented in the production code comment.

## WARNING 6 Closure

Verified pre-execution: `klanker-terraform` literal appears 17 times in init.go before this plan (not 25+ as the plan estimated — the count had drifted but the convention holds). All sidecar-upload helpers use this literal. No `cfg.GetAWSProfile()` method exists on `*config.Config`. New helper joins the convention; code-adjacent comment added for future refactor traceability.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Pre-existing broken Go syntax in bootstrap_secrets_test.go**
- **Found during:** Task 2 (needed test package to compile to run TestEnsureSecretsGitignore)
- **Issue:** `getenvForTest` function used `import "os"` inside function body (invalid Go); `getenvForTest` called non-existent `envLookup(key)`. Also missing `"os"` import at file level.
- **Fix:** Replaced broken function body with `return os.Getenv(key)`, added `"os"` to imports
- **Files modified:** internal/app/cmd/bootstrap_secrets_test.go (linter reconciled to HEAD)
- **Note:** The linter reconciled the file to HEAD state since bootstrap_secrets_test.go was already committed in the 89-04 commit. Changes were absorbed cleanly.

**2. [Rule 2 - Missing functionality] Test assertion bug in FirstLinePresentSecondMissing**
- **Found during:** Task 2 GREEN phase (test ran but failed)
- **Issue:** Test used `strings.Count(got, line1)` which found `/secrets/*` twice — once as a standalone line and once as a substring inside `!/secrets/*.enc.yaml`
- **Fix:** Replaced substring count with line-anchored count using `strings.Split + TrimSpace == line1` comparison
- **Files modified:** internal/app/cmd/configure_secrets_test.go

## Operator Follow-up Required

`km init --sidecars` must be run AFTER this plan lands to push the sops binary to S3. Until the binary is in `s3://${KM_ARTIFACTS_BUCKET}/binaries/sops`, the userdata block from 89-05 will 404 on bundle fetch during sandbox boot.

## Self-Check: PASSED

- FOUND: internal/app/cmd/init.go
- FOUND: internal/app/cmd/configure.go
- FOUND: internal/app/cmd/init_test.go
- FOUND: internal/app/cmd/configure_secrets_test.go
- FOUND: commit 8f2a44d (Task 1 — fetchAndUploadSops)
- FOUND: commit bd42dae (Task 2 — ensureSecretsGitignore)
- FOUND: fetchAndUploadSops in init.go
- FOUND: ensureSecretsGitignore in configure.go
