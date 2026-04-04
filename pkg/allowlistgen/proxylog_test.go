package allowlistgen

import (
	"strings"
	"testing"
)

// TestParseDNSProxyLogs verifies that dns_query events are recorded and deduplicated.
func TestParseDNSProxyLogs(t *testing.T) {
	input := strings.NewReader(`
{"level":"info","sandbox_id":"test","event_type":"dns_query","domain":"api.github.com","allowed":true}
{"level":"info","sandbox_id":"test","event_type":"dns_query","domain":"pypi.org","allowed":true}
{"level":"info","sandbox_id":"test","event_type":"dns_query","domain":"api.github.com","allowed":true}
`)
	rec := NewRecorder()
	if err := parseDNSProxyLogs(input, rec); err != nil {
		t.Fatalf("parseDNSProxyLogs error: %v", err)
	}

	domains := rec.DNSDomains()
	if len(domains) != 2 {
		t.Fatalf("expected 2 unique DNS domains, got %d: %v", len(domains), domains)
	}
	if domains[0] != "api.github.com" {
		t.Errorf("expected domains[0] = api.github.com, got %s", domains[0])
	}
	if domains[1] != "pypi.org" {
		t.Errorf("expected domains[1] = pypi.org, got %s", domains[1])
	}
}

// TestParseDNSProxyLogs_SkipNonDNS verifies that non-dns_query events and
// non-JSON lines are silently skipped.
func TestParseDNSProxyLogs_SkipNonDNS(t *testing.T) {
	input := strings.NewReader(`
{"level":"info","event_type":"dns_response","domain":"api.github.com"}
not json at all
{"level":"debug","msg":"starting dns proxy"}
{"level":"info","event_type":"dns_query","domain":"pypi.org","allowed":true}
`)
	rec := NewRecorder()
	if err := parseDNSProxyLogs(input, rec); err != nil {
		t.Fatalf("parseDNSProxyLogs error: %v", err)
	}

	domains := rec.DNSDomains()
	if len(domains) != 1 {
		t.Fatalf("expected 1 DNS domain, got %d: %v", len(domains), domains)
	}
	if domains[0] != "pypi.org" {
		t.Errorf("expected pypi.org, got %s", domains[0])
	}
}

// TestParseHTTPProxyLogs_Connect verifies that github_mitm_connect events with
// a host:port are recorded with the port stripped.
func TestParseHTTPProxyLogs_Connect(t *testing.T) {
	input := strings.NewReader(`{"level":"info","event_type":"github_mitm_connect","host":"github.com:443"}`)
	rec := NewRecorder()
	if err := parseHTTPProxyLogs(input, rec); err != nil {
		t.Fatalf("parseHTTPProxyLogs error: %v", err)
	}

	hosts := rec.Hosts()
	if len(hosts) != 1 {
		t.Fatalf("expected 1 host, got %d: %v", len(hosts), hosts)
	}
	if hosts[0] != "github.com" {
		t.Errorf("expected github.com (port stripped), got %s", hosts[0])
	}
}

// TestParseHTTPProxyLogs_RepoAllowed verifies that github_repo_allowed events
// produce the correct owner/repo entry.
func TestParseHTTPProxyLogs_RepoAllowed(t *testing.T) {
	input := strings.NewReader(`{"level":"info","event_type":"github_repo_allowed","owner":"octocat","repo":"hello"}`)
	rec := NewRecorder()
	if err := parseHTTPProxyLogs(input, rec); err != nil {
		t.Fatalf("parseHTTPProxyLogs error: %v", err)
	}

	repos := rec.Repos()
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d: %v", len(repos), repos)
	}
	if repos[0] != "octocat/hello" {
		t.Errorf("expected octocat/hello, got %s", repos[0])
	}
}

// TestParseHTTPProxyLogs_Blocked verifies that http_blocked events are recorded
// in learning mode (captures everything the workload needs).
func TestParseHTTPProxyLogs_Blocked(t *testing.T) {
	input := strings.NewReader(`{"level":"info","event_type":"http_blocked","host":"evil.com:443"}`)
	rec := NewRecorder()
	if err := parseHTTPProxyLogs(input, rec); err != nil {
		t.Fatalf("parseHTTPProxyLogs error: %v", err)
	}

	hosts := rec.Hosts()
	if len(hosts) != 1 {
		t.Fatalf("expected 1 host, got %d: %v", len(hosts), hosts)
	}
	if hosts[0] != "evil.com" {
		t.Errorf("expected evil.com (port stripped), got %s", hosts[0])
	}
}

// TestParseProxyLogs_NilReaders verifies that nil readers are handled gracefully.
func TestParseProxyLogs_NilReaders(t *testing.T) {
	rec := NewRecorder()
	if err := ParseProxyLogs(nil, nil, rec); err != nil {
		t.Fatalf("ParseProxyLogs(nil, nil) returned error: %v", err)
	}
	if len(rec.DNSDomains()) != 0 || len(rec.Hosts()) != 0 || len(rec.Repos()) != 0 {
		t.Error("expected empty recorder after nil readers")
	}
}

// TestParseProxyLogs_Combined verifies that DNS and HTTP log readers are both
// processed when provided together.
func TestParseProxyLogs_Combined(t *testing.T) {
	dnsInput := strings.NewReader(`{"level":"info","event_type":"dns_query","domain":"api.github.com","allowed":true}`)
	httpInput := strings.NewReader(`{"level":"info","event_type":"github_repo_allowed","owner":"octocat","repo":"hello"}
{"level":"info","event_type":"github_mitm_connect","host":"registry.npmjs.org:443"}`)

	rec := NewRecorder()
	if err := ParseProxyLogs(dnsInput, httpInput, rec); err != nil {
		t.Fatalf("ParseProxyLogs error: %v", err)
	}

	if len(rec.DNSDomains()) != 1 {
		t.Errorf("expected 1 DNS domain, got %d: %v", len(rec.DNSDomains()), rec.DNSDomains())
	}
	if len(rec.Hosts()) != 1 {
		t.Errorf("expected 1 host, got %d: %v", len(rec.Hosts()), rec.Hosts())
	}
	if len(rec.Repos()) != 1 {
		t.Errorf("expected 1 repo, got %d: %v", len(rec.Repos()), rec.Repos())
	}
}
