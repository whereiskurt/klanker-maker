package capacity

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
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

// RankAZs returns allAZs reordered by capacity preference for the given instanceType
// and region. azPreference AZs are placed first (spec.runtime.azPreference); the
// remaining AZs are sorted by last-success stickiness and ICE recency from store.
//
// Phase 124-01 stub: returns allAZs unchanged. The full ranking implementation
// (DescribeInstanceTypeOfferings filter, ServiceQuotas GPU quota gate, store-based
// sticky/ICE ordering) is delivered in Phase 124-04.
//
// Callers must NOT rely on any specific ordering from this stub — treat the return
// as an approximation until 124-04 lands.
func RankAZs(
	ctx context.Context,
	instanceType, region string,
	azPreference []string,
	store CapacityStore,
	ec2c EC2OfferingsAPI,
	sqc ServiceQuotasAPI,
	allAZs []string,
) ([]string, error) {
	// Stub: return allAZs unchanged. 124-04 implements the full ranking.
	return allAZs, nil
}
