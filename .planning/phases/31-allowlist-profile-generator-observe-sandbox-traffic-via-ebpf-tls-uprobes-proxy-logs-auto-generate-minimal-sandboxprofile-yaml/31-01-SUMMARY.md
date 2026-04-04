---
phase: 31-allowlist-profile-generator
plan: "01"
subsystem: allowlistgen
tags: [tls, ebpf, profile, allowlist, generator, normalization]
dependency_graph:
  requires:
    - pkg/ebpf/tls (EventHandler, ParseHTTPRequest, ExtractGitHubRepo)
    - pkg/profile (SandboxProfile, Validate)
    - golang.org/x/net/publicsuffix
  provides:
    - pkg/allowlistgen.Recorder
    - pkg/allowlistgen.CollapseToDNSSuffixes
    - pkg/allowlistgen.Generate / GenerateYAML
  affects:
    - pkg/profile/types.go (omitempty fix on MaxLifetime)
tech_stack:
  added:
    - pkg/allowlistgen (new package)
  patterns:
    - TDD (RED then GREEN for both tasks)
    - linux build constraint on HandleTLSEvent (tls package is linux-only)
    - HTTPS-implies-DNS union before suffix collapse
key_files:
  created:
    - pkg/allowlistgen/recorder.go
    - pkg/allowlistgen/recorder_tls.go
    - pkg/allowlistgen/recorder_test.go
    - pkg/allowlistgen/recorder_tls_test.go
    - pkg/allowlistgen/normalize.go
    - pkg/allowlistgen/normalize_test.go
    - pkg/allowlistgen/generator.go
    - pkg/allowlistgen/generator_test.go
  modified:
    - pkg/profile/types.go (MaxLifetime omitempty fix)
decisions:
  - "Split HandleTLSEvent into recorder_tls.go with //go:build linux to isolate tls package dependency"
  - "TestNormalizeSuffixes updated: githubusercontent.com is itself a PSL entry, so raw.githubusercontent.com stays as .raw.githubusercontent.com (correct conservative behavior)"
  - "HTTPS-implies-DNS: unionStrings(dns, hosts) before CollapseToDNSSuffixes so every observed host contributes to DNS suffixes"
  - "Generator produces full schema-valid scaffold profile (not just egress fragment) so TestGenerateValidates can call profile.Validate()"
metrics:
  duration: "~8 minutes"
  completed: "2026-04-04"
  tasks: 2
  files: 9
requirements: [AGEN-02, AGEN-03]
---

# Phase 31 Plan 01: allowlistgen — Recorder, Normalizer, Generator Summary

Thread-safe Recorder accumulating DNS/HTTP/GitHub observations with publicsuffix-based DNS collapse and HTTPS-implies-DNS profile generation producing schema-valid SandboxProfile YAML.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Recorder and DNS suffix normalization with tests | add6c85 | recorder.go, recorder_tls.go, normalize.go + tests |
| 2 | Generator produces valid SandboxProfile YAML with deduplication | d84e32a | generator.go, generator_test.go, pkg/profile/types.go |

## What Was Built

### Recorder (`recorder.go` + `recorder_tls.go`)

Thread-safe struct with three `map[string]struct{}` fields protected by `sync.Mutex`:
- `RecordDNSQuery` — strips trailing FQDN dot, lowercases, deduplicates
- `RecordHost` — strips port via `net.SplitHostPort`, lowercases
- `RecordRepo` — lowercases owner/repo pair
- `HandleTLSEvent` (linux-only) — processes `DirWrite` events, parses HTTP/1.1, records host and GitHub repo
- Accessors `DNSDomains()`, `Hosts()`, `Repos()` return sorted slices

### Normalizer (`normalize.go`)

`CollapseToDNSSuffixes` uses `golang.org/x/net/publicsuffix.EffectiveTLDPlusOne` to:
- Collapse multiple subdomains to their eTLD+1 suffix (`.github.com`)
- Preserve correct PSL entries (`githubusercontent.com` is itself a PSL entry → `raw.githubusercontent.com` maps to `.raw.githubusercontent.com`, not `.githubusercontent.com`)
- Skip bare TLDs (errors from `EffectiveTLDPlusOne`) to prevent `.com`-level over-permissive suffixes

### Generator (`generator.go`)

`Generate(base string)` implements HTTPS-implies-DNS:
1. Unions DNS domains and observed hosts before suffix collapse
2. Collapses union to eTLD+1 suffixes (`AllowedDNSSuffixes`)
3. Removes hosts covered by those suffixes (`deduplicateHosts`)
4. Builds a complete `profile.SandboxProfile` scaffold with sensible defaults for all schema-required fields

`GenerateYAML` wraps `Generate` + `goccy/go-yaml.Marshal` with a header comment. Output passes `profile.Validate()` (full JSON schema + semantic validation).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed deadlock in Generate() from nested mutex acquisition**
- **Found during:** Task 2 test run
- **Issue:** `Generate()` acquired `r.mu.Lock()` then called `r.DNSDomains()` which also acquires the same mutex — deadlock
- **Fix:** Removed the outer lock; accessor methods (`DNSDomains`, `Hosts`, `Repos`) each acquire the mutex independently
- **Files modified:** pkg/allowlistgen/generator.go
- **Commit:** d84e32a

**2. [Rule 1 - Bug] Fixed schema/struct mismatch on LifecycleSpec.MaxLifetime**
- **Found during:** Task 2 — TestGenerateValidates failed with "additional properties 'maxLifetime' not allowed"
- **Issue:** `LifecycleSpec.MaxLifetime` had no `omitempty` so goccy/go-yaml emitted `maxLifetime: ""`, which fails JSON schema (schema has `additionalProperties: false` and doesn't list `maxLifetime`)
- **Fix:** Added `omitempty` to both yaml and json struct tags on `MaxLifetime`
- **Files modified:** pkg/profile/types.go
- **Commit:** d84e32a

**3. [Design alignment] Updated TestNormalizeSuffixes expected output for PSL accuracy**
- **Found during:** Task 1 — test expected `.githubusercontent.com` but `raw.githubusercontent.com` maps to `.raw.githubusercontent.com` (correct PSL behavior: `githubusercontent.com` is itself a registered PSL entry)
- **Fix:** Updated test expectation to `.raw.githubusercontent.com` — this aligns with user design direction to be conservative and keep more specific entries
- **Files modified:** pkg/allowlistgen/normalize_test.go
- **Commit:** add6c85

## Self-Check: PASSED

All created files exist. Both task commits verified (add6c85, d84e32a). All 22 tests pass with race detector.
