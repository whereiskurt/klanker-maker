---
phase: 89-sops-secret-injection-for-sandboxes
plan: 06
subsystem: infra
tags: [sops, kms, doctor, secrets, documentation]

# Dependency graph
requires:
  - phase: 89-02
    provides: sandbox-secrets-key KMS module (alias/${prefix}-sandbox-secrets)
  - phase: 89-04
    provides: km bootstrap --shared-secrets-key + KMSAliasLister interface in bootstrap.go

provides:
  - checkSharedSecretsKey doctor check registered in km doctor default checkset
  - Five-test coverage for the check (OK, MissingOwn, OrphansPresent, OrphansWithoutOwn, NilClientIsSkipped)
  - docs/sandbox-secrets.md operator runbook (140 lines)
  - CLAUDE.md Where-to-look row + km bootstrap --shared-secrets-key CLI entry
  - OPERATOR-GUIDE.md ## SOPS secret injection section

affects: [89-sops-secret-injection-for-sandboxes]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "orphan-WARN doctor check pattern: nil-guard → ListAliases paginated scan → missing-own-first precedence → orphan-list"
    - "KMSAliasLister interface reused from bootstrap.go (same package, no redeclaration)"
    - "SecretsKeyClient in DoctorDeps constructed only when awsCfg available (WARNING 5 compliance)"

key-files:
  created:
    - internal/app/cmd/doctor_secrets_test.go
    - docs/sandbox-secrets.md
  modified:
    - internal/app/cmd/doctor.go
    - CLAUDE.md
    - OPERATOR-GUIDE.md

key-decisions:
  - "KMSAliasLister interface not redeclared in doctor.go — reused from bootstrap.go (same package); confirmed by build failure on attempt"
  - "SecretsKeyClient nil → CheckSkipped (not CheckError): doctor must degrade gracefully without AWS creds"
  - "Missing-own takes precedence over orphan list in checkSharedSecretsKey (more actionable warning)"
  - "SOPS-08-IAM-OPERATOR verified no-op: tightened grep returned line 484 ('kms:*' exact broad grant); no operator IAM change required"

patterns-established:
  - "Pattern: doctor check nil-client guard via DoctorDeps field (SecretsKeyClient); field nil when awsCfg unavailable — mirrors SESRulesClient pattern"

requirements-completed: [SOPS-08-IAM-OPERATOR, SOPS-18-DOCTOR-CHECK, SOPS-22-DOCS]

# Metrics
duration: 5min
completed: 2026-05-27
---

# Phase 89 Plan 06: SOPS Doctor Check + Operator Documentation Summary

**checkSharedSecretsKey KMS alias doctor check with orphan-WARN shape, 5-test TDD coverage, and full operator runbook (docs/sandbox-secrets.md + CLAUDE.md row + OPERATOR-GUIDE.md section)**

## Performance

- **Duration:** ~5 min
- **Started:** 2026-05-27T20:59:47Z
- **Completed:** 2026-05-27T21:04:45Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments

- `checkSharedSecretsKey` added to `doctor.go`, registered in default checkset after `checkSESRules`; nil-client guard per WARNING 5
- Five tests GREEN: OK, MissingOwn, OrphansPresent, OrphansWithoutOwn, NilClientIsSkipped
- `docs/sandbox-secrets.md` created (140 lines): feature overview, when-to-use SOPS vs SSM, prerequisites, workflow with encryption example, troubleshooting section, security model (v1/v2), operator cheat-sheet
- CLAUDE.md Where-to-look row added adjacent to "Send / receive email" row; `km bootstrap --shared-secrets-key` added to CLI list
- OPERATOR-GUIDE.md `## SOPS secret injection` section added before section 9 (additionalSnapshots)

## Task Commits

1. **Task 1 RED: failing tests for checkSharedSecretsKey** - `58c41a2` (test)
2. **Task 1 GREEN: checkSharedSecretsKey implementation** - `27acec9` (feat)
3. **Task 2: docs + CLAUDE.md + OPERATOR-GUIDE.md** - `1c5e12d` (feat)

## Files Created/Modified

- `internal/app/cmd/doctor.go` — Added `SecretsKeyClient KMSAliasLister` field to `DoctorDeps`; added `checkSharedSecretsKey` function; registered in default checkset; constructed client in `initRealDepsWithExisting`
- `internal/app/cmd/doctor_secrets_test.go` — Five subtests using `doctorFakeKMSAliasLister` test double (named to avoid conflict with `fakeKMSAliasLister` in `bootstrap_secrets_test.go`)
- `docs/sandbox-secrets.md` — New operator runbook (140 lines)
- `CLAUDE.md` — Where-to-look row + CLI bootstrap command
- `OPERATOR-GUIDE.md` — New `## SOPS secret injection` section

## SOPS-08-IAM-OPERATOR Verify (INFO 10 tightened regex)

Tightened grep command:
```
grep -nE '"kms:\*"|Action.*=.*"kms:\*"' infra/modules/km-operator-policy/v1.0.0/main.tf
```

Result:
```
484:        Action   = ["kms:*"]
```

- Matched line: **484** (matches REQUIREMENTS.md SOPS-08 anchor at line 484 — no drift)
- The regex anchors on quoted `"kms:*"` or assignment form `Action.*=.*"kms:*"`
- Does NOT false-positive on `kms:Decrypt*`, `kms:Re*`, or other star-suffix patterns
- **Conclusion: SOPS-08-IAM-OPERATOR is a no-op — the broad operator KMS grant is already in place. No code change shipped.**

## WARNING 5 Closure

The KMS client for `checkSharedSecretsKey` is constructed ONLY when `awsCfg` is non-nil:

```go
// In initRealDepsWithExisting (returns early when LoadAWSConfig fails):
deps.SecretsKeyClient = kms.NewFromConfig(awsCfg)
```

When `awsCfg` is unavailable, `initRealDepsWithExisting` returns early (before any client construction), leaving `SecretsKeyClient` nil. The nil-client guard in `checkSharedSecretsKey` then returns `CheckSkipped`. This behavior is locked by `TestCheckSharedSecretsKey/NilClientIsSkipped`.

The `TestDoctor*` regression suite passes unchanged (cached run, no failures).

## Decisions Made

- `KMSAliasLister` NOT redeclared in `doctor.go`: confirmed it already exists in `bootstrap.go` (same package) — build failed immediately when duplicate was added; removed duplicate, reused `bootstrap.go` declaration
- `doctorFakeKMSAliasLister` named distinctly from `fakeKMSAliasLister` to avoid package-level test type collision with `bootstrap_secrets_test.go`
- Missing-own takes precedence in `checkSharedSecretsKey` (return WARN-missing before checking orphan list) — matches "most actionable" principle from plan

## Deviations from Plan

None — plan executed exactly as written. The only adaptation was naming the test type `doctorFakeKMSAliasLister` to avoid collision with the existing `fakeKMSAliasLister` in `bootstrap_secrets_test.go` (both files are in `package cmd`), and NOT redeclaring `KMSAliasLister` in `doctor.go` since it already exists in `bootstrap.go` (same package). These are implementation-detail choices within the plan's guidance.

## Operator Follow-up

```bash
make build   # so km doctor picks up checkSharedSecretsKey
km bootstrap --shared-secrets-key   # (if not yet done)
km doctor    # expect: ✓ Shared secrets KMS key healthy
```

---
*Phase: 89-sops-secret-injection-for-sandboxes*
*Completed: 2026-05-27*
