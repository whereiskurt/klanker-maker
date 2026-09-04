// Package cmd — herdr_test.go
// Tests for km herdr start / km herdr status.
package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseHerdrStatus_AllPresent(t *testing.T) {
	out := `=== sshd ===
active
=== authkeys exists ===
yes
=== authkeys content ===
ssh-ed25519 AAAAC3Nz km-sb-abc123
=== herdr path ===
/usr/local/bin/herdr
=== herdr version ===
herdr 0.8.2
`
	st := parseHerdrStatus(out)
	if !st.SSHDActive {
		t.Error("SSHDActive = false; want true")
	}
	if !st.AuthKeysPresent {
		t.Error("AuthKeysPresent = false; want true")
	}
	if st.HerdrPath != "/usr/local/bin/herdr" {
		t.Errorf("HerdrPath = %q; want /usr/local/bin/herdr", st.HerdrPath)
	}
	if st.HerdrVersion != "0.8.2" {
		t.Errorf("HerdrVersion = %q; want 0.8.2", st.HerdrVersion)
	}
}

func TestParseHerdrStatus_HerdrAbsent(t *testing.T) {
	out := `=== sshd ===
active
=== authkeys exists ===
yes
=== authkeys content ===
ssh-ed25519 AAAAC3Nz km-sb-abc123
=== herdr path ===
=== herdr version ===
`
	st := parseHerdrStatus(out)
	if !st.SSHDActive || !st.AuthKeysPresent {
		t.Fatal("sshd/authkeys should still parse as healthy when herdr is absent")
	}
	if st.HerdrPath != "" {
		t.Errorf("HerdrPath = %q; want empty", st.HerdrPath)
	}
	if st.HerdrVersion != "" {
		t.Errorf("HerdrVersion = %q; want empty", st.HerdrVersion)
	}
}

// TestParseHerdrStatus_VersionWithExtraFields covers `herdr --version` printing
// more than "herdr X.Y.Z" (a commit hash, a build date). Only the semver-looking
// token is kept, so a chattier upstream does not make km think herdr is absent.
func TestParseHerdrStatus_VersionWithExtraFields(t *testing.T) {
	out := `=== sshd ===
active
=== authkeys exists ===
yes
=== herdr path ===
/usr/local/bin/herdr
=== herdr version ===
herdr 0.8.2 (build abc1234, 2026-08-01)
`
	if got := parseHerdrStatus(out).HerdrVersion; got != "0.8.2" {
		t.Errorf("HerdrVersion = %q; want 0.8.2", got)
	}
}

// TestHerdrStatusScript_ProbesAllFour asserts the single SSM round trip covers
// every fact the banner and status output need. A missing probe here shows up as
// a silently-empty field rather than an error.
func TestHerdrStatusScript_ProbesAllFour(t *testing.T) {
	for _, want := range []string{
		"=== sshd ===",
		"=== authkeys exists ===",
		"=== herdr path ===",
		"=== herdr version ===",
	} {
		if !strings.Contains(herdrStatusScript, want) {
			t.Errorf("herdrStatusScript missing marker %q", want)
		}
	}
}

// TestHerdrInstallScript_UsesS3AndCorrectPath pins the install to the shared S3
// key and the system path. Installing to ~/.local/bin would work for the sandbox
// user but leave root's PATH without herdr, which km-presence signal 8 needs.
func TestHerdrInstallScript_UsesS3AndCorrectPath(t *testing.T) {
	s := herdrInstallScript()
	if !strings.Contains(s, herdrS3Key) {
		t.Errorf("install script does not reference %q", herdrS3Key)
	}
	if !strings.Contains(s, "/usr/local/bin/herdr") {
		t.Error("install script does not install to /usr/local/bin/herdr")
	}
	if !strings.Contains(s, "chmod 0755 /usr/local/bin/herdr") {
		t.Error("install script does not chmod the binary executable")
	}
}

// TestHerdrBanner_PrintsAttachCommandAndSSHConfigOptOut asserts the two things
// the operator cannot discover on their own: the exact attach command, and the
// fact that herdr will fight km over ~/.ssh/config unless told not to.
func TestHerdrBanner_PrintsAttachCommandAndSSHConfigOptOut(t *testing.T) {
	var buf bytes.Buffer
	st := herdrBoxState{SSHDActive: true, AuthKeysPresent: true,
		HerdrPath: "/usr/local/bin/herdr", HerdrVersion: "0.8.2"}
	herdrBanner(&buf, "sb-abc123", "km-sb-abc123", 2224, st)
	got := buf.String()

	if !strings.Contains(got, "herdr --remote km-sb-abc123") {
		t.Errorf("banner missing the attach command; got:\n%s", got)
	}
	if !strings.Contains(got, "manage_ssh_config = false") {
		t.Errorf("banner missing the ssh-config opt-out; got:\n%s", got)
	}
	if !strings.Contains(got, "ctrl+b q") {
		t.Errorf("banner missing the detach keybinding; got:\n%s", got)
	}
}

// TestHerdrDefaultLocalPort_DoesNotCollide pins 2224. km vscode owns 2222 and
// both km tunnel modes own 2223; being attached to a box with more than one of
// these at once is a plausible combination, and a collision surfaces as a
// confusing "Connection closed by 127.0.0.1" rather than a bind error.
func TestHerdrDefaultLocalPort_DoesNotCollide(t *testing.T) {
	cmd := NewHerdrCmd(nil)
	start, _, err := cmd.Find([]string{"start"})
	if err != nil {
		t.Fatalf("find start subcommand: %v", err)
	}
	f := start.Flags().Lookup("local-port")
	if f == nil {
		t.Fatal("start has no --local-port flag")
	}
	if f.DefValue != "2224" {
		t.Errorf("--local-port default = %s; want 2224 (2222=vscode, 2223=tunnel)", f.DefValue)
	}
}

// TestNewHerdrCmd_HasStartAndStatusOnly asserts the surface stays minimal.
// Rekey is deliberately absent: herdr shares the vscode keypair, and two verbs
// rotating one key is a footgun.
func TestNewHerdrCmd_HasStartAndStatusOnly(t *testing.T) {
	cmd := NewHerdrCmd(nil)
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	if !got["start"] || !got["status"] {
		t.Fatalf("expected start and status subcommands; got %v", got)
	}
	if got["rekey"] {
		t.Error("km herdr must NOT define rekey — km vscode rekey rotates the shared keypair")
	}
}
