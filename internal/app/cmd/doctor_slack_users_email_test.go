package cmd

// doctor_slack_users_email_test.go — Phase 72 Wave 4 tests for km doctor
// slack_users_read_email_scope check.
// Plan 72-08 flips the t.Skip stubs to real assertions.
//
// Mirrors the existing slack_files_write_scope pattern in
// doctor_slack_transcript_test.go.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestDoctor_SlackUsersReadEmailScope_Pass — bot scopes include users:read.email.
// Check returns OK.
func TestDoctor_SlackUsersReadEmailScope_Pass(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "channels:history", "users:read", "users:read.email"}, nil
	}
	r := checkSlackUsersReadEmailScope(context.Background(), getScopes)
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
}

// TestDoctor_SlackUsersReadEmailScope_Warn — scopes missing users:read.email.
// Check returns WARN with remediation pointing at "km slack manifest" + reinstall + rotate-token.
func TestDoctor_SlackUsersReadEmailScope_Warn(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "channels:history"}, nil // missing users:read.email
	}
	r := checkSlackUsersReadEmailScope(context.Background(), getScopes)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "users:read.email") {
		t.Errorf("message should mention scope name; got: %s", r.Message)
	}
	if !strings.Contains(r.Message+r.Remediation, "km slack manifest") {
		t.Errorf("message/remediation should reference km slack manifest; got msg=%s rem=%s", r.Message, r.Remediation)
	}
	if !strings.Contains(r.Message+r.Remediation, "rotate-token") {
		t.Errorf("message/remediation should reference token rotation; got msg=%s rem=%s", r.Message, r.Remediation)
	}
}

// TestDoctor_SlackUsersReadEmailScope_Skip — getScopes is nil (Slack not configured).
// Check returns SKIPPED.
func TestDoctor_SlackUsersReadEmailScope_Skip(t *testing.T) {
	r := checkSlackUsersReadEmailScope(context.Background(), nil)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s (msg=%s)", r.Status, r.Message)
	}
}

// TestDoctor_SlackUsersReadEmailScope_Error — getScopes returns an error.
// Check returns WARN (do not fail doctor on auth.test outage).
func TestDoctor_SlackUsersReadEmailScope_Error(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return nil, errors.New("invalid_auth")
	}
	r := checkSlackUsersReadEmailScope(context.Background(), getScopes)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN on getScopes error, got %s (msg=%s)", r.Status, r.Message)
	}
}
