package slack_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// users.conversations is a legacy read method: Slack ignores arguments sent as
// a JSON body, so private channels silently vanished. It must be form-encoded.
func TestListBotChannels_FormEncodedIncludesPrivate(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			t.Errorf("Content-Type = %q; want form-encoded", ct)
		}
		_ = r.ParseForm()
		if got := r.PostForm.Get("types"); got != "public_channel,private_channel" {
			t.Errorf("types = %q", got)
		}
		if got := r.PostForm.Get("exclude_archived"); got != "true" {
			t.Errorf("exclude_archived = %q", got)
		}
		chans := []map[string]any{{"id": "C1", "name": "pub"}}
		if r.PostForm.Get("types") == "public_channel,private_channel" {
			chans = append(chans, map[string]any{"id": "G1", "name": "priv"})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(listPage(chans, ""))
	}))
	defer ts.Close()

	got, err := newClientAgainstServer(ts).ListBotChannels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Name != "priv" {
		t.Errorf("channels = %+v; want public and private", got)
	}
}
