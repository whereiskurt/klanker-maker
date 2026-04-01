//go:build linux

package tls

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
)

// ErrNotHTTP is returned when the payload does not contain an HTTP request.
var ErrNotHTTP = errors.New("not an HTTP request")

// HTTPRequest holds the parsed components of an HTTP/1.1 request line and Host header.
type HTTPRequest struct {
	Method string
	Path   string
	Host   string
	Proto  string
}

// validHTTPMethods is the set of methods we recognise as HTTP request indicators.
var validHTTPMethods = map[string]bool{
	"GET":     true,
	"POST":    true,
	"PUT":     true,
	"DELETE":  true,
	"PATCH":   true,
	"HEAD":    true,
	"OPTIONS": true,
	"CONNECT": true,
}

// ParseHTTPRequest parses an HTTP/1.1 request from captured TLS plaintext.
// It returns ErrNotHTTP when the payload does not start with a valid HTTP method
// or when the payload is an HTTP/2 connection preface.
func ParseHTTPRequest(payload []byte) (*HTTPRequest, error) {
	if len(payload) == 0 {
		return nil, ErrNotHTTP
	}

	// Reject HTTP/2 connection preface ("PRI * HTTP/2.0").
	if bytes.HasPrefix(payload, []byte("PRI * HTTP/2.0")) {
		return nil, ErrNotHTTP
	}

	// Quick check: first token must be a valid HTTP method.
	spaceIdx := bytes.IndexByte(payload, ' ')
	if spaceIdx < 0 {
		return nil, ErrNotHTTP
	}
	method := string(payload[:spaceIdx])
	if !validHTTPMethods[method] {
		return nil, ErrNotHTTP
	}

	// Ensure the payload has a proper line ending so http.ReadRequest can parse it.
	// If the payload is truncated (no \r\n\r\n), append one so the stdlib parser
	// can at least read the request line.
	data := payload
	if !bytes.Contains(data, []byte("\r\n\r\n")) {
		data = append(bytes.Clone(data), []byte("\r\n\r\n")...)
	}

	// Use the stdlib HTTP parser via a limited reader (guard against huge payloads).
	reader := bufio.NewReader(io.LimitReader(bytes.NewReader(data), int64(MaxPayloadLen)+256))
	req, err := http.ReadRequest(reader)
	if err != nil {
		return nil, ErrNotHTTP
	}

	return &HTTPRequest{
		Method: req.Method,
		Path:   req.URL.RequestURI(),
		Host:   req.Host,
		Proto:  req.Proto,
	}, nil
}

// isGitHubHost returns true if the host belongs to GitHub.
func isGitHubHost(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" ||
		host == "api.github.com" ||
		strings.HasSuffix(host, ".github.com")
}
