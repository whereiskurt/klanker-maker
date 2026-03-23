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
// when ManagementAccountID and ApplicationAccountID are both populated.
func TestBootstrapDryRunShowsSCP(t *testing.T) {
	cfg := &config.Config{
		ManagementAccountID:  "123456789012",
		ApplicationAccountID: "987654321098",
		PrimaryRegion:        "us-east-1",
		Domain:               "test.example.com",
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
// warning when ManagementAccountID is empty.
func TestBootstrapDryRunNoManagementAccount(t *testing.T) {
	cfg := &config.Config{
		ManagementAccountID:  "",
		ApplicationAccountID: "987654321098",
		PrimaryRegion:        "us-east-1",
		Domain:               "test.example.com",
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
		ManagementAccountID:  "123456789012",
		ApplicationAccountID: "987654321098",
		PrimaryRegion:        "us-east-1",
		Domain:               "test.example.com",
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
