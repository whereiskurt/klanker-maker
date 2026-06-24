package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	goyaml "github.com/goccy/go-yaml"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// NewValidateCmd creates the "km validate" subcommand.
// Usage: km validate <profile.yaml> [profile2.yaml ...]
//
// For each file:
//  1. Read file contents
//  2. Check for abstract fragment (metadata.abstract: true) — skip with message
//  3. If extends present: resolve FULL inheritance DAG using profile.Resolve(<leaf>)
//     then validate the fully-merged result
//  4. Run profile.Validate() on the (resolved or raw) profile bytes
//  5. Print errors or success per-file
//
// Exit code 1 if ANY file is invalid.
func NewValidateCmd(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate <profile.yaml> [profile2.yaml ...]",
		Short: "Validate one or more sandbox profile YAML files",
		Long:  helpText("validate"),
		Args:  cobra.MinimumNArgs(1),
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
// Returns true if validation failed, false if it passed (or skipped as abstract).
func validateFile(cfg *config.Config, filePath string) bool {
	log.Debug().Str("file", filePath).Msg("validating profile")

	// Step 1: Read file
	raw, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s: cannot read file: %v\n", filePath, err)
		return true
	}

	// Step 2: Abstract-fragment guard — skip standalone validation with a clear message.
	// Abstract fragments (metadata.abstract: true) are partial base definitions intended
	// for inheritance only. They deliberately omit required fields that concrete children
	// supply. Validating them standalone would produce spurious required-field errors.
	if profile.IsAbstractFragment(raw) {
		fmt.Printf("SKIP: %s is an abstract base fragment (metadata.abstract: true); validated only when merged into a leaf profile via extends:\n", filePath)
		return false
	}

	// Step 3: Parse to check for extends field
	parsed, parseErr := profile.Parse(raw)

	// Step 4: Resolve the FULL multi-parent extends DAG if extends is present.
	// We resolve the LEAF profile by name so the full DAG is walked (not just the
	// first parent). The leaf's own directory is prepended to searchPaths so that
	// siblings/base fragments in the same directory are found by name.
	if parseErr == nil && parsed.Extends.IsSet() {
		log.Debug().
			Str("file", filePath).
			Str("extends", strings.Join(parsed.Extends.List(), ",")).
			Msg("resolving full inheritance DAG")

		// Derive the leaf name: strip the .yaml suffix from the base filename.
		// e.g. "profiles/dc34.ami.yaml" → leaf name "dc34.ami"
		leafName := strings.TrimSuffix(filepath.Base(filePath), ".yaml")

		// Include the file's directory in search paths (FIRST) so that relative
		// sibling/base profiles resolve correctly (RESEARCH Pitfall 6).
		fileDir := filepath.Dir(filePath)
		searchPaths := append([]string{fileDir}, cfg.ProfileSearchPaths...)

		// Resolve the leaf: this walks the full DAG from the leaf through all parents.
		resolved, resolveErr := profile.Resolve(leafName, searchPaths)
		if resolveErr != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %s: failed to resolve extends %q: %v\n", filePath, strings.Join(parsed.Extends.List(), ","), resolveErr)
			return true
		}

		// Validate the fully-merged profile (not the raw partial child bytes).
		// We marshal the resolved struct to YAML and run the full Validate() pipeline
		// (schema + semantic) on the merged result. This correctly handles partial
		// child profiles that inherit required fields from their parents.
		mergedBytes, marshalErr := goyaml.Marshal(resolved)
		if marshalErr != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %s: failed to marshal resolved profile: %v\n", filePath, marshalErr)
			return true
		}

		errs := profile.Validate(mergedBytes)
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

	// No extends — validate raw bytes directly
	errs := profile.Validate(raw)

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
