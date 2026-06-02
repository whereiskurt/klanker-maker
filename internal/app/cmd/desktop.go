package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// desktopStatusScript is the combined SSM script sent by both desktop start and desktop status.
// Single round-trip: returns kasmvnc systemd unit state and kasmpasswd existence.
const desktopStatusScript = `echo "=== kasmvnc ==="
systemctl is-active kasmvnc 2>&1 || true
echo "=== kasmpasswd ==="
test -f /home/sandbox/.kasmpasswd && echo yes || echo no`

// NewDesktopCmd returns the `km desktop` parent command. Phase 93.
func NewDesktopCmd(cfg *config.Config) *cobra.Command {
	return newDesktopCmdInternal(cfg, nil, nil, nil)
}

// newDesktopCmdInternal is the dependency-injectable constructor for km desktop.
func newDesktopCmdInternal(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
	parent := &cobra.Command{
		Use:          "desktop",
		Short:        "Connect to a KasmVNC graphical desktop session over SSM port-forward",
		SilenceUsage: true,
	}
	parent.AddCommand(newDesktopStartCmd(cfg, fetcher, execFn, ssmClient))
	parent.AddCommand(newDesktopStatusCmd(cfg, fetcher, ssmClient))
	return parent
}

func newDesktopStartCmd(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
	var localPort int
	cmd := &cobra.Command{
		Use:          "start <sandbox-id>",
		Short:        "Start a KasmVNC port-forward and print the browser URL + credential",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			f, e, s, err := resolveDesktopDeps(c.Context(), cfg, fetcher, execFn, ssmClient)
			if err != nil {
				return err
			}
			sandboxID, err := ResolveSandboxID(c.Context(), cfg, args[0])
			if err != nil {
				return err
			}
			return runDesktopStart(c.Context(), cfg, f, e, s, sandboxID, localPort)
		},
	}
	cmd.Flags().IntVar(&localPort, "local-port", 8444, "Local port for the SSM forward (default 8444)")
	return cmd
}

func newDesktopStatusCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	return &cobra.Command{
		Use:          "status <sandbox-id>",
		Short:        "Report whether the KasmVNC desktop is active and the credential is present",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			f, _, s, err := resolveDesktopDeps(c.Context(), cfg, fetcher, nil, ssmClient)
			if err != nil {
				return err
			}
			sandboxID, err := ResolveSandboxID(c.Context(), cfg, args[0])
			if err != nil {
				return err
			}
			return runDesktopStatus(c.Context(), cfg, f, s, sandboxID)
		},
	}
}

// resolveDesktopDeps initialises real AWS clients when test-injected nil deps are provided.
// Mirrors resolveVSCodeDeps exactly.
func resolveDesktopDeps(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) (SandboxFetcher, ShellExecFunc, SSMSendAPI, error) {
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

// runDesktopStart resolves the sandbox, verifies the local credential file, runs the SSM
// pre-flight check, prints the browser URL + credential, then opens the foreground SSM
// port-forward to the remote KasmVNC port (8444).
func runDesktopStart(ctx context.Context, _ *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID string, localPort int) error {
	// Probe the local port before doing any AWS work.
	// KasmVNC's default remote port (8444) may already be locally occupied.
	probeLn, probeErr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if probeErr != nil {
		return fmt.Errorf("local port %d is already in use — pick a different one with --local-port (e.g. 18444)", localPort)
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

	// Locate the local credential file; fail fast with a clear hint if absent.
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	credPath := filepath.Join(home, ".km", "desktop", sandboxID)
	if _, statErr := os.Stat(credPath); statErr != nil {
		if os.IsNotExist(statErr) {
			return fmt.Errorf("desktop credential for %s not found at %s. If you created this sandbox on a different machine, copy the ~/.km/desktop/%s file over",
				sandboxID, credPath, sandboxID)
		}
		return fmt.Errorf("stat desktop credential: %w", statErr)
	}

	// Read and split the "user:pass" credential file.
	credBytes, err := os.ReadFile(credPath)
	if err != nil {
		return fmt.Errorf("read desktop credential: %w", err)
	}
	credStr := strings.TrimSpace(string(credBytes))
	parts := strings.SplitN(credStr, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("malformed desktop credential at %s (expected user:pass)", credPath)
	}
	credUser, credPass := parts[0], parts[1]

	// Single-round-trip SSM pre-flight check.
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, desktopStatusScript)
	if err != nil {
		return fmt.Errorf("ssm pre-flight check: %w", err)
	}
	if err := parseDesktopStatus(out, sandboxID); err != nil {
		return err
	}

	// Print the connection block before opening the blocking port-forward.
	fmt.Printf("✓ KasmVNC desktop ready\n")
	fmt.Printf("✓ Forwarding localhost:%d → sandbox:8444\n\n", localPort)
	fmt.Printf("Open in your browser: https://localhost:%d/\n\n", localPort)
	fmt.Printf("  user: %s\n", credUser)
	fmt.Printf("  pass: %s\n\n", credPass)
	fmt.Printf("Press Ctrl-C to close the tunnel (KasmVNC keeps running on the sandbox).\n\n")

	// Open the foreground SSM port-forward (blocks until Ctrl-C).
	pfCmd := buildPortForwardCmd(ctx, instanceID, rec.Region, strconv.Itoa(localPort), "8444")
	pfCmd.Stdin = os.Stdin
	pfCmd.Stdout = os.Stdout
	pfCmd.Stderr = os.Stderr
	return execFn(pfCmd)
}

// runDesktopStatus resolves the sandbox, runs the combined SSM status script, and prints a
// one-line summary. Returns a non-nil error (non-zero exit) when not fully healthy.
func runDesktopStatus(ctx context.Context, _ *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI, sandboxID string) error {
	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, desktopStatusScript)
	if err != nil {
		return fmt.Errorf("ssm status check: %w", err)
	}
	if err := parseDesktopStatus(out, sandboxID); err != nil {
		return err
	}
	fmt.Printf("✓ KasmVNC desktop ready (kasmvnc active, kasmpasswd present)\n")
	return nil
}

// parseDesktopStatus interprets the combined SSM script output and returns a descriptive
// error for each failure mode, or nil when both kasmvnc and kasmpasswd are healthy.
func parseDesktopStatus(out, sandboxID string) error {
	kasmActive := strings.Contains(out, "=== kasmvnc ===\nactive")
	kasmPasswd := strings.Contains(out, "=== kasmpasswd ===\nyes")

	switch {
	case !kasmActive && !kasmPasswd:
		return fmt.Errorf("desktop not enabled in this sandbox's profile — set spec.runtime.desktop.enabled: true and recreate the sandbox")
	case !kasmPasswd:
		return fmt.Errorf("unexpected state: kasmvnc is running but ~/.kasmpasswd is absent — recreate the sandbox")
	case !kasmActive:
		return fmt.Errorf("desktop not running — is spec.runtime.desktop.enabled: true set and the sandbox recreated? Try: km shell %s -- sudo systemctl start kasmvnc", sandboxID)
	}
	return nil
}
