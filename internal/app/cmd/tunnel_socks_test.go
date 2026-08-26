package cmd

import (
	"bytes"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

func TestTunnelSocks_DryRunTouchesNoAWS(t *testing.T) {
	execFn := func(*exec.Cmd) error {
		t.Fatal("dry-run must not execute anything")
		return nil
	}
	cmd := newTunnelCmdInternal(&config.Config{}, failingFetcher{t}, execFn)
	cmd.SetArgs([]string{"socks", "sb-1", "--dry-run"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	stdout := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("dry-run: %v", err)
		}
	})

	for _, want := range []string{
		"127.0.0.1:1080",           // default SOCKS bind port on the box
		"socks5h://127.0.0.1:1080", // socks5h, so names resolve operator-side
		"ExitOnForwardFailure=yes", // same silent-failure guard as the k8s mode
		"-R 1080",                  // destination-less -R: ssh itself is the proxy
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("socks dry-run missing %q\n---\n%s", want, stdout)
		}
	}

	// A destination on the -R would mean a plain port-forward, not a proxy.
	if strings.Contains(stdout, "-R 1080:") {
		t.Errorf("socks -R must carry NO destination, got:\n%s", stdout)
	}
	// No broker in this mode.
	if strings.Contains(stdout, "kubetoken.sock") {
		t.Errorf("socks mode must not forward a broker socket:\n%s", stdout)
	}
}

func TestTunnelSocks_ProxyEnvOffByDefault(t *testing.T) {
	cmd := newTunnelCmdInternal(&config.Config{}, failingFetcher{t}, nil)
	cmd.SetArgs([]string{"socks", "sb-1", "--dry-run"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	stdout := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("dry-run: %v", err)
		}
	})

	// Off by default is a considered choice, not caution: a blanket proxy
	// routes Bedrock/Anthropic/OpenAI calls away from km's metering MITM.
	if strings.Contains(stdout, "ALL_PROXY=socks5h") {
		t.Errorf("proxy env must be OFF unless --set-proxy-env is given:\n%s", stdout)
	}
}

func TestTunnelSocks_SetProxyEnvExcludesIMDS(t *testing.T) {
	cmd := newTunnelCmdInternal(&config.Config{}, failingFetcher{t}, nil)
	cmd.SetArgs([]string{"socks", "sb-1", "--dry-run", "--set-proxy-env"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	stdout := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("dry-run: %v", err)
		}
	})

	if !strings.Contains(stdout, "ALL_PROXY=socks5h://127.0.0.1:1080") {
		t.Errorf("--set-proxy-env should export ALL_PROXY:\n%s", stdout)
	}
	// Routing link-local IMDS through the operator's laptop breaks every AWS
	// call the sandbox makes, because that is how it gets its role credentials.
	if !strings.Contains(stdout, "169.254.169.254") {
		t.Errorf("NO_PROXY must exclude IMDS or the sandbox loses its IAM role:\n%s", stdout)
	}
}

func TestTunnelSocks_CustomBindPort(t *testing.T) {
	cmd := newTunnelCmdInternal(&config.Config{}, failingFetcher{t}, nil)
	cmd.SetArgs([]string{"socks", "sb-1", "--dry-run", "--bind-port", "1888"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	stdout := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("dry-run: %v", err)
		}
	})

	if !strings.Contains(stdout, "-R 1888") {
		t.Errorf("custom bind port not honoured:\n%s", stdout)
	}
}

func TestTunnelSocks_RejectsBadBindPort(t *testing.T) {
	for _, port := range []string{"0", "70000"} {
		cmd := newTunnelCmdInternal(&config.Config{}, failingFetcher{t}, nil)
		cmd.SetArgs([]string{"socks", "sb-1", "--dry-run", "--bind-port", port})
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})

		if err := cmd.Execute(); err == nil {
			t.Errorf("--bind-port %s should be rejected", port)
		}
	}
}

func TestTunnelSocks_LocalPortInUseFailsBeforeAWS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	cmd := newTunnelCmdInternal(&config.Config{}, failingFetcher{t}, nil)
	cmd.SetArgs([]string{"socks", "sb-1", "--local-port", strconv.Itoa(port)})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for a bound local port")
	} else if !strings.Contains(err.Error(), "--local-port") {
		t.Errorf("error should hint at --local-port, got: %v", err)
	}
}

func TestTunnelSocks_Registered(t *testing.T) {
	parent := newTunnelCmdInternal(&config.Config{}, failingFetcher{t}, nil)
	var found bool
	for _, c := range parent.Commands() {
		if c.Name() == "socks" {
			found = true
			break
		}
	}
	if !found {
		t.Error("km tunnel socks is not registered under km tunnel")
	}
}
