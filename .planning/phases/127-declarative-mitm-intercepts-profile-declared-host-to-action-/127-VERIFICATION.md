---
phase: 127-declarative-mitm-intercepts-profile-declared-host-to-action-
verified: 2026-08-26T06:48:51Z
status: human_needed
score: 17/17 must-haves verified
behavior_unverified: 0
overrides_applied: 0
human_verification:
  - test: "Part 2, Step 1 (Default flipped) — `km create` a profile with no `mitm:` block, `km shell`, `curl -sI https://google.com`"
    expected: "Google's real response (or an allowlist block) — NOT a 301 to YouTube. Proves the built-in Rickroll is gone fleet-wide."
    why_human: "Requires a live AWS sandbox; cannot be exercised from static analysis or unit tests."
  - test: "Part 2, Step 2 (Opt back in) — `km create profiles/mitm-demo.yaml`, `curl -sI https://www.google.com`, `km logs`"
    expected: "301 with Location: https://www.youtube.com/watch?v=dQw4w9WgXcQ; km logs shows event_type=mitm_intercept intercept=rickroll"
    why_human: "Requires a live AWS sandbox and real cloud-init boot."
  - test: "Part 2, Step 3 (The off-switch) — a copy of mitm-demo.yaml with `rickroll`/`enabled:false` active"
    expected: "curl no longer 301s to YouTube; a profile whose ONLY enabled rule is disabled writes no mitm.conf drop-in at all"
    why_human: "Requires a live AWS sandbox; also needs an on-box file-presence check via SSM."
  - test: "Part 2, Step 4 (respond path + systemd/base64 transport) — curl https://api.example.com/ against mitm-demo1"
    expected: "HTTP 503, body byte-for-byte 'maintenance window in progress' (embedded spaces surviving the base64 Environment= transport)"
    why_human: "Requires a live AWS sandbox; the unit-level wire-contract test (TestMITMWireContract_CompilerAndSidecarAgree, already run and green) covers the encode/decode round trip but not the live systemd unit."
  - test: "Part 2, Step 5 (Deny beats intercept live) — add api.example.com to deniedHosts on a mitm-demo.yaml copy, create, curl"
    expected: "km validate WARNs about the denied-host overlap before create; live curl returns the deny gate's 403, not the rule's 503"
    why_human: "Requires a live AWS sandbox to prove runtime precedence, not just the validate-time WARN (already verified statically)."
  - test: "Part 2, Step 6 (eBPF threading) — mitm-demo.yaml under enforcement: both, curl https://www.google.com"
    expected: "Rule fires identically via the DNAT/connect4 path, confirming buildL7ProxyHosts' intercept-host threading end to end"
    why_human: "Requires a live AWS sandbox with eBPF enforcement; buildL7ProxyHosts' code path was verified statically but the DNAT redirect itself needs a running kernel/BPF map."
---

# Phase 127: Declarative MITM intercepts — profile-declared host-to-action rules Verification Report

**Phase Goal:** Replace the hardcoded `google.com` → Rickroll easter egg in the http-proxy sidecar with a declarative, profile-declared `spec.network.mitm.intercepts` surface. Human's stated goal, verbatim: *"The goal is to have no more mitm on the google by default, but to have the sample written up."*

**Verified:** 2026-08-26T06:48:51Z
**Status:** human_needed
**Re-verification:** No — initial verification

## Summary

Both must-haves the task asked me to verify hardest hold up under direct code
inspection, live test execution, and one adversarial guard-disable experiment:

1. **DEFAULT OFF is real, not just documented.** Traced the full chain: `pkg/profile/mitm.go`'s
   `ProfileIntercepts`/`EnabledIntercepts` are nil-safe (a profile with no `mitm:` block returns
   `nil` from every accessor) → `pkg/compiler/userdata.go`'s `buildMITMInterceptsB64` returns `""`
   when there are zero enabled intercepts → the `{{- if .MITMInterceptsB64 }}` template guard skips
   the entire `mitm.conf` drop-in and its `KM_MITM_INTERCEPTS=` line → `sidecars/http-proxy/main.go`
   only calls `httpproxy.WithIntercepts`/`tl.SetIntercepts` when `os.Getenv("KM_MITM_INTERCEPTS")` is
   non-empty → `WithIntercepts` is a no-op on an empty slice → `NewProxy`'s
   `if len(cfg.intercepts) > 0 { registerInterceptHandlers(...) }` never registers the handlers at
   all. The old `googleHostRegex`/Rickroll code is **fully deleted** (only comments and test-fixture
   URLs reference the old string now — confirmed via `grep -rn "googleHostRegex|Rickroll|gws"` across
   the sidecar). Grepped every shipped profile (`profiles/`, `pkg/profile/builtins/`) and confirmed
   **only** `profiles/mitm-demo.yaml` and its own `profiles/base/mitm-rickroll.yaml` fragment
   reference `mitm:` at all — no default/builtin profile opts in.
2. **SAMPLE WRITTEN UP is real and usable, not just present.** `profiles/base/mitm-rickroll.yaml`
   (abstract fragment) and `profiles/mitm-demo.yaml` (worked leaf extending it) both exist,
   `./km validate` reports them correctly (SKIP for the abstract fragment, `valid` for the leaf, zero
   WARN lines), and `bash scripts/validate-all-profiles.sh` passes 27/27 including the new demo.
   I additionally resolved `mitm-demo.yaml` through the real `pkg/profile.Resolve()` (not just a
   hand-built test fixture) in a throwaway test and confirmed it produces exactly the two expected
   enabled intercepts (`rickroll` from the fragment, `maintenance` from the leaf) — the `extends:`
   chain genuinely works end to end. `docs/mitm-intercepts.md` is a 253-line runbook whose every
   factual claim I cross-checked against the actual code (matching semantics, precedence order,
   deploy surface, validate errors/warnings, eBPF DNS-widening caveat) and found accurate.

**The security-critical `isPlatformOwnedHost` anti-shadow guard is real and load-bearing**, not
decorative. I read it on both the goproxy path (`intercept.go`'s `registerInterceptHandlers`, called
from `proxy.go` with `cfg.budget != nil, len(cfg.githubRepos) > 0` — the exact same booleans that
gate whether the Bedrock/Anthropic/OpenAI/GitHub handlers are registered in the first place) and the
transparent/eBPF path (`transparent.go`'s `matchInterceptForRequest`, called from
`relayWithInspection` with `tl.budget != nil, len(tl.githubRepos) > 0`). To go beyond trusting the
SUMMARY's claim, I **temporarily patched `isPlatformOwnedHost` to always return `false`** and
re-ran the targeted precedence tests: `TestHTTPProxy_AnthropicMeteringBeatsIntercept` and both
`TestTransparentListener_MatchInterceptForRequest_{GitHubPrecedence,AnthropicMeteringPrecedence}`
**failed** with the guard disabled (an intercept answered instead of the real metering/repo-filter
handler) and pass with it restored — proving the guard is the thing actually preventing the shadow,
not an incidental side effect of test ordering. I restored the file afterward and confirmed `git
diff` shows zero drift and the full sidecar suite is green again.

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|---|---|---|
| 1 | A profile can declare `spec.network.mitm.intercepts[]` with name/enabled/hosts/action and `km validate` accepts it | ✓ VERIFIED | `./km validate profiles/mitm-demo.yaml` → `valid`; schema at `pkg/profile/schemas/sandbox_profile.schema.json:476-533` |
| 2 | A leaf that extends a fragment and restates an intercept by name overrides it wholesale (last-wins), not union | ✓ VERIFIED | `pkg/profile/mitm.go` `CollapseIntercepts`; live-resolved `mitm-demo.yaml` produces exactly 2 collapsed intercepts, not 3 |
| 3 | A leaf carrying only `name` + `enabled:false` resolves to a disabled rule, zero enabled intercepts exposed | ✓ VERIFIED | `EnabledIntercepts` filters on `InterceptEnabled`; `pkg/compiler/userdata_mitm_test.go#TestMITMInterceptsDormant_OnlyDisabledIntercept` passes |
| 4 | A profile with no `mitm:` block yields nil `NetworkMITMSpec`, zero intercepts from every accessor | ✓ VERIFIED | `ProfileIntercepts` nil-safe; `TestMITMInterceptsDormant_NoMITMBlock` + `TestValidateMITM_NoMITMBlock_NoError` pass |
| 5 | The proxy reads `KM_MITM_INTERCEPTS`, decodes base64 JSON, answers with the declared redirect/canned response | ✓ VERIFIED | `main.go:155-169`; `intercept.go` `ParseIntercepts`/`InterceptResponse`; live-run `TestHTTPProxy_InterceptFiresForHostAbsentFromAllowlist` passes |
| 6 | A lookalike host the old unanchored regex matched by accident does NOT match the new matcher | ✓ VERIFIED | `MatchesHost` requires exact apex/subdomain relationship; `TestMatchesHost_SuffixExtensionDoesNotMatch` passes |
| 7 | An intercept declaring a metering host does not shadow Bedrock/Anthropic/OpenAI/GitHub | ✓ VERIFIED | `isPlatformOwnedHost` on both code paths; **adversarially proven load-bearing** by disabling the guard and observing 3 precedence tests fail, then restoring and re-confirming green (see Summary) |
| 8 | An intercept declaring a denied host never fires — the deny gate answers first | ✓ VERIFIED | Deny gate registered first in `NewProxy`; `TestHTTPProxy_DenyBeatsIntercept` passes live |
| 9 | A `KM_MITM_INTERCEPTS` value that fails to decode registers zero intercepts, logs an error, egress still works | ✓ VERIFIED | `main.go` uses `log.Error` (not `log.Fatal`) on parse failure; `grep -c log.Fatal` unchanged at 4 |
| 10 | An intercept fires for a host absent from `ALLOWED_HOSTS`, preserving the old built-in's behaviour | ✓ VERIFIED | `TestHTTPProxy_InterceptFiresForHostAbsentFromAllowlist` passes; registration point is ahead of the general CONNECT allowlist handler |
| 11 | An enabled intercept renders a systemd drop-in with base64 `KM_MITM_INTERCEPTS` | ✓ VERIFIED | `userdata.go:5100-5119` guarded template section; `TestMITMInterceptsEmission_RedirectRule` passes |
| 12 | No `mitm:` block → no drop-in, and frozen byte-identity goldens still match | ✓ VERIFIED | `TestMITMInterceptsDormant_NoMITMBlock` + `TestMITMInterceptsByteIdentityGoldensStillPass` (phase92 + h1 subtests) pass |
| 13 | A profile whose only intercept is disabled renders no drop-in | ✓ VERIFIED | `TestMITMInterceptsDormant_OnlyDisabledIntercept` passes |
| 14 | The compiler's emitted base64 decodes through the sidecar's own `ParseIntercepts`, incl. a body with spaces + newline | ✓ VERIFIED | `TestMITMWireContract_CompilerAndSidecarAgree` — a genuine cross-package test importing `httpproxy` into the compiler test — passes |
| 15 | Intercept hosts appear in `--proxy-hosts` so `connect4` DNATs them under `ebpf`/`both` | ✓ VERIFIED | `buildL7ProxyHosts` appends `EnabledIntercepts(...)` hosts last (code read directly, `userdata.go:5940-5949`) |
| 16 | `km validate` enforces the 5 locked errors and 3 locked warnings | ✓ VERIFIED | `pkg/profile/validate.go` `validateMITMIntercepts`; 30+ targeted tests in `pkg/profile` all pass live |
| 17 | Default-off + sample-written-up: fragment, demo leaf, and docs exist and are actually usable | ✓ VERIFIED | Files exist; `km validate` clean; `scripts/validate-all-profiles.sh` 27/27; live-resolved via `pkg/profile.Resolve()`; docs cross-checked against code |

**Score:** 17/17 truths verified (0 present-but-behavior-unverified)

### Required Artifacts

| Artifact | Expected | Status | Details |
|---|---|---|---|
| `pkg/profile/types.go` (NetworkSpec.MITM + 4 structs) | Go types for the intercept surface | ✓ VERIFIED | `NetworkMITMSpec`/`MITMIntercept`/`MITMAction`/`MITMRespond` present, wired into `NetworkSpec` |
| `pkg/profile/mitm.go` | Collapse/enabled accessors | ✓ VERIFIED | `CollapseIntercepts`, `EnabledIntercepts`, `InterceptEnabled`, `DuplicateInterceptNames`, `ProfileIntercepts` all present, substantive, tested |
| `pkg/profile/schemas/sandbox_profile.schema.json` | `spec.network.mitm` subtree | ✓ VERIFIED | `additionalProperties:false` throughout; name pattern matches `mitmNamePattern` in validate.go |
| `sidecars/http-proxy/httpproxy/intercept.go` | Matcher/loader/handlers/anti-shadow guard | ✓ VERIFIED | 258 lines, no stubs; `isPlatformOwnedHost` present and load-bearing (proven adversarially) |
| `sidecars/http-proxy/httpproxy/proxy.go` | Old easter egg deleted; intercepts registered at exact old position | ✓ VERIFIED | `grep` for `googleHostRegex`/`Rickroll`/`gws` in `proxy.go` returns only a doc-comment reference; registration order confirmed by direct read: deny → SES MITM → action quota → GitHub filter → intercepts → general CONNECT |
| `sidecars/http-proxy/httpproxy/transparent.go` | `SetIntercepts` + guarded transparent-path check | ✓ VERIFIED | `matchInterceptForRequest` applies the same guard; called from `relayWithInspection` after GitHub/budget branches |
| `sidecars/http-proxy/main.go` | `KM_MITM_INTERCEPTS` env read, fail-toward-zero | ✓ VERIFIED | Wired to both `WithIntercepts` (goproxy) and `tl.SetIntercepts` (transparent) |
| `pkg/compiler/userdata.go` | `MITMInterceptsB64`, `buildMITMInterceptsB64`, guarded template, `buildL7ProxyHosts` addition | ✓ VERIFIED | All four present and correctly gated |
| `pkg/profile/validate.go` | `validateMITMIntercepts` + reserved-host list | ✓ VERIFIED | Reserved-host list matches `isPlatformOwnedHost`'s regex set exactly (cross-referenced by file/line comments in the code itself) |
| `profiles/base/mitm-rickroll.yaml` | Abstract migration fragment | ✓ VERIFIED | `metadata.abstract: true`; `km validate` SKIPs it correctly; reproduces the exact old redirect target |
| `profiles/mitm-demo.yaml` | Worked leaf | ✓ VERIFIED | Extends the fragment + 3 other base fragments, adds a `respond` rule, carries a documented off-switch; validates clean; resolves correctly via real `Resolve()` |
| `docs/mitm-intercepts.md` | Operator runbook | ✓ VERIFIED | 253 lines; every factual claim cross-checked against code and found accurate |

### Key Link Verification

| From | To | Via | Status | Details |
|---|---|---|---|---|
| `pkg/profile/inherit.go` (`fromMap`) | `CollapseIntercepts` | post-merge normalisation | ✓ WIRED | Confirmed by live-resolving `mitm-demo.yaml` and getting exactly 2 collapsed rules |
| `pkg/compiler/userdata.go` `buildMITMInterceptsB64` | `profile.EnabledIntercepts` | same by-name-collapse/disabled-drop code path | ✓ WIRED | Direct code read + `TestMITMWireContract_CompilerAndSidecarAgree` |
| `pkg/compiler/userdata.go` template | `sidecars/http-proxy/main.go` `KM_MITM_INTERCEPTS` read | conditional systemd drop-in | ✓ WIRED | Cross-package contract test decodes the compiler's real emitted value with the sidecar's real `ParseIntercepts` |
| `sidecars/http-proxy/httpproxy/proxy.go` `NewProxy` | `registerInterceptHandlers` | `cfg.budget != nil, len(cfg.githubRepos) > 0` passthrough | ✓ WIRED | Verified the exact same booleans gate both the platform handlers' registration and the guard — adversarially confirmed load-bearing |
| `sidecars/http-proxy/httpproxy/transparent.go` `relayWithInspection` | `matchInterceptForRequest` | same guard, transparent path | ✓ WIRED | Adversarially confirmed load-bearing (guard-disable experiment) |
| `pkg/profile/validate.go` `mitmReservedHosts` | `sidecars/http-proxy/httpproxy/intercept.go` `isPlatformOwnedHost` | hand-maintained mirror (pkg/profile cannot import a sidecar package) | ✓ WIRED (by inspection) | Both lists cover the same 4 GitHub hosts + Anthropic + OpenAI + Bedrock-any-region; comments in validate.go explicitly cross-reference the sidecar file/line |

### Behavioral Spot-Checks / Adversarial Verification

| Behavior | Command | Result | Status |
|---|---|---|---|
| Guard is load-bearing (goproxy path, Anthropic) | Patched `isPlatformOwnedHost` to `return false`, ran `TestHTTPProxy_AnthropicMeteringBeatsIntercept` | FAILED ("the intercept answered instead of the Anthropic metering handler") | ✓ Confirms guard is real, not decorative |
| Guard is load-bearing (transparent path, GitHub) | Same patch, ran `TestTransparentListener_MatchInterceptForRequest_GitHubPrecedence` | FAILED (intercept matched instead of nil) | ✓ Confirms guard is real, not decorative |
| Guard is load-bearing (transparent path, Anthropic) | Same patch, ran `TestTransparentListener_MatchInterceptForRequest_AnthropicMeteringPrecedence` | FAILED (intercept matched instead of nil) | ✓ Confirms guard is real, not decorative |
| Guard restored, suite re-verified | `go test ./sidecars/http-proxy/... -count=1` after restoring the file | `ok` | ✓ No lingering drift (`git diff` clean) |
| `mitm-demo.yaml` resolves via real `extends:` chain | Throwaway test calling `profile.Resolve("mitm-demo", ...)` | 2 enabled intercepts: `rickroll` (`.google.com`), `maintenance` (`api.example.com`) | ✓ Confirms the sample is genuinely usable, not just schema-valid in isolation |
| `scripts/validate-all-profiles.sh` | `bash scripts/validate-all-profiles.sh` | `all 27 profiles valid` | ✓ PASS |
| Full workspace test suite (run once) | `go test ./... -count=1 -timeout 600s` | Exit 1 — exactly the 5 documented pre-existing `internal/app/cmd` AWS-fast-fail-seam failures, no new failures | ✓ Matches documented baseline |
| `go build ./...` / `go vet ./...` | both | build clean; vet shows one pre-existing, unrelated IPv6-format warning in `transparent.go` (present since a pre-Phase-127 commit, `9366d571`) | ✓ No new issues |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|---|---|---|---|---|
| REQ-127-SCHEMA | 127-01 | Types + schema, no apiVersion bump | ✓ SATISFIED | `pkg/profile/types.go`, schema subtree |
| REQ-127-OVERRIDE | 127-01 | By-name last-wins collapse | ✓ SATISFIED | `CollapseIntercepts` + live-resolved test |
| REQ-127-MATCH | 127-02 | `IsHostAllowed`-style matching, no regex | ✓ SATISFIED | `MatchesHost` + suffix-lookalike test |
| REQ-127-ORDER | 127-02 | Exact registration position + precedence | ✓ SATISFIED | Direct code read + adversarial guard test |
| REQ-127-ENV | 127-02/03 | Base64 env transport, fail-toward-zero | ✓ SATISFIED | `main.go` + `ParseIntercepts` + wire-contract test |
| REQ-127-EBPF | 127-03 | `buildL7ProxyHosts` threading, no DNS widening | ✓ SATISFIED | Code read; `TestMITMIntercept_DoesNotWidenAllowedDNS` passes |
| REQ-127-VALIDATE | 127-04 | 5 errors + 3 warnings | ✓ SATISFIED | `validateMITMIntercepts`, 30+ tests pass |
| REQ-127-DEFAULTOFF | 127-05 | Fragment + demo + docs | ✓ SATISFIED | Files exist, validate clean, resolve correctly |
| REQ-127-DEPLOY | 127-05 | 3-command deploy sequence documented | ✓ SATISFIED | `docs/mitm-intercepts.md` § Deploy surface matches CLAUDE.md Phase 129 block |

**Orphaned requirements:** None of REQ-127-SCHEMA/OVERRIDE/MATCH/ORDER/ENV/EBPF/VALIDATE/DEFAULTOFF/DEPLOY appear in `.planning/REQUIREMENTS.md` — this is a **known, pre-flagged gap** (the task description explicitly calls it out as "known"), not a new finding. It does not block phase completion since the requirements are fully traceable through the ROADMAP.md phase entry and the plan frontmatter instead, and every one was independently verified against the codebase in this report.

### Anti-Patterns Found

None. Grepped every file this phase created or modified (`sidecars/http-proxy/httpproxy/{intercept,transparent,proxy}.go`, `sidecars/http-proxy/main.go`, `pkg/profile/{mitm,validate}.go`, `pkg/compiler/userdata.go`, `profiles/base/mitm-rickroll.yaml`, `profiles/mitm-demo.yaml`, `docs/mitm-intercepts.md`) for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER` — zero hits. The old `Rickroll`/`googleHostRegex`/`gws` strings are fully deleted from source (only a doc-comment reference and generic test-fixture names like `"rickroll"` remain, which is expected — they name the *migrated* rule, not dead code).

**Unrelated, pre-existing observation (not a Phase 127 gap):** `sidecars/http-proxy/httpproxy/transparent.go` has no deny-gate check at all in `relayWithInspection` — the Aug-21 egress-deny-lists feature (`netpolicy.Denier`) is wired into the goproxy path (`main.go:48`, `WithDenier`) but never into `TransparentListener`. This predates Phase 127 (confirmed via `git log --follow`) and is outside this phase's scope — the phase's own "deny beats intercept" claim and its `127-UAT.md` Step 5 test both target the goproxy (default `enforcement: proxy`) path only, which does have the deny gate. Flagging for awareness, not as a Phase 127 blocker.

### Human Verification Required

The 6-step live AWS UAT in `127-UAT.md` Part 2 is legitimately **PENDING** — it is being executed separately by the orchestrator right now, per the task's known context, and every step is fully scaffolded with exact commands and expected results. All 6 items are listed in the frontmatter `human_verification` block above. None of them represent a code gap; they are the live-infrastructure half of verification that cannot be exercised from a static/unit-test pass (the code paths they exercise were independently verified here via unit tests, the compiler-to-sidecar wire-contract test, and the adversarial guard-disable experiment — Part 2 is confirming the same logic holds when a real kernel, real systemd, and real DNS/eBPF maps are involved).

### Gaps Summary

No code-level gaps found. Every must-have truth from all 5 plans is VERIFIED against the actual codebase (not just the SUMMARY narrative), all 9 phase requirements are satisfied, all artifacts exist/are substantive/are wired, `go build`/`go vet` are clean (one unrelated pre-existing vet warning), the full test suite matches the documented environmental-failure baseline exactly (5 known `internal/app/cmd` AWS-fast-fail failures, nothing new), and the security-critical anti-shadow guard was proven load-bearing through direct experimentation rather than accepted on the SUMMARY's word. The only outstanding item is the live 6-step AWS UAT (Part 2), which is explicitly out of this plan's scope and is being run separately — hence `human_needed` rather than `passed`.

---

*Verified: 2026-08-26T06:48:51Z*
*Verifier: Claude (gsd-verifier)*
