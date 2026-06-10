package bridge

import "strings"

// Target is one fanout destination for a HackerOne program: an {alias, profile}
// pair. Multi-target fanout (Phase 103) means a single trigger dispatches the
// same prompt to every Target in a program — the GitHub bridge resolved exactly
// one alias, H1 resolves N.
//
// This is the bridge-local mirror of config.H1Target, kept decoupled from
// internal/app/config so the bridge package has no dependency on the CLI config
// loader: the Lambda main.go (Plan 07/08 wiring) translates the SSM/env config
// into these structs and feeds them to Resolve.
type Target struct {
	Alias   string `json:"alias,omitempty"`
	Profile string `json:"profile,omitempty"`
}

// EventEntry maps a HackerOne lifecycle event to the auto-triage prompt.
// An absent/empty Events map leaves a program comment-keyword-only (auto-triage
// dormant by default).
type EventEntry struct {
	Prompt string `json:"prompt"`
}

// CommandEntry is a named comment-context command — the /command name -> prompt
// map referenced by ProgramEntry.Commands and the Resolve commands return value.
// It lives here because it is part of ProgramEntry's shape (Plan 103-02 owns
// ProgramEntry). Mirrors config.H1CommandEntry plus the per-command routing/allow
// fields the command engine (commands.go, Plan 103-03/05) consumes.
type CommandEntry struct {
	// Description is a human-readable summary shown in /help replies and km h1 status.
	Description string `json:"description,omitempty"`

	// Alias optionally overrides the program/target alias when this command is dispatched.
	Alias string `json:"alias,omitempty"`

	// Profile optionally overrides the profile when this command is dispatched.
	Profile string `json:"profile,omitempty"`

	// Allow is the per-command inner allowlist (intersection-narrows the program allowlist).
	Allow []string `json:"allow,omitempty"`

	// Prompt is the prompt template injected as the initial turn. May contain
	// "{{args}}" plus report-field refs expanded by the command engine.
	Prompt string `json:"prompt"`
}

// ProgramEntry maps a HackerOne program handle (the routing key that replaces
// GitHub's owner/repo) to its multi-target dispatch config, login allowlist, and
// the two trigger-model maps: Events (auto-triage) and Commands (comment-keyword).
//
// JSON field names mirror the km-config.yaml h1.programs surface so a JSON-encoded
// env form unmarshals into this struct directly.
type ProgramEntry struct {
	// Handle is the program handle matched (exact) against the incoming webhook's
	// data.report program-handle relationship. The deny-by-default routing key.
	Handle string `json:"handle"`

	// Targets is the multi-target fanout list. When a Target omits Alias it
	// defaults to "h1-{handle}"; when it omits Profile it defaults to defaultProfile.
	Targets []Target `json:"targets,omitempty"`

	// Allow is the HackerOne username allowlist (deny-by-default).
	Allow []string `json:"allow,omitempty"`

	// BotHandle optionally overrides the install-wide comment-keyword token for
	// this program.
	BotHandle string `json:"bot_handle,omitempty"`

	// Events is the auto-triage map (event type -> prompt). Dormant when empty.
	Events map[string]EventEntry `json:"events,omitempty"`

	// Commands is the comment-context command map (/command name -> prompt).
	Commands map[string]CommandEntry `json:"commands,omitempty"`

	// DefaultCommand names the Commands key dispatched when a triggering comment
	// carries the handle but no /command. Empty => free-form prompt.
	DefaultCommand string `json:"default_command,omitempty"`
}

// Resolve maps a HackerOne program handle to its multi-target dispatch config by
// scanning entries for an exact handle match.
//
// Resolution:
//  1. First exact handle match wins (HackerOne has no glob/pattern routing —
//     the program handle is an exact identifier, unlike GitHub's owner/repo globs).
//  2. Each returned Target's Alias defaults to "h1-{handle}" when empty, and its
//     Profile defaults to defaultProfile when empty.
//  3. events and commands are returned verbatim (non-nil maps so callers can range
//     safely; an empty events map means auto-triage is dormant for the program).
//  4. No match → ok=false; the handler 200-drops (no config for this program).
//
// Pure function with no AWS dependency — exhaustively table-testable without mocks.
func Resolve(handle string, entries []ProgramEntry, defaultProfile string) (targets []Target, allow []string, events map[string]EventEntry, commands map[string]CommandEntry, ok bool) {
	for _, e := range entries {
		if e.Handle != handle {
			continue
		}

		resolved := make([]Target, 0, len(e.Targets))
		for _, t := range e.Targets {
			if t.Alias == "" {
				t.Alias = defaultAlias(handle)
			}
			if t.Profile == "" {
				t.Profile = defaultProfile
			}
			resolved = append(resolved, t)
		}

		ev := e.Events
		if ev == nil {
			ev = map[string]EventEntry{}
		}
		cmds := e.Commands
		if cmds == nil {
			cmds = map[string]CommandEntry{}
		}
		return resolved, e.Allow, ev, cmds, true
	}

	return nil, nil, nil, nil, false
}

// defaultAlias derives the canonical alias for a program when a Target omits one:
// "h1-{handle}" (the HackerOne analog of the GitHub "gh-{owner}-{repo}" default).
func defaultAlias(handle string) string {
	return "h1-" + handle
}

// ContainsHandle reports whether body contains the literal handle token
// (case-insensitive). HackerOne internal comments have no bot user to @-mention,
// so the configured handle (e.g. "@km") is matched as a literal substring.
// An empty handle never matches (a dormant install must not trigger on every comment).
func ContainsHandle(body, handle string) bool {
	if handle == "" {
		return false
	}
	return strings.Contains(strings.ToLower(body), strings.ToLower(handle))
}

// ExtractBody returns the free-form prompt that follows the first occurrence of
// the handle token in body, trimmed. When the handle is absent (or empty), the
// whole body is returned trimmed. Mirror of pkg/github/bridge.ExtractMentionBody.
func ExtractBody(body, handle string) string {
	if handle == "" {
		return strings.TrimSpace(body)
	}
	lower := strings.ToLower(body)
	idx := strings.Index(lower, strings.ToLower(handle))
	if idx == -1 {
		return strings.TrimSpace(body)
	}
	after := body[idx+len(handle):]
	return strings.TrimSpace(after)
}
