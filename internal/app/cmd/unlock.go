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
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
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
			return runUnlock(ctx, cmd, cfg, nil, sandboxID, yes)
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&remote, "remote", false, "Dispatch unlock to Lambda via EventBridge")

	return cmd
}

// NewUnlockCmdWithLatchDDB builds the unlock command with an optional injected
// LatchAwareDDB client (covers both safety-lock + freeze-latch clear). Pass nil
// to use the real AWS-backed client. Used in tests to inject a mock without
// touching real DynamoDB or real AWS config.
func NewUnlockCmdWithLatchDDB(cfg *config.Config, ddb LatchAwareDDB) *cobra.Command {
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
			return runUnlock(ctx, cmd, cfg, ddb, sandboxID, yes)
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	return cmd
}

// runUnlock is the shared unlock implementation for both NewUnlockCmdWithPublisher
// and NewUnlockCmdWithLatchDDB. When ddb is nil, a real DynamoDB client is loaded
// from the AWS config. The function clears both the safety lock AND the action-freeze
// latch atomically in sequence — both operations are idempotent.
func runUnlock(ctx context.Context, cobraCmd *cobra.Command, cfg *config.Config, ddb LatchAwareDDB, sandboxID string, yes bool) error {
	var dynamoClient awspkg.SandboxMetadataAPI

	if ddb != nil {
		dynamoClient = ddb
	} else {
		awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		dynamoClient = dynamodb.NewFromConfig(awsCfg)
	}

	tableName := cfg.GetSandboxTableName()

	// Primary path: try DynamoDB atomic safety-lock unlock.
	unlockErr := awspkg.UnlockSandboxDynamo(ctx, dynamoClient, tableName, sandboxID)
	if unlockErr != nil {
		// S3 fallback: if DynamoDB table doesn't exist, fall back to S3 read-modify-write.
		var rnf *dynamodbtypes.ResourceNotFoundException
		if errors.As(unlockErr, &rnf) {
			return runUnlockS3Fallback(ctx, cfg, sandboxID, yes)
		}
		// "not locked" error from UnlockSandboxDynamo — still attempt freeze clear below.
		// We intentionally don't return here: the sandbox might be frozen but not
		// safety-locked, in which case we should still clear the freeze latch.
		// Surface this as a non-fatal note only when the freeze clear also has nothing to do.
		_ = unlockErr
	}

	// Always attempt to clear the freeze latch — idempotent, harmless when not frozen.
	unfreezeErr := awspkg.UnfreezeSandboxDynamo(ctx, dynamoClient, tableName, sandboxID)
	if unfreezeErr != nil {
		var rnf *dynamodbtypes.ResourceNotFoundException
		if errors.As(unfreezeErr, &rnf) {
			// Table doesn't exist — safety-lock also failed via S3 fallback path above,
			// so we already returned. If we reach here something is inconsistent; surface it.
			return fmt.Errorf("unfreeze: DynamoDB table %q not found", tableName)
		}
		// For other errors (sandbox row missing), surface as warning but don't fail the
		// command — the safety lock may already have been cleared successfully.
		if unlockErr != nil {
			// Both operations failed — surface original unlock error.
			return unlockErr
		}
		// Safety lock clear succeeded but freeze clear had an issue (e.g. row gone).
		// Treat as success — the row is likely gone or never frozen.
	}

	out := cobraCmd.OutOrStdout()

	// Report what was cleared.
	lockCleared := unlockErr == nil
	freezeCleared := unfreezeErr == nil

	switch {
	case lockCleared && freezeCleared:
		fmt.Fprintf(out, ansiGreen+"Sandbox %s: cleared safety lock + action freeze."+ansiReset+"\n", sandboxID)
	case lockCleared:
		fmt.Fprintf(out, ansiGreen+"Sandbox %s: cleared safety lock."+ansiReset+" (no action freeze was set)\n", sandboxID)
	case freezeCleared:
		fmt.Fprintf(out, ansiGreen+"Sandbox %s: cleared action freeze."+ansiReset+" (no safety lock was set)\n", sandboxID)
	default:
		fmt.Fprintf(out, ansiGreen+"Sandbox %s: no locks to clear."+ansiReset+"\n", sandboxID)
	}

	fmt.Fprintf(out, "Actions resume immediately (no km resume needed).\n")
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
		Key:         aws.String("tf-" + cfg.GetResourcePrefix() + "/sandboxes/" + sandboxID + "/metadata.json"),
		Body:        bytes.NewReader(metaJSON),
		ContentType: aws.String("application/json"),
	})
	if putErr != nil {
		return fmt.Errorf("write sandbox metadata: %w", putErr)
	}

	fmt.Printf(ansiGreen+"Sandbox %s unlocked."+ansiReset+"\n", sandboxID)
	return nil
}
