---
phase: 127-declarative-mitm-intercepts-profile-declared-host-to-action-
plan: 04
subsystem: profile-validation
tags: [go, validation, mitm, netpolicy, km-validate]

requires:
  - phase: 127-01
    provides: "spec.network.mitm.intercepts profile surface (types + schema), EnabledIntercepts/ProfileIntercepts/DuplicateInterceptNames"
provides:
  - "km validate semantic rules for spec.network.mitm.intercepts: five errors, three warnings"
  - "validateMITMIntercepts (unexported) inside pkg/profile/validate.go"
affects: []

tech-stack:
  added: []
  patterns:
    - "Belt-and-suspenders semantic checks mirroring Rule 2/Rule 4's convention: schema leaves hosts/action unenforced (disable-only override), semantic layer enforces them only against EnabledIntercepts"
    - "Local, hand-maintained mirror of a sidecar's host-matching semantics (prefix/suffix for bedrock-runtime, leading-dot/exact/wildcard for allowedDNSSuffixes) instead of importing the sidecar package, to avoid a pkg/profile -> sidecars import"

key-files:
  created: []
  modified:
    - pkg/profile/validate.go
    - pkg/profile/validate_mitm_test.go

key-decisions:
  - "Name and duplicate-name checks run against the FULL, uncollapsed ProfileIntercepts list (so a disabled entry's malformed name is still caught, and DuplicateInterceptNames has the same-file conflicts to find); hosts/action/redirect/status checks and all three warnings run only against EnabledIntercepts (collapsed, enabled-only) so a disable-only override (name + enabled:false) never trips them"
  - "Redirect validated with net/url.Parse (scheme must be http/https, Host must be non-empty) rather than a hand-rolled prefix check, per the plan's explicit instruction"
  - "Reserved-host list matches by exact string for Anthropic/OpenAI/GitHub and by prefix+suffix (with a non-empty-region guard) for bedrock-runtime, instead of importing sidecars/http-proxy/httpproxy's regexes — pkg/profile must not import a sidecar package; each entry's source file is commented in place"
  - "Denied-host overlap warning calls netpolicy.Match directly (the same matcher IsHostDenied wraps) against the union of deniedHosts/deniedDNSSuffixes, so km validate can never disagree with the runtime deny gate"
  - "DNS-reachability warning reimplements allowedDNSSuffixes' leading-dot/exact/wildcard semantics locally (hostCoveredByDNSSuffixes) rather than importing IsHostAllowed, for the same no-sidecar-import reason; fires under both enforcement: ebpf and enforcement: both, per the plan's explicit inclusion of both"

patterns-established: []

requirements-completed: [REQ-127-VALIDATE]

coverage:
  - id: D1
    description: "km validate rejects an enabled intercept with no hosts, with zero or two actions, with a redirect that is not an absolute http(s) URL, with a respond status outside 100-599, or with a malformed name — each fires with an actionable path, and a disabled intercept is exempt from the hosts/action/redirect/status checks"
    requirement: "REQ-127-VALIDATE"
    verification:
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_EmptyHosts_Error"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_NoAction_Error"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_BothRedirectAndRespond_Error"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_NeitherRedirectNorRespond_Error"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_Redirect_RelativePathRejected"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_Redirect_BareHostRejected"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_Redirect_NonHTTPSchemeRejected"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_Redirect_HTTPAndHTTPSAccepted"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_RespondStatus_ZeroIsError"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_RespondStatus_599IsAccepted"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_RespondStatus_ValidValuesAccepted"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_RespondStatus_OutOfRangeRejected"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_EmptyName_Error"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_MalformedName_Error"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_DisabledMalformedName_StillErrors"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_DisabledEntry_NoHostsNoAction_NoError"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_NoMITMBlock_NoError"
        status: pass
      - kind: integration
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_ThroughPublicEntryPoint"
        status: pass
      - kind: integration
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_ThroughPublicEntryPoint_RespondStatusOutOfRange"
        status: pass
    human_judgment: false
  - id: D2
    description: "Two entries sharing a name with conflicting bodies inside one file is a km validate error, while byte-identical repeats (and the same name recurring across an extends: DAG, which fromMap collapses before validate ever sees it) are not flagged"
    requirement: "REQ-127-VALIDATE"
    verification:
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_DuplicateNameConflictingBodies_Error"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_DuplicateNameIdenticalBodies_NoError"
        status: pass
    human_judgment: false
  - id: D3
    description: "km validate warns (never errors, never fails the exit code) when an intercept host overlaps a platform-reserved host (Anthropic/OpenAI/GitHub exact match, bedrock-runtime.<region>.amazonaws.com prefix/suffix match), when it also overlaps deniedHosts/deniedDNSSuffixes via the canonical netpolicy.Match matcher, or when it is not covered by allowedDNSSuffixes under enforcement: ebpf or both; a host covered by an allowedDNSSuffixes entry of '*' produces no DNS warning, the default proxy enforcement never produces a DNS warning, and a disabled intercept produces no warnings at all"
    requirement: "REQ-127-VALIDATE"
    verification:
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_ReservedHost_Anthropic_Warning"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_ReservedHost_OpenAI_Warning"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_ReservedHost_GitHub_Warning"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_ReservedHost_Bedrock_AnyRegion_Warning"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_ReservedHost_AllWarningsAreIsWarningTrue"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_DeniedHosts_Overlap_Warning"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_DeniedDNSSuffixes_Overlap_Warning"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_DNSReachability_EBPF_Warning"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_DNSReachability_Both_Warning"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_DNSReachability_DefaultProxyEnforcement_NoWarning"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_DNSReachability_CoveredHost_NoWarning"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_DNSReachability_WildcardAllowedDNSSuffixes_NoWarning"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_Warnings_NeverFailExitCode"
        status: pass
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_DisabledIntercept_NoWarnings"
        status: pass
      - kind: integration
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_ThroughPublicEntryPoint_ReservedHostWarning"
        status: pass
    human_judgment: false
  - id: D4
    description: "A profile with no mitm block produces no new errors and no new warnings — dormant by default, byte-identical km validate output to before this plan"
    verification:
      - kind: unit
        ref: "pkg/profile/validate_mitm_test.go#TestValidateMITM_NoMITMBlock_NoError"
        status: pass
      - kind: other
        ref: "go test ./pkg/profile/... -count=1 (exit 0, full package green including all pre-existing tests)"
        status: pass
    human_judgment: false

duration: 25min
completed: 2026-08-26
status: complete
---

# Phase 127 Plan 04: Declarative MITM intercepts — km validate errors and warnings Summary

**Added `validateMITMIntercepts` to `pkg/profile/validate.go`, giving `km validate` the five locked errors (empty hosts, missing/duplicate action, non-absolute-http(s) redirect, out-of-range respond status, malformed/duplicate name) and three locked warnings (platform-reserved host overlap, denied-host overlap via `netpolicy.Match`, DNS-unreachable under `enforcement: ebpf`/`both`) for `spec.network.mitm.intercepts` — dormant when a profile declares no `mitm:` block.**

## Performance

- **Started:** 2026-08-26T06:00:00Z (approx, first read)
- **Completed:** 2026-08-26T06:22:12Z
- **Duration:** ~25 min of tool time
- **Tasks:** 2
- **Files modified:** 2 (`pkg/profile/validate.go`, `pkg/profile/validate_mitm_test.go` — both pre-existing plan-declared targets, no new files)

## Accomplishments

- `validateMITMIntercepts(p *SandboxProfile) []ValidationError` wired into `ValidateSemantic`, reachable from both `km validate` (via `Validate(raw)` → `ValidateSemantic`) and `km create`'s validation path.
- Name and duplicate-name checks run against the full, uncollapsed `ProfileIntercepts(p)` list (a malformed name is caught whether or not the rule is enabled; `DuplicateInterceptNames` flags same-file conflicting duplicates without false-positiving on the legal extends-override path, since `fromMap` has already collapsed any cross-file duplicate before `ValidateSemantic` ever runs).
- Hosts/action/redirect/status checks and all three warnings run only against `EnabledIntercepts(all)` — a disable-only override entry (`name` + `enabled: false`) is exempt from every one of them, which is what makes the phase's headline off-switch usable.
- Redirect validation uses `net/url.Parse`: scheme must be `http` or `https`, host must be non-empty — rejects relative paths, bare hosts, and non-http schemes; accepts both `http://` and `https://` absolute URLs.
- Respond-status validation: `status < 100 || status > 599` is an error, so an absent status (Go zero value `0`) is caught along with genuinely out-of-range values; 100/301/503/599 all validate clean.
- Platform-reserved overlap warning: an ordered `mitmReservedHosts` list (`api.anthropic.com`, `api.openai.com`, the four GitHub hosts) plus a `isBedrockRuntimeHost` prefix/suffix matcher for `bedrock-runtime.<region>.amazonaws.com` — each entry comments the sidecar file it mirrors (`sidecars/http-proxy/httpproxy/{proxy,openai,github}.go`) so a future regex change there has a visible counterpart here. No sidecar import; pkg/profile must not depend on sidecars/http-proxy.
- Denied-host overlap warning: calls `netpolicy.Match(host, union(deniedHosts, deniedDNSSuffixes))` directly — the same matcher `IsHostDenied` wraps at runtime — so `km validate` can never disagree with the deny gate that actually runs.
- DNS-reachability warning: a local `hostCoveredByDNSSuffixes` helper reimplements `allowedDNSSuffixes`' leading-dot/exact/`*`-wildcard semantics (mirroring `IsHostAllowed` without importing it), fires under `enforcement: ebpf` and `enforcement: both` (the enforcer's own resolver serves DNS in both modes, per Phase 125's userdata evidence), never under default `proxy` enforcement.
- All three warnings carry `IsWarning: true` and are computed per-intercept from its declared `Hosts`, so a rule that overlaps multiple concerns (e.g. both reserved AND denied) gets one warning per concern, each naming the offending host(s).
- 37 new tests in `pkg/profile/validate_mitm_test.go` covering every bullet in both tasks' `<behavior>` blocks, plus three tests driving an error/warning through the public `profile.Validate(raw)` entry point on YAML bytes (not only `ValidateSemantic`).

## Task Commits

1. **Task 1: The five intercept validation errors** - `a4a8e99d` (feat)
2. **Task 2: The three intercept warnings — platform-reserved, denied, and DNS-unreachable** - `bbde6278` (feat)

**Plan metadata:** (this commit, docs)

## Files Created/Modified

- `pkg/profile/validate.go` - `validateMITMIntercepts`, `mitmNamePattern`, `mitmReservedHosts`, `isBedrockRuntimeHost`, `isMITMReservedHost`, `mitmBareHost`, `hostCoveredByDNSSuffixes`, `mitmPath`; wired into `ValidateSemantic`; new imports `net/url` and `github.com/whereiskurt/klanker-maker/pkg/netpolicy`
- `pkg/profile/validate_mitm_test.go` - 37 tests (package `profile_test`, matching the existing `validate_hibernation_test.go`/`egress_deny_test.go` convention) covering every locked error and warning, disable-only exemption, no-mitm-block dormancy, and two public-entry-point cases

## Decisions Made

See `key-decisions` in the frontmatter. In summary: split the enforcement scope exactly as the plan specified (name/duplicate checks against the full list, hosts/action/redirect/status/warnings against `EnabledIntercepts` only); validated redirects with `net/url` rather than hand-rolling; mirrored sidecar host-matching semantics locally in two places (bedrock prefix/suffix, DNS-suffix leading-dot/exact/wildcard) rather than importing `sidecars/http-proxy/httpproxy`, per the plan's explicit prohibition on new cross-package coupling; used `netpolicy.Match` directly (already imported package, zero-cycle) for the denied-host warning as instructed.

## Deviations from Plan

None — plan executed exactly as written. Both tasks' `<behavior>` bullets, `<action>` instructions, and `<acceptance_criteria>` greps were satisfied without needing a Rule 1/2/3/4 fix.

**Process note (not a deviation):** the two tasks' code and tests were authored together during implementation (both live in the same function and the same test file), then mechanically split back into two commits — a task-1-only intermediate state (errors only, warnings code and imports removed) was built, tested, and committed first; the warnings code was then restored on top and committed second. Both intermediate and final states were `go build ./...` clean and `go test ./pkg/profile/... -count=1` green before their respective commits, so the two commits are genuinely bisectable, not just cosmetically split.

---

**Total deviations:** 0
**Impact on plan:** None.

## Issues Encountered

None. The one non-obvious detail worth flagging for future readers: `bedrock-runtime.amazonaws.com` (no region segment) deliberately does NOT match `isBedrockRuntimeHost` — the prefix/suffix check requires `len(host) >= len(prefix)+len(suffix)` so the two literals can't overlap, matching the original `bedrockHostRegex`'s `.+` requirement between `bedrock-runtime.` and `.amazonaws.com`. Not exercised by a negative test (not called out in the plan's behavior list), but verified by hand during implementation.

## User Setup Required

None — no external service configuration required. This plan is code-only (Go validation rules and tests); no `km init`, `km create`, or AWS-touching command was run, per the plan's constraints.

## Next Phase Readiness

- `km validate` now fully enforces the Phase 127 design's locked validation contract for `spec.network.mitm.intercepts`. Combined with plans 01-03, the feature is validate → profile → compiler → sidecar complete.
- No blockers. `go test ./pkg/profile/... -count=1` and `go build ./...` are both green. Full-repo `go test ./... -count=1` shows only the 5 known pre-existing failures in `internal/app/cmd` (fast-fail AWS credential seam), unrelated to this plan.
- Remaining phase 127 surface (per the phase's stated scope): `profiles/base/mitm-rickroll.yaml` abstract fragment, `docs/mitm-intercepts.md`, a CLAUDE.md "Where to look" row, and the 6-step live UAT — none of which this plan owned.
- No file under `pkg/compiler/`, `sidecars/`, `infra/`, or `internal/app/cmd/` was touched, per this plan's prohibitions — verified via `git status --short` before each commit.

---
*Phase: 127-declarative-mitm-intercepts-profile-declared-host-to-action-*
*Completed: 2026-08-26*

## Self-Check: PASSED

Verified both modified files exist on disk (`pkg/profile/validate.go`, `pkg/profile/validate_mitm_test.go`) and both task commit hashes (`a4a8e99d`, `bbde6278`) are present in `git log --oneline`.
