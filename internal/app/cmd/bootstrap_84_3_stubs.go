package cmd

// bootstrap_84_3_stubs.go — Temporary stubs for Phase 84.3 Plan 03 symbols.
// runBootstrapAll stub remains until Task 2 of Plan 03 replaces it.
// warnEmptyAccountIDs was implemented in bootstrap.go (Plan 03 Task 1) — stub removed.
//
// DO NOT REMOVE until Plan 03 Task 2 is complete.

import (
	"context"
	"io"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// runBootstrapAll is a stub for Plan 03 Task 2's --all routing implementation.
// Real implementation: bootstrap.go (Plan 03 Task 2).
func runBootstrapAll(ctx context.Context, cfg *config.Config, dryRun, plan, sharedSESOnly bool, out io.Writer) error {
	return nil
}
