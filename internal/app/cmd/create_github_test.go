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
	// pathResults maps the *ssm.GetParametersByPathInput.Path string to a slice of
	// parameters returned by GetParametersByPath. When a path is absent from the
	// map the mock returns an empty slice (legacy fallback path).
	pathResults map[string][]ssmtypes.Parameter
	// pathCallCount counts how many times GetParametersByPath was invoked. Tests
	// that must guarantee the wildcard branch is NOT taken can assert this stays 0.
	pathCallCount int
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

func (m *mockSSMGetPut) GetParametersByPath(ctx context.Context, params *ssm.GetParametersByPathInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	m.pathCallCount++
	path := awssdk.ToString(params.Path)
	out := m.pathResults[path]
	if out == nil {
		out = []ssmtypes.Parameter{}
	}
	return &ssm.GetParametersByPathOutput{Parameters: out}, nil
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

// --- Wildcard-only enumeration tests (Phase quick-6 / GH-FIX-01..05) ---
//
// When allowedRepos contains only wildcards (or bare repos with no owner),
// extractRepoOwner returns "" for every entry, so resolveInstallationID has
// no concrete owner to look up. Pre-fix, this fell straight through to the
// legacy /km/config/github/installation-id key — which is unset on most
// deployments — and silently returned ErrGitHubNotConfigured. The fix
// enumerates /km/config/github/installations/ via GetParametersByPath BEFORE
// the legacy fallback, returning the single ID when exactly one exists,
// surfacing &ErrAmbiguousInstallation{Candidates: ...} when ≥2 exist, and
// only falling through to the legacy key when zero per-owner entries exist.

const installationsPathPrefix = "/km/config/github/installations/"

// TestResolveInstallationID_WildcardOnly_SingleInstallation_ReturnsIt:
// wildcard-only allowedRepos, no legacy key, exactly one per-owner installation
// parameter — should be auto-selected.
func TestResolveInstallationID_WildcardOnly_SingleInstallation_ReturnsIt(t *testing.T) {
	mock := &mockSSMGetPut{
		getResults: map[string]mockSSMResult{
			"/km/config/github/installation-id": {err: paramNotFound("/km/config/github/installation-id")},
		},
		pathResults: map[string][]ssmtypes.Parameter{
			installationsPathPrefix: {
				{
					Name:  awssdk.String("/km/config/github/installations/whereiskurt"),
					Value: awssdk.String("555555"),
				},
			},
		},
	}
	id, err := resolveInstallationID(context.Background(), mock, []string{"*"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "555555" {
		t.Errorf("got %q, want %q", id, "555555")
	}
}

// TestResolveInstallationID_WildcardOnly_MultipleInstallations_ReturnsAmbiguous:
// wildcard-only allowedRepos with two per-owner installation parameters — must
// surface &ErrAmbiguousInstallation with both candidates and a fix-suggestion
// message that mentions BOTH the legacy installation-id key and the
// owner-prefixed allowedRepos workaround.
func TestResolveInstallationID_WildcardOnly_MultipleInstallations_ReturnsAmbiguous(t *testing.T) {
	mock := &mockSSMGetPut{
		// Legacy key intentionally absent from getResults — should not be consulted
		// because enumeration succeeds with multiple candidates first.
		pathResults: map[string][]ssmtypes.Parameter{
			installationsPathPrefix: {
				{
					Name:  awssdk.String("/km/config/github/installations/orgA"),
					Value: awssdk.String("111"),
				},
				{
					Name:  awssdk.String("/km/config/github/installations/orgB"),
					Value: awssdk.String("222"),
				},
			},
		},
	}
	id, err := resolveInstallationID(context.Background(), mock, []string{"*"})
	if err == nil {
		t.Fatalf("expected error, got id=%q", id)
	}
	if id != "" {
		t.Errorf("expected empty id on ambiguity, got %q", id)
	}

	var ambig *ErrAmbiguousInstallation
	if !errors.As(err, &ambig) {
		t.Fatalf("expected *ErrAmbiguousInstallation, got: %v", err)
	}
	if len(ambig.Candidates) != 2 {
		t.Errorf("expected 2 candidates, got %d (%v)", len(ambig.Candidates), ambig.Candidates)
	}
	if ambig.Candidates[0] != "orgA" || ambig.Candidates[1] != "orgB" {
		t.Errorf("expected sorted candidates [orgA orgB], got %v", ambig.Candidates)
	}

	msg := err.Error()
	for _, want := range []string{"orgA", "orgB", "/km/config/github/installation-id", "allowedRepos"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}

// TestResolveInstallationID_WildcardOnly_NoInstallations_LegacySet_ReturnsLegacy:
// wildcard-only allowedRepos, zero per-owner installation parameters, but the
// legacy /km/config/github/installation-id key IS set — preserves existing
// single-installation deployments.
func TestResolveInstallationID_WildcardOnly_NoInstallations_LegacySet_ReturnsLegacy(t *testing.T) {
	mock := &mockSSMGetPut{
		getResults: map[string]mockSSMResult{
			"/km/config/github/installation-id": {value: "legacy-99"},
		},
		pathResults: map[string][]ssmtypes.Parameter{
			installationsPathPrefix: {}, // explicitly empty
		},
	}
	id, err := resolveInstallationID(context.Background(), mock, []string{"*"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "legacy-99" {
		t.Errorf("got %q, want %q", id, "legacy-99")
	}
}

// TestResolveInstallationID_WildcardOnly_NoInstallations_NoLegacy_ReturnsNotConfigured:
// wildcard-only allowedRepos, zero per-owner installations, no legacy key —
// preserves the ErrGitHubNotConfigured contract so the caller still emits the
// quiet "⊘ skipped (not configured)" message.
func TestResolveInstallationID_WildcardOnly_NoInstallations_NoLegacy_ReturnsNotConfigured(t *testing.T) {
	mock := &mockSSMGetPut{
		getResults: map[string]mockSSMResult{
			"/km/config/github/installation-id": {err: paramNotFound("/km/config/github/installation-id")},
		},
		pathResults: map[string][]ssmtypes.Parameter{
			installationsPathPrefix: {},
		},
	}
	_, err := resolveInstallationID(context.Background(), mock, []string{"*"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrGitHubNotConfigured) {
		t.Errorf("expected ErrGitHubNotConfigured, got: %v", err)
	}
}

// TestResolveInstallationID_ConcreteOwner_StillUsesPerOwnerKey_RegressionGuard:
// when allowedRepos contains a concrete owner, the function MUST use the
// per-owner key and MUST NOT call GetParametersByPath. Asserts via
// pathCallCount == 0 on the mock.
func TestResolveInstallationID_ConcreteOwner_StillUsesPerOwnerKey_RegressionGuard(t *testing.T) {
	mock := &mockSSMGetPut{
		getResults: map[string]mockSSMResult{
			"/km/config/github/installations/whereiskurt": {value: "333"},
		},
		// pathResults intentionally nil — concrete-owner branch must not enumerate.
	}
	id, err := resolveInstallationID(context.Background(), mock, []string{"whereiskurt/foo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "333" {
		t.Errorf("got %q, want %q", id, "333")
	}
	if mock.pathCallCount != 0 {
		t.Errorf("expected 0 GetParametersByPath calls on concrete-owner path, got %d", mock.pathCallCount)
	}
}

// TestCreateGitHubCaller_DifferentiatesAmbiguity verifies (source-level) that
// the create.go caller differentiates ErrAmbiguousInstallation from the silent
// "not configured" skip — required for GH-FIX-05.
func TestCreateGitHubCaller_DifferentiatesAmbiguity(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"ErrAmbiguousInstallation type defined or referenced", "ErrAmbiguousInstallation"},
		{"errors.As for typed-error branch", "errors.As(tokenErr,"},
		{"loud warning glyph in caller", "⚠"},
		{"fix-suggestion: legacy installation-id key", "installation-id"},
		{"fix-suggestion: owner-prefixed allowedRepos", "allowedRepos"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
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
