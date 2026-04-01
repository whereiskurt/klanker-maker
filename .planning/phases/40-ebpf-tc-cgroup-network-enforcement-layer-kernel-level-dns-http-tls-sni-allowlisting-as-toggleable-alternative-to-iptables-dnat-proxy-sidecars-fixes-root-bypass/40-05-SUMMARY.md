---
phase: 40-ebpf-network-enforcement
plan: 05
subsystem: network-enforcement
tags: [ebpf, cgroup, iptables, profile-schema, compiler, user-data, enforcement-mode]

# Dependency graph
requires:
  - phase: 40-02
    provides: eBPF enforcer lifecycle (ebpf-attach command) that user-data invokes
  - phase: 40-03
    provides: DNS resolver daemon architecture that eBPF cgroup enforcement coordinates with
  - phase: 40-04
    provides: TC SNI classifier patterns that informed eBPF enforcement bootstrap
provides:
  - NetworkSpec.Enforcement field (proxy/ebpf/both) in profile types
  - JSON schema enforcement enum with proxy default
  - Semantic validation for enforcement values and substrate compatibility
  - Conditional user-data generation: iptables DNAT for proxy/both, eBPF cgroup for ebpf/both
  - km ebpf-attach invocation in EC2 user-data bootstrap
  - Docker compose eBPF warning (proxy always used on Docker in Phase 40)
affects: [40-06, compiler, profile-validation, km-create, km-validate]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Conditional template sections: {{- if or (eq .Enforcement X) (eq .Enforcement Y) }} pattern for multi-mode user-data"
    - "Belt-and-suspenders validation: JSON schema enum + semantic validation both check enforcement values"
    - "Substrate scope boundary: eBPF is EC2-only, enforced by compile-time zerolog warning for Docker"

key-files:
  created: []
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/validate.go
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go
    - pkg/compiler/compose.go

key-decisions:
  - "Default enforcement is 'proxy' (omitted field) for backwards compatibility — no existing profiles need updating"
  - "eBPF enforcement scoped to EC2 only in Phase 40 — Docker always gets proxy enforcement with explicit zerolog warning"
  - "Semantic validation produces errors (not just warnings) for eBPF on non-EC2 substrates to prevent silent misconfiguration"
  - "Tests check for 'iptables -t nat' and 'export HTTP_PROXY' (actual commands) not section comment strings to avoid false positives"

patterns-established:
  - "Enforcement mode: profile field drives compiler output; empty string always means proxy at compile time"
  - "Docker scope boundary: explicitly log and override eBPF requests rather than silently ignore"

requirements-completed:
  - EBPF-NET-09
  - EBPF-NET-11

# Metrics
duration: 5min
completed: 2026-03-31
---

# Phase 40 Plan 05: Profile enforcement field + conditional eBPF/iptables user-data generation Summary

**Profile `spec.network.enforcement` field (proxy/ebpf/both) added with compiler emitting conditional EC2 user-data: iptables DNAT for proxy mode, cgroup BPF attach + km.slice cgroup creation for eBPF mode**

## Performance

- **Duration:** ~5 min
- **Started:** 2026-03-31T07:07:59Z
- **Completed:** 2026-03-31T07:12:41Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments

- Added `Enforcement` field to `NetworkSpec` (proxy/ebpf/both) with full JSON schema enum and semantic validation
- Compiler generates conditional user-data: iptables DNAT section for `proxy`/`both`, eBPF cgroup section for `ebpf`/`both`
- eBPF section creates `km.slice/km-{id}.scope` cgroup, invokes `km ebpf-attach`, configures `/etc/profile.d/km-cgroup.sh` to move sandbox user into cgroup
- Docker compose path explicitly warns via zerolog and always forces proxy enforcement (eBPF EC2-only in Phase 40)
- Four new tests cover all enforcement modes (default, proxy, ebpf, both)

## Task Commits

1. **Task 1: Add enforcement field to profile types, JSON schema, and validation** - `af6c290` (feat)
2. **Task 2: Update compiler to emit conditional eBPF vs iptables user-data** - `daebe88` (feat)

**Deviation fix:** `80f5dea` (chore: go mod tidy for pre-existing go.sum drift)

## Files Created/Modified

- `pkg/profile/types.go` - Added `Enforcement string` field to `NetworkSpec` with comment
- `pkg/profile/schemas/sandbox_profile.schema.json` - Added `enforcement` enum property to network object
- `pkg/profile/validate.go` - Rules 4+5: validate enforcement values and warn on non-EC2 eBPF usage
- `pkg/compiler/userdata.go` - `Enforcement` in `userDataParams`; conditional iptables/eBPF template sections
- `pkg/compiler/userdata_test.go` - Four enforcement mode tests (Default/Proxy/Ebpf/Both)
- `pkg/compiler/compose.go` - Added zerolog import; Docker eBPF warning in `generateDockerCompose`

## Decisions Made

- Default is `proxy` (omitted field equals proxy) — no existing profiles need changes
- eBPF on Docker/ECS produces semantic validation errors (strong signal) not just warnings
- Tests use `iptables -t nat` and `export HTTP_PROXY` patterns rather than section comment text to avoid false matches

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Fixed pre-existing go.sum drift breaking make build**
- **Found during:** Task 2 verification (make build)
- **Issue:** go.sum was stale; `make build` failed with "missing go.sum entry for github.com/spf13/cobra"
- **Fix:** Ran `go mod tidy` to refresh go.sum
- **Files modified:** go.mod, go.sum
- **Verification:** `make build` succeeds (Built: km v0.0.89)
- **Committed in:** `80f5dea` (chore: separate commit)

---

**Total deviations:** 1 auto-fixed (Rule 3 - blocking)
**Impact on plan:** go.sum fix was necessary for build verification. No scope creep.

## Issues Encountered

- Test `TestUserDataEnforcementEbpf` initially failed because string `"iptables DNAT"` appears in non-conditional comments (sidecar user creation comment on line 256, and the ebpf-only message itself). Fixed by testing for `"iptables -t nat"` (actual iptables command) and `"export HTTP_PROXY"` (actual env var export) instead of comment strings.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Profile schema and compiler fully support enforcement modes
- `km validate` accepts and rejects enforcement values correctly
- `km create` with `enforcement: ebpf` will emit cgroup bootstrap in EC2 user-data
- Phase 40-06 or integration tests can verify the `km ebpf-attach` flow end-to-end
- Built-in profiles unchanged; existing sandboxes unaffected (proxy is default)

---
*Phase: 40-ebpf-network-enforcement*
*Completed: 2026-03-31*
