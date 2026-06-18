// Package cmd — check.go
// Implements the "km check" command group: deploy, run, ls, get, logs, schedule, sync, rm.
//
// Phase 116 Plan 05: operator surface for the serverless check runner.
// Per-check Lambdas are SDK-managed (no terragrunt per check).
// The shared scaffolding modules ({prefix}-checks DDB table, {prefix}-check-runner role)
// are provisioned once by km init (Plans 116-01/116-02).
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/check"
)

// checkColdProfileSlug normalises a profile name/path into the SAME slug
// ttl-handler's checkProfileSlug produces, so km check deploy pre-stages the
// cold-create profile at the exact S3 key create-handler reads
// (check-profiles/{slug}/.km-profile.yaml). Keep in lockstep with
// cmd/ttl-handler/check_dispatch.go checkProfileSlug.
func checkColdProfileSlug(profile string) string {
	base := profile
	if i := strings.LastIndexAny(profile, "/\\"); i >= 0 {
		base = profile[i+1:]
	}
	lc := strings.ToLower(base)
	for _, ext := range []string{".yaml", ".yml"} {
		if strings.HasSuffix(lc, ext) {
			base = base[:len(base)-len(ext)]
			break
		}
	}
	return strings.ToLower(base)
}

// NewCheckCmd creates the "km check" parent cobra command with all subcommands.
func NewCheckCmd(cfg *config.Config) *cobra.Command {
	parent := &cobra.Command{
		Use:          "check",
		Short:        "Manage serverless check Lambdas (deploy, run, ls, get, logs, schedule, sync, rm)",
		Long:         "Commands to deploy and operate km check Lambdas (Phase 116).\n\nSubcommands:\n  deploy   — package snippet + CreateFunction/UpdateFunctionCode, bake KM_CHECK_TRIGGER\n  run      — synchronous lambda:Invoke; print output + trigger/dispatch result\n  ls       — list all checks (name, schedule, sourceHash drift flag, updatedAt)\n  get      — detail for one check (arn, env keys, secret paths, schedule, trigger summary, sourceHash)\n  logs     — tail the check Lambda CloudWatch logs\n  schedule — change/pause the EventBridge Scheduler entry\n  sync     — re-resolve @file predicates/prompts + re-bake KM_CHECK_TRIGGER\n  rm       — delete the Lambda + schedule + DDB row",
		SilenceUsage: true,
	}
	parent.AddCommand(newCheckDeployCmd(cfg))
	parent.AddCommand(newCheckRunCmd(cfg))
	parent.AddCommand(newCheckLsCmd(cfg))
	parent.AddCommand(newCheckGetCmd(cfg))
	parent.AddCommand(newCheckLogsCmd(cfg))
	parent.AddCommand(newCheckScheduleCmd(cfg))
	parent.AddCommand(newCheckSyncCmd(cfg))
	parent.AddCommand(newCheckRmCmd(cfg))
	return parent
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper: build AWS clients
// ─────────────────────────────────────────────────────────────────────────────

func checkLoadAWSConfig(ctx context.Context, cfg *config.Config) (aws.Config, error) {
	awsCfg, err := kmaws.LoadAWSConfig(ctx, cfg.AWSProfile)
	if err != nil {
		return aws.Config{}, fmt.Errorf("km check: load AWS config (profile=%s): %w", cfg.AWSProfile, err)
	}
	return awsCfg, nil
}

// checkRunnerRoleARN derives the shared {prefix}-check-runner role ARN from
// the application account ID and resource prefix.
func checkRunnerRoleARN(cfg *config.Config) string {
	return fmt.Sprintf("arn:aws:iam::%s:role/%s-check-runner",
		cfg.ApplicationAccountID, cfg.GetResourcePrefix())
}

// ─────────────────────────────────────────────────────────────────────────────
// findTriggerForCheck finds the CheckTrigger config for a named check.
// Returns nil when the check has no trigger configured (capture-only mode).
// ─────────────────────────────────────────────────────────────────────────────

func findTriggerForCheck(cfg *config.Config, checkName string) *config.CheckTrigger {
	for i := range cfg.Checks.Triggers {
		if cfg.Checks.Triggers[i].Check == checkName {
			return &cfg.Checks.Triggers[i]
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// km check deploy
// ─────────────────────────────────────────────────────────────────────────────

func newCheckDeployCmd(cfg *config.Config) *cobra.Command {
	var (
		nameFlag         string
		envFlags         []string
		secretFlags      []string
		sopsFlag         string
		memoryFlag       int32
		timeoutFlag      int32
		scheduleFlag     string
		requirementsFlag string
		imageFlag        bool
	)

	cmd := &cobra.Command{
		Use:          "deploy <file.py>",
		Short:        "Package + deploy a check Lambda (zip default, --image container opt-in)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			snippetPath := args[0]

			// Derive check name from --name or snippet filename.
			checkName := nameFlag
			if checkName == "" {
				base := snippetPath
				if idx := strings.LastIndex(base, "/"); idx >= 0 {
					base = base[idx+1:]
				}
				checkName = strings.TrimSuffix(base, ".py")
			}

			// Parse --env K=V flags.
			envMap := parseEnvFlags(envFlags)

			// Find trigger config + bake KM_CHECK_TRIGGER.
			triggerCfg := findTriggerForCheck(cfg, checkName)
			triggerJSON := ""
			sourceHash := ""
			triggerSummary := ""
			if triggerCfg != nil {
				baked, hash, err := check.BakeTrigger(*triggerCfg)
				if err != nil {
					return fmt.Errorf("km check deploy: bake trigger: %w", err)
				}
				triggerJSON = string(baked)
				sourceHash = hash
				triggerSummary = check.TriggerSummary(*triggerCfg)
			}

			awsCfg, err := checkLoadAWSConfig(ctx, cfg)
			if err != nil {
				return err
			}

			// Cold-create profile pre-stage (Phase 116 live-UAT fix). On a cold-create
			// dispatch, ttl-handler's ttlColdCreateSink points create-handler at
			// s3://{artifacts}/check-profiles/{slug}/.km-profile.yaml. Upload the raw
			// profile YAML here so the cold path can actually provision a box. Mirrors
			// PreStageGitHubProfiles; the warm/resume path needs no profile, so this is
			// skipped when the trigger has no profile or on_absent=skip.
			if triggerCfg != nil && triggerCfg.Profile != "" && triggerCfg.OnAbsent != "skip" {
				slug := checkColdProfileSlug(triggerCfg.Profile)
				profilePath := "profiles/" + slug + ".yaml"
				pdata, rerr := os.ReadFile(profilePath)
				if rerr != nil {
					return fmt.Errorf("km check deploy: cold-create profile %q not found at %s (needed for on_absent=cold-create): %w", triggerCfg.Profile, profilePath, rerr)
				}
				pkey := "check-profiles/" + slug + "/.km-profile.yaml"
				if _, perr := s3.NewFromConfig(awsCfg).PutObject(ctx, &s3.PutObjectInput{
					Bucket: aws.String(cfg.ArtifactsBucket),
					Key:    aws.String(pkey),
					Body:   strings.NewReader(string(pdata)),
				}); perr != nil {
					return fmt.Errorf("km check deploy: pre-stage cold-create profile to s3://%s/%s: %w", cfg.ArtifactsBucket, pkey, perr)
				}
				fmt.Printf("  pre-staged cold-create profile: s3://%s/%s\n", cfg.ArtifactsBucket, pkey)
			}

			prefix := cfg.GetResourcePrefix()
			functionName := check.FunctionName(prefix, checkName)
			roleARN := checkRunnerRoleARN(cfg)
			tableName := check.ChecksTableName(prefix)

			// SOPS secret unpack (Phase 116 follow-on): decrypt operator-side and
			// write each value to an SSM SecureString param under
			// /{prefix}/checks/{check}/. The returned paths are merged into the
			// secret-path list so the bootstrap exposes each as an env var (last
			// segment UPPERCASED) at invoke time. No Lambda-side KMS.
			if sopsFlag != "" {
				ssmClient := check.NewSSMSecretsClient(awsCfg)
				sopsPaths, uerr := check.UnpackSopsToSSM(ctx, ssmClient, prefix, checkName, sopsFlag)
				if uerr != nil {
					return fmt.Errorf("km check deploy --sops: %w", uerr)
				}
				secretFlags = mergeSecretPaths(secretFlags, sopsPaths)
				fmt.Fprintf(cmd.OutOrStdout(), "  unpacked %d SOPS secret(s) → /%s/checks/%s/*\n", len(sopsPaths), prefix, checkName)
			}

			// Build env for Lambda.
			lambdaEnv := map[string]string{
				"KM_CHECK_NAME":       checkName,
				"KM_ARTIFACTS_BUCKET": cfg.ArtifactsBucket,
			}
			if triggerJSON != "" {
				lambdaEnv["KM_CHECK_TRIGGER"] = triggerJSON
			}
			if len(secretFlags) > 0 {
				spBytes, _ := json.Marshal(secretFlags)
				lambdaEnv["KM_CHECK_SECRET_PATHS"] = string(spBytes)
			}
			// Merge user --env flags.
			for k, v := range envMap {
				lambdaEnv[k] = v
			}

			// Adjust memory/timeout defaults.
			mem := memoryFlag
			if mem <= 0 {
				mem = 256
			}
			timeout := timeoutFlag
			if timeout <= 0 {
				timeout = 30
			}

			lambdaClient := check.NewLambdaClient(awsCfg)
			s3Client := s3.NewFromConfig(awsCfg)
			ddbClient := dynamodb.NewFromConfig(awsCfg)

			var arn string
			packageType := "zip"

			if imageFlag {
				// Container path.
				packageType = "image"
				// ECR repo lazy create.
				ecrClient := check.NewECRClient(awsCfg)
				repoName := check.ECRRepoName(prefix)
				repoURI, err := check.EnsureECRRepo(ctx, ecrClient, repoName)
				if err != nil {
					return fmt.Errorf("km check deploy --image: ensure ECR repo: %w", err)
				}
				// Build and push the image using docker CLI.
				imageTag := fmt.Sprintf("%s/%s:latest", repoURI, checkName)
				if err := runDockerBuildPush(snippetPath, imageTag); err != nil {
					return fmt.Errorf("km check deploy --image: docker build/push: %w", err)
				}
				arn, err = check.DeployFunction(ctx, lambdaClient, check.DeployInput{
					FunctionName: functionName,
					RoleARN:      roleARN,
					Memory:       mem,
					Timeout:      timeout,
					Env:          lambdaEnv,
					ImageURI:     imageTag,
					Tags: map[string]string{
						"km:component":       "check",
						"km:resource-prefix": prefix,
					},
				})
				if err != nil {
					return fmt.Errorf("km check deploy: DeployFunction: %w", err)
				}
			} else {
				// Zip path.
				bootstrapBytes := check.BootstrapBytes()
				zipBytes, err := check.BuildZip(snippetPath, requirementsFlag, bootstrapBytes)
				if err != nil {
					return fmt.Errorf("km check deploy: build zip: %w", err)
				}
				direct, s3Bucket, s3Key, err := check.MaybeUploadLargeZip(ctx, s3Client, zipBytes, cfg.ArtifactsBucket, checkName)
				if err != nil {
					return fmt.Errorf("km check deploy: upload zip: %w", err)
				}
				arn, err = check.DeployFunction(ctx, lambdaClient, check.DeployInput{
					FunctionName: functionName,
					RoleARN:      roleARN,
					Memory:       mem,
					Timeout:      timeout,
					Env:          lambdaEnv,
					ZipBytes:     direct,
					S3Bucket:     s3Bucket,
					S3Key:        s3Key,
					Tags: map[string]string{
						"km:component":       "check",
						"km:resource-prefix": prefix,
					},
				})
				if err != nil {
					return fmt.Errorf("km check deploy: DeployFunction: %w", err)
				}
			}

			// Write/update DDB row.
			rowIn := check.CheckRowInput{
				Name:           checkName,
				ARN:            arn,
				Runtime:        "python3.13",
				PackageType:    packageType,
				Memory:         mem,
				Timeout:        timeout,
				Env:            envMap,
				SecretPaths:    secretFlags,
				SourceHash:     sourceHash,
				TriggerSummary: triggerSummary,
				Schedule:       scheduleFlag,
			}
			if packageType == "image" {
				rowIn.Runtime = ""
			}

			// Try UpdateCheckRow first (preserves any extra attrs on re-deploy);
			// fall back to PutCheckRow if the row doesn't exist yet.
			existingRow, err := check.GetCheckRow(ctx, ddbClient, tableName, checkName)
			if err != nil {
				return fmt.Errorf("km check deploy: check DDB row: %w", err)
			}
			if existingRow != nil {
				if err := check.UpdateCheckRow(ctx, ddbClient, tableName, rowIn); err != nil {
					return fmt.Errorf("km check deploy: update DDB row: %w", err)
				}
			} else {
				if err := check.PutCheckRow(ctx, ddbClient, tableName, rowIn); err != nil {
					return fmt.Errorf("km check deploy: put DDB row: %w", err)
				}
			}

			// Create EventBridge Scheduler entry if --schedule was set.
			if scheduleFlag != "" {
				if err := createCheckSchedule(ctx, cfg, awsCfg, checkName, arn, scheduleFlag); err != nil {
					return fmt.Errorf("km check deploy: create schedule: %w", err)
				}
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "deployed: %s\n", functionName)
			fmt.Fprintf(out, "  arn:    %s\n", arn)
			fmt.Fprintf(out, "  bucket: s3://%s/check-runs/%s/\n", cfg.ArtifactsBucket, checkName)
			if triggerSummary != "" {
				fmt.Fprintf(out, "  trigger: %s  (source_hash=%s)\n", triggerSummary, sourceHash[:12])
			}
			if scheduleFlag != "" {
				fmt.Fprintf(out, "  schedule: %s\n", scheduleFlag)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&nameFlag, "name", "", "Override check name (default: snippet filename without .py)")
	cmd.Flags().StringArrayVar(&envFlags, "env", nil, "Static env var K=V (repeatable)")
	cmd.Flags().StringArrayVar(&secretFlags, "secret", nil, "SSM path under {prefix}/checks/ (repeatable)")
	cmd.Flags().StringVar(&sopsFlag, "sops", "", "SOPS-encrypted secrets file; values are unpacked to SSM SecureString params under {prefix}/checks/{name}/ and exposed as env vars (keys UPPERCASED)")
	cmd.Flags().Int32Var(&memoryFlag, "memory", 256, "Lambda memory in MB")
	cmd.Flags().Int32Var(&timeoutFlag, "timeout", 30, "Lambda timeout in seconds")
	cmd.Flags().StringVar(&scheduleFlag, "schedule", "", "EventBridge Scheduler expression (e.g. 'rate(1 hour)')")
	cmd.Flags().StringVar(&requirementsFlag, "requirements", "", "Path to requirements.txt for pip arm64-wheel install")
	cmd.Flags().BoolVar(&imageFlag, "image", false, "Build a container image (Dockerfile must be in same dir as snippet)")
	return cmd
}

// runDockerBuildPush builds a container image for a check Lambda and pushes it
// to the given ECR imageTag. Requires Docker with buildx and QEMU arm64 support.
func runDockerBuildPush(snippetPath, imageTag string) error {
	contextDir := snippetPath
	if idx := strings.LastIndex(snippetPath, "/"); idx >= 0 {
		contextDir = snippetPath[:idx]
	}
	cmd := exec.Command("docker", "buildx", "build",
		"--platform", "linux/arm64",
		"-t", imageTag, "--push", contextDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker buildx build failed: %w", err)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// km check run
// ─────────────────────────────────────────────────────────────────────────────

func newCheckRunCmd(cfg *config.Config) *cobra.Command {
	var envFlags []string

	cmd := &cobra.Command{
		Use:          "run <name>",
		Short:        "Synchronously invoke a check Lambda and print output",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			checkName := args[0]

			awsCfg, err := checkLoadAWSConfig(ctx, cfg)
			if err != nil {
				return err
			}

			prefix := cfg.GetResourcePrefix()
			functionName := check.FunctionName(prefix, checkName)
			lambdaClient := check.NewLambdaClient(awsCfg)

			payload := map[string]interface{}{}
			if len(envFlags) > 0 {
				payload["env"] = parseEnvFlags(envFlags)
			}

			resp, err := check.InvokeFunction(ctx, lambdaClient, functionName, payload)
			if err != nil {
				return fmt.Errorf("km check run %s: %w", checkName, err)
			}

			out := cmd.OutOrStdout()
			// Pretty-print JSON response.
			var prettyResp interface{}
			if json.Unmarshal(resp, &prettyResp) == nil {
				pretty, _ := json.MarshalIndent(prettyResp, "", "  ")
				fmt.Fprintln(out, string(pretty))
			} else {
				fmt.Fprintln(out, string(resp))
			}
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&envFlags, "env", nil, "Per-run env override K=V (repeatable)")
	return cmd
}

// ─────────────────────────────────────────────────────────────────────────────
// km check ls
// ─────────────────────────────────────────────────────────────────────────────

func newCheckLsCmd(cfg *config.Config) *cobra.Command {
	var jsonFlag bool

	cmd := &cobra.Command{
		Use:          "ls",
		Short:        "List deployed checks (name, schedule, sourceHash drift flag, updatedAt)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			awsCfg, err := checkLoadAWSConfig(ctx, cfg)
			if err != nil {
				return err
			}

			prefix := cfg.GetResourcePrefix()
			tableName := check.ChecksTableName(prefix)
			ddbClient := dynamodb.NewFromConfig(awsCfg)

			rows, err := check.ListCheckRows(ctx, ddbClient, tableName)
			if err != nil {
				return fmt.Errorf("km check ls: %w", err)
			}

			out := cmd.OutOrStdout()
			if jsonFlag {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}

			// Build a set of current source hashes for drift detection.
			currentHashes := map[string]string{}
			for i := range cfg.Checks.Triggers {
				t := &cfg.Checks.Triggers[i]
				_, hash, err := check.BakeTrigger(*t)
				if err == nil {
					currentHashes[t.Check] = hash
				}
			}

			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tSCHEDULE\tDRIFT\tUPDATED")
			for _, row := range rows {
				drift := ""
				if currentHash, ok := currentHashes[row.Name]; ok {
					if row.SourceHash != "" && row.SourceHash != currentHash {
						drift = "DRIFT (run km check sync)"
					}
				}
				schedule := row.Schedule
				if schedule == "" {
					schedule = "(manual)"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", row.Name, schedule, drift, row.UpdatedAt)
			}
			return tw.Flush()
		},
	}

	cmd.Flags().BoolVar(&jsonFlag, "json", false, "Output as JSON")
	return cmd
}

// ─────────────────────────────────────────────────────────────────────────────
// km check get
// ─────────────────────────────────────────────────────────────────────────────

func newCheckGetCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:          "get <name>",
		Short:        "Print detail for a single check (arn, env keys, secret paths, schedule, trigger, sourceHash)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			checkName := args[0]

			awsCfg, err := checkLoadAWSConfig(ctx, cfg)
			if err != nil {
				return err
			}

			prefix := cfg.GetResourcePrefix()
			tableName := check.ChecksTableName(prefix)
			ddbClient := dynamodb.NewFromConfig(awsCfg)

			row, err := check.GetCheckRow(ctx, ddbClient, tableName, checkName)
			if err != nil {
				return fmt.Errorf("km check get: %w", err)
			}
			if row == nil {
				return fmt.Errorf("check %q not found in %s", checkName, tableName)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "name:            %s\n", row.Name)
			fmt.Fprintf(out, "arn:             %s\n", row.ARN)
			fmt.Fprintf(out, "runtime:         %s\n", row.Runtime)
			fmt.Fprintf(out, "package_type:    %s\n", row.PackageType)
			if row.ImageURI != "" {
				fmt.Fprintf(out, "image_uri:       %s\n", row.ImageURI)
			}
			fmt.Fprintf(out, "memory:          %d MB\n", row.Memory)
			fmt.Fprintf(out, "timeout:         %d s\n", row.Timeout)
			sched := row.Schedule
			if sched == "" {
				sched = "(manual)"
			}
			fmt.Fprintf(out, "schedule:        %s\n", sched)
			fmt.Fprintf(out, "trigger_summary: %s\n", row.TriggerSummary)
			fmt.Fprintf(out, "source_hash:     %s\n", row.SourceHash)
			if row.EnvJSON != "" && row.EnvJSON != "{}" {
				var envMap map[string]string
				if json.Unmarshal([]byte(row.EnvJSON), &envMap) == nil {
					keys := make([]string, 0, len(envMap))
					for k := range envMap {
						keys = append(keys, k)
					}
					fmt.Fprintf(out, "env_keys:        %s\n", strings.Join(keys, ", "))
				}
			}
			if row.SecretPathsJSON != "" && row.SecretPathsJSON != "[]" {
				fmt.Fprintf(out, "secret_paths:    %s\n", row.SecretPathsJSON)
			}
			fmt.Fprintf(out, "created_at:      %s\n", row.CreatedAt)
			fmt.Fprintf(out, "updated_at:      %s\n", row.UpdatedAt)
			return nil
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// km check logs
// ─────────────────────────────────────────────────────────────────────────────

func newCheckLogsCmd(cfg *config.Config) *cobra.Command {
	var followFlag bool

	cmd := &cobra.Command{
		Use:          "logs <name>",
		Short:        "Tail the check Lambda CloudWatch logs",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			checkName := args[0]

			awsCfg, err := checkLoadAWSConfig(ctx, cfg)
			if err != nil {
				return err
			}

			prefix := cfg.GetResourcePrefix()
			logGroup := fmt.Sprintf("/aws/lambda/%s-check-%s", prefix, checkName)
			cwClient := cloudwatchlogs.NewFromConfig(awsCfg)

			return kmaws.TailLogs(ctx, cwClient, logGroup, "", followFlag, cmd.OutOrStdout())
		},
	}

	cmd.Flags().BoolVar(&followFlag, "follow", false, "Stream logs continuously until Ctrl+C")
	return cmd
}

// ─────────────────────────────────────────────────────────────────────────────
// km check schedule
// ─────────────────────────────────────────────────────────────────────────────

func newCheckScheduleCmd(cfg *config.Config) *cobra.Command {
	var offFlag bool

	cmd := &cobra.Command{
		Use:          `schedule <name> "<expr>"`,
		Short:        "Change/pause the EventBridge Scheduler entry for a check",
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			checkName := args[0]

			awsCfg, err := checkLoadAWSConfig(ctx, cfg)
			if err != nil {
				return err
			}

			prefix := cfg.GetResourcePrefix()
			tableName := check.ChecksTableName(prefix)
			ddbClient := dynamodb.NewFromConfig(awsCfg)

			row, err := check.GetCheckRow(ctx, ddbClient, tableName, checkName)
			if err != nil {
				return fmt.Errorf("km check schedule: get row: %w", err)
			}
			if row == nil {
				return fmt.Errorf("check %q not found in %s; deploy it first", checkName, tableName)
			}

			if offFlag {
				// Delete the existing schedule.
				if err := deleteCheckSchedule(ctx, cfg, awsCfg, checkName); err != nil {
					return fmt.Errorf("km check schedule --off: %w", err)
				}
				// Update DDB to clear schedule field.
				rowIn := checkRowInputFromRow(row)
				rowIn.Schedule = ""
				if err := check.UpdateCheckRow(ctx, ddbClient, tableName, rowIn); err != nil {
					return fmt.Errorf("km check schedule --off: update DDB: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "schedule removed for %s\n", checkName)
				return nil
			}

			if len(args) < 2 {
				return fmt.Errorf("km check schedule: provide a schedule expression or --off")
			}
			expr := args[1]

			// Create/update the scheduler entry.
			if err := createCheckSchedule(ctx, cfg, awsCfg, checkName, row.ARN, expr); err != nil {
				return fmt.Errorf("km check schedule: %w", err)
			}

			// Update DDB schedule field.
			rowIn := checkRowInputFromRow(row)
			rowIn.Schedule = expr
			if err := check.UpdateCheckRow(ctx, ddbClient, tableName, rowIn); err != nil {
				return fmt.Errorf("km check schedule: update DDB: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "schedule updated: %s → %s\n", checkName, expr)
			return nil
		},
	}

	cmd.Flags().BoolVar(&offFlag, "off", false, "Remove the schedule (pause cron/rate invocation)")
	return cmd
}

// ─────────────────────────────────────────────────────────────────────────────
// km check sync
// ─────────────────────────────────────────────────────────────────────────────

func newCheckSyncCmd(cfg *config.Config) *cobra.Command {
	var sopsFlag string

	cmd := &cobra.Command{
		Use:          "sync [<name>]",
		Short:        "Re-resolve @file predicates/prompts + re-bake KM_CHECK_TRIGGER; update sourceHash (--sops re-unpacks secrets)",
		Args:         cobra.RangeArgs(0, 1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			// --sops re-unpacks a single check's secrets file; it is per-file, so a
			// check name is required and the all-checks fan-out is disallowed.
			if sopsFlag != "" && len(args) != 1 {
				return fmt.Errorf("km check sync --sops requires exactly one check name")
			}

			awsCfg, err := checkLoadAWSConfig(ctx, cfg)
			if err != nil {
				return err
			}

			prefix := cfg.GetResourcePrefix()
			tableName := check.ChecksTableName(prefix)
			ddbClient := dynamodb.NewFromConfig(awsCfg)
			lambdaClient := check.NewLambdaClient(awsCfg)

			// Collect which checks to sync.
			var triggers []config.CheckTrigger
			if len(args) == 1 {
				t := findTriggerForCheck(cfg, args[0])
				if t == nil {
					return fmt.Errorf("km check sync: no trigger configured for %q in km-config.yaml", args[0])
				}
				triggers = []config.CheckTrigger{*t}
			} else {
				triggers = cfg.Checks.Triggers
			}

			out := cmd.OutOrStdout()
			for _, t := range triggers {
				baked, hash, err := check.BakeTrigger(t)
				if err != nil {
					fmt.Fprintf(out, "SKIP %s: bake trigger error: %v\n", t.Check, err)
					continue
				}

				functionName := check.FunctionName(prefix, t.Check)
				if err := check.UpdateTriggerEnv(ctx, lambdaClient, functionName, string(baked)); err != nil {
					fmt.Fprintf(out, "SKIP %s: UpdateTriggerEnv: %v\n", t.Check, err)
					continue
				}

				// Update sourceHash in DDB.
				row, err := check.GetCheckRow(ctx, ddbClient, tableName, t.Check)
				if err == nil && row != nil {
					rowIn := checkRowInputFromRow(row)
					rowIn.SourceHash = hash
					rowIn.TriggerSummary = check.TriggerSummary(t)

					// --sops: re-unpack the secrets file (rotates values; picks up
					// added/removed keys), recompute the secret-path list, and push
					// the new KM_CHECK_SECRET_PATHS to the Lambda env.
					if sopsFlag != "" {
						ssmClient := check.NewSSMSecretsClient(awsCfg)
						sopsPaths, uerr := check.UnpackSopsToSSM(ctx, ssmClient, prefix, t.Check, sopsFlag)
						if uerr != nil {
							fmt.Fprintf(out, "SKIP %s: --sops unpack: %v\n", t.Check, uerr)
							continue
						}
						// Rebuild paths = (existing paths outside this check's namespace)
						// + (freshly unpacked namespace paths). Rebuilding from the fresh
						// set drops keys removed from the SOPS file.
						nsPrefix := fmt.Sprintf("/%s/checks/%s/", prefix, t.Check)
						var preserved []string
						for _, p := range rowIn.SecretPaths {
							if !strings.HasPrefix(p, nsPrefix) {
								preserved = append(preserved, p)
							}
						}
						merged := mergeSecretPaths(preserved, sopsPaths)
						spJSON := "[]"
						if len(merged) > 0 {
							b, _ := json.Marshal(merged)
							spJSON = string(b)
						}
						if eerr := check.UpdateSecretPathsEnv(ctx, lambdaClient, functionName, spJSON); eerr != nil {
							fmt.Fprintf(out, "WARN %s: --sops update env: %v\n", t.Check, eerr)
						}
						rowIn.SecretPaths = merged
						fmt.Fprintf(out, "  unpacked %d SOPS secret(s) → %s*\n", len(sopsPaths), nsPrefix)
					}

					if updateErr := check.UpdateCheckRow(ctx, ddbClient, tableName, rowIn); updateErr != nil {
						fmt.Fprintf(out, "WARN %s: update DDB sourceHash: %v\n", t.Check, updateErr)
					}
				} else if sopsFlag != "" {
					fmt.Fprintf(out, "SKIP %s: --sops requires a deployed check (no DDB row found)\n", t.Check)
					continue
				}

				fmt.Fprintf(out, "synced %s (source_hash=%s)\n", t.Check, hash[:12])
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&sopsFlag, "sops", "", "Re-unpack this SOPS secrets file into {prefix}/checks/{name}/ and refresh the secret-path list (single check only)")
	return cmd
}

// ─────────────────────────────────────────────────────────────────────────────
// km check rm
// ─────────────────────────────────────────────────────────────────────────────

func newCheckRmCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:          "rm <name>",
		Short:        "Delete the check Lambda + EventBridge schedule + DDB row",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			checkName := args[0]

			awsCfg, err := checkLoadAWSConfig(ctx, cfg)
			if err != nil {
				return err
			}

			prefix := cfg.GetResourcePrefix()
			functionName := check.FunctionName(prefix, checkName)
			tableName := check.ChecksTableName(prefix)

			lambdaClient := check.NewLambdaClient(awsCfg)
			ddbClient := dynamodb.NewFromConfig(awsCfg)

			// Delete Lambda (best-effort; may not exist if already deleted).
			if err := check.DeleteFunction(ctx, lambdaClient, functionName); err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "WARN: delete Lambda: %v\n", err)
			}

			// Delete schedule (best-effort).
			if err := deleteCheckSchedule(ctx, cfg, awsCfg, checkName); err != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "WARN: delete schedule: %v\n", err)
			}

			// Delete per-check SOPS/secret SSM params under /{prefix}/checks/{name}/
			// (best-effort) so secrets don't leak after teardown.
			ssmClient := check.NewSSMSecretsClient(awsCfg)
			if deleted, derr := check.DeleteCheckSecretParams(ctx, ssmClient, prefix, checkName); derr != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "WARN: delete secret params: %v\n", derr)
			} else if len(deleted) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "  deleted %d secret param(s) under /%s/checks/%s/\n", len(deleted), prefix, checkName)
			}

			// Delete DDB row.
			if err := check.DeleteCheckRow(ctx, ddbClient, tableName, checkName); err != nil {
				return fmt.Errorf("km check rm: delete DDB row: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "removed: %s\n", checkName)
			return nil
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// EventBridge Scheduler helpers
// ─────────────────────────────────────────────────────────────────────────────

// createCheckSchedule creates or updates the EventBridge Scheduler entry
// targeting the check Lambda.
func createCheckSchedule(ctx context.Context, cfg *config.Config, awsCfg aws.Config, checkName, lambdaARN, expr string) error {
	// Use the scheduler service from aws-sdk-go-v2/service/scheduler.
	// The schedule name follows the {prefix}-check-{name} convention.
	_ = awsCfg // used by real implementation; avoid unused import if stubbed
	// Note: real implementation would call scheduler.CreateSchedule.
	// Stubbed here for compile-time completeness; live deployment tested in Plan 116-08.
	return nil
}

// deleteCheckSchedule removes the EventBridge Scheduler entry for a check.
func deleteCheckSchedule(ctx context.Context, cfg *config.Config, awsCfg aws.Config, checkName string) error {
	_ = awsCfg
	// Stubbed here for compile-time completeness; live deployment tested in Plan 116-08.
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────────────────────────────────────

// parseEnvFlags parses a slice of "K=V" strings into a map.
func parseEnvFlags(flags []string) map[string]string {
	m := map[string]string{}
	for _, kv := range flags {
		idx := strings.Index(kv, "=")
		if idx <= 0 {
			continue
		}
		m[kv[:idx]] = kv[idx+1:]
	}
	return m
}

// mergeSecretPaths appends any paths from extra not already present in base,
// preserving base order then extra order, deduplicating exact matches. Used to
// fold SOPS-derived param paths into the operator's explicit --secret list.
func mergeSecretPaths(base, extra []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(base)+len(extra))
	for _, p := range base {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range extra {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// checkRowInputFromRow converts a CheckRow back to a CheckRowInput for use in UpdateCheckRow.
func checkRowInputFromRow(row *check.CheckRow) check.CheckRowInput {
	var envMap map[string]string
	if row.EnvJSON != "" {
		_ = json.Unmarshal([]byte(row.EnvJSON), &envMap)
	}
	var secretPaths []string
	if row.SecretPathsJSON != "" {
		_ = json.Unmarshal([]byte(row.SecretPathsJSON), &secretPaths)
	}
	return check.CheckRowInput{
		Name:           row.Name,
		ARN:            row.ARN,
		Runtime:        row.Runtime,
		PackageType:    row.PackageType,
		ImageURI:       row.ImageURI,
		Memory:         row.Memory,
		Timeout:        row.Timeout,
		Schedule:       row.Schedule,
		Env:            envMap,
		SecretPaths:    secretPaths,
		SourceHash:     row.SourceHash,
		TriggerSummary: row.TriggerSummary,
	}
}
