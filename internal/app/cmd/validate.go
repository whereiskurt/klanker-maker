package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// NewValidateCmd creates the "km validate" subcommand.
// Usage: km validate <profile.yaml> [profile2.yaml ...]
//
// For each file:
//  1. Read file contents
//  2. Parse to check for extends field
//  3. If extends present: resolve inheritance chain using profile.Resolve()
//  4. Run profile.Validate() on the resolved profile bytes
//  5. Print errors or success per-file
//
// Exit code 1 if ANY file is invalid.
func NewValidateCmd(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate <profile.yaml> [profile2.yaml ...]",
		Short: "Validate one or more sandbox profile YAML files",
		Long:  helpText("validate"),
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cfg, args)
		},
	}

	return cmd
}

// runValidate processes each profile file and reports validation results.
// It continues checking all files even if earlier ones fail.
func runValidate(cfg *config.Config, files []string) error {
	anyFailed := false

	for _, filePath := range files {
		failed := validateFile(cfg, filePath)
		if failed {
			anyFailed = true
		}
	}

	if anyFailed {
		return fmt.Errorf("one or more profiles failed validation")
	}
	return nil
}

// validateFile validates a single profile YAML file.
// Returns true if validation failed, false if it passed.
func validateFile(cfg *config.Config, filePath string) bool {
	log.Debug().Str("file", filePath).Msg("validating profile")

	// Step 1: Read file
	raw, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s: cannot read file: %v\n", filePath, err)
		return true
	}

	// Step 2: Parse to check for extends field
	parsed, parseErr := profile.Parse(raw)

	// Step 3: Resolve inheritance chain if extends is present.
	// Add the file's directory to search paths so sibling profiles can be resolved.
	// Resolve() returns a fully merged profile with all inherited fields applied.
	var validationTarget []byte
	if parseErr == nil && parsed.Extends != "" {
		log.Debug().
			Str("file", filePath).
			Str("extends", parsed.Extends).
			Msg("resolving inheritance chain")

		// Include the file's directory in search paths for relative profile resolution
		fileDir := filepath.Dir(filePath)
		searchPaths := append([]string{fileDir}, cfg.ProfileSearchPaths...)

		resolved, resolveErr := profile.Resolve(parsed.Extends, searchPaths)
		if resolveErr != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %s: failed to resolve extends %q: %v\n", filePath, parsed.Extends, resolveErr)
			return true
		}

		// Marshal the resolved (parent) profile, then merge child fields on top.
		// Since profile.Resolve returns the parent chain fully resolved, we need
		// to apply the child's overrides. We do this by validating the raw child
		// bytes for schema correctness, then validating the semantic constraints
		// on the merged profile (resolved parent + child overrides).
		//
		// Schema validation: run on raw child bytes (catches structural issues)
		schemaErrs := profile.ValidateSchema(raw)
		// Semantic validation: run on the resolved profile (catches logical issues
		// that depend on inherited values, e.g. ttl vs idleTimeout from parent)
		semanticErrs := profile.ValidateSemantic(resolved)

		allErrs := append(schemaErrs, semanticErrs...)
		if len(allErrs) > 0 {
			failed := false
			for _, e := range allErrs {
				if e.IsWarning {
					fmt.Fprintf(os.Stderr, "WARN: %s: %s\n", filePath, e.Error())
				} else {
					fmt.Fprintf(os.Stderr, "ERROR: %s: %s\n", filePath, e.Error())
					failed = true
				}
			}
			if !failed {
				fmt.Printf("%s: valid (with warnings)\n", filePath)
				return false
			}
			return true
		}

		fmt.Printf("%s: valid\n", filePath)
		return false
	}

	// No extends — validate raw bytes directly
	validationTarget = raw

	// Step 4: Run validation
	errs := profile.Validate(validationTarget)

	// Step 5: Report results — separate warnings from errors.
	// Warnings (IsWarning=true) print with WARN: prefix but do not cause exit 1.
	// Errors print with ERROR: prefix and flip anyFailed in the caller.
	if len(errs) > 0 {
		failed := false
		for _, e := range errs {
			if e.IsWarning {
				fmt.Fprintf(os.Stderr, "WARN: %s: %s\n", filePath, e.Error())
			} else {
				fmt.Fprintf(os.Stderr, "ERROR: %s: %s\n", filePath, e.Error())
				failed = true
			}
		}
		if !failed {
			fmt.Printf("%s: valid (with warnings)\n", filePath)
			return false
		}
		return true
	}

	fmt.Printf("%s: valid\n", filePath)
	return false
}
