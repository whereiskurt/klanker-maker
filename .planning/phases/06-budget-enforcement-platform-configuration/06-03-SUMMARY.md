---
phase: 06-budget-enforcement-platform-configuration
plan: "03"
subsystem: config-domain
tags: [domain, config, schema, compiler, terragrunt, configui]
dependency_graph:
  requires: [06-01]
  provides: [CONF-02]
  affects: [pkg/compiler, pkg/profile, internal/app/cmd, cmd/configui, infra/live]
tech_stack:
  added: []
  patterns:
    - Config.Domain field with empty-string sentinel (callers default to "klankermaker.ai")
    - NetworkConfig.EmailDomain propagation through compiler pipeline
    - JSON schema $id __SCHEMA_DOMAIN__ placeholder replaced at compile time
    - apiVersion pattern ^.+/v1alpha1$ allows any domain prefix
    - Variadic emailDomainOverride parameter for backward-compatible function signatures
key_files:
  created: []
  modified:
    - internal/app/cmd/create.go
    - internal/app/cmd/destroy.go
    - internal/app/config/config.go
    - pkg/compiler/compiler.go
    - pkg/compiler/service_hcl.go
    - pkg/compiler/userdata.go
    - pkg/profile/schema.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - schemas/sandbox_profile.schema.json
    - infra/live/site.hcl
    - cmd/configui/handlers.go
    - cmd/configui/handlers_editor.go
    - cmd/configui/main.go
    - cmd/configui/templates/editor.html
decisions:
  - "Config.Domain is empty by default (no viper default) — callers use empty check with klankermaker.ai fallback to preserve TestLoadBackwardCompat compatibility"
  - "NetworkConfig.EmailDomain added to thread domain through compiler pipeline without changing Compile() signature"
  - "generateUserData and generateECSServiceHCL use variadic/optional domain parameters for backward-compatible test calls"
  - "JSON schema $id uses __SCHEMA_DOMAIN__ placeholder replaced at compileSchemaForDomain() call time"
  - "apiVersion changed from const to pattern ^.+/v1alpha1$ — forks use their own domain prefix"
  - "NotificationsEmail field added to userDataParams so spot interruption from-address is config-derived"
metrics:
  duration: "955s"
  completed_date: "2026-03-22"
  tasks_completed: 2
  files_modified: 14
---

# Phase 06 Plan 03: Replace Hardcoded klankermaker.ai Domain References Summary

Replace all ~20 hardcoded `klankermaker.ai` references with config-derived values so forks work with any domain (CONF-02).

## What Was Built

All production code paths now derive the domain from `cfg.Domain` / `KM_DOMAIN` environment variable, with `"klankermaker.ai"` as the fallback default when no configuration is provided. A fork with `domain: mysandboxes.example.com` in `km-config.yaml` (or `KM_DOMAIN=mysandboxes.example.com`) works end-to-end without code changes.

## Tasks Completed

### Task 1: Replace hardcoded domain in Go source and update apiVersion validation
- **commit:** 965146e

**create.go and destroy.go:**
- Replaced `const emailDomain = "sandboxes.klankermaker.ai"` with `cfg.Domain`-derived variables
- `emailDomain := "sandboxes." + baseDomain` where `baseDomain = cfg.Domain` (fallback to "klankermaker.ai")
- `create.go` also sets `NetworkConfig.EmailDomain` so the compiler uses the correct domain

**pkg/compiler/service_hcl.go:**
- Added `EmailDomain string` field to `NetworkConfig`
- `generateECSServiceHCL` reads `network.EmailDomain` with fallback to "sandboxes.klankermaker.ai"
- Updated `SandboxEmail` and `NotificationsEmail` struct comment to "config-derived"

**pkg/compiler/userdata.go:**
- `generateUserData` accepts variadic `emailDomainOverride ...string` parameter (backward-compatible)
- Added `NotificationsEmail` field to `userDataParams` — spot interruption notification from-address
- Template uses `{{ .NotificationsEmail }}` instead of hardcoded "notifications@sandboxes.klankermaker.ai"

**pkg/compiler/compiler.go:**
- `compileEC2` passes `network.EmailDomain` to `generateUserData`

**pkg/profile/schema.go:**
- Added `defaultSchemaDomain = "klankermaker.ai"` constant
- Added `SchemaForDomain(domain string)` public function
- Added `compileSchemaForDomain(domain string)` that replaces `__SCHEMA_DOMAIN__` placeholder
- `Schema()` delegates to `compileSchemaForDomain(defaultSchemaDomain)` for backward compatibility

**pkg/profile/schemas/sandbox_profile.schema.json:**
- `$id` changed from `https://klankermaker.ai/...` to `https://__SCHEMA_DOMAIN__/...`
- `apiVersion` changed from `"const": "klankermaker.ai/v1alpha1"` to `"pattern": "^.+/v1alpha1$"`

**schemas/sandbox_profile.schema.json (root):**
- Same `apiVersion` pattern change for tooling consistency

**internal/app/config/config.go:**
- No viper default added for `domain` (preserves TestLoadBackwardCompat which expects empty Domain when no config file)
- Comment updated: "When empty, callers default to 'klankermaker.ai'"

### Task 2: Replace hardcoded domain in Terraform/Terragrunt and ConfigUI templates
- **commit:** 0660db3

**infra/live/site.hcl:**
- `domain = get_env("KM_DOMAIN", "klankermaker.ai")` — reads from environment with fallback

**cmd/configui/handlers.go:**
- Added `domain string` field to `Handler` struct

**cmd/configui/handlers_editor.go:**
- Added `Domain string` field to `EditorData` struct
- `handleEditorPage` populates `Domain` from `h.domain` (fallback to "klankermaker.ai")

**cmd/configui/main.go:**
- Handler initialized with `domain: envOrDefault("KM_DOMAIN", "klankermaker.ai")`

**cmd/configui/templates/editor.html:**
- `NEW_PROFILE_TEMPLATE` uses `{{.Domain}}/v1alpha1` instead of `klankermaker.ai/v1alpha1`

## Verification Results

```
ok  github.com/whereiskurt/klankrmkr/cmd/configui
ok  github.com/whereiskurt/klankrmkr/cmd/ttl-handler
ok  github.com/whereiskurt/klankrmkr/internal/app/cmd
ok  github.com/whereiskurt/klankrmkr/internal/app/config
ok  github.com/whereiskurt/klankrmkr/pkg/aws
ok  github.com/whereiskurt/klankrmkr/pkg/compiler
ok  github.com/whereiskurt/klankrmkr/pkg/lifecycle
ok  github.com/whereiskurt/klankrmkr/pkg/profile
ok  github.com/whereiskurt/klankrmkr/pkg/terragrunt
ok  github.com/whereiskurt/klankrmkr/sidecars/audit-log
ok  github.com/whereiskurt/klankrmkr/sidecars/dns-proxy/dnsproxy
ok  github.com/whereiskurt/klankrmkr/sidecars/http-proxy/httpproxy
```

**Production code grep result:** Zero hardcoded `klankermaker.ai` in active code paths. All remaining references are fallback defaults (correct) or documentation comments.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing feature] Added NotificationsEmail to userDataParams**
- **Found during:** Task 1
- **Issue:** Spot interruption shell template had hardcoded `notifications@sandboxes.klankermaker.ai` as from-address for SES send-email call — was not a simple const but a hardcoded string in a Go template
- **Fix:** Added `NotificationsEmail` field to `userDataParams` and populated as `"notifications@" + emailDomain`; template uses `{{ .NotificationsEmail }}`
- **Files modified:** `pkg/compiler/userdata.go`
- **Commit:** 965146e

**2. [Rule 1 - Bug] Config.Domain default handling**
- **Found during:** Task 1
- **Issue:** Adding `v.SetDefault("domain", "klankermaker.ai")` broke `TestLoadBackwardCompat` which expects `Domain` to be empty when no km-config.yaml is present
- **Fix:** Removed viper default; callers do empty-string check with fallback instead
- **Files modified:** `internal/app/config/config.go`
- **Commit:** 965146e

## Self-Check: PASSED
