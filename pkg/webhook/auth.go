package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ErrUnauthorized is returned for every authentication failure. The caller logs
// it and returns 200 with no dispatch — never a 401, which tells a prober
// exactly what it wants to know and makes some senders retry.
var ErrUnauthorized = errors.New("webhook: unauthorized")

// DefaultAuthHeader is used when a source omits auth.header.
const DefaultAuthHeader = "Authorization"

// Authenticate verifies an inbound request against a source's auth config.
// headers MUST be lowercase-keyed (Lambda Function URL delivers them that way).
//
// Fails closed on every ambiguity: unknown type, empty configured secret, and
// missing header. The empty-secret case is the one worth naming — a bare
// constant-time compare of "" against "" SUCCEEDS, so an SSM parameter that
// resolved to empty would silently open the endpoint to anyone.
func Authenticate(auth config.WebhookAuth, secret string, headers map[string]string, body []byte) error {
	if secret == "" {
		return ErrUnauthorized
	}

	name := auth.Header
	if name == "" {
		name = DefaultAuthHeader
	}
	provided, ok := headers[strings.ToLower(name)]
	if !ok || provided == "" {
		return ErrUnauthorized
	}

	switch strings.ToLower(auth.Type) {
	case "bearer":
		token := strings.TrimSpace(strings.TrimPrefix(provided, "Bearer "))
		if subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1 {
			return nil
		}
		return ErrUnauthorized

	case "hmac":
		sig := strings.TrimSpace(strings.TrimPrefix(provided, "sha256="))
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		if subtle.ConstantTimeCompare([]byte(sig), []byte(want)) == 1 {
			return nil
		}
		return ErrUnauthorized

	default:
		return ErrUnauthorized
	}
}
