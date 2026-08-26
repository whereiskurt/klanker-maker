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
		Exec:          &ExecConfig{Command: "kubelogin", Args: []string{"get-token"}},
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
