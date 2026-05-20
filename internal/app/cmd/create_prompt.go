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
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

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
// instance ID for sandboxID, push queue files, and kick the runner.
//
// Runs AFTER runCreate / runCreateRemote returns (sandbox is reachable by
// then). The create-handler Lambda is not involved — RESEARCH.md Pitfall #1.
//
// R1 regression guard: when prompts is empty this function is a true no-op.
// The caller also guards with `if len(resolvedPrompts) > 0`.
func doStep16PromptPush(ctx context.Context, cfg *config.Config, sandboxID string, prompts []string, noBedrock bool, awsProfile string) error {
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
	fetcher := newRealFetcher(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())
	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance for sandbox %s: %w", sandboxID, err)
	}

	fmt.Printf("Step 16: pushing %d queued prompt(s) to sandbox %s...\n", len(prompts), sandboxID)
	if err := pushQueueFiles(ctx, ssmClient, instanceID, prompts, noBedrock); err != nil {
		return fmt.Errorf("push queue files: %w", err)
	}
	if err := kickQueueRunner(ctx, ssmClient, instanceID); err != nil {
		// Wave 1: km-queue.service unit not yet on box — warn and continue.
		// Plan 86-03 seeds the unit via userdata.go; this WARN disappears after that.
		fmt.Fprintf(os.Stderr, "WARN: systemctl start km-queue returned non-zero (expected until Wave 2 systemd unit lands): %v\n", err)
	}
	fmt.Printf("Step 16: queue armed with %d prompt(s). Returning; runner drains in background.\n", len(prompts))
	return nil
}
