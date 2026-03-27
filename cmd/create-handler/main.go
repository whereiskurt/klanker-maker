// Package main implements the km create handler Lambda.
//
// When an EventBridge SandboxCreate event arrives (from km create --remote),
// this handler:
//  1. Downloads the sandbox profile from S3 at {artifact_prefix}/.km-profile.yaml
//  2. Writes the profile to /tmp/{sandbox-id}.yaml
//  3. Runs km create as a subprocess (km binary bundled at /var/task/km)
//  4. On subprocess failure, sends a "create-failed" lifecycle notification via SES
//  5. On success, returns nil — km create already sent the "created" notification
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	lambdaruntime "github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/rs/zerolog/log"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// errExecFailed is a sentinel error used by RunCommand in tests to simulate subprocess failure.
var errExecFailed = errors.New("exec: subprocess failed")

// CreateEvent is the EventBridge detail payload delivered to this Lambda.
// It matches the SandboxCreateDetail published by km create --remote.
type CreateEvent struct {
	SandboxID      string `json:"sandbox_id"`
	ArtifactBucket string `json:"artifact_bucket"`
	ArtifactPrefix string `json:"artifact_prefix"`
	OperatorEmail  string `json:"operator_email,omitempty"`
	OnDemand       bool   `json:"on_demand"`
}

// S3GetAPI is the narrow S3 interface needed to download files.
type S3GetAPI interface {
	GetObject(ctx context.Context, input *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// SESV2API re-exports the narrow SES interface from pkg/aws for use in this package.
type SESV2API = awspkg.SESV2API

// RunCommandFunc is the function signature for running an external command.
// Enables dependency injection for testing without real subprocesses.
type RunCommandFunc func(cmd string, args []string, env []string) ([]byte, error)

// CreateHandler holds injected dependencies for testability.
type CreateHandler struct {
	S3Client     S3GetAPI
	SESClient    SESV2API
	Domain       string
	KMBinaryPath string
	// RunCommand is called to execute the km create subprocess.
	// When nil, defaults to execRunCommand (os/exec-based).
	RunCommand RunCommandFunc
}

// Handle is the Lambda handler method. It is called by lambdaruntime.Start in main().
func (h *CreateHandler) Handle(ctx context.Context, event CreateEvent) error {
	if event.SandboxID == "" {
		return fmt.Errorf("create-handler: sandbox_id is required in event payload")
	}

	log.Info().Str("sandbox_id", event.SandboxID).Str("bucket", event.ArtifactBucket).Msg("create event received")

	// Step 1: Download profile from S3
	profileKey := event.ArtifactPrefix + "/.km-profile.yaml"
	resp, err := h.S3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: strPtr(event.ArtifactBucket),
		Key:    strPtr(profileKey),
	})
	if err != nil {
		return fmt.Errorf("download profile from S3 s3://%s/%s: %w", event.ArtifactBucket, profileKey, err)
	}
	defer resp.Body.Close()
	profileBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read profile bytes: %w", err)
	}

	// Step 2: Write profile to /tmp/{sandbox-id}.yaml
	profilePath := filepath.Join("/tmp", event.SandboxID+".yaml")
	if writeErr := os.WriteFile(profilePath, profileBytes, 0o644); writeErr != nil {
		return fmt.Errorf("write profile to /tmp: %w", writeErr)
	}
	defer os.Remove(profilePath)

	// Step 3: Build km create subprocess arguments
	kmBinary := h.KMBinaryPath
	if kmBinary == "" {
		kmBinary = "/var/task/km"
	}
	args := []string{"create", profilePath, "--aws-profile", ""}
	if event.OnDemand {
		args = append(args, "--on-demand")
	}

	// Pass context env vars to the subprocess
	env := append(os.Environ(),
		"KM_ARTIFACTS_BUCKET="+event.ArtifactBucket,
		"KM_REMOTE_CREATE=true",
	)
	if event.OperatorEmail != "" {
		env = append(env, "KM_OPERATOR_EMAIL="+event.OperatorEmail)
	}

	// Step 4: Run km create subprocess
	runner := h.RunCommand
	if runner == nil {
		runner = execRunCommand
	}
	out, runErr := runner(kmBinary, args, env)
	if runErr != nil {
		log.Error().Err(runErr).Str("sandbox_id", event.SandboxID).
			Str("output", string(out)).Msg("km create subprocess failed")

		// Send failure notification if SES client and operator email are configured
		if h.SESClient != nil && event.OperatorEmail != "" && h.Domain != "" {
			notifyErr := awspkg.SendLifecycleNotification(ctx, h.SESClient, event.OperatorEmail, event.SandboxID, "create-failed", h.Domain)
			if notifyErr != nil {
				log.Warn().Err(notifyErr).Str("sandbox_id", event.SandboxID).
					Msg("failed to send create-failed notification (non-fatal)")
			}
		}
		return fmt.Errorf("km create subprocess failed for sandbox %s: %w", event.SandboxID, runErr)
	}

	log.Info().Str("sandbox_id", event.SandboxID).Str("output", string(out)).
		Msg("km create subprocess succeeded")
	// Step 5: Return nil — km create already sent the "created" lifecycle notification
	return nil
}

// execRunCommand runs an external binary using os/exec.
func execRunCommand(cmd string, args []string, env []string) ([]byte, error) {
	// Import os/exec only in production code path — tests inject RunCommand directly.
	// This avoids importing os/exec in the test binary where it's not needed.
	return runOSExec(cmd, args, env)
}

// strPtr returns a pointer to a string — helper to avoid taking address of literals.
func strPtr(s string) *string {
	return &s
}

// main constructs a CreateHandler with real AWS clients and registers it with the Lambda runtime.
func main() {
	ctx := context.Background()
	awsProfile := os.Getenv("KM_AWS_PROFILE") // empty in Lambda — uses execution role

	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load AWS config")
	}

	domain := os.Getenv("KM_EMAIL_DOMAIN")
	if domain == "" {
		domain = "sandboxes.klankermaker.ai"
	}

	kmBinaryPath := os.Getenv("KM_BINARY_PATH")
	if kmBinaryPath == "" {
		kmBinaryPath = "/var/task/km"
	}

	h := &CreateHandler{
		S3Client:     s3.NewFromConfig(awsCfg),
		SESClient:    sesv2.NewFromConfig(awsCfg),
		Domain:       domain,
		KMBinaryPath: kmBinaryPath,
	}

	lambdaruntime.Start(h.Handle)
}
