package cmd

// doctor_slack_users_read_test.go — tests for the km doctor users:read scope check
// (the required companion of users:read.email). Mirrors
// doctor_slack_users_email_test.go.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestDoctor_SlackUsersReadScope_Pass — bot scopes include users:read. Check OK.
func TestDoctor_SlackUsersReadScope_Pass(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "channels:history", "users:read", "users:read.email"}, nil
	}
	r := checkSlackUsersReadScope(context.Background(), getScopes)
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
}

// TestDoctor_SlackUsersReadScope_WarnWhenOnlyEmail — the exact drift this check
// exists to catch: users:read.email present but the base users:read absent. The
// .email substring must NOT satisfy the base-scope check.
func TestDoctor_SlackUsersReadScope_WarnWhenOnlyEmail(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "users:read.email"}, nil // base users:read absent
	}
	r := checkSlackUsersReadScope(context.Background(), getScopes)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN when only users:read.email present, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "companion") {
		t.Errorf("message should explain the companion relationship; got: %s", r.Message)
	}
	if !strings.Contains(r.Message+r.Remediation, "km slack manifest") {
		t.Errorf("remediation should reference km slack manifest; got msg=%s rem=%s", r.Message, r.Remediation)
	}
}

// TestDoctor_SlackUsersReadScope_Skip — getScopes nil (Slack not configured). SKIPPED.
func TestDoctor_SlackUsersReadScope_Skip(t *testing.T) {
	r := checkSlackUsersReadScope(context.Background(), nil)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s (msg=%s)", r.Status, r.Message)
	}
}

// TestDoctor_SlackUsersReadScope_Error — getScopes errors → WARN (don't fail doctor
// on an auth.test outage).
func TestDoctor_SlackUsersReadScope_Error(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return nil, errors.New("invalid_auth")
	}
	r := checkSlackUsersReadScope(context.Background(), getScopes)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN on getScopes error, got %s (msg=%s)", r.Status, r.Message)
	}
}
