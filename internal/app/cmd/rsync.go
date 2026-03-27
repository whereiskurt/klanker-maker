package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// NewRsyncCmd creates the "km rsync" command group.
func NewRsyncCmd(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rsync",
		Short: "Save and restore sandbox user home snapshots",
		Long: `Snapshot and restore files from the sandbox shell user's $HOME.

Paths are configured in km-config.yaml under rsync_paths (default: .claude, .bashrc, .gitconfig).
Paths are relative to the shell user's home directory.`,
	}

	cmd.AddCommand(newRsyncSaveCmd(cfg))
	cmd.AddCommand(newRsyncListCmd(cfg))
	cmd.AddCommand(newRsyncLoadCmd(cfg))

	return cmd
}

// newRsyncSaveCmd creates "km rsync save <sandbox> <name>".
func newRsyncSaveCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:          "save <sandbox-id|#number> <name>",
		Short:        "Save sandbox user's home files to S3",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return fmt.Errorf("resolve sandbox: %w", err)
			}
			name := args[1]

			printBanner("km rsync save", fmt.Sprintf("%s → %s", sandboxID, name))

			awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
			if err != nil {
				return fmt.Errorf("load AWS config: %w", err)
			}

			fetcher := newRealFetcher(awsCfg, cfg.StateBucket)
			rec, err := fetcher.FetchSandbox(ctx, sandboxID)
			if err != nil {
				return fmt.Errorf("fetch sandbox: %w", err)
			}
			instanceID, err := extractResourceID(rec.Resources, ":instance/")
			if err != nil {
				return fmt.Errorf("find instance: %w", err)
			}

			paths := cfg.RsyncPaths
			if len(paths) == 0 {
				paths = []string{".claude", ".bashrc", ".bash_profile", ".gitconfig"}
			}

			bucket := cfg.ArtifactsBucket
			if bucket == "" {
				return fmt.Errorf("artifacts_bucket not configured")
			}
			s3Key := fmt.Sprintf("rsync/%s.tar.gz", name)

			// Find the sandbox user's home and tar the configured paths
			var quotedPaths []string
			for _, p := range paths {
				quotedPaths = append(quotedPaths, fmt.Sprintf("'%s'", p))
			}
			shellCmd := fmt.Sprintf(
				`SHELL_HOME=$(getent passwd sandbox | cut -d: -f6) && `+
					`[ -n "$SHELL_HOME" ] || SHELL_HOME=/home/sandbox && `+
					`cd "$SHELL_HOME" && PATHS="" && `+
					`for p in %s; do [ -e "$p" ] && PATHS="$PATHS $p"; done && `+
					`if [ -n "$PATHS" ]; then `+
					`tar czf /tmp/km-rsync.tar.gz $PATHS && `+
					`aws s3 cp /tmp/km-rsync.tar.gz "s3://%s/%s" && `+
					`echo "RSYNC_OK: $(du -sh /tmp/km-rsync.tar.gz | cut -f1)"; `+
					`else echo "RSYNC_EMPTY: no matching paths found"; fi`,
				strings.Join(quotedPaths, " "), bucket, s3Key,
			)

			ssmClient := ssm.NewFromConfig(awsCfg)
			fmt.Printf("  Paths: %s\n", strings.Join(paths, ", "))
			fmt.Printf("  From:  %s (%s)\n", sandboxID, instanceID)
			fmt.Printf("  To:    s3://%s/%s\n", bucket, s3Key)

			cmdOut, err := ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
				InstanceIds:  []string{instanceID},
				DocumentName: awssdk.String("AWS-RunShellScript"),
				Parameters: map[string][]string{
					"commands": {shellCmd},
				},
			})
			if err != nil {
				return fmt.Errorf("send command: %w", err)
			}
			commandID := awssdk.ToString(cmdOut.Command.CommandId)

			fmt.Printf("  Uploading...")
			return pollSSMCommand(ctx, ssmClient, commandID, instanceID, "RSYNC_OK", name)
		},
	}
}

// newRsyncListCmd creates "km rsync list".
func newRsyncListCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List saved snapshots",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			bucket := cfg.ArtifactsBucket
			if bucket == "" {
				return fmt.Errorf("artifacts_bucket not configured")
			}

			awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
			if err != nil {
				return fmt.Errorf("load AWS config: %w", err)
			}

			s3Client := s3.NewFromConfig(awsCfg)
			out, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
				Bucket: awssdk.String(bucket),
				Prefix: awssdk.String("rsync/"),
			})
			if err != nil {
				return fmt.Errorf("list snapshots: %w", err)
			}

			if len(out.Contents) == 0 {
				fmt.Println("No rsync snapshots found.")
				return nil
			}

			printBanner("km rsync list", fmt.Sprintf("%d snapshot(s)", len(out.Contents)))
			for _, obj := range out.Contents {
				key := awssdk.ToString(obj.Key)
				name := strings.TrimPrefix(key, "rsync/")
				name = strings.TrimSuffix(name, ".tar.gz")
				size := awssdk.ToInt64(obj.Size)
				modified := awssdk.ToTime(obj.LastModified)
				fmt.Printf("  %-20s %6dKB  %s\n", name, size/1024, modified.Local().Format("2006-01-02 3:04 PM"))
			}
			return nil
		},
	}
}

// newRsyncLoadCmd creates "km rsync load <sandbox> <name>".
func newRsyncLoadCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:          "load <sandbox-id|#number> <name>",
		Short:        "Restore a snapshot into a running sandbox",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return fmt.Errorf("resolve sandbox: %w", err)
			}
			name := args[1]

			printBanner("km rsync load", fmt.Sprintf("%s ← %s", sandboxID, name))

			awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
			if err != nil {
				return fmt.Errorf("load AWS config: %w", err)
			}

			fetcher := newRealFetcher(awsCfg, cfg.StateBucket)
			rec, err := fetcher.FetchSandbox(ctx, sandboxID)
			if err != nil {
				return fmt.Errorf("fetch sandbox: %w", err)
			}
			instanceID, err := extractResourceID(rec.Resources, ":instance/")
			if err != nil {
				return fmt.Errorf("find instance: %w", err)
			}

			bucket := cfg.ArtifactsBucket
			if bucket == "" {
				return fmt.Errorf("artifacts_bucket not configured")
			}
			s3Key := fmt.Sprintf("rsync/%s.tar.gz", name)

			shellCmd := fmt.Sprintf(
				`SHELL_HOME=$(getent passwd sandbox | cut -d: -f6) && `+
					`[ -n "$SHELL_HOME" ] || SHELL_HOME=/home/sandbox && `+
					`aws s3 cp "s3://%s/%s" /tmp/km-rsync.tar.gz && `+
					`cd "$SHELL_HOME" && tar xzf /tmp/km-rsync.tar.gz && `+
					`chown -R sandbox:sandbox "$SHELL_HOME" && `+
					`echo "RSYNC_OK: restored"`,
				bucket, s3Key,
			)

			ssmClient := ssm.NewFromConfig(awsCfg)
			fmt.Printf("  Restoring: %s\n", name)
			fmt.Printf("  Into:      %s (%s)\n", sandboxID, instanceID)

			cmdOut, err := ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
				InstanceIds:  []string{instanceID},
				DocumentName: awssdk.String("AWS-RunShellScript"),
				Parameters: map[string][]string{
					"commands": {shellCmd},
				},
			})
			if err != nil {
				return fmt.Errorf("send command: %w", err)
			}
			commandID := awssdk.ToString(cmdOut.Command.CommandId)

			fmt.Printf("  Restoring...")
			return pollSSMCommand(ctx, ssmClient, commandID, instanceID, "RSYNC_OK", name)
		},
	}
}

// pollSSMCommand waits for an SSM command to complete and reports the result.
func pollSSMCommand(ctx context.Context, client *ssm.Client, commandID, instanceID, successMarker, name string) error {
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
		if status == "Success" {
			output := awssdk.ToString(inv.StandardOutputContent)
			fmt.Println(" done")
			if strings.Contains(output, successMarker) {
				fmt.Printf("  ✓ %s\n", name)
			} else {
				fmt.Printf("  ⊘ %s\n", strings.TrimSpace(output))
			}
			return nil
		}
		if status == "Failed" || status == "Cancelled" || status == "TimedOut" {
			fmt.Println()
			stderr := awssdk.ToString(inv.StandardErrorContent)
			return fmt.Errorf("command %s: %s", status, stderr)
		}
	}
	fmt.Println()
	return fmt.Errorf("timed out waiting for command")
}
