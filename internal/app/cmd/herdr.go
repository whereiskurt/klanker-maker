// Package cmd — herdr.go
// km herdr: prepare a sandbox for a Herdr remote attach and hold the transport
// open. See docs/superpowers/specs/2026-09-04-herdr-remote-attach-design.md.
//
// This is a sibling of km vscode, not a km tunnel mode. km tunnel carries a
// network path from the workstation INTO the sandbox, and its security note
// (km's egress enforcement does not see traffic crossing the tunnel) follows
// from that direction. Herdr points the other way: the operator reaches in over
// a transport km already terminates, and nothing new becomes reachable from the
// box. Filing it under tunnel would attach a caveat that is false here.
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// herdrS3Key is the single S3 key contract shared with base/tools/herdr.yaml
// and fetchAndUploadHerdr. Pinned by TestHerdrS3Key_PinnedAcrossAllSites.
const herdrS3Key = "binaries/herdr"

// herdrStatusScript is the single combined SSM script sent by both
// `km herdr start` and `km herdr status`. One round trip, four facts.
const herdrStatusScript = `echo "=== sshd ==="
systemctl is-active sshd 2>&1 || true
echo "=== authkeys exists ==="
test -f /home/sandbox/.ssh/authorized_keys && echo yes || echo no
echo "=== authkeys content ==="
cat /home/sandbox/.ssh/authorized_keys 2>/dev/null | head -1 || true
echo "=== herdr path ==="
HERDR_BIN=$(command -v herdr 2>/dev/null || true)
if [ -z "$HERDR_BIN" ] && [ -x /home/sandbox/.local/bin/herdr ]; then
  HERDR_BIN=/home/sandbox/.local/bin/herdr
fi
echo "$HERDR_BIN"
echo "=== herdr version ==="
if [ -n "$HERDR_BIN" ]; then "$HERDR_BIN" --version 2>/dev/null || true; fi`

// herdrBoxState is the parsed result of herdrStatusScript.
type herdrBoxState struct {
	SSHDActive      bool
	AuthKeysPresent bool
	HerdrPath       string // "" when absent
	HerdrVersion    string // "" when absent or unparseable
}

// herdrSemverRe extracts the first semver-looking token from `herdr --version`
// output, so a chattier upstream (commit hash, build date) does not read as
// "herdr absent".
var herdrSemverRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// parseHerdrStatus interprets herdrStatusScript's output. It never errors:
// callers decide which missing facts are fatal, because `start` can repair a
// missing herdr binary while `status` only reports it.
func parseHerdrStatus(out string) herdrBoxState {
	return herdrBoxState{
		SSHDActive:      strings.Contains(out, "=== sshd ===\nactive"),
		AuthKeysPresent: strings.Contains(out, "=== authkeys exists ===\nyes"),
		HerdrPath:       strings.TrimSpace(sectionOf(out, "=== herdr path ===")),
		HerdrVersion:    herdrSemverRe.FindString(sectionOf(out, "=== herdr version ===")),
	}
}

// sectionOf returns the text between marker and the next "=== " marker (or EOF).
func sectionOf(out, marker string) string {
	i := strings.Index(out, marker)
	if i < 0 {
		return ""
	}
	rest := out[i+len(marker):]
	if j := strings.Index(rest, "\n=== "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// herdrInstallScript pulls the pinned binary from S3 using the instance role.
// Deliberately NOT `curl https://herdr.dev/install.sh` — the interactive
// installer needs public egress, which on an ebpf/both profile means adding a
// ".herdr.dev" DNS suffix (DNS is the one layer root does not bypass), and
// Herdr's own docs say non-interactive installs fail outright.
func herdrInstallScript() string {
	return fmt.Sprintf(`set -e
# KM_ARTIFACTS_BUCKET is not exported into non-login shells (cloud-init, SSM),
# but every sandbox carries it in /etc/profile.d/km-identity.sh. Prefer the
# environment when it is already set so this keeps working if that ever changes.
if [ -z "${KM_ARTIFACTS_BUCKET:-}" ] && [ -r /etc/profile.d/km-identity.sh ]; then
  . /etc/profile.d/km-identity.sh
fi
if [ -z "${KM_ARTIFACTS_BUCKET:-}" ]; then
  echo "KM_ARTIFACTS_BUCKET is unset and /etc/profile.d/km-identity.sh did not supply it" >&2
  exit 1
fi
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/%s" /usr/local/bin/herdr
chmod 0755 /usr/local/bin/herdr
echo "=== herdr path ==="
command -v herdr 2>/dev/null || true
echo "=== herdr version ==="
herdr --version 2>/dev/null || true`, herdrS3Key)
}

// herdrBanner prints the connection block before the blocking port-forward opens.
func herdrBanner(w io.Writer, sandboxID, alias string, localPort int, st herdrBoxState) {
	fmt.Fprintf(w, "✓ Updated ~/.ssh/config (Host: %s)\n", alias)
	fmt.Fprintf(w, "✓ herdr v%s present at %s\n", st.HerdrVersion, st.HerdrPath)
	fmt.Fprintf(w, "✓ Forwarding localhost:%d → sandbox:22\n\n", localPort)
	fmt.Fprintf(w, "In another terminal:\n\n")
	fmt.Fprintf(w, "    herdr --remote %s\n\n", alias)
	fmt.Fprintf(w, "  Named session:  herdr --remote %s --session agents\n", alias)
	fmt.Fprintf(w, "  Detach:         ctrl+b q          (panes keep running)\n")
	fmt.Fprintf(w, "  Reattach:       rerun the same command\n\n")
	fmt.Fprintf(w, "  Herdr rewrites ~/.ssh/config by default and km owns the %s block\n", alias)
	fmt.Fprintf(w, "  in that file. Add this to ~/.config/herdr/config.toml so they do not\n")
	fmt.Fprintf(w, "  fight over it:\n\n")
	fmt.Fprintf(w, "      [remote]\n      manage_ssh_config = false\n\n")
	fmt.Fprintf(w, "The tunnel auto-reconnects if it drops; Ctrl-C closes it. Detached panes\n")
	fmt.Fprintf(w, "survive both — but `km herdr status %s` shows what the idle timer sees.\n\n", sandboxID)
}

// NewHerdrCmd returns the `km herdr` parent command.
func NewHerdrCmd(cfg *config.Config) *cobra.Command {
	return newHerdrCmdInternal(cfg, nil, nil, nil)
}

func newHerdrCmdInternal(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
	parent := &cobra.Command{
		Use:          "herdr",
		Short:        "Attach a Herdr terminal multiplexer to a sandbox over SSM",
		SilenceUsage: true,
	}
	parent.AddCommand(newHerdrStartCmd(cfg, fetcher, execFn, ssmClient))
	parent.AddCommand(newHerdrStatusCmd(cfg, fetcher, ssmClient))
	return parent
}

func newHerdrStartCmd(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
	var localPort int
	var noInstall bool
	cmd := &cobra.Command{
		Use:   "start <sandbox-id>",
		Short: "Hold open the transport for a Herdr remote attach",
		Long: `Prepare a sandbox to accept a Herdr remote attach and hold the transport open.

Run this in one terminal and ` + "`herdr --remote km-<id>`" + ` in another. Panes keep
running when you detach with ctrl+b q, and survive this command being Ctrl-C'd —
but a Herdr session does NOT survive a reboot, so km stop kills every pane's
process. km pause hibernates and keeps them. An idle stop under teardownPolicy:
stop follows spec.runtime.hibernation: it hibernates (panes survive) when that is
set, and stops (panes die) when it is not.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			f, e, s, err := resolveVSCodeDeps(c.Context(), cfg, fetcher, execFn, ssmClient)
			if err != nil {
				return err
			}
			sandboxID, err := ResolveSandboxID(c.Context(), cfg, args[0])
			if err != nil {
				return err
			}
			return runHerdrStart(c.Context(), f, e, s, sandboxID, localPort, noInstall)
		},
	}
	// 2224: km vscode owns 2222, both km tunnel modes own 2223.
	cmd.Flags().IntVar(&localPort, "local-port", 2224, "Local port for the SSM forward to sshd")
	cmd.Flags().BoolVar(&noInstall, "no-install", false, "Fail instead of installing herdr when it is absent from the sandbox")
	return cmd
}

func newHerdrStatusCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	return &cobra.Command{
		Use:          "status <sandbox-id>",
		Short:        "Report sshd, authorized_keys, herdr version, and what the idle timer sees",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			f, _, s, err := resolveVSCodeDeps(c.Context(), cfg, fetcher, nil, ssmClient)
			if err != nil {
				return err
			}
			sandboxID, err := ResolveSandboxID(c.Context(), cfg, args[0])
			if err != nil {
				return err
			}
			return runHerdrStatus(c.Context(), f, s, sandboxID)
		},
	}
}

func runHerdrStart(ctx context.Context, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID string, localPort int, noInstall bool) error {
	instanceID, region, alias, privPath, err := connectPrep(ctx, fetcher, sandboxID, localPort, localPort+100)
	if err != nil {
		return err
	}

	// Runs BEFORE upsertSandboxHost — an unhealthy sandbox must not get an
	// ssh-config entry written for it.
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, herdrStatusScript)
	if err != nil {
		return fmt.Errorf("ssm pre-flight check: %w", err)
	}
	st := parseHerdrStatus(out)

	// herdrHealthError, not parseVSCodeStatus: both read the identical
	// sshd/authorized_keys markers, but parseVSCodeStatus's absent-both
	// message ("VS Code not enabled...") is confusing to meet from a
	// km herdr command. herdrHealthError gives the herdr-specific wording so
	// `km herdr start` and `km herdr status` diagnose the same failure the
	// same way.
	if !st.SSHDActive || !st.AuthKeysPresent {
		return herdrHealthError(st, sandboxID)
	}

	if st.HerdrPath == "" {
		if noInstall {
			return fmt.Errorf("herdr is not installed on %s and --no-install was set; install it with `km herdr start %s` or add `extends: [base/tools/herdr]` to the profile", sandboxID, sandboxID)
		}
		fmt.Printf("herdr not found on %s — installing from s3://.../%s\n", sandboxID, herdrS3Key)
		instOut, instErr := sendSSMAndWait(ctx, ssmClient, instanceID, herdrInstallScript())
		if instErr != nil {
			return fmt.Errorf("install herdr on sandbox: %w", instErr)
		}
		// Assign only the two herdr fields the install output actually carries —
		// its markers don't include sshd/authkeys, so replacing the whole
		// struct would silently zero SSHDActive/AuthKeysPresent even though
		// nothing changed on that front.
		instSt := parseHerdrStatus(instOut)
		st.HerdrPath = instSt.HerdrPath
		st.HerdrVersion = instSt.HerdrVersion
		if st.HerdrPath == "" {
			return fmt.Errorf("herdr install ran but the binary is still absent on %s — check that `km init --sidecars` has seeded s3://<artifacts>/%s", sandboxID, herdrS3Key)
		}
		fmt.Printf("✓ Installed herdr v%s\n", st.HerdrVersion)
	}

	if err := upsertSandboxHost(alias, privPath, localPort); err != nil {
		return fmt.Errorf("upsert ssh-config: %w", err)
	}

	herdrBanner(os.Stdout, sandboxID, alias, localPort, st)

	buildPF := func(c context.Context) *exec.Cmd {
		return buildPortForwardCmd(c, instanceID, region, strconv.Itoa(localPort), "22")
	}
	return runReconnectingPortForward(ctx, execFn, buildPF, sshBannerTunnelProbe(localPort), true, os.Stdout)
}

// herdrPresenceSignal8Script reports whether the box's km-presence binary carries
// signal 8.
//
// Greps for the exact symbol `checkHerdrPaneBusy`, NOT the word "herdr". Go embeds
// source file paths in binaries, so a bare "herdr" match is satisfied by any build
// whose repo path happens to contain that word — verified live: a km-presence with
// no signal 8 at all matched, because the build worktree was named "herdr". That
// false positive suppresses the warning this probe exists to raise, so the symbol
// name is the only trustworthy marker. Pinned by TestHerdrPresenceProbe_PinsSignal8Symbol.
const herdrPresenceSignal8Script = `echo "=== presence signal8 ==="
if strings /opt/km/bin/km-presence 2>/dev/null | grep -q checkHerdrPaneBusy; then echo yes; else echo no; fi`

// herdrStatusReport prints the readiness summary and returns a non-nil error
// when the sandbox cannot accept an attach.
func herdrStatusReport(w io.Writer, sandboxID string, st herdrBoxState, presenceHasSignal8 bool) error {
	if !st.SSHDActive || !st.AuthKeysPresent {
		return herdrHealthError(st, sandboxID)
	}
	if st.HerdrPath == "" {
		return fmt.Errorf("herdr is not installed on %s — run `km herdr start %s` to install it, or add `extends: [base/tools/herdr]` to the profile and recreate", sandboxID, sandboxID)
	}

	fmt.Fprintf(w, "✓ herdr ready on %s (sshd active, authorized_keys present, herdr v%s at %s)\n",
		sandboxID, st.HerdrVersion, st.HerdrPath)

	if !presenceHasSignal8 {
		fmt.Fprintf(w, "\n⚠ This sandbox runs a km-presence daemon without signal 8.\n")
		fmt.Fprintf(w, "  A DETACHED herdr session running work is invisible to the idle timer,\n")
		fmt.Fprintf(w, "  so the box can still be stopped mid-run. Under teardownPolicy: stop that\n")
		fmt.Fprintf(w, "  kills every pane's process — herdr sessions do not survive a reboot.\n")
		fmt.Fprintf(w, "  Fix: km destroy %s && km create <profile>\n", sandboxID)
	}
	return nil
}

// herdrHealthError renders the same diagnoses parseVSCodeStatus gives, from an
// already-parsed state rather than raw SSM output — herdrStatusReport only has
// the struct, not the original script text.
func herdrHealthError(st herdrBoxState, sandboxID string) error {
	switch {
	case !st.SSHDActive && !st.AuthKeysPresent:
		return fmt.Errorf("sshd is not running and authorized_keys is absent on %s — this profile likely has spec.runtime.vscode.enabled: false; set it true and recreate the sandbox", sandboxID)
	case !st.AuthKeysPresent:
		return fmt.Errorf("unexpected state: sshd is running but /home/sandbox/.ssh/authorized_keys is absent — recreate the sandbox")
	default:
		return fmt.Errorf("sshd is not running on the sandbox; try `km shell %s -- sudo systemctl start sshd`", sandboxID)
	}
}

func runHerdrStatus(ctx context.Context, fetcher SandboxFetcher, ssmClient SSMSendAPI, sandboxID string) error {
	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, herdrStatusScript+"\n"+herdrPresenceSignal8Script)
	if err != nil {
		return fmt.Errorf("ssm status check: %w", err)
	}
	st := parseHerdrStatus(out)
	// Read the probe's own section. A bare strings.Contains(out, "yes") would
	// also match the "=== authkeys exists ===\nyes" section above it and report
	// every sandbox as signal-8-capable, silencing the warning that matters most.
	hasS8 := strings.TrimSpace(sectionOf(out, "=== presence signal8 ===")) == "yes"
	return herdrStatusReport(os.Stdout, sandboxID, st, hasS8)
}
