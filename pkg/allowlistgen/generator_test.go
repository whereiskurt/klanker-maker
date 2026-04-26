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

func TestGenerateAnnotatedYAML(t *testing.T) {
	r := buildRecorder(
		[]string{"api.github.com", "codeload.github.com", "pypi.org", "files.pythonhosted.org"},
		[]string{"api.github.com:443", "registry.npmjs.org:443"},
		[]string{"octocat/hello-world"},
	)
	data, err := r.GenerateAnnotatedYAML("")
	if err != nil {
		t.Fatalf("GenerateAnnotatedYAML returned error: %v", err)
	}
	s := string(data)

	// Must contain annotation header.
	if !strings.Contains(s, "--learn-annotate") {
		t.Error("expected --learn-annotate annotation header")
	}

	// Must contain domain count summary.
	if !strings.Contains(s, "Observed:") {
		t.Error("expected 'Observed:' summary line")
	}

	// Must contain suffix with domain mapping.
	if !strings.Contains(s, ".github.com") {
		t.Error("expected .github.com suffix in annotations")
	}
	if !strings.Contains(s, "api.github.com") {
		t.Error("expected api.github.com in domain mapping")
	}

	// Must still be valid YAML after stripping comments.
	var p profile.SandboxProfile
	if err := unmarshalYAML(data, &p); err != nil {
		t.Fatalf("annotated YAML could not be unmarshalled: %v", err)
	}
}

// unmarshalYAML is a test helper to parse YAML into any struct.
func unmarshalYAML(data []byte, v interface{}) error {
	return goyaml.Unmarshal(data, v)
}

// TestRecordAMI_StoresValue verifies that RecordAMI stores the value and AMI() retrieves it.
func TestRecordAMI_StoresValue(t *testing.T) {
	r := allowlistgen.NewRecorder()
	if got := r.AMI(); got != "" {
		t.Errorf("expected empty AMI by default, got %q", got)
	}
	r.RecordAMI("ami-0abc1234")
	if got := r.AMI(); got != "ami-0abc1234" {
		t.Errorf("expected ami-0abc1234, got %q", got)
	}
}

// TestRecordAMI_TrimsAndIgnoresEmpty verifies trimming and empty-string no-op behaviour.
func TestRecordAMI_TrimsAndIgnoresEmpty(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordAMI("  ami-0abc1234  ")
	if got := r.AMI(); got != "ami-0abc1234" {
		t.Errorf("expected trimmed ami-0abc1234, got %q", got)
	}
	// Empty call should not overwrite existing value.
	r.RecordAMI("")
	if got := r.AMI(); got != "ami-0abc1234" {
		t.Errorf("expected previous value preserved after empty RecordAMI, got %q", got)
	}
	// Whitespace-only call should also be ignored.
	r.RecordAMI("   ")
	if got := r.AMI(); got != "ami-0abc1234" {
		t.Errorf("expected previous value preserved after whitespace-only RecordAMI, got %q", got)
	}
}

// TestGenerate_WithAMI verifies that RecordAMI causes Generate to emit Spec.Runtime.AMI.
func TestGenerate_WithAMI(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordAMI("ami-0abc123")
	r.RecordDNSQuery("example.com")

	p, err := r.Generate("")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if p.Spec.Runtime.AMI != "ami-0abc123" {
		t.Errorf("expected Spec.Runtime.AMI=ami-0abc123, got %q", p.Spec.Runtime.AMI)
	}
}

// TestGenerate_WithoutAMI_DoesNotSetField verifies that Generate does not set
// Spec.Runtime.AMI when RecordAMI has not been called (zero value / omitempty).
func TestGenerate_WithoutAMI_DoesNotSetField(t *testing.T) {
	r := allowlistgen.NewRecorder()
	p, err := r.Generate("")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if p.Spec.Runtime.AMI != "" {
		t.Errorf("expected Spec.Runtime.AMI to be empty when not recorded, got %q", p.Spec.Runtime.AMI)
	}
}

// TestGenerateAnnotatedYAML_WithAMI_RoundTripsThroughYAML verifies that the AMI
// value survives marshal/unmarshal through GenerateAnnotatedYAML → profile.Parse.
func TestGenerateAnnotatedYAML_WithAMI_RoundTripsThroughYAML(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordAMI("ami-0abc123")
	r.RecordDNSQuery("example.com")

	data, err := r.GenerateAnnotatedYAML("")
	if err != nil {
		t.Fatalf("GenerateAnnotatedYAML returned error: %v", err)
	}

	var p profile.SandboxProfile
	if err := unmarshalYAML(data, &p); err != nil {
		t.Fatalf("unmarshal failed: %v\nYAML:\n%s", err, string(data))
	}
	if p.Spec.Runtime.AMI != "ami-0abc123" {
		t.Errorf("expected Spec.Runtime.AMI=ami-0abc123 after round-trip, got %q", p.Spec.Runtime.AMI)
	}
}

// TestGenerate_WithAMIAndInitCommands_BothPresent verifies Phase 55 + Phase 56 compatibility:
// RecordAMI and RecordCommand both called, output has both Spec.Runtime.AMI and
// Spec.Execution.InitCommands populated.
func TestGenerate_WithAMIAndInitCommands_BothPresent(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordAMI("ami-0abc123")
	r.RecordCommand("apt install curl")
	r.RecordCommand("go build ./...")

	p, err := r.Generate("")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if p.Spec.Runtime.AMI != "ami-0abc123" {
		t.Errorf("expected Spec.Runtime.AMI=ami-0abc123, got %q", p.Spec.Runtime.AMI)
	}
	if len(p.Spec.Execution.InitCommands) != 2 {
		t.Fatalf("expected 2 InitCommands, got %d: %v", len(p.Spec.Execution.InitCommands), p.Spec.Execution.InitCommands)
	}
	if p.Spec.Execution.InitCommands[0] != "apt install curl" {
		t.Errorf("expected InitCommands[0]=apt install curl, got %q", p.Spec.Execution.InitCommands[0])
	}
	if p.Spec.Execution.InitCommands[1] != "go build ./..." {
		t.Errorf("expected InitCommands[1]=go build ./..., got %q", p.Spec.Execution.InitCommands[1])
	}
}

func TestGenerateWithCommands(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordCommand("apt install curl")
	r.RecordCommand("pip install requests")
	r.RecordCommand("go build ./...")

	p, err := r.Generate("")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	want := []string{"apt install curl", "pip install requests", "go build ./..."}
	got := p.Spec.Execution.InitCommands
	if len(got) != len(want) {
		t.Fatalf("InitCommands: expected %v, got %v", want, got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("InitCommands[%d]: expected %q, got %q", i, w, got[i])
		}
	}
}

func TestGenerateWithoutCommands(t *testing.T) {
	r := allowlistgen.NewRecorder()
	p, err := r.Generate("")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if len(p.Spec.Execution.InitCommands) != 0 {
		t.Errorf("expected nil/empty InitCommands when no commands recorded, got %v", p.Spec.Execution.InitCommands)
	}
}

func TestGenerateAnnotatedYAMLWithCommands(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordCommand("apt install curl")
	r.RecordCommand("pip install requests")

	data, err := r.GenerateAnnotatedYAML("")
	if err != nil {
		t.Fatalf("GenerateAnnotatedYAML returned error: %v", err)
	}
	s := string(data)

	if !strings.Contains(s, "# Commands observed") {
		t.Errorf("expected '# Commands observed' header in annotated output, got:\n%s", s)
	}
	if !strings.Contains(s, "#   apt install curl") {
		t.Errorf("expected 'apt install curl' listed in command annotations, got:\n%s", s)
	}
	if !strings.Contains(s, "#   pip install requests") {
		t.Errorf("expected 'pip install requests' listed in command annotations, got:\n%s", s)
	}
}

func TestGenerateAnnotatedYAMLNoCommandsBlock(t *testing.T) {
	r := allowlistgen.NewRecorder()
	data, err := r.GenerateAnnotatedYAML("")
	if err != nil {
		t.Fatalf("GenerateAnnotatedYAML returned error: %v", err)
	}
	s := string(data)

	if strings.Contains(s, "# Commands observed") {
		t.Errorf("expected no command block when no commands recorded, got:\n%s", s)
	}
}

func TestGenerateAnnotatedYAMLCommandCount(t *testing.T) {
	r := allowlistgen.NewRecorder()
	r.RecordCommand("apt install curl")
	r.RecordCommand("pip install requests")

	data, err := r.GenerateAnnotatedYAML("")
	if err != nil {
		t.Fatalf("GenerateAnnotatedYAML returned error: %v", err)
	}
	s := string(data)

	// Header should contain command count (2 commands)
	if !strings.Contains(s, "2 commands") {
		t.Errorf("expected '2 commands' in header summary, got:\n%s", s)
	}
}
