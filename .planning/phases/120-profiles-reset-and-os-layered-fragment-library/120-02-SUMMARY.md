---
phase: 120-profiles-reset-and-os-layered-fragment-library
plan: "02"
subsystem: profiles
tags: [profiles, fragment-library, composition, leaf-profiles]
dependency_graph:
  requires: ["120-01"]
  provides: ["profiles/learner.yaml", "profiles/desktop.yaml", "profiles/github.yaml", "profiles/h1.yaml"]
  affects: ["km validate", "km create", "profiles/base/**"]
tech_stack:
  added: []
  patterns: ["extends: multi-parent composition", "bool zero-value trap avoidance", "in-leaf network allowlist narrowing"]
key_files:
  created:
    - profiles/learner.yaml
    - profiles/desktop.yaml
    - profiles/github.yaml
    - profiles/h1.yaml
  modified: []
decisions:
  - "learner enables klanker plugin via base/plugin-klanker (intentional delta from learn.v2.yaml, documented in leaf header)"
  - "desktop uses narrow in-leaf network (not base/safenetwork) to avoid list-union broadening to '*'"
  - "h1 keeps tracing disabled in-leaf — does not extend base/sidecars-all"
  - "github and h1 use full base/toolchain-agents (single version-pin site; heavier than legacy but deliberate)"
  - "github and h1 both set email.enabled:true in-leaf to prevent Rule S5 WARN (both channels off)"
metrics:
  duration: "146s"
  completed_date: "2026-06-26"
  tasks_completed: 3
  files_changed: 4
---

# Phase 120 Plan 02: Composed Leaf Profiles Summary

**One-liner:** Four composed operator-facing leaves (learner/desktop/github/h1) extending Wave-1 OS+toolchain+plugin fragments, each km-validate clean with no WARN.

## What Was Built

Authored the 4 canonical operator-facing leaf profiles that replace the ~20-file `profiles/` inventory. Each leaf is a thin composition over the Plan 01 fragment library, with only leaf-specific fields (bool runtime, lifecycle, desktop block, secrets, notification routing) declared in-leaf.

### profiles/learner.yaml

Replaces `testdata/profiles/learn.v2.yaml`. Extends 12 fragments:
`[os/redhat, toolchain-agents, plugin-klanker, safenetwork, sidecars-all, observability-learn, budget-standard, artifacts-workspace, iam-us-east-1, agent-claude-all-tools, email-strict, slack-persandbox]`

Leaf-only fields: t3.2xlarge runtime, `spot:false/hibernation:false/mountEFS:true` (bool zero-value trap compliance), EFS `/shared` + `/data` volume, 8h/1h/stop lifecycle, `SANDBOX_MODE: learn-v2-direct-api`, github inbound enabled, `cli.noBedrock:true`.

**Intentional delta from learn.v2.yaml:** klanker plugin ENABLED (via `base/plugin-klanker` `enabledPlugins:true` configFile). The frozen `learn.v2.yaml` left it disabled only to protect the byte-identity fixture, now archived in `testdata/profiles/`. Documented in leaf header comment.

### profiles/desktop.yaml

Replaces `testdata/profiles/desktop.legacy.yaml`. Extends 9 fragments:
`[os/debian, toolchain-agents, plugin-klanker, sidecars-all, observability-learn, budget-standard, artifacts-workspace, iam-us-east-1, agent-claude-all-tools]`

Leaf-only fields: `spec.runtime.desktop` block (kiosk/firefox/1920x1080), `spot:false/hibernation:false`, t3.large/30GB, 4h/30m/stop lifecycle, narrow network allowlist in-leaf (NOT `base/safenetwork`), email-on/slack-off notification (avoids per-sandbox Slack WARN). First-boot network caveat header comment preserved.

### profiles/github.yaml

Replaces `testdata/profiles/github-review.yaml`. Extends 9 fragments:
`[os/redhat, toolchain-agents, plugin-klanker, sidecars-all, observability-learn, budget-standard, artifacts-workspace, iam-us-east-1, agent-claude-all-tools]`

Leaf-only fields: `spot:true`, t3.medium, 2h/20m/stop lifecycle, narrow network allowlist in-leaf, `notification.github.inbound.enabled:true`, `email.enabled:true` (prevents Rule S5), `secrets.sopsFile: github-review-secrets.enc.yaml`. Full toolchain (deliberate — single version-pin site), no `base/slack-persandbox` (avoids per-sandbox Slack WARN).

### profiles/h1.yaml

Replaces `testdata/profiles/h1-triage.yaml`. Extends 6 fragments:
`[os/redhat, toolchain-agents, plugin-klanker, observability-learn, iam-us-east-1, agent-claude-all-tools]`

Leaf-only fields: `spot:true`, t3.medium, 2h/20m/stop lifecycle, narrow network (`api.hackerone.com` + AWS + Anthropic), `sourceAccess.mode:none` (no repo clone), sidecars block in-leaf (tracing disabled — does NOT extend `base/sidecars-all`), `notification.h1.inbound.enabled:true`, `email.enabled:true`, `secrets.sopsFile: h1-triage-secrets.enc.yaml`.

## Verification

All 4 leaves validate clean:

```
learner: rc=0 clean
desktop: rc=0 clean
github:  rc=0 clean
h1:      rc=0 clean
```

Extends ordering verified — os/* fragment is first in each leaf's `extends:` list:
- learner: `base/os/redhat`
- desktop: `base/os/debian`
- github:  `base/os/redhat`
- h1:      `base/os/redhat`

## Deviations from Plan

None — plan executed exactly as written.

## Commits

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Author profiles/learner.yaml | 85731816 | profiles/learner.yaml |
| 2 | Author profiles/desktop.yaml | b0693970 | profiles/desktop.yaml |
| 3 | Author profiles/github.yaml + h1.yaml | ccbee338 | profiles/github.yaml, profiles/h1.yaml |

## Self-Check: PASSED

All 4 leaf files confirmed present on disk. All 3 task commits confirmed in git log.
