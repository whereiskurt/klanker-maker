package webhook

import (
	"regexp"
	"strings"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// templateVar matches {{name}} and {{dotted.name}}.
var templateVar = regexp.MustCompile(`\{\{([a-zA-Z0-9_.]+)\}\}`)

// MatchRule returns the first rule whose Match block passes, and its index.
// Returns (nil, -1) when nothing matches.
//
// Semantics: all named fields must match (AND); within a field any listed value
// matches (OR); an empty Match matches everything. A field ABSENT from the
// envelope is a non-match, never a wildcard — that is what makes a typo'd field
// name dispatch nothing instead of everything.
func MatchRule(env *Envelope, rules []config.WebhookRule) (*config.WebhookRule, int) {
	for i := range rules {
		if ruleMatches(env, rules[i]) {
			return &rules[i], i
		}
	}
	return nil, -1
}

func ruleMatches(env *Envelope, r config.WebhookRule) bool {
	for field, allowed := range r.Match {
		val, ok := env.Field(field)
		if !ok {
			return false // fail closed on an unknown/absent field
		}
		if !containsFold(allowed, val) {
			return false
		}
	}
	return true
}

func containsFold(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

// ExpandTemplate replaces {{field}} references with envelope values. Unknown
// variables are left VERBATIM (matching ExpandEventTemplate in the GitHub
// bridge): a silently blanked variable hides an operator typo, whereas a
// literal {{nope}} in a prompt is self-diagnosing.
//
// Note for readers: Wiz templates also use {{...}}, but Wiz renders its own
// variables before sending. By the time this runs, the body is already
// rendered — there is no collision.
func ExpandTemplate(tmpl string, env *Envelope) string {
	return templateVar.ReplaceAllStringFunc(tmpl, func(m string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(m, "{{"), "}}")
		if v, ok := env.Field(name); ok {
			return v
		}
		return m
	})
}
