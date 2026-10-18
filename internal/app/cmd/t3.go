// Package cmd — t3.go
// km t3: run T3 Code (https://github.com/pingdotgg/t3code) on a sandbox as a
// background service and reach it from the operator's browser over SSM.
// See docs/superpowers/specs/2026-10-17-t3code-sandbox-design.md.
//
// A sibling of km vscode / km desktop / km herdr. Unlike those, the server is
// started and stopped explicitly and outlives the operator's terminal: t3's
// agents keep working with no client attached. The idle reaper sees it through
// km-presence signal 9 (a client connected OR a turn running), never through
// the server merely existing.
package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

const (
	// t3RemotePort is the loopback port the km-owned unit serves on. A contract
	// shared with km-presence signal 9 (cmd/km-presence/t3.go t3Port), pinned by
	// TestT3PortPinnedAcrossSites.
	t3RemotePort = 3773
	// t3DefaultLocalPort avoids 3773 (t3's own default, likely in use by a t3 on
	// the operator's laptop) and km's other forwards: 2222 vscode, 2223 tunnel,
	// 2224 herdr, 8444 desktop.
	t3DefaultLocalPort = 3774
	// t3Version is the pinned T3 Code release. `km t3 start --version` overrides it.
	t3Version  = "0.1.0"
	t3UnitName = "km-t3code.service"
)

// t3UnitFile is the systemd unit `km t3 start` writes. Every line is
// load-bearing — see spec §5.3:
//   - system unit with User=sandbox, not `t3 service install` (a user unit that
//     needs linger, i.e. sudo, and lives outside km's control);
//   - bash -lc so /etc/profile.d puts /opt/km/shims first (brokered secrets) and
//     nvm/~/.local/bin on PATH — t3 resolves claude/codex from PATH;
//   - UnsetEnvironment=XDG_RUNTIME_DIR keeps t3 from wrapping agents in
//     `systemd-run --user` scopes, so they stay in this unit's cgroup (signal 5
//     excludes them by that cgroup);
//   - stdout to ~/.t3/km-t3code.log (0600), not the journal: t3 prints a pairing
//     token at startup. The redirect is opened by the sandbox user's own shell,
//     NOT by systemd's StandardOutput=append: — systemd opens that as root, and
//     ~/.t3 is sandbox-controlled, so a symlink planted there would point a root
//     open() at any file on the box;
//     A log the sandbox user does not own (left by an older root-side create)
//     is replaced, not appended to — sandbox owns ~/.t3, so it may unlink it.
//     `$$L` is systemd's escape for a literal `$`: systemd expands $VAR in
//     ExecStart itself, before bash ever sees the line;
//   - Restart=on-failure, not always, so `km t3 stop` sticks.
const t3UnitFile = `[Unit]
Description=T3 Code server (km t3)
After=network-online.target

[Service]
User=sandbox
Group=sandbox
WorkingDirectory=/workspace
Environment=T3CODE_TELEMETRY_ENABLED=false
UnsetEnvironment=XDG_RUNTIME_DIR
ExecStart=/bin/bash -lc 'umask 077; L=/home/sandbox/.t3/km-t3code.log; mkdir -p /home/sandbox/.t3; if [ -e "$$L" ] && [ ! -O "$$L" ]; then rm -f "$$L"; fi; touch "$$L" && chmod 600 "$$L"; exec t3 serve --host 127.0.0.1 --port 3773 --no-browser /workspace >>/home/sandbox/.t3/km-t3code.log 2>&1'
KillMode=mixed
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`

// t3StatusScript is the single combined probe used by start, stop, status and
// connect. POSIX sh: sendSSMAndWait runs it under dash.
const t3StatusScript = `echo "=== t3 path ==="
T3_BIN=/home/sandbox/.local/bin/t3
if [ -x "$T3_BIN" ]; then echo "$T3_BIN"; fi
echo "=== t3 version ==="
if [ -x "$T3_BIN" ]; then runuser -u sandbox -- "$T3_BIN" --version 2>/dev/null || true; fi
echo "=== unit active ==="
systemctl is-active km-t3code.service 2>/dev/null || true
echo "=== unit enabled ==="
systemctl is-enabled km-t3code.service 2>/dev/null || true
echo "=== ready ==="
if curl -fsS -o /dev/null --max-time 3 http://127.0.0.1:3773/.well-known/t3/environment 2>/dev/null; then echo yes; else echo no; fi
echo "=== providers ==="
runuser -u sandbox -- bash -lc 'command -v claude; command -v codex' 2>/dev/null || true
echo "=== presence token ==="
if [ -s /etc/km/t3/presence.token ]; then echo yes; else echo no; fi
echo "=== presence signal9 ==="
if grep -aq checkT3InUse /opt/km/bin/km-presence 2>/dev/null; then echo yes; else echo no; fi`

// t3StopScript disables as well as stops, so a later km resume does not bring
// the server back. ~/.t3 (threads, settings, sessions) is left alone.
const t3StopScript = `systemctl disable --now km-t3code.service >/dev/null 2>&1 || true
` + t3StatusScript

var t3VersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// t3StartScript installs (if needed), writes and starts the unit, then mints the
// presence token. version must already match t3VersionRe.
//
// The presence token never reaches stdout: python3 writes it straight into a
// root-only file and prints only the session id. SSM keeps command output, so
// anything echoed here would be readable for weeks.
//
// This runs as ROOT. It never touches a path the sandbox user controls (~/.t3,
// a fixed name in /tmp) with a root file operation: a symlink planted there
// would turn a root touch/chown/redirect into a write to any file on the box.
// Work on ~/.t3 happens via runuser as sandbox; root scratch files come from
// mktemp; the unit is staged inside /etc/systemd/system.
func t3StartScript(version string) string {
	return fmt.Sprintf(`KM_T3_VERSION='%s'
has_libatomic() {
  ldconfig -p 2>/dev/null | grep -q 'libatomic\.so\.1' && return 0
  [ -e /usr/lib64/libatomic.so.1 ] || [ -e /usr/lib/x86_64-linux-gnu/libatomic.so.1 ]
}
# 1. libatomic (root). The t3 binary will not run without it on AL2023, and its
# installer's own smoke test fails first. The package repos are outside
# base/network/locked's allowlist, so on such a box this fails — report it.
if ! has_libatomic; then
  PKGLOG=$(mktemp)
  if command -v dnf >/dev/null 2>&1; then dnf install -y libatomic >"$PKGLOG" 2>&1 || true
  elif command -v apt-get >/dev/null 2>&1; then apt-get install -y libatomic1 >"$PKGLOG" 2>&1 || true
  fi
  rm -f "$PKGLOG"
  if ! has_libatomic; then
    echo "=== libatomic ==="
    echo missing
    exit 0
  fi
fi
# 2. t3 itself (sandbox user), pinned, from the tagged installer on GitHub —
# not t3.codes, which base/network/locked does not allow. The installer
# verifies the release's SHA256SUMS. Compared as an exact version so a re-run
# on an up-to-date box never reinstalls and restarts mid-turn.
NEED_RESTART=0
CUR=$(runuser -u sandbox -- /home/sandbox/.local/bin/t3 --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
if [ "$CUR" != "$KM_T3_VERSION" ]; then
  echo "installing t3 v$KM_T3_VERSION"
  INSTLOG=$(mktemp)
  # pipefail: without it a failed download pipes nothing into sh, which exits 0.
  if ! runuser -u sandbox -- bash -lc "set -o pipefail; curl -fsSL https://raw.githubusercontent.com/pingdotgg/t3code/v$KM_T3_VERSION/scripts/install.sh | T3CODE_VERSION=$KM_T3_VERSION sh" >"$INSTLOG" 2>&1; then
    echo "=== install failed ==="
    tail -5 "$INSTLOG"
    rm -f "$INSTLOG"
    exit 0
  fi
  rm -f "$INSTLOG"
  NEED_RESTART=1
fi
# 3. The unit. ~/.t3 and the server log are created by the unit itself, as the
# sandbox user (see t3UnitFile).
[ -d /workspace ] || install -d -o sandbox -g sandbox /workspace
NEWUNIT=$(mktemp /etc/systemd/system/.km-t3code.XXXXXX)
cat > "$NEWUNIT" <<'KM_T3_UNIT_EOF'
%sKM_T3_UNIT_EOF
if ! cmp -s "$NEWUNIT" /etc/systemd/system/km-t3code.service; then
  chmod 0644 "$NEWUNIT"
  mv -f "$NEWUNIT" /etc/systemd/system/km-t3code.service
  systemctl daemon-reload
  NEED_RESTART=1
else
  rm -f "$NEWUNIT"
fi
systemctl enable km-t3code.service >/dev/null 2>&1
if [ "$NEED_RESTART" = 1 ]; then systemctl restart km-t3code.service; else systemctl start km-t3code.service; fi
# 4. Wait for it to answer.
i=0
until curl -fsS -o /dev/null --max-time 2 http://127.0.0.1:3773/.well-known/t3/environment 2>/dev/null; do
  i=$((i+1))
  if [ "$i" -ge 30 ]; then
    echo "=== not ready ==="
    runuser -u sandbox -- tail -20 /home/sandbox/.t3/km-t3code.log 2>/dev/null | sed -E 's/(token=|Token: )[^ ]+/\1REDACTED/g'
    break
  fi
  sleep 1
done
# 5. Presence token for km-presence signal 9: read-only scope, root-only file,
# never printed. Issue first, then revoke older km-presence sessions, so a
# failed issue leaves the previous token working.
install -d -m 0700 /etc/km/t3
NEWID=$(runuser -u sandbox -- bash -lc 't3 auth session issue --scope orchestration:read --label km-presence --ttl 365d --json' 2>/dev/null | python3 -c '
import json, os, sys
d = json.load(sys.stdin)
fd = os.open("/etc/km/t3/presence.token.tmp", os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
os.write(fd, (d["token"] + "\n").encode())
os.close(fd)
print(d["sessionId"])
' 2>/dev/null || true)
if [ -n "$NEWID" ] && [ -s /etc/km/t3/presence.token.tmp ]; then
  mv -f /etc/km/t3/presence.token.tmp /etc/km/t3/presence.token
  runuser -u sandbox -- bash -lc 't3 auth session list --json' 2>/dev/null | NEWID="$NEWID" python3 -c '
import json, os, sys
for s in json.load(sys.stdin):
    if (s.get("client") or {}).get("label") == "km-presence" and s.get("sessionId") != os.environ["NEWID"]:
        print(s["sessionId"])
' 2>/dev/null | while read -r old; do
    runuser -u sandbox -- bash -lc "t3 auth session revoke '$old'" >/dev/null 2>&1 || true
  done
else
  rm -f /etc/km/t3/presence.token.tmp
fi
`, version, t3UnitFile) + t3StatusScript
}

// t3PairScript mints a single-use, 5-minute pairing link whose base URL is the
// operator's end of the forward, and prints only that URL.
func t3PairScript(localPort int, sandboxID string) string {
	return fmt.Sprintf(`
echo "=== pair url ==="
runuser -u sandbox -- bash -lc 't3 auth pairing create --base-url http://localhost:%d --json --ttl 5m --label km-t3-%s' 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("pairUrl",""))' 2>/dev/null || true`, localPort, sandboxID)
}

// t3BoxState is the parsed result of t3StatusScript (plus start's extra markers).
type t3BoxState struct {
	Path             string
	Version          string
	Active           bool
	Enabled          bool
	Ready            bool
	Providers        []string
	PresenceToken    bool
	Signal9          bool
	LibatomicMissing bool
	InstallFailed    string // installer tail when the install failed
	NotReadyLog      string // redacted log tail when the server never answered
	PairURL          string
}

// parseT3Status never errors; callers decide which missing facts are fatal.
func parseT3Status(out string) t3BoxState {
	field := func(marker string) string { return strings.TrimSpace(sectionOf(out, marker)) }
	var providers []string
	for _, l := range strings.Split(field("=== providers ==="), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			providers = append(providers, l)
		}
	}
	st := t3BoxState{
		Path:             field("=== t3 path ==="),
		Version:          herdrSemverRe.FindString(sectionOf(out, "=== t3 version ===")),
		Active:           field("=== unit active ===") == "active",
		Enabled:          field("=== unit enabled ===") == "enabled",
		Ready:            field("=== ready ===") == "yes",
		Providers:        providers,
		PresenceToken:    field("=== presence token ===") == "yes",
		Signal9:          field("=== presence signal9 ===") == "yes",
		LibatomicMissing: field("=== libatomic ===") == "missing",
		PairURL:          field("=== pair url ==="),
	}
	if strings.Contains(out, "=== install failed ===") {
		st.InstallFailed = field("=== install failed ===")
		if st.InstallFailed == "" {
			st.InstallFailed = "(no installer output)"
		}
	}
	if strings.Contains(out, "=== not ready ===") {
		st.NotReadyLog = field("=== not ready ===")
	}
	return st
}

// t3SetupError reports the failures that mean start could not get a server up.
func t3SetupError(st t3BoxState, sandboxID string) error {
	switch {
	case st.LibatomicMissing:
		return fmt.Errorf("libatomic is missing on %s and the OS package repos are unreachable from it "+
			"(a base/network/locked-style egress allowlist). Add cdn.amazonlinux.com (AL2023) or the Ubuntu "+
			"archive to spec.network.egress.allowedDNSSuffixes and recreate, or use an AMI that already has "+
			"libatomic (km ami bake)", sandboxID)
	case st.InstallFailed != "":
		return fmt.Errorf("t3 install failed on %s (GitHub release download):\n%s", sandboxID, st.InstallFailed)
	case st.Path == "":
		return fmt.Errorf("t3 is not installed on %s — run `km t3 start %s`", sandboxID, sandboxID)
	case !st.Active:
		return fmt.Errorf("t3 is installed on %s but %s is not running — run `km t3 start %s`", sandboxID, t3UnitName, sandboxID)
	}
	return nil
}

// t3StatusReport prints the readiness summary and returns non-nil when no
// server is running.
func t3StatusReport(w io.Writer, sandboxID string, st t3BoxState) error {
	if err := t3SetupError(st, sandboxID); err != nil {
		return err
	}
	enabled := "starts again on resume"
	if !st.Enabled {
		enabled = "not enabled — will not come back after a resume"
	}
	fmt.Fprintf(w, "✓ t3 v%s running on %s (127.0.0.1:%d; %s)\n", st.Version, sandboxID, t3RemotePort, enabled)
	if !st.Ready {
		fmt.Fprintf(w, "⚠ the server is not answering yet — see /home/sandbox/.t3/km-t3code.log on the sandbox\n")
		if st.NotReadyLog != "" {
			fmt.Fprintf(w, "%s\n", st.NotReadyLog)
		}
	}
	if len(st.Providers) > 0 {
		fmt.Fprintf(w, "  providers on PATH: %s\n", strings.Join(st.Providers, ", "))
	} else {
		fmt.Fprintf(w, "⚠ no claude or codex on the sandbox user's PATH — t3 has no agent to drive.\n")
		fmt.Fprintf(w, "  Use a profile that installs one (base/userinit), then log in with `km agent auth %s --claude`.\n", sandboxID)
	}
	if !st.PresenceToken {
		fmt.Fprintf(w, "⚠ no presence token — a turn running with no browser attached will not keep the box awake.\n")
		fmt.Fprintf(w, "  Run `km t3 start %s` again to mint it.\n", sandboxID)
	}
	if !st.Signal9 {
		fmt.Fprintf(w, "\n⚠ This sandbox runs a km-presence daemon without signal 9.\n")
		fmt.Fprintf(w, "  An open browser alone does not keep it awake, and t3's agents keep it awake for\n")
		fmt.Fprintf(w, "  at least 30 min after every turn (signal 5 sees their long-lived processes).\n")
		fmt.Fprintf(w, "  Fix: km destroy %s && km create <profile>\n", sandboxID)
	}
	return nil
}

// t3Target resolves deps, the sandbox reference, and its EC2 instance id.
func t3Target(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, ref string) (f SandboxFetcher, e ShellExecFunc, s SSMSendAPI, sandboxID, instanceID, region string, err error) {
	f, e, s, err = resolveVSCodeDeps(ctx, cfg, fetcher, execFn, ssmClient)
	if err != nil {
		return
	}
	sandboxID, err = ResolveSandboxID(ctx, cfg, ref)
	if err != nil {
		return
	}
	rec, ferr := f.FetchSandbox(ctx, sandboxID)
	if ferr != nil {
		err = fmt.Errorf("fetch sandbox: %w", ferr)
		return
	}
	instanceID, err = extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		err = fmt.Errorf("find EC2 instance: %w", err)
		return
	}
	region = rec.Region
	return
}

// NewT3Cmd returns the `km t3` parent command.
func NewT3Cmd(cfg *config.Config) *cobra.Command {
	return newT3CmdInternal(cfg, nil, nil, nil)
}

func newT3CmdInternal(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
	var localPort int
	var noPair bool
	parent := &cobra.Command{
		Use:   "t3 <sandbox-id>",
		Short: "Run T3 Code on a sandbox in the background and connect your browser over SSM",
		Long: `Run a T3 Code server on a sandbox and reach it from your browser or the t3 desktop app.

  km t3 start  <sandbox-id>   install t3 if needed and start the server in the background
  km t3 <sandbox-id>          forward localhost:3774 to it and print a pairing link
  km t3 status <sandbox-id>   installed, running, providers, idle-timer wiring
  km t3 stop   <sandbox-id>   stop the server (and keep it stopped across resume)

The server keeps running after you close this terminal; t3's agents keep
working with nobody attached. The sandbox stays awake while a browser is
connected or an agent turn is running, and idles out otherwise — a started
server nobody is using does not hold the box up.

Only SSM reaches the server: it listens on the sandbox's 127.0.0.1. T3 Connect,
LAN/Tailscale pairing and the phone app are not supported.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			return t3Run(c.Context(), cfg, fetcher, execFn, ssmClient, args[0], localPort, noPair)
		},
	}
	parent.Flags().IntVar(&localPort, "local-port", t3DefaultLocalPort, "Local port for the SSM forward to the t3 server")
	parent.Flags().BoolVar(&noPair, "no-pair", false, "Do not mint a pairing link (this browser is already paired)")
	parent.AddCommand(newT3StartCmd(cfg, fetcher, ssmClient))
	parent.AddCommand(newT3StopCmd(cfg, fetcher, ssmClient))
	parent.AddCommand(newT3StatusCmd(cfg, fetcher, ssmClient))
	return parent
}

// t3Run is the connect entry point. A package var so the cobra layer can be
// tested for routing without AWS.
var t3Run = func(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, ref string, localPort int, noPair bool) error {
	// Probe the local port before any AWS work: session-manager-plugin fails
	// silently on a squatted port and the browser then reaches the squatter.
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		return fmt.Errorf("local port %d is already in use — pick a different one with --local-port (e.g. %d)", localPort, localPort+1)
	}
	ln.Close()
	_, e, s, sandboxID, instanceID, region, err := t3Target(ctx, cfg, fetcher, execFn, ssmClient, ref)
	if err != nil {
		return err
	}
	return runT3Connect(ctx, e, s, sandboxID, instanceID, region, localPort, noPair, os.Stdout)
}

// runT3Connect checks the server is up, mints a pairing link unless noPair,
// prints the banner, and holds the reconnecting forward in the foreground.
func runT3Connect(ctx context.Context, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID, instanceID, region string, localPort int, noPair bool, w io.Writer) error {
	script := t3StatusScript
	if !noPair {
		script += t3PairScript(localPort, sandboxID)
	}
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, script)
	if err != nil {
		return fmt.Errorf("ssm pre-flight check: %w", err)
	}
	st := parseT3Status(out)
	if err := t3SetupError(st, sandboxID); err != nil {
		return err
	}

	fmt.Fprintf(w, "✓ t3 v%s running on %s\n", st.Version, sandboxID)
	fmt.Fprintf(w, "✓ Forwarding localhost:%d → sandbox:%d\n\n", localPort, t3RemotePort)
	fmt.Fprintf(w, "Open:            http://localhost:%d/\n", localPort)
	switch {
	case st.PairURL != "":
		fmt.Fprintf(w, "First time here: %s\n", st.PairURL)
		fmt.Fprintf(w, "                 (single use, expires in 5 minutes)\n")
		fmt.Fprintf(w, "Desktop app:     Settings → Connections → Add environment → paste the pairing link\n")
	case !noPair:
		fmt.Fprintf(w, "⚠ could not mint a pairing link; on the sandbox: t3 auth pairing create --base-url http://localhost:%d\n", localPort)
	}
	fmt.Fprintf(w, "\nOne local port is one environment in your browser: use --local-port to\n")
	fmt.Fprintf(w, "connect a second sandbox at the same time.\n")
	fmt.Fprintf(w, "The tunnel auto-reconnects; Ctrl-C closes it. The server keeps running —\n")
	fmt.Fprintf(w, "`km t3 stop %s` stops it.\n\n", sandboxID)

	buildPF := func(c context.Context) *exec.Cmd {
		return buildPortForwardCmd(c, instanceID, region, strconv.Itoa(localPort), strconv.Itoa(t3RemotePort))
	}
	return runReconnectingPortForward(ctx, execFn, buildPF, httpTunnelProbe(localPort), true, w)
}

func newT3StartCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "start <sandbox-id>",
		Short: "Install t3 if needed and start it in the background on a sandbox",
		Long: `Install the pinned T3 Code release if the sandbox lacks it, write and enable
km-t3code.service (127.0.0.1:3773, running as the sandbox user), and mint the
read-only token km-presence uses to see whether a turn is running.

Idempotent: on a box that already runs the same version, it only makes sure the
server is up. Then connect with: km t3 <sandbox-id>`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if !t3VersionRe.MatchString(version) {
				return fmt.Errorf("--version must look like 1.2.3, got %q", version)
			}
			_, _, s, sandboxID, instanceID, _, err := t3Target(c.Context(), cfg, fetcher, nil, ssmClient, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Starting t3 v%s on %s (installs on first run, ~30s)…\n", version, sandboxID)
			out, err := sendSSMAndWait(c.Context(), s, instanceID, t3StartScript(version))
			if err != nil {
				return fmt.Errorf("ssm start: %w", err)
			}
			if err := t3StatusReport(os.Stdout, sandboxID, parseT3Status(out)); err != nil {
				return err
			}
			fmt.Printf("\nNext: km t3 %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&version, "version", t3Version, "T3 Code release to install")
	return cmd
}

func newT3StopCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	return &cobra.Command{
		Use:          "stop <sandbox-id>",
		Short:        "Stop the t3 server on a sandbox and keep it stopped across resume",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			_, _, s, sandboxID, instanceID, _, err := t3Target(c.Context(), cfg, fetcher, nil, ssmClient, args[0])
			if err != nil {
				return err
			}
			out, err := sendSSMAndWait(c.Context(), s, instanceID, t3StopScript)
			if err != nil {
				return fmt.Errorf("ssm stop: %w", err)
			}
			return t3StopReport(os.Stdout, sandboxID, parseT3Status(out))
		},
	}
}

func t3StopReport(w io.Writer, sandboxID string, st t3BoxState) error {
	if st.Active {
		return fmt.Errorf("%s is still active on %s after stop — check `systemctl status %s` there", t3UnitName, sandboxID, t3UnitName)
	}
	fmt.Fprintf(w, "✓ t3 stopped on %s (disabled: stays stopped across resume). Threads and settings in ~/.t3 are kept.\n", sandboxID)
	return nil
}

func newT3StatusCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	return &cobra.Command{
		Use:          "status <sandbox-id>",
		Short:        "Report t3's install, server, providers, and what the idle timer sees",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			_, _, s, sandboxID, instanceID, _, err := t3Target(c.Context(), cfg, fetcher, nil, ssmClient, args[0])
			if err != nil {
				return err
			}
			out, err := sendSSMAndWait(c.Context(), s, instanceID, t3StatusScript)
			if err != nil {
				return fmt.Errorf("ssm status check: %w", err)
			}
			return t3StatusReport(os.Stdout, sandboxID, parseT3Status(out))
		},
	}
}
