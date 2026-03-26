package aws_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// mockSESV2API implements SESV2API for unit tests.
// Each method captures its input and returns the configured error.
type mockSESV2API struct {
	createIdentityCalled bool
	createIdentityInput  *sesv2.CreateEmailIdentityInput
	createIdentityErr    error

	deleteIdentityCalled bool
	deleteIdentityInput  *sesv2.DeleteEmailIdentityInput
	deleteIdentityErr    error

	sendEmailCalled bool
	sendEmailInput  *sesv2.SendEmailInput
	sendEmailErr    error
}

func (m *mockSESV2API) CreateEmailIdentity(ctx context.Context, input *sesv2.CreateEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.CreateEmailIdentityOutput, error) {
	m.createIdentityCalled = true
	m.createIdentityInput = input
	return &sesv2.CreateEmailIdentityOutput{}, m.createIdentityErr
}

func (m *mockSESV2API) DeleteEmailIdentity(ctx context.Context, input *sesv2.DeleteEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.DeleteEmailIdentityOutput, error) {
	m.deleteIdentityCalled = true
	m.deleteIdentityInput = input
	return &sesv2.DeleteEmailIdentityOutput{}, m.deleteIdentityErr
}

func (m *mockSESV2API) SendEmail(ctx context.Context, input *sesv2.SendEmailInput, optFns ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	m.sendEmailCalled = true
	m.sendEmailInput = input
	return &sesv2.SendEmailOutput{}, m.sendEmailErr
}

// ============================================================
// ProvisionSandboxEmail tests
// ============================================================

func TestSES_ProvisionSandboxEmail_ReturnsAddress(t *testing.T) {
	mock := &mockSESV2API{}
	addr, err := kmaws.ProvisionSandboxEmail(context.Background(), mock, "sb-abc123", "sandboxes.klankermaker.ai")
	if err != nil {
		t.Fatalf("ProvisionSandboxEmail returned unexpected error: %v", err)
	}
	wantAddr := "sb-abc123@sandboxes.klankermaker.ai"
	if addr != wantAddr {
		t.Errorf("returned address = %q; want %q", addr, wantAddr)
	}
	// Should NOT call CreateEmailIdentity — domain identity covers all addresses.
	if mock.createIdentityCalled {
		t.Error("ProvisionSandboxEmail should not call CreateEmailIdentity; domain identity is sufficient")
	}
}

// ============================================================
// SendLifecycleNotification tests
// ============================================================

func TestSES_SendLifecycleNotification_SubjectContainsSandboxIDAndEvent(t *testing.T) {
	mock := &mockSESV2API{}
	err := kmaws.SendLifecycleNotification(context.Background(), mock, "admin@company.com", "sb-abc123", "destroyed", "sandboxes.klankermaker.ai")
	if err != nil {
		t.Fatalf("SendLifecycleNotification returned unexpected error: %v", err)
	}
	if !mock.sendEmailCalled {
		t.Fatal("expected SendEmail to be called")
	}
	if mock.sendEmailInput == nil {
		t.Fatal("SendEmail called with nil input")
	}

	subject := *mock.sendEmailInput.Content.Simple.Subject.Data
	if !strings.Contains(subject, "sb-abc123") {
		t.Errorf("subject %q does not contain sandbox ID 'sb-abc123'", subject)
	}
	if !strings.Contains(subject, "destroyed") {
		t.Errorf("subject %q does not contain event 'destroyed'", subject)
	}
}

func TestSES_SendLifecycleNotification_FromAddressIsNotificationsAt(t *testing.T) {
	mock := &mockSESV2API{}
	err := kmaws.SendLifecycleNotification(context.Background(), mock, "admin@company.com", "sb-abc123", "idle-timeout", "sandboxes.klankermaker.ai")
	if err != nil {
		t.Fatalf("SendLifecycleNotification returned unexpected error: %v", err)
	}

	wantFrom := "notifications@sandboxes.klankermaker.ai"
	if mock.sendEmailInput.FromEmailAddress == nil || *mock.sendEmailInput.FromEmailAddress != wantFrom {
		t.Errorf("FromEmailAddress = %v; want %q", mock.sendEmailInput.FromEmailAddress, wantFrom)
	}
}

func TestSES_SendLifecycleNotification_Error(t *testing.T) {
	sdkErr := errors.New("sdk error: MessageRejected")
	mock := &mockSESV2API{sendEmailErr: sdkErr}

	err := kmaws.SendLifecycleNotification(context.Background(), mock, "admin@company.com", "sb-abc123", "error", "sandboxes.klankermaker.ai")
	if err == nil {
		t.Fatal("expected error from SendLifecycleNotification when SendEmail fails")
	}
	if !strings.Contains(err.Error(), "MessageRejected") {
		t.Errorf("expected error to contain 'MessageRejected', got: %v", err)
	}
}

// ============================================================
// CleanupSandboxEmail tests
// ============================================================

func TestSES_CleanupSandboxEmail_Success(t *testing.T) {
	mock := &mockSESV2API{}
	err := kmaws.CleanupSandboxEmail(context.Background(), mock, "sb-abc123", "sandboxes.klankermaker.ai")
	if err != nil {
		t.Fatalf("CleanupSandboxEmail returned unexpected error: %v", err)
	}
	if !mock.deleteIdentityCalled {
		t.Fatal("expected DeleteEmailIdentity to be called")
	}
	if mock.deleteIdentityInput == nil || mock.deleteIdentityInput.EmailIdentity == nil {
		t.Fatal("DeleteEmailIdentity called with nil input or nil EmailIdentity")
	}
	wantAddr := "sb-abc123@sandboxes.klankermaker.ai"
	if *mock.deleteIdentityInput.EmailIdentity != wantAddr {
		t.Errorf("EmailIdentity = %q; want %q", *mock.deleteIdentityInput.EmailIdentity, wantAddr)
	}
}

func TestSES_CleanupSandboxEmail_IdempotentOnNotFound(t *testing.T) {
	// NotFoundException should be treated as idempotent success (km destroy may be retried)
	notFoundErr := &sesv2types.NotFoundException{
		Message: strPtr("Email identity not found"),
	}
	mock := &mockSESV2API{deleteIdentityErr: notFoundErr}

	err := kmaws.CleanupSandboxEmail(context.Background(), mock, "sb-gone", "sandboxes.klankermaker.ai")
	if err != nil {
		t.Fatalf("CleanupSandboxEmail should return nil for NotFoundException (idempotent), got: %v", err)
	}
}

// strPtr is a local helper for string pointers in tests.
// (stringPtr is defined in scheduler_test.go; this avoids redeclaration)
func strPtr(s string) *string { return &s }

// ============================================================
// SendApprovalRequest tests
// ============================================================

func TestSendApprovalRequest_FromAddressIsSandboxMailbox(t *testing.T) {
	mock := &mockSESV2API{}
	err := kmaws.SendApprovalRequest(context.Background(), mock,
		"sb-abc123", "sandboxes.klankermaker.ai", "operator@company.com",
		"deploy-prod", "Deploy production stack v2.3.1")
	if err != nil {
		t.Fatalf("SendApprovalRequest returned unexpected error: %v", err)
	}
	if !mock.sendEmailCalled {
		t.Fatal("expected SendEmail to be called")
	}
	// From must be sandbox's own mailbox so operator reply routes back to sandbox
	wantFrom := "sb-abc123@sandboxes.klankermaker.ai"
	if mock.sendEmailInput.FromEmailAddress == nil || *mock.sendEmailInput.FromEmailAddress != wantFrom {
		t.Errorf("FromEmailAddress = %v; want %q", mock.sendEmailInput.FromEmailAddress, wantFrom)
	}
}

func TestSendApprovalRequest_ToAddressIsOperator(t *testing.T) {
	mock := &mockSESV2API{}
	err := kmaws.SendApprovalRequest(context.Background(), mock,
		"sb-abc123", "sandboxes.klankermaker.ai", "operator@company.com",
		"deploy-prod", "Deploy production stack v2.3.1")
	if err != nil {
		t.Fatalf("SendApprovalRequest returned unexpected error: %v", err)
	}
	if len(mock.sendEmailInput.Destination.ToAddresses) != 1 {
		t.Fatalf("expected 1 ToAddress, got %d", len(mock.sendEmailInput.Destination.ToAddresses))
	}
	if mock.sendEmailInput.Destination.ToAddresses[0] != "operator@company.com" {
		t.Errorf("ToAddresses[0] = %q; want %q", mock.sendEmailInput.Destination.ToAddresses[0], "operator@company.com")
	}
}

func TestSendApprovalRequest_SubjectContainsSandboxIDAndAction(t *testing.T) {
	mock := &mockSESV2API{}
	err := kmaws.SendApprovalRequest(context.Background(), mock,
		"sb-abc123", "sandboxes.klankermaker.ai", "operator@company.com",
		"deploy-prod", "Deploy production stack v2.3.1")
	if err != nil {
		t.Fatalf("SendApprovalRequest returned unexpected error: %v", err)
	}
	subject := *mock.sendEmailInput.Content.Simple.Subject.Data
	wantSubject := "[KM-APPROVAL-REQUEST] sb-abc123 deploy-prod"
	if subject != wantSubject {
		t.Errorf("Subject = %q; want %q", subject, wantSubject)
	}
}

func TestSendApprovalRequest_BodyContainsActionAndInstructions(t *testing.T) {
	mock := &mockSESV2API{}
	err := kmaws.SendApprovalRequest(context.Background(), mock,
		"sb-abc123", "sandboxes.klankermaker.ai", "operator@company.com",
		"deploy-prod", "Deploy production stack v2.3.1")
	if err != nil {
		t.Fatalf("SendApprovalRequest returned unexpected error: %v", err)
	}
	body := *mock.sendEmailInput.Content.Simple.Body.Text.Data
	if !strings.Contains(body, "sb-abc123") {
		t.Errorf("body does not contain sandbox ID 'sb-abc123': %q", body)
	}
	if !strings.Contains(body, "deploy-prod") {
		t.Errorf("body does not contain action 'deploy-prod': %q", body)
	}
	if !strings.Contains(body, "Deploy production stack v2.3.1") {
		t.Errorf("body does not contain description: %q", body)
	}
	if !strings.Contains(body, "APPROVED") {
		t.Errorf("body does not contain 'APPROVED' instruction: %q", body)
	}
	if !strings.Contains(body, "DENIED") {
		t.Errorf("body does not contain 'DENIED' instruction: %q", body)
	}
}

func TestSendApprovalRequest_Error(t *testing.T) {
	sdkErr := errors.New("sdk error: MessageRejected")
	mock := &mockSESV2API{sendEmailErr: sdkErr}

	err := kmaws.SendApprovalRequest(context.Background(), mock,
		"sb-abc123", "sandboxes.klankermaker.ai", "operator@company.com",
		"deploy-prod", "Deploy production stack")
	if err == nil {
		t.Fatal("expected error from SendApprovalRequest when SendEmail fails")
	}
	if !strings.Contains(err.Error(), "MessageRejected") {
		t.Errorf("expected error to contain 'MessageRejected', got: %v", err)
	}
}
