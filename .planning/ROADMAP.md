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
**Plans:** 0 plans (run /gsd:plan-phase 118)

Plans:
- [ ] TBD (run /gsd:plan-phase 118 to break down)
