package cmd

import "strings"

// nameContainsSandboxIDToken reports whether name contains sbID as a
// hyphen-delimited token — i.e. preceded by '-' (or string start) and
// followed by '-' or end-of-string. This avoids the substring-collision
// false-negatives that strings.Contains(name, sbID) produces when a sandbox
// ID coincidentally appears as a substring of an unrelated resource name.
//
// sbID itself contains a dash ({profilePrefix}-{8hex} per
// pkg/compiler/sandbox_id.go), so this cannot use strings.Split.
//
// Naming patterns this handles:
//
//	{prefix}-{component}-{sandboxID}                    (suffix; IAM, KMS)
//	{prefix}-{component}-{sandboxID}-{regionLabel}      (middle; regional KMS)
//	{prefix}-at-{command}-{sandboxID}-{timeExpr}        (middle; schedules)
func nameContainsSandboxIDToken(name, sbID string) bool {
	if sbID == "" || name == "" {
		return false
	}
	// Match start-of-string OR a "-" delimiter immediately preceding sbID.
	// Walk every "-"+sbID occurrence; if the followed character is end-of-string
	// or "-", it is a true token match.
	if strings.HasPrefix(name, sbID) {
		end := len(sbID)
		if end == len(name) || name[end] == '-' {
			return true
		}
	}
	needle := "-" + sbID
	rest := name
	base := 0
	for {
		i := strings.Index(rest, needle)
		if i < 0 {
			return false
		}
		end := base + i + len(needle)
		if end == len(name) || name[end] == '-' {
			return true
		}
		rest = rest[i+1:]
		base += i + 1
	}
}
