package profile

import (
	"embed"
	"fmt"
	"path"
	"strings"
)

//go:embed builtins
var builtinFS embed.FS

var builtinNames = []string{"open-dev", "restricted-dev", "hardened", "sealed", "goose", "ao", "codex", "learn"}

// ListBuiltins returns the names of all built-in profiles.
func ListBuiltins() []string {
	return append([]string{}, builtinNames...)
}

// IsBuiltin checks if a name is a built-in profile.
func IsBuiltin(name string) bool {
	for _, n := range builtinNames {
		if n == name {
			return true
		}
	}
	return false
}

// LoadBuiltin loads and parses a built-in profile by name.
func LoadBuiltin(name string) (*SandboxProfile, error) {
	if !IsBuiltin(name) {
		return nil, fmt.Errorf("unknown built-in profile: %q (available: %s)", name, strings.Join(builtinNames, ", "))
	}

	data, err := builtinFS.ReadFile(path.Join("builtins", name+".yaml"))
	if err != nil {
		return nil, fmt.Errorf("reading built-in profile %q: %w", name, err)
	}

	p, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parsing built-in profile %q: %w", name, err)
	}

	return p, nil
}
