// Package cmd — hermes_test.go
// Tests for km hermes start / stop / status / connect.
package cmd

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

const hermesStatusAllPresent = `=== hermes path ===
/home/sandbox/.local/bin/hermes
=== hermes version ===
Hermes Agent v0.22.4 (python 3.12.7)
=== webui tag ===
v0.52.544
=== unit active ===
active
=== unit enabled ===
enabled
=== health ===
{"status": "ok", "active_streams": 0, "active_runs": 0, "runs": [], "last_run_finished_at": null}
=== health code ===
200
=== provider ===
bedrock
=== password file ===
yes
=== libatomic present ===
yes
=== presence signal10 ===
yes
`

func TestParseHermesStatus_AllPresent(t *testing.T) {
	st := parseHermesStatus(hermesStatusAllPresent)
	if st.Path != "/home/sandbox/.local/bin/hermes" || st.Version != "0.22.4" || st.WebUITag != "v0.52.544" {
		t.Errorf("path/version/tag = %q/%q/%q", st.Path, st.Version, st.WebUITag)
	}
	if !st.Active || !st.Enabled || !st.Ready || !st.PasswordFile || !st.Libatomic || !st.Signal10 {
		t.Errorf("flags = %+v; want all true", st)
	}
	if st.Provider != "bedrock" || st.HealthStatus != "ok" || st.HealthCode != 200 {
		t.Errorf("provider/health = %q/%q/%d", st.Provider, st.HealthStatus, st.HealthCode)
	}
	if st.LibatomicMissing || st.InstallFailed != "" || st.Password != "" || st.NotReadyLog != "" {
		t.Errorf("unexpected failure fields: %+v", st)
	}
	if err := hermesSetupError(st, "sb-1"); err != nil {
		t.Errorf("healthy box: %v", err)
	}
}

func TestParseHermesStatus_NotInstalled(t *testing.T) {
	st := parseHermesStatus(`=== hermes path ===
=== hermes version ===
=== webui tag ===
=== unit active ===
inactive
=== unit enabled ===
disabled
=== health ===
=== health code ===
000
=== provider ===
=== password file ===
no
=== libatomic present ===
no
=== presence signal10 ===
no
`)
	if st.Path != "" || st.Version != "" || st.WebUITag != "" || st.Active || st.Enabled || st.Ready || st.Provider != "" || st.PasswordFile {
		t.Errorf("state = %+v; want empty", st)
	}
	if err := hermesSetupError(st, "sb-1"); err == nil || !strings.Contains(err.Error(), "km hermes start sb-1") {
		t.Fatalf("err = %v; want a pointer at km hermes start", err)
	}
}

// The agent's --version output is not a contract: pull the semver out of
// whatever surrounds it.
func TestParseHermesStatus_ChattyVersion(t *testing.T) {
	out := strings.Replace(hermesStatusAllPresent, "Hermes Agent v0.22.4 (python 3.12.7)",
		"Loading config from ~/.hermes/config.yaml\nhermes-agent 0.22.4\nupdate available: 0.23.0", 1)
	if st := parseHermesStatus(out); st.Version != "0.22.4" {
		t.Errorf("Version = %q; want 0.22.4", st.Version)
	}
}

func TestParseHermesStatus_NotRunning(t *testing.T) {
	out := strings.Replace(hermesStatusAllPresent, "=== unit active ===\nactive", "=== unit active ===\nfailed", 1)
	st := parseHermesStatus(out)
	if st.Active {
		t.Fatal("Active = true for a failed unit")
	}
	err := hermesSetupError(st, "sb-1")
	if err == nil || !strings.Contains(err.Error(), "not running") || !strings.Contains(err.Error(), "km hermes start sb-1") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseHermesStatus_NoProvider(t *testing.T) {
	for _, v := range []string{"", "None", "null", "(not set)"} {
		out := strings.Replace(hermesStatusAllPresent, "=== provider ===\nbedrock", "=== provider ===\n"+v, 1)
		if st := parseHermesStatus(out); st.Provider != "" {
			t.Errorf("provider output %q parsed as %q; want empty", v, st.Provider)
		}
	}
}

// A 503 still carries the activity counters; the parser keeps them and marks
// the server not ready.
func TestParseHermesStatus_DegradedHealth(t *testing.T) {
	out := strings.Replace(hermesStatusAllPresent,
		`{"status": "ok", "active_streams": 0, "active_runs": 0, "runs": [], "last_run_finished_at": null}`+"\n=== health code ===\n200",
		`{"status": "degraded", "active_streams": 1, "active_runs": 2}`+"\n=== health code ===\n503", 1)
	st := parseHermesStatus(out)
	if st.Ready || st.HealthCode != 503 || st.HealthStatus != "degraded" || st.ActiveRuns != 2 || st.ActiveStreams != 1 {
		t.Errorf("state = %+v", st)
	}
}

func TestParseHermesStatus_LibatomicMissingIsANamedError(t *testing.T) {
	st := parseHermesStatus("=== libatomic ===\nmissing\n")
	if !st.LibatomicMissing {
		t.Fatal("LibatomicMissing = false")
	}
	err := hermesSetupError(st, "sb-1")
	if err == nil || !strings.Contains(err.Error(), "allowedDNSSuffixes") || !strings.Contains(err.Error(), "km ami bake") {
		t.Fatalf("err = %v; want the locked-egress recipe", err)
	}
}

func TestParseHermesStatus_InstallFailed(t *testing.T) {
	st := parseHermesStatus("installing hermes-agent v0.22.4\n=== install failed ===\ncurl: (6) Could not resolve host\n")
	err := hermesSetupError(st, "sb-1")
	if err == nil || !strings.Contains(err.Error(), "Could not resolve host") || !strings.Contains(err.Error(), "pypi.org") {
		t.Fatalf("err = %v; want the installer tail and the egress hint", err)
	}
}

func TestParseHermesStatus_PasswordOnlyFromItsMarker(t *testing.T) {
	if st := parseHermesStatus(hermesStatusAllPresent); st.Password != "" {
		t.Fatalf("Password = %q without the marker", st.Password)
	}
	st := parseHermesStatus(hermesStatusAllPresent + "=== password ===\nAbC123_-xyz\n")
	if st.Password != "AbC123_-xyz" {
		t.Fatalf("Password = %q", st.Password)
	}
}

// TestHermesUnitFile_LoadBearingLines pins the unit lines spec §3.2 explains.
func TestHermesUnitFile_LoadBearingLines(t *testing.T) {
	for _, want := range []string{
		"User=sandbox",
		"Group=sandbox",
		"ExecStart=/bin/bash -lc '",
		"bootstrap.py --foreground --no-browser --host 127.0.0.1 8787 >>\"$$L\" 2>&1'",
		"Environment=HERMES_WEBUI_HOST=127.0.0.1",
		"Environment=HERMES_WEBUI_PORT=8787",
		"Environment=HERMES_HOME=/home/sandbox/.hermes",
		"Environment=HERMES_WEBUI_DEFAULT_WORKSPACE=/workspace",
		"EnvironmentFile=/etc/km/hermes/webui.env",
		"umask 077",
		`chmod 600 "$$L"`,
		`[ ! -O "$$L" ]`,
		"UnsetEnvironment=XDG_RUNTIME_DIR",
		"KillMode=mixed",
		"TimeoutStopSec=120",
		"Restart=on-failure",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(hermesUnitFile, want) {
			t.Errorf("unit file missing %q", want)
		}
	}
	// systemd expands $VAR in ExecStart before bash runs: every shell variable
	// must be written $$VAR.
	if m := regexp.MustCompile(`[^$]\$[A-Za-z_{]`).FindString(hermesUnitFile); m != "" {
		t.Errorf("unit file uses a bare $ (%q); systemd expands it — write $$", m)
	}
	for _, bad := range []string{"0.0.0.0", "journal", "Restart=always", "StandardOutput=", "StandardError=", "HERMES_WEBUI_PASSWORD"} {
		if strings.Contains(hermesUnitFile, bad) {
			t.Errorf("unit file must not contain %q", bad)
		}
	}
}

func TestHermesStartScript_PinsAndPassword(t *testing.T) {
	s := hermesStartScript(hermesStartOpts{AgentVersion: "0.22.4", WebUIVersion: "0.52.544"})
	for _, want := range []string{
		"KM_HERMES_AGENT_VERSION='0.22.4'",
		"KM_HERMES_WEBUI_VERSION='0.52.544'",
		"https://raw.githubusercontent.com/NousResearch/hermes-agent/v$KM_V/scripts/install.sh",
		`git ls-remote https://github.com/NousResearch/hermes-agent.git "refs/tags/v$KM_V^{}"`,
		"--non-interactive --skip-browser --skip-computer-use --branch main --commit \"$SHA\"",
		"set -o pipefail",
		`git clone -q --depth 1 --branch "v$KM_W" https://github.com/nesquena/hermes-webui.git`,
		"install -d -m 0700 -o root -g root /etc/km/hermes",
		"mktemp /etc/km/hermes/.webui.env.XXXXXX",
		"HERMES_WEBUI_COOKIE_NAME=km_hermes_session",
		"/dev/urandom",
		"mktemp /etc/systemd/system/.km-hermes.XXXXXX",
		"systemctl enable km-hermes.service",
		"runuser -u sandbox -- tail -20 /home/sandbox/.hermes/km-hermes.log",
		libatomicInstallStep,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("start script missing %q", want)
		}
	}
	// Never the unpinned installer, never the stable channel.
	for _, bad := range []string{"hermes-agent.nousresearch.com", "--channel", "0.0.0.0", "/hermes-agent/main/scripts"} {
		if strings.Contains(s, bad) {
			t.Errorf("start script must not contain %q", bad)
		}
	}
	if !strings.Contains(s, hermesUnitFile) {
		t.Error("start script does not embed hermesUnitFile verbatim")
	}
	if !strings.HasSuffix(s, hermesStatusScript) {
		t.Error("start script must end with the status probe so start reports like status")
	}
}

// The password is created only if absent (or on --rotate-password) and never
// reaches stdout: SSM keeps command output.
func TestHermesStartScript_PasswordOnlyIfAbsentAndNeverEchoed(t *testing.T) {
	s := hermesStartScript(hermesStartOpts{AgentVersion: "0.22.4", WebUIVersion: "0.52.544"})
	if !strings.Contains(s, `if [ "$KM_HERMES_ROTATE" = 1 ] || [ ! -s /etc/km/hermes/webui.env ]; then`) {
		t.Error("password must be written only when absent or rotating")
	}
	if !strings.Contains(s, "KM_HERMES_ROTATE=0") {
		t.Error("default start must not rotate")
	}
	if r := hermesStartScript(hermesStartOpts{AgentVersion: "0.22.4", WebUIVersion: "0.52.544", RotatePassword: true}); !strings.Contains(r, "KM_HERMES_ROTATE=1") {
		t.Error("--rotate-password did not reach the script")
	}
	for _, script := range []string{s, hermesStatusScript, hermesStopScript} {
		for _, bad := range []string{`echo "$PW"`, `echo $PW`, "cat /etc/km/hermes", "=== password ===", "sed -n 's/^HERMES_WEBUI_PASSWORD="} {
			if strings.Contains(script, bad) {
				t.Errorf("script prints the password: contains %q", bad)
			}
		}
	}
	// The only write to the password file is the printf into the mktemp file.
	if strings.Count(s, "HERMES_WEBUI_PASSWORD=") != 1 || !strings.Contains(s, `printf 'HERMES_WEBUI_PASSWORD=%s\nHERMES_WEBUI_COOKIE_NAME=km_hermes_session\n' "$PW" >"$PWTMP"`) {
		t.Error("password must be written once, via printf into the mktemp file")
	}
}

// Provider seeding runs as the sandbox user, only when model.provider is
// unset, and only when the profile has execution.useBedrock: true.
func TestHermesStartScript_ProviderSeedingGated(t *testing.T) {
	s := hermesStartScript(hermesStartOpts{AgentVersion: "0.22.4", WebUIVersion: "0.52.544", Model: "us.anthropic.claude-opus-4-1"})
	for _, want := range []string{
		"KM_HERMES_MODEL='us.anthropic.claude-opus-4-1'",
		`config get model.provider`,
		`if [ -z "$cur" ] && awk`,
		"/^[ ]*execution:[ ]*$/",
		"useBedrock:[ ]*true",
		"/opt/km/.km-profile.yaml",
		`config set model.provider bedrock`,
		`config set model.default "${KM_MODEL:-us.anthropic.claude-sonnet-4-6}"`,
		`config set bedrock.region "$KM_REGION"`,
		"runuser -u sandbox -- env KM_REGION=",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("start script missing %q", want)
		}
	}
	// The seeding block must not set the provider unconditionally.
	seed := s[strings.Index(s, "KM_HERMES_SEED_EOF"):]
	if i := strings.Index(seed, "config set model.provider"); i < strings.Index(seed, `if [ -z "$cur" ]`) {
		t.Error("model.provider is set before the absent-provider check")
	}
}

// The root-run part of the script never does a root file operation on a
// sandbox-controlled path (~sandbox, /workspace, a fixed /tmp name): a planted
// symlink would turn it into a root write anywhere on the box. Work in ~/.hermes
// happens via runuser; root writes stay in /etc/km/hermes and /etc/systemd.
func TestHermesStartScript_NoRootFileOpsOnSandboxPaths(t *testing.T) {
	s := hermesStartScript(hermesStartOpts{AgentVersion: "0.22.4", WebUIVersion: "0.52.544"})
	rootPart := strings.Replace(s, hermesUnitFile, "", 1)
	// Strip everything handed to the sandbox user: single-quoted bash -lc bodies
	// and the seeding heredoc.
	rootPart = regexp.MustCompile(`(?s)bash -lc '.*?'`).ReplaceAllString(rootPart, "")
	rootPart = regexp.MustCompile(`(?s)<<'KM_HERMES_SEED_EOF'.*?\nKM_HERMES_SEED_EOF`).ReplaceAllString(rootPart, "")
	rootPart = strings.ReplaceAll(rootPart, "runuser -u sandbox -- tail -20 /home/sandbox/.hermes/km-hermes.log", "")
	for _, bad := range []string{
		"/tmp/",
		"chown", "chmod 0600 /home", "touch /home", "rm -rf /home", "mkdir -p /home",
		"> /home", ">/home", ">> /home", "git -C /home", "git clone",
		"cat /opt/km/.km-profile.yaml", "/workspace/",
	} {
		if strings.Contains(rootPart, bad) {
			t.Errorf("root part of the start script touches a sandbox-controlled path: %q", bad)
		}
	}
}

// TestHermesScripts_AreValidSh parses each rendered script with sh -n. The SSM
// document runs it under dash, so a bashism would only show up on a live box.
func TestHermesScripts_AreValidSh(t *testing.T) {
	for name, script := range map[string]string{
		"start":   hermesStartScript(hermesStartOpts{AgentVersion: "0.22.4", WebUIVersion: "0.52.544", Model: "m", RotatePassword: true}),
		"stop":    hermesStopScript,
		"connect": hermesStatusScript + hermesPasswordScript,
	} {
		cmd := exec.Command("sh", "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s script fails sh -n: %v\n%s", name, err, out)
		}
	}
}

func TestHermesStopScript_Disables(t *testing.T) {
	if !strings.HasPrefix(hermesStopScript, "systemctl disable --now km-hermes.service") {
		t.Fatalf("stop must disable as well as stop, so resume does not bring it back")
	}
}

// TestHermesPortPinnedAcrossSites: the unit, the connect forward and
// km-presence signal 10 must agree on the port, or signal 10 watches a port
// nothing listens on.
func TestHermesPortPinnedAcrossSites(t *testing.T) {
	if !strings.Contains(hermesUnitFile, "--host 127.0.0.1 "+strconv.Itoa(hermesRemotePort)+" ") ||
		!strings.Contains(hermesUnitFile, "HERMES_WEBUI_PORT="+strconv.Itoa(hermesRemotePort)+"\n") {
		t.Errorf("unit file does not serve on hermesRemotePort %d", hermesRemotePort)
	}
	if !strings.Contains(hermesStatusScript, "127.0.0.1:"+strconv.Itoa(hermesRemotePort)+"/health") {
		t.Errorf("status probe does not use hermesRemotePort")
	}
	src, err := os.ReadFile("../../../cmd/km-presence/hermes.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`hermesPort\s*=\s*(\d+)`).FindSubmatch(src)
	if m == nil || string(m[1]) != strconv.Itoa(hermesRemotePort) {
		t.Fatalf("km-presence hermesPort = %q; want %d", m, hermesRemotePort)
	}
	if !strings.Contains(string(src), "checkHermesInUse") || !strings.Contains(hermesStatusScript, "checkHermesInUse") {
		t.Error("status's signal 10 probe and km-presence's symbol disagree")
	}
	presenceDir, _ := os.ReadFile("../../../cmd/km-presence/main.go")
	all := string(src) + string(presenceDir)
	if !strings.Contains(all, `"/km-hermes.service"`) && !strings.Contains(all, `"km-hermes.service"`) || hermesUnitName != "km-hermes.service" {
		t.Error("km-presence's cgroup match and hermesUnitName disagree")
	}
}

func TestHermesStatusReport_Running(t *testing.T) {
	var buf bytes.Buffer
	if err := hermesStatusReport(&buf, "sb-1", parseHermesStatus(hermesStatusAllPresent)); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "✓ hermes-agent v0.22.4 + webui v0.52.544 running on sb-1") {
		t.Errorf("report = %s", out)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("healthy box printed a warning: %s", out)
	}
}

func TestHermesStatusReport_Warnings(t *testing.T) {
	st := parseHermesStatus(hermesStatusAllPresent)
	st.Signal10, st.Provider, st.PasswordFile, st.Ready = false, "", false, false
	var buf bytes.Buffer
	if err := hermesStatusReport(&buf, "sb-1", st); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"without signal 10", "km destroy sb-1", "no model provider", "hermes model", "km hermes start sb-1", "not answering"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestHermesStopReport(t *testing.T) {
	st := parseHermesStatus(hermesStatusAllPresent)
	if err := hermesStopReport(&bytes.Buffer{}, "sb-1", st); err == nil {
		t.Error("a still-active unit after stop must be an error")
	}
	st.Active = false
	var buf bytes.Buffer
	if err := hermesStopReport(&buf, "sb-1", st); err != nil || !strings.Contains(buf.String(), "stays stopped across resume") {
		t.Errorf("err=%v out=%s", err, buf.String())
	}
}

func TestRunHermesConnect_RefusesWhenNotRunning(t *testing.T) {
	r := &recordingSSM{stdout: strings.Replace(hermesStatusAllPresent, "=== unit active ===\nactive", "=== unit active ===\ninactive", 1)}
	execFn := func(*exec.Cmd) error {
		t.Fatal("forward started although the server is not running")
		return nil
	}
	err := runHermesConnect(context.Background(), execFn, r, "sb-1", "i-0abc", "us-east-1", 8788, false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "km hermes start sb-1") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunHermesConnect_NoPasswordSendsNoPasswordCommand(t *testing.T) {
	stopped := strings.Replace(hermesStatusAllPresent, "=== unit active ===\nactive", "=== unit active ===\ninactive", 1)
	r := &recordingSSM{stdout: stopped}
	_ = runHermesConnect(context.Background(), func(*exec.Cmd) error { return nil }, r, "sb-1", "i-0abc", "us-east-1", 8788, true, &bytes.Buffer{})
	if len(r.sent) != 1 {
		t.Fatalf("connect must be one round trip, sent %d", len(r.sent))
	}
	for _, c := range r.sent {
		if strings.Contains(c, "=== password ===") || strings.Contains(c, "HERMES_WEBUI_PASSWORD") {
			t.Fatalf("--no-password still sent a password command: %s", c)
		}
	}
	r2 := &recordingSSM{stdout: stopped}
	_ = runHermesConnect(context.Background(), func(*exec.Cmd) error { return nil }, r2, "sb-1", "i-0abc", "us-east-1", 8788, false, &bytes.Buffer{})
	if len(r2.sent) != 1 || !strings.Contains(r2.sent[0], "=== password ===") {
		t.Fatalf("default connect must read the password in the same round trip: %v", r2.sent)
	}
}

func TestHermesRun_PortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	err = hermesRun(context.Background(), &config.Config{}, &t3StubFetcher{}, nil, &recordingSSM{}, "sb-1", port, false)
	if err == nil || !strings.Contains(err.Error(), "--local-port") {
		t.Fatalf("err = %v", err)
	}
}

func TestHermesCmd_Routing(t *testing.T) {
	origRun, origStart := hermesRun, hermesStart
	t.Cleanup(func() { hermesRun, hermesStart = origRun, origStart })
	var gotRef string
	var gotPort int
	var gotNoPw bool
	hermesRun = func(_ context.Context, _ *config.Config, _ SandboxFetcher, _ ShellExecFunc, _ SSMSendAPI, ref string, localPort int, noPassword bool) error {
		gotRef, gotPort, gotNoPw = ref, localPort, noPassword
		return nil
	}
	cmd := newHermesCmdInternal(&config.Config{}, nil, nil, nil)
	cmd.SetArgs([]string{"mybox"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotRef != "mybox" || gotPort != 8788 || gotNoPw {
		t.Errorf("connect got ref=%q port=%d noPassword=%v; want mybox 8788 false", gotRef, gotPort, gotNoPw)
	}
	cmd = newHermesCmdInternal(&config.Config{}, nil, nil, nil)
	cmd.SetArgs([]string{"mybox", "--local-port", "4000", "--no-password"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotPort != 4000 || !gotNoPw {
		t.Errorf("flags did not reach connect: port=%d noPassword=%v", gotPort, gotNoPw)
	}

	var gotStartRef string
	var gotOpts hermesStartOpts
	hermesStart = func(_ context.Context, _ *config.Config, _ SandboxFetcher, _ SSMSendAPI, ref string, o hermesStartOpts) error {
		gotStartRef, gotOpts = ref, o
		return nil
	}
	cmd = newHermesCmdInternal(&config.Config{}, nil, nil, nil)
	cmd.SetArgs([]string{"start", "mybox"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotStartRef != "mybox" || gotOpts != (hermesStartOpts{AgentVersion: hermesAgentVersion, WebUIVersion: hermesWebUIVersion}) {
		t.Errorf("start defaults = %q %+v", gotStartRef, gotOpts)
	}
	cmd = newHermesCmdInternal(&config.Config{}, nil, nil, nil)
	cmd.SetArgs([]string{"start", "mybox", "--agent-version", "0.23.0", "--webui-version", "0.53.1", "--model", "us.anthropic.claude-opus-4-1", "--rotate-password"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := hermesStartOpts{AgentVersion: "0.23.0", WebUIVersion: "0.53.1", Model: "us.anthropic.claude-opus-4-1", RotatePassword: true}
	if gotOpts != want {
		t.Errorf("start flags = %+v; want %+v", gotOpts, want)
	}

	names := map[string]bool{}
	for _, c := range newHermesCmdInternal(&config.Config{}, nil, nil, nil).Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"start", "stop", "status"} {
		if !names[want] {
			t.Errorf("missing subcommand %q", want)
		}
	}
}

func TestHermesStartCmd_RejectsBadInputs(t *testing.T) {
	for _, args := range [][]string{
		{"--agent-version", "latest; rm -rf /"},
		{"--webui-version", "v0.52.544"},
		{"--model", "x' && reboot"},
	} {
		cmd := newHermesCmdInternal(&config.Config{}, &t3StubFetcher{}, nil, &recordingSSM{})
		cmd.SetArgs(append([]string{"start", "mybox"}, args...))
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "must look like") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
}
