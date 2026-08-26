package kubetunnel

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// BoxKubeconfigPath is where km tunnel writes the sandbox's kubeconfig.
const BoxKubeconfigPath = "/home/sandbox/.km/kubeconfig"

// BoxShimPath is where km tunnel writes the credential shim. It must be on the
// sandbox user's PATH for kubectl's exec stanza to find it by bare name.
const BoxShimPath = "/home/sandbox/.km/km-kubetoken"

// BoxSocketPath is the sandbox-side end of the reverse-forwarded broker socket.
const BoxSocketPath = "/home/sandbox/.km/kubetoken.sock"

// BoxUser is the sandbox account km connects as; it owns the sshd session that
// binds both reverse forwards.
const BoxUser = "sandbox"

// RenderBoxKubeconfig builds the kubeconfig written onto the sandbox.
//
// The server is the loopback bind, and the real cluster name appears ONLY as
// tls-server-name. That split is what lets the box verify the real certificate
// while dialling 127.0.0.1, which in turn is why km needs no /etc/hosts entry
// and no root on the box — and why the loopback port is free to differ from the
// cluster's real port.
func RenderBoxKubeconfig(t *Target, bindPort int) ([]byte, error) {
	if t == nil {
		return nil, fmt.Errorf("nil target")
	}
	if t.TLSServerName == "" {
		return nil, fmt.Errorf("target has no TLS server name; the box could not verify the cluster certificate")
	}
	if bindPort <= 0 || bindPort > 65535 {
		return nil, fmt.Errorf("invalid bind port %d", bindPort)
	}

	doc := map[string]any{
		"apiVersion": "v1",
		"kind":       "Config",
		// current-context is set so a bare `kubectl` works on the box with no
		// --context; an agent shelling in should not need to know km's naming.
		"current-context": t.Context,
		"clusters": []map[string]any{{
			"name": t.Context,
			"cluster": map[string]any{
				"server":          fmt.Sprintf("https://127.0.0.1:%d", bindPort),
				"tls-server-name": t.TLSServerName,
			},
		}},
		"contexts": []map[string]any{{
			"name": t.Context,
			"context": map[string]any{
				"cluster": t.Context,
				"user":    t.Context,
			},
		}},
		"users": []map[string]any{{
			"name": t.Context,
			"user": map[string]any{
				"exec": map[string]any{
					"apiVersion": "client.authentication.k8s.io/v1",
					"command":    BoxShimPath,
					// interactiveMode is REQUIRED under client.authentication.k8s.io/v1
					// (unlike v1beta1, which defaults it). Omitting it makes kubectl
					// reject the exec config outright with "interactiveMode must be
					// specified", before the plugin ever runs. "Never" is the honest
					// value: the shim curls a unix socket and never wants a TTY — any
					// browser hop happens on the operator's workstation, inside their
					// own plugin, not here.
					"interactiveMode": "Never",
				},
			},
		}},
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal box kubeconfig: %w", err)
	}
	return out, nil
}

// RenderShim builds the on-box credential shim.
//
// exec (rather than a plain call) is deliberate: kubectl distinguishes a plugin
// that failed from one that returned junk, so curl's exit status must become
// the shim's. Swallowing it would turn a broker error into a much less
// diagnosable "unparseable credential".
func RenderShim(sockPath string) string {
	return "#!/bin/sh\n" +
		"# Written by `km tunnel`. Asks the operator's workstation for a\n" +
		"# Kubernetes credential over the reverse-forwarded unix socket.\n" +
		"exec curl --silent --show-error --fail-with-body \\\n" +
		"  --unix-socket " + sockPath + " http://localhost" + TokenPath + "\n"
}

// SSHOpts describes one ssh invocation.
type SSHOpts struct {
	// KeyPath is the per-sandbox private key (~/.km/keys/<id>).
	KeyPath string
	// LocalPort is the laptop port the SSM forward bound to sshd.
	LocalPort int
	// Interactive requests a TTY and both reverse forwards. When false this is
	// the provisioning invocation: no TTY, no forwards, one remote command.
	Interactive bool
	// BindPort is the sandbox loopback port for the Kubernetes API.
	BindPort int
	// Target supplies the real cluster host and port, dialled operator-side.
	Target *Target
	// BrokerSocket is the laptop-side socket the box socket forwards to.
	BrokerSocket string
	// RemoteCommand is run instead of the default shell. For the provisioning
	// invocation it is the setup script; for the interactive one it is the
	// login shell wrapped in `env KUBECONFIG=...`, which is how the box's
	// kubeconfig becomes the default without writing to ~/.kube/config and
	// clobbering whatever was already there.
	RemoteCommand string
}

// InteractiveRemoteCommand returns the login shell wrapped so that kubectl in
// the tunnel shell finds the generated kubeconfig with no further setup.
//
// Setting it here rather than appending an export to ~/.bashrc is deliberate:
// the tunnel is per-session, and a persistent export would outlive it and point
// a later shell at a dead socket.
func InteractiveRemoteCommand() string {
	return "env KUBECONFIG=" + BoxKubeconfigPath + " bash -l"
}

// BuildSSHArgs constructs the ssh argv (excluding the "ssh" binary itself).
func BuildSSHArgs(o SSHOpts) []string {
	args := []string{
		"-i", o.KeyPath,
		"-p", strconv.Itoa(o.LocalPort),
		// The SSM forward is loopback-only and the host key changes whenever a
		// sandbox is recreated behind the same local port, so the usual
		// known_hosts protection has nothing to protect here and would only
		// produce spurious MITM warnings.
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		// ExitOnForwardFailure is THE most important option in this phase.
		// A failed -R bind is only a WARNING by default, so without it the
		// operator gets a perfectly good shell attached to a dead tunnel and
		// an inexplicable "connection refused" from kubectl. Two operators
		// tunnelling to one sandbox is exactly how that happens.
		"-o", "ExitOnForwardFailure=yes",
		// Without this a leftover socket from a crashed session blocks the
		// rebind, which surfaces as the same confusing failure.
		"-o", "StreamLocalBindUnlink=yes",
		"-o", "ServerAliveInterval=30",
	}

	if o.Interactive {
		args = append(args, "-t")
		if o.Target != nil {
			args = append(args, "-R", fmt.Sprintf("%d:%s:%d", o.BindPort, o.Target.ServerHost, o.Target.ServerPort))
		}
		args = append(args, "-R", fmt.Sprintf("%s:%s", BoxSocketPath, o.BrokerSocket))
	} else {
		args = append(args, "-T")
	}

	args = append(args, fmt.Sprintf("%s@127.0.0.1", BoxUser))

	if o.RemoteCommand != "" {
		args = append(args, o.RemoteCommand)
	}
	return args
}

// SSHCommandString renders an argv as a copy-pasteable shell command, for
// --print-ssh. This is a first-class escape hatch rather than debug output:
// the operator cannot exercise this code path on the machine it was written on,
// so being able to bypass km's orchestration entirely and still get work done
// is worth a permanent flag.
func SSHCommandString(args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, "ssh")
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// shellQuote single-quotes an argument when it contains anything the shell
// would interpret.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '@' || r == '=' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
