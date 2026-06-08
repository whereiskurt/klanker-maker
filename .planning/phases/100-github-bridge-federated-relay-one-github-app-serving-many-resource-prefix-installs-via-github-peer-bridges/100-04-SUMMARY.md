---
phase: 100-github-bridge-federated-relay-one-github-app-serving-many-resource-prefix-installs-via-github-peer-bridges
plan: 04
subsystem: infra
tags: [km-doctor, github-bridge, federated-relay, peer-bridges, operator-docs, uat, self-loop]

# Dependency graph
requires:
  - phase: 100-01
    provides: cfg.Github.PeerBridges config field (the check reads it via GetGithubPeerBridges)
  - phase: 100-03
    provides: shipped relay behavior (Resolve() reorder + !matched relay branch) that the docs/UAT describe
  - phase: 97-github-bridge
    provides: checkGitHubBridgeURL SSM path {prefix}config/github/bridge-url + GitHub doctor check group
provides:
  - checkGitHubPeerBridges (malformed URL / self-loop → WARN; empty → SKIP) mirroring checkSlackPeerBridges
  - GetGithubPeerBridges() DoctorConfigProvider accessor + adapter impl
  - km doctor "GitHub peer bridges" check wired into the GitHub group, gated on githubConfigured
  - docs/github-bridge.md § Phase 100 federated-relay operator runbook + deploy sequence
  - OPERATOR-GUIDE.md federated-relay subsection (cross-link)
  - CLAUDE.md Phase 100 note + Where-to-look row
  - 100-UAT.md two-install/one-App E2E runbook (GH-FED-E2E manual verification)
affects: [phase-101-orphan-repo-reply, github-bridge-operators]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Reuse the generic SSMParamStore (slackSSMStore) to read a non-Slack SSM path ({prefix}config/github/bridge-url) — the store is keyed by full path, not Slack-specific"
    - "doctor peer-bridge check mirrors the Slack template verbatim (url.Parse + scheme/host + self-loop), differing only in config source + messaging — keeps the two federation checks symmetric"
    - "Pattern-B placeholder in docs flipped from 'not yet implemented' to a full implemented runbook on phase landing"

key-files:
  created:
    - .planning/phases/100-github-bridge-federated-relay-one-github-app-serving-many-resource-prefix-installs-via-github-peer-bridges/100-UAT.md
    - .planning/phases/100-github-bridge-federated-relay-one-github-app-serving-many-resource-prefix-installs-via-github-peer-bridges/deferred-items.md
  modified:
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_github_test.go
    - internal/app/cmd/doctor_test.go
    - docs/github-bridge.md
    - OPERATOR-GUIDE.md
    - CLAUDE.md

key-decisions:
  - "checkGitHubPeerBridges reuses slackSSMStore (a generic SSMParamStore, not Slack-specific) to read {prefix}config/github/bridge-url — avoids threading a second SSM store through DoctorDeps just for one check."
  - "Implemented DETERMINISTIC checks only (RESEARCH OQ2): malformed-URL + self-loop + empty→SKIP. The undeterminable 'front-door-with-empty-peers' WARN was intentionally NOT implemented — the bridge cannot know whether GitHub is configured to deliver to it (GitHub-App-side state)."
  - "Wired the check next to the Slack peer-bridge wiring (where slackSSMStore is in scope) rather than in the GitHub group at line ~3438 (slackSSMStore not yet defined there); gated on githubConfigured to honor the dormant-by-default contract."
  - "Test placed in the existing doctor_github_test.go (test name TestGithubPeerBridges, -run GithubPeerBridges) per the plan artifact spec, not a standalone file."

patterns-established:
  - "GitHub federation doctor check is structurally identical to Slack's — future federation surfaces should keep this 1:1 mapping for operator predictability."

requirements-completed: [GH-FED-DOCTOR, GH-FED-E2E]

# Metrics
duration: 11min
completed: 2026-06-08
---

# Phase 100 Plan 04: km doctor peer-bridge check + federated-relay docs + E2E UAT Summary

**`checkGitHubPeerBridges` (malformed-URL / self-loop → WARN, empty → SKIP) wired into km doctor, plus the docs/github-bridge.md § Phase 100 federated-relay runbook, OPERATOR-GUIDE + CLAUDE.md notes, and the two-install/one-App GH-FED-E2E UAT runbook.**

## Performance

- **Duration:** 11 min
- **Started:** 2026-06-08T12:31:07Z
- **Completed:** 2026-06-08T12:42:43Z
- **Tasks:** 3
- **Files modified:** 6 modified, 2 created

## Accomplishments
- `checkGitHubPeerBridges` mirrors `checkSlackPeerBridges`: deterministic malformed-URL + self-loop WARNs, empty→SKIP, OK when clean. Wired into the GitHub bridge check group gated on `githubConfigured`, own bridge URL from SSM `{prefix}config/github/bridge-url`.
- `GetGithubPeerBridges()` added to the `DoctorConfigProvider` interface + `appConfigAdapter` (+ two test stubs).
- `docs/github-bridge.md` § Phase 100: front-door + full-symmetry model, verbatim header forward, single-hop `X-KM-Relayed:1` loop guard table, owner-posts-the-single-👀 rule, `github.peer_bridges` config surface, deploy sequence (`make build-lambdas` + `km init --dry-run=false`, explicitly NOT `--sidecars`), in-place `v1.1.0` edit note, dormancy/byte-identity invariant, `km doctor` peer check, and the documented-not-enforced correctness invariant. The "Pattern B — not yet implemented" placeholder is now marked implemented + cross-linked.
- `OPERATOR-GUIDE.md` federated-relay subsection cross-linking the runbook; `CLAUDE.md` Phase 100 note (newest-first) + Where-to-look row.
- `100-UAT.md`: two-install/one-App live runbook asserting exactly one 👀 (owner `sec`), correct dispatch, no double-processing, plus orphan-drop, loop-guard, dormancy, and rollback checks.

## Task Commits

1. **Task 1 (RED): failing test for checkGitHubPeerBridges** - `6a843bd1` (test)
2. **Task 1 (GREEN): implement checkGitHubPeerBridges + wiring** - `6ec78ddf` (feat)
3. **Task 2: federated-relay docs (github-bridge.md + OPERATOR-GUIDE + CLAUDE.md)** - `705a58c3` (docs)
4. **Task 3: two-install/one-App E2E UAT runbook + deferred-items** - `15cecb8d` (docs)

**Plan metadata:** _(this commit)_

## Files Created/Modified
- `internal/app/cmd/doctor.go` - `checkGitHubPeerBridges` + `GetGithubPeerBridges` interface/adapter method + GitHub-group wiring; `net/url` import.
- `internal/app/cmd/doctor_github_test.go` - `TestGithubPeerBridges` table-driven cases.
- `internal/app/cmd/doctor_test.go` - `GetGithubPeerBridges()` stubs on the two test config types.
- `docs/github-bridge.md` - § Phase 100 federated-relay runbook; ToC + doctor-table rows; Pattern B placeholder marked implemented.
- `OPERATOR-GUIDE.md` - federated-relay subsection (cross-link).
- `CLAUDE.md` - Phase 100 note + Where-to-look row.
- `.planning/phases/100-.../100-UAT.md` - GH-FED-E2E two-install runbook (created).
- `.planning/phases/100-.../deferred-items.md` - pre-existing out-of-scope test failure log (created).

## Decisions Made
- Reused `slackSSMStore` (generic `SSMParamStore`) to read the GitHub bridge-url — no new DoctorDeps field.
- Deterministic checks only per RESEARCH OQ2 — skipped the undeterminable front-door-empty WARN.
- Wired the check where `slackSSMStore` is in scope (next to the Slack peer wiring), gated on `githubConfigured`.

## Deviations from Plan

None — plan executed exactly as written. The config field (`cfg.Github.PeerBridges`) was already present from Plan 100-01, so Task 1 only added the doctor check + accessor as specified.

## Issues Encountered
- The full `internal/app/cmd` suite reported one failure, `TestUnlockCmd_RequiresStateBucket`, which reaches **live AWS** (DynamoDB lock lookup succeeds instead of short-circuiting on empty `StateBucket`) in this sandbox. It is unrelated to this plan's doctor/github/docs changes and fails identically on the pre-change commit `f81fd713`. Logged to `deferred-items.md` per the executor SCOPE BOUNDARY; not fixed. All in-scope suites (`pkg/github/bridge`, `internal/app/config`, and the doctor/github tests) are green, and `make build` succeeds (km v0.4.883).

## User Setup Required
None — no external service configuration required. (Operators run the 100-UAT.md runbook live during verification; that is documented, not automated.)

## Next Phase Readiness
- GH-FED-DOCTOR + GH-FED-E2E satisfied; Phase 100 federated relay is fully shipped (config + export + relayer + reorder/relay branch from Plans 01–03, doctor + docs + UAT here).
- Deferred to Phase 101: orphan-repo helpful PR comment (the claim-aware scatter-gather analog).

## Self-Check: PASSED

All claimed files exist (doctor.go, doctor_github_test.go, github-bridge.md, OPERATOR-GUIDE.md, CLAUDE.md, 100-UAT.md, 100-04-SUMMARY.md) and all four task commits (`6a843bd1`, `6ec78ddf`, `705a58c3`, `15cecb8d`) are present in git history. `checkGitHubPeerBridges` is present in both doctor.go and the test file. `make build` succeeds (km v0.4.883).

---
*Phase: 100-github-bridge-federated-relay-one-github-app-serving-many-resource-prefix-installs-via-github-peer-bridges*
*Completed: 2026-06-08*
