package execlog_test

import (
	"os"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
)

func TestReadDir_ReadsLiveAndRotatedOldestFirst(t *testing.T) {
	dir := t.TempDir()
	p := execlog.Path(dir)

	// A rotated generation holding the older record, and a live file holding
	// the newer one. ReadDir must return both, oldest first.
	older := `{"ts":"2026-08-29T10:00:00Z","kind":"exec","pid":1,"uid":0,"comm":"old","ret":0}`
	newer := `{"ts":"2026-08-29T11:00:00Z","kind":"exec","pid":2,"uid":0,"comm":"new","ret":0}`
	if err := os.WriteFile(p+".1", []byte(older+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(newer+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	recs, err := execlog.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d: %+v", len(recs), recs)
	}
	if recs[0].Comm != "old" || recs[1].Comm != "new" {
		t.Errorf("want oldest-first, got %s then %s", recs[0].Comm, recs[1].Comm)
	}
	if !recs[0].TS.Equal(time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("timestamp not parsed: %v", recs[0].TS)
	}
}

func TestReadDir_SkipsCorruptLinesAndMissingDir(t *testing.T) {
	// A missing directory is the normal state of a box that has executed
	// nothing yet, and must not read as an error.
	recs, err := execlog.ReadDir(t.TempDir() + "/nope")
	if err != nil {
		t.Fatalf("missing dir must not error: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("want no records, got %d", len(recs))
	}

	dir := t.TempDir()
	good := `{"ts":"2026-08-29T10:00:00Z","kind":"exec","pid":1,"uid":0,"comm":"ok","ret":0}`
	body := "not json at all\n" + good + "\n" + `{"ts":"2026-08-29T10:00:01Z"}` + "\n"
	if err := os.WriteFile(execlog.Path(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	recs, err = execlog.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	// The garbage line is skipped, and so is the record with no kind — a
	// record whose kind is unknown cannot be placed on either side of the join.
	if len(recs) != 1 || recs[0].Comm != "ok" {
		t.Fatalf("want only the well-formed record, got %+v", recs)
	}
}

func TestReadDir_SurfacesAPermissionErrorInsteadOfAnEmptyTrace(t *testing.T) {
	// The store is 0700 root-only. A caller that cannot read it must be told
	// so — reading nothing and reporting nothing look identical to a caller
	// that only checks len(recs)==0, and this store has exactly one producer,
	// so there is no "other producer" left to answer for the trace the way
	// flowlog's multi-writer directory has. This is the read-side twin of the
	// Phase 131 flow-store EACCES defect flowlog.Writer's warnOnce prevents.
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the permission bits this test relies on")
	}
	dir := t.TempDir()
	if err := os.WriteFile(execlog.Path(dir), []byte(`{"ts":"2026-08-29T10:00:00Z","kind":"exec","pid":1,"uid":0,"comm":"ok","ret":0}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // let t.TempDir() clean up

	_, err := execlog.ReadDir(dir)
	if err == nil {
		t.Fatal("want an error when the store cannot be opened, not a silent empty trace")
	}
	if !os.IsPermission(err) {
		t.Errorf("want a permission error, got %v", err)
	}
}
