// Package bridge — aws_adapters.go
// Production-backed implementations of the bridge interfaces (interfaces.go).
// These adapters wire real AWS services (DynamoDB for nonce/rate/alias lookup,
// SSM for the per-source shared secret, SQS for webhook-inbound queue delivery,
// EventBridge for cold sandbox create) into the Handler used by the
// km-webhook-bridge Lambda.
//
// Design mirrors pkg/github/bridge/aws_adapters.go and pkg/h1/bridge/aws_adapters.go
// closely. Key differences (this bridge is one-way — no thread store, no
// reaction/comment poster):
//   - SSMSecretFetcher: takes the SSM path PER CALL (Fetch(ctx, ssmPath)), not a
//     fixed struct field — a webhook install can have many sources, each with its
//     own secret path, sharing one cold-start cache keyed by path.
//   - DynamoWebhookNonceStore: shared nonces table, no key-prefixing needed (the
//     webhook package already prefixes replay/cooldown/rate keys distinctly).
//   - DynamoRateCounter: the one genuinely new adapter — an atomic ADD backing
//     the install-wide rate ceiling (pkg/webhook.RateCounter).
//   - DynamoAliasResolver: queries alias-index GSI + reads webhook_inbound_queue_url
//     (not github_inbound_queue_url / h1's equivalent).
//   - WebhookSQSAdapter: mirrors GitHubSQSAdapter/H1SQSAdapter for webhook-inbound
//     FIFO queues, but bridge.QueueSender.Send has no caller-supplied dedup id
//     (Task 6's interface signature), so the adapter derives one from the body.
//   - EventBridgeAdapter: publishes a SandboxCreate event carrying the ALREADY
//     EXPANDED prompt (Handler.dispatch expands the template before calling
//     ColdCreate) via the shared pkg/aws.SandboxCreateDetail/PutSandboxCreateEvent
//     helpers — mirrors cmd/ttl-handler/check_dispatch.go's ttlColdCreateSink,
//     which is the more recent (Phase 116) convention for this exact shape.
package bridge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/webhook"
)

// Compile-time checks: every adapter satisfies the interface it's meant to back.
var (
	_ SecretFetcher       = (*SSMSecretFetcher)(nil)
	_ webhook.NonceStore  = (*DynamoWebhookNonceStore)(nil)
	_ webhook.RateCounter = (*DynamoRateCounter)(nil)
	_ AliasResolver       = (*DynamoAliasResolver)(nil)
	_ QueueSender         = (*WebhookSQSAdapter)(nil)
	_ ColdCreator         = (*EventBridgeAdapter)(nil)
	_ Resumer             = (*EC2Resumer)(nil)
	_ StatusWriter        = (*DynamoSandboxStatusWriter)(nil)
	_ ActionLimitsFetcher = (*DDBActionLimitsFetcher)(nil)
	_ Freezer             = (*DynamoFreezer)(nil)
)

// ============================================================
// Narrow interfaces for adapters
// ============================================================

// SecretSSMClient is the minimal SSM interface used by SSMSecretFetcher.
type SecretSSMClient interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// DynamoQueryPutter is the minimal DynamoDB interface needed by the nonce store
// and the alias resolver.
type DynamoQueryPutter interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	Query(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// DynamoUpdateItemClient is the minimal DynamoDB interface required by
// DynamoSandboxStatusWriter (DeleteItem, for clearing an orphaned row — Phase 109
// pattern) and DynamoRateCounter (UpdateItem, for the atomic ADD counter).
type DynamoUpdateItemClient interface {
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

// SQSClient is the narrow SQS interface required by WebhookSQSAdapter.
type SQSClient interface {
	SendMessage(ctx context.Context, params *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// EC2StartAPI is the narrow EC2 interface required by EC2Resumer.
type EC2StartAPI interface {
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
}

// ============================================================
// SSMSecretFetcher — per-source shared secret, cached per SSM path
// ============================================================

// cachedValue holds one cached string value with an expiry time.
type cachedValue struct {
	value  string
	expiry time.Time
}

// SSMSecretFetcher implements bridge.SecretFetcher by reading and caching each
// source's shared secret from SSM. Unlike the GitHub/H1 bridges (one secret path
// per Lambda, fixed at cold start), a webhook install can configure many sources
// each with a different secret_path, so the path is a Fetch() parameter and the
// cache is keyed by path.
type SSMSecretFetcher struct {
	Client   SecretSSMClient
	CacheTTL time.Duration // defaults to 15 minutes

	mu    sync.Mutex
	cache map[string]cachedValue
}

func (f *SSMSecretFetcher) Fetch(ctx context.Context, ssmPath string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ttl := f.CacheTTL
	if ttl == 0 {
		ttl = 15 * time.Minute
	}
	if f.cache == nil {
		f.cache = make(map[string]cachedValue)
	}
	if cv, ok := f.cache[ssmPath]; ok && cv.value != "" && time.Now().Before(cv.expiry) {
		return cv.value, nil
	}

	out, err := f.Client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(ssmPath),
		WithDecryption: awssdk.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("webhook-bridge: fetch secret from SSM %s: %w", ssmPath, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("webhook-bridge: SSM parameter %s has no value", ssmPath)
	}
	v := *out.Parameter.Value
	f.cache[ssmPath] = cachedValue{value: v, expiry: time.Now().Add(ttl)}
	return v, nil
}

// ============================================================
// DynamoWebhookNonceStore — webhook.NonceStore backed by the shared nonces table
// ============================================================

// DynamoWebhookNonceStore implements webhook.NonceStore using the same shared
// nonces DynamoDB table as the other bridges. Unlike DynamoGitHubNonceStore, keys
// are used verbatim (no extra prefix) — pkg/webhook already prefixes replay keys
// ("wh:"), cooldown keys ("wh-cd:"), and rate keys ("wh-rate:") distinctly.
type DynamoWebhookNonceStore struct {
	Client    DynamoQueryPutter
	TableName string // e.g. "km-slack-bridge-nonces" (shared nonces table)
}

// CheckAndStore returns (true, nil) if the key was already stored (replay or
// cooldown-suppressed), (false, nil) on first insertion, or (false, err) on
// storage failure. TTL is applied via the ttl_expiry attribute.
func (s *DynamoWebhookNonceStore) CheckAndStore(ctx context.Context, key string, ttlSeconds int) (bool, error) {
	ttlExpiry := time.Now().Unix() + int64(ttlSeconds)

	_, err := s.Client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: awssdk.String(s.TableName),
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
			return true, nil // already seen — replay/cooldown suppressed
		}
		return false, fmt.Errorf("webhook-bridge: nonce store: %w", err)
	}
	return false, nil
}

// ============================================================
// DynamoRateCounter — webhook.RateCounter backed by an atomic ADD
// ============================================================

// DynamoRateCounter implements webhook.RateCounter with an atomic ADD on the
// shared nonces table. The bucket row carries a TTL so it self-reaps.
type DynamoRateCounter struct {
	Client    DynamoUpdateItemClient
	TableName string
}

func (c *DynamoRateCounter) Increment(ctx context.Context, key string, ttlSeconds int) (int64, error) {
	expiry := time.Now().Unix() + int64(ttlSeconds)
	out, err := c.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(c.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"nonce": &dynamodbtypes.AttributeValueMemberS{Value: key},
		},
		UpdateExpression: awssdk.String("ADD hit_count :one SET ttl_expiry = if_not_exists(ttl_expiry, :exp)"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":one": &dynamodbtypes.AttributeValueMemberN{Value: "1"},
			":exp": &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(expiry, 10)},
		},
		ReturnValues: dynamodbtypes.ReturnValueUpdatedNew,
	})
	if err != nil {
		return 0, fmt.Errorf("webhook-bridge: rate counter: %w", err)
	}
	n, ok := out.Attributes["hit_count"].(*dynamodbtypes.AttributeValueMemberN)
	if !ok {
		return 0, fmt.Errorf("webhook-bridge: rate counter: missing hit_count")
	}
	return strconv.ParseInt(n.Value, 10, 64)
}

// ============================================================
// DynamoAliasResolver — alias-index GSI lookup + webhook queue URL fetch
// ============================================================

// DynamoAliasResolver implements bridge.AliasResolver by querying the
// alias-index GSI on km-sandboxes and reading the webhook_inbound_queue_url
// attribute for the warm-dispatch path.
type DynamoAliasResolver struct {
	Client    DynamoQueryPutter
	TableName string // e.g. "km-sandboxes"
}

// ResolveByAliasWithStatus queries the alias-index GSI for the sandbox_id and
// status of the sandbox with the given alias. Returns an error if no sandbox
// exists (the caller treats this as the cold-create trigger).
// status="" (attribute absent in DDB) is equivalent to "running" (backward compat).
func (r *DynamoAliasResolver) ResolveByAliasWithStatus(ctx context.Context, alias string) (string, string, error) {
	out, err := r.Client.Query(ctx, &dynamodb.QueryInput{
		TableName:              awssdk.String(r.TableName),
		IndexName:              awssdk.String("alias-index"),
		KeyConditionExpression: awssdk.String("alias = :alias"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":alias": &dynamodbtypes.AttributeValueMemberS{Value: alias},
		},
		Limit: awssdk.Int32(2), // fetch 2 to detect duplicates
	})
	if err != nil {
		return "", "", fmt.Errorf("webhook-bridge: resolve alias %q via GSI: %w", alias, err)
	}
	if len(out.Items) == 0 {
		return "", "", fmt.Errorf("webhook-bridge: alias %q not found", alias)
	}
	if len(out.Items) > 1 {
		return "", "", fmt.Errorf("webhook-bridge: alias %q is ambiguous (matched multiple sandboxes)", alias)
	}

	item := out.Items[0]
	sv, ok := item["sandbox_id"]
	if !ok {
		return "", "", fmt.Errorf("webhook-bridge: alias %q: GSI item missing sandbox_id", alias)
	}
	s, ok := sv.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		return "", "", fmt.Errorf("webhook-bridge: alias %q: sandbox_id not a String", alias)
	}
	sandboxID := s.Value

	// status is optional — absent means "running" (backward compat with rows created
	// before the status field was introduced).
	status := ""
	if statV, ok := item["status"]; ok {
		if sv2, ok := statV.(*dynamodbtypes.AttributeValueMemberS); ok {
			status = sv2.Value
		}
	}
	return sandboxID, status, nil
}

// QueueURL fetches the webhook_inbound_queue_url attribute from the sandbox's
// km-sandboxes row. Returns an error if absent (queue not provisioned).
func (r *DynamoAliasResolver) QueueURL(ctx context.Context, sandboxID string) (string, error) {
	out, err := r.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(r.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		ProjectionExpression: awssdk.String("webhook_inbound_queue_url"),
	})
	if err != nil {
		return "", fmt.Errorf("webhook-bridge: fetch webhook queue URL for %s: %w", sandboxID, err)
	}
	if len(out.Item) == 0 {
		return "", fmt.Errorf("webhook-bridge: sandbox %s not found in table", sandboxID)
	}
	if v, ok := out.Item["webhook_inbound_queue_url"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok && sv.Value != "" {
			return sv.Value, nil
		}
	}
	return "", fmt.Errorf("webhook-bridge: sandbox %s has no webhook_inbound_queue_url (inbound not provisioned)", sandboxID)
}

// ============================================================
// WebhookSQSAdapter — bridge.QueueSender for webhook-inbound FIFO queues
// ============================================================

// WebhookSQSAdapter implements bridge.QueueSender by sending to a webhook-inbound
// FIFO queue. The webhook-inbound queue has content_based_deduplication=false
// (infra/modules/sqs-inbound-dlq), and bridge.QueueSender.Send has no
// caller-supplied dedup id (unlike the GitHub/H1 SQSSender interfaces), so the
// adapter derives MessageDeduplicationId from a SHA-256 of the body — a retried
// Send with byte-identical content naturally coalesces within SQS's 5-minute
// dedup window, while distinct payloads (distinct envelope/prompt) always get
// distinct ids.
type WebhookSQSAdapter struct {
	Client SQSClient
}

// Send posts body to queueURL with the given MessageGroupId. groupID is the
// SANDBOX ID (see bridge.QueueSender doc), making delivery strictly serial per box.
func (a *WebhookSQSAdapter) Send(ctx context.Context, queueURL, groupID, body string) error {
	sum := sha256.Sum256([]byte(body))
	dedupID := hex.EncodeToString(sum[:])

	_, err := a.Client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               awssdk.String(queueURL),
		MessageBody:            awssdk.String(body),
		MessageGroupId:         awssdk.String(groupID),
		MessageDeduplicationId: awssdk.String(dedupID),
	})
	if err != nil {
		return fmt.Errorf("webhook-bridge: SQS SendMessage to %s: %w", queueURL, err)
	}
	return nil
}

// ============================================================
// EventBridgeAdapter — bridge.ColdCreator, publishes SandboxCreate for cold path
// ============================================================

// generateWebhookSandboxID returns a new unique sandbox ID in the form "wh-" + 8
// lowercase hex characters (e.g. "wh-a1b2c3d4"). Mirrors compiler.GenerateSandboxID
// semantics but is defined locally to avoid depending on pkg/compiler from this
// package (mirrors the same choice made in pkg/github/bridge and pkg/h1/bridge).
func generateWebhookSandboxID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failures are exceedingly rare; fail loudly rather than
		// propagate through a value-returning function with no error return.
		panic(fmt.Sprintf("webhook-bridge: generateWebhookSandboxID: rand.Read: %v", err))
	}
	return "wh-" + hex.EncodeToString(b)
}

// profileSlug normalises a profile name/path into a directory-safe slug.
// Mirrors pkg/github/bridge/aws_adapters.go's profileSlug.
func profileSlug(profile string) string {
	base := filepath.Base(profile)
	for _, ext := range []string{".yaml", ".yml"} {
		if strings.HasSuffix(strings.ToLower(base), ext) {
			base = base[:len(base)-len(ext)]
			break
		}
	}
	return strings.ToLower(base)
}

// EventBridgeAdapter implements bridge.ColdCreator by emitting a SandboxCreate
// event carrying the alias, profile (via the artifact_prefix convention), and the
// already-expanded prompt (Handler.dispatch expands {{field}} templates before
// calling ColdCreate — unlike the GitHub/H1 bridges, there is no raw envelope for
// the create-handler to re-expand later).
type EventBridgeAdapter struct {
	Client         kmaws.EventBridgeAPI
	ArtifactBucket string // required by SandboxCreateDetail
	ArtifactPrefix string // reserved for forward-compat; unused (see ArtifactPrefix construction below)
}

// ColdCreate publishes a SandboxCreate event. profile is stored in artifact_prefix
// ("webhook-profiles/{slug}") so the create-handler knows which profile YAML to
// use; sandbox_id is generated here (not by the create-handler) for determinism.
func (a *EventBridgeAdapter) ColdCreate(ctx context.Context, alias, profile, prompt string) error {
	sandboxID := generateWebhookSandboxID()
	detail := kmaws.SandboxCreateDetail{
		SandboxID:      sandboxID,
		ArtifactBucket: a.ArtifactBucket,
		ArtifactPrefix: "webhook-profiles/" + profileSlug(profile),
		Alias:          alias,
		CreatedBy:      "webhook",
		Prompt:         prompt,
	}
	if err := kmaws.PutSandboxCreateEvent(ctx, a.Client, detail); err != nil {
		return fmt.Errorf("webhook-bridge: cold-create: %w", err)
	}
	return nil
}

// ============================================================
// EC2Resumer — bridge.Resumer, starts stopped EC2 sandbox instances
// ============================================================

// EC2Resumer implements bridge.Resumer by finding stopped EC2 instances tagged
// with the km sandbox-id tag and starting them. Mirrors
// pkg/github/bridge/aws_adapters.go's EC2Resumer (Phase 109 stopping-poll logic
// included verbatim — the same race applies to any bridge that can catch a box
// mid-pause).
type EC2Resumer struct {
	Client          EC2StartAPI
	SandboxIDTagKey string // e.g. "km:sandbox-id"; default when empty
	ResourcePrefix  string // INERT: retained for wiring compat, no longer read
}

func (r *EC2Resumer) sandboxIDTagKey() string {
	if r.SandboxIDTagKey != "" {
		return r.SandboxIDTagKey
	}
	// km ALWAYS tags sandbox instances "km:sandbox-id" regardless of resource_prefix
	// (project_sandbox_id_tag_always_km_namespace). Mirror the CLI resume path
	// (internal/app/cmd/resume.go) which hardcodes "tag:km:sandbox-id".
	return "km:sandbox-id"
}

// StartSandbox finds stopped (or stopping) EC2 instances tagged with the km
// sandbox-id tag equal to sandboxID and calls StartInstances on them. Returns
// nil when at least one instance was started, or an error describing the
// failure — wrapping ErrNoResumableInstance when the instance is genuinely gone
// (terminal; the caller falls back to cold-create) as opposed to a transient
// Describe/Start API error (not wrapped; caller retains log-and-enqueue retry).
func (r *EC2Resumer) StartSandbox(ctx context.Context, sandboxID string) error {
	tagKey := r.sandboxIDTagKey()

	descOut, err := r.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("tag:" + tagKey), Values: []string{sandboxID}},
			{Name: awssdk.String("instance-state-name"), Values: []string{"stopped", "stopping"}},
		},
	})
	if err != nil {
		return fmt.Errorf("webhook-bridge: EC2Resumer.DescribeInstances for %s: %w", sandboxID, err)
	}

	type foundInst struct {
		id       string
		stopping bool
	}
	var found []foundInst
	for _, res := range descOut.Reservations {
		for _, inst := range res.Instances {
			if inst.InstanceId == nil || *inst.InstanceId == "" {
				continue
			}
			isStopping := inst.State != nil &&
				inst.State.Name == ec2types.InstanceStateNameStopping
			found = append(found, foundInst{id: *inst.InstanceId, stopping: isStopping})
		}
	}
	if len(found) == 0 {
		// Terminal: the instance is gone (orphaned alias row). Wrap the sentinel so
		// the caller can branch with errors.Is and fall back to cold-create.
		return fmt.Errorf("webhook-bridge: no stopped/stopping EC2 instances found for sandbox %s (tag %s): %w",
			sandboxID, tagKey, ErrNoResumableInstance)
	}

	const stoppingPollInterval = 2 * time.Second
	const stoppingPollTimeout = 8 * time.Second

	allStopping := true
	for _, fi := range found {
		if !fi.stopping {
			allStopping = false
			break
		}
	}

	if allStopping {
		deadline := time.Now().Add(stoppingPollTimeout)
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				goto doStart
			case <-time.After(stoppingPollInterval):
			}
			rePoll, pollErr := r.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
				Filters: []ec2types.Filter{
					{Name: awssdk.String("tag:" + tagKey), Values: []string{sandboxID}},
					{Name: awssdk.String("instance-state-name"), Values: []string{"stopped"}},
				},
			})
			if pollErr != nil {
				continue
			}
			var stoppedNow []foundInst
			for _, res := range rePoll.Reservations {
				for _, inst := range res.Instances {
					if inst.InstanceId != nil && *inst.InstanceId != "" {
						stoppedNow = append(stoppedNow, foundInst{id: *inst.InstanceId})
					}
				}
			}
			if len(stoppedNow) > 0 {
				found = stoppedNow
				break
			}
		}
	}

doStart:
	var instanceIDs []string
	for _, fi := range found {
		instanceIDs = append(instanceIDs, fi.id)
	}

	if _, err := r.Client.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: instanceIDs,
	}); err != nil {
		return fmt.Errorf("webhook-bridge: EC2Resumer.StartInstances for %s: %w", sandboxID, err)
	}
	return nil
}

// ============================================================
// DynamoSandboxStatusWriter — bridge.StatusWriter backed by km-sandboxes
// ============================================================

// DynamoSandboxStatusWriter implements bridge.StatusWriter. Only DeleteItem is
// used (clearing an orphaned status=stopped row whose EC2 instance is gone —
// Phase 109 pattern) — never a full-row PutItem, which would strip attributes
// not present in a partial struct.
type DynamoSandboxStatusWriter struct {
	Client    DynamoUpdateItemClient
	TableName string // e.g. "km-sandboxes"
}

// DeleteSandboxRow removes the km-sandboxes row for sandboxID via a single
// DeleteItem keyed by sandbox_id, so a stale alias resolves as absent and the
// subsequent cold-create does not trip the ambiguous-alias guard.
func (w *DynamoSandboxStatusWriter) DeleteSandboxRow(ctx context.Context, sandboxID string) error {
	_, err := w.Client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: awssdk.String(w.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
	})
	if err != nil {
		return fmt.Errorf("webhook-bridge: delete stale sandbox row %s: %w", sandboxID, err)
	}
	return nil
}

// ============================================================
// DDBActionLimitsFetcher — per-sandbox action_limits resolver (Task 9A)
// ============================================================

// webhookGetItemAPI is the minimal GetItem surface DDBActionLimitsFetcher
// needs. *dynamodb.Client satisfies it.
type webhookGetItemAPI interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
}

// DDBActionLimitsFetcher implements bridge.ActionLimitsFetcher by reading the
// action_limits JSON string from the km-sandboxes row (GetItem keyed by
// sandbox_id). Wired into Handler.Limits in cmd/km-webhook-bridge/main.go. An
// absent row or absent action_limits attr returns "" (dormant — the quota gate
// then no-ops). Mirrors pkg/h1/bridge.DDBActionLimitsFetcher.
type DDBActionLimitsFetcher struct {
	Client    webhookGetItemAPI // *dynamodb.Client satisfies this
	TableName string            // e.g. "km-sandboxes"
}

// FetchLimits returns the action_limits JSON for sandboxID, or "" when the row
// or attr is absent. Only a GetItem transport error is surfaced.
func (f *DDBActionLimitsFetcher) FetchLimits(ctx context.Context, sandboxID string) (string, error) {
	out, err := f.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(f.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		ProjectionExpression: awssdk.String("action_limits"),
	})
	if err != nil {
		return "", fmt.Errorf("webhook-bridge: action_limits lookup for %s: %w", sandboxID, err)
	}
	if out == nil || out.Item == nil {
		return "", nil
	}
	if v, ok := out.Item["action_limits"].(*dynamodbtypes.AttributeValueMemberS); ok {
		return v.Value, nil
	}
	return "", nil
}

// ============================================================
// DynamoFreezer — auto-freeze adapter (Task 9A)
// ============================================================

// DynamoFreezer implements bridge.Freezer by calling kmaws.FreezeSandboxDynamo
// on the km-sandboxes table. Wired into Handler.Freezer in
// cmd/km-webhook-bridge/main.go. Mirrors pkg/h1/bridge.DynamoFreezer.
type DynamoFreezer struct {
	Client DynamoUpdateItemClient // *dynamodb.Client satisfies this
	Table  string                 // e.g. "km-sandboxes"
}

// webhookUpdateOnlyMetaClient adapts DynamoUpdateItemClient to
// kmaws.SandboxMetadataAPI. Only UpdateItem is exercised by
// FreezeSandboxDynamo; the remaining methods panic to make any accidental
// call loud.
type webhookUpdateOnlyMetaClient struct{ c DynamoUpdateItemClient }

func (a webhookUpdateOnlyMetaClient) UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return a.c.UpdateItem(ctx, in, opts...)
}
func (a webhookUpdateOnlyMetaClient) GetItem(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	panic("webhookUpdateOnlyMetaClient: GetItem not implemented")
}
func (a webhookUpdateOnlyMetaClient) PutItem(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	panic("webhookUpdateOnlyMetaClient: PutItem not implemented")
}
func (a webhookUpdateOnlyMetaClient) DeleteItem(_ context.Context, _ *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	panic("webhookUpdateOnlyMetaClient: DeleteItem not implemented")
}
func (a webhookUpdateOnlyMetaClient) Scan(_ context.Context, _ *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	panic("webhookUpdateOnlyMetaClient: Scan not implemented")
}
func (a webhookUpdateOnlyMetaClient) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	panic("webhookUpdateOnlyMetaClient: Query not implemented")
}

// FreezeSandbox latches action_frozen=true on the sandbox's km-sandboxes row.
// by should be "auto:<action>:<window>" for auto-on-breach freezes.
func (f *DynamoFreezer) FreezeSandbox(ctx context.Context, sandboxID, reason, by string) error {
	return kmaws.FreezeSandboxDynamo(ctx, webhookUpdateOnlyMetaClient{f.Client}, f.Table, sandboxID, reason, by)
}
