package lifecycle

import (
	"context"
	"errors"
	"testing"
)

// callRecorder tracks the order of function calls.
type callRecorder struct {
	calls []string
}

func (r *callRecorder) record(name string) func(ctx context.Context, sandboxID string) error {
	return func(ctx context.Context, sandboxID string) error {
		r.calls = append(r.calls, name)
		return nil
	}
}

func (r *callRecorder) recordError(name string, err error) func(ctx context.Context, sandboxID string) error {
	return func(ctx context.Context, sandboxID string) error {
		r.calls = append(r.calls, name)
		return err
	}
}

// TestDestroyPolicyCallsDestroy verifies the destroy policy calls Destroy.
func TestDestroyPolicyCallsDestroy(t *testing.T) {
	rec := &callRecorder{}
	cbs := TeardownCallbacks{
		Destroy:         rec.record("destroy"),
		Stop:            rec.record("stop"),
		UploadArtifacts: nil,
	}
	if err := ExecuteTeardown(context.Background(), "destroy", "sb-001", cbs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rec.calls) != 1 || rec.calls[0] != "destroy" {
		t.Errorf("expected [destroy], got %v", rec.calls)
	}
}

// TestStopPolicyCallsStop verifies the stop policy calls Stop.
func TestStopPolicyCallsStop(t *testing.T) {
	rec := &callRecorder{}
	cbs := TeardownCallbacks{
		Destroy:         rec.record("destroy"),
		Stop:            rec.record("stop"),
		UploadArtifacts: nil,
	}
	if err := ExecuteTeardown(context.Background(), "stop", "sb-001", cbs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rec.calls) != 1 || rec.calls[0] != "stop" {
		t.Errorf("expected [stop], got %v", rec.calls)
	}
}

// TestRetainPolicyNoDestroyOrStop verifies retain policy does NOT call Destroy or Stop.
func TestRetainPolicyNoDestroyOrStop(t *testing.T) {
	rec := &callRecorder{}
	cbs := TeardownCallbacks{
		Destroy:         rec.record("destroy"),
		Stop:            rec.record("stop"),
		UploadArtifacts: nil,
	}
	if err := ExecuteTeardown(context.Background(), "retain", "sb-001", cbs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, call := range rec.calls {
		if call == "destroy" || call == "stop" {
			t.Errorf("retain policy must not call destroy or stop, got calls: %v", rec.calls)
		}
	}
}

// TestUnknownPolicyReturnsError verifies unknown policy returns an error.
func TestUnknownPolicyReturnsError(t *testing.T) {
	cbs := TeardownCallbacks{
		Destroy:         func(ctx context.Context, sandboxID string) error { return nil },
		UploadArtifacts: nil,
	}
	err := ExecuteTeardown(context.Background(), "nuke", "sb-001", cbs)
	if err == nil {
		t.Fatal("expected error for unknown policy")
	}
}

// TestUploadArtifactsCalledBeforeDestroyOnDestroyPolicy verifies call ordering.
func TestUploadArtifactsCalledBeforeDestroyOnDestroyPolicy(t *testing.T) {
	rec := &callRecorder{}
	cbs := TeardownCallbacks{
		Destroy:         rec.record("destroy"),
		Stop:            rec.record("stop"),
		UploadArtifacts: rec.record("upload"),
	}
	if err := ExecuteTeardown(context.Background(), "destroy", "sb-001", cbs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rec.calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %v", rec.calls)
	}
	if rec.calls[0] != "upload" {
		t.Errorf("expected upload to be called first, got %v", rec.calls)
	}
	if rec.calls[1] != "destroy" {
		t.Errorf("expected destroy to be called second, got %v", rec.calls)
	}
}

// TestUploadArtifactsCalledBeforeStopOnStopPolicy verifies call ordering for stop.
func TestUploadArtifactsCalledBeforeStopOnStopPolicy(t *testing.T) {
	rec := &callRecorder{}
	cbs := TeardownCallbacks{
		Destroy:         rec.record("destroy"),
		Stop:            rec.record("stop"),
		UploadArtifacts: rec.record("upload"),
	}
	if err := ExecuteTeardown(context.Background(), "stop", "sb-001", cbs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rec.calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %v", rec.calls)
	}
	if rec.calls[0] != "upload" {
		t.Errorf("expected upload to be called first, got %v", rec.calls)
	}
	if rec.calls[1] != "stop" {
		t.Errorf("expected stop to be called second, got %v", rec.calls)
	}
}

// TestUploadArtifactsCalledOnRetainPolicy verifies upload is called for retain policy too.
func TestUploadArtifactsCalledOnRetainPolicy(t *testing.T) {
	rec := &callRecorder{}
	cbs := TeardownCallbacks{
		Destroy:         rec.record("destroy"),
		Stop:            rec.record("stop"),
		UploadArtifacts: rec.record("upload"),
	}
	if err := ExecuteTeardown(context.Background(), "retain", "sb-001", cbs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	hasUpload := false
	for _, call := range rec.calls {
		if call == "upload" {
			hasUpload = true
		}
	}
	if !hasUpload {
		t.Error("expected UploadArtifacts to be called even for retain policy")
	}
	// retain should NOT call destroy or stop
	for _, call := range rec.calls {
		if call == "destroy" || call == "stop" {
			t.Errorf("retain policy must not call destroy or stop, got calls: %v", rec.calls)
		}
	}
}

// TestUploadArtifactsNilSkipped verifies nil UploadArtifacts is skipped, not panicked.
func TestUploadArtifactsNilSkipped(t *testing.T) {
	rec := &callRecorder{}
	cbs := TeardownCallbacks{
		Destroy:         rec.record("destroy"),
		UploadArtifacts: nil, // nil = skip
	}
	if err := ExecuteTeardown(context.Background(), "destroy", "sb-001", cbs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rec.calls) != 1 || rec.calls[0] != "destroy" {
		t.Errorf("expected [destroy], got %v", rec.calls)
	}
}

// TestUploadArtifactsErrorDoesNotBlockTeardown verifies best-effort: upload error is logged not propagated.
func TestUploadArtifactsErrorDoesNotBlockTeardown(t *testing.T) {
	uploadErr := errors.New("S3 upload failed")
	rec := &callRecorder{}
	cbs := TeardownCallbacks{
		Destroy:         rec.record("destroy"),
		UploadArtifacts: rec.recordError("upload", uploadErr),
	}
	// Should NOT return error even though upload failed
	if err := ExecuteTeardown(context.Background(), "destroy", "sb-001", cbs); err != nil {
		t.Fatalf("upload error should not propagate, but got: %v", err)
	}
	// Destroy should still have been called
	hasDestroy := false
	for _, call := range rec.calls {
		if call == "destroy" {
			hasDestroy = true
		}
	}
	if !hasDestroy {
		t.Error("expected Destroy to still be called after upload failure")
	}
}

// --------------------------------------------------------------------------
// OnNotify tests
// --------------------------------------------------------------------------

// TestOnNotifyCalledAfterSuccessfulDestroy verifies OnNotify is called with "destroyed"
// event on successful destroy policy execution.
func TestOnNotifyCalledAfterSuccessfulDestroy(t *testing.T) {
	var notifyEvent string
	cbs := TeardownCallbacks{
		Destroy: func(ctx context.Context, sandboxID string) error { return nil },
		OnNotify: func(ctx context.Context, sandboxID string, event string) error {
			notifyEvent = event
			return nil
		},
	}
	if err := ExecuteTeardown(context.Background(), "destroy", "sb-001", cbs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notifyEvent != "destroyed" {
		t.Errorf("expected OnNotify event %q, got %q", "destroyed", notifyEvent)
	}
}

// TestOnNotifyCalledWithErrorOnDestroyFailure verifies OnNotify is called with "error"
// event when the Destroy callback returns an error.
func TestOnNotifyCalledWithErrorOnDestroyFailure(t *testing.T) {
	destroyErr := errors.New("terragrunt destroy failed")
	var notifyEvent string
	cbs := TeardownCallbacks{
		Destroy: func(ctx context.Context, sandboxID string) error { return destroyErr },
		OnNotify: func(ctx context.Context, sandboxID string, event string) error {
			notifyEvent = event
			return nil
		},
	}
	err := ExecuteTeardown(context.Background(), "destroy", "sb-001", cbs)
	if err == nil {
		t.Fatal("expected destroy error to be propagated")
	}
	if notifyEvent != "error" {
		t.Errorf("expected OnNotify event %q on destroy failure, got %q", "error", notifyEvent)
	}
}

// TestOnNotifyNilIsBackwardCompatible verifies that nil OnNotify does not panic.
func TestOnNotifyNilIsBackwardCompatible(t *testing.T) {
	cbs := TeardownCallbacks{
		Destroy:  func(ctx context.Context, sandboxID string) error { return nil },
		OnNotify: nil,
	}
	if err := ExecuteTeardown(context.Background(), "destroy", "sb-001", cbs); err != nil {
		t.Fatalf("nil OnNotify should not cause error: %v", err)
	}
}

// TestOnNotifyFailureDoesNotAffectTeardown verifies that OnNotify errors are swallowed.
func TestOnNotifyFailureDoesNotAffectTeardown(t *testing.T) {
	notifyErr := errors.New("SES unreachable")
	cbs := TeardownCallbacks{
		Destroy: func(ctx context.Context, sandboxID string) error { return nil },
		OnNotify: func(ctx context.Context, sandboxID string, event string) error {
			return notifyErr
		},
	}
	// Teardown itself should succeed even if OnNotify fails.
	if err := ExecuteTeardown(context.Background(), "destroy", "sb-001", cbs); err != nil {
		t.Fatalf("OnNotify failure should not propagate teardown error: %v", err)
	}
}
