package profile

import (
	"testing"
)

// phase92BoolPtr is a local pointer helper for the Wave 2 notification merge tests.
func phase92BoolPtr(b bool) *bool { return &b }

// TestInheritNotificationSpec_ChildOnlyTranscriptInheritsParentPerSandbox verifies
// the pointer-merge bug fix (VC-7): a child profile that sets only ONE notification
// field (notification.slack.transcript.enabled: true) must still inherit the parent's
// OTHER notification settings (notification.slack.perSandbox: true).
//
// Pre-Phase-92 behavior (the bug): the child's pointer-typed Spec.CLI fully replaced
// the parent's, dropping all parent notify settings. Phase 92 fixes this via the typed
// deep-merge in mergeNotificationSpec.
//
// This exercises the typed mergeNotificationSpec(parent, child *NotificationSpec)
// directly; TestInheritNotification_ProfileMergeWiresNotification below proves the
// merger is actually wired into the profile-level merge() entry point.
//
// VC-7
func TestInheritNotificationSpec_ChildOnlyTranscriptInheritsParentPerSandbox(t *testing.T) {
	parent := &NotificationSpec{
		Slack: &NotificationSlackSpec{
			Enabled:    phase92BoolPtr(true),
			PerSandbox: phase92BoolPtr(true),
		},
	}
	child := &NotificationSpec{
		Slack: &NotificationSlackSpec{
			Transcript: &NotificationSlackTranscriptSpec{Enabled: phase92BoolPtr(true)},
		},
	}

	merged := mergeNotificationSpec(parent, child)

	if merged == nil || merged.Slack == nil {
		t.Fatalf("merged slack is nil — pointer merge dropped parent's slack settings")
	}
	if merged.Slack.PerSandbox == nil || !*merged.Slack.PerSandbox {
		t.Errorf("expected merged perSandbox=true (from parent), got %v", merged.Slack.PerSandbox)
	}
	if merged.Slack.Enabled == nil || !*merged.Slack.Enabled {
		t.Errorf("expected merged slack.enabled=true (from parent), got %v", merged.Slack.Enabled)
	}
	if merged.Slack.Transcript == nil ||
		merged.Slack.Transcript.Enabled == nil ||
		!*merged.Slack.Transcript.Enabled {
		t.Errorf("expected merged transcript.enabled=true (from child), got %v", merged.Slack.Transcript)
	}
}

// TestInheritNotification_ProfileMergeWiresNotification proves the typed merger is
// wired into the profile-level merge() entry point — a child SandboxProfile that only
// flips transcript on still inherits the parent's perSandbox after merge().
func TestInheritNotification_ProfileMergeWiresNotification(t *testing.T) {
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

	merged := merge(parent, child)

	if merged.Spec.Notification == nil || merged.Spec.Notification.Slack == nil {
		t.Fatalf("merge() dropped notification.slack — typed merger not wired into merge()")
	}
	if merged.Spec.Notification.Slack.PerSandbox == nil || !*merged.Spec.Notification.Slack.PerSandbox {
		t.Errorf("merge() did not inherit parent perSandbox=true, got %v",
			merged.Spec.Notification.Slack.PerSandbox)
	}
	if merged.Spec.Notification.Slack.Transcript == nil ||
		merged.Spec.Notification.Slack.Transcript.Enabled == nil ||
		!*merged.Spec.Notification.Slack.Transcript.Enabled {
		t.Errorf("merge() did not carry child transcript.enabled=true, got %v",
			merged.Spec.Notification.Slack.Transcript)
	}
}

// TestInheritNotificationSpec_ParentNil_ChildOverrides: parent has no notification →
// use child as-is.
func TestInheritNotificationSpec_ParentNil_ChildOverrides(t *testing.T) {
	child := &NotificationSpec{Slack: &NotificationSlackSpec{Enabled: phase92BoolPtr(true)}}
	merged := mergeNotificationSpec(nil, child)
	if merged != child {
		t.Errorf("expected child returned as-is when parent is nil")
	}
}

// TestInheritNotificationSpec_ChildNil_ParentInherits: child has no notification →
// use parent as-is.
func TestInheritNotificationSpec_ChildNil_ParentInherits(t *testing.T) {
	parent := &NotificationSpec{Slack: &NotificationSlackSpec{Enabled: phase92BoolPtr(true)}}
	merged := mergeNotificationSpec(parent, nil)
	if merged != parent {
		t.Errorf("expected parent returned as-is when child is nil")
	}
}

// TestInheritNotificationSpec_BothNil: both nil → nil result.
func TestInheritNotificationSpec_BothNil(t *testing.T) {
	if merged := mergeNotificationSpec(nil, nil); merged != nil {
		t.Errorf("expected nil result when both parent and child are nil, got %v", merged)
	}
}

// TestInheritNotificationSpec_InvitesMerge_ChildEmailsReplaceParent: a non-empty child
// emails list replaces the parent's (child-wins), while other invites fields merge.
func TestInheritNotificationSpec_InvitesMerge_ChildEmailsReplaceParent(t *testing.T) {
	parent := &NotificationSpec{
		Slack: &NotificationSlackSpec{
			Invites: &NotificationSlackInvitesSpec{
				Emails:     []string{"parent@example.com"},
				UseConnect: phase92BoolPtr(true),
			},
		},
	}
	child := &NotificationSpec{
		Slack: &NotificationSlackSpec{
			Invites: &NotificationSlackInvitesSpec{
				Emails: []string{"child@example.com"},
			},
		},
	}

	merged := mergeNotificationSpec(parent, child)
	inv := merged.Slack.Invites
	if inv == nil {
		t.Fatalf("merged invites is nil")
	}
	if len(inv.Emails) != 1 || inv.Emails[0] != "child@example.com" {
		t.Errorf("expected child emails to replace parent, got %v", inv.Emails)
	}
	if inv.UseConnect == nil || !*inv.UseConnect {
		t.Errorf("expected useConnect=true inherited from parent, got %v", inv.UseConnect)
	}
}

// TestInheritNotificationSpec_InvitesMerge_EmptyChildEmailsKeepParent: an empty child
// emails list inherits the parent's list.
func TestInheritNotificationSpec_InvitesMerge_EmptyChildEmailsKeepParent(t *testing.T) {
	parent := &NotificationSpec{
		Slack: &NotificationSlackSpec{
			Invites: &NotificationSlackInvitesSpec{Emails: []string{"parent@example.com"}},
		},
	}
	child := &NotificationSpec{
		Slack: &NotificationSlackSpec{
			Invites: &NotificationSlackInvitesSpec{UseConnect: phase92BoolPtr(false)},
		},
	}

	merged := mergeNotificationSpec(parent, child)
	inv := merged.Slack.Invites
	if len(inv.Emails) != 1 || inv.Emails[0] != "parent@example.com" {
		t.Errorf("expected parent emails inherited when child empty, got %v", inv.Emails)
	}
	if inv.UseConnect == nil || *inv.UseConnect {
		t.Errorf("expected useConnect=false from child, got %v", inv.UseConnect)
	}
}
