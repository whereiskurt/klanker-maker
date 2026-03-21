# Phase 1: Schema, Compiler & AWS Foundation - Research

**Researched:** 2026-03-21
**Domain:** Go CLI schema validation, JSON Schema 2020-12, profile inheritance, Terragrunt/OpenTofu module migration, AWS multi-account foundation
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Schema Validation**
- Strict validation: reject unknown fields (catches typos like 'lifecylce' immediately)
- All top-level spec sections are required (lifecycle, runtime, execution, sourceAccess, network, identity, sidecars, observability, policy, agent) — no hidden defaults
- Error messages use JSON path + message format: `spec.network.egress.allowedDNSSuffixes[0]: must be a valid DNS suffix pattern` — precise, grep-friendly, CI-ready
- Semantic checks included in v1 (not just schema structure): catch logical errors like spot: true on unsupported instance types, TTL shorter than idle timeout, etc.

**Profile Inheritance**
- Max inheritance depth: 3 levels (built-in → team base → workload-specific)
- `extends` references profiles by name only (not file path) — resolver looks up built-in profiles and a configurable search path
- Omitted sections in child profiles inherit parent's values — child only needs to specify overrides
- Child always wins on conflicts, no warning — simple override semantics, no noise

**Built-in Profiles**
- All four profiles route traffic through proxy sidecars (even open-dev) — enforcement is always on, profiles differ only in allowlist strictness
- All four sidecars enabled on all profiles (DNS proxy, HTTP proxy, audit log, tracing) — no graduated sidecar enablement
- Default TTLs: open-dev 24h, restricted-dev 8h, hardened 4h, sealed 1h
- Filesystem policy is independent of profile tier — sealed does NOT imply read-only filesystem
- Network policy graduation: open-dev has permissive allowlist, restricted-dev has curated allowlist, hardened has minimal egress (AWS APIs + specific hosts), sealed has zero egress

**Module Copy Strategy**
- Terraform modules go in `infra/modules/` (network, ec2spot, ecs-cluster, ecs-task, ecs-service, secrets)
- Rename variables/outputs from defcon naming to km naming; strip features not needed for sandboxes (e.g. ALB from network module); keep core logic intact
- ConfigUI Go code deferred to Phase 5 — not copied in Phase 1
- Full Terragrunt hierarchy copied: site.hcl, service.hcl, and the full live/ directory structure from defcon.run.34

### Claude's Discretion
- Exact JSON Schema Draft 2020-12 structure and composition
- Go struct design for SandboxProfile types
- Compiler output format (JSON tfvars vs HCL)
- Specific semantic validation rules beyond the examples discussed
- Profile search path resolution implementation details

### Deferred Ideas (OUT OF SCOPE)
None — discussion stayed within phase scope
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| SCHM-01 | Operator can define a SandboxProfile in YAML with apiVersion, kind, metadata, spec sections | Go struct layout + goccy/go-yaml parsing; Kubernetes-style apiVersion/kind/metadata/spec |
| SCHM-02 | Schema supports lifecycle, runtime, execution, sourceAccess, network, identity, sidecars, observability, policy, and agent sections | JSON Schema Draft 2020-12 composition with santhosh-tekuri/jsonschema/v6; strict additionalProperties: false |
| SCHM-03 | Operator can run `km validate <profile.yaml>` and get clear error messages for invalid profiles | santhosh-tekuri/jsonschema/v6 produces structured ValidationError with instance path; goccy/go-yaml path-based error messages; Cobra command wiring |
| SCHM-04 | Profile can extend a base profile via `extends` field, inheriting and overriding specific sections | Depth-first inheritance resolver with cycle detection; child-wins merge semantics for allowlists (override, not union) |
| SCHM-05 | Four built-in profiles ship with Klanker Maker: open-dev, restricted-dev, hardened, sealed | Embedded via go:embed; network policy graduation: open-dev permissive → sealed zero egress |
| INFR-01 | AWS multi-account setup: management account, terraform account, application account | Terragrunt site.hcl global config pattern from defcon.run.34; S3 backend with per-account state prefix |
| INFR-02 | AWS SSO configured for operator access across accounts | AWS Identity Center (SSO) SAML/OIDC config; Terragrunt provider config with SSO profile |
| INFR-03 | Route53 hosted zone configured in management account, delegated to application account | NS delegation: parent zone in mgmt account, child zone in app account; Terragrunt site/global/route53 module |
| INFR-04 | KMS keys provisioned for SOPS encryption | aws_kms_key resource with multi-region replica; SOPS .sops.yaml pointing at key ARN |
| INFR-05 | S3 buckets for artifacts with lifecycle policies and cross-region replication | S3 bucket + aws_s3_bucket_replication_configuration; s3-uploads module adapted from defcon.run.34 |
| INFR-06 | Terragrunt per-sandbox directory isolation (no workspace sharing) | Per-sandbox `infra/live/sandboxes/<sandbox-id>/terragrunt.hcl`; S3 state key includes sandbox-id |
| INFR-07 | Domain registered in management account and connected to application account | Route53 registrar in mgmt account; NS records copied to child hosted zone |
| INFR-08 | All modules from defcon.run.34 copied into repo, renamed, no cross-repo dependency | Six modules: network, ec2spot, ecs-cluster, ecs-task, ecs-service, secrets; Terragrunt live/ hierarchy; all defcon.run.34 references replaced with km references |
</phase_requirements>

---

## Summary

Phase 1 has two parallel work streams: (1) the Go package for SandboxProfile schema validation and profile inheritance, and (2) the AWS foundation infrastructure (multi-account, KMS, S3, Route53) plus copying and renaming all Terraform modules from defcon.run.34. Neither stream blocks the other — they converge in Phase 2 when the compiler needs both the schema types and the module variable contracts to generate correct Terragrunt inputs.

The schema layer is straightforward: define Go structs matching the Kubernetes-style apiVersion/kind/metadata/spec envelope, write a JSON Schema document for external validation with `santhosh-tekuri/jsonschema/v6`, and implement the inheritance resolver as a graph walk with cycle detection. The locked decision to use name-based (not path-based) extends resolution means built-in profiles are the canonical search root; operators extend them by name. The four built-in profiles differ only in allowlist strictness — the structure is identical, so defining them after the schema is locked is a write-once task.

The Terraform/Terragrunt stream is a module migration job, not a design job. The defcon.run.34 modules are at `~/working/defcon.run.34/infra/terraform/modules/` and the live Terragrunt hierarchy is at `infra/terraform/live/site/`. Modules use versioned subdirectories (`v1.0.0/`). Each module needs variable renaming (drop `site.*` and `dns.*` in favor of `sandbox_id`, `km_env`, etc.) and feature trimming (remove ALB from network, remove DNS record creation from ec2spot, remove SSH key generation from ec2spot in favor of SSM-only access). The Terragrunt hierarchy is copied and the root `site.hcl` is re-rooted to reference `CLAUDE.md` or another km-specific anchor instead of `AGENTS.md`.

**Primary recommendation:** Start the Go schema package and the Terragrunt copy in parallel. The schema package has no AWS dependencies; the module copy has no Go dependencies. Both must be complete before Phase 2 begins.

---

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| github.com/spf13/cobra | v1.10.2 | CLI command tree (`km validate`) | De facto standard for production Go CLIs; tiogo architecture already patterns against it |
| github.com/spf13/viper | v1.21.0 | Config file + env var binding | Hierarchical config with zero boilerplate; integrates with cobra via BindPFlags |
| github.com/goccy/go-yaml | v1.x | YAML parsing for SandboxProfile | go-yaml/yaml (gopkg.in) is archived; goccy/go-yaml is the actively maintained replacement with path-based error messages |
| github.com/santhosh-tekuri/jsonschema/v6 | v6.0.2 | JSON Schema Draft 2020-12 validation | Only Go library with full Draft 2020-12 compliance; structured errors with JSON pointer paths; preferred over struct-tag validators because the schema is user-facing/declarative |
| github.com/rs/zerolog | v1.34.0 | Structured logging | Zero-allocation JSON; better than logrus (maintenance mode) for new Go projects |
| github.com/google/uuid | v1.x | Sandbox ID generation | Standard UUID v4 generation for unique sandbox identifiers |
| OpenTofu | 1.9.1 | IaC engine | MPL 2.0 fork of Terraform; drop-in replacement; Terraform OSS BSL is EOL July 2025 |
| Terragrunt | 0.77.x | Orchestrates OpenTofu modules, per-directory isolation | Proven pattern from defcon.run.34; `km create` compiles profile to Terragrunt inputs |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| github.com/hashicorp/hcl/v2 | v2.x | Parse/write Terragrunt HCL inputs | When compiler generates JSON .tfvars.json files — use this for any HCL reading, avoid string interpolation |
| github.com/zclconf/go-cty | v1.x | HCL type system | Required by hcl/v2 |
| golang.org/x/sys | latest | Low-level syscalls | If any OS-level filesystem work is needed in schema resolution |
| tenv | latest | OpenTofu + Terragrunt version manager | Replaces tfenv/tofuenv; `brew install tenv` on macOS |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| santhosh-tekuri/jsonschema/v6 | go-playground/validator | validator is struct-tag only — not suitable for a user-facing YAML schema that must produce field-path errors; do not use |
| santhosh-tekuri/jsonschema/v6 | gojsonschema (xeipuuv) | only supports draft v4/v6/v7; last updated 2021; do not use |
| goccy/go-yaml | gopkg.in/yaml.v3 | archived/unmaintained; do not use |
| OpenTofu 1.9.1 | Terraform 1.x (BSL) | BSL Terraform is EOL July 2025; do not use in new open-source projects |
| zerolog | logrus | logrus is maintenance mode; tiogo used it but zerolog is the current recommendation |
| JSON tfvars (.tfvars.json) | Generated HCL string interpolation | HCL has quoting/escaping rules that break when generated by fmt.Sprintf; JSON tfvars are unambiguous and Terraform/OpenTofu accepts them natively |

**Installation:**
```bash
# Go CLI — core schema packages
go get github.com/spf13/cobra@v1.10.2
go get github.com/spf13/viper@v1.21.0
go get github.com/rs/zerolog@v1.34.0
go get github.com/goccy/go-yaml@latest
go get github.com/santhosh-tekuri/jsonschema/v6@v6.0.2
go get github.com/google/uuid@latest
go get github.com/hashicorp/hcl/v2@latest
go get github.com/zclconf/go-cty@latest

# Infrastructure tooling (macOS)
brew install tenv
tenv tofu install 1.9.1
tenv tg install latest  # Terragrunt 0.77.x
```

---

## Architecture Patterns

### Recommended Project Structure (Phase 1 scope)

```
klankrmkr/
├── cmd/
│   └── km/
│       └── main.go                  # Binary entry point
├── internal/
│   └── app/
│       ├── cmd/
│       │   └── validate.go          # km validate <profile.yaml>
│       └── config/
│           └── config.go            # Central Config struct (Viper-backed)
├── pkg/
│   └── profile/
│       ├── types.go                 # Go struct definitions (SandboxProfile, Spec, all sections)
│       ├── schema.go                # Embedded JSON Schema (//go:embed sandbox_profile.schema.json)
│       ├── validate.go              # Schema + semantic validation logic
│       ├── inherit.go               # extends resolution: name lookup + cycle detection + merge
│       └── builtins.go              # Load/embed built-in profiles (//go:embed profiles/*.yaml)
├── profiles/                        # Built-in SandboxProfile YAML templates
│   ├── open-dev.yaml
│   ├── restricted-dev.yaml
│   ├── hardened.yaml
│   └── sealed.yaml
├── schemas/
│   └── sandbox_profile.schema.json  # JSON Schema Draft 2020-12 document
└── infra/
    ├── modules/                     # Copied + renamed from defcon.run.34
    │   ├── network/
    │   │   └── v1.0.0/              # Trimmed: no ALB; kept: VPC, subnets, SGs, NAT GW opt
    │   ├── ec2spot/
    │   │   └── v1.0.0/              # Trimmed: no SSH key pair, no DNS records; added: km:sandbox-id tag, IMDSv2 required
    │   ├── ecs-cluster/
    │   │   └── v1.0.0/
    │   ├── ecs-task/
    │   │   └── v1.0.0/
    │   ├── ecs-service/
    │   │   └── v1.0.0/
    │   └── secrets/
    │       └── v1.0.0/              # Kept: KMS, SSM Parameter Store; stripped: Secrets Manager (defer to Phase 2)
    └── live/
        ├── site.hcl                 # km-specific root config (zone, backend, provider)
        └── sandboxes/               # Per-sandbox Terragrunt directories (written by compiler in Phase 2)
            └── _template/
                └── terragrunt.hcl   # Template showing expected input variables
```

### Pattern 1: SandboxProfile Go Struct Layout

**What:** Define Go structs that mirror the JSON Schema document exactly. The YAML unmarshals into these structs; the JSON Schema validates the same document externally. Both representations must stay in sync.

**When to use:** Always — the struct is the in-memory type; the JSON Schema is the user-facing contract.

**Example:**
```go
// pkg/profile/types.go
// Source: project design; apiVersion modeled on Kubernetes API machinery conventions

package profile

type SandboxProfile struct {
    APIVersion string   `yaml:"apiVersion"` // "klankermaker.ai/v1alpha1"
    Kind       string   `yaml:"kind"`       // "SandboxProfile"
    Metadata   Metadata `yaml:"metadata"`
    Extends    string   `yaml:"extends,omitempty"` // profile name, not path
    Spec       Spec     `yaml:"spec"`
}

type Metadata struct {
    Name   string            `yaml:"name"`
    Labels map[string]string `yaml:"labels,omitempty"`
}

type Spec struct {
    Lifecycle    LifecycleSpec    `yaml:"lifecycle"`
    Runtime      RuntimeSpec      `yaml:"runtime"`
    Execution    ExecutionSpec    `yaml:"execution"`
    SourceAccess SourceAccessSpec `yaml:"sourceAccess"`
    Network      NetworkSpec      `yaml:"network"`
    Identity     IdentitySpec     `yaml:"identity"`
    Sidecars     SidecarsSpec     `yaml:"sidecars"`
    Observability ObservabilitySpec `yaml:"observability"`
    Policy       PolicySpec       `yaml:"policy"`
    Agent        AgentSpec        `yaml:"agent"`
}

type RuntimeSpec struct {
    Substrate string `yaml:"substrate"` // "ec2" | "ecs"
    Spot      bool   `yaml:"spot"`
    // ... instance type, region constraints, etc.
}
```

### Pattern 2: JSON Schema Validation with santhosh-tekuri/jsonschema/v6

**What:** Validate YAML documents against a JSON Schema Draft 2020-12 document. Convert YAML to JSON (via goccy/go-yaml), then run schema validation. Extract structured error paths for user-facing messages.

**When to use:** In `km validate` and as a pre-flight inside every `km create`.

**Example:**
```go
// pkg/profile/validate.go
// Source: https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6

package profile

import (
    "github.com/santhosh-tekuri/jsonschema/v6"
)

func ValidateSchema(raw []byte) error {
    // 1. Parse YAML to interface{} via goccy/go-yaml
    // 2. Re-encode to JSON
    // 3. Validate against embedded schema
    compiler := jsonschema.NewCompiler()
    schema, err := compiler.Compile("sandbox_profile.schema.json")
    if err != nil {
        return err
    }
    var doc interface{}
    // ... unmarshal and validate
    if err := schema.Validate(doc); err != nil {
        var ve *jsonschema.ValidationError
        if errors.As(err, &ve) {
            // ve.InstanceLocation gives JSON pointer: "/spec/network/egress/allowedDNSSuffixes/0"
            // Format as: "spec.network.egress.allowedDNSSuffixes[0]: must be a valid DNS suffix pattern"
        }
    }
    return nil
}
```

### Pattern 3: Profile Inheritance Resolver

**What:** Load a profile's `extends` chain depth-first, detect cycles, then merge: child fields override parent fields for all scalar values and for allowlist arrays. Metadata labels are additive (the only exception).

**When to use:** Before validation — inheritance must be resolved first so the merged document is validated as a whole.

**Example:**
```go
// pkg/profile/inherit.go

func Resolve(name string, searchPaths []string) (*SandboxProfile, error) {
    return resolve(name, searchPaths, nil, 0)
}

func resolve(name string, searchPaths []string, visited map[string]bool, depth int) (*SandboxProfile, error) {
    if depth > 3 { // max depth: 3 per locked decision
        return nil, fmt.Errorf("inheritance depth exceeded (max 3): resolving %q", name)
    }
    if visited[name] {
        return nil, fmt.Errorf("circular inheritance detected: %q", name)
    }
    visited[name] = true

    profile, err := load(name, searchPaths)
    if err != nil {
        return nil, err
    }

    if profile.Extends == "" {
        return profile, nil
    }

    parent, err := resolve(profile.Extends, searchPaths, visited, depth+1)
    if err != nil {
        return nil, err
    }

    return merge(parent, profile), nil // child wins; allowlists REPLACE not extend
}
```

### Pattern 4: Terragrunt Module Structure (copied from defcon.run.34)

**What:** Modules use `v1.0.0/` versioned subdirectories. Terragrunt `terragrunt.hcl` files in the live directory include a `config.hcl` from the module directory that exposes the module path. The root `site.hcl` defines global config consumed by all modules.

**When to use:** Always — this is the established Terragrunt pattern from defcon.run.34 that Klanker Maker inherits.

**Key change from defcon.run.34:** Replace `find_in_parent_folders("AGENTS.md")` with `find_in_parent_folders("CLAUDE.md")` as the repo root anchor. Replace `label = "dc34"` with `label = "km"`. Replace `tf_state_prefix = "tf-dc34"` with `tf_state_prefix = "tf-km"`.

**Example site.hcl skeleton:**
```hcl
# infra/live/site.hcl
locals {
  site = {
    label           = "km"
    tf_state_prefix = "tf-km"
    random_suffix   = get_env("KMGUID", "")
  }

  dns = {
    zonename   = get_env("KM_DOMAIN", "")
    subdomains = []
    ttl        = 300
  }

  secret_values = jsondecode(
    fileexists("${get_terragrunt_dir()}/.secrets.sops.json")
    ? run_cmd("--terragrunt-quiet", "sops", "--decrypt", "${get_terragrunt_dir()}/.secrets.sops.json")
    : fileexists("${get_terragrunt_dir()}/.secrets.json")
    ? file("${get_terragrunt_dir()}/.secrets.json")
    : "{}"
  )
}
```

### Anti-Patterns to Avoid

- **Generating HCL by string interpolation:** HCL has quoting/escaping rules that differ from JSON. Use `hashicorp/hcl/v2` for reading HCL, and generate `.tfvars.json` (not `.tfvars`) for compiler outputs — JSON has unambiguous escaping rules and Terraform/OpenTofu accepts it natively.
- **Additive merge on allowlist arrays in inheritance:** If child allowlists are unioned with parent allowlists, a child profile can never be more restrictive than its parent. The merge must be: child's array REPLACES parent's array for all allowlist fields.
- **Using YAML `<<:` merge key for inheritance:** YAML 1.2 deprecated the merge key; it only works for mappings (not sequences), and goccy/go-yaml behavior for sequences with `<<:` is undefined. Implement inheritance in Go code, not in YAML syntax.
- **Recursive resolve without a visited set:** Profile A extending B extending A causes infinite recursion and a goroutine stack overflow. Always carry a visited set into recursive calls.
- **SSH key pairs in the ec2spot module:** The defcon.run.34 ec2spot module generates TLS private keys and stores them in Terraform state (plaintext in S3 under the state file). For sandbox modules, remove `tls_private_key`, `aws_key_pair`, and `local_file` resources. Use SSM Session Manager for access instead.
- **`0.0.0.0/0` egress in security groups:** The defcon.run.34 ec2spot module allows all outbound traffic in its security group. This is the critical pitfall from project research — it allows proxy bypass. The km module must restrict egress to proxy ports only. This is a Phase 2 concern for the actual SG rules, but the module skeleton must not hard-code open egress.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| YAML parsing with path-aware errors | Custom YAML walker | github.com/goccy/go-yaml | Path-based error messages are built in; 60+ more spec compliance fixes vs archived yaml.v3 |
| JSON Schema validation | Struct tag validators, custom rule engine | github.com/santhosh-tekuri/jsonschema/v6 | Draft 2020-12 compliance; structured ValidationError with JSON pointer instance paths; ships `jv` CLI for schema debugging |
| CLI command tree | Manual flag parsing, custom dispatch | github.com/spf13/cobra | Provides persistent flags, subcommand nesting, help text generation, shell completion — defects here waste hours |
| Inheritance cycle detection | None (assume "users won't do this") | Go DFS with visited map (hand-rolled, trivial) | A recursive load without cycle detection causes goroutine stack overflow on first circular profile |
| OpenTofu version management | Manual binary download | tenv | Manages multiple OpenTofu + Terragrunt versions per-project; `.opentofu-version` file in repo pins version |
| Terraform module authoring from scratch | Writing new VPC/EC2/ECS HCL from scratch | Copy + adapt from defcon.run.34 | All six required modules (network, ec2spot, ecs-cluster, ecs-task, ecs-service, secrets) are already tested and working; adaptation takes days, not weeks |
| SOPS secret decryption in Terragrunt | Custom secret loader | `run_cmd("sops", "--decrypt", ...)` in site.hcl | defcon.run.34 already uses this pattern; SOPS has native KMS integration |

**Key insight:** The JSON Schema validation and YAML parsing libraries do the most complex work of Phase 1 (field-path error messages, schema composition, keyword support). The inheritance resolver is the only substantive custom code — and it is a straightforward depth-first graph walk, not a novel algorithm.

---

## Common Pitfalls

### Pitfall 1: Allowlist Array Merge Semantics — Child Extends Instead of Overriding Parent

**What goes wrong:** When compiling `spec.network.egress.allowedDomains`, the merger does a union of parent and child arrays. A "hardened" child of "restricted-dev" that specifies `allowedDomains: []` still inherits all of restricted-dev's allowed domains because the union of anything with a non-empty list is non-empty.

**Why it happens:** The YAML deep-merge idiom (recursive map merge) naturally unions sequences. Go's `reflect.DeepEqual`-based mergers do the same. "Override semantics for arrays" must be explicitly coded.

**How to avoid:** In the merge function, for any field of type `[]string` that represents an allowlist (domains, actions, repos, secrets, suffixes), always take the child value if it is non-nil, even if it is an empty slice. An empty child allowlist means "zero allowed," not "inherit parent." Add a test: child with `allowedDomains: []` merged with open-dev must produce an empty allowed list.

**Warning signs:** Compiler test showing that a "sealed" child (zero egress) of "open-dev" still has allowed domains in its compiled output.

### Pitfall 2: Circular Inheritance Causes Goroutine Stack Overflow

**What goes wrong:** Profile A has `extends: B`, B has `extends: A`. The loader calls `resolve("A")` → `resolve("B")` → `resolve("A")` and crashes with a goroutine stack overflow (Go's default 1GB goroutine stack eventually exhausts). The error message is a runtime panic, not a user-friendly validation error.

**Why it happens:** Recursive resolution without a visited set or depth limit.

**How to avoid:** Carry a `visited map[string]bool` (or a `[]string` path for error reporting) into the recursive resolve call. Return a structured error on revisit: `circular inheritance: A → B → A`. Also enforce the max-depth limit (3 per locked decision) as a separate check — depth limit catches chains that aren't circular but are too long.

**Warning signs:** `km validate` hangs or panics rather than returning an error for a circular profile YAML.

### Pitfall 3: defcon.run.34 Module Variables Are Site-Specific, Not Sandbox-Specific

**What goes wrong:** The copied modules have variables like `site.label`, `site.random_suffix`, `dns.zonename`, `dns.subdomains`. These make sense for a multi-service site (defcon.run.34) but not for a per-sandbox provisioning model. If left unchanged, every sandbox share the same `site.label` value in resource names, causing name collisions when two sandboxes provision simultaneously.

**Why it happens:** Module copy without variable contract redesign. The variables are renamed but not restructured for the sandbox use case.

**How to avoid:** When adapting modules, replace the `site.*` and `dns.*` input variables with sandbox-scoped equivalents:
- `site.label` → `sandbox_id` (UUID, unique per sandbox)
- `site.random_suffix` → remove (sandbox_id already unique)
- `dns.zonename` → `km_domain` (global config from site.hcl, not per-sandbox)
- `dns.subdomains` → remove (not relevant for sandboxes)
- Add `km_env` (e.g., "dev", "prod") as a tagging variable

### Pitfall 4: ec2spot Module Stores SSH Private Keys in Terraform State

**What goes wrong:** The defcon.run.34 ec2spot module creates `tls_private_key` and `aws_key_pair` resources, then saves the private key to a local file. The private key is stored in plaintext in the Terraform state file (S3). Anyone with `s3:GetObject` access to the state bucket can read all sandbox SSH keys.

**Why it happens:** Inherited pattern not audited before copying.

**How to avoid:** When copying the ec2spot module, remove:
- `resource "tls_private_key" "ec2spot"` — generates RSA key in state
- `resource "aws_key_pair" "ec2spot"` — uploads public key to EC2
- `resource "local_file" "ec2spot_key"` — writes private key to disk

Use SSM Session Manager (`AmazonSSMManagedInstanceCore` policy, already in the module) for all instance access. Do not create EC2 key pairs for sandboxes.

### Pitfall 5: JSON Schema `additionalProperties: false` Conflicts with Unknown Extensions

**What goes wrong:** The locked decision requires strict validation (reject unknown fields). If a team adds a future extension field (`spec.myCustomField`) the schema will reject it until the schema is updated. This is intentional, but it creates a friction point at schema upgrade time.

**Why it happens:** Strict schemas are not a pitfall per se, but the upgrade path must be planned.

**How to avoid:** Version the schema with the apiVersion field (`klankermaker.ai/v1alpha1`). When new fields are added, they are added to the schema document and the Go structs simultaneously. The schema is embedded in the binary (`//go:embed schemas/sandbox_profile.schema.json`), so upgrading the schema means recompiling the binary. Document this contract explicitly.

### Pitfall 6: Terragrunt Root Anchor Uses AGENTS.md — Breaks in km Repo

**What goes wrong:** The defcon.run.34 Terragrunt config uses `find_in_parent_folders("AGENTS.md")` to locate the repo root. This file does not exist in the klankrmkr repo, so Terragrunt will fail to find the root and all relative paths in site.hcl break.

**Why it happens:** Direct copy-paste of Terragrunt patterns without auditing the root anchor file.

**How to avoid:** When copying site.hcl and any terragrunt.hcl that uses `find_in_parent_folders`, replace `"AGENTS.md"` with `"CLAUDE.md"` (or another repo-root marker that exists in the klankrmkr repo). Search all copied HCL files for `AGENTS.md` as part of the copy task.

---

## Code Examples

Verified patterns from official sources:

### JSON Schema Draft 2020-12 with santhosh-tekuri/jsonschema/v6

```go
// Source: https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6

package profile

import (
    "embed"
    "encoding/json"
    "fmt"

    "github.com/goccy/go-yaml"
    "github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed ../../schemas/sandbox_profile.schema.json
var schemaFS embed.FS

func validateSchema(rawYAML []byte) []ValidationError {
    // Step 1: YAML → Go interface{}
    var doc interface{}
    if err := yaml.Unmarshal(rawYAML, &doc); err != nil {
        return []ValidationError{{Path: "<root>", Message: err.Error()}}
    }

    // Step 2: interface{} → JSON bytes (jsonschema/v6 validates JSON)
    jsonBytes, err := json.Marshal(doc)
    if err != nil {
        return []ValidationError{{Path: "<root>", Message: err.Error()}}
    }

    // Step 3: Compile schema and validate
    compiler := jsonschema.NewCompiler()
    schemaData, _ := schemaFS.ReadFile("schemas/sandbox_profile.schema.json")
    _ = compiler.AddResource("sandbox_profile.schema.json", bytes.NewReader(schemaData))
    schema, _ := compiler.Compile("sandbox_profile.schema.json")

    var inst interface{}
    _ = json.Unmarshal(jsonBytes, &inst)

    if err := schema.Validate(inst); err != nil {
        var ve *jsonschema.ValidationError
        if errors.As(err, &ve) {
            return formatErrors(ve)
        }
    }
    return nil
}

func formatErrors(ve *jsonschema.ValidationError) []ValidationError {
    // ve.InstanceLocation: JSON pointer like "/spec/network/egress/allowedDNSSuffixes/0"
    // Convert to dot notation: "spec.network.egress.allowedDNSSuffixes[0]"
    path := jsonPointerToDotNotation(ve.InstanceLocation)
    return []ValidationError{{Path: path, Message: ve.Message}}
}
```

### km validate Cobra Command

```go
// internal/app/cmd/validate.go
// Source: tiogo architecture pattern (https://github.com/whereiskurt/tiogo)

package cmd

import (
    "fmt"
    "os"

    "github.com/spf13/cobra"
    "github.com/klankrmkr/klankrmkr/pkg/profile"
)

func NewValidateCmd(cfg *config.Config) *cobra.Command {
    return &cobra.Command{
        Use:   "validate <profile.yaml>",
        Short: "Validate a SandboxProfile YAML file",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            raw, err := os.ReadFile(args[0])
            if err != nil {
                return fmt.Errorf("reading profile: %w", err)
            }

            errs := profile.Validate(raw)
            if len(errs) == 0 {
                fmt.Println("profile is valid")
                return nil
            }

            for _, e := range errs {
                fmt.Fprintf(os.Stderr, "%s: %s\n", e.Path, e.Message)
            }
            return fmt.Errorf("profile has %d validation error(s)", len(errs))
        },
    }
}
```

### Built-in Profile YAML Structure (open-dev example)

```yaml
# profiles/open-dev.yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: open-dev
  labels:
    tier: permissive
spec:
  lifecycle:
    ttl: 24h
    idleTimeout: 2h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    spot: true
    instanceType: t4g.medium
  execution:
    shell: /bin/bash
    workdir: /workspace
  sourceAccess:
    github:
      allowedRepos: ["*"]
      allowedRefs: ["*"]
      allowedPermissions: ["clone", "fetch"]
  network:
    egress:
      allowedDomains: ["*"]
      allowedDNSSuffixes: ["*"]
  identity:
    aws:
      sessionDuration: 1h
      allowedActions: ["s3:GetObject", "s3:PutObject", "ssm:GetParameter"]
  sidecars:
    dnsProxy: {enabled: true}
    httpProxy: {enabled: true}
    auditLog: {enabled: true}
    tracing: {enabled: true}
  observability:
    logDestination: cloudwatch
  policy:
    filesystem: {}
  agent: {}
```

### Terragrunt Module Adaptation: Variable Renaming

The ec2spot module variables must be restructured when copying. The defcon.run.34 pattern passes a rich `site` object and `ec2spots` list (multi-instance, multi-region). For km, each sandbox is a single instance with a known sandbox_id:

```hcl
# infra/modules/ec2spot/v1.0.0/variables.tf (km version)
# Source: adapted from defcon.run.34/infra/terraform/modules/ec2spot/v1.0.0/variables.tf

variable "sandbox_id" {
  type        = string
  description = "Unique sandbox identifier (UUID). Used in resource names and tags."
}

variable "km_env" {
  type        = string
  description = "Klanker Maker environment label (e.g. dev, prod)"
  default     = "dev"
}

variable "instance_type" {
  type        = string
  description = "EC2 instance type"
  default     = "t4g.medium"
}

variable "spot" {
  type        = bool
  description = "Use spot instance (true) or on-demand (false)"
  default     = true
}

variable "vpc_id" {
  type        = string
  description = "VPC ID where the sandbox instance will be created"
}

variable "subnet_id" {
  type        = string
  description = "Subnet ID for the sandbox instance"
}

variable "iam_instance_profile" {
  type        = string
  description = "IAM instance profile name (created by iam module)"
}

variable "user_data" {
  type        = string
  description = "EC2 user-data bootstrap script (generated by profile compiler)"
  default     = ""
}

variable "region" {
  type        = string
  description = "AWS region full name (e.g. us-east-1)"
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Terraform OSS (MPL → BSL) | OpenTofu 1.9.1 (MPL 2.0) | July 2025 (Terraform BSL EOL) | New projects must use OpenTofu; identical HCL and provider compatibility |
| gopkg.in/yaml.v3 | github.com/goccy/go-yaml | 2024-2025 (yaml.v3 archived) | go-yaml/yaml is archived; cobra migrated away; goccy provides path-based errors |
| go-playground/validator for YAML schemas | santhosh-tekuri/jsonschema/v6 | When Draft 2020-12 support needed | Struct-tag validators can't express user-facing JSON Schema; jsonschema/v6 is the only Go Draft 2020-12 library |
| Terraform workspaces for environment isolation | Terragrunt per-directory pattern | Post-2022 pattern solidified | Workspaces share backend config and are not a security boundary; per-directory gives S3 key isolation |
| logrus | zerolog | 2023+ (logrus maintenance mode) | logrus gets no new features; zerolog is zero-allocation JSON; tiogo used logrus but km should use zerolog |

**Deprecated/outdated:**
- `gopkg.in/yaml.v3`: archived; do not use in new Go code
- `github.com/aws/aws-sdk-go` (v1): EOL; use v2 (`github.com/aws/aws-sdk-go-v2`)
- `gojsonschema` (xeipuuv): last updated 2021; supports only draft v4/v6/v7; do not use
- Terraform workspaces for per-sandbox isolation: use Terragrunt per-directory instead
- `tls_private_key` + `aws_key_pair` in ec2spot: security anti-pattern; use SSM Session Manager instead

---

## Open Questions

1. **Semantic validation rules for v1**
   - What we know: locked decision requires semantic checks beyond structure (spot on unsupported instance types, TTL shorter than idle timeout)
   - What's unclear: complete enumeration of semantic rules for the initial implementation
   - Recommendation: Define a minimum set of semantic rules during planning — at minimum: (a) `lifecycle.ttl` >= `lifecycle.idleTimeout`, (b) `runtime.substrate` must be "ec2" or "ecs", (c) if `runtime.spot: false`, `runtime.spotFallback` setting is irrelevant. Additional semantic rules can be added incrementally.

2. **Profile search path for extends resolution**
   - What we know: locked as Claude's discretion; built-ins are searched first, then a configurable path
   - What's unclear: where the configurable search path is configured (env var, config file, CLI flag?)
   - Recommendation: Use a `KM_PROFILE_PATH` environment variable (colon-separated directories, analogous to `PATH`) with a sensible default of `~/.km/profiles`. Built-in profiles are always searched first via the embedded FS.

3. **Compiler output format for Terragrunt inputs**
   - What we know: locked as Claude's discretion between JSON tfvars and HCL
   - What's unclear: which is simpler to generate and consume
   - Recommendation: Use JSON `.tfvars.json` format. OpenTofu accepts these natively, JSON has unambiguous escaping rules (no HCL string interpolation pitfalls), and `encoding/json` in Go is a stdlib dependency.

4. **AWS multi-account bootstrapping automation level**
   - What we know: INFR-01 through INFR-07 require management, terraform, and application accounts with SSO, Route53, KMS, S3
   - What's unclear: how much of this is manual AWS console work vs. automated Terraform
   - Recommendation: For Phase 1, the Terragrunt modules for the foundation infrastructure (KMS, S3, Route53 delegation) should be present and runnable. The initial AWS account creation and SSO enablement are manual one-time steps documented in an operator runbook. Automate as much as possible in Terraform but document manual steps clearly.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing + testify (standard for Go projects) |
| Config file | none — standard `go test ./...` |
| Quick run command | `go test ./pkg/profile/... -v` |
| Full suite command | `go test ./... -v` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| SCHM-01 | Valid SandboxProfile YAML with all required envelope fields parses without error | unit | `go test ./pkg/profile/... -run TestParse` | ❌ Wave 0 |
| SCHM-02 | All 10 spec sections present and unknown fields rejected | unit | `go test ./pkg/profile/... -run TestSchemaValidation` | ❌ Wave 0 |
| SCHM-03 | Invalid profile returns structured error with JSON path | unit | `go test ./pkg/profile/... -run TestValidateErrors` | ❌ Wave 0 |
| SCHM-04 | Child profile inherits parent values; child values override; circular extends returns error | unit | `go test ./pkg/profile/... -run TestInheritance` | ❌ Wave 0 |
| SCHM-05 | All four built-in profiles load and pass validation | unit | `go test ./pkg/profile/... -run TestBuiltinProfiles` | ❌ Wave 0 |
| INFR-01 through INFR-08 | AWS infrastructure provisioned per spec; modules present with no defcon references | manual / smoke | `grep -r "defcon" infra/` returns empty | ❌ Wave 0 |

### Sampling Rate

- **Per task commit:** `go test ./pkg/profile/... -v`
- **Per wave merge:** `go test ./... -v`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/profile/types_test.go` — covers SCHM-01 (parse valid profile)
- [ ] `pkg/profile/validate_test.go` — covers SCHM-02, SCHM-03 (schema validation, error paths)
- [ ] `pkg/profile/inherit_test.go` — covers SCHM-04 (inheritance, cycle detection, depth limit)
- [ ] `pkg/profile/builtins_test.go` — covers SCHM-05 (four built-in profiles parse and validate)
- [ ] `go.mod` / `go.sum` — no Go module initialized yet; required before any Go code compiles
- [ ] `schemas/sandbox_profile.schema.json` — schema document must exist before validate tests run
- [ ] `profiles/open-dev.yaml`, `restricted-dev.yaml`, `hardened.yaml`, `sealed.yaml` — built-in profile files must exist before builtins tests run

---

## Sources

### Primary (HIGH confidence)

- https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6 — v6.0.2 (May 2025), JSON Schema Draft 2020-12 compliance, ValidationError structure, instance location path format
- https://pkg.go.dev/github.com/spf13/cobra — v1.10.2, verified live; cobra command pattern for `km validate`
- https://pkg.go.dev/github.com/goccy/go-yaml — actively maintained replacement for go-yaml/yaml; path-based error messages confirmed
- defcon.run.34 infra/terraform/modules/ — directly inspected: ec2spot/v1.0.0/main.tf, network/v1.0.0/variables.tf, ecs-task/v1.0.0/variables.tf, secrets/v1.0.0/ structure; site.hcl pattern, terragrunt.hcl pattern
- .planning/research/STACK.md — prior verified stack research; all libraries verified via pkg.go.dev
- .planning/research/ARCHITECTURE.md — prior architecture research; build order, component boundaries, data flow

### Secondary (MEDIUM confidence)

- .planning/research/PITFALLS.md — pitfall analysis with source attributions; critical pitfalls sourced from authoritative security research
- .planning/research/SUMMARY.md — project-level summary cross-referencing all research
- https://docs.terragrunt.com/reference/supported-versions — OpenTofu 1.9.1 + Terragrunt 0.77.x compatibility confirmed

### Tertiary (LOW confidence)

- None for Phase 1 scope — all critical findings are HIGH or MEDIUM confidence

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries verified via pkg.go.dev with live version checks; prior project research confirmed
- Architecture: HIGH — grounded in direct inspection of defcon.run.34 modules; tiogo Go CLI architecture patterns
- Pitfalls: HIGH (schema/inheritance pitfalls from Go type system knowledge; confirmed patterns); MEDIUM (module adaptation pitfalls from direct code inspection)
- AWS foundation (INFR-01 to INFR-08): MEDIUM — standard AWS patterns; specific account bootstrapping steps depend on operator's existing account structure

**Research date:** 2026-03-21
**Valid until:** 2026-04-21 (stable libraries; OpenTofu and Terragrunt version pins should be re-verified before planning if more than 30 days pass)
