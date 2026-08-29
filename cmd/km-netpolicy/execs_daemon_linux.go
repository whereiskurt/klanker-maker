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

// execTracer is the subset of *ebpfexec.Tracer that the drain loop needs. It
// exists so the shutdown sequence in drainRemaining can be exercised by a test
// without a kernel — production always calls runExecsDaemon, which only ever
// hands it a real *ebpfexec.Tracer.
type execTracer interface {
	Events() <-chan execlog.Record
	Close() error
}

// runExecsDaemon loads the tracepoints and appends every record to the store
// until it is signalled to stop.
//
// It runs as root under km-execlog.service. Unlike the enforcer it makes no
// decisions and holds no state a restart would lose, so a crash costs a gap in
// the trace and nothing else — which is why the unit restarts it rather than
// treating a failure as fatal to the sandbox.
func runExecsDaemon(o opts) error {
	tr, err := ebpfexec.NewTracer()
	if err != nil {
		return fmt.Errorf("start exec tracer: %w", err)
	}
	defer tr.Close()

	w := execlog.NewWriter(execlog.Path(o.execDir), execlog.DefaultMaxBytes)
	defer w.Close()

	fmt.Fprintf(o.stdout, "exec tracing started (store: %s)\n", execlog.Path(o.execDir))

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
			// SIGTERM is the routine stop path — every systemd restart, package
			// upgrade, `km stop`, and `km pause` — not the rare crash the comment
			// above excuses. Abandoning what is already decoded and sitting in
			// the tracer's buffered channel here would turn a bounded, routine
			// stop into a truncated trace, and Task 8 wires ExecStop to upload
			// exactly this store — a truncated tail is what an operator would be
			// reading afterwards.
			drainRemaining(w, tr)
			return nil
		}
	}
}

// drainRemaining runs the exact shutdown sequence: close the tracer FIRST so
// it stops producing, then drain whatever it had already decoded and buffered
// before Close was called, writing each record. Split out from the sigs case
// above so the no-loss / no-deadlock guarantee can be tested directly against
// a fake tracer, without a kernel and without racing a real os.Signal delivery
// against the daemon's own startup.
//
// This cannot deadlock against *ebpfexec.Tracer as it stands today
// (pkg/ebpf/exec/tracer_linux.go, commit 3842652e): Tracer.Close closes its
// internal "closed" signal channel BEFORE it closes the ring reader or waits
// on its drain goroutine's WaitGroup. Every place drain's own select can block
// trying to send a decoded record into Events() carries a matching `case
// <-t.closed: return` — so once Close has closed that channel, drain's next
// blocked send resolves immediately via that case instead of waiting for a
// reader, and it never depends on this loop still being the one pulling
// events. Tracer.Close only returns once that drain goroutine has actually
// exited (via its WaitGroup), and drain closes Events() before signalling the
// WaitGroup (its `defer close(t.out)` is registered after `defer
// t.wg.Done()`, so it runs first on the LIFO unwind) — so by the time Close
// returns here, Events() is already closed and holds exactly the records that
// were fully decoded and handed to it before the close, no more and no less.
// The `for range` below therefore drains a already-closed, already-final
// channel and returns as soon as it is empty; it is not depending on the
// tracer to still be producing anything.
func drainRemaining(w *execlog.Writer, tr execTracer) {
	tr.Close()
	for r := range tr.Events() {
		_ = w.Write(r)
	}
}
