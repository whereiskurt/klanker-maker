---
phase: 93-km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward
plan: "06"
subsystem: docs-skill-profile
tags: [desktop, kasmvnc, profile, skill, docs, kiosk, xfce, operator-ux]
dependency_graph:
  requires: [93-02, 93-05]
  provides: [profiles/desktop.yaml, skills/desktop/SKILL.md, docs/desktop.md]
  affects: [scripts/validate-all-profiles.sh, CLAUDE.md, OPERATOR-GUIDE.md, .claude-plugin/plugin.json, .claude-plugin/marketplace.json]
tech_stack:
  added: []
  patterns: [skill-mirroring-vscode, lockstep-plugin-version-bump, profile-inventory-gate]
key_files:
  created:
    - profiles/desktop.yaml
    - skills/desktop/SKILL.md
    - docs/desktop.md
  modified:
    - scripts/validate-all-profiles.sh
    - .claude-plugin/plugin.json
    - .claude-plugin/marketplace.json
    - CLAUDE.md
    - OPERATOR-GUIDE.md
decisions:
  - "profiles/desktop.yaml uses apiVersion v1alpha2, spec.runtime.ami: ubuntu-24.04, spec.runtime.desktop.enabled: true with kiosk mode and [firefox] browsers"
  - "Plugin version bumped 0.3.0 → 0.4.0 in lockstep across plugin.json + marketplace.json per the cache-gate constraint"
  - "OPERATOR-GUIDE.md section numbered 10 (appended after additionalSnapshots section 9)"
metrics:
  duration: "363s"
  completed_date: "2026-06-02"
  tasks_completed: 3
  files_changed: 8
---

# Phase 93 Plan 06: Operator Deliverables — profile, skill, docs Summary

**One-liner:** kiosk-Firefox desktop profile (v1alpha2, ubuntu-24.04) + klanker:desktop skill (plugin 0.4.0 lockstep) + docs/desktop.md runbook + CLAUDE.md/OPERATOR-GUIDE.md updates, all verified by validate-all-profiles gate.

## Tasks Completed

| # | Task | Commit | Key files |
|---|------|--------|-----------|
| 1 | profiles/desktop.yaml + inventory gate | b4867c0d | profiles/desktop.yaml, scripts/validate-all-profiles.sh |
| 2 | klanker:desktop skill + plugin/marketplace version bump | 6e234b4a | skills/desktop/SKILL.md, .claude-plugin/plugin.json, .claude-plugin/marketplace.json |
| 3 | docs/desktop.md + CLAUDE.md row + OPERATOR-GUIDE.md section | 7f21ed21 | docs/desktop.md, CLAUDE.md, OPERATOR-GUIDE.md |

## Decisions Made

- **Profile posture**: minimal kiosk-Firefox example with `spec.network.enforcement: proxy` and Firefox-oriented egress allowlist; `spec.notification` both disabled (generates only a warning, not an error — expected for a minimal kiosk profile)
- **metadata.description**: removed after `km validate` rejected it as an additional property not in the schema
- **sourceAccess required**: added `mode: allowlist` with empty allowedRepos/allowedRefs (required field per schema)
- **allowedHosts required**: added empty array (required alongside allowedDNSSuffixes by schema)
- **Plugin version 0.3.0 → 0.4.0**: bumped in lockstep per the cache-gate memory entry; description strings updated in both files to mention desktop skill
- **OPERATOR-GUIDE.md section 10**: appended after the existing additionalSnapshots section 9 — same structure (when to use, schema, validation table, operator workflow, credential lifecycle, AMI-bake, network requirements, security model, rollout, failure mode reference)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] metadata.description field not in schema**
- **Found during:** Task 1, first validate run
- **Issue:** Profile had `metadata.description:` — not an allowed field per the JSON schema
- **Fix:** Removed the field; moved the description into a comment at the top of the file
- **Files modified:** profiles/desktop.yaml
- **Commit:** b4867c0d (in same task)

**2. [Rule 2 - Missing required field] spec.sourceAccess required**
- **Found during:** Task 1, first validate run
- **Issue:** Schema requires `spec.sourceAccess` — omitting it is a hard validation error
- **Fix:** Added `sourceAccess.mode: allowlist` with empty `allowedRepos` and `allowedRefs` arrays
- **Files modified:** profiles/desktop.yaml
- **Commit:** b4867c0d (in same task)

**3. [Rule 2 - Missing required field] spec.network.egress.allowedHosts required**
- **Found during:** Task 1, first validate run
- **Issue:** Schema requires `allowedHosts` alongside `allowedDNSSuffixes`
- **Fix:** Added `allowedHosts: []` (empty — kiosk Firefox example has no explicit host allowances)
- **Files modified:** profiles/desktop.yaml
- **Commit:** b4867c0d (in same task)

## Verification Results

```
validate-all-profiles: all 21 profiles valid
```

- `make build && bash scripts/validate-all-profiles.sh` exits 0 with desktop.yaml included
- `./km validate profiles/desktop.yaml` passes (with expected WARN: both notification channels disabled)
- `test -f skills/desktop/SKILL.md` — present
- `test -f docs/desktop.md` — present
- Plugin versions in lockstep: both 0.4.0 (bumped from 0.3.0)
- `grep -q "km desktop" docs/desktop.md` — matches
- `grep -q "desktop" CLAUDE.md` — matches (Where-to-look row + CLI entries)
- `grep -qi "km desktop" OPERATOR-GUIDE.md` — matches (section 10)

## Self-Check: PASSED

Files created:
- profiles/desktop.yaml: FOUND
- skills/desktop/SKILL.md: FOUND
- docs/desktop.md: FOUND

Commits:
- b4867c0d: FOUND
- 6e234b4a: FOUND
- 7f21ed21: FOUND
