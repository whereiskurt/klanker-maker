package bridge_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

// claim_thread: after a successful TOP-LEVEL post the bridge records the new
// message's thread row, owned by the sender. Threaded posts ignore the flag;
// a failed post writes nothing. Drives the full Handle (signed envelope).

func claimHandler(t *testing.T, slackFake *fakeSlack) (*recordingThreads, func(thread string, claim bool) int) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	th := &recordingThreads{rows: map[string]string{}}
	h.Threads = th
	h.Slack = slackFake
	return th, func(thread string, claim bool) int {
		env := makeEnv("post", "sb-abc123", "C0123ABC")
		env.ThreadTS, env.ClaimThread = thread, claim
		return h.Handle(context.Background(), signRequest(t, env, priv)).StatusCode
	}
}

func TestPost_ClaimThread_UpsertsRowForTopLevelPost(t *testing.T) {
	th, post := claimHandler(t, &fakeSlack{returnTS: "1700.1"})
	if st := post("", true); st != 200 {
		t.Fatalf("status %d", st)
	}
	if len(th.upserts) != 1 || th.upserts[0] != "C0123ABC|1700.1|sb-abc123" {
		t.Fatalf("upserts = %v", th.upserts)
	}
}

func TestPost_ClaimThread_IgnoredForThreadedPost(t *testing.T) {
	th, post := claimHandler(t, &fakeSlack{returnTS: "1700.2"})
	if st := post("1600.0", true); st != 200 {
		t.Fatalf("status %d", st)
	}
	if len(th.upserts) != 0 {
		t.Fatalf("threaded post must not claim: %v", th.upserts)
	}
}

func TestPost_WithoutClaimThread_WritesNoRow(t *testing.T) {
	th, post := claimHandler(t, &fakeSlack{returnTS: "1700.3"})
	post("", false)
	if len(th.upserts) != 0 {
		t.Fatalf("plain post must not claim: %v", th.upserts)
	}
}

func TestPost_ClaimThread_FailedPostWritesNothing(t *testing.T) {
	th, post := claimHandler(t, &fakeSlack{postErr: errors.New("channel_not_found")})
	if st := post("", true); st == 200 {
		t.Fatalf("expected failure status")
	}
	if len(th.upserts) != 0 {
		t.Fatalf("failed post must not claim: %v", th.upserts)
	}
}
