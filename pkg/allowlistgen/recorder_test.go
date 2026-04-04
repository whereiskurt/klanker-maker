package allowlistgen_test

import (
	"sync"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/allowlistgen"
)

func TestRecorderDNS(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordDNSQuery("api.github.com.")
	domains := r.DNSDomains()
	if len(domains) != 1 || domains[0] != "api.github.com" {
		t.Fatalf("expected [api.github.com], got %v", domains)
	}
}

func TestRecorderDNS_Dedup(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordDNSQuery("api.github.com.")
	r.RecordDNSQuery("api.github.com.")
	r.RecordDNSQuery("API.GITHUB.COM")
	domains := r.DNSDomains()
	if len(domains) != 1 {
		t.Fatalf("expected 1 entry after dedup, got %d: %v", len(domains), domains)
	}
}

func TestRecorderHost(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordHost("api.github.com:443")
	hosts := r.Hosts()
	if len(hosts) != 1 || hosts[0] != "api.github.com" {
		t.Fatalf("expected [api.github.com], got %v", hosts)
	}
}

func TestRecorderHost_NoPort(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordHost("example.com")
	hosts := r.Hosts()
	if len(hosts) != 1 || hosts[0] != "example.com" {
		t.Fatalf("expected [example.com], got %v", hosts)
	}
}

func TestRecorderRepo(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordRepo("octocat/hello-world")
	repos := r.Repos()
	if len(repos) != 1 || repos[0] != "octocat/hello-world" {
		t.Fatalf("expected [octocat/hello-world], got %v", repos)
	}
}

func TestRecorderRepo_Lowercase(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordRepo("Octocat/Hello-World")
	repos := r.Repos()
	if len(repos) != 1 || repos[0] != "octocat/hello-world" {
		t.Fatalf("expected [octocat/hello-world], got %v", repos)
	}
}

func TestRecorderConcurrent(t *testing.T) {
	r := allowlistgen.NewRecorder()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r.RecordDNSQuery("api.github.com.")
			r.RecordHost("api.github.com:443")
			r.RecordRepo("octocat/hello-world")
		}(i)
	}
	wg.Wait()
	if len(r.DNSDomains()) != 1 {
		t.Fatalf("expected 1 DNS domain after concurrent dedup, got %d", len(r.DNSDomains()))
	}
}
