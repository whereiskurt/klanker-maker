package cmd

// TestCreateGitHubSkip tests the graceful skip behavior of generateAndStoreGitHubToken
// when SSM parameters for the GitHub App are not configured.
//
// Uses an internal test (package cmd) so it can access unexported functions.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// mockSSMGetPut is a test double for SSMGetPutAPI.
type mockSSMGetPut struct {
	// getResults maps parameter name to (output, error).
	getResults map[string]mockSSMResult
}

type mockSSMResult struct {
	value string
	err   error
}

func (m *mockSSMGetPut) GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	name := awssdk.ToString(params.Name)
	res, ok := m.getResults[name]
	if !ok {
		return nil, fmt.Errorf("unexpected SSM GetParameter call for %q", name)
	}
	if res.err != nil {
		return nil, res.err
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{Value: awssdk.String(res.value)},
	}, nil
}

func (m *mockSSMGetPut) PutParameter(ctx context.Context, params *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	// Always succeed in tests unless we need to test write failures.
	return &ssm.PutParameterOutput{}, nil
}

// paramNotFound builds a ParameterNotFound error for the given parameter name.
func paramNotFound(name string) error {
	return &ssmtypes.ParameterNotFound{
		Message: awssdk.String(fmt.Sprintf("Parameter %s not found.", name)),
	}
}

// TestCreateGitHubSkip_AppClientIDNotFound verifies that generateAndStoreGitHubToken
// returns ErrGitHubNotConfigured when app-client-id SSM param is missing.
func TestCreateGitHubSkip_AppClientIDNotFound(t *testing.T) {
	mock := &mockSSMGetPut{
		getResults: map[string]mockSSMResult{
			"/km/config/github/app-client-id": {err: paramNotFound("/km/config/github/app-client-id")},
		},
	}

	_, err := generateAndStoreGitHubToken(context.Background(), mock, "sb-test", "alias/km-platform", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrGitHubNotConfigured) {
		t.Errorf("expected ErrGitHubNotConfigured, got: %v", err)
	}
}

// TestCreateGitHubSkip_InstallationIDNotFound verifies that generateAndStoreGitHubToken
// returns ErrGitHubNotConfigured when installation-id SSM param is missing.
func TestCreateGitHubSkip_InstallationIDNotFound(t *testing.T) {
	mock := &mockSSMGetPut{
		getResults: map[string]mockSSMResult{
			"/km/config/github/app-client-id":   {value: "123456"},
			"/km/config/github/private-key":     {value: "not-a-real-key"},
			"/km/config/github/installation-id": {err: paramNotFound("/km/config/github/installation-id")},
		},
	}

	_, err := generateAndStoreGitHubToken(context.Background(), mock, "sb-test", "alias/km-platform", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrGitHubNotConfigured) {
		t.Errorf("expected ErrGitHubNotConfigured, got: %v", err)
	}
}

// TestCreateGitHubSkip_AccessDeniedIsNotSkipped verifies that non-ParameterNotFound
// SSM errors are propagated as-is (not converted to ErrGitHubNotConfigured).
func TestCreateGitHubSkip_AccessDeniedIsNotSkipped(t *testing.T) {
	accessDenied := fmt.Errorf("AccessDeniedException: User not authorized to read parameter")
	mock := &mockSSMGetPut{
		getResults: map[string]mockSSMResult{
			"/km/config/github/app-client-id": {err: accessDenied},
		},
	}

	_, err := generateAndStoreGitHubToken(context.Background(), mock, "sb-test", "alias/km-platform", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrGitHubNotConfigured) {
		t.Error("AccessDenied should NOT return ErrGitHubNotConfigured — it is not a 'not configured' case")
	}
}

// TestCreateGitHubSkip_CallerPrintsSkipMessage verifies the "skipped (not configured)"
// message appears in create.go's caller (source-level).
func TestCreateGitHubSkip_CallerPrintsSkipMessage(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"ErrGitHubNotConfigured sentinel defined", "ErrGitHubNotConfigured"},
		{"errors.Is check in caller", "errors.Is(tokenErr, ErrGitHubNotConfigured)"},
		{"skipped (not configured) message", "skipped (not configured)"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

// TestCreateGitHubSkip_NonFatalPreserved verifies that non-ErrGitHubNotConfigured
// errors are still logged as warnings in the caller (source-level).
func TestCreateGitHubSkip_NonFatalPreserved(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	if !strings.Contains(s, "GitHub App token generation failed (non-fatal") {
		t.Error("create.go: non-fatal warn log for GitHub token errors not found")
	}
}

// --- Tests for extractRepoOwner ---

func TestExtractRepoOwner(t *testing.T) {
	tests := []struct {
		repo string
		want string
	}{
		{"userA/my-repo", "userA"},
		{"orgB/other-repo", "orgB"},
		{"bare-repo", ""},
		{"*", ""},
		{"", ""},
		{"owner/repo/extra", "owner"},
	}
	for _, tc := range tests {
		got := extractRepoOwner(tc.repo)
		if got != tc.want {
			t.Errorf("extractRepoOwner(%q) = %q, want %q", tc.repo, got, tc.want)
		}
	}
}

// --- Tests for resolveInstallationID ---

func TestResolveInstallationID_PerAccountFound(t *testing.T) {
	mock := &mockSSMGetPut{
		getResults: map[string]mockSSMResult{
			"/km/config/github/installations/userA": {value: "12345678"},
		},
	}
	id, err := resolveInstallationID(context.Background(), mock, []string{"userA/repo1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "12345678" {
		t.Errorf("got %q, want %q", id, "12345678")
	}
}

func TestResolveInstallationID_FallbackToLegacy(t *testing.T) {
	mock := &mockSSMGetPut{
		getResults: map[string]mockSSMResult{
			"/km/config/github/installations/userA": {err: paramNotFound("/km/config/github/installations/userA")},
			"/km/config/github/installation-id":     {value: "99999"},
		},
	}
	id, err := resolveInstallationID(context.Background(), mock, []string{"userA/repo1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "99999" {
		t.Errorf("got %q, want %q", id, "99999")
	}
}

func TestResolveInstallationID_BothMissing(t *testing.T) {
	mock := &mockSSMGetPut{
		getResults: map[string]mockSSMResult{
			"/km/config/github/installations/userA": {err: paramNotFound("/km/config/github/installations/userA")},
			"/km/config/github/installation-id":     {err: paramNotFound("/km/config/github/installation-id")},
		},
	}
	_, err := resolveInstallationID(context.Background(), mock, []string{"userA/repo1"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrGitHubNotConfigured) {
		t.Errorf("expected ErrGitHubNotConfigured, got: %v", err)
	}
}

func TestResolveInstallationID_BareReposFallbackToLegacy(t *testing.T) {
	mock := &mockSSMGetPut{
		getResults: map[string]mockSSMResult{
			"/km/config/github/installation-id": {value: "legacy-id"},
		},
	}
	id, err := resolveInstallationID(context.Background(), mock, []string{"bare-repo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "legacy-id" {
		t.Errorf("got %q, want %q", id, "legacy-id")
	}
}

func TestResolveInstallationID_WildcardFallbackToLegacy(t *testing.T) {
	mock := &mockSSMGetPut{
		getResults: map[string]mockSSMResult{
			"/km/config/github/installation-id": {value: "legacy-id"},
		},
	}
	id, err := resolveInstallationID(context.Background(), mock, []string{"*"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "legacy-id" {
		t.Errorf("got %q, want %q", id, "legacy-id")
	}
}

func TestResolveInstallationID_MixedOwnersUsesFirst(t *testing.T) {
	mock := &mockSSMGetPut{
		getResults: map[string]mockSSMResult{
			"/km/config/github/installations/orgA": {value: "11111"},
		},
	}
	id, err := resolveInstallationID(context.Background(), mock, []string{"orgA/repo1", "orgB/repo2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "11111" {
		t.Errorf("got %q, want %q", id, "11111")
	}
}

func TestResolveInstallationID_NilReposFallbackToLegacy(t *testing.T) {
	mock := &mockSSMGetPut{
		getResults: map[string]mockSSMResult{
			"/km/config/github/installation-id": {value: "legacy-id"},
		},
	}
	id, err := resolveInstallationID(context.Background(), mock, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "legacy-id" {
		t.Errorf("got %q, want %q", id, "legacy-id")
	}
}

// TestGenerateAndStoreGitHubToken_UsesPerAccountSSMKey verifies that
// generateAndStoreGitHubToken calls resolveInstallationID (source-level check).
func TestGenerateAndStoreGitHubToken_UsesPerAccountSSMKey(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"resolveInstallationID call", "resolveInstallationID("},
		{"per-account SSM path", "installations/"},
		{"extractRepoOwner helper", "extractRepoOwner("},
		{"installation ID injected into HCL", "resolvedInstallationID"},
		{"HCL string replacement", `installation_id      = ""`},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}
