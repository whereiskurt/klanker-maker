package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/pkg/kubetunnel"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// socksOpts carries the flag set for one `km tunnel socks` invocation.
type socksOpts struct {
	localPort   int
	bindPort    int
	setProxyEnv bool
	dryRun      bool
	printSSH    bool
	verbose     bool
}

func newTunnelSocksCmd(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc) *cobra.Command {
	var o socksOpts

	cmd := &cobra.Command{
		Use:   "socks <sandbox-id>",
		Short: "Open a sandbox shell with a SOCKS proxy that egresses via your workstation",
		Long: `Open an interactive shell on a sandbox with a SOCKS5 proxy on its loopback that
egresses through THIS workstation — so the box reaches whatever your VPN reaches.

Unlike the k8s mode, nothing is written on the sandbox and there is no credential
broker: ssh itself is the proxy. Names resolve on your machine, over your VPN.

By default the proxy is available but not wired into the shell's environment; the
banner prints the export line. Use --set-proxy-env to have the shell start with it
already set, but read what that does to AI-spend metering first (see
docs/k8s-reverse-tunnel.md).

This is a wide path by construction: while it is open, anything on the box can reach
anything your workstation can. It dies when you exit the shell.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			return runTunnelSocks(c.Context(), cfg, fetcher, execFn, args[0], o)
		},
	}

	cmd.Flags().IntVar(&o.localPort, "local-port", 2223, "Local port for the SSM forward to sshd")
	cmd.Flags().IntVar(&o.bindPort, "bind-port", 1080, "Sandbox loopback port for the SOCKS proxy")
	cmd.Flags().BoolVar(&o.setProxyEnv, "set-proxy-env", false,
		"Start the shell with ALL_PROXY/HTTPS_PROXY/HTTP_PROXY already pointing at the tunnel (this stops km metering AI spend — see the runbook)")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "Print the ssh command that would run; connect to nothing")
	cmd.Flags().BoolVar(&o.printSSH, "print-ssh", false, "Print the ssh command and exit, so you can run it by hand")
	cmd.Flags().BoolVar(&o.verbose, "verbose", false, "Announce each leg as it comes up")

	return cmd
}

// runTunnelSocks is the socks mode's orchestration. It is deliberately shorter
// than the k8s mode: same transport, but no kubeconfig to resolve, no broker to
// run, and nothing to provision on the box — `ssh -R <port>` with no
// destination makes ssh itself the proxy.
func runTunnelSocks(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, idArg string, o socksOpts) error {
	if o.bindPort <= 0 || o.bindPort > 65535 {
		return fmt.Errorf("invalid --bind-port %d", o.bindPort)
	}

	buildArgs := func(keyPath string) []string {
		return kubetunnel.BuildSSHArgs(kubetunnel.SSHOpts{
			KeyPath:       keyPath,
			LocalPort:     o.localPort,
			Interactive:   true,
			SOCKSPort:     o.bindPort,
			RemoteCommand: kubetunnel.SOCKSRemoteCommand(o.bindPort, o.setProxyEnv),
		})
	}

	if o.dryRun {
		fmt.Printf("km tunnel socks --dry-run (nothing was connected)\n\n")
		fmt.Printf("Sandbox SOCKS port:  127.0.0.1:%d  (egresses via THIS machine)\n", o.bindPort)
		fmt.Printf("Proxy URL on the box: %s\n", kubetunnel.SOCKSProxyURL(o.bindPort))
		fmt.Printf("Shell proxy env:      %v\n\n", o.setProxyEnv)
		fmt.Printf("ssh command:\n  %s\n", kubetunnel.SSHCommandString(buildArgs("<~/.km/keys/"+idArg+">")))
		return nil
	}

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

	if o.printSSH {
		fmt.Println(kubetunnel.SSHCommandString(buildArgs(keyPath)))
		return nil
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

	printSocksBanner(os.Stdout, sandboxID, o)

	sshCmd := exec.CommandContext(ctx, "ssh", buildArgs(keyPath)...)
	sshCmd.Stdin = os.Stdin
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr
	sshErr := runInteractiveWithTerminalRestore(execFn, sshCmd)

	cancelFwd()
	<-fwdDone

	if sshErr != nil {
		return fmt.Errorf("tunnel dropped, re-run km tunnel socks: %w", sshErr)
	}
	return nil
}

func printSocksBanner(w io.Writer, sandboxID string, o socksOpts) {
	fmt.Fprintf(w, "\n✓ SOCKS tunnel up to %s\n", sandboxID)
	fmt.Fprintf(w, "  sandbox 127.0.0.1:%d → your workstation → your VPN\n\n", o.bindPort)

	if o.setProxyEnv {
		fmt.Fprintf(w, "  This shell already has ALL_PROXY/HTTPS_PROXY/HTTP_PROXY set.\n")
		fmt.Fprintf(w, "  NOTE: km cannot meter AI spend for traffic taking this path.\n")
	} else {
		fmt.Fprintf(w, "  To use it:  export ALL_PROXY=%s\n", kubetunnel.SOCKSProxyURL(o.bindPort))
		fmt.Fprintf(w, "  or per-command:  ALL_PROXY=%s curl https://internal.example.com\n",
			kubetunnel.SOCKSProxyURL(o.bindPort))
	}
	fmt.Fprintf(w, "\n  The tunnel closes when you exit this shell.\n\n")
}
