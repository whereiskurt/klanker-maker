---
phase: 89
plan: 01
subsystem: profile-schema
tags: [sops, secrets, schema, validation, tdd]
dependency_graph:
  requires: []
  provides:
    - SecretsSpec struct (pkg/profile/types.go)
    - Spec.Secrets *SecretsSpec pointer
    - ValidateSopsBundleFile exported helper
    - spec.secrets JSON Schema property
    - age-encrypted test fixture
  affects:
    - pkg/profile/compiler (downstream 89-02+: reads Spec.Secrets.SopsFile)
    - pkg/profile/validate_test.go (no change; existing suite passes)
tech_stack:
  added:
    - age v1.3.1 (fixture generation only; not a Go dependency)
    - sops v3.11.0 (fixture generation only; not a Go dependency)
  patterns:
    - TDD (RED → GREEN per-task commits)
    - pointer-optional struct fields (nil = absent = backwards compatible)
    - offline-only validation (no AWS/KMS calls from ValidateSemantic or ValidateSopsBundleFile)
key_files:
  created:
    - pkg/profile/secrets_test.go
    - pkg/profile/testdata/secrets-fixture.enc.yaml
    - pkg/profile/testdata/secrets-fixture-age.key
  modified:
    - pkg/profile/types.go
    - pkg/profile/validate.go
    - pkg/profile/schemas/sandbox_profile.schema.json
decisions:
  - "SecretsSpec placed as pointer (*SecretsSpec) on Spec so nil = absent (backwards compatible with all pre-89 profiles)"
  - "ValidateSemantic checks only .enc.yaml suffix; file existence + sops: block check layered on via ValidateSopsBundleFile by callers (km validate / km create)"
  - "Fixture generated via real sops v3.11.0 + age v1.3.1 (not synthetic fallback); downstream 89-05 decrypt round-trip will use the same key file"
  - "Encrypted with SOPS_AGE_RECIPIENTS from /tmp to avoid .sops.yaml config conflict in project root"
metrics:
  duration: 197s
  completed: "2026-05-27"
  tasks: 2
  files: 6
---

# Phase 89 Plan 01: Profile Schema for SOPS Secret Injection Summary

**One-liner:** SecretsSpec struct + .enc.yaml suffix validator + offline sops: block helper + JSON Schema property for SOPS-encrypted environment variable injection.

## What Was Built

Wave 0 schema surface pinning the contract all downstream Phase 89 plans (compiler, create, destroy, doctor) depend on.

**Production files modified (additive only — no behavioral change for profiles without spec.secrets):**

- `pkg/profile/types.go` — `SecretsSpec` struct + `Secrets *SecretsSpec` pointer on `Spec`
- `pkg/profile/validate.go` — spec.secrets.sopsFile `.enc.yaml` suffix check in `ValidateSemantic`; exported `ValidateSopsBundleFile(bundlePath string) error` offline helper
- `pkg/profile/schemas/sandbox_profile.schema.json` — `secrets` object with `sopsFile` string property and `additionalProperties: false`

**New test/fixture files:**

- `pkg/profile/secrets_test.go` — 11 tests across 4 groups (SecretsSpec parse, semantic validation, bundle file helper, JSON Schema accept/reject)
- `pkg/profile/testdata/secrets-fixture.enc.yaml` — age-encrypted bundle (OPENAI_API_KEY, ANTHROPIC_API_KEY, SOME_MULTI_TOKEN_VALUE) generated with real sops v3.11.0 + age v1.3.1
- `pkg/profile/testdata/secrets-fixture-age.key` — age test key (TEST KEY ONLY — not for real secrets)

## Exported Symbols Added

| Symbol | File | Description |
|--------|------|-------------|
| `SecretsSpec` | `types.go` | Struct with `SopsFile string` field |
| `Spec.Secrets` | `types.go` | `*SecretsSpec` pointer — nil when absent |
| `ValidateSopsBundleFile` | `validate.go` | Offline check: file exists, parses as YAML, has top-level `sops:` block |

## Fixture Generation Method

**Real sops + age** (not synthetic fallback).

- Tool versions: age v1.3.1, sops v3.11.0
- Public key: `age1mrymka5ape32skllwr8w0gnmy5ckm65gwf5gg4gzeawjv7mmcqpsn0snxg`
- Encrypted from `/tmp` to avoid `.sops.yaml` config conflict in project root
- Plaintext deleted after encryption (`/tmp/secrets-fixture-plain.yaml` removed)
- Downstream plan 89-05 can use the age key for a real decrypt round-trip offline (no KMS required)

## Test Results

All 11 Phase-89 tests GREEN; full `pkg/profile` suite passes; `go vet` clean; JSON Schema parses.

```
=== RUN   TestSecretsSpecParse          --- PASS
=== RUN   TestSecretsSpecAbsent         --- PASS
=== RUN   TestSecretsSpecEmpty          --- PASS
=== RUN   TestValidateSemanticSecrets_BadSuffix  --- PASS
=== RUN   TestValidateSemanticSecrets_GoodSuffix --- PASS
=== RUN   TestValidateSopsBundleFile_MissingFile         --- PASS
=== RUN   TestValidateSopsBundleFile_MissingSopsBlock     --- PASS
=== RUN   TestValidateSopsBundleFile_ValidFixture         --- PASS
=== RUN   TestSchemaSecretsSpec_ValidObject               --- PASS
=== RUN   TestSchemaSecretsSpec_RejectArrayType           --- PASS
=== RUN   TestSchemaSecretsSpec_RejectUnknownProperty     --- PASS
ok  github.com/whereiskurt/klanker-maker/pkg/profile
```

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| Task 1 (RED) | `15095c4` | test(89-01): add RED test scaffold + age-encrypted fixture |
| Task 2 (GREEN) | `06172cb` | feat(89-01): SecretsSpec + ValidateSemantic + ValidateSopsBundleFile + JSON Schema |

## Deviations from Plan

None — plan executed exactly as written.

The `.sops.yaml` config conflict when running sops from the project root was handled by running sops from `/tmp` (a minor environmental detail, not a plan deviation).

## Self-Check: PASSED

- `pkg/profile/secrets_test.go` — FOUND
- `pkg/profile/testdata/secrets-fixture.enc.yaml` — FOUND
- `pkg/profile/testdata/secrets-fixture-age.key` — FOUND
- `pkg/profile/types.go` (SecretsSpec) — FOUND
- `pkg/profile/validate.go` (ValidateSopsBundleFile) — FOUND
- `pkg/profile/schemas/sandbox_profile.schema.json` (secrets property) — FOUND
- Commit `15095c4` — FOUND
- Commit `06172cb` — FOUND
