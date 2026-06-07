# Phase 99: GitHub bridge commands — Research

**Researched:** 2026-06-07
**Domain:** GitHub bridge extension — command dispatch layer on top of Phase 97/98 resolve pipeline
**Confidence:** HIGH (all integration points verified from live source code)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- **D1** — Prompt template + routing override only. No guardrails.
- **D2** — Command overrides repo. Repo is the fallback + auth gate. `alias = command.alias || repo.alias`. `profile = command.profile || repo.profile || default_profile`.
- **D3** — Command located anywhere in the comment (not anchored after mention).
- **D4** — Inline text OR `@<path>` file reference. `@file` read at `km init` time on operator workstation; inlined into published JSON. Bridge Lambda never reads FS. Path resolved relative to `km-config.yaml` directory. Missing = hard `km init` error + `km doctor` check.
- **D5** — Multiple commands in one comment = hard error reply, no dispatch. Repeats of same command deduped + allowed.
- **D6** — Lenient: unknown `/token` is plain text → free-form dispatch (or via effective default). No unknown-command-help-spam reply.
- **D7** — Per-command allowlist via intersection/narrowing. `repo.allow` = outer gate (silent drop). `command.allow` = inner gate (polite reply when sender passes repo.allow but fails command.allow).
- **D8** — SSM `{prefix}/config/github/commands` — single JSON doc, NOT a Lambda env var (avoids 4 KB env ceiling). Bridge reads at cold start.
- **D9** — Phase 99, separate from Phase 98.
- **D10** — Configurable `default_command`. `github.repos[].default_command` overrides `github.default_command`. Unset = free-form passthrough. Both values must name a defined command.

### Claude's Discretion
- Exact Go file/function layout within `pkg/github/bridge/` and the config package.
- Test file organization (table-driven, in the style of existing `Resolve()` tests).
- Wording/formatting details of the `/help` and error replies beyond the spec examples.
- `km doctor` check grouping/ordering within the existing GitHub doctor group.

### Deferred Ideas (OUT OF SCOPE)
- Per-command guardrails (tools / budget / model / agent selection).
- Named-environment indirection (`environments:` block) and per-repo-per-command box derivation.
- Template variables beyond `{{args}}`.
- Per-repo command definitions (command set is install-global for v1).
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| GH-CMD-CONFIG | `GithubConfig.Commands map[string]GithubCommandEntry` + `GithubConfig.DefaultCommand` + `GithubRepoEntry.DefaultCommand` + getters + v2→v merge-list entries | Config struct at lines 107–117 of `config.go`; merge-list at lines 434–485; `UnmarshalKey("github", ...)` at lines 606–609 |
| GH-CMD-FILEREF | `@file` prompts resolved at `km init` time (operator side), inlined into SSM JSON; missing = hard error | `resolvePrompts()` in `create_prompt.go` lines 82–102; same pattern applies to command prompt resolution at init |
| GH-CMD-SSM | `{prefix}/config/github/commands` SSM param written by `km init`, read by bridge at cold start | `putSSMParam()` at `configure_github.go:357`; SSM cold-start pattern at `main.go:90–95`; `SSMSecretFetcher` / `SSMBotLoginFetcher` in `aws_adapters.go` |
| GH-CMD-PARSE | Code-strip + token scan + match → 0/1/>1 known commands + `{{args}}` extraction + template expansion — pure functions in `pkg/github/bridge/` | New file; mirrors `resolve.go` pure-function style; no AWS dependencies |
| GH-CMD-ROUTE | Command pass slots in after Step 6 (repo-allow gate) and before Steps 8–9 (alias resolve + dispatch); produces new `{alias, profile, prompt}` tuple consumed by Phase 98 dispatch unchanged | `webhook_handler.go` lines 214–226 (Steps 6–7); dispatch call sites lines 265–388 |
| GH-CMD-AUTH | Intersection auth: repo.allow outer gate (existing, silent drop), command.allow inner gate (polite reply); plus built-in `/help` special-casing | `isInAllowlist()` at `webhook_handler.go:426`; new reply path via `Reactor.AddReaction` + comment post using installation token |
| GH-CMD-HELP | Built-in `/help` recognized before the auth narrow gate; replies with command list + effective default per repo; uses same installation token + comment API as 👀 ACK | `InstallationReactor.AddReaction()` pattern in `aws_adapters.go:823`; new `CommentPoster` interface needed |
| GH-CMD-E2E | E2E: operator configures commands + `@file` prompt + default_command; posts a PR comment with `/cmd` token; bridge dispatches to correct alias/profile with expanded template | Deploy path confirmed: `make build-lambdas` + `km init --dry-run=false` only |
</phase_requirements>

---

## Summary

Phase 99 layers a **command dispatch pass** on top of the Phase 97/98 GitHub bridge resolve pipeline. The Phase 98 bridge (`pkg/github/bridge/webhook_handler.go`) already handles HMAC verify → bot-loop guard → PR-only filter → thread-bypass → mention check → repo Resolve() → repo-allow gate → delivery dedupe → warm/resume/cold dispatch → 👀 ACK. Phase 99 inserts a new **command pass** between the repo-allow gate (Step 6, line ~226) and the prompt/envelope construction (line ~244), and adds a new reply path (installation token POST comment) for multi-command errors and command-not-authorized responses.

The config plumbing follows the exact three-edit rule documented in project memory `project_config_key_merge_list`: (1) struct field in `GithubConfig` + `GithubRepoEntry`, (2) population line in `Load()` cfg construction, and (3) merge-list entry in the v2→v loop. The `github` block already has one merge-list entry (line 483 of `config.go`) using `UnmarshalKey` — new sub-fields added to `GithubConfig` will be picked up by the same `UnmarshalKey("github", &cfg.Github)` call at line 607, so no NEW merge-list entry is required for nested fields; only `GithubRepoEntry.DefaultCommand` (a new struct field in an existing struct) + `GithubConfig.Commands` and `GithubConfig.DefaultCommand` (new fields on existing struct) need struct additions.

The SSM pattern for `{prefix}/config/github/commands` mirrors exactly the existing `webhook-secret`, `bot-login`, and `bridge-url` SSM keys: operator-side `putSSMParam()` during `km init`, bridge reads via `ssm.GetParameter` at cold-start, 15-minute `cachedValue` wrapper. The bridge Lambda's `init()` in `cmd/km-github-bridge/main.go` is the single cold-start wiring point.

**Primary recommendation:** Add `CommandsConfig` (the SSM-fetched command map) as a new field on `WebhookHandler`, fetch it in `main.go init()` via a new `SSMCommandsFetcher` mirroring `SSMSecretFetcher`, and slot the command pass into `Handle()` at line 244 before `promptBody := ExtractMentionBody(...)`.

---

## Standard Stack

### Core (all existing — no new libraries required)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `pkg/github/bridge` | local | Bridge interfaces, resolve, webhook handler | Extension target |
| `internal/app/config` | local | `GithubConfig`, `GithubRepoEntry`, `Load()`, merge-list | Existing config plumbing |
| `internal/app/cmd` | local | `km init`, `km doctor`, `km github status` | Existing CLI surface |
| `aws-sdk-go-v2/service/ssm` | existing | SSM GetParameter/PutParameter | Same as `SSMSecretFetcher` |
| `encoding/json` | stdlib | SSM JSON doc marshal/unmarshal | Already used throughout bridge |
| `strings`, `regexp` | stdlib | Token scanning, code-strip | Pure Go, no dependencies |

### No New Dependencies

Phase 99 requires zero new Go module dependencies. All AWS SDK clients already exist in the bridge Lambda's `init()`.

---

## Architecture Patterns

### Recommended Project Structure

New files go into existing packages — no new packages:

```
pkg/github/bridge/
├── resolve.go                   # existing — Resolve(), ExtractMentionBody()
├── commands.go                  # NEW — CommandEntry, ParseCommands(), StripCode(),
│                                #        ExtractArgs(), ExpandTemplate()
├── commands_test.go             # NEW — pure-function table tests
├── webhook_handler.go           # MODIFIED — add Commands field; slot command pass
├── aws_adapters.go              # MODIFIED — add SSMCommandsFetcher
├── interfaces.go                # MODIFIED — add CommandsFetcher, CommentPoster interfaces
│
internal/app/config/
├── config.go                   # MODIFIED — GithubCommandEntry struct,
│                                #            GithubConfig.Commands + DefaultCommand,
│                                #            GithubRepoEntry.DefaultCommand
│
internal/app/cmd/
├── init.go                     # MODIFIED — resolve @file, write SSM commands param,
│                                #            drift WARN, call PreStageGitHubProfiles
├── doctor.go                   # MODIFIED — new doctor checks in GitHub group
├── github.go                   # MODIFIED — km github status adds command listing
│
cmd/km-github-bridge/
└── main.go                     # MODIFIED — wire SSMCommandsFetcher at cold start
```

### Pattern 1: SSMCommandsFetcher (mirrors SSMSecretFetcher)

**What:** Cached SSM fetcher for the `{prefix}/config/github/commands` JSON doc. Returns a `map[string]CommandEntry` (parsed at fetch time, not per-invocation).

**When to use:** Cold start in `main.go init()`, assigned to `WebhookHandler.Commands`.

```go
// Source: pkg/github/bridge/aws_adapters.go (mirrors SSMSecretFetcher pattern)
type SSMCommandsFetcher struct {
    Client   SecretSSMClient
    Path     string        // e.g. "/{prefix}/config/github/commands"
    CacheTTL time.Duration // defaults to 15 minutes

    mu    sync.Mutex
    cache struct {
        commands map[string]CommandEntry
        expiry   time.Time
    }
}

func (f *SSMCommandsFetcher) Fetch(ctx context.Context) (map[string]CommandEntry, error) {
    // lock, check cache, GetParameter with WithDecryption=false (plain String),
    // json.Unmarshal into map[string]CommandEntry, cache + return
}
```

### Pattern 2: CommandEntry struct in config package

**What:** New `GithubCommandEntry` in `internal/app/config/config.go` alongside `GithubRepoEntry`. New fields on `GithubConfig` and `GithubRepoEntry`.

```go
// Source: internal/app/config/config.go (after GithubRepoEntry, ~line 99)
type GithubCommandEntry struct {
    Description string   `mapstructure:"description" yaml:"description,omitempty" json:"description,omitempty"`
    Alias       string   `mapstructure:"alias"       yaml:"alias,omitempty"       json:"alias,omitempty"`
    Profile     string   `mapstructure:"profile"     yaml:"profile,omitempty"     json:"profile,omitempty"`
    Allow       []string `mapstructure:"allow"       yaml:"allow,omitempty"       json:"allow,omitempty"`
    Prompt      string   `mapstructure:"prompt"      yaml:"prompt"               json:"prompt"`
}

// GithubConfig (existing struct ~line 107) gains two new fields:
//   Commands       map[string]GithubCommandEntry
//   DefaultCommand string

// GithubRepoEntry (existing struct ~line 83) gains one new field:
//   DefaultCommand string
```

**CRITICAL: No new merge-list entry required.** The merge-list entry `"github"` at line 483 of `config.go` is the single entry; the existing `v.UnmarshalKey("github", &cfg.Github)` at line 607 decodes the entire `github:` block including new fields. This is the same mechanism used by `Repos []GithubRepoEntry` today.

### Pattern 3: Command pass slot in webhook_handler.go

**What:** New block between the repo-allow gate (line ~226) and the prompt/envelope construction (line ~244). The pass reads `h.Commands`, parses the comment body, and either replies or produces a modified `{alias, profile, promptBody}`.

**Current code at the insertion point:**

```go
// webhook_handler.go lines 243–257 (Phase 98 current code)
promptBody := ExtractMentionBody(payload.Comment.Body, botLogin)

env := GitHubEnvelope{
    Source:    "github",
    ...
    Body:      promptBody,
    ...
}
```

**Phase 99 inserts BEFORE line 244:**

```go
// Command pass — slots between repo-allow gate and envelope construction.
// When h.Commands is nil or empty, this block is skipped (dormant-by-default).
if len(h.Commands) > 0 || h.DefaultCommand != "" {
    mentionBody := ExtractMentionBody(payload.Comment.Body, botLogin)
    result, cmdErr := RunCommandPass(ctx, mentionBody, h.Commands, h.DefaultCommand, repoDefaultCommand, payload.Comment.User.Login)
    switch result.Action {
    case CommandActionReply:
        // multi-command error or /help — post comment, return 200
    case CommandActionDeny:
        // command allow narrowing failed — post "not authorized" comment, return 200
    case CommandActionDispatch:
        // override alias/profile/prompt, fall through to envelope construction
        alias = result.Alias
        profile = result.Profile
        promptBody = result.Prompt
    case CommandActionPassthrough:
        // no command (and no default), use free-form body
        promptBody = mentionBody
    }
}
```

`h.Commands` is `map[string]CommandEntry` fetched from SSM at cold start (nil when unconfigured → dormant). `h.DefaultCommand` is the install-wide default command name.

### Pattern 4: CommentPoster interface + reply path

**What:** New interface in `interfaces.go` for posting a reply comment (distinct from the existing `GitHubReactor` which only posts reactions). Used by the command pass for multi-command errors, not-authorized, and `/help`.

```go
// New interface in pkg/github/bridge/interfaces.go
type CommentPoster interface {
    // PostComment posts a text comment on issue/PR number in the given repo.
    // Uses the installation token (same mint-JWT → exchange flow as GitHubReactor).
    PostComment(ctx context.Context, installationID, owner, repo string, issueNumber int, body string) error
}
```

The concrete implementation mirrors `InstallationReactor.AddReaction()` in `aws_adapters.go`: mint App JWT via `pkggithub.GenerateGitHubAppJWT`, exchange for installation token via `pkggithub.ExchangeForInstallationToken`, POST to `/repos/{owner}/{repo}/issues/{number}/comments`.

### Anti-Patterns to Avoid

- **Adding `github.commands` as a separate merge-list entry:** Unnecessary — the `"github"` entry at line 483 already covers the entire `github:` block via `UnmarshalKey`. Adding a second entry would be a no-op or break parsing.
- **Publishing commands as an env var:** Explicitly rejected (D8) — templates can be several KB, far exceeding the 4 KB Lambda env total. SSM is the correct path.
- **Lambda FS read of `@file`:** Explicitly rejected (D4) — Lambda has no operator filesystem access. Resolution must happen at `km init` time.
- **Modifying the warm/resume/cold dispatch logic:** The command pass only changes `{alias, profile, prompt}`. Steps 8–10 (`webhook_handler.go` lines 265–388) are unchanged.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Token scanning with regex | Custom lexer | `strings.Fields` + prefix check + embedded-slash filter | Regex is over-engineered for single-segment pattern `^/[A-Za-z][A-Za-z0-9_-]*$` |
| Code-block stripping | Custom parser | Simple state machine: scan for ` ``` ` and `` ` `` markers | CommonMark parsing is far too heavy; spec only needs to skip false positives |
| Template expansion | text/template | Simple `strings.ReplaceAll(template, "{{args}}", args)` | Only one variable; `text/template` adds complexity with no benefit |
| SSM caching | Custom cache library | Copy `cachedValue` struct from `aws_adapters.go` | Already in the codebase; identical pattern |
| Installation token minting | Separate auth package | Copy `pkggithub.GenerateGitHubAppJWT` + `ExchangeForInstallationToken` call pattern from `InstallationReactor.AddReaction()` | Already exists at `aws_adapters.go:823` |

---

## Common Pitfalls

### Pitfall 1: Forgetting to add struct fields to the `UnmarshalKey` source struct
**What goes wrong:** `GithubCommandEntry` added to `GithubConfig.Commands` but not declared with `mapstructure` tags — `UnmarshalKey` silently ignores the field.
**Why it happens:** `mapstructure` requires struct tags matching the YAML key names; Go JSON tags alone are not enough for viper's UnmarshalKey.
**How to avoid:** Every field needs `mapstructure:"<yaml_key>" yaml:"<yaml_key>" json:"<json_key>"` tags.
**Warning signs:** `km init` shows no error but `km github status` shows empty command list.

### Pitfall 2: SSM param present even when commands is empty
**What goes wrong:** `km init` writes the SSM `commands` param even when `github.commands` is absent, making `km doctor` report a stale param when commands are later removed.
**How to avoid:** Gate the SSM write on `len(cfg.Github.Commands) > 0`, identical to the `KM_GITHUB_REPOS` gate at `init.go:1074`.

### Pitfall 3: Command pass active when commands SSM param is nil
**What goes wrong:** Bridge tries to read `h.Commands` but the SSM param is absent (first deploy, unconfigured) — panics or returns garbage.
**How to avoid:** `SSMCommandsFetcher.Fetch()` returns an empty map (not nil) when the SSM param is absent (ParameterNotFound = dormant). `len(h.Commands) > 0` gate in `Handle()` is the dormant guard.

### Pitfall 4: `/help` counted as a known command during token scan
**What goes wrong:** `/help` is typed by a user; the parser scans for it in the defined commands map and finds nothing (it's reserved, not definable); falls through to free-form or default. Instead, `/help` must be intercepted BEFORE the match-against-defined-commands step.
**How to avoid:** Check for `/help` token FIRST in the scanner, before looking up defined commands.

### Pitfall 5: `{{args}}` extraction strips too much or too little
**What goes wrong:** Stripping the `@mention` AND the `/command` token by naive `strings.Replace` removes all occurrences. If the command name appears in prose, it gets incorrectly stripped.
**How to avoid:** Strip the FIRST occurrence of the `@mention` token and the FIRST occurrence of the `/command` token only (by position, not all-occurrences replace).

### Pitfall 6: `@file` path resolution relative to CWD, not km-config.yaml dir
**What goes wrong:** Operator runs `km init` from a different directory; `@prompts/gh-review.txt` resolves relative to CWD, not to the km-config.yaml location.
**How to avoid:** The `@file` resolver must use `filepath.Dir(km-config.yaml path)` as the base, not `os.Getwd()`. Check how viper resolves `v2.ConfigFileUsed()` to get the km-config.yaml dir.

### Pitfall 7: `make build-lambdas` rebuilds from hardcoded list
**What goes wrong:** `km-github-bridge` zip is already in the `lambdaBuilds()` list — this is NOT a new Lambda, so no list changes needed. But the bridge zip must be rebuilt to pick up the new code.
**How to avoid:** Run `make build-lambdas` (not just `make build`) before `km init --dry-run=false`. The existing list entry at `init.go:1876` already covers km-github-bridge.

---

## Code Examples

### resolve.go — ExtractMentionBody (exact current logic, Phase 99 builds on top)

```go
// Source: pkg/github/bridge/resolve.go lines 104–113
func ExtractMentionBody(body, botLogin string) string {
    lower := strings.ToLower(body)
    mention := "@" + strings.ToLower(botLogin)
    idx := strings.Index(lower, mention)
    if idx == -1 {
        return strings.TrimSpace(body)
    }
    after := body[idx+len(mention):]
    return strings.TrimSpace(after)
}
```

The Phase 99 `{{args}}` extraction needs the FULL body (for `/command` stripping), not just the post-mention text. `ExtractMentionBody` returns only the post-mention text; the command parser needs to work on the full body BEFORE this call, or separately track both the mention position and the command token position.

**Recommended approach:** Phase 99 introduces `ExtractArgs(body, botLogin, commandToken string) string` that strips both the `@mention` and `/commandToken` in a single pass.

### webhook_handler.go — exact current dispatch tuple (UNCHANGED by Phase 99)

```go
// Source: pkg/github/bridge/webhook_handler.go lines 243–257
// Phase 99: command pass modifies alias, profile, promptBody BEFORE this block.
promptBody := ExtractMentionBody(payload.Comment.Body, botLogin)

env := GitHubEnvelope{
    Source:        "github",
    Repo:          payload.Repository.FullName,
    Number:        payload.Issue.Number,
    Kind:          "issue_comment",
    CommentID:     payload.Comment.ID,
    HTMLURL:       payload.Comment.HTMLURL,
    Sender:        payload.Comment.User.Login,
    Body:          promptBody,              // ← Phase 99 sets this before line 244
    InstallID:     InstallIDString(payload.Installation.ID),
    DefaultBranch: payload.Repository.DefaultBranch,
}
```

### config.go — exact merge-list and UnmarshalKey (Phase 97/98 current, no change needed)

```go
// Source: internal/app/config/config.go lines 434–485 (v2→v merge loop)
// The single "github" entry covers the ENTIRE github: block including new fields.
// DO NOT add "github.commands" or "github.default_command" as separate entries.
for _, key := range []string{
    ...
    "github",   // line 483 — covers Repos, DefaultProfile, Commands, DefaultCommand
} {
    if v2.IsSet(key) && ... {
        v.Set(key, v2.Get(key))
    }
}

// Source: internal/app/config/config.go lines 606–609
// UnmarshalKey decodes the entire github: block.
if err := v.UnmarshalKey("github", &cfg.Github); err != nil {
    return nil, fmt.Errorf("unmarshal github: %w", err)
}
```

### init.go — KM_GITHUB_REPOS drift WARN pattern (commands SSM write mirrors this)

```go
// Source: internal/app/cmd/init.go lines 1066–1092
// Commands SSM write should mirror this gate + drift WARN pattern:
if len(cfg.Github.Repos) > 0 {
    // ... marshal to JSON ...
    if envVal := os.Getenv("KM_GITHUB_REPOS"); envVal != "" && envVal != yamlGithubRepos {
        fmt.Fprintf(os.Stderr, "WARN: KM_GITHUB_REPOS=%s (env) overrides ...\n", ...)
    } else if envVal == "" {
        os.Setenv("KM_GITHUB_REPOS", yamlGithubRepos)
    }
}
// For commands: gate on len(cfg.Github.Commands) > 0; write to SSM (not env var).
// The drift WARN for SSM: compare current SSM value with assembled JSON before writing.
```

### resolvePrompts — @file resolution pattern (commands prompt resolution mirrors exactly)

```go
// Source: internal/app/cmd/create_prompt.go lines 82–102
func resolvePrompts(raw []string) ([]string, error) {
    out := make([]string, len(raw))
    for i, v := range raw {
        switch {
        case strings.HasPrefix(v, "@@"):
            out[i] = v[1:]          // @@ escape
        case strings.HasPrefix(v, "@"):
            path := v[1:]           // @file: read from filesystem
            data, err := os.ReadFile(path)
            if err != nil {
                return nil, fmt.Errorf("--prompt @%s: %w", path, err)
            }
            out[i] = string(data)
        default:
            out[i] = v
        }
    }
    return out, nil
}
// Phase 99: same logic for command prompt fields.
// Path resolved relative to filepath.Dir(v2.ConfigFileUsed()) not os.Getwd().
// Missing file = hard error (not a soft warning).
```

### putSSMParam — SSM write helper (commands SSM write uses this directly)

```go
// Source: internal/app/cmd/configure_github.go lines 355–372
func putSSMParam(ctx context.Context, client SSMWriteAPI, name, value string,
    paramType ssmtypes.ParameterType, kmsKeyID string, overwrite bool) error {
    input := &ssm.PutParameterInput{
        Name:      aws.String(name),
        Value:     aws.String(value),
        Type:      paramType,
        Overwrite: aws.Bool(overwrite),
    }
    if kmsKeyID != "" { input.KeyId = aws.String(kmsKeyID) }
    _, err := client.PutParameter(ctx, input)
    return err
}
// Usage for commands: putSSMParam(ctx, ssmClient,
//     cfg.GetSsmPrefix()+"config/github/commands",
//     string(commandsJSON),
//     ssmtypes.ParameterTypeString, "", true)
```

### doctor.go — check registration pattern (new checks append to existing GitHub group)

```go
// Source: internal/app/cmd/doctor.go lines 3285–3297
// New checks append in the same closure style:
checks = append(checks, func(_ context.Context) CheckResult {
    return checkGitHubReposResolvable(githubRepos, githubDefaultProfile)
})
checks = append(checks, func(_ context.Context) CheckResult {
    return checkGitHubAliasCollision(githubRepos)
})
// Phase 99 adds after checkGitHubAliasCollision:
checks = append(checks, func(_ context.Context) CheckResult {
    return checkGitHubCommandsValid(cfg.Github.Commands, cfg.Github.DefaultCommand, githubRepos)
})
checks = append(checks, func(ctx context.Context) CheckResult {
    return checkGitHubCommandsSSMParam(ctx, ssmClient, cfg.GetSsmPrefix(), cfg.Github.Commands)
})
```

### km github status — current output (Phase 99 extends)

```go
// Source: internal/app/cmd/github.go lines 279–300 (RunGitHubStatus)
// Current output:
fmt.Fprintf(out, "GitHub bridge config (prefix: %s):\n", cfg.GetSsmPrefix())
fmt.Fprintf(out, "  webhook-secret:  %s\n", secretDisplay)
fmt.Fprintf(out, "  bot-login:       %s\n", botLogin)
fmt.Fprintf(out, "  bridge-url:      %s\n", bridgeURL)
fmt.Fprintf(out, "  app-client-id:   %s\n", appClientID)
fmt.Fprintf(out, "  installation-id: %s\n", installID)
// Phase 99 adds AFTER the existing output:
// - Read SSM commands param, parse + display:
//   "  commands (%d):  ...\n"
//   "    /review — read-only review, inline findings [→ gh-myorg]\n"
//   "    /patch  — apply the smallest fix [→ gh-myorg-dev]\n"
// - Per-repo effective default:
//   "  default_command: review (install-wide)\n"
//   "  repos: ...\n"
//   "    myorg/*  default_command: explain (per-repo)\n"
```

### bridge main.go — cold-start wiring (commands SSM reader added here)

```go
// Source: cmd/km-github-bridge/main.go lines 72–203
// Phase 99 adds after botLoginFetcher wiring (~line 137):
commandsFetcher := &bridge.SSMCommandsFetcher{
    Client:   ssmClient,
    Path:     "/" + prefix + "/config/github/commands",
    CacheTTL: 15 * time.Minute,
}
// Eagerly read at cold start (like appClientID):
commands, _ := commandsFetcher.Fetch(ctx)  // empty map if param absent → dormant

// WebhookHandler gains new fields:
webhookHandler = &bridge.WebhookHandler{
    ...existing fields...
    Commands:          commands,            // map[string]CommandEntry, nil = dormant
    CommandsFetcher:   commandsFetcher,     // for cache refresh on warm invocations
    DefaultCommand:    defaultCommand,      // from KM_GITHUB_DEFAULT_COMMAND env
    Commenter:         commenter,           // CommentPoster for error/help replies
}
```

---

## State of the Art

| Old Approach | Current Approach | Impact |
|--------------|------------------|--------|
| Free-form comment body dispatched verbatim | Phase 97/98: `ExtractMentionBody()` strips `@mention` prefix | Phase 99 adds command layer on top; `ExtractMentionBody()` still used for free-form fallback |
| All github config as env vars | Phase 97: `KM_GITHUB_REPOS` as JSON env var | Phase 99: commands go to SSM (too large for env); env pattern is RETAINED for repos |
| No per-command routing | Phase 98: single alias per repo | Phase 99: command can override alias + profile per invocation |

---

## Open Questions

1. **`@file` path base directory** — `resolvePrompts` in `create_prompt.go` uses `os.ReadFile(path)` with no base dir, i.e. CWD-relative. For command prompts, path must be relative to `km-config.yaml`. The `v2` viper instance's `ConfigFileUsed()` method returns the km-config.yaml path. The `km init` code must use `filepath.Join(filepath.Dir(v2.ConfigFileUsed()), path[1:])` when resolving `@file` values. This needs confirmation that `v2.ConfigFileUsed()` is accessible from the init command context.

2. **`repoDefaultCommand` in Handle()** — The per-repo `DefaultCommand` is on `GithubRepoEntry` in config. In the bridge, `h.Entries []RepoEntry` uses `bridge.RepoEntry` (not `config.GithubRepoEntry`). `bridge.RepoEntry` needs a `DefaultCommand string` field too. This mirrors how `bridge.RepoEntry` already mirrors `config.GithubRepoEntry` (compare `resolve.go:15–31` vs `config.go:83–99`). Both structs must be extended.

3. **`CommandsFetcher` vs eager cold-start load** — Current pattern: `appClientID` and `privateKeyPEM` are read eagerly at cold start (line 142 of `main.go`), NOT via a `SecretFetcher` abstraction. `SSMSecretFetcher` is used for per-invocation cached fetches (webhook secret, bot-login). For commands, a hybrid approach is appropriate: fetch eagerly at cold start (fast, deterministic) + store in `WebhookHandler.Commands` (already-parsed map). A `CommandsFetcher` interface is still useful for testability.

---

## Validation Architecture

Nyquist_validation is enabled (`workflow.nyquist_validation: true` in `.planning/config.json`).

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing (`testing` stdlib) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./pkg/github/bridge/... ./internal/app/cmd/... -run TestGitHubCmd -v` |
| Full suite command | `go test ./pkg/github/bridge/... ./internal/app/config/... ./internal/app/cmd/... -v` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| GH-CMD-CONFIG | `GithubCommandEntry` marshals/unmarshals + `UnmarshalKey` round-trip | unit | `go test ./internal/app/config/... -run TestGithubConfig` | ❌ Wave 0 |
| GH-CMD-FILEREF | `@file` resolution: present, missing, `@@` escape | unit | `go test ./internal/app/cmd/... -run TestResolveCommandPrompts` | ❌ Wave 0 |
| GH-CMD-SSM | SSM param write (with drift WARN) + read round-trip | unit (fake SSM) | `go test ./internal/app/cmd/... -run TestInitGitHubCommands` | ❌ Wave 0 |
| GH-CMD-PARSE | StripCode + token scan (including `/usr/bin/patch`, code-fenced false positives, multi, dedup, unknown) | unit | `go test ./pkg/github/bridge/... -run TestCommandParse` | ❌ Wave 0 |
| GH-CMD-PARSE | `{{args}}` extraction (command anywhere, no command) | unit | `go test ./pkg/github/bridge/... -run TestExtractArgs` | ❌ Wave 0 |
| GH-CMD-PARSE | Template expansion (`{{args}}` present, absent) | unit | `go test ./pkg/github/bridge/... -run TestExpandTemplate` | ❌ Wave 0 |
| GH-CMD-PARSE | Effective-default resolution (per-repo wins, top-level fallback, unset→free-form) | unit | `go test ./pkg/github/bridge/... -run TestEffectiveDefault` | ❌ Wave 0 |
| GH-CMD-ROUTE | Resolution precedence: command.alias overrides repo.alias; command.profile overrides repo.profile | unit | `go test ./pkg/github/bridge/... -run TestCommandRouting` | ❌ Wave 0 |
| GH-CMD-AUTH | Auth intersection: in-both, fails-command, fails-repo | unit | `go test ./pkg/github/bridge/... -run TestCommandAuth` | ❌ Wave 0 |
| GH-CMD-AUTH | Handler: multi-command error reply path (mock Commenter) | unit (handler with fakes) | `go test ./pkg/github/bridge/... -run TestHandle_MultiCommand` | ❌ Wave 0 |
| GH-CMD-AUTH | Handler: command-not-authorized reply path | unit (handler with fakes) | `go test ./pkg/github/bridge/... -run TestHandle_CommandNotAuthorized` | ❌ Wave 0 |
| GH-CMD-HELP | Handler: `/help` reply lists commands + effective default | unit (handler with fakes) | `go test ./pkg/github/bridge/... -run TestHandle_Help` | ❌ Wave 0 |
| GH-CMD-HELP | Lenient-unknown: unknown `/token` dispatches free-form, no reply | unit (handler with fakes) | `go test ./pkg/github/bridge/... -run TestHandle_UnknownToken` | ❌ Wave 0 |
| GH-CMD-HELP | Default-command: command-less comment runs effective default | unit (handler with fakes) | `go test ./pkg/github/bridge/... -run TestHandle_DefaultCommand` | ❌ Wave 0 |
| GH-CMD-CONFIG | Doctor: `@file` missing WARN | unit (pure config check) | `go test ./internal/app/cmd/... -run TestDoctorGitHubCommandsAtFileMissing` | ❌ Wave 0 |
| GH-CMD-CONFIG | Doctor: profile unresolvable WARN | unit (pure config check) | `go test ./internal/app/cmd/... -run TestDoctorGitHubCommandsProfileUnresolvable` | ❌ Wave 0 |
| GH-CMD-CONFIG | Doctor: `help` shadow WARN | unit (pure config check) | `go test ./internal/app/cmd/... -run TestDoctorGitHubCommandsHelpShadow` | ❌ Wave 0 |
| GH-CMD-CONFIG | Doctor: alias-overlap (command alias ↔ repo alias) WARN | unit (pure config check) | `go test ./internal/app/cmd/... -run TestDoctorGitHubCommandsAliasOverlap` | ❌ Wave 0 |
| GH-CMD-CONFIG | Doctor: `default_command` references undefined command | unit (pure config check) | `go test ./internal/app/cmd/... -run TestDoctorGitHubCommandsDefaultUndefined` | ❌ Wave 0 |
| GH-CMD-CONFIG | Doctor: SSM commands param present when configured | unit (fake SSM) | `go test ./internal/app/cmd/... -run TestDoctorGitHubCommandsSSMParam` | ❌ Wave 0 |
| GH-CMD-E2E | Dormant when `github.commands` absent — Handle() byte-identical | unit (handler with fakes) | `go test ./pkg/github/bridge/... -run TestHandle_CommandsDormant` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/github/bridge/... ./internal/app/cmd/... -run TestGitHubCmd -count=1`
- **Per wave merge:** `go test ./pkg/github/bridge/... ./internal/app/config/... ./internal/app/cmd/... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

All test files are new — none exist yet:
- [ ] `pkg/github/bridge/commands_test.go` — covers GH-CMD-PARSE, GH-CMD-ROUTE, GH-CMD-AUTH, GH-CMD-HELP (pure-function + handler-with-fakes tests)
- [ ] `internal/app/config/config_github_commands_test.go` — covers GH-CMD-CONFIG struct round-trip
- [ ] `internal/app/cmd/doctor_github_commands_test.go` — covers all doctor check WARNs
- [ ] `internal/app/cmd/init_github_commands_test.go` — covers `@file` resolution + SSM write

Existing test helpers to reuse:
- `handle_test.go`: `mockSecretFetcher`, `mockBotLoginFetcher`, `mockNonceStore`, `mockResolver`, `mockPublisher`, `mockSQS`, `mockReactor` — all reusable for command pass tests
- `webhook_handler_phase98_test.go`: `mockGitHubThreadStore`, `buildPayloadJSON()`, `buildRequest()`, `defaultOpts()` — reusable
- `resolve_test.go`: table-driven style with `[]struct{name, ...want...}` — mirror this for all pure-function tests

---

## Deploy Surface Confirmation

**No new Lambda.** The km-github-bridge Lambda already exists from Phase 97 and is already in `lambdaBuilds()` list (`init.go:1876`). Phase 99 deploy is:

```bash
make build-lambdas     # rebuild bridge zip with new code
km init --dry-run=false  # update Lambda code (zip upload), NO new Terraform modules
```

**NOT required:**
- `km init --sidecars` — no SandboxProfile schema change
- `make build` (km operator binary) — no new `regionalModules()` entry (no new DDB table, no new Lambda)
- Sandbox recreate — no sandbox-side changes

**SSM write at `km init` time** (operator-side, NOT Lambda-side): `putSSMParam(ctx, ssmClient, prefix+"config/github/commands", commandsJSON, String, "", overwrite=true)`. This is a plain String (not SecureString) — commands are config, not secrets.

**Env-wins drift WARN:** After writing SSM, check if the SSM value differs from what was computed from yaml. If operator has a stale SSM param (e.g. from a previous `km init`), the drift WARN fires. Pattern: read current SSM value, compare with assembled JSON, print WARN if different, then write.

---

## Sources

### Primary (HIGH confidence)
- `pkg/github/bridge/resolve.go` — `Resolve()` signature, `ExtractMentionBody()` exact logic, `RepoEntry` struct
- `pkg/github/bridge/webhook_handler.go` — 11-step Handle() flow, exact insertion point for command pass (line 244), `isInAllowlist()`, dispatch call sites
- `pkg/github/bridge/aws_adapters.go` — `SSMSecretFetcher`/`SSMBotLoginFetcher` cache pattern, `InstallationReactor.AddReaction()` token mint flow
- `pkg/github/bridge/interfaces.go` — all interfaces, compile-time checks
- `internal/app/config/config.go` — `GithubConfig`, `GithubRepoEntry`, merge-list (lines 434–485), `UnmarshalKey` (lines 606–609)
- `internal/app/cmd/init.go` — `KM_GITHUB_REPOS` drift WARN (lines 1066–1092), `PreStageGitHubProfiles`, `lambdaBuilds()` list (line 1876)
- `internal/app/cmd/doctor.go` — GitHub check registration (lines 3197–3297), `DetectGitHubAliasIssues` (lines 1163–1226), check pattern
- `internal/app/cmd/github.go` — `RunGitHubStatus` exact output (lines 279–300), `putSSMParam` pattern
- `internal/app/cmd/create_prompt.go` — `resolvePrompts()` exact implementation (lines 82–102)
- `cmd/km-github-bridge/main.go` — Lambda cold-start wiring, env var list, `readAppCredentials()` pattern
- `pkg/github/bridge/resolve_test.go` — table-driven test style to mirror
- `pkg/github/bridge/handle_test.go` — mock patterns (mockSecretFetcher, mockResolver, etc.)
- `pkg/github/bridge/webhook_handler_phase98_test.go` — handler test helpers (buildPayloadJSON, defaultOpts)

### Secondary (MEDIUM confidence)
- `docs/superpowers/specs/2026-06-07-github-bridge-commands-design.md` — approved design spec (all decisions D1–D10 verified against code)
- `docs/github-bridge.md` — Phase 97/98 operator runbook (deploy sequence confirmed)

### Tertiary (LOW confidence — not needed; design is fully locked)
- None — all design decisions resolved before research; research was code-mapping only

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries and patterns verified from live source
- Architecture (insertion point, config plumbing): HIGH — exact line numbers cited
- SSM pattern: HIGH — mirrors existing SSMSecretFetcher exactly
- Test conventions: HIGH — existing test files read and style documented
- `@file` path base dir: MEDIUM — `v2.ConfigFileUsed()` path not verified from init.go call site (open question 1)

**Research date:** 2026-06-07
**Valid until:** 2026-07-07 (Phase 98 shipped 2026-06-07; codebase is stable)
