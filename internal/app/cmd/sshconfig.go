package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// hostLineRe matches "Host <name> [<name>...]" lines (the start of a Host
// block). ssh(5) allows several patterns on one Host line and matches the first
// block whose pattern list contains the requested name, so km writes both the
// operator's alias and the sandbox id: `Host km-<alias> km-<sandbox-id>`.
var hostLineRe = regexp.MustCompile(`^Host\s+(\S+(?:[ \t]+\S+)*)[ \t]*$`)

// hostBlock holds a parsed SSH Host block (its name list + raw content
// including the Host line).
//
// The LAST name is the sandbox id by construction — see renderHostBlock and
// readManagedAliases, which depend on that ordering.
type hostBlock struct {
	names   []string
	content []byte // the "Host <names...>\n  ..." lines including trailing newline
}

// sandboxIDName returns the block's last name, which km renders as the
// `km-<sandbox-id>` form. Empty for a block with no names.
func (b hostBlock) sandboxIDName() string {
	if len(b.names) == 0 {
		return ""
	}
	return b.names[len(b.names)-1]
}

// sharesName reports whether b and names have any name in common. Used to
// decide whether an upsert replaces an existing block: reusing an alias for a
// freshly created sandbox must overwrite the old entry rather than append a
// second block claiming the same alias, since ssh takes the first match.
func (b hostBlock) sharesName(names []string) bool {
	for _, n := range b.names {
		for _, m := range names {
			if n == m {
				return true
			}
		}
	}
	return false
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
			current = &hostBlock{names: strings.Fields(string(m[1]))}
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
//
// names is written in order, so callers MUST put the `km-<sandbox-id>` form
// last: readManagedAliases reads the last name as the sandbox id.
func renderHostBlock(names []string, opts HostOptions) []byte {
	port := opts.Port
	if port == 0 {
		port = 2222
	}
	s := fmt.Sprintf("Host %s\n  HostName %s\n  Port %d\n  User %s\n  IdentityFile %s\n  IdentitiesOnly yes\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  ServerAliveInterval 30\n",
		strings.Join(names, " "), opts.HostName, port, opts.User, opts.IdentityFile)
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

// UpsertHost inserts or replaces the Host entry for names inside the managed
// block of configPath. Creates configPath (mode 0600) and the managed block if
// absent. Content outside the markers is preserved byte-for-byte.
//
// An existing block is replaced when it shares ANY name with names, not only
// when the lists are equal — so recreating a sandbox under a reused alias
// overwrites that alias's old entry instead of leaving two blocks claiming it
// (ssh would silently take the first, pointing at the destroyed sandbox).
//
// The last entry in names must be the `km-<sandbox-id>` form; see
// renderHostBlock.
func UpsertHost(configPath string, names []string, opts HostOptions) error {
	if len(names) == 0 {
		return fmt.Errorf("sshconfig: UpsertHost requires at least one host name")
	}
	content, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("sshconfig: read %s: %w", configPath, err)
	}
	// err == nil → file exists; err != nil (IsNotExist) → treat as empty.

	before, inside, after := managedSections(content)

	newBlock := renderHostBlock(names, opts)

	var blocks []hostBlock
	if inside != nil {
		blocks = parseHostBlocks(inside)
	}

	// Replace existing block or append new one.
	found := false
	for i, b := range blocks {
		if b.sharesName(names) {
			blocks[i].names = names
			blocks[i].content = newBlock
			found = true
			break
		}
	}
	if !found {
		blocks = append(blocks, hostBlock{names: names, content: newBlock})
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

// RemoveHost drops the Host entry naming alias from the managed block of
// configPath. A block is removed when it CONTAINS alias among its names, not
// only when it is the sole name — callers pass the `km-<sandbox-id>` form and
// must still drop a block that also carries the operator's alias.
// Returns nil when alias or file is absent (idempotent). Cleans up
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
		if !b.sharesName([]string{alias}) {
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
