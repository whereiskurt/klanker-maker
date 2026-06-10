// webhook_handler_test.go — Phase 103 Plan 04 tests for the H1 Handle() flow.
//
// Ports pkg/github/bridge/webhook_handler_test.go's injection pattern and adds the
// HackerOne-specific cases: the two-trigger gate (auto-triage event-gate +
// @handle comment-keyword), the loop guard (actor == api_username), the authz
// asymmetry (comment-keyword gates on allow; auto-triage does NOT), multi-target
// fanout (N distinct dedupIDs + N thread rows), the 3-way dispatch, the
// internal-error→200 rule, and the internal-only synchronous ACK.
//
// The reply-gate (safety-critical) tests live in webhook_handler_replygate_test.go.
package bridge_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

// hmacSig computes the X-H1-Signature value (sha256=<hex>) for a body.
func hmacSig(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// ============================================================
// Shared fakes for the Handle() flow
// ============================================================

type fakeSecret struct {
	secret string
	err    error
}

func (f *fakeSecret) Fetch(_ context.Context) (string, error) { return f.secret, f.err }

type fakeNonce struct {
	mu       sync.Mutex
	seen     map[string]bool
	forceErr error
	calls    int
}

func (f *fakeNonce) CheckAndStore(_ context.Context, key string, _ int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.forceErr != nil {
		return false, f.forceErr
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[key] {
		return true, nil
	}
	f.seen[key] = true
	return false, nil
}

// fakeResolver implements SandboxAliasResolverWithStatus. statuses maps alias→status;
// missing maps alias→true to force a not-found (cold) result.
type fakeResolver struct {
	statuses map[string]string // alias → status ("running"/"stopped"/"paused")
	missing  map[string]bool   // alias → true ⇒ ResolveByAlias returns error (cold)
	queueErr error
}

func (r *fakeResolver) ResolveByAlias(ctx context.Context, alias string) (string, error) {
	id, _, err := r.ResolveByAliasWithStatus(ctx, alias)
	return id, err
}

func (r *fakeResolver) ResolveByAliasWithStatus(_ context.Context, alias string) (string, string, error) {
	if r.missing[alias] {
		return "", "", errString("alias not found")
	}
	status := r.statuses[alias]
	if status == "" {
		status = "running"
	}
	return "sb-" + alias, status, nil
}

func (r *fakeResolver) H1QueueURL(_ context.Context, sandboxID string) (string, error) {
	if r.queueErr != nil {
		return "", r.queueErr
	}
	return "https://sqs/" + sandboxID + ".fifo", nil
}

type errString string

func (e errString) Error() string { return string(e) }

type fakeResumer struct {
	mu      sync.Mutex
	started []string
	err     error
}

func (r *fakeResumer) StartSandbox(_ context.Context, sandboxID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = append(r.started, sandboxID)
	return r.err
}

type fakePublisher struct {
	mu     sync.Mutex
	calls  []struct{ alias, profile, env string }
	err    error
}

func (p *fakePublisher) PutSandboxCreate(_ context.Context, alias, profile, env string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, struct{ alias, profile, env string }{alias, profile, env})
	return p.err
}

type sqsCall struct {
	queueURL, body, groupID, dedupID string
}

type fakeSQS struct {
	mu    sync.Mutex
	sends []sqsCall
	err   error
}

func (s *fakeSQS) Send(_ context.Context, queueURL, body, groupID, dedupID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sends = append(s.sends, sqsCall{queueURL, body, groupID, dedupID})
	return s.err
}

type threadRow struct{ reportID, target, sandboxID string }

type fakeThreadStore struct {
	mu       sync.Mutex
	known    map[string]map[string]string // reportID → target → sandboxID (pre-seeded "known" rows)
	upserts  []threadRow
	lookErr  error
}

func (t *fakeThreadStore) LookupSandbox(_ context.Context, reportID, target string) (string, string, string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lookErr != nil {
		return "", "", "", t.lookErr
	}
	if m, ok := t.known[reportID]; ok {
		if sid, ok := m[target]; ok {
			return sid, "", "", nil
		}
		// any-target-known: return the first row for the report (drives the bypass)
		for _, sid := range m {
			return sid, "", "", nil
		}
	}
	return "", "", "", nil
}

func (t *fakeThreadStore) Upsert(_ context.Context, reportID, target, sandboxID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.upserts = append(t.upserts, threadRow{reportID, target, sandboxID})
	return nil
}

func (t *fakeThreadStore) UpdateSession(_ context.Context, _, _, _, _ string) error { return nil }

func (t *fakeThreadStore) InvalidateStaleSession(_ context.Context, _, _, _ string) error {
	return nil
}

type ackCall struct {
	reportID string
	body     string
	internal bool
}

type fakeCommenter struct {
	mu    sync.Mutex
	posts []ackCall
	err   error
}

func (c *fakeCommenter) PostComment(_ context.Context, reportID, body string, internal bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.posts = append(c.posts, ackCall{reportID, body, internal})
	return c.err
}

// ============================================================
// Test payload + request builders
// ============================================================

// h1Body builds a synthetic HackerOne webhook body for the given program handle,
// report id, actor username, comment message, and internal flag.
func h1Body(program, reportID, actor, message string, internal bool) []byte {
	payload := map[string]any{
		"data": map[string]any{
			"activity": map[string]any{
				"id":   "act-1",
				"type": "activity-comment",
				"attributes": map[string]any{
					"message":  message,
					"internal": internal,
				},
				"relationships": map[string]any{
					"actor": map[string]any{
						"data": map[string]any{
							"attributes": map[string]any{"username": actor},
						},
					},
				},
			},
			"report": map[string]any{
				"id":         reportID,
				"attributes": map[string]any{"title": "Test report", "state": "new"},
				"relationships": map[string]any{
					"program": map[string]any{
						"data": map[string]any{
							"attributes": map[string]any{"handle": program},
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(payload)
	return b
}

// newRequest wraps a body + event + delivery GUID in a WebhookRequest. The
// signature is computed so VerifyH1Signature passes for secret "s3cr3t".
func newRequest(body []byte, event, guid string) bridge.WebhookRequest {
	return bridge.WebhookRequest{
		Headers: map[string]string{
			"x-h1-event":     event,
			"x-h1-delivery":  guid,
			"x-h1-signature": hmacSig("s3cr3t", body),
		},
		RawBody: body,
		Body:    string(body),
	}
}

// baseHandler builds a handler wired with the common fakes and a program config.
func baseHandler(programs []bridge.ProgramEntry, fakes *handlerFakes) *bridge.WebhookHandler {
	return &bridge.WebhookHandler{
		Secret:         fakes.secret,
		APIUsername:    "km-bot", // loop-guard identity
		Nonces:         fakes.nonce,
		Resolver:       fakes.resolver,
		Resumer:        fakes.resumer,
		Publisher:      fakes.publisher,
		SQS:            fakes.sqs,
		Threads:        fakes.threads,
		Commenter:      fakes.commenter,
		Entries:        programs,
		DefaultProfile: "h1-review",
		BotHandle:      "@km",
	}
}

type handlerFakes struct {
	secret    *fakeSecret
	nonce     *fakeNonce
	resolver  *fakeResolver
	resumer   *fakeResumer
	publisher *fakePublisher
	sqs       *fakeSQS
	threads   *fakeThreadStore
	commenter *fakeCommenter
}

func newFakes() *handlerFakes {
	return &handlerFakes{
		secret:    &fakeSecret{secret: "s3cr3t"},
		nonce:     &fakeNonce{},
		resolver:  &fakeResolver{statuses: map[string]string{}, missing: map[string]bool{}},
		resumer:   &fakeResumer{},
		publisher: &fakePublisher{},
		sqs:       &fakeSQS{},
		threads:   &fakeThreadStore{known: map[string]map[string]string{}},
		commenter: &fakeCommenter{},
	}
}

// singleProgram returns a one-target program with the given handle + allowlist + events.
func singleProgram(handle string, allow []string, events map[string]bridge.EventEntry) []bridge.ProgramEntry {
	return []bridge.ProgramEntry{{
		Handle:  handle,
		Targets: []bridge.Target{{Alias: "h1-" + handle, Profile: "h1-review"}},
		Allow:   allow,
		Events:  events,
	}}
}

// ============================================================
// TestHandle_Dedup
// ============================================================

func TestHandle_Dedup(t *testing.T) {
	fakes := newFakes()
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, nil), fakes)
	body := h1Body("km-sandbox", "100", "alice", "@km please triage", false)
	req := newRequest(body, "report_comment_created", "guid-dup")

	r1 := h.Handle(context.Background(), req)
	if r1.StatusCode != 200 {
		t.Fatalf("first call status=%d; want 200", r1.StatusCode)
	}
	sendsAfterFirst := len(fakes.sqs.sends)
	if sendsAfterFirst == 0 {
		t.Fatal("first call must dispatch at least once")
	}

	r2 := h.Handle(context.Background(), req) // same GUID → replay
	if r2.StatusCode != 200 {
		t.Fatalf("replay status=%d; want 200", r2.StatusCode)
	}
	if len(fakes.sqs.sends) != sendsAfterFirst {
		t.Errorf("replay dispatched again: sends=%d want %d (no new dispatch)", len(fakes.sqs.sends), sendsAfterFirst)
	}
}

// ============================================================
// TestHandle_AutoTriage
// ============================================================

func TestHandle_AutoTriage(t *testing.T) {
	events := map[string]bridge.EventEntry{
		"report_created": {Prompt: "Triage report {{report_id}}"},
	}
	// listed event → dispatch
	fakes := newFakes()
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, events), fakes)
	body := h1Body("km-sandbox", "200", "external-reporter", "", false)
	r := h.Handle(context.Background(), newRequest(body, "report_created", "g1"))
	if r.StatusCode != 200 {
		t.Fatalf("status=%d; want 200", r.StatusCode)
	}
	if len(fakes.sqs.sends) != 1 {
		t.Fatalf("listed event must dispatch once; sends=%d", len(fakes.sqs.sends))
	}
	// The dispatched body must carry the expanded event prompt (report_id substituted).
	var env bridge.H1Envelope
	_ = json.Unmarshal([]byte(fakes.sqs.sends[0].body), &env)
	if !strings.Contains(env.Body, "200") {
		t.Errorf("dispatched body %q must contain expanded report_id 200", env.Body)
	}

	// unlisted event → drop
	fakes2 := newFakes()
	h2 := baseHandler(singleProgram("km-sandbox", []string{"alice"}, events), fakes2)
	body2 := h1Body("km-sandbox", "201", "alice", "", false)
	r2 := h2.Handle(context.Background(), newRequest(body2, "report_unlisted_event", "g2"))
	if r2.StatusCode != 200 {
		t.Fatalf("unlisted event status=%d; want 200", r2.StatusCode)
	}
	if len(fakes2.sqs.sends) != 0 {
		t.Errorf("unlisted event must NOT dispatch; sends=%d", len(fakes2.sqs.sends))
	}

	// empty events → auto-triage dormant
	fakes3 := newFakes()
	h3 := baseHandler(singleProgram("km-sandbox", []string{"alice"}, nil), fakes3)
	body3 := h1Body("km-sandbox", "202", "alice", "", false)
	r3 := h3.Handle(context.Background(), newRequest(body3, "report_created", "g3"))
	if r3.StatusCode != 200 {
		t.Fatalf("dormant status=%d; want 200", r3.StatusCode)
	}
	if len(fakes3.sqs.sends) != 0 {
		t.Errorf("empty events ⇒ auto-triage dormant; sends=%d", len(fakes3.sqs.sends))
	}
}

// ============================================================
// TestHandle_Mention
// ============================================================

func TestHandle_Mention(t *testing.T) {
	// with @handle → dispatch
	fakes := newFakes()
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, nil), fakes)
	body := h1Body("km-sandbox", "300", "alice", "@km look at this", false)
	r := h.Handle(context.Background(), newRequest(body, "report_comment_created", "m1"))
	if r.StatusCode != 200 || len(fakes.sqs.sends) != 1 {
		t.Fatalf("@handle must dispatch: status=%d sends=%d", r.StatusCode, len(fakes.sqs.sends))
	}

	// without @handle + unknown thread → drop
	fakes2 := newFakes()
	h2 := baseHandler(singleProgram("km-sandbox", []string{"alice"}, nil), fakes2)
	body2 := h1Body("km-sandbox", "301", "alice", "just a normal comment", false)
	r2 := h2.Handle(context.Background(), newRequest(body2, "report_comment_created", "m2"))
	if r2.StatusCode != 200 || len(fakes2.sqs.sends) != 0 {
		t.Fatalf("no @handle + unknown thread must drop: status=%d sends=%d", r2.StatusCode, len(fakes2.sqs.sends))
	}

	// without @handle but KNOWN thread → dispatch (bypass)
	fakes3 := newFakes()
	fakes3.threads.known["302"] = map[string]string{"h1-km-sandbox": "sb-h1-km-sandbox"}
	h3 := baseHandler(singleProgram("km-sandbox", []string{"alice"}, nil), fakes3)
	body3 := h1Body("km-sandbox", "302", "alice", "follow-up without handle", false)
	r3 := h3.Handle(context.Background(), newRequest(body3, "report_comment_created", "m3"))
	if r3.StatusCode != 200 || len(fakes3.sqs.sends) != 1 {
		t.Fatalf("known thread must bypass handle: status=%d sends=%d", r3.StatusCode, len(fakes3.sqs.sends))
	}
}

// ============================================================
// TestHandle_LoopGuard
// ============================================================

func TestHandle_LoopGuard(t *testing.T) {
	fakes := newFakes()
	h := baseHandler(singleProgram("km-sandbox", []string{"km-bot"}, nil), fakes)
	// actor == APIUsername ("km-bot") — the bot's own internal comment.
	body := h1Body("km-sandbox", "400", "km-bot", "@km on it", false)
	r := h.Handle(context.Background(), newRequest(body, "report_comment_created", "lg1"))
	if r.StatusCode != 200 {
		t.Fatalf("status=%d; want 200", r.StatusCode)
	}
	if len(fakes.sqs.sends) != 0 {
		t.Errorf("loop guard must drop the bot's own comment; sends=%d", len(fakes.sqs.sends))
	}
}

// ============================================================
// TestHandle_Authz
// ============================================================

func TestHandle_Authz(t *testing.T) {
	// comment-keyword by actor NOT in allow → silent drop
	fakes := newFakes()
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, nil), fakes)
	body := h1Body("km-sandbox", "500", "mallory", "@km do the thing", false)
	r := h.Handle(context.Background(), newRequest(body, "report_comment_created", "az1"))
	if r.StatusCode != 200 || len(fakes.sqs.sends) != 0 {
		t.Fatalf("non-allowlisted comment must silent-drop: status=%d sends=%d", r.StatusCode, len(fakes.sqs.sends))
	}

	// auto-triage event by external reporter NOT in allow → STILL dispatches (OQ3)
	events := map[string]bridge.EventEntry{"report_created": {Prompt: "triage"}}
	fakes2 := newFakes()
	h2 := baseHandler(singleProgram("km-sandbox", []string{"alice"}, events), fakes2)
	body2 := h1Body("km-sandbox", "501", "external-reporter", "", false)
	r2 := h2.Handle(context.Background(), newRequest(body2, "report_created", "az2"))
	if r2.StatusCode != 200 || len(fakes2.sqs.sends) != 1 {
		t.Fatalf("auto-triage must NOT gate on allow: status=%d sends=%d", r2.StatusCode, len(fakes2.sqs.sends))
	}
}

// ============================================================
// TestHandle_Fanout
// ============================================================

func TestHandle_Fanout(t *testing.T) {
	programs := []bridge.ProgramEntry{{
		Handle: "km-sandbox",
		Targets: []bridge.Target{
			{Alias: "h1-prog-a", Profile: "h1-review"},
			{Alias: "h1-prog-b", Profile: "h1-deep"},
		},
		Allow: []string{"alice"},
	}}
	fakes := newFakes()
	h := baseHandler(programs, fakes)
	body := h1Body("km-sandbox", "600", "alice", "@km triage", false)
	r := h.Handle(context.Background(), newRequest(body, "report_comment_created", "fo1"))
	if r.StatusCode != 200 {
		t.Fatalf("status=%d; want 200", r.StatusCode)
	}
	if len(fakes.sqs.sends) != 2 {
		t.Fatalf("2 targets must produce 2 enqueues; sends=%d", len(fakes.sqs.sends))
	}
	// Distinct dedupIDs.
	if fakes.sqs.sends[0].dedupID == fakes.sqs.sends[1].dedupID {
		t.Errorf("fanout dedupIDs must differ; both=%q", fakes.sqs.sends[0].dedupID)
	}
	// dedupID must include the target alias.
	if !strings.Contains(fakes.sqs.sends[0].dedupID, "h1-prog-a") &&
		!strings.Contains(fakes.sqs.sends[1].dedupID, "h1-prog-a") {
		t.Errorf("a dedupID must include target alias h1-prog-a; got %q, %q",
			fakes.sqs.sends[0].dedupID, fakes.sqs.sends[1].dedupID)
	}
	// 2 thread rows, distinct targets.
	if len(fakes.threads.upserts) != 2 {
		t.Fatalf("2 targets must produce 2 thread rows; upserts=%d", len(fakes.threads.upserts))
	}
	if fakes.threads.upserts[0].target == fakes.threads.upserts[1].target {
		t.Errorf("thread rows must have distinct targets; both=%q", fakes.threads.upserts[0].target)
	}
}

// ============================================================
// TestHandle_Dispatch — warm / cold / resume
// ============================================================

func TestHandle_Dispatch(t *testing.T) {
	prog := singleProgram("km-sandbox", []string{"alice"}, nil)

	// warm (running) → SQS enqueue, no publish, no resume
	fakes := newFakes()
	fakes.resolver.statuses["h1-km-sandbox"] = "running"
	h := baseHandler(prog, fakes)
	body := h1Body("km-sandbox", "700", "alice", "@km go", false)
	h.Handle(context.Background(), newRequest(body, "report_comment_created", "d1"))
	if len(fakes.sqs.sends) != 1 || len(fakes.publisher.calls) != 0 || len(fakes.resumer.started) != 0 {
		t.Fatalf("warm: sends=%d publish=%d resume=%d", len(fakes.sqs.sends), len(fakes.publisher.calls), len(fakes.resumer.started))
	}

	// cold (alias missing) → EventBridge create, no SQS
	fakes2 := newFakes()
	fakes2.resolver.missing["h1-km-sandbox"] = true
	h2 := baseHandler(prog, fakes2)
	body2 := h1Body("km-sandbox", "701", "alice", "@km go", false)
	h2.Handle(context.Background(), newRequest(body2, "report_comment_created", "d2"))
	if len(fakes2.publisher.calls) != 1 || len(fakes2.sqs.sends) != 0 {
		t.Fatalf("cold: publish=%d sends=%d", len(fakes2.publisher.calls), len(fakes2.sqs.sends))
	}

	// resume (stopped) → StartSandbox then SQS enqueue
	fakes3 := newFakes()
	fakes3.resolver.statuses["h1-km-sandbox"] = "stopped"
	h3 := baseHandler(prog, fakes3)
	body3 := h1Body("km-sandbox", "702", "alice", "@km go", false)
	h3.Handle(context.Background(), newRequest(body3, "report_comment_created", "d3"))
	if len(fakes3.resumer.started) != 1 || len(fakes3.sqs.sends) != 1 {
		t.Fatalf("resume: started=%d sends=%d", len(fakes3.resumer.started), len(fakes3.sqs.sends))
	}
}

// ============================================================
// TestHandle_InternalError200
// ============================================================

func TestHandle_InternalError200(t *testing.T) {
	fakes := newFakes()
	fakes.sqs.err = errString("SQS down")
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, nil), fakes)
	body := h1Body("km-sandbox", "800", "alice", "@km go", false)
	r := h.Handle(context.Background(), newRequest(body, "report_comment_created", "ie1"))
	if r.StatusCode != 200 {
		t.Errorf("internal SQS error must still return 200; got %d", r.StatusCode)
	}

	// nonce store error → 200, no dispatch
	fakes2 := newFakes()
	fakes2.nonce.forceErr = errString("DDB down")
	h2 := baseHandler(singleProgram("km-sandbox", []string{"alice"}, nil), fakes2)
	body2 := h1Body("km-sandbox", "801", "alice", "@km go", false)
	r2 := h2.Handle(context.Background(), newRequest(body2, "report_comment_created", "ie2"))
	if r2.StatusCode != 200 {
		t.Errorf("nonce error must still return 200; got %d", r2.StatusCode)
	}
}

// ============================================================
// TestHandle_ACK — exactly one INTERNAL ack comment
// ============================================================

func TestHandle_ACK(t *testing.T) {
	fakes := newFakes()
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, nil), fakes)
	body := h1Body("km-sandbox", "900", "alice", "@km go", false)
	h.Handle(context.Background(), newRequest(body, "report_comment_created", "ack1"))
	if len(fakes.commenter.posts) != 1 {
		t.Fatalf("expected exactly 1 ack comment; got %d", len(fakes.commenter.posts))
	}
	if !fakes.commenter.posts[0].internal {
		t.Errorf("the ack comment MUST be internal:true; got internal=%v", fakes.commenter.posts[0].internal)
	}
	if fakes.commenter.posts[0].reportID != "900" {
		t.Errorf("ack reportID=%q; want 900", fakes.commenter.posts[0].reportID)
	}
}

// ============================================================
// Signature bad path
// ============================================================

func TestHandle_BadSignature(t *testing.T) {
	fakes := newFakes()
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, nil), fakes)
	body := h1Body("km-sandbox", "950", "alice", "@km go", false)
	req := newRequest(body, "report_comment_created", "bs1")
	req.Headers["x-h1-signature"] = "sha256=deadbeef" // wrong
	r := h.Handle(context.Background(), req)
	if r.StatusCode != 401 {
		t.Errorf("bad signature must return 401; got %d", r.StatusCode)
	}
}
