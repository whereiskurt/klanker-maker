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
