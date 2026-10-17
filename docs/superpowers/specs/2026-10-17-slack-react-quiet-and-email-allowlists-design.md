# Slack: reaction quiet period + email allowlists (domain wildcards, per-klanker narrowing) — design

**Date:** 2026-10-17. **Status:** approved in chat (operator: "defaults confirmed"). Builds on
`2026-10-16-slack-default-route-design.md` and `2026-10-17-slack-bot-initiated-dm-design.md`,
both merged in PR #147. One phase, two features that share the bridge gates and the config
plumbing.

## Part 1 — Reaction quiet period

### 1.1 Problem
The bridge reacts 👀 on every message it dispatches. In a live back-and-forth that is noise:
the person already knows the bot is there. Phase 91.4/91.5's `react_always: false` is the only
knob and it silences every thread reply, which loses the "I saw that" signal after a lull.

### 1.2 Decisions (confirmed)
| Decision | Choice | Why |
|---|---|---|
| Rule | A **thread reply** reacts only when the thread has been quiet for more than N seconds, measured from the thread row's `last_dispatch_ts`. **Top-level messages always react** (unchanged engagement signal). `react_always: false` still silences thread replies entirely (it wins). | Reuses `last_dispatch_ts`, which `MarkDispatched` advances after every successful delivery in every mode, so the boundary is exact and already stored. |
| Default | **180 s, on by default** (install key absent ⇒ 180). `0` disables (today's behaviour). | Operator-confirmed. This is a deliberate behaviour change for every install on the next `km init`; CLAUDE.md and the docs say so. |
| Levels | Install `slack.react_quiet_seconds` (int) → `KM_SLACK_REACT_QUIET_SECONDS`; profile `notification.slack.inbound.reactQuietSeconds` (`*int`) → row attr `slack_react_quiet_seconds` (N) → `SandboxRoutingInfo.ReactQuietSeconds *int`. Per-sandbox wins when set; same precedence as `reactAlways`. | Mirrors the three existing tri-state inbound knobs; nothing new to learn. |
| Where it applies | Everywhere a thread reply is dispatched: the box's own `#sb-` channel, routed threads, DM threads. | Consistency; a per-channel exception has no user. |
| Row read | Mapped channels: one `DispatchBoundary` read in the react step (new GetItem). Routed/continuity threads: reuse the row already fetched. Read error or no row ⇒ **react** (fail toward the signal). | A missing reaction is the failure to prefer over a silent bot. |
| Clock | `h.Now()` vs the row's `last_dispatch_ts` (ISO-8601 as written by `MarkDispatched`). | Already injectable for tests. |

### 1.3 Changes
- `internal/app/config/config.go`: `Slack.ReactQuietSeconds *int` with the merge-list entry
  `slack.react_quiet_seconds` (the `project_config_key_merge_list` trap), validation `>= 0`;
  `km init` exports `KM_SLACK_REACT_QUIET_SECONDS` (absent key ⇒ `180`).
- `infra/modules/lambda-slack-bridge/v1.0.0`: additive var `slack_react_quiet_seconds`
  (default `"180"`) into the env block (edit in place, additive, as Phase 115 did).
- `pkg/profile/types.go` + JSON schema: `reactQuietSeconds *int` on the inbound block
  (`additionalProperties:false`, so the schema must gain it). `km validate` WARNs when set
  without `inbound.enabled`.
- `internal/app/cmd/create_slack_inbound.go`: write `slack_react_quiet_seconds` only when set;
  `pkg/aws/sandbox_dynamo.go` round-trips it (the `SandboxMetadata` lossy-round-trip trap);
  `routingInfoFromItem` reads it.
- `pkg/slack/bridge/events_handler.go` react step (~line 698): after the `reactAlways` check,
  `if quiet > 0 && msg.ThreadTS != "" { if last, err := boundary(); err == nil && last != "" && now.Sub(last) <= quiet { skip, log "reaction skipped (quiet period)" } }`.
- `cmd/km-slack-bridge/main.go`: read the env into `EventsHandler.ReactQuietSeconds`.
- Docs: `docs/slack-notifications.md` § Phase 91 (new "Quiet period" subsection + the knob
  table), `CLAUDE.md` bullet, `km-config.yaml` example.

### 1.4 Tests
Bridge: thread reply within the window ⇒ no `reactions.add`; after the window ⇒ reaction;
top-level ⇒ reaction regardless; `react_always:false` ⇒ silent regardless of window; row read
error ⇒ reaction; per-sandbox `0` overrides install `180` (reacts every time); per-sandbox
`600` overrides install `0`. Config: merge-list regression guard; env export `180` when absent.
Profile: schema round-trip through YAML (the `project_struct_level_tests_bypass_schema` rule).

## Part 2 — Email allowlists: `*@domain` and per-klanker narrowing

### 2.1 Problem
`slack.default_route.allow_emails` is a flat list of exact addresses for the whole install.
Two needs: admit a whole domain (`*@example.com`) without listing people, and let one klanker
(e.g. `ir-bot`) accept only a few of those people even though the install admits the domain.

### 2.2 Decisions (confirmed)
| Decision | Choice | Why |
|---|---|---|
| Wildcard form | Exactly `*@<domain>`: one `*`, as the whole local part, non-empty domain, lowercased. Anything else with `*` (`k*@x`, `*@*`, bare `*`) fails `km init` validation. | A single unambiguous form; no glob engine. |
| Matching | `Allows(email)`: exact set first, then the domain set on the part after the last `@`. Still lowercased, still deny on empty. | Cheap; no change to the `users.info` identity rule. |
| Per-klanker list | `notification.slack.inbound.allowEmails []string` (exact or `*@domain`). **Absent ⇒ inherit the install list. Present ⇒ the sender must pass the install list AND this list** (intersection; it can only narrow). An explicitly empty list is a `km validate` **error** ("omit the field to inherit"). | Never "empty means everyone": that is the Phase 118 user-id list's inverted rule, left alone and NOT reinterpreted. Intersection keeps the install list the outer boundary. |
| Where it applies | Every dispatch to a sandbox whose row carries the list: its own `#sb-` channel, routed threads, DM threads, and `km-slack dm` targets sent **by** that sandbox. | "Everywhere", confirmed; a hole in the mapped channel would defeat the narrowing. |
| Refusal | Silent drop, logged at Info (`sender not in sandbox allowEmails`). No hint. | Consistent with the route; a hint in a shared channel leaks who is allowed. |
| Relationship to Phase 118 `inbound.allow` (user ids) | Both gates apply (AND). | Different identities (id vs email); neither replaces the other. |
| Cold-create first turn | The row does not exist yet, so the narrowing cannot apply to the very first routed turn; the install list does. Documented limitation, same shape as strict context on a first turn. | The profile is only known to the create-handler at that point. |
| Email resolution on mapped channels | The cached `users.info` fetcher is wired **unconditionally** in `main.go` (it only needs the bot token); the default route itself stays gated on its three env vars. A sandbox with `allowEmails` on an install with no route still gets its gate. | Otherwise the field is silently inert on route-less installs. |
| Doctor | `Slack default route allowlist` resolves exact entries with `users.lookupByEmail` as today and reports a wildcard entry as `*@example.com (domain wildcard, not resolvable)` without failing. | A domain cannot be looked up; it must not read as a broken entry. |

### 2.3 Changes
- `pkg/slack/bridge/default_route.go`: `NewDefaultRoute` splits entries into `exact` and
  `domains`; `Allows` checks both. New `AllowsAll(email string, lists ...[]string) bool` helper
  used for the per-sandbox intersection (each list compiled the same way, memoised per routing
  info).
- `internal/app/config/config.go`: `SlackDefaultRoute.Validate` accepts `*@domain`, rejects
  other globs; `NormalizedEmails` unchanged otherwise.
- `pkg/profile/types.go` + schema: `allowEmails []string` on the inbound block; `km validate`:
  entries validated with the same rule; empty-present ⇒ error; set without `inbound.enabled` ⇒ WARN.
- `create_slack_inbound.go`: write `slack_allow_emails` (S, comma-joined, lowercased) when
  present; `sandbox_dynamo.go` round-trip; `routingInfoFromItem` → `SandboxRoutingInfo.AllowEmails`.
- `pkg/slack/bridge/events_handler.go`: a single `senderAllowedForSandbox(ctx, info, user)`
  used by the owned path (after the Phase 118 id check, before mention/thread), the continuity
  branch, and the route's enqueue/resume branch (after the install-list check). Routed strict
  context's trust predicate uses the same function. Deny on lookup error (never cached).
- `pkg/slack/bridge/handler_dm.go`: after the install-list check, fetch the posting sandbox's
  routing info by id (`SandboxByID`) and apply its `AllowEmails` to the target; refusal stays
  `403 dm_not_allowed`.
- `internal/app/cmd/doctor_slack.go`: wildcard reporting.
- Docs: `docs/slack-notifications.md` § Default route (allowlist forms, narrowing, the
  intersection rule, the first-turn limitation), § Phase 118 (the AND with user ids),
  `docs/slack-default-route.md` §4/§11, `docs/profile-reference.md` rows, CLAUDE.md bullet.

### 2.4 Tests
`DefaultRoute`: exact, domain, subdomain NOT matched by `*@example.com` (`a@sub.example.com`
is denied), case, rejection of bad globs. Bridge matrix: install admits domain + sandbox list
narrows ⇒ listed sender dispatched, unlisted (same domain) silently dropped with the Info log,
on the owned path, continuity, route enqueue, route resume, and `dm`; absent list ⇒ install
rule only; lookup error ⇒ deny; AND with Phase 118 ids. Config/profile/doctor/metadata
round-trip tests as in Part 1.

## 3. Deploy
`make build` → `make build-lambdas` → `km init --dry-run=false` (bridge env var + zip,
create-handler schema). Existing sandboxes carry neither per-sandbox field until recreate; the
install-level quiet period and domain wildcards take effect on the next event after the apply.
No manifest change, no new scope, no new table.

## 4. UAT
1. Thread reply within 3 minutes of the bot's last turn: no 👀; after 3 minutes: 👀; top-level: 👀.
2. `allow_emails: ["*@example.com"]`: a domain member is dispatched, an outsider is dropped.
3. `ir-bot` profile with `allowEmails: [kurt@example.com, bob@example.com]`: a third domain
   member mentions in `#sb-ir-bot`, in a routed thread, and replies under a DM: all dropped
   with the Info log; the two listed people work everywhere; `km-slack dm --to <third>` from
   `ir-bot` is `dm_not_allowed`.
4. `km doctor` shows the wildcard entry as a domain wildcard and the exact entries resolved.
