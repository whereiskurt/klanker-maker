package cmd_test

import (
	"context"
	"testing"

	"github.com/whereiskurt/klankrmkr/internal/app/config"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
)

// TestResolveSandboxID_CustomPrefix verifies that ResolveSandboxID recognizes
// sandbox IDs with custom prefixes (not sb-) as valid IDs (returns as-is).
func TestResolveSandboxID_CustomPrefix(t *testing.T) {
	cfg := &config.Config{}
	ctx := context.Background()

	tests := []struct {
		ref     string
		wantID  string
		wantErr bool
	}{
		// Custom prefix IDs — must be recognized and returned as-is
		{"claude-abc12345", "claude-abc12345", false},
		{"build-abc12345", "build-abc12345", false},
		{"a-abc12345", "a-abc12345", false},
		// Legacy sb- prefix — backwards compat
		{"sb-abc12345", "sb-abc12345", false},
		// Numeric string — falls through to list lookup (requires AWS, expect error)
		{"3", "", true},
		// Invalid format — not an ID, not a number — expect error
		{"NOT-VALID", "", true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.ref, func(t *testing.T) {
			got, err := cmd.ResolveSandboxID(ctx, cfg, tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ResolveSandboxID(%q): expected error, got %q", tt.ref, got)
				}
				return
			}
			if err != nil {
				t.Errorf("ResolveSandboxID(%q): unexpected error: %v", tt.ref, err)
				return
			}
			if got != tt.wantID {
				t.Errorf("ResolveSandboxID(%q) = %q, want %q", tt.ref, got, tt.wantID)
			}
		})
	}
}
