// Package cmd provides the Cobra command tree for the km CLI.
package cmd

import (
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// NewRootCmd creates the root "km" command with global flags and subcommands attached.
func NewRootCmd(cfg *config.Config) *cobra.Command {
	var logLevel string

	root := &cobra.Command{
		Use:   "km",
		Short: "klankrmkr — sandbox profile management CLI",
		Long:  helpText("root"),
		Version: cfg.Version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return configureLogging(logLevel)
		},
		// Suppress usage on error — callers see the error message, not usage
		SilenceUsage: true,
	}

	// Global flags
	root.PersistentFlags().StringVar(
		&logLevel, "log-level", cfg.LogLevel,
		"Log verbosity level (trace, debug, info, warn, error)",
	)

	// Register subcommands
	root.AddCommand(NewInitCmd(cfg))
	root.AddCommand(NewValidateCmd(cfg))
	root.AddCommand(NewCreateCmd(cfg))
	root.AddCommand(NewDestroyCmd(cfg))
	root.AddCommand(NewListCmd(cfg))
	root.AddCommand(NewStatusCmd(cfg))
	root.AddCommand(NewLogsCmd(cfg))

	// "km configure" with "km configure github" as a subcommand.
	configureCmd := NewConfigureCmd(cfg)
	configureCmd.AddCommand(NewConfigureGitHubCmd(cfg))
	root.AddCommand(configureCmd)

	root.AddCommand(NewBootstrapCmd(cfg))
	root.AddCommand(NewBudgetCmd(cfg))
	root.AddCommand(NewShellCmd(cfg))
	root.AddCommand(NewDoctorCmd(cfg))

	return root
}

// Execute creates the Config, builds the command tree, and runs the CLI.
// It exits with code 1 on any error.
func Execute() {
	cfg, err := config.Load()
	if err != nil {
		log.Error().Err(err).Msg("failed to load configuration")
		os.Exit(1)
	}
	cfg.Version = "0.1.0-dev"

	root := NewRootCmd(cfg)
	if err := root.Execute(); err != nil {
		// cobra already prints the error; just exit non-zero
		os.Exit(1)
	}
}

// configureLogging sets the global zerolog log level from the flag value.
func configureLogging(level string) error {
	parsed, err := zerolog.ParseLevel(level)
	if err != nil {
		return err
	}
	zerolog.SetGlobalLevel(parsed)
	return nil
}
