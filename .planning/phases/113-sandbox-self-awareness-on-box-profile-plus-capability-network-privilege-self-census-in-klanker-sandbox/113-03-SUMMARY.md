---
phase: 113-sandbox-self-awareness-on-box-profile-plus-capability-network-privilege-self-census-in-klanker-sandbox
plan: 03
subsystem: docs
tags: [docs, operational-gotchas, claude-md, live-uat, deploy-surface, self-census]

# Dependency graph
requires:
  - phase: 113-01
    provides: /opt/km/.km-profile.yaml written at boot (the file the docs + UAT describe)
  - phase: 113-02
    provides: klanker:sandbox six-section self-census (the skill the docs + UAT verify)
provides:
  - docs/operational-gotchas.md "Sandbox self-awareness (Phase 113)" section (on-box profile semantics + six-section census + deploy surface)
  - CLAUDE.md Phase 113 summary block + Where-to-look rows
  - 113-UAT.md (live UAT sign-off on slacktest02 / learn-f6c5af5c)
  - Redaction spec-review sign-off (verbatim — no embeddable-secret field found)
affects: [operators shipping Phase 113; any phase referencing the on-box profile or self-census]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Per-phase CLAUDE.md summary block (Phase 111/112 style): what changed, what's out of scope, deploy surface, where-to-look pointer"
    - "operational-gotchas section: semantic-equivalence (NOT byte-identity) framing for re-serialized on-box YAML vs raw S3 bytes"

key-files:
  created:
    - .planning/phases/113-sandbox-self-awareness-on-box-profile-plus-capability-network-privilege-self-census-in-klanker-sandbox/113-UAT.md
    - .planning/phases/113-sandbox-self-awareness-on-box-profile-plus-capability-network-privilege-self-census-in-klanker-sandbox/113-03-SUMMARY.md
  modified:
    - docs/operational-gotchas.md
    - CLAUDE.md
    - skills/sandbox/SKILL.md (Section C fix — committed 9e3918aa by orchestrator during UAT)

key-decisions:
  - "Redaction: write VERBATIM (no redaction) — review found no embeddable-secret field; profile carries SSM paths not values, sandbox user already has IAM read on those paths"
  - "Semantic equivalence (NOT byte-identical) is the documented + UAT-asserted invariant: on-box is yaml.Marshal(p) of the effective post-compile struct; S3 is the original rendered string; omitempty elides false-valued booleans on-box"
  - "Deploy surface: make build-lambdas + km init --dry-run=false (create-handler carries the userdata — NOT --sidecars) + plugin 0.4.10 bump; existing sandboxes need km destroy && km create"

requirements-completed: []

# Metrics
duration: 20min
completed: 2026-06-14
---

# Phase 113 Plan 03: Docs + Live UAT Summary

**Phase 113 documented (operational-gotchas.md + CLAUDE.md) and verified end-to-end on live sandbox `slacktest02` (`learn-f6c5af5c`): on-box profile present + semantically equivalent to S3, six-section census clean (incl. the single allowed/blocked curl pair), graceful degradation confirmed, redaction sign-off = verbatim.**

## Performance

- **Duration:** ~20 min (docs Task 1; UAT executed by orchestrator)
- **Completed:** 2026-06-14
- **Tasks:** 2 of 2
- **Files modified:** 3 (docs/operational-gotchas.md, CLAUDE.md, + skills/sandbox/SKILL.md via the UAT-driven Section C fix)

## Accomplishments

- **Task 1 (autonomous):** Confirmed `scripts/validate-all-profiles.sh` green (22/22 — no schema change introduced by the phase). Added a "Sandbox self-awareness (Phase 113)" section to `docs/operational-gotchas.md` covering:
  - `/opt/km/.km-profile.yaml` write semantics (`yaml.Marshal` of the params struct; mode 0644 sandbox:sandbox; written VERBATIM — secrets are SSM paths, not values).
  - **Semantic equivalence, NOT byte-identity** — re-serialized YAML vs raw S3 bytes; the on-box copy captures post-resolution mutations (`--no-bedrock`, TTL/idle overrides).
  - The six-section census table (A–F) and the **two safe-active curls** as the only traffic-generating step (visible in `km otel`).
  - Pre-Phase-113 graceful degradation (`PROFILE_AVAILABLE=0` fallback, never errors).
  - **Deploy surface:** `make build-lambdas` + `km init --dry-run=false` (NOT `--sidecars`), plugin `0.4.10` bump, existing sandboxes need `km destroy && km create`. No schema/TF/DDB/IAM/bridge change.
- Added a Phase 113 summary block to `CLAUDE.md` (Phase 111/112 style) + two "Where to look" rows (self-census skill + on-box profile / deploy notes).
- **Task 2 (checkpoint:human-verify):** Live UAT executed by the orchestrator on `slacktest02` (`learn-f6c5af5c`, instance `i-089866f287f26b987`, profile `learn`, enforcement `both`). All sections passed; results recorded verbatim in `113-UAT.md`. Redaction spec-review signed off as verbatim.

## Task Commits

1. **Task 1: docs(113-03) — CLAUDE.md Phase 113 block + operational-gotchas.md entry** — `f977122a` (docs)
2. **Task 2: Live UAT (checkpoint) — verified end-to-end on slacktest02; Section C fix folded in** — `9e3918aa` (orchestrator, SKILL.md Section C fix) + `113-UAT.md` (this plan)

## Files Created/Modified

- `docs/operational-gotchas.md` — New "Sandbox self-awareness (Phase 113)" section (on-box profile semantics, six-section census table, deploy surface).
- `CLAUDE.md` — Phase 113 summary block (before Phase 111) + two Where-to-look rows.
- `113-UAT.md` — Live UAT results (verbatim), Section C defect log, redaction sign-off.
- `skills/sandbox/SKILL.md` — Section C network-census fix (committed `9e3918aa` by the orchestrator during UAT; not re-committed here).

## Deviations from Plan

### UAT-driven auto-fix (Task 2)

**1. [Rule 1 — Bug] Section C network census had four shipped bugs, exposed by live UAT**
- **Found during:** Task 2 (live UAT, Section C run on `slacktest02`).
- **Issue:** Four real bugs in `skills/sandbox/SKILL.md` Section C: (1) DNAT counter `grep -c DNAT || echo 0` double-emitted `0\n0` → `[: integer expression expected`; (2) enforcement parse `grep -A2 '^  network:'` too shallow, missed `enforcement:` below the `egress:` sub-block → wrongly reported default=proxy for an `enforcement: both` profile; (3) egress-allowlist parse over-scooped unrelated YAML list items (`allowedRegions`, `initCommands`, `trustedDirectories`); (4) the safe-active probe consequently picked `us-east-1` as the "allowed host" (meaningless curl).
- **Fix:** Bare `grep -c` + `${VAR:-0}`; match the unique `enforcement:` key directly; awk block-scoped to `allowedHosts`/`allowedDNSSuffixes`; pick a concrete non-wildcard host (fall back to `api.anthropic.com` on a wildcard-only allowlist); dropped the `|| echo 000` http_code double-emit. Also hardened proxy detection to use `HTTPS_PROXY`/injected-CA signals (no root needed) instead of the root-only iptables DNAT count.
- **Files modified:** `skills/sandbox/SKILL.md` (Section C).
- **Commit:** `9e3918aa` (orchestrator; already committed before this finalize — NOT re-committed by this plan).
- **Verification:** Section C re-UAT PASS — inferred mode `both` (no mismatch WARN), allowlist block-scoped, exactly two curls (ALLOWED `api.anthropic.com`→404, BLOCKED `evil.example.com`→000).
- **Plugin version:** stays `0.4.10` (the 113-02 unreleased bump; the fix rides it).

**Total deviations:** 1 UAT-driven auto-fix (Rule 1 — bug). No scope creep; confined to Section C of the skill.

## Redaction Spec-Review

Signed off as **write VERBATIM, no redaction.** The on-box profile carries SSM *paths*
(e.g. `allowedSecretPaths`), not secret values; the sandbox user already has IAM read on those
paths and can read everything `configFiles` materialize on disk. No embeddable-secret field
found. The CONTEXT.md default decision (verbatim) stands — no `configFiles`-body redaction gate
was added.

## Authentication Gates

None.

## Issues Encountered

The Section C bugs (above) were the only issue; found and fixed during live UAT, re-verified PASS.

## Next Phase Readiness

Phase 113 is complete and live-verified. Operators ship it via `make build-lambdas` +
`km init --dry-run=false` + plugin `0.4.10` bump; existing sandboxes gain the on-box file on
`km destroy && km create` and degrade gracefully until then.

## Self-Check: PASSED

- `113-UAT.md` exists: FOUND
- `113-03-SUMMARY.md` exists: FOUND
- Commit `f977122a` (Task 1 docs): FOUND
- Commit `9e3918aa` (Section C fix): FOUND
- `docs/operational-gotchas.md` has `/opt/km/.km-profile.yaml`: FOUND
- `CLAUDE.md` has `Phase 113`: FOUND

---
*Phase: 113-sandbox-self-awareness-on-box-profile-plus-capability-network-privilege-self-census-in-klanker-sandbox*
*Completed: 2026-06-14*
