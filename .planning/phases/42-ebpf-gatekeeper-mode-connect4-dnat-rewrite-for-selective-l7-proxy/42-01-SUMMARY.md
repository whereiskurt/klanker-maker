---
phase: 42-ebpf-gatekeeper-mode-connect4-dnat-rewrite-for-selective-l7-proxy
plan: 01
subsystem: ebpf
tags: [ebpf, bpf, connect4, sendmsg4, gatekeeper, proxy-pid, l7-proxy, cgroup]

# Dependency graph
requires:
  - phase: 40-ebpf-network-enforcement
    provides: BPF connect4/sendmsg4 programs, enforcer.go, Config struct, bpf.c

provides:
  - Dual-PID exemption in BPF hooks (enforcer PID + HTTP proxy PID)
  - const_http_proxy_pid volatile constant in common.h and generated Go bindings
  - HTTPProxyPID field in Config struct with enforcer wiring
  - --proxy-pid CLI flag on km ebpf-attach with block-mode warning
  - buildL7ProxyHosts() deriving domain suffixes from profile GitHub/Bedrock fields
  - L7ProxyHosts template field fixing --proxy-hosts to pass domain suffixes (not repo names)
  - Unit tests for buildL7ProxyHosts (4 cases) and TestProxyHosts in resolver

affects:
  - 42-02 (will use --proxy-pid flag and L7ProxyHosts for connect4 DNAT rewrite setup)
  - pkg/compiler (userdata template now passes domain suffixes via L7ProxyHosts)
  - pkg/ebpf (enforcer wires second volatile constant)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Dual-PID volatile const exemption pattern: const_proxy_pid (enforcer) + const_http_proxy_pid (HTTP proxy)"
    - "L7 domain derivation from profile fields: GitHub -> 4 domains, Bedrock -> .amazonaws.com + api.anthropic.com"
    - "buildL7ProxyHosts() function separate from joinGitHubAllowedRepos() — correct separation of concerns"

key-files:
  created:
    - pkg/ebpf/enforcer_test.go (new tests for HTTPProxyPID field)
  modified:
    - pkg/ebpf/headers/common.h (const_http_proxy_pid volatile const)
    - pkg/ebpf/bpf.c (dual-PID exemption in connect4 and sendmsg4)
    - pkg/ebpf/types.go (HTTPProxyPID field in Config struct)
    - pkg/ebpf/enforcer.go (wires const_http_proxy_pid from Config.HTTPProxyPID)
    - pkg/ebpf/bpf_x86_bpfel.go (regenerated with ConstHttpProxyPid variable)
    - pkg/ebpf/bpf_x86_bpfel.o (regenerated binary)
    - internal/app/cmd/ebpf_attach.go (--proxy-pid flag, httpProxyPID in Config)
    - pkg/compiler/userdata.go (L7ProxyHosts field, buildL7ProxyHosts, template fix)
    - pkg/compiler/userdata_test.go (4 TestL7ProxyHosts* tests)
    - pkg/ebpf/resolver/resolver_test.go (TestProxyHosts + TestProxyHostsMockUpdater)

key-decisions:
  - "PID-file approach for HTTP proxy exemption (not UID): simple, acceptable for Phase 42; stale-PID limitation documented (proxy restart without enforcer restart breaks exemption until enforcer restarts)"
  - "buildL7ProxyHosts derives domain suffixes from profile fields (GitHub + Bedrock) — NOT from GitHub repo names which was the prior (wrong) behavior"
  - ".amazonaws.com prefix used for Bedrock endpoints (broader than strictly necessary but non-Bedrock traffic passes through proxy without MITM inspection)"
  - "TestProxyHosts in resolver package tests suffix matching via NewAllowlist (same algorithm as isProxyHost which is unexported)"

patterns-established:
  - "Dual volatile const PID exemption: add const_X_pid to common.h, check in BPF hook, wire in enforcer.go, expose as Config.HTTPProxyPID"
  - "L7 proxy domain derivation: buildL7ProxyHosts() returns comma-separated domain suffixes from profile fields for use in --proxy-hosts flag"

requirements-completed: [EBPF-NET-03, EBPF-NET-09]

# Metrics
duration: 8min
completed: 2026-04-03
---

# Phase 42 Plan 01: BPF Dual-PID Exemption and L7ProxyHosts Derivation Summary

**BPF const_http_proxy_pid volatile constant added for dual-PID gatekeeper exemption, with buildL7ProxyHosts() deriving correct domain suffix list from profile GitHub/Bedrock fields**

## Performance

- **Duration:** 8 min
- **Started:** 2026-04-03T03:57:14Z
- **Completed:** 2026-04-03T04:05:33Z
- **Tasks:** 2
- **Files modified:** 10

## Accomplishments
- Dual-PID exemption in BPF connect4 and sendmsg4 hooks prevents HTTP proxy from being redirected to itself in block mode (infinite loop fix)
- Regenerated Go BPF bindings (bpf_x86_bpfel.go/.o) include ConstHttpProxyPid variable, wired in enforcer.go
- --proxy-pid flag added to km ebpf-attach with block-mode warning when unset
- buildL7ProxyHosts() correctly derives domain suffixes (not repo names) from profile GitHub/Bedrock config
- Template --proxy-hosts fixed to use L7ProxyHosts field instead of GitHubAllowedRepos
- 6 new unit tests across compiler and resolver packages

## Task Commits

Each task was committed atomically:

1. **Task 1: BPF dual-PID exemption + enforcer wiring + --proxy-pid flag** - `f80650c` (feat)
2. **Task 2: L7ProxyHosts derivation helper + template --proxy-hosts fix + unit tests** - `a82bd3f` (feat)

## Files Created/Modified
- `pkg/ebpf/headers/common.h` - Added volatile const_http_proxy_pid
- `pkg/ebpf/bpf.c` - Dual-PID exemption in connect4 and sendmsg4 hooks
- `pkg/ebpf/types.go` - HTTPProxyPID uint32 field in Config struct
- `pkg/ebpf/enforcer.go` - Wires const_http_proxy_pid from cfg.HTTPProxyPID
- `pkg/ebpf/bpf_x86_bpfel.go` - Regenerated with ConstHttpProxyPid variable
- `pkg/ebpf/bpf_x86_bpfel.o` - Regenerated binary object
- `pkg/ebpf/enforcer_test.go` - 3 tests for HTTPProxyPID Config field
- `internal/app/cmd/ebpf_attach.go` - --proxy-pid flag, httpProxyPID in Config, block-mode warning
- `pkg/compiler/userdata.go` - L7ProxyHosts field, buildL7ProxyHosts(), template fix
- `pkg/compiler/userdata_test.go` - 4 TestL7ProxyHosts* tests
- `pkg/ebpf/resolver/resolver_test.go` - TestProxyHosts (8 subtests) + TestProxyHostsMockUpdater

## Decisions Made
- PID-file approach for HTTP proxy exemption (not UID-based): simpler, acceptable for Phase 42. Known limitation: if HTTP proxy restarts (new PID), exemption becomes stale until enforcer also restarts. Documented in plan.
- buildL7ProxyHosts derives from profile fields (sourceAccess.github + useBedrock), not from GitHub repo names which was the prior incorrect behavior
- .amazonaws.com suffix for Bedrock (broader than ideal but non-Bedrock traffic passes transparently)

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
- None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Plan 42-02 can now use --proxy-pid flag to pass the HTTP proxy PID for exemption
- Template passes correct domain suffixes via L7ProxyHosts for --proxy-hosts
- BPF programs correctly exempt both enforcer and HTTP proxy from connect4/sendmsg4 interception

---
*Phase: 42-ebpf-gatekeeper-mode-connect4-dnat-rewrite-for-selective-l7-proxy*
*Completed: 2026-04-03*
