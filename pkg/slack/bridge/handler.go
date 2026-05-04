package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/whereiskurt/klankrmkr/pkg/slack"
)

// logger is the package-level structured logger. Lambda ships stderr to
// CloudWatch automatically — no additional log-group configuration needed.
// The logger is replaced in tests via SetLogger.
var logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

// SetLogger replaces the package logger. Used in tests to capture output.
func SetLogger(l *slog.Logger) { logger = l }

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

	// Phase 68 — ActionUpload support. These three fields are zero-valued for
	// the existing post/archive/test paths and only consulted in the
	// ActionUpload dispatch case; tests for the legacy paths can leave them
	// unset.
	S3Getter          S3ObjectGetter
	FileUploader      SlackFileUploader
	MissingFilesWrite bool // set by main.go cold-start scope probe
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
		logger.WarnContext(ctx, "bridge: bad_envelope", "step", "parse", "error", err.Error(), "status", 400)
		return errResp(400, "bad_envelope")
	}
	if env.Action == "" || env.SenderID == "" || env.Channel == "" || env.Nonce == "" || env.Timestamp == 0 || env.Version == 0 {
		logger.WarnContext(ctx, "bridge: missing_fields", "step", "parse", "action", env.Action, "sender_id", env.SenderID, "status", 400)
		return errResp(400, "missing_fields")
	}
	if env.Version != slack.EnvelopeVersion {
		logger.WarnContext(ctx, "bridge: unsupported_version", "step", "parse", "version", env.Version, "status", 400)
		return errResp(400, "unsupported_version")
	}
	if env.Action != slack.ActionPost && env.Action != slack.ActionArchive && env.Action != slack.ActionTest && env.Action != slack.ActionUpload {
		logger.WarnContext(ctx, "bridge: unknown_action", "step", "parse", "action", env.Action, "status", 400)
		return errResp(400, "unknown_action")
	}

	// Truncate nonce for log — first 8 chars sufficient for correlation.
	noncePrefix := env.Nonce
	if len(noncePrefix) > 8 {
		noncePrefix = noncePrefix[:8]
	}

	logger.InfoContext(ctx, "bridge: request",
		"action", env.Action,
		"sender_id", env.SenderID,
		"channel", env.Channel,
		"nonce_prefix", noncePrefix,
	)

	// Header sender consistency (defense in depth — sig still verifies the body).
	headerSender := req.Headers["X-KM-Sender-ID"]
	if headerSender == "" {
		headerSender = req.Headers["x-km-sender-id"]
	}
	if headerSender != "" && headerSender != env.SenderID {
		logger.WarnContext(ctx, "bridge: sender_header_mismatch",
			"step", "header_check",
			"header_sender", headerSender,
			"envelope_sender", env.SenderID,
			"status", 401,
		)
		return errResp(401, "sender_header_mismatch")
	}

	// Step 2 — replay-timestamp.
	now := h.Now()
	skew := now.Unix() - env.Timestamp
	if skew < 0 {
		skew = -skew
	}
	if skew > MaxClockSkewSeconds {
		logger.WarnContext(ctx, "bridge: stale_timestamp",
			"step", "timestamp",
			"skew_seconds", skew,
			"envelope_ts", env.Timestamp,
			"status", 401,
		)
		return errResp(401, "stale_timestamp")
	}

	// Step 3 — replay-nonce.
	if err := h.Nonces.Reserve(ctx, env.Nonce, NonceTTLSeconds); err != nil {
		if errors.Is(err, ErrNonceReplayed) {
			logger.WarnContext(ctx, "bridge: replayed_nonce",
				"step", "nonce",
				"nonce_prefix", noncePrefix,
				"sender_id", env.SenderID,
				"status", 401,
			)
			return errResp(401, "replayed_nonce")
		}
		logger.ErrorContext(ctx, "bridge: nonce_unavailable",
			"step", "nonce",
			"error", err.Error(),
			"status", 500,
		)
		return errResp(500, "nonce_unavailable")
	}

	// Step 4 — public key.
	pub, err := h.Keys.Fetch(ctx, env.SenderID)
	if err != nil {
		if errors.Is(err, ErrSenderNotFound) {
			logger.WarnContext(ctx, "bridge: unknown_sender",
				"step", "key_fetch",
				"sender_id", env.SenderID,
				"status", 404,
			)
			return errResp(404, "unknown_sender")
		}
		logger.ErrorContext(ctx, "bridge: key_lookup_failed",
			"step", "key_fetch",
			"sender_id", env.SenderID,
			"error", err.Error(),
			"status", 500,
		)
		return errResp(500, "key_lookup_failed")
	}

	// Step 5 — signature.
	sigB64 := req.Headers["X-KM-Signature"]
	if sigB64 == "" {
		sigB64 = req.Headers["x-km-signature"]
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		logger.WarnContext(ctx, "bridge: bad_signature_encoding",
			"step", "signature",
			"sender_id", env.SenderID,
			"error", err.Error(),
			"status", 401,
		)
		return errResp(401, "bad_signature_encoding")
	}
	if err := slack.VerifyEnvelope(&env, sig, pub); err != nil {
		logger.WarnContext(ctx, "bridge: bad_signature",
			"step", "signature",
			"sender_id", env.SenderID,
			"error", err.Error(),
			"status", 401,
		)
		return errResp(401, "bad_signature")
	}

	// Step 6 — action authorization.
	isOperator := env.SenderID == slack.SenderOperator
	if !isOperator {
		if env.Action == slack.ActionArchive || env.Action == slack.ActionTest {
			logger.WarnContext(ctx, "bridge: sandbox_action_forbidden",
				"step", "authz",
				"action", env.Action,
				"sender_id", env.SenderID,
				"status", 403,
			)
			return errResp(403, "sandbox_action_forbidden")
		}
		// Sandbox post: channel must match owned channel.
		owned, err := h.Channels.OwnedChannel(ctx, env.SenderID)
		if err != nil {
			logger.ErrorContext(ctx, "bridge: channel_lookup_failed",
				"step", "authz",
				"sender_id", env.SenderID,
				"error", err.Error(),
				"status", 500,
			)
			return errResp(500, "channel_lookup_failed")
		}
		if owned == "" || owned != env.Channel {
			logger.WarnContext(ctx, "bridge: channel_mismatch",
				"step", "authz",
				"sender_id", env.SenderID,
				"channel", env.Channel,
				"owned_channel", owned,
				"status", 403,
			)
			return errResp(403, "channel_mismatch")
		}
	}

	// Step 7 — bot token.
	if _, err := h.Token.Fetch(ctx); err != nil {
		logger.ErrorContext(ctx, "bridge: bot_token_unavailable",
			"step", "token_fetch",
			"error", err.Error(),
			"status", 500,
		)
		return errResp(500, "bot_token_unavailable")
	}

	// Dispatch.
	switch env.Action {
	case slack.ActionPost, slack.ActionTest:
		ts, err := h.Slack.PostMessage(ctx, env.Channel, env.Subject, env.Body, env.ThreadTS)
		resp := slackResponse(ts, err)
		if err != nil {
			logger.ErrorContext(ctx, "bridge: slack_call_failed",
				"step", "dispatch",
				"action", env.Action,
				"channel", env.Channel,
				"slack_error", err.Error(),
				"status", resp.StatusCode,
			)
		} else {
			logger.InfoContext(ctx, "bridge: ok", "action", env.Action, "channel", env.Channel, "ts", ts, "status", 200)
		}
		return resp
	case slack.ActionArchive:
		err := h.Slack.ArchiveChannel(ctx, env.Channel)
		resp := slackResponse("", err)
		if err != nil {
			logger.ErrorContext(ctx, "bridge: slack_call_failed",
				"step", "dispatch",
				"action", env.Action,
				"channel", env.Channel,
				"slack_error", err.Error(),
				"status", resp.StatusCode,
			)
		} else {
			logger.InfoContext(ctx, "bridge: ok", "action", env.Action, "channel", env.Channel, "status", 200)
		}
		return resp
	case slack.ActionUpload:
		// Phase 68 — stream a transcript object from S3 directly into Slack's
		// 3-step file upload flow (no full-buffering in Lambda memory).
		//
		// Validation order (CONTEXT.md, must_haves.truths): cold-start scope
		// gate first; then envelope-level validations (cheap, fail-fast) before
		// any AWS call. S3 GetObject is the first AWS-side step; if it fails
		// we surface 502.

		// 0. Cold-start scope gate.
		if h.MissingFilesWrite {
			logger.WarnContext(ctx, "bridge: scope_missing",
				"step", "dispatch",
				"action", env.Action,
				"status", 400,
			)
			return errResp(400, "bot lacks files:write — operator must re-auth Slack App")
		}

		// 1. S3Key prefix must match transcripts/{senderID}/ — defense in
		//    depth even after channel-ownership check above.
		expectedPrefix := "transcripts/" + env.SenderID + "/"
		if !strings.HasPrefix(env.S3Key, expectedPrefix) {
			logger.WarnContext(ctx, "bridge: s3_key_prefix_mismatch",
				"step", "dispatch",
				"sender_id", env.SenderID,
				"s3_key", env.S3Key,
				"status", 403,
			)
			return errResp(403, "s3_key_prefix_mismatch")
		}
		// 2. Filename must be safe.
		if env.Filename == "" || len(env.Filename) > 255 || strings.ContainsAny(env.Filename, "/\x00") {
			logger.WarnContext(ctx, "bridge: filename_invalid",
				"step", "dispatch",
				"filename_len", len(env.Filename),
				"status", 400,
			)
			return errResp(400, "filename_invalid")
		}
		// 3. ContentType allow-list.
		switch env.ContentType {
		case "application/gzip", "application/json", "text/plain":
		default:
			logger.WarnContext(ctx, "bridge: content_type_not_allowed",
				"step", "dispatch",
				"content_type", env.ContentType,
				"status", 400,
			)
			return errResp(400, "content_type_not_allowed")
		}
		// 4. SizeBytes in (0, 100MB].
		const maxUploadBytes = 100 * 1024 * 1024
		if env.SizeBytes <= 0 || env.SizeBytes > maxUploadBytes {
			logger.WarnContext(ctx, "bridge: size_invalid",
				"step", "dispatch",
				"size_bytes", env.SizeBytes,
				"status", 400,
			)
			return errResp(400, "size_invalid")
		}
		// 5. Channel non-empty (defensive — header-level missing_fields would
		//    normally have caught this; keep parity with CONTEXT.md spec).
		if env.Channel == "" {
			return errResp(400, "channel_empty")
		}

		// 6. Stream S3 → Slack.
		body, _, err := h.S3Getter.GetObject(ctx, env.S3Key)
		if err != nil {
			logger.WarnContext(ctx, "bridge: s3_get_failed",
				"step", "dispatch",
				"s3_key", env.S3Key,
				"error", err.Error(),
				"status", 502,
			)
			return errResp(502, "s3_get_failed")
		}
		defer body.Close()

		fileID, permalink, err := h.FileUploader.UploadFile(ctx, env.Channel, env.ThreadTS, env.Filename, env.ContentType, env.SizeBytes, body)
		if err != nil {
			logger.WarnContext(ctx, "bridge: upload_failed",
				"step", "dispatch",
				"channel", env.Channel,
				"error", err.Error(),
				"status", 502,
			)
			return errResp(502, "upload_failed")
		}

		logger.InfoContext(ctx, "bridge: ok",
			"action", env.Action,
			"channel", env.Channel,
			"file_id", fileID,
			"size_bytes", env.SizeBytes,
			"status", 200,
		)
		return jsonResp(200, map[string]any{"ok": true, "file_id": fileID, "permalink": permalink})
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
