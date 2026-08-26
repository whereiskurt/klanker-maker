package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/kubetunnel"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// tunnelOpts carries the resolved flag set for one `km tunnel` invocation.
type tunnelOpts struct {
	contextName string
	kubeconfig  string
	localPort   int
	bindPort    int
	dryRun      bool
	printSSH    bool
	verbose     bool
}

// NewTunnelCmd returns the `km tunnel` command (Phase 130).
func NewTunnelCmd(cfg *config.Config) *cobra.Command {
	return newTunnelCmdInternal(cfg, nil, nil)
}

func newTunnelCmdInternal(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc) *cobra.Command {
	var o tunnelOpts

	cmd := &cobra.Command{
		Use:   "tunnel <sandbox-id> --context <kube-context>",
		Short: "Open a sandbox shell carrying a reverse tunnel to an operator-only Kubernetes cluster",
		Long: `Open an interactive shell on a sandbox with kubectl working against a cluster
that is only reachable from THIS workstation.

The cluster is dialled from your machine, over your VPN — the sandbox never gets a
route, a DNS entry, a VPN credential, an SSO refresh token, or an AWS credential.
Kubernetes credentials are minted here by your own exec plugin and proxied to the
box over a reverse-forwarded unix socket.

The tunnel dies when you exit the shell. That lifetime is the real control: while
it is up, anything on the box that can reach the forwarded port is talking to your
cluster as you, and km's egress enforcement does not see that traffic.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if o.contextName == "" {
				return fmt.Errorf("--context is required: name the kube context to tunnel (km tunnel %s --context <ctx>)", args[0])
			}
			return runTunnel(c.Context(), cfg, fetcher, execFn, args[0], o)
		},
	}

	cmd.Flags().StringVar(&o.contextName, "context", "", "Kube context to tunnel (required)")
	cmd.Flags().StringVar(&o.kubeconfig, "kubeconfig", "", "Override $KUBECONFIG / ~/.kube/config")
	// 2223, not 2222: `km vscode start` owns 2222 and having VS Code attached
	// to a box while tunnelling from it is a plausible combination.
	cmd.Flags().IntVar(&o.localPort, "local-port", 2223, "Local port for the SSM forward to sshd")
	cmd.Flags().IntVar(&o.bindPort, "bind-port", 6443, "Sandbox loopback port for the Kubernetes API")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "Resolve and print the target, ssh command, and box kubeconfig; connect to nothing")
	cmd.Flags().BoolVar(&o.printSSH, "print-ssh", false, "Print the ssh command and exit, so you can run it by hand")
	cmd.Flags().BoolVar(&o.verbose, "verbose", false, "Announce each leg as it comes up")

	return cmd
}

// runTunnel orchestrates the five legs. Order matters and failures unwind what
// has already been started.
func runTunnel(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, idArg string, o tunnelOpts) error {
	// 1. Resolve the operator's kubeconfig FIRST. It needs no AWS, no network
	//    and no sandbox, so a typo in --context costs a second rather than a
	//    round trip through Session Manager.
	paths := kubetunnel.KubeconfigPaths(o.kubeconfig)
	kc, err := kubetunnel.Load(paths)
	if err != nil {
		return err
	}
	target, err := kc.Resolve(o.contextName)
	if err != nil {
		return err
	}
	vlog(o.verbose, "resolved context %q → %s:%d (SNI %s) via %s",
		target.Context, target.ServerHost, target.ServerPort, target.TLSServerName, target.Exec.Command)

	boxKubeconfig, err := kubetunnel.RenderBoxKubeconfig(target, o.bindPort)
	if err != nil {
		return err
	}

	// --dry-run stops here deliberately: everything above is the part most
	// likely to be wrong, and it is verifiable with no VPN and no sandbox.
	if o.dryRun {
		return printTunnelDryRun(os.Stdout, idArg, target, boxKubeconfig, o)
	}

	// 2. Fail fast on a bound local port, before any AWS work.
	probeLn, probeErr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", o.localPort))
	if probeErr != nil {
		return fmt.Errorf("local port %d is already in use — pick a different one with --local-port (e.g. %d)",
			o.localPort, o.localPort+100)
	}
	probeLn.Close()

	sandboxID, err := ResolveSandboxID(ctx, cfg, idArg)
	if err != nil {
		return err
	}

	keyPath, err := sandboxKeyPath(sandboxID)
	if err != nil {
		return err
	}

	// --print-ssh needs the key path and sandbox id but nothing live, so it
	// also stops before any AWS call.
	if o.printSSH {
		return printTunnelSSH(os.Stdout, keyPath, target, o)
	}

	fetcher, execFn, err = resolveTunnelDeps(ctx, cfg, fetcher, execFn)
	if err != nil {
		return err
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	// 3. Bring the SSM forward up in the BACKGROUND. This is the structural
	//    difference from `km vscode start`, which runs the forward in the
	//    foreground as its last act — here the forward has to stay live
	//    underneath an ssh session we own.
	fwdCtx, cancelFwd := context.WithCancel(ctx)
	defer cancelFwd()

	region := rec.Region
	buildPF := func(c context.Context) *exec.Cmd {
		return buildPortForwardCmd(c, instanceID, region, strconv.Itoa(o.localPort), "22")
	}
	fwdDone := make(chan error, 1)
	go func() {
		fwdDone <- runReconnectingPortForward(fwdCtx, execFn, buildPF, sshBannerTunnelProbe(o.localPort), true, io.Discard)
	}()

	if err := waitForSSHD(fwdCtx, o.localPort, 60*time.Second); err != nil {
		cancelFwd()
		return fmt.Errorf("SSM port-forward to sshd never came up: %w", err)
	}
	vlog(o.verbose, "SSM forward up: 127.0.0.1:%d → %s:22", o.localPort, instanceID)

	// 4. Start the broker. Its log goes to stderr so mint lines interleave
	//    visibly with the operator's shell — the tunnel is interactive by
	//    design, so somebody is watching.
	broker, err := kubetunnel.NewBroker(target.Exec, os.Stderr)
	if err != nil {
		cancelFwd()
		return err
	}
	defer broker.Close()
	if err := broker.Serve(); err != nil {
		cancelFwd()
		return err
	}
	vlog(o.verbose, "credential broker listening on %s", broker.SocketPath())

	// 5. Provision the box over a non-interactive ssh on the same forward.
	//    NOT SSM send-command: that runs dash and mangles newlines in
	//    multi-line payloads, and we already have a working SSH path.
	provArgs := kubetunnel.BuildSSHArgs(kubetunnel.SSHOpts{
		KeyPath:       keyPath,
		LocalPort:     o.localPort,
		Interactive:   false,
		RemoteCommand: provisionScript(boxKubeconfig, kubetunnel.RenderShim(kubetunnel.BoxSocketPath)),
	})
	provCmd := exec.CommandContext(ctx, "ssh", provArgs...)
	provCmd.Stdout = io.Discard
	provCmd.Stderr = os.Stderr
	if err := execFn(provCmd); err != nil {
		cancelFwd()
		return fmt.Errorf("provision sandbox kubeconfig: %w", err)
	}
	vlog(o.verbose, "wrote %s and %s on the sandbox", kubetunnel.BoxKubeconfigPath, kubetunnel.BoxShimPath)

	// 6. The interactive session with both reverse forwards.
	sshArgs := kubetunnel.BuildSSHArgs(kubetunnel.SSHOpts{
		KeyPath:       keyPath,
		LocalPort:     o.localPort,
		Interactive:   true,
		BindPort:      o.bindPort,
		Target:        target,
		BrokerSocket:  broker.SocketPath(),
		RemoteCommand: kubetunnel.InteractiveRemoteCommand(),
	})

	printTunnelBanner(os.Stdout, sandboxID, target, o)

	sshCmd := exec.CommandContext(ctx, "ssh", sshArgs...)
	sshCmd.Stdin = os.Stdin
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr
	sshErr := execFn(sshCmd)

	cancelFwd()
	<-fwdDone

	if sshErr != nil {
		// Deliberately no retry. Reconnecting would lose the operator's shell
		// state anyway, so faking resilience buys nothing and hides the cause.
		return fmt.Errorf("tunnel dropped, re-run km tunnel: %w", sshErr)
	}
	return nil
}

// provisionScript builds the remote shell one-liner that creates ~/.km and
// writes both files. Content goes through base64 so newlines, quotes, and YAML
// indentation survive the trip through ssh's remote-command shell intact.
func provisionScript(kubeconfig []byte, shim string) string {
	kcB64 := base64.StdEncoding.EncodeToString(kubeconfig)
	shimB64 := base64.StdEncoding.EncodeToString([]byte(shim))

	return "set -e; " +
		"mkdir -p /home/sandbox/.km; chmod 700 /home/sandbox/.km; " +
		// curl is what the shim uses; failing here with a clear message beats
		// a kubectl "unparseable credential" much later.
		`command -v curl >/dev/null 2>&1 || { echo "km tunnel: curl not found on this sandbox; the credential shim needs it" >&2; exit 1; }; ` +
		"printf %s '" + kcB64 + "' | base64 -d > " + kubetunnel.BoxKubeconfigPath + "; " +
		"chmod 600 " + kubetunnel.BoxKubeconfigPath + "; " +
		"printf %s '" + shimB64 + "' | base64 -d > " + kubetunnel.BoxShimPath + "; " +
		"chmod 700 " + kubetunnel.BoxShimPath + "; " +
		// Stale socket from a crashed session would block the rebind; sshd's
		// StreamLocalBindUnlink handles it too, but clearing it here makes the
		// failure impossible rather than merely handled.
		"rm -f " + kubetunnel.BoxSocketPath + "; " +
		"echo km-tunnel-provisioned"
}

// waitForSSHD blocks until the forwarded port answers with an SSH banner.
func waitForSSHD(ctx context.Context, localPort int, timeout time.Duration) error {
	probe := sshBannerTunnelProbe(localPort)
	deadline := time.Now().Add(timeout)
	for {
		if probe() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no SSH banner on 127.0.0.1:%d after %s", localPort, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// sandboxKeyPath locates the per-sandbox private key written at create time.
func sandboxKeyPath(sandboxID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	p := filepath.Join(home, ".km", "keys", sandboxID)
	if _, statErr := os.Stat(p); statErr != nil {
		if os.IsNotExist(statErr) {
			return "", fmt.Errorf("private key for %s not found at %s. If you created this sandbox on a different machine, copy the ~/.km/keys/%s* files over",
				sandboxID, p, sandboxID)
		}
		return "", fmt.Errorf("stat private key: %w", statErr)
	}
	return p, nil
}

func resolveTunnelDeps(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc) (SandboxFetcher, ShellExecFunc, error) {
	if fetcher == nil {
		if cfg.StateBucket == "" {
			return nil, nil, fmt.Errorf("state bucket not configured")
		}
		awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if err != nil {
			return nil, nil, fmt.Errorf("load AWS config: %w", err)
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())
		_ = ssm.NewFromConfig(awsCfg) // parity with sibling verbs; no SSM calls here by design
	}
	if execFn == nil {
		execFn = defaultShellExec
	}
	return fetcher, execFn, nil
}

func vlog(on bool, format string, args ...any) {
	if on {
		fmt.Fprintf(os.Stderr, "[km tunnel] "+format+"\n", args...)
	}
}

func printTunnelDryRun(w io.Writer, idArg string, t *kubetunnel.Target, boxKubeconfig []byte, o tunnelOpts) error {
	args := kubetunnel.BuildSSHArgs(kubetunnel.SSHOpts{
		KeyPath:       "<~/.km/keys/" + idArg + ">",
		LocalPort:     o.localPort,
		Interactive:   true,
		BindPort:      o.bindPort,
		Target:        t,
		BrokerSocket:  "<broker socket>",
		RemoteCommand: kubetunnel.InteractiveRemoteCommand(),
	})

	fmt.Fprintf(w, "km tunnel --dry-run (nothing was connected)\n\n")
	fmt.Fprintf(w, "Resolved context:   %s\n", t.Context)
	fmt.Fprintf(w, "Cluster address:    %s:%d   (dialled from THIS machine, over your VPN)\n", t.ServerHost, t.ServerPort)
	fmt.Fprintf(w, "TLS server name:    %s\n", t.TLSServerName)
	fmt.Fprintf(w, "Credential plugin:  %s %v\n", t.Exec.Command, t.Exec.Args)
	fmt.Fprintf(w, "Sandbox API port:   127.0.0.1:%d\n\n", o.bindPort)
	fmt.Fprintf(w, "ssh command:\n  %s\n\n", kubetunnel.SSHCommandString(args))
	fmt.Fprintf(w, "kubeconfig that would be written to %s:\n\n%s\n", kubetunnel.BoxKubeconfigPath, boxKubeconfig)
	fmt.Fprintf(w, "Check the cluster address and plugin above against your own kubeconfig before running for real.\n")
	return nil
}

func printTunnelSSH(w io.Writer, keyPath string, t *kubetunnel.Target, o tunnelOpts) error {
	args := kubetunnel.BuildSSHArgs(kubetunnel.SSHOpts{
		KeyPath:       keyPath,
		LocalPort:     o.localPort,
		Interactive:   true,
		BindPort:      o.bindPort,
		Target:        t,
		BrokerSocket:  "<broker socket — start km tunnel normally to get one>",
		RemoteCommand: kubetunnel.InteractiveRemoteCommand(),
	})
	fmt.Fprintln(w, kubetunnel.SSHCommandString(args))
	return nil
}

func printTunnelBanner(w io.Writer, sandboxID string, t *kubetunnel.Target, o tunnelOpts) {
	fmt.Fprintf(w, "\n✓ Tunnel up to %s\n", sandboxID)
	fmt.Fprintf(w, "  cluster    %s:%d → sandbox 127.0.0.1:%d\n", t.ServerHost, t.ServerPort, o.bindPort)
	fmt.Fprintf(w, "  kubeconfig %s (already the default for this shell)\n", kubetunnel.BoxKubeconfigPath)
	fmt.Fprintf(w, "\n  Try:  kubectl get ns\n")
	fmt.Fprintf(w, "  The tunnel closes when you exit this shell.\n\n")
}
