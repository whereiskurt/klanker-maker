package bridge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

const v1Body = `{"km_schema":"v1","source":"wiz","delivery_key":"iss-1:CREATED:t0",
"type":"issue","id":"iss-1","severity":"CRITICAL","status":"OPEN","title":"Public bucket",
"entity":{"type":"BUCKET","name":"logs","cloud_id":"arn:1"},"url":"https://app.wiz.io/x"}`

type stubs struct {
	secret     string
	sandboxID  string
	status     string
	resolveErr error
	resumeErr  error

	enqueued   []string
	resumed    []string
	created    []string
	rowsKilled []string

	// order records the relative sequence of DeleteSandboxRow / ColdCreate calls
	// so the self-heal ordering (delete BEFORE cold-create) is actually pinned,
	// not just inferred from both having happened.
	order []string
}

func (s *stubs) Fetch(_ context.Context, _ string) (string, error) { return s.secret, nil }
func (s *stubs) ResolveByAliasWithStatus(_ context.Context, _ string) (string, string, error) {
	return s.sandboxID, s.status, s.resolveErr
}
func (s *stubs) QueueURL(_ context.Context, _ string) (string, error) { return "https://q", nil }
func (s *stubs) Send(_ context.Context, _, groupID, body string) error {
	s.enqueued = append(s.enqueued, groupID+"|"+body)
	return nil
}
func (s *stubs) StartSandbox(_ context.Context, id string) error {
	s.resumed = append(s.resumed, id)
	return s.resumeErr
}
func (s *stubs) DeleteSandboxRow(_ context.Context, id string) error {
	s.rowsKilled = append(s.rowsKilled, id)
	s.order = append(s.order, "delete:"+id)
	return nil
}
func (s *stubs) ColdCreate(_ context.Context, alias, _, _ string) error {
	s.created = append(s.created, alias)
	s.order = append(s.order, "create:"+alias)
	return nil
}

func newHandler(t *testing.T, s *stubs) *Handler {
	t.Helper()
	return &Handler{
		Sources: []config.WebhookSource{{
			Name: "wiz",
			Auth: config.WebhookAuth{Type: "bearer", Header: "Authorization", SecretPath: "/p"},
			Rules: []config.WebhookRule{{
				Match:    map[string][]string{"severity": {"CRITICAL"}},
				Alias:    "ir-bot",
				Profile:  "ir-bot",
				OnAbsent: "cold-create",
				GroupBy:  "{{entity.cloud_id}}",
				Prompt:   "Triage {{title}} on {{entity.name}}",
			}},
		}},
		Secrets:  s,
		Resolver: s,
		Queue:    s,
		Resumer:  s,
		Status:   s,
		Cold:     s,
		Nonces:   &fakeNonce{},
		Rates:    &fakeRate{},
		Now:      func() int64 { return 1000 },
	}
}

func authedReq() Request {
	return Request{
		Path:    "/wiz",
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    []byte(v1Body),
	}
}

func TestHandle_WarmEnqueue(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "running"}
	resp := newHandler(t, s).Handle(context.Background(), authedReq())

	if resp.Status != 200 {
		t.Fatalf("status: got %d, want 200", resp.Status)
	}
	if len(s.enqueued) != 1 {
		t.Fatalf("enqueued: got %d, want 1", len(s.enqueued))
	}
	// MessageGroupId is the sandbox id => fully serial per box.
	if !strings.HasPrefix(s.enqueued[0], "sb-1|") {
		t.Errorf("MessageGroupId must be the sandbox id: %q", s.enqueued[0])
	}
	if !strings.Contains(s.enqueued[0], "Triage Public bucket on logs") {
		t.Errorf("prompt not expanded: %q", s.enqueued[0])
	}
	if len(s.created) != 0 {
		t.Errorf("must not cold-create when warm: %v", s.created)
	}
}

func TestHandle_StoppedResumesThenEnqueues(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "stopped"}
	newHandler(t, s).Handle(context.Background(), authedReq())

	if len(s.resumed) != 1 || s.resumed[0] != "sb-1" {
		t.Errorf("resumed: %v", s.resumed)
	}
	if len(s.enqueued) != 1 {
		t.Errorf("must still enqueue after resume: %v", s.enqueued)
	}
}

func TestHandle_AbsentAliasColdCreates(t *testing.T) {
	s := &stubs{secret: "tok", resolveErr: errors.New("not found")}
	newHandler(t, s).Handle(context.Background(), authedReq())

	if len(s.created) != 1 || s.created[0] != "ir-bot" {
		t.Errorf("created: %v", s.created)
	}
	if len(s.enqueued) != 0 {
		t.Errorf("cold path must not enqueue: %v", s.enqueued)
	}
}

// Phase 109 self-heal: a terminal resume failure means the row is orphaned.
// Delete it, then cold-create — otherwise the stale row shadows creation forever.
func TestHandle_TerminalResumeFailureSelfHeals(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "stopped",
		resumeErr: ErrNoResumableInstance}
	newHandler(t, s).Handle(context.Background(), authedReq())

	if len(s.rowsKilled) != 1 || s.rowsKilled[0] != "sb-1" {
		t.Errorf("stale row must be deleted: %v", s.rowsKilled)
	}
	if len(s.created) != 1 {
		t.Errorf("must fall through to cold-create: %v", s.created)
	}
	if len(s.enqueued) != 0 {
		t.Errorf("must not enqueue to a dead box: %v", s.enqueued)
	}
	// Pin the ORDER, not just that both happened: a handler that cold-creates
	// first and deletes the row second would leak a race where a re-delivered
	// event resolves the still-present stale row before the delete lands.
	wantOrder := []string{"delete:sb-1", "create:ir-bot"}
	if len(s.order) != len(wantOrder) {
		t.Fatalf("order: got %v, want %v", s.order, wantOrder)
	}
	for i, want := range wantOrder {
		if s.order[i] != want {
			t.Errorf("order[%d]: got %q, want %q (full: %v)", i, s.order[i], want, s.order)
		}
	}
}

func TestHandle_OnAbsentSkip(t *testing.T) {
	s := &stubs{secret: "tok", resolveErr: errors.New("not found")}
	h := newHandler(t, s)
	h.Sources[0].Rules[0].OnAbsent = "skip"
	h.Handle(context.Background(), authedReq())

	if len(s.created) != 0 {
		t.Errorf("on_absent: skip must not cold-create: %v", s.created)
	}
}

func TestHandle_BadAuthDropsWith200(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "running"}
	req := authedReq()
	req.Headers["authorization"] = "Bearer wrong"

	resp := newHandler(t, s).Handle(context.Background(), req)
	if resp.Status != 200 {
		t.Errorf("must still return 200: got %d", resp.Status)
	}
	if len(s.enqueued) != 0 {
		t.Errorf("unauthorized must not dispatch: %v", s.enqueued)
	}
	// Distinguish "dropped for the right reason" from "dropped for any reason" —
	// a handler that e.g. dropped on a bug in cooldown/rate gating would also
	// satisfy the two checks above.
	if resp.Log != "unauthorized" {
		t.Errorf("Log: got %q, want %q", resp.Log, "unauthorized")
	}
}

func TestHandle_UnknownSourcePathDrops(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "running"}
	req := authedReq()
	req.Path = "/nope"

	resp := newHandler(t, s).Handle(context.Background(), req)
	if resp.Status != 200 || len(s.enqueued) != 0 {
		t.Errorf("unknown source must drop: status=%d enqueued=%v", resp.Status, s.enqueued)
	}
	if resp.Log != "unknown_source" {
		t.Errorf("Log: got %q, want %q", resp.Log, "unknown_source")
	}
}

func TestHandle_ReplayDropped(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "running"}
	h := newHandler(t, s)
	first := h.Handle(context.Background(), authedReq())
	second := h.Handle(context.Background(), authedReq())

	if len(s.enqueued) != 1 {
		t.Errorf("replay must be dropped: %d dispatches", len(s.enqueued))
	}
	if first.Log != "dispatched" {
		t.Errorf("first delivery Log: got %q, want %q", first.Log, "dispatched")
	}
	// Distinguish "dropped for replay" from "dropped for any reason" — e.g. a
	// handler that accidentally re-matched no rule on the second call would
	// also satisfy the enqueue-count check above.
	if second.Log != "replay" {
		t.Errorf("second delivery Log: got %q, want %q", second.Log, "replay")
	}
}

func TestHandle_UnparseableReturns200NoDispatch(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "running"}
	req := authedReq()
	req.Body = []byte(`{not json`)

	resp := newHandler(t, s).Handle(context.Background(), req)
	if resp.Status != 200 {
		t.Errorf("status: got %d, want 200", resp.Status)
	}
	if len(s.enqueued) != 0 {
		t.Errorf("must not dispatch: %v", s.enqueued)
	}
	if resp.Log != "unparseable" {
		t.Errorf("Log: got %q, want %q", resp.Log, "unparseable")
	}
}

func TestHandle_NoMatchingRuleDrops(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "running"}
	h := newHandler(t, s)
	h.Sources[0].Rules[0].Match = map[string][]string{"severity": {"LOW"}}
	resp := h.Handle(context.Background(), authedReq())

	if len(s.enqueued) != 0 {
		t.Errorf("non-matching payload must not dispatch: %v", s.enqueued)
	}
	if resp.Log != "no_match" {
		t.Errorf("Log: got %q, want %q", resp.Log, "no_match")
	}
}

type fakeNonce struct{ seen map[string]bool }

func (f *fakeNonce) CheckAndStore(_ context.Context, key string, _ int) (bool, error) {
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[key] {
		return true, nil
	}
	f.seen[key] = true
	return false, nil
}

type fakeRate struct{ n int64 }

func (f *fakeRate) Increment(_ context.Context, _ string, _ int) (int64, error) {
	f.n++
	return f.n, nil
}
