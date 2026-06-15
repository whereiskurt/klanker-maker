package bridge_test

// event_router_test.go — Wave 0 RED scaffold for Phase 115.
//
// Covers GH-EVENT-ROUTER (MatchEventRule) and GH-EVENT-TEMPLATE (ExpandEventTemplate).
// These tests COMPILE-FAIL until Phase 115 Plan 02 implements event_router.go.
// That is the intended RED state for Wave 0.

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// ============================================================
// TestMatchEventRule — GH-EVENT-ROUTER
// ============================================================

func TestMatchEventRule(t *testing.T) {
	// baseline payload used in most cases
	basePayload := bridge.EventPayload{
		Repo:          "myorg/myrepo",
		Action:        "created",
		Sender:        "alice",
		DefaultBranch: "main",
		HTMLURL:       "https://github.com/myorg/myrepo",
	}

	tests := []struct {
		name      string
		eventType string
		payload   bridge.EventPayload
		rules     []bridge.EventRule
		// wantNil is true when no rule should match.
		wantNil bool
		// wantPrompt is the Prompt of the expected matching rule (discriminating field).
		wantPrompt string
	}{
		{
			name:      "empty rules slice returns nil",
			eventType: "repository",
			payload:   basePayload,
			rules:     nil,
			wantNil:   true,
		},
		{
			name:      "on != eventType returns nil",
			eventType: "push",
			payload:   basePayload,
			rules: []bridge.EventRule{
				{On: "repository", Match: "myorg/myrepo", Prompt: "should-not-match"},
			},
			wantNil: true,
		},
		{
			name:      "exact match wins over later glob rule",
			eventType: "repository",
			payload:   basePayload,
			rules: []bridge.EventRule{
				{On: "repository", Match: "myorg/*", Prompt: "glob-prompt"},
				{On: "repository", Match: "myorg/myrepo", Prompt: "exact-prompt"},
			},
			wantNil:    false,
			wantPrompt: "exact-prompt",
		},
		{
			name:      "exact match wins even when glob is first in list",
			eventType: "repository",
			payload:   basePayload,
			rules: []bridge.EventRule{
				{On: "repository", Match: "myorg/*", Prompt: "glob-first"},
				{On: "repository", Match: "myorg/myrepo", Prompt: "exact-second"},
			},
			wantNil:    false,
			wantPrompt: "exact-second",
		},
		{
			name:      "first glob rule wins when no exact match",
			eventType: "repository",
			payload:   basePayload,
			rules: []bridge.EventRule{
				{On: "repository", Match: "myorg/*", Prompt: "first-glob"},
				{On: "repository", Match: "myorg/*", Prompt: "second-glob"},
			},
			wantNil:    false,
			wantPrompt: "first-glob",
		},
		{
			name:      "glob myorg/* matches myorg/repo",
			eventType: "repository",
			payload: bridge.EventPayload{
				Repo:   "myorg/newrepo",
				Action: "created",
			},
			rules: []bridge.EventRule{
				{On: "repository", Match: "myorg/*", Prompt: "glob-match"},
			},
			wantNil:    false,
			wantPrompt: "glob-match",
		},
		{
			name:      "glob myorg/* does not match other/repo",
			eventType: "repository",
			payload: bridge.EventPayload{
				Repo:   "other/repo",
				Action: "created",
			},
			rules: []bridge.EventRule{
				{On: "repository", Match: "myorg/*", Prompt: "myorg-only"},
			},
			wantNil: true,
		},
		{
			name:      "actions empty matches any action",
			eventType: "repository",
			payload: bridge.EventPayload{
				Repo:   "myorg/myrepo",
				Action: "deleted",
			},
			rules: []bridge.EventRule{
				{On: "repository", Match: "myorg/*", Actions: nil, Prompt: "any-action"},
			},
			wantNil:    false,
			wantPrompt: "any-action",
		},
		{
			name:      "actions non-empty + action in list matches",
			eventType: "repository",
			payload: bridge.EventPayload{
				Repo:   "myorg/myrepo",
				Action: "created",
			},
			rules: []bridge.EventRule{
				{On: "repository", Match: "myorg/*", Actions: []string{"created", "publicized"}, Prompt: "action-match"},
			},
			wantNil:    false,
			wantPrompt: "action-match",
		},
		{
			name:      "actions non-empty + action NOT in list returns nil",
			eventType: "repository",
			payload: bridge.EventPayload{
				Repo:   "myorg/myrepo",
				Action: "deleted",
			},
			rules: []bridge.EventRule{
				{On: "repository", Match: "myorg/*", Actions: []string{"created", "publicized"}, Prompt: "should-not-match"},
			},
			wantNil: true,
		},
		{
			name:      "exclude glob suppresses otherwise-matching rule",
			eventType: "repository",
			payload: bridge.EventPayload{
				Repo:   "myorg/archive-old",
				Action: "created",
			},
			rules: []bridge.EventRule{
				{On: "repository", Match: "myorg/*", Exclude: []string{"myorg/archive-*"}, Prompt: "no-archive"},
			},
			wantNil: true,
		},
		{
			name:      "exclude glob does not suppress non-matching repo",
			eventType: "repository",
			payload: bridge.EventPayload{
				Repo:   "myorg/active-repo",
				Action: "created",
			},
			rules: []bridge.EventRule{
				{On: "repository", Match: "myorg/*", Exclude: []string{"myorg/archive-*"}, Prompt: "active-match"},
			},
			wantNil:    false,
			wantPrompt: "active-match",
		},
		{
			name:      "first non-excluded rule wins when multiple rules present",
			eventType: "push",
			payload: bridge.EventPayload{
				Repo:   "myorg/myrepo",
				Action: "",
			},
			rules: []bridge.EventRule{
				{On: "repository", Match: "myorg/*", Prompt: "wrong-event"},
				{On: "push", Match: "myorg/*", Prompt: "right-event"},
			},
			wantNil:    false,
			wantPrompt: "right-event",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := bridge.MatchEventRule(tc.eventType, tc.payload, tc.rules)
			if tc.wantNil {
				if got != nil {
					t.Errorf("MatchEventRule() = {Prompt:%q}, want nil", got.Prompt)
				}
				return
			}
			if got == nil {
				t.Fatalf("MatchEventRule() = nil, want rule with Prompt=%q", tc.wantPrompt)
			}
			if got.Prompt != tc.wantPrompt {
				t.Errorf("MatchEventRule().Prompt = %q, want %q", got.Prompt, tc.wantPrompt)
			}
		})
	}
}

// ============================================================
// TestExpandEventTemplate — GH-EVENT-TEMPLATE
// ============================================================

func TestExpandEventTemplate(t *testing.T) {
	p := bridge.EventPayload{
		Repo:          "myorg/myrepo",
		Action:        "created",
		Sender:        "alice",
		DefaultBranch: "main",
		HTMLURL:       "https://github.com/myorg/myrepo",
	}

	tests := []struct {
		name      string
		tmpl      string
		eventType string
		payload   bridge.EventPayload
		want      string
	}{
		{
			name:      "all six vars replaced",
			tmpl:      "repo={{repo}} event={{event}} action={{action}} sender={{sender}} branch={{default_branch}} url={{html_url}}",
			eventType: "repository",
			payload:   p,
			want:      "repo=myorg/myrepo event=repository action=created sender=alice branch=main url=https://github.com/myorg/myrepo",
		},
		{
			name:      "unknown var left verbatim",
			tmpl:      "known={{repo}} unknown={{nope}}",
			eventType: "repository",
			payload:   p,
			want:      "known=myorg/myrepo unknown={{nope}}",
		},
		{
			name:      "empty template returns empty string",
			tmpl:      "",
			eventType: "repository",
			payload:   p,
			want:      "",
		},
		{
			name:      "repeated var replaced in all positions",
			tmpl:      "{{repo}} and again {{repo}}",
			eventType: "repository",
			payload:   p,
			want:      "myorg/myrepo and again myorg/myrepo",
		},
		{
			name:      "event var reflects eventType parameter",
			tmpl:      "event={{event}}",
			eventType: "push",
			payload:   p,
			want:      "event=push",
		},
		{
			name:      "no vars → template returned verbatim",
			tmpl:      "A new repository was created.",
			eventType: "repository",
			payload:   p,
			want:      "A new repository was created.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := bridge.ExpandEventTemplate(tc.tmpl, tc.payload, tc.eventType)
			if got != tc.want {
				t.Errorf("ExpandEventTemplate() = %q, want %q", got, tc.want)
			}
		})
	}
}
