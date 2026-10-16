package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

type fakeEmailLookup struct{ known map[string]string }

func (f *fakeEmailLookup) LookupUserByEmail(_ context.Context, email string) (string, bool, error) {
	id, ok := f.known[email]
	return id, ok, nil
}

func statusOf(rs []CheckResult, name string) (CheckStatus, string) {
	for _, r := range rs {
		if r.Name == name {
			return r.Status, r.Message
		}
	}
	return "", "missing"
}

const routeProfileStrict = routeProfileOK + "        threadBypass: false\n"

func TestCheckSlackDefaultRoute_SkippedWhenAbsent(t *testing.T) {
	rs := checkSlackDefaultRoute(context.Background(), nil, nil, nil, "")
	if len(rs) != 1 || rs[0].Status != CheckSkipped {
		t.Fatalf("want one SKIPPED result, got %+v", rs)
	}
}

func TestCheckSlackDefaultRoute_AllGreen(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	route := &config.SlackDefaultRoute{Alias: "ir-bot", Profile: p, AllowEmails: []string{"kurt@example.com"}}
	rs := checkSlackDefaultRoute(context.Background(), route, nil, &fakeEmailLookup{known: map[string]string{"kurt@example.com": "U1"}}, "channels:history,im:history,mpim:history")
	for _, name := range []string{"Slack default route profile", "Slack default route allowlist", "Slack default route scopes"} {
		if st, msg := statusOf(rs, name); st != CheckOK {
			t.Errorf("%s: %s (%s)", name, st, msg)
		}
	}
	if _, msg := statusOf(rs, "Slack default route profile"); strings.Contains(msg, "threadBypass") {
		t.Errorf("non-strict profile must not carry the strict note: %q", msg)
	}
}

func TestCheckSlackDefaultRoute_StrictProfileNote(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileStrict)
	route := &config.SlackDefaultRoute{Alias: "ir-bot", Profile: p, AllowEmails: []string{"kurt@example.com"}}
	rs := checkSlackDefaultRoute(context.Background(), route, nil, nil, "")
	st, msg := statusOf(rs, "Slack default route profile")
	if st != CheckOK || !strings.HasSuffix(msg, "; threadBypass: false — thread follow-ups in invited channels require an @-mention") {
		t.Errorf("strict profile: %s %q", st, msg)
	}
	if st, _ := statusOf(rs, "Slack default route allowlist"); st != CheckSkipped {
		t.Errorf("nil lookup must SKIP the allowlist, got %s", st)
	}
	if st, _ := statusOf(rs, "Slack default route scopes"); st != CheckSkipped {
		t.Errorf("empty scopes cache must SKIP, got %s", st)
	}
}

func TestCheckSlackDefaultRoute_Warnings(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "quiet.yaml", routeProfileNoInbound)
	route := &config.SlackDefaultRoute{Alias: "ir-bot", Profile: p, AllowEmails: []string{"ghost@example.com"}}
	rs := checkSlackDefaultRoute(context.Background(), route, nil, &fakeEmailLookup{}, "channels:history")
	if st, _ := statusOf(rs, "Slack default route profile"); st != CheckWarn {
		t.Errorf("profile without inbound must WARN")
	}
	if st, msg := statusOf(rs, "Slack default route allowlist"); st != CheckWarn || !strings.Contains(msg, "ghost@example.com") {
		t.Errorf("unresolvable email must WARN and be named; got %s %q", st, msg)
	}
	if st, msg := statusOf(rs, "Slack default route scopes"); st != CheckWarn || !strings.Contains(msg, "im:history") {
		t.Errorf("missing im:history must WARN; got %s %q", st, msg)
	}
}
