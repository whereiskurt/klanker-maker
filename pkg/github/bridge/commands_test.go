package bridge_test

// commands_test.go — table-driven pure-function tests for the command parsing/resolution
// layer. Mirrors the style of resolve_test.go ([]struct{name,...want...}).
//
// Coverage:
//   TestCommandParse   — StripCode + token scan: code-block suppression, embedded-slash
//                        rejection, /help reserved, multi/dedup/unknown/zero cases.
//   TestExtractArgs    — {{args}} extraction: strips first @mention + first /cmd token.
//   TestExpandTemplate — template expansion: {{args}} present vs absent.
//   TestEffectiveDefault — per-repo default wins over install-wide; unset → free-form.
//   TestCommandRouting — command.alias/profile override repo.alias/profile.
//   TestCommandAuth    — intersection narrowing: command.allow can only restrict.

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// ============================================================
// TestCommandParse — StripCode + token scanning
// ============================================================

func TestCommandParse(t *testing.T) {
	// Build a small command registry for matching.
	commands := map[string]bridge.CommandEntry{
		"patch":  {Description: "apply a patch", Prompt: "Apply the patch: {{args}}"},
		"review": {Description: "review the PR", Prompt: "Review the PR: {{args}}"},
	}

	tests := []struct {
		name string
		body string
		// expected ParseResult fields
		wantHelpRequested bool
		wantKnown         []string // sorted expected known command names; nil = expect none
		wantMultiError    bool
	}{
		{
			name:           "fenced code block suppresses /patch token",
			body:           "Look at this:\n```\n/patch this thing\n```\nno command here",
			wantKnown:      nil,
			wantMultiError: false,
		},
		{
			name:           "inline backtick suppresses /patch token",
			body:           "Try running `/patch` it won't work",
			wantKnown:      nil,
			wantMultiError: false,
		},
		{
			name:           "embedded slash /usr/bin/patch is not a candidate",
			body:           "I ran /usr/bin/patch and it failed",
			wantKnown:      nil,
			wantMultiError: false,
		},
		{
			name:      "bare /patch is a known command",
			body:      "@mybot[bot] please /patch the login bug",
			wantKnown: []string{"patch"},
		},
		{
			name:              "/help is reserved built-in, not in defined commands",
			body:              "@mybot[bot] /help",
			wantHelpRequested: true,
			wantKnown:         nil,
		},
		{
			name:           "two distinct known commands → multi-command error",
			body:           "@mybot[bot] /patch and /review the PR",
			wantKnown:      []string{"patch", "review"},
			wantMultiError: true,
		},
		{
			name:      "/patch repeated twice → deduped, single command",
			body:      "@mybot[bot] /patch the bug and also /patch it again",
			wantKnown: []string{"patch"},
		},
		{
			name:      "one known /patch + unknown /frobnicate → exactly one known",
			body:      "@mybot[bot] /patch and /frobnicate stuff",
			wantKnown: []string{"patch"},
		},
		{
			name:      "zero known tokens → 0-known result",
			body:      "@mybot[bot] please fix the bug",
			wantKnown: nil,
		},
		{
			// Case-sensitivity: command keys are literal; /PATCH does not match "patch"
			// Decision: commands are case-SENSITIVE (YAML key = exact match).
			name:      "/PATCH uppercase does not match lowercase key",
			body:      "@mybot[bot] /PATCH the file",
			wantKnown: nil,
		},
		{
			// Fenced block with language specifier
			name:           "fenced code with language specifier suppresses command",
			body:           "Example:\n```go\nfunc /review() {}\n```\nDone.",
			wantKnown:      nil,
			wantMultiError: false,
		},
		{
			// /help is intercepted first; MultiError is NOT set because /help is reserved
			// (not a "known command" from the defined map). The Known slice may contain
			// /patch (it was still scanned), but the handler short-circuits on HelpRequested
			// before inspecting Known — so Known content is irrelevant here. We only assert
			// HelpRequested=true and MultiError=false.
			name:              "/help with other known command — help wins, no multi-error",
			body:              "@mybot[bot] /help /patch",
			wantHelpRequested: true,
			wantKnown:         []string{"patch"}, // /help is reserved; /patch still found
			wantMultiError:    false,              // /help does not count as a "known" command
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

			// Compare known command sets (order-independent)
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
// TestExtractArgs — strip first @mention + first /command token
// ============================================================

func TestExtractArgs(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		botLogin     string
		commandToken string // empty string = command-less default path
		want         string
	}{
		{
			name:         "strips mention and command, leaves args",
			body:         "@mybot[bot] please /patch the login bug",
			botLogin:     "mybot[bot]",
			commandToken: "patch",
			want:         "please the login bug",
		},
		{
			name:         "command-less default path: strips only mention",
			body:         "@mybot[bot] fix the authentication issue",
			botLogin:     "mybot[bot]",
			commandToken: "",
			want:         "fix the authentication issue",
		},
		{
			name:         "command name appearing in prose: only first /command token stripped",
			body:         "@mybot[bot] /patch the /patch in the code",
			botLogin:     "mybot[bot]",
			commandToken: "patch",
			want:         "the /patch in the code",
		},
		{
			name:         "command appears before mention",
			body:         "/patch @mybot[bot] this file",
			botLogin:     "mybot[bot]",
			commandToken: "patch",
			want:         "this file",
		},
		{
			name:         "whitespace normalized — extra spaces collapsed",
			body:         "@mybot[bot]   please   /patch   the   bug",
			botLogin:     "mybot[bot]",
			commandToken: "patch",
			want:         "please the bug",
		},
		{
			name:         "mention only, no command, no extra text",
			body:         "@mybot[bot]",
			botLogin:     "mybot[bot]",
			commandToken: "",
			want:         "",
		},
		{
			name:         "case-insensitive mention strip",
			body:         "@MyBot[Bot] please /patch this",
			botLogin:     "mybot[bot]",
			commandToken: "patch",
			want:         "please this",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := bridge.ExtractArgs(tc.body, tc.botLogin, tc.commandToken)
			if got != tc.want {
				t.Errorf("ExtractArgs(%q, %q, %q) = %q want %q", tc.body, tc.botLogin, tc.commandToken, got, tc.want)
			}
		})
	}
}

// ============================================================
// TestExpandTemplate — {{args}} substitution
// ============================================================

func TestExpandTemplate(t *testing.T) {
	tests := []struct {
		name     string
		template string
		args     string
		want     string
	}{
		{
			name:     "{{args}} present → substituted inline",
			template: "Review the PR changes: {{args}}",
			args:     "focus on security",
			want:     "Review the PR changes: focus on security",
		},
		{
			name:     "{{args}} absent → args appended on new line",
			template: "Apply the standard patch process.",
			args:     "to the login module",
			want:     "Apply the standard patch process.\nto the login module",
		},
		{
			name:     "empty args with {{args}} present → no trailing garbage",
			template: "Review this PR: {{args}}",
			args:     "",
			want:     "Review this PR: ",
		},
		{
			name:     "empty args with {{args}} absent → no trailing newline appended",
			template: "Apply the standard patch.",
			args:     "",
			want:     "Apply the standard patch.",
		},
		{
			name:     "multiple {{args}} placeholders → all replaced",
			template: "Task: {{args}} — Details: {{args}}",
			args:     "fix auth",
			want:     "Task: fix auth — Details: fix auth",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := bridge.ExpandTemplate(tc.template, tc.args)
			if got != tc.want {
				t.Errorf("ExpandTemplate(%q, %q) = %q want %q", tc.template, tc.args, got, tc.want)
			}
		})
	}
}

// ============================================================
// TestEffectiveDefault — per-repo vs install-wide vs unset
// ============================================================

func TestEffectiveDefault(t *testing.T) {
	tests := []struct {
		name           string
		repoDefault    string
		installDefault string
		want           string
	}{
		{
			name:           "per-repo default wins over install-wide",
			repoDefault:    "review",
			installDefault: "patch",
			want:           "review",
		},
		{
			name:           "install-wide used when per-repo is empty",
			repoDefault:    "",
			installDefault: "patch",
			want:           "patch",
		},
		{
			name:           "both empty → free-form signal (empty string)",
			repoDefault:    "",
			installDefault: "",
			want:           "",
		},
		{
			name:           "per-repo set, install-wide empty → per-repo wins",
			repoDefault:    "explain",
			installDefault: "",
			want:           "explain",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := bridge.EffectiveDefault(tc.repoDefault, tc.installDefault)
			if got != tc.want {
				t.Errorf("EffectiveDefault(%q, %q) = %q want %q", tc.repoDefault, tc.installDefault, got, tc.want)
			}
		})
	}
}

// ============================================================
// TestCommandRouting — alias/profile precedence
// ============================================================

func TestCommandRouting(t *testing.T) {
	tests := []struct {
		name          string
		cmdAlias      string
		cmdProfile    string
		repoAlias     string
		repoProfile   string
		defaultProfile string
		wantAlias     string
		wantProfile   string
	}{
		{
			name:          "command.alias overrides repo.alias",
			cmdAlias:      "cmd-alias",
			repoAlias:     "repo-alias",
			wantAlias:     "cmd-alias",
			defaultProfile: "default",
			wantProfile:   "default",
		},
		{
			name:          "empty command.alias falls back to repo.alias",
			cmdAlias:      "",
			repoAlias:     "repo-alias",
			wantAlias:     "repo-alias",
			defaultProfile: "default",
			wantProfile:   "default",
		},
		{
			name:          "command.profile overrides repo.profile",
			cmdAlias:      "",
			cmdProfile:    "cmd-profile",
			repoAlias:     "repo-alias",
			repoProfile:   "repo-profile",
			wantAlias:     "repo-alias",
			wantProfile:   "cmd-profile",
			defaultProfile: "default",
		},
		{
			name:          "empty command.profile falls back to repo.profile",
			cmdAlias:      "",
			cmdProfile:    "",
			repoAlias:     "repo-alias",
			repoProfile:   "repo-profile",
			wantAlias:     "repo-alias",
			wantProfile:   "repo-profile",
			defaultProfile: "default",
		},
		{
			name:          "both profile empty → default_profile",
			cmdAlias:      "",
			cmdProfile:    "",
			repoAlias:     "repo-alias",
			repoProfile:   "",
			wantAlias:     "repo-alias",
			wantProfile:   "default",
			defaultProfile: "default",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			alias, profile := bridge.ResolveCommandRouting(tc.cmdAlias, tc.cmdProfile, tc.repoAlias, tc.repoProfile, tc.defaultProfile)
			if alias != tc.wantAlias {
				t.Errorf("alias=%q want %q", alias, tc.wantAlias)
			}
			if profile != tc.wantProfile {
				t.Errorf("profile=%q want %q", profile, tc.wantProfile)
			}
		})
	}
}

// ============================================================
// TestCommandAuth — intersection narrowing
// ============================================================

func TestCommandAuth(t *testing.T) {
	tests := []struct {
		name         string
		sender       string
		cmdAllow     []string // inner gate; nil or empty = command doesn't restrict
		wantAllowed  bool
	}{
		{
			name:        "sender passes command.allow → allowed",
			sender:      "alice",
			cmdAllow:    []string{"alice", "bob"},
			wantAllowed: true,
		},
		{
			name:        "sender fails command.allow → denied (narrowed)",
			sender:      "charlie",
			cmdAllow:    []string{"alice", "bob"},
			wantAllowed: false,
		},
		{
			name:        "command.allow empty → no inner restriction → allowed",
			sender:      "anyone",
			cmdAllow:    nil,
			wantAllowed: true,
		},
		{
			name:        "command.allow empty slice → no inner restriction → allowed",
			sender:      "anyone",
			cmdAllow:    []string{},
			wantAllowed: true,
		},
		{
			name:        "case-insensitive comparison: Alice matches alice",
			sender:      "Alice",
			cmdAllow:    []string{"alice"},
			wantAllowed: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := bridge.CommandAllowed(tc.sender, tc.cmdAllow)
			if got != tc.wantAllowed {
				t.Errorf("CommandAllowed(%q, %v) = %v want %v", tc.sender, tc.cmdAllow, got, tc.wantAllowed)
			}
		})
	}
}

// ============================================================
// TestRunCommandPass — integration of all pure layers
// ============================================================

func TestRunCommandPass(t *testing.T) {
	commands := map[string]bridge.CommandEntry{
		"patch": {
			Description: "apply a patch",
			Alias:       "patch-alias",
			Profile:     "patch-profile",
			Allow:       []string{"alice"},
			Prompt:      "Apply: {{args}}",
		},
		"review": {
			Description: "review the PR",
			Prompt:      "Review this PR.\n",
			// No Alias, no Profile, no Allow restriction
		},
	}

	tests := []struct {
		name              string
		mentionBody       string // post-mention-strip body (pre-args-extract)
		fullBody          string // full original comment body
		sender            string
		repoAlias         string
		repoProfile       string
		defaultProfile    string
		repoDefaultCmd    string
		installDefaultCmd string
		wantAction        bridge.CommandAction
		wantAliasContains string  // substring check on alias
		wantPromptContains string // substring check on prompt
		wantReplyContains string  // substring check on reply text (for Reply/Deny actions)
	}{
		{
			name:               "known /patch command dispatches correctly",
			fullBody:           "@mybot[bot] /patch the login bug",
			sender:             "alice",
			repoAlias:          "repo-alias",
			repoProfile:        "repo-profile",
			defaultProfile:     "default-profile",
			wantAction:         bridge.CommandActionDispatch,
			wantAliasContains:  "patch-alias",
			wantPromptContains: "Apply:",
		},
		{
			name:           "multi-command error → Reply action",
			fullBody:       "@mybot[bot] /patch and /review",
			sender:         "alice",
			repoAlias:      "repo-alias",
			defaultProfile: "default",
			wantAction:     bridge.CommandActionReply,
		},
		{
			name:               "command.allow denies non-listed sender → Deny action",
			fullBody:           "@mybot[bot] /patch the bug",
			sender:             "mallory",
			repoAlias:          "repo-alias",
			defaultProfile:     "default",
			wantAction:         bridge.CommandActionDeny,
			wantReplyContains:  "not authorized",
		},
		{
			name:               "/help → Reply action with command list",
			fullBody:           "@mybot[bot] /help",
			sender:             "alice",
			repoAlias:          "repo-alias",
			defaultProfile:     "default",
			wantAction:         bridge.CommandActionReply,
			wantReplyContains:  "patch",
		},
		{
			name:               "no known command + no default → Passthrough",
			fullBody:           "@mybot[bot] fix the tests please",
			sender:             "alice",
			repoAlias:          "repo-alias",
			defaultProfile:     "default",
			wantAction:         bridge.CommandActionPassthrough,
		},
		{
			name:               "no known command + install default-command → Dispatch with default",
			fullBody:           "@mybot[bot] the auth module is broken",
			sender:             "alice",
			repoAlias:          "repo-alias",
			repoProfile:        "repo-profile",
			defaultProfile:     "default",
			installDefaultCmd:  "review",
			wantAction:         bridge.CommandActionDispatch,
			wantPromptContains: "Review this PR",
		},
		{
			name:               "repo default-command overrides install default",
			fullBody:           "@mybot[bot] the auth module is broken",
			sender:             "alice",
			repoAlias:          "repo-alias",
			repoProfile:        "repo-profile",
			defaultProfile:     "default",
			installDefaultCmd:  "patch",
			repoDefaultCmd:     "review",
			wantAction:         bridge.CommandActionDispatch,
			wantPromptContains: "Review this PR",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := bridge.RunCommandPass(
				tc.fullBody,
				commands,
				tc.installDefaultCmd,
				tc.repoDefaultCmd,
				tc.sender,
				tc.repoAlias,
				tc.repoProfile,
				tc.defaultProfile,
				"mybot[bot]",
			)

			if result.Action != tc.wantAction {
				t.Errorf("Action=%v want %v (ReplyText=%q, Prompt=%q)", result.Action, tc.wantAction, result.ReplyText, result.Prompt)
			}
			if tc.wantAliasContains != "" && !containsStr(result.Alias, tc.wantAliasContains) {
				t.Errorf("Alias=%q want to contain %q", result.Alias, tc.wantAliasContains)
			}
			if tc.wantPromptContains != "" && !containsStr(result.Prompt, tc.wantPromptContains) {
				t.Errorf("Prompt=%q want to contain %q", result.Prompt, tc.wantPromptContains)
			}
			if tc.wantReplyContains != "" && !containsStr(result.ReplyText, tc.wantReplyContains) {
				t.Errorf("ReplyText=%q want to contain %q", result.ReplyText, tc.wantReplyContains)
			}
		})
	}
}

// containsStr is a case-insensitive substring check for test assertions.
func containsStr(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && (s == sub || len(s) > 0 && (func() bool {
		sl := strings.ToLower(s)
		subl := strings.ToLower(sub)
		return strings.Contains(sl, subl)
	})())
}
