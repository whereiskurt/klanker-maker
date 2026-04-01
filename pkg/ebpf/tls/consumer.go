//go:build linux

package tls

import (
	"bytes"
	"context"
	"encoding/binary"
	"sync/atomic"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/rs/zerolog"
)

// EventHandler is defined in github.go — reused here.
// type EventHandler func(event *TLSEvent) error

// Consumer reads TLS events from the BPF ring buffer and dispatches them
// to registered handlers. It follows the same pattern as the audit consumer
// in pkg/ebpf/audit.
type Consumer struct {
	reader   *ringbuf.Reader
	handlers []EventHandler
	logger   zerolog.Logger

	// Metrics
	eventsTotal    atomic.Uint64
	eventsOpenSSL  atomic.Uint64
	eventsGnuTLS   atomic.Uint64
	eventsNSS      atomic.Uint64
	handlerErrors  atomic.Uint64
}

// NewConsumer creates a Consumer that reads TLS events from the given ring
// buffer map. The map should be the tls_events map from the OpenSSL BPF objects.
func NewConsumer(eventsMap *ebpf.Map, logger zerolog.Logger) (*Consumer, error) {
	rd, err := ringbuf.NewReader(eventsMap)
	if err != nil {
		return nil, err
	}
	return &Consumer{
		reader: rd,
		logger: logger,
	}, nil
}

// AddHandler registers an event handler. All registered handlers are called
// for each event, in the order they were added.
func (c *Consumer) AddHandler(h EventHandler) {
	c.handlers = append(c.handlers, h)
}

// Run reads events from the ring buffer until the context is cancelled.
// It deserializes each event into a TLSEvent and dispatches to all handlers.
// Returns nil on clean shutdown.
func (c *Consumer) Run(ctx context.Context) error {
	// Watch for context cancellation to close the reader, which unblocks Read().
	go func() {
		<-ctx.Done()
		c.reader.Close()
	}()

	for {
		record, err := c.reader.Read()
		if err != nil {
			if err == ringbuf.ErrClosed {
				return nil
			}
			c.logger.Debug().Err(err).Msg("tls ring buffer read error")
			continue
		}

		var event TLSEvent
		if err := binary.Read(bytes.NewReader(record.RawSample), binary.LittleEndian, &event); err != nil {
			c.logger.Debug().Err(err).Msg("failed to decode tls event")
			continue
		}

		c.eventsTotal.Add(1)
		c.trackLibraryMetric(event.LibraryType)

		c.logger.Info().
			Uint32("pid", event.Pid).
			Str("direction", event.DirectionName()).
			Str("library", event.LibraryName()).
			Uint32("payload_len", event.PayloadLen).
			Str("remote", event.RemoteAddr().String()).
			Str("event_type", "tls_capture").
			Msg("TLS event captured")

		c.dispatchEvent(&event)
	}
}

// dispatchEvent calls all registered handlers with the event.
// Handler errors are logged but do not stop the consumer.
func (c *Consumer) dispatchEvent(event *TLSEvent) {
	for _, h := range c.handlers {
		if err := h(event); err != nil {
			c.handlerErrors.Add(1)
			c.logger.Warn().
				Err(err).
				Uint32("pid", event.Pid).
				Str("direction", event.DirectionName()).
				Str("library", event.LibraryName()).
				Msg("tls event handler error")
		}
	}
}

// trackLibraryMetric increments the per-library event counter.
func (c *Consumer) trackLibraryMetric(libType uint8) {
	switch libType {
	case LibOpenSSL:
		c.eventsOpenSSL.Add(1)
	case LibGnuTLS:
		c.eventsGnuTLS.Add(1)
	case LibNSS:
		c.eventsNSS.Add(1)
	}
}

// Stats returns current consumer metrics.
func (c *Consumer) Stats() ConsumerStats {
	return ConsumerStats{
		EventsTotal:   c.eventsTotal.Load(),
		EventsOpenSSL: c.eventsOpenSSL.Load(),
		EventsGnuTLS:  c.eventsGnuTLS.Load(),
		EventsNSS:     c.eventsNSS.Load(),
		HandlerErrors: c.handlerErrors.Load(),
	}
}

// ConsumerStats holds runtime metrics for the TLS event consumer.
type ConsumerStats struct {
	EventsTotal   uint64
	EventsOpenSSL uint64
	EventsGnuTLS  uint64
	EventsNSS     uint64
	HandlerErrors uint64
}

// Close shuts down the ring buffer reader, unblocking any pending Read() call.
func (c *Consumer) Close() error {
	return c.reader.Close()
}
