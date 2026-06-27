// Package httpproxy — quota_classify.go
// URL→Action classifier for the MITM proxy action quota chokepoint (Phase 121).
//
// Scope: only api.github.com (GitHub writes) and email.*.amazonaws.com (SES email send).
// Lambda Function URLs (*.lambda-url.*.on.aws) are explicitly excluded — the bridge
// Lambda owns Slack/H1 quota counting and counting at the proxy would double-count.
//
// See CONTEXT.md §3 for the full chokepoint/action taxonomy.
package httpproxy

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/elazarl/goproxy"
	"github.com/rs/zerolog/log"
	"github.com/whereiskurt/klanker-maker/pkg/quota"
)

// sesHostRegex matches SESv2 regional endpoint hostnames.
// Matches: email.us-east-1.amazonaws.com, email.eu-west-1.amazonaws.com, etc.
var sesHostRegex = regexp.MustCompile(`^email\.[a-z0-9-]+\.amazonaws\.com(:\d+)?$`)

// lambdaURLRegex matches Lambda Function URL hostnames (*.lambda-url.*.on.aws).
// These are the bridge Function URLs used by km-slack — proxy must NOT count them
// to avoid double-counting slack_post (Risk 1, RESEARCH.md §9).
var lambdaURLRegex = regexp.MustCompile(`\.lambda-url\.[a-z0-9-]+\.on\.aws(:\d+)?$`)

// sesPathPrefix is the SESv2 SendEmail API path prefix.
const sesPathPrefix = "/v2/email/outbound-emails"

// ClassifyAction maps an outbound HTTP request (method, host, path) to a quota.Action.
// Returns "" (no action) when:
//   - The method is not POST.
//   - The host is a Lambda Function URL (bridge URL — must never be counted here).
//   - The path does not match a recognised write endpoint.
//
// The caller is responsible for only invoking ClassifyAction on hosts that are
// already within scope (api.github.com or sesHostRegex).
// Exported for testability; internal callers use it directly.
func ClassifyAction(method, host, path string) quota.Action {
	return classifyAction(method, host, path)
}

// classifyAction is the internal implementation.
func classifyAction(method, host, path string) quota.Action {
	if method != http.MethodPost {
		return ""
	}

	// Strip port for host matching.
	h := stripPort(host)

	// Explicit exclusion: Lambda Function URLs must never be classified.
	// This is the double-count guard for slack_post (Risk 1).
	if lambdaURLRegex.MatchString(host) {
		return ""
	}

	switch {
	case h == "api.github.com":
		return classifyGitHubAction(path)
	case sesHostRegex.MatchString(host):
		return classifySESAction(path)
	}
	return ""
}

// classifyGitHubAction returns the quota.Action for a POST to api.github.com.
// Only /repos/*/... paths are classified. Non-repo or unknown paths return "".
func classifyGitHubAction(path string) quota.Action {
	// All GitHub repo write paths start with /repos/
	const reposPrefix = "/repos/"
	if !strings.HasPrefix(path, reposPrefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, reposPrefix)
	// rest = "{owner}/{repo}/{...}"
	parts := strings.SplitN(rest, "/", 5)
	if len(parts) < 3 {
		// Not enough segments for a write endpoint.
		return ""
	}
	// parts[0] = owner, parts[1] = repo, parts[2] = verb, parts[3...] = sub-path

	verb := parts[2] // "pulls", "issues", etc.
	switch {
	case verb == "pulls" && len(parts) == 3:
		// POST /repos/{o}/{r}/pulls → create PR
		return quota.ActionGithubPR
	case verb == "pulls" && len(parts) >= 5 && parts[4] == "reviews":
		// POST /repos/{o}/{r}/pulls/{n}/reviews → create PR review
		return quota.ActionGithubReview
	case verb == "issues" && len(parts) >= 5 && parts[4] == "comments":
		// POST /repos/{o}/{r}/issues/{n}/comments → create issue/PR comment
		return quota.ActionGithubComment
	}
	return ""
}

// classifySESAction returns quota.ActionEmailSend for a SESv2 SendEmail POST.
func classifySESAction(path string) quota.Action {
	if strings.HasPrefix(path, sesPathPrefix) {
		return quota.ActionEmailSend
	}
	return ""
}

// stripPort removes any ":port" suffix from a host string.
func stripPort(host string) string {
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		// Check it's a port (only digits after colon).
		port := host[idx+1:]
		for _, c := range port {
			if c < '0' || c > '9' {
				return host
			}
		}
		return host[:idx]
	}
	return host
}

// actionQuotaOptions holds all state for action quota enforcement.
type actionQuotaOptions struct {
	client    quota.QuotaAPI
	tableName string
	sandboxID string
	limits    quota.Limits
}

// WithActionQuota enables proxy-level action quota enforcement (Phase 121).
// When KM_QUOTA_TABLE is set at startup, this option is applied so the proxy
// records and optionally blocks github_pr/github_comment/github_review/email_send
// actions against the per-sandbox DynamoDB counter table.
//
// Mirrors WithBudgetEnforcement (proxy.go:65): accumulates onto cfg.actionQuota
// and registers OnRequest handlers in NewProxy.
//
// Absent table name (KM_QUOTA_TABLE unset) ⇒ option not applied ⇒ dormant.
func WithActionQuota(client quota.QuotaAPI, tableName, sandboxID string, limits quota.Limits) ProxyOption {
	return func(_ *goproxy.ProxyHttpServer, cfg *proxyConfig) {
		cfg.actionQuota = &actionQuotaOptions{
			client:    client,
			tableName: tableName,
			sandboxID: sandboxID,
			limits:    limits,
		}
	}
}

// actionDeniedResponse returns a 429 Too Many Requests response for a blocked action.
func actionDeniedResponse(req *http.Request, sandboxID string, action quota.Action, dec quota.Decision) *http.Response {
	body := `{"error":"quota_exceeded","action":"` + string(action) + `","sandbox_id":"` + sandboxID + `","on_breach":"` + string(dec.OnBreach) + `"}`
	resp := goproxy.NewResponse(req, "application/json", http.StatusTooManyRequests, body)
	resp.Header.Set("Retry-After", "3600")
	return resp
}

// registerActionQuotaHandlers registers OnRequest handlers for action quota enforcement.
// Called from NewProxy when cfg.actionQuota != nil.
// Scoped to api.github.com + sesHostRegex ONLY — never the bridge Function URL.
func registerActionQuotaHandlers(proxy *goproxy.ProxyHttpServer, aq *actionQuotaOptions) {
	// Combined host matcher: api.github.com OR ses endpoints.
	// Lambda Function URLs (*.lambda-url.*.on.aws) are intentionally excluded.
	apiGithubRegex := regexp.MustCompile(`^api\.github\.com(:\d+)?$`)

	// GitHub quota OnRequest: classify + record.
	proxy.OnRequest(goproxy.ReqHostMatches(apiGithubRegex)).DoFunc(
		func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			action := classifyAction(req.Method, req.Host, req.URL.Path)
			if action == "" {
				return req, nil
			}
			limit, ok := aq.limits[action]
			if !ok {
				// No limit configured for this action — dormant (pass through, no count).
				return req, nil
			}
			dec, err := quota.Record(req.Context(), aq.client, aq.tableName, aq.sandboxID, action, limit)
			if err != nil {
				log.Error().
					Err(err).
					Str("sandbox_id", aq.sandboxID).
					Str("action", string(action)).
					Str("event_type", "quota_record_error").
					Msg("")
				// Fail open — let the request through on DDB error.
				return req, nil
			}
			log.Info().
				Str("sandbox_id", aq.sandboxID).
				Str("event_type", "quota_recorded").
				Str("action", string(action)).
				Bool("tripped", dec.Tripped).
				Str("worst_window", dec.WorstWindow).
				Str("on_breach", string(dec.OnBreach)).
				Msg("")
			if dec.Tripped && (dec.OnBreach == quota.BreachBlock || dec.OnBreach == quota.BreachFreeze) {
				return req, actionDeniedResponse(req, aq.sandboxID, action, dec)
			}
			return req, nil
		},
	)

	// SES quota OnRequest: classify + record.
	// Must be registered BEFORE the general CONNECT handler (goproxy first-match).
	proxy.OnRequest(goproxy.ReqHostMatches(sesHostRegex)).DoFunc(
		func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			action := classifyAction(req.Method, req.Host, req.URL.Path)
			if action == "" {
				return req, nil
			}
			limit, ok := aq.limits[action]
			if !ok {
				return req, nil
			}
			dec, err := quota.Record(req.Context(), aq.client, aq.tableName, aq.sandboxID, action, limit)
			if err != nil {
				log.Error().
					Err(err).
					Str("sandbox_id", aq.sandboxID).
					Str("action", string(action)).
					Str("event_type", "quota_record_error").
					Msg("")
				return req, nil
			}
			log.Info().
				Str("sandbox_id", aq.sandboxID).
				Str("event_type", "quota_recorded").
				Str("action", string(action)).
				Bool("tripped", dec.Tripped).
				Str("worst_window", dec.WorstWindow).
				Str("on_breach", string(dec.OnBreach)).
				Msg("")
			if dec.Tripped && (dec.OnBreach == quota.BreachBlock || dec.OnBreach == quota.BreachFreeze) {
				return req, actionDeniedResponse(req, aq.sandboxID, action, dec)
			}
			return req, nil
		},
	)
}
