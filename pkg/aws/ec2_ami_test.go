// Package aws — unit tests for ec2_ami.go helpers.
// Uses a mock implementation of EC2AMIAPI to avoid real AWS calls.
package aws

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// ---- mockEC2AMI -- reusable mock for all AMI tests --------------------------

// mockEC2AMI implements EC2AMIAPI. It captures inputs for assertions and returns
// pre-configured outputs and errors.
type mockEC2AMI struct {
	// Captured inputs
	createInput     *ec2.CreateImageInput
	describeInput   *ec2.DescribeImagesInput
	deregisterInput *ec2.DeregisterImageInput
	copyInput       *ec2.CopyImageInput
	createTagsInput *ec2.CreateTagsInput

	// Configurable returns
	createOut     *ec2.CreateImageOutput
	createErr     error
	describeOut   *ec2.DescribeImagesOutput
	describeErr   error
	deregisterOut *ec2.DeregisterImageOutput
	deregisterErr error
	copyOut       *ec2.CopyImageOutput
	copyErr       error
	createTagsErr error

	// Counters for "was it called" assertions
	describeCalls int
}

// Compile-time interface satisfaction checks.
var _ EC2AMIAPI = (*mockEC2AMI)(nil)

// EC2AMIAPI methods ---------------------------------------------------------

func (m *mockEC2AMI) CreateImage(_ context.Context, params *ec2.CreateImageInput, _ ...func(*ec2.Options)) (*ec2.CreateImageOutput, error) {
	m.createInput = params
	return m.createOut, m.createErr
}

func (m *mockEC2AMI) DescribeImages(_ context.Context, params *ec2.DescribeImagesInput, _ ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	m.describeInput = params
	m.describeCalls++
	return m.describeOut, m.describeErr
}

func (m *mockEC2AMI) DeregisterImage(_ context.Context, params *ec2.DeregisterImageInput, _ ...func(*ec2.Options)) (*ec2.DeregisterImageOutput, error) {
	m.deregisterInput = params
	return m.deregisterOut, m.deregisterErr
}

func (m *mockEC2AMI) CopyImage(_ context.Context, params *ec2.CopyImageInput, _ ...func(*ec2.Options)) (*ec2.CopyImageOutput, error) {
	m.copyInput = params
	return m.copyOut, m.copyErr
}

func (m *mockEC2AMI) CreateTags(_ context.Context, params *ec2.CreateTagsInput, _ ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	m.createTagsInput = params
	return &ec2.CreateTagsOutput{}, m.createTagsErr
}

// ---- helper: find tag value by key -----------------------------------------

func findTag(tags []types.Tag, key string) (string, bool) {
	for _, t := range tags {
		if awssdk.ToString(t.Key) == key {
			return awssdk.ToString(t.Value), true
		}
	}
	return "", false
}

// ============================================================================
// Test 1: TestKMBakeTags_IncludesAllRequiredKeys
// ============================================================================

func TestKMBakeTags_IncludesAllRequiredKeys(t *testing.T) {
	tags := KMBakeTags("sb-abc123", "myprofile", "myalias", "t3.micro", "us-east-1", "v1.0.0")

	required := []string{
		"km:sandbox-id", "km:profile", "km:alias", "km:baked-at",
		"km:source-region", "km:instance-type", "km:baked-by", "km:km-version", "Name",
	}
	for _, k := range required {
		v, ok := findTag(tags, k)
		if !ok {
			t.Errorf("missing required tag %q", k)
			continue
		}
		if v == "" {
			t.Errorf("tag %q has empty value", k)
		}
	}
}

// ============================================================================
// Test 2: TestKMBakeTags_EmptyAlias_OmitsAliasOrLeavesBlank
// ============================================================================

func TestKMBakeTags_EmptyAlias_OmitsAliasOrLeavesBlank(t *testing.T) {
	tags := KMBakeTags("sb-abc123", "myprofile", "", "t3.micro", "us-east-1", "v1.0.0")

	// Must not panic; km:alias tag must be absent (our implementation omits it).
	_, ok := findTag(tags, "km:alias")
	if ok {
		t.Error("km:alias tag should be omitted when alias is empty")
	}

	// All other required tags still present.
	for _, k := range []string{"km:sandbox-id", "km:profile", "km:baked-at", "Name"} {
		if _, ok := findTag(tags, k); !ok {
			t.Errorf("missing tag %q when alias is empty", k)
		}
	}
}

// ============================================================================
// Test 3: TestAMIName_SanitizesIllegalChars
// ============================================================================

func TestAMIName_SanitizesIllegalChars(t *testing.T) {
	ts := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	name := AMIName("restricted dev:v2", "sb-001", ts)

	if strings.Contains(name, ":") {
		t.Errorf("name contains illegal colon: %q", name)
	}
	if strings.Contains(name, " ") {
		// spaces are actually legal in AMI names per AWS, but our sanitizer keeps them;
		// however the profile part "restricted dev:v2" → "restricted dev-v2"
	}
	if len(name) > 128 {
		t.Errorf("name length %d exceeds 128 chars: %q", len(name), name)
	}
}

// ============================================================================
// Test 4: TestAMIName_TruncatesLongProfile
// ============================================================================

func TestAMIName_TruncatesLongProfile(t *testing.T) {
	longProfile := strings.Repeat("x", 200)
	ts := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	name := AMIName(longProfile, "sb-001", ts)

	if len(name) > 128 {
		t.Errorf("name length %d exceeds 128 chars for long profile", len(name))
	}
	if !strings.HasPrefix(name, "km-") {
		t.Errorf("name does not start with 'km-': %q", name)
	}
}

// ============================================================================
// Test 5: TestAMIName_DeterministicTimestamp
// ============================================================================

func TestAMIName_DeterministicTimestamp(t *testing.T) {
	ts := time.Date(2026, 4, 26, 10, 30, 59, 0, time.UTC)
	name := AMIName("myprofile", "sb-abc123", ts)

	want := "km-myprofile-sb-abc123-20260426103059"
	if name != want {
		t.Errorf("got %q, want %q", name, want)
	}
}

// ============================================================================
// Test 6: TestBakeAMI_TagSpecifications
// ============================================================================

func TestBakeAMI_TagSpecifications(t *testing.T) {
	m := &mockEC2AMI{
		createOut: &ec2.CreateImageOutput{ImageId: awssdk.String("ami-test001")},
		describeOut: &ec2.DescribeImagesOutput{
			Images: []types.Image{
				{ImageId: awssdk.String("ami-test001"), State: types.ImageStateAvailable},
			},
		},
	}

	tags := []types.Tag{
		{Key: awssdk.String("km:sandbox-id"), Value: awssdk.String("sb-001")},
	}
	_, err := BakeAMI(context.Background(), m, "i-123", "km-test", "desc", tags, 5*time.Second)
	if err != nil {
		t.Fatalf("BakeAMI returned unexpected error: %v", err)
	}

	if m.createInput == nil {
		t.Fatal("CreateImage was not called")
	}
	if len(m.createInput.TagSpecifications) != 2 {
		t.Fatalf("want 2 TagSpecifications, got %d", len(m.createInput.TagSpecifications))
	}
	if m.createInput.TagSpecifications[0].ResourceType != types.ResourceTypeImage {
		t.Errorf("first TagSpec ResourceType = %v, want ResourceTypeImage", m.createInput.TagSpecifications[0].ResourceType)
	}
	if m.createInput.TagSpecifications[1].ResourceType != types.ResourceTypeSnapshot {
		t.Errorf("second TagSpec ResourceType = %v, want ResourceTypeSnapshot", m.createInput.TagSpecifications[1].ResourceType)
	}
	// Both specs must carry identical tags.
	if len(m.createInput.TagSpecifications[0].Tags) != len(m.createInput.TagSpecifications[1].Tags) {
		t.Error("image and snapshot TagSpecs have different tag counts")
	}
	// NoReboot must be true.
	if m.createInput.NoReboot == nil || !*m.createInput.NoReboot {
		t.Error("NoReboot must be true")
	}
}

// ============================================================================
// Test 7: TestBakeAMI_CreateImageError_NoWaiterCall
// ============================================================================

func TestBakeAMI_CreateImageError_NoWaiterCall(t *testing.T) {
	m := &mockEC2AMI{
		createErr: errors.New("simulated API error"),
	}

	id, err := BakeAMI(context.Background(), m, "i-123", "km-test", "desc", nil, 5*time.Second)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if id != "" {
		t.Errorf("expected empty ID on CreateImage error, got %q", id)
	}
	if m.describeCalls != 0 {
		t.Errorf("DescribeImages should not be called when CreateImage fails, got %d calls", m.describeCalls)
	}
}

// ============================================================================
// Test 8: TestDeleteAMI_PassesDeleteAssociatedSnapshots
// ============================================================================

func TestDeleteAMI_PassesDeleteAssociatedSnapshots(t *testing.T) {
	m := &mockEC2AMI{deregisterOut: &ec2.DeregisterImageOutput{}}

	if err := DeleteAMI(context.Background(), m, "ami-001", false); err != nil {
		t.Fatalf("DeleteAMI returned unexpected error: %v", err)
	}
	if m.deregisterInput == nil {
		t.Fatal("DeregisterImage was not called")
	}
	if m.deregisterInput.DeleteAssociatedSnapshots == nil || !*m.deregisterInput.DeleteAssociatedSnapshots {
		t.Error("DeleteAssociatedSnapshots must be true")
	}
}

// ============================================================================
// Test 9: TestDeleteAMI_DryRunPropagated
// ============================================================================

func TestDeleteAMI_DryRunPropagated(t *testing.T) {
	m := &mockEC2AMI{deregisterOut: &ec2.DeregisterImageOutput{}}

	if err := DeleteAMI(context.Background(), m, "ami-001", true); err != nil {
		t.Fatalf("DeleteAMI returned unexpected error: %v", err)
	}
	if m.deregisterInput == nil {
		t.Fatal("DeregisterImage was not called")
	}
	if m.deregisterInput.DryRun == nil || !*m.deregisterInput.DryRun {
		t.Error("DryRun must be true when dryRun=true is passed")
	}
}

// ============================================================================
// Test 10: TestSnapshotIDsFromImage
// ============================================================================

func TestSnapshotIDsFromImage(t *testing.T) {
	img := types.Image{
		BlockDeviceMappings: []types.BlockDeviceMapping{
			{
				DeviceName: awssdk.String("/dev/xvda"),
				Ebs:        &types.EbsBlockDevice{SnapshotId: awssdk.String("snap-aaa111")},
			},
			{
				DeviceName: awssdk.String("/dev/xvdf"),
				// No Ebs field — should be skipped.
			},
		},
	}

	ids := SnapshotIDsFromImage(img)
	if len(ids) != 1 {
		t.Fatalf("expected 1 snapshot ID, got %d", len(ids))
	}
	if ids[0] != "snap-aaa111" {
		t.Errorf("got %q, want snap-aaa111", ids[0])
	}
}

// ============================================================================
// Test 11: TestListBakedAMIs_FilterAndOwners
// ============================================================================

func TestListBakedAMIs_FilterAndOwners(t *testing.T) {
	m := &mockEC2AMI{
		describeOut: &ec2.DescribeImagesOutput{Images: []types.Image{}},
	}

	_, err := ListBakedAMIs(context.Background(), m)
	if err != nil {
		t.Fatalf("ListBakedAMIs returned unexpected error: %v", err)
	}
	if m.describeInput == nil {
		t.Fatal("DescribeImages was not called")
	}

	// Assert Owners=["self"]
	if len(m.describeInput.Owners) != 1 || m.describeInput.Owners[0] != "self" {
		t.Errorf("Owners = %v, want [\"self\"]", m.describeInput.Owners)
	}

	// Assert filter tag:km:sandbox-id with Values=["*"]
	foundTagFilter := false
	for _, f := range m.describeInput.Filters {
		if awssdk.ToString(f.Name) == "tag:km:sandbox-id" {
			foundTagFilter = true
			if len(f.Values) != 1 || f.Values[0] != "*" {
				t.Errorf("filter tag:km:sandbox-id has Values=%v, want [\"*\"]", f.Values)
			}
		}
	}
	if !foundTagFilter {
		t.Error("filter tag:km:sandbox-id not found in DescribeImages input")
	}
}

// ============================================================================
// Test 12: TestListBakedAMIs_SortedNewestFirst
// ============================================================================

func TestListBakedAMIs_SortedNewestFirst(t *testing.T) {
	m := &mockEC2AMI{
		describeOut: &ec2.DescribeImagesOutput{
			Images: []types.Image{
				{ImageId: awssdk.String("ami-apr26"), CreationDate: awssdk.String("2026-04-26T10:00:00.000Z")},
				{ImageId: awssdk.String("ami-apr20"), CreationDate: awssdk.String("2026-04-20T10:00:00.000Z")},
				{ImageId: awssdk.String("ami-apr25"), CreationDate: awssdk.String("2026-04-25T10:00:00.000Z")},
			},
		},
	}

	images, err := ListBakedAMIs(context.Background(), m)
	if err != nil {
		t.Fatalf("ListBakedAMIs returned unexpected error: %v", err)
	}
	if len(images) != 3 {
		t.Fatalf("expected 3 images, got %d", len(images))
	}

	wantOrder := []string{"ami-apr26", "ami-apr25", "ami-apr20"}
	for i, want := range wantOrder {
		got := awssdk.ToString(images[i].ImageId)
		if got != want {
			t.Errorf("images[%d].ImageId = %q, want %q", i, got, want)
		}
	}
}
