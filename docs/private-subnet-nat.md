# Private-subnet sandboxes + per-AZ NAT gateways

**Phase 125.** Sandboxes today always land in a public subnet with a public IPv4 and egress
straight through the Internet Gateway (IGW). This phase adds the ability to place a sandbox
on a **private** subnet behind a NAT gateway, chosen **per profile**, while public-subnet
sandboxes keep working unchanged in the same VPC.

## The toggles

There are three knobs. The first two are the load-bearing pair and are deliberately decoupled;
the third only sizes the topology.

| Toggle | Scope | Default | Controls |
|---|---|---|---|
| `network.nat_gateway` (`km-config.yaml`) | install / region | absent (`false`) | whether NAT + EIP infrastructure **exists** at all |
| `spec.network.privateSubnet` (SandboxProfile) | per sandbox | `false` | whether **this** sandbox's ENI lands private |
| `network.private_subnet_count` (`km-config.yaml`) | install / region | absent (all 4) | **how many** private subnets — and therefore NAT gateways — are built |

The first two absent means byte-identical behaviour to Phase 124 — no new resources, no new
sandbox behaviour, no `apiVersion` bump.

They are decoupled on purpose: NAT can exist with zero private sandboxes running against it
(costs money, `km doctor` WARNs — see below), and a profile can declare
`privateSubnet: true` while NAT is off (rejected at `km create` time, **not** at `km validate`
time — the profile itself is never wrong, only the install's current state is). This lets an
operator run public and private sandboxes side by side in one VPC and flip NAT on only when a
private sandbox is actually about to run.

## Per-AZ topology — why not one shared NAT

There is **one NAT gateway + one EIP per Availability Zone** (4 in this install, one per public
subnet), not a single shared NAT. Phase 124's AZ-failover sweep (`capacity.RankAZs` +
classify-and-retry, see `docs/operational-gotchas.md` § AZ failover + capacity feasibility)
routinely rotates a sandbox launch across all 4 AZs chasing capacity. A single shared NAT would:

- add cross-AZ data-transfer charges to every byte of egress from any AZ other than the NAT's
  own, and
- turn that one AZ into a single point of failure for the whole install's internet access.

Each NAT gateway sits in the public subnet of the **same** AZ as the private subnet it serves,
so a sandbox's egress never leaves its own AZ to reach the internet.

## Cost — this is the whole reason the toggle exists

NAT is not cheap, and the toggle's entire purpose is to make it trivially reversible when idle:

- **~$132/month baseline** for 4 NAT gateways (≈$0.045/hr each, 4 AZs), plus
- **$0.045/GB processed**.

Worked example: a GPU profile pulling 300GB of model weights through NAT costs roughly
**$13.50 in NAT data-processing charges alone, per box**, on top of the baseline. If you are
not currently running a private sandbox, there is no reason to be paying the baseline — see
Reversal below.

### Paying for fewer AZs — `network.private_subnet_count`

The baseline scales linearly with AZ count, so the cheapest way to keep NAT on is to build
fewer of them:

```yaml
network:
    nat_gateway: true
    private_subnet_count: 1    # 1 private subnet, 1 NAT, ~$33/mo instead of ~$132
```

The network module counts private subnets, their route tables, the subnet associations, the
EIPs **and** the NAT gateways all off `length(var.vpc.private_subnets_cidr)`, so this single
number sizes the whole private topology. Absent key → all 4, byte-identical to before.
Accepted range is 1–4; `km init` rejects anything outside it, naming the key. Raising the
ceiling means adding CIDRs to `all_private_subnets_cidr` in
`infra/live/<region>/network/terragrunt.hcl` first.

**The tradeoff is capacity tolerance, and it is not visible from the yaml.** Private subnets
are index-paired with AZs, so `private_subnet_count: 1` confines every private sandbox to the
**first** AZ. Phase 124's sweep then has exactly one candidate and cannot rotate: a private
`km create` fails outright when that AZ is capacity-dry for the requested instance type,
rather than shifting to another AZ as it would with all 4 built. Weigh ~$99/month against that.

**Public-subnet sandboxes — the default — are unaffected.** `availability_zone_count` and the
public subnet list are untouched by this knob; only private placement narrows.

**Lowering it on an existing install destroys subnets.** Going 4 → 1 destroys 3 private
subnets, 3 route tables, and 3 associations. `aws_subnet` and `aws_route_table` are **not** on
the destroy-class protected list, so `km init --plan` will *not* stop you — it shows them as
ordinary destroys. Read the plan, and make sure nothing is running in those subnets first.

## The one-time route-table split on first apply — read this before your first `km init`

**This is disclosed prominently because it can surprise an operator who touches neither
toggle.** In `infra/modules/network/v1.0.0`, `aws_route_table.private` is an un-counted
singleton, gated by nothing at all — only the NAT gateway, EIP, and the private→NAT route are
gated on `nat_gateway.enabled`. `infra/modules/network/v1.1.0` (the version this phase ships)
splits that single route table into **one route table per private subnet**, and it does this
**unconditionally** — the split is a property of sourcing v1.1.0, not of the NAT toggle.

That means the **first** `km init --dry-run=false` you run after this phase ships will show a
non-empty plan for the `network` module — one `moved` block plus three additional empty route
tables plus the private-subnet route-table associations repointed at their own per-AZ table —
**even if `network.nat_gateway` is never set to `true`**. This is expected, additive, and
non-destructive; it happens exactly once (subsequent applies are no-ops on that part), it is
independent of the NAT toggle, and nothing is using the private subnets yet so nothing running
is disrupted.

An operator reading "dormant by default, byte-identical to Phase 124" would reasonably expect a
zero-diff plan on that first apply. That guarantee is about **sandbox behaviour**, not about
the network module's Terraform plan output — plan the `network` module first with
`km init --plan` if you want to see this before it applies.

## Reversal — turning NAT off when idle

```bash
# 1. destroy every private sandbox first
km destroy <private-sandbox-id> --remote --yes

# 2. flip the toggle off in km-config.yaml
#    network:
#      nat_gateway: false

# 3. re-apply
km init --dry-run=false
```

`km init` **refuses** to disable NAT while any sandbox row is `network_placement=private`
unless that row's status is definitively terminal (`failed`, `killed`, `destroyed`,
`terminated`) — it lists the offending sandbox ids and stops. There is no
`--force`/`--i-accept-destroys` override for this guard, by design: NAT is what gives those
sandboxes egress, so pulling it out from under a private sandbox would silently sever its
internet access.

Note the guard deliberately does NOT require `status=running`. `km create` does not write a
`status` attribute at all — `km list` derives status by reconciling against live EC2 — so a
fully live private sandbox has an empty status, and a `status=running` filter would match
nothing and fail open. A **stopped** private sandbox also blocks: it needs an egress path
again the moment it resumes. Rows are deleted on destroy, so a present non-terminal row means
the sandbox still exists.

When NAT is disabled, **the private subnets and their per-AZ route tables still exist, with no
default route — a routeless island.** This is intentional and costs nothing. It's what makes
the toggle additive in *both* directions: turning NAT back on later doesn't need to recreate
the private subnets, and turning it off doesn't touch anything a future private sandbox will
need.

## The guards

- **`km create` fail-fast.** A profile with `spec.network.privateSubnet: true` against an
  install with no NAT gateway fails immediately, before any terragrunt artifact or S3 upload,
  naming the `network.nat_gateway` key and the `km init --dry-run=false` command to fix it.
- **`km init` refuse-to-disable.** Covered above — no override flag exists.
- **`km doctor` WARN — NAT idle.** NAT is enabled but zero running private sandboxes depend on
  it. Names the ~$132/month cost and states it's safe to disable.
- **`km doctor` WARN — private sandbox without NAT.** A running private sandbox exists but NAT
  is off (this should be structurally unreachable given the create-time guard, but the check
  exists as a second line of defense against config drift). Names the affected sandbox ids.

Both `km doctor` checks are read-only, degrade to silent-skip if sandbox metadata can't be
fetched (never a false alarm), and are silent on any install that has never touched either
toggle.

## Deploy surface — order is load-bearing

```bash
make build                 # 1. operator binary FIRST
make build-lambdas         # 2. create-handler zip
km init --dry-run=false    # 3. full terragrunt apply
```

**Why this order, specifically:**

1. **`make build` before anything else.** The operator `km` binary is what extracts
   `NetworkOutputs.PrivateSubnets`/`NATGatewayIDs` from the network module's outputs and
   exports `KM_NAT_GATEWAY_ENABLED` to terragrunt. A stale binary silently skips **both** —
   the NAT toggle appears to do nothing, with no error.
2. **`make build-lambdas`.** The create-handler Lambda carries the new compiler
   (`sandbox_subnets`/`associate_public_ip` emission), the updated profile JSON schema
   (`spec.network.privateSubnet`), and the `infra/templates` bundle with the per-substrate
   module version pin (see REQ-125-SUBPIN below) — all needed for `km create --remote` to
   compile a private-placement sandbox correctly.
3. **`km init --dry-run=false`.** The NAT gateways, EIPs, and per-AZ route tables are real
   Terraform resources; they only appear on a full terragrunt apply.

**`--sidecars` is explicitly NOT the deploy path here.** No sidecar binary changed in this
phase — `km init --sidecars` would rebuild and upload `km-*` sidecars for nothing, and it does
not apply any terragrunt module, so the NAT/EIP/route resources would never be created.

**Existing sandboxes are unaffected** and keep public placement until they are recreated
(`km destroy && km create`) — this phase does not touch anything about a running sandbox's
existing network configuration.

## Two accepted properties

- **Shared NAT EIP as observed source IP.** All sandboxes placed in the same AZ share that
  AZ's NAT EIP as their observed egress source IP. This is inherent to how NAT works, not
  something this phase introduces or could avoid without one NAT per sandbox (which would
  defeat the cost rationale entirely).
- **EIP quota headroom.** The default Elastic IP quota is 5 per region. Four NAT EIPs (one per
  AZ) fit comfortably but leave only one spare — be mindful before provisioning additional
  Elastic IPs for other purposes in the same region.

## Scope fences — do not re-open these

- **EFS needs no change.** There is at most one mount target per AZ; a private-subnet instance
  in a given AZ reaches the existing mount target for that same AZ, which already lives in the
  public subnet. `infra/live/use1/efs/terragrunt.hcl` is untouched.
- **SSM needs no change.** The SSM agent dials outbound; `km shell` / `km vscode` /
  `km desktop` port-forwards work unmodified over NAT — no inbound reachability was ever
  required.
- **Deferred, not delivered here:** VPC interface/gateway endpoints (a real reduction in NAT
  data-processing cost — a natural follow-up phase), IPv6/egress-only IGW, cross-region NAT,
  and migrating an already-running sandbox from public to private placement.

## REQ-125-SUBPIN — the per-substrate module version pin

As part of this phase, `infra/templates/sandbox/terragrunt.hcl`'s single shared module-version
literal was restructured into a `locals.substrate_module_versions` map, looked up per substrate
(`ec2spot` → `v1.3.0`, `ecs` → `v1.0.0`) instead of one hardcoded string shared by both. This
incidentally repairs a pre-existing, latent bug: the old shared literal pointed the ECS
substrate at a module directory that did not exist on disk
(`infra/modules/ecs/v1.2.0/` was never created).

**Scope boundary — read carefully.** This phase fixed the version **pin** only. The ECS
substrate itself is **not in use** and has **never been tested** in this install. Fixing the
pin means `terraform init` no longer fails on a nonexistent module path for a hypothetical ECS
sandbox; it does **not** mean ECS sandbox creation works end to end. That remains unproven and
out of scope for this phase.

## Residual risk — ttl-handler destroy across a module-version boundary

`cmd/ttl-handler/main.go` renders its destroy-placeholder `main.tf` against a **frozen**
`ec2spot/v1.0.0` pin, unconditionally, regardless of which version a sandbox was actually
created against. This phase's `ec2spot/v1.3.0` renames `public_subnets` to `sandbox_subnets`
and adds `associate_public_ip` — so an automatic TTL expiry of a v1.3.0-created sandbox drives
`terraform destroy` off a v1.0.0-shaped config while operating on v1.3.0-shaped state.

`RESEARCH.md` Finding 1 resolves this analytically at **HIGH confidence**: `terraform destroy`
operates on existing state, not on the destroy-time config's variable names, and this same
v1.0.0 pin has already survived two prior version bumps (v1.1.0, v1.2.0) without incident. The
ordinary `km destroy` path does **not** exercise this at all — it reuses the sandbox's original
create-time `terragrunt.hcl`, so it never crosses module versions — which is why only a live,
Lambda-driven TTL expiry proves the drift path.

If UAT step 6 (the live ttl-handler auto-destroy test) has not yet been exercised against real
AWS, treat this as an **accepted residual risk**, not a proven guarantee: the failure mode
would be an orphaned EC2 instance, ENI, and EBS volume per expired private sandbox — billed
indefinitely and invisible to `km list` (which reads the DynamoDB row, not live EC2 state).
Check `.planning/phases/125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway/125-UAT.md`
for whether step 6 was exercised and its outcome before relying on unattended TTL expiry for a
private sandbox in production.

## See also

- `docs/operational-gotchas.md` § AZ failover + capacity feasibility (Phase 124) — the AZ
  sweep this phase's per-AZ NAT integrates with.
- `.planning/phases/125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway/125-UAT.md`
  — the live UAT record for this phase.
