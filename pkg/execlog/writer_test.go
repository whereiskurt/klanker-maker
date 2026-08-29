package execlog_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
)

func TestWriter_AppendsJSONL(t *testing.T) {
	dir := t.TempDir()
	p := execlog.Path(dir)
	w := execlog.NewWriter(p, execlog.DefaultMaxBytes)
	defer w.Close()

	if err := w.Write(execlog.Record{
		TS: time.Now(), Kind: execlog.KindExec, PID: 42, UID: 1000,
		Comm: "curl", Args: []string{"curl", "https://api.github.com"},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Fatalf("want 1 line, got %d: %s", n, b)
	}
	if !strings.Contains(string(b), `"kind":"exec"`) {
		t.Errorf("record missing kind: %s", b)
	}
}

func TestWriter_RotatesAndCountsRotations(t *testing.T) {
	dir := t.TempDir()
	p := execlog.Path(dir)
	// A cap small enough that the second record forces a rotation.
	w := execlog.NewWriter(p, 120)
	defer w.Close()

	for i := 0; i < 6; i++ {
		if err := w.Write(execlog.Record{
			TS: time.Now(), Kind: execlog.KindExec, PID: i, Comm: "sh",
		}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if _, err := os.Stat(p + ".1"); err != nil {
		t.Fatalf("want a rotated generation on disk: %v", err)
	}
	// The counter is what `pin`-style callers read to know the census lost
	// records; a rotation that does not bump it is silent data loss.
	if got := execlog.Rotations(dir); got < 1 {
		t.Errorf("want rotations >= 1, got %d", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "execs.jsonl.rotations")); err != nil {
		t.Errorf("rotation counter file missing: %v", err)
	}
}
