package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// NewUnlockCmd creates the "km unlock" subcommand.
func NewUnlockCmd(cfg *config.Config) *cobra.Command {
	return NewUnlockCmdWithPublisher(cfg, nil)
}

// NewUnlockCmdWithPublisher builds the unlock command with an optional injected
// RemoteCommandPublisher. Pass nil to use the real AWS-backed publisher.
// Used in tests to inject a mock publisher for --remote path testing.
func NewUnlockCmdWithPublisher(cfg *config.Config, pub RemoteCommandPublisher) *cobra.Command {
	var remote bool
	var yes bool

	cmd := &cobra.Command{
		Use:          "unlock <sandbox-id | #number>",
		Short:        "Unlock a sandbox to allow destroy/stop/pause",
		Long:         helpText("unlock"),
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
				return publisher.PublishSandboxCommand(ctx, sandboxID, "unlock")
			}
			return runUnlock(ctx, cfg, sandboxID, yes)
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&remote, "remote", false, "Dispatch unlock to Lambda via EventBridge")

	return cmd
}

func runUnlock(ctx context.Context, cfg *config.Config, sandboxID string, yes bool) error {
	awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	dynamoClient := dynamodb.NewFromConfig(awsCfg)
	tableName := cfg.SandboxTableName
	if tableName == "" {
		tableName = "km-sandboxes"
	}

	// Primary path: try DynamoDB atomic unlock.
	unlockErr := awspkg.UnlockSandboxDynamo(ctx, dynamoClient, tableName, sandboxID)
	if unlockErr != nil {
		// S3 fallback: if DynamoDB table doesn't exist, fall back to S3 read-modify-write.
		var rnf *dynamodbtypes.ResourceNotFoundException
		if errors.As(unlockErr, &rnf) {
			return runUnlockS3Fallback(ctx, cfg, sandboxID, yes)
		}
		// "not locked" error from UnlockSandboxDynamo — surface to user.
		return unlockErr
	}

	fmt.Printf(ansiGreen+"Sandbox %s unlocked."+ansiReset+"\n", sandboxID)
	return nil
}

// runUnlockS3Fallback performs the legacy S3 read-modify-write unlock for environments
// where the km-sandboxes DynamoDB table has not been provisioned yet.
func runUnlockS3Fallback(ctx context.Context, cfg *config.Config, sandboxID string, yes bool) error {
	if cfg.StateBucket == "" {
		return fmt.Errorf("state bucket not configured — unlock requires S3 metadata (DynamoDB table not found)")
	}

	awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	s3Client := s3.NewFromConfig(awsCfg)

	meta, err := awspkg.ReadSandboxMetadata(ctx, s3Client, cfg.StateBucket, sandboxID)
	if err != nil {
		return fmt.Errorf("read sandbox metadata: %w", err)
	}

	if !meta.Locked {
		fmt.Printf("Sandbox %s is not locked.\n", sandboxID)
		return nil
	}

	if !yes {
		fmt.Printf("Unlock sandbox %s? This will allow destroy/stop/pause. [y/N] ", sandboxID)
		var answer string
		fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" && answer != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	meta.Locked = false
	meta.LockedAt = nil

	metaJSON, _ := json.Marshal(meta)
	_, putErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(cfg.StateBucket),
		Key:         aws.String("tf-km/sandboxes/" + sandboxID + "/metadata.json"),
		Body:        bytes.NewReader(metaJSON),
		ContentType: aws.String("application/json"),
	})
	if putErr != nil {
		return fmt.Errorf("write sandbox metadata: %w", putErr)
	}

	fmt.Printf(ansiGreen+"Sandbox %s unlocked."+ansiReset+"\n", sandboxID)
	return nil
}
