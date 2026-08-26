package webhook

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

func envFixture(t *testing.T) *Envelope {
	t.Helper()
	env, err := ParseEnvelope([]byte(wizV1Body), config.WebhookSource{Name: "wiz"})
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	return env
}

func TestMatchRule_FirstMatchWins(t *testing.T) {
	env := envFixture(t)
	rules := []config.WebhookRule{
		{Match: map[string][]string{"severity": {"LOW"}}, Alias: "no"},
		{Match: map[string][]string{"severity": {"CRITICAL", "HIGH"}}, Alias: "yes"},
		{Match: map[string][]string{"severity": {"CRITICAL"}}, Alias: "also-yes"},
	}
	got, idx := MatchRule(env, rules)
	if got == nil {
		t.Fatal("expected a match")
	}
	if got.Alias != "yes" || idx != 1 {
		t.Errorf("got alias=%q idx=%d, want yes/1", got.Alias, idx)
	}
}

// All named fields must match (AND); values within a field are OR.
func TestMatchRule_AndAcrossFields(t *testing.T) {
	env := envFixture(t)

	both := []config.WebhookRule{{Match: map[string][]string{
		"type": {"issue"}, "severity": {"CRITICAL"},
	}, Alias: "hit"}}
	if got, _ := MatchRule(env, both); got == nil {
		t.Error("both fields matching should hit")
	}

	one := []config.WebhookRule{{Match: map[string][]string{
		"type": {"issue"}, "severity": {"LOW"},
	}, Alias: "miss"}}
	if got, _ := MatchRule(env, one); got != nil {
		t.Error("one field failing must not hit")
	}
}

// A field the envelope does not carry is a NON-match, never a wildcard.
// A typo'd path must dispatch nothing rather than everything.
func TestMatchRule_UnknownFieldFailsClosed(t *testing.T) {
	env := envFixture(t)
	rules := []config.WebhookRule{
		{Match: map[string][]string{"sevrity": {"CRITICAL"}}, Alias: "typo"},
	}
	if got, _ := MatchRule(env, rules); got != nil {
		t.Fatal("a match on an unknown field must fail closed")
	}
}

func TestMatchRule_EmptyMatchIsWildcard(t *testing.T) {
	env := envFixture(t)
	rules := []config.WebhookRule{{Alias: "catch-all"}}
	got, _ := MatchRule(env, rules)
	if got == nil || got.Alias != "catch-all" {
		t.Fatal("an empty match block must match everything")
	}
}

func TestMatchRule_NoRulesNoMatch(t *testing.T) {
	if got, idx := MatchRule(envFixture(t), nil); got != nil || idx != -1 {
		t.Errorf("got (%v,%d), want (nil,-1)", got, idx)
	}
}

func TestExpandTemplate(t *testing.T) {
	env := envFixture(t)

	got := ExpandTemplate("sev={{severity}} entity={{entity.name}} type={{type}}", env)
	want := "sev=CRITICAL entity=logs type=issue"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// Unknown vars are left verbatim — same decision as ExpandEventTemplate in
	// the GitHub bridge. Silent blanking hides typos.
	if got := ExpandTemplate("x={{nope}}", env); got != "x={{nope}}" {
		t.Errorf("unknown var: got %q", got)
	}

	// {{raw}} carries the whole payload into the prompt.
	if got := ExpandTemplate("{{raw}}", env); got != env.Raw {
		t.Error("{{raw}} must expand to the full body")
	}
}
