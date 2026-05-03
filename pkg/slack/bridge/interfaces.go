package bridge

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
)

// ErrNonceReplayed is returned by NonceStore.Reserve when the nonce already
// exists (replay).
var ErrNonceReplayed = errors.New("bridge: nonce replayed")

// ErrSenderNotFound is returned by PublicKeyFetcher.Fetch when the sender_id
// has no entry in the km-identities DynamoDB table.
var ErrSenderNotFound = errors.New("bridge: sender not found")

// ErrSlackRateLimited is the typed error SlackPoster implementations return
// when Slack responds with a 429. Bridge surfaces this as HTTP 503 with a
// Retry-After header so km-slack's retry loop can honor it.
type ErrSlackRateLimited struct {
	RetryAfterSeconds int
	Method            string
}

func (e *ErrSlackRateLimited) Error() string {
	return "slack rate-limited"
}

// PublicKeyFetcher resolves senderID to an Ed25519 public key. Production
// implementation calls pkg/aws.FetchPublicKey against DynamoDB km-identities
// (RESEARCH.md correction #1: NOT SSM). Returns ErrSenderNotFound for unknown
// senders.
type PublicKeyFetcher interface {
	Fetch(ctx context.Context, senderID string) (ed25519.PublicKey, error)
}

// NonceStore atomically inserts nonce with TTL. Returns ErrNonceReplayed if
// the nonce already exists. Production implementation uses DynamoDB
// km-slack-bridge-nonces with ConditionExpression attribute_not_exists.
type NonceStore interface {
	Reserve(ctx context.Context, nonce string, ttlSeconds int) error
}

// ChannelOwnershipFetcher returns the slack_channel_id stored on the sandbox
// metadata record. Empty string + nil error means the sandbox has no channel
// configured — caller must reject any sandbox post.
type ChannelOwnershipFetcher interface {
	OwnedChannel(ctx context.Context, sandboxID string) (string, error)
}

// BotTokenFetcher returns the Slack bot token. Production implementation
// reads SSM /km/slack/bot-token (SecureString, KMS-decrypted).
type BotTokenFetcher interface {
	Fetch(ctx context.Context) (string, error)
}

// SlackPoster is the narrow Slack-API surface the handler needs. Production
// implementation is a *slack.Client from pkg/slack (lazy-rebuilt per cold
// start using the BotTokenFetcher). Errors of type *ErrSlackRateLimited are
// recognized specially and surfaced as 503 + Retry-After.
type SlackPoster interface {
	PostMessage(ctx context.Context, channel, subject, body, threadTS string) (string, error)
	ArchiveChannel(ctx context.Context, channelID string) error
}

// S3ObjectGetter abstracts S3 GetObject for streaming transcripts to Slack
// without buffering the full body in Lambda memory. Phase 68.
//
// Production implementation: S3GetterAdapter (aws_adapters.go) wraps an
// *s3.Client and pulls from KM_ARTIFACTS_BUCKET. Caller MUST Close() the
// returned reader.
type S3ObjectGetter interface {
	// GetObject returns the body stream and Content-Length for the given key.
	GetObject(ctx context.Context, key string) (body io.ReadCloser, contentLength int64, err error)
}

// SlackFileUploader abstracts the 3-step Slack file upload flow (Plan 04
// pkg/slack.Client.UploadFile). Phase 68.
//
// Production implementation: SlackFileUploaderAdapter (aws_adapters.go) wraps
// a *slack.Client and forwards through UploadFile, returning fileID + permalink
// for inclusion in the bridge response body.
type SlackFileUploader interface {
	UploadFile(ctx context.Context, channel, threadTS, filename, contentType string, sizeBytes int64, body io.Reader) (fileID, permalink string, err error)
}
