package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The posted marker is the contract with the Slack inbound poller's
// km-slack-final-post block: a successful threaded post/reply leaves
// ${KM_SLACK_POSTED_DIR:-/tmp}/km-slack-posted.<thread_ts>, and the poller
// skips its own end-of-turn post when it finds one.

func markerExists(t *testing.T, dir, thread string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, "km-slack-posted."+thread))
	return err == nil
}

func TestPostedMarker_WrittenOnSuccessfulThreadedPost(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KM_SLACK_POSTED_DIR", dir)
	_, priv := genKey(t)
	srv, _ := newFakeBridge(t)
	defer srv.Close()

	if _, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", writeTmpBody(t, "answer"), "111.222", "plain"); err != nil {
		t.Fatalf("runWith: %v", err)
	}
	if !markerExists(t, dir, "111.222") {
		t.Fatal("expected posted marker after a successful threaded post")
	}
}

func TestPostedMarker_WrittenOnSuccessfulThreadedReply(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KM_SLACK_POSTED_DIR", dir)
	t.Setenv("KM_SLACK_CHANNEL_ID", "C_CHAN")
	t.Setenv("KM_SLACK_THREAD_TS", "333.444")
	_, priv := genKey(t)
	srv, _ := newFakeBridge(t)
	defer srv.Close()

	if err := runReplyWith(context.Background(), priv, "sb-test", srv.URL, runReplyOptions{
		bodyPath: writeTmpBody(t, "answer"),
		render:   "plain",
	}); err != nil {
		t.Fatalf("runReplyWith: %v", err)
	}
	if !markerExists(t, dir, "333.444") {
		t.Fatal("expected posted marker after a successful threaded reply")
	}
}

func TestPostedMarker_NotWrittenOnFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KM_SLACK_POSTED_DIR", dir)
	_, priv := genKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprintln(w, `{"error":"unauthorized"}`)
	}))
	defer srv.Close()

	if _, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", writeTmpBody(t, "answer"), "555.666", "plain"); err == nil {
		t.Fatal("expected an error from a 401 bridge")
	}
	if markerExists(t, dir, "555.666") {
		t.Fatal("a failed post must not leave a marker (the poller would then drop the reply)")
	}
}

func TestPostedMarker_NotWrittenOnNotOK(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KM_SLACK_POSTED_DIR", dir)
	_, priv := genKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":false,"error":"channel_not_found"}`)
	}))
	defer srv.Close()

	if _, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", writeTmpBody(t, "answer"), "777.888", "plain"); err == nil {
		t.Fatal("expected an error from a not-ok bridge response")
	}
	if markerExists(t, dir, "777.888") {
		t.Fatal("a not-ok post must not leave a marker")
	}
}

func TestPostedMarker_NotWrittenForTopLevelPost(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KM_SLACK_POSTED_DIR", dir)
	_, priv := genKey(t)
	srv, _ := newFakeBridge(t)
	defer srv.Close()

	if _, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", writeTmpBody(t, "status"), "", "plain"); err != nil {
		t.Fatalf("runWith: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a top-level post must not write a marker; found %d entries", len(entries))
	}
}

func TestPostedMarker_OptOutSkipsMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KM_SLACK_POSTED_DIR", dir)
	t.Setenv("KM_SLACK_POSTED_MARKER", "0")
	_, priv := genKey(t)
	srv, _ := newFakeBridge(t)
	defer srv.Close()

	if _, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", writeTmpBody(t, "tool progress"), "999.000", "plain"); err != nil {
		t.Fatalf("runWith: %v", err)
	}
	if markerExists(t, dir, "999.000") {
		t.Fatal("KM_SLACK_POSTED_MARKER=0 must suppress the marker (hook stream-drain posts are not the agent's answer)")
	}
}

func TestPostedMarkerPath_DefaultsToTmpAndRejectsPathSeparators(t *testing.T) {
	t.Setenv("KM_SLACK_POSTED_DIR", "")
	if got := postedMarkerPath("1.2"); got != "/tmp/km-slack-posted.1.2" {
		t.Errorf("default path: got %q", got)
	}
	for _, bad := range []string{"", "../x", "a/b", ".."} {
		if got := postedMarkerPath(bad); got != "" {
			t.Errorf("postedMarkerPath(%q) = %q, want empty", bad, got)
		}
	}
}
