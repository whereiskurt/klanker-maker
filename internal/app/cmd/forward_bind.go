package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
)

// forwardBindEnv names the address km's SSM port-forwards listen on for the
// operator. Unset (or a loopback address) is the default and changes nothing:
// session-manager-plugin binds the local port itself, on "localhost".
//
// Set it to a non-loopback address — the operator container sets 0.0.0.0 — and
// km inserts a relay: the plugin is started on a private ephemeral loopback
// port and km listens on KM_FORWARD_BIND:<local-port>, copying bytes between
// the two. This exists because the plugin hard-codes its listener to
// "localhost" with no option to change it, so inside a container the forward
// lands on the CONTAINER's loopback and `docker run -p` can never reach it.
// The relay is the only way `km vscode/herdr/desktop/model/tunnel` and
// `km shell --ports` can be driven from a container and used from the host.
//
// Publish the ports on the host's loopback only (`-p 127.0.0.1:2222:2222`);
// 0.0.0.0 inside the container is what docker needs to see, not an invitation
// to expose an SSH tunnel into a sandbox to the LAN.
const forwardBindEnv = "KM_FORWARD_BIND"

// forwardBindAddr returns the configured outward bind address, or "" when the
// plugin's own loopback listener is all that is wanted. A loopback value is
// treated as unset: the plugin already does exactly that, and a relay from
// loopback to loopback would only add a hop.
func forwardBindAddr() string {
	v := strings.TrimSpace(os.Getenv(forwardBindEnv))
	if v == "" {
		return ""
	}
	if strings.EqualFold(v, "localhost") {
		return ""
	}
	if ip := net.ParseIP(v); ip != nil && ip.IsLoopback() {
		return ""
	}
	return v
}

// forwardRelays memoises operator-port → plugin-port for the life of a
// context. runReconnectingPortForward rebuilds the plugin command on every
// reconnect with the SAME context, so a second call for the same local port
// must hand back the same plugin port: the relay listener is already up and
// pointing there, and the plugin simply rebinds it.
var forwardRelays = struct {
	mu    sync.Mutex
	ports map[string]string
}{ports: map[string]string{}}

// relayPluginPort returns the loopback port the plugin should bind for an
// operator-facing localPort, starting the outward relay on first use. When no
// relay is configured it returns localPort unchanged — the plain path.
func relayPluginPort(ctx context.Context, localPort string, w io.Writer) string {
	bind := forwardBindAddr()
	if bind == "" {
		return localPort
	}
	forwardRelays.mu.Lock()
	defer forwardRelays.mu.Unlock()
	if p, ok := forwardRelays.ports[localPort]; ok {
		return p
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(w, "⚠ %s=%s: cannot allocate a loopback port for the relay (%v) — forwarding on loopback only\n", forwardBindEnv, bind, err)
		return localPort
	}
	_, pluginPort, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()
	if err := startForwardRelay(ctx, bind, localPort, "127.0.0.1:"+pluginPort, w); err != nil {
		fmt.Fprintf(w, "⚠ %s=%s: relay on %s failed (%v) — forwarding on loopback only\n", forwardBindEnv, bind, net.JoinHostPort(bind, localPort), err)
		return localPort
	}
	forwardRelays.ports[localPort] = pluginPort
	fmt.Fprintf(w, "  relay: %s → 127.0.0.1:%s (%s)\n", net.JoinHostPort(bind, localPort), pluginPort, forwardBindEnv)
	go func() {
		<-ctx.Done()
		forwardRelays.mu.Lock()
		if forwardRelays.ports[localPort] == pluginPort {
			delete(forwardRelays.ports, localPort)
		}
		forwardRelays.mu.Unlock()
	}()
	return pluginPort
}

// startForwardRelay listens on bind:localPort and pipes every accepted
// connection to target (the plugin's loopback listener). It returns once the
// listener is bound, so a caller can rely on the port being taken; the accept
// loop runs until ctx ends, which closes the listener. A dial failure on
// target — the plugin not up yet, or dropped — closes that one client
// connection and nothing else, mirroring what the plugin's own listener would
// have done: the client retries, the relay stays.
func startForwardRelay(ctx context.Context, bind, localPort, target string, w io.Writer) error {
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, localPort))
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return // listener closed with ctx
			}
			go func(client net.Conn) {
				defer client.Close()
				var d net.Dialer
				upstream, err := d.DialContext(ctx, "tcp", target)
				if err != nil {
					return
				}
				defer upstream.Close()
				done := make(chan struct{}, 2)
				cp := func(dst, src net.Conn) {
					_, _ = io.Copy(dst, src)
					if t, ok := dst.(*net.TCPConn); ok {
						_ = t.CloseWrite()
					}
					done <- struct{}{}
				}
				go cp(upstream, client)
				go cp(client, upstream)
				<-done
				<-done
			}(client)
		}
	}()
	return nil
}
