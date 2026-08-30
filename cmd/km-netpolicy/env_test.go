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

// AWS-RunShellScript (the SSM document `execs save` runs under) invokes a
// bare, non-login shell — no /etc/profile.d, so no ambient AWS_REGION the way
// a root login would get. The region must resolve from opts (env var or the
// boot-written env file), the same fallback chain as every other field here.
func TestBuildOpts_ResolvesRegionFromEnvFileWhenEnvVarAbsent(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "netpolicy.env")
	if err := os.WriteFile(envFile, []byte("AWS_REGION=us-west-2\n"), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	// The ambient environment carries no AWS_REGION at all — the exact shape
	// of an SSM AWS-RunShellScript invocation — so a regression back to
	// os.Getenv or the SDK's own ambient-env lookup would resolve empty here.
	o := buildOpts(func(string) string { return "" }, envFile)

	if o.region != "us-west-2" {
		t.Errorf("region = %q, want us-west-2 from the env file", o.region)
	}
}

// A real AWS_REGION environment variable must win over the file, matching
// every other field's precedence.
func TestBuildOpts_RegionEnvVarBeatsEnvFile(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "netpolicy.env")
	if err := os.WriteFile(envFile, []byte("AWS_REGION=us-west-2\n"), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	getenv := func(k string) string {
		if k == "AWS_REGION" {
			return "eu-west-1"
		}
		return ""
	}

	o := buildOpts(getenv, envFile)
	if o.region != "eu-west-1" {
		t.Errorf("region = %q, want the env var eu-west-1 to win", o.region)
	}
}

// AWS_DEFAULT_REGION is accepted as a fallback name — a box whose
// /etc/profile.d only sets the older name (root login shells set
// AWS_DEFAULT_REGION, not AWS_REGION) must still resolve.
func TestBuildOpts_FallsBackToAWSDefaultRegion(t *testing.T) {
	getenv := func(k string) string {
		if k == "AWS_DEFAULT_REGION" {
			return "ap-southeast-2"
		}
		return ""
	}

	o := buildOpts(getenv, filepath.Join(t.TempDir(), "absent.env"))
	if o.region != "ap-southeast-2" {
		t.Errorf("region = %q, want the AWS_DEFAULT_REGION fallback", o.region)
	}
}

// AWS_REGION must win over AWS_DEFAULT_REGION when both are present.
func TestBuildOpts_AWSRegionBeatsAWSDefaultRegion(t *testing.T) {
	getenv := func(k string) string {
		switch k {
		case "AWS_REGION":
			return "us-east-1"
		case "AWS_DEFAULT_REGION":
			return "ap-southeast-2"
		}
		return ""
	}

	o := buildOpts(getenv, filepath.Join(t.TempDir(), "absent.env"))
	if o.region != "us-east-1" {
		t.Errorf("region = %q, want AWS_REGION to win over AWS_DEFAULT_REGION", o.region)
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
