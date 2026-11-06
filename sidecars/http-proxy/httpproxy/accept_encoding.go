package httpproxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/elazarl/goproxy"
	"github.com/rs/zerolog/log"
)

// Accept-Encoding handling.
//
// goproxy's RemoveProxyHeaders (run after every OnRequest handler, on both the
// plain-HTTP and the MITM path) deletes the client's Accept-Encoding unless
// KeepAcceptEncoding is set, and Go's http.Transport then adds its own "gzip"
// and transparently decompresses the reply. That is what lets the metering
// handlers parse plain bodies, and it is harmless for every client that does
// not sign the header.
//
// It is NOT harmless for a SigV4 client that signs accept-encoding — the
// Anthropic Python SDK over httpx does ("gzip, deflate"); botocore and Claude
// Code do not. AWS then sees "gzip", recomputes a different signature and
// answers 403 "The request signature we calculated does not match".
//
// So the proxy keeps the header itself (KeepAcceptEncoding) and the first
// OnRequest handler deletes it again for every request except one whose SigV4
// Authorization lists accept-encoding in SignedHeaders. Everything else is
// exactly as before. For the preserved case the transport no longer
// decompresses, the client gets the upstream bytes untouched, and metering
// decodes a copy (decodeForMetering).
//
// The transparent (eBPF-redirected) path in transparent.go writes the client's
// request verbatim and never touched Accept-Encoding, so it needs no gate.

// registerAcceptEncodingGate must run before any other OnRequest handler is
// registered, so no request can reach the transport without passing it.
func registerAcceptEncodingGate(proxy *goproxy.ProxyHttpServer) {
	proxy.KeepAcceptEncoding = true
	proxy.OnRequest().DoFunc(func(req *http.Request, _ *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		if !sigV4SignsAcceptEncoding(req) {
			req.Header.Del("Accept-Encoding")
		}
		return req, nil
	})
}

// sigV4SignsAcceptEncoding reports whether req carries an AWS SigV4
// Authorization header whose SignedHeaders list includes accept-encoding.
func sigV4SignsAcceptEncoding(req *http.Request) bool {
	authz := req.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "AWS4-HMAC-SHA256 ") {
		return false
	}
	_, list, ok := strings.Cut(authz, "SignedHeaders=")
	if !ok {
		return false
	}
	if i := strings.IndexAny(list, ", "); i >= 0 {
		list = list[:i]
	}
	for _, h := range strings.Split(list, ";") {
		if strings.EqualFold(h, "accept-encoding") {
			return true
		}
	}
	return false
}

var errUnknownEncoding = errors.New("unsupported content-encoding")

// decodeForMetering returns body decoded per the Content-Encoding value. An
// empty or identity encoding returns body as is — the common case, since the
// transport already decompressed every response it asked to be compressed.
// Codings are undone last-applied first. Output is capped at
// maxResponseBodySize, the same bound the metering reader captures.
func decodeForMetering(body []byte, contentEncoding string) ([]byte, error) {
	codings := strings.Split(contentEncoding, ",")
	out := body
	for i := len(codings) - 1; i >= 0; i-- {
		var r io.Reader
		var err error
		switch c := strings.ToLower(strings.TrimSpace(codings[i])); c {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			r, err = gzip.NewReader(bytes.NewReader(out))
		case "deflate":
			// RFC 9110 deflate is zlib-wrapped; some servers send raw DEFLATE.
			if r, err = zlib.NewReader(bytes.NewReader(out)); err != nil {
				r, err = flate.NewReader(bytes.NewReader(out)), nil
			}
		default:
			return nil, fmt.Errorf("%w: %q", errUnknownEncoding, c)
		}
		if err != nil {
			return nil, err
		}
		if out, err = io.ReadAll(io.LimitReader(r, int64(maxResponseBodySize))); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// meterDecoded wraps a metering callback so it sees the decoded body. The
// client's bytes are never touched — the metering reader only tees a copy.
// An encoding it cannot decode logs <provider>_unknown_encoding (a decode
// failure, <provider>_decode_error) and skips metering rather than feed a
// parser compressed bytes.
func meterDecoded(resp *http.Response, sandboxID, provider string, fn func([]byte)) func([]byte) {
	ce := resp.Header.Get("Content-Encoding")
	return func(captured []byte) {
		decoded, err := decodeForMetering(captured, ce)
		if err != nil {
			event := provider + "_decode_error"
			if errors.Is(err, errUnknownEncoding) {
				event = provider + "_unknown_encoding"
			}
			log.Warn().
				Str("sandbox_id", sandboxID).
				Str("event_type", event).
				Str("content_encoding", ce).
				Err(err).
				Msg("response not metered")
			return
		}
		fn(decoded)
	}
}
