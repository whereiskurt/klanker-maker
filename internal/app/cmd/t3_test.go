// Package cmd — t3_test.go
// Tests for km t3 start / stop / status / connect.
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
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// t3StubFetcher never reaches AWS; the tests that use it fail before a fetch.
type t3StubFetcher struct{}

func (t3StubFetcher) FetchSandbox(context.Context, string) (*kmaws.SandboxRecord, error) {
	return &kmaws.SandboxRecord{}, nil
}

const t3StatusAllPresent = `=== t3 path ===
/home/sandbox/.local/bin/t3
=== t3 version ===
t3 v0.1.0
=== unit active ===
active
=== unit enabled ===
enabled
=== ready ===
yes
=== providers ===
/usr/bin/claude
/usr/local/bin/codex
=== presence token ===
yes
=== presence signal9 ===
yes
`

func TestParseT3Status_AllPresent(t *testing.T) {
	st := parseT3Status(t3StatusAllPresent)
	if st.Path != "/home/sandbox/.local/bin/t3" || st.Version != "0.1.0" {
		t.Errorf("path/version = %q/%q", st.Path, st.Version)
	}
	if !st.Active || !st.Enabled || !st.Ready || !st.PresenceToken || !st.Signal9 {
		t.Errorf("flags = %+v; want all true", st)
	}
	if strings.Join(st.Providers, ",") != "/usr/bin/claude,/usr/local/bin/codex" {
		t.Errorf("providers = %v", st.Providers)
	}
	if st.LibatomicMissing || st.InstallFailed != "" || st.PairURL != "" {
		t.Errorf("unexpected failure fields: %+v", st)
	}
}

func TestParseT3Status_NotInstalled(t *testing.T) {
	st := parseT3Status(`=== t3 path ===
=== t3 version ===
=== unit active ===
inactive
=== unit enabled ===
disabled
=== ready ===
no
=== providers ===
=== presence token ===
no
=== presence signal9 ===
no
`)
	if st.Path != "" || st.Version != "" || st.Active || st.Enabled || st.Ready || len(st.Providers) != 0 {
		t.Errorf("state = %+v; want empty", st)
	}
	if err := t3SetupError(st, "sb-1"); err == nil || !strings.Contains(err.Error(), "km t3 start sb-1") {
		t.Errorf("err = %v; want one naming `km t3 start sb-1`", err)
	}
}

func TestParseT3Status_ChattyVersion(t *testing.T) {
	st := parseT3Status("=== t3 version ===\nt3 v0.1.0 (abc1234, 2026-10-15)\n=== unit active ===\nactive\n")
	if st.Version != "0.1.0" {
		t.Errorf("version = %q; want 0.1.0", st.Version)
	}
}

func TestParseT3Status_LibatomicMissingIsANamedError(t *testing.T) {
	st := parseT3Status("=== libatomic ===\nmissing\n")
	if !st.LibatomicMissing {
		t.Fatal("LibatomicMissing = false")
	}
	err := t3SetupError(st, "sb-1")
	if err == nil || !strings.Contains(err.Error(), "allowedDNSSuffixes") || !strings.Contains(err.Error(), "km ami bake") {
		t.Fatalf("err = %v; want the locked-egress recipe", err)
	}
}

func TestParseT3Status_InstallFailed(t *testing.T) {
	st := parseT3Status("installing t3 v0.1.0\n=== install failed ===\ncurl: (6) Could not resolve host\n")
	err := t3SetupError(st, "sb-1")
	if err == nil || !strings.Contains(err.Error(), "Could not resolve host") {
		t.Fatalf("err = %v; want the installer tail", err)
	}
}

func TestParseT3Status_PairURLOnlyFromItsMarker(t *testing.T) {
	st := parseT3Status(t3StatusAllPresent + "=== pair url ===\nhttp://localhost:3774/pair#token=abc\n")
	if st.PairURL != "http://localhost:3774/pair#token=abc" {
		t.Fatalf("PairURL = %q", st.PairURL)
	}
}

// TestT3UnitFile_LoadBearingLines pins the unit lines spec §5.3 explains.
func TestT3UnitFile_LoadBearingLines(t *testing.T) {
	for _, want := range []string{
		"User=sandbox",
		"ExecStart=/bin/bash -lc '",
		"exec t3 serve --host 127.0.0.1 --port 3773 --no-browser /workspace >>/home/sandbox/.t3/km-t3code.log 2>&1'",
		"umask 077",
		`chmod 600 "$$L"`,
		`[ ! -O "$$L" ]`,
		"UnsetEnvironment=XDG_RUNTIME_DIR",
		"Environment=T3CODE_TELEMETRY_ENABLED=false",
		"Restart=on-failure",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(t3UnitFile, want) {
			t.Errorf("unit file missing %q", want)
		}
	}
	// StandardOutput=append: is opened by systemd as ROOT; ~/.t3 is
	// sandbox-controlled, so a planted symlink would aim that root open() at any
	// file on the box. The redirect must happen in the sandbox user's shell.
	// A bare $L would be expanded (to nothing) by systemd before bash runs.
	if regexp.MustCompile(`[^$]\$L`).MatchString(t3UnitFile) {
		t.Error("unit file uses $L; systemd expands it — write $$L")
	}
	for _, bad := range []string{"0.0.0.0", "journal", "auto-bootstrap", "Restart=always", "StandardOutput=", "StandardError="} {
		if strings.Contains(t3UnitFile, bad) {
			t.Errorf("unit file must not contain %q", bad)
		}
	}
}

// TestT3StartScript_NeverEchoesPresenceToken: SSM keeps command output, so the
// presence token must go straight from t3 into a root-only file.
func TestT3StartScript_NeverEchoesPresenceToken(t *testing.T) {
	s := t3StartScript("0.1.0")
	for _, want := range []string{
		"--scope orchestration:read --label km-presence --ttl 365d --json",
		`"/etc/km/t3/presence.token.tmp", os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600`,
		`print(d["sessionId"])`,
		"https://raw.githubusercontent.com/pingdotgg/t3code/v$KM_T3_VERSION/scripts/install.sh",
		"T3CODE_VERSION=$KM_T3_VERSION sh",
		"KM_T3_VERSION='0.1.0'",
		"systemctl enable km-t3code.service",
		"set -o pipefail; curl -fsSL",
		"grep -oE '[0-9]+\\.[0-9]+\\.[0-9]+' | head -1)\nif [ \"$CUR\" != \"$KM_T3_VERSION\" ]",
		"mktemp /etc/systemd/system/.km-t3code.XXXXXX",
		"runuser -u sandbox -- tail -20 /home/sandbox/.t3/km-t3code.log",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("start script missing %q", want)
		}
	}
	for _, bad := range []string{`print(d["token"]`, "cat /etc/km/t3", "--token-only", "t3.codes/install.sh"} {
		if strings.Contains(s, bad) {
			t.Errorf("start script must not contain %q", bad)
		}
	}
	if !strings.Contains(s, t3UnitFile) {
		t.Error("start script does not embed t3UnitFile verbatim")
	}
	// The root-run part (everything but the embedded unit text) must never do a
	// root file operation on a sandbox-controlled path: ~/.t3 is owned by the
	// sandbox user and /tmp names are guessable, so either is a symlink away
	// from root writing anywhere on the box.
	rootPart := strings.Replace(s, t3UnitFile, "", 1)
	for _, bad := range []string{
		"/tmp/km-t3",
		"chown sandbox:sandbox /home/sandbox/.t3",
		"chmod 0600 /home/sandbox/.t3",
		"touch /home/sandbox/.t3",
		"install -d -m 0700 -o sandbox -g sandbox /home/sandbox/.t3",
		"tail -20 /home/sandbox/.t3/km-t3code.log 2>/dev/null | sed", // must be via runuser
	} {
		if strings.Contains(strings.Replace(rootPart, "runuser -u sandbox -- tail -20", "", 1), bad) {
			t.Errorf("root part of the start script touches a sandbox-controlled path: %q", bad)
		}
	}
	if !strings.HasSuffix(s, t3StatusScript) {
		t.Error("start script must end with the status probe so start reports like status")
	}
}

// TestT3StartScript_IsValidSh parses the rendered script with sh -n. The SSM
// document runs it under dash, so a bashism would only show up on a live box.
func TestT3StartScript_IsValidSh(t *testing.T) {
	for name, script := range map[string]string{
		"start":   t3StartScript("0.1.0"),
		"stop":    t3StopScript,
		"connect": t3StatusScript + t3PairScript(3774, "sb-1"),
	} {
		cmd := exec.Command("sh", "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s script fails sh -n: %v\n%s", name, err, out)
		}
	}
}

func TestT3StopScript_Disables(t *testing.T) {
	if !strings.HasPrefix(t3StopScript, "systemctl disable --now km-t3code.service") {
		t.Fatalf("stop must disable as well as stop, so resume does not bring it back")
	}
}

func TestT3PairScript(t *testing.T) {
	s := t3PairScript(3774, "sb-1")
	if !strings.Contains(s, "t3 auth pairing create --base-url http://localhost:3774 --json --ttl 5m --label km-t3-sb-1") {
		t.Errorf("pair script = %s", s)
	}
	if !strings.Contains(s, `get("pairUrl","")`) || strings.Contains(s, "credential") {
		t.Errorf("pair script must print only pairUrl: %s", s)
	}
}

// TestT3PortPinnedAcrossSites: the unit, the connect forward and km-presence
// signal 9 must agree on the port, or signal 9 watches a port nothing listens on.
func TestT3PortPinnedAcrossSites(t *testing.T) {
	if !strings.Contains(t3UnitFile, "--port "+strconv.Itoa(t3RemotePort)+" ") {
		t.Errorf("unit file does not serve on t3RemotePort %d", t3RemotePort)
	}
	if !strings.Contains(t3StatusScript, "127.0.0.1:"+strconv.Itoa(t3RemotePort)+"/") {
		t.Errorf("status probe does not use t3RemotePort")
	}
	src, err := os.ReadFile("../../../cmd/km-presence/t3.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`const t3Port = (\d+)`).FindSubmatch(src)
	if m == nil || string(m[1]) != strconv.Itoa(t3RemotePort) {
		t.Fatalf("km-presence t3Port = %q; want %d", m, t3RemotePort)
	}
	if !strings.Contains(string(src), `"/km-t3code.service"`) || t3UnitName != "km-t3code.service" {
		t.Error("km-presence's cgroup match and t3UnitName disagree")
	}
}

func TestT3StatusReport_Running(t *testing.T) {
	var buf bytes.Buffer
	if err := t3StatusReport(&buf, "sb-1", parseT3Status(t3StatusAllPresent)); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "✓ t3 v0.1.0 running on sb-1") {
		t.Errorf("report = %s", out)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("healthy box printed a warning: %s", out)
	}
}

func TestT3StatusReport_Warnings(t *testing.T) {
	st := parseT3Status(t3StatusAllPresent)
	st.Signal9, st.Providers, st.PresenceToken = false, nil, false
	var buf bytes.Buffer
	if err := t3StatusReport(&buf, "sb-1", st); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"without signal 9", "30 min", "km destroy sb-1", "km agent auth sb-1", "km t3 start sb-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestT3StatusReport_InstalledNotRunning(t *testing.T) {
	st := parseT3Status(t3StatusAllPresent)
	st.Active = false
	err := t3StatusReport(&bytes.Buffer{}, "sb-1", st)
	if err == nil || !strings.Contains(err.Error(), "km t3 start sb-1") {
		t.Fatalf("err = %v", err)
	}
}

func TestT3StopReport(t *testing.T) {
	st := parseT3Status(t3StatusAllPresent)
	if err := t3StopReport(&bytes.Buffer{}, "sb-1", st); err == nil {
		t.Error("a still-active unit after stop must be an error")
	}
	st.Active = false
	var buf bytes.Buffer
	if err := t3StopReport(&buf, "sb-1", st); err != nil || !strings.Contains(buf.String(), "stays stopped across resume") {
		t.Errorf("err=%v out=%s", err, buf.String())
	}
}

func TestRunT3Connect_RefusesWhenNotRunning(t *testing.T) {
	r := &recordingSSM{stdout: strings.Replace(t3StatusAllPresent, "=== unit active ===\nactive", "=== unit active ===\ninactive", 1)}
	execFn := func(*exec.Cmd) error {
		t.Fatal("forward started although the server is not running")
		return nil
	}
	err := runT3Connect(context.Background(), execFn, r, "sb-1", "i-0abc", "us-east-1", 3774, false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "km t3 start sb-1") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunT3Connect_NoPairSendsNoPairCommand(t *testing.T) {
	r := &recordingSSM{stdout: strings.Replace(t3StatusAllPresent, "active\n", "inactive\n", 1)}
	_ = runT3Connect(context.Background(), func(*exec.Cmd) error { return nil }, r, "sb-1", "i-0abc", "us-east-1", 3774, true, &bytes.Buffer{})
	for _, c := range r.sent {
		if strings.Contains(c, "pairing create") {
			t.Fatalf("--no-pair still sent a pairing command: %s", c)
		}
	}
	r2 := &recordingSSM{stdout: r.stdout}
	_ = runT3Connect(context.Background(), func(*exec.Cmd) error { return nil }, r2, "sb-1", "i-0abc", "us-east-1", 3774, false, &bytes.Buffer{})
	if len(r2.sent) != 1 || !strings.Contains(r2.sent[0], "pairing create") {
		t.Fatalf("default connect must mint a pairing link in the same round trip: %v", r2.sent)
	}
}

func TestT3Run_PortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	err = t3Run(context.Background(), &config.Config{}, &t3StubFetcher{}, nil, &recordingSSM{}, "sb-1", port, false)
	if err == nil || !strings.Contains(err.Error(), "--local-port") {
		t.Fatalf("err = %v", err)
	}
}

func TestT3Cmd_Routing(t *testing.T) {
	orig := t3Run
	t.Cleanup(func() { t3Run = orig })
	var gotRef string
	var gotPort int
	var gotNoPair bool
	t3Run = func(_ context.Context, _ *config.Config, _ SandboxFetcher, _ ShellExecFunc, _ SSMSendAPI, ref string, localPort int, noPair bool) error {
		gotRef, gotPort, gotNoPair = ref, localPort, noPair
		return nil
	}
	cmd := newT3CmdInternal(&config.Config{}, nil, nil, nil)
	cmd.SetArgs([]string{"mybox"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotRef != "mybox" || gotPort != 3774 || gotNoPair {
		t.Errorf("connect got ref=%q port=%d noPair=%v; want mybox 3774 false", gotRef, gotPort, gotNoPair)
	}

	cmd = newT3CmdInternal(&config.Config{}, nil, nil, nil)
	cmd.SetArgs([]string{"mybox", "--local-port", "4000", "--no-pair"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotPort != 4000 || !gotNoPair {
		t.Errorf("flags did not reach connect: port=%d noPair=%v", gotPort, gotNoPair)
	}

	names := map[string]bool{}
	for _, c := range newT3CmdInternal(&config.Config{}, nil, nil, nil).Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"start", "stop", "status"} {
		if !names[want] {
			t.Errorf("missing subcommand %q", want)
		}
	}
}

func TestT3StartCmd_RejectsBadVersion(t *testing.T) {
	cmd := newT3CmdInternal(&config.Config{}, &t3StubFetcher{}, nil, &recordingSSM{})
	cmd.SetArgs([]string{"start", "mybox", "--version", "latest; rm -rf /"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--version must look like") {
		t.Fatalf("err = %v", err)
	}
}
