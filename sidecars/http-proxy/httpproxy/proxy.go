// Package httpproxy provides HTTP/HTTPS CONNECT proxy logic for the km sidecar.
// It enforces a host allowlist and injects W3C traceparent headers on allowed
// outbound CONNECT requests.
//
// When budget enforcement is enabled via WithBudgetEnforcement, the proxy uses
// AlwaysMitm for bedrock-runtime hosts to intercept SSE responses and meter AI
// token usage per sandbox. Non-Bedrock HTTPS traffic continues to use OkConnect
// passthrough (no MITM).
package httpproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/elazarl/goproxy"
	"github.com/rs/zerolog/log"
	"github.com/whereiskurt/klankrmkr/pkg/aws"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// bedrockHostRegex matches bedrock-runtime endpoints in any AWS region.
var bedrockHostRegex = regexp.MustCompile(`^bedrock-runtime\..+\.amazonaws\.com`)

// anthropicHostRegex matches the Anthropic direct API endpoint.
// Claude Code uses api.anthropic.com by default (not Bedrock).
var anthropicHostRegex = regexp.MustCompile(`^api\.anthropic\.com`)

// BudgetUpdater is called after each Bedrock response to write the remaining
// AI budget to a sidecar-local file (e.g. /run/km/budget_remaining).
// Set to nil to skip the file update.
type BudgetUpdater func(remaining float64)

// budgetEnforcementOptions holds all state needed for Bedrock MITM budget enforcement.
type budgetEnforcementOptions struct {
	client      aws.BudgetAPI
	tableName   string
	modelRates  map[string]aws.BedrockModelRate
	cache       *budgetCache
	onBudgetUpdate BudgetUpdater
}

// ProxyOption is a functional option applied to a proxy during NewProxy.
type ProxyOption func(*goproxy.ProxyHttpServer, *proxyConfig)

// proxyConfig accumulates optional proxy configuration across ProxyOption calls.
type proxyConfig struct {
	budget      *budgetEnforcementOptions
	githubRepos []string
	httpsOnly   bool
}

// WithBudgetEnforcement enables Bedrock MITM interception and DynamoDB spend
// tracking for the proxy. When the sandbox AI budget is exhausted, the proxy
// returns 403 before forwarding the request.
//
// onBudgetUpdate may be nil; if provided it is called after each Bedrock
// response with the remaining AI budget (limit - spent).
func WithBudgetEnforcement(client aws.BudgetAPI, tableName string, modelRates map[string]aws.BedrockModelRate, onBudgetUpdate BudgetUpdater) ProxyOption {
	return func(_ *goproxy.ProxyHttpServer, cfg *proxyConfig) {
		cfg.budget = &budgetEnforcementOptions{
			client:         client,
			tableName:      tableName,
			modelRates:     modelRates,
			cache:          NewBudgetCache(),
			onBudgetUpdate: onBudgetUpdate,
		}
	}
}

// WithCustomCA sets a custom CA certificate for MITM TLS interception.
// The certPEM must contain both the certificate and private key in PEM format.
// This replaces goproxy's built-in test CA so that MITM leaf certificates
// are signed by the platform's own CA (which the sandbox trusts via
// update-ca-certificates at boot).
func WithCustomCA(certPEM []byte) ProxyOption {
	return func(_ *goproxy.ProxyHttpServer, _ *proxyConfig) {
		cert, err := tls.X509KeyPair(certPEM, certPEM)
		if err != nil {
			log.Error().Err(err).Msg("failed to parse custom CA cert+key; falling back to goproxy default CA")
			return
		}
		cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			log.Error().Err(err).Msg("failed to parse custom CA leaf; falling back to goproxy default CA")
			return
		}
		goproxy.GoproxyCa = cert
		goproxy.OkConnect = &goproxy.ConnectAction{Action: goproxy.ConnectAccept, TLSConfig: goproxy.TLSConfigFromCA(&cert)}
		goproxy.MitmConnect = &goproxy.ConnectAction{Action: goproxy.ConnectMitm, TLSConfig: goproxy.TLSConfigFromCA(&cert)}
		goproxy.HTTPMitmConnect = &goproxy.ConnectAction{Action: goproxy.ConnectMitm, TLSConfig: goproxy.TLSConfigFromCA(&cert)}
		goproxy.RejectConnect = &goproxy.ConnectAction{Action: goproxy.ConnectReject, TLSConfig: goproxy.TLSConfigFromCA(&cert)}
		log.Info().Str("subject", cert.Leaf.Subject.CommonName).Msg("custom CA loaded for MITM")
	}
}

// WithHTTPSOnly blocks plain HTTP requests (non-TLS).
// On EC2, the security group enforces HTTPS-only at the network layer.
// On Docker, there's no security group — this option provides equivalent enforcement.
func WithHTTPSOnly() ProxyOption {
	return func(_ *goproxy.ProxyHttpServer, cfg *proxyConfig) {
		cfg.httpsOnly = true
	}
}

// IsHostAllowed reports whether host is in the allowed list.
// The port is stripped from "host:port" before comparison.
// Matching is case-insensitive. An empty allowed list denies everything.
// Entries starting with "." are treated as suffix matches (e.g. ".amazonaws.com"
// matches "bedrock-runtime.us-east-1.amazonaws.com").
func IsHostAllowed(host string, allowed []string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		// No port present — use as-is.
		h = host
	}
	h = strings.ToLower(h)
	for _, a := range allowed {
		a = strings.ToLower(a)
		if strings.HasPrefix(a, ".") {
			// Suffix match: ".amazonaws.com" matches "x.y.amazonaws.com"
			if strings.HasSuffix(h, a) {
				return true
			}
		} else if a == h {
			return true
		}
	}
	return false
}

// InjectTraceContext injects W3C traceparent and tracestate headers into h using
// the globally registered OTel text map propagator. ctx is used as the span
// context source. This is exported so tests can verify injection without
// requiring a full proxy round-trip.
func InjectTraceContext(ctx context.Context, h http.Header) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(h))
}

// NewProxy creates a goproxy proxy that enforces the allowed host list.
// Allowed CONNECT tunnels are permitted after traceparent header injection.
// Blocked CONNECT tunnels are rejected with ConnectReject (client sees 403).
// Plain HTTP requests to blocked hosts receive a 403 response.
//
// otel.SetTextMapPropagator must be called before NewProxy for header injection
// to produce traceparent values (a no-op propagator is the safe default).
//
// Optional ProxyOption values extend the proxy with additional behavior (e.g.
// Bedrock MITM budget enforcement via WithBudgetEnforcement).
func NewProxy(allowed []string, sandboxID string, opts ...ProxyOption) *goproxy.ProxyHttpServer {
	// Ensure W3C TraceContext propagation is registered.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = false

	// Apply functional options to build the proxy config.
	cfg := &proxyConfig{}
	for _, opt := range opts {
		opt(proxy, cfg)
	}

	// -------------------------------------------------------------------------
	// Budget enforcement: Bedrock MITM handlers (registered BEFORE OkConnect).
	// -------------------------------------------------------------------------
	if cfg.budget != nil {
		be := cfg.budget

		// Pre-flight OnRequest check: reject requests to Bedrock when the sandbox
		// AI budget is already exhausted (cached check — no DynamoDB read).
		proxy.OnRequest(goproxy.ReqHostMatches(bedrockHostRegex)).DoFunc(
			func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
				entry := be.cache.Get(sandboxID)
				if entry != nil && entry.AILimit > 0 && entry.AISpent >= entry.AILimit {
					log.Info().
						Str("sandbox_id", sandboxID).
						Str("event_type", "ai_budget_exhausted_preflight").
						Str("host", req.Host).
						Float64("spent", entry.AISpent).
						Float64("limit", entry.AILimit).
						Msg("")
					modelID := ExtractModelID(req.URL.Path)
					return req, BedrockBlockedResponse(req, sandboxID, modelID, entry.AISpent, entry.AILimit)
				}
				return req, nil
			},
		)

		// MITM handler: AlwaysMitm for bedrock-runtime hosts.
		// This MUST be registered before the general CONNECT handler so goproxy
		// first-match semantics route Bedrock CONNECT through MITM.
		proxy.OnRequest(goproxy.ReqHostMatches(bedrockHostRegex)).HandleConnectFunc(
			func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
				log.Info().
					Str("event_type", "bedrock_mitm_connect").
					Str("sandbox_id", sandboxID).
					Str("host", host).
					Msg("")
				return goproxy.MitmConnect, host
			})

		// OnResponse: intercept Bedrock InvokeModel responses, extract tokens, price, increment.
		// Uses a tee-reader approach: the response body streams through to the client
		// immediately while being captured in a buffer. When the stream ends (EOF),
		// token extraction and DynamoDB metering fire asynchronously.
		// This avoids blocking streaming responses (invoke-with-response-stream).
		proxy.OnResponse(goproxy.ReqHostMatches(bedrockHostRegex)).DoFunc(
			func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
				log.Info().
					Str("event_type", "bedrock_response_intercepted").
					Str("sandbox_id", sandboxID).
					Int("status", func() int { if resp != nil { return resp.StatusCode }; return 0 }()).
					Str("host", func() string { if ctx.Req != nil { return ctx.Req.Host }; return "" }()).
					Msg("")
				if resp == nil || ctx.Req == nil {
					return resp
				}
				req := ctx.Req

				// Pre-flight budget check (cached — no DynamoDB read).
				entry := be.cache.Get(sandboxID)
				if entry != nil && entry.AILimit > 0 && entry.AISpent >= entry.AILimit {
					modelID := ExtractModelID(req.URL.Path)
					log.Info().
						Str("sandbox_id", sandboxID).
						Str("event_type", "ai_budget_exhausted_response").
						Str("model", modelID).
						Float64("spent", entry.AISpent).
						Float64("limit", entry.AILimit).
						Msg("")
					// Drain and close the upstream body before replacing it.
					_ = resp.Body.Close()
					return BedrockBlockedResponse(req, sandboxID, modelID, entry.AISpent, entry.AILimit)
				}

				// Wrap the response body in a metering reader that captures data as it
				// streams through to the client. When the stream ends (EOF or close),
				// the onComplete callback fires asynchronously to extract tokens and
				// increment DynamoDB spend.
				modelID := ExtractModelID(req.URL.Path)
				resp.Body = newMeteringReader(resp.Body, func(captured []byte) {
					inputTokens, outputTokens, parseErr := ExtractBedrockTokens(bytes.NewReader(captured))
					if parseErr != nil || (inputTokens == 0 && outputTokens == 0) {
						return
					}

					var costUSD float64
					if rate, ok := be.modelRates[modelID]; ok {
						costUSD = CalculateCost(inputTokens, outputTokens, rate.InputPricePer1KTokens, rate.OutputPricePer1KTokens)
					}

					log.Info().
						Str("sandbox_id", sandboxID).
						Str("event_type", "bedrock_tokens_metered").
						Str("model", modelID).
						Int("input_tokens", inputTokens).
						Int("output_tokens", outputTokens).
						Float64("cost_usd", costUSD).
						Msg("")

					be.cache.UpdateLocalSpend(sandboxID, costUSD)

					updatedSpend, incrementErr := aws.IncrementAISpend(
						context.Background(),
						be.client,
						be.tableName,
						sandboxID,
						modelID,
						inputTokens,
						outputTokens,
						costUSD,
					)
					if incrementErr != nil {
						log.Error().
							Str("sandbox_id", sandboxID).
							Str("event_type", "bedrock_spend_increment_error").
							Err(incrementErr).
							Msg("")
						return
					}

					cachedEntry := be.cache.Get(sandboxID)
					if cachedEntry != nil {
						cachedEntry.AISpent = updatedSpend
						be.cache.Set(sandboxID, cachedEntry)
					}

					if be.onBudgetUpdate != nil {
						limit := float64(0)
						if cachedEntry != nil {
							limit = cachedEntry.AILimit
						}
						remaining := limit - updatedSpend
						be.onBudgetUpdate(remaining)
					}
				})

				return resp
			},
		)

		// -----------------------------------------------------------------
		// Anthropic direct API (api.anthropic.com) MITM handlers.
		// Must be registered INSIDE this if-block and BEFORE the general
		// CONNECT handler — goproxy uses first-match for CONNECT.
		// -----------------------------------------------------------------

		// Pre-flight OnRequest check: reject Anthropic requests when the sandbox
		// AI budget is already exhausted (cached check — no DynamoDB read).
		proxy.OnRequest(goproxy.ReqHostMatches(anthropicHostRegex)).DoFunc(
			func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
				entry := be.cache.Get(sandboxID)
				if entry != nil && entry.AILimit > 0 && entry.AISpent >= entry.AILimit {
					log.Info().
						Str("sandbox_id", sandboxID).
						Str("event_type", "ai_budget_exhausted_preflight").
						Str("host", req.Host).
						Float64("spent", entry.AISpent).
						Float64("limit", entry.AILimit).
						Msg("")
					return req, AnthropicBlockedResponse(req, sandboxID, "", entry.AISpent, entry.AILimit)
				}
				return req, nil
			},
		)

		// MITM handler: AlwaysMitm for api.anthropic.com.
		proxy.OnRequest(goproxy.ReqHostMatches(anthropicHostRegex)).HandleConnect(goproxy.AlwaysMitm)

		// OnResponse: intercept Anthropic /v1/messages responses, extract tokens, price, increment.
		// Uses tee-reader approach (same as Bedrock) to handle streaming SSE responses
		// without blocking. Works for Claude Code Max (direct API) and future providers.
		proxy.OnResponse(goproxy.ReqHostMatches(anthropicHostRegex)).DoFunc(
			func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
				if resp == nil || ctx.Req == nil {
					return resp
				}
				req := ctx.Req

				// Pre-flight budget check (cached).
				entry := be.cache.Get(sandboxID)
				if entry != nil && entry.AILimit > 0 && entry.AISpent >= entry.AILimit {
					log.Info().
						Str("sandbox_id", sandboxID).
						Str("event_type", "ai_budget_exhausted_response").
						Str("host", req.Host).
						Float64("spent", entry.AISpent).
						Float64("limit", entry.AILimit).
						Msg("")
					_ = resp.Body.Close()
					return AnthropicBlockedResponse(req, sandboxID, "", entry.AISpent, entry.AILimit)
				}

				// Wrap body in metering reader — streams through to client,
				// fires token extraction + DynamoDB metering on EOF.
				resp.Body = newMeteringReader(resp.Body, func(captured []byte) {
					modelID, inputTokens, outputTokens, parseErr := ExtractAnthropicTokens(bytes.NewReader(captured))
					if parseErr != nil || (inputTokens == 0 && outputTokens == 0) {
						return
					}

					var costUSD float64
					if rate, ok := staticAnthropicRates[modelID]; ok {
						costUSD = CalculateCost(inputTokens, outputTokens, rate.InputPricePer1KTokens, rate.OutputPricePer1KTokens)
					}

					log.Info().
						Str("sandbox_id", sandboxID).
						Str("event_type", "anthropic_tokens_metered").
						Str("model", modelID).
						Int("input_tokens", inputTokens).
						Int("output_tokens", outputTokens).
						Float64("cost_usd", costUSD).
						Msg("")

					be.cache.UpdateLocalSpend(sandboxID, costUSD)

					updatedSpend, incrementErr := aws.IncrementAISpend(
						context.Background(),
						be.client,
						be.tableName,
						sandboxID,
						modelID,
						inputTokens,
						outputTokens,
						costUSD,
					)
					if incrementErr != nil {
						log.Error().
							Str("sandbox_id", sandboxID).
							Str("event_type", "anthropic_spend_increment_error").
							Err(incrementErr).
							Msg("")
						return
					}

					cachedEntry := be.cache.Get(sandboxID)
					if cachedEntry != nil {
						cachedEntry.AISpent = updatedSpend
						be.cache.Set(sandboxID, cachedEntry)
					}

					if be.onBudgetUpdate != nil {
						limit := float64(0)
						if cachedEntry != nil {
							limit = cachedEntry.AILimit
						}
						remaining := limit - updatedSpend
						be.onBudgetUpdate(remaining)
					}
				})

				return resp
			},
		)
	}

	// -------------------------------------------------------------------------
	// GitHub repo-level MITM handlers (registered BEFORE general CONNECT handler).
	// When githubRepos is non-empty:
	//   a) CONNECT to GitHub hosts is intercepted via MitmConnect so the proxy
	//      can inspect request URLs after TLS termination. This also implicitly
	//      allows GitHub hosts through the proxy regardless of the allowedHosts
	//      list — the MITM handler fires before the general CONNECT handler.
	//   b) OnRequest for GitHub hosts runs ExtractRepoFromPath + IsRepoAllowed
	//      and blocks unlisted repos with a 403 JSON response.
	// When githubRepos is nil/empty, no handlers are registered and GitHub hosts
	// fall through to the general CONNECT handler (normal host-level filtering).
	// -------------------------------------------------------------------------
	if len(cfg.githubRepos) > 0 {
		allowedRepos := cfg.githubRepos

		// (a) MITM CONNECT: intercept GitHub HTTPS tunnels.
		proxy.OnRequest(goproxy.ReqHostMatches(githubHostsRegex)).HandleConnectFunc(
			func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
				log.Info().
					Str("event_type", "github_mitm_connect").
					Str("sandbox_id", sandboxID).
					Str("host", host).
					Msg("")
				return goproxy.MitmConnect, host
			},
		)

		// (b) OnRequest: enforce repo allowlist after MITM decrypts the request.
		proxy.OnRequest(goproxy.ReqHostMatches(githubHostsRegex)).DoFunc(
			func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
				repo := ExtractRepoFromPath(req.Host, req.URL.Path)
				if repo == "" {
					// Non-repo URL (login, rate_limit, etc.) — pass through.
					return req, nil
				}
				if IsRepoAllowed(repo, allowedRepos) {
					log.Info().
						Str("event_type", "github_repo_allowed").
						Str("sandbox_id", sandboxID).
						Str("repo", repo).
						Msg("")
					return req, nil
				}
				log.Info().
					Str("event_type", "github_repo_blocked").
					Str("sandbox_id", sandboxID).
					Str("repo", repo).
					Msg("")
				return req, GitHubBlockedResponse(req, sandboxID, repo)
			},
		)
	}

	// -------------------------------------------------------------------------
	// General CONNECT (HTTPS) handler — OkConnect for allowed non-Bedrock hosts.
	// -------------------------------------------------------------------------
	proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
		if !IsHostAllowed(host, allowed) {
			log.Info().
				Str("sandbox_id", sandboxID).
				Str("event_type", "http_blocked").
				Str("host", host).
				Msg("")
			return goproxy.RejectConnect, host
		}

		// Inject W3C traceparent into the original CONNECT request headers.
		if ctx.Req != nil {
			otel.GetTextMapPropagator().Inject(
				context.Background(),
				propagation.HeaderCarrier(ctx.Req.Header),
			)
		}

		return goproxy.OkConnect, host
	})

	// Plain HTTP handler.
	// When githubRepos is configured, GitHub hosts are implicitly allowed and
	// already filtered by the GitHub OnRequest handler above — skip them here.
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		// HTTPS-only mode: block plain HTTP requests.
		// On EC2 the security group blocks port 80 egress; on Docker this is the equivalent.
		if cfg.httpsOnly && req.URL.Scheme == "http" {
			log.Info().
				Str("sandbox_id", sandboxID).
				Str("event_type", "http_blocked_https_only").
				Str("host", req.Host).
				Str("url", req.URL.String()).
				Msg("")
			return req, goproxy.NewResponse(req, goproxy.ContentTypeText, http.StatusForbidden,
				"Blocked: HTTPS only — plain HTTP is not allowed by sandbox policy")
		}
		if len(cfg.githubRepos) > 0 && githubHostsRegex.MatchString(req.Host) {
			// GitHub hosts are handled by the GitHub-specific OnRequest handler.
			return req, nil
		}
		if !IsHostAllowed(req.Host, allowed) {
			log.Info().
				Str("sandbox_id", sandboxID).
				Str("event_type", "http_blocked").
				Str("host", req.Host).
				Msg("")
			return req, goproxy.NewResponse(req, goproxy.ContentTypeText, http.StatusForbidden, "Blocked by km sandbox policy")
		}
		return req, nil
	})

	return proxy
}
