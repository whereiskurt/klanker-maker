// Package main implements the km TTL handler Lambda.
//
// When an EventBridge scheduler fires a TTL expiry event for a sandbox,
// this handler:
//  1. Validates the sandbox_id in the event payload.
//  2. Downloads the sandbox profile from S3.
//  3. Uploads sandbox artifacts to S3 (the primary gap closure for OBSV-04/OBSV-05).
//  4. Sends a "ttl-expired" lifecycle notification to the operator (if configured).
//  5. Deletes the TTL schedule (self-cleanup).
//
// The actual infrastructure teardown (terragrunt destroy) is delegated to a
// separate cleanup job — the Lambda purpose is artifact preservation and notification.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/rs/zerolog/log"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	profilepkg "github.com/whereiskurt/klankrmkr/pkg/profile"
)

// TTLEvent is the EventBridge scheduler payload delivered to this Lambda.
type TTLEvent struct {
	SandboxID string `json:"sandbox_id"`
}

// S3GetAPI is the narrow S3 interface needed to download the sandbox profile.
type S3GetAPI interface {
	GetObject(ctx context.Context, input *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// S3GetPutAPI combines read and write S3 operations needed by the handler.
type S3GetPutAPI interface {
	S3GetAPI
	awspkg.S3PutAPI
}

// SESV2API re-exports the narrow SES interface from pkg/aws for use in this package.
type SESV2API = awspkg.SESV2API

// SchedulerAPI re-exports the narrow Scheduler interface from pkg/aws.
type SchedulerAPI = awspkg.SchedulerAPI

// TTLHandler holds injected dependencies for testability.
type TTLHandler struct {
	S3Client      S3GetPutAPI
	SESClient     SESV2API
	Scheduler     SchedulerAPI
	Bucket        string
	OperatorEmail string
	Domain        string
}

// HandleTTLEvent is the Lambda handler method. It is called by lambda.Start in main().
func (h *TTLHandler) HandleTTLEvent(ctx context.Context, event TTLEvent) error {
	// Step 1: Validate sandbox ID.
	if event.SandboxID == "" {
		return fmt.Errorf("ttl-handler: sandbox_id is required in event payload")
	}
	sandboxID := event.SandboxID

	log.Info().Str("sandbox_id", sandboxID).Msg("TTL expiry event received")

	// Step 2: Download sandbox profile from S3 (best-effort — missing profile skips artifact upload).
	var sandboxProfile *profilepkg.SandboxProfile
	profileBytes, profileErr := downloadProfileFromS3(ctx, h.S3Client, h.Bucket, sandboxID)
	if profileErr != nil {
		log.Warn().Err(profileErr).Str("sandbox_id", sandboxID).
			Msg("could not load sandbox profile from S3; skipping artifact upload")
	} else {
		sandboxProfile, _ = profilepkg.Parse(profileBytes)
	}

	// Step 3: Upload artifacts if the profile specifies artifact paths.
	if sandboxProfile != nil && sandboxProfile.Spec.Artifacts != nil && len(sandboxProfile.Spec.Artifacts.Paths) > 0 {
		arts := sandboxProfile.Spec.Artifacts
		uploaded, skipped, uploadErr := awspkg.UploadArtifacts(ctx, h.S3Client, h.Bucket, sandboxID, arts.Paths, arts.MaxSizeMB)
		if uploadErr != nil {
			log.Warn().Err(uploadErr).Str("sandbox_id", sandboxID).
				Msg("artifact upload error (best-effort); continuing")
		} else {
			log.Info().Str("sandbox_id", sandboxID).
				Int("uploaded", uploaded).
				Int("skipped", len(skipped)).
				Msg("artifact upload complete")
		}
	} else {
		log.Debug().Str("sandbox_id", sandboxID).Msg("no artifact paths configured; skipping upload")
	}

	// Step 4: Send "ttl-expired" lifecycle notification (if operator email is configured).
	if h.OperatorEmail != "" && h.SESClient != nil {
		if notifyErr := awspkg.SendLifecycleNotification(ctx, h.SESClient, h.OperatorEmail, sandboxID, "ttl-expired", h.Domain); notifyErr != nil {
			log.Warn().Err(notifyErr).Str("sandbox_id", sandboxID).
				Msg("failed to send ttl-expired notification (non-fatal)")
		}
	}

	// Step 5: Delete TTL schedule (self-cleanup, idempotent).
	if h.Scheduler != nil {
		if schedErr := awspkg.DeleteTTLSchedule(ctx, h.Scheduler, sandboxID); schedErr != nil {
			log.Warn().Err(schedErr).Str("sandbox_id", sandboxID).
				Msg("failed to delete TTL schedule (non-fatal)")
		}
	}

	log.Info().Str("sandbox_id", sandboxID).Msg("TTL handler completed")
	return nil
}

// downloadProfileFromS3 retrieves the sandbox profile YAML stored at
// artifacts/{sandboxID}/.km-profile.yaml in the given S3 bucket.
// This mirrors the same function in internal/app/cmd/destroy.go.
func downloadProfileFromS3(ctx context.Context, client S3GetAPI, bucket, sandboxID string) ([]byte, error) {
	key := "artifacts/" + sandboxID + "/.km-profile.yaml"
	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get profile from S3 s3://%s/%s: %w", bucket, key, err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// main constructs a TTLHandler with real AWS clients and registers it with the Lambda runtime.
func main() {
	ctx := context.Background()
	awsProfile := os.Getenv("KM_AWS_PROFILE") // empty in Lambda — uses execution role

	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load AWS config")
	}

	bucket := os.Getenv("KM_ARTIFACTS_BUCKET")
	if bucket == "" {
		bucket = "km-sandbox-artifacts-ea554771"
	}
	domain := os.Getenv("KM_EMAIL_DOMAIN")
	if domain == "" {
		domain = "sandboxes.klankermaker.ai"
	}

	// Real S3 client that satisfies both GetObject and PutObject.
	s3Client := s3.NewFromConfig(awsCfg)

	h := &TTLHandler{
		S3Client:      s3Client,
		SESClient:     sesv2.NewFromConfig(awsCfg),
		Scheduler:     scheduler.NewFromConfig(awsCfg),
		Bucket:        bucket,
		OperatorEmail: os.Getenv("KM_OPERATOR_EMAIL"),
		Domain:        domain,
	}

	lambda.Start(h.HandleTTLEvent)
}
