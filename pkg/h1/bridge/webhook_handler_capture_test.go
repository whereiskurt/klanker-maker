package bridge_test

// Tests for the step-0 raw capture (2026-09-18 report_created→Slack design).
// Capture must run BEFORE signature verification (so a 401 is still captured),
// must receive the verbatim headers + body, and must never change the response.

import (
	"context"
	"sync"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

type captureCall struct {
	guid    string
	headers map[string]string
	body    []byte
}

type fakeCapturer struct {
	mu    sync.Mutex
	calls []captureCall
	err   error
}

func (c *fakeCapturer) Capture(_ context.Context, guid string, headers map[string]string, body []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, captureCall{guid, headers, body})
	return c.err
}

func TestHandle_Capture_RunsBeforeVerifyAndOn401(t *testing.T) {
	fakes := newFakes()
	cap := &fakeCapturer{}
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, nil), fakes)
	h.Capture = cap

	body := h1Body("km-sandbox", "400", "alice", "@km hi", false)
	req := newRequest(body, "report_comment_created", "g-401")
	req.Headers["x-h1-signature"] = "sha256=deadbeef" // wrong ⇒ 401

	r := h.Handle(context.Background(), req)
	if r.StatusCode != 401 {
		t.Fatalf("status=%d; want 401 (bad signature)", r.StatusCode)
	}
	if len(cap.calls) != 1 {
		t.Fatalf("capture must run even when the signature is rejected; calls=%d", len(cap.calls))
	}
	got := cap.calls[0]
	if got.guid != "g-401" {
		t.Errorf("guid=%q; want g-401", got.guid)
	}
	if string(got.body) != string(body) {
		t.Errorf("captured body must be the verbatim raw body")
	}
	if got.headers["x-h1-event"] != "report_comment_created" {
		t.Errorf("captured headers must be the request headers; got %+v", got.headers)
	}
	if len(fakes.sqs.sends) != 0 {
		t.Errorf("a 401 must not dispatch")
	}
}

func TestHandle_Capture_ErrorIsSoft(t *testing.T) {
	fakes := newFakes()
	cap := &fakeCapturer{err: errString("s3 down")}
	events := map[string]bridge.EventEntry{"report_created": {Prompt: "x"}}
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, events), fakes)
	h.Capture = cap

	body := h1Body("km-sandbox", "401", "reporter", "", false)
	r := h.Handle(context.Background(), newRequest(body, "report_created", "g-soft"))
	if r.StatusCode != 200 {
		t.Fatalf("status=%d; want 200 (capture failure is non-fatal)", r.StatusCode)
	}
	if len(fakes.sqs.sends) != 1 {
		t.Errorf("capture failure must not block dispatch; sends=%d", len(fakes.sqs.sends))
	}
}

func TestHandle_Capture_NilIsDormant(t *testing.T) {
	fakes := newFakes()
	events := map[string]bridge.EventEntry{"report_created": {Prompt: "x"}}
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, events), fakes) // Capture nil
	body := h1Body("km-sandbox", "402", "reporter", "", false)
	r := h.Handle(context.Background(), newRequest(body, "report_created", "g-nil"))
	if r.StatusCode != 200 || len(fakes.sqs.sends) != 1 {
		t.Errorf("nil Capture must be a no-op: status=%d sends=%d", r.StatusCode, len(fakes.sqs.sends))
	}
}
