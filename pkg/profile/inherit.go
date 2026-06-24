package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	goyaml "github.com/goccy/go-yaml"
)

// maxInheritanceDepth is the maximum number of extends hops allowed in a chain.
// Raised from 3 → 10 in Phase 117 Plan 02 (DAG multi-parent support).
const maxInheritanceDepth = 10

// ─── Generic map-level merge engine (Plan 02, Task 1) ────────────────────────
//
// deepMerge produces a new map that is the recursive union of dst and src.
// Rules (applied per key):
//   - If only in dst  → keep dst value.
//   - If only in src  → add src value.
//   - Both are map[string]any → recurse.
//   - Both are slices ([]any) → concatDedup(dst, src).
//   - Otherwise (scalar collision) → src wins.
//
// deepMerge never mutates dst or src.
func deepMerge(dst, src map[string]any) map[string]any {
	out := make(map[string]any, len(dst)+len(src))
	// Copy dst first.
	for k, v := range dst {
		out[k] = v
	}
	for k, sv := range src {
		dv, exists := out[k]
		if !exists {
			out[k] = sv
			continue
		}
		dm, dIsMap := dv.(map[string]any)
		sm, sIsMap := sv.(map[string]any)
		if dIsMap && sIsMap {
			out[k] = deepMerge(dm, sm)
			continue
		}
		da, dIsSlice := toSlice(dv)
		sa, sIsSlice := toSlice(sv)
		if dIsSlice && sIsSlice {
			out[k] = concatDedup(da, sa)
			continue
		}
		// Scalar: src wins.
		out[k] = sv
	}
	return out
}

// toSlice coerces v to []any if it is a []any or []interface{} (goccy/go-yaml
// always decodes YAML sequences into []interface{}, which is identical to []any
// in Go 1.18+).
func toSlice(v any) ([]any, bool) {
	s, ok := v.([]any)
	return s, ok
}

// concatDedup concatenates b after a, dropping any element in b that is
// already present in the result (order-preserving, first-occurrence kept).
// Scalars are compared by reflect.DeepEqual; maps/objects are also compared
// by reflect.DeepEqual (handles additionalSnapshots object-list de-dup).
func concatDedup(a, b []any) []any {
	out := make([]any, 0, len(a)+len(b))
	contains := func(slice []any, v any) bool {
		for _, x := range slice {
			if reflect.DeepEqual(x, v) {
				return true
			}
		}
		return false
	}
	for _, v := range a {
		out = append(out, v)
	}
	for _, v := range b {
		if !contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// ─── DAG resolver (Plan 02, Task 2) ──────────────────────────────────────────

// Resolve loads a profile by name and resolves its extends chain.
// It searches built-in profiles first, then the provided searchPaths directories.
// Cycle detection and max depth (10) are enforced.
// NOTE: Resolve does NOT call Validate() internally. Callers are responsible
// for calling Resolve() and Validate() separately.
func Resolve(name string, searchPaths []string) (*SandboxProfile, error) {
	memo := make(map[string]map[string]any)
	acc, _, err := resolveMap(name, "", searchPaths, make(map[string]bool), 0, memo)
	if err != nil {
		return nil, err
	}
	return fromMap(acc)
}

// resolveMap is the recursive DAG resolver. It returns the merged map[string]any
// for the named profile plus the resolved base directory of that profile (for
// fragment-relative searchPath prepending).
//
// ancestry is COPIED per branch (Pitfall 1: a single shared visited map
// false-flags diamond bases as cycles). memo (keyed by resolved abs path /
// builtin name) caches the resolved map so a shared base is resolved once.
func resolveMap(
	name, baseDir string,
	searchPaths []string,
	ancestry map[string]bool,
	depth int,
	memo map[string]map[string]any,
) (map[string]any, string, error) {
	if depth > maxInheritanceDepth {
		return nil, "", fmt.Errorf("inheritance depth exceeded: max %d levels allowed", maxInheritanceDepth)
	}

	// Build an effective search path: fragment's own directory (if any) first.
	effectiveSearch := searchPaths
	if baseDir != "" {
		effectiveSearch = append([]string{baseDir}, searchPaths...)
	}

	// Load raw bytes and the resolved directory for the profile.
	rawBytes, resolvedDir, memoKey, err := loadRaw(name, effectiveSearch)
	if err != nil {
		return nil, "", err
	}

	// Check ancestry AFTER loading (so we have the canonical memoKey for cycle check).
	if ancestry[memoKey] {
		return nil, "", fmt.Errorf("circular inheritance detected: profile %q already in chain", name)
	}

	// Memoization: if we've already resolved this profile in this call tree,
	// return the cached result without re-resolving its parents (diamond dedup).
	if cached, ok := memo[memoKey]; ok {
		return cached, resolvedDir, nil
	}

	// Decode to generic map for deepMerge processing.
	var rawMap map[string]any
	if err := goyaml.Unmarshal(rawBytes, &rawMap); err != nil {
		return nil, "", fmt.Errorf("parsing profile %q: %w", name, err)
	}
	if rawMap == nil {
		rawMap = make(map[string]any)
	}

	// Extract extends list from the raw map.
	parents, err := extractExtends(rawMap)
	if err != nil {
		return nil, "", fmt.Errorf("profile %q extends field: %w", name, err)
	}

	if len(parents) == 0 {
		// No parents: this IS the base. Store in memo and return.
		memo[memoKey] = rawMap
		return rawMap, resolvedDir, nil
	}

	// Build per-branch ancestry copy (Pitfall 1).
	branchAncestry := make(map[string]bool, len(ancestry)+1)
	for k, v := range ancestry {
		branchAncestry[k] = v
	}
	branchAncestry[memoKey] = true

	// Fold bases left→right.
	acc := make(map[string]any)
	for _, parent := range parents {
		parentMap, parentDir, err := resolveMap(parent, resolvedDir, effectiveSearch, branchAncestry, depth+1, memo)
		if err != nil {
			return nil, "", fmt.Errorf("resolving parent %q of %q: %w", parent, name, err)
		}
		_ = parentDir // parentDir is used by child's own resolveMap call above
		acc = deepMerge(acc, parentMap)
	}

	// Merge the child's own map LAST (child wins).
	acc = deepMerge(acc, rawMap)

	// Clear extends from the merged map (it's a merge-time concept).
	delete(acc, "extends")

	// Clear metadata.abstract from the merged result. Abstract is a property of
	// individual base fragments, not of the concrete resolved leaf. When a base
	// fragment declares abstract:true and the leaf does not override it, the
	// deepMerge would propagate it — but the resolved result is always concrete.
	clearAbstractFromMetadata(acc)

	// Post-merge: move execution.initCommandsAppend onto execution.initCommands.
	applyInitCommandsAppend(acc)

	memo[memoKey] = acc
	return acc, resolvedDir, nil
}

// extractExtends reads the "extends" key from a raw profile map and returns
// the list of parent names (scalar string or []string sequence).
func extractExtends(rawMap map[string]any) ([]string, error) {
	v, ok := rawMap["extends"]
	if !ok || v == nil {
		return nil, nil
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil, nil
		}
		return []string{t}, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, elem := range t {
			s, ok := elem.(string)
			if !ok {
				return nil, fmt.Errorf("extends element is not a string: %T", elem)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unexpected extends type: %T", v)
	}
}

// applyInitCommandsAppend moves execution.initCommandsAppend onto the tail of
// execution.initCommands (concat+dedup), then removes the append key.
// Both keys are expected to hold []any when present.
func applyInitCommandsAppend(acc map[string]any) {
	execRaw, ok := acc["execution"]
	if !ok {
		return
	}
	exec, ok := execRaw.(map[string]any)
	if !ok {
		return
	}
	appendRaw, hasAppend := exec["initCommandsAppend"]
	if !hasAppend {
		return
	}
	appended, ok := toSlice(appendRaw)
	if !ok {
		return
	}
	existing, _ := toSlice(exec["initCommands"])
	exec["initCommands"] = concatDedup(existing, appended)
	delete(exec, "initCommandsAppend")
	acc["execution"] = exec
}

// clearAbstractFromMetadata removes the "abstract" flag from the top-level
// "metadata" map of the merged profile. A resolved leaf profile is always
// concrete — the "abstract" property of individual base fragments must not
// propagate to the merged result.
func clearAbstractFromMetadata(acc map[string]any) {
	meta, ok := acc["metadata"].(map[string]any)
	if !ok {
		return
	}
	delete(meta, "abstract")
	acc["metadata"] = meta
}

// fromMap marshals a map[string]any back to YAML and parses it into a
// *SandboxProfile. Extends is set to nil (already deleted from the map).
func fromMap(m map[string]any) (*SandboxProfile, error) {
	raw, err := goyaml.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("re-marshaling merged profile: %w", err)
	}
	var p SandboxProfile
	if err := goyaml.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("unmarshaling merged profile: %w", err)
	}
	p.Extends = nil
	return &p, nil
}

// loadRaw loads the raw YAML bytes for a named profile, returning the bytes,
// the resolved directory (for fragment-relative parent resolution), and a
// canonical memoization key (abs path or builtin name).
func loadRaw(name string, searchPaths []string) ([]byte, string, string, error) {
	// Try built-in profiles first.
	if IsBuiltin(name) {
		p, err := LoadBuiltin(name)
		if err != nil {
			return nil, "", "", err
		}
		raw, err := goyaml.Marshal(p)
		if err != nil {
			return nil, "", "", fmt.Errorf("marshaling builtin %q: %w", name, err)
		}
		// Built-ins don't have a directory; use the builtin name as memo key.
		return raw, "", "builtin:" + name, nil
	}

	// Search in provided paths.
	for _, dir := range searchPaths {
		path := filepath.Join(dir, name+".yaml")
		data, err := os.ReadFile(path)
		if err == nil {
			abs, _ := filepath.Abs(path)
			resolvedDir := filepath.Dir(abs)
			return data, resolvedDir, abs, nil
		}
	}

	return nil, "", "", fmt.Errorf("profile %q not found in built-in profiles or search paths %v", name, searchPaths)
}

// ─── Legacy typed-merge shims (kept for test backward-compatibility) ──────────
//
// The hand-written field-by-field zoo is deleted. These thin wrappers keep the
// internal function names alive so inherit_notification_test.go and
// inherit_agent_test.go compile without modification. Each wrapper routes
// through the generic deepMerge engine via a YAML round-trip.
//
// Note on non-pointer bool zero-value (Pitfall 2): base FRAGMENTS must only
// declare fields they intend to set. A fragment that writes a full spec.runtime
// block forces spot:false etc. onto children. The engine itself needs no
// special-casing — scalar child-wins handles the override correctly.

// mergeNotificationSpec merges parent and child *NotificationSpec via deepMerge.
// Child non-nil fields override parent; nil fields inherit from parent.
// Implemented via YAML round-trip through deepMerge.
func mergeNotificationSpec(parent, child *NotificationSpec) *NotificationSpec {
	if parent == nil {
		return child
	}
	if child == nil {
		return parent
	}
	pm := mustMarshalToMap(parent)
	cm := mustMarshalToMap(child)
	// Special handling for notification.slack.invites.emails: child non-empty
	// replaces parent (historic behavior tested by inherit_notification_test.go).
	// This preserves the "child-wins" semantics for email lists specifically.
	childEmails := childEmailsList(cm)
	parentEmails := childEmailsList(pm)
	merged := deepMerge(pm, cm)
	// Re-apply child-wins for invites.emails if child set a non-empty list.
	if len(childEmails) > 0 {
		setEmailsList(merged, childEmails)
	} else if len(parentEmails) > 0 {
		setEmailsList(merged, parentEmails)
	}
	var result NotificationSpec
	if err := roundTripMap(merged, &result); err != nil {
		return child
	}
	return &result
}

// childEmailsList extracts notification.slack.invites.emails from a raw notification map.
func childEmailsList(m map[string]any) []any {
	slack, _ := m["slack"].(map[string]any)
	if slack == nil {
		return nil
	}
	invites, _ := slack["invites"].(map[string]any)
	if invites == nil {
		return nil
	}
	emails, _ := toSlice(invites["emails"])
	return emails
}

// setEmailsList sets notification.slack.invites.emails in the merged map.
func setEmailsList(m map[string]any, emails []any) {
	slack, _ := m["slack"].(map[string]any)
	if slack == nil {
		return
	}
	invites, _ := slack["invites"].(map[string]any)
	if invites == nil {
		return
	}
	invites["emails"] = emails
	slack["invites"] = invites
	m["slack"] = slack
}

// mergeAgentSpec merges parent and child *AgentSpec at the typed level,
// using deepMerge for sub-maps. Avoids YAML round-trip to preserve Go
// native types in the Permissions map[string]any (int stays int, not uint64).
func mergeAgentSpec(parent, child *AgentSpec) *AgentSpec {
	if parent == nil {
		return child
	}
	if child == nil {
		return parent
	}
	result := &AgentSpec{}
	// Scalar: child wins when non-empty.
	result.Default = parent.Default
	if child.Default != "" {
		result.Default = child.Default
	}
	// Pointer sub-structs: recurse typed.
	result.Claude = mergeAgentClaudeSpec(parent.Claude, child.Claude)
	result.Codex = mergeAgentCodexSpec(parent.Codex, child.Codex)
	return result
}

// mergeAgentClaudeSpec merges two *AgentClaudeSpec values. Uses deepMerge
// for Permissions (map[string]any) to preserve native Go types.
func mergeAgentClaudeSpec(parent, child *AgentClaudeSpec) *AgentClaudeSpec {
	if parent == nil {
		return child
	}
	if child == nil {
		return parent
	}
	out := &AgentClaudeSpec{
		TrustedDirectories: parent.TrustedDirectories,
		Args:               parent.Args,
	}
	if len(child.TrustedDirectories) > 0 {
		out.TrustedDirectories = child.TrustedDirectories
	}
	if len(child.Args) > 0 {
		out.Args = child.Args
	}
	out.Tools = mergeAgentToolsSpec(parent.Tools, child.Tools)
	out.Permissions = mergePermissionsPassthrough(parent.Permissions, child.Permissions)
	return out
}

// mergeAgentCodexSpec merges two *AgentCodexSpec values.
func mergeAgentCodexSpec(parent, child *AgentCodexSpec) *AgentCodexSpec {
	if parent == nil {
		return child
	}
	if child == nil {
		return parent
	}
	out := &AgentCodexSpec{
		Tools: mergeAgentToolsSpec(parent.Tools, child.Tools),
		Args:  parent.Args,
	}
	if len(child.Args) > 0 {
		out.Args = child.Args
	}
	return out
}

// mergeAgentToolsSpec is value-typed; child non-empty slices replace parent's.
func mergeAgentToolsSpec(parent, child AgentToolsSpec) AgentToolsSpec {
	out := parent
	if len(child.AutoApprove) > 0 {
		out.AutoApprove = child.AutoApprove
	}
	if len(child.Deny) > 0 {
		out.Deny = child.Deny
	}
	return out
}

// mergePermissionsPassthrough key-merges two permissions maps; child wins on
// collision. Uses deepMerge (recursive map union, child keys win on collision)
// WITHOUT a YAML round-trip, preserving native Go types (int stays int).
func mergePermissionsPassthrough(parent, child map[string]any) map[string]any {
	if parent == nil {
		return child
	}
	if child == nil {
		return parent
	}
	return deepMerge(parent, child)
}

// merge combines parent and child profiles using the deepMerge engine.
// The generic merge subsumes the hand-written mergeSpecSection zoo.
func merge(parent, child *SandboxProfile) *SandboxProfile {
	pm := mustMarshalToMap(parent)
	cm := mustMarshalToMap(child)
	merged := deepMerge(pm, cm)
	delete(merged, "extends")
	result, err := fromMap(merged)
	if err != nil {
		// Fallback: return child wholesale (should never happen in practice).
		c := *child
		c.Extends = nil
		return &c
	}
	return result
}

// mustMarshalToMap serialises v to YAML then decodes into map[string]any.
// Panics only if goccy marshal fails on a known-good type (should never happen).
func mustMarshalToMap(v any) map[string]any {
	raw, err := goyaml.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := goyaml.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	if m == nil {
		return map[string]any{}
	}
	return m
}

// roundTripMap marshals m to YAML and unmarshals into dst.
func roundTripMap(m map[string]any, dst any) error {
	raw, err := goyaml.Marshal(m)
	if err != nil {
		return err
	}
	return goyaml.Unmarshal(raw, dst)
}
