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

**Plans:** 12/12 plans complete

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
- [ ] 121-11-PLAN.md — GAP 1: pkg/quota writes breached_at + on_breach on first-breach (restores ALR-01 alerter live path) + REAL-path breach-write test
- [ ] 121-12-PLAN.md — GAP 2: Slack+H1 bridges auto-latch action_frozen via Freezer on BreachFreeze (BRG-01/H1-01, CONTEXT §7 dec 8) + freeze-only tests

### Phase 122: GPU vLLM model-serving sandbox profiles plus local-model chat (codex repoint, km model start, Anthropic shim)

**Goal:** Stand up GPU EC2 sandboxes that serve 70B-class local models via vLLM (on a Deep Learning AMI base, weights on a persistent volume), and make that model reachable through every km interface — VS Code Remote-SSH, Slack chat-with-resume (on-box codex repointed at `localhost:8000`), on-box terminal/headless codex, and laptop dev via a new `km model start` SSM port-forward (with an on-box Anthropic↔OpenAI shim so local Claude Code can drive it). claude stays cloud-pointed on-box to preserve a `/claude`-vs-`/codex` cloud-vs-local A/B.
**Requirements**: phase-local synthetic IDs (derived from CONTEXT.md + 122-RESEARCH.md + 122-VALIDATION.md): REQ-122-PROFILES (7 GPU vLLM serving leaves + base/gpu/serve fragment), REQ-122-CODEX (synthesizeCodexConfig local-provider emission), REQ-122-MODELSTART (km model start/status), REQ-122-SHIM (on-box LiteLLM dual-gateway + Anthropic shim), REQ-122-UAT (full 7-gate live UAT)
**Depends on:** Phase 121, Phase 117 (composable inheritance), Phase 92 (agent tool-gating / codex config synthesis)
**Plans:** 4/5 complete; 122-05 partial (docs done; live UAT G3–G9 BLOCKED on G-instance quota — request `d7fe8a96…` PENDING in 052251888500/us-east-1). Gateway shipped as **Bifrost** (multi-provider router, not the original "shim"; LiteLLM = fallback).

**Design spec:** `docs/superpowers/specs/2026-06-27-gpu-vllm-serving-profiles-design.md`. RESEARCH/CONTEXT supersede the spec: O7 → a gateway is CORE (Codex needs the Responses API since Feb 2026); the bake-off chose **Bifrost** on `:8001` as a 5-route multi-provider router (local/claude-bedrock/claude-anthropic/gpt-oss-bedrock/gpt-frontier). Resume: `.planning/phases/122-*/122-UAT.md`.

Plans:
- [x] 122-01-PLAN.md — Wave 0: AgentCodexSpec localBaseURL/localModel + JSON schema + 6 Wave-0 RED test stubs ✓
- [x] 122-02-PLAN.md — base/gpu/serve fragment (Bifrost v1.0.6 5-route router + OTEL) + 7 GPU leaves; validate-all 20/20 ✓
- [x] 122-03-PLAN.md — synthesizeCodexConfig emits [model_providers.local] (wire_api=responses, :8001) + golden; full suite green ✓
- [x] 122-04-PLAN.md — km model start/status + httpTunnelProbe reusing runReconnectingPortForward + root registration + 8 tests ✓
- [~] 122-05-PLAN.md — live UAT: G1/G2 ✓ (unit), Task 4 docs ✓ (docs/gpu-model-serving.md + CLAUDE.md); G3–G9 BLOCKED on G-quota (no GPU spend incurred). Resume per 122-UAT.md once quota lands.

### Phase 123: klankermaker.ai setup wizard — interview-and-emit webapp (self-setup sub-project A)

**Goal:** A public, client-side-only static wizard at `klankermaker.ai` that interviews a
newcomer and emits a downloadable bundle — AWS access config (IAM/SSO `klanker-*` profiles) +
a prefilled `km-config.yaml` + a **gate-segmented, readable `km` runbook** — with **no creds in
the browser and nothing transmitted**. Architecture: a **declarative question-graph** (data) over
**two layers** (gated foundation spine + flat capability catalog) rendered by a generic static
site, guarded by a **CI contract test that runs emitted config through real `km`** so schema drift
fails the build. Presets: ⚡ Quick start (no email/domain; Slack+GitHub+H1+checks) / 🏛 Full
(email+Org+everything) / 🛠 Custom. Account-vend + quota are emitted as **forward-compatible
placeholders** (sub-projects C/B); resume-state is the artifact contract only (sub-project D).
**This is sub-project A of the larger self-setup-from-zero milestone (A webapp / B `km quota
request` / C `km account` lifecycle / D resume model); only A is in scope here.**

**Requirements** (phase-local synthetic IDs, derived from the design spec):
REQ-123-GRAPH (declarative question-graph schema + capability-descriptor format + presets),
REQ-123-EMIT (pure emitter: answers → km-config.yaml + aws-config snippet + N gate-segmented
scripts + README; golden bundles per preset), REQ-123-RENDER (dependency-light static renderer:
branch, validate client-side, build bundle in-browser, "looks awesome"), REQ-123-CONTRACT (CI
test in km repo: emitted config per preset → real `km validate`/`km configure --check`; drift
fails build), REQ-123-HOST (static hosting story for klankermaker.ai; S3+CloudFront).

**Design spec:** `docs/superpowers/specs/2026-06-27-klankermaker-setup-wizard-design.md`
**Depends on:** none (additive; new in-repo `web/wizard/` tree + one thin km-side `--check` seam).
Independent of Phase 122 — planned as 123 with GSD focus deliberately left on 122.

Plans:
- [ ] 123-01-PLAN.md — Wave 1: question-graph schema + capability-descriptor format + pure emitter + golden bundles per preset (no UI). The testable core; the runbook/bundle format that is the contract for B/C/D.
- [ ] 123-02-PLAN.md — Wave 2: static renderer (graph-driven branching UI, client-side validation, in-browser bundle generation) + frontend-design pass.
- [ ] 123-03-PLAN.md — Wave 3: CI contract test — add thin `km configure --check`, emit km-config per preset, run real km over each; drift fails CI.
- [ ] 123-04-PLAN.md — Wave 4: static hosting for klankermaker.ai (S3+CloudFront+ACM), pinned-km fetch+checksum in the emitted runbook, CI deploy.

### Phase 124: Platform-wide AZ failover and capacity feasibility for EC2 launches

**Goal:** Stop EC2 launches from being pinned to a single capacity-dry AZ
(`us-east-1a`). Wrap the existing `compile → terragrunt apply` pipeline in an
orchestrated AZ sweep inside `km create` (inherited by the cold-create Lambda,
which runs `km create` as a subprocess): try AZs in a capacity-aware order,
classify failures so `InsufficientInstanceCapacity` iterates to the next AZ
while a *regional* quota wall (`L-DB2E81BA`=0) or auth/param error fails fast,
kill the spot `wait_for_fulfillment` forever-loop with a bounded waiter, and
keep the GPU `additionalVolume` co-located with the chosen AZ. Add an honest
`km capacity` feasibility report (no false "available" — AWS has no live
on-demand capacity API) backed by a new `{prefix}-capacity` DDB table that
remembers recent ICE failures + last-success AZ per instance type. Platform-wide
(spot + on-demand, GPU + non-GPU); retires the `spot-multi-az` pending todo.

**Requirements** (phase-local synthetic IDs, derived from the design spec):
REQ-124-SWEEP (AZ override plumbing compiler→ec2spot single-instance + N>1 spread
preserved; classify-and-retry loop in km create; taint/replace between attempts),
REQ-124-CLASSIFY (shared error taxonomy: ICE/spot-price/waiter-timeout → iterate;
quota/auth/invalid → fail-fast; create-handler `nocap` refactored onto it),
REQ-124-WAITER (bounded `timeouts.create` on `aws_spot_instance_request`; full
4-AZ sweep fits Lambda 900s budget),
REQ-124-RANK (capacity-aware rankAZs: drop non-offering AZs via
DescribeInstanceTypeOfferings, regional-quota gate up front, last-success sticky,
ICE deprioritize, `spec.runtime.azPreference` override),
REQ-124-STORE (new `{prefix}-capacity` DDB table + TF module + regionalModules()
bump + live unit; (instanceType, az) key, TTL'd ICE rows, read/write from operator
+ Lambda),
REQ-124-CAPCMD (`km capacity <profile|--type>` feasibility report;
verdicts likely/quota-blocked/not-offered/recently-dry/unknown),
REQ-124-SURFACE (`spec.runtime.azPreference` additive schema; `km create
--wait-for-capacity[=30m]` opt-in outer backoff; `km doctor` table + GPU-quota=0 WARN),
REQ-124-UAT (live: GPU launch fails over 1a→1c, quota=0 fail-fast, capacity report,
all 4 subnets exist).

**Design spec:** `docs/superpowers/specs/2026-06-28-az-failover-capacity-feasibility-design.md`
**Depends on:** none hard (additive; touches the `ec2spot` module + `pkg/compiler`
+ a new DDB table). Motivated by Phase 122 (GPU vLLM profiles) and supersedes the
`spot-multi-az` todo; EBS coupling interacts with Phase 87 (additionalSnapshots).
Independent of Phase 123 (setup wizard) — sequential number only, no code dependency.

**Out of scope:** multi-region failover; live on-demand capacity *prediction*;
Lambda auto-requeue of `nocap` cold-creates; EC2 Fleet/CreateFleet rewrite; spot
interruption handling. (See spec § Non-goals.)

**Plans:** 4/6 plans executed

Plans:
- [ ] 124-01-PLAN.md — Wave 0: `pkg/capacity/` core (pure `ClassifyError` taxonomy + DDB capacity store w/ TTL + `RankAZs` interface surface/stub) + `servicequotas` SDK + `spec.runtime.azPreference` field/schema + `GetCapacityTableName()` + azPreference byte-identity test.
- [ ] 124-02-PLAN.md — Wave 1: refactor `cmd/create-handler` `nocap` onto `pkg/capacity.ClassifyError` + upgrade the existing `km create` AZ-rotation loop (create.go:746-869) into classify-and-retry (ICE iterate; quota/auth/invalid fail-fast w/ L-DB2E81BA-named remediation). (TDD)
- [ ] 124-03-PLAN.md — Wave 0: `dynamodb-capacity` TF module (+TTL) + live unit + `regionalModules()` entry + module-count test bump (26→27) + bounded spot `timeouts.create=3m` on `aws_spot_instance_request` only. **`make build` before `km init`** gotcha called out.
- [ ] 124-04-PLAN.md — Wave 2: capacity-aware `RankAZs` impl (offerings drop + GPU-quota gate + last-success sticky + ICE deprioritize + azPreference merge) + wire RankAZs into `km create` pre-loop ordering + `km capacity <profile|--type>` honest-verdict report.
- [ ] 124-05-PLAN.md — Wave 3: `km create --wait-for-capacity[=30m]` opt-in outer backoff (never forwarded to the Lambda subprocess) + `km doctor` capacity-table check + GPU-family quota=0 WARN.
- [ ] 124-06-PLAN.md — Wave 4 (live UAT, G-quota gated): full `go test ./...` green + 20/20 profiles + deploy (make build BEFORE km init; make build-lambdas) + docs + G1 4-subnets / G2 km capacity accuracy / G3-G5 GPU 1a→1c failover + quota-0 fail-fast + sticky (quota-deferred like Phase 122).
