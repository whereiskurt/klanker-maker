package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// NewLockCmd creates the "km lock" subcommand.
func NewLockCmd(cfg *config.Config) *cobra.Command {
	return NewLockCmdWithPublisher(cfg, nil)
}

// NewLockCmdWithPublisher builds the lock command with an optional injected
// RemoteCommandPublisher. Pass nil to use the real AWS-backed publisher.
// Used in tests to inject a mock publisher for --remote path testing.
func NewLockCmdWithPublisher(cfg *config.Config, pub RemoteCommandPublisher) *cobra.Command {
	var remote bool

	cmd := &cobra.Command{
		Use:          "lock <sandbox-id | #number>",
		Short:        "Lock a sandbox to prevent accidental destroy/stop/pause",
		Long:         helpText("lock"),
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
				return publisher.PublishSandboxCommand(ctx, sandboxID, "lock")
			}
			return runLock(ctx, cfg, sandboxID)
		},
	}

	cmd.Flags().BoolVar(&remote, "remote", false, "Dispatch lock to Lambda via EventBridge")

	return cmd
}

func runLock(ctx context.Context, cfg *config.Config, sandboxID string) error {
	if cfg.StateBucket == "" {
		return fmt.Errorf("state bucket not configured — lock requires S3 metadata")
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

	if meta.Locked {
		fmt.Printf("Sandbox %s is already locked.\n", sandboxID)
		return nil
	}

	meta.Locked = true
	now := time.Now()
	meta.LockedAt = &now

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

	fmt.Printf(ansiGreen+"Sandbox %s locked."+ansiReset+"\n", sandboxID)
	return nil
}

// CheckSandboxLock reads metadata from S3 and returns an error if the sandbox is locked.
// Fail-open: returns nil if StateBucket is empty, AWS config fails to load, or metadata is missing.
// For commands that REQUIRE the lock check (destroy, stop, pause), call this
// at the top of runXxx before any expensive AWS operations.
func CheckSandboxLock(ctx context.Context, cfg *config.Config, sandboxID string) error {
	if cfg.StateBucket == "" {
		return nil // no metadata store — can't check lock, fail-open
	}
	awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return nil // fail-open: if we can't load config, don't block the command
	}
	s3Client := s3.NewFromConfig(awsCfg)
	meta, err := awspkg.ReadSandboxMetadata(ctx, s3Client, cfg.StateBucket, sandboxID)
	if err != nil {
		return nil // fail-open: missing metadata shouldn't block destroy
	}
	if meta.Locked {
		return fmt.Errorf("sandbox %s is locked — run 'km unlock %s' first", sandboxID, sandboxID)
	}
	return nil
}
