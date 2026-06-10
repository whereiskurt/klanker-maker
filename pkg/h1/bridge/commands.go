package bridge

// commands.go — the command / agent-verb parser for the km-h1-bridge.
//
// Forked near-verbatim from pkg/github/bridge/commands.go (Phase 99 command engine
// + Phase 102 agent verbs). HackerOne additions:
//   - /reply_to_researcher is a reserved token. Parsing it sets ParseResult.ReplyToResearcher
//     (parse-only intent; the visibility gate that honors it lives in Plan 04). It is NOT a
//     template command — researcher-visible replies are INTERNAL-by-default + allowlist-gated.
//   - ReportFields + ExpandTemplateFields pre-expand a fixed small set of report-field refs
//     ({{report_id}} {{title}} {{state}} {{program}}) on top of the existing {{args}} fill.
//
// The repo→program rename: where GitHub said "repo", HackerOne says "program". The
// pure functions here are program-agnostic (they take alias/profile/allow strings), so
// the only surface change is the reserved-token set + the report-field expansion.

import (
	"fmt"
	"sort"
	"strings"
)

// reservedReplyToResearcher is the HackerOne-specific reserved command token. It
// requests a researcher-visible (non-internal) reply — gated downstream (Plan 04)
// by Command-present AND program allowlist membership. Reserved here so it is never
// treated as a template command.
const reservedReplyToResearcher = "reply_to_researcher"

// CommandSet is the envelope written to SSM at {prefix}/config/h1/commands.
// It wraps the command map with the install-wide default_command so both travel
// together over the single SSM param. Read by the bridge; the bridge Lambda must NOT
// import internal/app/config — this is the Lambda-side boundary type.
// CommandSet wraps the command map with the install-wide default_command, written
// to SSM at {prefix}/config/h1/commands and read by the bridge.
type CommandSet struct {
	Commands       map[string]CommandEntry `json:"commands"`
	DefaultCommand string                  `json:"default_command,omitempty"`
}

// CommandEntry is the comment-context command type (/command name -> prompt).
// commands.go owns the command-parsing domain (Plan 103-03); resolve.go's
// ProgramEntry.Commands references this shared type. JSON field names mirror the
// km-config.yaml h1.programs[].commands surface (read from the SSM commands doc).
type CommandEntry struct {
	// Description is a human-readable summary shown in /help replies and km h1 status.
	Description string `json:"description,omitempty"`

	// Alias optionally overrides the program/target alias when this command is dispatched.
	Alias string `json:"alias,omitempty"`

	// Profile optionally overrides the profile when this command is dispatched.
	Profile string `json:"profile,omitempty"`

	// Allow is the per-command inner allowlist (intersection-narrows the program allowlist).
	Allow []string `json:"allow,omitempty"`

	// Prompt is the prompt template. May contain "{{args}}" plus the fixed report-field
	// refs ({{report_id}}/{{title}}/{{state}}/{{program}}) expanded by ExpandTemplateFields.
	Prompt string `json:"prompt"`
}

// CommandAction describes what the command pass wants the handler to do next.
type CommandAction int

const (
	// CommandActionPassthrough — no command matched, no default; fall through to free-form.
	CommandActionPassthrough CommandAction = iota
	// CommandActionDispatch — a command was resolved; handler does IO dispatch.
	CommandActionDispatch
	// CommandActionReply — post a reply and return 200 without dispatch (/help, multi-error).
	CommandActionReply
	// CommandActionDeny — sender passed the program gate but failed the command's inner gate.
	CommandActionDeny
)

// CommandPassResult is returned by RunCommandPass to the handler.
type CommandPassResult struct {
	Action    CommandAction
	Alias     string
	Profile   string
	Prompt    string
	ReplyText string
}

// ParseResult is the output of ParseCommands.
type ParseResult struct {
	// HelpRequested is true when the body contains "/help" (reserved built-in).
	HelpRequested bool

	// Known is the de-duplicated list of distinct known command names found.
	// len(Known) > 1 ⇒ MultiError.
	Known []string

	// MultiError is true when more than one distinct known command was found.
	MultiError bool

	// AgentVerb is the resolved agent override: "claude", "codex", or "" (none).
	// /claude and /codex are reserved built-ins on a separate axis from template
	// commands; same verb twice is deduped.
	AgentVerb string

	// AgentVerbConflict is true when BOTH /claude AND /codex appear.
	AgentVerbConflict bool

	// ReplyToResearcher is the HackerOne-specific intent flag: true when the body
	// contains "/reply_to_researcher" (reserved built-in). PARSE-ONLY — the gate
	// that honors it (Command-present AND program-allowlist) lives in Plan 04. The
	// safety default is internal (false).
	ReplyToResearcher bool
}

// StripCode removes fenced ``` blocks and `inline code` spans before token scanning,
// preventing false positives where a /command appears inside a code example.
func StripCode(body string) string {
	var out strings.Builder
	out.Grow(len(body))

	i := 0
	for i < len(body) {
		if i+2 < len(body) && body[i] == '`' && body[i+1] == '`' && body[i+2] == '`' {
			i += 3
			for i < len(body) && body[i] != '\n' {
				i++
			}
			for i < len(body) {
				if i+2 < len(body) && body[i] == '`' && body[i+1] == '`' && body[i+2] == '`' {
					i += 3
					break
				}
				i++
			}
			out.WriteByte(' ')
			continue
		}

		if body[i] == '`' {
			i++
			for i < len(body) && body[i] != '`' && body[i] != '\n' {
				i++
			}
			if i < len(body) && body[i] == '`' {
				i++
			}
			out.WriteByte(' ')
			continue
		}

		out.WriteByte(body[i])
		i++
	}

	return out.String()
}

// isCommandCandidate returns true when tok looks like a command token:
//   - starts with '/'
//   - remainder matches ^[A-Za-z][A-Za-z0-9_-]*$
//   - contains no further '/' (rejects /usr/bin/triage)
func isCommandCandidate(tok string) (name string, ok bool) {
	if len(tok) < 2 || tok[0] != '/' {
		return "", false
	}
	rest := tok[1:]
	if strings.ContainsRune(rest, '/') {
		return "", false
	}
	if !isLetter(rest[0]) {
		return "", false
	}
	for _, c := range rest[1:] {
		if !isLetter(byte(c)) && !isDigit(byte(c)) && c != '_' && c != '-' {
			return "", false
		}
	}
	return rest, true
}

func isLetter(c byte) bool { return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }

// ParseCommands scans body for command tokens and matches them against commands.
//
// Reserved built-ins (intercepted before the defined-command lookup):
//   - /help                → HelpRequested
//   - /claude, /codex      → AgentVerb (separate axis; same verb deduped; both → conflict)
//   - /reply_to_researcher → ReplyToResearcher intent flag (HackerOne addition)
//
// Defined commands are looked up case-SENSITIVE (literal YAML key). Distinct known
// commands are de-duplicated; >1 distinct → MultiError. Unknown tokens are ignored.
func ParseCommands(body string, commands map[string]CommandEntry) ParseResult {
	stripped := StripCode(body)
	tokens := strings.Fields(stripped)

	seenKnown := make(map[string]bool)

	var result ParseResult

	for _, tok := range tokens {
		name, ok := isCommandCandidate(tok)
		if !ok {
			continue
		}

		// /help — reserved built-in.
		if name == "help" {
			result.HelpRequested = true
			continue
		}

		// /claude and /codex — reserved agent-verb built-ins (separate axis).
		if name == "claude" || name == "codex" {
			if result.AgentVerb == "" || result.AgentVerb == name {
				result.AgentVerb = name // dedup: same verb twice is fine
			} else {
				result.AgentVerbConflict = true // /claude AND /codex = conflict
			}
			continue
		}

		// /reply_to_researcher — HackerOne reserved built-in. Sets the intent flag;
		// never a template command (researcher-visible reply is gated downstream).
		if name == reservedReplyToResearcher {
			result.ReplyToResearcher = true
			continue
		}

		if _, defined := commands[name]; defined {
			seenKnown[name] = true
		}
		// Unknown tokens are silently ignored (lenient — unknown = plain text).
	}

	for name := range seenKnown {
		result.Known = append(result.Known, name)
	}
	sort.Strings(result.Known)

	result.MultiError = len(result.Known) > 1

	// When both /claude and /codex appear, neither verb takes effect.
	if result.AgentVerbConflict {
		result.AgentVerb = ""
	}

	return result
}

// ExtractArgs strips the FIRST @{handle} (case-insensitive) and the FIRST
// /{commandToken} (case-sensitive, when non-""), then whitespace-normalizes.
func ExtractArgs(body, botHandle, commandToken string) string {
	return ExtractArgsWithAgent(body, botHandle, commandToken, "")
}

// ExtractArgsWithAgent is the agent-verb-aware variant. It strips, in addition to
// the mention and command token, the FIRST /{agentVerbToken} and the FIRST
// /reply_to_researcher token — so the sandbox agent never sees reserved tokens as
// literal prompt text.
func ExtractArgsWithAgent(body, botHandle, commandToken, agentVerbToken string) string {
	mention := "@" + botHandle
	mentionLower := strings.ToLower(mention)
	lowerBody := strings.ToLower(body)

	var workBuf strings.Builder
	workBuf.Grow(len(body))
	mentionIdx := strings.Index(lowerBody, mentionLower)
	if mentionIdx >= 0 {
		workBuf.WriteString(body[:mentionIdx])
		workBuf.WriteString(body[mentionIdx+len(mention):])
	} else {
		workBuf.WriteString(body)
	}
	work := workBuf.String()

	work = stripFirstToken(work, commandToken)
	work = stripFirstToken(work, agentVerbToken)
	// Always strip /reply_to_researcher from the prompt text (reserved built-in).
	work = stripFirstToken(work, reservedReplyToResearcher)

	return strings.Join(strings.Fields(work), " ")
}

// stripFirstToken removes the first occurrence of "/{token}" from s (exact,
// case-sensitive). No-op when token == "" or the token is absent.
func stripFirstToken(s, token string) string {
	if token == "" {
		return s
	}
	tok := "/" + token
	idx := strings.Index(s, tok)
	if idx < 0 {
		return s
	}
	end := idx + len(tok)
	return s[:idx] + s[end:]
}

// ExpandTemplate replaces "{{args}}" in the template with args. When the template
// has no "{{args}}", args are appended on a new line (unless args is empty).
func ExpandTemplate(template, args string) string {
	const placeholder = "{{args}}"
	if strings.Contains(template, placeholder) {
		return strings.ReplaceAll(template, placeholder, args)
	}
	if args == "" {
		return template
	}
	return template + "\n" + args
}

// ReportFields is the small fixed set of report-field references the handler
// supplies for template pre-expansion (HackerOne Pattern 3). Unknown {{x}} refs
// are left intact.
type ReportFields struct {
	ReportID string
	Title    string
	State    string
	Program  string
}

// ExpandTemplateFields pre-expands the fixed report-field refs ({{report_id}},
// {{title}}, {{state}}, {{program}}) from fields, then runs the standard {{args}}
// fill via ExpandTemplate. Unknown {{x}} placeholders are preserved verbatim.
//
// Order matters: report fields are substituted FIRST so a report value that itself
// contains "{{args}}" does not get double-expanded (report fields come from the
// HackerOne payload, not from operator template authoring — treat them as data).
func ExpandTemplateFields(template, args string, fields ReportFields) string {
	r := strings.NewReplacer(
		"{{report_id}}", fields.ReportID,
		"{{title}}", fields.Title,
		"{{state}}", fields.State,
		"{{program}}", fields.Program,
	)
	expanded := r.Replace(template)
	return ExpandTemplate(expanded, args)
}

// EffectiveDefault resolves the effective default command for a program: per-program
// default wins over install-wide; "" when both empty (free-form passthrough signal).
func EffectiveDefault(programDefault, installDefault string) string {
	if programDefault != "" {
		return programDefault
	}
	return installDefault
}

// ResolveCommandRouting applies command-overrides-program routing:
//
//	alias   = command.alias || program.alias
//	profile = command.profile || program.profile || default_profile
func ResolveCommandRouting(cmdAlias, cmdProfile, programAlias, programProfile, defaultProfile string) (alias, profile string) {
	alias = cmdAlias
	if alias == "" {
		alias = programAlias
	}
	profile = cmdProfile
	if profile == "" {
		profile = programProfile
	}
	if profile == "" {
		profile = defaultProfile
	}
	return alias, profile
}

// CommandAllowed implements the inner allow gate (command.allow). Returns true when
// cmdAllow is empty (no inner restriction) or sender is in cmdAllow (case-insensitive).
// The outer program allow gate is the caller's responsibility.
func CommandAllowed(sender string, cmdAllow []string) bool {
	if len(cmdAllow) == 0 {
		return true
	}
	senderLower := strings.ToLower(sender)
	for _, allowed := range cmdAllow {
		if strings.ToLower(allowed) == senderLower {
			return true
		}
	}
	return false
}

// buildHelpReply constructs a /help reply listing agent verbs, defined commands,
// and the effective default. currentAgentType (when non-"") adds a current-thread-agent line.
func buildHelpReply(commands map[string]CommandEntry, effectiveDefaultCmd string, currentAgentType string) string {
	var b strings.Builder

	b.WriteString("**Available agents:**\n")
	b.WriteString("- `/claude` — dispatch this thread to Claude\n")
	b.WriteString("- `/codex` — dispatch this thread to Codex\n")
	if currentAgentType != "" {
		b.WriteString(fmt.Sprintf("\n**Current thread agent:** `%s`\n", currentAgentType))
	}

	b.WriteString("\n**Available commands:**\n\n")

	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entry := commands[name]
		desc := entry.Description
		if desc == "" {
			desc = "(no description)"
		}
		b.WriteString(fmt.Sprintf("- `/%s` — %s\n", name, desc))
	}

	if effectiveDefaultCmd != "" {
		b.WriteString(fmt.Sprintf("\n**Default:** `/%s` (used when no command is specified)\n", effectiveDefaultCmd))
	}

	return b.String()
}

// RunCommandPass is the pure, IO-free entry point for the command dispatch layer.
// The handler calls this after the program-allow gate and before envelope construction.
//
// Parameters mirror the GitHub bridge (repo→program rename): fullBody, commands,
// install/program default command names, sender, programAlias, programProfile,
// defaultProfile, botHandle, currentAgentType.
func RunCommandPass(
	fullBody string,
	commands map[string]CommandEntry,
	installDefaultCmd, programDefaultCmd string,
	sender, programAlias, programProfile, defaultProfile string,
	botHandle string,
	currentAgentType string,
) CommandPassResult {
	effectiveDefaultCmd := EffectiveDefault(programDefaultCmd, installDefaultCmd)

	parsed := ParseCommands(fullBody, commands)

	if parsed.HelpRequested {
		return CommandPassResult{
			Action:    CommandActionReply,
			ReplyText: buildHelpReply(commands, effectiveDefaultCmd, currentAgentType),
		}
	}

	if parsed.MultiError {
		names := strings.Join(parsed.Known, ", /")
		return CommandPassResult{
			Action:    CommandActionReply,
			ReplyText: fmt.Sprintf("Multiple commands found: `/%s`. Please use only one command per comment.", names),
		}
	}

	var commandName string
	if len(parsed.Known) == 1 {
		commandName = parsed.Known[0]
	} else if effectiveDefaultCmd != "" {
		commandName = effectiveDefaultCmd
	}

	if commandName == "" {
		return CommandPassResult{Action: CommandActionPassthrough}
	}

	entry, ok := commands[commandName]
	if !ok {
		return CommandPassResult{Action: CommandActionPassthrough}
	}

	if !CommandAllowed(sender, entry.Allow) {
		return CommandPassResult{
			Action:    CommandActionDeny,
			ReplyText: fmt.Sprintf("You are not authorized to use the `/%s` command.", commandName),
		}
	}

	var cmdToken string
	if len(parsed.Known) == 1 {
		cmdToken = commandName
	}
	args := ExtractArgsWithAgent(fullBody, botHandle, cmdToken, parsed.AgentVerb)

	prompt := ExpandTemplate(entry.Prompt, args)

	alias, profile := ResolveCommandRouting(entry.Alias, entry.Profile, programAlias, programProfile, defaultProfile)

	return CommandPassResult{
		Action:  CommandActionDispatch,
		Alias:   alias,
		Profile: profile,
		Prompt:  prompt,
	}
}
