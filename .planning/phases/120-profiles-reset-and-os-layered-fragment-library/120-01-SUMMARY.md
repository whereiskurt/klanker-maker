---
phase: 120-profiles-reset-and-os-layered-fragment-library
plan: "01"
subsystem: profiles
tags: [profiles, fragments, os-layering, abstract, toolchain, slack, plugin]

requires:
  - phase: 117-composable-multi-parent-profile-inheritance
    provides: deepMerge / extends list / abstract fragment mechanics (metadata.abstract, concatDedup)

provides:
  - Five new abstract base fragments under profiles/base/ (os/redhat, os/debian, toolchain-agents, plugin-klanker, slack-persandbox)
  - Single version-pin site for claude-code@2.1.132 and codex rust-v0.133.0 in toolchain-agents.yaml
  - OS-layered fragment split: RH-family vs Debian-family bootstrap separated from toolchain
  - profiles/base/os/ subdirectory (new)

affects:
  - 120-02 (leaf authoring — extends these fragments)
  - 120-03 (test path updates — validates against leaves that use these fragments)
  - 120-04 (validate-all-profiles.sh rewrite — must skip base/os/*.yaml subdir)

tech-stack:
  added: []
  patterns:
    - "OS-layered fragment: only string spec.runtime.ami declared; no bool fields (spot/hibernation/mountEFS) to avoid zero-value trap"
    - "Single-pin-site: claude-code and codex version pins each appear exactly once across profiles/base/"
    - "initCommands ordering: list base/os/* FIRST in leaf extends so OS steps precede toolchain steps via concat-in-order"
    - "Mixed-mode settings.json: enabledPlugins in inlined configFile + agent.claude.tools.* synthesis coexist via Phase 92 mergeSynthesizedClaudeSettings"

key-files:
  created:
    - profiles/base/os/redhat.yaml
    - profiles/base/os/debian.yaml
    - profiles/base/toolchain-agents.yaml
    - profiles/base/plugin-klanker.yaml
    - profiles/base/slack-persandbox.yaml

key-decisions:
  - "OS fragments declare ONLY string spec.runtime.ami — no spot/hibernation/mountEFS (bool zero-value trap; leaf owns all bools)"
  - "toolchain-agents.yaml is the single version-pin site for claude-code@2.1.132 and codex rust-v0.133.0"
  - "plugin-klanker enables the klanker plugin (enabledPlugins settings.json) — intentional Phase 120 delta from frozen learn.v2.yaml which left it disabled to protect byte-identity fixture (now in testdata/)"
  - "slack-persandbox sets both perSandbox:true AND enabled:true together to avoid Rule S2/S3/S-channelname km validate WARNs"
  - "debian fragment carries only the cert-trust initCommands step; Ubuntu bootstrap handled at compiler userdata level (Phase 93 OS-aware stub)"

requirements-completed: [R2]

duration: 4min
completed: 2026-06-26
---

# Phase 120 Plan 01: OS-layered Fragment Library Summary

**Five new abstract base fragments establishing OS-layered toolchain split with single version-pin site for claude-code@2.1.132 and codex rust-v0.133.0**

## Performance

- **Duration:** ~4 min
- **Started:** 2026-06-26T04:40:07Z
- **Completed:** 2026-06-26T04:43:24Z
- **Tasks:** 3
- **Files created:** 5 (+ 1 new directory profiles/base/os/)

## Accomplishments

- Authored `profiles/base/os/redhat.yaml` with amazon-linux-2023 ami + 4 RH-specific initCommands (yum, crond, ca-bundle cert, SSL_CERT_FILE export) — bool zero-value trap avoided (ONLY string ami under spec.runtime)
- Authored `profiles/base/os/debian.yaml` with ubuntu-24.04 ami + single cert-trust initCommand (Ubuntu compiler stub handles the rest since Phase 93)
- Authored `profiles/base/toolchain-agents.yaml` as the single version-pin site: goose + claude-code@2.1.132 + codex rust-v0.133.0 + nvm/node22 + gsd + herdr; rsyncPaths + string-only env; no manual codex config.toml write; no plugin clone
- Authored `profiles/base/plugin-klanker.yaml` with known_marketplaces.json + installed_plugins one-liner + enabledPlugins settings.json (Phase 120 intentional delta — plugin enabled, not just installed)
- Authored `profiles/base/slack-persandbox.yaml` with perSandbox:true + enabled:true + channelName + archiveOnDestroy:false + inbound.enabled + invites (S2/S3/S-channelname WARN avoidance confirmed)
- All 5 fragments return `SKIP` from `km validate` with exit 0
- Single-pin-site invariant verified: claude-code@2.1.132 and rust-v0.133.0 each appear in exactly 1 file across profiles/base/

## Task Commits

1. **Task 1: OS fragments (redhat + debian)** - `1a6f9dd7` (feat)
2. **Task 2: toolchain-agents.yaml** - `767ca7ba` (feat)
3. **Task 3: plugin-klanker + slack-persandbox** - `9374abfb` (feat)

## Files Created

- `profiles/base/os/redhat.yaml` — RedHat-family OS layer: ami amazon-linux-2023 + yum + crond + RH cert-trust path + SSL_CERT_FILE export
- `profiles/base/os/debian.yaml` — Debian-family OS layer: ami ubuntu-24.04 + Debian cert-trust step (compiler stub handles apt/ForceIPv4/ssh.service/systemd-resolved)
- `profiles/base/toolchain-agents.yaml` — OS-agnostic agent toolchain: goose + claude-code@2.1.132 + codex rust-v0.133.0 + nvm/node22 + gsd + herdr; rsyncPaths; string-only env
- `profiles/base/plugin-klanker.yaml` — klanker plugin install + enabledPlugins settings.json (Phase 120 delta: enable, not just install)
- `profiles/base/slack-persandbox.yaml` — per-sandbox Slack notification block (perSandbox:true + enabled:true + invites)

## Decisions Made

- **Bool zero-value trap:** OS fragments declare ONLY `spec.runtime.ami` (string) — all bool fields (spot/hibernation/mountEFS) belong exclusively in the leaf. Verified no grep matches for `^\s+(spot|hibernation|mountEFS):` in either OS fragment.
- **Debian minimal:** Only the cert-trust initCommand in debian.yaml; the Phase 93 compiler userdata stub is OS-aware and handles apt-over-443/ForceIPv4/python3-awscli/ssh.service/systemd-resolved-stop without needing profile-level initCommands.
- **Plugin enable delta:** `plugin-klanker.yaml` enables the plugin via `enabledPlugins: {"klanker@klanker-maker": true}` in a configFiles settings.json. The frozen `learn.v2.yaml` left it disabled only to protect the byte-identity fixture. That fixture is now archived in `testdata/profiles/` and decoupled from live profiles — enabling is safe and intentional.
- **Slack WARN avoidance:** `slack-persandbox.yaml` sets both `perSandbox:true` AND `enabled:true` together. Any leaf that extends this fragment and then sets `notification.slack.enabled:false` would re-trigger Rule S2 — documented in the fragment comment.
- **goose OTEL line ordering:** The `printf > /etc/profile.d/km-zz-goose-otel.sh` line is in toolchain-agents (creates the file), while `echo export SSL_CERT_FILE >> /etc/profile.d/km-zz-goose-otel.sh` is in os/redhat (appends to it). The initCommands concat-in-order means the redhat lines precede toolchain lines ONLY when the leaf lists `base/os/redhat` before `base/toolchain-agents` in extends. This ordering constraint is documented in the fragment comments and the CONTEXT.md.

## Deviations from Plan

None - plan executed exactly as written.

Minor adjustment: the automated verify for Task 2 `! grep -q 'config.toml'` would have matched a comment in toolchain-agents.yaml that mentioned `config.toml` in a note about what was excluded. Updated the comment wording to avoid the false grep hit while preserving the semantic content.

## Issues Encountered

The Task 1 commit included previously-staged `git mv` operations from prior work on this branch (profile file moves: learn.v2.yaml variants, dc34, desktop, github-review, etc. moved to testdata/profiles/). These were already staged from earlier branch work and were committed alongside the OS fragments. The commit is semantically correct — the moves were intentional Phase 120 work; they simply happened to be pre-staged. The final state of profiles/ is correct: only base/, checks/, secrets/, and prompt files remain.

## Next Phase Readiness

- 5 abstract fragments ready for Plan 02 (leaf authoring — learner, desktop, github, h1)
- Plan 02 can directly extend these fragments in extends: lists
- Single-pin-site invariant established; Plan 02 leaves must NOT re-declare version pins
- `profiles/base/os/` subdir exists; Plan 04 must extend validate-all-profiles.sh skip loop to cover `base/os/*.yaml`

---
*Phase: 120-profiles-reset-and-os-layered-fragment-library*
*Completed: 2026-06-26*
