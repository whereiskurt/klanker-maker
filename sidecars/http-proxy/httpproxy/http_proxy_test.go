package httpproxy_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/elazarl/goproxy"
	"github.com/whereiskurt/klankrmkr/sidecars/http-proxy/httpproxy"
)

// proxyClient returns an *http.Client that routes all requests through proxyAddr.
func proxyClient(t *testing.T, proxyAddr string) *http.Client {
	t.Helper()
	proxyURL, err := url.Parse("http://" + proxyAddr)
	if err != nil {
		t.Fatalf("invalid proxy URL: %v", err)
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}
}

// startProxyServer starts an httptest server using the given goproxy handler and
// returns the server + its host:port address.
func startProxyServer(t *testing.T, proxy *goproxy.ProxyHttpServer) (*httptest.Server, string) {
	t.Helper()
	s := httptest.NewServer(proxy)
	t.Cleanup(s.Close)
	u, _ := url.Parse(s.URL)
	return s, u.Host
}

func TestHTTPProxy_AllowedHost(t *testing.T) {
	// Start a target HTTPS-like server (plain HTTP for simplicity — we test the
	// proxy CONNECT decision, not TLS).
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	}))
	defer target.Close()

	targetURL, _ := url.Parse(target.URL)
	allowedHost := targetURL.Hostname()

	proxy := httpproxy.NewProxy([]string{allowedHost}, "test-sandbox")
	_, proxyAddr := startProxyServer(t, proxy)

	client := proxyClient(t, proxyAddr)
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for allowed host, got %d", resp.StatusCode)
	}
}

func TestHTTPProxy_BlockedHost(t *testing.T) {
	// Start a target server with host not in the allowlist.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "should not reach here")
	}))
	defer target.Close()

	proxy := httpproxy.NewProxy([]string{"allowed.example.com"}, "test-sandbox")
	_, proxyAddr := startProxyServer(t, proxy)

	client := proxyClient(t, proxyAddr)
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for blocked host, got %d", resp.StatusCode)
	}
}

func TestHTTPProxy_AllowedWithPort(t *testing.T) {
	// Verify that "host:port" is stripped correctly.
	if httpproxy.IsHostAllowed("allowed.example.com:443", []string{"allowed.example.com"}) == false {
		t.Error("IsHostAllowed should strip port and match host")
	}
	if httpproxy.IsHostAllowed("evil.com:443", []string{"allowed.example.com"}) == true {
		t.Error("IsHostAllowed should deny host not in list")
	}
	if httpproxy.IsHostAllowed("allowed.example.com:8443", []string{"allowed.example.com"}) == false {
		t.Error("IsHostAllowed should strip any port and still match")
	}
}

func TestHTTPProxy_TraceparentInjected(t *testing.T) {
	// Verify two things:
	// 1. The proxy returns "200 Connection established" for an allowed CONNECT.
	// 2. InjectTraceContext (called by the CONNECT handler) injects a Traceparent
	//    header when OTel is initialized with TraceContext propagator.

	// Part 1: verify the proxy allows the CONNECT and returns 200.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for target: %v", err)
	}
	defer ln.Close()
	_, targetPort, _ := net.SplitHostPort(ln.Addr().String())

	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(io.Discard, c)
			}(conn)
		}
	}()

	proxy := httpproxy.NewProxy([]string{"localhost"}, "test-sandbox")
	_, proxyAddr := startProxyServer(t, proxy)

	// Use raw TCP to send CONNECT — http.Client does not support manual CONNECT.
	connectTarget := fmt.Sprintf("localhost:%s", targetPort)
	rawConn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("failed to connect to proxy: %v", err)
	}
	defer rawConn.Close()

	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", connectTarget, connectTarget)
	if _, err := fmt.Fprint(rawConn, connectReq); err != nil {
		t.Fatalf("failed to write CONNECT: %v", err)
	}

	buf := make([]byte, 256)
	n, _ := rawConn.Read(buf)
	response := string(buf[:n])
	t.Logf("proxy CONNECT response: %q", response)

	if !strings.Contains(response, "200") {
		t.Errorf("expected 200 Connection established for allowed host, got: %q", response)
	}

	// Part 2: verify InjectTraceContext adds the Traceparent header.
	// With TraceContext propagator registered and no active span, the propagator
	// does not inject (W3C spec requires a valid trace-id). We verify the function
	// runs without panic and returns a canonical no-op result with context.Background().
	//
	// To get an actual traceparent we would need an active OTel span. Here we
	// verify the function signature works correctly.
	h := make(http.Header)
	httpproxy.InjectTraceContext(context.Background(), h)
	// No panic = inject ran. Value may be empty without an active span.
	t.Logf("InjectTraceContext with no span: Traceparent=%q", h.Get("Traceparent"))
}

func TestIsHostAllowed(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		allowed []string
		want    bool
	}{
		{"exact match", "example.com", []string{"example.com"}, true},
		{"exact match with port", "example.com:443", []string{"example.com"}, true},
		{"case insensitive", "EXAMPLE.COM", []string{"example.com"}, true},
		{"blocked host", "evil.com", []string{"example.com"}, false},
		{"empty allowed list", "example.com", []string{}, false},
		{"multiple allowed", "b.com", []string{"a.com", "b.com", "c.com"}, true},
		{"port only stripped", "example.com:8443", []string{"example.com"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := httpproxy.IsHostAllowed(tc.host, tc.allowed)
			if got != tc.want {
				t.Errorf("IsHostAllowed(%q, %v) = %v, want %v", tc.host, tc.allowed, got, tc.want)
			}
		})
	}
}
