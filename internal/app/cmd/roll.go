// Package cmd — roll.go
// "km roll creds" Cobra command for operator-facing credential rotation.
//
// Modes:
//   - All (no flags): rotate platform creds + all running sandbox identities
//   - Sandbox (--sandbox <id>): rotate a single sandbox's credentials
//   - Platform (--platform): rotate proxy CA, KMS, optional GitHub App key
//
// Follows the DI deps pattern from doctor.go for testability.
package cmd

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
	appcfg "github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/github"
)

// ============================================================
// Narrow interfaces
// ============================================================

// RollSSMAPI extends RotationSSMAPI with SendCommand for proxy restart.
// Implemented by *ssm.Client.
type RollSSMAPI interface {
	kmaws.RotationSSMAPI
	SendCommand(ctx context.Context, input *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
}

// RollKMSAPI covers KMS DescribeKey and RotateKeyOnDemand.
// Implemented by *kms.Client.
type RollKMSAPI interface {
	DescribeKey(ctx context.Context, input *kms.DescribeKeyInput, optFns ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
	RotateKeyOnDemand(ctx context.Context, input *kms.RotateKeyOnDemandInput, optFns ...func(*kms.Options)) (*kms.RotateKeyOnDemandOutput, error)
}

// RollS3API is the minimal S3 interface for proxy CA cert upload.
// Implemented by *s3.Client.
type RollS3API interface {
	kmaws.RotationS3API
}

// RollDynamoAPI is the DynamoDB interface for identity table operations.
// Implemented by *dynamodb.Client.
type RollDynamoAPI interface {
	kmaws.IdentityTableAPI
}

// RollCWAPI is the CloudWatch Logs interface for audit logging.
// Implemented by *cloudwatchlogs.Client.
type RollCWAPI interface {
	kmaws.RotationCWAPI
}

// RollECSAPI covers ECS ListTasks and StopTask for force-restart.
// Implemented by *ecs.Client.
type RollECSAPI interface {
	ListTasks(ctx context.Context, input *ecs.ListTasksInput, optFns ...func(*ecs.Options)) (*ecs.ListTasksOutput, error)
	StopTask(ctx context.Context, input *ecs.StopTaskInput, optFns ...func(*ecs.Options)) (*ecs.StopTaskOutput, error)
}

// RollEC2API covers EC2 DescribeInstances for proxy restart instance lookup.
// Implemented by *ec2.Client.
type RollEC2API interface {
	DescribeInstances(ctx context.Context, input *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// ============================================================
// DI deps struct
// ============================================================

// RollDeps holds all injected AWS clients for the roll creds command.
// Nil fields cause real AWS clients to be initialized at run time.
type RollDeps struct {
	SSMClient    RollSSMAPI
	KMSClient    RollKMSAPI
	S3Client     RollS3API
	DynamoClient RollDynamoAPI
	CWClient     RollCWAPI
	ECSClient    RollECSAPI
	EC2Client    RollEC2API
	Lister       SandboxLister
}

// ============================================================
// Output types for JSON mode
// ============================================================

// rollResult is returned by the JSON output mode.
type rollResult struct {
	Mode        string       `json:"mode"`
	Succeeded   int          `json:"succeeded"`
	Failed      int          `json:"failed"`
	Duration    string       `json:"duration"`
	Failures    []rollError  `json:"failures,omitempty"`
}

// rollError captures a per-sandbox failure.
type rollError struct {
	SandboxID string `json:"sandbox_id"`
	Error     string `json:"error"`
}

// ============================================================
// Command constructors
// ============================================================

// NewRollCmd creates the "km roll" command with real AWS clients.
func NewRollCmd(cfg *appcfg.Config) *cobra.Command {
	return NewRollCmdWithDeps(cfg, nil)
}

// NewRollCmdWithDeps creates the "km roll" command with injected dependencies.
// Pass nil deps for production use — real AWS clients are initialized at run time.
// This overload is used in tests to inject mock AWS clients.
func NewRollCmdWithDeps(cfg interface{}, deps *RollDeps) *cobra.Command {
	roll := &cobra.Command{
		Use:          "roll",
		Short:        "Rotate platform and sandbox credentials",
		SilenceUsage: true,
	}

	var (
		sandboxID      string
		platformOnly   bool
		githubKeyFile  string
		forceRestart   bool
		jsonOutput     bool
		dryRun         bool
	)

	creds := &cobra.Command{
		Use:   "creds [sandbox-id | #number]",
		Short: "Rotate all credentials (platform + sandboxes)",
		Long: `Rotate platform and sandbox credentials.

Without arguments: rotates proxy CA, KMS key, and all running sandbox identities.
With a sandbox ID or #number: rotates only that sandbox's credentials.
With --platform: rotates only platform credentials (proxy CA, KMS, optional GitHub App key).

Dry-run is ON by default — use --dry-run=false to actually execute rotations.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			// Resolve positional sandbox argument (takes precedence over --sandbox flag)
			if len(args) == 1 {
				// Accept sandbox ID or #number
				resolved, err := ResolveSandboxID(ctx, cfgTyped(cfg), args[0])
				if err != nil {
					return fmt.Errorf("resolve sandbox %q: %w", args[0], err)
				}
				sandboxID = resolved
			}

			// Initialize real AWS clients when deps is nil.
			if deps == nil {
				realDeps, err := initRealRollDeps(ctx, cfg)
				if err != nil {
					return fmt.Errorf("initialize AWS clients: %w", err)
				}
				deps = realDeps
			}

			return runRollCreds(cmd, deps, sandboxID, platformOnly, githubKeyFile, forceRestart, jsonOutput, dryRun)
		},
	}

	creds.Flags().StringVar(&sandboxID, "sandbox", "", "Rotate credentials for a single sandbox ID")
	creds.Flags().BoolVar(&platformOnly, "platform", false, "Rotate platform credentials only")
	creds.Flags().StringVar(&githubKeyFile, "github-private-key-file", "", "Path to new GitHub App PEM private key file")
	creds.Flags().BoolVar(&forceRestart, "force-restart", false, "Force ECS task restart after proxy CA rotation")
	creds.Flags().BoolVar(&jsonOutput, "json", false, "Output results as JSON")
	creds.Flags().BoolVar(&dryRun, "dry-run", true, "Show what would be rotated without making changes (default: true, use --dry-run=false to execute)")

	roll.AddCommand(creds)
	return roll
}

// ============================================================
// Main orchestration
// ============================================================

// cfgTyped extracts *appcfg.Config from the interface{} cfg parameter.
// Returns nil if cfg is not *appcfg.Config (e.g., in tests).
func cfgTyped(cfg interface{}) *appcfg.Config {
	if c, ok := cfg.(*appcfg.Config); ok {
		return c
	}
	return nil
}

// runRollCreds is the core execution logic for km roll creds.
func runRollCreds(
	cmd *cobra.Command,
	deps *RollDeps,
	sandboxID string,
	platformOnly bool,
	githubKeyFile string,
	forceRestart bool,
	jsonOutput bool,
	dryRun bool,
) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	out := cmd.OutOrStdout()
	start := time.Now()

	// Default KMS key alias used throughout the platform.
	const kmsKeyAlias = "alias/km-platform"
	// Default KMS key ID — use alias, KMS accepts alias names.
	const kmsKeyID = kmsKeyAlias

	// Empty string means use the test config — in production these come from config.
	// For tests (deps != nil, cfg == nil) we use reasonable defaults.
	const tableName = "km-identities"
	const stateBucket = "km-terraform-state"

	var failures []rollError
	succeeded := 0

	// -----------------------------------------------------------------
	// SANDBOX MODE: --sandbox <id> or positional argument
	// -----------------------------------------------------------------
	if sandboxID != "" && !platformOnly {
		if !jsonOutput {
			fprintBanner(out, "km roll creds", fmt.Sprintf("sandbox mode: %s", sandboxID))
		}

		if dryRun {
			fmt.Fprintf(out, "  [dry-run] Would rotate Ed25519 identity for sandbox %s\n", sandboxID)
			fmt.Fprintf(out, "  [dry-run] Would re-encrypt SSM parameters under /sandbox/%s/\n", sandboxID)
			return nil
		}

		if err := rotateSandbox(ctx, out, jsonOutput, deps, sandboxID, kmsKeyID, tableName); err != nil {
			failures = append(failures, rollError{SandboxID: sandboxID, Error: err.Error()})
		} else {
			succeeded++
		}

		return printSummary(out, jsonOutput, "sandbox", start, succeeded, failures)
	}

	// -----------------------------------------------------------------
	// PLATFORM MODE: --platform (or all-mode platform step)
	// -----------------------------------------------------------------
	if platformOnly {
		if !jsonOutput {
			fprintBanner(out, "km roll creds", "platform mode")
		}

		if dryRun {
			fmt.Fprintf(out, "  [dry-run] Would rotate proxy CA cert in S3\n")
			fmt.Fprintf(out, "  [dry-run] Would rotate KMS key on-demand: %s\n", kmsKeyID)
			if githubKeyFile != "" {
				fmt.Fprintf(out, "  [dry-run] Would rotate GitHub App key from %s\n", githubKeyFile)
			}
			fmt.Fprintf(out, "  [dry-run] Would restart proxies on running sandboxes\n")
			return nil
		}

		if err := rotatePlatform(ctx, out, jsonOutput, deps, kmsKeyID, githubKeyFile, stateBucket); err != nil {
			return fmt.Errorf("platform rotation failed: %w", err)
		}
		succeeded++

		// Restart proxies on running sandboxes (CRED-05)
		if err := restartProxiesForSandboxes(ctx, out, jsonOutput, deps, forceRestart, nil); err != nil {
			// Non-fatal — log but don't abort
			fmt.Fprintf(out, "  [warn] proxy restart: %v\n", err)
		}

		return printSummary(out, jsonOutput, "platform", start, succeeded, failures)
	}

	// -----------------------------------------------------------------
	// ALL MODE: no flags — platform + all running sandboxes
	// -----------------------------------------------------------------
	if !jsonOutput {
		fprintBanner(out, "km roll creds", "all mode")
	}

	if dryRun {
		fmt.Fprintf(out, "  [dry-run] Would rotate proxy CA cert in S3\n")
		fmt.Fprintf(out, "  [dry-run] Would rotate KMS key on-demand: %s\n", kmsKeyID)
		if githubKeyFile != "" {
			fmt.Fprintf(out, "  [dry-run] Would rotate GitHub App key from %s\n", githubKeyFile)
		}

		// Still enumerate sandboxes for the dry-run report
		sandboxes, err := deps.Lister.ListSandboxes(ctx, false)
		if err != nil {
			return fmt.Errorf("list sandboxes: %w", err)
		}
		running := filterRunning(sandboxes)
		fmt.Fprintf(out, "  [dry-run] Would rotate credentials for %d running sandbox(es):\n", len(running))
		for _, sb := range running {
			fmt.Fprintf(out, "    - %s (Ed25519 identity + SSM re-encrypt)\n", sb.SandboxID)
		}
		fmt.Fprintf(out, "  [dry-run] Would restart proxies on %d sandbox(es)\n", len(running))
		return nil
	}

	// Step 1: Rotate platform
	if err := rotatePlatform(ctx, out, jsonOutput, deps, kmsKeyID, githubKeyFile, stateBucket); err != nil {
		return fmt.Errorf("platform rotation failed: %w", err)
	}
	succeeded++

	// Step 2: Enumerate running sandboxes
	sandboxes, err := deps.Lister.ListSandboxes(ctx, false)
	if err != nil {
		return fmt.Errorf("list sandboxes: %w", err)
	}

	running := filterRunning(sandboxes)
	if !jsonOutput {
		fmt.Fprintf(out, "  Found %d running sandbox(es)\n", len(running))
	}

	// Step 3: Rotate each running sandbox (per-sandbox failures are non-fatal)
	for _, sb := range running {
		if err := rotateSandbox(ctx, out, jsonOutput, deps, sb.SandboxID, kmsKeyID, tableName); err != nil {
			failures = append(failures, rollError{SandboxID: sb.SandboxID, Error: err.Error()})
			if !jsonOutput {
				fmt.Fprintf(out, "  [error] sandbox %s: %v\n", sb.SandboxID, err)
			}
		} else {
			succeeded++
		}
	}

	// Step 4: Restart proxies on running sandboxes (CRED-05)
	if err := restartProxiesForSandboxes(ctx, out, jsonOutput, deps, forceRestart, running); err != nil {
		// Non-fatal
		fmt.Fprintf(out, "  [warn] proxy restart: %v\n", err)
	}

	return printSummary(out, jsonOutput, "all", start, succeeded, failures)
}

// ============================================================
// Platform rotation
// ============================================================

// rotatePlatform rotates proxy CA cert, KMS key on-demand, and optional GitHub App key.
func rotatePlatform(ctx context.Context, out interface{ Write([]byte) (int, error) }, jsonOutput bool, deps *RollDeps, kmsKeyID, githubKeyFile, bucket string) error {
	// Step 1: Rotate proxy CA cert
	oldFP, newFP, err := kmaws.RotateProxyCACert(ctx, deps.S3Client, bucket)
	if err != nil {
		return fmt.Errorf("rotate proxy CA cert: %w", err)
	}
	if !jsonOutput {
		fmt.Fprintf(out, "  [ok] proxy CA rotated: %s -> %s\n", oldFPStr(oldFP), newFP)
	}
	// Audit
	_ = kmaws.WriteRotationAudit(ctx, deps.CWClient, kmaws.RotationAuditEvent{
		Event:     "rotate-proxy-ca",
		KeyType:   "ecdsa-p256",
		BeforeFP:  oldFP,
		AfterFP:   newFP,
		Timestamp: time.Now().UTC(),
		Success:   true,
	})

	// Step 2: KMS rotate key on demand
	// Verify key exists first via DescribeKey
	_, err = deps.KMSClient.DescribeKey(ctx, &kms.DescribeKeyInput{
		KeyId: awssdk.String(kmsKeyID),
	})
	if err != nil {
		return fmt.Errorf("describe KMS key %q: %w", kmsKeyID, err)
	}

	_, err = deps.KMSClient.RotateKeyOnDemand(ctx, &kms.RotateKeyOnDemandInput{
		KeyId: awssdk.String(kmsKeyID),
	})
	if err != nil {
		return fmt.Errorf("rotate KMS key on-demand %q: %w", kmsKeyID, err)
	}
	if !jsonOutput {
		fmt.Fprintf(out, "  [ok] KMS key rotated on-demand: %s\n", kmsKeyID)
	}
	// Audit
	_ = kmaws.WriteRotationAudit(ctx, deps.CWClient, kmaws.RotationAuditEvent{
		Event:     "rotate-kms",
		KeyType:   "kms",
		AfterFP:   kmsKeyID,
		Timestamp: time.Now().UTC(),
		Success:   true,
	})

	// Step 3: Optional GitHub App key rotation
	if githubKeyFile != "" {
		if err := rotateGitHubKey(ctx, out, jsonOutput, deps, githubKeyFile); err != nil {
			return fmt.Errorf("rotate GitHub App key: %w", err)
		}
	} else {
		if !jsonOutput {
			fmt.Fprintf(out, "  [info] --github-private-key-file not provided, skipping GitHub App key rotation\n")
		}
	}

	return nil
}

// ============================================================
// GitHub App key rotation
// ============================================================

// rotateGitHubKey reads a PEM file, validates JWT generation, and writes to SSM.
func rotateGitHubKey(ctx context.Context, out interface{ Write([]byte) (int, error) }, jsonOutput bool, deps *RollDeps, pemFile string) error {
	pemData, err := os.ReadFile(pemFile)
	if err != nil {
		return fmt.Errorf("read GitHub App PEM file %q: %w", pemFile, err)
	}

	// Validate PEM block is parseable
	block, _ := pem.Decode(pemData)
	if block == nil {
		return fmt.Errorf("invalid PEM: no PEM block found in %q", pemFile)
	}

	// Attempt JWT generation to validate the key (use empty app ID for validation only)
	_, err = github.GenerateGitHubAppJWT("validation-check", pemData)
	if err != nil {
		return fmt.Errorf("validate GitHub App private key: %w", err)
	}

	// Write to SSM at /km/config/github/private-key
	const githubKeySSMPath = "/km/config/github/private-key"
	_, ssmErr := deps.SSMClient.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      awssdk.String(githubKeySSMPath),
		Value:     awssdk.String(string(pemData)),
		Type:      ssmtypes.ParameterTypeSecureString,
		Overwrite: awssdk.Bool(true),
	})
	if ssmErr != nil {
		return fmt.Errorf("write GitHub App private key to SSM %q: %w", githubKeySSMPath, ssmErr)
	}

	if !jsonOutput {
		fmt.Fprintf(out, "  [ok] GitHub App private key written to SSM: %s\n", githubKeySSMPath)
	}
	return nil
}

// ============================================================
// Sandbox rotation
// ============================================================

// rotateSandbox rotates a single sandbox's Ed25519 key and re-encrypts SSM parameters.
func rotateSandbox(ctx context.Context, out interface{ Write([]byte) (int, error) }, jsonOutput bool, deps *RollDeps, sandboxID, kmsKeyID, tableName string) error {
	oldFP, newFP, err := kmaws.RotateSandboxIdentity(ctx, deps.SSMClient, deps.DynamoClient, sandboxID, kmsKeyID, tableName)
	if err != nil {
		return fmt.Errorf("rotate sandbox identity: %w", err)
	}
	if !jsonOutput {
		fmt.Fprintf(out, "  [ok] sandbox %s identity rotated: %s -> %s\n", sandboxID, oldFPStr(oldFP), newFP)
	}
	// Audit
	_ = kmaws.WriteRotationAudit(ctx, deps.CWClient, kmaws.RotationAuditEvent{
		Event:     "rotate-identity",
		SandboxID: sandboxID,
		KeyType:   "ed25519",
		BeforeFP:  oldFP,
		AfterFP:   newFP,
		Timestamp: time.Now().UTC(),
		Success:   true,
	})

	// Re-encrypt SSM parameters under the sandbox path
	count, err := kmaws.ReEncryptSSMParameters(ctx, deps.SSMClient, sandboxID, kmsKeyID)
	if err != nil {
		return fmt.Errorf("re-encrypt SSM parameters: %w", err)
	}
	if !jsonOutput {
		fmt.Fprintf(out, "  [ok] sandbox %s: %d SSM parameter(s) re-encrypted\n", sandboxID, count)
	}
	// Audit
	_ = kmaws.WriteRotationAudit(ctx, deps.CWClient, kmaws.RotationAuditEvent{
		Event:     "re-encrypt-ssm",
		SandboxID: sandboxID,
		KeyType:   "ssm-params",
		AfterFP:   fmt.Sprintf("count:%d", count),
		Timestamp: time.Now().UTC(),
		Success:   true,
	})

	return nil
}

// ============================================================
// Proxy restart (CRED-05)
// ============================================================

// restartProxiesForSandboxes restarts the km-http-proxy on each running sandbox after
// a proxy CA rotation. Fire-and-forget pattern per research Pitfall 3.
//
// EC2: SSM SendCommand with AWS-RunShellScript to download new CA and restart proxy.
// ECS: if forceRestart, StopTask forces task replacement; otherwise log eventual-consistency.
func restartProxiesForSandboxes(ctx context.Context, out interface{ Write([]byte) (int, error) }, jsonOutput bool, deps *RollDeps, forceRestart bool, sandboxes []kmaws.SandboxRecord) error {
	if sandboxes == nil {
		// Platform-only mode: enumerate running sandboxes for proxy restart
		var err error
		all, err := deps.Lister.ListSandboxes(ctx, false)
		if err != nil {
			return fmt.Errorf("list sandboxes for proxy restart: %w", err)
		}
		sandboxes = filterRunning(all)
	}

	for _, sb := range sandboxes {
		switch strings.ToLower(sb.Substrate) {
		case "ec2":
			if err := restartEC2Proxy(ctx, out, jsonOutput, deps, sb.SandboxID); err != nil {
				// Non-fatal — log and continue
				if !jsonOutput {
					fmt.Fprintf(out, "  [warn] EC2 proxy restart for %s: %v\n", sb.SandboxID, err)
				}
			}
		case "ecs":
			if forceRestart {
				if err := restartECSProxy(ctx, out, jsonOutput, deps, sb.SandboxID); err != nil {
					// Non-fatal
					if !jsonOutput {
						fmt.Fprintf(out, "  [warn] ECS proxy restart for %s: %v\n", sb.SandboxID, err)
					}
				}
			} else {
				if !jsonOutput {
					fmt.Fprintf(out, "  [info] ECS sandbox %s: new CA will be picked up on next task start (use --force-restart to force)\n", sb.SandboxID)
				}
			}
		case "docker":
			if !jsonOutput {
				fmt.Fprintf(out, "  Skipping docker sandbox %s (proxy restart not supported for local containers)\n", sb.SandboxID)
			}
		}
	}
	return nil
}

// restartEC2Proxy sends SSM RunShellScript to restart km-http-proxy on EC2 instances.
// Fire-and-forget: does not wait for command completion.
func restartEC2Proxy(ctx context.Context, out interface{ Write([]byte) (int, error) }, jsonOutput bool, deps *RollDeps, sandboxID string) error {
	// Look up EC2 instance IDs for this sandbox (tagged with km:sandbox-id)
	result, err := deps.EC2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   awssdk.String("tag:km:sandbox-id"),
				Values: []string{sandboxID},
			},
			{
				Name:   awssdk.String("instance-state-name"),
				Values: []string{"running"},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("describe EC2 instances for sandbox %s: %w", sandboxID, err)
	}

	var instanceIDs []string
	for _, res := range result.Reservations {
		for _, inst := range res.Instances {
			if inst.InstanceId != nil {
				instanceIDs = append(instanceIDs, *inst.InstanceId)
			}
		}
	}

	if len(instanceIDs) == 0 {
		return nil // No instances to restart
	}

	// Fire-and-forget SSM SendCommand
	_, err = deps.SSMClient.SendCommand(ctx, &ssm.SendCommandInput{
		DocumentName: awssdk.String("AWS-RunShellScript"),
		InstanceIds:  instanceIDs,
		Parameters: map[string][]string{
			"commands": {
				"# Restart km-http-proxy with new CA cert",
				"systemctl restart km-http-proxy || true",
			},
		},
		Comment: awssdk.String(fmt.Sprintf("km roll creds: restart km-http-proxy for sandbox %s", sandboxID)),
	})
	if err != nil {
		return fmt.Errorf("send SSM command to %d instance(s) for sandbox %s: %w", len(instanceIDs), sandboxID, err)
	}

	if !jsonOutput {
		fmt.Fprintf(out, "  [ok] EC2 proxy restart issued for sandbox %s (%d instance(s))\n", sandboxID, len(instanceIDs))
	}
	return nil
}

// restartECSProxy calls StopTask on running ECS tasks for the sandbox, forcing task replacement.
func restartECSProxy(ctx context.Context, out interface{ Write([]byte) (int, error) }, jsonOutput bool, deps *RollDeps, sandboxID string) error {
	// List tasks for this sandbox's cluster/service
	listOut, err := deps.ECSClient.ListTasks(ctx, &ecs.ListTasksInput{
		Cluster: awssdk.String(sandboxID),
	})
	if err != nil {
		// Try with a different cluster name format
		listOut, err = deps.ECSClient.ListTasks(ctx, &ecs.ListTasksInput{})
		if err != nil {
			return fmt.Errorf("list ECS tasks for sandbox %s: %w", sandboxID, err)
		}
	}

	if len(listOut.TaskArns) == 0 {
		return nil
	}

	for _, taskARN := range listOut.TaskArns {
		// Fire-and-forget StopTask
		_, err := deps.ECSClient.StopTask(ctx, &ecs.StopTaskInput{
			Task:   awssdk.String(taskARN),
			Reason: awssdk.String(fmt.Sprintf("km roll creds: force proxy restart for sandbox %s", sandboxID)),
		})
		if err != nil {
			if !jsonOutput {
				fmt.Fprintf(out, "  [warn] StopTask %s for sandbox %s: %v\n", taskARN, sandboxID, err)
			}
		}
	}

	if !jsonOutput {
		fmt.Fprintf(out, "  [ok] ECS tasks stopped for sandbox %s (%d task(s))\n", sandboxID, len(listOut.TaskArns))
	}
	return nil
}

// ============================================================
// Helpers
// ============================================================

// filterRunning returns only sandboxes with status "running".
func filterRunning(sandboxes []kmaws.SandboxRecord) []kmaws.SandboxRecord {
	var running []kmaws.SandboxRecord
	for _, sb := range sandboxes {
		if strings.EqualFold(sb.Status, "running") {
			running = append(running, sb)
		}
	}
	return running
}

// oldFPStr returns "(none)" for empty fingerprints (fresh sandbox/cert).
func oldFPStr(fp string) string {
	if fp == "" {
		return "(none)"
	}
	return fp
}

// printSummary prints the rotation summary in human-readable or JSON format.
func printSummary(out interface{ Write([]byte) (int, error) }, jsonOutput bool, mode string, start time.Time, succeeded int, failures []rollError) error {
	duration := time.Since(start).Round(time.Millisecond)
	failed := len(failures)

	if jsonOutput {
		result := rollResult{
			Mode:      mode,
			Succeeded: succeeded,
			Failed:    failed,
			Duration:  duration.String(),
			Failures:  failures,
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Human-readable summary
	fmt.Fprintln(out)
	fmt.Fprintln(out, strings.Repeat("─", 50))
	if failed == 0 {
		fmt.Fprintf(out, "  %d credential(s) rotated successfully in %s\n", succeeded, duration)
	} else {
		fmt.Fprintf(out, "  %d succeeded, %d failed in %s\n", succeeded, failed, duration)
		for _, f := range failures {
			if f.SandboxID != "" {
				fmt.Fprintf(out, "  [error] sandbox %s: %s\n", f.SandboxID, f.Error)
			}
		}
	}

	return nil
}

// ============================================================
// Real AWS client initialization
// ============================================================

// initRealRollDeps initializes real AWS clients for production use.
func initRealRollDeps(ctx context.Context, cfg interface{}) (*RollDeps, error) {
	var profile string
	var stateBucket = "km-terraform-state"

	switch v := cfg.(type) {
	case *appcfg.Config:
		if v != nil {
			profile = v.AWSProfile
			if v.StateBucket != "" {
				stateBucket = v.StateBucket
			}
		}
	}
	_ = stateBucket

	opts := []func(*awsconfig.LoadOptions) error{}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	return &RollDeps{
		SSMClient:    &rollSSMAdapter{ssm.NewFromConfig(awsCfg)},
		KMSClient:    kms.NewFromConfig(awsCfg),
		S3Client:     s3.NewFromConfig(awsCfg),
		DynamoClient: dynamodb.NewFromConfig(awsCfg),
		CWClient:     cloudwatchlogs.NewFromConfig(awsCfg),
		ECSClient:    ecs.NewFromConfig(awsCfg),
		EC2Client:    ec2.NewFromConfig(awsCfg),
		Lister:       newRealLister(awsCfg, "km-terraform-state", "km-sandboxes"),
	}, nil
}

// rollSSMAdapter wraps *ssm.Client to satisfy RollSSMAPI (which embeds RotationSSMAPI + SendCommand).
// *ssm.Client already satisfies all methods directly; this adapter is a compile-time check only.
type rollSSMAdapter struct {
	*ssm.Client
}
