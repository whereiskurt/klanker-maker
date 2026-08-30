package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestProfileGen_EmitsYAMLFromCensus(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{flowDir: seedFlows(t), stdout: &out, stderr: &errb}

	if code := runProfileGen(o); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "apiVersion:") || !strings.Contains(got, "SandboxProfile") {
		t.Fatalf("want a SandboxProfile document:\n%s", got)
	}
	if !strings.Contains(got, "github.com") {
		t.Errorf("want the observed host reflected in the profile:\n%s", got)
	}
	if strings.Contains(got, "evil.example.com") {
		t.Errorf("a denied host must never reach the generated allowlist:\n%s", got)
	}
}
