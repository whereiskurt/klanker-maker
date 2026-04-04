package allowlistgen_test

import (
	"strings"
	"testing"
	"time"

	goyaml "github.com/goccy/go-yaml"
	"github.com/whereiskurt/klankrmkr/pkg/allowlistgen"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// buildRecorder creates a recorder with specified observations.
// dns entries are recorded via RecordDNSQuery (plain domain, no trailing dot handled separately),
// hosts include port ("host:443"), repos as "owner/repo".
func buildRecorder(dns, hosts, repos []string) *allowlistgen.Recorder {
	r := allowlistgen.NewRecorder()
	for _, d := range dns {
		r.RecordDNSQuery(d)
	}
	for _, h := range hosts {
		r.RecordHost(h)
	}
	for _, repo := range repos {
		r.RecordRepo(repo)
	}
	return r
}

// TestGenerate_Basic verifies the core deduplication and DNS suffix logic.
// api.github.com and pypi.org produce suffixes .github.com and .pypi.org.
// registry.npmjs.org is NOT covered by any suffix (npmjs.org is its eTLD+1
// → suffix .npmjs.org, and registry.npmjs.org ends with .npmjs.org, so it IS
// covered). api.github.com IS covered by .github.com.
// Per HTTPS-implies-DNS: every host also implies its DNS suffix.
func TestGenerate_Basic(t *testing.T) {
	r := buildRecorder(
		[]string{"api.github.com", "pypi.org"},
		[]string{"api.github.com:443", "registry.npmjs.org:443"},
		[]string{"octocat/hello-world"},
	)
	p, err := r.Generate("")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	// DNS suffixes from: dns domains + hosts
	// api.github.com → .github.com
	// pypi.org → .pypi.org
	// registry.npmjs.org → .npmjs.org
	suffixes := p.Spec.Network.Egress.AllowedDNSSuffixes
	wantSuffixes := []string{".github.com", ".npmjs.org", ".pypi.org"}
	if len(suffixes) != len(wantSuffixes) {
		t.Errorf("AllowedDNSSuffixes: expected %v, got %v", wantSuffixes, suffixes)
	}
	for i, w := range wantSuffixes {
		if i < len(suffixes) && suffixes[i] != w {
			t.Errorf("suffix[%d]: expected %q, got %q", i, w, suffixes[i])
		}
	}

	// Hosts: api.github.com covered by .github.com → omitted.
	// registry.npmjs.org covered by .npmjs.org → omitted.
	hosts := p.Spec.Network.Egress.AllowedHosts
	if len(hosts) != 0 {
		t.Errorf("AllowedHosts: expected empty (all covered by suffixes), got %v", hosts)
	}

	// Repos
	repos := p.Spec.SourceAccess.GitHub.AllowedRepos
	if len(repos) != 1 || repos[0] != "octocat/hello-world" {
		t.Errorf("AllowedRepos: expected [octocat/hello-world], got %v", repos)
	}
}

func TestGenerate_Empty(t *testing.T) {
	r := allowlistgen.NewRecorder()
	p, err := r.Generate("")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if p.Spec.Network.Egress.AllowedDNSSuffixes == nil {
		t.Error("AllowedDNSSuffixes should not be nil")
	}
	if p.Spec.Network.Egress.AllowedHosts == nil {
		t.Error("AllowedHosts should not be nil")
	}
	if len(p.Spec.Network.Egress.AllowedDNSSuffixes) != 0 {
		t.Errorf("expected empty AllowedDNSSuffixes, got %v", p.Spec.Network.Egress.AllowedDNSSuffixes)
	}
	if len(p.Spec.Network.Egress.AllowedHosts) != 0 {
		t.Errorf("expected empty AllowedHosts, got %v", p.Spec.Network.Egress.AllowedHosts)
	}
}

func TestGenerate_WithBase(t *testing.T) {
	r := allowlistgen.NewRecorder()
	p, err := r.Generate("restricted-dev")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if p.Extends != "restricted-dev" {
		t.Errorf("expected Extends=restricted-dev, got %q", p.Extends)
	}
}

func TestGenerate_MetadataName(t *testing.T) {
	r := allowlistgen.NewRecorder()
	p, err := r.Generate("")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if !strings.HasPrefix(p.Metadata.Name, "observed-") {
		t.Errorf("expected name to start with observed-, got %q", p.Metadata.Name)
	}
	// Verify date-like pattern: observed-YYYYMMDD-HHMMSS
	name := p.Metadata.Name[len("observed-"):]
	_, err = time.Parse("20060102-150405", name)
	if err != nil {
		t.Errorf("metadata name does not contain date pattern: %q, err: %v", name, err)
	}
}

func TestGenerateYAML_Valid(t *testing.T) {
	r := buildRecorder(
		[]string{"api.github.com"},
		[]string{"api.github.com:443"},
		nil,
	)
	data, err := r.GenerateYAML("")
	if err != nil {
		t.Fatalf("GenerateYAML returned error: %v", err)
	}
	// Must be non-empty and parseable as SandboxProfile
	var p profile.SandboxProfile
	if err := unmarshalYAML(data, &p); err != nil {
		t.Fatalf("GenerateYAML output could not be unmarshalled: %v\nYAML:\n%s", err, string(data))
	}
	if p.APIVersion != "klankermaker.ai/v1alpha1" {
		t.Errorf("expected apiVersion klankermaker.ai/v1alpha1, got %q", p.APIVersion)
	}
}

func TestGenerate_HostDedup(t *testing.T) {
	r := buildRecorder(
		[]string{"foo.example.com", "bar.example.com"},
		[]string{"foo.example.com", "baz.other.com"},
		nil,
	)
	p, err := r.Generate("")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	// DNS suffixes from DNS domains + hosts:
	// foo.example.com, bar.example.com → .example.com
	// foo.example.com (host) → .example.com  (already there)
	// baz.other.com (host) → .other.com
	wantSuffixes := []string{".example.com", ".other.com"}
	if len(p.Spec.Network.Egress.AllowedDNSSuffixes) != len(wantSuffixes) {
		t.Errorf("suffixes: expected %v, got %v", wantSuffixes, p.Spec.Network.Egress.AllowedDNSSuffixes)
	}
	// All hosts covered by suffixes → empty
	if len(p.Spec.Network.Egress.AllowedHosts) != 0 {
		t.Errorf("AllowedHosts: expected empty, got %v", p.Spec.Network.Egress.AllowedHosts)
	}
}

func TestGenerateValidates(t *testing.T) {
	r := buildRecorder(
		[]string{"api.github.com"},
		[]string{"api.github.com:443"},
		[]string{"octocat/hello-world"},
	)
	data, err := r.GenerateYAML("")
	if err != nil {
		t.Fatalf("GenerateYAML returned error: %v", err)
	}
	errs := profile.Validate(data)
	if len(errs) > 0 {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		t.Errorf("profile.Validate failed:\n%s\nYAML:\n%s", strings.Join(msgs, "\n"), string(data))
	}
}

func TestGenerateYAMLHeader(t *testing.T) {
	r := allowlistgen.NewRecorder()
	data, err := r.GenerateYAML("")
	if err != nil {
		t.Fatalf("GenerateYAML returned error: %v", err)
	}
	if !strings.Contains(string(data), "Generated by km observe") {
		t.Errorf("expected header comment in YAML output, got:\n%s", string(data))
	}
}

// unmarshalYAML is a test helper to parse YAML into any struct.
func unmarshalYAML(data []byte, v interface{}) error {
	return goyaml.Unmarshal(data, v)
}
