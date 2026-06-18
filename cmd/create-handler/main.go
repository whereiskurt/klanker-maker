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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/events"
	lambdaruntime "github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/goccy/go-yaml"
	"github.com/rs/zerolog/log"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// errExecFailed is a sentinel error used by RunCommand in tests to simulate subprocess failure.
var errExecFailed = errors.New("exec: subprocess failed")

// CreateEvent is the EventBridge detail payload delivered to this Lambda.
// It matches the SandboxCreateDetail published by km create --remote.
type CreateEvent struct {
	SandboxID      string  `json:"sandbox_id"`
	ArtifactBucket string  `json:"artifact_bucket"`
	ArtifactPrefix string  `json:"artifact_prefix"`
	OperatorEmail  string  `json:"operator_email,omitempty"`
	OnDemand       bool    `json:"on_demand"`
	Alias          string  `json:"alias,omitempty"`
	TTL            string  `json:"ttl,omitempty"`
	Idle           string  `json:"idle,omitempty"`
	ComputeBudget  float64 `json:"compute_budget,omitempty"`
	AIBudget       float64 `json:"ai_budget,omitempty"`
	NoBedrock      bool    `json:"no_bedrock,omitempty"`

	// Prompt carries an initial agent prompt for the new box (Phase 116 cold-create
	// dispatch). When non-empty, it is forwarded to `km create --prompt`, which seeds
	// the on-box prompt queue (/workspace/.km-agent/queue) drained on first boot.
	// Empty for plain creates. Mirrors the field on SandboxCreateDetail.
	Prompt string `json:"prompt,omitempty"`

	// GithubEnvelope carries the JSON-serialized GitHub webhook envelope for
	// cold-create correction (Phase 97, Pitfall 1 fix). Matches the same field
	// on SandboxCreateDetail. After provisioning, this is drained into the new
	// sandbox's github-inbound FIFO queue. Empty for non-github creates (dormant).
	GithubEnvelope string `json:"github_envelope,omitempty"`
}

// S3GetAPI is the narrow S3 interface needed to download files.
type S3GetAPI interface {
	GetObject(ctx context.Context, input *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// GithubInboundSQSAPI is the narrow SQS interface used by the github-inbound
// enqueue step (Phase 97, Task 3). Only GetQueueUrl and SendMessage are needed.
// *sqs.Client satisfies this interface directly.
type GithubInboundSQSAPI interface {
	GetQueueUrl(ctx context.Context, input *sqs.GetQueueUrlInput, optFns ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error)
	SendMessage(ctx context.Context, input *sqs.SendMessageInput, optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// SESV2API re-exports the narrow SES interface from pkg/aws for use in this package.
type SESV2API = awspkg.SESV2API

// RunCommandFunc is the function signature for running an external command.
// Enables dependency injection for testing without real subprocesses.
type RunCommandFunc func(cmd string, args []string, env []string) ([]byte, error)

// DynamoAPI is the narrow DynamoDB interface for status updates.
// *dynamodb.Client satisfies this interface.
type DynamoAPI = awspkg.SandboxMetadataAPI

// EC2DescribeAPI is the narrow EC2 interface for the idempotency guard (Bug J):
// looking up whether a sandbox-id already has a provisioned instance.
// *ec2.Client satisfies this interface.
type EC2DescribeAPI interface {
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// CreateHandler holds injected dependencies for testability.
type CreateHandler struct {
	S3Client          S3GetAPI
	SESClient         SESV2API
	DynamoClient      DynamoAPI
	// EC2Client backs the idempotency guard. Nil in unit tests (RunCommand mode) →
	// the guard is skipped. main() injects *ec2.Client.
	EC2Client         EC2DescribeAPI
	SSMClient         awspkg.IdentitySSMAPI   // for identity key generation
	IdentityClient    awspkg.IdentityTableAPI // for publishing identity to DynamoDB
	TableName         string                  // DynamoDB sandbox metadata table (default: "km-sandboxes")
	IdentityTableName string                  // DynamoDB identity table (default: "km-identities")
	Domain            string
	ToolchainDir      string // directory containing km, terraform, terragrunt binaries + infra/
	// SQSClient is used to drain the carried GithubEnvelope into the per-sandbox
	// github-inbound FIFO queue after provisioning (Phase 97, Task 3).
	// When nil, the enqueue step is skipped — only non-nil when the event has a
	// non-empty GithubEnvelope (dormant for non-github creates). In production,
	// the main() function injects *sqs.Client from the shared AWS config.
	SQSClient GithubInboundSQSAPI
	// RunCommand is called to execute the km create subprocess.
	// When nil, defaults to execRunCommand (os/exec-based).
	RunCommand RunCommandFunc
}

// coldStartOnce ensures toolchain download happens only once per Lambda container.
var coldStartOnce sync.Once
var coldStartErr error

// getEmailDomain returns the sandbox email domain from the KM_EMAIL_DOMAIN env var.
// Falls back to "sandboxes.klankermaker.ai" for un-migrated installs until plan 04 wires the env block.
func getEmailDomain() string {
	if v := os.Getenv("KM_EMAIL_DOMAIN"); v != "" {
		return v
	}
	return "sandboxes.klankermaker.ai"
}

// resourcePrefix returns the resource prefix from the KM_RESOURCE_PREFIX env var,
// falling back to "km" only when truly unset. Source of truth for derived
// defaults below so a non-default install (e.g. resource_prefix=kph) gets
// prefix-correct fallbacks even if other env vars accidentally aren't set.
func resourcePrefix() string {
	if v := os.Getenv("KM_RESOURCE_PREFIX"); v != "" {
		return v
	}
	return "km"
}

// sandboxTableName returns the DynamoDB sandbox table name from the
// KM_SANDBOX_TABLE_NAME env var. Falls back to {prefix}-sandboxes derived
// from KM_RESOURCE_PREFIX so a non-default install resolves to its actual
// table (kph-sandboxes) instead of silently using the literal "km-sandboxes".
func sandboxTableName() string {
	if v := os.Getenv("KM_SANDBOX_TABLE_NAME"); v != "" {
		return v
	}
	return resourcePrefix() + "-sandboxes"
}

// identitiesTable returns the DynamoDB identities table name from the
// KM_IDENTITIES_TABLE env var. Same prefix-derived fallback as sandboxTableName.
func identitiesTable() string {
	if v := os.Getenv("KM_IDENTITIES_TABLE"); v != "" {
		return v
	}
	return resourcePrefix() + "-identities"
}

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

	// Idempotency guard (Bug J): EventBridge retries a failed async Lambda invoke. A
	// create that provisioned an EC2 instance before failing at a later step (e.g. the
	// prompt-push) would otherwise be re-provisioned as a SECOND instance for the same
	// sandbox_id on retry (observed live in Phase 116). If a non-terminated instance
	// already carries this sandbox-id tag, the box exists — skip the duplicate (nil).
	if h.EC2Client != nil {
		if instID := h.existingInstanceID(ctx, event.SandboxID); instID != "" {
			log.Info().Str("sandbox_id", event.SandboxID).Str("instance", instID).
				Msg("sandbox already has a provisioned instance — skipping duplicate create (idempotent retry)")
			return nil
		}
	}

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

	// Step 1b (Phase 89): if the profile declares spec.secrets.sopsFile, download
	// the bundle from the remote-create prefix and rewrite the profile's sopsFile
	// to an absolute /tmp path. The km subprocess then resolves it correctly and
	// uploads to the sandbox's S3 destination via uploadSopsBundleIfPresent.
	profileBytes, sopsBundlePath, err := patchProfileForSops(ctx, h.S3Client, event.ArtifactBucket, event.ArtifactPrefix, event.SandboxID, profileBytes)
	if err != nil {
		return fmt.Errorf("patch profile for sops: %w", err)
	}
	if sopsBundlePath != "" {
		defer os.Remove(sopsBundlePath)
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
	if event.TTL != "" {
		args = append(args, "--ttl", event.TTL)
	}
	if event.Idle != "" {
		args = append(args, "--idle", event.Idle)
	}
	if event.ComputeBudget > 0 {
		args = append(args, "--compute", fmt.Sprintf("%.2f", event.ComputeBudget))
	}
	if event.AIBudget > 0 {
		args = append(args, "--ai", fmt.Sprintf("%.2f", event.AIBudget))
	}
	if event.NoBedrock {
		args = append(args, "--no-bedrock")
	}
	if event.Prompt != "" {
		// Phase 116 cold-create: seed the on-box prompt queue so the new box runs
		// the trigger prompt on first boot. args is exec'd directly (no shell), so a
		// multi-line prompt is safe as a single arg element.
		args = append(args, "--prompt", event.Prompt)
	}

	// Pass context env vars to the subprocess
	// Set PATH so km can find terraform and terragrunt
	env := append(os.Environ(),
		"KM_ARTIFACTS_BUCKET="+event.ArtifactBucket,
		"KM_REMOTE_CREATE=true",
		"PATH="+h.ToolchainDir+":/usr/local/bin:/usr/bin:/bin",
		"KM_REPO_ROOT="+h.ToolchainDir,
		"KM_CONFIG_PATH="+filepath.Join(h.ToolchainDir, "km-config.yaml"),
		"HOME=/tmp",
	)
	if event.OperatorEmail != "" {
		env = append(env, "KM_OPERATOR_EMAIL="+event.OperatorEmail)
	}

	// Phase 73: pass the operator-generated VS Code SSH pubkey through to the
	// subprocess so its keypair-gen step reuses it instead of writing to a
	// read-only Lambda filesystem and producing a key the operator doesn't have.
	pubkeyKey := event.ArtifactPrefix + "/vscode-pubkey.txt"
	if pkResp, pkErr := h.S3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: strPtr(event.ArtifactBucket),
		Key:    strPtr(pubkeyKey),
	}); pkErr == nil {
		pkBytes, readErr := io.ReadAll(pkResp.Body)
		pkResp.Body.Close()
		if readErr == nil && len(pkBytes) > 0 {
			env = append(env, "KM_VSCODE_SSH_PUBKEY="+strings.TrimSpace(string(pkBytes)))
		}
	}

	// Phase 93: pass the operator-generated KasmVNC credential ("user:pass") through
	// to the subprocess so its desktop-credential step REUSES it instead of
	// regenerating a fresh password the operator never sees — otherwise the box's
	// ~/.kasmpasswd and the operator's ~/.km/desktop/<id> diverge and login 401s.
	credKey := event.ArtifactPrefix + "/desktop-creds.txt"
	if dcResp, dcErr := h.S3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: strPtr(event.ArtifactBucket),
		Key:    strPtr(credKey),
	}); dcErr == nil {
		dcBytes, readErr := io.ReadAll(dcResp.Body)
		dcResp.Body.Close()
		if readErr == nil {
			if parts := strings.SplitN(strings.TrimSpace(string(dcBytes)), ":", 2); len(parts) == 2 && parts[0] != "" && parts[1] != "" {
				env = append(env,
					"KM_DESKTOP_KASM_USER="+parts[0],
					"KM_DESKTOP_KASM_PASS="+parts[1],
				)
			}
		}
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

		// Phase 77: extract a one-line summary from subprocess output for persistence.
		failureReason := extractFailureReason(outStr)
		failedAt := time.Now().UTC()

		// Update DynamoDB metadata so km list shows the failure instead of stuck "starting".
		// Phase 77: switch to the new helper so failure_reason and failed_at land in the same UpdateItem.
		if h.DynamoClient != nil && h.TableName != "" {
			if statusErr := awspkg.UpdateSandboxStatusAndReasonDynamo(ctx, h.DynamoClient, h.TableName, event.SandboxID, failStatus, failureReason, failedAt); statusErr != nil {
				log.Warn().Err(statusErr).Str("sandbox_id", event.SandboxID).
					Str("status", failStatus).Msg("failed to update metadata status+reason (non-fatal)")
			} else {
				log.Info().Str("sandbox_id", event.SandboxID).
					Str("status", failStatus).Msg("updated metadata status with failure reason")
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

	// Step 5: Provision sandbox identity (Ed25519 signing key + DynamoDB public key).
	// Non-fatal: sandbox is already provisioned — identity failure does not break the sandbox.
	// Only runs when SSM and identity DynamoDB clients are wired up and the profile has an email section.
	// Note: km create subprocess (Step 4) already runs this via Step 15 in runCreate. This step
	// provides belt-and-suspenders coverage for cases where the subprocess identity step fails
	// (e.g., SSM permissions not yet propagated at subprocess time).
	if h.SSMClient != nil && h.IdentityClient != nil {
		parsedProfile, profileErr := profile.Parse(profileBytes)
		if profileErr != nil {
			log.Warn().Err(profileErr).Str("sandbox_id", event.SandboxID).
				Msg("failed to parse profile for identity generation (non-fatal)")
		} else if parsedProfile.Spec.Email != nil {
			resourcePrefix := os.Getenv("KM_RESOURCE_PREFIX")
			if resourcePrefix == "" {
				resourcePrefix = "km"
			}
			kmsKeyAlias := os.Getenv("KM_PLATFORM_KMS_KEY_ARN")
			if kmsKeyAlias == "" {
				region := os.Getenv("KM_REGION_LABEL")
				if region == "" {
					region = "use1"
				}
				kmsKeyAlias = "alias/km-platform-" + resourcePrefix + "-" + region
			}

			pubKey, identErr := awspkg.EnsureSandboxIdentity(ctx, h.SSMClient, resourcePrefix, event.SandboxID, kmsKeyAlias)
			if identErr != nil {
				log.Warn().Err(identErr).Str("sandbox_id", event.SandboxID).
					Msg("failed to provision sandbox identity (non-fatal)")
			} else {
				// Conditionally generate X25519 encryption key if profile requires/allows encryption.
				var encPubKey *[32]byte
				enc := parsedProfile.Spec.Email.Encryption
				if enc == "optional" || enc == "required" {
					encPubKey, identErr = awspkg.GenerateEncryptionKey(ctx, h.SSMClient, resourcePrefix, event.SandboxID, kmsKeyAlias)
					if identErr != nil {
						log.Warn().Err(identErr).Str("sandbox_id", event.SandboxID).
							Msg("failed to generate encryption key (non-fatal — signing key still published)")
					}
				}

				// Derive email domain from handler config (set at init from KM_EMAIL_DOMAIN env var).
				emailDomain := h.Domain
				if emailDomain == "" {
					emailDomain = getEmailDomain()
				}

				identityTableName := h.IdentityTableName
				if identityTableName == "" {
					identityTableName = identitiesTable()
				}
				identityEmailAddr := fmt.Sprintf("%s@%s", event.SandboxID, emailDomain)
				signing := parsedProfile.Spec.Email.Signing
				verifyInbound := parsedProfile.Spec.Email.VerifyInbound
				encryption := parsedProfile.Spec.Email.Encryption
				alias := parsedProfile.Spec.Email.Alias
				allowedSenders := parsedProfile.Spec.Email.AllowedSenders
				if pubErr := awspkg.PublishIdentity(ctx, h.IdentityClient, identityTableName, event.SandboxID, identityEmailAddr, pubKey, encPubKey, signing, verifyInbound, encryption, alias, allowedSenders); pubErr != nil {
					log.Warn().Err(pubErr).Str("sandbox_id", event.SandboxID).
						Msg("failed to publish identity to DynamoDB (non-fatal)")
				} else {
					log.Info().Str("sandbox_id", event.SandboxID).Msg("sandbox identity provisioned and published")
				}
			}
		}
	}

	// Step 6 (Phase 97): Drain the carried GithubEnvelope into the sandbox's
	// github-inbound FIFO queue so the poller dispatches it on first boot.
	// Best-effort: a failed enqueue does NOT fail the create — the operator can
	// re-mention to trigger a new event. Non-github creates have GithubEnvelope=""
	// so this block is entirely dormant for non-github sandboxes.
	if event.GithubEnvelope != "" && h.SQSClient != nil {
		if enqErr := h.drainGithubEnvelope(ctx, event.SandboxID, event.GithubEnvelope); enqErr != nil {
			log.Warn().Err(enqErr).Str("sandbox_id", event.SandboxID).
				Msg("failed to enqueue github envelope into github-inbound queue (non-fatal — operator can re-mention)")
		}
	}

	return nil
}

// drainGithubEnvelope resolves the per-sandbox github-inbound FIFO queue URL and
// sends the carried envelope as a FIFO message (MessageGroupId = sandboxID,
// MessageDeduplicationId = SHA-256 hex of the envelope body). Both fields are
// required by FIFO queues with ContentBasedDeduplication=false.
// existingInstanceID returns the ID of a non-terminated EC2 instance already tagged
// with the given sandbox-id, or "" if none (the Bug J idempotency check). A transient
// DescribeInstances error returns "" (fail-open) so a genuine first create is never
// blocked by a read blip.
func (h *CreateHandler) existingInstanceID(ctx context.Context, sandboxID string) string {
	out, err := h.EC2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:km:sandbox-id"), Values: []string{sandboxID}},
			{Name: aws.String("instance-state-name"), Values: []string{"pending", "running", "stopping", "stopped"}},
		},
	})
	if err != nil {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).Msg("idempotency DescribeInstances failed; proceeding with create (fail-open)")
		return ""
	}
	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			return aws.ToString(inst.InstanceId)
		}
	}
	return ""
}

func (h *CreateHandler) drainGithubEnvelope(ctx context.Context, sandboxID, envelope string) error {
	prefix := resourcePrefix()
	queueName := awspkg.GitHubInboundQueueName(prefix, sandboxID)

	urlOut, err := h.SQSClient.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{
		QueueName: aws.String(queueName),
	})
	if err != nil {
		return fmt.Errorf("get github-inbound queue URL for %s: %w", queueName, err)
	}
	queueURL := aws.ToString(urlOut.QueueUrl)

	// FIFO dedup: SHA-256 hex of the envelope body so duplicate cold-create
	// events (e.g. from retried EventBridge delivery) are de-duplicated within
	// the 5-minute SQS dedup window.
	sum := sha256.Sum256([]byte(envelope))
	dedupID := fmt.Sprintf("%x", sum[:])

	_, err = h.SQSClient.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               aws.String(queueURL),
		MessageBody:            aws.String(envelope),
		MessageGroupId:         aws.String(sandboxID),
		MessageDeduplicationId: aws.String(dedupID),
	})
	if err != nil {
		return fmt.Errorf("send github envelope to %s: %w", queueURL, err)
	}
	log.Info().Str("sandbox_id", sandboxID).Str("queue", queueName).
		Msg("github envelope drained into github-inbound queue")
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
// patchProfileForSops bridges the operator → Lambda → km subprocess gap for
// Phase 89 SOPS bundles. When the profile declares spec.secrets.sopsFile (a
// path relative to the profile YAML on the operator workstation), the Lambda
// downloads the bundle from the remote-create S3 prefix and rewrites the
// profile's sopsFile to an absolute /tmp path. The km subprocess then
// resolves the path against the rewritten profile and uploads the bundle to
// the sandbox's S3 destination via uploadSopsBundleIfPresent.
//
// No-op when the profile lacks Spec.Secrets or SopsFile is empty.
// Returns the (possibly unchanged) profile bytes and the local bundle path
// (empty when no bundle). Caller defers os.Remove(bundlePath) when non-empty.
func patchProfileForSops(ctx context.Context, client S3GetAPI, bucket, prefix, sandboxID string, profileBytes []byte) ([]byte, string, error) {
	p, err := profile.Parse(profileBytes)
	if err != nil {
		return nil, "", fmt.Errorf("parse profile: %w", err)
	}
	if p.Spec.Secrets == nil || p.Spec.Secrets.SopsFile == "" {
		return profileBytes, "", nil
	}

	bundleKey := prefix + "/.km-secrets-bundle.enc.yaml"
	bundlePath := filepath.Join("/tmp", sandboxID+"-secrets.enc.yaml")
	if dlErr := downloadS3File(ctx, client, bucket, bundleKey, bundlePath); dlErr != nil {
		return nil, "", fmt.Errorf("download sops bundle s3://%s/%s: %w", bucket, bundleKey, dlErr)
	}

	p.Spec.Secrets.SopsFile = bundlePath

	newBytes, marshalErr := yaml.Marshal(p)
	if marshalErr != nil {
		return nil, bundlePath, fmt.Errorf("marshal patched profile: %w", marshalErr)
	}
	return newBytes, bundlePath, nil
}

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

// extractFailureReason pulls a failure summary out of subprocess output for persistence
// in the sandbox DynamoDB record (Phase 77). The result is capped at 1024 chars per the
// locked PRD decision in 77-CONTEXT.md.
//
// Strategy:
//  1. Scan the output bottom-up. Take the first line that starts with "Error:" — that
//     is the km error format and is the most actionable single-line summary.
//  2. If no such line exists, take the last 1024 chars of the output and prefix with
//     "<no error line; tail of subprocess output> " so the operator knows it is a tail
//     dump rather than a structured error.
//
// The return value is always ≤1024 chars.
func extractFailureReason(out string) string {
	const maxLen = 1024
	const noErrorMarker = "<no error line; tail of subprocess output> "

	// Scan lines from the bottom. strings.Split on "\n" preserves trailing-empty
	// entries — that's fine; the loop skips lines that don't start with "Error:".
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if strings.HasPrefix(line, "Error:") {
			if len(line) > maxLen {
				return line[:maxLen]
			}
			return line
		}
	}

	// No Error: line — fall back to a tail dump.
	tail := out
	if len(tail) > maxLen-len(noErrorMarker) {
		tail = tail[len(tail)-(maxLen-len(noErrorMarker)):]
	}
	return noErrorMarker + tail
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

	domain := getEmailDomain()

	toolchainDir := os.Getenv("KM_TOOLCHAIN_DIR")
	if toolchainDir == "" {
		toolchainDir = "/tmp/toolchain"
	}

	dynClient := awsdynamodb.NewFromConfig(awsCfg)
	h := &CreateHandler{
		S3Client:          s3.NewFromConfig(awsCfg),
		SESClient:         sesv2.NewFromConfig(awsCfg),
		DynamoClient:      dynClient,
		EC2Client:         ec2.NewFromConfig(awsCfg), // Bug J idempotency guard

		SSMClient:         ssm.NewFromConfig(awsCfg),
		IdentityClient:    dynClient,
		TableName:         sandboxTableName(),
		IdentityTableName: identitiesTable(),
		Domain:            domain,
		ToolchainDir:      toolchainDir,
		// Phase 97: inject SQS client for github-inbound envelope drain.
		SQSClient: sqs.NewFromConfig(awsCfg),
	}

	lambdaruntime.Start(h.Handle)
}
