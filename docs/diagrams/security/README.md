# Security and infrastructure diagrams

Eleven self-contained HTML diagrams. Each is one file with inline SVG — no build
step, no external assets beyond a Google Fonts stylesheet. Open any of them
directly in a browser.

They are a visual companion to [`docs/security-model.md`](../../security-model.md),
[`docs/brokered-secrets.md`](../../brokered-secrets.md) and
[`docs/operational-gotchas.md`](../../operational-gotchas.md) — the prose stays
authoritative, these exist to make the shape of the thing legible in one look.

| # | Diagram | Answers |
|---|---|---|
| 1 | [Containment boundaries](01-containment-boundaries.html) | What sits between the agent and the account, and which of those rings a compromised box can erase |
| 2 | [Compensating layers](02-compensating-layers.html) | What each defensive layer stops — and, in the right-hand column, the escape it deliberately leaves open |
| 3 | [Brokered secret unsealing](03-secret-unsealing.html) | How a SOPS bundle reaches one process without ever landing on disk or in an environment |
| 4 | [Bridge Lambda trust boundary](04-bridge-trust-boundary.html) | Why a public, unauthenticated Function URL is safe, why the bridge has no route into the VPC, and the two HackerOne edges that cross the other way — the internal ack that `reply: none` removes, and the opt-in raw capture written before the HMAC check |
| 5 | [The IMDS fence](05-imds-fence-traces.html) | Two processes on one box asking for the same credential, rule by rule |
| 6 | [Terragrunt invocation path](06-terragrunt-invocation.html) | Who runs Terraform, from where, against which named bucket and lock table |
| 7 | [IaC file provenance](07-iac-provenance.html) | Which process authors every file an apply touches — and why `backend.tf` is not in the repo |
| 8 | [YAML → HCL compiler](08-yaml-to-hcl-compiler.html) | How the two YAML inputs travel separate routes, and what `compiler.Compile()` actually emits |
| 9 | [Bootstrap and init](09-bootstrap-and-init.html) | What each command applies, in which account, and the two resources that are not Terraform |
| 10 | [SSM parameters and KMS keys](10-ssm-and-kms-keys.html) | Every `/{prefix}/` parameter by branch and the command that writes it, the four KMS keys with their aliases and creators, and which key each SecureString is actually written under |
| 11 | [S3 buckets and presigned URLs](11-s3-buckets-and-presign.html) | The three buckets and which one a sandbox can reach, every prefix in the artifacts bucket with its writer and reader, what a presigned URL actually carries, and why the region-lock `Allow "*"` — not the scoped S3 statements — is what AWS evaluates |

## YAML → HCL, in prose

Diagram 8 is the picture; these are the details that belong in text rather than
on a canvas.

### The two inputs never meet

`km-config.yaml` and the SandboxProfile look like peers and are not. Only the
profile is compiled.

| Input | Route | Ends up as |
|---|---|---|
| `km-config.yaml` | `config.Load()` → `ExportTerragruntEnvVars()` | `KM_*` process env, read by `site.hcl` via `get_env()`. Never touches the compiler. |
| `profiles/<name>.yaml` | `profile.Resolve()` → `ValidateSchema` + `ValidateSemantic` → `compiler.Compile()` | `service.hcl` (a `locals` block) and `user-data.sh` |

### What `extends:` does, exactly

`profile.Resolve(name, searchPaths)` walks the `extends` DAG left to right, child
applied last, and `deepMerge`s each result (`pkg/profile/inherit.go`). Diamonds
are memoised per resolved path, cycles are rejected on an ancestry check, and the
chain is capped at 10 hops.

| Value shape | Merge rule |
|---|---|
| map | recurse, key-union — both sides survive at every depth |
| scalar | right-hand side wins, so the child always wins |
| list | `concatDedup` — concatenate, drop duplicates, keep first occurrence and order |
| object list | same, compared with `reflect.DeepEqual` (this is what de-dups `additionalSnapshots`) |

Two consequences worth knowing before you author a fragment:

- **A child cannot narrow a base's list.** Lists union, so a leaf that wants a
  tighter `allowedDNSSuffixes` than its base must compose from a narrower base
  or keep the field in-leaf. `execution.initCommandsAppend` exists precisely
  because `initCommands` itself always unions.
- **Non-pointer bools push their zero value.** A fragment that writes a whole
  block with plain `bool` fields sends `false` down to every child. Keep
  mixed-bool blocks like `spec.runtime` in the leaf.

### What `compiler.Compile()` emits

One `CompiledArtifacts` struct. It contains no Terraform — no `resource`, no
`module`, no `provider`. The parts that reach disk:

| Field | Written to | Note |
|---|---|---|
| `ServiceHCL` | `infra/live/<region>/sandboxes/<id>/service.hcl` | a `locals` block; `module_inputs` carries every Terraform variable |
| `UserData` | `.../user-data.sh` | base64-embedded into `service.hcl` as `user_data_base64` |
| `FullUserData` | `s3://<artifacts>/artifacts/<id>/km-userdata.sh` | only when the script exceeds the 16KB EC2 limit — `UserData` becomes a fetch stub |
| `BudgetEnforcerHCL` | `.../budget-enforcer/terragrunt.hcl` | only when the profile declares a budget |
| `GitHubTokenHCL` | `.../github-token/terragrunt.hcl` | only when `sourceAccess.github` is set |

The unit's own `terragrunt.hcl` is **copied byte-for-byte** from
`infra/templates/sandbox/` by `terragrunt.CreateSandboxDir` — the compiler never
writes it. That template is what carries the module version pin
(`locals.substrate_module_versions`), looked up by the `substrate_module` key the
compiler put in `service.hcl`.

### The S3 artifact set

A remote (`--remote`) create needs everything the create-handler Lambda cannot
derive, so `km create` uploads it under `artifacts/<sandbox-id>/`:

| Key | What it is |
|---|---|
| `.km-profile.yaml` | the **resolved** profile — flattened, with `extends` already applied, because the Lambda cannot resolve `profiles/base/**` |
| `km-userdata.sh` | the full boot script, when it exceeded the inline limit |
| `km-init.sh` | the profile's `initCommands`, fetched by the box at boot |

The same resolved profile is also dumped on the box at
`/opt/km/.km-profile.yaml`, which is what makes the `klanker:sandbox`
self-census possible.

## The HCL for bootstrap and init

Diagram 9 is the order. This is the wiring.

### `km bootstrap` — three things, only one of them Terraform

| What | How | Where |
|---|---|---|
| SCP `{prefix}-sandbox-containment` | `terragrunt apply` | `infra/live/management/scp` → `infra/modules/scp/v2.0.0` |
| KMS key `alias/{prefix}-platform` | `kms.CreateKey` + `CreateAlias`, direct SDK | no unit, no state |
| S3 `{prefix}-artifacts-{account-id}` | `s3.CreateBucket` + `PutBucketVersioning`, direct SDK | no unit, no state |

The last two are a chicken-and-egg answer, not an oversight — the artifacts bucket
is where Lambda zips and the toolchain live, so it has to exist before anything
can be uploaded to it, and the KMS key encrypts the SSM parameters the rest of
the platform writes. They are created imperatively and idempotently
(`HeadBucket` / `DescribeKey` first, create only on a miss).

The consequence worth knowing: **no `terragrunt plan` will ever show them, and
`km uninit` will not remove them.** `km unbootstrap` is the command that does.

`km bootstrap --shared-ses` and `--shared-secrets-key` are separate, opt-in
subcommands applying one unit each — `infra/live/use1/ses-shared-rule-set` and
`infra/live/use1/sandbox-secrets-key`. Both paths are **hardcoded to `use1`**,
not derived from `primary_region`.

### The SCP unit is the one that does not `include "root"`

Every other unit in the tree inherits `root.hcl`'s backend and provider. The SCP
unit declares its own, because it has to run against a different account:

```hcl
# infra/live/management/scp/terragrunt.hcl
locals {
  site     = read_terragrunt_config(find_in_parent_folders("site.hcl")).locals.site
  accounts = read_terragrunt_config(find_in_parent_folders("site.hcl")).locals.accounts
}

generate "provider" {
  path      = "provider.tf"
  if_exists = "overwrite_terragrunt"
  contents  = <<-EOF
    provider "aws" {
      region = "us-east-1"
      assume_role {
        role_arn = "arn:aws:iam::${local.accounts.organization}:role/${local.site.label}-org-admin"
      }
      ...
    }
  EOF
}

remote_state {
  config = {
    # state key is NOT region-prefixed — Organizations is a global service
    key = "${local.site.tf_state_prefix}/management/scp/terraform.tfstate"
    ...
  }
}

terraform {
  source = "${dirname(find_in_parent_folders("CLAUDE.md"))}/infra/modules/scp//v2.0.0"
}
```

Two details there are load-bearing. The role it assumes is
`{prefix}-org-admin`, so a non-default install assumes **its own** org-admin role
rather than the canonical install's. And the state key omits the region, because
an SCP is global — putting it under a region label would have made a second
region's apply create a second, competing policy.

### `km init` — 28 units, and why the order is not cosmetic

`regionalModules()` in `internal/app/cmd/init.go` is the ordered list. Two
different wiring mechanisms decide that order:

- **`efs` reads a file.** Its `terragrunt.hcl` does
  `jsondecode(file("${get_terragrunt_dir()}/../network/outputs.json"))`, so
  `network` must have applied first and `outputs.json` must be on disk. That file
  is a gitignored local cache, written from `terragrunt output -json` or fetched
  straight out of the state object in S3 (`fetchAndCacheOutputs`). This is why
  `km init --plan` on a fresh install *skips* `efs` and exits 0 rather than
  failing.
- **The five bridge Lambdas use `dependency` blocks** with
  `mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]`.
  Terragrunt resolves those itself. `"show"` in that list is not optional — the
  destroy-class plan gate runs `terragrunt show` on the plan file, and a unit
  missing it fails there rather than during apply.

A representative unit, start to finish:

```hcl
# infra/live/use1/network/terragrunt.hcl
locals {
  repo_root     = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars     = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
}

include "root" { path = find_in_parent_folders("root.hcl") }

terraform { source = "${local.repo_root}/infra/modules/network/v1.1.0" }

inputs = {
  km_label     = local.site_vars.locals.site.label
  region_label = local.region_config.locals.region_label
  vpc = { cidr_block = "10.0.0.0/16", ... }
  nat_gateway = { enabled = tobool(get_env("KM_NAT_GATEWAY_ENABLED", "false")) }
}
```

### One value, all the way through

`resource_prefix` is the clearest trace, because it names almost every resource
in the install:

```text
km-config.yaml            resource_prefix: km
      │
      ▼  ExportTerragruntEnvVars()
KM_RESOURCE_PREFIX=km     process env, before every terragrunt exec
      │
      ▼  infra/live/site.hcl
locals.site.label            = get_env("KM_RESOURCE_PREFIX", "km")
locals.site.tf_state_prefix  = "tf-${get_env("KM_RESOURCE_PREFIX", "km")}"
locals.backend.bucket        = "${local.site.tf_state_prefix}-state-${local.region.label}"
locals.backend.dynamodb_table= "${local.site.tf_state_prefix}-locks-${local.region.label}"
      │
      ├──▼  infra/live/root.hcl        remote_state.config.bucket / dynamodb_table
      └──▼  a unit's inputs            km_label = local.site_vars.locals.site.label
              │
              ▼  infra/modules/<name>/<vN>/variables.tf
           var.km_label → every resource name and every tag
```

Set `resource_prefix: rg` and the same tree provisions a second, fully isolated
install in the same account — different state bucket, different lock table,
different SCP, different SES rules. Nothing in `infra/` needs editing, which is
the whole point of routing it through `get_env()` rather than hardcoding it.

## Reading the colour

The palette is the `dc35` skin: near-black paper, teal accent, and one addition
made specifically for this set.

| Treatment | Means |
|---|---|
| **Dashed red** | A trust boundary. Something is denied at this line, by something other than the code inside it. |
| **Solid red** | Residual risk, or a path that terminates because a control fired. |
| **Teal** | The one or two elements that carry the diagram's argument. Never more than two per figure. |
| **Grey fill, soft border** | Hand-authored by a person (diagrams 6 and 7 only). |

Red is not in the base skin — it was added here because "boundary" and "deny"
are semantic roles the palette had no token for, and because a security diagram
that signals its boundaries with the same colour as its focal nodes is not
signalling anything.

## What these diagrams deliberately do not claim

Every figure names its own limits rather than implying a guarantee it cannot
keep. Three worth restating, because they are the ones most often read
optimistically:

- **`privileged: true` collapses the two innermost rings** in diagram 1 and
  erases layer L3 in diagram 2. Everything from account IAM outward survives it,
  because it is enforced by something the instance cannot reach. The real
  control is `privileged: false`.
- **The secrets broker is a logged door, not a wall.** `km-env` states an
  identity; it does not authenticate one. What changes is the character of a
  theft — a passive file read becomes a pid-attributed request that writes a
  `secret_unseal` event. That is detection, not prevention.
- **A policy simulator cannot verify diagram 5.** AWS reports an unsatisfiable
  condition exactly as it reports a missing statement, so a simulator tells you
  what a policy *says*, never what AWS *enforces*. The negative control in the
  boot selftest is the only thing that proves the fence.

## Regenerating or editing

These were authored with the `diagram-design` skill against the `dc35` client
profile (`references/style-guide.md`). Editing one by hand is fine — they are
plain SVG on a 4px grid. To validate a change:

```bash
python3 <diagram-design-skill-dir>/scripts/self_check.py 01-containment-boundaries.html
```

To export a PNG or SVG for a slide, follow the skill's `references/export.md`;
nothing here depends on the export having been run.
