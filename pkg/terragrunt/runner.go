// Package terragrunt provides a runner for executing Terragrunt commands
// with real-time stdout/stderr streaming and environment isolation.
package terragrunt

import (
	"bytes"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Runner wraps the Terragrunt binary and executes commands in sandbox directories.
// Apply and Destroy stream output in real time to os.Stdout/os.Stderr when Verbose
// is true, or capture output quietly (showing only errors/warnings) when Verbose is false.
// The AWS_PROFILE env var is injected for every command.
type Runner struct {
	AWSProfile string // e.g. "klanker-terraform"
	RepoRoot   string // absolute path to repo root (anchored by CLAUDE.md)
	Verbose    bool   // when false (default), suppress raw output; when true, stream to terminal
}

// NewRunner returns a Runner configured with the given AWS profile and repo root.
func NewRunner(awsProfile, repoRoot string) *Runner {
	return &Runner{AWSProfile: awsProfile, RepoRoot: repoRoot}
}

// Apply runs `terragrunt apply -auto-approve` inside sandboxDir.
// When Verbose is true, stdout and stderr are streamed in real time to the terminal.
// When Verbose is false (default), output is captured; on failure the captured stderr
// is printed so errors are always visible. Warnings are always printed.
func (r *Runner) Apply(ctx context.Context, sandboxDir string) error {
	cmd := r.BuildApplyCommand(ctx, sandboxDir)
	return r.runCommand(cmd)
}

// Destroy runs `terragrunt destroy -auto-approve` inside sandboxDir.
// When Verbose is true, stdout and stderr are streamed in real time to the terminal.
// When Verbose is false (default), output is captured; on failure the captured stderr
// is printed so errors are always visible. Warnings are always printed.
func (r *Runner) Destroy(ctx context.Context, sandboxDir string) error {
	cmd := r.BuildDestroyCommand(ctx, sandboxDir)
	return r.runCommand(cmd)
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

// DestroyWithStderr runs destroy, capturing stderr to the provided buffer (for lock error detection).
// When Verbose is true: stdout streams to terminal, stderr streams to both terminal and stderrBuf.
// When Verbose is false: stdout is captured (discarded on success), stderr goes to both
// stderrBuf and is printed on failure so errors are always visible.
func (r *Runner) DestroyWithStderr(ctx context.Context, sandboxDir string, stderrBuf *strings.Builder) error {
	cmd := r.BuildDestroyCommand(ctx, sandboxDir)
	if r.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = io.MultiWriter(os.Stderr, stderrBuf)
		return cmd.Run()
	}
	// Quiet mode: capture stdout, send stderr to buffer only
	var stdoutBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = stderrBuf
	err := cmd.Run()
	if err != nil {
		// Always print stderr on failure so errors are visible
		printWarningsAndErrors(stderrBuf.String())
	}
	return err
}

// DestroyForceUnlock runs `terragrunt destroy -auto-approve` with `-lock=false`
// to bypass a stale state lock. Used after the operator confirms lock clearing.
func (r *Runner) DestroyForceUnlock(ctx context.Context, sandboxDir string) error {
	cmd := r.buildCommand(ctx, sandboxDir, "destroy", "-auto-approve", "-lock=false")
	return r.runCommand(cmd)
}

// runCommand executes the command in verbose or quiet mode based on r.Verbose.
// In verbose mode: streams stdout and stderr directly to the terminal.
// In quiet mode: captures stdout and stderr; on failure prints captured stderr;
// warnings from stderr are always printed regardless of success.
func (r *Runner) runCommand(cmd *exec.Cmd) error {
	if r.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	// Quiet mode: capture output
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	if err != nil {
		// Always print captured stderr on failure so errors are visible
		if stderrContent := stderrBuf.String(); stderrContent != "" {
			fmt.Fprint(os.Stderr, stderrContent)
		}
		return err
	}
	// On success: print any warnings from stderr
	if stderrContent := stderrBuf.String(); stderrContent != "" {
		printWarningsAndErrors(stderrContent)
	}
	return nil
}

// printWarningsAndErrors scans each line of output and prints lines that
// contain warning or error indicators to os.Stderr.
func printWarningsAndErrors(output string) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		lower := strings.ToLower(line)
		if strings.Contains(lower, "warning") || strings.Contains(lower, "warn") ||
			strings.Contains(lower, "error") {
			fmt.Fprintln(os.Stderr, line)
		}
	}
}

// buildCommand is the internal factory that constructs a Terragrunt command
// with the correct working directory and environment.
func (r *Runner) buildCommand(ctx context.Context, sandboxDir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "terragrunt", args...) //nolint:gosec // terragrunt binary is a fixed, trusted binary
	cmd.Dir = sandboxDir
	cmd.Env = append(os.Environ(), "AWS_PROFILE="+r.AWSProfile)
	return cmd
}
