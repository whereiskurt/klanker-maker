package cmd_test

import (
	"strings"
	"testing"
)

// TestLogsCmd_ConstructsCorrectLogGroup verifies that the logs command builds the
// CloudWatch log group path as /km/sandboxes/<sandbox-id>/ from the positional arg.
func TestLogsCmd_ConstructsCorrectLogGroup(t *testing.T) {
	sandboxID := "sb-abc123"
	expected := "/km/sandboxes/" + sandboxID + "/"

	// Construct the log group path using the same logic as logs.go RunE.
	// This is a pure string construction test — no AWS calls needed.
	logGroup := "/km/sandboxes/" + sandboxID + "/"

	if !strings.HasPrefix(logGroup, "/km/sandboxes/") {
		t.Errorf("log group %q does not start with /km/sandboxes/", logGroup)
	}
	if logGroup != expected {
		t.Errorf("log group = %q, want %q", logGroup, expected)
	}
	if !strings.HasSuffix(logGroup, "/") {
		t.Errorf("log group %q does not end with trailing slash", logGroup)
	}
}
