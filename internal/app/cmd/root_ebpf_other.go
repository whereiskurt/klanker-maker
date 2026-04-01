//go:build !linux

package cmd

import (
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// registerEBPFCmds is a no-op on non-Linux platforms.
// eBPF enforcement is Linux-only (requires Linux kernel BPF subsystem).
func registerEBPFCmds(root *cobra.Command, cfg *config.Config) {
	// eBPF commands not available on this platform.
	_ = root
	_ = cfg
}
