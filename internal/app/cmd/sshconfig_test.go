// Package cmd — sshconfig_test.go
// Tests for UpsertHost / RemoveHost managed SSH config block.
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture writes content to a file named "config" inside dir and returns the path.
func writeFixture(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "config")
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatalf("writeFixture: %v", err)
	}
	return p
}

// TestSSHConfig_UpsertCreatesFileIfAbsent verifies that UpsertHost creates the
// config file (mode 0600) with markers + the Host entry when configPath is absent.
func TestSSHConfig_UpsertCreatesFileIfAbsent(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config")
	err := UpsertHost(cfgPath, "km-sb-abc", HostOptions{
		HostName: "localhost", Port: 2222, User: "sandbox",
		IdentityFile: "~/.km/keys/sb-abc",
	})
	if err != nil {
		t.Fatalf("UpsertHost: %v", err)
	}
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode()&0o777 != 0o600 {
		t.Errorf("mode = %o, want 0o600", info.Mode()&0o777)
	}
	content, _ := os.ReadFile(cfgPath)
	s := string(content)
	if !strings.Contains(s, beginMarker) {
		t.Error("missing begin marker")
	}
	if !strings.Contains(s, endMarker) {
		t.Error("missing end marker")
	}
	if !strings.Contains(s, "Host km-sb-abc") {
		t.Error("missing Host line")
	}
	if !strings.Contains(s, "Port 2222") {
		t.Error("missing Port line")
	}
	if !strings.Contains(s, "IdentitiesOnly yes") {
		t.Error("missing IdentitiesOnly default")
	}
}

// TestSSHConfig_UpsertAppendsMarkersIfAbsent verifies that UpsertHost appends
// managed markers at the end when configPath exists with content but no markers.
func TestSSHConfig_UpsertAppendsMarkersIfAbsent(t *testing.T) {
	dir := t.TempDir()
	existing := "Host github.com\n  IdentityFile ~/.ssh/github_rsa\n"
	cfgPath := writeFixture(t, dir, existing)

	opts := HostOptions{HostName: "localhost", Port: 2222, User: "sandbox", IdentityFile: "~/.km/keys/sb-abc123"}
	if err := UpsertHost(cfgPath, "km-sb-abc123", opts); err != nil {
		t.Fatalf("UpsertHost: %v", err)
	}
	raw, _ := os.ReadFile(cfgPath)
	content := string(raw)
	if !strings.HasPrefix(content, existing) {
		t.Errorf("pre-existing content altered; got: %q", content)
	}
	if !strings.Contains(content, beginMarker) {
		t.Error("missing begin marker")
	}
	if !strings.Contains(content, "Host km-sb-abc123") {
		t.Error("missing Host entry")
	}
}

// TestSSHConfig_UpsertReplacesExistingEntry verifies that UpsertHost replaces
// only the target entry when markers + Host entry already exist for alias.
func TestSSHConfig_UpsertReplacesExistingEntry(t *testing.T) {
	dir := t.TempDir()
	initial := beginMarker + "\nHost km-sb-abc123\n  HostName localhost\n  Port 2222\n  User sandbox\n  IdentityFile ~/.km/keys/sb-abc123\n  IdentitiesOnly yes\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  ServerAliveInterval 30\n" + endMarker + "\n"
	cfgPath := writeFixture(t, dir, initial)

	opts := HostOptions{HostName: "localhost", Port: 9000, User: "sandbox", IdentityFile: "~/.km/keys/sb-abc123"}
	if err := UpsertHost(cfgPath, "km-sb-abc123", opts); err != nil {
		t.Fatalf("UpsertHost: %v", err)
	}
	raw, _ := os.ReadFile(cfgPath)
	content := string(raw)
	if strings.Count(content, "Host km-sb-abc123") != 1 {
		t.Errorf("expected exactly 1 Host entry; got %d", strings.Count(content, "Host km-sb-abc123"))
	}
	if !strings.Contains(content, "Port 9000") {
		t.Error("expected updated port 9000")
	}
	if strings.Contains(content, "Port 2222") {
		t.Error("old port 2222 still present after replace")
	}
}

// TestSSHConfig_UpsertInsertsBeforeEnd verifies that UpsertHost inserts the new
// entry before END marker when markers exist with a different alias.
func TestSSHConfig_UpsertInsertsBeforeEnd(t *testing.T) {
	dir := t.TempDir()
	initial := beginMarker + "\nHost km-sb-other\n  HostName localhost\n  Port 2222\n  User sandbox\n  IdentityFile ~/.km/keys/sb-other\n  IdentitiesOnly yes\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  ServerAliveInterval 30\n" + endMarker + "\n"
	cfgPath := writeFixture(t, dir, initial)

	opts := HostOptions{HostName: "localhost", Port: 3333, User: "sandbox", IdentityFile: "~/.km/keys/sb-new"}
	if err := UpsertHost(cfgPath, "km-sb-new", opts); err != nil {
		t.Fatalf("UpsertHost: %v", err)
	}
	raw, _ := os.ReadFile(cfgPath)
	content := string(raw)
	if !strings.Contains(content, "Host km-sb-other") {
		t.Error("existing entry lost")
	}
	if !strings.Contains(content, "Host km-sb-new") {
		t.Error("new entry missing")
	}
	// New entry must appear before the end marker.
	idxNew := strings.Index(content, "Host km-sb-new")
	idxEnd := strings.Index(content, endMarker)
	if idxNew > idxEnd {
		t.Error("new entry appears after end marker")
	}
}

// TestSSHConfig_PreservesOutsideMarkers verifies that content before AND after
// the markers is preserved byte-for-byte after UpsertHost.
func TestSSHConfig_PreservesOutsideMarkers(t *testing.T) {
	dir := t.TempDir()
	before := "# personal hosts\nHost myserver\n  HostName 1.2.3.4\n\n"
	afterContent := "\n# end of file\n"
	initial := before + beginMarker + "\nHost km-sb-abc123\n  HostName localhost\n  Port 2222\n  User sandbox\n  IdentityFile ~/.km/keys/sb-abc123\n  IdentitiesOnly yes\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  ServerAliveInterval 30\n" + endMarker + "\n" + afterContent
	cfgPath := writeFixture(t, dir, initial)

	opts := HostOptions{HostName: "localhost", Port: 2222, User: "sandbox", IdentityFile: "~/.km/keys/sb-abc123"}
	if err := UpsertHost(cfgPath, "km-sb-abc123", opts); err != nil {
		t.Fatalf("UpsertHost: %v", err)
	}
	raw, _ := os.ReadFile(cfgPath)
	content := string(raw)
	if !strings.HasPrefix(content, before) {
		t.Errorf("content before markers changed; want prefix %q; got %q", before, content[:min(len(before)+20, len(content))])
	}
	if !strings.Contains(content, afterContent) {
		t.Errorf("content after markers changed; want content containing %q", afterContent)
	}
}

// min returns the smaller of a and b (Go 1.21 has builtin min, earlier versions need this).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestSSHConfig_RemovePreservesOthers verifies that RemoveHost removes only the
// target entry while preserving the other entry in the managed block.
func TestSSHConfig_RemovePreservesOthers(t *testing.T) {
	dir := t.TempDir()
	initial := beginMarker + "\nHost km-sb-one\n  HostName localhost\n  Port 2222\n  User sandbox\n  IdentityFile ~/.km/keys/sb-one\n  IdentitiesOnly yes\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  ServerAliveInterval 30\nHost km-sb-two\n  HostName localhost\n  Port 3333\n  User sandbox\n  IdentityFile ~/.km/keys/sb-two\n  IdentitiesOnly yes\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  ServerAliveInterval 30\n" + endMarker + "\n"
	cfgPath := writeFixture(t, dir, initial)

	if err := RemoveHost(cfgPath, "km-sb-one"); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	raw, _ := os.ReadFile(cfgPath)
	content := string(raw)
	if strings.Contains(content, "Host km-sb-one") {
		t.Error("removed entry still present")
	}
	if !strings.Contains(content, "Host km-sb-two") {
		t.Error("other entry was lost")
	}
}

// TestSSHConfig_RemoveCleansMarkersWhenLast verifies that RemoveHost drops the
// entry AND the managed markers when the last entry is removed.
func TestSSHConfig_RemoveCleansMarkersWhenLast(t *testing.T) {
	dir := t.TempDir()
	pre := "Host myserver\n  HostName 1.2.3.4\n"
	initial := pre + "\n" + beginMarker + "\nHost km-sb-abc123\n  HostName localhost\n  Port 2222\n  User sandbox\n  IdentityFile ~/.km/keys/sb-abc123\n  IdentitiesOnly yes\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  ServerAliveInterval 30\n" + endMarker + "\n"
	cfgPath := writeFixture(t, dir, initial)

	if err := RemoveHost(cfgPath, "km-sb-abc123"); err != nil {
		t.Fatalf("RemoveHost: %v", err)
	}
	raw, _ := os.ReadFile(cfgPath)
	content := string(raw)
	if strings.Contains(content, beginMarker) {
		t.Error("begin marker not cleaned up after last entry removed")
	}
	if strings.Contains(content, endMarker) {
		t.Error("end marker not cleaned up after last entry removed")
	}
	if !strings.Contains(content, "Host myserver") {
		t.Error("pre-existing content was lost")
	}
}

// TestSSHConfig_RemoveIdempotentMissing verifies that RemoveHost returns nil
// when the file exists but the alias is not present.
func TestSSHConfig_RemoveIdempotentMissing(t *testing.T) {
	dir := t.TempDir()
	initial := beginMarker + "\nHost km-sb-other\n  HostName localhost\n  Port 2222\n  User sandbox\n  IdentityFile ~/.km/keys/sb-other\n  IdentitiesOnly yes\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  ServerAliveInterval 30\n" + endMarker + "\n"
	cfgPath := writeFixture(t, dir, initial)

	if err := RemoveHost(cfgPath, "km-sb-nothere"); err != nil {
		t.Errorf("expected nil error for missing alias; got: %v", err)
	}
	// File must be unchanged.
	raw, _ := os.ReadFile(cfgPath)
	if string(raw) != initial {
		t.Error("file was modified even though alias was absent")
	}
}

// TestSSHConfig_RemoveIdempotentNoFile verifies that RemoveHost returns nil and
// does not create the file when configPath does not exist.
func TestSSHConfig_RemoveIdempotentNoFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "nonexistent_config")
	if err := RemoveHost(cfgPath, "km-sb-abc123"); err != nil {
		t.Errorf("expected nil error for non-existent file; got: %v", err)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Error("RemoveHost created a file when configPath was absent")
	}
}
