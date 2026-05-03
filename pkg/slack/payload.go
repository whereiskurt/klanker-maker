// Package slack provides the Phase 63 Slack-notify primitives: envelope
// construction, canonical JSON serialization, Ed25519 sign/verify, and a thin
// Slack Web API client. Used by the sandbox-side km-slack binary, the
// km-slack-bridge Lambda, and operator-side km slack commands.
package slack

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxBodyBytes is the per-message body cap, matching Slack's chat.postMessage
// 40 KB text limit. Enforced client-side (km-slack) before signing and again
// at the bridge to defeat over-large signed payloads.
const MaxBodyBytes = 40 * 1024

// EnvelopeVersion is the integer ABI version stamped on every payload. Phase
// 63 ships v1; future major changes (v2 closed-loop) bump this.
const EnvelopeVersion = 1

// Allowed actions on the bridge envelope.
const (
	ActionPost    = "post"
	ActionArchive = "archive"
	ActionTest    = "test"
	ActionUpload  = "upload"
)

// SenderOperator is the canonical sender_id for operator-signed envelopes.
const SenderOperator = "operator"

// ErrBodyTooLarge is returned by BuildEnvelope when body exceeds MaxBodyBytes.
var ErrBodyTooLarge = errors.New("slack: body exceeds 40KB limit")

// Errors returned by BuildEnvelopeUpload. Named so downstream callers (Plan
// 05 km-slack upload, Plan 08 bridge handler) can match precisely.
var (
	ErrUploadFilenameInvalid = errors.New("slack: upload filename invalid (must be non-empty, ≤255 bytes, no slash, no NUL)")
	ErrUploadS3KeyEmpty      = errors.New("slack: upload s3_key required")
	ErrUploadSizeInvalid     = errors.New("slack: upload size_bytes must be > 0")
	ErrUploadChannelEmpty    = errors.New("slack: upload channel required")
)

// SlackEnvelope is the JSON shape of the bridge request. Fields are tagged
// alphabetically so encoding/json produces deterministic canonical bytes for
// signing — both sender (km-slack / operator) and verifier (bridge Lambda)
// import this struct.
//
// Phase 68 added four upload-specific fields (content_type, filename, s3_key,
// size_bytes) inserted in alphabetical-by-tag position. EnvelopeVersion stays
// at 1: for non-upload actions the new fields serialize as their zero values
// and are ignored bridge-side, preserving backwards compatibility with
// existing post/archive/test signers.
type SlackEnvelope struct {
	Action      string `json:"action"`
	Body        string `json:"body"`
	Channel     string `json:"channel"`
	ContentType string `json:"content_type"`
	Filename    string `json:"filename"`
	Nonce       string `json:"nonce"`
	S3Key       string `json:"s3_key"`
	SenderID    string `json:"sender_id"`
	SizeBytes   int64  `json:"size_bytes"`
	Subject     string `json:"subject"`
	ThreadTS    string `json:"thread_ts"`
	Timestamp   int64  `json:"timestamp"`
	Version     int    `json:"version"`
}

// BuildEnvelope constructs a fresh envelope with a random 128-bit nonce and the
// current Unix timestamp. Returns ErrBodyTooLarge if body > MaxBodyBytes.
func BuildEnvelope(action, senderID, channel, subject, body, threadTS string) (*SlackEnvelope, error) {
	if len(body) > MaxBodyBytes {
		return nil, ErrBodyTooLarge
	}
	var nb [16]byte
	if _, err := rand.Read(nb[:]); err != nil {
		return nil, fmt.Errorf("slack: nonce read: %w", err)
	}
	return &SlackEnvelope{
		Action:    action,
		Body:      body,
		Channel:   channel,
		Nonce:     hex.EncodeToString(nb[:]),
		SenderID:  senderID,
		Subject:   subject,
		ThreadTS:  threadTS,
		Timestamp: time.Now().Unix(),
		Version:   EnvelopeVersion,
	}, nil
}

// BuildEnvelopeUpload constructs a fresh ActionUpload envelope. The body field
// is intentionally empty — uploads carry the file via S3 and the four new
// upload-specific fields (content_type, filename, s3_key, size_bytes).
// EnvelopeVersion stays at 1 (additive change). MaxBodyBytes does not apply.
//
// Validation:
//   - channel must be non-empty
//   - s3Key must be non-empty
//   - filename must be non-empty, ≤255 bytes, contain no '/' or NUL byte
//   - sizeBytes must be > 0
//
// contentType is not validated here (the bridge enforces an allow-list).
func BuildEnvelopeUpload(senderID, channel, threadTS, s3Key, filename, contentType string, sizeBytes int64) (*SlackEnvelope, error) {
	if channel == "" {
		return nil, ErrUploadChannelEmpty
	}
	if s3Key == "" {
		return nil, ErrUploadS3KeyEmpty
	}
	if filename == "" || len(filename) > 255 || strings.ContainsAny(filename, "/\x00") {
		return nil, ErrUploadFilenameInvalid
	}
	if sizeBytes <= 0 {
		return nil, ErrUploadSizeInvalid
	}
	var nb [16]byte
	if _, err := rand.Read(nb[:]); err != nil {
		return nil, fmt.Errorf("slack: nonce read: %w", err)
	}
	return &SlackEnvelope{
		Action:      ActionUpload,
		Body:        "",
		Channel:     channel,
		ContentType: contentType,
		Filename:    filename,
		Nonce:       hex.EncodeToString(nb[:]),
		S3Key:       s3Key,
		SenderID:    senderID,
		SizeBytes:   sizeBytes,
		Subject:     "",
		ThreadTS:    threadTS,
		Timestamp:   time.Now().Unix(),
		Version:     EnvelopeVersion,
	}, nil
}

// CanonicalJSON returns the deterministic JSON byte representation of env.
// Field order is fixed by the struct tag order (alphabetical).
func CanonicalJSON(env *SlackEnvelope) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		return nil, fmt.Errorf("slack: canonical encode: %w", err)
	}
	// Encoder.Encode appends a trailing newline; strip it for canonical form.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// SignEnvelope returns the canonical JSON bytes plus a raw Ed25519 signature.
// Caller is responsible for any base64 encoding before transport.
func SignEnvelope(env *SlackEnvelope, priv ed25519.PrivateKey) (canonical, signature []byte, err error) {
	canonical, err = CanonicalJSON(env)
	if err != nil {
		return nil, nil, err
	}
	signature = ed25519.Sign(priv, canonical)
	return canonical, signature, nil
}

// VerifyEnvelope checks that signature is a valid Ed25519 signature over the
// canonical JSON of env using pub. Returns nil on success.
func VerifyEnvelope(env *SlackEnvelope, signature []byte, pub ed25519.PublicKey) error {
	canonical, err := CanonicalJSON(env)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, canonical, signature) {
		return errors.New("slack: signature verification failed")
	}
	return nil
}
