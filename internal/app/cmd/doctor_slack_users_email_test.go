package cmd

// doctor_slack_users_email_test.go — Phase 72 Wave 0 stubs for km doctor
// slack_users_read_email_scope check.
// Plan 72-08 (Wave 4) flips the t.Skip calls to real assertions.
//
// Mirrors the existing slack_files_write_scope pattern in doctor_slack.go.
//
// When Wave 4 lands:
//   - Add checkSlackUsersReadEmailScope in doctor_slack.go
//   - Replace each t.Skip with assertions that match doctor_slack_transcript_test.go pattern

import (
	"testing"
)

// TestDoctor_SlackUsersReadEmailScope_Pass — bot scopes include users:read.email.
// Check returns OK (no error, no warning).
// Wave 4 assertion: check returns a passing DoctorResult.
func TestDoctor_SlackUsersReadEmailScope_Pass(t *testing.T) {
	t.Skip("TODO Wave 4: implement doctor check in 72-08")
}

// TestDoctor_SlackUsersReadEmailScope_Warn — scopes missing users:read.email.
// Check returns WARN with remediation pointing at "km slack manifest" + reinstall.
// Wave 4 assertion: check returns WARN; message contains "km slack manifest".
func TestDoctor_SlackUsersReadEmailScope_Warn(t *testing.T) {
	t.Skip("TODO Wave 4: implement doctor check in 72-08")
}
