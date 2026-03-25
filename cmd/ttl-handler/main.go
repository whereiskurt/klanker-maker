// Package main implements the km TTL handler Lambda.
//
// When an EventBridge scheduler fires a TTL expiry event for a sandbox,
// this handler:
//  1. Validates the sandbox_id in the event payload.
//  2. Downloads the sandbox profile from S3.
//  3. Uploads sandbox artifacts to S3 (the primary gap closure for OBSV-04/OBSV-05).
//  4. Sends a "ttl-expired" lifecycle notification to the operator (if configured).
//  5. Deletes the TTL schedule (self-cleanup).
//  6. Destroys sandbox resources via AWS SDK (PROV-05/PROV-06).
//
// The teardown uses AWS SDK calls (not terragrunt subprocess) because the Lambda
// runtime (provided.al2023) does NOT include the terragrunt binary.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/aws/aws-lambda-go/lambda"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/rs/zerolog/log"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	profilepkg "github.com/whereiskurt/klankrmkr/pkg/profile"
)

// TTLEvent is the EventBridge scheduler or EventBridge Events payload delivered to this Lambda.
type TTLEvent struct {
	SandboxID string `json:"sandbox_id"`
	// EventType distinguishes TTL schedule events ("ttl", default) from idle events ("idle").
	// Empty defaults to "ttl" for backward compatibility.
	EventType string `json:"event_type,omitempty"`
}

// S3GetAPI is the narrow S3 interface needed to download the sandbox profile.
type S3GetAPI interface {
	GetObject(ctx context.Context, input *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// S3GetPutAPI combines read, write, and delete S3 operations needed by the handler.
type S3GetPutAPI interface {
	S3GetAPI
	awspkg.S3PutAPI
	awspkg.S3DeleteAPI
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
	StateBucket   string // S3 bucket holding terraform state
	StatePrefix   string // state key prefix (e.g. "tf-km")
	Region        string // AWS region (e.g. "us-east-1")
	RegionLabel   string // short region label (e.g. "use1")
	OperatorEmail string
	Domain        string
	// TeardownFunc destroys the sandbox resources after TTL expiry or idle detection.
	// If nil, teardown is skipped (backward compatible with existing tests).
	TeardownFunc func(ctx context.Context, sandboxID string) error
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

	// Step 6: Destroy sandbox resources (PROV-05/PROV-06).
	// Uses AWS SDK calls — no terragrunt subprocess in the Lambda runtime.
	if h.TeardownFunc != nil {
		if err := h.TeardownFunc(ctx, sandboxID); err != nil {
			log.Error().Err(err).Str("sandbox_id", sandboxID).Msg("sandbox teardown failed")
			return fmt.Errorf("teardown sandbox %s: %w", sandboxID, err)
		}
		log.Info().Str("sandbox_id", sandboxID).Msg("sandbox resources destroyed")
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

// terraformDestroy runs `terraform destroy -auto-approve` against the sandbox's
// S3-backed state. The terraform binary is bundled alongside bootstrap in the Lambda zip.
func terraformDestroy(ctx context.Context, h *TTLHandler, sandboxID string) error {
	// Lambda writable directory — clean up any leftovers from previous failed runs
	workDir := filepath.Join("/tmp", "tf-"+sandboxID)
	os.RemoveAll(workDir)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	region := h.Region
	if region == "" {
		region = "us-east-1"
	}
	regionLabel := h.RegionLabel
	if regionLabel == "" {
		regionLabel = "use1"
	}
	statePrefix := h.StatePrefix
	if statePrefix == "" {
		statePrefix = "tf-km"
	}

	// Determine the terraform module source from the state file.
	// For now, assume ec2spot — the most common substrate.
	// TODO: Read substrate from metadata.json to handle ECS sandboxes.
	moduleSource := "ec2spot"

	// State key: tf-km/use1/sandboxes/<sandbox-id>/terraform.tfstate
	stateKey := fmt.Sprintf("%s/%s/sandboxes/%s/terraform.tfstate", statePrefix, regionLabel, sandboxID)

	// Write a minimal main.tf that references the same module and backend.
	// terraform destroy only needs the module source and state — it reads
	// resource addresses from state and destroys them.
	mainTF := fmt.Sprintf(`
terraform {
  required_version = ">= 1.6.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }
  backend "s3" {
    bucket         = %q
    key            = %q
    region         = %q
    encrypt        = true
    dynamodb_table = %q
  }
}

provider "aws" {
  region = %q
}

module "sandbox" {
  source       = "./module"
  km_label     = "km"
  region_label = %q
  region_full  = %q
  sandbox_id   = %q
  vpc_id       = "destroy-placeholder"
  public_subnets     = []
  availability_zones = []
  ec2spots           = []
}
`, h.StateBucket, stateKey, region,
		statePrefix+"-locks-"+regionLabel, region,
		regionLabel, region, sandboxID)

	if err := os.WriteFile(filepath.Join(workDir, "main.tf"), []byte(mainTF), 0o644); err != nil {
		return fmt.Errorf("write main.tf: %w", err)
	}

	// Download the module source from the Lambda's bundled modules directory.
	// The Lambda zip includes infra/modules/ alongside the bootstrap binary.
	// In Lambda, the binary runs from /var/task/ — modules are at /var/task/infra/modules/
	bundledModule := filepath.Join("/var/task", "infra", "modules", moduleSource, "v1.0.0")
	if _, err := os.Stat(bundledModule); os.IsNotExist(err) {
		// Fallback: module not bundled, try direct state-only destroy
		log.Warn().Str("module", bundledModule).Msg("module not bundled in Lambda; attempting state-only destroy")
		return terraformDestroyStateOnly(ctx, workDir)
	}

	// Symlink the module so terraform can read it
	if err := os.Symlink(bundledModule, filepath.Join(workDir, "module")); err != nil {
		return fmt.Errorf("symlink module: %w", err)
	}

	// terraform init — use /tmp for all data to stay within ephemeral storage
	log.Info().Str("sandbox_id", sandboxID).Msg("running terraform init")
	tfEnv := append(os.Environ(), "TF_DATA_DIR="+filepath.Join(workDir, ".terraform"))
	initCmd := exec.CommandContext(ctx, "/var/task/terraform", "init", "-no-color", "-input=false")
	initCmd.Dir = workDir
	initCmd.Env = tfEnv
	if out, err := initCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("terraform init: %s: %w", string(out), err)
	}

	// terraform destroy
	log.Info().Str("sandbox_id", sandboxID).Msg("running terraform destroy")
	destroyCmd := exec.CommandContext(ctx, "/var/task/terraform", "destroy", "-auto-approve", "-no-color", "-input=false")
	destroyCmd.Dir = workDir
	destroyCmd.Env = tfEnv
	out, err := destroyCmd.CombinedOutput()
	log.Info().Str("sandbox_id", sandboxID).Str("output", string(out)).Msg("terraform destroy output")
	if err != nil {
		return fmt.Errorf("terraform destroy: %s: %w", string(out), err)
	}

	// Clean up S3 metadata so km list no longer shows this sandbox
	if h.StateBucket != "" {
		if delErr := awspkg.DeleteSandboxMetadata(ctx, h.S3Client, h.StateBucket, sandboxID); delErr != nil {
			log.Warn().Err(delErr).Str("sandbox_id", sandboxID).Msg("failed to delete metadata (non-fatal)")
		}
	}

	return nil
}

// terraformDestroyStateOnly runs terraform destroy without module source — relies on
// state containing enough info for terraform to identify and destroy resources.
func terraformDestroyStateOnly(ctx context.Context, workDir string) error {
	initCmd := exec.CommandContext(ctx, "/var/task/terraform", "init", "-no-color", "-input=false")
	initCmd.Dir = workDir
	if out, err := initCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("terraform init (state-only): %s: %w", string(out), err)
	}

	destroyCmd := exec.CommandContext(ctx, "/var/task/terraform", "destroy", "-auto-approve", "-no-color", "-input=false")
	destroyCmd.Dir = workDir
	out, err := destroyCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("terraform destroy (state-only): %s: %w", string(out), err)
	}
	log.Info().Str("output", string(out)).Msg("terraform destroy (state-only) output")
	return nil
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
		bucket = "km-artifacts" // fallback; should be set via Lambda env var
	}
	domain := os.Getenv("KM_EMAIL_DOMAIN")
	if domain == "" {
		domain = "sandboxes.klankermaker.ai"
	}

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	s3Client := s3.NewFromConfig(awsCfg)

	h := &TTLHandler{
		S3Client:      s3Client,
		SESClient:     sesv2.NewFromConfig(awsCfg),
		Scheduler:     scheduler.NewFromConfig(awsCfg),
		Bucket:        bucket,
		StateBucket:   os.Getenv("KM_STATE_BUCKET"),
		StatePrefix:   os.Getenv("KM_STATE_PREFIX"),
		Region:        region,
		RegionLabel:   os.Getenv("KM_REGION_LABEL"),
		OperatorEmail: os.Getenv("KM_OPERATOR_EMAIL"),
		Domain:        domain,
		TeardownFunc: nil, // set below
	}

	// Use terraform-based teardown if terraform binary is bundled.
	if _, err := os.Stat("/var/task/terraform"); err == nil {
		h.TeardownFunc = func(ctx context.Context, sandboxID string) error {
			return terraformDestroy(ctx, h, sandboxID)
		}
		log.Info().Msg("terraform binary found — using terraform destroy for teardown")
	} else {
		log.Warn().Msg("terraform binary not found — teardown will be skipped")
	}

	lambda.Start(h.HandleTTLEvent)
}
