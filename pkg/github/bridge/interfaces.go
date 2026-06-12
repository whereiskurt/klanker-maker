package bridge

import "context"

// PeerClaimResult is one peer's verdict on a relayed webhook. Claimed=true means
// the peer owns the repo and handled it (OR is unreachable/legacy — rollout safety
// treats those as claimed to avoid a false orphan). Claimed=false means the peer
// explicitly does not own the repo.
//
// NO Channels field: the GitHub orphan reply has no repo list to return (unlike
// the Slack Phase-96 analog which lists running sandbox channels).
type PeerClaimResult struct {
	PeerURL string // for logging
	Claimed bool   // true = peer owns repo (or legacy/error/timeout — conservative)
}

// PeerRelayer broadcasts an unowned webhook to sibling github-bridge installs.
// Phase 101: claim-aware scatter-gather (the Phase-96 Slack analog, MINUS Channels).
// Broadcast returns one PeerClaimResult per peer so the caller can detect true
// orphan repos (zero claims from any peer).
//
// Rollout safety (GH-ORPHAN-ROLLOUT, LOCKED): any transport error, non-2xx status,
// OR unparseable/legacy body is treated as Claimed:true — never produce a false orphan
// in a mixed-version fleet. Only an explicit {"claimed":false} counts as unclaimed.
type PeerRelayer interface {
	// Broadcast POSTs the verbatim webhook body to every configured peer bridge
	// in parallel, forwarding the GitHub auth/routing headers plus X-KM-Relayed:1.
	// It is synchronous (waits for all POSTs / the bounded context) so the Lambda
	// runtime is not frozen with in-flight goroutines. Empty peer list ⇒ (nil, nil).
	// Returns one PeerClaimResult per peer; errors are non-fatal (tallied Claimed:true).
	Broadcast(ctx context.Context, rawBody []byte, ghHeaders map[string]string) ([]PeerClaimResult, error)
}

// SandboxResumer starts a stopped or paused EC2 sandbox instance.
// Used by WebhookHandler when an alias resolves to a stopped sandbox — the
// resumer starts it and the enqueued prompt drains once the box boots.
// Errors are non-fatal (logged); enqueue still proceeds so the prompt is not lost.
type SandboxResumer interface {
	// StartSandbox starts all stopped EC2 instances tagged with sandboxID.
	// Returns nil when at least one instance was started, or a descriptive
	// error that will be logged non-fatally by the caller.
	StartSandbox(ctx context.Context, sandboxID string) error
}

// SandboxStatusWriter writes status updates back to the km-sandboxes DynamoDB
// table via UpdateItem (never PutItem — full-row replaces strip un-marshalled
// attributes, the SandboxMetadata lossy round-trip footgun).
// Used by WebhookHandler to flip status=running after a successful auto-resume
// so km list / km resume reflect the current state and a follow-up @-mention
// takes the warm path instead of firing a redundant StartInstances.
type SandboxStatusWriter interface {
	// SetStatusRunning sets status="running" on the km-sandboxes row for the
	// given sandboxID. Non-fatal in the caller — errors are logged but do not
	// abort the enqueue or change the 200 response.
	SetStatusRunning(ctx context.Context, sandboxID string) error

	// DeleteSandboxRow removes the km-sandboxes row for sandboxID. Used to clear an
	// orphaned alias row (status=stopped, instance gone) so the alias becomes absent
	// and the subsequent cold-create does not trip the ambiguous-alias guard
	// (ResolveByAliasWithStatus rejects an alias matching more than one row).
	// Non-fatal in the caller — a delete failure is logged; the cold-create still fires.
	DeleteSandboxRow(ctx context.Context, sandboxID string) error
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

// SecretFetcher returns the GitHub webhook signing secret (cached from SSM).
type SecretFetcher interface {
	Fetch(ctx context.Context) (string, error)
}

// BotLoginFetcher returns the bot's GitHub login name (e.g. "klanker-maker[bot]").
// Used for loop guard and mention detection. Cached after first fetch.
type BotLoginFetcher interface {
	Fetch(ctx context.Context) (string, error)
}

// DeliveryNonceStore atomically checks and stores X-GitHub-Delivery GUIDs
// to prevent replay attacks. Reuses the km-slack-bridge-nonces DynamoDB table
// with "github-delivery:" key prefix.
type DeliveryNonceStore interface {
	// CheckAndStore returns (true, nil) if the key was already seen (replay),
	// (false, nil) on first insertion, or (false, err) on storage failure.
	CheckAndStore(ctx context.Context, key string, ttlSeconds int) (alreadySeen bool, err error)
}

// SandboxAliasResolver looks up a sandbox by its alias and returns its
// github-inbound queue URL. Used by the warm dispatch path.
type SandboxAliasResolver interface {
	// ResolveByAlias returns the sandbox_id for the given alias via the
	// alias-index GSI. Returns an error (sandbox not found) for the cold path.
	ResolveByAlias(ctx context.Context, alias string) (sandboxID string, err error)

	// GitHubQueueURL returns the github_inbound_queue_url DDB attribute
	// for the given sandbox_id. Returns an error if the queue URL is absent.
	GitHubQueueURL(ctx context.Context, sandboxID string) (queueURL string, err error)
}

// EventBridgePublisher publishes a SandboxCreate EventBridge event (cold path).
// The bridge fills in alias + profile + githubEnvelopeJSON so the create-handler
// can provision the sandbox and drain the carried envelope into the github-inbound
// FIFO queue after provisioning.
type EventBridgePublisher interface {
	PutSandboxCreate(ctx context.Context, alias, profile, githubEnvelopeJSON string) error
}

// SQSSender sends a single message to a FIFO SQS queue.
// Mirrors pkg/slack/bridge.SQSSender exactly.
type SQSSender interface {
	Send(ctx context.Context, queueURL, body, groupID, deduplicationID string) error
}

// GitHubReactor posts a reaction emoji to a GitHub issue comment.
// Used by WebhookHandler to ACK inbound comments with 👀.
// Implementations MUST treat already-reacted as idempotent success (return nil).
type GitHubReactor interface {
	// AddReaction posts a reaction to a comment on the given repo.
	// content is the emoji keyword, e.g. "eyes".
	AddReaction(ctx context.Context, installationID, owner, repo string, commentID int64, content string) error
}

// CommentPoster posts a text comment on a GitHub issue or pull request.
// Used by WebhookHandler for multi-command errors, command-not-authorized denials,
// and /help replies. Uses the same installation token mint pattern as GitHubReactor.
type CommentPoster interface {
	// PostComment posts a text comment on the issue/PR with the given number in
	// the named owner/repo. installationID is the GitHub App installation ID string.
	// Returns nil on success; errors are logged but do not change the 200 response.
	PostComment(ctx context.Context, installationID, owner, repo string, issueNumber int, body string) error
}

// CommandsFetcher fetches the command set from SSM (or a cache layer). Used by
// WebhookHandler to allow per-invocation cache refresh without a full cold start.
// The concrete implementation (SSMCommandsFetcher) caches for 15 minutes.
type CommandsFetcher interface {
	// Fetch returns the current command map and the install-wide default_command.
	// Returns an empty (non-nil) map and "" when the SSM parameter is absent
	// (dormant — not an error). Callers treat an empty map identically to a nil
	// map: the command pass is skipped.
	Fetch(ctx context.Context) (map[string]CommandEntry, string, error)
}

// GitHubThreadStore tracks (repo, number) → {sandbox_id, agent_session_id, agent_type}
// mappings backed by the km-github-threads DynamoDB table. Enables follow-up @-mentions
// in the same PR/issue to continue the same agent session (GH-X-CONTINUITY) and allows
// replies in a known thread to bypass the re-@-mention requirement (GH-X-THREADBYPASS).
//
// Phase 102: agent_type is schema-on-write — old rows without the attribute return ""
// (treated as profile default downstream). No Terraform change, no migration.
//
// Continuity data lives ONLY in km-github-threads — never in km-sandboxes — to avoid
// the SandboxMetadata lossy round-trip footgun.
type GitHubThreadStore interface {
	// LookupSandbox returns the sandbox_id, agent_session_id, and agent_type for
	// (repo, number). Returns ("", "", "", nil) when the row is absent (first
	// dispatch, not an error). agent_type is "" for pre-Phase-102 rows (treated as
	// profile default downstream).
	LookupSandbox(ctx context.Context, repo string, number int) (sandboxID, sessionID, agentType string, err error)

	// Upsert creates a new (repo, number) → sandbox_id row only if one does not
	// already exist (attribute_not_exists condition). ConditionalCheckFailed is
	// treated as idempotent success — the row already exists with valid data.
	Upsert(ctx context.Context, repo string, number int, sandboxID string) error

	// UpdateSession sets agent_session_id and agent_type on an existing (repo, number)
	// row via UpdateItem (never PutItem — avoids the SandboxMetadata lossy round-trip
	// footgun). Called by the poller after each agent turn completes.
	// agentType="" is valid — it writes an empty string, preserving the attribute for
	// future reads (downstream treats "" as profile default).
	UpdateSession(ctx context.Context, repo string, number int, sessionID, agentType string) error

	// InvalidateStaleSession overwrites the sandbox_id and clears agent_session_id
	// on a (repo, number) row whose stored sandbox_id no longer matches the current
	// live sandbox (box was recreated). This prevents a cross-box --resume that would
	// always fail with "No conversation found" and head-of-line block the FIFO queue.
	//
	// Gap E fix (98-06): called by Handle() when LookupSandbox returns a sandbox_id
	// that differs from the alias-resolved current sandbox. Non-fatal — errors are
	// logged but do not abort dispatch.
	InvalidateStaleSession(ctx context.Context, repo string, number int, newSandboxID string) error
}
