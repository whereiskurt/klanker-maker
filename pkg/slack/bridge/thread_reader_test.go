package bridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubTokens struct{}

func (stubTokens) Fetch(ctx context.Context) (string, error) { return "xoxb-test", nil }

func TestSlackThreadReaderAdapter_Replies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.replies" {
			t.Errorf("path %s", r.URL.Path)
		}
		r.ParseForm()
		if r.Form.Get("channel") != "C1" || r.Form.Get("ts") != "1.0" || r.Form.Get("oldest") != "0.5" || r.Form.Get("limit") != "200" {
			t.Errorf("form %v", r.Form)
		}
		if r.Header.Get("Authorization") != "Bearer xoxb-test" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"messages":[{"user":"U1","ts":"1.0","text":"root"},{"bot_id":"B1","ts":"1.1","text":"bot","subtype":"bot_message"},{"user":"U2","ts":"1.2","text":"with file","files":[{"id":"F1","name":"a.png","size":12}]}]}`))
	}))
	defer srv.Close()
	a := &SlackThreadReaderAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Tokens: stubTokens{}}
	got, err := a.Replies(context.Background(), "C1", "1.0", "0.5")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[1].BotID != "B1" || got[2].Files[0].ID != "F1" || got[0].Text != "root" {
		t.Fatalf("got %+v", got)
	}
}

func TestSlackThreadReaderAdapter_OmitsEmptyOldest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if _, ok := r.Form["oldest"]; ok {
			t.Errorf("oldest must be absent when empty: %v", r.Form)
		}
		w.Write([]byte(`{"ok":true,"messages":[]}`))
	}))
	defer srv.Close()
	a := &SlackThreadReaderAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Tokens: stubTokens{}}
	if _, err := a.Replies(context.Background(), "C1", "1.0", ""); err != nil {
		t.Fatal(err)
	}
}

func TestSlackThreadReaderAdapter_SlackError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":false,"error":"ratelimited"}`))
	}))
	defer srv.Close()
	a := &SlackThreadReaderAdapter{HTTPClient: srv.Client(), BaseURL: srv.URL, Tokens: stubTokens{}}
	if _, err := a.Replies(context.Background(), "C1", "1.0", ""); err == nil || !strings.Contains(err.Error(), "ratelimited") {
		t.Fatalf("want error naming ratelimited, got %v", err)
	}
}
