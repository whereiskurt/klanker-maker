# Design spec — Per-profile private-subnet sandboxes with per-AZ NAT gateways

**Phase:** 125
**Date:** 2026-08-19
**Status:** design locked (operator decisions made up front; intended for an autonomous run)

## Problem

Every sandbox today lands in a public subnet with `associate_public_ip_address = true`
and egresses via the IGW. There is no way to place a sandbox on a private subnet
behind a NAT gateway — useful when a partner allowlists a static egress IP, or when
policy forbids public IPv4 on workload ENIs.

## Operator decisions (locked — do NOT re-litigate during execution)

1. **Per-profile placement toggle**, not install-wide. The operator will run both
   public and private sandboxes side by side in the same VPC.
2. **One NAT gateway per AZ**, not a single shared NAT. Phase 124's AZ-failover sweep
   rotates sandboxes across all 4 AZs; a single NAT would add cross-AZ data-transfer
   charges to every byte and make one AZ a single point of failure for the whole
   install's egress.
3. **Additive and reversible.** Turning NAT on is a pure-additive apply. Turning it
   off must be possible (and cheap to decide) whenever no private sandboxes are
   running — NAT is the expensive part and the operator wants it gone when idle.
4. **Dormant by default.** Both toggles absent ⇒ byte-identical behaviour to Phase 124.

## Design

### Two independent toggles

| Toggle | Scope | Default | Controls |
|---|---|---|---|
| `network.nat_gateway` (km-config.yaml) | install / region | `false` | whether NAT+EIP infrastructure *exists* |
| `spec.network.privateSubnet` (SandboxProfile) | per sandbox | `false` | whether *this* sandbox's ENI lands private |

They are deliberately decoupled: NAT can exist with zero private sandboxes (costing
money, flagged by `km doctor`), and a private profile can be authored while NAT is
off (rejected at `km create` with an actionable message, not at `km validate` — the
profile itself is not wrong, the install state is).

### Network module → `infra/modules/network/v1.1.0`

New version directory (repo convention: versioned module dirs, never edit a released
version in place). Copy of v1.0.0 with:

- `aws_eip.nat` — `count = var.nat_gateway.enabled ? length(var.vpc.private_subnets_cidr) : 0`
- `aws_nat_gateway.nat` — same count; `subnet_id = aws_subnet.public_subnet[count.index].id`
  so each NAT sits in the **public subnet of the same AZ** as the private subnet it serves.
- `aws_route_table.private` — becomes `count = length(var.vpc.private_subnets_cidr)`
  (was a single un-counted resource). One private route table per AZ is mandatory:
  a shared route table cannot hold four different `0.0.0.0/0` NAT targets.
- `aws_route_table_association.private` — `private_subnet[i] → aws_route_table.private[i]`
- `aws_route.private_nat_gateway` — `count = enabled ? N : 0`, `aws_route_table.private[i] → aws_nat_gateway.nat[i]`
- **`moved` blocks** from `aws_route_table.private` → `aws_route_table.private[0]` to keep
  the first apply's plan clean.
- New outputs (plural): `private_route_table_ids`, `nat_gateway_ids`, `nat_eip_public_ips`.
  The old singular `nat_gateway_id` / `nat_eip_public_ip` outputs are dropped in v1.1.0.

**When NAT is disabled** the private subnets and their per-AZ route tables still exist
with no default route — an island. This is intentional, costs nothing, and is what makes
the toggle purely additive in both directions.

**Destroy-class gate:** `aws_route_table`, `aws_nat_gateway`, and `aws_eip` are NOT in
`pkg/terragrunt/planreport/protected.go` `ProtectedTypes` (verified). The route-table
restructure will not trip the gate and does not need `--i-accept-destroys`.

### `ec2spot` module → v1.3.0

- Rename var `public_subnets` → `sandbox_subnets`. The var is load-bearing for reading
  correctness; feeding private subnet IDs into something named `public_subnets` is the
  kind of naming lie that causes a later incident. `local.effective_subnets` and the
  auto-create fallback follow the rename.
- New var `associate_public_ip` (bool, **default `true`**). Replaces the two hardcoded
  `associate_public_ip_address = true` sites. In a private subnet this must be `false` —
  a public IPv4 there is unroutable *and* billable (~$0.005/hr since Feb 2024).

### Go plumbing

- `NetworkOutputs` (`internal/app/cmd/init.go:436`) gains `PrivateSubnets []string` and
  `NATGatewayIDs []string`; extraction at `init.go:2835`, display at `init.go:2416`.
- Resolve placement **once**, into a single `SandboxSubnets` list, before the Phase 124
  AZ sweep — then the sweep at `create.go:790–921` (which reorders subnets in lockstep
  with `AvailabilityZones` and rotates on ICE) operates on the resolved list unchanged.
  Do not fork the sweep.
- Consumers to update: `create.go:621`, `create.go:2431`, `budget.go:376`,
  `pkg/compiler/service_hcl.go` (3 template structs + 2 templates emitting `public_subnets`).
- `RankAZs`: for a private sandbox, drop any AZ with no NAT gateway. With NAT count ==
  AZ count this is a no-op today, but it is the correct guard if EIP quota ever caps NAT count.

### Remote (Lambda) path

- **create-handler** runs `km create` as a subprocess and re-reads network outputs from S3.
  The S3-cached outputs must carry `private_subnets` + `nat_gateway_ids`.
- **ttl-handler** (`cmd/ttl-handler/main.go:1215`) renders a destroy-placeholder `main.tf`
  with hardcoded var names (`public_subnets = []`) and pins the module path to
  **`v1.0.0`** (`filepath.Join("/var/task", "infra", "modules", moduleSource, "v1.0.0")`).
  **Verify first** whether that pin makes the destroy path immune to the ec2spot rename or
  whether it produces a state/schema mismatch when destroying a v1.3.0-created sandbox.
  This is the highest-risk unknown in the phase — resolve it before the rename lands.

### Profile schema

`spec.network` is `additionalProperties: false` (`pkg/profile/schemas/sandbox_profile.schema.json:422`),
so `privateSubnet` must be added to the schema, not just the Go struct. Additive optional
bool, default false. **No `apiVersion` bump.**

### State + guards

- `km create` writes `network_placement = "private" | "public"` to the `{prefix}-sandboxes`
  row (schema-on-write, no TF migration). It **must** round-trip through `SandboxMetadata`
  marshal/unmarshal or pause/resume/extend will strip it on the full-row `PutItem`.
- `km create` with a `privateSubnet: true` profile while NAT is disabled → **fail fast**
  with the exact `km-config.yaml` key and `km init` command to fix it.
- `km init` flipping `nat_gateway: false` while any sandbox row has
  `network_placement=private` and is running → refuse, list the sandbox ids.
- `km doctor`: WARN when NAT is enabled but zero private sandboxes exist ("safe to
  disable, costing ~$X/mo"); WARN when private sandboxes exist but NAT is absent.

### Cost (must be printed by `km init` when enabling)

4 AZs × ~$0.045/hr ≈ **~$132/month** baseline, plus **$0.045/GB processed**. This is the
entire reason the toggle must be flippable: a GPU profile pulling 300GB of weights costs
~$13.50 in NAT processing alone, per box.

## Explicitly NOT in scope (do not "fix" these)

- **EFS needs no change.** There is at most one mount target per AZ; a private-subnet
  instance in `us-east-1a` reaches the existing AZ-`1a` mount target that lives in the
  public subnet. `infra/live/use1/efs/terragrunt.hcl:44` stays on `public_subnets`.
- **SSM needs no change.** The agent dials out; `km shell` / `vscode` / `desktop`
  port-forwards work unmodified over NAT. Nothing in the codebase depends on inbound
  public reachability — `PublicIP` appears only cosmetically at `status.go:542`.
- VPC interface/gateway endpoints (a real NAT-cost reduction) — follow-up phase.
- IPv6 / egress-only IGW, cross-region NAT.

## Deploy surface

`make build` (**before** `km init` — the binary carries the new `NetworkOutputs` parsing
and env export; a stale binary silently skips it) → `make build-lambdas` (create-handler
carries the new compiler + profile schema) → `km init --dry-run=false`.
**NOT `--sidecars`** (no sidecar binary changes; the NAT/EIP/route resources need a full
terragrunt apply). Existing sandboxes are unaffected and keep public placement until
`km destroy && km create`.

## UAT (live, in order)

1. Baseline: `km create` a normal profile with NAT off — unchanged, public IP present.
2. Flip `network.nat_gateway: true`, `km init --dry-run=false` — plan is **additive only**
   (4 EIP + 4 NAT + 4 routes + route-table moves), no gate trip.
3. `km create` a `privateSubnet: true` profile — instance has **no** public IP, lands in a
   `10.0.10x.0/24` subnet.
4. From the box: `km shell` works; egress succeeds and the observed source IP equals that
   AZ's NAT EIP; the `spec.network.egress` allowlist still blocks `evil.example.com`.
5. Confirm the AZ sweep: force an ICE-class failure and confirm rotation picks a different
   AZ **and** that AZ's NAT.
6. `km destroy` the private sandbox; flip `network.nat_gateway: false`; `km init` — NAT/EIP
   destroyed, private subnets survive as islands, public sandboxes unaffected.
7. Negative: with NAT off, `km create` the private profile → fails fast with the fix message.
