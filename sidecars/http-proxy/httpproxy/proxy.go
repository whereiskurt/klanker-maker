// Package httpproxy provides HTTP/HTTPS CONNECT proxy logic for the km sidecar.
// It enforces a host allowlist and injects W3C traceparent headers on allowed
// outbound CONNECT requests.
package httpproxy

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/elazarl/goproxy"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

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
func NewProxy(allowed []string, sandboxID string) *goproxy.ProxyHttpServer {
	// Ensure W3C TraceContext propagation is registered.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = false

	// CONNECT (HTTPS) handler.
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
