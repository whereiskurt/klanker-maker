# Phase 66: Multi-instance Support (resource_prefix + email_subdomain) — Research

**Researched:** 2026-05-01
**Domain:** Go config struct extension, Terraform module parameterization, Terragrunt site.hcl threading, Lambda env-var hygiene
**Confidence:** HIGH

---

## Summary

Phase 66 introduces two knobs — `resource_prefix` (default `"km"`) and `email_subdomain` (default `"sandboxes"`) — that together make every account-globally-unique AWS resource name and every email address derivable from config rather than hardcoded. The change is purely additive: with defaults in place, the existing install produces identical resource names.

The work divides cleanly into five layers: (1) config struct + helper methods, (2) Go call-site migration for email domain, (3) Go call-site migration for resource names (table names, Lambda names, SSM paths, log groups), (4) Terraform module parameterization (Lambda/EventBridge modules do NOT yet accept a prefix variable; DynamoDB modules already do), and (5) Terragrunt live config + site.hcl threading.

The heaviest change is in layer 4: the five management Lambda modules (`create-handler`, `ttl-handler`, `email-handler`, `lambda-slack-bridge`, `ecs-spot-handler`) all hardcode `"km-"` directly in IAM role names, policy names, and function names inside the TF module. Each module needs a `var.resource_prefix` (default `"km"`) threaded through every `name` attribute. The DynamoDB modules already accept `var.table_name` so the live `terragrunt.hcl` files just need to switch from the literal `"km-..."` to `"${local.site_vars.locals.site.label}-..."`.

**Primary recommendation:** Extend `site.hcl`'s `site` block with `label = get_env("KM_RESOURCE_PREFIX", "km")` and `email_subdomain = get_env("KM_EMAIL_SUBDOMAIN", "sandboxes")`. All HCL consumers already read `local.site_vars.locals.site.label` — the existing `"tf-km"` state prefix already uses this field pattern. The Go side reads `resource_prefix` and `email_subdomain` from `km-config.yaml` via viper.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| REQ-PLATFORM-MULTI-INSTANCE | Multiple km installs coexist in a single AWS account via configurable resource prefix | Config struct extension + module parameterization findings below |
| REQ-CONFIG-EXTENSIBILITY | Extends the Phase 65 config model (same struct, same viper merge pattern) | Config struct section + Phase 65 pattern analysis |
</phase_requirements>

---

## Standard Stack

### Core
| Library / Tool | Version | Purpose | Why Standard |
|---------------|---------|---------|--------------|
| `github.com/spf13/viper` | already in use | km-config.yaml merging with env override | Same viper flow used for all Phase 65 fields |
| Go `internal/app/config/config.go` | N/A (in-repo) | Single source of truth for config fields | Established pattern; Phase 65 added `OrganizationAccountID`, `DNSParentAccountID` here |
| Terragrunt `site.hcl` | already in use | Injects site-level vars into all module calls | Already threads `label`, `tf_state_prefix`, `domain` — extend with two more keys |

### No New Dependencies
Phase 66 is purely a threading/migration phase. Zero new external dependencies.

---

## Architecture Patterns

### Pattern 1: Config Struct Extension (follows Phase 65)

Add two fields to `internal/app/config/config.go`:

```go
// ResourcePrefix is the prefix for all account-globally-unique resource names.
// Maps to km-config.yaml key resource_prefix. Defaults to "km".
ResourcePrefix string

// EmailSubdomain is the subdomain used for SES email addresses.
// Maps to km-config.yaml key email_subdomain. Defaults to "sandboxes".
EmailSubdomain string
```

In `Load()`, add defaults and viper merge keys following the exact Phase 65 pattern:

```go
v.SetDefault("resource_prefix", "km")
v.SetDefault("email_subdomain", "sandboxes")
```

Add to the km-config.yaml key merge list:
```go
"resource_prefix",
"email_subdomain",
```

In the cfg struct literal:
```go
ResourcePrefix: v.GetString("resource_prefix"),
EmailSubdomain: v.GetString("email_subdomain"),
```

### Pattern 2: Helper Methods on Config

Add three helpers. These collapse 30+ inline string concatenations to a single authoritative source:

```go
// GetEmailDomain returns the full email domain (e.g. "sandboxes.klankermaker.ai").
// Falls back to "sandboxes.klankermaker.ai" when both fields are empty.
func (c *Config) GetEmailDomain() string {
    sub := c.EmailSubdomain
    if sub == "" {
        sub = "sandboxes"
    }
    domain := c.Domain
    if domain == "" {
        domain = "klankermaker.ai"
    }
    return sub + "." + domain
}

// GetResourcePrefix returns the resource prefix (e.g. "km").
// Falls back to "km" when empty.
func (c *Config) GetResourcePrefix() string {
    if c.ResourcePrefix == "" {
        return "km"
    }
    return c.ResourcePrefix
}

// GetSsmPrefix returns the SSM parameter prefix (e.g. "/km/").
func (c *Config) GetSsmPrefix() string {
    return "/" + c.GetResourcePrefix() + "/"
}
```

### Pattern 3: DoctorConfigProvider Interface Extension

`DoctorConfigProvider` in `doctor.go` is the interface pattern used so doctor checks are testable. Add the same three getters to the interface and the `appConfigAdapter`:

```go
// In DoctorConfigProvider interface:
GetResourcePrefix() string
GetEmailDomain() string
GetSsmPrefix() string

// In appConfigAdapter:
func (a *appConfigAdapter) GetResourcePrefix() string { return a.cfg.GetResourcePrefix() }
func (a *appConfigAdapter) GetEmailDomain() string    { return a.cfg.GetEmailDomain() }
func (a *appConfigAdapter) GetSsmPrefix() string      { return a.cfg.GetSsmPrefix() }
```

### Pattern 4: site.hcl Threading

The existing `site.hcl` already has:

```hcl
site = {
    label           = "km"
    tf_state_prefix = "tf-km"
    domain          = get_env("KM_DOMAIN", "klankermaker.ai")
    ...
}
```

Extend the site block with two new keys read from env vars that `km init` exports (same pattern as `KM_DOMAIN`):

```hcl
site = {
    label            = get_env("KM_RESOURCE_PREFIX", "km")
    tf_state_prefix  = "tf-${get_env("KM_RESOURCE_PREFIX", "km")}"
    domain           = get_env("KM_DOMAIN", "klankermaker.ai")
    email_subdomain  = get_env("KM_EMAIL_SUBDOMAIN", "sandboxes")
    random_suffix    = get_env("KMGUID", "")
}
```

`local.site_vars.locals.backend.bucket` and `local.site_vars.locals.backend.dynamodb_table` are already derived from `local.site.tf_state_prefix`, so TF state backend renames flow automatically.

All live `terragrunt.hcl` files that currently hardcode `table_name = "km-budgets"` switch to:
```hcl
table_name = "${local.site_vars.locals.site.label}-budgets"
```

All live `terragrunt.hcl` files that currently build `email_domain = "sandboxes.${local.site_vars.locals.site.domain}"` switch to:
```hcl
email_domain = "${local.site_vars.locals.site.email_subdomain}.${local.site_vars.locals.site.domain}"
```

### Pattern 5: Lambda Module Parameterization

The five management Lambda modules currently hardcode `"km-"` everywhere inside the module. The fix is to add `var.resource_prefix` (default `"km"`) to each module's `variables.tf` and reference it throughout `main.tf`. This is the safe TF-rename approach: change only the `name` attribute value, not the TF logical resource name.

**Modules requiring this treatment:**
- `infra/modules/create-handler/v1.0.0/` — 20+ hardcoded `"km-"` strings
- `infra/modules/ttl-handler/v1.0.0/` — 15+ hardcoded `"km-"` strings
- `infra/modules/email-handler/v1.0.0/` — 12+ hardcoded `"km-"` strings, PLUS `SANDBOX_TABLE_NAME = "km-sandboxes"` env var (must become `var.sandbox_table_name`)
- `infra/modules/lambda-slack-bridge/v1.0.0/` — uses `locals { function_name = "km-slack-bridge" }` pattern already; change to `locals { function_name = "${var.resource_prefix}-slack-bridge" }`
- `infra/modules/ecs-spot-handler/v1.0.0/` — 6 hardcoded `"km-"` strings; singleton per-account concern documented in pitfalls

**Modules already safe (DynamoDB — already accept var.table_name):**
- `dynamodb-budget`, `dynamodb-identities` (both v1.0.0 and v1.1.0), `dynamodb-sandboxes`, `dynamodb-schedules`, `dynamodb-slack-nonces` — `name = var.table_name` confirmed. Only live `terragrunt.hcl` inputs need updating.

**Variable defaults in all module-level `variables.tf` files:**
```hcl
variable "resource_prefix" {
  description = "Resource name prefix for all km AWS resources (e.g. 'km', 'km2')"
  type        = string
  default     = "km"
}
```

### Pattern 6: Lambda Env-Var Hygiene (critical discovery)

**Current state (UNEVEN — must be unified):**

| Lambda | Table env var | Hardcoded fallback |
|--------|---------------|-------------------|
| `km-slack-bridge` | `KM_IDENTITIES_TABLE`, `KM_SANDBOXES_TABLE`, `KM_NONCE_TABLE` | Yes, `"km-*"` |
| `km-budget-enforcer` | `KM_BUDGET_TABLE` (via TF `KM_BUDGET_TABLE = var.budget_table_name`) | Yes, `"km-budgets"` |
| `ttl-handler` | `SANDBOX_TABLE_NAME`, `KM_BUDGET_TABLE` | Yes — `"km-sandboxes"`, `"km-budgets"` |
| `ttl-handler` | `km-ttl-handler`, `km-ttl-scheduler` (function names, not env) | Hardcoded strings — NOT env-driven |
| `create-handler` | `SANDBOX_TABLE_NAME` | Yes, `"km-sandboxes"` |
| `email-create-handler` | `SANDBOX_TABLE_NAME` | Yes, `"km-sandboxes"` |
| `configui` | `KM_BUDGET_TABLE` | Yes, `"km-budgets"` |

**The right pattern (used by `km-slack-bridge`):**
- TF module sets env var using `var.identities_table_name` (which comes from `dependency.identities.outputs.table_name`)
- Lambda code reads env var and falls back to default string
- Both must be updated: TF sets the prefix-aware name; Lambda code hardcoded default becomes a second-line defense only (still `"km-"` until env is set)

**Additional issue:** `ttl-handler` hardcodes Lambda function names directly in code (`"km-ttl-handler"`, `"km-ttl-scheduler"`) when creating/managing EventBridge schedules. These must become env-var-driven (`KM_TTL_HANDLER_NAME`, `KM_TTL_SCHEDULER_ROLE`).

### Anti-Patterns to Avoid

- **Rename TF logical resource identifiers** (e.g. `resource "aws_iam_role" "km_handler"` → `resource "aws_iam_role" "handler"`): this triggers destroy/create on stateful resources. Only change the `name` attribute.
- **Introduce a new module version** just to add `var.resource_prefix`: edit in-place within the existing `v1.0.0/` — no new version directory needed. These are internal modules with no external consumers.
- **Thread `resource_prefix` into per-sandbox names** that already include `{sandboxID}`: out of scope per roadmap. Names like `km-budget-enforcer-{sandboxID}` are already collision-free across installs.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Multi-value viper defaults | Custom config file parser | `v.SetDefault()` + viper merge | Already established — identical to Phase 65 |
| Helper method test | Manual string comparison | Existing `config_test.go` pattern with `writeKMConfig()` helper | Fits seamlessly |
| TF resource parameterization | New TF module version | Add `var.resource_prefix` to existing v1.0.0 in-place | No external module consumers |
| site.hcl env var reads | Shell scripts or init bootstrapping | `get_env()` with default, same as existing `KM_DOMAIN` | Already the site.hcl pattern |

---

## Common Pitfalls

### Pitfall 1: DynamoDB Table Rename = Data Loss
**What goes wrong:** If a TF logical resource name change causes `terraform plan` to show "destroy" + "create" for a DynamoDB table, applying that plan destroys all data.
**Why it happens:** TF tracks resources by logical name in state. Renaming the logical name (`resource "aws_dynamodb_table" "budget"` → `resource "aws_dynamodb_table" "my_budget"`) forces replacement.
**How to avoid:** ONLY change the `name` attribute (the physical AWS name), never the TF resource label. All five DynamoDB modules already use `name = var.table_name` — the TF logical name (`aws_dynamodb_table.budget`) stays constant. Verify every plan with `terraform plan -detailed-exitcode` before apply.
**Warning signs:** `terraform plan` shows `-/+ destroy and then create replacement` for any DynamoDB resource.

### Pitfall 2: SES Domain Identity DNS Round-Trip
**What goes wrong:** Changing `email_subdomain` on an existing install (e.g., `sandboxes` → `myco`) invalidates SES domain verification. The new domain needs new DKIM + MX + verification TXT records in Route53 and can take up to 72h for DNS propagation.
**Why it happens:** SES verifies the domain identity string. A different subdomain is a different identity.
**How to avoid:** Document clearly that `email_subdomain` is a one-time choice at `km init` time. Do NOT change it on a live install. `km doctor` should check that the configured email domain matches the verified SES domain identity.
**Warning signs:** SES shows "verification pending" after changing the subdomain.

### Pitfall 3: EventBridge Schedule Group Cannot be Renamed Safely
**What goes wrong:** If `resource_prefix` changes on a live install, the EventBridge schedule group `km-at` (created by ttl-handler module) cannot be renamed — all existing schedules under it carry the old group name. They'd need to be recreated.
**Why it happens:** EventBridge schedules are children of a schedule group; the group name is embedded in the schedule ARN.
**How to avoid:** Document that `resource_prefix` is a one-time choice at `km init`. Migrations are out of scope per roadmap. At initial install, the group name will be `{prefix}-at`.
**Warning signs:** `km at list` returns empty after prefix change while schedules still exist in the old group.

### Pitfall 4: ttl-handler Hardcodes Own Function Name in Lambda Code
**What goes wrong:** `cmd/ttl-handler/main.go:351` hardcodes `"km-ttl-handler"` when creating EventBridge schedules that target itself. With a custom prefix, the Lambda is deployed as `{prefix}-ttl-handler` but the code still constructs schedule targets pointing to `"km-ttl-handler"` — schedules fire into void.
**Why it happens:** The Lambda code does not read its own function name from context; it hardcodes it.
**How to avoid:** Add `KM_TTL_HANDLER_NAME` and `KM_TTL_SCHEDULER_ROLE` env vars to the ttl-handler module, set from `var.resource_prefix`. Lambda code reads them with a `"km-ttl-handler"` fallback.
**Warning signs:** Scheduled sandbox TTLs never fire; EventBridge shows schedule targets unresolvable.

### Pitfall 5: configui's `kmPrefix = "/km/"` Constant
**What goes wrong:** `cmd/configui/handlers_secrets.go:47` defines `const kmPrefix = "/km/"` and uses it as a path prefix for SSM parameter CRUD. With a custom prefix, SSM params land under `/{prefix}/` but configui only shows and manages `/km/` params.
**Why it happens:** The constant is set at compile time, not runtime.
**How to avoid:** Change `const kmPrefix = "/km/"` to a variable read from `KM_RESOURCE_PREFIX` env var with `"km"` as default, constructing `"/" + prefix + "/"`.
**Warning signs:** configui shows no secrets / Slack config missing in UI.

### Pitfall 6: ecs-spot-handler is an Account-Singleton Today
**What goes wrong:** `infra/modules/ecs-spot-handler/v1.0.0/main.tf` hardcodes `name = "km-ecs-spot-handler"` for the Lambda, IAM role, and EventBridge rule. If two km installs exist in the same account, the second `terragrunt apply` will attempt to create an IAM role with an identical name — IAM enforces uniqueness per account.
**Why it happens:** The module was designed as an account-singleton.
**How to avoid:** Add `var.resource_prefix` to the ecs-spot-handler module. Both installs use distinct prefixes and the names no longer collide.
**Warning signs:** `terragrunt apply` of ecs-spot-handler fails with `EntityAlreadyExists`.

### Pitfall 7: email-handler Module Hardcodes SANDBOX_TABLE_NAME
**What goes wrong:** `infra/modules/email-handler/v1.0.0/main.tf:252` has `SANDBOX_TABLE_NAME = "km-sandboxes"` hardcoded in the Lambda env block — it does NOT use a variable. The `email-create-handler` Lambda code reads `os.Getenv("SANDBOX_TABLE_NAME")` and falls back to `"km-sandboxes"`. With a custom prefix, the table is `{prefix}-sandboxes` but the Lambda writes to `km-sandboxes`.
**Why it happens:** The email-handler module was not updated alongside slack-bridge (which does use a variable).
**How to avoid:** Add `var.sandbox_table_name` (default `"km-sandboxes"`) to the email-handler module and wire it into the env block.
**Warning signs:** Remote sandbox create via email succeeds but sandbox record never appears in DynamoDB.

### Pitfall 8: TF State Backend Bucket is Read at Plan Time
**What goes wrong:** The TF state backend bucket (`tf-km-state-{region}`) must exist BEFORE any `terragrunt apply`. `km init` creates this bucket with a hardcoded `tf-km-state-{regionLabel}` format. If `resource_prefix` is set after the bucket already exists under the old name, there will be a mismatch.
**Why it happens:** `init.go:832` constructs the bucket name as `fmt.Sprintf("tf-km-state-%s", regionLabel)` — hardcoded `tf-km`.
**How to avoid:** Make `km init` read `cfg.GetResourcePrefix()` to construct the bucket name. The `site.hcl` `tf_state_prefix` is also derived from `label` already — making `label` configurable via env var automatically fixes the HCL side. The Go side (`fetchAndCacheOutputs`, `init.go`) needs explicit cfg plumbing.
**Warning signs:** `terragrunt init` fails with backend bucket not found after changing prefix.

---

## Code Examples

### Config helper methods (unit-testable pattern)
```go
// Source: internal/app/config/config.go — new methods
func (c *Config) GetResourcePrefix() string {
    if c.ResourcePrefix == "" {
        return "km"
    }
    return c.ResourcePrefix
}

func (c *Config) GetEmailDomain() string {
    sub := c.EmailSubdomain
    if sub == "" {
        sub = "sandboxes"
    }
    d := c.Domain
    if d == "" {
        d = "klankermaker.ai"
    }
    return sub + "." + d
}

func (c *Config) GetSsmPrefix() string {
    return "/" + c.GetResourcePrefix() + "/"
}
```

### Config test pattern (follows existing config_test.go style)
```go
func TestGetEmailDomain_Default(t *testing.T) {
    dir := t.TempDir()
    writeKMConfig(t, dir, "domain: example.com\n")
    orig, _ := os.Getwd(); defer os.Chdir(orig)
    os.Chdir(dir)
    cfg, _ := config.Load()
    if got := cfg.GetEmailDomain(); got != "sandboxes.example.com" {
        t.Errorf("got %q, want %q", got, "sandboxes.example.com")
    }
}

func TestGetEmailDomain_CustomSubdomain(t *testing.T) {
    dir := t.TempDir()
    writeKMConfig(t, dir, "domain: example.com\nemail_subdomain: mail\n")
    // ... cfg.GetEmailDomain() == "mail.example.com"
}

func TestGetResourcePrefix_Default(t *testing.T) {
    // with empty config, GetResourcePrefix() returns "km"
}

func TestGetResourcePrefix_Custom(t *testing.T) {
    writeKMConfig(t, dir, "resource_prefix: km2\n")
    // cfg.GetResourcePrefix() == "km2"
}
```

### site.hcl extension
```hcl
# Source: infra/live/site.hcl — add to site block
site = {
    label           = get_env("KM_RESOURCE_PREFIX", "km")
    tf_state_prefix = "tf-${get_env("KM_RESOURCE_PREFIX", "km")}"
    domain          = get_env("KM_DOMAIN", "klankermaker.ai")
    email_subdomain = get_env("KM_EMAIL_SUBDOMAIN", "sandboxes")
    random_suffix   = get_env("KMGUID", "")
}
# backend block stays derived from site.tf_state_prefix (unchanged logic)
```

### live terragrunt.hcl DynamoDB table names
```hcl
# Before (infra/live/use1/dynamodb-budget/terragrunt.hcl):
inputs = { table_name = "km-budgets" }

# After:
inputs = { table_name = "${local.site_vars.locals.site.label}-budgets" }
```

### Lambda module var.resource_prefix pattern
```hcl
# Source: infra/modules/create-handler/v1.0.0/variables.tf — add
variable "resource_prefix" {
  description = "Prefix for all resource names (default: km)"
  type        = string
  default     = "km"
}

# Source: infra/modules/create-handler/v1.0.0/main.tf — replace hardcoded "km-"
resource "aws_iam_role" "create_handler" {
  name = "${var.resource_prefix}-create-handler"
  ...
}
```

### live terragrunt.hcl passing prefix to Lambda modules
```hcl
# Source: infra/live/use1/create-handler/terragrunt.hcl — add to inputs
inputs = {
  ...
  resource_prefix = local.site_vars.locals.site.label
}
```

### email domain in live HCL (ttl-handler, create-handler, email-handler)
```hcl
# Before:
email_domain = "sandboxes.${local.site_vars.locals.site.domain}"

# After:
email_domain = "${local.site_vars.locals.site.email_subdomain}.${local.site_vars.locals.site.domain}"
```

### Go call-site migration (example for email.go)
```go
// Before (internal/app/cmd/email.go):
func emailDomain(cfg *config.Config) string {
    if cfg.Domain != "" {
        return "sandboxes." + cfg.Domain
    }
    return "sandboxes.klankermaker.ai"
}

// After:
func emailDomain(cfg *config.Config) string {
    return cfg.GetEmailDomain()
}
```

### SSM path migration (example for slack.go)
```go
// Before:
botToken, _ := ssmStore.Get(ctx, "/km/slack/bot-token", true)

// After:
botToken, _ := ssmStore.Get(ctx, cfg.GetSsmPrefix()+"slack/bot-token", true)
```

---

## Complete Call-Site Inventory

### Category A: email domain (`"sandboxes." + domain`) — 30 sites across 8 files
All collapse to `cfg.GetEmailDomain()` or equivalent:

| File | Lines | Pattern |
|------|-------|---------|
| `internal/app/cmd/create.go` | 400, 413, 1001, 1003, 1094, 1545, 1753 | `"sandboxes." + networkDomain` |
| `internal/app/cmd/destroy.go` | 397, 653 | `"sandboxes." + destroyBaseDomain` |
| `internal/app/cmd/budget.go` | 387 | `"sandboxes." + domain` |
| `internal/app/cmd/email.go` | 87–92 | `emailDomain()` helper (refactor helper to use `cfg.GetEmailDomain()`) |
| `internal/app/cmd/init.go` | 225, 336, 466, 493, 1507 | `"sandboxes." + cfg.Domain` |
| `internal/app/cmd/doctor.go` | 754, 757 | `fmt.Sprintf("sandboxes.%s", domain)` |
| `internal/app/cmd/configure.go` | 232, 234 | `"sandboxes.%s"` printf |
| `internal/app/cmd/info.go` | 88, 98 | `"@sandboxes." + cfg.Domain` |
| `cmd/budget-enforcer/main.go` | 642 | `"sandboxes.klankermaker.ai"` fallback |
| `cmd/create-handler/main.go` | 262–266, 444 | `"sandboxes.klankermaker.ai"` fallback + prefix check |
| `cmd/ttl-handler/main.go` | 1396 | `"sandboxes.klankermaker.ai"` fallback |
| `pkg/compiler/service_hcl.go` | 793 | `"sandboxes.klankermaker.ai"` default in compiler |
| `pkg/compiler/userdata.go` | 2375 | `"sandboxes.klankermaker.ai"` default in userdata generator |
| `pkg/compiler/budget_enforcer_hcl.go` | 85 | HCL template string — migrate to `email_subdomain` site var |

Note: `pkg/aws/ses.go` has NO hardcoded subdomain — it already takes `domain` as a parameter (the caller is responsible). `ses.go` does NOT need changes.

### Category B: SSM path prefix (`"/km/"`) — 75+ sites across 11 files
All collapse to `cfg.GetSsmPrefix()` prefix:

| File | Key paths |
|------|-----------|
| `internal/app/cmd/create.go` | `/km/slack/bot-token`, `/km/config/github/*` |
| `internal/app/cmd/create_slack.go` | `/km/slack/*` (4 paths) |
| `internal/app/cmd/slack.go` | `/km/slack/*` (8 paths) |
| `internal/app/cmd/configure_github.go` | `/km/config/github/*` (10 paths) |
| `internal/app/cmd/doctor_slack.go` | `/km/slack/*` (4 paths) |
| `internal/app/cmd/doctor.go` | `/km/config/github/*` (4 paths) |
| `internal/app/cmd/init.go` | `/km/config/remote-create/safe-phrase` |
| `internal/app/cmd/logs.go` | `/km/sandboxes/` log group |
| `internal/app/cmd/status.go` | `/km/sandboxes/` log group |
| `cmd/configui/handlers.go` | `/km/sandboxes` log group |
| `cmd/configui/handlers_secrets.go` | `const kmPrefix = "/km/"` — must become variable |
| `cmd/github-token-refresher/main.go` | `/km/config/github/*` (2 paths) |
| `cmd/km-slack-bridge/main.go` | `/km/slack/bot-token` (via env var `KM_BOT_TOKEN_PATH`) |
| `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` | `bot_token_path = "/km/slack/bot-token"` — becomes `"/${local.site_vars.locals.site.label}/slack/bot-token"` |
| `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` | default `"/km/slack/bot-token"` |
| `infra/modules/email-handler/v1.0.0/variables.tf` | default `"/km/config/remote-create/safe-phrase"` |
| `infra/modules/github-token/v1.0.0/main.tf` | `KM_GITHUB_SSM_CONFIG_PREFIX = "/km/config/github"` |

### Category C: Resource prefix (`"km-"` resource names) — 100+ sites

**Singletons (global per install — must become `{prefix}-`):**

| File | Lines | Names |
|------|-------|-------|
| `internal/app/cmd/create.go` | 649, 696, 851, 865, 892, 1162, 1873 | `km-sandboxes`, `km-ttl-handler`, `km-ttl-scheduler`, `km-budgets`, `km-identities` |
| `internal/app/cmd/destroy.go` | 99, 181, 285, 447, 461, 665, 677 | `km-sandboxes`, `km-identities`, `km-budget-*` |
| `internal/app/cmd/resume.go` | 83, 87, 166, 170 | `km-sandboxes`, `km-budgets`, `km-ttl-handler`, `km-ttl-scheduler` |
| `internal/app/cmd/shell.go` | 219, 451, 719, 843 | `km-sandboxes` |
| `internal/app/cmd/extend.go` | 110, 151, 161 | `km-sandboxes`, `km-ttl-handler`, `km-ttl-scheduler` |
| `internal/app/cmd/list.go` | 77 | `km-sandboxes` |
| `internal/app/cmd/unlock.go` | 72 | `km-sandboxes` |
| `internal/app/cmd/stop.go` | 77 | `km-sandboxes` |
| `internal/app/cmd/budget.go` | 52, 135, 142 | `km-sandbox-{id}-role`, `km-sandboxes`, `km-budgets` |
| `internal/app/cmd/ami.go` | 153, 837 | `km-sandboxes` |
| `internal/app/cmd/init.go` | 464, 1686, 1709 | `km-identities`, `km-create-handler`, `km-slack-bridge` |
| `internal/app/cmd/doctor.go` | 496, 1025, 1048, 1179, 1204, 1897, 1906, 1919, 1983, 2244, 2260 | various `km-*` checks |
| `internal/app/cmd/at.go` | 319, 702 | `km-at` group default |
| `cmd/ttl-handler/main.go` | 351, 355, 412, 637, 658, 711, 718, and 15+ more | `km-ttl-handler`, `km-ttl-scheduler`, `km-at`, `km-budgets`, `km-schedules`, etc. |
| `cmd/create-handler/main.go` | 271, 454, 459 | `km-identities`, `km-sandboxes` |
| `cmd/budget-enforcer/main.go` | 549, 586, 638, 647 | `km-budget-*`, `km-budgets`, `km-sandboxes` |
| `cmd/email-create-handler/main.go` | 967 | `km-sandboxes` |
| `cmd/km-slack-bridge/main.go` | 48, 49, 50 | `km-identities`, `km-sandboxes`, `km-slack-bridge-nonces` |
| `cmd/configui/main.go` | 107 | `km-budgets` |
| `pkg/aws/cloudwatch.go` | 68, 87 | `/km/sandboxes/` log group |
| `pkg/aws/scheduler.go` | 44 | `km-ttl-{sandboxID}` |
| `pkg/compiler/lifecycle.go` | 29 | `km-ttl-{sandboxID}` |
| `pkg/compiler/service_hcl.go` | 316, 322 | `/km/sandboxes/` log group |
| `internal/app/cmd/configure.go` | 251 | `km-budgets` default in wizard output |
| `infra/live/use1/dynamodb-*/terragrunt.hcl` | inputs | `table_name = "km-*"` (5 files) |
| `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` | mock_outputs | `"km-*"` table names in mock outputs |
| `infra/modules/{5 Lambda modules}` | main.tf | All IAM + Lambda names |

**Per-sandbox names (ALREADY collision-free via sandboxID — DO NOT change):**
`km-budget-enforcer-{sandboxID}`, `km-github-token-refresher-{sandboxID}`, `km-ec2spot-ssm-{sandboxID}-{region}`, `km-ec2spot-profile-{sandboxID}-{region}`, `km-docker-{sandboxID}-{region}`, `km-sidecar-{sandboxID}-{region}`, `km-ttl-{sandboxID}`, `km-budget-{sandboxID}`, `km-github-token-{sandboxID}`

These appear in `destroy.go`, `create.go`, `ttl-handler/main.go`, `budget-enforcer module`, `budget-enforcer/main.go` but are explicitly out of scope per roadmap.

### Category D: TF state prefix (`"tf-km"`) — 14 sites

| File | Line | Code |
|------|------|------|
| `infra/live/site.hcl` | 5 | `tf_state_prefix = "tf-km"` — becomes `"tf-${get_env(...)}"` |
| `internal/app/cmd/init.go` | 832, 833 | `fmt.Sprintf("tf-km-state-%s", regionLabel)` — use `cfg.GetResourcePrefix()` |
| `internal/app/cmd/resume.go` | 138 | `"tf-km/sandboxes/"` — use cfg prefix |
| `internal/app/cmd/unlock.go` | 131 | `"tf-km/sandboxes/"` — use cfg prefix |
| `internal/app/cmd/extend.go` | 183 | `"tf-km/sandboxes/"` — use cfg prefix |
| `internal/app/cmd/lock.go` | 120 | `"tf-km/sandboxes/"` — use cfg prefix |
| `cmd/ttl-handler/main.go` | 954, 962, 1061 | `statePrefix = "tf-km"` fallback |
| `cmd/configui/main.go` | 66 | `KM_BUCKET` default `"tf-km"` |

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact for Phase 66 |
|--------------|------------------|--------------|---------------------|
| `accounts.management` single field | `accounts.organization` + `accounts.dns_parent` | Phase 65 | Same struct extension pattern applies here |
| DynamoDB modules hardcoding `name = "km-*"` | `name = var.table_name` with default | Already done (before Phase 66) | DynamoDB live configs just need literal → site.label substitution |
| `lambda-slack-bridge` module using locals | `locals { function_name = "km-slack-bridge" }` | Phase 63 | Good pattern — extend to `"${var.resource_prefix}-slack-bridge"` |
| TTL/create/email Lambda modules | Fully hardcoded names | Still current | Require var.resource_prefix addition in Phase 66 |

**Deprecated/outdated patterns to remove:**
- All inline `"sandboxes." + cfg.Domain` → `cfg.GetEmailDomain()`
- `const kmPrefix = "/km/"` in configui → runtime variable
- `fmt.Sprintf("tf-km-state-%s", regionLabel)` in init.go → use `cfg.GetResourcePrefix()`

---

## Recommended Plan Slicing

Based on the dependency graph and atomic commit constraints:

### Wave 1 (independent):
- **66-01**: Config struct (`ResourcePrefix`, `EmailSubdomain`), helper methods (`GetEmailDomain`, `GetResourcePrefix`, `GetSsmPrefix`), unit tests. Touches: `internal/app/config/config.go`, `config_test.go`. Zero AWS changes.
- **66-02**: Migrate all Go email-domain call sites (Category A, ~30 sites). Depends on 66-01. Touches: `internal/app/cmd/*.go`, `cmd/*/main.go`, `pkg/compiler/*.go`.

### Wave 2 (depends on 66-01):
- **66-03**: Migrate all Go SSM path + resource name call sites (Categories B+C, 100+ sites in internal/cmd/pkg). Extends `DoctorConfigProvider` interface. Touches: essentially all `internal/app/cmd/*.go`, `cmd/*/main.go`, `pkg/aws/*.go`. This is the largest change.

### Wave 3 (depends on 66-01 for env var names):
- **66-04**: Thread `resource_prefix` into the five Lambda TF modules (add `var.resource_prefix` to create-handler, ttl-handler, email-handler, lambda-slack-bridge, ecs-spot-handler). Update live `terragrunt.hcl` DynamoDB inputs. Update `site.hcl` to add `label` and `email_subdomain` env vars. Update the five DynamoDB live configs.

### Wave 4 (depends on all prior):
- **66-05**: TF state backend (`tf-km-state-{region}` → `tf-{prefix}-state-{region}`), `km init` state bucket naming, `km configure` wizard additions (prompt for prefix + subdomain), `km doctor` prefix-collision warning, grep-audit verification pass (zero `"sandboxes."` literals, zero `"/km/"` literals outside `.planning/`, zero `"km-"` literals for singleton resources outside `.planning/`).

**Wave layout:**
```
Wave 1: [66-01]
Wave 2: [66-02, 66-03 in parallel (independent call-site domains)]
Wave 3: [66-04]
Wave 4: [66-05]
```

66-02 and 66-03 can run in parallel because they touch different call-site categories (email domain vs. resource names) with no shared file conflicts.

---

## Validation Architecture

nyquist_validation is enabled per `.planning/config.json`.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) + `go test` |
| Config file | `internal/app/config/config_test.go` (existing), new test files per package |
| Quick run command | `go test ./internal/app/config/... -run TestGetEmailDomain\|TestGetResourcePrefix\|TestGetSsmPrefix -v` |
| Full suite command | `go test ./internal/... ./pkg/... ./cmd/...` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| REQ-PLATFORM-MULTI-INSTANCE | `GetResourcePrefix()` defaults to "km" | unit | `go test ./internal/app/config/... -run TestGetResourcePrefix_Default` | ❌ Wave 0 |
| REQ-PLATFORM-MULTI-INSTANCE | `GetResourcePrefix()` returns custom value from km-config.yaml | unit | `go test ./internal/app/config/... -run TestGetResourcePrefix_Custom` | ❌ Wave 0 |
| REQ-PLATFORM-MULTI-INSTANCE | `GetEmailDomain()` defaults to "sandboxes.{domain}" | unit | `go test ./internal/app/config/... -run TestGetEmailDomain_Default` | ❌ Wave 0 |
| REQ-PLATFORM-MULTI-INSTANCE | `GetEmailDomain()` returns custom subdomain | unit | `go test ./internal/app/config/... -run TestGetEmailDomain_Custom` | ❌ Wave 0 |
| REQ-PLATFORM-MULTI-INSTANCE | `GetSsmPrefix()` returns "/{prefix}/" | unit | `go test ./internal/app/config/... -run TestGetSsmPrefix` | ❌ Wave 0 |
| REQ-CONFIG-EXTENSIBILITY | km-config.yaml with no resource_prefix loads without error | unit | `go test ./internal/app/config/... -run TestLoadBackwardCompat` | ✅ (extend existing) |
| REQ-CONFIG-EXTENSIBILITY | km-config.yaml resource_prefix overrides default | unit | `go test ./internal/app/config/... -run TestLoadResourcePrefix` | ❌ Wave 0 |
| REQ-PLATFORM-MULTI-INSTANCE | Zero `"sandboxes."` literal string concats remain | grep audit | `! grep -rn '"sandboxes\.' ./internal ./pkg ./cmd --include='*.go'` | ❌ Wave 0 (gate in 66-05) |
| REQ-PLATFORM-MULTI-INSTANCE | Zero `"/km/"` hardcoded SSM paths remain (outside _test.go) | grep audit | `! grep -rn '"/km/' ./internal ./pkg ./cmd --include='*.go'` | ❌ Wave 0 (gate in 66-05) |
| REQ-PLATFORM-MULTI-INSTANCE | TF plan on DynamoDB modules shows no destroy/create with prefix change | manual | `terragrunt plan` (manual smoke) | manual-only |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/config/... -count=1`
- **Per wave merge:** `go test ./internal/... ./pkg/... -count=1`
- **Phase gate:** Full suite green (`go test ./...`) before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `internal/app/config/config_test.go` — extend with `TestGetResourcePrefix_Default`, `TestGetResourcePrefix_Custom`, `TestGetEmailDomain_Default`, `TestGetEmailDomain_Custom`, `TestGetSsmPrefix`, `TestLoadResourcePrefix`, `TestLoadEmailSubdomain`
- [ ] No new framework install needed — stdlib `go test` already in use

---

## Open Questions

1. **Should `resource_prefix` be prompted in `km configure` wizard?**
   - What we know: `km configure` currently asks for domain, accounts, SSO, region, email, safe_phrase
   - What's unclear: User experience — do operators set this upfront or only when needed?
   - Recommendation: Add to wizard with default value `"km"`, prompt: "Resource prefix (default: km — change only for second install in same account)"

2. **Should `km init` error if another prefix's resources already exist?**
   - What we know: A "prefix collision" detection would require listing all Lambda/DynamoDB names and checking for the requested prefix
   - What's unclear: Whether `km doctor` is the right gate vs. `km init` pre-flight
   - Recommendation: Add a `km doctor` check `checkPrefixCollision` that warns if `{prefix}-ttl-handler` Lambda already exists (indicates a running install), rather than blocking `km init`

3. **`km-sandbox-containment` SCP name — does it need to be prefixed?**
   - What we know: `infra/modules/scp/v1.0.0/main.tf:216` hardcodes `name = "km-sandbox-containment"`. The SCP is org-scoped, not account-scoped. Only one install can deploy the SCP per org.
   - What's unclear: Is there a scenario where two installs in the same org each want their own SCP?
   - Recommendation: Leave SCP name as `"km-sandbox-containment"` (hardcoded, not prefixed) per the roadmap constraint. Document the caveat. This is the planned approach — only one SCP per org account.

4. **`pkg/aws/ec2_ami.go:54` uses prefix `"km-"` to filter operator-baked AMIs in `km ami list`**
   - What we know: `ec2_ami.go:54: prefix := "km-"` filters AMI names when listing
   - What's unclear: With a custom prefix, `km ami list` would show no AMIs
   - Recommendation: Thread `cfg.GetResourcePrefix()` into the AMI listing call and use `prefix + "-"` as the filter

---

## Sources

### Primary (HIGH confidence)
- Direct code audit of `/Users/khundeck/working/klankrmkr/` codebase (2026-05-01)
- `internal/app/config/config.go` — Config struct definition and Load() function
- `internal/app/config/config_test.go` — Existing test patterns
- `internal/app/cmd/doctor.go` — DoctorConfigProvider interface + appConfigAdapter
- `infra/live/site.hcl` — site block structure and existing env var threading pattern
- `infra/modules/dynamodb-*/v1.0.0/main.tf` — All confirmed to use `name = var.table_name`
- `.planning/phases/65-*/65-04-PLAN.md` — Phase 65 rename pattern study

### Secondary (MEDIUM confidence)
- `infra/modules/create-handler/`, `ttl-handler/`, `email-handler/`, `lambda-slack-bridge/`, `ecs-spot-handler/` module `main.tf` files — confirmed hardcoded resource names
- `km-config.yaml` (live operator config) — current field set

### Tertiary (LOW confidence — manual verification needed)
- TF plan behavior for module `name` attribute changes: LOW — not tested. Must run `terragrunt plan -detailed-exitcode` to confirm no unexpected replaces before applying.

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — well-established viper + config pattern, Phase 65 precedent
- Architecture: HIGH — site.hcl threading and module parameterization patterns fully verified
- Pitfalls: HIGH — all identified from direct code audit; DynamoDB replace risk is architectural fact
- Call-site inventory: HIGH — generated from live grep of codebase

**Research date:** 2026-05-01
**Valid until:** 2026-06-01 (stable codebase, no external dependencies changing)
