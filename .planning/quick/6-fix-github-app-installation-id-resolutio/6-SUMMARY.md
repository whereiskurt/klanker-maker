---
phase: 6-fix-github-app-installation-id-resolutio
plan: 1
subsystem: github-app-token-integration
tags: [github-app, ssm, installation-id, wildcard, learn-mode, bugfix, tdd]
one_liner: "resolveInstallationID now enumerates /km/config/github/installations/ via GetParametersByPath when allowedRepos is wildcard-only, surfacing single-installation auto-select / multi-installation ambiguity / zero-installation legacy fallback — fixes silent ⊘ skip on learn.yaml"
status: complete
operator_gate: PENDING

requires:
  - SSMGetPutAPI interface (internal/app/cmd/create.go)
  - resolveInstallationID (internal/app/cmd/create.go)
  - extractRepoOwner (internal/app/cmd/create.go) — UNCHANGED
  - generateAndStoreGitHubToken caller block (internal/app/cmd/create.go:980-1003)
  - SSM parameters under /km/config/github/installations/ (operator-managed)
  - legacy /km/config/github/installation-id (operator-managed, optional fallback)

provides:
  - ErrAmbiguousInstallation typed error with Candidates []string
  - SSMGetPutAPI.GetParametersByPath method on the interface
  - wildcard-only enumeration branch in resolveInstallationID
  - differentiated caller warning (⊘ not-configured / ⚠ ambiguous / log.Warn unexpected)

affects:
  - km create profiles/learn.yaml — now successfully provisions a token when exactly one installation is configured under /km/config/github/installations/
  - km create with multi-tenant SSM + wildcard-only allowedRepos — surfaces a loud actionable warning instead of a silent skip
  - all existing concrete-owner code paths — unchanged (regression guard in place)

tech_stack:
  added:
    - none (uses existing aws-sdk-go-v2 SSM client + standard library sort/strings/errors)
  patterns:
    - typed error with errors.As recovery (replaces sentinel-only error handling at the caller)
    - SSM hierarchical enumeration via GetParametersByPath (mirrors doctor.go:468-481 idiom)
    - graceful-degradation chain: enumeration -> legacy -> ErrGitHubNotConfigured

key_files:
  created: []
  modified:
    - internal/app/cmd/create.go
    - internal/app/cmd/create_github_test.go

decisions:
  - typed error (struct) rather than wrapped sentinel — gives the caller a clean errors.As recovery path AND lets the Error() string print the candidate list for the operator
  - enumeration runs ONLY on the wildcard/bare-repo branch — concrete-owner path is untouched (regression guard asserts pathCallCount == 0)
  - non-nil GetParametersByPath error -> fall through to legacy lookup (do NOT block) — preserves graceful degradation if the IAM policy lacks ssm:GetParametersByPath but a working legacy installation-id exists
  - errors.Is(ErrGitHubNotConfigured) checked BEFORE errors.As(*ErrAmbiguousInstallation) at the caller — zero-installations + no-legacy still emits the quiet ⊘ line, only ≥2 installations emit the loud ⚠

metrics:
  duration: "9m"
  tasks_completed: 3
  files_modified: 2
  commits: 3
  tests_added: 5  # plus 1 caller source-level test extension
  tests_passing: 20  # all in-scope create_github_test.go tests
  completed_date: "2026-05-01"
---

# Phase quick-6 Plan 1: Fix GitHub App Installation-ID Resolution Summary

## Root Cause

`extractRepoOwner("*")` returns `""` by design (and likewise for bare repos and empty strings), so `resolveInstallationID` had no concrete owner to feed into the per-account SSM key. The function then fell straight through to the legacy `/km/config/github/installation-id` parameter — which is unset on most modern multi-installation deployments — and returned `ErrGitHubNotConfigured`. The caller at `create.go:994` silently printed `⊘ GitHub token: skipped (not configured)`, no token landed in `/sandbox/{id}/github-token`, and the in-sandbox `GIT_ASKPASS` helper had nothing to feed `git`. The end result: `km create profiles/learn.yaml` (allowedRepos `["*"]`) provisioned successfully but `git clone` inside the sandbox prompted for a username — exactly the bug GH-FIX-01..05 set out to close.

## Fix Shape

Three layered changes, all in `internal/app/cmd/create.go`:

1. **Interface extension** — `SSMGetPutAPI` gained `GetParametersByPath`. `*ssm.Client` already satisfies the new method (proved by package compile); no production-side wiring change needed.

2. **New typed error** — `ErrAmbiguousInstallation{Candidates []string}` with a pointer-receiver `Error()` that names BOTH fix paths (legacy installation-id key OR owner-prefixed `allowedRepos` entry). Callers recover the candidate list via `errors.As(err, &target)` where `target` is `*ErrAmbiguousInstallation`.

3. **Enumeration branch in `resolveInstallationID`** — when `firstOwner == ""` (wildcard or bare-repo), enumerate `/km/config/github/installations/` via `GetParametersByPath`:
   - exactly 1 parameter → auto-select and return
   - ≥2 parameters → return `&ErrAmbiguousInstallation{Candidates: <sorted>}`
   - 0 parameters (or enumeration error) → fall through to legacy lookup as before

   The concrete-owner branch (`firstOwner != ""`) is wrapped in an `if/else` so it MUST NOT call `GetParametersByPath` — locked in by `TestResolveInstallationID_ConcreteOwner_StillUsesPerOwnerKey_RegressionGuard` which asserts `mock.pathCallCount == 0`.

4. **Differentiated caller warning** at `create.go:1019-1042` — replaced the binary `if errors.Is(...) / else log.Warn` with a three-branch switch:
   - `errors.Is(ErrGitHubNotConfigured)` → quiet `⊘ skipped (not configured)` (preserves zero-installation deployments)
   - `errors.As(*ErrAmbiguousInstallation)` → loud `⚠ skipped — ambiguous installation` with candidate list and BOTH fix-path suggestions on stderr, plus a structured zerolog warn carrying the candidates
   - default → existing `log.Warn` for unexpected errors

   Order matters: `errors.Is(ErrGitHubNotConfigured)` is checked first, so the zero-installation+no-legacy case still gets the quiet ⊘ line.

## Files Changed

- `internal/app/cmd/create.go` — interface extension, typed error, enumeration branch, three-branch caller switch (+73, -7 from Task 2 + +15, -2 from Task 3 = ~88 net new lines)
- `internal/app/cmd/create_github_test.go` — `mockSSMGetPut` extended with `pathResults` map + `pathCallCount` counter + `GetParametersByPath` method; five new `TestResolveInstallationID_*` cases; new `TestCreateGitHubCaller_DifferentiatesAmbiguity` source-level test (+200 lines)

## Files NOT Changed (Deliberately)

- `pkg/github/token.go` — already strips `repositories` from the access-token request when any allowed-repo entry is `*`, so once the caller hands it the right installation ID + sandbox-signed JWT, the wildcard-scoped token comes back correctly. No change needed at the exchange layer.
- `profiles/learn.yaml` — already declares `allowedRepos: ["*"]`, which is the post-fix world's correct shape. The bug was below it in the resolver, not in the profile.
- `internal/app/cmd/doctor.go` — already uses the same `GetParametersByPath` enumeration idiom we mirrored; the doctor check was healthy and informative pre-fix.

## Commits

| # | Hash    | Message                                                                       |
|---|---------|-------------------------------------------------------------------------------|
| 1 | f8f5c27 | test(6): add failing tests for wildcard-only installation enumeration         |
| 2 | 74a190c | feat(6): resolve wildcard-only installation IDs via SSM enumeration           |
| 3 | 6a7a7d4 | feat(6): differentiate ambiguous-installation warning from silent skip        |

## Verification (executor-side)

- `go build ./...` succeeds.
- `go test ./internal/app/cmd/ -run 'TestResolveInstallationID|TestCreateGitHubSkip|TestExtractRepoOwner|TestGenerateAndStoreGitHubToken|TestCreateGitHubCaller'` — all 20 tests PASS.
- `grep -n 'ErrAmbiguousInstallation\|GetParametersByPath\|⚠ GitHub token' internal/app/cmd/create.go` shows the type definition + interface method + the warning glyph in the caller.
- `git diff --stat pkg/github/token.go profiles/learn.yaml` is empty.
- TDD discipline observed: Task 1 commit was RED (compile error: `ErrAmbiguousInstallation undefined`); Task 2 commit turned 5/6 new tests GREEN (caller source-level test still red, by design); Task 3 commit closed the last red.

## Deferred Issues (out of scope)

While running the full `go test ./internal/app/cmd/` sweep, one pre-existing test failed:

- **`TestUnlockCmd_RequiresStateBucket`** — fails on this workstation because the test exercises `km unlock` with empty `StateBucket`, the code path tries to refresh AWS SSO credentials before checking the bucket guard, and SSO credentials on this machine are expired. The error message contains the SSO credential failure instead of "state bucket". Unrelated to this task — `unlock.go` was not touched. Should be fixed independently by either (a) re-ordering the StateBucket guard ahead of any AWS client construction in unlock.go, or (b) the operator running `aws sso login` to refresh credentials before running the suite. Logging here per the GSD scope-boundary rule.

## Deviations from Plan

None — plan executed exactly as written, three atomic commits, TDD red-green discipline preserved at each commit boundary.

## Operator Gate Status

**PENDING** — manual end-to-end verification is the operator's responsibility per the plan's `<success_criteria>` "Operator-gated" block. The executor explicitly did NOT run `make build`, `km create profiles/learn.yaml`, `km shell`, `git clone`, or `km destroy`. The operator (whereiskurt) should:

1. `make build` — per project memory `feedback_rebuild_km`, always Make (not bare `go build`) so the binary picks up version ldflags.
2. `km create profiles/learn.yaml` — expect `✓ GitHub token stored in SSM` (not `⊘ skipped`).
3. `km shell <sandbox-id>` then `git clone https://github.com/whereiskurt/defcon.run.34 /tmp/test` — expect success with no username prompt.
4. `km destroy --remote --yes <sandbox-id>` — per project memory `feedback_destroy_flags`.

Update the `operator_gate:` field in this summary's frontmatter to `PASSED` (or `FAILED` with notes) after running the gate.

## Self-Check: PASSED

- File `internal/app/cmd/create.go` — FOUND, contains `ErrAmbiguousInstallation`, `GetParametersByPath` on `SSMGetPutAPI`, enumeration branch, three-way caller switch with `⚠`.
- File `internal/app/cmd/create_github_test.go` — FOUND, contains all five new `TestResolveInstallationID_*` tests + `TestCreateGitHubCaller_DifferentiatesAmbiguity` + extended `mockSSMGetPut` with `pathResults`/`pathCallCount`/`GetParametersByPath`.
- Commit f8f5c27 — FOUND in `git log`.
- Commit 74a190c — FOUND in `git log`.
- Commit 6a7a7d4 — FOUND in `git log`.
- `pkg/github/token.go` — UNCHANGED (verified via `git diff --stat`).
- `profiles/learn.yaml` — UNCHANGED (verified via `git diff --stat`).
