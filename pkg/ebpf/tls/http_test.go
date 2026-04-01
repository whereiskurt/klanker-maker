//go:build linux

package tls

import (
	"errors"
	"testing"
)

func TestParseHTTPRequest_ValidGET(t *testing.T) {
	raw := []byte("GET /repos/owner/repo HTTP/1.1\r\nHost: api.github.com\r\nUser-Agent: git/2.39\r\n\r\n")
	req, err := ParseHTTPRequest(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Method != "GET" {
		t.Errorf("method = %q, want GET", req.Method)
	}
	if req.Path != "/repos/owner/repo" {
		t.Errorf("path = %q, want /repos/owner/repo", req.Path)
	}
	if req.Host != "api.github.com" {
		t.Errorf("host = %q, want api.github.com", req.Host)
	}
}

func TestParseHTTPRequest_ValidPOST(t *testing.T) {
	raw := []byte("POST /v1/messages HTTP/1.1\r\nHost: api.anthropic.com\r\nContent-Length: 42\r\n\r\n{\"model\":\"claude\"}")
	req, err := ParseHTTPRequest(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Method != "POST" {
		t.Errorf("method = %q, want POST", req.Method)
	}
	if req.Path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", req.Path)
	}
	if req.Host != "api.anthropic.com" {
		t.Errorf("host = %q, want api.anthropic.com", req.Host)
	}
}

func TestParseHTTPRequest_NonHTTPData(t *testing.T) {
	raw := []byte{0x16, 0x03, 0x01, 0x00, 0x05} // TLS handshake bytes
	_, err := ParseHTTPRequest(raw)
	if err == nil {
		t.Fatal("expected error for non-HTTP data")
	}
	if !errors.Is(err, ErrNotHTTP) {
		t.Errorf("error = %v, want ErrNotHTTP", err)
	}
}

func TestParseHTTPRequest_TruncatedNoHeaders(t *testing.T) {
	// Request line present but no complete headers
	raw := []byte("GET /path HTTP/1.1\r\n")
	req, err := ParseHTTPRequest(raw)
	// Should still parse the request line even without complete headers
	if err != nil {
		t.Fatalf("unexpected error for truncated request: %v", err)
	}
	if req.Method != "GET" {
		t.Errorf("method = %q, want GET", req.Method)
	}
	if req.Path != "/path" {
		t.Errorf("path = %q, want /path", req.Path)
	}
}

func TestParseHTTPRequest_HTTP2Preface(t *testing.T) {
	raw := []byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n")
	_, err := ParseHTTPRequest(raw)
	if err == nil {
		t.Fatal("expected error for HTTP/2 preface")
	}
	if !errors.Is(err, ErrNotHTTP) {
		t.Errorf("error = %v, want ErrNotHTTP", err)
	}
}

func TestParseHTTPRequest_EmptyPayload(t *testing.T) {
	_, err := ParseHTTPRequest(nil)
	if err == nil {
		t.Fatal("expected error for empty payload")
	}
	if !errors.Is(err, ErrNotHTTP) {
		t.Errorf("error = %v, want ErrNotHTTP", err)
	}
}
