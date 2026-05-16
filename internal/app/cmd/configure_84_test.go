package cmd

// Phase 84 — W0-01, W0-02, W0-03: operator-email derivation tests.
// These tests live in package cmd (not cmd_test) so they can call
// the unexported deriveOperatorEmail helper and the runConfigure function.
//
// RED at the end of Plan 84-01 (Wave 0 scaffolds).
// GREEN at the end of Plan 84-04 (km configure operator-email derivation).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigure_DerivesOperatorEmailFromPrefix (W0-01) — direct unit test for
// the deriveOperatorEmail helper function. The helper does not yet exist; this
// test is RED until Plan 84-04 adds the helper.
func TestConfigure_DerivesOperatorEmailFromPrefix(t *testing.T) {
	got := deriveOperatorEmail("kph", "sandboxes", "example.com")
	want := "operator-kph@sandboxes.example.com"
	if got != want {
		t.Errorf("deriveOperatorEmail(kph, sandboxes, example.com) = %q; want %q", got, want)
	}

	// blank inputs must return ""
	if v := deriveOperatorEmail("", "sandboxes", "example.com"); v != "" {
		t.Errorf("deriveOperatorEmail with blank prefix = %q; want empty", v)
	}

	// default install prefix
	got2 := deriveOperatorEmail("km", "sandboxes", "example.com")
	want2 := "operator-km@sandboxes.example.com"
	if got2 != want2 {
		t.Errorf("deriveOperatorEmail(km, sandboxes, example.com) = %q; want %q", got2, want2)
	}
}

// TestConfigure_BlankOperatorEmail_DerivesFromPrefix (W0-02) — drives runConfigure
// in non-interactive mode with a blank operator_email; asserts the written config
// derives the email from prefix + email_subdomain + domain.
func TestConfigure_BlankOperatorEmail_DerivesFromPrefix(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer

	err := runConfigure(
		strings.NewReader(""), // stdin (unused in non-interactive)
		&out,
		dir,   // outputDir
		true,  // nonInteractive
		false, // resetPrefix
		"rg",                        // resourcePrefix
		"sandboxes",                 // emailSubdomain
		"example.com",               // domain
		"",                          // organizationAcct
		"111111111111",              // dnsParentAcct
		"222222222222",              // terraformAcct
		"333333333333",              // applicationAcct
		"https://sso.example.com/start", // ssoStartURL
		"us-east-1",                 // ssoRegion
		"us-east-1",                 // region
		"",                          // stateBucket
		"",                          // artifactsBucket
		"",                          // operatorEmail — blank: should be derived
		"",                          // safePhrase
		0,                           // maxSandboxes
	)
	if err != nil {
		t.Fatalf("runConfigure: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "km-config.yaml"))
	if err != nil {
		t.Fatalf("read km-config.yaml: %v", err)
	}
	body := string(data)
	want := "operator-rg@sandboxes.example.com"
	if !strings.Contains(body, want) {
		t.Errorf("km-config.yaml should contain derived operator_email %q; got:\n%s", want, body)
	}
}

// TestConfigure_ResetPrefix_ClearsOperatorEmail (W0-03) — verifies that
// --reset-prefix clears the stored operator_email so it will be re-derived on
// the next run. Starts from a config with a pre-existing operator_email.
func TestConfigure_ResetPrefix_ClearsOperatorEmail(t *testing.T) {
	dir := t.TempDir()

	// Write an existing km-config.yaml with a pre-existing operator_email.
	existingCfg := `resource_prefix: kph
email_subdomain: sandboxes
domain: example.com
operator_email: operator-kph@sandboxes.example.com
accounts:
  dns_parent: "111111111111"
  terraform: "222222222222"
  application: "333333333333"
sso:
  start_url: https://sso.example.com/start
  region: us-east-1
region: us-east-1
`
	if err := os.WriteFile(filepath.Join(dir, "km-config.yaml"), []byte(existingCfg), 0600); err != nil {
		t.Fatalf("write existing km-config.yaml: %v", err)
	}

	var out bytes.Buffer
	err := runConfigure(
		strings.NewReader(""),
		&out,
		dir,
		true,  // nonInteractive
		true,  // resetPrefix — key flag: must clear operator_email
		"",    // resourcePrefix (empty — reset means use default "km")
		"sandboxes",
		"example.com",
		"",
		"111111111111",
		"222222222222",
		"333333333333",
		"https://sso.example.com/start",
		"us-east-1",
		"us-east-1",
		"",
		"",
		"", // operatorEmail — empty; with reset, stored email must be discarded
		"",
		0,
	)
	if err != nil {
		t.Fatalf("runConfigure --reset-prefix: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "km-config.yaml"))
	if err != nil {
		t.Fatalf("read km-config.yaml: %v", err)
	}
	body := string(data)

	// After --reset-prefix the old operator_email must NOT be present.
	if strings.Contains(body, "operator-kph@") {
		t.Errorf("km-config.yaml must NOT contain old operator_email after --reset-prefix; got:\n%s", body)
	}
	// The operator_email must also NOT be re-derived in the same --reset-prefix run.
	// The NEXT configure run (without --reset-prefix) will derive it from the new prefix.
	if strings.Contains(body, "operator_email:") {
		t.Errorf("km-config.yaml must NOT contain operator_email field after --reset-prefix (should be empty/omitted); got:\n%s", body)
	}
}
