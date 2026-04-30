// Package aws — metadata.go
// SandboxMetadata is the schema for the JSON object written to
// s3://<state-bucket>/tf-km/sandboxes/<sandbox-id>/metadata.json
// after a successful km create. It is read by km list and km status.
package aws

import "time"

// SandboxMetadata describes a provisioned sandbox.
// Written by km create; read by km list/status without AWS tag API calls.
type SandboxMetadata struct {
	SandboxID   string     `json:"sandbox_id"`
	ProfileName string     `json:"profile_name"`
	Substrate   string     `json:"substrate"`
	Region      string     `json:"region"`
	Status      string     `json:"status,omitempty"`        // "starting", "running", "failed", "killed", "reaped" (spot reclaim); empty = "running" (backward compat)
	CreatedAt   time.Time  `json:"created_at"`
	TTLExpiry   *time.Time `json:"ttl_expiry,omitempty"`
	IdleTimeout string     `json:"idle_timeout,omitempty"` // e.g. "15m", from profile lifecycle.idleTimeout
	MaxLifetime string     `json:"max_lifetime,omitempty"` // e.g. "72h", from profile lifecycle.maxLifetime; empty = no cap
	CreatedBy   string     `json:"created_by,omitempty"`   // creation method: "cli", "email", "api", "remote"
	Alias       string     `json:"alias,omitempty"`        // human-friendly alias (e.g. "orc", "wrkr-1")
	ClonedFrom  string     `json:"cloned_from,omitempty"`  // source sandbox ID if this is a clone
	Locked         bool       `json:"locked,omitempty"`          // true when sandbox is locked by km lock
	LockedAt       *time.Time `json:"locked_at,omitempty"`      // when the sandbox was locked
	TeardownPolicy string     `json:"teardown_policy,omitempty"` // "destroy", "stop", or "retain" — from profile lifecycle
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`     // display-only expiry (always set when TTL configured, not used by DynamoDB native TTL)

	// Phase 63 — Slack notification metadata.
	// SlackChannelID is the Slack channel ID (C...) the sandbox posts to.
	// Empty when notifySlackEnabled was false at create time.
	SlackChannelID string `json:"slack_channel_id,omitempty"`
	// SlackPerSandbox indicates the channel was created exclusively for this
	// sandbox (vs. shared mode). Drives km destroy archive behavior.
	SlackPerSandbox bool `json:"slack_per_sandbox,omitempty"`
}
