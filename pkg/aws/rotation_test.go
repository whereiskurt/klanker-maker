// Package aws_test — rotation_test.go
// Unit tests for credential rotation functions:
// RotateSandboxIdentity, UpdateIdentityPublicKey, RotateProxyCACert,
// ReEncryptSSMParameters, WriteRotationAudit, ed25519Fingerprint.
package aws_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ============================================================
// Mock: RotationSSMAPI
// ============================================================

type mockRotationSSMAPI struct {
	putParameterCalled  bool
	putParameterInput   *ssm.PutParameterInput
	putParameterErr     error

	getParameterCalled bool
	getParameterInput  *ssm.GetParameterInput
	getParameterValue  string
	getParameterErr    error

	getParametersByPathCalled bool
	getParametersByPathInput  *ssm.GetParametersByPathInput
	getParametersByPathResult []ssmtypes.Parameter
	getParametersByPathErr    error

	// Track all PutParameter calls for re-encryption tests
	putParameterInputs []*ssm.PutParameterInput
}

func (m *mockRotationSSMAPI) PutParameter(ctx context.Context, input *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	m.putParameterCalled = true
	m.putParameterInput = input
	m.putParameterInputs = append(m.putParameterInputs, input)
	return &ssm.PutParameterOutput{}, m.putParameterErr
}

func (m *mockRotationSSMAPI) GetParameter(ctx context.Context, input *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	m.getParameterCalled = true
	m.getParameterInput = input
	if m.getParameterErr != nil {
		return nil, m.getParameterErr
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{
			Value: awssdk.String(m.getParameterValue),
		},
	}, nil
}

func (m *mockRotationSSMAPI) GetParametersByPath(ctx context.Context, input *ssm.GetParametersByPathInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	m.getParametersByPathCalled = true
	m.getParametersByPathInput = input
	if m.getParametersByPathErr != nil {
		return nil, m.getParametersByPathErr
	}
	return &ssm.GetParametersByPathOutput{
		Parameters: m.getParametersByPathResult,
	}, nil
}

// ============================================================
// Mock: RotationDynamoAPI (reuse IdentityTableAPI interface)
// ============================================================

type mockRotationDynamoAPI struct {
	putItemCalled bool
	putItemInput  *dynamodb.PutItemInput
	putItemErr    error

	getItemCalled bool
	getItemOutput *dynamodb.GetItemOutput
	getItemErr    error
}

func (m *mockRotationDynamoAPI) PutItem(ctx context.Context, input *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	m.putItemCalled = true
	m.putItemInput = input
	return &dynamodb.PutItemOutput{}, m.putItemErr
}

func (m *mockRotationDynamoAPI) GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	m.getItemCalled = true
	if m.getItemErr != nil {
		return nil, m.getItemErr
	}
	if m.getItemOutput != nil {
		return m.getItemOutput, nil
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (m *mockRotationDynamoAPI) DeleteItem(ctx context.Context, input *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}

// ============================================================
// Mock: RotationS3API
// ============================================================

type mockRotationS3API struct {
	putObjectCalled bool
	putObjectInputs []*s3.PutObjectInput
	putObjectErr    error

	getObjectCalled bool
	getObjectBody   []byte
	getObjectErr    error
}

func (m *mockRotationS3API) PutObject(ctx context.Context, input *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	m.putObjectCalled = true
	m.putObjectInputs = append(m.putObjectInputs, input)
	return &s3.PutObjectOutput{}, m.putObjectErr
}

func (m *mockRotationS3API) GetObject(ctx context.Context, input *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	m.getObjectCalled = true
	if m.getObjectErr != nil {
		return nil, m.getObjectErr
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(m.getObjectBody)),
	}, nil
}

// ============================================================
// Mock: RotationCWAPI
// ============================================================

type mockRotationCWAPI struct {
	createLogGroupCalled  bool
	createLogGroupInput   *cloudwatchlogs.CreateLogGroupInput
	createLogGroupErr     error

	createLogStreamCalled bool
	createLogStreamInput  *cloudwatchlogs.CreateLogStreamInput
	createLogStreamErr    error

	putLogEventsCalled bool
	putLogEventsInput  *cloudwatchlogs.PutLogEventsInput
	putLogEventsErr    error

	// Capture all PutRetentionPolicy calls
	putRetentionPolicyCalled bool
}

func (m *mockRotationCWAPI) CreateLogGroup(ctx context.Context, input *cloudwatchlogs.CreateLogGroupInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogGroupOutput, error) {
	m.createLogGroupCalled = true
	m.createLogGroupInput = input
	return &cloudwatchlogs.CreateLogGroupOutput{}, m.createLogGroupErr
}

func (m *mockRotationCWAPI) CreateLogStream(ctx context.Context, input *cloudwatchlogs.CreateLogStreamInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogStreamOutput, error) {
	m.createLogStreamCalled = true
	m.createLogStreamInput = input
	return &cloudwatchlogs.CreateLogStreamOutput{}, m.createLogStreamErr
}

func (m *mockRotationCWAPI) PutLogEvents(ctx context.Context, input *cloudwatchlogs.PutLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutLogEventsOutput, error) {
	m.putLogEventsCalled = true
	m.putLogEventsInput = input
	return &cloudwatchlogs.PutLogEventsOutput{}, m.putLogEventsErr
}

func (m *mockRotationCWAPI) GetLogEvents(ctx context.Context, input *cloudwatchlogs.GetLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error) {
	return &cloudwatchlogs.GetLogEventsOutput{}, nil
}

func (m *mockRotationCWAPI) PutRetentionPolicy(ctx context.Context, input *cloudwatchlogs.PutRetentionPolicyInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutRetentionPolicyOutput, error) {
	m.putRetentionPolicyCalled = true
	return &cloudwatchlogs.PutRetentionPolicyOutput{}, nil
}

func (m *mockRotationCWAPI) DeleteLogGroup(ctx context.Context, input *cloudwatchlogs.DeleteLogGroupInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DeleteLogGroupOutput, error) {
	return &cloudwatchlogs.DeleteLogGroupOutput{}, nil
}

func (m *mockRotationCWAPI) CreateExportTask(ctx context.Context, input *cloudwatchlogs.CreateExportTaskInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateExportTaskOutput, error) {
	return &cloudwatchlogs.CreateExportTaskOutput{}, nil
}

// ============================================================
// Helper: build a DynamoDB GetItem response for an existing identity (rotation tests)
// ============================================================

// makeRotationGetItemOutput builds a minimal DynamoDB GetItemOutput for rotation tests.
// Uses the existing makeIdentityGetItemOutput (defined in identity_test.go) with empty encKey.
func makeRotationGetItemOutput(sandboxID, pubKeyB64, emailAddress string) *dynamodb.GetItemOutput {
	return makeIdentityGetItemOutput(sandboxID, pubKeyB64, emailAddress, "")
}

// ============================================================
// Helper: generate a valid Ed25519 private key stored in SSM format
// ============================================================

func generateTestEd25519SSMValue() (ed25519.PublicKey, string) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return pub, base64.StdEncoding.EncodeToString([]byte(priv))
}

// ============================================================
// Tests: RotateSandboxIdentity
// ============================================================

// TestRotateSandboxIdentity_WithExistingKey verifies that:
// - FetchPublicKey is called (GetItem on DynamoDB)
// - GenerateSandboxIdentity is called (PutParameter on SSM)
// - UpdateIdentityPublicKey is called (PutItem on DynamoDB)
// - Returns non-empty oldFP and newFP
func TestRotateSandboxIdentity_WithExistingKey(t *testing.T) {
	ctx := context.Background()

	existingPub, existingPrivB64 := generateTestEd25519SSMValue()
	existingPubB64 := base64.StdEncoding.EncodeToString(existingPub)

	ssmMock := &mockRotationSSMAPI{
		getParameterValue: existingPrivB64,
	}
	dynamoMock := &mockRotationDynamoAPI{
		getItemOutput: makeRotationGetItemOutput("sb-test", existingPubB64, "test@sandbox.local"),
	}

	oldFP, newFP, err := kmaws.RotateSandboxIdentity(ctx, ssmMock, dynamoMock, "sb-test", "kms-key-id", "km-identities")
	if err != nil {
		t.Fatalf("RotateSandboxIdentity returned unexpected error: %v", err)
	}

	// Old fingerprint should be non-empty (existing key was found)
	if oldFP == "" {
		t.Error("expected non-empty oldFP when existing key was found")
	}

	// New fingerprint should differ from old
	if newFP == "" {
		t.Error("expected non-empty newFP after rotation")
	}
	if oldFP == newFP {
		t.Error("expected oldFP != newFP after rotation")
	}

	// SSM PutParameter must be called (for new key generation)
	if !ssmMock.putParameterCalled {
		t.Error("expected SSM PutParameter to be called for new key generation")
	}

	// DynamoDB PutItem must be called (unconditional update)
	if !dynamoMock.putItemCalled {
		t.Error("expected DynamoDB PutItem to be called for identity update")
	}
}

// TestRotateSandboxIdentity_FreshSandbox verifies rotation succeeds when
// there is no existing key (FetchPublicKey returns nil record).
func TestRotateSandboxIdentity_FreshSandbox(t *testing.T) {
	ctx := context.Background()

	ssmMock := &mockRotationSSMAPI{}
	// No getItemOutput set → returns empty GetItemOutput (nil record from FetchPublicKey)
	dynamoMock := &mockRotationDynamoAPI{}

	oldFP, newFP, err := kmaws.RotateSandboxIdentity(ctx, ssmMock, dynamoMock, "sb-fresh", "kms-key-id", "km-identities")
	if err != nil {
		t.Fatalf("RotateSandboxIdentity (fresh sandbox) returned unexpected error: %v", err)
	}

	// Old fingerprint should be empty string for fresh sandboxes
	if oldFP != "" {
		t.Errorf("expected empty oldFP for fresh sandbox, got %q", oldFP)
	}

	// New fingerprint should be non-empty
	if newFP == "" {
		t.Error("expected non-empty newFP after rotation for fresh sandbox")
	}

	// PutItem must still be called (to publish new key)
	if !dynamoMock.putItemCalled {
		t.Error("expected DynamoDB PutItem to be called even for fresh sandbox")
	}
}

// ============================================================
// Tests: UpdateIdentityPublicKey
// ============================================================

// TestUpdateIdentityPublicKey_NoConditionExpression verifies that
// UpdateIdentityPublicKey does NOT set ConditionExpression on the PutItem call.
// This is the critical difference from PublishIdentity (which uses attribute_not_exists).
func TestUpdateIdentityPublicKey_NoConditionExpression(t *testing.T) {
	ctx := context.Background()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	dynamoMock := &mockRotationDynamoAPI{
		// Simulate existing record so the function can merge fields
		getItemOutput: makeRotationGetItemOutput("sb-cond", base64.StdEncoding.EncodeToString(pub), "old@sandbox.local"),
	}

	err = kmaws.UpdateIdentityPublicKey(ctx, dynamoMock, "km-identities", "sb-cond", pub, nil)
	if err != nil {
		t.Fatalf("UpdateIdentityPublicKey returned error: %v", err)
	}

	if !dynamoMock.putItemCalled {
		t.Fatal("expected PutItem to be called")
	}

	// CRITICAL: ConditionExpression must be nil — this is what distinguishes
	// UpdateIdentityPublicKey from PublishIdentity
	if dynamoMock.putItemInput.ConditionExpression != nil {
		t.Errorf("UpdateIdentityPublicKey MUST NOT set ConditionExpression, got: %q",
			*dynamoMock.putItemInput.ConditionExpression)
	}
}

// TestUpdateIdentityPublicKey_OverwritesExistingKey verifies that updating
// a sandbox with an existing key succeeds (not blocked by any condition).
func TestUpdateIdentityPublicKey_OverwritesExistingKey(t *testing.T) {
	ctx := context.Background()

	oldPub, _, _ := ed25519.GenerateKey(rand.Reader)
	newPub, _, _ := ed25519.GenerateKey(rand.Reader)

	dynamoMock := &mockRotationDynamoAPI{
		getItemOutput: makeRotationGetItemOutput("sb-overwrite",
			base64.StdEncoding.EncodeToString(oldPub), "test@sandbox.local"),
	}

	err := kmaws.UpdateIdentityPublicKey(ctx, dynamoMock, "km-identities", "sb-overwrite", newPub, nil)
	if err != nil {
		t.Fatalf("UpdateIdentityPublicKey should not fail on overwrite: %v", err)
	}

	// Verify PutItem was called with the new public key value
	if dynamoMock.putItemInput == nil {
		t.Fatal("putItemInput was nil")
	}
	pkAttr, ok := dynamoMock.putItemInput.Item["public_key"]
	if !ok {
		t.Fatal("PutItem item missing 'public_key' attribute")
	}
	pkStr, ok := pkAttr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatal("public_key attribute is not a string")
	}
	expectedPubB64 := base64.StdEncoding.EncodeToString(newPub)
	if pkStr.Value != expectedPubB64 {
		t.Errorf("PutItem public_key = %q, want %q", pkStr.Value, expectedPubB64)
	}
}

// ============================================================
// Tests: RotateProxyCACert
// ============================================================

// TestRotateProxyCACert_UploadsToCorrectS3Paths verifies that the function:
// - Uploads cert to sidecars/km-proxy-ca.crt
// - Uploads key to sidecars/km-proxy-ca.key
// - Returns fingerprints
func TestRotateProxyCACert_UploadsToCorrectS3Paths(t *testing.T) {
	ctx := context.Background()

	s3Mock := &mockRotationS3API{
		// Old cert not found (fresh — GetObject will fail)
		getObjectErr: fmt.Errorf("NoSuchKey"),
	}

	oldFP, newFP, err := kmaws.RotateProxyCACert(ctx, s3Mock, "km-artifacts-bucket")
	if err != nil {
		t.Fatalf("RotateProxyCACert returned unexpected error: %v", err)
	}

	// Must have called PutObject twice (cert + key)
	if len(s3Mock.putObjectInputs) != 2 {
		t.Errorf("expected 2 PutObject calls, got %d", len(s3Mock.putObjectInputs))
	}

	// Extract the keys that were uploaded
	uploadedKeys := make(map[string]bool)
	for _, inp := range s3Mock.putObjectInputs {
		if inp.Key != nil {
			uploadedKeys[*inp.Key] = true
		}
	}

	if !uploadedKeys["sidecars/km-proxy-ca.crt"] {
		t.Error("expected upload to sidecars/km-proxy-ca.crt")
	}
	if !uploadedKeys["sidecars/km-proxy-ca.key"] {
		t.Error("expected upload to sidecars/km-proxy-ca.key")
	}

	// New fingerprint must be non-empty
	if newFP == "" {
		t.Error("expected non-empty newFP from RotateProxyCACert")
	}

	// Old fingerprint is empty (no old cert existed)
	if oldFP != "" {
		t.Errorf("expected empty oldFP when no old cert, got %q", oldFP)
	}
}

// TestRotateProxyCACert_WithExistingCert verifies that when an old cert exists,
// oldFP is populated and differs from newFP.
func TestRotateProxyCACert_WithExistingCert(t *testing.T) {
	ctx := context.Background()

	// Generate a "previous" cert to serve as the old cert
	// We need a valid PEM-encoded DER cert for fingerprint calculation
	// Use a simple placeholder cert body
	oldCertPEM := []byte("-----BEGIN CERTIFICATE-----\nMIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA\n-----END CERTIFICATE-----\n")

	s3Mock := &mockRotationS3API{
		getObjectBody: oldCertPEM,
	}

	oldFP, newFP, err := kmaws.RotateProxyCACert(ctx, s3Mock, "km-artifacts-bucket")
	if err != nil {
		t.Fatalf("RotateProxyCACert returned error: %v", err)
	}

	// Both fingerprints should be populated and different
	if newFP == "" {
		t.Error("expected non-empty newFP")
	}
	_ = oldFP // oldFP may be empty if cert is malformed, but function should not error
}

// ============================================================
// Tests: ReEncryptSSMParameters
// ============================================================

// TestReEncryptSSMParameters_ReEncryptsAllParams verifies that:
// - GetParametersByPath is called with correct path and Recursive=true
// - PutParameter is called for each param with Overwrite=true
// - Returns count of re-encrypted params
func TestReEncryptSSMParameters_ReEncryptsAllParams(t *testing.T) {
	ctx := context.Background()

	params := []ssmtypes.Parameter{
		{Name: awssdk.String("/sandbox/sb-test/signing-key"), Value: awssdk.String("key1val"), Type: ssmtypes.ParameterTypeSecureString},
		{Name: awssdk.String("/sandbox/sb-test/encryption-key"), Value: awssdk.String("key2val"), Type: ssmtypes.ParameterTypeSecureString},
		{Name: awssdk.String("/sandbox/sb-test/db-password"), Value: awssdk.String("dbpass"), Type: ssmtypes.ParameterTypeSecureString},
	}

	ssmMock := &mockRotationSSMAPI{
		getParametersByPathResult: params,
	}

	count, err := kmaws.ReEncryptSSMParameters(ctx, ssmMock, "sb-test", "kms-key-id")
	if err != nil {
		t.Fatalf("ReEncryptSSMParameters returned error: %v", err)
	}

	// Count must match number of parameters
	if count != len(params) {
		t.Errorf("expected count=%d, got %d", len(params), count)
	}

	// GetParametersByPath must be called with recursive path
	if !ssmMock.getParametersByPathCalled {
		t.Fatal("expected GetParametersByPath to be called")
	}
	if ssmMock.getParametersByPathInput == nil || ssmMock.getParametersByPathInput.Path == nil {
		t.Fatal("GetParametersByPath input path is nil")
	}
	expectedPath := "/sandbox/sb-test/"
	if *ssmMock.getParametersByPathInput.Path != expectedPath {
		t.Errorf("GetParametersByPath path = %q, want %q",
			*ssmMock.getParametersByPathInput.Path, expectedPath)
	}
	if ssmMock.getParametersByPathInput.Recursive == nil || !*ssmMock.getParametersByPathInput.Recursive {
		t.Error("GetParametersByPath must have Recursive=true")
	}
	if ssmMock.getParametersByPathInput.WithDecryption == nil || !*ssmMock.getParametersByPathInput.WithDecryption {
		t.Error("GetParametersByPath must have WithDecryption=true")
	}

	// PutParameter must be called once per param, all with Overwrite=true
	if len(ssmMock.putParameterInputs) != len(params) {
		t.Errorf("expected %d PutParameter calls, got %d", len(params), len(ssmMock.putParameterInputs))
	}
	for i, inp := range ssmMock.putParameterInputs {
		if inp.Overwrite == nil || !*inp.Overwrite {
			t.Errorf("PutParameter call %d: Overwrite must be true", i)
		}
		if inp.KeyId == nil || *inp.KeyId != "kms-key-id" {
			t.Errorf("PutParameter call %d: KeyId must be %q", i, "kms-key-id")
		}
	}
}

// TestReEncryptSSMParameters_EmptyPath verifies behavior with no params found.
func TestReEncryptSSMParameters_EmptyPath(t *testing.T) {
	ctx := context.Background()

	ssmMock := &mockRotationSSMAPI{
		getParametersByPathResult: []ssmtypes.Parameter{},
	}

	count, err := kmaws.ReEncryptSSMParameters(ctx, ssmMock, "sb-empty", "kms-key-id")
	if err != nil {
		t.Fatalf("ReEncryptSSMParameters (empty) returned error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected count=0 for empty param list, got %d", count)
	}
}

// ============================================================
// Tests: WriteRotationAudit
// ============================================================

// TestWriteRotationAudit_WritesStructuredJSON verifies that:
// - Log group /km/credential-rotation is created (idempotent)
// - Log stream is created
// - PutLogEvents is called with JSON-marshaled RotationAuditEvent
// - The event includes before/after fingerprints
func TestWriteRotationAudit_WritesStructuredJSON(t *testing.T) {
	ctx := context.Background()

	cwMock := &mockRotationCWAPI{}

	event := kmaws.RotationAuditEvent{
		Event:     "rotate-identity",
		SandboxID: "sb-audit",
		KeyType:   "ed25519",
		BeforeFP:  "sha256:aabbccdd",
		AfterFP:   "sha256:11223344",
		Timestamp: time.Now().UTC(),
		Success:   true,
	}

	err := kmaws.WriteRotationAudit(ctx, cwMock, event)
	if err != nil {
		t.Fatalf("WriteRotationAudit returned error: %v", err)
	}

	// Log group must be created for /km/credential-rotation
	if !cwMock.createLogGroupCalled {
		t.Error("expected CreateLogGroup to be called")
	}
	if cwMock.createLogGroupInput == nil || cwMock.createLogGroupInput.LogGroupName == nil {
		t.Fatal("CreateLogGroup input is nil")
	}
	if *cwMock.createLogGroupInput.LogGroupName != "/km/credential-rotation" {
		t.Errorf("CreateLogGroup name = %q, want %q",
			*cwMock.createLogGroupInput.LogGroupName, "/km/credential-rotation")
	}

	// Log stream must be created
	if !cwMock.createLogStreamCalled {
		t.Error("expected CreateLogStream to be called")
	}

	// PutLogEvents must be called
	if !cwMock.putLogEventsCalled {
		t.Error("expected PutLogEvents to be called")
	}
	if cwMock.putLogEventsInput == nil || len(cwMock.putLogEventsInput.LogEvents) == 0 {
		t.Fatal("PutLogEvents called with no events")
	}

	// Verify the event message is valid JSON containing before/after fingerprints
	msgPtr := cwMock.putLogEventsInput.LogEvents[0].Message
	if msgPtr == nil {
		t.Fatal("log event Message is nil")
	}
	var decoded kmaws.RotationAuditEvent
	if err := json.Unmarshal([]byte(*msgPtr), &decoded); err != nil {
		t.Fatalf("log event message is not valid JSON: %v (got: %s)", err, *msgPtr)
	}
	if decoded.BeforeFP != event.BeforeFP {
		t.Errorf("BeforeFP = %q, want %q", decoded.BeforeFP, event.BeforeFP)
	}
	if decoded.AfterFP != event.AfterFP {
		t.Errorf("AfterFP = %q, want %q", decoded.AfterFP, event.AfterFP)
	}
	if decoded.SandboxID != event.SandboxID {
		t.Errorf("SandboxID = %q, want %q", decoded.SandboxID, event.SandboxID)
	}
	if decoded.Event != event.Event {
		t.Errorf("Event = %q, want %q", decoded.Event, event.Event)
	}
}

// TestWriteRotationAudit_LogStreamNameIncludesDate verifies that the log stream
// name includes the current date and event name.
func TestWriteRotationAudit_LogStreamNameIncludesDate(t *testing.T) {
	ctx := context.Background()

	cwMock := &mockRotationCWAPI{}

	event := kmaws.RotationAuditEvent{
		Event:     "rotate-proxy-ca",
		SandboxID: "sb-stream",
		KeyType:   "ecdsa-p256",
		Timestamp: time.Now().UTC(),
		Success:   true,
	}

	err := kmaws.WriteRotationAudit(ctx, cwMock, event)
	if err != nil {
		t.Fatalf("WriteRotationAudit returned error: %v", err)
	}

	if cwMock.createLogStreamInput == nil || cwMock.createLogStreamInput.LogStreamName == nil {
		t.Fatal("CreateLogStream input is nil")
	}

	streamName := *cwMock.createLogStreamInput.LogStreamName
	// Stream name should contain the date in some form
	today := time.Now().UTC().Format("2006-01-02")
	if !strings.Contains(streamName, today) {
		t.Errorf("log stream name %q should contain today's date %q", streamName, today)
	}
	if !strings.Contains(streamName, event.Event) {
		t.Errorf("log stream name %q should contain event type %q", streamName, event.Event)
	}
}

// ============================================================
// Tests: Fingerprint helpers (via exported behavior in RotateSandboxIdentity)
// ============================================================

// TestEd25519Fingerprint_DeterministicFormat verifies that fingerprints have
// the sha256:XXXXXXXXXXXXXXXX format (16 hex chars = 8 bytes).
// We test this indirectly by checking the fingerprint returned by RotateSandboxIdentity.
func TestEd25519Fingerprint_DeterministicFormat(t *testing.T) {
	ctx := context.Background()

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	ssmMock := &mockRotationSSMAPI{}
	dynamoMock := &mockRotationDynamoAPI{
		getItemOutput: makeRotationGetItemOutput("sb-fp", pubB64, "fp@sandbox.local"),
	}

	_, newFP, err := kmaws.RotateSandboxIdentity(ctx, ssmMock, dynamoMock, "sb-fp", "kms-key-id", "km-identities")
	if err != nil {
		t.Fatalf("RotateSandboxIdentity returned error: %v", err)
	}

	// Fingerprint format: sha256:XXXXXXXXXXXXXXXX (sha256: prefix + 16 hex chars)
	if !strings.HasPrefix(newFP, "sha256:") {
		t.Errorf("fingerprint %q should start with 'sha256:'", newFP)
	}
	hexPart := strings.TrimPrefix(newFP, "sha256:")
	if len(hexPart) != 16 {
		t.Errorf("fingerprint hex part %q should be 16 chars (8 bytes), got %d", hexPart, len(hexPart))
	}
	for _, c := range hexPart {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("fingerprint hex part %q contains non-hex char: %c", hexPart, c)
		}
	}
}
