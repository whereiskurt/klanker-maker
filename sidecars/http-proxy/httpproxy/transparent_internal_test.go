package httpproxy

// Internal (same-package) test file — needed to reach the unexported
// matchInterceptForRequest helper directly, per plan 127-02 task 3's
// instruction to factor the transparent-listener match-and-respond decision
// into a small testable helper when exercising the full BPF-backed listener
// is impractical (there is no pinned-map fixture available in unit tests).

import (
	"net/http"
	"testing"
)

func TestTransparentListener_MatchInterceptForRequest_Hit(t *testing.T) {
	tl := &TransparentListener{
		intercepts: []Intercept{
			{Name: "chaos", Hosts: []string{"api.example.com"}, Respond: &Respond{Status: 200, Body: "intercepted"}},
		},
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.example.com/", nil)
	req.Host = "api.example.com"

	ic := tl.matchInterceptForRequest(req)
	if ic == nil || ic.Name != "chaos" {
		t.Fatalf("matchInterceptForRequest = %+v, want the chaos intercept to match", ic)
	}
}

func TestTransparentListener_MatchInterceptForRequest_Miss(t *testing.T) {
	tl := &TransparentListener{
		intercepts: []Intercept{
			{Name: "chaos", Hosts: []string{"api.example.com"}, Respond: &Respond{Status: 200, Body: "intercepted"}},
		},
	}
	req, _ := http.NewRequest(http.MethodGet, "https://other.example.com/", nil)
	req.Host = "other.example.com"

	if ic := tl.matchInterceptForRequest(req); ic != nil {
		t.Fatalf("matchInterceptForRequest = %+v, want nil for a non-matching host", ic)
	}
}

// The GitHub-precedence case: an intercept declared for a GitHub host must
// not fire once GitHub repo filtering is active on the listener, mirroring
// the goproxy-path precedence proven in intercept_test.go.
func TestTransparentListener_MatchInterceptForRequest_GitHubPrecedence(t *testing.T) {
	tl := &TransparentListener{
		githubRepos: []string{"owner/repo"},
		intercepts: []Intercept{
			{Name: "github-chaos", Hosts: []string{"api.github.com"}, Respond: &Respond{Status: 200, Body: "intercepted"}},
		},
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/owner/repo", nil)
	req.Host = "api.github.com"

	if ic := tl.matchInterceptForRequest(req); ic != nil {
		t.Fatalf("matchInterceptForRequest = %+v, want nil — the GitHub repo filter owns this host", ic)
	}
}

// Without GitHub filtering configured, the same host is free for an
// intercept to claim — isPlatformOwnedHost must not treat api.github.com as
// reserved on a sandbox where the GitHub repo filter was never enabled.
func TestTransparentListener_MatchInterceptForRequest_GitHubHostFreeWhenFilterDisabled(t *testing.T) {
	tl := &TransparentListener{
		intercepts: []Intercept{
			{Name: "github-chaos", Hosts: []string{"api.github.com"}, Respond: &Respond{Status: 200, Body: "intercepted"}},
		},
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/owner/repo", nil)
	req.Host = "api.github.com"

	ic := tl.matchInterceptForRequest(req)
	if ic == nil || ic.Name != "github-chaos" {
		t.Fatalf("matchInterceptForRequest = %+v, want the github-chaos intercept to fire when no repo filter is configured", ic)
	}
}

// The Anthropic-metering-precedence case, mirroring the GitHub one above.
func TestTransparentListener_MatchInterceptForRequest_AnthropicMeteringPrecedence(t *testing.T) {
	tl := &TransparentListener{
		budget: &budgetEnforcementOptions{cache: NewBudgetCache()},
		intercepts: []Intercept{
			{Name: "anthropic-chaos", Hosts: []string{"api.anthropic.com"}, Respond: &Respond{Status: 200, Body: "intercepted"}},
		},
	}
	req, _ := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
	req.Host = "api.anthropic.com"

	if ic := tl.matchInterceptForRequest(req); ic != nil {
		t.Fatalf("matchInterceptForRequest = %+v, want nil — AI budget metering owns this host", ic)
	}
}
