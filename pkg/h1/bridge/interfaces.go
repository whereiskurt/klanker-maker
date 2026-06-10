package bridge

import "context"

// This file is the HackerOne fork of pkg/github/bridge/interfaces.go (Phase 103).
//
// KEPT (shape-reuse, identical contract to the GitHub bridge): SecretFetcher,
// DeliveryNonceStore, SandboxAliasResolver(+WithStatus), SandboxResumer,
// SandboxStatusWriter, EventBridgePublisher, SQSSender.
//
// DROPPED (out of scope for H1, per 103-CONTEXT):
//   - PeerRelayer   — federated relay is GitHub-only (HackerOne webhooks point at a
//                     specific install's Function URL; fanout is satisfied by
//                     in-scope multi-target dispatch, not relay).
//   - BotLoginFetcher — HackerOne has no bot user; the trigger is a literal config
//                       handle string (resolve.ContainsHandle), not an @-mention of
//                       a resolved bot login.
//   - GitHubReactor   — HackerOne comments have no 👀 reaction primitive.
//
// FORKED:
//   - GitHubThreadStore → H1ThreadStore  (key = report_id + target, multi-target safe)
//   - CommentPoster     → H1Commenter    (adds the safety-critical `internal bool`)

// SecretFetcher returns the HackerOne webhook signing secret (cached from SSM).
// The X-H1-Signature HMAC-SHA256 verify keys on this secret.
type SecretFetcher interface {
	Fetch(ctx context.Context) (string, error)
}

// DeliveryNonceStore atomically checks and stores X-H1-Delivery GUIDs to prevent
// replay. Reuses the shared {prefix}-slack-bridge-nonces DynamoDB table with an
// "h1-delivery:" key prefix (analog of "github-delivery:"), 24h TTL.
type DeliveryNonceStore interface {
	// CheckAndStore returns (true, nil) if the key was already seen (replay),
	// (false, nil) on first insertion, or (false, err) on storage failure.
	CheckAndStore(ctx context.Context, key string, ttlSeconds int) (alreadySeen bool, err error)
}

// SandboxAliasResolver looks up a sandbox by its alias and returns its
// h1-inbound queue URL. Used by the warm dispatch path.
type SandboxAliasResolver interface {
	// ResolveByAlias returns the sandbox_id for the given alias via the
	// alias-index GSI. Returns an error (sandbox not found) for the cold path.
	ResolveByAlias(ctx context.Context, alias string) (sandboxID string, err error)

	// H1QueueURL returns the h1_inbound_queue_url DDB attribute for the given
	// sandbox_id. Returns an error if the queue URL is absent.
	H1QueueURL(ctx context.Context, sandboxID string) (queueURL string, err error)
}

// SandboxAliasResolverWithStatus extends SandboxAliasResolver with a status-aware
// variant that returns the sandbox status alongside the sandbox_id. This enables
// the unified dispatch decision: resume stopped/paused sandboxes instead of
// cold-creating a duplicate, and cold-create only when the alias is truly absent.
type SandboxAliasResolverWithStatus interface {
	SandboxAliasResolver

	// ResolveByAliasWithStatus returns the sandbox_id and status for the given alias.
	// status "" (attribute absent in DDB) is equivalent to "running" (backward compat).
	// Returns an error (sandbox not found) when the alias does not exist in DDB —
	// the caller treats this as the cold-create trigger.
	ResolveByAliasWithStatus(ctx context.Context, alias string) (sandboxID, status string, err error)
}

// SandboxResumer starts a stopped or paused EC2 sandbox instance. Used when an
// alias resolves to a stopped sandbox — the resumer starts it and the enqueued
// prompt drains once the box boots. Errors are non-fatal (logged); enqueue still
// proceeds so the prompt is not lost.
type SandboxResumer interface {
	// StartSandbox starts all stopped EC2 instances tagged with sandboxID.
	StartSandbox(ctx context.Context, sandboxID string) error
}

// SandboxStatusWriter writes status updates back to the km-sandboxes DynamoDB
// table via UpdateItem (never PutItem — full-row replaces strip un-marshalled
// attributes, the SandboxMetadata lossy round-trip footgun, project_sandboxmetadata_lossy_roundtrip).
type SandboxStatusWriter interface {
	// SetStatusRunning sets status="running" on the km-sandboxes row for the given
	// sandboxID after a successful auto-resume. Non-fatal in the caller.
	SetStatusRunning(ctx context.Context, sandboxID string) error
}

// EventBridgePublisher publishes a SandboxCreate EventBridge event (cold path).
// The bridge fills in alias + profile + h1EnvelopeJSON so the create-handler can
// provision the sandbox and drain the carried envelope into the h1-inbound FIFO
// queue after provisioning.
type EventBridgePublisher interface {
	PutSandboxCreate(ctx context.Context, alias, profile, h1EnvelopeJSON string) error
}

// SQSSender sends a single message to a FIFO SQS queue. Mirrors
// pkg/github/bridge.SQSSender and pkg/slack/bridge.SQSSender exactly.
type SQSSender interface {
	Send(ctx context.Context, queueURL, body, groupID, deduplicationID string) error
}

// H1ThreadStore tracks (reportID, target) → {sandbox_id, agent_session_id, agent_type}
// mappings backed by the {prefix}-h1-threads DynamoDB table. Enables follow-up
// comments in the same report to continue the same agent session, and replies in a
// known thread to bypass the handle-keyword requirement (the GitHub thread-bypass analog).
//
// The key is (reportID, target): multi-target fanout means N targets dispatch on the
// same report, each needing its own continuity row so they do not collide.
//
// agent_type is schema-on-write — old rows without the attribute return "" (treated
// as profile default downstream). No Terraform migration.
//
// All writes MUST be UpdateItem-shaped (never full-row PutItem) to avoid the
// SandboxMetadata lossy round-trip footgun (project_sandboxmetadata_lossy_roundtrip).
type H1ThreadStore interface {
	// LookupSandbox returns the sandbox_id, agent_session_id, and agent_type for
	// (reportID, target). Returns ("", "", "", nil) when the row is absent (first
	// dispatch, not an error). agentType is "" for rows written before agent_type
	// existed (treated as profile default downstream).
	LookupSandbox(ctx context.Context, reportID, target string) (sandboxID, sessionID, agentType string, err error)

	// Upsert creates a new (reportID, target) → sandbox_id row only if one does not
	// already exist (attribute_not_exists condition). ConditionalCheckFailed is
	// treated as idempotent success — the row already exists with valid data.
	Upsert(ctx context.Context, reportID, target, sandboxID string) error

	// UpdateSession sets agent_session_id and agent_type on an existing
	// (reportID, target) row via UpdateItem (never PutItem). agentType="" is valid —
	// it writes an empty string, preserving the attribute for future reads.
	UpdateSession(ctx context.Context, reportID, target, sessionID, agentType string) error

	// InvalidateStaleSession overwrites the sandbox_id and clears agent_session_id on
	// a (reportID, target) row whose stored sandbox_id no longer matches the current
	// live sandbox (box was recreated). Prevents a cross-box --resume that would always
	// fail with "No conversation found" and head-of-line block the FIFO queue. Non-fatal.
	InvalidateStaleSession(ctx context.Context, reportID, target, newSandboxID string) error
}

// H1Commenter posts a comment back to a HackerOne report via the customer API
// (HTTP Basic Auth). The forked-from-GitHub addition is the `internal` flag — the
// safety-critical visibility gate.
//
// SAFETY (103-CONTEXT, the safety-critical part): internal=true posts a HackerOne
// internal comment (visible only to the program team); internal=false posts a
// researcher-visible comment. INTERNAL IS THE DEFAULT at every layer — the public
// path (internal=false) must be explicit and is allowlist-gated by the caller.
// Implementations MUST NOT default an absent flag to public.
type H1Commenter interface {
	// PostComment posts a comment on the given report. When internal is true the
	// comment is a HackerOne internal comment; when false it is researcher-visible.
	// Returns nil on success; errors are logged but do not change the 200 response.
	PostComment(ctx context.Context, reportID, body string, internal bool) error
}
