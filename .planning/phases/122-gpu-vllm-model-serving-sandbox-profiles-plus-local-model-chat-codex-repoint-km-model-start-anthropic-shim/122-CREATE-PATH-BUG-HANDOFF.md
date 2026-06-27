# Phase 122 — create-path init-payload bug: cold-start handoff

**Status:** BLOCKER for Phase 122 GPU serving. Branch `phase-122-gpu-vllm-serving`.
**Date:** 2026-06-27. AWS: `AWS_PROFILE=klanker-application` (acct 052251888500, us-east-1).

## One-line bug

`km create --local` builds `/tmp/km-init.sh` (the boot init payload) containing
**only `base/userinit`'s 18 `initCommands`** — the leaf's own `initCommands` AND
every base's `initCommandsAppend` are **silently dropped**. Net effect: GPU profiles
never run their vLLM/Bifrost bring-up at boot, so the sandbox boots but serves nothing.

## Hard evidence (from the cpurig3 confirming recreate, sb-405b0001)

On the booted box, via SSM:
- `test -f /tmp/km-init.sh` → yes, SIZE=5222
- `grep -c '\[km-init\]' /tmp/km-init.sh` → **18** (exactly base/userinit's list: goose,
  claude-code@2.1.132, codex, nvm, `npx get-shit-done-cc`, herdr, plugin clone, …)
- `grep -c KM_VLLM_UNIT /tmp/km-init.sh` → **0**
- `grep -c VLLM_MODEL /tmp/km-init.sh` → **0**  (the LEAF's `initCommands` vllm.env — also gone)
- `grep -c bifrost-data /tmp/km-init.sh` → **0**
- Result: `systemctl is-active bifrost vllm` → inactive/inactive; `/etc/km/bifrost-data`
  absent. (Slack poller WAS active; OTEL/budget/slack-channel all landed — so MOST of
  the inheritance merge worked; only the InitCommands payload is wrong.)

## Repro (cheap, no GPU, ~12 min)

```
AWS_PROFILE=klanker-application ./km create profiles/gpu-rehearsal-cpu.yaml --alias rig --local   # t3.xlarge, vLLM masked, ~$0.16/hr
# wait ~10-12 min for cloud-init 'done', then via SSM on the instance:
grep -c '\[km-init\]' /tmp/km-init.sh        # = 18 (only base/userinit)
grep -c KM_VLLM_UNIT /tmp/km-init.sh          # = 0 (gpu/serve initCommandsAppend dropped)
grep -c VLLM_MODEL /tmp/km-init.sh            # = 0 (leaf initCommands dropped)
# then: ./km destroy <sb-id> --remote --yes
```
(SSM helper pattern used all session: `aws ssm send-command … AWS-RunShellScript`,
poll `get-command-invocation … --query Status` until Success.)

## Where to look (code)

- `internal/app/cmd/create.go:~1023` — `for _, cmd := range resolvedProfile.Spec.Execution.InitCommands { … }`
  builds km-init.sh. **Confirm `resolvedProfile` is the output of `profile.Resolve()`
  and that its `.Spec.Execution.InitCommands` ALREADY contains the leaf + gpu/serve
  entries at this point.** Strong suspicion the create-path uses a profile that is NOT
  the fully-merged one (or merges a different struct than it writes from).
- `internal/app/cmd/create.go:~2888` — `formatInitCommandLines(cmd)`: renders each cmd
  as `echo '[km-init] <cmd>'\n<cmd>\n`. Multi-line heredoc cmds survive this fine IF
  they reach it — but they don't (the list is already short upstream).
- `pkg/profile/inherit.go:97 Resolve(...)`, `:27 deepMerge(...)`, `:236 applyInitCommandsAppend(...)`
  — `applyInitCommandsAppend` (concat+dedup of initCommandsAppend onto initCommands)
  looks correct in isolation; `deepMerge` does concat+dedup for lists. So Resolve()
  itself probably produces the right InitCommands — meaning the bug is that create.go
  doesn't FEED km-init.sh from Resolve()'s output (or feeds a stale/partial profile).
- `pkg/compiler/userdata.go:4833-4845` — renders `.InitCommands` + `.InitScripts` into
  the bootstrap (downloads + runs km-init.sh in §7.5); `.ConfigFiles` in §7.6 AFTER.
  `.InitCommandsAppend` is NOT a userdata template field → it MUST be merged into
  InitCommands by Resolve() before compile. (Confirmed: grep userdata.go shows only
  `.InitCommands`/`.InitScripts`/`.ConfigFiles`.)

## Suggested first step

Add a debug print (or a unit test) of `resolvedProfile.Spec.Execution.InitCommands`
right before km-init.sh is written in create.go (~1023), running `km create --local
profiles/gpu-rehearsal-cpu.yaml --dry-run`-style or a Go test that calls the same
resolve. Expect to see ONLY base/userinit's 18 → confirms the create path's resolve is
the culprit (vs the compiler/userdata path which `km validate` exercises and which is
green). Then trace which profile object create.go resolves vs writes from. Likely a
one-spot fix (call/await `profile.Resolve()` and use ITS InitCommands).

Cross-check: does `km create --remote` (the default, via create-handler Lambda) have
the same bug, or only `--local`? Memory `project_remote_create_flattens_extends` says
remote uploads `yaml.Marshal(resolvedProfile)` (extends flattened) — so remote may be
FINE and only `--local` is broken. If so, the GPU UAT could even use `--remote` once
the toolchain is refreshed (`make build-lambdas` + `km init --dry-run=false`).

## What's already DONE (don't redo)

base/gpu/serve is fully corrected + the bring-up is the RIGHT structure (just blocked):
1. Bifrost install → `docker run maximhq/bifrost:v1.6.0` (no release binary).
2. Bifrost config → real `providers`-only schema (validated live vs Bedrock).
3. Boot-brick `iam.allowedSecretPaths:[/sandbox/*/secrets]` removed.
4. Bedrock IAM gap → design pivots to direct keys (claude-anthropic + gpt-frontier);
   Bedrock optional/keyless if the role is granted `bedrock:InvokeModel`.
5. #5/#6 rework: units+config+`chown 1000:1000` in initCommandsAppend; leaf vllm.env in
   initCommands. PROVEN manually on-box (chown→pull→start → gpt-oss-bedrock + claude-
   bedrock both returned via instance-role SigV4). Just doesn't auto-run due to THIS bug.

Model IDs (current, live on Bedrock here): `bedrock/openai.gpt-oss-120b` (DROP the
`-1:0` suffix), `bedrock/us.anthropic.claude-sonnet-4-6` / `…opus-4-8` (need `us.`
prefix). Codex knob: `localBaseURL: http://localhost:8001/openai/v1`, `wire_api=responses`.
Anthropic key parked at SSM `/km/secrets/anthropic-api-key` + SOPS `secrets/gpu.enc.yaml`;
OpenAI key (for gpt-frontier) to go at SSM `/km/secrets/openai-api-key`.

## After this bug is fixed

1. Confirming recreate of `gpu-rehearsal-cpu` → bifrost.service active + `:8001=200`
   automatically; then a real GPU run once the G-quota lands (request `d7fe8a96…`).
2. Fold the OpenAI key into `secrets/gpu.enc.yaml`; activate gpt-frontier.
3. Apply the latent km hardening: the secret-injection loop (`userdata.go:480`) should
   `|| true` so a bad allowed path soft-fails instead of bricking boot.

## Full detail

`122-BIFROST-VALIDATION.md` (gateway validation + all 6 bug write-ups + the BLOCKER
section). `122-UAT.md` (gate log + G-quota). `docs/gpu-model-serving.md` (operator guide).
Design spec: `docs/superpowers/specs/2026-06-27-gpu-vllm-serving-profiles-design.md`.
