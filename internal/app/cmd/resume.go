package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/compiler"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// NewResumeCmd creates the "km resume" subcommand.
func NewResumeCmd(cfg *config.Config) *cobra.Command {
	return NewResumeCmdWithPublisher(cfg, nil)
}

// NewResumeCmdWithPublisher builds the resume command with an optional injected
// RemoteCommandPublisher. Pass nil to use the real AWS-backed publisher.
func NewResumeCmdWithPublisher(cfg *config.Config, pub RemoteCommandPublisher) *cobra.Command {
	var remote bool

	cmd := &cobra.Command{
		Use:          "resume <sandbox-id | #number>",
		Aliases:      []string{"start"},
		Short:        "Resume a paused or stopped sandbox",
		Long:         helpText("resume"),
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
				return publisher.PublishSandboxCommand(ctx, sandboxID, "resume")
			}
			return runResume(ctx, cfg, sandboxID)
		},
	}

	cmd.Flags().BoolVar(&remote, "remote", false, "Dispatch resume to Lambda via EventBridge")

	return cmd
}

func runResume(ctx context.Context, cfg *config.Config, sandboxID string) error {
	if cfg.StateBucket == "" {
		return fmt.Errorf("state bucket not configured")
	}

	awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	tableName := cfg.SandboxTableName
	if tableName == "" {
		tableName = "km-sandboxes"
	}
	budgetTable := cfg.BudgetTableName
	if budgetTable == "" {
		budgetTable = "km-budgets"
	}
	dynamoClient := dynamodb.NewFromConfig(awsCfg)

	ec2Client := ec2.NewFromConfig(awsCfg)

	descOut, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:km:sandbox-id"), Values: []string{sandboxID}},
			{Name: aws.String("instance-state-name"), Values: []string{"stopped"}},
		},
	})
	if err != nil {
		return fmt.Errorf("describe instances: %w", err)
	}

	resumed := 0
	for _, res := range descOut.Reservations {
		for _, inst := range res.Instances {
			instanceID := aws.ToString(inst.InstanceId)
			fmt.Printf("Resuming instance "+ansiYellow+"%s"+ansiReset+"...\n", instanceID)
			if _, err := ec2Client.StartInstances(ctx, &ec2.StartInstancesInput{
				InstanceIds: []string{instanceID},
			}); err != nil {
				return fmt.Errorf("resume instance %s: %w", instanceID, err)
			}
			resumed++
		}
	}

	if resumed == 0 {
		return fmt.Errorf("no stopped instances found for sandbox %s", sandboxID)
	}

	// Close the open pause interval in the budget table so paused time stops accruing.
	// Non-fatal: a DynamoDB error only logs a warning and lifecycle continues.
	if err := awspkg.RecordResumeClose(ctx, dynamoClient, budgetTable, sandboxID, time.Now().UTC()); err != nil {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).Msg("failed to record resume close in budget table (non-fatal)")
	}

	// Update metadata status back to "running" via DynamoDB (with S3 fallback on ResourceNotFoundException).
	if statusErr := awspkg.UpdateSandboxStatusDynamo(ctx, dynamoClient, tableName, sandboxID, "running"); statusErr != nil {
		var rnf *dynamodbtypes.ResourceNotFoundException
		if errors.As(statusErr, &rnf) && cfg.StateBucket != "" {
			// Table doesn't exist — fall back to S3 read-modify-write.
			s3Client := s3.NewFromConfig(awsCfg)
			if meta, readErr := awspkg.ReadSandboxMetadata(ctx, s3Client, cfg.StateBucket, sandboxID); readErr == nil {
				meta.Status = "running"
				metaJSON, _ := json.Marshal(meta)
				_, _ = s3Client.PutObject(ctx, &s3.PutObjectInput{
					Bucket:      aws.String(cfg.StateBucket),
					Key:         aws.String("tf-km/sandboxes/" + sandboxID + "/metadata.json"),
					Body:        bytes.NewReader(metaJSON),
					ContentType: aws.String("application/json"),
				})
			}
		} else {
			fmt.Printf(ansiYellow+"  [warn] could not update metadata: %v"+ansiReset+"\n", statusErr)
		}
	}

	// Recreate TTL schedule from the profile's TTL duration, counting from now.
	if cfg.ArtifactsBucket != "" {
		s3Client := s3.NewFromConfig(awsCfg)
		profileKey := fmt.Sprintf("artifacts/%s/.km-profile.yaml", sandboxID)
		obj, getErr := s3Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(cfg.ArtifactsBucket),
			Key:    aws.String(profileKey),
		})
		if getErr == nil {
			defer obj.Body.Close()
			profileData, _ := io.ReadAll(obj.Body)
			p, parseErr := profile.Parse(profileData)
			if parseErr == nil && p != nil && p.Spec.Lifecycle.TTL != "" && p.Spec.Lifecycle.TTL != "0" {
				ttlDuration, durErr := time.ParseDuration(p.Spec.Lifecycle.TTL)
				if durErr == nil {
					newExpiry := time.Now().Add(ttlDuration)
					lambdaClient := lambda.NewFromConfig(awsCfg)
					fnOut, fnErr := lambdaClient.GetFunction(ctx, &lambda.GetFunctionInput{
						FunctionName: aws.String("km-ttl-handler"),
					})
					iamClient := iam.NewFromConfig(awsCfg)
					roleOut, roleErr := iamClient.GetRole(ctx, &iam.GetRoleInput{
						RoleName: aws.String("km-ttl-scheduler"),
					})
					if fnErr == nil && roleErr == nil {
						schedulerClient := scheduler.NewFromConfig(awsCfg)
						// Delete any existing schedule first (may linger from previous TTL cycle).
						awspkg.DeleteTTLSchedule(ctx, schedulerClient, sandboxID)
						schedInput := compiler.BuildTTLScheduleInput(sandboxID, newExpiry,
							aws.ToString(fnOut.Configuration.FunctionArn),
							aws.ToString(roleOut.Role.Arn))
						if schedErr := awspkg.CreateTTLSchedule(ctx, schedulerClient, schedInput); schedErr == nil {
							fmt.Printf("  TTL schedule recreated: expires in %s\n", p.Spec.Lifecycle.TTL)
							// Update TTL expiry in DynamoDB.
							meta, readErr := awspkg.ReadSandboxMetadataDynamo(ctx, dynamoClient, tableName, sandboxID)
							if readErr == nil {
								meta.TTLExpiry = &newExpiry
								meta.ExpiresAt = &newExpiry
								awspkg.WriteSandboxMetadataDynamo(ctx, dynamoClient, tableName, meta)
							}
						} else {
							fmt.Printf(ansiYellow+"  [warn] could not recreate TTL schedule: %v"+ansiReset+"\n", schedErr)
						}
					}
				}
			}
		}
	}

	fmt.Printf(ansiGreen+"Sandbox %s resumed."+ansiReset+"\n", sandboxID)
	return nil
}
