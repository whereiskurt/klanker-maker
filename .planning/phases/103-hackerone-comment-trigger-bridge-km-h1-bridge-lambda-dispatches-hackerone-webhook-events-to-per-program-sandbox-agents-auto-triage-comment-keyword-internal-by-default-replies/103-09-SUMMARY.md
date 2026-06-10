---
phase: 103-hackerone-comment-trigger-bridge
plan: 09
subsystem: sandbox-runtime
tags: [hackerone, userdata, sqs, fifo, dynamodb, session-continuity, agent-verb, internal-reply, dormancy-golden, schema]

# Dependency graph
requires:
  - phase: 103-hackerone-comment-trigger-bridge
    plan: 08
    provides: "create_h1_inbound.go (provisionH1InboundQueue/rollbackH1InboundQueue + notificationH1Inbound forward-compat stub) + pkg/aws/sqs.go H1InboundQueueName/CreateH1InboundQueue/H1InboundDLQName (1800s VisibilityTimeout + DLQ RedrivePolicy) + SSM /{prefix}/sandbox/{id}/h1-inbound-queue-url"
  - phase: 103-hackerone-comment-trigger-bridge
    plan: 05
    provides: "cmd/km-h1 sandbox helper (comment internal-by-default / read / state over HTTP Basic Auth) — the back-channel the poller's preamble teaches"
  - phase: 103-hackerone-comment-trigger-bridge
    plan: 01
    provides: "TestUserdataH1ByteIdentity Wave-0 dormancy golden (h1_byte_identity_golden.txt) guarding that the H1 poller renders ONLY when enabled"
provides:
  - "notification.h1.inbound.enabled schema field (NotificationH1Spec / NotificationH1InboundSpec, *bool default false) — the km init --sidecars schema addition + the dormancy gate"
  - "km-h1-inbound-poller userdata heredoc + km-h1-inbound-poller.service systemd unit + km-h1 binary download + KM_H1_INBOUND_QUEUE_URL notify.env slot + 4 systemctl enable/restart lines, ALL gated on H1InboundEnabled"
  - "session continuity keyed (report_id, target) via DDB GetItem + UpdateItem write-back (NOT PutItem); target = this sandbox's own KM_SANDBOX_ALIAS"
  - "agent-verb precedence (verb > thread agent_type > profile default) + cross-agent reset + codex-missing guard + Gap-E stale-resume retry, ported from the GitHub Phase 102 poller"
  - "INTERNAL-by-default reply preamble; --reply-to-researcher taught ONLY when the envelope carries reply_to_researcher:true (safety layer)"
  - "profiles/h1-triage.yaml — lean spot t3.medium (2h TTL / 20m idle, stop), sourceAccess none, api.hackerone.com egress, notification.h1.inbound.enabled:true"
  - "create.go Step 11g: wires provisionH1InboundQueue (Plan 08 stub repointed at p.Spec.Notification.H1.Inbound)"
  - "TestUserdataH1EnabledRendersPoller (active half of the dormancy invariant)"
affects: [103-10-uat]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "On the box, the (report_id, target) thread key's `target` is the sandbox's OWN alias (KM_SANDBOX_ALIAS, falling back to KM_SANDBOX_ID): the bridge dispatched HERE because this alias matched the fanout target.Alias and upserted the (report_id, target.Alias) row with this sandbox_id — so the poller reconstructs the SK locally without the bridge carrying it in the H1Envelope JSON."
    - "Session write-back uses DDB UpdateItem (SET agent_session_id, agent_type) NOT PutItem — a full-row PutItem would strip sandbox_id/ttl_expiry that the bridge upsert set (memory project_sandboxmetadata_lossy_roundtrip), the same hazard the GitHub poller avoids."
    - "The reply preamble is SAFETY-CONDITIONAL: --reply-to-researcher is only TAUGHT to the agent when the envelope's reply_to_researcher flag is true; an internal-default trigger never even sees the external verb in its prompt, reinforcing internal-by-default at the prompt layer (on top of km-h1's flag/JSON-body layers)."

key-files:
  created:
    - profiles/h1-triage.yaml
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_h1_byte_identity_test.go
    - internal/app/cmd/create_h1_inbound.go
    - internal/app/cmd/create.go

key-decisions:
  - "profiles/h1-triage.yaml uses sourceAccess.mode: none (not allowlist+github like github-review.yaml) — the triage agent reads the report and replies entirely over the HackerOne customer API via km-h1, so no git clone is needed. Egress allowlist is api.hackerone.com + .amazonaws.com + api.anthropic.com only."
  - "On-box `target` derives from KM_SANDBOX_ALIAS (fallback KM_SANDBOX_ID) rather than being carried in the H1Envelope — the envelope never includes the target alias (the bridge knows it at dispatch; the box is the target). This keeps the envelope schema unchanged and the SK reconstructable locally."
  - "KM_H1_REPLY_AGENT is exported inline in the sudo string (claude/codex) as a forward-compat attribution hook mirroring KM_GITHUB_REPLY_AGENT, even though cmd/km-h1 (Plan 05) does not yet read it — harmless, and ready if km-h1 grows a 'via Claude/Codex' footer."
  - "The active assertion lives in a new TestUserdataH1EnabledRendersPoller (flips the gate in-struct on the same ec2-basic.yaml testdata profile) rather than a new H1-enabled testdata file — the gate is a single *bool, so an in-test flip is the minimal seam and keeps the golden's baseline profile stable."

patterns-established:
  - "Dormancy invariant is now pinned from BOTH sides: TestUserdataH1ByteIdentity (H1-free → byte-identical golden) + TestUserdataH1EnabledRendersPoller (H1-on → poller/unit/binary/key/UpdateItem/preamble present)."

requirements-completed: [H1-THREAD-CONTINUITY, H1-AGENT-VERB, H1-REPLY-INTERNAL-DEFAULT, H1-DEPLOY-WIRING]

# Metrics
duration: 18min
completed: 2026-06-10
---

# Phase 103 Plan 09: H1 sandbox-side inbound poller Summary

**The km-h1-inbound-poller now consumes the per-sandbox h1-inbound FIFO on the box — long-poll, (report_id, target) session resume via UpdateItem, agent-verb precedence, codex-missing + stale-resume guards, and an INTERNAL-by-default km-h1 reply preamble — all gated on notification.h1.inbound.enabled so the Wave-0 dormancy golden stays byte-identical for every H1-free profile.**

## Performance

- **Duration:** ~18 min
- **Started:** 2026-06-10T00:48Z
- **Completed:** 2026-06-10T01:06Z
- **Tasks:** 2 (both auto)
- **Files created:** 1; **modified:** 6

## Accomplishments
- `notification.h1.inbound.enabled` schema field (Go struct + JSON schema) — the dormancy gate AND the `km init --sidecars` schema addition.
- `km-h1-inbound-poller` userdata heredoc + systemd unit + km-h1 binary download + 4 systemctl lines, forked from the GitHub poller and re-keyed `(report_id, target)`, with UpdateItem write-back, agent-verb precedence, and a safety-conditional internal-by-default reply preamble.
- `profiles/h1-triage.yaml` (lean spot t3.medium, `sourceAccess none`, `api.hackerone.com` egress, inbound enabled) — validates clean; full 22-profile inventory still green.
- `create.go` Step 11g wires `provisionH1InboundQueue` (Plan 08's `notificationH1Inbound` stub repointed at `p.Spec.Notification.H1.Inbound`).

## Task Commits

1. **Task 1: notification.h1.inbound schema field + h1-triage profile** — `5f78fb3d` (feat)
2. **Task 2: km-h1-inbound-poller userdata + create.go wiring** — `177dec8b` (feat)

## Files Created/Modified
- `pkg/profile/types.go` — NotificationH1Spec / NotificationH1InboundSpec + NotificationSpec.H1.
- `pkg/profile/schemas/sandbox_profile.schema.json` — notification.h1.inbound.enabled object schema.
- `profiles/h1-triage.yaml` — lean H1 triage profile (created).
- `pkg/compiler/userdata.go` — H1InboundEnabled param + h1InboundEnabled() helper; poller heredoc, unit, binary download, notify.env slot, systemctl lines (all gated).
- `pkg/compiler/userdata_h1_byte_identity_test.go` — TestUserdataH1EnabledRendersPoller (active half).
- `internal/app/cmd/create_h1_inbound.go` — notificationH1Inbound repointed at the real field (type NotificationH1InboundSpec).
- `internal/app/cmd/create.go` — Step 11g H1 inbound provisioning.

## Decisions Made
See key-decisions in frontmatter: `sourceAccess: none` for h1-triage; on-box `target` from KM_SANDBOX_ALIAS; forward-compat KM_H1_REPLY_AGENT; in-struct gate flip for the active test.

## Deploy note (memory project_schema_change_requires_km_init / feedback_km_init_full_apply)

`notification.h1.inbound` is a SandboxProfile **schema addition** → the management/create-handler Lambdas must be refreshed with **`km init --sidecars`** before a remote `km create profiles/h1-triage.yaml` will validate. (Plan 08's `KM_H1_*` env-block changes additionally require a full `km init --dry-run=false`; the two deploys compose.) Existing sandboxes need `km destroy && km create` to gain the poller. No production HackerOne program is a target — only the operator's HackerOne Sandbox account (Plan 10 UAT).

## Deviations from Plan

### Auto-resolved (no user input needed)

**1. [Rule 3 - Blocking] h1-triage profile uses `sourceAccess: none`**
- **Found during:** Task 1.
- **Issue:** The plan said "fork github-review.yaml". github-review declares `sourceAccess.mode: allowlist` + a `github:` block, but `sourceAccess` is schema-required and the H1 triage agent needs no git clone (it works entirely over the HackerOne API). Copying github-review's `github:` allowedRepos verbatim would be misleading dead config.
- **Fix:** Set `sourceAccess.mode: none` (valid enum) and dropped the github block. `km validate` clean.
- **Files modified:** profiles/h1-triage.yaml.
- **Committed in:** `5f78fb3d`.

**2. [Scope] notificationH1Inbound return type changed GitHub→H1**
- The Plan-08 stub returned `*NotificationGitHubInboundSpec` (the field didn't exist yet). Plan 09 births `NotificationH1InboundSpec` and repoints the accessor at it. `provisionH1InboundQueue` only touches `.Enabled` (present on both), so no other call-site changed.
- **Committed in:** `177dec8b`.

---

**Total deviations:** 2 auto-resolved (1 blocking, 1 scope). Zero architectural.
**Impact on plan:** None — both verification commands pass (`go build ./... && km validate profiles/h1-triage.yaml`; `go test ./pkg/compiler -run "TestUserdataH1ByteIdentity|H1"`).

## Issues Encountered
- First run of TestUserdataH1EnabledRendersPoller failed on the `(report_id, target)` key substring: inside the `'H1INBOUND'` heredoc the `\"` escapes are LITERAL backslash-quote, so the expected string needed a Go raw string (backslashes preserved) rather than a double-quoted one. Fixed by switching the assertion to a backtick literal. All other substrings matched first try.
- Pre-existing `internal/app/cmd` flake `TestUnlockCmd_RequiresStateBucket` (expired SSO token, unrelated to this plan per Plan-08 SUMMARY) was NOT exercised — the targeted `-run 'H1Inbound|H1'` sweep passes clean.

## Next Phase Readiness
- Sandbox-side consumer complete: the bridge (Plans 07–08) dispatches into the FIFO; this poller drains it, resumes per-(report_id,target) sessions, and replies internal-by-default.
- Plan 10 (Wave 6 E2E/UAT) can now run the full live loop against the operator's HackerOne Sandbox account: webhook → bridge → queue → poller → agent → km-h1 INTERNAL reply, plus the `/reply_to_researcher` allowlist-gated external path.

## Self-Check: PASSED

- Created/modified files all present on disk: profiles/h1-triage.yaml, pkg/profile/types.go, pkg/compiler/userdata.go, internal/app/cmd/create.go, pkg/compiler/userdata_h1_byte_identity_test.go.
- Commits `5f78fb3d` + `177dec8b` present in git history.
- `go build ./...` clean; `make build` succeeds (km v0.4.906); `go test ./pkg/compiler -run 'ByteIdentity|H1'` green (dormancy preserved + enabled-profile contains the poller); `scripts/validate-all-profiles.sh` → all 22 profiles valid.

---
*Phase: 103-hackerone-comment-trigger-bridge*
*Completed: 2026-06-10*
