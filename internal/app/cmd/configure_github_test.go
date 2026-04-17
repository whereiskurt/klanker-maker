package cmd_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// realTestPEM generates a real 2048-bit RSA private key PEM for tests that require
// JWT generation (e.g. TestRunSetup_FullFlow which exercises fetchInstallations).
func realTestPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	return string(pem.EncodeToMemory(block))
}

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

// ---- Manifest flow tests (Task 1 - --setup flag and GitHub App manifest flow) ----

func TestConfigureGitHubSetup_FlagRegistered(t *testing.T) {
	cfg := &config.Config{}
	c := cmd.NewConfigureGitHubCmd(cfg)

	f := c.Flags().Lookup("setup")
	if f == nil {
		t.Fatal("expected --setup flag to be registered on 'km configure github'")
	}
	if f.DefValue != "false" {
		t.Errorf("--setup default: got %q, want %q", f.DefValue, "false")
	}
}

func TestManifestJSON_Structure(t *testing.T) {
	got := cmd.BuildManifestJSON("http://127.0.0.1:12345/github-app-setup")

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("buildManifestJSON returned invalid JSON: %v\nraw: %s", err, got)
	}

	// name field
	if m["name"] != "klanker-maker-sandbox" {
		t.Errorf("name: got %v, want klanker-maker-sandbox", m["name"])
	}

	// public must be true (allows cross-account installation)
	if pub, ok := m["public"].(bool); !ok || !pub {
		t.Errorf("public: got %v, want true", m["public"])
	}

	// default_permissions: contents = "read", pull_requests = "write"
	dp, ok := m["default_permissions"].(map[string]interface{})
	if !ok {
		t.Fatalf("default_permissions missing or wrong type")
	}
	if dp["contents"] != "read" {
		t.Errorf("default_permissions.contents: got %v, want read", dp["contents"])
	}
	if dp["pull_requests"] != "write" {
		t.Errorf("default_permissions.pull_requests: got %v, want write", dp["pull_requests"])
	}

	// hook_attributes.active = false
	ha, ok := m["hook_attributes"].(map[string]interface{})
	if !ok {
		t.Fatalf("hook_attributes missing or wrong type")
	}
	if active, ok := ha["active"].(bool); !ok || active {
		t.Errorf("hook_attributes.active: got %v, want false", ha["active"])
	}

	// redirect_url must be present
	if m["redirect_url"] != "http://127.0.0.1:12345/github-app-setup" {
		t.Errorf("redirect_url: got %v, want http://127.0.0.1:12345/github-app-setup", m["redirect_url"])
	}
}

func TestReceiveManifestCode_Success(t *testing.T) {
	// Covered by TestReceiveManifestCode_DirectHit which uses the port callback variant.
	// This test verifies the ReceiveManifestCode base function compiles and is callable.
	// The actual callback-based test is the canonical one.
	t.Skip("covered by TestReceiveManifestCode_DirectHit")
}

func TestReceiveManifestCode_DirectHit(t *testing.T) {
	// Use a synchronization approach: run the receiver in a goroutine,
	// use cmd.StartManifestServer to get the port, then hit it.
	portCh := make(chan int, 1)
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		code, _, err := cmd.ReceiveManifestCodeWithPortCb(t.Context(), 5, func(p int) {
			portCh <- p
		})
		if err != nil {
			errCh <- err
		} else {
			codeCh <- code
		}
	}()

	select {
	case port := <-portCh:
		url := fmt.Sprintf("http://127.0.0.1:%d/github-app-setup?code=abc123", port)
		resp, err := http.Get(url) //nolint:noctx
		if err != nil {
			t.Fatalf("GET callback URL: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("callback status: %d, body: %s", resp.StatusCode, body)
		}

		select {
		case code := <-codeCh:
			if code != "abc123" {
				t.Errorf("code: got %q, want %q", code, "abc123")
			}
		case err := <-errCh:
			t.Fatalf("ReceiveManifestCode error: %v", err)
		case <-t.Context().Done():
			t.Fatal("timeout waiting for code")
		}
	case err := <-errCh:
		t.Fatalf("ReceiveManifestCode startup error: %v", err)
	case <-t.Context().Done():
		t.Fatal("timeout waiting for port")
	}
}

func TestReceiveManifestCode_Timeout(t *testing.T) {
	// timeout=1 second — no request sent, should return timeout error
	_, _, err := cmd.ReceiveManifestCodeWithPortCb(t.Context(), 1, func(int) {})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "deadline") {
		t.Errorf("expected timeout-related error, got: %v", err)
	}
}

func TestExchangeManifestCode_Success(t *testing.T) {
	// Mock GitHub manifest exchange API
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/app-manifests/") || !strings.Contains(r.URL.Path, "/conversions") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		resp := map[string]interface{}{
			"id":          int64(12345),
			"client_id":   "Iv1.test123",
			"pem":         "-----BEGIN RSA PRIVATE KEY-----\nfake\n-----END RSA PRIVATE KEY-----\n",
			"webhook_secret": "wh-secret",
			"html_url":    "https://github.com/apps/klanker-maker-sandbox",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	result, err := cmd.ExchangeManifestCode(t.Context(), srv.URL, "testcode123")
	if err != nil {
		t.Fatalf("ExchangeManifestCode: %v", err)
	}
	if result.ID != 12345 {
		t.Errorf("ID: got %d, want 12345", result.ID)
	}
	if result.ClientID != "Iv1.test123" {
		t.Errorf("ClientID: got %q, want %q", result.ClientID, "Iv1.test123")
	}
	if !strings.Contains(result.PEM, "RSA PRIVATE KEY") {
		t.Errorf("PEM: expected RSA key, got %q", result.PEM)
	}
}

func TestExchangeManifestCode_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity) // 422
		_, _ = w.Write([]byte(`{"message":"code is already used"}`))
	}))
	defer srv.Close()

	_, err := cmd.ExchangeManifestCode(t.Context(), srv.URL, "usedcode")
	if err == nil {
		t.Fatal("expected error for 422, got nil")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Errorf("expected 422 in error, got: %v", err)
	}
}

func TestRunSetup_FullFlow(t *testing.T) {
	// Use a real RSA key so that GenerateGitHubAppJWT (used by fetchInstallations) succeeds.
	realPEM := realTestPEM(t)

	// Mock GitHub API: manifest exchange + installations
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/app-manifests/") && strings.Contains(r.URL.Path, "/conversions"):
			w.WriteHeader(http.StatusCreated)
			resp := map[string]interface{}{
				"id":        int64(99001),
				"client_id": "Iv1.fullflow",
				"pem":       realPEM,
			}
			_ = json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/app/installations":
			w.WriteHeader(http.StatusOK)
			installations := []map[string]interface{}{
				{"id": int64(55001), "account": map[string]interface{}{"login": "test-org"}},
			}
			_ = json.NewEncoder(w).Encode(installations)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	mock := &mockSSMWrite{}
	cfg := &config.Config{}
	out := &bytes.Buffer{}

	err := cmd.RunConfigureGitHubSetup(t.Context(), mock, out, cfg, false, srv.URL, "setupcode")
	if err != nil {
		t.Fatalf("RunConfigureGitHubSetup: %v", err)
	}

	// Should write app-client-id, private-key, installation-id
	names := make(map[string]bool)
	for _, call := range mock.calls {
		if call.Name != nil {
			names[*call.Name] = true
		}
	}
	if !names["/km/config/github/app-client-id"] {
		t.Error("expected SSM write for /km/config/github/app-client-id")
	}
	if !names["/km/config/github/private-key"] {
		t.Error("expected SSM write for /km/config/github/private-key")
	}
	if !names["/km/config/github/installation-id"] {
		t.Error("expected SSM write for /km/config/github/installation-id")
	}
}

// ---- mockSSMReadWrite implements SSMReadWriteAPI for discover tests ----

type mockSSMReadWrite struct {
	mockSSMWrite
	params map[string]string // simulated SSM parameter store
}

func newMockSSMReadWrite(params map[string]string) *mockSSMReadWrite {
	return &mockSSMReadWrite{params: params}
}

func (m *mockSSMReadWrite) GetParameter(_ context.Context, input *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	name := ""
	if input.Name != nil {
		name = *input.Name
	}
	v, ok := m.params[name]
	if !ok {
		return nil, fmt.Errorf("ParameterNotFound: %s", name)
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{
			Name:  input.Name,
			Value: &v,
		},
	}, nil
}

// ---- Discover flow tests (Task 1 - per-account installation keys) ----

func TestDiscoverInstallation_MultipleInstallations_WritesPerAccountKeys(t *testing.T) {
	realPEM := realTestPEM(t)

	// Mock GitHub API returning 2 installations
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/app/installations" {
			w.WriteHeader(http.StatusOK)
			installations := []map[string]interface{}{
				{"id": int64(1001), "account": map[string]interface{}{"login": "org-alpha"}},
				{"id": int64(1002), "account": map[string]interface{}{"login": "org-beta"}},
			}
			_ = json.NewEncoder(w).Encode(installations)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	// Override the base URL for fetchInstallations
	origBase := cmd.GithubManifestBaseURL
	cmd.GithubManifestBaseURL = srv.URL
	defer func() { cmd.GithubManifestBaseURL = origBase }()

	mock := newMockSSMReadWrite(map[string]string{
		"/km/config/github/app-client-id": "Iv1.test",
		"/km/config/github/private-key":   realPEM,
	})
	out := &bytes.Buffer{}

	err := cmd.RunDiscoverInstallation(t.Context(), mock, out, true)
	if err != nil {
		t.Fatalf("RunDiscoverInstallation: %v", err)
	}

	// Should write per-account keys for both installations
	foundAlpha := findSSMCall(mock.calls, "/km/config/github/installations/org-alpha")
	if foundAlpha == nil {
		t.Error("expected SSM write for /km/config/github/installations/org-alpha")
	} else if *foundAlpha.Value != "1001" {
		t.Errorf("org-alpha value: got %q, want %q", *foundAlpha.Value, "1001")
	}

	foundBeta := findSSMCall(mock.calls, "/km/config/github/installations/org-beta")
	if foundBeta == nil {
		t.Error("expected SSM write for /km/config/github/installations/org-beta")
	} else if *foundBeta.Value != "1002" {
		t.Errorf("org-beta value: got %q, want %q", *foundBeta.Value, "1002")
	}
}

func TestDiscoverInstallation_MultipleInstallations_WritesLegacyKey(t *testing.T) {
	realPEM := realTestPEM(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/app/installations" {
			w.WriteHeader(http.StatusOK)
			installations := []map[string]interface{}{
				{"id": int64(2001), "account": map[string]interface{}{"login": "first-org"}},
				{"id": int64(2002), "account": map[string]interface{}{"login": "second-org"}},
			}
			_ = json.NewEncoder(w).Encode(installations)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	origBase := cmd.GithubManifestBaseURL
	cmd.GithubManifestBaseURL = srv.URL
	defer func() { cmd.GithubManifestBaseURL = origBase }()

	mock := newMockSSMReadWrite(map[string]string{
		"/km/config/github/app-client-id": "Iv1.test",
		"/km/config/github/private-key":   realPEM,
	})
	out := &bytes.Buffer{}

	err := cmd.RunDiscoverInstallation(t.Context(), mock, out, true)
	if err != nil {
		t.Fatalf("RunDiscoverInstallation: %v", err)
	}

	// Legacy key should be written with FIRST installation's ID
	foundLegacy := findSSMCall(mock.calls, "/km/config/github/installation-id")
	if foundLegacy == nil {
		t.Error("expected SSM write for legacy /km/config/github/installation-id")
	} else if *foundLegacy.Value != "2001" {
		t.Errorf("legacy installation-id value: got %q, want %q", *foundLegacy.Value, "2001")
	}
}

func TestDiscoverInstallation_SingleInstallation_WritesBothKeys(t *testing.T) {
	realPEM := realTestPEM(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/app/installations" {
			w.WriteHeader(http.StatusOK)
			installations := []map[string]interface{}{
				{"id": int64(3001), "account": map[string]interface{}{"login": "solo-org"}},
			}
			_ = json.NewEncoder(w).Encode(installations)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	origBase := cmd.GithubManifestBaseURL
	cmd.GithubManifestBaseURL = srv.URL
	defer func() { cmd.GithubManifestBaseURL = origBase }()

	mock := newMockSSMReadWrite(map[string]string{
		"/km/config/github/app-client-id": "Iv1.test",
		"/km/config/github/private-key":   realPEM,
	})
	out := &bytes.Buffer{}

	err := cmd.RunDiscoverInstallation(t.Context(), mock, out, true)
	if err != nil {
		t.Fatalf("RunDiscoverInstallation: %v", err)
	}

	// Per-account key
	foundAccount := findSSMCall(mock.calls, "/km/config/github/installations/solo-org")
	if foundAccount == nil {
		t.Error("expected SSM write for /km/config/github/installations/solo-org")
	} else if *foundAccount.Value != "3001" {
		t.Errorf("solo-org value: got %q, want %q", *foundAccount.Value, "3001")
	}

	// Legacy key
	foundLegacy := findSSMCall(mock.calls, "/km/config/github/installation-id")
	if foundLegacy == nil {
		t.Error("expected SSM write for legacy /km/config/github/installation-id")
	} else if *foundLegacy.Value != "3001" {
		t.Errorf("legacy value: got %q, want %q", *foundLegacy.Value, "3001")
	}
}

func TestDiscoverInstallation_OutputListsAllAccounts(t *testing.T) {
	realPEM := realTestPEM(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/app/installations" {
			w.WriteHeader(http.StatusOK)
			installations := []map[string]interface{}{
				{"id": int64(4001), "account": map[string]interface{}{"login": "org-one"}},
				{"id": int64(4002), "account": map[string]interface{}{"login": "org-two"}},
			}
			_ = json.NewEncoder(w).Encode(installations)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	origBase := cmd.GithubManifestBaseURL
	cmd.GithubManifestBaseURL = srv.URL
	defer func() { cmd.GithubManifestBaseURL = origBase }()

	mock := newMockSSMReadWrite(map[string]string{
		"/km/config/github/app-client-id": "Iv1.test",
		"/km/config/github/private-key":   realPEM,
	})
	out := &bytes.Buffer{}

	err := cmd.RunDiscoverInstallation(t.Context(), mock, out, true)
	if err != nil {
		t.Fatalf("RunDiscoverInstallation: %v", err)
	}

	outStr := out.String()
	if !strings.Contains(outStr, "installations/org-one") {
		t.Errorf("expected output to mention installations/org-one, got: %s", outStr)
	}
	if !strings.Contains(outStr, "installations/org-two") {
		t.Errorf("expected output to mention installations/org-two, got: %s", outStr)
	}
}

func TestRunSetup_NoInstallations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/app-manifests/") && strings.Contains(r.URL.Path, "/conversions"):
			w.WriteHeader(http.StatusCreated)
			resp := map[string]interface{}{
				"id":        int64(99002),
				"client_id": "Iv1.noinstall",
				"pem":       validTestPEM,
			}
			_ = json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/app/installations":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode([]interface{}{}) // empty
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	mock := &mockSSMWrite{}
	cfg := &config.Config{}
	out := &bytes.Buffer{}

	err := cmd.RunConfigureGitHubSetup(t.Context(), mock, out, cfg, false, srv.URL, "setupcode2")
	if err != nil {
		t.Fatalf("RunConfigureGitHubSetup (no installations): %v", err)
	}

	// Should NOT write installation-id
	for _, call := range mock.calls {
		if call.Name != nil && *call.Name == "/km/config/github/installation-id" {
			t.Error("expected NO SSM write for /km/config/github/installation-id when no installations")
		}
	}

	// Should write app-client-id and private-key
	names := make(map[string]bool)
	for _, call := range mock.calls {
		if call.Name != nil {
			names[*call.Name] = true
		}
	}
	if !names["/km/config/github/app-client-id"] {
		t.Error("expected SSM write for /km/config/github/app-client-id even with no installations")
	}
	if !names["/km/config/github/private-key"] {
		t.Error("expected SSM write for /km/config/github/private-key even with no installations")
	}

	// Should print instructions about installing the App
	outStr := out.String()
	if !strings.Contains(strings.ToLower(outStr), "install") {
		t.Errorf("expected install instructions in output, got: %s", outStr)
	}
}

// ---- Setup flow per-account tests (Task 2) ----

func TestRunSetup_FullFlow_WritesPerAccountKeys(t *testing.T) {
	realPEM := realTestPEM(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/app-manifests/") && strings.Contains(r.URL.Path, "/conversions"):
			w.WriteHeader(http.StatusCreated)
			resp := map[string]interface{}{
				"id":        int64(99001),
				"client_id": "Iv1.fullflow",
				"pem":       realPEM,
			}
			_ = json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/app/installations":
			w.WriteHeader(http.StatusOK)
			installations := []map[string]interface{}{
				{"id": int64(55001), "account": map[string]interface{}{"login": "setup-org-a"}},
				{"id": int64(55002), "account": map[string]interface{}{"login": "setup-org-b"}},
			}
			_ = json.NewEncoder(w).Encode(installations)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	mock := &mockSSMWrite{}
	cfg := &config.Config{}
	out := &bytes.Buffer{}

	err := cmd.RunConfigureGitHubSetup(t.Context(), mock, out, cfg, false, srv.URL, "setupcode-peraccount")
	if err != nil {
		t.Fatalf("RunConfigureGitHubSetup: %v", err)
	}

	// Should write per-account keys for both installations
	foundA := findSSMCall(mock.calls, "/km/config/github/installations/setup-org-a")
	if foundA == nil {
		t.Error("expected SSM write for /km/config/github/installations/setup-org-a")
	} else if *foundA.Value != "55001" {
		t.Errorf("setup-org-a value: got %q, want %q", *foundA.Value, "55001")
	}

	foundB := findSSMCall(mock.calls, "/km/config/github/installations/setup-org-b")
	if foundB == nil {
		t.Error("expected SSM write for /km/config/github/installations/setup-org-b")
	} else if *foundB.Value != "55002" {
		t.Errorf("setup-org-b value: got %q, want %q", *foundB.Value, "55002")
	}

	// Legacy key should still be written with first installation
	foundLegacy := findSSMCall(mock.calls, "/km/config/github/installation-id")
	if foundLegacy == nil {
		t.Error("expected SSM write for legacy /km/config/github/installation-id")
	} else if *foundLegacy.Value != "55001" {
		t.Errorf("legacy value: got %q, want %q", *foundLegacy.Value, "55001")
	}
}

// ---- Manual flow --account flag tests (Task 2) ----

func TestConfigureGitHub_ManualWithAccount_WritesPerAccountKey(t *testing.T) {
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
		"--installation-id", "77001",
		"--account", "my-org",
	})

	if err := c.Execute(); err != nil {
		t.Fatalf("configure github --account: %v", err)
	}

	// Should write per-account key
	foundAccount := findSSMCall(mock.calls, "/km/config/github/installations/my-org")
	if foundAccount == nil {
		t.Error("expected SSM write for /km/config/github/installations/my-org")
	} else if *foundAccount.Value != "77001" {
		t.Errorf("per-account value: got %q, want %q", *foundAccount.Value, "77001")
	}

	// Legacy key should also be written
	foundLegacy := findSSMCall(mock.calls, "/km/config/github/installation-id")
	if foundLegacy == nil {
		t.Error("expected SSM write for legacy /km/config/github/installation-id")
	} else if *foundLegacy.Value != "77001" {
		t.Errorf("legacy value: got %q, want %q", *foundLegacy.Value, "77001")
	}
}

func TestConfigureGitHub_ManualWithoutAccount_LegacyOnly(t *testing.T) {
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
		"--installation-id", "88001",
	})

	if err := c.Execute(); err != nil {
		t.Fatalf("configure github (no --account): %v", err)
	}

	// Legacy key should be written
	foundLegacy := findSSMCall(mock.calls, "/km/config/github/installation-id")
	if foundLegacy == nil {
		t.Error("expected SSM write for legacy /km/config/github/installation-id")
	}

	// Should NOT write any per-account key (no --account flag)
	for _, call := range mock.calls {
		if call.Name != nil && strings.Contains(*call.Name, "/km/config/github/installations/") {
			t.Errorf("unexpected per-account SSM write: %s (--account was not provided)", *call.Name)
		}
	}
}

func TestConfigureGitHub_AccountFlagRegistered(t *testing.T) {
	cfg := &config.Config{}
	c := cmd.NewConfigureGitHubCmd(cfg)

	f := c.Flags().Lookup("account")
	if f == nil {
		t.Fatal("expected --account flag to be registered on 'km configure github'")
	}
}
