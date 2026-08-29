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
