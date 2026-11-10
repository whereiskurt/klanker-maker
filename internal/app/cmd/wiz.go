package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/smithy-go"
	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/wizoutpost"
)

// WizStackAPI is the slice of the CloudFormation client km wiz status uses.
type WizStackAPI interface {
	DescribeStacks(ctx context.Context, in *cloudformation.DescribeStacksInput, optFns ...func(*cloudformation.Options)) (*cloudformation.DescribeStacksOutput, error)
}

// NewWizCmd returns the `km wiz` command group.
func NewWizCmd(cfg *config.Config) *cobra.Command {
	wizCmd := &cobra.Command{
		Use:          "wiz",
		Short:        "Inspect the Wiz Outpost connector",
		SilenceUsage: true,
	}
	wizCmd.AddCommand(&cobra.Command{
		Use:          "status",
		Short:        "Show the Wiz Outpost connector stack and the values to paste into Wiz",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			awsCfg, err := kmaws.LoadAWSConfig(ctx, cfg.AWSProfile)
			if err != nil {
				return fmt.Errorf("failed to load AWS config: %w", err)
			}
			return runWizStatus(ctx, cmd.OutOrStdout(), cloudformation.NewFromConfig(awsCfg),
				cfg.GetResourcePrefix(), cfg.Wiz.Outpost != nil)
		},
	})
	return wizCmd
}

// isStackNotFound reports whether err is CloudFormation's "stack does not exist".
func isStackNotFound(err error) bool {
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && apiErr.ErrorCode() == "ValidationError" &&
		strings.Contains(apiErr.ErrorMessage(), "does not exist")
}

// runWizStatus describes the connector stack and prints its state and the Wiz
// paste-out values. A missing stack is reported, not an error.
func runWizStatus(ctx context.Context, w io.Writer, api WizStackAPI, prefix string, configured bool) error {
	name := wizoutpost.StackName(prefix)
	out, err := api.DescribeStacks(ctx, &cloudformation.DescribeStacksInput{StackName: &name})
	if err != nil {
		if !isStackNotFound(err) {
			return fmt.Errorf("describe stack %s: %w", name, err)
		}
		if !configured {
			fmt.Fprintln(w, "Wiz Outpost connector: not configured (no wiz.outpost block in km-config.yaml)")
		} else {
			fmt.Fprintf(w, "stack %s not found — run km init --wiz --dry-run=false\n", name)
		}
		return nil
	}
	if len(out.Stacks) == 0 {
		fmt.Fprintf(w, "stack %s not found — run km init --wiz --dry-run=false\n", name)
		return nil
	}
	stack := out.Stacks[0]

	if !configured {
		fmt.Fprintln(w, "Wiz Outpost stack exists but wiz: is not configured — remove the connector in Wiz, then km uninit or delete the stack")
	}
	fmt.Fprintf(w, "Stack: %s\n", name)
	fmt.Fprintf(w, "Status: %s\n", string(stack.StackStatus))
	for _, p := range stack.Parameters {
		if p.ParameterKey != nil && *p.ParameterKey == "WizVersionLastUpdated" && p.ParameterValue != nil {
			fmt.Fprintf(w, "Template: %s\n", *p.ParameterValue)
		}
	}
	outputs := map[string]string{}
	for _, o := range stack.Outputs {
		if o.OutputKey != nil && o.OutputValue != nil {
			outputs[*o.OutputKey] = *o.OutputValue
		}
	}
	fmt.Fprintln(w)
	printWizPasteOut(w, outputs["WizAccessRoleARNID"], outputs["WizScannerRoleARNID"], outputs["ExternalID"])
	return nil
}
