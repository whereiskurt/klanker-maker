package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// NewPauseCmd creates the "km pause" subcommand.
func NewPauseCmd(cfg *config.Config) *cobra.Command {
	return NewPauseCmdWithPublisher(cfg, nil)
}

// NewPauseCmdWithPublisher builds the pause command with an optional injected
// RemoteCommandPublisher. Pass nil to use the real AWS-backed publisher.
// Used in tests to inject a mock publisher for --remote path testing.
func NewPauseCmdWithPublisher(cfg *config.Config, pub RemoteCommandPublisher) *cobra.Command {
	var remote bool

	cmd := &cobra.Command{
		Use:          "pause <sandbox-id | #number>",
		Short:        "Pause a sandbox's EC2 instance (hibernate, preserving RAM state)",
		Long:         helpText("pause"),
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
				return publisher.PublishSandboxCommand(ctx, sandboxID, "pause")
			}
			return runPause(ctx, cfg, sandboxID)
		},
	}

	cmd.Flags().BoolVar(&remote, "remote", false, "Dispatch pause to Lambda via EventBridge")

	return cmd
}

func runPause(ctx context.Context, cfg *config.Config, sandboxID string) error {
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
					s3ClientEarly := s3.NewFromConfig(awsCfg)
					if m, s3Err := awspkg.ReadSandboxMetadata(ctx, s3ClientEarly, stateBucket, sandboxID); s3Err == nil {
						meta = m
						metaErr = nil
					}
				}
			}
		}
		if metaErr == nil && meta != nil && meta.Substrate == "docker" {
			homeDir, _ := os.UserHomeDir()
			composeFile := filepath.Join(homeDir, ".km", "sandboxes", sandboxID, "docker-compose.yml")
			if _, statErr := os.Stat(composeFile); os.IsNotExist(statErr) {
				return fmt.Errorf("docker sandbox %s is not running on this host", sandboxID)
			}
			if err := runDockerCompose(ctx, sandboxID, "pause"); err != nil {
				return err
			}
			fmt.Printf(ansiGreen+"Sandbox %s paused."+ansiReset+" Use 'km resume %s' to unpause.\n", sandboxID, sandboxID)
			return nil
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

	paused := 0
	for _, res := range descOut.Reservations {
		for _, inst := range res.Instances {
			instanceID := aws.ToString(inst.InstanceId)

			// Spot instances cannot be stopped or hibernated — fail fast with clear message
			if inst.InstanceLifecycle == ec2types.InstanceLifecycleTypeSpot {
				return fmt.Errorf("cannot pause spot instance %s — spot instances cannot be stopped.\n"+
					"  Create with --on-demand to enable pause/resume:\n"+
					"  km create <profile.yaml> --on-demand", instanceID)
			}

			fmt.Printf("Pausing instance "+ansiYellow+"%s"+ansiReset+"...\n", instanceID)
			_, err := ec2Client.StopInstances(ctx, &ec2.StopInstancesInput{
				InstanceIds: []string{instanceID},
				Hibernate:   aws.Bool(true),
			})
			if err != nil && strings.Contains(err.Error(), "UnsupportedHibernationConfiguration") {
				// Instance wasn't launched with hibernation — fall back to normal stop
				fmt.Printf(ansiYellow+"  [info] hibernate not available, stopping normally"+ansiReset+"\n")
				_, err = ec2Client.StopInstances(ctx, &ec2.StopInstancesInput{
					InstanceIds: []string{instanceID},
				})
			}
			if err != nil {
				return fmt.Errorf("pause instance %s: %w", instanceID, err)
			}
			paused++
		}
	}

	if paused == 0 {
		return fmt.Errorf("no running instances found for sandbox %s", sandboxID)
	}

	// Update metadata status to "paused" via DynamoDB (S3 fallback handled silently on error).
	if statusErr := awspkg.UpdateSandboxStatusDynamo(ctx, dynamoClient, tableName, sandboxID, "paused"); statusErr != nil {
		fmt.Printf(ansiYellow+"  [warn] could not update metadata: %v"+ansiReset+"\n", statusErr)
	}

	fmt.Printf(ansiGreen+"Sandbox %s paused."+ansiReset+" Use 'km resume %s' to restart.\n", sandboxID, sandboxID)
	return nil
}
