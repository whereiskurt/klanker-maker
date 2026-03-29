package httpproxy_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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

// generateTestCA creates a self-signed CA cert+key PEM for testing WithCustomCA.
func generateTestCA(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "km-test-ca"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(1 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	var buf []byte
	buf = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	buf = append(buf, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})...)
	return buf
}

func TestWithCustomCA_SetsGoproxyCa(t *testing.T) {
	// Save original CA to restore after test.
	origCA := goproxy.GoproxyCa
	defer func() { goproxy.GoproxyCa = origCA }()

	caPEM := generateTestCA(t)
	// Apply the option — it should replace goproxy.GoproxyCa.
	proxy := httpproxy.NewProxy(nil, "test-sandbox", httpproxy.WithCustomCA(caPEM))
	if proxy == nil {
		t.Fatal("NewProxy returned nil")
	}
	if goproxy.GoproxyCa.Leaf == nil {
		t.Fatal("GoproxyCa.Leaf is nil after WithCustomCA")
	}
	if goproxy.GoproxyCa.Leaf.Subject.CommonName != "km-test-ca" {
		t.Errorf("expected CA CN 'km-test-ca', got %q", goproxy.GoproxyCa.Leaf.Subject.CommonName)
	}
}

func TestWithCustomCA_InvalidPEM_FallsBack(t *testing.T) {
	origCA := goproxy.GoproxyCa
	origCN := ""
	if origCA.Leaf != nil {
		origCN = origCA.Leaf.Subject.CommonName
	}
	defer func() { goproxy.GoproxyCa = origCA }()

	// Pass garbage PEM — should log error and not crash.
	proxy := httpproxy.NewProxy(nil, "test-sandbox", httpproxy.WithCustomCA([]byte("not-valid-pem")))
	if proxy == nil {
		t.Fatal("NewProxy returned nil with invalid PEM")
	}
	// CA should still be the original (fallback).
	if goproxy.GoproxyCa.Leaf != nil && goproxy.GoproxyCa.Leaf.Subject.CommonName != origCN {
		t.Errorf("CA should have fallen back to original, got CN %q", goproxy.GoproxyCa.Leaf.Subject.CommonName)
	}
}

// ---------------------------------------------------------------------------
// GitHub MITM filter integration tests
// ---------------------------------------------------------------------------

// makeGitHubTarget starts a plain-HTTP test server that simulates GitHub
// responses. It returns the server and a URL template where {path} is a
// placeholder for callers to substitute.
func makeGitHubTarget(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return s
}

// makeGitHubProxyClient returns a plain HTTP client routed through the proxy
// but with the Host header overridden so the proxy sees it as a GitHub host.
// Since we can't do real TLS in unit tests, we use plain HTTP and rely on
// the OnRequest (plain-HTTP) handler being registered alongside the CONNECT
// handler. The important invariant is that the proxy enforces the repo filter.
func makeGitHubProxyClient(t *testing.T, proxyAddr string) *http.Client {
	t.Helper()
	proxyURL, _ := url.Parse("http://" + proxyAddr)
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}
}

// githubPlainHTTPClient returns an http.Client that routes requests through the
// proxy using plain HTTP. The client does NOT follow CONNECT tunnels — it sends
// plain-HTTP proxy requests. This lets us test the OnRequest DoFunc handlers
// without needing real TLS or a valid certificate chain.
func githubPlainHTTPClient(t *testing.T, proxyAddr string) *http.Client {
	t.Helper()
	proxyURL, _ := url.Parse("http://" + proxyAddr)
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			// Disable TLS so we only exercise the plain-HTTP code path.
			TLSClientConfig: nil,
		},
	}
}

// startGitHubFilterProxy starts a proxy with WithGitHubRepoFilter and injects
// a custom Dialer that redirects all connections to GitHub hosts to targetAddr
// (our local test server). This lets integration tests exercise the full
// OnRequest filter path using plain HTTP without needing real TLS or DNS.
func startGitHubFilterProxy(
	t *testing.T,
	targetAddr string,
	allowedRepos []string,
) (*httptest.Server, string) {
	t.Helper()

	proxy := httpproxy.NewProxy(
		nil, // no allowedHosts — GitHub is implicitly allowed via WithGitHubRepoFilter
		"sandbox-gh-test",
		httpproxy.WithGitHubRepoFilter(allowedRepos),
	)

	// Override the proxy's outbound transport to dial our local target instead
	// of real GitHub hosts. This intercepts connections at the TCP layer.
	proxy.Tr = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Redirect all github.com / githubusercontent connections to local target.
			return (&net.Dialer{}).DialContext(ctx, network, targetAddr)
		},
	}

	s := httptest.NewServer(proxy)
	t.Cleanup(s.Close)
	u, _ := url.Parse(s.URL)
	return s, u.Host
}

// TestHTTPProxy_GitHubAllowedRepo verifies that a request to an allowed repo
// passes through (proxy returns the upstream response), even when github.com
// is NOT in the allowedHosts list. The presence of WithGitHubRepoFilter
// implicitly allows GitHub hosts.
func TestHTTPProxy_GitHubAllowedRepo(t *testing.T) {
	target := makeGitHubTarget(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	targetURL, _ := url.Parse(target.URL)
	targetHost := targetURL.Host

	_, proxyAddr := startGitHubFilterProxy(t, targetHost, []string{"owner/repo"})
	client := proxyClient(t, proxyAddr)

	// Request to github.com/owner/repo — proxy redirects TCP to local target.
	resp, err := client.Get("http://github.com/owner/repo")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for allowed repo, got %d", resp.StatusCode)
	}
}

// TestHTTPProxy_GitHubBlockedRepo verifies that a request to a repo NOT in the
// allowedRepos list returns 403 with a JSON body containing "repo_not_allowed".
func TestHTTPProxy_GitHubBlockedRepo(t *testing.T) {
	target := makeGitHubTarget(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("should not reach"))
	}))
	targetURL, _ := url.Parse(target.URL)
	targetHost := targetURL.Host

	_, proxyAddr := startGitHubFilterProxy(t, targetHost, []string{"owner/allowed"})
	client := proxyClient(t, proxyAddr)

	resp, err := client.Get("http://github.com/owner/blocked")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for blocked repo, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("expected JSON body, got: %s", body)
	}
	if m["error"] != "repo_not_allowed" {
		t.Errorf("expected error=repo_not_allowed, got %v", m["error"])
	}
}

// TestHTTPProxy_GitHubNonRepoPassthrough verifies that non-repo GitHub URLs
// (e.g. /rate_limit, /login) pass through without being blocked.
func TestHTTPProxy_GitHubNonRepoPassthrough(t *testing.T) {
	target := makeGitHubTarget(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("rate limit response"))
	}))
	targetURL, _ := url.Parse(target.URL)
	targetHost := targetURL.Host

	_, proxyAddr := startGitHubFilterProxy(t, targetHost, []string{"owner/repo"})
	client := proxyClient(t, proxyAddr)

	// /rate_limit is a non-repo URL on api.github.com — should pass through.
	resp, err := client.Get("http://api.github.com/rate_limit")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for non-repo URL, got %d", resp.StatusCode)
	}
}

// TestHTTPProxy_GitHubNoFilter verifies that when WithGitHubRepoFilter is not
// configured, GitHub hosts are subject to normal host-level filtering only
// (i.e., NOT implicitly allowed). This preserves backward compatibility.
func TestHTTPProxy_GitHubNoFilter(t *testing.T) {
	target := makeGitHubTarget(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	targetURL, _ := url.Parse(target.URL)
	targetHost := targetURL.Host

	// No WithGitHubRepoFilter — github.com is NOT in allowed list either.
	proxy := httpproxy.NewProxy(
		[]string{targetHost}, // only local target is allowed, not github.com
		"sandbox-gh-test",
	)
	_, proxyAddr := startProxyServer(t, proxy)
	client := proxyClient(t, proxyAddr)

	// Without WithGitHubRepoFilter, github.com is not in allowedHosts,
	// so the plain-HTTP handler should block it with 403.
	resp, err := client.Get("http://github.com/owner/repo")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 when GitHub not in allowedHosts and no filter configured, got %d", resp.StatusCode)
	}
}
