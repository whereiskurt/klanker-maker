package flowlog_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

func TestReadDir_MergesProducersAndGenerations(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("flows.dns.jsonl", `{"ts":"2026-08-28T14:00:00Z","src":"dns","verdict":"allow","host":"a.example"}`+"\n")
	write("flows.dns.jsonl.1", `{"ts":"2026-08-28T13:00:00Z","src":"dns","verdict":"allow","host":"old.example"}`+"\n")
	write("flows.http.jsonl", `{"ts":"2026-08-28T14:01:00Z","src":"http","verdict":"deny","host":"b.example","port":443}`+"\n")

	recs, err := flowlog.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("want 3 records, got %d", len(recs))
	}
	// Sorted oldest-first so `flows --since` and display order are stable.
	if recs[0].Host != "old.example" {
		t.Errorf("want oldest first, got %q", recs[0].Host)
	}
}

func TestReadDir_SkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	body := `{"ts":"2026-08-28T14:00:00Z","src":"dns","verdict":"allow","host":"good.example"}` + "\n" +
		"this is not json\n" +
		`{"ts":"2026-08-28T14:00:01Z","src":"dns","verdict":"allow","host":"also-good.example"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "flows.dns.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	recs, err := flowlog.ReadDir(dir)
	if err != nil {
		t.Fatalf("a corrupt line must not fail the read: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 good records, got %d", len(recs))
	}
}

func TestReadDir_MissingDirIsEmptyNotError(t *testing.T) {
	recs, err := flowlog.ReadDir(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("absent dir is the pre-first-flow state, not an error: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("want 0 records, got %d", len(recs))
	}
}
