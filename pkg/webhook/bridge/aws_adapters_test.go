// aws_adapters_test.go — Task 9 fix round 1: unit tests for the AWS adapters.
//
// Mirrors the mock style of pkg/github/bridge/aws_adapters_test.go: narrow
// hand-rolled fakes for each client interface, no mocking library.
//
// Scope (per the review that requested this file): DynamoRateCounter.Increment
// (the one genuinely new adapter — priority), DynamoAliasResolver (both
// methods, including pinning the current ambiguous-alias behavior),
// WebhookSQSAdapter.Send (MessageGroupId + dedup id derivation), and the
// EC2Resumer terminal/transient ErrNoResumableInstance split (the
// highest-consequence behavior in the file — getting it backwards deletes
// live sandbox rows).
package bridge_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/whereiskurt/klanker-maker/pkg/webhook/bridge"
)

// ============================================================
// DynamoRateCounter.Increment
// ============================================================

// fakeDynamoUpdateClient implements bridge.DynamoUpdateItemClient (UpdateItem +
// DeleteItem) for DynamoRateCounter and DynamoSandboxStatusWriter tests.
type fakeDynamoUpdateClient struct {
	updateInput  *dynamodb.UpdateItemInput
	updateOutput *dynamodb.UpdateItemOutput
	updateErr    error
}

func (f *fakeDynamoUpdateClient) UpdateItem(_ context.Context, params *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updateInput = params
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return f.updateOutput, nil
}

func (f *fakeDynamoUpdateClient) DeleteItem(_ context.Context, _ *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}

// hitCountOutput builds an UpdateItemOutput whose UPDATED_NEW attributes carry
// hit_count = n as a DynamoDB Number.
func hitCountOutput(n int64) *dynamodb.UpdateItemOutput {
	return &dynamodb.UpdateItemOutput{
		Attributes: map[string]dynamodbtypes.AttributeValue{
			"hit_count": &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(n, 10)},
		},
	}
}

// TestDynamoRateCounter_Increment_ReturnsPostIncrementValue verifies the happy
// path: Increment returns the hit_count DynamoDB handed back in UPDATED_NEW.
func TestDynamoRateCounter_Increment_ReturnsPostIncrementValue(t *testing.T) {
	fake := &fakeDynamoUpdateClient{updateOutput: hitCountOutput(3)}
	counter := &bridge.DynamoRateCounter{Client: fake, TableName: "km-slack-bridge-nonces"}

	n, err := counter.Increment(context.Background(), "wh-rate:12345", 60)
	if err != nil {
		t.Fatalf("Increment returned error: %v", err)
	}
	if n != 3 {
		t.Errorf("Increment = %d; want 3", n)
	}
}

// TestDynamoRateCounter_Increment_ExpressionUsesIfNotExistsForTTL is the
// priority mutation guard the review named: dropping if_not_exists from the TTL
// clause would push the bucket's expiry forward on every hit instead of only on
// first creation, breaking the "one TTL, set once" guarantee. Asserting on the
// expression STRING makes that mutation fail loudly instead of compiling clean
// and passing every other test.
func TestDynamoRateCounter_Increment_ExpressionUsesIfNotExistsForTTL(t *testing.T) {
	fake := &fakeDynamoUpdateClient{updateOutput: hitCountOutput(1)}
	counter := &bridge.DynamoRateCounter{Client: fake, TableName: "t"}

	if _, err := counter.Increment(context.Background(), "k", 60); err != nil {
		t.Fatalf("Increment returned error: %v", err)
	}

	if fake.updateInput == nil || fake.updateInput.UpdateExpression == nil {
		t.Fatal("UpdateItem was not called with an UpdateExpression")
	}
	expr := *fake.updateInput.UpdateExpression

	if !strings.Contains(expr, "if_not_exists(ttl_expiry") {
		t.Errorf("UpdateExpression = %q; must contain if_not_exists(ttl_expiry — "+
			"without it, every hit within the bucket's life pushes the TTL forward, "+
			"and a hot bucket under a real storm would never expire", expr)
	}
	if !strings.Contains(expr, "ADD hit_count") {
		t.Errorf("UpdateExpression = %q; must ADD hit_count (the atomic counter itself)", expr)
	}
}

// TestDynamoRateCounter_Increment_ReturnValuesUpdatedNew asserts the request
// asks DynamoDB for UPDATED_NEW — the only ReturnValues mode that hands back
// the post-increment hit_count in one round trip.
func TestDynamoRateCounter_Increment_ReturnValuesUpdatedNew(t *testing.T) {
	fake := &fakeDynamoUpdateClient{updateOutput: hitCountOutput(1)}
	counter := &bridge.DynamoRateCounter{Client: fake, TableName: "t"}

	if _, err := counter.Increment(context.Background(), "k", 60); err != nil {
		t.Fatalf("Increment returned error: %v", err)
	}
	if fake.updateInput.ReturnValues != dynamodbtypes.ReturnValueUpdatedNew {
		t.Errorf("ReturnValues = %v; want UPDATED_NEW", fake.updateInput.ReturnValues)
	}
}

// TestDynamoRateCounter_Increment_MissingHitCountIsError verifies that an
// UPDATED_NEW response with no hit_count attribute returns an error — never a
// silent (0, nil), which would mean the rate ceiling can never trip.
func TestDynamoRateCounter_Increment_MissingHitCountIsError(t *testing.T) {
	fake := &fakeDynamoUpdateClient{updateOutput: &dynamodb.UpdateItemOutput{
		Attributes: map[string]dynamodbtypes.AttributeValue{},
	}}
	counter := &bridge.DynamoRateCounter{Client: fake, TableName: "t"}

	n, err := counter.Increment(context.Background(), "k", 60)
	if err == nil {
		t.Fatalf("expected an error when hit_count is absent, got (%d, nil) — "+
			"a silent zero means CheckRate never trips the ceiling", n)
	}
}

// TestDynamoRateCounter_Increment_WrongTypeHitCountIsError verifies that a
// hit_count attribute of the wrong DynamoDB type (e.g. a String instead of a
// Number) is treated the same as absent — an error, not a silent zero.
func TestDynamoRateCounter_Increment_WrongTypeHitCountIsError(t *testing.T) {
	fake := &fakeDynamoUpdateClient{updateOutput: &dynamodb.UpdateItemOutput{
		Attributes: map[string]dynamodbtypes.AttributeValue{
			"hit_count": &dynamodbtypes.AttributeValueMemberS{Value: "3"},
		},
	}}
	counter := &bridge.DynamoRateCounter{Client: fake, TableName: "t"}

	n, err := counter.Increment(context.Background(), "k", 60)
	if err == nil {
		t.Fatalf("expected an error for a non-Number hit_count, got (%d, nil)", n)
	}
}

// TestDynamoRateCounter_Increment_UnparseableNumericHitCountIsError verifies
// that a Number-typed hit_count whose value string doesn't parse as an int64
// returns an error rather than silently truncating to zero.
func TestDynamoRateCounter_Increment_UnparseableNumericHitCountIsError(t *testing.T) {
	fake := &fakeDynamoUpdateClient{updateOutput: &dynamodb.UpdateItemOutput{
		Attributes: map[string]dynamodbtypes.AttributeValue{
			"hit_count": &dynamodbtypes.AttributeValueMemberN{Value: "not-a-number"},
		},
	}}
	counter := &bridge.DynamoRateCounter{Client: fake, TableName: "t"}

	if _, err := counter.Increment(context.Background(), "k", 60); err == nil {
		t.Fatal("expected an error for an unparseable numeric hit_count value")
	}
}

// TestDynamoRateCounter_Increment_ClientErrorPropagates verifies a DynamoDB
// client error is returned, not swallowed.
func TestDynamoRateCounter_Increment_ClientErrorPropagates(t *testing.T) {
	fake := &fakeDynamoUpdateClient{updateErr: errors.New("ddb down")}
	counter := &bridge.DynamoRateCounter{Client: fake, TableName: "t"}

	if _, err := counter.Increment(context.Background(), "k", 60); err == nil {
		t.Fatal("expected the client error to propagate")
	}
}

// TestDynamoRateCounter_Increment_TTLPassedThrough verifies the ttlSeconds the
// CALLER supplies is what ends up in the :exp expression value (now + ttl),
// not some other constant — this is what makes the bucket self-reap on the
// window the caller actually asked for.
func TestDynamoRateCounter_Increment_TTLPassedThrough(t *testing.T) {
	fake := &fakeDynamoUpdateClient{updateOutput: hitCountOutput(1)}
	counter := &bridge.DynamoRateCounter{Client: fake, TableName: "t"}

	const ttlSeconds = 120
	before := time.Now().Unix()
	if _, err := counter.Increment(context.Background(), "k", ttlSeconds); err != nil {
		t.Fatalf("Increment returned error: %v", err)
	}
	after := time.Now().Unix()

	expAttr, ok := fake.updateInput.ExpressionAttributeValues[":exp"].(*dynamodbtypes.AttributeValueMemberN)
	if !ok {
		t.Fatal("ExpressionAttributeValues[\":exp\"] missing or not a Number")
	}
	exp, err := strconv.ParseInt(expAttr.Value, 10, 64)
	if err != nil {
		t.Fatalf(":exp value %q did not parse as int64: %v", expAttr.Value, err)
	}
	if exp < before+ttlSeconds || exp > after+ttlSeconds {
		t.Errorf(":exp = %d; want in [%d, %d] (now + the caller-supplied ttlSeconds=%d)",
			exp, before+ttlSeconds, after+ttlSeconds, ttlSeconds)
	}
}

// ============================================================
// DynamoAliasResolver
// ============================================================

// fakeDynamoQueryClient implements bridge.DynamoQueryPutter (GetItem, PutItem,
// Query) for DynamoAliasResolver tests. PutItem is unused by this adapter but
// required to satisfy the interface.
type fakeDynamoQueryClient struct {
	queryOutput *dynamodb.QueryOutput
	queryErr    error

	getItemInput  *dynamodb.GetItemInput
	getItemOutput *dynamodb.GetItemOutput
	getItemErr    error
}

func (f *fakeDynamoQueryClient) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return f.queryOutput, nil
}

func (f *fakeDynamoQueryClient) GetItem(_ context.Context, params *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.getItemInput = params
	if f.getItemErr != nil {
		return nil, f.getItemErr
	}
	return f.getItemOutput, nil
}

func (f *fakeDynamoQueryClient) PutItem(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}

// TestDynamoAliasResolver_ResolveByAliasWithStatus_Hit verifies the happy path:
// a single GSI item returns its sandbox_id and status.
func TestDynamoAliasResolver_ResolveByAliasWithStatus_Hit(t *testing.T) {
	fake := &fakeDynamoQueryClient{queryOutput: &dynamodb.QueryOutput{
		Items: []map[string]dynamodbtypes.AttributeValue{
			{
				"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "wh-abc123"},
				"status":     &dynamodbtypes.AttributeValueMemberS{Value: "running"},
			},
		},
	}}
	resolver := &bridge.DynamoAliasResolver{Client: fake, TableName: "km-sandboxes"}

	id, status, err := resolver.ResolveByAliasWithStatus(context.Background(), "ir-bot")
	if err != nil {
		t.Fatalf("ResolveByAliasWithStatus returned error: %v", err)
	}
	if id != "wh-abc123" {
		t.Errorf("id = %q; want %q", id, "wh-abc123")
	}
	if status != "running" {
		t.Errorf("status = %q; want %q", status, "running")
	}
}

// TestDynamoAliasResolver_ResolveByAliasWithStatus_AbsentIsError verifies that
// zero GSI items returns an error — the caller treats this as the cold-create
// trigger, so silence here would silently skip dispatch instead.
func TestDynamoAliasResolver_ResolveByAliasWithStatus_AbsentIsError(t *testing.T) {
	fake := &fakeDynamoQueryClient{queryOutput: &dynamodb.QueryOutput{Items: nil}}
	resolver := &bridge.DynamoAliasResolver{Client: fake, TableName: "km-sandboxes"}

	if _, _, err := resolver.ResolveByAliasWithStatus(context.Background(), "no-such-alias"); err == nil {
		t.Fatal("expected an error for an absent alias (the cold-create trigger)")
	}
}

// TestDynamoAliasResolver_ResolveByAliasWithStatus_AmbiguousReturnsError pins
// the CURRENT code's behavior when the alias-index GSI returns more than one
// item for the same alias: it returns an error naming the ambiguity, rather
// than picking the first/last item. Per review instruction, this test asserts
// what the code actually does; my assessment (not a change) is in the fix
// report — failing closed on an ambiguous alias looks like the right call
// (same as pkg/github/bridge's identical code), since silently picking one of
// two sandboxes for the same alias could dispatch to the wrong box.
func TestDynamoAliasResolver_ResolveByAliasWithStatus_AmbiguousReturnsError(t *testing.T) {
	fake := &fakeDynamoQueryClient{queryOutput: &dynamodb.QueryOutput{
		Items: []map[string]dynamodbtypes.AttributeValue{
			{"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "wh-a"}},
			{"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "wh-b"}},
		},
	}}
	resolver := &bridge.DynamoAliasResolver{Client: fake, TableName: "km-sandboxes"}

	_, _, err := resolver.ResolveByAliasWithStatus(context.Background(), "dup-alias")
	if err == nil {
		t.Fatal("expected an error when the alias-index GSI returns >1 item for one alias")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error = %q; want it to name the ambiguity (pinning current code intent)", err.Error())
	}
}

// TestDynamoAliasResolver_QueueURL_ReadsWebhookInboundQueueUrlAttribute asserts
// BOTH the returned value and the exact attribute/ProjectionExpression name —
// a rename to some other bridge's convention (e.g. github_inbound_queue_url)
// must break this test, since Task 8's create-flow writes exactly this name.
func TestDynamoAliasResolver_QueueURL_ReadsWebhookInboundQueueUrlAttribute(t *testing.T) {
	fake := &fakeDynamoQueryClient{getItemOutput: &dynamodb.GetItemOutput{
		Item: map[string]dynamodbtypes.AttributeValue{
			"webhook_inbound_queue_url": &dynamodbtypes.AttributeValueMemberS{Value: "https://sqs/wh-abc123.fifo"},
		},
	}}
	resolver := &bridge.DynamoAliasResolver{Client: fake, TableName: "km-sandboxes"}

	url, err := resolver.QueueURL(context.Background(), "wh-abc123")
	if err != nil {
		t.Fatalf("QueueURL returned error: %v", err)
	}
	if url != "https://sqs/wh-abc123.fifo" {
		t.Errorf("url = %q; want %q", url, "https://sqs/wh-abc123.fifo")
	}

	if fake.getItemInput == nil || fake.getItemInput.ProjectionExpression == nil {
		t.Fatal("GetItem was not called with a ProjectionExpression")
	}
	if got := *fake.getItemInput.ProjectionExpression; got != "webhook_inbound_queue_url" {
		t.Errorf("ProjectionExpression = %q; want %q", got, "webhook_inbound_queue_url")
	}
}

// TestDynamoAliasResolver_QueueURL_AbsentAttributeIsError verifies that a row
// missing webhook_inbound_queue_url (inbound never provisioned) returns an
// error rather than an empty URL the caller might send to.
func TestDynamoAliasResolver_QueueURL_AbsentAttributeIsError(t *testing.T) {
	fake := &fakeDynamoQueryClient{getItemOutput: &dynamodb.GetItemOutput{
		Item: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "wh-abc123"},
		},
	}}
	resolver := &bridge.DynamoAliasResolver{Client: fake, TableName: "km-sandboxes"}

	if _, err := resolver.QueueURL(context.Background(), "wh-abc123"); err == nil {
		t.Fatal("expected an error when webhook_inbound_queue_url is absent")
	}
}

// ============================================================
// WebhookSQSAdapter.Send
// ============================================================

// fakeSQSClient captures the most recent SendMessage input.
type fakeSQSClient struct {
	lastInput *sqs.SendMessageInput
	err       error
}

func (f *fakeSQSClient) SendMessage(_ context.Context, params *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.lastInput = params
	if f.err != nil {
		return nil, f.err
	}
	return &sqs.SendMessageOutput{}, nil
}

// TestWebhookSQSAdapter_Send_MessageGroupIdIsCallerGroupID verifies groupID
// (the sandbox id, per bridge.QueueSender's doc) is passed straight through as
// MessageGroupId — this is what makes delivery strictly serial per box.
func TestWebhookSQSAdapter_Send_MessageGroupIdIsCallerGroupID(t *testing.T) {
	fake := &fakeSQSClient{}
	adapter := &bridge.WebhookSQSAdapter{Client: fake}

	if err := adapter.Send(context.Background(), "https://sqs/q.fifo", "wh-abc123", `{"prompt":"go"}`); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if fake.lastInput == nil {
		t.Fatal("SendMessage was not called")
	}
	if got := awssdk.ToString(fake.lastInput.MessageGroupId); got != "wh-abc123" {
		t.Errorf("MessageGroupId = %q; want the sandbox id %q", got, "wh-abc123")
	}
}

// TestWebhookSQSAdapter_Send_DedupIDDiffersForDifferentBodies verifies that two
// distinct payloads (distinct envelope/prompt) get distinct
// MessageDeduplicationId values — necessary since bridge.QueueSender.Send takes
// no caller-supplied dedup id and the queue has content_based_deduplication=false.
func TestWebhookSQSAdapter_Send_DedupIDDiffersForDifferentBodies(t *testing.T) {
	fake := &fakeSQSClient{}
	adapter := &bridge.WebhookSQSAdapter{Client: fake}

	if err := adapter.Send(context.Background(), "url", "sb-1", `{"id":"1"}`); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	first := awssdk.ToString(fake.lastInput.MessageDeduplicationId)

	if err := adapter.Send(context.Background(), "url", "sb-1", `{"id":"2"}`); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	second := awssdk.ToString(fake.lastInput.MessageDeduplicationId)

	if first == "" || second == "" {
		t.Fatal("MessageDeduplicationId must not be empty")
	}
	if first == second {
		t.Errorf("dedup id must differ for distinct bodies; got %q for both", first)
	}
}

// TestWebhookSQSAdapter_Send_DedupIDMatchesForIdenticalBodies verifies a
// retried Send with byte-identical content derives the SAME dedup id — the
// mechanism that lets it coalesce inside SQS's 5-minute in-flight dedup window
// instead of enqueuing a true duplicate.
func TestWebhookSQSAdapter_Send_DedupIDMatchesForIdenticalBodies(t *testing.T) {
	fake := &fakeSQSClient{}
	adapter := &bridge.WebhookSQSAdapter{Client: fake}
	body := `{"id":"same"}`

	if err := adapter.Send(context.Background(), "url", "sb-1", body); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	first := awssdk.ToString(fake.lastInput.MessageDeduplicationId)

	if err := adapter.Send(context.Background(), "url", "sb-1", body); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	second := awssdk.ToString(fake.lastInput.MessageDeduplicationId)

	if first != second {
		t.Errorf("dedup id must be identical for byte-identical bodies (so a "+
			"retried Send coalesces within SQS's dedup window); got %q vs %q", first, second)
	}
}

// ============================================================
// EC2Resumer — terminal vs. transient ErrNoResumableInstance split
// ============================================================

// fakeEC2Client implements bridge.EC2StartAPI for EC2Resumer tests.
type fakeEC2Client struct {
	describeResponses []*ec2.DescribeInstancesOutput
	describeCallCount int
	describeErr       error

	startCalled bool
	startErr    error
}

func (f *fakeEC2Client) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	idx := f.describeCallCount
	if idx >= len(f.describeResponses) {
		idx = len(f.describeResponses) - 1
	}
	f.describeCallCount++
	return f.describeResponses[idx], nil
}

func (f *fakeEC2Client) StartInstances(_ context.Context, _ *ec2.StartInstancesInput, _ ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	f.startCalled = true
	if f.startErr != nil {
		return nil, f.startErr
	}
	return &ec2.StartInstancesOutput{}, nil
}

func makeInstance(id string, state ec2types.InstanceStateName) ec2types.Instance {
	return ec2types.Instance{
		InstanceId: awssdk.String(id),
		State:      &ec2types.InstanceState{Name: state},
	}
}

func singleReservation(instances ...ec2types.Instance) *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: instances}},
	}
}

func emptyDescribe() *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{}
}

// TestEC2Resumer_NoInstances_WrapsErrNoResumableInstance is the terminal case:
// zero stopped/stopping instances means the box is genuinely gone (orphaned
// alias row). The caller branches on errors.Is to delete the stale row and
// cold-create — this MUST wrap the sentinel.
func TestEC2Resumer_NoInstances_WrapsErrNoResumableInstance(t *testing.T) {
	fake := &fakeEC2Client{describeResponses: []*ec2.DescribeInstancesOutput{emptyDescribe()}}
	resumer := &bridge.EC2Resumer{Client: fake, SandboxIDTagKey: "km:sandbox-id"}

	err := resumer.StartSandbox(context.Background(), "wh-gone")
	if err == nil {
		t.Fatal("expected an error when no resumable instances exist")
	}
	if !errors.Is(err, bridge.ErrNoResumableInstance) {
		t.Errorf("errors.Is(err, ErrNoResumableInstance) = false; want true (err=%v)", err)
	}
	if fake.startCalled {
		t.Error("StartInstances must NOT be called when no instances were found")
	}
}

// TestEC2Resumer_DescribeInstancesError_DoesNotWrapErrNoResumableInstance is
// the transient case: a DescribeInstances API error (e.g. a throttle or expired
// credentials) says nothing about whether the instance exists. Wrapping the
// sentinel here would make the caller delete a live sandbox's DynamoDB row on a
// blip — this MUST NOT satisfy errors.Is.
func TestEC2Resumer_DescribeInstancesError_DoesNotWrapErrNoResumableInstance(t *testing.T) {
	fake := &fakeEC2Client{describeErr: errors.New("AWS: RequestExpired")}
	resumer := &bridge.EC2Resumer{Client: fake, SandboxIDTagKey: "km:sandbox-id"}

	err := resumer.StartSandbox(context.Background(), "wh-err")
	if err == nil {
		t.Fatal("expected an error from the DescribeInstances API failure")
	}
	if errors.Is(err, bridge.ErrNoResumableInstance) {
		t.Error("a transient DescribeInstances error must NOT satisfy errors.Is(ErrNoResumableInstance) " +
			"— the caller must retain its log-and-enqueue retry, not delete a live sandbox row")
	}
}

// TestEC2Resumer_StartInstancesError_DoesNotWrapErrNoResumableInstance covers
// the third branch: the instance WAS found (it's in the describe response) but
// StartInstances itself failed. The instance clearly exists, so this must not
// trigger the terminal delete-row-and-cold-create path either.
func TestEC2Resumer_StartInstancesError_DoesNotWrapErrNoResumableInstance(t *testing.T) {
	fake := &fakeEC2Client{
		describeResponses: []*ec2.DescribeInstancesOutput{
			singleReservation(makeInstance("i-stopped123", ec2types.InstanceStateNameStopped)),
		},
		startErr: errors.New("AWS: IncorrectInstanceState"),
	}
	resumer := &bridge.EC2Resumer{Client: fake, SandboxIDTagKey: "km:sandbox-id"}

	err := resumer.StartSandbox(context.Background(), "wh-startfail")
	if err == nil {
		t.Fatal("expected an error from the StartInstances API failure")
	}
	if errors.Is(err, bridge.ErrNoResumableInstance) {
		t.Error("a StartInstances failure must NOT satisfy errors.Is(ErrNoResumableInstance) " +
			"— the instance was just found by DescribeInstances, so it is not the orphaned-row case")
	}
}
