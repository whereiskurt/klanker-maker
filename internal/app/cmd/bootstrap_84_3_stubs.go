package cmd

// bootstrap_84_3_stubs.go — Temporary stubs for Phase 84.3 Plan 03 symbols.
// These stubs allow Plan 02's test binary to compile while Plan 03 functions
// are not yet implemented. Plan 03 will replace these stubs with real implementations.
//
// DO NOT REMOVE until Plan 03 is complete.

import (
	"context"
	"io"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// runBootstrapAll is a stub for Plan 03's --all routing implementation.
// Real implementation: bootstrap.go (Plan 03).
func runBootstrapAll(ctx context.Context, cfg *config.Config, dryRun, plan, sharedSESOnly bool, out io.Writer) error {
	return nil
}

// warnEmptyAccountIDs is a stub for Plan 03's banner WARN helper.
// Real implementation: bootstrap.go (Plan 03).
func warnEmptyAccountIDs(cfg *config.Config, w io.Writer) {}
