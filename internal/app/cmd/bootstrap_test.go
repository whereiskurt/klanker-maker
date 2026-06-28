package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// makeFakeTerragruntForBootstrap installs a sleeping fake terragrunt on PATH
// so defaultApplyTerragrunt's runner spawns it instead of the real binary.
// Mirrors makeFakeTerragruntBin in pkg/terragrunt/runner_test.go.
//
// Phase 84.1-02 H6: used to exercise BootstrapApplyTimeout without invoking
// real terragrunt.
func makeFakeTerragruntForBootstrap(t *testing.T, body string) {
	t.Helper()
	binDir := t.TempDir()
	script := "#!/bin/sh\n" + body + "\n"
	path := filepath.Join(binDir, "terragrunt")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake terragrunt: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// withShortBootstrapTimeout overrides cmd.BootstrapApplyTimeout for the
// duration of the test and restores it on cleanup. Tests use this so the RED
// path doesn't have to wait 10 real minutes for the default to fire.
func withShortBootstrapTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	orig := cmd.BootstrapApplyTimeout
	cmd.BootstrapApplyTimeout = d
	t.Cleanup(func() { cmd.BootstrapApplyTimeout = orig })
}

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

// --- Phase 84.1 plan 01 Task 2: end-to-end env-var assertions ---------------
//
// These tests verify GAP-1 / GAP-7 closure at the bootstrap level: by the time
// runBootstrapSharedSES returns, every env var that site.hcl get_env() consults
// must be exported. The third test is the "strict superset" canary — its
// asserted-vars list MUST stay in sync with infra/live/**/terragrunt.hcl
// get_env(...) calls.
//
// All three drive RunBootstrapSharedSES via the test seam, with dryRun=true and
// a mockSESIdentityLister so no AWS call fires and no terragrunt subprocess runs.

// bootstrapSharedSESCfg builds a representative cfg with every env-var-relevant
// field populated. Reused across the three tests below.
func bootstrapSharedSESCfg() *config.Config {
	return &config.Config{
		OrganizationAccountID: "111111111111",
		DNSParentAccountID:    "222222222222",
		ApplicationAccountID:  "333333333333",
		Domain:                "test.example.com",
		PrimaryRegion:         "us-east-1",
		ArtifactsBucket:       "km-artifacts-12345",
		Route53ZoneID:         "Z08522462XE7ANTIK5FTX",
		ResourcePrefix:        "km",
		EmailSubdomain:        "sandboxes",
		OperatorEmail:         "operator-km@sandboxes.test.example.com",
	}
}

// clearTerragruntEnv clears every env var that ExportTerragruntEnvVars writes,
// using t.Setenv so cleanup happens automatically at test end.
func clearTerragruntEnv(t *testing.T) {
	t.Helper()
	vars := []string{
		"KM_ARTIFACTS_BUCKET",
		"KM_ACCOUNTS_ORGANIZATION",
		"KM_ACCOUNTS_DNS_PARENT",
		"KM_ACCOUNTS_APPLICATION",
		"KM_DOMAIN",
		"KM_REGION",
		"KM_REGION_LABEL",
		"KM_OPERATOR_EMAIL",
		"KM_SCHEDULER_ROLE_ARN",
		"KM_RESOURCE_PREFIX",
		"KM_EMAIL_SUBDOMAIN",
		"KM_ROUTE53_ZONE_ID",
	}
	for _, v := range vars {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
}

// TestRunBootstrapSharedSES_ExportsKMRoute53ZoneID is the GAP-1 closure test:
// km bootstrap must export KM_ROUTE53_ZONE_ID before any terragrunt invocation
// so the foundation MX/DKIM/verification record applies can resolve the zone.
func TestRunBootstrapSharedSES_ExportsKMRoute53ZoneID(t *testing.T) {
	clearTerragruntEnv(t)

	cfg := bootstrapSharedSESCfg()
	mock := &mockSESIdentityLister{} // both rule set + identity absent → dry-run-safe

	err := cmd.RunBootstrapSharedSES(context.Background(), cfg, true, io.Discard, mock)
	if err != nil {
		t.Fatalf("RunBootstrapSharedSES dry-run: %v", err)
	}

	if got := os.Getenv("KM_ROUTE53_ZONE_ID"); got != "Z08522462XE7ANTIK5FTX" {
		t.Errorf("KM_ROUTE53_ZONE_ID = %q, want %q (GAP-1 regression)", got, "Z08522462XE7ANTIK5FTX")
	}
}

// TestRunBootstrapSharedSES_ExportsKMArtifactsBucket is the GAP-7 closure test:
// km bootstrap must export KM_ARTIFACTS_BUCKET so direct-terragrunt operator
// workflows produce well-formed S3 ARNs.
func TestRunBootstrapSharedSES_ExportsKMArtifactsBucket(t *testing.T) {
	clearTerragruntEnv(t)

	cfg := bootstrapSharedSESCfg()
	mock := &mockSESIdentityLister{}

	err := cmd.RunBootstrapSharedSES(context.Background(), cfg, true, io.Discard, mock)
	if err != nil {
		t.Fatalf("RunBootstrapSharedSES dry-run: %v", err)
	}

	if got := os.Getenv("KM_ARTIFACTS_BUCKET"); got != "km-artifacts-12345" {
		t.Errorf("KM_ARTIFACTS_BUCKET = %q, want %q (GAP-7 regression)", got, "km-artifacts-12345")
	}
}

// TestRunBootstrapSharedSES_ExportsAllSiteHCLVars is the strict-superset canary.
// Must remain in sync with infra/live/**/terragrunt.hcl get_env(...) calls.
// If a new env var is added to site.hcl, add it here and to ExportTerragruntEnvVars.
func TestRunBootstrapSharedSES_ExportsAllSiteHCLVars(t *testing.T) {
	clearTerragruntEnv(t)

	cfg := bootstrapSharedSESCfg()
	mock := &mockSESIdentityLister{}

	err := cmd.RunBootstrapSharedSES(context.Background(), cfg, true, io.Discard, mock)
	if err != nil {
		t.Fatalf("RunBootstrapSharedSES dry-run: %v", err)
	}

	// Must remain in sync with infra/live/**/terragrunt.hcl get_env(...) calls.
	required := []string{
		"KM_ACCOUNTS_APPLICATION",
		"KM_ACCOUNTS_DNS_PARENT",
		"KM_ACCOUNTS_ORGANIZATION",
		"KM_ARTIFACTS_BUCKET",
		"KM_DOMAIN",
		"KM_EMAIL_SUBDOMAIN",
		"KM_REGION",
		"KM_REGION_LABEL",
		"KM_RESOURCE_PREFIX",
		"KM_ROUTE53_ZONE_ID",
	}

	for _, v := range required {
		if got := os.Getenv(v); got == "" {
			t.Errorf("%s not exported by RunBootstrapSharedSES (strict-superset violation)", v)
		}
	}
}

// ---- Phase 84.1-02 Task 3 (H6): bootstrap apply timeout tests ----

// TestDefaultApplyTerragrunt_HonorsContextTimeout verifies that the bootstrap
// apply path (defaultApplyTerragrunt, reached via the package-level
// ApplyTerragruntFunc default) is bounded by BootstrapApplyTimeout. Without
// this bound, a wedged terragrunt during `km bootstrap --shared-ses` hangs
// indefinitely just like the km init regression in 84-10-UAT.md (GAP-4/5).
//
// Plan-checker revision 1 H6: this is the bootstrap-side mirror of Task 2's
// init-side per-module timeout.
func TestDefaultApplyTerragrunt_HonorsContextTimeout(t *testing.T) {
	// "exec sleep 30" replaces the shell with sleep so SIGINT reaches sleep
	// directly; sleep exits immediately on SIGINT, avoiding the 5s WaitDelay
	// kill-grace in pkg/terragrunt/runner.go that "sleep 30" (without exec)
	// would incur while the shell ignores SIGINT waiting for its child.
	makeFakeTerragruntForBootstrap(t, "exec sleep 30")
	withShortBootstrapTimeout(t, 200*time.Millisecond)

	// Use a tmpdir as the apply target — terragrunt is faked anyway.
	dir := t.TempDir()

	start := time.Now()
	err := cmd.ApplyTerragruntFunc(context.Background(), dir)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error from wedged terragrunt, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected error wrapping context.DeadlineExceeded, got: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Errorf("defaultApplyTerragrunt blocked for %s — expected to return within seconds of the 200ms BootstrapApplyTimeout", elapsed)
	}
}

// TestRunBootstrapSharedSES_HonorsBootstrapTimeout verifies the end-to-end
// flow: km bootstrap --shared-ses with dryRun=false invokes
// ApplyTerragruntFunc, which is bounded by BootstrapApplyTimeout.
func TestRunBootstrapSharedSES_HonorsBootstrapTimeout(t *testing.T) {
	// "exec sleep 30" replaces the shell with sleep so SIGINT reaches sleep
	// directly; sleep exits immediately on SIGINT, avoiding the 5s WaitDelay
	// kill-grace in pkg/terragrunt/runner.go that "sleep 30" (without exec)
	// would incur while the shell ignores SIGINT waiting for its child.
	makeFakeTerragruntForBootstrap(t, "exec sleep 30")
	withShortBootstrapTimeout(t, 200*time.Millisecond)
	clearTerragruntEnv(t)

	cfg := bootstrapSharedSESCfg()
	mock := &mockSESIdentityLister{} // dry-run-safe — rule set + identity absent

	start := time.Now()
	err := cmd.RunBootstrapSharedSES(context.Background(), cfg, false, io.Discard, mock)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error from wedged terragrunt, got nil")
	}
	// Error string must mention either "deadline exceeded" or "wedged" so an
	// operator reading km bootstrap output can diagnose the hang.
	msg := err.Error()
	if !strings.Contains(msg, "deadline exceeded") && !strings.Contains(msg, "wedged") {
		t.Errorf("expected error to mention 'deadline exceeded' or 'wedged', got: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Errorf("RunBootstrapSharedSES blocked for %s — expected to return within seconds of the 200ms BootstrapApplyTimeout", elapsed)
	}
}

// TestRunBootstrap_WritesRegionHCL verifies Phase 84.4.1 BOOTSTRAP-REGION-HCL-PREREQ:
// runBootstrap (the plain SCP+KMS+artifacts subflow) must call ensureRegionHCL so
// that fresh clones don't hit `read_terragrunt_config "../region.hcl"` errors.
//
// Wave 0: scaffolding only. Wave 2 plan 84.4.1-04 adds the ensureRegionHCL call.
func TestRunBootstrap_WritesRegionHCL(t *testing.T) {
	// Phase 84.4.1 BOOTSTRAP-REGION-HCL-PREREQ: verify bootstrap.go contains
	// the ensureRegionHCL call in the runBootstrap function.
	//
	// The unit test of ensureRegionHCL itself lives in bootstrap_84_4_1_test.go
	// (package cmd, has access to unexported helpers).
	src, err := os.ReadFile(filepath.Join(".", "bootstrap.go"))
	if err != nil {
		t.Fatalf("read bootstrap.go: %v", err)
	}
	if !bytes.Contains(src, []byte("ensureRegionHCL")) {
		t.Errorf("bootstrap.go does not reference ensureRegionHCL — Phase 84.4.1 BOOTSTRAP-REGION-HCL-PREREQ not applied")
	}
}

// TestRunBootstrapSharedSES_WritesRegionHCL verifies the same prereq for the --shared-ses path.
//
// Wave 2 plan 84.4.1-04: source-grep confirming runBootstrapSharedSES calls ensureRegionHCL.
func TestRunBootstrapSharedSES_WritesRegionHCL(t *testing.T) {
	// Phase 84.4.1 BOOTSTRAP-REGION-HCL-PREREQ: verify bootstrap.go contains
	// the ensureRegionHCL call reachable from runBootstrapSharedSES.
	//
	// The unit test of ensureRegionHCL itself lives in bootstrap_84_4_1_test.go.
	src, err := os.ReadFile(filepath.Join(".", "bootstrap.go"))
	if err != nil {
		t.Fatalf("read bootstrap.go: %v", err)
	}
	if !bytes.Contains(src, []byte("ensureRegionHCL")) {
		t.Errorf("bootstrap.go does not reference ensureRegionHCL — Phase 84.4.1 BOOTSTRAP-REGION-HCL-PREREQ not applied to runBootstrapSharedSES")
	}
}
