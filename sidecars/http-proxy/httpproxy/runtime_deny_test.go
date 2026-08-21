package httpproxy_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
	httpproxy "github.com/whereiskurt/klanker-maker/sidecars/http-proxy/httpproxy"
)

func startDenierProxy(t *testing.T, targetAddr string, allowed []string, d *netpolicy.Denier) string {
	t.Helper()

	proxy := httpproxy.NewProxy(allowed, "sandbox-runtime-deny", httpproxy.WithDenier(d))
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

// A deny appended at runtime must take effect on an already-running proxy. The
// gate has to be registered even though the deny list was EMPTY at construction
// time — registering it lazily on first non-empty read would be too late.
func TestHTTPProxy_RuntimeDenyTakesEffectWithoutRestart(t *testing.T) {
	target := denyTestTarget(t)

	path := filepath.Join(t.TempDir(), "deny.list")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed deny file: %v", err)
	}
	denier := netpolicy.NewDenier(nil, netpolicy.NewStore(path, 0))

	proxyAddr := startDenierProxy(t, target, []string{"*"}, denier)
	client := denyProxyClient(t, proxyAddr)

	before, err := client.Get("http://later.example.com/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	before.Body.Close()
	if before.StatusCode != http.StatusOK {
		t.Fatalf("before the deny: got %d, want 200", before.StatusCode)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString("later.example.com\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	after, err := client.Get("http://later.example.com/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	after.Body.Close()
	if after.StatusCode != http.StatusForbidden {
		t.Errorf("after the deny: got %d, want 403", after.StatusCode)
	}

	// Narrowing, not sealing.
	other, err := client.Get("http://unrelated.example.org/")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	other.Body.Close()
	if other.StatusCode != http.StatusOK {
		t.Errorf("unrelated host: got %d, want 200", other.StatusCode)
	}
}

// A runtime deny must beat the GitHub carve-out exactly as a static one does.
func TestHTTPProxy_RuntimeDenyBeatsGitHubCarveOut(t *testing.T) {
	target := denyTestTarget(t)

	path := filepath.Join(t.TempDir(), "deny.list")
	if err := os.WriteFile(path, []byte("api.github.com\n"), 0o644); err != nil {
		t.Fatalf("seed deny file: %v", err)
	}
	denier := netpolicy.NewDenier(nil, netpolicy.NewStore(path, 0))

	proxy := httpproxy.NewProxy(nil, "sandbox-runtime-deny-gh",
		httpproxy.WithDenier(denier),
		httpproxy.WithGitHubRepoFilter([]string{"owner/repo"}),
	)
	proxy.Tr = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, target)
		},
	}
	s := httptest.NewServer(proxy)
	t.Cleanup(s.Close)
	u, _ := url.Parse(s.URL)

	client := denyProxyClient(t, u.Host)
	resp, err := client.Get("http://api.github.com/rate_limit")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("runtime-denied GitHub host: got %d, want 403", resp.StatusCode)
	}
}
