package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// vsCodeStatusScript is the single combined SSM script sent by both vscode start and vscode status.
// Single round-trip: returns sshd state, authorized_keys existence, and first key line.
const vsCodeStatusScript = `echo "=== sshd ==="
systemctl is-active sshd 2>&1 || true
echo "=== authkeys exists ==="
test -f /home/sandbox/.ssh/authorized_keys && echo yes || echo no
echo "=== authkeys content ==="
cat /home/sandbox/.ssh/authorized_keys 2>/dev/null | head -1 || true`

// NewVSCodeCmd returns the `km vscode` parent command. Phase 73.
func NewVSCodeCmd(cfg *config.Config) *cobra.Command {
	return newVSCodeCmdInternal(cfg, nil, nil, nil)
}

// newVSCodeCmdInternal is the dependency-injectable constructor for km vscode.
func newVSCodeCmdInternal(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
	parent := &cobra.Command{
		Use:          "vscode",
		Short:        "Connect local VS Code to a sandbox via Remote-SSH over SSM",
		SilenceUsage: true,
	}
	parent.AddCommand(newVSCodeStartCmd(cfg, fetcher, execFn, ssmClient))
	parent.AddCommand(newVSCodeStatusCmd(cfg, fetcher, ssmClient))
	return parent
}

func newVSCodeStartCmd(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
	var localPort int
	cmd := &cobra.Command{
		Use:          "start <sandbox-id>",
		Short:        "Start a Remote-SSH port-forward and configure local SSH for VS Code",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			f, e, s, err := resolveVSCodeDeps(c.Context(), cfg, fetcher, execFn, ssmClient)
			if err != nil {
				return err
			}
			sandboxID, err := ResolveSandboxID(c.Context(), cfg, args[0])
			if err != nil {
				return err
			}
			return runVSCodeStart(c.Context(), cfg, f, e, s, sandboxID, localPort)
		},
	}
	cmd.Flags().IntVar(&localPort, "local-port", 2222, "Local port for the SSM forward (default 2222)")
	return cmd
}

func newVSCodeStatusCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	return &cobra.Command{
		Use:          "status <sandbox-id>",
		Short:        "Report whether sshd is active and the authorized_keys are installed",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			f, _, s, err := resolveVSCodeDeps(c.Context(), cfg, fetcher, nil, ssmClient)
			if err != nil {
				return err
			}
			sandboxID, err := ResolveSandboxID(c.Context(), cfg, args[0])
			if err != nil {
				return err
			}
			return runVSCodeStatus(c.Context(), cfg, f, s, sandboxID)
		},
	}
}

// resolveVSCodeDeps initialises real AWS clients when test-injected nil deps are provided.
// Mirrors the inline pattern used in agent.go's runAgentNonInteractive.
func resolveVSCodeDeps(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) (SandboxFetcher, ShellExecFunc, SSMSendAPI, error) {
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

// runVSCodeStart resolves the sandbox, verifies the local private key, runs the SSM pre-flight
// check, upserts the ssh-config entry, prints the operator instruction block, then opens the
// foreground SSM port-forward.
func runVSCodeStart(ctx context.Context, _ *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID string, localPort int) error {
	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	// Locate the local private key; fail fast with a portability hint if absent.
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	privPath := filepath.Join(home, ".km", "keys", sandboxID)
	if _, statErr := os.Stat(privPath); statErr != nil {
		if os.IsNotExist(statErr) {
			return fmt.Errorf("private key for %s not found at %s. If you created this sandbox on a different machine, copy the ~/.km/keys/%s* files over",
				sandboxID, privPath, sandboxID)
		}
		return fmt.Errorf("stat private key: %w", statErr)
	}

	// Single-round-trip SSM pre-flight check.
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, vsCodeStatusScript)
	if err != nil {
		return fmt.Errorf("ssm pre-flight check: %w", err)
	}
	if err := parseVSCodeStatus(out, sandboxID); err != nil {
		return err
	}

	// Upsert the ~/.ssh/config entry.
	sshConfigPath := filepath.Join(home, ".ssh", "config")
	alias := "km-" + sandboxID
	opts := HostOptions{
		HostName:     "localhost",
		Port:         localPort,
		User:         "sandbox",
		IdentityFile: privPath,
	}
	if err := UpsertHost(sshConfigPath, alias, opts); err != nil {
		return fmt.Errorf("upsert ssh-config: %w", err)
	}

	// Print the connection block before opening the blocking port-forward.
	fmt.Printf("✓ Updated ~/.ssh/config (Host: %s)\n", alias)
	fmt.Printf("✓ Forwarding localhost:%d → sandbox:22\n\n", localPort)
	fmt.Printf("In VS Code: F1 → \"Remote-SSH: Connect to Host...\" → %s\n", alias)
	fmt.Printf("Press Ctrl-C to close the tunnel (sshd keeps running on the sandbox).\n\n")

	// Open the foreground SSM port-forward (blocks until Ctrl-C).
	pfCmd := buildPortForwardCmd(ctx, instanceID, rec.Region, strconv.Itoa(localPort), "22")
	pfCmd.Stdin = os.Stdin
	pfCmd.Stdout = os.Stdout
	pfCmd.Stderr = os.Stderr
	return execFn(pfCmd)
}

// runVSCodeStatus resolves the sandbox, runs the combined SSM status script, and prints a
// one-line summary. Returns a non-nil error (non-zero exit) when not fully healthy.
func runVSCodeStatus(ctx context.Context, _ *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI, sandboxID string) error {
	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, vsCodeStatusScript)
	if err != nil {
		return fmt.Errorf("ssm status check: %w", err)
	}
	if err := parseVSCodeStatus(out, sandboxID); err != nil {
		return err
	}
	fmt.Printf("✓ VS Code Remote-SSH ready (sshd active, authorized_keys present)\n")
	return nil
}

// parseVSCodeStatus interprets the combined SSM script output and returns a descriptive error
// for each failure mode, or nil when both sshd and authorized_keys are healthy.
func parseVSCodeStatus(out, sandboxID string) error {
	sshdActive := strings.Contains(out, "=== sshd ===\nactive")
	authkeysPresent := strings.Contains(out, "=== authkeys exists ===\nyes")

	switch {
	case !sshdActive && !authkeysPresent:
		return fmt.Errorf("VS Code not enabled in this sandbox's profile (set spec.cli.vscodeEnabled: true and recreate the sandbox)")
	case !authkeysPresent:
		return fmt.Errorf("unexpected state: sshd is running but /home/sandbox/.ssh/authorized_keys is absent — recreate the sandbox")
	case !sshdActive:
		return fmt.Errorf("sshd is not running on the sandbox; try `km shell %s -- sudo systemctl start sshd`", sandboxID)
	}
	return nil
}
