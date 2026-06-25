package cmd

// doctor_slack_groups_read_test.go — tests for the km doctor groups:read scope
// check (Phase 118). groups:read gates reading private-channel metadata; without
// it km can create/post to private channels but cannot inspect them. Mirrors
// doctor_slack_users_read_test.go.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestDoctor_SlackGroupsReadScope_Pass — bot scopes include groups:read. Check OK.
func TestDoctor_SlackGroupsReadScope_Pass(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "groups:write", "groups:history", "groups:read"}, nil
	}
	r := checkSlackGroupsReadScope(context.Background(), getScopes)
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
}

// TestDoctor_SlackGroupsReadScope_WarnWhenMissing — the exact drift this check
// exists to catch: groups:write + groups:history present (private channels can be
// created + receive events) but groups:read absent (cannot inspect them). This is
// the real-world state of an install that predates the groups:read manifest entry.
func TestDoctor_SlackGroupsReadScope_WarnWhenMissing(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "groups:write", "groups:history"}, nil // groups:read absent
	}
	r := checkSlackGroupsReadScope(context.Background(), getScopes)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN when groups:read missing, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "private") {
		t.Errorf("message should mention private channels; got: %s", r.Message)
	}
	if !strings.Contains(r.Message+r.Remediation, "km slack manifest") {
		t.Errorf("remediation should reference km slack manifest; got msg=%s rem=%s", r.Message, r.Remediation)
	}
}

// TestDoctor_SlackGroupsReadScope_Skip — getScopes nil (Slack not configured). SKIPPED.
func TestDoctor_SlackGroupsReadScope_Skip(t *testing.T) {
	r := checkSlackGroupsReadScope(context.Background(), nil)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s (msg=%s)", r.Status, r.Message)
	}
}

// TestDoctor_SlackGroupsReadScope_Error — getScopes errors → WARN (don't fail
// doctor on an auth.test outage).
func TestDoctor_SlackGroupsReadScope_Error(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return nil, errors.New("invalid_auth")
	}
	r := checkSlackGroupsReadScope(context.Background(), getScopes)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN on getScopes error, got %s (msg=%s)", r.Status, r.Message)
	}
}
