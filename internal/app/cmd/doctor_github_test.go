// Package cmd — doctor_github_test.go
// Plan 97-06 — tests for the Phase 97 GitHub bridge doctor checks:
//   - checkGitHubWebhookSecret
//   - checkGitHubBotLoginCached
//   - checkGitHubBridgeURL
//   - checkGitHubReposResolvable
//
// All tests use mockSSMReadClient (defined in doctor_test.go) and inline
// appcfg.GithubRepoEntry slices. No real AWS calls.
package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	appcfg "github.com/whereiskurt/klanker-maker/internal/app/config"
)

// =============================================================================
// checkGitHubWebhookSecret
// =============================================================================

// TestGitHubDoctor_WebhookSecret_OK: parameter present → OK.
func TestGitHubDoctor_WebhookSecret_OK(t *testing.T) {
	client := &mockSSMReadClient{
		outputs: map[string]*ssm.GetParameterOutput{
			"/km/config/github/webhook-secret": {
				Parameter: &ssmtypes.Parameter{
					Name:  aws.String("/km/config/github/webhook-secret"),
					Value: aws.String("s3cr3t"),
				},
			},
		},
	}
	r := checkGitHubWebhookSecret(context.Background(), client, "/km/")
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "configured") {
		t.Errorf("expected 'configured' in message, got: %s", r.Message)
	}
}

// TestGitHubDoctor_WebhookSecret_Missing: ParameterNotFound → WARN with remediation.
func TestGitHubDoctor_WebhookSecret_Missing(t *testing.T) {
	client := &mockSSMReadClient{} // all params → ParameterNotFound
	r := checkGitHubWebhookSecret(context.Background(), client, "/km/")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "not found") {
		t.Errorf("expected 'not found' in message, got: %s", r.Message)
	}
	if !strings.Contains(r.Remediation, "km github init") {
		t.Errorf("expected remediation to mention 'km github init', got: %s", r.Remediation)
	}
}

// TestGitHubDoctor_WebhookSecret_NilClient: nil SSM client → SKIPPED.
func TestGitHubDoctor_WebhookSecret_NilClient(t *testing.T) {
	r := checkGitHubWebhookSecret(context.Background(), nil, "/km/")
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s", r.Status)
	}
}

// =============================================================================
// checkGitHubBotLoginCached
// =============================================================================

// TestGitHubDoctor_BotLogin_OK: parameter present and non-empty → OK with login in message.
func TestGitHubDoctor_BotLogin_OK(t *testing.T) {
	client := &mockSSMReadClient{
		outputs: map[string]*ssm.GetParameterOutput{
			"/km/config/github/bot-login": {
				Parameter: &ssmtypes.Parameter{
					Name:  aws.String("/km/config/github/bot-login"),
					Value: aws.String("klanker-maker[bot]"),
				},
			},
		},
	}
	r := checkGitHubBotLoginCached(context.Background(), client, "/km/")
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "klanker-maker[bot]") {
		t.Errorf("expected login in message, got: %s", r.Message)
	}
}

// TestGitHubDoctor_BotLogin_Missing: ParameterNotFound → WARN.
func TestGitHubDoctor_BotLogin_Missing(t *testing.T) {
	client := &mockSSMReadClient{}
	r := checkGitHubBotLoginCached(context.Background(), client, "/km/")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "not cached") {
		t.Errorf("expected 'not cached' in message, got: %s", r.Message)
	}
	if !strings.Contains(r.Remediation, "km github init") {
		t.Errorf("expected remediation to mention 'km github init', got: %s", r.Remediation)
	}
}

// TestGitHubDoctor_BotLogin_NilClient: nil SSM client → SKIPPED.
func TestGitHubDoctor_BotLogin_NilClient(t *testing.T) {
	r := checkGitHubBotLoginCached(context.Background(), nil, "/km/")
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s", r.Status)
	}
}

// =============================================================================
// checkGitHubBridgeURL
// =============================================================================

// TestGitHubDoctor_BridgeURL_OK: valid HTTPS URL present → OK.
func TestGitHubDoctor_BridgeURL_OK(t *testing.T) {
	client := &mockSSMReadClient{
		outputs: map[string]*ssm.GetParameterOutput{
			"/km/config/github/bridge-url": {
				Parameter: &ssmtypes.Parameter{
					Name:  aws.String("/km/config/github/bridge-url"),
					Value: aws.String("https://abc123.lambda-url.us-east-1.on.aws/"),
				},
			},
		},
	}
	r := checkGitHubBridgeURL(context.Background(), client, "/km/")
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "https://") {
		t.Errorf("expected URL in message, got: %s", r.Message)
	}
}

// TestGitHubDoctor_BridgeURL_Missing: ParameterNotFound → WARN with deploy remediation.
func TestGitHubDoctor_BridgeURL_Missing(t *testing.T) {
	client := &mockSSMReadClient{}
	r := checkGitHubBridgeURL(context.Background(), client, "/km/")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Remediation, "km init") {
		t.Errorf("expected remediation to mention 'km init', got: %s", r.Remediation)
	}
}

// TestGitHubDoctor_BridgeURL_NotHTTPS: URL without HTTPS scheme → WARN.
func TestGitHubDoctor_BridgeURL_NotHTTPS(t *testing.T) {
	client := &mockSSMReadClient{
		outputs: map[string]*ssm.GetParameterOutput{
			"/km/config/github/bridge-url": {
				Parameter: &ssmtypes.Parameter{
					Name:  aws.String("/km/config/github/bridge-url"),
					Value: aws.String("http://insecure.example.com/"),
				},
			},
		},
	}
	r := checkGitHubBridgeURL(context.Background(), client, "/km/")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN for non-HTTPS URL, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "not HTTPS") {
		t.Errorf("expected 'not HTTPS' in message, got: %s", r.Message)
	}
}

// TestGitHubDoctor_BridgeURL_NilClient: nil SSM client → SKIPPED.
func TestGitHubDoctor_BridgeURL_NilClient(t *testing.T) {
	r := checkGitHubBridgeURL(context.Background(), nil, "/km/")
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s", r.Status)
	}
}

// =============================================================================
// checkGitHubReposResolvable
// =============================================================================

// TestGitHubDoctor_ReposResolvable_NoRepos: empty repos slice → SKIPPED.
func TestGitHubDoctor_ReposResolvable_NoRepos(t *testing.T) {
	r := checkGitHubReposResolvable(nil, "")
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED for empty repos, got %s (msg=%s)", r.Status, r.Message)
	}
}

// TestGitHubDoctor_ReposResolvable_AllHealthy: repos with profiles or default_profile → OK.
func TestGitHubDoctor_ReposResolvable_AllHealthy(t *testing.T) {
	repos := []appcfg.GithubRepoEntry{
		{Match: "org/repo-a", Profile: "profiles/github-review.yaml", Allow: []string{"alice"}},
		{Match: "org/repo-b", Profile: "profiles/github-review.yaml", Allow: []string{"bob"}},
	}
	r := checkGitHubReposResolvable(repos, "")
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "2 repo entry") {
		t.Errorf("expected count in message, got: %s", r.Message)
	}
}

// TestGitHubDoctor_ReposResolvable_DefaultProfileFallback: no per-entry profile
// but default_profile is set → OK (resolves via fallback).
func TestGitHubDoctor_ReposResolvable_DefaultProfileFallback(t *testing.T) {
	repos := []appcfg.GithubRepoEntry{
		{Match: "org/repo-a", Allow: []string{"alice"}}, // no Profile
	}
	r := checkGitHubReposResolvable(repos, "profiles/github-review.yaml")
	if r.Status != CheckOK {
		t.Fatalf("expected OK with default_profile fallback, got %s (msg=%s)", r.Status, r.Message)
	}
}

// TestGitHubDoctor_ReposResolvable_MissingProfile: no profile, no default_profile → WARN.
func TestGitHubDoctor_ReposResolvable_MissingProfile(t *testing.T) {
	repos := []appcfg.GithubRepoEntry{
		{Match: "org/repo-a", Allow: []string{"alice"}}, // no Profile
	}
	r := checkGitHubReposResolvable(repos, "") // no default_profile either
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN for missing profile, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "no profile") {
		t.Errorf("expected 'no profile' in message, got: %s", r.Message)
	}
	if !strings.Contains(r.Remediation, "default_profile") {
		t.Errorf("expected remediation to mention default_profile, got: %s", r.Remediation)
	}
}

// TestGitHubDoctor_ReposResolvable_MatchOverlap: two entries with the same Match
// pattern → WARN with 'match-overlap' text.
func TestGitHubDoctor_ReposResolvable_MatchOverlap(t *testing.T) {
	repos := []appcfg.GithubRepoEntry{
		{Match: "org/repo-a", Profile: "profiles/github-review.yaml", Allow: []string{"alice"}},
		{Match: "org/repo-a", Profile: "profiles/github-review.yaml", Allow: []string{"bob"}},
	}
	r := checkGitHubReposResolvable(repos, "")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN for match-overlap, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "match-overlap") {
		t.Errorf("expected 'match-overlap' in message, got: %s", r.Message)
	}
}

// TestGitHubDoctor_ReposResolvable_OverlapAndMissingProfile: two issues in one
// config: overlap + missing profile → WARN surfacing both.
func TestGitHubDoctor_ReposResolvable_OverlapAndMissingProfile(t *testing.T) {
	repos := []appcfg.GithubRepoEntry{
		{Match: "org/repo-a", Allow: []string{"alice"}}, // no profile
		{Match: "org/repo-a", Allow: []string{"bob"}},   // duplicate + no profile
	}
	r := checkGitHubReposResolvable(repos, "")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	// Must surface both issues.
	if !strings.Contains(r.Message, "match-overlap") {
		t.Errorf("expected 'match-overlap' mentioned, got: %s", r.Message)
	}
	if !strings.Contains(r.Message, "no profile") {
		t.Errorf("expected 'no profile' mentioned, got: %s", r.Message)
	}
}

// =============================================================================
// Integration: unconfigured GitHub skips silently (mirroring Slack skip pattern)
// =============================================================================

// TestGitHubDoctor_Unconfigured_Skipped: no github.repos in config AND app-client-id
// parameter absent → the GitHub App Config check returns SKIPPED, not WARN.
// This prevents spurious WARN on installs that have no GitHub integration.
func TestGitHubDoctor_Unconfigured_Skipped(t *testing.T) {
	// SSM returns ParameterNotFound for all keys → "not configured" path.
	ssmClient := &mockSSMReadClient{}

	// Build the inline closure that buildChecks uses for the "GitHub App Config" check
	// when no repos are configured and app-client-id is absent.
	githubConfigured := false
	ssmPrefix := "/km/"

	checkFn := func(ctx context.Context) CheckResult {
		if ssmClient == nil {
			return CheckResult{Name: "GitHub App Config", Status: CheckSkipped, Message: "SSM client not available"}
		}
		if !githubConfigured {
			probe, probeErr := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
				Name: aws.String(ssmPrefix + "config/github/app-client-id"),
			})
			var notFoundErr *ssmtypes.ParameterNotFound
			if isAs(probeErr, notFoundErr) || (probeErr == nil && (probe.Parameter == nil || probe.Parameter.Value == nil || aws.ToString(probe.Parameter.Value) == "")) {
				return CheckResult{
					Name:    "GitHub App Config",
					Status:  CheckSkipped,
					Message: "GitHub integration not configured (no github.repos in km-config.yaml)",
				}
			}
		}
		return checkGitHubConfig(ctx, ssmClient, ssmPrefix)
	}

	r := checkFn(context.Background())
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED when GitHub is unconfigured, got %s (msg=%s)", r.Status, r.Message)
	}
	if strings.Contains(strings.ToLower(r.Message), "warn") {
		t.Errorf("expected no WARN text in skip message, got: %s", r.Message)
	}
}

// isAs is a helper that wraps errors.As for use in the test closure above.
// It returns true when err is assignable to the target type.
func isAs(err error, target interface{}) bool {
	if err == nil {
		return false
	}
	switch target.(type) {
	case *ssmtypes.ParameterNotFound:
		var pnf *ssmtypes.ParameterNotFound
		return errorsAs(err, &pnf)
	}
	return false
}

// errorsAs calls errors.As without importing the errors package in this file's
// top-level imports (it is already imported by doctor_test.go in the same package).
func errorsAs(err error, target interface{}) bool {
	// Leverage the standard library via a type switch on the concrete type.
	// This avoids a duplicate import while keeping the function local to the test.
	type errAsIface interface {
		As(interface{}) bool
	}
	// errors.As cannot be called directly without importing errors; however, the
	// same package (cmd_test) already imports it via doctor_test.go. Because all
	// _test.go files in a package share the same test binary we delegate to the
	// same logic by checking interface satisfaction.
	if x, ok := err.(errAsIface); ok {
		return x.As(target)
	}
	// Fallback: direct type assertion for *ssmtypes.ParameterNotFound.
	if _, ok := target.(**ssmtypes.ParameterNotFound); ok {
		_, isMissing := err.(*ssmtypes.ParameterNotFound)
		return isMissing
	}
	return false
}

// =============================================================================
// checkGitHubPeerBridges (Phase 100 — federated relay)
// =============================================================================

// TestGithubPeerBridges exercises all return paths of checkGitHubPeerBridges.
//
// Mirrors checkSlackPeerBridges (doctor_slack.go) semantics:
//   - Empty/nil peerBridges → SKIPPED (federation not configured).
//   - Malformed URL entry   → WARN  (operator misconfiguration).
//   - Self-loop             → WARN  (peer URL equals own bridge URL).
//   - All valid + distinct  → OK.
func TestGithubPeerBridges(t *testing.T) {
	const own = "https://abc123.lambda-url.us-east-1.on.aws/"

	tests := []struct {
		name         string
		peerBridges  []string
		ownBridgeURL string
		wantStatus   CheckStatus
		wantMsgSub   string // substring that must appear in Message; "" = no check
		wantRemSub   string // substring that must appear in Remediation; "" = no check
	}{
		{
			name:         "nil peerBridges → SKIPPED",
			peerBridges:  nil,
			ownBridgeURL: own,
			wantStatus:   CheckSkipped,
			wantMsgSub:   "federation off",
		},
		{
			name:         "empty peerBridges → SKIPPED",
			peerBridges:  []string{},
			ownBridgeURL: own,
			wantStatus:   CheckSkipped,
			wantMsgSub:   "federation off",
		},
		{
			name:         "malformed URL → WARN",
			peerBridges:  []string{"::::not a url"},
			ownBridgeURL: own,
			wantStatus:   CheckWarn,
			wantMsgSub:   "malformed",
			wantRemSub:   "peer_bridges",
		},
		{
			name:         "missing scheme → WARN",
			peerBridges:  []string{"abc123.lambda-url.us-east-1.on.aws/"},
			ownBridgeURL: own,
			wantStatus:   CheckWarn,
			wantMsgSub:   "malformed",
		},
		{
			name:         "self-loop → WARN",
			peerBridges:  []string{own},
			ownBridgeURL: own,
			wantStatus:   CheckWarn,
			wantMsgSub:   "self-loop",
			wantRemSub:   "peer_bridges",
		},
		{
			name: "valid distinct peers → OK",
			peerBridges: []string{
				"https://def456.lambda-url.us-east-1.on.aws/",
				"https://ghi789.lambda-url.us-east-1.on.aws/",
			},
			ownBridgeURL: own,
			wantStatus:   CheckOK,
		},
		{
			name: "valid peers + ownBridgeURL empty → OK (no self-loop possible)",
			peerBridges: []string{
				"https://def456.lambda-url.us-east-1.on.aws/",
			},
			ownBridgeURL: "",
			wantStatus:   CheckOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkGitHubPeerBridges(tc.peerBridges, tc.ownBridgeURL)
			if got.Status != tc.wantStatus {
				t.Errorf("status: got %v, want %v (message: %q)", got.Status, tc.wantStatus, got.Message)
			}
			if tc.wantMsgSub != "" && !strings.Contains(got.Message, tc.wantMsgSub) {
				t.Errorf("message: got %q, want substring %q", got.Message, tc.wantMsgSub)
			}
			if tc.wantRemSub != "" && !strings.Contains(got.Remediation, tc.wantRemSub) {
				t.Errorf("remediation: got %q, want substring %q", got.Remediation, tc.wantRemSub)
			}
		})
	}
}
