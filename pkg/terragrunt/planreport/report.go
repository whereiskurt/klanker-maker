package planreport

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Report holds the classified resource changes from a single terraform plan.
// Module is set by the caller (planreport.Parse does not set it).
type Report struct {
	Module      string
	Adds        []ResourceChange
	Changes     []ResourceChange
	Destroys    []ResourceChange
	Replaces    []ResourceChange
	ParseFailed bool
	ParseError  error
}

// ResourceChange represents a single resource that terraform plans to act on.
type ResourceChange struct {
	Address string
	Type    string
	Action  string // "create" | "update" | "delete" | "replace" | "no-op" | "read" | "forget"
}

// rawPlan is the minimal struct needed to unmarshal terraform show -json output.
// We hand-roll this rather than importing github.com/hashicorp/terraform-json
// because that library's authors explicitly recommend against using it as a
// forward-compat parser (it drops unknown attributes). We only need 3 fields
// per resource_change: address, type, and change.actions.
type rawPlan struct {
	FormatVersion   string              `json:"format_version"`
	ResourceChanges []rawResourceChange `json:"resource_changes"`
}

type rawResourceChange struct {
	Address string    `json:"address"`
	Type    string    `json:"type"`
	Change  rawChange `json:"change"`
}

type rawChange struct {
	Actions []string `json:"actions"`
}

// Parse parses the JSON bytes produced by `terraform show -json <planfile>`.
// It accepts any format_version starting with "1." (the stable Terraform
// 1.x API — https://developer.hashicorp.com/terraform/internals/json-format).
// The returned Report.Module field is empty; the caller sets it.
func Parse(jsonBytes []byte) (Report, error) {
	var raw rawPlan
	if err := json.Unmarshal(jsonBytes, &raw); err != nil {
		return Report{}, fmt.Errorf("parse plan JSON: %w", err)
	}
	if err := isCompatibleFormatVersion(raw.FormatVersion); err != nil {
		return Report{}, err
	}

	var report Report
	for _, rc := range raw.ResourceChanges {
		action := classifyAction(rc.Change.Actions)
		ch := ResourceChange{
			Address: rc.Address,
			Type:    rc.Type,
			Action:  action,
		}
		switch action {
		case "create":
			report.Adds = append(report.Adds, ch)
		case "update":
			report.Changes = append(report.Changes, ch)
		case "delete":
			report.Destroys = append(report.Destroys, ch)
		case "replace":
			report.Replaces = append(report.Replaces, ch)
		// "no-op", "read", "forget", "unknown" — skip
		}
	}
	return report, nil
}

// isCompatibleFormatVersion returns nil if s is a 1.x format_version string,
// or an error otherwise. Empty string and anything not beginning with "1."
// are rejected.
func isCompatibleFormatVersion(s string) error {
	if s == "" || !strings.HasPrefix(s, "1.") {
		return fmt.Errorf("unsupported plan format_version %q (want 1.x)", s)
	}
	// The prefix "1." is sufficient to accept any "1.M" or "1.M.m" version.
	// Reject bare "1." (no minor version) as malformed.
	rest := strings.TrimPrefix(s, "1.")
	if rest == "" {
		return fmt.Errorf("unsupported plan format_version %q (want 1.x)", s)
	}
	return nil
}

// classifyAction maps a terraform actions array to a single action string.
//
// Terraform JSON schema (format_version 1.x):
//
//	[]                            → "no-op"
//	["no-op"]                     → "no-op"
//	["create"]                    → "create"
//	["read"]                      → "read"
//	["update"]                    → "update"
//	["delete"]                    → "delete"
//	["delete","create"] or
//	["create","delete"]           → "replace"
//	["forget"]                    → "forget"
//	anything else                 → "unknown"
func classifyAction(actions []string) string {
	switch len(actions) {
	case 0:
		return "no-op"
	case 1:
		return actions[0]
	case 2:
		a, b := actions[0], actions[1]
		if (a == "delete" && b == "create") || (a == "create" && b == "delete") {
			return "replace"
		}
		return "unknown"
	default:
		return "unknown"
	}
}
