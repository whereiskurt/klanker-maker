package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/whereiskurt/klankrmkr/pkg/slack/bridge"
)

// ============================================================
// Stub implementations for Lambda handler tests
// ============================================================

type stubPublicKeyFetcher struct{}

func (s *stubPublicKeyFetcher) Fetch(_ context.Context, _ string) (ed25519.PublicKey, error) {
	return make([]byte, 32), nil
}

type stubNonceStore struct{}

func (s *stubNonceStore) Reserve(_ context.Context, _ string, _ int) error {
	return nil
}

type stubChannelOwnership struct{}

func (s *stubChannelOwnership) OwnedChannel(_ context.Context, _ string) (string, error) {
	return "C01234567", nil
}

type stubBotTokenFetcher struct{}

func (s *stubBotTokenFetcher) Fetch(_ context.Context) (string, error) {
	return "xoxb-stub-token", nil
}

type stubSlackPoster struct{}

func (s *stubSlackPoster) PostMessage(_ context.Context, _, _, _, _ string) (string, error) {
	return "1234567890.000001", nil
}

func (s *stubSlackPoster) ArchiveChannel(_ context.Context, _ string) error {
	return nil
}

func TestHandle_BadEnvelope(t *testing.T) {
	// Override global handler with stub
	origHandler := handler
	handler = &bridge.Handler{
		Now:      time.Now,
		Keys:     &stubPublicKeyFetcher{},
		Nonces:   &stubNonceStore{},
		Channels: &stubChannelOwnership{},
		Token:    &stubBotTokenFetcher{},
		Slack:    &stubSlackPoster{},
	}
	t.Cleanup(func() { handler = origHandler })

	ev := events.LambdaFunctionURLRequest{
		Body:    `{"bad": "json"}`,
		Headers: map[string]string{},
	}

	resp, err := handle(context.Background(), ev)
	if err != nil {
		t.Fatalf("handle returned error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for bad envelope, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(resp.Body), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
}

func TestHandle_ResponseHeadersPresent(t *testing.T) {
	origHandler := handler
	handler = &bridge.Handler{
		Now:      time.Now,
		Keys:     &stubPublicKeyFetcher{},
		Nonces:   &stubNonceStore{},
		Channels: &stubChannelOwnership{},
		Token:    &stubBotTokenFetcher{},
		Slack:    &stubSlackPoster{},
	}
	t.Cleanup(func() { handler = origHandler })

	ev := events.LambdaFunctionURLRequest{
		Body:    `not json`,
		Headers: map[string]string{},
	}

	resp, err := handle(context.Background(), ev)
	if err != nil {
		t.Fatalf("handle returned error: %v", err)
	}
	// Response should always have Content-Type header
	if resp.Headers == nil {
		t.Error("expected non-nil response headers")
	}
}
