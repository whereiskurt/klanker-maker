// Package cmd provides the Cobra command tree for the km CLI.
package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/version"
)

// NewRootCmd creates the root "km" command with global flags and subcommands attached.
func NewRootCmd(cfg *config.Config) *cobra.Command {
	var logLevel string

	root := &cobra.Command{
		Use:   "km",
		Short: "klanker-maker — sandbox profile management CLI",
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
	root.AddCommand(NewUninitCmd(cfg))
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

	// "km github" as a shortcut for "km configure github --setup"
	root.AddCommand(&cobra.Command{
		Use:   "github",
		Short: "Shortcut for 'km configure github --setup'",
		RunE: func(cmd *cobra.Command, args []string) error {
			setupCmd := NewConfigureGitHubCmd(cfg)
			setupCmd.SetArgs(append([]string{"--setup"}, args...))
			return setupCmd.Execute()
		},
	})

	root.AddCommand(NewExtendCmd(cfg))
	root.AddCommand(NewStopCmd(cfg))
	root.AddCommand(NewPauseCmd(cfg))
	root.AddCommand(NewResumeCmd(cfg))
	root.AddCommand(NewLockCmd(cfg))
	root.AddCommand(NewUnlockCmd(cfg))
	root.AddCommand(NewBootstrapCmd(cfg))
	root.AddCommand(NewUnbootstrapCmd(cfg))
	root.AddCommand(NewBudgetCmd(cfg))
	root.AddCommand(NewShellCmd(cfg))
	root.AddCommand(NewAgentCmd(cfg))
	root.AddCommand(NewDoctorCmd(cfg))
	root.AddCommand(NewRollCmd(cfg))
	root.AddCommand(NewRsyncCmd(cfg))
	root.AddCommand(NewCloneCmd(cfg))
	root.AddCommand(NewOtelCmd(cfg))
	root.AddCommand(NewEnvCmd(cfg))
	root.AddCommand(NewInfoCmd(cfg))
	root.AddCommand(NewEmailCmd(cfg))
	root.AddCommand(NewAMICmd(cfg))
	root.AddCommand(NewSlackCmd(cfg))
	root.AddCommand(NewVSCodeCmd(cfg))
	root.AddCommand(NewClusterCmd(cfg))

	// "km at" — schedule deferred and recurring sandbox operations.
	// "km schedule" is registered as an alias so both work identically.
	atCmd := NewAtCmd(cfg)
	atCmd.Aliases = []string{"schedule"}
	atCmd.AddCommand(NewAtListCmd(cfg))
	atCmd.AddCommand(NewAtCancelCmd(cfg))
	root.AddCommand(atCmd)

	// Register Linux-only eBPF commands (no-op on other platforms).
	registerEBPFCmds(root, cfg)

	// Shell completion subcommand
	root.AddCommand(&cobra.Command{
		Use:          "completion [bash|zsh]",
		Short:        "Generate shell completion script",
		Long:         helpText("completion"),
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(os.Stdout)
			case "zsh":
				return root.GenZshCompletion(os.Stdout)
			default:
				return fmt.Errorf("unsupported shell %q: use bash or zsh", args[0])
			}
		},
	})

	return root
}

// Execute creates the Config, builds the command tree, and runs the CLI.
// It exits with code 1 on any error, or with the typed exit code when the
// returned error is an *ExitCodeError (e.g. from --wait queue drain failure).
//
// This is the ONE place os.Exit is called for typed exit codes. Keeping it
// here (outside RunE) ensures that any defer statements registered by RunE
// or Cobra middleware have already run by the time os.Exit fires.
func Execute() {
	cfg, err := config.Load()
	if err != nil {
		log.Error().Err(err).Msg("failed to load configuration")
		os.Exit(1)
	}
	cfg.Version = version.String()

	root := NewRootCmd(cfg)
	if err := root.Execute(); err != nil {
		// Detect *ExitCodeError (e.g. --wait queue drain with first-failure exit code).
		// Cobra has already printed the error message via its normal error path;
		// we suppress the duplicate fmt.Fprintf here and just exit with the carried code.
		var exitErr *ExitCodeError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		// Other errors: cobra already printed the error; just exit non-zero.
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

// printBanner prints a command header to stderr with version and timestamp.
func printBanner(cmd, context string) {
	fprintBanner(os.Stderr, cmd, context)
}

// fprintBanner prints a command header to a writer with version and timestamp.
func fprintBanner(w io.Writer, cmd, context string) {
	now := time.Now().Local().Format("3:04PM 2006-01-02")
	fmt.Fprintf(w, "%s — %s [%s] %s\n", cmd, context, version.Number, now)
	fmt.Fprintln(w, strings.Repeat("─", 46))
}
