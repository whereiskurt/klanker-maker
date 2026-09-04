// Command km-env asks km-secretsd for secrets and execs one command with them
// in its environment.
//
// There is deliberately NO export verb and no `eval $(km-env)` form: that would
// put the bundle straight back into a shell, which is the entire thing this
// phase removes. See TestNoShellExportVerbExists.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

func main() {
	verb, req, cmd, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "km-env: %v\n", err)
		fmt.Fprintln(os.Stderr, "usage: km-env exec [--as NAME] [--only K1,K2] -- <cmd> [args...]")
		fmt.Fprintln(os.Stderr, "       km-env list")
		os.Exit(2)
	}

	resp, err := unseal(secrets.SocketPath, req)
	if err != nil {
		// Fail closed and loudly. Running the command without its secrets
		// produces a confusing 401 far from the real cause.
		fmt.Fprintf(os.Stderr, "km-env: %v\n", err)
		fmt.Fprintf(os.Stderr, "km-env: is km-secretsd running? try: systemctl status km-secretsd\n")
		os.Exit(1)
	}

	if verb == "list" {
		for _, k := range resp.Keys {
			fmt.Println(k) // NAMES only, never values
		}
		return
	}

	bin, err := exec.LookPath(cmd[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "km-env: %v\n", err)
		os.Exit(127)
	}
	env := buildEnv(os.Environ(), resp.Values)
	// execve replaces this process, so km-env never lingers holding secrets.
	if err := syscall.Exec(bin, cmd, env); err != nil {
		fmt.Fprintf(os.Stderr, "km-env: exec %s: %v\n", bin, err)
		os.Exit(126)
	}
}

func parseArgs(argv []string) (string, secrets.UnsealRequest, []string, error) {
	var req secrets.UnsealRequest
	if len(argv) == 0 {
		return "", req, nil, errors.New("missing verb")
	}
	verb := argv[0]
	if verb != "exec" && verb != "list" {
		return "", req, nil, fmt.Errorf("unknown verb %q", verb)
	}

	rest := argv[1:]
	var cmd []string
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--as":
			if i+1 >= len(rest) {
				return "", req, nil, errors.New("--as needs a value")
			}
			i++
			req.As = rest[i]
		case "--only":
			if i+1 >= len(rest) {
				return "", req, nil, errors.New("--only needs a value")
			}
			i++
			for _, k := range strings.Split(rest[i], ",") {
				if k = strings.TrimSpace(k); k != "" {
					req.Only = append(req.Only, k)
				}
			}
		case "--":
			cmd = rest[i+1:]
			i = len(rest)
		default:
			return "", req, nil, fmt.Errorf("unexpected argument %q", rest[i])
		}
	}

	if verb == "exec" && len(cmd) == 0 {
		return "", req, nil, errors.New("exec needs a command after --")
	}
	return verb, req, cmd, nil
}

func unseal(socketPath string, req secrets.UnsealRequest) (secrets.UnsealResponse, error) {
	var resp secrets.UnsealResponse

	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return resp, fmt.Errorf("cannot reach the secrets broker at %s: %w", socketPath, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return resp, fmt.Errorf("send request: %w", err)
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return resp, fmt.Errorf("read response: %w", err)
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// buildEnv overlays unsealed values onto base, replacing any same-named entry.
func buildEnv(base []string, vals map[string][]byte) []string {
	out := make([]string, 0, len(base)+len(vals))
	for _, e := range base {
		name, _, found := strings.Cut(e, "=")
		if found {
			if _, shadowed := vals[name]; shadowed {
				continue
			}
		}
		out = append(out, e)
	}
	for k, v := range vals {
		out = append(out, k+"="+string(v))
	}
	return out
}
