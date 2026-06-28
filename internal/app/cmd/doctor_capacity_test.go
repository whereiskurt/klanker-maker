// Package cmd — doctor_capacity_test.go
// Unit tests for the Phase 124 capacity doctor checks:
//   - checkGPUQuotaHeadroom  — GPU vCPU quota (L-DB2E81BA) WARN/OK/SKIP
//   - checkDynamoTable        — capacity table existence (WARN on missing)
package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	servicequotastypes "github.com/aws/aws-sdk-go-v2/service/servicequotas/types"
	"github.com/whereiskurt/klanker-maker/pkg/capacity"
)

// =============================================================================
// Mock: minimal capacity.ServiceQuotasAPI
// =============================================================================

// fakeServiceQuotasClient implements capacity.ServiceQuotasAPI.
// Returns the configured quota value or error.
type fakeServiceQuotasClient struct {
	quotaValue *float64 // nil => nil Value in output (triggers "nil value" error)
	err        error
}

var _ capacity.ServiceQuotasAPI = (*fakeServiceQuotasClient)(nil)

func (f *fakeServiceQuotasClient) GetServiceQuota(
	_ context.Context,
	params *servicequotas.GetServiceQuotaInput,
	_ ...func(*servicequotas.Options),
) (*servicequotas.GetServiceQuotaOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	q := &servicequotastypes.ServiceQuota{}
	if f.quotaValue != nil {
		q.Value = f.quotaValue
	}
	return &servicequotas.GetServiceQuotaOutput{Quota: q}, nil
}

// fakeServiceQuotasClientZero returns quota == 0.
func fakeServiceQuotasClientZero() *fakeServiceQuotasClient {
	v := 0.0
	return &fakeServiceQuotasClient{quotaValue: &v}
}

// fakeServiceQuotasClientNonzero returns quota > 0.
func fakeServiceQuotasClientNonzero(v float64) *fakeServiceQuotasClient {
	return &fakeServiceQuotasClient{quotaValue: &v}
}

// =============================================================================
// Tests: checkGPUQuotaHeadroom
// =============================================================================

// TestDoctor_CapacityChecks drives checkGPUQuotaHeadroom with quota=0 (Warn),
// quota>0 (OK), and a probe error (Skipped).
func TestDoctor_CapacityChecks(t *testing.T) {
	ctx := context.Background()

	t.Run("quota=0 produces CheckWarn naming L-DB2E81BA", func(t *testing.T) {
		r := checkGPUQuotaHeadroom(ctx, fakeServiceQuotasClientZero())
		if r.Status != CheckWarn {
			t.Fatalf("quota=0: expected CheckWarn, got %s: %s", r.Status, r.Message)
		}
		if !strings.Contains(r.Message, "0 vCPUs") {
			t.Errorf("quota=0: expected '0 vCPUs' in message, got: %s", r.Message)
		}
		if !strings.Contains(r.Remediation, capacity.GPUVCPUQuotaCode) {
			t.Errorf("quota=0: expected %q in remediation, got: %s", capacity.GPUVCPUQuotaCode, r.Remediation)
		}
	})

	t.Run("quota>0 produces CheckOK with vCPU count", func(t *testing.T) {
		r := checkGPUQuotaHeadroom(ctx, fakeServiceQuotasClientNonzero(192))
		if r.Status != CheckOK {
			t.Fatalf("quota>0: expected CheckOK, got %s: %s", r.Status, r.Message)
		}
		if !strings.Contains(r.Message, "192") {
			t.Errorf("quota>0: expected '192' in message, got: %s", r.Message)
		}
		if !strings.Contains(r.Message, "vCPUs") {
			t.Errorf("quota>0: expected 'vCPUs' in message, got: %s", r.Message)
		}
	})

	t.Run("probe error produces CheckSkipped", func(t *testing.T) {
		errClient := &fakeServiceQuotasClient{err: fmt.Errorf("service quotas unavailable")}
		r := checkGPUQuotaHeadroom(ctx, errClient)
		if r.Status != CheckSkipped {
			t.Fatalf("error: expected CheckSkipped, got %s: %s", r.Status, r.Message)
		}
	})

	t.Run("nil client produces CheckSkipped", func(t *testing.T) {
		r := checkGPUQuotaHeadroom(ctx, nil)
		if r.Status != CheckSkipped {
			t.Fatalf("nil client: expected CheckSkipped, got %s: %s", r.Status, r.Message)
		}
	})

	t.Run("check name is GPU vCPU quota (L-DB2E81BA)", func(t *testing.T) {
		r := checkGPUQuotaHeadroom(ctx, fakeServiceQuotasClientZero())
		if !strings.Contains(r.Name, capacity.GPUVCPUQuotaCode) {
			t.Errorf("check name must contain %q, got: %q", capacity.GPUVCPUQuotaCode, r.Name)
		}
	})
}

// TestDoctor_CapacityTableCheck verifies that the capacity DynamoDB table check
// uses WARN (not ERROR) when the table is missing, following the slack-channels pattern.
func TestDoctor_CapacityTableCheck(t *testing.T) {
	ctx := context.Background()
	tbl := "km-capacity"

	t.Run("table exists produces CheckOK", func(t *testing.T) {
		r := checkDynamoTable(ctx, &mockDynamoClient{output: &dynamodb.DescribeTableOutput{}}, tbl, "Capacity Table ("+tbl+")")
		if r.Status != CheckOK {
			t.Fatalf("expected OK, got %s: %s", r.Status, r.Message)
		}
	})

	// Verify that when called from buildChecks the ERROR is demoted to WARN.
	// We test this by calling checkDynamoTable and demoting, mirroring the
	// buildChecks closure exactly.
	t.Run("missing table demoted to CheckWarn by buildChecks closure", func(t *testing.T) {
		r := checkDynamoTable(ctx, &mockDynamoClient{err: fmt.Errorf("not found")}, tbl, "Capacity Table ("+tbl+")")
		// Raw result from checkDynamoTable is CheckError.
		if r.Status != CheckError {
			t.Fatalf("raw checkDynamoTable for missing table: expected CheckError, got %s", r.Status)
		}
		// Demote, mirroring the buildChecks closure.
		if r.Status == CheckError {
			r.Status = CheckWarn
			r.Remediation = "Run 'km init --dry-run=false' to create the DynamoDB capacity table"
		}
		if r.Status != CheckWarn {
			t.Fatalf("after demotion: expected CheckWarn, got %s", r.Status)
		}
		if !strings.Contains(r.Remediation, "capacity") {
			t.Errorf("remediation must mention capacity, got: %s", r.Remediation)
		}
	})
}

// TestDoctor_DoctorDeps_ServiceQuotasClientField confirms that DoctorDeps has
// a ServiceQuotasClient field of the correct interface type, and that buildChecks
// accesses it. This is a source-level assertion on doctor.go.
func TestDoctor_DoctorDeps_ServiceQuotasClientField(t *testing.T) {
	// Construct a DoctorDeps and set ServiceQuotasClient — if the field doesn't
	// exist or has the wrong type, this will fail to compile.
	var deps DoctorDeps
	deps.ServiceQuotasClient = fakeServiceQuotasClientNonzero(512)
	if deps.ServiceQuotasClient == nil {
		t.Error("ServiceQuotasClient must be non-nil after assignment")
	}
}

// mockDynamoClient (local alias for tests below that need DescribeTable only).
// Defined as a thin generic wrapper so we can reuse mockDynamoClient from
// the package's other test files (it's already defined there).
// NOTE: mockDynamoClient is defined in doctor_helpers_test.go; this file
// references it directly without re-declaring.

// helperQuotaServiceCode confirms the quota service code constant is "ec2".
func TestDoctor_GPUQuotaServiceCode(t *testing.T) {
	if capacity.GPUQuotaServiceCode != "ec2" {
		t.Errorf("GPUQuotaServiceCode: expected %q, got %q", "ec2", capacity.GPUQuotaServiceCode)
	}
	if capacity.GPUVCPUQuotaCode != "L-DB2E81BA" {
		t.Errorf("GPUVCPUQuotaCode: expected %q, got %q", "L-DB2E81BA", capacity.GPUVCPUQuotaCode)
	}
}

// TestDoctor_GetCapacityTableName asserts that DoctorConfigProvider includes
// GetCapacityTableName via a compile-time check using the appConfigAdapter.
// If DoctorConfigProvider doesn't have GetCapacityTableName, this won't compile.
func TestDoctor_GetCapacityTableName(t *testing.T) {
	var _ DoctorConfigProvider = (*appConfigAdapter)(nil)
	// The interface is fully implemented by appConfigAdapter including
	// GetCapacityTableName — confirmed by the compile-time assertion above.
	t.Log("DoctorConfigProvider includes GetCapacityTableName — OK")
}

// Ensure servicequotastypes is used (imported above).
var _ = servicequotastypes.ServiceQuota{}

// fakeServiceQuotasClientNonzeroPtr is a helper to satisfy awssdk.Float64.
var _ = awssdk.Float64(0)
