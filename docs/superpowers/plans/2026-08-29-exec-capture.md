# Phase 132 — Exec Capture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give a km sandbox an eBPF-backed record of every process it executed, and a verb that answers "which process reached that host" by joining that record against the Phase 131 flow census.

**Architecture:** Four tracepoints (`sys_enter_execve`, `sys_enter_execveat`, `sys_exit_execve`, `sched_process_exit`) feed a ring buffer drained by a root daemon, which appends JSONL to `/var/lib/km/execs/execs.jsonl`. The daemon is a hidden verb of the already-deployed `km-netpolicy` binary, run by an unconditional `km-execlog.service` — deliberately NOT inside `km ebpf-attach`, which userdata gates on enforcement mode. Read verbs and the pid/lifetime join live in the same binary.

**Tech Stack:** Go, `cilium/ebpf` v0.21.0 (ring buffer + tracepoint links), C for the BPF programs (compiled by bpf2go), `aws-sdk-go-v2/service/s3`, Terraform, systemd.

**Spec:** `docs/superpowers/specs/2026-08-29-exec-capture-design.md`

## Global Constraints

- **No new Go module dependencies.** `cilium/ebpf v0.21.0`, `golang.org/x/sys v0.43.0` and `aws-sdk-go-v2/service/s3 v1.97.1` are already in `go.mod`. The `go.mod`/`go.sum` diff for this phase must be empty.
- **`CGO_ENABLED=0` everywhere.** `km-netpolicy` cross-compiles to `linux/amd64` for the sandbox. `cilium/ebpf` is pure Go; nothing added here may require cgo.
- **Build tags mirror `pkg/ebpf`.** Real loader: `//go:build linux && amd64`. Stub: `//go:build !linux || !amd64`. `cmd/km-netpolicy` must keep building and testing on `darwin/arm64`.
- **`go generate` needs Homebrew clang, not Apple clang.** Apple clang has no BPF target (`error: unable to create target: 'No available targets are compatible with triple "bpf"'`). Use `/opt/homebrew/opt/llvm/bin/clang` (verified: Homebrew clang 21.1.8, BPF target OK).
- **Never edit `infra/modules/ec2spot/v1.5.0`.** Module directories are immutable; a change means a new `v1.6.0` directory plus a pin bump.
- **Exec store is root-only**, `0700` directory / `0600` files. The sandbox user must never read it.
- **`AWS_REGION` on every unit that touches S3.** Its absence is the bug `216d4664` just fixed on `km-capture.service`.
- **Linux-only tests run cross-compiled under Docker:** `go test -c` for `linux/amd64`, then run in a container. Never compile inside qemu.
- **Every task ends green:** `go build ./...` and `go vet ./...` clean, and the touched packages pass `go test -count=1`.

---

### Task 1: `pkg/execlog` — record, writer, reader

The store. Deliberately duplicates `flowlog.Writer` rather than sharing it; see spec §11.

**Files:**
- Create: `pkg/execlog/record.go`
- Create: `pkg/execlog/writer.go`
- Create: `pkg/execlog/reader.go`
- Test: `pkg/execlog/writer_test.go`, `pkg/execlog/reader_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `execlog.Record`, `execlog.KindExec`/`KindExit`, `execlog.DefaultDir`, `execlog.DefaultMaxBytes`, `execlog.Path(dir string) string`, `execlog.NewWriter(path string, maxBytes int64) *Writer`, `(*Writer).Write(Record) error`, `(*Writer).Close() error`, `execlog.ReadDir(dir string) ([]Record, error)`, `execlog.Rotations(dir string) int`.

- [ ] **Step 1: Write `pkg/execlog/record.go`**

```go
// Package execlog holds the process-execution vocabulary shared by the eBPF
// exec tracer and the on-box km-netpolicy helper.
//
// It is the process-side counterpart to pkg/flowlog: flowlog records where a
// sandbox went, execlog records what was running when it went there. The two
// join on pid (see join.go), which is the whole reason this package exists.
//
// Unlike flowlog there is exactly ONE producer — the root exec daemon — so
// there is one file, and none of flowlog's multi-writer directory-permission
// reasoning applies here. The directory is root-only instead, because argv is
// recorded unredacted and root's argv must not be readable by the sandbox user.
package execlog

import "time"

// Record kinds. Exit records carry only pid, timestamp and comm; they exist so
// the pid join can bound a process's lifetime and refuse to attribute a flow to
// a pid that has since been reused.
const (
	KindExec = "exec"
	KindExit = "exit"
)

// DefaultDir is where the daemon writes and km-netpolicy reads.
//
// Under /var/lib rather than /run for the same reason the deny list and flow
// store are: a trace silently emptied by a reboot would misrepresent what a box
// did, and this one feeds an operator's forensics rather than a live decision.
const DefaultDir = "/var/lib/km/execs"

// DefaultMaxBytes caps the live file before rotation. One previous generation
// is retained, so the store costs at most 2x this on disk.
const DefaultMaxBytes int64 = 16 << 20 // 16 MiB

// Record is one process event.
//
// Ret is deliberately NOT omitempty: zero means the exec succeeded, and a field
// absent because it was zero would be indistinguishable from one absent because
// it was never recorded. Every other optional field is omitempty for the mirror
// of that reason — a zero pid or an empty comm is not an observation.
type Record struct {
	TS        time.Time `json:"ts"`
	Kind      string    `json:"kind"`
	PID       int       `json:"pid"`
	PPID      int       `json:"ppid,omitempty"`
	UID       int       `json:"uid"`
	Comm      string    `json:"comm,omitempty"`
	Args      []string  `json:"args,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
	Ret       int       `json:"ret"`
	CgroupID  uint64    `json:"cgroup_id,omitempty"`
}

// Exe returns the executable the record describes: argv[0] when argv was
// captured, otherwise the kernel's 16-byte comm. Callers display this, so it
// must never be empty for an exec record that captured anything at all.
func (r Record) Exe() string {
	if len(r.Args) > 0 && r.Args[0] != "" {
		return r.Args[0]
	}
	return r.Comm
}

// Path returns the store file inside dir.
func Path(dir string) string { return dir + "/execs.jsonl" }
```

- [ ] **Step 2: Write the failing writer test**

Create `pkg/execlog/writer_test.go`:

```go
package execlog_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
)

func TestWriter_AppendsJSONL(t *testing.T) {
	dir := t.TempDir()
	p := execlog.Path(dir)
	w := execlog.NewWriter(p, execlog.DefaultMaxBytes)
	defer w.Close()

	if err := w.Write(execlog.Record{
		TS: time.Now(), Kind: execlog.KindExec, PID: 42, UID: 1000,
		Comm: "curl", Args: []string{"curl", "https://api.github.com"},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Fatalf("want 1 line, got %d: %s", n, b)
	}
	if !strings.Contains(string(b), `"kind":"exec"`) {
		t.Errorf("record missing kind: %s", b)
	}
}

func TestWriter_RotatesAndCountsRotations(t *testing.T) {
	dir := t.TempDir()
	p := execlog.Path(dir)
	// A cap small enough that the second record forces a rotation.
	w := execlog.NewWriter(p, 120)
	defer w.Close()

	for i := 0; i < 6; i++ {
		if err := w.Write(execlog.Record{
			TS: time.Now(), Kind: execlog.KindExec, PID: i, Comm: "sh",
		}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if _, err := os.Stat(p + ".1"); err != nil {
		t.Fatalf("want a rotated generation on disk: %v", err)
	}
	// The counter is what `pin`-style callers read to know the census lost
	// records; a rotation that does not bump it is silent data loss.
	if got := execlog.Rotations(dir); got < 1 {
		t.Errorf("want rotations >= 1, got %d", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "execs.jsonl.rotations")); err != nil {
		t.Errorf("rotation counter file missing: %v", err)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./pkg/execlog/ -run TestWriter -count=1`
Expected: FAIL — `undefined: execlog.NewWriter`, `undefined: execlog.Rotations`.

- [ ] **Step 4: Write `pkg/execlog/writer.go`**

```go
package execlog

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
)

// Writer appends records to the store, rotating at a size cap.
//
// Safe for concurrent use, though today there is a single caller: the exec
// daemon's ring-buffer drain loop.
type Writer struct {
	path     string
	maxBytes int64

	mu   sync.Mutex
	f    *os.File
	size int64

	warnOnce sync.Once
}

// NewWriter returns a Writer for path. The file is opened lazily on first
// Write, so constructing one before the directory exists is safe.
func NewWriter(path string, maxBytes int64) *Writer {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Writer{path: path, maxBytes: maxBytes}
}

// Write appends one record as a JSON line.
//
// The Writer logs its OWN first failure exactly once. This is not optional
// politeness: Phase 131's flow store failed EACCES on every write under the
// default enforcement mode and reported nothing, because each producer
// discarded the error at its call site. A store that fails silently is worse
// than no store, because an empty trace reads as "nothing happened".
func (w *Writer) Write(r Record) error {
	err := w.write(r)
	if err != nil {
		w.warnOnce.Do(func() {
			log.Warn().
				Str("event_type", "execlog_write_failed").
				Str("path", w.path).
				Err(err).
				Msg("exec recording is not working; the process trace will be incomplete")
		})
	}
	return err
}

func (w *Writer) write(r Record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.openLocked(); err != nil {
		return err
	}
	if w.size+int64(len(line)) > w.maxBytes {
		if err := w.rotateLocked(); err != nil {
			return err
		}
	}
	n, err := w.f.Write(line)
	w.size += int64(n)
	return err
}

// openLocked opens the live file if it is not already open. The directory is
// 0700 and the file 0600: argv is recorded unredacted and includes root's, so
// the sandbox user must not be able to read it. Caller holds w.mu.
func (w *Writer) openLocked() error {
	if w.f != nil {
		return nil
	}
	if err := os.MkdirAll(dirOf(w.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f, w.size = f, fi.Size()
	return nil
}

// rotateLocked moves the live file aside to ".1" and reopens an empty one.
// Exactly one previous generation is kept. Caller holds w.mu.
func (w *Writer) rotateLocked() error {
	if err := w.f.Close(); err != nil {
		return err
	}
	w.f = nil
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	bumpRotations(w.path)
	w.size = 0
	return w.openLocked()
}

// Close releases the underlying file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

const rotationSuffix = ".rotations"

// bumpRotations increments the rotation counter beside path. Best-effort: a
// counter that cannot be written costs a warning at read time, never a lost
// record, so a failure here must not abort a rotation that already happened.
func bumpRotations(path string) {
	n := readRotations(path)
	_ = os.WriteFile(path+rotationSuffix, []byte(strconv.Itoa(n+1)+"\n"), 0o600)
}

func readRotations(path string) int {
	b, err := os.ReadFile(path + rotationSuffix)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// Rotations reports how many times the store in dir has rotated. Two or more
// means the oldest generation was overwritten and the trace is missing its
// earliest execs — typically the package-install phase.
func Rotations(dir string) int { return readRotations(Path(dir)) }

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}
	return "."
}
```

- [ ] **Step 5: Run the writer test to verify it passes**

Run: `go test ./pkg/execlog/ -run TestWriter -count=1 -v`
Expected: PASS (both tests).

- [ ] **Step 6: Write the failing reader test**

Create `pkg/execlog/reader_test.go`:

```go
package execlog_test

import (
	"os"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
)

func TestReadDir_ReadsLiveAndRotatedOldestFirst(t *testing.T) {
	dir := t.TempDir()
	p := execlog.Path(dir)

	// A rotated generation holding the older record, and a live file holding
	// the newer one. ReadDir must return both, oldest first.
	older := `{"ts":"2026-08-29T10:00:00Z","kind":"exec","pid":1,"uid":0,"comm":"old","ret":0}`
	newer := `{"ts":"2026-08-29T11:00:00Z","kind":"exec","pid":2,"uid":0,"comm":"new","ret":0}`
	if err := os.WriteFile(p+".1", []byte(older+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(newer+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	recs, err := execlog.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d: %+v", len(recs), recs)
	}
	if recs[0].Comm != "old" || recs[1].Comm != "new" {
		t.Errorf("want oldest-first, got %s then %s", recs[0].Comm, recs[1].Comm)
	}
	if !recs[0].TS.Equal(time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("timestamp not parsed: %v", recs[0].TS)
	}
}

func TestReadDir_SkipsCorruptLinesAndMissingDir(t *testing.T) {
	// A missing directory is the normal state of a box that has executed
	// nothing yet, and must not read as an error.
	recs, err := execlog.ReadDir(t.TempDir() + "/nope")
	if err != nil {
		t.Fatalf("missing dir must not error: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("want no records, got %d", len(recs))
	}

	dir := t.TempDir()
	good := `{"ts":"2026-08-29T10:00:00Z","kind":"exec","pid":1,"uid":0,"comm":"ok","ret":0}`
	body := "not json at all\n" + good + "\n" + `{"ts":"2026-08-29T10:00:01Z"}` + "\n"
	if err := os.WriteFile(execlog.Path(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	recs, err = execlog.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	// The garbage line is skipped, and so is the record with no kind — a
	// record whose kind is unknown cannot be placed on either side of the join.
	if len(recs) != 1 || recs[0].Comm != "ok" {
		t.Fatalf("want only the well-formed record, got %+v", recs)
	}
}
```

- [ ] **Step 7: Run it to verify it fails**

Run: `go test ./pkg/execlog/ -run TestReadDir -count=1`
Expected: FAIL — `undefined: execlog.ReadDir`.

- [ ] **Step 8: Write `pkg/execlog/reader.go`**

```go
package execlog

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
)

// ReadDir reads the live store in dir and its one rotated generation, oldest
// record first.
//
// It never fails on bad data. A missing directory is the normal state of a box
// that has executed nothing yet; a corrupt line is skipped. The trace is
// forensic evidence read after the fact, so partial data is strictly better
// than an error that hides the rest of it.
func ReadDir(dir string) ([]Record, error) {
	var out []Record
	for _, p := range []string{Path(dir), Path(dir) + ".1"} {
		out = append(out, readFile(p)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS.Before(out[j].TS) })
	return out, nil
}

// readFile parses one JSONL file, skipping anything unparseable. An unreadable
// file yields no records rather than an error.
func readFile(path string) []Record {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []Record
	sc := bufio.NewScanner(f)
	// A record with 20 args of 128 bytes is ~2.6 KB, but a corrupt file can
	// present one enormous "line"; cap it rather than allocating without bound.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		if r.Kind != KindExec && r.Kind != KindExit {
			continue
		}
		out = append(out, r)
	}
	return out
}
```

- [ ] **Step 9: Run the full package test**

Run: `go test ./pkg/execlog/ -count=1 -v`
Expected: PASS — 4 tests.

- [ ] **Step 10: Commit**

```bash
git add pkg/execlog/record.go pkg/execlog/writer.go pkg/execlog/reader.go \
        pkg/execlog/writer_test.go pkg/execlog/reader_test.go
git commit -m "feat(execlog): the process-trace store, root-only and rotation-counted"
```

---

### Task 2: The pid/lifetime join

The verb that justifies the phase. Pure Go, fully testable on the host, and the one piece where a plausible-but-wrong implementation silently misattributes.

**Files:**
- Create: `pkg/execlog/join.go`
- Test: `pkg/execlog/join_test.go`

**Interfaces:**
- Consumes: `execlog.Record` (Task 1), `flowlog.Record` (shipped in Phase 131).
- Produces: `execlog.NewIndex(recs []Record) *Index`, `(*Index).At(pid int, t time.Time) (Record, bool)`, `execlog.Attribution{Flow flowlog.Record; Exec Record; Found bool}`, `execlog.Who(execs []Record, flows []flowlog.Record, host string) []Attribution`, `execlog.HostMatches(flowHost, query string) bool`.

- [ ] **Step 1: Write the failing join test**

Create `pkg/execlog/join_test.go`:

```go
package execlog_test

import (
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

func at(min int) time.Time {
	return time.Date(2026, 8, 29, 12, min, 0, 0, time.UTC)
}

func execAt(min, pid int, comm string, args ...string) execlog.Record {
	return execlog.Record{TS: at(min), Kind: execlog.KindExec, PID: pid, Comm: comm, Args: args}
}

func exitAt(min, pid int) execlog.Record {
	return execlog.Record{TS: at(min), Kind: execlog.KindExit, PID: pid}
}

func TestIndexAt_MatchesTheExecLiveAtThatMoment(t *testing.T) {
	ix := execlog.NewIndex([]execlog.Record{execAt(0, 100, "curl", "curl", "https://x")})
	got, ok := ix.At(100, at(5))
	if !ok {
		t.Fatal("want a match for a pid that had exec'd and not exited")
	}
	if got.Comm != "curl" {
		t.Errorf("want curl, got %q", got.Comm)
	}
}

func TestIndexAt_PIDReuseDoesNotMisattribute(t *testing.T) {
	// This is the case the whole design of the exit tracepoint exists for.
	// pid 100 runs curl, exits, and the number is reused by python. A flow at
	// minute 30 belongs to python and must NEVER be reported as curl.
	ix := execlog.NewIndex([]execlog.Record{
		execAt(0, 100, "curl", "curl", "https://first"),
		exitAt(10, 100),
		execAt(20, 100, "python3", "python3", "fetch.py"),
	})
	got, ok := ix.At(100, at(30))
	if !ok {
		t.Fatal("want a match against the second exec")
	}
	if got.Comm != "python3" {
		t.Fatalf("pid reuse misattributed the flow to %q", got.Comm)
	}
}

func TestIndexAt_NoMatchAfterExitOrBeforeExec(t *testing.T) {
	ix := execlog.NewIndex([]execlog.Record{
		execAt(10, 100, "curl", "curl"),
		exitAt(20, 100),
	})
	// The process was dead by minute 30 and nothing re-used the pid. Reporting
	// curl here would be a confident lie.
	if _, ok := ix.At(100, at(30)); ok {
		t.Error("must not attribute a flow to a process that had already exited")
	}
	// Nothing had exec'd on that pid yet at minute 5.
	if _, ok := ix.At(100, at(5)); ok {
		t.Error("must not attribute a flow to an exec that had not happened yet")
	}
	// A pid never seen at all.
	if _, ok := ix.At(999, at(15)); ok {
		t.Error("must not invent a match for an unknown pid")
	}
}

func TestIndexAt_LatestExecWinsForReExec(t *testing.T) {
	// A shell that exec's a binary keeps its pid. The later exec is the truth.
	ix := execlog.NewIndex([]execlog.Record{
		execAt(0, 100, "bash", "bash", "-c", "curl https://x"),
		execAt(1, 100, "curl", "curl", "https://x"),
	})
	got, _ := ix.At(100, at(2))
	if got.Comm != "curl" {
		t.Errorf("want the most recent exec, got %q", got.Comm)
	}
}

func TestWho_AttributesMatchingFlows(t *testing.T) {
	execs := []execlog.Record{execAt(0, 100, "curl", "curl", "https://api.github.com")}
	flows := []flowlog.Record{
		{TS: at(5), Src: flowlog.SrcEBPF, Verdict: flowlog.VerdictAllow, Host: "api.github.com", PID: 100},
		{TS: at(6), Src: flowlog.SrcEBPF, Verdict: flowlog.VerdictAllow, Host: "example.com", PID: 100},
	}
	got := execlog.Who(execs, flows, "api.github.com")
	if len(got) != 1 {
		t.Fatalf("want 1 attribution, got %d", len(got))
	}
	if !got[0].Found || got[0].Exec.Comm != "curl" {
		t.Errorf("want the curl exec, got %+v", got[0])
	}
}

func TestWho_FlowWithoutPIDIsReportedUnattributed(t *testing.T) {
	// Under proxy enforcement the DNS and HTTP proxies record no pid. The flow
	// still happened and must still be listed — silently dropping it would make
	// a real destination look like it was never reached.
	execs := []execlog.Record{execAt(0, 100, "curl", "curl")}
	flows := []flowlog.Record{
		{TS: at(5), Src: flowlog.SrcHTTP, Verdict: flowlog.VerdictAllow, Host: "api.github.com"},
	}
	got := execlog.Who(execs, flows, "api.github.com")
	if len(got) != 1 {
		t.Fatalf("want the flow listed anyway, got %d", len(got))
	}
	if got[0].Found {
		t.Error("a flow with no pid must not be attributed to anything")
	}
}

func TestHostMatches_SubdomainAndCaseAndNonMatch(t *testing.T) {
	cases := []struct {
		flowHost, query string
		want            bool
	}{
		{"api.github.com", "api.github.com", true},
		{"api.github.com", "github.com", true},  // subdomain of the query
		{"API.GitHub.com", "github.com", true},  // case-insensitive
		{"github.com", "api.github.com", false}, // parent is not a match for a child
		{"github.com.evil.example", "github.com", false}, // the lookalike Phase 129 fixed
		{"", "github.com", false},
	}
	for _, c := range cases {
		if got := execlog.HostMatches(c.flowHost, c.query); got != c.want {
			t.Errorf("HostMatches(%q, %q) = %v, want %v", c.flowHost, c.query, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./pkg/execlog/ -run 'TestIndex|TestWho|TestHostMatches' -count=1`
Expected: FAIL — `undefined: execlog.NewIndex`, `undefined: execlog.Who`, `undefined: execlog.HostMatches`.

- [ ] **Step 3: Write `pkg/execlog/join.go`**

```go
package execlog

import (
	"sort"
	"strings"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

// Index answers "what was pid P running at time T" over a trace.
//
// It exists because joining a flow to an exec on pid ALONE is wrong: pids are
// reused, so a flow can land on a number whose original owner is long dead.
// Exit records bound each process's lifetime, which turns the join from a
// plausible guess into a fact — and a wrong attribution here is worse than no
// attribution, because it names an innocent process with full confidence.
type Index struct {
	byPID map[int][]Record
}

// NewIndex builds the per-pid timeline. Records may arrive in any order.
func NewIndex(recs []Record) *Index {
	ix := &Index{byPID: map[int][]Record{}}
	for _, r := range recs {
		if r.Kind != KindExec && r.Kind != KindExit {
			continue
		}
		ix.byPID[r.PID] = append(ix.byPID[r.PID], r)
	}
	for pid := range ix.byPID {
		rs := ix.byPID[pid]
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].TS.Before(rs[j].TS) })
		ix.byPID[pid] = rs
	}
	return ix
}

// At returns the exec that pid was running at time t, if any.
//
// It walks the pid's timeline forward, holding the most recent exec and
// clearing it on an exit. Whatever is held when the walk passes t is the
// answer; nothing held means the pid had either not exec'd yet or had already
// exited, and in both cases the honest answer is "no match".
func (ix *Index) At(pid int, t time.Time) (Record, bool) {
	var cur Record
	var live bool
	for _, r := range ix.byPID[pid] {
		if r.TS.After(t) {
			break
		}
		switch r.Kind {
		case KindExec:
			cur, live = r, true
		case KindExit:
			cur, live = Record{}, false
		}
	}
	return cur, live
}

// Attribution pairs one flow with the process that produced it.
//
// Found is false when the flow carries no pid (the DNS and HTTP proxies record
// none) or when no process owned that pid at that moment. The flow is still
// returned either way: dropping it would make a destination the sandbox really
// reached look like it was never reached at all.
type Attribution struct {
	Flow  flowlog.Record
	Exec  Record
	Found bool
}

// Who returns every flow to host, each paired with the process behind it.
func Who(execs []Record, flows []flowlog.Record, host string) []Attribution {
	ix := NewIndex(execs)
	var out []Attribution
	for _, f := range flows {
		if !HostMatches(f.Host, host) && !strings.EqualFold(f.Addr, host) {
			continue
		}
		a := Attribution{Flow: f}
		if f.PID != 0 {
			a.Exec, a.Found = ix.At(f.PID, f.TS)
		}
		out = append(out, a)
	}
	return out
}

// HostMatches reports whether a flow's host is query or a subdomain of it.
//
// Deliberately the same shape as the allowlist matcher: case-insensitive, and a
// leading-dot boundary rather than a bare suffix, so "github.com.evil.example"
// does not match "github.com" — the lookalike Phase 129's matcher narrowing
// fixed. A forensics query that quietly matched the attacker's domain would be
// worse than one that matched nothing.
func HostMatches(flowHost, query string) bool {
	if flowHost == "" || query == "" {
		return false
	}
	h := strings.ToLower(strings.TrimSuffix(flowHost, "."))
	q := strings.ToLower(strings.TrimPrefix(strings.TrimSuffix(query, "."), "."))
	return h == q || strings.HasSuffix(h, "."+q)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./pkg/execlog/ -count=1 -v`
Expected: PASS — all tests including the four `TestIndexAt_*` cases.

- [ ] **Step 5: Commit**

```bash
git add pkg/execlog/join.go pkg/execlog/join_test.go
git commit -m "feat(execlog): join flows to processes on pid bounded by lifetime"
```

---

### Task 3: The BPF programs

**Files:**
- Create: `pkg/ebpf/exec/exec.c`
- Create: `pkg/ebpf/exec/gen.go`
- Create (generated, committed): `pkg/ebpf/exec/execbpf_x86_bpfel.go`, `pkg/ebpf/exec/execbpf_x86_bpfel.o`

**Interfaces:**
- Consumes: `pkg/ebpf/headers/{vmlinux.h,bpf_helpers.h}` (already present).
- Produces: generated `loadExecbpfObjects(*execbpfObjects, *ebpf.CollectionOptions) error` and the fields `execbpfObjects.TpEnterExecve`, `.TpEnterExecveat`, `.TpExitExecve`, `.TpProcessExit`, `.ExecEvents`.

- [ ] **Step 1: Write `pkg/ebpf/exec/exec.c`**

Note the two things that will otherwise cost an afternoon each: the event is far
larger than BPF's 512-byte stack so it is built in a per-CPU scratch map, and
`sched_process_exit` fires per *thread*, so it must be filtered to the
thread-group leader or the store fills with thread noise.

```c
// SPDX-License-Identifier: GPL-2.0-only
/*
 * exec.c — Kernel-side process-execution tracing for klanker-maker.
 *
 * Programs:
 *   1. tracepoint/syscalls/sys_enter_execve    — capture argv into an in-flight slot
 *   2. tracepoint/syscalls/sys_enter_execveat  — same, for the execveat entry point
 *   3. tracepoint/syscalls/sys_exit_execve     — stamp the return code and emit
 *   4. tracepoint/sched/sched_process_exit     — emit a process-end marker
 *
 * The enter/exit split exists so FAILED execs are recorded: an agent reaching
 * for a binary that is absent or not permitted is a finding, and
 * sched_process_exec fires only on success.
 *
 * Program 4 exists so the userspace join can bound a process's lifetime. Without
 * it, pid reuse makes the flow-to-process join a confident lie.
 *
 * Build: go generate ./pkg/ebpf/exec/ (requires clang WITH a BPF target —
 * Apple clang has none; use /opt/homebrew/opt/llvm/bin/clang).
 */

#include "../headers/vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_core_read.h>

#define MAX_ARGS      20
#define ARGSIZE       128
#define TASK_COMM_LEN 16

#define KIND_EXEC 0
#define KIND_EXIT 1

/* Split into a header and an argv tail so an exit record can be emitted as just
 * the header. The ring buffer carries each record's own length, so userspace
 * tells the two apart by size — an exit record costs 56 bytes instead of 2.6 KB,
 * and exits are as frequent as execs. */
struct exec_hdr {
    __u64 ts_ns;
    __u64 cgroup_id;
    __u32 pid;
    __u32 ppid;
    __u32 uid;
    __s32 ret;
    __u8  kind;
    __u8  truncated;
    __u8  nargs;
    __u8  _pad;
    char  comm[TASK_COMM_LEN];
};

struct exec_event {
    struct exec_hdr h;
    char args[MAX_ARGS][ARGSIZE];
};

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 1 << 22); /* 4 MiB */
} exec_events SEC(".maps");

/* In-flight execve between enter and exit, keyed by pid_tgid. */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 4096);
    __type(key, __u64);
    __type(value, struct exec_event);
} inflight SEC(".maps");

/* struct exec_event is ~2.6 KB and the BPF stack is 512 bytes, so the event is
 * assembled in a per-CPU scratch slot and copied from there. Building it on the
 * stack does not merely warn — the verifier rejects the program outright. */
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, struct exec_event);
} scratch SEC(".maps");

static __always_inline int record_enter(const char *const *argv)
{
    __u32 zero = 0;
    struct exec_event *e = bpf_map_lookup_elem(&scratch, &zero);
    if (!e)
        return 0;

    __builtin_memset(e, 0, sizeof(*e));

    __u64 id = bpf_get_current_pid_tgid();
    e->h.kind      = KIND_EXEC;
    e->h.pid       = (__u32)(id >> 32);
    e->h.uid       = (__u32)bpf_get_current_uid_gid();
    e->h.cgroup_id = bpf_get_current_cgroup_id();

    struct task_struct *task = (struct task_struct *)bpf_get_current_task();
    e->h.ppid = BPF_CORE_READ(task, real_parent, tgid);

    int n = 0;
#pragma unroll
    for (int i = 0; i < MAX_ARGS; i++) {
        const char *p = NULL;
        if (bpf_probe_read_user(&p, sizeof(p), &argv[i]) != 0 || !p)
            goto done;
        if (bpf_probe_read_user_str(e->args[i], ARGSIZE, p) < 0)
            goto done;
        n = i + 1;
    }
    /* Every slot filled: check whether there was at least one more argument we
     * had no room for, so `truncated` means "we lost some" rather than "we
     * happened to use every slot". */
    {
        const char *p = NULL;
        if (bpf_probe_read_user(&p, sizeof(p), &argv[MAX_ARGS]) == 0 && p)
            e->h.truncated = 1;
    }
done:
    e->h.nargs = (__u8)n;
    bpf_map_update_elem(&inflight, &id, e, BPF_ANY);
    return 0;
}

SEC("tracepoint/syscalls/sys_enter_execve")
int tp_enter_execve(struct trace_event_raw_sys_enter *ctx)
{
    /* execve(const char *filename, char *const argv[], char *const envp[]) */
    return record_enter((const char *const *)ctx->args[1]);
}

SEC("tracepoint/syscalls/sys_enter_execveat")
int tp_enter_execveat(struct trace_event_raw_sys_enter *ctx)
{
    /* execveat(int dirfd, const char *pathname, char *const argv[], ...) */
    return record_enter((const char *const *)ctx->args[2]);
}

SEC("tracepoint/syscalls/sys_exit_execve")
int tp_exit_execve(struct trace_event_raw_sys_exit *ctx)
{
    __u64 id = bpf_get_current_pid_tgid();
    struct exec_event *e = bpf_map_lookup_elem(&inflight, &id);
    if (!e)
        return 0;

    e->h.ts_ns = bpf_ktime_get_boot_ns();
    e->h.ret   = (__s32)ctx->ret;
    /* On success this is the comm of the NEW image, which is what an operator
     * wants to see; on failure it is the caller's, which is also what they
     * want. Either way it is read here rather than at enter. */
    bpf_get_current_comm(&e->h.comm, sizeof(e->h.comm));

    bpf_ringbuf_output(&exec_events, e, sizeof(*e), 0);
    bpf_map_delete_elem(&inflight, &id);
    return 0;
}

SEC("tracepoint/sched/sched_process_exit")
int tp_process_exit(void *ctx)
{
    __u64 id   = bpf_get_current_pid_tgid();
    __u32 tgid = (__u32)(id >> 32);
    __u32 pid  = (__u32)id;

    /* This tracepoint fires for every TASK, threads included. Only the
     * thread-group leader's exit ends the process, and only that bounds the
     * lifetime the join cares about. Without this filter a threaded process
     * emits an exit record per thread and the join sees the process die early. */
    if (tgid != pid)
        return 0;

    __u32 zero = 0;
    struct exec_event *e = bpf_map_lookup_elem(&scratch, &zero);
    if (!e)
        return 0;

    __builtin_memset(e, 0, sizeof(e->h));
    e->h.kind  = KIND_EXIT;
    e->h.pid   = tgid;
    e->h.ts_ns = bpf_ktime_get_boot_ns();
    bpf_get_current_comm(&e->h.comm, sizeof(e->h.comm));

    /* Header only — see the struct comment. */
    bpf_ringbuf_output(&exec_events, e, sizeof(struct exec_hdr), 0);
    return 0;
}

char _license[] SEC("license") = "GPL";
```

- [ ] **Step 2: Write `pkg/ebpf/exec/gen.go`**

```go
//go:build linux

// Package exec contains the kernel-side BPF programs and userspace loader for
// klanker-maker process-execution tracing.
//
// It is deliberately a separate package with its own compiled object from
// pkg/ebpf: the enforcer's programs are cgroup-attached and gated on
// enforcement mode, while these are tracepoints that run on every sandbox. One
// object serving both would couple observation to enforcement, which is exactly
// the coupling that made Phase 131's flow recording dead under the default mode.
//
// To regenerate (requires clang WITH a BPF target — Apple clang has none):
//
//	CLANG=/opt/homebrew/opt/llvm/bin/clang go generate ./pkg/ebpf/exec/
package exec

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags linux -target amd64 execbpf exec.c -- -I../headers -O2 -g -Wall -Werror
```

- [ ] **Step 3: Generate the BPF object**

Run:
```bash
cd pkg/ebpf/exec && CLANG=/opt/homebrew/opt/llvm/bin/clang go generate ./... && cd -
ls -la pkg/ebpf/exec/
```
Expected: `execbpf_x86_bpfel.go` and `execbpf_x86_bpfel.o` exist.

If clang reports `unable to create target: 'No available targets are compatible with triple "bpf"'`, `CLANG` is pointing at Apple clang. Confirm with `/opt/homebrew/opt/llvm/bin/clang --version` (expect "Homebrew clang").

- [ ] **Step 4: Verify it compiles into the tree**

Run: `go build ./... && go vet ./pkg/ebpf/exec/`
Expected: clean. The generated file carries `//go:build linux && amd64`, so this only proves it parses on darwin; Task 4 proves it loads.

- [ ] **Step 5: Commit**

```bash
git add pkg/ebpf/exec/exec.c pkg/ebpf/exec/gen.go \
        pkg/ebpf/exec/execbpf_x86_bpfel.go pkg/ebpf/exec/execbpf_x86_bpfel.o
git commit -m "feat(ebpf): execve and process-exit tracepoints in their own object"
```

---

### Task 4: The loader

**Files:**
- Create: `pkg/ebpf/exec/tracer_linux.go`
- Create: `pkg/ebpf/exec/tracer_stub.go`
- Test: `pkg/ebpf/exec/tracer_linux_test.go`

**Interfaces:**
- Consumes: Task 3's generated objects; `execlog.Record`, `execlog.KindExec`, `execlog.KindExit` (Task 1).
- Produces: `exec.NewTracer() (*Tracer, error)`, `(*Tracer).Events() <-chan execlog.Record`, `(*Tracer).Close() error`, and `exec.ErrUnsupported`.

- [ ] **Step 1: Write `pkg/ebpf/exec/tracer_stub.go`**

```go
//go:build !linux || !amd64

// Stub for platforms without the compiled BPF object (darwin dev machines,
// arm64 Lambda). Exec tracing only runs on EC2 x86_64 sandboxes; this lets
// cmd/km-netpolicy build and test everywhere else.
package exec

import (
	"errors"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
)

// ErrUnsupported is returned when exec tracing is not available on this
// platform. Callers surface it as a clear message rather than a crash.
var ErrUnsupported = errors.New("exec tracing requires linux/amd64")

// Tracer is the no-op form of the eBPF exec tracer.
type Tracer struct{}

// NewTracer always fails on unsupported platforms.
func NewTracer() (*Tracer, error) { return nil, ErrUnsupported }

// Events returns a nil channel; it is never reached because NewTracer fails.
func (t *Tracer) Events() <-chan execlog.Record { return nil }

// Close is a no-op.
func (t *Tracer) Close() error { return nil }
```

- [ ] **Step 2: Write `pkg/ebpf/exec/tracer_linux.go`**

```go
//go:build linux && amd64

package exec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"
	"golang.org/x/sys/unix"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
)

// ErrUnsupported is returned when exec tracing is not available. On this build
// it can still surface if the kernel rejects the programs.
var ErrUnsupported = errors.New("exec tracing unavailable")

const (
	maxArgs      = 20
	argSize      = 128
	taskCommLen  = 16
	kindExecByte = 0
	kindExitByte = 1
)

// rawHdr mirrors struct exec_hdr in exec.c field for field. Any change there
// must change this, and the size assertion in the test is what catches it.
type rawHdr struct {
	TSNS      uint64
	CgroupID  uint64
	PID       uint32
	PPID      uint32
	UID       uint32
	Ret       int32
	Kind      uint8
	Truncated uint8
	NArgs     uint8
	_         uint8
	Comm      [taskCommLen]byte
}

// Tracer loads the execve tracepoints and streams decoded records.
type Tracer struct {
	objs   execbpfObjects
	links  []link.Link
	rd     *ringbuf.Reader
	out    chan execlog.Record
	closed chan struct{}
	// bootWall is the wall-clock time the machine booted, used to convert the
	// kernel's CLOCK_BOOTTIME stamps into absolute times. It is computed ONCE:
	// deriving it per record would let clock adjustment jitter reorder the
	// trace, and the join depends on ordering being monotonic.
	bootWall time.Time
}

// NewTracer loads the BPF programs, attaches all four tracepoints, and starts
// draining the ring buffer.
func NewTracer() (*Tracer, error) {
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("remove memlock: %w", err)
	}

	t := &Tracer{out: make(chan execlog.Record, 1024), closed: make(chan struct{})}
	if err := loadExecbpfObjects(&t.objs, nil); err != nil {
		return nil, fmt.Errorf("load exec bpf objects: %w", err)
	}

	attach := []struct {
		group, name string
		prog        interface{ Close() error }
	}{}
	_ = attach // replaced below; kept explicit for readability

	type tp struct {
		group, name string
		prog        *ebpfProgram
	}
	_ = tp

	links, err := t.attachAll()
	if err != nil {
		t.objs.Close()
		return nil, err
	}
	t.links = links

	rd, err := ringbuf.NewReader(t.objs.ExecEvents)
	if err != nil {
		t.closeLinks()
		t.objs.Close()
		return nil, fmt.Errorf("open ringbuf: %w", err)
	}
	t.rd = rd

	boot, err := bootWallClock()
	if err != nil {
		t.rd.Close()
		t.closeLinks()
		t.objs.Close()
		return nil, err
	}
	t.bootWall = boot

	go t.drain()
	return t, nil
}

// Events streams decoded records until Close.
func (t *Tracer) Events() <-chan execlog.Record { return t.out }

// Close detaches everything and stops the drain goroutine.
func (t *Tracer) Close() error {
	select {
	case <-t.closed:
		return nil
	default:
		close(t.closed)
	}
	if t.rd != nil {
		t.rd.Close()
	}
	t.closeLinks()
	return t.objs.Close()
}

func (t *Tracer) closeLinks() {
	for _, l := range t.links {
		l.Close()
	}
	t.links = nil
}

// drain reads the ring buffer until it is closed, decoding each record.
//
// A malformed or short record is SKIPPED, never fatal: losing one event must
// never stop the trace, for the same reason a producer never lets a failed flow
// write abort an egress decision.
func (t *Tracer) drain() {
	defer close(t.out)
	for {
		rec, err := t.rd.Read()
		if err != nil {
			return // reader closed
		}
		r, ok := t.decode(rec.RawSample)
		if !ok {
			continue
		}
		select {
		case t.out <- r:
		case <-t.closed:
			return
		}
	}
}

// decode turns one ring-buffer sample into a record. Exit records are emitted
// as the header alone, so the sample's length is what distinguishes them.
func (t *Tracer) decode(b []byte) (execlog.Record, bool) {
	var h rawHdr
	if len(b) < binary.Size(h) {
		return execlog.Record{}, false
	}
	if err := binary.Read(bytes.NewReader(b), binary.LittleEndian, &h); err != nil {
		return execlog.Record{}, false
	}

	r := execlog.Record{
		TS:        t.bootWall.Add(time.Duration(h.TSNS)),
		PID:       int(h.PID),
		PPID:      int(h.PPID),
		UID:       int(h.UID),
		Ret:       int(h.Ret),
		CgroupID:  h.CgroupID,
		Comm:      cstr(h.Comm[:]),
		Truncated: h.Truncated != 0,
	}

	switch h.Kind {
	case kindExitByte:
		r.Kind = execlog.KindExit
		return r, true
	case kindExecByte:
		r.Kind = execlog.KindExec
	default:
		return execlog.Record{}, false
	}

	args := b[binary.Size(h):]
	n := int(h.NArgs)
	if n > maxArgs {
		n = maxArgs
	}
	for i := 0; i < n; i++ {
		lo := i * argSize
		if lo+argSize > len(args) {
			break
		}
		r.Args = append(r.Args, cstr(args[lo:lo+argSize]))
	}
	return r, true
}

// cstr trims a fixed-width NUL-padded C string.
func cstr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// bootWallClock returns the wall-clock instant the machine booted.
//
// The kernel stamps events with CLOCK_BOOTTIME (nanoseconds since boot,
// including suspend), so absolute times need this offset. CLOCK_BOOTTIME rather
// than CLOCK_MONOTONIC because a sandbox can be paused and resumed, and a
// monotonic clock that stops across a hibernate would compress hours of trace
// into an instant.
func bootWallClock() (time.Time, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); err != nil {
		return time.Time{}, fmt.Errorf("clock_gettime(BOOTTIME): %w", err)
	}
	return time.Now().Add(-time.Duration(ts.Nano())), nil
}
```

- [ ] **Step 3: Add `attachAll` in the same file**

Append to `pkg/ebpf/exec/tracer_linux.go`, and delete the two placeholder
`attach`/`tp` declarations from `NewTracer` — they exist only to keep Step 2's
listing readable and must not survive:

```go
// attachAll attaches the four tracepoints, unwinding cleanly if any fails.
//
// A partial attach is never left in place: three of four tracepoints would
// produce a trace that looks complete and silently is not — the exact failure
// shape this phase exists to eliminate.
func (t *Tracer) attachAll() ([]link.Link, error) {
	specs := []struct{ group, name string; prog *ebpf.Program }{
		{"syscalls", "sys_enter_execve", t.objs.TpEnterExecve},
		{"syscalls", "sys_enter_execveat", t.objs.TpEnterExecveat},
		{"syscalls", "sys_exit_execve", t.objs.TpExitExecve},
		{"sched", "sched_process_exit", t.objs.TpProcessExit},
	}
	var links []link.Link
	for _, s := range specs {
		l, err := link.Tracepoint(s.group, s.name, s.prog, nil)
		if err != nil {
			for _, done := range links {
				done.Close()
			}
			return nil, fmt.Errorf("attach %s/%s: %w", s.group, s.name, err)
		}
		links = append(links, l)
	}
	return links, nil
}
```

Add `"github.com/cilium/ebpf"` to the import block.

- [ ] **Step 4: Write the Linux-only tracer test**

Create `pkg/ebpf/exec/tracer_linux_test.go`:

```go
//go:build linux && amd64

package exec

import (
	"encoding/binary"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
)

// The Go struct must stay the same size as the C one, or every field after the
// first mismatch decodes as garbage while still looking like a plausible record.
func TestRawHdrSize(t *testing.T) {
	if got, want := binary.Size(rawHdr{}), 56; got != want {
		t.Fatalf("rawHdr is %d bytes, C struct exec_hdr is %d — they have drifted", got, want)
	}
}

func TestTracer_CapturesAnExecWithArgv(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("loading BPF programs requires root")
	}
	tr, err := NewTracer()
	if err != nil {
		t.Fatalf("NewTracer: %v", err)
	}
	defer tr.Close()

	// Give the tracepoints a moment to be live before generating the event.
	time.Sleep(200 * time.Millisecond)
	marker := "km-exec-capture-probe"
	if err := exec.Command("/bin/echo", marker).Run(); err != nil {
		t.Fatalf("probe command: %v", err)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case r := <-tr.Events():
			if r.Kind != execlog.KindExec {
				continue
			}
			if len(r.Args) >= 2 && r.Args[1] == marker {
				if r.PID == 0 || r.UID != 0 {
					t.Errorf("record has implausible identity: %+v", r)
				}
				if r.TS.Before(time.Now().Add(-time.Hour)) {
					t.Errorf("boot-time conversion is wrong: %v", r.TS)
				}
				return
			}
		case <-deadline:
			t.Fatal("never saw the probe exec; tracepoints attached but produced nothing")
		}
	}
}
```

- [ ] **Step 5: Build and run the Linux test under Docker**

This is the step that resolves the spec's open risk (§7): whether tracepoint
attach works in a container at all. Find out now, not in the last wave.

Run:
```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/exectracer.test ./pkg/ebpf/exec/
docker run --rm --privileged -v /tmp/exectracer.test:/t \
  -v /sys/kernel/debug:/sys/kernel/debug alpine /t -test.v
```
Expected: PASS for both tests.

If attach fails with a permissions or missing-`tracefs` error, retry with
`--pid=host`. If it still fails, the container cannot host this test: record that
in the plan, keep `TestRawHdrSize` (which needs no kernel), and move
`TestTracer_CapturesAnExecWithArgv` to the live UAT in Task 10. **Do not delete
it and do not weaken it into something that passes vacuously.**

- [ ] **Step 6: Verify the darwin build still works**

Run: `go build ./... && go test ./pkg/execlog/ ./cmd/km-netpolicy/ -count=1`
Expected: clean — the stub keeps the tree building where the object does not exist.

- [ ] **Step 7: Commit**

```bash
git add pkg/ebpf/exec/tracer_linux.go pkg/ebpf/exec/tracer_stub.go \
        pkg/ebpf/exec/tracer_linux_test.go
git commit -m "feat(ebpf): userspace tracer streaming decoded exec records"
```

---

### Task 5: The daemon verb

**Files:**
- Create: `cmd/km-netpolicy/execs_daemon_linux.go`
- Create: `cmd/km-netpolicy/execs_daemon_stub.go`
- Test: `cmd/km-netpolicy/execs_daemon_stub_test.go`

**Interfaces:**
- Consumes: `exec.NewTracer`, `exec.ErrUnsupported` (Task 4); `execlog.NewWriter`, `execlog.Path`, `execlog.DefaultDir` (Task 1).
- Produces: `runExecsDaemon(o opts) error`, matching the existing `opts` struct used by the other verbs in this package.

- [ ] **Step 1: Write `cmd/km-netpolicy/execs_daemon_stub.go`**

```go
//go:build !linux || !amd64

package main

import "fmt"

// runExecsDaemon reports plainly that the platform cannot host the tracer,
// rather than failing in a way that reads like a configuration problem.
func runExecsDaemon(o opts) error {
	fmt.Fprintln(o.stderr, "exec tracing requires linux/amd64")
	return errUnsupportedPlatform
}
```

- [ ] **Step 2: Write `cmd/km-netpolicy/execs_daemon_linux.go`**

```go
//go:build linux && amd64

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	ebpfexec "github.com/whereiskurt/klanker-maker/pkg/ebpf/exec"
	"github.com/whereiskurt/klanker-maker/pkg/execlog"
)

// runExecsDaemon loads the tracepoints and appends every record to the store
// until it is signalled to stop.
//
// It runs as root under km-execlog.service. Unlike the enforcer it makes no
// decisions and holds no state a restart would lose, so a crash costs a gap in
// the trace and nothing else — which is why the unit restarts it rather than
// treating a failure as fatal to the sandbox.
func runExecsDaemon(o opts) error {
	dir := envOr("KM_EXEC_DIR", execlog.DefaultDir)

	tr, err := ebpfexec.NewTracer()
	if err != nil {
		return fmt.Errorf("start exec tracer: %w", err)
	}
	defer tr.Close()

	w := execlog.NewWriter(execlog.Path(dir), execlog.DefaultMaxBytes)
	defer w.Close()

	fmt.Fprintf(o.stdout, "exec tracing started (store: %s)\n", execlog.Path(dir))

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case r, ok := <-tr.Events():
			if !ok {
				return nil
			}
			// The error is deliberately discarded here: the Writer logs its own
			// first failure once, and a write problem must never stop the drain
			// loop and back the ring buffer up.
			_ = w.Write(r)
		case <-sigs:
			return nil
		}
	}
}

// envOr returns the environment value for key, or def when it is unset.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

If `envOr` (or an equivalent) already exists in `cmd/km-netpolicy` — check
`env.go` and `capture.go` first — reuse it and delete this copy.

- [ ] **Step 3: Add `errUnsupportedPlatform` if it does not exist**

Check `cmd/km-netpolicy/main.go` for an existing sentinel. If absent, add to `main.go`:

```go
// errUnsupportedPlatform is returned by verbs whose implementation exists only
// on the sandbox's linux/amd64 build.
var errUnsupportedPlatform = errors.New("unsupported platform")
```

- [ ] **Step 4: Write the stub test**

Create `cmd/km-netpolicy/execs_daemon_stub_test.go`:

```go
//go:build !linux || !amd64

package main

import (
	"bytes"
	"testing"
)

func TestRunExecsDaemon_UnsupportedPlatformIsClear(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{stdout: &out, stderr: &errb}
	if err := runExecsDaemon(o); err == nil {
		t.Fatal("want an error on an unsupported platform")
	}
	if !bytes.Contains(errb.Bytes(), []byte("linux/amd64")) {
		t.Errorf("want a message naming the platform requirement, got %q", errb.String())
	}
}
```

- [ ] **Step 5: Run it**

Run: `go test ./cmd/km-netpolicy/ -run TestRunExecsDaemon -count=1 -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/km-netpolicy/execs_daemon_linux.go cmd/km-netpolicy/execs_daemon_stub.go \
        cmd/km-netpolicy/execs_daemon_stub_test.go cmd/km-netpolicy/main.go
git commit -m "feat(km-netpolicy): execs-daemon verb draining the tracer to the store"
```

---

### Task 6: The `execs` and `who` read verbs

**Files:**
- Create: `cmd/km-netpolicy/execs.go`
- Test: `cmd/km-netpolicy/execs_test.go`
- Modify: `cmd/km-netpolicy/main.go` (dispatch + usage)

**Interfaces:**
- Consumes: `execlog.ReadDir`, `execlog.Rotations` (Task 1); `execlog.Who`, `execlog.Attribution` (Task 2); `flowlog.ReadDir`, `flowlog.DefaultDir` (Phase 131).
- Produces: `runExecs(o opts, args []string) error`, `runWho(o opts, args []string) error`, `formatExec(r execlog.Record) string`.

- [ ] **Step 1: Write the failing verb test**

Create `cmd/km-netpolicy/execs_test.go`:

```go
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

	t.Setenv("KM_EXEC_DIR", dir)
	var out, errb bytes.Buffer
	if err := runExecs(opts{stdout: &out, stderr: &errb}, nil); err != nil {
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

	t.Setenv("KM_EXEC_DIR", dir)
	var out, errb bytes.Buffer
	if err := runExecs(opts{stdout: &out, stderr: &errb}, []string{"--failed"}); err != nil {
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
	t.Setenv("KM_EXEC_DIR", t.TempDir())
	var out, errb bytes.Buffer
	if err := runExecs(opts{stdout: &out, stderr: &errb}, nil); err != nil {
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

	t.Setenv("KM_EXEC_DIR", execDir)
	t.Setenv("KM_FLOWLOG_DIR", flowDir)
	var out, errb bytes.Buffer
	if err := runWho(opts{stdout: &out, stderr: &errb}, []string{"api.github.com"}); err != nil {
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

	t.Setenv("KM_EXEC_DIR", execDir)
	t.Setenv("KM_FLOWLOG_DIR", flowDir)
	var out, errb bytes.Buffer
	if err := runWho(opts{stdout: &out, stderr: &errb}, []string{"api.github.com"}); err != nil {
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
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/km-netpolicy/ -run 'TestRunExecs|TestRunWho' -count=1`
Expected: FAIL — `undefined: runExecs`, `undefined: runWho`.

- [ ] **Step 3: Write `cmd/km-netpolicy/execs.go`**

```go
package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

// execDir returns the exec store directory, overridable for tests and for a
// daemon told to write elsewhere.
func execDir() string { return envOr("KM_EXEC_DIR", execlog.DefaultDir) }

// flowDir returns the flow store directory that `who` joins against.
func flowDir() string { return envOr("KM_FLOWLOG_DIR", flowlog.DefaultDir) }

// runExecs lists what the sandbox executed.
func runExecs(o opts, args []string) error {
	fs := flag.NewFlagSet("execs", flag.ContinueOnError)
	fs.SetOutput(o.stderr)
	since := fs.Duration("since", 0, "only show execs within this window (e.g. 10m)")
	uid := fs.Int("uid", -1, "only show execs by this uid")
	failed := fs.Bool("failed", false, "only show execs that failed")
	asJSON := fs.Bool("json", false, "emit raw JSONL")
	if err := fs.Parse(args); err != nil {
		return err
	}

	recs, err := execlog.ReadDir(execDir())
	if err != nil {
		return err
	}

	cutoff := time.Time{}
	if *since > 0 {
		cutoff = time.Now().Add(-*since)
	}

	shown := 0
	for _, r := range recs {
		// Exit records exist only so the join can bound a lifetime. They are
		// not commands anybody ran, so they never appear in this listing.
		if r.Kind != execlog.KindExec {
			continue
		}
		if !cutoff.IsZero() && r.TS.Before(cutoff) {
			continue
		}
		if *uid >= 0 && r.UID != *uid {
			continue
		}
		if *failed && r.Ret == 0 {
			continue
		}
		shown++
		if *asJSON {
			fmt.Fprintln(o.stdout, jsonLine(r))
			continue
		}
		fmt.Fprintln(o.stdout, formatExec(r))
	}
	if shown == 0 {
		fmt.Fprintln(o.stdout, "(none)")
	}
	warnIfTruncated(o)
	return nil
}

// formatExec renders one exec as a single line.
//
// A failed exec is marked explicitly rather than left to a bare non-zero
// number: it is the more interesting of the two outcomes and must not read as
// noise in a long trace.
func formatExec(r execlog.Record) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  uid=%-5d pid=%-7d ppid=%-7d %s",
		r.TS.UTC().Format("15:04:05"), r.UID, r.PID, r.PPID, cmdline(r))
	if r.Truncated {
		b.WriteString(" …[argv truncated]")
	}
	if r.Ret != 0 {
		fmt.Fprintf(&b, "  [FAILED ret=%d]", r.Ret)
	}
	return b.String()
}

// cmdline joins argv for display, falling back to comm when argv was not
// captured at all.
func cmdline(r execlog.Record) string {
	if len(r.Args) == 0 {
		return r.Comm
	}
	return strings.Join(r.Args, " ")
}

// runWho reports which process reached a host.
func runWho(o opts, args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(o.stderr, "usage: km-netpolicy who <host>")
		return errors.New("who requires a host")
	}
	host := args[0]

	execs, err := execlog.ReadDir(execDir())
	if err != nil {
		return err
	}
	flows, err := flowlog.ReadDir(flowDir())
	if err != nil {
		return err
	}

	hits := execlog.Who(execs, flows, host)
	if len(hits) == 0 {
		fmt.Fprintf(o.stdout, "no recorded flows to %s\n", host)
		return nil
	}

	unattributed := 0
	for _, a := range hits {
		ts := a.Flow.TS.UTC().Format("15:04:05")
		dest := a.Flow.Host
		if dest == "" {
			dest = a.Flow.Addr
		}
		if a.Found {
			fmt.Fprintf(o.stdout, "%s  %-8s %-9s %s  ← pid=%d %s\n",
				ts, a.Flow.Verdict, a.Flow.Src, dest, a.Exec.PID, cmdline(a.Exec))
			continue
		}
		unattributed++
		reason := "no exec recorded for that pid"
		if a.Flow.PID == 0 {
			reason = "no pid on this flow"
		}
		fmt.Fprintf(o.stdout, "%s  %-8s %-9s %s  ← (%s)\n",
			ts, a.Flow.Verdict, a.Flow.Src, dest, reason)
	}

	// Silence here would read as "nothing reached that host", which is the
	// opposite of the truth. Say which half of the join was missing.
	if unattributed == len(hits) {
		fmt.Fprintf(o.stdout,
			"\nNone of these flows could be attributed to a process.\n"+
				"Only the eBPF enforcement path records a pid alongside a flow, so pid\n"+
				"attribution needs spec.network.enforcement: ebpf or both. The exec trace\n"+
				"itself is complete regardless — see `km-netpolicy execs`.\n")
	}
	warnIfTruncated(o)
	return nil
}

// warnIfTruncated says so when rotation has already discarded part of the trace.
//
// One rotation is harmless: the previous generation is still on disk and is
// read. Two means the oldest generation was overwritten, and the trace is
// missing its earliest execs with no other signal that anything was lost.
func warnIfTruncated(o opts) {
	if n := execlog.Rotations(execDir()); n >= 2 {
		fmt.Fprintf(o.stderr,
			"warning: the exec store has rotated %d times; the earliest execs "+
				"(typically the package-install phase) are no longer on disk\n", n)
	}
}
```

- [ ] **Step 4: Add `jsonLine` if the package has no equivalent**

Check `cmd/km-netpolicy/observe.go` for how `flows --json` serialises a record and
reuse that helper. If there is none, add to `execs.go`:

```go
// jsonLine renders a record as one JSON line, matching the on-disk form.
func jsonLine(r execlog.Record) string {
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return string(b)
}
```

and add `"encoding/json"` to the imports.

- [ ] **Step 5: Wire dispatch and usage in `cmd/km-netpolicy/main.go`**

Add to the switch beside the existing `case "capture":`:

```go
	case "execs":
		err = runExecs(o, rest)
	case "who":
		err = runWho(o, rest)
	case "execs-daemon":
		err = runExecsDaemon(o)
```

Add to the usage text, beside the `capture` lines:

```
  execs [--since 10m] [--uid N] [--failed] [--json]
                          what this sandbox executed
  who <host>              which process reached <host>
```

`execs-daemon` is deliberately omitted from usage, exactly as `capture-daemon` is:
it is the unit's entry point, not an operator verb.

- [ ] **Step 6: Run the tests**

Run: `go test ./cmd/km-netpolicy/ -count=1 -v`
Expected: PASS, including the pre-existing verb tests.

- [ ] **Step 7: Commit**

```bash
git add cmd/km-netpolicy/execs.go cmd/km-netpolicy/execs_test.go cmd/km-netpolicy/main.go
git commit -m "feat(km-netpolicy): execs and who verbs over the process trace"
```

---

### Task 7: `execs save` — upload to S3

**Files:**
- Create: `cmd/km-netpolicy/execs_save.go`
- Test: `cmd/km-netpolicy/execs_save_test.go`
- Modify: `cmd/km-netpolicy/execs.go` (route the `save` sub-verb)

**Interfaces:**
- Consumes: `execlog.Path`, `execDir()` (Tasks 1, 6).
- Produces: `runExecsSave(o opts) error`, `execsKey(sandboxID string, now time.Time) string`.

- [ ] **Step 1: Write the failing key test**

Create `cmd/km-netpolicy/execs_save_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestExecsKey_IsScopedToTheSandboxAndTimestamped(t *testing.T) {
	got := execsKey("sb-abc123", time.Date(2026, 8, 29, 15, 4, 5, 0, time.UTC))
	if !strings.HasPrefix(got, "execs/sb-abc123/") {
		t.Errorf("key must be scoped under the sandbox's own prefix: %q", got)
	}
	if !strings.Contains(got, "20260829T150405Z") {
		t.Errorf("key must carry a timestamp so repeat saves do not overwrite: %q", got)
	}
	if !strings.HasSuffix(got, ".jsonl") {
		t.Errorf("key must keep the store's extension: %q", got)
	}
}

func TestRunExecsSave_MissingBucketIsAClearError(t *testing.T) {
	t.Setenv("KM_ARTIFACTS_BUCKET", "")
	t.Setenv("KM_SANDBOX_ID", "sb-abc123")
	t.Setenv("KM_EXEC_DIR", t.TempDir())
	var out, errb bytes.Buffer
	err := runExecsSave(opts{stdout: &out, stderr: &errb})
	if err == nil {
		t.Fatal("want an error when no bucket is configured")
	}
	if !strings.Contains(err.Error(), "KM_ARTIFACTS_BUCKET") {
		t.Errorf("error must name the missing variable, got %v", err)
	}
}

func TestRunExecsSave_EmptyStoreIsNotAnError(t *testing.T) {
	// ExecStop runs this on every graceful shutdown, including boxes that
	// executed nothing. A non-zero exit there would make every clean stop look
	// like a failure in the journal.
	t.Setenv("KM_ARTIFACTS_BUCKET", "bucket")
	t.Setenv("KM_SANDBOX_ID", "sb-abc123")
	t.Setenv("KM_EXEC_DIR", t.TempDir())
	var out, errb bytes.Buffer
	if err := runExecsSave(opts{stdout: &out, stderr: &errb}); err != nil {
		t.Fatalf("empty store must not error: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to save") {
		t.Errorf("want an explicit nothing-to-save line, got %q", out.String())
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/km-netpolicy/ -run TestExecsKey -count=1`
Expected: FAIL — `undefined: execsKey`.

- [ ] **Step 3: Write `cmd/km-netpolicy/execs_save.go`**

Model it on `uploadCapture` in `capture_daemon_linux.go:399`; read that first and
match its config loading and error handling.

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
)

// execsKey is the object key one save lands at.
//
// Scoped under the sandbox's own id, matching the transcripts/ and captures/
// grants, so a compromised sandbox cannot write over another's trace. The
// timestamp means repeat saves accumulate rather than overwrite — the trace
// grows over a box's life and an earlier save may hold records that rotation
// has since discarded.
func execsKey(sandboxID string, now time.Time) string {
	return fmt.Sprintf("execs/%s/execs-%s.jsonl", sandboxID, now.UTC().Format("20060102T150405Z"))
}

// runExecsSave uploads the live store to S3 using the instance role.
//
// Best-effort by construction: it is wired to the unit's ExecStop, so a failure
// here must never turn a clean shutdown into a failed one. The file always stays
// on disk.
func runExecsSave(o opts) error {
	bucket := os.Getenv("KM_ARTIFACTS_BUCKET")
	if bucket == "" {
		return errors.New("KM_ARTIFACTS_BUCKET is not set; cannot save the exec trace")
	}
	sandboxID := os.Getenv("KM_SANDBOX_ID")
	if sandboxID == "" {
		return errors.New("KM_SANDBOX_ID is not set; cannot scope the exec trace")
	}

	path := execlog.Path(execDir())
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(o.stdout, "nothing to save: no exec trace on disk yet")
			return nil
		}
		return err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Size() == 0 {
		fmt.Fprintln(o.stdout, "nothing to save: the exec trace is empty")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}

	key := execsKey(sandboxID, time.Now())
	if _, err := s3.NewFromConfig(cfg).PutObject(ctx, &s3.PutObjectInput{
		Bucket: &bucket,
		Key:    &key,
		Body:   f,
	}); err != nil {
		return fmt.Errorf("upload exec trace: %w", err)
	}

	fmt.Fprintf(o.stdout, "saved: s3://%s/%s (%d bytes)\n", bucket, key, fi.Size())
	return nil
}
```

- [ ] **Step 4: Route the `save` sub-verb**

At the top of `runExecs` in `cmd/km-netpolicy/execs.go`, before the flag parsing:

```go
	// `execs save` is a sub-verb rather than a flag: it writes to S3, and a
	// flag on a read-only listing verb is too easy to trip by accident.
	if len(args) > 0 && args[0] == "save" {
		return runExecsSave(o)
	}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./cmd/km-netpolicy/ -count=1 -v`
Expected: PASS. `TestRunExecsSave_MissingBucketIsAClearError` and
`TestRunExecsSave_EmptyStoreIsNotAnError` both return before any AWS call, so
they need no credentials.

- [ ] **Step 6: Update usage in `main.go`**

```
  execs save              upload the process trace to S3
```

- [ ] **Step 7: Commit**

```bash
git add cmd/km-netpolicy/execs_save.go cmd/km-netpolicy/execs_save_test.go \
        cmd/km-netpolicy/execs.go cmd/km-netpolicy/main.go
git commit -m "feat(km-netpolicy): execs save uploads the process trace to S3"
```

---

### Task 8: Userdata provisioning

**Files:**
- Modify: `pkg/compiler/userdata.go` (store dir, `km-execlog.service`, `userDataParams.ExecLogDir`)
- Test: `pkg/compiler/userdata_execlog_test.go`
- Modify (regenerate): `pkg/compiler/testdata/userdata_additional_volume_only.golden.sh`
- Modify (hand-patch): `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh`

**Interfaces:**
- Consumes: `runExecsDaemon` / `runExecsSave` verb names (Tasks 5, 7).
- Produces: `userDataParams.ExecLogDir` and the rendered unit.

- [ ] **Step 1: Write the failing userdata test**

Create `pkg/compiler/userdata_execlog_test.go`:

```go
package compiler

import "testing"

// Exec capture is unconditional: it only reports what already happened, so its
// presence cannot widen a policy. Gating it on enforcement mode is what made
// Phase 131's flow recording dead under the default, and this test is what
// stops that being re-introduced.
func TestUserData_ExecLogProvisionedInEveryEnforcementMode(t *testing.T) {
	for _, mode := range []string{"proxy", "ebpf", "both"} {
		t.Run(mode, func(t *testing.T) {
			p := testUserDataParams()
			p.Enforcement = mode
			out, err := generateUserData(p)
			if err != nil {
				t.Fatalf("generateUserData: %v", err)
			}
			for _, want := range []string{
				"km-execlog.service",
				"km-netpolicy execs-daemon",
				"systemctl enable --now km-execlog",
				"/var/lib/km/execs",
			} {
				if !contains(out, want) {
					t.Errorf("mode %s: userdata missing %q", mode, want)
				}
			}
		})
	}
}

func TestUserData_ExecStoreIsRootOnly(t *testing.T) {
	// argv is recorded unredacted and includes root's. A sandbox-readable store
	// would be an information leak on exactly the unprivileged profiles that
	// are supposed to be the tighter ones.
	out, err := generateUserData(testUserDataParams())
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	if !contains(out, "chmod 700 /var/lib/km/execs") {
		t.Error("exec store directory must be 0700 root-only")
	}
	if contains(out, "chmod 1777 /var/lib/km/execs") || contains(out, "chmod 777 /var/lib/km/execs") {
		t.Error("exec store must not be world-writable like the flow store")
	}
}

func TestUserData_ExecLogUnitCarriesRegionAndSavesOnStop(t *testing.T) {
	out, err := generateUserData(testUserDataParams())
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	// Without AWS_REGION the SDK fails with "Invalid region: region was not a
	// valid DNS name", which reads as a bucket problem. That was the km-capture
	// bug fixed in 216d4664 and it is not being re-introduced here.
	if !contains(out, "Environment=AWS_REGION=") {
		t.Error("km-execlog.service must carry AWS_REGION")
	}
	if !contains(out, "ExecStop=/opt/km/bin/km-netpolicy execs save") {
		t.Error("a graceful stop must save the trace without anyone remembering")
	}
}
```

If `testUserDataParams` and `contains` do not already exist in the package's
tests, use whatever the neighbouring userdata tests use — check
`pkg/compiler/userdata_test.go` and reuse its constructor verbatim rather than
adding a second one.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./pkg/compiler/ -run TestUserData_ExecLog -count=1`
Expected: FAIL — the strings are absent from the rendered userdata.

- [ ] **Step 3: Add the template parameter**

In `pkg/compiler/userdata.go`, in the `userDataParams` struct beside `CaptureDir`:

```go
	// ExecLogDir is where the exec tracer writes the process trace. Root-only:
	// argv is recorded unredacted and includes root's own.
	ExecLogDir string
```

Set its default wherever `CaptureDir` is defaulted, to `/var/lib/km/execs`.

- [ ] **Step 4: Provision the directory**

In the unconditional block that already creates `{{ .FlowLogDir }}` and
`{{ .CaptureDir }}` (near `userdata.go:1373`), add:

```bash
# Exec trace store. Unlike the flow directory this has exactly ONE producer —
# the root exec daemon — so it needs none of the 1777 multi-writer handling.
# It is root-only for a stronger reason: argv is recorded unredacted and
# includes root's, so a sandbox-readable store would leak on exactly the
# unprivileged profiles that are meant to be the tighter ones.
mkdir -p {{ .ExecLogDir }}
chmod 700 {{ .ExecLogDir }}
```

- [ ] **Step 5: Render the unit**

Immediately after the `km-capture.service` block (which ends `systemctl enable --now km-capture`):

```bash
# Exec trace daemon: a hidden verb of the same km-netpolicy binary, for the
# same reason capture is — a binary userdata fetches but km init does not
# upload 404s at boot and, under set -e, aborts the whole bootstrap.
#
# Provisioned UNCONDITIONALLY, outside the enforcement conditional. The eBPF
# enforcer is gated on enforcement mode, so a tracer living inside it would be
# silently dead under `proxy`. Observation that only reports what already
# happened cannot widen a policy by being present.
cat > /etc/systemd/system/km-execlog.service << 'UNIT'
[Unit]
Description=km process execution tracer
After=network-online.target
[Service]
Type=simple
User=root
Environment=KM_EXEC_DIR={{ .ExecLogDir }}
Environment=KM_ARTIFACTS_BUCKET={{ .KMArtifactsBucket }}
Environment=KM_SANDBOX_ID={{ .SandboxID }}
# Same reason km-capture.service carries it: without a region the SDK cannot
# resolve an S3 endpoint and fails with "Invalid region", which reads as a
# bucket problem rather than a config one.
Environment=AWS_REGION={{ .AWSRegion }}
ExecStart=/opt/km/bin/km-netpolicy execs-daemon
# A graceful stop saves the trace, so km stop / km pause / an instance
# terminate do not need anyone to have remembered. Best-effort: never blocks
# the stop, and a hard power-off still loses the unsaved tail.
ExecStop=/opt/km/bin/km-netpolicy execs save
Restart=always
RestartSec=2
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now km-execlog
```

- [ ] **Step 6: Run the new tests**

Run: `go test ./pkg/compiler/ -run TestUserData_ExecLog -count=1 -v`
Expected: PASS — all three, including all three enforcement modes.

- [ ] **Step 7: Regenerate the capturable goldens**

Run:
```bash
CAPTURE_ADDVOL_GOLDEN=1 go test ./pkg/compiler/ -run TestUserData -count=1
CAPTURE_PRE_H1_BASELINE=1 go test ./pkg/compiler/ -run H1 -count=1
CAPTURE_LOCAL_CODEX_GOLDEN=1 go test ./pkg/compiler/ -run Codex -count=1
git diff --stat pkg/compiler/testdata/
```
Expected: the diffs are purely additive — the new `mkdir`/`chmod` pair and the
unit block. Read the diff and confirm nothing else moved.

- [ ] **Step 8: Hand-patch the FROZEN pre-92 baseline**

**Do NOT run `CAPTURE_PRE92_BASELINE=1`.** That flag writes the UNSTRIPPED
output and folds the post-baseline SubagentStop script into a golden the
byte-identity test deliberately strips, corrupting it. This is a known trap that
has already cost time once.

Instead, edit `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh`
by hand, inserting exactly the same two hunks the other goldens gained, at the
same positions relative to their surrounding lines.

Then verify:
```bash
go test ./pkg/compiler/ -run Phase92ByteIdentity -count=1 -v
```
Expected: PASS.

- [ ] **Step 9: Full package run**

Run: `go test ./pkg/compiler/ -count=1`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add pkg/compiler/userdata.go pkg/compiler/userdata_execlog_test.go pkg/compiler/testdata/
git commit -m "feat(userdata): provision the exec store and km-execlog.service unconditionally"
```

---

### Task 9: IAM — `ec2spot/v1.6.0`

**Files:**
- Create: `infra/modules/ec2spot/v1.6.0/` (full copy of `v1.5.0`, one statement changed)
- Modify: `infra/templates/sandbox/terragrunt.hcl` (`locals.substrate_module_versions`)
- Test: `pkg/compiler/ec2spot_timeout_test.go` (the existing pin-drift guard)

**Interfaces:**
- Consumes: `execsKey`'s prefix shape from Task 7 — the grant and the key must agree on `execs/${sandbox_id}/*`.
- Produces: the new module version pin.

- [ ] **Step 1: Copy the module**

Run:
```bash
cp -R infra/modules/ec2spot/v1.5.0 infra/modules/ec2spot/v1.6.0
```

Module directories are immutable. `v1.5.0` must not be touched.

- [ ] **Step 2: Add the grant**

In `infra/modules/ec2spot/v1.6.0/main.tf`, in the `Resource` list that already
holds `transcripts/`, `captures/` and `learn/` (around line 566), add:

```hcl
          # Phase 132: the process trace `km-netpolicy execs save` uploads,
          # scoped to this sandbox's own prefix exactly as its siblings are.
          "arn:aws:s3:::${var.artifacts_bucket}/execs/${var.sandbox_id}/*",
```

- [ ] **Step 3: Bump the pin**

In `infra/templates/sandbox/terragrunt.hcl`, change the `ec2spot` entry in
`locals.substrate_module_versions` from `v1.5.0` to `v1.6.0`.

- [ ] **Step 4: Verify the drift guard still passes**

`TestEC2SpotModuleDir_TracksLivePin` parses the pin out of the sandbox template
and asserts the tests read the same version. It exists because
`ec2spot_timeout_test.go` silently read `v1.2.0` while the live pin was `v1.3.0`
for a whole phase, making every assertion in it inert.

Run: `go test ./pkg/compiler/ -run EC2Spot -count=1 -v`
Expected: PASS. If it fails naming a version, the test's own constant needs the
same bump — that is exactly what the guard is for.

- [ ] **Step 5: Validate the Terraform**

Run:
```bash
terraform -chdir=infra/modules/ec2spot/v1.6.0 fmt -check
terraform -chdir=infra/modules/ec2spot/v1.6.0 init -backend=false && \
terraform -chdir=infra/modules/ec2spot/v1.6.0 validate
```
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add infra/modules/ec2spot/v1.6.0 infra/templates/sandbox/terragrunt.hcl \
        pkg/compiler/ec2spot_timeout_test.go
git commit -m "feat(ec2spot): v1.6.0 grants execs/ so the process trace can be saved"
```

---

### Task 10: The VulnHunter profile, docs, and the live UAT

**Files:**
- Create: `profiles/vulnhunt.yaml`
- Create: `docs/exec-capture.md`
- Create: `.planning/phases/132-exec-capture/132-UAT.md`
- Modify: `CLAUDE.md` (phase block + the `km-netpolicy` row in the helper table + the "Where to look" table)
- Modify: `.planning/ROADMAP.md`

**Interfaces:**
- Consumes: everything above.
- Produces: nothing code depends on.

- [ ] **Step 1: Write `profiles/vulnhunt.yaml`**

Start from `profiles/dc34.yaml`'s `extends:` list — it already composes the six
fragments this needs, already sets `enforcement: both` via
`base/network/safenetwork`, and already clones `defcon.run.34`.

```yaml
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: vulnhunt
  labels:
    tier: security
    tool: vulnhunter
  prefix: vulnhunt

extends:
  - base/network/safenetwork      # enforcement: both — required for `who` to attribute
  - base/sidecars-all
  - base/observability-learn
  - base/budget-standard
  - base/artifacts-workspace
  - base/iam-us-east-1
  - base/agent-claude-all-tools

spec:
  lifecycle:
    ttl: "8h"
    idleTimeout: "1h"
    teardownPolicy: stop

  runtime:
    substrate: ec2
    spot: false
    instanceType: t3.large
    region: us-east-1
    rootVolumeSize: 30
    ami: amazon-linux-2023

  execution:
    shell: /bin/bash
    workingDir: /workspace
    # Bedrock rather than the first-party API: VulnHunter's own README warns
    # that running dual-use vulnerability analysis against an unenrolled
    # Anthropic account may be blocked by real-time cyber safeguards. Bedrock
    # is not a first-party platform, so that concern does not apply.
    useBedrock: true
    privileged: true
    initCommands:
      - "yum install -y git nodejs npm python3 python3-pip jq tar gzip tmux"
      - "npm install -g @anthropic-ai/claude-code@2.1.114"
      - "mkdir -p /workspace && chown -R sandbox:sandbox /workspace"
      - "su - sandbox -c 'git clone --depth 1 https://github.com/whereiskurt/defcon.run.34 /workspace/defcon.run.34'"
      - "su - sandbox -c 'git clone --depth 1 https://github.com/capitalone/VulnHunter /workspace/VulnHunter'"
      - "su - sandbox -c 'mkdir -p /home/sandbox/.claude/skills && cp -r /workspace/VulnHunter/vulnhunt /home/sandbox/.claude/skills/vulnhunt'"

  sourceAccess:
    mode: allowlist
    github:
      allowedRepos:
        - whereiskurt/defcon.run.34
        - capitalone/VulnHunter
      allowedRefs:
        - "*"

  cli:
    noBedrock: false

  notification:
    events:
      onIdle: true
    slack:
      enabled: true
      perSandbox: true
      channelName: "sb-{profile}-{alias}"
      archiveOnDestroy: true
      inbound:
        enabled: true
```

- [ ] **Step 2: Validate it**

Run:
```bash
make build
./bin/km validate profiles/vulnhunt.yaml
./scripts/validate-all-profiles.sh
```
Expected: both pass. `validate-all-profiles.sh` is the single-source-of-truth
gate over the profile inventory; if it counts files, add the new one to its list.

- [ ] **Step 3: Write `docs/exec-capture.md`**

Cover, in this order: what the trace is and where it lives; the four verbs with
worked output; **the `who`-under-`proxy` limitation stated plainly, in the same
voice as Phase 131's `transparent.go` gap**, including the operator's 2026-08-29
decision to accept it because `proxy` is the Docker path; the root-only store and
why (unredacted argv includes root's); the 20×128 argv bound and what `truncated`
means; rotation and what two rotations cost; `execs save`, `ExecStop`, and the
honest statement that a hard power-off loses the unsaved tail; the deploy surface
from spec §9 including the do-not-split warning; and troubleshooting — empty
trace, `journalctl -u km-execlog`, and `execs --failed` as the first thing to
look at.

- [ ] **Step 4: Write the UAT**

Create `.planning/phases/132-exec-capture/132-UAT.md` with the seven checks from
spec §8, each with the exact command and its expected output:

1. `km-netpolicy execs | head -50` is non-empty after a VulnHunter run.
2. `km-netpolicy who api.anthropic.com` (or a host the run actually reached, read
   off `km-netpolicy observed`) names a real process.
3. `km-netpolicy execs --uid 0 | head` shows sudo'd execs.
4. `km-netpolicy execs save` prints an `s3://` URI; `aws s3 ls` confirms it.
5. `systemctl stop km-execlog` then `journalctl -u km-execlog | tail` shows the
   ExecStop save ran; a second object exists in S3.
6. `km-netpolicy execs --since 10m` stays responsive, and
   `cat /var/lib/km/execs/execs.jsonl.rotations` reports a plausible count.
7. On a `profiles/uat-proxy-census.yaml` box, `who <host>` prints the explanation
   naming `ebpf`/`both` rather than `(none)`.

- [ ] **Step 5: Update `CLAUDE.md`**

Add a Phase 132 block in the house style of the Phase 131 block above it. It must
carry, at minimum: the enforcement-gating trap and why the daemon is separate; the
root-only store and the two decisions that force it; the `who`-under-`proxy`
limitation plus the operator's acceptance; the `sched_process_exit`/pid-reuse
reasoning; the `AWS_REGION` and do-not-split deploy warnings; and the recorded
`pkg/jsonlstore` debt from spec §11.

Add `execs`, `who`, and `execs save` to the `km-netpolicy` row of the
sandbox-side helper table, and a "Where to look" row pointing at
`docs/exec-capture.md`.

- [ ] **Step 6: Update `.planning/ROADMAP.md`**

Add the Phase 132 entry in the same shape as Phase 130's, listing the ten tasks
of this plan as its plans.

- [ ] **Step 7: Final whole-branch verification**

Run:
```bash
go build ./... && go vet ./...
go test ./... -count=1 2>&1 | tee /tmp/132-tests.log; echo "exit=$?"
grep -c '^FAIL' /tmp/132-tests.log
git diff --stat main -- go.mod go.sum
```
Expected: build and vet clean; the only failures are the pre-existing
`internal/app/cmd` `TestBootstrap*`/`TestCluster*` AWS fast-fail-seam tests that
also fail on `main`; and **the `go.mod`/`go.sum` diff is empty**.

Check the test command's own exit code, not a pipeline's — `go test | tail`
returns tail's zero and hides a FAIL.

- [ ] **Step 8: Commit**

```bash
git add profiles/vulnhunt.yaml docs/exec-capture.md \
        .planning/phases/132-exec-capture/132-UAT.md CLAUDE.md .planning/ROADMAP.md
git commit -m "docs(exec-capture): operator runbook, vulnhunt UAT profile, phase record"
```

---

## Wave Structure

Tasks within a wave are independent and may run in parallel; a wave starts only
when the previous one is green.

| Wave | Tasks | Rationale |
|---|---|---|
| 1 | 1, 3 | The store and the BPF object share no code. **Task 3 Step 5 resolves the container-attach unknown; run it early.** |
| 2 | 2, 4 | The join needs Task 1's `Record`; the tracer needs Tasks 1 and 3. |
| 3 | 5, 6, 7 | All three are `cmd/km-netpolicy` verbs over the finished packages. They touch `main.go`'s dispatch — **serialize the `main.go` edits or they will collide.** |
| 4 | 8, 9 | Userdata and Terraform, independent of the Go work and of each other. |
| 5 | 10 | Docs, profile, UAT — needs everything above to describe it accurately. |

## Self-Review

**Spec coverage.** §3.1 → Task 3. §3.2 → Tasks 3, 5, 8 (unconditional
provisioning + the enforcement-mode test). §3.3 → Tasks 3, 6 (uid captured,
`--uid` filter). §3.4 → Task 3 (bounds, `truncated`). §4.1 → Tasks 3, 4. §4.2 →
Tasks 1, 8 (0700/0600 in both the writer and the userdata test). §4.3 → Tasks 6,
7. §4.4 → Task 8. §4.5 → Task 9. §5 → Task 2 (pid reuse) and Task 6 (the
explanation). §6 → Task 1. §7 → Tasks 1, 2, 4 Step 5. §8 → Task 10. §9 → Task 10
Step 5. §10 → Task 10 Step 3. §11 → Task 10 Step 5. No gaps.

**Known rough edge, deliberately left.** Task 4 Step 2's listing contains two
placeholder declarations (`attach`, `tp`) that exist only to keep the code
readable before `attachAll` is introduced; Step 3 says explicitly to delete them.
An executor working Step 2 in isolation will produce a file that does not vet
until Step 3 lands. This is called out rather than silently fixed because the
alternative — inlining `attachAll` into Step 2 — makes a 90-line step.

**Type consistency.** `execlog.Record` is the single record type across Tasks 1,
2, 4, 5, 6, 7. `opts` with `stdout`/`stderr` matches the existing
`cmd/km-netpolicy` verbs. `execDir()`/`flowDir()` are defined once in Task 6 and
used by Task 7. `rawHdr` is pinned to the C struct by a size assertion. The
`execs/${sandbox_id}/*` prefix appears in Task 7's `execsKey` and Task 9's IAM
grant and they agree.
