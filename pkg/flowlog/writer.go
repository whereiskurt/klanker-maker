package flowlog

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"
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

	// warnOnce makes the first write failure loud exactly once.
	warnOnce sync.Once
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
//
// Every producer discards the error at the call site for exactly that reason,
// which is how a whole enforcement mode once recorded nothing at all — the flow
// directory was root-owned while both proxies ran unprivileged, and every write
// failed EACCES in complete silence. The Writer therefore logs its OWN first
// failure, once, so a permission or disk problem is visible in the sidecar's
// journal without any producer having to remember to surface it.
func (w *Writer) Write(r Record) error {
	err := w.write(r)
	if err != nil {
		w.warnOnce.Do(func() {
			log.Warn().
				Str("event_type", "flowlog_write_failed").
				Str("path", w.path).
				Err(err).
				Msg("egress flow recording is not working; the census feeding km-netpolicy pin will be incomplete")
		})
	}
	return err
}

// write is Write without the one-shot warning, so the warning fires on the
// first genuine failure rather than on a failure to marshal a test record.
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
//
// The rotation counter is bumped alongside, because the SECOND rotation
// silently and permanently discards the oldest records — typically the
// package-install phase, which is exactly the traffic an operator wants pinned.
// `km-netpolicy pin` reads the counter and refuses to seal a box against a
// census it can prove is missing records.
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

// rotationSuffix names the sidecar file holding a producer's rotation count.
const rotationSuffix = ".rotations"

// bumpRotations increments the rotation counter beside path. Best-effort: a
// counter that cannot be written costs a warning at pin time, never a lost
// record, so a failure here must not abort a rotation that already happened.
func bumpRotations(path string) {
	n := readRotations(path)
	_ = os.WriteFile(path+rotationSuffix, []byte(strconv.Itoa(n+1)+"\n"), 0o644)
}

// readRotations returns how many times path has rotated, or 0 when the counter
// is absent or unreadable.
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

// TruncatedProducers returns the producers in dir whose census has lost records
// to rotation, mapped to their rotation count.
//
// One rotation is harmless — the previous generation is still on disk as ".1"
// and ReadDir reads it. TWO means the oldest generation was overwritten, so the
// census is missing its earliest destinations with no other signal that
// anything was lost. That matters here more than it would anywhere else,
// because the census feeds an irreversible operation.
func TruncatedProducers(dir string) map[string]int {
	out := map[string]int{}
	for _, src := range []string{SrcEBPF, SrcDNS, SrcHTTP, SrcResolver} {
		if n := readRotations(FileFor(dir, src)); n >= 2 {
			out[src] = n
		}
	}
	return out
}

// RotatedProducers returns the producers in dir that have rotated at least
// once. Nothing is lost yet at one rotation, but the next one will lose the
// earliest records, which is worth saying before an irreversible pin.
func RotatedProducers(dir string) []string {
	var out []string
	for _, src := range []string{SrcEBPF, SrcDNS, SrcHTTP, SrcResolver} {
		if readRotations(FileFor(dir, src)) >= 1 {
			out = append(out, src)
		}
	}
	return out
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
