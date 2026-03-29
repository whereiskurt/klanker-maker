package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
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
	if cfg.StateBucket == "" {
		return fmt.Errorf("state bucket not configured")
	}

	if err := CheckSandboxLock(ctx, cfg, sandboxID); err != nil {
		return err
	}

	awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	ec2Client := ec2.NewFromConfig(awsCfg)
	s3Client := s3.NewFromConfig(awsCfg)

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
			fmt.Printf("Pausing instance "+ansiYellow+"%s"+ansiReset+"...\n", instanceID)
			if _, err := ec2Client.StopInstances(ctx, &ec2.StopInstancesInput{
				InstanceIds: []string{instanceID},
				Hibernate:   aws.Bool(true),
			}); err != nil {
				return fmt.Errorf("pause instance %s: %w", instanceID, err)
			}
			paused++
		}
	}

	if paused == 0 {
		return fmt.Errorf("no running instances found for sandbox %s", sandboxID)
	}

	// Update metadata status to "paused"
	meta, err := awspkg.ReadSandboxMetadata(ctx, s3Client, cfg.StateBucket, sandboxID)
	if err != nil {
		return fmt.Errorf("read sandbox metadata: %w", err)
	}
	meta.Status = "paused"
	metaJSON, _ := json.Marshal(meta)
	_, putErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(cfg.StateBucket),
		Key:         aws.String("tf-km/sandboxes/" + sandboxID + "/metadata.json"),
		Body:        bytes.NewReader(metaJSON),
		ContentType: aws.String("application/json"),
	})
	if putErr != nil {
		fmt.Printf(ansiYellow+"  [warn] could not update metadata: %v"+ansiReset+"\n", putErr)
	}

	fmt.Printf(ansiGreen+"Sandbox %s paused."+ansiReset+"\n", sandboxID)
	return nil
}
