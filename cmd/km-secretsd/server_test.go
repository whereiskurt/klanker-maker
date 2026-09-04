package main

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

type recordingAudit struct{ events []map[string]any }

func (r *recordingAudit) Emit(t string, d map[string]any) error {
	e := map[string]any{"event_type": t}
	for k, v := range d {
		e[k] = v
	}
	r.events = append(r.events, e)
	return nil
}

func serverWith(t *testing.T, yaml string, g secrets.Grants) (*Server, *recordingAudit) {
	t.Helper()
	stubDecrypt(t, yaml, nil)
	aud := &recordingAudit{}
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	if err := osWriteFile(p); err != nil {
		t.Fatal(err)
	}
	return &Server{CiphertextPath: p, Grants: g, Audit: aud}, aud
}

// roundTrip runs one request through Handle over a socketpair.
func roundTrip(t *testing.T, s *Server, req secrets.UnsealRequest, uid, pid uint32) secrets.UnsealResponse {
	t.Helper()
	c1, c2 := net.Pipe()
	go func() {
		defer c2.Close()
		s.Handle(c2, uid, pid)
	}()
	defer c1.Close()
	if err := json.NewEncoder(c1).Encode(req); err != nil {
		t.Fatal(err)
	}
	var resp secrets.UnsealResponse
	if err := json.NewDecoder(c1).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHandle_ReturnsGrantedKeysOnly(t *testing.T) {
	s, _ := serverWith(t, "A: 1\nB: 2\n", secrets.Grants{"claude": {"A"}})
	resp := roundTrip(t, s, secrets.UnsealRequest{As: "claude"}, 1000, 4242)

	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if _, ok := resp.Values["B"]; ok {
		t.Error("returned B, which claude was never granted")
	}
	if string(resp.Values["A"]) != "1" {
		t.Errorf("A = %q", resp.Values["A"])
	}
}

func TestHandle_UnknownConsumerRefused(t *testing.T) {
	s, _ := serverWith(t, "A: 1\n", secrets.Grants{"claude": {"A"}})
	resp := roundTrip(t, s, secrets.UnsealRequest{As: "codex"}, 1000, 4242)

	if resp.Error == "" {
		t.Fatal("an ungranted identity was served")
	}
	if len(resp.Values) != 0 {
		t.Error("values returned alongside an error")
	}
}

func TestHandle_AuditsEveryUnsealWithNamesNotValues(t *testing.T) {
	s, aud := serverWith(t, "A: supersecret\n", nil)
	roundTrip(t, s, secrets.UnsealRequest{As: "claude"}, 1000, 4242)

	if len(aud.events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(aud.events))
	}
	ev := aud.events[0]
	if ev["event_type"] != "secret_unseal" {
		t.Errorf("event_type = %v", ev["event_type"])
	}
	if ev["uid"] != uint32(1000) || ev["pid"] != uint32(4242) {
		t.Errorf("peer credentials not recorded: %+v", ev)
	}
	blob, _ := json.Marshal(ev)
	if string(blob) != "" && contains(string(blob), "supersecret") {
		t.Error("audit event contains a secret VALUE")
	}
}

func TestHandle_AuditsRefusals(t *testing.T) {
	// A refused request is the more interesting security event, not the less.
	s, aud := serverWith(t, "A: 1\n", secrets.Grants{"claude": {"A"}})
	roundTrip(t, s, secrets.UnsealRequest{As: "nobody"}, 1000, 4242)

	if len(aud.events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(aud.events))
	}
	if aud.events[0]["event_type"] != "secret_unseal_refused" {
		t.Errorf("event_type = %v", aud.events[0]["event_type"])
	}
}

func TestHandle_DecryptFailureIsRefusalNotEmptySuccess(t *testing.T) {
	stubDecrypt(t, "", errDecryptStub)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	if err := osWriteFile(p); err != nil {
		t.Fatal(err)
	}
	s := &Server{CiphertextPath: p, Audit: &recordingAudit{}}
	resp := roundTrip(t, s, secrets.UnsealRequest{}, 1000, 1)

	if resp.Error == "" {
		t.Fatal("a KMS failure was reported as success with no keys")
	}
}
