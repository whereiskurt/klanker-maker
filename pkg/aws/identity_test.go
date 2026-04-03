// Package aws_test — identity_test.go
// Full unit test coverage for the sandbox identity library:
// Ed25519 key generation, SSM storage, DynamoDB publishing,
// email signing/verification, raw MIME signed email sending,
// NaCl box encryption, and cleanup.
package aws_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ============================================================
// Mock: IdentitySSMAPI
// ============================================================

type mockIdentitySSMAPI struct {
	putParameterCalled  bool
	putParameterInput   *ssm.PutParameterInput
	putParameterErr     error

	getParameterCalled  bool
	getParameterInput   *ssm.GetParameterInput
	getParameterValue   string
	getParameterErr     error

	deleteParameterCalled bool
	deleteParameterInputs []*ssm.DeleteParameterInput
	deleteParameterErr    error
}

func (m *mockIdentitySSMAPI) PutParameter(ctx context.Context, input *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	m.putParameterCalled = true
	m.putParameterInput = input
	return &ssm.PutParameterOutput{}, m.putParameterErr
}

func (m *mockIdentitySSMAPI) GetParameter(ctx context.Context, input *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	m.getParameterCalled = true
	m.getParameterInput = input
	if m.getParameterErr != nil {
		return nil, m.getParameterErr
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{
			Value: aws.String(m.getParameterValue),
		},
	}, nil
}

func (m *mockIdentitySSMAPI) DeleteParameter(ctx context.Context, input *ssm.DeleteParameterInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
	m.deleteParameterInputs = append(m.deleteParameterInputs, input)
	return &ssm.DeleteParameterOutput{}, m.deleteParameterErr
}

// ============================================================
// Mock: IdentityTableAPI
// ============================================================

type mockIdentityTableAPI struct {
	putItemCalled bool
	putItemInput  *dynamodb.PutItemInput
	putItemErr    error

	getItemCalled bool
	getItemInput  *dynamodb.GetItemInput
	getItemOutput *dynamodb.GetItemOutput
	getItemErr    error

	deleteItemCalled bool
	deleteItemInput  *dynamodb.DeleteItemInput
	deleteItemErr    error
}

func (m *mockIdentityTableAPI) PutItem(ctx context.Context, input *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	m.putItemCalled = true
	m.putItemInput = input
	return &dynamodb.PutItemOutput{}, m.putItemErr
}

func (m *mockIdentityTableAPI) GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	m.getItemCalled = true
	m.getItemInput = input
	if m.getItemErr != nil {
		return nil, m.getItemErr
	}
	if m.getItemOutput != nil {
		return m.getItemOutput, nil
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (m *mockIdentityTableAPI) DeleteItem(ctx context.Context, input *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	m.deleteItemCalled = true
	m.deleteItemInput = input
	return &dynamodb.DeleteItemOutput{}, m.deleteItemErr
}

// ============================================================
// Mock: SESV2API (already defined in ses_test.go — reuse mockSESV2API2 here
// to avoid duplicate type names)
// ============================================================

type mockIdentitySESAPI struct {
	sendEmailCalled bool
	sendEmailInput  *sesv2.SendEmailInput
	sendEmailErr    error
}

func (m *mockIdentitySESAPI) CreateEmailIdentity(ctx context.Context, input *sesv2.CreateEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.CreateEmailIdentityOutput, error) {
	return &sesv2.CreateEmailIdentityOutput{}, nil
}

func (m *mockIdentitySESAPI) DeleteEmailIdentity(ctx context.Context, input *sesv2.DeleteEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.DeleteEmailIdentityOutput, error) {
	return &sesv2.DeleteEmailIdentityOutput{}, nil
}

func (m *mockIdentitySESAPI) SendEmail(ctx context.Context, input *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	m.sendEmailCalled = true
	m.sendEmailInput = input
	return &sesv2.SendEmailOutput{MessageId: aws.String("test-msg-id")}, m.sendEmailErr
}

// ============================================================
// Helpers
// ============================================================

// makeTestKeys generates a real Ed25519 key pair and returns base64-encoded strings.
func makeTestKeys(t *testing.T) (pubKeyB64, privKeyB64 string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	privFull := []byte(priv) // 64 bytes: seed+public
	return base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(privFull)
}

// makeIdentityGetItemOutput builds a DynamoDB GetItemOutput with identity fields.
func makeIdentityGetItemOutput(sandboxID, pubKeyB64, email string, encKeyB64 string) *dynamodb.GetItemOutput {
	item := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":  &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		"public_key":  &dynamodbtypes.AttributeValueMemberS{Value: pubKeyB64},
		"email_address": &dynamodbtypes.AttributeValueMemberS{Value: email},
	}
	if encKeyB64 != "" {
		item["encryption_public_key"] = &dynamodbtypes.AttributeValueMemberS{Value: encKeyB64}
	}
	return &dynamodb.GetItemOutput{Item: item}
}

// ============================================================
// GenerateSandboxIdentity tests
// ============================================================

func TestIdentity_GenerateSandboxIdentity_SSMPathAndType(t *testing.T) {
	mockSSM := &mockIdentitySSMAPI{}
	pubKey, err := kmaws.GenerateSandboxIdentity(context.Background(), mockSSM, "sb-test01", "alias/km-key")
	if err != nil {
		t.Fatalf("GenerateSandboxIdentity returned error: %v", err)
	}
	if !mockSSM.putParameterCalled {
		t.Fatal("expected PutParameter to be called")
	}
	if mockSSM.putParameterInput == nil {
		t.Fatal("PutParameter input is nil")
	}
	wantPath := "/sandbox/sb-test01/signing-key"
	if mockSSM.putParameterInput.Name == nil || *mockSSM.putParameterInput.Name != wantPath {
		t.Errorf("SSM Name = %v; want %q", mockSSM.putParameterInput.Name, wantPath)
	}
	if mockSSM.putParameterInput.Type != ssmtypes.ParameterTypeSecureString {
		t.Errorf("SSM Type = %v; want SecureString", mockSSM.putParameterInput.Type)
	}
	if mockSSM.putParameterInput.KeyId == nil || *mockSSM.putParameterInput.KeyId != "alias/km-key" {
		t.Errorf("SSM KeyId = %v; want %q", mockSSM.putParameterInput.KeyId, "alias/km-key")
	}
	// Overwrite must be true for retry-safe operation
	if mockSSM.putParameterInput.Overwrite == nil || !*mockSSM.putParameterInput.Overwrite {
		t.Error("SSM Overwrite must be true")
	}
	// Public key must be 32 bytes (Ed25519)
	if len(pubKey) != ed25519.PublicKeySize {
		t.Errorf("public key length = %d; want %d (Ed25519)", len(pubKey), ed25519.PublicKeySize)
	}
}

func TestIdentity_GenerateSandboxIdentity_PublicKey32Bytes(t *testing.T) {
	mockSSM := &mockIdentitySSMAPI{}
	pubKey, err := kmaws.GenerateSandboxIdentity(context.Background(), mockSSM, "sb-test02", "alias/km-key")
	if err != nil {
		t.Fatalf("GenerateSandboxIdentity returned error: %v", err)
	}
	if len(pubKey) != ed25519.PublicKeySize {
		t.Errorf("public key length = %d; want 32", len(pubKey))
	}
}

// ============================================================
// GenerateEncryptionKey tests
// ============================================================

func TestIdentity_GenerateEncryptionKey_SSMPathAndSize(t *testing.T) {
	mockSSM := &mockIdentitySSMAPI{}
	pubKey, err := kmaws.GenerateEncryptionKey(context.Background(), mockSSM, "sb-test03", "alias/km-key")
	if err != nil {
		t.Fatalf("GenerateEncryptionKey returned error: %v", err)
	}
	if !mockSSM.putParameterCalled {
		t.Fatal("expected PutParameter to be called")
	}
	wantPath := "/sandbox/sb-test03/encryption-key"
	if mockSSM.putParameterInput.Name == nil || *mockSSM.putParameterInput.Name != wantPath {
		t.Errorf("SSM Name = %v; want %q", mockSSM.putParameterInput.Name, wantPath)
	}
	// X25519 public key is 32 bytes
	if pubKey == nil || len(pubKey) != 32 {
		t.Errorf("encryption public key length = %v; want 32", len(pubKey))
	}
}

// ============================================================
// PublishIdentity tests
// ============================================================

func TestIdentity_PublishIdentity_PutItemFields(t *testing.T) {
	mockDyn := &mockIdentityTableAPI{}

	// Generate real keys
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pubKeyB64 := base64.StdEncoding.EncodeToString(pub)

	err := kmaws.PublishIdentity(context.Background(), mockDyn, "km-identities", "sb-pub01", "sb-pub01@sandboxes.example.com", pub, nil, "", "", "", "", nil)
	if err != nil {
		t.Fatalf("PublishIdentity returned error: %v", err)
	}
	if !mockDyn.putItemCalled {
		t.Fatal("expected PutItem to be called")
	}
	item := mockDyn.putItemInput.Item
	if item == nil {
		t.Fatal("PutItem item is nil")
	}
	checkStringAttr(t, item, "sandbox_id", "sb-pub01")
	checkStringAttr(t, item, "public_key", pubKeyB64)
	checkStringAttr(t, item, "email_address", "sb-pub01@sandboxes.example.com")
	if _, ok := item["created_at"]; !ok {
		t.Error("expected created_at attribute to be set")
	}
	// No encryption_public_key when nil
	if _, ok := item["encryption_public_key"]; ok {
		t.Error("encryption_public_key should not be present when nil")
	}
	// ConditionExpression for idempotency
	if mockDyn.putItemInput.ConditionExpression == nil {
		t.Error("expected ConditionExpression for idempotent publish")
	}
}

func TestIdentity_PublishIdentity_IncludesEncryptionKey(t *testing.T) {
	mockDyn := &mockIdentityTableAPI{}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)

	var encPubKey [32]byte
	_, _ = rand.Read(encPubKey[:])
	encPubKeyPtr := &encPubKey

	err := kmaws.PublishIdentity(context.Background(), mockDyn, "km-identities", "sb-enc01", "sb-enc01@example.com", pub, encPubKeyPtr, "", "", "", "", nil)
	if err != nil {
		t.Fatalf("PublishIdentity returned error: %v", err)
	}
	item := mockDyn.putItemInput.Item
	if _, ok := item["encryption_public_key"]; !ok {
		t.Error("expected encryption_public_key attribute when encPubKey is non-nil")
	}
}

// ============================================================
// FetchPublicKey tests
// ============================================================

func TestIdentity_FetchPublicKey_ReturnsIdentityRecord(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pubKeyB64 := base64.StdEncoding.EncodeToString(pub)

	mockDyn := &mockIdentityTableAPI{
		getItemOutput: makeIdentityGetItemOutput("sb-fetch01", pubKeyB64, "sb-fetch01@example.com", ""),
	}

	record, err := kmaws.FetchPublicKey(context.Background(), mockDyn, "km-identities", "sb-fetch01")
	if err != nil {
		t.Fatalf("FetchPublicKey returned error: %v", err)
	}
	if record == nil {
		t.Fatal("expected non-nil IdentityRecord")
	}
	if record.SandboxID != "sb-fetch01" {
		t.Errorf("SandboxID = %q; want %q", record.SandboxID, "sb-fetch01")
	}
	if record.PublicKeyB64 != pubKeyB64 {
		t.Errorf("PublicKeyB64 = %q; want %q", record.PublicKeyB64, pubKeyB64)
	}
	if record.EmailAddress != "sb-fetch01@example.com" {
		t.Errorf("EmailAddress = %q; want %q", record.EmailAddress, "sb-fetch01@example.com")
	}
	if record.EncryptionPublicKeyB64 != "" {
		t.Errorf("EncryptionPublicKeyB64 should be empty, got %q", record.EncryptionPublicKeyB64)
	}
}

func TestIdentity_FetchPublicKey_ReturnsNilWhenNotFound(t *testing.T) {
	// DynamoDB GetItem returns empty Item map when key doesn't exist
	mockDyn := &mockIdentityTableAPI{
		getItemOutput: &dynamodb.GetItemOutput{Item: nil},
	}

	record, err := kmaws.FetchPublicKey(context.Background(), mockDyn, "km-identities", "sb-notfound")
	if err != nil {
		t.Fatalf("FetchPublicKey returned error: %v", err)
	}
	if record != nil {
		t.Errorf("expected nil record for not-found sandbox, got %+v", record)
	}
}

func TestIdentity_FetchPublicKey_WithEncryptionKey(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pubKeyB64 := base64.StdEncoding.EncodeToString(pub)

	var encKey [32]byte
	_, _ = rand.Read(encKey[:])
	encKeyB64 := base64.StdEncoding.EncodeToString(encKey[:])

	mockDyn := &mockIdentityTableAPI{
		getItemOutput: makeIdentityGetItemOutput("sb-fetch02", pubKeyB64, "sb-fetch02@example.com", encKeyB64),
	}

	record, err := kmaws.FetchPublicKey(context.Background(), mockDyn, "km-identities", "sb-fetch02")
	if err != nil {
		t.Fatalf("FetchPublicKey returned error: %v", err)
	}
	if record == nil {
		t.Fatal("expected non-nil record")
	}
	if record.EncryptionPublicKeyB64 != encKeyB64 {
		t.Errorf("EncryptionPublicKeyB64 = %q; want %q", record.EncryptionPublicKeyB64, encKeyB64)
	}
}

// ============================================================
// SignEmailBody + VerifyEmailSignature tests
// ============================================================

func TestIdentity_SignAndVerify_RoundTrip(t *testing.T) {
	pubB64, privB64 := makeTestKeys(t)

	body := "Hello sandbox world! This is the email body."
	sigB64, err := kmaws.SignEmailBody(privB64, body)
	if err != nil {
		t.Fatalf("SignEmailBody returned error: %v", err)
	}
	if sigB64 == "" {
		t.Fatal("expected non-empty signature")
	}

	err = kmaws.VerifyEmailSignature(pubB64, body, sigB64)
	if err != nil {
		t.Errorf("VerifyEmailSignature failed on valid signature: %v", err)
	}
}

func TestIdentity_VerifyEmailSignature_TamperedBody(t *testing.T) {
	pubB64, privB64 := makeTestKeys(t)

	body := "Original body."
	sigB64, err := kmaws.SignEmailBody(privB64, body)
	if err != nil {
		t.Fatalf("SignEmailBody returned error: %v", err)
	}

	err = kmaws.VerifyEmailSignature(pubB64, "Tampered body!", sigB64)
	if err == nil {
		t.Error("expected error for tampered body; got nil")
	}
}

func TestIdentity_VerifyEmailSignature_WrongPublicKey(t *testing.T) {
	_, privB64 := makeTestKeys(t)
	wrongPubB64, _ := makeTestKeys(t) // different key pair

	body := "Some body content."
	sigB64, err := kmaws.SignEmailBody(privB64, body)
	if err != nil {
		t.Fatalf("SignEmailBody returned error: %v", err)
	}

	err = kmaws.VerifyEmailSignature(wrongPubB64, body, sigB64)
	if err == nil {
		t.Error("expected error for wrong public key; got nil")
	}
}

// ============================================================
// SendSignedEmail tests
// ============================================================

// makeSendSignedEmailMocks returns SSM mock pre-loaded with a signing key,
// and an identity table mock that returns empty (no encryption key by default).
func makeSendSignedEmailMocks(t *testing.T, sandboxID string) (ssmMock *mockIdentitySSMAPI, dynMock *mockIdentityTableAPI, privKeyB64 string) {
	t.Helper()
	_, privFull, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	privKeyB64 = base64.StdEncoding.EncodeToString(privFull)

	ssmMock = &mockIdentitySSMAPI{
		getParameterValue: privKeyB64,
	}
	dynMock = &mockIdentityTableAPI{
		getItemOutput: &dynamodb.GetItemOutput{Item: nil}, // no recipient identity by default
	}
	return ssmMock, dynMock, privKeyB64
}

func TestIdentity_SendSignedEmail_RawMIMEHeaders(t *testing.T) {
	sesMock := &mockIdentitySESAPI{}
	ssmMock, dynMock, _ := makeSendSignedEmailMocks(t, "sb-sender01")

	err := kmaws.SendSignedEmail(
		context.Background(),
		sesMock, ssmMock, dynMock,
		"sb-sender01@example.com",  // from
		"recipient@example.com",    // to
		"Test subject",             // subject
		"Hello this is the body.", // body
		"sb-sender01",             // sandboxID
		"sb-recipient01",          // recipientSandboxID
		"km-identities",           // tableName
		"off",                     // encryptionPolicy
		nil,                       // attachments
	)
	if err != nil {
		t.Fatalf("SendSignedEmail returned error: %v", err)
	}
	if !sesMock.sendEmailCalled {
		t.Fatal("expected SendEmail to be called")
	}
	input := sesMock.sendEmailInput
	if input.Content == nil || input.Content.Raw == nil {
		t.Fatal("expected Content.Raw (not Content.Simple) for custom headers")
	}
	rawData := string(input.Content.Raw.Data)
	if !strings.Contains(rawData, "X-KM-Signature:") {
		t.Errorf("raw MIME missing X-KM-Signature header; got: %s", rawData)
	}
	if !strings.Contains(rawData, "X-KM-Sender-ID: sb-sender01") {
		t.Errorf("raw MIME missing X-KM-Sender-ID header; got: %s", rawData)
	}
}

func TestIdentity_SendSignedEmail_BodyMatchesSigningInput(t *testing.T) {
	sesMock := &mockIdentitySESAPI{}
	ssmMock, dynMock, _ := makeSendSignedEmailMocks(t, "sb-sender02")
	// Pre-load actual private key into SSM mock
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	privFull := []byte(priv)
	privKeyB64 := base64.StdEncoding.EncodeToString(privFull)
	pubKeyB64 := base64.StdEncoding.EncodeToString(pub)
	ssmMock.getParameterValue = privKeyB64

	body := "The exact body content."

	err := kmaws.SendSignedEmail(
		context.Background(),
		sesMock, ssmMock, dynMock,
		"sb-sender02@example.com", "recip@example.com",
		"Subject", body,
		"sb-sender02", "sb-recip02", "km-identities", "off", nil,
	)
	if err != nil {
		t.Fatalf("SendSignedEmail error: %v", err)
	}

	rawData := string(sesMock.sendEmailInput.Content.Raw.Data)

	// Extract the X-KM-Signature value
	var sigB64 string
	for _, line := range strings.Split(rawData, "\r\n") {
		if strings.HasPrefix(line, "X-KM-Signature: ") {
			sigB64 = strings.TrimPrefix(line, "X-KM-Signature: ")
			break
		}
	}
	if sigB64 == "" {
		t.Fatal("could not extract X-KM-Signature from MIME")
	}

	// Verify the signature against the body using the public key
	err = kmaws.VerifyEmailSignature(pubKeyB64, body, sigB64)
	if err != nil {
		t.Errorf("signature verification failed: %v", err)
	}
}

func TestIdentity_SendSignedEmail_EncryptionRequired_NoRecipientKey_ReturnsError(t *testing.T) {
	sesMock := &mockIdentitySESAPI{}
	ssmMock, dynMock, _ := makeSendSignedEmailMocks(t, "sb-sender03")
	// dynMock returns nil item (no recipient identity)

	err := kmaws.SendSignedEmail(
		context.Background(),
		sesMock, ssmMock, dynMock,
		"sb-sender03@example.com", "recip@example.com",
		"Subject", "Body",
		"sb-sender03", "sb-recip03", "km-identities", "required", nil,
	)
	if err == nil {
		t.Error("expected error when encryption=required and recipient has no public key")
	}
}

func TestIdentity_SendSignedEmail_EncryptionRequired_WithRecipientKey_Encrypted(t *testing.T) {
	sesMock := &mockIdentitySESAPI{}
	ssmMock, _, _ := makeSendSignedEmailMocks(t, "sb-sender04")

	// Generate an X25519 key pair for recipient encryption
	var recipEncPub, recipEncPriv [32]byte
	_, _ = rand.Read(recipEncPriv[:])
	// For X25519 key derivation — we'll use it in EncryptForRecipient test
	// For test purposes: just set a random 32-byte key as the encryption public key
	_, _ = rand.Read(recipEncPub[:])
	encKeyB64 := base64.StdEncoding.EncodeToString(recipEncPub[:])

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pubKeyB64 := base64.StdEncoding.EncodeToString(pub)

	dynMock := &mockIdentityTableAPI{
		getItemOutput: makeIdentityGetItemOutput("sb-recip04", pubKeyB64, "sb-recip04@example.com", encKeyB64),
	}

	err := kmaws.SendSignedEmail(
		context.Background(),
		sesMock, ssmMock, dynMock,
		"sb-sender04@example.com", "sb-recip04@example.com",
		"Subject", "Confidential body",
		"sb-sender04", "sb-recip04", "km-identities", "required", nil,
	)
	if err != nil {
		t.Fatalf("SendSignedEmail (required, with key) returned error: %v", err)
	}
	rawData := string(sesMock.sendEmailInput.Content.Raw.Data)
	if !strings.Contains(rawData, "X-KM-Encrypted: true") {
		t.Errorf("expected X-KM-Encrypted: true in MIME; got: %s", rawData)
	}
}

func TestIdentity_SendSignedEmail_EncryptionOptional_NoRecipientKey_SendsPlaintext(t *testing.T) {
	sesMock := &mockIdentitySESAPI{}
	ssmMock, dynMock, _ := makeSendSignedEmailMocks(t, "sb-sender05")
	// dynMock returns nil item (no encryption key)

	err := kmaws.SendSignedEmail(
		context.Background(),
		sesMock, ssmMock, dynMock,
		"sb-sender05@example.com", "recip@example.com",
		"Subject", "Plain body",
		"sb-sender05", "sb-recip05", "km-identities", "optional", nil,
	)
	if err != nil {
		t.Errorf("SendSignedEmail (optional, no key) should succeed; got: %v", err)
	}
	rawData := string(sesMock.sendEmailInput.Content.Raw.Data)
	if strings.Contains(rawData, "X-KM-Encrypted: true") {
		t.Error("unexpected X-KM-Encrypted header in optional no-key case")
	}
}

func TestIdentity_SendSignedEmail_EncryptionOptional_WithRecipientKey_Encrypted(t *testing.T) {
	sesMock := &mockIdentitySESAPI{}
	ssmMock, _, _ := makeSendSignedEmailMocks(t, "sb-sender06")

	var encPub [32]byte
	_, _ = rand.Read(encPub[:])
	encKeyB64 := base64.StdEncoding.EncodeToString(encPub[:])

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pubKeyB64 := base64.StdEncoding.EncodeToString(pub)

	dynMock := &mockIdentityTableAPI{
		getItemOutput: makeIdentityGetItemOutput("sb-recip06", pubKeyB64, "sb-recip06@example.com", encKeyB64),
	}

	err := kmaws.SendSignedEmail(
		context.Background(),
		sesMock, ssmMock, dynMock,
		"sb-sender06@example.com", "sb-recip06@example.com",
		"Subject", "Secret",
		"sb-sender06", "sb-recip06", "km-identities", "optional", nil,
	)
	if err != nil {
		t.Fatalf("SendSignedEmail (optional, with key) returned error: %v", err)
	}
	rawData := string(sesMock.sendEmailInput.Content.Raw.Data)
	if !strings.Contains(rawData, "X-KM-Encrypted: true") {
		t.Errorf("expected X-KM-Encrypted: true when optional + key exists; got: %s", rawData)
	}
}

func TestIdentity_SendSignedEmail_EncryptionOff_SkipsFetch(t *testing.T) {
	sesMock := &mockIdentitySESAPI{}
	ssmMock, dynMock, _ := makeSendSignedEmailMocks(t, "sb-sender07")

	err := kmaws.SendSignedEmail(
		context.Background(),
		sesMock, ssmMock, dynMock,
		"sb-sender07@example.com", "recip@example.com",
		"Subject", "Body",
		"sb-sender07", "sb-recip07", "km-identities", "off", nil,
	)
	if err != nil {
		t.Fatalf("SendSignedEmail (off) returned error: %v", err)
	}
	// FetchPublicKey should NOT be called when encryption=off
	if dynMock.getItemCalled {
		t.Error("FetchPublicKey (DynamoDB GetItem) should not be called when encryption=off")
	}
}

// ============================================================
// CleanupSandboxIdentity tests
// ============================================================

func TestIdentity_Cleanup_DeletesSigningKey(t *testing.T) {
	mockSSM := &mockIdentitySSMAPI{}
	mockDyn := &mockIdentityTableAPI{}

	err := kmaws.CleanupSandboxIdentity(context.Background(), mockSSM, mockDyn, "km-identities", "sb-clean01")
	if err != nil {
		t.Fatalf("CleanupSandboxIdentity returned error: %v", err)
	}

	// Should have called DeleteParameter for signing-key
	var foundSigningKey bool
	for _, inp := range mockSSM.deleteParameterInputs {
		if inp.Name != nil && *inp.Name == "/sandbox/sb-clean01/signing-key" {
			foundSigningKey = true
		}
	}
	if !foundSigningKey {
		t.Error("expected DeleteParameter called with /sandbox/sb-clean01/signing-key")
	}

	// Should have called DeleteItem on DynamoDB
	if !mockDyn.deleteItemCalled {
		t.Error("expected DeleteItem to be called on DynamoDB")
	}
	pkAttr, ok := mockDyn.deleteItemInput.Key["sandbox_id"]
	if !ok {
		t.Fatal("expected sandbox_id in DeleteItem key")
	}
	pkStr, ok := pkAttr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok || pkStr.Value != "sb-clean01" {
		t.Errorf("DeleteItem key sandbox_id = %v; want sb-clean01", pkAttr)
	}
}

func TestIdentity_Cleanup_DeletesEncryptionKey(t *testing.T) {
	mockSSM := &mockIdentitySSMAPI{}
	mockDyn := &mockIdentityTableAPI{}

	err := kmaws.CleanupSandboxIdentity(context.Background(), mockSSM, mockDyn, "km-identities", "sb-clean02")
	if err != nil {
		t.Fatalf("CleanupSandboxIdentity returned error: %v", err)
	}

	var foundEncKey bool
	for _, inp := range mockSSM.deleteParameterInputs {
		if inp.Name != nil && *inp.Name == "/sandbox/sb-clean02/encryption-key" {
			foundEncKey = true
		}
	}
	if !foundEncKey {
		t.Error("expected DeleteParameter called with /sandbox/sb-clean02/encryption-key")
	}
}

func TestIdentity_Cleanup_IdempotentOnParameterNotFound(t *testing.T) {
	// ParameterNotFound from SSM should be swallowed
	mockSSM := &mockIdentitySSMAPI{
		deleteParameterErr: &ssmtypes.ParameterNotFound{},
	}
	mockDyn := &mockIdentityTableAPI{}

	err := kmaws.CleanupSandboxIdentity(context.Background(), mockSSM, mockDyn, "km-identities", "sb-gone")
	if err != nil {
		t.Errorf("CleanupSandboxIdentity should return nil for ParameterNotFound, got: %v", err)
	}
}

func TestIdentity_Cleanup_DynamoDeleteItemCalled(t *testing.T) {
	mockSSM := &mockIdentitySSMAPI{}
	mockDyn := &mockIdentityTableAPI{}

	err := kmaws.CleanupSandboxIdentity(context.Background(), mockSSM, mockDyn, "km-identities", "sb-clean03")
	if err != nil {
		t.Fatalf("CleanupSandboxIdentity returned error: %v", err)
	}
	if !mockDyn.deleteItemCalled {
		t.Error("expected DeleteItem to be called")
	}
}

// ============================================================
// EncryptForRecipient + DecryptFromSender round-trip tests
// ============================================================

func TestIdentity_EncryptDecrypt_RoundTrip(t *testing.T) {
	// Generate X25519 key pair using random bytes (box.GenerateKey would be ideal
	// but for the test we use the library directly)
	var pubKey, privKey [32]byte
	// Use GenerateEncryptionKey pattern: box.GenerateKey equivalent
	// We'll use the exported functions from the identity library
	_, _ = rand.Read(privKey[:]) // This is a test key, not a real X25519 key

	// Use a simple approach: generate proper keys via the identity library's helpers
	// Since we can't call box.GenerateKey directly here without importing it,
	// we'll call the library function pair with known test vectors
	plaintext := []byte("secret message content for sandbox")

	// For round-trip test: generate a proper key pair
	// We'll use the GenerateEncryptionKey mock approach:
	// The actual nacl/box key generation is done inside the library.
	// We can test via the exported encrypt/decrypt pair only if we have
	// valid X25519 keys. Let's use crypto/rand for test keys.
	// NOTE: NaCl box keys are just 32-byte random values that work with Curve25519.
	_, _ = rand.Read(pubKey[:])
	_, _ = rand.Read(privKey[:])

	// Encrypt
	ciphertext, err := kmaws.EncryptForRecipient(&pubKey, plaintext)
	if err != nil {
		t.Fatalf("EncryptForRecipient returned error: %v", err)
	}
	if len(ciphertext) <= len(plaintext) {
		t.Error("ciphertext should be longer than plaintext (overhead from box)")
	}

	// Decrypt — we can't verify the content with wrong private key, but round-trip
	// with correct key should work. For a proper test, generate keys via box.GenerateKey.
	// Since we control the test, we'll verify the function exists and doesn't panic.
	// The actual correctness test needs real key pairs — see next test.
	_ = ciphertext
}

func TestIdentity_EncryptDecrypt_CorrectRoundTrip(t *testing.T) {
	// Use GenerateEncryptionKey to get a real X25519 key pair via the library
	// Since GenerateEncryptionKey stores in SSM, we use a mock.
	mockSSM := &mockIdentitySSMAPI{}
	encPubKey, err := kmaws.GenerateEncryptionKey(context.Background(), mockSSM, "sb-enc-rt", "alias/km-key")
	if err != nil {
		t.Fatalf("GenerateEncryptionKey: %v", err)
	}

	// Retrieve the private key from the mock SSM value
	privKeyB64 := mockSSM.putParameterInput.Value
	if privKeyB64 == nil || *privKeyB64 == "" {
		t.Fatal("SSM PutParameter value not set")
	}
	privKeyBytes, err := base64.StdEncoding.DecodeString(*privKeyB64)
	if err != nil {
		t.Fatalf("decode priv key: %v", err)
	}
	var privKey [32]byte
	copy(privKey[:], privKeyBytes)

	plaintext := []byte("encrypt me for round trip test")
	ciphertext, err := kmaws.EncryptForRecipient(encPubKey, plaintext)
	if err != nil {
		t.Fatalf("EncryptForRecipient: %v", err)
	}

	decrypted, err := kmaws.DecryptFromSender(&privKey, encPubKey, ciphertext)
	if err != nil {
		t.Fatalf("DecryptFromSender: %v", err)
	}

	if string(decrypted) != string(plaintext) {
		t.Errorf("round-trip decryption mismatch: got %q; want %q", decrypted, plaintext)
	}
}

// ============================================================
// Policy fields: PublishIdentity stores + FetchPublicKey reads
// ============================================================

// makeIdentityGetItemOutputWithPolicies builds a DynamoDB GetItemOutput with identity
// and policy fields for testing policy round-trips.
func makeIdentityGetItemOutputWithPolicies(sandboxID, pubKeyB64, email, signing, verifyInbound, encryption string) *dynamodb.GetItemOutput {
	item := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":    &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		"public_key":    &dynamodbtypes.AttributeValueMemberS{Value: pubKeyB64},
		"email_address": &dynamodbtypes.AttributeValueMemberS{Value: email},
	}
	if signing != "" {
		item["signing_policy"] = &dynamodbtypes.AttributeValueMemberS{Value: signing}
	}
	if verifyInbound != "" {
		item["verify_inbound_policy"] = &dynamodbtypes.AttributeValueMemberS{Value: verifyInbound}
	}
	if encryption != "" {
		item["encryption_policy"] = &dynamodbtypes.AttributeValueMemberS{Value: encryption}
	}
	return &dynamodb.GetItemOutput{Item: item}
}

// TestIdentity_PublishIdentity_PolicyFieldsStored verifies that policy attribute values
// are present in the DynamoDB PutItem call when non-empty.
func TestIdentity_PublishIdentity_PolicyFieldsStored(t *testing.T) {
	mockDyn := &mockIdentityTableAPI{}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)

	err := kmaws.PublishIdentity(context.Background(), mockDyn, "km-identities", "sb-policy01", "sb-policy01@example.com", pub, nil, "required", "optional", "off", "", nil)
	if err != nil {
		t.Fatalf("PublishIdentity returned error: %v", err)
	}
	item := mockDyn.putItemInput.Item
	checkStringAttr(t, item, "signing_policy", "required")
	checkStringAttr(t, item, "verify_inbound_policy", "optional")
	checkStringAttr(t, item, "encryption_policy", "off")
}

// TestIdentity_PublishIdentity_EmptyPolicyFieldsOmitted verifies that empty policy
// values are NOT added to DynamoDB (preserves legacy row compatibility).
func TestIdentity_PublishIdentity_EmptyPolicyFieldsOmitted(t *testing.T) {
	mockDyn := &mockIdentityTableAPI{}
	pub, _, _ := ed25519.GenerateKey(rand.Reader)

	err := kmaws.PublishIdentity(context.Background(), mockDyn, "km-identities", "sb-policy02", "sb-policy02@example.com", pub, nil, "", "", "", "", nil)
	if err != nil {
		t.Fatalf("PublishIdentity returned error: %v", err)
	}
	item := mockDyn.putItemInput.Item
	if _, ok := item["signing_policy"]; ok {
		t.Error("signing_policy should not be present when signing is empty")
	}
	if _, ok := item["verify_inbound_policy"]; ok {
		t.Error("verify_inbound_policy should not be present when verifyInbound is empty")
	}
	if _, ok := item["encryption_policy"]; ok {
		t.Error("encryption_policy should not be present when encryption is empty")
	}
}

// TestIdentity_FetchPublicKey_PolicyFieldsReadBack verifies that policy fields stored
// in DynamoDB are correctly read back into the IdentityRecord.
func TestIdentity_FetchPublicKey_PolicyFieldsReadBack(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pubKeyB64 := base64.StdEncoding.EncodeToString(pub)

	mockDyn := &mockIdentityTableAPI{
		getItemOutput: makeIdentityGetItemOutputWithPolicies("sb-policy03", pubKeyB64, "sb-policy03@example.com", "required", "optional", "off"),
	}

	record, err := kmaws.FetchPublicKey(context.Background(), mockDyn, "km-identities", "sb-policy03")
	if err != nil {
		t.Fatalf("FetchPublicKey returned error: %v", err)
	}
	if record == nil {
		t.Fatal("expected non-nil IdentityRecord")
	}
	if record.Signing != "required" {
		t.Errorf("Signing = %q; want %q", record.Signing, "required")
	}
	if record.VerifyInbound != "optional" {
		t.Errorf("VerifyInbound = %q; want %q", record.VerifyInbound, "optional")
	}
	if record.Encryption != "off" {
		t.Errorf("Encryption = %q; want %q", record.Encryption, "off")
	}
}

// TestIdentity_FetchPublicKey_LegacyRowEmptyPolicies verifies that legacy DynamoDB rows
// (without policy attributes) return empty strings — not an error.
func TestIdentity_FetchPublicKey_LegacyRowEmptyPolicies(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pubKeyB64 := base64.StdEncoding.EncodeToString(pub)

	// No policy fields in the item (simulates a legacy row)
	mockDyn := &mockIdentityTableAPI{
		getItemOutput: makeIdentityGetItemOutput("sb-legacy01", pubKeyB64, "sb-legacy01@example.com", ""),
	}

	record, err := kmaws.FetchPublicKey(context.Background(), mockDyn, "km-identities", "sb-legacy01")
	if err != nil {
		t.Fatalf("FetchPublicKey returned error for legacy row: %v", err)
	}
	if record == nil {
		t.Fatal("expected non-nil IdentityRecord for legacy row")
	}
	if record.Signing != "" {
		t.Errorf("legacy row Signing should be empty, got %q", record.Signing)
	}
	if record.VerifyInbound != "" {
		t.Errorf("legacy row VerifyInbound should be empty, got %q", record.VerifyInbound)
	}
	if record.Encryption != "" {
		t.Errorf("legacy row Encryption should be empty, got %q", record.Encryption)
	}
}

// ============================================================
// Task 4: Tests for multipart buildRawMIME (via SendSignedEmail)
// ============================================================

// TestMultipart_SinglePartBackwardCompat verifies that SendSignedEmail with nil attachments
// still produces a single-part text/plain MIME message (backward compatibility).
func TestMultipart_SinglePartBackwardCompat(t *testing.T) {
	sesMock := &mockIdentitySESAPI{}
	ssmMock, dynMock, _ := makeSendSignedEmailMocks(t, "sb-mp-back01")

	err := kmaws.SendSignedEmail(
		context.Background(),
		sesMock, ssmMock, dynMock,
		"from@example.com", "to@example.com", "Subject", "Body text",
		"sb-mp-back01", "sb-recip-back01", "km-identities", "off", nil,
	)
	if err != nil {
		t.Fatalf("SendSignedEmail (no attachments) returned error: %v", err)
	}
	rawData := string(sesMock.sendEmailInput.Content.Raw.Data)
	if !strings.Contains(rawData, "Content-Type: text/plain") {
		t.Errorf("single-part message should have Content-Type: text/plain; got:\n%s", rawData)
	}
	if strings.Contains(rawData, "multipart") {
		t.Errorf("single-part message should not contain 'multipart'; got:\n%s", rawData)
	}
}

// TestMultipart_OneAttachment verifies multipart/mixed structure with a single attachment.
func TestMultipart_OneAttachment(t *testing.T) {
	sesMock := &mockIdentitySESAPI{}
	ssmMock, dynMock, _ := makeSendSignedEmailMocks(t, "sb-mp-att01")

	attachments := []kmaws.Attachment{
		{Filename: "report.txt", Data: []byte("attachment content here")},
	}
	err := kmaws.SendSignedEmail(
		context.Background(),
		sesMock, ssmMock, dynMock,
		"from@example.com", "to@example.com", "Subject", "Body text",
		"sb-mp-att01", "sb-recip-att01", "km-identities", "off", attachments,
	)
	if err != nil {
		t.Fatalf("SendSignedEmail with 1 attachment returned error: %v", err)
	}
	rawData := string(sesMock.sendEmailInput.Content.Raw.Data)
	if !strings.Contains(rawData, "multipart/mixed") {
		t.Errorf("expected multipart/mixed Content-Type; got:\n%s", rawData)
	}
	if !strings.Contains(rawData, `filename="report.txt"`) {
		t.Errorf("expected filename=\"report.txt\" in Content-Disposition; got:\n%s", rawData)
	}
	if !strings.Contains(rawData, "Content-Transfer-Encoding: base64") {
		t.Errorf("expected Content-Transfer-Encoding: base64; got:\n%s", rawData)
	}
	if !strings.Contains(rawData, "Content-Disposition: attachment") {
		t.Errorf("expected Content-Disposition: attachment; got:\n%s", rawData)
	}
}

// TestMultipart_TwoAttachments verifies multipart/mixed with two attachments: boundary
// is present and both Content-Disposition: attachment; filename=... headers appear.
func TestMultipart_TwoAttachments(t *testing.T) {
	sesMock := &mockIdentitySESAPI{}
	ssmMock, dynMock, _ := makeSendSignedEmailMocks(t, "sb-mp-att02")

	attachments := []kmaws.Attachment{
		{Filename: "file1.bin", Data: []byte("first attachment")},
		{Filename: "file2.txt", Data: []byte("second attachment")},
	}
	err := kmaws.SendSignedEmail(
		context.Background(),
		sesMock, ssmMock, dynMock,
		"from@example.com", "to@example.com", "Subject", "Body",
		"sb-mp-att02", "sb-recip-att02", "km-identities", "off", attachments,
	)
	if err != nil {
		t.Fatalf("SendSignedEmail with 2 attachments returned error: %v", err)
	}
	rawData := string(sesMock.sendEmailInput.Content.Raw.Data)

	// Both filenames should appear.
	if !strings.Contains(rawData, `filename="file1.bin"`) {
		t.Errorf("expected filename=\"file1.bin\"; got:\n%s", rawData)
	}
	if !strings.Contains(rawData, `filename="file2.txt"`) {
		t.Errorf("expected filename=\"file2.txt\"; got:\n%s", rawData)
	}
	// Boundary marker should appear at least 3 times (2 part boundaries + closing --)
	boundaryCount := strings.Count(rawData, "--")
	if boundaryCount < 3 {
		t.Errorf("expected at least 3 boundary lines (--boundary x2, --boundary-- x1); got %d '--' occurrences", boundaryCount)
	}
}

// TestMultipart_SignatureCoversBodyOnly verifies that the X-KM-Signature header
// covers only the text body, not the attachment data.
func TestMultipart_SignatureCoversBodyOnly(t *testing.T) {
	sesMock := &mockIdentitySESAPI{}
	ssmMock, _, _ := makeSendSignedEmailMocks(t, "sb-mp-sig01")

	// Use a real key pair so we can verify the signature.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	privKeyB64 := base64.StdEncoding.EncodeToString([]byte(priv))
	pubKeyB64 := base64.StdEncoding.EncodeToString(pub)
	ssmMock.getParameterValue = privKeyB64

	body := "The signed body text."
	attachments := []kmaws.Attachment{
		{Filename: "data.bin", Data: []byte("ATTACHMENT DATA that must NOT affect signature")},
	}

	dynMock := &mockIdentityTableAPI{}
	err := kmaws.SendSignedEmail(
		context.Background(),
		sesMock, ssmMock, dynMock,
		"from@example.com", "to@example.com", "Subject", body,
		"sb-mp-sig01", "sb-recip-sig01", "km-identities", "off", attachments,
	)
	if err != nil {
		t.Fatalf("SendSignedEmail (multipart, signature test) returned error: %v", err)
	}

	rawData := string(sesMock.sendEmailInput.Content.Raw.Data)

	// Extract X-KM-Signature.
	var sigB64 string
	for _, line := range strings.Split(rawData, "\r\n") {
		if strings.HasPrefix(line, "X-KM-Signature: ") {
			sigB64 = strings.TrimPrefix(line, "X-KM-Signature: ")
			break
		}
	}
	if sigB64 == "" {
		t.Fatal("could not extract X-KM-Signature from multipart MIME")
	}

	// Signature must verify against body only (not body + attachment data).
	if err := kmaws.VerifyEmailSignature(pubKeyB64, body, sigB64); err != nil {
		t.Errorf("signature verification against body-only failed: %v", err)
	}
	// Signature must NOT verify against body+attachment concatenation.
	combined := body + string(attachments[0].Data)
	if err := kmaws.VerifyEmailSignature(pubKeyB64, combined, sigB64); err == nil {
		t.Error("signature should NOT verify against body+attachment concatenation")
	}
}

// ============================================================
// Task 6: Round-trip test — buildRawMIME → ParseSignedMessage
// ============================================================

// TestMultipart_RoundTrip builds a multipart/mixed MIME message via SendSignedEmail
// (which calls buildRawMIME internally) with a body + 2 attachments, then parses it
// with ParseSignedMessage and verifies body, both attachments, and signature.
func TestMultipart_RoundTrip(t *testing.T) {
	sesMock := &mockIdentitySESAPI{}
	ssmMock, _, _ := makeSendSignedEmailMocks(t, "sb-rt01")

	// Use a real Ed25519 key pair for end-to-end signature verification.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	privKeyB64 := base64.StdEncoding.EncodeToString([]byte(priv))
	pubKeyB64 := base64.StdEncoding.EncodeToString(pub)
	ssmMock.getParameterValue = privKeyB64

	body := "Round-trip body content."
	att1 := kmaws.Attachment{Filename: "a.txt", Data: []byte("attachment one")}
	att2 := kmaws.Attachment{Filename: "b.bin", Data: []byte{0x01, 0x02, 0x03, 0xFF}}

	dynMock := &mockIdentityTableAPI{}
	err := kmaws.SendSignedEmail(
		context.Background(),
		sesMock, ssmMock, dynMock,
		"from@example.com", "to@example.com", "Round-trip subject", body,
		"sb-rt01", "sb-rt-recv01", "km-identities", "off",
		[]kmaws.Attachment{att1, att2},
	)
	if err != nil {
		t.Fatalf("SendSignedEmail (round-trip) returned error: %v", err)
	}

	rawMIME := sesMock.sendEmailInput.Content.Raw.Data

	// Parse the produced MIME.
	msg, parseErr := kmaws.ParseSignedMessage(rawMIME, "sb-rt-recv01-x", pubKeyB64, []string{"*"}, "")
	if parseErr != nil {
		t.Fatalf("ParseSignedMessage (round-trip) returned error: %v", parseErr)
	}

	// Body must match.
	if msg.Body != body {
		t.Errorf("round-trip body = %q; want %q", msg.Body, body)
	}

	// Signature must verify.
	if !msg.SignatureOK {
		t.Error("expected SignatureOK=true in round-trip")
	}

	// Both attachments must be present.
	if len(msg.Attachments) != 2 {
		t.Fatalf("expected 2 attachments in round-trip; got %d", len(msg.Attachments))
	}
	if msg.Attachments[0].Filename != att1.Filename {
		t.Errorf("Attachment[0].Filename = %q; want %q", msg.Attachments[0].Filename, att1.Filename)
	}
	if string(msg.Attachments[0].Data) != string(att1.Data) {
		t.Errorf("Attachment[0].Data = %q; want %q", msg.Attachments[0].Data, att1.Data)
	}
	if msg.Attachments[1].Filename != att2.Filename {
		t.Errorf("Attachment[1].Filename = %q; want %q", msg.Attachments[1].Filename, att2.Filename)
	}
	if string(msg.Attachments[1].Data) != string(att2.Data) {
		t.Errorf("Attachment[1].Data = %v; want %v", msg.Attachments[1].Data, att2.Data)
	}
}

// ============================================================
// Helpers
// ============================================================

func checkStringAttr(t *testing.T, item map[string]dynamodbtypes.AttributeValue, key, want string) {
	t.Helper()
	attr, ok := item[key]
	if !ok {
		t.Errorf("expected attribute %q in DynamoDB item", key)
		return
	}
	strAttr, ok := attr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Errorf("attribute %q is not a string type", key)
		return
	}
	if strAttr.Value != want {
		t.Errorf("attribute %q = %q; want %q", key, strAttr.Value, want)
	}
}

// ============================================================
// Mock: IdentityQueryAPI (for FetchPublicKeyByAlias)
// ============================================================

type mockIdentityQueryAPI struct {
	queryCalled bool
	queryInput  *dynamodb.QueryInput
	queryOutput *dynamodb.QueryOutput
	queryErr    error
}

func (m *mockIdentityQueryAPI) Query(ctx context.Context, input *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	m.queryCalled = true
	m.queryInput = input
	if m.queryErr != nil {
		return nil, m.queryErr
	}
	if m.queryOutput != nil {
		return m.queryOutput, nil
	}
	return &dynamodb.QueryOutput{Items: nil, Count: 0}, nil
}

// makeAliasQueryOutput builds a DynamoDB QueryOutput simulating alias-index GSI result.
func makeAliasQueryOutput(sandboxID, pubKeyB64, email, alias string, allowedSenders []string) *dynamodb.QueryOutput {
	item := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":    &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		"public_key":    &dynamodbtypes.AttributeValueMemberS{Value: pubKeyB64},
		"email_address": &dynamodbtypes.AttributeValueMemberS{Value: email},
		"alias":         &dynamodbtypes.AttributeValueMemberS{Value: alias},
	}
	if len(allowedSenders) > 0 {
		item["allowed_senders"] = &dynamodbtypes.AttributeValueMemberSS{Value: allowedSenders}
	}
	return &dynamodb.QueryOutput{
		Items: []map[string]dynamodbtypes.AttributeValue{item},
		Count: 1,
	}
}

// ============================================================
// FetchPublicKeyByAlias tests
// ============================================================

func TestFetchPublicKeyByAlias_KnownAlias(t *testing.T) {
	pubKeyB64, _ := makeTestKeys(t)
	mockQuery := &mockIdentityQueryAPI{
		queryOutput: makeAliasQueryOutput("sb-alias01", pubKeyB64, "sb-alias01@example.com", "research.team-a", []string{"self"}),
	}

	record, err := kmaws.FetchPublicKeyByAlias(context.Background(), mockQuery, "km-identities", "research.team-a")
	if err != nil {
		t.Fatalf("FetchPublicKeyByAlias returned error: %v", err)
	}
	if record == nil {
		t.Fatal("expected non-nil IdentityRecord for known alias")
	}
	if record.SandboxID != "sb-alias01" {
		t.Errorf("SandboxID = %q; want %q", record.SandboxID, "sb-alias01")
	}
	if record.Alias != "research.team-a" {
		t.Errorf("Alias = %q; want %q", record.Alias, "research.team-a")
	}
	if record.PublicKeyB64 != pubKeyB64 {
		t.Errorf("PublicKeyB64 mismatch")
	}
}

func TestFetchPublicKeyByAlias_UnknownAlias(t *testing.T) {
	mockQuery := &mockIdentityQueryAPI{
		queryOutput: &dynamodb.QueryOutput{Items: nil, Count: 0},
	}

	record, err := kmaws.FetchPublicKeyByAlias(context.Background(), mockQuery, "km-identities", "unknown.alias")
	if err != nil {
		t.Fatalf("FetchPublicKeyByAlias returned error for unknown alias: %v", err)
	}
	if record != nil {
		t.Errorf("expected nil IdentityRecord for unknown alias, got: %+v", record)
	}
}

func TestFetchPublicKeyByAlias_QueriesAliasIndex(t *testing.T) {
	mockQuery := &mockIdentityQueryAPI{
		queryOutput: &dynamodb.QueryOutput{Items: nil, Count: 0},
	}

	_, _ = kmaws.FetchPublicKeyByAlias(context.Background(), mockQuery, "km-identities", "build.frontend")
	if !mockQuery.queryCalled {
		t.Fatal("expected Query to be called")
	}
	if mockQuery.queryInput == nil {
		t.Fatal("Query input is nil")
	}
	if mockQuery.queryInput.IndexName == nil || *mockQuery.queryInput.IndexName != "alias-index" {
		t.Errorf("IndexName = %v; want %q", mockQuery.queryInput.IndexName, "alias-index")
	}
}

// ============================================================
// MatchesAllowList tests
// ============================================================

func TestMatchesAllowList_Wildcard(t *testing.T) {
	if !kmaws.MatchesAllowList([]string{"*"}, "sb-any", "", "sb-recv") {
		t.Error("expected * to match any sender")
	}
}

func TestMatchesAllowList_Self_Match(t *testing.T) {
	if !kmaws.MatchesAllowList([]string{"self"}, "sb-recv", "", "sb-recv") {
		t.Error("expected self to match when senderID == receiverSandboxID")
	}
}

func TestMatchesAllowList_Self_NoMatch(t *testing.T) {
	if kmaws.MatchesAllowList([]string{"self"}, "sb-other", "", "sb-recv") {
		t.Error("expected self to reject when senderID != receiverSandboxID")
	}
}

func TestMatchesAllowList_ExactID(t *testing.T) {
	if !kmaws.MatchesAllowList([]string{"sb-partner"}, "sb-partner", "", "sb-recv") {
		t.Error("expected exact sandbox ID match to permit sender")
	}
}

func TestMatchesAllowList_WildcardAlias_Match(t *testing.T) {
	if !kmaws.MatchesAllowList([]string{"build.*"}, "sb-x", "build.frontend", "sb-recv") {
		t.Error("expected build.* to match build.frontend alias")
	}
}

func TestMatchesAllowList_WildcardAlias_NoMatch(t *testing.T) {
	if kmaws.MatchesAllowList([]string{"build.*"}, "sb-x", "deploy.backend", "sb-recv") {
		t.Error("expected build.* to reject deploy.backend alias")
	}
}

func TestMatchesAllowList_Empty_RejectsAll(t *testing.T) {
	if kmaws.MatchesAllowList([]string{}, "sb-x", "some.alias", "sb-recv") {
		t.Error("expected empty patterns to reject all senders")
	}
}

// ============================================================
// PublishIdentity alias/allowedSenders tests
// ============================================================

func TestPublishIdentity_WithAlias_StoresAliasAttribute(t *testing.T) {
	pubKeyB64, _ := makeTestKeys(t)
	pubKeyBytes, _ := base64.StdEncoding.DecodeString(pubKeyB64)
	pubKey := ed25519.PublicKey(pubKeyBytes)

	mockDyn := &mockIdentityTableAPI{}
	err := kmaws.PublishIdentity(context.Background(), mockDyn, "km-identities", "sb-a01",
		"sb-a01@example.com", pubKey, nil, "", "", "", "research.team-a", nil)
	if err != nil {
		t.Fatalf("PublishIdentity returned error: %v", err)
	}
	if !mockDyn.putItemCalled {
		t.Fatal("expected PutItem to be called")
	}
	checkStringAttr(t, mockDyn.putItemInput.Item, "alias", "research.team-a")
}

func TestPublishIdentity_WithAllowedSenders_StoresSSAttribute(t *testing.T) {
	pubKeyB64, _ := makeTestKeys(t)
	pubKeyBytes, _ := base64.StdEncoding.DecodeString(pubKeyB64)
	pubKey := ed25519.PublicKey(pubKeyBytes)

	mockDyn := &mockIdentityTableAPI{}
	err := kmaws.PublishIdentity(context.Background(), mockDyn, "km-identities", "sb-a02",
		"sb-a02@example.com", pubKey, nil, "", "", "", "", []string{"self", "build.*"})
	if err != nil {
		t.Fatalf("PublishIdentity returned error: %v", err)
	}
	attr, ok := mockDyn.putItemInput.Item["allowed_senders"]
	if !ok {
		t.Fatal("expected allowed_senders attribute in DynamoDB item")
	}
	ssAttr, ok := attr.(*dynamodbtypes.AttributeValueMemberSS)
	if !ok {
		t.Fatal("expected allowed_senders to be a StringSet (SS) attribute")
	}
	if len(ssAttr.Value) != 2 {
		t.Errorf("expected 2 allowed_senders, got %d", len(ssAttr.Value))
	}
}

func TestPublishIdentity_NoAliasNoAllowedSenders_OmitsBothAttributes(t *testing.T) {
	pubKeyB64, _ := makeTestKeys(t)
	pubKeyBytes, _ := base64.StdEncoding.DecodeString(pubKeyB64)
	pubKey := ed25519.PublicKey(pubKeyBytes)

	mockDyn := &mockIdentityTableAPI{}
	err := kmaws.PublishIdentity(context.Background(), mockDyn, "km-identities", "sb-legacy",
		"sb-legacy@example.com", pubKey, nil, "", "", "", "", nil)
	if err != nil {
		t.Fatalf("PublishIdentity returned error: %v", err)
	}
	if _, ok := mockDyn.putItemInput.Item["alias"]; ok {
		t.Error("expected alias attribute to be omitted when alias is empty string")
	}
	if _, ok := mockDyn.putItemInput.Item["allowed_senders"]; ok {
		t.Error("expected allowed_senders attribute to be omitted when nil")
	}
}
