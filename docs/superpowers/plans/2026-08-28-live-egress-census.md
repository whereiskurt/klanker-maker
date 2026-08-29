# Live Egress Census, Allowlist Pinning, Packet Capture — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a running sandbox report every destination it has reached, collapse a wide-open allowlist down to only those destinations, and capture packets on demand.

**Architecture:** Three existing egress producers (eBPF audit consumer, DNS proxy, HTTP proxy) each append JSONL flow records to their own file under `/var/lib/km/flows/`; `km-netpolicy` merges them at read time. A new kernel-append-only `allow.pins` file mirrors the shipped `deny.list`, and every consumer of the deny store gains a matching pin read. Packet capture runs as a root daemon exposing a Unix socket, shipped as a hidden verb of the `km-netpolicy` binary that already exists on every sandbox.

**Tech Stack:** Go 1.x, `golang.org/x/sys/unix` (AF_PACKET), `golang.org/x/net/bpf` (classic BPF filters), `golang.org/x/net/publicsuffix` (via existing `allowlistgen`), zerolog, systemd, Terraform/Terragrunt.

**Spec:** `docs/superpowers/specs/2026-08-28-live-egress-census-design.md` — read it before Task 1. Locked decisions 1–7 in that document are not open for re-litigation during execution.

## Global Constraints

- **`CGO_ENABLED=0` always.** Sidecars cross-compile to `linux/amd64` from macOS. No cgo, no libpcap, no new module dependencies beyond what `go.mod` already has.
- **No new module dependencies.** `golang.org/x/sys`, `golang.org/x/net`, `github.com/cilium/ebpf`, and `github.com/rs/zerolog` are already direct deps. Adding anything else means the design was misread.
- **Linux-only code gets `//go:build linux`** plus a stub for other platforms, following `pkg/ebpf/enforcer_stub.go`.
- **Narrowing must stay structural, never a trusted rule.** Denies union; pins intersect. Neither has a removal verb. If a step seems to need one, stop and re-read the spec.
- **`km-netpolicy` is on every sandbox unconditionally** as of v0.8.8 (commit `0c7f9880`). Do not add or reinstate a profile gate.
- **Terraform module directories are immutable.** Never edit `infra/modules/ec2spot/v1.4.0`; create `v1.5.0`.
- **Every task ends green.** Run `go build ./... && go test ./<touched packages>/...` before each commit and confirm the command's own exit status — `go test | tail` returns tail's exit code and hides failures.
- **Commit messages:** conventional-commit prefix, and end with the `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>` and `Claude-Session:` trailers used by this repo.

---

### Task 1: `pkg/flowlog` — record type and rotating writer

**Files:**
- Create: `pkg/flowlog/record.go`
- Create: `pkg/flowlog/writer.go`
- Test: `pkg/flowlog/writer_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `flowlog.Record` struct; `flowlog.NewWriter(path string, maxBytes int64) *Writer`; `(*Writer).Write(Record) error`; `(*Writer).Close() error`; constants `SrcEBPF`/`SrcDNS`/`SrcHTTP` and `VerdictAllow`/`VerdictDeny`/`VerdictRedirect`; `flowlog.DefaultDir`, `flowlog.DefaultMaxBytes`.

- [ ] **Step 1: Write the failing test**

Create `pkg/flowlog/writer_test.go`:

```go
package flowlog_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

func TestWriter_AppendsJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flows.dns.jsonl")
	w := flowlog.NewWriter(path, 1<<20)
	defer w.Close()

	rec := flowlog.Record{
		TS:      time.Date(2026, 8, 28, 14, 2, 11, 0, time.UTC),
		Src:     flowlog.SrcDNS,
		Verdict: flowlog.VerdictAllow,
		Host:    "api.github.com",
	}
	if err := w.Write(rec); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Write(rec); err != nil {
		t.Fatalf("Write: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), body)
	}
	if !strings.Contains(lines[0], `"host":"api.github.com"`) {
		t.Errorf("line missing host: %s", lines[0])
	}
	// Fields the producer did not observe must be omitted entirely, never
	// emitted as zero values that a reader would mistake for observations.
	if strings.Contains(lines[0], `"addr"`) || strings.Contains(lines[0], `"pid"`) {
		t.Errorf("unobserved fields must be omitted, got: %s", lines[0])
	}
}

func TestWriter_RotatesAtCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flows.dns.jsonl")
	// Cap small enough that the second record forces a rotation.
	w := flowlog.NewWriter(path, 80)
	defer w.Close()

	rec := flowlog.Record{TS: time.Now().UTC(), Src: flowlog.SrcDNS, Verdict: flowlog.VerdictAllow, Host: "example.com"}
	for i := 0; i < 6; i++ {
		if err := w.Write(rec); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated file %s.1: %v", path, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat live file: %v", err)
	}
	if fi.Size() > 80 {
		t.Errorf("live file %d bytes exceeds cap 80", fi.Size())
	}
}

func TestWriter_UnwritableDirDoesNotPanic(t *testing.T) {
	// A producer must never die because the flow store is unavailable.
	w := flowlog.NewWriter("/nonexistent-dir-abc/flows.dns.jsonl", 1<<20)
	defer w.Close()
	if err := w.Write(flowlog.Record{TS: time.Now(), Src: flowlog.SrcDNS, Verdict: flowlog.VerdictAllow, Host: "x.example"}); err == nil {
		t.Error("want an error surfaced to the caller for a bad path")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/flowlog/ -run TestWriter -v`
Expected: FAIL — package `flowlog` does not exist.

- [ ] **Step 3: Write `pkg/flowlog/record.go`**

```go
// Package flowlog holds the egress-observation vocabulary shared by the DNS
// proxy, the HTTP proxy, the eBPF audit consumer, and the on-box km-netpolicy
// helper.
//
// It is the read side of what pkg/netpolicy is the write side of: netpolicy
// decides what a sandbox may reach, flowlog records what it actually reached.
//
// Each producer owns exactly one file and appends to it. Nothing merges at
// write time. A single shared file would put three writers in a rotation race;
// a socket daemon would add a process whose death silently loses records.
// Per-producer files also degrade honestly — a dead producer reads as "that
// source is missing", never as a corrupt store.
package flowlog

import "time"

// Producer identifiers. These name the observing component, not the protocol:
// a CONNECT to :443 seen by the HTTP proxy is SrcHTTP even though the payload
// is TLS.
const (
	SrcEBPF = "ebpf"
	SrcDNS  = "dns"
	SrcHTTP = "http"
)

// Verdicts, matching the eBPF ActionDeny/Allow/Redirect vocabulary so a record
// means the same thing regardless of which producer wrote it.
const (
	VerdictAllow    = "allow"
	VerdictDeny     = "deny"
	VerdictRedirect = "redirect"
)

// DefaultDir is where producers write and km-netpolicy reads.
//
// Under /var/lib rather than /run for the same reason the deny list is: /run is
// a tmpfs cleared on boot, and a census silently emptied by a reboot would
// invite an operator to pin a set far narrower than the box actually needs.
const DefaultDir = "/var/lib/km/flows"

// DefaultMaxBytes caps each producer's live file before rotation. One previous
// generation is retained, so a producer costs at most 2x this on disk.
const DefaultMaxBytes int64 = 16 << 20 // 16 MiB

// Record is one observed egress decision.
//
// Producers fill only what they observed and omit the rest: the DNS proxy knows
// a name and a verdict but not the eventual peer; the HTTP proxy knows host,
// port and verdict; the eBPF path knows address, port, pid and comm but not the
// name. Every optional field is omitempty because a zero value here would be
// indistinguishable from an observation of zero, and an operator pins against
// this data.
type Record struct {
	TS      time.Time `json:"ts"`
	Src     string    `json:"src"`
	Verdict string    `json:"verdict"`
	Host    string    `json:"host,omitempty"`
	Addr    string    `json:"addr,omitempty"`
	Port    int       `json:"port,omitempty"`
	Proto   string    `json:"proto,omitempty"`
	PID     int       `json:"pid,omitempty"`
	Comm    string    `json:"comm,omitempty"`
}

// FileFor returns the conventional file path for a producer within dir.
func FileFor(dir, src string) string { return dir + "/flows." + src + ".jsonl" }
```

- [ ] **Step 4: Write `pkg/flowlog/writer.go`**

```go
package flowlog

import (
	"encoding/json"
	"os"
	"sync"
)

// Writer appends records to one producer's file, rotating at a size cap.
//
// Safe for concurrent use. Callers are egress hot paths, so the mutex is held
// only across the append itself.
type Writer struct {
	path     string
	maxBytes int64

	mu   sync.Mutex
	f    *os.File
	size int64
}

// NewWriter returns a Writer for path. The file is opened lazily on first
// Write so constructing one is safe before the directory exists.
func NewWriter(path string, maxBytes int64) *Writer {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Writer{path: path, maxBytes: maxBytes}
}

// Write appends one record as a JSON line.
//
// The error is returned for tests and for a producer that wants to log it once.
// Producers MUST NOT treat it as fatal: losing observability is strictly better
// than dropping an egress decision the sandbox is waiting on.
func (w *Writer) Write(r Record) error {
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

// openLocked opens the live file if it is not already open, creating the
// directory. Caller holds w.mu.
func (w *Writer) openLocked() error {
	if w.f != nil {
		return nil
	}
	if err := os.MkdirAll(dirOf(w.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
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
// Exactly one previous generation is kept; the older ".1" is replaced. Caller
// holds w.mu.
func (w *Writer) rotateLocked() error {
	if err := w.f.Close(); err != nil {
		return err
	}
	w.f = nil
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
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

// dirOf returns everything before the final "/", or "." when there is none.
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

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./pkg/flowlog/ -v`
Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
git add pkg/flowlog/
git commit -m "feat(flowlog): record type and rotating per-producer writer"
```

---

### Task 2: `pkg/flowlog` — reader, merge, and census

**Files:**
- Create: `pkg/flowlog/reader.go`
- Create: `pkg/flowlog/census.go`
- Test: `pkg/flowlog/reader_test.go`
- Test: `pkg/flowlog/census_test.go`

**Interfaces:**
- Consumes: `flowlog.Record`, `flowlog.FileFor` (Task 1).
- Produces: `flowlog.ReadDir(dir string) ([]Record, error)`; `flowlog.Dest` struct; `flowlog.Census` struct with `Allowed []Dest` and `Denied []Dest`; `flowlog.Summarize(recs []Record) Census`.

**Note on name↔address correlation.** The spec calls it best-effort, and it
happens at WRITE time, not here. The eBPF enforcer holds both the resolver's
name map and the flow writer in the same process, so the eBPF producer fills
`Host` itself (Task 9, via `resolver.Allowlist.NameForIP`). There is
deliberately no read-time correlation function: exporting the resolver's map
across a process boundary just to re-join it later would add a second source of
truth for something one process already knows.

- [ ] **Step 1: Write the failing tests**

Create `pkg/flowlog/reader_test.go`:

```go
package flowlog_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

func TestReadDir_MergesProducersAndGenerations(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("flows.dns.jsonl", `{"ts":"2026-08-28T14:00:00Z","src":"dns","verdict":"allow","host":"a.example"}`+"\n")
	write("flows.dns.jsonl.1", `{"ts":"2026-08-28T13:00:00Z","src":"dns","verdict":"allow","host":"old.example"}`+"\n")
	write("flows.http.jsonl", `{"ts":"2026-08-28T14:01:00Z","src":"http","verdict":"deny","host":"b.example","port":443}`+"\n")

	recs, err := flowlog.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("want 3 records, got %d", len(recs))
	}
	// Sorted oldest-first so `flows --since` and display order are stable.
	if recs[0].Host != "old.example" {
		t.Errorf("want oldest first, got %q", recs[0].Host)
	}
}

func TestReadDir_SkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	body := `{"ts":"2026-08-28T14:00:00Z","src":"dns","verdict":"allow","host":"good.example"}` + "\n" +
		"this is not json\n" +
		`{"ts":"2026-08-28T14:00:01Z","src":"dns","verdict":"allow","host":"also-good.example"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "flows.dns.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	recs, err := flowlog.ReadDir(dir)
	if err != nil {
		t.Fatalf("a corrupt line must not fail the read: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 good records, got %d", len(recs))
	}
}

func TestReadDir_MissingDirIsEmptyNotError(t *testing.T) {
	recs, err := flowlog.ReadDir(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("absent dir is the pre-first-flow state, not an error: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("want 0 records, got %d", len(recs))
	}
}
```

Create `pkg/flowlog/census_test.go`:

```go
package flowlog_test

import (
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

func recAt(min int, verdict, host string) flowlog.Record {
	return flowlog.Record{
		TS:      time.Date(2026, 8, 28, 14, min, 0, 0, time.UTC),
		Src:     flowlog.SrcHTTP,
		Verdict: verdict,
		Host:    host,
	}
}

func TestSummarize_SeparatesAllowedFromDenied(t *testing.T) {
	c := flowlog.Summarize([]flowlog.Record{
		recAt(0, flowlog.VerdictAllow, "api.github.com"),
		recAt(1, flowlog.VerdictAllow, "api.github.com"),
		recAt(2, flowlog.VerdictDeny, "evil.example.com"),
	})

	if len(c.Allowed) != 1 || c.Allowed[0].Host != "api.github.com" {
		t.Fatalf("allowed: %+v", c.Allowed)
	}
	if c.Allowed[0].Count != 2 {
		t.Errorf("want count 2, got %d", c.Allowed[0].Count)
	}
	if len(c.Denied) != 1 || c.Denied[0].Host != "evil.example.com" {
		t.Fatalf("denied: %+v", c.Denied)
	}
}

func TestSummarize_DeniedNeverAppearsInAllowed(t *testing.T) {
	// A host both allowed and later denied must still be pinnable — it WAS
	// reachable. But a host only ever denied must never leak into Allowed,
	// because Allowed is what pin intersects against.
	c := flowlog.Summarize([]flowlog.Record{
		recAt(0, flowlog.VerdictDeny, "blocked.example"),
	})
	for _, d := range c.Allowed {
		if d.Host == "blocked.example" {
			t.Fatal("a never-allowed host leaked into the pinnable set")
		}
	}
}

func TestSummarize_RedirectCountsAsAllowed(t *testing.T) {
	// MITM interception is how an allowed host is reached, not a block.
	c := flowlog.Summarize([]flowlog.Record{
		recAt(0, flowlog.VerdictRedirect, "bedrock-runtime.us-east-1.amazonaws.com"),
	})
	if len(c.Allowed) != 1 {
		t.Fatalf("want redirect treated as allowed, got %+v", c.Allowed)
	}
}

func TestSummarize_AddressOnlyAndNamedAreDistinctDests(t *testing.T) {
	// An eBPF record whose address the resolver could not name stays IP-only.
	// It must not collapse into a named destination, because an operator
	// deciding what to pin has to see that something went somewhere unnamed.
	c := flowlog.Summarize([]flowlog.Record{
		{TS: time.Now(), Src: flowlog.SrcEBPF, Verdict: flowlog.VerdictAllow, Host: "api.github.com", Addr: "140.82.113.6"},
		{TS: time.Now(), Src: flowlog.SrcEBPF, Verdict: flowlog.VerdictAllow, Addr: "203.0.113.9"},
	})
	if len(c.Allowed) != 2 {
		t.Fatalf("want 2 distinct destinations, got %+v", c.Allowed)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/flowlog/ -run 'TestReadDir|TestSummarize' -v`
Expected: FAIL — `ReadDir` and `Summarize` undefined.

- [ ] **Step 3: Write `pkg/flowlog/reader.go`**

```go
package flowlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// ReadDir reads every producer file in dir (live files and their one rotated
// generation) and returns all records sorted oldest-first.
//
// It never fails on bad data. A missing directory is the normal state of a box
// that has not yet made an egress decision; a corrupt line is skipped. The only
// error returned is a genuine directory-listing failure, because that means the
// caller is being told about a store it cannot see at all.
func ReadDir(dir string) ([]Record, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "flows.*.jsonl"))
	if err != nil {
		return nil, err
	}
	rotated, err := filepath.Glob(filepath.Join(dir, "flows.*.jsonl.1"))
	if err != nil {
		return nil, err
	}
	matches = append(matches, rotated...)

	var out []Record
	for _, path := range matches {
		out = append(out, readFile(path)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS.Before(out[j].TS) })
	return out, nil
}

// readFile parses one JSONL file, skipping anything unparseable. An unreadable
// file yields no records rather than an error: one dead producer must not
// blind the operator to the other two.
func readFile(path string) []Record {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []Record
	sc := bufio.NewScanner(f)
	// Records are small, but a corrupt file can present one enormous "line";
	// cap it rather than letting the scanner allocate without bound.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue
		}
		if r.Src == "" || r.Verdict == "" {
			continue
		}
		out = append(out, r)
	}
	return out
}
```

- [ ] **Step 4: Write `pkg/flowlog/census.go`**

```go
package flowlog

import (
	"sort"
	"time"
)

// Dest is one destination in the census, with how often it was seen and when
// it was last seen.
type Dest struct {
	Host  string
	Addr  string
	Count int
	Last  time.Time
}

// Key is what a destination is deduplicated by: its name when one was
// observed, otherwise its address.
func (d Dest) Key() string {
	if d.Host != "" {
		return d.Host
	}
	return d.Addr
}

// Census is the deduplicated view of where a sandbox has been.
//
// Allowed and Denied are kept strictly apart. Allowed is the set pin intersects
// against, so a destination that was never permitted must not appear in it —
// pinning a denied host would be an attempt to widen policy through the census.
type Census struct {
	Allowed []Dest
	Denied  []Dest
}

// Summarize collapses records into a census, sorted by descending count so the
// destinations that dominate a session appear first.
//
// VerdictRedirect counts as allowed: MITM interception is how an allowed host
// is reached (Bedrock, Anthropic, OpenAI metering), not a block.
func Summarize(recs []Record) Census {
	allowed := map[string]*Dest{}
	denied := map[string]*Dest{}

	for _, r := range recs {
		bucket := allowed
		if r.Verdict == VerdictDeny {
			bucket = denied
		}
		d := Dest{Host: r.Host, Addr: r.Addr}
		key := d.Key()
		if key == "" {
			continue
		}
		cur, ok := bucket[key]
		if !ok {
			cur = &Dest{Host: r.Host, Addr: r.Addr}
			bucket[key] = cur
		}
		// A later record may carry a name for a destination first seen as bare
		// address; keep the richer view.
		if cur.Host == "" && r.Host != "" {
			cur.Host = r.Host
		}
		if cur.Addr == "" && r.Addr != "" {
			cur.Addr = r.Addr
		}
		cur.Count++
		if r.TS.After(cur.Last) {
			cur.Last = r.TS
		}
	}

	return Census{Allowed: flatten(allowed), Denied: flatten(denied)}
}

// flatten turns the accumulator map into a slice sorted by descending count,
// then by key so output is deterministic for equal counts.
func flatten(m map[string]*Dest) []Dest {
	out := make([]Dest, 0, len(m))
	for _, d := range m {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key() < out[j].Key()
	})
	return out
}

// NameFn resolves an address to the name that was looked up to reach it.
//
// The eBPF enforcer supplies one backed by the resolver's own map, and nothing
// else does. There is deliberately no reverse-DNS lookup and no heuristic
// inference behind this: a wrong name on a flow record is worse than an absent
// one, because the operator pins against this data and would be pinning a
// destination the box never actually reached. A nil NameFn, or one that returns
// "", leaves the record IP-only — which is the honest answer.
type NameFn func(addr string) string
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./pkg/flowlog/ -v`
Expected: PASS (all tests from Tasks 1 and 2).

- [ ] **Step 6: Commit**

```bash
git add pkg/flowlog/
git commit -m "feat(flowlog): merge reader, census summarizer, resolver-only correlation"
```

---

### Task 3: `pkg/netpolicy` — pin store and intersection algebra

**Files:**
- Create: `pkg/netpolicy/pins.go`
- Test: `pkg/netpolicy/pins_test.go`
- Modify: `pkg/netpolicy/netpolicy.go` (append `MatchAllow`)

**Interfaces:**
- Consumes: `netpolicy.ParseLine`, `netpolicy.Match` (existing).
- Produces: `netpolicy.MatchAllow(host string, patterns []string) bool`; `netpolicy.PinStore` with `NewPinStore(path string, interval time.Duration) *PinStore`, `(*PinStore).Generations() [][]string`, `(*PinStore).Path() string`; `netpolicy.PinsAllow(host string, gens [][]string) bool`; `netpolicy.FormatPinBlock(n int, at time.Time, patterns []string) string`; `netpolicy.DefaultPinPath`.

- [ ] **Step 1: Write the failing test**

Create `pkg/netpolicy/pins_test.go`:

```go
package netpolicy_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
)

func TestPinsAllow_NoPinsAllowsEverything(t *testing.T) {
	// An unpinned box must behave exactly as it did before pins existed.
	if !netpolicy.PinsAllow("anything.example", nil) {
		t.Error("no pins must not narrow anything")
	}
}

func TestPinsAllow_SingleGenerationIsMembership(t *testing.T) {
	gens := [][]string{{".github.com", "pypi.org"}}
	if !netpolicy.PinsAllow("api.github.com", gens) {
		t.Error("subdomain of a dotted pin must be allowed")
	}
	if !netpolicy.PinsAllow("pypi.org", gens) {
		t.Error("exact pin must be allowed")
	}
	if netpolicy.PinsAllow("files.pypi.org", gens) {
		t.Error("bare pin must NOT cover subdomains on the allow side")
	}
	if netpolicy.PinsAllow("evil.example", gens) {
		t.Error("unpinned host must be denied")
	}
}

func TestPinsAllow_GenerationsIntersect(t *testing.T) {
	// Successive pins can only narrow: a host must be present in EVERY
	// generation to survive. This is the whole guarantee.
	gens := [][]string{
		{".github.com", ".pypi.org"},
		{".github.com"},
	}
	if !netpolicy.PinsAllow("api.github.com", gens) {
		t.Error("host in both generations must survive")
	}
	if netpolicy.PinsAllow("files.pypi.org", gens) {
		t.Error("host dropped by a later pin must be denied — pins intersect")
	}
}

func TestPinsAllow_EmptyGenerationIsDenyAll(t *testing.T) {
	// Pinning a box that has touched nothing yields the empty set. Deny-all is
	// the correct reading of "allow only what I observed" when nothing was
	// observed; the CLI is what must warn, not the algebra.
	if netpolicy.PinsAllow("anything.example", [][]string{{}}) {
		t.Error("an empty generation must deny everything")
	}
}

func TestPinStore_ParsesGenerationBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allow.pins")
	body := "# pin 1 2026-08-28T14:02:11Z\n" +
		".github.com\n" +
		".pypi.org\n" +
		"# pin 2 2026-08-28T15:30:00Z\n" +
		".github.com\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	gens := netpolicy.NewPinStore(path, 0).Generations()
	if len(gens) != 2 {
		t.Fatalf("want 2 generations, got %d: %+v", len(gens), gens)
	}
	if len(gens[0]) != 2 || len(gens[1]) != 1 {
		t.Fatalf("generation contents wrong: %+v", gens)
	}
}

func TestPinStore_AbsentFileIsUnpinned(t *testing.T) {
	gens := netpolicy.NewPinStore(filepath.Join(t.TempDir(), "nope"), 0).Generations()
	if len(gens) != 0 {
		t.Fatalf("absent pin file must read as unpinned, got %+v", gens)
	}
}

func TestPinStore_RoundTripsFormatPinBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allow.pins")
	block := netpolicy.FormatPinBlock(1, time.Date(2026, 8, 28, 14, 0, 0, 0, time.UTC), []string{".github.com"})
	if err := os.WriteFile(path, []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}
	gens := netpolicy.NewPinStore(path, 0).Generations()
	if len(gens) != 1 || len(gens[0]) != 1 || gens[0][0] != ".github.com" {
		t.Fatalf("FormatPinBlock output must parse back: %+v", gens)
	}
}

func TestMatchAllow_IsNarrowerThanMatch(t *testing.T) {
	// The deny matcher deliberately covers subdomains from a bare entry. The
	// allow matcher must NOT: on the allow side that would grant more than the
	// operator wrote.
	if !netpolicy.Match("api.evil.example", []string{"evil.example"}) {
		t.Error("deny matcher should cover subdomains")
	}
	if netpolicy.MatchAllow("api.evil.example", []string{"evil.example"}) {
		t.Error("allow matcher must not cover subdomains from a bare entry")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/netpolicy/ -run 'TestPin|TestMatchAllow' -v`
Expected: FAIL — `PinsAllow`, `NewPinStore`, `MatchAllow`, `FormatPinBlock` undefined.

- [ ] **Step 3: Append `MatchAllow` to `pkg/netpolicy/netpolicy.go`**

Add at the end of the file:

```go
// MatchAllow reports whether host matches any of the patterns using ALLOW-side
// semantics, which are deliberately narrower than Match's.
//
// A leading dot means apex-plus-subdomains (".github.com" matches "github.com"
// and "api.github.com"); a bare entry matches exactly and nothing below it.
// This mirrors httpproxy.IsHostAllowed so a pinned host is judged the same way
// a profile-declared allowlist entry is.
//
// The asymmetry with Match is the point. On the deny side, strictness fails
// open; on the allow side, looseness grants more than the operator wrote.
func MatchAllow(host string, patterns []string) bool {
	h := stripPort(host)
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if h == "" {
		return false
	}
	for _, p := range patterns {
		if p == "*" {
			return true
		}
		p = strings.ToLower(strings.TrimSuffix(p, "."))
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, ".") {
			if h == p[1:] || strings.HasSuffix(h, p) {
				return true
			}
		} else if h == p {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Write `pkg/netpolicy/pins.go`**

```go
package netpolicy

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// DefaultPinPath is where the runtime allow-pin file lives on a sandbox.
//
// It sits beside deny.list, under /var/lib for the same reason: a reboot that
// dropped accumulated pins would WIDEN the policy, which the design forbids.
const DefaultPinPath = "/var/lib/km/netpolicy/allow.pins"

// pinHeaderPrefix marks the start of a generation block.
const pinHeaderPrefix = "# pin "

// PinStore is a lazily-reloading view of the allow-pin file.
//
// It is the intersection counterpart to Store. Where Store's entries union into
// an ever-larger deny set, a PinStore's generations intersect into an
// ever-smaller allow set. Both directions narrow; neither has a removal verb.
//
// Like Store it never reports an error: an absent or unreadable file reads as
// "unpinned", which is the correct default for the overwhelmingly common case.
//
// Safe for concurrent use.
type PinStore struct {
	path     string
	interval time.Duration

	mu        sync.RWMutex
	gens      [][]string
	lastMod   time.Time
	lastSize  int64
	lastCheck time.Time
	loaded    bool
}

// NewPinStore returns a PinStore reading path, re-checking at most once per
// interval. An interval of 0 checks on every call.
func NewPinStore(path string, interval time.Duration) *PinStore {
	return &PinStore{path: path, interval: interval}
}

// Path returns the file this store reads.
func (s *PinStore) Path() string { return s.path }

// Generations returns the pinned sets, oldest first, reloading if the cached
// view has expired and the file changed.
func (s *PinStore) Generations() [][]string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	if s.loaded && s.interval > 0 && time.Since(s.lastCheck) < s.interval {
		out := s.gens
		s.mu.RUnlock()
		return out
	}
	s.mu.RUnlock()
	return s.Reload()
}

// Reload re-reads the file unconditionally.
func (s *PinStore) Reload() [][]string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastCheck = time.Now()

	fi, err := os.Stat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.gens, s.loaded = nil, true
			s.lastMod, s.lastSize = time.Time{}, 0
			return nil
		}
		s.loaded = true
		return s.gens
	}
	if s.loaded && fi.ModTime().Equal(s.lastMod) && fi.Size() == s.lastSize {
		return s.gens
	}
	body, err := os.ReadFile(s.path)
	if err != nil {
		s.loaded = true
		return s.gens
	}

	s.gens = ParsePinBlocks(string(body))
	s.lastMod, s.lastSize, s.loaded = fi.ModTime(), fi.Size(), true
	return s.gens
}

// ParsePinBlocks splits a pin-file body into generations.
//
// A generation starts at a "# pin N <timestamp>" header and runs to the next
// one. Patterns are validated with ParseLine, so a malformed entry is dropped
// exactly as it is in a deny list. A generation whose entries were ALL dropped
// is retained as an empty generation rather than discarded — dropping it would
// silently widen the intersection back out.
func ParsePinBlocks(body string) [][]string {
	var gens [][]string
	cur := -1
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, pinHeaderPrefix) {
			gens = append(gens, []string{})
			cur = len(gens) - 1
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if cur < 0 {
			// Entries before any header belong to an implicit first generation.
			gens = append(gens, []string{})
			cur = 0
		}
		if p, ok := ParseLine(trimmed); ok {
			gens[cur] = append(gens[cur], p)
		}
	}
	return gens
}

// FormatPinBlock renders one generation for appending to the pin file.
func FormatPinBlock(n int, at time.Time, patterns []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%d %s\n", pinHeaderPrefix, n, at.UTC().Format(time.RFC3339))
	for _, p := range patterns {
		b.WriteString(p)
		b.WriteString("\n")
	}
	return b.String()
}

// PinsAllow reports whether host survives every pinned generation.
//
// No generations means unpinned, so everything passes and a box behaves exactly
// as it did before pins existed. Otherwise host must match in EVERY generation:
// the effective allow set is the intersection, so each pin can only ever shrink
// it. That monotonicity is what makes "pin never widens" a property of the data
// structure rather than a rule anyone has to trust.
func PinsAllow(host string, gens [][]string) bool {
	for _, g := range gens {
		if !MatchAllow(host, g) {
			return false
		}
	}
	return true
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./pkg/netpolicy/ -v`
Expected: PASS — new pin tests plus all pre-existing netpolicy tests.

- [ ] **Step 6: Commit**

```bash
git add pkg/netpolicy/
git commit -m "feat(netpolicy): allow-pin store with intersection algebra and allow-side matcher"
```

---

### Task 4: Pin candidate generation from a census

**Files:**
- Create: `pkg/flowlog/pincandidates.go`
- Test: `pkg/flowlog/pincandidates_test.go`

**Interfaces:**
- Consumes: `flowlog.Census`, `flowlog.Dest` (Task 2); `allowlistgen.CollapseToDNSSuffixes` (existing).
- Produces: `flowlog.PinCandidates(c Census, exact bool) (suffixes []string, hosts []string)`.

- [ ] **Step 1: Write the failing test**

Create `pkg/flowlog/pincandidates_test.go`:

```go
package flowlog_test

import (
	"reflect"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

func censusOf(hosts ...string) flowlog.Census {
	var ds []flowlog.Dest
	for _, h := range hosts {
		ds = append(ds, flowlog.Dest{Host: h, Count: 1})
	}
	return flowlog.Census{Allowed: ds}
}

func TestPinCandidates_CollapsedByDefault(t *testing.T) {
	suf, hosts := flowlog.PinCandidates(censusOf("api.github.com", "raw.github.com", "pypi.org"), false)

	wantSuf := []string{".github.com", ".pypi.org"}
	if !reflect.DeepEqual(suf, wantSuf) {
		t.Errorf("suffixes = %v, want %v", suf, wantSuf)
	}
	// Default mode collapses hosts too, so a CDN hostname the session happened
	// not to touch does not strand the box.
	if !reflect.DeepEqual(hosts, wantSuf) {
		t.Errorf("hosts = %v, want %v", hosts, wantSuf)
	}
}

func TestPinCandidates_ExactKeepsLiteralHosts(t *testing.T) {
	suf, hosts := flowlog.PinCandidates(censusOf("api.github.com", "pypi.org"), true)

	// DNS is ALWAYS collapsed regardless of --exact: names under a domain vary
	// per request and an exact DNS allowlist stops resolving almost at once.
	wantSuf := []string{".github.com", ".pypi.org"}
	if !reflect.DeepEqual(suf, wantSuf) {
		t.Errorf("suffixes = %v, want %v (must collapse even with exact)", suf, wantSuf)
	}
	wantHosts := []string{"api.github.com", "pypi.org"}
	if !reflect.DeepEqual(hosts, wantHosts) {
		t.Errorf("hosts = %v, want %v", hosts, wantHosts)
	}
}

func TestPinCandidates_IgnoresDeniedAndAddressOnly(t *testing.T) {
	c := flowlog.Census{
		Allowed: []flowlog.Dest{{Host: "api.github.com"}, {Addr: "203.0.113.9"}},
		Denied:  []flowlog.Dest{{Host: "evil.example.com"}},
	}
	suf, hosts := flowlog.PinCandidates(c, true)

	for _, got := range append(append([]string{}, suf...), hosts...) {
		if got == "evil.example.com" || got == ".example.com" {
			t.Fatalf("a denied host must never become pinnable: %v / %v", suf, hosts)
		}
		if got == "203.0.113.9" {
			t.Fatal("an address-only observation is not an allowlist entry")
		}
	}
	if len(hosts) != 1 || hosts[0] != "api.github.com" {
		t.Errorf("hosts = %v", hosts)
	}
}

func TestPinCandidates_EmptyCensusYieldsEmptySets(t *testing.T) {
	suf, hosts := flowlog.PinCandidates(flowlog.Census{}, false)
	if len(suf) != 0 || len(hosts) != 0 {
		t.Fatalf("empty census must yield empty sets, got %v / %v", suf, hosts)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/flowlog/ -run TestPinCandidates -v`
Expected: FAIL — `PinCandidates` undefined.

- [ ] **Step 3: Write `pkg/flowlog/pincandidates.go`**

```go
package flowlog

import (
	"sort"

	"github.com/whereiskurt/klanker-maker/pkg/allowlistgen"
)

// PinCandidates turns a census into the two lists a pin writes: DNS suffixes
// and hosts.
//
// Only Census.Allowed is consulted. A denied destination was never in the allow
// set, so it cannot appear in an intersection of allow sets — excluding it is
// what stops the census being a route to widening policy.
//
// Address-only observations are skipped. An allowlist is expressed in names,
// and a bare address is not a name; pinning one would produce an entry no
// matcher on either side would ever hit.
//
// DNS suffixes are ALWAYS collapsed to eTLD+1, regardless of exact. Names under
// a domain vary per request, so an exact-match DNS allowlist stops resolving
// almost immediately. CollapseToDNSSuffixes also skips anything that cannot
// reduce to an eTLD+1, so a pin can never generate a bare ".com".
//
// Hosts collapse by default and stay literal under exact. The default sits on
// the loose side deliberately: pin is irreversible, so too-loose costs a
// follow-up pin while too-tight costs a destroy/create.
func PinCandidates(c Census, exact bool) (suffixes []string, hosts []string) {
	var names []string
	for _, d := range c.Allowed {
		if d.Host == "" {
			continue
		}
		names = append(names, d.Host)
	}

	suffixes = allowlistgen.CollapseToDNSSuffixes(names)
	if !exact {
		return suffixes, suffixes
	}

	seen := map[string]struct{}{}
	for _, n := range names {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		hosts = append(hosts, n)
	}
	sort.Strings(hosts)
	return suffixes, hosts
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/flowlog/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/flowlog/
git commit -m "feat(flowlog): derive pin candidates from a census, collapsed by default"
```

---

### Task 5: `km-netpolicy observed` and `flows` verbs

**Files:**
- Create: `cmd/km-netpolicy/observe.go`
- Test: `cmd/km-netpolicy/observe_test.go`
- Modify: `cmd/km-netpolicy/main.go` (dispatch, usage, opts)

**Interfaces:**
- Consumes: `flowlog.ReadDir`, `flowlog.Summarize` (Task 2).
- Produces: `runObserved(o opts) int`; `runFlows(args []string, o opts) int`; `opts.flowDir` field.

- [ ] **Step 1: Write the failing test**

Create `cmd/km-netpolicy/observe_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/km-netpolicy/ -run 'TestObserved|TestFlows' -v`
Expected: FAIL — `runObserved`, `runFlows`, `opts.flowDir` undefined.

- [ ] **Step 3: Write `cmd/km-netpolicy/observe.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

// runObserved prints the deduplicated census: where this sandbox has been.
//
// Allowed and denied are printed under separate headings and never merged.
// Allowed is the set `pin` intersects against, so folding a blocked
// destination into it would turn the census into a way to widen policy.
func runObserved(o opts) int {
	recs, err := flowlog.ReadDir(o.flowDir)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s: cannot read flow store %s: %v\n", prog, o.flowDir, err)
		return 1
	}
	c := flowlog.Summarize(recs)

	fmt.Fprintf(o.stdout, "allowed (%d):\n", len(c.Allowed))
	printDests(o.stdout, c.Allowed)
	fmt.Fprintf(o.stdout, "\ndenied (%d):\n", len(c.Denied))
	printDests(o.stdout, c.Denied)
	return 0
}

// printDests renders one census section, or an explicit empty marker. An
// unadorned blank section reads as a broken command rather than a quiet box.
func printDests(w io.Writer, ds []flowlog.Dest) {
	if len(ds) == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}
	for _, d := range ds {
		label := d.Host
		if label == "" {
			label = d.Addr
		} else if d.Addr != "" {
			label = fmt.Sprintf("%s (%s)", d.Host, d.Addr)
		}
		fmt.Fprintf(w, "  %-48s %5d conns   last %s\n", label, d.Count, humanAgo(d.Last))
	}
}

// humanAgo renders a coarse age. Precision beyond a minute is noise for a
// census an operator reads to decide whether a session is finished.
func humanAgo(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// runFlows prints raw per-connection records.
func runFlows(args []string, o opts) int {
	var (
		since    time.Duration
		onlyDeny bool
		asJSON   bool
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--denied":
			onlyDeny = true
		case "--json":
			asJSON = true
		case "--since":
			if i+1 >= len(args) {
				fmt.Fprintf(o.stderr, "%s flows: --since needs a duration (e.g. 10m)\n", prog)
				return 2
			}
			i++
			d, err := time.ParseDuration(args[i])
			if err != nil {
				fmt.Fprintf(o.stderr, "%s flows: %q is not a duration: %v\n", prog, args[i], err)
				return 2
			}
			since = d
		default:
			fmt.Fprintf(o.stderr, "%s flows: unknown flag %q\n", prog, args[i])
			return 2
		}
	}

	recs, err := flowlog.ReadDir(o.flowDir)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s: cannot read flow store %s: %v\n", prog, o.flowDir, err)
		return 1
	}

	cutoff := time.Time{}
	if since > 0 {
		cutoff = time.Now().Add(-since)
	}
	shown := 0
	for _, r := range recs {
		if onlyDeny && r.Verdict != flowlog.VerdictDeny {
			continue
		}
		if !cutoff.IsZero() && r.TS.Before(cutoff) {
			continue
		}
		shown++
		if asJSON {
			b, err := json.Marshal(r)
			if err != nil {
				continue
			}
			fmt.Fprintln(o.stdout, string(b))
			continue
		}
		fmt.Fprintf(o.stdout, "%s  %-8s %-8s %-40s %s\n",
			r.TS.UTC().Format(time.RFC3339), r.Src, r.Verdict, target(r), procOf(r))
	}
	if shown == 0 && !asJSON {
		fmt.Fprintln(o.stdout, "(no matching flows)")
	}
	return 0
}

// target renders the destination of a record, preferring the name.
func target(r flowlog.Record) string {
	host := r.Host
	if host == "" {
		host = r.Addr
	}
	if r.Port != 0 {
		return fmt.Sprintf("%s:%d", host, r.Port)
	}
	return host
}

// procOf renders the originating process when the producer observed one. Only
// the eBPF path does; DNS and HTTP records legitimately have none.
func procOf(r flowlog.Record) string {
	if r.Comm == "" && r.PID == 0 {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%s[%d]", r.Comm, r.PID))
}
```

- [ ] **Step 4: Wire dispatch, opts, and usage in `cmd/km-netpolicy/main.go`**

Add `flowDir` to the `opts` struct, immediately after `denyFile`:

```go
	denyFile    string
	flowDir     string
```

In `buildOpts`, after the `denyFile` resolution block, add:

```go
	flowDir := pick("KM_FLOWLOG_DIR")
	if flowDir == "" {
		flowDir = flowlog.DefaultDir
	}
```

and add `flowDir: flowDir,` to the returned `opts` literal. Add the import
`"github.com/whereiskurt/klanker-maker/pkg/flowlog"`.

In `run`, add cases before `default`:

```go
	case "observed":
		return runObserved(o)
	case "flows":
		return runFlows(args[1:], o)
```

Extend the `usage` const's Usage block with:

```
  km-netpolicy observed                      show every destination reached so far
  km-netpolicy flows [--since 10m] [--denied] [--json]
                                             show raw per-connection records
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./cmd/km-netpolicy/ -v`
Expected: PASS — new tests plus all pre-existing `km-netpolicy` tests.

- [ ] **Step 6: Commit**

```bash
git add cmd/km-netpolicy/
git commit -m "feat(km-netpolicy): observed and flows verbs over the flow store"
```

---

### Task 6: `km-netpolicy profile` verb

**Files:**
- Create: `cmd/km-netpolicy/profilegen.go`
- Test: `cmd/km-netpolicy/profilegen_test.go`
- Modify: `cmd/km-netpolicy/main.go` (dispatch, usage)

**Interfaces:**
- Consumes: `flowlog.ReadDir`, `flowlog.Summarize` (Task 2); `allowlistgen.NewRecorder`, `(*Recorder).RecordDNSQuery`, `(*Recorder).RecordHost`, `(*Recorder).GenerateAnnotatedYAML` (existing).
- Produces: `runProfileGen(o opts) int`.

- [ ] **Step 1: Write the failing test**

Create `cmd/km-netpolicy/profilegen_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/km-netpolicy/ -run TestProfileGen -v`
Expected: FAIL — `runProfileGen` undefined.

- [ ] **Step 3: Write `cmd/km-netpolicy/profilegen.go`**

```go
package main

import (
	"fmt"

	"github.com/whereiskurt/klanker-maker/pkg/allowlistgen"
	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

// runProfileGen writes a SandboxProfile to stdout describing what this sandbox
// actually reached.
//
// It reuses allowlistgen.Recorder rather than rendering YAML here so the output
// is the same annotated shape `km shell --learn` produces. An operator who has
// read one learned.*.yaml can read this without learning a second format.
//
// Only the allowed census feeds the recorder. A denied destination was never
// reachable, so emitting it into an allowlist would hand back a profile that
// grants more than the box ever had.
func runProfileGen(o opts) int {
	recs, err := flowlog.ReadDir(o.flowDir)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s: cannot read flow store %s: %v\n", prog, o.flowDir, err)
		return 1
	}
	c := flowlog.Summarize(recs)

	rec := allowlistgen.NewRecorder()
	for _, d := range c.Allowed {
		if d.Host == "" {
			continue
		}
		rec.RecordDNSQuery(d.Host)
		rec.RecordHost(d.Host)
	}

	yamlBytes, err := rec.GenerateAnnotatedYAML("")
	if err != nil {
		fmt.Fprintf(o.stderr, "%s profile: generate failed: %v\n", prog, err)
		return 1
	}
	fmt.Fprint(o.stdout, string(yamlBytes))
	return 0
}
```

- [ ] **Step 4: Wire dispatch and usage in `cmd/km-netpolicy/main.go`**

Add case before `default`:

```go
	case "profile":
		return runProfileGen(o)
```

Extend `usage`:

```
  km-netpolicy profile                       emit a SandboxProfile from the census
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./cmd/km-netpolicy/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/km-netpolicy/
git commit -m "feat(km-netpolicy): profile verb emitting a SandboxProfile from the census"
```

---

### Task 7: `km-netpolicy pin` verb

**Files:**
- Create: `cmd/km-netpolicy/pin.go`
- Test: `cmd/km-netpolicy/pin_test.go`
- Modify: `cmd/km-netpolicy/main.go` (dispatch, usage, opts)

**Interfaces:**
- Consumes: `flowlog.ReadDir`, `flowlog.Summarize`, `flowlog.PinCandidates` (Tasks 2, 4); `netpolicy.NewPinStore`, `netpolicy.FormatPinBlock` (Task 3).
- Produces: `runPin(args []string, o opts) int`; `opts.pinFile` field.

- [ ] **Step 1: Write the failing test**

Create `cmd/km-netpolicy/pin_test.go`:

```go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
)

func pinOpts(t *testing.T, out, errb *bytes.Buffer) opts {
	t.Helper()
	pinFile := filepath.Join(t.TempDir(), "allow.pins")
	// The real file is created at boot with chattr +a. Pin refuses to create it
	// itself, so the test must pre-create it exactly as the box does.
	if err := os.WriteFile(pinFile, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	return opts{flowDir: seedFlows(t), pinFile: pinFile, stdout: out, stderr: errb}
}

func TestPin_DryRunWritesNothing(t *testing.T) {
	var out, errb bytes.Buffer
	o := pinOpts(t, &out, &errb)

	if code := runPin([]string{"--dry-run"}, o); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errb.String())
	}
	body, err := os.ReadFile(o.pinFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Fatalf("--dry-run must not write: %q", body)
	}
	got := out.String()
	if !strings.Contains(got, ".github.com") {
		t.Errorf("dry-run must show the candidate set:\n%s", got)
	}
	if !strings.Contains(strings.ToLower(got), "irreversible") {
		t.Errorf("dry-run must state that pinning cannot be undone:\n%s", got)
	}
}

func TestPin_RequiresYes(t *testing.T) {
	var out, errb bytes.Buffer
	o := pinOpts(t, &out, &errb)

	if code := runPin(nil, o); code == 0 {
		t.Fatal("pin without --yes must refuse")
	}
	body, _ := os.ReadFile(o.pinFile)
	if len(body) != 0 {
		t.Fatalf("refused pin must not write: %q", body)
	}
}

func TestPin_YesAppendsGeneration(t *testing.T) {
	var out, errb bytes.Buffer
	o := pinOpts(t, &out, &errb)

	if code := runPin([]string{"--yes"}, o); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errb.String())
	}
	gens := netpolicy.NewPinStore(o.pinFile, 0).Generations()
	if len(gens) != 1 {
		t.Fatalf("want 1 generation, got %d", len(gens))
	}
	if !netpolicy.PinsAllow("api.github.com", gens) {
		t.Error("observed host must survive its own pin")
	}
	if netpolicy.PinsAllow("unrelated.example", gens) {
		t.Error("unobserved host must be denied after pinning")
	}
}

func TestPin_SecondPinOnlyNarrows(t *testing.T) {
	var out, errb bytes.Buffer
	o := pinOpts(t, &out, &errb)
	if code := runPin([]string{"--yes"}, o); code != 0 {
		t.Fatalf("first pin exit %d", code)
	}

	// A second pin taken from an emptier census must not restore anything.
	o2 := o
	o2.flowDir = t.TempDir()
	if code := runPin([]string{"--yes", "--allow-empty"}, o2); code != 0 {
		t.Fatalf("second pin exit %d, stderr=%s", code, errb.String())
	}
	gens := netpolicy.NewPinStore(o.pinFile, 0).Generations()
	if len(gens) != 2 {
		t.Fatalf("want 2 generations, got %d", len(gens))
	}
	if netpolicy.PinsAllow("api.github.com", gens) {
		t.Error("a later empty pin must narrow, never restore")
	}
}

func TestPin_EmptyCensusRefusedWithoutAllowEmpty(t *testing.T) {
	var out, errb bytes.Buffer
	o := pinOpts(t, &out, &errb)
	o.flowDir = filepath.Join(t.TempDir(), "absent")

	if code := runPin([]string{"--yes"}, o); code == 0 {
		t.Fatal("pinning an empty census is deny-all and must be refused by default")
	}
	if !strings.Contains(errb.String(), "--allow-empty") {
		t.Errorf("refusal must name the override:\n%s", errb.String())
	}
}

func TestPin_MissingFileRefuses(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{flowDir: seedFlows(t), pinFile: filepath.Join(t.TempDir(), "absent"), stdout: &out, stderr: &errb}

	if code := runPin([]string{"--yes"}, o); code == 0 {
		t.Fatal("pin must refuse when the append-only file does not exist")
	}
	if _, err := os.Stat(o.pinFile); err == nil {
		t.Fatal("pin must not create the file itself — it would lack chattr +a and nothing would enforce it")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/km-netpolicy/ -run TestPin -v`
Expected: FAIL — `runPin`, `opts.pinFile` undefined.

- [ ] **Step 3: Write `cmd/km-netpolicy/pin.go`**

```go
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
)

// runPin narrows this sandbox's allowlist to what it has actually reached.
//
// A pin appends one generation to the append-only pin file. The effective allow
// set is the intersection of every generation, so a pin can only ever shrink
// it. There is no un-pin verb and adding one would destroy the guarantee.
func runPin(args []string, o opts) int {
	var dryRun, exact, yes, allowEmpty bool
	for _, a := range args {
		switch a {
		case "--dry-run":
			dryRun = true
		case "--exact":
			exact = true
		case "--yes":
			yes = true
		case "--allow-empty":
			allowEmpty = true
		default:
			fmt.Fprintf(o.stderr, "%s pin: unknown flag %q\n", prog, a)
			return 2
		}
	}

	recs, err := flowlog.ReadDir(o.flowDir)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s: cannot read flow store %s: %v\n", prog, o.flowDir, err)
		return 1
	}
	census := flowlog.Summarize(recs)
	suffixes, hosts := flowlog.PinCandidates(census, exact)
	altSuffixes, altHosts := flowlog.PinCandidates(census, !exact)

	fmt.Fprintf(o.stdout, "pinned DNS suffixes (%d):\n", len(suffixes))
	printList(o.stdout, suffixes)
	fmt.Fprintf(o.stdout, "\npinned hosts (%d):\n", len(hosts))
	printList(o.stdout, hosts)

	if dryRun {
		mode, alt := "collapsed", "--exact"
		if exact {
			mode, alt = "exact", "default (collapsed)"
		}
		fmt.Fprintf(o.stdout, "\nmode: %s. With %s you would pin instead:\n", mode, alt)
		fmt.Fprintf(o.stdout, "  suffixes (%d): %v\n  hosts (%d): %v\n",
			len(altSuffixes), altSuffixes, len(altHosts), altHosts)
		fmt.Fprintf(o.stdout,
			"\nnothing written. Pinning is IRREVERSIBLE — there is no un-pin verb,\n"+
				"and recovering from a too-tight pin means km destroy && km create.\n"+
				"Re-run with --yes to apply.\n")
		return 0
	}

	// An empty candidate set is a legitimate reading of "allow only what I
	// observed" on a box that observed nothing — and it is deny-all. Refuse by
	// default so it is never reached by accident.
	if len(suffixes) == 0 && len(hosts) == 0 && !allowEmpty {
		fmt.Fprintf(o.stderr,
			"%s pin: the census is empty, so this pin would deny ALL egress.\n"+
				"  This is usually a sign the box has not done its work yet.\n"+
				"  Pass --allow-empty if sealing the box is genuinely what you want.\n", prog)
		return 1
	}

	if !yes {
		fmt.Fprintf(o.stderr,
			"\n%s pin: refusing without --yes.\n"+
				"  Pinning is IRREVERSIBLE. Run with --dry-run first, then --yes.\n", prog)
		return 1
	}

	// An absent file means boot never provisioned the pin file. Creating it here
	// would produce a file with no kernel append-only attribute that no proxy is
	// watching — worse than refusing, because it would look like it worked. This
	// mirrors runDeny's stance on the deny list.
	if _, err := os.Stat(o.pinFile); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(o.stderr,
				"%s: pin file %s does not exist — this sandbox predates allowlist pinning.\n"+
					"  Recreate it with km destroy && km create.\n", prog, o.pinFile)
			return 1
		}
		fmt.Fprintf(o.stderr, "%s: cannot access %s: %v\n", prog, o.pinFile, err)
		return 1
	}

	store := netpolicy.NewPinStore(o.pinFile, 0)
	gen := len(store.Generations()) + 1
	block := netpolicy.FormatPinBlock(gen, time.Now(), dedupe(append(append([]string{}, suffixes...), hosts...)))

	f, err := os.OpenFile(o.pinFile, os.O_APPEND|os.O_WRONLY, 0o666)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s: cannot append to %s: %v\n", prog, o.pinFile, err)
		return 1
	}
	if _, err := f.WriteString(block); err != nil {
		f.Close()
		fmt.Fprintf(o.stderr, "%s: write failed: %v\n", prog, err)
		return 1
	}
	if err := f.Close(); err != nil {
		fmt.Fprintf(o.stderr, "%s: close failed: %v\n", prog, err)
		return 1
	}

	// Read back so the caller knows the pin is in force rather than merely
	// written — the same discipline runDeny applies.
	live := netpolicy.NewPinStore(o.pinFile, 0).Generations()
	if len(live) != gen {
		fmt.Fprintf(o.stderr, "%s: pin did not survive read-back — NOT in force\n", prog)
		return 1
	}
	fmt.Fprintf(o.stdout, "\npinned. generation %d of %d now in force (takes effect within ~1s).\n", gen, len(live))
	fmt.Fprintf(o.stdout, "everything not listed above is now denied.\n")
	return 0
}
```

- [ ] **Step 4: Wire dispatch, opts, and usage in `cmd/km-netpolicy/main.go`**

Add `pinFile string` to `opts` after `flowDir`. In `buildOpts`:

```go
	pinFile := pick("KM_NETPOLICY_PINS")
	if pinFile == "" {
		pinFile = netpolicy.DefaultPinPath
	}
```

and add `pinFile: pinFile,` to the returned literal. Add case before `default`:

```go
	case "pin":
		return runPin(args[1:], o)
```

Extend `usage`:

```
  km-netpolicy pin [--dry-run] [--exact] [--yes]
                                             narrow the allowlist to the census
```

- [ ] **Step 5: Extend `runList` to report pins**

In `runList`, before the `Dropped` check, add:

```go
	pins := netpolicy.NewPinStore(o.pinFile, 0).Generations()
	fmt.Fprintln(o.stdout, "\nallow pins (each generation narrows further):")
	if len(pins) == 0 {
		fmt.Fprintln(o.stdout, "  (none — allowlist is as the profile declared it)")
	} else {
		for i, g := range pins {
			fmt.Fprintf(o.stdout, "  generation %d:\n", i+1)
			for _, p := range g {
				fmt.Fprintf(o.stdout, "    %s\n", p)
			}
		}
	}
```

This matters for the same reason `/etc/km/netpolicy.env` exists: without it a
pinned box prints nothing about pins and reads as unpinned.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./cmd/km-netpolicy/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/km-netpolicy/
git commit -m "feat(km-netpolicy): pin verb narrowing the allowlist to the observed census"
```

---

### Task 8: Wire pin enforcement into the DNS proxy, HTTP proxy, and eBPF resolver

**Files:**
- Modify: `sidecars/dns-proxy/dnsproxy/proxy.go` (allow decision)
- Modify: `sidecars/dns-proxy/main.go` (construct pin store from env)
- Modify: `sidecars/http-proxy/httpproxy/proxy.go` (allow decision)
- Modify: `sidecars/http-proxy/main.go` (construct pin store from env)
- Modify: `pkg/ebpf/resolver/allowlist.go` (allow decision)
- Modify: `pkg/ebpf/resolver/resolver.go` (`ResolverConfig` gains `PinFile`)
- Modify: `internal/app/cmd/ebpf_attach.go` (`--netpolicy-pins` flag)
- Test: `pkg/netpolicy/wiring_guard_test.go`

**Interfaces:**
- Consumes: `netpolicy.NewPinStore`, `netpolicy.PinsAllow` (Task 3).
- Produces: `netpolicy.Pinner` with `NewPinner(store *PinStore) *Pinner` and `(*Pinner).Allows(host string) bool`.

- [ ] **Step 1: Write the failing wiring-guard test**

Create `pkg/netpolicy/wiring_guard_test.go`:

```go
package netpolicy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every consumer of the runtime DENY store must also consult the PIN store.
//
// Commit 0c7f9880 had to touch five sites to un-gate km-netpolicy, and its
// message records why a partial change is worse than none: ship the writer
// without the readers and the verb reports success while nothing enforces the
// result. The pin file has the identical shape and the identical failure mode,
// so this guard makes the pairing mechanical instead of remembered.
//
// The eBPF site is the one most likely to be missed and the most load-bearing:
// under ebpf/both the bootstrap leaves km-dns-proxy disabled and the resolver
// serves DNS, so a pin that skipped it would report success while every host
// stayed resolvable.
func TestEveryDenyConsumerAlsoReadsPins(t *testing.T) {
	repoRoot := findRepoRoot(t)
	consumers := []string{
		"sidecars/dns-proxy/dnsproxy/proxy.go",
		"sidecars/http-proxy/httpproxy/proxy.go",
		"pkg/ebpf/resolver/allowlist.go",
	}
	for _, rel := range consumers {
		body, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		src := string(body)
		if !strings.Contains(src, "IsDenied") {
			t.Fatalf("%s no longer consults the deny store — this guard needs updating", rel)
		}
		if !strings.Contains(src, "Pinner") && !strings.Contains(src, "PinsAllow") {
			t.Errorf("%s reads denies but not pins: a pin there reports success while nothing enforces it", rel)
		}
	}
}

// findRepoRoot walks up from the test's working directory to the module root.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find repo root")
	return ""
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/netpolicy/ -run TestEveryDenyConsumer -v`
Expected: FAIL — three consumers read denies but not pins.

- [ ] **Step 3: Add `Pinner` to `pkg/netpolicy/pins.go`**

```go
// Pinner answers the allow-side question every egress decision needs once pins
// exist: does this host survive every pin generation?
//
// It is the intersection counterpart to Denier. A nil Pinner, or one over an
// absent file, allows everything — so a box that has never pinned behaves
// exactly as it did before pins existed.
//
// Like Denier it is consulted per decision rather than snapshotted, which is
// what makes a pin taken at 10:00 effective on the next request without a
// restart.
type Pinner struct {
	store *PinStore
}

// NewPinner wraps a PinStore. A nil store yields a Pinner that allows all.
func NewPinner(store *PinStore) *Pinner { return &Pinner{store: store} }

// Allows reports whether host survives every pinned generation.
func (p *Pinner) Allows(host string) bool {
	if p == nil || p.store == nil {
		return true
	}
	return PinsAllow(host, p.store.Generations())
}
```

- [ ] **Step 4: Wire the DNS proxy**

In `sidecars/dns-proxy/dnsproxy/proxy.go`, change `NewHandler`'s signature to
take a pinner and apply it as a conjunction:

```go
func NewHandler(allowedSuffixes []string, denier *netpolicy.Denier, pinner *netpolicy.Pinner, upstreamAddr, sandboxID string) dns.HandlerFunc {
```

and replace the decision line:

```go
		denied := denier.IsDenied(domain)
		allowed := !denied && IsAllowed(domain, allowedSuffixes) && pinner.Allows(domain)
```

Pins are a conjunction applied AFTER the existing allow check, never a
replacement for it. That ordering is what keeps the change monotone: `A && P`
is a subset of `A` for any `P`.

Add `Bool("pinned_out", !denied && IsAllowed(domain, allowedSuffixes) && !pinner.Allows(domain)).`
to the existing `log.Info()` chain, so an operator can tell "never allowed"
from "was allowed until you pinned".

In `sidecars/dns-proxy/main.go`, build the pinner and pass it:

```go
	var pinStore *netpolicy.PinStore
	if pf := os.Getenv("KM_NETPOLICY_PINS"); pf != "" {
		pinStore = netpolicy.NewPinStore(pf, netpolicy.DefaultReloadInterval)
	}
	pinner := netpolicy.NewPinner(pinStore)
	handler := dnsproxy.NewHandler(allowedSuffixes, denier, pinner, upstream, sandboxID)
```

- [ ] **Step 5: Wire the HTTP proxy**

In `sidecars/http-proxy/httpproxy/proxy.go`, add beside `IsHostDenied`:

```go
// IsHostPinnedOut reports whether host is excluded by the runtime allow pins.
//
// Callers must apply this AFTER IsHostDenied and IsHostAllowed. Pins narrow an
// allowlist; they never override a deny and never grant what the profile did
// not already allow.
func IsHostPinnedOut(host string, pinner *netpolicy.Pinner) bool {
	return !pinner.Allows(host)
}
```

At each site that currently calls `IsHostAllowed` to gate a request, add
`&& !IsHostPinnedOut(host, p.pinner)`. Thread a `pinner *netpolicy.Pinner`
field onto the proxy struct, populated in `main.go` from `KM_NETPOLICY_PINS`
exactly as the DNS proxy does. Reuse the existing `http_denied` log event with
an added `Bool("pinned_out", true)` field rather than inventing a new event
type — `km otel` and the audit-log sidecar already parse `http_denied`.

- [ ] **Step 6: Wire the eBPF resolver**

In `pkg/ebpf/resolver/allowlist.go`, add a `pinner *netpolicy.Pinner` field to
`Allowlist`, extend `NewAllowlist` to accept it, and apply it in `IsAllowed`
after the deny check and after the allowAll/suffix check:

```go
	// Pins narrow whatever the allowlist permitted. Applied after allowAll so a
	// wide-open profile collapses to exactly the pinned set — the motivating
	// case, since * ∩ observed = observed.
	if !a.pinner.Allows(name) {
		return false
	}
```

Add `PinFile string` to `ResolverConfig` and a `pinnerFor(cfg)` helper mirroring
`denierFor`. In `internal/app/cmd/ebpf_attach.go`, add the flag:

```go
	cmd.Flags().StringVar(&netpolicyPins, "netpolicy-pins", "",
		"path to the runtime allow-pin file (empty disables pin narrowing)")
```

and thread it into `ResolverConfig.PinFile`.

- [ ] **Step 7: Run the full affected suite**

Run: `go build ./... && go test ./pkg/netpolicy/... ./pkg/ebpf/... ./sidecars/... -v`
Expected: PASS, including `TestEveryDenyConsumerAlsoReadsPins`. Fix any
call-site compile errors from the changed `NewHandler`/`NewAllowlist`
signatures — existing tests construct both.

- [ ] **Step 8: Commit**

```bash
git add pkg/netpolicy/ pkg/ebpf/ sidecars/ internal/app/cmd/ebpf_attach.go
git commit -m "feat(netpolicy): enforce allow pins in both proxies and the eBPF resolver"
```

---

### Task 9: Emit flow records from all three producers

**Files:**
- Modify: `sidecars/dns-proxy/dnsproxy/proxy.go`, `sidecars/dns-proxy/main.go`
- Modify: `sidecars/http-proxy/httpproxy/proxy.go`, `sidecars/http-proxy/main.go`
- Modify: `pkg/ebpf/audit/audit.go`
- Test: `sidecars/dns-proxy/dnsproxy/flowlog_test.go`
- Test: `pkg/ebpf/audit/flowlog_test.go`

**Interfaces:**
- Consumes: `flowlog.NewWriter`, `flowlog.Record`, `flowlog.FileFor` (Task 1).
- Produces: flow records on disk from live traffic. No new exported Go API.

- [ ] **Step 1: Write the failing test**

Create `sidecars/dns-proxy/dnsproxy/flowlog_test.go`:

```go
package dnsproxy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miekg/dns"
	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
	"github.com/whereiskurt/klanker-maker/sidecars/dns-proxy/dnsproxy"
)

// fakeWriter captures the ResponseWriter interface the handler needs.
type fakeWriter struct{ dns.ResponseWriter }

func (fakeWriter) WriteMsg(*dns.Msg) error { return nil }

func TestHandler_RecordsAllowAndDenyFlows(t *testing.T) {
	dir := t.TempDir()
	path := flowlog.FileFor(dir, flowlog.SrcDNS)
	w := flowlog.NewWriter(path, 1<<20)
	defer w.Close()

	h := dnsproxy.NewHandlerWithFlows(
		[]string{"github.com"},
		netpolicy.NewDenier([]string{"evil.example"}, nil),
		netpolicy.NewPinner(nil),
		"127.0.0.1:53", "sb-test", w,
	)

	for _, name := range []string{"api.github.com.", "evil.example."} {
		m := new(dns.Msg)
		m.SetQuestion(name, dns.TypeA)
		h(fakeWriter{}, m)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no flow file written: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, `"verdict":"allow"`) {
		t.Errorf("want an allow record:\n%s", got)
	}
	if !strings.Contains(got, `"verdict":"deny"`) {
		t.Errorf("want a deny record:\n%s", got)
	}
	if !strings.Contains(got, `"src":"dns"`) {
		t.Errorf("want the dns producer tag:\n%s", got)
	}
}

func TestHandler_NilWriterIsSafe(t *testing.T) {
	// Flow recording must never be load-bearing for an egress decision.
	h := dnsproxy.NewHandlerWithFlows(
		[]string{"github.com"}, nil, nil, "127.0.0.1:53", "sb-test", nil,
	)
	m := new(dns.Msg)
	m.SetQuestion("api.github.com.", dns.TypeA)
	h(fakeWriter{}, m) // must not panic
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sidecars/dns-proxy/... -run TestHandler_Records -v`
Expected: FAIL — `NewHandlerWithFlows` undefined.

- [ ] **Step 3: Add the flow-recording handler to the DNS proxy**

In `sidecars/dns-proxy/dnsproxy/proxy.go`, rename the existing constructor's
body into `NewHandlerWithFlows` taking a trailing `flows *flowlog.Writer`, and
keep `NewHandler` as a thin wrapper passing `nil` so existing callers and tests
compile unchanged.

Immediately after the existing `log.Info()` call, add:

```go
		// Best-effort. A flow store that cannot be written must never affect the
		// answer the sandbox is waiting on — losing observability beats stalling
		// egress. The error is deliberately dropped here; the writer surfaces
		// persistent failure through its own logging at construction time.
		if flows != nil {
			verdict := flowlog.VerdictAllow
			if !allowed {
				verdict = flowlog.VerdictDeny
			}
			_ = flows.Write(flowlog.Record{
				TS:      time.Now().UTC(),
				Src:     flowlog.SrcDNS,
				Verdict: verdict,
				Host:    strings.TrimSuffix(domain, "."),
			})
		}
```

In `sidecars/dns-proxy/main.go`, construct the writer from `KM_FLOWLOG_DIR`
(empty disables) and pass it to `NewHandlerWithFlows`.

- [ ] **Step 4: Add flow recording to the HTTP proxy**

Apply the same pattern in `sidecars/http-proxy`: a `flows *flowlog.Writer` field
on the proxy struct, written at each allow/deny/intercept decision point with
`Src: flowlog.SrcHTTP`, `Host`, `Port`, and `Proto: "tcp"`. Use
`VerdictRedirect` at the MITM interception sites so the census records how an
allowed host was reached rather than mislabelling metering as a block.

- [ ] **Step 5: Add flow recording to the eBPF audit consumer**

First add the reverse index to `pkg/ebpf/resolver/allowlist.go`. The resolver
already stores `resolved map[string]resolvedEntry` (domain → IPs); invert it on
demand rather than maintaining a second map that could drift:

```go
// NameForIP returns the domain that resolved to ip, or "" if this resolver
// never handed that address out.
//
// Expired entries are deliberately still consulted: a connection to an address
// whose TTL has since lapsed was still made to that name, and the census is a
// record of what happened, not of what is currently resolvable.
func (a *Allowlist) NameForIP(ip string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for domain, entry := range a.resolved {
		for _, got := range entry.ips {
			if got.String() == ip {
				return domain
			}
		}
	}
	return ""
}
```

Then create `pkg/ebpf/audit/flowlog_test.go` asserting that a `Consumer`
constructed with a writer emits one record per ring-buffer event, that
`ActionDeny`/`ActionAllow`/`ActionRedirect` map onto the three verdicts, that
`Addr`, `Port`, `PID` and `Comm` are carried, and — the case worth pinning —
that a nil `NameFn` and a `NameFn` returning `""` both leave `Host` empty rather
than writing a placeholder.

Add to `Consumer`:

```go
	flows  *flowlog.Writer
	nameFn flowlog.NameFn
```

with a `NewConsumerWithFlows(eventsMap, sandboxID, logger, flows, nameFn)`
constructor; `NewConsumer` stays as a wrapper passing nil for both. In the event
loop, after the existing structured log:

```go
		// Best-effort, exactly as in the proxies: the ring buffer must keep
		// draining even if the flow store cannot be written. A blocked consumer
		// means dropped events, which is worse than missing observability.
		if c.flows != nil {
			addr := ipString(ev.DstIP)
			host := ""
			if c.nameFn != nil {
				host = c.nameFn(addr)
			}
			_ = c.flows.Write(flowlog.Record{
				TS:      time.Now().UTC(),
				Src:     flowlog.SrcEBPF,
				Verdict: verdictFor(ev.Action),
				Host:    host,
				Addr:    addr,
				Port:    int(ntohs(ev.DstPort)),
				Proto:   "tcp",
				PID:     int(ev.Pid),
				Comm:    commString(ev.Comm),
			})
		}
```

Add `verdictFor(action uint8) string` mapping `ActionDeny`→`VerdictDeny`,
`ActionRedirect`→`VerdictRedirect`, and anything else→`VerdictAllow`. Reuse the
existing byte-order and `comm` helpers in `pkg/ebpf/audit/helpers.go` rather
than writing new ones.

In `internal/app/cmd/ebpf_attach.go`, add a `--flowlog-dir` flag (default
`flowlog.DefaultDir`, empty disables), build the writer, and pass
`allowlist.NameForIP` as the `NameFn`. The enforcer is the only place that
holds both the resolver and the writer, which is exactly why correlation
happens here and not at read time.

- [ ] **Step 6: Run the affected suites**

Run: `go build ./... && go test ./sidecars/... ./pkg/ebpf/... ./pkg/flowlog/... -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add sidecars/ pkg/ebpf/ internal/app/cmd/ebpf_attach.go
git commit -m "feat(flowlog): emit flow records from the DNS proxy, HTTP proxy, and eBPF consumer"
```

---

### Task 10: `pkg/pcap` — pure-Go pcap file writer

**Files:**
- Create: `pkg/pcap/writer.go`
- Test: `pkg/pcap/writer_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `pcap.Writer` with `NewWriter(w io.Writer, snaplen uint32) (*Writer, error)`, `(*Writer).WritePacket(ts time.Time, data []byte, origLen int) error`; constants `pcap.LinkTypeEthernet`, `pcap.DefaultSnaplen`.

- [ ] **Step 1: Write the failing test**

Create `pkg/pcap/writer_test.go`:

```go
package pcap_test

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/pcap"
)

func TestWriter_GlobalHeaderIsByteExact(t *testing.T) {
	var buf bytes.Buffer
	if _, err := pcap.NewWriter(&buf, 65535); err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	got := buf.Bytes()
	if len(got) != 24 {
		t.Fatalf("global header must be exactly 24 bytes, got %d", len(got))
	}
	if magic := binary.LittleEndian.Uint32(got[0:4]); magic != 0xa1b2c3d4 {
		t.Errorf("magic = %#x, want 0xa1b2c3d4", magic)
	}
	if major := binary.LittleEndian.Uint16(got[4:6]); major != 2 {
		t.Errorf("version major = %d, want 2", major)
	}
	if minor := binary.LittleEndian.Uint16(got[6:8]); minor != 4 {
		t.Errorf("version minor = %d, want 4", minor)
	}
	if snap := binary.LittleEndian.Uint32(got[16:20]); snap != 65535 {
		t.Errorf("snaplen = %d, want 65535", snap)
	}
	if link := binary.LittleEndian.Uint32(got[20:24]); link != pcap.LinkTypeEthernet {
		t.Errorf("linktype = %d, want %d", link, pcap.LinkTypeEthernet)
	}
}

func TestWriter_PacketRecordHeader(t *testing.T) {
	var buf bytes.Buffer
	w, err := pcap.NewWriter(&buf, 65535)
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Unix(1756389731, 123456000).UTC()
	payload := []byte{0xde, 0xad, 0xbe, 0xef}
	if err := w.WritePacket(ts, payload, 128); err != nil {
		t.Fatalf("WritePacket: %v", err)
	}

	rec := buf.Bytes()[24:]
	if len(rec) != 16+len(payload) {
		t.Fatalf("record = %d bytes, want %d", len(rec), 16+len(payload))
	}
	if sec := binary.LittleEndian.Uint32(rec[0:4]); sec != 1756389731 {
		t.Errorf("ts_sec = %d", sec)
	}
	if usec := binary.LittleEndian.Uint32(rec[4:8]); usec != 123456 {
		t.Errorf("ts_usec = %d, want 123456", usec)
	}
	if incl := binary.LittleEndian.Uint32(rec[8:12]); incl != 4 {
		t.Errorf("incl_len = %d, want 4", incl)
	}
	// orig_len must report the wire length even when the capture was truncated,
	// or Wireshark silently misreports every truncated packet.
	if orig := binary.LittleEndian.Uint32(rec[12:16]); orig != 128 {
		t.Errorf("orig_len = %d, want 128", orig)
	}
	if !bytes.Equal(rec[16:], payload) {
		t.Errorf("payload = %x", rec[16:])
	}
}

func TestWriter_TruncatesToSnaplen(t *testing.T) {
	var buf bytes.Buffer
	w, err := pcap.NewWriter(&buf, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WritePacket(time.Now(), make([]byte, 64), 64); err != nil {
		t.Fatal(err)
	}
	rec := buf.Bytes()[24:]
	if incl := binary.LittleEndian.Uint32(rec[8:12]); incl != 8 {
		t.Errorf("incl_len = %d, want snaplen 8", incl)
	}
	if orig := binary.LittleEndian.Uint32(rec[12:16]); orig != 64 {
		t.Errorf("orig_len = %d, want the full wire length 64", orig)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/pcap/ -v`
Expected: FAIL — package `pcap` does not exist.

- [ ] **Step 3: Write `pkg/pcap/writer.go`**

```go
// Package pcap writes libpcap-format capture files.
//
// The format is small enough to implement directly — a 24-byte global header
// and a 16-byte header per packet — and doing so keeps the sidecars free of
// cgo. That is a hard requirement, not a preference: sidecars cross-compile to
// linux/amd64 from macOS with CGO_ENABLED=0, and libpcap would break the build.
package pcap

import (
	"encoding/binary"
	"io"
	"time"
)

// LinkTypeEthernet is LINKTYPE_ETHERNET, the link type for frames captured
// from an AF_PACKET socket on an Ethernet interface.
const LinkTypeEthernet = 1

// DefaultSnaplen captures whole frames on a standard-MTU interface.
const DefaultSnaplen uint32 = 65535

// magic is the little-endian microsecond-resolution pcap magic. Writing native
// little-endian keeps readers on x86 and arm from having to byte-swap.
const magic uint32 = 0xa1b2c3d4

// Writer emits a pcap stream. It is not safe for concurrent use; the capture
// daemon owns exactly one per capture.
type Writer struct {
	w       io.Writer
	snaplen uint32
	buf     [16]byte
}

// NewWriter writes the global header and returns a Writer ready for packets.
func NewWriter(w io.Writer, snaplen uint32) (*Writer, error) {
	if snaplen == 0 {
		snaplen = DefaultSnaplen
	}
	var hdr [24]byte
	binary.LittleEndian.PutUint32(hdr[0:4], magic)
	binary.LittleEndian.PutUint16(hdr[4:6], 2)  // version major
	binary.LittleEndian.PutUint16(hdr[6:8], 4)  // version minor
	binary.LittleEndian.PutUint32(hdr[8:12], 0) // thiszone: timestamps are UTC
	binary.LittleEndian.PutUint32(hdr[12:16], 0)
	binary.LittleEndian.PutUint32(hdr[16:20], snaplen)
	binary.LittleEndian.PutUint32(hdr[20:24], LinkTypeEthernet)
	if _, err := w.Write(hdr[:]); err != nil {
		return nil, err
	}
	return &Writer{w: w, snaplen: snaplen}, nil
}

// WritePacket appends one packet.
//
// origLen is the length on the wire, which may exceed len(data) when the
// capture was truncated to snaplen. Reporting it honestly is what lets
// Wireshark mark a packet as truncated instead of silently presenting a short
// frame as complete.
func (p *Writer) WritePacket(ts time.Time, data []byte, origLen int) error {
	if uint32(len(data)) > p.snaplen {
		data = data[:p.snaplen]
	}
	if origLen < len(data) {
		origLen = len(data)
	}
	binary.LittleEndian.PutUint32(p.buf[0:4], uint32(ts.Unix()))
	binary.LittleEndian.PutUint32(p.buf[4:8], uint32(ts.Nanosecond()/1000))
	binary.LittleEndian.PutUint32(p.buf[8:12], uint32(len(data)))
	binary.LittleEndian.PutUint32(p.buf[12:16], uint32(origLen))
	if _, err := p.w.Write(p.buf[:]); err != nil {
		return err
	}
	_, err := p.w.Write(data)
	return err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/pcap/ -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add pkg/pcap/
git commit -m "feat(pcap): pure-Go libpcap-format writer with no cgo dependency"
```

---

### Task 11: Capture daemon and `km-netpolicy capture` verbs

**Files:**
- Create: `cmd/km-netpolicy/capture.go` (client verbs, all platforms)
- Create: `cmd/km-netpolicy/capture_daemon_linux.go` (`//go:build linux`)
- Create: `cmd/km-netpolicy/capture_daemon_stub.go` (`//go:build !linux`)
- Test: `cmd/km-netpolicy/capture_test.go`
- Modify: `cmd/km-netpolicy/main.go` (dispatch, usage)

**Interfaces:**
- Consumes: `pcap.NewWriter` (Task 10).
- Produces: `runCapture(args []string, o opts) int`; `runCaptureDaemon(o opts) int`; request/response JSON types `captureReq`/`captureResp`; `opts.captureSock`, `opts.captureDir`.

- [ ] **Step 1: Write the failing test**

Create `cmd/km-netpolicy/capture_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCapture_RejectsUnboundedDuration(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{stdout: &out, stderr: &errb}

	if code := runCapture([]string{"start", "--duration", "0"}, o); code == 0 {
		t.Fatal("a zero duration is an unbounded capture and must be refused")
	}
}

func TestCapture_RejectsDurationOverCeiling(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{stdout: &out, stderr: &errb}

	if code := runCapture([]string{"start", "--duration", "24h"}, o); code == 0 {
		t.Fatal("duration beyond the hard ceiling must be refused")
	}
	if !strings.Contains(errb.String(), maxCaptureDuration.String()) {
		t.Errorf("refusal must name the ceiling:\n%s", errb.String())
	}
}

func TestCapture_RejectsSizeOverCeiling(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{stdout: &out, stderr: &errb}

	if code := runCapture([]string{"start", "--max-size", "100GB"}, o); code == 0 {
		t.Fatal("max-size beyond the ceiling must be refused")
	}
}

func TestCapture_UnknownSubverb(t *testing.T) {
	var out, errb bytes.Buffer
	o := opts{stdout: &out, stderr: &errb}

	if code := runCapture([]string{"frobnicate"}, o); code != 2 {
		t.Fatalf("unknown subverb should exit 2, got %d", code)
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"250MB": 250 << 20,
		"1GB":   1 << 30,
		"512KB": 512 << 10,
		"1024":  1024,
	}
	for in, want := range cases {
		got, err := parseSize(in)
		if err != nil {
			t.Errorf("parseSize(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
	if _, err := parseSize("banana"); err == nil {
		t.Error("want an error for a non-size")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/km-netpolicy/ -run 'TestCapture|TestParseSize' -v`
Expected: FAIL — `runCapture`, `parseSize`, `maxCaptureDuration` undefined.

- [ ] **Step 3: Write `cmd/km-netpolicy/capture.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Hard ceilings. These are not defaults an operator can raise with a flag: an
// unbounded capture on a sandbox with a 30GB data volume fills the disk and
// takes the box down, and a capture is a diagnostic, not a recording studio.
const (
	maxCaptureDuration     = 60 * time.Minute
	maxCaptureSize         = int64(2) << 30 // 2 GiB
	defaultCaptureDuration = 5 * time.Minute
	defaultCaptureSize     = int64(250) << 20
)

// DefaultCaptureSock is where the root daemon listens. Mode 0660, group
// sandbox, so an unprivileged agent can request a capture without ever holding
// CAP_NET_RAW itself.
const DefaultCaptureSock = "/run/km/capture.sock"

// DefaultCaptureDir is where finished captures land before upload.
const DefaultCaptureDir = "/var/lib/km/capture"

// captureReq is the client→daemon message.
type captureReq struct {
	Op       string `json:"op"` // "start" | "stop" | "status" | "list"
	Duration string `json:"duration,omitempty"`
	MaxSize  int64  `json:"max_size,omitempty"`
	Port     int    `json:"port,omitempty"`
	Host     string `json:"host,omitempty"`
}

// captureResp is the daemon→client reply.
type captureResp struct {
	OK    bool     `json:"ok"`
	Msg   string   `json:"msg,omitempty"`
	Files []string `json:"files,omitempty"`
	S3URI string   `json:"s3_uri,omitempty"`
}

// runCapture is the unprivileged client. It validates bounds locally so an
// obviously-wrong request never reaches the privileged daemon.
func runCapture(args []string, o opts) int {
	if len(args) == 0 {
		fmt.Fprintf(o.stderr, "%s capture: needs a subverb (start|stop|status|list)\n", prog)
		return 2
	}

	req := captureReq{Op: args[0]}
	dur := defaultCaptureDuration
	size := defaultCaptureSize

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--duration", "--max-size", "--port", "--host":
			if i+1 >= len(args) {
				fmt.Fprintf(o.stderr, "%s capture: %s needs a value\n", prog, args[i])
				return 2
			}
			flag, val := args[i], args[i+1]
			i++
			switch flag {
			case "--duration":
				d, err := time.ParseDuration(val)
				if err != nil {
					fmt.Fprintf(o.stderr, "%s capture: %q is not a duration: %v\n", prog, val, err)
					return 2
				}
				dur = d
			case "--max-size":
				n, err := parseSize(val)
				if err != nil {
					fmt.Fprintf(o.stderr, "%s capture: %q is not a size: %v\n", prog, val, err)
					return 2
				}
				size = n
			case "--port":
				n, err := strconv.Atoi(val)
				if err != nil {
					fmt.Fprintf(o.stderr, "%s capture: %q is not a port\n", prog, val)
					return 2
				}
				req.Port = n
			case "--host":
				req.Host = val
			}
		default:
			fmt.Fprintf(o.stderr, "%s capture: unknown flag %q\n", prog, args[i])
			return 2
		}
	}

	switch req.Op {
	case "start":
		if dur <= 0 {
			fmt.Fprintf(o.stderr, "%s capture: --duration must be positive; there is no unbounded capture\n", prog)
			return 2
		}
		if dur > maxCaptureDuration {
			fmt.Fprintf(o.stderr, "%s capture: --duration %s exceeds the hard ceiling of %s\n",
				prog, dur, maxCaptureDuration)
			return 2
		}
		if size <= 0 || size > maxCaptureSize {
			fmt.Fprintf(o.stderr, "%s capture: --max-size must be between 1 and %d bytes\n", prog, maxCaptureSize)
			return 2
		}
		req.Duration = dur.String()
		req.MaxSize = size
	case "stop", "status", "list":
		// no bounds to check
	default:
		fmt.Fprintf(o.stderr, "%s capture: unknown subverb %q (want start|stop|status|list)\n", prog, req.Op)
		return 2
	}

	resp, err := captureRPC(o.captureSock, req)
	if err != nil {
		fmt.Fprintf(o.stderr,
			"%s capture: cannot reach the capture daemon at %s: %v\n"+
				"  Check: systemctl status km-capture\n", prog, o.captureSock, err)
		return 1
	}
	if !resp.OK {
		fmt.Fprintf(o.stderr, "%s capture: %s\n", prog, resp.Msg)
		return 1
	}
	if resp.Msg != "" {
		fmt.Fprintln(o.stdout, resp.Msg)
	}
	for _, f := range resp.Files {
		fmt.Fprintf(o.stdout, "  %s\n", f)
	}
	if resp.S3URI != "" {
		fmt.Fprintf(o.stdout, "uploaded: %s\n", resp.S3URI)
	}
	return 0
}

// captureRPC sends one request over the Unix socket and reads one reply.
func captureRPC(sock string, req captureReq) (captureResp, error) {
	var resp captureResp
	conn, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		return resp, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return resp, err
	}
	err = json.NewDecoder(conn).Decode(&resp)
	return resp, err
}

// parseSize accepts a byte count with an optional KB/MB/GB suffix (binary
// multiples, matching how operators think about disk).
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "KB"):
		mult, s = 1<<10, strings.TrimSuffix(s, "KB")
	case strings.HasSuffix(s, "MB"):
		mult, s = 1<<20, strings.TrimSuffix(s, "MB")
	case strings.HasSuffix(s, "GB"):
		mult, s = 1<<30, strings.TrimSuffix(s, "GB")
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, err
	}
	return n * mult, nil
}
```

- [ ] **Step 4: Write the Linux daemon**

Create `cmd/km-netpolicy/capture_daemon_linux.go`:

```go
//go:build linux

package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/whereiskurt/klanker-maker/pkg/pcap"
)

// captureState is the daemon's single in-flight capture. At most one runs at a
// time: two concurrent captures on one interface double the disk cost for
// duplicate data, and the second is almost always a forgotten first.
type captureState struct {
	mu     sync.Mutex
	active bool
	path   string
	stop   chan struct{}
	done   chan struct{}
	err    error
}

// runCaptureDaemon serves capture requests over a Unix socket as root.
//
// It exists because CAP_NET_RAW cannot be delegated through a file. Everything
// else in this binary is an unprivileged operation on an append-only file; this
// is the one place that needs an arbitrating privileged process, and it holds
// no state between captures.
func runCaptureDaemon(o opts) int {
	if err := os.MkdirAll(filepath.Dir(o.captureSock), 0o755); err != nil {
		fmt.Fprintf(o.stderr, "%s capture-daemon: %v\n", prog, err)
		return 1
	}
	if err := os.MkdirAll(o.captureDir, 0o755); err != nil {
		fmt.Fprintf(o.stderr, "%s capture-daemon: %v\n", prog, err)
		return 1
	}
	// A stale socket from an unclean stop would make Listen fail forever.
	_ = os.Remove(o.captureSock)

	ln, err := net.Listen("unix", o.captureSock)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s capture-daemon: listen: %v\n", prog, err)
		return 1
	}
	defer ln.Close()

	// 0660 owned by the sandbox group so an unprivileged agent can ask for a
	// capture without ever holding CAP_NET_RAW. If the profile has no sandbox
	// group, fall back to 0666 rather than refusing to start — the socket only
	// ever accepts the bounded verbs below, and a dead daemon helps nobody.
	if err := os.Chmod(o.captureSock, 0o660); err != nil {
		fmt.Fprintf(o.stderr, "%s capture-daemon: chmod: %v\n", prog, err)
	}
	if g, gerr := user.LookupGroup("sandbox"); gerr == nil {
		if gid, cerr := strconv.Atoi(g.Gid); cerr == nil {
			_ = os.Chown(o.captureSock, 0, gid)
		}
	} else {
		_ = os.Chmod(o.captureSock, 0o666)
	}

	st := &captureState{}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return 0 // listener closed on shutdown
		}
		go serveCapture(conn, st, o)
	}
}

// serveCapture handles one request/response exchange.
func serveCapture(conn net.Conn, st *captureState, o opts) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	var req captureReq
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(captureResp{OK: false, Msg: "bad request: " + err.Error()})
		return
	}

	var resp captureResp
	switch req.Op {
	case "start":
		resp = st.start(req, o)
	case "stop":
		resp = st.stopCapture(o)
	case "status":
		resp = st.status()
	case "list":
		resp = listCaptures(o)
	default:
		resp = captureResp{OK: false, Msg: "unknown op " + req.Op}
	}
	_ = json.NewEncoder(conn).Encode(resp)
}

// start opens the raw socket and begins streaming to a pcap file.
func (s *captureState) start(req captureReq, o opts) captureResp {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		return captureResp{OK: false, Msg: "a capture is already running; run `km-netpolicy capture stop` first"}
	}

	dur, err := time.ParseDuration(req.Duration)
	if err != nil || dur <= 0 || dur > maxCaptureDuration {
		return captureResp{OK: false, Msg: "duration out of bounds"}
	}
	if req.MaxSize <= 0 || req.MaxSize > maxCaptureSize {
		return captureResp{OK: false, Msg: "max_size out of bounds"}
	}

	// Refuse unless there is room for the capture twice over. Filling the data
	// volume takes the whole sandbox down, which is a far worse outcome than a
	// refused diagnostic.
	var stat unix.Statfs_t
	if err := unix.Statfs(o.captureDir, &stat); err == nil {
		free := int64(stat.Bavail) * int64(stat.Bsize)
		if free < req.MaxSize*2 {
			return captureResp{OK: false, Msg: fmt.Sprintf(
				"only %d bytes free in %s; need %d (2x max-size)", free, o.captureDir, req.MaxSize*2)}
		}
	}

	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(unix.ETH_P_ALL)))
	if err != nil {
		return captureResp{OK: false, Msg: "AF_PACKET socket: " + err.Error()}
	}
	if filter := buildFilter(req); len(filter) > 0 {
		if err := attachFilter(fd, filter); err != nil {
			unix.Close(fd)
			return captureResp{OK: false, Msg: "attach filter: " + err.Error()}
		}
	}

	path := filepath.Join(o.captureDir, time.Now().UTC().Format("20060102T150405Z")+".pcap")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		unix.Close(fd)
		return captureResp{OK: false, Msg: "create capture file: " + err.Error()}
	}
	w, err := pcap.NewWriter(f, pcap.DefaultSnaplen)
	if err != nil {
		f.Close()
		unix.Close(fd)
		return captureResp{OK: false, Msg: "pcap header: " + err.Error()}
	}

	s.active, s.path, s.err = true, path, nil
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	go s.loop(fd, f, w, dur, req.MaxSize)

	return captureResp{OK: true, Msg: fmt.Sprintf(
		"capturing to %s (max %s / %d bytes)", path, dur, req.MaxSize)}
}

// loop streams frames until any bound is hit. Both bounds are enforced here,
// not just the one the operator was thinking about when they typed the command.
func (s *captureState) loop(fd int, f *os.File, w *pcap.Writer, dur time.Duration, maxSize int64) {
	defer close(s.done)
	defer unix.Close(fd)
	defer f.Close()

	deadline := time.Now().Add(dur)
	buf := make([]byte, 65536)
	var written int64

	// A read timeout is what lets the loop notice the deadline and an explicit
	// stop on an idle interface, instead of blocking in recvfrom forever.
	tv := unix.Timeval{Sec: 1}
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)

	for {
		select {
		case <-s.stop:
			return
		default:
		}
		if time.Now().After(deadline) || written >= maxSize {
			return
		}
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == unix.EAGAIN || err == unix.EINTR {
				continue
			}
			s.err = err
			return
		}
		if n <= 0 {
			continue
		}
		if err := w.WritePacket(time.Now().UTC(), buf[:n], n); err != nil {
			s.err = err
			return
		}
		written += int64(n) + 16
	}
}

// stopCapture ends the running capture and uploads the result.
func (s *captureState) stopCapture(o opts) captureResp {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return captureResp{OK: false, Msg: "no capture is running"}
	}
	close(s.stop)
	<-s.done
	s.active = false
	path := s.path

	resp := captureResp{OK: true, Msg: "stopped " + path, Files: []string{path}}
	if s.err != nil {
		resp.Msg = fmt.Sprintf("stopped %s (with error: %v)", path, s.err)
	}
	// Upload is a convenience; the file on disk is the deliverable. A failed
	// upload must not lose the capture, so it is reported and the local file
	// left in place — the same stance the learn flush takes.
	if uri, err := uploadCapture(path, o); err != nil {
		resp.Msg += fmt.Sprintf(" (S3 upload failed, file kept locally: %v)", err)
	} else {
		resp.S3URI = uri
	}
	return resp
}

func (s *captureState) status() captureResp {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return captureResp{OK: true, Msg: "idle"}
	}
	return captureResp{OK: true, Msg: "capturing to " + s.path}
}

// listCaptures reports finished capture files.
func listCaptures(o opts) captureResp {
	entries, err := os.ReadDir(o.captureDir)
	if err != nil {
		return captureResp{OK: true, Msg: "no captures"}
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".pcap" {
			files = append(files, filepath.Join(o.captureDir, e.Name()))
		}
	}
	if len(files) == 0 {
		return captureResp{OK: true, Msg: "no captures"}
	}
	return captureResp{OK: true, Msg: fmt.Sprintf("%d capture(s):", len(files)), Files: files}
}

// htons converts a uint16 to network byte order, which AF_PACKET's protocol
// argument requires.
func htons(v uint16) uint16 {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	return binary.LittleEndian.Uint16(b[:])
}
```

Add two more small files alongside it:

- `buildFilter(req captureReq) []unix.SockFilter` — assembles a classic BPF
  program from `--port`/`--host` using `golang.org/x/net/bpf` and
  `bpf.Assemble`, returning nil when neither flag is set. Converting
  `bpf.RawInstruction` to `unix.SockFilter` is a direct field copy.
- `attachFilter(fd int, filter []unix.SockFilter) error` — `unix.SetsockoptSockFprog`
  with `unix.SO_ATTACH_FILTER`.
- `uploadCapture(path string, o opts) (string, error)` — puts the file at
  `s3://$KM_ARTIFACTS_BUCKET/captures/$KM_SANDBOX_ID/<basename>` using the
  instance role, returning the URI. This is the prefix Task 13 grants.

Create `cmd/km-netpolicy/capture_daemon_stub.go` with `//go:build !linux`
defining `runCaptureDaemon(o opts) int` that prints "packet capture requires
Linux" and returns 1, so the command still builds and tests on macOS. Follow
`pkg/ebpf/enforcer_stub.go` for the pattern.

- [ ] **Step 5: Wire dispatch and usage in `cmd/km-netpolicy/main.go`**

Add `captureSock` and `captureDir` to `opts`, resolved in `buildOpts` from
`KM_CAPTURE_SOCK` / `KM_CAPTURE_DIR` with the defaults above. Add cases:

```go
	case "capture":
		return runCapture(args[1:], o)
	case "capture-daemon":
		return runCaptureDaemon(o)
```

Document `capture` in `usage`. Deliberately leave `capture-daemon` out of the
usage text: it is the systemd `ExecStart` entry point, not an operator verb,
and listing it invites someone to run a second one by hand.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./cmd/km-netpolicy/ -v && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./cmd/km-netpolicy/`
Expected: PASS, and the linux build succeeds.

- [ ] **Step 7: Commit**

```bash
git add cmd/km-netpolicy/
git commit -m "feat(km-netpolicy): bounded on-demand packet capture via a root daemon"
```

---

### Task 12: Userdata wiring

**Files:**
- Modify: `pkg/compiler/userdata.go`
- Test: `pkg/compiler/userdata_test.go`
- Modify: golden fixtures under `pkg/compiler/testdata/`

**Interfaces:**
- Consumes: everything above.
- Produces: boot-time provisioning of the flow directory, the pin file, the capture unit, and the env vars every consumer reads.

- [ ] **Step 1: Write the failing test**

Add to `pkg/compiler/userdata_test.go`:

```go
func TestUserData_ProvisionsFlowStoreAndPins(t *testing.T) {
	script := renderUserDataForTest(t, minimalProfile(t))

	// The five-site trap from commit 0c7f9880, restated for pins: writer plus
	// every reader, or the verb reports success while nothing enforces it.
	wants := []string{
		"/var/lib/km/flows",                       // flow dir exists
		"/var/lib/km/netpolicy/allow.pins",        // pin file exists
		"chattr +a /var/lib/km/netpolicy/allow.pins", // kernel-enforced append-only
		"KM_FLOWLOG_DIR=",                         // producers can write
		"KM_NETPOLICY_PINS=",                      // proxies can read
		"--netpolicy-pins",                        // eBPF resolver can read
		"km-capture.service",                      // capture daemon unit
		"capture-daemon",                          // its ExecStart verb
	}
	for _, w := range wants {
		if !strings.Contains(script, w) {
			t.Errorf("userdata missing %q", w)
		}
	}
}

func TestUserData_PinFileIsAppendOnlyBeforeAnythingCanWrite(t *testing.T) {
	script := renderUserDataForTest(t, minimalProfile(t))
	chattrIdx := strings.Index(script, "chattr +a /var/lib/km/netpolicy/allow.pins")
	unitIdx := strings.Index(script, "km-capture.service")
	if chattrIdx < 0 || unitIdx < 0 {
		t.Fatal("expected both the chattr and the unit")
	}
	if chattrIdx > unitIdx {
		t.Error("the pin file must be sealed append-only before any service starts")
	}
}
```

Match the existing helper names in `userdata_test.go` — if
`renderUserDataForTest`/`minimalProfile` do not exist, use whatever the file's
existing tests use to render a script.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/compiler/ -run TestUserData_Provisions -v`
Expected: FAIL — none of the strings present.

- [ ] **Step 3: Add the userdata section**

In `pkg/compiler/userdata.go`, beside the existing block that creates
`RuntimeDenyFile` and runs `chattr +a` (around line 1326), add an
unconditional sibling block. Follow the existing comment style — explain why,
not what:

```bash
# Allow pins. The intersection counterpart to the deny list: each generation
# narrows the allowlist further and none can widen it. Append-only in the
# kernel for the same reason deny.list is — a sandbox that could rewrite this
# file could restore access it had already given up.
mkdir -p /var/lib/km/netpolicy /var/lib/km/flows /var/lib/km/capture
touch /var/lib/km/netpolicy/allow.pins
chmod 666 /var/lib/km/netpolicy/allow.pins
chattr +a /var/lib/km/netpolicy/allow.pins 2>/dev/null || \
  echo "[km-bootstrap] WARN: chattr +a failed on allow.pins (filesystem may not support it)"
chmod 755 /var/lib/km/flows
```

Add `KM_FLOWLOG_DIR` and `KM_NETPOLICY_PINS` to `/etc/km/netpolicy.env` and to
the `Environment=` blocks of **both** proxy units. Add `--netpolicy-pins` and
`--flowlog-dir` to the `km-ebpf-enforcer` `ExecStart`. Add the capture unit:

```bash
cat > /etc/systemd/system/km-capture.service << 'UNIT'
[Unit]
Description=km packet capture daemon
After=network-online.target
[Service]
Type=simple
User=root
Environment=KM_CAPTURE_SOCK=/run/km/capture.sock
Environment=KM_CAPTURE_DIR=/var/lib/km/capture
Environment=KM_ARTIFACTS_BUCKET={{ .ArtifactsBucket }}
Environment=KM_SANDBOX_ID={{ .SandboxID }}
ExecStart=/opt/km/bin/km-netpolicy capture-daemon
Restart=always
RestartSec=2
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now km-capture
```

No new `sidecarBuilds()` entry is needed: the daemon is a verb of the
`km-netpolicy` binary, which `0c7f9880` already ships to every sandbox. That is
the whole reason it was folded in rather than made a fourth sidecar — a binary
the userdata fetches but `km init` does not upload 404s at boot and, under
`set -e`, aborts the entire bootstrap.

- [ ] **Step 4: Regenerate goldens — read this before running anything**

Most goldens regenerate with their sanctioned capture flags. **One does not.**

`userdata_learn_v2_pre92_baseline.golden.sh` is a FROZEN baseline that the
byte-identity test deliberately strips post-baseline content from. Re-capturing
it with `CAPTURE_PRE92_BASELINE=1` folds the SubagentStop script into the
frozen file and corrupts the very thing it exists to prove. **Hand-patch that
file**: add only the new lines this task introduces, in the position the
renderer emits them, and change nothing else.

Every other golden regenerates normally:

```bash
go test ./pkg/compiler/ -run TestUserData 2>&1 | head -40
# then, for each non-frozen golden, use its own capture env var as documented
# in userdata_test.go
```

- [ ] **Step 5: Run the full compiler suite**

Run: `go test ./pkg/compiler/ -v 2>&1 | tail -30; echo "exit=$?"`
Expected: PASS. Confirm the printed exit code is 0 — piping to `tail` otherwise
reports tail's status and hides a failure.

- [ ] **Step 6: Commit**

```bash
git add pkg/compiler/
git commit -m "feat(userdata): provision flow store, append-only pin file, and capture daemon"
```

---

### Task 13: `ec2spot` v1.5.0 — captures/ and learn/ S3 grants

**Files:**
- Create: `infra/modules/ec2spot/v1.5.0/` (full copy of `v1.4.0`, then edited)
- Modify: `infra/templates/sandbox/terragrunt.hcl` (`locals.substrate_module_versions`)
- Test: `pkg/compiler/ec2spot_timeout_test.go` (retarget the version-pinned assertions)

**Interfaces:**
- Consumes: nothing in Go.
- Produces: a sandbox role that can write `captures/${sandbox_id}/*` and `learn/${sandbox_id}/*`.

- [ ] **Step 1: Copy the module**

```bash
cp -R infra/modules/ec2spot/v1.4.0 infra/modules/ec2spot/v1.5.0
find infra/modules/ec2spot/v1.5.0 -name '.terraform.lock.hcl' -delete
find infra/modules/ec2spot/v1.5.0 -name '.terraform' -type d -exec rm -rf {} +
```

The lock sweep is not optional. Stray gitignored `.terraform.lock.hcl` files
under a module source get copied into the terragrunt cache and cause
provider-checksum failures on a fresh apply.

- [ ] **Step 2: Widen the S3 statement**

In `infra/modules/ec2spot/v1.5.0/main.tf`, replace the `S3PutTranscript`
statement (v1.4.0 line ~562) with:

```hcl
      {
        Sid    = "S3PutSandboxArtifacts"
        Effect = "Allow"
        Action = ["s3:PutObject"]
        Resource = [
          "arn:aws:s3:::${var.artifacts_bucket}/transcripts/${var.sandbox_id}/*",
          "arn:aws:s3:::${var.artifacts_bucket}/captures/${var.sandbox_id}/*",
          # learn/ has been written by the eBPF enforcer's observe flush since
          # that feature shipped, with no grant to permit it — every PutObject
          # returned 403, swallowed by a Warn and masked by the SSM RunCommand
          # fallback in fetchEC2ObservedJSON. The missing grant is the defect;
          # the soft failure is correct and must stay.
          "arn:aws:s3:::${var.artifacts_bucket}/learn/${var.sandbox_id}/*",
        ]
      },
```

Every prefix stays scoped to `${var.sandbox_id}`, so one sandbox can never
write into another's artifacts.

- [ ] **Step 3: Bump the pin**

In `infra/templates/sandbox/terragrunt.hcl`, change the `ec2spot` entry in
`locals.substrate_module_versions` from `v1.4.0` to `v1.5.0`.

- [ ] **Step 4: Retarget the version-pinned test**

`pkg/compiler/ec2spot_timeout_test.go` reads a hardcoded module directory. It
silently read `v1.2.0` while the live pin was `v1.3.0` for an entire phase,
making every assertion in it inert. There is already a
`TestEC2SpotModuleDir_TracksLivePin` drift guard that parses the pin from the
sandbox template — run it and update the hardcoded path it flags.

Run: `go test ./pkg/compiler/ -run 'TestEC2Spot' -v`
Expected: the drift guard fails first naming the stale path; fix it, then all pass.

- [ ] **Step 5: Validate the Terraform**

```bash
terraform -chdir=infra/modules/ec2spot/v1.5.0 fmt -check
terraform -chdir=infra/modules/ec2spot/v1.5.0 validate
```

Expected: both clean. If `validate` needs an init, use `terraform -chdir=... init -backend=false`.

- [ ] **Step 6: Commit**

```bash
git add infra/modules/ec2spot/v1.5.0/ infra/templates/sandbox/terragrunt.hcl pkg/compiler/
git commit -m "feat(ec2spot): v1.5.0 grants captures/ and repairs the never-granted learn/ prefix"
```

---

### Task 14: Documentation

**Files:**
- Create: `docs/egress-census.md`
- Modify: `CLAUDE.md` (phase block + "Where to look" row + CLI section)
- Modify: `docs/egress-deny-lists.md` (cross-reference pins)

**Interfaces:**
- Consumes: everything above.
- Produces: the operator runbook.

- [ ] **Step 1: Write `docs/egress-census.md`**

Cover, in this order: the problem; `observed` and `flows` with real output; the
pin model with the `* ∩ observed = observed` walkthrough; collapsed-vs-`--exact`
and why the default is loose; the two sharp edges (empty census is deny-all;
pin is a snapshot, not a mode); `capture` with its bounds and the honest note
that most bytes are opaque TLS; the deploy surface; troubleshooting.

Use generic placeholder hosts throughout — `example.com`, `api.github.com`,
synthetic account IDs. Never a real tenant or customer name.

- [ ] **Step 2: Add the CLAUDE.md phase block**

Follow the house format of the existing phase blocks. It must state:
unconditional (no profile field, no dormant case); the five-site pin wiring
trap; that `pin` is irreversible with no un-pin verb; the collapsed default and
its asymmetric-risk rationale; the `learn/` grant repair; and the deploy surface
`make build` + `make build-lambdas` + `km init --dry-run=false`, **not**
`--sidecars`, with existing sandboxes needing `km destroy && km create`.

Add a "Where to look" row pointing at `docs/egress-census.md`, and add the new
verbs to the CLI section.

- [ ] **Step 3: Cross-reference from the deny-list doc**

Add a short section to `docs/egress-deny-lists.md` explaining that denies union
and pins intersect, that both narrow, and that a deny still beats a pin.

- [ ] **Step 4: Verify every documented command exists**

For each command in the new doc, confirm the verb and flags are real:

```bash
go run ./cmd/km-netpolicy --help
go run ./cmd/km-netpolicy pin --dry-run 2>&1 | head
```

Documenting a flag that does not exist is the most common drift in this repo.

- [ ] **Step 5: Commit**

```bash
git add docs/ CLAUDE.md
git commit -m "docs: operator runbook for the egress census, allowlist pinning, and capture"
```

---

### Task 15: Full verification and PR

**Files:** none created.

- [ ] **Step 1: Full build and test**

```bash
go build ./... ; echo "build exit=$?"
go test ./... -timeout 900s 2>&1 | tail -40 ; echo "test exit=${PIPESTATUS[0]}"
```

Expected: build 0, test 0. A FAIL here is a real regression — the three
historically-red packages were fixed in Phase 107. If `cmd/ttl-handler` tests
hang, they are missing a `TeardownFunc` and are hitting real IMDS.

- [ ] **Step 2: Cross-compile the sidecar the way `km init` does**

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/km-netpolicy ./cmd/km-netpolicy/
file /tmp/km-netpolicy
```

Expected: an ELF x86-64 binary. This is the exact combination the AF_PACKET
code must survive; a cgo dependency shows up here and nowhere else.

- [ ] **Step 3: Run the Linux-only tests under Docker**

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/pcap.test ./pkg/pcap/
docker run --rm -v /tmp:/t alpine /t/pcap.test -test.v
```

Compile on the host and run the binary in the container. Compiling inside qemu
crashes the Go compiler.

- [ ] **Step 4: Confirm no new dependencies**

```bash
git diff origin/main -- go.mod go.sum
```

Expected: empty. Any change means the design was misread — everything needed
was already a direct dependency.

- [ ] **Step 5: Push and open the PR**

```bash
git push -u origin feat/live-egress-census-pin-capture
gh pr create --title "feat: live egress census, allowlist pinning, and on-demand packet capture" --body "$(cat <<'BODY'
Implements `docs/superpowers/specs/2026-08-28-live-egress-census-design.md` (phase 130).

A sandbox already observed every egress decision three different ways and could
report none of it. This adds five verbs to `km-netpolicy` — which v0.8.8 just
put on every sandbox — so a box can say where it has been and narrow itself to
exactly that.

**`pin` is the load-bearing piece.** Denies union; pins intersect. Both are
monotone, so "never widens" stays a property of the data structure rather than a
rule anyone has to trust. Allow-all needs no special case: `* ∩ observed =
observed`, which is exactly the wide-open-box workflow. Hosts collapse to eTLD+1
by default with `--exact` as the opt-in, because pin is irreversible and the risk
is asymmetric — too loose costs a follow-up pin, too tight costs a
destroy/create.

**Also repairs a shipped defect.** The sandbox role's only S3 write grant was
`transcripts/`, so learn mode's flush to `learn/{id}/` has been 403-ing since it
was written, hidden by a `Warn` and the SSM fallback. `ec2spot/v1.5.0` grants it.
The swallowed error is deliberately left alone — it is why `km shell --learn`
kept working.

**Not dormant.** Observation is unconditional, so userdata goldens change for
every profile. Same reasoning `0c7f9880` used: a facility that only narrows, and
only reports what already happened, cannot widen a policy by being present.

**Deploy:** `make build` + `make build-lambdas` + `km init --dry-run=false`.
NOT `--sidecars` — userdata rides in the create-handler zip. Existing sandboxes
need `km destroy && km create`.

**Not yet done:** live UAT. The spec carries a 9-step sequence; steps 6, 7 and 9
(pin enforcement, the eBPF resolve path, and the proxy-mode path) are the ones
unit tests structurally cannot cover.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01FigukasQcuRtyoZpjrZGzm
BODY
)"
```

- [ ] **Step 6: Report back**

Post the PR URL, the final test summary, and an explicit list of what was NOT
verified — above all the live UAT steps, which need a real sandbox.
