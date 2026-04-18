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

func TestRecordCommand_Basic(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordCommand("apt install curl")
	r.RecordCommand("pip install requests")
	r.RecordCommand("go build ./...")
	cmds := r.Commands()
	want := []string{"apt install curl", "pip install requests", "go build ./..."}
	if len(cmds) != len(want) {
		t.Fatalf("expected %d commands, got %d: %v", len(want), len(cmds), cmds)
	}
	for i, w := range want {
		if cmds[i] != w {
			t.Errorf("command[%d]: expected %q, got %q", i, w, cmds[i])
		}
	}
}

func TestRecordCommand_Dedup(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordCommand("apt install curl")
	r.RecordCommand("apt install curl")
	cmds := r.Commands()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command after dedup, got %d: %v", len(cmds), cmds)
	}
	if cmds[0] != "apt install curl" {
		t.Errorf("expected 'apt install curl', got %q", cmds[0])
	}
}

func TestRecordCommand_Empty(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordCommand("")
	r.RecordCommand("   ")
	r.RecordCommand("\t")
	cmds := r.Commands()
	if len(cmds) != 0 {
		t.Fatalf("expected no commands from empty/whitespace inputs, got %d: %v", len(cmds), cmds)
	}
}

func TestRecordCommand_OrderPreserved(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordCommand("cmd A")
	r.RecordCommand("cmd B")
	r.RecordCommand("cmd A") // duplicate — should be ignored
	cmds := r.Commands()
	want := []string{"cmd A", "cmd B"}
	if len(cmds) != len(want) {
		t.Fatalf("expected %v, got %v", want, cmds)
	}
	for i, w := range want {
		if cmds[i] != w {
			t.Errorf("command[%d]: expected %q, got %q", i, w, cmds[i])
		}
	}
}

func TestRecordCommand_EmptySliceNotNil(t *testing.T) {
	r := allowlistgen.NewRecorder()
	cmds := r.Commands()
	if cmds == nil {
		t.Fatal("Commands() should return empty slice, not nil")
	}
	if len(cmds) != 0 {
		t.Fatalf("expected 0 commands, got %d", len(cmds))
	}
}

func TestRecordCommand_Concurrent(t *testing.T) {
	r := allowlistgen.NewRecorder()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.RecordCommand("apt install curl")
		}()
	}
	wg.Wait()
	cmds := r.Commands()
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command after concurrent dedup, got %d: %v", len(cmds), cmds)
	}
}
