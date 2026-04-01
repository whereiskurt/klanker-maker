//go:build linux

// Package audit provides a ring buffer consumer for eBPF network deny events.
// It reads from the BPF ring buffer map populated by the enforcer's BPF programs
// and emits structured JSON audit log entries for each event.
package audit

import (
	"bytes"
	"context"
	"encoding/binary"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/rs/zerolog"
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
}

// NewConsumer creates a Consumer that reads from the given eBPF ring buffer map.
// eventsMap must be the "events" map from the enforcer's BPF object collection.
func NewConsumer(eventsMap *ebpf.Map, sandboxID string, logger zerolog.Logger) (*Consumer, error) {
	rd, err := ringbuf.NewReader(eventsMap)
	if err != nil {
		return nil, err
	}
	return &Consumer{
		reader:    rd,
		sandboxID: sandboxID,
		logger:    logger,
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
	}
}

// Close shuts down the ring buffer reader, unblocking any pending Read() call.
func (c *Consumer) Close() error {
	return c.reader.Close()
}

