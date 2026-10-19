# Thread session state leaves the sandbox role: local files + one bridge action — design

**Date:** 2026-10-18. **Status:** approved in chat (approach 1 of 2; design sections approved).
**Branch:** `feat/thread-session-bridge-action`. **Predecessor:**
`docs/superpowers/specs/2026-10-17-sandbox-role-least-privilege-design.md` (§8 follow-up; its §9 UAT
records the residual this spec closes). **Canary:** `ir-bot`.

## 1. Problem

After ec2spot v1.8.0 the sandbox role may `GetItem`/`UpdateItem` the three threads tables
(`{p}-slack-threads`, `{p}-github-threads`, `{p}-h1-threads`) for a named attribute set that
excludes `sandbox_id`. That closed the forged-row hole but left a residual: the grant is
table-wide, so a box can update the session attributes of ANY thread given its key. Two concrete
abuses: a poisoned `last_assistant_msg` is pasted into the victim box's Phase 70 handoff seed prompt
(cross-sandbox prompt injection); a large `last_processed_event_ts` makes the victim's poller skip
every later message through the Phase 119 idempotency guard. Both pre-existed under the old
Allow-star; the goal here is a role with **no access to the threads tables at all**.

## 2. What the pollers write today, and who reads it

| Attribute | Written by | Read by |
|---|---|---|
| `claude_session_id` (Slack) / `agent_session_id` (GitHub, H1) | poller, end of turn | the same poller next turn (`--resume`); **Slack bridge** (`LookupBySession` GSI behind `km-slack reply`'s session-aware thread lookup, Phase 110); GitHub/H1 bridges read it in `LookupSandbox` but no consumer uses the value |
| `agent_type` | poller | the same poller; **GitHub/H1 bridges** (`/claude`, `/codex` verb precedence, Phase 102); Slack bridge returns it from `LookupBySession` |
| `last_assistant_msg` | Slack poller | the same poller only (handoff seed) |
| `last_turn_ts`, `ttl_expiry` | poller | nobody / DynamoDB TTL |
| `last_processed_event_ts` | Slack poller | the same poller only (crash-redelivery dedup) |

Only the session id and `agent_type` leave the box. Everything else is box-private state that lived
in DynamoDB for convenience.

## 3. Decisions

| Decision | Choice | Why |
|---|---|---|
| Box-private state | **Local JSON file per thread**, `/var/lib/km/sessions/<source>/<key>.json`, root-owned `0700` dir (the pollers run as root), fields `session_id`, `agent_type`, `last_assistant_msg`, `last_processed_event_ts`, `updated_at`. `<key>` = Slack `<channel>_<thread_ts>`, GitHub `<owner>_<repo>_<number>` (slashes → `_`), H1 `<report_id>_<target>`. | Survives `km stop`/`resume` (same disk); gone on recreate, which is correct because the agent's own session files are gone too. The prompt-injection channel disappears instead of being gated. No read of any table remains in a poller. |
| Bridge-needed state | **One signed action `session`** on the Slack bridge's existing sandbox-facing endpoint (Ed25519 envelope, nonce replay store, same error vocabulary), called by all three pollers via a new `km-slack session` verb. Payload: `source` ∈ `slack|github|h1`, the thread key, `session_id`, `agent_type`. | The Slack bridge is already the only sandbox-facing API with signature verification; the GitHub and H1 bridges are webhook-only. One verb, one handler. |
| Authorization of the write | The bridge loads the row and requires `row.sandbox_id == env.SenderID`; a missing row is **`404 thread_unknown`, never created**; a mismatch is `403 thread_not_owned`. It then SETs the session id attribute (`claude_session_id` for Slack, `agent_session_id` for GitHub/H1), `agent_type`, `last_turn_ts`, `ttl_expiry`. Empty `session_id` clears the attribute (`REMOVE`), replacing the pollers' stale-session `REMOVE` path. | Rows exist only where the bridge dispatched; a box can only touch its own threads. |
| The Phase 70 handoff thread | `km-slack post --claim-thread`: the envelope gains `claim_thread bool` (`omitempty`, load-bearing for signing compatibility like `target_user`); after a successful top-level post the bridge `Upsert`s `{channel, ts, sandbox_id = SenderID}` for the message it just created. Only the handoff block in the Slack poller passes it. | The handoff's new thread has no row until the first dispatched reply; without a row the `session` write would 404. The bridge posted the message itself, so it knows exactly who owns the new thread. |
| Role | New immutable `ec2spot/v1.9.0`: `ec2spot_threads_rw` deleted; nothing else changes. Pin bumped at the three sites. | The threads tables leave the sandbox role entirely. |
| Slack bridge IAM/env | `lambda-slack-bridge` gains `dynamodb:GetItem`/`UpdateItem` on `{p}-github-threads` and `{p}-h1-threads` and env `KM_GITHUB_THREADS_TABLE`, `KM_H1_THREADS_TABLE` (edited in place, additive, like the Phase 100 pattern). | The `session` action writes those tables on the pollers' behalf. |
| GitHub/H1 bridge code | Unchanged. Their `LookupSandbox`/`UpdateSession` stay; `agent_type` keeps arriving through the new action. | Nothing they do moves. |
| Half-deploys | New poller + old bridge ⇒ `400 unknown_action` logged, turn completes, `km-slack reply` session lookup stale for that thread until the bridge deploys. Old poller + new bridge ⇒ unchanged (old boxes keep their v1.8.0 role until recreate). New bridge + old `km-slack` ⇒ no `claim_thread` field ⇒ bridge behaves as today. | Fail-soft in every direction; nothing dangerous. |

## 4. Components

### 4.1 `pkg/slack/payload.go`
- `ActionSession = "session"`.
- `SlackEnvelope` gains `Source string json:"source,omitempty"`, `Repo string json:"repo,omitempty"`,
  `Number int json:"number,omitempty"`, `ReportID string json:"report_id,omitempty"`,
  `Target string json:"target,omitempty"`, `AgentType string json:"agent_type,omitempty"`,
  `ClaimThread bool json:"claim_thread,omitempty"`. `SessionID` and `Channel`/`ThreadTS` are reused.
  Every new field is `omitempty` so envelopes from older `km-slack` builds canonicalise to the same
  bytes (the bridge re-canonicalises before verifying; see the `TargetUser` comment).

### 4.2 `cmd/km-slack/session.go` — `km-slack session`
```
km-slack session --source slack  --channel C… --thread TS        --session-id ID --agent claude|codex
km-slack session --source github --repo owner/name --number N     --session-id ID --agent …
km-slack session --source h1     --report R --target T           --session-id ID --agent …
```
`--session-id ""` (or `--clear`) clears. Prints `km-slack: session recorded` on 200; non-zero exit
with the bridge's error code on anything else. Reads the signing key and bridge URL exactly as `dm`
does (`runDMWith` is the template). Missing/invalid flags are rejected at parse time.

### 4.3 `pkg/slack/bridge/handler_session.go` — `handleSession`
- `Handle` admits `ActionSession` with `Channel` not required (like `dm`/`lookup-thread`); the
  per-source key completeness is validated in the handler (`400 missing_fields`).
- `source=slack`: `Threads.LookupSandbox(channel, thread)`; `""` ⇒ `404 thread_unknown`; `≠
  SenderID` ⇒ `403 thread_not_owned`; else `Threads.SetSession(channel, thread, sessionID, agentType)`
  (new interface method: `UpdateItem SET claude_session_id, agent_type, last_turn_ts, ttl_expiry`, or
  `REMOVE claude_session_id` when empty; never touches `sandbox_id`, `last_dispatch_ts`,
  `created_at`, `turn_count`).
- `source=github|h1`: two new small stores in the Slack bridge package wrapping the other tables
  (`GitHubThreadSessions`, `H1ThreadSessions`) with `LookupSandbox` + `SetSession` of the same shape,
  keyed `(repo, number)` / `(report_id, target)`, attribute `agent_session_id`. Table names from
  `KM_GITHUB_THREADS_TABLE` / `KM_H1_THREADS_TABLE`; an unset name ⇒ `503 source_unavailable` (the
  bridge was deployed without the env; loud, not silent).
- Logs one line per call: `bridge: session source=… sender=… status=…`.

### 4.4 `claim_thread` on `post`
In the `ActionPost` branch, after `chat.postMessage` succeeds and only when the post is top-level
(`ThreadTS == ""`) and `env.ClaimThread`: `Threads.Upsert(env.Channel, newTS, env.SenderID)`
(the `if_not_exists` form from v1.8.0, so a row that somehow exists is not overwritten). Threaded
posts ignore the flag. Failure to upsert is logged and does not fail the post.

### 4.5 Pollers (`pkg/compiler/userdata.go`)
- A shared bash helper `km_session_file <source> <key>` → path; `km_session_read <file>` exports
  `S_SESSION_ID S_AGENT_TYPE S_LAST_MSG S_LAST_EVENT` via `jq`; `km_session_write <file> …` writes
  atomically (`mktemp` + `mv`). Lives once in the template and is rendered into each poller.
- Slack poller: the `get-item` becomes `km_session_read`; the end-of-turn `update-item` becomes
  `km_session_write` + `km-slack session --source slack … || log`; the Phase 119 guard reads
  `S_LAST_EVENT`; the handoff's new-top-level `km-slack post` gains `--claim-thread`, and the handoff
  writes the NEW thread's state file (session cleared, agent set) so the next turn starts clean.
- GitHub/H1 pollers: `get-item` → `km_session_read`; both `update-item`s (SET and the stale-session
  `REMOVE`) → `km_session_write` + `km-slack session --source github|h1 …`.
- No `aws dynamodb` call against any threads table remains in the template.
- The frozen pre-92 golden is hand-patched for exactly these hunks; the other goldens regenerate with
  their sanctioned flags.

### 4.6 Module and pins
`infra/modules/ec2spot/v1.9.0` = v1.8.0 minus `ec2spot_threads_rw`. Pins: sandbox template,
`pkg/compiler/ec2spot_timeout_test.go`, `pkg/terragrunt/substrate_version_pin_test.go`.
`lambda-slack-bridge/v1.0.0`: two table ARNs added to the existing threads statement (or a sibling
statement), two env vars, two `variable`s with `default = ""`; `infra/live/use1/lambda-slack-bridge`
passes them from the install's table names (`KM_GITHUB_THREADS_TABLE`/`KM_H1_THREADS_TABLE` already
exist in `ExportTerragruntEnvVars`; confirm during planning).

## 5. Tests
- `pkg/slack`: envelope round-trip with and without the new fields canonicalises identically to a
  pre-change envelope (signing compatibility).
- `cmd/km-slack`: `session` flag parsing (per-source completeness, `--clear`), envelope shape, exit
  codes on 404/403/503.
- `pkg/slack/bridge`: `handleSession` table — owned/unowned/missing rows per source; empty session ⇒
  REMOVE; unset table env ⇒ 503; `post --claim-thread` upserts only for a top-level post and only
  after success; replay nonce still rejected.
- `pkg/compiler`: rendered-bash tests run the helpers under real `bash` + `jq` (write then read
  round-trip, atomic replace); the Slack poller's end-of-turn block calls a fake `km-slack` with
  `session --source slack` and the right key; the handoff block passes `--claim-thread`; a
  template-wide guard: no `aws dynamodb` invocation names a `*THREADS_TABLE` (replaces
  `TestThreadsTable*`); goldens.
- `pkg/terragrunt`: the threads guard reverts to "no Allow statement names a threads table and no
  wildcard DynamoDB write" on the pinned module (its original form, now with no exception); the
  call-coverage test keeps passing (fewer calls); a new assertion that `lambda-slack-bridge` grants
  the two extra tables when their variables are set.
- Live on the canary: `aws dynamodb update-item` on `km-slack-threads` as uid `sandbox` ⇒
  AccessDenied for ANY attribute (the v1.8.0 control only proved `sandbox_id`); a Slack turn → the
  thread row's `claude_session_id` set by the bridge, the box's state file present; `km-slack reply`
  session lookup works on the next turn; a `codex:` handoff → new thread row owned by the box,
  reply in the new thread; GitHub and H1 turns if an inbound profile is at hand (else the unit tests
  carry them); stop/resume keeps the state files.

## 6. Rollout
`make build` → `make build-lambdas` → `km init --dry-run=false` (bridge code + IAM + env; the new
pin) → recreate `ir-bot` → §5 live controls → go/no-go (no-go = repin v1.8.0, redeploy the bridge
from `main`). Existing boxes stay on v1.8.0 until recreated; `km doctor` "Sandbox role shape" is
extended to also name roles that still carry a `-threads-rw` inline policy.

## 7. Out of scope
GitHub/H1 bridges getting their own sandbox-facing endpoints; per-recipient mail keys; a
per-sandbox MITM CA; migrating historical session attributes (a recreated box has no session to
resume anyway).

## 8. Live UAT — canary `ir-bot` (2026-10-18, go/no-go)

Deploy: `make build` → `make build-lambdas` → `km init --dry-run=false` (28 modules, 11 changed —
the Slack bridge code plus its two new table grants/env — 0 destroyed). Canary: `km destroy
irbot-136169c8 --remote --yes` → `km create profiles/ir-bot.yaml ir-bot` → `irbot-fee8539b`. Role
`km-ec2spot-ssm-irbot-fee8539b-use1`: 16 inline policies, no `-threads-rw`, no managed policy,
aggregate 7,186 of 10,240 characters. Boot `SANDBOX_READY`; sidecars active.

Negative controls as uid `sandbox` (all as required): `get-item` and `update-item` on
`km-slack-threads` ⇒ AccessDenied (any attribute, not only `sandbox_id`); `get-item` on
`km-github-threads` ⇒ AccessDenied; `km-slack session` on an unknown thread ⇒ `404 thread_unknown`
for both the `slack` and `github` sources (so the bridge has the GitHub table wired).

Positive controls: an owned-channel turn and a routed turn both replied in ~6 s; each row carries
the bridge-written `claude_session_id`/`agent_type`/`last_turn_ts` beside `sandbox_id`/
`last_dispatch_ts`; `last_assistant_msg` and `last_processed_event_ts` are absent from DynamoDB
and present in `/var/lib/km/sessions/slack/<channel>_<ts>.json` (dir and files `0700`/`0600`).
A second turn in the same thread resumed the same session id and answered from memory. A `codex:`
handoff posted its new top-level message, the bridge claimed the row (`sandbox_id` set at post
time) and the new thread's state file started with `agent_type: codex`; the codex turn itself
failed `401` (this profile has no OpenAI credentials — environment, not the feature; the failed
message was purged from the canary queue). `km-slack reply --session <id>` found the thread
through the bridge's session lookup and posted. `km stop`/`km resume`: the three state files
survived, pollers active, `km doctor` 0 errors, "Sandbox role shape" OK.

One driver artifact, not a defect: a synthetic thread reply that reuses the anchor's `ts` as its
event `ts` is skipped by the Phase 119 guard (same `last_processed_event_ts`); real Slack events
never do that.

**Verdict: GO.** Follow-ups recorded: the handoff's root-run Step 6 post leaves a root-owned
posted marker (set `KM_SLACK_POSTED_MARKER=0` there); pre-v1.9.0 GitHub/H1 rows lacking
`sandbox_id` block the bridge's upsert (doctor note or sweep); a cold-created GitHub/H1 box's
first-turn session record answers 404 until the second turn.
