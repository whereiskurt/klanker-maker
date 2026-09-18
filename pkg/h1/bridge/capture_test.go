package bridge_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

type fakeS3Put struct {
	inputs []*s3.PutObjectInput
	err    error
}

func (f *fakeS3Put) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.inputs = append(f.inputs, in)
	return &s3.PutObjectOutput{}, f.err
}

func TestS3RawCapturer_WritesGUIDKeyedRecord(t *testing.T) {
	fake := &fakeS3Put{}
	c := &bridge.S3RawCapturer{
		Client: fake,
		Bucket: "my-artifacts",
		Now:    func() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC) },
	}
	headers := map[string]string{"x-h1-event": "report_created", "x-h1-delivery": "abc-123"}
	body := []byte(`{"data":{"report":{"id":"1"}}}`)

	if err := c.Capture(context.Background(), "abc-123", headers, body); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(fake.inputs) != 1 {
		t.Fatalf("PutObject calls=%d; want 1", len(fake.inputs))
	}
	in := fake.inputs[0]
	if *in.Bucket != "my-artifacts" || *in.Key != "h1-captures/abc-123.json" {
		t.Errorf("bucket/key = %s/%s; want my-artifacts/h1-captures/abc-123.json", *in.Bucket, *in.Key)
	}
	if in.ContentType == nil || *in.ContentType != "application/json" {
		t.Errorf("ContentType = %v; want application/json", in.ContentType)
	}
	raw, _ := io.ReadAll(in.Body)
	var rec struct {
		ReceivedAt string            `json:"received_at"`
		Headers    map[string]string `json:"headers"`
		Body       json.RawMessage   `json:"body"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("record is not JSON: %v\n%s", err, raw)
	}
	if rec.ReceivedAt != "2026-09-18T12:00:00Z" {
		t.Errorf("received_at = %q", rec.ReceivedAt)
	}
	if rec.Headers["x-h1-event"] != "report_created" {
		t.Errorf("headers not preserved: %+v", rec.Headers)
	}
	if string(rec.Body) != string(body) {
		t.Errorf("valid JSON body must be embedded verbatim, got %s", rec.Body)
	}
}

func TestS3RawCapturer_NonJSONBodyIsStringAndMissingGUIDUsesTimestamp(t *testing.T) {
	fake := &fakeS3Put{}
	c := &bridge.S3RawCapturer{
		Client: fake,
		Bucket: "b",
		Now:    func() time.Time { return time.Unix(0, 1700000000000000000) },
	}
	if err := c.Capture(context.Background(), "", map[string]string{}, []byte("not json")); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if got := *fake.inputs[0].Key; got != "h1-captures/1700000000000000000.json" {
		t.Errorf("key = %q; want timestamp fallback", got)
	}
	raw, _ := io.ReadAll(fake.inputs[0].Body)
	if !strings.Contains(string(raw), `"body":"not json"`) {
		t.Errorf("non-JSON body must be embedded as a string: %s", raw)
	}
}

func TestS3RawCapturer_PutErrorIsReturned(t *testing.T) {
	fake := &fakeS3Put{err: errString("boom")}
	c := &bridge.S3RawCapturer{Client: fake, Bucket: "b"}
	if err := c.Capture(context.Background(), "g", nil, []byte(`{}`)); err == nil {
		t.Errorf("PutObject error must be returned (the handler logs it and continues)")
	}
}

func TestS3RawCapturer_EmptyBucketIsError(t *testing.T) {
	c := &bridge.S3RawCapturer{Client: &fakeS3Put{}, Bucket: ""}
	if err := c.Capture(context.Background(), "g", nil, []byte(`{}`)); err == nil {
		t.Errorf("empty bucket must error rather than PutObject to \"\"")
	}
}
