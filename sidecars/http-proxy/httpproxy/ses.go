// Package httpproxy — ses.go
// SES MITM registration for the km http-proxy sidecar (Phase 121).
//
// SES (email.*.amazonaws.com) was previously an OkConnect passthrough — the proxy
// allowed the CONNECT tunnel but could not inspect the POST body. This file adds
// MitmConnect registration for sesHostRegex so the proxy can intercept
// POST /v2/email/outbound-emails and apply action quota enforcement.
//
// Registration order: MUST be called BEFORE the general CONNECT handler in NewProxy
// so goproxy first-match semantics route SES CONNECT through MITM (same as
// bedrockHostRegex / anthropicHostRegex / openaiHostRegex handlers in proxy.go).
//
// The km CA cert (installed system-wide via update-ca-certificates at boot) is
// trusted by the AWS CLI, so `aws sesv2 send-email` honours the MITM.
// Risk: AWS_CA_BUNDLE override could break this — see RESEARCH.md §9 Risk 2.
// Live UAT deferred (flagged in SUMMARY.md).
package httpproxy

import (
	"github.com/elazarl/goproxy"
	"github.com/rs/zerolog/log"
)

// WithSESMITM registers a MitmConnect handler for SES regional endpoints
// (email.*.amazonaws.com), enabling the proxy to intercept SESv2 SendEmail
// requests for action quota counting.
//
// This MUST be registered BEFORE the general CONNECT handler — it is a
// ProxyOption so the caller (NewProxy / main.go) controls ordering.
// When called inside NewProxy via registerSESMITMHandlers, it is invoked
// after budget enforcement handlers and before the general CONNECT handler.
func WithSESMITM() ProxyOption {
	return func(proxy *goproxy.ProxyHttpServer, _ *proxyConfig) {
		registerSESMITMHandlers(proxy, "")
	}
}

// registerSESMITMHandlers registers the MitmConnect handler for SES hosts.
// sandboxID is used for logging; pass "" when called without a sandbox context.
// Exported for reuse by transparent.go if needed; package-internal via NewProxy.
func registerSESMITMHandlers(proxy *goproxy.ProxyHttpServer, sandboxID string) {
	// MitmConnect for email.*.amazonaws.com — must be registered BEFORE the
	// general CONNECT handler so goproxy first-match routes SES through MITM.
	proxy.OnRequest(goproxy.ReqHostMatches(sesHostRegex)).HandleConnectFunc(
		func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
			log.Info().
				Str("event_type", "ses_mitm_connect").
				Str("sandbox_id", sandboxID).
				Str("host", host).
				Msg("")
			return goproxy.MitmConnect, host
		},
	)
}
