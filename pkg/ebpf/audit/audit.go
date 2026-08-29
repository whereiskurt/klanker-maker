//go:build linux

// Package audit provides a ring buffer consumer for eBPF network deny events.
// It reads from the BPF ring buffer map populated by the enforcer's BPF programs
// and emits structured JSON audit log entries for each event.
package audit

import (
	"bytes"
	"context"
	"encoding/binary"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/rs/zerolog"
	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

// bpfEvent mirrors the BPF-side struct event defined in common.h.
// Fields match the C layout (little-endian, no padding between uint32 fields).
// Layout:
//
//	uint64 timestamp   — nanoseconds since boot
//	uint32 pid         — process ID
//	uint32 src_ip      — source IPv4 in network byte order
//	uint32 dst_ip      — destination IPv4 in network byte order
//	uint16 dst_port    — destination port in network byte order
//	uint8  action      — ActionDeny/Allow/Redirect
//	uint8  layer       — LayerConnect4/Sendmsg4/EgressSKB/Sockops
//	uint8  comm[16]    — process name (null-terminated)
type bpfEvent struct {
	Timestamp uint64
	Pid       uint32
	SrcIP     uint32
	DstIP     uint32
	DstPort   uint16
	Action    uint8
	Layer     uint8
	Comm      [16]byte
}

// Consumer reads events from the eBPF ring buffer and emits structured audit logs.
type Consumer struct {
	reader    *ringbuf.Reader
	sandboxID string
	logger    zerolog.Logger

	flows  *flowlog.Writer
	nameFn flowlog.NameFn
}

// NewConsumer creates a Consumer that reads from the given eBPF ring buffer map.
// eventsMap must be the "events" map from the enforcer's BPF object collection.
func NewConsumer(eventsMap *ebpf.Map, sandboxID string, logger zerolog.Logger) (*Consumer, error) {
	return NewConsumerWithFlows(eventsMap, sandboxID, logger, nil, nil)
}

// NewConsumerWithFlows is NewConsumer plus best-effort flow-record emission.
// flows may be nil (flow recording disabled), which makes the Consumer
// byte-identical to NewConsumer — a flow store that cannot be written must
// never affect enforcement, and the ring buffer must keep draining either way.
//
// nameFn resolves a destination address to the domain that was looked up to
// reach it (see resolver.Allowlist.NameForIP). It may be nil, or return "" for
// an address it never handed out; both leave the record's Host empty rather
// than fabricating a name — see flowlog.NameFn's doc comment for why a wrong
// hostname is worse than an absent one here.
func NewConsumerWithFlows(eventsMap *ebpf.Map, sandboxID string, logger zerolog.Logger, flows *flowlog.Writer, nameFn flowlog.NameFn) (*Consumer, error) {
	rd, err := ringbuf.NewReader(eventsMap)
	if err != nil {
		return nil, err
	}
	return &Consumer{
		reader:    rd,
		sandboxID: sandboxID,
		logger:    logger,
		flows:     flows,
		nameFn:    nameFn,
	}, nil
}

// Run reads events from the ring buffer until the context is cancelled or
// the ring buffer is closed. It logs each event as a structured JSON entry.
// Returns nil on clean shutdown (ErrClosed or ctx.Done).
func (c *Consumer) Run(ctx context.Context) error {
	doneCh := ctx.Done()
	for {
		// Check context before blocking read.
		select {
		case <-doneCh:
			return nil
		default:
		}

		record, err := c.reader.Read()
		if err != nil {
			if err == ringbuf.ErrClosed {
				return nil
			}
			// Transient errors: log and continue.
			c.logger.Debug().Err(err).Msg("ring buffer read error")
			continue
		}

		var event bpfEvent
		if err := binary.Read(bytes.NewBuffer(record.RawSample), binary.LittleEndian, &event); err != nil {
			c.logger.Debug().Err(err).Msg("failed to decode bpf event")
			continue
		}

		c.handleEvent(event)
	}
}

// handleEvent emits the structured audit log entry for one decoded ring-buffer
// event, then best-effort records it as a flow observation. Split out of Run
// so the flow-recording logic is directly testable without a real ring buffer.
func (c *Consumer) handleEvent(event bpfEvent) {
	comm := nullTermString(event.Comm[:])
	srcIP := uint32ToIP(event.SrcIP)
	dstIP := uint32ToIP(event.DstIP)

	entry := c.logger.With().
		Str("event_type", "ebpf_network_deny").
		Str("sandbox_id", c.sandboxID).
		Uint32("pid", event.Pid).
		Str("src_ip", srcIP.String()).
		Str("dst_ip", dstIP.String()).
		Uint16("dst_port", event.DstPort).
		Str("action", actionString(event.Action)).
		Str("layer", layerString(event.Layer)).
		Str("comm", comm).
		Logger()

	switch event.Action {
	case actionDeny:
		entry.Warn().Msg("ebpf network deny")
	default:
		entry.Debug().Msg("ebpf network event")
	}

	// Best-effort, exactly as in the proxies: the ring buffer must keep
	// draining even if the flow store cannot be written. A blocked consumer
	// means dropped events, which is worse than missing observability.
	if c.flows != nil {
		addr := dstIP.String()
		host := ""
		if c.nameFn != nil {
			host = c.nameFn(addr)
		}
		_ = c.flows.Write(flowlog.Record{
			TS:      time.Now().UTC(),
			Src:     flowlog.SrcEBPF,
			Verdict: verdictFor(event.Action),
			Host:    host,
			Addr:    addr,
			Port:    int(event.DstPort),
			Proto:   "tcp",
			PID:     int(event.Pid),
			Comm:    comm,
		})
	}
}

// Close shuts down the ring buffer reader, unblocking any pending Read() call.
func (c *Consumer) Close() error {
	return c.reader.Close()
}

