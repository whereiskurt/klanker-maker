package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// NewStopCmd creates the "km stop" subcommand.
func NewStopCmd(cfg *config.Config) *cobra.Command {
	return NewStopCmdWithPublisher(cfg, nil)
}

// NewStopCmdWithPublisher builds the stop command with an optional injected
// RemoteCommandPublisher. Pass nil to use the real AWS-backed publisher.
// Used in tests to inject a mock publisher for --remote path testing.
func NewStopCmdWithPublisher(cfg *config.Config, pub RemoteCommandPublisher) *cobra.Command {
	var remote bool

	cmd := &cobra.Command{
		Use:          "stop <sandbox-id | #number>",
		Short:        "Stop a sandbox's EC2 instance (preserves infrastructure for restart)",
		Long:         helpText("stop"),
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
				publisher := pub
				if publisher == nil {
					publisher = newRealRemotePublisher(cfg)
				}
				return publisher.PublishSandboxCommand(ctx, sandboxID, "stop")
			}
			return runStop(ctx, cfg, sandboxID)
		},
	}

	cmd.Flags().BoolVar(&remote, "remote", false, "Dispatch stop to Lambda via EventBridge")

	return cmd
}

func runStop(ctx context.Context, cfg *config.Config, sandboxID string) error {
	if err := CheckSandboxLock(ctx, cfg, sandboxID); err != nil {
		return err
	}

	awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	// Check metadata for docker substrate — route before EC2 API calls.
	// Docker sandboxes have no AWS-tagged EC2 resources, so EC2 lookup would fail.
	tableName := cfg.SandboxTableName
	if tableName == "" {
		tableName = "km-sandboxes"
	}
	dynamoClient := dynamodb.NewFromConfig(awsCfg)
	displayRef := sandboxID
	{
		meta, metaErr := awspkg.ReadSandboxMetadataDynamo(ctx, dynamoClient, tableName, sandboxID)
		if metaErr != nil {
			// S3 fallback on ResourceNotFoundException.
			var rnf *dynamodbtypes.ResourceNotFoundException
			if errors.As(metaErr, &rnf) {
				stateBucket := cfg.StateBucket
				if stateBucket == "" {
					stateBucket = os.Getenv("KM_STATE_BUCKET")
				}
				if stateBucket != "" {
					s3Client := s3.NewFromConfig(awsCfg)
					if m, s3Err := awspkg.ReadSandboxMetadata(ctx, s3Client, stateBucket, sandboxID); s3Err == nil {
						meta = m
						metaErr = nil
					}
				}
			}
		}
		if metaErr == nil && meta != nil {
			if meta.Alias != "" {
				displayRef = meta.Alias
			}
			if meta.Substrate == "docker" {
				homeDir, _ := os.UserHomeDir()
				composeFile := filepath.Join(homeDir, ".km", "sandboxes", sandboxID, "docker-compose.yml")
				if _, statErr := os.Stat(composeFile); os.IsNotExist(statErr) {
					return fmt.Errorf("docker sandbox %s is not running on this host", sandboxID)
				}
				if err := runDockerCompose(ctx, sandboxID, "stop"); err != nil {
					return err
				}
				fmt.Printf(ansiGreen+"Sandbox %s stopped."+ansiReset+" Use 'km resume %s' to restart.\n", displayRef, displayRef)
				return nil
			}
		}
		// If metadata not found or substrate is not docker, proceed with EC2 path.
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
			fmt.Printf("Stopping instance "+ansiYellow+"%s"+ansiReset+"...\n", instanceID)
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

	// Update metadata status to "stopped" and clear ttl_expiry so DynamoDB's native TTL
	// doesn't auto-delete the record while the sandbox is stopped.
	if statusErr := awspkg.UpdateSandboxStatusAndClearTTL(ctx, dynamoClient, tableName, sandboxID, "stopped"); statusErr != nil {
		fmt.Printf(ansiYellow+"  [warn] could not update metadata: %v"+ansiReset+"\n", statusErr)
	}

	fmt.Printf(ansiGreen+"Sandbox %s stopped."+ansiReset+" Use 'km resume %s' to restart.\n", displayRef, displayRef)
	return nil
}
