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
	"io"
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
	budget *budgetEnforcementOptions
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

// IsHostAllowed reports whether host is in the allowed list.
// The port is stripped from "host:port" before comparison.
// Matching is case-insensitive. An empty allowed list denies everything.
func IsHostAllowed(host string, allowed []string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		// No port present — use as-is.
		h = host
	}
	h = strings.ToLower(h)
	for _, a := range allowed {
		if strings.ToLower(a) == h {
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
		proxy.OnRequest(goproxy.ReqHostMatches(bedrockHostRegex)).HandleConnect(goproxy.AlwaysMitm)

		// OnResponse: intercept Bedrock InvokeModel responses, extract tokens, price, increment.
		proxy.OnResponse(goproxy.ReqHostMatches(bedrockHostRegex)).DoFunc(
			func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
				if resp == nil || ctx.Req == nil {
					return resp
				}
				req := ctx.Req

				// Read the full response body (capped at 10MB).
				bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxResponseBodySize)))
				_ = resp.Body.Close()
				// Replace body immediately so client always gets the response data.
				resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))

				if err != nil {
					log.Warn().
						Str("sandbox_id", sandboxID).
						Str("event_type", "bedrock_body_read_error").
						Err(err).
						Msg("")
					return resp
				}

				// Check cached budget again post-body-read (covers races between preflight
				// and response interception, e.g. concurrent requests draining budget).
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
					return BedrockBlockedResponse(req, sandboxID, modelID, entry.AISpent, entry.AILimit)
				}

				// Extract tokens from the response body.
				inputTokens, outputTokens, parseErr := ExtractBedrockTokens(bytes.NewReader(bodyBytes))
				if parseErr != nil || (inputTokens == 0 && outputTokens == 0) {
					// No tokens to record — pass through unchanged.
					return resp
				}

				// Look up model rate (fall back to zero cost if model unknown).
				modelID := ExtractModelID(req.URL.Path)
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

				// Optimistically update local cache before DynamoDB round-trip.
				be.cache.UpdateLocalSpend(sandboxID, costUSD)

				// Fire-and-forget DynamoDB increment — don't block the response path.
				go func() {
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

					// Refresh cache with authoritative DynamoDB value.
					cachedEntry := be.cache.Get(sandboxID)
					if cachedEntry != nil {
						// Update the cache's AISpent with the authoritative value from DynamoDB.
						// Re-set the entry so the TTL clock resets on the authoritative value.
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
				}()

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
		// Model ID is extracted from the response body (not the URL path — Anthropic does not
		// encode the model in the URL). staticAnthropicRates is used directly; it is separate
		// from be.modelRates which carries Bedrock-only rates.
		proxy.OnResponse(goproxy.ReqHostMatches(anthropicHostRegex)).DoFunc(
			func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
				if resp == nil || ctx.Req == nil {
					return resp
				}
				req := ctx.Req

				// Read the full response body (capped at 10 MB).
				bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxResponseBodySize)))
				_ = resp.Body.Close()
				// Replace body immediately so the client always gets the response data.
				resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))

				if err != nil {
					log.Warn().
						Str("sandbox_id", sandboxID).
						Str("event_type", "anthropic_body_read_error").
						Err(err).
						Msg("")
					return resp
				}

				// Check cached budget again post-body-read (covers races between
				// preflight and response interception).
				entry := be.cache.Get(sandboxID)
				if entry != nil && entry.AILimit > 0 && entry.AISpent >= entry.AILimit {
					log.Info().
						Str("sandbox_id", sandboxID).
						Str("event_type", "ai_budget_exhausted_response").
						Str("host", req.Host).
						Float64("spent", entry.AISpent).
						Float64("limit", entry.AILimit).
						Msg("")
					return AnthropicBlockedResponse(req, sandboxID, "", entry.AISpent, entry.AILimit)
				}

				// Extract model ID and tokens from the response body.
				modelID, inputTokens, outputTokens, parseErr := ExtractAnthropicTokens(bytes.NewReader(bodyBytes))
				if parseErr != nil || (inputTokens == 0 && outputTokens == 0) {
					// No tokens to record — pass through unchanged.
					return resp
				}

				// Look up model rate in the Anthropic static rate table.
				// Falls back to zero cost if the model ID is not yet in the table.
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

				// Optimistically update local cache before DynamoDB round-trip.
				be.cache.UpdateLocalSpend(sandboxID, costUSD)

				// Fire-and-forget DynamoDB increment — don't block the response path.
				go func() {
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

					// Refresh cache with authoritative DynamoDB value.
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
				}()

				return resp
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
	proxy.OnRequest().DoFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
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
