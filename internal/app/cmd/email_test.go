// Package cmd_test — email_test.go
// Unit tests for km email send and km email read commands.
// Uses injected mock dependencies — no real AWS calls.
package cmd_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	"golang.org/x/crypto/nacl/box"
)

// ============================================================
// Mock: emailMockSSM — satisfies cmd.EmailSSMAPI (== kmaws.IdentitySSMAPI)
// ============================================================

type emailMockSSM struct {
	values map[string]string // key path → base64 value
	getErr error
}

func newEmailMockSSM(values map[string]string) *emailMockSSM {
	if values == nil {
		values = make(map[string]string)
	}
	return &emailMockSSM{values: values}
}

func (m *emailMockSSM) PutParameter(_ context.Context, input *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	if input.Name != nil && input.Value != nil {
		m.values[*input.Name] = *input.Value
	}
	return &ssm.PutParameterOutput{}, nil
}

func (m *emailMockSSM) GetParameter(_ context.Context, input *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if input.Name == nil {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	val, ok := m.values[*input.Name]
	if !ok {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{Value: awssdk.String(val)},
	}, nil
}

func (m *emailMockSSM) DeleteParameter(_ context.Context, _ *ssm.DeleteParameterInput, _ ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
	return &ssm.DeleteParameterOutput{}, nil
}

// ============================================================
// Mock: emailMockSES — satisfies kmaws.SESV2API
// ============================================================

type emailMockSES struct {
	called bool
	input  *sesv2.SendEmailInput
	err    error
}

func (m *emailMockSES) CreateEmailIdentity(_ context.Context, _ *sesv2.CreateEmailIdentityInput, _ ...func(*sesv2.Options)) (*sesv2.CreateEmailIdentityOutput, error) {
	return &sesv2.CreateEmailIdentityOutput{}, nil
}

func (m *emailMockSES) DeleteEmailIdentity(_ context.Context, _ *sesv2.DeleteEmailIdentityInput, _ ...func(*sesv2.Options)) (*sesv2.DeleteEmailIdentityOutput, error) {
	return &sesv2.DeleteEmailIdentityOutput{}, nil
}

func (m *emailMockSES) SendEmail(_ context.Context, input *sesv2.SendEmailInput, _ ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	m.called = true
	m.input = input
	return &sesv2.SendEmailOutput{MessageId: awssdk.String("test-msg-id")}, m.err
}

// ============================================================
// Mock: emailMockIdentity — satisfies kmaws.IdentityTableAPI
// ============================================================

type emailMockIdentity struct {
	rows   map[string]map[string]dynamodbtypes.AttributeValue
	getErr error
}

func (m *emailMockIdentity) PutItem(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}

func (m *emailMockIdentity) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.rows == nil {
		return &dynamodb.GetItemOutput{}, nil
	}
	sidAttr, ok := input.Key["sandbox_id"]
	if !ok {
		return &dynamodb.GetItemOutput{}, nil
	}
	sid := sidAttr.(*dynamodbtypes.AttributeValueMemberS).Value
	item, exists := m.rows[sid]
	if !exists {
		return &dynamodb.GetItemOutput{}, nil
	}
	return &dynamodb.GetItemOutput{Item: item}, nil
}

func (m *emailMockIdentity) DeleteItem(_ context.Context, _ *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}

// ============================================================
// Mock: emailMockS3 — satisfies cmd.EmailS3API (== kmaws.MailboxS3API)
// ============================================================

type emailMockS3 struct {
	// bodies maps S3 key → raw MIME bytes.
	// ListObjectsV2 returns all keys from bodies map;
	// GetObject returns the body for the requested key.
	bodies  map[string][]byte
	listErr error
	getErr  error
}

func (m *emailMockS3) ListObjectsV2(_ context.Context, _ *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var objs []s3types.Object
	for k := range m.bodies {
		key := k
		objs = append(objs, s3types.Object{Key: &key})
	}
	return &s3.ListObjectsV2Output{Contents: objs}, nil
}

func (m *emailMockS3) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if input.Key == nil {
		return nil, fmt.Errorf("nil key")
	}
	body, ok := m.bodies[*input.Key]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", *input.Key)
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(body)),
	}, nil
}

// ============================================================
// Helpers
// ============================================================

// testEmailCfg returns a minimal config for email command tests.
func testEmailCfg() *config.Config {
	return &config.Config{
		Domain:            "klankermaker.ai",
		IdentityTableName: "km-identities-test",
		ArtifactsBucket:   "test-artifacts-bucket",
	}
}

// execEmailCmd executes km email <args> with injected deps, returns stdout and error.
func execEmailCmd(t *testing.T, cfg *config.Config, sendDeps *cmd.EmailSendDeps, readDeps *cmd.EmailReadDeps, args []string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "km", SilenceUsage: true, SilenceErrors: true}
	emailCmd := cmd.NewEmailCmdWithDeps(cfg, sendDeps, readDeps)
	root.AddCommand(emailCmd)
	root.SetArgs(args)

	var buf bytes.Buffer
	// Propagate output writer to all subcommands.
	root.SetOut(&buf)
	for _, c := range emailCmd.Commands() {
		c.SetOut(&buf)
	}

	err := root.Execute()
	return buf.String(), err
}

// genEd25519Pair generates an Ed25519 key pair for testing.
// Returns privB64 (64-byte private key, base64-encoded) and pubB64.
func genEd25519Pair(t *testing.T) (privB64, pubB64 string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key pair: %v", err)
	}
	return base64.StdEncoding.EncodeToString([]byte(priv)), base64.StdEncoding.EncodeToString([]byte(pub))
}

// signBody signs a body with an Ed25519 private key (base64-encoded).
func signBody(t *testing.T, privB64, body string) string {
	t.Helper()
	privBytes, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil {
		t.Fatalf("decode private key: %v", err)
	}
	sig := ed25519.Sign(ed25519.PrivateKey(privBytes), []byte(body))
	return base64.StdEncoding.EncodeToString(sig)
}

// buildMIME builds a raw MIME email for read tests.
func buildMIME(from, to, subject, senderID, body, sigB64 string) []byte {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("From: %s\r\n", from))
	sb.WriteString(fmt.Sprintf("To: %s\r\n", to))
	sb.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	if senderID != "" {
		sb.WriteString(fmt.Sprintf("X-KM-Sender-ID: %s\r\n", senderID))
	}
	if sigB64 != "" {
		sb.WriteString(fmt.Sprintf("X-KM-Signature: %s\r\n", sigB64))
	}
	sb.WriteString("Content-Type: text/plain\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return []byte(sb.String())
}

// buildMultipartMIME builds a multipart/mixed MIME email with one text part and one attachment.
func buildMultipartMIME(from, to, subject, senderID, body string, attachName string, attachData []byte) []byte {
	boundary := "testboundary12345"
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("From: %s\r\n", from))
	sb.WriteString(fmt.Sprintf("To: %s\r\n", to))
	sb.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	if senderID != "" {
		sb.WriteString(fmt.Sprintf("X-KM-Sender-ID: %s\r\n", senderID))
	}
	sb.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=%q\r\n", boundary))
	sb.WriteString("\r\n")
	// Text part
	sb.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	sb.WriteString("Content-Type: text/plain\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	sb.WriteString("\r\n")
	// Attachment part
	sb.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	sb.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=%q\r\n", attachName))
	sb.WriteString("Content-Type: application/octet-stream\r\n")
	sb.WriteString("Content-Transfer-Encoding: base64\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(base64.StdEncoding.EncodeToString(attachData))
	sb.WriteString("\r\n")
	sb.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
	return []byte(sb.String())
}

// ============================================================
// Tests: km email send (Task 6)
// ============================================================

// TestEmailSend_MissingFrom verifies that missing --from flag returns an error.
func TestEmailSend_MissingFrom(t *testing.T) {
	cfg := testEmailCfg()
	_, err := execEmailCmd(t, cfg, nil, nil, []string{"email", "send", "--to", "sb-recip01", "--subject", "hi", "--body", "-"})
	if err == nil {
		t.Fatal("expected error when --from is missing, got nil")
	}
}

// TestEmailSend_MissingTo verifies that missing --to flag returns an error.
func TestEmailSend_MissingTo(t *testing.T) {
	cfg := testEmailCfg()
	_, err := execEmailCmd(t, cfg, nil, nil, []string{"email", "send", "--from", "sb-sender1", "--subject", "hi", "--body", "-"})
	if err == nil {
		t.Fatal("expected error when --to is missing, got nil")
	}
}

// TestEmailSend_MissingSubject verifies that missing --subject flag returns an error.
func TestEmailSend_MissingSubject(t *testing.T) {
	cfg := testEmailCfg()
	_, err := execEmailCmd(t, cfg, nil, nil, []string{"email", "send", "--from", "sb-sender1", "--to", "sb-recip01", "--body", "-"})
	if err == nil {
		t.Fatal("expected error when --subject is missing, got nil")
	}
}

// TestEmailSend_MissingBody verifies that missing --body flag returns an error.
func TestEmailSend_MissingBody(t *testing.T) {
	cfg := testEmailCfg()
	_, err := execEmailCmd(t, cfg, nil, nil, []string{"email", "send", "--from", "sb-sender1", "--to", "sb-recip01", "--subject", "hi"})
	if err == nil {
		t.Fatal("expected error when --body is missing, got nil")
	}
}

// TestEmailSend_InvalidFromSandboxID verifies that an invalid --from sandbox ID returns an error.
func TestEmailSend_InvalidFromSandboxID(t *testing.T) {
	cfg := testEmailCfg()
	privB64, _ := genEd25519Pair(t)
	mockSSM := newEmailMockSSM(map[string]string{
		"/sandbox/INVALID/signing-key": privB64,
	})
	mockSES := &emailMockSES{}
	mockIdentity := &emailMockIdentity{}

	bodyFile := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(bodyFile, []byte("hello"), 0600); err != nil {
		t.Fatalf("write body file: %v", err)
	}

	sendDeps := &cmd.EmailSendDeps{SES: mockSES, SSMParam: mockSSM, Identity: mockIdentity}
	_, err := execEmailCmd(t, cfg, sendDeps, nil, []string{
		"email", "send",
		"--from", "INVALID",
		"--to", "sb-recip01",
		"--subject", "hi",
		"--body", bodyFile,
	})
	if err == nil {
		t.Fatal("expected error for invalid --from sandbox ID, got nil")
	}
	if mockSES.called {
		t.Error("SES SendEmail should not have been called for invalid sandbox ID")
	}
}

// TestEmailSend_InvalidToSandboxID verifies that an invalid --to sandbox ID returns an error.
func TestEmailSend_InvalidToSandboxID(t *testing.T) {
	cfg := testEmailCfg()
	privB64, _ := genEd25519Pair(t)
	mockSSM := newEmailMockSSM(map[string]string{
		"/sandbox/sb-sender1/signing-key": privB64,
	})
	mockSES := &emailMockSES{}
	mockIdentity := &emailMockIdentity{}

	bodyFile := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(bodyFile, []byte("hello"), 0600); err != nil {
		t.Fatalf("write body file: %v", err)
	}

	sendDeps := &cmd.EmailSendDeps{SES: mockSES, SSMParam: mockSSM, Identity: mockIdentity}
	_, err := execEmailCmd(t, cfg, sendDeps, nil, []string{
		"email", "send",
		"--from", "sb-sender1",
		"--to", "NOT_VALID",
		"--subject", "hi",
		"--body", bodyFile,
	})
	if err == nil {
		t.Fatal("expected error for invalid --to sandbox ID, got nil")
	}
	if mockSES.called {
		t.Error("SES SendEmail should not have been called for invalid sandbox ID")
	}
}

// TestEmailSend_SuccessNoAttachments verifies a successful send with mocked clients.
func TestEmailSend_SuccessNoAttachments(t *testing.T) {
	cfg := testEmailCfg()
	privB64, _ := genEd25519Pair(t)
	mockSSM := newEmailMockSSM(map[string]string{
		"/sandbox/sb-sender1/signing-key": privB64,
	})
	mockSES := &emailMockSES{}
	mockIdentity := &emailMockIdentity{} // no identity rows → no encryption

	bodyFile := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(bodyFile, []byte("hello world"), 0600); err != nil {
		t.Fatalf("write body file: %v", err)
	}

	sendDeps := &cmd.EmailSendDeps{SES: mockSES, SSMParam: mockSSM, Identity: mockIdentity}
	out, err := execEmailCmd(t, cfg, sendDeps, nil, []string{
		"email", "send",
		"--from", "sb-sender1",
		"--to", "sb-recip01",
		"--subject", "Test Subject",
		"--body", bodyFile,
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !mockSES.called {
		t.Error("expected SES SendEmail to be called, but it was not")
	}
	if !strings.Contains(out, "sb-sender1") {
		t.Errorf("expected output to mention sender, got: %q", out)
	}
	if !strings.Contains(out, "sb-recip01") {
		t.Errorf("expected output to mention recipient, got: %q", out)
	}
	if !strings.Contains(out, "attachments: 0") {
		t.Errorf("expected output to say 0 attachments, got: %q", out)
	}
}

// TestEmailSend_TwoAttachments verifies that --attach reads two files into Attachment structs.
func TestEmailSend_TwoAttachments(t *testing.T) {
	cfg := testEmailCfg()
	privB64, _ := genEd25519Pair(t)
	mockSSM := newEmailMockSSM(map[string]string{
		"/sandbox/sb-sender1/signing-key": privB64,
	})
	mockSES := &emailMockSES{}
	mockIdentity := &emailMockIdentity{}

	tmpDir := t.TempDir()
	bodyFile := filepath.Join(tmpDir, "body.txt")
	attach1 := filepath.Join(tmpDir, "file1.txt")
	attach2 := filepath.Join(tmpDir, "file2.bin")
	if err := os.WriteFile(bodyFile, []byte("body"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attach1, []byte("attachment one"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(attach2, []byte("attachment two"), 0600); err != nil {
		t.Fatal(err)
	}

	sendDeps := &cmd.EmailSendDeps{SES: mockSES, SSMParam: mockSSM, Identity: mockIdentity}
	out, err := execEmailCmd(t, cfg, sendDeps, nil, []string{
		"email", "send",
		"--from", "sb-sender1",
		"--to", "sb-recip01",
		"--subject", "With Attachments",
		"--body", bodyFile,
		"--attach", attach1 + "," + attach2,
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !mockSES.called {
		t.Error("expected SES SendEmail to be called")
	}
	if !strings.Contains(out, "attachments: 2") {
		t.Errorf("expected output to say 2 attachments, got: %q", out)
	}

	// Verify the raw MIME contains both filenames.
	rawMIME := string(mockSES.input.Content.Raw.Data)
	if !strings.Contains(rawMIME, "file1.txt") {
		t.Errorf("expected MIME to contain file1.txt, got: %q", rawMIME[:min(200, len(rawMIME))])
	}
	if !strings.Contains(rawMIME, "file2.bin") {
		t.Errorf("expected MIME to contain file2.bin")
	}
}

// TestEmailSend_BodyFromStdin verifies that --body - reads from stdin.
func TestEmailSend_BodyFromStdin(t *testing.T) {
	cfg := testEmailCfg()
	privB64, _ := genEd25519Pair(t)
	mockSSM := newEmailMockSSM(map[string]string{
		"/sandbox/sb-sender1/signing-key": privB64,
	})
	mockSES := &emailMockSES{}
	mockIdentity := &emailMockIdentity{}

	// Write a temp file that will act as stdin (the CLI internally reads from os.Stdin).
	// We can test this by writing a body file and using "-"; we'll pass via a temp file as stdin.
	// Since the command reads from os.Stdin directly when bodyPath="-", we use a file for stdin instead.
	// The simplest approach: write body to a file with path "-"? No — instead test via a temp file.
	// Instead, verify that passing "-" as body with stdin redirected works by creating a pipe.
	// This requires testing the runEmailSend function directly or accepting the stdin limitation.
	// For CLI tests, we verify that "-" as the body flag doesn't cause a filesystem error.
	// (Actual stdin reading is tested by unit coverage of readBodyArg.)
	sendDeps := &cmd.EmailSendDeps{SES: mockSES, SSMParam: mockSSM, Identity: mockIdentity}

	// Redirect stdin to a pipe with known content.
	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	os.Stdin = r
	_, _ = w.WriteString("hello from stdin")
	w.Close()
	defer func() { os.Stdin = origStdin }()

	_, execErr := execEmailCmd(t, cfg, sendDeps, nil, []string{
		"email", "send",
		"--from", "sb-sender1",
		"--to", "sb-recip01",
		"--subject", "Stdin Test",
		"--body", "-",
	})
	if execErr != nil {
		t.Fatalf("expected no error with --body -, got: %v", execErr)
	}
	if !mockSES.called {
		t.Error("expected SES SendEmail to be called")
	}
	// Verify body contains stdin content.
	rawMIME := string(mockSES.input.Content.Raw.Data)
	if !strings.Contains(rawMIME, "hello from stdin") {
		t.Errorf("expected MIME to contain stdin body, got first 200 chars: %q", rawMIME[:min(200, len(rawMIME))])
	}
}

// ============================================================
// Tests: km email read (Task 7)
// ============================================================

// TestEmailRead_NoSandboxIDArg verifies that no positional arg returns an error.
func TestEmailRead_NoSandboxIDArg(t *testing.T) {
	cfg := testEmailCfg()
	_, err := execEmailCmd(t, cfg, nil, nil, []string{"email", "read"})
	if err == nil {
		t.Fatal("expected error when no sandbox ID arg, got nil")
	}
}

// TestEmailRead_EmptyMailbox verifies that an empty mailbox prints "No messages".
func TestEmailRead_EmptyMailbox(t *testing.T) {
	cfg := testEmailCfg()
	mockS3 := &emailMockS3{bodies: map[string][]byte{}} // no messages
	mockSSM := newEmailMockSSM(nil)
	mockIdentity := &emailMockIdentity{}

	readDeps := &cmd.EmailReadDeps{S3Client: mockS3, SSMParam: mockSSM, Identity: mockIdentity}
	out, err := execEmailCmd(t, cfg, nil, readDeps, []string{"email", "read", "sb-recip01"})
	if err != nil {
		t.Fatalf("expected no error for empty mailbox, got: %v", err)
	}
	if !strings.Contains(out, "No messages") {
		t.Errorf("expected 'No messages' output, got: %q", out)
	}
}

// TestEmailRead_SinglePlaintextMessage verifies a single plaintext message displays correctly.
func TestEmailRead_SinglePlaintextMessage(t *testing.T) {
	cfg := testEmailCfg()
	domain := "sandboxes.klankermaker.ai"
	sandboxID := "sb-recip01"
	toAddr := fmt.Sprintf("%s@%s", sandboxID, domain)
	fromAddr := fmt.Sprintf("sb-sender1@%s", domain)

	mime := buildMIME(fromAddr, toAddr, "Hello World", "sb-sender1", "This is the body.", "")

	mockS3 := &emailMockS3{
		bodies: map[string][]byte{
			"mail/msg001": mime,
		},
	}
	mockSSM := newEmailMockSSM(nil)
	// Receiver has open allow-list so all senders are accepted.
	mockIdentity := &emailMockIdentity{
		rows: map[string]map[string]dynamodbtypes.AttributeValue{
			sandboxID: {
				"sandbox_id":      &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
				"allowed_senders": &dynamodbtypes.AttributeValueMemberSS{Value: []string{"*"}},
			},
		},
	}

	readDeps := &cmd.EmailReadDeps{S3Client: mockS3, SSMParam: mockSSM, Identity: mockIdentity}
	out, err := execEmailCmd(t, cfg, nil, readDeps, []string{"email", "read", sandboxID})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if strings.Contains(out, "No messages") {
		t.Error("expected message to be displayed, got 'No messages'")
	}
	if !strings.Contains(out, "Hello World") {
		t.Errorf("expected subject 'Hello World' in output, got: %q", out)
	}
	if !strings.Contains(out, "This is the body.") {
		t.Errorf("expected body preview in output, got: %q", out)
	}
}

// TestEmailRead_SignedMessageShowsOK verifies that a signed message with valid signature shows OK.
func TestEmailRead_SignedMessageShowsOK(t *testing.T) {
	cfg := testEmailCfg()
	domain := "sandboxes.klankermaker.ai"
	sandboxID := "sb-recip01"
	senderID := "sb-sender1"
	toAddr := fmt.Sprintf("%s@%s", sandboxID, domain)
	fromAddr := fmt.Sprintf("%s@%s", senderID, domain)

	body := "Signed message body."
	privB64, pubB64 := genEd25519Pair(t)
	sigB64 := signBody(t, privB64, body)

	mime := buildMIME(fromAddr, toAddr, "Signed Subject", senderID, body, sigB64)

	// Provide sender's public key in DynamoDB.
	pubKeyBytes, _ := base64.StdEncoding.DecodeString(pubB64)
	mockIdentity := &emailMockIdentity{
		rows: map[string]map[string]dynamodbtypes.AttributeValue{
			senderID: {
				"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: senderID},
				"public_key": &dynamodbtypes.AttributeValueMemberS{Value: base64.StdEncoding.EncodeToString(pubKeyBytes)},
			},
			// receiver record — open allow-list
			sandboxID: {
				"sandbox_id":      &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
				"allowed_senders": &dynamodbtypes.AttributeValueMemberSS{Value: []string{"*"}},
			},
		},
	}

	mockS3 := &emailMockS3{
		bodies: map[string][]byte{
			"mail/signedmsg": mime,
		},
	}
	mockSSM := newEmailMockSSM(nil)

	readDeps := &cmd.EmailReadDeps{S3Client: mockS3, SSMParam: mockSSM, Identity: mockIdentity}
	out, err := execEmailCmd(t, cfg, nil, readDeps, []string{"email", "read", sandboxID})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, "OK") {
		t.Errorf("expected signature status 'OK' in output, got: %q", out)
	}
}

// TestEmailRead_JSONOutput verifies that --json produces a valid JSON array.
func TestEmailRead_JSONOutput(t *testing.T) {
	cfg := testEmailCfg()
	domain := "sandboxes.klankermaker.ai"
	sandboxID := "sb-recip01"
	toAddr := fmt.Sprintf("%s@%s", sandboxID, domain)
	fromAddr := fmt.Sprintf("sb-sender1@%s", domain)

	mime := buildMIME(fromAddr, toAddr, "JSON Subject", "sb-sender1", "JSON body.", "")

	mockS3 := &emailMockS3{
		bodies: map[string][]byte{
			"mail/jsonmsg": mime,
		},
	}
	mockIdentity := &emailMockIdentity{
		rows: map[string]map[string]dynamodbtypes.AttributeValue{
			sandboxID: {
				"sandbox_id":      &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
				"allowed_senders": &dynamodbtypes.AttributeValueMemberSS{Value: []string{"*"}},
			},
		},
	}
	mockSSM := newEmailMockSSM(nil)

	readDeps := &cmd.EmailReadDeps{S3Client: mockS3, SSMParam: mockSSM, Identity: mockIdentity}
	out, err := execEmailCmd(t, cfg, nil, readDeps, []string{"email", "read", "--json", sandboxID})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	var msgs []map[string]interface{}
	if jsonErr := json.Unmarshal([]byte(out), &msgs); jsonErr != nil {
		t.Fatalf("expected valid JSON array, got parse error: %v\noutput: %q", jsonErr, out)
	}
	if len(msgs) != 1 {
		t.Errorf("expected 1 message in JSON array, got %d", len(msgs))
	}
	if subject, ok := msgs[0]["subject"].(string); !ok || subject != "JSON Subject" {
		t.Errorf("expected subject 'JSON Subject', got: %v", msgs[0]["subject"])
	}
}

// TestEmailRead_MultipartExtractsAttachments verifies that a multipart/mixed message
// extracts attachment info and displays it.
func TestEmailRead_MultipartExtractsAttachments(t *testing.T) {
	cfg := testEmailCfg()
	domain := "sandboxes.klankermaker.ai"
	sandboxID := "sb-recip01"
	toAddr := fmt.Sprintf("%s@%s", sandboxID, domain)
	fromAddr := fmt.Sprintf("sb-sender1@%s", domain)

	attachData := []byte("attachment content here")
	mime := buildMultipartMIME(fromAddr, toAddr, "Multipart Subject", "sb-sender1", "Main body.", "report.txt", attachData)

	mockS3 := &emailMockS3{
		bodies: map[string][]byte{
			"mail/multipartmsg": mime,
		},
	}
	mockIdentity := &emailMockIdentity{
		rows: map[string]map[string]dynamodbtypes.AttributeValue{
			sandboxID: {
				"sandbox_id":      &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
				"allowed_senders": &dynamodbtypes.AttributeValueMemberSS{Value: []string{"*"}},
			},
		},
	}
	mockSSM := newEmailMockSSM(nil)

	readDeps := &cmd.EmailReadDeps{S3Client: mockS3, SSMParam: mockSSM, Identity: mockIdentity}
	out, err := execEmailCmd(t, cfg, nil, readDeps, []string{"email", "read", sandboxID})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, "attach") {
		t.Errorf("expected output to mention attachment, got: %q", out)
	}
}

// TestEmailRead_EncryptedMessageAutoDecrypts verifies that an encrypted message
// is auto-decrypted when keys are available.
func TestEmailRead_EncryptedMessageAutoDecrypts(t *testing.T) {
	cfg := testEmailCfg()
	domain := "sandboxes.klankermaker.ai"
	sandboxID := "sb-recip01"
	toAddr := fmt.Sprintf("%s@%s", sandboxID, domain)
	fromAddr := fmt.Sprintf("sb-sender1@%s", domain)

	// Generate NaCl box key pair for the recipient.
	encPub, encPriv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate NaCl key pair: %v", err)
	}
	privB64 := base64.StdEncoding.EncodeToString(encPriv[:])
	pubB64 := base64.StdEncoding.EncodeToString(encPub[:])

	// Encrypt the plaintext body using EncryptForRecipient.
	plaintext := "secret message"
	ciphertext, err := encryptForTest(encPub, []byte(plaintext))
	if err != nil {
		t.Fatalf("encrypt for test: %v", err)
	}
	ciphertextB64 := base64.StdEncoding.EncodeToString(ciphertext)

	// Build a MIME message with X-KM-Encrypted: true and the ciphertext as body.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("From: %s\r\n", fromAddr))
	sb.WriteString(fmt.Sprintf("To: %s\r\n", toAddr))
	sb.WriteString("Subject: Encrypted Test\r\n")
	sb.WriteString("X-KM-Sender-ID: sb-sender1\r\n")
	sb.WriteString("X-KM-Encrypted: true\r\n")
	sb.WriteString("Content-Type: text/plain\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(ciphertextB64)
	mime := []byte(sb.String())

	mockS3 := &emailMockS3{
		bodies: map[string][]byte{
			"mail/encryptedmsg": mime,
		},
	}
	mockIdentity := &emailMockIdentity{
		rows: map[string]map[string]dynamodbtypes.AttributeValue{
			sandboxID: {
				"sandbox_id":             &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
				"encryption_public_key":  &dynamodbtypes.AttributeValueMemberS{Value: pubB64},
				"allowed_senders":        &dynamodbtypes.AttributeValueMemberSS{Value: []string{"*"}},
			},
			// No sender record needed since we skip signature verification for unsigned messages.
		},
	}
	mockSSM := newEmailMockSSM(map[string]string{
		fmt.Sprintf("/sandbox/%s/encryption-key", sandboxID): privB64,
	})

	readDeps := &cmd.EmailReadDeps{S3Client: mockS3, SSMParam: mockSSM, Identity: mockIdentity}
	out, err := execEmailCmd(t, cfg, nil, readDeps, []string{"email", "read", sandboxID})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out, plaintext) {
		t.Errorf("expected decrypted plaintext %q in output, got: %q", plaintext, out)
	}
}

// ============================================================
// Helpers for encryption test
// ============================================================

// encryptForTest encrypts plaintext using NaCl box.SealAnonymous for the given public key.
// Mirrors kmaws.EncryptForRecipient without importing kmaws (avoids circular dep concerns).
func encryptForTest(recipientPubKey *[32]byte, plaintext []byte) ([]byte, error) {
	ciphertext, err := box.SealAnonymous(nil, plaintext, recipientPubKey, rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	return ciphertext, nil
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
