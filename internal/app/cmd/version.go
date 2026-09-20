package cmd

import (
	"text/template"

	"github.com/spf13/cobra"
)

// NewVersionCmd returns `km version`, an alias for `km --version`. It renders
// cobra's own version template against the root command so the two can never
// print different things.
func NewVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "version",
		Short:        "Print the km version (same as km --version)",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(c *cobra.Command, _ []string) error {
			root := c.Root()
			t, err := template.New("version").Parse(root.VersionTemplate())
			if err != nil {
				return err
			}
			return t.Execute(root.OutOrStdout(), root)
		},
	}
}
