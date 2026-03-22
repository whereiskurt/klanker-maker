package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// --- Mock implementations ---

// mockLister satisfies SandboxLister.
type mockLister struct {
	records []kmaws.SandboxRecord
	err     error
}

func (m *mockLister) ListSandboxes(_ context.Context, _ string) ([]kmaws.SandboxRecord, error) {
	return m.records, m.err
}

// mockFinder satisfies SandboxFinder.
type mockFinder struct {
	location *kmaws.SandboxLocation
	err      error
}

func (m *mockFinder) FindSandbox(_ context.Context, _ string) (*kmaws.SandboxLocation, error) {
	return m.location, m.err
}

// mockCWLogs satisfies CWLogsAPI.
type mockCWLogs struct {
	output *cloudwatchlogs.FilterLogEventsOutput
	err    error
}

func (m *mockCWLogs) FilterLogEvents(_ context.Context, input *cloudwatchlogs.FilterLogEventsInput, _ ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error) {
	return m.output, m.err
}

// --- Test helpers ---

var (
	testCreatedAt = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	testExpiry    = time.Date(2026, 1, 16, 10, 0, 0, 0, time.UTC)
)

func sampleRecords() []kmaws.SandboxRecord {
	return []kmaws.SandboxRecord{
		{
			SandboxID:    "sbx-abc123",
			Profile:      "basic-ec2.yaml",
			Substrate:    "ec2",
			Region:       "us-east-1",
			Status:       "running",
			CreatedAt:    testCreatedAt,
			TTLExpiry:    &testExpiry,
			TTLRemaining: "23h00m",
		},
	}
}

func sampleLocation() *kmaws.SandboxLocation {
	return &kmaws.SandboxLocation{
		SandboxID:     "sbx-abc123",
		S3StatePath:   "tf-km/sandboxes/sbx-abc123",
		ResourceCount: 3,
		ResourceARNs: []string{
			"arn:aws:ec2:us-east-1:123456789012:instance/i-0123456789abcdef0",
			"arn:aws:ec2:us-east-1:123456789012:vpc/vpc-0123456789abcdef0",
			"arn:aws:iam::123456789012:role/km-sbx-abc123",
		},
	}
}

// newTestHandler constructs a Handler with minimal inline templates for testing.
func newTestHandler(lister SandboxLister, finder SandboxFinder, cw CWLogsFilterAPI, profilesDir string) *Handler {
	return &Handler{
		tmpl:        buildTestTemplates(),
		lister:      lister,
		finder:      finder,
		cwClient:    cw,
		profilesDir: profilesDir,
	}
}

// --- Tests ---

// Test: GET / returns 200 with "sandbox" in body.
func TestHandleDashboard_FullPage(t *testing.T) {
	h := newTestHandler(&mockLister{records: sampleRecords()}, nil, nil, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	h.handleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "sandbox") {
		t.Errorf("expected 'sandbox' in body, got: %s", body)
	}
}

// Test: GET /api/sandboxes with HX-Request header returns partial HTML (not full page).
func TestHandleDashboard_HTMXPartial(t *testing.T) {
	h := newTestHandler(&mockLister{records: sampleRecords()}, nil, nil, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/api/sandboxes", nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()

	h.handleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	// HTMX partial should contain sandbox row data (not full page with <html> tag)
	if strings.Contains(body, "<html") {
		t.Errorf("HTMX partial should not contain full <html> page, got: %s", body)
	}
	if !strings.Contains(body, "sbx-abc123") {
		t.Errorf("expected sandbox ID in partial, got: %s", body)
	}
}

// Test: GET /api/sandboxes without HX-Request returns full page with base layout.
func TestHandleSandboxesPartial_FullPage(t *testing.T) {
	h := newTestHandler(&mockLister{records: sampleRecords()}, nil, nil, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/api/sandboxes", nil)
	w := httptest.NewRecorder()

	h.handleSandboxesPartial(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// Test: GET /api/sandboxes/{id} returns 200 with resource ARNs listed.
func TestHandleSandboxDetail_Found(t *testing.T) {
	finder := &mockFinder{location: sampleLocation()}
	lister := &mockLister{records: sampleRecords()}
	h := newTestHandler(lister, finder, nil, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/api/sandboxes/sbx-abc123", nil)
	req = setPathValue(req, "id", "sbx-abc123")
	w := httptest.NewRecorder()

	h.handleSandboxDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "arn:aws:ec2") {
		t.Errorf("expected resource ARN in body, got: %s", body)
	}
}

// Test: GET /api/sandboxes/{id} for nonexistent sandbox returns 404.
func TestHandleSandboxDetail_NotFound(t *testing.T) {
	finder := &mockFinder{err: kmaws.ErrSandboxNotFound}
	h := newTestHandler(&mockLister{}, finder, nil, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/api/sandboxes/sbx-missing", nil)
	req = setPathValue(req, "id", "sbx-missing")
	w := httptest.NewRecorder()

	h.handleSandboxDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// Test: GET /api/sandboxes/{id}/logs returns 200 with log entries.
func TestHandleSandboxLogs_WithEntries(t *testing.T) {
	ts1 := int64(1700000000000) // milliseconds
	ts2 := int64(1700000001000)
	cw := &mockCWLogs{
		output: &cloudwatchlogs.FilterLogEventsOutput{
			Events: []cwltypes.FilteredLogEvent{
				{Timestamp: aws.Int64(ts1), Message: aws.String("sandbox sbx-abc123 started")},
				{Timestamp: aws.Int64(ts2), Message: aws.String("sandbox sbx-abc123 ready")},
			},
		},
	}
	h := newTestHandler(&mockLister{}, nil, cw, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/api/sandboxes/sbx-abc123/logs", nil)
	req = setPathValue(req, "id", "sbx-abc123")
	w := httptest.NewRecorder()

	h.handleSandboxLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "sbx-abc123 started") {
		t.Errorf("expected log message in response, got: %s", body)
	}
	if !strings.Contains(body, "sbx-abc123 ready") {
		t.Errorf("expected second log message in response, got: %s", body)
	}
}

// Test: nil cwClient returns placeholder gracefully.
func TestHandleSandboxLogs_NilClient(t *testing.T) {
	h := newTestHandler(&mockLister{}, nil, nil, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/api/sandboxes/sbx-abc123/logs", nil)
	req = setPathValue(req, "id", "sbx-abc123")
	w := httptest.NewRecorder()

	h.handleSandboxLogs(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even with nil cwClient, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "No audit logs available") {
		t.Errorf("expected placeholder message, got: %s", body)
	}
}

// Test: GET /api/schema returns application/json with valid JSON.
func TestHandleSchema(t *testing.T) {
	h := newTestHandler(&mockLister{}, nil, nil, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/api/schema", nil)
	w := httptest.NewRecorder()

	h.handleSchema(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected application/json Content-Type, got %q", ct)
	}
	body := w.Body.Bytes()
	if len(body) == 0 {
		t.Error("expected non-empty schema JSON body")
	}
	// Basic JSON validity: should start with '{'
	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "{") {
		t.Errorf("expected JSON object, got: %s", trimmed[:min(50, len(trimmed))])
	}
}

// Test: SchemaJSON() returns non-empty byte slice.
func TestSchemaJSON_NonEmpty(t *testing.T) {
	data := profile.SchemaJSON()
	if len(data) == 0 {
		t.Error("SchemaJSON() returned empty byte slice")
	}
}

// setPathValue injects a URL path value into the request context using Go 1.22's
// request URL pattern matching. For tests we manually set it.
func setPathValue(r *http.Request, key, value string) *http.Request {
	// Use the standard library's SetPathValue (Go 1.22+)
	r.SetPathValue(key, value)
	return r
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
