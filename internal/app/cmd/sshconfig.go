package cmd

import "errors"

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

// UpsertHost inserts or replaces the Host entry for alias inside the managed
// block of configPath. Creates configPath (mode 0600) and the managed block if
// absent. Phase 73 Wave 1 Plan 73-03 implements; this is a Wave 0 stub.
func UpsertHost(configPath, alias string, opts HostOptions) error {
	return errors.New("sshconfig: not implemented (Wave 1 Plan 73-03)")
}

// RemoveHost drops the Host entry for alias from the managed block of
// configPath. Returns nil when alias or file is absent (idempotent). Cleans up
// the managed-block markers when the block is empty after removal. Phase 73
// Wave 1 Plan 73-03 implements; this is a Wave 0 stub.
func RemoveHost(configPath, alias string) error {
	return errors.New("sshconfig: not implemented (Wave 1 Plan 73-03)")
}
