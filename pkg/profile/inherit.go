package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

const maxInheritanceDepth = 3

// Resolve loads a profile by name and resolves its extends chain.
// It searches built-in profiles first, then the provided searchPaths directories.
// Cycle detection and max depth (3) are enforced.
// NOTE: Resolve does NOT call Validate() internally. Callers are responsible
// for calling Resolve() and Validate() separately.
func Resolve(name string, searchPaths []string) (*SandboxProfile, error) {
	return resolve(name, searchPaths, make(map[string]bool), 0)
}

func resolve(name string, searchPaths []string, visited map[string]bool, depth int) (*SandboxProfile, error) {
	if depth > maxInheritanceDepth {
		return nil, fmt.Errorf("inheritance depth exceeded: max %d levels allowed", maxInheritanceDepth)
	}

	if visited[name] {
		return nil, fmt.Errorf("circular inheritance detected: profile %q already in chain", name)
	}
	visited[name] = true

	profile, err := load(name, searchPaths)
	if err != nil {
		return nil, err
	}

	if profile.Extends == "" {
		return profile, nil
	}

	parent, err := resolve(profile.Extends, searchPaths, visited, depth+1)
	if err != nil {
		return nil, fmt.Errorf("resolving parent %q of %q: %w", profile.Extends, name, err)
	}

	merged := merge(parent, profile)
	return merged, nil
}

func load(name string, searchPaths []string) (*SandboxProfile, error) {
	// Try built-in profiles first
	if IsBuiltin(name) {
		return LoadBuiltin(name)
	}

	// Search in provided paths
	for _, dir := range searchPaths {
		path := filepath.Join(dir, name+".yaml")
		data, err := os.ReadFile(path)
		if err == nil {
			p, parseErr := Parse(data)
			if parseErr != nil {
				return nil, fmt.Errorf("parsing profile %q from %s: %w", name, path, parseErr)
			}
			return p, nil
		}
	}

	return nil, fmt.Errorf("profile %q not found in built-in profiles or search paths %v", name, searchPaths)
}

// merge combines parent and child profiles. Child values override parent values.
// For allowlist arrays (AllowedDNSSuffixes, AllowedHosts, etc.), if child specifies
// them at all, child's array replaces parent's entirely.
// metadata.labels are the ONE exception: they are merged additively.
func merge(parent, child *SandboxProfile) *SandboxProfile {
	result := &SandboxProfile{
		APIVersion: child.APIVersion,
		Kind:       child.Kind,
		Metadata:   child.Metadata,
		Spec:       child.Spec,
	}

	// Merge metadata.labels additively (the one exception)
	if parent.Metadata.Labels != nil || child.Metadata.Labels != nil {
		merged := make(map[string]string)
		for k, v := range parent.Metadata.Labels {
			merged[k] = v
		}
		for k, v := range child.Metadata.Labels {
			merged[k] = v
		}
		result.Metadata.Labels = merged
	}

	// For each spec section: if child section is zero-value, use parent's
	mergeSpecSection(&result.Spec.Lifecycle, &parent.Spec.Lifecycle, &child.Spec.Lifecycle)
	mergeSpecSection(&result.Spec.Runtime, &parent.Spec.Runtime, &child.Spec.Runtime)
	mergeSpecSection(&result.Spec.Execution, &parent.Spec.Execution, &child.Spec.Execution)
	mergeSpecSection(&result.Spec.SourceAccess, &parent.Spec.SourceAccess, &child.Spec.SourceAccess)
	mergeSpecSection(&result.Spec.Network, &parent.Spec.Network, &child.Spec.Network)
	mergeSpecSection(&result.Spec.Identity, &parent.Spec.Identity, &child.Spec.Identity)
	mergeSpecSection(&result.Spec.Sidecars, &parent.Spec.Sidecars, &child.Spec.Sidecars)
	mergeSpecSection(&result.Spec.Observability, &parent.Spec.Observability, &child.Spec.Observability)
	mergeSpecSection(&result.Spec.Agent, &parent.Spec.Agent, &child.Spec.Agent)

	// Clear extends — resolved profile has no parent
	result.Extends = ""

	return result
}

// mergeSpecSection uses reflection to check if the child section is zero-value.
// If it is, use parent's value instead.
func mergeSpecSection(result, parent, child interface{}) {
	childVal := reflect.ValueOf(child).Elem()
	parentVal := reflect.ValueOf(parent).Elem()
	resultVal := reflect.ValueOf(result).Elem()

	if childVal.IsZero() {
		resultVal.Set(parentVal)
	} else {
		resultVal.Set(childVal)
	}
}
