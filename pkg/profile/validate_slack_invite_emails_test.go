package profile_test

// validate_slack_invite_emails_test.go — Phase 72 Layer 6 tests for
// notifySlackInviteEmails and useSlackConnect profile field validation.
// Plan 72-02 (Wave 1) — stub t.Skip calls replaced with real assertions.

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

// TestParse_NotifySlackInviteEmails — YAML with spec.cli.notifySlackInviteEmails
// round-trips through Parse; the slice is intact and in order.
func TestParse_NotifySlackInviteEmails(t *testing.T) {
	const yaml = `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: {name: t}
spec:
  cli:
    notifySlackEnabled: true
    notifySlackInviteEmails: [alice@example.com, bob@example.com]
`
	p, err := profile.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Spec.CLI == nil {
		t.Fatal("expected Spec.CLI to be non-nil")
	}
	got := p.Spec.CLI.NotifySlackInviteEmails
	want := []string{"alice@example.com", "bob@example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NotifySlackInviteEmails: got %v; want %v", got, want)
	}
}

// TestValidate_InviteEmails_RequiresSlackEnabled — non-empty list + notifySlackEnabled:false
// → validation error SE1 with correct path and message fragment.
func TestValidate_InviteEmails_RequiresSlackEnabled(t *testing.T) {
	const yaml = `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: {name: t}
spec:
  cli:
    notifySlackEnabled: false
    notifySlackInviteEmails: [alice@example.com]
`
	errs := parseAndValidateInvite(t, yaml)
	if !containsInviteError(errs, "spec.cli.notifySlackInviteEmails", "requires notifySlackEnabled") {
		t.Errorf("expected SE1 error (path=spec.cli.notifySlackInviteEmails, msg contains 'requires notifySlackEnabled'); got %+v", errs)
	}
}

// TestValidate_InviteEmails_InvalidEmail — malformed entry in the list
// → validation error SE2 with path spec.cli.notifySlackInviteEmails[0] and "invalid email".
func TestValidate_InviteEmails_InvalidEmail(t *testing.T) {
	const yaml = `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: {name: t}
spec:
  cli:
    notifySlackEnabled: true
    notifySlackInviteEmails: [notanemail]
`
	errs := parseAndValidateInvite(t, yaml)
	if !containsInviteError(errs, "spec.cli.notifySlackInviteEmails[0]", "invalid email") {
		t.Errorf("expected SE2 error (path=spec.cli.notifySlackInviteEmails[0], msg contains 'invalid email'); got %+v", errs)
	}
}

// TestParse_UseSlackConnect — useSlackConnect:false round-trips; omitting the field leaves nil pointer.
func TestParse_UseSlackConnect(t *testing.T) {
	// Explicit false round-trips.
	const yamlFalse = `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: {name: t}
spec:
  cli:
    notifySlackEnabled: true
    useSlackConnect: false
    notifySlackInviteEmails: [alice@example.com]
`
	p, err := profile.Parse([]byte(yamlFalse))
	if err != nil {
		t.Fatalf("parse (false): %v", err)
	}
	if p.Spec.CLI == nil {
		t.Fatal("expected Spec.CLI to be non-nil")
	}
	if p.Spec.CLI.UseSlackConnect == nil || *p.Spec.CLI.UseSlackConnect != false {
		t.Errorf("expected UseSlackConnect=false; got %v", p.Spec.CLI.UseSlackConnect)
	}
	// useSlackConnect:false with valid invite list and slackEnabled should produce no errors.
	if errs := profile.ValidateSemantic(p); len(errs) != 0 {
		t.Errorf("expected no validation errors for useSlackConnect:false; got %v", errs)
	}

	// Omitted field stays nil (resolved to true at read time in km create, not here).
	const yamlOmit = `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: {name: t}
spec:
  cli:
    notifySlackEnabled: true
    notifySlackInviteEmails: [alice@example.com]
`
	p2, err := profile.Parse([]byte(yamlOmit))
	if err != nil {
		t.Fatalf("parse (omit): %v", err)
	}
	if p2.Spec.CLI == nil {
		t.Fatal("expected Spec.CLI to be non-nil (omit case)")
	}
	if p2.Spec.CLI.UseSlackConnect != nil {
		t.Errorf("expected nil UseSlackConnect when omitted; got %v", *p2.Spec.CLI.UseSlackConnect)
	}
}

// TestSchema_InviteEmails — schema file contains the new field entries.
func TestSchema_InviteEmails(t *testing.T) {
	schemaBytes, err := os.ReadFile("schemas/sandbox_profile.schema.json")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if !bytes.Contains(schemaBytes, []byte(`"notifySlackInviteEmails"`)) {
		t.Errorf("schema missing notifySlackInviteEmails entry")
	}
	if !bytes.Contains(schemaBytes, []byte(`"format": "email"`)) {
		t.Errorf("schema missing format:email constraint")
	}
	if !bytes.Contains(schemaBytes, []byte(`"useSlackConnect"`)) {
		t.Errorf("schema missing useSlackConnect entry")
	}
}

// TestValidate_InviteEmails_EmptyList_NoRequiresSlack — empty list with notifySlackEnabled:false
// → no error (empty list is a no-op).
func TestValidate_InviteEmails_EmptyList_NoRequiresSlack(t *testing.T) {
	const yaml = `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata: {name: t}
spec:
  cli:
    notifySlackEnabled: false
    notifySlackInviteEmails: []
`
	errs := parseAndValidateInvite(t, yaml)
	for _, e := range errs {
		if e.IsWarning {
			continue
		}
		if strings.Contains(e.Message, "notifySlackInviteEmails") {
			t.Errorf("unexpected error for empty invite list: %+v", e)
		}
	}
}
