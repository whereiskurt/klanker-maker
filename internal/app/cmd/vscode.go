package cmd

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// NewVSCodeCmd returns the `km vscode` parent command. Phase 73.
func NewVSCodeCmd(cfg *config.Config) *cobra.Command {
	return newVSCodeCmdInternal(cfg, nil, nil, nil)
}

// newVSCodeCmdInternal is the dependency-injectable constructor for km vscode.
// Wave 3 Plan 73-06 implements the RunE bodies; Wave 0 ships compile-only stubs.
func newVSCodeCmdInternal(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
	parent := &cobra.Command{
		Use:          "vscode",
		Short:        "Connect local VS Code to a sandbox via Remote-SSH over SSM",
		SilenceUsage: true,
	}
	parent.AddCommand(&cobra.Command{
		Use:   "start <sandbox-id>",
		Short: "Start a Remote-SSH port-forward and configure local SSH for VS Code",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("vscode start: not implemented (Wave 3 Plan 73-06)")
		},
	})
	parent.AddCommand(&cobra.Command{
		Use:   "status <sandbox-id>",
		Short: "Report whether sshd is active and the authorized_keys are installed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("vscode status: not implemented (Wave 3 Plan 73-06)")
		},
	})
	// Suppress unused-parameter warnings until Wave 3 wires them.
	_ = cfg
	_ = fetcher
	_ = execFn
	_ = ssmClient
	return parent
}
