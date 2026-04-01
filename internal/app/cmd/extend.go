package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	iampkg "github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/compiler"
)

// NewExtendCmd creates the "km extend" subcommand.
// Usage: km extend <sandbox-id | #number> <duration>
func NewExtendCmd(cfg *config.Config) *cobra.Command {
	return NewExtendCmdWithPublisher(cfg, nil)
}

// NewExtendCmdWithPublisher builds the extend command with an optional injected
// RemoteCommandPublisher. Pass nil to use the real AWS-backed publisher.
// Used in tests to inject a mock publisher for --remote path testing.
func NewExtendCmdWithPublisher(cfg *config.Config, pub RemoteCommandPublisher) *cobra.Command {
	var remote bool

	cmd := &cobra.Command{
		Use:          "extend <sandbox-id | #number> <duration>",
		Short:        "Extend a sandbox's TTL by the given duration",
		Long:         helpText("extend"),
		Args:         cobra.ExactArgs(2),
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
				return publisher.PublishSandboxCommand(ctx, sandboxID, "extend", "duration", args[1])
			}
			duration, err := time.ParseDuration(args[1])
			if err != nil {
				return fmt.Errorf("invalid duration %q: %w (examples: 1h, 30m, 2h30m)", args[1], err)
			}
			return runExtend(ctx, cfg, sandboxID, duration)
		},
	}

	cmd.Flags().BoolVar(&remote, "remote", false, "Dispatch extend to Lambda via EventBridge")

	return cmd
}

func runExtend(ctx context.Context, cfg *config.Config, sandboxID string, addDuration time.Duration) error {
	awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	tableName := cfg.SandboxTableName
	if tableName == "" {
		tableName = "km-sandboxes"
	}
	dynamoClient := dynamodb.NewFromConfig(awsCfg)
	schedulerClient := scheduler.NewFromConfig(awsCfg)

	// Step 1: Read current metadata — DynamoDB primary, S3 fallback.
	meta, err := awspkg.ReadSandboxMetadataDynamo(ctx, dynamoClient, tableName, sandboxID)
	if err != nil {
		var rnf *dynamodbtypes.ResourceNotFoundException
		if errors.As(err, &rnf) && cfg.StateBucket != "" {
			s3Client := s3.NewFromConfig(awsCfg)
			meta, err = awspkg.ReadSandboxMetadata(ctx, s3Client, cfg.StateBucket, sandboxID)
		}
		if err != nil {
			return fmt.Errorf("read sandbox metadata: %w", err)
		}
	}

	// Step 2: Calculate new TTL expiry
	var newExpiry time.Time
	if meta.TTLExpiry != nil && meta.TTLExpiry.After(time.Now()) {
		// TTL hasn't expired yet — extend from current expiry
		newExpiry = meta.TTLExpiry.Add(addDuration)
	} else {
		// TTL already expired or not set — extend from now
		newExpiry = time.Now().Add(addDuration)
	}

	// Step 2b: Enforce MaxLifetime cap if set
	if err := CheckMaxLifetime(meta, newExpiry); err != nil {
		return err
	}

	// Step 3: Delete old schedule and create new one
	if delErr := awspkg.DeleteTTLSchedule(ctx, schedulerClient, sandboxID); delErr != nil {
		fmt.Printf(ansiYellow+"  [warn] could not delete old TTL schedule: %v"+ansiReset+"\n", delErr)
	}

	// Auto-discover TTL Lambda ARN
	lambdaClient := lambda.NewFromConfig(awsCfg)
	fnOut, fnErr := lambdaClient.GetFunction(ctx, &lambda.GetFunctionInput{
		FunctionName: aws.String("km-ttl-handler"),
	})
	if fnErr != nil {
		return fmt.Errorf("TTL handler Lambda not found — cannot create schedule: %w", fnErr)
	}
	ttlLambdaARN := aws.ToString(fnOut.Configuration.FunctionArn)

	// Auto-discover scheduler role ARN
	iamClient := iampkg.NewFromConfig(awsCfg)
	roleOut, roleErr := iamClient.GetRole(ctx, &iampkg.GetRoleInput{
		RoleName: aws.String("km-ttl-scheduler"),
	})
	if roleErr != nil {
		return fmt.Errorf("TTL scheduler role not found — run 'km init': %w", roleErr)
	}
	schedulerRoleARN := aws.ToString(roleOut.Role.Arn)

	schedInput := compiler.BuildTTLScheduleInput(sandboxID, newExpiry, ttlLambdaARN, schedulerRoleARN)
	if err := awspkg.CreateTTLSchedule(ctx, schedulerClient, schedInput); err != nil {
		return fmt.Errorf("create TTL schedule: %w", err)
	}

	// Step 4: Update metadata with new expiry via DynamoDB (S3 fallback on ResourceNotFoundException).
	meta.TTLExpiry = &newExpiry
	if writeErr := awspkg.WriteSandboxMetadataDynamo(ctx, dynamoClient, tableName, meta); writeErr != nil {
		var rnf *dynamodbtypes.ResourceNotFoundException
		if errors.As(writeErr, &rnf) && cfg.StateBucket != "" {
			// Fall back to S3
			s3Client := s3.NewFromConfig(awsCfg)
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
		} else {
			fmt.Printf(ansiYellow+"  [warn] could not update metadata: %v"+ansiReset+"\n", writeErr)
		}
	}

	remaining := time.Until(newExpiry).Round(time.Second)
	fmt.Printf(ansiGreen+"TTL extended for %s"+ansiReset+": new expiry in %s (%s)\n",
		sandboxID, remaining, newExpiry.Local().Format("3:04:05 PM MST"))
	return nil
}

// CheckMaxLifetime enforces the MaxLifetime cap from sandbox metadata.
// If meta.MaxLifetime is empty, no cap is enforced (backward compatible).
// If the proposed newExpiry exceeds CreatedAt + MaxLifetime, an error is returned
// with a clear message including the cap duration and max expiry time.
//
// This function is exported so it can be unit tested without AWS dependencies.
func CheckMaxLifetime(meta *awspkg.SandboxMetadata, newExpiry time.Time) error {
	if meta.MaxLifetime == "" {
		return nil
	}
	maxLifetimeDuration, err := time.ParseDuration(meta.MaxLifetime)
	if err != nil {
		// Malformed MaxLifetime — skip enforcement rather than blocking the user.
		return fmt.Errorf("invalid maxLifetime %q in sandbox metadata: %w", meta.MaxLifetime, err)
	}
	maxExpiry := meta.CreatedAt.Add(maxLifetimeDuration)
	if newExpiry.After(maxExpiry) {
		return fmt.Errorf(
			"extend would exceed max lifetime (%s); sandbox was created at %s, max expiry is %s",
			meta.MaxLifetime,
			meta.CreatedAt.UTC().Format("2006-01-02 15:04:05 UTC"),
			maxExpiry.UTC().Format("2006-01-02 15:04:05 UTC"),
		)
	}
	return nil
}
