package slack_test

// client_invite_test.go — Phase 72 Layer 1: InviteUserToChannel tests.
// Plan 72-01 (Wave 1) implementation.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// TestClient_InviteUserToChannel_OK — server returns ok=true with channel.
// Assertion: err == nil.
func TestClient_InviteUserToChannel_OK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/conversations.invite" {
			t.Errorf("path = %q; want /conversations.invite", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"channel":"C123"`) || !strings.Contains(string(body), `"users":"U123"`) {
			t.Errorf("body missing required fields: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(map[string]any{"channel": map[string]any{"id": "C123"}}))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	if err := c.InviteUserToChannel(context.Background(), "C123", "U123"); err != nil {
		t.Errorf("err = %v; want nil", err)
	}
}

// TestClient_InviteUserToChannel_AlreadyMember — server returns already_in_channel.
// Assertion: err == nil (idempotent — treated as success, mirrors JoinChannel contract).
func TestClient_InviteUserToChannel_AlreadyMember(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackErr("already_in_channel"))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	if err := c.InviteUserToChannel(context.Background(), "C123", "U123"); err != nil {
		t.Errorf("err = %v; want nil (idempotent)", err)
	}
}

// TestClient_InviteUserToChannel_CantInviteSelf — server returns cant_invite_self.
// Assertion: *slack.SlackAPIError with Code == "cant_invite_self". NOT swallowed.
func TestClient_InviteUserToChannel_CantInviteSelf(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackErr("cant_invite_self"))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	err := c.InviteUserToChannel(context.Background(), "C123", "U123")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apierr *slack.SlackAPIError
	if !errors.As(err, &apierr) {
		t.Fatalf("err type = %T; want *SlackAPIError", err)
	}
	if apierr.Code != "cant_invite_self" {
		t.Errorf("Code = %q; want cant_invite_self", apierr.Code)
	}
}

// TestClient_InviteUserToChannel_NotInChannel — server returns not_in_channel.
// Assertion: *slack.SlackAPIError with Code == "not_in_channel".
// Bot needs to JoinChannel first before it can invite users.
func TestClient_InviteUserToChannel_NotInChannel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackErr("not_in_channel"))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	err := c.InviteUserToChannel(context.Background(), "C123", "U123")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apierr *slack.SlackAPIError
	if !errors.As(err, &apierr) {
		t.Fatalf("err type = %T; want *SlackAPIError", err)
	}
	if apierr.Code != "not_in_channel" {
		t.Errorf("Code = %q; want not_in_channel", apierr.Code)
	}
}

// TestClient_InviteUserToChannel_UserIsRestricted — server returns user_is_restricted.
// Assertion: *slack.SlackAPIError with Code == "user_is_restricted" (NOT swallowed).
// Proves that restricted guest users are not silently invited.
func TestClient_InviteUserToChannel_UserIsRestricted(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackErr("user_is_restricted"))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	err := c.InviteUserToChannel(context.Background(), "C123", "U123")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apierr *slack.SlackAPIError
	if !errors.As(err, &apierr) {
		t.Fatalf("err type = %T; want *SlackAPIError", err)
	}
	if apierr.Code != "user_is_restricted" {
		t.Errorf("Code = %q; want user_is_restricted", apierr.Code)
	}
}

// TestClient_InviteUserToChannelStrict_AlreadyMember — server returns already_in_channel.
// Assertion: errors.Is(err, ErrAlreadyInChannel) — strict variant surfaces the sentinel.
func TestClient_InviteUserToChannelStrict_AlreadyMember(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackErr("already_in_channel"))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	err := c.InviteUserToChannelStrict(context.Background(), "C123", "U123")
	if err == nil {
		t.Fatal("expected ErrAlreadyInChannel, got nil")
	}
	if !errors.Is(err, slack.ErrAlreadyInChannel) {
		t.Errorf("err is not ErrAlreadyInChannel: %v", err)
	}
}

// TestClient_InviteUserToChannelStrict_OK — server returns ok=true.
// Assertion: err == nil (fresh invite succeeds).
func TestClient_InviteUserToChannelStrict_OK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(map[string]any{"channel": map[string]any{"id": "C123"}}))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	if err := c.InviteUserToChannelStrict(context.Background(), "C123", "U123"); err != nil {
		t.Errorf("err = %v; want nil", err)
	}
}
