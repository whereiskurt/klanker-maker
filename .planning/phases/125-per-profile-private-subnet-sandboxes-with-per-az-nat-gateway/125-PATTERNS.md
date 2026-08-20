# Phase 125: Per-profile private-subnet sandboxes with per-AZ NAT gateways - Pattern Map

**Mapped:** 2026-08-19
**Files analyzed:** 21
**Analogs found:** 20 / 21 (1 flagged "no close analog" — the module-version-bump
regression test needs adaptation, not a literal copy)

This map builds on RESEARCH.md's file:line anchors — it does not repeat them, it
adds **the closest existing analog for each new artifact**, with excerpts to copy
from directly.

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|---|---|---|---|---|
| `infra/modules/network/v1.1.0/{main,variables,outputs}.tf` | terraform module (new version dir) | CRUD (provision NAT/EIP/route infra) | `infra/modules/ec2spot/v1.2.0/` (created from v1.1.0 by commit `5d8fa00d`) | role-match (module-bump precedent, different resource domain) |
| `infra/modules/ec2spot/v1.3.0/{main,variables}.tf` | terraform module (new version dir) | CRUD (provision compute) | `infra/modules/ec2spot/v1.2.0/` (created from v1.1.0, commit `5d8fa00d`) | exact (same module lineage) |
| `infra/live/use1/network/terragrunt.hcl` | terragrunt live unit | config (env→tfvar) | `infra/live/use1/lambda-github-bridge/terragrunt.hcl` (`get_env` pattern) — **but note the bool-typing gotcha below** | role-match |
| `infra/templates/sandbox/terragrunt.hcl` | terragrunt template (verbatim-copied) | config (version pin) | itself, prior bump commits `ef51664d` (v1.0.0→v1.1.0), `b9cedd8c` (v1.1.0→v1.2.0) | exact |
| `internal/app/cmd/init.go` (`NetworkOutputs`, `LoadNetworkOutputs`, env export+drift-WARN, display) | config/orchestration | request-response (terraform outputs → Go struct → env) | Phase 96/101 `DefaultRouter` chain (same file) | exact |
| `internal/app/cmd/create.go` (NetworkConfig construction x2, fail-fast guard, sweep) | controller/orchestration | request-response + retry-loop | `mountEFS` fail-fast guard (create.go:643-645); Phase 124 AZ sweep (already in this file) | exact |
| `internal/app/cmd/budget.go:365-379` | controller/orchestration | request-response | `create.go` NetworkConfig construction (3rd near-duplicate site) | exact |
| `pkg/capacity/rankaz.go` | service (decision logic) | transform | itself (Step 1 offered-AZ filter, existing GPU-quota Step 2 gate) | exact |
| `pkg/compiler/service_hcl.go` | service (template compiler) | transform | itself (`EnableBedrock`/`HibernationEnabled` bool-threading precedent) | exact |
| `pkg/compiler/compiler_test.go` | test | — | itself (existing `TestCompileEC2ServiceHCL` substring check) | exact |
| new test mirroring `TestSandboxTemplateUsesEC2SpotV120` | test | — | `pkg/compiler/compiler_secrets_test.go` (commit `b9cedd8c`) | role-match (needs adaptation — see "No Analog Found") |
| `internal/app/config/config.go` (`NetworkConfig` struct + load + merge-list) | config | CRUD (yaml→struct) | `SlackConfig.DefaultRouter` / `GithubConfig.DefaultRouter` (Phase 96/101) | exact |
| `internal/app/config/config_test.go` | test | — | `TestLoadSlackDefaultRouter_{True,False,Absent,MergeListRegression}` | exact |
| `pkg/profile/types.go` (`NetworkSpec.PrivateSubnet`) | model (schema struct) | CRUD | `NetworkSpec.HTTPSOnly bool` (sibling field, same struct) | exact |
| `pkg/profile/schemas/sandbox_profile.schema.json:444-452` | config (JSON schema) | request-response (validation) | sibling `httpsOnly`/`enforcement` properties in the same `network` object | exact |
| `pkg/aws/metadata.go` (`SandboxMetadata.NetworkPlacement`) | model | CRUD | `SlackReactAlways *bool` / `SlackAllow []string` (Phase 91.5/118) | exact |
| `pkg/aws/sandbox_dynamo.go` (marshal/unmarshal chokepoint) | service (DDB mapper) | CRUD | `marshalSandboxItem` line 520-521 (`SlackReactAlways`), `unmarshalSlackFields` line 293-325 | exact |
| new create-time writer for `network_placement` | service | event-driven (fire-once at create) | `internal/app/cmd/create_slack_inbound.go` (`slackInboundDeps`, `UpdateSandboxAttr` DI pattern) | role-match |
| `internal/app/cmd/doctor_network.go` + `_test.go` | controller (doctor check) | request-response | `internal/app/cmd/doctor_slack.go` (`checkSlackPeerBridges`) + `doctor_slack_peer_bridges_test.go` | exact |
| `km init` "refuse NAT-disable while private sandboxes running" guard | controller (destructive-change guard) | request-response | `internal/app/cmd/uninit.go:304-345` (`RunUninitWithDeps` active-sandbox-count refuse) | exact |
| `cmd/ttl-handler/main.go:1215` | — | — | N/A — **investigation only, no code change expected** (see Finding 1) | n/a |

---

## Pattern Assignments

### 1. `infra/modules/network/v1.1.0/` (terraform module, CRUD)

**No prior *network*-module version bump exists** (network has only ever had
`v1.0.0`). The closest real analog is **how ec2spot itself was bumped
v1.1.0→v1.2.0**, which is a 2-commit pattern worth replicating exactly:

**Commit 1 — new version dir, additive only** (`5d8fa00d feat(89-02): bump
ec2spot to v1.2.0 with SOPS secrets IAM policies`):
```
 infra/modules/ec2spot/v1.2.0/main.tf      | 801 ++++++++++++++++++++++++++++++
 infra/modules/ec2spot/v1.2.0/outputs.tf   |  42 ++
 infra/modules/ec2spot/v1.2.0/variables.tf | 168 +++++++
 3 files changed, 1011 insertions(+)
```
Commit message convention: `feat(<phase>-<plan>): bump <module> to v<X.Y.Z> with <summary>`
— note the body explicitly states *what's new* and *what's untouched*
(`"v1.1.0 untouched"`), which is exactly CONTEXT.md's "never edit a released
version in place" rule made visible in commit history.

**Commit 2 — repoint the ONE place that resolves the version, same day**
(`b9cedd8c feat(89-05): bump sandbox module from ec2spot/v1.1.0 to v1.2.0 +
compiler emission tests`):
```
 infra/templates/sandbox/terragrunt.hcl |   2 +-
 pkg/compiler/compiler_secrets_test.go  | 118 +++++++++++++++++++++++++++++++++
 2 files changed, 119 insertions(+), 1 deletion(-)
```
```diff
-  source = "${local.repo_root}/infra/modules/${local.svc_config.locals.substrate_module}/v1.1.0"
+  source = "${local.repo_root}/infra/modules/${local.svc_config.locals.substrate_module}/v1.2.0"
```
**Phase 125 difference (per RESEARCH Finding 1):** this single literal must
become a **per-substrate lookup** (`ec2spot` → v1.3.0, `ecs` → v1.0.0), not a
second single-string replace — the network module bump doesn't touch this file
at all (network isn't consumed via `substrate_module`), only the ec2spot bump
does.

**What to change:** copy `infra/modules/network/v1.0.0/{main,variables,outputs}.tf`
verbatim into a new `v1.1.0/` dir, then apply the `count`-ification + `moved`
blocks + plural outputs CONTEXT.md already specifies (Finding 6 in RESEARCH.md
has the exact `moved` syntax). No analog needed for the Terraform mechanics
themselves — CONTEXT.md + RESEARCH.md Finding 6 are already concrete.

---

### 2. `infra/modules/ec2spot/v1.3.0/` — new optional bool var threading

**Exact existing pattern to copy: `enable_bedrock` (bool, HCL var → main.tf →
service_hcl.go template → Go struct → compile-time assignment).**

**Module variable** (`infra/modules/ec2spot/v1.2.0/variables.tf:83-87`):
```hcl
variable "enable_bedrock" {
  type        = bool
  description = "Whether to attach Bedrock IAM policy to the sandbox role. Set to false with --no-bedrock."
  default     = true
}
```
A second, even closer bool-var precedent for a **safety-relevant** flag with a
default (`hibernation_enabled`, `variables.tf:95-99`):
```hcl
variable "hibernation_enabled" {
  type        = bool
  description = "Enable EC2 hibernation (on-demand only, requires encrypted root volume)"
  default     = false
}
```

**Consumption in `main.tf`** — `enable_bedrock` gates a `count`
(`main.tf:308`):
```hcl
count = local.total_ec2spot_count > 0 && var.enable_bedrock ? 1 : 0
```
`hibernation_enabled` is consumed as a direct resource attribute
(`main.tf:705`, `716`, `722`):
```hcl
hibernation = var.hibernation_enabled
```
**For `associate_public_ip`, the direct target is even simpler** — it's a 1:1
replace of the two hardcoded literals (`main.tf:603`, `main.tf:713`):
```hcl
associate_public_ip_address = true
```
→
```hcl
associate_public_ip_address = var.associate_public_ip
```
with a new var block mirroring `enable_bedrock`'s shape:
```hcl
variable "associate_public_ip" {
  type        = bool
  description = "Attach a public IPv4 to the launched instance. Must be false in a private subnet — public IPv4 is unroutable there and billable (~$0.005/hr since Feb 2024)."
  default     = true
}
```

**`public_subnets` var rename target** (`variables.tf:50-54`):
```hcl
variable "public_subnets" {
  type        = list(string)
  description = "List of public subnet IDs. If empty, subnets are created in the per-sandbox VPC."
  default     = []
}
```
and its one consumer (`main.tf:98`):
```hcl
effective_subnets = length(var.public_subnets) > 0 ? var.public_subnets : aws_subnet.sandbox[*].id
```
→ rename both the var and the local to `sandbox_subnets`
/`local.effective_subnets` stays the same name (it's already
substrate-neutral).

**Go-side threading (service_hcl.go), the exact chain to copy:**

Template literal (`service_hcl.go:135`):
```
    enable_bedrock = {{ .EnableBedrock }}
```
Struct field (`service_hcl.go:498`):
```go
	// Bedrock access control
	EnableBedrock bool // true unless --no-bedrock flag is set
```
Assignment at compile time (`service_hcl.go:787`):
```go
	params := ec2HCLParams{
		EnableBedrock:     enableBedrock(p),
```
**For the ec2spot rename**, the corresponding three sites are already
identified by RESEARCH.md Finding 2/5 (`service_hcl.go:105/486/796`
`PublicSubnets`→`SandboxSubnets`, `ecsHCLParams`/`ecsServiceHCLTemplate` at
`222/548/1019` **must NOT change**). Follow the exact same
template-line→struct-field→assignment shape shown above for `enable_bedrock`,
substituting the new field name.

**Test to update** (`pkg/compiler/compiler_test.go` ~line 144) — this is the
existing regression guard for the ec2spot template's `module_inputs`
substring, already flagged by RESEARCH Finding 2; no new analog needed beyond
what's already anchored there.

---

### 3. `network.nat_gateway` km-config.yaml knob — full 6-touchpoint chain

**Analog: `slack.default_router` (field-by-field `*bool` pattern, Phase 96) —
this is the pattern to copy verbatim**, because `network:` is a **brand-new**
top-level block (no existing `UnmarshalKey("network", …)` call to piggyback
on, unlike `github`/`h1`/`checks`/`limits`).

**1. Struct field** (`internal/app/config/config.go:56-66`, on `SlackConfig`):
```go
	// DefaultRouter is the Phase 96 front-door router toggle. Tri-state via *bool:
	//   nil    → key absent from yaml; router off (Phase 95 byte-identical)
	//   &true  → router on: ...
	//   &false → router explicitly off (same as nil but operator-visible in yaml)
	// Maps to km-config.yaml key slack.default_router. Exported as
	// KM_SLACK_DEFAULT_ROUTER by ExportTerragruntEnvVars (Phase 96).
	DefaultRouter *bool `mapstructure:"default_router" yaml:"default_router,omitempty"`
```
Copy this shape onto a **new** `NetworkConfig` struct:
```go
type NetworkConfig struct {
	// NATGateway is the Phase 125 install-level NAT toggle. Tri-state via *bool:
	//   nil    → key absent from yaml; NAT off (Phase 124 byte-identical)
	//   &true  → NAT gateway + EIP infra exists (per-AZ)
	//   &false → NAT explicitly off
	// Maps to km-config.yaml key network.nat_gateway. Exported as
	// KM_NAT_GATEWAY_ENABLED by ExportTerragruntEnvVars.
	NATGateway *bool `mapstructure:"nat_gateway" yaml:"nat_gateway,omitempty"`
}
```
and add `Network NetworkConfig \`mapstructure:"network"\`` to the parent
`Config` struct (find the parent struct's existing `Slack SlackConfig`
field-declaration site and mirror it — not independently re-anchored here,
low-risk mechanical addition).

**2. Merge-list entry (CRITICAL — the documented silent-drop footgun)**
(`config.go:896-897`):
```go
			// Phase 96: front-door router toggle. CRITICAL: without this entry,
			// slack.default_router: true is silently ignored (project_config_key_merge_list).
			"slack.default_router",
```
Add `"network.nat_gateway",` with an identical comment, citing
`project_config_key_merge_list` (per this repo's own memory of the footgun).

**3. Load logic** (`config.go:1033-1035`):
```go
	if v.IsSet("slack.default_router") {
		val := v.GetBool("slack.default_router")
		cfg.Slack.DefaultRouter = &val
	}
```
Copy verbatim as:
```go
	if v.IsSet("network.nat_gateway") {
		val := v.GetBool("network.nat_gateway")
		cfg.Network.NATGateway = &val
	}
```

**4. Env export + drift WARN** (`internal/app/cmd/init.go:1657-1669`):
```go
	if cfg.Slack.DefaultRouter != nil {
		yamlSlackDefaultRouter := strconv.FormatBool(*cfg.Slack.DefaultRouter)
		if envVal := os.Getenv("KM_SLACK_DEFAULT_ROUTER"); envVal != "" && envVal != yamlSlackDefaultRouter {
			fmt.Fprintf(os.Stderr, "WARN: KM_SLACK_DEFAULT_ROUTER=%s (env) overrides km-config.yaml slack.default_router=%s\n", envVal, yamlSlackDefaultRouter)
		} else if envVal == "" {
			os.Setenv("KM_SLACK_DEFAULT_ROUTER", yamlSlackDefaultRouter) //nolint:errcheck
		}
	}
```
Copy verbatim for `KM_NAT_GATEWAY_ENABLED` / `cfg.Network.NATGateway` /
`network.nat_gateway`.

**5. Terragrunt `get_env` consumption — THIS IS THE ONE STEP THAT DOES NOT
TRANSFER 1:1.** Every existing `get_env`-sourced toggle in this repo flows
into a Lambda `environment.variables` string map (e.g.
`infra/live/use1/lambda-github-bridge/terragrunt.hcl:100`:
`github_default_router = get_env("KM_GITHUB_DEFAULT_ROUTER", "false")`, no
coercion needed because Lambda env vars are always strings). `network.hcl`'s
`nat_gateway.enabled` is a **native `optional(bool, false)`-typed** Terraform
variable — the analog string-getter call must be wrapped:
```hcl
nat_gateway = {
  enabled = tobool(get_env("KM_NAT_GATEWAY_ENABLED", "false"))
}
```
`infra/live/use1/network/terragrunt.hcl` currently has **zero** `get_env`
calls (verified) — this will be the first one in that file.

**6. `km configure` — NOT part of this chain.** Confirmed by grep: neither
`slack.default_router` nor `github.default_router` appear in
`internal/app/cmd/configure.go` or `env.go`. `network.nat_gateway` is a
manually-added yaml key, same as its two exemplars — no wizard question
needed.

**Test analog** (`internal/app/config/config_test.go:1009-1108`, four
functions: `TestLoadSlackDefaultRouter_{True,False,Absent,MergeListRegression}`)
— copy this 4-test shape (not the `TestLoadGithubDefaultRouter_Set` shape,
which specifically demonstrates the *simpler*, no-extra-merge-entry path only
available to already-atomically-decoded top-level blocks like `github`).

---

### 4. `spec.network.privateSubnet` profile bool

**Go struct** (`pkg/profile/types.go:576-586`):
```go
// NetworkSpec controls egress network policy.
type NetworkSpec struct {
	Egress    EgressSpec `yaml:"egress"`
	HTTPSOnly bool       `yaml:"httpsOnly,omitempty"` // Block plain HTTP; on EC2 security groups enforce this, on Docker the proxy enforces it
	// Enforcement selects the network enforcement mechanism.
	// ...
	Enforcement string `yaml:"enforcement,omitempty"`
}
```
`HTTPSOnly bool` (plain, non-pointer) is the exact sibling-field analog —
default-false semantics, no tri-state needed (a profile field only ever has
one authoritative value, unlike an install-level yaml key that must
distinguish "absent" from "false"). Add:
```go
	// PrivateSubnet places the sandbox ENI in a private subnet behind a per-AZ
	// NAT gateway instead of a public subnet with a public IPv4. Requires
	// network.nat_gateway: true at the install level (checked at km create
	// time, not km validate — the profile itself is never wrong; only the
	// install's current NAT state can make it unsatisfiable). Default false =
	// byte-identical to Phase 124 (public subnet, associate_public_ip=true).
	PrivateSubnet bool `yaml:"privateSubnet,omitempty"`
```

**JSON schema** (`pkg/profile/schemas/sandbox_profile.schema.json:444-452`,
`spec.network` object, `additionalProperties: false`):
```json
            "httpsOnly": {
              "type": "boolean",
              "description": "Block plain HTTP traffic; on EC2 security groups enforce this, on Docker the proxy enforces it"
            },
            "enforcement": {
              "type": "string",
              "enum": ["proxy", "ebpf", "both"],
              "default": "proxy",
              "description": "Network enforcement mechanism: proxy (iptables DNAT, default), ebpf (cgroup BPF, EC2-only), or both (eBPF primary + proxy for L7 inspection, EC2-only)"
            }
```
Insert a sibling `"privateSubnet"` boolean property in the same shape as
`httpsOnly` (plain `type: boolean`, no `default` key needed since Go zero-value
already is `false`).

**`km validate` WARN — NOT NEEDED for this field**, and this is itself a
useful negative finding: CONTEXT.md is explicit that the NAT-state check
belongs at `km create` (AWS-backed, install-scoped), not `km validate`
(offline, profile-scoped) — "the profile is not wrong, the install state is."
The `km validate`-WARN pattern (e.g. `spec.notification.slack.private` +
`perSandbox` cross-field WARN at `pkg/profile/validate.go`, tested at
`validate_test.go:1651-1716`) is the right shape for a WARN **if** a
same-profile cross-field inconsistency existed, but `privateSubnet` has none
— it's a single independent bool. Do not add a validate-time check here.

---

### 5. `network_placement` per-sandbox DDB attribute — full round-trip chain

**Analog: `SlackReactAlways *bool` (Phase 91.5) / `SlackAllow []string`
(Phase 118) — the doc comment at `pkg/aws/metadata.go:65-73` already states
the exact failure mode to avoid, verbatim:**
```go
	// These MUST round-trip through marshal/unmarshal: every read-modify-write
	// path (resume.go TTL recreation, extend.go, the ttl-handler Lambda) PutItems
	// the whole row, so a struct that drops them silently reverts the sandbox to
	// install defaults on the next lifecycle write — observed as a sandbox losing
	// `mentionOnly: true` (and gaining 👀-on-everything) after a pause/resume.
	SlackMentionOnly *bool `json:"slack_mention_only,omitempty"`
	SlackReactAlways *bool `json:"slack_react_always,omitempty"`
```

**1. Struct field** — add to `SandboxMetadata` (`pkg/aws/metadata.go`, next
to the block above):
```go
	// NetworkPlacement is "private" or "public" — the subnet class the
	// sandbox's ENI landed in at km create time (Phase 125). Empty = pre-125
	// sandbox (implicitly public). Fixed for the sandbox's lifetime (placement
	// cannot change without recreate) but MUST still round-trip through
	// marshal/unmarshal — the km init NAT-disable refuse-guard depends on
	// finding this value on RUNNING rows, and a dropped field would silently
	// let the guard miss a genuinely-private sandbox.
	NetworkPlacement string `json:"network_placement,omitempty"`
```

**2. Marshal (write) chokepoint** — `pkg/aws/sandbox_dynamo.go:520-521`:
```go
	if meta.SlackReactAlways != nil {
		item["slack_react_always"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: *meta.SlackReactAlways}
	}
```
For a plain non-empty string, the closer analog is `SlackAllow`'s S-typed
sibling (`sandbox_dynamo.go:528-529`):
```go
	if len(meta.SlackAllow) > 0 {
		item["slack_allow"] = &dynamodbtypes.AttributeValueMemberS{Value: strings.Join(meta.SlackAllow, ",")}
```
→
```go
	if meta.NetworkPlacement != "" {
		item["network_placement"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.NetworkPlacement}
	}
```
Add this inside `marshalSandboxItem` (func starts at `sandbox_dynamo.go:433`).

**3. Unmarshal (read) chokepoint** — `unmarshalSlackFields`
(`sandbox_dynamo.go:293-325`) is the sibling function; `attrStr` (defined in
`doctor_ddb_rows.go:14-24`, but it's a `pkg/aws`-internal helper co-located in
`sandbox_dynamo.go` for the S-attribute read path — confirm the exact
same-package helper name at `sandbox_dynamo.go:174`'s `unmarshalSandboxItem`)
is the read counterpart to `AttributeValueMemberS`. Either extend
`unmarshalSlackFields` with one more line, or add a 3-line sibling
`unmarshalNetworkFields(item, meta)` called from both `toSandboxMetadata()`
(line 79) and `unmarshalSandboxItem` (line 174) — **this single chokepoint is
what makes resume.go/extend.go/ttl-handler automatically preserve the field**,
exactly as `SlackReactAlways` needed no dedicated resume.go/extend.go edit.

**4. Create-time writer** — closest analog is
`internal/app/cmd/create_slack_inbound.go`'s dependency-injection shape
(`slackInboundDeps` struct, `UpdateSandboxAttr func(ctx, sandboxID, attr,
value string) error` field) — but that file provisions a whole SQS queue,
which is heavier than needed here. **A lighter, more proportionate analog is
`UpdateSandboxStringAttrDynamo`** (`pkg/aws/sandbox_dynamo.go:1009`), a
generic single-string-attribute writer already used elsewhere in the
codebase — call it directly from `create.go` right after a successful create,
passing `"network_placement"` and `"private"`/`"public"`, rather than building
a new dedicated deps-injection file. Confirm the exact call signature at
`sandbox_dynamo.go:1009` before wiring (not independently re-verified this
session, but the function name and rough shape are confirmed to exist by the
`grep` in Finding 4/CONTEXT).

---

### 6. New `km doctor` check — mirror `checkSlackPeerBridges`

**Pure function + CheckResult analog** (`internal/app/cmd/doctor_slack.go:605-649`):
```go
func checkSlackPeerBridges(peerBridges []string, ownBridgeURL string) CheckResult {
	name := "Slack peer bridges"
	if len(peerBridges) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "slack.peer_bridges not set — federation off",
		}
	}
	var malformed, selfLoop []string
	for _, raw := range peerBridges {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Host == "" {
			malformed = append(malformed, raw)
			continue
		}
		if ownBridgeURL != "" && raw == ownBridgeURL {
			selfLoop = append(selfLoop, raw)
		}
	}
	if len(malformed) > 0 {
		return CheckResult{
			Name:   name,
			Status: CheckWarn,
			Message: fmt.Sprintf("malformed peer_bridges URL(s): %s — check km-config.yaml slack.peer_bridges",
				strings.Join(malformed, ", ")),
			Remediation: "Edit km-config.yaml slack.peer_bridges to list well-formed https:// /events URLs for each sibling km install. Run `km init --dry-run=false` to deploy the updated Lambda env.",
		}
	}
	// ... self-loop WARN, then OK
}
```
**Registration in `doctor.go`** (`doctor.go:4468-4481`):
```go
	checks = append(checks, func(ctx context.Context) CheckResult {
		var ownBridgeURL string
		if slackSSMStore != nil {
			ownBridgeURL, _ = slackSSMStore.Get(ctx, cfg.GetSsmPrefix()+"slack/bridge-url", false)
		}
		r := checkSlackPeerBridges(peerBridges, ownBridgeURL)
		if r.Status == CheckError {
			r.Status = CheckWarn
		}
		return r
	})
```
Follow this exact `checks = append(checks, func(ctx context.Context)
CheckResult { ... })` closure-registration shape for the two new checks
(NAT-idle, private-without-NAT).

**Data-source analog for the count-based checks** — both new WARNs need to
scan live sandbox metadata, filtered by a field, exactly like
`listSandboxesWithInboundImpl` (`doctor_slack.go:539-551`):
```go
func listSandboxesWithInboundImpl(ctx context.Context, client kmaws.SandboxMetadataAPI, tableName string) ([]inboundRow, error) {
	metas, err := kmaws.ListAllSandboxMetadataDynamo(ctx, client, tableName)
	if err != nil {
		return nil, fmt.Errorf("list sandbox metadata: %w", err)
	}
	var rows []inboundRow
	for _, m := range metas {
		if m.SlackInboundQueueURL == "" {
			continue
		}
		rows = append(rows, inboundRow{SandboxID: m.SandboxID, QueueURL: m.SlackInboundQueueURL})
	}
	return rows, nil
}
```
Copy this shape filtering on `m.NetworkPlacement == "private" && m.Status ==
"running"` for both new checks (NAT-idle = zero such rows while
`cfg.Network.NATGateway` is true; private-without-NAT = one-or-more such rows
while NAT is false/nil).

**Test file structure to mirror** (`doctor_slack_peer_bridges_test.go:1-15`):
```go
package cmd

import (
	"strings"
	"testing"
)

// TestCheckSlackPeerBridges exercises all return paths of checkSlackPeerBridges ...
func TestCheckSlackPeerBridges(t *testing.T) {
	tests := []struct {
		name        string
		peerBridges []string
		ownBridgeURL string
		wantStatus  CheckStatus
		wantMsgSub  string
		wantRemSub  string
	}{ /* table cases */ }
	// ...
}
```
Table-driven, one `_test.go` per doctor concern — create
`doctor_network.go` + `doctor_network_test.go` (not folded into an existing
file), matching the one-file-per-concern convention (`doctor_ddb_rows.go`,
`doctor_ebs.go`, `doctor_slack_transcript.go`, etc.).

---

### 7. Fail-fast guard in `km create` (privateSubnet without NAT)

**Exact analog — `mountEFS` fail-fast** (`internal/app/cmd/create.go:634-645`):
```go
	// Step 6a-efs: Load EFS outputs for shared filesystem mount (Phase 43).
	// Only applies to non-docker substrates; docker does not support EFS mounts.
	if substrate != "docker" {
		efsID, err := LoadEFSOutputs(repoRoot, regionLabel)
		if err != nil {
			return fmt.Errorf("failed to load EFS outputs for %s: %w", regionLabel, err)
		}
		network.EFSFilesystemID = efsID

		// Validate: profile requests EFS mount but EFS not initialized.
		if resolvedProfile.Spec.Runtime.MountEFS && efsID == "" {
			return fmt.Errorf("profile requests mountEFS but EFS is not initialized for region %s — run 'km init --region %s' first", regionLabel, region)
		}
	}
```
This is a near-perfect structural template: profile-level bool requesting
infra that may or may not exist at the install level, checked immediately
after loading the relevant outputs, failing with the exact config key + fix
command. Adapt directly:
```go
	if resolvedProfile.Spec.Network.PrivateSubnet && len(networkOutputs.PrivateSubnets) == 0 {
		return fmt.Errorf("profile requests spec.network.privateSubnet but NAT gateway infrastructure is not enabled for region %s — set network.nat_gateway: true in km-config.yaml and run 'km init --dry-run=false'", region)
	}
```
Insertion point: right after `LoadNetworkOutputs` at `create.go:615-624`
(local site) and the mirrored `--remote` site at `create.go:2415-2432` —
RESEARCH.md Finding 5 already anchors both call sites for the
`compiler.NetworkConfig{...}` literal; add the guard immediately after each.

**Sibling analog worth noting for style** — the network-outputs-missing guard
one line above the EFS one (`create.go:615-617`):
```go
		networkOutputs, err := LoadNetworkOutputs(repoRoot, regionLabel)
		if err != nil {
			return fmt.Errorf("failed to load network config for %s: %w\nRun 'km init --region %s' first", region, err, region)
		}
```
Same "load, then check, then actionable message naming the exact `km init`
command" shape.

---

### 8. `km init` refuse-to-disable-NAT guard (secondary, Claude's Discretion on placement)

**Exact analog — `uninit.go`'s active-sandbox-count refuse**
(`internal/app/cmd/uninit.go:304-345`):
```go
	if lister != nil && !opts.Force {
		records, err := lister.ListSandboxes(ctx, false)
		if err != nil {
			return fmt.Errorf("failed to list sandboxes (use --force to skip this check): %w", err)
		}
		activeCount := 0
		for _, r := range records {
			if r.Region == region && r.Status == "running" {
				activeCount++
			}
		}
		if activeCount > 0 {
			return fmt.Errorf(
				"%d active sandbox(es) found in region %s — destroy them first or use --force to proceed anyway",
				activeCount, region,
			)
```
Adapt for `km init`: list sandboxes, filter `Status == "running" &&
NetworkPlacement == "private"`, collect their IDs (CONTEXT.md explicitly
requires "listing the sandbox ids", one step more specific than uninit's bare
count), and refuse the apply when `cfg.Network.NATGateway` is being flipped
`false`. This check has no existing `--force`-style escape hatch specified in
CONTEXT.md — do not add one unless the plan calls for it (destroy the private
sandboxes first is the only documented recovery path, mirroring "destroy them
first" above but without the override flag, since silently orphaning private
sandboxes' egress is worse than a blocked uninit).

---

## Shared Patterns

### Config toggle plumbing (install-level bool)
**Source:** `internal/app/config/config.go` `SlackConfig.DefaultRouter` chain
(struct field → merge-list entry → `IsSet`/`GetBool` load → env export w/
drift WARN). **Apply to:** `network.nat_gateway`.

### Profile-level plain bool (per-sandbox)
**Source:** `pkg/profile/types.go` `NetworkSpec.HTTPSOnly`. **Apply to:**
`spec.network.privateSubnet`.

### SandboxMetadata lossy-round-trip avoidance
**Source:** `pkg/aws/metadata.go:65-73` doc comment + `sandbox_dynamo.go`
marshal/unmarshal chokepoint (`marshalSandboxItem` / `unmarshalSlackFields`).
**Apply to:** `network_placement`. This is the single highest-value shared
pattern in this phase — RESEARCH.md's Pitfall 5 and this repo's own memory
(`project_sandboxmetadata_lossy_roundtrip`) both flag it as a repeat offender.

### Doctor check registration
**Source:** `doctor.go:4468-4481` closure-append pattern +
`doctor_slack.go:605-649` pure-function `CheckResult` shape + one
`doctor_*.go`/`doctor_*_test.go` file pair per concern. **Apply to:**
`doctor_network.go`.

### Fail-fast precondition guard in `km create`
**Source:** `create.go:643-645` (`mountEFS`) and `create.go:617`
(network-outputs-missing). **Apply to:** the `privateSubnet`-without-NAT
guard — both existing guards name the exact `km init` fix command, which
CONTEXT.md explicitly requires this new guard to do too.

### Module version bump (2-commit shape)
**Source:** commits `5d8fa00d` (new version dir, additive) +
`b9cedd8c` (repoint the ONE consumer + add a regression test). **Apply to:**
both `network` v1.0.0→v1.1.0 and `ec2spot` v1.2.0→v1.3.0 — plan these as two
separable commits/tasks each, matching the repo's own history.

---

## No Analog Found

| File | Role | Data Flow | Reason |
|---|---|---|---|
| New test mirroring `TestSandboxTemplateUsesEC2SpotV120` (`compiler_secrets_test.go:34-53`) | test | — | The existing test does a single-literal substring check (`strings.Contains(content, "/v1.2.0")`, `!strings.Contains(content, "/v1.1.0")`). Phase 125's per-substrate lookup (Finding 1) means this single-literal shape no longer fits — the new test must assert the lookup resolves `ec2spot`→`v1.3.0` **and** `ecs`→`v1.0.0` separately (e.g. by rendering/parsing the new locals map, or by two independent substring checks against the per-substrate values if the lookup stays a flat HCL map literal). **Flag for the planner:** design this test alongside the per-substrate lookup structure, not as a copy-paste of the existing one. |
| `km init` NAT-disable refuse-guard's exact call site | controller | — | No prior `km init` flow refuses an apply based on **live DDB sandbox state** (uninit does refuse based on live state, but it's a *different command* with a *different destructive action*). This is a genuinely new integration point in `init.go`'s apply flow — RESEARCH.md and CONTEXT.md both leave "exact placement" to discretion; recommend inserting it as a pre-apply check on the `network` module specifically (mirroring how the destroy-class gate in `km init --plan` is module-scoped), not a blanket `km init` gate. |

## Metadata

**Analog search scope:** `infra/modules/{network,ec2spot}/*`,
`infra/live/use1/{network,lambda-github-bridge}/terragrunt.hcl`,
`infra/templates/sandbox/terragrunt.hcl`, `internal/app/config/config.go{,_test.go}`,
`internal/app/cmd/{init,create,budget,uninit,doctor,doctor_slack,create_slack_inbound}.go`
+ their `_test.go` files, `pkg/compiler/{service_hcl,compiler_test,compiler_secrets_test}.go`,
`pkg/profile/{types,validate,schemas/sandbox_profile.schema.json}`,
`pkg/aws/{metadata,sandbox_dynamo}.go`, git log for `infra/templates/sandbox/terragrunt.hcl`
and `infra/modules/ec2spot/v1.2.0/*`.
**Files scanned:** ~30 (direct reads) + git history for 2 files.
**Pattern extraction date:** 2026-08-19
