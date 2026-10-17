# Cold-create boot race Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A cold-created sandbox answers its first Slack/GitHub/H1/webhook prompt as soon as the agent is installed, never 30 minutes later.

**Architecture:** Two userdata-only changes. The bootstrap writes `/var/lib/km/init-done` once profile init has run (success, failure, or nothing to run); each inbound poller waits for that marker, bounded, before its first receive and only then checks the agent is on PATH. Separately, a turn that exits 127 (agent binary missing) gets its SQS message made visible again in 60 s instead of the queue's 1800 s.

**Tech Stack:** bash inside the Go template `pkg/compiler/userdata.go`, rendered-bash tests in `pkg/compiler`, the frozen pre-92 golden.

**Spec:** `docs/superpowers/specs/2026-10-17-cold-create-boot-race-design.md`

## Global Constraints

- Marker path is exactly `/var/lib/km/init-done`; the gate's bound is exactly `1200` seconds polled every `5`; the 127 visibility is exactly `60` seconds. Log prefixes keep each poller's existing `[km-<source>-inbound-poller]` / `[km-inbound-poller]` tags.
- The four pollers are the heredocs `SLACKINBOUND`, `GITHUBINBOUND`, `H1INBOUND`, `WEBHOOKINBOUND` in `pkg/compiler/userdata.go`. The mail poller and the tmux `km agent run` path are out of scope.
- `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` is hand-patched with exactly the template hunks by a script asserting each old string matches once per file; `CAPTURE_PRE92_BASELINE=1` is forbidden. The two full-output goldens (`userdata_additional_volume_only.golden.sh`, `h1_byte_identity_golden.txt`) may be regenerated with their own sanctioned capture flags if their tests say so, and the diff must be only these hunks.
- Every implementer runs `go build ./...` and `go test ./pkg/compiler/` (whole package: other tests pin the golden byte-for-byte) before committing; stage by pathspec only; commit trailers:
  `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` and `Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt`.
- Branch `fix/cold-create-boot-race`; never push or merge.

---

### Task 1: The bootstrap writes `/var/lib/km/init-done`

**Files:**
- Modify: `pkg/compiler/userdata.go` (section 7.5 "Profile init", the block that ends `echo "[km-bootstrap] No init script found in S3 (skipped)"\nfi\n{{- end }}`, ~line 5861–5900)
- Modify: `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` (same hunk)
- Test: `pkg/compiler/userdata_init_failed_test.go` (`initBlock`, `runInitBlock`, new cases)

**Interfaces:**
- Produces: the rendered line `mkdir -p /var/lib/km && touch /var/lib/km/init-done` immediately after the 7.5 block's closing `{{- end }}`, rendered for EVERY profile (with or without init). Task 2's gate waits for that path.

- [ ] **Step 1: Failing tests**

In `userdata_init_failed_test.go`:
- Change `initBlock`'s terminator so the extracted block includes the new line: replace the terminator string `"No init script found in S3 (skipped)\"\nfi\n"` with `"No init script found in S3 (skipped)\"\nfi\nmkdir -p /var/lib/km && touch /var/lib/km/init-done\n"` (both places it appears).
- `runInitBlock` already rewrites `/var/lib/km` to the temp dir; add a fifth return value `done bool` = whether `<dir>/init-done` exists after the run, and return it.
- Add:

```go
func TestUserdataInitBlock_MarkerWrittenOnSuccessAndFailure(t *testing.T) {
	p := initProfile()
	out := mustRender(t, p) // use whatever helper the existing tests in this file use to render userdata
	block := initBlock(t, out)
	_, _, _, _, done := runInitBlock(t, block, "#!/bin/bash\nexit 0\n")
	if !done {
		t.Fatal("init-done must be written after a successful init")
	}
	_, _, _, _, done = runInitBlock(t, block, "#!/bin/bash\nprintf 'exit=1\\nstep=1\\ntotal=1\\ncommand=false\\n' > \"$(dirname \"$0\")/init-failed\"\nexit 1\n")
	if !done {
		t.Fatal("init-done must be written after a FAILED init too, or the pollers wait out their bound")
	}
	_, _, _, _, done = runInitBlock(t, block, "") // aws s3 cp fails: no init script in S3
	if !done {
		t.Fatal("init-done must be written when there is no init script")
	}
}

// A profile with no initCommands/initScripts renders no 7.5 block at all; the
// marker line must still render (unconditionally) or such a box's pollers wait
// 1200 s on every cold boot.
func TestUserdata_InitDoneMarkerRendersWithoutInit(t *testing.T) {
	p := baseProfile()
	p.Spec.Execution.InitCommands = nil
	out := mustRender(t, p)
	if strings.Count(out, "touch /var/lib/km/init-done") != 1 {
		t.Fatalf("init-done touch must render exactly once for a no-init profile, got %d", strings.Count(out, "touch /var/lib/km/init-done"))
	}
}
```

Replace `mustRender` with the real render helper used in that file (read the existing `TestUserdataInitBlock_FailureIsReportedNotSwallowed` to find it), and adjust the failure-arm init script to match how the existing failure test writes `init-failed` (the `init-failed` path is the rewritten one; copy the existing test's approach exactly).

- [ ] **Step 2: Run, expect FAIL** — `cd pkg/compiler && go test -run 'InitBlock|InitDoneMarker' .`

- [ ] **Step 3: Implement**

In `userdata.go`, directly after the line `{{- end }}` that closes `{{- if or .InitCommands .InitScripts }}` (the 7.5 block), add:

```bash
# Cold-create boot race (2026-10-17): the inbound pollers start in section 5 and
# must not drain their first message before profile init (which installs the
# agent) has run. Written after 7.5 on EVERY boot path — success, failure, no
# init at all — so a poller never waits out its bound on a healthy box. Lives in
# /var/lib/km so a stop/resume (userdata does not re-run) does not wait again.
mkdir -p /var/lib/km && touch /var/lib/km/init-done
```

Apply the identical hunk to the frozen golden at the same spot (find `No init script found in S3 (skipped)"` then the following `fi`).

- [ ] **Step 4: Run, expect PASS** — `cd pkg/compiler && go test . && cd ../.. && go build ./... && git diff --stat`

If `TestUserdata_AdditionalVolumeOnly_*` or the H1 byte-identity test fail on their goldens, regenerate those two with the capture flag named in their test file's comment and confirm `git diff` on them is only this hunk.

- [ ] **Step 5: Commit**

```bash
git add pkg/compiler/userdata.go pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh pkg/compiler/userdata_init_failed_test.go   # plus the two goldens if regenerated
git commit -m "fix(bootstrap): write /var/lib/km/init-done after profile init on every boot path"
```

---

### Task 2: The four inbound pollers gate their first receive on the marker

**Files:**
- Modify: `pkg/compiler/userdata.go` — in each of the four heredocs, the `command -v "$AGENT" >/dev/null 2>&1 || echo "[km-inbound-poller] WARNING: agent '$AGENT' not on PATH …"` line (Slack ~1988, GitHub ~2809, H1 ~3225, webhook ~3606)
- Modify: frozen golden (same hunks; it contains all four pollers only if the profile enables them — check which heredocs it holds and patch those)
- Test: `pkg/compiler/userdata_init_gate_test.go` (new), `pkg/compiler/userdata_init_gate_guard_test.go` (new)

**Interfaces:**
- Consumes: `/var/lib/km/init-done` (Task 1).
- Produces: a block delimited by `# --- km-init-gate ---` / `# --- end km-init-gate ---` in each poller, placed where the agent check is today and containing the moved agent check as its last line.

- [ ] **Step 1: Failing tests**

`pkg/compiler/userdata_init_gate_test.go`:

```go
package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const initGateStart = "# --- km-init-gate ---"
const initGateEnd = "# --- end km-init-gate ---"

// runInitGate runs the Slack poller's gate snippet under real bash with the
// marker path and the bound rewritten, and returns combined output + elapsed.
func runInitGate(t *testing.T, markerDir string, bound int, agentOnPath bool) (string, time.Duration) {
	t.Helper()
	block := slackPollerBlock(t)
	s := strings.Index(block, initGateStart)
	e := strings.Index(block, initGateEnd)
	if s < 0 || e < 0 || e < s {
		t.Fatal("init gate markers not found in the slack poller")
	}
	snippet := block[s:e]
	if !strings.Contains(snippet, "/var/lib/km/init-done") || !strings.Contains(snippet, "1200") {
		t.Fatalf("gate no longer uses the shared marker path or the 1200 s bound:\n%s", snippet)
	}
	snippet = strings.ReplaceAll(snippet, "/var/lib/km/init-done", filepath.Join(markerDir, "init-done"))
	snippet = strings.ReplaceAll(snippet, "-lt 1200", "-lt "+itoa(bound))
	snippet = strings.ReplaceAll(snippet, "sleep 5", "sleep 1")
	agent := "definitely-not-on-path-xyz"
	if agentOnPath {
		agent = "true"
	}
	script := "set -euo pipefail\nAGENT=" + agent + "\n" + snippet
	start := time.Now()
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("gate failed under bash: %v\n%s", err, out)
	}
	return string(out), time.Since(start)
}

func itoa(n int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + fmtInt(n)) }
func fmtInt(n int) string  { return strconvItoa(n) }
```

(Replace the last two helpers with a plain `strconv.Itoa` import; they are here only to show intent.)

```go
func TestInitGate_MarkerPresent_NoWait(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "init-done"), nil, 0o644)
	out, el := runInitGate(t, dir, 10, true)
	if strings.Contains(out, "waiting for profile init") || el > 2*time.Second {
		t.Fatalf("must not wait when the marker exists: %s (%v)", out, el)
	}
}

func TestInitGate_MarkerAppears_Proceeds(t *testing.T) {
	dir := t.TempDir()
	go func() { time.Sleep(2 * time.Second); os.WriteFile(filepath.Join(dir, "init-done"), nil, 0o644) }()
	out, el := runInitGate(t, dir, 10, true)
	if !strings.Contains(out, "waiting for profile init") || !strings.Contains(out, "profile init done after") || el > 6*time.Second {
		t.Fatalf("must wait then proceed: %s (%v)", out, el)
	}
}

func TestInitGate_Timeout_ProceedsWithWarning(t *testing.T) {
	dir := t.TempDir()
	out, el := runInitGate(t, dir, 3, false)
	if !strings.Contains(out, "marker not seen after") || el < 2*time.Second || el > 8*time.Second {
		t.Fatalf("must give up at the bound with a WARN: %s (%v)", out, el)
	}
	if !strings.Contains(out, "not on PATH") {
		t.Fatalf("the agent check must run AFTER the gate (and report a truly missing agent): %s", out)
	}
}
```

`pkg/compiler/userdata_init_gate_guard_test.go` (name-agnostic over the file, mirroring `pkg/secrets/wiring_guard_test.go`):

```go
package compiler

import (
	"os"
	"strings"
	"testing"
)

// Every inbound poller heredoc (anything that defines dispatch_as_sandbox) must
// carry the init gate before its first receive-message, and the exit-127
// visibility rule after its "agent run failed" line. Name-agnostic: a fifth
// poller added later is covered without an edit.
func TestEveryInboundPollerHasInitGateAnd127Rule(t *testing.T) {
	body, err := os.ReadFile("userdata.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	heredocs := []string{}
	for _, l := range strings.Split(src, "\n") {
		if strings.HasPrefix(l, "cat > /opt/km/bin/km-") && strings.Contains(l, "-inbound-poller << '") {
			heredocs = append(heredocs, l)
		}
	}
	if len(heredocs) < 4 {
		t.Fatalf("expected at least 4 inbound poller heredocs, found %d", len(heredocs))
	}
	for _, h := range heredocs {
		name := h[strings.Index(h, "<< '")+4 : len(h)-1]
		start := strings.Index(src, h)
		end := strings.Index(src[start:], "\n"+name+"\n")
		if end < 0 {
			t.Fatalf("%s: terminator not found", name)
		}
		poller := src[start : start+end]
		gate := strings.Index(poller, "# --- km-init-gate ---")
		recv := strings.Index(poller, "receive-message")
		if gate < 0 || recv < 0 || gate > recv {
			t.Errorf("%s: init gate missing or placed after the first receive-message", name)
		}
		failed := strings.Index(poller, "agent run failed (exit $RUN_EXIT)")
		rule := strings.Index(poller, "--visibility-timeout 60")
		if failed < 0 || rule < 0 || rule < failed {
			t.Errorf("%s: exit-127 visibility rule missing or placed before the failure line", name)
		}
	}
}
```

(The 127 half of the guard fails until Task 3 — that is expected; Task 2 commits with the guard's 127 assertions temporarily guarded by `t.Skip` ONLY if you cannot land Task 3 in the same commit. Preferred: do Tasks 2 and 3 as one commit if the diff stays reviewable; otherwise keep the guard's 127 check commented with a `// Task 3` note and remove the note in Task 3.)

- [ ] **Step 2: Run, expect FAIL** — `cd pkg/compiler && go test -run 'InitGate|EveryInboundPoller' .`

- [ ] **Step 3: Implement**

In each of the four pollers, replace the single `command -v "$AGENT" …` line with:

```bash
# --- km-init-gate ---
# Cold-create boot race: section 5 starts this poller before section 7.5 runs the
# profile's initCommands (which install the agent). Wait for the init marker,
# bounded, before the first receive — then check the agent, so the warning below
# means "this profile has no agent install", not "the race was lost".
if [ ! -e /var/lib/km/init-done ]; then
  echo "[km-inbound-poller] waiting for profile init (/var/lib/km/init-done)"
  _km_init_wait=0
  while [ ! -e /var/lib/km/init-done ] && [ "$_km_init_wait" -lt 1200 ]; do sleep 5; _km_init_wait=$((_km_init_wait+5)); done
  if [ -e /var/lib/km/init-done ]; then
    echo "[km-inbound-poller] profile init done after ${_km_init_wait}s"
  else
    echo "[km-inbound-poller] WARN: profile init marker not seen after ${_km_init_wait}s; proceeding"
  fi
fi
command -v "$AGENT" >/dev/null 2>&1 || echo "[km-inbound-poller] WARNING: agent '$AGENT' not on PATH — dispatched turns will NOT run; add the agent install to the profile initCommands (e.g. npm install -g @anthropic-ai/claude-code)"
# --- end km-init-gate ---
```

Keep each poller's existing warning text verbatim (copy it from that poller; the H1/webhook variants may differ slightly). Also fix the stale comment above the Slack poller's check ("initCommands have already run by now") to say the gate guarantees it. Patch the frozen golden for every poller it contains.

- [ ] **Step 4: Run, expect PASS** — `cd pkg/compiler && go test . && cd ../.. && go build ./...`

- [ ] **Step 5: Commit**

```bash
git add pkg/compiler/userdata.go pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh pkg/compiler/userdata_init_gate_test.go pkg/compiler/userdata_init_gate_guard_test.go
git commit -m "fix(pollers): wait for /var/lib/km/init-done before the first receive; agent check after the gate"
```

---

### Task 3: Exit 127 makes the message visible again in 60 s

**Files:**
- Modify: `pkg/compiler/userdata.go` — the four `WARN: agent run failed (exit $RUN_EXIT), message returns to queue` lines (Slack ~2765, GitHub ~3190, H1 ~3573, webhook ~3769)
- Modify: frozen golden (same hunks)
- Test: `pkg/compiler/userdata_exit127_test.go` (new) + enable the 127 half of the guard from Task 2

- [ ] **Step 1: Failing test**

```go
package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const exit127Start = "# --- km-exit-127 ---"
const exit127End = "# --- end km-exit-127 ---"

// The failure arm, run under bash with a fake aws that records its argv.
func runExit127(t *testing.T, runExit string) []string {
	t.Helper()
	block := slackPollerBlock(t)
	s := strings.Index(block, exit127Start)
	e := strings.Index(block, exit127End)
	if s < 0 || e < 0 {
		t.Fatal("exit-127 markers not found in the slack poller")
	}
	dir := t.TempDir()
	rec := filepath.Join(dir, "calls")
	os.WriteFile(filepath.Join(dir, "aws"), []byte("#!/bin/bash\necho \"$*\" >> "+shellQuote(rec)+"\n"), 0o755)
	script := "set -euo pipefail\nPATH=" + shellQuote(dir) + ":$PATH\nRUN_EXIT=" + runExit + "\nQUEUE_URL=q\nRECEIPT=r\nREGION=us-east-1\n" + block[s:e]
	if out, err := exec.Command("bash", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("failure arm failed under bash: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(rec)
	var calls []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			calls = append(calls, l)
		}
	}
	return calls
}

func TestExit127_ShortensVisibilityTo60(t *testing.T) {
	calls := runExit127(t, "127")
	if len(calls) != 1 || !strings.Contains(calls[0], "change-message-visibility") || !strings.Contains(calls[0], "--visibility-timeout 60") || !strings.Contains(calls[0], "--receipt-handle r") {
		t.Fatalf("exit 127 must shorten the visibility to 60 s once: %v", calls)
	}
}

func TestExitOther_LeavesVisibilityAlone(t *testing.T) {
	if calls := runExit127(t, "1"); len(calls) != 0 {
		t.Fatalf("a non-127 failure must keep today's behaviour (no visibility change): %v", calls)
	}
}
```

- [ ] **Step 2: Run, expect FAIL** — `cd pkg/compiler && go test -run 'Exit127|ExitOther|EveryInboundPoller' .`

- [ ] **Step 3: Implement**

Replace each poller's failure line with:

```bash
        # --- km-exit-127 ---
        echo "[km-slack-inbound-poller] WARN: agent run failed (exit $RUN_EXIT), message returns to queue"
        if [ "$RUN_EXIT" -eq 127 ]; then
          # 127 = the agent binary is missing (the boot race the init gate now
          # prevents, or a profile with no agent install). Retry in 60 s instead
          # of the queue's 1800 s; the shared DLQ's maxReceiveCount still bounds it.
          echo "[km-slack-inbound-poller] agent missing (exit 127): message visible again in 60s"
          aws sqs change-message-visibility --queue-url "$QUEUE_URL" --receipt-handle "$RECEIPT" --visibility-timeout 60 --region "$REGION" 2>/dev/null || true
        fi
        # --- end km-exit-127 ---
```

with each poller's own tag and indentation. Confirm `$QUEUE_URL`, `$RECEIPT`, `$REGION` are the variable names in scope at that point in each poller (they are in the Slack one; check the other three and adapt). Patch the frozen golden for every poller it contains; enable the 127 assertions in the Task 2 guard.

- [ ] **Step 4: Run, expect PASS** — `cd pkg/compiler && go test . && cd ../.. && go build ./...`

- [ ] **Step 5: Commit**

```bash
git add pkg/compiler/userdata.go pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh pkg/compiler/userdata_exit127_test.go pkg/compiler/userdata_init_gate_guard_test.go
git commit -m "fix(pollers): an exit-127 turn makes its message visible again in 60 s"
```

---

### Task 4: Docs and memory

**Files:**
- Modify: `docs/operational-gotchas.md` (new entry "Cold-create first turn and the init gate"), `docs/slack-default-route.md` §11 (close the boot-race item), `CLAUDE.md` (a 4-line bullet above the Slack default route block), `docs/slack-notifications.md` (one sentence where the cold-create first turn is described)

- [ ] **Step 1: Write** the gotcha entry: symptom (first reply ~30 min late, journal lines), root cause (section 5 vs 7.5, the false comment), the fix (marker, gate, bound, 127 rule), what a resumed box does (no wait: marker persists), how to diagnose a profile with no agent (the WARNING now means that), deploy surface (`make build-lambdas` + `km init --dry-run=false`; existing boxes on recreate).
- [ ] **Step 2: Verify** `go build ./...`; commit by pathspec: `docs: cold-create boot race — init gate and exit-127 visibility`.

---

## Execution order
1 → 2 → 3 → 4. Tasks 2 and 3 may be one commit if the implementer prefers; the guard test must end up asserting both halves.

## After the plan (controller)
`make build-lambdas` + `km init --dry-run=false`; `km destroy` the default sandbox; mention from a channel; the reply must arrive within boot time, with the journal showing "waiting for profile init" → "profile init done after Ns" and no exit 127. Update memory `project_cold_create_first_turn_boot_race` to "fixed" with the mechanism.
