package kubetunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// TokenPath is the HTTP path the on-box shim requests. It is part of the
// contract between RenderShim and the broker; changing one without the other
// yields a 404 that surfaces as an unparseable-credential error in kubectl.
const TokenPath = "/token"

// PluginRunner executes a credential plugin and returns its streams separately.
// It exists as a seam so the broker is testable on a machine with no plugin,
// no cluster and no VPN — which is every machine this repo is developed on.
type PluginRunner func(ctx context.Context, e *ExecConfig) (stdout, stderr []byte, err error)

// Broker serves Kubernetes exec-credentials over a unix socket on the
// OPERATOR'S workstation. `km tunnel` reverse-forwards this socket into the
// sandbox, so the box can ask for a token without ever holding the credential
// that mints one.
//
// The broker is deliberately the dumbest component in the phase. It does not
// parse, validate, cache, retry, or rewrite the plugin's output — kubectl
// already caches on status.expirationTimestamp, and the plugin owns everything
// about OIDC. Every assumption the broker avoids is one that cannot be wrong on
// first contact with a real cluster, which matters because that first contact
// happens on a machine this code was never run on.
//
// A unix socket rather than a TCP port is load-bearing: filesystem permissions
// then genuinely gate access, there is no port to collide with another
// operator's tunnel, and no bearer-token scheme is needed to make it safe.
type Broker struct {
	dir        string
	socketPath string
	exec       *ExecConfig
	log        io.Writer
	runner     PluginRunner

	srv *http.Server
	ln  net.Listener

	closeOnce sync.Once
	closeErr  error
}

// NewBroker creates the socket directory but does not listen yet.
func NewBroker(e *ExecConfig, logOut io.Writer) (*Broker, error) {
	if e == nil {
		return nil, fmt.Errorf("broker requires an exec config")
	}
	if logOut == nil {
		logOut = io.Discard
	}

	dir, err := os.MkdirTemp("", "km-broker-")
	if err != nil {
		return nil, fmt.Errorf("create broker socket dir: %w", err)
	}
	// MkdirTemp is already 0700 on Unix, but assert it rather than assume:
	// the directory mode is the socket's ONLY access control.
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("secure broker socket dir: %w", err)
	}

	return &Broker{
		dir:        dir,
		socketPath: filepath.Join(dir, "broker.sock"),
		exec:       e,
		log:        logOut,
		runner:     defaultRunner,
	}, nil
}

// SocketPath is the local side of the `-R <remote-socket>:<local-socket>` spec.
func (b *Broker) SocketPath() string { return b.socketPath }

// SetRunner overrides the plugin runner. Test seam.
func (b *Broker) SetRunner(r PluginRunner) { b.runner = r }

// Serve binds the socket and starts accepting. It returns as soon as the
// listener is up, so the caller can proceed to open ssh.
func (b *Broker) Serve() error {
	ln, err := net.Listen("unix", b.socketPath)
	if err != nil {
		return fmt.Errorf("listen on broker socket %s: %w", b.socketPath, err)
	}
	b.ln = ln

	mux := http.NewServeMux()
	mux.HandleFunc(TokenPath, b.handleToken)
	b.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		// http.ErrServerClosed is the normal shutdown path.
		_ = b.srv.Serve(ln)
	}()
	return nil
}

// handleToken runs the plugin and returns its stdout unchanged.
//
// The request body is deliberately never read. The shim may send the box's
// KUBERNETES_EXEC_INFO, but that describes the fake 127.0.0.1 endpoint the box
// dials, so forwarding it to a plugin expecting the real cluster would be
// actively misleading. Plugins requiring provideClusterInfo are unsupported;
// kubelogin does not need it.
func (b *Broker) handleToken(w http.ResponseWriter, r *http.Request) {
	stdout, stderr, err := b.runner(r.Context(), b.exec)
	stamp := time.Now().UTC().Format(time.RFC3339)

	if err != nil {
		fmt.Fprintf(b.log, "[km-tunnel %s] credential mint failed: %v\n", stamp, err)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		// Surface the plugin's own stderr: it is the only diagnostic the
		// operator gets, and it lands on the terminal they are already
		// watching because the tunnel is interactive by design.
		fmt.Fprintf(w, "credential plugin failed: %v\n%s", err, stderr)
		return
	}

	fmt.Fprintf(b.log, "[km-tunnel %s] credential mint ok (%d bytes)\n", stamp, len(stdout))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(stdout)
}

// Close shuts the server down and removes the socket directory. It is safe to
// call more than once: the command layer defers it and may also call it on an
// error path.
func (b *Broker) Close() error {
	b.closeOnce.Do(func() {
		if b.srv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = b.srv.Shutdown(ctx)
		}
		if b.ln != nil {
			_ = b.ln.Close()
		}
		b.closeErr = os.RemoveAll(b.dir)
	})
	return b.closeErr
}

// defaultRunner executes the operator's real credential plugin.
func defaultRunner(ctx context.Context, e *ExecConfig) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, e.Command, e.Args...) //nolint:gosec // operator's own kubeconfig plugin, by design

	// Inheriting the environment is load-bearing, not laziness: kubelogin
	// resolves its token cache under $HOME and finds a browser opener via
	// $PATH, so a clean environment would break the refresh flow this exists
	// to support. Exec env entries are appended so they win on conflict.
	env := os.Environ()
	for _, ev := range e.Env {
		env = append(env, ev.Name+"="+ev.Value)
	}
	cmd.Env = env

	// No stdin and no TTY: the plugin opens a browser rather than prompting,
	// and claiming interactivity we cannot honour from inside an HTTP handler
	// would just make failures stranger.
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	if err != nil {
		err = fmt.Errorf("run credential plugin %q: %w", e.Command, err)
	}
	return outBuf.Bytes(), errBuf.Bytes(), err
}
