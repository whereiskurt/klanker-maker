package profile_test

// validate_slack_invite_emails_test.go — Phase 72 Layer 6 tests for
// notification.slack.invites.emails and .useConnect profile field validation.
// Phase 92 (Wave 2): migrated from spec.cli.notifySlackInviteEmails/useSlackConnect
// to the structured spec.notification.slack.invites block.

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// parseAndValidateInvite parses the YAML string and runs ValidateSemantic.
// Fatalf on parse failure; returns only the semantic errors.
func parseAndValidateInvite(t *testing.T, yaml string) []profile.ValidationError {
	t.Helper()
	p, err := profile.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return profile.ValidateSemantic(p)
}

// containsInviteError returns true if any non-warning error has the given path and message fragment.
func containsInviteError(errs []profile.ValidationError, path, msg string) bool {
	for _, e := range errs {
		if e.IsWarning {
			continue
		}
		if e.Path == path && strings.Contains(e.Message, msg) {
			return true
		}
	}
	return false
}

// TestParse_NotifySlackInviteEmails — YAML with notification.slack.invites.emails
// round-trips through Parse; the slice is intact and in order.
func TestParse_NotifySlackInviteEmails(t *testing.T) {
	const yaml = `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: {name: t}
spec:
  notification:
    slack:
      enabled: true
      invites:
        emails: [alice@example.com, bob@example.com]
`
	p, err := profile.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Spec.Notification == nil || p.Spec.Notification.Slack == nil || p.Spec.Notification.Slack.Invites == nil {
		t.Fatal("expected Spec.Notification.Slack.Invites to be non-nil")
	}
	got := p.Spec.Notification.Slack.Invites.Emails
	want := []string{"alice@example.com", "bob@example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("invites.emails: got %v; want %v", got, want)
	}
}

// TestValidate_InviteEmails_RequiresSlackEnabled — non-empty list + slack.enabled:false
// → validation error SE1 with correct path and message fragment.
func TestValidate_InviteEmails_RequiresSlackEnabled(t *testing.T) {
	const yaml = `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: {name: t}
spec:
  notification:
    slack:
      enabled: false
      invites:
        emails: [alice@example.com]
`
	errs := parseAndValidateInvite(t, yaml)
	if !containsInviteError(errs, "spec.notification.slack.invites.emails", "requires notification.slack.enabled") {
		t.Errorf("expected SE1 error (path=spec.notification.slack.invites.emails, msg contains 'requires notification.slack.enabled'); got %+v", errs)
	}
}

// TestValidate_InviteEmails_InvalidEmail — malformed entry in the list
// → validation error SE2 with path spec.notification.slack.invites.emails[0] and "invalid email".
func TestValidate_InviteEmails_InvalidEmail(t *testing.T) {
	const yaml = `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: {name: t}
spec:
  notification:
    slack:
      enabled: true
      invites:
        emails: [notanemail]
`
	errs := parseAndValidateInvite(t, yaml)
	if !containsInviteError(errs, "spec.notification.slack.invites.emails[0]", "invalid email") {
		t.Errorf("expected SE2 error (path=spec.notification.slack.invites.emails[0], msg contains 'invalid email'); got %+v", errs)
	}
}

// TestParse_UseSlackConnect — invites.useConnect:false round-trips; omitting the field leaves nil pointer.
func TestParse_UseSlackConnect(t *testing.T) {
	// Explicit false round-trips.
	const yamlFalse = `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: {name: t}
spec:
  notification:
    slack:
      enabled: true
      invites:
        useConnect: false
        emails: [alice@example.com]
`
	p, err := profile.Parse([]byte(yamlFalse))
	if err != nil {
		t.Fatalf("parse (false): %v", err)
	}
	if p.Spec.Notification == nil || p.Spec.Notification.Slack == nil || p.Spec.Notification.Slack.Invites == nil {
		t.Fatal("expected Spec.Notification.Slack.Invites to be non-nil")
	}
	if p.Spec.Notification.Slack.Invites.UseConnect == nil || *p.Spec.Notification.Slack.Invites.UseConnect != false {
		t.Errorf("expected useConnect=false; got %v", p.Spec.Notification.Slack.Invites.UseConnect)
	}
	// useConnect:false with valid invite list and slack.enabled should produce no errors.
	if errs := profile.ValidateSemantic(p); len(errs) != 0 {
		t.Errorf("expected no validation errors for useConnect:false; got %v", errs)
	}

	// Omitted field stays nil (resolved to true at read time in km create, not here).
	const yamlOmit = `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: {name: t}
spec:
  notification:
    slack:
      enabled: true
      invites:
        emails: [alice@example.com]
`
	p2, err := profile.Parse([]byte(yamlOmit))
	if err != nil {
		t.Fatalf("parse (omit): %v", err)
	}
	if p2.Spec.Notification == nil || p2.Spec.Notification.Slack == nil || p2.Spec.Notification.Slack.Invites == nil {
		t.Fatal("expected Spec.Notification.Slack.Invites to be non-nil (omit case)")
	}
	if p2.Spec.Notification.Slack.Invites.UseConnect != nil {
		t.Errorf("expected nil useConnect when omitted; got %v", *p2.Spec.Notification.Slack.Invites.UseConnect)
	}
}

// TestSchema_InviteEmails — schema file contains the new field entries.
func TestSchema_InviteEmails(t *testing.T) {
	schemaBytes, err := os.ReadFile("schemas/sandbox_profile.schema.json")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if !bytes.Contains(schemaBytes, []byte(`"invites"`)) {
		t.Errorf("schema missing invites entry")
	}
	if !bytes.Contains(schemaBytes, []byte(`"format": "email"`)) {
		t.Errorf("schema missing format:email constraint")
	}
	if !bytes.Contains(schemaBytes, []byte(`"useConnect"`)) {
		t.Errorf("schema missing useConnect entry")
	}
}

// TestValidate_InviteEmails_EmptyList_NoRequiresSlack — empty list with slack.enabled:false
// → no error (empty list is a no-op).
func TestValidate_InviteEmails_EmptyList_NoRequiresSlack(t *testing.T) {
	const yaml = `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: {name: t}
spec:
  notification:
    slack:
      enabled: false
      invites:
        emails: []
`
	errs := parseAndValidateInvite(t, yaml)
	for _, e := range errs {
		if e.IsWarning {
			continue
		}
		if strings.Contains(e.Message, "invites.emails") {
			t.Errorf("unexpected error for empty invite list: %+v", e)
		}
	}
}
