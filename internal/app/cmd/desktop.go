package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
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

// desktopStatusScript is the combined SSM script sent by both desktop start and desktop status.
// Single round-trip: returns kasmvnc systemd unit state and kasmpasswd existence.
const desktopStatusScript = `echo "=== kasmvnc ==="
systemctl is-active kasmvnc 2>&1 || true
echo "=== kasmpasswd ==="
test -f /home/sandbox/.kasmpasswd && echo yes || echo no
echo "=== unitfile ==="
test -f /etc/systemd/system/kasmvnc.service && echo yes || echo no
echo "=== cloudinit ==="
cloud-init status 2>/dev/null | head -1 || echo "status: unknown"`

// desktopRestartScript force-restarts the server-side KasmVNC session: stop the
// unit, hard-kill a wedged Xvnc, clear stale X lock/socket files, then start
// again. This re-runs ~/.vnc/xstartup, so the WM + browser come up fresh — the
// equivalent of logging out of XFCE and back in. set +e keeps a no-op stop/kill
// (e.g. already-dead Xvnc) from aborting before the start.
const desktopRestartScript = `set +e
echo "=== RESTART ==="
sudo systemctl stop kasmvnc 2>&1
sudo -u sandbox /usr/bin/vncserver -kill :1 >/dev/null 2>&1
sudo rm -f /tmp/.X1-lock /tmp/.X11-unix/X1 2>/dev/null
sudo systemctl start kasmvnc 2>&1
sleep 3
echo "=== STATUS ==="
systemctl is-active kasmvnc 2>&1`

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
	parent.AddCommand(newDesktopRekeyCmd(cfg, fetcher, ssmClient))
	parent.AddCommand(newDesktopRestartCmd(cfg, fetcher, ssmClient))
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
	fmt.Printf("The tunnel auto-reconnects if it drops; press Ctrl-C to close it (KasmVNC keeps running on the sandbox).\n\n")

	// Open the SSM port-forward with auto-reconnect. KasmVNC survives a dropped
	// tunnel server-side, so reconnecting returns to the same desktop session. An
	// HTTPS liveness probe also recycles a silently-hung plugin (frozen desktop).
	region := rec.Region
	buildPF := func(c context.Context) *exec.Cmd {
		return buildPortForwardCmd(c, instanceID, region, strconv.Itoa(localPort), "8444")
	}
	return runReconnectingPortForward(ctx, execFn, buildPF, httpsTunnelProbe(localPort), true, os.Stdout)
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
	// unitPresent: the kasmvnc.service unit file is written only by the desktop
	// userdata block, so it is the authoritative signal that the profile actually
	// enabled the desktop — distinguishing "not enabled" from "enabled, still
	// installing/booting" (Phase 93.1, GAP-93-02).
	unitPresent := strings.Contains(out, "=== unitfile ===\nyes")
	cloudInitRunning := strings.Contains(out, "status: running")

	if kasmActive && kasmPasswd {
		return nil
	}

	// No unit file → the desktop was never provisioned for this sandbox.
	if !unitPresent {
		if cloudInitRunning {
			return fmt.Errorf("desktop not ready yet — the sandbox is still running cloud-init (first boot installs KasmVNC + the browser, which takes a few minutes). Re-check with: km desktop status %s", sandboxID)
		}
		return fmt.Errorf("desktop not enabled in this sandbox's profile — set spec.runtime.desktop.enabled: true and recreate the sandbox")
	}

	// Unit file present → desktop IS enabled; report the in-progress / failed state.
	switch {
	case cloudInitRunning:
		return fmt.Errorf("desktop still provisioning — cloud-init is mid first-boot install (KasmVNC + browser). Wait a few minutes, then: km desktop status %s", sandboxID)
	case !kasmPasswd:
		return fmt.Errorf("desktop enabled but ~/.kasmpasswd is not seeded yet — boot may still be in progress; re-check shortly or inspect: km shell %s -- sudo journalctl -u cloud-final", sandboxID)
	default: // unit present, password seeded, but service not active
		return fmt.Errorf("desktop installed but kasmvnc is not running — try: km shell %s -- sudo systemctl status kasmvnc", sandboxID)
	}
}

// newDesktopRekeyCmd returns `km desktop rekey` — rotate the per-sandbox KasmVNC
// password on a running sandbox without destroy/recreate. Mirrors `km vscode rekey`.
func newDesktopRekeyCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	var force, yes bool
	cmd := &cobra.Command{
		Use:   "rekey <sandbox-id>",
		Short: "Rotate the KasmVNC password for a running desktop sandbox",
		Long: `Rotate the per-sandbox KasmVNC password on a running sandbox.

Generates a fresh random password, updates ~/.kasmpasswd on the sandbox via SSM
(with a readback check), then atomically replaces the local ~/.km/desktop/<id>
credential. KasmVNC re-reads its password file per web-auth, so the rotation
does not interrupt an already-connected desktop session — the new password
applies to the next login (open km desktop start and log in again).`,
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
			awsCfg, err := kmaws.LoadAWSConfig(c.Context(), "klanker-terraform")
			if err != nil {
				return fmt.Errorf("load AWS config for EC2: %w", err)
			}
			return runDesktopRekey(c.Context(), cfg, f, ec2.NewFromConfig(awsCfg), s, sandboxID, force, yes)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Override the km lock safety lock")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

func newDesktopRestartCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "restart <sandbox-id>",
		Short: "Force a server-side restart of the KasmVNC session (Xvnc + WM + browser)",
		Long: `Force-restart the KasmVNC graphical session on the sandbox.

Stops the kasmvnc unit, hard-kills a wedged Xvnc, clears stale X lock/socket
files, then starts it again — re-running ~/.vnc/xstartup so the window manager
and browser come up fresh. Equivalent to logging out of XFCE and back in. Use it
when the desktop is frozen, the WM is in a bad state, or input handling is stuck.

This interrupts any connected session (the browser session is dropped); the
sandbox, its files, and the KasmVNC credential are untouched. Reconnect with
km desktop start afterward.`,
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
			return runDesktopRestart(c.Context(), cfg, f, s, sandboxID, yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

// runDesktopRestart resolves the sandbox, confirms the desktop is provisioned
// (unit file present — NOT that it is currently active, since a hung session is
// exactly when restart is wanted), optionally prompts, then force-restarts the
// KasmVNC session over SSM and verifies the unit comes back active.
func runDesktopRestart(ctx context.Context, _ *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI, sandboxID string, yes bool) error {
	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	// Pre-flight: confirm the desktop is provisioned for this sandbox. We check
	// the unit FILE (not active state) — a frozen/inactive session is precisely
	// when a restart is wanted, so we must not gate on it being active.
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, desktopStatusScript)
	if err != nil {
		return fmt.Errorf("ssm pre-flight: %w", err)
	}
	if !strings.Contains(out, "=== unitfile ===\nyes") {
		if strings.Contains(out, "status: running") {
			return fmt.Errorf("desktop not ready yet — the sandbox is still running cloud-init. Re-check with: km desktop status %s", sandboxID)
		}
		return fmt.Errorf("desktop not enabled in this sandbox's profile — set spec.runtime.desktop.enabled: true and recreate the sandbox")
	}

	// Confirmation (skipped with --yes) — restart drops the live session.
	if !yes {
		fmt.Printf("Restart the KasmVNC session on %s? This drops any connected desktop session.\n", sandboxID)
		fmt.Printf("Continue? [y/N] ")
		var answer string
		fmt.Scanln(&answer)
		fmt.Println()
		if answer != "y" && answer != "Y" && answer != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	restartOut, err := sendSSMAndWait(ctx, ssmClient, instanceID, desktopRestartScript)
	if err != nil {
		return fmt.Errorf("ssm restart: %w", err)
	}
	if !strings.Contains(restartOut, "=== STATUS ===\nactive") {
		return fmt.Errorf("KasmVNC did not come back active after restart. Inspect with:\n  km shell %s   (then: sudo systemctl status kasmvnc; sudo journalctl -u kasmvnc -n 50)", sandboxID)
	}

	fmt.Printf("✓ KasmVNC session restarted (Xvnc + window manager + browser came up fresh)\n")
	fmt.Printf("Reconnect with: km desktop start %s\n", sandboxID)
	return nil
}

// runDesktopRekey rotates the KasmVNC password. Gates mirror runVSCodeRekey:
// fetch → EC2 running-state → lock → SSM pre-flight, then generate → push → verify
// → atomic local commit.
func runDesktopRekey(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, ec2Client ec2DescribeAPI, ssmClient SSMSendAPI, sandboxID string, force, yes bool) error {
	// Gate 1: fetch sandbox + instance ID.
	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	// Gate 2: EC2 running-state check.
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

	// Gate 3: lock check (skipped when --force).
	if !force {
		if err := checkSandboxLock(ctx, cfg, sandboxID); err != nil {
			return fmt.Errorf("sandbox is locked. Use --force to override or run: km unlock %s", sandboxID)
		}
	}

	// Gate 4: SSM pre-flight via the shared desktop status script.
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, desktopStatusScript)
	if err != nil {
		return fmt.Errorf("ssm pre-flight: %w", err)
	}
	if err := parseDesktopStatus(out, sandboxID); err != nil {
		return err
	}
	fmt.Printf("✓ Pre-flight check passed (kasmvnc active, kasmpasswd present)\n")

	// Step 1: determine the KasmVNC username. The local credential file is the
	// source of truth; absent (cross-laptop) we fall back to the only user km ever
	// provisions ("kasm").
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	credPath := filepath.Join(home, ".km", "desktop", sandboxID)
	user := "kasm"
	localCredAbsent := false
	if b, rerr := os.ReadFile(credPath); rerr == nil {
		if parts := strings.SplitN(strings.TrimSpace(string(b)), ":", 2); len(parts) == 2 && parts[0] != "" {
			user = parts[0]
		}
	} else if os.IsNotExist(rerr) {
		localCredAbsent = true
	} else {
		return fmt.Errorf("read desktop credential: %w", rerr)
	}

	// Step 2: confirmation (skipped when --yes).
	if !yes {
		src := credPath
		if localCredAbsent {
			src = "(no local credential — cross-laptop rotation)"
		}
		fmt.Printf("\nRotating KasmVNC password for %s (user %q)\n", sandboxID, user)
		fmt.Printf("  Local: %s\n", src)
		fmt.Printf("Continue? [y/N] ")
		var answer string
		fmt.Scanln(&answer)
		fmt.Println()
		if answer != "y" && answer != "Y" && answer != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Step 3: generate a fresh password (same alphabet as km create — shell-safe).
	newPass, err := randomPassword(16)
	if err != nil {
		return fmt.Errorf("generate new password: %w", err)
	}

	// Step 4: SSM push — rewrite ~/.kasmpasswd as the sandbox user, with readback.
	installScript := fmt.Sprintf(`set -e
printf '%%s\n%%s\n' '%s' '%s' | sudo -u sandbox kasmvncpasswd -u '%s' -w -r /home/sandbox/.kasmpasswd
chmod 600 /home/sandbox/.kasmpasswd
chown sandbox:sandbox /home/sandbox/.kasmpasswd
echo "=== READBACK ==="
sudo -u sandbox test -s /home/sandbox/.kasmpasswd && echo "kasmpasswd-updated"`, newPass, newPass, user)

	ssmOut, err := sendSSMAndWait(ctx, ssmClient, instanceID, installScript)
	if err != nil {
		return fmt.Errorf("ssm rekey: %w", err)
	}

	// Step 5: readback verification.
	if !strings.Contains(ssmOut, "=== READBACK ===") || !strings.Contains(ssmOut, "kasmpasswd-updated") {
		return fmt.Errorf("remote password update verification failed. Old password still active locally.\nInspect via: km shell %s  (then: sudo -u sandbox cat /home/sandbox/.kasmpasswd)\nRe-run: km desktop rekey %s", sandboxID, sandboxID)
	}
	fmt.Printf("✓ Updated ~/.kasmpasswd on the sandbox via SSM (verified)\n")

	// Step 6: atomic local commit (.new then rename).
	desktopDir := filepath.Join(home, ".km", "desktop")
	if err := os.MkdirAll(desktopDir, 0o700); err != nil {
		return fmt.Errorf("create desktop credential dir: %w", err)
	}
	newPath := credPath + ".new"
	if err := os.WriteFile(newPath, []byte(user+":"+newPass), 0o600); err != nil {
		return fmt.Errorf("write new credential: %w", err)
	}
	if err := os.Rename(newPath, credPath); err != nil {
		return fmt.Errorf("commit new credential: %w", err)
	}

	// Step 7: final output.
	action := "replaced"
	if localCredAbsent {
		action = "created"
	}
	fmt.Printf("✓ Local credential %s atomically (~/.km/desktop/%s)\n\n", action, sandboxID)
	fmt.Printf("Rekey complete. KasmVNC re-reads the password file per login, so any open\nsession stays connected; the new password applies on the next login —\nrun: km desktop start %s\n", sandboxID)
	return nil
}
