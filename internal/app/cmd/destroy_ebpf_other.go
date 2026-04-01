//go:build !(linux && amd64)

package cmd

import "github.com/rs/zerolog"

// cleanupEBPF is a no-op on non-Linux platforms.
// eBPF is Linux-only; BPF artifacts do not exist on macOS, Windows, or other OSes.
// When km destroy runs from an operator's laptop (non-Linux), the actual BPF cleanup
// happens automatically when the EC2 instance is terminated (bpffs is in-memory).
func cleanupEBPF(sandboxID string, logger zerolog.Logger) {
	logger.Debug().
		Str("sandbox_id", sandboxID).
		Msg("eBPF cleanup skipped (non-Linux platform)")
}
