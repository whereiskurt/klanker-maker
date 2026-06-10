# Phase 104: Slack channel O(1) resolution on alias reuse - Context

**Gathered:** 2026-06-10
**Status:** Ready for planning
**Source:** Design spec (`docs/superpowers/specs/2026-06-10-slack-channel-reuse-o1-resolution-spec.md`) + implementation plan (`docs/superpowers/plans/2026-06-10-slack-channel-o1-resolution.md`), authored in a pairing session. All decisions locked.

<domain>
## Phase Boundary

**Delivers:** `km create` on a reused `--alias` (whose per-sandbox Slack channel already exists) resolves the existing channel in **bounded, O(1)** time — never the unbounded `conversations.list` workspace scan that wedges the 900s create-handler Lambda and strands the sandbox in `starting`.

**Incident that triggered it (2026-06-10):** bringing up the `github-bot` warm box (`profiles/github-review.yaml`, per-sandbox Slack, `archiveOnDestroy:false` ⇒ every recreate hits `conversations.create → name_taken`) hung the create-handler for the full 900s. `slack.enabled:false` made create succeed in ~2 min ⇒ the hang is entirely in Slack channel resolution (create Step 6c).

**Root cause (confirmed against code, not hypothesis):**
- `resolveExistingChannelID` (`internal/app/cmd/create_slack.go:152`) gates the SSM by-name cache hit on `conversations.info(cachedID) == nil-err`.
- `ChannelInfo` (`pkg/slack/client.go:749`) returns the RAW Slack error — no `channel_not_found` vs transient classification.
- ⇒ ANY transient info error (a momentary `ratelimited`/5xx/context blip) falls through to `FindChannelByName` (`pkg/slack/client.go:605`): a bare `for{}` with `limit:1000`, `exclude_archived:true`, **no page cap, no sub-deadline** (freshly-created channels sort LAST, so it walks every page).
- Client per-request timeout is only 10s (`slack.NewClient(token, nil)` at `create.go:597`), so the 15-min wedge is the many-page walk + retries, not a single hung request.
- The SSM by-name cache value was PRESENT and CORRECT during the incident (`/sec/slack/channel-id-by-name/sb-github-bot-sec`=`C0B91RA9CPR`) — so this is NOT a cache miss; the defeater is the info-gate fall-through.

**In scope:** the three-layer fix (P0 bound, P1 lookup-first + classify, P2 durable store + adopt) at create time.

**Out of scope:** the Slack bridge (NOT a consumer — no `lambda-slack-bridge` changes); `archiveOnDestroy` default-flip hygiene (separate ticket); any SandboxProfile schema change.
</domain>

<decisions>
## Implementation Decisions (all LOCKED)

### P0 — Bound resolution (safety net)
- Wrap per-sandbox Slack channel resolution (create Step 6c / Mode-2 of `resolveSlackChannel`) in a wall-clock sub-context. Budget default **45s** via `KM_SLACK_RESOLVE_BUDGET` (seconds). Far below the 900s ceiling, far above a normal create+info round-trip.
- `FindChannelByName` gains a **max-pages cap** and honors `ctx` cancellation per page; returns a typed `ErrScanCapExceeded` (distinct from `*SlackAPIError{Code:"ratelimited"}`) so callers emit adopt/channelOverride guidance, not a retry-shortly hint.
- Page cap default **OFF**: `KM_SLACK_MAX_SCAN_PAGES=0` ⇒ `FindChannelByName` returns `ErrScanCapExceeded` with NO HTTP call ⇒ fail-fast. Opt-in `>0` runs a bounded scan.

### P1 — Lookup-first O(1) hot path
- Look up the stored channel ID **before** `conversations.create`.
- Classify `conversations.info` errors via a new `IsChannelNotFound(err)` helper: ONLY a definitive `channel_not_found` invalidates the stored mapping (→ recreate). Every other error (ratelimited, 5xx, network, context) is transient.
- Transient info error policy: **bounded-retry 2× (500ms backoff) then optimistically USE the stored ID** — never enumerate. (This is the exact defeater that caused the incident.)

### P2 — Durable authoritative store
- New dedicated **`km-slack-channels` DynamoDB table**, hash_key `alias`, **no TTL** (mapping must persist across destroy/recreate; stale rows self-heal via the `channel_not_found` recreate path), PAY_PER_REQUEST, SSE on.
- Read **first** (authoritative) during resolution; the existing SSM by-name cache stays as a back-compat read/write fallback. Write-through to BOTH on create/resolve.
- **Rejected: storing on `km-sandboxes`** — destroy DELETES that row (`destroy.go:583/779`) and `ListAllSandboxesByDynamo` SCANS the table (`sandbox_dynamo.go:518`), so a synthetic alias item would pollute `km list`. A dedicated table avoids both. (Note: this overrides the spec §11 Q3 lean toward a km-sandboxes record — operator chose the dedicated table after the destroy-deletes-row + Scan-pollution findings surfaced.)
- DDB lookup keyed on `alias`; skip when alias is empty (no stable reuse key — channel name then derives from the regenerating sandbox_id and never collides).

### C — Operator escape hatch
- `km slack adopt <alias> <channelID>`: validate `^C[A-Z0-9]+$` + confirm bot membership (`conversations.info` is_member) + write-through to BOTH the DDB store and the SSM by-name cache. Bad ID / non-member rejected with actionable guidance ("find the ID in Slack → channel → About → Channel ID").
- `notification.slack.channelOverride` remains the documented zero-lookup manual escape (Mode 3, unchanged).

### Observability
- Single INFO log per resolution: `slack_resolve path=cache_hit|cache_optimistic|created|scan_capped|failfast ms=… id=…`. This ships the spec's Phase-0 defeater-confirmation inline — the operator's next real reuse confirms which branch fires, no separate remote-repro needed.

### Deploy surface (this repo has been bitten by incomplete surface before)
- TF module `infra/modules/dynamodb-slack-channels/v1.0.0` (mirror `dynamodb-slack-threads`) + live unit `infra/live/use1/dynamodb-slack-channels/terragrunt.hcl` + `init.go` regionalModules entry.
- create-handler IAM: km-operator-policy var (`slack_channels_table_name`, default "") + IAM policy (GetItem/PutItem/DescribeTable, count-gated) → create-handler var + wiring → live-unit dependency + input.
- Config: `GetSlackChannelsTableName()` getter (`{prefix}-slack-channels` derivation) + v2→v merge-list entry (`project_config_key_merge_list`).
- Go: `pkg/aws.SlackChannelStore` (GetByAlias/UpsertByAlias) + wire into `resolveSlackChannel` and `km create`.
- `km doctor`: table-existence check only (NOT orphan-row scan — alias rows are not per-sandbox and must never be auto-deleted).
- Build order: `make build` the km binary BEFORE `km init` (`project_make_build_precedes_km_init` — a stale binary silently skips the new module). Deploy = `make build-lambdas` + `km init --dry-run=false` (new table + IAM + env-block ⇒ full apply, NOT `--sidecars`). No SandboxProfile schema change ⇒ no `--sidecars`, existing sandboxes unaffected (create-time fix).

### Claude's Discretion
- Exact internal helper factoring (e.g. `lookupStoredChannelID` / `storeChannelMapping` / `validateStoredChannel`), test-fake shapes, and the `slackResolveSleep` ctx-aware sleep var.
- Precise doctor check wording/placement.
- Whether the bounded-scan opt-in surfaces as env-only or also a km-config.yaml key (lean env-only to avoid schema churn).
</decisions>

<specifics>
## Specific Ideas

- Plan breakdown already set in the roadmap (5 plans): 104-01 P0+P1 core → 104-02 table+live+init.go → 104-03 IAM+config+store+wiring → 104-04 adopt+doctor → 104-05 docs+deploy-audit+live UAT.
- Failure-mode test matrix (design spec §7): stored-live (O(1)); stored + transient info (no scan); stored + `channel_not_found` (recreate); fresh create (write-through); name_taken + no mapping + scan-off (fail-fast); budget exceeded; archived-name reservation (existing wording kept); multi-install prefix isolation; `--local` creds can read the store.
- TDD throughout (failing test → minimal impl → verify → commit), mirroring `internal/app/cmd/create_slack_test.go` and `pkg/slack/client_test.go` fake patterns.
- Reference implementations to mirror: `dynamodb-slack-threads` (table module + live unit), `DDBThreadStore` in `pkg/slack/bridge/aws_adapters.go` (DDB helper), `SlackThreadsTableName`/`GetSlackThreadsTableName` (config), the `dynamodb_slack_threads` IAM resource in `km-operator-policy`.
- Master TDD plan with full code-bearing steps: `docs/superpowers/plans/2026-06-10-slack-channel-o1-resolution.md`.
</specifics>

<deferred>
## Deferred Ideas

- `archiveOnDestroy: true` default-flip hygiene — separate ticket (archiving doesn't avoid `name_taken`: Slack reserves the name ~30 days).
- Bridge-side channel-name resolution — N/A (bridge is not a consumer).
- A km-config.yaml knob for the scan cap / budget — lean env-only for now (no schema churn); revisit if operators need per-install tuning.
</deferred>

---

*Phase: 104-slack-channel-o-1-resolution-on-alias-reuse*
*Context gathered: 2026-06-10 from vendored design spec + implementation plan*
