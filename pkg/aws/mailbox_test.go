// Package aws_test — mailbox_test.go
// Unit tests for the mailbox reader library: ListMailboxMessages, ReadMessage, ParseSignedMessage.
package aws_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ============================================================
// Mock: MailboxS3API
// ============================================================

type mockMailboxS3API struct {
	listCalled    bool
	listInput     *s3.ListObjectsV2Input
	listPages     []*s3.ListObjectsV2Output // returned in sequence
	listCallCount int
	listErr       error

	getCalled bool
	getInput  *s3.GetObjectInput
	getBody   []byte
	getErr    error
}

func (m *mockMailboxS3API) ListObjectsV2(ctx context.Context, input *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	m.listCalled = true
	m.listInput = input
	if m.listErr != nil {
		return nil, m.listErr
	}
	if len(m.listPages) == 0 {
		return &s3.ListObjectsV2Output{}, nil
	}
	page := m.listPages[m.listCallCount%len(m.listPages)]
	m.listCallCount++
	return page, nil
}

func (m *mockMailboxS3API) GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	m.getCalled = true
	m.getInput = input
	if m.getErr != nil {
		return nil, m.getErr
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(m.getBody)),
	}, nil
}

// ============================================================
// Helpers
// ============================================================

// buildTestMIME constructs a raw MIME message for mailbox testing.
// If sigB64 is non-empty, X-KM-Signature header is added.
// If encrypted is true, X-KM-Encrypted: true header is added.
func buildTestMIME(from, to, subject, senderID, body, sigB64 string, encrypted bool) []byte {
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
	if encrypted {
		sb.WriteString("X-KM-Encrypted: true\r\n")
	}
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return []byte(sb.String())
}

// makeTestEd25519Keys generates a real Ed25519 pair for mailbox tests.
func makeMailboxTestKeys(t *testing.T) (pubKeyB64, privKeyB64 string, pub ed25519.PublicKey, priv ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	pubKeyB64 = base64.StdEncoding.EncodeToString(pub)
	privKeyB64 = base64.StdEncoding.EncodeToString([]byte(priv))
	return pubKeyB64, privKeyB64, pub, priv
}

// signBody signs body bytes with Ed25519 private key.
func signBody(t *testing.T, priv ed25519.PrivateKey, body string) string {
	t.Helper()
	sig := ed25519.Sign(priv, []byte(body))
	return base64.StdEncoding.EncodeToString(sig)
}

// ============================================================
// ListMailboxMessages tests
// ============================================================

func TestListMailboxMessages_ReturnsAllKeys(t *testing.T) {
	mockS3 := &mockMailboxS3API{
		listPages: []*s3.ListObjectsV2Output{
			{
				Contents: []s3types.Object{
					{Key: aws.String("mail/msg-001.eml")},
					{Key: aws.String("mail/msg-002.eml")},
				},
				IsTruncated: aws.Bool(false),
			},
		},
	}

	keys, err := kmaws.ListMailboxMessages(context.Background(), mockS3, "km-artifacts", "sb-recv01", "example.com")
	if err != nil {
		t.Fatalf("ListMailboxMessages returned error: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d: %v", len(keys), keys)
	}
	if keys[0] != "mail/msg-001.eml" {
		t.Errorf("keys[0] = %q; want %q", keys[0], "mail/msg-001.eml")
	}
}

func TestListMailboxMessages_EmptyBucket(t *testing.T) {
	mockS3 := &mockMailboxS3API{
		listPages: []*s3.ListObjectsV2Output{
			{
				Contents:    []s3types.Object{},
				IsTruncated: aws.Bool(false),
			},
		},
	}

	keys, err := kmaws.ListMailboxMessages(context.Background(), mockS3, "km-artifacts", "sb-recv02", "example.com")
	if err != nil {
		t.Fatalf("ListMailboxMessages returned error for empty bucket: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected 0 keys for empty bucket, got %d", len(keys))
	}
}

// ============================================================
// ReadMessage tests
// ============================================================

func TestReadMessage_ReturnsRawBytes(t *testing.T) {
	wantBody := []byte("From: a@example.com\r\n\r\nHello world")
	mockS3 := &mockMailboxS3API{
		getBody: wantBody,
	}

	got, err := kmaws.ReadMessage(context.Background(), mockS3, "km-artifacts", "mail/msg-001.eml")
	if err != nil {
		t.Fatalf("ReadMessage returned error: %v", err)
	}
	if !bytes.Equal(got, wantBody) {
		t.Errorf("ReadMessage returned %q; want %q", got, wantBody)
	}
}

func TestReadMessage_ErrorOnMissingKey(t *testing.T) {
	mockS3 := &mockMailboxS3API{
		getErr: &s3types.NoSuchKey{},
	}

	_, err := kmaws.ReadMessage(context.Background(), mockS3, "km-artifacts", "mail/missing.eml")
	if err == nil {
		t.Fatal("expected error for missing key, got nil")
	}
}

// ============================================================
// ParseSignedMessage tests
// ============================================================

func TestParseSignedMessage_ParsesHeaders(t *testing.T) {
	pubKeyB64, privKeyB64, _, priv := makeMailboxTestKeys(t)
	_ = privKeyB64
	body := "Hello from sandbox!"
	sigB64 := signBody(t, priv, body)

	rawMIME := buildTestMIME("sender@example.com", "recv@example.com", "Test subject",
		"sb-sender01", body, sigB64, false)

	msg, err := kmaws.ParseSignedMessage(rawMIME, "sb-recv01", pubKeyB64, []string{"*"}, "")
	if err != nil {
		t.Fatalf("ParseSignedMessage returned error: %v", err)
	}
	if msg.From != "sender@example.com" {
		t.Errorf("From = %q; want %q", msg.From, "sender@example.com")
	}
	if msg.To != "recv@example.com" {
		t.Errorf("To = %q; want %q", msg.To, "recv@example.com")
	}
	if msg.Subject != "Test subject" {
		t.Errorf("Subject = %q; want %q", msg.Subject, "Test subject")
	}
	if msg.SenderID != "sb-sender01" {
		t.Errorf("SenderID = %q; want %q", msg.SenderID, "sb-sender01")
	}
}

func TestParseSignedMessage_SignatureOK_ValidSig(t *testing.T) {
	pubKeyB64, _, _, priv := makeMailboxTestKeys(t)
	body := "Signed body content"
	sigB64 := signBody(t, priv, body)

	rawMIME := buildTestMIME("s@ex.com", "r@ex.com", "Subj", "sb-sender02", body, sigB64, false)

	msg, err := kmaws.ParseSignedMessage(rawMIME, "sb-recv02", pubKeyB64, []string{"*"}, "")
	if err != nil {
		t.Fatalf("ParseSignedMessage returned error: %v", err)
	}
	if !msg.SignatureOK {
		t.Error("expected SignatureOK=true for valid Ed25519 signature")
	}
	if msg.Plaintext {
		t.Error("expected Plaintext=false when X-KM-Signature is present")
	}
}

func TestParseSignedMessage_SignatureOK_InvalidSig(t *testing.T) {
	pubKeyB64, _, _, _ := makeMailboxTestKeys(t)
	body := "Signed body"
	invalidSig := base64.StdEncoding.EncodeToString(make([]byte, 64)) // all zeros

	rawMIME := buildTestMIME("s@ex.com", "r@ex.com", "Subj", "sb-sender03", body, invalidSig, false)

	msg, err := kmaws.ParseSignedMessage(rawMIME, "sb-recv03", pubKeyB64, []string{"*"}, "")
	if err != nil {
		t.Fatalf("ParseSignedMessage should not return error for invalid sig; got: %v", err)
	}
	if msg.SignatureOK {
		t.Error("expected SignatureOK=false for invalid signature")
	}
}

func TestParseSignedMessage_Plaintext_NoSignatureHeader(t *testing.T) {
	pubKeyB64, _, _, _ := makeMailboxTestKeys(t)
	rawMIME := buildTestMIME("s@ex.com", "r@ex.com", "Subj", "sb-sender04", "plain body", "", false)

	msg, err := kmaws.ParseSignedMessage(rawMIME, "sb-recv04", pubKeyB64, []string{"*"}, "")
	if err != nil {
		t.Fatalf("ParseSignedMessage returned error for plaintext: %v", err)
	}
	if !msg.Plaintext {
		t.Error("expected Plaintext=true when X-KM-Signature header is absent")
	}
}

func TestParseSignedMessage_SenderNotOnAllowList_ReturnsError(t *testing.T) {
	pubKeyB64, _, _, priv := makeMailboxTestKeys(t)
	body := "Unauthorized"
	sigB64 := signBody(t, priv, body)
	rawMIME := buildTestMIME("s@ex.com", "r@ex.com", "Subj", "sb-stranger", body, sigB64, false)

	_, err := kmaws.ParseSignedMessage(rawMIME, "sb-recv05", pubKeyB64, []string{"self"}, "")
	if err == nil {
		t.Fatal("expected error for sender not on allow-list, got nil")
	}
	if !errors.Is(err, kmaws.ErrSenderNotAllowed) {
		t.Errorf("expected ErrSenderNotAllowed; got: %v", err)
	}
}

func TestParseSignedMessage_SelfMail_AlwaysPermitted(t *testing.T) {
	pubKeyB64, _, _, priv := makeMailboxTestKeys(t)
	body := "Self message"
	sigB64 := signBody(t, priv, body)
	// senderID == receiverSandboxID = "sb-self01"
	rawMIME := buildTestMIME("s@ex.com", "r@ex.com", "Subj", "sb-self01", body, sigB64, false)

	// allowedSenders is empty — self-mail should bypass
	msg, err := kmaws.ParseSignedMessage(rawMIME, "sb-self01", pubKeyB64, []string{}, "")
	if err != nil {
		t.Fatalf("ParseSignedMessage should permit self-mail; got error: %v", err)
	}
	if msg.SenderID != "sb-self01" {
		t.Errorf("SenderID = %q; want %q", msg.SenderID, "sb-self01")
	}
}

func TestParseSignedMessage_EncryptedBody(t *testing.T) {
	pubKeyB64, _, _, priv := makeMailboxTestKeys(t)
	body := base64.StdEncoding.EncodeToString([]byte("encrypted-ciphertext"))
	sigB64 := signBody(t, priv, body)
	rawMIME := buildTestMIME("s@ex.com", "r@ex.com", "Subj", "sb-enc01", body, sigB64, true)

	msg, err := kmaws.ParseSignedMessage(rawMIME, "sb-recv-enc", pubKeyB64, []string{"*"}, "")
	if err != nil {
		t.Fatalf("ParseSignedMessage returned error for encrypted body: %v", err)
	}
	if !msg.Encrypted {
		t.Error("expected Encrypted=true when X-KM-Encrypted: true is present")
	}
}

// ============================================================
// Safe phrase tests
// ============================================================

func TestSafePhraseExtraction(t *testing.T) {
	pubKeyB64, _, _, priv := makeMailboxTestKeys(t)
	body := "Hello!\nKM-AUTH: secret123\nMore text here."
	sigB64 := signBody(t, priv, body)
	rawMIME := buildTestMIME("s@ex.com", "r@ex.com", "Subj", "sb-sp01", body, sigB64, false)

	msg, err := kmaws.ParseSignedMessage(rawMIME, "sb-recv-sp", pubKeyB64, []string{"*"}, "")
	if err != nil {
		t.Fatalf("ParseSignedMessage returned error: %v", err)
	}
	if msg.SafePhrase != "secret123" {
		t.Errorf("SafePhrase = %q; want %q", msg.SafePhrase, "secret123")
	}
}

func TestSafePhraseMatch(t *testing.T) {
	pubKeyB64, _, _, priv := makeMailboxTestKeys(t)
	body := "KM-AUTH: correctphrase"
	sigB64 := signBody(t, priv, body)
	rawMIME := buildTestMIME("s@ex.com", "r@ex.com", "Subj", "sb-sp02", body, sigB64, false)

	msg, err := kmaws.ParseSignedMessage(rawMIME, "sb-recv-sp2", pubKeyB64, []string{"*"}, "correctphrase")
	if err != nil {
		t.Fatalf("ParseSignedMessage returned error: %v", err)
	}
	if !msg.SafePhraseOK {
		t.Error("expected SafePhraseOK=true when extracted phrase matches expected")
	}
	if msg.SafePhrase != "correctphrase" {
		t.Errorf("SafePhrase = %q; want %q", msg.SafePhrase, "correctphrase")
	}
}

func TestSafePhraseMismatch(t *testing.T) {
	pubKeyB64, _, _, priv := makeMailboxTestKeys(t)
	body := "KM-AUTH: wrongphrase"
	sigB64 := signBody(t, priv, body)
	rawMIME := buildTestMIME("s@ex.com", "r@ex.com", "Subj", "sb-sp03", body, sigB64, false)

	msg, err := kmaws.ParseSignedMessage(rawMIME, "sb-recv-sp3", pubKeyB64, []string{"*"}, "expectedphrase")
	if err != nil {
		t.Fatalf("ParseSignedMessage returned error: %v", err)
	}
	if msg.SafePhraseOK {
		t.Error("expected SafePhraseOK=false when extracted phrase does not match expected")
	}
}

func TestSafePhraseAbsent(t *testing.T) {
	pubKeyB64, _, _, priv := makeMailboxTestKeys(t)
	body := "No auth phrase in this body."
	sigB64 := signBody(t, priv, body)
	rawMIME := buildTestMIME("s@ex.com", "r@ex.com", "Subj", "sb-sp04", body, sigB64, false)

	msg, err := kmaws.ParseSignedMessage(rawMIME, "sb-recv-sp4", pubKeyB64, []string{"*"}, "someexpected")
	if err != nil {
		t.Fatalf("ParseSignedMessage returned error: %v", err)
	}
	if msg.SafePhrase != "" {
		t.Errorf("SafePhrase = %q; want empty string when KM-AUTH absent", msg.SafePhrase)
	}
	if msg.SafePhraseOK {
		t.Error("expected SafePhraseOK=false when KM-AUTH absent")
	}
}

func TestSafePhraseEmptyExpected(t *testing.T) {
	pubKeyB64, _, _, priv := makeMailboxTestKeys(t)
	body := "KM-AUTH: somephrase"
	sigB64 := signBody(t, priv, body)
	rawMIME := buildTestMIME("s@ex.com", "r@ex.com", "Subj", "sb-sp05", body, sigB64, false)

	msg, err := kmaws.ParseSignedMessage(rawMIME, "sb-recv-sp5", pubKeyB64, []string{"*"}, "")
	if err != nil {
		t.Fatalf("ParseSignedMessage returned error: %v", err)
	}
	// Even though KM-AUTH is present, SafePhraseOK should be false when expectedSafePhrase is ""
	if msg.SafePhraseOK {
		t.Error("expected SafePhraseOK=false when expectedSafePhrase is empty string")
	}
	// But SafePhrase itself should still be extracted
	if msg.SafePhrase != "somephrase" {
		t.Errorf("SafePhrase = %q; want %q (extraction still happens)", msg.SafePhrase, "somephrase")
	}
}

// ============================================================
// PollForApproval tests
// ============================================================

// buildReplyMIME constructs a minimal raw MIME reply email for PollForApproval testing.
func buildReplyMIME(from, to, subject, body string) []byte {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("From: %s\r\n", from))
	sb.WriteString(fmt.Sprintf("To: %s\r\n", to))
	sb.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return []byte(sb.String())
}

// mockPollS3 returns a mock that serves a list page followed by per-key GetObject responses.
type mockPollS3 struct {
	keys     []string // S3 keys to return in list
	messages map[string][]byte // key -> raw MIME bytes
}

func (m *mockPollS3) ListObjectsV2(ctx context.Context, input *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	var objs []s3types.Object
	for _, k := range m.keys {
		k := k
		objs = append(objs, s3types.Object{Key: &k})
	}
	return &s3.ListObjectsV2Output{
		Contents:    objs,
		IsTruncated: aws.Bool(false),
	}, nil
}

func (m *mockPollS3) GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := ""
	if input.Key != nil {
		key = *input.Key
	}
	data, ok := m.messages[key]
	if !ok {
		return nil, &s3types.NoSuchKey{}
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(data)),
	}, nil
}

func TestPollForApproval_Approved(t *testing.T) {
	replyKey := "mail/reply-001.eml"
	replyMIME := buildReplyMIME(
		"operator@company.com",
		"sb-test01@sandboxes.example.com",
		"Re: [KM-APPROVAL-REQUEST] sb-test01 deploy-prod",
		"APPROVED\n\nLooks good to me.",
	)
	mock := &mockPollS3{
		keys:     []string{replyKey},
		messages: map[string][]byte{replyKey: replyMIME},
	}

	result, err := kmaws.PollForApproval(context.Background(), mock, "km-artifacts", "sb-test01", "example.com", "deploy-prod")
	if err != nil {
		t.Fatalf("PollForApproval returned error: %v", err)
	}
	if !result.Found {
		t.Error("expected Found=true when reply matches action")
	}
	if !result.Approved {
		t.Error("expected Approved=true when body contains APPROVED")
	}
	if result.Denied {
		t.Error("expected Denied=false when body contains APPROVED")
	}
}

func TestPollForApproval_Denied(t *testing.T) {
	replyKey := "mail/reply-002.eml"
	replyMIME := buildReplyMIME(
		"operator@company.com",
		"sb-test02@sandboxes.example.com",
		"Re: [KM-APPROVAL-REQUEST] sb-test02 delete-db",
		"DENIED\n\nThis action is not authorized.",
	)
	mock := &mockPollS3{
		keys:     []string{replyKey},
		messages: map[string][]byte{replyKey: replyMIME},
	}

	result, err := kmaws.PollForApproval(context.Background(), mock, "km-artifacts", "sb-test02", "example.com", "delete-db")
	if err != nil {
		t.Fatalf("PollForApproval returned error: %v", err)
	}
	if !result.Found {
		t.Error("expected Found=true when reply matches action")
	}
	if result.Approved {
		t.Error("expected Approved=false when body contains DENIED")
	}
	if !result.Denied {
		t.Error("expected Denied=true when body contains DENIED")
	}
}

func TestPollForApproval_NotFound(t *testing.T) {
	// Mailbox is empty — no reply
	mock := &mockPollS3{
		keys:     []string{},
		messages: map[string][]byte{},
	}

	result, err := kmaws.PollForApproval(context.Background(), mock, "km-artifacts", "sb-test03", "example.com", "some-action")
	if err != nil {
		t.Fatalf("PollForApproval returned error for empty mailbox: %v", err)
	}
	if result.Found {
		t.Error("expected Found=false when no reply in mailbox")
	}
	if result.Approved {
		t.Error("expected Approved=false when no reply found")
	}
}

func TestPollForApproval_CaseInsensitive(t *testing.T) {
	replyKey := "mail/reply-004.eml"
	replyMIME := buildReplyMIME(
		"operator@company.com",
		"sb-test04@sandboxes.example.com",
		"Re: [KM-APPROVAL-REQUEST] sb-test04 scale-up",
		"approved\n\nyes please",
	)
	mock := &mockPollS3{
		keys:     []string{replyKey},
		messages: map[string][]byte{replyKey: replyMIME},
	}

	result, err := kmaws.PollForApproval(context.Background(), mock, "km-artifacts", "sb-test04", "example.com", "scale-up")
	if err != nil {
		t.Fatalf("PollForApproval returned error: %v", err)
	}
	if !result.Found {
		t.Error("expected Found=true for case-insensitive match")
	}
	if !result.Approved {
		t.Error("expected Approved=true for lowercase 'approved'")
	}
}

func TestPollForApproval_SubjectMustContainAction(t *testing.T) {
	// Reply with mismatched action — should NOT be found
	replyKey := "mail/reply-005.eml"
	replyMIME := buildReplyMIME(
		"operator@company.com",
		"sb-test05@sandboxes.example.com",
		"Re: [KM-APPROVAL-REQUEST] sb-test05 other-action",
		"APPROVED",
	)
	mock := &mockPollS3{
		keys:     []string{replyKey},
		messages: map[string][]byte{replyKey: replyMIME},
	}

	result, err := kmaws.PollForApproval(context.Background(), mock, "km-artifacts", "sb-test05", "example.com", "deploy-prod")
	if err != nil {
		t.Fatalf("PollForApproval returned error: %v", err)
	}
	if result.Found {
		t.Error("expected Found=false when reply subject does not contain the requested action")
	}
}

func TestSafePhraseAtStartOfLine(t *testing.T) {
	pubKeyB64, _, _, priv := makeMailboxTestKeys(t)
	// Test KM-AUTH at start of line (after newline)
	body := "Preamble text\nKM-AUTH: linestart\nTrailing text"
	sigB64 := signBody(t, priv, body)
	rawMIME := buildTestMIME("s@ex.com", "r@ex.com", "Subj", "sb-sp06", body, sigB64, false)

	msg, err := kmaws.ParseSignedMessage(rawMIME, "sb-recv-sp6", pubKeyB64, []string{"*"}, "linestart")
	if err != nil {
		t.Fatalf("ParseSignedMessage returned error: %v", err)
	}
	if msg.SafePhrase != "linestart" {
		t.Errorf("SafePhrase = %q; want %q for KM-AUTH after newline", msg.SafePhrase, "linestart")
	}
	if !msg.SafePhraseOK {
		t.Error("expected SafePhraseOK=true for matching phrase at start of line")
	}
}
