package cmd

// env_84_3_stubs.go — Temporary stubs for Phase 84.3 Plan 04 env.go symbols.
// These stubs allow Plan 02's test binary to compile while Plan 04 functions
// are not yet implemented. Plan 04 will replace these stubs with real env.go.
//
// DO NOT REMOVE until Plan 04 is complete.

import (
	"io"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// NewEnvCmd is a stub for Plan 04's km env command.
// Real implementation: env.go (Plan 04).
func NewEnvCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{Use: "env"}
}

// runEnvExport is a stub for Plan 04's env export helper.
// Real implementation: env.go (Plan 04).
func runEnvExport(cfg *config.Config, out io.Writer, includeAWSProfile bool) error {
	return nil
}
