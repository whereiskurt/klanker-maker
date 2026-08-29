package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func writeExecStore(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/execs.jsonl", []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunExecs_ListsArgvAndMarksFailures(t *testing.T) {
	dir := t.TempDir()
	writeExecStore(t, dir, strings.Join([]string{
		`{"ts":"2026-08-29T12:00:00Z","kind":"exec","pid":100,"uid":1000,"comm":"curl","args":["curl","https://api.github.com"],"ret":0}`,
		`{"ts":"2026-08-29T12:00:01Z","kind":"exec","pid":101,"uid":0,"comm":"nmap","args":["nmap","-sS","10.0.0.1"],"ret":-2}`,
		`{"ts":"2026-08-29T12:00:02Z","kind":"exit","pid":100}`,
	}, "\n")+"\n")

	var out, errb bytes.Buffer
	if err := runExecs(opts{execDir: dir, stdout: &out, stderr: &errb}, nil); err != nil {
		t.Fatalf("runExecs: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "curl https://api.github.com") {
		t.Errorf("argv not rendered: %s", s)
	}
	// Exit records are bookkeeping for the join, not commands anybody ran.
	if strings.Contains(s, `"kind":"exit"`) || strings.Contains(s, "exit ") {
		t.Errorf("exit records leaked into the command listing: %s", s)
	}
	// A failed exec must be visibly different from a successful one; it is the
	// more interesting of the two.
	if !strings.Contains(s, "nmap") || !strings.Contains(s, "FAILED") {
		t.Errorf("failed exec not marked: %s", s)
	}
}

func TestRunExecs_FailedFilterShowsOnlyFailures(t *testing.T) {
	dir := t.TempDir()
	writeExecStore(t, dir, strings.Join([]string{
		`{"ts":"2026-08-29T12:00:00Z","kind":"exec","pid":100,"uid":1000,"comm":"curl","args":["curl"],"ret":0}`,
		`{"ts":"2026-08-29T12:00:01Z","kind":"exec","pid":101,"uid":0,"comm":"nmap","args":["nmap"],"ret":-2}`,
	}, "\n")+"\n")

	var out, errb bytes.Buffer
	if err := runExecs(opts{execDir: dir, stdout: &out, stderr: &errb}, []string{"--failed"}); err != nil {
		t.Fatalf("runExecs: %v", err)
	}
	if strings.Contains(out.String(), "curl") {
		t.Errorf("--failed showed a successful exec: %s", out.String())
	}
	if !strings.Contains(out.String(), "nmap") {
		t.Errorf("--failed hid the failure: %s", out.String())
	}
}

func TestRunExecs_EmptyStoreSaysSo(t *testing.T) {
	var out, errb bytes.Buffer
	if err := runExecs(opts{execDir: t.TempDir(), stdout: &out, stderr: &errb}, nil); err != nil {
		t.Fatalf("runExecs: %v", err)
	}
	if !strings.Contains(out.String(), "(none)") {
		t.Errorf("want an explicit empty marker, got %q", out.String())
	}
}

func TestRunWho_ExplainsAnUnattributableFlowInsteadOfPrintingNothing(t *testing.T) {
	// Under proxy enforcement flows carry no pid. `who` must say why rather
	// than printing (none), which reads as "nothing reached that host".
	execDir := t.TempDir()
	writeExecStore(t, execDir, `{"ts":"2026-08-29T12:00:00Z","kind":"exec","pid":100,"uid":1000,"comm":"curl","args":["curl"],"ret":0}`+"\n")

	flowDir := t.TempDir()
	if err := os.WriteFile(flowDir+"/flows.http.jsonl",
		[]byte(`{"ts":"2026-08-29T12:00:05Z","src":"http","verdict":"allow","host":"api.github.com"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if err := runWho(opts{execDir: execDir, flowDir: flowDir, stdout: &out, stderr: &errb}, []string{"api.github.com"}); err != nil {
		t.Fatalf("runWho: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "api.github.com") {
		t.Errorf("the flow itself must still be listed: %s", s)
	}
	if !strings.Contains(s, "no pid") || !strings.Contains(s, "ebpf") {
		t.Errorf("want an explanation naming the enforcement requirement, got: %s", s)
	}
}

func TestRunWho_NamesTheProcess(t *testing.T) {
	execDir := t.TempDir()
	writeExecStore(t, execDir, `{"ts":"2026-08-29T12:00:00Z","kind":"exec","pid":100,"uid":1000,"comm":"curl","args":["curl","https://api.github.com"],"ret":0}`+"\n")

	flowDir := t.TempDir()
	if err := os.WriteFile(flowDir+"/flows.ebpf.jsonl",
		[]byte(`{"ts":"2026-08-29T12:00:05Z","src":"ebpf","verdict":"allow","host":"api.github.com","pid":100}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if err := runWho(opts{execDir: execDir, flowDir: flowDir, stdout: &out, stderr: &errb}, []string{"api.github.com"}); err != nil {
		t.Fatalf("runWho: %v", err)
	}
	if !strings.Contains(out.String(), "curl https://api.github.com") {
		t.Errorf("want the responsible command named: %s", out.String())
	}
}

func TestRunWho_RequiresAHost(t *testing.T) {
	var out, errb bytes.Buffer
	if err := runWho(opts{stdout: &out, stderr: &errb}, nil); err == nil {
		t.Fatal("want an error when no host is given")
	}
}
