// Package terragrunt provides a runner for executing Terragrunt commands
// with real-time stdout/stderr streaming and environment isolation.
package terragrunt

import (
	"bytes"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runner wraps the Terragrunt binary and executes commands in sandbox directories.
// Apply and Destroy stream output in real time to os.Stdout/os.Stderr when Verbose
// is true, or capture output quietly (showing only errors/warnings) when Verbose is false.
// The AWS_PROFILE env var is injected for every command.
//
// Phase 84.1-02 (GAP-4 + GAP-5): in quiet mode, the runner emits a periodic
// heartbeat every ProgressInterval (default 15s) so a wedged terragrunt
// surfaces as visible progress instead of a silent multi-minute hang. Callers
// that pass a bounded context.Context get a context.DeadlineExceeded error
// when the deadline trips — the spawned terragrunt process is SIGTERM'd
// (cmd.Cancel) and then SIGKILL'd after a short WaitDelay.
type Runner struct {
	AWSProfile       string        // e.g. "klanker-terraform"
	RepoRoot         string        // absolute path to repo root (anchored by CLAUDE.md)
	Verbose          bool          // when false (default), suppress raw output; when true, stream to terminal
	ProgressInterval time.Duration // Phase 84.1-02: heartbeat cadence in quiet mode. Zero disables.
	ProgressWriter   io.Writer     // Phase 84.1-02: where heartbeat ticks are written. Nil => os.Stderr.
}

// DefaultProgressInterval is the default heartbeat cadence for quiet-mode runs.
// Long enough to avoid spamming logs during normal applies (seconds-to-minutes),
// short enough to give operators useful feedback on multi-minute applies.
const DefaultProgressInterval = 15 * time.Second

// NewRunner returns a Runner configured with the given AWS profile and repo root.
// Phase 84.1-02: ProgressInterval defaults to DefaultProgressInterval so every
// production caller picks up the heartbeat behaviour automatically.
func NewRunner(awsProfile, repoRoot string) *Runner {
	return &Runner{
		AWSProfile:       awsProfile,
		RepoRoot:         repoRoot,
		ProgressInterval: DefaultProgressInterval,
	}
}

// Apply runs `terragrunt apply -auto-approve` inside sandboxDir.
// When Verbose is true, stdout and stderr are streamed in real time to the terminal.
// When Verbose is false (default), output is captured; on failure the captured stderr
// is printed so errors are always visible. Warnings are always printed.
//
// Phase 84.1-02: honours ctx deadline/cancellation and emits a heartbeat in
// quiet mode (see Runner doc). A bounded ctx returns context.DeadlineExceeded
// (wrapped) when the deadline trips.
func (r *Runner) Apply(ctx context.Context, sandboxDir string) error {
	cmd := r.BuildApplyCommand(ctx, sandboxDir)
	return r.runCommand(ctx, cmd)
}

// Destroy runs `terragrunt destroy -auto-approve` inside sandboxDir.
// When Verbose is true, stdout and stderr are streamed in real time to the terminal.
// When Verbose is false (default), output is captured; on failure the captured stderr
// is printed so errors are always visible. Warnings are always printed.
func (r *Runner) Destroy(ctx context.Context, sandboxDir string) error {
	cmd := r.BuildDestroyCommand(ctx, sandboxDir)
	return r.runCommand(ctx, cmd)
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

// BuildPlanWithOutputCommand constructs the exec.Cmd for `terragrunt plan -out=<planFile>`
// without running it. Used directly by PlanWithOutput() and exposed for command-construction tests.
//
// The planFile path MUST be absolute — see Pitfall 1 in
// .planning/phases/84.2-.../84.2-RESEARCH.md: terragrunt resolves relative paths
// inside .terragrunt-cache/<hash>/<module>/, which the caller cannot easily discover.
// os.CreateTemp("", "...") returns absolute paths on macOS/Linux (the only platforms km targets).
func (r *Runner) BuildPlanWithOutputCommand(ctx context.Context, sandboxDir, planFile string) *exec.Cmd {
	return r.buildCommand(ctx, sandboxDir, "plan", "-out="+planFile)
}

// BuildShowPlanJSONCommand constructs the exec.Cmd for `terragrunt show -json <planFile>`
// without running it. Used by ShowPlanJSON; exposed for command-construction tests.
// Invoked via terragrunt (not terraform directly) so the .terragrunt-cache resolution
// is consistent with BuildPlanWithOutputCommand.
func (r *Runner) BuildShowPlanJSONCommand(ctx context.Context, sandboxDir, planFile string) *exec.Cmd {
	return r.buildCommand(ctx, sandboxDir, "show", "-json", planFile)
}

// Reconfigure runs `terragrunt init -reconfigure` inside sandboxDir to refresh
// the local .terragrunt-cache backend metadata. Used before destroy when the
// backend bucket name resolves differently than when state was last written
// (e.g. KM_RESOURCE_PREFIX env var changed between apply and destroy because
// the operator upgraded km past Phase 66's resource_prefix introduction or
// past commit 504b4dd's slack-init env-var fix). Without -reconfigure,
// terragrunt's auto-init on destroy hits "Backend configuration block has
// changed" and refuses to proceed.
//
// Safe to call when no reconfigure is needed — it's a no-op in that case.
func (r *Runner) Reconfigure(ctx context.Context, sandboxDir string) error {
	cmd := r.buildCommand(ctx, sandboxDir, "init", "-reconfigure")
	return r.runCommand(ctx, cmd)
}

// Plan runs `terragrunt plan` inside sandboxDir for dry-run preview without
// mutating state. Used by km cluster add --dry-run=true so operators can review
// the IAM role that WOULD be created before flipping --dry-run=false.
//
// Honors r.Verbose the same way Apply does: streams output when true, captures
// and prints warnings/errors when false. No -auto-approve flag (plan is read-only).
func (r *Runner) Plan(ctx context.Context, sandboxDir string) error {
	cmd := r.buildCommand(ctx, sandboxDir, "plan")
	return r.runCommand(ctx, cmd)
}

// PlanWithOutput runs `terragrunt plan -out=<planFile>` inside sandboxDir.
//
// Stdout is captured into stdoutBuf (for optional --verbose echo by the caller —
// typically internal/app/cmd/runInitPlan and runBootstrapSharedSESPlan in Phase 84.2).
// Stderr handling matches Apply: when r.Verbose, stderr streams to os.Stderr; when
// r.Verbose is false, stderr is captured and only printed on failure (matches the
// OPER-01 quiet-by-default contract).
//
// Phase 84.1-02: inherits the runner-layer ctx deadline + heartbeat via runBounded.
// A bounded ctx returns context.DeadlineExceeded (wrapped) when the deadline trips —
// do NOT add a local context.WithTimeout here (would double-wrap and confuse
// ModuleTimeoutFunc bookkeeping in init.go).
//
// The planFile path MUST be absolute. Pass nil for stdoutBuf to discard stdout.
func (r *Runner) PlanWithOutput(ctx context.Context, sandboxDir, planFile string, stdoutBuf *bytes.Buffer) error {
	cmd := r.BuildPlanWithOutputCommand(ctx, sandboxDir, planFile)
	if stdoutBuf == nil {
		stdoutBuf = &bytes.Buffer{} // discard
	}
	// Always capture stdout to the caller-supplied buffer (independent of r.Verbose
	// — callers decide later whether to echo the buffer to the terminal).
	cmd.Stdout = stdoutBuf
	var stderrBuf bytes.Buffer
	if r.Verbose {
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	} else {
		cmd.Stderr = &stderrBuf
	}
	err := r.runBounded(ctx, cmd)
	if err != nil && !r.Verbose && stderrBuf.Len() > 0 {
		// Surface terragrunt's stderr on failure when quiet mode would have hidden it.
		fmt.Fprintln(os.Stderr, stderrBuf.String())
	}
	return err
}

// ShowPlanJSON runs `terragrunt show -json <planFile>` inside sandboxDir and returns
// the JSON bytes for the caller to parse (typically via
// pkg/terragrunt/planreport.Parse in Phase 84.2 init/bootstrap plan wiring).
//
// Uses cmd.Output() (not runBounded) for the same reason Output() does — we need
// clean stdout-only bytes for the JSON parser. On non-zero exit, the returned error
// is *exec.ExitError; its .Stderr field holds the terragrunt stderr so callers can
// surface it with the module name.
//
// The planFile path MUST be absolute (same Pitfall 1 reasoning as PlanWithOutput).
func (r *Runner) ShowPlanJSON(ctx context.Context, sandboxDir, planFile string) ([]byte, error) {
	cmd := r.BuildShowPlanJSONCommand(ctx, sandboxDir, planFile)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("terragrunt show -json: %w", err)
	}
	return out, nil
}

// ApplyWithStderr runs apply, capturing stderr to the provided buffer (for error detection).
// When Verbose is true: stdout streams to terminal, stderr streams to both terminal and stderrBuf.
// When Verbose is false: stdout is captured, stderr goes to stderrBuf and is printed on failure.
//
// Phase 84.1-02: same ctx + heartbeat treatment as runCommand.
func (r *Runner) ApplyWithStderr(ctx context.Context, sandboxDir string, stderrBuf *strings.Builder) error {
	cmd := r.BuildApplyCommand(ctx, sandboxDir)
	if r.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = io.MultiWriter(os.Stderr, stderrBuf)
		return r.runBounded(ctx, cmd)
	}
	var stdoutBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = stderrBuf
	err := r.runBounded(ctx, cmd)
	if err != nil {
		printWarningsAndErrors(stderrBuf.String())
	}
	return err
}

// DestroyWithStderr runs destroy, capturing stderr to the provided buffer (for lock error detection).
// When Verbose is true: stdout streams to terminal, stderr streams to both terminal and stderrBuf.
// When Verbose is false: stdout is captured (discarded on success), stderr goes to both
// stderrBuf and is printed on failure so errors are always visible.
//
// Phase 84.1-02: same ctx + heartbeat treatment as runCommand.
func (r *Runner) DestroyWithStderr(ctx context.Context, sandboxDir string, stderrBuf *strings.Builder) error {
	cmd := r.BuildDestroyCommand(ctx, sandboxDir)
	if r.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = io.MultiWriter(os.Stderr, stderrBuf)
		return r.runBounded(ctx, cmd)
	}
	// Quiet mode: capture stdout, send stderr to buffer only
	var stdoutBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = stderrBuf
	err := r.runBounded(ctx, cmd)
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
	return r.runCommand(ctx, cmd)
}

// runCommand executes the command in verbose or quiet mode based on r.Verbose.
// In verbose mode: streams stdout and stderr directly to the terminal.
// In quiet mode: captures stdout and stderr; on failure prints captured stderr;
// warnings from stderr are always printed regardless of success.
//
// Phase 84.1-02: ctx is honoured (deadline/cancel triggers SIGTERM then SIGKILL
// via cmd.Cancel + cmd.WaitDelay) and a quiet-mode heartbeat is emitted at
// r.ProgressInterval. When ctx.Err() is non-nil after cmd.Wait returns, the
// runner wraps it so callers can errors.Is-detect context.DeadlineExceeded /
// context.Canceled instead of seeing the raw "signal: killed" exec.ExitError.
func (r *Runner) runCommand(ctx context.Context, cmd *exec.Cmd) error {
	if r.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return r.runBounded(ctx, cmd)
	}
	// Quiet mode: capture output
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := r.runBounded(ctx, cmd)
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

// runBounded starts cmd, watches for ctx cancellation, fires a quiet-mode
// heartbeat, and waits for completion. Returns ctx.Err() (wrapped) when ctx
// caused the kill so callers can errors.Is-detect DeadlineExceeded/Canceled.
//
// Phase 84.1-02 (GAP-4 + GAP-5): centralises the bounded-execution semantics
// for Apply / Destroy / Reconfigure / Plan / *WithStderr / DestroyForceUnlock.
func (r *Runner) runBounded(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}

	// Quiet-mode heartbeat: tick every ProgressInterval until cmd exits or ctx
	// fires. Verbose mode already streams raw output so no heartbeat is needed.
	heartbeatDone := make(chan struct{})
	if !r.Verbose && r.ProgressInterval > 0 {
		w := r.ProgressWriter
		if w == nil {
			w = os.Stderr
		}
		go runHeartbeat(ctx, heartbeatDone, cmd, r.ProgressInterval, w)
	}

	waitErr := cmd.Wait()
	close(heartbeatDone)

	// When ctx fires, exec.CommandContext signals the process (via cmd.Cancel
	// or default Kill) and cmd.Wait returns "signal: killed" or similar. Surface
	// the ctx error so callers can errors.Is-detect timeouts vs cancellations
	// vs real terragrunt failures.
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return fmt.Errorf("terragrunt %s: %w (process killed after timeout)", cmdName(cmd), ctxErr)
		}
		if errors.Is(ctxErr, context.Canceled) {
			return fmt.Errorf("terragrunt %s: %w", cmdName(cmd), ctxErr)
		}
	}
	return waitErr
}

// runHeartbeat writes a "... still running" tick to w every interval, until
// either ctx fires or done is closed. Includes elapsed time and the child PID
// so operators can correlate with `ps` / aws-cli observations during a hang.
func runHeartbeat(ctx context.Context, done <-chan struct{}, cmd *exec.Cmd, interval time.Duration, w io.Writer) {
	start := time.Now()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			pid := 0
			if cmd.Process != nil {
				pid = cmd.Process.Pid
			}
			fmt.Fprintf(w, "  ... still running (%s elapsed, pid=%d)\n",
				t.Sub(start).Round(time.Second), pid)
		}
	}
}

// cmdName returns the first non-binary arg (the terragrunt subcommand) so
// error messages read as "terragrunt apply: ..." instead of "terragrunt: ...".
// Falls back to the binary path if no subcommand is present.
func cmdName(cmd *exec.Cmd) string {
	if len(cmd.Args) >= 2 {
		return cmd.Args[1]
	}
	return cmd.Path
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
//
// TG_BACKEND_BOOTSTRAP=true is set unconditionally (unless the operator already
// exported it) so the first `km init` against a fresh AWS account auto-creates
// the S3 backend bucket. Older Terragrunt versions auto-bootstrapped on first
// apply; v0.69+ requires the explicit opt-in. The flag is a no-op when the
// bucket already exists, so it's safe for every subsequent apply/destroy/output.
//
// TG_NON_INTERACTIVE=true pairs with --backend-bootstrap: the bootstrap flag
// only enables the *capability*, terragrunt still prompts "Would you like
// Terragrunt to create it? (y/n)" by default. Without TG_NON_INTERACTIVE,
// km init dies on EOF when nothing is on stdin. Every km command driving the
// runner is non-interactive anyway, so this is safe to default on.
func (r *Runner) buildCommand(ctx context.Context, sandboxDir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "terragrunt", args...) //nolint:gosec // terragrunt binary is a fixed, trusted binary
	cmd.Dir = sandboxDir
	cmd.Env = append(os.Environ(), "AWS_PROFILE="+r.AWSProfile)
	if os.Getenv("TG_BACKEND_BOOTSTRAP") == "" {
		cmd.Env = append(cmd.Env, "TG_BACKEND_BOOTSTRAP=true")
	}
	if os.Getenv("TG_NON_INTERACTIVE") == "" {
		cmd.Env = append(cmd.Env, "TG_NON_INTERACTIVE=true")
	}
	// Phase 84.1-02: graceful shutdown on ctx cancel — send os.Interrupt first
	// (SIGINT on Unix, CTRL_BREAK_EVENT on Windows; per L15 from plan-checker
	// rev 1, do NOT use syscall.SIGTERM which is non-portable). WaitDelay
	// escalates to SIGKILL if the child doesn't exit cleanly within 5s so
	// Ctrl-C feels responsive even when terragrunt holds state locks.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 5 * time.Second
	return cmd
}
