# Phase 126: Cross-Account Capacity Borrowing — Research

**Researched:** 2026-08-22
**Domain:** Terragrunt cross-account provider generation, aws-sdk-go-v2 AssumeRole, cross-account IAM containment, DynamoDB schema-on-write metadata threading
**Confidence:** HIGH (the single highest-value question was resolved by executing the exact pinned `terragrunt v0.99.1` binary against reproduction fixtures, not by reading docs)

## Summary

This phase has one document that already carries almost all of the locked architecture: `docs/superpowers/specs/2026-06-29-cross-account-gpu-launch-design.md`, re-verified 2026-08-22 against `6b682f7e` with five corrections (C1–C5). This research does not re-litigate that spec's decisions — it (a) puts the single highest-risk claim in the spec (C1's provider-override mechanism) under an actual, live terragrunt binary and finds it **incomplete as written**, (b) traces four more codebase precedents the spec gestures at but doesn't fully resolve (the SCP two-phase bootstrap credential model, the cold-clone teardown's account-blindness, the capacity-store key collision, and `km account add`'s own Terraform state backend, which the spec never addresses), and (c) confirms the exact idiomatic Go shape for the one genuinely new capability (`stscreds.AssumeRoleProvider`).

**The load-bearing finding:** C1 proposes a child `generate "provider"` block in `infra/templates/sandbox/terragrunt.hcl` that overrides the one it inherits from `include "root"`. Tested directly against the repo's pinned `terragrunt 0.99.1` binary (`/opt/homebrew/bin/terragrunt`, confirmed via `terragrunt --version` to be the exact version this repo bundles per `.goreleaser.yaml`), **the default merge behavior does not silently override — it hard-fails every terragrunt render** with `Detected generate blocks with the same name: [provider]`, for every sandbox, not just cross-account ones. The fix is one line — add `merge_strategy = "deep"` to the `include "root"` block — which was verified, empirically, to (a) let the child's `generate "provider"` fully replace root's (confirmed via `terragrunt render --json`), (b) correctly interpolate a `local` sourced via `read_terragrunt_config(service.hcl)` into the generated HCL's conditional `assume_role` block, and (c) leave the sandbox template's existing `remote_state` full-override (already in production) byte-for-byte unaffected. This is scoped to the sandbox template only — `merge_strategy` is set per-`include`-block in the child file, and a repo-wide grep confirms 40+ other units `include "root"` without setting it, none of which are touched by this change.

**Primary recommendation:** REQ-126-LAUNCH's provider-override mechanism is C1 plus one addition: `merge_strategy = "deep"` on `infra/templates/sandbox/terragrunt.hcl`'s `include "root"` block. Everything else in C1 — the conditional `%{ if }` HCL templating, reading `launch_account` out of `service.hcl` via `read_terragrunt_config`, leaving the block emitting root's unchanged provider when the local is empty — is proven correct by direct execution and needs no further validation before planning. Two structurally separate gaps the spec did not resolve — `km account add`'s own Terraform state backend, and the cold-clone teardown path's account-blind resource discovery — both have concrete, evidence-backed resolutions below and should be folded into Wave 2 / Wave 5 respectively.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Launcher/box role + boundary + network + results bucket (B) | IAM/Infra (Terraform, applied against B) | — | Pure infra-as-code in the target account; no application-tier logic |
| `km account add` / `register` / `list` / `rm` | Operator CLI (`internal/app/cmd`) | Terragrunt runner (`pkg/terragrunt`) | New Cobra command family, mirrors `km cluster` |
| `launch_accounts` config + `RuntimeSpec.LaunchAccount` | Config/Schema (`internal/app/config`, `pkg/profile`) | — | Declarative link registry + opt-in profile field, both are pure data |
| Provider `assume_role` generation | Terragrunt config (`infra/templates/sandbox/terragrunt.hcl`) | Compiler (`pkg/compiler` writes the `service.hcl` local) | HCL-level cross-account targeting; compiler only supplies the value |
| Capacity/quota check in B | Operator CLI (`internal/app/cmd/create.go`, `capacity.go`) | `pkg/capacity`, new `pkg/aws` AssumeRole helper | Business logic lives in Go; B-scoped `aws.Config` is a data dependency |
| Capacity DynamoDB store (namespaced) | `pkg/capacity` (store construction) | DynamoDB (A only) | Store stays home; namespacing is a constructor-time concern, not a network concern |
| Cross-account teardown (destroy + ttl-handler) | Operator CLI / Lambda (`destroy.go`, `cmd/ttl-handler`) | `pkg/aws` (SandboxMetadata) | Both need to know *which* account before they can reach it — that's a DynamoDB metadata concern, not a Terraform concern |
| `km doctor` cross-account checks | Operator CLI (`doctor.go`) | — | Read-only health checks, same tier as every other doctor check |

## Project Constraints (from CLAUDE.md)

These are binding, not optional, for whatever plan comes out of this research:

- **Deploy-surface discipline is the single most error-prone part of this repo.** For Phase 126 specifically: `km account add/register/list/rm` + the profile field + create-flow changes are **operator-binary-only** (`make build`); the `launchAccount` schema field reaching **remote** create requires `make build-lambdas` + `km init --dry-run=false` (the create-handler Lambda's bundled `toolchain/km` must be refreshed — `[[project_schema_change_requires_km_init]]`); B-side roles/network are provisioned by `km account add` running terragrunt directly against B, **not** by `km init` (standalone, like `km cluster add`). Do not conflate these three deploy paths.
- **`make build` before `km init`, always**, when a phase adds a `regionalModules()` entry or changes what the operator binary emits (`[[project_make_build_precedes_km_init]]`) — this phase doesn't add a DynamoDB table, but it DOES change what `create.go`/`capacity.go` emit into terragrunt inputs, so the ordering still applies to any remote-create testing.
- **New km-config.yaml keys (`launch_accounts`) MUST be added to the v2→v merge-list in `config.Load()`** or the value is silently ignored (`[[project_config_key_merge_list]]` — this exact footgun has bitten `slack.mention_only`, `slack_channels_table_name`, and `network.nat_gateway` in prior phases; the merge-list entry is not optional).
- **Any new per-sandbox DynamoDB attribute (`launch_account`-style) must round-trip through the shared marshal/unmarshal chokepoint** (`pkg/aws/metadata.go`) or resume/extend/pause silently strip it (`[[project_sandboxmetadata_lossy_roundtrip]]`). Phase 125's `NetworkPlacement` field is the exact precedent to copy, including its dedicated round-trip test file.
- **`km destroy` flags:** use `--remote --yes`, never `--force` (`[[feedback_destroy_flags]]`) — relevant to any UAT script this phase's plan writes.
- **Rebuild `km` with `make build` after every source edit**, not bare `go build` (`[[feedback_rebuild_km]]`) — the VERSION-file dev-build counter and GitCommit stamp matter for later "which binary is this" debugging.
- **`km init --dry-run=false` for real deploys, never `km init --sidecars` when an env-block/IAM/schema change is involved** (`[[feedback_km_init_full_apply]]`).
- **Terragrunt providers belong in `root.hcl`, not in individual modules' `required_providers` blocks** (`[[project_terragrunt_providers_in_root]]`) — the new sandbox-template `generate "provider"` override is the one deliberate, already-precedented exception to this rule (SCP and s3-replication already do it); it must not be read as license to add ad hoc provider blocks elsewhere.
- **Any command that runs terragrunt must call `ExportConfigEnvVars(cfg)`** first or non-default `resource_prefix` installs 403 (`[[project_terragrunt_env_export]]`) — applies to the new `km account add`/`register` commands.
- **The Plan-before-apply destroy-class gate** (`km init --plan` / `--i-accept-destroys`) exists for protected resource types (SES, Route53, S3, DynamoDB, KMS). It is unlikely to fire for this phase's home-account changes (one S3 bucket-policy statement append, one merge-list entry) but the plan should note it applies if `km account rm`'s A-side cleanup ever touches the bucket policy in a way that looks like a broader change.
- **Terragrunt-with-`dependency` blocks needs `"show"` in `mock_outputs_allowed_terraform_commands`** (`[[project_terragrunt_show_needs_mocks]]`) — relevant only if the new `account-links` unit ever adds a `dependency` block (the recommended shape below does not).

## Project Skills

No `.claude/skills/` directory exists in this repo; the relevant project skill is `skills/cluster/SKILL.md` (`klanker:cluster`), read in full below as the cross-account enrollment precedent. `skills/init/SKILL.md` documents the `--only <module>` scoped-apply fast path, relevant to `km account register`'s A-side S3 bucket-policy grant if it's ever folded into a scoped init instead of a bespoke command (see Pattern 3).

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| REQ-126-CFG | `launch_accounts` config block + merge-list entry; `RuntimeSpec.LaunchAccount` + JSON schema; `km validate` rejects unknown link name and `launchAccount`+`privateSubnet` together | Pattern 6 (map-shaped config precedent: `github.commands`/`h1.events`), Pitfall 4 (`ValidateSemantic` has no config parameter — three call sites enumerated), exact schema/merge-list insertion points identified |
| REQ-126-ENROLL | `km account add` (B creds): launcher + box role + boundary + results bucket + optional EFS; trust policy names all three A-side principals; `--provision-network` provisions one subnet per AZ | Pattern 2 (two-phase bootstrap — the SCP precedent's *actual* credential mechanics, traced through `bootstrap.go`), Pattern 3 (the state-backend gap the spec never addresses — concrete recommendation), Pattern 7 (three-principal trust list is already a proven wildcard-ARN-pattern in `BuildSCPPolicyFromPrefix`) |
| REQ-126-REGISTER | `km account register`/`list`/`rm` (A creds): writes link, appends read-only artifacts grant | Confirmed as a pure A-side, no-cross-account-credential operation; no new findings needed beyond Pattern 6 |
| REQ-126-LAUNCH | Conditional `generate "provider"` override in the sandbox template; network resolution at both `create.go:688-709` and `:2528-2552` | **Pattern 1 — the empirically-verified mechanism, including the required `merge_strategy = "deep"` addition C1 omits.** Both create-flow copies read; structurally confirmed to already carry the Phase 125 pattern this phase should mirror |
| REQ-126-CAPACITY | First `stscreds.AssumeRoleProvider` in `pkg/aws`; second assumed-role `awsCfg`; capacity store namespaced in the constructor, not via the `instanceType` argument | Pattern 4 (exact function/line-level confirmation of the C4 collision in `pkg/capacity/rankaz.go`/`store.go`), Pattern 7 (idiomatic `stscreds` composition against the existing `LoadAWSConfig` helper, with authoritative field names), Pitfall 2 (the fail-open anti-pattern already in this codebase that must NOT be copied here) |
| REQ-126-TEARDOWN | `km destroy` (incl. cold-clone fallback) and ttl-handler both assume the launcher; `km account rm` | Pattern 5 — **a materially bigger finding than C3 states**: the cold-clone path's `FindSandboxByID` is account-scoped and will not discover a B-hosted sandbox at all, not just lack a `launch_account` local. ttl-handler's `TTLHandler` struct already carries a `DynamoClient` and `SSMClient`, de-risking that half of the fix |
| REQ-126-DOCTOR | Link reachable, launcher assumable, artifacts grant present, no orphaned B instances | Modeled directly on the existing `checkSlackPeerBridges`-style pattern (CLAUDE.md-documented) and the `uninit.go`/`doctor.go` AssumeRole-for-a-check precedent found in Pattern 2's investigation |
| REQ-126-UAT | Live: capacity reads B's headroom; create lands in B; destroy/TTL reap it; `simulate-principal-policy` proves containment | Pattern 8 gives a concrete `simulate-principal-policy` test matrix, sourced from the design spec's own IAM policy JSON, plus a verified fact about permissions-boundary evaluation |

</phase_requirements>

<user_constraints>
## User Constraints (sourced from the design spec — no CONTEXT.md exists for this phase; the spec plays that role per the task brief)

### Locked Decisions (from the spec's resolved Q1–Q4 and Guiding Principle)

- **Q1 (trusted principals):** three A-side principals in the launcher trust policy from the start — operator SSO role, `*-create-handler` Lambda role, `*-ttl-handler` Lambda role. All three authored in Wave 2 (retrofitting needs B admin creds again).
- **Q2 (ExternalId storage):** runtime source of truth is SSM SecureString at `{prefix}/launch-accounts/<name>/external-id`; `km-config.yaml` stores only the SSM path, never the plaintext value. Auto-generated by `km account add`, or seeded via `--external-id <v>` / `--sops <file>`.
- **Q3/C4 (capacity store):** account-scoped from v1, namespaced **inside the `DynamoCapacityStore` constructor** (`NewDynamoCapacityStore(client, table, accountNS)`), never by rewriting the `instanceType` argument passed to `RankAZs`. Both home and linked launches share the one `{prefix}-capacity` table in A.
- **Q4 (Bedrock from B):** opt-in only, via `km account add --enable-bedrock`; default is direct API keys via SOPS. Bedrock calls via a lean B box are explicitly **unmetered** (documented, not blocked).
- **Guiding principle:** "You deploy the door; A only knocks." Account A holds no standing power in B outside the one pre-provisioned launcher role.
- **Storage model:** no cross-account writes, ever. B→A is read-only (artifacts). A→B is read-only (results bucket). B→B is read-write (results bucket, same account).
- **The account carries its region.** When `launchAccount` is set, subnet/SG/region come from the link record, not `spec.runtime.region`; a conflicting `spec.runtime.region` is ignored with a warning.
- **CLI shape:** `km account add|register|list|rm`, chosen over `km capacity account add` (naming collision with the existing `km capacity` feasibility command) and over a `km launch-account` verb.

### Claude's Discretion (areas the spec leaves open, or where this research found gaps the spec didn't address)

- **`km account add`'s own Terraform state backend.** The spec asserts "state always lives in A's home S3 backend" but that statement is proven only for the *sandbox launch* flow (state describing a B-hosted EC2 instance, stored in A's bucket, SCP-style). It says nothing about where `km account add`'s *own* state (the launcher/box role/network/bucket definitions) lives, and the spec's stated credential model for that command (run with literal B admin creds) is incompatible with reusing `include "root"`'s A-only-writable backend without modification. See Pattern 3 for the recommended resolution (local state, standalone unit) and the reasoning.
- **Exact wording/shape of the `km validate` "launchAccount references an unknown link" check** — the spec states the requirement but not the mechanism; see Pitfall 4 for the structural constraint (`ValidateSemantic` takes no config parameter) that the plan needs to resolve.
- **Whether `km account add` uses real `terraform apply` (like `km cluster add`) or a `--show-prereqs`-style CLI-command-printing wizard (like the SCP org-admin bootstrap)** — the spec implies the former (it describes `km account rm` tearing down "B-side roles/network"), and this research recommends the former for exactly that reason (destroy needs real state to destroy against), but flags that this is a deliberate divergence from the SCP precedent's actual mechanism, not an oversight.

### Deferred Ideas (OUT OF SCOPE — from the spec's Non-goals)

- Multi-region support (single-region only, matching the rest of km).
- Full-featured cross-account sandboxes: no home-account budget metering, Slack, or email wired into B boxes.
- Go-side AssumeRole *launch* (RunInstances) — the launch itself stays terragrunt-driven via the generated provider; `stscreds` is used only for the **capacity/quota check**, not for provisioning.
- A second full `km init` control plane in account B (explicitly rejected — this is the entire reason the launcher-shim design exists).
- General multi-account capacity survey / automatic account-picking scheduling.
</user_constraints>

## Standard Stack

This phase is almost entirely Go/Terraform/AWS-native. There is exactly **one** new import, and it is a subpackage of a dependency already present in this repo's module graph.

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|---------------|
| `github.com/aws/aws-sdk-go-v2/credentials/stscreds` | Same version line as `github.com/aws/aws-sdk-go-v2/credentials v1.19.12` (already an indirect dependency in `go.mod:61`) | `AssumeRoleProvider` — the credentials provider for REQ-126-CAPACITY's second, B-scoped `aws.Config` | First-party AWS SDK package, official, and the only credential-provider abstraction in aws-sdk-go-v2 purpose-built for this; the alternative is hand-rolling what this codebase's own `uninit.go`/`doctor.go` already did once (see Pitfall 2 — and that hand-rolled version has a bug this phase must not repeat) |

**Installation:** no `go get` of a new module version is required in the typical sense — add the import (`github.com/aws/aws-sdk-go-v2/credentials/stscreds`) to whichever `pkg/aws` file hosts the new helper and run `go mod tidy`; expect the existing `// indirect` marker on `github.com/aws/aws-sdk-go-v2/credentials` in `go.mod:61` to either stay as-is (if `stscreds` is a subpackage of that same module, which official documentation describes but does not state with full certainty — see Assumptions Log A1) or resolve a new line automatically. Either way this is mechanical; it is not a decision point for the plan.

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/aws/aws-sdk-go-v2/service/sts` | `v1.41.9` (already `go.mod:31`, already imported in `pkg/aws`, `internal/app/cmd/uninit.go`) | `sts.NewFromConfig` — the client `stscreds.NewAssumeRoleProvider` wraps | Already in use; no version change |
| `terragrunt` | `v0.99.1` (pinned, `.goreleaser.yaml`) | HCL merge/generate mechanics for REQ-126-LAUNCH | Confirmed by direct execution to be the exact binary this repo bundles; do not test against a different terragrunt version without re-verifying — merge-strategy behavior has changed across terragrunt major versions historically |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `stscreds.AssumeRoleProvider` (auto-refresh via `aws.NewCredentialsCache`) | Hand-rolled `sts.AssumeRole()` + `staticCredentials` (the exact pattern already in `internal/app/cmd/doctor.go:3266-3282`/`uninit.go:235-270`) | The hand-rolled version is a one-shot, non-refreshing credential set (fine for a single short-lived doctor check) and — critically — the existing `uninit.go` caller **falls back to the current (A) credentials on AssumeRole failure**. That fallback is correct for an SCP-cleanup best-effort path; it would be a serious bug if copied into the capacity check (see Pitfall 2), since it would silently check A's quota while believing it checked B's. |
| `merge_strategy = "deep"` on `include "root"` (recommended) | Drop `include "root"` entirely and go fully standalone (mirrors `infra/live/management/scp/terragrunt.hcl` and `infra/live/use1/s3-replication/terragrunt.hcl`, both of which already avoid this exact collision this way) | Standalone is the repo's *more common* idiom for "I need my own provider block" (used twice already, `merge_strategy` used zero times before this phase) but would require manually duplicating root's `errors { retry "transient_network" {...} }` block (12+ lines) into the sandbox template, creating a drift risk every time root's retry policy changes. `merge_strategy = "deep"` keeps that inheritance for free — see Pattern 1 for the full tradeoff. |

## Package Legitimacy Audit

**Not applicable in the typical npm/pypi/crates sense**, and the `gsd-tools query package-legitimacy check` seam's `--ecosystem` flag does not enumerate Go modules. The one new import (`github.com/aws/aws-sdk-go-v2/credentials/stscreds`) is a subpackage of `github.com/aws/aws-sdk-go-v2/credentials`, which is:
- already present in `go.mod:61` as a resolved, hash-pinned (`go.sum:17-18`) **indirect** dependency of this exact repo, pulled in transitively by `aws-sdk-go-v2/config` (already a direct dependency, `go.mod:8`);
- published by the `github.com/aws` GitHub organization — the same publisher as `service/sts`, `service/ec2`, `service/servicequotas`, and every other AWS client this codebase already imports directly;
- confirmed to exist and match the expected API shape via `pkg.go.dev/github.com/aws/aws-sdk-go-v2/credentials/stscreds` (an official Go documentation mirror, not a third-party registry).

There is no slopsquatting risk vector here (no name to typo-confuse, no unaffiliated publisher, no registry to poison) — this is functionally equivalent to importing a different subpackage of a library already fully trusted and vendored.

| Package | Registry | Age | Downloads | Source Repo | Verdict | Disposition |
|---------|----------|-----|-----------|-------------|---------|-------------|
| `github.com/aws/aws-sdk-go-v2/credentials/stscreds` | Go module proxy (not npm/PyPI/crates) | Part of aws-sdk-go-v2, GA since 2021, actively maintained | N/A (Go modules don't report download counts) | `github.com/aws/aws-sdk-go-v2` (official AWS org) | OK | Approved — first-party, already-vendored |

**Packages removed due to [SLOP] verdict:** none.
**Packages flagged as suspicious [SUS]:** none.

## Architecture Patterns

### System Architecture Diagram

```
 ENROLLMENT (one-time, per linked account)                    ROUTINE (every sandbox launch)
 ───────────────────────────────────────                      ──────────────────────────────

 operator, B admin creds                                       km create <profile w/ launchAccount> (A creds)
        │                                                              │
        ▼                                                              ▼
 km account add ──────► standalone terragrunt unit          resolve `launch_accounts.<name>` from
   (Pattern 3)           (own provider = plain B,                km-config.yaml (A-side, no network call)
                          own LOCAL state — no                        │
                          include "root")                             ▼
        │                        │                          compiler writes `launch_account` local
        │                        ▼                                into service.hcl (Wave 4 / REQ-126-LAUNCH)
        │              creates in B:                                  │
        │               • {prefix}-gpu-launcher (trust: A            ▼
        │                 SSO + create-handler + ttl-        infra/templates/sandbox/terragrunt.hcl
        │                 handler, ExternalId)                 include "root" { merge_strategy = "deep" }
        │               • {prefix}-gpu-box + boundary            + child generate "provider" (Pattern 1)
        │               • one subnet per AZ + SG (C5)                 │
        │               • {prefix}-results-{B} bucket        launch_account != "" ?
        │                                                        │              │
        ▼                                                       yes             no
 prints link block                                                │              │
        │                                                         ▼              ▼
        ▼                                                  assume_role{}   plain provider
 operator, A creds                                          block emitted   (byte-identical
        │                                                        │           to Phase 125)
        ▼                                                        ▼
 km account register ──► writes km-config.yaml            terragrunt apply, backend STILL A's
   (A-side only)          launch_accounts.<name>            S3 bucket (state), PROVIDER assumes
        │                 + appends read-only grant          into B (SCP-proven pattern)
        │                 on A's artifacts bucket for              │
        │                 B's box role                             ▼
        ▼                                                   RunInstances lands in B
 km account list / rm                                              │
                                                                     ▼
                                                          ┌─────────────────────┐
 CAPACITY CHECK (parallel, before the sweep)              │ km capacity / doctor │
 ─────────────────────────────────────────                └─────────────────────┘
 create.go: home awsCfg (A) ──sts.AssumeRole──► second awsCfg (B, stscreds.AssumeRoleProvider)
        │                                              │
        ▼                                              ▼
 RankAZs(ctx, instanceType, region, ..., store, ec2c(B), sqc(B), allAZs, natAZs)
        │
        ▼
 capacity.NewDynamoCapacityStore(ddbClient(A), table, accountNS="481723467561")
   — DDB writes still land in A; only the KEY is namespaced (Pattern 4)

 TEARDOWN (destroy / TTL expiry)
 ────────────────────────────────
 km destroy <id>  ──►  local sandbox dir exists?
                          │yes                    │no (cold-clone path)
                          ▼                        ▼
                 terragrunt destroy      READ {prefix}-sandboxes DDB row FIRST (Pattern 5 —
                 (service.hcl already      new prerequisite; FindSandboxByID alone is A-scoped
                 carries launch_account,    and will never see a B-hosted resource)
                 assume_role fires                 │
                 automatically)                    ▼
                                          launch_account found? → synthesize minimalHCL WITH
                                          the local → terragrunt destroy assumes into B
 ttl-handler (Lambda, EventBridge-scheduled) ──► reads {prefix}-sandboxes row (already has
                                                   DynamoClient) → resolves launcher ARN +
                                                   SSM ExternalId (already has SSMClient) →
                                                   renders main.tf with conditional assume_role
```

### Recommended Project Structure

```
internal/app/cmd/
├── account.go              # NEW — km account add/register/list/rm (mirrors cluster.go)
├── create.go                # extended: second awsCfg for capacity, launch_account resolution
├── capacity.go               # extended: same second-awsCfg pattern for `km capacity`
├── destroy.go                # extended: cold-clone path reads DDB row before FindSandboxByID
├── doctor.go                  # extended: link reachable / launcher assumable / grant present checks

pkg/aws/
├── assume.go                # NEW — AssumeRoleConfig(ctx, base aws.Config, roleARN, externalID, region) helper
├── metadata.go                # extended: SandboxMetadata.LaunchAccount field (mirrors NetworkPlacement)
├── sandbox_dynamo_launch_account_test.go  # NEW — mirrors sandbox_dynamo_network_test.go

pkg/capacity/
├── store.go                  # extended: DynamoCapacityStore gains accountNS field, itemKey becomes a method

pkg/profile/
├── types.go                   # extended: RuntimeSpec.LaunchAccount string field (near line 465)
├── validate.go                  # NEW function (not a ValidateSemantic edit — see Pitfall 4): validates
│                                  launchAccount against loaded config, called from the 3 existing call sites

infra/templates/sandbox/
├── terragrunt.hcl             # extended: include "root" { merge_strategy = "deep" } + child generate "provider"

infra/modules/
├── gpu-launcher-account/v1.0.0/  # NEW module — launcher role, box role, boundary, network, results bucket, EFS

infra/live/{region}/account-links/{name}/
├── terragrunt.hcl              # NEW, standalone (no include "root"), generated by `km account add`
├── terraform.tfstate            # LOCAL state (Pattern 3) — not in A's shared S3 backend

cmd/ttl-handler/
├── main.go                      # extended: destroy main.tf render gains conditional assume_role
```

### Pattern 1: The provider-override mechanism (empirically verified — the decisive finding)

**What:** A child `generate "provider"` block, in a file that also `include`s a parent defining a `generate "provider"` block with the same label, does **not** silently override the parent's under terragrunt's default merge behavior. It hard-errors.

**Evidence.** Built three fixture pairs under a scratch directory and ran the exact pinned `terragrunt 0.99.1` binary (`terragrunt render --json --working-dir <dir> --non-interactive`, which resolves config with zero AWS calls — no backend init, no provider download):

1. `include "root" { path = ... }` (no `merge_strategy`) + child `generate "provider" {...}`, parent ALSO defines `generate "provider" {...}` → **`ERROR: Detected generate blocks with the same name: [provider]`**, exit 1. This happened identically whether or not the conditional content differed (tested both a `launch_account`-set and `launch_account`-empty variant — both errored the same way, confirming it's a static structural collision, not a runtime/content issue).
2. Same files, `include "root" { path = ...; merge_strategy = "deep" }` → renders cleanly. `generate.provider.contents` in the resolved JSON is the **child's** content (parent's fully discarded), correctly rendering a conditional `%{ if local.launch_account != "" ~} assume_role { role_arn = "..."; external_id = "..." } %{ endif ~}` block with values interpolated from a `local` sourced via `read_terragrunt_config("${get_terragrunt_dir()}/service.hcl")` — proving C1's (c) claim (reading a compiler-written local at generate-time) as a side effect. The file's own `remote_state` override (present in the fixture, not itself redeclared under a conflicting label) was preserved unchanged, confirming `merge_strategy = "deep"` does not disturb the already-working `remote_state` full-override the real sandbox template depends on.
3. Same files, `include "root" { path = ...; merge_strategy = "no_merge" }` (child also fully redeclares `remote_state`) → also renders cleanly — this is the shape `infra/live/management/scp/terragrunt.hcl` and `infra/live/use1/s3-replication/terragrunt.hcl` **already use in production**, confirmed by reading both files: neither uses `include "root"` at all; both redeclare `remote_state` and `generate "provider"` standalone. `s3-replication`'s own comment states the reason explicitly: *"Standalone (no root include) to avoid duplicate generate 'provider' blocks."* — this is the exact collision found empirically above, already known to a prior author of this codebase.
4. A fourth fixture reproduced the **real** sandbox template's exact shape (locals sourced from `service.hcl`, `include "root" { merge_strategy = "deep" }`, a full `remote_state` override with a per-sandbox key, a conditional `generate "provider"`, and an `inputs = {...}` block) with `launch_account` both set and empty. Both rendered cleanly with no errors; the empty case produced a clean `provider "aws" { region = "us-east-1" }` with no stray template artifacts, no dangling `assume_role {}`.

A repo-wide `grep -rln 'include "root"' infra/live infra/templates` found 40+ units using the default (unset) `merge_strategy`; because `merge_strategy` is configured per-`include`-block *in the child file*, adding it to `infra/templates/sandbox/terragrunt.hcl` only affects that one template. No other unit is at risk.

**When to use:** exactly REQ-126-LAUNCH's provider override. This is not a general-purpose pattern to reach for elsewhere in the codebase — the repo's established idiom for "I need my own provider" is still "go standalone" (used twice already); this phase is the first case where standalone would cost more (losing the `errors` retry-block inheritance) than `merge_strategy = "deep"` costs (one unprecedented line).

**Example (verified working HCL — this is the actual rendered shape, not a guess):**
```hcl
# infra/templates/sandbox/terragrunt.hcl — additions only, rest unchanged
locals {
  # ...existing locals...
  launch_account = try(local.svc_config.locals.launch_account, "")
}

include "root" {
  path           = find_in_parent_folders("root.hcl")
  merge_strategy = "deep"   # REQUIRED — default (shallow) errors on the generate-block collision below
}

# ...existing remote_state override, unchanged...

generate "provider" {
  path      = "provider.tf"
  if_exists = "overwrite_terragrunt"
  contents  = <<-EOF
    provider "aws" {
      region = "${local.svc_config.locals.region_full}"
      %{ if local.launch_account != "" ~}
      assume_role {
        role_arn    = "${local.svc_config.locals.launcher_role_arn}"
        external_id = "${local.svc_config.locals.launcher_external_id}"
      }
      %{ endif ~}
      default_tags {
        tags = {
          ManagedBy = "Terragrunt"
          km_label  = "${local.site_vars.locals.site.label}"
        }
      }
    }
  EOF
}
```
The compiler's job (Wave 4) is only to write `launch_account`, `launcher_role_arn`, and `launcher_external_id` into `service.hcl`'s `locals` block when `spec.runtime.launchAccount` is set — everything else in this block is static and was proven correct above.

### Pattern 2: The SCP precedent's *actual* credential mechanics — a two-phase bootstrap, not a single automated apply

**What:** The design spec cites `infra/live/management/scp/terragrunt.hcl` as "the proven pattern" for backend-in-A + provider-assume-into-B. Reading both the terragrunt unit and its Go caller shows this is true, but the mechanism is more specific than "backend stays in A": it requires a **pre-existing trust relationship**, established out-of-band, before the automated path ever runs.

- `infra/live/management/scp/terragrunt.hcl` has its own `generate "provider"` with `assume_role { role_arn = "arn:aws:iam::${local.accounts.organization}:role/${local.site.label}-org-admin" }` and its own `remote_state` pointing at the same shared A bucket — no `include "root"`.
- `RunBootstrapFunc` (`internal/app/cmd/bootstrap.go`) calls `ApplyTerragruntFunc(ctx, scpDir)` directly, using the operator's **ambient** credentials — there is no profile switch to a B-native identity anywhere in that code path.
- For this to succeed, the ambient credentials must simultaneously (a) be trusted to write A's S3 state bucket directly, and (b) be trusted by `{prefix}-org-admin`'s trust policy in B. That trust relationship is established **manually**, before automation ever runs, via `km bootstrap --scp` / `--show-prereqs`, which does not run terraform at all — it **prints** the raw `aws iam create-role` / `put-role-policy` CLI commands (`bootstrap.go:1470-1780`) for the operator to run by hand, once, using their own literal B credentials.

**When to use:** this is the correct mental model for REQ-126-ENROLL/REGISTER's two commands, and it resolves an apparent contradiction in the design spec (see Pattern 3): `km account add` (run with literal B admin creds) is Phase 0 — it is the step that creates the *first* trust anchor in B, and nothing in A can do that for it, by definition (nothing in B trusts A yet). `km create ... launchAccount: mgmt-gpu` (run with ambient A creds, provider assumes into B) is Phase 1 — the SCP pattern applies to *this* step exactly as the spec describes, because by the time it runs, the launcher role (the trust anchor) already exists.

**Anti-pattern to avoid:** do not try to make `km account add` itself use the SCP's "ambient-A-creds-assume-into-B" model — there is nothing in B for a not-yet-created role to be reached through. Literal B credentials for this one command are structurally required, not a design shortcut to be optimized away.

### Pattern 3: `km account add`'s Terraform state backend — a gap the spec doesn't address, resolved by precedent

**What:** The design spec's component table says every B-side resource is "provisioned by `km account add` (B creds)" and separately asserts "state always lives in A's home S3 backend" — but never reconciles these for `km account add`'s *own* state. If `km account add`'s terragrunt unit reuses `include "root"`'s backend (A's `tf-km-state-use1` bucket, whose policy — per the ROADMAP's own already-verified rejection of `km create --aws-profile <mgmt>` — allows only `arn:aws:iam::052251888500:root`) while running with `AWS_PROFILE` set to a literal B profile (which `pkg/terragrunt/runner.go:420` injects uniformly for the *entire* subprocess, with no way to split backend-credentials from provider-credentials at the Go layer), it hits the **exact same 403** the ROADMAP already flags as a rejected shortcut.

**Recommendation:** give `km account add`'s generated unit (`infra/live/{region}/account-links/{name}/terragrunt.hcl`) its own **standalone** shape — no `include "root"`, `backend = "local"`, own plain `provider "aws" { region = ... }` (no `assume_role`; ambient B creds are used directly for this one command). This is the third instance of the exact "standalone, no include" idiom already used by SCP and s3-replication (Pattern 1), extended to the state backend too. Rationale:
- Avoids the 403 entirely — local state needs no bucket IAM at all.
- Avoids adding a **second** door into A's critical state infrastructure (a cross-account bucket-policy grant for B's admin identity) for a rarely-run, single-operator, admin-only bootstrap step — which would undercut the design's own "the launcher role is the only door" security narrative.
- Chicken-and-egg-free: nothing here depends on anything that doesn't already exist at the moment `km account add` first runs.
- Matches `km cluster add`'s use of real `terraform apply`/`destroy` (not the SCP org-admin path's CLI-command-printing, which has no state to destroy against) — necessary because the design's `km account rm` is specified to tear down B-side roles/network, which needs real Terraform state to do cleanly.

**Tradeoff, stated plainly:** local state means no team-shared state and no locking. For a low-frequency, single-operator enrollment operation this is an acceptable, precedented v1 tradeoff (mirrors how `km cluster add`'s per-cluster stacks, though A-native, are also not expected to be edited concurrently by multiple operators). If this becomes a multi-operator operation later, migrating to a small dedicated S3 bucket *in B* is the natural, additive follow-up — not a v1 requirement.

### Pattern 4: Capacity store namespacing — verified down to the exact colliding call

**What:** Confirmed by direct reading of `pkg/capacity/rankaz.go` and `pkg/capacity/store.go` (not just the spec's description):
- `RankAZs(ctx, instanceType, region, azPreference, store, ec2c, sqc, allAZs, natAZs)` passes `instanceType` to `DescribeAZOfferings` (`rankaz.go:174`, which builds an EC2 `Filters: [{Name: "instance-type", Values: [instanceType]}]`) **and** to `IsGPUFamily(instanceType)` (`rankaz.go:210`) **and**, via `rankScore` → `store.Get(ctx, instanceType, az)`, into `itemKey(instanceType, az)` (`store.go:151-155`), which sets the DynamoDB hash-key attribute `"instanceType"` to that literal string.
- A namespaced value like `"481723467561#g6e.12xlarge"` passed as `instanceType` would reach the EC2 `DescribeInstanceTypeOfferings` filter and find nothing (breaking AZ offering detection), confirming C4's diagnosis exactly.
- The fix is surgical: `DynamoCapacityStore` needs an `accountNS string` field, and `itemKey` needs to become a method (or take an extra parameter) that prefixes the DDB key value with `s.accountNS + "#"` **only** when non-empty — the `instanceType` string flowing to `RankAZs`'s EC2/quota calls stays untouched everywhere else.

**Critical operational detail the spec's Q3 doesn't spell out:** only `RecordICE` rows carry a TTL (45 min, `ICETTLSeconds`); `RecordSuccess` rows persist **forever** (no TTL). The spec's "existing bare-keyed home rows age out via the 45-min TTL" is only true for ICE rows — accumulated sticky-AZ *success* history for home launches would NOT age out, and would be silently orphaned if home's own namespace were ever changed from empty. **The plan must keep the home/default account's `accountNS` as the empty string** (not, say, A's own account ID) — only newly-linked accounts get a non-empty namespace — or months of accumulated home AZ-preference history is silently lost.

**Two construction call sites confirmed:** `internal/app/cmd/create.go` (`capacityStore = capacity.NewDynamoCapacityStore(ddbForCapacity, cfg.GetCapacityTableName())`, 2-arg today) and `internal/app/cmd/capacity.go:173` (`store := capacity.NewDynamoCapacityStore(ddbClient, cfg.GetCapacityTableName())`, same 2-arg shape). Both need a third `accountNS` argument threaded from the resolved launch-account (empty string for home).

### Pattern 5: Cross-account teardown discovery — C3 is necessary but not sufficient

**What:** C3 correctly flags that `internal/app/cmd/destroy.go`'s cold-clone `minimalHCL` (five hardcoded locals: `sandbox_id`, `region_label`, `region_full`, `substrate_module`, `module_inputs`) has no `launch_account` field. Reading the surrounding code shows a **more fundamental** blocker sitting *before* that: the cold-clone branch is only reached after `awspkg.FindSandboxByID(ctx, tagClient, sandboxID)` already succeeds (`destroy.go` — call happens, then resources are reported, then the local-directory check follows). `FindSandboxByID` (`pkg/aws/discover.go:45`) calls `resourcegroupstaggingapi.GetResourcesInput` scoped to whatever account `tagClient` is authenticated as — which, for `km destroy` today, is always the operator's ambient (A) credentials.

**Consequence:** a B-hosted sandbox is invisible to this call. `output.ResourceTagMappingList` would be empty, and the function returns `ErrSandboxNotFound` — `km destroy <b-hosted-id>` fails immediately with "sandbox not found," *before* the cold-clone `minimalHCL` synthesis branch is ever reached, regardless of whether that branch's literal gains a `launch_account` field.

**The correct fix chain, in order:**
1. `SandboxMetadata` (`pkg/aws/metadata.go`, next to `NetworkPlacement` at lines 111/123) gains a `LaunchAccount string` field, threaded through the exact same marshal/unmarshal chokepoint, with its own round-trip test mirroring `pkg/aws/sandbox_dynamo_network_test.go` (`TestLaunchAccount_RoundTrip`, following `TestNetworkPlacement_PrivateRoundTrip`'s shape exactly).
2. `km destroy`'s discovery flow reads the `{prefix}-sandboxes` DynamoDB row for `sandboxID` **before or in place of** the A-scoped `FindSandboxByID` call, at least when the local directory is absent. If `launch_account != ""`, the flow should either skip the A-scoped tag discovery (it will never find anything there) or perform a **second**, B-scoped tag lookup using the same assumed-role `aws.Config` this phase already builds for the capacity check — the human-facing "(N resources)" print is a nice-to-have, not a correctness requirement, and can degrade gracefully.
3. Only then does C3's literal fix apply: extend `minimalHCL` with `launch_account = %q`, sourced from the DDB read in step 2, not from any local file (there is none in the cold-clone case by definition).

Note this fix is needed **only** for the cold-clone path. When the local sandbox directory exists, `service.hcl` already carries `launch_account` from create time (Wave 4), and `terragrunt destroy` run from that directory picks up the same conditional `generate "provider"` override verified in Pattern 1 with no DDB read required.

**ttl-handler is better-positioned than the cold-clone path.** `TTLHandler` (`cmd/ttl-handler/main.go`) already carries `DynamoClient awspkg.SandboxMetadataAPI` and `SSMClient *ssmpkg.Client` as struct fields (populated once at Lambda cold-start). Reading the sandbox's new `LaunchAccount` DDB attribute and its SSM ExternalId are both small additions on top of capabilities already wired in — only the `launch_accounts` config-to-Lambda-env threading (a small JSON blob, precedented repeatedly elsewhere, e.g. `KM_GITHUB_EVENTS`, `KM_SLACK_PEER_BRIDGES`) and the conditional `assume_role` block in the Go-templated `main.tf` string (`cmd/ttl-handler/main.go:1193-1230`, plain Go `fmt.Sprintf` branching — not HCL `%{if}` templating, since this string is built entirely in Go) are new. Also note: `h.Region`/`h.StateBucket`/`h.StatePrefix` are Lambda-instance-wide (from env vars at cold start), not per-event — the region for a B-hosted sandbox's destroy must come from the **link record** (via the new DynamoDB read), not from `h.Region`, mirroring how the design already says "the account carries its region" for create.

### Pattern 6: Config plumbing — two precedents, choose per shape

**What:** `internal/app/config/config.go` already has both shapes `launch_accounts` needs modeled:
- **List-shaped, with a CLI command family:** `clusters` (`ClusterConfig`, `config.go:751`) is in the v2→v merge-list (`config.go:964`), has `v.SetDefault("clusters", []interface{}{})` (`:874`) and `v.UnmarshalKey("clusters", &cfg.Clusters)` (`:1159`) — the exact skeleton `km cluster add/list/rm` is built on.
- **Map-shaped, keyed by name (closer to `launch_accounts: { mgmt-gpu: {...} }`):** `github.commands map[string]GithubCommandEntry` (`config.go:310`) and `h1.events map[string]H1EventEntry` / `h1.commands map[string]H1CommandEntry` (`config.go:423,427`) are the existing precedent for a name-keyed sub-config block.

**Recommendation:** `launch_accounts` should follow the `github.commands`/`h1.events` map shape (`map[string]LaunchAccountConfig`) for the config struct itself, but the merge-list registration, `SetDefault`, and `UnmarshalKey` calls follow `clusters`' exact three-call pattern (just with a `map[string]interface{}{}` default instead of a slice). The `"launch_accounts"` string **must** be added to the merge-list at `config.go:964` — this is the single most commonly-hit footgun in this codebase's recent history (already bit `slack.mention_only`, `slack_channels_table_name`, `network.nat_gateway`).

### Pattern 7: `stscreds` composition against the existing `LoadAWSConfig` helper

**What:** `pkg/aws/client.go:28` exposes `LoadAWSConfig` as a package-level `var` (function value, not a `func` declaration — a deliberate test-seam so test binaries can override it, consistent with `[[project_cmd_testmain_fast_seams]]`). It returns `(aws.Config, error)` for the home (A) account. The idiomatic composition, confirmed against `pkg.go.dev/github.com/aws/aws-sdk-go-v2/credentials/stscreds` and cross-checked against a second, independent WebSearch (both agree):

```go
// pkg/aws/assume.go (new)
func AssumeRoleConfig(ctx context.Context, base aws.Config, roleARN, externalID, region string) (aws.Config, error) {
    stsClient := sts.NewFromConfig(base)
    provider := stscreds.NewAssumeRoleProvider(stsClient, roleARN, func(o *stscreds.AssumeRoleOptions) {
        o.RoleSessionName = "km-capacity-check"
        if externalID != "" {
            o.ExternalID = aws.String(externalID)
        }
    })
    cfg := base.Copy()
    cfg.Credentials = aws.NewCredentialsCache(provider) // REQUIRED — AssumeRoleProvider does not self-cache
    cfg.Region = region
    return cfg, nil
}
```
Call site shape (`create.go`/`capacity.go`):
```go
homeCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)   // unchanged, existing line
// ...resolve link record (launcherRoleARN, externalID, region) from launch_accounts config...
launchCfg, err := awspkg.AssumeRoleConfig(ctx, homeCfg, launcherRoleARN, externalID, link.Region)
ec2OfferingsClient := ec2svc.NewFromConfig(launchCfg, func(o *ec2svc.Options) { o.Region = link.Region })
sqClient := sqsvc.NewFromConfig(launchCfg, func(o *sqsvc.Options) { o.Region = link.Region })
```
`aws.Config.Copy()` is a standard aws-sdk-go-v2 method (used precisely so the base config's other settings — retry policy, HTTP client, logger — are preserved and only `Credentials`/`Region` are swapped).

**`AssumeRoleOptions` fields confirmed** (pkg.go.dev): `RoleSessionName string`, `ExternalID *string`, `Duration time.Duration` (default 15 min), `SerialNumber`/`TokenProvider` (MFA, unused here), `Policy *string` (inline session policy, unused here), `Tags []types.Tag`. `ExternalID` is a pointer — must be `aws.String(externalID)`, and should only be set when non-empty (matches Q2's SSM-sourced ExternalId).

**Anti-pattern already in this codebase, not to copy** (see Pitfall 2): `internal/app/cmd/uninit.go:235-270` hand-rolls this exact assume-role pattern (raw `sts.AssumeRole` call + a hand-written `staticCredentials` implementing `aws.CredentialsProvider`) instead of `stscreds`, and on `AssumeRole` failure it **falls back to using the current (A) credentials** with a warning log rather than failing the operation. That fallback is defensible for its actual use case (best-effort SCP cleanup during `km uninit`) but would be a serious correctness bug if the same pattern were copied for the capacity check — a failed AssumeRole into B must **fail the capacity check**, not silently check A's 64-vCPU quota while believing it checked B's 768.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Cross-account temporary credentials with auto-refresh | A hand-rolled `sts.AssumeRole()` + custom `aws.CredentialsProvider` (the `uninit.go`/`doctor.go` pattern) | `stscreds.AssumeRoleProvider` wrapped in `aws.NewCredentialsCache` | The hand-rolled version already exists once in this codebase and is single-shot (no refresh) with a fail-open fallback that is wrong for this use case; `stscreds` is the SDK-blessed, auto-refreshing equivalent |
| Cross-account provider targeting in Terraform | A Go-side `RunInstances` call using assumed-role credentials directly | Terragrunt-generated `assume_role` provider block | Explicitly a spec non-goal ("Go-side AssumeRole launch" is rejected) — the launch stays terragrunt-driven so it gets the same plan/apply/state lifecycle as every other sandbox resource |

**Key insight:** almost nothing in this phase is a "library choice" problem — it's an "existing internal chokepoint" problem (does `ValidateSemantic` take a config? does `FindSandboxByID` know about other accounts? does the capacity store's key collide with the EC2 filter?). The don't-hand-roll list is short because the risk in this phase is architectural, not library selection.

## Common Pitfalls

### Pitfall 1: Assuming C1's mechanism works as literally written
**What goes wrong:** implementing the child `generate "provider"` block exactly as C1 describes it (no `merge_strategy` change) breaks `km create` for **every** sandbox, cross-account or not, the moment `infra/templates/sandbox/terragrunt.hcl` gains its own `generate "provider"` block.
**Why it happens:** terragrunt's default (shallow) merge treats a same-labeled `generate` block in parent and child as an error, not a silent override — confirmed by direct execution against the pinned binary (Pattern 1).
**How to avoid:** add `merge_strategy = "deep"` to the sandbox template's `include "root"` block as part of the same change.
**Warning signs:** `terragrunt render`/`plan`/`apply` failing with `Detected generate blocks with the same name: [provider]` on ANY sandbox create, including ones without `launchAccount` set.

### Pitfall 2: Fail-open AssumeRole for the capacity check
**What goes wrong:** copying the `uninit.go` pattern's "fall back to current credentials on AssumeRole failure" behavior into the capacity/quota check.
**Why it happens:** it's the only existing in-repo precedent for cross-account AssumeRole, and it's easy to copy without noticing the fallback semantics were tuned for a different (best-effort cleanup) use case.
**How to avoid:** the capacity check must fail closed — a failed AssumeRole into B is a hard error for a launch that requires B's quota, not a silent A-quota check.
**Warning signs:** a `km create` for a `launchAccount`-scoped GPU profile succeeding capacity checks even when the launcher role doesn't exist or the ExternalId is wrong.

### Pitfall 3: Treating `FindSandboxByID` as account-agnostic
**What goes wrong:** implementing C3's literal fix (add `launch_account` to `minimalHCL`) without fixing the earlier `FindSandboxByID` call, leaving `km destroy <b-hosted-id>` failing with "sandbox not found" before the fix ever has a chance to run.
**Why it happens:** the two problems are adjacent in the same function but the tag-discovery call happens first and is not obviously account-scoped from reading `destroy.go` alone — it only becomes visible on reading `pkg/aws/discover.go`.
**How to avoid:** see Pattern 5's three-step fix chain; the DynamoDB `LaunchAccount` read must happen before or instead of the A-scoped tag lookup.
**Warning signs:** `km destroy` working for a B-hosted sandbox when the local directory exists, but failing with "not found" for the identical sandbox once the directory is deleted.

### Pitfall 4: Adding the `launchAccount`-unknown-link check inside `ValidateSemantic`
**What goes wrong:** `ValidateSemantic(p *SandboxProfile) []ValidationError` (`pkg/profile/validate.go:221`) takes **only** a profile — no `*config.Config` parameter. It's called from three places (`validate.go:58`, `:64` inside the package's own `Validate()`, and `internal/app/cmd/create.go:3194` directly). A check that needs `km-config.yaml`'s `launch_accounts` map cannot live inside this function without either breaking its signature (touching all three call sites and every existing test) or accepting an awkward global/package-var config injection.
**Why it happens:** every other `km validate` semantic check operates purely on the profile's own fields; this is the first check that needs external, loaded config.
**How to avoid:** add a new, separate function (e.g. `ValidateLaunchAccount(p *SandboxProfile, cfg *config.Config) []ValidationError`) called alongside — not inside — `ValidateSemantic`, at the CLI layer where `cfg` is already loaded (both `km validate <profile>` and `km create` load config early).
**Warning signs:** a compile error touching `pkg/profile/validate_test.go`'s existing `ValidateSemantic` call sites, or a `pkg/profile` package that suddenly imports `internal/app/config` (a layering violation — profile validation should not depend on the CLI config package).

### Pitfall 5: Namespacing home's own capacity-store rows
**What goes wrong:** "fixing" Q3 by giving A's own account ID a namespace too (for symmetry), which orphans every existing `RecordSuccess` row (no TTL, persists forever) under the old bare key.
**Why it happens:** namespacing "every account, including home" reads as more consistent than special-casing home to stay empty.
**How to avoid:** home/default `accountNS` must stay the empty string; only newly-linked accounts get a non-empty prefix.
**Warning signs:** `km capacity` (home profiles) losing sticky-AZ preference immediately after this phase ships, with no travel-back-in-time way to recover the lost history.

### Pitfall 6: Reusing `include "root"`'s A-only backend for `km account add`
**What goes wrong:** generating `km account add`'s terragrunt unit from a template that `include`s root.hcl (inheriting A's S3 backend) while running the command with literal B credentials — an immediate 403 on the state bucket, the exact failure mode already documented as rejected for `km create --aws-profile <mgmt>`.
**Why it happens:** every other new terragrunt unit in this codebase's recent history (dynamodb-*, lambda-*, ttl-handler, etc.) correctly reuses `include "root"`, so it's the path of least resistance to copy without noticing this command's credentials are fundamentally different (literal B creds, not ambient A creds assuming into B).
**How to avoid:** see Pattern 3 — standalone unit, local state, no `include "root"`.
**Warning signs:** `km account add` failing with an S3 `AccessDenied` on the *backend* (not the resources being created) the very first time it's run with a B profile.

## Code Examples

### Capacity store, namespaced (the C4 fix, exact shape)
```go
// pkg/capacity/store.go — modified
type DynamoCapacityStore struct {
    client    CapacityDDBClient
    tableName string
    accountNS string // "" = home account (bare key, unchanged from pre-Phase-126 behavior)
}

func NewDynamoCapacityStore(client CapacityDDBClient, tableName string, accountNS string) *DynamoCapacityStore {
    return &DynamoCapacityStore{client: client, tableName: tableName, accountNS: accountNS}
}

func (s *DynamoCapacityStore) itemKey(instanceType, az string) map[string]dynamodbtypes.AttributeValue {
    key := instanceType
    if s.accountNS != "" {
        key = s.accountNS + "#" + instanceType
    }
    return map[string]dynamodbtypes.AttributeValue{
        "instanceType": &dynamodbtypes.AttributeValueMemberS{Value: key},
        "az":           &dynamodbtypes.AttributeValueMemberS{Value: az},
    }
}
// RecordICE/RecordSuccess/Get all change their itemKey(...) calls to s.itemKey(...) —
// the CapacityStore interface (RecordICE/RecordSuccess/Get signatures) is UNCHANGED.
```
Both construction call sites (`create.go`, `capacity.go:173`) pass `accountNS: ""` for the home path and the resolved link's account ID for a `launchAccount`-scoped path.

### `service.hcl` locals the compiler must emit (Wave 4)
```hcl
locals {
  # ...existing locals (sandbox_id, region_label, substrate_module, module_inputs)...
  launch_account         = "mgmt-gpu"                                      # empty string when unset
  launcher_role_arn      = "arn:aws:iam::481723467561:role/km-gpu-launcher"
  launcher_external_id   = "super-secret-value-from-ssm"                    # resolved at compile time from SSM
}
```
Verified via direct `terragrunt render --json` execution (Pattern 1) that these three locals, read via `read_terragrunt_config(service.hcl)`, correctly flow into the conditional `assume_role` block.

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|---------------|--------|
| Single-account sandbox launches only | Optional cross-account launch via a pre-provisioned launcher role | This phase (v1) | Nothing changes for existing profiles — `launchAccount` absent is byte-identical to Phase 125, verified empirically (Pattern 1, fixture 4) |
| `DynamoCapacityStore` keyed bare by `instanceType` | Keyed by `accountNS#instanceType` when non-home | This phase | Schema-on-write, no table migration; home rows keep their existing bare keys forever |

**No deprecations.** This phase is purely additive at every layer touched (config, schema, terragrunt, Go).

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|-----------------|
| A1 | `stscreds` is a subpackage of the `credentials` Go module (not a separately-versioned module requiring its own `go.mod` pin) | Standard Stack | LOW — even if wrong, `go mod tidy` resolves it automatically; this affects zero design decisions, only whether `go.sum` gains one extra module-hash pair. Two independent WebSearch/WebFetch lookups both leaned this way but neither stated it with full certainty. |
| A2 | Terraform's AWS provider `assume_role` sub-block's `external_id` argument works exactly as used in the SCP precedent and this phase's generated HCL (well-established, long-stable Terraform AWS provider functionality) | Pattern 1, Code Examples | LOW — this is bog-standard, years-stable Terraform AWS provider functionality already implicitly exercised by the SCP module's `assume_role { role_arn = ... }` (SCP's doesn't use `external_id`, but the `external_id` argument's existence and behavior inside the same block is not independently fetched from HashicorpAWS-provider docs in this session; it is treated as effectively certain training knowledge, not verified via WebFetch this session) |
| A3 | `iam simulate-principal-policy`'s permissions-boundary evaluation (confirmed via WebSearch/AWS docs, GA since Jan 2020) behaves identically when the SIMULATED principal is a role in the org **management** account (SCP-exempt) as it does for a normal member-account role | UAT / Security Domain | MEDIUM — if boundary evaluation is in any way SCP-evaluation-coupled in a way that behaves differently for the management account specifically, the Wave 2 "prove the negative" exit criterion would need a live-account test rather than a pre-apply dry check. Not independently tested against a real management-account role in this session (no B credentials, no mutating AWS calls permitted for this research pass). |

## Open Questions

1. **Does `km account add` use real `terraform apply`/`destroy`, or the SCP org-admin path's CLI-command-printing wizard?**
   - What we know: the design spec's CLI surface (`km account rm` "tears down B-side roles/network") implies real, destroyable Terraform state; `km cluster add` is the closer precedent (real terraform) than the SCP bootstrap (printed CLI commands, no state tracking).
   - What's unclear: whether the plan should treat this as settled by precedent-weight (this research recommends: yes, real terraform, per Pattern 3) or whether the user wants the lighter-weight, no-state CLI-print approach for a rarely-run bootstrap step.
   - Recommendation: proceed with real terraform + local state (Pattern 3) unless corrected; it's the only option that makes `km account rm` a clean, idempotent teardown rather than a manual cleanup checklist.

2. **Exact shape of the `km account add --provision-network` "one subnet per AZ" output consumed by C5's AZ-sweep fix.**
   - What we know: C5 requires the link record to store a subnet **list**, not a single ID, so `RankAZs`'s `maxAttempts` doesn't collapse to 1 in B.
   - What's unclear: whether the link's subnet list needs the exact same `natAZs`-style filtering `create.go:688-709` already applies for Phase 125 private subnets, or whether B's subnets (public, per the design's "lean network... skips EFS/NAT by design" framing) sidestep that entirely.
   - Recommendation: since B's design explicitly has no NAT-gateway concept ("the link record has one subnet and no NAT concept" per C5's own text before the fix), the per-AZ subnet list in B should be treated as always-public — `natAZs` filtering should be skipped (nil) for any `launchAccount`-scoped sweep, not derived from B's (nonexistent) NAT gateways.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `terragrunt` | REQ-126-LAUNCH mechanism verification | Yes | 0.99.1 (`/opt/homebrew/bin/terragrunt`) — confirmed to exactly match this repo's pinned bundled version | — |
| AWS credentials for account B (management, 481723467561) | Live UAT (REQ-126-UAT), any real `km account add` test | Not available in this research session (by design — read-only research, no AWS mutation) | — | This research relied on repo code + docs + local terragrunt execution only; the plan's Wave 2/UAT steps will need real B admin credentials, out of scope for research |
| Go toolchain / `go mod tidy` | `stscreds` import | Not exercised this session (no source modification permitted) | go.mod shows Go SDK v2 modules already resolved | Mechanical step, low risk (A1) |

**Missing dependencies with no fallback:** B admin credentials — required for Wave 2 execution and UAT, not for planning.
**Missing dependencies with fallback:** none blocking for the planning phase itself.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` (stdlib), no external test framework |
| Config file | none — plain `go test` |
| Quick run command | `go test ./pkg/capacity/... ./pkg/aws/... ./pkg/profile/...` (packages this phase touches that ARE covered by the default sweep) |
| Full suite command | `make test` **plus, separately**, `go test ./internal/app/cmd/... -timeout 600s` and `go test ./cmd/ttl-handler/... -timeout 600s` |

**Critical finding, not previously documented for this phase:** `Makefile:76`'s `test` target explicitly **excludes** `cmd/ttl-handler`, `internal/app/cmd`, `cmd/km-slack`, and `pkg/compiler` from `go test ./...`:
```
test:
	go test $$(go list ./... | grep -v 'cmd/km-slack' | grep -v 'cmd/ttl-handler' | grep -v 'internal/app/cmd' | grep -v 'pkg/compiler')
```
Nearly all of this phase's new/changed Go code lives in exactly those excluded packages (`internal/app/cmd/account.go`, `create.go`, `capacity.go`, `destroy.go`, `doctor.go`; `cmd/ttl-handler/main.go`). Per `[[project_cmd_testmain_fast_seams]]` and `[[project_cmd_suite_pre_existing_failures]]`, these packages need their own explicit `go test` invocation with an extended timeout (AWS-config-loading fast-fail seams make some tests slow/fragile in isolation) and — per `[[feedback_check_go_test_exit_not_pipe]]` — the plan's verification steps must check the command's own exit code, not the exit code of a downstream `| tail`/`| grep`.

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|---------------------|--------------|
| REQ-126-CFG | `launch_accounts` merge-list + unmarshal round-trip | unit | `go test ./internal/app/config/... -run TestLoad` | ❌ Wave 1 (new subtest, existing file) |
| REQ-126-CFG | `km validate` rejects unknown link name | unit | `go test ./pkg/profile/... -run TestValidateLaunchAccount` | ❌ Wave 1 (new file, per Pitfall 4) |
| REQ-126-LAUNCH | provider-override renders correctly with/without `launch_account` | integration (terragrunt render, no AWS) | manual `terragrunt render --json` against fixtures (as performed in this research) — **no existing automated coverage for terragrunt HCL behavior itself**; consider a thin Go test that shells out to `terragrunt render --json` against the real template, asserting on the JSON, mirroring `pkg/terragrunt/substrate_version_pin_test.go`'s HCL-parsing approach | ❌ Wave 4 gap |
| REQ-126-CAPACITY | `AssumeRoleConfig` composes correctly, `DynamoCapacityStore` namespacing | unit | `go test ./pkg/aws/... -run TestAssumeRoleConfig` / `go test ./pkg/capacity/... -run TestNamespacedItemKey` | ❌ Wave 4 |
| REQ-126-TEARDOWN | `SandboxMetadata.LaunchAccount` round-trip | unit | `go test ./pkg/aws/... -run TestLaunchAccount_RoundTrip` (mirrors `sandbox_dynamo_network_test.go`) | ❌ Wave 5 |
| REQ-126-TEARDOWN | cold-clone path reads DDB before tag-scan | unit (mocked DynamoClient/TagAPI) | `go test ./internal/app/cmd/... -run TestDestroyColdClone -timeout 600s` | ❌ Wave 5 |
| REQ-126-DOCTOR | link reachable / launcher assumable checks | unit (mocked STS) | `go test ./internal/app/cmd/... -run TestDoctorAccountLink -timeout 600s` | ❌ Wave 5 |
| REQ-126-UAT | live launcher containment proof | manual-only | `aws iam simulate-principal-policy ...` (see Security Domain test matrix below) | N/A — manual by nature |

### Sampling Rate
- **Per task commit:** package-scoped `go test ./<changed-package>/...`
- **Per wave merge:** `make test` + the two excluded-package invocations above, both with explicit exit-code checks
- **Phase gate:** full suite green (including the two excluded packages) before `/gsd-verify-work`; live UAT (REQ-126-UAT) requires real B credentials and is necessarily manual/scripted-but-supervised

### Wave 0 Gaps
- [ ] `pkg/profile/validate_launch_account_test.go` — new file, covers REQ-126-CFG's unknown-link rejection
- [ ] `pkg/aws/sandbox_dynamo_launch_account_test.go` — new file, covers REQ-126-TEARDOWN's metadata round-trip
- [ ] `pkg/aws/assume_test.go` — new file, covers REQ-126-CAPACITY's `AssumeRoleConfig` helper (mockable via a fake `AssumeRoleAPIClient`, same interface-narrowing pattern already used throughout `pkg/aws`/`pkg/capacity`)
- [ ] No new test framework or config needed — Go stdlib `testing` covers everything

## Security Domain

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-------------------|
| V2 Authentication | No | This is machine-to-machine STS AssumeRole, not user authentication |
| V3 Session Management | Partial | STS temporary credentials ARE a session concept — `Duration` (default 15 min via `stscreds.AssumeRoleOptions`) should be set explicitly rather than left at the SDK default, matching the short-lived, single-purpose nature of the capacity check |
| V4 Access Control | **Yes — the core of this phase** | The launcher role's identity policy + permissions boundary (design spec's IAM containment section) IS the access-control mechanism; `ExternalId` (Q2, SSM SecureString) is the confused-deputy mitigation for the cross-account trust |
| V5 Input Validation | Yes | `km validate` rejecting an unknown `launchAccount` link name and rejecting `launchAccount`+`privateSubnet` together (C5) are both input-validation gates that must fire before any AWS call |
| V6 Cryptography | Partial | `ExternalId` and `external_id_ssm`'s value are secrets-at-rest concerns (SSM SecureString, existing SOPS→SSM pattern from Phase 89) — no new cryptographic primitive is introduced by this phase |

### Known Threat Patterns for cross-account IAM launcher roles

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|-----------------------|
| Confused deputy — a third party tricks A's create-handler into assuming the launcher role on their behalf | Spoofing | `ExternalId`, required on every `AssumeRole` call (Q2) — exactly the AWS-documented purpose of `ExternalId` |
| Privilege escalation via the box role once launched | Elevation of Privilege | Permissions boundary (`{prefix}-gpu-box-boundary`) caps the box role even if its own policy is later widened by mistake |
| Launcher role scope creep (an untagged/non-GPU/wrong-subnet resource) | Elevation of Privilege / Tampering | The launcher's own identity policy conditions (`ec2:InstanceType` allowlist, `ArnEquals ec2:Subnet`, `aws:RequestTag/km:managed-by`) are the primary control — **and since B is SCP-exempt, there is no secondary org-level backstop**, making this policy the entire boundary |
| Home-account capacity-store cross-contamination | Tampering (data integrity) | Constructor-level namespacing (Pattern 4), never touching the shared `instanceType` argument that also feeds the EC2 API |
| Cold-clone destroy silently doing nothing for a B-hosted sandbox (availability risk — an orphaned box keeps billing) | Denial of Service (of the *control*, not the service) | Pattern 5's fix chain; this is explicitly flagged in the phase requirements as "not optional" (REQ-126-TEARDOWN) |

### Practical `simulate-principal-policy` test matrix for REQ-126-UAT

Confirmed via AWS documentation (search, cross-referenced across `docs.aws.amazon.com` and the CLI reference) that `simulate-principal-policy` **does** evaluate permissions boundaries when they're attached to the simulated principal (GA since Jan 2020) — this is the mechanism that can actually prove the containment negative for the launcher role, since B (the org management account) is SCP-exempt and therefore has no SCP layer for the simulator to additionally evaluate there. Each row below should be one `aws iam simulate-principal-policy --policy-source-arn <launcher-role-arn> --action-names <action> [--resource-arns <arn>] [--context-entries ...]` call, expecting the stated verdict:

| Simulated call | Expected verdict | Proves |
|---|---|---|
| `ec2:RunInstances` on an allowlisted instance type, in the km subnet, with `km:managed-by=klankermaker` tag context | allowed | The happy path works at all |
| `ec2:RunInstances` with instance type NOT in the allowlist | implicitDeny | `ec2:InstanceType` condition is enforced |
| `ec2:RunInstances` targeting a subnet other than the km subnet | implicitDeny | `ArnEquals ec2:Subnet` condition is enforced |
| `ec2:RunInstances` with no `km:managed-by` tag context supplied | implicitDeny | `aws:RequestTag` condition is enforced |
| `iam:CreateRole` / `iam:PutRolePolicy` (any resource) | implicitDeny | The launcher can never self-escalate — not present in the policy at all |
| `ec2:TerminateInstances` on a resource NOT tagged `km:managed-by=klankermaker` | implicitDeny | `LifecycleTaggedOnly`'s `aws:ResourceTag` condition scopes lifecycle actions |
| `s3:GetObject` on any bucket other than the home artifacts bucket | implicitDeny | No S3 access beyond the one explicit cross-account grant (which is on A's bucket policy, not the launcher's own policy — should also independently return denied when simulated against the launcher role alone) |
| `organizations:*` (any action) | implicitDeny | No Organizations API access — relevant precisely because B is the org management account and a careless policy could accidentally grant this |

## Sources

### Primary (HIGH confidence — direct execution / direct code reading, this session)
- `terragrunt v0.99.1` (`/opt/homebrew/bin/terragrunt`, confirmed identical to `.goreleaser.yaml`'s pin) — `render --json` executed against four fixture pairs to resolve the C1 mechanism definitively.
- `infra/live/root.hcl`, `infra/live/management/scp/terragrunt.hcl`, `infra/live/use1/s3-replication/terragrunt.hcl`, `infra/templates/sandbox/terragrunt.hcl` — read in full.
- `pkg/terragrunt/sandbox.go`, `pkg/terragrunt/substrate_version_pin_test.go`, `pkg/terragrunt/runner.go` (AWS_PROFILE injection) — read in full/relevant sections.
- `pkg/capacity/rankaz.go`, `pkg/capacity/store.go` — read in full.
- `internal/app/cmd/create.go` (network resolution, capacity/quota check, `runCreateRemote`, flag defaults), `internal/app/cmd/capacity.go`, `internal/app/cmd/destroy.go` (cold-clone fallback), `internal/app/cmd/uninit.go`/`doctor.go` (AssumeRole precedent + `staticCredentials`), `internal/app/cmd/bootstrap.go` (SCP two-phase bootstrap mechanics) — relevant sections read directly.
- `cmd/ttl-handler/main.go` (`TTLEvent`, `TTLHandler` struct, destroy `main.tf` renderer) — relevant sections read directly.
- `pkg/aws/client.go` (`LoadAWSConfig`), `pkg/aws/discover.go` (`FindSandboxByID`), `pkg/aws/metadata.go` (`NetworkPlacement` precedent) — read directly.
- `internal/app/config/config.go` (v2→v merge-list, `clusters`/`github.commands`/`h1.events` precedents) — read directly.
- `pkg/profile/validate.go` (`ValidateSemantic` signature and call sites), `pkg/profile/schemas/sandbox_profile.schema.json` (`privateSubnet` precedent) — read directly.
- `Makefile` (test target's package exclusions), `go.mod`/`go.sum` (dependency versions) — read directly.
- `skills/cluster/SKILL.md` — read in full.
- `docs/superpowers/specs/2026-06-29-cross-account-gpu-launch-design.md` — read in full, including the 2026-08-22 re-verification section.

### Secondary (MEDIUM confidence — WebSearch/WebFetch, cross-referenced)
- [pkg.go.dev/github.com/aws/aws-sdk-go-v2/credentials/stscreds](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/credentials/stscreds) — `NewAssumeRoleProvider` signature, `AssumeRoleOptions` fields, cache-wrapping requirement.
- AWS IAM policy simulator documentation (via search, [docs.aws.amazon.com/IAM/latest/UserGuide/access_policies_testing-policies.html](https://docs.aws.amazon.com/IAM/latest/UserGuide/access_policies_testing-policies.html), [aws.amazon.com/about-aws/whats-new/2020/01/aws-iam-policy-simulator-now-simulates-permissions-boundary-policies](https://aws.amazon.com/about-aws/whats-new/2020/01/aws-iam-policy-simulator-now-simulates-permissions-boundary-policies)) — permissions-boundary evaluation confirmed GA since Jan 2020.

### Tertiary (LOW confidence — single WebSearch, not independently cross-checked)
- Whether `stscreds` is a subpackage of the `credentials` module vs. a separately-versioned module (Assumption A1) — the search result itself was hedged ("appears", "suggests"); flagged rather than asserted.
- The first WebFetch of `docs.terragrunt.com/reference/config-blocks-and-attributes` (before empirical testing) claimed the child `generate` block "completely overrides... no error occurs" — this was **directly contradicted** by running the actual pinned binary (Pattern 1). It is not cited as a source above because it was superseded by direct execution; it is noted here only as a documented instance of why this research did not stop at a single WebFetch for the load-bearing claim.

## Metadata

**Confidence breakdown:**
- Provider-override mechanism (REQ-126-LAUNCH core): HIGH — verified by direct execution against the exact pinned terragrunt binary, with four independent fixture variants and a repo-wide blast-radius check.
- Capacity store namespacing (REQ-126-CAPACITY): HIGH — verified by direct reading of the exact colliding function calls, down to the line level.
- Teardown discovery gap (REQ-126-TEARDOWN): HIGH for the diagnosis (direct code reading of `FindSandboxByID` and `destroy.go`'s call order); MEDIUM for the recommended fix shape (architecturally sound and precedented by `NetworkPlacement`, but not built/tested this session).
- `km account add` state-backend recommendation (REQ-126-ENROLL): MEDIUM — strong precedent-based reasoning (three in-repo examples of the same "standalone unit" idiom), but this is a genuine gap the design spec left open, not something with a single objectively-correct answer; flagged as Claude's Discretion, not a locked decision.
- `stscreds` idiomatic usage (REQ-126-CAPACITY): MEDIUM-HIGH — authoritative package documentation, cross-referenced across two independent lookups that agreed, composed against code already read directly in this repo.
- IAM containment test matrix (REQ-126-UAT): MEDIUM — AWS documentation confirms the mechanism (permissions-boundary evaluation), but the specific test matrix was constructed by this research from the design spec's own policy JSON, not independently run against a live account.

**Research date:** 2026-08-22
**Valid until:** 30 days for the Go/AWS SDK and codebase-structural findings (stable); reduce to 7 days for anything tied to the exact terragrunt version if the bundled version pin in `.goreleaser.yaml` changes before this phase is planned/executed — merge-strategy behavior is exactly the kind of thing that can shift across terragrunt releases, and this entire research's highest-value finding is tied to `v0.99.1` specifically.
