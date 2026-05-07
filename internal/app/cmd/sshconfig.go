package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const (
	beginMarker = "# BEGIN km vscode hosts (managed; do not edit between markers)"
	endMarker   = "# END km vscode hosts"
)

// HostOptions controls the SSH Host entry rendered into the managed block.
type HostOptions struct {
	HostName     string // "localhost"
	Port         int    // default 2222
	User         string // "sandbox"
	IdentityFile string // "~/.km/keys/sb-abc123"
}

// hostLineRe matches "Host <alias>" lines (the start of a Host block).
var hostLineRe = regexp.MustCompile(`^Host\s+(\S+)\s*$`)

// hostBlock holds a parsed SSH Host block (alias + raw content including the Host line).
type hostBlock struct {
	alias   string
	content []byte // the "Host <alias>\n  ..." lines including trailing newline
}

// managedSections splits content into (before, inside, after) regions around
// the managed-block markers. If markers are absent, before = full content and
// inside/after are nil.
func managedSections(content []byte) (before, inside, after []byte) {
	lines := bytes.SplitAfter(content, []byte("\n"))

	beginIdx := -1
	endIdx := -1
	for i, line := range lines {
		trimmed := bytes.TrimRight(line, "\r\n")
		if string(trimmed) == beginMarker {
			beginIdx = i
		} else if beginIdx >= 0 && string(trimmed) == endMarker {
			endIdx = i
			break
		}
	}

	if beginIdx < 0 || endIdx < 0 {
		// No markers found — entire content is "before".
		return content, nil, nil
	}

	before = joinLines(lines[:beginIdx])
	inside = joinLines(lines[beginIdx+1 : endIdx])
	after = joinLines(lines[endIdx+1:])
	return before, inside, after
}

// joinLines concatenates a slice of lines (each already has its newline).
func joinLines(lines [][]byte) []byte {
	return bytes.Join(lines, nil)
}

// parseHostBlocks splits the inside-markers region into individual Host blocks.
// A block starts at a "Host <alias>" line and ends at the next Host line or EOF.
func parseHostBlocks(inside []byte) []hostBlock {
	if len(inside) == 0 {
		return nil
	}
	lines := bytes.SplitAfter(inside, []byte("\n"))

	var blocks []hostBlock
	var current *hostBlock

	for _, line := range lines {
		trimmed := bytes.TrimRight(line, "\r\n")
		if m := hostLineRe.FindSubmatch(trimmed); m != nil {
			if current != nil {
				blocks = append(blocks, *current)
			}
			alias := string(m[1])
			current = &hostBlock{alias: alias}
			current.content = append(current.content, line...)
		} else if current != nil {
			current.content = append(current.content, line...)
		}
		// Lines before first Host block are ignored (should not occur in well-formed inside).
	}
	if current != nil {
		blocks = append(blocks, *current)
	}
	return blocks
}

// renderHostBlock produces the canonical text for a Host block.
func renderHostBlock(alias string, opts HostOptions) []byte {
	port := opts.Port
	if port == 0 {
		port = 2222
	}
	s := fmt.Sprintf("Host %s\n  HostName %s\n  Port %d\n  User %s\n  IdentityFile %s\n  IdentitiesOnly yes\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  ServerAliveInterval 30\n",
		alias, opts.HostName, port, opts.User, opts.IdentityFile)
	return []byte(s)
}

// atomicWrite writes content to path using a temp file + rename (atomic on
// macOS/Linux). The temp file is created in the same directory so that the
// rename is on the same filesystem. Parent directories are created as needed.
func atomicWrite(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("sshconfig: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".km-ssh-config-*")
	if err != nil {
		return fmt.Errorf("sshconfig: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op on success (after Rename)

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("sshconfig: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("sshconfig: close temp: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("sshconfig: chmod temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("sshconfig: rename temp -> %s: %w", path, err)
	}
	return nil
}

// ensureTrailingNewline appends '\n' to buf if it is non-empty and does not
// already end with a newline.
func ensureTrailingNewline(buf *bytes.Buffer) {
	if buf.Len() == 0 {
		return
	}
	if buf.Bytes()[buf.Len()-1] != '\n' {
		buf.WriteByte('\n')
	}
}

// UpsertHost inserts or replaces the Host entry for alias inside the managed
// block of configPath. Creates configPath (mode 0600) and the managed block if
// absent. Content outside the markers is preserved byte-for-byte.
func UpsertHost(configPath, alias string, opts HostOptions) error {
	content, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("sshconfig: read %s: %w", configPath, err)
	}
	// err == nil → file exists; err != nil (IsNotExist) → treat as empty.

	before, inside, after := managedSections(content)

	newBlock := renderHostBlock(alias, opts)

	var blocks []hostBlock
	if inside != nil {
		blocks = parseHostBlocks(inside)
	}

	// Replace existing block or append new one.
	found := false
	for i, b := range blocks {
		if b.alias == alias {
			blocks[i].content = newBlock
			found = true
			break
		}
	}
	if !found {
		blocks = append(blocks, hostBlock{alias: alias, content: newBlock})
	}

	// Reassemble.
	var out bytes.Buffer
	out.Write(before)
	// Ensure a newline separator between before-content and the begin marker,
	// but only when before is non-empty.
	ensureTrailingNewline(&out)
	out.WriteString(beginMarker + "\n")
	for _, b := range blocks {
		out.Write(b.content)
	}
	out.WriteString(endMarker + "\n")
	out.Write(after)

	assembled := out.Bytes()

	if os.IsNotExist(err) {
		// File did not exist — create via MkdirAll + WriteFile (simpler than atomic
		// rename when there is nothing to atomically replace).
		if mkErr := os.MkdirAll(filepath.Dir(configPath), 0o700); mkErr != nil {
			return fmt.Errorf("sshconfig: mkdir %s: %w", filepath.Dir(configPath), mkErr)
		}
		return os.WriteFile(configPath, assembled, 0o600)
	}

	return atomicWrite(configPath, assembled, 0o600)
}

// RemoveHost drops the Host entry for alias from the managed block of
// configPath. Returns nil when alias or file is absent (idempotent). Cleans up
// the managed-block markers when the block becomes empty after removal.
// When the entire file consisted solely of our managed block, the result is an
// empty file (mode 0600).
func RemoveHost(configPath, alias string) error {
	content, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("sshconfig: read %s: %w", configPath, err)
	}

	before, inside, after := managedSections(content)
	if inside == nil {
		// No markers — nothing to remove.
		return nil
	}

	blocks := parseHostBlocks(inside)
	kept := blocks[:0]
	for _, b := range blocks {
		if b.alias != alias {
			kept = append(kept, b)
		}
	}
	if len(kept) == len(blocks) {
		// Alias not present — idempotent no-op.
		return nil
	}

	var out bytes.Buffer
	out.Write(before)

	if len(kept) == 0 {
		// Drop markers entirely; just join before + after.
		// Insert newline separator only when both sides are non-empty.
		if len(before) > 0 && len(after) > 0 {
			ensureTrailingNewline(&out)
		}
		out.Write(after)
	} else {
		ensureTrailingNewline(&out)
		out.WriteString(beginMarker + "\n")
		for _, b := range kept {
			out.Write(b.content)
		}
		out.WriteString(endMarker + "\n")
		out.Write(after)
	}

	return atomicWrite(configPath, out.Bytes(), 0o600)
}
