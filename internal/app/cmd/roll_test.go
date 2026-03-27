package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	cmd "github.com/whereiskurt/klankrmkr/internal/app/cmd"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ============================================================
// Mock implementations
// ============================================================

// mockRollSSM implements RollSSMAPI.
type mockRollSSM struct {
	putParams        []ssm.PutParameterInput
	getParams        map[string]string
	sendCommandCalls []ssm.SendCommandInput
	getByPathResult  []ssmtypes.Parameter
	sendCommandErr   error
	putErr           error
	getParamErr      error
}

func (m *mockRollSSM) PutParameter(ctx context.Context, input *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	if m.putErr != nil {
		return nil, m.putErr
	}
	m.putParams = append(m.putParams, *input)
	return &ssm.PutParameterOutput{}, nil
}

func (m *mockRollSSM) GetParameter(ctx context.Context, input *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if m.getParamErr != nil {
		return nil, m.getParamErr
	}
	if m.getParams != nil {
		if val, ok := m.getParams[awssdk.ToString(input.Name)]; ok {
			return &ssm.GetParameterOutput{
				Parameter: &ssmtypes.Parameter{
					Name:  input.Name,
					Value: awssdk.String(val),
				},
			}, nil
		}
	}
	return nil, &ssmtypes.ParameterNotFound{}
}

func (m *mockRollSSM) DeleteParameter(ctx context.Context, input *ssm.DeleteParameterInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
	return &ssm.DeleteParameterOutput{}, nil
}

func (m *mockRollSSM) GetParametersByPath(ctx context.Context, input *ssm.GetParametersByPathInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	return &ssm.GetParametersByPathOutput{Parameters: m.getByPathResult}, nil
}

func (m *mockRollSSM) SendCommand(ctx context.Context, input *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	if m.sendCommandErr != nil {
		return nil, m.sendCommandErr
	}
	m.sendCommandCalls = append(m.sendCommandCalls, *input)
	return &ssm.SendCommandOutput{}, nil
}

// mockRollKMS implements RollKMSAPI.
type mockRollKMS struct {
	describeKeyResult   *kms.DescribeKeyOutput
	rotateErr           error
	describeErr         error
	rotateOnDemandCalls int
}

func (m *mockRollKMS) DescribeKey(ctx context.Context, input *kms.DescribeKeyInput, optFns ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	if m.describeErr != nil {
		return nil, m.describeErr
	}
	if m.describeKeyResult != nil {
		return m.describeKeyResult, nil
	}
	return &kms.DescribeKeyOutput{KeyMetadata: &kmstypes.KeyMetadata{
		KeyId: input.KeyId,
	}}, nil
}

func (m *mockRollKMS) RotateKeyOnDemand(ctx context.Context, input *kms.RotateKeyOnDemandInput, optFns ...func(*kms.Options)) (*kms.RotateKeyOnDemandOutput, error) {
	if m.rotateErr != nil {
		return nil, m.rotateErr
	}
	m.rotateOnDemandCalls++
	return &kms.RotateKeyOnDemandOutput{}, nil
}

// mockRollS3 implements RollS3API.
type mockRollS3 struct {
	putCalls   []s3.PutObjectInput
	existingCA []byte
	putErr     error
}

func (m *mockRollS3) PutObject(ctx context.Context, input *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if m.putErr != nil {
		return nil, m.putErr
	}
	m.putCalls = append(m.putCalls, *input)
	return &s3.PutObjectOutput{}, nil
}

func (m *mockRollS3) GetObject(ctx context.Context, input *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.existingCA != nil {
		return &s3.GetObjectOutput{
			Body: io.NopCloser(bytes.NewReader(m.existingCA)),
		}, nil
	}
	return nil, errors.New("not found")
}

// mockRollDynamo implements IdentityTableAPI (RollDynamoAPI).
type mockRollDynamo struct {
	putCalls  []dynamodb.PutItemInput
	getResult map[string]dynamodbtypes.AttributeValue
}

func (m *mockRollDynamo) PutItem(ctx context.Context, input *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	m.putCalls = append(m.putCalls, *input)
	return &dynamodb.PutItemOutput{}, nil
}

func (m *mockRollDynamo) GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if m.getResult != nil {
		return &dynamodb.GetItemOutput{Item: m.getResult}, nil
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (m *mockRollDynamo) DeleteItem(ctx context.Context, input *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}

// mockRollCW implements RotationCWAPI.
type mockRollCW struct {
	events []cloudwatchlogs.PutLogEventsInput
}

func (m *mockRollCW) CreateLogGroup(ctx context.Context, input *cloudwatchlogs.CreateLogGroupInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogGroupOutput, error) {
	return &cloudwatchlogs.CreateLogGroupOutput{}, nil
}

func (m *mockRollCW) CreateLogStream(ctx context.Context, input *cloudwatchlogs.CreateLogStreamInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogStreamOutput, error) {
	return &cloudwatchlogs.CreateLogStreamOutput{}, nil
}

func (m *mockRollCW) PutLogEvents(ctx context.Context, input *cloudwatchlogs.PutLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutLogEventsOutput, error) {
	m.events = append(m.events, *input)
	return &cloudwatchlogs.PutLogEventsOutput{}, nil
}

// mockRollECS implements RollECSAPI.
type mockRollECS struct {
	listTasksResult []string // task ARNs
	stopTaskCalls   []ecs.StopTaskInput
	listErr         error
}

func (m *mockRollECS) ListTasks(ctx context.Context, input *ecs.ListTasksInput, optFns ...func(*ecs.Options)) (*ecs.ListTasksOutput, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return &ecs.ListTasksOutput{TaskArns: m.listTasksResult}, nil
}

func (m *mockRollECS) StopTask(ctx context.Context, input *ecs.StopTaskInput, optFns ...func(*ecs.Options)) (*ecs.StopTaskOutput, error) {
	m.stopTaskCalls = append(m.stopTaskCalls, *input)
	return &ecs.StopTaskOutput{Task: &ecstypes.Task{TaskArn: input.Task}}, nil
}

// mockRollEC2 implements RollEC2API.
type mockRollEC2 struct {
	instanceIDs []string
}

func (m *mockRollEC2) DescribeInstances(ctx context.Context, input *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if len(m.instanceIDs) == 0 {
		return &ec2.DescribeInstancesOutput{}, nil
	}
	var instances []ec2types.Instance
	for _, id := range m.instanceIDs {
		instances = append(instances, ec2types.Instance{InstanceId: awssdk.String(id)})
	}
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{
			{Instances: instances},
		},
	}, nil
}

// mockSandboxLister implements SandboxLister.
type mockSandboxLister struct {
	sandboxes []kmaws.SandboxRecord
	err       error
}

func (m *mockSandboxLister) ListSandboxes(ctx context.Context, useTagScan bool) ([]kmaws.SandboxRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.sandboxes, nil
}

// ============================================================
// Tests
// ============================================================

// TestRollCreds_AllMode verifies that km roll creds with no flags:
// - Rotates platform creds (proxy CA + KMS)
// - Enumerates sandboxes
// - Rotates each sandbox identity + SSM re-encryption
// - Writes CloudWatch audit events
func TestRollCreds_AllMode(t *testing.T) {
	lister := &mockSandboxLister{
		sandboxes: []kmaws.SandboxRecord{
			{SandboxID: "sb-11111111", Status: "running", Substrate: "ec2"},
			{SandboxID: "sb-22222222", Status: "running", Substrate: "ec2"},
		},
	}
	ssmClient := &mockRollSSM{}
	kmsClient := &mockRollKMS{}
	s3Client := &mockRollS3{}
	cwClient := &mockRollCW{}
	dynamoClient := &mockRollDynamo{}
	ec2Client := &mockRollEC2{}
	ecsClient := &mockRollECS{}

	deps := &cmd.RollDeps{
		SSMClient:    ssmClient,
		KMSClient:    kmsClient,
		S3Client:     s3Client,
		DynamoClient: dynamoClient,
		CWClient:     cwClient,
		ECSClient:    ecsClient,
		EC2Client:    ec2Client,
		Lister:       lister,
	}

	rollCmd := cmd.NewRollCmdWithDeps(nil, deps)
	rollCmd.SetArgs([]string{"creds"})
	var buf bytes.Buffer
	rollCmd.SetOut(&buf)

	err := rollCmd.Execute()
	if err != nil {
		t.Fatalf("km roll creds (all mode) returned error: %v", err)
	}

	// Proxy CA rotation uploads 2 S3 objects (cert + key)
	if len(s3Client.putCalls) < 2 {
		t.Errorf("expected at least 2 S3 PutObject calls for CA cert+key, got %d", len(s3Client.putCalls))
	}

	// KMS rotation called once
	if kmsClient.rotateOnDemandCalls != 1 {
		t.Errorf("expected 1 KMS RotateKeyOnDemand call, got %d", kmsClient.rotateOnDemandCalls)
	}

	// Sandbox identities rotated: DynamoDB PutItem calls (one per sandbox)
	if len(dynamoClient.putCalls) < 2 {
		t.Errorf("expected at least 2 DynamoDB PutItem calls (one per sandbox), got %d", len(dynamoClient.putCalls))
	}

	// Audit events written
	if len(cwClient.events) == 0 {
		t.Error("expected CloudWatch audit events, got none")
	}
}

// TestRollCreds_SandboxMode verifies --sandbox flag:
// - Only rotates specified sandbox identity + SSM params
// - Does NOT rotate platform creds (proxy CA, KMS)
func TestRollCreds_SandboxMode(t *testing.T) {
	lister := &mockSandboxLister{}
	ssmClient := &mockRollSSM{}
	kmsClient := &mockRollKMS{}
	s3Client := &mockRollS3{}
	cwClient := &mockRollCW{}
	dynamoClient := &mockRollDynamo{}

	deps := &cmd.RollDeps{
		SSMClient:    ssmClient,
		KMSClient:    kmsClient,
		S3Client:     s3Client,
		DynamoClient: dynamoClient,
		CWClient:     cwClient,
		ECSClient:    &mockRollECS{},
		EC2Client:    &mockRollEC2{},
		Lister:       lister,
	}

	rollCmd := cmd.NewRollCmdWithDeps(nil, deps)
	rollCmd.SetArgs([]string{"creds", "--sandbox", "sb-12345678"})
	var buf bytes.Buffer
	rollCmd.SetOut(&buf)

	err := rollCmd.Execute()
	if err != nil {
		t.Fatalf("km roll creds --sandbox returned error: %v", err)
	}

	// Sandbox identity rotated (DynamoDB PutItem)
	if len(dynamoClient.putCalls) == 0 {
		t.Error("expected DynamoDB PutItem for sandbox identity rotation")
	}

	// Platform creds NOT rotated: no S3 PutObject (no proxy CA rotation)
	if len(s3Client.putCalls) != 0 {
		t.Errorf("expected no S3 calls in sandbox-only mode, got %d", len(s3Client.putCalls))
	}

	// KMS NOT rotated
	if kmsClient.rotateOnDemandCalls != 0 {
		t.Errorf("expected no KMS rotation in sandbox-only mode, got %d calls", kmsClient.rotateOnDemandCalls)
	}

	// Audit events written
	if len(cwClient.events) == 0 {
		t.Error("expected CloudWatch audit events for sandbox mode")
	}
}

// TestRollCreds_PlatformMode verifies --platform flag:
// - Rotates proxy CA + KMS
// - Does NOT enumerate sandboxes
func TestRollCreds_PlatformMode(t *testing.T) {
	// If this lister is called, the test would fail via error
	lister := &mockSandboxLister{
		err: errors.New("lister should not be called in platform mode"),
	}
	ssmClient := &mockRollSSM{}
	kmsClient := &mockRollKMS{}
	s3Client := &mockRollS3{}
	cwClient := &mockRollCW{}
	dynamoClient := &mockRollDynamo{}

	deps := &cmd.RollDeps{
		SSMClient:    ssmClient,
		KMSClient:    kmsClient,
		S3Client:     s3Client,
		DynamoClient: dynamoClient,
		CWClient:     cwClient,
		ECSClient:    &mockRollECS{},
		EC2Client:    &mockRollEC2{},
		Lister:       lister,
	}

	rollCmd := cmd.NewRollCmdWithDeps(nil, deps)
	rollCmd.SetArgs([]string{"creds", "--platform"})
	var buf bytes.Buffer
	rollCmd.SetOut(&buf)

	err := rollCmd.Execute()
	if err != nil {
		t.Fatalf("km roll creds --platform returned error: %v", err)
	}

	// Proxy CA uploaded (2 S3 PutObject calls: cert + key)
	if len(s3Client.putCalls) < 2 {
		t.Errorf("expected at least 2 S3 PutObject calls, got %d", len(s3Client.putCalls))
	}

	// KMS rotated
	if kmsClient.rotateOnDemandCalls != 1 {
		t.Errorf("expected 1 KMS rotation, got %d", kmsClient.rotateOnDemandCalls)
	}

	// DynamoDB NOT called (no sandbox identity rotation)
	if len(dynamoClient.putCalls) != 0 {
		t.Errorf("expected no DynamoDB calls in platform-only mode, got %d", len(dynamoClient.putCalls))
	}

	// Audit events written
	if len(cwClient.events) == 0 {
		t.Error("expected CloudWatch audit events for platform mode")
	}
}

// TestRollCreds_PlatformMode_SkipsGitHubKeyWhenNotProvided verifies that
// --platform without --github-private-key-file skips GitHub key rotation.
func TestRollCreds_PlatformMode_SkipsGitHubKeyWhenNotProvided(t *testing.T) {
	ssmClient := &mockRollSSM{}
	deps := &cmd.RollDeps{
		SSMClient:    ssmClient,
		KMSClient:    &mockRollKMS{},
		S3Client:     &mockRollS3{},
		DynamoClient: &mockRollDynamo{},
		CWClient:     &mockRollCW{},
		ECSClient:    &mockRollECS{},
		EC2Client:    &mockRollEC2{},
		Lister:       &mockSandboxLister{},
	}

	rollCmd := cmd.NewRollCmdWithDeps(nil, deps)
	rollCmd.SetArgs([]string{"creds", "--platform"})
	var buf bytes.Buffer
	rollCmd.SetOut(&buf)

	err := rollCmd.Execute()
	if err != nil {
		t.Fatalf("km roll creds --platform returned error: %v", err)
	}

	// No SSM PutParameter for github private key
	for _, call := range ssmClient.putParams {
		if awssdk.ToString(call.Name) == "/km/config/github/private-key" {
			t.Error("expected no GitHub private-key SSM write without --github-private-key-file")
		}
	}
}

// TestRollCreds_PerSandboxFailureIsNonFatal verifies that:
// - A sandbox that fails rotation does not abort bulk rotation
// - A summary is printed at the end showing successes and failures
func TestRollCreds_PerSandboxFailureIsNonFatal(t *testing.T) {
	lister := &mockSandboxLister{
		sandboxes: []kmaws.SandboxRecord{
			{SandboxID: "sb-good1111", Status: "running", Substrate: "ec2"},
			{SandboxID: "sb-bad11111", Status: "running", Substrate: "ec2"},
			{SandboxID: "sb-good2222", Status: "running", Substrate: "ec2"},
		},
	}

	deps := &cmd.RollDeps{
		SSMClient:    &failingSSM{badSandbox: "sb-bad11111"},
		KMSClient:    &mockRollKMS{},
		S3Client:     &mockRollS3{},
		DynamoClient: &mockRollDynamo{},
		CWClient:     &mockRollCW{},
		ECSClient:    &mockRollECS{},
		EC2Client:    &mockRollEC2{},
		Lister:       lister,
	}

	rollCmd := cmd.NewRollCmdWithDeps(nil, deps)
	rollCmd.SetArgs([]string{"creds"})
	var buf bytes.Buffer
	rollCmd.SetOut(&buf)

	err := rollCmd.Execute()
	// The command should NOT return an error — per-sandbox failures are non-fatal
	if err != nil {
		t.Fatalf("km roll creds should not return error on per-sandbox failure, got: %v", err)
	}

	output := buf.String()
	// Summary should mention failures
	if !containsAny(output, "failed", "failure", "error", "Error") {
		t.Errorf("expected failure summary in output, got: %q", output)
	}
}

// failingSSM fails SSM PutParameter for a specific sandbox.
type failingSSM struct {
	mockRollSSM
	badSandbox string
}

func (f *failingSSM) PutParameter(ctx context.Context, input *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	name := awssdk.ToString(input.Name)
	if containsStr(name, f.badSandbox) {
		return nil, errors.New("simulated SSM failure for bad sandbox")
	}
	f.putParams = append(f.putParams, *input)
	return &ssm.PutParameterOutput{}, nil
}

func (f *failingSSM) GetParametersByPath(ctx context.Context, input *ssm.GetParametersByPathInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	return &ssm.GetParametersByPathOutput{Parameters: nil}, nil
}

func containsStr(s, sub string) bool {
	if sub == "" || len(s) < len(sub) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if containsStr(s, sub) {
			return true
		}
	}
	return false
}

// TestRollCreds_EC2ProxyRestart verifies that after proxy CA rotation,
// SSM SendCommand is issued to each running EC2 sandbox.
func TestRollCreds_EC2ProxyRestart(t *testing.T) {
	lister := &mockSandboxLister{
		sandboxes: []kmaws.SandboxRecord{
			{SandboxID: "sb-ec2aaaaa", Status: "running", Substrate: "ec2"},
		},
	}
	ssmClient := &mockRollSSM{}
	ec2Client := &mockRollEC2{instanceIDs: []string{"i-0abcdef1234567890"}}

	deps := &cmd.RollDeps{
		SSMClient:    ssmClient,
		KMSClient:    &mockRollKMS{},
		S3Client:     &mockRollS3{},
		DynamoClient: &mockRollDynamo{},
		CWClient:     &mockRollCW{},
		ECSClient:    &mockRollECS{},
		EC2Client:    ec2Client,
		Lister:       lister,
	}

	rollCmd := cmd.NewRollCmdWithDeps(nil, deps)
	rollCmd.SetArgs([]string{"creds"})
	var buf bytes.Buffer
	rollCmd.SetOut(&buf)

	err := rollCmd.Execute()
	if err != nil {
		t.Fatalf("km roll creds returned error: %v", err)
	}

	// SSM SendCommand should be issued for EC2 proxy restart
	if len(ssmClient.sendCommandCalls) == 0 {
		t.Error("expected SSM SendCommand for EC2 proxy restart, got none")
	}
}

// TestRollCreds_ECSProxyRestart_ForceRestart verifies that --force-restart
// causes StopTask to be called on running ECS sandbox tasks.
func TestRollCreds_ECSProxyRestart_ForceRestart(t *testing.T) {
	lister := &mockSandboxLister{
		sandboxes: []kmaws.SandboxRecord{
			{SandboxID: "sb-ecs11111", Status: "running", Substrate: "ecs"},
		},
	}
	ecsClient := &mockRollECS{
		listTasksResult: []string{"arn:aws:ecs:us-east-1:123456789012:task/sb-ecs11111/abc123"},
	}

	deps := &cmd.RollDeps{
		SSMClient:    &mockRollSSM{},
		KMSClient:    &mockRollKMS{},
		S3Client:     &mockRollS3{},
		DynamoClient: &mockRollDynamo{},
		CWClient:     &mockRollCW{},
		ECSClient:    ecsClient,
		EC2Client:    &mockRollEC2{},
		Lister:       lister,
	}

	rollCmd := cmd.NewRollCmdWithDeps(nil, deps)
	rollCmd.SetArgs([]string{"creds", "--force-restart"})
	var buf bytes.Buffer
	rollCmd.SetOut(&buf)

	err := rollCmd.Execute()
	if err != nil {
		t.Fatalf("km roll creds --force-restart returned error: %v", err)
	}

	// ECS StopTask should be called
	if len(ecsClient.stopTaskCalls) == 0 {
		t.Error("expected ECS StopTask calls with --force-restart, got none")
	}
}

// TestRollCreds_AuditEventsWritten verifies CloudWatch audit events
// are written for each rotation step.
func TestRollCreds_AuditEventsWritten(t *testing.T) {
	cwClient := &mockRollCW{}
	lister := &mockSandboxLister{
		sandboxes: []kmaws.SandboxRecord{
			{SandboxID: "sb-audit111", Status: "running", Substrate: "ec2"},
		},
	}

	deps := &cmd.RollDeps{
		SSMClient:    &mockRollSSM{},
		KMSClient:    &mockRollKMS{},
		S3Client:     &mockRollS3{},
		DynamoClient: &mockRollDynamo{},
		CWClient:     cwClient,
		ECSClient:    &mockRollECS{},
		EC2Client:    &mockRollEC2{},
		Lister:       lister,
	}

	rollCmd := cmd.NewRollCmdWithDeps(nil, deps)
	rollCmd.SetArgs([]string{"creds"})
	var buf bytes.Buffer
	rollCmd.SetOut(&buf)

	err := rollCmd.Execute()
	if err != nil {
		t.Fatalf("km roll creds returned error: %v", err)
	}

	// At minimum: proxy CA audit + KMS audit + sandbox identity audit
	if len(cwClient.events) < 3 {
		t.Errorf("expected at least 3 CloudWatch audit events, got %d", len(cwClient.events))
	}
}

// TestRollCreds_HelpShowsAllFlags verifies the roll creds command exposes all documented flags.
func TestRollCreds_HelpShowsAllFlags(t *testing.T) {
	rollCmd := cmd.NewRollCmdWithDeps(nil, &cmd.RollDeps{
		SSMClient:    &mockRollSSM{},
		KMSClient:    &mockRollKMS{},
		S3Client:     &mockRollS3{},
		DynamoClient: &mockRollDynamo{},
		CWClient:     &mockRollCW{},
		ECSClient:    &mockRollECS{},
		EC2Client:    &mockRollEC2{},
		Lister:       &mockSandboxLister{},
	})

	// Find the creds subcommand
	credsCmd, _, err := rollCmd.Find([]string{"creds"})
	if err != nil || credsCmd == nil {
		t.Fatal("expected 'creds' subcommand to exist")
	}

	requiredFlags := []string{"sandbox", "platform", "github-private-key-file", "force-restart", "json"}
	for _, flag := range requiredFlags {
		if f := credsCmd.Flags().Lookup(flag); f == nil {
			t.Errorf("expected flag --%s to be defined on 'creds' subcommand", flag)
		}
	}
}

// Ensure time package is used.
var _ = time.Now
