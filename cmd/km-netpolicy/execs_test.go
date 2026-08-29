package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
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

func TestRunExecs_PermissionErrorNamesRootShellRatherThanReportingEmpty(t *testing.T) {
	// A sandbox user running `km-netpolicy execs` without --root would
	// previously see (none), which reads as "the box did nothing" rather
	// than "you can't read this". The error must be surfaced and must name
	// the fix.
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the permission bits this test relies on")
	}
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/execs.jsonl", []byte(`{"ts":"2026-08-29T12:00:00Z","kind":"exec","pid":1,"uid":0,"comm":"ok","ret":0}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	var out, errb bytes.Buffer
	err := runExecs(opts{execDir: dir, stdout: &out, stderr: &errb}, nil)
	if err == nil {
		t.Fatal("want an error, not a silent (none)")
	}
	if strings.Contains(out.String(), "(none)") {
		t.Errorf("must not report an empty trace on a permission error: %s", out.String())
	}
	if !strings.Contains(errb.String(), "km shell --root") {
		t.Errorf("want the fix named on stderr, got %q", errb.String())
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

func TestRunExecs_JSONOnEmptyResultEmitsNoNonJSONLine(t *testing.T) {
	dir := t.TempDir()
	writeExecStore(t, dir, `{"ts":"2026-08-29T12:00:00Z","kind":"exec","pid":100,"uid":1000,"comm":"curl","args":["curl"],"ret":0}`+"\n")

	var out, errb bytes.Buffer
	// --failed with no failures present means zero matching records, but --json
	// means a consumer parses stdout as a JSONL stream — a literal "(none)"
	// line would corrupt it exactly like the rotation warning would if it were
	// ever routed to stdout instead of stderr.
	if err := runExecs(opts{execDir: dir, stdout: &out, stderr: &errb}, []string{"--json", "--failed"}); err != nil {
		t.Fatalf("runExecs: %v", err)
	}
	if strings.Contains(out.String(), "(none)") {
		t.Errorf("--json leaked a non-JSON empty marker onto stdout: %q", out.String())
	}
	if out.String() != "" {
		t.Errorf("want no stdout output for an empty --json result, got %q", out.String())
	}
}

func TestRunExecs_NegativeUIDIsRejected(t *testing.T) {
	dir := t.TempDir()
	writeExecStore(t, dir, `{"ts":"2026-08-29T12:00:00Z","kind":"exec","pid":100,"uid":1000,"comm":"curl","args":["curl"],"ret":0}`+"\n")

	var out, errb bytes.Buffer
	// A negative --uid must be rejected rather than silently disabling the
	// filter (uid>=0 is the sentinel for "unset") — a silently-ignored filter
	// on a forensic trace is worse than an error: the operator would believe
	// they'd seen only the filtered set when they'd actually seen everything.
	if err := runExecs(opts{execDir: dir, stdout: &out, stderr: &errb}, []string{"--uid", "-1"}); err == nil {
		t.Fatal("want an error for a negative --uid")
	}
	if strings.Contains(out.String(), "curl") {
		t.Errorf("a rejected --uid must not fall through to an unfiltered listing: %s", out.String())
	}
}

func TestCmdline_FlagsArgv0DisagreeingWithComm(t *testing.T) {
	// execve("/usr/bin/curl", []string{"bash", ...}) renders argv[0] as
	// "bash" while the kernel's own comm still says "curl" — a forensics
	// listing that only ever showed argv would let the process lie about
	// what it is.
	r := execlog.Record{Comm: "curl", Args: []string{"bash", "-c", "curl https://evil.example"}}
	got := cmdline(r)
	if !strings.Contains(got, "bash -c curl https://evil.example") {
		t.Errorf("want argv rendered as given, got %q", got)
	}
	if !strings.Contains(got, "comm=curl") {
		t.Errorf("want the disagreeing comm surfaced, got %q", got)
	}
}

func TestCmdline_DoesNotFlagAgreeingArgv0(t *testing.T) {
	r := execlog.Record{Comm: "curl", Args: []string{"/usr/bin/curl", "https://api.github.com"}}
	got := cmdline(r)
	if strings.Contains(got, "comm=") {
		t.Errorf("argv0's basename matches comm; must not annotate: %q", got)
	}
}

func TestCmdline_DoesNotFlagTruncationOnlyMismatch(t *testing.T) {
	// TASK_COMM_LEN truncates comm to 15 usable bytes (exec.c), so a long
	// argv[0] basename that merely got cut short is not a lie and must not
	// be flagged as one.
	r := execlog.Record{Comm: "python3.11-long", Args: []string{"/usr/bin/python3.11-longer-name", "script.py"}}
	got := cmdline(r)
	if strings.Contains(got, "comm=") {
		t.Errorf("a truncation-only mismatch must not be flagged: %q", got)
	}
}

func TestRunWho_RejectsUnexpectedTrailingArgument(t *testing.T) {
	execDir := t.TempDir()
	writeExecStore(t, execDir, `{"ts":"2026-08-29T12:00:00Z","kind":"exec","pid":100,"uid":1000,"comm":"curl","args":["curl"],"ret":0}`+"\n")
	flowDir := t.TempDir()

	var out, errb bytes.Buffer
	// A trailing token (e.g. an accidental flag) must error rather than be
	// silently dropped, matching runFlows/runPin's stance on unrecognised
	// tokens.
	if err := runWho(opts{execDir: execDir, flowDir: flowDir, stdout: &out, stderr: &errb}, []string{"api.github.com", "--json"}); err == nil {
		t.Fatal("want an error for an unexpected trailing argument")
	}
}
