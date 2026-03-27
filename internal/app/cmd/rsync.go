package cmd

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"sort"
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
	cmd.AddCommand(newRsyncViewCmd(cfg))
	cmd.AddCommand(newRsyncDeleteCmd(cfg))

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
				paths = []string{".claude", ".claude.json", ".bashrc", ".bash_profile", ".gitconfig"}
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

// newRsyncDeleteCmd creates "km rsync delete <name>".
func newRsyncDeleteCmd(cfg *config.Config) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:          "delete <name>",
		Short:        "Delete a saved snapshot",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			name := args[0]
			bucket := cfg.ArtifactsBucket
			if bucket == "" {
				return fmt.Errorf("artifacts_bucket not configured")
			}

			s3Key := fmt.Sprintf("rsync/%s.tar.gz", name)

			awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
			if err != nil {
				return fmt.Errorf("load AWS config: %w", err)
			}
			s3Client := s3.NewFromConfig(awsCfg)

			// Check if snapshot exists
			_, err = s3Client.HeadObject(ctx, &s3.HeadObjectInput{
				Bucket: awssdk.String(bucket),
				Key:    awssdk.String(s3Key),
			})
			if err != nil {
				return fmt.Errorf("snapshot %q not found", name)
			}

			// Confirm unless --yes
			if !yes {
				fmt.Printf("Delete snapshot %q? [y/N] ", name)
				var answer string
				fmt.Scanln(&answer)
				if answer != "y" && answer != "Y" && answer != "yes" {
					fmt.Println("Aborted.")
					return nil
				}
			}

			_, err = s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: awssdk.String(bucket),
				Key:    awssdk.String(s3Key),
			})
			if err != nil {
				return fmt.Errorf("delete snapshot: %w", err)
			}

			fmt.Printf("  Deleted snapshot %q\n", name)
			return nil
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	return cmd
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

// newRsyncViewCmd creates "km rsync view <name>".
func newRsyncViewCmd(cfg *config.Config) *cobra.Command {
	var maxDepth int

	cmd := &cobra.Command{
		Use:          "view <name>",
		Short:        "Show contents of a saved snapshot in tree format",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			name := args[0]

			bucket := cfg.ArtifactsBucket
			if bucket == "" {
				return fmt.Errorf("artifacts_bucket not configured")
			}

			awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
			if err != nil {
				return fmt.Errorf("load AWS config: %w", err)
			}

			s3Client := s3.NewFromConfig(awsCfg)
			s3Key := fmt.Sprintf("rsync/%s.tar.gz", name)

			out, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
				Bucket: awssdk.String(bucket),
				Key:    awssdk.String(s3Key),
			})
			if err != nil {
				return fmt.Errorf("snapshot %q not found: %w", name, err)
			}
			defer out.Body.Close()

			gz, err := gzip.NewReader(out.Body)
			if err != nil {
				return fmt.Errorf("decompress: %w", err)
			}
			defer gz.Close()

			// Collect all entries
			type entry struct {
				path  string
				size  int64
				isDir bool
			}
			var entries []entry
			var totalSize int64

			tr := tar.NewReader(gz)
			for {
				hdr, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("read tar: %w", err)
				}
				entries = append(entries, entry{
					path:  hdr.Name,
					size:  hdr.Size,
					isDir: hdr.Typeflag == tar.TypeDir,
				})
				totalSize += hdr.Size
			}

			printBanner("km rsync view", fmt.Sprintf("%s (%d entries, %s)",
				name, len(entries), formatSize(totalSize)))

			// Build tree from flat paths
			root := &treeNode{name: name, children: make(map[string]*treeNode)}
			for _, e := range entries {
				p := strings.TrimSuffix(e.path, "/")
				parts := strings.Split(p, "/")
				node := root
				for _, part := range parts {
					if part == "" {
						continue
					}
					if node.children[part] == nil {
						node.children[part] = &treeNode{name: part, children: make(map[string]*treeNode)}
					}
					node = node.children[part]
				}
				node.isDir = e.isDir
				node.size = e.size
			}

			printTree(root, "", 0, maxDepth)
			return nil
		},
	}

	var full bool
	cmd.Flags().IntVar(&maxDepth, "depth", 1, "Limit tree depth (default 1)")
	cmd.Flags().BoolVar(&full, "full", false, "Show full tree (no depth limit)")
	cmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		if full {
			maxDepth = 0
		}
		return nil
	}
	return cmd
}

// treeNode represents a file or directory in the snapshot tree.
type treeNode struct {
	name     string
	size     int64
	isDir    bool
	children map[string]*treeNode
}

// printTree renders a tree node and its children in `tree` command format.
// maxDepth of 0 means unlimited; otherwise stops descending at that depth.
func printTree(node *treeNode, prefix string, depth, maxDepth int) {
	names := make([]string, 0, len(node.children))
	for name := range node.children {
		names = append(names, name)
	}
	sort.Strings(names)

	for i, name := range names {
		child := node.children[name]
		isLast := i == len(names)-1

		connector := "├── "
		childPrefix := "│   "
		if isLast {
			connector = "└── "
			childPrefix = "    "
		}

		if child.isDir || len(child.children) > 0 {
			childCount := countDescendants(child)
			if maxDepth > 0 && depth+1 >= maxDepth {
				// At depth limit — show summary
				fmt.Printf("  %s%s%s/ (%d items)\n", prefix, connector, name, childCount)
			} else {
				fmt.Printf("  %s%s%s/\n", prefix, connector, name)
				printTree(child, prefix+childPrefix, depth+1, maxDepth)
			}
		} else {
			fmt.Printf("  %s%s%s (%s)\n", prefix, connector, name, formatSize(child.size))
		}
	}
}

// countDescendants returns the total number of files and dirs under a node.
func countDescendants(node *treeNode) int {
	count := len(node.children)
	for _, child := range node.children {
		count += countDescendants(child)
	}
	return count
}

// formatSize returns a human-readable size string.
func formatSize(bytes int64) string {
	switch {
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%dB", bytes)
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
