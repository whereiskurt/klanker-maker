// Pins the pairing between the sandbox user's login shell and /etc/shells.
package compiler

import (
	"regexp"
	"strings"
	"testing"
)

// usermodShellRe finds the login shell userdata assigns to the sandbox user.
var usermodShellRe = regexp.MustCompile(`(?m)^usermod -s (\S+) sandbox\s*$`)

// TestUserdataRegistersSandboxShellInEtcShells is a mechanical pairing guard:
// whatever path userdata sets as the sandbox user's login shell must also be
// registered in /etc/shells.
//
// Found live, and the failure is silent in both directions. km set the passwd
// shell to /usr/local/bin/km-sandbox-shell but never registered it, so every
// tool that validates $SHELL against /etc/shells rejected it — and they do not
// agree on how. herdr fell back to /bin/sh, giving panes a POSIX-mode shell
// with no ~/.bashrc and no login profile, on a profile that asked for
// /bin/bash. Nothing logged it anywhere; the operator just met a bare $ prompt.
//
// Deliberately name-agnostic: it reads the path out of the usermod line rather
// than hardcoding it, so renaming the wrapper cannot leave the registration
// pointing at the old path.
//
// Enforcement must be ebpf or both. The whole km-sandbox-shell block lives
// inside the eBPF enforcer section, so under the schema-default "proxy" mode it
// never renders and userdata does not set a custom login shell at all — which
// is also why this only ever bit ebpf/both sandboxes.
func TestUserdataRegistersSandboxShellInEtcShells(t *testing.T) {
	p := baseProfile()
	p.Spec.Network.Enforcement = "both"
	ud, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("render userdata: %v", err)
	}

	m := usermodShellRe.FindStringSubmatch(ud)
	if m == nil {
		t.Fatal("no `usermod -s <shell> sandbox` line in rendered userdata — " +
			"if the login shell is now set another way, update this guard rather than deleting it")
	}
	shell := m[1]

	if !strings.Contains(ud, "/etc/shells") {
		t.Fatalf("userdata sets the sandbox login shell to %s but never touches /etc/shells", shell)
	}
	// The registered path must be the SAME path usermod assigned.
	if !strings.Contains(ud, shell+"' >> /etc/shells") && !strings.Contains(ud, shell+`" >> /etc/shells`) {
		t.Errorf("login shell %s is not the path appended to /etc/shells", shell)
	}
	// Appending unconditionally would grow the file on every resume.
	if !strings.Contains(ud, "grep -qxF") {
		t.Error("the /etc/shells append is not guarded — it must be idempotent across resumes")
	}
}
