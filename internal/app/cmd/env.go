package cmd

// env.go — Phase 84.3 closure (g) KM-ENV-EXPORT-HELPER.
// Implements the "km env" subcommand that prints the full export KM_* block
// for use with `eval $(km env)` in an operator shell. Follows the info.go
// file-per-command pattern verbatim (NewXxxCmd + runXxx helpers).

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// NewEnvCmd creates the "km env" subcommand.
// Prints the full `export KM_*` block that Terragrunt subprocesses consume via
// site.hcl get_env(). Use with `eval $(km env)` to set up an operator shell for
// direct terragrunt invocation.
func NewEnvCmd(cfg *config.Config) *cobra.Command {
	var includeAWSProfile bool
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Print exportable KM_* env block for use with eval $(km env)",
		Long:  "Print `export KEY=value` lines for every KM_* var that terragrunt subprocesses (via site.hcl get_env()) read. Use with `eval $(km env)` to set up an operator shell for direct terragrunt invocation. Excludes AWS_PROFILE by default (operator-shell-local; opt-in via --aws-profile).",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEnvExport(cfg, cmd.OutOrStdout(), includeAWSProfile)
		},
	}
	cmd.Flags().BoolVar(&includeAWSProfile, "aws-profile", false, "Include AWS_PROFILE in the export block")
	return cmd
}

// runEnvExport writes the full `export KM_*` block to w. Keys are derived from
// cfg and sorted for deterministic output. If includeAWSProfile is true,
// `export AWS_PROFILE=<ambient>` is appended at the end (not sorted with KM_*
// vars — it is not a KM_* var and is intentionally separate).
func runEnvExport(cfg *config.Config, w io.Writer, includeAWSProfile bool) error {
	vars := map[string]string{
		"KM_RESOURCE_PREFIX":       cfg.GetResourcePrefix(),
		"KM_REGION":                cfg.PrimaryRegion,
		"KM_REGION_LABEL":          cfg.GetRegionLabel(),
		"KM_DOMAIN":                cfg.Domain,
		"KM_EMAIL_SUBDOMAIN":       cfg.EmailSubdomain,
		"KM_ROUTE53_ZONE_ID":       cfg.Route53ZoneID,
		"KM_ACCOUNTS_ORGANIZATION": cfg.OrganizationAccountID,
		"KM_ACCOUNTS_DNS_PARENT":   cfg.DNSParentAccountID,
		"KM_ACCOUNTS_APPLICATION":  cfg.ApplicationAccountID,
		"KM_ARTIFACTS_BUCKET":      cfg.ArtifactsBucket,
		"KM_OPERATOR_EMAIL":        cfg.OperatorEmail,
	}

	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic output

	for _, k := range keys {
		fmt.Fprintf(w, "export %s=%s\n", k, vars[k])
	}
	if includeAWSProfile {
		fmt.Fprintf(w, "export AWS_PROFILE=%s\n", os.Getenv("AWS_PROFILE"))
	}
	return nil
}
