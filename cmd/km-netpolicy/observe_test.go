package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedFlows(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := `{"ts":"2026-08-28T14:00:00Z","src":"http","verdict":"allow","host":"api.github.com","port":443}` + "\n" +
		`{"ts":"2026-08-28T14:00:01Z","src":"http","verdict":"allow","host":"api.github.com","port":443}` + "\n" +
		`{"ts":"2026-08-28T14:00:02Z","src":"dns","verdict":"deny","host":"evil.example.com"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "flows.http.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestObserved_ListsAllowedAndDeniedSeparately(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{flowDir: seedFlows(t), stdout: &out, stderr: &errb}

	if code := runObserved(o); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errb.String())
	}
	got := out.String()
	if !strings.Contains(got, "api.github.com") {
		t.Errorf("missing allowed host:\n%s", got)
	}
	if !strings.Contains(got, "evil.example.com") {
		t.Errorf("missing denied host:\n%s", got)
	}
	// The denied host must appear under its own heading, never folded into the
	// allowed census — that set is what pin intersects against.
	allowedIdx := strings.Index(got, "allowed")
	deniedIdx := strings.Index(got, "denied")
	if allowedIdx < 0 || deniedIdx < 0 || deniedIdx < allowedIdx {
		t.Errorf("want an allowed section then a denied section:\n%s", got)
	}
}

func TestObserved_EmptyStoreIsNotAnError(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{flowDir: filepath.Join(t.TempDir(), "absent"), stdout: &out, stderr: &errb}

	if code := runObserved(o); code != 0 {
		t.Fatalf("a box that has made no egress decision is not an error: %d", code)
	}
	if !strings.Contains(out.String(), "(none)") {
		t.Errorf("want an explicit empty marker:\n%s", out.String())
	}
}

func TestFlows_DeniedFilter(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{flowDir: seedFlows(t), stdout: &out, stderr: &errb}

	if code := runFlows([]string{"--denied"}, o); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errb.String())
	}
	got := out.String()
	if strings.Contains(got, "api.github.com") {
		t.Errorf("--denied must exclude allowed flows:\n%s", got)
	}
	if !strings.Contains(got, "evil.example.com") {
		t.Errorf("--denied must include denied flows:\n%s", got)
	}
}

func TestFlows_JSONIsOnePerLine(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{flowDir: seedFlows(t), stdout: &out, stderr: &errb}

	if code := runFlows([]string{"--json"}, o); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if !strings.HasPrefix(line, "{") {
			t.Fatalf("every --json line must be an object, got %q", line)
		}
	}
}
