package flowlog_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

func TestWriter_AppendsJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flows.dns.jsonl")
	w := flowlog.NewWriter(path, 1<<20)
	defer w.Close()

	rec := flowlog.Record{
		TS:      time.Date(2026, 8, 28, 14, 2, 11, 0, time.UTC),
		Src:     flowlog.SrcDNS,
		Verdict: flowlog.VerdictAllow,
		Host:    "api.github.com",
	}
	if err := w.Write(rec); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Write(rec); err != nil {
		t.Fatalf("Write: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), body)
	}
	if !strings.Contains(lines[0], `"host":"api.github.com"`) {
		t.Errorf("line missing host: %s", lines[0])
	}
	// Fields the producer did not observe must be omitted entirely, never
	// emitted as zero values that a reader would mistake for observations.
	if strings.Contains(lines[0], `"addr"`) || strings.Contains(lines[0], `"pid"`) {
		t.Errorf("unobserved fields must be omitted, got: %s", lines[0])
	}
}

func TestWriter_RotatesAtCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flows.dns.jsonl")
	// Cap small enough that a couple of records force a rotation, but large
	// enough that at least one record fits: a single JSON line for this record
	// shape (RFC3339Nano timestamp included) runs ~85-95 bytes on its own, so a
	// cap of 80 could never be honored by any implementation — one record alone
	// would always exceed it. 200 fits ~2 records per generation.
	const cap = 200
	w := flowlog.NewWriter(path, cap)
	defer w.Close()

	rec := flowlog.Record{TS: time.Now().UTC(), Src: flowlog.SrcDNS, Verdict: flowlog.VerdictAllow, Host: "example.com"}
	for i := 0; i < 6; i++ {
		if err := w.Write(rec); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated file %s.1: %v", path, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat live file: %v", err)
	}
	if fi.Size() > cap {
		t.Errorf("live file %d bytes exceeds cap %d", fi.Size(), cap)
	}
}

func TestWriter_UnwritableDirDoesNotPanic(t *testing.T) {
	// A producer must never die because the flow store is unavailable.
	w := flowlog.NewWriter("/nonexistent-dir-abc/flows.dns.jsonl", 1<<20)
	defer w.Close()
	if err := w.Write(flowlog.Record{TS: time.Now(), Src: flowlog.SrcDNS, Verdict: flowlog.VerdictAllow, Host: "x.example"}); err == nil {
		t.Error("want an error surfaced to the caller for a bad path")
	}
}
