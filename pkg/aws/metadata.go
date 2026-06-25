// Package aws — metadata.go
// SandboxMetadata is the schema for the JSON object written to
// s3://<state-bucket>/tf-km/sandboxes/<sandbox-id>/metadata.json
// after a successful km create. It is read by km list and km status.
package aws

import "time"

// SandboxMetadata describes a provisioned sandbox.
// Written by km create; read by km list/status without AWS tag API calls.
type SandboxMetadata struct {
	SandboxID      string     `json:"sandbox_id"`
	ProfileName    string     `json:"profile_name"`
	Substrate      string     `json:"substrate"`
	Region         string     `json:"region"`
	Status         string     `json:"status,omitempty"` // "starting", "running", "failed", "killed", "reaped" (spot reclaim); empty = "running" (backward compat)
	CreatedAt      time.Time  `json:"created_at"`
	TTLExpiry      *time.Time `json:"ttl_expiry,omitempty"`
	IdleTimeout    string     `json:"idle_timeout,omitempty"`    // e.g. "15m", from profile lifecycle.idleTimeout
	MaxLifetime    string     `json:"max_lifetime,omitempty"`    // e.g. "72h", from profile lifecycle.maxLifetime; empty = no cap
	CreatedBy      string     `json:"created_by,omitempty"`      // creation method: "cli", "email", "api", "remote"
	Alias          string     `json:"alias,omitempty"`           // human-friendly alias (e.g. "orc", "wrkr-1")
	ClonedFrom     string     `json:"cloned_from,omitempty"`     // source sandbox ID if this is a clone
	Locked         bool       `json:"locked,omitempty"`          // true when sandbox is locked by km lock
	LockedAt       *time.Time `json:"locked_at,omitempty"`       // when the sandbox was locked
	TeardownPolicy string     `json:"teardown_policy,omitempty"` // "destroy", "stop", or "retain" — from profile lifecycle
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`      // display-only expiry (always set when TTL configured, not used by DynamoDB native TTL)

	// Phase 63 — Slack notification metadata.
	// SlackChannelID is the Slack channel ID (C...) the sandbox posts to.
	// Empty when notifySlackEnabled was false at create time.
	SlackChannelID string `json:"slack_channel_id,omitempty"`
	// SlackPerSandbox indicates the channel was created exclusively for this
	// sandbox (vs. shared mode). Drives km destroy archive behavior.
	SlackPerSandbox bool `json:"slack_per_sandbox,omitempty"`

	// SlackArchiveOnDestroy controls whether km destroy archives the per-sandbox
	// Slack channel at teardown. nil = default (archive); &true = archive;
	// &false = preserve for audit trail. Only meaningful when SlackPerSandbox=true.
	// Persisted at km create so km destroy is self-contained (Plan 63-08 sets it;
	// Plan 63-09 reads it at destroy time).
	SlackArchiveOnDestroy *bool `json:"slack_archive_on_destroy,omitempty"`

	// Phase 67 — Slack-inbound metadata.
	// SlackInboundQueueURL is the SQS FIFO queue URL for inbound Slack messages.
	// Empty when notifySlackInboundEnabled was false at create time.
	// Populated by provisionSlackInboundQueue (Plan 67-06); read by drainSlackInbound (Plan 67-07).
	SlackInboundQueueURL string `json:"slack_inbound_queue_url,omitempty"`

	// Phase 97 — GitHub inbound metadata.
	// GithubInboundQueueURL is the SQS FIFO queue URL for inbound GitHub comment-trigger events.
	// Empty when notification.github.inbound.enabled was false/absent at create time (dormant invariant).
	// Populated by provisionGitHubInboundQueue (Plan 97-03); read by the GitHub poller (Plan 97-05).
	// MUST round-trip through marshal/unmarshal: every read-modify-write path (resume.go, extend.go,
	// ttl-handler Lambda) PutItems the whole row — dropping this field reverts the sandbox to
	// dormant on the next lifecycle write (project_sandboxmetadata_lossy_roundtrip footgun).
	GithubInboundQueueURL string `json:"github_inbound_queue_url,omitempty"`

	// Phase 91.5 — per-sandbox Slack inbound overrides. Written at km create by
	// create_slack_inbound.go ONLY when the profile sets the field explicitly;
	// read by the bridge's FetchByChannel. Tri-state via *bool: nil = "fall back
	// to the install-level default" (KM_SLACK_MENTION_ONLY / KM_SLACK_REACT_ALWAYS),
	// &true / &false = explicit per-sandbox override.
	//
	// These MUST round-trip through marshal/unmarshal: every read-modify-write
	// path (resume.go TTL recreation, extend.go, the ttl-handler Lambda) PutItems
	// the whole row, so a struct that drops them silently reverts the sandbox to
	// install defaults on the next lifecycle write — observed as a sandbox losing
	// `mentionOnly: true` (and gaining 👀-on-everything) after a pause/resume.
	SlackMentionOnly *bool `json:"slack_mention_only,omitempty"`
	SlackReactAlways *bool `json:"slack_react_always,omitempty"`
	// Phase 118: per-sandbox trigger allowlist. When non-nil/non-empty, only
	// messages from listed Slack user IDs are dispatched; overrides the install-level
	// EventsHandler.Allow. Stored in DDB as a comma-joined S attribute "slack_allow"
	// (NOT SS — the same pattern as other string fields). nil/empty = fall back to the
	// install-level KM_SLACK_ALLOW (or everyone when that is also empty).
	//
	// MUST round-trip through marshal/unmarshal: every read-modify-write path (resume.go
	// TTL recreation, extend.go, the ttl-handler Lambda) PutItems the whole row — dropping
	// this field reverts the sandbox to install defaults (SandboxMetadata lossy round-trip
	// footgun documented in project_sandboxmetadata_lossy_roundtrip).
	SlackAllow []string `json:"slack_allow,omitempty"`

	// Phase 77 — failure discoverability. Set by the create-handler on failure
	// (see UpdateSandboxStatusAndReasonDynamo). Reads as zero-value on records
	// that never failed.
	FailureReason string     `json:"failure_reason,omitempty"`
	FailedAt      *time.Time `json:"failed_at,omitempty"`
}
