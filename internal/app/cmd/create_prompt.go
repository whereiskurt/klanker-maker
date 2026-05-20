package cmd

// create_prompt.go — Phase 86 operator-side prompt-queue helpers.
//
// ReconcileMetaStatus mirrors the bash runner's startup reconcile step in Go.
// It returns the post-reconcile status for a meta.json status field:
//   - "running" → "pending"  (runner crashed/paused mid-execution; restart it)
//   - all others → unchanged (done/failed/skipped/pending are idempotent)
//
// These helpers are intentionally kept in a separate file so unit tests can
// exercise them without spinning up the full NewCreateCmd Cobra tree.
//
// Execution model (RESEARCH.md Pitfall #1):
//   - resolvePrompts runs OPERATOR-side, BEFORE any AWS call.
//   - pushQueueFiles + kickQueueRunner run OPERATOR-side AFTER runCreate /
//     runCreateRemote completes (sandbox is reachable at that point).
//   - The create-handler Lambda is UNTOUCHED — no Lambda redeploy required.

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// ExitCodeError carries a process exit code up the cobra call stack.
// The outermost command boundary (Execute() in root.go) detects this via
// errors.As and translates it into os.Exit(Code) AT the outermost boundary —
// never inside RunE — so any deferred cleanup registered by RunE or Cobra
// middleware runs to completion before the process exits.
//
// Why a typed error (not inline os.Exit):
//   - Inline os.Exit inside doStep16PromptPush bypasses any defer statements
//     registered higher in the call stack (Cobra's RunE wrappers, signal
//     handlers, telemetry flushes, AWS SDK connection cleanup).
//   - A typed error round-trips through RunE's error return value cleanly;
//     Cobra's normal error path runs all defers; the outermost layer (just
//     outside cmd.Execute()) inspects with errors.As and only then calls
//     os.Exit(code).
//   - This matches Cobra-community idioms (e.g. kubectl's cmdutil.CheckErr
//     + typed errors pattern).
type ExitCodeError struct {
	Code  int   // process exit code to propagate
	Inner error // optional wrapped underlying error
}

func (e *ExitCodeError) Error() string {
	if e.Inner != nil {
		return fmt.Sprintf("queue chain failed (exit code %d): %v", e.Code, e.Inner)
	}
	return fmt.Sprintf("queue chain failed (exit code %d)", e.Code)
}

func (e *ExitCodeError) Unwrap() error { return e.Inner }

// QueuePollInterval is the delay between successive meta.json status reads.
// Declared as an exported package-level var (not const) so tests in the
// cmd_test package can override it to a sub-millisecond value and avoid
// 5s-per-poll test latency. Production code uses the 5s default.
var QueuePollInterval = 5 * time.Second

// ResolvePrompts processes the raw --prompt values:
//   - "@@literal" → literal "@literal" (escape for operators who need a leading @)
//   - "@filepath" → UTF-8 contents of filepath (missing file → clear error)
//   - anything else → passed through unchanged
//
// Runs entirely operator-side before any SSM/AWS call. A missing file is a
// hard-fail: the operator sees the path in the error message.
//
// Exported for testability from the cmd_test package.
func ResolvePrompts(raw []string) ([]string, error) {
	return resolvePrompts(raw)
}

func resolvePrompts(raw []string) ([]string, error) {
	out := make([]string, len(raw))
	for i, v := range raw {
		switch {
		case strings.HasPrefix(v, "@@"):
			// @@ escape: strip one @, keep the rest as a literal string.
			out[i] = v[1:]
		case strings.HasPrefix(v, "@"):
			// @file: read the file verbatim, UTF-8.
			path := v[1:]
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("--prompt @%s: %w", path, err)
			}
			out[i] = string(data)
		default:
			out[i] = v
		}
	}
	return out, nil
}

// WaitForQueueDrain is the exported wrapper around waitForQueueDrain for cmd_test tests.
func WaitForQueueDrain(ctx context.Context, ssmClient SSMSendAPI, instanceID string, expectedCount int) (int, error) {
	return waitForQueueDrain(ctx, ssmClient, instanceID, expectedCount)
}

// PushQueueFiles is the exported wrapper around pushQueueFiles for cmd_test tests.
func PushQueueFiles(ctx context.Context, ssmClient SSMSendAPI, instanceID string, prompts []string, noBedrock bool) error {
	return pushQueueFiles(ctx, ssmClient, instanceID, prompts, noBedrock)
}

// pushQueueFiles serialises N prompts into /workspace/.km-agent/queue/ on the
// EC2 sandbox via a single atomic SSM RunShellScript invocation.
//
// File layout for each prompt[i]:
//
//	/workspace/.km-agent/queue/NNN.prompt     (0600, base64-decoded prompt text)
//	/workspace/.km-agent/queue/NNN.meta.json  (0600, JSON status record)
//
// NNN is zero-padded to 3 digits (001, 002, …).
// meta.json shape: {"no_bedrock":<bool>,"created_at":"<RFC3339>","status":"pending"}
//
// All files are base64-encoded in transit to avoid heredoc-nesting / shell-escaping
// issues with special characters inside prompt text (same pattern as BuildAgentShellCommands).
func pushQueueFiles(ctx context.Context, ssmClient SSMSendAPI, instanceID string, prompts []string, noBedrock bool) error {
	var sb strings.Builder
	sb.WriteString("set -eu\n")
	// Wait up to 60s for the `sandbox` user to exist — DynamoDB metadata flips to
	// "running" when EC2 reaches running state, but userdata's user-creation step
	// may still be in flight. Without this guard, chown -R sandbox:sandbox below
	// fails with "invalid user" on a race.
	sb.WriteString("for i in $(seq 1 30); do getent passwd sandbox >/dev/null && break; sleep 2; done\n")
	sb.WriteString("getent passwd sandbox >/dev/null || { echo 'sandbox user never appeared after 60s'; exit 1; }\n")
	sb.WriteString("mkdir -p /workspace/.km-agent/queue\n")
	sb.WriteString("chmod 0700 /workspace/.km-agent/queue\n")

	createdAt := time.Now().UTC().Format(time.RFC3339)
	noBedrockVal := "false"
	if noBedrock {
		noBedrockVal = "true"
	}

	for i, p := range prompts {
		num := fmt.Sprintf("%03d", i+1) // 001, 002, …

		// Prompt file — base64-encoded to survive all special characters.
		b64Prompt := base64.StdEncoding.EncodeToString([]byte(p))
		sb.WriteString(fmt.Sprintf("echo %s | base64 -d > /workspace/.km-agent/queue/%s.prompt\n", b64Prompt, num))
		sb.WriteString(fmt.Sprintf("chmod 0600 /workspace/.km-agent/queue/%s.prompt\n", num))

		// Meta JSON — also base64-encoded for consistency.
		meta := fmt.Sprintf(`{"no_bedrock":%s,"created_at":"%s","status":"pending"}`, noBedrockVal, createdAt)
		b64Meta := base64.StdEncoding.EncodeToString([]byte(meta))
		sb.WriteString(fmt.Sprintf("echo %s | base64 -d > /workspace/.km-agent/queue/%s.meta.json\n", b64Meta, num))
		sb.WriteString(fmt.Sprintf("chmod 0600 /workspace/.km-agent/queue/%s.meta.json\n", num))
	}

	// Ensure sandbox user owns all queue files (runner's auth probe checks ~/.claude/).
	sb.WriteString("chown -R sandbox:sandbox /workspace/.km-agent/queue\n")

	_, err := sendSSMAndWait(ctx, ssmClient, instanceID, sb.String())
	return err
}

// kickQueueRunner issues `systemctl start km-queue` via SSM.
//
// In Wave 1, the km-queue.service systemd unit does not yet exist on the box —
// that lands in Plan 86-03 (userdata.go seeding). Until then, this call is a
// harmless no-op (|| true suppresses the "unit not found" exit code).
// Plan 86-03 will tighten this once the unit is present.
func kickQueueRunner(ctx context.Context, ssmClient SSMSendAPI, instanceID string) error {
	kickCmd := "systemctl start km-queue 2>&1 || true"
	_, err := sendSSMAndWait(ctx, ssmClient, instanceID, kickCmd)
	return err
}

// queueStatusEntry is a parsed line from the queue status poll script output.
type queueStatusEntry struct {
	num    string
	status string
}

// parseQueueStatuses parses the output of the queue status poll script.
// Each line has the format "NNN|status" (e.g. "001|done", "002|failed").
// Blank lines and malformed lines are silently skipped.
func parseQueueStatuses(out string) []queueStatusEntry {
	var entries []queueStatusEntry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		entries = append(entries, queueStatusEntry{num: parts[0], status: parts[1]})
	}
	return entries
}

// fetchFailedExitCode reads the exit code written by the runner for the most
// recently failed run entry. Returns 1 as a safe fallback if the exit_code
// file is missing or unparseable (ensures non-zero exit code is always
// propagated on failure).
func fetchFailedExitCode(ctx context.Context, ssmClient SSMSendAPI, instanceID string) (int, error) {
	cmd := `sudo -u sandbox bash -c '
for d in $(ls -1t /workspace/.km-agent/runs/ 2>/dev/null); do
    s=$(cat "/workspace/.km-agent/runs/$d/status" 2>/dev/null)
    if [ "$s" = "failed" ]; then
        cat "/workspace/.km-agent/runs/$d/exit_code" 2>/dev/null
        exit 0
    fi
done
echo 1'`
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, cmd)
	if err != nil {
		return 1, err
	}
	ec, parseErr := strconv.Atoi(strings.TrimSpace(out))
	if parseErr != nil {
		return 1, parseErr
	}
	return ec, nil
}

// waitForQueueDrain blocks until every queue entry is terminal (done/failed/skipped).
// It polls /workspace/.km-agent/queue/*.meta.json every queuePollInterval seconds.
//
// Returns:
//   - (0, nil)    all entries are "done"
//   - (>0, nil)   first failed entry's exit code (from runs/<runID>/exit_code)
//   - (-1, err)   SSM/IO error during polling OR ctx.Done() returned
//
// Respects ctx — exits promptly on cancellation (within one poll cycle).
// Poll cadence is controlled by queuePollInterval (package-level var for test override).
func waitForQueueDrain(ctx context.Context, ssmClient SSMSendAPI, instanceID string, expectedCount int) (int, error) {
	statusCmd := `sudo -u sandbox bash -c '
for f in $(ls -1 /workspace/.km-agent/queue/*.meta.json 2>/dev/null | sort); do
    [ -f "$f" ] || continue
    num=$(basename "$f" .meta.json)
    status=$(jq -r .status "$f" 2>/dev/null || echo "unknown")
    echo "$num|$status"
done'`

	ticker := time.NewTicker(QueuePollInterval)
	defer ticker.Stop()
	// First poll immediately — don't wait a full interval for the first read.
	firstPoll := time.NewTimer(0)
	defer firstPoll.Stop()

	for {
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-firstPoll.C:
			// first-iteration only; subsequent polls rely on ticker
		case <-ticker.C:
		}

		out, err := sendSSMAndWait(ctx, ssmClient, instanceID, statusCmd)
		if err != nil {
			return -1, fmt.Errorf("queue status poll: %w", err)
		}

		statuses := parseQueueStatuses(out)
		if len(statuses) < expectedCount {
			// Files not visible yet — runner is still starting. Wait.
			continue
		}

		terminal := true
		var firstFailedNum string
		for _, s := range statuses {
			switch s.status {
			case "done", "skipped":
				// terminal-ok
			case "failed":
				if firstFailedNum == "" {
					firstFailedNum = s.num
				}
			case "pending", "running":
				terminal = false
			default:
				terminal = false
			}
		}
		if !terminal {
			continue
		}

		if firstFailedNum == "" {
			return 0, nil // all done
		}
		// Fetch exit code from the most recent failed run directory.
		exitCode, _ := fetchFailedExitCode(ctx, ssmClient, instanceID)
		if exitCode == 0 {
			exitCode = 1 // safety: ensure non-zero on any failure
		}
		return exitCode, nil
	}
}

// ReconcileMetaStatus is a pure Go mirror of the bash runner's startup reconcile step.
// On every start the bash runner resets "running" entries back to "pending"
// so that a reboot/pause-resume cycle does not permanently strand queue entries.
//
// Exported for unit testing from cmd_test (PQ-08).
func ReconcileMetaStatus(status string) string {
	if status == "running" {
		return "pending"
	}
	return status
}

// doStep16PromptPush is the operator-side Step 16 hook: resolve the EC2
// instance ID for sandboxID, push queue files, kick the runner, and
// optionally block until the queue drains.
//
// Runs AFTER runCreate / runCreateRemote returns (sandbox is reachable by
// then). The create-handler Lambda is not involved — RESEARCH.md Pitfall #1.
//
// R1 regression guard: when prompts is empty this function is a true no-op.
// The caller also guards with `if len(resolvedPrompts) > 0`.
//
// When wait=true and the queue drains with at least one failure, this function
// returns a *ExitCodeError carrying the first-failed entry's exit code. The
// caller (RunE in create.go) returns the error unchanged; the outermost cobra
// boundary (Execute() in root.go) detects it via errors.As and calls
// os.Exit(exitErr.Code). This preserves all deferred cleanup registered by
// RunE and Cobra middleware — inline os.Exit would skip them.
func doStep16PromptPush(ctx context.Context, cfg *config.Config, sandboxID string, prompts []string, noBedrock bool, awsProfile string, wait bool) error {
	if len(prompts) == 0 {
		return nil // R1 regression guard
	}

	// Load AWS config (mirrors agent.go runAgentResults ~line 848).
	awsCfg, err := kmaws.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	ssmClient := ssm.NewFromConfig(awsCfg)

	// Resolve EC2 instance ID via the existing fetcher pattern (agent.go ~line 852).
	// For --remote: runCreateRemote returns immediately after Lambda dispatch, so the
	// instance won't exist yet. Poll up to 8 min for the sandbox to reach "running"
	// status and expose its EC2 instance in metadata (Lambda provisioning is typically
	// 3-5 min for EC2 + SSM-agent-ready).
	fetcher := newRealFetcher(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())
	const provisionTimeout = 8 * time.Minute
	const pollInterval = 10 * time.Second
	deadline := time.Now().Add(provisionTimeout)
	var rec *kmaws.SandboxRecord
	var instanceID string
	for {
		var fetchErr error
		rec, fetchErr = fetcher.FetchSandbox(ctx, sandboxID)
		if fetchErr == nil {
			if id, idErr := extractResourceID(rec.Resources, ":instance/"); idErr == nil && rec.Status == "running" {
				instanceID = id
				break
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting %v for sandbox %s to reach running with EC2 instance (last status: %q)", provisionTimeout, sandboxID, rec.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
		fmt.Printf("Step 16: waiting for sandbox %s to reach running (status=%q)...\n", sandboxID, rec.Status)
	}

	fmt.Printf("Step 16: pushing %d queued prompt(s) to sandbox %s...\n", len(prompts), sandboxID)
	if err := pushQueueFiles(ctx, ssmClient, instanceID, prompts, noBedrock); err != nil {
		return fmt.Errorf("push queue files: %w", err)
	}
	if err := kickQueueRunner(ctx, ssmClient, instanceID); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: systemctl start km-queue: %v\n", err)
	}

	if !wait {
		fmt.Printf("Step 16: queue armed with %d prompt(s); returning (use --wait to block).\n", len(prompts))
		return nil
	}

	fmt.Printf("Step 16: queue armed; waiting for drain (--wait)...\n")
	exitCode, err := waitForQueueDrain(ctx, ssmClient, instanceID, len(prompts))
	if err != nil {
		return fmt.Errorf("wait for queue drain: %w", err)
	}
	if exitCode == 0 {
		fmt.Printf("Step 16: queue drained (all %d prompt(s) done).\n", len(prompts))
		return nil
	}
	// Non-zero: return a typed ExitCodeError. The outermost cobra boundary
	// (just outside cmd.Execute()) detects this via errors.As and calls
	// os.Exit(exitCode). DO NOT call os.Exit here — defers higher up the
	// stack (AWS SDK cleanup, telemetry flushes, etc.) must still run.
	return &ExitCodeError{Code: exitCode}
}
