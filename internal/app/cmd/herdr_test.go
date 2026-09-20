// Package cmd — herdr_test.go
// Tests for km herdr start / km herdr status.
package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
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

// TestHerdrInstallScript_ResolvesBucketFromIdentityFile pins the fix for a
// script that could never work over SSM: KM_ARTIFACTS_BUCKET is exported only
// into login shells (/etc/profile.d/km-identity.sh), and cloud-init/SSM both
// run non-login shells, so a bare "${KM_ARTIFACTS_BUCKET}" reference resolves
// to the empty string and the s3 cp target becomes "s3:///binaries/herdr" —
// this exact failure mode was reproduced live on a probe sandbox. A test that
// only checks the script *contains* the s3 key (as this one used to) cannot
// tell a working script from a broken one, since the broken version also
// contained it.
func TestHerdrInstallScript_ResolvesBucketFromIdentityFile(t *testing.T) {
	s := herdrInstallScript()
	if !strings.Contains(s, "/etc/profile.d/km-identity.sh") {
		t.Error("install script does not fall back to /etc/profile.d/km-identity.sh for KM_ARTIFACTS_BUCKET")
	}
	if !strings.Contains(s, `-z "${KM_ARTIFACTS_BUCKET:-}"`) {
		t.Error("install script does not guard on an empty KM_ARTIFACTS_BUCKET")
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

// TestRunVSCodeStart_UnhealthyPreflightDoesNotWriteSSHConfig pins the fix to a
// real behavioural regression introduced by extracting connectPrep: the SSM
// pre-flight must run BEFORE the ~/.ssh/config entry is written, exactly as
// km vscode start always behaved before this task. connectPrep must never
// upsert the ssh-config entry itself — only upsertSandboxHost may, and only
// after the caller's own pre-flight has passed.
func TestRunVSCodeStart_UnhealthyPreflightDoesNotWriteSSHConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	keyPath := filepath.Join(tmp, ".km", "keys", "sb-abc123")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("dummy-private-key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, ".ssh"), 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}

	// sshd inactive — an unhealthy sandbox. Not port 2222: that port is
	// occupied by an unrelated live process on this machine.
	mockSSM := &vsCodeSSMMock{output: "=== sshd ===\ninactive\n=== authkeys exists ===\nyes\n"}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")

	err := runVSCodeStart(context.Background(), &config.Config{}, fetcher, nil, mockSSM, "sb-abc123", 34567)
	if err == nil {
		t.Fatal("expected error for unhealthy sshd, got nil")
	}

	sshConfigPath := filepath.Join(tmp, ".ssh", "config")
	data, statErr := os.ReadFile(sshConfigPath)
	if statErr == nil && strings.Contains(string(data), "km-sb-abc123") {
		t.Errorf("ssh-config was written for an unhealthy sandbox; got:\n%s", data)
	}
}

// TestRunHerdrStart_UnhealthyPreflightDoesNotWriteSSHConfig is the herdr analog
// of the vscode test above — same connectPrep/upsertSandboxHost split, same rule.
func TestRunHerdrStart_UnhealthyPreflightDoesNotWriteSSHConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	keyPath := filepath.Join(tmp, ".km", "keys", "sb-abc123")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("dummy-private-key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, ".ssh"), 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}

	mockSSM := &vsCodeSSMMock{output: "=== sshd ===\ninactive\n=== authkeys exists ===\nyes\n"}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")

	err := runHerdrStart(context.Background(), nil, fetcher, nil, mockSSM, "sb-abc123", 34568, false, true, "")
	if err == nil {
		t.Fatal("expected error for unhealthy sshd, got nil")
	}

	sshConfigPath := filepath.Join(tmp, ".ssh", "config")
	data, statErr := os.ReadFile(sshConfigPath)
	if statErr == nil && strings.Contains(string(data), "km-sb-abc123") {
		t.Errorf("ssh-config was written for an unhealthy sandbox; got:\n%s", data)
	}
}

// TestRunHerdrStart_UnhealthyPreflightUsesHerdrWording pins the F12 fix:
// runHerdrStart must diagnose an unhealthy pre-flight with herdrHealthError's
// wording, not parseVSCodeStatus's — meeting "VS Code not enabled in this
// sandbox's profile" from a `km herdr start` invocation is confusing, since
// the operator never mentioned VS Code. sshd inactive + authkeys absent is
// the one case where the two wordings actually differ (the sshd-only case
// happens to share text with both), so it is the only fixture that proves
// the switch.
func TestRunHerdrStart_UnhealthyPreflightUsesHerdrWording(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	keyPath := filepath.Join(tmp, ".km", "keys", "sb-abc123")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("dummy-private-key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	mockSSM := &vsCodeSSMMock{output: "=== sshd ===\ninactive\n=== authkeys exists ===\nno\n"}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")

	err := runHerdrStart(context.Background(), nil, fetcher, nil, mockSSM, "sb-abc123", 34571, false, true, "")
	if err == nil {
		t.Fatal("expected error for unhealthy sshd+authkeys, got nil")
	}
	if strings.Contains(err.Error(), "VS Code not enabled") {
		t.Errorf("runHerdrStart used parseVSCodeStatus's wording; got %q", err)
	}
	if !strings.Contains(err.Error(), "spec.runtime.vscode.enabled: false") {
		t.Errorf("runHerdrStart should use herdrHealthError's wording; got %q", err)
	}
}

// herdrSendCountingSSMMock returns a fixed pre-flight output from every
// GetCommandInvocation call, like vsCodeSSMMock, but also counts SendCommand
// invocations — used to prove --no-install never sends the install script.
type herdrSendCountingSSMMock struct {
	output       string
	sendCommands int
}

func (m *herdrSendCountingSSMMock) SendCommand(_ context.Context, _ *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	m.sendCommands++
	return &ssm.SendCommandOutput{Command: &ssmtypes.Command{CommandId: awssdk.String("cmd-herdr-noinstall-test")}}, nil
}

func (m *herdrSendCountingSSMMock) GetCommandInvocation(_ context.Context, _ *ssm.GetCommandInvocationInput, _ ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return &ssm.GetCommandInvocationOutput{
		Status:                ssmtypes.CommandInvocationStatusSuccess,
		StandardOutputContent: awssdk.String(m.output),
	}, nil
}

// TestRunHerdrStart_NoInstallShortCircuitsWithoutSendingInstallCommand pins
// spec §11's required --no-install unit test: herdr absent + --no-install
// must error naming the flag, and must never attempt the install SSM command
// (only the pre-flight SendCommand call happens).
func TestRunHerdrStart_NoInstallShortCircuitsWithoutSendingInstallCommand(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	keyPath := filepath.Join(tmp, ".km", "keys", "sb-abc123")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("dummy-private-key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	// Healthy sshd/authkeys, herdr absent — not port 2222: an unrelated live
	// process holds it on this machine.
	mockSSM := &herdrSendCountingSSMMock{
		output: "=== sshd ===\nactive\n=== authkeys exists ===\nyes\n=== herdr path ===\n=== herdr version ===\n",
	}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")

	err := runHerdrStart(context.Background(), nil, fetcher, nil, mockSSM, "sb-abc123", 34569, true, true, "")
	if err == nil {
		t.Fatal("expected error when herdr is absent and --no-install is set")
	}
	if !strings.Contains(err.Error(), "--no-install") {
		t.Errorf("error should name --no-install; got %q", err)
	}
	if mockSSM.sendCommands != 1 {
		t.Errorf("expected exactly 1 SendCommand call (pre-flight only, no install attempted); got %d", mockSSM.sendCommands)
	}
}

// TestRunHerdrStart_RepairInstallPreservesPreflightFields pins commit
// 384224fb's fix: the install branch assigns ONLY HerdrPath/HerdrVersion from
// the install output, because that output carries no sshd/authkeys markers —
// replacing the whole struct would silently zero SSHDActive/AuthKeysPresent
// even though the pre-flight already found them true. A full success run
// (through upsertSandboxHost) is the only way to observe that those two
// fields survived, since herdrBanner itself never reads them.
func TestRunHerdrStart_RepairInstallPreservesPreflightFields(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	keyPath := filepath.Join(tmp, ".km", "keys", "sb-abc123")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("dummy-private-key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, ".ssh"), 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}

	preflight := "=== sshd ===\nactive\n=== authkeys exists ===\nyes\n=== herdr path ===\n=== herdr version ===\n"
	installOut := "=== herdr path ===\n/usr/local/bin/herdr\n=== herdr version ===\nherdr 0.8.2\n"
	mockSSM := &sequencedSSMMock{outputs: []string{preflight, installOut}}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")
	execFn := func(c *exec.Cmd) error { return nil }

	err := runHerdrStart(context.Background(), nil, fetcher, execFn, mockSSM, "sb-abc123", 34570, false, true, "")
	if err != nil {
		t.Fatalf("expected success after a repair install, got: %v", err)
	}

	// If the install branch had clobbered SSHDActive/AuthKeysPresent to
	// false, parseVSCodeStatus's pre-flight check already ran against the
	// PRE-FLIGHT output and would have returned before this point — so
	// reaching here at all is part of the pin. The stronger assertion is
	// that upsertSandboxHost, which runs only after the repaired state is
	// assembled, actually wrote the ssh-config entry.
	sshConfigPath := filepath.Join(tmp, ".ssh", "config")
	data, statErr := os.ReadFile(sshConfigPath)
	if statErr != nil || !strings.Contains(string(data), "km-sb-abc123") {
		t.Fatalf("expected ssh-config to be written after a successful repair install; statErr=%v data=%q", statErr, data)
	}
}

func TestHerdrStatusReport_ReadyIsNilError(t *testing.T) {
	var buf bytes.Buffer
	st := herdrBoxState{SSHDActive: true, AuthKeysPresent: true,
		HerdrPath: "/usr/local/bin/herdr", HerdrVersion: "0.8.2"}
	if err := herdrStatusReport(&buf, "sb-abc123", st, true); err != nil {
		t.Fatalf("healthy sandbox reported an error: %v", err)
	}
	if !strings.Contains(buf.String(), "0.8.2") {
		t.Errorf("report omits the herdr version; got:\n%s", buf.String())
	}
}

func TestHerdrStatusReport_HerdrAbsentIsError(t *testing.T) {
	var buf bytes.Buffer
	st := herdrBoxState{SSHDActive: true, AuthKeysPresent: true}
	err := herdrStatusReport(&buf, "sb-abc123", st, true)
	if err == nil {
		t.Fatal("expected a non-nil error when herdr is absent")
	}
	if !strings.Contains(err.Error(), "km herdr start") {
		t.Errorf("error should name the repair command; got %q", err)
	}
}

// TestHerdrStatusReport_WarnsOnPreSignal8Presence is the load-bearing one.
// km-presence is fetched at boot, so a sandbox created before signal 8 shipped
// keeps the seven-signal daemon and a detached herdr session on it is STILL
// reapable. Saying so is the difference between "the box vanished" and "I could
// see it coming".
func TestHerdrStatusReport_WarnsOnPreSignal8Presence(t *testing.T) {
	var buf bytes.Buffer
	st := herdrBoxState{SSHDActive: true, AuthKeysPresent: true,
		HerdrPath: "/usr/local/bin/herdr", HerdrVersion: "0.8.2"}
	if err := herdrStatusReport(&buf, "sb-abc123", st, false); err != nil {
		t.Fatalf("a pre-signal-8 daemon is a warning, not an error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "km destroy") {
		t.Errorf("warning should name the recreate that fixes it; got:\n%s", got)
	}
	if !strings.Contains(strings.ToLower(got), "idle") {
		t.Errorf("warning should say the box is still idle-reapable; got:\n%s", got)
	}
}

// TestHerdrPresenceProbe_PinsSignal8Symbol pins the probe's grep target to the real
// km-presence function name. The probe is a string match against a binary, so a
// rename in cmd/km-presence would silently turn it into a permanent "no" with no
// compile error anywhere — the same class of drift the herdrS3Key three-site pin
// guards against.
func TestHerdrPresenceProbe_PinsSignal8Symbol(t *testing.T) {
	const symbol = "checkHerdrPaneBusy"

	if !strings.Contains(herdrPresenceSignal8Script, symbol) {
		t.Errorf("probe does not grep for %q", symbol)
	}
	if strings.Contains(herdrPresenceSignal8Script, "grep -q herdr") {
		t.Error("probe greps the bare word \"herdr\", which Go's embedded build paths match spuriously")
	}

	raw, err := os.ReadFile("../../../cmd/km-presence/runner.go")
	if err != nil {
		t.Fatalf("read km-presence runner.go: %v", err)
	}
	if !strings.Contains(string(raw), "func "+symbol) {
		t.Fatalf("cmd/km-presence/runner.go has no func %s. "+
			"herdrPresenceSignal8Script greps the km-presence binary for that exact symbol to "+
			"decide whether a sandbox has signal 8; renaming it silently turns the probe into a "+
			"permanent \"no\" with no compile error anywhere. Update both together.", symbol)
	}
}

// TestHerdrStatusReport_ReadsSignal8FromItsOwnSection proves the caller-side parse
// (sectionOf on the combined SSM output) picks out the signal-8 answer and not an
// unrelated "yes" earlier in the same output. The three TestHerdrStatusReport_*
// tests above pass a bool straight into herdrStatusReport and so never exercise this
// parse — which is exactly why the fix-round-1 defect (a false "yes" from
// authkeys-exists bleeding into a bare Contains check) was invisible to them.
func TestHerdrStatusReport_ReadsSignal8FromItsOwnSection(t *testing.T) {
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
=== presence signal8 ===
no
`
	got := strings.TrimSpace(sectionOf(out, "=== presence signal8 ==="))
	if got != "no" {
		t.Fatalf("sectionOf read %q; want \"no\" — a bare strings.Contains(out, \"yes\") "+
			"would wrongly match the authkeys-exists section above it", got)
	}

	// And the positive case: signal8 genuinely present.
	outYes := strings.Replace(out, "=== presence signal8 ===\nno", "=== presence signal8 ===\nyes", 1)
	got = strings.TrimSpace(sectionOf(outYes, "=== presence signal8 ==="))
	if got != "yes" {
		t.Fatalf("sectionOf read %q; want \"yes\"", got)
	}
}

// =============================================================================
// One-window attach flow (km launches herdr itself)
// =============================================================================

func TestHerdrAttachArgs(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		session string
		want    []string
	}{
		{"no session", "herdrbox", "", []string{"--remote", "herdrbox"}},
		{"named session", "herdrbox", "agents", []string{"--remote", "herdrbox", "--session", "agents"}},
		{"blank session is not passed through", "herdrbox", "   ", []string{"--remote", "herdrbox"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := herdrAttachArgs(tc.host, tc.session)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// stubHerdrOnPath puts an executable `herdr` on PATH so exec.LookPath finds it,
// and returns its path. The stub is never actually run — execFn is the seam.
func stubHerdrOnPath(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	p := filepath.Join(binDir, "herdr")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write herdr stub: %v", err)
	}
	t.Setenv("PATH", binDir)
	return p
}

func herdrStartFixture(t *testing.T) (*vsCodeFetcherMock, *sequencedSSMMock) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	keyPath := filepath.Join(tmp, ".km", "keys", "sb-abc123")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("k"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, ".ssh"), 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	healthy := "=== sshd ===\nactive\n=== authkeys exists ===\nyes\n=== herdr path ===\n/usr/local/bin/herdr\n=== herdr version ===\nherdr 0.8.2\n"
	return newVSCodeEC2Sandbox("sb-abc123"), &sequencedSSMMock{outputs: []string{healthy}}
}

// startFakeSSHD binds 127.0.0.1:port and answers every connection with an SSH
// banner, so waitForSSHD's probe succeeds.
//
// It must NOT be running before runHerdrStart is called: connectPrep probes the
// port and refuses to proceed if anything already holds it. So the caller
// starts this from inside execFn, when the port-forward command runs — the same
// ordering as production, where the forward is what binds the port.
func startFakeSSHD(t *testing.T, port int) func() {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Skipf("cannot bind 127.0.0.1:%d: %v", port, err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("SSH-2.0-fake\r\n"))
			c.Close()
		}
	}()
	return func() { ln.Close() }
}

// The default flow must actually launch herdr, not just print a command. The
// port-forward and the herdr invocation both go through execFn, so the test
// asserts on which commands were run.
func TestRunHerdrStart_AttachLaunchesHerdr(t *testing.T) {
	stub := stubHerdrOnPath(t)
	fetcher, mockSSM := herdrStartFixture(t)
	const port = 34590

	var mu sync.Mutex
	var ran []string
	var stopSSHD func()
	forwardHeld := make(chan struct{})
	t.Cleanup(func() { close(forwardHeld) })
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		if stopSSHD != nil {
			stopSSHD()
		}
	})

	execFn := func(c *exec.Cmd) error {
		joined := strings.Join(c.Args, " ")
		mu.Lock()
		ran = append(ran, joined)
		isForward := stopSSHD == nil && !strings.HasPrefix(joined, stub+" ")
		if isForward {
			stopSSHD = startFakeSSHD(t, port)
		}
		mu.Unlock()
		if isForward {
			// Stand in for the real forward, which blocks for the life of the
			// session. Released at cleanup; runHerdrStart never waits on it.
			<-forwardHeld
		}
		return nil
	}

	err := runHerdrStart(context.Background(), nil, fetcher, execFn, mockSSM, "sb-abc123", port, false, false, "agents")
	if err != nil {
		t.Fatalf("attach flow returned: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var sawHerdr bool
	for _, cmd := range ran {
		if strings.HasPrefix(cmd, stub+" ") {
			sawHerdr = true
			for _, want := range []string{"--remote", "--session agents"} {
				if !strings.Contains(cmd, want) {
					t.Errorf("herdr invocation missing %q: %s", want, cmd)
				}
			}
		}
	}
	if !sawHerdr {
		t.Fatalf("herdr was never launched; commands run: %v", ran)
	}
}

// --no-attach must NOT launch herdr even when it is on PATH — that flag exists
// for driving several clients over one forward, or attaching with another tool.
func TestRunHerdrStart_NoAttachDoesNotLaunchHerdr(t *testing.T) {
	stub := stubHerdrOnPath(t)
	fetcher, mockSSM := herdrStartFixture(t)

	var ran []string
	execFn := func(c *exec.Cmd) error {
		ran = append(ran, strings.Join(c.Args, " "))
		return nil
	}

	if err := runHerdrStart(context.Background(), nil, fetcher, execFn, mockSSM, "sb-abc123", 34591, false, true, ""); err != nil {
		t.Fatalf("no-attach flow returned: %v", err)
	}
	for _, cmd := range ran {
		if strings.HasPrefix(cmd, stub+" ") {
			t.Fatalf("--no-attach launched herdr anyway: %s", cmd)
		}
	}
}

// A missing local herdr must fall back to holding the forward, not fail. The
// transport is still useful and someone may attach with something else.
func TestRunHerdrStart_MissingLocalHerdrFallsBackToHoldingTheForward(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no herdr anywhere
	fetcher, mockSSM := herdrStartFixture(t)

	execFn := func(c *exec.Cmd) error { return nil }
	if err := runHerdrStart(context.Background(), nil, fetcher, execFn, mockSSM, "sb-abc123", 34592, false, false, ""); err != nil {
		t.Fatalf("missing local herdr should fall back, not fail: %v", err)
	}
}

// The two entry points and what each must do. `km herdr <id>` is the daily
// verb — bring up the transport AND attach — so it should not need "start".
// `km herdr start <id>` is transport-only: forward + ssh config held open in
// the foreground, herdr NOT launched, for driving several clients over one
// forward or attaching from another tool. --no-attach is what start used to
// need to behave that way; it is kept as an accepted no-op so scripts and
// habits do not break, and --session lives on the bare form, which is the
// only one that attaches.
func TestHerdrCmd_BareAttachesStartIsTransportOnly(t *testing.T) {
	type call struct {
		id      string
		attach  bool
		session string
		port    int
	}
	var got []call
	orig := herdrRun
	t.Cleanup(func() { herdrRun = orig })
	herdrRun = func(_ context.Context, _ *config.Config, _ SandboxFetcher, _ ShellExecFunc, _ SSMSendAPI, ref string, localPort int, _ bool, attach bool, session string) error {
		got = append(got, call{id: ref, attach: attach, session: session, port: localPort})
		return nil
	}
	run := func(args ...string) error {
		root := &cobra.Command{Use: "km"}
		root.AddCommand(NewHerdrCmd(&config.Config{}))
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs(append([]string{"herdr"}, args...))
		return root.Execute()
	}

	if err := run("kphtest1"); err != nil {
		t.Fatalf("km herdr <id>: %v", err)
	}
	if err := run("kphtest1", "--session", "agents", "--local-port", "2299"); err != nil {
		t.Fatalf("km herdr <id> --session: %v", err)
	}
	if err := run("start", "kphtest1"); err != nil {
		t.Fatalf("km herdr start <id>: %v", err)
	}
	if err := run("start", "kphtest1", "--no-attach"); err != nil {
		t.Fatalf("km herdr start --no-attach must still be accepted: %v", err)
	}
	if err := run("start", "kphtest1", "--session", "agents"); err == nil {
		t.Error("km herdr start does not attach, so --session must be rejected rather than silently ignored")
	}

	want := []call{
		{id: "kphtest1", attach: true, session: "", port: 2224},
		{id: "kphtest1", attach: true, session: "agents", port: 2299},
		{id: "kphtest1", attach: false, session: "", port: 2224},
		{id: "kphtest1", attach: false, session: "", port: 2224},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d calls, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// The bare form must not swallow the subcommands.
	if err := run("status"); err == nil {
		t.Error("km herdr status with no id should fail on args, not be treated as sandbox \"status\"")
	}
}
