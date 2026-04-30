package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/whereiskurt/klankrmkr/pkg/slack"
)

// MaxClockSkewSeconds is the half-window for replay-timestamp protection.
const MaxClockSkewSeconds = 300

// NonceTTLSeconds matches the DynamoDB nonce table TTL.
const NonceTTLSeconds = 600

// Request is the bridge HTTP request shape.
type Request struct {
	Body    string            // JSON-encoded SlackEnvelope
	Headers map[string]string // X-KM-Sender-ID, X-KM-Signature, Content-Type
}

// Response is the bridge HTTP response shape.
type Response struct {
	StatusCode int               `json:"statusCode"`
	Body       string            `json:"body"`
	Headers    map[string]string `json:"headers,omitempty"`
}

// Handler holds the injectable dependencies. Plan 06 wires production
// implementations; tests inject in-memory fakes.
type Handler struct {
	Now      func() time.Time
	Keys     PublicKeyFetcher
	Nonces   NonceStore
	Channels ChannelOwnershipFetcher
	Token    BotTokenFetcher
	Slack    SlackPoster
}

// jsonResp builds a Response with a JSON body.
func jsonResp(status int, payload any, extraHeaders ...map[string]string) *Response {
	b, _ := json.Marshal(payload)
	h := map[string]string{"Content-Type": "application/json"}
	for _, m := range extraHeaders {
		for k, v := range m {
			h[k] = v
		}
	}
	return &Response{StatusCode: status, Body: string(b), Headers: h}
}

func errResp(status int, code string) *Response {
	return jsonResp(status, map[string]any{"ok": false, "error": code})
}

// Handle runs the seven verification steps. Pure function (mod the injected
// interfaces); no global state.
func (h *Handler) Handle(ctx context.Context, req *Request) *Response {
	// Step 1 — parse envelope.
	var env slack.SlackEnvelope
	if err := json.Unmarshal([]byte(req.Body), &env); err != nil {
		return errResp(400, "bad_envelope")
	}
	if env.Action == "" || env.SenderID == "" || env.Channel == "" || env.Nonce == "" || env.Timestamp == 0 || env.Version == 0 {
		return errResp(400, "missing_fields")
	}
	if env.Version != slack.EnvelopeVersion {
		return errResp(400, "unsupported_version")
	}
	if env.Action != slack.ActionPost && env.Action != slack.ActionArchive && env.Action != slack.ActionTest {
		return errResp(400, "unknown_action")
	}

	// Header sender consistency (defense in depth — sig still verifies the body).
	headerSender := req.Headers["X-KM-Sender-ID"]
	if headerSender == "" {
		headerSender = req.Headers["x-km-sender-id"]
	}
	if headerSender != "" && headerSender != env.SenderID {
		return errResp(401, "sender_header_mismatch")
	}

	// Step 2 — replay-timestamp.
	now := h.Now()
	skew := now.Unix() - env.Timestamp
	if skew < 0 {
		skew = -skew
	}
	if skew > MaxClockSkewSeconds {
		return errResp(401, "stale_timestamp")
	}

	// Step 3 — replay-nonce.
	if err := h.Nonces.Reserve(ctx, env.Nonce, NonceTTLSeconds); err != nil {
		if errors.Is(err, ErrNonceReplayed) {
			return errResp(401, "replayed_nonce")
		}
		return errResp(500, "nonce_unavailable")
	}

	// Step 4 — public key.
	pub, err := h.Keys.Fetch(ctx, env.SenderID)
	if err != nil {
		if errors.Is(err, ErrSenderNotFound) {
			return errResp(404, "unknown_sender")
		}
		return errResp(500, "key_lookup_failed")
	}

	// Step 5 — signature.
	sigB64 := req.Headers["X-KM-Signature"]
	if sigB64 == "" {
		sigB64 = req.Headers["x-km-signature"]
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return errResp(401, "bad_signature_encoding")
	}
	if err := slack.VerifyEnvelope(&env, sig, pub); err != nil {
		return errResp(401, "bad_signature")
	}

	// Step 6 — action authorization.
	isOperator := env.SenderID == slack.SenderOperator
	if !isOperator {
		if env.Action == slack.ActionArchive || env.Action == slack.ActionTest {
			return errResp(403, "sandbox_action_forbidden")
		}
		// Sandbox post: channel must match owned channel.
		owned, err := h.Channels.OwnedChannel(ctx, env.SenderID)
		if err != nil {
			return errResp(500, "channel_lookup_failed")
		}
		if owned == "" || owned != env.Channel {
			return errResp(403, "channel_mismatch")
		}
	}

	// Step 7 — bot token.
	if _, err := h.Token.Fetch(ctx); err != nil {
		return errResp(500, "bot_token_unavailable")
	}

	// Dispatch.
	switch env.Action {
	case slack.ActionPost, slack.ActionTest:
		ts, err := h.Slack.PostMessage(ctx, env.Channel, env.Subject, env.Body, env.ThreadTS)
		return slackResponse(ts, err)
	case slack.ActionArchive:
		err := h.Slack.ArchiveChannel(ctx, env.Channel)
		return slackResponse("", err)
	}
	return errResp(500, "internal") // unreachable
}

// slackResponse maps a Slack-call result to the bridge HTTP response.
func slackResponse(ts string, err error) *Response {
	if err == nil {
		return jsonResp(200, map[string]any{"ok": true, "ts": ts})
	}
	var rl *ErrSlackRateLimited
	if errors.As(err, &rl) {
		retry := strconv.Itoa(rl.RetryAfterSeconds)
		return jsonResp(503, map[string]any{"ok": false, "error": "rate_limited"},
			map[string]string{"Retry-After": retry})
	}
	// Surface Slack error code if present in the err message
	// (e.g. "slack chat.postMessage: channel_not_found").
	code := "slack_upstream"
	if msg := err.Error(); strings.Contains(msg, ":") {
		parts := strings.Split(msg, ":")
		code = strings.TrimSpace(parts[len(parts)-1])
	}
	return jsonResp(502, map[string]any{"ok": false, "error": code})
}
