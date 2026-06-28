---
created: 2026-06-27T21:00:00.000Z
title: km create --local drops leaf + base initCommands from the boot payload (km-init.sh)
area: cli
phase: 122
priority: blocker
files:
  - internal/app/cmd/create.go:1023
  - internal/app/cmd/create.go:2888
  - pkg/profile/inherit.go:97
  - pkg/profile/inherit.go:236
  - profiles/base/gpu/serve.yaml
handoff: .planning/phases/122-gpu-vllm-model-serving-sandbox-profiles-plus-local-model-chat-codex-repoint-km-model-start-anthropic-shim/122-CREATE-PATH-BUG-HANDOFF.md
---

## Problem

`km create --local` builds `/tmp/km-init.sh` (the boot init payload) with **only
`base/userinit`'s 18 `initCommands`** — the leaf's own `initCommands` AND every base's
`initCommandsAppend` are silently dropped. So composed GPU profiles
(`extends: […, base/gpu/serve]`) boot but never run their vLLM/Bifrost bring-up.

**Evidence** (cpurig3 confirming recreate, Phase 122): on the booted box,
`grep -c '\[km-init\]' /tmp/km-init.sh` = 18, but `grep -c KM_VLLM_UNIT` = 0 and
`grep -c VLLM_MODEL` = 0 (leaf's initCommands vllm.env also missing). Other merged
fields (Slack poller, OTEL, budget) land fine — so it's specific to the InitCommands
payload, not the whole resolve.

`applyInitCommandsAppend` (inherit.go:236) + `deepMerge` (inherit.go:27) look correct
in isolation, so `profile.Resolve()` likely produces the right InitCommands — the bug
is that **create.go doesn't write km-init.sh from `Resolve()`'s output** (uses a stale/
partial profile). First step: print `resolvedProfile.Spec.Execution.InitCommands` right
before km-init.sh is written (create.go ~1023); expect only the 18.

Also check whether `km create --remote` (the EC2 default, via the create-handler
Lambda) is affected or fine — memory `project_remote_create_flattens_extends` suggests
remote uploads `yaml.Marshal(resolvedProfile)` so remote may already be correct.

## Why it matters

BLOCKER for Phase 122 GPU model-serving. Everything else (gateway config, codex path)
is validated live on hardware; this is the last thing between us and a working GPU run.
Likely affects ANY composed profile that relies on a base's `initCommandsAppend` for
boot-time setup — so it may be a broader inheritance-vs-create correctness bug, not just
Phase 122.

## Repro / full context

See the handoff doc (frontmatter `handoff:`) for the exact repro, code map, hypotheses,
and the full list of what's already fixed (don't redo). Cheap repro: `km create
profiles/gpu-rehearsal-cpu.yaml --alias rig --local` (t3.xlarge, ~$0.16/hr, no GPU
quota), SSM-grep `/tmp/km-init.sh`, then `km destroy`.
