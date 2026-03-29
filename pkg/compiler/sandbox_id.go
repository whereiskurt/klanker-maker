// Package compiler translates a SandboxProfile into Terragrunt artifacts
// (service.hcl, user-data.sh) for EC2 and ECS substrates. It is a pure
// function with no AWS side effects — fully testable without credentials.
package compiler

import (
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// GenerateSandboxID returns a unique sandbox identifier in the form {prefix}-XXXXXXXX,
// where XXXXXXXX is the first 8 hex characters of a random UUID.
// If prefix is empty, it defaults to "sb" for backwards compatibility.
// Format matches: ^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$
func GenerateSandboxID(prefix string) string {
	if prefix == "" {
		prefix = "sb"
	}
	id := uuid.New().String()
	// UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	// Take the first 8 hex chars (before the first dash)
	hex := strings.ReplaceAll(id, "-", "")
	return prefix + "-" + hex[:8]
}

// validSandboxIDPattern matches generalized sandbox IDs of the form
// {prefix}-{8hex} where prefix is 1-12 lowercase alphanumeric chars starting
// with a letter.
var validSandboxIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$`)

// IsValidSandboxID returns true if id matches the expected sandbox ID format:
// a lowercase alpha-start prefix (1-12 chars) followed by a dash and 8 hex chars.
func IsValidSandboxID(id string) bool {
	return validSandboxIDPattern.MatchString(id)
}
