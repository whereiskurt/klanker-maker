# Phase 80: km cluster — cross-account IRSA for k8s integrations — Context

**Gathered:** 2026-05-11
**Status:** Ready for planning
**Source:** PRD Express Path (`docs/superpowers/specs/2026-05-11-km-cluster-cross-account-irsa-design.md`)

<domain>
## Phase Boundary

**In scope:**

1. **New shared Terraform module:** `infra/modules/km-operator-policy/v1.0.0/` extracted from `infra/modules/create-handler/v1.0.0/main.tf:28-528`. Contains the 14 inline IAM policy statements that every `km`-running role needs.
2. **Refactor `infra/modules/create-handler/v1.0.0/`** to consume the shared module. Net IAM JSON diff must be zero (verified via `terragrunt plan`).
3. **New Terraform module:** `infra/modules/cluster-irsa/v1.0.0/` with cross-account trust policy + consumption of the shared policy module.
4. **New CLI:** `km cluster add | list | rm` (Cobra subcommand at `internal/app/cmd/cluster.go`).
5. **Config schema:** New `ClusterConfig` struct + `Clusters []ClusterConfig` field in `internal/app/config/config.go`. Persisted to `km-config.yaml` under `clusters:` list.
6. **Runtime artifact generation:** `km cluster add` writes `infra/live/{region-label}/cluster-{name}/terragrunt.hcl` from a template, runs `terragrunt apply --auto-approve`, captures role ARN output, persists to config.
7. **Phase-close integration test:** Full `km cluster add --dry-run=false` against `klanker-application` profile → verify role in IAM → `km cluster list` shows it → `km cluster rm` destroys + un-persists.
8. **Docs:** Add `## Cross-account k8s integrations` section to CLAUDE.md mirroring the Phase 73/79 doc pattern.

**Out of scope (explicit deferral):**

- `km doctor` checks (`cluster_irsa_trust_healthy`, `cluster_irsa_stale_roles`). Deferred to a follow-up phase.
- End-to-end pod-side validation (assume-role from a real k8s pod). Operator will do that when deploying to the remote account; phase 80 just verifies the role + trust policy + IAM JSON.
- Cross-account org SCP allowances. The klanker account is already permitted to trust external OIDC providers; no SCP changes needed for v1.
- Multiple operators per cluster (multiple service accounts under one role). v1 ships single `--service-account` per `km cluster add`.
- Terraform module versioning workflow for `km-operator-policy` (we ship v1.0.0; bumping to v1.1.0 when policies change is a future concern).
- ServiceAccount manifest deployment helper (e.g., `km cluster manifest > sa.yaml`). v1 prints the YAML inline in the success output.

</domain>

<decisions>
## Implementation Decisions

### Phase decomposition (LOCKED 2026-05-11)
- **Single phase 80.** No 80/81 split. Module extract + new module + CLI + config + integration test all ship together.
- **Reasoning:** The seam between "scaffold + dry-run" and "apply path" is artificial — they're the same code path with one boolean flag.

### Policy module strategy (LOCKED 2026-05-11)
- **Extract `km-operator-policy/v1.0.0/` as a shared module.** Do NOT duplicate create-handler's policy set into cluster-irsa.
- Both `create-handler` and `cluster-irsa` consume the shared module.
- **Refactor of `create-handler` must produce zero net IAM JSON change** — verified via `terragrunt plan` in `infra/live/use1/create-handler/` showing no `aws_iam_role_policy` replacements/destroys.
- **Reasoning:** Avoids documented drift risk between Lambda role and IRSA role; eliminates the "duplicate now, extract later" follow-up.

### Test target before phase close (LOCKED 2026-05-11)
- Full `km cluster add --dry-run=false` against `klanker-application` profile.
- Sequence: dry-run plan → apply → `aws iam get-role` proves role exists → `km cluster list` shows it → `km cluster rm --dry-run=false` destroys → role gone from IAM + entry gone from `km-config.yaml`.
- No k8s pod assume-role test required at phase close (operator does that on remote deploy).

### Trust policy details
- `Principal.Federated` references the OIDC provider ARN in the *cluster's* AWS account (NOT klanker's `data.aws_caller_identity`). Pass `oidc_provider_arn` as an explicit Terraform input.
- Strip `arn:aws:iam::<account>:oidc-provider/` prefix to derive the condition variable key (e.g. `oidc.eks.us-east-1.amazonaws.com/id/XXXX`).
- `aud` condition: always `StringEquals "sts.amazonaws.com"`.
- `sub` condition: `StringLike` when `--namespace` or `--service-account` contains `*`, else `StringEquals`. Mirrors Greenhouse `aws_irsa_role` module pattern.
- `--namespace` default: `*` (wildcard — any namespace can assume, scoped only by service account name).
- `--service-account` default: `km`.

### CLI flags on `km cluster add`
| Flag | Default | Required |
|---|---|---|
| `--name` | (none) | yes |
| `--oidc-provider-arn` | (none) | yes |
| `--namespace` | `*` | no |
| `--service-account` | `km` | no |
| `--aws-profile` | `klanker-application` | no |
| `--region` | `us-east-1` | no |
| `--verbose` | `false` | no |
| `--dry-run` | `true` | no |

`--dry-run=true` runs `terragrunt plan` only; `--dry-run=false` runs `terragrunt apply --auto-approve`.

### CLI behavior contracts
- **Idempotency:** if a cluster with the same `--name` exists in `km-config.yaml`, print the existing role ARN and exit 0 without re-applying.
- **Pre-flight validation:** call `sts.GetCallerIdentity` before any Terragrunt invocation. Fail fast on wrong-account or expired creds (pattern from `cmd/init.go:310-316`).
- **AWS profile resolution:** must call `ExportConfigEnvVars(cfg)` before invoking `terragrunt` (memory: `project_terragrunt_env_export.md` — non-default `resource_prefix` installs hit 403 HeadBucket otherwise).
- **Output capture:** after apply, run `terragrunt output -raw role_arn` to get the ARN. Persist to `km-config.yaml` via the same `persistKMConfigFields` pattern at `cmd/init.go:1719-1755`.
- **Rollback on persistence failure:** if `terragrunt apply` succeeds but `km-config.yaml` write fails, leave the IAM role in place and emit a clear error pointing the operator at `km cluster rm` cleanup. Do NOT attempt to undo the apply automatically.

### Config schema
```go
type ClusterConfig struct {
    Name             string `mapstructure:"name"               yaml:"name"`
    OIDCProviderARN  string `mapstructure:"oidc_provider_arn"  yaml:"oidc_provider_arn"`
    Namespace        string `mapstructure:"namespace"          yaml:"namespace"`
    ServiceAccount   string `mapstructure:"service_account"    yaml:"service_account"`
    RoleARN          string `mapstructure:"role_arn"           yaml:"role_arn"`
}
// Config gains: Clusters []ClusterConfig `mapstructure:"clusters" yaml:"clusters"`
```
Absent `clusters:` key = empty slice; no migration needed for existing installs.

### IAM role naming
- Role name: `{resource_prefix}-cluster-{cluster_name}` (e.g. `km-cluster-dev-use1-0`).
- Terraform state key: `${tf_state_prefix}/${region_label}/cluster-{cluster_name}/terraform.tfstate`.
- Terragrunt stack directory: `infra/live/{region-label}/cluster-{name}/`.

### Handoff output
On successful apply, `km cluster add` prints a ready-to-paste ServiceAccount YAML manifest (with `eks.amazonaws.com/role-arn` + `eks.amazonaws.com/token-expiration: "3600"` annotations) AND the four-item bullet list from the spec's "What the service team needs" section.

### Claude's Discretion
- **Internal helper organization:** how `internal/app/cmd/cluster.go` splits into helpers vs. inline logic — copy the structure of `internal/app/cmd/init.go` for consistency.
- **Test layout:** mirror existing `cmd/*_test.go` conventions (table-driven, `t.Run` subtests).
- **Error message phrasing:** follow the tone of existing `km` errors (no emoji, concrete next-action hint).
- **Terragrunt template embedding:** `embed.FS` vs. raw string constant — pick whichever matches the existing terragrunt-template pattern in the codebase. Check `pkg/compiler/` and `internal/app/cmd/init.go` for prior art.
- **Pre-existing data sources in trust policy:** the spec uses `data "aws_caller_identity" "current"` even though the trust policy doesn't reference it. Drop unused data blocks unless they're needed for the role ARN output.
- **Region label derivation:** use `compiler.RegionLabel()` if it exists; otherwise pick the simplest equivalent (likely a small util in `pkg/compiler/`).
- **`km cluster list` output format:** table layout follows the spec; column widths are operator's preference, lean on `text/tabwriter` if used elsewhere.
- **Refactor mechanism for `create-handler`:** whether the shared module exposes `policy_documents` (map of JSON strings keyed by name, consumed via `aws_iam_role_policy.foo { policy = module.shared.policy_documents["s3_artifacts"] }`) or generates the `aws_iam_role_policy` resources internally (with role_id passed in). Choose whichever keeps the `terragrunt plan` diff cleanest — test both with a quick spike if needed.

</decisions>

<specifics>
## Specific Ideas

- **OIDC provider host extraction:** Terraform `replace(var.oidc_provider_arn, "/^arn:aws:iam::[0-9]+:oidc-provider\\//", "")` — gives `oidc.eks.us-east-1.amazonaws.com/id/XXXX` for use as the condition variable key.
- **Reference files for patterns to follow:**
  - `infra/modules/create-handler/v1.0.0/main.tf:28-528` — authoritative source of the policy set to extract.
  - `infra/live/use1/create-handler/terragrunt.hcl` — template structure for the per-cluster terragrunt.hcl.
  - `internal/app/cmd/init.go:310-316` — pre-flight `sts.GetCallerIdentity` pattern.
  - `internal/app/cmd/init.go:1719-1755` — `persistKMConfigFields` pattern.
  - `internal/app/cmd/init.go` — overall command structure including terragrunt runner usage.
  - `pkg/aws/client.go:23-40` — `LoadAWSConfig` profile-vs-environment fallback.
- **Wildcard trust condition logic:** mirror `infrastructure/terraform/modules/aws_irsa_role_lotus/variables.tf:78-83` from the Greenhouse repo (referenced in spec).
- **Site-locals to consume:** `site_vars.locals.site.label` for `resource_prefix`, `site_vars.locals.backend.bucket` for `state_bucket`, `site_vars.locals.site.tf_state_prefix` for state key prefix — all already pulled from `infra/live/site.hcl` by existing modules.
- **Acceptance criteria for the IAM JSON diff check:**
  - Run `cd infra/live/use1/create-handler && terragrunt plan -detailed-exitcode`.
  - Expected: exit code 0 (no changes) OR all changes confined to `aws_iam_role_policy` *additions/removals* with byte-identical JSON content (a reorganization without semantic diff).
  - NOT acceptable: any `~ aws_iam_role.create_handler` replacement or any policy body diff.

</specifics>

<deferred>
## Deferred Ideas

- **`km doctor` checks** — `cluster_irsa_trust_healthy` (verifies the role's OIDC provider ARN still resolves), `cluster_irsa_stale_roles` (detects IAM roles tagged `km-cluster-*` with no corresponding `km-config.yaml` entry). Queued for a follow-up phase after first remote deploy proves the trust pattern works end-to-end.
- **`km cluster manifest` subcommand** — emits the ServiceAccount YAML to stdout for `kubectl apply -f -`. v1 prints it inline in the `add` output; promote to a subcommand if operators ask.
- **Multiple service-account/namespace pairs per role** — current trust policy `sub` is a single value. If operators need one role assumable by SA `foo` in `ns-a` AND SA `bar` in `ns-b`, the condition value becomes a list. Defer until requested.
- **K8s pod-side smoke test in `km doctor`** — would require operator to run `km doctor` from within the cluster. Out of scope; phase-close test is operator-side IAM verification only.
- **Custom inline policy hooks** — letting an operator add extra inline policies to the IRSA role for cluster-specific needs (e.g., one cluster's `km` instance also needs `bedrock:InvokeModel`). Not requested; trivial to add as an `--inline-policy <file>` flag later.
- **Cross-region role provisioning** — `km cluster add` currently writes to `infra/live/{region-label}/cluster-{name}/`. Multi-region clusters would need either one role per region (current behavior, fine) or a single global role with multiple region stacks consuming it. v1 ships per-region; revisit if needed.
- **Multi-account cluster trust** — one role trusting OIDC providers from multiple AWS accounts. Trust policy `Principal.Federated` can be a list; defer until requested.

</deferred>

---

*Phase: 80-km-cluster-cross-account-irsa-for-k8s-integrations*
*Context gathered: 2026-05-11 via PRD Express Path*
