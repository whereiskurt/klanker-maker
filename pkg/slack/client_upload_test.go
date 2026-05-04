package slack

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// uploadStub builds an httptest.Server that mimics the Slack 3-step upload
// flow. The same server hosts the upload_url (it points back at /upload on
// itself) so a single stub covers all 3 endpoints.
//
// Caller hooks let individual tests inject failures or capture state.
type uploadStub struct {
	server          *httptest.Server
	step1OK         bool
	step1Error      string
	step2Status     int  // 0 → 200
	step2Drop       bool // close connection mid-PUT instead of replying
	step3OK         bool
	step3Error      string
	capturedLen     int64
	capturedHash    string
	capturedStep3   map[string]any
	capturedReceived []byte // optional full-body capture (used only by hash test sanity)
	step1Hits       int
	step2Hits       int
	step3Hits       int
	stepOrder       []string
}

func newUploadStub(t *testing.T) *uploadStub {
	t.Helper()
	s := &uploadStub{step1OK: true, step3OK: true}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/files.getUploadURLExternal":
			s.step1Hits++
			s.stepOrder = append(s.stepOrder, "step1")
			w.Header().Set("Content-Type", "application/json")
			if !s.step1OK {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": s.step1Error})
				return
			}
			uploadURL := "http://" + r.Host + "/upload"
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":         true,
				"upload_url": uploadURL,
				"file_id":    "F12345",
			})
		case "/upload":
			s.step2Hits++
			s.stepOrder = append(s.stepOrder, "step2")
			s.capturedLen, _ = strconv.ParseInt(r.Header.Get("Content-Length"), 10, 64)
			h := sha256.New()
			_, _ = io.Copy(h, r.Body)
			s.capturedHash = hex.EncodeToString(h.Sum(nil))
			if s.step2Drop {
				// Hijack and close immediately to simulate mid-PUT failure.
				if hj, ok := w.(http.Hijacker); ok {
					if conn, _, err := hj.Hijack(); err == nil {
						_ = conn.Close()
					}
				}
				return
			}
			status := s.step2Status
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
		case "/api/files.completeUploadExternal":
			s.step3Hits++
			s.stepOrder = append(s.stepOrder, "step3")
			body, _ := io.ReadAll(r.Body)
			s.capturedReceived = body
			_ = json.Unmarshal(body, &s.capturedStep3)
			w.Header().Set("Content-Type", "application/json")
			if !s.step3OK {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": s.step3Error})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"files": []map[string]any{
					{"id": "F12345", "permalink": "https://slack.example/files/F12345"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	return s
}

func (s *uploadStub) close() { s.server.Close() }

func (s *uploadStub) client() *Client {
	c := NewClient("xoxb-test", s.server.Client())
	c.SetBaseURL(s.server.URL + "/api")
	return c
}

// TestUploadFile_HappyPath: all 3 endpoints called in order; FileID + Permalink returned.
func TestUploadFile_HappyPath(t *testing.T) {
	s := newUploadStub(t)
	defer s.close()

	payload := []byte("hello slack")
	res, err := s.client().UploadFile(
		context.Background(),
		"C0123ABC",
		"1700000000.000100",
		"transcript.json.gz",
		"application/gzip",
		int64(len(payload)),
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if res.FileID != "F12345" {
		t.Errorf("FileID = %q; want F12345", res.FileID)
	}
	if res.Permalink != "https://slack.example/files/F12345" {
		t.Errorf("Permalink = %q; want slack.example/files/F12345", res.Permalink)
	}
	if got := strings.Join(s.stepOrder, ","); got != "step1,step2,step3" {
		t.Errorf("call order = %q; want step1,step2,step3", got)
	}
}

// TestUploadFile_Step1Failure: ok:false on step 1 surfaces error mentioning the step.
func TestUploadFile_Step1Failure(t *testing.T) {
	s := newUploadStub(t)
	s.step1OK = false
	s.step1Error = "upload_url_unreachable"
	defer s.close()

	_, err := s.client().UploadFile(
		context.Background(),
		"C0123ABC",
		"",
		"x.gz",
		"application/gzip",
		11,
		strings.NewReader("hello slack"),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "files.getUploadURLExternal") {
		t.Errorf("error %q does not identify step 1", err.Error())
	}
	if !strings.Contains(err.Error(), "upload_url_unreachable") {
		t.Errorf("error %q does not include Slack error code", err.Error())
	}
	if s.step2Hits != 0 || s.step3Hits != 0 {
		t.Errorf("steps after step1 hit unexpectedly: step2=%d step3=%d", s.step2Hits, s.step3Hits)
	}
}

// TestUploadFile_Step2NetworkFailure: step 2 returns 500 → error wraps "PUT upload".
func TestUploadFile_Step2NetworkFailure(t *testing.T) {
	s := newUploadStub(t)
	s.step2Status = http.StatusInternalServerError
	defer s.close()

	_, err := s.client().UploadFile(
		context.Background(),
		"C0123ABC",
		"",
		"x.gz",
		"application/gzip",
		11,
		strings.NewReader("hello slack"),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "PUT upload") {
		t.Errorf("error %q does not identify PUT step", err.Error())
	}
	if s.step3Hits != 0 {
		t.Errorf("step3 hit despite step2 failure: %d", s.step3Hits)
	}
}

// TestUploadFile_Step2ChunkedRejected: assert ContentLength header equals
// sizeBytes. Setting ContentLength on the request causes Go's HTTP client to
// use Content-Length framing instead of chunked transfer-encoding (which
// Slack rejects on signed upload URLs).
func TestUploadFile_Step2ChunkedRejected(t *testing.T) {
	s := newUploadStub(t)
	defer s.close()

	payload := []byte("hello-slack-chunked-test")
	_, err := s.client().UploadFile(
		context.Background(),
		"C0123ABC",
		"",
		"x.gz",
		"application/gzip",
		int64(len(payload)),
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if s.capturedLen != int64(len(payload)) {
		t.Errorf("Content-Length = %d; want %d (no chunked encoding)", s.capturedLen, len(payload))
	}
}

// TestUploadFile_Step3Failure: ok:false on completeUploadExternal surfaces error.
func TestUploadFile_Step3Failure(t *testing.T) {
	s := newUploadStub(t)
	s.step3OK = false
	s.step3Error = "channel_not_found"
	defer s.close()

	_, err := s.client().UploadFile(
		context.Background(),
		"C9NOTFOUND",
		"",
		"x.gz",
		"application/gzip",
		11,
		strings.NewReader("hello slack"),
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "files.completeUploadExternal") {
		t.Errorf("error %q does not identify step 3", err.Error())
	}
	if !strings.Contains(err.Error(), "channel_not_found") {
		t.Errorf("error %q does not include Slack error code", err.Error())
	}
}

// TestUploadFile_StreamingPassThrough: 1 MB body streamed via io.Reader; stub
// hashes the received bytes and asserts equality with the source hash. This
// proves no buffering/corruption between caller's reader and the wire.
func TestUploadFile_StreamingPassThrough(t *testing.T) {
	s := newUploadStub(t)
	defer s.close()

	payload := bytes.Repeat([]byte("ABCDEFGH"), 128*1024) // exactly 1 MiB
	expected := sha256.Sum256(payload)

	_, err := s.client().UploadFile(
		context.Background(),
		"C0123ABC",
		"",
		"big.bin",
		"application/octet-stream",
		int64(len(payload)),
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if s.capturedLen != int64(len(payload)) {
		t.Errorf("Content-Length = %d; want %d", s.capturedLen, len(payload))
	}
	if s.capturedHash != hex.EncodeToString(expected[:]) {
		t.Errorf("body hash mismatch: got %s want %s", s.capturedHash, hex.EncodeToString(expected[:]))
	}
}

// TestUploadFile_OmitEmptyThreadTS: when threadTS == "", step 3 JSON body
// must NOT contain "thread_ts" key (Slack rejects empty-string thread_ts).
// When threadTS != "", the key must be present with the supplied value.
func TestUploadFile_OmitEmptyThreadTS(t *testing.T) {
	t.Run("empty thread_ts omitted", func(t *testing.T) {
		s := newUploadStub(t)
		defer s.close()
		_, err := s.client().UploadFile(
			context.Background(),
			"C0123ABC",
			"", // empty
			"x.gz",
			"application/gzip",
			11,
			strings.NewReader("hello slack"),
		)
		if err != nil {
			t.Fatalf("UploadFile: %v", err)
		}
		if _, present := s.capturedStep3["thread_ts"]; present {
			t.Errorf("thread_ts unexpectedly present in step3 body: %v", s.capturedStep3)
		}
	})

	t.Run("non-empty thread_ts forwarded", func(t *testing.T) {
		s := newUploadStub(t)
		defer s.close()
		_, err := s.client().UploadFile(
			context.Background(),
			"C0123ABC",
			"1700000000.000100",
			"x.gz",
			"application/gzip",
			11,
			strings.NewReader("hello slack"),
		)
		if err != nil {
			t.Fatalf("UploadFile: %v", err)
		}
		v, present := s.capturedStep3["thread_ts"]
		if !present {
			t.Fatalf("thread_ts missing from step3 body: %v", s.capturedStep3)
		}
		if v != "1700000000.000100" {
			t.Errorf("thread_ts = %v; want 1700000000.000100", v)
		}
	})
}
