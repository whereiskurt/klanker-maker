// Package compiler translates a SandboxProfile into Terragrunt artifacts
// (service.hcl, user-data.sh) for EC2 and ECS substrates. It is a pure
// function with no AWS side effects — fully testable without credentials.
package compiler

import (
	"strings"

	"github.com/google/uuid"
)

// GenerateSandboxID returns a unique sandbox identifier in the form sb-XXXXXXXX,
// where XXXXXXXX is the first 8 hex characters of a random UUID.
// Format matches: ^sb-[a-f0-9]{8}$
func GenerateSandboxID() string {
	id := uuid.New().String()
	// UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	// Take the first 8 hex chars (before the first dash)
	hex := strings.ReplaceAll(id, "-", "")
	return "sb-" + hex[:8]
}
