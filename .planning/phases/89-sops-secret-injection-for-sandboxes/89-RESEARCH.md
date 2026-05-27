# Phase 89: SOPS Secret Injection for Sandboxes — Research

**Researched:** 2026-05-26
**Domain:** Declarative secret injection, SOPS + AWS KMS, Terraform module skeleton, terragrunt bootstrap subflows, EC2 user-data templating, sandbox IAM emission
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Schema shape:**
- New top-level section: `spec.secrets.sopsFile: ./path/to/file.enc.yaml`
- Sibling to (NOT replacement of) existing `spec.execution.secrets: []string` (SSM-path field stays as-is — clean separation between SOPS and legacy SSM injection)
- **Single file** per profile — not a list, no overlay fragments
- Path resolution: relative to the profile YAML location (matches how `extends:` works today — keeps profiles portable)
- Bundle contents: flat key:value YAML. Every top-level key materializes as one env var in `/etc/sandbox-secrets.env`. Reserved keys `sops` and `_meta` ignored (SOPS itself embeds a `sops:` metadata block in encrypted output)

**Repo layout for encrypted files:**
- Convention: `secrets/` directory at operator repo root
- File suffix: `*.enc.yaml` **mandatory** (anchors the gitignore rule)
- Filename within `secrets/`: operator-chosen, free-form (e.g., `secrets/codex.enc.yaml`, `secrets/shared.enc.yaml`)
- `.gitignore` policy: `/secrets/*` + `!/secrets/*.enc.yaml` — encrypted files version with profiles, plaintext workdir files never leak
- `km configure` writes the two `.gitignore` lines on first run (idempotent — only if not already present)
- Profile-to-bundle topology: km does NOT enforce 1:1 or 1:N — operators decide based on blast radius

**Bootstrap UX for the shared KMS key:**
- New top-level bootstrap flag: `km bootstrap --shared-secrets-key` (mirrors `--shared-ses`)
- Module: `infra/modules/sandbox-secrets-key/v1.0.0/` with `lifecycle.prevent_destroy = true` and module-version-bump discipline (v1.0.0 → v1.1.0 additive; v2.0.0 resource-name churn)
- `km bootstrap --all` chains it: foundation → shared-ses → shared-secrets-key. `--all` mutex with `--shared-secrets-key`
- `--plan` honors the Phase 84.2 destroy-class gate; `aws_kms_key` is already in the protected types list
- KMS naming: alias `alias/${resource_prefix}-sandbox-secrets` (account+region scoped — km is single-region-per-install)
- Sandbox instance-profile IAM: `kms:Decrypt` with `Condition: kms:ResourceAliases` for cross-prefix isolation
- `km uninit` removes only this install's KMS key and alias; sibling installs unaffected
- `km doctor` check (`checkSharedSecretsKey`): (1) alias exists, (2) key policy grants account root + sandbox instance-profile role, (3) WARN on aliases whose `resource_prefix` is not in local `km-config.yaml`

**Boot-time materialization:**
- `sops` binary distribution: pre-uploaded to `s3://${prefix}-artifacts-*/binaries/sops` by `km init --sidecars` alongside existing sidecar binaries. User-data fetches at boot via existing S3-download pattern. No GitHub egress required.
- Decrypted file: `/etc/sandbox-secrets.env`, owned `root:root`, mode `0400`
- Env exposure: `/etc/profile.d/zz-sandbox-secrets.sh` sources the env file for all login shells (matches `zz-km-notify.sh` naming + sourcing convention)
- Decrypt failure at boot: **hard-abort** — user-data exits non-zero, sandbox enters failed state
- S3 bundle cleanup: `km destroy` explicitly deletes `s3://${prefix}-artifacts-*/sandboxes/<id>/secrets.enc.yaml`. S3 lifecycle policy on `sandboxes/<id>/secrets.enc.yaml` with 7-day expiry as belt-and-suspenders.

### Claude's Discretion
- Exact module Terraform structure (deletion_window_in_days, enable_key_rotation, etc. — copy proven defaults from existing `infra/modules/secrets/v1.0.0/`)
- sops version pin (latest stable at planner time)
- Userdata template line-by-line layout for the decrypt block
- IAM policy doc structure (statement IDs, condition syntax)
- JSON Schema export shape under `pkg/profile/schemas/` for `spec.secrets`
- Test fixtures and where they live
- Whether `km validate` decrypts the bundle (probably no — validation should not require KMS access; just verify file exists + has SOPS metadata block)

### Deferred Ideas (OUT OF SCOPE)
- **Per-profile / per-sandbox KMS key isolation** via key policy + `aws:PrincipalTag` (v2)
- **Secret rotation without sandbox recreate** (v2 `km secrets refresh <sandbox-id>`)
- **`km secrets edit <profile>`** ergonomic wrapper (v2)
- **`km secrets validate <profile>`** standalone validator with KMS access
- **Non-env secret types** (files-on-disk, certs, SSH keys, .netrc) — flat env-var YAML chosen for v1
- **CloudTrail-backed "last-used" age check in `km doctor`**
- **Per-region key replication** (km is single-region-per-install today)
- **Profile-to-bundle 1:1 enforcement** (explicitly rejected — operators decide topology)
</user_constraints>

<phase_requirements>
## Phase Requirements (Proposed Synthetic IDs)

Phase 89 has no requirement IDs assigned in ROADMAP.md (entry says "TBD"). The planner MUST mint phase-local synthetic IDs in REQUIREMENTS.md following the Phase 84.2/84.3 pattern. Recommended mint:

| ID | Description | Research Support |
|----|-------------|------------------|
| SOPS-01-SCHEMA | New `spec.secrets.sopsFile: string` profile schema field; JSON Schema embed entry under `pkg/profile/schemas/sandbox_profile.schema.json`; `SecretsSpec` struct added to `Spec` in `pkg/profile/types.go` | "Code Examples — Profile Schema" + "Architecture Patterns — Profile schema extension" |
| SOPS-02-VALIDATION | `km validate` semantic check: file exists relative to profile, `.enc.yaml` suffix, contains a `sops:` block (no KMS calls — validation runs offline) | "Code Examples — Semantic Validator" + Pitfall "Validation must not require AWS creds" |
| SOPS-03-KMS-MODULE | New `infra/modules/sandbox-secrets-key/v1.0.0/` with `aws_kms_key` + `aws_kms_alias` + key policy + `lifecycle.prevent_destroy=true`; `enable_key_rotation=true`; `deletion_window_in_days=30` (copied from `infra/modules/secrets/v1.0.0/`) | "Standard Stack — Module skeleton" + reference module `ses-shared-rule-set/v1.0.0/` |
| SOPS-04-MODULE-WIRING | New `infra/live/use1/sandbox-secrets-key/terragrunt.hcl` consuming `site.hcl` + reading `region.hcl` from parent dir; `include "root"`; tags `km:owner=foundation`, `km:phase=89`, `km:component=sandbox-secrets-key` | "Code Examples — terragrunt.hcl skeleton" mirroring `ses-shared-rule-set/terragrunt.hcl` |
| SOPS-05-BOOTSTRAP-FLAG | New `--shared-secrets-key` flag on `km bootstrap`; `runBootstrapSharedSecretsKey` function mirroring `runBootstrapSharedSES` (line 488); test seam `RunBootstrapSharedSecretsKeyFunc` | "Code Examples — Bootstrap subflow" + bootstrap.go:488 |
| SOPS-06-BOOTSTRAP-PLAN | `--shared-secrets-key --plan` routes through Phase 84.2 destroy-class gate; `runBootstrapSharedSecretsKeyPlanWithWriter` mirrors `runBootstrapSharedSESPlanWithWriter` (line 961) | "Standard Stack — Plan flow" + bootstrap.go:961 |
| SOPS-07-BOOTSTRAP-ALL-CHAIN | `runBootstrapAll` extended: foundation → shared-ses → shared-secrets-key, in that order; `--all` ↔ `--shared-secrets-key` mutex added in cobra RunE | bootstrap.go:912, bootstrap.go:1123 |
| SOPS-08-IAM-OPERATOR | Operator IAM (`infra/modules/km-operator-policy`) — KMS broad permission set already present (`kms:*` on all resources, line 484); NO change required for operator bootstrap. Document this as a "free pass". | "Don't Hand-Roll — IAM" + km-operator-policy/v1.0.0/main.tf:474-489 |
| SOPS-09-IAM-SANDBOX | `infra/modules/ec2spot/v1.X.0/main.tf` gains `aws_iam_role_policy.ec2spot_sandbox_secrets_kms` — `kms:Decrypt` with `Condition: StringEquals { kms:ResourceAliases = "alias/${var.resource_prefix}-sandbox-secrets" }`; module-version-bump to v1.2.0 (additive policy) | "Code Examples — Sandbox IAM" + ec2spot/v1.1.0/main.tf:387 (github-token policy precedent) |
| SOPS-10-SCHEMA-EXPORT | JSON Schema in `pkg/profile/schemas/sandbox_profile.schema.json` gains `spec.secrets` object schema with `sopsFile` string property | "Standard Stack — schema embed" + pkg/profile/schema.go:13 |
| SOPS-11-COMPILER-UPLOAD | `internal/app/cmd/create.go` new pre-terragrunt-apply step: reads `Spec.Secrets.SopsFile` (relative to profile path), uploads bytes verbatim to `s3://{artifactBucket}/sandboxes/{sandboxID}/secrets.enc.yaml`; slots into the existing PutObject sequence (line 676-694, 882-895) | "Architecture Patterns — Bundle upload sequence" + create.go:684/883/930/2093 |
| SOPS-12-USERDATA-FETCH | `pkg/compiler/userdata.go` gains template fields `SopsBundlePresent bool` + render block: `aws s3 cp s3://${KM_ARTIFACTS_BUCKET}/binaries/sops /opt/km/bin/sops; aws s3 cp s3://${KM_ARTIFACTS_BUCKET}/sandboxes/{sandboxID}/secrets.enc.yaml /etc/sandbox-secrets.enc.yaml`; gated on `{{- if .SopsBundlePresent }}` | "Code Examples — Userdata template" + userdata.go:970-979 (sidecar download precedent) |
| SOPS-13-USERDATA-DECRYPT | New userdata block: `sops decrypt --output-type dotenv /etc/sandbox-secrets.enc.yaml > /etc/sandbox-secrets.env`; ownership `root:root`, mode `0400`; runs after AWS creds are reachable (post-IMDS init) | "Code Examples — sops decrypt invocation" + "Pitfall — sops dotenv output" |
| SOPS-14-USERDATA-ENV-EXPOSURE | `/etc/profile.d/zz-sandbox-secrets.sh` written with `[ -r /etc/sandbox-secrets.env ] && set -a && . /etc/sandbox-secrets.env && set +a` (uses `set -a` for dotenv → exported env vars) | "Pitfall — dotenv → exported vars" + zz-km-notify.sh precedent |
| SOPS-15-BOOT-FAIL-ABORT | Userdata `set -e` ensures decrypt failure aborts boot; explicit `exit 1` after a final fallback test on `/etc/sandbox-secrets.env` existence | "Architecture Patterns — Hard-fail discipline" |
| SOPS-16-DESTROY-CLEANUP | `internal/app/cmd/destroy.go` new step: `s3.DeleteObject(s3://${artifactsBucket}/sandboxes/{sandboxID}/secrets.enc.yaml)` — non-fatal warning if missing (idempotent) | "Code Examples — Destroy hook" + destroy.go S3 cleanup precedents |
| SOPS-17-S3-LIFECYCLE | `infra/modules/s3-artifacts-lifecycle/v1.1.0/main.tf` adds rule `sandbox-secrets-7day` with `filter.prefix = "sandboxes/"` + `expiration { days = 7 }`; module bumped v1.0.0 → v1.1.0 (additive rule) | "Code Examples — S3 lifecycle additive" + s3-artifacts-lifecycle/v1.0.0/main.tf |
| SOPS-18-DOCTOR-CHECK | `checkSharedSecretsKey` in `internal/app/cmd/doctor.go`: ListAliases scan for `alias/*-sandbox-secrets`; for each, parse prefix from alias name (strip `alias/`, split on `-sandbox-secrets`), WARN if prefix ≠ `localPrefix`; reuses existing `KMSCleanupAPI.ListAliases` (doctor.go:1208) | "Code Examples — Doctor check" + checkSESRules at doctor.go:3496 |
| SOPS-19-CONFIGURE-GITIGNORE | `internal/app/cmd/configure.go` first-run step: append `/secrets/*` + `!/secrets/*.enc.yaml` to `.gitignore` if not already present (read-modify-write, idempotent) | "Code Examples — Idempotent gitignore append" |
| SOPS-20-SIDECARS-SOPS-DEPLOY | `internal/app/cmd/init.go::buildAndUploadSidecars` extended to download sops binary (v3.13.1 linux/amd64) and upload to `s3://{bucket}/binaries/sops`; mirrors `fetchAndUploadOtelcolContrib` pattern (init.go:1881) | "Standard Stack — sops binary" + init.go:1876-1916 |
| SOPS-21-UNINIT-CLEANUP | `km uninit` (if exists; else km-bootstrap teardown) removes per-install KMS alias + scheduled key delete; leaves sibling-install keys untouched (matches `km uninit` SES-rules-only pattern in CLAUDE.md) | CLAUDE.md "km uninit removes only this install's two rules and leaves the shared rule set and sibling installs' rules intact" |
| SOPS-22-DOCS | `docs/sandbox-secrets.md` operator guide + `CLAUDE.md` "Where to look" entry + OPERATOR-GUIDE.md `## SOPS secret injection` section | "Standard Stack — operator docs" |
| SOPS-23-UAT-ACCEPTANCE | End-to-end: Codex sandbox declares `spec.secrets.sopsFile: ./secrets/codex.enc.yaml`, `km create`, boots clean, Phase 88 OpenAI meter writes `BUDGET#ai#gpt-*` rows without operator post-create wiring (no SSM SendCommand, no out-of-band SecureString) | "Acceptance Criteria — Phase 88 closure" |
</phase_requirements>

## Summary

Phase 89 adds declarative SOPS-encrypted secrets to SandboxProfiles. A new `spec.secrets.sopsFile` field references an encrypted bundle file (relative to the profile YAML). The compiler uploads the encrypted bundle to S3 pre-`terragrunt apply`. User-data at boot fetches a pre-deployed `sops` binary from S3, decrypts the bundle using a shared per-install KMS key, materializes the result as `/etc/sandbox-secrets.env`, and exposes the key=value lines as exported shell env vars via `/etc/profile.d/zz-sandbox-secrets.sh`. The shared KMS key is provisioned by a new `km bootstrap --shared-secrets-key` subflow that mirrors the proven `--shared-ses` shape end-to-end (module skeleton, plan-flow, `--all` chain, orphan-WARN doctor check, `km uninit` semantics).

The architecture is dominated by **pattern replication**, not new design — every component has a direct precedent in this codebase (Phase 84 SES shared rule set, Phase 56 sidecar binary upload, Phase 75 S3 artifacts lifecycle, ec2spot github-token KMS policy). The only "new" technology is `sops` (v3.13.1) which is a single static linux/amd64 binary distributed via GitHub releases with native AWS KMS support and `--output-type dotenv` for flat env-var bundles — no glue code required.

**Primary recommendation:** Treat Phase 89 as a 1:1 port of the Phase 84 SES shared rule set pattern, with `aws_kms_key` substituting for `aws_ses_receipt_rule_set` and `sops decrypt --output-type dotenv` substituting for the existing user-data env-file-write idiom. Every uncertain decision should default to "do what shared-ses did at that exact spot." The single genuinely-new addition is the sops binary upload step in `km init --sidecars`.

## Standard Stack

### Core
| Library / Asset | Version | Purpose | Why Standard |
|-----------------|---------|---------|--------------|
| `getsops/sops` | v3.13.1 (May 16 2026) | KMS-aware encrypted YAML/JSON/dotenv format with first-class AWS KMS support | Industry standard for declarative secrets; CNCF-graduated; single static binary; native `--output-type dotenv` |
| AWS KMS | Provider's current (`hashicorp/aws` from root.hcl) | Key + alias for symmetric encryption | Already in protected types list (Phase 84.2); already broadly granted to operator role (kms:* on *); CLAUDE.md INFR-04 calls out KMS-for-SOPS as v1 requirement |
| `aws-sdk-go-v2/service/kms` | matches existing deps | ListAliases for doctor check | Same SDK already imported (doctor.go uses kms.ListAliases at line 1208) |
| `aws-sdk-go-v2/service/s3` | matches existing deps | Bundle upload pre-apply + delete on destroy | Already wired in create.go and destroy.go |
| `hashicorp/aws` Terraform | from `root.hcl` provider generate | `aws_kms_key` + `aws_kms_alias` + `aws_iam_policy_document` | NEVER declare `required_providers` in module HCL — `root.hcl`'s generate "provider" stanza is the single source (memory `project_terragrunt_providers_in_root`) |

### Supporting
| Asset | Version | Purpose | When to Use |
|-------|---------|---------|-------------|
| `goccy/go-yaml` | matches `pkg/profile` | Parse SOPS-encrypted bundle to detect presence of `sops:` metadata block in `km validate` | When implementing offline validation that must not require AWS creds |
| `santhosh-tekuri/jsonschema/v6` | matches `pkg/profile/schema.go:11` | JSON Schema validation of `spec.secrets` shape | Add object schema entry to embedded `sandbox_profile.schema.json` |
| `github.com/spf13/cobra` | matches existing | New `--shared-secrets-key` flag | Mirror `--shared-ses` flag declaration in bootstrap.go:1148 |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| sops + dotenv output | Mozilla SOPS with `--output-type yaml` + jq+envsubst glue | Native `dotenv` mode (verified via official docs) avoids shell glue, eliminates a class of escaping bugs |
| Single static binary | Package install (`yum`/`apt`) at boot | Boot-time package install adds DNS/HTTP egress requirement and ~10s latency; static binary is two `aws s3 cp` ops (already proven in this codebase) |
| `sops -d` (legacy) | `sops decrypt` (current) | Functionally identical; current syntax matches v3.x docs and `--output-type dotenv` flag examples |
| sops v3.13.1 | sops v3.11.0 | Pin to latest (released 2026-05-16, 10 days old); dotenv format stable across both |

**Installation (in `km init --sidecars`):**
```bash
# Download once, upload to S3 — mirror fetchAndUploadOtelcolContrib pattern (init.go:1881)
curl -sL https://github.com/getsops/sops/releases/download/v3.13.1/sops-v3.13.1.linux.amd64 \
  -o build/sops
chmod +x build/sops
aws s3 cp build/sops s3://${KM_ARTIFACTS_BUCKET}/binaries/sops --profile klanker-terraform
```

## Architecture Patterns

### Recommended Project Structure (additive)
```
infra/modules/sandbox-secrets-key/v1.0.0/   # NEW — mirrors ses-shared-rule-set/v1.0.0
├── main.tf                                  # aws_kms_key + aws_kms_alias + key policy doc
├── variables.tf                             # resource_prefix, aws_region, tags
└── outputs.tf                               # alias_name, key_arn

infra/live/use1/sandbox-secrets-key/         # NEW
└── terragrunt.hcl                           # mirrors ses-shared-rule-set/terragrunt.hcl

pkg/profile/                                 # MODIFIED
├── types.go                                 # +SecretsSpec, +Spec.Secrets *SecretsSpec
├── validate.go                              # +ValidateSemantic spec.secrets clause
└── schemas/sandbox_profile.schema.json      # +spec.secrets object schema

pkg/compiler/userdata.go                     # MODIFIED — new template block + field
internal/app/cmd/
├── bootstrap.go                             # +runBootstrapSharedSecretsKey + cobra flag
├── create.go                                # +bundle PutObject step (pre-apply)
├── destroy.go                               # +bundle DeleteObject step
├── doctor.go                                # +checkSharedSecretsKey + invocation
├── configure.go                             # +gitignore append step
└── init.go                                  # +sops binary upload in buildAndUploadSidecars

infra/modules/s3-artifacts-lifecycle/v1.1.0/ # NEW (or update v1.0.0 to v1.1.0)
└── main.tf                                  # +sandboxes/ 7-day rule

infra/modules/ec2spot/v1.2.0/                # NEW (additive bump from v1.1.0)
└── main.tf                                  # +ec2spot_sandbox_secrets_kms iam_role_policy

docs/sandbox-secrets.md                      # NEW — operator guide
secrets/                                     # NEW operator-repo directory (gitignored except *.enc.yaml)
```

### Pattern 1: Bootstrap Subflow Replication

**What:** `runBootstrapSharedSecretsKey` is a line-by-line port of `runBootstrapSharedSES` (bootstrap.go:488-639) with KMS substituted for SES.

**When to use:** Every shared-resource bootstrap subflow in this codebase follows this exact 6-stage skeleton: (1) `loadBootstrapConfig` + `ExportTerragruntEnvVars`, (2) `ensureRegionHCL`, (3) AWS client construction with `--dry-run` graceful degradation, (4) auto-detect "does this resource already exist?", (5) `os.Setenv("KM_REGISTER_*", ...)` to control terraform module `count`, (6) `ApplyTerragruntFunc(ctx, dir)`.

**Example:**
```go
// Source: bootstrap.go:488-639 (verified pattern)
func runBootstrapSharedSecretsKey(ctx context.Context, cfg *config.Config, dryRun bool, w io.Writer, kmsListerOverride KMSAliasLister) error {
    loadedCfg, err := loadBootstrapConfig(cfg)
    if err != nil { return err }
    ExportTerragruntEnvVars(loadedCfg)

    // region.hcl prereq — Phase 84.4.1 BOOTSTRAP-REGION-HCL-PREREQ
    region := loadedCfg.PrimaryRegion
    regionLabel := compiler.RegionLabel(region)
    regionDir := filepath.Join(findRepoRoot(), "infra", "live", regionLabel)
    if err := ensureRegionHCL(regionDir, regionLabel, region); err != nil { return err }

    moduleDir := filepath.Join(regionDir, "sandbox-secrets-key")
    aliasName := fmt.Sprintf("alias/%s-sandbox-secrets", loadedCfg.GetResourcePrefix())

    // Auto-detect — was the alias already created (e.g., from a sibling install or prior run)?
    var lister KMSAliasLister
    if kmsListerOverride != nil {
        lister = kmsListerOverride
    } else {
        awsCfg, err := awspkg.LoadAWSConfigInRegion(ctx, "klanker-terraform", region)
        if err != nil {
            if dryRun {
                fmt.Fprintf(w, "Dry run — would run: terragrunt apply %s\n", moduleDir)
                return nil
            }
            return fmt.Errorf("load AWS config: %w", err)
        }
        lister = kms.NewFromConfig(awsCfg)
    }
    registerKey, err := detectSharedSecretsKeyState(ctx, lister, aliasName)
    if err != nil { /* dry-run tolerant — same shape as SES */ }
    os.Setenv("KM_REGISTER_SECRETS_KEY", strconv.FormatBool(registerKey))

    if dryRun {
        fmt.Fprintf(w, "Dry run — would run: terragrunt apply %s\n", moduleDir)
        return nil
    }
    return ApplyTerragruntFunc(ctx, moduleDir)
}
```

### Pattern 2: User-Data Conditional Template Block

**What:** New template fields `SopsBundlePresent bool` (+ optional `SopsBundleS3Key string` for explicit override) gate a conditional block in `userDataTemplate`.

**When to use:** Every per-profile-feature userdata extension in this codebase uses this gate idiom — see `EnforceEBPF`, `NotifySlackEnabled`, `LearnMode`, `EFSFilesystemID`. Block is wrapped in `{{- if .SopsBundlePresent }}...{{- end }}` and field is populated in the compiler's userdata params constructor.

**Example slot location:** After section "5. Sidecar binaries: install DNS proxy, HTTP proxy, audit log" (userdata.go:964) — the new block needs sops binary downloaded first, so it must come after the existing aws s3 cp sidecar block:

```bash
# Source: userdata.go:970-979 (sidecar pattern) + getsops/sops docs (verified)
{{- if .SopsBundlePresent }}
# ============================================================
# 5.5. SOPS secret injection (Phase 89)
# ============================================================
echo "[km-bootstrap] Decrypting SOPS bundle..."
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/binaries/sops" /opt/km/bin/sops
chmod +x /opt/km/bin/sops
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sandboxes/{{ .SandboxID }}/secrets.enc.yaml" /etc/sandbox-secrets.enc.yaml
chmod 0400 /etc/sandbox-secrets.enc.yaml
# Decrypt to dotenv format; sops uses AWS SDK creds (instance profile) automatically.
# --output-type dotenv emits KEY=VALUE lines (one per top-level YAML key).
if ! /opt/km/bin/sops decrypt --output-type dotenv /etc/sandbox-secrets.enc.yaml > /etc/sandbox-secrets.env; then
  echo "[km-bootstrap] FATAL: sops decrypt failed — aborting boot" >&2
  exit 1
fi
chown root:root /etc/sandbox-secrets.env
chmod 0400 /etc/sandbox-secrets.env
# Re-mode the encrypted bundle to 0400 (already 0400 above; defensive).
# Expose key=value as exported env vars via /etc/profile.d/.
cat > /etc/profile.d/zz-sandbox-secrets.sh << 'SOPSENV'
# Phase 89: load decrypted secrets into login-shell env.
# set -a/+a flips auto-export so dotenv KEY=VALUE lines become exported vars.
if [ -r /etc/sandbox-secrets.env ]; then
  set -a
  . /etc/sandbox-secrets.env
  set +a
fi
SOPSENV
chmod 0644 /etc/profile.d/zz-sandbox-secrets.sh
echo "[km-bootstrap] SOPS bundle decrypted to /etc/sandbox-secrets.env (root:root 0400)"
{{- end }}
```

### Pattern 3: Pre-Apply S3 Bundle Upload

**What:** The compiler's encrypted bundle upload slots into `create.go`'s pre-`terragrunt apply` PutObject sequence, alongside the existing full-userdata, profile-yaml, and init-script uploads.

**When to use:** Any per-sandbox compile-time artifact that must exist in S3 before user-data runs (because user-data downloads it). Three existing precedents at create.go:684 (userdata), 883 (profile yaml), 930 (init script).

**Example (create.go ~line 695, post-userdata upload):**
```go
// Source: create.go:676-694 (verified pattern)
if resolvedProfile.Spec.Secrets != nil && resolvedProfile.Spec.Secrets.SopsFile != "" {
    profileDir := filepath.Dir(profilePath)
    bundlePath := filepath.Join(profileDir, resolvedProfile.Spec.Secrets.SopsFile)
    bundleBytes, readErr := os.ReadFile(bundlePath)
    if readErr != nil {
        return fmt.Errorf("read sops bundle %s: %w", bundlePath, readErr)
    }
    if _, putErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
        Bucket:      aws.String(artifactBucket),
        Key:         aws.String(fmt.Sprintf("sandboxes/%s/secrets.enc.yaml", sandboxID)),
        Body:        bytes.NewReader(bundleBytes),
        ContentType: aws.String("application/x-yaml"),
    }); putErr != nil {
        return fmt.Errorf("upload sops bundle: %w", putErr)
    }
    fmt.Fprintf(os.Stderr, "  ✓ SOPS bundle uploaded to s3://%s/sandboxes/%s/secrets.enc.yaml\n", artifactBucket, sandboxID)
}
```

### Pattern 4: Doctor Orphan-WARN

**What:** `checkSharedSecretsKey` reuses the SES-rules orphan-WARN shape verbatim (doctor.go:3487-3552) but pivots on `kms.ListAliases` instead of `ses.DescribeReceiptRuleSet`.

**When to use:** Multi-install resource discovery — operator wants to know "are there KMS aliases on this account that aren't owned by my install?"

**Example:**
```go
// Source: doctor.go:1196-1221 (ListAliases pattern) + doctor.go:3487-3552 (orphan-WARN logic)
type KMSAliasLister interface {
    ListAliases(ctx context.Context, params *kms.ListAliasesInput, optFns ...func(*kms.Options)) (*kms.ListAliasesOutput, error)
    DescribeKey(ctx context.Context, params *kms.DescribeKeyInput, optFns ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
}

func checkSharedSecretsKey(ctx context.Context, client KMSAliasLister, localPrefix string) CheckResult {
    const name = "Shared secrets KMS key"
    if client == nil {
        return CheckResult{Name: name, Status: CheckSkipped, Message: "KMS client unavailable"}
    }
    expectAlias := fmt.Sprintf("alias/%s-sandbox-secrets", localPrefix)
    suffix := "-sandbox-secrets"

    var ownFound bool
    var orphans []string
    var marker *string
    for {
        out, err := client.ListAliases(ctx, &kms.ListAliasesInput{Marker: marker})
        if err != nil {
            return CheckResult{Name: name, Status: CheckError, Message: fmt.Sprintf("ListAliases: %v", err)}
        }
        for _, a := range out.Aliases {
            n := awssdk.ToString(a.AliasName)
            if !strings.HasSuffix(n, suffix) { continue }
            if n == expectAlias {
                ownFound = true
            } else {
                orphans = append(orphans, n)
            }
        }
        if !out.Truncated { break }
        marker = out.NextMarker
    }
    if !ownFound {
        return CheckResult{Name: name, Status: CheckWarn,
            Message:     fmt.Sprintf("alias %s not found", expectAlias),
            Remediation: "Run `km bootstrap --shared-secrets-key`"}
    }
    if len(orphans) > 0 {
        return CheckResult{Name: name, Status: CheckWarn,
            Message:     fmt.Sprintf("orphan secrets KMS aliases: %s", strings.Join(orphans, ", ")),
            Remediation: "Other installs' aliases — expected when sibling install present"}
    }
    return CheckResult{Name: name, Status: CheckOK,
        Message: fmt.Sprintf("alias %s healthy", expectAlias)}
}
```

### Anti-Patterns to Avoid

- **Decrypting bundle in `km validate`:** Validation must not require AWS creds (see validate.go — schema + semantic checks are pure). Validation should only check (1) file exists relative to profile, (2) `.enc.yaml` suffix, (3) contains a `sops:` block via YAML parse (offline).
- **Embedding sops binary in km binary via go-embed:** Increases km binary size by ~25MB and forces a re-link per sops version bump. The S3-upload-once pattern (fetch-on-init) is already established for otelcol-contrib (init.go:1881).
- **Per-profile KMS keys in v1:** Explicitly rejected in CONTEXT.md — defer to v2 (key policy + aws:PrincipalTag). Plan v1 with the shared-key alias contract so v2 can swap policy without schema churn.
- **`required_providers` in `infra/modules/sandbox-secrets-key/v1.0.0/main.tf`:** Memory `project_terragrunt_providers_in_root` — providers live ONLY in root.hcl's generate "provider" stanza.
- **Forgetting `mock_outputs_allowed_terraform_commands = [..., "show"]`** if the new terragrunt.hcl has dependency blocks (memory `project_terragrunt_show_needs_mocks`). The `ses-shared-rule-set/terragrunt.hcl` has NO dependency blocks (verified at line 1-43), so the sandbox-secrets-key terragrunt.hcl can follow the same dep-free shape and skip this pitfall entirely.
- **Decrypting before instance profile credentials are reachable:** IMDS comes up later in cloud-init on AL2023; the decrypt block MUST come after the existing sidecar block (userdata.go:964) where AWS-CLI / SDK calls are already known to work.
- **dotenv values containing `=` or whitespace:** `sops --output-type dotenv` should quote-escape, but planner should add a test fixture with a value containing `=` and verify the env-file shell sourcing handles it.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| AWS-KMS-aware secret format | Custom encryption-at-rest scheme | `getsops/sops` v3.13.1 | CNCF-graduated, format-stable since 2018, native KMS integration |
| YAML → env-file translation | jq/yq/sed pipeline | `sops decrypt --output-type dotenv` | Native flag verified in official docs; handles quoting/escaping correctly |
| KMS broad operator permissions | Per-statement scoping for KMS | `kms:*` on `*` (km-operator-policy already grants it at main.tf:484) | Already in place; no operator IAM changes needed |
| Sandbox-side KMS key isolation | Per-key IAM scoping | `Condition: StringEquals { kms:ResourceAliases }` on the broad `kms:Decrypt` policy | Standard AWS pattern; isolates per `resource_prefix` without per-key ARN enumeration |
| sops binary distribution | bake into AMI / install at boot via package manager | S3 upload during `km init --sidecars` + `aws s3 cp` at boot | Mirrors otelcol-contrib + 5 other sidecar binaries; well-trodden path |
| Bundle file presence detection | Probe S3 from compiler | `Spec.Secrets.SopsFile != ""` from profile struct | Profile is source of truth at compile time; S3 upload is fire-and-forget |
| First-run gitignore append | Operator does manually | Read `.gitignore`, check substring, append if missing | Three-line idempotent block; same shape as `km configure` writing `# Add this file to .gitignore` header (configure.go:702) |

**Key insight:** The sops project does ALL the cryptographic heavy lifting (envelope encryption, key derivation, format parsing, AWS SDK auth chain). Phase 89 is plumbing — file paths, IAM conditions, terraform module skeleton, doctor scanning, gitignore append. Resist any temptation to reimplement what sops already does correctly.

## Common Pitfalls

### Pitfall 1: AWS SDK Credential Chain at Boot
**What goes wrong:** `sops decrypt` fails with `NoCredentialProviders` early in cloud-init.
**Why it happens:** IMDS isn't reachable until network is up; running sops decrypt in section 1 or 2 of user-data races the instance profile creds.
**How to avoid:** Place the decrypt block **after** the existing sidecar binary download (userdata.go:970), which is already proven to have working AWS creds (it does `aws s3 cp` from the same S3 bucket with the same credential chain).
**Warning signs:** sops error message mentioning "no EC2 IMDS role" or "no credentials in chain" — re-order the userdata block.

### Pitfall 2: dotenv Output Format Edge Cases
**What goes wrong:** A secret value containing `"`, `\`, `\n`, `=`, or spaces produces a malformed `/etc/sandbox-secrets.env` that breaks `. /etc/sandbox-secrets.env`.
**Why it happens:** dotenv has no formal spec; sops's encoder may not match shell's parser for exotic content.
**How to avoid:** (a) Document that values must be ASCII printable, single-line, no embedded `"` or `\`. (b) Add a Phase 89 test fixture with the most-exotic ASCII chars sops claims to support and verify shell sourcing works. (c) Use `set -a; . /etc/sandbox-secrets.env; set +a` (NOT `source` and NOT `export $(...)`) — the dot-sourcing path handles quoted values correctly.
**Warning signs:** Empty env vars after boot for entries that should have values; shell parse errors in cloud-init log.

### Pitfall 3: Multi-Region KMS Aliases Are Not Cross-Region
**What goes wrong:** A multi-region install (today not supported, but planned future) creates per-region aliases that can't decrypt each other's bundles.
**Why it happens:** KMS aliases are region-scoped. `alias/km-sandbox-secrets` in us-east-1 ≠ same alias in us-west-2.
**How to avoid:** Document that v1 is single-region-per-install (matches existing km posture). Defer multi-region key replication to v2.
**Warning signs:** Operator manually copying bundles across regions and hitting `NotFoundException` from KMS.

### Pitfall 4: `kms:ResourceAliases` Condition Pitfall
**What goes wrong:** Sandbox IAM policy with `Condition: StringEquals { kms:ResourceAliases: "alias/km-sandbox-secrets" }` denies decrypt because the alias isn't on the *underlying key* yet.
**Why it happens:** `kms:ResourceAliases` evaluates against the aliases attached to the target key at evaluation time. If terraform creates the policy before the alias is attached, the first decrypt fails.
**How to avoid:** In `infra/live/use1/sandbox-secrets-key/terragrunt.hcl`, ensure the alias depends on the key (Terraform's implicit dependency via `target_key_id` handles this). In sandbox-side IAM (ec2spot module), use the alias-form ARN in `Resource = "arn:aws:kms:*:*:alias/${var.resource_prefix}-sandbox-secrets"` PLUS the condition — belt-and-suspenders.
**Warning signs:** First boot of first sandbox post-bootstrap fails with `AccessDenied` on `kms:Decrypt`. Re-running fixes it.

### Pitfall 5: Bundle Upload Succeeds, Terragrunt Apply Fails
**What goes wrong:** S3 PutObject for `sandboxes/<id>/secrets.enc.yaml` succeeds, then `terragrunt apply` fails, leaving an orphan S3 object.
**Why it happens:** PutObject is pre-apply (no rollback wired). Other pre-apply uploads (userdata.sh, init script, profile.yaml) have the same race condition.
**How to avoid:** The S3 lifecycle rule with 7-day expiry handles this cleanup (CONTEXT.md decision). For belt-and-suspenders, `km destroy` deletes the bundle explicitly. Manual operator cleanup is also possible via the lifecycle.
**Warning signs:** Orphan objects under `sandboxes/` whose sandbox IDs don't exist in DDB. Doctor could check this in a future hardening pass.

### Pitfall 6: `km uninit` Must Not Schedule-Delete a Sibling Install's Key
**What goes wrong:** `km uninit` deletes `alias/km-sandbox-secrets` AND schedules the underlying key for deletion. A sibling install with `resource_prefix=km2` has its own `alias/km2-sandbox-secrets` pointing to the SAME key (if operators set things up incorrectly) and gets locked out.
**Why it happens:** v1 has one shared key per install; multiple installs in the same account own DIFFERENT keys (different alias names). The risk is only if an operator manually points two aliases at one key. The terraform module always creates BOTH key + alias together, so this is structurally prevented.
**How to avoid:** Document that `sandbox-secrets-key` always creates a fresh key per `resource_prefix`. `km uninit` only removes the alias matching the local `resource_prefix`. Schedule-delete on the underlying key (key ID) is safe because the terraform module owns that key 1:1 with the alias.
**Warning signs:** Doctor check shows orphan aliases pointing to a "still-being-deleted" key (KMS shows `PendingDeletion` state).

### Pitfall 7: Plan-Before-Apply Trips on First Bootstrap
**What goes wrong:** `km bootstrap --shared-secrets-key --plan` trips the destroy-class gate on the very first run because `aws_kms_key` is in `ProtectedTypes` (planreport/protected.go:34).
**Why it happens:** The gate fires on `destroy` or `replace` — creates do not trip it. The first `apply` only creates, so the gate doesn't fire. False alarm risk only if operators misread the trip message.
**How to avoid:** Verify the gate's wording is unambiguous about "destroy/replace" not "create" — current logic at planreport/protected.go:7 says "destroy or replace them" which is correct.
**Warning signs:** Operator confused by trip output on a first run; planner should add a "fresh install" smoke test to phase 89 UAT.

### Pitfall 8: Plugin Cache Lock Drift After New Module
**What goes wrong:** After shipping the new `sandbox-secrets-key/v1.0.0/` module, operators running `km init` hit provider-checksum errors.
**Why it happens:** Memory `project_plugin_cache_lock_drift` — plugin_cache_dir + gitignored locks cause this on operator Macs.
**How to avoid:** Document in 89-UAT that the operator should run `terragrunt run --all --queue-exclude-dir 'sandboxes/**' init -- -upgrade` once after merging this phase.
**Warning signs:** "Error: Failed to install provider — checksum list..." on the new module.

### Pitfall 9: Validation Must Not Require KMS Access
**What goes wrong:** `km validate profiles/codex.yaml` fails on CI/CD runners with no AWS creds because the validator tries to decrypt the SOPS bundle.
**Why it happens:** Conflating validation with verification.
**How to avoid:** SOPS-02-VALIDATION must only check (a) file exists, (b) `.enc.yaml` suffix, (c) the file's YAML contains a top-level `sops:` key (offline parse). NO `sops decrypt`, NO AWS SDK call in `km validate`.
**Warning signs:** Validate exit code depending on AWS creds; CI breaks when AWS_PROFILE isn't set.

### Pitfall 10: km init --sidecars Doesn't Pick Up Profile Schema Changes
**What goes wrong:** Operator ships Phase 89, runs `km init --sidecars` (fast deploy), creates a Codex sandbox via the management Lambda — and the Lambda rejects the new `spec.secrets` field because its embedded km binary is stale.
**Why it happens:** Memory `project_schema_change_requires_km_init` — schema additions need the create-handler Lambda's km binary refreshed. `--sidecars` only uploads sidecar binaries.
**How to avoid:** **Both** `km init` (full, deploys lambdas) AND `km init --sidecars` (sops binary upload) are required. Document this in 89-UAT and CLAUDE.md.
**Warning signs:** `km create --remote profiles/codex.yaml` fails with "unknown field secrets" from the create-handler Lambda.

## Code Examples

Verified patterns from the existing codebase (line numbers are anchors for the planner):

### Profile Schema Extension (pkg/profile/types.go)

```go
// Source: types.go:189-236 (ExecutionSpec precedent); inserted at top of Spec sibling block ~line 50
type SecretsSpec struct {
    // SopsFile is a path (relative to the profile YAML location) to a
    // SOPS-encrypted YAML bundle. The bundle's top-level keys become
    // environment variables in /etc/sandbox-secrets.env at boot.
    // Reserved keys "sops" and "_meta" are ignored (sops embeds metadata).
    // Empty (the zero value) means no secret injection — backwards compatible.
    SopsFile string `yaml:"sopsFile,omitempty" json:"sopsFile,omitempty"`
}

// Add to Spec struct after the existing optional fields (types.go:40-54):
//   Secrets *SecretsSpec `yaml:"secrets,omitempty"`
```

### Semantic Validator (pkg/profile/validate.go)

```go
// Source: validate.go:219-336 (existing semantic check shape); add to ValidateSemantic body
if p.Spec.Secrets != nil && p.Spec.Secrets.SopsFile != "" {
    if !strings.HasSuffix(p.Spec.Secrets.SopsFile, ".enc.yaml") {
        errs = append(errs, ValidationError{
            Path:    "spec.secrets.sopsFile",
            Message: fmt.Sprintf("must end with .enc.yaml (got %q) — anchors the .gitignore rule", p.Spec.Secrets.SopsFile),
        })
    }
    // Note: file-existence check (relative to profile location) is performed by the
    // CALLER who has the profile path. ValidateSemantic operates on the parsed struct
    // alone — file-system access is layered on at km validate / km create call sites.
}
```

### terragrunt.hcl Skeleton (infra/live/use1/sandbox-secrets-key/terragrunt.hcl)

```hcl
# Source: ses-shared-rule-set/terragrunt.hcl:1-43 (verified)
locals {
  repo_root  = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars  = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  aws_region    = local.region_config.locals.region_full
  resource_prefix = local.site_vars.locals.site.label
}

include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "${local.repo_root}/infra/modules/sandbox-secrets-key/v1.0.0"
}

inputs = {
  resource_prefix     = local.resource_prefix
  aws_region          = local.aws_region
  register_secrets_key = tobool(get_env("KM_REGISTER_SECRETS_KEY", "true"))

  tags = {
    "km:owner"     = "foundation"
    "km:phase"     = "89"
    "km:component" = "sandbox-secrets-key"
  }
}
```

### Terraform Module Skeleton (infra/modules/sandbox-secrets-key/v1.0.0/main.tf)

```hcl
# Source: ses-shared-rule-set/v1.0.0/main.tf:88-95 (prevent_destroy) +
#         infra/modules/secrets/v1.0.0/main.tf:37-56 (KMS key + alias pattern)
data "aws_caller_identity" "current" {}

resource "aws_kms_key" "secrets" {
  count                   = var.register_secrets_key ? 1 : 0
  description             = "${var.resource_prefix} sandbox secrets (SOPS) — Phase 89"
  deletion_window_in_days = 30
  enable_key_rotation     = true
  policy                  = data.aws_iam_policy_document.secrets_key_policy.json

  tags = merge(var.tags, {
    Name      = "${var.resource_prefix}-sandbox-secrets"
    Purpose   = "sops-bundle-decryption"
    ManagedBy = "Terragrunt"
  })

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_kms_alias" "secrets" {
  count         = var.register_secrets_key ? 1 : 0
  name          = "alias/${var.resource_prefix}-sandbox-secrets"
  target_key_id = aws_kms_key.secrets[0].key_id
}

data "aws_iam_policy_document" "secrets_key_policy" {
  # Account root admin (standard pattern from secrets/v1.0.0)
  statement {
    sid    = "EnableAccountAdmin"
    effect = "Allow"
    principals {
      type        = "AWS"
      identifiers = ["arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"]
    }
    actions   = ["kms:*"]
    resources = ["*"]
  }

  # Sandbox instance profiles in this account can decrypt only via this alias.
  # ResourceAliases condition scopes by alias — cross-prefix isolation in v1.
  statement {
    sid    = "AllowSandboxDecrypt"
    effect = "Allow"
    principals {
      type        = "AWS"
      identifiers = ["arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"]
    }
    actions   = ["kms:Decrypt", "kms:DescribeKey"]
    resources = ["*"]
    condition {
      test     = "StringEquals"
      variable = "kms:ResourceAliases"
      values   = ["alias/${var.resource_prefix}-sandbox-secrets"]
    }
    # The principal scoping to actual sandbox role ARNs happens via the
    # sandbox-side IAM policy added in ec2spot/v1.2.0 — using account root here
    # keeps the key policy permissive enough that any future sandbox role in
    # this account can decrypt without a key-policy update per profile.
  }
}
```

### Sandbox IAM Statement (infra/modules/ec2spot/v1.2.0/main.tf — additive)

```hcl
# Source: ec2spot/v1.1.0/main.tf:387-432 (github-token pattern, verified)
resource "aws_iam_role_policy" "ec2spot_sandbox_secrets_kms" {
  count = local.total_ec2spot_count > 0 ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-sandbox-secrets-kms"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "KMSDecryptSandboxSecrets"
        Effect   = "Allow"
        Action   = ["kms:Decrypt", "kms:DescribeKey"]
        Resource = ["arn:aws:kms:*:${data.aws_caller_identity.current.account_id}:key/*"]
        Condition = {
          StringEquals = {
            "kms:ResourceAliases" = "alias/${var.resource_prefix}-sandbox-secrets"
          }
        }
      }
    ]
  })
}

# S3 read of own secrets bundle — scoped to per-sandbox prefix.
resource "aws_iam_role_policy" "ec2spot_sandbox_secrets_s3" {
  count = (local.total_ec2spot_count > 0 && var.artifacts_bucket != "") ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-sandbox-secrets-s3"
  role  = aws_iam_role.ec2spot_ssm[0].id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "S3GetOwnSecretsBundle"
        Effect   = "Allow"
        Action   = ["s3:GetObject"]
        Resource = "arn:aws:s3:::${var.artifacts_bucket}/sandboxes/${var.sandbox_id}/secrets.enc.yaml"
      }
    ]
  })
}
```

### Idempotent Gitignore Append (configure.go)

```go
// Source: configure.go:686-712 (write-file pattern); placed in km configure first-run flow
func ensureSecretsGitignore(repoRoot string) error {
    path := filepath.Join(repoRoot, ".gitignore")
    existing, _ := os.ReadFile(path)  // missing == empty == fine
    body := string(existing)

    needed := []string{"/secrets/*", "!/secrets/*.enc.yaml"}
    var toAppend []string
    for _, line := range needed {
        if !strings.Contains(body, line) {
            toAppend = append(toAppend, line)
        }
    }
    if len(toAppend) == 0 {
        return nil  // idempotent — already present
    }
    block := "\n# Phase 89: SOPS-encrypted secrets (km configure)\n" +
        strings.Join(toAppend, "\n") + "\n"
    f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil { return err }
    defer f.Close()
    _, err = f.WriteString(block)
    return err
}
```

### Destroy Hook (destroy.go ~line 540, after artifact upload + CloudWatch export)

```go
// Source: destroy.go S3 deletion patterns (e.g., DeleteSandboxMetadata at line 534)
{
    secretsKey := fmt.Sprintf("sandboxes/%s/secrets.enc.yaml", sandboxID)
    if _, delErr := s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
        Bucket: aws.String(artifactBucket),
        Key:    aws.String(secretsKey),
    }); delErr != nil {
        log.Warn().Err(delErr).Str("sandbox_id", sandboxID).
            Msg("delete sops bundle (non-fatal — S3 lifecycle will GC after 7 days)")
    } else {
        log.Info().Str("sandbox_id", sandboxID).Msg("sops bundle deleted from S3")
    }
}
```

### S3 Lifecycle Additive Rule (infra/modules/s3-artifacts-lifecycle/v1.1.0/main.tf)

```hcl
# Source: s3-artifacts-lifecycle/v1.0.0/main.tf:1-19 (extend, don't replace)
resource "aws_s3_bucket_lifecycle_configuration" "artifacts" {
  bucket = var.bucket_name

  rule {
    id     = "slack-inbound-30day"
    status = "Enabled"
    filter { prefix = "slack-inbound/" }
    expiration { days = 30 }
  }

  rule {
    id     = "sandbox-secrets-7day"
    status = "Enabled"
    filter { prefix = "sandboxes/" }
    expiration { days = 7 }
  }
}
```

### sops Binary Upload in km init --sidecars (init.go ~line 1916)

```go
// Source: init.go:1881-1916 (fetchAndUploadOtelcolContrib pattern, verified)
const sopsVersion = "3.13.1"

func fetchAndUploadSops(buildDir, bucket string) error {
    binaryPath := filepath.Join(buildDir, "sops")
    if _, err := os.Stat(binaryPath); err == nil {
        fmt.Printf("  sops already in build/ (skip download)\n")
    } else {
        url := fmt.Sprintf("https://github.com/getsops/sops/releases/download/v%s/sops-v%s.linux.amd64", sopsVersion, sopsVersion)
        fmt.Printf("  Downloading sops v%s...\n", sopsVersion)
        dlCmd := exec.Command("bash", "-c",
            fmt.Sprintf("curl -fsSL %q -o %q", url, binaryPath))
        if out, err := dlCmd.CombinedOutput(); err != nil {
            return fmt.Errorf("download sops: %s: %w", string(out), err)
        }
        if err := os.Chmod(binaryPath, 0o755); err != nil {
            return fmt.Errorf("chmod sops: %w", err)
        }
    }
    s3Key := "binaries/sops"
    fmt.Printf("  Uploading sops to s3://%s/%s...\n", bucket, s3Key)
    uploadCmd := exec.Command("aws", "s3", "cp", binaryPath,
        fmt.Sprintf("s3://%s/%s", bucket, s3Key),
        "--profile", "klanker-terraform")
    if out, err := uploadCmd.CombinedOutput(); err != nil {
        return fmt.Errorf("upload sops: %s: %w", string(out), err)
    }
    fmt.Printf("  Uploaded sops\n")
    return nil
}
// Call from buildAndUploadSidecars after fetchAndUploadOtelcolContrib (init.go:1869).
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| SSM SecureString + out-of-band injection at create time | SOPS bundle + boot-time decrypt | Phase 89 (this) | Removes manual operator step; Phase 88 Codex sandboxes "just work" |
| `sops -d file.yaml` (legacy syntax) | `sops decrypt file.yaml` + `--output-type dotenv` | sops v3.x (already current) | Cleaner; better dotenv path; native env-var output |
| `aws s3 cp` from public artifacts bucket | Same path via instance-profile IAM scoped to `sandboxes/{id}/*` | Existing precedent (Phase 68 transcripts) | Per-sandbox isolation |
| Per-sandbox KMS keys (`infra/modules/secrets/v1.0.0/`) | Shared per-install key (`infra/modules/sandbox-secrets-key/v1.0.0/`) | Phase 89 v1 | Single key reduces blast radius management; v2 will move to per-sandbox via principal tags |

**Deprecated/outdated:**
- The "operator decrypts and injects via SSM SendCommand" workflow (Phase 88 UAT) — superseded by this phase
- Storing `OPENAI_API_KEY: ""` placeholder in profiles (codex.yaml line 37) — replaced by `spec.secrets.sopsFile`

## Open Questions

1. **Does the `aws_kms_key` policy need to enumerate sandbox role ARNs?**
   - What we know: The shared key policy currently uses `account root` as principal + `kms:ResourceAliases` condition. Each sandbox role then has its own inline policy with the SAME condition.
   - What's unclear: Whether AWS evaluates the condition correctly when both key policy and IAM policy specify it — or whether the key policy needs to enumerate per-sandbox role patterns.
   - Recommendation: Use account-root principal in key policy (broad permit) + tight IAM-side condition. This is the same shape the existing per-sandbox `infra/modules/secrets/v1.0.0/` uses (lines 60-86) and is verified to work.

2. **Does `sops decrypt --output-type dotenv` quote values containing spaces?**
   - What we know: Official docs verify the `--output-type dotenv` flag exists and is documented.
   - What's unclear: Exact quoting behavior for values with `"`, `\`, spaces, or `=`.
   - Recommendation: Phase 89 plan should include a Wave-0 test fixture (`pkg/compiler/testdata/secrets-fixture.enc.yaml`) with multi-token values and verify the decrypted env file is shell-parseable via `bash -n` and value equality.

3. **Should `km validate` warn (not error) on missing `secrets/` directory?**
   - What we know: Validation is offline. Profile path is known at validate time.
   - What's unclear: Whether absence of the referenced file should be hard error or warning (an operator might `git clone` without `secrets/` having been provided).
   - Recommendation: Hard error — referenced file MUST exist at validate time. The operator's git workflow should ensure encrypted bundles are committed (via the `!/secrets/*.enc.yaml` exception in `.gitignore`).

4. **Module version: bump ec2spot to v1.2.0 or add to v1.1.0?**
   - What we know: ec2spot v1.0.0 and v1.1.0 both exist. The github-token policy was added in one of them.
   - What's unclear: Whether additive policy additions warrant a fresh `v1.2.0` directory or whether modifications-in-place are tolerated.
   - Recommendation: Bump to v1.2.0. Phase 84.4 lesson is clear: module version bumps for any additive change. Phase 89 plan should reserve the directory `infra/modules/ec2spot/v1.2.0/`.

5. **Is the `sops` binary download in `km init --sidecars` going to be slow for first-time operators?**
   - What we know: Binary is ~25MB (sops is Go-based, statically linked).
   - What's unclear: Whether the download from GitHub releases is reliable in CI/CD or operator workstations behind corporate proxies.
   - Recommendation: Cache to `build/sops` (already done in pattern); accept the one-time cost. Document the binary URL in `OPERATOR-GUIDE.md` for air-gapped operators who need to side-load.

6. **Should ECS substrate also be supported in v1?**
   - What we know: CONTEXT.md and ROADMAP focus on EC2 (the Codex use case). The ec2spot module is named.
   - What's unclear: Whether ECS Fargate tasks should also get the IAM policy + userdata equivalent (Fargate doesn't have user-data — would need to be in the container image or task definition env).
   - Recommendation: v1 scope is EC2 (matches Phase 88 acceptance — Codex sandboxes are EC2). Defer ECS to v2 alongside per-profile key isolation. Document this in the OPERATOR-GUIDE.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (`go test`) + `pytest`-style table-driven Go tests; UAT via `make test` and bash scripts |
| Config file | Per-package `*_test.go`; module-level wired via `go test ./...` |
| Quick run command | `go test ./pkg/profile/... ./internal/app/cmd/... ./pkg/compiler/...` |
| Full suite command | `make test` (runs `go test ./...`) + manual UAT scripts in `89-N-UAT.md` files |
| Phase gate | `make test` green + Phase-88-closure UAT (Codex sandbox accrues `BUDGET#ai#gpt-*` with sops-injected `OPENAI_API_KEY`) |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|--------------|
| SOPS-01-SCHEMA | `spec.secrets.sopsFile` parses, defaults to empty | unit | `go test ./pkg/profile/ -run TestSecretsSpecParse` | ❌ Wave 0 — new `pkg/profile/secrets_test.go` |
| SOPS-02-VALIDATION | Reject missing `.enc.yaml` suffix; allow empty; absence of `sops:` block in YAML errors | unit | `go test ./pkg/profile/ -run TestValidateSemanticSecrets` | ❌ Wave 0 — extends `pkg/profile/validate_test.go` |
| SOPS-03-KMS-MODULE | Module compiles via `terraform validate` (no apply) | smoke | `cd infra/modules/sandbox-secrets-key/v1.0.0 && terraform init -backend=false && terraform validate` | ❌ Wave 0 — new module |
| SOPS-04-MODULE-WIRING | terragrunt.hcl resolves locals + applies on dry-run | smoke | `cd infra/live/use1/sandbox-secrets-key && terragrunt plan` | ❌ Wave 0 — new terragrunt.hcl |
| SOPS-05-BOOTSTRAP-FLAG | `km bootstrap --shared-secrets-key --dry-run` prints expected output | unit (table) | `go test ./internal/app/cmd/ -run TestRunBootstrapSharedSecretsKeyDryRun` | ❌ Wave 0 — extends `bootstrap_test.go` |
| SOPS-06-BOOTSTRAP-PLAN | `km bootstrap --shared-secrets-key --plan` evaluates destroy-class gate | unit | `go test ./internal/app/cmd/ -run TestRunBootstrapSharedSecretsKeyPlan` | ❌ Wave 0 — new test, mirrors `bootstrap_plan_test.go` |
| SOPS-07-BOOTSTRAP-ALL-CHAIN | `--all` runs both subflows in order; `--all` + `--shared-secrets-key` mutex errors | unit | `go test ./internal/app/cmd/ -run TestRunBootstrapAllChain_Phase89` | ❌ Wave 0 — extends `bootstrap_test.go` |
| SOPS-08-IAM-OPERATOR | No-op — operator IAM already grants `kms:*` | manual | `grep 'kms:\*' infra/modules/km-operator-policy/v1.0.0/main.tf` | ✅ verified at line 484 |
| SOPS-09-IAM-SANDBOX | New ec2spot v1.2.0 emits the two IAM policies with correct `kms:ResourceAliases` condition | unit | `go test ./pkg/compiler/ -run TestEC2SpotSecretsIAMEmission` (or terraform plan-show snapshot) | ❌ Wave 0 — new test |
| SOPS-10-SCHEMA-EXPORT | JSON Schema accepts valid `spec.secrets` and rejects bad types | unit | `go test ./pkg/profile/ -run TestSchemaSecretsSpec` | ❌ Wave 0 |
| SOPS-11-COMPILER-UPLOAD | create.go uploads bundle to expected S3 key; respects empty `SopsFile` | unit + integration | `go test ./internal/app/cmd/ -run TestCreatePutSopsBundle` (with mock S3 client) | ❌ Wave 0 |
| SOPS-12-USERDATA-FETCH | userdata template emits sops binary + bundle fetch block iff `SopsBundlePresent` | unit | `go test ./pkg/compiler/ -run TestUserdataSopsBlock` | ❌ Wave 0 — extends `userdata_test.go` |
| SOPS-13-USERDATA-DECRYPT | Decrypt block uses `sops decrypt --output-type dotenv`, hard-fails on error | unit (template snapshot) | `go test ./pkg/compiler/ -run TestUserdataSopsDecryptShape` | ❌ Wave 0 |
| SOPS-14-USERDATA-ENV-EXPOSURE | `/etc/profile.d/zz-sandbox-secrets.sh` uses `set -a`/`. file`/`set +a`; mode 0644 | unit (template snapshot) | `go test ./pkg/compiler/ -run TestUserdataSopsProfileD` | ❌ Wave 0 |
| SOPS-15-BOOT-FAIL-ABORT | Decrypt failure path in userdata template emits `exit 1` | unit (template snapshot grep) | `go test ./pkg/compiler/ -run TestUserdataSopsFailAbort` | ❌ Wave 0 |
| SOPS-16-DESTROY-CLEANUP | destroy.go calls `DeleteObject` on the bundle S3 key | unit | `go test ./internal/app/cmd/ -run TestDestroyDeletesSopsBundle` (mock S3) | ❌ Wave 0 |
| SOPS-17-S3-LIFECYCLE | s3-artifacts-lifecycle v1.1.0 emits both rules | smoke (terraform validate + show -json) | `cd infra/modules/s3-artifacts-lifecycle/v1.1.0 && terraform validate` | ❌ Wave 0 |
| SOPS-18-DOCTOR-CHECK | `checkSharedSecretsKey` returns OK / WARN(missing) / WARN(orphans) via mocked KMS | unit (table) | `go test ./internal/app/cmd/ -run TestCheckSharedSecretsKey` | ❌ Wave 0 — new `doctor_secrets_test.go` |
| SOPS-19-CONFIGURE-GITIGNORE | First call appends; second call is no-op; existing partial content is respected | unit | `go test ./internal/app/cmd/ -run TestEnsureSecretsGitignore` | ❌ Wave 0 |
| SOPS-20-SIDECARS-SOPS-DEPLOY | `buildAndUploadSidecars` includes sops download + upload | manual-only | `km init --sidecars` against test bucket; verify `aws s3 ls s3://${bucket}/binaries/sops` | ❌ Wave 0 — UAT script |
| SOPS-21-UNINIT-CLEANUP | `km uninit` only deletes own-prefix alias + scheduled-deletes own key | manual-only | UAT scenario in `89-N-UAT.md` (requires real AWS account with sibling install) | ❌ Wave 2 |
| SOPS-22-DOCS | `docs/sandbox-secrets.md` exists; CLAUDE.md "Where to look" mentions it | smoke | `test -f docs/sandbox-secrets.md && grep -q 'sandbox-secrets' CLAUDE.md` | ❌ Wave 1 |
| SOPS-23-UAT-ACCEPTANCE | Live: Codex sandbox accrues `BUDGET#ai#gpt-*` via sops-injected `OPENAI_API_KEY` | manual-only (UAT) | Documented script in `89-N-UAT.md`; mirrors Phase 88 plan 07 UAT | ❌ Wave 2 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/profile/... ./pkg/compiler/... ./internal/app/cmd/...` (~30s on this codebase)
- **Per wave merge:** `make test` (full suite)
- **Phase gate:** Full suite green + UAT (`89-N-UAT.md` scenarios) before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/profile/secrets_test.go` — covers SOPS-01, SOPS-02
- [ ] `pkg/profile/schemas/sandbox_profile.schema.json` — add `spec.secrets` object schema (SOPS-10)
- [ ] `internal/app/cmd/bootstrap_secrets_test.go` — covers SOPS-05, SOPS-06, SOPS-07
- [ ] `internal/app/cmd/create_secrets_test.go` — covers SOPS-11
- [ ] `internal/app/cmd/destroy_secrets_test.go` — covers SOPS-16
- [ ] `internal/app/cmd/doctor_secrets_test.go` — covers SOPS-18
- [ ] `internal/app/cmd/configure_secrets_test.go` — covers SOPS-19
- [ ] `pkg/compiler/userdata_secrets_test.go` — covers SOPS-12, SOPS-13, SOPS-14, SOPS-15
- [ ] `pkg/compiler/testdata/secrets-fixture.enc.yaml` — sample SOPS-encrypted bundle for tests (use age key, not KMS, to keep tests offline)
- [ ] `infra/modules/sandbox-secrets-key/v1.0.0/{main,variables,outputs}.tf` (SOPS-03)
- [ ] `infra/live/use1/sandbox-secrets-key/terragrunt.hcl` (SOPS-04)
- [ ] `infra/modules/s3-artifacts-lifecycle/v1.1.0/main.tf` (SOPS-17)
- [ ] `infra/modules/ec2spot/v1.2.0/main.tf` — additive policies (SOPS-09)
- [ ] `docs/sandbox-secrets.md` (SOPS-22)

**Test fixture strategy (critical):** Wave-0 tests must avoid real KMS calls. Two viable approaches:
1. **Use age encryption** for SOPS fixtures (`sops --age <pubkey> -e bundle.yaml`) — sops natively supports age and decrypts offline if `SOPS_AGE_KEY` env var is set in CI/CD. This lets unit tests round-trip without AWS.
2. **Mock the decrypt step entirely** by injecting a fake `sops` binary in CI/CD that emits pre-known dotenv output. Brittle but simpler.

Recommendation: **age-based fixtures**. Decrypt-path tests should exercise the real sops binary in CI. The KMS path is exercised in Wave 2 UAT only.

## Sources

### Primary (HIGH confidence)
- **Codebase verified line-by-line:**
  - `internal/app/cmd/bootstrap.go` (488-639, 880-1051) — `runBootstrapSharedSES` + `runBootstrapAll` patterns
  - `internal/app/cmd/create.go` (676-694, 882-895, 928-940, 2080-2102) — PutObject sequence
  - `internal/app/cmd/destroy.go` (380-540) — destroy flow
  - `internal/app/cmd/doctor.go` (1196-1290, 3480-3552) — KMS ListAliases + SES orphan-WARN
  - `internal/app/cmd/init.go` (1770-1916) — sidecar binary upload + otelcol-contrib precedent
  - `internal/app/cmd/configure.go` (680-720) — first-run write patterns
  - `pkg/compiler/userdata.go` (100-300, 960-1010, 3512-3643) — userdata template structure + sidecar download
  - `pkg/profile/types.go` (29-236) — Spec / ExecutionSpec / CLISpec
  - `pkg/profile/validate.go` (16-336) — ValidationError + ValidateSemantic
  - `pkg/profile/schema.go` (1-60) — JSON Schema embed
  - `pkg/terragrunt/planreport/protected.go` (1-36) — ProtectedTypes confirms `aws_kms_key` is gated
  - `infra/modules/ses-shared-rule-set/v1.0.0/` — module skeleton template
  - `infra/modules/secrets/v1.0.0/main.tf` — KMS key + alias + policy doc shape
  - `infra/modules/km-operator-policy/v1.0.0/main.tf` (474-489) — operator already has `kms:*`
  - `infra/modules/ec2spot/v1.1.0/main.tf` (380-432) — sandbox IAM KMS Condition pattern
  - `infra/modules/s3-artifacts-lifecycle/v1.0.0/main.tf` — additive rule shape
  - `infra/live/use1/ses-shared-rule-set/terragrunt.hcl` — live wiring template
  - `profiles/codex.yaml` (35-37) — current `OPENAI_API_KEY: ""` placeholder being replaced
  - `.planning/REQUIREMENTS.md` — NETW-07 (SOPS encrypts secrets at rest with KMS) already marked Complete via Phase 2
  - `.planning/STATE.md` lines 1351-1358 — Phase 89 motivation context

- **Project memory (verified HIGH):**
  - `project_terragrunt_env_export` — ExportTerragruntEnvVars required in any terragrunt-invoking command
  - `project_terragrunt_providers_in_root` — No `required_providers` in module HCL
  - `project_terragrunt_show_needs_mocks` — `"show"` must be in mock_outputs_allowed_terraform_commands for dep blocks (not applicable here — sandbox-secrets-key has no deps, mirroring ses-shared-rule-set)
  - `project_schema_change_requires_km_init` — Schema additions need full `km init`, not just `--sidecars`
  - `project_km_init_lambdas_doesnt_deploy` — `--lambdas` doesn't deploy; full `km init` required
  - `project_plugin_cache_lock_drift` — `init -upgrade` sweep recommended after new module
  - `project_phase_70_followups` — module additions require `km init --sidecars` to deploy

### Secondary (MEDIUM confidence)
- **sops latest stable release v3.13.1** (May 16 2026) — verified via WebFetch of github.com/getsops/sops/releases
- **sops `--output-type dotenv` flag** — verified via WebFetch of getsops.io/docs/
- **sops AWS SDK chain auth** — verified via WebFetch of getsops.io/docs/ (uses `~/.aws/credentials` or env vars or instance profile transparently)
- **sops single static binary distribution** — verified via WebFetch (linux amd64 + arm64 binaries on releases page)

### Tertiary (LOW confidence)
- **`sops --output-type dotenv` quoting behavior** for values with spaces / `=` / `"` — NOT verified by official docs. Flagged in Open Questions #2; recommend Wave-0 fixture test.
- **`kms:ResourceAliases` condition timing** — known AWS pitfall (alias must be attached before first decrypt). Recommended belt-and-suspenders with alias-form ARN in Resource. Worth confirming in Wave 2 UAT.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — every library/asset is either already-imported in this codebase or a verified-current external (sops v3.13.1)
- Architecture: HIGH — every pattern has a line-anchored precedent (SES bootstrap, KMS in github-token, S3 lifecycle additive, doctor orphan-WARN)
- Pitfalls: HIGH on items 1, 4, 5, 6, 7, 8, 10 (codebase-anchored) and MEDIUM on items 2, 9 (dotenv format edges + validation gating; verified by docs but warrant explicit Wave-0 fixtures)
- Synthetic requirement IDs: HIGH — modeled on Phase 84.2/84.3 IDs already in REQUIREMENTS.md

**Research date:** 2026-05-26
**Valid until:** 2026-06-25 (30 days — code references are line-anchored; sops version may bump but patch-level upgrades don't change the contract)

## RESEARCH COMPLETE
