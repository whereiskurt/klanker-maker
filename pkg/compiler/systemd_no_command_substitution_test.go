package compiler

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// systemd does NOT perform command substitution in ExecStart. A bare
// `--flag $(cmd)` argument is passed through literally, so the program receives
// "$(cmd" as its value and fails to parse it.
//
// This is invisible to every other check: the Go code compiles, the template
// renders, string-presence assertions pass, and the unit file looks right to a
// reader. The failure only appears on a booted box as a crash-looping unit —
// and for km-ebpf-enforcer specifically that means a sandbox with NO network
// enforcement at all, which is exactly the silent-success shape Phase 135
// exists to eliminate.
//
// The established idiom in this file when a value must be computed at start
// time is an ExecStartPre that writes it into an EnvironmentFile, then
// ${VAR} in ExecStart -- see the KM_HTTP_PROXY_PID handling.
func TestUserdata_NoCommandSubstitutionInExecStartArgs(t *testing.T) {
	b, err := os.ReadFile("userdata.go")
	if err != nil {
		t.Fatal(err)
	}

	// A continued ExecStart argument: leading whitespace, a --flag, then $(...).
	contArg := regexp.MustCompile(`(?m)^\s+--[A-Za-z0-9-]+\s+\$\(`)
	// An ExecStart= line using $(...) without handing it to a shell.
	execLine := regexp.MustCompile(`(?m)^ExecStart[A-Za-z]*=.*\$\(`)

	for _, m := range contArg.FindAllString(string(b), -1) {
		t.Errorf("systemd will not expand this ExecStart argument: %q\n"+
			"  Use an ExecStartPre that writes the value to an EnvironmentFile, "+
			"then ${VAR} -- or let the program resolve it itself.",
			strings.TrimSpace(m))
	}
	for _, m := range execLine.FindAllString(string(b), -1) {
		if strings.Contains(m, "sh -c") || strings.Contains(m, "bash -c") {
			continue // handed to a shell, which does expand it
		}
		t.Errorf("systemd will not expand $(...) here: %q", strings.TrimSpace(m))
	}
}
