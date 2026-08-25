package cmd_test

import (
	"strings"
	"testing"

	cmd "github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// enrolledCfg returns a config whose launch_accounts already carries the link
// baseAccountAddOpts uses, so RunAccountAdd hits the idempotency branch.
func enrolledCfg(name string) *config.Config {
	return &config.Config{
		LaunchAccounts: map[string]config.LaunchAccountConfig{
			name: {
				AccountID:       "222222222222",
				LauncherRoleARN: "arn:aws:iam::222222222222:role/km-gpu-launcher",
				BoxRoleARN:      "arn:aws:iam::222222222222:role/km-gpu-box",
			},
		},
	}
}

// Without --force an already-enrolled link short-circuits: no AWS calls, no
// terraform. This is the cheap-rerun guarantee.
func TestAccountAdd_AlreadyEnrolled_ExitsWithoutForce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	runner := &mockAccountRunner{}
	// FailIfCalled on both AWS doubles: the early exit must touch neither.
	installAccountSeams(t, runner, &mockLinkStateS3{FailIfCalled: t}, &mockLinkLockDynamoDB{FailIfCalled: t}, testCaller)

	var out strings.Builder
	opts := baseAccountAddOpts("mgmt-gpu")
	opts.DryRun = false // even a real run must not proceed
	if err := cmd.RunAccountAdd(enrolledCfg("mgmt-gpu"), opts, t.TempDir(), &out); err != nil {
		t.Fatalf("RunAccountAdd: %v", err)
	}
	if runner.PlanCalled || len(runner.Applied) > 0 {
		t.Error("an already-enrolled link must not re-run terraform without --force")
	}
	msg := out.String()
	if !strings.Contains(msg, "already enrolled") {
		t.Errorf("output must say the link is already enrolled; got:\n%s", msg)
	}
	// The message has to name the escape hatch, or the operator's only visible
	// options are tearing the link down or hand-editing IAM in the target account.
	if !strings.Contains(msg, "--force") {
		t.Errorf("output must point at --force; got:\n%s", msg)
	}
}

// With --force the module is re-applied, which is the only way to converge an
// existing link after a gpu-launcher-account module change (a widened launcher
// policy, a fixed tag condition, a new box-role grant).
func TestAccountAdd_AlreadyEnrolled_ForceReapplies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	runner := &mockAccountRunner{}
	installAccountSeams(t, runner, &mockLinkStateS3{}, &mockLinkLockDynamoDB{}, testCaller)

	var out strings.Builder
	opts := baseAccountAddOpts("mgmt-gpu")
	opts.DryRun = true // keep it off the backend path; still proves we got past the guard
	opts.Force = true
	if err := cmd.RunAccountAdd(enrolledCfg("mgmt-gpu"), opts, t.TempDir(), &out); err != nil {
		t.Fatalf("RunAccountAdd --force: %v", err)
	}
	if !runner.ValidateCalled {
		t.Error("--force must proceed into the render/validate path, not short-circuit")
	}
	if !strings.Contains(out.String(), "Re-applying") {
		t.Errorf("output must state that the link is being re-applied; got:\n%s", out.String())
	}
}
