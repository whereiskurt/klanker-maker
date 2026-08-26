package kubetunnel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// socketClient returns an http.Client that dials the broker's unix socket.
func socketClient(sock string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
}

func newTestBroker(t *testing.T, runner PluginRunner) (*Broker, *bytes.Buffer) {
	t.Helper()
	logBuf := &bytes.Buffer{}
	b, err := NewBroker(&ExecConfig{Command: "stub"}, logBuf)
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	b.SetRunner(runner)
	if err := b.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b, logBuf
}

func TestBroker_SocketDirIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets and mode bits are POSIX-only")
	}
	b, err := NewBroker(&ExecConfig{Command: "stub"}, io.Discard)
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	defer b.Close()

	// The socket's ONLY access control is the filesystem, so this mode is
	// load-bearing rather than hygiene.
	info, err := os.Stat(filepath.Dir(b.SocketPath()))
	if err != nil {
		t.Fatalf("stat socket dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("socket dir mode = %#o, want 0700", perm)
	}
}

func TestBroker_PassesStdoutThroughVerbatim(t *testing.T) {
	// Trailing whitespace and odd spacing must survive: the broker must not
	// parse or re-serialise the ExecCredential, or it becomes a second thing
	// that can disagree with what the plugin actually said.
	const raw = "{\"apiVersion\":\"client.authentication.k8s.io/v1\",\"status\":{\"token\":\"tok\"}}   \n"

	b, logBuf := newTestBroker(t, func(context.Context, *ExecConfig) ([]byte, []byte, error) {
		return []byte(raw), nil, nil
	})

	resp, err := socketClient(b.SocketPath()).Get("http://localhost/token")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != raw {
		t.Errorf("body = %q, want byte-identical %q", body, raw)
	}
	if !strings.Contains(logBuf.String(), "mint") {
		t.Errorf("expected a mint log line, got: %q", logBuf.String())
	}
}

func TestBroker_PluginFailureSurfacesStderr(t *testing.T) {
	b, logBuf := newTestBroker(t, func(context.Context, *ExecConfig) ([]byte, []byte, error) {
		return nil, []byte("could not refresh token: browser closed"), errors.New("exit status 1")
	})

	resp, err := socketClient(b.SocketPath()).Get("http://localhost/token")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	// The operator reads this on their own terminal; swallowing the plugin's
	// stderr would turn a diagnosable auth failure into a blank 500.
	if !strings.Contains(string(body), "browser closed") {
		t.Errorf("body should carry the plugin stderr, got: %q", body)
	}
	if !strings.Contains(logBuf.String(), "failed") {
		t.Errorf("expected a failure log line, got: %q", logBuf.String())
	}
}

func TestBroker_UnknownPathIs404(t *testing.T) {
	b, _ := newTestBroker(t, func(context.Context, *ExecConfig) ([]byte, []byte, error) {
		t.Error("runner must not be called for an unknown path")
		return nil, nil, nil
	})

	resp, err := socketClient(b.SocketPath()).Get("http://localhost/nope")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestBroker_CloseRemovesSocketAndIsIdempotent(t *testing.T) {
	b, err := NewBroker(&ExecConfig{Command: "stub"}, io.Discard)
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	b.SetRunner(func(context.Context, *ExecConfig) ([]byte, []byte, error) { return []byte("{}"), nil, nil })
	if err := b.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	dir := filepath.Dir(b.SocketPath())
	if _, err := os.Stat(b.SocketPath()); err != nil {
		t.Fatalf("socket should exist after Serve: %v", err)
	}

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("socket dir should be gone after Close, stat err = %v", err)
	}

	// The command layer defers Close and may also call it on an error path.
	if err := b.Close(); err != nil {
		t.Errorf("second Close should be a no-op, got: %v", err)
	}
}

func TestBroker_DoesNotReadRequestBody(t *testing.T) {
	// KUBERNETES_EXEC_INFO must not be forwarded: the box's cluster info
	// describes the fake 127.0.0.1 endpoint, so passing it to a plugin that
	// expects the real cluster would be actively misleading.
	var sawExec *ExecConfig
	b, _ := newTestBroker(t, func(_ context.Context, e *ExecConfig) ([]byte, []byte, error) {
		sawExec = e
		return []byte("{}"), nil, nil
	})

	resp, err := socketClient(b.SocketPath()).Post(
		"http://localhost/token", "application/json",
		strings.NewReader(`{"kind":"ExecCredential","spec":{"cluster":{"server":"https://127.0.0.1:6443"}}}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if sawExec == nil || sawExec.Command != "stub" {
		t.Errorf("runner should receive the configured exec unchanged, got %+v", sawExec)
	}
}

func TestDefaultRunner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses /bin/sh")
	}
	dir := t.TempDir()

	writeStub := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o700); err != nil { //nolint:gosec // test stub must be executable
			t.Fatalf("write stub: %v", err)
		}
		return p
	}

	t.Run("separates stdout from stderr", func(t *testing.T) {
		p := writeStub("ok.sh", "#!/bin/sh\necho 'OUT'\necho 'ERR' >&2\n")
		out, errOut, err := defaultRunner(context.Background(), &ExecConfig{Command: p})
		if err != nil {
			t.Fatalf("runner: %v", err)
		}
		if strings.TrimSpace(string(out)) != "OUT" {
			t.Errorf("stdout = %q, want OUT", out)
		}
		if strings.TrimSpace(string(errOut)) != "ERR" {
			t.Errorf("stderr = %q, want ERR", errOut)
		}
	})

	t.Run("propagates a non-zero exit with stderr intact", func(t *testing.T) {
		p := writeStub("fail.sh", "#!/bin/sh\necho 'boom' >&2\nexit 3\n")
		_, errOut, err := defaultRunner(context.Background(), &ExecConfig{Command: p})
		if err == nil {
			t.Fatal("expected an error for exit 3")
		}
		if !strings.Contains(string(errOut), "boom") {
			t.Errorf("stderr = %q, want it to contain boom", errOut)
		}
	})

	t.Run("passes args through", func(t *testing.T) {
		p := writeStub("args.sh", "#!/bin/sh\necho \"$1|$2\"\n")
		out, _, err := defaultRunner(context.Background(), &ExecConfig{Command: p, Args: []string{"a", "b"}})
		if err != nil {
			t.Fatalf("runner: %v", err)
		}
		if strings.TrimSpace(string(out)) != "a|b" {
			t.Errorf("stdout = %q, want a|b", out)
		}
	})

	t.Run("inherits the environment and appends exec env", func(t *testing.T) {
		// Inheriting is load-bearing: kubelogin finds its token cache under
		// $HOME and its browser opener via $PATH, so a clean environment
		// would break the very refresh flow this exists to support.
		p := writeStub("env.sh", "#!/bin/sh\necho \"$KM_TEST_INHERITED|$KM_TEST_ADDED\"\n")
		t.Setenv("KM_TEST_INHERITED", "inherited")
		out, _, err := defaultRunner(context.Background(), &ExecConfig{
			Command: p,
			Env:     []ExecEnv{{Name: "KM_TEST_ADDED", Value: "added"}},
		})
		if err != nil {
			t.Fatalf("runner: %v", err)
		}
		if strings.TrimSpace(string(out)) != "inherited|added" {
			t.Errorf("stdout = %q, want inherited|added", out)
		}
	})

	t.Run("missing command names the command", func(t *testing.T) {
		_, _, err := defaultRunner(context.Background(), &ExecConfig{Command: "km-no-such-plugin-xyz"})
		if err == nil {
			t.Fatal("expected an error for a missing command")
		}
		if !strings.Contains(err.Error(), "km-no-such-plugin-xyz") {
			t.Errorf("error should name the command, got: %v", err)
		}
	})

	t.Run("honours context cancellation", func(t *testing.T) {
		p := writeStub("slow.sh", "#!/bin/sh\nsleep 30\n")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := defaultRunner(ctx, &ExecConfig{Command: p}); err == nil {
			t.Error("expected an error from a cancelled context")
		}
	})
}
