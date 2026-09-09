# Security and infrastructure diagrams

Seven self-contained HTML diagrams. Each is one file with inline SVG — no build
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
| 4 | [Bridge Lambda trust boundary](04-bridge-trust-boundary.html) | Why a public, unauthenticated Function URL is safe, and why the bridge has no route into the VPC |
| 5 | [The IMDS fence](05-imds-fence-traces.html) | Two processes on one box asking for the same credential, rule by rule |
| 6 | [Terragrunt invocation path](06-terragrunt-invocation.html) | Who runs Terraform, from where, against which named bucket and lock table |
| 7 | [IaC file provenance](07-iac-provenance.html) | Which process authors every file an apply touches — and why `backend.tf` is not in the repo |
| 8 | [YAML → HCL compiler](08-yaml-to-hcl-compiler.html) | How the two YAML inputs travel separate routes, and what `compiler.Compile()` actually emits |

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
