# Phase 125: Per-profile private-subnet sandboxes with per-AZ NAT gateways - Research

**Researched:** 2026-08-19
**Domain:** Terraform module versioning + Go CLI network plumbing (AWS VPC/NAT/EC2), in-repo only
**Confidence:** HIGH

## Summary

This is a pure code-archaeology research task — no new libraries, no external docs. All
findings below are `[VERIFIED: <file>]` by direct inspection of this repo's source, except
where noted `[ASSUMED]` (general Terraform semantics not exercised live in this session).

The design in CONTEXT.md/the design spec is sound and its locked decisions are not
revisited. The single most planning-critical fact this research adds, **not present in
CONTEXT.md's canonical_refs**, is that there is a **third, previously-unflagged module
version pin**: `infra/templates/sandbox/terragrunt.hcl:43` hardcodes
`source = ".../infra/modules/${substrate_module}/v1.2.0"` as **one literal string shared by
both the `ec2spot` and `ecs` substrates**. This file is copied byte-for-byte (no templating)
into every new sandbox directory by `pkg/terragrunt/sandbox.CreateSandboxDir`, for **both**
`km create` and the `km destroy` local-directory-missing fallback. Two consequences:

1. Bumping ec2spot to v1.3.0 **must** edit this file, or the rename has no effect at create
   time and every new sandbox keeps loading v1.2.0.
2. `infra/modules/ecs/` only has a `v1.0.0` directory — this same hardcoded `v1.2.0` string
   already points ECS substrate at a **nonexistent** `infra/modules/ecs/v1.2.0/` today. ECS
   sandbox creation is presently broken by this file, independent of Phase 125. The fix
   (per-substrate version lookup) is required to land the ec2spot bump cleanly and, as a
   side effect, stops ECS from being pinned to a *different* wrong path.

The HIGHEST-PRIORITY question — whether the ec2spot `public_subnets`→`sandbox_subnets`
rename endangers the `ttl-handler` destroy path — resolves to **safe, by an existing and
already-proven pattern**: `ttl-handler` hardcodes destroy to the **frozen `v1.0.0`** module
unconditionally (never the version used at create time), and this has already tolerated two
prior module evolutions (v1.0.0→v1.1.0→v1.2.0 added new resources not present in v1.0.0's
config) without incident. Terraform `destroy` operates against **state contents**, not the
resource set declared in current config — a resource in state but absent from the
destroy-time config is destroyed as an "orphan," which is exactly how the existing v1.0.0
pin has survived two real version bumps already. See Finding 1 for the full trace.

**Primary recommendation:** Treat this phase as three coordinated but separable edits: (A)
network module v1.1.0 (NAT/route-table restructure, additive), (B) ec2spot module v1.3.0 +
the **`infra/templates/sandbox/terragrunt.hcl` per-substrate version fix** (new, not in
CONTEXT.md) + the ONE ec2spot-only template line in `service_hcl.go` (not the ECS one), (C)
Go plumbing (NetworkOutputs → SandboxSubnets → sweep → compiler → SandboxMetadata →
doctor/guards), following the `slack.default_router`/`github.default_router` config-plumbing
exemplar precisely (Finding 3) for the new `network.nat_gateway` key.

## Architectural Responsibility Map

This codebase is an AWS infrastructure-provisioning CLI + Terraform module set, not a web
app — the standard Browser/SSR/API/CDN/DB tier taxonomy doesn't map cleanly. Substituting
this project's actual layers:

| Capability | Primary Layer | Secondary Layer | Rationale |
|------------|-------------|----------------|-----------|
| NAT/EIP/route-table provisioning | Terraform module (`infra/modules/network`) | Terragrunt live unit (`infra/live/use1/network`) | Module owns resource shape; live unit owns the install-level toggle value |
| Subnet placement decision (public vs private) | Go CLI orchestration (`internal/app/cmd/create.go`) | Terraform module (`ec2spot`, consumes resolved subnet list) | Placement is a policy decision made once in Go, before compilation; the module just wires whatever subnet ID it's given |
| AZ failover / capacity ranking | Go CLI (`pkg/capacity.RankAZs`) | — | Pure decision logic, AWS-API-backed, no Terraform involvement |
| Config toggle plumbing (`network.nat_gateway`) | Go CLI config layer (`internal/app/config`) | Terragrunt `get_env` (`infra/live/.../terragrunt.hcl`) | Config owns YAML→env; terragrunt owns env→Terraform var |
| Per-sandbox state (`network_placement`) | AWS runtime (DynamoDB via `pkg/aws`) | Go CLI (read/write call sites) | DynamoDB is the source of truth; Go call sites are just readers/writers that must not drop the field |
| Guard / doctor checks | Go CLI (`internal/app/cmd/doctor_*.go`, `create.go` fail-fast) | — | Pure Go logic reading Config + DynamoDB, no new infra |

## Phase Requirements

| ID | Description (inferred from phase description) | Research Support |
|----|-------------|------------------|
| REQ-125-NETMOD | Network module v1.1.0: per-AZ NAT/EIP/route tables, `moved` blocks, plural outputs | Finding 6 (Terraform mechanics), Finding 7 (outputs propagation) |
| REQ-125-EC2SPOT | ec2spot module v1.3.0: `sandbox_subnets` rename + `associate_public_ip` | Finding 1 (destroy-path safety), Finding 2 (golden test exposure) |
| REQ-125-TOGGLE | Two dormant-by-default toggles (`network.nat_gateway`, `spec.network.privateSubnet`) | Finding 3 (config plumbing exemplar), schema anchor below |
| REQ-125-PLUMB | Go plumbing: NetworkOutputs → single resolution point → AZ sweep → compiler | Finding 5 (AZ sweep mechanics + exact insertion point) |
| REQ-125-REMOTE | create-handler Lambda / `km create --remote` see the new outputs | Finding 7 (outputs.json write/read, local + S3 fallback) |
| REQ-125-GUARD | Fail-fast at `km create`; refuse `km init` NAT-disable while private sandboxes run | Finding 4 (SandboxMetadata round-trip precedent) |
| REQ-125-DOCTOR | `km doctor` WARN on NAT-idle / private-without-NAT | Finding 3 (doctor file convention), doctor file inventory below |
| REQ-125-UAT | Live UAT per CONTEXT.md success criteria | Validation Architecture section |

---

## Finding 1 (HIGHEST PRIORITY): ttl-handler destroy path, the `km destroy` fallback path, and the ec2spot rename

### The two destroy paths are different, and only one was in CONTEXT.md

**Path A — `ttl-handler` Lambda (TTL/idle auto-destroy).**
`cmd/ttl-handler/main.go:1177-1230` `[VERIFIED: cmd/ttl-handler/main.go]`:
```go
moduleSource := "ec2spot"  // line 1178, TODO comment: doesn't handle ECS
...
bundledModule := filepath.Join("/var/task", "infra", "modules", moduleSource, "v1.0.0")  // line 1230
```
This is a **hardcoded literal `"v1.0.0"`**, frozen forever regardless of the actual create-time
module version. It renders a stub `main.tf` with `public_subnets = []`, `availability_zones =
[]`, `ec2spots = []`, `vpc_id = "destroy-placeholder"` and runs `terraform destroy
-auto-approve` against the sandbox's real S3-backed state.

**Path B — `km destroy` operator path, "sandbox dir not found locally" fallback.**
`internal/app/cmd/destroy.go:238-269` `[VERIFIED: internal/app/cmd/destroy.go]`: when the
per-sandbox directory isn't present on the local machine (fresh checkout, different
operator machine, or a `--remote`-created sandbox), it calls `terragrunt.CreateSandboxDir`
(which **copies** `infra/templates/sandbox/terragrunt.hcl` verbatim — see below) and writes
a minimal `service.hcl` with `substrate_module = "ec2spot"`, `module_inputs = {}`. Unlike
ttl-handler, this path is **not** frozen at v1.0.0 — it uses **whatever version is currently
hardcoded** in `infra/templates/sandbox/terragrunt.hcl` at the time `km destroy` runs
(today v1.2.0, after this phase v1.3.0). If the sandbox dir **is** found locally (the normal
case — same machine that ran `km create`), the original `terragrunt.hcl` copied at create
time is reused unmodified — no version drift at all in that case.

### Why the rename is safe for both paths

1. **Resource block *addresses* (not variable names) drive what `terraform destroy` deletes,
   and addresses are unchanged.** Comparing `infra/modules/ec2spot/v1.0.0/main.tf` and
   `v1.2.0/main.tf` resource block names `[VERIFIED: infra/modules/ec2spot/v1.0.0/main.tf,
   v1.2.0/main.tf]`: v1.0.0 has 26 resource blocks (`aws_vpc.sandbox`,
   `aws_spot_instance_request.ec2spot`, `aws_instance.ec2_ondemand`, `aws_iam_role.ec2spot_ssm`,
   etc.); v1.2.0 has all 26 of those **plus 6 more** added since (`ec2spot_github_inbound_sqs`,
   `aws_ebs_volume.snapshot`, `aws_volume_attachment.snapshot`,
   `ec2spot_sandbox_secrets_kms`, `ec2spot_sandbox_secrets_s3`). None of the original 26 were
   renamed or removed across three real version bumps. `sandbox_subnets` in v1.3.0 is a
   **variable** rename only — it does not touch any resource block name.
2. **`terraform destroy` proposes deletion for every resource instance found in prior
   state, independent of whether the resource is declared in the config currently being
   used to run the destroy.** This is standard, long-documented Terraform behavior (the same
   mechanism that destroys a resource block you simply delete from your `.tf` and then
   `apply`) `[ASSUMED: general Terraform semantics, not exercised live this session — but
   this is exactly the behavior the existing v1.0.0-pin-for-all-versions design already
   depends on and has apparently worked without an incident report across v1.0.0→v1.2.0]`.
   Concretely: v1.2.0-created sandboxes have `aws_ebs_volume.snapshot` etc. in their state
   that v1.0.0's config (used for ttl-handler destroy) never declares — if orphan-destroy
   semantics didn't hold, those resources would already be un-destroyable today, which
   would have surfaced as a known incident. None is recorded in CLAUDE.md's phase history.
3. **All ec2spot module variables used in the destroy stub have safe defaults.**
   `public_subnets` / `sandbox_subnets`: `default = []` in every version
   `[VERIFIED: infra/modules/ec2spot/v1.2.0/variables.tf]`. `associate_public_ip` (new in
   v1.3.0 per CONTEXT.md) is specified to default `true` — irrelevant at destroy time since
   `ec2spots = []` means the instance resource's `count` evaluates to 0 in the stub config
   (no create is planned; only state-tracked deletes happen).

**Conclusion: the ec2spot rename does not threaten either destroy path.** This is not a new
guarantee introduced by this phase — it's the same tolerance the codebase has already relied
on for two prior version bumps, now exercised a third time.

### New finding not in CONTEXT.md: the shared, ECS-breaking version pin

`infra/templates/sandbox/terragrunt.hcl:43` `[VERIFIED: infra/templates/sandbox/terragrunt.hcl]`:
```hcl
terraform {
  source = "${local.repo_root}/infra/modules/${local.svc_config.locals.substrate_module}/v1.2.0"
}
```
This is copied **verbatim, with no substitution**, by `pkg/terragrunt/sandbox.go:13-29`
`CreateSandboxDir` (`copyFile(src, dst)`, no templating at all)
`[VERIFIED: pkg/terragrunt/sandbox.go]`. This is the **actual, single, currently-live
version pin used by `km create` for every new sandbox** (both substrates) — and by the
`km destroy` local-dir-missing fallback described above. It is NOT the same file as
`infra/templates/sandbox/service.hcl`, which is a **stale, unused reference/scaffold**
(comment: "DO NOT edit manually — this template is the structural pattern only"; it
describes a defunct `ecs-cluster`/`ecs-task`/`ecs-service` split superseded by the combined
`ecs` module, and is never read by any Go code — confirmed by a repo-wide grep for
`infra/templates` outside `pkg/terragrunt/sandbox.go`'s own doc comment and the
`km init --sidecars` bundling tar list) `[VERIFIED: infra/templates/sandbox/service.hcl;
grep -rn "infra/templates" pkg/terragrunt/ internal/app/cmd/ cmd/]`.

**Because this ONE literal `v1.2.0` string is shared by both substrates, and
`infra/modules/ecs/` only has a `v1.0.0` directory** `[VERIFIED: ls infra/modules/ecs/]`,
**ECS-substrate `km create` is already broken today** — `terraform init` would fail
resolving `infra/modules/ecs/v1.2.0` (nonexistent). This is a pre-existing, out-of-scope bug,
but it means: **the plan must restructure this line into a per-substrate lookup** (e.g. a
locals map `{ ec2spot = "v1.3.0", ecs = "v1.0.0" }`) rather than a single bump — doing a
blind global bump to `v1.3.0` would just repoint the (already-broken) ECS path at a
*different* nonexistent directory. A per-substrate map is a strict improvement either way,
and is the only change this phase needs to make here (no obligation to fully fix ECS
substrate — out of scope — just don't make the version-pin file worse or leave the
ec2spot rename inert).

**This file is bundled into the Lambda/remote toolchain wholesale** — `internal/app/cmd/init.go:3587`
`tar czf ... infra/modules infra/live infra/templates ...` `[VERIFIED: internal/app/cmd/init.go:3587]`
— so the create-handler Lambda picks up the same version-pin edit on the next
`make build-lambdas` + `km init --dry-run=false` deploy, consistent with CONTEXT.md's stated
deploy surface (no additional deploy step required beyond what's already planned).

### Also confirmed: no `moved`-block interaction between the two destroy paths and the network module

Both destroy paths (`ttl-handler` and `km destroy`'s fallback) only ever reference the
**`ec2spot`** module — neither touches the `network` module's Terraform state at all (the
network module's shared VPC/subnet/NAT state lives entirely in
`infra/live/use1/network/terraform.tfstate`, applied only via `km init`, never per-sandbox).
So the network module's v1.0.0→v1.1.0 `moved` blocks (route table restructure) have **zero
interaction** with either sandbox-destroy code path — that risk is fully contained to the
one-time `km init --dry-run=false` apply of the network live unit.

---

## Finding 2: Golden/byte-identity test exposure

**Exactly one test assertion needs updating, and no golden fixture file changes.**
`[VERIFIED: pkg/compiler/compiler_test.go, pkg/compiler/testdata/]`

- `pkg/compiler/testdata/` contains **exactly one** golden file:
  `service_hcl_km_prefix_94_05.golden.hcl`. Its `substrate_module = "ecs"` header
  (line 6) and `public_subnets = ["subnet-a", "subnet-b"]` (line 13) belong to the **ECS**
  template (`ecsServiceHCLTemplate`, `service_hcl.go:222`) — ECS's underlying module
  (`infra/modules/ecs/v1.0.0/variables.tf:38`, `variable "public_subnets"`) is **not**
  renamed by this phase. **Do not touch this golden file.**
- `pkg/compiler/compiler_test.go:97-131` `TestCompileEC2ServiceHCL`, the `checks` list at
  line ~144 includes the literal substring `"public_subnets"` — this is the **ec2spot**
  template's test (`substrate_module = "ec2spot"` check at line ~141), and **must** change
  to `"sandbox_subnets"` once `service_hcl.go:105`'s template literal is renamed.
- `pkg/compiler/compiler_test.go:628-648` `TestCompileECSNetworkConfig` asserts on the
  **value** `"subnet-pub1"`, not the field-name literal `"public_subnets"` — despite its
  `t.Errorf` message saying "missing public_subnets," the actual `strings.Contains` check
  doesn't test the field name at all. **Unaffected by the rename either way.**

`service_hcl.go` has **two** template literals reading `public_subnets = [...]`
(`ec2ServiceHCLTemplate` line 105 — rename this one; `ecsServiceHCLTemplate` line 222 —
leave this one) and **three** Go structs carrying a `PublicSubnets []string` field:
`ec2HCLParams` (line 486, feeds the EC2 template — candidate to rename alongside the
template, or keep the Go field name and only change what flows into it, per CONTEXT.md's
"Claude's Discretion" on exact symbol naming), `ecsHCLParams` (line 548, feeds the ECS
template — must NOT change), and `NetworkConfig` (line 687, the compiler's own input struct
— this is the struct CONTEXT.md's `SandboxSubnets` resolution point should populate; see
Finding 5). This exactly matches CONTEXT.md's own count ("3 template structs + 2 templates
emitting `public_subnets`") — the ambiguity CONTEXT.md left open (which of the 3/2 is which)
is now resolved: **rename only the ec2spot-side struct/template; the ECS-side ones must stay
`public_subnets` because the ECS module's own variable name is untouched.**

The **frozen pre-92 userdata baseline** and other userdata goldens are entirely
unrelated — this phase makes zero userdata changes (compiler HCL only), consistent with
CONTEXT.md's scope. `[VERIFIED: no `userdata` references anywhere in the CONTEXT.md decision
set or the files this research touched]`.

---

## Finding 3: Config plumbing exemplar (`slack.default_router` / `slack.allow` precedent, applied to `network.nat_gateway`)

There is **no existing `network:` top-level YAML block or `NetworkConfig`-named struct in
`internal/app/config/config.go`** `[VERIFIED: grep "^type.*struct" internal/app/config/config.go]`
— Phase 125 adds a brand-new top-level section, following the exact pattern used for
`SlackConfig`, `GithubConfig`, `H1Config`. The clearest, most recent exemplar for a
**tri-state install-level bool** nested under a new/existing top-level block is
`slack.default_router` / `github.default_router` (Phase 96/101). Full touchpoint list with
anchors, all `[VERIFIED: internal/app/config/config.go, internal/app/cmd/init.go,
infra/live/use1/lambda-github-bridge/terragrunt.hcl]`:

| # | Touchpoint | File:Line (exemplar) | What Phase 125 needs |
|---|------------|----------------------|------------------------|
| 1 | Struct field | `config.go:66` `DefaultRouter *bool` on `SlackConfig`; `config.go:266` same on `GithubConfig` | New `NetworkConfig` struct (or reuse an existing minimal one) with `NATGateway *bool \`mapstructure:"nat_gateway" yaml:"nat_gateway,omitempty"\`` |
| 2 | **Merge-list entry (CRITICAL — silent-drop footgun)** | `config.go:897` `"slack.default_router",` inside the v2→v merge-list array | Add `"network.nat_gateway",` to the same array, with a comment citing `project_config_key_merge_list` |
| 3 | Load logic | `config.go:1033-1035` `if v.IsSet("slack.default_router") { val := v.GetBool(...); cfg.Slack.DefaultRouter = &val }` | Same shape for `network.nat_gateway` → `cfg.Network.NATGateway` |
| 4 | Env export + drift WARN | `init.go:1657-1669` (`KM_SLACK_DEFAULT_ROUTER`) and `init.go:1737-1751` (`KM_GITHUB_DEFAULT_ROUTER`) — both follow: only export when yaml value differs from empty/default; WARN + env-wins if `os.Getenv(...)` already set to something different | New block exporting `KM_NAT_GATEWAY_ENABLED` with the identical WARN-on-drift shape |
| 5 | Terragrunt `get_env` consumption | `infra/live/use1/lambda-github-bridge/terragrunt.hcl:100` `github_default_router = get_env("KM_GITHUB_DEFAULT_ROUTER", "false")` — flows into a Lambda `environment.variables` string map (no type coercion needed) | `infra/live/use1/network/terragrunt.hcl` — **different from the exemplar**: `nat_gateway.enabled` is a **native Terraform `bool`-typed** variable (`optional(bool, false)`), not a Lambda env string, so the get_env call needs a `tobool()` wrap: `nat_gateway = { enabled = tobool(get_env("KM_NAT_GATEWAY_ENABLED", "false")) }` — flag this explicitly, it's the one place this exemplar doesn't transfer 1:1 |
| 6 | `km doctor` check | dedicated `doctor_slack.go` (paired with `doctor_slack_peer_bridges_test.go` etc.) — one file per concern, `doctor_*.go` + `doctor_*_test.go` | New `doctor_network.go` + `doctor_network_test.go` for the two WARN checks (NAT-idle, private-without-NAT) |

`km configure` is **not** part of this exemplar chain — `slack.default_router` /
`github.default_router` are not interactive-wizard questions, they're manually-added
YAML keys (consistent with CLAUDE.md's "Claude's Discretion" area not mentioning
`km configure`). No evidence this phase needs to touch `configure.go`.

---

## Finding 4: SandboxMetadata round-trip — exact file list for `network_placement`

Exemplar: `SlackReactAlways *bool` (Phase 91.5), whose own doc comment at
`pkg/aws/metadata.go:65-69` **already states the exact failure mode** this phase must avoid
(a struct that drops the field reverts the sandbox to defaults on the next pause/resume
`PutItem`). `[VERIFIED: pkg/aws/metadata.go, pkg/aws/sandbox_dynamo.go,
internal/app/cmd/create_slack_inbound.go, internal/app/cmd/resume.go]`

Files that must carry a new field (e.g. `NetworkPlacement string
\`json:"network_placement,omitempty"\`` — public/private, or empty = pre-125 sandbox):

1. **`pkg/aws/metadata.go:11`** — add the field to the `SandboxMetadata` struct itself, with
   a comment following the existing round-trip-warning convention (lines 54-56, 65-69 are
   ready-made templates for the wording).
2. **`pkg/aws/sandbox_dynamo.go`** — the actual DynamoDB marshal/unmarshal chokepoint:
   `marshalSandboxItem(meta *SandboxMetadata)` (line 433, write path — add an
   `item["network_placement"] = &dynamodbtypes.AttributeValueMemberS{...}` guarded by
   non-empty, mirroring line 520-521's `if meta.SlackReactAlways != nil` pattern) and
   `unmarshalSlackFields`/sibling unmarshal helpers (line 293 area — either extend an
   existing helper or add a small `unmarshalNetworkFields`, called from
   `toSandboxMetadata()` at line 79 and `unmarshalSandboxItem` at line 174). **This is the
   single chokepoint** — once the field round-trips here, `resume.go`, `extend.go`, and
   `cmd/ttl-handler/main.go`'s read-modify-write paths preserve it automatically without
   per-file changes, *provided* they read-then-PutItem the whole struct (which they already
   do for `SlackReactAlways` — confirmed by that field needing no dedicated resume.go/extend.go
   edit beyond the marshal/unmarshal chokepoint, other than the writer at create time).
3. **A new writer at `km create` time** analogous to `internal/app/cmd/create_slack_inbound.go`
   — write `network_placement` once, immediately after the sandbox is created (a new small
   file or an addition to an existing create.go step; CONTEXT.md leaves exact placement to
   discretion).
4. **`internal/app/cmd/init.go`** appears in the `SlackReactAlways` grep hit list too — this
   is `km status`/display code reading the field for operator-facing output, not part of the
   round-trip mechanism itself; a `network_placement` display line in `km status` is optional
   nice-to-have, not required for correctness.

---

## Finding 5: Phase 124 AZ sweep — exact single-resolution-point insertion

`internal/app/cmd/create.go:780-935` `[VERIFIED: internal/app/cmd/create.go]`. Mechanics,
precisely:

- `network` is a `*compiler.NetworkConfig` (constructed earlier, see below) with two
  **index-paired** slices: `network.AvailabilityZones` and `network.PublicSubnets` —
  `AvailabilityZones[i]` and `PublicSubnets[i]` are always the same AZ's data.
- `RankAZs(ctx, rankInstanceType, region, azPreference, capacityStore, ec2c, sqc,
  network.AvailabilityZones)` (call site `create.go:811`) takes and returns **only an AZ
  list** — it has no knowledge of subnets at all.
- Immediately after ranking, `create.go:824-843` builds `subnetByAZ` by zipping the
  **original** `AvailabilityZones[i] ↔ PublicSubnets[i]`, then rebuilds
  `network.PublicSubnets` in the **ranked** AZ order by looking each ranked AZ up in that map.
- Inside the retry loop (`create.go:900-921`), on `attempt > 0` **both**
  `network.PublicSubnets` and `network.AvailabilityZones` are rotated in lockstep
  (`append(x[1:], x[0])`) — this is what "rotate on ICE" means mechanically.
- `compiler.Compile(resolvedProfile, sandboxID, onDemand, network, amiBDMDevices)` is called
  fresh on every attempt, so whatever `network.PublicSubnets` currently holds flows straight
  into the compiler.

**The single resolution point CONTEXT.md calls for is: add a `network.SandboxSubnets
[]string` field to `compiler.NetworkConfig` (`service_hcl.go:685-700`), populate it exactly
once** — right before `create.go:795`'s `if substrate != "docker" && len(...AvailabilityZones)
> 0 {` block begins — **as either `network.PublicSubnets` or `network.PrivateSubnets`
depending on `resolvedProfile.Spec.Network.PrivateSubnet`**, then mechanically replace every
`network.PublicSubnets` reference **inside the sweep block only** (lines 829, 830, 841, 903,
909, 919, 921) with `network.SandboxSubnets`. `network.PublicSubnets` itself stays
populated with the true public list throughout (needed for the ECS path, which doesn't get
private placement, and for anything that legitimately wants "the public subnets" later).
The **compiler** consumer side then needs exactly one edit:
`service_hcl.go:796`'s `ec2HCLParams{ PublicSubnets: network.PublicSubnets, ...}` →
`SandboxSubnets: network.SandboxSubnets` (feeding the renamed ec2spot template field);
`service_hcl.go:1019`'s ECS `ecsHCLParams{ PublicSubnets: network.PublicSubnets, ...}`
**stays unchanged** (ECS always gets true public subnets — no private placement support
this phase).

**`compiler.NetworkConfig` is constructed in exactly three places**, all of which need a
`PrivateSubnets []string` field populated alongside the existing `PublicSubnets`:
`create.go:611-624` (local/interactive `km create`), `create.go:2415-2432` (`km create
--remote`), `budget.go:365-379` (budget top-up re-provisioning path — re-provisions an
existing sandbox from its stored profile, so it also needs to resolve placement the same
way; CONTEXT.md's consumer list already names all three of these lines). All three currently
read from a `*NetworkOutputs` (`internal/app/cmd/init.go:436-442`) populated by
`LoadNetworkOutputs` — that struct is the correct place for the new `PrivateSubnets
[]string \`json:"private_subnets"\`` and `NATGatewayIDs []string
\`json:"nat_gateway_ids"\`` fields (see Finding 7).

**`RankAZs` signature must change** — it currently has exactly **one caller in the entire
repo** (`create.go:811`; verified by a repo-wide grep, no second call site in `km capacity`
or elsewhere) `[VERIFIED: grep -rn "RankAZs(" excluding tests and rankaz.go itself]`, so this
is a low-blast-radius change. The natural insertion point for "drop any AZ with no NAT" is
inside `pkg/capacity/rankaz.go`'s **Step 1** (`rankaz.go:166-176`, right after the
`DescribeAZOfferings` offered-list is computed, before Step 2's GPU quota gate) — filter
`offered` down to AZs present in a new `natAZs []string`/`map[string]bool` parameter, added
to `RankAZs`'s signature and passed by the caller only when `privateSubnet: true` (public
sandboxes pass nil/skip the filter, a no-op).

---

## Finding 6: Terraform mechanics — `moved` blocks and terragrunt mock_outputs

**`moved` blocks:** GA since Terraform 1.1, fully supported under the pinned **1.9.8**
`[VERIFIED: internal/app/cmd/init.go:69, const tfDesiredVersion = "1.9.8"]`
`[ASSUMED: general well-established Terraform feature, not verified via a live `terraform
plan` in this session]`. Syntax for the singleton→indexed transition CONTEXT.md specifies:
```hcl
moved {
  from = aws_route_table.private
  to   = aws_route_table.private[0]
}
```
Placed anywhere in the module's `.tf` files (conventionally alongside the resource block).
Terraform reconciles state automatically on the next plan — shown as "moved" not
"destroy+create," satisfying CONTEXT.md's "keep the first apply's plan clean" requirement
and staying outside the destroy-class gate (a move is not a destroy).

**Terragrunt `mock_outputs` — does NOT apply here.** The known
`project_terragrunt_show_needs_mocks` gotcha is specific to a native terragrunt `dependency
{}` block. A repo-wide search found **zero** live units using a native `dependency` block
against the `network` module `[VERIFIED: grep -rn "dependency \"network\"" infra/live/]` —
the only downstream consumer, `infra/live/use1/efs/terragrunt.hcl:11,44`, reads network
outputs via **`jsondecode(file("${get_terragrunt_dir()}/../network/outputs.json"))`**, a
plain HCL file-read, not a terragrunt dependency graph edge. This mechanism has no
`mock_outputs_allowed_terraform_commands` requirement — it just fails hard if
`outputs.json` doesn't exist yet, which is pre-existing bootstrap ordering (network must be
applied first), unrelated to this phase. **EFS reads only `vpc_id` and `public_subnets`**
`[VERIFIED: infra/live/use1/efs/terragrunt.hcl:44]` — both output names are **unchanged** by
v1.1.0 (only new plural outputs are added; `nat_gateway_id`/`nat_eip_public_ip` singular
outputs are the only ones dropped, and EFS never read those). This directly confirms
CONTEXT.md's scope-fence claim ("EFS needs no change") at the mechanism level, not just by
assertion.

The **sandbox/create path** (the *other* real consumer of network outputs) doesn't use
terragrunt `dependency` blocks either — it goes through Go code (`LoadNetworkOutputs`,
Finding 7), so it also has no `mock_outputs` exposure.

---

## Finding 7: Remote (Lambda) path — how `km create --remote` and create-handler see network outputs

`internal/app/cmd/init.go` `[VERIFIED]`:

- **Write side (`km init` apply):** `init.go:2405-2410` — immediately after `terragrunt
  apply` on the `network` module, `runner.Output(ctx, mod.dir)` fetches the **full,
  unfiltered** output map and writes it verbatim to `infra/live/<region>/network/outputs.json`.
  Any new output the module adds (`private_subnets`, `nat_gateway_ids`,
  `private_route_table_ids`, `nat_eip_public_ips`) appears in this file automatically on the
  next `km init --dry-run=false` — **no code change needed on the write side.**
- **Read side, local-file-present case:** `LoadNetworkOutputs` (`init.go:2811-2844`) reads
  that file and calls `extractTFOutput(raw, "<key>", &outputs.<Field>)` **per key it wants**
  — this IS the code-change surface. Add two calls:
  `extractTFOutput(raw, "private_subnets", &outputs.PrivateSubnets)` and
  `extractTFOutput(raw, "nat_gateway_ids", &outputs.NATGatewayIDs)`, alongside the existing
  three (`vpc_id`, `public_subnets`, `availability_zones`).
- **Read side, local-file-absent case (`--remote` / Lambda cold path):**
  `fetchAndCacheOutputs` (`init.go:2881-2940`) reads the network module's **raw S3 tfstate
  object** directly (`s3://tf-{prefix}-state-{region}/tf-{prefix}/{region}/network/terraform.tfstate`),
  parses `state.Outputs map[string]json.RawMessage` — again **unfiltered, all outputs** —
  re-serializes and **caches** it to the same local `outputs.json` path. So this fallback
  also needs **zero code change** to carry the new keys; it flows through the same
  `extractTFOutput` calls added above once `LoadNetworkOutputs` re-parses the cached file.
- **Lambda bundling:** the create-handler Lambda's `infra.tar.gz` bundle
  (`init.go:3579-3589`) tars up `infra/live` **wholesale**, so whatever `outputs.json`
  exists locally on the machine that ran the last `make build-lambdas` + `km init
  --dry-run=false` is what's baked into the Lambda's snapshot; `fetchAndCacheOutputs` is
  the Lambda-side fallback/refresh if that snapshot is stale or the file is missing at
  cold-start. Both paths are automatically correct once (a) the network module v1.1.0 is
  applied and (b) the two `extractTFOutput` calls are added — no separate remote-path logic
  is required beyond what CONTEXT.md's `NetworkOutputs` extension already specifies.

**`km create --remote` construction site** (`create.go:2415-2432`) is a straight duplicate
of the local construction site (`create.go:611-624`) — same `LoadNetworkOutputs` call, same
`compiler.NetworkConfig{...}` literal. Both need the identical
`PrivateSubnets: networkOutputs.PrivateSubnets` addition; there is no separate remote-only
code path to design.

---

## Standard Stack

Not applicable — this phase adds zero new external dependencies. All work is within the
existing Go module (`go 1.25.5`), Terraform 1.9.8, Terragrunt 0.99.1 toolchain already
pinned by the project.

## Package Legitimacy Audit

**N/A — no external packages installed by this phase.** All changes are to existing
first-party Terraform modules and Go source files.

## Architecture Patterns

### Recommended edit sequence (dependency-ordered, not necessarily the wave/task split)

1. `infra/modules/network/v1.1.0/` — new version dir (copy of v1.0.0), NAT/EIP/route-table
   `count`-ification, `moved` blocks, plural outputs (Finding 6).
2. `infra/live/use1/network/terragrunt.hcl` — bump `source` to v1.1.0 (line 33) **and** wire
   `nat_gateway.enabled = tobool(get_env("KM_NAT_GATEWAY_ENABLED", "false"))` (line ~53,
   Finding 3 row 5).
3. `infra/modules/ec2spot/v1.3.0/` — new version dir, `sandbox_subnets` rename +
   `associate_public_ip` var.
4. `infra/templates/sandbox/terragrunt.hcl` — **the newly-found third pin** — restructure the
   single `v1.2.0` literal into a per-substrate lookup that resolves ec2spot→v1.3.0,
   ecs→v1.0.0 (Finding 1).
5. `pkg/compiler/service_hcl.go` — rename the EC2-only template field/struct (line 105, 486,
   796); leave the ECS ones (222, 548, 1019) untouched (Finding 2).
6. `pkg/compiler/compiler_test.go` — update the one substring check (line ~144) (Finding 2).
7. `internal/app/config/config.go` — new `NetworkConfig`/`nat_gateway` struct + merge-list
   entry + load logic (Finding 3).
8. `internal/app/cmd/init.go` — `NetworkOutputs` fields + extraction calls + env export/drift
   WARN + display (Finding 3, 5, 7).
9. `internal/app/cmd/create.go` (both sites), `budget.go` — `PrivateSubnets` plumbing +
   `SandboxSubnets` single-resolution-point insertion ahead of the sweep (Finding 5).
10. `pkg/capacity/rankaz.go` — NAT-AZ filter parameter, single caller update (Finding 5).
11. `pkg/aws/metadata.go` + `sandbox_dynamo.go` + a create-time writer — `network_placement`
    round-trip (Finding 4).
12. `pkg/profile/schemas/sandbox_profile.schema.json:444-452` (insert after `enforcement`,
    still inside the `network` object's `properties`) + `pkg/profile/types.go:576-586`
    (`NetworkSpec` struct, append field after `Enforcement`) — `privateSubnet` schema +
    Go field.
13. Fail-fast guard (`km create`) + refuse-disable guard (`km init`) + `doctor_network.go`
    WARNs (CONTEXT.md decisions; Finding 3 row 6 for file convention).

### Anti-Patterns to Avoid

- **Do not** rename `service_hcl.go:222`'s `public_subnets` (ECS template) — its module
  variable is untouched; renaming it breaks ECS's already-fragile creation path further.
- **Do not** blind-bump `infra/templates/sandbox/terragrunt.hcl`'s single version literal to
  `v1.3.0` without making it per-substrate — this would silently repoint ECS at yet another
  wrong directory (still broken, differently).
- **Do not** fork the AZ sweep loop into a "private variant" — CONTEXT.md is explicit and
  Finding 5 confirms the sweep's internals (rotation, ranking, retry) are substrate/placement
  agnostic once they operate on a `SandboxSubnets` list resolved once up front.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| AZ↔subnet reordering on rank/rotate | A second, private-aware sweep loop | The existing `create.go:780-935` sweep, retargeted at `network.SandboxSubnets` | Already handles ICE classify-retry, capacity recording, ranking; forking it doubles maintenance and is explicitly forbidden by CONTEXT.md |
| Config toggle plumbing | A bespoke `network.nat_gateway` load path | The `slack.default_router`/`github.default_router` 6-touchpoint pattern (Finding 3) | Proven pattern with dedicated regression-guard tests (`TestLoadGithubDefaultRouter_Set`-style); skipping the merge-list entry is a well-documented, repeatedly-hit footgun in this repo's history |

**Key insight:** every piece of this phase has a nearly-exact precedent already shipped in
this codebase (Phase 96/101 for config toggles, Phase 91.5/97 for DDB round-trip, Phase 124
for the sweep itself). The work is almost entirely "follow the existing pattern," not design.

## Runtime State Inventory

Triggered by the `public_subnets`→`sandbox_subnets` **variable** rename in the ec2spot
module. This is narrower than a typical rename phase (no sandbox-id/service-name rename),
but the trigger still applies, so all 5 categories are answered explicitly:

| Category | Items Found | Action Required |
|----------|-------------|------------------|
| Stored data | **None.** Terraform `.tfstate` files record resource **addresses** and their exported **attribute values** — they do not persist the **input variable names** used to produce a plan. A variable rename has no state-file footprint at all. `[ASSUMED: standard Terraform state-file format, not independently re-derived from an actual `.tfstate` dump in this session, but this is basic/well-known Terraform architecture]` | None |
| Live service config | None — no external service (Slack/GitHub/n8n/Datadog-style) references `public_subnets` by name outside this repo's own Terraform/Go source. | None |
| OS-registered state | None — this is a Terraform module variable, not an OS-level registration. | None |
| Secrets/env vars | None — `public_subnets` is not an env var name or SSM key anywhere in this repo `[VERIFIED: the earlier repo-wide grep for `public_subnets` found only .tf/.hcl/.go/golden hits, no SSM/env-var occurrences]`. | None |
| Build artifacts / installed packages | The three module-version-pin locations (network live unit, ec2spot's implicit consumer via `infra/templates/sandbox/terragrunt.hcl`) are themselves "build artifacts" of a sort — covered exhaustively in Findings 1 and the edit sequence above. | Edit the 2 pin files as listed |

**Net effect:** this rename is exceptionally low-risk from a runtime-state perspective —
its entire blast radius is the handful of `.go`/`.tf`/`.hcl` files already enumerated.

## Common Pitfalls

### Pitfall 1: Renaming the ECS-side `public_subnets` by mistake
**What goes wrong:** `service_hcl.go` has two nearly-identical template lines and three
structs with the same field name; a search-and-replace across the whole file breaks ECS.
**Why it happens:** the two templates are visually similar and both say `public_subnets`.
**How to avoid:** rename only `service_hcl.go:105`/`486`/`796` (ec2spot); leave `222`/`548`/`1019`
(ecs) untouched. Confirmed disambiguation in Finding 2.
**Warning signs:** `TestCompileECSNetworkConfig` or the golden file test starts failing.

### Pitfall 2: Bumping `infra/templates/sandbox/terragrunt.hcl`'s version pin globally
**What goes wrong:** a single `v1.2.0`→`v1.3.0` string replace repoints ECS at a
still-nonexistent directory (just a different wrong path than today).
**Why it happens:** the file interpolates `substrate_module` but hardcodes one shared
version suffix — easy to miss since it's not in CONTEXT.md's canonical_refs at all.
**How to avoid:** restructure to a per-substrate lookup (Finding 1, Architecture Patterns
step 4).
**Warning signs:** ECS-substrate `km create`/`km validate` smoke test (if one exists) starts
failing differently than before, or a `terraform init: module not found` error mentioning
`ecs/v1.3.0`.

### Pitfall 3: Forgetting the config merge-list entry
**What goes wrong:** `network.nat_gateway: true` in `km-config.yaml` is silently ignored —
`km init` behaves as if it were `false`, with no error.
**Why it happens:** this is a documented, repeatedly-hit footgun in this exact codebase
(`project_config_key_merge_list`) — the merge-list array (`config.go:~870-910`) is a
separate step from adding the struct field and load logic.
**How to avoid:** add `"network.nat_gateway",` to the merge-list array (Finding 3 row 2) in
the same commit as the struct field.
**Warning signs:** `km init --dry-run=false` after setting the key shows no NAT
resources in the plan.

### Pitfall 4: `nat_gateway.enabled` type mismatch
**What goes wrong:** `terragrunt.hcl`'s `nat_gateway = { enabled = get_env(...) }` passes a
**string** ("true"/"false") into a Terraform variable typed `optional(bool, false)`, which
Terraform will likely coerce automatically in HCL2 but is inconsistent with how every other
`get_env`-sourced toggle in this repo is consumed (all existing examples flow into Lambda
`environment.variables` string maps, not native Terraform bools).
**Why it happens:** `network.nat_gateway` is the first `get_env`-sourced toggle that feeds a
native `bool`-typed module variable rather than a Lambda env string.
**How to avoid:** wrap explicitly: `tobool(get_env("KM_NAT_GATEWAY_ENABLED", "false"))`.
**Warning signs:** `terraform plan` shows a type-conversion warning/error on `nat_gateway.enabled`.

### Pitfall 5: SandboxMetadata field dropped on pause/resume
**What goes wrong:** `network_placement` reverts to empty after a `km resume`/`km extend`,
even though the sandbox is genuinely still private (harmless for placement itself, since
placement is fixed at EC2-launch time and can't actually change — but it **does** break the
`km init` refuse-to-disable-NAT guard, which depends on this DDB field to find running
private sandboxes).
**Why it happens:** documented, repeated pattern in this codebase
(`project_sandboxmetadata_lossy_roundtrip`) — any read-modify-write path that reconstructs
a `SandboxMetadata` without going through the shared marshal/unmarshal in
`sandbox_dynamo.go` drops fields not explicitly copied.
**How to avoid:** add the field to `marshalSandboxItem`/`unmarshalSlackFields`-sibling code
in `sandbox_dynamo.go` (the single chokepoint), not to each individual caller.
**Warning signs:** `km status` shows the field, then it disappears after a `km pause &&
km resume` cycle.

## Code Examples

### `moved` block (network module v1.0.0 → v1.1.0)
```hcl
# Source: infra/modules/network/v1.1.0/main.tf (new)
moved {
  from = aws_route_table.private
  to   = aws_route_table.private[0]
}
```

### `get_env` + `tobool` for a native-bool Terraform variable
```hcl
# Source: infra/live/use1/network/terragrunt.hcl (existing file has zero get_env calls today)
nat_gateway = {
  enabled = tobool(get_env("KM_NAT_GATEWAY_ENABLED", "false"))
}
```

### Config merge-list + load pattern (exact precedent to copy)
```go
// Source: internal/app/config/config.go:897, :1033-1035 (github.default_router precedent)
// 1. In the v2->v merge-list array:
"network.nat_gateway",
// 2. In the load function:
if v.IsSet("network.nat_gateway") {
    val := v.GetBool("network.nat_gateway")
    cfg.Network.NATGateway = &val
}
```

### `extractTFOutput` extension for new network outputs
```go
// Source: internal/app/cmd/init.go:2831-2841 (LoadNetworkOutputs)
outputs := &NetworkOutputs{}
if err := extractTFOutput(raw, "vpc_id", &outputs.VPCID); err != nil { return nil, err }
if err := extractTFOutput(raw, "public_subnets", &outputs.PublicSubnets); err != nil { return nil, err }
if err := extractTFOutput(raw, "availability_zones", &outputs.AvailabilityZones); err != nil { return nil, err }
_ = extractTFOutput(raw, "sandbox_mgmt_sg_id", &outputs.SandboxMgmtSGID)
// New for Phase 125 (both non-fatal-if-absent, mirroring sandbox_mgmt_sg_id, since a
// pre-125 network module's outputs.json won't have these keys yet):
_ = extractTFOutput(raw, "private_subnets", &outputs.PrivateSubnets)
_ = extractTFOutput(raw, "nat_gateway_ids", &outputs.NATGatewayIDs)
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|---------------|--------|
| Single un-counted `aws_route_table.private` | Per-AZ indexed `aws_route_table.private[count.index]` | This phase (v1.1.0) | Required because one route table cannot hold 4 distinct `0.0.0.0/0` NAT targets |
| `associate_public_ip_address = true` hardcoded (2 sites in ec2spot) | Variable-driven `associate_public_ip` (default true) | This phase (v1.3.0) | Enables private placement without a public IPv4 (unroutable + billable in a private subnet since Feb 2024) |

**Deprecated/outdated:** the network module's singular `nat_gateway_id` / `nat_eip_public_ip`
outputs are dropped in v1.1.0 (per CONTEXT.md) — confirmed no consumer reads them today
(`[VERIFIED: grep -rn "nat_gateway_id\|nat_eip_public_ip" infra/ internal/ pkg/ --include=*.go --include=*.hcl` returned only the network module's own outputs.tf definitions, no external readers]`), so dropping them is safe.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Terraform `destroy` proposes deletion for every state-tracked resource regardless of whether current config declares it ("orphan destroy" semantics) | Finding 1 | If wrong, the ttl-handler/km-destroy fallback paths could fail to destroy some resources on a version-mismatched destroy — but this is not a NEW risk introduced by this phase; it's the exact mechanism the codebase has silently depended on for 2 prior ec2spot version bumps already, so a live UAT destroy test (already in CONTEXT.md's success criteria #6) will catch it if wrong |
| A2 | `moved` blocks are fully supported and behave as documented under the pinned Terraform 1.9.8 | Finding 6 | Extremely low risk — `moved` has been stable since 1.1; not independently re-verified via a live `terraform plan` this session |
| A3 | Terraform `.tfstate` does not persist input variable names, only resource addresses/attributes | Runtime State Inventory | If wrong, the ec2spot rename would need a state-data migration step, not just a code edit — very low likelihood given this is fundamental, widely-documented Terraform architecture |

## Open Questions

1. **Exact Go symbol names for the ec2spot-side rename (`ec2HCLParams.PublicSubnets` →
   `SandboxSubnets`?)**
   - What we know: CONTEXT.md explicitly leaves "exact Go symbol/field naming beyond
     `SandboxSubnets`/`PrivateSubnets`/`NATGatewayIDs`" to discretion.
   - What's unclear: whether the plan should rename `ec2HCLParams.PublicSubnets` too, or
     just change what's assigned into it.
   - Recommendation: rename it for consistency with the module variable it feeds
     (`sandbox_subnets`) — avoids a confusing "Go field named PublicSubnets holding private
     subnet IDs" state, which is exactly the naming-lie CONTEXT.md's design rationale for
     the ec2spot rename itself warns against.

2. **Should `infra/templates/sandbox/terragrunt.hcl`'s per-substrate version fix also repair
   ECS's already-broken pin (point it at `ecs/v1.0.0`, the only version that exists)?**
   - What we know: today it points at a nonexistent `ecs/v1.2.0`; ECS substrate creation is
     already broken independent of this phase.
   - What's unclear: whether "fixing" this is in scope, or whether the plan should just make
     the pin per-substrate-correct-for-ec2spot while leaving ECS pointed at whatever value
     preserves current (broken) behavior, to avoid any scope creep into ECS.
   - Recommendation: fix it (point ecs at `v1.0.0`, the version that actually exists) as a
     one-line side effect of making the lookup per-substrate — it costs nothing extra and
     removes a footgun, but flag it to the user as an incidental fix outside the phase's
     stated scope in case they'd rather leave ECS's breakage untouched for a separate phase.

## Environment Availability

Skipped — no new external tool/service dependencies. AWS CLI, Terraform 1.9.8, Terragrunt
0.99.1 are already required and present per the existing toolchain (`internal/app/cmd/init.go`
`tfDesiredVersion`/`tgVersion` constants).

## Validation Architecture

`workflow.nyquist_validation` is `true` in `.planning/config.json` — section included.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` (stdlib), `go test` |
| Config file | none — standard `go.mod` (`go 1.25.5`) |
| Quick run command | `go test ./pkg/compiler/... ./pkg/capacity/... ./internal/app/config/... -run TestCompileEC2\|TestLoadNAT\|TestRankAZs -v` (name the new tests to match) |
| Full suite command | `go test ./... -count=1 -timeout 600s` (per CLAUDE.md/memory: capture the command's own exit code, not a piped `tail`'s — known false-green footgun in this repo) |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| REQ-125-NETMOD | network v1.1.0 plan is additive-only, no destroy-class trip | manual/live (terraform plan is not a `go test`) | `terragrunt plan` in `infra/live/use1/network/` after flipping the toggle | N/A — live UAT only, per CONTEXT.md success criterion #2 |
| REQ-125-EC2SPOT | ec2spot v1.3.0 template renders `sandbox_subnets`, respects `associate_public_ip` | unit | `go test ./pkg/compiler/ -run TestCompileEC2ServiceHCL -v` | ✅ `pkg/compiler/compiler_test.go` (existing, needs the one-line update from Finding 2) |
| REQ-125-TOGGLE | `network.nat_gateway` round-trips through config Load(); dormant by default | unit | `go test ./internal/app/config/ -run TestLoad -v` | ✅ `internal/app/config/config_test.go` exists — Wave 0 gap: add `TestLoadNetworkNATGateway_Set`/`_Unset` mirroring `TestLoadGithubDefaultRouter_Set` |
| REQ-125-PLUMB | `SandboxSubnets` resolved once, sweep operates on it unchanged | unit | `go test ./internal/app/cmd/ -run TestCreate -v` | ✅ `internal/app/cmd/create_test.go` exists — Wave 0 gap: add a placement-resolution table test |
| REQ-125-REMOTE | `--remote` path carries `PrivateSubnets`/`NATGatewayIDs` | unit | `go test ./internal/app/cmd/ -run TestLoadNetworkOutputs\|TestInit -v` | ✅ `internal/app/cmd/init_test.go` exists (already asserts on `public_subnets` extraction per the earlier grep hit at line 56) — Wave 0 gap: extend with the two new keys |
| REQ-125-GUARD | fail-fast at create; refuse NAT-disable with running private sandboxes | unit | `go test ./internal/app/cmd/ -run TestCreate.*Private\|TestInit.*NAT -v` | ❌ Wave 0 — new test file/cases |
| REQ-125-DOCTOR | `km doctor` WARNs on NAT-idle / private-without-NAT | unit | `go test ./internal/app/cmd/ -run TestDoctorNetwork -v` | ❌ Wave 0 — new `doctor_network_test.go`, mirror `doctor_slack_peer_bridges_test.go`'s structure |
| REQ-125-UAT | live 7-step UAT per CONTEXT.md | manual/live | N/A — operator-run against real AWS | N/A |

### Sampling Rate
- **Per task commit:** targeted `go test ./pkg/compiler/... ./pkg/capacity/... -run <NewTest> -v`
- **Per wave merge:** `go test ./internal/app/cmd/... ./internal/app/config/... -count=1`
- **Phase gate:** `go test ./... -count=1 -timeout 600s` green, plus
  `scripts/validate-all-profiles.sh` passing (per CONTEXT.md success criteria), before
  `/gsd-verify-work`.

### Wave 0 Gaps
- [ ] `internal/app/config/config_test.go` — add `TestLoadNetworkNATGateway_{Set,Unset}` (merge-list regression guard, mirroring `TestLoadGithubDefaultRouter_Set`)
- [ ] `internal/app/cmd/create_test.go` — add a placement-resolution table test (public/private → correct `SandboxSubnets`)
- [ ] `internal/app/cmd/init_test.go` — extend the existing `public_subnets` output-extraction test with `private_subnets`/`nat_gateway_ids`
- [ ] `internal/app/cmd/doctor_network_test.go` — new file, NAT-idle + private-without-NAT WARN cases
- [ ] `pkg/capacity/rankaz_test.go` — extend for the new NAT-AZ-filter parameter (exists already, needs new cases, not a new file)
- [ ] `pkg/aws/sandbox_dynamo_test.go` (if it exists — not directly confirmed this session) — round-trip test for `network_placement`, mirroring whatever test covers `SlackReactAlways`

## Security Domain

`security_enforcement` is not set to `false` anywhere seen — treated as enabled.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V4 Access Control | Yes | Private subnet placement is a **security-positive** capability add (removes public IPv4 exposure entirely for opted-in sandboxes) — no new attack surface, an existing-pattern reduction of one |
| V5 Input Validation | Marginal | New `spec.network.privateSubnet` bool and `network.nat_gateway` bool are both plain booleans validated by JSON Schema `type: boolean` — no injection surface |
| V6 Cryptography | No | Not applicable — no new secrets/crypto material |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Egress bypassing the DNS/HTTP proxy allowlist via NAT | Elevation of Privilege / Info Disclosure | **Not a new concern** — NAT only changes the *path* egress takes to the internet (via a NAT EIP instead of a direct public IP), not *whether* it's filtered. The existing `spec.network.egress` allowlist enforcement (Security Groups + eBPF/proxy sidecars) operates at the instance/cgroup level, upstream of NAT, and is untouched by this phase — confirmed by CONTEXT.md's UAT step 4 explicitly re-testing the `evil.example.com` block from a private sandbox. |
| NAT Gateway becoming an unintended shared trust boundary between sandboxes in the same AZ | Info Disclosure | Out of scope for this phase (flagged as a real property, not a regression) — all sandboxes in the same AZ share that AZ's NAT EIP as their observed source IP; this is inherent to NAT and not something Phase 125 changes or is expected to mitigate. Worth a one-line callout in the operator doc this phase is expected to touch. |

## Sources

### Primary (HIGH confidence — direct file reads this session)
- `cmd/ttl-handler/main.go` (destroy stub, moduleSource pin)
- `internal/app/cmd/destroy.go` (operator destroy path, minimalHCL fallback)
- `pkg/terragrunt/sandbox.go` (CreateSandboxDir/PopulateSandboxDir — confirms verbatim file copy, no templating)
- `infra/templates/sandbox/terragrunt.hcl`, `infra/templates/sandbox/service.hcl` (the newly-found shared version pin; confirmed the service.hcl file is unused/stale)
- `infra/modules/network/v1.0.0/{main,variables,outputs}.tf`
- `infra/modules/ec2spot/{v1.0.0,v1.2.0}/{main,variables}.tf`
- `infra/live/use1/network/terragrunt.hcl`, `infra/live/use1/efs/terragrunt.hcl`, `infra/live/use1/lambda-{github,slack}-bridge/terragrunt.hcl`
- `pkg/compiler/service_hcl.go`, `pkg/compiler/compiler_test.go`, `pkg/compiler/testdata/service_hcl_km_prefix_94_05.golden.hcl`
- `internal/app/config/config.go` (SlackConfig/GithubConfig DefaultRouter/Allow precedent, merge-list array)
- `internal/app/cmd/init.go` (NetworkOutputs, LoadNetworkOutputs, fetchAndCacheOutputs, env export/drift-WARN pattern, Lambda tar bundling)
- `internal/app/cmd/create.go` (AZ sweep, NetworkConfig construction x2)
- `internal/app/cmd/budget.go` (NetworkConfig construction x3rd site)
- `pkg/capacity/rankaz.go` (RankAZs implementation, sole caller confirmed via repo-wide grep)
- `pkg/aws/metadata.go`, `pkg/aws/sandbox_dynamo.go` (SandboxMetadata round-trip precedent)
- `pkg/terragrunt/planreport/protected.go` (ProtectedTypes list — confirmed no route_table/nat_gateway/eip entries)
- `pkg/profile/schemas/sandbox_profile.schema.json`, `pkg/profile/types.go` (NetworkSpec schema/struct insertion anchors)
- `.planning/config.json` (nyquist_validation: true)

### Secondary (MEDIUM confidence)
- None used — no web search or external docs were needed for this in-repo research task.

### Tertiary (LOW confidence / assumed)
- General Terraform `destroy`/orphan-resource/`moved`-block semantics (Assumptions A1, A2) — well-established, widely-documented Terraform behavior, not independently re-verified via a live `terraform plan`/`destroy` in this session.

## Metadata

**Confidence breakdown:**
- ttl-handler/destroy safety (Finding 1): HIGH — directly verified code + well-established Terraform semantics; the one live-UAT-only piece (an actual destroy of a version-mismatched sandbox) is already covered by CONTEXT.md's UAT plan
- Golden test exposure (Finding 2): HIGH — every relevant file read directly, no golden files missed
- Config plumbing exemplar (Finding 3): HIGH — exact file:line anchors for a proven, repeatedly-used pattern in this exact codebase
- AZ sweep integration (Finding 5): HIGH — full function read, single resolution point identified precisely
- Terraform mechanics (Finding 6): HIGH for the terragrunt-mock_outputs non-applicability (directly verified); MEDIUM-leaning-HIGH for `moved`-block syntax (standard, not independently re-derived)
- Remote path (Finding 7): HIGH — both write and read sides traced end-to-end

**Research date:** 2026-08-19
**Valid until:** this is a point-in-time snapshot of a specific pre-existing codebase state (module versions, line numbers); treat as valid only until the plan actually starts editing these files — re-check line numbers if execution is delayed more than a few days past this research.
