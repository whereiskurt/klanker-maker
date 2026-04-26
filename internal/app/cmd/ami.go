// Package cmd — ami.go
// Implements the "km ami" command group: list, delete, bake, copy subcommands.
// Provides BakeFromSandbox and FindProfilesReferencingAMI as exported helpers
// for use by Plan 05 (km shell --learn --ami) and Plan 06 (km doctor checkStaleAMIs).
package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	dynamodbsvc "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	profilepkg "github.com/whereiskurt/klankrmkr/pkg/profile"
	"github.com/whereiskurt/klankrmkr/pkg/version"
)

// NewAMICmd builds the "km ami" parent command with list/delete/bake/copy children.
func NewAMICmd(cfg *config.Config) *cobra.Command {
	return NewAMICmdWithDeps(cfg, nil, nil, nil)
}

// NewAMICmdWithDeps allows DI for tests.
//   - ec2Factory: produces region-specific EC2AMIAPI clients.
//   - fetcher:    SandboxFetcher for "km ami bake <sandbox-id>".
//   - lister:     SandboxLister for "km ami list --unused" to enumerate running sandboxes.
func NewAMICmdWithDeps(cfg *config.Config, ec2Factory func(region string) kmaws.EC2AMIAPI, fetcher SandboxFetcher, lister SandboxLister) *cobra.Command {
	amiCmd := &cobra.Command{
		Use:          "ami",
		Short:        "Manage custom AMIs baked from sandboxes",
		SilenceUsage: true,
	}
	amiCmd.AddCommand(newAMIListCmd(cfg, ec2Factory, lister))
	amiCmd.AddCommand(newAMIDeleteCmd(cfg, ec2Factory))
	amiCmd.AddCommand(newAMIBakeCmd(cfg, ec2Factory, fetcher))
	amiCmd.AddCommand(newAMICopyCmd(cfg, ec2Factory))
	return amiCmd
}

// ============================================================
// km ami list
// ============================================================

func newAMIListCmd(cfg *config.Config, ec2Factory func(region string) kmaws.EC2AMIAPI, lister SandboxLister) *cobra.Command {
	var (
		wide       bool
		profile    string
		ageStr     string
		unused     bool
		region     string
		allRegions bool
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List custom AMIs baked from sandboxes",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			// Parse --age flag if provided.
			var ageDuration time.Duration
			if ageStr != "" {
				var err error
				ageDuration, err = parseAge(ageStr)
				if err != nil {
					return fmt.Errorf("invalid --age value %q: %w", ageStr, err)
				}
			}

			// Determine regions to query.
			targetRegion := region
			if targetRegion == "" {
				targetRegion = amiPrimaryRegion(cfg)
			}

			var regions []string
			if allRegions {
				regions = collectConfiguredRegions(cfg)
			} else {
				regions = []string{targetRegion}
			}

			// Build production EC2 factory if not injected.
			factory := ec2Factory
			if factory == nil {
				factory = buildRealEC2Factory(ctx)
			}

			// Collect images from all regions.
			var allImages []ec2types.Image
			for _, r := range regions {
				client := factory(r)
				if client == nil {
					continue
				}
				imgs, err := kmaws.ListBakedAMIs(ctx, client)
				if err != nil {
					return fmt.Errorf("list AMIs in %s: %w", r, err)
				}
				// Annotate each image's region if not already tagged.
				for i := range imgs {
					imgs[i] = annotateRegion(imgs[i], r)
				}
				allImages = append(allImages, imgs...)
			}

			// Sort newest-first.
			sort.Slice(allImages, func(i, j int) bool {
				ci := awssdk.ToString(allImages[i].CreationDate)
				cj := awssdk.ToString(allImages[j].CreationDate)
				return ci > cj
			})

			// Apply --profile filter.
			if profile != "" {
				allImages = filterByTag(allImages, "km:profile", profile)
			}

			// Apply --age filter: keep images older than the given duration.
			if ageDuration > 0 {
				allImages = filterByAge(allImages, ageDuration)
			}

			// Apply --unused filter.
			if unused {
				realLister := lister
				if realLister == nil {
					awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
					if err != nil {
						return fmt.Errorf("load AWS config: %w", err)
					}
					tableName := cfg.SandboxTableName
					if tableName == "" {
						tableName = "km-sandboxes"
					}
					realLister = newRealLister(awsCfg, cfg.StateBucket, tableName)
				}
				allImages = filterUnused(ctx, cfg, allImages, realLister)
			}

			if len(allImages) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No matching AMIs found.")
				return nil
			}

			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(allImages)
			}

			return printAMITable(cmd.OutOrStdout(), cfg, allImages, wide)
		},
	}

	cmd.Flags().BoolVar(&wide, "wide", false, "Show all columns (region, sandbox-id, snapshots, encrypted, instance type, $/month)")
	cmd.Flags().StringVar(&profile, "profile", "", "Filter by km:profile tag")
	cmd.Flags().StringVar(&ageStr, "age", "", "Filter AMIs older than this duration (e.g. 7d, 168h)")
	cmd.Flags().BoolVar(&unused, "unused", false, "Only show AMIs with no profile reference and no running sandbox")
	cmd.Flags().StringVar(&region, "region", "", "Target a specific region (default: KM_REGION)")
	cmd.Flags().BoolVar(&allRegions, "all-regions", false, "Query all configured regions in parallel")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	return cmd
}

// buildRealEC2Factory returns a production ec2Factory closure.
func buildRealEC2Factory(ctx context.Context) func(region string) kmaws.EC2AMIAPI {
	return func(r string) kmaws.EC2AMIAPI {
		awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if err != nil {
			return nil
		}
		awsCfgCopy := awsCfg.Copy()
		awsCfgCopy.Region = r
		return ec2.NewFromConfig(awsCfgCopy)
	}
}

// annotateRegion attaches a synthetic km:source-region tag to an image if not already tagged.
func annotateRegion(img ec2types.Image, region string) ec2types.Image {
	for _, t := range img.Tags {
		if awssdk.ToString(t.Key) == "km:source-region" {
			return img
		}
	}
	img.Tags = append(img.Tags, ec2types.Tag{
		Key:   awssdk.String("km:source-region"),
		Value: awssdk.String(region),
	})
	return img
}

// filterByTag returns images whose tag with the given key equals value.
func filterByTag(images []ec2types.Image, tagKey, value string) []ec2types.Image {
	var out []ec2types.Image
	for _, img := range images {
		for _, t := range img.Tags {
			if awssdk.ToString(t.Key) == tagKey && awssdk.ToString(t.Value) == value {
				out = append(out, img)
				break
			}
		}
	}
	return out
}

// filterByAge returns images that are older than the given duration.
func filterByAge(images []ec2types.Image, age time.Duration) []ec2types.Image {
	cutoff := time.Now().UTC().Add(-age)
	var out []ec2types.Image
	for _, img := range images {
		if img.CreationDate == nil {
			continue
		}
		created, err := time.Parse(time.RFC3339, awssdk.ToString(img.CreationDate))
		if err != nil {
			continue
		}
		if created.Before(cutoff) {
			out = append(out, img)
		}
	}
	return out
}

// filterUnused returns images that have no profile reference and no running sandbox using them.
func filterUnused(ctx context.Context, cfg *config.Config, images []ec2types.Image, lister SandboxLister) []ec2types.Image {
	var sandboxes []kmaws.SandboxRecord
	if lister != nil {
		recs, err := lister.ListSandboxes(ctx, false)
		if err == nil {
			sandboxes = recs
		}
	}

	var out []ec2types.Image
	for _, img := range images {
		amiID := awssdk.ToString(img.ImageId)

		// Check profile references.
		refs, _ := FindProfilesReferencingAMI(cfg.ProfileSearchPaths, amiID)
		if len(refs) > 0 {
			continue
		}

		// Check running sandboxes.
		inUse := false
		for _, rec := range sandboxes {
			if sandboxUsesAMI(cfg, rec, amiID) {
				inUse = true
				break
			}
		}
		if inUse {
			continue
		}

		out = append(out, img)
	}
	return out
}

// sandboxUsesAMI checks whether a sandbox record references the given AMI via its profile.
func sandboxUsesAMI(cfg *config.Config, rec kmaws.SandboxRecord, amiID string) bool {
	if rec.Profile == "" {
		return false
	}
	for _, dir := range cfg.ProfileSearchPaths {
		expanded := expandAMIPath(dir)
		matches, err := filepath.Glob(filepath.Join(expanded, "*.yaml"))
		if err != nil {
			continue
		}
		for _, path := range matches {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			p, err := profilepkg.Parse(data)
			if err != nil {
				continue
			}
			if p.Metadata.Name == rec.Profile && p.Spec.Runtime.AMI == amiID {
				return true
			}
		}
	}
	return false
}

// tagValue returns the value of a named tag from an image, or "".
func tagValue(img ec2types.Image, key string) string {
	for _, t := range img.Tags {
		if awssdk.ToString(t.Key) == key {
			return awssdk.ToString(t.Value)
		}
	}
	return ""
}

// imageAgeString returns a human-readable age string for an AMI.
func imageAgeString(img ec2types.Image) string {
	if img.CreationDate == nil {
		return "?"
	}
	created, err := time.Parse(time.RFC3339, awssdk.ToString(img.CreationDate))
	if err != nil {
		return "?"
	}
	age := time.Since(created)
	if age < 0 {
		age = 0
	}
	days := int(age.Hours() / 24)
	hours := int(age.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dh", hours)
}

// imageSizeGB returns the total GB across all EBS block device mappings.
func imageSizeGB(img ec2types.Image) int32 {
	var total int32
	for _, bdm := range img.BlockDeviceMappings {
		if bdm.Ebs != nil && bdm.Ebs.VolumeSize != nil {
			total += *bdm.Ebs.VolumeSize
		}
	}
	return total
}

// printAMITable writes the narrow (default) or wide AMI table to w.
func printAMITable(w io.Writer, cfg *config.Config, images []ec2types.Image, wide bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	if wide {
		fmt.Fprintln(tw, "AMI ID\tNAME\tAGE\tSIZE\tPROFILE\tREFS\tREGION\tSANDBOX-ID\tSNAPS\tENCRYPTED\tINSTANCE\t$/MONTH")
	} else {
		fmt.Fprintln(tw, "AMI ID\tNAME\tAGE\tSIZE\tPROFILE\tREFS")
	}

	for _, img := range images {
		amiID := awssdk.ToString(img.ImageId)
		name := awssdk.ToString(img.Name)
		if len(name) > 40 {
			name = name[:37] + "..."
		}
		age := imageAgeString(img)
		sizeGB := imageSizeGB(img)
		sizeStr := fmt.Sprintf("%dGB", sizeGB)
		profileName := tagValue(img, "km:profile")

		refs, _ := FindProfilesReferencingAMI(cfg.ProfileSearchPaths, amiID)
		refCount := len(refs)

		if wide {
			imgRegion := tagValue(img, "km:source-region")
			sandboxID := tagValue(img, "km:sandbox-id")
			snapCount := len(kmaws.SnapshotIDsFromImage(img))
			encrypted := isAMIEncrypted(img)
			instanceType := tagValue(img, "km:instance-type")
			costPerMonth := fmt.Sprintf("$%.2f", float64(sizeGB)*0.05)
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%d\t%v\t%s\t%s\n",
				amiID, name, age, sizeStr, profileName, refCount,
				imgRegion, sandboxID, snapCount, encrypted, instanceType, costPerMonth)
		} else {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\n",
				amiID, name, age, sizeStr, profileName, refCount)
		}
	}
	return tw.Flush()
}

// isAMIEncrypted returns true if any EBS volume in the image is encrypted.
func isAMIEncrypted(img ec2types.Image) bool {
	for _, bdm := range img.BlockDeviceMappings {
		if bdm.Ebs != nil && bdm.Ebs.Encrypted != nil && *bdm.Ebs.Encrypted {
			return true
		}
	}
	return false
}

// collectConfiguredRegions returns the primary region plus any replica region.
func collectConfiguredRegions(cfg *config.Config) []string {
	primary := amiPrimaryRegion(cfg)
	regions := []string{primary}
	if replica := os.Getenv("KM_REPLICA_REGION"); replica != "" && replica != primary {
		regions = append(regions, replica)
	}
	return regions
}

// ============================================================
// km ami delete
// ============================================================

func newAMIDeleteCmd(cfg *config.Config, ec2Factory func(region string) kmaws.EC2AMIAPI) *cobra.Command {
	var (
		force  bool
		yes    bool
		dryRun bool
		region string
	)

	cmd := &cobra.Command{
		Use:          "delete <ami-id>",
		Short:        "Delete an AMI and its associated snapshots",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			amiID := args[0]

			targetRegion := region
			if targetRegion == "" {
				targetRegion = amiPrimaryRegion(cfg)
			}

			factory := ec2Factory
			if factory == nil {
				factory = buildRealEC2Factory(ctx)
			}
			client := factory(targetRegion)
			if client == nil {
				return fmt.Errorf("failed to create EC2 client for region %s", targetRegion)
			}

			// Describe the AMI to confirm existence and get snapshot IDs.
			descOut, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
				ImageIds: []string{amiID},
			})
			if err != nil {
				return fmt.Errorf("describe AMI %s: %w", amiID, err)
			}
			if len(descOut.Images) == 0 {
				return fmt.Errorf("AMI %s not found in region %s", amiID, targetRegion)
			}
			img := descOut.Images[0]
			snapIDs := kmaws.SnapshotIDsFromImage(img)

			// Profile refcount safety check.
			refs, err := FindProfilesReferencingAMI(cfg.ProfileSearchPaths, amiID)
			if err != nil {
				return fmt.Errorf("scan profiles: %w", err)
			}
			if len(refs) > 0 && !force {
				fmt.Fprintf(cmd.OutOrStdout(), "AMI %s is referenced by %d profile(s):\n", amiID, len(refs))
				for _, ref := range refs {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", ref)
				}
				return fmt.Errorf("refusing to delete a referenced AMI; rerun with --force to override")
			}

			// Dry-run preview.
			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "would delete AMI %s + %d snapshot(s): %s\n",
					amiID, len(snapIDs), strings.Join(snapIDs, ", "))
				return nil
			}

			// Confirmation prompt (unless --yes).
			if !yes {
				confirmed, promptErr := amiConfirmPrompt(cmd.InOrStdin(), cmd.OutOrStdout(),
					fmt.Sprintf("Delete AMI %s and %d associated snapshot(s)? [y/N]: ", amiID, len(snapIDs)))
				if promptErr != nil {
					return promptErr
				}
				if !confirmed {
					fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
					return fmt.Errorf("aborted by user")
				}
			}

			// Execute deletion.
			if err := kmaws.DeleteAMI(ctx, client, amiID, false); err != nil {
				return fmt.Errorf("delete AMI %s: %w", amiID, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted AMI %s (and %d snapshot(s))\n", amiID, len(snapIDs))
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Bypass profile refcount safety check")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview what would be deleted without making changes")
	cmd.Flags().StringVar(&region, "region", "", "Region where the AMI resides (default: KM_REGION)")
	return cmd
}

// amiConfirmPrompt prints prompt to out and reads a line from in.
// Returns true if the user typed y, Y, or yes.
func amiConfirmPrompt(in io.Reader, out io.Writer, prompt string) (bool, error) {
	fmt.Fprint(out, prompt)
	scanner := bufio.NewScanner(in)
	if scanner.Scan() {
		answer := strings.TrimSpace(scanner.Text())
		return answer == "y" || answer == "Y" || answer == "yes", nil
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}

// ============================================================
// km ami bake
// ============================================================

func newAMIBakeCmd(cfg *config.Config, ec2Factory func(region string) kmaws.EC2AMIAPI, fetcher SandboxFetcher) *cobra.Command {
	var (
		description string
		waitTimeout string
	)

	cmd := &cobra.Command{
		Use:          "bake <sandbox-id>",
		Short:        "Bake an AMI from a running sandbox",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}

			realFetcher := fetcher
			if realFetcher == nil {
				realFetcher = newRealAMISandboxFetcher(cfg)
			}

			rec, err := realFetcher.FetchSandbox(ctx, sandboxID)
			if err != nil {
				return fmt.Errorf("fetch sandbox: %w", err)
			}

			// Parse optional wait-timeout.
			var timeout time.Duration
			if waitTimeout != "" {
				timeout, err = time.ParseDuration(waitTimeout)
				if err != nil {
					return fmt.Errorf("invalid --wait-timeout %q: %w", waitTimeout, err)
				}
			}

			amiID, err := bakeFromSandboxInternal(ctx, cfg, *rec, sandboxID, rec.Profile, version.Number, description, timeout, ec2Factory)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "baked AMI: %s\n", amiID)
			return nil
		},
	}

	cmd.Flags().StringVar(&description, "description", "", "AMI description")
	cmd.Flags().StringVar(&waitTimeout, "wait-timeout", "15m", "Maximum time to wait for AMI to become available")
	return cmd
}

// ============================================================
// km ami copy
// ============================================================

func newAMICopyCmd(cfg *config.Config, ec2Factory func(region string) kmaws.EC2AMIAPI) *cobra.Command {
	var (
		toRegion    string
		fromRegion  string
		description string
		waitTimeout string
	)

	cmd := &cobra.Command{
		Use:          "copy <ami-id>",
		Short:        "Copy an AMI to another region with re-tagging",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			amiID := args[0]

			if toRegion == "" {
				return fmt.Errorf("--to-region is required")
			}

			srcRegion := fromRegion
			if srcRegion == "" {
				srcRegion = amiPrimaryRegion(cfg)
			}

			var timeout time.Duration
			if waitTimeout != "" {
				var err error
				timeout, err = time.ParseDuration(waitTimeout)
				if err != nil {
					return fmt.Errorf("invalid --wait-timeout %q: %w", waitTimeout, err)
				}
			}
			if timeout == 0 {
				timeout = 15 * time.Minute
			}

			factory := ec2Factory
			if factory == nil {
				factory = buildRealEC2Factory(ctx)
			}

			srcClient := factory(srcRegion)
			dstClient := factory(toRegion)
			if srcClient == nil || dstClient == nil {
				return fmt.Errorf("failed to build EC2 clients")
			}

			// Describe the source AMI to get its name and tags.
			descOut, err := srcClient.DescribeImages(ctx, &ec2.DescribeImagesInput{
				ImageIds: []string{amiID},
			})
			if err != nil {
				return fmt.Errorf("describe source AMI %s: %w", amiID, err)
			}
			if len(descOut.Images) == 0 {
				return fmt.Errorf("AMI %s not found in region %s", amiID, srcRegion)
			}
			srcImg := descOut.Images[0]

			name := awssdk.ToString(srcImg.Name)
			if description == "" {
				description = fmt.Sprintf("Copy of %s from %s", amiID, srcRegion)
			}

			// Re-use source tags on the destination.
			tags := srcImg.Tags

			dstAMIID, err := kmaws.CopyAMI(ctx, srcClient, dstClient, srcRegion, toRegion, amiID, name, description, tags, timeout)
			if err != nil {
				return fmt.Errorf("copy AMI: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "copied AMI %s → %s in %s\n", amiID, dstAMIID, toRegion)
			return nil
		},
	}

	cmd.Flags().StringVar(&toRegion, "to-region", "", "Destination region (required)")
	cmd.Flags().StringVar(&fromRegion, "from-region", "", "Source region (default: KM_REGION)")
	cmd.Flags().StringVar(&description, "description", "", "Description for the copied AMI")
	cmd.Flags().StringVar(&waitTimeout, "wait-timeout", "15m", "Maximum time to wait for copy to complete")
	return cmd
}

// ============================================================
// Exported helpers consumed by Plan 05 and Plan 06
// ============================================================

// BakeFromSandbox bakes an AMI from a running sandbox using its DynamoDB record.
// Returns the AMI ID and any error. Used by `km ami bake` and `km shell --learn --ami` (Plan 05).
// The ec2Factory is nil in production; tests may pass a mock factory.
func BakeFromSandbox(ctx context.Context, cfg *config.Config, rec kmaws.SandboxRecord, sandboxID, profileName, kmVersion string) (string, error) {
	return bakeFromSandboxInternal(ctx, cfg, rec, sandboxID, profileName, kmVersion, "", 0, nil)
}

// bakeFromSandboxInternal is the shared implementation used by BakeFromSandbox and newAMIBakeCmd.
func bakeFromSandboxInternal(ctx context.Context, cfg *config.Config, rec kmaws.SandboxRecord, sandboxID, profileName, kmVersion, description string, waitTimeout time.Duration, ec2Factory func(region string) kmaws.EC2AMIAPI) (string, error) {
	// Validate substrate.
	switch rec.Substrate {
	case "ec2", "ec2spot", "ec2demand":
		// supported
	default:
		return "", fmt.Errorf("bake requires an EC2 substrate sandbox (got %q); only ec2/ec2spot/ec2demand sandboxes can be baked", rec.Substrate)
	}

	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return "", fmt.Errorf("find EC2 instance for sandbox %s: %w", sandboxID, err)
	}

	amiName := kmaws.AMIName(profileName, sandboxID, time.Now())
	tags := kmaws.KMBakeTags(sandboxID, profileName, rec.Alias, "", rec.Region, kmVersion)

	if description == "" {
		description = fmt.Sprintf("km bake of sandbox %s (profile: %s)", sandboxID, profileName)
	}
	if waitTimeout == 0 {
		waitTimeout = 15 * time.Minute
	}

	// Build EC2 client for the sandbox's region.
	var client kmaws.EC2AMIAPI
	if ec2Factory != nil {
		client = ec2Factory(rec.Region)
	} else {
		awsCfg, loadErr := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if loadErr != nil {
			return "", fmt.Errorf("load AWS config: %w", loadErr)
		}
		awsCfgCopy := awsCfg.Copy()
		awsCfgCopy.Region = rec.Region
		client = ec2.NewFromConfig(awsCfgCopy)
	}

	return kmaws.BakeAMI(ctx, client, instanceID, amiName, description, tags, waitTimeout)
}

// FindProfilesReferencingAMI walks all directories in searchPaths recursively
// for *.yaml and *.yml files and returns the file paths of any profile that has
// spec.runtime.ami equal to amiID. Exported so Plan 06's checkStaleAMIs can call
// it cross-package as cmd.FindProfilesReferencingAMI.
func FindProfilesReferencingAMI(searchPaths []string, amiID string) ([]string, error) {
	var refs []string
	for _, dir := range searchPaths {
		expanded := expandAMIPath(dir)
		walkErr := filepath.Walk(expanded, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				// Silently skip inaccessible entries.
				return nil
			}
			if info.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".yaml" && ext != ".yml" {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			p, parseErr := profilepkg.Parse(data)
			if parseErr != nil {
				return nil
			}
			if p.Spec.Runtime.AMI == amiID {
				refs = append(refs, path)
			}
			return nil
		})
		if walkErr != nil {
			// Non-fatal: directory may not exist.
			continue
		}
	}
	return refs, nil
}

// ============================================================
// Internal helpers
// ============================================================

// parseAge parses a duration string that may use Go syntax (e.g. "168h", "30m")
// or the convenience "Nd" notation (e.g. "7d" = 7 days).
func parseAge(s string) (time.Duration, error) {
	// Try standard Go duration first.
	d, err := time.ParseDuration(s)
	if err == nil {
		return d, nil
	}
	// Try Nd notation.
	if strings.HasSuffix(s, "d") {
		nStr := s[:len(s)-1]
		n, convErr := strconv.Atoi(nStr)
		if convErr == nil && n > 0 {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	return 0, fmt.Errorf("cannot parse duration %q: use Go duration (e.g. 168h) or day notation (e.g. 7d)", s)
}

// expandAMIPath expands a leading ~ to the user's home directory.
// Named expandAMIPath to avoid conflict with any other expandPath in the package.
func expandAMIPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// newRealAMISandboxFetcher builds a production SandboxFetcher backed by DynamoDB.
func newRealAMISandboxFetcher(cfg *config.Config) SandboxFetcher {
	return &realAMISandboxFetcher{cfg: cfg}
}

type realAMISandboxFetcher struct {
	cfg *config.Config
}

// amiPrimaryRegion returns the configured primary region for AMI commands, falling back
// to the KM_REGION environment variable, then "us-east-1".
func amiPrimaryRegion(cfg *config.Config) string {
	if cfg.PrimaryRegion != "" {
		return cfg.PrimaryRegion
	}
	if r := os.Getenv("KM_REGION"); r != "" {
		return r
	}
	return "us-east-1"
}

func (f *realAMISandboxFetcher) FetchSandbox(ctx context.Context, sandboxID string) (*kmaws.SandboxRecord, error) {
	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	tableName := f.cfg.SandboxTableName
	if tableName == "" {
		tableName = "km-sandboxes"
	}
	dynClient := dynamodbsvc.NewFromConfig(awsCfg)
	meta, err := kmaws.ReadSandboxMetadataDynamo(ctx, dynClient, tableName, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("fetch sandbox %s: %w", sandboxID, err)
	}
	return &kmaws.SandboxRecord{
		SandboxID: meta.SandboxID,
		Profile:   meta.ProfileName,
		Substrate: meta.Substrate,
		Region:    meta.Region,
		Status:    meta.Status,
		Alias:     meta.Alias,
		CreatedAt: meta.CreatedAt,
	}, nil
}
