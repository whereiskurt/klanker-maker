---
phase: 113-sandbox-self-awareness-on-box-profile-plus-capability-network-privilege-self-census-in-klanker-sandbox
plan: 02
subsystem: skills
tags: [skill-rewrite, klanker-sandbox, self-census, plugin-version-bump, graceful-degradation]

# Dependency graph
requires:
  - phase: 113-01
    provides: /opt/km/.km-profile.yaml written at boot (now real, skill can cat it)
provides:
  - klanker:sandbox SKILL.md rewritten as six-section self-census (A-F)
  - Graceful fallback for pre-Phase-113 sandboxes (PROFILE_AVAILABLE=0 path)
  - plugin version 0.4.10 in plugin.json and marketplace.json (lockstep)
affects: [113-03, any phase adding skill content that bumps plugin version]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "PROFILE_AVAILABLE guard: every profile-derived line gated on PROFILE_AVAILABLE=0/1"
    - "Enforcement inference via EBPF_ACTIVE + IPTABLES_DNAT (no KM_ENFORCEMENT env var)"
    - "Exactly two safe-active curls (allowed + blocked) — labeled as the only traffic-generating step"
    - "sudo -n true cross-checked against spec.execution.privileged with WARN on mismatch"
    - "Plugin version lockstep: plugin.json .version == marketplace.json .plugins[0].version"

key-files:
  created:
    - .planning/phases/113-sandbox-self-awareness-on-box-profile-plus-capability-network-privilege-self-census-in-klanker-sandbox/113-02-SUMMARY.md
  modified:
    - skills/sandbox/SKILL.md
    - .claude-plugin/plugin.json
    - .claude-plugin/marketplace.json

key-decisions:
  - "PROFILE_AVAILABLE=0 path falls back to env-var census + live probes; never errors on missing file"
  - "Network section uses systemctl is-active km-ebpf-enforcer + iptables DNAT count to infer mode (no KM_ENFORCEMENT env exists on-box)"
  - "Exactly two safe-active curls: one allowed host (profile-declared or api.anthropic.com fallback), one known-blocked (evil.example.com) — labeled for km otel audit"
  - "sudo probe cross-checks spec.execution.privileged and WARNs on mismatch"
  - "klanker:slack cross-linked in Section E, not duplicated (locked decision in CONTEXT.md)"
  - "Plugin version 0.4.10: Phase 111 holds 0.4.9, so 113 takes the next free patch"

requirements-completed: []

# Metrics
duration: 2min
completed: 2026-06-14
---

# Phase 113 Plan 02: klanker:sandbox Self-Census Rewrite Summary

**`klanker:sandbox` is now a six-section self-census (A–F) grounded in `/opt/km/.km-profile.yaml` (written at boot by Plan 01) with graceful fallback for pre-Phase-113 boxes.**

## Performance

- **Duration:** ~2 min
- **Started:** 2026-06-14T17:14:38Z
- **Completed:** 2026-06-14T17:17:24Z
- **Tasks:** 2 of 2
- **Files modified:** 3

## Accomplishments

- Rewrote `skills/sandbox/SKILL.md` from 106-line "detect env + email tooling" scope into a 361-line structured six-section self-census
- Section A: Identity & Agent — KM_SANDBOX_ID guard, KM_AGENT default from both env and profile
- Section B: Capability Census — sidecar helper binary loop (`km-send`, `km-recv`, `km-slack`, `km-presence`, `km-github`, `km-h1`), email policy, Slack post-back env, inbound poller systemctl probes, runtime features from profile (VS Code, desktop, budget/TTL, storage)
- Section C: Network Position — enforcement mode inferred from `systemctl is-active km-ebpf-enforcer` + `iptables DNAT` count (no `KM_ENFORCEMENT` env var exists on-box); proxy/CA/eBPF passive signals; profile egress allowlist; exactly two safe-active curls labeled as the only traffic-generating step
- Section D: Privilege & Restrictions — `sudo -n true` probe cross-checked against `spec.execution.privileged` with WARN on mismatch; each restriction explained with WHY (no sudo = intentional, do not escalate; allowedRefs; secret paths; network egress)
- Section E: Slack Publish-Back Readiness — confirms `KM_NOTIFY_SLACK_ENABLED`/`KM_SLACK_CHANNEL_ID`/`KM_SLACK_BRIDGE_URL`/`KM_SLACK_THREAD_TS`, cross-links `klanker:slack` with no duplication
- Section F: Self-Diagnosis Summary — posture statement template with filled-in example
- Graceful fallback preamble: `PROFILE_AVAILABLE=0` path never errors on absent `/opt/km/.km-profile.yaml`; degrades to env-var census + live probes
- Preserved all existing guidance: signing key check, `km-send --no-sign` for external email, `KM-AUTH` inbound filter
- Bumped plugin version to `0.4.10` in both `.claude-plugin/plugin.json` and `.claude-plugin/marketplace.json` in lockstep (Phase 111 holds 0.4.9; 113 takes the next free patch)

## Task Commits

1. **Task 1: Rewrite SKILL.md into the six-section self-census** — `a8dc5737` (feat)
2. **Task 2: Bump plugin version to 0.4.10 in plugin.json + marketplace.json** — `bc6cdd3e` (chore)

## Files Created/Modified

- `skills/sandbox/SKILL.md` — Rewritten: 106 lines → 361 lines; six sections A–F; PROFILE_AVAILABLE guard; enforcement inference; two safe-active curls; sudo cross-check; klanker:slack cross-link
- `.claude-plugin/plugin.json` — `.version` bumped from `0.4.9` to `0.4.10`
- `.claude-plugin/marketplace.json` — `.plugins[0].version` bumped from `0.4.9` to `0.4.10`

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- `skills/sandbox/SKILL.md` exists: FOUND
- `a8dc5737` commit exists: FOUND
- `bc6cdd3e` commit exists: FOUND
- `skills/slack/SKILL.md` unchanged: CONFIRMED (not in git diff)
- All 7 automated grep checks: PASSED
- Version match check (plugin.json == marketplace.json == 0.4.10): PASSED
- No redaction logic in skill: PASSED
