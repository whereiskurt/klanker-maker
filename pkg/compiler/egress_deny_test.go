package compiler_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/compiler"
)

// A profile that declares no denies must not mention them anywhere in the
// rendered user-data. This is what keeps every existing sandbox byte-identical
// and the frozen goldens untouched.
func TestUserData_NoDenyLists_EmitsNothing(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")

	artifacts, err := compiler.Compile(p, "sb-denynone", false, testNetwork(), nil)
	if err != nil {
		t.Fatalf("Compile error = %v", err)
	}

	for _, token := range []string{"DENIED_SUFFIXES", "DENIED_HOSTS", "--denied-dns", "--denied-hosts"} {
		if strings.Contains(artifacts.UserData, token) {
			t.Errorf("UserData must not contain %q when the profile declares no denies", token)
		}
	}
}

func TestUserData_DeniedDNSSuffixes_EmitsProxyEnv(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	p.Spec.Network.Egress.DeniedDNSSuffixes = []string{"evil.example.com", ".tracker.net"}

	artifacts, err := compiler.Compile(p, "sb-denydns", false, testNetwork(), nil)
	if err != nil {
		t.Fatalf("Compile error = %v", err)
	}

	want := "Environment=DENIED_SUFFIXES=evil.example.com,.tracker.net"
	if !strings.Contains(artifacts.UserData, want) {
		t.Errorf("UserData should contain %q", want)
	}
	// deniedHosts was not set, so the HTTP proxy must stay untouched.
	if strings.Contains(artifacts.UserData, "Environment=DENIED_HOSTS=") {
		t.Error("UserData must not contain DENIED_HOSTS when only deniedDNSSuffixes is set")
	}
}

func TestUserData_DeniedHosts_EmitsProxyEnv(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	p.Spec.Network.Egress.DeniedHosts = []string{"evil.example.com"}

	artifacts, err := compiler.Compile(p, "sb-denyhost", false, testNetwork(), nil)
	if err != nil {
		t.Fatalf("Compile error = %v", err)
	}

	want := "Environment=DENIED_HOSTS=evil.example.com"
	if !strings.Contains(artifacts.UserData, want) {
		t.Errorf("UserData should contain %q", want)
	}
	if strings.Contains(artifacts.UserData, "Environment=DENIED_SUFFIXES=") {
		t.Error("UserData must not contain DENIED_SUFFIXES when only deniedHosts is set")
	}
}

// The Docker substrate runs the same two proxy binaries, so it has to carry the
// deny lists too — otherwise `km create --docker` would silently ignore them.
func TestDockerCompose_DenyLists(t *testing.T) {
	p := loadTestProfile(t, "docker-basic.yaml")
	p.Spec.Network.Egress.DeniedDNSSuffixes = []string{"evil.example.com"}
	p.Spec.Network.Egress.DeniedHosts = []string{"bad.example.net"}

	artifacts, err := compiler.Compile(p, "sb-denydock", false, testNetwork(), nil)
	if err != nil {
		t.Fatalf("Compile error = %v", err)
	}

	if !strings.Contains(artifacts.DockerComposeYAML, `DENIED_SUFFIXES: "evil.example.com"`) {
		t.Error("compose should set DENIED_SUFFIXES on the dns-proxy service")
	}
	if !strings.Contains(artifacts.DockerComposeYAML, `DENIED_HOSTS: "bad.example.net"`) {
		t.Error("compose should set DENIED_HOSTS on the http-proxy service")
	}
}

func TestDockerCompose_NoDenyLists_EmitsNothing(t *testing.T) {
	p := loadTestProfile(t, "docker-basic.yaml")

	artifacts, err := compiler.Compile(p, "sb-denydock0", false, testNetwork(), nil)
	if err != nil {
		t.Fatalf("Compile error = %v", err)
	}

	for _, token := range []string{"DENIED_SUFFIXES", "DENIED_HOSTS"} {
		if strings.Contains(artifacts.DockerComposeYAML, token) {
			t.Errorf("compose must not contain %q when the profile declares no denies", token)
		}
	}
}

// The ECS substrate is not exercised in practice (see CLAUDE.md), but leaving
// it unwired would mean a profile's denies silently evaporate there rather than
// being enforced or rejected. Wiring it costs two template lines.
func TestECSServiceHCL_DenyLists(t *testing.T) {
	p := loadTestProfile(t, "ecs-basic.yaml")
	p.Spec.Network.Egress.DeniedDNSSuffixes = []string{"evil.example.com"}
	p.Spec.Network.Egress.DeniedHosts = []string{"bad.example.net"}

	artifacts, err := compiler.Compile(p, "sb-denyecs", false, testNetwork(), nil)
	if err != nil {
		t.Fatalf("Compile error = %v", err)
	}

	if !strings.Contains(artifacts.ServiceHCL, `{ name = "DENIED_SUFFIXES", value = "evil.example.com" }`) {
		t.Error("ECS task definition should set DENIED_SUFFIXES on the dns-proxy container")
	}
	if !strings.Contains(artifacts.ServiceHCL, `{ name = "DENIED_HOSTS",              value = "bad.example.net" }`) {
		t.Error("ECS task definition should set DENIED_HOSTS on the http-proxy container")
	}
}

func TestECSServiceHCL_NoDenyLists_EmitsNothing(t *testing.T) {
	p := loadTestProfile(t, "ecs-basic.yaml")

	artifacts, err := compiler.Compile(p, "sb-denyecs0", false, testNetwork(), nil)
	if err != nil {
		t.Fatalf("Compile error = %v", err)
	}

	for _, token := range []string{"DENIED_SUFFIXES", "DENIED_HOSTS"} {
		if strings.Contains(artifacts.ServiceHCL, token) {
			t.Errorf("ServiceHCL must not contain %q when the profile declares no denies", token)
		}
	}
}

// In eBPF mode the resolver, not the DNS proxy, is what refuses to populate the
// BPF trie — so the denies have to reach `km ebpf-attach` too, or an eBPF-mode
// sandbox would enforce the allowlist but silently ignore every deny.
func TestUserData_DenyListsReachEBPFAttach(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	p.Spec.Network.Enforcement = "ebpf"
	p.Spec.Network.Egress.DeniedDNSSuffixes = []string{"evil.example.com"}
	p.Spec.Network.Egress.DeniedHosts = []string{"bad.example.net"}

	artifacts, err := compiler.Compile(p, "sb-denyebpf", false, testNetwork(), nil)
	if err != nil {
		t.Fatalf("Compile error = %v", err)
	}

	if !strings.Contains(artifacts.UserData, `--denied-dns "evil.example.com"`) {
		t.Error("UserData should pass --denied-dns to km ebpf-attach")
	}
	if !strings.Contains(artifacts.UserData, `--denied-hosts "bad.example.net"`) {
		t.Error("UserData should pass --denied-hosts to km ebpf-attach")
	}
}
