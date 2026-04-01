//go:build linux && amd64

package cmd

import (
	"github.com/rs/zerolog"
	ebpfpkg "github.com/whereiskurt/klankrmkr/pkg/ebpf"
)

// cleanupEBPF removes any pinned BPF programs and maps for the sandbox.
// This is a no-op for proxy-mode sandboxes that never had eBPF attached,
// because IsPinned() checks for the presence of the bpffs pin directory.
//
// Primary cleanup mechanism for remote destroy: EC2 instance termination
// removes bpffs automatically (it is an in-memory filesystem). This function
// is effective when km destroy runs ON the same Linux host as the sandbox
// (e.g., automated in-instance cleanup or local development).
func cleanupEBPF(sandboxID string, logger zerolog.Logger) {
	if !ebpfpkg.IsPinned(sandboxID) {
		logger.Debug().
			Str("sandbox_id", sandboxID).
			Msg("no pinned eBPF programs found, skipping cleanup")
		return
	}
	logger.Info().
		Str("sandbox_id", sandboxID).
		Msg("cleaning up pinned eBPF programs and maps")
	if err := ebpfpkg.Cleanup(sandboxID); err != nil {
		logger.Warn().
			Err(err).
			Str("sandbox_id", sandboxID).
			Msg("eBPF cleanup failed (non-fatal, instance will be terminated)")
	} else {
		logger.Info().
			Str("sandbox_id", sandboxID).
			Msg("eBPF cleanup complete")
	}
}
