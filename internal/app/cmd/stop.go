package cmd

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// NewStopCmd creates the "km stop" subcommand.
func NewStopCmd(cfg *config.Config) *cobra.Command {
	var remote bool

	cmd := &cobra.Command{
		Use:          "stop <sandbox-id | #number>",
		Short:        "Stop a sandbox's EC2 instance (preserves infrastructure for restart)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}
			if remote {
				return publishRemoteCommand(ctx, sandboxID, "stop")
			}
			return runStop(ctx, sandboxID)
		},
	}

	cmd.Flags().BoolVar(&remote, "remote", false, "Dispatch stop to Lambda via EventBridge")

	return cmd
}

func runStop(ctx context.Context, sandboxID string) error {
	awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	ec2Client := ec2.NewFromConfig(awsCfg)

	descOut, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:km:sandbox-id"), Values: []string{sandboxID}},
			{Name: aws.String("instance-state-name"), Values: []string{"running"}},
		},
	})
	if err != nil {
		return fmt.Errorf("describe instances: %w", err)
	}

	stopped := 0
	for _, res := range descOut.Reservations {
		for _, inst := range res.Instances {
			instanceID := aws.ToString(inst.InstanceId)
			fmt.Printf("Stopping instance %s...\n", instanceID)
			if _, err := ec2Client.StopInstances(ctx, &ec2.StopInstancesInput{
				InstanceIds: []string{instanceID},
			}); err != nil {
				return fmt.Errorf("stop instance %s: %w", instanceID, err)
			}
			stopped++
		}
	}

	if stopped == 0 {
		return fmt.Errorf("no running instances found for sandbox %s", sandboxID)
	}

	fmt.Printf("Sandbox %s stopped. Use 'km budget add %s --compute <amount>' to restart.\n", sandboxID, sandboxID)
	return nil
}
