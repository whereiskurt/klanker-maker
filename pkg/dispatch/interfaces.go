// Package dispatch provides the shared alias-resolution + resume/cold-create decision
// logic for the km check runner (Phase 116). It factors out the 3-way dispatch
// decision (exists → agent-run, absent + onAbsent=cold-create → cold-create,
// absent + onAbsent=skip → drop) from the per-bridge handlers into a single
// testable package.
//
// The warm/resume terminal action is SSM agent-run dispatch (AgentRunSink), NOT
// an SQS FIFO enqueue — so existing sandboxes receive check dispatches without
// recreate, and the GitHub/H1/Slack bridges are NOT modified by this package.
//
// Cooldown is enforced via the shared nonces table (key "check-trigger:{name}").
package dispatch

import "context"

// AliasResolver resolves a sandbox alias to its sandbox ID and current status.
// The warm dispatch decision is: err==nil && status!="" → sandbox exists (any
// non-terminated state); err!=nil → alias absent → fall through to onAbsent.
//
// Concrete implementation: pkg/github/bridge/aws_adapters.go DynamoAliasResolver,
// which reads the km-sandboxes alias-index GSI and the `status` DDB attribute
// (NOT `state` — see project memory project_slack_bridge_inbound_e2e_and_status_attr).
type AliasResolver interface {
	// ResolveByAliasWithStatus returns the sandbox_id and status for the given alias.
	// status "" (attribute absent in DDB) is equivalent to "running" (backward compat).
	// Returns an error when the alias does not exist — treated as the absent trigger.
	ResolveByAliasWithStatus(ctx context.Context, alias string) (sandboxID, status string, err error)
}

// AgentRunSink dispatches a prompt to an existing (or auto-resuming) sandbox via
// SSM SendCommand. This is the WARM path terminal action for pkg/dispatch.
//
// The sink owns auto-resume mechanics (stopped/paused instance start + SSM dispatch),
// mirroring ttl-handler handleAgentRun with AutoStart=true. The dispatch decision
// in ResumeOrCreate is simply "sandbox exists → agent-run"; the sink handles all
// EC2/SSM plumbing so pkg/dispatch stays free of AWS SDK dependencies.
type AgentRunSink interface {
	// DispatchAgentRun fires the prompt to the sandbox via SSM.
	// The sink is responsible for auto-resuming stopped/paused instances before
	// dispatching. sandboxID is the km-sandboxes row key (km:sandbox-id tag value).
	DispatchAgentRun(ctx context.Context, sandboxID, prompt string) error
}

// ColdCreateSink cold-creates a new sandbox carrying the prompt envelope.
// This is the ABSENT path terminal action when onAbsent == "cold-create".
//
// Concrete implementation mirrors pkg/github/bridge/aws_adapters.go
// EventBridgeAdapter.PutSandboxCreate — emits a SandboxCreate EventBridge event
// so the create-handler Lambda provisions the sandbox and dispatches the prompt
// once the box boots.
type ColdCreateSink interface {
	// ColdCreate provisions a new sandbox for the given alias using the named profile.
	// prompt is carried in the envelope so the box-side poller delivers it after boot.
	ColdCreate(ctx context.Context, alias, profile, prompt string) error
}

// NonceStore atomically checks and stores a key with a TTL to enforce cooldown
// windows. Reuses the km-slack-bridge-nonces DynamoDB table with the
// "check-trigger:{name}" key prefix.
//
// Concrete implementation: pkg/github/bridge/aws_adapters.go DynamoGitHubNonceStore
// (same PutItem + ConditionExpression pattern; see RESEARCH §6 for the snippet).
type NonceStore interface {
	// CheckAndStore returns (true, nil) if the key was already seen within the TTL
	// window (suppress the trigger), (false, nil) on first insertion (proceed),
	// or (false, err) on storage failure (caller fail-opens — never strand a real fire).
	CheckAndStore(ctx context.Context, key string, ttlSeconds int) (alreadySeen bool, err error)
}
