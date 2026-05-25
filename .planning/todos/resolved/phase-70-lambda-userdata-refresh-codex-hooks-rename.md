---
title: Phase 70 follow-up — `km init --sidecars` to push Plan 70-02 codex_hooks→hooks rename to Lambda
area: km-cli + lambda
created: 2026-05-24
origin: Phase 70 SC-1/SC-2 UAT 2026-05-24
---

### Problem
Commit `a1fb750` ("fix(70-02): rename codex_hooks → hooks") updated the userdata template's `~/.codex/config.toml` writer to emit the Codex 0.133+ feature flag name. The local km binary was rebuilt to v0.3.710/v0.3.711 with the fix, BUT the **`km-create-handler` Lambda** still has the older km binary baked into its `toolchain/km` deployment.

Result: when an operator does `km create --remote` (the default), the Lambda compiles userdata with the OLD km that writes `[features] codex_hooks = true`. Codex 0.133+ emits a deprecation event in the JSONL stream on every `codex exec`:

```
{"type":"item.completed","item":{"id":"item_0","type":"error","message":"`[features].codex_hooks` is deprecated. Use `[features].hooks` instead."}}
```

Plan 70-10's JSONL parser filters this out (selects only `item.type=agent_message`), so functionally harmless. But cosmetic noise in every operator's run output, and a forward-compat risk if Codex ever escalates the deprecation to an error.

### Fix
Single command, no code changes:
```bash
./km init --sidecars
```

That uploads the current local km binary to `s3://${KM_ARTIFACTS_BUCKET}/toolchain/km` and force-cold-starts the create-handler Lambda so it pulls the new toolchain on next invocation.

### Why this todo exists
We tried this during UAT but the AMI-baked sandboxes already had the stale config baked in, so it didn't observably help until we recreated sandboxes from non-AMI profiles. On a clean recreate after `km init --sidecars`, the new userdata is correct.

This todo is just a reminder: after merging the a1fb750 commit (already on main), run `km init --sidecars` to push to Lambda. Then file as done.

### Verification
After `km init --sidecars` + a fresh `km create`: the new sandbox's `~/.codex/config.toml` should contain `hooks = true` (not `codex_hooks = true`), and `codex exec --json` should NOT emit the deprecation event in the JSONL stream.

### Resolution (2026-05-24)
No code change needed — `a1fb750` already shipped the rename. Confirmed at `pkg/compiler/userdata.go:907` (`hooks = true`). KPH ran `./km init --sidecars` to push the rebuilt km binary into the create-handler Lambda's toolchain alongside the three other Phase 70 follow-up fixes (`15a5240` km-identities, `914f183` RESUME_ARG, `df00ebb` chat.getPermalink). That one deploy resolves all four todos.
