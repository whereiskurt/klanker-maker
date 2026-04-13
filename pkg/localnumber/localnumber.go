// Package localnumber manages persistent local sandbox numbering.
//
// Numbers are stored in a JSON file at ~/.config/km/local-numbers.json.
// They are monotonic, assigned at create time, and never reused while any
// sandbox exists. The counter resets to 1 only when the map is empty.
package localnumber

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// State is the on-disk representation of the local sandbox number registry.
type State struct {
	Next int            `json:"next"`
	Map  map[string]int `json:"map"`
}

// StateFilePath returns the path to the local numbers JSON file.
// It uses os.UserConfigDir() for XDG compliance, falling back to
// $HOME/.config if UserConfigDir is unavailable.
func StateFilePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", herr
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "km", "local-numbers.json"), nil
}

// LoadFrom loads state from an explicit file path. It is the testable inner
// implementation used by both Load and tests.
//
// Missing file or corrupt JSON both return a fresh empty state (no error).
func LoadFrom(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return freshState(), nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		// Corrupt file — return a fresh state without propagating the error
		return freshState(), nil
	}
	if s.Map == nil {
		s.Map = map[string]int{}
	}
	if s.Next < 1 {
		s.Next = 1
	}
	return &s, nil
}

// Load loads state from the default config path (StateFilePath).
// Missing file or corrupt JSON return a fresh empty state.
func Load() (*State, error) {
	path, err := StateFilePath()
	if err != nil {
		return freshState(), nil
	}
	return LoadFrom(path)
}

// SaveTo writes state to an explicit file path. It creates the parent
// directory if it does not exist and uses an atomic tmp+rename pattern.
// File permissions are 0o600; directory permissions are 0o700.
func SaveTo(s *State, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Save writes state to the default config path (StateFilePath).
func Save(s *State) error {
	path, err := StateFilePath()
	if err != nil {
		return err
	}
	return SaveTo(s, path)
}

// Assign assigns the next available number to sandboxID and returns it.
// If sandboxID already has a number the existing number is returned (idempotent).
// A nil Map is initialised automatically.
func Assign(s *State, sandboxID string) int {
	if s.Map == nil {
		s.Map = map[string]int{}
	}
	if existing, ok := s.Map[sandboxID]; ok {
		return existing
	}
	num := s.Next
	s.Map[sandboxID] = num
	s.Next++
	return num
}

// Remove deletes sandboxID from the map. If the map becomes empty after
// removal, Next is reset to 1 so numbering restarts cleanly.
// No-op if sandboxID is not in the map.
func Remove(s *State, sandboxID string) {
	delete(s.Map, sandboxID)
	if len(s.Map) == 0 {
		s.Next = 1
	}
}

// Resolve returns the sandbox ID that holds local number num.
// Returns ("", false) if no sandbox has that number.
func Resolve(s *State, num int) (string, bool) {
	for id, n := range s.Map {
		if n == num {
			return id, true
		}
	}
	return "", false
}

// Reconcile synchronises state against the current live sandbox IDs from
// DynamoDB. It prunes map entries whose IDs are absent from liveSandboxIDs,
// then assigns numbers to live IDs that have no existing entry. If the map
// is empty after reconciliation, Next is reset to 1.
func Reconcile(s *State, liveSandboxIDs []string) {
	if s.Map == nil {
		s.Map = map[string]int{}
	}

	live := make(map[string]struct{}, len(liveSandboxIDs))
	for _, id := range liveSandboxIDs {
		live[id] = struct{}{}
	}

	// Prune stale entries
	for id := range s.Map {
		if _, ok := live[id]; !ok {
			delete(s.Map, id)
		}
	}

	// Assign numbers to unknown live sandboxes
	for _, id := range liveSandboxIDs {
		if _, ok := s.Map[id]; !ok {
			Assign(s, id)
		}
	}

	// Reset counter if map is now empty
	if len(s.Map) == 0 {
		s.Next = 1
	}
}

// freshState returns a new empty State with Next set to 1.
func freshState() *State {
	return &State{Next: 1, Map: map[string]int{}}
}
