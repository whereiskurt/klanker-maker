---
phase: 89
slug: sops-secret-injection-for-sandboxes
status: approved
nyquist_compliant: true
wave_0_complete: true
created: 2026-05-26
approved: 2026-05-26
---

# Phase 89 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution. Source: `89-RESEARCH.md` § Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (`go test`) + table-driven Go subtests; manual UAT via bash scripts |
| **Config file** | Per-package `*_test.go`; module-level via `go test ./...` |
| **Quick run command** | `go test ./pkg/profile/... ./internal/app/cmd/... ./pkg/compiler/...` |
| **Full suite command** | `make test` (runs `go test ./...`) |
| **Estimated runtime** | ~30s (quick) / ~3min (full `make test`) |

Wave-0 also installs/exercises:
- `sops` v3.13.1 (linux amd64) — fetched from GitHub releases by `km init --sidecars`
- `age` key for offline encrypted-fixture round-trip (avoids real KMS calls in unit tests)

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/profile/... ./pkg/compiler/... ./internal/app/cmd/...` (~30s)
- **After every plan wave:** Run `make test` (full Go suite)
- **Before `/gsd:verify-work`:** Full suite must be green AND UAT scenarios in `89-N-UAT.md` pass on live AWS
- **Max feedback latency:** 30 seconds (per-commit quick run)

---

## Per-Task Verification Map

(Plan/wave/task IDs are placeholders — `gsd-planner` will reassign concrete `89-NN-NN` IDs when PLAN.md files are generated. Mapping below preserves requirement→test type→command bindings.)

| Req ID | Behavior | Test Type | Automated Command | File Status |
|--------|----------|-----------|-------------------|-------------|
| SOPS-01-SCHEMA | `spec.secrets.sopsFile` parses, defaults to empty | unit | `go test ./pkg/profile/ -run TestSecretsSpecParse` | ❌ W0 — new `pkg/profile/secrets_test.go` |
| SOPS-02-VALIDATION | Reject missing `.enc.yaml` suffix; allow empty; absence of `sops:` block errors | unit | `go test ./pkg/profile/ -run TestValidateSemanticSecrets` | ❌ W0 — extends `validate_test.go` |
| SOPS-03-KMS-MODULE | Module compiles via `terraform validate` | smoke | `cd infra/modules/sandbox-secrets-key/v1.0.0 && terraform init -backend=false && terraform validate` | ❌ W0 — new module |
| SOPS-04-MODULE-WIRING | terragrunt.hcl resolves locals + plans clean | smoke | `cd infra/live/use1/sandbox-secrets-key && terragrunt plan` | ❌ W0 — new terragrunt.hcl |
| SOPS-05-BOOTSTRAP-FLAG | `km bootstrap --shared-secrets-key --dry-run` prints expected output | unit | `go test ./internal/app/cmd/ -run TestRunBootstrapSharedSecretsKeyDryRun` | ❌ W0 — extends `bootstrap_test.go` |
| SOPS-06-BOOTSTRAP-PLAN | `--shared-secrets-key --plan` evaluates destroy-class gate | unit | `go test ./internal/app/cmd/ -run TestRunBootstrapSharedSecretsKeyPlan` | ❌ W0 — mirrors `bootstrap_plan_test.go` |
| SOPS-07-BOOTSTRAP-ALL-CHAIN | `--all` runs both subflows in order; `--all` + `--shared-secrets-key` mutex errors | unit | `go test ./internal/app/cmd/ -run TestRunBootstrapAllChain_Phase89` | ❌ W0 — extends `bootstrap_test.go` |
| SOPS-08-IAM-OPERATOR | No-op — operator IAM already grants `kms:*` (km-operator-policy line 484) | manual | `grep 'kms:\*' infra/modules/km-operator-policy/v1.0.0/main.tf` | ✅ verified |
| SOPS-09-IAM-SANDBOX | ec2spot v1.2.0 emits the two IAM policies with correct `kms:ResourceAliases` condition | unit | `go test ./pkg/compiler/ -run TestEC2SpotSecretsIAMEmission` (or terraform plan-show snapshot) | ❌ W0 — new test |
| SOPS-10-SCHEMA-EXPORT | JSON Schema accepts valid `spec.secrets`, rejects bad types | unit | `go test ./pkg/profile/ -run TestSchemaSecretsSpec` | ❌ W0 |
| SOPS-11-COMPILER-UPLOAD | create.go uploads bundle to expected S3 key; respects empty `SopsFile` | unit + integration | `go test ./internal/app/cmd/ -run TestCreatePutSopsBundle` (mock S3) | ❌ W0 |
| SOPS-12-USERDATA-FETCH | userdata emits sops binary + bundle fetch block iff `SopsBundlePresent` | unit | `go test ./pkg/compiler/ -run TestUserdataSopsBlock` | ❌ W0 — extends `userdata_test.go` |
| SOPS-13-USERDATA-DECRYPT | Decrypt block uses `sops decrypt --output-type dotenv`, hard-fails on error | unit (template snapshot) | `go test ./pkg/compiler/ -run TestUserdataSopsDecryptShape` | ❌ W0 |
| SOPS-14-USERDATA-ENV-EXPOSURE | `/etc/profile.d/zz-sandbox-secrets.sh` uses `set -a` / `. file` / `set +a`; mode 0644 | unit (template snapshot) | `go test ./pkg/compiler/ -run TestUserdataSopsProfileD` | ❌ W0 |
| SOPS-15-BOOT-FAIL-ABORT | Decrypt failure path emits `exit 1` in user-data | unit (template grep) | `go test ./pkg/compiler/ -run TestUserdataSopsFailAbort` | ❌ W0 |
| SOPS-16-DESTROY-CLEANUP | destroy.go calls `DeleteObject` on the bundle S3 key | unit | `go test ./internal/app/cmd/ -run TestDestroyDeletesSopsBundle` (mock S3) | ❌ W0 |
| SOPS-17-S3-LIFECYCLE | s3-artifacts-lifecycle v1.1.0 emits both rules (existing + new 7-day) | smoke | `cd infra/modules/s3-artifacts-lifecycle/v1.1.0 && terraform validate` | ❌ W0 |
| SOPS-18-DOCTOR-CHECK | `checkSharedSecretsKey` returns OK / WARN(missing) / WARN(orphans) via mocked KMS | unit (table) | `go test ./internal/app/cmd/ -run TestCheckSharedSecretsKey` | ❌ W0 — new `doctor_secrets_test.go` |
| SOPS-19-CONFIGURE-GITIGNORE | First call appends; second is no-op; partial existing content respected | unit | `go test ./internal/app/cmd/ -run TestEnsureSecretsGitignore` | ❌ W0 |
| SOPS-20-SIDECARS-SOPS-DEPLOY | `buildAndUploadSidecars` includes sops download + upload | manual-only | `km init --sidecars` against test bucket; verify `aws s3 ls s3://${bucket}/binaries/sops` | ❌ W0 — UAT script |
| SOPS-21-UNINIT-CLEANUP | `km uninit` only deletes own-prefix alias + schedule-deletes own key | manual-only | UAT scenario in `89-N-UAT.md` (requires sibling install) | ❌ W2 |
| SOPS-22-DOCS | `docs/sandbox-secrets.md` exists; CLAUDE.md references it | smoke | `test -f docs/sandbox-secrets.md && grep -q 'sandbox-secrets' CLAUDE.md` | ❌ W1 |
| SOPS-23-UAT-ACCEPTANCE | Live: Codex sandbox accrues `BUDGET#ai#gpt-*` via sops-injected `OPENAI_API_KEY` | manual-only (UAT) | Documented script in `89-N-UAT.md`; mirrors Phase 88 plan 07 | ❌ W2 |

*Status legend: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky · W0/W1/W2 = wave the test lands in*

---

## Wave 0 Requirements

Test scaffolds and fixtures that must exist before Wave 1 implementation tasks land:

- [ ] `pkg/profile/secrets_test.go` — stubs for SOPS-01, SOPS-02
- [ ] `pkg/profile/schemas/sandbox_profile.schema.json` — add `spec.secrets` object schema (SOPS-10)
- [ ] `internal/app/cmd/bootstrap_secrets_test.go` — stubs for SOPS-05, SOPS-06, SOPS-07
- [ ] `internal/app/cmd/create_secrets_test.go` — stubs for SOPS-11
- [ ] `internal/app/cmd/destroy_secrets_test.go` — stubs for SOPS-16
- [ ] `internal/app/cmd/doctor_secrets_test.go` — stubs for SOPS-18
- [ ] `internal/app/cmd/configure_secrets_test.go` — stubs for SOPS-19
- [ ] `pkg/compiler/userdata_secrets_test.go` — stubs for SOPS-12, SOPS-13, SOPS-14, SOPS-15
- [ ] `pkg/compiler/testdata/secrets-fixture.enc.yaml` — sample SOPS-encrypted bundle for tests (**use age key, not KMS**, to keep tests offline-runnable)
- [ ] `infra/modules/sandbox-secrets-key/v1.0.0/{main,variables,outputs}.tf` — new module skeleton (SOPS-03)
- [ ] `infra/live/use1/sandbox-secrets-key/terragrunt.hcl` — live wiring (SOPS-04)
- [ ] `infra/modules/s3-artifacts-lifecycle/v1.1.0/main.tf` — additive lifecycle rule (SOPS-17)
- [ ] `infra/modules/ec2spot/v1.2.0/main.tf` — additive IAM policies (SOPS-09)

**Test fixture strategy:** Wave-0 tests MUST avoid real KMS calls. Use **age encryption** for SOPS fixtures (`sops --age <pubkey> -e bundle.yaml`); sops natively supports age and decrypts offline if `SOPS_AGE_KEY` env var is set. KMS-backed decryption is exercised only in Wave 2 UAT against a live AWS account.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| sops binary lands in S3 at `s3://${prefix}-artifacts-*/binaries/sops` | SOPS-20 | Requires real AWS account + write to artifacts bucket | `km init --sidecars` against test install; `aws s3 ls s3://${bucket}/binaries/sops` must show the object |
| `km uninit` preserves sibling-install KMS resources | SOPS-21 | Requires two real installs (different `resource_prefix`) in same account | Provision `km` + `km2` installs, run `km uninit` on first, verify `km2-sandbox-secrets` alias and key still exist via `aws kms list-aliases` |
| Codex sandbox accrues `BUDGET#ai#gpt-*` via sops-injected `OPENAI_API_KEY` | SOPS-23 | End-to-end live: KMS decrypt + sandbox boot + Phase 88 metering proxy + DynamoDB write | UAT script in `89-N-UAT.md`; mirrors Phase 88 plan 07 (curl through MITM proxy in lieu of Codex WebSocket-upgrade behavior) |
| `docs/sandbox-secrets.md` operator runbook | SOPS-22 | Prose review (operator-facing) | `test -f docs/sandbox-secrets.md && grep -q 'sandbox-secrets' CLAUDE.md` + manual readability pass |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify (audit during plan-checker pass)
- [ ] Wave 0 covers all `MISSING` references above
- [ ] No watch-mode flags (one-shot `go test` only)
- [ ] Feedback latency < 30s (quick command)
- [ ] `nyquist_compliant: true` set in frontmatter after plan-checker pass

**Approval:** approved 2026-05-26 (plan-checker iteration 2 PASS)
