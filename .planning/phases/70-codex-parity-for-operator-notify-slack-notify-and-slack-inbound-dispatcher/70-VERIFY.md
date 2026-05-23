---
phase: 70
plan: 09
uat_run_date: TBD
operator: KPH
codex_version: TBD (expected: 0.133.0+)
km_version: v0.3.709
verified: false
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

## Flow 1 — SC-1: Codex sandbox provisioning

**On `learn-codex`:**
```bash
km shell learn-codex
# inside:
ls -la ~/.codex/config.toml
cat /etc/profile.d/km-notify-env.sh | grep KM_AGENT
cat /etc/km/notify.env | grep KM_AGENT
```

**Expected:**
- `~/.codex/config.toml` exists, owned by sandbox:sandbox, mode 0644 (no-op artifact under Codex 0.133 but file present)
- `/etc/profile.d/km-notify-env.sh` contains `export KM_AGENT="codex"`
- `/etc/km/notify.env` contains `KM_AGENT="codex"`

**Observed:** (paste)

**Status:** ⬜ PASS / ⬜ FAIL

---

## Flow 2 — SC-2: Operator-side Codex run with idle notify

**From operator workstation:**
```bash
./km agent run --codex --prompt "What model are you?" --wait learn-codex
```

**Expected:**
- Operator email arrives in inbox with subject containing `[learn-codex]` and body containing Codex's model name
- Slack `#sb-learn-codex` shows the same body in a new top-level thread (no agent-run thread, this is the Stop-equivalent idle notify post)
- Mechanism: poller-less `km agent run` invokes Codex with `--json`; the wrapper parses the JSONL stream and posts the last `agent_message.text` (no hook fired, JSONL parse is the contract)

**Observed:** (paste email subject + body excerpt; paste Slack screenshot or message permalink)

**Status:** ⬜ PASS / ⬜ FAIL

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

## Flow 4 — SC-4: Slack inbound, first Codex turn

**In Slack `#sb-learn-codex` channel (new top-level post):**
```
list workspace files
```

**Expected:**
- Within ~30s, a thread reply lands from the bot containing Codex's response
- `km status learn-codex` reports active inbound thread count = 1
- DDB row in `km-slack-threads` keyed on `(C_channel_id, T_thread_ts)`:
  - `agent_type = "codex"`
  - `claude_session_id = <UUID>` (this is Codex's `thread_id` — column-name hangover documented)
  - `last_assistant_msg = "<truncated reply>"`

**To verify DDB:**
```bash
aws --profile klanker-application --region us-east-1 dynamodb scan --table-name km-slack-threads --max-items 5 | jq '.Items[] | {channel_id, thread_ts, agent_type, claude_session_id: (.claude_session_id.S // empty)[:20], last_assistant_msg: (.last_assistant_msg.S // empty)[:80]}'
```

**Observed:** (paste DDB row attrs, Slack thread permalink)

**Status:** ⬜ PASS / ⬜ FAIL

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

## Flow 7 — SC-7: km doctor checks

**From operator workstation:**
```bash
./km doctor 2>&1 | grep -E "codex_version_supports_jsonl|agent_type_consistency"
```

**Expected:**
- `codex_version_supports_jsonl` → PASS or WARN (PASS if SSM-probe succeeded; WARN if blocked by SCP — both acceptable per Path B)
- `agent_type_consistency` → PASS (no drift between DDB rows and S3 profiles)

**Observed:** (paste km doctor output)

**Status:** ⬜ PASS / ⬜ FAIL

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

## Summary

| SC | Description | Status |
|---|---|---|
| SC-1 | Codex sandbox provisioning + env emission | ⬜ |
| SC-2 | Operator-side Codex run idle notify (JSONL parse) | ⬜ |
| SC-3 | PermissionRequest event — DROPPED (Path B) | N/A |
| SC-4 | Slack inbound first Codex turn | ⬜ |
| SC-5 | Codex multi-turn resume | ⬜ |
| SC-6 | KM_SLACK_THREAD_TS gating | ⬜ |
| SC-7 | km doctor checks green | ⬜ |
| SC-8 | Top-level prefix routing | ⬜ |
| SC-9 | Same-agent prefix is no-op | ⬜ |
| SC-10 | Cross-agent mid-thread switch with handoff | ⬜ |

**Overall:** ⬜ PASS / ⬜ FAIL — record any deviations from spec for follow-up plans

**Notes / deviations:**

(operator free-form notes)
