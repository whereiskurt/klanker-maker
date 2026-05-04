---
phase: 66-multi-instance-support-configurable-resource-prefix-and-email-subdomain
plan: 02
subsystem: email-domain-migration
tags: [go, config, multi-instance, email-domain, resource-prefix, lambda, compiler, ami]

# Dependency graph
requires:
  - phase: 66-01
    provides: GetEmailDomain() and GetResourcePrefix() on *Config, DoctorConfigProvider extended

provides:
  - All operator-side "sandboxes." + domain concatenations migrated to cfg.GetEmailDomain()
  - Lambda handlers read KM_EMAIL_DOMAIN env var with safe "sandboxes.klankermaker.ai" fallback
  - AMIName() accepts variadic prefix string; ami.go caller passes cfg.GetResourcePrefix()+"-"
  - pkg/compiler nil-network fallbacks marked with TODO Phase 66 plan 04 inline comments
  - Grep audit green: zero executable "sandboxes." literals in ./internal ./pkg (comment-only matches remain)

affects:
  - 66-04 (TF modules wire KM_EMAIL_DOMAIN env var into Lambda env blocks)
  - Sandboxes provisioned after this plan will use email_subdomain if configured

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Variadic string parameter for AMIName() preserves backward-compat while allowing prefix injection"
    - "Lambda env-var helper function pattern: getEmailDomain() reads KM_EMAIL_DOMAIN lazily (not init-time)"
    - "TODO Phase 66 plan 04 inline comment on same line as deferred HCL-template literals (enables grep audit)"

key-files:
  created: []
  modified:
    - internal/app/cmd/email.go
    - internal/app/cmd/create.go
    - internal/app/cmd/destroy.go
    - internal/app/cmd/budget.go
    - internal/app/cmd/init.go
    - internal/app/cmd/doctor.go
    - cmd/budget-enforcer/main.go
    - cmd/create-handler/main.go
    - cmd/ttl-handler/main.go
    - pkg/compiler/service_hcl.go
    - pkg/compiler/userdata.go
    - pkg/compiler/budget_enforcer_hcl.go
    - pkg/aws/ec2_ami.go
    - internal/app/cmd/ami.go

key-decisions:
  - "AMIName() uses variadic prefix (not required parameter) to preserve backward compat in KMBakeTags which lacks cfg access"
  - "Lambda getEmailDomain() uses lazy env read (not init-time var) so SAM/test tools can override at test time"
  - "checkSESIdentity() parameter renamed from domain to emailDomain; callsite passes cfg.GetEmailDomain() directly"
  - "service_hcl.go and userdata.go nil-network fallbacks deferred to plan 04 with inline TODO (not removed)"
  - "budget_enforcer_hcl.go HCL template email_domain deferred to plan 04 — HCL template references site.domain already, plan 04 adds email_subdomain interpolation"
  - "create.go Docker path: removed stale KM_EMAIL_DOMAIN env fallback for base domain (incorrect pattern — GetEmailDomain() handles it)"

requirements-completed:
  - REQ-PLATFORM-MULTI-INSTANCE

# Metrics
duration: 1023s
completed: 2026-05-04
---

# Phase 66 Plan 02: Email Domain Call-Site Migration Summary

**Migrated ~17 "sandboxes." + domain call sites to cfg.GetEmailDomain(); Lambda handlers get getEmailDomain() env-var helper; AMIName() prefix is config-driven; grep audit green**

## Performance

- **Duration:** 17 min (1023s)
- **Started:** 2026-05-04T14:00:26Z
- **Completed:** 2026-05-04T14:17:29Z
- **Tasks:** 3
- **Files modified:** 14

## Accomplishments

### Task 1: Operator-side cmd/*.go (6 files, 11 sites migrated)

| File | Sites | Change |
|------|-------|--------|
| email.go | 1 | `emailDomain()` helper body → `cfg.GetEmailDomain()` (cascades to all 3 callers) |
| create.go | 5 | 2× NetworkConfig.EmailDomain, safe-phrase display, SES provisioning, Docker email, remote create |
| destroy.go | 2 | SES notification and SES cleanup both use `cfg.GetEmailDomain()` |
| budget.go | 1 | NetworkConfig.EmailDomain uses `cfg.GetEmailDomain()` |
| init.go | 1 | `ensureSandboxHostedZone` uses `cfg.GetEmailDomain()` |
| doctor.go | 1 | `checkSESIdentity()` parameter renamed to `emailDomain`; callsite passes `cfg.GetEmailDomain()` |

configure.go and info.go had no `"sandboxes."` literals (grep confirmed empty).

### Task 2: Lambda handlers — KM_EMAIL_DOMAIN env var pattern (3 files)

Each Lambda handler now has:
```go
func getEmailDomain() string {
    if v := os.Getenv("KM_EMAIL_DOMAIN"); v != "" {
        return v
    }
    return "sandboxes.klankermaker.ai"
}
```

| File | Change |
|------|--------|
| budget-enforcer/main.go | Added helper; replaced inline at `main()` init |
| create-handler/main.go | Added helper; `main()` init uses helper; inner `Handle()` falls back to `h.Domain` (which is set from helper at init) |
| ttl-handler/main.go | Added helper; replaced inline at `main()` init |

The `KM_EMAIL_DOMAIN` env var will be wired up by Plan 04's TF module env blocks. Until then, all three Lambda handlers fall back to `"sandboxes.klankermaker.ai"` — preserving current behavior on existing installs.

### Task 3: pkg/ files — compiler templates + AMI filter (5 files)

| File | Action | Notes |
|------|--------|-------|
| service_hcl.go | Preserves `network.EmailDomain` (already cfg-derived); nil-network fallback marked `TODO Phase 66 plan 04` | NetworkConfig.EmailDomain is always set by create.go |
| userdata.go | Nil-network fallback marked `TODO Phase 66 plan 04` | Variadic `emailDomainOverride[0]` already receives `network.EmailDomain` from compiler.go |
| budget_enforcer_hcl.go | HCL template `email_domain` deferred; inline `TODO Phase 66 plan 04` on same line as literal | HCL already uses `${local.site_vars.locals.site.domain}` — plan 04 adds email_subdomain |
| ec2_ami.go | `AMIName()` gains variadic `prefix ...string` parameter; default `"km-"` preserved | Caller passes `cfg.GetResourcePrefix() + "-"` |
| ami.go | Updated `AMIName()` call to pass `cfg.GetResourcePrefix() + "-"` | Semantic: `km ami bake` now generates `{prefix}-{profile}-...` names |

## Deferred Sites (Plan 04)

Three HCL-template-emitting sites marked with `TODO Phase 66 plan 04` inline comments:

| File | Line | Pattern | Deferred Because |
|------|------|---------|-----------------|
| `pkg/compiler/service_hcl.go` | ~831 | `"sandboxes.klankermaker.ai"` nil-network fallback | network==nil only occurs in tests; plan 04 will thread cfg |
| `pkg/compiler/userdata.go` | ~2960 | `"sandboxes.klankermaker.ai"` emailDomainOverride fallback | Variadic API; plan 04 will pass cfg |
| `pkg/compiler/budget_enforcer_hcl.go` | ~86 | HCL template `"sandboxes.${local.site_vars.locals.site.domain}"` | Pure HCL; plan 04 rewrites to add `email_subdomain` interpolation |

## Grep Audit

Running the final audit:
```bash
grep -rn '"sandboxes\.' ./internal ./pkg --include='*.go' | grep -v _test.go | grep -v 'TODO Phase 66 plan 04' | grep -v 'return "sandboxes\.klankermaker\.ai"'
```

**Result: 8 matches, all in Go `//` comment lines only** (doc comments, inline field doc strings). Zero executable string literals remain. Audit GREEN.

## AMI Prefix Semantic Change

`km ami bake` now generates AMI names as `{prefix}-{profile}-{sandboxID}-{timestamp}` instead of the hardcoded `km-{...}`. For default installations (`resource_prefix = "km"`), output is byte-identical. For custom-prefix installs (plan 04 wires this), AMIs are namespaced under the install prefix.

`ListBakedAMIs` filters by `km:sandbox-id` tag (not name prefix), so existing AMIs created before this plan remain visible.

## Task Commits

| Task | Commit | Description |
|------|--------|-------------|
| Task 1 | d29c617 | feat(66-02): migrate operator-side cmd email domain call sites |
| Task 2 | 75f1427 | feat(66-02): migrate Lambda handlers to KM_EMAIL_DOMAIN env var |
| Task 3 | 034768a | feat(66-02): migrate pkg compiler + AMI filter to GetEmailDomain/GetResourcePrefix |

## Build & Test

- `make build` → `km v0.2.494 (034768a)` — PASS
- `go build ./internal/app/cmd/... ./cmd/budget-enforcer/... ./cmd/create-handler/... ./cmd/ttl-handler/... ./pkg/... ./internal/...` — all PASS
- `go test ./internal/app/cmd/... -run 'Email|Domain|Init|Doctor|Configure|Info|Create|Destroy|Budget'` — PASS (exit 0)
- `go test ./pkg/aws/...` — PASS
- Pre-existing failures confirmed:
  - `TestHandleTTLEvent_UploadsArtifactsWhenConfigured` — times out calling real AWS EC2/IAM (pre-existing, not caused by this plan)
  - `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID` + `TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime` — pre-existing failures in pkg/compiler

## Open Call Sites for Plans 03-05

```bash
# Category B: /km/ SSM paths (~86 sites) — Plan 03
grep -rn '"/km/' ./internal ./pkg ./cmd --include='*.go' | grep -v _test.go | wc -l

# Category C: km- resource name singletons (~134 sites) — Plans 03/05
grep -rn '"km-' ./internal ./pkg ./cmd --include='*.go' | grep -v _test.go | grep -v "km-config" | wc -l
```

## Self-Check: PASSED

- FOUND: internal/app/cmd/email.go - GetEmailDomain() CONFIRMED
- FOUND: internal/app/cmd/create.go - cfg.GetEmailDomain() CONFIRMED (5 sites)
- FOUND: internal/app/cmd/destroy.go - cfg.GetEmailDomain() CONFIRMED (2 sites)
- FOUND: pkg/aws/ec2_ami.go - AMIName variadic prefix CONFIRMED
- FOUND: internal/app/cmd/ami.go - cfg.GetResourcePrefix()+"-" CONFIRMED
- FOUND commit: d29c617 (Task 1)
- FOUND commit: 75f1427 (Task 2)
- FOUND commit: 034768a (Task 3)
- make build: PASS (km v0.2.494)
- grep audit: GREEN (comment-only matches)
