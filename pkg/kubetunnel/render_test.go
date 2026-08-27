package kubetunnel

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func testTarget() *Target {
	return &Target{
		Context:       "k8s1",
		ServerHost:    "k8s1.corp",
		ServerPort:    6443,
		TLSServerName: "api.k8s1.corp",
		Exec: &ExecConfig{
			APIVersion: "client.authentication.k8s.io/v1",
			Command:    "kubelogin",
			Args:       []string{"get-token"},
		},
	}
}

func TestRenderBoxKubeconfig(t *testing.T) {
	out, err := RenderBoxKubeconfig(testTarget(), 16443)
	if err != nil {
		t.Fatalf("RenderBoxKubeconfig: %v", err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("rendered kubeconfig is not valid YAML: %v", err)
	}

	if doc["current-context"] != "k8s1" {
		t.Errorf("current-context = %v, want k8s1 (a bare kubectl on the box must work)", doc["current-context"])
	}

	clusters, ok := doc["clusters"].([]any)
	if !ok || len(clusters) != 1 {
		t.Fatalf("clusters = %v, want exactly one", doc["clusters"])
	}
	cl := clusters[0].(map[string]any)["cluster"].(map[string]any)

	if got := cl["server"]; got != "https://127.0.0.1:16443" {
		t.Errorf("server = %v, want the loopback bind https://127.0.0.1:16443", got)
	}
	if got := cl["tls-server-name"]; got != "api.k8s1.corp" {
		t.Errorf("tls-server-name = %v, want api.k8s1.corp", got)
	}

	// The real host must appear ONLY as SNI. If it leaked into server: the box
	// would try to dial it directly, which is precisely what it cannot do.
	if strings.Contains(cl["server"].(string), "k8s1.corp") {
		t.Errorf("server %v must not contain the real cluster host", cl["server"])
	}

	users := doc["users"].([]any)
	execBlk := users[0].(map[string]any)["user"].(map[string]any)["exec"].(map[string]any)
	if got := execBlk["command"]; got != BoxShimPath {
		t.Errorf("exec command = %v, want %s", got, BoxShimPath)
	}
	// Mirrors testTarget's exec apiVersion — see
	// TestRenderBoxKubeconfig_MirrorsOperatorExecAPIVersion for why this is
	// never a constant of km's own choosing.
	if got := execBlk["apiVersion"]; got != "client.authentication.k8s.io/v1" {
		t.Errorf("exec apiVersion = %v", got)
	}
	// Required under client.authentication.k8s.io/v1: without it kubectl
	// rejects the exec config with "interactiveMode must be specified" before
	// the plugin ever runs. Never is correct — the shim curls a unix socket.
	if got := execBlk["interactiveMode"]; got != "Never" {
		t.Errorf("exec interactiveMode = %v, want Never (required under apiVersion v1)", got)
	}
}

func TestRenderBoxKubeconfig_Rejects(t *testing.T) {
	tests := []struct {
		name   string
		target *Target
		port   int
	}{
		{"nil target", nil, 6443},
		{"empty SNI", &Target{Context: "c", ServerHost: "h", ServerPort: 443}, 6443},
		{"zero port", testTarget(), 0},
		{"out of range port", testTarget(), 70000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := RenderBoxKubeconfig(tc.target, tc.port); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestRenderShim(t *testing.T) {
	s := RenderShim("/home/sandbox/.km/kubetoken.sock")

	if !strings.HasPrefix(s, "#!/bin/sh\n") {
		t.Errorf("shim must start with a shebang, got: %q", s)
	}
	if !strings.Contains(s, "--unix-socket /home/sandbox/.km/kubetoken.sock") {
		t.Errorf("shim must curl the unix socket, got: %q", s)
	}
	if !strings.Contains(s, TokenPath) {
		t.Errorf("shim must request %s (the broker only serves that path), got: %q", TokenPath, s)
	}
	// exec makes curl's exit status the shim's; without it kubectl cannot tell
	// a failed mint from a malformed credential.
	if !strings.Contains(s, "exec curl") {
		t.Errorf("shim must exec curl so the exit code propagates, got: %q", s)
	}
}

func TestBuildSSHArgs_SilentFailureOptionsAlwaysPresent(t *testing.T) {
	// These two options fail SILENTLY when absent — a missing
	// ExitOnForwardFailure yields a working shell with a dead tunnel. This
	// test exists so removing either one breaks the build rather than a
	// production session.
	for _, interactive := range []bool{true, false} {
		args := BuildSSHArgs(SSHOpts{
			KeyPath: "/k", LocalPort: 2223, Interactive: interactive,
			BindPort: 6443, Target: testTarget(), BrokerSocket: "/tmp/b.sock",
			RemoteCommand: "true",
		})
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "ExitOnForwardFailure=yes") {
			t.Errorf("interactive=%v: ExitOnForwardFailure=yes missing from %q", interactive, joined)
		}
		if !strings.Contains(joined, "StreamLocalBindUnlink=yes") {
			t.Errorf("interactive=%v: StreamLocalBindUnlink=yes missing from %q", interactive, joined)
		}
	}
}

func TestBuildSSHArgs_Interactive(t *testing.T) {
	args := BuildSSHArgs(SSHOpts{
		KeyPath: "/home/me/.km/keys/sb-1", LocalPort: 2223, Interactive: true,
		BindPort: 6443, Target: testTarget(), BrokerSocket: "/tmp/km-broker-x/broker.sock",
	})
	joined := strings.Join(args, " ")

	want := []string{
		"-i /home/me/.km/keys/sb-1",
		"-p 2223",
		"-t",
		"-R 6443:k8s1.corp:6443",
		"-R " + BoxSocketPath + ":/tmp/km-broker-x/broker.sock",
		"sandbox@127.0.0.1",
	}
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("missing %q in %q", w, joined)
		}
	}
}

func TestBuildSSHArgs_ProvisioningHasNoForwardsAndNoTTY(t *testing.T) {
	args := BuildSSHArgs(SSHOpts{
		KeyPath: "/k", LocalPort: 2223, Interactive: false,
		BindPort: 6443, Target: testTarget(), BrokerSocket: "/tmp/b.sock",
		RemoteCommand: "set -e; mkdir -p ~/.km",
	})
	joined := strings.Join(args, " ")

	if strings.Contains(joined, "-R 6443:") {
		t.Errorf("provisioning must not carry the API forward: %q", joined)
	}
	if strings.Contains(joined, "-R "+BoxSocketPath) {
		t.Errorf("provisioning must not carry the socket forward: %q", joined)
	}
	if strings.Contains(joined, " -t ") {
		t.Errorf("provisioning must not request a TTY: %q", joined)
	}
	if args[len(args)-1] != "set -e; mkdir -p ~/.km" {
		t.Errorf("remote command must be last, got %q", args[len(args)-1])
	}
}

func TestBuildSSHArgs_PortsAreDistinctFromClusterPort(t *testing.T) {
	// The loopback bind port need not match the cluster's real port — that is
	// what removes any need for a privileged bind on the box.
	tgt := testTarget()
	tgt.ServerPort = 443
	args := BuildSSHArgs(SSHOpts{
		KeyPath: "/k", LocalPort: 2223, Interactive: true,
		BindPort: 16443, Target: tgt, BrokerSocket: "/tmp/b.sock",
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-R 16443:k8s1.corp:443") {
		t.Errorf("want -R 16443:k8s1.corp:443 in %q", joined)
	}
}

func TestSSHCommandString(t *testing.T) {
	got := SSHCommandString([]string{"-p", "2223", "-o", "ExitOnForwardFailure=yes", "sandbox@127.0.0.1", "set -e; echo hi"})

	if !strings.HasPrefix(got, "ssh ") {
		t.Errorf("must start with ssh, got %q", got)
	}
	// The remote command contains a space and a semicolon, so it must be
	// quoted or pasting the line would run "echo hi" as a separate command.
	if !strings.Contains(got, `'set -e; echo hi'`) {
		t.Errorf("remote command must be quoted, got %q", got)
	}
	if strings.Contains(got, "'-p'") {
		t.Errorf("simple flags should not be quoted, got %q", got)
	}
}

func TestInteractiveRemoteCommand(t *testing.T) {
	got := InteractiveRemoteCommand()

	// kubectl in the tunnel shell must find the generated kubeconfig with no
	// further setup, and without clobbering ~/.kube/config on the box.
	if !strings.Contains(got, "KUBECONFIG="+BoxKubeconfigPath) {
		t.Errorf("must set KUBECONFIG to %s, got %q", BoxKubeconfigPath, got)
	}
	if !strings.Contains(got, "bash -l") {
		t.Errorf("must start a login shell, got %q", got)
	}
}

func TestBuildSSHArgs_InteractiveCarriesRemoteCommandLast(t *testing.T) {
	args := BuildSSHArgs(SSHOpts{
		KeyPath: "/k", LocalPort: 2223, Interactive: true,
		BindPort: 6443, Target: testTarget(), BrokerSocket: "/tmp/b.sock",
		RemoteCommand: InteractiveRemoteCommand(),
	})
	if args[len(args)-1] != InteractiveRemoteCommand() {
		t.Errorf("remote command must be last, got %q", args[len(args)-1])
	}
	// The remote command must not displace either forward.
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-R 6443:k8s1.corp:6443") || !strings.Contains(joined, "-R "+BoxSocketPath) {
		t.Errorf("interactive args lost a forward: %q", joined)
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "''"},
		{"plain", "plain"},
		{"/a/b:c@d=e", "/a/b:c@d=e"},
		{"has space", "'has space'"},
		{"semi;colon", "'semi;colon'"},
		{"it's", `'it'\''s'`},
	}
	for _, tc := range tests {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRenderBoxKubeconfig_MirrorsOperatorExecAPIVersion pins the fix for the
// live-UAT failure "exec plugin is configured to use API version
// client.authentication.k8s.io/v1, plugin returned version
// client.authentication.k8s.io/v1beta1".
//
// kubectl requires an EXACT match between the apiVersion its kubeconfig asks
// for and the one the plugin's ExecCredential carries. The broker returns the
// operator's plugin stdout verbatim and never sets KUBERNETES_EXEC_INFO, so the
// plugin emits its own default — which for kubectl oidc-login is v1beta1. The
// box kubeconfig therefore cannot pick a version of its own; it must mirror
// whatever the operator's kubeconfig declares, since that is the version their
// working local kubectl already agrees with the plugin on.
func TestRenderBoxKubeconfig_MirrorsOperatorExecAPIVersion(t *testing.T) {
	for _, want := range []string{
		"client.authentication.k8s.io/v1beta1",
		"client.authentication.k8s.io/v1",
	} {
		t.Run(want, func(t *testing.T) {
			tgt := testTarget()
			tgt.Exec.APIVersion = want

			out, err := RenderBoxKubeconfig(tgt, 16443)
			if err != nil {
				t.Fatalf("RenderBoxKubeconfig: %v", err)
			}
			var doc map[string]any
			if err := yaml.Unmarshal(out, &doc); err != nil {
				t.Fatalf("not valid YAML: %v", err)
			}
			users := doc["users"].([]any)
			execBlk := users[0].(map[string]any)["user"].(map[string]any)["exec"].(map[string]any)

			if got := execBlk["apiVersion"]; got != want {
				t.Errorf("box exec apiVersion = %v, want %v (must mirror the operator's kubeconfig, "+
					"or kubectl on the box rejects the credential the plugin actually returns)", got, want)
			}
			// interactiveMode is REQUIRED under v1 and valid-but-optional under
			// v1beta1 (k8s 1.23+), so Never stays correct for both.
			if got := execBlk["interactiveMode"]; got != "Never" {
				t.Errorf("interactiveMode = %v, want Never", got)
			}
		})
	}
}

// A target whose exec stanza carries no apiVersion cannot be rendered: guessing
// one reintroduces exactly the mismatch this phase's UAT hit.
func TestRenderBoxKubeconfig_RejectsMissingExecAPIVersion(t *testing.T) {
	tgt := testTarget()
	tgt.Exec.APIVersion = ""
	if _, err := RenderBoxKubeconfig(tgt, 16443); err == nil {
		t.Error("expected an error when the operator's exec stanza has no apiVersion")
	}
}

// TestRenderBoxKubeconfig_EmitsClusterCA pins the box-side half of the
// unknown-authority fix: the CA resolved from the operator's kubeconfig must
// actually reach the file kubectl reads.
func TestRenderBoxKubeconfig_EmitsClusterCA(t *testing.T) {
	tgt := testTarget()
	tgt.CAData = "SU5MSU5FLUNB"

	out, err := RenderBoxKubeconfig(tgt, 16443)
	if err != nil {
		t.Fatalf("RenderBoxKubeconfig: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("not valid YAML: %v", err)
	}
	cl := doc["clusters"].([]any)[0].(map[string]any)["cluster"].(map[string]any)

	if got := cl["certificate-authority-data"]; got != "SU5MSU5FLUNB" {
		t.Errorf("certificate-authority-data = %v, want the operator's CA inlined", got)
	}
	// tls-server-name is what the CA is checked AGAINST. Emitting one without
	// the other is the failure mode this pair of fields exists to avoid.
	if got := cl["tls-server-name"]; got != "api.k8s1.corp" {
		t.Errorf("tls-server-name = %v, want it kept alongside the CA", got)
	}
	if _, ok := cl["insecure-skip-tls-verify"]; ok {
		t.Error("insecure-skip-tls-verify must be absent when a CA is present: kubectl rejects both together")
	}
}

// When no CA is available the box falls back to its system trust store. km must
// not quietly substitute insecure-skip-tls-verify to make the error go away.
func TestRenderBoxKubeconfig_NoCANoInsecure(t *testing.T) {
	out, err := RenderBoxKubeconfig(testTarget(), 16443)
	if err != nil {
		t.Fatalf("RenderBoxKubeconfig: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("not valid YAML: %v", err)
	}
	cl := doc["clusters"].([]any)[0].(map[string]any)["cluster"].(map[string]any)

	if _, ok := cl["certificate-authority-data"]; ok {
		t.Error("certificate-authority-data must be absent when the operator's config has no CA")
	}
	if _, ok := cl["insecure-skip-tls-verify"]; ok {
		t.Error("km must never invent insecure-skip-tls-verify; only the operator's own config may set it")
	}
}

// insecure-skip-tls-verify is mirrored only when the operator asked for it, and
// then the CA must be dropped — kubectl errors out if both are set.
func TestRenderBoxKubeconfig_InsecureExcludesCA(t *testing.T) {
	tgt := testTarget()
	tgt.InsecureSkipTLSVerify = true
	tgt.CAData = "SU5MSU5FLUNB"

	out, err := RenderBoxKubeconfig(tgt, 16443)
	if err != nil {
		t.Fatalf("RenderBoxKubeconfig: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("not valid YAML: %v", err)
	}
	cl := doc["clusters"].([]any)[0].(map[string]any)["cluster"].(map[string]any)

	if got := cl["insecure-skip-tls-verify"]; got != true {
		t.Errorf("insecure-skip-tls-verify = %v, want true", got)
	}
	if _, ok := cl["certificate-authority-data"]; ok {
		t.Error("a CA alongside insecure-skip-tls-verify makes kubectl refuse the config outright")
	}
}
