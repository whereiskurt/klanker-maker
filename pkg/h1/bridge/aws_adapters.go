// Package bridge — aws_adapters.go
//
// Production-backed implementations of the H1 bridge interfaces, forked from
// pkg/github/bridge/aws_adapters.go (Phase 103). The stateless, product-agnostic
// adapters (alias resolve, SQS, EventBridge cold-create, EC2 resume, status
// write-back, SSM secret/commands fetch) are thin H1-named wrappers — same shape
// as the GitHub bridge, swapped log/error prefixes and the h1_inbound_queue_url
// attribute name. We keep H1-named wrappers (rather than editing the github
// package) so the 6 shipped GitHub-bridge phases stay uncoupled.
//
// The genuinely-forked adapters:
//   - DynamoH1NonceStore  — DeliveryNonceStore with the "h1-delivery:" key prefix.
//   - DynamoH1ThreadStore — H1ThreadStore keyed (report_id, target), UpdateItem-shaped.
//   - H1APICommenter      — H1Commenter for the synchronous internal "on it" ACK only
//     (replies-to-researcher are posted by the SANDBOX helper,
//     NOT the bridge — the bridge holds Basic-Auth creds purely
//     for the internal ACK path).
package bridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// ============================================================
// Narrow interfaces for adapters
// ============================================================

// SecretSSMClient is the minimal SSM interface used by the secret/commands fetchers.
type SecretSSMClient interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// DynamoQueryPutter is the minimal DynamoDB interface needed by the adapters.
type DynamoQueryPutter interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	Query(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// DynamoH1ThreadClient is a superset of DynamoQueryPutter that adds UpdateItem,
// required by DynamoH1ThreadStore.UpdateSession / InvalidateStaleSession.
type DynamoH1ThreadClient interface {
	DynamoQueryPutter
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

// DynamoUpdateItemClient is the minimal DynamoDB interface required by
// DynamoSandboxStatusWriter: UpdateItem (flip status=running after a successful
// auto-resume) and DeleteItem (Phase 109: clear an orphaned status=stopped row whose
// EC2 instance is gone, so the alias becomes absent for cold-create).
type DynamoUpdateItemClient interface {
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

// ErrNonceReplayed is returned conceptually when the delivery GUID was already seen.
var ErrNonceReplayed = errors.New("h1-bridge: delivery GUID already seen (replay)")

// ErrNoResumableInstance is returned (wrapped) by EC2Resumer.StartSandbox when no
// stopped/stopping EC2 instance exists for the sandbox. The instance is gone — an
// orphaned alias row (status=stopped, instance terminated out from under km). The
// caller branches on errors.Is to fall back to cold-create rather than enqueue to a
// dead per-sandbox queue. A transient DescribeInstances/StartInstances API error is
// deliberately NOT wrapped with this sentinel (it must retain log-and-enqueue retry).
var ErrNoResumableInstance = errors.New("h1-bridge: no resumable EC2 instance")

const (
	// H1DeliveryNoncePrefix isolates X-H1-Delivery GUIDs in the shared nonces
	// table (analog of "github-delivery:"). The handler prepends it before calling
	// DeliveryNonceStore.CheckAndStore.
	H1DeliveryNoncePrefix = "h1-delivery:"
	// H1DeliveryNonceTTLSeconds is the TTL for delivery-GUID dedup entries (24h),
	// covering HackerOne's redelivery window.
	H1DeliveryNonceTTLSeconds = 86400
)

// ============================================================
// SSMSecretFetcher — webhook signing secret
// ============================================================

// cachedValue holds one cached string value with an expiry time.
type cachedValue struct {
	value  string
	expiry time.Time
}

// SSMSecretFetcher fetches and caches the HackerOne webhook signing secret from SSM.
// The 15-minute cache avoids redundant SSM calls on warm Lambda invocations.
type SSMSecretFetcher struct {
	Client   SecretSSMClient
	Path     string        // e.g. "/{prefix}/config/h1/webhook-secret"
	CacheTTL time.Duration // defaults to 15 minutes

	mu    sync.Mutex
	cache cachedValue
}

// Fetch returns the cached secret or reads it from SSM (SecureString).
func (f *SSMSecretFetcher) Fetch(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ttl := f.CacheTTL
	if ttl == 0 {
		ttl = 15 * time.Minute
	}
	if f.cache.value != "" && time.Now().Before(f.cache.expiry) {
		return f.cache.value, nil
	}

	out, err := f.Client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(f.Path),
		WithDecryption: awssdk.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("h1-bridge: fetch webhook secret from SSM %s: %w", f.Path, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("h1-bridge: SSM parameter %s has no value", f.Path)
	}
	v := *out.Parameter.Value
	f.cache = cachedValue{value: v, expiry: time.Now().Add(ttl)}
	return v, nil
}

// Compile-time check.
var _ SecretFetcher = (*SSMSecretFetcher)(nil)

// ============================================================
// DynamoH1NonceStore — DeliveryNonceStore backed by DynamoDB
// ============================================================

// DynamoH1NonceStore implements DeliveryNonceStore using the same shared nonces
// DynamoDB table as the Slack/GitHub bridges. Keys are prefixed "h1-delivery:" by
// the handler (H1DeliveryNoncePrefix) to isolate them from Slack/GitHub entries.
type DynamoH1NonceStore struct {
	Client    DynamoQueryPutter
	TableName string // e.g. "km-slack-bridge-nonces" (shared nonces table)
}

// CheckAndStore returns (true, nil) if the key was already stored (replay),
// (false, nil) on first insertion, or (false, err) on storage failure. TTL is
// applied via the ttl_expiry attribute.
func (s *DynamoH1NonceStore) CheckAndStore(ctx context.Context, key string, ttlSeconds int) (bool, error) {
	ttlExpiry := time.Now().Unix() + int64(ttlSeconds)

	_, err := s.Client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: awssdk.String(s.TableName),
		Item: map[string]dynamodbtypes.AttributeValue{
			"nonce":      &dynamodbtypes.AttributeValueMemberS{Value: key},
			"ttl_expiry": &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(ttlExpiry, 10)},
		},
		ConditionExpression: awssdk.String("attribute_not_exists(nonce)"),
	})
	if err != nil {
		var condFailed *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &condFailed) {
			return true, nil // already seen — replay
		}
		return false, fmt.Errorf("h1-bridge: nonce store: %w", err)
	}
	return false, nil
}

// Compile-time check.
var _ DeliveryNonceStore = (*DynamoH1NonceStore)(nil)

// ============================================================
// DynamoAliasResolver — alias-index GSI lookup + h1 queue URL fetch
// ============================================================

// DynamoAliasResolver implements SandboxAliasResolver(+WithStatus) by querying the
// alias-index GSI on km-sandboxes and reading the h1_inbound_queue_url attribute.
type DynamoAliasResolver struct {
	Client    DynamoQueryPutter
	TableName string // e.g. "km-sandboxes"
}

// ResolveByAlias queries the alias-index GSI for the sandbox_id of the alias.
func (r *DynamoAliasResolver) ResolveByAlias(ctx context.Context, alias string) (string, error) {
	sandboxID, _, err := r.ResolveByAliasWithStatus(ctx, alias)
	return sandboxID, err
}

// ResolveByAliasWithStatus queries the alias-index GSI for sandbox_id + status.
// status="" (attribute absent) is equivalent to "running" (backward compat).
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
		return "", "", fmt.Errorf("h1-bridge: resolve alias %q via GSI: %w", alias, err)
	}
	if len(out.Items) == 0 {
		return "", "", fmt.Errorf("h1-bridge: alias %q not found", alias)
	}
	if len(out.Items) > 1 {
		return "", "", fmt.Errorf("h1-bridge: alias %q is ambiguous (matched multiple sandboxes)", alias)
	}

	item := out.Items[0]
	sv, ok := item["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		return "", "", fmt.Errorf("h1-bridge: alias %q: GSI item missing sandbox_id", alias)
	}
	sandboxID := sv.Value

	status := ""
	if statV, ok := item["status"].(*dynamodbtypes.AttributeValueMemberS); ok {
		status = statV.Value
	}
	return sandboxID, status, nil
}

// H1QueueURL fetches the h1_inbound_queue_url attribute for the sandbox row.
func (r *DynamoAliasResolver) H1QueueURL(ctx context.Context, sandboxID string) (string, error) {
	out, err := r.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(r.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		ProjectionExpression: awssdk.String("h1_inbound_queue_url"),
	})
	if err != nil {
		return "", fmt.Errorf("h1-bridge: fetch h1 queue URL for %s: %w", sandboxID, err)
	}
	if len(out.Item) == 0 {
		return "", fmt.Errorf("h1-bridge: sandbox %s not found in table", sandboxID)
	}
	if v, ok := out.Item["h1_inbound_queue_url"].(*dynamodbtypes.AttributeValueMemberS); ok && v.Value != "" {
		return v.Value, nil
	}
	return "", fmt.Errorf("h1-bridge: sandbox %s has no h1_inbound_queue_url (inbound not provisioned)", sandboxID)
}

// Compile-time checks.
var (
	_ SandboxAliasResolver           = (*DynamoAliasResolver)(nil)
	_ SandboxAliasResolverWithStatus = (*DynamoAliasResolver)(nil)
)

// ============================================================
// H1SQSAdapter — SQSSender for h1-inbound FIFO queues
// ============================================================

// SQSClient is the narrow SQS interface required by H1SQSAdapter.
type SQSClient interface {
	SendMessage(ctx context.Context, params *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// H1SQSAdapter implements SQSSender by sending to an h1-inbound FIFO queue.
type H1SQSAdapter struct {
	Client SQSClient
}

// Send sends body to the given FIFO queue with the specified MessageGroupId and
// MessageDeduplicationId.
func (a *H1SQSAdapter) Send(ctx context.Context, queueURL, body, groupID, deduplicationID string) error {
	_, err := a.Client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               awssdk.String(queueURL),
		MessageBody:            awssdk.String(body),
		MessageGroupId:         awssdk.String(groupID),
		MessageDeduplicationId: awssdk.String(deduplicationID),
	})
	if err != nil {
		return fmt.Errorf("h1-bridge: SQS SendMessage to %s: %w", queueURL, err)
	}
	return nil
}

// Compile-time check.
var _ SQSSender = (*H1SQSAdapter)(nil)

// ============================================================
// EventBridgeAdapter — publishes SandboxCreate for cold path
// ============================================================

// generateH1SandboxID returns a new unique sandbox ID in the form "h1-" + 8 hex chars.
func generateH1SandboxID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("h1-bridge: generateH1SandboxID: rand.Read: %v", err))
	}
	return "h1-" + hex.EncodeToString(b)
}

// profileSlug normalises a profile name/path into a directory-safe slug.
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

// EventBridgeAPI is the narrow EventBridge interface needed here.
type EventBridgeAPI interface {
	PutEvents(ctx context.Context, params *eventbridge.PutEventsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error)
}

// sandboxCreateDetail mirrors pkg/aws.SandboxCreateDetail to avoid an import cycle.
// The h1_envelope field carries the serialized H1Envelope so the create-handler can
// drain it into the per-sandbox h1-inbound FIFO after provisioning.
type sandboxCreateDetail struct {
	SandboxID      string `json:"sandbox_id"`
	ArtifactBucket string `json:"artifact_bucket"`
	ArtifactPrefix string `json:"artifact_prefix"`
	Alias          string `json:"alias,omitempty"`
	H1Envelope     string `json:"h1_envelope,omitempty"`
}

// EventBridgeAdapter implements EventBridgePublisher by emitting a SandboxCreate
// event carrying the alias, profile (via the artifact_prefix convention), and the
// serialized H1Envelope.
type EventBridgeAdapter struct {
	Client         EventBridgeAPI
	ArtifactBucket string
	ArtifactPrefix string
}

// PutSandboxCreate publishes a SandboxCreate event. profile is stored in
// artifact_prefix ("h1-profiles/{slug}") so the create-handler resolves the YAML.
func (a *EventBridgeAdapter) PutSandboxCreate(ctx context.Context, alias, profile, h1EnvelopeJSON string) error {
	detail := sandboxCreateDetail{
		SandboxID:      generateH1SandboxID(),
		ArtifactBucket: a.ArtifactBucket,
		ArtifactPrefix: "h1-profiles/" + profileSlug(profile),
		Alias:          alias,
		H1Envelope:     h1EnvelopeJSON,
	}
	detailBytes, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("h1-bridge: marshal SandboxCreateDetail: %w", err)
	}
	out, err := a.Client.PutEvents(ctx, &eventbridge.PutEventsInput{
		Entries: []ebtypes.PutEventsRequestEntry{
			{
				Source:     awssdk.String("km.sandbox"),
				DetailType: awssdk.String("SandboxCreate"),
				Detail:     awssdk.String(string(detailBytes)),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("h1-bridge: EventBridge PutEvents: %w", err)
	}
	if out.FailedEntryCount > 0 {
		return fmt.Errorf("h1-bridge: EventBridge PutEvents: %d entries failed", out.FailedEntryCount)
	}
	return nil
}

// Compile-time check.
var _ EventBridgePublisher = (*EventBridgeAdapter)(nil)

// ============================================================
// EC2Resumer — starts stopped EC2 sandbox instances (warm-resume path)
// ============================================================

// EC2StartAPI is the narrow EC2 interface required by EC2Resumer.
type EC2StartAPI interface {
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
}

// EC2Resumer implements SandboxResumer by finding stopped/stopping EC2 instances
// tagged with the km sandbox-id tag and starting them. Ported from the GitHub
// bridge's resumer (including the "stopping" tolerance + bounded poll, Gap C).
type EC2Resumer struct {
	Client          EC2StartAPI
	SandboxIDTagKey string // default "km:sandbox-id" when empty
	ResourcePrefix  string // INERT: retained for wiring compat, no longer read (see sandboxIDTagKey)
}

func (r *EC2Resumer) sandboxIDTagKey() string {
	if r.SandboxIDTagKey != "" {
		return r.SandboxIDTagKey
	}
	// km ALWAYS tags sandbox instances "km:sandbox-id" regardless of resource_prefix
	// (the prefix lives in the separate "km:resource-prefix" tag). Deriving
	// "{prefix}:sandbox-id" matched nothing on non-"km" installs, which made StartSandbox
	// falsely report ErrNoResumableInstance and triggered the Phase-109 delete+cold-create
	// self-heal for fully-resumable stopped boxes. The CLI resume path
	// (internal/app/cmd/resume.go) hardcodes "tag:km:sandbox-id" — mirror it here.
	// ResourcePrefix is retained on the struct but no longer read (Option A; inert).
	return "km:sandbox-id"
}

// StartSandbox finds stopped (or stopping) instances tagged with sandboxID and
// starts them. Bounded poll waits briefly for "stopping"→"stopped" transitions.
func (r *EC2Resumer) StartSandbox(ctx context.Context, sandboxID string) error {
	tagKey := r.sandboxIDTagKey()

	descOut, err := r.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("tag:" + tagKey), Values: []string{sandboxID}},
			{Name: awssdk.String("instance-state-name"), Values: []string{"stopped", "stopping"}},
		},
	})
	if err != nil {
		return fmt.Errorf("h1-bridge: EC2Resumer.DescribeInstances for %s: %w", sandboxID, err)
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
			isStopping := inst.State != nil && inst.State.Name == ec2types.InstanceStateNameStopping
			found = append(found, foundInst{id: *inst.InstanceId, stopping: isStopping})
		}
	}
	if len(found) == 0 {
		// Terminal: the instance is gone (orphaned alias row). Wrap the sentinel so
		// the caller can branch with errors.Is and fall back to cold-create.
		return fmt.Errorf("h1-bridge: no stopped/stopping EC2 instances found for sandbox %s (tag %s): %w",
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
	if _, err := r.Client.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: instanceIDs}); err != nil {
		return fmt.Errorf("h1-bridge: EC2Resumer.StartInstances for %s: %w", sandboxID, err)
	}
	return nil
}

// Compile-time check.
var _ SandboxResumer = (*EC2Resumer)(nil)

// ============================================================
// DynamoSandboxStatusWriter — SandboxStatusWriter backed by km-sandboxes
// ============================================================

// DynamoSandboxStatusWriter implements SandboxStatusWriter via UpdateItem (never
// PutItem — avoids the SandboxMetadata lossy round-trip footgun).
type DynamoSandboxStatusWriter struct {
	Client    DynamoUpdateItemClient
	TableName string
}

// SetStatusRunning sets status="running" on the km-sandboxes row for sandboxID.
func (w *DynamoSandboxStatusWriter) SetStatusRunning(ctx context.Context, sandboxID string) error {
	_, err := w.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(w.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		UpdateExpression:         awssdk.String("SET #st = :running"),
		ExpressionAttributeNames: map[string]string{"#st": "status"},
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":running": &dynamodbtypes.AttributeValueMemberS{Value: "running"},
		},
	})
	if err != nil {
		return fmt.Errorf("h1-bridge: SetStatusRunning for %s: %w", sandboxID, err)
	}
	return nil
}

// DeleteSandboxRow removes the km-sandboxes row for sandboxID via a single DeleteItem
// keyed by sandbox_id. Phase 109: called to clear an orphaned alias row (status=stopped,
// EC2 instance gone) so the alias resolves as absent and the subsequent cold-create does
// not trip the ambiguous-alias guard. Non-fatal in the caller.
func (w *DynamoSandboxStatusWriter) DeleteSandboxRow(ctx context.Context, sandboxID string) error {
	_, err := w.Client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: awssdk.String(w.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
	})
	if err != nil {
		return fmt.Errorf("h1-bridge: delete stale sandbox row %s: %w", sandboxID, err)
	}
	return nil
}

// Compile-time check.
var _ SandboxStatusWriter = (*DynamoSandboxStatusWriter)(nil)

// ============================================================
// DynamoH1ThreadStore — H1ThreadStore backed by km-h1-threads
// ============================================================

// DynamoH1ThreadStore implements H1ThreadStore using the {prefix}-h1-threads
// DynamoDB table. Key: hash=report_id(S), range=target(S). Multi-target fanout
// means N targets dispatch on the same report — the (report_id, target) composite
// key gives each its own continuity row so they never collide.
//
// All session writes are UpdateItem-shaped (never full-row PutItem) to avoid the
// SandboxMetadata lossy round-trip footgun. Continuity data lives ONLY here.
type DynamoH1ThreadStore struct {
	Client    DynamoH1ThreadClient
	TableName string // e.g. "km-h1-threads" (from KM_H1_THREADS_TABLE env var)
}

// LookupSandbox returns sandbox_id, agent_session_id, and agent_type for
// (reportID, target). Returns ("", "", "", nil) when the row is absent.
func (s *DynamoH1ThreadStore) LookupSandbox(ctx context.Context, reportID, target string) (string, string, string, error) {
	out, err := s.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(s.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"report_id": &dynamodbtypes.AttributeValueMemberS{Value: reportID},
			"target":    &dynamodbtypes.AttributeValueMemberS{Value: target},
		},
		ProjectionExpression: awssdk.String("sandbox_id, agent_session_id, agent_type"),
	})
	if err != nil {
		return "", "", "", fmt.Errorf("h1-threads: lookup (%s, %s): %w", reportID, target, err)
	}
	if len(out.Item) == 0 {
		return "", "", "", nil // absent → first dispatch
	}

	sandboxID := ""
	if v, ok := out.Item["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS); ok {
		sandboxID = v.Value
	}
	sessionID := ""
	if v, ok := out.Item["agent_session_id"].(*dynamodbtypes.AttributeValueMemberS); ok {
		sessionID = v.Value
	}
	agentType := ""
	if v, ok := out.Item["agent_type"].(*dynamodbtypes.AttributeValueMemberS); ok {
		agentType = v.Value
	}
	return sandboxID, sessionID, agentType, nil
}

// Upsert creates a (reportID, target) row only if one does not already exist
// (attribute_not_exists(report_id) condition). ConditionalCheckFailed is
// idempotent success (the row exists with valid data). ttl_expiry = now + 30 days.
func (s *DynamoH1ThreadStore) Upsert(ctx context.Context, reportID, target, sandboxID string) error {
	ttlExpiry := time.Now().Unix() + 30*24*3600

	_, err := s.Client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: awssdk.String(s.TableName),
		Item: map[string]dynamodbtypes.AttributeValue{
			"report_id":  &dynamodbtypes.AttributeValueMemberS{Value: reportID},
			"target":     &dynamodbtypes.AttributeValueMemberS{Value: target},
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
			"ttl_expiry": &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(ttlExpiry, 10)},
		},
		ConditionExpression: awssdk.String("attribute_not_exists(report_id)"),
	})
	if err != nil {
		var condFailed *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &condFailed) {
			return nil // idempotent — row already live
		}
		return fmt.Errorf("h1-threads: upsert (%s, %s): %w", reportID, target, err)
	}
	return nil
}

// UpdateSession sets agent_session_id + agent_type on the (reportID, target) row
// via UpdateItem (never PutItem). agentType="" writes an empty string.
func (s *DynamoH1ThreadStore) UpdateSession(ctx context.Context, reportID, target, sessionID, agentType string) error {
	_, err := s.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(s.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"report_id": &dynamodbtypes.AttributeValueMemberS{Value: reportID},
			"target":    &dynamodbtypes.AttributeValueMemberS{Value: target},
		},
		UpdateExpression: awssdk.String("SET agent_session_id = :sid, agent_type = :at"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":sid": &dynamodbtypes.AttributeValueMemberS{Value: sessionID},
			":at":  &dynamodbtypes.AttributeValueMemberS{Value: agentType},
		},
	})
	if err != nil {
		return fmt.Errorf("h1-threads: update-session (%s, %s): %w", reportID, target, err)
	}
	return nil
}

// InvalidateStaleSession overwrites sandbox_id and REMOVEs agent_session_id on the
// (reportID, target) row when the stored sandbox_id no longer matches the live box.
func (s *DynamoH1ThreadStore) InvalidateStaleSession(ctx context.Context, reportID, target, newSandboxID string) error {
	ttlExpiry := time.Now().Unix() + 30*24*3600
	_, err := s.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(s.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"report_id": &dynamodbtypes.AttributeValueMemberS{Value: reportID},
			"target":    &dynamodbtypes.AttributeValueMemberS{Value: target},
		},
		UpdateExpression: awssdk.String("SET sandbox_id = :sid, ttl_expiry = :ttl REMOVE agent_session_id"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":sid": &dynamodbtypes.AttributeValueMemberS{Value: newSandboxID},
			":ttl": &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(ttlExpiry, 10)},
		},
	})
	if err != nil {
		return fmt.Errorf("h1-threads: invalidate-stale-session (%s, %s): %w", reportID, target, err)
	}
	return nil
}

// Compile-time check.
var _ H1ThreadStore = (*DynamoH1ThreadStore)(nil)

// ============================================================
// SSMCommandsFetcher — CommandSet backed by SSM (15-min cache)
// ============================================================

// SSMCommandsFetcher fetches and caches the H1 CommandSet from SSM. Returns an
// empty map + "" default when the parameter is absent (dormant-by-default).
type SSMCommandsFetcher struct {
	Client   SecretSSMClient
	Path     string        // e.g. "/{prefix}/config/h1/commands"
	CacheTTL time.Duration // defaults to 15 minutes

	mu    sync.Mutex
	cache struct {
		commands       map[string]CommandEntry
		defaultCommand string
		expiry         time.Time
	}
}

// Fetch returns the current command map and install-wide default_command.
func (f *SSMCommandsFetcher) Fetch(ctx context.Context) (map[string]CommandEntry, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ttl := f.CacheTTL
	if ttl == 0 {
		ttl = 15 * time.Minute
	}
	if f.cache.commands != nil && time.Now().Before(f.cache.expiry) {
		return f.cache.commands, f.cache.defaultCommand, nil
	}

	out, err := f.Client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(f.Path),
		WithDecryption: awssdk.Bool(false),
	})
	if err != nil {
		var notFound *ssmtypes.ParameterNotFound
		if errors.As(err, &notFound) {
			empty := map[string]CommandEntry{}
			f.cache.commands = empty
			f.cache.defaultCommand = ""
			f.cache.expiry = time.Now().Add(ttl)
			return empty, "", nil
		}
		return nil, "", fmt.Errorf("h1-bridge: fetch commands from SSM %s: %w", f.Path, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		empty := map[string]CommandEntry{}
		f.cache.commands = empty
		f.cache.defaultCommand = ""
		f.cache.expiry = time.Now().Add(ttl)
		return empty, "", nil
	}

	// The value is base64-encoded JSON (km init encodes to dodge SSM's {{}} restriction).
	raw := []byte(*out.Parameter.Value)
	if decoded, b64Err := base64.StdEncoding.DecodeString(*out.Parameter.Value); b64Err == nil {
		raw = decoded
	}

	var cs CommandSet
	if jsonErr := json.Unmarshal(raw, &cs); jsonErr != nil {
		return nil, "", fmt.Errorf("h1-bridge: parse commands JSON from SSM %s: %w", f.Path, jsonErr)
	}
	if cs.Commands == nil {
		cs.Commands = map[string]CommandEntry{}
	}

	f.cache.commands = cs.Commands
	f.cache.defaultCommand = cs.DefaultCommand
	f.cache.expiry = time.Now().Add(ttl)
	return cs.Commands, cs.DefaultCommand, nil
}

// ============================================================
// H1APICommenter — H1Commenter via HackerOne customer API (Basic Auth)
// ============================================================

// H1APICommenter implements H1Commenter for the bridge's synchronous internal
// "on it" ACK only. The bridge holds the HackerOne customer-API Basic-Auth
// identity (api_username + api_token) purely so it can post the INTERNAL ack —
// researcher-visible replies come from the SANDBOX helper (cmd/km-h1), not here.
//
// SAFETY: the internal flag is passed through verbatim; the handler is the layer
// that decides internal vs public (deny-by-default). This adapter never defaults.
type H1APICommenter struct {
	BaseURL     string // e.g. "https://api.hackerone.com/v1"; defaults to the public API
	APIUsername string
	APIToken    string
	HTTPClient  *http.Client
}

func (c *H1APICommenter) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return "https://api.hackerone.com/v1"
}

func (c *H1APICommenter) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// PostComment posts an activity (comment) to the report via the customer API.
// internal=true posts a HackerOne internal comment; internal=false is researcher-
// visible. Errors are returned but the caller logs them non-fatally (200 unchanged).
func (c *H1APICommenter) PostComment(ctx context.Context, reportID, body string, internal bool) error {
	apiURL := fmt.Sprintf("%s/reports/%s/activities", c.baseURL(), url.PathEscape(reportID))

	payload := map[string]any{
		"data": map[string]any{
			"type": "activity-comment",
			"attributes": map[string]any{
				"message":  body,
				"internal": internal,
			},
		},
	}
	reqBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("h1-bridge: commenter: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("h1-bridge: commenter: build request: %w", err)
	}
	req.SetBasicAuth(c.APIUsername, c.APIToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("h1-bridge: commenter: POST comment: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("h1-bridge: commenter: unexpected status %d: %s",
		resp.StatusCode, strings.TrimSpace(string(respBody)))
}

// Compile-time check.
var _ H1Commenter = (*H1APICommenter)(nil)

// ============================================================
// DynamoFreezer — auto-freeze adapter (Phase 121 GAP-2)
// ============================================================

// DynamoFreezer implements Freezer by calling kmaws.FreezeSandboxDynamo on the
// km-sandboxes table. Wired into WebhookHandler.Freezer in main.go.
type DynamoFreezer struct {
	Client DynamoUpdateItemClient // *dynamodb.Client satisfies this
	Table  string                 // e.g. "km-sandboxes"
}

// h1UpdateOnlyMetaClient adapts DynamoUpdateItemClient to kmaws.SandboxMetadataAPI.
// Only UpdateItem is exercised by FreezeSandboxDynamo; the remaining methods
// panic to make any accidental call loud.
type h1UpdateOnlyMetaClient struct{ c DynamoUpdateItemClient }

func (a h1UpdateOnlyMetaClient) UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return a.c.UpdateItem(ctx, in, opts...)
}
func (a h1UpdateOnlyMetaClient) GetItem(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	panic("h1UpdateOnlyMetaClient: GetItem not implemented")
}
func (a h1UpdateOnlyMetaClient) PutItem(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	panic("h1UpdateOnlyMetaClient: PutItem not implemented")
}
func (a h1UpdateOnlyMetaClient) DeleteItem(_ context.Context, _ *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	panic("h1UpdateOnlyMetaClient: DeleteItem not implemented")
}
func (a h1UpdateOnlyMetaClient) Scan(_ context.Context, _ *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	panic("h1UpdateOnlyMetaClient: Scan not implemented")
}
func (a h1UpdateOnlyMetaClient) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	panic("h1UpdateOnlyMetaClient: Query not implemented")
}

// FreezeSandbox latches action_frozen=true on the sandbox's km-sandboxes row.
// by should be "auto:<action>:<window>" for auto-on-breach freezes.
func (f *DynamoFreezer) FreezeSandbox(ctx context.Context, sandboxID, reason, by string) error {
	return kmaws.FreezeSandboxDynamo(ctx, h1UpdateOnlyMetaClient{f.Client}, f.Table, sandboxID, reason, by)
}

// compile-time interface check.
var _ Freezer = (*DynamoFreezer)(nil)

// ============================================================
// DDBActionLimitsFetcher — per-sandbox action_limits resolver (Phase 121)
// ============================================================

// h1GetItemAPI is the minimal GetItem surface DDBActionLimitsFetcher needs.
// *dynamodb.Client satisfies it.
type h1GetItemAPI interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
}

// DDBActionLimitsFetcher implements H1ActionLimitsFetcher by reading the
// action_limits JSON string from the km-sandboxes row (GetItem keyed by
// sandbox_id). Wired into WebhookHandler.Limits in main.go. An absent row or
// absent action_limits attr returns "" (dormant — quota.Record then no-ops).
type DDBActionLimitsFetcher struct {
	Client    h1GetItemAPI // *dynamodb.Client satisfies this
	TableName string       // e.g. "km-sandboxes"
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
		return "", fmt.Errorf("h1-bridge: action_limits lookup for %s: %w", sandboxID, err)
	}
	if out == nil || out.Item == nil {
		return "", nil
	}
	if v, ok := out.Item["action_limits"].(*dynamodbtypes.AttributeValueMemberS); ok {
		return v.Value, nil
	}
	return "", nil
}

// compile-time interface check.
var _ H1ActionLimitsFetcher = (*DDBActionLimitsFetcher)(nil)
