---
phase: 70
plan: 09
uat_run_date: 2026-05-24
operator: KPH
codex_version: 0.133.0
km_version: v0.3.710
uat_ami: ami-0944742220403a527 (pinned via profiles/*.local.yaml — not committed)
verified: human_needed
sc_passed: [SC-1, SC-7]
sc_partial: [SC-4]
sc_code_landed: [SC-2]  # Plan 70-11 shipped; UAT pending
sc_deferred: [SC-5, SC-6, SC-8, SC-9, SC-10]
sc_dropped: [SC-3]
followups: [Plan 70-12 (UAT re-run on non-AMI sandboxes or after signing-key fix)]
plan_70_11_status: shipped (km v0.3.711)
---

# Phase 70 UAT — `70-VERIFY.md`

End-to-end verification of all 10 Success Criteria (minus SC-3, which Path B dropped). Operator-driven against two real EC2 sandboxes:

- **`learn-codex`** — from `profiles/learn.v2.codex.yaml` (spec.cli.agent: codex, Codex 0.133.0 baked in AMI)
- **`learn`** — from `profiles/learn.v2.yaml` (spec.cli.agent absent → defaults to claude)

Each flow records: **inputs**, **observed output**, **PASS/FAIL**, and **notes** (deviations, gotchas).

---

## Pre-flight

| Item | Expected | Observed | Status |
|---|---|---|---|
| `km init --sidecars` exit 0 | clean | | ⬜ |
| `km create learn-codex` reaches `running` | bootstrap complete | | ⬜ |
| `km create learn` reaches `running` | bootstrap complete | | ⬜ |
| `learn-codex`: `codex --version` | 0.133.0+ | | ⬜ |
| `learn-codex`: `codex exec --json --dangerously-bypass-approvals-and-sandbox "ping"` produces `turn.completed` | yes | | ⬜ |
| Both `#sb-learn-codex` and `#sb-learn` Slack channels created | yes (per-sandbox channels) | | ⬜ |

If pre-flight fails: do NOT proceed; investigate Codex auth / AMI bake / channel creation before running UAT flows.

---

## Flow 1 — SC-1: Codex sandbox provisioning ✅ PASS (2026-05-24)

**On `learncodex` (sandbox `learn-009e0e7b`, instance i-0a0fdceab2329d9a6), via SSM:**

**Observed:**
- `~/.codex/config.toml` exists: `-rw------- 1 sandbox sandbox 401` (Codex tightened mode to 0600 after login; harmless — the file is sandbox-user-only)
- Hook entries present:
  ```toml
  [[hooks.PermissionRequest]]
  matcher = ".*"
  [[hooks.PermissionRequest.hooks]]
  type = "command"
  command = "/opt/km/bin/km-notify-hook PermissionRequest"
  timeout = 30
  [[hooks.Stop]]
  [[hooks.Stop.hooks]]
  type = "command"
  command = "/opt/km/bin/km-notify-hook Stop"
  timeout = 30
  ```
- `/etc/profile.d/km-notify-env.sh` contains `export KM_AGENT="codex"` ✓
- `/etc/km/notify.env` contains `KM_AGENT=codex` ✓
- `codex --version` → `codex-cli 0.133.0` ✓

**Cosmetic deviation (NOT a SC-1 failure):**
The config.toml feature flag is `codex_hooks = true` (the OLD name). The km binary at `a1fb750` writes the new `hooks = true` name, but **Lambda's compiled userdata template is from v0.3.709** (pre-fix) and hasn't been refreshed via `km init --sidecars` since the fix landed. Codex 0.133 emits a deprecation event in the JSONL stream on every exec, which Plan 70-10's parser filters out via `select(.item.type=="agent_message")`. Functionally harmless. To clean up before future UAT runs: `./km init --sidecars` then destroy + recreate.

**Status:** ✅ PASS — all required artifacts present, KM_AGENT correctly emitted to both env files, Codex version meets ≥ 0.121.0 floor.

---

## Flow 2 — SC-2: Operator-side Codex run with idle notify ❌ FAIL (2026-05-24) — Path B gap, follow-up Plan 70-11

**From operator workstation:**
```bash
./km agent run --codex --prompt "What model are you? One short sentence only." --wait learncodex
```

**Observed (stdout JSONL stream):**
```
{"type":"thread.started","thread_id":"019e5abe-3038-7612-8bc0-0a37046d8534"}
{"type":"item.completed","item":{"id":"item_0","type":"error","message":"`[features].codex_hooks` is deprecated..."}}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"I'm Codex, a coding agent based on GPT-5."}}
{"type":"turn.completed","usage":{"input_tokens":10910,...}}
```

✓ Codex ran to completion.
✓ Agent reply was correctly captured to `/workspace/.km-agent/runs/20260524T160752Z/output.json`.
✗ Stop hook did NOT fire — `/opt/km/bin/km-notify-hook` access time remained at 14:56 (sandbox boot time), not 16:07 (agent run time).
✗ No operator email (expected — `notifyEmailEnabled: false`).
✗ No Slack post in `#sb-learncodex` for this turn.

**Root cause:** Codex 0.133's `[features].hooks` flag was promoted to stable, but the documented `[[hooks.Stop]]` TOML schema does NOT actually fire commands in the shipping CLI. Plan 70-03 added the Codex Stop branch + `last_assistant_message` fast-path to `km-notify-hook`, but Codex never invokes the hook script. Plan 70-10's Path B fixed the POLLER path (Flows 4-5 below) but did NOT fix the operator-side `km agent run --codex` path — that flow still relies on hooks to drive notify.

**Status:** ❌ FAIL — defer to gap-closure Plan 70-11 (add post-`codex exec` JSONL parse + synthetic `Stop` hook invocation to `internal/app/cmd/agent.go` BuildAgentShellCommands Codex branch). Estimated complexity: ~15 LOC in the bash wrapper that the agent dispatch builds.

---

## Flow 3 — SC-3: DROPPED under Path B

Codex under `--dangerously-bypass-approvals-and-sandbox` does NOT emit PermissionRequest events; tools execute without an approval gate. SC-3 was already moot. Document below if you want to capture the empirical confirmation:

```bash
./km agent run --codex --prompt "Write a file to /tmp/p70-uat-permission-test.txt with content hello" --wait learn-codex
km shell learn-codex
# inside: ls -la /tmp/p70-uat-permission-test.txt   # should exist, no approval prompt happened
```

**Observed:** (paste — file created, no permission notification fired)

**Status:** ⬜ N/A (dropped from Phase 70 scope) — record observation only

---

## Flow 4 — SC-4: Slack inbound, first Codex turn ⚠ PARTIAL (2026-05-24) — Path B mechanism PROVEN, Slack delivery blocked by AMI-bake regressions

**Operator action:** Posted "list workspace files"-like prompt as new top-level in `#sb-learncodex` (channel `C0B6Q14QVB2`).

**What the implementation did right:**

Phase 70's Path B mechanism is **end-to-end verified** in DDB:

```json
{
  "channel_id": "C0B6Q14QVB2",
  "thread_ts": "1779639669.879789",
  "agent_type": "codex",
  "claude_session_id": "019e5acc-b013-7443-99db-f13c2653520f",
  "last_assistant_msg": "Yes. I'm Codex, running as a coding agent in your workspace.\n",
  "last_turn_ts": "2026-05-24T16:23:52Z"
}
```

- ✅ Poller picked up SQS message
- ✅ Dispatched `codex exec --json --dangerously-bypass-approvals-and-sandbox` correctly
- ✅ **JSONL parse extracted `thread_id` from `thread.started` event** (Path B's session-ID source — Plan 70-10's exact contract)
- ✅ **JSONL parse extracted the LAST `agent_message.text`** as `last_assistant_msg` (Plan 70-10's exact extraction filter)
- ✅ DDB row written with `agent_type=codex`, `claude_session_id` populated from JSONL, `last_assistant_msg` populated from JSONL — agent-aware writeback (Plan 70-05 + 70-10)

**Why Slack reply didn't land in-thread (NOT a Phase 70 issue):**

Two AMI-bake regressions discovered during UAT — both orthogonal to Plan 70's implementation:

1. **Stale sandbox-id in 7 systemd unit files.** The AMI baked from bakesrc embedded `SANDBOX_ID=learn-3cad85fe` literally into `/etc/systemd/system/km-{slack-inbound-poller,audit-log,tracing,mail-poller,presence,queue,ebpf-enforcer}.service`. When learncodex booted from the AMI, cloud-init renamed the host but did NOT regenerate these unit files with the new sandbox-id (`learn-009e0e7b`). The poller polled the WRONG SQS queue for 34 minutes. Fixed in-place via SSM `sed -i 's/learn-3cad85fe/learn-009e0e7b/g' ...` + systemctl restart.
2. **Signing-keypair identity mismatch.** km-slack post returned `slack: bridge returned 401: {"error":"bad_signature","ok":false}`. The Lambda bridge has learncodex's public key registered (per `km status`: `Public Key: b0PwLsE079CatmL3...`), but the on-sandbox private key inherited from the AMI does not match. Phase 14 platform-identity keypair was AMI-baked from bakesrc instead of issued fresh at create.

Both are AMI-bake limitations: per-sandbox cryptographic + identifier state captured into snapshots that should have been regenerated at boot. They affect ANY sandbox derived from any operator-baked AMI — not specific to Phase 70.

**Recommended follow-ups (out of Phase 70 scope):**
- `km ami bake` should clean per-sandbox state before snapshotting (or sandbox userdata should regenerate it at every boot from IMDS / km-config).
- Phase 14 identity keypair should be re-issued + re-registered with the bridge on every userdata boot, not derived from AMI.
- File these as separate gap-closure phases (e.g., Phase 56.x or new phase).

**Status:** ⚠ PARTIAL PASS — Plan 70-10's Path B JSONL parsing contract is **fully validated** by the DDB row content; visible end-to-end Slack delivery blocked by AMI-bake regressions (logged for follow-up). Phase 70's code paths work as designed.

---

## Flow 5 — SC-5: Codex multi-turn resume

**In the same Slack thread from Flow 4 (reply):**
```
how many files were there?
```

**Expected:**
- Reply within ~30s, references the count from the prior turn (proves `codex exec resume <thread_id>` works)
- Same DDB row: `claude_session_id` unchanged, `last_assistant_msg` updated, `last_turn_ts` updated

**Observed:** (paste reply text + DDB attrs)

**Status:** ⬜ PASS / ⬜ FAIL

---

## Flow 6 — SC-6: KM_SLACK_THREAD_TS gating for Codex

This is implicit — Flow 2 above proves the operator-side `km agent run --codex` posts via the Stop-equivalent wrapper, and Flows 4-5 prove the poller-driven path doesn't double-post. If both passed, SC-6 passes.

**Status:** ⬜ PASS (derived from Flows 2+4+5) / ⬜ FAIL

---

## Flow 7 — SC-7: km doctor checks ✅ PASS (2026-05-24)

**From operator workstation:**
```bash
./km doctor 2>&1 | grep -E "codex|agent_type"
```

**Observed:**
```
✓ agent_type_consistency              1 thread row(s) consistent with profile agent_type
- codex_version_supports_jsonl        codex version check deps not configured
32 checks passed, 12 warnings, 0 errors
```

- ✅ `agent_type_consistency` PASS — the 1 DDB row written during Flow 4 is consistent with `learncodex`'s `agent: codex` profile
- ✅ `codex_version_supports_jsonl` SKIP (correctly) — `CodexSSMRunner` is nil because the org-level SCP blocks `ssm:SendCommand` on the application account. Matches Plan 70-07's documented design (`internal/app/cmd/doctor_codex.go` returns `CheckSkipped` on nil deps)

**Status:** ✅ PASS — both checks behave exactly as designed.

---

## Flow 8 — SC-8: Top-level prefix routing on a claude-default profile

**In Slack `#sb-learn` (claude-default) channel — NEW top-level post:**
```
codex: list workspace files
```

**Expected:**
- A new thread is born under that top-level message; the reply comes from Codex (not Claude)
- New DDB row in `km-slack-threads` for `(C_learn_channel, T_new_thread_ts)`:
  - `agent_type = "codex"` (even though profile default is claude)
- Operator posts a follow-up in the same thread (no prefix): Codex resumes with its `thread_id`

**Observed:** (paste DDB row, Slack thread permalink, follow-up evidence)

**Status:** ⬜ PASS / ⬜ FAIL

---

## Flow 9 — SC-9: Same-agent prefix is a no-op

**In the same Codex-rooted thread from Flow 8 (reply):**
```
codex: do another thing
```

**Expected:**
- Same thread, same `thread_id`, Codex resumes — NO new top-level message, NO handoff post, NO new DDB row
- The agent receives the prompt with the `codex:` prefix stripped

**Observed:**

**Status:** ⬜ PASS / ⬜ FAIL

---

## Flow 10 — SC-10: Cross-agent mid-thread switch

**Start a fresh Claude thread in `#sb-learn`** (new top-level, no prefix):
```
What is the project's primary value proposition?
```

After Claude replies, **in the same thread (reply):**
```
codex: check the answer
```

**Expected sequence (operator captures permalinks):**
1. In the OLD claude thread, the bot posts: `Switching to codex → continuing in this thread.\nhttps://<workspace>.slack.com/archives/<channel>/p<new_top_ts>`
2. A NEW top-level message appears in `#sb-learn`:
   ```
   Continuing from https://<workspace>.slack.com/archives/<channel>/p<old_thread_ts>

   Previous assistant (claude) said:
   > <up to 500 chars of Claude's prior reply>
   ```
3. Codex replies in this NEW thread, addressing "check the answer" with Claude's prior message as seeded context (Codex should reference the answer Claude gave)
4. DDB has TWO rows for `#sb-learn`'s channel — the original Claude row + a new Codex row keyed on the new `thread_ts`
5. Operator replies in the OLD thread WITHOUT a prefix → Claude resumes its prior session (proves OLD session not killed)

**Observed:** (paste both Slack permalinks, paste both DDB row attr summaries, paste the old-thread resume evidence)

**Status:** ⬜ PASS / ⬜ FAIL

---

## Summary (2026-05-24)

| SC | Description | Status | Notes |
|---|---|---|---|
| SC-1 | Codex sandbox provisioning + env emission | ✅ PASS | config.toml + KM_AGENT emitted to both env files; Codex 0.133.0 |
| SC-2 | Operator-side Codex run idle notify | ✅ CODE LANDED — UAT pending | **Plan 70-11 shipped 2026-05-24** (km v0.3.711) — `BuildAgentShellCommands` codex branch now parses `output.json` JSONL post-exec and synthesizes a Stop hook payload pipe to `/opt/km/bin/km-notify-hook`. 4/4 tests PASS. Visible end-to-end Slack delivery awaits signing-key fix on a fresh sandbox (out-of-scope AMI-bake regression). |
| SC-3 | PermissionRequest event | N/A | Dropped under Path B (Codex never emits under `--dangerously-bypass-approvals-and-sandbox`; verified empirically) |
| SC-4 | Slack inbound first Codex turn | ⚠ PARTIAL | **Path B JSONL parse mechanism FULLY VERIFIED via DDB row**: `agent_type=codex`, `claude_session_id` from `thread.started`, `last_assistant_msg` from last `agent_message.text`. Visible Slack delivery blocked by AMI-bake signing-key mismatch (NOT a Phase 70 issue) |
| SC-5 | Codex multi-turn resume | ⏭ DEFERRED | Same Slack delivery block. JSONL resume path is structurally identical to SC-4; would pass on a non-AMI-baked sandbox |
| SC-6 | KM_SLACK_THREAD_TS gating | ⏭ DEFERRED | Depends on Stop hook firing (blocked by Codex hook reality). Variable IS passed correctly (verified in journal); gating logic is moot when hook doesn't fire |
| SC-7 | km doctor checks green | ✅ PASS | `agent_type_consistency` green; `codex_version_supports_jsonl` correctly SKIPs under SCP nil-deps |
| SC-8 | Top-level prefix routing | ⏭ DEFERRED | Slack delivery block; prefix parser unit tests (Plan 70-06) cover the regex logic |
| SC-9 | Same-agent prefix is no-op | ⏭ DEFERRED | Same |
| SC-10 | Cross-agent mid-thread switch | ⏭ DEFERRED | Same; 8-step switch sequence unit-tested (Plan 70-06 `TestPoller_CrossAgentSwitch_OrderingFetchesOldPermalinkFirst`) |

**Overall outcome:** Phase 70's Path B implementation is **structurally proven** — DDB writeback under SC-4 conclusively demonstrates JSONL parsing for both session-ID (`thread_id` from `thread.started`) and last assistant message (LAST `item.completed` with `item.type=agent_message`). End-to-end Slack visible delivery requires either (a) fresh non-AMI sandboxes for clean signing keys, or (b) AMI-bake regeneration fixes — both orthogonal to Phase 70 scope.

### Follow-up plans needed before declaring Phase 70 done

| ID | Title | Status |
|---|---|---|
| **70-11** (Path B agent-run notify) | Add post-`codex exec` JSONL parse + synthetic `Stop` hook invocation to operator-side `km agent run --codex` shell wrapper | ✅ **SHIPPED 2026-05-24** — km v0.3.711; 4 codex shell tests PASS; `BuildAgentShellCommands` codex branch + bash block |
| **70-12** (UAT re-run on clean sandboxes) | Operator-driven UAT re-run on fresh non-AMI sandboxes to validate visible Slack delivery (SC-2/4/5/8/9/10) | ⏭ Pending operator — requires either fresh non-AMI sandboxes OR signing-key fix on existing learncodex |

### Out-of-scope issues discovered during UAT (file as separate phases)

| Issue | Recommended phase |
|---|---|
| AMI bake captures stale per-sandbox state (sandbox-id baked into 7 systemd unit files) | Phase 56.x (learn-mode AMI lifecycle) |
| Phase 14 identity ed25519 keypair AMI-bake regression (signing-key bridge mismatch) | New phase or Phase 56.x |
| Lambda userdata template not refreshed after `a1fb750` (still emits deprecated `codex_hooks` flag) | Quick task: `./km init --sidecars` after Phase 70 ships |

### Deviations from original spec

1. **Hook-based design (original) → JSONL stream parsing (Path B).** Adopted 2026-05-23 after Plan 70-00 spike v1/v2/v3 confirmed Codex 0.121/0.133 do not fire user-defined hooks despite stable `[features].hooks` flag. Spec updated; CONTEXT.md "Path B contract" section is the locked source of truth.
2. **SC-3 dropped.** Codex never emits PermissionRequest events under `--dangerously-bypass-approvals-and-sandbox`; the file got created without any approval gate during the v2 spike.
3. **SC-2 functionally regressed under Path B** — original spec assumed Stop hook would post; Path B redesign missed updating the operator-side `km agent run --codex` path. Captured as Plan 70-11.

**Verification status:** `human_needed` — Plan 70-11 must land + UAT re-run on clean sandboxes before Phase 70 can be marked `passed`.
