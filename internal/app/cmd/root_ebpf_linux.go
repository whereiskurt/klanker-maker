//go:build linux && amd64

package cmd

import (
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// registerEBPFCmds registers Linux-only eBPF subcommands on the root command.
// These commands require Linux kernel support and are not available on other platforms.
func registerEBPFCmds(root *cobra.Command, cfg *config.Config) {
	root.AddCommand(NewEBPFAttachCmd(cfg))
}
