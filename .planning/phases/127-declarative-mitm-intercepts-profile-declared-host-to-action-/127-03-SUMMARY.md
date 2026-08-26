---
phase: 127-declarative-mitm-intercepts-profile-declared-host-to-action-
plan: 03
subsystem: compiler
tags: [go, userdata, systemd, mitm, http-proxy, ebpf, compiler-sidecar-contract]

requires:
  - phase: 127-01
    provides: "spec.network.mitm.intercepts profile surface (types + schema), profile.EnabledIntercepts/ProfileIntercepts"
  - phase: 127-02
    provides: "KM_MITM_INTERCEPTS wire contract (sidecars/http-proxy/httpproxy/intercept.go: Intercept/Respond/ParseIntercepts/MatchesHost)"
provides:
  - "userDataParams.MITMInterceptsB64 + buildMITMInterceptsB64 (compiler-side flat-wire-shape marshaller)"
  - "Guarded 7.3c mitm.conf systemd drop-in template section"
  - "buildL7ProxyHosts intercept-host threading (--proxy-hosts) without widening --allowed-dns"
  - "Compiler-to-sidecar wire contract test (real user-data decoded by the real httpproxy.ParseIntercepts)"
affects: [127-04, 127-05]

tech-stack:
  added: []
  patterns:
    - "Local wire-shape mirror struct (mitmWireIntercept/mitmWireRespond) defined in the compiler package rather than importing the sidecar package into non-test code, kept honest by a test-only import + contract test — same shape as the profile/sidecar split already established in plans 01/02"
    - "Empty-string-is-dormant field on userDataParams (MITMInterceptsB64), mirroring the existing ActionLimitsJSON precedent exactly: same guard style, same heredoc/drop-in mechanics, same byte-identity discipline"

key-files:
  created:
    - pkg/compiler/userdata_mitm_test.go
  modified:
    - pkg/compiler/userdata.go

key-decisions:
  - "Wire structs (mitmWireIntercept/mitmWireRespond) are a hand-maintained local mirror of sidecars/http-proxy/httpproxy/intercept.go's Intercept/Respond, not an import — matches the plan's explicit instruction to avoid importing the sidecar package into non-test compiler code; a contract test (TestMITMWireContract_CompilerAndSidecarAgree) is the thing that keeps the two structurally honest"
  - "buildL7ProxyHosts was refactored to use a local seen-map + add() closure instead of repeated append() calls, so the new de-duplication requirement (a duplicate host, e.g. an intercept declaring api.anthropic.com on a Bedrock-enabled profile, must not appear twice) could be satisfied without special-casing each existing group"
  - "TestMITMIntercept_DoesNotWidenAllowedDNS sets spec.network.enforcement: ebpf explicitly on both compared profiles, because --allowed-dns is only rendered inside the km ebpf-attach ExecStart block, itself guarded on enforcement: ebpf|both — the default enforcement (proxy) never emits that flag at all"

patterns-established:
  - "userdata_mitm_test.go's mitmProfileWithIntercepts(ics ...profile.MITMIntercept) helper is now the canonical way future compiler tests should construct a profile carrying spec.network.mitm.intercepts"

requirements-completed: [REQ-127-ENV, REQ-127-EBPF]

coverage:
  - id: D1
    description: "A profile with an enabled intercept renders KM_MITM_INTERCEPTS inside a conditional mitm.conf systemd drop-in, base64-decoding to a flat JSON element with no nested action key and no enabled key; a respond body with a space and an embedded newline survives byte for byte"
    requirement: "REQ-127-ENV"
    verification:
      - kind: unit
        ref: "pkg/compiler/userdata_mitm_test.go#TestMITMInterceptsEmission_RedirectRule"
        status: pass
      - kind: unit
        ref: "pkg/compiler/userdata_mitm_test.go#TestMITMInterceptsEmission_RespondBodyWithSpaceAndNewlineSurvives"
        status: pass
    human_judgment: false
  - id: D2
    description: "A profile with no mitm block, or whose only intercept is disabled, renders neither KM_MITM_INTERCEPTS nor mitm.conf; a mix of one enabled + one disabled intercept emits exactly the enabled one; both frozen byte-identity goldens (Phase92 learn.v2, H1) remain untouched"
    requirement: "REQ-127-ENV"
    verification:
      - kind: unit
        ref: "pkg/compiler/userdata_mitm_test.go#TestMITMInterceptsDormant_NoMITMBlock"
        status: pass
      - kind: unit
        ref: "pkg/compiler/userdata_mitm_test.go#TestMITMInterceptsDormant_OnlyDisabledIntercept"
        status: pass
      - kind: unit
        ref: "pkg/compiler/userdata_mitm_test.go#TestMITMInterceptsEmission_OneEnabledOneDisabled"
        status: pass
      - kind: unit
        ref: "pkg/compiler/userdata_mitm_test.go#TestMITMInterceptsByteIdentityGoldensStillPass"
        status: pass
      - kind: unit
        ref: "pkg/compiler/userdata_phase92_byte_identity_test.go#TestUserdataLearnV2Phase92ByteIdentity"
        status: pass
      - kind: unit
        ref: "pkg/compiler/userdata_h1_byte_identity_test.go#TestUserdataH1ByteIdentity"
        status: pass
      - kind: other
        ref: "git status --porcelain pkg/compiler/testdata/ (empty — no golden touched)"
        status: pass
    human_judgment: false
  - id: D3
    description: "Enabled intercept hosts reach --proxy-hosts (buildL7ProxyHosts), appended after the existing GitHub/Bedrock-Anthropic/OpenAI order, de-duplicated against hosts already present, while a disabled intercept's hosts are absent and --allowed-dns is provably unchanged by declaring an intercept"
    requirement: "REQ-127-EBPF"
    verification:
      - kind: unit
        ref: "pkg/compiler/userdata_mitm_test.go#TestBuildL7ProxyHosts_IntersectHostPresent"
        status: pass
      - kind: unit
        ref: "pkg/compiler/userdata_mitm_test.go#TestBuildL7ProxyHosts_DisabledInterceptHostAbsent"
        status: pass
      - kind: unit
        ref: "pkg/compiler/userdata_mitm_test.go#TestBuildL7ProxyHosts_OrderIsGithubBedrockOpenAIThenIntercepts"
        status: pass
      - kind: unit
        ref: "pkg/compiler/userdata_mitm_test.go#TestBuildL7ProxyHosts_DuplicateHostNotEmittedTwice"
        status: pass
      - kind: unit
        ref: "pkg/compiler/userdata_mitm_test.go#TestMITMIntercept_DoesNotWidenAllowedDNS"
        status: pass
    human_judgment: false
  - id: D4
    description: "A single contract test renders real compiler user-data for a profile with one redirect and one respond rule, decodes the emitted KM_MITM_INTERCEPTS value with the REAL sidecar parser (httpproxy.ParseIntercepts), and asserts field-by-field equality plus httpproxy.MatchesHost success for each declared host (including a leading-dot subdomain match) — proving the two independently-authored wire ends agree"
    requirement: "REQ-127-ENV"
    verification:
      - kind: integration
        ref: "pkg/compiler/userdata_mitm_test.go#TestMITMWireContract_CompilerAndSidecarAgree"
        status: pass
      - kind: other
        ref: "go build ./... (exit 0 — confirms no non-test import of the sidecar package was introduced)"
        status: pass
    human_judgment: false

duration: 22min
completed: 2026-08-26
status: complete
---

# Phase 127 Plan 03: Declarative MITM intercepts — compiler drop-in Summary

**The compiler now emits a conditional `mitm.conf` systemd drop-in carrying base64-encoded JSON of a profile's enabled MITM intercepts (`KM_MITM_INTERCEPTS`), threads those hosts into `--proxy-hosts` for eBPF/both enforcement, and a contract test proves the emitted value decodes with the sidecar's own parser into exactly what the profile declared — while both frozen byte-identity goldens stay untouched.**

## Performance

- **Started:** 2026-08-26T01:52:00-04:00 (approx, first file read)
- **Completed:** 2026-08-26T02:14:00-04:00 (approx)
- **Duration:** ~22 min of tool time
- **Tasks:** 3
- **Files modified:** 2 (1 modified: `pkg/compiler/userdata.go`; 1 created: `pkg/compiler/userdata_mitm_test.go`)

## Accomplishments

- `userDataParams.MITMInterceptsB64 string` — empty means dormant, mirroring the existing `ActionLimitsJSON` precedent exactly.
- `buildMITMInterceptsB64(p *profile.SandboxProfile) string` — calls `profile.EnabledIntercepts(profile.ProfileIntercepts(p))` (the same by-name collapse and disabled-rule drop plan 01 shipped), maps each surviving entry onto a local `mitmWireIntercept`/`mitmWireRespond` flat wire shape (no `action` nesting, no `enabled` key), `json.Marshal`s, and base64-encodes. Returns `""` on an empty result or a marshal error — dormant is the safe direction.
- New guarded template section 7.3c (`{{- if .MITMInterceptsB64 }}`), copied structurally from the Phase 121 `KM_ACTION_LIMITS` drop-in (7.3b): `mkdir -p`, a single-quoted heredoc writing `/etc/systemd/system/km-http-proxy.service.d/mitm.conf` with `Environment=KM_MITM_INTERCEPTS=<base64>`, `systemctl daemon-reload` + `systemctl restart km-http-proxy`, and a bootstrap echo. Both frozen byte-identity goldens (`TestUserdataLearnV2Phase92ByteIdentity`, `TestUserdataH1ByteIdentity`) were verified untouched — no golden file needed regeneration.
- `buildL7ProxyHosts` extended to append every enabled intercept's hosts last, after the existing GitHub / Bedrock-Anthropic / OpenAI groups, refactored internally to a `seen`-map + `add()` closure so the new de-duplication requirement (an intercept re-declaring an already-present host, e.g. `api.anthropic.com` on a Bedrock profile) doesn't emit a duplicate. A code comment documents the deliberate non-widening of `allowedDNSSuffixes`.
- `pkg/compiler/userdata_mitm_test.go` — 15 tests covering emission, dormancy (no mitm block / only-disabled intercept), mixed enabled+disabled collapse, `--proxy-hosts` threading (presence, disabled-absence, order, de-dup), `--allowed-dns` non-widening, and the compiler-to-sidecar wire contract test that decodes real rendered user-data with the real `httpproxy.ParseIntercepts` + `httpproxy.MatchesHost`.

## Task Commits

1. **Task 1: Emit the conditional mitm.conf drop-in with base64 JSON** - `f8435a8a` (feat)
2. **Task 2: Thread intercept hosts into buildL7ProxyHosts** - `6b10fb5b` (feat)
3. **Task 3: Compiler-to-sidecar contract test** - `6ac52899` (test)

**Plan metadata:** (this commit, docs)

## Files Created/Modified

- `pkg/compiler/userdata.go` - `MITMInterceptsB64` field, `buildMITMInterceptsB64` + local `mitmWireIntercept`/`mitmWireRespond` wire structs, the guarded 7.3c template section, `buildL7ProxyHosts` intercept-host threading + de-duplication, the `userDataParams` literal assignment, `encoding/base64` import
- `pkg/compiler/userdata_mitm_test.go` - all 15 tests for this plan's three tasks, plus `mitmProfileWithIntercepts`, `extractEnvValue`, `extractQuotedFlagValue` test helpers

## Decisions Made

- Kept the wire structs as a hand-maintained local mirror rather than importing `sidecars/http-proxy/httpproxy` into non-test compiler code, per the plan's explicit instruction — the contract test in Task 3 is the thing that keeps the two ends honest, not the type system.
- Refactored `buildL7ProxyHosts` to use a `seen`-map + `add()` closure instead of bare `append()` calls, since the plan's own acceptance criteria required "a duplicate host already present from another group is not emitted twice" and the existing code had no de-duplication machinery at all.
- Set `spec.network.enforcement: ebpf` explicitly in `TestMITMIntercept_DoesNotWidenAllowedDNS` on both compared profiles, since `--allowed-dns` only renders inside the `km ebpf-attach` `ExecStart` block (itself guarded on `enforcement: ebpf|both`) — under the default `proxy` enforcement the flag is never emitted at all, so the comparison would otherwise be vacuous.

## Deviations from Plan

### Auto-fixed Issues

None — no bugs, missing functionality, or blocking issues required a Rule 1/2/3 fix. The implementation followed the plan's `<action>` blocks directly.

**Note (not a deviation, an acceptance-criteria imprecision, same category as plan 01's `"block"` grep note):** Task 1's acceptance criteria states `grep -c 'KM_MITM_INTERCEPTS' pkg/compiler/userdata.go` should return `1`. The actual count is `4` — one is the functional `Environment=KM_MITM_INTERCEPTS={{ .MITMInterceptsB64 }}` template line (the intended emission point, unique), and the other three are doc comments (the 7.3c section header comment, the `MITMInterceptsB64` field doc comment, and the `buildMITMInterceptsB64` function doc comment) that reference the env var name for readability. Verified manually that the emission itself is singular and correctly guarded; the extra occurrences are documentation, not duplicate emission logic. No action taken.

---

**Total deviations:** 0 auto-fixed
**Impact on plan:** None. Plan executed exactly as written; the one acceptance-criteria note above is documented for the next reader's benefit, not a code change.

## Issues Encountered

None. The one iteration needed was discovering, while writing `TestMITMIntercept_DoesNotWidenAllowedDNS`, that `--allowed-dns` is only rendered under `enforcement: ebpf|both` — resolved by setting `spec.network.enforcement: ebpf` on the test's profiles rather than treating it as a bug (the flag's conditional rendering is pre-existing, unrelated behavior this plan does not own).

## User Setup Required

None — no external service configuration required. This plan is code-only (Go compiler changes and tests); no `km init`, `km create`, or AWS-touching command was run, per the plan's constraints.

## Next Phase Readiness

- `KM_MITM_INTERCEPTS` now flows end to end: profile (`plan 01`) → compiler (`this plan`) → sidecar (`plan 02`), with a contract test pinning the wire shape between the compiler and sidecar ends.
- No blockers. `go test ./pkg/compiler/... -count=1` and `go build ./...` are both green. Full-repo `go test ./... -count=1` shows only the 5 known pre-existing failures in `internal/app/cmd` (fast-fail AWS credential seam — `TestBootstrapSCPApplyPath`, `TestBootstrapSCPSkipped_OrganizationBlank`, `TestClusterAdd`, `TestClusterRm`, `TestClusterAddPersistFailure`), unrelated to this plan.
- Plan 04 (validation) can now rely on this plan's `buildL7ProxyHosts` comment as the authoritative statement of the "declaring an intercept does not widen DNS" contract when writing the `km validate` WARN for an intercept host not covered by `allowedDNSSuffixes` under `enforcement: ebpf`.
- Plan 04/05 should note: `buildMITMInterceptsB64` and `buildL7ProxyHosts` both call `profile.EnabledIntercepts(profile.ProfileIntercepts(p))` independently — any future refactor that changes intercept resolution must update both call sites (and the tests covering each) in lockstep.

---
*Phase: 127-declarative-mitm-intercepts-profile-declared-host-to-action-*
*Completed: 2026-08-26*

## Self-Check: PASSED

All created/modified files verified present on disk (pkg/compiler/userdata.go, pkg/compiler/userdata_mitm_test.go, this SUMMARY.md). All three task commit hashes (f8435a8a, 6b10fb5b, 6ac52899) verified present in git log.
