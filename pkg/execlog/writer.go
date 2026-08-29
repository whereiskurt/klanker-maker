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
