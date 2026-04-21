package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
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

// mockDynamo implements SandboxMetadataAPI for testing — returns empty results by default.
type mockDynamo struct {
	items map[string]map[string]dynamodbtypes.AttributeValue
}

func (m *mockDynamo) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if m.items != nil {
		// Key is sandbox_id
		if k, ok := input.Key["sandbox_id"]; ok {
			id := k.(*dynamodbtypes.AttributeValueMemberS).Value
			if item, ok := m.items[id]; ok {
				return &dynamodb.GetItemOutput{Item: item}, nil
			}
		}
	}
	return &dynamodb.GetItemOutput{}, nil // empty item = not found
}

func (m *mockDynamo) PutItem(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}

func (m *mockDynamo) UpdateItem(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return &dynamodb.UpdateItemOutput{}, nil
}

func (m *mockDynamo) DeleteItem(_ context.Context, _ *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}

func (m *mockDynamo) Scan(_ context.Context, _ *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	return &dynamodb.ScanOutput{Items: []map[string]dynamodbtypes.AttributeValue{}}, nil
}

func (m *mockDynamo) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{Items: []map[string]dynamodbtypes.AttributeValue{}}, nil
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

// buildMIMEEmailWithSubject creates a multipart/mixed RFC 5322 email with a custom subject.
func buildMIMEEmailWithSubject(from, subject, body, yamlAttachment string) []byte {
	var buf bytes.Buffer
	boundary := "testboundary"

	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: operator@sandboxes.example.com\r\n")
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%q\r\n", boundary)
	fmt.Fprintf(&buf, "\r\n")

	mw := multipart.NewWriter(&buf)
	_ = mw.SetBoundary(boundary)

	textHeader := make(textproto.MIMEHeader)
	textHeader.Set("Content-Type", "text/plain; charset=utf-8")
	pw, _ := mw.CreatePart(textHeader)
	fmt.Fprint(pw, body)

	yamlHeader := make(textproto.MIMEHeader)
	yamlHeader.Set("Content-Type", "text/yaml; charset=utf-8")
	yamlHeader.Set("Content-Disposition", `attachment; filename="profile.yaml"`)
	yw, _ := mw.CreatePart(yamlHeader)
	fmt.Fprint(yw, yamlAttachment)

	mw.Close()
	return buf.Bytes()
}

// buildMIMEEmail creates a multipart email with "Create Sandbox" subject (backward compat).
func buildMIMEEmail(from, body, yamlAttachment string) []byte {
	return buildMIMEEmailWithSubject(from, "Create Sandbox", body, yamlAttachment)
}

// buildPlainEmailWithSubject creates a single-part text/plain email with a custom subject.
func buildPlainEmailWithSubject(from, subject, body string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: operator@sandboxes.example.com\r\n")
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&buf, "\r\n")
	fmt.Fprint(&buf, body)
	return buf.Bytes()
}

// buildPlainEmail creates a plain email with "Create Sandbox" subject (backward compat).
func buildPlainEmail(from, body string) []byte {
	return buildPlainEmailWithSubject(from, "Create Sandbox", body)
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

func newTestHandler(s3data map[string][]byte, safePhraseParam string, eb *mockEB, ses *mockSES) *OperatorEmailHandler {
	return &OperatorEmailHandler{
		S3Client: &mockS3{objects: s3data},
		SSMClient: &mockSSM{params: map[string]string{
			"/km/config/remote-create/safe-phrase": safePhraseParam,
		}},
		EventBridgeClient: eb,
		SESClient:         ses,
		ArtifactBucket:    "test-bucket",
		StateBucket:       "test-state-bucket",
		Domain:            "example.com",
		SafePhraseSSMKey:  "/km/config/remote-create/safe-phrase",
	}
}

// newTestHandlerWithAI creates a handler with BedrockClient wired in and a mock DynamoDB client.
func newTestHandlerWithAI(s3Client OperatorS3API, safePhraseParam string, eb *mockEB, ses *mockSES, bedrock BedrockRuntimeAPI) *OperatorEmailHandler {
	return &OperatorEmailHandler{
		S3Client: s3Client,
		DynamoClient: &mockDynamo{},
		SandboxTableName: "km-sandboxes",
		SSMClient: &mockSSM{params: map[string]string{
			"/km/config/remote-create/safe-phrase": safePhraseParam,
		}},
		EventBridgeClient: eb,
		SESClient:         ses,
		ArtifactBucket:    "test-bucket",
		StateBucket:       "test-state-bucket",
		Domain:            "example.com",
		SafePhraseSSMKey:  "/km/config/remote-create/safe-phrase",
		BedrockClient:     bedrock,
		BedrockModelID:    "us.anthropic.claude-haiku-4-5-20251001-v1:0",
	}
}

// buildPlainEmailWithHeaders creates a plain-text email with custom headers.
func buildPlainEmailWithHeaders(from, subject, body string, extraHeaders map[string]string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", from)
	fmt.Fprintf(&buf, "To: operator@sandboxes.example.com\r\n")
	fmt.Fprintf(&buf, "Subject: %s\r\n", subject)
	for k, v := range extraHeaders {
		fmt.Fprintf(&buf, "%s: %s\r\n", k, v)
	}
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&buf, "\r\n")
	fmt.Fprint(&buf, body)
	return buf.Bytes()
}

// ---- AI path tests ----

// Test: free-form email with BedrockClient → action command → confirmation reply + S3 save.
func TestHandleEmail_AIPath_ActionCommand(t *testing.T) {
	cmdJSON := `{"command":"create","type":"action","profile":"open-dev","overrides":{"ttl":"2h"},"confidence":0.92,"reasoning":"User wants an open-dev sandbox"}`
	bedrock := &mockBedrock{response: buildHaikuResponseBody(cmdJSON)}
	s3mock := &mockS3WithBody{objects: map[string][]byte{}}

	emailBody := fmt.Sprintf("KM-AUTH: %s\nspin up a goose sandbox\n", testSafePhrase)
	rawEmail := buildPlainEmailWithSubject("operator@corp.com", "Hey can you help", emailBody)
	s3mock.objects["mail/ai001"] = rawEmail

	eb := &mockEB{}
	ses := &mockSES{}
	h := newTestHandlerWithAI(s3mock, testSafePhrase, eb, ses, bedrock)

	event := buildEventRecord("test-bucket", "mail/ai001")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	// Should send confirmation reply, not execute EventBridge yet
	if len(eb.events) != 0 {
		t.Errorf("action command should not dispatch EventBridge before confirmation, got %d events", len(eb.events))
	}
	if len(ses.sent) != 1 {
		t.Fatalf("expected 1 confirmation reply, got %d", len(ses.sent))
	}
	replyBody := awssdk.ToString(ses.sent[0].Content.Simple.Body.Text.Data)
	if !strings.Contains(replyBody, "YES") && !strings.Contains(replyBody, "yes") {
		t.Errorf("confirmation reply should mention YES, got: %s", replyBody)
	}
	if !strings.Contains(replyBody, "create") {
		t.Errorf("confirmation reply should mention command, got: %s", replyBody)
	}

	// Should save conversation state to S3
	found := false
	for _, k := range s3mock.putKeys {
		if strings.HasPrefix(k, "mail/conversations/") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected conversation state saved to S3 under mail/conversations/, putKeys=%v", s3mock.putKeys)
	}
}

// Test: info command "list" → executes immediately, replies, no confirmation, no S3 save.
func TestHandleEmail_AIPath_InfoCommand_List(t *testing.T) {
	cmdJSON := `{"command":"list","type":"info","profile":"","overrides":{},"confidence":0.95,"reasoning":"User wants to list running sandboxes"}`
	bedrock := &mockBedrock{response: buildHaikuResponseBody(cmdJSON)}
	s3mock := &mockS3WithBody{objects: map[string][]byte{}}

	emailBody := fmt.Sprintf("KM-AUTH: %s\nwhat sandboxes are running?\n", testSafePhrase)
	rawEmail := buildPlainEmailWithSubject("operator@corp.com", "Quick question", emailBody)
	s3mock.objects["mail/ai002"] = rawEmail

	eb := &mockEB{}
	ses := &mockSES{}
	h := newTestHandlerWithAI(s3mock, testSafePhrase, eb, ses, bedrock)

	event := buildEventRecord("test-bucket", "mail/ai002")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	// Info command: should send immediate reply
	if len(ses.sent) != 1 {
		t.Fatalf("expected 1 reply for info command, got %d", len(ses.sent))
	}
	// No conversation state saved
	for _, k := range s3mock.putKeys {
		if strings.HasPrefix(k, "mail/conversations/") {
			t.Errorf("info command should not save conversation state, but saved key: %s", k)
		}
	}
	// No EventBridge event
	if len(eb.events) != 0 {
		t.Errorf("info command should not dispatch EventBridge, got %d events", len(eb.events))
	}
}

// Test: low confidence → clarifying question, state saved as "new".
func TestHandleEmail_AIPath_LowConfidence(t *testing.T) {
	cmdJSON := `{"command":"create","type":"action","profile":"","overrides":{},"confidence":0.4,"reasoning":"Ambiguous request"}`
	bedrock := &mockBedrock{response: buildHaikuResponseBody(cmdJSON)}
	s3mock := &mockS3WithBody{objects: map[string][]byte{}}

	emailBody := fmt.Sprintf("KM-AUTH: %s\ndo something with a sandbox maybe\n", testSafePhrase)
	rawEmail := buildPlainEmailWithSubject("operator@corp.com", "Something", emailBody)
	s3mock.objects["mail/ai003"] = rawEmail

	eb := &mockEB{}
	ses := &mockSES{}
	h := newTestHandlerWithAI(s3mock, testSafePhrase, eb, ses, bedrock)

	event := buildEventRecord("test-bucket", "mail/ai003")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	// Should send clarifying question, not confirmation
	if len(ses.sent) != 1 {
		t.Fatalf("expected 1 clarifying reply, got %d", len(ses.sent))
	}
	if len(eb.events) != 0 {
		t.Errorf("low confidence should not dispatch events, got %d", len(eb.events))
	}

	// Should save conversation state with state="new"
	var savedState *ConversationState
	for k, v := range s3mock.putBodies {
		if strings.HasPrefix(k, "mail/conversations/") {
			var cs ConversationState
			if err := json.Unmarshal(v, &cs); err == nil {
				savedState = &cs
			}
		}
	}
	if savedState == nil {
		t.Fatal("expected conversation state saved for low confidence")
	}
	if savedState.State != "new" {
		t.Errorf("expected conversation state 'new' for low confidence, got %q", savedState.State)
	}
}

// Test: YAML attachment with "Create" subject → fast-path to handleCreate (no Haiku).
func TestHandleEmail_FastPath_YAMLAttachment(t *testing.T) {
	cmdJSON := `{"command":"create","type":"action","profile":"open-dev","overrides":{},"confidence":0.95,"reasoning":"Explicit create"}`
	bedrock := &mockBedrock{response: buildHaikuResponseBody(cmdJSON)}
	s3mock := &mockS3WithBody{objects: map[string][]byte{}}

	emailBody := fmt.Sprintf("KM-AUTH: %s\n", testSafePhrase)
	rawEmail := buildMIMEEmail("operator@corp.com", emailBody, testYAML)
	s3mock.objects["mail/ai004"] = rawEmail

	eb := &mockEB{}
	ses := &mockSES{}
	h := newTestHandlerWithAI(s3mock, testSafePhrase, eb, ses, bedrock)

	// Capture mockBedrock calls — if bedrock is called, fast path was bypassed
	type countBedrock struct {
		mockBedrock
		calls int
	}
	// We can't easily intercept, but we verify EventBridge fires immediately (fast-path behavior)
	event := buildEventRecord("test-bucket", "mail/ai004")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	// Fast path: EventBridge fires immediately (not awaiting confirmation)
	if len(eb.events) != 1 {
		t.Errorf("YAML attachment fast-path should dispatch EventBridge immediately, got %d events", len(eb.events))
	}
}

// Test: subject with "status" keyword → handleStatus fast-path (no Haiku).
func TestHandleEmail_StatusStillWorks(t *testing.T) {
	cmdJSON := `{"command":"status","type":"info","profile":"","overrides":{},"confidence":0.99,"reasoning":"Status request"}`
	bedrock := &mockBedrock{response: buildHaikuResponseBody(cmdJSON)}

	body := fmt.Sprintf("KM-AUTH: %s\n", testSafePhrase)
	rawEmail := buildPlainEmailWithSubject("operator@corp.com", "status sb-abc12345", body)

	eb := &mockEB{}
	ses := &mockSES{}
	s3data := map[string][]byte{"mail/ai005": rawEmail}
	h := newTestHandlerWithAI(&mockS3{objects: s3data}, testSafePhrase, eb, ses, bedrock)

	event := buildEventRecord("test-bucket", "mail/ai005")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	// Status replies via SES (sandbox not found is graceful)
	if len(ses.sent) != 1 {
		t.Fatalf("expected 1 status reply, got %d", len(ses.sent))
	}
	if len(eb.events) != 0 {
		t.Errorf("status should not dispatch EventBridge events, got %d", len(eb.events))
	}
}

// Test: reply "yes" to awaiting_confirmation conversation → dispatches EventBridge create.
func TestHandleConversation_YesReply(t *testing.T) {
	threadID := "original-msg-id-001"
	resolvedCmd := &InterpretedCommand{
		Command:    "create",
		Type:       "action",
		Profile:    "open-dev",
		Overrides:  map[string]interface{}{"ttl": "2h"},
		Confidence: 0.92,
		Reasoning:  "User wants a sandbox",
	}
	conv := &ConversationState{
		ThreadID:    threadID,
		Sender:      "operator@corp.com",
		Started:     time.Now().Add(-5 * time.Minute),
		Updated:     time.Now().Add(-4 * time.Minute),
		State:       "awaiting_confirmation",
		ResolvedCmd: resolvedCmd,
		Messages:    []ConversationMsg{},
	}
	convJSON, _ := json.Marshal(conv)
	convKey := conversationKey(threadID)

	s3mock := &mockS3WithBody{objects: map[string][]byte{
		convKey: convJSON,
	}}

	// Reply email with In-Reply-To pointing to threadID
	emailBody := fmt.Sprintf("KM-AUTH: %s\nyes\n", testSafePhrase)
	rawEmail := buildPlainEmailWithHeaders("operator@corp.com", "Re: Sandbox confirmation", emailBody, map[string]string{
		"Message-ID":  "<reply-001@example.com>",
		"In-Reply-To": "<" + threadID + ">",
	})
	s3mock.objects["mail/ai006"] = rawEmail

	eb := &mockEB{}
	ses := &mockSES{}
	bedrock := &mockBedrock{} // should not be called
	h := newTestHandlerWithAI(s3mock, testSafePhrase, eb, ses, bedrock)

	event := buildEventRecord("test-bucket", "mail/ai006")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	// Should dispatch EventBridge create event
	if len(eb.events) != 1 {
		t.Fatalf("expected 1 EventBridge event on yes reply, got %d", len(eb.events))
	}
	// Should send "executing" reply
	if len(ses.sent) != 1 {
		t.Fatalf("expected 1 executing reply, got %d", len(ses.sent))
	}

	// Conversation state should be saved with state="confirmed"
	var savedConv *ConversationState
	for k, v := range s3mock.putBodies {
		if strings.HasPrefix(k, "mail/conversations/") {
			var cs ConversationState
			if err := json.Unmarshal(v, &cs); err == nil {
				savedConv = &cs
			}
		}
	}
	if savedConv == nil {
		t.Fatal("expected conversation state updated after yes reply")
	}
	if savedConv.State != "confirmed" {
		t.Errorf("expected state 'confirmed' after yes reply, got %q", savedConv.State)
	}
}

// Test: reply "cancel" → cancellation acknowledgment, state saved as "cancelled".
func TestHandleConversation_CancelReply(t *testing.T) {
	threadID := "original-msg-id-002"
	resolvedCmd := &InterpretedCommand{
		Command:    "create",
		Type:       "action",
		Profile:    "open-dev",
		Overrides:  map[string]interface{}{},
		Confidence: 0.9,
		Reasoning:  "User wants a sandbox",
	}
	conv := &ConversationState{
		ThreadID:    threadID,
		Sender:      "operator@corp.com",
		Started:     time.Now().Add(-5 * time.Minute),
		Updated:     time.Now().Add(-4 * time.Minute),
		State:       "awaiting_confirmation",
		ResolvedCmd: resolvedCmd,
		Messages:    []ConversationMsg{},
	}
	convJSON, _ := json.Marshal(conv)
	convKey := conversationKey(threadID)

	s3mock := &mockS3WithBody{objects: map[string][]byte{
		convKey: convJSON,
	}}

	emailBody := fmt.Sprintf("KM-AUTH: %s\ncancel\n", testSafePhrase)
	rawEmail := buildPlainEmailWithHeaders("operator@corp.com", "Re: Sandbox confirmation", emailBody, map[string]string{
		"Message-ID":  "<reply-002@example.com>",
		"In-Reply-To": "<" + threadID + ">",
	})
	s3mock.objects["mail/ai007"] = rawEmail

	eb := &mockEB{}
	ses := &mockSES{}
	bedrock := &mockBedrock{}
	h := newTestHandlerWithAI(s3mock, testSafePhrase, eb, ses, bedrock)

	event := buildEventRecord("test-bucket", "mail/ai007")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	if len(eb.events) != 0 {
		t.Errorf("cancel should not dispatch EventBridge events, got %d", len(eb.events))
	}
	if len(ses.sent) != 1 {
		t.Fatalf("expected 1 cancellation reply, got %d", len(ses.sent))
	}

	// State should be saved as "cancelled"
	var savedConv *ConversationState
	for k, v := range s3mock.putBodies {
		if strings.HasPrefix(k, "mail/conversations/") {
			var cs ConversationState
			if err := json.Unmarshal(v, &cs); err == nil {
				savedConv = &cs
			}
		}
	}
	if savedConv == nil {
		t.Fatal("expected conversation state updated after cancel reply")
	}
	if savedConv.State != "cancelled" {
		t.Errorf("expected state 'cancelled' after cancel reply, got %q", savedConv.State)
	}
}

// Test: revision reply → calls Haiku again with revised context, updated confirmation sent.
func TestHandleConversation_RevisionReply(t *testing.T) {
	threadID := "original-msg-id-003"
	resolvedCmd := &InterpretedCommand{
		Command:    "create",
		Type:       "action",
		Profile:    "open-dev",
		Overrides:  map[string]interface{}{"ttl": "2h"},
		Confidence: 0.88,
		Reasoning:  "User wants a sandbox",
	}
	conv := &ConversationState{
		ThreadID:    threadID,
		Sender:      "operator@corp.com",
		Started:     time.Now().Add(-5 * time.Minute),
		Updated:     time.Now().Add(-4 * time.Minute),
		State:       "awaiting_confirmation",
		ResolvedCmd: resolvedCmd,
		Messages:    []ConversationMsg{},
	}
	convJSON, _ := json.Marshal(conv)
	convKey := conversationKey(threadID)

	// Bedrock returns revised command
	revisedJSON := `{"command":"create","type":"action","profile":"open-dev","overrides":{"ttl":"2h","instanceType":"t3.large"},"confidence":0.91,"reasoning":"User wants t3.large override"}`
	bedrock := &mockBedrock{response: buildHaikuResponseBody(revisedJSON)}

	s3mock := &mockS3WithBody{objects: map[string][]byte{
		convKey: convJSON,
	}}

	emailBody := fmt.Sprintf("KM-AUTH: %s\nmake it a t3.large instead\n", testSafePhrase)
	rawEmail := buildPlainEmailWithHeaders("operator@corp.com", "Re: Sandbox confirmation", emailBody, map[string]string{
		"Message-ID":  "<reply-003@example.com>",
		"In-Reply-To": "<" + threadID + ">",
	})
	s3mock.objects["mail/ai008"] = rawEmail

	eb := &mockEB{}
	ses := &mockSES{}
	h := newTestHandlerWithAI(s3mock, testSafePhrase, eb, ses, bedrock)

	event := buildEventRecord("test-bucket", "mail/ai008")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	// Should send updated confirmation (not execute)
	if len(eb.events) != 0 {
		t.Errorf("revision should not dispatch EventBridge events, got %d", len(eb.events))
	}
	if len(ses.sent) != 1 {
		t.Fatalf("expected 1 updated confirmation reply, got %d", len(ses.sent))
	}

	// State should be awaiting_confirmation with updated cmd
	var savedConv *ConversationState
	for k, v := range s3mock.putBodies {
		if strings.HasPrefix(k, "mail/conversations/") {
			var cs ConversationState
			if err := json.Unmarshal(v, &cs); err == nil {
				savedConv = &cs
			}
		}
	}
	if savedConv == nil {
		t.Fatal("expected conversation state updated after revision")
	}
	if savedConv.State != "awaiting_confirmation" {
		t.Errorf("expected state 'awaiting_confirmation' after revision, got %q", savedConv.State)
	}
}

// Test: destroy command → action type → confirmation required.
func TestHandleEmail_AIPath_DestroyCommand(t *testing.T) {
	cmdJSON := `{"command":"destroy","type":"action","profile":"","overrides":{"sandbox_id":"goose-abc12345"},"confidence":0.95,"reasoning":"User wants to destroy goose1"}`
	bedrock := &mockBedrock{response: buildHaikuResponseBody(cmdJSON)}
	s3mock := &mockS3WithBody{objects: map[string][]byte{}}

	emailBody := fmt.Sprintf("KM-AUTH: %s\ndestroy goose1\n", testSafePhrase)
	rawEmail := buildPlainEmailWithSubject("operator@corp.com", "Cleanup", emailBody)
	s3mock.objects["mail/ai009"] = rawEmail

	eb := &mockEB{}
	ses := &mockSES{}
	h := newTestHandlerWithAI(s3mock, testSafePhrase, eb, ses, bedrock)

	event := buildEventRecord("test-bucket", "mail/ai009")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	// Destroy is action — requires confirmation, no EventBridge
	if len(eb.events) != 0 {
		t.Errorf("destroy action should require confirmation, not dispatch EventBridge, got %d events", len(eb.events))
	}
	if len(ses.sent) != 1 {
		t.Fatalf("expected 1 confirmation reply for destroy, got %d", len(ses.sent))
	}
}

// Test: missing KM-AUTH — rejects before AI call.
func TestHandleEmail_MissingKMAuth_AIPath(t *testing.T) {
	cmdJSON := `{"command":"create","type":"action","profile":"open-dev","overrides":{},"confidence":0.95,"reasoning":"test"}`
	bedrock := &mockBedrock{response: buildHaikuResponseBody(cmdJSON)}
	s3mock := &mockS3WithBody{objects: map[string][]byte{}}

	// No KM-AUTH in email body
	rawEmail := buildPlainEmailWithSubject("operator@corp.com", "spin up a sandbox", "please create one for me")
	s3mock.objects["mail/ai010"] = rawEmail

	eb := &mockEB{}
	ses := &mockSES{}
	h := newTestHandlerWithAI(s3mock, testSafePhrase, eb, ses, bedrock)
	h.VerboseErrors = true // test expects rejection reply

	event := buildEventRecord("test-bucket", "mail/ai010")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle should not return error for rejection: %v", err)
	}

	// Should reject with KM-AUTH error, no events
	if len(eb.events) != 0 {
		t.Errorf("missing KM-AUTH should not dispatch events, got %d", len(eb.events))
	}
	if len(ses.sent) != 1 {
		t.Fatalf("expected 1 rejection reply, got %d", len(ses.sent))
	}
	replyBody := awssdk.ToString(ses.sent[0].Content.Simple.Body.Text.Data)
	if !strings.Contains(replyBody, "KM-AUTH") {
		t.Errorf("rejection should mention KM-AUTH, got: %s", replyBody)
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

// Test: create dispatch — multipart MIME with YAML attachment.
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

// Test: create dispatch — plain text body with YAML inline.
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

// Test: missing KM-AUTH sends rejection reply.
func TestHandleEmailCreate_MissingKMAuth(t *testing.T) {
	rawEmail := buildPlainEmail("operator@corp.com", testYAML)

	eb := &mockEB{}
	ses := &mockSES{}
	s3data := map[string][]byte{"mail/msg003": rawEmail}
	h := newTestHandler(s3data, testSafePhrase, eb, ses)
	h.VerboseErrors = true // test expects rejection reply

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

// Test: wrong KM-AUTH sends rejection reply.
func TestHandleEmailCreate_WrongKMAuth(t *testing.T) {
	plainBody := fmt.Sprintf("KM-AUTH: wrongphrase\n%s", testYAML)
	rawEmail := buildPlainEmail("operator@corp.com", plainBody)

	eb := &mockEB{}
	ses := &mockSES{}
	s3data := map[string][]byte{"mail/msg004": rawEmail}
	h := newTestHandler(s3data, testSafePhrase, eb, ses)
	h.VerboseErrors = true // test expects rejection reply

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

// Test: correct KM-AUTH + valid YAML dispatches EventBridge event.
func TestHandleEmailCreate_CorrectKMAuth_ValidYAML(t *testing.T) {
	plainBody := fmt.Sprintf("KM-AUTH: %s\n%s", testSafePhrase, testYAML)
	rawEmail := buildPlainEmail("operator@corp.com", plainBody)

	eb := &mockEB{}
	ses := &mockSES{}
	s3data := map[string][]byte{"mail/msg005": rawEmail}
	h := newTestHandler(s3data, testSafePhrase, eb, ses)

	event := buildEventRecord("test-bucket", "mail/msg005")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	if len(eb.events) != 1 {
		t.Fatalf("expected 1 EventBridge event, got %d", len(eb.events))
	}
	detail := awssdk.ToString(eb.events[0].Entries[0].Detail)
	if detail == "" {
		t.Error("expected non-empty event detail")
	}
	if len(ses.sent) != 1 {
		t.Errorf("expected 1 acknowledgment email, got %d", len(ses.sent))
	}
}

// Test: invalid YAML sends rejection reply, no EventBridge event.
func TestHandleEmailCreate_InvalidYAML(t *testing.T) {
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

// Test: subject with "status" + sandbox ID replies with metadata.
func TestHandleStatus_ReturnsMetadata(t *testing.T) {
	body := fmt.Sprintf("KM-AUTH: %s\n", testSafePhrase)
	rawEmail := buildPlainEmailWithSubject("operator@corp.com", "status sb-abc12345", body)

	eb := &mockEB{}
	ses := &mockSES{}
	s3data := map[string][]byte{
		"mail/msg010": rawEmail,
	}
	h := newTestHandlerWithAI(&mockS3{objects: s3data}, testSafePhrase, eb, ses, nil)
	// Seed DynamoDB with sandbox metadata record
	h.DynamoClient = &mockDynamo{
		items: map[string]map[string]dynamodbtypes.AttributeValue{
			"sb-abc12345": {
				"sandbox_id":   &dynamodbtypes.AttributeValueMemberS{Value: "sb-abc12345"},
				"profile_name": &dynamodbtypes.AttributeValueMemberS{Value: "open-dev"},
				"substrate":    &dynamodbtypes.AttributeValueMemberS{Value: "ec2"},
				"region":       &dynamodbtypes.AttributeValueMemberS{Value: "us-east-1"},
				"created_at":   &dynamodbtypes.AttributeValueMemberS{Value: "2026-03-25T10:00:00Z"},
				"ttl_expiry":   &dynamodbtypes.AttributeValueMemberS{Value: "2026-03-27T10:00:00Z"},
			},
		},
	}

	event := buildEventRecord("test-bucket", "mail/msg010")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	if len(eb.events) != 0 {
		t.Errorf("status should not publish EventBridge events, got %d", len(eb.events))
	}
	if len(ses.sent) != 1 {
		t.Fatalf("expected 1 status reply, got %d", len(ses.sent))
	}

	replyBody := awssdk.ToString(ses.sent[0].Content.Simple.Body.Text.Data)
	if !strings.Contains(replyBody, "sb-abc12345") {
		t.Errorf("reply should contain sandbox ID, got: %s", replyBody)
	}
	if !strings.Contains(replyBody, "open-dev") {
		t.Errorf("reply should contain profile name, got: %s", replyBody)
	}
	if !strings.Contains(replyBody, "ec2") {
		t.Errorf("reply should contain substrate, got: %s", replyBody)
	}
}

// Test: status with sandbox not found replies gracefully.
func TestHandleStatus_NotFound(t *testing.T) {
	body := fmt.Sprintf("KM-AUTH: %s\n", testSafePhrase)
	rawEmail := buildPlainEmailWithSubject("operator@corp.com", "status sb-00000000", body)

	eb := &mockEB{}
	ses := &mockSES{}
	s3data := map[string][]byte{"mail/msg011": rawEmail}
	h := newTestHandlerWithAI(&mockS3{objects: s3data}, testSafePhrase, eb, ses, nil)

	event := buildEventRecord("test-bucket", "mail/msg011")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	if len(ses.sent) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(ses.sent))
	}
	replyBody := awssdk.ToString(ses.sent[0].Content.Simple.Body.Text.Data)
	if !strings.Contains(replyBody, "not found") {
		t.Errorf("reply should say not found, got: %s", replyBody)
	}
}

// Test: unrecognized subject sends help reply.
func TestHandleUnrecognizedSubject_SendsHelp(t *testing.T) {
	body := fmt.Sprintf("KM-AUTH: %s\n", testSafePhrase)
	rawEmail := buildPlainEmailWithSubject("operator@corp.com", "hello world", body)

	eb := &mockEB{}
	ses := &mockSES{}
	s3data := map[string][]byte{"mail/msg012": rawEmail}
	h := newTestHandler(s3data, testSafePhrase, eb, ses)

	event := buildEventRecord("test-bucket", "mail/msg012")
	if err := h.Handle(context.Background(), event); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	if len(ses.sent) != 1 {
		t.Fatalf("expected 1 help reply, got %d", len(ses.sent))
	}
	subject := awssdk.ToString(ses.sent[0].Content.Simple.Subject.Data)
	if subject != "Operator Help" {
		t.Errorf("expected help subject, got: %s", subject)
	}
	replyBody := awssdk.ToString(ses.sent[0].Content.Simple.Body.Text.Data)
	if !strings.Contains(replyBody, "create") || !strings.Contains(replyBody, "status") {
		t.Errorf("help should list commands, got: %s", replyBody)
	}
}

// Test: extractSandboxID finds IDs in various formats and returns the full prefix-hex ID.
func TestExtractSandboxID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Standard sb- prefix — must return full id including prefix
		{"status sb-abc12345", "sb-abc12345"},
		// Custom prefix — must return full id with custom prefix
		{"status claude-abc12345", "claude-abc12345"},
		{"status build-abc12345", "build-abc12345"},
		// Single-char prefix
		{"status a-abc12345", "a-abc12345"},
		// No sandbox ID in subject
		{"some text without an id", ""},
		{"status", ""},
		{"status no-id-here", ""},
	}
	for _, tt := range tests {
		got := extractSandboxID(tt.input)
		if got != tt.want {
			t.Errorf("extractSandboxID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// Test: no sb- prefix repair — extractSandboxID should return "claude-abc12345" not "sb-abc12345".
func TestExtractSandboxID_NoPrefixRepair(t *testing.T) {
	// If the old prefix-repair logic (sb- prepend) were still present,
	// this would return "sb-abc12345" for "claude-abc12345" input.
	got := extractSandboxID("status claude-abc12345")
	if got == "sb-"+got[3:] || got == "sb-abc12345" {
		t.Errorf("extractSandboxID applied invalid sb- prefix repair: got %q", got)
	}
	if got != "claude-abc12345" {
		t.Errorf("extractSandboxID(%q) = %q, want %q", "status claude-abc12345", got, "claude-abc12345")
	}
}

// ---- sender allowlist tests ----

// Test: sender NOT in operator allowlist → silently dropped.
func TestHandle_SenderNotAllowed(t *testing.T) {
	kmConfig := `email:
  allowedSenders:
    - "admin@co.com"
`
	emailBody := fmt.Sprintf("KM-AUTH: %s\nHello\n", testSafePhrase)
	rawEmail := buildPlainEmailWithSubject("rando@evil.com", "status sb-abc12345", emailBody)

	s3data := map[string][]byte{
		"mail/msg-notallowed": rawEmail,
		"toolchain/km-config.yaml": []byte(kmConfig),
	}
	eb := &mockEB{}
	ses := &mockSES{}
	h := newTestHandler(s3data, testSafePhrase, eb, ses)

	event := buildEventRecord("test-bucket", "mail/msg-notallowed")
	err := h.Handle(context.Background(), event)
	if err != nil {
		t.Fatalf("Handle should return nil for non-allowed sender, got: %v", err)
	}
	// Should NOT send any reply (silent drop)
	if len(ses.sent) != 0 {
		t.Errorf("expected no SES sends for non-allowed sender, got %d", len(ses.sent))
	}
}

// Test: sender matches wildcard in operator allowlist → proceeds.
func TestHandle_SenderAllowed(t *testing.T) {
	kmConfig := `email:
  allowedSenders:
    - "*@company.com"
`
	emailBody := fmt.Sprintf("KM-AUTH: %s\nstatus sb-abc12345\n", testSafePhrase)
	rawEmail := buildPlainEmailWithSubject("user@company.com", "status sb-abc12345", emailBody)

	s3data := map[string][]byte{
		"mail/msg-allowed": rawEmail,
		"toolchain/km-config.yaml": []byte(kmConfig),
	}
	eb := &mockEB{}
	ses := &mockSES{}
	h := newTestHandler(s3data, testSafePhrase, eb, ses)
	h.DynamoClient = &mockDynamo{}
	h.SandboxTableName = "km-sandboxes"

	event := buildEventRecord("test-bucket", "mail/msg-allowed")
	err := h.Handle(context.Background(), event)
	if err != nil {
		t.Fatalf("Handle error for allowed sender: %v", err)
	}
	// Should proceed past allowlist and reach safe phrase check + dispatch.
	// With valid safe phrase and "status" subject, should send a status reply.
	if len(ses.sent) == 0 {
		t.Errorf("expected SES reply for allowed sender, got none")
	}
}

// Test: empty allowlist → all senders proceed (backward compatible).
func TestHandle_EmptyAllowlist(t *testing.T) {
	kmConfig := `domain: example.com
`
	emailBody := fmt.Sprintf("KM-AUTH: %s\nHello\n", testSafePhrase)
	rawEmail := buildPlainEmailWithSubject("anyone@anywhere.com", "status sb-abc12345", emailBody)

	s3data := map[string][]byte{
		"mail/msg-emptyallow": rawEmail,
		"toolchain/km-config.yaml": []byte(kmConfig),
	}
	eb := &mockEB{}
	ses := &mockSES{}
	h := newTestHandler(s3data, testSafePhrase, eb, ses)
	h.DynamoClient = &mockDynamo{}
	h.SandboxTableName = "km-sandboxes"

	event := buildEventRecord("test-bucket", "mail/msg-emptyallow")
	err := h.Handle(context.Background(), event)
	if err != nil {
		t.Fatalf("Handle error with empty allowlist: %v", err)
	}
	// Should proceed — no filtering when allowlist is empty
	if len(ses.sent) == 0 {
		t.Errorf("expected SES reply with empty allowlist, got none")
	}
}

// Test: S3 error fetching km-config.yaml → fail-open (proceed normally).
func TestHandle_AllowlistS3Error(t *testing.T) {
	emailBody := fmt.Sprintf("KM-AUTH: %s\nHello\n", testSafePhrase)
	rawEmail := buildPlainEmailWithSubject("anyone@anywhere.com", "status sb-abc12345", emailBody)

	// No km-config.yaml in S3 → GetObject will return NoSuchKey
	s3data := map[string][]byte{
		"mail/msg-s3error": rawEmail,
	}
	eb := &mockEB{}
	ses := &mockSES{}
	h := newTestHandler(s3data, testSafePhrase, eb, ses)
	h.DynamoClient = &mockDynamo{}
	h.SandboxTableName = "km-sandboxes"

	event := buildEventRecord("test-bucket", "mail/msg-s3error")
	err := h.Handle(context.Background(), event)
	if err != nil {
		t.Fatalf("Handle error with missing km-config.yaml: %v", err)
	}
	// Should proceed — fail-open when km-config.yaml is missing
	if len(ses.sent) == 0 {
		t.Errorf("expected SES reply when km-config.yaml missing (fail-open), got none")
	}
}
