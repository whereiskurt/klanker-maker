package cmd_test

import (
	"bytes"
	"context"
	"encoding/pem"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// mockSSMWrite is a minimal test double for SSMWriteAPI.
// Records all PutParameter calls for assertion.
type mockSSMWrite struct {
	calls []*ssm.PutParameterInput
	err   error
}

func (m *mockSSMWrite) PutParameter(_ context.Context, input *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	m.calls = append(m.calls, input)
	if m.err != nil {
		return nil, m.err
	}
	return &ssm.PutParameterOutput{}, nil
}

// validTestPEM is a minimal RSA PEM that passes pem.Decode but is not a real key.
// configure_github validates PEM decode only (not key validity) so this is sufficient.
var validTestPEM = `-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEA2a2rwplBQLF29amygykEMmYz0+Kcj3bKBp29s8AQWH1Y
AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAQAB
-----END RSA PRIVATE KEY-----
`

func TestConfigureGitHubCmd_CommandShape(t *testing.T) {
	cfg := &config.Config{}
	c := cmd.NewConfigureGitHubCmd(cfg)

	if c == nil {
		t.Fatal("NewConfigureGitHubCmd returned nil")
	}
	if c.Use != "github" {
		t.Errorf("Use: got %q, want %q", c.Use, "github")
	}
}

func TestConfigureGitHubCmd_FlagRegistration(t *testing.T) {
	cfg := &config.Config{}
	c := cmd.NewConfigureGitHubCmd(cfg)

	flags := []string{"app-client-id", "private-key-file", "installation-id", "non-interactive", "force"}
	for _, f := range flags {
		if c.Flags().Lookup(f) == nil {
			t.Errorf("expected flag --%s to be registered", f)
		}
	}
}

func TestConfigureGitHubCmd_WritesClientIDToSSM(t *testing.T) {
	cfg := &config.Config{}
	mock := &mockSSMWrite{}

	pemFile := writeTempPEM(t, validTestPEM)

	in := bytes.NewReader(nil)
	out := &bytes.Buffer{}
	c := cmd.NewConfigureGitHubCmdWithDeps(cfg, mock, in, out)
	c.SetArgs([]string{
		"--non-interactive",
		"--app-client-id", "Iv1.abc123",
		"--private-key-file", pemFile,
		"--installation-id", "99887766",
	})

	if err := c.Execute(); err != nil {
		t.Fatalf("configure github: %v", err)
	}

	// Must write /km/config/github/app-client-id as String
	found := findSSMCall(mock.calls, "/km/config/github/app-client-id")
	if found == nil {
		t.Fatal("expected PutParameter call for /km/config/github/app-client-id")
	}
	if string(found.Type) != "String" {
		t.Errorf("app-client-id type: got %q, want %q", found.Type, "String")
	}
	if *found.Value != "Iv1.abc123" {
		t.Errorf("app-client-id value: got %q, want %q", *found.Value, "Iv1.abc123")
	}
}

func TestConfigureGitHubCmd_WritesPrivateKeyAsSecureString(t *testing.T) {
	cfg := &config.Config{}
	mock := &mockSSMWrite{}

	pemFile := writeTempPEM(t, validTestPEM)

	in := bytes.NewReader(nil)
	out := &bytes.Buffer{}
	c := cmd.NewConfigureGitHubCmdWithDeps(cfg, mock, in, out)
	c.SetArgs([]string{
		"--non-interactive",
		"--app-client-id", "Iv1.abc123",
		"--private-key-file", pemFile,
		"--installation-id", "99887766",
	})

	if err := c.Execute(); err != nil {
		t.Fatalf("configure github: %v", err)
	}

	// Must write /km/config/github/private-key as SecureString
	found := findSSMCall(mock.calls, "/km/config/github/private-key")
	if found == nil {
		t.Fatal("expected PutParameter call for /km/config/github/private-key")
	}
	if string(found.Type) != "SecureString" {
		t.Errorf("private-key type: got %q, want SecureString", found.Type)
	}
	if *found.Value != validTestPEM {
		t.Errorf("private-key value mismatch")
	}
}

func TestConfigureGitHubCmd_WritesInstallationIDToSSM(t *testing.T) {
	cfg := &config.Config{}
	mock := &mockSSMWrite{}

	pemFile := writeTempPEM(t, validTestPEM)

	in := bytes.NewReader(nil)
	out := &bytes.Buffer{}
	c := cmd.NewConfigureGitHubCmdWithDeps(cfg, mock, in, out)
	c.SetArgs([]string{
		"--non-interactive",
		"--app-client-id", "Iv1.abc123",
		"--private-key-file", pemFile,
		"--installation-id", "99887766",
	})

	if err := c.Execute(); err != nil {
		t.Fatalf("configure github: %v", err)
	}

	// Must write /km/config/github/installation-id as String
	found := findSSMCall(mock.calls, "/km/config/github/installation-id")
	if found == nil {
		t.Fatal("expected PutParameter call for /km/config/github/installation-id")
	}
	if string(found.Type) != "String" {
		t.Errorf("installation-id type: got %q, want String", found.Type)
	}
	if *found.Value != "99887766" {
		t.Errorf("installation-id value: got %q, want 99887766", *found.Value)
	}
}

func TestConfigureGitHubCmd_RejectsInvalidPEM(t *testing.T) {
	cfg := &config.Config{}
	mock := &mockSSMWrite{}

	// Write a non-PEM file
	f, err := os.CreateTemp(t.TempDir(), "bad-key-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("this is not a PEM file")
	_ = f.Close()

	in := bytes.NewReader(nil)
	out := &bytes.Buffer{}
	c := cmd.NewConfigureGitHubCmdWithDeps(cfg, mock, in, out)
	c.SetArgs([]string{
		"--non-interactive",
		"--app-client-id", "Iv1.abc123",
		"--private-key-file", f.Name(),
		"--installation-id", "99887766",
	})

	err = c.Execute()
	if err == nil {
		t.Fatal("expected error for invalid PEM, got nil")
	}
	if len(mock.calls) != 0 {
		t.Errorf("expected no SSM writes for invalid PEM, got %d", len(mock.calls))
	}
}

func TestConfigureGitHubCmd_ForceFlag(t *testing.T) {
	cfg := &config.Config{}
	mock := &mockSSMWrite{}

	pemFile := writeTempPEM(t, validTestPEM)

	in := bytes.NewReader(nil)
	out := &bytes.Buffer{}
	c := cmd.NewConfigureGitHubCmdWithDeps(cfg, mock, in, out)
	c.SetArgs([]string{
		"--non-interactive",
		"--app-client-id", "Iv1.abc123",
		"--private-key-file", pemFile,
		"--installation-id", "99887766",
		"--force",
	})

	if err := c.Execute(); err != nil {
		t.Fatalf("configure github --force: %v", err)
	}

	// All writes should have Overwrite=true when --force is set
	for _, call := range mock.calls {
		if call.Overwrite == nil || !*call.Overwrite {
			t.Errorf("expected Overwrite=true for all calls with --force, got false for %s", *call.Name)
		}
	}
}

func TestConfigureGitHubCmd_NonInteractiveMissingFlags(t *testing.T) {
	cfg := &config.Config{}
	mock := &mockSSMWrite{}

	in := bytes.NewReader(nil)
	out := &bytes.Buffer{}
	c := cmd.NewConfigureGitHubCmdWithDeps(cfg, mock, in, out)
	c.SetArgs([]string{
		"--non-interactive",
		// missing: --app-client-id, --private-key-file, --installation-id
	})

	err := c.Execute()
	if err == nil {
		t.Fatal("expected error for missing required --non-interactive flags, got nil")
	}
}

func TestConfigureGitHubCmd_RegisteredUnderConfigure(t *testing.T) {
	km := buildKM(t)

	// km configure github --help should succeed
	out, err := runKMArgs(km, "", "configure", "github", "--help")
	if err != nil {
		t.Fatalf("km configure github --help: %v\noutput: %s", err, out)
	}

	lc := strings.ToLower(out)
	if !strings.Contains(lc, "github") {
		t.Errorf("expected 'github' in configure github --help output; got: %s", out)
	}
}

// ---- helpers ----

// findSSMCall searches calls for a PutParameter with the given name.
func findSSMCall(calls []*ssm.PutParameterInput, name string) *ssm.PutParameterInput {
	for _, c := range calls {
		if c.Name != nil && *c.Name == name {
			return c
		}
	}
	return nil
}

// writeTempPEM writes PEM content to a temp file and returns its path.
func writeTempPEM(t *testing.T, content string) string {
	t.Helper()
	// Validate the PEM content is decodable before writing
	block, _ := pem.Decode([]byte(content))
	if block == nil {
		t.Fatal("writeTempPEM: test setup error — PEM content is not decodable")
	}
	f, err := os.CreateTemp(t.TempDir(), "test-key-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(content)
	_ = f.Close()
	return f.Name()
}

// Ensure the cmd.NewConfigureGitHubCmdWithDeps function signature is used correctly.
// This compile-time check ensures the exported DI constructor exists.
var _ *cobra.Command = func() *cobra.Command {
	cfg := &config.Config{}
	mock := &mockSSMWrite{}
	return cmd.NewConfigureGitHubCmdWithDeps(cfg, mock, nil, nil)
}()
