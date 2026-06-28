// Package cmd — model.go
// km model start / km model status: operator-side SSM port-forward to the on-box
// Bifrost gateway (:8001), mirroring km vscode start / km desktop start.
//
// Bifrost is a multi-provider model router installed by base/gpu/serve profiles. It
// serves the OpenAI Chat Completions + Responses API (for Codex) and the Anthropic
// Messages API (for Claude Code, via /anthropic) on a single port :8001, fronting
// vLLM :8000 for the local model plus cloud routes (Bedrock, api.anthropic.com).
//
// km model start <sb> [--local-port 8001] [--anthropic]
//   Opens a reconnecting SSM port-forward to remote port 8001.
//   --anthropic adds Claude-Code-specific connection hints (ANTHROPIC_BASE_URL).
//
// km model status <sb>
//   Runs a single-round-trip SSM probe to report vllm + bifrost health.
package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// modelStatusScript is the combined SSM script sent by both model start and model status.
// Single round-trip: returns vllm + bifrost systemd unit states and whether Bifrost
// answers on :8001.
const modelStatusScript = `echo "=== vllm ==="
systemctl is-active vllm 2>&1 || true
echo "=== bifrost ==="
systemctl is-active bifrost 2>&1 || true
echo "=== gateway ==="
curl -s -o /dev/null -w '%{http_code}' http://localhost:8001/ 2>/dev/null || echo "0"`

// NewModelCmd returns the km model parent command (Phase 122).
func NewModelCmd(cfg *config.Config) *cobra.Command {
	return newModelCmdInternal(cfg, nil, nil, nil)
}

// newModelCmdInternal is the dependency-injectable constructor for km model.
func newModelCmdInternal(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
	parent := &cobra.Command{
		Use:          "model",
		Short:        "Connect to the on-box model gateway (Bifrost) over SSM port-forward",
		SilenceUsage: true,
	}
	parent.AddCommand(newModelStartCmd(cfg, fetcher, execFn, ssmClient))
	parent.AddCommand(newModelStatusCmd(cfg, fetcher, ssmClient))
	return parent
}

func newModelStartCmd(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
	var localPort int
	var anthropic bool
	cmd := &cobra.Command{
		Use:          "start <sandbox-id>",
		Short:        "Start a Bifrost gateway port-forward and print client connection hints",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			f, e, _, err := resolveModelDeps(c.Context(), cfg, fetcher, execFn, ssmClient)
			if err != nil {
				return err
			}
			sandboxID, err := ResolveSandboxID(c.Context(), cfg, args[0])
			if err != nil {
				return err
			}
			return runModelStart(c.Context(), cfg, f, e, sandboxID, localPort, anthropic)
		},
	}
	cmd.Flags().IntVar(&localPort, "local-port", 8001, "Local port for the SSM forward (default 8001)")
	cmd.Flags().BoolVar(&anthropic, "anthropic", false, "Print Claude Code connection hints (ANTHROPIC_BASE_URL via Bifrost /anthropic path)")
	return cmd
}

func newModelStatusCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	return &cobra.Command{
		Use:          "status <sandbox-id>",
		Short:        "Report whether vLLM and the Bifrost gateway are active",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			f, _, s, err := resolveModelDeps(c.Context(), cfg, fetcher, nil, ssmClient)
			if err != nil {
				return err
			}
			sandboxID, err := ResolveSandboxID(c.Context(), cfg, args[0])
			if err != nil {
				return err
			}
			return runModelStatus(c.Context(), cfg, f, s, sandboxID)
		},
	}
}

// resolveModelDeps initialises real AWS clients when test-injected nil deps are provided.
// Mirrors resolveVSCodeDeps / resolveDesktopDeps exactly.
func resolveModelDeps(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) (SandboxFetcher, ShellExecFunc, SSMSendAPI, error) {
	if fetcher == nil {
		if cfg.StateBucket == "" {
			return nil, nil, nil, fmt.Errorf("state bucket not configured")
		}
		awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if err != nil {
			return nil, nil, nil, fmt.Errorf("load AWS config: %w", err)
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())
		if ssmClient == nil {
			ssmClient = ssm.NewFromConfig(awsCfg)
		}
	}
	if execFn == nil {
		execFn = defaultShellExec
	}
	return fetcher, execFn, ssmClient, nil
}

// runModelStart resolves the sandbox, probes the local port, prints the connection
// hint block, then opens a foreground auto-reconnecting SSM port-forward to Bifrost
// (:8001) on the sandbox.
//
// Bifrost serves BOTH the OpenAI/Responses API surface (for Codex) and the
// Anthropic Messages API (for Claude Code) via its /anthropic path — so
// --anthropic is a semantic alias that selects Claude-Code-oriented hints, not
// a different port.
func runModelStart(ctx context.Context, _ *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, sandboxID string, localPort int, anthropic bool) error {
	// Probe the local port before doing any AWS work.
	probeLn, probeErr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if probeErr != nil {
		return fmt.Errorf("local port %d is already in use — pick a different one with --local-port (e.g. 18001)", localPort)
	}
	probeLn.Close()

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	// Print the connection hint block before opening the blocking port-forward.
	fmt.Printf("✓ Forwarding localhost:%d → sandbox:8001 (Bifrost gateway)\n\n", localPort)

	if anthropic {
		// Claude Code connects via Bifrost's /anthropic ingress (not /v1/messages).
		// ANTHROPIC_BASE_URL must point at /anthropic on the forwarded port, NOT
		// at the raw Bifrost root (Bifrost routes by path prefix).
		fmt.Printf("Claude Code (Anthropic Messages API via Bifrost):\n")
		fmt.Printf("  export ANTHROPIC_BASE_URL=http://localhost:%d/anthropic\n\n", localPort)
		fmt.Printf("  Then: claude --model local (or whichever local model name you configured)\n\n")
	} else {
		// Default: OpenAI-compatible surface for Codex and raw curl.
		fmt.Printf("Codex (OpenAI/Responses API):\n")
		fmt.Printf("  base_url=\"http://localhost:%d/v1\"\n", localPort)
		fmt.Printf("  wire_api=\"responses\"\n\n")
		fmt.Printf("curl (OpenAI chat completions):\n")
		fmt.Printf("  curl http://localhost:%d/v1/chat/completions -H 'Content-Type: application/json' \\\n", localPort)
		fmt.Printf("    -d '{\"model\":\"local\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}'\n\n")
		fmt.Printf("For Claude Code hints: km model start %s --anthropic\n\n", sandboxID)
	}
	fmt.Printf("The tunnel auto-reconnects if it drops; press Ctrl-C to close it (Bifrost keeps running on the sandbox).\n\n")

	// Open the SSM port-forward with auto-reconnect. Bifrost survives a dropped
	// tunnel server-side. A plain-HTTP liveness probe recycles a silently-hung plugin.
	region := rec.Region
	buildPF := func(c context.Context) *exec.Cmd {
		return buildPortForwardCmd(c, instanceID, region, strconv.Itoa(localPort), "8001")
	}
	return runReconnectingPortForward(ctx, execFn, buildPF, httpTunnelProbe(localPort), true, os.Stdout)
}

// runModelStatus resolves the sandbox, runs the combined SSM status script, and
// prints a one-line summary. Returns a non-nil error (non-zero exit) when not
// fully healthy.
func runModelStatus(ctx context.Context, _ *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI, sandboxID string) error {
	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, modelStatusScript)
	if err != nil {
		return fmt.Errorf("ssm status check: %w", err)
	}
	return parseModelStatus(out, sandboxID)
}

// parseModelStatus interprets the combined SSM script output and returns a
// descriptive error for each failure mode, or nil when both vllm and bifrost
// are active.
func parseModelStatus(out, sandboxID string) error {
	vllmActive := strings.Contains(out, "=== vllm ===\nactive")
	bifrostActive := strings.Contains(out, "=== bifrost ===\nactive")

	switch {
	case !vllmActive && !bifrostActive:
		return fmt.Errorf("model gateway not enabled on this sandbox — create the sandbox from a gpu/serve profile; sandbox: %s", sandboxID)
	case !vllmActive:
		return fmt.Errorf("vllm is not running (bifrost active). Try: km shell %s -- sudo systemctl start vllm", sandboxID)
	case !bifrostActive:
		return fmt.Errorf("bifrost gateway is not running (vllm active). Try: km shell %s -- sudo systemctl start bifrost", sandboxID)
	}
	fmt.Printf("✓ Model gateway ready (vllm active, bifrost active) — sandbox: %s\n", sandboxID)
	return nil
}
