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
	"github.com/whereiskurt/klanker-maker/pkg/sshkey"
	gossh "golang.org/x/crypto/ssh"

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

// connectPrep is everything km vscode start and km herdr start do identically:
// probe the local port, resolve the sandbox and its instance, and locate the
// local private key. It deliberately stops short of the SSM pre-flight (each
// command probes different facts) AND of upserting ~/.ssh/config — each caller
// runs its own pre-flight FIRST and only then calls upsertSandboxHost, so an
// unhealthy sandbox never gets an ssh-config entry written for it. That
// ordering is load-bearing: see upsertSandboxHost.
//
// portSuggestion is the alternate --local-port value named in the collision
// error; each caller supplies its own so the message stays specific to that
// command's port range rather than a generic offset.
//
// Returns the instance id, the AWS region, the ssh-config alias, and the local
// private key path.
func connectPrep(ctx context.Context, fetcher SandboxFetcher, sandboxID string, localPort, portSuggestion int) (instanceID, region string, hostNames []string, privPath string, err error) {
	// Probe the local port before doing any AWS work or writing ssh-config.
	// Common debug ports (9222 Chrome DevTools, 9229 Node, 5900 VNC) often
	// already have a process bound — session-manager-plugin will silently
	// fail to bind and ssh hits the squatter, producing a confusing
	// "Connection closed by 127.0.0.1" error.
	probeLn, probeErr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if probeErr != nil {
		return "", "", nil, "", fmt.Errorf("local port %d is already in use — pick a different one with --local-port (e.g. %d)", localPort, portSuggestion)
	}
	probeLn.Close()

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return "", "", nil, "", fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err = extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return "", "", nil, "", fmt.Errorf("find EC2 instance: %w", err)
	}

	privPath, err = sandboxKeyPath(sandboxID)
	if err != nil {
		return "", "", nil, "", err
	}

	// ssh(5) allows several patterns per Host line and matches the first block
	// containing the requested name, so km writes both forms: the operator's
	// alias for typing, and the sandbox id so anything already referencing
	// km-<sandbox-id> (a saved VS Code workspace, a script, this repo's docs)
	// keeps resolving.
	//
	// The id MUST stay last — km doctor's stale-entry sweep reads the last name
	// as the sandbox id, and that sweep deletes keypairs.
	hostNames = sandboxHostNames(rec.Alias, sandboxID)
	return instanceID, rec.Region, hostNames, privPath, nil
}

// sandboxHostNames builds the ssh-config Host name list for a sandbox:
// `km-<alias>` first when an alias exists, then always `km-<sandbox-id>` last.
// A blank alias, or one that would render the same name as the id, yields the
// id alone rather than a duplicated pattern.
func sandboxHostNames(alias, sandboxID string) []string {
	id := "km-" + sandboxID
	alias = strings.TrimSpace(alias)
	if alias == "" || alias == sandboxID {
		return []string{id}
	}
	return []string{"km-" + alias, id}
}

// primaryHostName is the name shown to the operator and typed into VS Code or
// `herdr --remote` — the friendly one when there is an alias.
func primaryHostName(hostNames []string) string {
	if len(hostNames) == 0 {
		return ""
	}
	return hostNames[0]
}

// upsertSandboxHost writes the ~/.ssh/config entry. Kept OUT of connectPrep so
// each caller can run its own SSM pre-flight FIRST — km vscode start has always
// declined to touch the operator's ssh config for a sandbox that turns out
// unhealthy, and that ordering is load-bearing.
func upsertSandboxHost(hostNames []string, privPath string, localPort int) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	return UpsertHost(filepath.Join(home, ".ssh", "config"), hostNames, HostOptions{
		HostName:     "localhost",
		Port:         localPort,
		User:         "sandbox",
		IdentityFile: privPath,
	})
}

// runVSCodeStart resolves the sandbox, verifies the local private key, runs the SSM pre-flight
// check, upserts the ssh-config entry, prints the operator instruction block, then opens the
// foreground SSM port-forward.
func runVSCodeStart(ctx context.Context, _ *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID string, localPort int) error {
	// 22122 was chosen deliberately (Phase 73 UAT) and is still documented as
	// the escape hatch in docs/vscode.md and docs/user-manual.md — keep it a
	// fixed literal here rather than an offset off localPort.
	instanceID, region, hostNames, privPath, err := connectPrep(ctx, fetcher, sandboxID, localPort, 22122)
	if err != nil {
		return err
	}

	// Single-round-trip SSM pre-flight check. Runs BEFORE upsertSandboxHost —
	// an unhealthy sandbox must not get an ssh-config entry written for it.
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, vsCodeStatusScript)
	if err != nil {
		return fmt.Errorf("ssm pre-flight check: %w", err)
	}
	if err := parseVSCodeStatus(out, sandboxID); err != nil {
		return err
	}

	if err := upsertSandboxHost(hostNames, privPath, localPort); err != nil {
		return fmt.Errorf("upsert ssh-config: %w", err)
	}
	alias := primaryHostName(hostNames)

	// Print the connection block before opening the blocking port-forward.
	fmt.Printf("✓ Updated ~/.ssh/config (Host: %s)\n", strings.Join(hostNames, " "))
	fmt.Printf("✓ Forwarding localhost:%d → sandbox:22\n\n", localPort)
	fmt.Printf("In VS Code: F1 → \"Remote-SSH: Connect to Host...\" → %s\n", alias)
	fmt.Printf("The tunnel auto-reconnects if it drops; press Ctrl-C to close it (sshd keeps running on the sandbox).\n\n")

	// Open the SSM port-forward with auto-reconnect. sshd survives a dropped tunnel,
	// so reconnecting lets VS Code re-establish over the same forward. An SSH-banner
	// liveness probe recycles a silently-hung plugin.
	buildPF := func(c context.Context) *exec.Cmd {
		return buildPortForwardCmd(c, instanceID, region, strconv.Itoa(localPort), "22")
	}
	return runReconnectingPortForward(ctx, execFn, buildPF, sshBannerTunnelProbe(localPort), true, os.Stdout)
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

	// Step 1: Local key state classification
	home, _ := os.UserHomeDir()
	keysDir := filepath.Join(home, ".km", "keys")
	localPubPath := filepath.Join(keysDir, sandboxID+".pub")
	_, statErr := os.Stat(localPubPath)
	localKeyAbsent := os.IsNotExist(statErr)

	// Step 2: Confirmation prompt (skipped when yes == true)
	if !yes {
		var oldFP string
		if localKeyAbsent {
			oldFP = "(no local key — cross-laptop bootstrap)"
		} else {
			oldFP = fmt.Sprintf("%s (~/.km/keys/%s)", pubkeyFingerprint(localPubPath), sandboxID)
		}
		fmt.Printf("\nRotating VS Code key for %s\n", sandboxID)
		fmt.Printf("  Old: %s\n", oldFP)
		fmt.Printf("  New: (will be generated)\n")
		fmt.Printf("Continue? [y/N] ")
		var answer string
		fmt.Scanln(&answer)
		fmt.Println()
		if answer != "y" && answer != "Y" && answer != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Step 3: Keypair generation to scratch paths
	privNewPath := filepath.Join(keysDir, sandboxID+".new")
	pubNewPath := filepath.Join(keysDir, sandboxID+".pub.new")
	newPubKeyLine, err := sshkey.GenerateAndWrite(privNewPath, pubNewPath, "km-"+sandboxID)
	if err != nil {
		return fmt.Errorf("generate new keypair: %w", err)
	}
	fmt.Printf("✓ New keypair generated (%s)\n", pubkeyFingerprint(pubNewPath))

	// Step 4: SSM install script + readback (single round-trip)
	installScript := fmt.Sprintf(`set -e
mkdir -p /home/sandbox/.ssh
chmod 700 /home/sandbox/.ssh
chown sandbox:sandbox /home/sandbox/.ssh
cat > /home/sandbox/.ssh/authorized_keys << 'KEY'
%s
KEY
chmod 600 /home/sandbox/.ssh/authorized_keys
chown sandbox:sandbox /home/sandbox/.ssh/authorized_keys
command -v restorecon >/dev/null 2>&1 && restorecon -R -v /home/sandbox/.ssh || true
echo "=== READBACK ==="
head -1 /home/sandbox/.ssh/authorized_keys`, newPubKeyLine)

	ssmOut, err := sendSSMAndWait(ctx, ssmClient, instanceID, installScript)
	if err != nil {
		return fmt.Errorf("ssm install: %w", err)
	}

	// Step 5: Readback verification
	markerIdx := strings.Index(ssmOut, "=== READBACK ===")
	if markerIdx < 0 {
		return fmt.Errorf("ssm install readback missing — no '=== READBACK ===' marker in output.\nInspect via: km shell %s -- cat ~/.ssh/authorized_keys\nRe-run: km vscode rekey %s", sandboxID, sandboxID)
	}
	afterMarker := ssmOut[markerIdx+len("=== READBACK ==="):]
	// Strip leading newline and find first non-empty line
	afterMarker = strings.TrimLeft(afterMarker, "\n")
	firstLine := afterMarker
	if idx := strings.Index(afterMarker, "\n"); idx >= 0 {
		firstLine = afterMarker[:idx]
	}
	readbackLine := strings.TrimRight(firstLine, "\r\n")
	if readbackLine != newPubKeyLine {
		return fmt.Errorf("remote key install verification failed. Old key still active locally.\nInspect via: km shell %s -- cat ~/.ssh/authorized_keys\nRe-run: km vscode rekey %s", sandboxID, sandboxID)
	}
	fmt.Printf("✓ Pushed to sandbox via SSM (verified — readback matches)\n")

	// Step 6: Atomic local commit (.pub FIRST, then private)
	privFinalPath := filepath.Join(keysDir, sandboxID)
	pubFinalPath := filepath.Join(keysDir, sandboxID+".pub")
	if err := os.Rename(pubNewPath, pubFinalPath); err != nil {
		return fmt.Errorf("commit new public key: %w", err)
	}
	if err := os.Rename(privNewPath, privFinalPath); err != nil {
		return fmt.Errorf("commit new private key: %w", err)
	}

	// Step 7: Final output
	actionWord := "replaced"
	if localKeyAbsent {
		actionWord = "created"
	}
	fmt.Printf("✓ Local key %s atomically (~/.km/keys/%s)\n\n", actionWord, sandboxID)
	fmt.Printf("Rekey complete. Active VS Code sessions stay on the old key until reconnect.\n")
	return nil
}

// pubkeyFingerprint reads pubPath, parses with gossh.ParseAuthorizedKey, returns
// gossh.FingerprintSHA256(pk) formatted as "SHA256:<base64>" (matches ssh-keygen -lf).
// Returns a descriptive placeholder on read or parse error so the confirmation prompt
// remains informative even when the local file is missing or corrupt.
func pubkeyFingerprint(pubPath string) string {
	raw, err := os.ReadFile(pubPath)
	if err != nil {
		return "(unable to read local pubkey)"
	}
	pk, _, _, _, err := gossh.ParseAuthorizedKey(raw)
	if err != nil {
		return "(unable to parse local pubkey)"
	}
	return gossh.FingerprintSHA256(pk)
}

// parseVSCodeStatus interprets the combined SSM script output and returns a descriptive error
// for each failure mode, or nil when both sshd and authorized_keys are healthy.
func parseVSCodeStatus(out, sandboxID string) error {
	sshdActive := strings.Contains(out, "=== sshd ===\nactive")
	authkeysPresent := strings.Contains(out, "=== authkeys exists ===\nyes")

	switch {
	case !sshdActive && !authkeysPresent:
		return fmt.Errorf("VS Code not enabled in this sandbox's profile (set spec.runtime.vscode.enabled: true and recreate the sandbox)")
	case !authkeysPresent:
		return fmt.Errorf("unexpected state: sshd is running but /home/sandbox/.ssh/authorized_keys is absent — recreate the sandbox")
	case !sshdActive:
		return fmt.Errorf("sshd is not running on the sandbox; try `km shell %s -- sudo systemctl start sshd`", sandboxID)
	}
	return nil
}
