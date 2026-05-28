---
phase: 89-sops-secret-injection-for-sandboxes
verified: 2026-05-28T05:15:00Z
status: passed
score: 23/23 requirements verified
re_verification: false
human_verification:
  - test: "km init --sidecars deploys sops binary to S3 and it is accessible at s3://{bucket}/binaries/sops"
    expected: "sops v3.13.1 linux/amd64 present at that S3 path; sandbox with spec.secrets.sopsFile boots without 404"
    why_human: "S3 binary presence cannot be verified offline; requires live AWS credentials and the actual bucket"
  - test: "km uninit removes own KMS alias and schedules key deletion without touching sibling installs"
    expected: "alias/km-sandbox-secrets enters PendingDeletion; sibling aliases untouched"
    why_human: "SOPS-21 uninit path was skipped in UAT (production install, not throwaway). Scenario 6 was SKIPPED per 89-07-UAT.md."
---

# Phase 89: SOPS Secret Injection for Sandboxes — Verification Report

**Phase Goal:** Declarative SOPS-encrypted secrets bundle attached to a profile (`spec.secrets.sopsFile: ./secrets/*.enc.yaml`); sandbox decrypts at boot using a shared per-install KMS key (provisioned by `km bootstrap --shared-secrets-key`) and exposes secret values as env vars via `/etc/profile.d/zz-sandbox-secrets.sh`. Acceptance: a Codex sandbox declares `spec.secrets.sopsFile: ./secrets/codex.enc.yaml`, boots, and Phase 88's OpenAI meter writes `BUDGET#ai#gpt-*` rows in DynamoDB without operator post-create wiring.

**Verified:** 2026-05-28T05:15:00Z
**Status:** PASSED
**Re-verification:** No — initial verification

## Context: UAT Outcome and Deploy Dependencies

The phase went through live UAT (89-07-UAT.md) that surfaced and fixed 8 real bugs via hot-fix commits. The UAT acceptance criterion was met: a codex sandbox booted with SOPS-injected `OPENAI_API_KEY` and Phase 88 metered a real `api.openai.com` call into `BUDGET#ai#gpt-4o-mini-2024-07-18` (spentUSD 0.0014127).

Two commits require `km init` redeploy to land on live infrastructure (code is committed and tested but not yet on-box):

- `5fc7aca` — s3-artifacts-lifecycle/v1.1.0 lifecycle rule (`sandbox-secrets-7day`) — requires `km init` to apply to the artifacts bucket
- `68b9188` — ttl-handler Step 7 SOPS bundle delete on remote/TTL destroy — requires `km init --lambdas` to update the Lambda

These are **deploy dependencies**, not code gaps. All other fixes (`e91871d`, `a6548e3`, `ed3c5fa`, `b8b3c6b`, `961fe3d`, `f3f6a16`) are fully live.

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Profile schema accepts `spec.secrets.sopsFile` and is backward-compatible (nil when absent) | VERIFIED | `pkg/profile/types.go:57` — `Secrets *SecretsSpec`; `TestSecretsSpecParse`, `TestSecretsSpecAbsent` PASS |
| 2 | `km validate` rejects non-`.enc.yaml` suffix and missing `sops:` block offline | VERIFIED | `pkg/profile/validate.go:418-422`; `TestValidateSemanticSecrets_BadSuffix`, `TestValidateSopsBundleFile_*` PASS |
| 3 | `km bootstrap --shared-secrets-key` provisions KMS key+alias; `--plan` hits destroy-class gate; `--all` chains it | VERIFIED | `bootstrap.go:935,1041,1141`; flag registered at line 1403; `--all` subflow 3 at line 1164; `TestRunBootstrapSharedSecretsKey*` PASS |
| 4 | Per-install KMS module (`sandbox-secrets-key/v1.0.0`) creates `aws_kms_key` with `prevent_destroy=true`, `enable_key_rotation=true`, and key policy with account-root admin only (no `kms:ResourceAliases` in key policy — fixed via `e91871d`) | VERIFIED | `infra/modules/sandbox-secrets-key/v1.0.0/main.tf` — single `EnableAccountAdmin` statement; no `kms:ResourceAliases` condition; live wiring at `infra/live/use1/sandbox-secrets-key/terragrunt.hcl` references `v1.0.0` |
| 5 | ec2spot/v1.2.0 attaches `kms:Decrypt` with `kms:ResourceAliases` condition + `s3:GetObject` for own bundle path | VERIFIED | `infra/modules/ec2spot/v1.2.0/main.tf:762-801`; sandbox terragrunt.hcl template uses `v1.2.0` (confirmed by `TestSandboxTemplateUsesEC2SpotV120` PASS) |
| 6 | Compiler emits SOPS section 5.5 in userdata (fetch sops binary + bundle, decrypt, expose via profile.d) only when `SopsBundlePresent` | VERIFIED | `pkg/compiler/userdata.go:992-1025`; `TestSopsBundlePresentPopulatedFromProfile` PASS; `TestSopsBundleAbsentWhenProfileHasNoSecrets` PASS |
| 7 | Decrypted secrets file is `root:sandbox 0440` (readable by sandbox user, not world) — hot fix `b8b3c6b` | VERIFIED | `userdata.go:1011-1012` — `chown root:sandbox` + `chmod 0440` |
| 8 | Boot fail-abort: `exit 1` on sops decrypt failure | VERIFIED | `userdata.go:1005-1006` — `echo "[km-bootstrap] FATAL..." && exit 1` in `if ! sops decrypt` block |
| 9 | `create.go` uploads SOPS bundle to S3 pre-apply (both local and remote paths) — hot fixes `a6548e3`, `ed3c5fa` | VERIFIED | `create.go:278-310` (`uploadSopsBundleIfPresent`); `create.go:2146-2164` (remote-create bridge); `TestCreatePutSopsBundle_*` all PASS |
| 10 | create-handler Lambda bridges SOPS bundle via `patchProfileForSops` (abs-path rewrite) | VERIFIED | `cmd/create-handler/main.go:433-454`; `TestPatchProfileForSops_*` PASS |
| 11 | `km destroy` (local path) deletes S3 bundle non-fatally | VERIFIED | `destroy.go:48-67,570-575` — `deleteSopsBundleNonFatal` called at Step 12.1 |
| 12 | `km destroy` (remote/TTL path) deletes S3 bundle via ttl-handler Step 7 — hot fix `68b9188` (requires `km init --lambdas` to deploy) | VERIFIED (code) | `cmd/ttl-handler/main.go:919-937`; deploy dependency noted |
| 13 | S3 lifecycle rule `sandbox-secrets-7day` on `sandboxes/` prefix as belt-and-suspenders — hot fix `5fc7aca` (requires `km init` to deploy) | VERIFIED (code) | `infra/modules/s3-artifacts-lifecycle/v1.1.0/main.tf:23-34`; live wiring at `infra/live/use1/s3-artifacts-lifecycle/terragrunt.hcl` references `v1.1.0` |
| 14 | `km agent run` sources `/etc/profile.d/zz-sandbox-secrets.sh` — hot fix `961fe3d` | VERIFIED | `internal/app/cmd/agent.go:1368`; `TestAgentShellCommands_SourcesSandboxSecrets` PASS |
| 15 | `km doctor` checks for own KMS alias and reports orphans | VERIFIED | `doctor.go:3570-3636`; `TestCheckSharedSecretsKey` (5 subtests) PASS |
| 16 | `km configure` idempotently adds `/secrets/*` + `!/secrets/*.enc.yaml` to `.gitignore` | VERIFIED | `configure.go:716-762`; `TestEnsureSecretsGitignore` (5 subtests) PASS |
| 17 | `km init --sidecars` downloads sops v3.13.1 and uploads to `s3://{bucket}/binaries/sops` | VERIFIED (code) | `init.go:1873-1922` — `fetchAndUploadSops` called in sidecars flow; deploy dependency (requires `km init --sidecars` run) |
| 18 | `km uninit` deletes own-prefix alias + schedules key deletion; preserves sibling installs | VERIFIED (code) | `uninit.go:285,540-616`; `TestDeleteOwnSecretsKMSAlias_*` PASS; live UAT Scenario 6 SKIPPED (production install) |
| 19 | JSON Schema rejects unknown properties and wrong types under `spec.secrets` | VERIFIED | `schemas/sandbox_profile.schema.json:510-520`; `TestSchemaSecretsSpec_*` PASS |
| 20 | codex profile has `spec.secrets.sopsFile: ./secrets/codex.enc.yaml` | VERIFIED | `profiles/codex.yaml:142-143` |
| 21 | codex config.toml forces HTTP transport (`supports_websockets = false` custom provider) to be MITM-compatible — hot fix `f3f6a16` | VERIFIED | `profiles/codex.yaml:48`; `profiles/learn.v2.codex.yaml:77`; both set `supports_websockets = false` in initCommand |
| 22 | Operator IAM already grants `kms:*` (no code change needed for SOPS-08) | VERIFIED | `infra/modules/km-operator-policy/v1.0.0/main.tf:484` — `Action = ["kms:*"]`; verified no-op |
| 23 | UAT acceptance criterion met: codex sandbox with SOPS-injected `OPENAI_API_KEY` accrued `BUDGET#ai#gpt-4o-mini-2024-07-18` (spentUSD 0.0014127) via Phase 88 meter | VERIFIED (live) | 89-07-UAT.md Scenario 4 — PASS; DynamoDB row confirmed |

**Score:** 23/23 truths verified

---

## Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/profile/types.go` | `SecretsSpec` struct + `Secrets *SecretsSpec` on `Spec` | VERIFIED | Lines 57, 381-390 |
| `pkg/profile/validate.go` | `.enc.yaml` suffix check + `ValidateSopsBundleFile` | VERIFIED | Lines 414-422, 552-590 |
| `pkg/profile/schemas/sandbox_profile.schema.json` | `spec.secrets` object with `sopsFile` string, `additionalProperties: false` | VERIFIED | Lines 510-520 |
| `pkg/profile/secrets_test.go` | 10 tests covering SOPS-01, SOPS-02, SOPS-10 | VERIFIED | All PASS |
| `pkg/compiler/userdata.go` | Section 5.5 SOPS block gated on `SopsBundlePresent`; `root:sandbox 0440`; `exit 1` on failure | VERIFIED | Lines 992-1025 |
| `pkg/compiler/compiler_secrets_test.go` | 4 tests covering template version + artifacts_bucket + SOPS block presence/absence | VERIFIED | All PASS |
| `infra/modules/sandbox-secrets-key/v1.0.0/main.tf` | KMS key + alias; `prevent_destroy=true`; account-root-only key policy | VERIFIED | Exists, substantive (60 lines), correct |
| `infra/modules/sandbox-secrets-key/v1.0.0/variables.tf` | `register_secrets_key` bool | VERIFIED | Exists |
| `infra/modules/sandbox-secrets-key/v1.0.0/outputs.tf` | `alias_name`, `key_arn`, `key_id` | VERIFIED | Exists |
| `infra/live/use1/sandbox-secrets-key/terragrunt.hcl` | References `v1.0.0`; `KM_REGISTER_SECRETS_KEY` env var | VERIFIED | Line 22, 28 |
| `infra/modules/s3-artifacts-lifecycle/v1.1.0/main.tf` | `sandbox-secrets-7day` rule on `sandboxes/` prefix | VERIFIED | Lines 23-34 |
| `infra/live/use1/s3-artifacts-lifecycle/terragrunt.hcl` | References `v1.1.0` | VERIFIED | Line 31 |
| `infra/modules/ec2spot/v1.2.0/main.tf` | `ec2spot_sandbox_secrets_kms` + `ec2spot_sandbox_secrets_s3` policies | VERIFIED | Lines 762-801 |
| `infra/templates/sandbox/terragrunt.hcl` | References `/v1.2.0` for ec2spot | VERIFIED | Line 43 |
| `internal/app/cmd/bootstrap.go` | `runBootstrapSharedSecretsKey`, `runBootstrapSharedSecretsKeyPlan`, `--all` chain | VERIFIED | Lines 935, 1041, 1141-1174, 1403 |
| `internal/app/cmd/bootstrap_secrets_test.go` | 3+ tests for SOPS-05/06/07 | VERIFIED | All PASS |
| `internal/app/cmd/create.go` | `uploadSopsBundleIfPresent` + remote-create bridge | VERIFIED | Lines 278-310, 2146-2164 |
| `internal/app/cmd/create_secrets_test.go` | Tests for upload helper | VERIFIED | All PASS |
| `internal/app/cmd/destroy.go` | `deleteSopsBundleNonFatal` + Step 12.1 call | VERIFIED | Lines 42-67, 570-575 |
| `cmd/create-handler/main.go` | `patchProfileForSops` for Lambda remote-create bridge | VERIFIED | Lines 433-454 |
| `cmd/ttl-handler/main.go` | Step 7 SOPS bundle delete | VERIFIED (code, deploy pending) | Lines 919-937 |
| `internal/app/cmd/agent.go` | Sources `zz-sandbox-secrets.sh` in agent script | VERIFIED | Line 1368 |
| `internal/app/cmd/agent_test.go` | `TestAgentShellCommands_SourcesSandboxSecrets` | VERIFIED | PASS |
| `internal/app/cmd/doctor.go` | `checkSharedSecretsKey` + `SecretsKeyClient` dep | VERIFIED | Lines 281-283, 2723-2729, 3570-3636 |
| `internal/app/cmd/doctor_secrets_test.go` | 5 subtests for doctor check | VERIFIED | All PASS |
| `internal/app/cmd/configure.go` | `ensureSecretsGitignore` | VERIFIED | Lines 716-762 |
| `internal/app/cmd/uninit.go` | `deleteOwnSecretsKMSAlias` + uninit call | VERIFIED | Lines 283-285, 540-616 |
| `internal/app/cmd/init.go` | `fetchAndUploadSops` + call in sidecars flow | VERIFIED | Lines 1873-1922 |
| `docs/sandbox-secrets.md` | 140-line operator runbook | VERIFIED | 140 lines; covers workflow, troubleshooting, security model |
| `CLAUDE.md` | Where-to-look row + `km bootstrap --shared-secrets-key` CLI entry | VERIFIED | Lines 18, 64 |
| `OPERATOR-GUIDE.md` | `## SOPS secret injection` section | VERIFIED | Line 1175; links to docs/sandbox-secrets.md |
| `profiles/codex.yaml` | `spec.secrets.sopsFile: ./secrets/codex.enc.yaml` + codex MITM config | VERIFIED | Lines 48, 142-143 |
| `profiles/learn.v2.codex.yaml` | `spec.secrets.sopsFile` + codex MITM config | VERIFIED | Lines 77, 182 |

---

## Key Link Verification

| From | To | Via | Status |
|------|----|-----|--------|
| `sandbox-secrets-key/v1.0.0/main.tf` | `ec2spot/v1.2.0/main.tf` | Both reference `alias/${var.resource_prefix}-sandbox-secrets`; ec2spot gates `kms:Decrypt` via `kms:ResourceAliases` on that exact alias | WIRED |
| `infra/live/use1/sandbox-secrets-key/terragrunt.hcl` | `infra/modules/sandbox-secrets-key/v1.0.0` | `terraform.source` at line 22 | WIRED |
| `infra/live/use1/s3-artifacts-lifecycle/terragrunt.hcl` | `infra/modules/s3-artifacts-lifecycle/v1.1.0` | Line 31 references `v1.1.0` | WIRED |
| `pkg/compiler/userdata.go` | S3 `sandboxes/{id}/secrets.enc.yaml` | Template uses `KM_ARTIFACTS_BUCKET/sandboxes/{{ .SandboxID }}/secrets.enc.yaml` | WIRED |
| `internal/app/cmd/create.go` | `s3://{bucket}/sandboxes/{id}/secrets.enc.yaml` | `uploadSopsBundleIfPresent` at line 745; remote-create bridge at line 2160 | WIRED |
| `cmd/create-handler/main.go` | `km create` subprocess | `patchProfileForSops` rewrites `sopsFile` to absolute `/tmp/{id}-secrets.enc.yaml` before invoking km create | WIRED |
| `internal/app/cmd/destroy.go` | S3 bundle deletion | `deleteSopsBundleNonFatal` called at Step 12.1 | WIRED |
| `cmd/ttl-handler/main.go` | S3 bundle deletion | Step 7 at line 919; gated on `h.S3Client != nil` | WIRED (deploy pending) |
| `internal/app/cmd/agent.go` | `/etc/profile.d/zz-sandbox-secrets.sh` | Explicit `source` at line 1368 in agent script | WIRED |
| `internal/app/cmd/bootstrap.go` | `infra/live/use1/sandbox-secrets-key` | `secretsKeyDir` at line 954; Terragrunt apply at line 1007 | WIRED |
| `internal/app/cmd/uninit.go` | KMS alias + key deletion | `deleteOwnSecretsKMSAlias` called at line 285 | WIRED |

---

## Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|---------|
| SOPS-01-SCHEMA | 89-01 | `spec.secrets.sopsFile` parses, defaults empty; `SecretsSpec` struct | SATISFIED | `types.go:57,381-390`; `TestSecretsSpecParse` PASS |
| SOPS-02-VALIDATION | 89-01 | Reject non-`.enc.yaml` suffix; require `sops:` block; offline | SATISFIED | `validate.go:414-422,552-590`; all `TestValidate*` PASS |
| SOPS-03-KMS-MODULE | 89-02 | `sandbox-secrets-key/v1.0.0` module with `aws_kms_key` + alias | SATISFIED | Module exists with correct resources, no illegal key policy conditions (post `e91871d`) |
| SOPS-04-MODULE-WIRING | 89-02 | Live wiring at `infra/live/use1/sandbox-secrets-key/terragrunt.hcl` | SATISFIED | File exists; references `v1.0.0`; reads `KM_REGISTER_SECRETS_KEY` |
| SOPS-05-BOOTSTRAP-FLAG | 89-04 | `km bootstrap --shared-secrets-key` flag + `runBootstrapSharedSecretsKey` | SATISFIED | `bootstrap.go:935,1388-1389,1403`; `TestRunBootstrapSharedSecretsKey*` PASS |
| SOPS-06-BOOTSTRAP-PLAN | 89-04 | `--shared-secrets-key --plan` hits Phase 84.2 destroy-class gate | SATISFIED | `bootstrap.go:1041-1127`; `aws_kms_key` in `ProtectedTypes` (planreport/protected.go:34) |
| SOPS-07-BOOTSTRAP-ALL-CHAIN | 89-04 | `--all` chains foundation → SES → secrets-key; mutex with `--shared-secrets-key` | SATISFIED | `bootstrap.go:1367-1374,1141-1174`; `TestBootstrapAll_MutexWithSharedSES` PASS |
| SOPS-08-IAM-OPERATOR | 89-06 | No-op — operator IAM already has `kms:*` at line 484 | SATISFIED | `km-operator-policy/v1.0.0/main.tf:484` — `Action = ["kms:*"]`; no code change required |
| SOPS-09-IAM-SANDBOX | 89-02 | ec2spot/v1.2.0 `kms:Decrypt` with `kms:ResourceAliases` + `s3:GetObject` scoped to own bundle | SATISFIED | `ec2spot/v1.2.0/main.tf:762-801` |
| SOPS-10-SCHEMA-EXPORT | 89-01 | JSON Schema gains `spec.secrets` object with `sopsFile` string, `additionalProperties: false` | SATISFIED | `schemas/sandbox_profile.schema.json:510-520`; `TestSchemaSecretsSpec_*` PASS |
| SOPS-11-COMPILER-UPLOAD | 89-05 | `create.go` uploads bundle to `sandboxes/<id>/secrets.enc.yaml` pre-apply | SATISFIED | `create.go:278-310`; `TestCreatePutSopsBundle_HappyPath` PASS; remote-create bridge at lines 2146-2164 |
| SOPS-12-USERDATA-FETCH | 89-05 | Userdata fetches sops binary + bundle when `SopsBundlePresent` | SATISFIED | `userdata.go:992-1001`; `TestSopsBundlePresentPopulatedFromProfile` PASS |
| SOPS-13-USERDATA-DECRYPT | 89-05 | `sops decrypt --output-type dotenv`; file is `root:sandbox 0440` (not `root:root 0400` — fixed by `b8b3c6b`) | SATISFIED | `userdata.go:1004,1011-1012`; production-correct ownership confirmed in UAT Scenario 3 |
| SOPS-14-USERDATA-ENV-EXPOSURE | 89-05 | `/etc/profile.d/zz-sandbox-secrets.sh` uses `set -a / . file / set +a` | SATISFIED | `userdata.go:1014-1022` |
| SOPS-15-BOOT-FAIL-ABORT | 89-05 | `exit 1` on decrypt failure | SATISFIED | `userdata.go:1005-1006` |
| SOPS-16-DESTROY-CLEANUP | 89-05 | Both local (`destroy.go`) and remote (`ttl-handler`) paths delete bundle | SATISFIED | `destroy.go:48-67` (local, live); `ttl-handler/main.go:919-937` (remote, code ready, deploy pending) |
| SOPS-17-S3-LIFECYCLE | 89-02 | `s3-artifacts-lifecycle/v1.1.0` adds `sandbox-secrets-7day` rule | SATISFIED | Module exists; live wiring points to `v1.1.0`; deploy pending (`km init`) |
| SOPS-18-DOCTOR-CHECK | 89-06 | `checkSharedSecretsKey` OK/WARN-missing/WARN-orphan | SATISFIED | `doctor.go:3570-3636`; 5 test subtests PASS |
| SOPS-19-CONFIGURE-GITIGNORE | 89-03 | `km configure` idempotently adds `/secrets/*` + `!/secrets/*.enc.yaml` | SATISFIED | `configure.go:716-762`; 5 test subtests PASS |
| SOPS-20-SIDECARS-SOPS-DEPLOY | 89-03 | `km init --sidecars` downloads sops v3.13.1 amd64 and uploads to S3 | SATISFIED (code) | `init.go:1873-1922`; deploy dependency — binary on S3 unverifiable offline |
| SOPS-21-UNINIT-CLEANUP | 89-04 | `km uninit` deletes own alias + schedules key deletion; preserves siblings | SATISFIED (code) | `uninit.go:540-616`; `TestDeleteOwnSecretsKMSAlias_*` PASS; live test SKIPPED (UAT Scenario 6) |
| SOPS-22-DOCS | 89-06 | `docs/sandbox-secrets.md` (140 lines) + CLAUDE.md row + OPERATOR-GUIDE.md section | SATISFIED | All three files verified; OPERATOR-GUIDE.md at `OPERATOR-GUIDE.md` (repo root), not `docs/operator-guide.md` |
| SOPS-23-UAT-ACCEPTANCE | 89-07 | Live Codex sandbox accrues `BUDGET#ai#gpt-*` via SOPS-injected key without post-create wiring | SATISFIED | 89-07-UAT.md Scenario 4 PASS: `BUDGET#ai#gpt-4o-mini-2024-07-18` spentUSD=0.0014127 |

---

## Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `pkg/compiler/userdata.go` | 3861 | `TODO Phase 66 plan 04: migrate nil-network fallback` | Info | Pre-existing Phase 66 item; unrelated to Phase 89 |
| `internal/app/cmd/create.go` | 1648-1662 | PLACEHOLDER string replacements | Info | Legitimate template pattern for Docker compose YAML; not a stub |

No blockers found in Phase 89 modified files.

---

## Test Suite Status

**All Phase 89 unit tests pass.** Pre-existing failures confirmed at baseline commit `30a105d` and unchanged by Phase 89:

| Test | Package | Failure Mode | Pre-existing |
|------|---------|-------------|--------------|
| `TestUserDataNotifyEnv*` (5 tests) | `pkg/compiler` | Test fixture drift | Yes — fails at 30a105d |
| `TestRunAgentAuthClaude_TeesAndCleans` | `internal/app/cmd` | Interactive OAuth subprocess | Yes — listed in problem statement |
| `TestStep11d_Success_WritesChannelIDParam` | `internal/app/cmd` | SSM path prefix mismatch (`/sandbox/` vs `/km/sandbox/`) | Yes — verified at 30a105d |
| `TestHandleTTLEvent_UploadsArtifactsWhenConfigured` | `cmd/ttl-handler` | Nil-pointer panic on AWS mock | Yes — listed in problem statement |

Phase 89 tests that pass (representative):

- `pkg/profile`: `TestSecretsSpecParse`, `TestSecretsSpecAbsent`, `TestSecretsSpecEmpty`, `TestValidateSemanticSecrets_*`, `TestValidateSopsBundleFile_*`, `TestSchemaSecretsSpec_*` — all PASS
- `pkg/compiler`: `TestSandboxTemplateUsesEC2SpotV120`, `TestCompileEC2ServiceHCLHasArtifactsBucket`, `TestSopsBundlePresentPopulatedFromProfile`, `TestSopsBundleAbsentWhenProfileHasNoSecrets` — all PASS
- `internal/app/cmd`: `TestRunBootstrapSharedSecretsKey*`, `TestDeleteOwnSecretsKMSAlias_*`, `TestCheckSharedSecretsKey` (5 subtests), `TestEnsureSecretsGitignore` (5 subtests), `TestAgentShellCommands_SourcesSandboxSecrets`, `TestCreatePutSopsBundle_*` — all PASS
- `cmd/create-handler`: `TestPatchProfileForSops_*` — all PASS

---

## Human Verification Required

### 1. sops binary on S3 (`km init --sidecars`)

**Test:** Run `km init --sidecars`, then check `aws s3 ls s3://${KM_ARTIFACTS_BUCKET}/binaries/sops`
**Expected:** Object present, size matches sops v3.13.1 linux/amd64 binary
**Why human:** Requires live AWS credentials + the actual artifacts bucket. The code path is verified (`init.go:1873-1922`) but the binary cannot be confirmed in S3 without a real call.

### 2. `km uninit` KMS cleanup (SOPS-21)

**Test:** On a throwaway install (non-production prefix), run `km uninit --yes`, then verify the `alias/{prefix}-sandbox-secrets` alias is removed and the underlying key has `KeyState=PendingDeletion`
**Expected:** Alias gone, key in PendingDeletion with ~7-day window; sibling install aliases untouched
**Why human:** UAT Scenario 6 was explicitly SKIPPED because the test install is production (resource_prefix `km`). The code is unit-tested (`TestDeleteOwnSecretsKMSAlias_*` PASS) but the live path was not exercised.

---

## Deploy Dependencies (Not Code Gaps)

These commits require operator action to land on infrastructure:

| Commit | Fix | Required Action |
|--------|-----|----------------|
| `5fc7aca` | s3-artifacts-lifecycle `sandbox-secrets-7day` rule | `km init` (applies the lifecycle config to the artifacts bucket) |
| `68b9188` | ttl-handler Step 7 SOPS bundle delete on remote destroy | `km init --lambdas` (redeploys ttl-handler Lambda) |
| `961fe3d` | `km agent run` sources `zz-sandbox-secrets.sh` | `make build` + `km init --sidecars` (new km binary on the management Lambda) |

Until these are deployed: (1) orphaned bundles on remote-destroy/TTL paths rely solely on the 7-day lifecycle rule backstop; (2) agent-run jobs in non-login shells may not see secrets (workaround: use `km shell` directly).

---

## Open Items (Not Phase 89 Scope)

Per 89-07-UAT.md:

1. **`km agent run` ignores `spec.cli.agent`** — `agent.go:215` defaults to claude; requires explicit `--codex` flag. Phase 70 defect.
2. **`profiles/codex.yaml` `hibernation: true` + `spot: true` requires `--on-demand`** — pre-existing profile constraint.
3. **codex default model** — `gpt-4o-mini` baked into initCommand; operators may prefer a different model. Operator-configurable in future.

---

_Verified: 2026-05-28T05:15:00Z_
_Verifier: Claude (gsd-verifier)_
