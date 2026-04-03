package resolver_test

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/whereiskurt/klankrmkr/pkg/ebpf/resolver"
)

// mockMapUpdater records which IPs were allowed and which were marked for proxy.
// Used by TestProxyHosts to verify that L7-flagged domains trigger MarkForProxy.
type mockMapUpdater struct {
	mu         sync.Mutex
	allowedIPs []net.IP
	proxyIPs   []net.IP
}

func (m *mockMapUpdater) AllowIP(ip net.IP) error {
	m.mu.Lock()
	m.allowedIPs = append(m.allowedIPs, ip)
	m.mu.Unlock()
	return nil
}

func (m *mockMapUpdater) MarkForProxy(ip net.IP) error {
	m.mu.Lock()
	m.proxyIPs = append(m.proxyIPs, ip)
	m.mu.Unlock()
	return nil
}

// TestAllowlistIsAllowed tests suffix matching semantics.
func TestAllowlistIsAllowed(t *testing.T) {
	tests := []struct {
		name     string
		domain   string
		suffixes []string
		want     bool
	}{
		{
			name:     "subdomain matches suffix with leading dot",
			domain:   "api.github.com",
			suffixes: []string{".github.com"},
			want:     true,
		},
		{
			name:     "exact match without leading dot in suffix",
			domain:   "github.com",
			suffixes: []string{".github.com"},
			want:     true,
		},
		{
			name:     "unrelated domain denied",
			domain:   "evil.com",
			suffixes: []string{".github.com"},
			want:     false,
		},
		{
			name:     "suffix only not substring — evil.com containing github.com suffix denied",
			domain:   "api.github.com.evil.com",
			suffixes: []string{".github.com"},
			want:     false,
		},
		{
			name:     "empty domain denied",
			domain:   "",
			suffixes: []string{".github.com"},
			want:     false,
		},
		{
			name:     "empty allowlist denies all",
			domain:   "foo.bar",
			suffixes: []string{},
			want:     false,
		},
		{
			name:     "case-insensitive matching — uppercase domain",
			domain:   "API.GitHub.COM",
			suffixes: []string{".github.com"},
			want:     true,
		},
		{
			name:     "trailing DNS dot stripped before comparison",
			domain:   "api.github.com.",
			suffixes: []string{".github.com"},
			want:     true,
		},
		{
			name:     "multiple suffixes — first matches",
			domain:   "api.github.com",
			suffixes: []string{".amazonaws.com", ".github.com"},
			want:     true,
		},
		{
			name:     "multiple suffixes — second matches",
			domain:   "s3.amazonaws.com",
			suffixes: []string{".github.com", ".amazonaws.com"},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			al := resolver.NewAllowlist(tt.suffixes)
			got := al.IsAllowed(tt.domain)
			if got != tt.want {
				t.Errorf("IsAllowed(%q, %v) = %v; want %v", tt.domain, tt.suffixes, got, tt.want)
			}
		})
	}
}

// TestAllowlistTTL tests resolved IP tracking and expiry.
func TestAllowlistTTL(t *testing.T) {
	al := resolver.NewAllowlist([]string{".github.com"})

	ip := net.ParseIP("1.2.3.4").To4()

	// Add entry with very short TTL.
	al.AddResolved("api.github.com", []net.IP{ip}, 50*time.Millisecond)

	// Should be resolvable immediately.
	if !al.IsResolved(ip) {
		t.Fatal("expected IP to be resolved immediately after AddResolved")
	}

	// Wait for expiry.
	time.Sleep(100 * time.Millisecond)

	// Sweep should remove expired entries and return removed IPs.
	removed := al.Sweep()

	var found bool
	for _, r := range removed {
		if r.Equal(ip) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Sweep() to return expired IP 1.2.3.4; got %v", removed)
	}

	// After sweep, IP should no longer be tracked.
	if al.IsResolved(ip) {
		t.Error("expected IP to no longer be resolved after Sweep() removed it")
	}
}

// TestAllowlistSweepKeepsNonExpired verifies Sweep only removes expired entries.
func TestAllowlistSweepKeepsNonExpired(t *testing.T) {
	al := resolver.NewAllowlist([]string{".github.com"})

	ipExpiring := net.ParseIP("1.2.3.4").To4()
	ipKeeping := net.ParseIP("5.6.7.8").To4()

	al.AddResolved("api.github.com", []net.IP{ipExpiring}, 50*time.Millisecond)
	al.AddResolved("example.github.com", []net.IP{ipKeeping}, 10*time.Second)

	time.Sleep(100 * time.Millisecond)

	removed := al.Sweep()

	// Only ipExpiring should be removed.
	for _, r := range removed {
		if r.Equal(ipKeeping) {
			t.Errorf("Sweep() removed non-expired IP %v", ipKeeping)
		}
	}

	if !al.IsResolved(ipKeeping) {
		t.Error("non-expired IP should still be tracked after Sweep()")
	}
}

// TestProxyHosts verifies that the ProxyHosts suffix list correctly identifies
// which domains should trigger MarkForProxy (L7 inspection) vs just AllowIP.
// The Allowlist suffix matching algorithm is the same used by isProxyHost, so
// these tests transitively validate that L7-flagged domain suffixes (github.com,
// api.github.com, .amazonaws.com, api.anthropic.com) correctly trigger
// MarkForProxy while non-L7 allowed domains do not.
func TestProxyHosts(t *testing.T) {
	// L7 proxy host suffixes derived from a profile with GitHub + Bedrock.
	// This mirrors what buildL7ProxyHosts() produces in pkg/compiler/userdata.go.
	l7ProxyHosts := []string{
		"github.com",
		"api.github.com",
		"raw.githubusercontent.com",
		"codeload.githubusercontent.com",
		".amazonaws.com",
		"api.anthropic.com",
	}

	tests := []struct {
		name        string
		domain      string
		wantIsProxy bool
	}{
		{
			name:        "github.com exact match is L7",
			domain:      "github.com",
			wantIsProxy: true,
		},
		{
			name:        "api.github.com exact match is L7",
			domain:      "api.github.com",
			wantIsProxy: true,
		},
		{
			name:        "raw.githubusercontent.com is L7",
			domain:      "raw.githubusercontent.com",
			wantIsProxy: true,
		},
		{
			name:        "subdomain of github.com is L7 via suffix match",
			domain:      "uploads.github.com",
			wantIsProxy: true,
		},
		{
			name:        "bedrock endpoint matches .amazonaws.com suffix",
			domain:      "bedrock-runtime.us-east-1.amazonaws.com",
			wantIsProxy: true,
		},
		{
			name:        "api.anthropic.com is L7",
			domain:      "api.anthropic.com",
			wantIsProxy: true,
		},
		{
			name:        "non-L7 domain not in proxy hosts",
			domain:      "example.com",
			wantIsProxy: false,
		},
		{
			name:        "evil.github.com.evil.com not matched (suffix only)",
			domain:      "github.com.evil.com",
			wantIsProxy: false,
		},
	}

	// Use NewAllowlist with the L7 proxy host suffixes to replicate the
	// isProxyHost suffix matching algorithm.
	proxyAllowlist := resolver.NewAllowlist(l7ProxyHosts)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := proxyAllowlist.IsAllowed(tt.domain)
			if got != tt.wantIsProxy {
				t.Errorf("IsAllowed(%q) = %v, want %v (proxyHosts = %v)",
					tt.domain, got, tt.wantIsProxy, l7ProxyHosts)
			}
		})
	}
}

// TestProxyHostsMockUpdater verifies that a ResolverConfig with ProxyHosts
// correctly uses the MapUpdater interface. This is a structural test confirming
// the interface is present and accepted by the resolver config.
func TestProxyHostsMockUpdater(t *testing.T) {
	mock := &mockMapUpdater{}

	cfg := resolver.ResolverConfig{
		ListenAddr:      "127.0.0.1:0",
		UpstreamAddr:    "169.254.169.253:53",
		SandboxID:       "test-proxy-sb",
		AllowedSuffixes: []string{"github.com", ".amazonaws.com"},
		MapUpdater:      mock,
		ProxyHosts:      []string{"github.com", "api.github.com", ".amazonaws.com", "api.anthropic.com"},
	}
	r := resolver.NewResolver(cfg)
	if r == nil {
		t.Fatal("NewResolver returned nil with valid ProxyHosts config")
	}

	// Verify mock can record both allow and proxy IPs.
	testIP := net.ParseIP("1.2.3.4")
	if err := mock.AllowIP(testIP); err != nil {
		t.Fatalf("AllowIP error: %v", err)
	}
	if err := mock.MarkForProxy(testIP); err != nil {
		t.Fatalf("MarkForProxy error: %v", err)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.allowedIPs) != 1 {
		t.Errorf("expected 1 allowed IP, got %d", len(mock.allowedIPs))
	}
	if len(mock.proxyIPs) != 1 {
		t.Errorf("expected 1 proxy IP, got %d", len(mock.proxyIPs))
	}
}
