package bridge_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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
	rows     map[string]string
	upserts  []string // "chan|ts|sb"
	sessions []string // "chan|ts|sid|agent" (ActionSession)
}

func (r *recordingThreads) SetSession(_ context.Context, ch, ts, sid, agent string) error {
	r.sessions = append(r.sessions, ch+"|"+ts+"|"+sid+"|"+agent)
	return nil
}

func (r *recordingThreads) Get(_ context.Context, _, _ string) (string, error) { return "", nil }
func (r *recordingThreads) Upsert(_ context.Context, ch, ts, sb string) error {
	r.upserts = append(r.upserts, ch+"|"+ts+"|"+sb)
	return nil
}
func (r *recordingThreads) Rebind(_ context.Context, _, _, _ string) error         { return nil }
func (r *recordingThreads) MarkDispatched(_ context.Context, _, _, _ string) error { return nil }
func (r *recordingThreads) DispatchBoundary(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
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
	h.DefaultRoute = bridge.NewDefaultRoute("ir-bot", "profiles/ir-bot.yaml", []string{"kurt@example.com"}, nil)
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

type dmErrByID struct{ err error }

func (e dmErrByID) FetchBySandboxID(_ context.Context, _ string) (bridge.SandboxRoutingInfo, error) {
	return bridge.SandboxRoutingInfo{}, e.err
}

func TestHandler_DM_SenderRowFetchError_FailsClosed(t *testing.T) {
	h, p, th, priv := dmHandler(t)
	h.SandboxByID = dmErrByID{err: errors.New("ddb blip")}
	resp := h.Handle(context.Background(), signRequest(t, dmEnv("U0KURT"), priv))
	if resp.StatusCode != 403 || responseBody(t, resp)["error"] != "dm_not_allowed" {
		t.Fatalf("sender-row read error must deny: %d %s", resp.StatusCode, resp.Body)
	}
	if len(p.opened) != 0 || len(p.posted) != 0 || len(th.upserts) != 0 {
		t.Fatal("no Slack call and no row on a sender-row read error")
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
	for i, bad := range []string{"", "kurt", "@U0KURT", "<@U0KURT>", "U0KURT/x"} {
		env := dmEnv(bad)
		env.Nonce = fmt.Sprintf("%032x", i+1) // the fake nonce store rejects replays
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

// An operator-signed dm is refused at authz: the operator has no sandbox to
// anchor the continuity row, and dm is a sandbox verb.
func TestHandler_DM_Operator_403(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	p := &dmPoster{fakeSlack: fakeSlack{returnTS: "1.1"}, dmChannel: "D0KURT"}
	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{slack.SenderOperator: pub}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{}},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    p,
	}
	h.DefaultRoute = bridge.NewDefaultRoute("ir-bot", "profiles/ir-bot.yaml", []string{"kurt@example.com"}, nil)
	h.SenderEmails = &dmEmails{byUser: map[string]string{"U0KURT": "kurt@example.com"}}
	env := makeEnv(slack.ActionDM, slack.SenderOperator, "")
	env.TargetUser = "U0KURT"
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 403 || responseBody(t, resp)["error"] != "operator_action_forbidden" {
		t.Fatalf("want 403 operator_action_forbidden, got %d %s", resp.StatusCode, resp.Body)
	}
	if len(p.opened) != 0 || len(p.posted) != 0 {
		t.Fatal("no Slack call on an operator dm")
	}
}

// A tripped slack_post quota refuses the dm before conversations.open. With
// --from-thread the trip notice lands on the originating thread.
func TestHandler_DM_QuotaTrip_429_NoOpen(t *testing.T) {
	for _, fromThread := range []bool{false, true} {
		h, p, th, priv := dmHandler(t)
		h.Quota = bridge.NewFakeQuotaClient(2) // count 2 > limit 1
		h.QuotaTable = "km-action-quota"
		h.Limits = &fakeActionLimits{limitsJSON: `{"slack_post":{"perHour":1,"onBreach":"block"}}`}
		env := dmEnv("U0KURT")
		if fromThread {
			env.Channel, env.ThreadTS = "CFOREIGN", "1.0"
		}
		resp := h.Handle(context.Background(), signRequest(t, env, priv))
		if resp.StatusCode != 429 || responseBody(t, resp)["error"] != "quota_exceeded" {
			t.Fatalf("fromThread=%v: want 429 quota_exceeded, got %d %s", fromThread, resp.StatusCode, resp.Body)
		}
		if len(p.opened) != 0 || len(th.upserts) != 0 {
			t.Fatalf("fromThread=%v: no OpenDM and no row on a quota trip (opened %v)", fromThread, p.opened)
		}
		if fromThread {
			if len(p.posted) != 1 || !strings.HasPrefix(p.posted[0], "CFOREIGN|") || !strings.Contains(p.posted[0], "Quota exceeded") {
				t.Fatalf("trip notice must land on the originating thread: %v", p.posted)
			}
		}
	}
}

// A half-set from-thread (channel without thread_ts) still sends the plain DM.
func TestHandler_DM_HalfSetFromThread_SendsPlainDM(t *testing.T) {
	h, p, _, priv := dmHandler(t)
	env := dmEnv("U0KURT")
	env.Channel = "CFOREIGN"
	if resp := h.Handle(context.Background(), signRequest(t, env, priv)); resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d %s", resp.StatusCode, resp.Body)
	}
	if len(p.posted) != 1 || strings.Contains(p.posted[0], "From:") {
		t.Fatalf("plain DM expected, no permalink: %v", p.posted)
	}
}

// dmByID serves the posting sandbox's routing info for the allowEmails override.
type dmByID struct {
	infos map[string]bridge.SandboxRoutingInfo
	err   error
}

func (f *dmByID) FetchBySandboxID(_ context.Context, id string) (bridge.SandboxRoutingInfo, error) {
	return f.infos[id], f.err
}

func dmOverrideHandler(t *testing.T, rowList []string, fetchErr error) (*bridge.Handler, ed25519.PrivateKey) {
	t.Helper()
	h, _, _, priv := dmHandler(t)
	h.DefaultRoute = bridge.NewDefaultRoute("ir-bot", "profiles/ir-bot.yaml", []string{"*@example.com"}, nil)
	h.SenderEmails = &dmEmails{byUser: map[string]string{
		"U0KURT": "kurt@example.com", "U0BOB": "bob@example.com", "U0Z": "z@other.example",
	}}
	h.SandboxByID = &dmByID{
		infos: map[string]bridge.SandboxRoutingInfo{"sb-abc123": {SandboxID: "sb-abc123", AllowEmails: rowList}},
		err:   fetchErr,
	}
	return h, priv
}

func TestHandler_DM_SandboxAllowEmails_ReplacesRouteList(t *testing.T) {
	h, priv := dmOverrideHandler(t, []string{"kurt@example.com"}, nil)
	resp := h.Handle(context.Background(), signRequest(t, dmEnv("U0BOB"), priv))
	if resp.StatusCode != 403 || responseBody(t, resp)["error"] != "dm_not_allowed" {
		t.Fatalf("bob is on the route list but not the sandbox's: want 403 dm_not_allowed, got %d %s", resp.StatusCode, resp.Body)
	}
	h, priv = dmOverrideHandler(t, []string{"kurt@example.com"}, nil)
	if resp := h.Handle(context.Background(), signRequest(t, dmEnv("U0KURT"), priv)); resp.StatusCode != 200 {
		t.Fatalf("kurt is on the sandbox list: want 200, got %d %s", resp.StatusCode, resp.Body)
	}
	h, priv = dmOverrideHandler(t, []string{"z@other.example"}, nil)
	if resp := h.Handle(context.Background(), signRequest(t, dmEnv("U0Z"), priv)); resp.StatusCode != 200 {
		t.Fatalf("the sandbox list replaces the route list: want 200, got %d %s", resp.StatusCode, resp.Body)
	}
}

func TestHandler_DM_NoSandboxList_RouteRuleApplies(t *testing.T) {
	h, priv := dmOverrideHandler(t, nil, nil)
	if resp := h.Handle(context.Background(), signRequest(t, dmEnv("U0BOB"), priv)); resp.StatusCode != 200 {
		t.Fatalf("no sandbox list: the route's *@example.com admits bob; got %d %s", resp.StatusCode, resp.Body)
	}
	h, priv = dmOverrideHandler(t, nil, nil)
	if resp := h.Handle(context.Background(), signRequest(t, dmEnv("U0Z"), priv)); resp.StatusCode != 403 {
		t.Fatalf("no sandbox list: z@other.example is off the route list; got %d %s", resp.StatusCode, resp.Body)
	}
}

func TestHandler_DM_SandboxFetchError_Denies(t *testing.T) {
	h, priv := dmOverrideHandler(t, []string{"kurt@example.com"}, errors.New("ddb blip"))
	if resp := h.Handle(context.Background(), signRequest(t, dmEnv("U0BOB"), priv)); resp.StatusCode != 403 {
		t.Fatalf("fetch error must fail closed even for a route-listed target; got %d %s", resp.StatusCode, resp.Body)
	}
}
