package httpproxy_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	httpproxy "github.com/whereiskurt/klanker-maker/sidecars/http-proxy/httpproxy"
)

// startDenyProxy builds a proxy with a deny list plus whatever extra options
// the caller wants, dialing every outbound connection to targetAddr.
func startDenyProxy(t *testing.T, targetAddr string, allowed, denied []string, opts ...httpproxy.ProxyOption) string {
	t.Helper()

	opts = append(opts, httpproxy.WithDeniedHosts(denied))
	proxy := httpproxy.NewProxy(allowed, "sandbox-deny-test", opts...)
	proxy.Tr = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, targetAddr)
		},
	}

	s := httptest.NewServer(proxy)
	t.Cleanup(s.Close)
	u, _ := url.Parse(s.URL)
	return u.Host
}

func denyTestTarget(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream reached"))
	}))
	t.Cleanup(s.Close)
	u, _ := url.Parse(s.URL)
	return u.Host
}

func denyProxyClient(t *testing.T, proxyAddr string) *http.Client {
	t.Helper()
	proxyURL, _ := url.Parse("http://" + proxyAddr)
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
}

// The deny list must beat the "*" allow-all wildcard end to end, not just in
// the matcher.
func TestHTTPProxy_DenyBeatsWildcardAllow(t *testing.T) {
	target := denyTestTarget(t)
	proxyAddr := startDenyProxy(t, target, []string{"*"}, []string{"evil.example.com"})
	client := denyProxyClient(t, proxyAddr)

	resp, err := client.Get("http://evil.example.com/payload")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("denied host under \"*\": got %d, want 403", resp.StatusCode)
	}

	ok, err := client.Get("http://good.example.com/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		t.Errorf("non-denied host under \"*\": got %d, want 200", ok.StatusCode)
	}
}

// WithGitHubRepoFilter implicitly allows GitHub hosts, bypassing the host
// allowlist entirely. A deny has to sit in front of that carve-out, or the
// denylist is only as good as the handler-registration order.
func TestHTTPProxy_DenyBeatsGitHubCarveOut(t *testing.T) {
	target := denyTestTarget(t)
	proxyAddr := startDenyProxy(t, target,
		nil, // no allowedHosts — GitHub is implicitly allowed by the repo filter
		[]string{"api.github.com"},
		httpproxy.WithGitHubRepoFilter([]string{"owner/repo"}),
	)
	client := denyProxyClient(t, proxyAddr)

	resp, err := client.Get("http://api.github.com/rate_limit")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("denied host that the GitHub filter would otherwise allow: got %d, want 403",
			resp.StatusCode)
	}
}

// No deny list configured must leave every existing decision untouched.
func TestHTTPProxy_NoDenyListIsInert(t *testing.T) {
	target := denyTestTarget(t)
	proxyAddr := startDenyProxy(t, target, []string{"good.example.com"}, nil)
	client := denyProxyClient(t, proxyAddr)

	ok, err := client.Get("http://good.example.com/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		t.Errorf("allowed host with no deny list: got %d, want 200", ok.StatusCode)
	}

	blocked, err := client.Get("http://other.example.com/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer blocked.Body.Close()
	if blocked.StatusCode != http.StatusForbidden {
		t.Errorf("host outside the allowlist: got %d, want 403", blocked.StatusCode)
	}
}

func TestIsHostDenied_ExactAndSuffix(t *testing.T) {
	denied := []string{"evil.example.com", ".tracker.net"}

	cases := []struct {
		name string
		host string
		want bool
	}{
		{"exact match", "evil.example.com", true},
		{"port stripped", "evil.example.com:443", true},
		{"case insensitive", "EVIL.Example.COM", true},
		{"subdomain of denied apex", "a.evil.example.com", true},
		{"leading-dot entry matches apex", "tracker.net", true},
		{"leading-dot entry matches subdomain", "cdn.tracker.net", true},
		{"leading-dot entry with port", "cdn.tracker.net:8443", true},
		{"unrelated host", "github.com", false},
		{"prefix collision", "evil.example.community", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := httpproxy.IsHostDenied(tc.host, denied); got != tc.want {
				t.Errorf("IsHostDenied(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestIsHostDenied_EmptyListDeniesNothing(t *testing.T) {
	if httpproxy.IsHostDenied("anything.example.com", nil) {
		t.Error("empty deny list must deny nothing")
	}
}

func TestIsHostDenied_StarDeniesEverything(t *testing.T) {
	if !httpproxy.IsHostDenied("github.com", []string{"*"}) {
		t.Error(`"*" in the deny list must deny everything`)
	}
}

// An entry with no leading dot must still cover subdomains, matching how the
// DNS proxy treats the same profile string. If these two drifted, a name could
// resolve but then be reachable (or vice versa) purely by which list it was in.
func TestIsHostDenied_BareEntryCoversSubdomains(t *testing.T) {
	if !httpproxy.IsHostDenied("api.evil.example.com", []string{"evil.example.com"}) {
		t.Error("bare deny entry must cover subdomains, as the DNS proxy does")
	}
}
