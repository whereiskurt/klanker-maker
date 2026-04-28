package compiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// extractNotifyHookScript renders user-data for a baseline profile, locates
// the km-notify-hook heredoc by its open/close delimiters, and returns the
// script body as a string (WITHOUT the surrounding heredoc delimiter lines).
// Also substitutes "/opt/km/bin/km-send" → "km-send" so tests can resolve
// the stub via PATH override (tests cannot write to /opt without root).
func extractNotifyHookScript(t *testing.T) string {
	t.Helper()
	p := baseProfile()
	ud, err := generateUserData(p, "sb-test", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	const open = "cat > /opt/km/bin/km-notify-hook << 'KM_NOTIFY_HOOK_EOF'\n"
	const close = "\nKM_NOTIFY_HOOK_EOF\n"

	i := strings.Index(ud, open)
	if i < 0 {
		t.Fatalf("could not find hook script open delimiter in user-data")
	}
	rest := ud[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		t.Fatalf("could not find hook script close delimiter in user-data")
	}
	body := rest[:j]

	// Substitute absolute path with PATH-resolved name so tests can inject
	// a stub via PATH prepend. The hook calls /opt/km/bin/km-send; on test
	// machines that path does not exist. Replace all occurrences.
	body = strings.ReplaceAll(body, "/opt/km/bin/km-send", "km-send")

	return body
}

// setupHookEnv writes the hook script to a temp file (chmod +x), copies the
// stub km-send into a bin subdirectory prepended to PATH, and configures
// KM_NOTIFY_TEST_LOG and KM_NOTIFY_LAST_FILE for isolation.
// Returns hookPath, logPath, and tmpdir.
func setupHookEnv(t *testing.T) (hookPath, logPath, tmpdir string) {
	t.Helper()
	tmpdir = t.TempDir()

	// 1. Write hook script.
	body := extractNotifyHookScript(t)
	hookPath = filepath.Join(tmpdir, "km-notify-hook")
	if err := os.WriteFile(hookPath, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	// 2. Copy stub km-send into a "bin" dir we prepend to PATH.
	binDir := filepath.Join(tmpdir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stubSrc := filepath.Join("testdata", "notify-hook-stub-km-send.sh")
	stubBytes, err := os.ReadFile(stubSrc)
	if err != nil {
		t.Fatalf("read stub km-send: %v", err)
	}
	stubDest := filepath.Join(binDir, "km-send")
	if err := os.WriteFile(stubDest, stubBytes, 0o755); err != nil {
		t.Fatal(err)
	}

	// 3. Set per-test env vars via t.Setenv (auto-restored after test).
	logPath = filepath.Join(tmpdir, "calls.log")
	t.Setenv("KM_NOTIFY_TEST_LOG", logPath)
	t.Setenv("KM_NOTIFY_LAST_FILE", filepath.Join(tmpdir, "km-notify.last"))
	// Prepend stub bin dir to PATH.
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return hookPath, logPath, tmpdir
}

// runHook invokes the hook script with bash, the given event argument, extra
// env vars (layered on top of the already-modified process env), and stdin
// payload. Returns exit code, stdout, stderr.
func runHook(t *testing.T, hookPath, event string, extraEnv map[string]string, stdin string) (int, string, string) {
	t.Helper()
	cmd := exec.Command("bash", hookPath, event)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	// Inherit current env (which already has our PATH/LOG overrides from t.Setenv).
	cmd.Env = os.Environ()
	for k, v := range extraEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	runErr := cmd.Run()
	code := 0
	if exitErr, ok := runErr.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if runErr != nil {
		t.Fatalf("runHook unexpected error: %v\nstderr: %s", runErr, errBuf.String())
	}
	return code, out.String(), errBuf.String()
}

// loadFixture reads a testdata fixture file and substitutes __TRANSCRIPT_PATH__
// with the provided transcriptPath value.
func loadFixture(t *testing.T, name, transcriptPath string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("loadFixture %s: %v", name, err)
	}
	return strings.ReplaceAll(string(data), "__TRANSCRIPT_PATH__", transcriptPath)
}

// writeTranscript copies the JSONL fixture into tmpdir and returns its path.
func writeTranscript(t *testing.T, tmpdir string) string {
	t.Helper()
	src := filepath.Join("testdata", "notify-hook-fixture-transcript.jsonl")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("writeTranscript: %v", err)
	}
	dst := filepath.Join(tmpdir, "transcript.jsonl")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

// stubCall holds one parsed invocation record from the stub km-send log.
type stubCall struct {
	args        string   // raw args line
	argList     []string // individual arg lines
	bodyFile    string   // value after "body_file: "
	bodyContent string   // text between body_contents_begin and body_contents_end
}

// readStubLog parses the stub's invocation log into one stubCall per call.
// Returns nil/empty slice if the log file does not exist.
func readStubLog(t *testing.T, logPath string) []stubCall {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("readStubLog: %v", err)
	}

	var calls []stubCall
	text := string(data)
	const startMark = "=== km-send call ==="
	const endMark = "=== end ==="

	for {
		si := strings.Index(text, startMark)
		if si < 0 {
			break
		}
		ei := strings.Index(text[si:], endMark)
		if ei < 0 {
			break
		}
		block := text[si+len(startMark) : si+ei]
		text = text[si+ei+len(endMark):]

		var c stubCall
		lines := strings.Split(block, "\n")
		inBody := false
		var bodyLines []string
		for _, line := range lines {
			switch {
			case strings.HasPrefix(line, "args: "):
				c.args = strings.TrimPrefix(line, "args: ")
			case strings.HasPrefix(line, "  arg: "):
				c.argList = append(c.argList, strings.TrimPrefix(line, "  arg: "))
			case strings.HasPrefix(line, "body_file: "):
				c.bodyFile = strings.TrimPrefix(line, "body_file: ")
			case line == "body_contents_begin":
				inBody = true
			case line == "body_contents_end":
				inBody = false
			case inBody:
				bodyLines = append(bodyLines, line)
			}
		}
		c.bodyContent = strings.Join(bodyLines, "\n")
		calls = append(calls, c)
	}
	return calls
}

// argListContains returns true if any element in argList equals target.
func argListContains(argList []string, target string) bool {
	for _, a := range argList {
		if a == target {
			return true
		}
	}
	return false
}

// ============================================================
// HOOK-05a: Gate off — no km-send call when KM_NOTIFY_ON_PERMISSION=0
// ============================================================

func TestNotifyHook_GatedOff(t *testing.T) {
	hookPath, logPath, tmpdir := setupHookEnv(t)
	transcript := writeTranscript(t, tmpdir)
	payload := loadFixture(t, "notify-hook-fixture-notification.json", transcript)

	code, _, stderr := runHook(t, hookPath, "Notification", map[string]string{
		"KM_NOTIFY_ON_PERMISSION": "0",
		"KM_SANDBOX_ID":           "sb-test",
	}, payload)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr: %s", code, stderr)
	}

	// Stub log should be absent or empty — no km-send invocation.
	info, statErr := os.Stat(logPath)
	if statErr == nil && info.Size() > 0 {
		b, _ := os.ReadFile(logPath)
		t.Errorf("expected zero km-send invocations but log contains:\n%s", string(b))
	}
}

// ============================================================
// HOOK-05b: Notification — km-send called with correct subject + body
// ============================================================

func TestNotifyHook_Notification(t *testing.T) {
	hookPath, logPath, tmpdir := setupHookEnv(t)
	transcript := writeTranscript(t, tmpdir)
	payload := loadFixture(t, "notify-hook-fixture-notification.json", transcript)

	code, _, stderr := runHook(t, hookPath, "Notification", map[string]string{
		"KM_NOTIFY_ON_PERMISSION": "1",
		"KM_SANDBOX_ID":           "sb-test",
	}, payload)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr: %s", code, stderr)
	}

	calls := readStubLog(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("expected 1 km-send invocation, got %d", len(calls))
	}
	c := calls[0]

	// Subject must contain the sandbox ID and "needs permission".
	if !argListContains(c.argList, "--subject") {
		t.Error("km-send called without --subject flag")
	}
	wantSubject := "[sb-test] needs permission"
	if !argListContains(c.argList, wantSubject) {
		t.Errorf("subject not found in args; argList=%v", c.argList)
	}

	// Body must contain the message text from the fixture.
	if !strings.Contains(c.bodyContent, "Claude needs your permission to use Bash") {
		t.Errorf("body missing permission message; body=%q", c.bodyContent)
	}
	// Body footer must include attach and results commands.
	if !strings.Contains(c.bodyContent, "km agent attach sb-test") {
		t.Errorf("body missing 'km agent attach sb-test'; body=%q", c.bodyContent)
	}
	if !strings.Contains(c.bodyContent, "km agent results sb-test") {
		t.Errorf("body missing 'km agent results sb-test'; body=%q", c.bodyContent)
	}
}

// ============================================================
// HOOK-05 recipient override: --to passed when KM_NOTIFY_EMAIL is set
// ============================================================

func TestNotifyHook_Notification_RecipientOverride(t *testing.T) {
	hookPath, logPath, tmpdir := setupHookEnv(t)
	transcript := writeTranscript(t, tmpdir)
	payload := loadFixture(t, "notify-hook-fixture-notification.json", transcript)

	code, _, stderr := runHook(t, hookPath, "Notification", map[string]string{
		"KM_NOTIFY_ON_PERMISSION": "1",
		"KM_SANDBOX_ID":           "sb-test",
		"KM_NOTIFY_EMAIL":         "team@example.com",
	}, payload)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr: %s", code, stderr)
	}

	calls := readStubLog(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("expected 1 km-send invocation, got %d", len(calls))
	}
	c := calls[0]

	// --to team@example.com must appear in the arg list.
	if !argListContains(c.argList, "--to") {
		t.Error("km-send called without --to flag when KM_NOTIFY_EMAIL is set")
	}
	if !argListContains(c.argList, "team@example.com") {
		t.Errorf("--to recipient not found in args; argList=%v", c.argList)
	}
}

// ============================================================
// HOOK-05c: Stop event — body contains LAST assistant text from transcript
// ============================================================

func TestNotifyHook_Stop(t *testing.T) {
	hookPath, logPath, tmpdir := setupHookEnv(t)
	transcript := writeTranscript(t, tmpdir)
	payload := loadFixture(t, "notify-hook-fixture-stop.json", transcript)

	code, _, stderr := runHook(t, hookPath, "Stop", map[string]string{
		"KM_NOTIFY_ON_IDLE": "1",
		"KM_SANDBOX_ID":     "sb-test",
	}, payload)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr: %s", code, stderr)
	}

	calls := readStubLog(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("expected 1 km-send invocation, got %d", len(calls))
	}
	c := calls[0]

	// Subject must be idle.
	wantSubject := "[sb-test] idle"
	if !argListContains(c.argList, wantSubject) {
		t.Errorf("subject not found in args; argList=%v", c.argList)
	}

	// Body must contain the LAST assistant text (not the first "Starting refactor...").
	wantLast := "I've finished refactoring the auth middleware. Let me know what you'd like to test next."
	if !strings.Contains(c.bodyContent, wantLast) {
		t.Errorf("body missing last assistant text; body=%q", c.bodyContent)
	}
	// Confirm the first (earlier) assistant text is NOT the one selected.
	wantFirst := "Starting refactor..."
	if strings.Contains(c.bodyContent, wantFirst) && !strings.Contains(c.bodyContent, wantLast) {
		t.Errorf("body has first assistant text instead of last; body=%q", c.bodyContent)
	}
}

// ============================================================
// HOOK-05d: Cooldown — second call within window suppressed
// ============================================================

func TestNotifyHook_Cooldown(t *testing.T) {
	hookPath, logPath, tmpdir := setupHookEnv(t)
	transcript := writeTranscript(t, tmpdir)
	payload := loadFixture(t, "notify-hook-fixture-notification.json", transcript)

	extraEnv := map[string]string{
		"KM_NOTIFY_ON_PERMISSION":   "1",
		"KM_SANDBOX_ID":             "sb-test",
		"KM_NOTIFY_COOLDOWN_SECONDS": "10",
	}

	// First invocation — should call km-send.
	code, _, stderr := runHook(t, hookPath, "Notification", extraEnv, payload)
	if code != 0 {
		t.Errorf("first call: exit code = %d, want 0; stderr: %s", code, stderr)
	}

	calls := readStubLog(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("first call: expected 1 km-send invocation, got %d", len(calls))
	}

	// Second invocation within cooldown window — must NOT call km-send.
	code, _, stderr = runHook(t, hookPath, "Notification", extraEnv, payload)
	if code != 0 {
		t.Errorf("second call: exit code = %d, want 0; stderr: %s", code, stderr)
	}

	calls = readStubLog(t, logPath)
	if len(calls) != 1 {
		t.Errorf("second call within cooldown: expected still 1 km-send invocation, got %d", len(calls))
	}
}

// ============================================================
// HOOK-05e: km-send failure — hook still exits 0
// ============================================================

func TestNotifyHook_SendFailure_StillExitsZero(t *testing.T) {
	hookPath, logPath, tmpdir := setupHookEnv(t)
	transcript := writeTranscript(t, tmpdir)
	payload := loadFixture(t, "notify-hook-fixture-notification.json", transcript)

	code, _, stderr := runHook(t, hookPath, "Notification", map[string]string{
		"KM_NOTIFY_ON_PERMISSION": "1",
		"KM_SANDBOX_ID":           "sb-test",
		"KM_NOTIFY_TEST_FAIL":     "1", // stub exits 1
	}, payload)
	if code != 0 {
		t.Errorf("exit code = %d, want 0 even when km-send fails; stderr: %s", code, stderr)
	}

	// Stub was still invoked (it logs before exiting 1).
	calls := readStubLog(t, logPath)
	if len(calls) != 1 {
		t.Errorf("expected 1 km-send invocation (stub records before failing), got %d", len(calls))
	}
}

// ============================================================
// HOOK-05 invariant: --body <file>, NOT stdin
// ============================================================

func TestNotifyHook_BodyViaFile_NotStdin(t *testing.T) {
	hookPath, logPath, tmpdir := setupHookEnv(t)
	transcript := writeTranscript(t, tmpdir)
	payload := loadFixture(t, "notify-hook-fixture-notification.json", transcript)

	code, _, stderr := runHook(t, hookPath, "Notification", map[string]string{
		"KM_NOTIFY_ON_PERMISSION": "1",
		"KM_SANDBOX_ID":           "sb-test",
	}, payload)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr: %s", code, stderr)
	}

	calls := readStubLog(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("expected 1 km-send invocation, got %d", len(calls))
	}
	c := calls[0]

	// --body must be present and reference a file path (not "(none or missing)").
	if !argListContains(c.argList, "--body") {
		t.Error("km-send called without --body flag — hook must use --body <file> per CLAUDE.md")
	}
	if c.bodyFile == "" || c.bodyFile == "(none or missing)" {
		t.Errorf("body_file in stub log is absent or missing: %q", c.bodyFile)
	}
	// Body file path must look like /tmp/km-notify-body.XXXXXX.
	if !strings.Contains(c.bodyFile, "km-notify-body.") {
		t.Errorf("body_file path %q does not match /tmp/km-notify-body.* pattern", c.bodyFile)
	}
	// The body content must be non-empty (file was written before send).
	if strings.TrimSpace(c.bodyContent) == "" {
		t.Error("body content is empty — hook must write body to file before calling km-send")
	}
}
