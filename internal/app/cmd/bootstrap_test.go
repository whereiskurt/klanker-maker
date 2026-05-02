package cmd_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// testTrustedBase returns a minimal trusted-base ARN slice for use in SCP tests.
// Uses a fixed account ID so tests are deterministic without AWS access.
func testTrustedBase() []string {
	acct := "123456789012"
	return []string{
		"arn:aws:iam::" + acct + ":role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*",
		"arn:aws:iam::" + acct + ":role/km-provisioner-*",
		"arn:aws:iam::" + acct + ":role/km-lifecycle-*",
		"arn:aws:iam::" + acct + ":role/km-ecs-spot-handler",
		"arn:aws:iam::" + acct + ":role/km-ttl-handler",
		"arn:aws:iam::" + acct + ":role/km-create-handler",
	}
}

// runBootstrapCmd is a helper that runs the bootstrap cobra command with the
// given dryRun flag and applyFn DI override. It captures stdout and returns it.
func runBootstrapCmd(t *testing.T, cfg *config.Config, dryRun bool, applyFn cmd.TerragruntApplyFunc) (string, error) {
	t.Helper()

	// Override the package-level apply func for DI.
	orig := cmd.ApplyTerragruntFunc
	if applyFn != nil {
		cmd.ApplyTerragruntFunc = applyFn
	}
	t.Cleanup(func() { cmd.ApplyTerragruntFunc = orig })

	var buf bytes.Buffer
	root := &cobra.Command{Use: "km"}
	bootstrapCmd := cmd.NewBootstrapCmdWithWriter(cfg, &buf)
	root.AddCommand(bootstrapCmd)

	args := []string{"bootstrap"}
	if dryRun {
		args = append(args, "--dry-run")
	} else {
		args = append(args, "--dry-run=false")
	}
	root.SetArgs(args)
	root.SetOut(&buf)
	root.SetErr(&buf)

	err := root.Execute()
	return buf.String(), err
}

// TestBootstrapDryRunShowsSCP verifies that dry-run output includes SCP policy details
// when OrganizationAccountID and ApplicationAccountID are both populated.
func TestBootstrapDryRunShowsSCP(t *testing.T) {
	cfg := &config.Config{
		OrganizationAccountID: "123456789012",
		ApplicationAccountID:  "987654321098",
		PrimaryRegion:         "us-east-1",
		Domain:                "test.example.com",
	}

	out, err := runBootstrapCmd(t, cfg, true, nil)
	if err != nil {
		t.Fatalf("bootstrap --dry-run: %v\noutput: %s", err, out)
	}

	checks := []struct {
		name    string
		pattern string
	}{
		{"policy name", "km-sandbox-containment"},
		{"application account ID", "987654321098"},
		{"SG mutation threat", "SG mutation"},
		{"trusted role carve-out", "AWSReservedSSO"},
	}

	for _, c := range checks {
		if !strings.Contains(out, c.pattern) {
			t.Errorf("bootstrap --dry-run output missing %s (%q); output:\n%s", c.name, c.pattern, out)
		}
	}
}

// TestBootstrapDryRunNoManagementAccount verifies that dry-run output includes a
// SCP-skipped message when OrganizationAccountID is empty.
func TestBootstrapDryRunNoManagementAccount(t *testing.T) {
	cfg := &config.Config{
		OrganizationAccountID: "",
		ApplicationAccountID:  "987654321098",
		PrimaryRegion:         "us-east-1",
		Domain:                "test.example.com",
	}

	out, err := runBootstrapCmd(t, cfg, true, nil)
	if err != nil {
		t.Fatalf("bootstrap --dry-run no-mgmt: %v\noutput: %s", err, out)
	}

	hasSkipped := strings.Contains(out, "SKIPPED") || strings.Contains(out, "no management account")
	if !hasSkipped {
		t.Errorf("expected warning about missing management account; output:\n%s", out)
	}
}

// TestBootstrapSCPApplyPath verifies that the non-dry-run path invokes terragrunt apply
// on a directory ending with "infra/live/management/scp".
func TestBootstrapSCPApplyPath(t *testing.T) {
	cfg := &config.Config{
		OrganizationAccountID: "123456789012",
		ApplicationAccountID:  "987654321098",
		PrimaryRegion:         "us-east-1",
		Domain:                "test.example.com",
	}

	var capturedDir string
	fakeApply := cmd.TerragruntApplyFunc(func(_ context.Context, dir string) error {
		capturedDir = dir
		return nil
	})

	_, err := runBootstrapCmd(t, cfg, false, fakeApply)
	if err != nil {
		t.Fatalf("bootstrap (non-dry-run): %v", err)
	}

	wantSuffix := "infra/live/management/scp"
	if !strings.HasSuffix(capturedDir, wantSuffix) {
		t.Errorf("terragrunt apply called with dir %q; want suffix %q", capturedDir, wantSuffix)
	}
}

// TestBootstrapSCP_IncludesAMILifecycleMutatingOps verifies that the DenyInfraAndStorage
// statement in the SCP policy includes all three Phase 56 AMI-lifecycle mutating operations.
// Calls buildSCPPolicy directly — requires no AWS config.
func TestBootstrapSCP_IncludesAMILifecycleMutatingOps(t *testing.T) {
	trustedBase := testTrustedBase()
	trustedInstance := append([]string{}, trustedBase...)
	trustedIAM := append(append([]string{}, trustedBase...), "arn:aws:iam::123456789012:role/km-budget-enforcer-*")
	trustedSSM := []string{"arn:aws:iam::*:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*"}

	policy := cmd.BuildSCPPolicy(trustedBase, trustedInstance, trustedIAM, trustedSSM, "us-east-1")

	wantOps := []string{
		"ec2:DeregisterImage",
		"ec2:DeleteSnapshot",
		"ec2:CreateTags",
	}

	var denyActions []string
	for _, stmt := range policy.Statement {
		if stmt.Sid == "DenyInfraAndStorage" {
			denyActions = stmt.Action
			break
		}
	}

	if denyActions == nil {
		t.Fatal("DenyInfraAndStorage statement not found in SCP policy")
	}

	for _, op := range wantOps {
		found := false
		for _, a := range denyActions {
			if a == op {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("DenyInfraAndStorage Action missing %q; actions: %v", op, denyActions)
		}
	}
}

// TestBootstrapSCP_DescribeOpsNotInDeny verifies that read-only Describe operations
// are NOT in any Deny statement — they must remain implicitly allowed.
// Calls buildSCPPolicy directly — requires no AWS config.
func TestBootstrapSCP_DescribeOpsNotInDeny(t *testing.T) {
	trustedBase := testTrustedBase()
	trustedInstance := append([]string{}, trustedBase...)
	trustedIAM := append(append([]string{}, trustedBase...), "arn:aws:iam::123456789012:role/km-budget-enforcer-*")
	trustedSSM := []string{"arn:aws:iam::*:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*"}

	policy := cmd.BuildSCPPolicy(trustedBase, trustedInstance, trustedIAM, trustedSSM, "us-east-1")

	readOnlyOps := []string{
		"ec2:DescribeImages",
		"ec2:DescribeSnapshots",
	}

	for _, stmt := range policy.Statement {
		if stmt.Effect != "Deny" {
			continue
		}
		for _, op := range readOnlyOps {
			for _, a := range stmt.Action {
				if a == op {
					t.Errorf("read-only op %q must NOT appear in Deny statement %q", op, stmt.Sid)
				}
			}
		}
	}
}

// TestBootstrapSCP_TrustedBaseUnchanged verifies that the ArnNotLike condition on
// DenyInfraAndStorage still includes the expected trusted-base principal patterns.
// Calls buildSCPPolicy directly — requires no AWS config.
func TestBootstrapSCP_TrustedBaseUnchanged(t *testing.T) {
	trustedBase := testTrustedBase()
	trustedInstance := append([]string{}, trustedBase...)
	trustedIAM := append(append([]string{}, trustedBase...), "arn:aws:iam::123456789012:role/km-budget-enforcer-*")
	trustedSSM := []string{"arn:aws:iam::*:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*"}

	policy := cmd.BuildSCPPolicy(trustedBase, trustedInstance, trustedIAM, trustedSSM, "us-east-1")

	var denyStmt *cmd.SCPStatement
	for i := range policy.Statement {
		if policy.Statement[i].Sid == "DenyInfraAndStorage" {
			denyStmt = &policy.Statement[i]
			break
		}
	}

	if denyStmt == nil {
		t.Fatal("DenyInfraAndStorage statement not found")
	}

	// Condition must be an ArnNotLike map containing the trusted-base ARNs.
	condMap, ok := denyStmt.Condition.(map[string]interface{})
	if !ok {
		t.Fatalf("DenyInfraAndStorage Condition is not map[string]interface{}; got %T", denyStmt.Condition)
	}
	arnNotLike, ok := condMap["ArnNotLike"].(map[string]interface{})
	if !ok {
		t.Fatal("DenyInfraAndStorage Condition missing ArnNotLike key")
	}
	principals, ok := arnNotLike["aws:PrincipalARN"].([]string)
	if !ok {
		t.Fatalf("ArnNotLike aws:PrincipalARN is not []string; got %T", arnNotLike["aws:PrincipalARN"])
	}

	wantPatterns := []string{"AWSReservedSSO", "km-provisioner", "km-lifecycle"}
	for _, pat := range wantPatterns {
		found := false
		for _, arn := range principals {
			if strings.Contains(arn, pat) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("trusted-base condition missing principal pattern %q; got: %v", pat, principals)
		}
	}
}

// TestBootstrapShowSCP_EmitsOperatorPositiveAllowGuidance verifies that writeOperatorIAMGuidance
// emits all required AMI-lifecycle ops and the SCP-vs-IAM-allow distinction rationale.
// Uses bytes.Buffer injection — requires no AWS config.
func TestBootstrapShowSCP_EmitsOperatorPositiveAllowGuidance(t *testing.T) {
	var buf bytes.Buffer
	cmd.WriteOperatorIAMGuidance(&buf)
	out := buf.String()

	wants := []string{
		"Operator IAM Positive-Allow Requirements",
		"ec2:DescribeImages",
		"ec2:DescribeSnapshots",
		"ec2:DeregisterImage",
		"ec2:DeleteSnapshot",
		"ec2:CreateTags",
		"NOT the same as granting",
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("operator guidance missing %q\nGot:\n%s", want, out)
		}
	}
}

// TestBootstrapDryRunShowsOrganizationAccount verifies that dry-run output includes
// "Organization account:" and "DNS parent account:" lines when both fields are set.
func TestBootstrapDryRunShowsOrganizationAccount(t *testing.T) {
	cfg := &config.Config{
		OrganizationAccountID: "111111111111",
		DNSParentAccountID:    "222222222222",
		ApplicationAccountID:  "333333333333",
		PrimaryRegion:         "us-east-1",
		Domain:                "test.example.com",
	}

	out, err := runBootstrapCmd(t, cfg, true, nil)
	if err != nil {
		t.Fatalf("bootstrap --dry-run: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, "Organization account: 111111111111") {
		t.Errorf("expected 'Organization account: 111111111111' in output; got:\n%s", out)
	}
	if !strings.Contains(out, "DNS parent account: 222222222222") {
		t.Errorf("expected 'DNS parent account: 222222222222' in output; got:\n%s", out)
	}
}

// TestBootstrapDryRunNoOrganizationAccount verifies that dry-run output includes a
// SCP-skipped message referencing "accounts.organization" when OrganizationAccountID is blank.
func TestBootstrapDryRunNoOrganizationAccount(t *testing.T) {
	cfg := &config.Config{
		OrganizationAccountID: "",
		DNSParentAccountID:    "222222222222",
		ApplicationAccountID:  "333333333333",
		PrimaryRegion:         "us-east-1",
		Domain:                "test.example.com",
	}

	out, err := runBootstrapCmd(t, cfg, true, nil)
	if err != nil {
		t.Fatalf("bootstrap --dry-run no-org: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, "SKIPPED") {
		t.Errorf("expected 'SKIPPED' in dry-run output when org blank; output:\n%s", out)
	}
	if !strings.Contains(out, "accounts.organization") {
		t.Errorf("expected 'accounts.organization' in SCP-skip message; output:\n%s", out)
	}
}

// TestBootstrapSCPSkipped_OrganizationBlank verifies that the non-dry-run path does NOT
// invoke terragrunt apply when OrganizationAccountID is blank.
func TestBootstrapSCPSkipped_OrganizationBlank(t *testing.T) {
	cfg := &config.Config{
		OrganizationAccountID: "",
		ApplicationAccountID:  "333333333333",
		PrimaryRegion:         "us-east-1",
		Domain:                "test.example.com",
	}

	applyCount := 0
	fakeApply := cmd.TerragruntApplyFunc(func(_ context.Context, _ string) error {
		applyCount++
		return nil
	})

	_, err := runBootstrapCmd(t, cfg, false, fakeApply)
	if err != nil {
		t.Fatalf("bootstrap (non-dry-run, org blank): %v", err)
	}

	if applyCount != 0 {
		t.Errorf("terragrunt apply should NOT be invoked when OrganizationAccountID is blank; got %d calls", applyCount)
	}
}

// TestShowPrereqsNoOrganizationAccount verifies that runShowPrereqs (via --show-prereqs)
// returns nil (not an error) and prints a descriptive message referencing accounts.organization
// when OrganizationAccountID is blank.
func TestShowPrereqsNoOrganizationAccount(t *testing.T) {
	cfg := &config.Config{
		OrganizationAccountID: "",
		ApplicationAccountID:  "333333333333",
		PrimaryRegion:         "us-east-1",
		Domain:                "test.example.com",
	}

	var buf bytes.Buffer
	root := &cobra.Command{Use: "km"}
	bootstrapCmd := cmd.NewBootstrapCmdWithWriter(cfg, &buf)
	root.AddCommand(bootstrapCmd)
	root.SetArgs([]string{"bootstrap", "--show-prereqs"})
	root.SetOut(&buf)
	root.SetErr(&buf)

	err := root.Execute()
	if err != nil {
		t.Errorf("expected no error from --show-prereqs with blank org; got: %v\noutput: %s", err, buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "accounts.organization") {
		t.Errorf("expected 'accounts.organization' in show-prereqs output; got:\n%s", out)
	}
}
