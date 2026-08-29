//go:build linux && amd64

package exec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"
	"github.com/rs/zerolog/log"
	"golang.org/x/sys/unix"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
)

// ErrUnsupported is returned when exec tracing is not available. On this build
// it can still surface if the kernel rejects the programs — every setup
// failure in NewTracer wraps it, so a caller can use errors.Is(err,
// exec.ErrUnsupported) to tell "cannot run here" from a bug in the caller,
// on the platform where the daemon actually runs.
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
	// Mirrors the C struct's explicit _pad2[4]. C pads exec_hdr to 56 for its
	// 8-byte alignment; Go's encoding/binary packs without padding, so without
	// this the argv slice would start 4 bytes early and every argument would
	// decode as garbage.
	_ [4]byte
}

// Tracer loads the execve tracepoints and streams decoded records.
type Tracer struct {
	objs   execbpfObjects
	links  []link.Link
	rd     *ringbuf.Reader
	out    chan execlog.Record
	closed chan struct{}

	// wg tracks the drain goroutine, so Close can block until it has actually
	// exited (and out is closed) instead of returning while it might still be
	// mid-decode or mid-send — provable rather than merely argued-safe.
	wg sync.WaitGroup

	// stallOnce makes the first time a slow consumer stalls the drain loop
	// loud exactly once, mirroring flowlog.Writer's warnOnce.
	stallOnce sync.Once
}

// NewTracer loads the BPF programs, attaches all five tracepoints, and starts
// draining the ring buffer.
func NewTracer() (*Tracer, error) {
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("remove memlock: %w: %w", ErrUnsupported, err)
	}

	t := &Tracer{out: make(chan execlog.Record, 1024), closed: make(chan struct{})}
	if err := loadExecbpfObjects(&t.objs, nil); err != nil {
		return nil, fmt.Errorf("load exec bpf objects: %w: %w", ErrUnsupported, err)
	}

	links, err := t.attachAll()
	if err != nil {
		t.objs.Close()
		return nil, err // already wraps ErrUnsupported, see attachAll
	}
	t.links = links

	rd, err := ringbuf.NewReader(t.objs.ExecEvents)
	if err != nil {
		t.closeLinks()
		t.objs.Close()
		return nil, fmt.Errorf("open ringbuf: %w: %w", ErrUnsupported, err)
	}
	t.rd = rd

	t.wg.Add(1)
	go t.drain()
	return t, nil
}

// Events streams decoded records until Close.
func (t *Tracer) Events() <-chan execlog.Record { return t.out }

// Close detaches everything and stops the drain goroutine.
//
// It waits for drain to actually exit before tearing down links and objects,
// and Events() is guaranteed closed by the time Close returns — a caller does
// not have to guess whether a record is still in flight.
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
	t.wg.Wait()
	t.closeLinks()
	return t.objs.Close()
}

func (t *Tracer) closeLinks() {
	for _, l := range t.links {
		l.Close()
	}
	t.links = nil
}

// attachAll attaches the five tracepoints, unwinding cleanly if any fails.
//
// A partial attach is never left in place: four of five tracepoints would
// produce a trace that looks complete and silently is not — the exact failure
// shape this phase exists to eliminate.
func (t *Tracer) attachAll() ([]link.Link, error) {
	specs := []struct {
		group, name string
		prog        *ebpf.Program
	}{
		{"syscalls", "sys_enter_execve", t.objs.TpEnterExecve},
		{"syscalls", "sys_enter_execveat", t.objs.TpEnterExecveat},
		{"syscalls", "sys_exit_execve", t.objs.TpExitExecve},
		// tp_exit_execve looks up its inflight entry by pid_tgid and never
		// inspects which syscall produced it, and sys_exit_execveat's
		// trace_event_raw_sys_exit context has the identical shape (id, ret)
		// as sys_exit_execve's — so the SAME program is attached again here,
		// under its own tracepoint, rather than duplicating it in exec.c.
		// Without this, sys_enter_execveat's stashed inflight entry is never
		// looked up or deleted: every execveat/fexecve is captured on entry
		// and then silently dropped, leaking one LRU slot per call until
		// eviction.
		{"syscalls", "sys_exit_execveat", t.objs.TpExitExecve},
		{"sched", "sched_process_exit", t.objs.TpProcessExit},
	}
	var links []link.Link
	for _, s := range specs {
		l, err := link.Tracepoint(s.group, s.name, s.prog, nil)
		if err != nil {
			for _, done := range links {
				done.Close()
			}
			return nil, fmt.Errorf("attach %s/%s: %w: %w", s.group, s.name, ErrUnsupported, err)
		}
		links = append(links, l)
	}
	return links, nil
}

// drain reads the ring buffer until it is closed, decoding each record.
//
// A malformed or short record is SKIPPED, never fatal: losing one event must
// never stop the trace, for the same reason a producer never lets a failed flow
// write abort an egress decision.
func (t *Tracer) drain() {
	defer t.wg.Done()
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
		default:
			// out is full: the consumer is not keeping up and the kernel ring
			// keeps filling behind this loop. BPF_MAP_TYPE_RINGBUF exposes no
			// drop counter to userspace — exec.c's bpf_ringbuf_output() simply
			// fails silently when the ring has no room — so a stalled
			// consumer is the only signal available here at all. Staying
			// silent about it would reproduce the exact defect this phase
			// exists to eliminate: a store that reports nothing happened when
			// what really happened is that nobody was listening fast enough.
			// Block after warning, same as the two-case select above, rather
			// than dropping the record outright.
			t.stallOnce.Do(func() {
				log.Warn().
					Str("event_type", "exec_tracer_consumer_stalled").
					Msg("exec tracer's consumer is not keeping up; the kernel ring buffer may now be dropping events")
			})
			select {
			case t.out <- r:
			case <-t.closed:
				return
			}
		}
	}
}

// decode turns one ring-buffer sample into a record. h.Kind is what
// distinguishes an exec record from an exit record; the sample's length is
// used only as a header-sized floor before decoding, never to tell the two
// apart — an exit record happens to always be exactly header-sized, but
// nothing about the wire format requires that of an exec record too.
func (t *Tracer) decode(b []byte) (execlog.Record, bool) {
	var h rawHdr
	if len(b) < binary.Size(h) {
		return execlog.Record{}, false
	}
	if err := binary.Read(bytes.NewReader(b), binary.LittleEndian, &h); err != nil {
		return execlog.Record{}, false
	}

	r := execlog.Record{
		TS:        wallTimeOf(h.TSNS),
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

// wallTimeOf converts a kernel CLOCK_BOOTTIME stamp into wall-clock time by
// measuring backwards from now, rather than forwards from a boot instant
// computed once at startup.
//
// This shape is deliberate and EC2-specific. km sandboxes hibernate and resume
// (`spec.runtime.hibernation`, set on the dc34 lineage), and they run chrony,
// so wall clock is stepped by NTP after a resume while the daemon process keeps
// running. A boot instant captured at daemon start would silently skew every
// timestamp after the first hibernate by however far the clock was corrected —
// and the join is a time-window comparison, so skew there does not look like a
// bug, it looks like flows belonging to different processes.
//
// CLOCK_BOOTTIME rather than CLOCK_MONOTONIC because BOOTTIME includes suspend:
// a monotonic clock that stops across hibernate would compress hours of trace
// into an instant.
//
// The cost is one clock_gettime per record, which is a vDSO call (~20ns) and
// nothing beside the JSON marshal and file write that follow it.
func wallTimeOf(evBootNS uint64) time.Time {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); err != nil {
		// Falling back to now is honest: the record is being decoded within
		// microseconds of the event, so now is a very good approximation, and
		// a zero time would silently drop the record out of every window query.
		return time.Now()
	}
	return time.Now().Add(-(time.Duration(uint64(ts.Nano()) - evBootNS)))
}
