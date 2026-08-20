package slack_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
	"testing"
)

// TestClient_UnarchiveChannel_OK covers the method that makes archive-on-destroy
// round-trippable. km could archive a channel but had no way to lift one back
// out, which turned every alias reuse into an unrecoverable create failure.
func TestClient_UnarchiveChannel_OK(t *testing.T) {
	var capturedBody []byte
	var capturedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(nil))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	if err := c.UnarchiveChannel(context.Background(), "C0123ABC"); err != nil {
		t.Fatalf("UnarchiveChannel: %v", err)
	}
	if !strings.Contains(capturedPath, "conversations.unarchive") {
		t.Errorf("called %q; want conversations.unarchive", capturedPath)
	}

	var payload map[string]any
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["channel"] != "C0123ABC" {
		t.Errorf("channel = %v; want C0123ABC", payload["channel"])
	}
}

// TestClient_UnarchiveChannel_MissingScopeSurfaces proves a scope failure is
// returned rather than swallowed — the caller must be able to fall through to the
// existing actionable "channel is archived" error instead of silently continuing.
func TestClient_UnarchiveChannel_MissingScope(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":false,"error":"missing_scope"}`))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	err := c.UnarchiveChannel(context.Background(), "C0123ABC")
	if err == nil {
		t.Fatal("UnarchiveChannel: got nil error on missing_scope")
	}
	if !slack.IsMissingScope(err) {
		t.Errorf("IsMissingScope(%v) = false; want true", err)
	}
}

// TestClient_ChannelDetails_SurfacesArchived is the core of the trap: km parsed
// is_archived at client.go:136 and then threw it away, so validateStoredChannel
// reported an archived channel as a healthy cache hit and the create marched on
// to a fatal conversations.join.
func TestClient_ChannelDetails_SurfacesArchived(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(map[string]any{
			"channel": map[string]any{
				"id":          "C0BAPD4TCDB",
				"name":        "sec-github-bot",
				"is_archived": true,
				"is_member":   false,
				"num_members": 0,
			},
		}))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	name, archived, isMember, err := c.ChannelDetails(context.Background(), "C0BAPD4TCDB")
	if err != nil {
		t.Fatalf("ChannelDetails: %v", err)
	}
	if !archived {
		t.Error("archived = false; want true (is_archived was parsed but dropped before this method existed)")
	}
	if name != "sec-github-bot" {
		t.Errorf("name = %q; want sec-github-bot — the STORED name, which is what makes a renamed template visible", name)
	}
	if isMember {
		t.Error("isMember = true; want false")
	}
}

// TestClient_ChannelDetails_LiveChannel is the healthy path.
func TestClient_ChannelDetails_LiveChannel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(map[string]any{
			"channel": map[string]any{
				"id":        "C0123ABC",
				"name":      "sb-xyz-github-bot",
				"is_member": true,
			},
		}))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	name, archived, isMember, err := c.ChannelDetails(context.Background(), "C0123ABC")
	if err != nil {
		t.Fatalf("ChannelDetails: %v", err)
	}
	if archived {
		t.Error("archived = true; want false")
	}
	if !isMember || name != "sb-xyz-github-bot" {
		t.Errorf("name=%q isMember=%v; want sb-xyz-github-bot/true", name, isMember)
	}
}

// TestClient_ChannelDetails_SendsFormEncoded guards the same footgun that already
// bit ChannelInfo: conversations.info rejects a JSON body with invalid_arguments.
func TestClient_ChannelDetails_SendsFormEncoded(t *testing.T) {
	var gotContentType, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write(slackOK(map[string]any{"channel": map[string]any{"id": "C0123ABC", "name": "n"}}))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	if _, _, _, err := c.ChannelDetails(context.Background(), "C0123ABC"); err != nil {
		t.Fatalf("ChannelDetails: %v", err)
	}
	if !strings.HasPrefix(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q; want form-encoded (conversations.info rejects JSON)", gotContentType)
	}
	form, err := url.ParseQuery(gotBody)
	if err != nil {
		t.Fatalf("body is not form-encoded (%q): %v", gotBody, err)
	}
	if form.Get("channel") != "C0123ABC" {
		t.Errorf("form channel = %q; want C0123ABC", form.Get("channel"))
	}
}

// TestClient_ChannelDetails_ChannelNotFound keeps the definitive-dead signal
// distinguishable, since only channel_not_found may invalidate a stored mapping.
func TestClient_ChannelDetails_ChannelNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":false,"error":"channel_not_found"}`))
	}))
	defer ts.Close()

	c := newClientAgainstServer(ts)
	_, _, _, err := c.ChannelDetails(context.Background(), "CDEAD")
	if err == nil {
		t.Fatal("ChannelDetails: got nil error for channel_not_found")
	}
	if !slack.IsChannelNotFound(err) {
		t.Errorf("IsChannelNotFound(%v) = false; want true", err)
	}
}
