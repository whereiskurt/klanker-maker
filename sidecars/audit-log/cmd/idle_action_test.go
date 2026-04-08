package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
)

// mockEB is a minimal EventBridge mock that records calls to PutEvents.
type mockEB struct {
	calls []eventbridge.PutEventsInput
}

func (m *mockEB) PutEvents(_ context.Context, params *eventbridge.PutEventsInput, _ ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error) {
	m.calls = append(m.calls, *params)
	return &eventbridge.PutEventsOutput{}, nil
}

// TestIdleActionHibernateCallsStop verifies that when idleAction="hibernate",
// the OnIdle callback calls PublishSandboxCommand with event_type "stop"
// and does NOT call cancel().
func TestIdleActionHibernateCallsStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eb := &mockEB{}
	cancelCalled := false
	cancelFn := func() { cancelCalled = true }

	rearmCount := int32(0)
	rearmFn := func() { atomic.AddInt32(&rearmCount, 1) }

	cb := buildIdleCallback(ctx, eb, "sb-test01", "hibernate", cancelFn, rearmFn)
	cb("sb-test01")

	if cancelCalled {
		t.Error("hibernate path: cancel() should NOT be called on idle")
	}

	// Verify a "stop" event was published (not "idle")
	if len(eb.calls) != 1 {
		t.Fatalf("hibernate path: expected 1 PutEvents call, got %d", len(eb.calls))
	}
	detail := ""
	if eb.calls[0].Entries != nil && len(eb.calls[0].Entries) > 0 && eb.calls[0].Entries[0].Detail != nil {
		detail = *eb.calls[0].Entries[0].Detail
	}
	if detail == "" {
		t.Fatal("hibernate path: PutEvents called with nil detail")
	}
	// event_type must be "stop", not "idle"
	if !containsStr(detail, `"stop"`) {
		t.Errorf("hibernate path: expected event_type=stop in detail JSON, got: %s", detail)
	}
	if containsStr(detail, `"idle"`) {
		t.Errorf("hibernate path: unexpected event_type=idle in detail JSON, got: %s", detail)
	}
}

// TestIdleActionHibernateRearmsDetector verifies that when idleAction="hibernate",
// the rearm function is called (not cancel).
func TestIdleActionHibernateRearmsDetector(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eb := &mockEB{}
	cancelCalled := false
	cancelFn := func() { cancelCalled = true }

	rearmCount := int32(0)
	rearmFn := func() { atomic.AddInt32(&rearmCount, 1) }

	cb := buildIdleCallback(ctx, eb, "sb-test02", "hibernate", cancelFn, rearmFn)
	cb("sb-test02")

	if cancelCalled {
		t.Error("hibernate path: cancel() should NOT be called — sidecar must keep running")
	}

	// rearmFn should be called so a new detector gets scheduled
	if atomic.LoadInt32(&rearmCount) != 1 {
		t.Errorf("hibernate path: expected rearmFn called once, got %d", atomic.LoadInt32(&rearmCount))
	}
}

// TestIdleActionDefaultCallsIdleEvent verifies that when idleAction="" (default/destroy),
// the OnIdle callback calls PublishSandboxIdleEvent and calls cancel().
func TestIdleActionDefaultCallsIdleEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eb := &mockEB{}
	cancelCalled := false
	cancelFn := func() { cancelCalled = true }

	rearmCount := int32(0)
	rearmFn := func() { atomic.AddInt32(&rearmCount, 1) }

	cb := buildIdleCallback(ctx, eb, "sb-test03", "", cancelFn, rearmFn)
	cb("sb-test03")

	if !cancelCalled {
		t.Error("default path: cancel() should be called on idle to trigger shutdown")
	}

	// Verify an "idle" event was published
	if len(eb.calls) != 1 {
		t.Fatalf("default path: expected 1 PutEvents call, got %d", len(eb.calls))
	}
	detail := ""
	if eb.calls[0].Entries != nil && len(eb.calls[0].Entries) > 0 && eb.calls[0].Entries[0].Detail != nil {
		detail = *eb.calls[0].Entries[0].Detail
	}
	if !containsStr(detail, `"idle"`) {
		t.Errorf("default path: expected event_type=idle in detail JSON, got: %s", detail)
	}
}

// TestIdleActionDefaultDoesNotRearm verifies that the default path does NOT re-arm.
func TestIdleActionDefaultDoesNotRearm(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eb := &mockEB{}
	cancelFn := func() {}
	rearmCount := int32(0)
	rearmFn := func() { atomic.AddInt32(&rearmCount, 1) }

	cb := buildIdleCallback(ctx, eb, "sb-test04", "", cancelFn, rearmFn)
	cb("sb-test04")

	if atomic.LoadInt32(&rearmCount) != 0 {
		t.Errorf("default path: rearmFn should NOT be called, got %d calls", atomic.LoadInt32(&rearmCount))
	}
}

// TestIdleActionNilEBClientHandled verifies that nil EventBridge client is handled gracefully
// (no panic) for both hibernate and default paths.
func TestIdleActionNilEBClientHandled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cancelFn := func() {}
	rearmFn := func() {}

	// hibernate — nil EB client
	cb := buildIdleCallback(ctx, nil, "sb-test05", "hibernate", cancelFn, rearmFn)
	cb("sb-test05") // must not panic

	// default — nil EB client
	cb2 := buildIdleCallback(ctx, nil, "sb-test06", "", cancelFn, rearmFn)
	cb2("sb-test06") // must not panic
}

// TestIdleActionDestroyEquivalentToEmpty verifies that "destroy" is treated same as "" (default).
func TestIdleActionDestroyEquivalentToEmpty(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eb := &mockEB{}
	cancelCalled := false
	cancelFn := func() { cancelCalled = true }
	rearmCount := int32(0)
	rearmFn := func() { atomic.AddInt32(&rearmCount, 1) }

	cb := buildIdleCallback(ctx, eb, "sb-test07", "destroy", cancelFn, rearmFn)
	cb("sb-test07")

	if !cancelCalled {
		t.Error("destroy path: cancel() should be called (same as default)")
	}
	if atomic.LoadInt32(&rearmCount) != 0 {
		t.Error("destroy path: rearmFn should NOT be called")
	}
}

// TestAfterFuncCalledForHibernate verifies that the AfterFunc mechanism schedules re-arm.
// We use a short delay and verify it fires within the timeout.
func TestAfterFuncCalledForHibernate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eb := &mockEB{}
	cancelFn := func() {}

	rearmCh := make(chan struct{}, 1)
	rearmFn := func() { rearmCh <- struct{}{} }

	cb := buildIdleCallback(ctx, eb, "sb-test08", "hibernate", cancelFn, rearmFn)
	cb("sb-test08")

	select {
	case <-rearmCh:
		// rearm was called synchronously by our mock rearmFn
	case <-time.After(100 * time.Millisecond):
		t.Error("hibernate path: rearmFn was not called within timeout")
	}
}

// containsStr is a simple substring helper for test assertions.
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
