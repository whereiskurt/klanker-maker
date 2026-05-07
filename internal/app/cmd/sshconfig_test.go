// Package cmd — sshconfig_test.go
// Failing-stub tests for UpsertHost / RemoveHost managed SSH config block.
// Wave 1 Plan 73-03 removes the t.Skip and implements the functions.
package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSSHConfig_UpsertCreatesFileIfAbsent verifies that UpsertHost creates the
// config file (mode 0600) with markers + the Host entry when configPath is absent.
func TestSSHConfig_UpsertCreatesFileIfAbsent(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-03): implement managed-block parser/writer")
	// dir := t.TempDir()
	// cfgPath := filepath.Join(dir, "config")
	// opts := HostOptions{HostName: "localhost", Port: 2222, User: "sandbox", IdentityFile: "~/.km/keys/sb-abc123"}
	// if err := UpsertHost(cfgPath, "km-sb-abc123", opts); err != nil {
	// 	t.Fatalf("UpsertHost: %v", err)
	// }
	// info, err := os.Stat(cfgPath)
	// if err != nil {
	// 	t.Fatalf("stat: %v", err)
	// }
	// if got := info.Mode().Perm(); got != 0600 {
	// 	t.Fatalf("config file mode = %o; want 0600", got)
	// }
	// raw, _ := os.ReadFile(cfgPath)
	// content := string(raw)
	// if !strings.Contains(content, beginMarker) {
	// 	t.Error("file missing begin marker")
	// }
	// if !strings.Contains(content, endMarker) {
	// 	t.Error("file missing end marker")
	// }
	// if !strings.Contains(content, "Host km-sb-abc123") {
	// 	t.Error("file missing Host entry")
	// }
	_ = filepath.Join
	_ = os.Stat
}

// TestSSHConfig_UpsertAppendsMarkersIfAbsent verifies that UpsertHost appends
// managed markers at the end when configPath exists with content but no markers.
func TestSSHConfig_UpsertAppendsMarkersIfAbsent(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-03): implement managed-block parser/writer")
	// dir := t.TempDir()
	// cfgPath := filepath.Join(dir, "config")
	// existing := "Host github.com\n  IdentityFile ~/.ssh/github_rsa\n"
	// if err := os.WriteFile(cfgPath, []byte(existing), 0600); err != nil {
	// 	t.Fatalf("write: %v", err)
	// }
	// opts := HostOptions{HostName: "localhost", Port: 2222, User: "sandbox", IdentityFile: "~/.km/keys/sb-abc123"}
	// if err := UpsertHost(cfgPath, "km-sb-abc123", opts); err != nil {
	// 	t.Fatalf("UpsertHost: %v", err)
	// }
	// raw, _ := os.ReadFile(cfgPath)
	// content := string(raw)
	// if !strings.HasPrefix(content, existing) {
	// 	t.Errorf("pre-existing content altered; got: %q", content)
	// }
	// if !strings.Contains(content, beginMarker) {
	// 	t.Error("missing begin marker")
	// }
	// if !strings.Contains(content, "Host km-sb-abc123") {
	// 	t.Error("missing Host entry")
	// }
	_ = filepath.Join
	_ = os.Stat
}

// TestSSHConfig_UpsertReplacesExistingEntry verifies that UpsertHost replaces
// only the target entry when markers + Host entry already exist for alias.
func TestSSHConfig_UpsertReplacesExistingEntry(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-03): implement managed-block parser/writer")
	// dir := t.TempDir()
	// cfgPath := filepath.Join(dir, "config")
	// initial := beginMarker + "\nHost km-sb-abc123\n  HostName localhost\n  Port 2222\n" + endMarker + "\n"
	// if err := os.WriteFile(cfgPath, []byte(initial), 0600); err != nil {
	// 	t.Fatalf("write: %v", err)
	// }
	// opts := HostOptions{HostName: "localhost", Port: 4444, User: "sandbox", IdentityFile: "~/.km/keys/sb-abc123"}
	// if err := UpsertHost(cfgPath, "km-sb-abc123", opts); err != nil {
	// 	t.Fatalf("UpsertHost: %v", err)
	// }
	// raw, _ := os.ReadFile(cfgPath)
	// content := string(raw)
	// if strings.Count(content, "Host km-sb-abc123") != 1 {
	// 	t.Errorf("expected exactly 1 Host entry; got %d", strings.Count(content, "Host km-sb-abc123"))
	// }
	// if !strings.Contains(content, "Port 4444") {
	// 	t.Error("expected updated port 4444")
	// }
	_ = filepath.Join
	_ = os.Stat
}

// TestSSHConfig_UpsertInsertsBeforeEnd verifies that UpsertHost inserts the new
// entry before END marker when markers exist with a different alias.
func TestSSHConfig_UpsertInsertsBeforeEnd(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-03): implement managed-block parser/writer")
	// dir := t.TempDir()
	// cfgPath := filepath.Join(dir, "config")
	// initial := beginMarker + "\nHost km-sb-other\n  HostName localhost\n  Port 2222\n" + endMarker + "\n"
	// if err := os.WriteFile(cfgPath, []byte(initial), 0600); err != nil {
	// 	t.Fatalf("write: %v", err)
	// }
	// opts := HostOptions{HostName: "localhost", Port: 3333, User: "sandbox", IdentityFile: "~/.km/keys/sb-new"}
	// if err := UpsertHost(cfgPath, "km-sb-new", opts); err != nil {
	// 	t.Fatalf("UpsertHost: %v", err)
	// }
	// raw, _ := os.ReadFile(cfgPath)
	// content := string(raw)
	// if !strings.Contains(content, "Host km-sb-other") {
	// 	t.Error("existing entry lost")
	// }
	// if !strings.Contains(content, "Host km-sb-new") {
	// 	t.Error("new entry missing")
	// }
	// // New entry must appear before the end marker.
	// idxNew := strings.Index(content, "Host km-sb-new")
	// idxEnd := strings.Index(content, endMarker)
	// if idxNew > idxEnd {
	// 	t.Error("new entry appears after end marker")
	// }
	_ = filepath.Join
	_ = os.Stat
}

// TestSSHConfig_PreservesOutsideMarkers verifies that content before AND after
// the markers is preserved byte-for-byte after UpsertHost.
func TestSSHConfig_PreservesOutsideMarkers(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-03): implement managed-block parser/writer")
	// dir := t.TempDir()
	// cfgPath := filepath.Join(dir, "config")
	// before := "# personal hosts\nHost myserver\n  HostName 1.2.3.4\n\n"
	// after := "\n# end of file\n"
	// initial := before + beginMarker + "\nHost km-sb-abc123\n  HostName localhost\n" + endMarker + "\n" + after
	// if err := os.WriteFile(cfgPath, []byte(initial), 0600); err != nil {
	// 	t.Fatalf("write: %v", err)
	// }
	// opts := HostOptions{HostName: "localhost", Port: 2222, User: "sandbox", IdentityFile: "~/.km/keys/sb-abc123"}
	// if err := UpsertHost(cfgPath, "km-sb-abc123", opts); err != nil {
	// 	t.Fatalf("UpsertHost: %v", err)
	// }
	// raw, _ := os.ReadFile(cfgPath)
	// content := string(raw)
	// if !strings.HasPrefix(content, before) {
	// 	t.Errorf("content before markers changed; want prefix %q; got %q", before, content[:min(len(before)+20, len(content))])
	// }
	// if !strings.Contains(content, after) {
	// 	t.Errorf("content after markers changed; want suffix containing %q", after)
	// }
	_ = filepath.Join
	_ = os.Stat
}

// TestSSHConfig_RemovePreservesOthers verifies that RemoveHost removes only the
// target entry while preserving the other entry in the managed block.
func TestSSHConfig_RemovePreservesOthers(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-03): implement managed-block parser/writer")
	// dir := t.TempDir()
	// cfgPath := filepath.Join(dir, "config")
	// initial := beginMarker + "\nHost km-sb-one\n  HostName localhost\n  Port 2222\nHost km-sb-two\n  HostName localhost\n  Port 3333\n" + endMarker + "\n"
	// if err := os.WriteFile(cfgPath, []byte(initial), 0600); err != nil {
	// 	t.Fatalf("write: %v", err)
	// }
	// if err := RemoveHost(cfgPath, "km-sb-one"); err != nil {
	// 	t.Fatalf("RemoveHost: %v", err)
	// }
	// raw, _ := os.ReadFile(cfgPath)
	// content := string(raw)
	// if strings.Contains(content, "Host km-sb-one") {
	// 	t.Error("removed entry still present")
	// }
	// if !strings.Contains(content, "Host km-sb-two") {
	// 	t.Error("other entry was lost")
	// }
	_ = filepath.Join
	_ = os.Stat
}

// TestSSHConfig_RemoveCleansMarkersWhenLast verifies that RemoveHost drops the
// entry AND the managed markers when the last entry is removed.
func TestSSHConfig_RemoveCleansMarkersWhenLast(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-03): implement managed-block parser/writer")
	// dir := t.TempDir()
	// cfgPath := filepath.Join(dir, "config")
	// pre := "Host myserver\n  HostName 1.2.3.4\n"
	// initial := pre + "\n" + beginMarker + "\nHost km-sb-abc123\n  HostName localhost\n  Port 2222\n" + endMarker + "\n"
	// if err := os.WriteFile(cfgPath, []byte(initial), 0600); err != nil {
	// 	t.Fatalf("write: %v", err)
	// }
	// if err := RemoveHost(cfgPath, "km-sb-abc123"); err != nil {
	// 	t.Fatalf("RemoveHost: %v", err)
	// }
	// raw, _ := os.ReadFile(cfgPath)
	// content := string(raw)
	// if strings.Contains(content, beginMarker) {
	// 	t.Error("begin marker not cleaned up after last entry removed")
	// }
	// if strings.Contains(content, endMarker) {
	// 	t.Error("end marker not cleaned up after last entry removed")
	// }
	// if !strings.Contains(content, "Host myserver") {
	// 	t.Error("pre-existing content was lost")
	// }
	_ = filepath.Join
	_ = os.Stat
}

// TestSSHConfig_RemoveIdempotentMissing verifies that RemoveHost returns nil
// when the file exists but the alias is not present.
func TestSSHConfig_RemoveIdempotentMissing(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-03): implement managed-block parser/writer")
	// dir := t.TempDir()
	// cfgPath := filepath.Join(dir, "config")
	// initial := beginMarker + "\nHost km-sb-other\n  HostName localhost\n  Port 2222\n" + endMarker + "\n"
	// if err := os.WriteFile(cfgPath, []byte(initial), 0600); err != nil {
	// 	t.Fatalf("write: %v", err)
	// }
	// if err := RemoveHost(cfgPath, "km-sb-nothere"); err != nil {
	// 	t.Errorf("expected nil error for missing alias; got: %v", err)
	// }
	// // File must be unchanged.
	// raw, _ := os.ReadFile(cfgPath)
	// if string(raw) != initial {
	// 	t.Error("file was modified even though alias was absent")
	// }
	_ = filepath.Join
	_ = os.Stat
}

// TestSSHConfig_RemoveIdempotentNoFile verifies that RemoveHost returns nil and
// does not create the file when configPath does not exist.
func TestSSHConfig_RemoveIdempotentNoFile(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 73-03): implement managed-block parser/writer")
	// dir := t.TempDir()
	// cfgPath := filepath.Join(dir, "nonexistent_config")
	// if err := RemoveHost(cfgPath, "km-sb-abc123"); err != nil {
	// 	t.Errorf("expected nil error for non-existent file; got: %v", err)
	// }
	// if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
	// 	t.Error("RemoveHost created a file when configPath was absent")
	// }
	_ = filepath.Join
	_ = os.Stat
}
