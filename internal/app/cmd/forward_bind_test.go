package cmd

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A throwaway TCP server standing in for session-manager-plugin's loopback
// listener: echoes whatever it receives, prefixed, so a test can prove bytes
// crossed the relay in both directions.
func startEcho(t *testing.T) (addr string, port string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				n, _ := c.Read(buf)
				_, _ = c.Write(append([]byte("echo:"), buf[:n]...))
			}(c)
		}
	}()
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	return ln.Addr().String(), p
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	return p
}

func roundTrip(t *testing.T, addr, msg string) string {
	t.Helper()
	var c net.Conn
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err = net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(c)
	return string(out)
}

func TestForwardBindAddr_UnsetAndLoopbackMeanNoRelay(t *testing.T) {
	for _, v := range []string{"", "  ", "127.0.0.1", "localhost", "::1"} {
		t.Setenv(forwardBindEnv, v)
		if got := forwardBindAddr(); got != "" {
			t.Errorf("KM_FORWARD_BIND=%q: want no relay, got bind %q", v, got)
		}
	}
	t.Setenv(forwardBindEnv, "0.0.0.0")
	if got := forwardBindAddr(); got != "0.0.0.0" {
		t.Errorf("want 0.0.0.0, got %q", got)
	}
}

// The relay must carry bytes both ways between an outward-facing listener and
// the plugin's loopback port, and must stop listening when its context ends.
func TestForwardRelay_ProxiesBothWaysAndClosesWithContext(t *testing.T) {
	echoAddr, _ := startEcho(t)
	lp := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := startForwardRelay(ctx, "127.0.0.1", lp, echoAddr, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := roundTrip(t, "127.0.0.1:"+lp, "ping"); got != "echo:ping" {
		t.Fatalf("through relay: got %q", got)
	}

	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := net.DialTimeout("tcp", "127.0.0.1:"+lp, 100*time.Millisecond); err != nil {
			return // listener gone — correct
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("relay listener still accepting after context cancel")
}

// buildPortForwardCmd is the single chokepoint every forward (vscode, herdr,
// desktop, model, tunnel, shell --ports, codex auth) goes through. With the
// knob unset it must hand the operator's port straight to the plugin; with it
// set, the plugin gets a private loopback port and reconnects (a second call
// for the same local port under the same context) must reuse that port so the
// relay keeps pointing at a live listener.
func TestBuildPortForwardCmd_RelayRewritesPluginPortOnlyWhenBound(t *testing.T) {
	t.Setenv(forwardBindEnv, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := buildPortForwardCmd(ctx, "i-0abc", "us-east-1", "2222", "22")
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, `"localPortNumber":["2222"]`) {
		t.Fatalf("unset: plugin must bind the operator's port; args=%s", args)
	}

	t.Setenv(forwardBindEnv, "127.0.0.1")
	c = buildPortForwardCmd(ctx, "i-0abc", "us-east-1", "2222", "22")
	if !strings.Contains(strings.Join(c.Args, " "), `"localPortNumber":["2222"]`) {
		t.Fatalf("loopback bind: plugin already listens on loopback, no rewrite expected")
	}

	lp := freePort(t)
	t.Setenv(forwardBindEnv, "0.0.0.0")
	c1 := buildPortForwardCmd(ctx, "i-0abc", "us-east-1", lp, "22")
	p1 := pluginPortFromArgs(t, c1.Args)
	if p1 == lp {
		t.Fatalf("bound: plugin must get a private loopback port, not %s", lp)
	}
	if _, err := strconv.Atoi(p1); err != nil {
		t.Fatalf("plugin port %q is not numeric", p1)
	}
	c2 := buildPortForwardCmd(ctx, "i-0abc", "us-east-1", lp, "22")
	if p2 := pluginPortFromArgs(t, c2.Args); p2 != p1 {
		t.Fatalf("reconnect must reuse the plugin port: first %s, second %s", p1, p2)
	}
	// The outward listener exists on the operator's port for the life of ctx.
	if _, err := net.DialTimeout("tcp", "127.0.0.1:"+lp, 500*time.Millisecond); err != nil {
		t.Fatalf("relay listener not up on %s: %v", lp, err)
	}
}

func pluginPortFromArgs(t *testing.T, args []string) string {
	t.Helper()
	const key = `"localPortNumber":["`
	joined := strings.Join(args, " ")
	i := strings.Index(joined, key)
	if i < 0 {
		t.Fatalf("no localPortNumber in %s", joined)
	}
	rest := joined[i+len(key):]
	return rest[:strings.Index(rest, `"`)]
}
