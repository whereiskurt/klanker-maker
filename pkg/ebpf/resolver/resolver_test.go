package resolver_test

import (
	"net"
	"testing"
	"time"

	"github.com/whereiskurt/klankrmkr/pkg/ebpf/resolver"
)

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
