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

func TestEmailAllowlist_ExactAndDomain(t *testing.T) {
	al, err := CompileEmailAllowlist([]string{"Kurt@Example.com", "*@Corp.example"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ok := range []string{"kurt@example.com", "KURT@example.com", "bob@corp.example", "Alice@CORP.EXAMPLE"} {
		if !al.Allows(ok) {
			t.Errorf("%q must be allowed", ok)
		}
	}
	for _, no := range []string{"bob@example.com", "a@sub.corp.example", "corp.example", "", "*@corp.example"} {
		if al.Allows(no) {
			t.Errorf("%q must be denied", no)
		}
	}
	if al.Len() != 2 {
		t.Fatalf("Len = %d", al.Len())
	}
}

func TestValidateEmailEntry_RejectsOtherGlobs(t *testing.T) {
	for _, bad := range []string{"*", "*@", "*@*", "k*@example.com", "*@example", "kurt@", "@example.com", "*@exa mple.com"} {
		if _, _, err := ValidateEmailEntry(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	n, isDomain, err := ValidateEmailEntry(" *@Example.COM ")
	if err != nil || !isDomain || n != "*@example.com" {
		t.Fatalf("got %q %v %v", n, isDomain, err)
	}
	n, isDomain, err = ValidateEmailEntry("Kurt@Example.com")
	if err != nil || isDomain || n != "kurt@example.com" {
		t.Fatalf("got %q %v %v", n, isDomain, err)
	}
}

// emailEntryCases is mirrored verbatim in internal/app/config
// (TestValidateRouteEmailEntry_MirrorsBridge): config cannot import this
// package, so the two validators are kept in lockstep by sharing this table.
var emailEntryCases = []struct {
	in string
	ok bool
}{
	{"kurt@example.com", true},
	{"Kurt@Example.COM", true},
	{" *@example.com ", true},
	{"*@corp.example", true},
	{"*", false},
	{"*@", false},
	{"*@*", false},
	{"k*@example.com", false},
	{"*@example", false},
	{"kurt@", false},
	{"@example.com", false},
	{"*@exa mple.com", false},
	{"nope", false},
	{"x@y.z", false},
}

func TestValidateEmailEntry_SharedTable(t *testing.T) {
	for _, tc := range emailEntryCases {
		_, _, err := ValidateEmailEntry(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("%q: ok=%v, err=%v", tc.in, tc.ok, err)
		}
	}
}

func TestDefaultRoute_AllowsDomainEntry(t *testing.T) {
	r := NewDefaultRoute("ir-bot", "profiles/ir-bot.yaml", []string{"*@example.com"})
	if r == nil || !r.Allows("anyone@example.com") || r.Allows("x@other.example") {
		t.Fatal("route must honour a domain entry")
	}
}
