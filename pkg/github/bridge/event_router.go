package bridge

import (
	"path"
	"strings"
)

// EventRule is a single entry in the github.events config block.
// It maps a (webhook event type, optional action list, repo match glob) triple
// to a sandbox + prompt. Phase 115.
//
// JSON field names mirror the km-config.yaml surface so json.Unmarshal from
// KM_GITHUB_EVENTS works directly (same pattern as RepoEntry / KM_GITHUB_REPOS).
type EventRule struct {
	// On is the GitHub webhook event type, e.g. "repository", "push", "release".
	// Required — a rule with an empty On never matches.
	On string `json:"on"`

	// Actions filters by the action field inside the event payload (e.g. "created").
	// When empty, all actions for the event type match. When non-empty, only listed
	// actions match.
	Actions []string `json:"actions,omitempty"`

	// Match is an exact "owner/repo" or glob "owner/*" pattern matched against
	// the repository full_name in the payload.
	// Exact matches always win over globs regardless of declaration order
	// (same two-pass semantics as Resolve in resolve.go).
	Match string `json:"match"`

	// Exclude is a list of glob patterns. A repo that matches any exclude pattern
	// is suppressed even if it matched the rule's Match. Useful for opt-out:
	// e.g. exclude archived repos via "myorg/archive-*".
	Exclude []string `json:"exclude,omitempty"`

	// Profile is the SandboxProfile path for cold-sandbox creation. Optional —
	// when empty the bridge uses WebhookHandler.DefaultProfile.
	Profile string `json:"profile,omitempty"`

	// Alias is the sandbox alias to use. When set, the handler looks up the
	// sandbox by alias (warm path: SQS enqueue). When empty, a fresh sandbox
	// is cold-created for every matching event (cold path: PutSandboxCreate).
	Alias string `json:"alias,omitempty"`

	// Agent overrides the sandbox agent for this dispatch turn: "claude", "codex",
	// or "". Empty means the sandbox profile default applies.
	Agent string `json:"agent,omitempty"`

	// CooldownSeconds, when > 0, suppresses repeated dispatch of the same
	// (event, repo, action) triple within the given window using the existing
	// nonces table (key prefix "gh-event-cooldown:"). 0 = no cooldown (default).
	CooldownSeconds int `json:"cooldown_seconds,omitempty"`

	// Prompt is the template injected as the agent's initial turn. May reference
	// the six named vars: {{repo}}, {{event}}, {{action}}, {{sender}},
	// {{default_branch}}, {{html_url}}. Unknown vars are left verbatim.
	Prompt string `json:"prompt"`
}

// EventPayload is the minimal parsed representation of a GitHub webhook body
// needed by the event router and template expander. It is populated from the
// generic GenericEventPayload struct in webhook_handler.go.
type EventPayload struct {
	// Repo is the repository full_name, e.g. "owner/repo".
	Repo string
	// Action is the action field from the webhook body, e.g. "created".
	Action string
	// Sender is the sender.login from the webhook body.
	Sender string
	// DefaultBranch is the repository.default_branch from the webhook body.
	DefaultBranch string
	// HTMLURL is the repository.html_url from the webhook body.
	HTMLURL string
}

// MatchEventRule returns the first EventRule from rules whose (On, Actions,
// Match, Exclude) all pass for the given eventType and payload. Returns nil
// when no rule matches.
//
// Resolution (mirrors Resolve in resolve.go — exact-before-glob first-match):
//
//  1. Pass 1: collect exact matches (rule.Match == payload.Repo) — first exact wins.
//  2. Pass 2: collect glob matches (isGlob(rule.Match) && path.Match hits) — first wins.
//  3. A rule fails pre-conditions when:
//     - rule.On != eventType, OR
//     - rule.Actions is non-empty AND payload.Action is not in rule.Actions, OR
//     - any exclude glob in rule.Exclude matches payload.Repo.
func MatchEventRule(eventType string, payload EventPayload, rules []EventRule) *EventRule {
	ruleMatches := func(r EventRule) bool {
		if r.On != eventType {
			return false
		}
		if len(r.Actions) > 0 && !containsAction(r.Actions, payload.Action) {
			return false
		}
		if excluded(r.Exclude, payload.Repo) {
			return false
		}
		return true
	}

	// Pass 1: exact match on rule.Match.
	for _, r := range rules {
		r := r // avoid loop-var aliasing
		if ruleMatches(r) && r.Match == payload.Repo {
			return &r
		}
	}

	// Pass 2: glob match, first-wins.
	for _, r := range rules {
		r := r // avoid loop-var aliasing
		if ruleMatches(r) && isGlob(r.Match) {
			ok, err := path.Match(r.Match, payload.Repo)
			if err == nil && ok {
				return &r
			}
		}
	}

	return nil
}

// ExpandEventTemplate replaces the six named template vars in tmpl with the
// corresponding values from p and eventType. Unknown vars (e.g. {{nope}}) are
// left verbatim — same no-text/template decision as ExpandTemplate in commands.go.
//
// Vars: {{repo}}, {{event}}, {{action}}, {{sender}}, {{default_branch}}, {{html_url}}.
func ExpandEventTemplate(tmpl string, p EventPayload, eventType string) string {
	r := strings.NewReplacer(
		"{{repo}}", p.Repo,
		"{{event}}", eventType,
		"{{action}}", p.Action,
		"{{sender}}", p.Sender,
		"{{default_branch}}", p.DefaultBranch,
		"{{html_url}}", p.HTMLURL,
	)
	return r.Replace(tmpl)
}

// containsAction reports whether action is in the list (case-sensitive).
func containsAction(actions []string, action string) bool {
	for _, a := range actions {
		if a == action {
			return true
		}
	}
	return false
}

// excluded reports whether repo matches any of the exclude globs.
// Exact non-glob entries are also matched directly. path.Match errors are
// treated as non-match (consistent with resolve.go).
func excluded(globs []string, repo string) bool {
	for _, g := range globs {
		if g == repo {
			return true
		}
		if isGlob(g) {
			ok, err := path.Match(g, repo)
			if err == nil && ok {
				return true
			}
		}
	}
	return false
}
