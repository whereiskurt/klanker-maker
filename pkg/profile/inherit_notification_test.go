//go:build phase92_wave2
// +build phase92_wave2

package profile

import (
	"testing"
)

// phase92BoolPtr is a local pointer helper for the Wave 2 RED stub (the repo has
// no internal/util/ptr package).
func phase92BoolPtr(b bool) *bool { return &b }

// TestInheritNotificationSpec_ChildOnlyTranscriptInheritsParentPerSandbox verifies
// the pointer-merge bug fix: a child profile that sets only ONE notification field
// (notification.slack.transcript.enabled: true) must still inherit the parent's
// OTHER notification settings (notification.slack.perSandbox: true).
//
// RED STATE (Wave 0): behind the phase92_wave2 build tag, NOT compiled by the
// default build. References the post-phase API (Spec.Notification,
// NotificationSpec, NotificationSlackSpec, NotificationSlackTranscriptSpec, and the
// typed mergeNotificationSpec wired into resolution) that does not exist until
// Wave 2. Wave 2 removes the build tag after mergeNotificationSpec lands; its
// "task done" criterion is:
//
//	go test ./pkg/profile/ -tags phase92_wave2 -run TestInheritNotificationSpec_ChildOnlyTranscriptInheritsParentPerSandbox  // GREEN
//
// Pre-Phase-92 behavior (the bug): the child's pointer-typed Spec.CLI fully
// replaced the parent's, dropping all parent notify settings. Phase 92 fixes this
// via a typed deep-merge in mergeNotificationSpec.
//
// VC-7
func TestInheritNotificationSpec_ChildOnlyTranscriptInheritsParentPerSandbox(t *testing.T) {
	parent := &SandboxProfile{
		Spec: Spec{
			Notification: &NotificationSpec{
				Slack: &NotificationSlackSpec{
					Enabled:    phase92BoolPtr(true),
					PerSandbox: phase92BoolPtr(true),
				},
			},
		},
	}
	child := &SandboxProfile{
		Spec: Spec{
			Notification: &NotificationSpec{
				Slack: &NotificationSlackSpec{
					Transcript: &NotificationSlackTranscriptSpec{Enabled: phase92BoolPtr(true)},
				},
			},
		},
	}

	merged := mergeNotificationSpec(parent, child)

	if merged.Spec.Notification == nil || merged.Spec.Notification.Slack == nil {
		t.Fatalf("merged slack is nil — pointer merge dropped parent's slack settings")
	}
	if merged.Spec.Notification.Slack.PerSandbox == nil || !*merged.Spec.Notification.Slack.PerSandbox {
		t.Errorf("expected merged perSandbox=true (from parent), got %v",
			merged.Spec.Notification.Slack.PerSandbox)
	}
	if merged.Spec.Notification.Slack.Transcript == nil ||
		merged.Spec.Notification.Slack.Transcript.Enabled == nil ||
		!*merged.Spec.Notification.Slack.Transcript.Enabled {
		t.Errorf("expected merged transcript.enabled=true (from child), got %v",
			merged.Spec.Notification.Slack.Transcript)
	}
}
