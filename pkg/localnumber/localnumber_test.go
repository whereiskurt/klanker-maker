package localnumber_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/localnumber"
)

// loadFrom and saveTo are test helpers that use an explicit path instead of
// the real config dir so tests are fully isolated in t.TempDir().
func loadFrom(t *testing.T, path string) *localnumber.State {
	t.Helper()
	s, err := localnumber.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom(%q): %v", path, err)
	}
	return s
}

func saveTo(t *testing.T, s *localnumber.State, path string) {
	t.Helper()
	if err := localnumber.SaveTo(s, path); err != nil {
		t.Fatalf("SaveTo(%q): %v", path, err)
	}
}

// TestAssign verifies:
//   - fresh state assigns 1 to first sandbox
//   - second sandbox gets 2
//   - re-assigning the same ID returns the original number (idempotent)
func TestAssign(t *testing.T) {
	s := &localnumber.State{Next: 1, Map: map[string]int{}}

	n1 := localnumber.Assign(s, "sb-aaa")
	if n1 != 1 {
		t.Errorf("first assign: got %d, want 1", n1)
	}
	if s.Next != 2 {
		t.Errorf("Next after first assign: got %d, want 2", s.Next)
	}

	n2 := localnumber.Assign(s, "sb-bbb")
	if n2 != 2 {
		t.Errorf("second assign: got %d, want 2", n2)
	}
	if s.Next != 3 {
		t.Errorf("Next after second assign: got %d, want 3", s.Next)
	}

	// Idempotent — same ID returns original number
	n1again := localnumber.Assign(s, "sb-aaa")
	if n1again != 1 {
		t.Errorf("re-assign same ID: got %d, want 1", n1again)
	}
	if s.Next != 3 {
		t.Errorf("Next should not change on re-assign: got %d, want 3", s.Next)
	}
}

// TestAssignNilMap verifies Assign initialises a nil map without panicking.
func TestAssignNilMap(t *testing.T) {
	s := &localnumber.State{Next: 1, Map: nil}
	n := localnumber.Assign(s, "sb-nil")
	if n != 1 {
		t.Errorf("assign with nil map: got %d, want 1", n)
	}
	if s.Map == nil {
		t.Error("Assign should initialize nil map")
	}
}

// TestRemove verifies:
//   - entry is deleted from map
//   - removing the last entry resets Next to 1
//   - removing a non-existent ID is a no-op
func TestRemove(t *testing.T) {
	s := &localnumber.State{Next: 1, Map: map[string]int{}}
	localnumber.Assign(s, "sb-aaa")
	localnumber.Assign(s, "sb-bbb")

	localnumber.Remove(s, "sb-aaa")
	if _, ok := s.Map["sb-aaa"]; ok {
		t.Error("Remove: entry should be gone")
	}
	// Next should still be 3 — map not empty
	if s.Next != 3 {
		t.Errorf("Next after partial remove: got %d, want 3", s.Next)
	}

	// Remove last entry — Next must reset to 1
	localnumber.Remove(s, "sb-bbb")
	if len(s.Map) != 0 {
		t.Errorf("map should be empty after removing all entries, got %v", s.Map)
	}
	if s.Next != 1 {
		t.Errorf("Next after removing last entry: got %d, want 1", s.Next)
	}

	// No-op remove of absent key
	localnumber.Remove(s, "sb-ghost")
	if s.Next != 1 {
		t.Errorf("Next after no-op remove: got %d, want 1", s.Next)
	}
}

// TestResolve verifies:
//   - returns the correct sandbox ID for a known number
//   - returns "", false for an unknown number
func TestResolve(t *testing.T) {
	s := &localnumber.State{Next: 1, Map: map[string]int{}}
	localnumber.Assign(s, "sb-aaa")
	localnumber.Assign(s, "sb-bbb")

	id, ok := localnumber.Resolve(s, 1)
	if !ok || id != "sb-aaa" {
		t.Errorf("Resolve(1): got (%q, %v), want (\"sb-aaa\", true)", id, ok)
	}

	id2, ok2 := localnumber.Resolve(s, 2)
	if !ok2 || id2 != "sb-bbb" {
		t.Errorf("Resolve(2): got (%q, %v), want (\"sb-bbb\", true)", id2, ok2)
	}

	_, ok3 := localnumber.Resolve(s, 99)
	if ok3 {
		t.Error("Resolve(99): expected false for nonexistent number")
	}
}

// TestReconcile verifies:
//   - prunes stale entries not in the live set
//   - assigns numbers to new live sandboxes
//   - resets Next when all sandboxes are pruned and no live IDs
func TestReconcile(t *testing.T) {
	s := &localnumber.State{Next: 1, Map: map[string]int{}}
	localnumber.Assign(s, "sb-old1")
	localnumber.Assign(s, "sb-old2")
	localnumber.Assign(s, "sb-keep")
	// s.Next is now 4

	live := []string{"sb-keep", "sb-new1", "sb-new2"}
	localnumber.Reconcile(s, live)

	// sb-old1 and sb-old2 should be pruned
	if _, ok := s.Map["sb-old1"]; ok {
		t.Error("Reconcile: sb-old1 should be pruned")
	}
	if _, ok := s.Map["sb-old2"]; ok {
		t.Error("Reconcile: sb-old2 should be pruned")
	}
	// sb-keep should retain its original number (3)
	if n := s.Map["sb-keep"]; n != 3 {
		t.Errorf("Reconcile: sb-keep number: got %d, want 3", n)
	}
	// sb-new1 and sb-new2 should be assigned
	if _, ok := s.Map["sb-new1"]; !ok {
		t.Error("Reconcile: sb-new1 should be assigned")
	}
	if _, ok := s.Map["sb-new2"]; !ok {
		t.Error("Reconcile: sb-new2 should be assigned")
	}

	// Full prune + empty live → reset
	s2 := &localnumber.State{Next: 1, Map: map[string]int{}}
	localnumber.Assign(s2, "sb-x")
	localnumber.Assign(s2, "sb-y")
	localnumber.Reconcile(s2, []string{})
	if len(s2.Map) != 0 {
		t.Errorf("Reconcile empty live: map should be empty, got %v", s2.Map)
	}
	if s2.Next != 1 {
		t.Errorf("Reconcile empty live: Next should reset to 1, got %d", s2.Next)
	}
}

// TestLoad verifies:
//   - missing file returns empty state (Next=1, empty map)
//   - corrupt JSON returns empty state (not an error)
//   - valid JSON is parsed correctly
//   - nil map in JSON is initialized to empty map
//   - Next < 1 in JSON is corrected to 1
func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local-numbers.json")

	// Missing file
	s, err := localnumber.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom missing file: unexpected error: %v", err)
	}
	if s.Next != 1 {
		t.Errorf("missing file: Next=%d, want 1", s.Next)
	}
	if s.Map == nil {
		t.Error("missing file: Map should be non-nil")
	}

	// Corrupt JSON
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s2, err := localnumber.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom corrupt JSON: unexpected error: %v", err)
	}
	if s2.Next != 1 {
		t.Errorf("corrupt JSON: Next=%d, want 1", s2.Next)
	}
	if s2.Map == nil {
		t.Error("corrupt JSON: Map should be non-nil")
	}

	// Valid JSON
	data, _ := json.Marshal(map[string]interface{}{
		"next": 5,
		"map":  map[string]int{"sb-abc": 3, "sb-def": 4},
	})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s3, err := localnumber.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom valid JSON: %v", err)
	}
	if s3.Next != 5 {
		t.Errorf("valid JSON: Next=%d, want 5", s3.Next)
	}
	if s3.Map["sb-abc"] != 3 {
		t.Errorf("valid JSON: sb-abc=%d, want 3", s3.Map["sb-abc"])
	}

	// Nil map in JSON → initialized
	data2, _ := json.Marshal(map[string]interface{}{
		"next": 2,
		"map":  nil,
	})
	if err := os.WriteFile(path, data2, 0o600); err != nil {
		t.Fatal(err)
	}
	s4, err := localnumber.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if s4.Map == nil {
		t.Error("nil map in JSON: should be initialized to empty map")
	}

	// Next < 1 corrected
	data3, _ := json.Marshal(map[string]interface{}{
		"next": 0,
		"map":  map[string]int{},
	})
	if err := os.WriteFile(path, data3, 0o600); err != nil {
		t.Fatal(err)
	}
	s5, err := localnumber.LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if s5.Next != 1 {
		t.Errorf("Next<1 in JSON: got %d, want 1", s5.Next)
	}
}

// TestSave verifies:
//   - creates parent directory if missing
//   - file exists after save
//   - content matches expected JSON (Next and map keys present)
//   - uses atomic write (no tmp file left behind)
func TestSave(t *testing.T) {
	dir := t.TempDir()
	// Use a nested path that doesn't exist yet to test MkdirAll
	path := filepath.Join(dir, "km", "local-numbers.json")

	s := &localnumber.State{Next: 3, Map: map[string]int{"sb-aaa": 1, "sb-bbb": 2}}
	if err := localnumber.SaveTo(s, path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	// File must exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("SaveTo: file does not exist after save")
	}

	// No tmp file left behind
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("SaveTo: .tmp file should not exist after save")
	}

	// Content round-trips correctly
	loaded := loadFrom(t, path)
	if loaded.Next != 3 {
		t.Errorf("round-trip Next: got %d, want 3", loaded.Next)
	}
	if loaded.Map["sb-aaa"] != 1 {
		t.Errorf("round-trip sb-aaa: got %d, want 1", loaded.Map["sb-aaa"])
	}
	if loaded.Map["sb-bbb"] != 2 {
		t.Errorf("round-trip sb-bbb: got %d, want 2", loaded.Map["sb-bbb"])
	}
}
