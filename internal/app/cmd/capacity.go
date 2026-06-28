package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	sqsvcpkg "github.com/aws/aws-sdk-go-v2/service/servicequotas"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/capacity"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// Capacity verdict constants. NEVER use "available" — it implies certainty we do not have.
const (
	VerdictNotOffered   = "not-offered"   // AZ does not list this instance type in DescribeInstanceTypeOfferings
	VerdictQuotaBlocked = "quota-blocked" // GPU family + regional headroom == 0
	VerdictRecentlyDry  = "recently-dry"  // fresh LastICEAt within the 45-min window
	VerdictLikely       = "likely"        // offered + quota OK + last-success or no recent ICE signal
	VerdictUnknown      = "unknown"       // offered + quota OK but no capacity store signal
)

// CapacityAZReport holds the per-AZ data for the capacity feasibility report.
type CapacityAZReport struct {
	AZ            string
	Offered       bool
	IsGPU         bool
	QuotaHeadroom float64
	QuotaAvail    bool // false if the quota check failed or is inapplicable
	LastICEAt     *time.Time
	LastSuccessAt *time.Time
	Verdict       string
}

// freshICEWindow mirrors the ICETTLSeconds constant as a duration (45 min).
const freshICEWindow = time.Duration(capacity.ICETTLSeconds) * time.Second

// ComputeCapacityVerdict computes the capacity verdict for an AZ given its attributes.
//
// Precedence (highest priority wins):
//
//	not-offered   — AZ does not offer the instance type
//	quota-blocked — GPU family + headroom == 0
//	recently-dry  — fresh LastICEAt within 45-min ICE window
//	likely        — offered, quota OK, has a last-success OR no fresh ICE (absent or stale)
//	unknown       — offered, quota OK, entry is nil (store unavailable)
//
// NEVER returns "available".
func ComputeCapacityVerdict(offered, isGPU bool, quotaHeadroom float64, quotaAvail bool, entry *capacity.CapacityEntry) string {
	if !offered {
		return VerdictNotOffered
	}
	if isGPU && quotaAvail && quotaHeadroom == 0 {
		return VerdictQuotaBlocked
	}
	if entry != nil && entry.LastICEAt != nil && time.Since(*entry.LastICEAt) < freshICEWindow {
		return VerdictRecentlyDry
	}
	// Offered with no quota block and no fresh ICE — likely.
	// Covers: has last-success, has stale (expired) ICE, or no history at all.
	if entry != nil {
		return VerdictLikely
	}
	// entry == nil means the store was unavailable: unknown.
	return VerdictUnknown
}

// newCapacityCmd creates the `km capacity` feasibility report command.
//
// Usage:
//
//	km capacity <profile.yaml>          # resolve instance type from profile
//	km capacity --type g6e.12xlarge     # explicit instance type
//	km capacity --type g6e.12xlarge --region us-east-1
func newCapacityCmd(cfg *config.Config) *cobra.Command {
	var typeFlag string
	var regionFlag string

	cmd := &cobra.Command{
		Use:   "capacity [profile.yaml]",
		Short: "Show per-AZ capacity feasibility for an EC2 instance type",
		Long: `Print a per-AZ capacity feasibility table for an EC2 instance type.

Verdicts: likely | quota-blocked | not-offered | recently-dry | unknown
Note: "available" is never shown — capacity is probabilistic, not guaranteed.

Examples:
  km capacity profiles/gpu-qwen-12x.yaml      # resolve type from profile
  km capacity --type g6e.12xlarge
  km capacity --type g6e.12xlarge --region us-west-2`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCapacity(cmd.Context(), cfg, args, typeFlag, regionFlag)
		},
	}

	cmd.Flags().StringVar(&typeFlag, "type", "", "EC2 instance type (mutually exclusive with profile path)")
	cmd.Flags().StringVar(&regionFlag, "region", "", "AWS region (default: primary region from km-config.yaml)")

	return cmd
}

func runCapacity(ctx context.Context, cfg *config.Config, args []string, typeFlag, regionFlag string) error {
	// Resolve instance type from flag or profile argument.
	instanceType, region, err := resolveCapacityTarget(cfg, args, typeFlag, regionFlag)
	if err != nil {
		return err
	}

	awsProfile := cfg.AWSProfile
	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}
	if region == "" {
		region = awsCfg.Region
	}

	ec2Client := ec2.NewFromConfig(awsCfg, func(o *ec2.Options) {
		o.Region = region
	})

	// Resolve AZs for the region via DescribeAvailabilityZones.
	allAZs, err := describeRegionAZs(ctx, ec2Client, region)
	if err != nil {
		return fmt.Errorf("list AZs for %s: %w", region, err)
	}
	if len(allAZs) == 0 {
		return fmt.Errorf("no available AZs found in region %s", region)
	}

	// Determine which AZs offer this instance type.
	offeredAZs, offerErr := capacity.DescribeAZOfferings(ctx, ec2Client, instanceType, allAZs)
	if offerErr != nil {
		fmt.Fprintf(os.Stderr, "  WARN: DescribeInstanceTypeOfferings failed (%v); marking all AZs as unknown\n", offerErr)
	}
	offeredSet := make(map[string]bool, len(offeredAZs))
	for _, az := range offeredAZs {
		offeredSet[az] = true
	}

	// GPU quota check (regional, applies to all AZs).
	isGPU := capacity.IsGPUFamily(instanceType)
	var quotaHeadroom float64
	var quotaAvail bool
	if isGPU {
		sqClient := sqsvcpkg.NewFromConfig(awsCfg, func(o *sqsvcpkg.Options) {
			o.Region = region
		})
		headroom, qErr := capacity.GetGPUVCPUQuota(ctx, sqClient)
		if qErr != nil {
			fmt.Fprintf(os.Stderr, "  WARN: GPU quota check failed (%v); quota column will show —\n", qErr)
		} else {
			quotaHeadroom = headroom
			quotaAvail = true
		}
	}

	// Capacity store lookups.
	ddbClient := dynamodb.NewFromConfig(awsCfg)
	store := capacity.NewDynamoCapacityStore(ddbClient, cfg.GetCapacityTableName())

	// Build per-AZ report rows.
	var rows []CapacityAZReport
	for _, az := range allAZs {
		offered := offeredSet[az]
		entry, _ := store.Get(ctx, instanceType, az) // non-fatal; nil entry is fine
		verdict := ComputeCapacityVerdict(offered, isGPU, quotaHeadroom, quotaAvail, entry)
		rows = append(rows, CapacityAZReport{
			AZ:            az,
			Offered:       offered,
			IsGPU:         isGPU,
			QuotaHeadroom: quotaHeadroom,
			QuotaAvail:    quotaAvail,
			LastICEAt:     entryTimeField(entry, "ice"),
			LastSuccessAt: entryTimeField(entry, "success"),
			Verdict:       verdict,
		})
	}

	// Sort by AZ name for stable output.
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].AZ < rows[j].AZ
	})

	// Render table.
	printCapacityTable(os.Stdout, instanceType, region, rows)
	return nil
}

// resolveCapacityTarget resolves instanceType and region from CLI args + flags.
func resolveCapacityTarget(cfg *config.Config, args []string, typeFlag, regionFlag string) (instanceType, region string, err error) {
	region = regionFlag
	if region == "" {
		region = cfg.PrimaryRegion
	}

	if typeFlag != "" {
		if len(args) > 0 {
			return "", "", fmt.Errorf("--type and a profile path are mutually exclusive")
		}
		return typeFlag, region, nil
	}

	if len(args) == 0 {
		return "", "", fmt.Errorf("provide either a profile path or --type <instanceType>")
	}

	// Load and (possibly) resolve the profile to extract the instance type.
	profilePath := args[0]
	raw, readErr := os.ReadFile(profilePath)
	if readErr != nil {
		return "", "", fmt.Errorf("read profile %s: %w", profilePath, readErr)
	}
	parsed, parseErr := profile.Parse(raw)
	if parseErr != nil {
		return "", "", fmt.Errorf("parse profile %s: %w", profilePath, parseErr)
	}

	var resolved *profile.SandboxProfile
	if parsed.Extends.IsSet() {
		leafName := strings.TrimSuffix(filepath.Base(profilePath), ".yaml")
		fileDir := filepath.Dir(profilePath)
		searchPaths := []string{fileDir}
		resolved, err = profile.Resolve(leafName, searchPaths)
		if err != nil {
			return "", "", fmt.Errorf("resolve profile %s: %w", profilePath, err)
		}
	} else {
		resolved = parsed
	}

	instanceType = resolved.Spec.Runtime.InstanceType
	if instanceType == "" {
		return "", "", fmt.Errorf("profile %s does not declare spec.runtime.instanceType", profilePath)
	}
	if region == "" && resolved.Spec.Runtime.Region != "" {
		region = resolved.Spec.Runtime.Region
	}
	return instanceType, region, nil
}

// describeRegionAZs returns the available AZ names in the given region.
func describeRegionAZs(ctx context.Context, client *ec2.Client, region string) ([]string, error) {
	out, err := client.DescribeAvailabilityZones(ctx, &ec2.DescribeAvailabilityZonesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("state"), Values: []string{"available"}},
			{Name: aws.String("region-name"), Values: []string{region}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeAvailabilityZones: %w", err)
	}
	var azs []string
	for _, az := range out.AvailabilityZones {
		if az.ZoneName != nil {
			azs = append(azs, *az.ZoneName)
		}
	}
	return azs, nil
}

// printCapacityTable renders the per-AZ capacity report as a tab-aligned table.
func printCapacityTable(w interface{ Write([]byte) (int, error) }, instanceType, region string, rows []CapacityAZReport) {
	fmt.Fprintf(w, "Capacity report: %s  region: %s\n\n", instanceType, region)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "AZ\tOFFERED\tQUOTA HEADROOM\tLAST ICE\tLAST SUCCESS\tVERDICT")
	fmt.Fprintln(tw, "--\t-------\t--------------\t--------\t------------\t-------")

	for _, r := range rows {
		offeredStr := "no"
		if r.Offered {
			offeredStr = "yes"
		}

		quotaStr := "—"
		if r.IsGPU && r.QuotaAvail {
			quotaStr = fmt.Sprintf("%.0f vCPU", r.QuotaHeadroom)
		} else if !r.IsGPU {
			quotaStr = "n/a"
		}

		lastICEStr := "—"
		if r.LastICEAt != nil {
			lastICEStr = r.LastICEAt.UTC().Format("2006-01-02T15:04Z")
		}

		lastSuccessStr := "—"
		if r.LastSuccessAt != nil {
			lastSuccessStr = r.LastSuccessAt.UTC().Format("2006-01-02T15:04Z")
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			r.AZ, offeredStr, quotaStr, lastICEStr, lastSuccessStr, r.Verdict)
	}

	tw.Flush()
}

// entryTimeField extracts the LastICEAt or LastSuccessAt time from a CapacityEntry.
func entryTimeField(entry *capacity.CapacityEntry, kind string) *time.Time {
	if entry == nil {
		return nil
	}
	if kind == "ice" {
		return entry.LastICEAt
	}
	return entry.LastSuccessAt
}
