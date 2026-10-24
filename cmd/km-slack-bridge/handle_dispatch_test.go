package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// dispatchStore is a HistoryStore stub; only the meta methods matter here.
type dispatchStore struct {
	meta       *bridge.ChannelMeta
	getMetaErr error
	putMeta    []bridge.ChannelMeta
}

func (s *dispatchStore) PutMessage(context.Context, bridge.HistoryMessageRow) error { return nil }
func (s *dispatchStore) UpdateText(context.Context, string, string, string, string) error {
	return nil
}
func (s *dispatchStore) DeleteMessage(context.Context, string, string) error { return nil }
func (s *dispatchStore) Query(context.Context, string, string, string, string, int) ([]bridge.HistoryMessageRow, bool, error) {
	return nil, false, nil
}
func (s *dispatchStore) PutMeta(_ context.Context, m bridge.ChannelMeta) error {
	s.putMeta = append(s.putMeta, m)
	s.meta = &m
	return nil
}
func (s *dispatchStore) GetMeta(context.Context, string) (*bridge.ChannelMeta, error) {
	return s.meta, s.getMetaErr
}
func (s *dispatchStore) ListMeta(context.Context) ([]bridge.ChannelMeta, error) { return nil, nil }
func (s *dispatchStore) PurgeChannel(context.Context, string) (int, error)      { return 0, nil }

type dispatchReader struct{}

func (dispatchReader) History(context.Context, string, string, string, int) ([]bridge.SlackReplyMessage, bool, error) {
	return nil, false, nil
}

func withEventsHandler(t *testing.T, st *dispatchStore) {
	t.Helper()
	orig := eventsHandler
	eventsHandler = &bridge.EventsHandler{History: st, HistoryReader: dispatchReader{}}
	t.Cleanup(func() { eventsHandler = orig })
}

const sqsRecord = `{"Records":[{"messageId":"m1","eventSource":"aws:sqs","body":"{\"channel\":\"C7\",\"oldest\":\"1.0\",\"remaining\":200}"}]}`

func TestHandle_SQSRecordRoutesToBackfill(t *testing.T) {
	st := &dispatchStore{meta: &bridge.ChannelMeta{Channel: "C7", BackfillState: "pending"}}
	withEventsHandler(t, st)
	out, err := handle(context.Background(), json.RawMessage(sqsRecord))
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := out.(events.SQSEventResponse)
	if !ok {
		t.Fatalf("want SQSEventResponse, got %T", out)
	}
	if len(resp.BatchItemFailures) != 0 {
		t.Fatalf("unexpected failures: %+v", resp.BatchItemFailures)
	}
	if st.meta == nil || st.meta.BackfillState != "done" {
		t.Fatalf("meta state = %+v, want done", st.meta)
	}
}

func TestHandle_SQSStoreErrorReportsBatchItemFailure(t *testing.T) {
	st := &dispatchStore{getMetaErr: errors.New("ddb down")}
	withEventsHandler(t, st)
	out, err := handle(context.Background(), json.RawMessage(sqsRecord))
	if err != nil {
		t.Fatal(err)
	}
	resp := out.(events.SQSEventResponse)
	if len(resp.BatchItemFailures) != 1 || resp.BatchItemFailures[0].ItemIdentifier != "m1" {
		t.Fatalf("failures = %+v, want exactly m1", resp.BatchItemFailures)
	}
}

func TestHandle_SQSNilHandlerAndMalformed(t *testing.T) {
	orig := eventsHandler
	eventsHandler = nil
	t.Cleanup(func() { eventsHandler = orig })
	out, _ := handle(context.Background(), json.RawMessage(sqsRecord))
	if n := len(out.(events.SQSEventResponse).BatchItemFailures); n != 1 {
		t.Fatalf("nil handler: %d failures, want 1", n)
	}
	bad := `{"Records":[{"messageId":"m2","eventSource":"aws:sqs","body":"not json"}]}`
	out, _ = handle(context.Background(), json.RawMessage(bad))
	if n := len(out.(events.SQSEventResponse).BatchItemFailures); n != 0 {
		t.Fatalf("malformed: %d failures, want 0 (dropped)", n)
	}
}

func TestHandle_URLRequestStillDispatches(t *testing.T) {
	orig := eventsHandler
	eventsHandler = nil
	t.Cleanup(func() { eventsHandler = orig })
	raw, _ := json.Marshal(events.LambdaFunctionURLRequest{RawPath: "/events", Body: "{}"})
	out, err := handle(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	resp, ok := out.(events.LambdaFunctionURLResponse)
	if !ok || resp.StatusCode != 503 {
		t.Fatalf("got %T %+v, want URL response 503", out, out)
	}
}

func TestWireChannelRoutes_SetsPatterns(t *testing.T) {
	h := &bridge.EventsHandler{}
	p := &fakeParam{value: `{"v":1,"routes":[],"patterns":[{"glob":"proj-*","alias":"a","profile":"p","allow":["*@example.com"],"mention_only":true}]}`}
	wireChannelRoutes(context.Background(), h, p, "/km/slack/routes", defaultRouteDeps{})
	if len(h.Patterns) != 1 {
		t.Fatalf("patterns = %d, want 1", len(h.Patterns))
	}
	h2 := &bridge.EventsHandler{}
	p2 := &fakeParam{value: `{"v":1,"routes":[]}`}
	wireChannelRoutes(context.Background(), h2, p2, "/km/slack/routes", defaultRouteDeps{})
	if h2.Patterns != nil {
		t.Fatalf("patterns = %v, want nil", h2.Patterns)
	}
}
