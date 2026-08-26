// Package bridge implements the webhook ingress dispatch decision. It is the
// generic analog of pkg/github/bridge and pkg/h1/bridge, minus the pieces those
// need for two-way conversations: there is NO thread store, NO reaction poster,
// and NO comment back-channel, because a webhook source is one-way.
package bridge

import (
	"context"
	"errors"
)

// ErrNoResumableInstance signals that an alias resolved to a DynamoDB row whose
// EC2 instance no longer exists — terminated out from under km. It is TERMINAL:
// the caller deletes the stale row and cold-creates. Transient DescribeInstances
// or StartInstances failures must NOT be wrapped in this, or a blip would delete
// a live sandbox's row. Mirrors the Phase 109 GitHub-bridge sentinel.
var ErrNoResumableInstance = errors.New("webhook-bridge: no resumable instance")

// SecretFetcher reads a source's shared secret from SSM (cached per cold start).
type SecretFetcher interface {
	Fetch(ctx context.Context, ssmPath string) (string, error)
}

// AliasResolver resolves a sandbox alias to its id, status, and inbound queue URL.
type AliasResolver interface {
	// ResolveByAliasWithStatus returns (sandboxID, status, nil) when the alias
	// exists. status "" is treated as absent. An error means the alias is absent
	// and the caller takes the cold path.
	ResolveByAliasWithStatus(ctx context.Context, alias string) (sandboxID, status string, err error)

	// QueueURL returns the webhook_inbound_queue_url attribute for a sandbox.
	QueueURL(ctx context.Context, sandboxID string) (string, error)
}

// QueueSender enqueues an envelope onto a per-sandbox FIFO queue.
type QueueSender interface {
	// Send posts body to queueURL with the given MessageGroupId. The group id is
	// the SANDBOX ID, making delivery fully serial per box: two triage turns
	// racing in one /workspace is a bug, not throughput.
	Send(ctx context.Context, queueURL, groupID, body string) error
}

// Resumer starts a stopped or paused sandbox.
type Resumer interface {
	StartSandbox(ctx context.Context, sandboxID string) error
}

// StatusWriter clears an orphaned sandbox row (UpdateItem/DeleteItem only —
// never PutItem, which strips un-marshalled attributes).
type StatusWriter interface {
	DeleteSandboxRow(ctx context.Context, sandboxID string) error
}

// ColdCreator publishes a SandboxCreate event carrying the expanded prompt.
type ColdCreator interface {
	ColdCreate(ctx context.Context, alias, profile, prompt string) error
}
