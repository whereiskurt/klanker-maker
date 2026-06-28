package capacity

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	"github.com/rs/zerolog/log"
)

// EC2OfferingsAPI is the narrow EC2 interface required by RankAZs for querying
// per-AZ instance type offerings. Using a narrow interface keeps the mock surface
// small (follows pkg/aws EC2API narrow-interface pattern).
type EC2OfferingsAPI interface {
	DescribeInstanceTypeOfferings(
		ctx context.Context,
		params *ec2.DescribeInstanceTypeOfferingsInput,
		optFns ...func(*ec2.Options),
	) (*ec2.DescribeInstanceTypeOfferingsOutput, error)
}

// ServiceQuotasAPI is the narrow Service Quotas interface required by RankAZs for
// querying the GPU vCPU quota (L-DB2E81BA).
type ServiceQuotasAPI interface {
	GetServiceQuota(
		ctx context.Context,
		params *servicequotas.GetServiceQuotaInput,
		optFns ...func(*servicequotas.Options),
	) (*servicequotas.GetServiceQuotaOutput, error)
}

// GPUVCPUQuotaCode is the Service Quotas code for "Running On-Demand G and VT
// instances" (vCPU-denominated). A value of 0 means GPU launches will fail-fast.
const GPUVCPUQuotaCode = "L-DB2E81BA"

// GPUQuotaServiceCode is the Service Quotas service code for EC2 quota lookups.
const GPUQuotaServiceCode = "ec2"

// freshICEWindow is the duration within which a LastICEAt is considered "fresh"
// and deprioritized in AZ ranking (matches ICETTLSeconds = 45 min).
const freshICEWindow = 45 * time.Minute

// QuotaError is returned by RankAZs when a regional quota wall blocks all AZs
// (fail-fast case — iterating AZs cannot fix a quota limit).
type QuotaError struct {
	// QuotaCode is the Service Quotas quota code, e.g. "L-DB2E81BA".
	QuotaCode string
	// Headroom is the available quota headroom (0 when quota is exhausted).
	Headroom float64
}

// Error implements the error interface.
func (e *QuotaError) Error() string {
	return fmt.Sprintf(
		"GPU vCPU quota %s is exhausted (headroom=%.0f): "+
			"request an increase at https://console.aws.amazon.com/servicequotas "+
			"(service ec2, quota %s)",
		e.QuotaCode, e.Headroom, e.QuotaCode,
	)
}

// isGPUFamily returns true for instance type prefixes "g" and "vt" (case-insensitive).
// These families are subject to the regional GPU vCPU quota (L-DB2E81BA).
// 124-RESEARCH Open Question 2: "g" covers g3/g4/g5/g6/g6e; "vt" covers vt1.
func isGPUFamily(instanceType string) bool {
	lower := strings.ToLower(instanceType)
	return strings.HasPrefix(lower, "g") || strings.HasPrefix(lower, "vt")
}

// DescribeAZOfferings returns the subset of azs that offer instanceType,
// using DescribeInstanceTypeOfferings (LocationType=availability-zone).
// On error, returns nil and the error (caller decides fallback behavior).
func DescribeAZOfferings(ctx context.Context, client EC2OfferingsAPI, instanceType string, azs []string) ([]string, error) {
	if len(azs) == 0 {
		return nil, nil
	}
	azFilter := make([]string, len(azs))
	copy(azFilter, azs)
	out, err := client.DescribeInstanceTypeOfferings(ctx, &ec2.DescribeInstanceTypeOfferingsInput{
		LocationType: ec2types.LocationTypeAvailabilityZone,
		Filters: []ec2types.Filter{
			{Name: awssdk.String("location"), Values: azFilter},
			{Name: awssdk.String("instance-type"), Values: []string{instanceType}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("DescribeInstanceTypeOfferings: %w", err)
	}
	offered := make([]string, 0, len(out.InstanceTypeOfferings))
	for _, o := range out.InstanceTypeOfferings {
		if o.Location != nil {
			offered = append(offered, *o.Location)
		}
	}
	return offered, nil
}

// GetGPUVCPUQuota returns the current applied quota value for the
// "Running On-Demand G and VT instances" quota (L-DB2E81BA).
func GetGPUVCPUQuota(ctx context.Context, client ServiceQuotasAPI) (float64, error) {
	out, err := client.GetServiceQuota(ctx, &servicequotas.GetServiceQuotaInput{
		ServiceCode: awssdk.String(GPUQuotaServiceCode),
		QuotaCode:   awssdk.String(GPUVCPUQuotaCode),
	})
	if err != nil {
		return 0, fmt.Errorf("GetServiceQuota %s: %w", GPUVCPUQuotaCode, err)
	}
	if out.Quota == nil || out.Quota.Value == nil {
		return 0, fmt.Errorf("quota %s has nil value", GPUVCPUQuotaCode)
	}
	return *out.Quota.Value, nil
}

// rankScore returns a numeric rank for an AZ: higher = more preferred.
//
// Scoring:
//
//	 2 — has a recorded last-success (sticky: tried before, worked)
//	 0 — no signal (offered but unknown history)
//	-1 — has a fresh ICE (within freshICEWindow = 45 min; deprioritize)
//
// Store errors are treated as score=0 (unknown).
func rankScore(ctx context.Context, store CapacityStore, instanceType, az string) int {
	entry, err := store.Get(ctx, instanceType, az)
	if err != nil || entry == nil {
		return 0
	}
	if entry.LastSuccessAt != nil {
		return 2
	}
	if entry.LastICEAt != nil && time.Since(*entry.LastICEAt) < freshICEWindow {
		return -1
	}
	return 0
}

// RankAZs returns allAZs reordered by capacity preference for the given instanceType
// and region. The ranking algorithm:
//
//  1. DescribeInstanceTypeOfferings — drops AZs that do not offer instanceType.
//     On API error: warns and falls back to allAZs (non-fatal; best-effort).
//
//  2. GPU quota gate (isGPUFamily only): if GetGPUVCPUQuota returns headroom==0,
//     returns (nil, *QuotaError) immediately — iterating AZs cannot fix a regional
//     quota wall. Quota errors are fail-fast. Non-GPU types skip this call entirely.
//
//  3. azPreference AZs that are offered appear first (intersect(azPreference, offered)).
//
//  4. Remaining offered AZs are sorted by rankScore: last-success first (sticky),
//     fresh-ICE last, alphabetical tiebreak for stability.
func RankAZs(
	ctx context.Context,
	instanceType, region string,
	azPreference []string,
	store CapacityStore,
	ec2c EC2OfferingsAPI,
	sqc ServiceQuotasAPI,
	allAZs []string,
) ([]string, error) {
	// Step 1: Filter to AZs that offer this instance type.
	offered, err := DescribeAZOfferings(ctx, ec2c, instanceType, allAZs)
	if err != nil {
		log.Warn().Err(err).Str("instanceType", instanceType).Str("region", region).
			Msg("capacity: DescribeInstanceTypeOfferings failed; using all AZs as fallback")
		offered = allAZs
	}
	// If offerings returns empty (unusual but possible), fall back to allAZs.
	if len(offered) == 0 {
		offered = allAZs
	}

	// Step 2: Regional quota gate for GPU families (fail-fast; regional, not per-AZ).
	if isGPUFamily(instanceType) {
		headroom, quotaErr := GetGPUVCPUQuota(ctx, sqc)
		if quotaErr != nil {
			log.Warn().Err(quotaErr).Str("instanceType", instanceType).
				Msg("capacity: GPU vCPU quota check failed; continuing without quota gate")
		} else if headroom == 0 {
			return nil, &QuotaError{QuotaCode: GPUVCPUQuotaCode, Headroom: 0}
		}
	}

	// Step 3: Merge azPreference ahead of the sorted list.
	// intersect(azPreference, offered) preserves the caller's preference order.
	offeredSet := make(map[string]bool, len(offered))
	for _, az := range offered {
		offeredSet[az] = true
	}
	prefSet := make(map[string]bool, len(azPreference))
	for _, az := range azPreference {
		prefSet[az] = true
	}

	var preferredHead []string
	for _, az := range azPreference {
		if offeredSet[az] {
			preferredHead = append(preferredHead, az)
		}
	}

	var remaining []string
	for _, az := range offered {
		if !prefSet[az] {
			remaining = append(remaining, az)
		}
	}

	// Step 4: Sort remaining by rankScore (last-success first, fresh-ICE last, alphabetical tiebreak).
	sort.SliceStable(remaining, func(i, j int) bool {
		si := rankScore(ctx, store, instanceType, remaining[i])
		sj := rankScore(ctx, store, instanceType, remaining[j])
		if si != sj {
			return si > sj // higher score = more preferred = earlier in list
		}
		return remaining[i] < remaining[j] // alphabetical tiebreak for stability
	})

	return append(preferredHead, remaining...), nil
}
