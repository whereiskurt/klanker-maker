package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

func TestRunPost_ClaimThreadRequiresNewMessage(t *testing.T) {
	var stderr bytes.Buffer
	code := runPost([]string{"--claim-thread", "--channel", "C1", "--body", "/nonexistent"}, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr=%q)", code, stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("--claim-thread requires --new-message")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func claimServer(t *testing.T, envs *[]slack.SlackEnvelope, rejectBlocks bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e slack.SlackEnvelope
		_ = json.NewDecoder(r.Body).Decode(&e)
		*envs = append(*envs, e)
		w.Header().Set("Content-Type", "application/json")
		if rejectBlocks && e.Blocks != "" {
			w.WriteHeader(400)
			fmt.Fprintln(w, `{"ok":false,"error":"invalid_blocks"}`)
			return
		}
		fmt.Fprintln(w, `{"ok":true,"ts":"1.1"}`)
	}))
}

func TestRunWithClaim_EnvelopeCarriesClaimThread(t *testing.T) {
	_, priv := genKey(t)
	var envs []slack.SlackEnvelope
	srv := claimServer(t, &envs, false)
	defer srv.Close()
	if _, err := runWithClaim(context.Background(), priv, "sb-test", srv.URL, "C123", "", writeTmpBody(t, "x"), "", "plain", true); err != nil {
		t.Fatal(err)
	}
	if len(envs) != 1 || !envs[0].ClaimThread {
		t.Fatalf("envs = %+v", envs)
	}
}

func TestRunWithClaim_FallbackEnvelopeKeepsClaimThread(t *testing.T) {
	_, priv := genKey(t)
	var envs []slack.SlackEnvelope
	srv := claimServer(t, &envs, true)
	defer srv.Close()
	if _, err := runWithClaim(context.Background(), priv, "sb-test", srv.URL, "C123", "", writeTmpBody(t, "# H\n\nbody"), "", "blocks", true); err != nil {
		t.Fatal(err)
	}
	if len(envs) != 2 || envs[0].Blocks == "" || envs[1].Blocks != "" || !envs[1].ClaimThread {
		t.Fatalf("want blocks attempt then claim-carrying fallback, got %+v", envs)
	}
}

func TestRunWith_DefaultDoesNotClaim(t *testing.T) {
	_, priv := genKey(t)
	var envs []slack.SlackEnvelope
	srv := claimServer(t, &envs, false)
	defer srv.Close()
	if _, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", writeTmpBody(t, "x"), "", "plain"); err != nil {
		t.Fatal(err)
	}
	if envs[0].ClaimThread {
		t.Fatal("default must not claim")
	}
}
