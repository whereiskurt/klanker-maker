package bridge

import (
	"context"
	"errors"
	"testing"
	"time"
)

type scriptedEmails struct {
	calls int
	email string
	err   error
}

func (s *scriptedEmails) SenderEmail(_ context.Context, _ string) (string, error) {
	s.calls++
	return s.email, s.err
}

func TestDefaultRoute_AllowsIsCaseInsensitiveAndTrimmed(t *testing.T) {
	r := NewDefaultRoute("ir-bot", "profiles/ir-bot.yaml", []string{" Kurt@Example.com "})
	if !r.Allows("kurt@example.com") || !r.Allows("KURT@EXAMPLE.COM") {
		t.Fatal("expected allow")
	}
	if r.Allows("other@example.com") || r.Allows("") {
		t.Fatal("expected deny")
	}
}

func TestNewDefaultRoute_EmptyEmailsIsNil(t *testing.T) {
	if NewDefaultRoute("a", "p", nil) != nil || NewDefaultRoute("a", "p", []string{" "}) != nil {
		t.Fatal("empty allowlist must yield a nil route (route OFF)")
	}
}

func TestRequiresMention(t *testing.T) {
	for ct, want := range map[string]bool{"im": false, "mpim": true, "channel": true, "group": true, "": true} {
		if got := requiresMention(ct); got != want {
			t.Errorf("requiresMention(%q)=%v want %v", ct, got, want)
		}
	}
}

func TestCachedSenderEmailFetcher_CachesSuccessOnly(t *testing.T) {
	now := time.Unix(1000, 0)
	inner := &scriptedEmails{email: "a@b.c"}
	c := &CachedSenderEmailFetcher{Inner: inner, TTL: 15 * time.Minute, Now: func() time.Time { return now }}
	for i := 0; i < 3; i++ {
		if e, err := c.SenderEmail(context.Background(), "U1"); err != nil || e != "a@b.c" {
			t.Fatalf("iter %d: %q %v", i, e, err)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("want 1 inner call, got %d", inner.calls)
	}
	now = now.Add(16 * time.Minute)
	c.SenderEmail(context.Background(), "U1")
	if inner.calls != 2 {
		t.Fatalf("want refetch after TTL, got %d calls", inner.calls)
	}

	bad := &scriptedEmails{err: errors.New("boom")}
	c2 := &CachedSenderEmailFetcher{Inner: bad, TTL: time.Hour, Now: func() time.Time { return now }}
	c2.SenderEmail(context.Background(), "U2")
	c2.SenderEmail(context.Background(), "U2")
	if bad.calls != 2 {
		t.Fatalf("a failure must never be cached; got %d calls", bad.calls)
	}
}
