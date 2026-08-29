//go:build linux && amd64

package main

import (
	"sync"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
)

// fakeTracer mimics *ebpfexec.Tracer's concurrency shape closely enough to
// exercise drainRemaining's shutdown sequence without a kernel:
//
//   - Events() is backed by a channel that starts already full of records the
//     "kernel" had already decoded before Close was called — exactly what a
//     real burst of execs sitting in the tracer's buffered channel looks like.
//   - A background goroutine plays the part of the real drain loop, still
//     trying to hand off one more record when Close is called. It blocks on
//     that send until Close's own escape valve — the same <-closed pattern
//     drain's second select uses — releases it, never on a reader.
//   - Close waits for that goroutine to actually exit (via a WaitGroup) before
//     returning and closing Events(), mirroring Tracer.Close's own ordering.
type fakeTracer struct {
	out    chan execlog.Record
	closed chan struct{}
	wg     sync.WaitGroup
}

// newFakeTracer pre-loads buffered into a full channel (already decoded,
// nobody has read them yet) and starts a goroutine that will try to also send
// stalled once buffered's capacity is exhausted — standing in for the real
// tracer's drain loop still running when the signal arrives.
func newFakeTracer(buffered []execlog.Record, stalled execlog.Record) *fakeTracer {
	ft := &fakeTracer{
		out:    make(chan execlog.Record, len(buffered)),
		closed: make(chan struct{}),
	}
	for _, r := range buffered {
		ft.out <- r
	}
	ft.wg.Add(1)
	go func() {
		defer ft.wg.Done()
		defer close(ft.out)
		select {
		case ft.out <- stalled:
		case <-ft.closed:
			// Abandoned, exactly as the real drain loop abandons a record when
			// Close fires while it is blocked trying to hand one off.
		}
	}()
	return ft
}

func (f *fakeTracer) Events() <-chan execlog.Record { return f.out }

func (f *fakeTracer) Close() error {
	select {
	case <-f.closed:
	default:
		close(f.closed)
	}
	f.wg.Wait()
	return nil
}

// TestDrainRemaining_NoLossNoDeadlock proves the fix: on shutdown, every
// record already decoded and buffered survives, and closing the tracer first
// never deadlocks against a producer that is concurrently, and permanently,
// blocked trying to hand off one more.
func TestDrainRemaining_NoLossNoDeadlock(t *testing.T) {
	buffered := []execlog.Record{
		{Kind: execlog.KindExec, Comm: "already-decoded-1"},
		{Kind: execlog.KindExec, Comm: "already-decoded-2"},
	}
	stalled := execlog.Record{Kind: execlog.KindExec, Comm: "would-be-next"}
	tr := newFakeTracer(buffered, stalled)

	dir := t.TempDir()
	w := execlog.NewWriter(execlog.Path(dir), execlog.DefaultMaxBytes)

	done := make(chan struct{})
	go func() {
		drainRemaining(w, tr)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("drainRemaining did not return — shutdown deadlocked")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	got, err := execlog.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(got) != len(buffered) {
		t.Fatalf("wrote %d record(s), want exactly the %d that were already buffered before Close; got %+v",
			len(got), len(buffered), got)
	}
	seen := make(map[string]bool, len(got))
	for _, r := range got {
		seen[r.Comm] = true
	}
	for _, want := range buffered {
		if !seen[want.Comm] {
			t.Errorf("missing already-buffered record %q — a routine stop must not lose it", want.Comm)
		}
	}
	if seen[stalled.Comm] {
		t.Errorf("found the stalled record %q — it was never actually decoded before Close and must not appear",
			stalled.Comm)
	}
}
