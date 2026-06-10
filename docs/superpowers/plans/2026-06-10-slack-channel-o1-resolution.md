# O(1) Slack Channel Resolution on Alias Reuse — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `km create` on a reused `--alias` resolve its existing per-sandbox Slack channel in bounded, O(1) time — never an unbounded `conversations.list` workspace scan that wedges the 900s create-handler Lambda.

**Architecture:** Three layers from the spec (`/Users/khundeck/Downloads/slack-channel-reuse-o1-resolution-spec.md`):
- **P0 (bound):** wrap Slack channel resolution in a wall-clock sub-context (~45s) and hard-cap the enumeration (default OFF). Worst case = fail-fast in <1 min.
- **P1 (O(1) hot path):** look up a stored channel ID *before* `conversations.create`; classify `conversations.info` errors so only a definitive `channel_not_found` invalidates the mapping — a transient blip is bounded-retried then optimistically trusted, **never** a reason to enumerate.
- **P2 (durable authoritative store):** a dedicated `km-slack-channels` DynamoDB table keyed by `alias`, written on create/resolve, read first; SSM by-name cache remains as a back-compat read/write fallback. `km slack adopt <alias> <channelID>` seeds the mapping for an orphaned channel.

**Tech Stack:** Go, AWS SDK v2 (DynamoDB, SSM), Terraform/Terragrunt, Slack Web API. Tests are Go `testing` with table-driven fakes (mirror `internal/app/cmd/create_slack_test.go` and `pkg/slack/client_test.go`).

**Confirmed design decisions (from pairing):**
- Resolve budget default **45s** (`KM_SLACK_RESOLVE_BUDGET`, seconds).
- Bounded scan **OFF by default** (`MAX_PAGES=0` ⇒ fail-fast) — `KM_SLACK_MAX_SCAN_PAGES` opt-in.
- Transient `conversations.info` error → **bounded-retry 2× then optimistic-use** the cached ID; never enumerate.
- Authoritative store = **dedicated `km-slack-channels` DDB table** (PK `alias`). SSM by-name cache kept as fallback.
- `km slack adopt` = validate `^C[A-Z0-9]+$` + confirm bot membership + write-through to DDB **and** SSM.
- `archiveOnDestroy` hygiene = out of scope (separate ticket).
- Bridge is **not** a consumer — no `lambda-slack-bridge` changes.

**Deploy surface (verified — the part this repo has been bitten by before):**
TF module + live terragrunt unit + `init.go` regionalModules entry + create-handler IAM (var + policy + wiring + live input) + config getter + Go DDB helper + resolver wiring + `km slack adopt` + docs. After merge: `make build` (NOT just `make build-lambdas`) so the new module is in the `km` binary, then `make build-lambdas` + `km init --dry-run=false` (new table + IAM + env block; NOT `--sidecars`). Existing sandboxes need `km destroy && km create` only if they must re-resolve; the resolver fix is create-time so it applies to the next create.

---

## File Structure

**P0/P1 — the bug fix (no infra):**
- `pkg/slack/client.go` — add `SlackMaxScanPages`/typed `errScanCapExceeded`; make `FindChannelByName` accept a max-pages cap and honor ctx per page. Add an `IsChannelNotFound(err)` classifier.
- `internal/app/cmd/create_slack.go` — rework `resolveExistingChannelID` + the Mode-2 branch of `resolveSlackChannel` into a bounded, lookup-first state machine; add `SlackResolveBudget`; add the `slack_resolve path=… ms=…` INFO log.

**P2 — durable store (infra + Go):**
- `infra/modules/dynamodb-slack-channels/v1.0.0/{main,variables,outputs}.tf` — new table module (mirror `dynamodb-slack-threads`).
- `infra/live/use1/dynamodb-slack-channels/terragrunt.hcl` — new live unit.
- `internal/app/cmd/init.go` — register `dynamodb-slack-channels` in regionalModules.
- `infra/modules/km-operator-policy/v1.0.0/{main,variables}.tf` — IAM grant (GetItem/PutItem) for create-handler role.
- `infra/modules/create-handler/v1.0.0/{main,variables}.tf` + `infra/live/use1/create-handler/terragrunt.hcl` — plumb `slack_channels_table_name`.
- `internal/app/config/config.go` — `SlackChannelsTableName` field + `GetSlackChannelsTableName()`.
- `pkg/aws/slack_channels.go` (NEW) — `SlackChannelStore` (GetByAlias/UpsertByAlias) + client interface.
- `internal/app/cmd/create_slack.go` + `create.go` — wire the store into resolution + write-through.

**C — operator escape hatch:**
- `internal/app/cmd/slack.go` (or `slack_adopt.go`) — `km slack adopt` subcommand.

**Docs:**
- `docs/slack-notifications.md` — new "O(1) channel resolution / km slack adopt" section.
- `CLAUDE.md` — phase note + deploy sequence.

---

## Task 1: Bound `FindChannelByName` (page cap + ctx-per-page + typed error)

**Files:**
- Modify: `pkg/slack/client.go` (FindChannelByName ~605-635; vars block ~196-207)
- Test: `pkg/slack/client_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `pkg/slack/client_test.go`:

```go
func TestFindChannelByName_PageCapExceeded(t *testing.T) {
	// Server always returns a full page with a next cursor and never the target,
	// so a finite page cap must stop the walk with a typed cap-exceeded error.
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channels":[{"id":"C1","name":"other"}],"response_metadata":{"next_cursor":"more"}}`))
	}))
	defer srv.Close()
	c := NewClient("xoxb-test", srv.Client())
	c.baseURL = srv.URL

	_, err := c.FindChannelByName(context.Background(), "sb-target", 3)
	if !errors.Is(err, ErrScanCapExceeded) {
		t.Fatalf("want ErrScanCapExceeded, got %v", err)
	}
	if pages != 3 {
		t.Fatalf("want exactly 3 pages walked, got %d", pages)
	}
}

func TestFindChannelByName_ZeroCapDisablesScan(t *testing.T) {
	// maxPages==0 means "do not scan at all" — return ErrScanCapExceeded without any HTTP call.
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"ok":true,"channels":[],"response_metadata":{"next_cursor":""}}`))
	}))
	defer srv.Close()
	c := NewClient("xoxb-test", srv.Client())
	c.baseURL = srv.URL

	_, err := c.FindChannelByName(context.Background(), "sb-target", 0)
	if !errors.Is(err, ErrScanCapExceeded) {
		t.Fatalf("want ErrScanCapExceeded for zero cap, got %v", err)
	}
	if called {
		t.Fatal("zero cap must not make any HTTP call")
	}
}

func TestFindChannelByName_CtxCancelledMidScan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel() // cancel after the first page is served
		_, _ = w.Write([]byte(`{"ok":true,"channels":[{"id":"C1","name":"other"}],"response_metadata":{"next_cursor":"more"}}`))
	}))
	defer srv.Close()
	c := NewClient("xoxb-test", srv.Client())
	c.baseURL = srv.URL

	_, err := c.FindChannelByName(ctx, "sb-target", 100)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
```

Add `"errors"` and `"net/http"`/`"net/http/httptest"` imports if not already present.

- [ ] **Step 2: Run tests to verify they fail to compile / fail**

Run: `go test ./pkg/slack/ -run TestFindChannelByName -v`
Expected: FAIL — `ErrScanCapExceeded` undefined and `FindChannelByName` takes 2 args not 3.

- [ ] **Step 3: Implement the bounded scan**

In `pkg/slack/client.go`, add near the rate-limit vars (~207):

```go
// ErrScanCapExceeded is returned by FindChannelByName when the page cap is hit
// before a match is found (including maxPages==0, which disables scanning
// entirely). Distinct from a *SlackAPIError{Code:"ratelimited"} so callers can
// give "set channelOverride / run km slack adopt" guidance rather than a
// retry-shortly hint.
var ErrScanCapExceeded = errors.New("slack: conversations.list scan exceeded page cap")
```

Replace `FindChannelByName` (lines ~605-635) with:

```go
// FindChannelByName scans public channels via conversations.list and returns
// the first channel whose name exactly matches. Returns ("", nil) if the scan
// completes with no match. The scan is BOUNDED: it walks at most maxPages pages
// and aborts on ctx cancellation between/within pages. maxPages==0 disables the
// scan entirely (returns ErrScanCapExceeded without any HTTP call) — the safe
// default for huge workspaces where enumeration is the very thing that wedges
// create. A cap hit returns ErrScanCapExceeded (NOT a SlackAPIError), so callers
// emit channelOverride / km slack adopt guidance rather than a rate-limit hint.
func (c *Client) FindChannelByName(ctx context.Context, name string, maxPages int) (string, error) {
	if maxPages <= 0 {
		return "", ErrScanCapExceeded
	}
	cursor := ""
	for page := 0; page < maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		body := map[string]any{
			"types":            "public_channel",
			"limit":            1000,
			"exclude_archived": true,
		}
		if cursor != "" {
			body["cursor"] = cursor
		}
		resp, err := c.callJSON(ctx, "conversations.list", body)
		if err != nil {
			return "", err
		}
		for _, ch := range resp.Channels {
			if ch.Name == name {
				return ch.ID, nil
			}
		}
		if resp.ResponseMetadata.NextCursor == "" {
			return "", nil // scan completed, no match
		}
		cursor = resp.ResponseMetadata.NextCursor
	}
	return "", ErrScanCapExceeded
}
```

- [ ] **Step 4: Update the existing caller + test to the new signature**

`pkg/slack/client_test.go` already has `TestFindChannelByName_RetriesOnRateLimit` / `TestFindChannelByName_RateLimitExhausted` calling the 2-arg form — update those call sites to pass a generous cap (e.g. `100`). The only production caller is `resolveExistingChannelID` (Task 3) — leave it for now; it will not compile until Task 3, so temporarily pass `1000` there if you need a green build between tasks, but Task 3 replaces it.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./pkg/slack/ -run TestFindChannelByName -v`
Expected: PASS (all four — three new + the two updated rate-limit tests).

- [ ] **Step 6: Commit**

```bash
git add pkg/slack/client.go pkg/slack/client_test.go
git commit -m "feat(slack): bound FindChannelByName with page cap + ctx-per-page (P0)"
```

---

## Task 2: Classify `conversations.info` errors (definitive vs transient)

**Files:**
- Modify: `pkg/slack/client.go` (near ChannelInfo ~746)
- Test: `pkg/slack/client_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestIsChannelNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"definitive", &SlackAPIError{Method: "conversations.info", Code: "channel_not_found"}, true},
		{"transient ratelimited", &SlackAPIError{Method: "conversations.info", Code: "ratelimited"}, false},
		{"nil", nil, false},
		{"network", errors.New("dial tcp: timeout"), false},
	}
	for _, tc := range cases {
		if got := IsChannelNotFound(tc.err); got != tc.want {
			t.Errorf("%s: IsChannelNotFound=%v want %v", tc.name, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/slack/ -run TestIsChannelNotFound -v`
Expected: FAIL — `IsChannelNotFound` undefined.

- [ ] **Step 3: Implement the classifier**

Add to `pkg/slack/client.go` after `ChannelInfo`:

```go
// IsChannelNotFound reports whether err is the definitive Slack
// "channel_not_found" response (a deleted/invalid channel) — the ONLY
// conversations.info error that should invalidate a stored channel mapping.
// Every other error (ratelimited, transient 5xx, network) is treated as a hiccup
// the caller bounded-retries / trusts optimistically, never as a reason to
// enumerate the workspace.
func IsChannelNotFound(err error) bool {
	var apierr *SlackAPIError
	return errors.As(err, &apierr) && apierr.Code == "channel_not_found"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/slack/ -run TestIsChannelNotFound -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/slack/client.go pkg/slack/client_test.go
git commit -m "feat(slack): IsChannelNotFound classifier for info-error gating (P1)"
```

---

## Task 3: Bounded, lookup-first resolver state machine (P0+P1 core)

This is the heart of the fix. It restructures `resolveExistingChannelID` so a stored ID is validated with bounded-retry-then-optimistic semantics and the scan is the capped last resort, and wraps Mode-2 resolution in a wall-clock budget with an observability log.

**Files:**
- Modify: `internal/app/cmd/create_slack.go` (resolveExistingChannelID ~152-182; resolveSlackChannel Mode-2 ~263-370; add budget/retry vars + SlackChannelStore interface)
- Test: `internal/app/cmd/create_slack_test.go`

- [ ] **Step 1: Add the tunables + interfaces (no behavior yet)**

At the top of `create_slack.go` (after imports), add:

```go
// Slack channel-resolution bounding knobs (P0). Package-level so tests shrink them.
var (
	// SlackResolveBudget caps total wall-clock for per-sandbox Slack channel
	// resolution. Far below the 900s create-handler ceiling, far above a normal
	// create+info round-trip. Override: KM_SLACK_RESOLVE_BUDGET (seconds).
	SlackResolveBudget = 45 * time.Second
	// SlackMaxScanPages caps the conversations.list fallback. 0 = scan disabled
	// (fail fast with adopt/channelOverride guidance) — the safe default for huge
	// workspaces. Override: KM_SLACK_MAX_SCAN_PAGES.
	SlackMaxScanPages = 0
	// slackInfoRetries is the bounded retry count for a transient conversations.info
	// probe before optimistically trusting the stored ID.
	slackInfoRetries = 2
	// slackInfoRetryDelay is the backoff between transient info retries.
	slackInfoRetryDelay = 500 * time.Millisecond
)

func init() {
	if v := os.Getenv("KM_SLACK_RESOLVE_BUDGET"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			SlackResolveBudget = time.Duration(secs) * time.Second
		}
	}
	if v := os.Getenv("KM_SLACK_MAX_SCAN_PAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			SlackMaxScanPages = n
		}
	}
}

// SlackChannelStore is the durable alias→channelID mapping (P2). The DDB-backed
// implementation lives in pkg/aws; resolveSlackChannel reads it first and
// write-throughs on create/resolve. A nil store disables the DDB layer (SSM
// by-name cache still applies) so tests and prefix-less paths degrade cleanly.
type SlackChannelStore interface {
	GetByAlias(ctx context.Context, alias string) (channelID string, err error)
	UpsertByAlias(ctx context.Context, alias, channelID string) error
}
```

Add `"strconv"` to the import block (and confirm `"os"`, `"time"` are present — they are).

- [ ] **Step 2: Write the failing tests for the resolver**

Add to `create_slack_test.go` (mirror the existing fake-`SlackAPI` pattern already in that file). The fakes must implement the new `FindChannelByName(ctx, name, maxPages)` signature.

```go
// Reuse, stored ID live → O(1): no create, no scan.
func TestResolvePerSandbox_StoredID_Live_NoScan(t *testing.T) {
	api := &fakeSlackAPI{
		channelInfoErr: nil, // info(cachedID) succeeds
		findShouldPanic: true, // FindChannelByName must NOT be called
	}
	store := &fakeChannelStore{m: map[string]string{"github-bot": "C0LIVE"}}
	id, per, err := resolvePerSandboxChannelForTest(t, api, store, "github-bot", "sb-github-bot")
	if err != nil || id != "C0LIVE" || !per {
		t.Fatalf("want C0LIVE/true/nil, got %q/%v/%v", id, per, err)
	}
	if api.createCalls != 0 {
		t.Fatalf("stored-live path must not call conversations.create")
	}
}

// Reuse, stored ID, transient info error → bounded-retry then optimistic-use, NO scan.
func TestResolvePerSandbox_StoredID_TransientInfo_NoScan(t *testing.T) {
	api := &fakeSlackAPI{
		channelInfoErr:  &slack.SlackAPIError{Method: "conversations.info", Code: "ratelimited"},
		findShouldPanic: true, // must never enumerate
	}
	store := &fakeChannelStore{m: map[string]string{"github-bot": "C0OPT"}}
	id, _, err := resolvePerSandboxChannelForTest(t, api, store, "github-bot", "sb-github-bot")
	if err != nil || id != "C0OPT" {
		t.Fatalf("transient info must optimistically use stored ID, got %q/%v", id, err)
	}
}

// Reuse, stored ID, definitive channel_not_found → invalidate + recreate cleanly.
func TestResolvePerSandbox_StoredID_NotFound_Recreates(t *testing.T) {
	api := &fakeSlackAPI{
		channelInfoErr: &slack.SlackAPIError{Method: "conversations.info", Code: "channel_not_found"},
		createID:       "C0NEW",
	}
	store := &fakeChannelStore{m: map[string]string{"github-bot": "C0DEAD"}}
	id, _, err := resolvePerSandboxChannelForTest(t, api, store, "github-bot", "sb-github-bot")
	if err != nil || id != "C0NEW" {
		t.Fatalf("dead stored ID must recreate, got %q/%v", id, err)
	}
	if store.m["github-bot"] != "C0NEW" {
		t.Fatalf("mapping must be rewritten to C0NEW, got %q", store.m["github-bot"])
	}
}

// No stored mapping, name_taken, scan disabled (default) → fail fast with guidance, NO scan.
func TestResolvePerSandbox_NameTaken_NoMapping_FailFast(t *testing.T) {
	old := SlackMaxScanPages
	SlackMaxScanPages = 0
	defer func() { SlackMaxScanPages = old }()
	api := &fakeSlackAPI{
		createErr:       &slack.SlackAPIError{Method: "conversations.create", Code: "name_taken"},
		findShouldPanic: true, // scan disabled ⇒ never called
	}
	store := &fakeChannelStore{m: map[string]string{}}
	_, _, err := resolvePerSandboxChannelForTest(t, api, store, "github-bot", "sb-github-bot")
	if err == nil || !strings.Contains(err.Error(), "km slack adopt") {
		t.Fatalf("want fail-fast adopt guidance, got %v", err)
	}
}

// Fresh alias, name free → create + write-through to store.
func TestResolvePerSandbox_FreshCreate_WritesStore(t *testing.T) {
	api := &fakeSlackAPI{createID: "C0FRESH"}
	store := &fakeChannelStore{m: map[string]string{}}
	id, _, err := resolvePerSandboxChannelForTest(t, api, store, "github-bot", "sb-github-bot")
	if err != nil || id != "C0FRESH" {
		t.Fatalf("want C0FRESH, got %q/%v", id, err)
	}
	if store.m["github-bot"] != "C0FRESH" {
		t.Fatalf("fresh create must write store, got %q", store.m["github-bot"])
	}
}

// Pre-104 channel: DDB store EMPTY, ID present in the SSM by-name cache, channel live →
// resolves O(1) AND migrates the mapping into the authoritative DDB store on first touch.
func TestResolvePerSandbox_StoredID_SSMOnly_BackfillsDDB(t *testing.T) {
	api := &fakeSlackAPI{
		channelInfoErr:  nil,  // info(SSM-sourced ID) succeeds
		findShouldPanic: true, // must NOT enumerate
	}
	store := &fakeChannelStore{m: map[string]string{}} // DDB empty
	// Seed the fake SSM by-name cache (NOT the store) with the existing channel ID.
	// resolvePerSandboxChannelForTest must let the test pre-seed the fake SSM store at
	// key slackChannelNameCacheKey(prefix, "sb-github-bot") = "C0SSM".
	id, _, err := resolvePerSandboxChannelForTestWithSSM(t, api, store,
		"github-bot", "sb-github-bot", map[string]string{"sb-github-bot": "C0SSM"})
	if err != nil || id != "C0SSM" {
		t.Fatalf("SSM-sourced hit must resolve O(1), got %q/%v", id, err)
	}
	if store.m["github-bot"] != "C0SSM" {
		t.Fatalf("SSM-sourced hit must back-fill the DDB store, got %q", store.m["github-bot"])
	}
	if api.createCalls != 0 {
		t.Fatalf("back-fill path must not create a channel")
	}
}
```

You will need a small `fakeChannelStore` (map-backed `SlackChannelStore`) and to extend the existing `fakeSlackAPI` with `findShouldPanic`/`createCalls`/`channelInfoErr` fields and the 3-arg `FindChannelByName`. Also write the `resolvePerSandboxChannelForTest` helper that builds a minimal profile with `notification.slack.enabled+perSandbox=true` and calls the real `resolveSlackChannel` with a fake SSM store. Mirror the construction already used by `TestResolveSlack_PerSandbox_NameTaken_RateLimited_ErrorMessage`.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/app/cmd/ -run TestResolvePerSandbox -v`
Expected: FAIL — helper + behavior not implemented.

- [ ] **Step 4: Implement the state machine**

Replace `resolveExistingChannelID` (lines ~152-182) and refactor the Mode-2 block of `resolveSlackChannel` (lines ~263-370) so resolution is lookup-first and bounded. New helper:

```go
// lookupStoredChannelID returns a previously-stored channel ID for the alias,
// DDB-first (authoritative) then SSM by-name (back-compat). Empty alias ⇒ skip
// DDB (no stable reuse key). Returns ("", false) on miss. fromDDB is true only
// when the hit came from the authoritative DDB store — callers back-fill DDB on
// an SSM-sourced hit (fromDDB=false) so pre-104 channels migrate on first touch.
func lookupStoredChannelID(ctx context.Context, store SlackChannelStore, ssmStore SSMParamStore,
	slackPrefix, alias, channelName string) (id string, fromDDB bool) {
	if store != nil && alias != "" {
		if v, err := store.GetByAlias(ctx, alias); err == nil && v != "" {
			return v, true
		}
	}
	if v, _ := ssmStore.Get(ctx, slackChannelNameCacheKey(slackPrefix, channelName), false); v != "" {
		return v, false
	}
	return "", false
}

// storeChannelMapping write-throughs the name→ID binding to BOTH the durable DDB
// store (by alias) and the SSM by-name cache. Best-effort: never fails the create.
func storeChannelMapping(ctx context.Context, store SlackChannelStore, ssmStore SSMParamStore,
	slackPrefix, alias, channelName, channelID string) {
	if store != nil && alias != "" && channelID != "" {
		if err := store.UpsertByAlias(ctx, alias, channelID); err != nil {
			log.Debug().Err(err).Str("alias", alias).Msg("DDB channel mapping upsert failed (non-fatal)")
		}
	}
	cacheSlackChannelIDByName(ctx, ssmStore, slackPrefix, channelName, channelID)
}

// validateStoredChannel probes conversations.info with bounded retry. Returns:
//   ok=true            → channel live, use it
//   gone=true          → definitive channel_not_found, invalidate + recreate
//   ok=false,gone=false → transient after retries → caller optimistically uses the ID
func validateStoredChannel(ctx context.Context, api SlackAPI, channelID string) (ok, gone bool) {
	var lastErr error
	for attempt := 0; attempt <= slackInfoRetries; attempt++ {
		if _, _, err := api.ChannelInfo(ctx, channelID); err == nil {
			return true, false
		} else {
			lastErr = err
			if slack.IsChannelNotFound(err) {
				return false, true
			}
		}
		if attempt < slackInfoRetries {
			if sleepErr := slackResolveSleep(ctx, slackInfoRetryDelay); sleepErr != nil {
				break
			}
		}
	}
	log.Debug().Err(lastErr).Str("channel", channelID).
		Msg("conversations.info transient after retries — optimistically trusting stored ID")
	return false, false
}
```

Add a `slackResolveSleep` var mirroring `slackSleep` in pkg/slack (ctx-aware), or reuse `time.After` with a ctx select.

Then restructure the Mode-2 path so it reads (pseudocode → real Go):

```
ctx, cancel := context.WithTimeout(ctx, SlackResolveBudget); defer cancel()
start := time.Now()
path := "failfast"
defer log path=… ms=… id=…

channelName := deriveSandboxChannelName(...)

// 1. lookup-first
if id, fromDDB := lookupStoredChannelID(...); id != "" {
    ok, gone := validateStoredChannel(ctx, api, id)
    switch {
    case ok:        path="cache_hit"; backfillDDBIfSSM(fromDDB, id); ensureBotMember(id); finishInvites(); return id
    case !gone:     path="cache_optimistic"; backfillDDBIfSSM(fromDDB, id); ensureBotMember(id); finishInvites(); return id   // transient
    default:        // gone → fall through to create (invalidate happens via overwrite on next store)
    }
}
// backfillDDBIfSSM: when !fromDDB && store != nil && alias != "" → store.UpsertByAlias(ctx, alias, id)
// best-effort (log-debug on error, never fail create). Promotes an SSM-sourced hit into the
// authoritative DDB table so a pre-104 / out-of-band channel migrates on first touch. No-op when
// fromDDB (DDB was already authoritative) or store==nil (104-01 production until 104-03 wires it).

// 2. create
id, createErr := api.CreateChannel(ctx, channelName)
if createErr == nil { path="created"; storeChannelMapping(...); ensureBotMember(id); finishInvites(); return id }

// 3. name_taken with no usable mapping
if isNameTaken(createErr) {
    if SlackMaxScanPages > 0 {
        if id, scanErr := api.FindChannelByName(ctx, channelName, SlackMaxScanPages); scanErr == nil && id != "" {
            path="scan_capped"; storeChannelMapping(...); ensureBotMember(id); finishInvites(); return id
        } else if errors.Is(scanErr, slack.ErrScanCapExceeded) || scanErr == nil {
            return failFast(channelName)   // capped or empty
        } else {
            return wrap(scanErr)           // ratelimited/other
        }
    }
    return failFast(channelName)           // scan disabled (default)
}
return wrap(createErr)
```

`failFast` message (reuse + extend the existing wording):

```go
func slackResolveFailFast(channelName string) error {
	return fmt.Errorf("Slack channel #%s exists but km has no stored ID for it. "+
		"Seed the mapping with `km slack adopt <alias> <channelID>` (find the ID in Slack → channel → About → Channel ID), "+
		"or set notification.slack.channelOverride=<id>. "+
		"(Workspace enumeration is disabled by default; set KM_SLACK_MAX_SCAN_PAGES>0 to allow a bounded scan.)", channelName)
}
```

Keep the existing bot-join + invite orchestration (lines ~296-369) intact — factor it into a small `ensureBotMemberAndInvite(...)` closure/helper so all three success paths share it without duplication.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/app/cmd/ -run 'TestResolvePerSandbox|TestResolveSlack' -v`
Expected: PASS (new tests + the existing `TestResolveSlack_*` updated for the new fake signature).

- [ ] **Step 6: Run the full package + slack package**

Run: `go test ./internal/app/cmd/ ./pkg/slack/ 2>&1 | tail -20`
Expected: PASS. Fix any other call sites of `FindChannelByName`/`resolveExistingChannelID` revealed by the compiler.

- [ ] **Step 7: Commit**

```bash
git add internal/app/cmd/create_slack.go internal/app/cmd/create_slack_test.go pkg/slack/client.go
git commit -m "feat(slack): bounded lookup-first channel resolution; never unbounded scan (P0+P1)"
```

---

## Task 4: `dynamodb-slack-channels` Terraform module

**Files:**
- Create: `infra/modules/dynamodb-slack-channels/v1.0.0/main.tf`
- Create: `infra/modules/dynamodb-slack-channels/v1.0.0/variables.tf`
- Create: `infra/modules/dynamodb-slack-channels/v1.0.0/outputs.tf`

- [ ] **Step 1: Create main.tf** (mirror `dynamodb-slack-threads`, single hash key `alias`)

```hcl
# km-slack-channels: durable alias → channel_id mapping for O(1) per-sandbox
# Slack channel resolution on alias reuse. Written by km create / km slack adopt;
# read first (before conversations.create) during create-time resolution.
# Survives sandbox destroy (the alias is the stable reuse key; sandbox_id is not).
resource "aws_dynamodb_table" "slack_channels" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "alias"

  attribute {
    name = "alias"
    type = "S"
  }

  point_in_time_recovery {
    enabled = false
  }

  server_side_encryption {
    enabled = true
  }

  tags = merge(var.tags, {
    Name      = var.table_name
    Component = "km-slack-channels"
  })
}
```

Note: **no TTL** — the mapping must persist across recreate cycles indefinitely (unlike slack-threads which TTLs conversations). A stale mapping self-heals via the `channel_not_found` recreate path (Task 3).

- [ ] **Step 2: Create variables.tf**

```hcl
variable "table_name" {
  description = "Name of the Slack channels DynamoDB table (e.g. km-slack-channels)."
  type        = string
  default     = "km-slack-channels"
}

variable "tags" {
  description = "Resource tags to merge onto the DynamoDB table."
  type        = map(string)
  default     = {}
}
```

- [ ] **Step 3: Create outputs.tf**

```hcl
output "table_name" {
  description = "Name of the Slack channels DynamoDB table."
  value       = aws_dynamodb_table.slack_channels.name
}

output "table_arn" {
  description = "ARN of the Slack channels DynamoDB table."
  value       = aws_dynamodb_table.slack_channels.arn
}
```

- [ ] **Step 4: Validate**

Run: `cd infra/modules/dynamodb-slack-channels/v1.0.0 && terraform init -backend=false && terraform validate`
Expected: `Success! The configuration is valid.`
(Do NOT add a `required_providers` block — root.hcl owns the provider generate stanza; see memory `project_terragrunt_providers_in_root`.)

- [ ] **Step 5: Commit**

```bash
git add infra/modules/dynamodb-slack-channels
git commit -m "feat(infra): dynamodb-slack-channels table module (PK alias, no TTL)"
```

---

## Task 5: Live terragrunt unit

**Files:**
- Create: `infra/live/use1/dynamodb-slack-channels/terragrunt.hcl`

- [ ] **Step 1: Create the unit** (mirror `infra/live/use1/dynamodb-slack-threads/terragrunt.hcl` exactly, changing only the three marked lines)

```hcl
locals {
  repo_root = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")

  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  region_label  = local.region_config.locals.region_label
}

include "root" {
  path = find_in_parent_folders("root.hcl")
}

remote_state {
  backend = "s3"
  generate = {
    path      = "backend.tf"
    if_exists = "overwrite_terragrunt"
  }
  config = {
    bucket         = local.site_vars.locals.backend.bucket
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/dynamodb-slack-channels/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/dynamodb-slack-channels/v1.0.0"
}

inputs = {
  table_name = "${local.site_vars.locals.site.label}-slack-channels"
  tags = {
    "km:component" = "km-slack-channels"
    "km:managed"   = "true"
  }
}
```

**Verify the exact `locals`/`inputs` shape against the real `dynamodb-slack-threads/terragrunt.hcl`** (read the source file, not a `.terragrunt-cache` copy) and match it — site.hcl key names must be identical.

- [ ] **Step 2: Commit**

```bash
git add infra/live/use1/dynamodb-slack-channels/terragrunt.hcl
git commit -m "feat(infra): live terragrunt unit for dynamodb-slack-channels"
```

---

## Task 6: Register module in `km init`

**Files:**
- Modify: `internal/app/cmd/init.go` (regionalModules list, right after the `dynamodb-slack-threads` entry ~277-282)

- [ ] **Step 1: Add the entry**

```go
{
	// O(1) Slack channel resolution: durable alias → channel_id mapping read
	// first during km create, written on create/resolve and by km slack adopt.
	// No dependents among Lambdas (create-handler reads it via SDK at runtime),
	// so ordering only needs to precede nothing in particular — placed beside
	// the other slack DDB tables.
	name:    "dynamodb-slack-channels",
	dir:     filepath.Join(regionDir, "dynamodb-slack-channels"),
	envReqs: nil,
},
```

- [ ] **Step 2: Verify it compiles + the list is well-formed**

Run: `make build` (NOT `go build` — ldflags; see memory `feedback_rebuild_km`). Then `./km init --plan 2>&1 | grep -i slack-channels` should show the module is enumerated. (A stale `km` binary silently skips new modules — memory `project_make_build_precedes_km_init`.)
Expected: build succeeds; `dynamodb-slack-channels` appears in the plan module list.

- [ ] **Step 3: Commit**

```bash
git add internal/app/cmd/init.go
git commit -m "feat(init): register dynamodb-slack-channels in regional modules"
```

---

## Task 7: Create-handler IAM for the new table

The create-handler Lambda runs `km create` (remote path) and must GetItem/PutItem on `km-slack-channels`. Plumb a table-name variable from the live unit → create-handler module → km-operator-policy, gated so empty disables the grant.

**Files:**
- Modify: `infra/modules/km-operator-policy/v1.0.0/variables.tf` (add `slack_channels_table_name`)
- Modify: `infra/modules/km-operator-policy/v1.0.0/main.tf` (add IAM policy resource)
- Modify: `infra/modules/create-handler/v1.0.0/variables.tf` (add var)
- Modify: `infra/modules/create-handler/v1.0.0/main.tf` (pass var into km_operator_policy module)
- Modify: `infra/live/use1/create-handler/terragrunt.hcl` (set the input + add dependency)

- [ ] **Step 1: km-operator-policy variable**

In `infra/modules/km-operator-policy/v1.0.0/variables.tf`:

```hcl
variable "slack_channels_table_name" {
  description = "Name of the km-slack-channels DynamoDB table. Empty disables the IAM grant."
  type        = string
  default     = ""
}
```

- [ ] **Step 2: km-operator-policy IAM resource**

In `infra/modules/km-operator-policy/v1.0.0/main.tf` (mirror the `dynamodb_slack_threads` resource):

```hcl
# O(1) Slack channel resolution: create-handler reads/writes the alias→channel_id map.
resource "aws_iam_role_policy" "dynamodb_slack_channels" {
  count = var.slack_channels_table_name != "" ? 1 : 0

  name = "${var.resource_prefix}-create-handler-dynamodb-slack-channels"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SlackChannelsTableAccess"
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:DescribeTable",
        ]
        Resource = [
          "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.slack_channels_table_name}",
        ]
      }
    ]
  })
}
```

- [ ] **Step 3: create-handler variable + wiring**

`infra/modules/create-handler/v1.0.0/variables.tf`:

```hcl
variable "slack_channels_table_name" {
  description = "Name of the km-slack-channels DynamoDB table (alias→channel_id map)."
  type        = string
  default     = ""
}
```

`infra/modules/create-handler/v1.0.0/main.tf` — in the `module "km_operator_policy"` block, add:

```hcl
  slack_channels_table_name = var.slack_channels_table_name
```

- [ ] **Step 4: live unit input + dependency**

In `infra/live/use1/create-handler/terragrunt.hcl`, add a dependency (mirror the slack_threads dependency if present) and the input:

```hcl
dependency "slack_channels" {
  config_path  = "../dynamodb-slack-channels"
  mock_outputs = {
    table_name = "km-slack-channels"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-slack-channels"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "show", "init", "destroy"]
}
```

…and in `inputs`:

```hcl
slack_channels_table_name = dependency.slack_channels.outputs.table_name
```

Include `"show"` in `mock_outputs_allowed_terraform_commands` (memory `project_terragrunt_show_needs_mocks` — the destroy-class gate runs `terragrunt show`).

- [ ] **Step 5: Validate the create-handler unit plans**

Run: `make build && ./km init --plan 2>&1 | grep -iE 'slack-channels|create-handler' | head`
Expected: both units plan cleanly; the new IAM resource shows as an add on create-handler.

- [ ] **Step 6: Commit**

```bash
git add infra/modules/km-operator-policy infra/modules/create-handler infra/live/use1/create-handler/terragrunt.hcl
git commit -m "feat(infra): create-handler IAM + plumbing for km-slack-channels"
```

---

## Task 8: Config field + getter

**Files:**
- Modify: `internal/app/config/config.go` (struct field, viper default, getter — mirror `SlackThreadsTableName`)
- Modify: `internal/app/config/config.go` v2→v merge-list (memory `project_config_key_merge_list` — a new key silently ignored otherwise)
- Test: `internal/app/config/config_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestGetSlackChannelsTableName(t *testing.T) {
	c := &Config{ResourcePrefix: "sec"}
	if got := c.GetSlackChannelsTableName(); got != "sec-slack-channels" {
		t.Fatalf("default derivation: got %q", got)
	}
	c.SlackChannelsTableName = "custom-tbl"
	if got := c.GetSlackChannelsTableName(); got != "custom-tbl" {
		t.Fatalf("explicit override: got %q", got)
	}
	var nilC *Config
	if got := nilC.GetSlackChannelsTableName(); got != "km-slack-channels" {
		t.Fatalf("nil receiver: got %q", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/app/config/ -run TestGetSlackChannelsTableName -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement** (mirror `SlackThreadsTableName` everywhere it appears)

Add struct field `SlackChannelsTableName string` (with the same `mapstructure`/yaml tag style as `SlackThreadsTableName`), `v.SetDefault("slack_channels_table_name", "")`, the merge-list entry, and:

```go
func (c *Config) GetSlackChannelsTableName() string {
	if c == nil {
		return "km-slack-channels"
	}
	if c.SlackChannelsTableName != "" {
		return c.SlackChannelsTableName
	}
	return c.GetResourcePrefix() + "-slack-channels"
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/app/config/ -run TestGetSlackChannelsTableName -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/config/config.go internal/app/config/config_test.go
git commit -m "feat(config): GetSlackChannelsTableName getter + merge-list entry"
```

---

## Task 9: DDB store Go helper (`pkg/aws/slack_channels.go`)

**Files:**
- Create: `pkg/aws/slack_channels.go`
- Test: `pkg/aws/slack_channels_test.go`

- [ ] **Step 1: Write the failing test** (use a fake DDB client mirroring existing `pkg/aws` test fakes)

```go
func TestSlackChannelStore_UpsertThenGet(t *testing.T) {
	fake := &fakeDDB{items: map[string]map[string]ddbtypes.AttributeValue{}}
	s := &SlackChannelStore{Client: fake, TableName: "km-slack-channels"}
	if err := s.UpsertByAlias(context.Background(), "github-bot", "C0X"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetByAlias(context.Background(), "github-bot")
	if err != nil || got != "C0X" {
		t.Fatalf("got %q/%v want C0X/nil", got, err)
	}
}

func TestSlackChannelStore_GetMiss(t *testing.T) {
	fake := &fakeDDB{items: map[string]map[string]ddbtypes.AttributeValue{}}
	s := &SlackChannelStore{Client: fake, TableName: "km-slack-channels"}
	got, err := s.GetByAlias(context.Background(), "absent")
	if err != nil || got != "" {
		t.Fatalf("miss must return \"\"/nil, got %q/%v", got, err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/aws/ -run TestSlackChannelStore -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement** (mirror `DDBThreadStore` in `pkg/slack/bridge/aws_adapters.go`; raw attribute maps, no TTL)

```go
package aws

import (
	"context"
	"fmt"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// SlackChannelGetPutAPI is the minimal DynamoDB surface SlackChannelStore needs.
type SlackChannelGetPutAPI interface {
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
}

// SlackChannelStore is the durable alias→channel_id mapping (km-slack-channels).
// PK=alias; no TTL (the mapping must persist across destroy/recreate). A stale
// mapping self-heals via the create-time channel_not_found recreate path.
type SlackChannelStore struct {
	Client    SlackChannelGetPutAPI
	TableName string
}

// GetByAlias returns the stored channel_id for alias, or "" on miss.
func (s *SlackChannelStore) GetByAlias(ctx context.Context, alias string) (string, error) {
	out, err := s.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(s.TableName),
		Key: map[string]ddbtypes.AttributeValue{
			"alias": &ddbtypes.AttributeValueMemberS{Value: alias},
		},
	})
	if err != nil {
		return "", fmt.Errorf("slack-channels GetItem %q: %w", alias, err)
	}
	if v, ok := out.Item["channel_id"]; ok {
		if sv, ok := v.(*ddbtypes.AttributeValueMemberS); ok {
			return sv.Value, nil
		}
	}
	return "", nil
}

// UpsertByAlias writes (overwriting) the alias→channel_id mapping plus an
// updated_at audit attribute.
func (s *SlackChannelStore) UpsertByAlias(ctx context.Context, alias, channelID string) error {
	_, err := s.Client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: awssdk.String(s.TableName),
		Item: map[string]ddbtypes.AttributeValue{
			"alias":      &ddbtypes.AttributeValueMemberS{Value: alias},
			"channel_id": &ddbtypes.AttributeValueMemberS{Value: channelID},
			"updated_at": &ddbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
		},
	})
	if err != nil {
		return fmt.Errorf("slack-channels PutItem %q: %w", alias, err)
	}
	return nil
}
```

`SlackChannelStore` satisfies the `cmd.SlackChannelStore` interface from Task 3 (same method set). Note `time.Now()` is fine in production code (the no-`Date.now()` rule is for Workflow scripts only).

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./pkg/aws/ -run TestSlackChannelStore -v`
Expected: PASS. (Add a `fakeDDB` to the test file if one doesn't already exist in `pkg/aws`.)

- [ ] **Step 5: Commit**

```bash
git add pkg/aws/slack_channels.go pkg/aws/slack_channels_test.go
git commit -m "feat(aws): SlackChannelStore DDB helper (alias→channel_id)"
```

---

## Task 10: Wire the DDB store into `km create`

**Files:**
- Modify: `internal/app/cmd/create.go` (~597 — build the store, pass to resolveSlackChannel)
- Modify: `internal/app/cmd/create_slack.go` (resolveSlackChannel signature: add `store SlackChannelStore`)

- [ ] **Step 1: Extend `resolveSlackChannel` signature**

Add a `store SlackChannelStore` parameter and thread it into `lookupStoredChannelID` / `storeChannelMapping` (Task 3). Update all test call sites.

- [ ] **Step 2: Build the store in create.go**

At `create.go:~597`, alongside the existing `slackClient := slack.NewClient(botToken, nil)` and SSM store construction, build:

```go
ddbClient := dynamodb.NewFromConfig(awsCfg)
channelStore := &awspkg.SlackChannelStore{
	Client:    ddbClient,
	TableName: cfg.GetSlackChannelsTableName(),
}
```

and pass `channelStore` into `resolveSlackChannel(...)`. (Confirm `awsCfg`/`awspkg` aliases match what's already imported in create.go.)

- [ ] **Step 3: Build + full test**

Run: `make build && go test ./internal/app/... ./pkg/... 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/app/cmd/create.go internal/app/cmd/create_slack.go internal/app/cmd/create_slack_test.go
git commit -m "feat(slack): wire SlackChannelStore into km create resolution (P2)"
```

---

## Task 11: `km slack adopt <alias> <channelID>`

**Files:**
- Create: `internal/app/cmd/slack_adopt.go`
- Modify: `internal/app/cmd/slack.go` (register the subcommand on the `slack` parent)
- Test: `internal/app/cmd/slack_adopt_test.go`

- [ ] **Step 1: Write the failing test** (validation logic, store-agnostic via interfaces)

```go
func TestSlackAdopt_RejectsBadChannelID(t *testing.T) {
	err := runSlackAdopt(context.Background(), nil, nil, "github-bot", "not-an-id", "/sec/slack/")
	if err == nil || !strings.Contains(err.Error(), "^C[A-Z0-9]+$") {
		t.Fatalf("want format error, got %v", err)
	}
}

func TestSlackAdopt_RequiresBotMembership(t *testing.T) {
	api := &fakeSlackAPI{channelInfoIsMember: false}
	err := runSlackAdopt(context.Background(), api, &fakeChannelStore{m: map[string]string{}}, "github-bot", "C0X", "/sec/slack/")
	if err == nil || !strings.Contains(err.Error(), "not a member") {
		t.Fatalf("want membership error, got %v", err)
	}
}

func TestSlackAdopt_WritesThrough(t *testing.T) {
	api := &fakeSlackAPI{channelInfoIsMember: true}
	store := &fakeChannelStore{m: map[string]string{}}
	if err := runSlackAdopt(context.Background(), api, store, "github-bot", "C0X", "/sec/slack/"); err != nil {
		t.Fatal(err)
	}
	if store.m["github-bot"] != "C0X" {
		t.Fatalf("adopt must write DDB store, got %q", store.m["github-bot"])
	}
}
```

`fakeSlackAPI.ChannelInfo` must return `(members, channelInfoIsMember, channelInfoErr)`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/app/cmd/ -run TestSlackAdopt -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement the core + cobra command**

```go
// runSlackAdopt validates a channel ID + bot membership, then write-throughs the
// alias→channelID mapping to BOTH the DDB store and the SSM by-name cache so a
// future km create on this alias resolves O(1).
func runSlackAdopt(ctx context.Context, api SlackAPI, store SlackChannelStore, alias, channelID, slackPrefix string) error {
	if !channelIDRe.MatchString(channelID) {
		return fmt.Errorf("channel ID %q does not match ^C[A-Z0-9]+$ (find it: Slack → channel → About → Channel ID)", channelID)
	}
	if alias == "" {
		return fmt.Errorf("alias is required (the stable --alias used at km create)")
	}
	_, isMember, err := api.ChannelInfo(ctx, channelID)
	if err != nil {
		return fmt.Errorf("validate channel %s: %w", channelID, err)
	}
	if !isMember {
		return fmt.Errorf("bot is not a member of %s — /invite the bot first, then re-run km slack adopt", channelID)
	}
	if store != nil {
		if err := store.UpsertByAlias(ctx, alias, channelID); err != nil {
			return fmt.Errorf("write DDB mapping: %w", err)
		}
	}
	// SSM by-name write-through requires the channel NAME; derive it from alias the
	// same way km create does so the by-name cache key matches.
	// (Use the default derivation: sb-{alias}; if the profile uses a custom
	// channelName template, the DDB-by-alias mapping above is the authoritative hit.)
	return nil
}
```

Wire a `cobra.Command{Use: "adopt <alias> <channelID>", Args: cobra.ExactArgs(2)}` that builds the real `slack.Client` (token from SSM, mirror `slack.go:230`) and `awspkg.SlackChannelStore` (table from `cfg.GetSlackChannelsTableName()`), then calls `runSlackAdopt`. Register it under the `slack` parent command in `slack.go`. Print a success line: `✓ adopted #<name> (<channelID>) for alias <alias>`.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/app/cmd/ -run TestSlackAdopt -v`
Expected: PASS.

- [ ] **Step 5: Build + smoke the command wiring**

Run: `make build && ./km slack adopt --help`
Expected: help text renders; `adopt` listed under `km slack`.

- [ ] **Step 6: Commit**

```bash
git add internal/app/cmd/slack_adopt.go internal/app/cmd/slack.go internal/app/cmd/slack_adopt_test.go
git commit -m "feat(slack): km slack adopt <alias> <channelID> escape hatch (C)"
```

---

## Task 12: `km doctor` existence check for the new table (light)

**Files:**
- Modify: `internal/app/cmd/doctor*.go` (the DDB-tables health check; add an existence/DescribeTable check — NOT an orphan-row scan, since alias rows are not per-sandbox and must never be auto-deleted)

- [ ] **Step 1: Locate the table-health check** and add a `DescribeTable` existence probe for `cfg.GetSlackChannelsTableName()`, WARN if missing, mirroring how other DDB tables are reported. Do **not** add it to the orphan-row scan path in `doctor_ddb_rows.go`.

- [ ] **Step 2: Build + run doctor against a configured install**

Run: `make build && AWS_PROFILE=klanker-application ./km doctor 2>&1 | grep -i slack-channels`
Expected: a line reporting the table present (or WARN if not yet applied).

- [ ] **Step 3: Commit**

```bash
git add internal/app/cmd/
git commit -m "feat(doctor): km-slack-channels table existence check"
```

---

## Task 13: Docs + CLAUDE.md + deploy sequence

**Files:**
- Modify: `docs/slack-notifications.md` (new section)
- Modify: `CLAUDE.md` (phase note + Where-to-look row)

- [ ] **Step 1: docs/slack-notifications.md** — add a section "O(1) channel resolution on alias reuse" covering: the bounded resolver, `KM_SLACK_RESOLVE_BUDGET` / `KM_SLACK_MAX_SCAN_PAGES` (default 0 = scan off), the `km-slack-channels` table, `notification.slack.channelOverride` as the manual escape, and `km slack adopt <alias> <channelID>` for orphaned channels with the "find the Channel ID in Slack → About" hint.

- [ ] **Step 2: CLAUDE.md** — add a phase note summarizing: new `km-slack-channels` table; bounded/fail-fast resolution; deploy = `make build` (binary picks up new module) + `make build-lambdas` + `km init --dry-run=false` (new table + IAM + env block; NOT `--sidecars`); existing sandboxes unaffected (create-time fix). Add a Where-to-look row pointing at the new docs section.

- [ ] **Step 3: Commit**

```bash
git add docs/slack-notifications.md CLAUDE.md
git commit -m "docs(slack): O(1) channel resolution + km slack adopt runbook"
```

---

## Final verification (before declaring done)

- [ ] `make build` succeeds (binary has ldflags + new module registered).
- [ ] `go test ./... 2>&1 | tail -30` — all green.
- [ ] `scripts/validate-all-profiles.sh` — profile inventory still validates (no schema change expected, but confirm).
- [ ] `./km init --plan` (against a real install, `AWS_PROFILE=klanker-application`) shows: new `dynamodb-slack-channels` table as an ADD, create-handler IAM policy as an ADD, no destroy-class trips. Use `--plan` per the destroy-class gate before any apply.
- [ ] Deploy-surface self-audit (memory `feedback_verify_deploy_surface_not_just_code`): module ✓, live unit ✓, init.go list ✓, IAM grant + ARN wiring ✓, config getter + merge-list ✓, runtime table-name derivation ✓, no stale-binary trap (`make build` before `km init`) ✓.
- [ ] Manual UAT (operator): on the real large workspace, `km create` a reused-alias profile (`profiles/github-review.yaml`, `archiveOnDestroy:false`) — confirm it completes in ~2 min, the `slack_resolve path=…` log shows `cache_hit`/`created`/`failfast` (never an unbounded scan), and a cold orphan resolves via `km slack adopt`. This is the spec's Phase 0 confirmation, now shipped inline.

---

## Notes carried from the spec's open questions (resolved)

- Budget 45s; transient-info = bounded-retry(2×)+optimistic; scan off by default; dedicated DDB table; adopt validates format+membership+write-through; archiveOnDestroy = separate ticket.
- The spec floated storing on `km-sandboxes` — rejected: destroy deletes that row and `ListAllSandboxesByDynamo` Scans it (synthetic items would pollute `km list`). Dedicated table avoids both.
