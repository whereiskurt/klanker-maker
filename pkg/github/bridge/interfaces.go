package bridge

import "context"

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
