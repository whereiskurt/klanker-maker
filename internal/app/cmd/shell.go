package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ShellExecFunc is the function signature for executing the AWS CLI subprocess.
// It is package-level so tests can replace it to capture args without executing.
type ShellExecFunc func(c *exec.Cmd) error

// defaultShellExec calls cmd.Run() — the real subprocess execution path.
func defaultShellExec(c *exec.Cmd) error {
	return c.Run()
}

// NewShellCmd creates the "km shell" subcommand using the real AWS-backed fetcher.
// Usage: km shell <sandbox-id>
func NewShellCmd(cfg *config.Config) *cobra.Command {
	return NewShellCmdWithFetcher(cfg, nil, nil)
}

// NewShellCmdWithFetcher builds the shell command with an optional custom fetcher and
// exec function. Pass nil for real AWS-backed clients. Used in tests for DI.
func NewShellCmdWithFetcher(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "shell <sandbox-id>",
		Short:        "Open an interactive shell into a running sandbox",
		Long:         helpText("shell"),
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShell(cmd, cfg, fetcher, execFn, args[0])
		},
	}
	return cmd
}

// runShell is the command RunE logic for km shell.
func runShell(cmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, sandboxID string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if fetcher == nil {
		if cfg.StateBucket == "" {
			return fmt.Errorf("state bucket not configured: set KM_STATE_BUCKET or state_bucket in km-config.yaml")
		}
		awsProfile := "klanker-terraform"
		awsCfg, err := kmaws.LoadAWSConfig(ctx, awsProfile)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket)
	}

	if execFn == nil {
		execFn = defaultShellExec
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}

	if rec.Status == "stopped" {
		return fmt.Errorf("sandbox %s is stopped — start it with 'km budget add %s --compute <amount>' first", sandboxID, sandboxID)
	}

	switch rec.Substrate {
	case "ec2":
		instanceID, err := extractResourceID(rec.Resources, ":instance/")
		if err != nil {
			return fmt.Errorf("find EC2 instance for sandbox %s: %w", sandboxID, err)
		}
		return execSSMSession(ctx, instanceID, rec.Region, execFn)
	case "ecs":
		clusterARN, err := findResourceARN(rec.Resources, ":cluster/")
		if err != nil {
			return fmt.Errorf("find ECS cluster for sandbox %s: %w", sandboxID, err)
		}
		taskARN, err := findResourceARN(rec.Resources, ":task/")
		if err != nil {
			return fmt.Errorf("find ECS task for sandbox %s: %w", sandboxID, err)
		}
		return execECSCommand(ctx, clusterARN, taskARN, rec.Region, execFn)
	default:
		return fmt.Errorf("unsupported substrate %q for km shell", rec.Substrate)
	}
}

// execSSMSession builds and runs: aws ssm start-session --target <instanceID> --region <region>
func execSSMSession(ctx context.Context, instanceID, region string, execFn ShellExecFunc) error {
	c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
		"--target", instanceID, "--region", region)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return execFn(c)
}

// execECSCommand builds and runs:
// aws ecs execute-command --cluster <clusterARN> --task <taskARN>
// --interactive --command /bin/bash --region <region>
func execECSCommand(ctx context.Context, clusterARN, taskARN, region string, execFn ShellExecFunc) error {
	c := exec.CommandContext(ctx, "aws", "ecs", "execute-command",
		"--cluster", clusterARN, "--task", taskARN,
		"--interactive", "--command", "/bin/bash", "--region", region)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return execFn(c)
}

// extractResourceID finds an ARN containing pattern and extracts the resource ID
// portion after the last "/". Example: "arn:....:instance/i-0abc123" -> "i-0abc123".
func extractResourceID(resources []string, pattern string) (string, error) {
	arn, err := findResourceARN(resources, pattern)
	if err != nil {
		return "", err
	}
	parts := strings.Split(arn, "/")
	return parts[len(parts)-1], nil
}

// findResourceARN returns the first ARN in resources that contains pattern.
func findResourceARN(resources []string, pattern string) (string, error) {
	for _, arn := range resources {
		if strings.Contains(arn, pattern) {
			return arn, nil
		}
	}
	return "", fmt.Errorf("no resource matching %q found in %v", pattern, resources)
}
