// main_test.go — Phase 103 Plan 07 tests for the km-h1-bridge Lambda entry.
//
// The init() in main.go constructs the real package-level webhookHandler against
// AWS adapters (no network at init — LoadDefaultConfig only builds a config). These
// tests OVERRIDE that global with a handler wired to in-package fakes so the entry's
// two load-bearing behaviors are exercised in isolation (Pitfall 1 + Pitfall 2):
//
//  1. A base64-ENCODED Function URL body is DECODED before HMAC verify, so a valid
//     signature over the decoded bytes reaches WebhookHandler.Handle and dispatches
//     (decode-then-verify — not verify-then-decode, which would 401 every base64 body).
//  2. Every internal error returns 200 (never 5xx) so HackerOne does not redeliver
//     with a fresh GUID that bypasses dedup.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"

	"github.com/aws/aws-lambda-go/events"

	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

const testSecret = "s3cr3t"

// hmacSig computes the X-H1-Signature value (sha256=<hex>) for a body.
func hmacSig(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// ── In-package fakes (the bridge_test fakes are not exported) ────────────────

type fakeSecret struct {
	secret string
	err    error
}

func (f *fakeSecret) Fetch(_ context.Context) (string, error) { return f.secret, f.err }

type fakeNonce struct {
	mu   sync.Mutex
	seen map[string]bool
	err  error
}

func (f *fakeNonce) CheckAndStore(_ context.Context, key string, _ int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, f.err
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[key] {
		return true, nil
	}
	f.seen[key] = true
	return false, nil
}

type fakeResolver struct {
	queueErr error
}

func (r *fakeResolver) ResolveByAlias(ctx context.Context, alias string) (string, error) {
	id, _, err := r.ResolveByAliasWithStatus(ctx, alias)
	return id, err
}

func (r *fakeResolver) ResolveByAliasWithStatus(_ context.Context, alias string) (string, string, error) {
	return "sb-" + alias, "running", nil
}

func (r *fakeResolver) H1QueueURL(_ context.Context, sandboxID string) (string, error) {
	if r.queueErr != nil {
		return "", r.queueErr
	}
	return "https://sqs/" + sandboxID + ".fifo", nil
}

type sqsCall struct{ queueURL, body, groupID, dedupID string }

type fakeSQS struct {
	mu    sync.Mutex
	sends []sqsCall
}

func (s *fakeSQS) Send(_ context.Context, queueURL, body, groupID, dedupID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sends = append(s.sends, sqsCall{queueURL, body, groupID, dedupID})
	return nil
}

type fakePublisher struct{}

func (p *fakePublisher) PutSandboxCreate(_ context.Context, _, _, _ string) error { return nil }

// h1CommentBody builds a synthetic HackerOne report_comment_created body whose
// comment message contains the bot handle (the comment-keyword trigger).
func h1CommentBody(program, reportID, actor, message string) []byte {
	payload := map[string]any{
		"data": map[string]any{
			"activity": map[string]any{
				"id":   "act-1",
				"type": "activity-comment",
				"attributes": map[string]any{
					"message":  message,
					"internal": true,
				},
				"relationships": map[string]any{
					"actor": map[string]any{
						"data": map[string]any{
							"attributes": map[string]any{"username": actor},
						},
					},
				},
			},
			"report": map[string]any{
				"id":         reportID,
				"attributes": map[string]any{"title": "Test report", "state": "new"},
				"relationships": map[string]any{
					"program": map[string]any{
						"data": map[string]any{
							"attributes": map[string]any{"handle": program},
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(payload)
	return b
}

// installHandler wires the package-level webhookHandler with the given fakes and a
// single km-sandbox program whose allowlist contains "alice".
func installHandler(secret *fakeSecret, nonce *fakeNonce, resolver *fakeResolver, sqs *fakeSQS) {
	webhookHandler = &bridge.WebhookHandler{
		Secret:    secret,
		Nonces:    nonce,
		Resolver:  resolver,
		Publisher: &fakePublisher{},
		SQS:       sqs,
		BotHandle: "@km",
		Entries: []bridge.ProgramEntry{
			{
				Handle:  "km-sandbox",
				Targets: []bridge.Target{{Alias: "h1-km-sandbox", Profile: "h1-triage"}},
				Allow:   []string{"alice"},
			},
		},
		DefaultProfile: "h1-triage",
	}
}

// TestHandle_Base64DecodeThenVerify proves Pitfall 1: a base64-ENCODED Function URL
// body whose signature is over the DECODED bytes reaches Handle and dispatches.
// If the entry verified before decoding, the signature would never match (401) and
// no SQS send would occur.
func TestHandle_Base64DecodeThenVerify(t *testing.T) {
	body := h1CommentBody("km-sandbox", "1337", "alice", "@km please triage")
	sqs := &fakeSQS{}
	installHandler(&fakeSecret{secret: testSecret}, &fakeNonce{}, &fakeResolver{}, sqs)

	ev := events.LambdaFunctionURLRequest{
		IsBase64Encoded: true,
		Body:            base64.StdEncoding.EncodeToString(body),
		Headers: map[string]string{
			"X-H1-Event":     "report_comment_created",
			"X-H1-Delivery":  "guid-1",
			"X-H1-Signature": hmacSig(testSecret, body), // signature over the DECODED bytes
		},
	}

	resp, err := handle(context.Background(), ev)
	if err != nil {
		t.Fatalf("handle returned error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; want 200 (decode-then-verify must reach Handle)", resp.StatusCode)
	}
	if len(sqs.sends) != 1 {
		t.Fatalf("sqs sends = %d; want 1 (decoded body must verify + dispatch)", len(sqs.sends))
	}
}

// TestHandle_BadSignature confirms the negative control: an UNSIGNED (or wrongly
// signed) base64 body is rejected 401 — i.e. the decode does not bypass verification.
func TestHandle_BadSignature(t *testing.T) {
	body := h1CommentBody("km-sandbox", "1337", "alice", "@km please triage")
	sqs := &fakeSQS{}
	installHandler(&fakeSecret{secret: testSecret}, &fakeNonce{}, &fakeResolver{}, sqs)

	ev := events.LambdaFunctionURLRequest{
		IsBase64Encoded: true,
		Body:            base64.StdEncoding.EncodeToString(body),
		Headers: map[string]string{
			"X-H1-Event":     "report_comment_created",
			"X-H1-Delivery":  "guid-bad",
			"X-H1-Signature": "sha256=deadbeef", // wrong signature
		},
	}

	resp, _ := handle(context.Background(), ev)
	if resp.StatusCode != 401 {
		t.Fatalf("status = %d; want 401 for a bad signature", resp.StatusCode)
	}
	if len(sqs.sends) != 0 {
		t.Fatalf("sqs sends = %d; want 0 (bad signature must not dispatch)", len(sqs.sends))
	}
}

// TestHandle_InternalError200 proves Pitfall 2: an internal error (here, the
// secret-fetch failing) returns 200 — never 5xx — so HackerOne does not redeliver
// with a fresh GUID that bypasses dedup.
func TestHandle_InternalError200(t *testing.T) {
	body := h1CommentBody("km-sandbox", "1337", "alice", "@km please triage")
	sqs := &fakeSQS{}
	installHandler(
		&fakeSecret{err: errString("ssm unavailable")}, // secret-fetch fails internally
		&fakeNonce{}, &fakeResolver{}, sqs,
	)

	ev := events.LambdaFunctionURLRequest{
		IsBase64Encoded: false,
		Body:            string(body),
		Headers: map[string]string{
			"X-H1-Event":     "report_comment_created",
			"X-H1-Delivery":  "guid-2",
			"X-H1-Signature": hmacSig(testSecret, body),
		},
	}

	resp, err := handle(context.Background(), ev)
	if err != nil {
		t.Fatalf("handle returned error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d; want 200 on internal error (no 5xx redelivery)", resp.StatusCode)
	}
}

// TestHandle_BadBase64 confirms a malformed base64 body is a clean 400, not a panic.
func TestHandle_BadBase64(t *testing.T) {
	installHandler(&fakeSecret{secret: testSecret}, &fakeNonce{}, &fakeResolver{}, &fakeSQS{})
	ev := events.LambdaFunctionURLRequest{
		IsBase64Encoded: true,
		Body:            "!!!not-base64!!!",
		Headers:         map[string]string{"x-h1-event": "report_comment_created"},
	}
	resp, _ := handle(context.Background(), ev)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d; want 400 for malformed base64", resp.StatusCode)
	}
}

// TestLowercaseHeaders verifies the defensive header normalization.
func TestLowercaseHeaders(t *testing.T) {
	out := lowercaseHeaders(map[string]string{"X-H1-Event": "report_created", "X-H1-Delivery": "g"})
	if out["x-h1-event"] != "report_created" || out["x-h1-delivery"] != "g" {
		t.Fatalf("lowercaseHeaders did not normalize keys: %v", out)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
