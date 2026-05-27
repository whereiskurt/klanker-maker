# Phase 89: SOPS Secret Injection for Sandboxes - Context

**Gathered:** 2026-05-26
**Status:** Ready for planning

<domain>
## Phase Boundary

Declarative SOPS-encrypted secrets bundle attached to a profile; the sandbox decrypts at boot using a shared KMS key and exposes secret values as environment variables.

Motivated by Phase 88 — Codex sandboxes today have no automated `OPENAI_API_KEY` injection path (Phase 88 UAT used out-of-band SSM SecureString + temp file + SSM send-command). Closing this gap makes Phase 88 OpenAI metering "just work" for Codex sandboxes without operator post-create wiring.

**Pre-locked architecture (decided 2026-05-26):**
- Option B over Option A — sandbox decrypts at boot, not operator decrypts + injects resolved env
- Compiled-in profile path — not git-clone-on-boot, not SSM-as-blob
- One shared KMS key per install (v1) — per-profile/per-sandbox isolation deferred to v2
- KMS key provisioned by `km bootstrap`, idempotent like shared SES rule set
- Bundle stored at `s3://${prefix}-artifacts-*/sandboxes/<id>/secrets.enc.yaml`
- Decrypted to `/etc/sandbox-secrets.env` (root:root 0400)

**In scope:** Schema, bootstrap module, compiler bundle upload, user-data decrypt + env exposure, doctor check, lifecycle cleanup.

**Out of scope (v2):** Per-profile/per-sandbox KMS isolation, secret rotation without recreate, `km secrets edit` wrapper, non-env secret types (file-on-disk, certs).

</domain>

<decisions>
## Implementation Decisions

### Schema shape
- New top-level section: `spec.secrets.sopsFile: ./path/to/file.enc.yaml`
- Sibling to (NOT replacement of) existing `spec.execution.secrets: []string` (SSM-path field stays as-is — clean separation between SOPS and legacy SSM injection)
- **Single file** per profile — not a list, no overlay fragments
- Path resolution: relative to the profile YAML location (matches how `extends:` works today — keeps profiles portable)
- Bundle contents: flat key:value YAML. Every top-level key materializes as one env var in `/etc/sandbox-secrets.env`. Reserved keys `sops` and `_meta` ignored (SOPS itself embeds a `sops:` metadata block in encrypted output)

### Repo layout for encrypted files
- Convention: `secrets/` directory at operator repo root
- File suffix: `*.enc.yaml` **mandatory** (anchors the gitignore rule)
- Filename within `secrets/`: operator-chosen, free-form (e.g., `secrets/codex.enc.yaml`, `secrets/shared.enc.yaml`)
- `.gitignore` policy: `/secrets/*` + `!/secrets/*.enc.yaml` — encrypted files version with profiles, plaintext workdir files never leak
- `km configure` writes the two `.gitignore` lines on first run (idempotent — only if not already present)
- Profile-to-bundle topology: km does NOT enforce 1:1 or 1:N — operators decide based on blast radius (e.g., 10 profiles may share `secrets/common.enc.yaml`)

### Bootstrap UX for the shared KMS key
- New top-level bootstrap flag: `km bootstrap --shared-secrets-key` (mirrors `--shared-ses`)
- Module: `infra/modules/sandbox-secrets-key/v1.0.0/` with `lifecycle.prevent_destroy = true` and module-version-bump discipline (sandbox-secrets-key v1.0.0 → v1.1.0 for additive changes, v2.0.0 for resource-name churn)
- `km bootstrap --all` **chains it**: foundation → shared-ses → shared-secrets-key. `--all` is mutex with `--shared-secrets-key` (same pattern as today's `--all` ↔ `--shared-ses` mutex)
- `--plan` honors the Phase 84.2 destroy-class gate; KMS keys join the protected types list (already there per Phase 84.2 protected.go)
- KMS naming: alias `alias/${resource_prefix}-sandbox-secrets` (account+region scoped — km is single-region-per-install)
- Sandbox instance-profile IAM: `kms:Decrypt` with `Condition: kms:ResourceAliases` for cross-prefix isolation
- `km uninit` removes only this install's KMS key and alias (shared by `resource_prefix` only); sibling installs unaffected
- `km doctor` check (`checkSharedSecretsKey`): (1) alias exists, (2) key policy grants account root + sandbox instance-profile role, (3) WARN on aliases whose `resource_prefix` is not in local `km-config.yaml` (orphan from sibling install — mirrors `checkSESRules` orphan pattern)

### Boot-time materialization
- `sops` binary distribution: pre-uploaded to `s3://${prefix}-artifacts-*/binaries/sops` by `km init --sidecars` alongside existing sidecar binaries (km-slack, km-notify-hook, etc.). user-data fetches at boot via existing S3-download pattern. No GitHub egress required, no internet rate-limit risk for hardened profiles.
- Decrypted file: `/etc/sandbox-secrets.env`, owned `root:root`, mode `0400`
- Env exposure to sandbox processes: `/etc/profile.d/zz-sandbox-secrets.sh` sources the env file for all login shells (matches `zz-km-notify.sh` naming + sourcing convention). `km shell`, `km agent run`, SSM session shells all pick it up via shell init.
- Decrypt failure at boot: **hard-abort** — user-data exits non-zero, sandbox enters failed state. Strict fail-closed (chose this over fail-open-with-email because Codex/claude calls would silently 401 against the metering proxy if env vars are missing, defeating the point of declarative secrets)
- S3 bundle cleanup: `km destroy` explicitly deletes `s3://${prefix}-artifacts-*/sandboxes/<id>/secrets.enc.yaml`. S3 lifecycle policy on `sandboxes/<id>/secrets.enc.yaml` with 7-day expiry as belt-and-suspenders for orphaned objects (failed destroys)

### Claude's Discretion
- Exact module Terraform structure for `infra/modules/sandbox-secrets-key/v1.0.0/` (deletion_window_in_days, enable_key_rotation, etc. — copy proven defaults from existing `infra/modules/secrets/v1.0.0/`)
- sops version pin (latest stable release at planner time)
- Userdata template line-by-line layout for the decrypt block
- IAM policy doc structure (statement IDs, condition syntax)
- JSON Schema export shape under `pkg/profile/schemas/` for `spec.secrets`
- Test fixtures and where they live (`pkg/profile/configfiles/` analogue for encrypted-bundle fixtures)
- Whether `km validate` decrypts the bundle (probably no — validation should not require KMS access; just verify file exists + has SOPS metadata block)

</decisions>

<specifics>
## Specific Ideas

- **Phase 88 closure is the acceptance signal**: a Codex sandbox declares `spec.secrets.sopsFile: ./secrets/codex.enc.yaml`, boots, and Phase 88's OpenAI meter writes `BUDGET#ai#gpt-*` rows in DynamoDB without any operator action post-create. UAT path mirrors Phase 88 plan 07.
- **Pattern reuse**: the shared SES rule set (`infra/modules/ses-shared-rule-set/v1.0.0/` + `runBootstrapSharedSES` + Phase 84 doctor orphan-check) is the line-by-line template. If a decision was already made for SES, mirror it here unless explicitly noted.
- **Sidecar binary distribution**: `sops` slots into the existing artifact pipeline (uploaded during `km init --sidecars`, fetched at boot via the same S3 path discipline as km-slack/km-notify-hook). No new infrastructure for binary distribution.
- **gitignore discipline**: `/secrets/*` + `!/secrets/*.enc.yaml` is the standard SOPS-with-git pattern. Documented in OPERATOR-GUIDE.md after `km configure` writes the lines.

</specifics>

<code_context>
## Existing Code Insights

### Reusable Assets

- **`infra/modules/ses-shared-rule-set/v1.0.0/`**: Template for `infra/modules/sandbox-secrets-key/v1.0.0/` — same shared-resource-with-`prevent_destroy` posture, same module-version-bump discipline.
- **`internal/app/cmd/bootstrap.go:runBootstrapSharedSES`** (line 488): Template for `runBootstrapSharedSecretsKey` — includes `--plan` integration (calls into destroy-class gate), dry-run output, region.hcl prereq (Phase 84.4.1), and listerOverride test seam.
- **`internal/app/cmd/bootstrap.go:runBootstrapAll`** (line 912): Template for chaining the new subflow into `--all` ordering.
- **`pkg/compiler/userdata.go:KMArtifactsBucket` wiring** (line 192, 970-971): S3 sidecar binary fetch pattern at boot — direct reuse for the `sops` binary download.
- **PutObject upload path in `internal/app/cmd/create.go:684,883,930,2093`**: Bundle upload to `s3://${prefix}-artifacts-*/sandboxes/<id>/secrets.enc.yaml` slots into the existing pre-terragrunt-apply S3 write flow.
- **`/etc/profile.d/zz-km-notify.sh` pattern in userdata.go**: Template for `/etc/profile.d/zz-sandbox-secrets.sh` (root-writes, sandbox-user-sources, `zz-` prefix for late-load order).
- **`checkSESRules` orphan-WARN logic**: Template for `checkSharedSecretsKey` orphan check (scan aliases matching `*-sandbox-secrets`, WARN if `resource_prefix` not in local `km-config.yaml`).
- **`infra/modules/secrets/v1.0.0/` (per-sandbox SSM/KMS)**: Reference for KMS key Terraform shape (deletion_window_in_days, enable_key_rotation, key policy doc structure), NOT a reuse target — different architecture (per-sandbox vs shared).

### Established Patterns

- **Module-version-bump discipline**: New resource types or naming changes require `v2.0.0` (Phase 84.4 lesson). For `sandbox-secrets-key`, ship as `v1.0.0` for additive use within Phase 89; future schema additions bump to `v1.1.0`.
- **Multi-install `resource_prefix` isolation**: Every km-owned resource name MUST template `${resource_prefix}` (Phase 84.4). Alias `alias/${resource_prefix}-sandbox-secrets` follows the per-install-rules pattern from Phase 84 SES.
- **`km init --sidecars` for fast binary deploy**: Adding `sops` to the artifact upload set requires bumping the sidecar manifest and re-running `km init --sidecars`. Existing sandboxes don't pick it up retroactively — `km destroy && km create`.
- **`km init --lambdas` is NOT a deploy** (memory `project_km_init_lambdas_doesnt_deploy`): If schema change touches Lambda toolchain (which it doesn't for Phase 89), full `km init` is required, not `--lambdas`.
- **terragrunt env exports**: Any km command invoking terragrunt must call `ExportConfigEnvVars(cfg)` before running terragrunt subprocesses (memory `project_terragrunt_env_export`) — `runBootstrapSharedSecretsKey` inherits this.
- **Plan-before-apply destroy-class gate** (Phase 84.2): `--plan` flag flows through to terragrunt plan with destroy-class trip detection. `aws_kms_key` is already in the protected types list (`protected.go`).
- **Show needs mocks**: any new `terragrunt.hcl` with `dependency` blocks must include `"show"` in `mock_outputs_allowed_terraform_commands` (memory `project_terragrunt_show_needs_mocks`).
- **No required_providers in module HCL**: `root.hcl`'s generate "provider" stanza is the single source (memory `project_terragrunt_providers_in_root`).
- **OpenSSL 3.5+ requirement** (`km-slack`, `km-send`): doesn't apply directly here — sops uses KMS API, not OpenSSL — but is reminder that boot-time tool installs must respect the AL2023 / Ubuntu 24.04 baseline.

### Integration Points

- **`pkg/profile/types.go`**: New `Secrets` struct (with `SopsFile string` field) added to `Spec`. Sibling to existing `Execution.Secrets []string` — distinct field names avoid YAML collision.
- **`pkg/profile/validate.go`**: New validation — `sopsFile` path must exist (relative to profile location), must have `.enc.yaml` suffix, must contain `sops:` metadata block. Validation must NOT require KMS access (validation runs without AWS creds).
- **`pkg/profile/schema_export.go` / `pkg/profile/schemas/`**: JSON Schema export gains `spec.secrets` object schema.
- **`pkg/compiler/compiler.go`**: Compile-time hook — when `Spec.Secrets.SopsFile != ""`, compiler reads the encrypted file from the profile-relative path, stages it for upload (does NOT decrypt).
- **`pkg/compiler/userdata.go`**: New template block that fetches `sops` binary from S3, fetches the encrypted bundle from S3 (pre-uploaded by create.go), runs sops -d, writes `/etc/sandbox-secrets.env`, writes `/etc/profile.d/zz-sandbox-secrets.sh`. Gated on `Spec.Secrets.SopsFile != ""`.
- **`internal/app/cmd/create.go`**: New step in the pre-terragrunt-apply phase — uploads the encrypted bundle to `s3://${prefix}-artifacts-*/sandboxes/<id>/secrets.enc.yaml`. Slots in next to existing S3 PutObject calls (line 684, 883, 930, 2093).
- **`internal/app/cmd/destroy.go`**: New cleanup step — deletes the S3 bundle object during destroy. Existing destroy flow already deletes other per-sandbox S3 artifacts.
- **`internal/app/cmd/bootstrap.go`**: New `--shared-secrets-key` flag + `runBootstrapSharedSecretsKey` function + `--all` chain extension + `--all` ↔ `--shared-secrets-key` mutex.
- **`internal/app/cmd/configure.go`**: New gitignore-write step — appends `/secrets/*` + `!/secrets/*.enc.yaml` to `.gitignore` if not already present.
- **`internal/app/cmd/doctor.go`**: New `checkSharedSecretsKey` function + add to default check set.
- **`infra/modules/sandbox-secrets-key/v1.0.0/`**: NEW Terraform module (`main.tf`, `variables.tf`, `outputs.tf`) — `aws_kms_key` + `aws_kms_alias` + key policy. Mirrors `ses-shared-rule-set` structure.
- **`infra/modules/s3-artifacts-lifecycle/`**: Add lifecycle rule for `sandboxes/*/secrets.enc.yaml` with 7-day expiry.
- **`infra/modules/km-operator-policy/`**: Operator IAM gains `kms:CreateKey`, `kms:CreateAlias`, `kms:DescribeKey`, `kms:PutKeyPolicy` scoped to `*-sandbox-secrets` aliases (for bootstrap).
- **Sandbox instance profile** (in profile compiler IAM emission): gains `kms:Decrypt` on the shared key, scoped by `kms:ResourceAliases` condition for cross-prefix isolation.

</code_context>

<deferred>
## Deferred Ideas

### v2 — Confirmed in roadmap entry
- **Per-profile / per-sandbox KMS key isolation** via key policy + `aws:PrincipalTag` — limits blast radius from "any sandbox can decrypt any bundle" (v1 posture) to "only this sandbox-id can decrypt its own bundle".
- **Secret rotation without sandbox recreate** — today rotation = `km destroy && km create`. v2 would support `km secrets refresh <sandbox-id>` to re-fetch + re-decrypt without infra churn.
- **`km secrets edit <profile>`** — ergonomic wrapper around `sops` for in-place edit. Today operators run `sops secrets/codex.enc.yaml` directly.

### Surfaced during discussion, parked
- **`km secrets validate <profile>`** — standalone validator that decrypts a bundle without launching a sandbox (verifies KMS access + bundle structure). Useful but adds an AWS-credential-requiring path to `km validate` that doesn't exist today.
- **Non-env secret types** (files-on-disk, certs, SSH keys, .netrc) — would require the "nested with `env:` / `files:`" schema shape we explicitly rejected for v1 in favor of flat env-var YAML. Revisit when a real need emerges.
- **CloudTrail-backed "last-used" age check in `km doctor`** — would catch misconfigured profiles that reference a key nobody decrypts against. Useful but heavier API calls; skip for v1.
- **Per-region key replication** — `km` is single-region-per-install today (config-driven). Multi-region installs would need cross-region key replicas. Out of scope until multi-region support arrives.
- **Profile-to-bundle 1:1 enforcement** — explicitly rejected: operators decide topology based on blast radius. Re-evaluate only if blast-radius incidents drive the requirement.

</deferred>

---

*Phase: 89-sops-secret-injection-for-sandboxes*
*Context gathered: 2026-05-26*
