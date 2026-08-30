package httpproxy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/elazarl/goproxy"
	"github.com/rs/zerolog/log"
	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

// Intercept is a single declarative host->action rule, transported to the
// sidecar as one element of the KM_MITM_INTERCEPTS JSON array (base64-encoded
// in the systemd drop-in — see ParseIntercepts). This is the WIRE contract:
// a FLAT shape with no "action" nesting, and no "enabled" field — the
// compiler drops disabled rules before they ever reach the box.
//
// Exactly one of Redirect or Respond must be set; ParseIntercepts drops any
// entry that sets neither or both.
type Intercept struct {
	Name     string   `json:"name"`
	Hosts    []string `json:"hosts"`
	Redirect string   `json:"redirect,omitempty"`
	Respond  *Respond `json:"respond,omitempty"`
}

// Respond is the canned-response action of an Intercept.
type Respond struct {
	Status      int    `json:"status"`
	ContentType string `json:"contentType,omitempty"`
	Body        string `json:"body"`
}

// MatchesHost reports whether host matches any entry in patterns.
//
// This reproduces IsHostAllowed's semantics — the port is stripped, matching
// is case-insensitive, a leading "." matches both the bare apex and any
// subdomain, and otherwise the entry must match host exactly — with ONE
// deliberate omission: "*" is NOT honoured as a match-all here. An intercept
// is a targeted rule; a match-all intercept would silently terminate every
// request at the proxy while looking like a harmless allowlist idiom, so
// wildcard entries are simply never satisfied by this matcher.
//
// This is also narrower than the hardcoded easter-egg pattern it replaces:
// that pattern was an unanchored `^(www\.)?google\.com`, so a lookalike host
// such as "google.com.evil.example" matched it too. MatchesHost requires an
// exact apex/subdomain relationship, so that lookalike no longer matches —
// a bug fix, not a regression.
func MatchesHost(host string, patterns []string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		// No port present — use as-is.
		h = host
	}
	h = strings.ToLower(h)
	for _, p := range patterns {
		if p == "*" {
			// Deliberately not honoured — see the doc comment above.
			continue
		}
		p = strings.ToLower(p)
		if strings.HasPrefix(p, ".") {
			if h == p[1:] || strings.HasSuffix(h, p) {
				return true
			}
		} else if p == h {
			return true
		}
	}
	return false
}

// MatchIntercept returns the first Intercept in ics whose Hosts match host,
// or nil when none match. Declaration order in the profile is the precedence
// order between intercepts.
func MatchIntercept(host string, ics []Intercept) *Intercept {
	for i := range ics {
		if MatchesHost(host, ics[i].Hosts) {
			return &ics[i]
		}
	}
	return nil
}

// ParseIntercepts decodes the KM_MITM_INTERCEPTS env value: base64-encoded
// JSON carrying a flat []Intercept array.
//
// An empty raw value returns (nil, nil) — no rules, no error. A base64 or
// JSON decode failure returns (nil, err): the WHOLE rule set is discarded,
// never half of it, because a partially-applied rule set could redirect
// traffic the operator never asked to redirect.
//
// After a successful unmarshal, any entry that cannot produce a safe action
// (zero hosts; neither Redirect nor Respond set; both set) is dropped with a
// warning log naming the rule, and the rest are kept.
func ParseIntercepts(raw string) ([]Intercept, error) {
	if raw == "" {
		return nil, nil
	}

	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("KM_MITM_INTERCEPTS is not valid base64: %w", err)
	}

	var ics []Intercept
	if err := json.Unmarshal(decoded, &ics); err != nil {
		return nil, fmt.Errorf("KM_MITM_INTERCEPTS is not valid JSON: %w", err)
	}

	var out []Intercept
	for _, ic := range ics {
		if len(ic.Hosts) == 0 {
			log.Warn().
				Str("event_type", "mitm_intercept_dropped").
				Str("intercept", ic.Name).
				Msg("dropping intercept with zero hosts")
			continue
		}
		hasRedirect := ic.Redirect != ""
		hasRespond := ic.Respond != nil
		if hasRedirect == hasRespond {
			// Either neither action is set, or both are — both are unsafe:
			// an intercept must have exactly one action.
			log.Warn().
				Str("event_type", "mitm_intercept_dropped").
				Str("intercept", ic.Name).
				Msg("dropping intercept with neither or both of redirect/respond set")
			continue
		}
		out = append(out, ic)
	}
	return out, nil
}

// WithIntercepts registers operator-declared intercepts on the proxy. A nil
// or empty slice sets nothing, so the option is a no-op and registration
// never happens — mirroring WithDeniedHosts.
func WithIntercepts(ics []Intercept) ProxyOption {
	return func(_ *goproxy.ProxyHttpServer, cfg *proxyConfig) {
		if len(ics) == 0 {
			return
		}
		cfg.intercepts = ics
	}
}

// InterceptResponse builds the canned http.Response for a matched Intercept.
//
// Redirect form: a 301 with a Location header set to the target and a short
// generic moved-permanently body naming it. The previous hardcoded easter-egg
// handler also emitted a fake upstream Server header to imitate the
// intercepted site; that cosplay is deliberately not reproduced here, so the
// response now honestly identifies as proxy-generated.
//
// Respond form: the declared status and body, with content type defaulting
// to text/plain when the rule leaves it empty.
func InterceptResponse(req *http.Request, ic *Intercept) *http.Response {
	if ic.Redirect != "" {
		body := `<HTML><HEAD><meta http-equiv="content-type" content="text/html;charset=utf-8">
<TITLE>301 Moved</TITLE></HEAD><BODY>
<H1>301 Moved</H1>
The document has moved
<A HREF="` + ic.Redirect + `">here</A>.
</BODY></HTML>`
		resp := goproxy.NewResponse(req, "text/html; charset=utf-8", http.StatusMovedPermanently, body)
		resp.Header.Set("Location", ic.Redirect)
		return resp
	}

	contentType := "text/plain"
	status := http.StatusOK
	body := ""
	if ic.Respond != nil {
		if ic.Respond.ContentType != "" {
			contentType = ic.Respond.ContentType
		}
		if ic.Respond.Status != 0 {
			status = ic.Respond.Status
		}
		body = ic.Respond.Body
	}
	return goproxy.NewResponse(req, contentType, status, body)
}

// isPlatformOwnedHost reports whether host is claimed by one of the
// platform's own hardcoded MITM handlers — Bedrock/Anthropic/OpenAI metering,
// or the GitHub repo filter — so that registerInterceptHandlers can refuse to
// shadow them even though it is otherwise a plain, unconditional handler.
//
// meteringEnabled/githubEnabled mirror the exact guards NewProxy already uses
// to decide whether those handlers are registered at all: an intercept for
// api.anthropic.com is not "platform-owned" on a sandbox with no budget
// enforcement, because nothing else would ever answer for that host anyway.
func isPlatformOwnedHost(host string, meteringEnabled, githubEnabled bool) bool {
	if meteringEnabled {
		if bedrockHostRegex.MatchString(host) ||
			anthropicHostRegex.MatchString(host) ||
			openaiHostRegex.MatchString(host) {
			return true
		}
	}
	if githubEnabled && githubHostsRegex.MatchString(host) {
		return true
	}
	return false
}

// registerInterceptHandlers registers the operator-intercept CONNECT and
// request handlers on proxy. Both handlers fall through (return nil / nil
// response) on a miss — the deny gate in NewProxy is the model for this.
//
// meteringEnabled and githubEnabled gate the isPlatformOwnedHost check: they
// must be true exactly when NewProxy has ALSO registered the corresponding
// metering / GitHub repo-filter handlers, so that an intercept can never
// shadow a platform handler that is actually live, while still being free to
// fire for those same hostnames on a sandbox where the platform handler was
// never registered in the first place.
func registerInterceptHandlers(proxy *goproxy.ProxyHttpServer, ics []Intercept, sandboxID string, resolver *pidResolver, meteringEnabled, githubEnabled bool, flows *flowlog.Writer) {
	proxy.OnRequest().HandleConnectFunc(
		func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
			if isPlatformOwnedHost(host, meteringEnabled, githubEnabled) {
				return nil, host
			}
			if MatchIntercept(host, ics) == nil {
				return nil, host
			}
			return goproxy.MitmConnect, host
		},
	)

	proxy.OnRequest().DoFunc(
		func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
			if isPlatformOwnedHost(req.Host, meteringEnabled, githubEnabled) {
				return req, nil
			}
			ic := MatchIntercept(req.Host, ics)
			if ic == nil {
				return req, nil
			}
			action := "respond"
			if ic.Redirect != "" {
				action = "redirect"
			}
			log.Info().
				Str("sandbox_id", sandboxID).
				Str("event_type", "mitm_intercept").
				Str("intercept", ic.Name).
				Str("host", req.Host).
				Str("action", action).
				Msg("")
			// Redirect, not deny, for BOTH the redirect and respond actions: the
			// destination matched here is reachable-by-policy (the operator wrote
			// a rule for it), it just answered via policy instead of the real
			// host. Recording it as deny would exclude it from the pinnable set
			// and break the intercept the first time this sandbox gets pinned.
			recordFlow(flows, resolver, flowlog.VerdictRedirect, req.Host, ctx)
			return req, InterceptResponse(req, ic)
		},
	)
}
