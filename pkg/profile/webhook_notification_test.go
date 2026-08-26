package profile

// webhook_notification_test.go — Phase 127 tests
//
// Tests for:
//   - NotificationWebhookSpec / NotificationWebhookInboundSpec types (tri-state *bool)
//   - notification.webhook.inbound.enabled round-trips through Parse()
//   - notification.webhook absent ⇒ nil (dormant)
//   - mergeNotificationSpec (generic deepMerge) handles the new Webhook field

import (
	"testing"
)

// ============================================================
// Type / parse tests
// ============================================================

// TestNotificationWebhookInbound_RoundTrip verifies that a profile YAML with
// notification.webhook.inbound.enabled: true round-trips through Parse().
func TestNotificationWebhookInbound_RoundTrip(t *testing.T) {
	raw := []byte("apiVersion: klankermaker.ai/v1alpha2\nkind: SandboxProfile\n" +
		"metadata:\n  name: t\nspec:\n  notification:\n    webhook:\n      inbound:\n        enabled: true\n")
	p, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Spec.Notification == nil || p.Spec.Notification.Webhook == nil ||
		p.Spec.Notification.Webhook.Inbound == nil {
		t.Fatal("notification.webhook.inbound missing after parse")
	}
	if e := p.Spec.Notification.Webhook.Inbound.Enabled; e == nil || !*e {
		t.Errorf("Enabled: got %v, want true", e)
	}
}

// TestNotificationWebhookInbound_AbsentIsNil verifies that when
// notification.webhook is absent, the field is nil (dormant invariant — zero
// artifacts).
func TestNotificationWebhookInbound_AbsentIsNil(t *testing.T) {
	raw := []byte("apiVersion: klankermaker.ai/v1alpha2\nkind: SandboxProfile\n" +
		"metadata:\n  name: t\nspec:\n  notification: {}\n")
	p, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Spec.Notification != nil && p.Spec.Notification.Webhook != nil {
		t.Error("absent webhook block must stay nil (dormant)")
	}
}

// ============================================================
// Merge tests
// ============================================================

// TestMergeNotificationWebhook_ChildOverridesParent verifies that a child
// profile's webhook.inbound.enabled=true overrides a nil parent field.
func TestMergeNotificationWebhook_ChildOverridesParent(t *testing.T) {
	boolTrue := true
	parent := &NotificationSpec{Webhook: nil}
	child := &NotificationSpec{
		Webhook: &NotificationWebhookSpec{
			Inbound: &NotificationWebhookInboundSpec{Enabled: &boolTrue},
		},
	}
	result := mergeNotificationSpec(parent, child)
	if result.Webhook == nil {
		t.Fatal("merge: Webhook nil after child override")
	}
	if result.Webhook.Inbound == nil {
		t.Fatal("merge: Webhook.Inbound nil after child override")
	}
	if result.Webhook.Inbound.Enabled == nil || !*result.Webhook.Inbound.Enabled {
		t.Error("merge: Webhook.Inbound.Enabled expected true")
	}
}

// TestMergeNotificationWebhook_ParentInheritsWhenChildNil verifies that when
// the child has no webhook block, the parent's webhook block is inherited.
func TestMergeNotificationWebhook_ParentInheritsWhenChildNil(t *testing.T) {
	boolTrue := true
	parent := &NotificationSpec{
		Webhook: &NotificationWebhookSpec{
			Inbound: &NotificationWebhookInboundSpec{Enabled: &boolTrue},
		},
	}
	child := &NotificationSpec{Webhook: nil}
	result := mergeNotificationSpec(parent, child)
	if result.Webhook == nil {
		t.Fatal("merge: Webhook nil — parent not inherited")
	}
	if result.Webhook.Inbound == nil || result.Webhook.Inbound.Enabled == nil || !*result.Webhook.Inbound.Enabled {
		t.Error("merge: Webhook.Inbound.Enabled expected true from parent")
	}
}
