package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// captureWriter is a tiny io.Writer that accumulates written bytes so tests can
// assert on what a verb printed to stdout.
type captureWriter struct{ b []byte }

func (c *captureWriter) Write(p []byte) (int, error) { c.b = append(c.b, p...); return len(p), nil }
func (c *captureWriter) String() string              { return string(c.b) }

// writeBodyFile is a test helper that writes body text to a temp file and
// returns its path, for exercising the --body @file flag.
func writeBodyFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "body.txt")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	return p
}

// decodeActivity unmarshals the activities request body into the nested
// {"data":{"type":..,"attributes":{"message":..,"internal":..}}} shape so
// tests can assert the internal flag at the JSON-body level (safety layer 4).
type activityReqBody struct {
	Data struct {
		Type       string `json:"type"`
		Attributes struct {
			Message  string `json:"message"`
			Internal bool   `json:"internal"`
		} `json:"attributes"`
	} `json:"data"`
}

func TestDispatch_NoArgs(t *testing.T) {
	if code := dispatch(nil, io.Discard); code != 2 {
		t.Errorf("dispatch(nil) = %d; want 2", code)
	}
}

func TestDispatch_UnknownSubcommand(t *testing.T) {
	if code := dispatch([]string{"notaverb"}, io.Discard); code != 2 {
		t.Errorf("dispatch(notaverb) = %d; want 2", code)
	}
}

func TestDispatch_Help(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		if code := dispatch([]string{arg}, io.Discard); code != 0 {
			t.Errorf("dispatch(%q) = %d; want 0", arg, code)
		}
	}
}

// TestCommentInternalDefault asserts that `km-h1 comment` with NO visibility
// flag posts attributes.internal == true — the safety-critical default at the
// JSON-body level.
func TestCommentInternalDefault(t *testing.T) {
	var gotPath, gotMethod string
	var got activityReqBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"99"}}`))
	}))
	defer srv.Close()

	bodyFile := writeBodyFile(t, "Triaging this report.")
	code := dispatch([]string{
		"comment", "--report", "2468", "--body", "@" + bodyFile,
	}, io.Discard, withBaseURL(srv.URL), withCreds("apiuser", "apitoken"))

	if code != 0 {
		t.Fatalf("comment exit = %d; want 0", code)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q; want POST", gotMethod)
	}
	if gotPath != "/reports/2468/activities" {
		t.Errorf("path = %q; want /reports/2468/activities", gotPath)
	}
	if got.Data.Type != "activity-comment" {
		t.Errorf("data.type = %q; want activity-comment", got.Data.Type)
	}
	if !got.Data.Attributes.Internal {
		t.Errorf("attributes.internal = false; want true (internal-by-default safety layer)")
	}
	if got.Data.Attributes.Message != "Triaging this report." {
		t.Errorf("attributes.message = %q; want %q", got.Data.Attributes.Message, "Triaging this report.")
	}
}

// TestCommentResearcherVisible asserts that --reply-to-researcher must be
// explicit to flip attributes.internal to false.
func TestCommentResearcherVisible(t *testing.T) {
	var got activityReqBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"99"}}`))
	}))
	defer srv.Close()

	bodyFile := writeBodyFile(t, "Thanks for the report!")
	code := dispatch([]string{
		"comment", "--report", "2468", "--body", "@" + bodyFile, "--reply-to-researcher",
	}, io.Discard, withBaseURL(srv.URL), withCreds("apiuser", "apitoken"))

	if code != 0 {
		t.Fatalf("comment exit = %d; want 0", code)
	}
	if got.Data.Attributes.Internal {
		t.Errorf("attributes.internal = true; want false with --reply-to-researcher")
	}
}

// TestCommentBasicAuth asserts the request carries the Authorization: Basic
// header derived from the supplied creds.
func TestCommentBasicAuth(t *testing.T) {
	var gotUser, gotPass string
	var gotOK bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, gotOK = r.BasicAuth()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"99"}}`))
	}))
	defer srv.Close()

	bodyFile := writeBodyFile(t, "hi")
	code := dispatch([]string{
		"comment", "--report", "2468", "--body", "@" + bodyFile,
	}, io.Discard, withBaseURL(srv.URL), withCreds("apiuser", "apitoken"))

	if code != 0 {
		t.Fatalf("comment exit = %d; want 0", code)
	}
	if !gotOK {
		t.Fatalf("request carried no Basic Auth header")
	}
	if gotUser != "apiuser" || gotPass != "apitoken" {
		t.Errorf("basic auth = %q:%q; want apiuser:apitoken", gotUser, gotPass)
	}
}

// TestRead asserts `km-h1 read` issues GET /reports/{id} and prints the body.
func TestRead(t *testing.T) {
	const respBody = `{"data":{"id":"2468","attributes":{"title":"XSS"}}}`
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(respBody))
	}))
	defer srv.Close()

	var out captureWriter
	code := dispatch([]string{
		"read", "--report", "2468",
	}, io.Discard, withBaseURL(srv.URL), withCreds("apiuser", "apitoken"), withStdout(&out))

	if code != 0 {
		t.Fatalf("read exit = %d; want 0", code)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q; want GET", gotMethod)
	}
	if gotPath != "/reports/2468" {
		t.Errorf("path = %q; want /reports/2468", gotPath)
	}
	if out.String() == "" {
		t.Errorf("read printed nothing; want report JSON")
	}
}

// TestRetry429 asserts a 429 then 200 sequence is retried with backoff and
// ultimately succeeds.
func TestRetry429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"99"}}`))
	}))
	defer srv.Close()

	bodyFile := writeBodyFile(t, "retry me")
	code := dispatch([]string{
		"comment", "--report", "2468", "--body", "@" + bodyFile,
	}, io.Discard, withBaseURL(srv.URL), withCreds("apiuser", "apitoken"), withBackoff(noBackoff))

	if code != 0 {
		t.Fatalf("comment exit = %d after 429 retry; want 0", code)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("server calls = %d; want 2 (429 then 200)", got)
	}
}

// TestState asserts `km-h1 state` issues the pinned state-change request.
// Per 103-CAPTURE/field-paths.md OQ2 the endpoint is LOW-confidence; the test
// pins the chosen candidate (POST /reports/{id}/state_changes).
func TestState(t *testing.T) {
	var gotMethod, gotPath string
	var got struct {
		Data struct {
			Type       string `json:"type"`
			Attributes struct {
				State string `json:"state"`
			} `json:"attributes"`
		} `json:"data"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"id":"99"}}`))
	}))
	defer srv.Close()

	code := dispatch([]string{
		"state", "--report", "2468", "--to", "triaged",
	}, io.Discard, withBaseURL(srv.URL), withCreds("apiuser", "apitoken"))

	if code != 0 {
		t.Fatalf("state exit = %d; want 0", code)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q; want POST", gotMethod)
	}
	if gotPath != "/reports/2468/state_changes" {
		t.Errorf("path = %q; want /reports/2468/state_changes", gotPath)
	}
	if got.Data.Attributes.State != "triaged" {
		t.Errorf("attributes.state = %q; want triaged", got.Data.Attributes.State)
	}
}
