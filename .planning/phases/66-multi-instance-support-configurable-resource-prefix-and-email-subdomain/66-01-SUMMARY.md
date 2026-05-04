---
phase: 66-multi-instance-support-configurable-resource-prefix-and-email-subdomain
plan: 01
subsystem: config
tags: [go, config, viper, multi-instance, resource-prefix, email-subdomain, ssm, doctor]

# Dependency graph
requires:
  - phase: 67-slack-inbound
    provides: ResourcePrefix field + GetResourcePrefix() shim already in config.go
  - phase: 68-slack-transcript-streaming
    provides: GetSlackStreamMessagesTableName() already in DoctorConfigProvider interface
provides:
  - EmailSubdomain field + viper binding (email_subdomain default "sandboxes") in Config struct
  - GetEmailDomain() helper method on *Config (nil-safe, falls back to "sandboxes.klankermaker.ai")
  - GetSsmPrefix() helper method on *Config (returns "/{prefix}/")
  - DoctorConfigProvider interface extended with 4 methods (GetResourcePrefix, GetEmailDomain, GetSsmPrefix, GetSlackThreadsTableName)
  - appConfigAdapter 4 new wrapper methods satisfying the extended interface
  - type-assert hack at former doctor.go:2344 removed (Pitfall 12 fixed)
affects:
  - 66-02 (email domain call-site migration uses GetEmailDomain)
  - 66-03 (SSM path + resource name migration uses GetSsmPrefix, GetResourcePrefix)
  - 66-04 (Terragrunt threading)
  - 66-05 (doctor checks use GetResourcePrefix, GetEmailDomain via interface)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Config helper methods use nil-safe pointer receivers: if c == nil || c.Field == '' { return default }"
    - "viper: SetDefault + merge-key list + struct initialization triple for each new config field"
    - "DoctorConfigProvider interface extended cleanly - no type-assert hacks"

key-files:
  created: []
  modified:
    - internal/app/config/config.go
    - internal/app/config/config_test.go
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go

key-decisions:
  - "EmailSubdomain field uses nil-safe GetEmailDomain() helper matching Phase 67 GetResourcePrefix() nil-safety pattern"
  - "GetEmailDomain() hardcodes 'klankermaker.ai' as the nil-receiver domain fallback (matching existing platform default)"
  - "DoctorConfigProvider interface extended with 4 methods rather than relying on type-assert; Pitfall 12 from RESEARCH.md resolved"
  - "Test stubs (testConfig, testDoctorConfig) return safe 'km' defaults for all 4 new methods; doctorStaleAMIConfig inherits via embedding"

requirements-completed:
  - REQ-CONFIG-EXTENSIBILITY
  - REQ-PLATFORM-MULTI-INSTANCE

# Metrics
duration: 3min
completed: 2026-05-04
---

# Phase 66 Plan 01: Config Foundation Summary

**EmailSubdomain field + GetEmailDomain/GetSsmPrefix helpers added to Config; DoctorConfigProvider extended with 4 methods eliminating the type-assert hack at doctor.go:2344**

## Performance

- **Duration:** 3 min (179s)
- **Started:** 2026-05-04T13:49:06Z
- **Completed:** 2026-05-04T13:52:05Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments

- Added `EmailSubdomain string` field to Config struct with viper binding (`email_subdomain` key, default `"sandboxes"`)
- Added `GetEmailDomain()` method: nil-safe, returns `"{subdomain}.{domain}"` falling back to `"sandboxes.klankermaker.ai"`
- Added `GetSsmPrefix()` method: returns `"/{prefix}/"` using existing nil-safe `GetResourcePrefix()`
- Extended `DoctorConfigProvider` interface with 4 methods; added 4 adapter wrappers to `appConfigAdapter`; removed type-assert hack (Pitfall 12 from RESEARCH.md)
- Added 7 new unit tests covering all new helpers and viper binding; Phase 67 tests remain untouched

## Task Commits

Each task was committed atomically:

1. **Task 1: EmailSubdomain field + GetEmailDomain/GetSsmPrefix + unit tests** - `0a60471` (feat)
2. **Task 2: Extend DoctorConfigProvider + appConfigAdapter + remove type-assert hack** - `c4ba2e9` (feat)

**Plan metadata:** (docs commit follows)

_Note: Both tasks used TDD (RED → GREEN pattern)_

## Files Created/Modified

- `internal/app/config/config.go` - Added `EmailSubdomain` field (line 164), viper SetDefault + merge key + struct init, `GetEmailDomain()` and `GetSsmPrefix()` methods (after `GetResourcePrefix()`)
- `internal/app/config/config_test.go` - Added 7 new tests: `TestGetResourcePrefix_Custom`, `TestGetEmailDomain_Default/Custom/NilSafe`, `TestGetSsmPrefix_Default/Custom`, `TestLoadEmailSubdomain`
- `internal/app/cmd/doctor.go` - Extended `DoctorConfigProvider` interface with 4 methods; added 4 adapter wrappers to `appConfigAdapter`; replaced type-assert hack with direct `cfg.GetResourcePrefix()` call
- `internal/app/cmd/doctor_test.go` - Added 4 methods to `testConfig` and `testDoctorConfig` stubs; `doctorStaleAMIConfig` inherits via embedding

## Type-Assert Hack Removal (Pitfall 12 Fix)

**Before (doctor.go:2343-2347):**
```go
// Derive the resource prefix from the concrete config type.
// DoctorConfigProvider doesn't expose GetResourcePrefix, so we type-assert.
deps.SlackResourcePrefix = "km" // default fallback
if appCfgTyped, ok := cfg.(*appConfigAdapter); ok {
    deps.SlackResourcePrefix = appCfgTyped.cfg.GetResourcePrefix()
}
```

**After (doctor.go:2355-2357):**
```go
// Derive the resource prefix directly via the interface (Phase 66: GetResourcePrefix
// is now part of DoctorConfigProvider — no type-assert needed).
deps.SlackResourcePrefix = cfg.GetResourcePrefix()
```

## Call-Site Inventory for Plans 02-05

Remaining open call sites (for plan-02/03/05 audits):

```bash
# Category A: email domain literal concats (~30 sites)
grep -rn '"sandboxes\.' ./internal ./pkg ./cmd --include='*.go' | grep -v _test.go

# Category B: /km/ SSM paths (~86 sites)
grep -rn '"/km/' ./internal ./pkg ./cmd --include='*.go' | grep -v _test.go

# Category C: km- resource name singletons (~134 sites)
grep -rn '"km-' ./internal ./pkg ./cmd --include='*.go' | grep -v _test.go | grep -v "km-config"
```

## Decisions Made

- Used nil-safe pointer receiver pattern consistent with Phase 67's `GetResourcePrefix()` (nil returns hardcoded defaults, not panics)
- `GetEmailDomain()` hardcodes `"klankermaker.ai"` as the nil-receiver fallback — matches existing platform domain default
- Interface extended at `DoctorConfigProvider` level rather than creating a new narrower interface — cleaner since all existing test stubs already implement the full interface

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Config foundation complete: `GetEmailDomain()`, `GetSsmPrefix()`, `GetResourcePrefix()` all available as clean interface methods
- Plans 02-05 can now use `cfg.GetEmailDomain()` (Category A), `cfg.GetSsmPrefix()` (Category B), `cfg.GetResourcePrefix()` (Category C) to migrate call sites
- `DoctorConfigProvider` extended cleanly — Plans 05 doctor checks can use the new methods without any type-assert workarounds
- Phase 67's existing `GetResourcePrefix()`, `GetSlackThreadsTableName()`, `GetSlackStreamMessagesTableName()` all unchanged and passing

---
*Phase: 66-multi-instance-support-configurable-resource-prefix-and-email-subdomain*
*Completed: 2026-05-04*

## Self-Check: PASSED

- FOUND: internal/app/config/config.go
- FOUND: internal/app/config/config_test.go
- FOUND: internal/app/cmd/doctor.go
- FOUND: internal/app/cmd/doctor_test.go
- FOUND commit: 0a60471
- FOUND commit: c4ba2e9
- EmailSubdomain field in config.go: CONFIRMED
- GetEmailDomain method in config.go: CONFIRMED
- GetSsmPrefix method in config.go: CONFIRMED
- GetResourcePrefix in DoctorConfigProvider interface: CONFIRMED
- Type-assert hack removed: CONFIRMED
