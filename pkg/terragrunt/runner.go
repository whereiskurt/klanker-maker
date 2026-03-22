// Package terragrunt provides a runner for executing Terragrunt commands
// with real-time stdout/stderr streaming and environment isolation.
package terragrunt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// Runner wraps the Terragrunt binary and executes commands in sandbox directories.
// Apply and Destroy stream output in real time to os.Stdout/os.Stderr.
// The AWS_PROFILE env var is injected for every command.
type Runner struct {
	AWSProfile string // e.g. "klanker-terraform"
	RepoRoot   string // absolute path to repo root (anchored by CLAUDE.md)
}

// NewRunner returns a Runner configured with the given AWS profile and repo root.
func NewRunner(awsProfile, repoRoot string) *Runner {
	return &Runner{AWSProfile: awsProfile, RepoRoot: repoRoot}
}

// Apply runs `terragrunt apply -auto-approve` inside sandboxDir, streaming
// stdout and stderr in real time to the caller's terminal.
func (r *Runner) Apply(ctx context.Context, sandboxDir string) error {
	cmd := r.BuildApplyCommand(ctx, sandboxDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Destroy runs `terragrunt destroy -auto-approve` inside sandboxDir, streaming
// stdout and stderr in real time to the caller's terminal.
func (r *Runner) Destroy(ctx context.Context, sandboxDir string) error {
	cmd := r.BuildDestroyCommand(ctx, sandboxDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Output runs `terragrunt output -json` inside sandboxDir, captures the output,
// and parses it as a JSON map. Returns the parsed key-value map.
func (r *Runner) Output(ctx context.Context, sandboxDir string) (map[string]interface{}, error) {
	cmd := r.BuildOutputCommand(ctx, sandboxDir)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("terragrunt output: %w", err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse terragrunt output JSON: %w", err)
	}
	return result, nil
}

// BuildApplyCommand constructs the exec.Cmd for `terragrunt apply -auto-approve`
// without running it. Used directly by Apply() and exposed for testing.
func (r *Runner) BuildApplyCommand(ctx context.Context, sandboxDir string) *exec.Cmd {
	return r.buildCommand(ctx, sandboxDir, "apply", "-auto-approve")
}

// BuildDestroyCommand constructs the exec.Cmd for `terragrunt destroy -auto-approve`
// without running it. Used directly by Destroy() and exposed for testing.
func (r *Runner) BuildDestroyCommand(ctx context.Context, sandboxDir string) *exec.Cmd {
	return r.buildCommand(ctx, sandboxDir, "destroy", "-auto-approve")
}

// BuildOutputCommand constructs the exec.Cmd for `terragrunt output -json`
// without running it. Used directly by Output() and exposed for testing.
func (r *Runner) BuildOutputCommand(ctx context.Context, sandboxDir string) *exec.Cmd {
	return r.buildCommand(ctx, sandboxDir, "output", "-json")
}

// buildCommand is the internal factory that constructs a Terragrunt command
// with the correct working directory and environment.
func (r *Runner) buildCommand(ctx context.Context, sandboxDir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "terragrunt", args...) //nolint:gosec // terragrunt binary is a fixed, trusted binary
	cmd.Dir = sandboxDir
	cmd.Env = append(os.Environ(), "AWS_PROFILE="+r.AWSProfile)
	return cmd
}
