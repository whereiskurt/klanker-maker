package cmd

// Wave 0 RED scaffolding — Phase 84.3 Plan 01 Task 2.
// Tests for closure (g) km env subcommand — KM-ENV-EXPORT-HELPER.
//
// These tests reference production symbols that Plan 04 will create:
//   - NewEnvCmd (cmd/env.go — new file, follows info.go pattern)
//   - runEnvExport (cmd/env.go unexported helper)
//
// RED contract: `go test ./internal/app/cmd/` fails with
//   undefined: NewEnvCmd
//   undefined: runEnvExport
// Plan 04 makes them GREEN.

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// makeSampleEnvConfig returns a Config with all KM_* env-var fields populated
// to deterministic test values, suitable for TestEnvCmd tests.
func makeSampleEnvConfig() *config.Config {
	return &config.Config{
		ResourcePrefix:        "km",
		PrimaryRegion:         "us-east-1",
		Domain:                "example.com",
		EmailSubdomain:        "sandboxes",
		Route53ZoneID:         "ZTEST12345",
		OrganizationAccountID: "111111111111",
		DNSParentAccountID:    "222222222222",
		ApplicationAccountID:  "333333333333",
		ArtifactsBucket:       "km-artifacts-111111111111",
		OperatorEmail:         "operator-km@sandboxes.example.com",
	}
}

// TestEnvCmd_DefaultOutput verifies that runEnvExport writes exactly the expected
// set of `export KEY=value` lines for the full KM_* superset (11 vars).
// AWS_PROFILE must NOT appear when includeAWSProfile is false.
func TestEnvCmd_DefaultOutput(t *testing.T) {
	cfg := makeSampleEnvConfig()
	var buf bytes.Buffer

	if err := runEnvExport(cfg, &buf, false); err != nil {
		t.Fatalf("runEnvExport: %v", err)
	}

	out := buf.String()
	required := []string{
		"export KM_RESOURCE_PREFIX=km",
		"export KM_REGION=us-east-1",
		"export KM_DOMAIN=example.com",
		"export KM_EMAIL_SUBDOMAIN=sandboxes",
		"export KM_ROUTE53_ZONE_ID=ZTEST12345",
		"export KM_ACCOUNTS_ORGANIZATION=111111111111",
		"export KM_ACCOUNTS_DNS_PARENT=222222222222",
		"export KM_ACCOUNTS_APPLICATION=333333333333",
		"export KM_ARTIFACTS_BUCKET=km-artifacts-111111111111",
		"export KM_OPERATOR_EMAIL=operator-km@sandboxes.example.com",
	}
	for _, want := range required {
		if !strings.Contains(out, want) {
			t.Errorf("runEnvExport output missing %q; got:\n%s", want, out)
		}
	}

	// AWS_PROFILE must NOT appear when includeAWSProfile is false.
	if strings.Contains(out, "AWS_PROFILE") {
		t.Errorf("unexpected AWS_PROFILE in default output; got:\n%s", out)
	}

	// Output must be deterministic (no blank lines between the export statements).
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for _, line := range lines {
		if line == "" {
			t.Errorf("unexpected blank line in runEnvExport output; got:\n%s", out)
			break
		}
		if !strings.HasPrefix(line, "export ") {
			t.Errorf("line does not start with 'export ': %q", line)
		}
	}
}

// TestEnvCmd_EvalSafe verifies that the runEnvExport output survives `eval` in bash.
// Skipped when /bin/bash is unavailable.
func TestEnvCmd_EvalSafe(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available; skipping eval-safety test")
	}

	cfg := makeSampleEnvConfig()
	var buf bytes.Buffer
	if err := runEnvExport(cfg, &buf, false); err != nil {
		t.Fatalf("runEnvExport: %v", err)
	}

	// Pipe the output into bash's eval, then echo $KM_RESOURCE_PREFIX.
	script := buf.String() + "\necho $KM_RESOURCE_PREFIX\n"
	cmd := exec.Command("bash", "-c", script)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("eval via bash failed: %v; script:\n%s", err, script)
	}
	got := strings.TrimRight(string(out), "\n")
	if got != cfg.ResourcePrefix {
		t.Errorf("eval result: got %q, want %q", got, cfg.ResourcePrefix)
	}
}

// TestEnvCmd_AWSProfileFlag verifies that when includeAWSProfile is true,
// runEnvExport includes `export AWS_PROFILE=<value>` where value is the
// ambient AWS_PROFILE env var.
func TestEnvCmd_AWSProfileFlag(t *testing.T) {
	t.Setenv("AWS_PROFILE", "klanker-terraform")

	cfg := makeSampleEnvConfig()
	var buf bytes.Buffer

	if err := runEnvExport(cfg, &buf, true); err != nil {
		t.Fatalf("runEnvExport(includeAWSProfile=true): %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "export AWS_PROFILE=klanker-terraform") {
		t.Errorf("expected 'export AWS_PROFILE=klanker-terraform' in output; got:\n%s", out)
	}

	// Confirm default (false) does NOT include it — re-run without the flag.
	var buf2 bytes.Buffer
	if err := runEnvExport(cfg, &buf2, false); err != nil {
		t.Fatalf("runEnvExport(includeAWSProfile=false): %v", err)
	}
	if strings.Contains(buf2.String(), "AWS_PROFILE") {
		t.Errorf("unexpected AWS_PROFILE in output when includeAWSProfile=false; got:\n%s", buf2.String())
	}
}

// TestEnvCmd_Registration verifies that NewEnvCmd(cfg) returns a properly
// registered cobra.Command with the correct Use and flag definitions.
func TestEnvCmd_Registration(t *testing.T) {
	cfg := makeSampleEnvConfig()
	c := NewEnvCmd(cfg)

	if c.Use != "env" {
		t.Errorf("cmd.Use = %q, want %q", c.Use, "env")
	}

	awsProfileFlag := c.Flags().Lookup("aws-profile")
	if awsProfileFlag == nil {
		t.Fatal("--aws-profile flag not registered on env command")
	}
	if awsProfileFlag.DefValue != "false" {
		t.Errorf("--aws-profile default = %q, want %q", awsProfileFlag.DefValue, "false")
	}

}
