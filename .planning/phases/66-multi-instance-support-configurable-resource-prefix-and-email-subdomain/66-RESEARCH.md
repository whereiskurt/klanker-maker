# Phase 66: Multi-instance Support (resource_prefix + email_subdomain) — Research

**Researched:** 2026-05-04 (REPLAN — prior research 2026-05-01 archived in git at commit 0d74e7f)
**Domain:** Go config struct extension, Terraform module parameterization, Terragrunt site.hcl threading, Lambda env-var hygiene
**Confidence:** HIGH

---

## Summary

Phase 66 introduces two knobs — `resource_prefix` (default `"km"`) and `email_subdomain` (default `"sandboxes"`) — that together make every account-globally-unique AWS resource name and every email address derivable from config rather than hardcoded. The change is purely additive: with defaults in place, the existing install produces identical resource names.

**Since the prior research (2026-05-01)**, Phases 67, 67.1, and 68 landed and added significant new surface area: two new DynamoDB tables (`km-slack-threads`, `km-slack-stream-messages`), one new SSM SecureString (`/km/slack/signing-secret`), per-sandbox SQS FIFO queues (`{prefix}-slack-inbound-{sandbox-id}.fifo`), and an expanded km-slack-bridge Lambda with four new environment variables. The good news: Phase 67 shipped a forward-compat `GetResourcePrefix()` helper and `ResourcePrefix` field in config.go **as a shim**, so the config-layer foundation for Phase 66 is partially built. The bad news: six specific new call sites are hardcoded and not yet routed through the helper.

**Primary recommendation:** Extend `site.hcl`'s `site` block with `label = get_env("KM_RESOURCE_PREFIX", "km")` and `email_subdomain = get_env("KM_EMAIL_SUBDOMAIN", "sandboxes")`. The Go side already has `GetResourcePrefix()` — complete it with `GetEmailDomain()`, `GetSsmPrefix()`, and `EmailSubdomain` field. Migrate all hardcoded call sites to the helpers.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| REQ-PLATFORM-MULTI-INSTANCE | Multiple km installs coexist in a single AWS account via configurable resource prefix | Config struct extension + module parameterization findings below |
| REQ-CONFIG-EXTENSIBILITY | Extends the Phase 65 config model (same struct, same viper merge pattern) | Config struct section + Phase 65 pattern analysis |
</phase_requirements>

---

## What Phase 67/68 Already Did For Phase 66 (Forward-compat Shims)

Before the drift audit below, note what arrived as partially done:

| Component | What's Already There | What's Still Missing |
|-----------|---------------------|---------------------|
| `config.go` `ResourcePrefix` field | Added (Phase 67 shim) | `EmailSubdomain` field |
| `config.go` `GetResourcePrefix()` | Implemented, fallback `"km"` (Phase 67) | — |
| `config.go` `GetSlackThreadsTableName()` | Implemented, uses `GetResourcePrefix()` (Phase 67) | — |
| `config.go` `GetSlackStreamMessagesTableName()` | Implemented, uses `GetResourcePrefix()` (Phase 68) | — |
| `pkg/aws/sqs.go` `SlackInboundQueueName()` | Uses `resourcePrefix` param (Phase 67) | — |
| `create_slack_inbound.go` | Calls `cfg.GetResourcePrefix()` (Phase 67) | — |
| `doctor.go` `SlackResourcePrefix` | Populated via type-assert on `appConfigAdapter` (Phase 67) | Should move to interface proper |
| `list.go:132` `cfg.GetSlackThreadsTableName()` | Correct (Phase 67) | — |
| `config.go` viper default `resource_prefix = "km"` | Added (Phase 67) | `email_subdomain` default |
| `GetEmailDomain()` helper | **NOT yet in config.go** | Needed for Phase 66 |
| `GetSsmPrefix()` helper | **NOT yet in config.go** | Needed for Phase 66 |
| `DoctorConfigProvider` interface `GetResourcePrefix()` | **NOT in interface** (type-assert hack at line 2344) | Add to interface + adapter |

---

## Standard Stack

### Core
| Library / Tool | Version | Purpose | Why Standard |
|---------------|---------|---------|--------------|
| `github.com/spf13/viper` | already in use | km-config.yaml merging with env override | Same viper flow used for all Phase 65/67 fields |
| Go `internal/app/config/config.go` | N/A (in-repo) | Single source of truth for config fields | Phase 67 already added `ResourcePrefix`, `GetResourcePrefix()`, `GetSlackThreadsTableName()`, `GetSlackStreamMessagesTableName()` here |
| Terragrunt `infra/live/site.hcl` | already in use | Injects site-level vars into all module calls | Already threads `label`, `tf_state_prefix`, `domain` — extend with `email_subdomain` and make `label` env-driven |

### No New Dependencies
Phase 66 is purely a threading/migration phase. Zero new external dependencies.

---

## Architecture Patterns

### Pattern 1: Config Struct Extension (partially done by Phase 67)

Phase 67 added `ResourcePrefix string` field and `GetResourcePrefix()` to `internal/app/config/config.go`. Phase 66 adds the remaining two:

```go
// EmailSubdomain is the subdomain used for SES email addresses.
// Maps to km-config.yaml key email_subdomain. Defaults to "sandboxes".
EmailSubdomain string
```

In `Load()`, add defaults and viper merge keys following the existing Phase 67 pattern:

```go
v.SetDefault("email_subdomain", "sandboxes")
// resource_prefix default already added by Phase 67
```

Add to the km-config.yaml key merge list:
```go
"email_subdomain",
// "resource_prefix" already in list from Phase 67
```

In the cfg struct literal:
```go
EmailSubdomain: v.GetString("email_subdomain"),
// ResourcePrefix already added by Phase 67
```

### Pattern 2: Helper Methods on Config (two still missing)

`GetResourcePrefix()` and `GetSlackThreadsTableName()` / `GetSlackStreamMessagesTableName()` already exist. Add the remaining two:

```go
// GetEmailDomain returns the full email domain (e.g. "sandboxes.klankermaker.ai").
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

// GetSsmPrefix returns the SSM parameter prefix (e.g. "/km/").
func (c *Config) GetSsmPrefix() string {
    return "/" + c.GetResourcePrefix() + "/"
}
```

### Pattern 3: DoctorConfigProvider Interface Extension

The `DoctorConfigProvider` interface in `doctor.go` currently accesses `GetResourcePrefix()` via a **type-assert hack** (line 2344: `if appCfgTyped, ok := cfg.(*appConfigAdapter); ok`). Phase 68 added `GetSlackStreamMessagesTableName()` to the interface but NOT `GetResourcePrefix()` or `GetSlackThreadsTableName()`. Phase 66 MUST:

1. Add `GetResourcePrefix()`, `GetEmailDomain()`, `GetSsmPrefix()`, and `GetSlackThreadsTableName()` to the `DoctorConfigProvider` interface.
2. Add corresponding adapter methods to `appConfigAdapter`.
3. Remove the type-assert hack at doctor.go:2344.

```go
// In DoctorConfigProvider interface (additions to existing 12 methods):
GetResourcePrefix() string
GetEmailDomain() string
GetSsmPrefix() string
GetSlackThreadsTableName() string
// GetSlackStreamMessagesTableName already in interface from Phase 68

// In appConfigAdapter (additions):
func (a *appConfigAdapter) GetResourcePrefix() string      { return a.cfg.GetResourcePrefix() }
func (a *appConfigAdapter) GetEmailDomain() string         { return a.cfg.GetEmailDomain() }
func (a *appConfigAdapter) GetSsmPrefix() string           { return a.cfg.GetSsmPrefix() }
func (a *appConfigAdapter) GetSlackThreadsTableName() string { return a.cfg.GetSlackThreadsTableName() }
```

### Pattern 4: site.hcl Threading

The existing `site.hcl` (`infra/live/site.hcl`) currently has:

```hcl
site = {
    label           = "km"           # HARDCODED — needs to become env-driven
    tf_state_prefix = "tf-km"        # HARDCODED — derived from label
    domain          = get_env("KM_DOMAIN", "klankermaker.ai")
    random_suffix   = get_env("KMGUID", "")
    # email_subdomain is ABSENT — needs to be added
}
```

Extend the site block:

```hcl
site = {
    label           = get_env("KM_RESOURCE_PREFIX", "km")
    tf_state_prefix = "tf-${get_env("KM_RESOURCE_PREFIX", "km")}"
    domain          = get_env("KM_DOMAIN", "klankermaker.ai")
    email_subdomain = get_env("KM_EMAIL_SUBDOMAIN", "sandboxes")
    random_suffix   = get_env("KMGUID", "")
}
```

`local.site_vars.locals.backend.bucket` and `local.site_vars.locals.backend.dynamodb_table` are already derived from `local.site.tf_state_prefix`, so TF state backend renames flow automatically.

All live `terragrunt.hcl` DynamoDB files that currently hardcode `table_name = "km-budgets"` switch to:
```hcl
table_name = "${local.site_vars.locals.site.label}-budgets"
```

### Pattern 5: Lambda Module Parameterization

The five management Lambda modules hardcode `"km-"` inside the module. The lambda-slack-bridge module gained `var.resource_prefix` in Phase 67 for SQS policy ARNs but its `function_name` local is STILL `"km-slack-bridge"` (hardcoded). All five modules need Phase 66 treatment:

**Modules requiring `var.resource_prefix` added or completed:**
- `infra/modules/create-handler/v1.0.0/` — 20+ hardcoded `"km-"` strings
- `infra/modules/ttl-handler/v1.0.0/` — 15+ hardcoded `"km-"` strings
- `infra/modules/email-handler/v1.0.0/` — 12+ hardcoded `"km-"` strings, PLUS `SANDBOX_TABLE_NAME = "km-sandboxes"` env var
- `infra/modules/lambda-slack-bridge/v1.0.0/` — `locals { function_name = "km-slack-bridge" }` at line 5 — must become `"${var.resource_prefix}-slack-bridge"`; `var.resource_prefix` already exists but is not used for function_name
- `infra/modules/ecs-spot-handler/v1.0.0/` — 6 hardcoded `"km-"` strings

**Modules already safe (DynamoDB — already accept `var.table_name`):**
- `dynamodb-budget`, `dynamodb-identities` (v1.0.0 and v1.1.0), `dynamodb-sandboxes`, `dynamodb-schedules`, `dynamodb-slack-nonces` — `name = var.table_name` confirmed.
- `dynamodb-slack-threads/v1.0.0/` — `name = var.table_name` confirmed (Phase 67).
- `dynamodb-slack-stream-messages/v1.0.0/` — `name = var.table_name` confirmed (Phase 68).

### Pattern 6: Lambda Env-Var Hygiene (unchanged from stale research, still applies)

The right pattern is: TF module sets env var from `var.*_table_name`, Lambda code reads env var with `"km-"` fallback. Both must be updated. The lambda-slack-bridge now has `KM_SLACK_THREADS_TABLE` and `KM_SIGNING_SECRET_PATH` as env vars in the module, but the live `terragrunt.hcl` does NOT pass `slack_threads_table_name` or `signing_secret_path` — they rely on module defaults (which happen to be correct for the default prefix). Phase 66 must wire these through `site.label`.

### Pattern 7: create.go Env Var Propagation to Compiler

The `pkg/compiler/userdata.go` and `pkg/compiler/service_hcl.go` do not receive `*config.Config` directly — they read resource names from process environment variables set by `create.go`'s `os.Setenv()` block. Phase 67/68 added `KM_SLACK_THREADS_TABLE` and `KM_SLACK_STREAM_TABLE` as env vars that the compiler reads, but `create.go` does NOT set these in its `os.Setenv` block. Result: the compiler falls back to hardcoded `"km-slack-threads"` / `"km-slack-stream-messages"` even when `resource_prefix` is set.

**Fix required in create.go:** Add two lines to the `os.Setenv` block (around line 360):
```go
os.Setenv("KM_SLACK_THREADS_TABLE", cfg.GetSlackThreadsTableName())
os.Setenv("KM_SLACK_STREAM_TABLE", cfg.GetSlackStreamMessagesTableName())
```

### Anti-Patterns to Avoid

- **Rename TF logical resource identifiers** (e.g. `resource "aws_iam_role" "km_handler"` → `resource "aws_iam_role" "handler"`): this triggers destroy/create on stateful resources. Only change the `name` attribute.
- **Introduce a new module version** just to add `var.resource_prefix`: edit in-place within the existing `v1.0.0/` — no new version directory needed.
- **Thread `resource_prefix` into per-sandbox names** that already include `{sandboxID}`: out of scope per roadmap.

---

## Phase 67/68 Drift Audit

This section documents the delta between the stale 2026-05-01 research and today's codebase after Phases 67, 67.1, and 68 landed. For each new resource, exact file:line references are given.

### New DynamoDB Tables

| Table | Module | module `name` attribute | Live Config File | Live `table_name` | Status |
|-------|--------|------------------------|-----------------|-------------------|--------|
| `km-slack-threads` (Phase 67) | `infra/modules/dynamodb-slack-threads/v1.0.0/main.tf` | `var.table_name` (safe) | `infra/live/use1/dynamodb-slack-threads/terragrunt.hcl:36` | `"km-slack-threads"` HARDCODED | Needs `site.label` substitution |
| `km-slack-stream-messages` (Phase 68) | `infra/modules/dynamodb-slack-stream-messages/v1.0.0/main.tf` | `var.table_name` (safe) | `infra/live/use1/dynamodb-slack-stream-messages/terragrunt.hcl:36` | `"km-slack-stream-messages"` HARDCODED | Needs `site.label` substitution |

Both modules use `name = var.table_name` — the module itself is already safe. Only the live Terragrunt config needs the literal replaced with `"${local.site_vars.locals.site.label}-slack-threads"` and `"${local.site_vars.locals.site.label}-slack-stream-messages"`.

### New SSM Parameters

| Parameter | Where Written (Go) | Current State | Fix |
|-----------|-------------------|---------------|-----|
| `/km/slack/signing-secret` (Phase 67, SecureString) | `internal/app/cmd/slack.go:759` (`PersistSigningSecret`) | HARDCODED path | `store.Put(ctx, cfg.GetSsmPrefix()+"slack/signing-secret", ...)` |
| `/km/slack/signing-secret` | `internal/app/cmd/slack.go:836` (log message) | String literal in log | Update message only |
| `/km/slack/signing-secret` (Lambda default) | `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf:76` | `default = "/km/slack/signing-secret"` | Live config must pass `signing_secret_path = "/${local.site_vars.locals.site.label}/slack/signing-secret"` |
| `/km/slack/bot-scopes-cache` (Phase 67) | `internal/app/cmd/slack.go:311,328` | HARDCODED `/km/` prefix | Migrate to `cfg.GetSsmPrefix()+"slack/bot-scopes-cache"` |
| `/km/slack/last-test-timestamp` (Phase 67) | `internal/app/cmd/slack.go:426,465` | HARDCODED `/km/` prefix | Migrate to `cfg.GetSsmPrefix()+"slack/last-test-timestamp"` |
| `/km/slack/workspace` (Phase 67) | `internal/app/cmd/slack.go:461` | HARDCODED `/km/` prefix | Migrate to `cfg.GetSsmPrefix()+"slack/workspace"` |
| `/sandbox/{id}/slack-inbound-queue-url` (Phase 67, per-sandbox) | `internal/app/cmd/create_slack_inbound.go:129` | `/sandbox/{id}/...` — **not prefixed** by design | Out of scope per roadmap (per-sandbox SSM path) |

### New SQS Resources (Per-Sandbox)

`{prefix}-slack-inbound-{sandbox-id}.fifo` queues are provisioned at `km create` and deleted at `km destroy`.

**Already prefix-aware (Phase 67 did this correctly):**
- `pkg/aws/sqs.go:44-45`: `SlackInboundQueueName(resourcePrefix, sandboxID)` takes `resourcePrefix` as a parameter.
- `internal/app/cmd/create_slack_inbound.go:100`: calls `deps.Cfg.GetResourcePrefix()`.
- `internal/app/cmd/doctor_slack.go:292`: `checkSlackInboundStaleQueues` takes `resourcePrefix` parameter; populated from `deps.SlackResourcePrefix`.
- `internal/app/cmd/doctor.go:2345-2347`: `deps.SlackResourcePrefix` populated via type-assert on `appConfigAdapter`.

**Still needs fix (Phase 66 normalizes):** The type-assert hack at `doctor.go:2344-2347` should be replaced once `GetResourcePrefix()` is properly added to the `DoctorConfigProvider` interface.

**Lambda-slack-bridge SQS IAM policy:** `infra/modules/lambda-slack-bridge/v1.0.0/main.tf:148` uses `var.resource_prefix` in the SQS wildcard ARN — correct. But the live config (`infra/live/use1/lambda-slack-bridge/terragrunt.hcl`) does **not** pass `resource_prefix`, so the bridge's SQS policy uses the default `"km"` even with a custom prefix. Fix: add `resource_prefix = local.site_vars.locals.site.label` to the live config inputs.

### New Lambda Environment Variables in km-slack-bridge

The bridge Lambda now has these Phase 67/68 env vars (from `infra/modules/lambda-slack-bridge/v1.0.0/main.tf:282-286`):

| Env Var | Set From | Default in Module | Live Config Passes It? | Phase 66 Action |
|---------|---------|------------------|------------------------|-----------------|
| `KM_SIGNING_SECRET_PATH` | `var.signing_secret_path` | `"/km/slack/signing-secret"` | No — uses default | Live config must pass prefix-aware path |
| `KM_SLACK_THREADS_TABLE` | `var.slack_threads_table_name` | `"km-slack-threads"` | No — uses default | Live config must pass `dependency.slack_threads.outputs.table_name` |
| `KM_SLACK_ACK_EMOJI` | `var.slack_ack_emoji` | `"eyes"` | No — uses default | No prefix concern; default is fine |
| `KM_RESOURCE_PREFIX` | `var.resource_prefix` | `"km"` | No — uses default | Live config must pass `local.site_vars.locals.site.label` |

**The live config is missing a `dependency "slack_threads"` block.** Without it, the bridge can't resolve `dependency.slack_threads.outputs.table_name`. The live config must add:
```hcl
dependency "slack_threads" {
  config_path = "../dynamodb-slack-threads"
  mock_outputs = { table_name = "km-slack-threads" }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply"]
}
```

### New DynamoDB GSI

`slack_channel_id-index` on the `km-sandboxes` table (Phase 67, in `infra/modules/dynamodb-sandboxes/v1.1.0/main.tf:55`). The GSI is within the table module which already uses `var.table_name` — no new prefix-awareness needed. The GSI ARN pattern `${var.sandboxes_table_name}/index/slack_channel_id-index` in the bridge module's IAM policy at `main.tf:180` already uses the variable — safe.

### New Go Call Sites Added by Phase 67/68

**Category B additions (new /km/ SSM paths in Go, Phase 67 delta):**

| File | New Lines | Old Count (stale) | New Count (today) |
|------|-----------|-------------------|-------------------|
| `internal/app/cmd/slack.go` | +14 new paths (signing-secret, bot-scopes-cache, last-test-timestamp, workspace, etc.) | 8 paths | 22 paths |
| `internal/app/cmd/doctor_slack.go` | +0 new /km/ paths | 4 paths | 4 paths |
| `internal/app/cmd/create_slack.go` | +0 new /km/ paths | 4 paths | 4 paths |
| **Total `/km/` in Go** | **+11 net new** | **75** | **86** |

New `"/km/"` paths not in stale research, all requiring `cfg.GetSsmPrefix()` migration:
- `internal/app/cmd/slack.go:311,328` — `/km/slack/bot-scopes-cache`
- `internal/app/cmd/slack.go:426,465` — `/km/slack/last-test-timestamp`
- `internal/app/cmd/slack.go:461` — `/km/slack/workspace`
- `internal/app/cmd/slack.go:759` — `/km/slack/signing-secret` (Phase 67 critical)
- `internal/app/cmd/slack.go:836` — log message mentioning `/km/slack/signing-secret` (low-impact, update for consistency)
- `internal/app/cmd/destroy_slack.go:75` — `/km/slack/bridge-url` (was already in stale research, verified unchanged)
- `pkg/compiler/userdata.go:184,1211` — bash script templates with `/km/slack/bridge-url`, `/km/slack/bridge-url` (sandbox-side scripts, **same concern as before**, still needs HCL template migration)

**Category C additions (new `"km-"` singleton names in Go, Phase 67/68 delta):**

The stale research counted 100+ singleton `"km-"` sites. Today the count is ~134 across all singleton resource names. New sites from Phase 67/68 that need migration:

| File | Line | Hardcoded Name | Fix |
|------|------|----------------|-----|
| `internal/app/cmd/status.go` | 468 | `"km-slack-threads"` | `cfg.GetSlackThreadsTableName()` — `cfg` must be threaded into the `km status` display path |
| `internal/app/cmd/doctor.go` | 2330 | `"km-sandboxes"` in `doctorSlackMetadataScanner.tableName` | Use `cfg.GetSandboxTableName()` (already in cfg struct) |
| `internal/app/cmd/doctor.go` | 2341 | `"km-sandboxes"` in `listSandboxesWithInboundImpl` call | Same fix |
| `internal/app/cmd/doctor.go` | 2392 | `"km-sandboxes"` in `newRealLister` call | Same fix |
| `internal/app/cmd/init.go` | 1724 | `"km-slack-bridge"` in `ForceSlackBridgeColdStartWith` | `cfg.GetResourcePrefix() + "-slack-bridge"` — cfg must be threaded in or env var read |
| `pkg/compiler/userdata.go` | 3074, 3090 | `"km-slack-threads"` fallback | Fixed by `create.go` setting `KM_SLACK_THREADS_TABLE` env var before compiler call |
| `pkg/compiler/userdata.go` | 3105 | `"km-slack-stream-messages"` fallback | Fixed by `create.go` setting `KM_SLACK_STREAM_TABLE` env var |
| `pkg/compiler/service_hcl.go` | 778 | `"km-slack-stream-messages"` fallback | Fixed by `create.go` setting `KM_SLACK_STREAM_TABLE` env var |

**Note:** `list.go:132` correctly uses `cfg.GetSlackThreadsTableName()` — no fix needed. `cmd/km-slack-bridge/main.go:157` uses `threadsTable = "km-slack-threads"` as env-var fallback — correct pattern; the Lambda module sets `KM_SLACK_THREADS_TABLE` from `var.slack_threads_table_name`.

### Env Files / systemd Units (Phase 67)

Phase 67 added `/etc/km/notify.env` and `km-slack-inbound-poller.service`. These consume env vars set at cloud-init time:

- `KM_SLACK_THREADS_TABLE` — set from `KM_SLACK_THREADS_TABLE` env var in userdata template (needs `create.go` to export correct value)
- `KM_SLACK_INBOUND_QUEUE_URL` — per-sandbox URL, **not prefix-dependent** (queue URL is a full AWS URL)
- `/etc/km/notify.env` — contains `KM_SLACK_STREAM_TABLE` and `KM_SLACK_THREADS_TABLE` lines written from `notifyEnv["KM_SLACK_THREADS_TABLE"]` in userdata.go (correct, env-var driven)

No systemd unit files contain hardcoded `km-` resource names beyond what they read from the env file. These are safe once `create.go` exports the correct env vars.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Multi-value viper defaults | Custom config file parser | `v.SetDefault()` + viper merge | Already established — identical to Phase 65 and Phase 67 |
| Helper method test | Manual string comparison | Existing `config_test.go` pattern with `writeKMConfig()` helper | Fits seamlessly |
| TF resource parameterization | New TF module version | Add `var.resource_prefix` to existing v1.0.0 in-place | No external module consumers |
| site.hcl env var reads | Shell scripts or init bootstrapping | `get_env()` with default, same as existing `KM_DOMAIN` | Already the site.hcl pattern |
| slack_threads table name in bridge | New module variable | Wire existing `var.slack_threads_table_name` through live config via dependency | Module already parameterized; only live config needs updating |

---

## Common Pitfalls

### Pitfall 1: DynamoDB Table Rename = Data Loss
**What goes wrong:** If a TF logical resource name change causes `terraform plan` to show "destroy" + "create" for a DynamoDB table, applying that plan destroys all data.
**Why it happens:** TF tracks resources by logical name in state. Renaming the logical name forces replacement.
**How to avoid:** ONLY change the `name` attribute (the physical AWS name), never the TF resource label. All seven DynamoDB modules (including the two new ones from Phase 67/68) already use `name = var.table_name`. Verify every plan with `terraform plan -detailed-exitcode` before apply.
**Warning signs:** `terraform plan` shows `-/+ destroy and then create replacement` for any DynamoDB resource.

### Pitfall 2: SES Domain Identity DNS Round-Trip
**What goes wrong:** Changing `email_subdomain` on an existing install invalidates SES domain verification. New domain needs new DKIM + MX + verification TXT records, up to 72h propagation.
**How to avoid:** Document that `email_subdomain` is a one-time choice at `km init` time. `km doctor` should check that the configured email domain matches the verified SES domain identity.

### Pitfall 3: EventBridge Schedule Group Cannot be Renamed Safely
**What goes wrong:** The EventBridge schedule group `km-at` cannot be renamed — all existing schedules under it carry the old group name. They'd need to be recreated.
**How to avoid:** Document that `resource_prefix` is a one-time choice at `km init`. Migrations are out of scope per roadmap.

### Pitfall 4: ttl-handler Hardcodes Own Function Name in Lambda Code
**What goes wrong:** `cmd/ttl-handler/main.go:351` hardcodes `"km-ttl-handler"` when creating EventBridge schedules that target itself. With a custom prefix, the Lambda is deployed as `{prefix}-ttl-handler` but the code still constructs schedule targets pointing to `"km-ttl-handler"`.
**How to avoid:** Add `KM_TTL_HANDLER_NAME` and `KM_TTL_SCHEDULER_ROLE` env vars to the ttl-handler module, set from `var.resource_prefix`. Lambda code reads them with a `"km-ttl-handler"` fallback.

### Pitfall 5: configui's `kmPrefix = "/km/"` Constant
**What goes wrong:** `cmd/configui/handlers_secrets.go:47` defines `const kmPrefix = "/km/"` and uses it as a path prefix for SSM parameter CRUD. With a custom prefix, SSM params land under `/{prefix}/` but configui only shows and manages `/km/` params.
**How to avoid:** Change `const kmPrefix = "/km/"` to a variable read from `KM_RESOURCE_PREFIX` env var with `"km"` as default.

### Pitfall 6: ecs-spot-handler is an Account-Singleton Today
**What goes wrong:** `infra/modules/ecs-spot-handler/v1.0.0/main.tf` hardcodes `name = "km-ecs-spot-handler"` for Lambda, IAM role, and EventBridge rule. Two km installs = IAM name collision.
**How to avoid:** Add `var.resource_prefix` to the ecs-spot-handler module.

### Pitfall 7: email-handler Module Hardcodes SANDBOX_TABLE_NAME
**What goes wrong:** `infra/modules/email-handler/v1.0.0/main.tf:252` has `SANDBOX_TABLE_NAME = "km-sandboxes"` hardcoded in the Lambda env block — it does NOT use a variable. With a custom prefix, the table is `{prefix}-sandboxes` but the Lambda writes to `km-sandboxes`.
**How to avoid:** Add `var.sandbox_table_name` (default `"km-sandboxes"`) to the email-handler module.

### Pitfall 8: TF State Backend Bucket is Read at Plan Time
**What goes wrong:** The TF state backend bucket (`tf-km-state-{region}`) must exist BEFORE any `terragrunt apply`. `init.go:832` constructs the bucket name as `fmt.Sprintf("tf-km-state-%s", regionLabel)` — hardcoded `tf-km`.
**How to avoid:** Make `km init` read `cfg.GetResourcePrefix()` to construct the bucket name.

### Pitfall 9 (NEW — Phase 67/68): lambda-slack-bridge Live Config Missing Phase 67 Variables
**What goes wrong:** The live `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` does NOT pass `signing_secret_path`, `slack_threads_table_name`, `resource_prefix`, or have a `dependency "slack_threads"` block. For the default prefix these all happen to resolve to the correct `"km"` defaults, so the current install works. But with a custom prefix, the bridge Lambda will: (a) look for signing secret at `/km/slack/signing-secret` instead of `/{prefix}/slack/signing-secret`, (b) use `km-slack-threads` regardless of actual table name, (c) use the wrong SQS IAM wildcard.
**How to avoid:** Phase 66 must add the `dependency "slack_threads"` block and wire all four variables through `site.label`.

### Pitfall 10 (NEW — Phase 67/68): status.go Hardcodes km-slack-threads
**What goes wrong:** `internal/app/cmd/status.go:468` calls `countActiveThreads(ctx, ddbClient, "km-slack-threads", rec.SlackChannelID)` with a hardcoded table name. The `km status` command does not receive `*config.Config` in this code path — it constructs a DDB client locally at line 456.
**How to avoid:** Thread `cfg.GetSlackThreadsTableName()` into the `km status` wide-output Slack threads display, similar to how `list.go:132` already does it.

### Pitfall 11 (NEW — Phase 67/68): ForceSlackBridgeColdStartWith Hardcodes Function Name
**What goes wrong:** `internal/app/cmd/init.go:1724` hardcodes `aws.String("km-slack-bridge")` in `ForceSlackBridgeColdStartWith`. With a custom prefix, the bridge is deployed as `{prefix}-slack-bridge` but `km slack rotate-token` tries to cold-start `km-slack-bridge`, failing silently (Lambda UpdateFunctionConfiguration returns `ResourceNotFoundException`).
**How to avoid:** Parameterize `ForceSlackBridgeColdStartWith` to accept function name, or read `KM_RESOURCE_PREFIX` env var inside the function.

### Pitfall 12 (NEW — Phase 67): DoctorConfigProvider GetResourcePrefix Type-Assert Hack
**What goes wrong:** `doctor.go:2344` uses `if appCfgTyped, ok := cfg.(*appConfigAdapter); ok` to get `GetResourcePrefix()` because the method is not in the `DoctorConfigProvider` interface. Test stubs that implement `DoctorConfigProvider` won't expose `GetResourcePrefix()`, meaning `deps.SlackResourcePrefix` always falls back to `"km"` in tests.
**How to avoid:** Add `GetResourcePrefix()` to `DoctorConfigProvider` and remove the type-assert.

---

## Code Examples

### Config helper methods (add GetEmailDomain + GetSsmPrefix)
```go
// Source: internal/app/config/config.go — new methods (GetResourcePrefix already exists)
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

### site.hcl extension
```hcl
# Source: infra/live/site.hcl — modify site block
site = {
    label           = get_env("KM_RESOURCE_PREFIX", "km")
    tf_state_prefix = "tf-${get_env("KM_RESOURCE_PREFIX", "km")}"
    domain          = get_env("KM_DOMAIN", "klankermaker.ai")
    email_subdomain = get_env("KM_EMAIL_SUBDOMAIN", "sandboxes")
    random_suffix   = get_env("KMGUID", "")
}
# backend block stays derived from site.tf_state_prefix (unchanged logic)
```

### Live DynamoDB terragrunt.hcl — new Phase 67/68 tables
```hcl
# Before (infra/live/use1/dynamodb-slack-threads/terragrunt.hcl):
inputs = { table_name = "km-slack-threads" }

# After:
inputs = { table_name = "${local.site_vars.locals.site.label}-slack-threads" }

# Before (infra/live/use1/dynamodb-slack-stream-messages/terragrunt.hcl):
inputs = { table_name = "km-slack-stream-messages" }

# After:
inputs = { table_name = "${local.site_vars.locals.site.label}-slack-stream-messages" }
```

### lambda-slack-bridge live config — add Phase 67 missing wiring
```hcl
# Add to infra/live/use1/lambda-slack-bridge/terragrunt.hcl:

dependency "slack_threads" {
  config_path = "../dynamodb-slack-threads"
  mock_outputs = {
    table_name = "km-slack-threads"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-slack-threads"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply"]
}

# Add to inputs block:
inputs = {
  ...existing inputs...
  resource_prefix          = local.site_vars.locals.site.label
  signing_secret_path      = "/${local.site_vars.locals.site.label}/slack/signing-secret"
  slack_threads_table_name = dependency.slack_threads.outputs.table_name
  # slack_threads_table_arn is needed for IAM — must add to module variables.tf too
}
```

### lambda-slack-bridge module — fix function_name
```hcl
# Source: infra/modules/lambda-slack-bridge/v1.0.0/main.tf — change local
locals {
  # Before:
  # function_name = "km-slack-bridge"
  # After:
  function_name = "${var.resource_prefix}-slack-bridge"
}
```

### slack.go — migrate signing-secret path
```go
// Before (internal/app/cmd/slack.go:759):
func PersistSigningSecret(ctx context.Context, store SlackSSMStore, secret, kmsKey string) error {
    return store.Put(ctx, "/km/slack/signing-secret", secret, true)
}

// After — requires adding cfg parameter or reading from env:
func PersistSigningSecret(ctx context.Context, store SlackSSMStore, secret, kmsKey, ssmPrefix string) error {
    return store.Put(ctx, ssmPrefix+"slack/signing-secret", secret, true)
}
// Callers pass cfg.GetSsmPrefix()
```

### create.go — set env vars for compiler
```go
// Add to the os.Setenv block in create.go (around line 379):
os.Setenv("KM_SLACK_THREADS_TABLE", cfg.GetSlackThreadsTableName())
os.Setenv("KM_SLACK_STREAM_TABLE", cfg.GetSlackStreamMessagesTableName())
```

---

## Complete Call-Site Inventory

### Category A: email domain (`"sandboxes." + domain`) — 30 sites across 8 files (UNCHANGED from stale)
All collapse to `cfg.GetEmailDomain()` or equivalent. No new sites added by Phase 67/68.

| File | Lines | Pattern |
|------|-------|---------|
| `internal/app/cmd/create.go` | 400, 413, 1114, 1116, 1207, 1658, 1866 | `"sandboxes." + networkDomain` |
| `internal/app/cmd/destroy.go` | 397, 693 | `"sandboxes." + destroyBaseDomain` |
| `internal/app/cmd/budget.go` | 387 | `"sandboxes." + domain` |
| `internal/app/cmd/email.go` | 87–92 | `emailDomain()` helper |
| `internal/app/cmd/init.go` | 1522 | `"sandboxes." + cfg.Domain` |
| `internal/app/cmd/doctor.go` | 780, 783 | `fmt.Sprintf("sandboxes.%s", domain)` |
| `cmd/budget-enforcer/main.go` | 642 | `"sandboxes.klankermaker.ai"` fallback |
| `cmd/ttl-handler/main.go` | 1396 | `"sandboxes.klankermaker.ai"` fallback |
| `pkg/compiler/userdata.go` | 2959 | `"sandboxes.klankermaker.ai"` default |
| `pkg/compiler/service_hcl.go` | 824 | `"sandboxes.klankermaker.ai"` default |
| `pkg/compiler/budget_enforcer_hcl.go` | 85 | HCL template string |
| `cmd/create-handler/main.go` | 262–266, 444 | `"sandboxes.klankermaker.ai"` fallback + prefix check |

### Category B: SSM path prefix (`"/km/"`) — 86 sites (was 75+, grew by 11)

New sites added by Phase 67 (all in `internal/app/cmd/slack.go`):
- `:311, :328` — `/km/slack/bot-scopes-cache`
- `:426, :461, :462, :463, :464, :465` — `/km/slack/last-test-timestamp`, `/km/slack/workspace`, `/km/slack/shared-channel-id`, `/km/slack/invite-email`, `/km/slack/bridge-url`, `/km/slack/last-test-timestamp`
- `:541` — `/km/slack/bot-token` (in `SlackRotateToken`)
- `:759` — `/km/slack/signing-secret` (Phase 67 — critical new site)

Existing sites from stale research: see stale research Categories B table (unchanged). Full file-by-file count: `configure_github.go` 10 paths, `doctor.go` 4+4 paths, `init.go` 1 path, `logs.go` 1 path, `status.go` 1 path, `configui/handlers_secrets.go` const, `configui/handlers.go` 2, `github-token-refresher/main.go` 2, `km-slack-bridge/main.go` 2, `email-create-handler/main.go` 1, `pkg/aws/cloudwatch.go` 2, `pkg/aws/rotation.go` 1 (log group), `pkg/compiler/service_hcl.go` 2, `pkg/compiler/userdata.go` 3 (bash templates), `pkg/slack/bridge/aws_adapters.go` 2 (doc comments), `infra/live/use1/lambda-slack-bridge/terragrunt.hcl:74` (`bot_token_path`), `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf:9,76`.

### Category C: Resource prefix (`"km-"` resource names) — ~134 sites (was 100+)

**New singleton sites from Phase 67/68 requiring action (not in stale research):**

| File | Line | Name | Action |
|------|------|------|--------|
| `internal/app/cmd/status.go` | 468 | `"km-slack-threads"` | Use `cfg.GetSlackThreadsTableName()` |
| `internal/app/cmd/doctor.go` | 2330 | `"km-sandboxes"` | Use `cfg.GetSandboxTableName()` via interface |
| `internal/app/cmd/doctor.go` | 2341 | `"km-sandboxes"` | Same |
| `internal/app/cmd/doctor.go` | 2392 | `"km-sandboxes"` | Same |
| `internal/app/cmd/init.go` | 1724 | `"km-slack-bridge"` | `cfg.GetResourcePrefix() + "-slack-bridge"` |
| `pkg/compiler/userdata.go` | 3074, 3090 | `"km-slack-threads"` | Fixed by `KM_SLACK_THREADS_TABLE` env var from `create.go` |
| `pkg/compiler/userdata.go` | 3105 | `"km-slack-stream-messages"` | Fixed by `KM_SLACK_STREAM_TABLE` env var from `create.go` |
| `pkg/compiler/service_hcl.go` | 778 | `"km-slack-stream-messages"` | Fixed by `KM_SLACK_STREAM_TABLE` env var from `create.go` |
| `infra/live/use1/dynamodb-slack-threads/terragrunt.hcl` | 36 | `"km-slack-threads"` | `"${local.site_vars.locals.site.label}-slack-threads"` |
| `infra/live/use1/dynamodb-slack-stream-messages/terragrunt.hcl` | 36 | `"km-slack-stream-messages"` | `"${local.site_vars.locals.site.label}-slack-stream-messages"` |
| `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` | 5 | `function_name = "km-slack-bridge"` | `"${var.resource_prefix}-slack-bridge"` |

**Existing singleton sites (unchanged, still in scope for Phase 66):** See stale research Category C for full list — `create.go`, `destroy.go`, `resume.go`, `shell.go`, `extend.go`, `list.go`, `unlock.go`, `stop.go`, `budget.go`, `ami.go`, `init.go`, `doctor.go`, `at.go`, `cmd/ttl-handler/main.go`, `cmd/create-handler/main.go`, `cmd/budget-enforcer/main.go`, `cmd/email-create-handler/main.go`, `cmd/km-slack-bridge/main.go`, `cmd/configui/main.go`, `pkg/aws/cloudwatch.go`, `pkg/aws/scheduler.go`, `pkg/compiler/lifecycle.go`, `pkg/compiler/service_hcl.go`, `internal/app/cmd/configure.go`, all five DynamoDB + two new DynamoDB + one Lambda live TF configs, all five Lambda TF modules + lambda-slack-bridge.

### Category D: TF state prefix (`"tf-km"`) — 14 sites (UNCHANGED)
See stale research — no new `"tf-km"` sites added by Phase 67/68.

---

## Recommended Plan Slicing

Based on the dependency graph and the Phase 67/68 additions, the wave structure from the stale research is still correct but wave content expands:

### Wave 1 (independent):
- **66-01**: Config struct (`EmailSubdomain`), helper methods (`GetEmailDomain`, `GetSsmPrefix`), unit tests. **NOTE:** `ResourcePrefix` field, `GetResourcePrefix()`, and the two Slack table helpers already exist from Phase 67 — add only the missing parts. Extend `DoctorConfigProvider` interface to include `GetResourcePrefix()`, `GetEmailDomain()`, `GetSsmPrefix()`, `GetSlackThreadsTableName()`. Remove type-assert hack at doctor.go:2344. Touches: `internal/app/config/config.go`, `config_test.go`, `internal/app/cmd/doctor.go`.

### Wave 2 (depends on 66-01):
- **66-02**: Migrate all Go email-domain call sites (Category A, ~30 sites). Touches: `internal/app/cmd/*.go`, `cmd/*/main.go`, `pkg/compiler/*.go`.
- **66-03**: Migrate all Go SSM path + resource name call sites (Categories B+C, ~100 sites). **Now includes Phase 67/68 new sites:** `slack.go` signing-secret and 4 other new paths, `status.go:468` km-slack-threads, `doctor.go:2330,2341,2392` km-sandboxes, `init.go:1724` km-slack-bridge. Add `os.Setenv("KM_SLACK_THREADS_TABLE",...)` and `os.Setenv("KM_SLACK_STREAM_TABLE",...)` to `create.go`.

### Wave 3 (depends on 66-01 for env var names):
- **66-04**: Thread `resource_prefix` into the five Lambda TF modules + lambda-slack-bridge function_name fix. Update live `terragrunt.hcl` DynamoDB inputs (including two new Phase 67/68 tables). Add `dependency "slack_threads"` block and four new `inputs` to `lambda-slack-bridge/terragrunt.hcl`. Update `site.hcl` to add `label` and `email_subdomain` env vars.

### Wave 4 (depends on all prior):
- **66-05**: TF state backend (`tf-km-state-{region}` → `tf-{prefix}-state-{region}`), `km init` state bucket naming, `km configure` wizard additions, `km doctor` prefix-collision warning, grep-audit verification pass.

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
| REQ-PLATFORM-MULTI-INSTANCE | `GetResourcePrefix()` defaults to "km" | unit | `go test ./internal/app/config/... -run TestGetResourcePrefix_Default` | ✅ already passes (Phase 67 added shim) |
| REQ-PLATFORM-MULTI-INSTANCE | `GetResourcePrefix()` returns custom value from km-config.yaml | unit | `go test ./internal/app/config/... -run TestGetResourcePrefix_Custom` | ❌ Wave 0 |
| REQ-PLATFORM-MULTI-INSTANCE | `GetEmailDomain()` defaults to "sandboxes.{domain}" | unit | `go test ./internal/app/config/... -run TestGetEmailDomain_Default` | ❌ Wave 0 |
| REQ-PLATFORM-MULTI-INSTANCE | `GetEmailDomain()` returns custom subdomain | unit | `go test ./internal/app/config/... -run TestGetEmailDomain_Custom` | ❌ Wave 0 |
| REQ-PLATFORM-MULTI-INSTANCE | `GetSsmPrefix()` returns "/{prefix}/" | unit | `go test ./internal/app/config/... -run TestGetSsmPrefix` | ❌ Wave 0 |
| REQ-PLATFORM-MULTI-INSTANCE | `GetSlackThreadsTableName()` uses resource prefix | unit | `go test ./internal/app/config/... -run TestGetSlackThreadsTableName` | ❌ Wave 0 |
| REQ-PLATFORM-MULTI-INSTANCE | `GetSlackStreamMessagesTableName()` uses resource prefix | unit | `go test ./internal/app/config/... -run TestGetSlackStreamMessagesTableName` | ❌ Wave 0 |
| REQ-CONFIG-EXTENSIBILITY | km-config.yaml with no resource_prefix loads without error | unit | `go test ./internal/app/config/... -run TestLoadBackwardCompat` | ✅ (extend existing) |
| REQ-CONFIG-EXTENSIBILITY | km-config.yaml resource_prefix overrides default | unit | `go test ./internal/app/config/... -run TestLoadResourcePrefix` | ❌ Wave 0 |
| REQ-PLATFORM-MULTI-INSTANCE | Zero `"sandboxes."` literal string concats remain | grep audit | `! grep -rn '"sandboxes\.' ./internal ./pkg ./cmd --include='*.go'` | ❌ Wave 0 (gate in 66-05) |
| REQ-PLATFORM-MULTI-INSTANCE | Zero `"/km/"` hardcoded SSM paths remain (outside _test.go) | grep audit | `! grep -rn '"/km/' ./internal ./pkg ./cmd --include='*.go' \| grep -v _test.go` | ❌ Wave 0 (gate in 66-05) |
| REQ-PLATFORM-MULTI-INSTANCE | TF plan on DynamoDB modules shows no destroy/create with prefix change | manual | `terragrunt plan` (manual smoke) | manual-only |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/config/... -count=1`
- **Per wave merge:** `go test ./internal/... ./pkg/... -count=1`
- **Phase gate:** Full suite green (`go test ./...`) before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `internal/app/config/config_test.go` — extend with `TestGetResourcePrefix_Custom`, `TestGetEmailDomain_Default`, `TestGetEmailDomain_Custom`, `TestGetSsmPrefix`, `TestGetSlackThreadsTableName`, `TestGetSlackStreamMessagesTableName`, `TestLoadResourcePrefix`, `TestLoadEmailSubdomain`
- [ ] Note: `TestGetResourcePrefix_Default` may already pass (Phase 67 shim) — verify and extend if needed
- [ ] No new framework install needed — stdlib `go test` already in use

---

## Open Questions

1. **Should `resource_prefix` be prompted in `km configure` wizard?**
   - What we know: `km configure` currently asks for domain, accounts, SSO, region, email, safe_phrase.
   - Recommendation: Add to wizard with default value `"km"`, prompt: "Resource prefix (default: km — change only for second install in same account)"

2. **Should `km init` error if another prefix's resources already exist?**
   - Recommendation: Add a `km doctor` check `checkPrefixCollision` that warns if `{prefix}-ttl-handler` Lambda already exists, rather than blocking `km init`.

3. **`km-sandbox-containment` SCP name — does it need to be prefixed?**
   - Recommendation: Leave SCP name as `"km-sandbox-containment"` (hardcoded, not prefixed). Only one SCP per org. Documented caveat.

4. **`pkg/aws/ec2_ami.go:54` uses prefix `"km-"` to filter AMIs in `km ami list`**
   - Recommendation: Thread `cfg.GetResourcePrefix()` into the AMI listing call.

5. **NEW: `PersistSigningSecret` signature change — does it break callers?**
   - What we know: The function is used in `km slack init` (calls `PersistSigningSecret`) and `km slack rotate-signing-secret`. Both are in `slack.go` and receive `cfg`.
   - Recommendation: Add `ssmPrefix string` parameter. Callers in `slack.go` pass `cfg.GetSsmPrefix()`.

6. **NEW: `ForceSlackBridgeColdStartWith` is exported (public) for tests — how to parameterize?**
   - What we know: `internal/app/cmd/init.go:1718` exports this function for unit testing. Tests pass a fake Lambda client.
   - Recommendation: Add a `functionName string` parameter; callers pass `cfg.GetResourcePrefix() + "-slack-bridge"`. Test stubs verify the function name passed, not a hardcoded constant.

---

## Sources

### Primary (HIGH confidence)
- Direct code audit of `/Users/khundeck/working/klankrmkr/` codebase (2026-05-04)
- `internal/app/config/config.go` — Config struct definition, Load() function, confirmed `ResourcePrefix` + `GetResourcePrefix()` already added by Phase 67
- `internal/app/config/config.go:339-371` — `GetSlackThreadsTableName()` and `GetSlackStreamMessagesTableName()` confirmed implemented
- `internal/app/cmd/doctor.go:148-200` — `DoctorConfigProvider` interface — confirmed does NOT have `GetResourcePrefix()` (type-assert hack at 2344)
- `internal/app/cmd/status.go:468` — confirmed hardcoded `"km-slack-threads"`
- `internal/app/cmd/init.go:1724` — confirmed hardcoded `"km-slack-bridge"`
- `internal/app/cmd/slack.go:759` — confirmed hardcoded `/km/slack/signing-secret`
- `infra/live/site.hcl` — confirmed `label = "km"` hardcoded, `email_subdomain` absent
- `infra/modules/lambda-slack-bridge/v1.0.0/main.tf:5` — confirmed `function_name = "km-slack-bridge"` hardcoded local
- `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` — confirmed `var.resource_prefix` exists (Phase 67) but not used for function_name
- `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` — confirmed missing `signing_secret_path`, `slack_threads_table_name`, `resource_prefix` inputs
- `infra/live/use1/dynamodb-slack-threads/terragrunt.hcl:36` — confirmed `table_name = "km-slack-threads"` hardcoded
- `infra/live/use1/dynamodb-slack-stream-messages/terragrunt.hcl:36` — confirmed `table_name = "km-slack-stream-messages"` hardcoded
- `infra/modules/dynamodb-slack-threads/v1.0.0/main.tf` — confirmed `name = var.table_name` (safe)
- `infra/modules/dynamodb-slack-stream-messages/v1.0.0/main.tf` — confirmed `name = var.table_name` (safe)
- `pkg/aws/sqs.go:44-45` — confirmed `SlackInboundQueueName(resourcePrefix, sandboxID)` already prefix-aware
- `internal/app/cmd/create_slack_inbound.go:100` — confirmed uses `cfg.GetResourcePrefix()`
- `internal/app/cmd/list.go:132` — confirmed uses `cfg.GetSlackThreadsTableName()` (correct)
- `.planning/phases/66-multi-instance-support-configurable-resource-prefix-and-email-subdomain/66-VALIDATION.md` — validation strategy (unchanged)

### Secondary (MEDIUM confidence)
- `git log --oneline --since="2026-05-01"` — confirmed Phase 67, 67.1, 68 commits and new files
- `infra/modules/lambda-slack-bridge/v1.0.0/main.tf:148,155,171,180,239,282-286` — Phase 67/68 IAM policy and env var additions confirmed

### Tertiary (LOW confidence — manual verification needed)
- TF plan behavior for `function_name` change on `aws_lambda_function`: LOW — changing the local from `"km-slack-bridge"` to `"${var.resource_prefix}-slack-bridge"` changes the physical Lambda name, which triggers destroy+create on the Lambda function resource. Existing install must use `terraform state mv` or add `lifecycle { create_before_destroy = true }`. **This is a significant risk for existing installs** — document explicitly that changing prefix on a live install requires manual state moves.

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — well-established viper + config pattern, Phase 65 and 67 precedents
- Architecture: HIGH — site.hcl threading and module parameterization patterns fully verified; Phase 67 forward-compat shims confirmed in live code
- Pitfalls: HIGH — all identified from direct code audit; DynamoDB replace risk is architectural fact; new pitfalls 9-12 confirmed from live grep
- Call-site inventory: HIGH — generated from live grep of codebase (2026-05-04)
- Phase 67/68 drift audit: HIGH — every file and line number verified against live codebase

**Research date:** 2026-05-04
**Valid until:** 2026-06-04 (stable codebase, no external dependencies changing)
**Supersedes:** 2026-05-01 research (archived in git at commit 0d74e7f)

## RESEARCH COMPLETE
