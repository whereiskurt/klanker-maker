### Phase 117: Composable multi-parent profile inheritance (deep-merge list-union extends)

**Goal:** A SandboxProfile can declare `extends:` as a single string OR an ordered list of base references; km deep-merges all bases + the child into one effective profile (maps recurse, scalars child-wins, lists concat+dedup), then validates the merged leaf. Replaces the typed-merger zoo with a generic map deep-merge so every section composes; `profiles/base/` fragments (metadata.abstract:true) collapse the ~80-line-per-profile duplication.
**Requirements**: none mapped (new architectural phase — must_haves derived from phase GOAL)
**Depends on:** Phase 116
**Plans:** 5/5 plans complete

Plans:
- [ ] 117-01-PLAN.md — extends string|[]string union type, fragment marker, initCommandsAppend + JSON schema, fix 3 call sites
- [ ] 117-02-PLAN.md — generic deepMerge engine + DAG resolve (diamond/memoized); delete the typed merger zoo
- [ ] 117-03-PLAN.md — wire Resolve into km validate/create; abstract-fragment skip; validate-all skips base/
- [ ] 117-04-PLAN.md — author profiles/base/ fragments; refactor learn.v2.* + dc34; byte-identity gate
- [ ] 117-05-PLAN.md — docs: OPERATOR-GUIDE § Composable inheritance, CLAUDE.md pointers, agent-tool-gating xref

### Phase 118: Slack trigger allowlist + private per-sandbox channels

**Goal:** Two composable Slack additions. (A) `notification.slack.private` (bool, default false) creates the per-sandbox channel as `is_private:true` (instead of hardcoded public at `pkg/slack/client.go:606`); invites unchanged; no new scopes. (B) A Uxxxx trigger allowlist named `allow`: install-level `slack.allow` (km-config.yaml → `KM_SLACK_ALLOW`) and per-sandbox `notification.slack.inbound.allow` (profile → `km-sandboxes` row → bridge `FetchByChannel`). Resolution: non-empty per-sandbox replaces install-level; else install-level; else empty=everyone (backward-compatible). Enforced in `events_handler.go` on `event.User`, silent ignore on reject (like the GitHub bridge), always enforced independent of mention-only mode and the Phase 91.3 thread-bypass. Design spec: `docs/superpowers/specs/2026-06-24-slack-trigger-allowlist-private-channels-design.md`.
**Requirements**: none mapped (additive feature — must_haves derived from the approved design spec)
**Depends on:** Phase 117
**Plans:** 6/6 plans complete

Plans:
- [ ] 118-01-PLAN.md — Wave 0 RED test stubs (allowlist enforcement, slack_allow round-trip, CreateChannel mocks, validate warns) + struct stub fields
- [ ] 118-02-PLAN.md — Feature A: thread private bool through CreateChannel + validate warns + JSON schema
- [ ] 118-03-PLAN.md — Feature B per-sandbox plumbing: slack_allow DDB round-trip + FetchByChannel + km create write
- [ ] 118-04-PLAN.md — Feature B install-level: slack.allow config (merge-list) → KM_SLACK_ALLOW → bridge EventsHandler.Allow
- [ ] 118-05-PLAN.md — Feature B enforcement: allowlist gate in events_handler.go Handle() (silent-drop, resolution order)
- [x] 118-06-PLAN.md — docs: slack-notifications.md § Phase 118 + CLAUDE.md summary (completed 2026-06-25)

### Phase 119: Slack inbound per-thread parallelism

**Goal:** Different Slack threads to the same sandbox run in PARALLEL while messages within a thread stay serial+ordered, bounded by an operator concurrency cap. Dormant by default (cap=1 == today's serial behaviour). Two layers + a knob: (1) Bridge — change `MessageGroupId` from `info.SandboxID` → `threadTS` (fallback `msg.TS`) at both `h.SQS.Send` sites in `events_handler.go` (unconditional; FIFO then gives parallel-across-threads / serial-within-thread for free). (2) Poller (`userdata.go`) — new `notification.slack.inbound.maxConcurrentThreads` (*int, default 1) → `KM_SLACK_MAX_CONCURRENCY` via `.NotifyEnv`; loop receives N, dispatches each turn in a backgrounded subshell behind a counting semaphore, ACKs AFTER the turn (required for per-thread ordering — reverses today's ack-first at `userdata.go:2069`), with a visibility heartbeat (`ChangeMessageVisibility`) since `VisibilityTimeout` is only 30s at `sqs.go:127`. Open decision: ack-after-completion reintroduces a crash-redelivery dup window → add a Slack per-turn idempotency guard (mirror Phase 108 GitHub `<!-- km-turn -->`) or accept. Out of scope: per-thread git-worktree isolation (shared /workspace mutation hazard — cap is for conversational fan-out), GitHub/H1 parity, separate queues per thread. No apiVersion bump. Design spec: `docs/superpowers/specs/2026-06-24-slack-inbound-per-thread-parallelism-design.md`.
**Requirements**: none mapped (additive feature — must_haves derived from the approved design spec)
**Depends on:** Phase 118
**Plans:** 5/5 plans complete

Plans:
- [x] 119-01-PLAN.md — Wave 0 RED test stubs (bridge group==threadTS, validate WARN, KM_SLACK_MAX_CONCURRENCY env, queue VisibilityTimeout) + MaxConcurrentThreads struct/schema field
- [x] 119-02-PLAN.md — Layer 1 bridge: MessageGroupId→threadTS at both Send sites + doc-comment fix; raise Slack inbound queue base VisibilityTimeout to 1800s
- [x] 119-03-PLAN.md — schema/validate/NotifyEnv plumbing: km validate WARN cap>1 w/o perSandbox+inbound; emit KM_SLACK_MAX_CONCURRENCY only when cap>1 (dormancy)
- [x] 119-04-PLAN.md — Layer 2 poller rewrite: wait -n counting semaphore + ack-after-completion + visibility heartbeat + RUN_ID uniqueness + last_processed_event_ts idempotency guard; hand-patch frozen golden
- [x] 119-05-PLAN.md — demo profile + live synthetic-HMAC E2E (parallelism/ordering/cap/heartbeat/dormant/dedup) + docs (slack-notifications § Phase 119, klanker:slack SKILL, CLAUDE.md)

### Phase 120: Profiles reset and OS-layered fragment library

**Goal:** Collapse `profiles/` to 3 composed demo profiles (`learner`/`desktop`/`github`) built from an expanded `profiles/base/` fragment library (`os/redhat`, `os/debian`, `toolchain-agents`, `plugin-klanker`, `slack-persandbox`). The high-churn toolchain install block (claude-code/codex version pins, copy-pasted across ~6 files today) is layered out into a single fragment. All retired demos + frozen byte-identity fixtures are archived into `testdata/profiles/` with test path constants updated in lockstep so byte-identity + golden contracts stay green. `learner` must functionally match today's `learn.v2.yaml`. Contributes to the larger "lean top-level" milestone goal (within-folder cleanup; top-level folder reduction is a separate follow-on phase).
**Requirements**: Design spec at `docs/superpowers/specs/2026-06-25-profiles-reset-fragment-library-design.md`
**Depends on:** Phase 119 (Phase 117 `extends:` multi-parent inheritance is the enabling mechanism)
**Deploy class:** `make build` only — no Lambda rebuild, no schema/DDB change, no `km init`, no sandbox recreate (inheritance resolves at `km validate`/`km create` time).
**Plans:** 4/4 plans complete

Plans:
- [ ] 120-01-PLAN.md — Author 5 new base fragments (os/redhat, os/debian, toolchain-agents single-pin, plugin-klanker, slack-persandbox)
- [ ] 120-02-PLAN.md — Author 4 composed leaves (learner, desktop, github, h1); km validate clean no-WARN
- [ ] 120-03-PLAN.md — git mv retired demos + frozen fixtures to testdata/profiles/ + update 6 test-path constants (atomic, no red commit)
- [ ] 120-04-PLAN.md — Rewrite validate-all-profiles.sh + km-config.yaml swaps + final gates + learner functional-match review

### Phase 121: Action quota and freeze quarantine for high-impact outbound actions

**Goal:** Give the platform an external, agent-untrusted quota layer on high-impact outbound
actions (GitHub writes, email send, Slack/H1 posts) so a sandbox manipulated into a tight loop
(malicious chatter *or* runaway/jailbroken agent) trips a limit at a chokepoint it cannot bypass.
Phase 1 alerts; the same counters later enforce. Adds multi-window per-(sandbox,action) counters
at the http-proxy and bridge Lambdas, per-profile + install-default config, dual operator/user
trip notices, a three-tier breach policy (`warn`/`block`/`freeze`), and a latched **quarantine**
(`action_frozen`) releasable only by the operator at the CLI (`km unlock`) — never from Slack.

**Requirements**: phase-local synthetic IDs (derived from CONTEXT.md + 121-VALIDATION.md): QUO-01..05, PRX-01..03, BRG-01..03, H1-01, META-01..02, ALR-01, INIT-01..02, CFG-01, PROF-01, CMP-01, CLI-01..03
**Depends on:** Phase 120

**Success criteria:**
- [ ] `pkg/quota` records multi-window counters (`lifetime` / `hour` / `day`) per (sandbox, action) in a new `{prefix}-action-quota` DynamoDB table via atomic `ADD`, fixed calendar buckets, TTL on rolling windows.
- [ ] http-proxy meters `github_pr` / `github_comment` / `github_review` / `email_send` by URL pattern; slack-bridge meters `slack_post`; h1-bridge meters `h1_comment`.
- [ ] Limits resolve per (action, window) as profile (`spec.limits`) → install default (`km-config.yaml limits:`) → unlimited; resolved map reaches the proxy via userdata and the bridges via the `km-sandboxes` row (`action_limits`, round-tripped through `SandboxMetadata`).
- [ ] Breach fires exactly one operator alert (DDB-Stream → `km-quota-alerter` Lambda → SES email + optional Slack control channel) per (sandbox, action, window).
- [ ] Breach fires a dual user-facing notice: in-thread reply from the bridge for chat trips (`slack_post`/`h1_comment`), channel-level post from the alerter for proxy trips — both control-plane (uncounted), enforce-aware wording, idempotent, best-effort when a Slack channel exists.
- [ ] Per-limit `onBreach: warn | block | freeze`; `block` denies that action for the window; all policies default to dormant so absent config is byte-identical to today.
- [ ] `freeze` latches `action_frozen=true` (+ reason/at/by) on the sandbox row, survives stop/resume, denies all actions, makes inbound bridges refuse new turns + post the frozen notice; triggered auto-on-breach or by `km freeze <sandbox>`; released only by `km unlock` (latch-aware). No Slack trigger (deferred).
- [ ] `km list` / `km status` show `FROZEN`; `km doctor` surfaces frozen sandboxes + the new table.
- [ ] Deploy surface verified: new table in `regionalModules()` (+ module-order test bump) and `init.go`; `km-quota-alerter` has TF module + live terragrunt unit + `init.go` entry + `lambdaBuilds()` entry; IAM added to ec2spot/slack/h1 roles + alerter; config key in the v2→v merge-list.

**Design context:** see this phase's `CONTEXT.md`

**Plans:** 1/10 plans executed

Plans:
- [ ] 121-01-PLAN.md — Wave 0: pkg/quota skeleton + RED test stubs + module-count bump (24→26) + all Wave 0 stubs
- [ ] 121-02-PLAN.md — pkg/quota core: multi-window atomic ADD counter + bucket math + TTL + ResolveLimits (TDD)
- [ ] 121-03-PLAN.md — spec.limits profile schema + validate + km-config limits: v2→v merge-list
- [ ] 121-04-PLAN.md — SandboxMetadata attrs (action_limits/action_frozen/frozen_*) + round-trip + Freeze/Unfreeze writers
- [ ] 121-05-PLAN.md — proxy chokepoint: URL→action classifier + WithActionQuota + SES MITM + no-double-count + ec2spot IAM
- [ ] 121-06-PLAN.md — bridge chokepoints: Slack+H1 quota.Record + frozen-dispatch gate + in-thread notice + FetchByChannel + bridge IAM
- [ ] 121-07-PLAN.md — compiler emits resolved limits to proxy userdata + km create writes action_limits attr (dormant byte-identical)
- [ ] 121-08-PLAN.md — dynamodb-action-quota TF module (+Streams) + live unit + regionalModules() (INIT-01)
- [ ] 121-09-PLAN.md — km-quota-alerter Lambda (DDB-Stream→SES+Slack, idempotent) + 4-part registration (INIT-02, ALR-01)
- [ ] 121-10-PLAN.md — CLI: km freeze + latch-aware km unlock + FROZEN in list/status + doctor surfacing
