package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// NewCloneCmd creates the "km clone" command with real AWS-backed clients.
func NewCloneCmd(cfg *config.Config) *cobra.Command {
	return NewCloneCmdWithDeps(cfg, nil, nil)
}

// NewCloneCmdWithDeps builds the clone command with optional dependency injection for
// testing. Pass nil for real AWS-backed clients.
func NewCloneCmdWithDeps(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	var alias string
	var count int
	var noCopy bool
	var verbose bool
	var awsProfile string

	cmd := &cobra.Command{
		Use:   "clone <source> [new-alias]",
		Short: "Duplicate a running sandbox from its stored profile",
		Long: `Create one or more independent sandbox clones from a running source sandbox.

The source sandbox's stored profile is fetched from S3 and used to provision a
fresh clone. By default, the /workspace directory is staged through SSM and S3
then downloaded to the new instance at boot time (EC2 only).

Use --no-copy to skip workspace staging and create a fresh sandbox from the same
profile without copying any files.

Use --count with --alias to create multiple clones with auto-suffixed aliases
(e.g. --alias wrkr --count 3 creates wrkr-1, wrkr-2, wrkr-3).

Examples:
  km clone sb-abc12345                        # clone, no alias
  km clone sb-abc12345 myalias               # alias from positional arg
  km clone sb-abc12345 --alias worker        # alias from flag
  km clone sb-abc12345 --alias wrkr --count 3 # 3 clones: wrkr-1, wrkr-2, wrkr-3
  km clone sb-abc12345 --no-copy             # fresh sandbox, no workspace copy`,
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			sourceRef := args[0]

			// If positional alias is given and --alias flag is empty, use positional
			if len(args) == 2 && alias == "" {
				alias = args[1]
			}

			// Validate: --count > 1 requires an alias
			if count > 1 && alias == "" {
				return fmt.Errorf("--alias is required when --count > 1")
			}

			return runClone(ctx, cfg, fetcher, ssmClient, sourceRef, alias, count, noCopy, verbose, awsProfile)
		},
	}

	cmd.Flags().StringVar(&alias, "alias", "", "Alias for the clone (required with --count)")
	cmd.Flags().IntVar(&count, "count", 1, "Number of clones to create")
	cmd.Flags().BoolVar(&noCopy, "no-copy", false, "Skip workspace staging; create a fresh sandbox from same profile")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Show verbose output during provisioning")
	cmd.Flags().StringVar(&awsProfile, "aws-profile", "klanker-terraform",
		"AWS profile for provisioning (same as km create --aws-profile)")

	return cmd
}

// runClone is the main clone orchestration function.
func runClone(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI,
	sourceRef, alias string, count int, noCopy bool, verbose bool, awsProfile string) error {

	// Step 1: Resolve source sandbox reference
	sourceID, err := ResolveSandboxID(ctx, cfg, sourceRef)
	if err != nil {
		return fmt.Errorf("resolve source sandbox: %w", err)
	}

	// Step 2: Read source metadata (use provided fetcher or create real one)
	var rec *awspkg.SandboxRecord
	if fetcher != nil {
		rec, err = fetcher.FetchSandbox(ctx, sourceID)
	} else {
		awsCfg, awsErr := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
		if awsErr != nil {
			return fmt.Errorf("load AWS config: %w", awsErr)
		}
		tableName := cfg.SandboxTableName
		if tableName == "" {
			tableName = "km-sandboxes"
		}
		realFetcher := newRealFetcher(awsCfg, cfg.StateBucket, tableName)
		rec, err = realFetcher.FetchSandbox(ctx, sourceID)
	}
	if err != nil {
		return fmt.Errorf("fetch source sandbox metadata: %w", err)
	}

	// Step 3: Validate source is running
	if rec.Status != "running" {
		return fmt.Errorf("source sandbox %s is not running (status: %s); run 'km resume %s' first",
			sourceID, rec.Status, sourceID)
	}

	// Step 4: Validate ECS substrate restriction for workspace copy
	if !noCopy && rec.Substrate == "ecs" {
		return fmt.Errorf("workspace copy is not supported for ECS sandboxes: SSM SendCommand is not available on ECS tasks; use --no-copy to clone the profile only")
	}

	// Step 5: Fetch stored profile from S3
	profileBytes, err := fetchStoredProfile(ctx, cfg, sourceID, ssmClient)
	if err != nil {
		return fmt.Errorf("fetch stored profile for %s: %w", sourceID, err)
	}

	// Step 6: Stage workspace via SSM (unless --no-copy)
	var stagingKey string
	if !noCopy {
		instanceID, extractErr := extractResourceID(rec.Resources, ":instance/")
		if extractErr != nil {
			return fmt.Errorf("find source instance ID: %w", extractErr)
		}

		// One staging upload serves all N clones
		timestamp := time.Now().UTC().Format("20060102T150405Z")
		stagingKey = fmt.Sprintf("artifacts/%s/staging/clone-%s.tar.gz", sourceID, timestamp)

		fmt.Printf("  Staging workspace from %s...", sourceID)

		var ssmClientReal *ssm.Client
		if ssmClient == nil {
			awsCfg, awsErr := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
			if awsErr != nil {
				return fmt.Errorf("load AWS config for SSM: %w", awsErr)
			}
			ssmClientReal = ssm.NewFromConfig(awsCfg)
		}

		stagingCmd := BuildWorkspaceStagingCmd([]string{"home/sandbox"}, cfg.ArtifactsBucket, stagingKey)
		if err := sendSSMCommand(ctx, ssmClient, ssmClientReal, instanceID, stagingCmd, "CLONE_STAGE_OK", "workspace stage"); err != nil {
			return fmt.Errorf("stage workspace from %s: %w", sourceID, err)
		}

		// For Docker substrate, defer cleanup of staging object.
		// For EC2, userdata cleans up the well-known key after download,
		// and we clean the source staging key here.
		if rec.Substrate == "docker" {
			defer func() {
				cleanupStagingKey(ctx, cfg, sourceID, stagingKey, ssmClient)
			}()
		} else {
			// Clean up the source staging key after all S3 copies are done (deferred to end of function)
			defer func() {
				cleanupStagingKey(ctx, cfg, sourceID, stagingKey, ssmClient)
			}()
		}
	}

	// Step 7: Write profile to temp file
	tmpFile, err := os.CreateTemp("", "km-clone-profile-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp profile file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(profileBytes); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp profile: %w", err)
	}
	tmpFile.Close()

	// Step 8: Generate aliases for all clones
	aliases := GenerateCloneAliases(alias, count)

	// Step 9: Provision each clone.
	// For EC2 remote creates: fire-and-forget. The workspace staging tarball is
	// copied to each clone's well-known S3 key (artifacts/{clone-id}/clone-workspace.tar.gz)
	// and userdata downloads it at boot. No waiting required.
	// For Docker: synchronous create, then SSM download.
	var s3ClientForCopy *s3.Client
	if !noCopy && stagingKey != "" && rec.Substrate != "docker" && ssmClient == nil {
		awsCfgCopy, copyErr := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
		if copyErr == nil {
			s3ClientForCopy = s3.NewFromConfig(awsCfgCopy)
		}
	}

	for i, cloneAlias := range aliases {
		if count > 1 {
			fmt.Printf("  Provisioning clone %d/%d: %s...\n", i+1, count, cloneAlias)
		} else {
			fmt.Printf("  Provisioning clone from %s...\n", sourceID)
		}

		if rec.Substrate == "docker" {
			// Docker: synchronous local create
			if err := runCreate(cfg, tmpFile.Name(), false, false, awsProfile, verbose, "", cloneAlias, "", "", ""); err != nil {
				return fmt.Errorf("provision clone %d (%s): %w", i+1, cloneAlias, err)
			}
			// Docker workspace download (synchronous — instance ready immediately)
			if !noCopy && stagingKey != "" {
				cloneID, resolveErr := resolveCloneID(ctx, cfg, cloneAlias, ssmClient)
				if resolveErr != nil {
					fmt.Printf("  Warning: could not resolve clone for workspace download: %v\n", resolveErr)
				} else {
					fmt.Printf("  Downloading workspace to %s...", cloneID)
					if dlErr := downloadWorkspaceToClone(ctx, cfg, cloneID, stagingKey, ssmClient); dlErr != nil {
						fmt.Printf("\n  Warning: workspace download failed: %v\n", dlErr)
					} else {
						fmt.Print(" done\n")
					}
				}
			}
		} else {
			// EC2: fire-and-forget remote create
			cloneID, createErr := runCreateRemote(cfg, tmpFile.Name(), false, false, awsProfile, cloneAlias, "", "")
			if createErr != nil {
				return fmt.Errorf("provision clone %d (%s): %w", i+1, cloneAlias, createErr)
			}

			// Copy staging tarball to clone's well-known S3 key so userdata downloads it at boot
			if !noCopy && stagingKey != "" && s3ClientForCopy != nil {
				cloneWorkspaceKey := fmt.Sprintf("artifacts/%s/clone-workspace.tar.gz", cloneID)
				_, copyErr := s3ClientForCopy.CopyObject(ctx, &s3.CopyObjectInput{
					Bucket:     awssdk.String(cfg.ArtifactsBucket),
					CopySource: awssdk.String(cfg.ArtifactsBucket + "/" + stagingKey),
					Key:        awssdk.String(cloneWorkspaceKey),
				})
				if copyErr != nil {
					fmt.Printf("  Warning: could not stage workspace for %s: %v\n", cloneID, copyErr)
				} else {
					fmt.Printf("  ✓ Workspace staged for %s (will download at boot)\n", cloneID)
				}
			}

			// Set cloned_from immediately (metadata already written by runCreateRemote)
			if err := updateClonedFromByID(ctx, cfg, cloneID, sourceID, ssmClient); err != nil {
				fmt.Printf("  Warning: could not update cloned_from metadata: %v\n", err)
			}
		}

		fmt.Printf("  ✓ Clone dispatched: %s\n", cloneAlias)
	}

	return nil
}

// fetchStoredProfile downloads the stored profile YAML from S3 for a sandbox.
func fetchStoredProfile(ctx context.Context, cfg *config.Config, sandboxID string, ssmClient SSMSendAPI) ([]byte, error) {
	profileKey := fmt.Sprintf("artifacts/%s/.km-profile.yaml", sandboxID)

	var s3Client *s3.Client
	if ssmClient == nil {
		// Real mode: create real S3 client
		awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
		s3Client = s3.NewFromConfig(awsCfg)
	} else {
		// Test mode with injected SSM client — return stub profile bytes
		// Tests that exercise fetchStoredProfile use the real path. For tests that
		// only need flag parsing/validation errors, we never reach here.
		return []byte("metadata:\n  name: test-profile\n  prefix: sb\nspec:\n  compute: {}\n"), nil
	}

	out, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(cfg.ArtifactsBucket),
		Key:    awssdk.String(profileKey),
	})
	if err != nil {
		return nil, fmt.Errorf("get profile %q: %w", profileKey, err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	return data, nil
}

// sendSSMCommand sends an SSM command and polls for completion.
// Uses injected ssmClient if non-nil, otherwise uses real ssmClientReal.
func sendSSMCommand(ctx context.Context, ssmClient SSMSendAPI, ssmClientReal *ssm.Client, instanceID, shellCmd, successMarker, name string) error {
	input := &ssm.SendCommandInput{
		InstanceIds:  []string{instanceID},
		DocumentName: awssdk.String("AWS-RunShellScript"),
		Parameters: map[string][]string{
			"commands": {shellCmd},
		},
	}

	var commandID string
	if ssmClient != nil {
		out, err := ssmClient.SendCommand(ctx, input)
		if err != nil {
			return fmt.Errorf("send command: %w", err)
		}
		commandID = awssdk.ToString(out.Command.CommandId)

		// Poll using injected client
		return pollSSMCommandAPI(ctx, ssmClient, commandID, instanceID, successMarker, name)
	}

	if ssmClientReal == nil {
		return fmt.Errorf("no SSM client available")
	}
	out, err := ssmClientReal.SendCommand(ctx, input)
	if err != nil {
		return fmt.Errorf("send command: %w", err)
	}
	commandID = awssdk.ToString(out.Command.CommandId)
	return pollSSMCommand(ctx, ssmClientReal, commandID, instanceID, successMarker, name)
}

// pollSSMCommandAPI polls an SSM command using the SSMSendAPI interface (for testing).
func pollSSMCommandAPI(ctx context.Context, client SSMSendAPI, commandID, instanceID, successMarker, name string) error {
	for i := 0; i < 30; i++ {
		time.Sleep(2 * time.Second)
		fmt.Print(".")

		inv, err := client.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
			CommandId:  awssdk.String(commandID),
			InstanceId: awssdk.String(instanceID),
		})
		if err != nil {
			continue
		}

		status := string(inv.Status)
		if status == string(ssmtypes.CommandInvocationStatusSuccess) {
			output := awssdk.ToString(inv.StandardOutputContent)
			fmt.Println(" done")
			if strings.Contains(output, successMarker) {
				fmt.Printf("  ok: %s\n", name)
			} else {
				fmt.Printf("  warning: %s\n", strings.TrimSpace(output))
			}
			return nil
		}
		if status == string(ssmtypes.CommandInvocationStatusFailed) ||
			status == string(ssmtypes.CommandInvocationStatusCancelled) ||
			status == string(ssmtypes.CommandInvocationStatusTimedOut) {
			fmt.Println()
			stderr := awssdk.ToString(inv.StandardErrorContent)
			return fmt.Errorf("command %s: %s", status, stderr)
		}
	}
	fmt.Println()
	return fmt.Errorf("timed out waiting for command")
}

// downloadWorkspaceToClone sends an SSM command to the clone to fetch staging artifact.
func downloadWorkspaceToClone(ctx context.Context, cfg *config.Config, cloneID, stagingKey string, ssmClient SSMSendAPI) error {
	// Resolve clone instance ID
	var cloneFetcher SandboxFetcher
	if ssmClient == nil {
		awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		tableName := cfg.SandboxTableName
		if tableName == "" {
			tableName = "km-sandboxes"
		}
		cloneFetcher = newRealFetcher(awsCfg, cfg.StateBucket, tableName)
	} else {
		// In tests, skip post-provision workspace download
		return nil
	}

	cloneRec, err := cloneFetcher.FetchSandbox(ctx, cloneID)
	if err != nil {
		return fmt.Errorf("fetch clone metadata: %w", err)
	}

	instanceID, err := extractResourceID(cloneRec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find clone instance ID: %w", err)
	}

	// Wait for the sandbox user to exist (userdata creates it after boot).
	// The instance may report "running" in DynamoDB before userdata finishes.
	downloadCmd := fmt.Sprintf(
		`for i in $(seq 1 30); do id sandbox >/dev/null 2>&1 && break; sleep 2; done && `+
			`aws s3 cp "s3://%s/%s" /tmp/km-workspace.tar.gz && `+
			`tar xzf /tmp/km-workspace.tar.gz -C / && `+
			`chown -R sandbox:sandbox /workspace && `+
			`chown -R sandbox:sandbox /home/sandbox && `+
			`rm -f /tmp/km-workspace.tar.gz && `+
			`echo CLONE_DOWNLOAD_OK`,
		cfg.ArtifactsBucket, stagingKey,
	)

	awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	realSSM := ssm.NewFromConfig(awsCfg)
	return sendSSMCommand(ctx, nil, realSSM, instanceID, downloadCmd, "CLONE_DOWNLOAD_OK", "workspace download")
}

// resolveCloneID attempts to find the new clone's sandbox ID using alias resolution.
func resolveCloneID(ctx context.Context, cfg *config.Config, cloneAlias string, ssmClient SSMSendAPI) (string, error) {
	if cloneAlias == "" {
		return "", fmt.Errorf("cannot resolve clone ID without alias")
	}
	if ssmClient != nil {
		// Test mode
		return "sb-testclone", nil
	}
	return ResolveSandboxID(ctx, cfg, cloneAlias)
}

// updateClonedFromByID sets the cloned_from field on a clone's DynamoDB metadata
// using the sandbox ID directly (no alias resolution needed).
func updateClonedFromByID(ctx context.Context, cfg *config.Config, cloneID, sourceID string, ssmClient SSMSendAPI) error {
	if ssmClient != nil {
		return nil
	}
	if cloneID == "" {
		return nil
	}

	awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	tableName := cfg.SandboxTableName
	if tableName == "" {
		tableName = "km-sandboxes"
	}
	dynamoClient := dynamodb.NewFromConfig(awsCfg)

	// Use UpdateItem to atomically set cloned_from without overwriting the full record.
	// This avoids a race with the remote create-handler Lambda which also writes metadata.
	_, err = dynamoClient.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: &tableName,
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: cloneID},
		},
		UpdateExpression: awssdk.String("SET cloned_from = :cf"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":cf": &dynamodbtypes.AttributeValueMemberS{Value: sourceID},
		},
	})
	return err
}

// waitForCloneRunning polls for the clone's alias to appear in DynamoDB and for its
// status to reach "running". Returns the resolved sandbox ID. This handles the async
// nature of remote create where the Lambda hasn't written metadata yet.
func waitForCloneRunning(ctx context.Context, cfg *config.Config, cloneAlias string, ssmClient SSMSendAPI, timeout time.Duration) (string, error) {
	if ssmClient != nil {
		// Test mode — assume running immediately
		return "sb-testclone", nil
	}

	awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return "", fmt.Errorf("load AWS config: %w", err)
	}
	tableName := cfg.SandboxTableName
	if tableName == "" {
		tableName = "km-sandboxes"
	}
	dynamoClient := dynamodb.NewFromConfig(awsCfg)

	deadline := time.Now().Add(timeout)
	pollInterval := 10 * time.Second

	for {
		// Try to resolve alias to sandbox ID
		cloneID, resolveErr := ResolveSandboxID(ctx, cfg, cloneAlias)
		if resolveErr == nil && cloneID != "" {
			// Alias resolved — now check status
			meta, readErr := awspkg.ReadSandboxMetadataDynamo(ctx, dynamoClient, tableName, cloneID)
			if readErr == nil && meta != nil {
				status := meta.Status
				if status == "" {
					status = "running" // backward compat
				}
				if status == "running" {
					return cloneID, nil
				}
				if status == "failed" || status == "killed" || status == "reaped" {
					return cloneID, fmt.Errorf("sandbox reached terminal status: %s", status)
				}
			}
		}

		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out after %s waiting for clone to reach running status", timeout)
		}

		fmt.Print(".")
		time.Sleep(pollInterval)
	}
}

// cleanupStagingKey deletes the staging S3 object (best-effort, logged on failure).
func cleanupStagingKey(ctx context.Context, cfg *config.Config, sourceID, stagingKey string, ssmClient SSMSendAPI) {
	if ssmClient != nil {
		// Test mode — skip
		return
	}
	awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return
	}
	s3Client := s3.NewFromConfig(awsCfg)
	_, _ = s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: awssdk.String(cfg.ArtifactsBucket),
		Key:    awssdk.String(stagingKey),
	})
}

// BuildWorkspaceStagingCmd constructs the SSM shell command to tar /workspace and
// upload to S3. It also handles optional extra paths and emits success/empty markers.
//
// Always includes /workspace. Additional paths are appended from extraPaths.
// Uses 'cd /' so all paths are absolute from root.
//
// Exported so tests can verify the command structure.
func BuildWorkspaceStagingCmd(extraPaths []string, bucket, stagingKey string) string {
	// Build the list of paths to include (workspace is always first)
	// We use conditional inclusion: if /workspace exists, tar it; otherwise emit CLONE_STAGE_EMPTY
	var pathList strings.Builder
	pathList.WriteString("workspace")
	for _, p := range extraPaths {
		if p != "" {
			// Strip leading slash if present (we're already at /)
			cleaned := strings.TrimPrefix(p, "/")
			pathList.WriteString(" ")
			pathList.WriteString(cleaned)
		}
	}

	return fmt.Sprintf(
		`cd / && `+
			`if [ -d workspace ]; then `+
			`PATHS="%s" && `+
			`tar czf /tmp/km-clone-workspace.tar.gz $PATHS 2>/dev/null; `+
			`if [ $? -eq 0 ]; then `+
			`aws s3 cp /tmp/km-clone-workspace.tar.gz "s3://%s/%s" && `+
			`rm -f /tmp/km-clone-workspace.tar.gz && `+
			`echo CLONE_STAGE_OK; `+
			`else `+
			`echo CLONE_STAGE_EMPTY: tar failed; `+
			`fi; `+
			`else `+
			`echo CLONE_STAGE_EMPTY: workspace not found; `+
			`fi`,
		pathList.String(), bucket, stagingKey,
	)
}

// GenerateCloneAliases returns the list of aliases for count clones.
// For count=1: returns [alias] (no suffix).
// For count>1: returns [alias-1, alias-2, ..., alias-N].
// Empty alias with count=1 returns [""].
//
// Exported so tests can verify alias generation.
func GenerateCloneAliases(baseAlias string, count int) []string {
	aliases := make([]string, count)
	if count == 1 {
		aliases[0] = baseAlias
		return aliases
	}
	for i := 0; i < count; i++ {
		aliases[i] = fmt.Sprintf("%s-%d", baseAlias, i+1)
	}
	return aliases
}
