// Package main — check_dispatch.go
// Implements the CheckDispatch and CheckRun event handlers for the km check runner
// (Phase 116 Stage B). These handlers are wired into the TTLHandler event switch via
// new "check-dispatch" and "check-run" event types routed BEFORE the sandbox_id guard.
//
// Stage B decision flow (locked in 116-CONTEXT.md):
//   - CheckDispatch event → ResumeOrCreate (pkg/dispatch) with adapters:
//     * AliasResolver: alias-index GSI on km-sandboxes (same pattern as github bridge)
//     * AgentRunSink: handleAgentRun with AutoStart=true (CANONICAL, stale-fork safe)
//     * ColdCreateSink: PutSandboxCreateEvent with created_by="check"
//     * NonceStore: nonces table with "check-trigger:{name}" key
//   - CheckRun event → synchronous lambda:Invoke of {prefix}-check-{name}
//
// The warm path uses handleAgentRun's SSM dispatch — NOT an SQS FIFO — so existing
// sandboxes receive check dispatches without recreate and no bridge is modified.
//
// project_ttl_agent_run_stale_fork: the AgentRunSink delegates to h.handleAgentRun
// (the CANONICAL path that sources /etc/profile.d/*.sh, omits --bare, and uses
// CLAUDE_CODE_OAUTH_TOKEN). Do NOT fork buildAgentRunScript.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	dynamodbpkg "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	lambdapkg "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/rs/zerolog/log"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	compilerpkg "github.com/whereiskurt/klanker-maker/pkg/compiler"
	"github.com/whereiskurt/klanker-maker/pkg/dispatch"
)

// ============================================================
// Test-seam interfaces (injectable in unit tests)
// ============================================================

// checkAliasResolver is the test seam for alias resolution. Satisfies dispatch.AliasResolver.
type checkAliasResolver interface {
	ResolveByAliasWithStatus(ctx context.Context, alias string) (sandboxID, status string, err error)
}

// checkAgentRunSink is the test seam for the warm dispatch. Satisfies dispatch.AgentRunSink.
type checkAgentRunSink interface {
	DispatchAgentRun(ctx context.Context, sandboxID, prompt string) error
}

// checkColdCreateSink is the test seam for cold-create. Satisfies dispatch.ColdCreateSink.
type checkColdCreateSink interface {
	ColdCreate(ctx context.Context, alias, profile, prompt string) error
}

// checkNonceStore is the test seam for cooldown nonces. Satisfies dispatch.NonceStore.
type checkNonceStore interface {
	CheckAndStore(ctx context.Context, key string, ttlSeconds int) (alreadySeen bool, err error)
}

// checkLambdaInvoker is the test seam for lambda:Invoke (check-run).
type checkLambdaInvoker interface {
	InvokeCheckLambda(ctx context.Context, functionName string) error
}

// ============================================================
// ttlAliasResolver — dispatch.AliasResolver backed by km-sandboxes alias-index GSI
// ============================================================

// dynamoAliasQueryClient is the narrow DynamoDB interface needed by ttlAliasResolver.
type dynamoAliasQueryClient interface {
	Query(ctx context.Context, params *dynamodbpkg.QueryInput, optFns ...func(*dynamodbpkg.Options)) (*dynamodbpkg.QueryOutput, error)
}

// ttlAliasResolver implements dispatch.AliasResolver using the km-sandboxes alias-index GSI.
// Mirrors DynamoAliasResolver.ResolveByAliasWithStatus from pkg/github/bridge/aws_adapters.go
// (not imported directly to avoid coupling cmd/ttl-handler to the bridge package).
type ttlAliasResolver struct {
	client    dynamoAliasQueryClient
	tableName string
}

func (r *ttlAliasResolver) ResolveByAliasWithStatus(ctx context.Context, alias string) (string, string, error) {
	out, err := r.client.Query(ctx, &dynamodbpkg.QueryInput{
		TableName:              awssdk.String(r.tableName),
		IndexName:              awssdk.String("alias-index"),
		KeyConditionExpression: awssdk.String("alias = :alias"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":alias": &dynamodbtypes.AttributeValueMemberS{Value: alias},
		},
		Limit: awssdk.Int32(2), // fetch 2 to detect duplicates
	})
	if err != nil {
		return "", "", fmt.Errorf("check-dispatch: resolve alias %q via GSI: %w", alias, err)
	}
	if len(out.Items) == 0 {
		return "", "", fmt.Errorf("check-dispatch: alias %q not found", alias)
	}
	if len(out.Items) > 1 {
		return "", "", fmt.Errorf("check-dispatch: alias %q is ambiguous (multiple rows matched)", alias)
	}

	item := out.Items[0]
	sv, ok := item["sandbox_id"]
	if !ok {
		return "", "", fmt.Errorf("check-dispatch: alias %q: GSI item missing sandbox_id", alias)
	}
	s, ok := sv.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		return "", "", fmt.Errorf("check-dispatch: alias %q: sandbox_id not a String", alias)
	}
	sandboxID := s.Value

	// status attribute — absent means "running" (backward compat, mirrors bridge).
	status := ""
	if statV, ok2 := item["status"]; ok2 {
		if sv2, ok3 := statV.(*dynamodbtypes.AttributeValueMemberS); ok3 {
			status = sv2.Value
		}
	}
	return sandboxID, status, nil
}

// ============================================================
// ttlAgentRunSink — dispatch.AgentRunSink (delegates to canonical handleAgentRun)
// ============================================================

// ttlAgentRunSink implements dispatch.AgentRunSink by building a TTLEvent{agent-run}
// and delegating to h.handleAgentRun. This reuses the canonical SSM-dispatch path
// (buildAgentRunScript) — already fixed for the stale-fork bugs documented in
// project_ttl_agent_run_stale_fork: sources /etc/profile.d/*.sh, omits --bare,
// uses CLAUDE_CODE_OAUTH_TOKEN for no-bedrock OAuth. Do NOT fork this.
type ttlAgentRunSink struct {
	handler *TTLHandler
}

func (s *ttlAgentRunSink) DispatchAgentRun(ctx context.Context, sandboxID, prompt string) error {
	// AutoStart=true so handleAgentRun resumes a stopped/paused box before dispatching.
	agentEvent := TTLEvent{
		SandboxID: sandboxID,
		EventType: "agent-run",
		Prompt:    prompt,
		AutoStart: true,
	}
	return s.handler.handleAgentRun(ctx, agentEvent)
}

// ============================================================
// ttlColdCreateSink — dispatch.ColdCreateSink (PutSandboxCreateEvent via EventBridge)
// ============================================================

// checkEBClient is the narrow EventBridge PutEvents interface for cold-create.
type checkEBClient interface {
	PutEvents(ctx context.Context, params *eventbridge.PutEventsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error)
}

// ttlColdCreateSink implements dispatch.ColdCreateSink by emitting a SandboxCreate
// EventBridge event. The create-handler Lambda provisions the box and the prompt is
// carried in the detail so the box-side poller delivers it after boot.
type ttlColdCreateSink struct {
	client         checkEBClient
	artifactBucket string
}

func (s *ttlColdCreateSink) ColdCreate(ctx context.Context, alias, profile, prompt string) error {
	sandboxID := compilerpkg.GenerateSandboxID("chk")

	// artifact_prefix follows the "github-profiles/{slug}" convention used by the bridge.
	// The create-handler appends "/.km-profile.yaml" to locate the profile YAML.
	artifactPrefix := "check-profiles/" + checkProfileSlug(profile)

	detail := awspkg.SandboxCreateDetail{
		SandboxID:      sandboxID,
		ArtifactBucket: s.artifactBucket,
		ArtifactPrefix: artifactPrefix,
		Alias:          alias,
		CreatedBy:      "check",
	}
	detailBytes, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("check-dispatch: cold-create: marshal detail: %w", err)
	}
	out, err := s.client.PutEvents(ctx, &eventbridge.PutEventsInput{
		Entries: []ebtypes.PutEventsRequestEntry{
			{
				Source:     awssdk.String("km.sandbox"),
				DetailType: awssdk.String("SandboxCreate"),
				Detail:     awssdk.String(string(detailBytes)),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("check-dispatch: cold-create: EventBridge PutEvents: %w", err)
	}
	if out.FailedEntryCount > 0 {
		return fmt.Errorf("check-dispatch: cold-create: EventBridge PutEvents: %d entries failed", out.FailedEntryCount)
	}
	return nil
}

// checkProfileSlug normalises a profile name/path into a directory-safe slug.
// Mirrors bridge/aws_adapters.go profileSlug (defined locally to avoid import coupling).
func checkProfileSlug(profile string) string {
	base := profile
	if i := strings.LastIndexAny(profile, "/\\"); i >= 0 {
		base = profile[i+1:]
	}
	lc := strings.ToLower(base)
	for _, ext := range []string{".yaml", ".yml"} {
		if strings.HasSuffix(lc, ext) {
			base = base[:len(base)-len(ext)]
			break
		}
	}
	return strings.ToLower(base)
}

// ============================================================
// ttlNonceStore — dispatch.NonceStore backed by the shared nonces DynamoDB table
// ============================================================

// dynamoNonceClient is the narrow DynamoDB interface needed by ttlNonceStore.
type dynamoNonceClient interface {
	PutItem(ctx context.Context, params *dynamodbpkg.PutItemInput, optFns ...func(*dynamodbpkg.Options)) (*dynamodbpkg.PutItemOutput, error)
}

// ttlNonceStore implements dispatch.NonceStore using the shared nonces DynamoDB table.
// Key pattern: "check-trigger:{name}". TTL via DynamoDB ttl_expiry attribute.
// Mirrors DynamoGitHubNonceStore.CheckAndStore from pkg/github/bridge/aws_adapters.go.
type ttlNonceStore struct {
	client    dynamoNonceClient
	tableName string
}

func (s *ttlNonceStore) CheckAndStore(ctx context.Context, key string, ttlSeconds int) (bool, error) {
	ttlExpiry := time.Now().Unix() + int64(ttlSeconds)

	_, err := s.client.PutItem(ctx, &dynamodbpkg.PutItemInput{
		TableName: awssdk.String(s.tableName),
		Item: map[string]dynamodbtypes.AttributeValue{
			"nonce": &dynamodbtypes.AttributeValueMemberS{Value: key},
			"ttl_expiry": &dynamodbtypes.AttributeValueMemberN{
				Value: strconv.FormatInt(ttlExpiry, 10),
			},
		},
		ConditionExpression: awssdk.String("attribute_not_exists(nonce)"),
	})
	if err != nil {
		var condFailed *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &condFailed) {
			return true, nil // already seen — cooldown active
		}
		return false, fmt.Errorf("check-dispatch: nonce store: %w", err)
	}
	return false, nil
}

// nonceTableName returns the nonces DynamoDB table name from env.
// The shared nonces table is "{prefix}-slack-bridge-nonces" per the existing convention.
func nonceTableName() string {
	if v := os.Getenv("KM_NONCE_TABLE"); v != "" {
		return v
	}
	return resourcePrefix() + "-slack-bridge-nonces"
}

// ============================================================
// ttlLambdaInvoker — checkLambdaInvoker (lambda:Invoke for check-run)
// ============================================================

// lambdaInvokeClient is the narrow Lambda interface needed by ttlLambdaInvoker.
type lambdaInvokeClient interface {
	Invoke(ctx context.Context, params *lambdapkg.InvokeInput, optFns ...func(*lambdapkg.Options)) (*lambdapkg.InvokeOutput, error)
}

// ttlLambdaInvoker implements checkLambdaInvoker using a real lambda:Invoke.
type ttlLambdaInvoker struct {
	client lambdaInvokeClient
}

func (i *ttlLambdaInvoker) InvokeCheckLambda(ctx context.Context, functionName string) error {
	out, err := i.client.Invoke(ctx, &lambdapkg.InvokeInput{
		FunctionName: awssdk.String(functionName),
		// Default InvocationType is RequestResponse (synchronous).
	})
	if err != nil {
		return fmt.Errorf("check-run: invoke %s: %w", functionName, err)
	}
	// FunctionError is non-nil when the Lambda function itself returned an error.
	if out.FunctionError != nil {
		return fmt.Errorf("check-run: function %s errored: %s", functionName, awssdk.ToString(out.FunctionError))
	}
	return nil
}

// ============================================================
// zerologSlogHandler — bridges zerolog to slog for pkg/dispatch
// ============================================================

// zerologSlogHandler adapts the package-level zerolog logger to slog.Handler
// so pkg/dispatch.ResumeOrCreate can emit structured log lines via zerolog.
type zerologSlogHandler struct{}

func (*zerologSlogHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (*zerologSlogHandler) Handle(_ context.Context, r slog.Record) error {
	ev := log.Debug()
	switch r.Level {
	case slog.LevelWarn:
		ev = log.Warn()
	case slog.LevelInfo:
		ev = log.Info()
	case slog.LevelError:
		ev = log.Error()
	}
	r.Attrs(func(a slog.Attr) bool {
		ev = ev.Str(a.Key, fmt.Sprintf("%v", a.Value.Any()))
		return true
	})
	ev.Msg(r.Message)
	return nil
}

func (*zerologSlogHandler) WithAttrs(_ []slog.Attr) slog.Handler { return &zerologSlogHandler{} }
func (*zerologSlogHandler) WithGroup(_ string) slog.Handler      { return &zerologSlogHandler{} }

// newDispatchLogger returns a *slog.Logger backed by the package-level zerolog logger.
func newDispatchLogger() *slog.Logger {
	return slog.New(&zerologSlogHandler{})
}

// ============================================================
// Local-to-pkg/dispatch bridge adapters
// ============================================================

// dispatchAliasResolverAdapter bridges checkAliasResolver → dispatch.AliasResolver.
type dispatchAliasResolverAdapter struct{ inner checkAliasResolver }

func (a *dispatchAliasResolverAdapter) ResolveByAliasWithStatus(ctx context.Context, alias string) (string, string, error) {
	return a.inner.ResolveByAliasWithStatus(ctx, alias)
}

// dispatchAgentRunSinkAdapter bridges checkAgentRunSink → dispatch.AgentRunSink.
type dispatchAgentRunSinkAdapter struct{ inner checkAgentRunSink }

func (a *dispatchAgentRunSinkAdapter) DispatchAgentRun(ctx context.Context, sandboxID, prompt string) error {
	return a.inner.DispatchAgentRun(ctx, sandboxID, prompt)
}

// dispatchColdCreateSinkAdapter bridges checkColdCreateSink → dispatch.ColdCreateSink.
type dispatchColdCreateSinkAdapter struct{ inner checkColdCreateSink }

func (a *dispatchColdCreateSinkAdapter) ColdCreate(ctx context.Context, alias, profile, prompt string) error {
	return a.inner.ColdCreate(ctx, alias, profile, prompt)
}

// dispatchNonceStoreAdapter bridges checkNonceStore → dispatch.NonceStore.
type dispatchNonceStoreAdapter struct{ inner checkNonceStore }

func (a *dispatchNonceStoreAdapter) CheckAndStore(ctx context.Context, key string, ttlSeconds int) (bool, error) {
	return a.inner.CheckAndStore(ctx, key, ttlSeconds)
}

// ============================================================
// handleCheckDispatch — Stage B event handler
// ============================================================

// handleCheckDispatch handles a "check-dispatch" TTLEvent emitted by the check Lambda
// bootstrap (Stage A) when the when_py predicate is truthy.
//
// Cross-plan contract (Plan 116-04 → 116-06, LOCKED):
//
//	event.CheckName      — check name (cooldown key + logging)
//	event.Alias          — sandbox alias to resume-or-cold-create
//	event.Prompt         — expanded prompt text (template already evaluated in Stage A)
//	event.ProfileName    — SandboxProfile name for cold-create (may be "" for warm-only)
//	event.OnAbsent       — "cold-create" (default) or "skip"
//	event.CooldownSeconds — 0 = no cooldown; >0 = suppress within window via nonces table
//	event.Reason         — reason string from Stage A predicate (for logging)
//
// The warm path delegates to h.handleAgentRun (AutoStart=true) — the CANONICAL path.
// No SQS FIFO is introduced; existing sandboxes need no recreate (bridges unmodified).
func (h *TTLHandler) handleCheckDispatch(ctx context.Context, event TTLEvent) error {
	return h.handleCheckDispatchWithAdapters(ctx, event, nil, nil, nil, nil)
}

// handleCheckDispatchWithAdapters is the testable version: nil adapters are replaced
// with real production implementations.
func (h *TTLHandler) handleCheckDispatchWithAdapters(
	ctx context.Context,
	event TTLEvent,
	resolver checkAliasResolver,
	agentRun checkAgentRunSink,
	cold checkColdCreateSink,
	nonces checkNonceStore,
) error {
	log.Info().
		Str("check_name", event.CheckName).
		Str("alias", event.Alias).
		Str("on_absent", event.OnAbsent).
		Int("cooldown_seconds", event.CooldownSeconds).
		Str("reason", event.Reason).
		Msg("check-dispatch event received")

	if event.CheckName == "" {
		return fmt.Errorf("check-dispatch: check_name is required")
	}
	if event.Alias == "" {
		return fmt.Errorf("check-dispatch: alias is required")
	}
	if event.Prompt == "" {
		return fmt.Errorf("check-dispatch: prompt is required")
	}

	// Build production adapters for any that are nil (test seams inject mocks).
	if resolver == nil || agentRun == nil || cold == nil || nonces == nil {
		awsCfg, err := awspkg.LoadAWSConfig(ctx, os.Getenv("KM_AWS_PROFILE"))
		if err != nil {
			return fmt.Errorf("check-dispatch: load AWS config: %w", err)
		}
		dynamoClient := dynamodbpkg.NewFromConfig(awsCfg)

		if resolver == nil {
			resolver = &ttlAliasResolver{
				client:    dynamoClient,
				tableName: h.SandboxTableName,
			}
		}
		if agentRun == nil {
			agentRun = &ttlAgentRunSink{handler: h}
		}
		if cold == nil {
			cold = &ttlColdCreateSink{
				client:         eventbridge.NewFromConfig(awsCfg),
				artifactBucket: h.Bucket,
			}
		}
		if nonces == nil {
			nonces = &ttlNonceStore{
				client:    dynamoClient,
				tableName: nonceTableName(),
			}
		}
	}

	// Call pkg/dispatch.ResumeOrCreate with bridged adapters.
	return dispatch.ResumeOrCreate(
		ctx,
		event.CheckName,
		event.Alias,
		event.Prompt,
		event.ProfileName,
		event.OnAbsent,
		event.CooldownSeconds,
		&dispatchAliasResolverAdapter{resolver},
		&dispatchAgentRunSinkAdapter{agentRun},
		&dispatchColdCreateSinkAdapter{cold},
		&dispatchNonceStoreAdapter{nonces},
		newDispatchLogger(),
	)
}

// ============================================================
// handleCheckRun — one-shot check Lambda invoke (km at '...' check run <name>)
// ============================================================

// handleCheckRun handles a "check-run" TTLEvent emitted by EventBridge Scheduler.
// It synchronously invokes the {prefix}-check-{name} Lambda (Stage A).
func (h *TTLHandler) handleCheckRun(ctx context.Context, event TTLEvent) error {
	return h.handleCheckRunWithInvoker(ctx, event, nil)
}

// handleCheckRunWithInvoker is the testable version: nil invoker → real lambda:Invoke.
func (h *TTLHandler) handleCheckRunWithInvoker(ctx context.Context, event TTLEvent, invoker checkLambdaInvoker) error {
	log.Info().Str("check_name", event.CheckName).Msg("check-run event received")

	if event.CheckName == "" {
		return fmt.Errorf("check-run: check_name is required (set check field in TTLEvent)")
	}

	functionName := resourcePrefix() + "-check-" + event.CheckName

	if invoker == nil {
		awsCfg, err := awspkg.LoadAWSConfig(ctx, os.Getenv("KM_AWS_PROFILE"))
		if err != nil {
			return fmt.Errorf("check-run: load AWS config: %w", err)
		}
		invoker = &ttlLambdaInvoker{client: lambdapkg.NewFromConfig(awsCfg)}
	}

	log.Info().Str("function", functionName).Msg("invoking check Lambda synchronously")
	if err := invoker.InvokeCheckLambda(ctx, functionName); err != nil {
		return fmt.Errorf("check-run: %w", err)
	}
	log.Info().
		Str("check_name", event.CheckName).
		Str("function", functionName).
		Msg("check Lambda invoked successfully")
	return nil
}
