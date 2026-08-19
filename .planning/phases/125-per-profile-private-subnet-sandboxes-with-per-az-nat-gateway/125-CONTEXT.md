# Phase 125: Per-profile private-subnet sandboxes with per-AZ NAT gateways - Context

**Gathered:** 2026-08-19
**Status:** Ready for planning
**Source:** PRD Express Path (docs/superpowers/specs/2026-08-19-private-subnet-nat-design.md)

<domain>
## Phase Boundary

Sandboxes today always land in a public subnet with `associate_public_ip_address = true`
and egress via the IGW. This phase adds the ability to place a sandbox on a **private**
subnet behind a NAT gateway, chosen **per profile**, while public-subnet sandboxes keep
working unchanged in the same VPC.

Delivers exactly two independent toggles:

| Toggle | Scope | Default | Controls |
|---|---|---|---|
| `network.nat_gateway` (km-config.yaml) | install / region | `false` | whether NAT+EIP infrastructure *exists* |
| `spec.network.privateSubnet` (SandboxProfile) | per sandbox | `false` | whether *this* sandbox's ENI lands private |

Both absent means byte-identical behaviour to Phase 124.

Much of the Terraform already exists: `infra/modules/network/v1.0.0` ships private
subnets, the NAT EIP, the NAT gateway, and the private NAT route, all gated on
`var.nat_gateway.enabled` (currently hardcoded `false` at
`infra/live/use1/network/terragrunt.hcl:53`). The bulk of the work is (a) restructuring
the single private route table into one-per-AZ, and (b) the Go plumbing that threads
subnet IDs from network outputs into the sandbox launch — `NetworkOutputs` carries only
`PublicSubnets` today.

</domain>

<decisions>
## Implementation Decisions

**All four operator decisions below are LOCKED. Do not re-litigate them during planning
or execution.**

### Placement model
- Placement toggle is **per profile** (`spec.network.privateSubnet`), NOT install-wide.
  The operator will run public and private sandboxes side by side in one VPC.
- Install-level `network.nat_gateway` and per-profile `privateSubnet` are **decoupled**.
  NAT may exist with zero private sandboxes (costs money, flagged by `km doctor`); a
  private profile may be authored while NAT is off (rejected at `km create`, NOT at
  `km validate` — the profile is not wrong, the install state is).

### NAT topology
- **One NAT gateway + EIP per AZ.** Not a single shared NAT. Rationale: Phase 124's
  AZ-failover sweep rotates sandboxes across all 4 AZs; a single NAT would add cross-AZ
  data-transfer charges to every egress byte and make one AZ a SPOF for the entire
  install's internet access.
- Each NAT sits in the **public subnet of the same AZ** as the private subnet it serves.

### Reversibility
- Turning NAT on is a **pure-additive apply**. Turning it off must be safe and cheap to
  decide whenever no private sandboxes are running — NAT is the expensive component
  (~$132/mo for 4 AZs + $0.045/GB) and the operator wants it gone when idle.
- **When NAT is disabled, the private subnets and their per-AZ route tables still exist
  with no default route — an island.** This is intentional, costs nothing, and is what
  makes the toggle additive in *both* directions.

### Dormancy
- Both toggles absent means byte-identical to Phase 124. No `apiVersion` bump.

### Module versioning (repo convention: never edit a released version in place)
- **`infra/modules/network/v1.1.0`** (new dir, copy of v1.0.0):
  - `aws_eip.nat` — `count = var.nat_gateway.enabled ? length(var.vpc.private_subnets_cidr) : 0`
  - `aws_nat_gateway.nat` — same count; `subnet_id = aws_subnet.public_subnet[count.index].id`
  - `aws_route_table.private` — becomes `count = length(var.vpc.private_subnets_cidr)`.
    **Mandatory**: one route table cannot hold four different `0.0.0.0/0` NAT targets.
  - `aws_route_table_association.private` — private_subnet[i] to aws_route_table.private[i]
  - `aws_route.private_nat_gateway` — `count = enabled ? N : 0`, private[i] to nat[i]
  - **`moved` blocks** from `aws_route_table.private` to `aws_route_table.private[0]` to keep
    the first plan clean.
  - New plural outputs: `private_route_table_ids`, `nat_gateway_ids`, `nat_eip_public_ips`.
    Old singular `nat_gateway_id` / `nat_eip_public_ip` are dropped in v1.1.0.
- **`infra/modules/ec2spot/v1.3.0`**:
  - Rename var `public_subnets` to `sandbox_subnets` (and `local.effective_subnets` plus
    the auto-create fallback). Rationale: feeding private subnet IDs into something named
    `public_subnets` is the kind of naming lie that causes a later incident.
  - New var `associate_public_ip` (bool, **default `true`**) replacing the two hardcoded
    `associate_public_ip_address = true` sites. Must be `false` in a private subnet — a
    public IPv4 there is unroutable *and* billable (~$0.005/hr since Feb 2024).

### Go plumbing
- `NetworkOutputs` (`internal/app/cmd/init.go:436`) gains `PrivateSubnets []string` and
  `NATGatewayIDs []string`; extraction at `init.go:2835`, display at `init.go:2416`.
- Resolve placement **once**, into a single `SandboxSubnets` list, **before** the Phase 124
  AZ sweep. The sweep at `create.go:790-921` (which reorders subnets in lockstep with
  `AvailabilityZones` and rotates on ICE) then operates on the resolved list unchanged.
  **Do not fork the sweep.**
- Consumers to update: `create.go:621`, `create.go:2431`, `budget.go:376`,
  `pkg/compiler/service_hcl.go` (3 template structs + 2 templates emitting `public_subnets`).
- `RankAZs`: for a private sandbox, drop any AZ with no NAT gateway.

### Config + schema
- Install toggle: `network.nat_gateway` to `KM_NAT_GATEWAY_ENABLED` to `get_env` in
  `infra/live/use1/network/terragrunt.hcl` (which currently has **zero** `get_env` calls).
  Must include the **config v2-to-v merge-list entry** in `config.Load()` or the yaml value
  is silently ignored, plus the env-vs-yaml drift WARN that other knobs have.
- Profile toggle: `spec.network` is `additionalProperties: false`
  (`pkg/profile/schemas/sandbox_profile.schema.json:422`), so `privateSubnet` must be added
  to the JSON schema, not just the Go struct.

### State + guards
- `km create` writes `network_placement = "private" | "public"` to the `{prefix}-sandboxes`
  row (schema-on-write, no TF migration). It **must** round-trip through `SandboxMetadata`
  marshal/unmarshal or pause/resume/extend will strip it on the full-row `PutItem`.
- `km create` with `privateSubnet: true` against a NAT-less install must **fail fast** with
  the exact km-config.yaml key and `km init` command to fix it.
- `km init` flipping `nat_gateway: false` while any sandbox row is
  `network_placement=private` and running must refuse, listing the sandbox ids.
- `km doctor`: WARN when NAT is enabled but zero private sandboxes exist (include the
  monthly cost, "safe to disable"); WARN when private sandboxes exist but NAT is absent.
- `km init` prints the NAT cost when enabling.

### Claude's Discretion
- Exact Go symbol/field naming beyond `SandboxSubnets` / `PrivateSubnets` / `NATGatewayIDs`.
- Test structure and table-test layout; where to put the placement-resolution helper.
- Wording of the fail-fast and doctor messages (must name the key + fix command).
- Whether `moved` blocks live in the module or a one-shot migration file.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase design contract
- `docs/superpowers/specs/2026-08-19-private-subnet-nat-design.md` — the authoritative
  design spec for this phase: locked decisions, module changes, guards, scope fences, UAT.

### Terraform to change
- `infra/modules/network/v1.0.0/main.tf`, `variables.tf`, `outputs.tf` — source for v1.1.0
- `infra/modules/ec2spot/v1.2.0/main.tf`, `variables.tf` — source for v1.3.0
- `infra/live/use1/network/terragrunt.hcl` — hardcoded `nat_gateway.enabled = false` (line 53)

### Go to change
- `internal/app/cmd/init.go` — `NetworkOutputs` (:436), display (:2416), extraction (:2835)
- `internal/app/cmd/create.go` — consumers (:621, :2431), Phase 124 AZ sweep (:790-921)
- `internal/app/cmd/budget.go:376` — consumer
- `pkg/compiler/service_hcl.go` — 3 template structs + 2 templates emitting `public_subnets`
- `pkg/profile/schemas/sandbox_profile.schema.json:422` — `spec.network`, `additionalProperties:false`
- `cmd/ttl-handler/main.go:1215` — destroy-placeholder main.tf (see Risk Summary)

### Prior-art / conventions
- `pkg/terragrunt/planreport/protected.go` — `ProtectedTypes` destroy-class gate list
- `.planning/phases/124-*/` — the AZ sweep this phase integrates with

</canonical_refs>

<specifics>
## Specific Ideas

**Verified during scoping — treat as established fact, do not re-derive:**

- `aws_route_table`, `aws_nat_gateway`, and `aws_eip` are **NOT** in `ProtectedTypes`
  (`pkg/terragrunt/planreport/protected.go`). The private route-table restructure will not
  trip the destroy-class gate and does not need `--i-accept-destroys`.
- Nothing in the codebase depends on inbound public reachability. `PublicIP` appears only
  cosmetically at `internal/app/cmd/status.go:542`.
- The network module already has private subnets at `10.0.101-104.0/24`,
  `availability_zone_count = 4`, and 4 public subnets — so NAT count is 4.

**Cost (must be surfaced by `km init` when enabling):** 4 x ~$0.045/hr is about
**$132/month** baseline plus **$0.045/GB processed**. A GPU profile pulling 300GB of weights
costs ~$13.50 in NAT processing alone, per box. This is the entire reason the toggle must be
flippable.

**Deploy surface:** `make build` (**before** `km init` — the binary carries the new
`NetworkOutputs` parsing and env export; a stale binary silently skips it), then
`make build-lambdas` (create-handler carries the new compiler + profile schema), then
`km init --dry-run=false`. **NOT `--sidecars`** (no sidecar binary changes; the NAT/EIP/route
resources need a full terragrunt apply). Existing sandboxes are unaffected and keep public
placement until `km destroy && km create`.

</specifics>

<risks>
## Risk Summary

**HIGHEST-RISK UNKNOWN — resolve before the ec2spot rename lands.**
`cmd/ttl-handler/main.go:1215` renders a destroy-placeholder `main.tf` that hardcodes the
ec2spot var names (`public_subnets = []`) **and** pins the module path to `v1.0.0`
(`filepath.Join("/var/task", "infra", "modules", moduleSource, "v1.0.0")`). Determine
whether that pin makes the destroy path immune to the `public_subnets` to `sandbox_subnets`
rename, or whether destroying a v1.3.0-created sandbox produces a state/schema mismatch.
The answer may argue for keeping the old var name. **Plan this as an investigation task
that gates the rename**, not as an assumption.

Secondary risks:
- Renaming the ec2spot var changes emitted `service.hcl`; check whether any golden or
  byte-identity test covers it. (The *userdata* goldens are untouched — no userdata change
  in this phase — and the frozen pre-92 baseline must never be re-captured.)
- `network_placement` silently lost on pause/resume if not threaded through `SandboxMetadata`.
- `network.nat_gateway` silently ignored if the config v2-to-v merge-list entry is omitted.
- EIP quota: default is 5 per region; 4 NAT EIPs fits but leaves little headroom.

</risks>

<scope_fence>
## Explicitly Out of Scope — do NOT "fix" these

- **EFS needs no change.** There is at most one mount target per AZ; a private-subnet
  instance in `us-east-1a` reaches the existing AZ-`1a` mount target that lives in the public
  subnet. `infra/live/use1/efs/terragrunt.hcl:44` stays on `public_subnets`.
- **SSM needs no change.** The agent dials out; `km shell` / `vscode` / `desktop`
  port-forwards work unmodified over NAT.
- VPC interface/gateway endpoints (a real NAT-cost reduction) — follow-up phase.
- IPv6 / egress-only IGW.
- Cross-region NAT, multi-region failover.
- Converting existing running sandboxes to private placement.

</scope_fence>

<deferred>
## Deferred Ideas

- VPC endpoints (SSM/S3/DDB/ECR) to cut NAT data-processing cost — separate phase.
- Per-AZ NAT cost telemetry / attribution into `km otel`.
- Allowing a running sandbox to migrate public-to-private without recreate.

</deferred>

<success_criteria>
## Success Criteria (live UAT, in order)

1. Baseline: `km create` a normal profile with NAT off — unchanged, public IP present.
2. Flip `network.nat_gateway: true`, `km init --dry-run=false` — plan is **additive only**
   (4 EIP + 4 NAT + 4 routes + route-table moves), no destroy-class gate trip.
3. `km create` a `privateSubnet: true` profile — instance has **no** public IP, lands in a
   `10.0.10x.0/24` subnet.
4. From the box: `km shell` works; egress succeeds and the observed source IP equals that
   AZ's NAT EIP; the `spec.network.egress` allowlist still blocks `evil.example.com`.
5. Force an ICE-class failure — the AZ sweep rotates to a different AZ **and** that AZ's NAT.
6. `km destroy` the private sandbox; flip `network.nat_gateway: false`; `km init` — NAT/EIP
   destroyed, private subnets survive as islands, public sandboxes unaffected.
7. Negative: with NAT off, `km create` the private profile fails fast with the fix message.

Plus: `go test ./...` green (whole-repo suite is green as of 2026-06-13 — a FAIL means a real
regression) and `scripts/validate-all-profiles.sh` passing.

</success_criteria>

---

*Phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway*
*Context gathered: 2026-08-19 via PRD Express Path*
