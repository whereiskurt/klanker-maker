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
	Status      string     `json:"status,omitempty"`        // "starting", "running", "failed"; empty = "running" (backward compat)
	CreatedAt   time.Time  `json:"created_at"`
	TTLExpiry   *time.Time `json:"ttl_expiry,omitempty"`
	IdleTimeout string     `json:"idle_timeout,omitempty"` // e.g. "15m", from profile lifecycle.idleTimeout
	MaxLifetime string     `json:"max_lifetime,omitempty"` // e.g. "72h", from profile lifecycle.maxLifetime; empty = no cap
	CreatedBy   string     `json:"created_by,omitempty"`   // creation method: "cli", "email", "api", "remote"
	Alias       string     `json:"alias,omitempty"`        // human-friendly alias (e.g. "orc", "wrkr-1")
	Locked      bool       `json:"locked,omitempty"`       // true when sandbox is locked by km lock
	LockedAt    *time.Time `json:"locked_at,omitempty"`    // when the sandbox was locked
}
