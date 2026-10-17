# Slack bot-initiated DM Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A sandbox agent can run `km-slack dm --to U… --text "…"` and the person receives a top-level DM from the bot; a reply under it continues with the same sandbox.

**Architecture:** A new signed-envelope action `dm` on the existing km-slack → bridge path. The bridge gates the target through the default route's email allowlist (the same `users.info` → email gate the inbound side uses), opens the DM with `conversations.open`, posts, and writes a thread row so the reply has continuity. One new scope (`im:write`), no infrastructure.

**Tech Stack:** Go 1.25, Slack Web API (`conversations.open`, `chat.postMessage`, `chat.getPermalink`), Ed25519-signed envelopes (`pkg/slack/payload.go`), bash poller rendered by `pkg/compiler/userdata.go`.

**Spec:** `docs/superpowers/specs/2026-10-17-slack-bot-initiated-dm-design.md`

## Global Constraints

- The new envelope field is `TargetUser string \`json:"target_user,omitempty"\``. **`omitempty` is mandatory**: the bridge re-canonicalises the struct before verifying, so a field that always serialises would break the signature of every envelope from a `km-slack` built before the field existed (spec §3 "Envelope field").
- Every refusal in the bridge is fail-closed and logs the step: `dm_disabled`, `bad_target`, `dm_not_allowed`, `channel_mismatch`, `quota_exceeded`, `dm_unsupported`. Operators (`sender_id == "operator"`) cannot send `dm`.
- The thread row for the DM is written only **after** a successful `chat.postMessage`, never before (spec §3 "Continuity").
- User ids only, validated with `^[UW][A-Z0-9]+$`; names are rejected at parse time in `km-slack` and with `400 bad_target` in the bridge.
- The DM is a top-level post: `km-slack dm` writes no `/tmp/km-slack-posted.*` marker.
- The frozen golden `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` is hand-patched with exactly the `userdata.go` hunk, never re-captured (`CAPTURE_PRE92_BASELINE=1` is forbidden).
- Docs use generic placeholders (`example.com`, `U0KURT`), never real tenants or ids.
- Every implementer runs `go build ./...` and `go vet` on the packages touched before committing; stage by pathspec only; commit trailers:
  `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` and `Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt`.
- Never push or merge; the branch is `feat/slack-default-route`.

---

### Task 1: Envelope action + `OpenDM` on the poster adapter + `AppendContextBlock`

**Files:**
- Modify: `pkg/slack/payload.go` (actions block ~line 35; `SlackEnvelope` struct ~line 70)
- Modify: `pkg/slack/mention.go` (next to `PrependMentionBlock`)
- Modify: `pkg/slack/bridge/interfaces.go` (after `BlockPoster`, ~line 78)
- Modify: `pkg/slack/bridge/aws_adapters.go` (after `GetPermalink`, ~line 446)
- Test: `pkg/slack/payload_dm_test.go`, `pkg/slack/mention_test.go` (append), `pkg/slack/bridge/aws_adapters_dm_test.go`

**Interfaces:**
- Produces: `slack.ActionDM = "dm"`; `SlackEnvelope.TargetUser string` (`json:"target_user,omitempty"`), declared between `Subject` and `Text` so the struct stays alphabetical; `slack.AppendContextBlock(blocksJSON, mrkdwn string) (string, bool)`; `bridge.DMOpener` interface `{ OpenDM(ctx context.Context, userID string) (string, error) }`; `(*SlackPosterAdapter).OpenDM`.

- [ ] **Step 1: Failing tests for the envelope field**

`pkg/slack/payload_dm_test.go`:

```go
package slack_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// omitempty on TargetUser is load-bearing: the bridge re-canonicalises before
// verifying, so an envelope from a km-slack that predates the field must
// produce byte-identical canonical JSON on both sides.
func TestCanonicalJSON_TargetUserOmittedWhenEmpty(t *testing.T) {
	env, err := slack.BuildEnvelope(slack.ActionPost, "sb-1", "C1", "", "hi", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := slack.CanonicalJSON(env)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "target_user") {
		t.Fatalf("empty TargetUser must not serialise (breaks old signers): %s", b)
	}
	env.TargetUser = "U0KURT"
	b, _ = slack.CanonicalJSON(env)
	if !strings.Contains(string(b), `"target_user":"U0KURT"`) {
		t.Fatalf("TargetUser not serialised: %s", b)
	}
	if slack.ActionDM != "dm" {
		t.Fatalf("ActionDM = %q", slack.ActionDM)
	}
}
```

Append to `pkg/slack/mention_test.go`:

```go
func TestAppendContextBlock(t *testing.T) {
	out, ok := slack.AppendContextBlock(`[{"type":"section","text":{"type":"mrkdwn","text":"hi"}}]`, "From: <https://example.slack.com/archives/C1/p1|thread>")
	if !ok {
		t.Fatal("expected ok")
	}
	if !strings.Contains(out, `"type":"context"`) || !strings.HasSuffix(strings.TrimSpace(out), "]") {
		t.Fatalf("context block not appended: %s", out)
	}
	if _, ok := slack.AppendContextBlock(`not json`, "x"); ok {
		t.Fatal("unparseable blocks must return ok=false")
	}
	fifty := "[" + strings.Repeat(`{"type":"divider"},`, 49) + `{"type":"divider"}]`
	if _, ok := slack.AppendContextBlock(fifty, "x"); ok {
		t.Fatal("at the 50-block cap must return ok=false")
	}
}
```

- [ ] **Step 2: Run them, expect compile failures**

Run: `cd pkg/slack && go test -run 'TestCanonicalJSON_TargetUser|TestAppendContextBlock' .`
Expected: FAIL — `slack.ActionDM`, `env.TargetUser`, `slack.AppendContextBlock` undefined.

- [ ] **Step 3: Implement the envelope pieces**

`pkg/slack/payload.go`, in the actions const block:

```go
	ActionDM           = "dm"            // 2026-10-17 — bot-initiated DM: conversations.open + top-level chat.postMessage
```

In `SlackEnvelope`, between `Subject` and `Text`:

```go
	// TargetUser is the Slack user id a `dm` envelope opens a conversation with.
	// omitempty is LOAD-BEARING: the bridge re-canonicalises the struct before
	// verifying, so a field that always serialises would change the signed bytes
	// of every envelope from a km-slack built before this field existed.
	TargetUser  string `json:"target_user,omitempty"`
```

`pkg/slack/mention.go`, after `PrependMentionBlock`:

```go
// AppendContextBlock appends a context block carrying mrkdwn to a Block Kit
// array (used by the bridge to add the originating-thread permalink to a DM).
// ok=false when the array does not parse or already holds 50 blocks; the
// caller then drops the blocks and relies on the text fallback.
func AppendContextBlock(blocksJSON, mrkdwn string) (string, bool) {
	var blocks []json.RawMessage
	if err := json.Unmarshal([]byte(blocksJSON), &blocks); err != nil {
		return blocksJSON, false
	}
	if len(blocks) >= 50 {
		return blocksJSON, false
	}
	ctxBlock, _ := json.Marshal(map[string]any{
		"type":     "context",
		"elements": []map[string]any{{"type": "mrkdwn", "text": mrkdwn}},
	})
	blocks = append(blocks, ctxBlock)
	out, err := json.Marshal(blocks)
	if err != nil {
		return blocksJSON, false
	}
	return string(out), true
}
```

(Check `PrependMentionBlock`'s imports; `encoding/json` is already there.)

- [ ] **Step 4: Run, expect PASS**

Run: `cd pkg/slack && go test .`

- [ ] **Step 5: Failing test for `OpenDM`**

`pkg/slack/bridge/aws_adapters_dm_test.go` (package `bridge`; look at an existing adapter test in `aws_adapters_test.go` for the `fakeToken`-style `BotTokenFetcher` used with `SlackPosterAdapter` and reuse it):

```go
package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSlackPosterAdapter_OpenDM(t *testing.T) {
	var gotAuth, gotUsers, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotUsers, _ = body["users"].(string)
		switch gotUsers {
		case "U0KURT":
			w.Write([]byte(`{"ok":true,"channel":{"id":"D0KURT"}}`))
		default:
			w.Write([]byte(`{"ok":false,"error":"cannot_dm_bot"}`))
		}
	}))
	defer srv.Close()
	a := &SlackPosterAdapter{BaseURL: srv.URL, Tokens: staticToken("xoxb-test")}
	ch, err := a.OpenDM(context.Background(), "U0KURT")
	if err != nil || ch != "D0KURT" {
		t.Fatalf("OpenDM = %q, %v", ch, err)
	}
	if gotPath != "/conversations.open" || gotAuth != "Bearer xoxb-test" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if _, err := a.OpenDM(context.Background(), "UBOT"); err == nil || !strings.Contains(err.Error(), "cannot_dm_bot") {
		t.Fatalf("want slack error surfaced, got %v", err)
	}
	var _ DMOpener = a
}
```

`staticToken` is whatever the existing adapter tests use for a `BotTokenFetcher` fake; if none exists in package `bridge` tests, add at the bottom of this file:

```go
type staticTokenFetcher string

func (s staticTokenFetcher) Fetch(context.Context) (string, error) { return string(s), nil }
func staticToken(s string) BotTokenFetcher               { return staticTokenFetcher(s) }
```

- [ ] **Step 6: Run, expect compile failure on `DMOpener`/`OpenDM`**

Run: `cd pkg/slack/bridge && go test -run TestSlackPosterAdapter_OpenDM .`

- [ ] **Step 7: Implement the interface and adapter method**

`pkg/slack/bridge/interfaces.go`, after `BlockPoster`:

```go
// DMOpener is the optional Slack-API surface for ActionDM (2026-10-17). The
// handler type-asserts h.Slack.(DMOpener) and answers 500 dm_unsupported when
// the poster lacks it, so existing fakes keep satisfying SlackPoster.
type DMOpener interface {
	OpenDM(ctx context.Context, userID string) (channelID string, err error)
}
```

`pkg/slack/bridge/aws_adapters.go`, after `GetPermalink`:

```go
// OpenDM opens (or returns) the bot's DM channel with userID via
// conversations.open and returns its id. 2026-10-17 — km-slack dm. Its own
// decoder: chat.postMessage answers "channel" as a string, conversations.open
// as an object, so slackAPIResponse cannot carry both.
func (s *SlackPosterAdapter) OpenDM(ctx context.Context, userID string) (string, error) {
	token, err := s.Tokens.Fetch(ctx)
	if err != nil {
		return "", fmt.Errorf("bridge: get bot token for conversations.open: %w", err)
	}
	b, _ := json.Marshal(map[string]any{"users": userID})
	req, err := http.NewRequestWithContext(ctx, "POST", s.getBaseURL()+"/conversations.open", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	hc := s.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if rlErr := checkRateLimit(resp.StatusCode, resp.Header.Get("Retry-After"), "conversations.open"); rlErr != nil {
		return "", rlErr
	}
	var out struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error"`
		Channel struct {
			ID string `json:"id"`
		} `json:"channel"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("bridge: decode conversations.open: %w", err)
	}
	if !out.OK {
		return "", fmt.Errorf("bridge: conversations.open: %s", out.Error)
	}
	if out.Channel.ID == "" {
		return "", errors.New("bridge: conversations.open: no channel id in response")
	}
	return out.Channel.ID, nil
}
```

- [ ] **Step 8: Run, expect PASS; build and vet**

Run: `cd pkg/slack/bridge && go test . && cd ../../.. && go build ./... && go vet ./pkg/slack/... `

- [ ] **Step 9: Commit**

```bash
git add pkg/slack/payload.go pkg/slack/payload_dm_test.go pkg/slack/mention.go pkg/slack/mention_test.go pkg/slack/bridge/interfaces.go pkg/slack/bridge/aws_adapters.go pkg/slack/bridge/aws_adapters_dm_test.go
git commit -m "feat(slack): dm envelope action, TargetUser (omitempty), OpenDM on the poster adapter, AppendContextBlock"
```

---

### Task 2: Bridge `ActionDM` handling + wiring

**Files:**
- Create: `pkg/slack/bridge/handler_dm.go`
- Modify: `pkg/slack/bridge/handler.go` (Handler struct ~line 48; step 6 authz ~lines 244–300; dispatch switch ~line 306)
- Modify: `cmd/km-slack-bridge/main.go` (`wireDefaultRoute` ~line 741 and its call ~line 350)
- Test: `pkg/slack/bridge/handler_dm_test.go` (package `bridge_test`), `cmd/km-slack-bridge/default_route_wiring_test.go`

**Interfaces:**
- Consumes: `slack.ActionDM`, `SlackEnvelope.TargetUser`, `bridge.DMOpener`, `slack.AppendContextBlock` (Task 1); existing `h.threadRoutedToSender`, `h.checkQuota`, `h.Threads.Upsert`, `errResp`, `jsonResp`, `slackResponse`, `DefaultRoute.Allows`, `SenderEmailFetcher`.
- Produces: `Handler.DefaultRoute *DefaultRoute`, `Handler.SenderEmails SenderEmailFetcher`; `wireDefaultRoute(h *bridge.EventsHandler, out *bridge.Handler, getenv func(string) string, d defaultRouteDeps)`; bridge response `{"ok":true,"ts":…,"channel_id":"D…"}`.

- [ ] **Step 1: Failing handler tests**

`pkg/slack/bridge/handler_dm_test.go`. Helpers `defaultHandler`, `makeEnv`, `signRequest`, `responseBody`, `fakeSlack` live in `handler_test.go` (same package `bridge_test`).

```go
package bridge_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// dmPoster: fakeSlack plus DMOpener.
type dmPoster struct {
	fakeSlack
	opened    []string
	openErr   error
	dmChannel string
}

func (p *dmPoster) OpenDM(_ context.Context, u string) (string, error) {
	p.opened = append(p.opened, u)
	if p.openErr != nil {
		return "", p.openErr
	}
	return p.dmChannel, nil
}

type dmEmails struct {
	byUser map[string]string
	err    error
}

func (e *dmEmails) SenderEmail(_ context.Context, u string) (string, error) {
	if e.err != nil {
		return "", e.err
	}
	if v, ok := e.byUser[u]; ok {
		return v, nil
	}
	return "", bridge.ErrSenderIneligible
}

// recordingThreads: LookupSandbox from rows, records Upserts.
type recordingThreads struct {
	rows    map[string]string
	upserts []string // "chan|ts|sb"
}

func (r *recordingThreads) Get(_ context.Context, _, _ string) (string, error) { return "", nil }
func (r *recordingThreads) Upsert(_ context.Context, ch, ts, sb string) error {
	r.upserts = append(r.upserts, ch+"|"+ts+"|"+sb)
	return nil
}
func (r *recordingThreads) Rebind(_ context.Context, _, _, _ string) error           { return nil }
func (r *recordingThreads) MarkDispatched(_ context.Context, _, _, _ string) error   { return nil }
func (r *recordingThreads) DispatchBoundary(_ context.Context, _, _ string) (string, error) { return "", nil }
func (r *recordingThreads) LookupSandbox(_ context.Context, ch, ts string) (string, error) {
	return r.rows[ch+"|"+ts], nil
}
func (r *recordingThreads) LookupBySession(_ context.Context, _, _ string) (string, string, string, error) {
	return "", "", "", nil
}

func dmHandler(t *testing.T) (*bridge.Handler, *dmPoster, *recordingThreads, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	p := &dmPoster{fakeSlack: fakeSlack{returnTS: "1714280400.000777"}, dmChannel: "D0KURT"}
	h.Slack = p
	th := &recordingThreads{rows: map[string]string{"CFOREIGN|1.0": "sb-abc123"}}
	h.Threads = th
	h.DefaultRoute = bridge.NewDefaultRoute("ir-bot", "profiles/ir-bot.yaml", []string{"kurt@example.com"})
	h.SenderEmails = &dmEmails{byUser: map[string]string{"U0KURT": "kurt@example.com", "U0OTHER": "other@example.com"}}
	return h, p, th, priv
}

func dmEnv(target string) *slack.SlackEnvelope {
	env := makeEnv(slack.ActionDM, "sb-abc123", "")
	env.Subject = ""
	env.TargetUser = target
	return env
}

func TestHandler_DM_AllowedTarget_OpensPostsAndWritesRow(t *testing.T) {
	h, p, th, priv := dmHandler(t)
	resp := h.Handle(context.Background(), signRequest(t, dmEnv("U0KURT"), priv))
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["channel_id"] != "D0KURT" || body["ts"] != "1714280400.000777" {
		t.Fatalf("response %v", body)
	}
	if len(p.opened) != 1 || p.opened[0] != "U0KURT" {
		t.Fatalf("opened %v", p.opened)
	}
	if len(p.posted) != 1 || !strings.HasPrefix(p.posted[0], "D0KURT||") {
		t.Fatalf("posted %v (must be top-level in the DM channel, no subject)", p.posted)
	}
	if len(th.upserts) != 1 || th.upserts[0] != "D0KURT|1714280400.000777|sb-abc123" {
		t.Fatalf("row %v", th.upserts)
	}
}

func TestHandler_DM_UnlistedTarget_403_NoSlackCall(t *testing.T) {
	h, p, th, priv := dmHandler(t)
	resp := h.Handle(context.Background(), signRequest(t, dmEnv("U0OTHER"), priv))
	if resp.StatusCode != 403 || responseBody(t, resp)["error"] != "dm_not_allowed" {
		t.Fatalf("want 403 dm_not_allowed, got %d %s", resp.StatusCode, resp.Body)
	}
	if len(p.opened) != 0 || len(p.posted) != 0 || len(th.upserts) != 0 {
		t.Fatal("no Slack call and no row on a refusal")
	}
}

func TestHandler_DM_LookupError_403(t *testing.T) {
	h, p, _, priv := dmHandler(t)
	h.SenderEmails = &dmEmails{err: errors.New("users.info: ratelimited")}
	resp := h.Handle(context.Background(), signRequest(t, dmEnv("U0KURT"), priv))
	if resp.StatusCode != 403 || responseBody(t, resp)["error"] != "dm_not_allowed" {
		t.Fatalf("lookup error must deny: %d %s", resp.StatusCode, resp.Body)
	}
	if len(p.opened) != 0 {
		t.Fatal("no Slack call on a lookup error")
	}
}

func TestHandler_DM_NoRoute_403_Disabled(t *testing.T) {
	h, _, _, priv := dmHandler(t)
	h.DefaultRoute = nil
	resp := h.Handle(context.Background(), signRequest(t, dmEnv("U0KURT"), priv))
	if resp.StatusCode != 403 || responseBody(t, resp)["error"] != "dm_disabled" {
		t.Fatalf("want 403 dm_disabled, got %d %s", resp.StatusCode, resp.Body)
	}
}

func TestHandler_DM_BadTarget_400(t *testing.T) {
	h, _, _, priv := dmHandler(t)
	for _, bad := range []string{"", "kurt", "@U0KURT", "<@U0KURT>", "U0KURT/x"} {
		env := dmEnv(bad)
		env.Nonce = "0000000000000000000000000000" + strings.Repeat("a", 4)[:4-len(bad)%4] // fresh nonce per loop
		resp := h.Handle(context.Background(), signRequest(t, env, priv))
		if resp.StatusCode != 400 || responseBody(t, resp)["error"] != "bad_target" {
			t.Fatalf("%q: want 400 bad_target, got %d %s", bad, resp.StatusCode, resp.Body)
		}
	}
}

func TestHandler_DM_PostFails_NoRow(t *testing.T) {
	h, p, th, priv := dmHandler(t)
	p.postErr = errors.New("channel_not_found")
	resp := h.Handle(context.Background(), signRequest(t, dmEnv("U0KURT"), priv))
	if resp.StatusCode == 200 {
		t.Fatalf("post failure must not be 200: %s", resp.Body)
	}
	if len(th.upserts) != 0 {
		t.Fatal("row must be written only after a successful post")
	}
}

func TestHandler_DM_OpenFails_502(t *testing.T) {
	h, p, _, priv := dmHandler(t)
	p.openErr = errors.New("bridge: conversations.open: missing_scope")
	resp := h.Handle(context.Background(), signRequest(t, dmEnv("U0KURT"), priv))
	if resp.StatusCode != 502 || !strings.Contains(resp.Body, "missing_scope") {
		t.Fatalf("want 502 carrying the Slack error, got %d %s", resp.StatusCode, resp.Body)
	}
}

func TestHandler_DM_FromThread_RoutedRow_AppendsPermalink(t *testing.T) {
	h, p, _, priv := dmHandler(t)
	env := dmEnv("U0KURT")
	env.Channel, env.ThreadTS = "CFOREIGN", "1.0"
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d %s", resp.StatusCode, resp.Body)
	}
	if !strings.Contains(p.posted[0], "From: https://example.slack.com/archives/CFOREIGN/p1.0") {
		t.Fatalf("permalink not appended: %v", p.posted)
	}
}

func TestHandler_DM_FromThread_ForeignRow_403_NoDM(t *testing.T) {
	h, p, th, priv := dmHandler(t)
	th.rows["CFOREIGN|1.0"] = "sb-someone-else"
	env := dmEnv("U0KURT")
	env.Channel, env.ThreadTS = "CFOREIGN", "1.0"
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 403 || responseBody(t, resp)["error"] != "channel_mismatch" {
		t.Fatalf("want 403 channel_mismatch, got %d %s", resp.StatusCode, resp.Body)
	}
	if len(p.opened) != 0 {
		t.Fatal("no DM may be sent when the originating thread is not the sender's")
	}
}

func TestHandler_DM_FromOwnChannel_Allowed(t *testing.T) {
	h, p, _, priv := dmHandler(t)
	env := dmEnv("U0KURT")
	env.Channel, env.ThreadTS = "C0123ABC", "9.9" // the sender's owned channel (defaultHandler)
	if resp := h.Handle(context.Background(), signRequest(t, env, priv)); resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d %s", resp.StatusCode, resp.Body)
	}
	if !strings.Contains(p.posted[0], "/archives/C0123ABC/p9.9") {
		t.Fatalf("permalink missing: %v", p.posted)
	}
}

func TestHandler_DM_PosterWithoutOpener_500(t *testing.T) {
	h, _, _, priv := dmHandler(t)
	h.Slack = &fakeSlack{returnTS: "1.1"} // no OpenDM
	resp := h.Handle(context.Background(), signRequest(t, dmEnv("U0KURT"), priv))
	if resp.StatusCode != 500 || responseBody(t, resp)["error"] != "dm_unsupported" {
		t.Fatalf("want 500 dm_unsupported, got %d %s", resp.StatusCode, resp.Body)
	}
}

func TestHandler_DM_Operator_403(t *testing.T) {
	// Operator envelopes are signed with the operator key in handler_test.go's
	// operator tests; copy that fixture's key setup here and send ActionDM as
	// slack.SenderOperator. Expect 403 operator_action_forbidden.
	t.Skip("fill in from the operator fixture in handler_test.go if one exists; otherwise assert via Handle on an operator-signed env")
}
```

Replace the `TestHandler_DM_BadTarget_400` nonce line with something simpler if the fake nonce store needs it unique: `env.Nonce = fmt.Sprintf("%032x", i+1)` in an indexed loop (see `handler_thread_authz_test.go`). Replace the operator skip with a real test if `handler_test.go` has an operator-signing fixture (search for `SenderOperator`); if it has none, delete that test and add the operator check to Task 2's step 3 only.

- [ ] **Step 2: Run, expect compile failures**

Run: `cd pkg/slack/bridge && go test -run TestHandler_DM .`

- [ ] **Step 3: Implement**

`pkg/slack/bridge/handler.go`, add to `Handler` after `Threads`:

```go
	// 2026-10-17 — ActionDM (km-slack dm). Both set by wireDefaultRoute when the
	// default route is configured; nil ⇒ every dm answers 403 dm_disabled. The
	// target must pass the SAME email allowlist the inbound route applies.
	DefaultRoute *DefaultRoute
	SenderEmails SenderEmailFetcher
```

Step 6 authz. In the `isOperator` branch add, before the `if !isOperator` block:

```go
	if isOperator && env.Action == slack.ActionDM {
		logger.WarnContext(ctx, "bridge: operator_action_forbidden", "step", "authz", "action", env.Action, "status", 403)
		return errResp(403, "operator_action_forbidden")
	}
```

and change the ownership gate condition from `if env.Action != slack.ActionLookupThread {` to:

```go
		// ActionDM: the DM channel does not exist yet; handleDM authorizes the
		// target (email allowlist) and, with --from-thread, the originating thread.
		if env.Action != slack.ActionLookupThread && env.Action != slack.ActionDM {
```

Dispatch switch, add before `case slack.ActionArchive:`:

```go
	case slack.ActionDM:
		return h.handleDM(ctx, env)
```

New file `pkg/slack/bridge/handler_dm.go`:

```go
package bridge

import (
	"context"
	"regexp"

	"github.com/whereiskurt/klanker-maker/pkg/quota"
	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

var reDMTarget = regexp.MustCompile(`^[UW][A-Z0-9]+$`)

// handleDM is ActionDM (2026-10-17): a sandbox asks the bot to DM a person.
// Every refusal is fail-closed and names its step. Order: route configured →
// target shape → target email on the route allowlist → --from-thread
// authorization + permalink → quota → conversations.open → post → thread row.
func (h *Handler) handleDM(ctx context.Context, env slack.SlackEnvelope) *Response {
	if h.DefaultRoute == nil || h.SenderEmails == nil {
		logger.WarnContext(ctx, "bridge: dm_disabled", "step", "dm", "sender_id", env.SenderID, "status", 403)
		return errResp(403, "dm_disabled")
	}
	if !reDMTarget.MatchString(env.TargetUser) {
		logger.WarnContext(ctx, "bridge: dm_bad_target", "step", "dm", "sender_id", env.SenderID, "status", 400)
		return errResp(400, "bad_target")
	}
	email, err := h.SenderEmails.SenderEmail(ctx, env.TargetUser)
	if err != nil || !h.DefaultRoute.Allows(email) {
		reason := "not on allow_emails"
		if err != nil {
			reason = err.Error()
		}
		logger.WarnContext(ctx, "bridge: dm_not_allowed", "step", "dm", "sender_id", env.SenderID,
			"target", env.TargetUser, "reason", reason, "status", 403)
		return errResp(403, "dm_not_allowed")
	}

	body, blocks := env.Body, env.Blocks
	if env.Channel != "" && env.ThreadTS != "" {
		owned, oerr := h.Channels.OwnedChannel(ctx, env.SenderID)
		if oerr != nil {
			logger.ErrorContext(ctx, "bridge: channel_lookup_failed", "step", "dm", "sender_id", env.SenderID, "error", oerr.Error(), "status", 500)
			return errResp(500, "channel_lookup_failed")
		}
		if owned == "" || owned != env.Channel {
			probe := env
			probe.Action = slack.ActionPost // threadRoutedToSender admits post/upload only
			if !h.threadRoutedToSender(ctx, probe) {
				logger.WarnContext(ctx, "bridge: channel_mismatch", "step", "dm", "sender_id", env.SenderID,
					"channel", env.Channel, "thread_ts", env.ThreadTS, "status", 403)
				return errResp(403, "channel_mismatch")
			}
		}
		if link, perr := h.Slack.GetPermalink(ctx, env.Channel, env.ThreadTS); perr != nil {
			logger.WarnContext(ctx, "bridge: dm_permalink_failed", "step", "dm", "error", perr.Error())
		} else {
			line := "From: " + link
			body += "\n\n" + line
			if blocks != "" {
				if withLink, ok := slack.AppendContextBlock(blocks, line); ok {
					blocks = withLink
				} else {
					blocks = "" // text fallback still carries the link
				}
			}
		}
	}

	if _, block := h.checkQuota(ctx, env.SenderID, quota.ActionSlackPost, "", ""); block {
		return errResp(429, "quota_exceeded")
	}

	opener, ok := h.Slack.(DMOpener)
	if !ok {
		logger.ErrorContext(ctx, "bridge: dm_unsupported", "step", "dm", "status", 500)
		return errResp(500, "dm_unsupported")
	}
	dmChannel, err := opener.OpenDM(ctx, env.TargetUser)
	if err != nil {
		logger.ErrorContext(ctx, "bridge: slack_call_failed", "step", "dm", "action", "conversations.open",
			"target", env.TargetUser, "slack_error", err.Error(), "status", 502)
		return slackResponse("", err)
	}

	var ts string
	if blocks != "" {
		if bp, okBP := h.Slack.(BlockPoster); okBP {
			ts, err = bp.PostMessageBlocks(ctx, dmChannel, env.Subject, body, blocks, "")
		} else {
			ts, err = h.Slack.PostMessage(ctx, dmChannel, env.Subject, body, "")
		}
	} else {
		ts, err = h.Slack.PostMessage(ctx, dmChannel, env.Subject, body, "")
	}
	if err != nil {
		logger.ErrorContext(ctx, "bridge: slack_call_failed", "step", "dm", "action", "chat.postMessage",
			"channel", dmChannel, "slack_error", err.Error())
		return slackResponse("", err)
	}
	// Continuity: a reply under this DM message routes back to this sandbox.
	// Written only after the post succeeded; a failure is logged, the DM stands.
	if h.Threads != nil {
		if uerr := h.Threads.Upsert(ctx, dmChannel, ts, env.SenderID); uerr != nil {
			logger.WarnContext(ctx, "bridge: dm_thread_row_failed", "step", "dm", "channel", dmChannel, "ts", ts, "error", uerr.Error())
		}
	}
	logger.InfoContext(ctx, "bridge: dm", "sender_id", env.SenderID, "target", env.TargetUser, "channel", dmChannel, "ts", ts, "status", 200)
	return jsonResp(200, map[string]any{"ok": true, "ts": ts, "channel_id": dmChannel})
}
```

Check how `handler.go` names its logger (the `logger.WarnContext` calls in `Handle`); use the identical identifier. Check `slackResponse("", err)` yields 502 for a plain error (it does for `GetPermalink` today).

`cmd/km-slack-bridge/main.go`: change the signature to
`func wireDefaultRoute(h *bridge.EventsHandler, out *bridge.Handler, getenv func(string) string, d defaultRouteDeps)` and after `h.SenderEmails = …` add:

```go
	if out != nil {
		out.DefaultRoute = route
		out.SenderEmails = h.SenderEmails // same cache; a deny is never cached
	}
```

Update the call site to `wireDefaultRoute(eventsHandler, handler, os.Getenv, defaultRouteDeps{…})`. Update `default_route_wiring_test.go`: pass `nil` for the first two calls and a `&bridge.Handler{}` for the third, then assert `out.DefaultRoute != nil && out.SenderEmails != nil`.

- [ ] **Step 4: Run, expect PASS**

Run: `cd pkg/slack/bridge && go test . && cd ../../../cmd/km-slack-bridge && go test . && cd ../.. && go build ./... && go vet ./pkg/slack/bridge/ ./cmd/km-slack-bridge/`

Also confirm the existing authz tests still pass unchanged: `go test -run 'ForeignChannel' ./pkg/slack/bridge/`.

- [ ] **Step 5: Commit**

```bash
git add pkg/slack/bridge/handler.go pkg/slack/bridge/handler_dm.go pkg/slack/bridge/handler_dm_test.go cmd/km-slack-bridge/main.go cmd/km-slack-bridge/default_route_wiring_test.go
git commit -m "feat(slack-bridge): dm action — allowlisted target, conversations.open, top-level post, continuity row"
```

---

### Task 3: `km-slack dm` verb

**Files:**
- Create: `cmd/km-slack/dm.go`
- Modify: `cmd/km-slack/main.go` (verb switch ~line 63; usage text; extract the render switch out of `runWith` ~lines 392–432 into `renderBody`)
- Test: `cmd/km-slack/dm_test.go`

**Interfaces:**
- Consumes: `slack.ActionDM`, `SlackEnvelope.TargetUser`, `slack.BuildEnvelope`, `slack.SignEnvelope`, `slack.PostToBridge`, `PostResponse.ChannelID`, `reMentionUserID`-equivalent validation (`slack.ParseMentions` rejects names; reuse it on a one-element slice).
- Produces: `renderBody(renderMode string, body []byte) (rendered, blocksJSON string)`; `runDM(args []string, stderr io.Writer) int`; `runDMWith(ctx, priv, sandboxID, bridgeURL string, o dmOptions) (channelID, ts string, err error)`.

- [ ] **Step 1: Failing tests**

`cmd/km-slack/dm_test.go` (package `main`). Look at `main_test.go` for how `runWith` is tested against an `httptest` bridge (it decodes the posted envelope and answers `{"ok":true,"ts":…}`); mirror that server here.

```go
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

func TestRunDM_RejectsNamesAndMissingText(t *testing.T) {
	var errb bytes.Buffer
	if rc := runDM([]string{"--to", "kurt", "--text", "hi"}, &errb); rc != 2 || !strings.Contains(errb.String(), "user id") {
		t.Fatalf("name must be rejected at parse time: rc=%d %s", rc, errb.String())
	}
	errb.Reset()
	if rc := runDM([]string{"--to", "U0KURT"}, &errb); rc != 2 {
		t.Fatalf("missing --text/--body must be rc 2, got %d %s", rc, errb.String())
	}
	errb.Reset()
	t.Setenv("KM_SLACK_CHANNEL_ID", "")
	t.Setenv("KM_SLACK_THREAD_TS", "")
	if rc := runDM([]string{"--to", "U0KURT", "--text", "hi", "--from-thread"}, &errb); rc != 2 || !strings.Contains(errb.String(), "KM_SLACK_THREAD_TS") {
		t.Fatalf("--from-thread without the turn env must be rc 2 naming the var: rc=%d %s", rc, errb.String())
	}
}

func TestRunDMWith_EnvelopeShapeAndNoMarker(t *testing.T) {
	var got slack.SlackEnvelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Envelope  json.RawMessage `json:"envelope"`
			Signature string          `json:"signature"`
		}
		b, _ := io.ReadAll(r.Body)
		// PostToBridge's wire format: copy the decoding main_test.go uses.
		json.Unmarshal(b, &req)
		json.Unmarshal(req.Envelope, &got)
		w.Write([]byte(`{"ok":true,"ts":"1.5","channel_id":"D0KURT"}`))
	}))
	defer srv.Close()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	markers := t.TempDir()
	t.Setenv("KM_SLACK_POSTED_DIR", markers)

	ch, ts, err := runDMWith(context.Background(), priv, "sb-1", srv.URL, dmOptions{to: "U0KURT", text: "hello", render: "plain"})
	if err != nil || ch != "D0KURT" || ts != "1.5" {
		t.Fatalf("got %q %q %v", ch, ts, err)
	}
	if got.Action != slack.ActionDM || got.TargetUser != "U0KURT" || got.Body != "hello" || got.Channel != "" || got.ThreadTS != "" {
		t.Fatalf("envelope %+v", got)
	}
	if entries, _ := os.ReadDir(markers); len(entries) != 0 {
		t.Fatalf("a DM is top-level and must write no posted marker: %v", entries)
	}

	_, _, err = runDMWith(context.Background(), priv, "sb-1", srv.URL, dmOptions{to: "U0KURT", text: "hello", render: "plain", fromChannel: "C1", fromThread: "1.0"})
	if err != nil || got.Channel != "C1" || got.ThreadTS != "1.0" {
		t.Fatalf("--from-thread must carry channel+thread: %+v %v", got, err)
	}
}

func TestRunDMWith_BodyFileAndBlocks(t *testing.T) {
	var got slack.SlackEnvelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Envelope json.RawMessage `json:"envelope"` }
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &req)
		json.Unmarshal(req.Envelope, &got)
		w.Write([]byte(`{"ok":true,"ts":"1.6","channel_id":"D0KURT"}`))
	}))
	defer srv.Close()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	f := filepath.Join(t.TempDir(), "body.md")
	os.WriteFile(f, []byte("# Summary\n\n- one\n- two\n"), 0o644)
	if _, _, err := runDMWith(context.Background(), priv, "sb-1", srv.URL, dmOptions{to: "U0KURT", bodyPath: f, render: "blocks-rich"}); err != nil {
		t.Fatal(err)
	}
	if got.Blocks == "" {
		t.Fatalf("blocks-rich must populate Blocks: %+v", got)
	}
}
```

Add `"io"` to the imports. If `main_test.go` decodes the bridge request differently (field names), copy its exact decoding.

- [ ] **Step 2: Run, expect compile failures**

Run: `cd cmd/km-slack && go test -run 'TestRunDM' .`

- [ ] **Step 3: Implement**

In `main.go`, extract the render switch from `runWith` into a function placed just above `runWith` and call it from `runWith` (behaviour-preserving; the existing `runWith` tests prove it):

```go
// renderBody applies the km-slack render tier to body and returns the text
// fallback plus the Block Kit JSON ("" when the tier produced none). Shared by
// post/reply and dm. Degrades blocks-rich → blocks → mrkdwn exactly as before.
func renderBody(renderMode string, body []byte) (rendered, blocksJSON string) {
	switch renderMode {
	case "blocks-rich":
		aiFooter := os.Getenv("KM_SLACK_AI_FOOTER") == "true"
		if bj, fallback, ok := slack.RenderRich(string(body), aiFooter); ok {
			return fallback, bj
		}
		if bj2, fallback2, ok := slack.RenderBlocks(string(body)); ok {
			return fallback2, bj2
		}
		return slack.Mrkdwnify(string(body)), ""
	case "blocks":
		if bj, fallback, ok := slack.RenderBlocks(string(body)); ok {
			return fallback, bj
		}
		return slack.Mrkdwnify(string(body)), ""
	case "mrkdwn":
		return slack.Mrkdwnify(string(body)), ""
	default:
		return string(body), ""
	}
}
```

Replace the switch in `runWith` with `rendered, blocksJSON := renderBody(renderMode, body)` (keep the mention and size handling that follows it unchanged).

Verb switch in `main()`: add `case "dm": os.Exit(runDM(os.Args[2:], os.Stderr))` beside `reply`, and a usage line:
`  dm --to U… (--text <s> | --body <file>) [--render …] [--from-thread]   DM a person on the route allowlist (top-level; no marker)`.

`cmd/km-slack/dm.go`:

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

type dmOptions struct {
	to          string
	text        string
	bodyPath    string
	render      string
	fromChannel string
	fromThread  string
}

// runDM — `km-slack dm`: ask the bridge to DM a person. The bridge admits only
// a target whose email is on slack.default_route.allow_emails. Top-level post:
// no posted marker, so the originating thread's automatic reply still lands.
func runDM(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("dm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o dmOptions
	var fromThread bool
	fs.StringVar(&o.to, "to", "", "Slack user id to DM (U…/W…); names are rejected — take the id from the [Slack] From: line")
	fs.StringVar(&o.text, "text", "", "Message text (or --body)")
	fs.StringVar(&o.bodyPath, "body", "", "Path to a body file (or --text)")
	fs.StringVar(&o.render, "render", "", "plain|mrkdwn|blocks|blocks-rich (default: KM_SLACK_RENDER or plain)")
	fs.BoolVar(&fromThread, "from-thread", false, "Append a permalink to the current turn's thread (KM_SLACK_CHANNEL_ID + KM_SLACK_THREAD_TS)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if _, err := slack.ParseMentions([]string{o.to}); err != nil || o.to == "" {
		fmt.Fprintf(stderr, "km-slack dm: --to must be a Slack user id (U…), not a name: %v\n", err)
		return 2
	}
	if (o.text == "") == (o.bodyPath == "") {
		fmt.Fprintln(stderr, "km-slack dm: exactly one of --text or --body is required")
		return 2
	}
	if fromThread {
		o.fromChannel, o.fromThread = os.Getenv("KM_SLACK_CHANNEL_ID"), os.Getenv("KM_SLACK_THREAD_TS")
		if o.fromChannel == "" || o.fromThread == "" {
			fmt.Fprintln(stderr, "km-slack dm: --from-thread needs KM_SLACK_CHANNEL_ID and KM_SLACK_THREAD_TS (set per turn by the inbound poller)")
			return 2
		}
	}
	if o.render == "" {
		o.render = os.Getenv("KM_SLACK_RENDER")
	}
	if o.render == "" {
		o.render = "plain"
	}

	sandboxID, bridgeURL := os.Getenv("KM_SANDBOX_ID"), os.Getenv("KM_SLACK_BRIDGE_URL")
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if sandboxID == "" || bridgeURL == "" || region == "" {
		fmt.Fprintln(stderr, "km-slack dm: KM_SANDBOX_ID, KM_SLACK_BRIDGE_URL and AWS_REGION must be set")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()
	priv, err := loadPrivateKey(ctx, region, sandboxID)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack dm: load signing key: %v\n", err)
		return 1
	}
	ch, ts, err := runDMWith(ctx, priv, sandboxID, bridgeURL, o)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack dm: %v\n", err)
		return 1
	}
	fmt.Printf("channel=%s ts=%s\n", ch, ts)
	return 0
}

// runDMWith renders, signs and POSTs an ActionDM envelope. Returns the DM
// channel id and message ts from the bridge.
func runDMWith(ctx context.Context, priv ed25519.PrivateKey, sandboxID, bridgeURL string, o dmOptions) (string, string, error) {
	body := []byte(o.text)
	if o.bodyPath != "" {
		b, err := os.ReadFile(o.bodyPath)
		if err != nil {
			return "", "", fmt.Errorf("read body file: %w", err)
		}
		body = b
	}
	rendered, blocksJSON := renderBody(o.render, body)
	if len(rendered) > slack.MaxRenderedBytes {
		rendered = rendered[:slack.MaxRenderedBytes] + "\n_…truncated_"
	}
	env, err := slack.BuildEnvelope(slack.ActionDM, sandboxID, o.fromChannel, "", rendered, o.fromThread)
	if err != nil {
		return "", "", err
	}
	env.TargetUser = o.to
	env.Blocks = blocksJSON
	_, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		return "", "", err
	}
	resp, err := slack.PostToBridge(ctx, bridgeURL, env, sig)
	if err != nil {
		return "", "", err
	}
	if !resp.OK {
		return "", "", errors.New("bridge returned not-ok: " + resp.Error)
	}
	fmt.Fprintf(os.Stderr, "km-slack: dm sent channel=%s ts=%s\n", resp.ChannelID, resp.TS)
	return resp.ChannelID, resp.TS, nil
}
```

Add `"crypto/ed25519"` to the imports. `PostToBridge` already returns an error for a not-ok body in some paths; keep the explicit check so a `403 dm_not_allowed` reads as `bridge returned not-ok: dm_not_allowed`.

- [ ] **Step 4: Run, expect PASS (including the existing `runWith` tests after the extraction)**

Run: `cd cmd/km-slack && go test . && cd ../.. && go build ./... && go vet ./cmd/km-slack/`

- [ ] **Step 5: Commit**

```bash
git add cmd/km-slack/main.go cmd/km-slack/dm.go cmd/km-slack/dm_test.go
git commit -m "feat(km-slack): dm verb — DM a person on the route allowlist, optional --from-thread permalink"
```

---

### Task 4: `im:write` scope — manifest, init scope check, doctor, permissions doc

**Files:**
- Modify: `internal/app/cmd/slack_manifest_template.json` (`oauth_config.scopes.bot`, after `"mpim:history"`)
- Modify: `internal/app/cmd/testdata/slack_manifest_golden.json` (same position)
- Modify: `internal/app/cmd/slack.go` (`VerifyEventsAPIScopes` ~line 964)
- Modify: `internal/app/cmd/doctor_slack.go` (`checkSlackDefaultRoute` step 3 ~lines 710–731)
- Modify: `docs/slack-app-permissions.md` (bot scopes table)
- Test: `internal/app/cmd/slack_manifest_test.go` (~line 154 and ~172), `internal/app/cmd/doctor_slack_test.go` (find the existing `Slack default route scopes` test)

- [ ] **Step 1: Extend the failing tests**

In `slack_manifest_test.go` ~line 154 add `"im:write"` to the wanted list:
`for _, want := range []string{`"im:history"`, `"mpim:history"`, `"im:write"`, `"message.im"`, `"message.mpim"`} {`
and ~line 172 add a sibling assertion that `im:write` is required:

```go
	ok, missing = VerifyEventsAPIScopes([]string{"channels:history", "groups:history", "im:history", "mpim:history", "reactions:write", "files:read"})
	if ok || !hasString(missing, "im:write") {
		t.Fatalf("im:write must be required; ok=%v missing=%v", ok, missing)
	}
```

In the doctor test for `Slack default route scopes`, add a case: scopes `im:history,mpim:history` without `im:write` → `CheckWarn` whose message contains `im:write`; and the OK message becomes `im:history, mpim:history and im:write present`.

- [ ] **Step 2: Run, expect FAIL**

Run: `cd internal/app/cmd && go test -run 'Manifest|Scope|DefaultRoute' .`

- [ ] **Step 3: Implement**

Template and golden: insert `"im:write",` directly after `"mpim:history",`.

`slack.go`: `required := []string{"channels:history", "groups:history", "im:history", "mpim:history", "im:write", "reactions:write", "files:read"}` and update the function comment to name `im:write` ("the bot opens a DM for `km-slack dm`").

`doctor_slack.go` step 3:

```go
	// 3. Scopes: DMs to the bot need im:history / mpim:history; km-slack dm needs im:write.
	…
	for _, s := range []string{"im:history", "mpim:history", "im:write"} {
	…
		Message:     "missing " + strings.Join(missing, ", ") + " — DMs to the bot are never delivered (history) / the bot cannot open a DM (im:write)",
	…
		Message: "im:history, mpim:history and im:write present"})
```

`docs/slack-app-permissions.md`: add a row to the bot-scopes table:
`| \`im:write\` | Open a DM with a person when a sandbox agent runs \`km-slack dm\` (\`conversations.open\`). Never used to read. The bridge admits only targets on \`slack.default_route.allow_emails\`. |`

- [ ] **Step 4: Run, expect PASS**

Run: `cd internal/app/cmd && go test -run 'Manifest|Scope|DefaultRoute|Doctor' . && cd ../../.. && go build ./... && go vet ./internal/app/cmd/`

- [ ] **Step 5: Commit**

```bash
git add internal/app/cmd/slack_manifest_template.json internal/app/cmd/testdata/slack_manifest_golden.json internal/app/cmd/slack.go internal/app/cmd/doctor_slack.go internal/app/cmd/slack_manifest_test.go internal/app/cmd/doctor_slack_test.go docs/slack-app-permissions.md
git commit -m "feat(slack): im:write in the manifest, init scope check and doctor — the bot can open a DM"
```

---

### Task 5: Preamble sentence naming the verb

**Files:**
- Modify: `pkg/compiler/userdata.go` (the `printf '[Slack] From: …'` line inside `# --- km-slack-sender-preamble ---`, ~line 2355)
- Modify: `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` (same line; hand-patch)
- Test: `pkg/compiler/userdata_slack_sender_test.go` (`TestUserdata_SlackInbound_SenderPreambleIsPrepended`)

- [ ] **Step 1: Failing test**

In `TestUserdata_SlackInbound_SenderPreambleIsPrepended`, add `"km-slack dm --to U0KURT"` and `"does not replace your reply here"` to the `want` list.

- [ ] **Step 2: Run, expect FAIL**

Run: `cd pkg/compiler && go test -run TestUserdata_SlackInbound_SenderPreambleIsPrepended .`

- [ ] **Step 3: Patch template and frozen golden with one script that asserts each match exactly once**

In the `printf` format string, after `to notify this person write <@%s> inline in that reply.` insert:
` To send this person a direct message run: km-slack dm --to %s --text '"'"'...'"'"' (it does not replace your reply here).`
and append one more `"$SENDER_ID"` argument after the existing third `"$SENDER_ID"` (the format now consumes: sender, channel, bot note, sender, sender). Apply the identical replacement to the frozen golden; verify `git diff --stat` shows the two files with the same hunk size, and `git diff pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh | grep -c '^[-+][^-+]'` equals the template's count.

Note the single-quote escaping: the preamble line is inside a single-quoted `printf` format; the pattern `'"'"'...'"'"'` yields `'...'` in the rendered prompt. Run the rendered-bash test to prove it rather than reasoning about it.

- [ ] **Step 4: Run, expect PASS**

Run: `cd pkg/compiler && go test . && cd ../.. && go vet ./pkg/compiler/`

- [ ] **Step 5: Commit**

```bash
git add pkg/compiler/userdata.go pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh pkg/compiler/userdata_slack_sender_test.go
git commit -m "feat(slack): the sender preamble names km-slack dm"
```

---

### Task 6: Skill, plugin bump, docs

**Files:**
- Modify: `skills/slack/SKILL.md` (new subsection after "Mentioning people", ~line 258; fix the paragraph at ~line 255 that says the Stop-hook posts the final answer)
- Modify: `.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json` (`0.4.18` → `0.4.19`)
- Modify: `docs/slack-notifications.md` (new `### Bot-initiated DMs (2026-10-17)` after `### One reply per turn`)
- Modify: `docs/slack-default-route.md` (§4 decisions table "Reach" row; §11 follow-ups: the DM item becomes "shipped")
- Modify: `CLAUDE.md` (one bullet appended to the "Slack default route + strict mention-only threads" block; one row in "Where to look")

- [ ] **Step 1: SKILL.md**

Replace the paragraph beginning `The Stop-hook auto-post of your final answer uses the inline path` with:

```markdown
Your final reply is posted to the thread automatically when the turn ends, and a `<@U…>` in it
notifies the person. Any `km-slack post`/`reply` into the thread **cancels** that automatic post
(the poller sees the marker km-slack leaves), so such a call must carry your complete answer.
Reach for `--mention` only on a post you make deliberately and completely.
```

Add after it:

```markdown
### Sending someone a DM ("can you DM me the summary?")

```bash
# The id comes from the [Slack] From: line / $KM_SLACK_SENDER_ID — never a name.
/opt/km/bin/km-slack dm --to "$KM_SLACK_SENDER_ID" --text "Summary: …"
/opt/km/bin/km-slack dm --to "$KM_SLACK_SENDER_ID" --body /tmp/summary.md --render blocks-rich --from-thread
```

- The DM is a **top-level** message from the bot; your reply in the current thread still posts
  automatically. `--from-thread` appends a permalink back to this thread.
- The bridge sends it only to a person on the install's `slack.default_route.allow_emails`.
  `bridge returned not-ok: dm_not_allowed` means the person is not listed; `dm_disabled` means
  this install has no default route. Say so in your reply rather than retrying.
- A reply the person types **under** your DM message comes back to this sandbox.
```

Bump both plugin version files to `0.4.19`.

- [ ] **Step 2: docs/slack-notifications.md**

After `### One reply per turn (2026-10-17)` add:

```markdown
### Bot-initiated DMs (2026-10-17)

`km-slack dm --to U… (--text | --body file) [--render …] [--from-thread]` asks the bridge to
open a DM with a person and post there, top-level. The bridge admits only a target whose
email (via `users.info`, the same cached fetcher as the inbound gate) is on
`slack.default_route.allow_emails`; without a default route every `dm` is `403 dm_disabled`.
On success it writes a `km-slack-threads` row for the DM message, so a reply typed under it
routes back to the same sandbox; a fresh top-level DM still goes through the default route.
Refusals, all fail-closed: `dm_disabled`, `bad_target`, `dm_not_allowed`, `channel_mismatch`
(`--from-thread` on a thread that is not the sender's), `quota_exceeded`, `dm_unsupported`.
Operators cannot send `dm`. New scope `im:write`; `km doctor`'s `Slack default route scopes`
requires it. The envelope field `target_user` is `omitempty` on purpose: the bridge
re-canonicalises before verifying, so a non-omitempty field would break every older
`km-slack`'s signatures.

Deploy = `make build` + `make build-lambdas` + `km init --dry-run=false` + `km slack manifest`
→ paste, reinstall → `km slack rotate-token --bot-token <new>` → `km doctor` → recreate the
default sandbox so its `km-slack` carries the verb.
```

- [ ] **Step 3: docs/slack-default-route.md and CLAUDE.md**

§4 "Reach" row: replace the trailing sentence with "`im:write` was added on 2026-10-17 for `km-slack dm` (bot-initiated DM, allowlist-gated; see `docs/slack-notifications.md` § Bot-initiated DMs)." §11: add under "Can wait" → move to a new line "Shipped 2026-10-17: bot-initiated DMs (`km-slack dm`)". `CLAUDE.md`: append one bullet to the Slack default route block:

```markdown
- **`km-slack dm --to U… ` (2026-10-17): the bot opens a DM with a person on the route
  allowlist and posts top-level; a reply under it routes back to the sandbox (thread row written
  after the post). New scope `im:write`. `target_user` is `omitempty` because the bridge
  re-canonicalises before verifying. See `docs/slack-notifications.md` § Bot-initiated DMs.
```

and a "Where to look" row: `| Ask the bot to DM someone — \`km-slack dm\`, who it may DM, the continuity row, the \`im:write\` scope | \`docs/slack-notifications.md\` § Bot-initiated DMs |`.

- [ ] **Step 4: Verify the plugin files parse and the skill renders**

Run: `python3 -c "import json;json.load(open('.claude-plugin/plugin.json'));json.load(open('.claude-plugin/marketplace.json'))" && grep -c "km-slack dm" skills/slack/SKILL.md`

- [ ] **Step 5: Commit**

```bash
git add skills/slack/SKILL.md .claude-plugin/plugin.json .claude-plugin/marketplace.json docs/slack-notifications.md docs/slack-default-route.md CLAUDE.md
git commit -m "docs(slack): km-slack dm in the skill (plugin 0.4.19), notifications doc, route write-up, CLAUDE.md"
```

---

## Execution order

1 → 2 → 3 → 4 → 5 → 6. Task 4 has no code dependency on 1–3 and may run earlier if a worker is idle, but never in parallel with another implementer.

## After the plan: deploy and live UAT (controller, not a task)

`make build` → `make build-lambdas` → `km init --dry-run=false` → `km slack manifest` (operator pastes, reinstalls) → `km slack rotate-token` (operator) → `km doctor` → recreate `ir-bot` → spec §7 steps 1–5.
