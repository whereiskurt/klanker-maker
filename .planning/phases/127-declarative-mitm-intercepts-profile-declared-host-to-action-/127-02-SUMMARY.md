---
phase: 127-declarative-mitm-intercepts-profile-declared-host-to-action-
plan: 02
subsystem: infra
tags: [go, goproxy, mitm, http-proxy, sidecar, network-enforcement]

requires:
  - phase: 127-01
    provides: "spec.network.mitm.intercepts profile surface (types + schema) — plan 03 wires the compiler drop-in between this plan and plan 01"
provides:
  - "Generic, env-driven MITM intercept engine in the http-proxy sidecar (KM_MITM_INTERCEPTS)"
  - "MatchesHost/MatchIntercept/ParseIntercepts/WithIntercepts/InterceptResponse/registerInterceptHandlers in sidecars/http-proxy/httpproxy/intercept.go"
  - "isPlatformOwnedHost anti-shadow guard shared by the goproxy path and the transparent (eBPF/both) path"
  - "Deletion of the hardcoded google.com/Rickroll easter egg"
affects: [127-03, 127-04, 127-05]

tech-stack:
  added: []
  patterns:
    - "isPlatformOwnedHost: an unconditional handler consults a shared guard function (gated by the SAME booleans NewProxy already used to decide whether the platform handler itself was registered) before matching, so a plain fallthrough (goproxy DoFunc chains do not stop on nil) can never let a later, lower-priority handler answer a request a live platform handler was supposed to own"
    - "Internal (package httpproxy, non-_test-suffixed) test file alongside the existing external (package httpproxy_test) test files, used only where an unexported helper must be exercised directly and a full network harness is impractical (transparent_internal_test.go)"

key-files:
  created:
    - sidecars/http-proxy/httpproxy/intercept.go
    - sidecars/http-proxy/httpproxy/intercept_test.go
    - sidecars/http-proxy/httpproxy/transparent_internal_test.go
  modified:
    - sidecars/http-proxy/httpproxy/proxy.go
    - sidecars/http-proxy/httpproxy/transparent.go
    - sidecars/http-proxy/main.go

key-decisions:
  - "Extended registerInterceptHandlers's signature beyond the plan's literal 3-arg sketch to (proxy, ics, sandboxID, meteringEnabled, githubEnabled) and added an isPlatformOwnedHost guard — required because goproxy's OnRequest/OnResponse dispatch does NOT stop at a handler that returns nil (a 'fall through' decision, e.g. an allowed GitHub repo or a not-yet-exhausted AI budget); without the guard, a later, unconditional intercept handler matching the same host would silently answer instead of the real dial ever happening, which is exactly the shadowing the design's locked decision #3 and this plan's must_haves prohibit"
  - "Applied the identical isPlatformOwnedHost guard on the transparent (eBPF/both) path via a new matchInterceptForRequest helper on TransparentListener — the same fallthrough-then-shadow risk exists in relayWithInspection's hand-written sequential checks (GitHub-blocked / budget-exhausted both `return` only on the negative case, not on the positive 'this host is platform-owned' case)"
  - "Added transparent_internal_test.go as package httpproxy (not httpproxy_test) specifically to reach the new unexported matchInterceptForRequest helper directly, per the plan's own fallback instruction ('If exercising the full listener is impractical, factor the match-and-respond decision into a small helper ... and test that helper directly') — there is no pinned-BPF-map fixture available in unit tests, and no existing test in this package drives relayWithInspection at all"
  - "InterceptResponse's redirect body/status intentionally omits the old Server: gws header — documented in the code as the deliberate end of the site-impersonation cosplay the old handler did, per the plan's own instruction"

requirements-completed: [REQ-127-MATCH, REQ-127-ORDER, REQ-127-ENV]

coverage:
  - id: D1
    description: "MatchesHost reproduces IsHostAllowed's apex/subdomain/port/case semantics without regex and without honouring '*', and rejects the suffix-extension lookalike (google.com.evil.example) that today's unanchored regex incorrectly matched"
    requirement: "REQ-127-MATCH"
    verification:
      - kind: unit
        ref: "sidecars/http-proxy/httpproxy/intercept_test.go#TestMatchesHost_SuffixExtensionDoesNotMatch"
        status: pass
      - kind: unit
        ref: "sidecars/http-proxy/httpproxy/intercept_test.go#TestMatchesHost_WildcardNotHonoured"
        status: pass
      - kind: unit
        ref: "sidecars/http-proxy/httpproxy/intercept_test.go#TestMatchesHost_BareEntryIsExact"
        status: pass
    human_judgment: false
  - id: D2
    description: "ParseIntercepts decodes base64+JSON KM_MITM_INTERCEPTS, fails toward zero intercepts (never half-applies) on a decode/unmarshal error, drops individually-unsafe entries while keeping well-formed ones, and round-trips a respond body containing spaces and an embedded newline byte for byte"
    requirement: "REQ-127-ENV"
    verification:
      - kind: unit
        ref: "sidecars/http-proxy/httpproxy/intercept_test.go#TestParseIntercepts_GarbageBase64ReturnsErrorNoPanic"
        status: pass
      - kind: unit
        ref: "sidecars/http-proxy/httpproxy/intercept_test.go#TestParseIntercepts_ValidBase64InvalidJSONReturnsError"
        status: pass
      - kind: unit
        ref: "sidecars/http-proxy/httpproxy/intercept_test.go#TestParseIntercepts_DropsUnsafeEntriesKeepsWellFormed"
        status: pass
      - kind: unit
        ref: "sidecars/http-proxy/httpproxy/intercept_test.go#TestParseIntercepts_RespondBodyWithSpacesAndNewlineSurvives"
        status: pass
    human_judgment: false
  - id: D3
    description: "registerInterceptHandlers registers at exactly the old easter-egg's position (after the GitHub repo filter, before the general CONNECT handler); a denied host, a GitHub-filtered host, and an AI-metered host declared as an intercept are all won by the platform handler, not the intercept, proven end to end on the actual client response"
    requirement: "REQ-127-ORDER"
    verification:
      - kind: unit
        ref: "sidecars/http-proxy/httpproxy/intercept_test.go#TestHTTPProxy_DenyBeatsIntercept"
        status: pass
      - kind: unit
        ref: "sidecars/http-proxy/httpproxy/intercept_test.go#TestHTTPProxy_GitHubFilterBeatsIntercept"
        status: pass
      - kind: unit
        ref: "sidecars/http-proxy/httpproxy/intercept_test.go#TestHTTPProxy_AnthropicMeteringBeatsIntercept"
        status: pass
      - kind: unit
        ref: "sidecars/http-proxy/httpproxy/intercept_test.go#TestHTTPProxy_InterceptFiresForHostAbsentFromAllowlist"
        status: pass
      - kind: unit
        ref: "sidecars/http-proxy/httpproxy/intercept_test.go#TestHTTPProxy_NoInterceptsGoogleNotRedirected"
        status: pass
    human_judgment: false
  - id: D4
    description: "main.go reads KM_MITM_INTERCEPTS and fails toward zero intercepts (log.Error, never log.Fatal) on a decode error so a malformed rule set never costs the sandbox its egress; a well-formed value logs mitm_intercepts_loaded with the rule count"
    requirement: "REQ-127-ENV"
    verification:
      - kind: unit
        ref: "go build ./sidecars/... (exit 0)"
        status: pass
      - kind: other
        ref: "grep -c 'log.Fatal' sidecars/http-proxy/main.go unchanged at 4 across this plan"
        status: pass
    human_judgment: false
  - id: D5
    description: "DNAT'd traffic under enforcement: ebpf/both (the transparent listener path) honours intercepts after the GitHub repo-filter and budget pre-flight checks, via a matchInterceptForRequest helper that applies the same anti-shadow guard as the goproxy path"
    requirement: "REQ-127-ORDER"
    verification:
      - kind: unit
        ref: "sidecars/http-proxy/httpproxy/transparent_internal_test.go#TestTransparentListener_MatchInterceptForRequest_Hit"
        status: pass
      - kind: unit
        ref: "sidecars/http-proxy/httpproxy/transparent_internal_test.go#TestTransparentListener_MatchInterceptForRequest_GitHubPrecedence"
        status: pass
      - kind: unit
        ref: "sidecars/http-proxy/httpproxy/transparent_internal_test.go#TestTransparentListener_MatchInterceptForRequest_AnthropicMeteringPrecedence"
        status: pass
    human_judgment: false

duration: 15min
completed: 2026-08-26
status: complete
---

# Phase 127 Plan 02: Declarative MITM intercepts — sidecar engine Summary

**Replaced the compiled-in google.com/Rickroll easter egg in the http-proxy sidecar with a generic KM_MITM_INTERCEPTS-driven intercept engine (matcher, base64/JSON loader, redirect/respond response builder), registered at the exact old handler position and hardened with an isPlatformOwnedHost guard so a declared rule can never shadow the deny gate, AI budget metering, or the GitHub repo filter on either the goproxy or the transparent (eBPF/both) code path.**

## Performance

- **Started:** 2026-08-26T01:33:00-04:00 (approx)
- **Completed:** 2026-08-26T01:47:08-04:00
- **Duration:** ~15 min of tool time
- **Tasks:** 3
- **Files modified:** 6 (3 created: intercept.go, intercept_test.go, transparent_internal_test.go; 3 modified: proxy.go, transparent.go, main.go)

## Accomplishments

- `sidecars/http-proxy/httpproxy/intercept.go`: `Intercept`/`Respond` wire types, `MatchesHost` (apex/subdomain/port/case matching, no regex, no `*` wildcard — narrower than the deleted unanchored regex, fixing the `google.com.evil.example` lookalike bug), `MatchIntercept` (first-match-wins), `ParseIntercepts` (base64+JSON, fails toward zero rules, drops individually-unsafe entries), `WithIntercepts`, `InterceptResponse` (redirect/respond builder, no impersonation `Server` header), `registerInterceptHandlers`.
- `isPlatformOwnedHost`: the key correctness addition beyond the plan's literal sketch — a guard, shared by both the goproxy and transparent code paths, that stops an intercept from shadowing a live Bedrock/Anthropic/OpenAI metering handler or the GitHub repo filter. Discovered empirically: goproxy's `OnRequest`/`OnResponse` dispatch chains do not stop at a handler that returns `nil` (a "fall through" decision — e.g. an allowed GitHub repo, or an AI budget that is not yet exhausted), so an unconditional, later-registered intercept handler matching the same host would otherwise silently answer instead of letting the real dial happen. Proven with a failing-then-passing end-to-end test before and after the fix.
- Deleted the hardcoded `googleHostRegex` easter egg (both handlers, the redirect literal, the imitation `Server: gws` header) from `proxy.go`; `registerInterceptHandlers` now registers in its exact place — after the GitHub repo filter, before the general CONNECT handler.
- `main.go` reads `KM_MITM_INTERCEPTS`, parses it, and fails toward zero intercepts (never `log.Fatal`) on a decode error, logging `mitm_intercepts_loaded` with the rule count on success.
- `TransparentListener` gains `intercepts`/`SetIntercepts` and a `matchInterceptForRequest` helper, wired into `relayWithInspection` after the GitHub-blocked and budget-exhausted-preflight branches, so DNAT'd traffic under `enforcement: ebpf`/`both` gets the same precedence guarantees as the explicit-proxy path.

## Task Commits

1. **Task 1: Create intercept.go — matcher, loader, option, and response builder** - `b31e904d` (feat)
2. **Task 2: Delete the hardcoded easter-egg block and register intercepts in its place** - `fd03c68c` (feat)
3. **Task 3: Wire KM_MITM_INTERCEPTS in main.go and honour intercepts on the transparent path** - `4a77b032` (feat)
4. **Fixup: avoid the deleted rickroll URL literal in test fixtures** - `e2fcf412` (fix) — the plan's own verification block greps for zero occurrences of the old Rickroll URL anywhere under `sidecars/`; the intercept tests had reused that exact URL as an example redirect target, so it was swapped for a generic `https://example.com/redirected`.

**Plan metadata:** (this commit, docs)

## Files Created/Modified

- `sidecars/http-proxy/httpproxy/intercept.go` - matcher/loader/option/response-builder/handler-registration + the `isPlatformOwnedHost` anti-shadow guard
- `sidecars/http-proxy/httpproxy/intercept_test.go` - unit tests for every matcher/loader/builder behavior plus end-to-end precedence tests (deny/GitHub/Anthropic beat intercept; intercept fires for a host absent from the allowlist; no intercepts leaves google.com unredirected; first-declared rule wins)
- `sidecars/http-proxy/httpproxy/transparent_internal_test.go` - internal (package `httpproxy`) test file exercising `matchInterceptForRequest` directly (hit/miss, GitHub precedence, Anthropic metering precedence)
- `sidecars/http-proxy/httpproxy/proxy.go` - `proxyConfig.intercepts` field; deleted the hardcoded google.com/Rickroll block; `registerInterceptHandlers` call at the exact same position
- `sidecars/http-proxy/httpproxy/transparent.go` - `TransparentListener.intercepts`/`SetIntercepts`/`matchInterceptForRequest`; intercept check in `relayWithInspection` after the GitHub and budget branches
- `sidecars/http-proxy/main.go` - `KM_MITM_INTERCEPTS` read/parse/log, `WithIntercepts` wiring, `tl.SetIntercepts` on the transparent branch

## Decisions Made

See `key-decisions` in the frontmatter — the `isPlatformOwnedHost` guard (both goproxy and transparent paths) and the `transparent_internal_test.go` internal test file are the two decisions that extend beyond the plan's literal text, both required for correctness rather than convenience.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Added an isPlatformOwnedHost guard to prevent intercepts from shadowing platform metering/repo-filter handlers**
- **Found during:** Task 2, while writing the required end-to-end precedence test `TestHTTPProxy_AnthropicMeteringBeatsIntercept`.
- **Issue:** The plan's literal `registerInterceptHandlers(proxy *goproxy.ProxyHttpServer, ics []Intercept, sandboxID string)` sketch registers an unconditional handler relying solely on registration ORDER for precedence. Empirically, goproxy's `filterRequest` does not stop iterating handlers when one returns `nil` (a "fall through," e.g. the Anthropic budget preflight when not exhausted, or the GitHub repo filter when the repo is allowed) — so a later, unconditional intercept handler matching the same host would still answer with a canned response, and the real dial (and therefore the real metering / repo-allow decision) would never happen. This directly contradicted the plan's own must_have ("An intercept declaring a metering host does not shadow the Bedrock/Anthropic/OpenAI handlers or the GitHub repo filter") and the design's locked decision #3.
- **Fix:** Added `isPlatformOwnedHost(host, meteringEnabled, githubEnabled bool) bool` (checking the same `bedrockHostRegex`/`anthropicHostRegex`/`openaiHostRegex`/`githubHostsRegex` the platform handlers already use, gated by the identical booleans `NewProxy` uses to decide whether those handlers were registered at all) and consulted it at the top of both the CONNECT and request handlers in `registerInterceptHandlers`, before any `MatchIntercept` call. Extended the function signature to `(proxy, ics, sandboxID, meteringEnabled, githubEnabled)` to carry the two booleans.
- **Files modified:** `sidecars/http-proxy/httpproxy/intercept.go` (guard + signature), `sidecars/http-proxy/httpproxy/proxy.go` (call site)
- **Verification:** `TestHTTPProxy_AnthropicMeteringBeatsIntercept` and `TestHTTPProxy_GitHubFilterBeatsIntercept` reproduce the failure without the guard (confirmed manually before adding it — the Anthropic test's `stub.capturedSK` stayed empty because the intercept answered first) and pass with it.
- **Committed in:** `b31e904d` (guard added in Task 1's file so it compiles standalone), wired and proven in `fd03c68c` (Task 2)

**2. [Rule 1 - Bug] Applied the same guard on the transparent (eBPF/both) path**
- **Found during:** Task 3, reasoning through `relayWithInspection`'s hand-written sequential checks.
- **Issue:** The same fallthrough-then-shadow risk exists in the transparent listener: the GitHub-blocked and budget-exhausted-preflight branches `return` early only on the NEGATIVE case (repo blocked / budget exhausted); on the positive case (repo allowed / budget available) they fall through to later code, so an unconditionally-placed intercept check would shadow the real dial and the metering that happens after it, exactly as in the goproxy path.
- **Fix:** Added `TransparentListener.matchInterceptForRequest`, applying the identical `isPlatformOwnedHost` guard, and called it from `relayWithInspection` instead of a bare `MatchIntercept` call.
- **Files modified:** `sidecars/http-proxy/httpproxy/transparent.go`
- **Verification:** `transparent_internal_test.go`'s `_GitHubPrecedence` and `_AnthropicMeteringPrecedence` tests.
- **Committed in:** `4a77b032` (Task 3)

**3. [Rule 3 - Blocking] Replaced the deleted Rickroll URL literal in test fixtures with a generic example URL**
- **Found during:** Post-Task-3 plan-level verification pass (`grep -rc 'dQw4w9WgXcQ' sidecars/` from the plan's `<verification>` block).
- **Issue:** `intercept_test.go` had reused `https://www.youtube.com/watch?v=dQw4w9WgXcQ` (from the worked YAML example in `127-CONTEXT.md`) as an example redirect target in several tests, which made the plan's own "the literal string is fully gone from `sidecars/`" check return a nonzero count.
- **Fix:** Swapped every occurrence for `https://example.com/redirected` (per the repo's "use generic placeholders" convention).
- **Files modified:** `sidecars/http-proxy/httpproxy/intercept_test.go`
- **Verification:** `grep -rc 'dQw4w9WgXcQ' sidecars/` returns 0 for every file; `go test ./sidecars/http-proxy/...` still green.
- **Committed in:** `e2fcf412`

---

**Total deviations:** 3 auto-fixed (2 Rule 1 correctness fixes, 1 Rule 3 blocking-verification fix)
**Impact on plan:** All three were necessary for the plan's own stated must_haves and verification block to actually hold; none change scope, add features, or touch files outside `sidecars/http-proxy/**`.

## Issues Encountered

The plan's literal `registerInterceptHandlers` sketch and the "registration order alone is sufficient" framing in the design doc/context turned out to be insufficient on their own — goproxy's fallthrough dispatch semantics required an explicit host-ownership guard (see Deviations #1 and #2 above) to actually deliver the precedence guarantee the plan's must_haves require. This was caught by writing the required end-to-end precedence tests exactly as specified before assuming they would pass.

## User Setup Required

None — no external service configuration required. This plan is code-only (Go sidecar changes); no `km init`, `km create`, or AWS-touching command was run, per the plan's constraints.

## Next Phase Readiness

- `KM_MITM_INTERCEPTS` (base64 JSON array, flat `{name, hosts, redirect}` / `{name, hosts, respond:{status,contentType,body}}` shape) is the wire contract plan 03 (compiler drop-in) must produce from the profile-level types plan 01 already shipped (`pkg/profile/mitm.go`'s `EnabledIntercepts`).
- No blockers. `go test ./sidecars/http-proxy/... -count=1` and `go build ./...` are both green; `make test` (whole repo, minus the four excluded packages) is green.
- Plan 03 should note: the sidecar only ever sees ENABLED, already-collapsed-by-name intercepts — `ParseIntercepts` has no `enabled` field in its wire type at all, matching the design's "disabled rules are dropped at compile time" contract.
- Plan 04 (validation) can rely on `isPlatformOwnedHost`'s regex list (Bedrock/Anthropic/OpenAI/GitHub) as the authoritative "platform-reserved host" set when writing the `km validate` WARN for an intercept overlapping one of those hosts — it is the same list this plan uses to enforce the guarantee at runtime.

---
*Phase: 127-declarative-mitm-intercepts-profile-declared-host-to-action-*
*Completed: 2026-08-26*
