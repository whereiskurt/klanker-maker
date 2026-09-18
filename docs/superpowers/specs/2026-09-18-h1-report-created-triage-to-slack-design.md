# HackerOne `report_created` → silent triage → Slack (analyst-blessed)

**Date:** 2026-09-18
**Status:** approved design, not yet implemented
**Builds on:** Phase 103 (`km-h1-bridge`, `docs/h1-bridge.md`)

## 1. Goal

When a new report lands on a bug-bounty program, a sandbox agent runs the
operator's own triage skill *before* an analyst looks at it, and delivers the
result to Slack — **without touching the HackerOne report**. The analyst reads
it, opens the report, and "blesses" the triage by typing `@km /triage` as an
internal comment, which posts it to HackerOne through the existing
comment-keyword flow.

The triage logic itself is **operator-owned and out of scope**: an existing
skill on the box does the reading, assessment, PDF generation and Slack post.
km's job is only to (a) hand the agent the operator's prompt file on
`report_created`, and (b) guarantee nothing is written to HackerOne on that
path.

Only `report_created` triggers. HackerOne exposes 32 program webhook events
(`api.hackerone.com/webhooks`); the operator has decided the other 31 are
out of scope for now. Nothing here prevents adding them later — the `events:`
map already accepts any event name.

## 2. What already exists (and is reused unchanged)

| Leg | Where | Status |
|---|---|---|
| HMAC verify, GUID dedupe, program resolve, `events:` auto-triage trigger | `pkg/h1/bridge/webhook_handler.go` | shipped |
| Warm / cold / resume dispatch to the `h1-<handle>` sandbox | same, `dispatchTarget` | shipped |
| `km-h1-inbound-poller` runs the agent with the event prompt | `pkg/compiler/userdata.go:3053` | shipped |
| `km-h1 read --report N` (full report JSON) | `cmd/km-h1/main.go` | shipped |
| `km-slack post` / `km-slack upload --s3-key transcripts/<id>/…` | `cmd/km-slack` | shipped |
| Box may `s3:PutObject` under `transcripts/<sandbox-id>/*` | `infra/modules/ec2spot/v1.7.0` | shipped |
| Per-sandbox Slack channel `sb-h1-<handle>` | `profiles/base/slack-persandbox.yaml` | shipped |
| Analyst bless path: `@km /triage` internal comment → `triage` command → INTERNAL comment | Phase 103 comment-keyword flow | shipped |

## 3. What is wrong for this goal today

1. **Three sites push onto the H1 report on every auto-triage dispatch.**
   - `webhook_handler.go:377` posts a synchronous INTERNAL "On it — dispatched to a
     sandbox agent." comment.
   - The poller preamble (`userdata.go:~3245`) says *"Posting your response
     (REQUIRED) … Do NOT only print your answer — it is discarded unless you post it
     with km-h1."* An obedient agent will post.
   - The poller's Phase 106 resume hint (`userdata.go:~3397`) posts a `<details>🔧 Resume…</details>`
     internal comment on the first turn of every report; the codex-missing guard (`:~3286`)
     posts too. Both are poller-owned and must gate on `reply_mode`.
2. **The bridge has never received a real HackerOne delivery.** `103-CAPTURE/field-paths.md`
   is a synthetic fallback; UAT is `awaiting-operator`. The routing key —
   `data.report.relationships.program.data.attributes.handle` — was confirmed against the
   REST report object, not a webhook, and HackerOne's own webhook example shows
   `data.report.relationships = {reporter, severity}` with no `program`. If it is wrong,
   every event silent-drops with `program=""` and nothing records the real shape.

## 4. Design

### 4.1 `km-config.yaml` — two additive keys

```yaml
h1:
    bot_handle: "@km"
    default_profile: profiles/h1.yaml
    debug_capture: true                 # NEW (default false)
    programs:
        - handle: <program-handle>
          allow: [<analyst-usernames>]
          targets:
            - alias: h1-<program-handle>
              profile: profiles/h1.yaml
          events:
            report_created:
                prompt: '@prompts/h1-new-report.txt'   # operator's file, relative to km-config.yaml;
                                                       # read at km init, inlined into KM_H1_PROGRAMS,
                                                       # {{report_id}} {{title}} {{state}} {{program}} expanded
                reply: none               # NEW: none | internal (default internal)
          commands:
            triage:
                description: Post the triage assessment as an INTERNAL comment
                prompt: '@prompts/h1-bless.txt'        # operator's file
          default_command: triage
```

- **`h1.debug_capture`** (bool, install-wide). When true, the bridge writes every
  delivery — `{received_at, headers, body}` — to
  `s3://<artifacts>/h1-captures/<X-H1-Delivery guid>.json` as **step 0, before
  signature verification**, so a rejected or unparseable delivery is still captured.
  Fail-soft (a PutObject error logs and the request proceeds). Off by default; the
  operator turns it off once the real payload is pinned. The GUID is the filename so
  HackerOne redeliveries overwrite rather than accumulate; a missing GUID falls back
  to `<unix-nanos>.json`.
- **`events.<name>.reply`** (`""` | `internal` | `none`; per-event). `internal` (and
  absent) is today's behaviour. `none` means: no bridge ack, and the agent is told not
  to post to HackerOne. It is per-event on purpose — a comment-keyword trigger is a
  human typing `@km` and expecting a response; only the autonomous path goes silent.

Config plumbing: `config.H1Config` gains `DebugCapture bool` (`mapstructure:"debug_capture"`);
`config.H1EventEntry` gains `Reply string` (`mapstructure:"reply"`). Both ride inside the
existing single `v.UnmarshalKey("h1", …)` — no new merge-list entry is needed (the whole
`h1:` block is one entry). `init.go` exports `KM_H1_DEBUG_CAPTURE` and the `reply` field
travels inside the existing `KM_H1_PROGRAMS` JSON.

### 4.2 Bridge — `pkg/h1/bridge`, `cmd/km-h1-bridge`

- `resolve.EventEntry` gains `Reply string \`json:"reply,omitempty"\``.
- `H1Envelope` gains `ReplyMode string \`json:"reply_mode,omitempty"\`` — set to the
  matched event's `Reply` on the auto-triage path, empty on the comment path. Old
  envelopes decode as `""` (≡ internal).
- `Handle()` step 10: the ack is skipped when the auto-triage event's `Reply == "none"`.
  Comment-keyword dispatches are unaffected.
- New `WebhookHandler.Capture RawCapturer` (interface `Put(ctx, key string, body []byte) error`),
  nil ⇒ dormant. Called first in `Handle()`. `cmd/km-h1-bridge/main.go` wires an S3
  adapter when `KM_H1_DEBUG_CAPTURE=true`.
- Drop-path log lines (`no program config, silent drop`, `event not a trigger, dropping`)
  gain a `top_level_keys` attribute (the sorted keys of the JSON object and of `data`),
  so a wrong wrapper is diagnosable from CloudWatch even with capture off.
- `infra/modules/lambda-h1-bridge/v1.0.0` (edited in place, additive): new
  `debug_capture` bool var → env `KM_H1_DEBUG_CAPTURE`, and a count-gated
  `aws_iam_role_policy` granting `s3:PutObject` on
  `arn:aws:s3:::<artifacts>/h1-captures/*`. `infra/live/use1/lambda-h1-bridge/terragrunt.hcl`
  reads `get_env("KM_H1_DEBUG_CAPTURE", "false")`.

### 4.3 Poller — `pkg/compiler/userdata.go`

`km-h1-inbound-poller` reads `REPLY_MODE=$(jq -r '.reply_mode // empty')`. When it is
`none`, the preamble's *"--- Posting your response (REQUIRED) ---"* block is replaced by:

```
--- Posting your response ---
Do NOT post anything to HackerOne for this trigger (no km-h1 comment). Deliver your
output where the task below says. A human will decide whether it reaches HackerOne.
```

For `internal`/absent the rendered script is byte-identical to today, so the frozen
H1 byte-identity golden (`TestUserdataH1ByteIdentity`) stays untouched.

### 4.4 Sandbox — operator-owned prompt files, no km skill

Nothing new ships on the box. The operator's `report_created` prompt file names
their own triage skill, its output locations, and the Slack post; the bless-path
prompt (`commands.triage.prompt`) names how to post that output as an INTERNAL
comment. Both are ordinary `@file` prompts resolved by `ResolveH1EventPrompts` /
`PublishH1CommandsToSSM` at `km init` (relative to the directory containing
`km-config.yaml`; a missing file is a hard `km init` error).

Two things the operator's prompt must carry, because the poller's preamble no
longer does on `reply: none` (see 4.3): where the output goes, and that the agent
must not call `km-h1 comment`. Whatever the skill needs on the box (the skill
itself, PDF tooling) is installed by the operator's profile
(`initCommandsAppend` / plugin marketplace), exactly as for any other sandbox.

The shipped `profiles/prompts/h1.*.prompt.txt` files stay as examples.

### 4.5 Bless path — unchanged

Analyst types `@km /triage` as an internal comment → comment-keyword trigger → `allow`
gate → `triage` command → agent posts the INTERNAL comment. `reply: none` does not apply
(comment path). The bridge still acks with "On it" here, which is correct: a human asked.

## 5. Safety properties preserved

- Researcher-visible replies remain impossible on the auto-triage path
  (`ComputeReplyToResearcher` never sees a `/reply_to_researcher` intent there).
- `reply: none` only removes writes; it cannot widen anything.
- `debug_capture` writes to a km-owned bucket prefix nothing else reads; captures contain
  the full report body (vulnerability details) — treat the prefix as sensitive, same as
  `captures/`. Off by default.

## 6. Deploy surface

`make build` (config keys) → `make build-lambdas` (bridge + create-handler userdata) →
`km init --dry-run=false` (env block + IAM; **not** `--sidecars`). No plugin bump.
The existing `h1-<handle>` sandbox needs `km destroy && km create` to gain the new poller
(the `reply_mode` preamble switch). Prompt-file edits alone need only `km init --h1`. `km init --h1` is sufficient for a later `debug_capture` flip alone.

## 7. Tests

- `resolve_test`: `Reply` round-trips through `KM_H1_PROGRAMS` JSON.
- `webhook_handler_test`: auto-triage with `reply: none` ⇒ no `PostComment`, envelope
  `reply_mode:"none"`; with `internal`/absent ⇒ ack posted, `reply_mode` absent (byte-identical
  envelope JSON); comment path never sets `reply_mode`.
- `webhook_handler_test`: capture is called before verify and on a 401; capture error does
  not change the response.
- `userdata` render test: `reply_mode:none` swaps the preamble block; absent ⇒ identical text;
  `TestUserdataH1ByteIdentity` unchanged.
- `config_test`: `debug_capture` and `events.*.reply` decode from YAML (through the real
  loader, not struct literals — see `project_struct_level_tests_bypass_schema`).
- Live: first real `report_created` with `debug_capture: true` pins the wrapper and the
  program-handle path; `103-CAPTURE/field-paths.md` gets re-pinned from the capture.

## 8. Out of scope

The triage skill, its PDF generation and its Slack post (operator-owned, already
built); the other 31 events; a Lambda-direct Slack firehose (decided against — B was
chosen); per-report sandboxes; un-silencing the ack for comment triggers; any change
to `km-h1 state`.
