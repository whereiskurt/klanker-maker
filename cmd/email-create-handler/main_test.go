package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// ---- mock implementations ----

type mockS3 struct {
	objects map[string][]byte
	putKeys []string
}

func (m *mockS3) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := awssdk.ToString(input.Key)
	data, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("NoSuchKey: %s", key)
	}
	return &s3.GetObjectOutput{Body: nopCloser(bytes.NewReader(data))}, nil
}

func (m *mockS3) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	m.putKeys = append(m.putKeys, awssdk.ToString(input.Key))
	return &s3.PutObjectOutput{}, nil
}

type mockSSM struct {
	params map[string]string
}

func (m *mockSSM) GetParameter(_ context.Context, input *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	name := awssdk.ToString(input.Name)
	val, ok := m.params[name]
	if !ok {
		return nil, fmt.Errorf("ParameterNotFound: %s", name)
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: awssdk.String(val)}}, nil
}

type mockEB struct {
	events []*eventbridge.PutEventsInput
}

func (m *mockEB) PutEvents(_ context.Context, input *eventbridge.PutEventsInput, _ ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error) {
	m.events = append(m.events, input)
	return &eventbridge.PutEventsOutput{}, nil
}

type mockSES struct {
	sent []*sesv2.SendEmailInput
}

func (m *mockSES) SendEmail(_ context.Context, input *sesv2.SendEmailInput, _ ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	m.sent = append(m.sent, input)
	return &sesv2.SendEmailOutput{}, nil
}

// nopCloser wraps a reader with a no-op Close.
type nopCloserReader struct {
	*bytes.Reader
}

func (nopCloserReader) Close() error { return nil }

func nopCloser(r *bytes.Reader) interface{ Read([]byte) (int, error); Close() error } {
	return nopCloserReader{r}
}

// ---- email builders ----

const testYAML = `name: test-sandbox
ttl: 1h
compute:
  type: ec2
  instance_type: t3.micro
`

const testSafePhrase = "secret123"

// buildMIMEEmail creates a multipart/mixed RFC 5322 email with a text body and a YAML attachment.
func buildMIMEEmail(from, body, yamlAttachment string) []byte {
	var buf bytes.Buffer
	boundary := "testboundary"

	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: create@sandboxes.example.com\r\n")
	fmt.Fprintf(&buf, "Subject: Create Sandbox\r\n")
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%q\r\n", boundary)
	fmt.Fprintf(&buf, "\r\n")

	mw := multipart.NewWriter(&buf)
	_ = mw.SetBoundary(boundary)

	// Text body part
	textHeader := make(textproto.MIMEHeader)
	textHeader.Set("Content-Type", "text/plain; charset=utf-8")
	pw, _ := mw.CreatePart(textHeader)
	fmt.Fprint(pw, body)

	// YAML attachment part
	yamlHeader := make(textproto.MIMEHeader)
	yamlHeader.Set("Content-Type", "text/yaml; charset=utf-8")
	yamlHeader.Set("Content-Disposition", `attachment; filename="profile.yaml"`)
	yw, _ := mw.CreatePart(yamlHeader)
	fmt.Fprint(yw, yamlAttachment)

	mw.Close()
	return buf.Bytes()
}

// buildPlainEmail creates a single-part text/plain RFC 5322 email.
func buildPlainEmail(from, body string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: create@sandboxes.example.com\r\n")
	fmt.Fprintf(&buf, "Subject: Create Sandbox\r\n")
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&buf, "\r\n")
	fmt.Fprint(&buf, body)
	return buf.Bytes()
}

// buildEventRecord creates an S3EventRecord with a single record entry.
func buildEventRecord(bucket, key string) S3EventRecord {
	return S3EventRecord{
		Records: []S3Record{
			{S3: S3Detail{Bucket: S3Bucket{Name: bucket}, Object: S3Object{Key: key}}},
		},
	}
}

// ---- helper: new handler ----

func newTestHandler(s3data map[string][]byte, safePhraseParam string, eb *mockEB, ses *mockSES) *EmailCreateHandler {
	return &EmailCreateHandler{
		S3Client: &mockS3{objects: s3data},
		SSMClient: &mockSSM{params: map[string]string{
			"/km/config/remote-create/safe-phrase": safePhraseParam,
		}},
		EventBridgeClient: eb,
		SESClient:         ses,
		ArtifactBucket:    "test-bucket",
		Domain:            "example.com",
		SafePhraseSSMKey:  "/km/config/remote-create/safe-phrase",
	}
}

// ---- tests ----

// Test: S3EventRecord struct deserializes correctly from SES S3 notification JSON.
func TestS3EventRecord_JSONDeserialization(t *testing.T) {
	raw := `{
		"Records": [{
			"s3": {
				"bucket": {"name": "my-bucket"},
				"object": {"key": "mail/abc123"}
			}
		}]
	}`
	var rec S3EventRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rec.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(rec.Records))
	}
	if rec.Records[0].S3.Bucket.Name != "my-bucket" {
		t.Errorf("bucket: got %q, want %q", rec.Records[0].S3.Bucket.Name, "my-bucket")
	}
	if rec.Records[0].S3.Object.Key != "mail/abc123" {
		t.Errorf("key: got %q, want %q", rec.Records[0].S3.Object.Key, "mail/abc123")
	}
}

// Test: handleEmailCreate parses YAML from multipart MIME attachment.
func TestHandleEmailCreate_MultipartYAMLAttachment(t *testing.T) {
	emailBody := fmt.Sprintf("KM-AUTH: %s\n", testSafePhrase)
	rawEmail := buildMIMEEmail("operator@corp.com", emailBody, testYAML)

	eb := &mockEB{}
	ses := &mockSES{}
	s3data := map[string][]byte{"mail/msg001": rawEmail}
	h := newTestHandler(s3data, testSafePhrase, eb, ses)

	event := buildEventRecord("test-bucket", "mail/msg001")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	if len(eb.events) != 1 {
		t.Errorf("expected 1 EventBridge event, got %d", len(eb.events))
	}
	if len(ses.sent) != 1 {
		t.Errorf("expected 1 SES acknowledgment, got %d", len(ses.sent))
	}
}

// Test: handleEmailCreate parses YAML from plain text body when no attachment present.
func TestHandleEmailCreate_PlainTextBody(t *testing.T) {
	plainBody := fmt.Sprintf("KM-AUTH: %s\n%s", testSafePhrase, testYAML)
	rawEmail := buildPlainEmail("operator@corp.com", plainBody)

	eb := &mockEB{}
	ses := &mockSES{}
	s3data := map[string][]byte{"mail/msg002": rawEmail}
	h := newTestHandler(s3data, testSafePhrase, eb, ses)

	event := buildEventRecord("test-bucket", "mail/msg002")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	if len(eb.events) != 1 {
		t.Errorf("expected 1 EventBridge event, got %d", len(eb.events))
	}
}

// Test: handleEmailCreate rejects email with missing KM-AUTH phrase.
func TestHandleEmailCreate_MissingKMAuth(t *testing.T) {
	rawEmail := buildPlainEmail("operator@corp.com", testYAML)

	eb := &mockEB{}
	ses := &mockSES{}
	s3data := map[string][]byte{"mail/msg003": rawEmail}
	h := newTestHandler(s3data, testSafePhrase, eb, ses)

	event := buildEventRecord("test-bucket", "mail/msg003")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle should not return error for rejection: %v", err)
	}

	if len(eb.events) != 0 {
		t.Errorf("expected 0 EventBridge events, got %d", len(eb.events))
	}
	if len(ses.sent) != 1 {
		t.Errorf("expected 1 rejection SES email, got %d", len(ses.sent))
	}
}

// Test: handleEmailCreate rejects email with wrong KM-AUTH phrase.
func TestHandleEmailCreate_WrongKMAuth(t *testing.T) {
	plainBody := fmt.Sprintf("KM-AUTH: wrongphrase\n%s", testYAML)
	rawEmail := buildPlainEmail("operator@corp.com", plainBody)

	eb := &mockEB{}
	ses := &mockSES{}
	s3data := map[string][]byte{"mail/msg004": rawEmail}
	h := newTestHandler(s3data, testSafePhrase, eb, ses)

	event := buildEventRecord("test-bucket", "mail/msg004")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle should not return error for rejection: %v", err)
	}

	if len(eb.events) != 0 {
		t.Errorf("expected 0 EventBridge events, got %d", len(eb.events))
	}
	if len(ses.sent) != 1 {
		t.Errorf("expected 1 rejection SES email, got %d", len(ses.sent))
	}
}

// Test: handleEmailCreate accepts correct KM-AUTH and valid YAML -> EventBridge event published.
func TestHandleEmailCreate_CorrectKMAuth_ValidYAML(t *testing.T) {
	plainBody := fmt.Sprintf("KM-AUTH: %s\n%s", testSafePhrase, testYAML)
	rawEmail := buildPlainEmail("operator@corp.com", plainBody)

	eb := &mockEB{}
	ses := &mockSES{}
	s3data := map[string][]byte{"mail/msg005": rawEmail}
	h := &EmailCreateHandler{
		S3Client: &mockS3{objects: s3data},
		SSMClient: &mockSSM{params: map[string]string{
			"/km/config/remote-create/safe-phrase": testSafePhrase,
		}},
		EventBridgeClient: eb,
		SESClient:         ses,
		ArtifactBucket:    "test-bucket",
		Domain:            "example.com",
		SafePhraseSSMKey:  "/km/config/remote-create/safe-phrase",
	}

	event := buildEventRecord("test-bucket", "mail/msg005")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	if len(eb.events) != 1 {
		t.Fatalf("expected 1 EventBridge event, got %d", len(eb.events))
	}
	// Verify event detail fields
	detail := awssdk.ToString(eb.events[0].Entries[0].Detail)
	if detail == "" {
		t.Error("expected non-empty event detail")
	}
	// Acknowledgment email should be sent
	if len(ses.sent) != 1 {
		t.Errorf("expected 1 acknowledgment email, got %d", len(ses.sent))
	}
}

// Test: handleEmailCreate validates parsed YAML via profile.Parse before dispatching.
func TestHandleEmailCreate_InvalidYAML(t *testing.T) {
	// Use YAML that goccy/go-yaml actually rejects (duplicate mapping key)
	plainBody := fmt.Sprintf("KM-AUTH: %s\nkey: : bad", testSafePhrase)
	rawEmail := buildPlainEmail("operator@corp.com", plainBody)

	eb := &mockEB{}
	ses := &mockSES{}
	s3data := map[string][]byte{"mail/msg006": rawEmail}
	h := newTestHandler(s3data, testSafePhrase, eb, ses)

	event := buildEventRecord("test-bucket", "mail/msg006")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle should not return error for invalid YAML rejection: %v", err)
	}

	if len(eb.events) != 0 {
		t.Errorf("expected 0 EventBridge events, got %d", len(eb.events))
	}
	if len(ses.sent) != 1 {
		t.Errorf("expected 1 rejection SES email, got %d", len(ses.sent))
	}
}
