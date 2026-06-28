package capacity_test

import (
	"context"
	"errors"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/servicequotas"
	sqtypes "github.com/aws/aws-sdk-go-v2/service/servicequotas/types"

	"github.com/whereiskurt/klanker-maker/pkg/capacity"
)

// --- fakes ---

// fakeEC2Offerings implements EC2OfferingsAPI.
type fakeEC2Offerings struct {
	offered []string
	err     error
	calls   int
}

func (f *fakeEC2Offerings) DescribeInstanceTypeOfferings(
	_ context.Context,
	_ *ec2.DescribeInstanceTypeOfferingsInput,
	_ ...func(*ec2.Options),
) (*ec2.DescribeInstanceTypeOfferingsOutput, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	var offerings []ec2types.InstanceTypeOffering
	for _, az := range f.offered {
		az := az
		offerings = append(offerings, ec2types.InstanceTypeOffering{Location: &az})
	}
	return &ec2.DescribeInstanceTypeOfferingsOutput{
		InstanceTypeOfferings: offerings,
	}, nil
}

// fakeServiceQuotas implements ServiceQuotasAPI.
type fakeServiceQuotas struct {
	value float64
	err   error
	calls int
}

func (f *fakeServiceQuotas) GetServiceQuota(
	_ context.Context,
	_ *servicequotas.GetServiceQuotaInput,
	_ ...func(*servicequotas.Options),
) (*servicequotas.GetServiceQuotaOutput, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &servicequotas.GetServiceQuotaOutput{
		Quota: &sqtypes.ServiceQuota{
			Value: awssdk.Float64(f.value),
		},
	}, nil
}

// fakeCapacityStore implements CapacityStore using a map keyed by "instanceType/az".
type fakeCapacityStore struct {
	entries map[string]*capacity.CapacityEntry
}

func (f *fakeCapacityStore) storeKey(instanceType, az string) string {
	return instanceType + "/" + az
}

func (f *fakeCapacityStore) RecordICE(_ context.Context, instanceType, az string) error {
	if f.entries == nil {
		f.entries = make(map[string]*capacity.CapacityEntry)
	}
	now := time.Now()
	e := f.getOrCreate(instanceType, az)
	e.LastICEAt = &now
	f.entries[f.storeKey(instanceType, az)] = e
	return nil
}

func (f *fakeCapacityStore) RecordSuccess(_ context.Context, instanceType, az string) error {
	if f.entries == nil {
		f.entries = make(map[string]*capacity.CapacityEntry)
	}
	now := time.Now()
	e := f.getOrCreate(instanceType, az)
	e.LastSuccessAt = &now
	f.entries[f.storeKey(instanceType, az)] = e
	return nil
}

func (f *fakeCapacityStore) Get(_ context.Context, instanceType, az string) (*capacity.CapacityEntry, error) {
	if f.entries == nil {
		return &capacity.CapacityEntry{InstanceType: instanceType, AZ: az}, nil
	}
	if e, ok := f.entries[f.storeKey(instanceType, az)]; ok {
		return e, nil
	}
	return &capacity.CapacityEntry{InstanceType: instanceType, AZ: az}, nil
}

func (f *fakeCapacityStore) getOrCreate(instanceType, az string) *capacity.CapacityEntry {
	k := f.storeKey(instanceType, az)
	if e, ok := f.entries[k]; ok {
		return e
	}
	return &capacity.CapacityEntry{InstanceType: instanceType, AZ: az}
}

// --- helpers ---

func containsAZ(list []string, az string) bool {
	for _, a := range list {
		if a == az {
			return true
		}
	}
	return false
}

func indexAZ(list []string, az string) int {
	for i, a := range list {
		if a == az {
			return i
		}
	}
	return -1
}

// --- tests ---

// TestRankAZs_DropsNonOffering: offerings mock returns only [1c,1b] for the type -> 1a dropped.
func TestRankAZs_DropsNonOffering(t *testing.T) {
	t.Parallel()

	ec2c := &fakeEC2Offerings{offered: []string{"us-east-1c", "us-east-1b"}}
	sqc := &fakeServiceQuotas{value: 100}
	store := &fakeCapacityStore{}

	allAZs := []string{"us-east-1a", "us-east-1b", "us-east-1c"}
	ranked, err := capacity.RankAZs(context.Background(), "t3.medium", "us-east-1",
		nil, store, ec2c, sqc, allAZs)

	if err != nil {
		t.Fatalf("RankAZs returned error: %v", err)
	}
	if containsAZ(ranked, "us-east-1a") {
		t.Errorf("us-east-1a should be dropped (not offered), got %v", ranked)
	}
	if !containsAZ(ranked, "us-east-1b") {
		t.Errorf("us-east-1b should be present (offered), got %v", ranked)
	}
	if !containsAZ(ranked, "us-east-1c") {
		t.Errorf("us-east-1c should be present (offered), got %v", ranked)
	}
}

// TestRankAZs_GPUQuotaBlock: GPU instance type + quota headroom=0 -> *QuotaError{L-DB2E81BA}.
func TestRankAZs_GPUQuotaBlock(t *testing.T) {
	t.Parallel()

	ec2c := &fakeEC2Offerings{offered: []string{"us-east-1a", "us-east-1b", "us-east-1c"}}
	sqc := &fakeServiceQuotas{value: 0} // zero headroom
	store := &fakeCapacityStore{}

	allAZs := []string{"us-east-1a", "us-east-1b", "us-east-1c"}
	ranked, err := capacity.RankAZs(context.Background(), "g6e.12xlarge", "us-east-1",
		nil, store, ec2c, sqc, allAZs)

	if ranked != nil {
		t.Errorf("expected nil AZ list on quota block, got %v", ranked)
	}
	if err == nil {
		t.Fatal("expected *QuotaError, got nil")
	}
	var qe *capacity.QuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *QuotaError, got %T: %v", err, err)
	}
	if qe.QuotaCode != capacity.GPUVCPUQuotaCode {
		t.Errorf("QuotaCode = %q, want %q", qe.QuotaCode, capacity.GPUVCPUQuotaCode)
	}
	if qe.Headroom != 0 {
		t.Errorf("Headroom = %f, want 0", qe.Headroom)
	}
}

// TestRankAZs_AZPreference: azPreference [1c] + offered [1a,1b,1c] -> 1c first.
func TestRankAZs_AZPreference(t *testing.T) {
	t.Parallel()

	ec2c := &fakeEC2Offerings{offered: []string{"us-east-1a", "us-east-1b", "us-east-1c"}}
	sqc := &fakeServiceQuotas{value: 100}
	store := &fakeCapacityStore{}

	allAZs := []string{"us-east-1a", "us-east-1b", "us-east-1c"}
	ranked, err := capacity.RankAZs(context.Background(), "t3.medium", "us-east-1",
		[]string{"us-east-1c"}, store, ec2c, sqc, allAZs)

	if err != nil {
		t.Fatalf("RankAZs returned error: %v", err)
	}
	if len(ranked) == 0 {
		t.Fatal("expected non-empty ranked list")
	}
	if ranked[0] != "us-east-1c" {
		t.Errorf("azPreference [1c]: expected 1c first, got %v", ranked)
	}
}

// TestRankAZs_ICEStickySuccess: store says 1b last-success + 1c fresh-ICE -> 1b before 1c; 1c last.
func TestRankAZs_ICEStickySuccess(t *testing.T) {
	t.Parallel()

	ec2c := &fakeEC2Offerings{offered: []string{"us-east-1a", "us-east-1b", "us-east-1c"}}
	sqc := &fakeServiceQuotas{value: 96} // non-zero quota so GPU launches aren't blocked
	store := &fakeCapacityStore{
		entries: map[string]*capacity.CapacityEntry{},
	}

	now := time.Now()
	freshICETime := now.Add(-5 * time.Minute) // 5 min ago = within the 45-min ICE window
	successTime := now.Add(-1 * time.Hour)

	store.entries["g6e.12xlarge/us-east-1b"] = &capacity.CapacityEntry{
		InstanceType:  "g6e.12xlarge",
		AZ:            "us-east-1b",
		LastSuccessAt: &successTime,
	}
	store.entries["g6e.12xlarge/us-east-1c"] = &capacity.CapacityEntry{
		InstanceType: "g6e.12xlarge",
		AZ:           "us-east-1c",
		LastICEAt:    &freshICETime,
	}

	allAZs := []string{"us-east-1a", "us-east-1b", "us-east-1c"}
	ranked, err := capacity.RankAZs(context.Background(), "g6e.12xlarge", "us-east-1",
		nil, store, ec2c, sqc, allAZs)

	if err != nil {
		t.Fatalf("RankAZs returned error: %v", err)
	}

	iB := indexAZ(ranked, "us-east-1b")
	iC := indexAZ(ranked, "us-east-1c")

	if iB == -1 || iC == -1 {
		t.Fatalf("expected both 1b and 1c in ranked list, got %v", ranked)
	}
	if iB >= iC {
		t.Errorf("expected 1b (last-success) before 1c (fresh-ICE), got order %v", ranked)
	}
	// 1c should be last (fresh ICE = lowest rank)
	if iC != len(ranked)-1 {
		t.Errorf("expected 1c (fresh-ICE) last in ranked list, got index %d in %v", iC, ranked)
	}
}

// TestRankAZs covers two general cases: non-GPU skips quota, offerings error falls back to allAZs.
func TestRankAZs(t *testing.T) {
	t.Parallel()

	t.Run("non-GPU skips quota call", func(t *testing.T) {
		t.Parallel()

		ec2c := &fakeEC2Offerings{offered: []string{"us-east-1a", "us-east-1b"}}
		sqc := &fakeServiceQuotas{value: 0} // quota=0 would block GPU, but c5 is non-GPU
		store := &fakeCapacityStore{}

		allAZs := []string{"us-east-1a", "us-east-1b"}
		ranked, err := capacity.RankAZs(context.Background(), "c5.xlarge", "us-east-1",
			nil, store, ec2c, sqc, allAZs)

		if err != nil {
			t.Fatalf("non-GPU type should not return error even with quota=0, got %v", err)
		}
		if sqc.calls != 0 {
			t.Errorf("GetServiceQuota should NOT be called for non-GPU type, got %d calls", sqc.calls)
		}
		if len(ranked) == 0 {
			t.Error("expected non-empty ranked list")
		}
	})

	t.Run("offerings error falls back to allAZs", func(t *testing.T) {
		t.Parallel()

		ec2c := &fakeEC2Offerings{err: errors.New("DescribeInstanceTypeOfferings API error")}
		sqc := &fakeServiceQuotas{value: 100}
		store := &fakeCapacityStore{}

		allAZs := []string{"us-east-1a", "us-east-1b", "us-east-1c"}
		ranked, err := capacity.RankAZs(context.Background(), "t3.medium", "us-east-1",
			nil, store, ec2c, sqc, allAZs)

		if err != nil {
			t.Fatalf("offerings error should not block the create; RankAZs should fall back to allAZs, got %v", err)
		}
		// All allAZs should be present (fallback)
		for _, az := range allAZs {
			if !containsAZ(ranked, az) {
				t.Errorf("AZ %q missing from ranked list after offerings error fallback: %v", az, ranked)
			}
		}
	})
}
