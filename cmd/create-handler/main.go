// Package main implements the km create handler Lambda.
//
// When an EventBridge SandboxCreate event arrives (from km create --remote),
// this handler:
//  1. Downloads km, terraform, terragrunt binaries from S3 (cold start)
//  2. Extracts the infra/ tarball from S3 (cold start)
//  3. Downloads the sandbox profile from S3 at {artifact_prefix}/.km-profile.yaml
//  4. Writes the profile to /tmp/{sandbox-id}.yaml
//  5. Runs km create as a subprocess
//  6. On subprocess failure, sends a "create-failed" lifecycle notification via SES
//  7. On success, returns nil — km create already sent the "created" notification
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aws/aws-lambda-go/events"
	lambdaruntime "github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
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
	Alias          string `json:"alias,omitempty"`
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

// DynamoAPI is the narrow DynamoDB interface for status updates.
// *dynamodb.Client satisfies this interface.
type DynamoAPI = awspkg.SandboxMetadataAPI

// CreateHandler holds injected dependencies for testability.
type CreateHandler struct {
	S3Client     S3GetAPI
	SESClient    SESV2API
	DynamoClient DynamoAPI
	TableName    string // DynamoDB sandbox metadata table (default: "km-sandboxes")
	Domain       string
	ToolchainDir string // directory containing km, terraform, terragrunt binaries + infra/
	// RunCommand is called to execute the km create subprocess.
	// When nil, defaults to execRunCommand (os/exec-based).
	RunCommand RunCommandFunc
}

// coldStartOnce ensures toolchain download happens only once per Lambda container.
var coldStartOnce sync.Once
var coldStartErr error

// Handle is the Lambda handler method. EventBridge Rules deliver the full envelope;
// the CreateEvent payload is inside the Detail field as a JSON string.
func (h *CreateHandler) Handle(ctx context.Context, ebEvent events.CloudWatchEvent) error {
	var event CreateEvent
	if err := json.Unmarshal(ebEvent.Detail, &event); err != nil {
		return fmt.Errorf("create-handler: unmarshal detail: %w (raw: %s)", err, string(ebEvent.Detail))
	}

	if event.SandboxID == "" {
		return fmt.Errorf("create-handler: sandbox_id is required in event payload")
	}

	log.Info().Str("sandbox_id", event.SandboxID).Str("bucket", event.ArtifactBucket).Msg("create event received")

	// Cold start: download toolchain from S3 (skip when RunCommand is injected — test mode)
	if h.RunCommand == nil {
		coldStartOnce.Do(func() {
			coldStartErr = h.downloadToolchain(ctx, event.ArtifactBucket)
		})
		if coldStartErr != nil {
			return fmt.Errorf("toolchain download failed: %w", coldStartErr)
		}
	}

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
	kmBinary := filepath.Join(h.ToolchainDir, "km")
	args := []string{"create", profilePath, "--aws-profile", "", "--sandbox-id", event.SandboxID}
	if event.OnDemand {
		args = append(args, "--on-demand")
	}
	if event.Alias != "" {
		args = append(args, "--alias", event.Alias)
	}

	// Pass context env vars to the subprocess
	// Set PATH so km can find terraform and terragrunt
	env := append(os.Environ(),
		"KM_ARTIFACTS_BUCKET="+event.ArtifactBucket,
		"KM_REMOTE_CREATE=true",
		"PATH="+h.ToolchainDir+":/usr/local/bin:/usr/bin:/bin",
		"KM_REPO_ROOT="+h.ToolchainDir,
		"KM_CONFIG_PATH="+filepath.Join(h.ToolchainDir, "km-config.yaml"),
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

		// Determine failure status — "nocap" for capacity errors, "failed" for everything else.
		failStatus := "failed"
		outStr := string(out)
		if strings.Contains(outStr, "InsufficientInstanceCapacity") ||
			strings.Contains(outStr, "MaxSpotInstanceCountExceeded") ||
			strings.Contains(outStr, "SpotMaxPriceTooLow") ||
			strings.Contains(outStr, "no Spot capacity") {
			failStatus = "nocap"
		}

		// Update DynamoDB metadata so km list shows the failure instead of stuck "starting"
		if h.DynamoClient != nil && h.TableName != "" {
			if statusErr := awspkg.UpdateSandboxStatusDynamo(ctx, h.DynamoClient, h.TableName, event.SandboxID, failStatus); statusErr != nil {
				log.Warn().Err(statusErr).Str("sandbox_id", event.SandboxID).
					Str("status", failStatus).Msg("failed to update metadata status (non-fatal)")
			} else {
				log.Info().Str("sandbox_id", event.SandboxID).
					Str("status", failStatus).Msg("updated metadata status")
			}
		}

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
	return nil
}

// downloadToolchain downloads km, terraform, terragrunt, and infra/ from S3 to the toolchain dir.
func (h *CreateHandler) downloadToolchain(ctx context.Context, bucket string) error {
	log.Info().Str("dir", h.ToolchainDir).Msg("downloading toolchain from S3 (cold start)")

	if err := os.MkdirAll(h.ToolchainDir, 0o755); err != nil {
		return fmt.Errorf("create toolchain dir: %w", err)
	}

	// Download binaries
	binaries := []struct {
		s3Key    string
		localName string
	}{
		{s3Key: "toolchain/km", localName: "km"},
		{s3Key: "toolchain/terraform", localName: "terraform"},
		{s3Key: "toolchain/terragrunt", localName: "terragrunt"},
	}

	for _, b := range binaries {
		localPath := filepath.Join(h.ToolchainDir, b.localName)
		if err := downloadS3File(ctx, h.S3Client, bucket, b.s3Key, localPath); err != nil {
			return fmt.Errorf("download %s: %w", b.s3Key, err)
		}
		if err := os.Chmod(localPath, 0o755); err != nil {
			return fmt.Errorf("chmod %s: %w", localPath, err)
		}
		log.Info().Str("binary", b.localName).Msg("downloaded")
	}

	// Download and extract infra tarball
	infraTarKey := "toolchain/infra.tar.gz"
	tarPath := filepath.Join(h.ToolchainDir, "infra.tar.gz")
	if err := downloadS3File(ctx, h.S3Client, bucket, infraTarKey, tarPath); err != nil {
		return fmt.Errorf("download infra tarball: %w", err)
	}
	if err := extractTarGz(tarPath, h.ToolchainDir); err != nil {
		return fmt.Errorf("extract infra tarball: %w", err)
	}
	os.Remove(tarPath)
	log.Info().Msg("infra/ extracted")

	// Download km-config.yaml for subprocess config
	kmConfigKey := "toolchain/km-config.yaml"
	kmConfigPath := filepath.Join(h.ToolchainDir, "km-config.yaml")
	if err := downloadS3File(ctx, h.S3Client, bucket, kmConfigKey, kmConfigPath); err != nil {
		log.Warn().Err(err).Msg("km-config.yaml not found in toolchain (non-fatal, subprocess will use defaults)")
	} else {
		log.Info().Msg("downloaded km-config.yaml")
	}

	return nil
}

// downloadS3File downloads an S3 object to a local file.
func downloadS3File(ctx context.Context, client S3GetAPI, bucket, key, localPath string) error {
	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("get s3://%s/%s: %w", bucket, key, err)
	}
	defer resp.Body.Close()

	f, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// extractTarGz extracts a .tar.gz file to the given directory using pure Go.
func extractTarGz(tarPath, destDir string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		target := filepath.Join(destDir, hdr.Name)

		// Prevent path traversal
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("tar entry %q escapes destination", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
			if err := os.Chmod(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		}
	}
	return nil
}

// execRunCommand runs an external binary using os/exec.
func execRunCommand(cmd string, args []string, env []string) ([]byte, error) {
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

	toolchainDir := os.Getenv("KM_TOOLCHAIN_DIR")
	if toolchainDir == "" {
		toolchainDir = "/tmp/toolchain"
	}

	tableName := os.Getenv("KM_SANDBOX_TABLE")
	if tableName == "" {
		tableName = "km-sandboxes"
	}

	h := &CreateHandler{
		S3Client:     s3.NewFromConfig(awsCfg),
		SESClient:    sesv2.NewFromConfig(awsCfg),
		DynamoClient: awsdynamodb.NewFromConfig(awsCfg),
		TableName:    tableName,
		Domain:       domain,
		ToolchainDir: toolchainDir,
	}

	lambdaruntime.Start(h.Handle)
}
