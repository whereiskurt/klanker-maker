package bridge_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

func TestSlackPosterAdapter_OpenDM(t *testing.T) {
	var gotAuth, gotUsers, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotUsers, _ = body["users"].(string)
		switch gotUsers {
		case "U0KURT":
			w.Write([]byte(`{"ok":true,"channel":{"id":"D0KURT"}}`))
		default:
			w.Write([]byte(`{"ok":false,"error":"cannot_dm_bot"}`))
		}
	}))
	defer srv.Close()
	a := &bridge.SlackPosterAdapter{BaseURL: srv.URL, Tokens: staticTokens{}}
	ch, err := a.OpenDM(context.Background(), "U0KURT")
	if err != nil || ch != "D0KURT" {
		t.Fatalf("OpenDM = %q, %v", ch, err)
	}
	if gotPath != "/conversations.open" || gotAuth != "Bearer xoxb-test" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if _, err := a.OpenDM(context.Background(), "UBOT"); err == nil || !strings.Contains(err.Error(), "cannot_dm_bot") {
		t.Fatalf("want slack error surfaced, got %v", err)
	}
	var _ bridge.DMOpener = a
}
