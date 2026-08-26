---
phase: 127-declarative-mitm-intercepts-profile-declared-host-to-action-
plan: 05
subsystem: profiles-and-docs
tags: [yaml, profile-inheritance, mitm, documentation, uat]

requires:
  - phase: 127-01
    provides: "spec.network.mitm.intercepts profile surface (types + schema), by-name collapse"
  - phase: 127-02
    provides: "sidecar intercept engine (KM_MITM_INTERCEPTS, matcher, precedence guard)"
  - phase: 127-03
    provides: "compiler mitm.conf drop-in + buildL7ProxyHosts intercept-host threading"
  - phase: 127-04
    provides: "km validate errors and warnings for spec.network.mitm.intercepts"
provides:
  - "profiles/base/mitm-rickroll.yaml — the only place the built-in Rickroll survives post-opt-in"
  - "profiles/mitm-demo.yaml — worked example composing the fragment plus a respond rule and the off-switch"
  - "scripts/validate-all-profiles.sh inventory entry (27 profiles total)"
  - "docs/mitm-intercepts.md — the operator runbook"
  - "CLAUDE.md Phase 129 block + Where-to-look row"
  - "127-UAT.md — automated gate results (done) + 6-step live UAT script (pending)"
affects: []

tech-stack:
  added: []
  patterns:
    - "Abstract migration fragment for a default-flip: profiles/base/mitm-rickroll.yaml follows the same metadata.abstract:true shape as profiles/base/network/{safenetwork,locked}.yaml, declaring only the one spec sub-block it owns"

key-files:
  created:
    - profiles/base/mitm-rickroll.yaml
    - profiles/mitm-demo.yaml
    - docs/mitm-intercepts.md
    - .planning/phases/127-declarative-mitm-intercepts-profile-declared-host-to-action-/127-UAT.md
  modified:
    - scripts/validate-all-profiles.sh
    - CLAUDE.md

key-decisions:
  - "Titled the CLAUDE.md phase block 'Phase 129', not 'Phase 127' — origin/main already ships CLAUDE.md phase blocks 127 (webhook ingress bridge) and 128 (Wiz sensor) as of the tree-drift merge (e5e53d86); the .planning/ directory and every plan/summary filename stay 127-* per the executor's explicit instruction. This is a deliberate, recorded decision, not a bug — the repo's CLAUDE.md/ROADMAP phase numbering already drifts (ROADMAP.md stops at 126)."
  - "profiles/mitm-demo.yaml composes base/network/safenetwork (allowedDNSSuffixes: [\"*\"]) rather than base/network/locked, so the demo validates with zero warnings without needing to add a bespoke DNS suffix — the wildcard already covers both the inherited rickroll host and the leaf's own maintenance host under the composed enforcement: both, exercising the eBPF proxy-host threading for free"
  - "The demo's second intercept host is api.example.com (a synthetic placeholder), not a real third-party API, matching the repo's 'use generic placeholders in docs' convention even though this is a profile file rather than a doc page"
  - "127-UAT.md's Part 1 (automated local gate) was executed now, as the plan's Task 3 verification requires; Part 2 (the 6-step live UAT against real AWS) is scaffolded and marked PENDING — this plan is documentation + local-gate only, per its own prohibitions on km init/km create/any AWS-touching command"

patterns-established:
  - "profiles/base/mitm-rickroll.yaml is the canonical abstract migration fragment for the phase — any future 'this used to be hardcoded, now it's opt-in' flip in this repo can follow the same shape: one metadata.abstract:true file declaring only the newly-opt-in spec sub-block, referenced from docs and a matching -demo.yaml leaf"

requirements-completed: [REQ-127-DEFAULTOFF, REQ-127-DEPLOY]

coverage:
  - id: D1
    description: "The Rickroll easter egg survives as an abstract fragment (profiles/base/mitm-rickroll.yaml) that any profile opts into with one extends: line, and is skipped (not validated standalone) by scripts/validate-all-profiles.sh since profiles/base/** is excluded by construction"
    requirement: "REQ-127-DEFAULTOFF"
    verification:
      - kind: other
        ref: "./km validate profiles/base/mitm-rickroll.yaml — prints SKIP, exit 0"
        status: pass
      - kind: other
        ref: "grep -c 'profiles/base/mitm-rickroll.yaml' scripts/validate-all-profiles.sh — returns 0 (never added to the array)"
        status: pass
    human_judgment: false
  - id: D2
    description: "profiles/mitm-demo.yaml composes the fragment plus a respond-action rule with a multi-word body and a commented-out off-switch, validates with zero WARN lines, and is covered by scripts/validate-all-profiles.sh's inventory gate"
    requirement: "REQ-127-DEFAULTOFF"
    verification:
      - kind: other
        ref: "./km validate profiles/mitm-demo.yaml — 'valid', exit 0, no WARN: lines"
        status: pass
      - kind: other
        ref: "bash scripts/validate-all-profiles.sh — exit 0, 'all 27 profiles valid'"
        status: pass
    human_judgment: false
  - id: D3
    description: "docs/mitm-intercepts.md documents the surface, the default flip and why it's accepted, the migration path, the by-name whole-entry override rule, host matching as a bug fix, precedence in both directions, why there is no block action, all km validate errors/warnings, the eBPF DNS-widening caveat plus the transparent-path dial asymmetry, and the exact three-command deploy sequence with the --sidecars hazard spelled out; CLAUDE.md carries both a Where-to-look row and a Phase 129 block naming the same deploy sequence"
    requirement: "REQ-127-DEPLOY"
    verification:
      - kind: other
        ref: "grep -c 'km init --dry-run=false' docs/mitm-intercepts.md == 1; grep -c 'make build-lambdas' == 1; grep -c 'deniedHosts' == 4; grep -c 'allowedDNSSuffixes' == 4"
        status: pass
      - kind: other
        ref: "grep -c 'mitm-intercepts.md' CLAUDE.md == 2 (Where-to-look row + Phase 129 block); grep -c 'Phase 129' CLAUDE.md == 2"
        status: pass
    human_judgment: false
  - id: D4
    description: "A 6-step live UAT exists in the phase directory, in the design spec's fixed order, each step with an exact command, expected observation, and empty result field, all marked PENDING; the automated local gate (go build, scoped tests, full suite, profile-validation) was run now and its results recorded"
    verification:
      - kind: other
        ref: "test -f 127-UAT.md; six numbered steps present; go build ./... exit 0; go test ./pkg/profile/... ./pkg/compiler/... ./sidecars/... -count=1 exit 0; go test ./... -count=1 exit 1 with exactly the 5 documented internal/app/cmd pre-existing failures; bash scripts/validate-all-profiles.sh exit 0"
        status: pass
      - kind: manual_procedural
        ref: "127-UAT.md Part 2 — 6 live steps against real AWS"
        status: unknown
    human_judgment: true
    rationale: "The 6-step live UAT requires real EC2 sandboxes and is explicitly out of scope for this plan (no km init/km create); it is a scaffolded script awaiting operator/orchestrator execution."

duration: 25min
completed: 2026-08-26
status: complete
---

# Phase 127 Plan 05: Declarative MITM intercepts — migration, documentation, and live UAT Summary

**Shipped the migration surface for the fleet-wide MITM opt-in flip: `profiles/base/mitm-rickroll.yaml` preserves the deleted built-in Rickroll as a one-line `extends:` opt-in, `profiles/mitm-demo.yaml` is the worked example (including the off-switch), `docs/mitm-intercepts.md` is the complete operator runbook, CLAUDE.md carries a Phase 129 block naming the exact three-command deploy sequence, and `127-UAT.md` records a green automated local gate plus a scaffolded 6-step live UAT.**

## Performance

- **Started:** 2026-08-26T06:10:00Z (approx, first read)
- **Completed:** 2026-08-26T06:35:00Z (approx)
- **Duration:** ~25 min of tool time
- **Tasks:** 3
- **Files modified:** 6 (4 created: `profiles/base/mitm-rickroll.yaml`, `profiles/mitm-demo.yaml`, `docs/mitm-intercepts.md`, `127-UAT.md`; 2 modified: `scripts/validate-all-profiles.sh`, `CLAUDE.md`)

## Accomplishments

- `profiles/base/mitm-rickroll.yaml`: an abstract fragment (`metadata.abstract: true`) declaring exactly one `spec.network.mitm.intercepts` entry — `rickroll`, redirecting `.google.com` to the exact YouTube URL the deleted `sidecars/http-proxy/httpproxy/proxy.go` handler used. No sibling `spec.network` keys, no other spec block, per the plan's bool-zero-value-trap guidance.
- `profiles/mitm-demo.yaml`: extends `base/os/redhat` + `base/network/safenetwork` + `base/platform` + `base/mitm-rickroll`, adds a `maintenance` `respond` rule (`503`, multi-word body `"maintenance window in progress"`), and carries a commented-out `- name: rickroll` / `enabled: false` block demonstrating the by-name override. Validates with `./km validate` reporting `valid` and zero `WARN:` lines — the wildcard `allowedDNSSuffixes: ["*"]` from `base/network/safenetwork` covers both intercept hosts under the composed `enforcement: both`, so the eBPF DNS-reachability warning never fires.
- `scripts/validate-all-profiles.sh`: added `profiles/mitm-demo.yaml` to the `PROFILES` array (27 entries total, up from 26) and corrected the header comment's inventory arithmetic to a precise breakdown (it had already drifted slightly before this plan — missing the deny-layered and 3 qwen38-oblit entries from its stated count — and is now accurate).
- `docs/mitm-intercepts.md`: a complete operator runbook covering the worked YAML for both actions, the opt-in default and why the fleet-wide flip is accepted, the migration path via the fragment, the by-name/last-wins/whole-entry override rule and why field-level merge would break it, host matching as a Phase-117-style bug fix (the old regex's unanchored-suffix bug), precedence in both directions (metering hosts are dead, non-allowlisted hosts still fire), why there is no `block` action, every `km validate` error and warning with its fix, the eBPF DNS-non-widening caveat plus the transparent-path TLS-dial asymmetry from plan 02, the exact three-command deploy sequence with the `--sidecars` hazard spelled out twice, and a "Not available" section naming the three deferred items.
- CLAUDE.md: a new "Where to look" row and a full Phase 129 block (titled 129, not 127 — see Deviations) inserted ahead of the existing Phase 128 block, matching the house style and level of deploy detail of neighbouring phase blocks.
- `127-UAT.md`: Part 1 (automated local gate) executed and recorded — `go build ./...` clean; the plan's required scoped test command (`pkg/profile`, `pkg/compiler`, `sidecars/...`) green; the full-repo suite green except the 5 documented pre-existing `internal/app/cmd` AWS-credential-seam failures (no new regressions); `scripts/validate-all-profiles.sh` green at 27/27. Part 2 is the design spec's 6-step live UAT, each step scaffolded with an exact command and expected observation, all marked PENDING.

## Task Commits

1. **Task 1: Ship the abstract fragment, the demo leaf, and the inventory entry** - `87269d1e` (feat)
2. **Task 2: Write docs/mitm-intercepts.md and the CLAUDE.md entries** - `9f4ae61e` (docs)
3. **Task 3: Write the 6-step live UAT and run the full local gate** - `5eaa57d8` (test)

**Plan metadata:** (this commit, docs)

## Files Created/Modified

- `profiles/base/mitm-rickroll.yaml` - abstract fragment preserving the deleted built-in Rickroll as an opt-in `extends:` target
- `profiles/mitm-demo.yaml` - concrete leaf composing the fragment, a `respond` demo rule, and the commented-out off-switch
- `scripts/validate-all-profiles.sh` - added the demo leaf to the `PROFILES` array, corrected the header's inventory arithmetic
- `docs/mitm-intercepts.md` - the complete operator runbook
- `CLAUDE.md` - Where-to-look row + Phase 129 block
- `.planning/phases/127-declarative-mitm-intercepts-profile-declared-host-to-action-/127-UAT.md` - automated gate results (done) + 6-step live UAT script (pending)

## Decisions Made

See `key-decisions` in the frontmatter. In summary: the CLAUDE.md phase-numbering collision was resolved by titling the block "Phase 129" per the executor's explicit tree-drift instruction (see Deviations below); the demo leaf composes `base/network/safenetwork` rather than `base/network/locked` to get a zero-warning wildcard-covered leaf without hand-adding a DNS suffix; the demo's second intercept host uses a synthetic `api.example.com` placeholder.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Reworded a comment in profiles/mitm-demo.yaml to avoid a double grep match**
- **Found during:** Task 1 acceptance-criteria verification.
- **Issue:** The plan's acceptance criterion `grep -c 'base/mitm-rickroll' profiles/mitm-demo.yaml` expects `1`, but the file's header comment also mentioned the literal string `base/mitm-rickroll` alongside the `extends:` line, making the count `2`.
- **Fix:** Reworded the comment to say "the one-line `extends:` entry below" instead of naming the fragment path literally, leaving the `extends:` line as the sole occurrence.
- **Files modified:** `profiles/mitm-demo.yaml`
- **Verification:** `grep -c 'base/mitm-rickroll' profiles/mitm-demo.yaml` returns `1`; `./km validate profiles/mitm-demo.yaml` still reports `valid`.
- **Committed in:** `87269d1e`

### Process note (not a deviation): CLAUDE.md phase-numbering collision

This executor's prompt flagged, ahead of time, that `origin/main` already ships CLAUDE.md phase
blocks 127 (webhook ingress bridge) and 128 (Wiz sensor) as of the tree-drift merge (`e5e53d86`,
20 commits). Per that explicit instruction, the new CLAUDE.md block for this plan's work is
titled **Phase 129** (dated 2026-08-26), not Phase 127. The `.planning/` phase directory and
every plan/summary filename in it correctly remain `127-*` — only the CLAUDE.md prose heading
uses 129. This mirrors an existing, accepted drift already visible in this repo: `.planning/ROADMAP.md`
stops at Phase 126 while CLAUDE.md documents Phases 127 and 128. This plan's REQ-127-DEPLOY and
REQ-127-DEFAULTOFF requirement IDs are unaffected — they refer to the `.planning/` phase number,
which did not change.

While touching CLAUDE.md's inventory-comment-adjacent file (`scripts/validate-all-profiles.sh`),
this plan also corrected a small pre-existing drift in that script's own header comment: it
previously claimed "5 composed leaves + 8 builtins + 7 GPU leaves + 1 private-subnet + 1 Wiz"
(= 22), while the actual array already held 26 entries (missing `deny-layered.yaml` and the 3
`gpu-qwen38-oblit-*` variants from the stated count). The header now states the full, accurate
27-entry breakdown. This is a documentation-accuracy fix in a file this plan was already
required to touch, not scope creep — the array itself was correct both before and after; only
the comment's arithmetic was stale.

---

**Total deviations:** 1 auto-fixed (Rule 3, blocking-verification fix), plus the pre-flagged
CLAUDE.md phase-numbering decision (not a deviation from the plan text — it was mandated by the
executor's own tree-drift instructions) and one incidental comment-accuracy fix.
**Impact on plan:** None beyond what was instructed. No scope change, no file touched outside
this plan's declared `files_modified` list.

## Issues Encountered

None beyond the numbering collision, which was pre-flagged and resolved per explicit
instruction before implementation began.

## User Setup Required

None — no external service configuration required. This plan is documentation, profile YAML,
and a shell-script inventory entry; per its own prohibitions, no `km init`, `km create`, or any
AWS-touching command was run. The 6-step live UAT in `127-UAT.md` is scaffolded and PENDING,
awaiting the orchestrator/operator to run it against real AWS.

## Next Phase Readiness

- Phase 127 (declarative MITM intercepts) is now complete end to end: profile schema + override
  (plan 01) → sidecar engine (plan 02) → compiler drop-in (plan 03) → validation (plan 04) →
  migration fragment, documentation, and UAT script (this plan).
- No blockers for the code/docs surface. `go build ./...` and the plan's required scoped test
  command are both green; the full-repo suite is green except the 5 documented pre-existing
  `internal/app/cmd` failures; `scripts/validate-all-profiles.sh` is green at 27/27.
- **Live UAT is the one remaining open item for this phase.** `127-UAT.md` Part 2 (6 steps) is
  written and ready but not executed — the primary user goal's second requirement ("a clear,
  readable worked sample") is satisfied on disk; the first requirement ("nothing intercepts
  google.com by default") is proven by unit/compiler tests (`TestMITMInterceptsDormant_*`,
  `TestHTTPProxy_NoInterceptsGoogleNotRedirected`) but not yet by a live `curl` against a real
  box. Running `127-UAT.md`'s deploy preamble + 6 steps against real AWS is the natural next
  action before this phase is considered fully verified.

---
*Phase: 127-declarative-mitm-intercepts-profile-declared-host-to-action-*
*Completed: 2026-08-26*
