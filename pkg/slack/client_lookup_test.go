package slack_test

// client_lookup_test.go — Phase 72 Layer 1: LookupUserByEmail tests.
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

// TestClient_LookupUserByEmail_Found — server returns ok=true with user.id="U123ABC".
// Assertion: (id="U123ABC", found=true, err=nil).
func TestClient_LookupUserByEmail_Found(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/users.lookupByEmail" {
			t.Errorf("path = %q; want /users.lookupByEmail", got)
		}
		// users.lookupByEmail is a legacy Slack method that rejects JSON
		// bodies with invalid_arguments — verify form-encoded wire format.
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			t.Errorf("Content-Type = %q; want application/x-www-form-urlencoded", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if got := string(body); got != "email=alice%40example.com" {
			t.Errorf("body = %q; want form-encoded email=alice%%40example.com", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok": true, "user": {"id": "U123ABC"}}`))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	id, found, err := c.LookupUserByEmail(context.Background(), "Alice@Example.com")
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if !found {
		t.Errorf("found = false; want true")
	}
	if id != "U123ABC" {
		t.Errorf("id = %q; want U123ABC", id)
	}
}

// TestClient_LookupUserByEmail_NotFound — server returns users_not_found.
// Assertion: ("", false, nil) — typed boolean miss, NOT an error.
func TestClient_LookupUserByEmail_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackErr("users_not_found"))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	id, found, err := c.LookupUserByEmail(context.Background(), "ghost@example.com")
	if err != nil {
		t.Fatalf("err = %v; want nil (miss is not error)", err)
	}
	if found {
		t.Errorf("found = true; want false")
	}
	if id != "" {
		t.Errorf("id = %q; want empty", id)
	}
}

// TestClient_LookupUserByEmail_MissingScope — server returns missing_scope.
// Assertion: *slack.SlackAPIError returned with Code == "missing_scope".
func TestClient_LookupUserByEmail_MissingScope(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackErr("missing_scope"))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	_, _, err := c.LookupUserByEmail(context.Background(), "alice@example.com")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apierr *slack.SlackAPIError
	if !errors.As(err, &apierr) {
		t.Fatalf("err type = %T; want *SlackAPIError", err)
	}
	if apierr.Code != "missing_scope" {
		t.Errorf("Code = %q; want missing_scope", apierr.Code)
	}
}

// TestClient_LookupUserByEmail_LowercasesEmail — asserts server receives lowercased
// email even when caller passes mixed-case input.
func TestClient_LookupUserByEmail_LowercasesEmail(t *testing.T) {
	var receivedBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok": true, "user": {"id": "U999"}}`))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	_, _, _ = c.LookupUserByEmail(context.Background(), "MIXED.Case@EXAMPLE.COM")

	if receivedBody != "email=mixed.case%40example.com" {
		t.Errorf("server received %q; want form-encoded email=mixed.case%%40example.com", receivedBody)
	}
}
