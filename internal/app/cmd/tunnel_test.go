package cmd

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/kubetunnel"
)

// failingFetcher fails the test if any AWS-backed call is made. The dry-run and
// print-ssh paths must be usable on a machine with no credentials, since that is
// how the operator will first check their kubeconfig resolution.
type failingFetcher struct{ t *testing.T }

func (f failingFetcher) FetchSandbox(context.Context, string) (*kmaws.SandboxRecord, error) {
	f.t.Fatal("no AWS call should be made on this path")
	return nil, nil
}

func writeKubeconfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config")
	body := `apiVersion: v1
kind: Config
clusters:
  - name: k8s1
    cluster:
      server: https://k8s1.corp:6443
      tls-server-name: k8s1.corp
contexts:
  - name: k8s1
    context:
      cluster: k8s1
      user: k8s1-oidc
users:
  - name: k8s1-oidc
    user:
      exec:
        apiVersion: client.authentication.k8s.io/v1
        command: kubelogin
        args: ["get-token"]
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return p
}

func TestTunnelCmd_RequiresContext(t *testing.T) {
	cmd := newTunnelCmdInternal(&config.Config{}, failingFetcher{t}, nil)
	cmd.SetArgs([]string{"k8s", "sb-1"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error when --context is omitted")
	}
	if !strings.Contains(err.Error(), "--context") {
		t.Errorf("error should name --context, got: %v", err)
	}
}

func TestTunnelCmd_DryRunTouchesNoAWS(t *testing.T) {
	kubeconfig := writeKubeconfig(t)

	out := &bytes.Buffer{}
	execFn := func(*exec.Cmd) error {
		t.Fatal("dry-run must not execute anything")
		return nil
	}

	cmd := newTunnelCmdInternal(&config.Config{}, failingFetcher{t}, execFn)
	cmd.SetArgs([]string{"k8s", "sb-1", "--context", "k8s1", "--kubeconfig", kubeconfig, "--dry-run"})
	cmd.SetOut(out)
	cmd.SetErr(out)

	// runTunnel writes its dry-run report to os.Stdout; capture it.
	stdout := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("dry-run: %v", err)
		}
	})

	for _, want := range []string{
		"k8s1.corp:6443", // the real cluster address, dialled operator-side
		"kubelogin",      // the plugin km resolved
		"ExitOnForwardFailure=yes",
		"127.0.0.1:6443",  // the loopback bind the box will use
		"tls-server-name", // proof the generated kubeconfig is shown
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("dry-run output missing %q\n---\n%s", want, stdout)
		}
	}
}

func TestTunnelCmd_DryRunSurfacesBadContext(t *testing.T) {
	kubeconfig := writeKubeconfig(t)

	cmd := newTunnelCmdInternal(&config.Config{}, failingFetcher{t}, nil)
	cmd.SetArgs([]string{"k8s", "sb-1", "--context", "typo", "--kubeconfig", kubeconfig, "--dry-run"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for an unknown context")
	}
	// The whole point of --dry-run is catching this with no VPN and no sandbox,
	// so the error must say what IS available.
	if !strings.Contains(err.Error(), "k8s1") {
		t.Errorf("error should list available contexts, got: %v", err)
	}
}

func TestTunnelCmd_LocalPortInUseFailsBeforeAWS(t *testing.T) {
	kubeconfig := writeKubeconfig(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	cmd := newTunnelCmdInternal(&config.Config{}, failingFetcher{t}, nil)
	cmd.SetArgs([]string{
		"k8s", "sb-1", "--context", "k8s1", "--kubeconfig", kubeconfig,
		"--local-port", strconv.Itoa(port),
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err = cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for a bound local port")
	}
	if !strings.Contains(err.Error(), "--local-port") {
		t.Errorf("error should hint at --local-port, got: %v", err)
	}
}

func TestTunnelCmd_Registered(t *testing.T) {
	// A verb that builds but is never added to root is invisible; this is the
	// cheapest guard against that.
	root := NewRootCmd(&config.Config{})
	var tunnel *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "tunnel" {
			tunnel = c
			break
		}
	}
	if tunnel == nil {
		t.Fatal("km tunnel is not registered on the root command")
	}

	// tunnel is a family, not a single command: modes are subcommands so a
	// second one does not have to arrive as a mutually-exclusive boolean.
	var haveK8s bool
	for _, c := range tunnel.Commands() {
		if c.Name() == "k8s" {
			haveK8s = true
			break
		}
	}
	if !haveK8s {
		t.Error("km tunnel k8s is not registered under km tunnel")
	}
}

func TestTunnelCmd_BareParentIsNotRunnable(t *testing.T) {
	// `km tunnel <id>` must not silently do something. Naming the mode is what
	// keeps room for `km tunnel socks` without a breaking change later.
	cmd := newTunnelCmdInternal(&config.Config{}, failingFetcher{t}, nil)
	cmd.SetArgs([]string{"sb-1"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err == nil {
		t.Error("bare `km tunnel <id>` should not run a mode; expected an error")
	}
}

func TestProvisionScript(t *testing.T) {
	kc := []byte("apiVersion: v1\nkind: Config\n")
	script := provisionScript(kc, kubetunnel.RenderShim(kubetunnel.BoxSocketPath))

	for _, want := range []string{
		"set -e",
		"mkdir -p /home/sandbox/.km",
		"chmod 700 /home/sandbox/.km",
		"command -v curl", // the shim depends on it; fail loudly and early
		"base64 -d",       // content travels base64 so YAML survives the remote shell
		kubetunnel.BoxKubeconfigPath,
		kubetunnel.BoxShimPath,
		"rm -f " + kubetunnel.BoxSocketPath, // a stale socket would block the rebind
	} {
		if !strings.Contains(script, want) {
			t.Errorf("provision script missing %q\n---\n%s", want, script)
		}
	}

	// Raw YAML must never appear inline: a newline in the remote command would
	// truncate the script at the first line break.
	if strings.Contains(script, "apiVersion: v1") {
		t.Errorf("kubeconfig must be base64-encoded, not inlined:\n%s", script)
	}
	if strings.Contains(script, "\n") {
		t.Errorf("provision script must be a single line, got:\n%s", script)
	}
}
