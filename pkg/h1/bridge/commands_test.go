package bridge_test

// commands_test.go — table-driven tests for the HackerOne command/agent-verb parser.
// Ported near-verbatim from pkg/github/bridge/commands_test.go, with the HackerOne
// additions:
//   - /reply_to_researcher reserved token → ReplyToResearcher intent flag (parse-only;
//     the visibility gate that consumes it lives in Plan 04).
//   - ExpandTemplate report-field pre-expansion: {{report_id}} {{title}} {{state}}
//     {{program}} fill from supplied values; {{args}} unchanged.
//
// Coverage:
//   TestParseCommands  — StripCode + token scan: code suppression, multi/dedup/zero,
//                        /help reserved, /reply_to_researcher reserved + intent flag.
//   TestAgentVerb      — /claude → claude; /codex → codex; both → conflict; neither → "".
//   TestReservedTokens — /help, /claude, /codex, /reply_to_researcher are reserved
//                        (never treated as template commands).
//   TestExpandTemplate — {{args}} + the fixed report-field refs.

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

// ============================================================
// TestParseCommands — StripCode + token scanning
// ============================================================

func TestParseCommands(t *testing.T) {
	commands := map[string]bridge.CommandEntry{
		"triage": {Description: "triage the report", Prompt: "Triage: {{args}}"},
		"summarize": {Description: "summarize the report", Prompt: "Summarize: {{args}}"},
	}

	tests := []struct {
		name                  string
		body                  string
		wantHelpRequested     bool
		wantKnown             []string
		wantMultiError        bool
		wantAgentVerb         string
		wantAgentVerbConflict bool
		wantReplyToResearcher bool
	}{
		{
			name:           "fenced code block suppresses /triage token",
			body:           "Look at this:\n```\n/triage this report\n```\nno command here",
			wantKnown:      nil,
			wantMultiError: false,
		},
		{
			name:           "inline backtick suppresses /triage token",
			body:           "Try running `/triage` it won't work",
			wantKnown:      nil,
			wantMultiError: false,
		},
		{
			name:           "embedded slash /usr/bin/triage is not a candidate",
			body:           "I ran /usr/bin/triage and it failed",
			wantKnown:      nil,
			wantMultiError: false,
		},
		{
			name:      "bare /triage is a known command",
			body:      "@km please /triage the XSS report",
			wantKnown: []string{"triage"},
		},
		{
			name:              "/help is reserved built-in, not in defined commands",
			body:              "@km /help",
			wantHelpRequested: true,
			wantKnown:         nil,
		},
		{
			name:           "two distinct known commands → multi-command error",
			body:           "@km /triage and /summarize the report",
			wantKnown:      []string{"summarize", "triage"},
			wantMultiError: true,
		},
		{
			name:      "/triage repeated twice → deduped, single command",
			body:      "@km /triage now and /triage again",
			wantKnown: []string{"triage"},
		},
		{
			name:      "one known /triage + unknown /frobnicate → exactly one known",
			body:      "@km /triage and /frobnicate stuff",
			wantKnown: []string{"triage"},
		},
		{
			name:      "zero known tokens → 0-known result",
			body:      "@km please look at this report",
			wantKnown: nil,
		},
		{
			name:      "/TRIAGE uppercase does not match lowercase key",
			body:      "@km /TRIAGE the report",
			wantKnown: nil,
		},

		// ── HackerOne addition: /reply_to_researcher reserved token ───────────────
		{
			name:                  "/reply_to_researcher sets ReplyToResearcher intent, not a known command",
			body:                  "@km /triage this. /reply_to_researcher",
			wantKnown:             []string{"triage"},
			wantReplyToResearcher: true,
		},
		{
			name:                  "/reply_to_researcher alone → intent flag set, no known command",
			body:                  "@km /reply_to_researcher please respond",
			wantKnown:             nil,
			wantReplyToResearcher: true,
		},
		{
			name:                  "/reply_to_researcher inside code fence → NOT recognized",
			body:                  "Example:\n```\n/reply_to_researcher\n```\ndone",
			wantReplyToResearcher: false,
		},

		// ── Agent verb cases ──────────────────────────────────────────────────────
		{
			name:          "/claude anywhere → AgentVerb=claude",
			body:          "@km /claude please triage",
			wantAgentVerb: "claude",
		},
		{
			name:          "/codex anywhere → AgentVerb=codex",
			body:          "Great. /codex summarize the report",
			wantAgentVerb: "codex",
		},
		{
			name:                  "/claude AND /codex → AgentVerbConflict",
			body:                  "@km /claude /codex triage",
			wantAgentVerbConflict: true,
		},
		{
			name:          "/codex /codex deduped → no conflict",
			body:          "@km /codex /codex triage",
			wantAgentVerb: "codex",
		},
		{
			name:          "/codex /triage compose → both axes resolved",
			body:          "@km /codex /triage the XSS",
			wantAgentVerb: "codex",
			wantKnown:     []string{"triage"},
		},
		{
			name:                  "compose: /codex /triage /reply_to_researcher → all three axes",
			body:                  "@km /codex /triage and /reply_to_researcher",
			wantAgentVerb:         "codex",
			wantKnown:             []string{"triage"},
			wantReplyToResearcher: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := bridge.ParseCommands(tc.body, commands)

			if result.HelpRequested != tc.wantHelpRequested {
				t.Errorf("HelpRequested=%v want %v", result.HelpRequested, tc.wantHelpRequested)
			}
			if result.MultiError != tc.wantMultiError {
				t.Errorf("MultiError=%v want %v", result.MultiError, tc.wantMultiError)
			}
			if result.AgentVerb != tc.wantAgentVerb {
				t.Errorf("AgentVerb=%q want %q", result.AgentVerb, tc.wantAgentVerb)
			}
			if result.AgentVerbConflict != tc.wantAgentVerbConflict {
				t.Errorf("AgentVerbConflict=%v want %v", result.AgentVerbConflict, tc.wantAgentVerbConflict)
			}
			if result.ReplyToResearcher != tc.wantReplyToResearcher {
				t.Errorf("ReplyToResearcher=%v want %v", result.ReplyToResearcher, tc.wantReplyToResearcher)
			}

			gotSet := make(map[string]bool, len(result.Known))
			for _, k := range result.Known {
				gotSet[k] = true
			}
			wantSet := make(map[string]bool, len(tc.wantKnown))
			for _, k := range tc.wantKnown {
				wantSet[k] = true
			}
			if len(gotSet) != len(wantSet) {
				t.Errorf("Known=%v want %v", result.Known, tc.wantKnown)
			} else {
				for k := range wantSet {
					if !gotSet[k] {
						t.Errorf("Known=%v missing %q, want %v", result.Known, k, tc.wantKnown)
					}
				}
			}
		})
	}
}

// ============================================================
// TestParseCommands_StripCode — /command inside a code fence is ignored
// ============================================================

func TestParseCommands_StripCode(t *testing.T) {
	commands := map[string]bridge.CommandEntry{"triage": {Prompt: "Triage: {{args}}"}}
	body := "Here is an example:\n```\n@km /triage /reply_to_researcher\n```\nplain text"
	r := bridge.ParseCommands(body, commands)
	if len(r.Known) != 0 {
		t.Errorf("Known=%v want none (code-fenced)", r.Known)
	}
	if r.ReplyToResearcher {
		t.Errorf("ReplyToResearcher=true want false (code-fenced)")
	}
}

// ============================================================
// TestAgentVerb — agent selection / conflict / none
// ============================================================

func TestAgentVerb(t *testing.T) {
	commands := map[string]bridge.CommandEntry{}
	tests := []struct {
		name         string
		body         string
		wantVerb     string
		wantConflict bool
	}{
		{"claude selected", "@km /claude triage", "claude", false},
		{"codex selected", "@km /codex triage", "codex", false},
		{"both → conflict, verb cleared", "@km /claude /codex triage", "", true},
		{"neither → empty (box default)", "@km please triage", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := bridge.ParseCommands(tc.body, commands)
			if r.AgentVerb != tc.wantVerb {
				t.Errorf("AgentVerb=%q want %q", r.AgentVerb, tc.wantVerb)
			}
			if r.AgentVerbConflict != tc.wantConflict {
				t.Errorf("AgentVerbConflict=%v want %v", r.AgentVerbConflict, tc.wantConflict)
			}
		})
	}
}

// ============================================================
// TestReservedTokens — reserved tokens are never template commands
// ============================================================

func TestReservedTokens(t *testing.T) {
	// Even if an operator (mis)declared these as command keys, the reserved
	// interceptions fire first — they never appear in Known.
	commands := map[string]bridge.CommandEntry{
		"help":                {Prompt: "should never match"},
		"claude":              {Prompt: "should never match"},
		"codex":               {Prompt: "should never match"},
		"reply_to_researcher": {Prompt: "should never match"},
		"triage":              {Prompt: "Triage: {{args}}"},
	}

	r := bridge.ParseCommands("@km /help /claude /codex /reply_to_researcher /triage", commands)

	for _, k := range r.Known {
		switch k {
		case "help", "claude", "codex", "reply_to_researcher":
			t.Errorf("reserved token %q leaked into Known=%v", k, r.Known)
		}
	}
	if !r.HelpRequested {
		t.Errorf("HelpRequested=false want true")
	}
	if !r.ReplyToResearcher {
		t.Errorf("ReplyToResearcher=false want true")
	}
	// /claude AND /codex both present → conflict, verb cleared.
	if !r.AgentVerbConflict {
		t.Errorf("AgentVerbConflict=false want true")
	}
	// /triage is the only real known command.
	if len(r.Known) != 1 || r.Known[0] != "triage" {
		t.Errorf("Known=%v want [triage]", r.Known)
	}
}

// ============================================================
// TestExpandTemplate — {{args}} + report-field refs
// ============================================================

func TestExpandTemplate(t *testing.T) {
	// {{args}}-only behavior must match the GitHub semantics exactly.
	t.Run("args only", func(t *testing.T) {
		tests := []struct {
			name, template, args, want string
		}{
			{"args present", "Triage: {{args}}", "focus on XSS", "Triage: focus on XSS"},
			{"args absent appended", "Triage the report.", "the login bug", "Triage the report.\nthe login bug"},
			{"empty args with placeholder", "Triage: {{args}}", "", "Triage: "},
			{"empty args no placeholder", "Triage the report.", "", "Triage the report."},
			{"multiple placeholders", "T: {{args}} — D: {{args}}", "fix", "T: fix — D: fix"},
		}
		for _, tc := range tests {
			if got := bridge.ExpandTemplate(tc.template, tc.args); got != tc.want {
				t.Errorf("%s: ExpandTemplate(%q,%q)=%q want %q", tc.name, tc.template, tc.args, got, tc.want)
			}
		}
	})

	// Report-field pre-expansion: {{report_id}} {{title}} {{state}} {{program}}.
	t.Run("report-field refs", func(t *testing.T) {
		fields := bridge.ReportFields{
			ReportID: "7000001",
			Title:    "Reflected XSS on /search",
			State:    "new",
			Program:  "km-sandbox",
		}
		tmpl := "Report {{report_id}} ({{title}}) in program {{program}} is {{state}}. Args: {{args}}"
		got := bridge.ExpandTemplateFields(tmpl, "be careful", fields)
		want := "Report 7000001 (Reflected XSS on /search) in program km-sandbox is new. Args: be careful"
		if got != want {
			t.Errorf("ExpandTemplateFields=%q want %q", got, want)
		}
	})

	t.Run("report-field refs without args placeholder still append args", func(t *testing.T) {
		fields := bridge.ReportFields{ReportID: "42", Title: "T", State: "triaged", Program: "p"}
		got := bridge.ExpandTemplateFields("Handle report {{report_id}} ({{state}}).", "extra note", fields)
		want := "Handle report 42 (triaged).\nextra note"
		if got != want {
			t.Errorf("ExpandTemplateFields=%q want %q", got, want)
		}
	})

	t.Run("unknown placeholder left intact", func(t *testing.T) {
		fields := bridge.ReportFields{ReportID: "1"}
		got := bridge.ExpandTemplateFields("Report {{report_id}} {{unknown}}", "", fields)
		want := "Report 1 {{unknown}}"
		if got != want {
			t.Errorf("ExpandTemplateFields=%q want %q (unknown ref preserved)", got, want)
		}
	})
}

// ============================================================
// Ported routing/auth helpers (unchanged from GitHub) — smoke coverage
// ============================================================

func TestRunCommandPass_Smoke(t *testing.T) {
	commands := map[string]bridge.CommandEntry{
		"triage": {Description: "triage", Alias: "triage-alias", Prompt: "Triage: {{args}}", Allow: []string{"alice"}},
	}

	t.Run("dispatch", func(t *testing.T) {
		r := bridge.RunCommandPass("@km /triage the XSS", commands, "", "", "alice",
			"prog-alias", "prog-profile", "default-profile", "km", "")
		if r.Action != bridge.CommandActionDispatch {
			t.Fatalf("Action=%v want Dispatch", r.Action)
		}
		if !strings.Contains(r.Prompt, "Triage:") {
			t.Errorf("Prompt=%q want Triage:", r.Prompt)
		}
		if r.Alias != "triage-alias" {
			t.Errorf("Alias=%q want triage-alias", r.Alias)
		}
	})

	t.Run("multi-command reply", func(t *testing.T) {
		cmds := map[string]bridge.CommandEntry{
			"triage": {Prompt: "T"}, "summarize": {Prompt: "S"},
		}
		r := bridge.RunCommandPass("@km /triage /summarize", cmds, "", "", "alice",
			"a", "p", "d", "km", "")
		if r.Action != bridge.CommandActionReply {
			t.Errorf("Action=%v want Reply", r.Action)
		}
	})

	t.Run("deny on inner allow", func(t *testing.T) {
		r := bridge.RunCommandPass("@km /triage it", commands, "", "", "mallory",
			"a", "p", "d", "km", "")
		if r.Action != bridge.CommandActionDeny {
			t.Errorf("Action=%v want Deny", r.Action)
		}
	})

	t.Run("help reply lists agents", func(t *testing.T) {
		r := bridge.RunCommandPass("@km /help", commands, "", "", "alice",
			"a", "p", "d", "km", "codex")
		if r.Action != bridge.CommandActionReply {
			t.Fatalf("Action=%v want Reply", r.Action)
		}
		for _, want := range []string{"/claude", "/codex", "Available agents", "Current thread agent"} {
			if !strings.Contains(r.ReplyText, want) {
				t.Errorf("help reply missing %q:\n%s", want, r.ReplyText)
			}
		}
	})
}
