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
**Plans:** 2/5 plans executed

Plans:
- [ ] 119-01-PLAN.md — Wave 0 RED test stubs (bridge group==threadTS, validate WARN, KM_SLACK_MAX_CONCURRENCY env, queue VisibilityTimeout) + MaxConcurrentThreads struct/schema field
- [ ] 119-02-PLAN.md — Layer 1 bridge: MessageGroupId→threadTS at both Send sites + doc-comment fix; raise Slack inbound queue base VisibilityTimeout to 1800s
- [ ] 119-03-PLAN.md — schema/validate/NotifyEnv plumbing: km validate WARN cap>1 w/o perSandbox+inbound; emit KM_SLACK_MAX_CONCURRENCY only when cap>1 (dormancy)
- [ ] 119-04-PLAN.md — Layer 2 poller rewrite: wait -n counting semaphore + ack-after-completion + visibility heartbeat + RUN_ID uniqueness + last_processed_event_ts idempotency guard; hand-patch frozen golden
- [ ] 119-05-PLAN.md — demo profile + live synthetic-HMAC E2E (parallelism/ordering/cap/heartbeat/dormant/dedup) + docs (slack-notifications § Phase 119, klanker:slack SKILL, CLAUDE.md)
