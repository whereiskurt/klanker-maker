package cmd

// init_84_3_stubs.go — Temporary stubs for Phase 84.3 Plan 04 init.go symbols.
// These stubs allow Plan 02's test binary to compile while Plan 04 functions
// are not yet implemented. Plan 04 will replace these stubs with real implementations.
//
// DO NOT REMOVE until Plan 04 is complete.

import (
	"context"
	"io"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ensureArtifactsBucketExists is a stub for Plan 04's init.go hard-fail helper.
// Real implementation: init.go (Plan 04).
func ensureArtifactsBucketExists(ctx context.Context, cfg *config.Config, out io.Writer, client S3HeadBucketAPI) error {
	return nil
}
