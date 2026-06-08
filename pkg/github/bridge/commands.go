package bridge

import (
	"fmt"
	"sort"
	"strings"
)

// CommandSet is the envelope written to SSM at {prefix}/config/github/commands.
// It wraps the command map with the install-wide default_command so both travel
// together over the single SSM param — a single source of truth (design D8).
//
// Written by km init (internal/app/cmd) and read by SSMCommandsFetcher (bridge side).
// The bridge Lambda must NOT import internal/app/config; CommandSet is the
// Lambda-side boundary type.
type CommandSet struct {
	Commands       map[string]CommandEntry `json:"commands"`
	DefaultCommand string                  `json:"default_command,omitempty"`
}

// CommandEntry is the bridge-local representation of a configured command.
// It is unmarshalled from the SSM JSON doc at {prefix}/config/github/commands
// (written by km init from config.GithubCommandEntry). The bridge Lambda must NOT
// import internal/app/config — this type is the Lambda-side boundary.
//
// JSON field names mirror the km-config.yaml surface so json.Unmarshal from the
// SSM doc works directly (same convention as RepoEntry).
type CommandEntry struct {
	// Description is a human-readable summary shown in /help replies.
	Description string `json:"description,omitempty"`

	// Alias overrides the repo alias when this command is dispatched.
	// Empty → falls back to the matched repo's alias.
	Alias string `json:"alias,omitempty"`

	// Profile overrides the profile when this command is dispatched.
	// Empty → falls back to the matched repo's profile (or default_profile).
	Profile string `json:"profile,omitempty"`

	// Allow is the per-command inner allowlist. Empty → no inner restriction;
	// only the repo's outer allow gate applies. When non-empty, the effective
	// allowlist is repo.allow ∩ command.allow (intersection narrowing only —
	// a command can never widen the repo gate).
	Allow []string `json:"allow,omitempty"`

	// Prompt is the prompt template for this command. May contain "{{args}}"
	// which is replaced by the extracted args from the comment body. When the
	// template contains no "{{args}}", args are appended on a new line.
	Prompt string `json:"prompt"`
}

// CommandAction describes what the command pass wants the handler to do next.
type CommandAction int

const (
	// CommandActionPassthrough means no command was matched and no default is configured.
	// The handler should fall through to free-form body dispatch (ExtractMentionBody).
	CommandActionPassthrough CommandAction = iota

	// CommandActionDispatch means a command was resolved (explicit or via default).
	// result.Alias, result.Profile, and result.Prompt are set; handler does IO dispatch.
	CommandActionDispatch

	// CommandActionReply means the bridge should post a comment reply and return 200
	// without dispatching to the sandbox. Used for /help and multi-command errors.
	CommandActionReply

	// CommandActionDeny means the sender passed the repo allow gate but failed the
	// command's inner allow gate. The bridge posts a polite "not authorized" reply.
	CommandActionDeny
)

// CommandPassResult is the value returned by RunCommandPass to the handler.
// The handler switches on Action and reads the appropriate fields.
type CommandPassResult struct {
	// Action instructs the handler how to proceed.
	Action CommandAction

	// Alias is the resolved sandbox alias (set when Action == CommandActionDispatch).
	Alias string

	// Profile is the resolved profile (set when Action == CommandActionDispatch).
	Profile string

	// Prompt is the fully-expanded agent prompt (set when Action == CommandActionDispatch).
	Prompt string

	// ReplyText is the comment body to post (set when Action == CommandActionReply or CommandActionDeny).
	ReplyText string
}

// ParseResult is the output of ParseCommands: which command tokens were
// found, whether /help was requested, and whether multiple distinct known
// commands conflict.
type ParseResult struct {
	// HelpRequested is true when the comment body contains "/help" (reserved
	// built-in; intercepted before the defined-command lookup).
	HelpRequested bool

	// Known is the de-duplicated list of distinct known command names found in the body.
	// When len(Known) > 1, MultiError is also true.
	Known []string

	// MultiError is true when more than one distinct known command was found.
	// The handler should post an error reply listing the conflicting commands.
	MultiError bool

	// AgentVerb is the resolved agent override found in the comment body:
	// "claude", "codex", or "" (none found). /claude and /codex are reserved
	// built-in tokens on a separate axis from template commands — they compose
	// freely with any /command (e.g. "/codex /patch fix X") and are intercepted
	// before the defined-command lookup. Same verb twice is deduped (no conflict).
	// Phase 102.
	AgentVerb string

	// AgentVerbConflict is true when BOTH /claude AND /codex appear in the same
	// comment. The handler posts a "Specify one agent" error reply and returns 200
	// without dispatching. Phase 102.
	AgentVerbConflict bool
}

// StripCode removes fenced ``` blocks and `inline code` spans from body before
// token scanning. This prevents false positives where a /command appears inside
// a code example rather than as an actionable invocation.
//
// The stripping is intentionally simple — a single-pass state machine that
// recognises only ``` fences and `single-backtick` spans. It does not attempt
// full CommonMark parsing; the goal is to eliminate common false-positive
// patterns without over-engineering.
func StripCode(body string) string {
	var out strings.Builder
	out.Grow(len(body))

	i := 0
	for i < len(body) {
		// Check for fenced code block (``` at the start of a line or after newline).
		if i+2 < len(body) && body[i] == '`' && body[i+1] == '`' && body[i+2] == '`' {
			// Skip the opening fence + optional language specifier + content until closing fence.
			i += 3
			// Skip optional language specifier (everything to end of line).
			for i < len(body) && body[i] != '\n' {
				i++
			}
			// Skip past the closing ```.
			for i < len(body) {
				if i+2 < len(body) && body[i] == '`' && body[i+1] == '`' && body[i+2] == '`' {
					i += 3
					break
				}
				i++
			}
			// Replace with a space so tokens on either side don't run together.
			out.WriteByte(' ')
			continue
		}

		// Check for inline backtick span.
		if body[i] == '`' {
			i++ // skip opening backtick
			for i < len(body) && body[i] != '`' && body[i] != '\n' {
				i++
			}
			if i < len(body) && body[i] == '`' {
				i++ // skip closing backtick
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
//   - the remainder (after the '/') matches ^[A-Za-z][A-Za-z0-9_-]*$
//   - contains no further '/' characters (rejects /usr/bin/patch)
//
// This is evaluated with strings.Fields splitting, so tok is already whitespace-bounded.
func isCommandCandidate(tok string) (name string, ok bool) {
	if len(tok) < 2 || tok[0] != '/' {
		return "", false
	}
	rest := tok[1:]
	// No embedded slash (rejects /usr/bin/patch etc).
	if strings.ContainsRune(rest, '/') {
		return "", false
	}
	// First char must be a letter.
	if !isLetter(rest[0]) {
		return "", false
	}
	// Remaining chars must be letter, digit, underscore, or hyphen.
	for _, c := range rest[1:] {
		if !isLetter(byte(c)) && !isDigit(byte(c)) && c != '_' && c != '-' {
			return "", false
		}
	}
	return rest, true
}

func isLetter(c byte) bool { return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }

// ParseCommands scans body for command tokens and matches them against the
// provided commands map.
//
// Parse rules (spec §"Parsing rules"):
//  1. StripCode is called first to remove fenced ``` blocks and `inline code` spans.
//  2. strings.Fields splits the stripped body into whitespace-bounded tokens.
//  3. Each token is tested as a command candidate (single-segment, ^/[A-Za-z][A-Za-z0-9_-]*$).
//  4. "/help" is intercepted FIRST as a reserved built-in before the defined-command lookup.
//  5. Remaining candidates are looked up in commands (case-SENSITIVE — YAML key = exact match).
//  6. Distinct known commands are de-duplicated; >1 distinct → MultiError.
//
// Case-sensitivity decision: command keys are literal (exact YAML keys); /PATCH does not
// match the "patch" key. This is consistent with other config-key lookups in the bridge.
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

		// /help is a reserved built-in — intercept before the defined-command lookup.
		if name == "help" {
			result.HelpRequested = true
			continue
		}

		// Phase 102: /claude and /codex are reserved built-in agent-verb tokens on a
		// separate axis from template commands. They compose freely with any /command
		// (e.g. "/codex /patch fix X") and are intercepted before the command map.
		// Dedup: same verb twice is fine; two DISTINCT verbs = AgentVerbConflict.
		if name == "claude" || name == "codex" {
			if result.AgentVerb == "" || result.AgentVerb == name {
				result.AgentVerb = name // dedup: same verb twice is fine
			} else {
				result.AgentVerbConflict = true // /claude AND /codex = conflict
			}
			continue
		}

		// Look up in the defined commands map (case-sensitive).
		if _, defined := commands[name]; defined {
			seenKnown[name] = true
		}
		// Unknown tokens are silently ignored (D6: lenient — unknown = plain text).
	}

	// Collect de-duplicated known command names in sorted order (deterministic output).
	for name := range seenKnown {
		result.Known = append(result.Known, name)
	}
	sort.Strings(result.Known)

	result.MultiError = len(result.Known) > 1

	// Phase 102: when both /claude and /codex appear (conflict), neither agent verb
	// takes effect — clear AgentVerb so callers only see one non-empty value at a time.
	if result.AgentVerbConflict {
		result.AgentVerb = ""
	}

	return result
}

// ExtractArgs extracts the "args" portion of a comment body by removing:
//  1. The FIRST occurrence of "@{botLogin}" (case-insensitive).
//  2. The FIRST occurrence of "/{commandToken}" (exact, case-sensitive; only when commandToken != "").
//
// Removal is by position (not strings.ReplaceAll) so that the command name appearing
// in prose text is only stripped once. The result is whitespace-normalized via
// strings.Fields join (collapsing repeated spaces/tabs/newlines into single spaces).
//
// When commandToken == "" (the default-command path), only the mention is stripped.
//
// Phase 102: delegates to ExtractArgsWithAgent with agentVerbToken="" so all stripping
// logic lives in one place.
func ExtractArgs(body, botLogin, commandToken string) string {
	return ExtractArgsWithAgent(body, botLogin, commandToken, "")
}

func isWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// ExtractArgsWithAgent is the Phase 102 extension of ExtractArgs. It strips:
//  1. The FIRST occurrence of "@{botLogin}" (case-insensitive) — same as ExtractArgs.
//  2. The FIRST occurrence of "/{commandToken}" — same as ExtractArgs (when non-"").
//  3. The FIRST occurrence of "/{agentVerbToken}" — Phase 102 addition (when non-"").
//
// The agent verb token (/claude or /codex) MUST be stripped from the args so the
// sandbox agent never sees it as literal prompt text (Pitfall 4 / PLAN 102-01).
//
// When agentVerbToken == "" (no agent verb present), behavior is identical to ExtractArgs.
func ExtractArgsWithAgent(body, botLogin, commandToken, agentVerbToken string) string {
	// Build the mention token to strip (case-insensitive search).
	mention := "@" + botLogin
	mentionLower := strings.ToLower(mention)

	lowerBody := strings.ToLower(body)

	// Find and remove the first occurrence of the mention (case-insensitive by position).
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

	// Find and remove the first occurrence of "/{commandToken}" (exact, case-sensitive).
	if commandToken != "" {
		cmdTok := "/" + commandToken
		cmdIdx := strings.Index(work, cmdTok)
		if cmdIdx >= 0 {
			end := cmdIdx + len(cmdTok)
			work = work[:cmdIdx] + work[end:]
		}
	}

	// Phase 102: Find and remove the first occurrence of "/{agentVerbToken}".
	if agentVerbToken != "" {
		agentTok := "/" + agentVerbToken
		agentIdx := strings.Index(work, agentTok)
		if agentIdx >= 0 {
			end := agentIdx + len(agentTok)
			work = work[:agentIdx] + work[end:]
		}
	}

	// Whitespace-normalize: split on any whitespace and rejoin with single spaces.
	return strings.Join(strings.Fields(work), " ")
}

// ExpandTemplate replaces "{{args}}" in the template with args. When the template
// does not contain "{{args}}", args are appended on a new line (unless args is empty).
//
// Multiple {{args}} placeholders are all replaced (strings.ReplaceAll).
// text/template is intentionally NOT used — there is only one variable and the
// simple approach has no ambiguity risk.
func ExpandTemplate(template, args string) string {
	const placeholder = "{{args}}"
	if strings.Contains(template, placeholder) {
		return strings.ReplaceAll(template, placeholder, args)
	}
	// No placeholder: append args on a new line (only when args is non-empty).
	if args == "" {
		return template
	}
	return template + "\n" + args
}

// EffectiveDefault resolves the effective default command name for a repo.
// Per-repo default (repoDefault) takes precedence over install-wide (installDefault).
// Returns "" when both are empty (free-form passthrough signal).
func EffectiveDefault(repoDefault, installDefault string) string {
	if repoDefault != "" {
		return repoDefault
	}
	return installDefault
}

// ResolveCommandRouting applies the command-overrides-repo routing rules:
//
//	alias   = command.alias || repo.alias
//	profile = command.profile || repo.profile || default_profile
func ResolveCommandRouting(cmdAlias, cmdProfile, repoAlias, repoProfile, defaultProfile string) (alias, profile string) {
	alias = cmdAlias
	if alias == "" {
		alias = repoAlias
	}

	profile = cmdProfile
	if profile == "" {
		profile = repoProfile
	}
	if profile == "" {
		profile = defaultProfile
	}

	return alias, profile
}

// CommandAllowed implements the inner allow gate (command.allow).
// Returns true when cmdAllow is empty/nil (no inner restriction) or when
// sender is in cmdAllow (case-insensitive, mirrors isInAllowlist convention).
//
// The outer repo allow gate (repo.allow) is the caller's responsibility — this
// function models only the inner command-level gate applied AFTER the repo gate passes.
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

// buildHelpReply constructs a /help reply listing all defined commands and
// the effective default for the current repo.
func buildHelpReply(commands map[string]CommandEntry, effectiveDefaultCmd string) string {
	var b strings.Builder
	b.WriteString("**Available commands:**\n\n")

	// List commands in sorted order for deterministic output.
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
// The handler calls this after the repo-allow gate and before envelope construction.
// Based on the result, the handler either posts a reply, denies, or dispatches.
//
// Parameters:
//   - fullBody: the original comment body (before any stripping)
//   - commands: the configured command map (nil/empty → dormant, passthrough)
//   - installDefaultCmd: install-wide default command name (may be "")
//   - repoDefaultCmd: per-repo default command name (may be "", overrides install-wide)
//   - sender: the GitHub login of the comment author
//   - repoAlias: the alias resolved by Resolve() for the matched repo entry
//   - repoProfile: the profile resolved by Resolve() for the matched repo entry
//   - defaultProfile: the top-level default_profile fallback
//   - botLogin: the bot's GitHub login (for mention stripping in ExtractArgs)
func RunCommandPass(
	fullBody string,
	commands map[string]CommandEntry,
	installDefaultCmd, repoDefaultCmd string,
	sender, repoAlias, repoProfile, defaultProfile string,
	botLogin string,
) CommandPassResult {
	// Determine the effective default command for this repo.
	effectiveDefaultCmd := EffectiveDefault(repoDefaultCmd, installDefaultCmd)

	// Parse the comment body for command tokens.
	parsed := ParseCommands(fullBody, commands)

	// /help is intercepted first (before auth, before multi-error).
	if parsed.HelpRequested {
		return CommandPassResult{
			Action:    CommandActionReply,
			ReplyText: buildHelpReply(commands, effectiveDefaultCmd),
		}
	}

	// Multi-command error: >1 distinct known commands.
	if parsed.MultiError {
		names := strings.Join(parsed.Known, ", /")
		return CommandPassResult{
			Action:    CommandActionReply,
			ReplyText: fmt.Sprintf("Multiple commands found: `/%s`. Please use only one command per comment.", names),
		}
	}

	// Resolve the command to dispatch (explicit or via default).
	var commandName string
	if len(parsed.Known) == 1 {
		commandName = parsed.Known[0]
	} else if effectiveDefaultCmd != "" {
		// No explicit command; use the effective default.
		commandName = effectiveDefaultCmd
	}

	if commandName == "" {
		// No command and no default → free-form passthrough.
		return CommandPassResult{Action: CommandActionPassthrough}
	}

	// Look up the resolved command entry.
	entry, ok := commands[commandName]
	if !ok {
		// The default_command names a non-existent command (misconfiguration).
		// Treat as passthrough — don't spam an error reply for operator misconfiguration.
		return CommandPassResult{Action: CommandActionPassthrough}
	}

	// Inner allow gate (command.allow narrows repo.allow).
	if !CommandAllowed(sender, entry.Allow) {
		return CommandPassResult{
			Action:    CommandActionDeny,
			ReplyText: fmt.Sprintf("You are not authorized to use the `/%s` command.", commandName),
		}
	}

	// Extract args: strip mention, command token, and agent verb token from the full body.
	// Phase 102: agent verb (/claude or /codex) must also be stripped so the agent
	// never sees it as literal prompt text (Pitfall 4).
	var cmdToken string
	if len(parsed.Known) == 1 {
		// Explicit command — strip the actual command token.
		cmdToken = commandName
	}
	// For default-command path (no explicit command token in body), cmdToken remains "".
	args := ExtractArgsWithAgent(fullBody, botLogin, cmdToken, parsed.AgentVerb)

	// Expand the prompt template.
	prompt := ExpandTemplate(entry.Prompt, args)

	// Resolve alias and profile (command overrides repo).
	alias, profile := ResolveCommandRouting(entry.Alias, entry.Profile, repoAlias, repoProfile, defaultProfile)

	return CommandPassResult{
		Action:  CommandActionDispatch,
		Alias:   alias,
		Profile: profile,
		Prompt:  prompt,
	}
}
