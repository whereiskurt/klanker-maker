package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The profile-baked denies live in the two proxy systemd units' Environment
// blocks, which a sandbox shell never sees. Without a shared env file,
// `km-netpolicy list` reports "(none)" for profile-baked denies on a box that
// actually has them — actively misleading, because it reads as "nothing is
// blocked at create time".
func TestBuildOpts_ReadsEnvFileWhenEnvVarsAbsent(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "netpolicy.env")
	body := "DENIED_SUFFIXES=evil.example.com,.tracker.example.net\n" +
		"DENIED_HOSTS=evil.example.com\n" +
		"KM_NETPOLICY_FILE=/var/lib/km/netpolicy/deny.list\n"
	if err := os.WriteFile(envFile, []byte(body), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	o := buildOpts(func(string) string { return "" }, envFile)

	if len(o.staticDNS) != 2 || o.staticDNS[0] != "evil.example.com" {
		t.Errorf("staticDNS = %v, want the two suffixes from the env file", o.staticDNS)
	}
	if len(o.staticHosts) != 1 || o.staticHosts[0] != "evil.example.com" {
		t.Errorf("staticHosts = %v, want the host from the env file", o.staticHosts)
	}
	if o.denyFile != "/var/lib/km/netpolicy/deny.list" {
		t.Errorf("denyFile = %q, want the path from the env file", o.denyFile)
	}
}

// A real environment variable must win over the file, so an operator can
// override for a one-off invocation.
func TestBuildOpts_EnvVarBeatsEnvFile(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "netpolicy.env")
	if err := os.WriteFile(envFile, []byte("DENIED_SUFFIXES=from-file.example.com\n"), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	getenv := func(k string) string {
		if k == "DENIED_SUFFIXES" {
			return "from-env.example.com"
		}
		return ""
	}

	o := buildOpts(getenv, envFile)
	if len(o.staticDNS) != 1 || o.staticDNS[0] != "from-env.example.com" {
		t.Errorf("staticDNS = %v, want the env var to win", o.staticDNS)
	}
}

func TestBuildOpts_MissingEnvFileIsNotAnError(t *testing.T) {
	o := buildOpts(func(string) string { return "" }, filepath.Join(t.TempDir(), "absent.env"))

	if o.denyFile == "" {
		t.Error("denyFile should fall back to the compiled-in default")
	}
	if len(o.staticDNS) != 0 || len(o.staticHosts) != 0 {
		t.Errorf("expected no static denies, got %v / %v", o.staticDNS, o.staticHosts)
	}
}

func TestParseEnvFile_IgnoresCommentsAndBlanks(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "netpolicy.env")
	body := "# a comment\n\nDENIED_HOSTS=a.example.com\n   \nNOT_A_PAIR\n"
	if err := os.WriteFile(envFile, []byte(body), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	got := parseEnvFile(envFile)
	if got["DENIED_HOSTS"] != "a.example.com" {
		t.Errorf("DENIED_HOSTS = %q, want a.example.com", got["DENIED_HOSTS"])
	}
	if len(got) != 1 {
		t.Errorf("expected exactly one pair, got %v", got)
	}
}

// Values may be quoted the way systemd-style env files often are.
func TestParseEnvFile_StripsSurroundingQuotes(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "netpolicy.env")
	if err := os.WriteFile(envFile, []byte("DENIED_HOSTS=\"a.example.com,b.example.com\"\n"), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	if got := parseEnvFile(envFile)["DENIED_HOSTS"]; got != "a.example.com,b.example.com" {
		t.Errorf("quoted value = %q, want it unquoted", got)
	}
}
