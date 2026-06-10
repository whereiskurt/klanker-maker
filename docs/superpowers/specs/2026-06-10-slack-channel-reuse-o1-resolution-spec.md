# Design Spec — O(1) Slack Channel Resolution on Alias Reuse (kill the create-time hang)

> Status: **DRAFT for review** · Author: pairing session 2026-06-10 (Kurt + Claude) · No code written yet.
> Scope: `internal/app/cmd/create_slack.go`, `pkg/slack/*`, create-handler path. Design only.

---

## 0. TL;DR

`km create` on a **reused `--alias`** (whose per-sandbox Slack channel already exists) can hang for the
full **900s create-handler Lambda timeout** and then get killed, leaving the sandbox stuck in `starting`.
Root cause: the Slack channel-resolution step can fall into an **unbounded `conversations.list`
enumeration** of the entire workspace (thousands of channels in a corporate Slack, Tier-2 rate-limited),
with **no page cap and no sub-timeout**. Disabling Slack makes the create succeed instantly — confirming
the hang is entirely in Slack channel resolution, not terragrunt/connectivity/throttling-in-general.

There is **no Slack-native O(1) name→ID lookup** (`conversations.info` needs an ID; `conversations.list`
has no name filter). So the only O(1) path is a **local mapping we control**. We already have one (the
SSM by-name cache, `05a4415e`), but it can silently degrade into the O(N) scan. **The fix is to make the
local-mapping path authoritative and to guarantee the resolution step can never run unbounded.**

Three layers, in priority order:

- **P0 (safety net):** Bound Slack resolution — a wall-clock sub-context (≈45–60s) + a hard page cap on
  the enumeration. Worst case becomes "fail fast with an actionable error in <1 min," never "wedge the
  create for 15 min." This is correct *regardless* of why the cache is being bypassed.
- **P1 (the O(1) optimization):** Look up the stored channel ID **before** `conversations.create`, and
  stop treating a transient `conversations.info` hiccup as a reason to enumerate the whole workspace.
- **P2 (robustness):** Make the mapping authoritative & durable (DDB keyed by `alias`, SSM by-name as
  back-compat), plus an operator escape hatch (`km slack adopt`) for genuinely orphaned channels.

---

## 1. Incident context (so this reads cold)

While bringing up the `github-bot` warm box (profile `profiles/github-review.yaml`, per-sandbox Slack
enabled), repeated `destroy`+`create` cycles on the **same alias `github-bot`** produced creates that
hung indefinitely (observed twice via the remote create-handler, once via `--local`). Each hung create:

- Wrote the `km-sandboxes` row as `starting`, then **never** progressed — no instance, no terragrunt
  state object, no held state lock for the whole window.
- The create-handler Lambda logged up to `"downloaded km-config.yaml"` then went silent (the `km create`
  subprocess buffers output) and was **killed at exactly 900s** (`END RequestId … 06:20:13`, no terragrunt
  output ever emitted).
- Setting `notification.slack.enabled: false` → create succeeds in the normal ~2 min. **This is the
  decisive signal: the hang is in Slack channel resolution (Step 6c), before terragrunt.**

The per-sandbox channel `sb-github-bot-sec` (= `C0B91RA9CPR`) already existed because the profile sets
`archiveOnDestroy: false`, so every recreate is guaranteed to hit `conversations.create → name_taken`.

### What we ruled out (with evidence)

| Hypothesis | Verdict | Evidence |
|---|---|---|
| Network/connectivity to Slack | **No** | `conversations.{info,create,join,inviteShared}` + `users.lookupByEmail` all returned correctly in 0.20–0.53s from the operator box. |
| General rate-limiting / bounded backoff | **No** | Bounded backoff is capped (30s/attempt, finite attempts) — cannot produce a 15-min stall. `conversations.list?limit=1` returned HTTP 200, no `Retry-After`. |
| Stale / wrong by-name cache | **No** | Cache key `/sec/slack/channel-id-by-name/sb-github-bot-sec` = `C0B91RA9CPR` since 00:35 (written by create-handler), correct. |
| Missing IAM read on the cache key | **No** | `sec-create-handler-ssm` grants `ssm:GetParameter` on `arn:…:parameter/sec/*`, which covers the key. |
| Deployed binary predates the O(1) cache | **No** | `05a4415e` (the O(1) cache) is in tag `v0.4.901`; operator on 0.4.905; deployed `toolchain/km` uploaded 06-10 03:49 UTC. |
| SSM prefix mismatch (read vs write key) | **No** | `GetSsmPrefix()` → `/sec/` consistently; keys match. |
| terragrunt / EFS / volume / lock | **No** | No state object, no lock ever appeared; `slack.enabled:false` create works. |

### What remains (the actual defect)

The O(1) cache code exists, the value is correct, the key matches, IAM allows the read — **yet the O(N)
scan still ran for 15 minutes.** That means the cache's *validation gate* fell through to the scan. In
`resolveExistingChannelID` (create_slack.go:152) the branch is:

```
1. cachedID := SSM Get "channel-id-by-name/<name>"        # O(1)
   if cachedID != "" AND conversations.info(cachedID) == nil-err:   return cachedID   # O(1) success
   # else (info returned ANY error) → fall through ↓
2. FindChannelByName(name)  →  conversations.list, walk EVERY page  # O(N), UNBOUNDED, no sub-timeout
```

So **any** non-nil error from the `conversations.info` probe (transient 5xx, a momentary `ratelimited`, a
context blip, an SDK quirk) silently converts an O(1) cache hit into an unbounded workspace scan. And
`FindChannelByName` (pkg/slack) is explicitly unbounded — its own comment notes "the scan may have to
walk EVERY page," `limit: 1000`, freshly-created channels sort **last**.

> **Phase 0 (must do first): confirm the exact defeater.** Add temporary INFO logging (or pull the
> buffered subprocess output) to record which branch fired: cache-miss vs `conversations.info` error
> (and its Slack error code) vs scan entry. The design below is correct regardless, but confirming
> turns "leading hypothesis" into "known." See §9.

---

## 2. Problem statement

> A `km create` that resolves a per-sandbox Slack channel **must complete in bounded time**, and the
> common case (reusing an alias whose channel already exists) **must be O(1)** — a direct lookup, never a
> workspace-wide enumeration. A resolution failure must **fail fast with an actionable error**, never
> consume the create-handler's 900s budget.

### Non-goals
- Changing how the **bridge** routes inbound messages (channel→sandbox) — unaffected.
- Changing per-sandbox channel **naming** (`sb-{alias}-sec`) — unaffected.
- "Fixing" Slack's API — it has no name→ID lookup; we work around it locally.

---

## 3. Constraints & invariants

1. **No Slack name→ID API.** `conversations.info(id)` and `conversations.list` (no name filter) are the
   only primitives. O(1)-by-name ⇒ local mapping only.
2. **`alias` is the stable reuse key.** `sandbox_id` is regenerated each create, so any sandbox-id-keyed
   record is useless for reuse (see create_slack.go:115-118). The channel **name** is derived
   deterministically from `alias` (`sb-{alias}-sec`), so name and alias are equally good keys.
3. **Create-handler hard ceiling = 900s** (Lambda timeout). Slack resolution runs *before* terragrunt;
   any stall here burns the entire budget and wedges the sandbox in `starting`.
4. **`archiveOnDestroy: false` ⇒ orphan channels persist** ⇒ reuse **always** hits `name_taken`. (Even
   `archiveOnDestroy: true` reserves the name for Slack's ~30-day archive window, so `name_taken` is not
   avoidable by archiving.)
5. **Multi-install / prefix.** Mapping keys must be `resource_prefix`-scoped (already true for SSM under
   `/{prefix}/…`; DDB would key on prefix too).
6. **Tier-2 rate limits.** `conversations.list` and `conversations.create` are Tier-2. Rapid iterate
   (destroy/create loops) is exactly the operator pattern that erodes the budget — the design must not
   *depend* on generous limits.
7. **No infra is provisioned during Slack resolution.** ⇒ failing fast here is clean (nothing to roll
   back); the operator simply re-runs. This makes "fail fast" strictly safe.
8. **Back-compat:** existing SSM by-name cache entries must keep working; no breaking schema change to
   profiles; no required migration job.

---

## 4. Design options

### Option A — Harden the existing SSM by-name cache + bound the scan *(minimal)*
- Look up the by-name cache **before** `conversations.create`.
- On the `conversations.info` validation: only fall through on a **definitive** `channel_not_found`;
  treat transient errors as "trust the cached ID" (or bounded-retry the info call), never as "enumerate."
- Cap `FindChannelByName` (page limit + sub-context deadline); on exceed, **fail fast** with
  `channelOverride` guidance.
- **Pros:** smallest diff; reuses existing infra; no new state/schema.
- **Cons:** SSM by-name cache is best-effort (write is non-fatal, create_slack.go:126) so it can be cold
  on first reuse after an out-of-band channel; still one source of truth that can be missing.

### Option B — Authoritative DDB `alias → channelID` mapping *(robust)*
- New durable item (e.g., in `km-sandboxes` as an alias-keyed record, or a small dedicated table)
  `(resource_prefix, alias) → {channelID, channelName, updatedAt}`, written on channel create/resolve.
- Create looks up by alias first (O(1) DDB GetItem — already on the hot path, always permitted), then a
  single `conversations.info` liveness check.
- **Pros:** alias is the true reuse key; DDB is authoritative/transactional, always readable by the
  create-handler, survives SSM eviction; clean semantics.
- **Cons:** new schema item + write path; two stores during transition (DDB + legacy SSM by-name).

### Option C — Never enumerate; fail-fast + operator adopt *(bound-the-worst-case)*
- If no stored mapping **and** `conversations.create` returns `name_taken`, **do not scan at all** — fail
  fast with: *"channel `<name>` exists but km has no record of its ID; set
  `notification.slack.channelOverride=<id>` or run `km slack adopt <alias> <channelID>`."*
- Add `km slack adopt <alias> <channelID>` to seed the mapping for an orphaned channel.
- **Pros:** worst case = 1 create call + immediate clear error; **O(N) eliminated entirely**.
- **Cons:** a genuinely orphaned channel (no record, created out-of-band) needs one manual operator
  action — acceptable and rare, and far better than a 15-min wedge.

### Option D — `archiveOnDestroy: true` by default *(orthogonal, not sufficient)*
- Archiving on destroy doesn't avoid `name_taken` (30-day name reservation) and needs unarchive on reuse.
  **Rejected as a primary fix; mention only as a separate hygiene discussion.**

---

## 5. Recommended design (layered)

Adopt **A + C now**, with **B as the durability upgrade**. Concretely, restructure
`resolveSlackChannel`'s per-sandbox (Mode 2) path into a bounded, lookup-first state machine:

```
resolvePerSandboxChannel(alias, channelName):
  ctx := withTimeout(parent, SLACK_RESOLVE_BUDGET)        # P0 safety net, e.g. 45–60s

  # 1. O(1) lookup-first (no create, no scan)
  id := lookupStoredChannelID(alias, channelName)          # DDB(alias) → fallback SSM(by-name)
  if id != "":
     switch validateLive(ctx, id):                         # one conversations.info
        ok            → ensureBotMember(id); return id      # ← O(1) happy path for reuse
        channel_gone  → forgetMapping(alias, channelName); goto create   # definitive only
        transient     → return id (optimistic)              # DO NOT enumerate; join/post will surface errors
                                                            #   (decision: optimistic-use vs bounded-retry — see §11 Q2)

  # 2. No stored mapping → create
  id, err := conversations.create(channelName)
  if err == nil:
     storeMapping(alias, channelName, id); ensureBotMember(id); return id   # fresh channel

  if err == name_taken:
     # 3. Orphan channel with no local record. NEVER unbounded.
     if ALLOW_BOUNDED_SCAN:                                 # config/flag, default OFF or small cap
        id := boundedFindChannelByName(ctx, channelName, MAX_PAGES)   # cap pages AND honor ctx deadline
        if id != "": storeMapping(...); ensureBotMember(id); return id
     return failFast(
        "channel #<name> exists but km has no stored ID. Set notification.slack.channelOverride=<id>, "
        "or run `km slack adopt <alias> <channelID>` (find it: Slack → channel → About → Channel ID).")

  return wrap(err)   # other create errors propagate
```

**Key properties**
- **Bounded always (P0):** `SLACK_RESOLVE_BUDGET` sub-context + `MAX_PAGES` cap ⇒ resolution can never
  exceed ~1 min, even on an unforeseen Slack stall. The create either succeeds or fails fast well under
  900s. *This single change would have turned the incident from a 15-min wedge into a clear 60s error.*
- **O(1) reuse (P1):** lookup-before-create + the transient-error rule removes the
  `info-hiccup → full-scan` cliff that bit us.
- **No silent O(N) (C):** enumeration is either OFF (fail-fast) or hard-capped; either way the operator
  gets `channelOverride` / `km slack adopt` guidance.
- **Authoritative store (P2/B):** DDB-by-alias is read first; SSM-by-name remains a read fallback for
  back-compat and is still written for older readers.

---

## 6. Component-level changes (design only — no code here)

| Area | File(s) | Change |
|---|---|---|
| Resolution state machine | `internal/app/cmd/create_slack.go` (`resolveSlackChannel`, `resolveExistingChannelID`) | Reorder to lookup-first; classify `conversations.info` errors (definitive vs transient); replace the unconditional fall-through-to-scan; wrap in a sub-context deadline. |
| Bounded enumeration | `pkg/slack` (`FindChannelByName`) | Accept a max-pages cap and honor `ctx` cancellation per page; return a typed "cap exceeded" error distinct from `ratelimited`. |
| Authoritative mapping (P2) | `pkg/aws` (DDB helpers) + create_slack.go | `alias → channelID` read/write; choose `km-sandboxes` alias-record vs small dedicated table (§11 Q3). |
| Liveness classification | `pkg/slack` (`ChannelInfo` callers) | Distinguish `channel_not_found` from transient errors so only the former invalidates the mapping. |
| Operator escape hatch (C) | `internal/app/cmd/slack*.go` | `km slack adopt <alias> <channelID>` (+ maybe `km slack relink`) to seed/repair the mapping; validate `^C[A-Z0-9]+$` and bot membership. |
| Config knobs | config + Lambda env | `SLACK_RESOLVE_BUDGET` (default ~45–60s), `MAX_PAGES` (default small or 0=off). Sensible defaults; not required in `km-config.yaml`. |
| Observability | both | INFO logs naming the path taken: `slack_resolve path=cache_hit|created|scan_capped|failfast id=… ms=…`. |

No SandboxProfile **schema** change required. `channelOverride` remains the documented manual escape and
stays mutually-exclusive with inbound (unchanged).

---

## 7. Failure modes & edge cases (must be covered by tests)

1. **Reuse, cache hit, channel live** → O(1), no create, no scan. *(the broken-today happy path)*
2. **Reuse, cache hit, `conversations.info` transient error** → must NOT enumerate (today's bug).
3. **Reuse, cache hit, channel actually deleted (`channel_not_found`)** → invalidate, recreate cleanly.
4. **Fresh alias, no mapping, name free** → create, store mapping.
5. **Fresh alias, name taken out-of-band, no mapping** → fail fast with `adopt`/`channelOverride`
   guidance (or bounded scan if enabled), never unbounded.
6. **Archived channel reserving the name (30-day window)** → `name_taken`, scan returns empty →
   clear "archived/reserved; unarchive or pick unique alias" error (already worded; keep).
7. **`SLACK_RESOLVE_BUDGET` exceeded** → fail fast, create abortable (no infra yet), clear next step.
8. **Rate-limited mid-resolution** → bounded retry then fail fast; never block to 900s.
9. **Multi-install:** two installs, same alias, different prefixes → prefix-scoped keys don't collide.
10. **External operator (Connect) invite** → unchanged; idempotent on reuse (already returns
    `AlreadyMember`).
11. **`--local` vs remote creds:** `--local` uses `klanker-terraform` — verify it can read the mapping
    store (DDB/SSM) or it'll behave as a permanent cache-miss. (Possible secondary cause of the `--local`
    hang; confirm in Phase 0.)

---

## 8. Backward compatibility & migration

- **Existing SSM by-name cache** entries are read (as fallback) and still written → zero-touch upgrade.
- **DDB alias mapping (P2)** is schema-on-write: absent rows simply miss → fall back to SSM/by-name →
  then create/fail-fast. No migration job. First successful resolve backfills the mapping.
- **No profile schema change**; existing profiles work unchanged.
- Behavior change is strictly: "reuse no longer enumerates / can't hang." A first reuse with neither DDB
  nor SSM record (e.g. channel created out-of-band) now **fails fast** instead of slow-scanning — call
  this out in release notes with the `km slack adopt` remedy.

---

## 9. Phase 0 — confirm the defeater before building (cheap, ~30 min)

1. Add temporary INFO logging in `resolveExistingChannelID`: log `cachedID`, the `conversations.info`
   error code (if any), and "entering FindChannelByName scan".
2. Reproduce one reuse create (remote), capture the create-handler logs **before** the 900s kill (tail
   live; or shorten the create-handler timeout temporarily to force an early flush).
3. Expected confirmation: either `cachedID==""` (cache-read miss in this context) **or**
   `conversations.info` returns a non-nil error → scan. Record which.
4. This determines whether P1's emphasis is the info-gate classification (most likely) or a cache-read
   issue specific to an execution context (e.g., `--local` creds).

---

## 10. Deployment notes (lessons from the incident)

- Resolution runs inside the **create-handler binary** (`toolchain/km` in S3), so shipping the fix
  requires `make build-lambdas` (clean) **+** `km init` to re-upload the toolchain — not just a local
  `make build`. (We confirmed the deployed binary was current this time, but call it out.)
- `SLACK_RESOLVE_BUDGET` / `MAX_PAGES`, if exposed as Lambda env, require a full `km init --dry-run=false`
  (env-block change), not `--sidecars` — consistent with the other env knobs in this repo.

---

## 11. Open questions for the morning (decisions to confirm)

1. **P0 budget value:** `SLACK_RESOLVE_BUDGET` = 45s or 60s? (Must be ≪ 900s, ≫ a normal create+info
   round-trip.)
2. **Transient `conversations.info` error policy:** optimistic-use the cached ID (simplest; later
   join/post surfaces real errors) **vs** bounded-retry the info call (2× w/ backoff) then fail fast?
   Recommendation: bounded-retry then optimistic-use; never enumerate.
3. **Authoritative store (P2):** reuse `km-sandboxes` with an `alias`-keyed record, or a small dedicated
   `km-slack-channels` table? (Leaning: alias record on `km-sandboxes` to avoid new infra.)
4. **Keep a bounded scan at all, or pure fail-fast (Option C)?** I.e., is `MAX_PAGES` default `0` (off,
   fail-fast) or a small N (e.g., 5 pages)? Leaning **off by default** + `km slack adopt`, because the
   scan is precisely the thing that bit us and the workspace is huge.
5. **`km slack adopt` scope:** just seed the mapping, or also validate membership + cache write-through?
6. **`archiveOnDestroy` hygiene (Option D):** separate ticket? Default stays `false`.

---

## 12. Appendix — quick reference

- Hang signature: `km-sandboxes` row `starting`, no instance, no state object, no held lock; create-handler
  silent after `"downloaded km-config.yaml"`; killed at `END RequestId … +900s`.
- O(1) cache today: SSM `"/{prefix}/slack/channel-id-by-name/{channelName}"` (create_slack.go:112-182);
  written best-effort on create/resolve; read first in `resolveExistingChannelID`, but falls through to
  `FindChannelByName` (pkg/slack) on **any** `conversations.info` error.
- `FindChannelByName`: `conversations.list`, `limit:1000`, `exclude_archived:true`, walks **every** page
  (new channels sort last), unbounded, no sub-timeout — the O(N) that wedged the create.
- Slack primitives: no name→ID lookup; `SlackAPI` (create_slack.go:85) = `CreateChannel`,
  `FindChannelByName`, `JoinChannel`, `InviteShared`, `ChannelInfo`, `LookupUserByEmail`,
  `InviteUserToChannelStrict`.
- Relevant commit: `05a4415e fix(slack): O(1) channel reuse + rate-limit retry for name_taken recovery`
  (in `v0.4.901`).
