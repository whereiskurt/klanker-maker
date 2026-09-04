package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

// fakeBroker answers one request with resp and records what it was asked.
func fakeBroker(t *testing.T, resp secrets.UnsealResponse) (string, *secrets.UnsealRequest) {
	t.Helper()
	// os.MkdirTemp with a short prefix, NOT t.TempDir: a unix socket path is
	// capped at ~104 bytes (sun_path) and t.TempDir embeds the full test name,
	// which pushes the longer names here over the limit with a bind: invalid
	// argument that looks like a product bug and is not one.
	dir, err := os.MkdirTemp("", "kc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := &secrets.UnsealRequest{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = json.NewDecoder(conn).Decode(got)
		_ = json.NewEncoder(conn).Encode(resp)
	}()
	t.Cleanup(func() { <-done })
	return sock, got
}

func TestFetch_AsksForCredentials(t *testing.T) {
	sock, asked := fakeBroker(t, secrets.UnsealResponse{
		Credentials: &secrets.Credentials{Version: 1, AccessKeyID: "AKIA"},
	})
	if _, err := fetch(sock); err != nil {
		t.Fatal(err)
	}
	if asked.Op != secrets.OpCredentials {
		t.Fatalf("Op = %q, want %q", asked.Op, secrets.OpCredentials)
	}
}

// The output must be exactly the credential_process schema, with AWS's own
// casing. One wrong key and every SDK on the box silently falls back down the
// credential chain — and behind the fence there is nothing to fall back to.
func TestRender_EmitsTheCredentialProcessSchema(t *testing.T) {
	out, err := render(&secrets.Credentials{
		Version: 1, AccessKeyID: "AKIA", SecretAccessKey: "sk",
		SessionToken: "tok", Expiration: "2026-09-04T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	for _, k := range []string{"Version", "AccessKeyId", "SecretAccessKey", "SessionToken", "Expiration"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing credential_process key %q in %s", k, out)
		}
	}
	if v, _ := m["Version"].(float64); v != 1 {
		t.Errorf("Version = %v, want 1", m["Version"])
	}
}

// Fail closed and loudly. An empty stdout with a zero exit reaches the operator
// as "unparseable credential_process output", which names the wrong component.
func TestFetch_ErrorsWhenTheBrokerRefuses(t *testing.T) {
	sock, _ := fakeBroker(t, secrets.UnsealResponse{Error: "the IMDS fence is not enabled on this sandbox"})
	_, err := fetch(sock)
	if err == nil {
		t.Fatal("a refusal was not surfaced as an error")
	}
	if !strings.Contains(err.Error(), "fence") {
		t.Errorf("error lost the broker's reason: %v", err)
	}
}

func TestFetch_ErrorsWhenTheBrokerIsAbsent(t *testing.T) {
	if _, err := fetch(filepath.Join(t.TempDir(), "nope.sock")); err == nil {
		t.Fatal("a dead broker was not surfaced as an error")
	}
}

// A malformed response must not become an empty-but-valid credential object.
func TestFetch_ErrorsOnAResponseWithNoCredentials(t *testing.T) {
	sock, _ := fakeBroker(t, secrets.UnsealResponse{})
	if _, err := fetch(sock); err == nil {
		t.Fatal("a response carrying no credentials was accepted")
	}
}
