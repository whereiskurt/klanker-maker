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
			return nil
		}
	}
}
