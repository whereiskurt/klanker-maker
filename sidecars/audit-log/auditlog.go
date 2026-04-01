// Package auditlog implements the km audit-log sidecar.
// It reads JSON-line audit events from an io.Reader (stdin in production)
// and routes them to a configured destination: stdout, CloudWatch Logs, or S3.
//
// JSON event schema (LOCKED — matches 03-CONTEXT.md):
//
//	{
//	  "timestamp":  "2026-03-21T12:00:00Z",
//	  "sandbox_id": "sb-a1b2c3d4",
//	  "event_type": "shell_command" | "dns_query" | "http_request",
//	  "source":     "audit-log" | "dns-proxy" | "http-proxy",
//	  "detail":     { ... event-specific fields ... }
//	}
package auditlog

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// AuditEvent is the canonical JSON schema for audit log events.
type AuditEvent struct {
	Timestamp time.Time              `json:"timestamp"`
	SandboxID string                 `json:"sandbox_id"`
	EventType string                 `json:"event_type"` // shell_command, dns_query, http_request
	Source    string                 `json:"source"`
	Detail    map[string]interface{} `json:"detail"`
}

// Destination receives audit events.
type Destination interface {
	Write(ctx context.Context, event AuditEvent) error
	Flush(ctx context.Context) error
}

// CloudWatchBackend is the minimal interface for pushing log messages.
// It allows mock injection in tests without depending on the AWS SDK directly.
type CloudWatchBackend interface {
	// EnsureLogGroup creates the log group and stream if they do not exist.
	EnsureLogGroup(ctx context.Context, logGroup, logStream string) error
	// PutLogMessages sends messages to the given log group/stream.
	PutLogMessages(ctx context.Context, logGroup, logStream string, messages []string) error
}

// Process reads newline-delimited JSON from r, parses each line as an AuditEvent,
// and routes it to dest. Invalid JSON lines emit a zerolog warning and are skipped.
// Process returns when r is exhausted (EOF) or on a read error.
func Process(ctx context.Context, r io.Reader, dest Destination) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var ev AuditEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			log.Warn().Str("raw", string(line)).Err(err).Msg("audit-log: skipping invalid JSON line")
			continue
		}

		if err := dest.Write(ctx, ev); err != nil {
			log.Error().Err(err).Msg("audit-log: failed to write event to destination")
		}
	}
	return scanner.Err()
}

// ---- StdoutDest ----

// stdoutDest writes JSON-marshaled events as newline-delimited JSON to w.
// In production, w is os.Stdout.
type stdoutDest struct {
	w io.Writer
}

// NewStdoutDest creates a Destination that writes JSON events to w.
// In production, pass os.Stdout.
func NewStdoutDest(w io.Writer) Destination {
	return &stdoutDest{w: w}
}

// Write encodes event as JSON and writes it followed by a newline.
func (d *stdoutDest) Write(_ context.Context, event AuditEvent) error {
	b, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("audit-log stdout: marshal event: %w", err)
	}
	b = append(b, '\n')
	_, err = d.w.Write(b)
	return err
}

// Flush is a no-op for stdout.
func (d *stdoutDest) Flush(_ context.Context) error { return nil }

// ---- cloudWatchDest ----

// cloudWatchDest buffers events and flushes them to CloudWatch Logs.
// It flushes on every Flush() call, or when the buffer reaches cwFlushThreshold.
type cloudWatchDest struct {
	backend   CloudWatchBackend
	logGroup  string
	logStream string
	buf       []AuditEvent
}

const cwFlushThreshold = 25

// NewCloudWatchDest creates a Destination that buffers events and sends them to CloudWatch.
// backend provides PutLogMessages and EnsureLogGroup (use a real CW client or a mock).
// logGroup is e.g. "/km/sandboxes/sb-a1b2c3d4/" and logStream is "audit".
func NewCloudWatchDest(backend CloudWatchBackend, logGroup, logStream string) Destination {
	return &cloudWatchDest{
		backend:   backend,
		logGroup:  logGroup,
		logStream: logStream,
	}
}

// Write appends event to the internal buffer. If the buffer reaches
// cwFlushThreshold, it is flushed immediately.
func (d *cloudWatchDest) Write(ctx context.Context, event AuditEvent) error {
	d.buf = append(d.buf, event)
	if len(d.buf) >= cwFlushThreshold {
		return d.flush(ctx)
	}
	return nil
}

// Flush sends all buffered events to CloudWatch and clears the buffer.
func (d *cloudWatchDest) Flush(ctx context.Context) error {
	return d.flush(ctx)
}

func (d *cloudWatchDest) flush(ctx context.Context) error {
	if len(d.buf) == 0 {
		return nil
	}

	msgs := make([]string, 0, len(d.buf))
	for _, ev := range d.buf {
		b, err := json.Marshal(ev)
		if err != nil {
			log.Warn().Err(err).Msg("audit-log: skipping unmarshalable event on flush")
			continue
		}
		msgs = append(msgs, string(b))
	}
	d.buf = d.buf[:0]

	if len(msgs) == 0 {
		return nil
	}

	if err := d.backend.PutLogMessages(ctx, d.logGroup, d.logStream, msgs); err != nil {
		return fmt.Errorf("audit-log CloudWatch flush: %w", err)
	}
	return nil
}

// ---- RedactingDestination ----

// RedactingDestination wraps another Destination and redacts secrets in the
// event Detail map before forwarding. It replaces:
//   - AWS access key IDs (AKIA...)
//   - Bearer tokens
//   - Hex strings of 40+ characters
//   - Literal secret values provided at construction (e.g. SSM secrets)
//
// Structural fields (SandboxID, EventType, Timestamp, Source) are never modified.
// Regex patterns are compiled once at construction and are safe for concurrent use.
type RedactingDestination struct {
	inner    Destination
	patterns []*regexp.Regexp
	literals []string
}

// NewRedactingDestination creates a RedactingDestination wrapping inner.
// literals is a list of exact secret strings (e.g. SSM values) to redact.
// Pass nil or an empty slice if no literals are needed.
func NewRedactingDestination(inner Destination, literals []string) *RedactingDestination {
	return &RedactingDestination{
		inner:    inner,
		patterns: compileDefaultPatterns(),
		literals: literals,
	}
}

// compileDefaultPatterns returns the three default redaction regex patterns.
// Patterns are compiled once and are safe for concurrent use.
func compileDefaultPatterns() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`AKIA[A-Z0-9]{16}`),
		regexp.MustCompile(`Bearer [A-Za-z0-9\-._~+/]+=*`),
		regexp.MustCompile(`[0-9a-f]{40,}`),
	}
}

// redactString replaces literal secrets first, then applies regex patterns.
// Each match is replaced with "[REDACTED]".
func redactString(s string, patterns []*regexp.Regexp, literals []string) string {
	for _, lit := range literals {
		if lit != "" {
			s = strings.ReplaceAll(s, lit, "[REDACTED]")
		}
	}
	for _, p := range patterns {
		s = p.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}

// redactValue recursively redacts secrets within an interface{} value.
// Strings are redacted directly; maps and slices are recursed into.
// Non-string scalar values (numbers, booleans) are passed through unchanged.
func redactValue(v interface{}, patterns []*regexp.Regexp, literals []string) interface{} {
	switch val := v.(type) {
	case string:
		return redactString(val, patterns, literals)
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, item := range val {
			out[k] = redactValue(item, patterns, literals)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, item := range val {
			out[i] = redactValue(item, patterns, literals)
		}
		return out
	default:
		return v
	}
}

// Write clones event.Detail, applies redaction to all values, then forwards
// the modified event to the inner Destination. Structural fields are untouched.
func (d *RedactingDestination) Write(ctx context.Context, event AuditEvent) error {
	// Clone the detail map so the original event is not mutated.
	redactedDetail := make(map[string]interface{}, len(event.Detail))
	for k, v := range event.Detail {
		redactedDetail[k] = redactValue(v, d.patterns, d.literals)
	}

	// Build a new event with the redacted detail; structural fields copied as-is.
	redacted := AuditEvent{
		Timestamp: event.Timestamp,
		SandboxID: event.SandboxID,
		EventType: event.EventType,
		Source:    event.Source,
		Detail:    redactedDetail,
	}

	return d.inner.Write(ctx, redacted)
}

// Flush delegates to the inner Destination.
func (d *RedactingDestination) Flush(ctx context.Context) error {
	return d.inner.Flush(ctx)
}

// ---- s3Dest ----

// S3Backend is the minimal interface for uploading objects to S3.
// Implemented by *s3.Client; allows mock injection in tests.
type S3Backend interface {
	PutObject(ctx context.Context, bucket, key string, data []byte) error
}

// S3BackendFactory creates an S3Backend on demand. Used for lazy initialization
// so the sidecar can start before credentials are available.
type S3BackendFactory func() (S3Backend, error)

// s3Dest buffers audit events in memory and flushes them to S3 as NDJSON files.
// Each flush produces one S3 object at: audit/{sandboxID}/{timestamp}.ndjson
// Events are also written to stdout as a local fallback (visible in docker logs).
// The S3 client is initialized lazily on first flush to handle credential race conditions
// (cred-refresh container may not have written credentials when audit-log starts).
type s3Dest struct {
	backend   S3Backend
	factory   S3BackendFactory // lazy init — called on first flush if backend is nil
	bucket    string
	sandboxID string
	buf       []AuditEvent
	stdout    Destination // also write to stdout for docker logs visibility
}

// NewS3Dest creates a Destination that buffers events and flushes to S3.
// Events are also written to stdout for local docker log visibility.
// The S3Backend can be nil — if a factory is provided via NewS3DestLazy,
// it will be initialized on first flush.
func NewS3Dest(backend S3Backend, bucket, sandboxID string, w io.Writer) Destination {
	return &s3Dest{
		backend:   backend,
		bucket:    bucket,
		sandboxID: sandboxID,
		stdout:    NewStdoutDest(w),
	}
}

// NewS3DestLazy creates an S3 Destination with lazy backend initialization.
// The factory is called on the first flush, giving the cred-refresh container
// time to write credentials before the S3 client is constructed.
func NewS3DestLazy(factory S3BackendFactory, bucket, sandboxID string, w io.Writer) Destination {
	return &s3Dest{
		factory:   factory,
		bucket:    bucket,
		sandboxID: sandboxID,
		stdout:    NewStdoutDest(w),
	}
}

// Write appends the event to the buffer and also writes to stdout.
// Flushes to S3 when buffer reaches s3FlushThreshold.
func (d *s3Dest) Write(ctx context.Context, event AuditEvent) error {
	// Always echo to stdout for docker logs
	_ = d.stdout.Write(ctx, event)

	d.buf = append(d.buf, event)
	if len(d.buf) >= s3FlushThreshold {
		return d.flush(ctx)
	}
	return nil
}

const s3FlushThreshold = 50

// Flush sends all buffered events to S3 as a single NDJSON object.
func (d *s3Dest) Flush(ctx context.Context) error {
	return d.flush(ctx)
}

func (d *s3Dest) ensureBackend() error {
	if d.backend != nil {
		return nil
	}
	if d.factory == nil {
		return fmt.Errorf("no S3 backend or factory configured")
	}
	backend, err := d.factory()
	if err != nil {
		return fmt.Errorf("lazy S3 backend init: %w", err)
	}
	d.backend = backend
	log.Info().Str("bucket", d.bucket).Msg("audit-log: S3 backend initialized (lazy)")
	return nil
}

func (d *s3Dest) flush(ctx context.Context) error {
	if len(d.buf) == 0 {
		return nil
	}

	if err := d.ensureBackend(); err != nil {
		log.Warn().Err(err).Int("buffered", len(d.buf)).Msg("audit-log: S3 not ready, events buffered for next flush")
		return nil // don't drop events — keep buffer, retry next flush
	}

	// Build NDJSON payload
	var payload []byte
	for _, ev := range d.buf {
		b, err := json.Marshal(ev)
		if err != nil {
			log.Warn().Err(err).Msg("audit-log: skipping unmarshalable event on S3 flush")
			continue
		}
		payload = append(payload, b...)
		payload = append(payload, '\n')
	}
	count := len(d.buf)
	d.buf = d.buf[:0]

	if len(payload) == 0 {
		return nil
	}

	// S3 key: audit/{sandboxID}/{timestamp}.ndjson
	key := fmt.Sprintf("audit/%s/%s.ndjson", d.sandboxID, time.Now().UTC().Format("20060102T150405Z"))

	if err := d.backend.PutObject(ctx, d.bucket, key, payload); err != nil {
		log.Error().Err(err).Str("key", key).Int("events", count).Msg("audit-log: S3 flush failed")
		return fmt.Errorf("audit-log S3 flush: %w", err)
	}

	log.Info().Str("key", key).Int("events", count).Msg("audit-log: flushed to S3")
	return nil
}
