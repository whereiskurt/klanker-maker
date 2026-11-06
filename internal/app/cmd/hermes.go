// Package cmd — hermes.go
// km hermes: run Hermes Agent (https://github.com/NousResearch/hermes-agent)
// with Hermes WebUI (https://github.com/nesquena/hermes-webui) on a sandbox as
// a background service and reach it from the operator's browser over SSM.
// See docs/superpowers/specs/2026-11-05-hermes-webui-sandbox-design.md.
//
// Modelled line for line on km t3 (t3.go): the server is started and stopped
// explicitly, outlives the operator's terminal, and is seen by the idle reaper
// through km-presence signal 10 (a browser connected OR a run/stream active),
// never through the server merely existing. The WebUI runs the agent
// in-process, so there is one unit and one port.
package cmd

import (
	"context"
	"encoding/json"
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
	// hermesRemotePort is the loopback port the km-owned unit serves on
	// (upstream's default). A contract shared with km-presence signal 10
	// (cmd/km-presence/hermes.go hermesPort), pinned by
	// TestHermesPortPinnedAcrossSites.
	hermesRemotePort = 8787
	// hermesDefaultLocalPort avoids 8787 (a WebUI on the operator's laptop) and
	// km's other forwards: 1455/1457 codex OAuth, 2222 vscode, 2223 tunnel,
	// 2224 herdr, 3774 t3, 8001 model, 8444 desktop.
	hermesDefaultLocalPort = 8788
	// hermesAgentVersion and hermesWebUIVersion are pinned as a release pair
	// (upstream: "treat as a release pair"). Upstream tags are v-prefixed.
	// `km hermes start --agent-version / --webui-version` override them.
	hermesAgentVersion = "0.22.4"
	hermesWebUIVersion = "0.52.544"
	hermesUnitName     = "km-hermes.service"
	// hermesDefaultModel is what provider seeding sets when the profile routes
	// through Bedrock and the operator gave no --model.
	hermesDefaultModel = "us.anthropic.claude-sonnet-4-6"
)

// hermesPythonCandidates are where the agent installer's virtualenv may put
// its interpreter. The WebUI must run under the agent's Python (it imports the
// agent in-process); python3 is the fallback.
// UAT: confirm which path install.sh v0.22.4 creates, then drop the other.
const hermesPythonCandidates = "/home/sandbox/.hermes/hermes-agent/venv/bin/python /home/sandbox/.hermes/hermes-agent/.venv/bin/python"

// hermesPickPython renders the shell that sets P to the agent's interpreter.
// d is the dollar sign: "$$" inside a systemd ExecStart (systemd expands $VAR
// itself before bash runs), "$" in a plain script.
func hermesPickPython(d string) string {
	return "P=python3; for c in " + hermesPythonCandidates + "; do if [ -x \"" + d + "c\" ]; then P=" + d + "c; break; fi; done"
}

// hermesUnitFile is the systemd unit `km hermes start` writes. Every line is
// load-bearing — see spec §3.2 and the t3 unit (t3UnitFile) it copies:
//   - system unit with User=sandbox; bash -lc so /etc/profile.d puts
//     /opt/km/shims first and ~/.local/bin on PATH;
//   - EnvironmentFile is read by systemd as ROOT before it drops to sandbox,
//     so the password file stays root:root 0600 in the root-only
//     /etc/km/hermes. (The running server's environ is readable by uid
//     sandbox; that is not a boundary.)
//   - UnsetEnvironment=XDG_RUNTIME_DIR keeps subagents in this unit's cgroup,
//     which km-presence signal 5 excludes;
//   - the log is opened by the sandbox user's own shell, never by systemd's
//     StandardOutput=append: (a root open() of a path in sandbox-owned
//     ~/.hermes, which a planted symlink would aim anywhere). A log the sandbox
//     user does not own is replaced, not appended to;
//   - bootstrap.py --foreground execv's into server.py, so the unit tracks the
//     server; KillMode=mixed + TimeoutStopSec=120 are upstream's own (a 90 s
//     run drain plus margin);
//   - Restart=on-failure, not always, so `km hermes stop` sticks.
//
// UAT: confirm bootstrap.py runs under the picked interpreter and that
// HERMES_WEBUI_PYTHON is the variable it honours.
var hermesUnitFile = `[Unit]
Description=Hermes WebUI + Agent (km hermes)
After=network-online.target

[Service]
User=sandbox
Group=sandbox
WorkingDirectory=/workspace
EnvironmentFile=/etc/km/hermes/webui.env
Environment=HERMES_HOME=/home/sandbox/.hermes
Environment=HERMES_WEBUI_HOST=127.0.0.1
Environment=HERMES_WEBUI_PORT=8787
Environment=HERMES_WEBUI_DEFAULT_WORKSPACE=/workspace
UnsetEnvironment=XDG_RUNTIME_DIR
ExecStart=/bin/bash -lc 'umask 077; L=/home/sandbox/.hermes/km-hermes.log; mkdir -p /home/sandbox/.hermes; if [ -e "$$L" ] && [ ! -O "$$L" ]; then rm -f "$$L"; fi; touch "$$L" && chmod 600 "$$L"; ` + hermesPickPython("$$") + `; export HERMES_WEBUI_PYTHON="$$P"; cd /home/sandbox/.hermes/hermes-webui && exec "$$P" bootstrap.py --foreground --no-browser --host 127.0.0.1 8787 >>"$$L" 2>&1'
KillMode=mixed
TimeoutStopSec=120
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`

// hermesStatusScript is the single combined probe used by start, stop, status
// and connect. POSIX sh: sendSSMAndWait runs it under dash. Everything that
// reads ~/.hermes runs as the sandbox user — git in particular, which would
// otherwise execute a sandbox-written config (core.fsmonitor, hooks) as root.
var hermesStatusScript = libatomicHasFunc + `echo "=== hermes path ==="
H_BIN=""
for c in /home/sandbox/.local/bin/hermes /home/sandbox/.hermes/hermes-agent/venv/bin/hermes; do
  if [ -x "$c" ]; then H_BIN="$c"; break; fi
done
if [ -n "$H_BIN" ]; then echo "$H_BIN"; fi
echo "=== hermes version ==="
if [ -n "$H_BIN" ]; then runuser -u sandbox -- bash -lc '"$0" --version' "$H_BIN" 2>/dev/null | head -5 || true; fi
echo "=== webui tag ==="
runuser -u sandbox -- bash -lc 'git -C ~/.hermes/hermes-webui describe --tags 2>/dev/null' 2>/dev/null | head -1 || true
echo "=== unit active ==="
systemctl is-active km-hermes.service 2>/dev/null || true
echo "=== unit enabled ==="
systemctl is-enabled km-hermes.service 2>/dev/null || true
echo "=== health ==="
curl -sS --max-time 3 -w '\n=== health code ===\n%{http_code}\n' http://127.0.0.1:8787/health 2>/dev/null || true
echo "=== provider ==="
if [ -n "$H_BIN" ]; then runuser -u sandbox -- bash -lc '"$0" config get model.provider' "$H_BIN" 2>/dev/null | head -1 || true; fi
echo "=== password file ==="
if [ -s /etc/km/hermes/webui.env ]; then echo yes; else echo no; fi
echo "=== libatomic present ==="
if has_libatomic; then echo yes; else echo no; fi
echo "=== presence signal10 ==="
if grep -aq checkHermesInUse /opt/km/bin/km-presence 2>/dev/null; then echo yes; else echo no; fi`

// hermesStopScript disables as well as stops, so a later km resume does not
// bring the server back. ~/.hermes (memory, skills, sessions) is left alone.
var hermesStopScript = `systemctl disable --now km-hermes.service >/dev/null 2>&1 || true
` + hermesStatusScript

// hermesPasswordScript prints the WebUI password under its marker. Only connect
// sends it; start and status never print the password.
const hermesPasswordScript = `
echo "=== password ==="
sed -n 's/^HERMES_WEBUI_PASSWORD=//p' /etc/km/hermes/webui.env 2>/dev/null || true`

// hermesModelRe bounds --model to what a Bedrock/provider model id looks like;
// it is interpolated into a shell variable.
var hermesModelRe = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,200}$`)

type hermesStartOpts struct {
	AgentVersion   string
	WebUIVersion   string
	Model          string
	RotatePassword bool
}

// hermesStartScript installs (if needed) the agent and the WebUI, writes the
// password file (only if absent, or on --rotate-password), seeds the model
// provider, writes and starts the unit. Both versions must already match
// t3VersionRe and Model hermesModelRe (or be empty).
//
// This runs as ROOT. Like t3StartScript it never touches a path the sandbox
// user controls (~sandbox, /workspace, a fixed /tmp name) with a root file
// operation: work in ~/.hermes happens via runuser as sandbox; root scratch
// files come from mktemp; root writes stay in /etc/km/hermes (root 0700) and
// /etc/systemd/system. The password never reaches stdout: SSM keeps command
// output.
func hermesStartScript(o hermesStartOpts) string {
	rotate := 0
	if o.RotatePassword {
		rotate = 1
	}
	head := fmt.Sprintf("KM_HERMES_AGENT_VERSION='%s'\nKM_HERMES_WEBUI_VERSION='%s'\nKM_HERMES_MODEL='%s'\nKM_HERMES_ROTATE=%d\n",
		o.AgentVersion, o.WebUIVersion, o.Model, rotate)
	return head + libatomicHasFunc + `# 1. libatomic (root). The agent installer's package step installs it with
# sudo -n, which an unprivileged sandbox cannot do. The package repos are
# outside base/network/locked's allowlist, so on such a box this fails.
` + libatomicInstallStep + `NEED_RESTART=0
hermes_bin() {
  H_BIN=""
  for c in /home/sandbox/.local/bin/hermes /home/sandbox/.hermes/hermes-agent/venv/bin/hermes; do
    if [ -x "$c" ]; then H_BIN="$c"; break; fi
  done
}
hermes_ver() {
  hermes_bin
  CUR=""
  if [ -n "$H_BIN" ]; then
    CUR=$(runuser -u sandbox -- bash -lc '"$0" --version' "$H_BIN" 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
  fi
}
# 2. The agent (sandbox user), pinned, from the installer at the pinned TAG on
# raw.githubusercontent.com — not the vendor's website installer and not the
# floating stable channel. Tags live on main: resolve the tag's commit and pin
# the checkout to it. Compared as an exact version so a re-run on an
# up-to-date box never reinstalls and restarts mid-run.
# UAT: confirm --branch main --commit <sha> lands exactly on the tag, that
# install.sh runs under bash -s, and that hermes --version then reports it.
hermes_ver
if [ "$CUR" != "$KM_HERMES_AGENT_VERSION" ]; then
  echo "installing hermes-agent v$KM_HERMES_AGENT_VERSION"
  INSTLOG=$(mktemp)
  if ! runuser -u sandbox -- env KM_V="$KM_HERMES_AGENT_VERSION" bash -lc '
set -o pipefail
SHA=$(git ls-remote https://github.com/NousResearch/hermes-agent.git "refs/tags/v$KM_V^{}" | cut -f1)
[ -n "$SHA" ] || SHA=$(git ls-remote https://github.com/NousResearch/hermes-agent.git "refs/tags/v$KM_V" | cut -f1)
case "$SHA" in
  *[!0-9a-f]*|"") echo "cannot resolve tag v$KM_V on github.com/NousResearch/hermes-agent"; exit 1;;
esac
[ "${#SHA}" -eq 40 ] || { echo "tag v$KM_V resolved to an unexpected id: $SHA"; exit 1; }
curl -fsSL "https://raw.githubusercontent.com/NousResearch/hermes-agent/v$KM_V/scripts/install.sh" | bash -s -- --non-interactive --skip-browser --skip-computer-use --branch main --commit "$SHA"
' >"$INSTLOG" 2>&1; then
    echo "=== install failed ==="
    tail -5 "$INSTLOG"
    rm -f "$INSTLOG"
    exit 0
  fi
  rm -f "$INSTLOG"
  hermes_ver
  if [ "$CUR" != "$KM_HERMES_AGENT_VERSION" ]; then
    echo "=== install failed ==="
    echo "the installer finished but hermes --version reports '$CUR', want $KM_HERMES_AGENT_VERSION (binary: ${H_BIN:-not found})"
    exit 0
  fi
  NEED_RESTART=1
fi
# 3. The WebUI (sandbox user): a shallow clone of the pinned tag, replaced only
# when the checked-out tag differs. Its state lives in ~/.hermes/webui, not in
# the checkout.
# UAT: confirm describe --tags names the tag on a --depth 1 clone.
WT=$(runuser -u sandbox -- bash -lc 'git -C ~/.hermes/hermes-webui describe --tags 2>/dev/null' 2>/dev/null | head -1)
if [ "$WT" != "v$KM_HERMES_WEBUI_VERSION" ]; then
  echo "installing hermes-webui v$KM_HERMES_WEBUI_VERSION"
  INSTLOG=$(mktemp)
  if ! runuser -u sandbox -- env KM_W="$KM_HERMES_WEBUI_VERSION" bash -lc '
set -e
mkdir -p ~/.hermes
cd ~/.hermes
rm -rf hermes-webui.km-new
git clone -q --depth 1 --branch "v$KM_W" https://github.com/nesquena/hermes-webui.git hermes-webui.km-new
rm -rf hermes-webui
mv hermes-webui.km-new hermes-webui
' >"$INSTLOG" 2>&1; then
    echo "=== install failed ==="
    tail -5 "$INSTLOG"
    rm -f "$INSTLOG"
    exit 0
  fi
  rm -f "$INSTLOG"
  NEED_RESTART=1
fi
# The WebUI's only deps are pyyaml + cryptography, in the agent's Python.
# UAT: whether the agent env already has both; if not, which installer works
# there (a uv venv may have no pip) — or HERMES_WEBUI_AUTO_INSTALL=1.
runuser -u sandbox -- bash -lc '` + hermesPickPython("$") + `; "$P" -c "import yaml, cryptography" 2>/dev/null || "$P" -m pip install -q pyyaml cryptography' >/dev/null 2>&1 || true
# 4. The login password, root-only, created only if absent (stable across
# restarts and analysts) or on --rotate-password. mktemp inside the root-only
# dir, then mv: no fixed name anywhere. Never printed.
install -d -m 0700 -o root -g root /etc/km/hermes
if [ "$KM_HERMES_ROTATE" = 1 ] || [ ! -s /etc/km/hermes/webui.env ]; then
  PWTMP=$(mktemp /etc/km/hermes/.webui.env.XXXXXX)
  PW=$(head -c 96 /dev/urandom | base64 | tr -dc 'A-Za-z0-9_-' | cut -c1-32)
  printf 'HERMES_WEBUI_PASSWORD=%s\nHERMES_WEBUI_COOKIE_NAME=km_hermes_session\n' "$PW" >"$PWTMP"
  PW=
  chmod 0600 "$PWTMP"
  mv -f "$PWTMP" /etc/km/hermes/webui.env
  NEED_RESTART=1
fi
# 5. Provider seeding (sandbox user), only when model.provider is unset —
# never clobber an operator's choice — and only when the profile routes
# through Bedrock (execution.useBedrock, block-scoped). The region comes from
# IMDS as root: under the IMDS fence the sandbox user cannot reach it.
# UAT: hermes config get/set key syntax and what get prints when unset.
TOK=$(curl -sS -X PUT --max-time 2 -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' http://169.254.169.254/latest/api/token 2>/dev/null || true)
REGION=$(curl -sS --max-time 2 -H "X-aws-ec2-metadata-token: $TOK" http://169.254.169.254/latest/meta-data/placement/region 2>/dev/null || true)
case "$REGION" in *[!a-z0-9-]*) REGION="";; esac
hermes_bin
SEEDLOG=$(mktemp)
runuser -u sandbox -- env KM_REGION="$REGION" KM_MODEL="$KM_HERMES_MODEL" KM_HBIN="$H_BIN" bash -l >"$SEEDLOG" 2>/dev/null <<'KM_HERMES_SEED_EOF'
[ -n "$KM_HBIN" ] || exit 0
cur=$("$KM_HBIN" config get model.provider 2>/dev/null | head -1 | tr -d '[:space:]')
case "$cur" in ""|None|none|null|"(notset)"|notset) cur="";; esac
if [ -z "$cur" ] && awk '
/^[ ]*execution:[ ]*$/ { match($0, /^ */); ind = RLENGTH; inb = 1; next }
inb && $0 !~ /^[ ]*$/ { match($0, /^ */); if (RLENGTH <= ind) inb = 0 }
inb && /^[ ]*useBedrock:[ ]*true[ ]*$/ { found = 1 }
END { exit found ? 0 : 1 }
' /opt/km/.km-profile.yaml 2>/dev/null; then
  "$KM_HBIN" config set model.provider bedrock >/dev/null 2>&1 && echo "km-seed: provider bedrock"
  "$KM_HBIN" config set model.default "${KM_MODEL:-us.anthropic.claude-sonnet-4-6}" >/dev/null 2>&1 && echo "km-seed: model ${KM_MODEL:-us.anthropic.claude-sonnet-4-6}"
  if [ -n "$KM_REGION" ]; then "$KM_HBIN" config set bedrock.region "$KM_REGION" >/dev/null 2>&1 && echo "km-seed: bedrock.region $KM_REGION"; fi
elif [ -n "$KM_MODEL" ]; then
  "$KM_HBIN" config set model.default "$KM_MODEL" >/dev/null 2>&1 && echo "km-seed: model $KM_MODEL"
fi
KM_HERMES_SEED_EOF
SEED=$(grep '^km-seed: ' "$SEEDLOG" || true)
rm -f "$SEEDLOG"
if [ -n "$SEED" ]; then
  echo "=== seeded ==="
  printf '%s\n' "$SEED"
  NEED_RESTART=1
fi
# 6. The unit, staged inside /etc/systemd/system.
[ -d /workspace ] || install -d -o sandbox -g sandbox /workspace
NEWUNIT=$(mktemp /etc/systemd/system/.km-hermes.XXXXXX)
cat > "$NEWUNIT" <<'KM_HERMES_UNIT_EOF'
` + hermesUnitFile + `KM_HERMES_UNIT_EOF
if ! cmp -s "$NEWUNIT" /etc/systemd/system/km-hermes.service; then
  chmod 0644 "$NEWUNIT"
  mv -f "$NEWUNIT" /etc/systemd/system/km-hermes.service
  systemctl daemon-reload
  NEED_RESTART=1
else
  rm -f "$NEWUNIT"
fi
systemctl enable km-hermes.service >/dev/null 2>&1
if [ "$NEED_RESTART" = 1 ]; then systemctl restart km-hermes.service; else systemctl start km-hermes.service; fi
# 7. Wait for it to answer. The first boot may install the WebUI's deps.
i=0
until curl -fsS -o /dev/null --max-time 2 http://127.0.0.1:8787/health 2>/dev/null; do
  i=$((i+1))
  if [ "$i" -ge 60 ]; then
    echo "=== not ready ==="
    runuser -u sandbox -- tail -20 /home/sandbox/.hermes/km-hermes.log 2>/dev/null | sed -E 's/((PASSWORD|[Pp]assword|token|Token)[=:] ?)[^ ]+/\1REDACTED/g'
    break
  fi
  sleep 1
done
` + hermesStatusScript
}

// hermesBoxState is the parsed result of hermesStatusScript (plus start's and
// connect's extra markers).
type hermesBoxState struct {
	Path             string
	Version          string
	WebUITag         string
	Active           bool
	Enabled          bool
	HealthCode       int
	HealthStatus     string
	ActiveRuns       int
	ActiveStreams    int
	Ready            bool // /health answered 200
	Provider         string
	PasswordFile     bool
	Libatomic        bool
	Signal10         bool
	LibatomicMissing bool
	InstallFailed    string // installer tail when an install failed
	NotReadyLog      string // redacted log tail when the server never answered
	Seeded           []string
	Password         string // only from connect's marker
}

// parseHermesStatus never errors; callers decide which missing facts are fatal.
func parseHermesStatus(out string) hermesBoxState {
	field := func(marker string) string { return strings.TrimSpace(sectionOf(out, marker)) }
	firstLine := func(s string) string {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[:i]
		}
		return strings.TrimSpace(s)
	}
	st := hermesBoxState{
		Path:             field("=== hermes path ==="),
		Version:          herdrSemverRe.FindString(sectionOf(out, "=== hermes version ===")),
		WebUITag:         firstLine(field("=== webui tag ===")),
		Active:           field("=== unit active ===") == "active",
		Enabled:          field("=== unit enabled ===") == "enabled",
		PasswordFile:     field("=== password file ===") == "yes",
		Libatomic:        field("=== libatomic present ===") == "yes",
		Signal10:         field("=== presence signal10 ===") == "yes",
		LibatomicMissing: field("=== libatomic ===") == "missing",
		Password:         firstLine(field("=== password ===")),
	}
	switch p := firstLine(field("=== provider ===")); strings.ToLower(strings.ReplaceAll(p, " ", "")) {
	case "", "none", "null", "(notset)", "notset":
	default:
		st.Provider = p
	}
	st.HealthCode, _ = strconv.Atoi(field("=== health code ==="))
	var h struct {
		Status        string `json:"status"`
		ActiveRuns    int    `json:"active_runs"`
		ActiveStreams int    `json:"active_streams"`
	}
	if json.Unmarshal([]byte(field("=== health ===")), &h) == nil {
		st.HealthStatus, st.ActiveRuns, st.ActiveStreams = h.Status, h.ActiveRuns, h.ActiveStreams
	}
	st.Ready = st.HealthCode == 200
	for _, l := range strings.Split(field("=== seeded ==="), "\n") {
		if l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "km-seed:")); l != "" {
			st.Seeded = append(st.Seeded, l)
		}
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

// hermesSetupError reports the failures that mean start could not get a
// server up.
func hermesSetupError(st hermesBoxState, sandboxID string) error {
	switch {
	case st.LibatomicMissing:
		return fmt.Errorf("libatomic is missing on %s and the OS package repos are unreachable from it "+
			"(a base/network/locked-style egress allowlist). Add cdn.amazonlinux.com (AL2023) or the Ubuntu "+
			"archive to spec.network.egress.allowedDNSSuffixes and recreate, or use an AMI that already has "+
			"libatomic (km ami bake)", sandboxID)
	case st.InstallFailed != "":
		return fmt.Errorf("hermes install failed on %s:\n%s\nThe installers need github.com, raw.githubusercontent.com, "+
			"objects.githubusercontent.com, api.github.com, pypi.org and files.pythonhosted.org (and possibly nodejs.org); "+
			"a locked egress allowlist blocks them — see docs/hermes.md § Egress", sandboxID, st.InstallFailed)
	case st.Path == "" || st.WebUITag == "":
		return fmt.Errorf("hermes is not installed on %s — run `km hermes start %s`", sandboxID, sandboxID)
	case !st.Active:
		return fmt.Errorf("hermes is installed on %s but %s is not running — run `km hermes start %s`", sandboxID, hermesUnitName, sandboxID)
	}
	return nil
}

// hermesStatusReport prints the readiness summary and returns non-nil when no
// server is running.
func hermesStatusReport(w io.Writer, sandboxID string, st hermesBoxState) error {
	if err := hermesSetupError(st, sandboxID); err != nil {
		return err
	}
	enabled := "starts again on resume"
	if !st.Enabled {
		enabled = "not enabled — will not come back after a resume"
	}
	fmt.Fprintf(w, "✓ hermes-agent v%s + webui %s running on %s (127.0.0.1:%d; %s)\n",
		st.Version, st.WebUITag, sandboxID, hermesRemotePort, enabled)
	for _, s := range st.Seeded {
		fmt.Fprintf(w, "  configured: %s\n", s)
	}
	if !st.Ready {
		fmt.Fprintf(w, "⚠ the server is not answering /health yet — see /home/sandbox/.hermes/km-hermes.log on the sandbox\n")
		if st.NotReadyLog != "" {
			fmt.Fprintf(w, "%s\n", st.NotReadyLog)
		}
	} else if st.ActiveRuns > 0 || st.ActiveStreams > 0 {
		fmt.Fprintf(w, "  busy: %d run(s), %d stream(s)\n", st.ActiveRuns, st.ActiveStreams)
	}
	if st.Provider != "" {
		fmt.Fprintf(w, "  model provider: %s\n", st.Provider)
	} else {
		fmt.Fprintf(w, "⚠ no model provider configured — use the WebUI's onboarding wizard, or `km shell %s` then `hermes model`.\n", sandboxID)
	}
	if !st.PasswordFile {
		fmt.Fprintf(w, "⚠ no login password file — run `km hermes start %s` to create it.\n", sandboxID)
	}
	if !st.Signal10 {
		fmt.Fprintf(w, "\n⚠ This sandbox runs a km-presence daemon without signal 10.\n")
		fmt.Fprintf(w, "  An open browser or a running turn does not keep it awake, and claude/codex\n")
		fmt.Fprintf(w, "  subagents Hermes starts may keep it awake long after (signal 5).\n")
		fmt.Fprintf(w, "  Fix: km destroy %s && km create <profile>\n", sandboxID)
	}
	return nil
}

// NewHermesCmd returns the `km hermes` parent command.
func NewHermesCmd(cfg *config.Config) *cobra.Command {
	return newHermesCmdInternal(cfg, nil, nil, nil)
}

func newHermesCmdInternal(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
	var localPort int
	var noPassword bool
	parent := &cobra.Command{
		Use:   "hermes <sandbox-id>",
		Short: "Run Hermes Agent + WebUI on a sandbox in the background and connect your browser over SSM",
		Long: `Run Hermes Agent with Hermes WebUI on a sandbox and reach it from your browser.

  km hermes start  <sandbox-id>   install hermes if needed and start the server in the background
  km hermes <sandbox-id>          forward localhost:8788 to it and print the login password
  km hermes status <sandbox-id>   installed, versions, running, provider, idle-timer wiring
  km hermes stop   <sandbox-id>   stop the server (and keep it stopped across resume)

The server keeps running after you close this terminal. The sandbox stays
awake while a browser is connected or a run is active, and idles out
otherwise — a started server nobody is using does not hold the box up.
Hermes cron jobs do NOT keep it awake.

The hermes CLI (TUI, hermes model, hermes setup) is reachable from
km shell <sandbox-id> as the sandbox user.

Only SSM reaches the server: it listens on the sandbox's 127.0.0.1, behind a
password km generates.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			return hermesRun(c.Context(), cfg, fetcher, execFn, ssmClient, args[0], localPort, noPassword)
		},
	}
	parent.Flags().IntVar(&localPort, "local-port", hermesDefaultLocalPort, "Local port for the SSM forward to the Hermes WebUI")
	parent.Flags().BoolVar(&noPassword, "no-password", false, "Do not fetch and print the login password")
	parent.AddCommand(newHermesStartCmd(cfg, fetcher, ssmClient))
	parent.AddCommand(newHermesStopCmd(cfg, fetcher, ssmClient))
	parent.AddCommand(newHermesStatusCmd(cfg, fetcher, ssmClient))
	return parent
}

// hermesRun is the connect entry point. A package var so the cobra layer can
// be tested for routing without AWS.
var hermesRun = func(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, ref string, localPort int, noPassword bool) error {
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
	return runHermesConnect(ctx, e, s, sandboxID, instanceID, region, localPort, noPassword, os.Stdout)
}

// runHermesConnect checks the server is up, reads the password unless
// noPassword (same round trip), prints the banner, and holds the reconnecting
// forward in the foreground.
func runHermesConnect(ctx context.Context, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID, instanceID, region string, localPort int, noPassword bool, w io.Writer) error {
	script := hermesStatusScript
	if !noPassword {
		script += hermesPasswordScript
	}
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, script)
	if err != nil {
		return fmt.Errorf("ssm pre-flight check: %w", err)
	}
	st := parseHermesStatus(out)
	if err := hermesSetupError(st, sandboxID); err != nil {
		return err
	}

	fmt.Fprintf(w, "✓ hermes-agent v%s + webui %s running on %s\n", st.Version, st.WebUITag, sandboxID)
	if !st.Ready {
		fmt.Fprintf(w, "⚠ the server is not answering /health — `km hermes status %s` shows its log\n", sandboxID)
	}
	fmt.Fprintf(w, "✓ Forwarding localhost:%d → sandbox:%d\n\n", localPort, hermesRemotePort)
	fmt.Fprintf(w, "Open:      http://localhost:%d/\n", localPort)
	switch {
	case st.Password != "":
		fmt.Fprintf(w, "Password:  %s          (km hermes start %s --rotate-password to change)\n", st.Password, sandboxID)
	case !noPassword:
		fmt.Fprintf(w, "⚠ no login password on the sandbox — run `km hermes start %s` to create it\n", sandboxID)
	}
	fmt.Fprintf(w, "Terminal:  km shell %s, then `hermes`\n", sandboxID)
	fmt.Fprintf(w, "The tunnel auto-reconnects; Ctrl-C closes it. The server keeps running — km hermes stop %s.\n\n", sandboxID)

	buildPF := func(c context.Context) *exec.Cmd {
		return buildPortForwardCmd(c, instanceID, region, strconv.Itoa(localPort), strconv.Itoa(hermesRemotePort))
	}
	return runReconnectingPortForward(ctx, execFn, buildPF, httpTunnelProbe(localPort), true, w)
}

// hermesStart is the start entry point (after flag validation). A package var
// so the cobra layer can be tested for routing without AWS.
var hermesStart = func(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI, ref string, o hermesStartOpts) error {
	_, _, s, sandboxID, instanceID, _, err := t3Target(ctx, cfg, fetcher, nil, ssmClient, ref)
	if err != nil {
		return err
	}
	fmt.Printf("Starting hermes-agent v%s + webui v%s on %s (installs on first run, a few minutes)…\n", o.AgentVersion, o.WebUIVersion, sandboxID)
	out, err := sendSSMAndWait(ctx, s, instanceID, hermesStartScript(o))
	if err != nil {
		return fmt.Errorf("ssm start: %w", err)
	}
	if err := hermesStatusReport(os.Stdout, sandboxID, parseHermesStatus(out)); err != nil {
		return err
	}
	fmt.Printf("\nNext: km hermes %s\n", ref)
	return nil
}

func newHermesStartCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	var o hermesStartOpts
	cmd := &cobra.Command{
		Use:   "start <sandbox-id>",
		Short: "Install Hermes Agent + WebUI if needed and start them in the background on a sandbox",
		Long: `Install the pinned Hermes Agent and Hermes WebUI release pair if the sandbox lacks
them, create the login password (once; --rotate-password replaces it), seed a
Bedrock model provider when the profile uses Bedrock and none is configured,
and write and enable km-hermes.service (127.0.0.1:8787, running as the sandbox
user).

Idempotent: on a box that already runs the same versions, it only makes sure
the server is up. The password is never printed here; connect with:
km hermes <sandbox-id>`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			if !t3VersionRe.MatchString(o.AgentVersion) {
				return fmt.Errorf("--agent-version must look like 1.2.3, got %q", o.AgentVersion)
			}
			if !t3VersionRe.MatchString(o.WebUIVersion) {
				return fmt.Errorf("--webui-version must look like 1.2.3 (no leading v), got %q", o.WebUIVersion)
			}
			if o.Model != "" && !hermesModelRe.MatchString(o.Model) {
				return fmt.Errorf("--model must look like a model id (letters, digits, . _ : / -), got %q", o.Model)
			}
			return hermesStart(c.Context(), cfg, fetcher, ssmClient, args[0], o)
		},
	}
	cmd.Flags().StringVar(&o.AgentVersion, "agent-version", hermesAgentVersion, "Hermes Agent release to install")
	cmd.Flags().StringVar(&o.WebUIVersion, "webui-version", hermesWebUIVersion, "Hermes WebUI release to install")
	cmd.Flags().StringVar(&o.Model, "model", "", "Default model to configure (default "+hermesDefaultModel+" when seeding Bedrock)")
	cmd.Flags().BoolVar(&o.RotatePassword, "rotate-password", false, "Replace the login password and restart the server")
	return cmd
}

func newHermesStopCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	return &cobra.Command{
		Use:          "stop <sandbox-id>",
		Short:        "Stop the Hermes server on a sandbox and keep it stopped across resume",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			_, _, s, sandboxID, instanceID, _, err := t3Target(c.Context(), cfg, fetcher, nil, ssmClient, args[0])
			if err != nil {
				return err
			}
			out, err := sendSSMAndWait(c.Context(), s, instanceID, hermesStopScript)
			if err != nil {
				return fmt.Errorf("ssm stop: %w", err)
			}
			return hermesStopReport(os.Stdout, sandboxID, parseHermesStatus(out))
		},
	}
}

func hermesStopReport(w io.Writer, sandboxID string, st hermesBoxState) error {
	if st.Active {
		return fmt.Errorf("%s is still active on %s after stop — check `systemctl status %s` there", hermesUnitName, sandboxID, hermesUnitName)
	}
	fmt.Fprintf(w, "✓ hermes stopped on %s (disabled: stays stopped across resume). Memory, skills and sessions in ~/.hermes are kept.\n", sandboxID)
	return nil
}

func newHermesStatusCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	return &cobra.Command{
		Use:          "status <sandbox-id>",
		Short:        "Report Hermes' install, versions, server, provider, and what the idle timer sees",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			_, _, s, sandboxID, instanceID, _, err := t3Target(c.Context(), cfg, fetcher, nil, ssmClient, args[0])
			if err != nil {
				return err
			}
			out, err := sendSSMAndWait(c.Context(), s, instanceID, hermesStatusScript)
			if err != nil {
				return fmt.Errorf("ssm status check: %w", err)
			}
			return hermesStatusReport(os.Stdout, sandboxID, parseHermesStatus(out))
		},
	}
}
