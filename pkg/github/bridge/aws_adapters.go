// Package bridge — aws_adapters.go
// Production-backed implementations of the bridge interfaces.
// These adapters wire real AWS services (DynamoDB for nonce/alias lookup,
// SSM for the webhook secret/bot-login/app credentials, SQS for github-inbound
// queue delivery, EventBridge for cold sandbox create) into the WebhookHandler
// used by the km-github-bridge Lambda.
//
// Design mirrors pkg/slack/bridge/aws_adapters.go closely. Key differences:
//   - SSMSecretFetcher: reads GitHub webhook secret (not Slack signing secret).
//   - SSMBotLoginFetcher: reads bot-login from SSM (not from auth.test).
//   - DynamoGitHubNonceStore: wraps DynamoNonceStore for the DeliveryNonceStore interface.
//   - DynamoAliasResolver: queries alias-index GSI + reads github_inbound_queue_url.
//   - GitHubSQSAdapter: mirrors SQSAdapter for github-inbound FIFO queues.
//   - EventBridgeAdapter: wraps PutSandboxCreateEvent with alias+profile+envelope.
//   - InstallationReactor: mints an App JWT → installation token, then POSTs 👀 reaction.
package bridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	pkggithub "github.com/whereiskurt/klanker-maker/pkg/github"
)

// ============================================================
// Narrow interfaces for adapters
// ============================================================

// SecretSSMClient is the minimal SSM interface used by the secret fetchers.
type SecretSSMClient interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// DynamoQueryPutter is the minimal DynamoDB interface needed by the adapters.
type DynamoQueryPutter interface {
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	Query(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// DynamoGitHubThreadClient is a superset of DynamoQueryPutter that adds UpdateItem,
// required by DynamoGitHubThreadStore.UpdateSession. Kept separate to avoid widening
// DynamoQueryPutter (which would force all existing fakes to implement UpdateItem).
type DynamoGitHubThreadClient interface {
	DynamoQueryPutter
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

// ErrNonceReplayed is returned by DynamoGitHubNonceStore when the key was already inserted.
var ErrNonceReplayed = errors.New("github-bridge: delivery GUID already seen (replay)")

// ============================================================
// SSMSecretFetcher — webhook signing secret
// ============================================================

// cachedValue holds one cached string value with an expiry time.
type cachedValue struct {
	value  string
	expiry time.Time
}

// SSMSecretFetcher fetches and caches the GitHub webhook secret from SSM.
// The 15-minute cache avoids redundant SSM calls on warm Lambda invocations.
type SSMSecretFetcher struct {
	Client   SecretSSMClient
	Path     string        // e.g. "/{prefix}/config/github/webhook-secret"
	CacheTTL time.Duration // defaults to 15 minutes

	mu    sync.Mutex
	cache cachedValue
}

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
		return "", fmt.Errorf("github-bridge: fetch webhook secret from SSM %s: %w", f.Path, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("github-bridge: SSM parameter %s has no value", f.Path)
	}
	v := *out.Parameter.Value
	f.cache = cachedValue{value: v, expiry: time.Now().Add(ttl)}
	return v, nil
}

// ============================================================
// SSMBotLoginFetcher — bot login name
// ============================================================

// SSMBotLoginFetcher fetches and caches the bot's GitHub login from SSM.
// Unlike Slack (which calls auth.test), the GitHub bot-login is a static
// configured string (e.g. "klanker-maker[bot]") stored at SSM by km github init.
type SSMBotLoginFetcher struct {
	Client   SecretSSMClient
	Path     string        // e.g. "/{prefix}/config/github/bot-login"
	CacheTTL time.Duration

	mu    sync.Mutex
	cache cachedValue
}

func (f *SSMBotLoginFetcher) Fetch(ctx context.Context) (string, error) {
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
		WithDecryption: awssdk.Bool(false), // bot-login is a plain String
	})
	if err != nil {
		return "", fmt.Errorf("github-bridge: fetch bot-login from SSM %s: %w", f.Path, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("github-bridge: SSM parameter %s has no value", f.Path)
	}
	v := *out.Parameter.Value
	f.cache = cachedValue{value: v, expiry: time.Now().Add(ttl)}
	return v, nil
}

// ============================================================
// DynamoGitHubNonceStore — DeliveryNonceStore backed by DynamoDB
// ============================================================

// DynamoGitHubNonceStore implements DeliveryNonceStore using the same nonces
// DynamoDB table as the Slack bridge. Keys are prefixed "github-delivery:" to
// isolate them from Slack event_id and operator nonce entries.
type DynamoGitHubNonceStore struct {
	Client    DynamoQueryPutter
	TableName string // e.g. "km-slack-bridge-nonces" (shared nonces table)
}

// CheckAndStore returns (true, nil) if the key was already stored (replay),
// (false, nil) on first insertion, or (false, err) on storage failure.
// TTL is applied via DynamoDB ttl_expiry attribute.
func (s *DynamoGitHubNonceStore) CheckAndStore(ctx context.Context, key string, ttlSeconds int) (bool, error) {
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
			return true, nil // already seen — replay
		}
		return false, fmt.Errorf("github-bridge: nonce store: %w", err)
	}
	return false, nil
}

// ============================================================
// DynamoAliasResolver — alias-index GSI lookup + queue URL fetch
// ============================================================

// DynamoAliasResolver implements SandboxAliasResolver by querying the
// alias-index GSI on km-sandboxes and reading the github_inbound_queue_url
// attribute for the warm-dispatch path.
type DynamoAliasResolver struct {
	Client    DynamoQueryPutter
	TableName string // e.g. "km-sandboxes"
}

// ResolveByAlias queries the alias-index GSI for the sandbox_id of the alias.
// Returns an error if no sandbox with that alias exists (cold path trigger).
func (r *DynamoAliasResolver) ResolveByAlias(ctx context.Context, alias string) (string, error) {
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
		return "", fmt.Errorf("github-bridge: resolve alias %q via GSI: %w", alias, err)
	}
	if len(out.Items) == 0 {
		return "", fmt.Errorf("github-bridge: alias %q not found", alias)
	}
	if len(out.Items) > 1 {
		return "", fmt.Errorf("github-bridge: alias %q is ambiguous (matched multiple sandboxes)", alias)
	}

	item := out.Items[0]
	sv, ok := item["sandbox_id"]
	if !ok {
		return "", fmt.Errorf("github-bridge: alias %q: GSI item missing sandbox_id", alias)
	}
	s, ok := sv.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		return "", fmt.Errorf("github-bridge: alias %q: sandbox_id not a String", alias)
	}
	return s.Value, nil
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
		return "", "", fmt.Errorf("github-bridge: resolve alias (with status) %q via GSI: %w", alias, err)
	}
	if len(out.Items) == 0 {
		return "", "", fmt.Errorf("github-bridge: alias %q not found", alias)
	}
	if len(out.Items) > 1 {
		return "", "", fmt.Errorf("github-bridge: alias %q is ambiguous (matched multiple sandboxes)", alias)
	}

	item := out.Items[0]
	sv, ok := item["sandbox_id"]
	if !ok {
		return "", "", fmt.Errorf("github-bridge: alias %q: GSI item missing sandbox_id", alias)
	}
	s, ok := sv.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		return "", "", fmt.Errorf("github-bridge: alias %q: sandbox_id not a String", alias)
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

// GitHubQueueURL fetches the github_inbound_queue_url attribute from the
// sandbox's km-sandboxes row. Returns an error if absent (queue not provisioned).
func (r *DynamoAliasResolver) GitHubQueueURL(ctx context.Context, sandboxID string) (string, error) {
	out, err := r.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(r.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		ProjectionExpression: awssdk.String("github_inbound_queue_url"),
	})
	if err != nil {
		return "", fmt.Errorf("github-bridge: fetch github queue URL for %s: %w", sandboxID, err)
	}
	if len(out.Item) == 0 {
		return "", fmt.Errorf("github-bridge: sandbox %s not found in table", sandboxID)
	}
	if v, ok := out.Item["github_inbound_queue_url"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok && sv.Value != "" {
			return sv.Value, nil
		}
	}
	return "", fmt.Errorf("github-bridge: sandbox %s has no github_inbound_queue_url (inbound not provisioned)", sandboxID)
}

// ============================================================
// GitHubSQSAdapter — SQSSender for github-inbound FIFO queues
// ============================================================

// SQSClient is the narrow SQS interface required by GitHubSQSAdapter.
type SQSClient interface {
	SendMessage(ctx context.Context, params *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// GitHubSQSAdapter implements SQSSender by sending to a github-inbound FIFO queue.
type GitHubSQSAdapter struct {
	Client SQSClient
}

// Send sends body to the given FIFO queue with the specified MessageGroupId
// and MessageDeduplicationId.
func (a *GitHubSQSAdapter) Send(ctx context.Context, queueURL, body, groupID, deduplicationID string) error {
	_, err := a.Client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               awssdk.String(queueURL),
		MessageBody:            awssdk.String(body),
		MessageGroupId:         awssdk.String(groupID),
		MessageDeduplicationId: awssdk.String(deduplicationID),
	})
	if err != nil {
		return fmt.Errorf("github-bridge: SQS SendMessage to %s: %w", queueURL, err)
	}
	return nil
}

// ============================================================
// EventBridgeAdapter — publishes SandboxCreate for cold path
// ============================================================

// generateGitHubSandboxID returns a new unique sandbox ID in the form "gh-" + 8
// lowercase hex characters (e.g. "gh-a1b2c3d4"). Mirrors compiler.GenerateSandboxID
// semantics but is defined locally to avoid an import cycle between bridge and
// pkg/compiler.
func generateGitHubSandboxID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failures are exceedingly rare; fall through to a time-seeded
		// fallback rather than propagating an error through a value-returning function.
		panic(fmt.Sprintf("github-bridge: generateGitHubSandboxID: rand.Read: %v", err))
	}
	return "gh-" + hex.EncodeToString(b)
}

// profileSlug normalises a profile name/path into a directory-safe slug.
// Examples:
//
//	"github-review"       → "github-review"
//	"github-review.yaml"  → "github-review"
//	"profiles/foo.yaml"   → "foo"
//
// The create-handler convention is artifact_prefix/directory; the slug is the
// directory component under "github-profiles/".
func profileSlug(profile string) string {
	// Drop any path prefix (keep just the base name).
	base := filepath.Base(profile)
	// Strip trailing ".yaml" or ".yml".
	for _, ext := range []string{".yaml", ".yml"} {
		if strings.HasSuffix(strings.ToLower(base), ext) {
			base = base[:len(base)-len(ext)]
			break
		}
	}
	return strings.ToLower(base)
}

// EventBridgeAPI is the narrow EventBridge interface needed here.
// *eventbridge.Client satisfies this.
type EventBridgeAPI interface {
	PutEvents(ctx context.Context, params *eventbridge.PutEventsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error)
}

// sandboxCreateDetail is a local copy of the fields we need to emit on EventBridge.
// It mirrors pkg/aws.SandboxCreateDetail to avoid an import cycle between bridge
// and pkg/aws. The shape must stay identical.
type sandboxCreateDetail struct {
	SandboxID      string `json:"sandbox_id"`
	ArtifactBucket string `json:"artifact_bucket"`
	ArtifactPrefix string `json:"artifact_prefix"`
	Alias          string `json:"alias,omitempty"`
	GithubEnvelope string `json:"github_envelope,omitempty"`
}

// EventBridgeAdapter implements EventBridgePublisher by emitting a SandboxCreate
// event carrying the alias, profile (via the artifact_prefix convention), and the
// serialized GitHubEnvelope so the create-handler can drain it post-provisioning.
//
// The Alias is passed in the `alias` field of SandboxCreateDetail (forwarded by
// the create-handler to the km create subprocess). The GithubEnvelope is a JSON
// string that the create-handler drains into the github-inbound FIFO after create.
type EventBridgeAdapter struct {
	Client         EventBridgeAPI
	ArtifactBucket string // required by SandboxCreateDetail
	ArtifactPrefix string // path prefix (the create handler resolves the actual profile YAML)
}

// PutSandboxCreate publishes a SandboxCreate event. profile is stored in
// artifact_prefix so the create-handler knows which profile YAML to use.
//
// artifact_prefix is the DIRECTORY "github-profiles/{profileSlug}"; the
// create-handler appends "/.km-profile.yaml" to resolve the actual profile file.
// sandbox_id is generated here as "gh-" + 8 hex chars so the create-handler can
// use the caller-supplied identity rather than generating its own (determinism).
func (a *EventBridgeAdapter) PutSandboxCreate(ctx context.Context, alias, profile, githubEnvelopeJSON string) error {
	sandboxID := generateGitHubSandboxID()
	detail := sandboxCreateDetail{
		SandboxID:      sandboxID,
		ArtifactBucket: a.ArtifactBucket,
		ArtifactPrefix: "github-profiles/" + profileSlug(profile),
		Alias:          alias,
		GithubEnvelope: githubEnvelopeJSON,
	}
	detailBytes, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("github-bridge: marshal SandboxCreateDetail: %w", err)
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
		return fmt.Errorf("github-bridge: EventBridge PutEvents: %w", err)
	}
	if out.FailedEntryCount > 0 {
		return fmt.Errorf("github-bridge: EventBridge PutEvents: %d entries failed", out.FailedEntryCount)
	}
	return nil
}

// ============================================================
// EC2Resumer — starts stopped EC2 sandbox instances (warm-resume path)
// ============================================================

// EC2StartAPI is the narrow EC2 interface required by EC2Resumer.
// *ec2.Client satisfies this interface.
type EC2StartAPI interface {
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
}

// EC2Resumer implements SandboxResumer by finding stopped EC2 instances tagged
// with the km sandbox-id tag and starting them. Mirrors the resume path in
// internal/app/cmd/resume.go:95-115.
type EC2Resumer struct {
	Client          EC2StartAPI
	SandboxIDTagKey string // e.g. "km:sandbox-id" (km standard sandbox tag)
	ResourcePrefix  string // e.g. "km" — used to default SandboxIDTagKey when empty
}

func (r *EC2Resumer) sandboxIDTagKey() string {
	if r.SandboxIDTagKey != "" {
		return r.SandboxIDTagKey
	}
	if r.ResourcePrefix != "" {
		return r.ResourcePrefix + ":sandbox-id"
	}
	return "km:sandbox-id"
}

// StartSandbox finds all stopped EC2 instances tagged with the km sandbox-id
// tag equal to sandboxID and calls StartInstances on them. Returns nil when
// at least one instance was started, or an error describing the failure.
func (r *EC2Resumer) StartSandbox(ctx context.Context, sandboxID string) error {
	tagKey := r.sandboxIDTagKey()
	descOut, err := r.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("tag:" + tagKey), Values: []string{sandboxID}},
			{Name: awssdk.String("instance-state-name"), Values: []string{"stopped"}},
		},
	})
	if err != nil {
		return fmt.Errorf("github-bridge: EC2Resumer.DescribeInstances for %s: %w", sandboxID, err)
	}

	var instanceIDs []string
	for _, res := range descOut.Reservations {
		for _, inst := range res.Instances {
			if inst.InstanceId != nil && *inst.InstanceId != "" {
				instanceIDs = append(instanceIDs, *inst.InstanceId)
			}
		}
	}
	if len(instanceIDs) == 0 {
		return fmt.Errorf("github-bridge: no stopped EC2 instances found for sandbox %s (tag %s)", sandboxID, tagKey)
	}

	if _, err := r.Client.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: instanceIDs,
	}); err != nil {
		return fmt.Errorf("github-bridge: EC2Resumer.StartInstances for %s: %w", sandboxID, err)
	}
	return nil
}

// ============================================================
// InstallationReactor — mints an App JWT → installation token, POSTs 👀
// ============================================================

// InstallationReactor implements GitHubReactor by:
//  1. Minting a short-lived App JWT via pkg/github.GenerateGitHubAppJWT.
//  2. Exchanging the JWT for an installation access token via
//     pkg/github.ExchangeForInstallationToken.
//  3. POSTing the 👀 reaction to the comment via the Reactions API.
//
// The token is NOT cached — it is minted per-invocation (the Lambda
// processes one comment at a time; the 10-minute App JWT is only created once
// per Handle() call). If per-warmup caching is needed in future, add a cache
// around the JWT signed string (not the access token, which GitHub invalidates).
type InstallationReactor struct {
	// AppClientID is the GitHub App client ID (read from SSM at cold-start).
	AppClientID string
	// PrivateKeyPEM is the App's RSA private key bytes (read from SSM at cold-start).
	PrivateKeyPEM []byte
	// HTTPClient is shared across attempts; defaults to http.DefaultClient.
	HTTPClient *http.Client
	// BaseURL for the GitHub API; overridden in tests via pkggithub.GitHubAPIBaseURL.
	BaseURL string
}

func (r *InstallationReactor) apiBaseURL() string {
	if r.BaseURL != "" {
		return r.BaseURL
	}
	return pkggithub.GitHubAPIBaseURL
}

func (r *InstallationReactor) httpClient() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// AddReaction mints an installation token and POSTs a reaction to the comment.
// Returns nil on success or already-reacted (idempotent). Errors are logged
// by the caller but do NOT change the 200 response (Pitfall 3 mitigation).
// ============================================================
// DynamoGitHubThreadStore — GitHubThreadStore backed by km-github-threads
// ============================================================

// DynamoGitHubThreadStore implements GitHubThreadStore using the km-github-threads
// DynamoDB table (created in 98-00). Key: hash=repo(S), range=number(N).
// Mirrors DDBThreadStore from pkg/slack/bridge/aws_adapters.go but uses a composite
// key of repo (string) + number (int) instead of channel_id + thread_ts.
//
// Continuity data lives ONLY here — never in km-sandboxes — to sidestep the
// SandboxMetadata lossy round-trip footgun (RESEARCH Pitfall 5).
type DynamoGitHubThreadStore struct {
	Client    DynamoGitHubThreadClient
	TableName string // e.g. "km-github-threads" (from KM_GITHUB_THREADS_TABLE env var)
}

// LookupSandbox returns the sandbox_id and agent_session_id for (repo, number).
// Returns ("", "", nil) when the row is absent — first dispatch is not an error.
func (s *DynamoGitHubThreadStore) LookupSandbox(ctx context.Context, repo string, number int) (string, string, error) {
	out, err := s.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(s.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"repo":   &dynamodbtypes.AttributeValueMemberS{Value: repo},
			"number": &dynamodbtypes.AttributeValueMemberN{Value: strconv.Itoa(number)},
		},
		ProjectionExpression: awssdk.String("sandbox_id, agent_session_id"),
	})
	if err != nil {
		return "", "", fmt.Errorf("github-threads: lookup (%s, %d): %w", repo, number, err)
	}
	if len(out.Item) == 0 {
		return "", "", nil // absent → first dispatch
	}

	sandboxID := ""
	if v, ok := out.Item["sandbox_id"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			sandboxID = sv.Value
		}
	}
	sessionID := ""
	if v, ok := out.Item["agent_session_id"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			sessionID = sv.Value
		}
	}
	return sandboxID, sessionID, nil
}

// Upsert creates a new (repo, number) → sandbox_id row only if one does not already
// exist (attribute_not_exists(repo) condition). ConditionalCheckFailed means the row
// already exists — that is idempotent success (do NOT overwrite agent_session_id set
// by the poller, mirroring the Slack bridge behavior).
// ttl_expiry is set to now + 30 days.
func (s *DynamoGitHubThreadStore) Upsert(ctx context.Context, repo string, number int, sandboxID string) error {
	ttlExpiry := time.Now().Unix() + 30*24*3600

	_, err := s.Client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: awssdk.String(s.TableName),
		Item: map[string]dynamodbtypes.AttributeValue{
			"repo":       &dynamodbtypes.AttributeValueMemberS{Value: repo},
			"number":     &dynamodbtypes.AttributeValueMemberN{Value: strconv.Itoa(number)},
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
			"ttl_expiry": &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(ttlExpiry, 10)},
		},
		ConditionExpression: awssdk.String("attribute_not_exists(repo)"),
	})
	if err != nil {
		var condFailed *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &condFailed) {
			// Row already exists — the thread is live and sandbox_id is already set.
			// This is the idempotent success path; do NOT overwrite.
			return nil
		}
		return fmt.Errorf("github-threads: upsert (%s, %d): %w", repo, number, err)
	}
	return nil
}

// UpdateSession sets agent_session_id on the (repo, number) row via an UpdateItem.
// Called by the sandbox poller after each agent turn completes so future turns resume
// the same session.
func (s *DynamoGitHubThreadStore) UpdateSession(ctx context.Context, repo string, number int, sessionID string) error {
	_, err := s.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(s.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"repo":   &dynamodbtypes.AttributeValueMemberS{Value: repo},
			"number": &dynamodbtypes.AttributeValueMemberN{Value: strconv.Itoa(number)},
		},
		UpdateExpression: awssdk.String("SET agent_session_id = :sid"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":sid": &dynamodbtypes.AttributeValueMemberS{Value: sessionID},
		},
	})
	if err != nil {
		return fmt.Errorf("github-threads: update-session (%s, %d): %w", repo, number, err)
	}
	return nil
}

func (r *InstallationReactor) AddReaction(ctx context.Context, installationID, owner, repo string, commentID int64, content string) error {
	// Step 1: mint App JWT.
	jwtToken, err := pkggithub.GenerateGitHubAppJWT(r.AppClientID, r.PrivateKeyPEM)
	if err != nil {
		return fmt.Errorf("github-bridge: reactor: generate JWT: %w", err)
	}

	// Step 2: exchange JWT for installation token.
	// We request all-repos scope ("*") with issues:write permission.
	// The installation is scoped to the specific repo so "*" resolves to just
	// that repo within the install's scope.
	// Reactions on a PR's conversation comment can require pull_requests:write
	// (the comment lives under a pull request), not just issues:write — request
	// both so the 👀 works on both issue and PR comments.
	token, err := pkggithub.ExchangeForInstallationToken(ctx, jwtToken, installationID,
		[]string{"*"}, map[string]string{"issues": "write", "pull_requests": "write"})
	if err != nil {
		return fmt.Errorf("github-bridge: reactor: exchange installation token: %w", err)
	}

	// Step 3: POST 👀 reaction (RESEARCH § Reactions API).
	url := fmt.Sprintf("%s/repos/%s/%s/issues/comments/%d/reactions",
		r.apiBaseURL(), owner, repo, commentID)

	body, _ := json.Marshal(map[string]string{"content": content})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("github-bridge: reactor: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("github-bridge: reactor: POST reaction: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)

	// 201 Created = new reaction; 200 OK = reaction already exists (idempotent).
	// 422 Unprocessable Entity is returned when the reaction already exists in
	// some GitHub API versions — treat as idempotent success.
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		return nil
	case http.StatusUnprocessableEntity:
		// already_reacted — treat as success per interface contract.
		return nil
	default:
		// Include the GitHub response body — it names the missing permission
		// (e.g. "Resource not accessible by integration") for diagnosis.
		return fmt.Errorf("github-bridge: reactor: unexpected status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
}
