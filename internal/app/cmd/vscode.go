package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ec2DescribeAPI is the minimal subset of ec2.Client used by runVSCodeRekey.
// Tests inject a mock; production code passes a real ec2.NewFromConfig(awsCfg).
type ec2DescribeAPI interface {
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

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
	parent.AddCommand(newVSCodeRekeyCmd(cfg, fetcher, ssmClient))
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
	// Probe the local port before doing any AWS work or writing ssh-config.
	// Common debug ports (9222 Chrome DevTools, 9229 Node, 5900 VNC) often
	// already have a process bound — session-manager-plugin will silently
	// fail to bind and ssh hits the squatter, producing a confusing
	// "Connection closed by 127.0.0.1" error.
	probeLn, probeErr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if probeErr != nil {
		return fmt.Errorf("local port %d is already in use — pick a different one with --local-port (e.g. 22122)", localPort)
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

func newVSCodeRekeyCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	var force, yes bool
	cmd := &cobra.Command{
		Use:   "rekey <sandbox-id>",
		Short: "Rotate the VS Code Remote-SSH ed25519 keypair for a running sandbox",
		Long: `Rotate the per-sandbox VS Code Remote-SSH ed25519 keypair on a running sandbox.

Generates a fresh keypair on the operator's machine, pushes the new public key to
the sandbox via SSM (with readback verification), then atomically replaces the
local key files. Active VS Code Remote-SSH sessions stay connected with the old
key until you reconnect.`,
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
			awsCfg, err := kmaws.LoadAWSConfig(c.Context(), "klanker-terraform")
			if err != nil {
				return fmt.Errorf("load AWS config for EC2: %w", err)
			}
			realEC2 := ec2.NewFromConfig(awsCfg)
			return runVSCodeRekey(c.Context(), cfg, f, realEC2, s, sandboxID, force, yes)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Override the km lock safety lock")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

func runVSCodeRekey(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, ec2Client ec2DescribeAPI, ssmClient SSMSendAPI, sandboxID string, force, yes bool) error {
	// Gate 1: fetch sandbox + extract instance ID
	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	// Gate 2: EC2 running-state check (mirrors pause.go:139-154)
	descOut, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("tag:km:sandbox-id"), Values: []string{sandboxID}},
			{Name: awssdk.String("instance-state-name"), Values: []string{"running"}},
		},
	})
	if err != nil {
		return fmt.Errorf("describe instances: %w", err)
	}
	running := false
	for _, r := range descOut.Reservations {
		if len(r.Instances) > 0 {
			running = true
			break
		}
	}
	if !running {
		return fmt.Errorf("sandbox %s is not running — check status with km list, then km resume %s", sandboxID, sandboxID)
	}
	fmt.Printf("✓ EC2 instance running (%s in %s)\n", instanceID, rec.Region)

	// Gate 3: lock check (skipped when --force)
	if !force {
		if err := checkSandboxLock(ctx, cfg, sandboxID); err != nil {
			return fmt.Errorf("sandbox is locked. Use --force to override or run: km unlock %s", sandboxID)
		}
	}

	// Gate 4: SSM remote probe via reused vsCodeStatusScript + parseVSCodeStatus
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, vsCodeStatusScript)
	if err != nil {
		return fmt.Errorf("ssm pre-flight: %w", err)
	}
	if err := parseVSCodeStatus(out, sandboxID); err != nil {
		return err
	}
	fmt.Printf("✓ Pre-flight check passed (sshd active, authorized_keys present)\n")

	// TODO Plan 76-02: local-key classification → confirmation prompt → keygen → SSM
	// install → verify → atomic commit. The marker below is the test seam Plan 76-02
	// will replace; until then, runVSCodeRekey returns nil after pre-flight passes.
	fmt.Printf("(Plan 76-02 wires up keygen + push + commit here)\n")
	_ = yes // used in Plan 76-02's confirmation prompt
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
