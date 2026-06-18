// Package config provides the central configuration struct for the km CLI.
// Configuration is loaded from ~/.km/config.yaml, environment variables (KM_ prefix),
// and CLI flags (highest precedence). A repo-root km-config.yaml is also loaded
// (merged via viper) to supply platform-level fields like Domain and AccountIDs.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// ClusterConfig represents a single registered Kubernetes cluster for cross-account
// IRSA (IAM Roles for Service Accounts) integration. Each entry maps a cluster's
// OIDC provider to a per-namespace/service-account IAM role in the application account.
// Populated from the km-config.yaml `clusters:` list (Plan 80).
// SlackConfig holds install-level Slack defaults that flow into the bridge
// Lambda environment via terragrunt.hcl get_env() calls. Phase 91.1 added
// MentionOnly so operators no longer need to `export KM_SLACK_MENTION_ONLY=true`
// before `km init` — set `slack.mention_only: true` in km-config.yaml instead.
type SlackConfig struct {
	// MentionOnly is the install-level default for the polite-bot @-mention
	// filter. Tri-state via *bool:
	//   nil    → key absent from yaml; bridge defaults to "false" (chatty)
	//   &true  → polite mode (bridge only acts on messages containing <@{bot_user_id}>)
	//   &false → chatty mode (bridge reacts to every message)
	// Maps to km-config.yaml key slack.mention_only. Exported as
	// KM_SLACK_MENTION_ONLY for infra/live/use1/lambda-slack-bridge/terragrunt.hcl
	// get_env() at terragrunt-apply time during `km init`.
	MentionOnly *bool `mapstructure:"mention_only" yaml:"mention_only,omitempty"`

	// ReactAlways is the install-level default for the Phase 91.4 first-only
	// reactor. Tri-state via *bool:
	//   nil    → key absent from yaml; bridge defaults to "true" (react on every dispatch)
	//   &true  → react on every dispatch (current chatty-reactor behaviour)
	//   &false → react ONLY on top-level engagement messages; thread replies
	//            dispatched via Phase 91.3 mention-bypass are silent
	// Maps to km-config.yaml key slack.react_always. Exported as
	// KM_SLACK_REACT_ALWAYS for the bridge Lambda environment block.
	ReactAlways *bool `mapstructure:"react_always" yaml:"react_always,omitempty"`

	// PeerBridges is the Phase 95 federated relay peer list — a []string of sibling
	// km install bridge /events URLs. Exported as KM_SLACK_PEER_BRIDGES (comma-joined)
	// by ExportTerragruntEnvVars for infra/live/use1/lambda-slack-bridge/terragrunt.hcl
	// get_env("KM_SLACK_PEER_BRIDGES", "").
	//   nil   → key absent from yaml; federation off (EventsHandler.Relayer stays nil)
	//   []URL → broadcast unknown-channel events to all peers (Plan 02 relay logic)
	// Uses []string directly — NOT the *bool tri-state. Empty explicitly-set slice
	// is also treated as "federation off" by the len>0 gate in ExportTerragruntEnvVars.
	// Maps to km-config.yaml key slack.peer_bridges (Phase 95).
	PeerBridges []string `mapstructure:"peer_bridges" yaml:"peer_bridges,omitempty"`

	// DefaultRouter is the Phase 96 front-door router toggle. Tri-state via *bool:
	//   nil    → key absent from yaml; router off (Phase 95 byte-identical)
	//   &true  → router on: when a message @-mentions the bot in an orphan channel
	//            (front-door FetchByChannel miss + zero peer claims), the bridge
	//            posts a threaded reply listing running sandbox channels.
	//   &false → router explicitly off (same as nil but operator-visible in yaml)
	// Meaningful only on the front-door install (the one that receives raw Slack
	// events). Setting it on a non-front-door peer is a no-op.
	// Maps to km-config.yaml key slack.default_router. Exported as
	// KM_SLACK_DEFAULT_ROUTER by ExportTerragruntEnvVars (Phase 96).
	DefaultRouter *bool `mapstructure:"default_router" yaml:"default_router,omitempty"`
}

type ClusterConfig struct {
	Name            string `mapstructure:"name"              yaml:"name"`
	OIDCProviderARN string `mapstructure:"oidc_provider_arn" yaml:"oidc_provider_arn"`
	Namespace       string `mapstructure:"namespace"         yaml:"namespace"`
	ServiceAccount  string `mapstructure:"service_account"   yaml:"service_account"`
	RoleARN         string `mapstructure:"role_arn"          yaml:"role_arn"`
}

// GithubRepoEntry maps a GitHub repository (by match pattern) to a km alias,
// a SandboxProfile, and an optional network allowlist. Used by the Phase 97
// bridge Lambda to resolve which sandbox to dispatch a PR comment to.
//
// Fields mirror the km-config.yaml github.repos list-of-objects shape.
// json tags match the yaml keys so KM_GITHUB_REPOS JSON is self-describing.
type GithubRepoEntry struct {
	// Match is the "owner/repo" string matched against the incoming webhook's
	// repository.full_name field. Exact match only (no glob in Wave 1).
	Match string `mapstructure:"match" yaml:"match" json:"match"`

	// Alias is the sandbox alias used when creating a cold sandbox for this repo
	// (km create --alias <alias>). Optional — falls back to DefaultProfile if absent.
	Alias string `mapstructure:"alias" yaml:"alias,omitempty" json:"alias,omitempty"`

	// Profile is the path to the SandboxProfile YAML file for this repo's sandbox.
	// Optional — falls back to GithubConfig.DefaultProfile when absent.
	Profile string `mapstructure:"profile" yaml:"profile,omitempty" json:"profile,omitempty"`

	// Allow is a supplemental network allowlist for this repo's sandbox.
	// Optional — sandbox profile's own allowlist is always the primary source.
	Allow []string `mapstructure:"allow" yaml:"allow,omitempty" json:"allow,omitempty"`

	// DefaultCommand is the per-repo override for the command key dispatched when
	// no @command verb is present in the GitHub PR comment. Falls back to
	// GithubConfig.DefaultCommand when empty. Phase 99 Plan 01.
	DefaultCommand string `mapstructure:"default_command" yaml:"default_command,omitempty" json:"default_command,omitempty"`
}

// GithubCommandEntry defines a named, operator-declared command that the bridge
// dispatches when the @bot-name <command> verb appears in a GitHub PR comment
// (or when used as the default_command fallback). Phase 99 Plan 01.
//
// All fields carry mapstructure tags — required by viper's UnmarshalKey;
// untagged fields are silently ignored (project_config_key_merge_list pitfall 1).
//
// The github.commands map is decoded by the SINGLE "github" merge-list entry
// (config.go ~line 484) + the UnmarshalKey("github", &cfg.Github) call below.
// Do NOT add a separate "github.commands" merge-list entry — the whole github:
// block is decoded atomically via UnmarshalKey; a sibling entry would be a no-op
// or cause parse-order issues (verified in 99-RESEARCH.md finding #6).
type GithubCommandEntry struct {
	// Description is a human-readable label shown in km github status and docs.
	Description string `mapstructure:"description" yaml:"description,omitempty" json:"description,omitempty"`

	// Alias is an optional sandbox alias to use instead of the repo-level alias
	// when this command is dispatched. Useful for routing commands to dedicated sandboxes.
	Alias string `mapstructure:"alias" yaml:"alias,omitempty" json:"alias,omitempty"`

	// Profile is an optional override SandboxProfile path for cold-sandbox creation
	// when this command is dispatched. Falls back to repo/install default when empty.
	Profile string `mapstructure:"profile" yaml:"profile,omitempty" json:"profile,omitempty"`

	// Allow is a supplemental network allowlist merged into the sandbox's allowlist
	// when this command's sandbox is provisioned.
	Allow []string `mapstructure:"allow" yaml:"allow,omitempty" json:"allow,omitempty"`

	// Prompt is the prompt text injected as the initial turn when this command is
	// dispatched. Required — the bridge skips dispatch if Prompt is empty.
	Prompt string `mapstructure:"prompt" yaml:"prompt" json:"prompt"`
}

// GithubEventRule is a single entry in the github.events config block.
// It maps a (webhook event type, optional action list, repo match glob) triple
// to a sandbox + prompt template that fires autonomously on GitHub webhook events.
// Phase 115.
//
// All fields carry mapstructure tags — required by viper's UnmarshalKey;
// untagged fields are silently ignored (project_config_key_merge_list pitfall 1).
//
// The github.events list is decoded by the SINGLE "github" merge-list entry
// (config.go:699) + the UnmarshalKey("github", &cfg.Github) call (config.go:831).
// Do NOT add a separate "github.events" merge-list entry — the whole github:
// block is decoded atomically via UnmarshalKey; a sibling entry would be a no-op
// or cause parse-order issues (RESEARCH.md Anti-Pattern + Pitfall 2).
type GithubEventRule struct {
	// On is the GitHub webhook event type, e.g. "repository", "push", "release".
	On string `mapstructure:"on" yaml:"on" json:"on"`

	// Actions filters by the action field inside the event payload (e.g. "created").
	// When empty, all actions for the event type match.
	Actions []string `mapstructure:"actions" yaml:"actions,omitempty" json:"actions,omitempty"`

	// Match is an exact "owner/repo" or glob "owner/*" pattern matched against
	// the repository full_name. Exact matches win over globs (same as github.repos).
	Match string `mapstructure:"match" yaml:"match" json:"match"`

	// Exclude is a list of glob patterns to suppress an otherwise-matching rule.
	Exclude []string `mapstructure:"exclude" yaml:"exclude,omitempty" json:"exclude,omitempty"`

	// Profile is the SandboxProfile path for cold-sandbox creation. Optional.
	Profile string `mapstructure:"profile" yaml:"profile,omitempty" json:"profile,omitempty"`

	// Alias is the sandbox alias. When set, the handler uses the warm path (SQS
	// enqueue); when empty, a fresh sandbox is cold-created (cold path).
	Alias string `mapstructure:"alias" yaml:"alias,omitempty" json:"alias,omitempty"`

	// Agent overrides the sandbox agent for this dispatch turn: "claude", "codex", or "".
	Agent string `mapstructure:"agent" yaml:"agent,omitempty" json:"agent,omitempty"`

	// CooldownSeconds, when > 0, suppresses repeated dispatch of the same
	// (event, repo, action) triple within the given window. 0 = no cooldown.
	// NOTE: yaml tag is camelCase (cooldownSeconds) to match the CONTEXT.md config
	// shape; mapstructure/json use snake_case (cooldown_seconds).
	CooldownSeconds int `mapstructure:"cooldown_seconds" yaml:"cooldownSeconds,omitempty" json:"cooldown_seconds,omitempty"`

	// Prompt is the template injected as the agent's initial turn.
	// Supports: {{repo}}, {{event}}, {{action}}, {{sender}}, {{default_branch}}, {{html_url}}.
	Prompt string `mapstructure:"prompt" yaml:"prompt" json:"prompt"`
}

// GithubConfig holds install-level GitHub defaults that flow into the bridge
// Lambda environment via km init. Phase 97 Plan 01 adds the github: block.
// Lambda environment via km init. Phase 97 Plan 01 adds the github: block.
//
// Maps to km-config.yaml key github. Absent key → zero value (no error).
// Exported as KM_GITHUB_REPOS (JSON-encoded) by ExportTerragruntEnvVars when
// at least one repo entry is present; absent config ⇒ dormant (nothing exported).
type GithubConfig struct {
	// Repos is the list of repository-to-sandbox mappings. Each entry maps a
	// repo's full_name to a km alias + profile + optional network allowlist.
	// Uses UnmarshalKey (structured list-of-objects) — same pattern as Clusters.
	Repos []GithubRepoEntry `mapstructure:"repos" yaml:"repos,omitempty"`

	// DefaultProfile is the fallback SandboxProfile path used when a matched
	// repo entry has no Profile set, or when no match is found and the bridge
	// falls through to the default case.
	DefaultProfile string `mapstructure:"default_profile" yaml:"default_profile,omitempty"`

	// DefaultCommand is the install-wide fallback command key (must be a key in
	// Commands) dispatched when no @verb is present and the matched repo entry
	// has no per-repo DefaultCommand. Optional — absent means no default dispatch.
	// Phase 99 Plan 01.
	DefaultCommand string `mapstructure:"default_command" yaml:"default_command,omitempty"`

	// Commands is the map of named operator-declared commands keyed by command verb
	// (e.g. "review", "triage"). Decoded by the existing UnmarshalKey("github", …)
	// call — no separate merge-list entry required (see GithubCommandEntry doc).
	// Nil/empty map when absent => bridge dormancy preserved. Phase 99 Plan 01.
	Commands map[string]GithubCommandEntry `mapstructure:"commands" yaml:"commands,omitempty" json:"commands,omitempty"`

	// PeerBridges is the Phase 100 federated relay peer list — a []string of sibling
	// km install github-bridge Function URLs. Exported as KM_GITHUB_PEER_BRIDGES
	// (comma-joined) by ExportTerragruntEnvVars for
	// infra/live/use1/lambda-github-bridge/terragrunt.hcl get_env("KM_GITHUB_PEER_BRIDGES", "").
	//   nil   → key absent from yaml; federation off (WebhookHandler.Relayer stays nil)
	//   []URL → broadcast unowned-repo webhooks to all peers (Plan 02 relay logic)
	//
	// DEVIATION from the Slack peer-bridges path (100-RESEARCH.md Pitfall 2): unlike
	// slack.peer_bridges, this field needs NO separate "github.peer_bridges" merge-list
	// entry and NO separate population block. The existing "github" merge entry
	// (config.go ~line 551) + the single v.UnmarshalKey("github", &cfg.Github) call
	// below decode the whole github: block atomically, this field included. Adding a
	// redundant merge entry would be a no-op at best (verified by TestLoadGithubPeerBridges_Set).
	PeerBridges []string `mapstructure:"peer_bridges" yaml:"peer_bridges,omitempty"`

	// DefaultRouter is the Phase 101 front-door orphan-repo router toggle — tri-state *bool;
	// nil/absent ⇒ dormant (Phase 100 byte-identical); true ⇒ front-door posts a helpful
	// reply when no install owns the commented-on repo. Exported as KM_GITHUB_DEFAULT_ROUTER
	// (strconv.FormatBool) by ExportTerragruntEnvVars (init.go) for
	// infra/live/use1/lambda-github-bridge/terragrunt.hcl get_env("KM_GITHUB_DEFAULT_ROUTER","false").
	//
	// Only the federation front-door install sets this to true; peers leave it absent.
	//
	// DECODED by the existing v.UnmarshalKey("github", &cfg.Github) call — NO new
	// "github.default_router" merge-list entry is required (RESEARCH Pitfall 6,
	// proven by TestLoadGithubDefaultRouter_Set). Mirrors Phase 100 PeerBridges precedent.
	DefaultRouter *bool `mapstructure:"default_router" yaml:"default_router,omitempty"`

	// Events is the Phase 115 generic webhook event → prompt router config.
	// Each entry maps a (event type, action list, repo glob) triple to a sandbox
	// and a prompt template. When absent/empty, the event routing path is dormant
	// and Handle() is byte-identical to Phase 114 for all non-issue_comment events.
	// Exported as KM_GITHUB_EVENTS (JSON-encoded) by ExportTerragruntEnvVars.
	//
	// DECODED by the SINGLE existing "github" merge-list entry (config.go:699) +
	// the v.UnmarshalKey("github", &cfg.Github) call (config.go:831).
	// NO new "github.events" merge-list entry is needed or permitted — the whole
	// github: block is decoded atomically (RESEARCH.md Pitfall 2).
	Events []GithubEventRule `mapstructure:"events" yaml:"events,omitempty" json:"events,omitempty"`
}

// H1Target is one fanout target for a HackerOne program: an {alias, profile}
// pair. Multi-target fanout (Phase 103) means a single trigger dispatches the
// same prompt to every target in a program's Targets list — the GitHub bridge
// resolved exactly one alias, H1 resolves N. Mirrors the inline {alias, profile}
// shape in km-config.yaml h1.programs[].targets.
type H1Target struct {
	// Alias is the sandbox alias for this target (km create --alias). Optional —
	// when empty the bridge derives "h1-{handle}" (resolve.defaultAlias analog).
	Alias string `mapstructure:"alias" yaml:"alias,omitempty" json:"alias,omitempty"`

	// Profile is the SandboxProfile path for cold create. Optional — falls back
	// to H1Config.DefaultProfile when empty.
	Profile string `mapstructure:"profile" yaml:"profile,omitempty" json:"profile,omitempty"`
}

// H1EventEntry maps a HackerOne lifecycle event (e.g. report_created) to the
// prompt dispatched on auto-triage. An absent/empty Events map leaves a program
// comment-keyword-only (auto-triage DORMANT by default) — mirrors the
// dormant-by-default posture used across the Slack/GitHub federation phases.
type H1EventEntry struct {
	// Prompt is the template injected as the initial agent turn when this event
	// fires. May reference report fields / {{args}} like GithubCommandEntry.Prompt.
	Prompt string `mapstructure:"prompt" yaml:"prompt" json:"prompt"`
}

// H1CommandEntry defines a named, operator-declared command parsed from a
// HackerOne comment body (the comment-keyword trigger model). Mirrors
// GithubCommandEntry (Phase 99) — Description for km h1 status / docs, Prompt as
// the injected turn template.
//
// All fields carry mapstructure tags — required by viper's UnmarshalKey;
// untagged fields are silently ignored (project_config_key_merge_list pitfall 1).
type H1CommandEntry struct {
	// Description is a human-readable label shown in km h1 status and docs.
	Description string `mapstructure:"description" yaml:"description,omitempty" json:"description,omitempty"`

	// Prompt is the prompt text injected as the initial turn when this command is
	// dispatched. Required — the bridge skips dispatch if Prompt is empty.
	Prompt string `mapstructure:"prompt" yaml:"prompt" json:"prompt"`
}

// H1ProgramEntry maps a HackerOne program (by its program handle, the routing
// key that replaces GitHub's owner/repo) to its multi-target dispatch config,
// login allowlist, and the two trigger-model maps: Events (auto-triage) and
// Commands (comment-keyword). Phase 103.
//
// Fields mirror the km-config.yaml h1.programs list-of-objects shape; json tags
// match the yaml keys so an env-encoded JSON form is self-describing.
type H1ProgramEntry struct {
	// Handle is the HackerOne program handle matched against the incoming
	// webhook's data.report program-handle relationship. Exact match (the
	// deny-by-default routing key).
	Handle string `mapstructure:"handle" yaml:"handle" json:"handle"`

	// Targets is the multi-target fanout list — each entry dispatches the same
	// prompt to its own sandbox (alias+profile) with its own report-id-keyed
	// thread continuity row.
	Targets []H1Target `mapstructure:"targets" yaml:"targets,omitempty" json:"targets,omitempty"`

	// Allow is the HackerOne username allowlist, deny-by-default: a comment from
	// a username not in this list is silently ignored (and gates the
	// /reply_to_researcher external-reply path).
	Allow []string `mapstructure:"allow" yaml:"allow,omitempty" json:"allow,omitempty"`

	// BotHandle optionally overrides the install-wide H1Config.BotHandle for this
	// program (the literal comment-keyword token). Empty => install default.
	BotHandle string `mapstructure:"bot_handle" yaml:"bot_handle,omitempty" json:"bot_handle,omitempty"`

	// Events is the auto-triage map: H1 lifecycle event type → prompt. DORMANT
	// when absent/empty (program is comment-keyword-only). Phase 103.
	Events map[string]H1EventEntry `mapstructure:"events" yaml:"events,omitempty" json:"events,omitempty"`

	// Commands is the comment-context command map: /command name → prompt.
	// Separate from Events (a program may declare both context sets). Phase 103.
	Commands map[string]H1CommandEntry `mapstructure:"commands" yaml:"commands,omitempty" json:"commands,omitempty"`

	// DefaultCommand is the per-program command key dispatched when a triggering
	// comment carries the bot handle but no /command. Must name a key in Commands.
	// Empty => free-form prompt. Phase 103.
	DefaultCommand string `mapstructure:"default_command" yaml:"default_command,omitempty" json:"default_command,omitempty"`
}

// H1Config holds install-level HackerOne bridge defaults that flow into the
// km-h1-bridge Lambda environment via km init. Phase 103.
//
// Maps to km-config.yaml key h1. Absent key → zero value (no error, no programs
// => dormant — byte-identical to a pre-H1 install). Mirrors GithubConfig.
type H1Config struct {
	// BotHandle is the install-wide comment-keyword trigger token (e.g. "@km").
	// HackerOne internal comments have no bot user to @-mention, so the handle is
	// a literal substring match in the comment body. Per-program H1ProgramEntry.BotHandle
	// overrides this.
	BotHandle string `mapstructure:"bot_handle" yaml:"bot_handle,omitempty" json:"bot_handle,omitempty"`

	// DefaultProfile is the fallback SandboxProfile path used when a matched
	// target has no Profile set.
	DefaultProfile string `mapstructure:"default_profile" yaml:"default_profile,omitempty" json:"default_profile,omitempty"`

	// Programs is the list of program-handle-to-targets mappings consumed by the
	// bridge Lambda to resolve which sandbox(es) to dispatch a report event to.
	// Uses UnmarshalKey (structured list-of-objects) — same pattern as Github.Repos.
	Programs []H1ProgramEntry `mapstructure:"programs" yaml:"programs,omitempty" json:"programs,omitempty"`
}

// ChecksConfig holds the install-level serverless check-runner defaults (Phase 116).
// Maps to km-config.yaml key checks. Absent key → zero value (no error, dormant).
// Triggers is decoded atomically via UnmarshalKey("checks", &cfg.Checks) — the
// "checks" merge-list entry is the critical precondition (project_config_key_merge_list).
//
// The checks.triggers list mirrors github.events in shape: a config-driven,
// list-of-objects block where the decision logic (when_py predicate) lives in
// CONFIG, never in the snippet. Absent config ⇒ no triggers ⇒ check Lambdas
// still run and capture output but never dispatch sandbox prompts.
type ChecksConfig struct {
	// Triggers is the list of check-to-alias dispatch rules. Each rule names a
	// deployed check Lambda, a Python predicate (when_py), and the alias-targeted
	// sandbox to receive the prompt when the predicate is truthy.
	// Absent list → zero-length slice (dormant, no dispatch).
	Triggers []CheckTrigger `mapstructure:"triggers" yaml:"triggers,omitempty"`
}

// CheckTrigger defines one check→sandbox dispatch rule for the km check runner.
// When a check Lambda run produces JSON output that satisfies the when_py predicate,
// a sandbox prompt is dispatched to the alias-targeted box (resume-or-cold-create).
//
// Tag discipline mirrors GithubEventRule (the structural template):
//   - mapstructure: snake_case (viper/mapstructure decode key)
//   - yaml: field name used in km-config.yaml; camelCase for multi-word fields
//     (matches CONTEXT.md config shape: cooldownSeconds, onAbsent)
type CheckTrigger struct {
	// Check is the name of the deployed check Lambda (matches the name used in
	// `km check deploy`). Required — the dispatch rule is inert without it.
	Check string `mapstructure:"check" yaml:"check"`

	// WhenPy is a Python predicate block. Wrapped as `def _pred(out): <body>` at
	// runtime; `out` is the parsed JSON output dict. Must return bool or (bool, reason).
	// Inline or @file (resolved at km check deploy/sync time). Optional — absent
	// means "always trigger" (useful for testing). Baked into KM_CHECK_TRIGGER.
	WhenPy string `mapstructure:"when_py" yaml:"when_py,omitempty"`

	// Alias is the sandbox alias targeted for resume-or-cold-create dispatch.
	// Required — without an alias the rule cannot route to a box.
	Alias string `mapstructure:"alias" yaml:"alias"`

	// Prompt is the template for the sandbox agent's initial turn.
	// Supports {{reason}} and {{out.<field>}} substitution. Inline or @file.
	// Optional — absent sends an empty prompt (the agent uses its default).
	Prompt string `mapstructure:"prompt" yaml:"prompt,omitempty"`

	// OnAbsent controls cold-sandbox creation when the alias is not found:
	//   "cold-create" (default) — provision a new sandbox from Profile.
	//   "skip"                  — do nothing; log check_dispatch_skip.
	// NOTE: yaml tag is camelCase (onAbsent) to match the CONTEXT.md config shape;
	// mapstructure uses snake_case (on_absent).
	OnAbsent string `mapstructure:"on_absent" yaml:"onAbsent,omitempty"`

	// CooldownSeconds, when > 0, suppresses repeated dispatch of the same check
	// within the given window. 0 = no cooldown. Enforced in Stage B (ttl-handler)
	// via the nonces table (key "check-trigger:{check}").
	// NOTE: yaml tag is camelCase (cooldownSeconds) to match the CONTEXT.md config
	// shape; mapstructure uses snake_case (cooldown_seconds). Mirrors GithubEventRule.
	CooldownSeconds int `mapstructure:"cooldown_seconds" yaml:"cooldownSeconds,omitempty"`
}

// Config holds all configuration values for the km CLI.
type Config struct {
	// ProfileSearchPaths is the ordered list of directories to search for profiles.
	// Built-in profiles are always searched first, before these paths.
	ProfileSearchPaths []string

	// LogLevel controls the zerolog log level (trace, debug, info, warn, error).
	LogLevel string

	// Version is the km CLI version string, injected at build time.
	Version string

	// StateBucket is the S3 bucket used for Terraform state and sandbox metadata.
	// Set via KM_STATE_BUCKET environment variable.
	StateBucket string

	// TTLLambdaARN is the Lambda function ARN for TTL sandbox teardown.
	// Set via KM_TTL_LAMBDA_ARN environment variable.
	// If empty, TTL schedules are not created.
	TTLLambdaARN string

	// SchedulerRoleARN is the IAM role ARN that EventBridge Scheduler assumes
	// to invoke the TTL Lambda. Set via KM_SCHEDULER_ROLE_ARN environment variable.
	SchedulerRoleARN string

	// --- Platform fields (from km-config.yaml at repo root) ---

	// Domain is the base domain for the platform (e.g. "klankermaker.ai").
	// Set via km-config.yaml domain key or KM_DOMAIN environment variable.
	// When empty, callers default to "klankermaker.ai". Used to derive email
	// addresses (sandboxes.{Domain}), schema $id, and apiVersion prefixes so
	// forks work with any domain without code changes.
	Domain string

	// OrganizationAccountID is the AWS Organizations management account ID (SCP target).
	// Maps to km-config.yaml key accounts.organization. Optional: blank skips SCP deployment.
	OrganizationAccountID string

	// DNSParentAccountID is the AWS account ID owning the parent Route53 hosted zone for cfg.Domain.
	// Maps to km-config.yaml key accounts.dns_parent. Blank skips DNS delegation in km init.
	DNSParentAccountID string

	// TerraformAccountID is the AWS account ID used for Terraform/infrastructure operations.
	// Maps to km-config.yaml key accounts.terraform.
	TerraformAccountID string

	// ApplicationAccountID is the AWS account ID where sandboxes are provisioned.
	// Maps to km-config.yaml key accounts.application.
	ApplicationAccountID string

	// SSOStartURL is the AWS SSO portal URL.
	// Maps to km-config.yaml key sso.start_url.
	SSOStartURL string

	// SSORegion is the AWS region where the SSO instance is hosted.
	// Maps to km-config.yaml key sso.region.
	SSORegion string

	// PrimaryRegion is the default AWS region for infrastructure operations.
	// Maps to km-config.yaml key region.
	PrimaryRegion string

	// BudgetTableName is the DynamoDB table name for sandbox budget tracking.
	// Maps to km-config.yaml key budget_table_name. Defaults to "km-budgets".
	BudgetTableName string

	// IdentityTableName is the DynamoDB table name for sandbox identity tracking.
	// Maps to km-config.yaml key identity_table_name. Defaults to "km-identities".
	IdentityTableName string

	// SandboxTableName is the DynamoDB table name for sandbox metadata.
	// Maps to km-config.yaml key sandbox_table_name. Defaults to "km-sandboxes".
	SandboxTableName string

	// ArtifactsBucket is the S3 bucket used for storing sandbox artifacts and profiles.
	// Set via KM_ARTIFACTS_BUCKET environment variable or artifacts_bucket in km-config.yaml.
	// Required for ECS sandbox re-provisioning via km budget add.
	ArtifactsBucket string

	// AWSProfile is the AWS CLI profile name used for infrastructure operations.
	// Set via KM_AWS_PROFILE environment variable or aws_profile in km-config.yaml.
	// Defaults to "klanker-terraform" when empty.
	AWSProfile string

	// Route53ZoneID is the hosted zone ID for the sandboxes subdomain.
	// Set via KM_ROUTE53_ZONE_ID environment variable or route53_zone_id in km-config.yaml.
	// Auto-created by km init if not set.
	Route53ZoneID string

	// OperatorEmail is the email address that receives sandbox lifecycle notifications
	// (TTL expiry, idle timeout, budget exhaustion, spot interruption, errors).
	// Set via operator_email in km-config.yaml or KM_OPERATOR_EMAIL environment variable.
	OperatorEmail string

	// SafePhrase is the shared secret for email-to-create authentication.
	// Included in emails as "KM-AUTH: <phrase>" to authorize sandbox creation.
	// Set via safe_phrase in km-config.yaml or KM_SAFE_PHRASE environment variable.
	// Written to SSM at /km/config/remote-create/safe-phrase during km init.
	SafePhrase string

	// RsyncPaths is the list of relative paths (from sandbox user $HOME) to
	// include in rsync snapshots. Default: [".claude", ".bashrc", ".gitconfig"]
	RsyncPaths []string

	// MaxSandboxes is the maximum number of concurrently active sandboxes allowed.
	// Set via max_sandboxes in km-config.yaml or KM_MAX_SANDBOXES environment variable.
	// A value of 0 means unlimited (no enforcement). Defaults to 10.
	MaxSandboxes int

	// SchedulesTableName is the DynamoDB table name for km-at schedule metadata.
	// Maps to km-config.yaml key schedules_table_name. Defaults to "km-schedules".
	SchedulesTableName string

	// CreateHandlerLambdaARN is the Lambda function ARN invoked by km-at create schedules
	// to provision sandboxes on a deferred or recurring basis.
	// Set via create_handler_lambda_arn in km-config.yaml or KM_CREATE_HANDLER_LAMBDA_ARN.
	CreateHandlerLambdaARN string

	// DoctorStaleAMIDays is the age threshold (in days) used by `km doctor` to flag
	// unused AMIs as stale. An AMI is "stale" when (a) it is older than this threshold,
	// (b) no profile in cfg.ProfileSearchPaths references it, AND (c) no running sandbox
	// currently uses it. Maps to km-config.yaml key doctor_stale_ami_days. Defaults to 30.
	DoctorStaleAMIDays int

	// DoctorLogRetentionDays is the retention period (in days) applied by `km doctor`
	// when the --set-log-retention flag sets a retention policy on CloudWatch log groups
	// that currently have none. Maps to km-config.yaml key doctor_log_retention_days. Defaults to 30.
	DoctorLogRetentionDays int

	// DoctorS3ExpireDays is the expiry period (in days) used by `km doctor` when the
	// --set-s3-lifecycle flag installs an S3 lifecycle rule expiring transient artifact
	// prefixes (logs/, remote-create/, agent-runs/, slack-inbound/). Maps to
	// km-config.yaml key doctor_s3_expire_days. Defaults to 30.
	DoctorS3ExpireDays int

	// DoctorIgnorePrefixes lists sibling resource_prefix values (other km installs
	// sharing this AWS account) that `km doctor` should treat as KNOWN — their
	// cross-install resources (SCPs, SES rules, sandbox-secrets KMS aliases) are
	// reported as OK rather than WARN. Maps to km-config.yaml key
	// doctor_ignore_prefixes. The --ignore-prefix flag augments this list.
	DoctorIgnorePrefixes []string

	// SlackThreadsTableName is the DynamoDB table name for the Slack-inbound
	// (channel_id, thread_ts) → claude_session_id mapping. Default
	// "km-slack-threads"; respects ResourcePrefix when set (Phase 66
	// forward-compat). Maps to km-config.yaml key slack_threads_table_name.
	SlackThreadsTableName string

	// SlackStreamMessagesTableName is the DynamoDB table name for the Phase 68
	// transcript-streaming (channel_id, slack_ts) → {sandbox_id, session_id,
	// transcript_offset, ttl_expiry} mapping. Default "km-slack-stream-messages";
	// respects ResourcePrefix when set. Maps to km-config.yaml key
	// slack_stream_messages_table_name.
	SlackStreamMessagesTableName string

	// SlackChannelsTableName is the DynamoDB table name for the Phase 104
	// alias→channel_id O(1) durable mapping. Default "km-slack-channels";
	// respects ResourcePrefix when set. Maps to km-config.yaml key
	// slack_channels_table_name.
	SlackChannelsTableName string

	// ResourcePrefix is the Phase-66 multi-instance prefix applied to AWS
	// resource names (e.g. "km", "stg", "kpf"). Default "km" via
	// GetResourcePrefix(). Phase 66 will populate this from km-config.yaml;
	// Phase 67 ships the shim helper so downstream code can use the helper
	// unconditionally. Maps to km-config.yaml key resource_prefix.
	ResourcePrefix string

	// EmailSubdomain is the subdomain used for SES email addresses
	// ({sandboxID}@{subdomain}.{domain}). Maps to km-config.yaml key
	// email_subdomain. Defaults to "sandboxes" via GetEmailDomain().
	// One-time choice at km init — changing requires fresh DNS/SES verification.
	EmailSubdomain string

	// ContainerSubstratesEnabled gates the ECR image build/push steps in
	// km init: km-sandbox container image plus the four sidecar images
	// (dns-proxy, http-proxy, audit-log, tracing). Container images are only
	// pulled by the docker and ecs substrates; EC2 sandboxes get raw binaries
	// from S3 (see pkg/compiler/userdata.go). Pointer-typed for tri-state
	// (unset/true/false): nil means "use default", which ShouldBuildContainerImages
	// resolves to true so existing installs keep building images. Maps to
	// km-config.yaml key container_substrates_enabled.
	ContainerSubstratesEnabled *bool

	// Clusters is the list of registered Kubernetes clusters for cross-account IRSA
	// integration. Each entry maps a cluster's OIDC provider to an IAM role in the
	// application account. Maps to km-config.yaml key clusters (Plan 80).
	// Absent key → empty slice (no error). Managed via `km cluster add/list/rm`.
	Clusters []ClusterConfig `mapstructure:"clusters" yaml:"clusters"`

	// Slack holds install-level Slack defaults (Phase 91.1). Currently only
	// MentionOnly is populated; future Slack-wide knobs slot in here. Maps to
	// km-config.yaml key slack. Absent key → zero value (no error).
	Slack SlackConfig `mapstructure:"slack" yaml:"slack,omitempty"`

	// Github holds install-level GitHub App defaults (Phase 97). Repos is the
	// list of repository-to-sandbox mappings consumed by the bridge Lambda.
	// Maps to km-config.yaml key github. Absent key → zero value (no error).
	// Exported as KM_GITHUB_REPOS (JSON) by ExportTerragruntEnvVars when configured.
	Github GithubConfig `mapstructure:"github" yaml:"github,omitempty"`

	// H1 holds install-level HackerOne bridge defaults (Phase 103). Programs is the
	// list of program-handle-to-targets mappings consumed by the km-h1-bridge Lambda.
	// Maps to km-config.yaml key h1. Absent key → zero value (no error, dormant).
	H1 H1Config `mapstructure:"h1" yaml:"h1,omitempty"`

	// Checks holds the install-level serverless check-runner config (Phase 116).
	// Triggers is the list of check→alias dispatch rules. Maps to km-config.yaml
	// key checks. Absent key → zero value (no error, dormant — no dispatch fired).
	// CRITICAL: "checks" must be in the v2→v merge-list in Load() or this block is
	// silently ignored (project_config_key_merge_list footgun).
	Checks ChecksConfig `mapstructure:"checks" yaml:"checks,omitempty"`

	// YAMLDefaults holds the raw km-config.yaml values for env-bound keys,
	// snapshotted during Load() BEFORE viper's AutomaticEnv binds env vars into
	// the cfg fields. Used by ExportTerragruntEnvVars to detect drift between
	// the env var and the yaml-configured value.
	// Keys are dotted yaml paths (e.g. "region", "artifacts_bucket", "domain").
	// Empty map when km-config.yaml is not found or key is absent in yaml.
	YAMLDefaults map[string]string

	// ConfigFilePath is the absolute path to the km-config.yaml file that was
	// successfully loaded by Load(). Empty string when km-config.yaml was not
	// found (first-install or Lambda env). Used by km init to derive the base
	// directory for @file prompt resolution in github.commands entries (Phase 99 Plan 03):
	//   configDir := filepath.Dir(cfg.ConfigFilePath)
	// The path is populated from v2.ConfigFileUsed() after ReadInConfig succeeds.
	ConfigFilePath string
}

// accountsYamlAuthoritativeKeys lists the viper keys for which km-config.yaml
// values take precedence over shell environment variables. This is an intentional
// asymmetry introduced by Phase 84.3 closure (h):
//
//   - accounts.organization, accounts.dns_parent, accounts.application: these three
//     account IDs are platform topology values that must reflect km-config.yaml so
//     that every km command (km init, km doctor, km info) uses the same account
//     regardless of what the operator has exported in their shell. Operators commonly
//     export KM_ACCOUNTS_APPLICATION for one-off experiments that should NOT silently
//     override the install-level topology for shared-account operations.
//
//   - accounts.terraform: INTENTIONALLY OMITTED. Operators legitimately set
//     KM_ACCOUNTS_TERRAFORM for one-off invocations to a different infra account
//     (e.g. staging vs production). Env-var precedence for this key is therefore
//     preserved. See Phase 84.3 CONTEXT.md decision and RESEARCH.md Pitfall 1.
var accountsYamlAuthoritativeKeys = map[string]bool{
	"accounts.organization": true,
	"accounts.dns_parent":   true,
	"accounts.application":  true,
	// accounts.terraform is intentionally absent — env wins for that key.
}

// isSetByEnv returns true if the given viper key has been overridden by an environment
// variable (KM_ prefix). Viper maps "foo.bar" -> KM_FOO_BAR (dots become underscores).
func isSetByEnv(_ *viper.Viper, key string) bool {
	envKey := "KM_" + strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(key))
	return os.Getenv(envKey) != ""
}

// Load reads configuration from (in order of increasing precedence):
//  1. Defaults
//  2. ~/.km/config.yaml
//  3. ./km-config.yaml (repo-root platform configuration, merged on top)
//  4. Environment variables with KM_ prefix
//  5. CLI flags (applied by the root command after Load returns)
//
// Returns a Config with all values resolved from the above sources.
func Load() (*Config, error) {
	v := viper.New()

	// Defaults for existing fields
	v.SetDefault("profile_search_paths", []string{"./profiles", "~/.km/profiles"})
	v.SetDefault("log_level", "info")
	v.SetDefault("state_bucket", "")
	v.SetDefault("ttl_lambda_arn", "")
	v.SetDefault("scheduler_role_arn", "")

	// Defaults for new platform fields.
	// Note: table names default to "" so prefix-aware helpers like
	// GetSandboxTableName() can derive {prefix}-{table} from resource_prefix.
	// Hardcoded "km-*" defaults would defeat multi-instance support.
	v.SetDefault("max_sandboxes", 10)
	v.SetDefault("budget_table_name", "")
	v.SetDefault("identity_table_name", "")
	v.SetDefault("sandbox_table_name", "")
	v.SetDefault("artifacts_bucket", "")
	v.SetDefault("aws_profile", "klanker-terraform")
	v.SetDefault("rsync_paths", []string{".claude", ".bashrc", ".bash_profile", ".gitconfig"})
	v.SetDefault("schedules_table_name", "")
	v.SetDefault("create_handler_lambda_arn", "")
	v.SetDefault("doctor_stale_ami_days", 30)
	v.SetDefault("doctor_log_retention_days", 30)
	v.SetDefault("doctor_s3_expire_days", 30)
	v.SetDefault("slack_threads_table_name", "")
	v.SetDefault("slack_stream_messages_table_name", "")
	v.SetDefault("slack_channels_table_name", "")
	v.SetDefault("resource_prefix", "km")
	v.SetDefault("email_subdomain", "sandboxes")
	v.SetDefault("clusters", []interface{}{})

	// Primary config file: ~/.km/config.yaml
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	home, err := os.UserHomeDir()
	if err == nil {
		v.AddConfigPath(filepath.Join(home, ".km"))
	}
	v.AddConfigPath(".")

	// Read config file — ignore "not found" errors; fail on parse errors
	if err := v.ReadInConfig(); err != nil {
		if _, notFound := err.(viper.ConfigFileNotFoundError); !notFound {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
	}

	// Environment variable overrides (KM_PROFILE_SEARCH_PATHS, KM_LOG_LEVEL, etc.)
	v.SetEnvPrefix("KM")
	// SetEnvKeyReplacer maps dot-notation viper keys to underscored env vars:
	// "accounts.terraform" → KM_ACCOUNTS_TERRAFORM, "sso.start_url" → KM_SSO_START_URL.
	// Without this, AutomaticEnv only handles flat (non-dot) keys correctly.
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()

	// Secondary config file: km-config.yaml in current directory or repo root.
	// Values in km-config.yaml are merged on top of ~/.km/config.yaml but environment
	// variables (set via AutomaticEnv above) retain highest precedence.
	v2 := viper.New()
	v2.SetConfigName("km-config")
	v2.SetConfigType("yaml")
	// Explicit config path override (used by Lambda cold start)
	if configPath := os.Getenv("KM_CONFIG_PATH"); configPath != "" {
		v2.SetConfigFile(configPath)
	} else {
		v2.AddConfigPath(".")
		// Also search KM_REPO_ROOT (used in Lambda where CWD != repo root)
		if repoRoot := os.Getenv("KM_REPO_ROOT"); repoRoot != "" {
			v2.AddConfigPath(repoRoot)
		}
	}
	var (
		yamlDefaults   map[string]string
		configFilePath string
	)
	if err := v2.ReadInConfig(); err == nil {
		// Capture the km-config.yaml path so callers can derive the base directory
		// for @file resolution (Phase 99 Plan 03). v2.ConfigFileUsed() returns the
		// absolute path after a successful ReadInConfig.
		configFilePath = v2.ConfigFileUsed()

		// Merge platform keys from v2 into v only when not already overridden by env.
		for _, key := range []string{
			"domain",
			"accounts.organization",
			"accounts.dns_parent",
			"accounts.terraform",
			"accounts.application",
			"sso.start_url",
			"sso.region",
			"region",
			"budget_table_name",
			"identity_table_name",
			"sandbox_table_name",
			"artifacts_bucket",
			"aws_profile",
			"state_bucket",
			"route53_zone_id",
			"operator_email",
			"safe_phrase",
			"rsync_paths",
			"max_sandboxes",
			"schedules_table_name",
			"create_handler_lambda_arn",
			"ttl_lambda_arn",
			"scheduler_role_arn",
			"doctor_stale_ami_days",
			"doctor_log_retention_days",
			"doctor_s3_expire_days",
			"doctor_ignore_prefixes",
			"slack_threads_table_name",
			"slack_stream_messages_table_name",
			// Phase 104.3: durable alias→channel_id DDB store. CRITICAL: without
			// this entry, slack_channels_table_name in km-config.yaml is silently
			// ignored (project_config_key_merge_list footgun).
			"slack_channels_table_name",
			"resource_prefix",
			"email_subdomain",
			"container_substrates_enabled",
			"clusters",
			// Phase 91.1: nested key for the polite-bot install-level default.
			"slack.mention_only",
			// Phase 91.4: nested key for the first-only reactor install-level default.
			"slack.react_always",
			// Phase 95: federated relay peer list. CRITICAL: without this entry,
			// v2.Get("slack.peer_bridges") is never merged into v and the field
			// stays nil regardless of what is in km-config.yaml (the known
			// project_config_key_merge_list footgun).
			"slack.peer_bridges",
			// Phase 96: front-door router toggle. CRITICAL: without this entry,
			// slack.default_router: true is silently ignored (project_config_key_merge_list).
			"slack.default_router",
			// Phase 97: github block (repos list-of-objects + default_profile). CRITICAL:
			// without this entry, the entire github: block is silently dropped regardless
			// of km-config.yaml content (project_config_key_merge_list footgun).
			"github",
			// Phase 103: h1 block (programs list-of-objects + bot_handle + default_profile).
			// CRITICAL: without this entry the entire h1: block is silently dropped
			// regardless of km-config.yaml content (project_config_key_merge_list footgun).
			// The whole h1: block (including the nested events/commands maps) is decoded
			// atomically by the single v.UnmarshalKey("h1", &cfg.H1) call below — do NOT
			// add sibling "h1.*" entries (mirrors the github precedent).
			"h1",
			// Phase 116: checks block (triggers list-of-objects). CRITICAL: without this
			// entry the entire checks: block is silently dropped regardless of km-config.yaml
			// content (project_config_key_merge_list footgun). The whole checks: block is
			// decoded atomically by v.UnmarshalKey("checks", &cfg.Checks) below — do NOT
			// add sibling "checks.*" entries (mirrors the github and h1 precedent).
			// Absent checks: block → zero value → dormant (no trigger dispatch).
			"checks",
		} {
			// yaml wins unconditionally for accountsYamlAuthoritativeKeys (organization,
			// dns_parent, application). For all other keys, env-var takes precedence
			// over yaml (standard viper merge semantics).
			if v2.IsSet(key) && (accountsYamlAuthoritativeKeys[key] || !isSetByEnv(v, key)) {
				v.Set(key, v2.Get(key))
			}
		}

		// Snapshot raw yaml values for env-bound keys so ExportTerragruntEnvVars
		// can detect drift between env vars and yaml values (Phase 84.3 gap closure 1).
		// This snapshot is taken AFTER the merge loop so the v2 values are definitive,
		// but BEFORE building cfg (whose fields are baked with env values by AutomaticEnv).
		yamlDefaults = map[string]string{}
		for _, key := range []string{
			"region", "domain", "artifacts_bucket", "resource_prefix",
			"operator_email", "route53_zone_id", "scheduler_role_arn",
			"email_subdomain",
		} {
			if v2.IsSet(key) {
				yamlDefaults[key] = v2.GetString(key)
			}
		}
	}
	// Not finding km-config.yaml is fine — continue with existing config.

	cfg := &Config{
		// Existing fields
		ProfileSearchPaths: v.GetStringSlice("profile_search_paths"),
		LogLevel:           v.GetString("log_level"),
		StateBucket:        v.GetString("state_bucket"),
		TTLLambdaARN:       v.GetString("ttl_lambda_arn"),
		SchedulerRoleARN:   v.GetString("scheduler_role_arn"),

		// New platform fields
		Domain:                       v.GetString("domain"),
		OrganizationAccountID:        v.GetString("accounts.organization"),
		DNSParentAccountID:           v.GetString("accounts.dns_parent"),
		TerraformAccountID:           v.GetString("accounts.terraform"),
		ApplicationAccountID:         v.GetString("accounts.application"),
		SSOStartURL:                  v.GetString("sso.start_url"),
		SSORegion:                    v.GetString("sso.region"),
		PrimaryRegion:                v.GetString("region"),
		BudgetTableName:              v.GetString("budget_table_name"),
		IdentityTableName:            v.GetString("identity_table_name"),
		SandboxTableName:             v.GetString("sandbox_table_name"),
		ArtifactsBucket:              v.GetString("artifacts_bucket"),
		AWSProfile:                   v.GetString("aws_profile"),
		Route53ZoneID:                v.GetString("route53_zone_id"),
		OperatorEmail:                v.GetString("operator_email"),
		SafePhrase:                   v.GetString("safe_phrase"),
		RsyncPaths:                   v.GetStringSlice("rsync_paths"),
		MaxSandboxes:                 v.GetInt("max_sandboxes"),
		SchedulesTableName:           v.GetString("schedules_table_name"),
		CreateHandlerLambdaARN:       v.GetString("create_handler_lambda_arn"),
		DoctorStaleAMIDays:           v.GetInt("doctor_stale_ami_days"),
		DoctorLogRetentionDays:       v.GetInt("doctor_log_retention_days"),
		DoctorS3ExpireDays:           v.GetInt("doctor_s3_expire_days"),
		DoctorIgnorePrefixes:         v.GetStringSlice("doctor_ignore_prefixes"),
		SlackThreadsTableName:        v.GetString("slack_threads_table_name"),
		SlackStreamMessagesTableName: v.GetString("slack_stream_messages_table_name"),
		SlackChannelsTableName:       v.GetString("slack_channels_table_name"),
		ResourcePrefix:               v.GetString("resource_prefix"),
		EmailSubdomain:               v.GetString("email_subdomain"),
		YAMLDefaults:                 yamlDefaults,
		ConfigFilePath:               configFilePath,
	}

	// ContainerSubstratesEnabled is tri-state via *bool: only populated when
	// the operator has explicitly set the key, so ShouldBuildContainerImages
	// can default unset → true for back-compat.
	if v.IsSet("container_substrates_enabled") {
		val := v.GetBool("container_substrates_enabled")
		cfg.ContainerSubstratesEnabled = &val
	}

	// Phase 91.1: slack.mention_only is tri-state via *bool. Only populated when
	// the operator has explicitly set the key — absent yaml key → nil pointer →
	// ExportTerragruntEnvVars emits nothing → terragrunt.hcl get_env() default
	// ("false") kicks in. Set to true to flip the install default to polite-bot.
	if v.IsSet("slack.mention_only") {
		val := v.GetBool("slack.mention_only")
		cfg.Slack.MentionOnly = &val
	}

	// Phase 91.4: slack.react_always is tri-state via *bool. Same shape as
	// slack.mention_only. Absent → bridge default "true" (react on every
	// dispatch). Set to false to flip to first-only-react.
	if v.IsSet("slack.react_always") {
		val := v.GetBool("slack.react_always")
		cfg.Slack.ReactAlways = &val
	}

	// Phase 95: slack.peer_bridges is a []string federated relay peer list.
	// Only populated when explicitly set — absent yaml key => nil slice =>
	// federation off (EventsHandler.Relayer stays nil). Uses GetStringSlice
	// directly; NOT the *bool tri-state. nil slice is the "federation off"
	// sentinel — no pointer indirection needed for []string.
	if v.IsSet("slack.peer_bridges") {
		cfg.Slack.PeerBridges = v.GetStringSlice("slack.peer_bridges")
	}

	// Phase 96: slack.default_router is tri-state via *bool. Only populated when
	// the operator has explicitly set the key — absent yaml key → nil pointer →
	// ExportTerragruntEnvVars emits nothing → terragrunt.hcl get_env() default
	// ("false") kicks in → router dormant (Phase 95 byte-identical). Set to true
	// on the front-door install only to enable the orphan-channel threaded reply.
	if v.IsSet("slack.default_router") {
		val := v.GetBool("slack.default_router")
		cfg.Slack.DefaultRouter = &val
	}

	// Clusters is a structured slice — viper's UnmarshalKey handles the
	// mapstructure decoding from the merged "clusters" key. SetDefault above
	// ensures a non-nil empty slice when the key is absent (RESEARCH.md Pitfall 6).
	if err := v.UnmarshalKey("clusters", &cfg.Clusters); err != nil {
		return nil, fmt.Errorf("unmarshal clusters: %w", err)
	}

	// Phase 97: github is a structured block (list-of-objects + scalar). Use
	// UnmarshalKey — same pattern as clusters — because repos is a list-of-objects
	// and viper's scalar GetString/GetStringSlice can't decode it. Absent key =>
	// zero-value GithubConfig (no error, no repos => dormant). The merge-list entry
	// "github" above is the precondition; without it this unmarshal sees an empty map.
	if err := v.UnmarshalKey("github", &cfg.Github); err != nil {
		return nil, fmt.Errorf("unmarshal github: %w", err)
	}

	// Phase 103: h1 is a structured block (programs list-of-objects with nested
	// events/commands maps + scalar bot_handle/default_profile). Use UnmarshalKey —
	// same pattern as github — because programs is a list-of-objects and viper's
	// scalar getters can't decode it. Absent key => zero-value H1Config (no error,
	// no programs => dormant). The merge-list entry "h1" above is the precondition;
	// without it this unmarshal sees an empty map (project_config_key_merge_list).
	if err := v.UnmarshalKey("h1", &cfg.H1); err != nil {
		return nil, fmt.Errorf("unmarshal h1: %w", err)
	}

	// Phase 116: checks is a structured block (triggers list-of-objects). Use
	// UnmarshalKey — same pattern as github and h1 — because triggers is a
	// list-of-objects and viper's scalar getters can't decode it. Absent key =>
	// zero-value ChecksConfig (no error, no triggers => dormant; no dispatch fired).
	// The merge-list entry "checks" above is the precondition; without it this
	// unmarshal sees an empty map (project_config_key_merge_list footgun).
	// Non-fatal: a YAML parse error in the checks block yields zero value + log;
	// we never fatal-error on absent or malformed optional config blocks.
	//
	// YAMLDefaults snapshot: checks.triggers is a list-of-objects with no scalar
	// top-level key to snapshot for drift WARN — mirrors the github.events treatment
	// (github.events is also list-of-objects; no scalar entry in yamlDefaults).
	// Drift detection for per-check env vars (KM_CHECK_TRIGGER) is handled at
	// km check deploy / km check sync time (per-Lambda, not at km init).
	if err := v.UnmarshalKey("checks", &cfg.Checks); err != nil {
		// non-fatal: absent checks: block → zero value → dormant
		// Mirrors h1's non-fatal treatment for future forward-compat
		return nil, fmt.Errorf("unmarshal checks: %w", err)
	}

	// If the AWS profile was set by default (not explicitly configured), verify it
	// exists in ~/.aws/config or ~/.aws/credentials. On EC2 instances there are no
	// named profiles — clear the field so the SDK falls through to the default
	// credential chain (instance profile / IMDS).
	if cfg.AWSProfile != "" && !v.IsSet("aws_profile") {
		if !awsProfileExists(cfg.AWSProfile) {
			cfg.AWSProfile = ""
		}
	}

	// Clamp DoctorStaleAMIDays: a zero or negative value would never flag any AMI,
	// which is almost certainly operator misconfiguration. Fall back to the default.
	if cfg.DoctorStaleAMIDays <= 0 {
		cfg.DoctorStaleAMIDays = 30
	}
	// Clamp DoctorLogRetentionDays: zero or negative would be meaningless. Fall back to 30.
	if cfg.DoctorLogRetentionDays <= 0 {
		cfg.DoctorLogRetentionDays = 30
	}
	// Clamp DoctorS3ExpireDays: zero or negative would be meaningless. Fall back to 30.
	if cfg.DoctorS3ExpireDays <= 0 {
		cfg.DoctorS3ExpireDays = 30
	}

	// Gap #2b (Phase 84.4.1.1): reject obvious placeholder artifacts_bucket values
	// at load time. Catches angle-bracket tokens only (e.g. "<prefix>-artifacts-12345678").
	//
	// Canonical-shape enforcement (^[a-z][a-z0-9-]*-artifacts-[0-9]{12}$) is intentionally
	// NOT applied here — it lives only at configure time (cmdCanonicalBucketRE in
	// configure.go). Reason: a strict canonical check at Load() breaks legacy installs
	// with pre-Phase-84.3 bucket names (e.g. literal "km-artifacts-12345"), locking the
	// operator out of every km command. See isPlaceholderBucket comment for full history.
	//
	// We validate the yaml-authoritative value (from yamlDefaults) rather than the
	// env-overridden cfg.ArtifactsBucket so that KM_ARTIFACTS_BUCKET env overrides
	// are not blocked.
	bucketToValidate := cfg.ArtifactsBucket
	if yamlVal, ok := yamlDefaults["artifacts_bucket"]; ok {
		bucketToValidate = yamlVal
	}
	if isPlaceholderBucket(bucketToValidate) {
		return nil, fmt.Errorf("artifacts_bucket=%q is a placeholder; re-run `km configure` to derive ${prefix}-artifacts-${account_id} automatically", bucketToValidate)
	}

	return cfg, nil
}

// isPlaceholderBucket reports whether the given artifacts_bucket value is a
// placeholder from km-config.example.yaml that an operator has not replaced.
// Returns true only for angle-bracket tokens (e.g. "<prefix>-artifacts-12345678") —
// those are unambiguously fake.
//
// Phase 84.4-08 UAT removed the prior `name == "km-artifacts-12345"` literal check:
// that name is a real, legitimate bucket on this operator's legacy install
// (predating Phase 84.3's `${prefix}-artifacts-${account_id}` derivation), so
// rejecting it broke `cfg.Load()` and every km command that read the config.
// Anyone with a literal placeholder-shaped name today is genuinely using that
// bucket; treat empty string as "unconfigured", not placeholder.
//
// Returns false for empty string (empty means unconfigured, not placeholder).
// Inline in config.go to avoid cross-package imports from config → cmd.
func isPlaceholderBucket(name string) bool {
	if name == "" {
		return false
	}
	if lt := strings.Index(name, "<"); lt >= 0 {
		if strings.Index(name[lt:], ">") >= 0 {
			return true
		}
	}
	return false
}

// ValidateArtifactsBucket validates the artifacts_bucket value loaded from
// km-config.yaml. Operators may use any S3 bucket name they choose; this
// function only catches obviously-unconfigured values:
//   - empty string returns nil (unconfigured is allowed at Load() time;
//     km configure / km init enforce non-empty separately).
//   - angle-bracket placeholders (any "<…>" token, e.g. the example.yaml
//     "<prefix>-artifacts-<account-id>") return an error.
//
// Lives in the config package to avoid import cycles from config.Load().
func ValidateArtifactsBucket(name string) error {
	if name == "" {
		return nil
	}
	if lt := strings.Index(name, "<"); lt >= 0 {
		if strings.Index(name[lt:], ">") >= 0 {
			return fmt.Errorf("artifacts_bucket=%q is a placeholder; set a real bucket name in km-config.yaml or re-run `km configure`", name)
		}
	}
	return nil
}

// GetResourcePrefix returns the configured resource prefix, falling back to
// "km" when unset. Phase 66 populates this from km-config.yaml; Phase 67
// callers use this helper directly so they remain forward-compatible.
func (c *Config) GetResourcePrefix() string {
	if c == nil || c.ResourcePrefix == "" {
		return "km"
	}
	return c.ResourcePrefix
}

// GetDoctorIgnorePrefixes returns the configured sibling resource_prefix values
// that `km doctor` treats as known (suppresses their cross-install WARNs).
// Returns nil when unset.
func (c *Config) GetDoctorIgnorePrefixes() []string {
	if c == nil {
		return nil
	}
	return c.DoctorIgnorePrefixes
}

// ShouldBuildContainerImages reports whether `km init` should build and push
// the km-sandbox + sidecar container images to ECR. Container images are only
// pulled by the docker and ecs substrates; EC2 sandboxes get raw binaries
// from S3, so EC2-only deployments can disable this and skip ~2–10 min of
// docker buildx + ECR push per init. Defaults to true when unset for back-compat.
func (c *Config) ShouldBuildContainerImages() bool {
	if c == nil || c.ContainerSubstratesEnabled == nil {
		return true
	}
	return *c.ContainerSubstratesEnabled
}

// GetRegionLabel returns the short label for cfg.PrimaryRegion
// (e.g. us-east-1 → use1, ca-central-1 → cac1, ap-southeast-2 → apse2).
// Falls back to "use1" when PrimaryRegion is unset or malformed (<3 parts);
// mirrors pkg/compiler.RegionLabel without importing it (avoids pulling
// compiler into config). Used to suffix regional resource names like the
// platform KMS alias.
func (c *Config) GetRegionLabel() string {
	region := ""
	if c != nil {
		region = c.PrimaryRegion
	}
	if region == "" {
		return "use1"
	}
	parts := strings.Split(region, "-")
	if len(parts) < 3 {
		return region
	}
	areaShort := parts[1]
	switch parts[1] {
	case "east":
		areaShort = "e"
	case "west":
		areaShort = "w"
	case "central":
		areaShort = "c"
	case "south":
		areaShort = "s"
	case "north":
		areaShort = "n"
	case "southeast":
		areaShort = "se"
	case "northeast":
		areaShort = "ne"
	case "northwest":
		areaShort = "nw"
	case "southwest":
		areaShort = "sw"
	}
	return parts[0] + areaShort + parts[2]
}

// GetPlatformKMSAlias returns the KMS key alias used for SSM SecureString
// encryption (sandbox identity keys, GitHub tokens, Slack signing secret, etc.).
// Format: "alias/km-platform-{prefix}-{regionLabel}" — "km" is the hardcoded
// brand namespace, {prefix} is GetResourcePrefix(), {regionLabel} is the short
// region label. The brand-prefix-region structure groups all platform aliases
// under "alias/km-platform-*" for easy filtering, while still distinguishing
// per-install (multi-instance) and per-region keys. Defaults to
// "alias/km-platform-km-use1" when neither prefix nor region is configured.
func (c *Config) GetPlatformKMSAlias() string {
	return "alias/km-platform-" + c.GetResourcePrefix() + "-" + c.GetRegionLabel()
}

// GetEmailDomain returns the full email domain (e.g. "sandboxes.klankermaker.ai").
// Falls back to "sandboxes.klankermaker.ai" when both fields are empty or the receiver
// is nil — mirrors the nil-safety pattern used by GetResourcePrefix.
func (c *Config) GetEmailDomain() string {
	sub := "sandboxes"
	if c != nil && c.EmailSubdomain != "" {
		sub = c.EmailSubdomain
	}
	domain := "klankermaker.ai"
	if c != nil && c.Domain != "" {
		domain = c.Domain
	}
	return sub + "." + domain
}

// GetSsmPrefix returns the SSM parameter path prefix (e.g. "/km/").
// Uses GetResourcePrefix() which handles nil-safety and the "km" default.
func (c *Config) GetSsmPrefix() string {
	return "/" + c.GetResourcePrefix() + "/"
}

// GetSlackThreadsTableName returns the Slack-threads DynamoDB table name.
// If SlackThreadsTableName is explicitly set, that value wins. Otherwise
// the name is derived from GetResourcePrefix() + "-slack-threads", which
// defaults to "km-slack-threads" when no prefix is configured.
func (c *Config) GetSlackThreadsTableName() string {
	if c == nil {
		return "km-slack-threads"
	}
	if c.SlackThreadsTableName != "" {
		return c.SlackThreadsTableName
	}
	return c.GetResourcePrefix() + "-slack-threads"
}

// GetSlackStreamMessagesTableName returns the Slack-stream-messages DynamoDB
// table name (Phase 68 transcript streaming). If SlackStreamMessagesTableName
// is explicitly set, that value wins. Otherwise the name is derived from
// GetResourcePrefix() + "-slack-stream-messages", which defaults to
// "km-slack-stream-messages" when no prefix is configured.
//
// Decision (Plan 68-03 Open Question 1): the suffix is "-slack-stream-messages"
// (NOT "-km-slack-stream-messages"), mirroring Phase 67's "-slack-threads"
// pattern, so the default prefix yields "km-slack-stream-messages" rather
// than "km-km-slack-stream-messages".
func (c *Config) GetSlackStreamMessagesTableName() string {
	if c == nil {
		return "km-slack-stream-messages"
	}
	if c.SlackStreamMessagesTableName != "" {
		return c.SlackStreamMessagesTableName
	}
	return c.GetResourcePrefix() + "-slack-stream-messages"
}

// GetSlackChannelsTableName returns the Slack-channels DynamoDB table name.
// If SlackChannelsTableName is explicitly set, that value wins. Otherwise
// the name is derived from GetResourcePrefix() + "-slack-channels", which
// defaults to "km-slack-channels" when no prefix is configured.
func (c *Config) GetSlackChannelsTableName() string {
	if c == nil {
		return "km-slack-channels"
	}
	if c.SlackChannelsTableName != "" {
		return c.SlackChannelsTableName
	}
	return c.GetResourcePrefix() + "-slack-channels"
}

// GetSandboxSessionDocumentName returns the per-install SSM Session Manager
// document name, e.g. "km-Sandbox-Session", "tg-Sandbox-Session". Mirrors the
// computation in infra/modules/ssm-session-doc/v2.0.0/main.tf (Phase 84.4.1).
//
// Phase 84.4.1: replaces 5 hardcoded "KM-Sandbox-Session" callsites
// (shell.go:500, agent.go:356/430/619, agent_auth.go:157/411). Note the
// lowercase 'k' — v1.0.0 used "KM-Sandbox-Session" (uppercase); v2.0.0 uses
// "${prefix}-Sandbox-Session" with lowercase prefix per the v2.0.0 contract.
func (c *Config) GetSandboxSessionDocumentName() string {
	return c.GetResourcePrefix() + "-Sandbox-Session"
}

// GetSandboxTableName returns the DynamoDB sandboxes table name.
// Derives from GetResourcePrefix() + "-sandboxes", defaulting to "km-sandboxes".
func (c *Config) GetSandboxTableName() string {
	if c == nil {
		return "km-sandboxes"
	}
	if c.SandboxTableName != "" {
		return c.SandboxTableName
	}
	return c.GetResourcePrefix() + "-sandboxes"
}

// GetBudgetTableName returns the DynamoDB budgets table name.
// Derives from GetResourcePrefix() + "-budgets", defaulting to "km-budgets".
func (c *Config) GetBudgetTableName() string {
	if c == nil {
		return "km-budgets"
	}
	if c.BudgetTableName != "" {
		return c.BudgetTableName
	}
	return c.GetResourcePrefix() + "-budgets"
}

// GetIdentityTableName returns the DynamoDB identities table name.
// Derives from GetResourcePrefix() + "-identities", defaulting to "km-identities".
func (c *Config) GetIdentityTableName() string {
	if c == nil {
		return "km-identities"
	}
	if c.IdentityTableName != "" {
		return c.IdentityTableName
	}
	return c.GetResourcePrefix() + "-identities"
}

// GetSchedulesTableName returns the DynamoDB schedules table name.
// Derives from GetResourcePrefix() + "-schedules", defaulting to "km-schedules".
func (c *Config) GetSchedulesTableName() string {
	if c == nil {
		return "km-schedules"
	}
	if c.SchedulesTableName != "" {
		return c.SchedulesTableName
	}
	return c.GetResourcePrefix() + "-schedules"
}

// GetH1Config returns the install-level HackerOne bridge config (Phase 103).
// Absent h1: block => zero-value H1Config (dormant). Callers consume Programs
// for routing and BotHandle/DefaultProfile as install defaults.
func (c *Config) GetH1Config() H1Config {
	return c.H1
}

// GetH1BotHandle returns the install-wide comment-keyword trigger token
// (e.g. "@km"). Empty when unset (dormant).
func (c *Config) GetH1BotHandle() string {
	return c.H1.BotHandle
}

// GetH1DefaultProfile returns the install-wide fallback SandboxProfile path used
// when a matched target has no Profile. Empty when unset.
func (c *Config) GetH1DefaultProfile() string {
	return c.H1.DefaultProfile
}

// GetH1Programs returns the configured HackerOne program routing entries.
// Nil/empty when the h1: block is absent (dormant).
func (c *Config) GetH1Programs() []H1ProgramEntry {
	return c.H1.Programs
}

// GetH1ProgramBotHandle resolves the effective comment-keyword token for a
// program handle: the per-program H1ProgramEntry.BotHandle override when set,
// otherwise the install-wide H1Config.BotHandle. An unknown handle (or one with
// no override) falls back to the install default. This encodes the precedence so
// callers (Plan 06/07) do not re-derive it.
func (c *Config) GetH1ProgramBotHandle(handle string) string {
	for _, p := range c.H1.Programs {
		if p.Handle == handle && p.BotHandle != "" {
			return p.BotHandle
		}
	}
	return c.H1.BotHandle
}

// awsProfileExists checks whether a named AWS profile is defined in
// ~/.aws/config or ~/.aws/credentials.
func awsProfileExists(profile string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	for _, name := range []string{"config", "credentials"} {
		data, err := os.ReadFile(filepath.Join(home, ".aws", name))
		if err != nil {
			continue
		}
		content := string(data)
		// AWS config uses [profile <name>], credentials uses [<name>]
		if strings.Contains(content, "[profile "+profile+"]") ||
			strings.Contains(content, "["+profile+"]") {
			return true
		}
	}
	return false
}
