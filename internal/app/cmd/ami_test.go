// Package cmd — ami_test.go
// Mock-based unit tests for km ami list, delete, bake, and copy subcommands.
// Tests run entirely in-process with injected mocks — no real AWS calls.
package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ============================================================
// mockEC2AMI — satisfies kmaws.EC2AMIAPI
// ============================================================

type mockEC2AMI struct {
	createImageOut      *ec2.CreateImageOutput
	createImageErr      error
	describeImagesOut   *ec2.DescribeImagesOutput
	describeImagesErr   error
	deregisterImageErr  error
	copyImageOut        *ec2.CopyImageOutput
	copyImageErr        error
	createTagsErr       error
	// Call recording for ordering assertions.
	callOrder []string
	// deregisterCalled is true after DeregisterImage is invoked.
	deregisterCalled bool
	// createTagsCalled is true after CreateTags is invoked.
	createTagsCalled bool
	// createTagsInput captures the last CreateTags input.
	createTagsInput *ec2.CreateTagsInput
}

var _ kmaws.EC2AMIAPI = (*mockEC2AMI)(nil)

func (m *mockEC2AMI) CreateImage(_ context.Context, _ *ec2.CreateImageInput, _ ...func(*ec2.Options)) (*ec2.CreateImageOutput, error) {
	m.callOrder = append(m.callOrder, "CreateImage")
	if m.createImageOut != nil {
		return m.createImageOut, m.createImageErr
	}
	return &ec2.CreateImageOutput{ImageId: awssdk.String("ami-0baked000000")}, m.createImageErr
}

func (m *mockEC2AMI) DescribeImages(_ context.Context, _ *ec2.DescribeImagesInput, _ ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	m.callOrder = append(m.callOrder, "DescribeImages")
	if m.describeImagesOut != nil {
		return m.describeImagesOut, m.describeImagesErr
	}
	return &ec2.DescribeImagesOutput{}, m.describeImagesErr
}

func (m *mockEC2AMI) DeregisterImage(_ context.Context, _ *ec2.DeregisterImageInput, _ ...func(*ec2.Options)) (*ec2.DeregisterImageOutput, error) {
	m.callOrder = append(m.callOrder, "DeregisterImage")
	m.deregisterCalled = true
	return &ec2.DeregisterImageOutput{}, m.deregisterImageErr
}

func (m *mockEC2AMI) CopyImage(_ context.Context, _ *ec2.CopyImageInput, _ ...func(*ec2.Options)) (*ec2.CopyImageOutput, error) {
	m.callOrder = append(m.callOrder, "CopyImage")
	if m.copyImageOut != nil {
		return m.copyImageOut, m.copyImageErr
	}
	return &ec2.CopyImageOutput{ImageId: awssdk.String("ami-0copied00000")}, m.copyImageErr
}

func (m *mockEC2AMI) CreateTags(_ context.Context, input *ec2.CreateTagsInput, _ ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	m.callOrder = append(m.callOrder, "CreateTags")
	m.createTagsCalled = true
	m.createTagsInput = input
	return &ec2.CreateTagsOutput{}, m.createTagsErr
}

// ============================================================
// mockSandboxListerAMI — satisfies SandboxLister
// ============================================================

type mockSandboxListerAMI struct {
	records []kmaws.SandboxRecord
	err     error
}

func (m *mockSandboxListerAMI) ListSandboxes(_ context.Context, _ bool) ([]kmaws.SandboxRecord, error) {
	return m.records, m.err
}

// ============================================================
// mockSandboxFetcherAMI — satisfies SandboxFetcher
// ============================================================

type mockSandboxFetcherAMI struct {
	rec *kmaws.SandboxRecord
	err error
}

func (m *mockSandboxFetcherAMI) FetchSandbox(_ context.Context, _ string) (*kmaws.SandboxRecord, error) {
	return m.rec, m.err
}

// ============================================================
// Helpers
// ============================================================

// amiTestConfig returns a minimal config.Config with a temp ProfileSearchPaths.
func amiTestConfig(t *testing.T, searchPaths ...string) *config.Config {
	t.Helper()
	cfg := &config.Config{
		ProfileSearchPaths: searchPaths,
		PrimaryRegion:      "us-east-1",
	}
	return cfg
}

// tempProfileDir creates a temporary directory and writes profile YAML files.
// profiles maps filename (without .yaml) to file contents.
func tempProfileDir(t *testing.T, profiles map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range profiles {
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write profile %s: %v", path, err)
		}
	}
	return dir
}

// makeImage builds a minimal ec2types.Image with the given tags and creation date.
func makeImage(id, name, profile, sandboxID string, createdAt time.Time, sizeGB int32) ec2types.Image {
	tags := []ec2types.Tag{
		{Key: awssdk.String("km:profile"), Value: awssdk.String(profile)},
		{Key: awssdk.String("km:sandbox-id"), Value: awssdk.String(sandboxID)},
		{Key: awssdk.String("km:source-region"), Value: awssdk.String("us-east-1")},
		{Key: awssdk.String("km:instance-type"), Value: awssdk.String("t3.medium")},
	}
	return ec2types.Image{
		ImageId:      awssdk.String(id),
		Name:         awssdk.String(name),
		CreationDate: awssdk.String(createdAt.UTC().Format(time.RFC3339)),
		BlockDeviceMappings: []ec2types.BlockDeviceMapping{
			{Ebs: &ec2types.EbsBlockDevice{
				VolumeSize: awssdk.Int32(sizeGB),
				SnapshotId: awssdk.String("snap-" + id[4:]),
			}},
		},
		Tags: tags,
	}
}

// executeAMICmd runs the ami command with the given args and returns stdout, stderr, and the error.
func executeAMICmd(t *testing.T, cfg *config.Config, ec2Factory func(string) kmaws.EC2AMIAPI, fetcher SandboxFetcher, lister SandboxLister, args []string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := NewAMICmdWithDeps(cfg, ec2Factory, fetcher, lister)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

// ============================================================
// Test 1: TestAMIList_NarrowOutput_Columns
// ============================================================

func TestAMIList_NarrowOutput_Columns(t *testing.T) {
	now := time.Now().UTC()
	img1 := makeImage("ami-0abc111111", "km-dev-sb-aaaa-20260101", "dev", "sb-aaaa1111", now.Add(-48*time.Hour), 20)
	img2 := makeImage("ami-0abc222222", "km-prod-sb-bbbb-20260101", "prod", "sb-bbbb2222", now.Add(-72*time.Hour), 30)

	mockClient := &mockEC2AMI{
		describeImagesOut: &ec2.DescribeImagesOutput{Images: []ec2types.Image{img1, img2}},
	}
	// ListBakedAMIs calls DescribeImages; mock returns our images.
	// But ListBakedAMIs filters on km:sandbox-id tag — ensure we provide that tag.
	// We supply the mock directly as the factory return.
	// NOTE: ListBakedAMIs does a real API call using the client; we need to re-use mock.
	// The mock's DescribeImages returns the images regardless of filter.
	factory := func(_ string) kmaws.EC2AMIAPI { return mockClient }

	cfg := amiTestConfig(t)
	stdout, _, err := executeAMICmd(t, cfg, factory, nil, &mockSandboxListerAMI{}, []string{"list"})
	if err != nil {
		t.Fatalf("list returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) < 1 {
		t.Fatalf("expected at least 1 line, got %d", len(lines))
	}

	header := lines[0]
	// Narrow output should have exactly 6 tab-separated columns.
	cols := strings.Fields(header)
	expected := []string{"AMI", "ID", "NAME", "AGE", "SIZE", "PROFILE", "REFS"}
	// tabwriter joins header with spaces; just count distinct column labels.
	// The header line is: "AMI ID  NAME  AGE  SIZE  PROFILE  REFS"
	// after tabwriter it may be split; check it contains all expected tokens.
	headerStr := strings.ToUpper(header)
	for _, col := range []string{"AMI", "NAME", "AGE", "SIZE", "PROFILE", "REFS"} {
		if !strings.Contains(headerStr, col) {
			t.Errorf("narrow header missing column %q; header=%q", col, header)
		}
	}
	// Wide-only columns should NOT appear.
	for _, wideCol := range []string{"REGION", "SANDBOX-ID", "SNAPS", "ENCRYPTED", "INSTANCE", "$/MONTH"} {
		if strings.Contains(headerStr, wideCol) {
			t.Errorf("narrow header should not contain wide column %q; header=%q", wideCol, header)
		}
	}
	_ = cols
	_ = expected
}

// ============================================================
// Test 2: TestAMIList_WideOutput_Columns
// ============================================================

func TestAMIList_WideOutput_Columns(t *testing.T) {
	now := time.Now().UTC()
	img := makeImage("ami-0abc333333", "km-dev-sb-cccc", "dev", "sb-cccc3333", now.Add(-24*time.Hour), 20)
	mockClient := &mockEC2AMI{
		describeImagesOut: &ec2.DescribeImagesOutput{Images: []ec2types.Image{img}},
	}
	factory := func(_ string) kmaws.EC2AMIAPI { return mockClient }

	cfg := amiTestConfig(t)
	stdout, _, err := executeAMICmd(t, cfg, factory, nil, &mockSandboxListerAMI{}, []string{"list", "--wide"})
	if err != nil {
		t.Fatalf("list --wide returned error: %v", err)
	}

	headerStr := strings.ToUpper(strings.Split(strings.TrimSpace(stdout), "\n")[0])
	for _, col := range []string{"AMI", "NAME", "AGE", "SIZE", "PROFILE", "REFS", "REGION", "SANDBOX-ID", "SNAPS", "ENCRYPTED", "INSTANCE"} {
		if !strings.Contains(headerStr, col) {
			t.Errorf("wide header missing column %q; header=%q", col, headerStr)
		}
	}
}

// ============================================================
// Test 3: TestAMIList_SortedNewestFirst
// ============================================================

func TestAMIList_SortedNewestFirst(t *testing.T) {
	now := time.Now().UTC()
	old := makeImage("ami-0old000000", "oldest", "dev", "sb-aaaa1111", now.Add(-30*24*time.Hour), 10)
	mid := makeImage("ami-0mid000000", "middle", "dev", "sb-bbbb2222", now.Add(-10*24*time.Hour), 10)
	newest := makeImage("ami-0new000000", "newest", "dev", "sb-cccc3333", now.Add(-1*24*time.Hour), 10)

	// Return in scrambled order to test sorting.
	mockClient := &mockEC2AMI{
		describeImagesOut: &ec2.DescribeImagesOutput{Images: []ec2types.Image{old, newest, mid}},
	}
	factory := func(_ string) kmaws.EC2AMIAPI { return mockClient }

	cfg := amiTestConfig(t)
	stdout, _, err := executeAMICmd(t, cfg, factory, nil, &mockSandboxListerAMI{}, []string{"list"})
	if err != nil {
		t.Fatalf("list returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	// lines[0] = header, lines[1..] = data rows
	if len(lines) < 4 {
		t.Fatalf("expected header + 3 data rows, got %d lines:\n%s", len(lines), stdout)
	}

	// Newest should appear first.
	if !strings.Contains(lines[1], "ami-0new000000") {
		t.Errorf("expected newest AMI first, got line: %q", lines[1])
	}
	if !strings.Contains(lines[3], "ami-0old000000") {
		t.Errorf("expected oldest AMI last, got line: %q", lines[3])
	}
}

// ============================================================
// Test 4: TestAMIList_AgeFilter
// ============================================================

func TestAMIList_AgeFilter(t *testing.T) {
	now := time.Now().UTC()
	day1 := makeImage("ami-0fresh111111", "day1", "dev", "sb-aaaa1111", now.Add(-1*24*time.Hour), 10)
	day10 := makeImage("ami-0tendays1111", "day10", "dev", "sb-bbbb2222", now.Add(-10*24*time.Hour), 10)
	day30 := makeImage("ami-0thirty11111", "day30", "dev", "sb-cccc3333", now.Add(-30*24*time.Hour), 10)

	mockClient := &mockEC2AMI{
		describeImagesOut: &ec2.DescribeImagesOutput{Images: []ec2types.Image{day1, day10, day30}},
	}
	factory := func(_ string) kmaws.EC2AMIAPI { return mockClient }

	cfg := amiTestConfig(t)
	stdout, _, err := executeAMICmd(t, cfg, factory, nil, &mockSandboxListerAMI{}, []string{"list", "--age", "7d"})
	if err != nil {
		t.Fatalf("list --age returned error: %v", err)
	}

	// day1 is only 1 day old — should be excluded (younger than 7d).
	if strings.Contains(stdout, "ami-0fresh111111") {
		t.Error("1-day-old AMI should be excluded by --age 7d")
	}
	// day10 and day30 should appear.
	if !strings.Contains(stdout, "ami-0tendays1111") {
		t.Error("10-day-old AMI should be included by --age 7d")
	}
	if !strings.Contains(stdout, "ami-0thirty11111") {
		t.Error("30-day-old AMI should be included by --age 7d")
	}
}

// ============================================================
// Test 5: TestAMIList_ProfileFilter
// ============================================================

func TestAMIList_ProfileFilter(t *testing.T) {
	now := time.Now().UTC()
	imgA1 := makeImage("ami-0aaaa111111", "km-a-1", "profile-a", "sb-aaaa1111", now.Add(-1*24*time.Hour), 10)
	imgB := makeImage("ami-0bbbb222222", "km-b-1", "profile-b", "sb-bbbb2222", now.Add(-2*24*time.Hour), 10)
	imgA2 := makeImage("ami-0aaaa333333", "km-a-2", "profile-a", "sb-cccc3333", now.Add(-3*24*time.Hour), 10)

	mockClient := &mockEC2AMI{
		describeImagesOut: &ec2.DescribeImagesOutput{Images: []ec2types.Image{imgA1, imgB, imgA2}},
	}
	factory := func(_ string) kmaws.EC2AMIAPI { return mockClient }

	cfg := amiTestConfig(t)
	stdout, _, err := executeAMICmd(t, cfg, factory, nil, &mockSandboxListerAMI{}, []string{"list", "--profile", "profile-a"})
	if err != nil {
		t.Fatalf("list --profile returned error: %v", err)
	}

	if strings.Contains(stdout, "ami-0bbbb222222") {
		t.Error("profile-b AMI should be excluded by --profile profile-a")
	}
	if !strings.Contains(stdout, "ami-0aaaa111111") {
		t.Error("first profile-a AMI should be included")
	}
	if !strings.Contains(stdout, "ami-0aaaa333333") {
		t.Error("second profile-a AMI should be included")
	}
}

// ============================================================
// Test 6: TestAMIList_UnusedFilter
// ============================================================

func TestAMIList_UnusedFilter(t *testing.T) {
	now := time.Now().UTC()
	// This AMI has no profile reference and no running sandbox backing it.
	unusedAMI := makeImage("ami-0unused00000", "unused", "dev", "sb-aaaa1111", now.Add(-5*24*time.Hour), 10)
	// This AMI is referenced by a profile.
	referencedAMI := makeImage("ami-0ref0000000", "referenced", "dev", "sb-bbbb2222", now.Add(-5*24*time.Hour), 10)

	// Create a temp profile that references referencedAMI.
	refProfile := fmt.Sprintf(`apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: dev
spec:
  lifecycle:
    ttl: 24h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
    ami: %s
  execution: {}
  sourceAccess: {}
  network: {}
  identity: {}
  sidecars: {}
  observability: {}
  agent: {}
`, "ami-0ref0000000")

	dir := tempProfileDir(t, map[string]string{"dev-ref": refProfile})
	cfg := amiTestConfig(t, dir)

	mockClient := &mockEC2AMI{
		describeImagesOut: &ec2.DescribeImagesOutput{Images: []ec2types.Image{unusedAMI, referencedAMI}},
	}
	factory := func(_ string) kmaws.EC2AMIAPI { return mockClient }
	// Lister returns no running sandboxes.
	lister := &mockSandboxListerAMI{records: []kmaws.SandboxRecord{}}

	stdout, _, err := executeAMICmd(t, cfg, factory, nil, lister, []string{"list", "--unused"})
	if err != nil {
		t.Fatalf("list --unused returned error: %v", err)
	}

	if !strings.Contains(stdout, "ami-0unused00000") {
		t.Error("unused AMI should be included by --unused filter")
	}
	if strings.Contains(stdout, "ami-0ref0000000") {
		t.Error("referenced AMI should be excluded by --unused filter")
	}
}

// ============================================================
// Test 7: TestAMIDelete_RefuseWhenProfileReferences
// ============================================================

func TestAMIDelete_RefuseWhenProfileReferences(t *testing.T) {
	refProfile := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: dev
spec:
  lifecycle:
    ttl: 24h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
    ami: ami-0targeted000
  execution: {}
  sourceAccess: {}
  network: {}
  identity: {}
  sidecars: {}
  observability: {}
  agent: {}
`
	dir := tempProfileDir(t, map[string]string{"dev-with-ami": refProfile})
	cfg := amiTestConfig(t, dir)

	img := makeImage("ami-0targeted000", "targeted", "dev", "sb-aaaa1111", time.Now().Add(-5*24*time.Hour), 20)
	mockClient := &mockEC2AMI{
		describeImagesOut: &ec2.DescribeImagesOutput{Images: []ec2types.Image{img}},
	}
	factory := func(_ string) kmaws.EC2AMIAPI { return mockClient }

	_, _, err := executeAMICmd(t, cfg, factory, nil, nil, []string{"delete", "ami-0targeted000", "--yes"})
	if err == nil {
		t.Fatal("expected error when deleting AMI referenced by a profile (no --force)")
	}
	if !strings.Contains(err.Error(), "rerun with --force") {
		t.Errorf("error message should mention --force; got: %v", err)
	}
	if mockClient.deregisterCalled {
		t.Error("DeregisterImage should NOT be called when profile refs exist without --force")
	}
}

// ============================================================
// Test 8: TestAMIDelete_ForceOverridesProfileRef
// ============================================================

func TestAMIDelete_ForceOverridesProfileRef(t *testing.T) {
	refProfile := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: dev
spec:
  lifecycle:
    ttl: 24h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
    ami: ami-0forcetest000
  execution: {}
  sourceAccess: {}
  network: {}
  identity: {}
  sidecars: {}
  observability: {}
  agent: {}
`
	dir := tempProfileDir(t, map[string]string{"dev-force": refProfile})
	cfg := amiTestConfig(t, dir)

	img := makeImage("ami-0forcetest000", "force-test", "dev", "sb-aaaa1111", time.Now().Add(-5*24*time.Hour), 20)
	mockClient := &mockEC2AMI{
		describeImagesOut: &ec2.DescribeImagesOutput{Images: []ec2types.Image{img}},
	}
	factory := func(_ string) kmaws.EC2AMIAPI { return mockClient }

	_, _, err := executeAMICmd(t, cfg, factory, nil, nil, []string{"delete", "ami-0forcetest000", "--force", "--yes"})
	if err != nil {
		t.Fatalf("expected no error with --force --yes; got: %v", err)
	}
	if !mockClient.deregisterCalled {
		t.Error("DeregisterImage should be called when --force is provided")
	}
}

// ============================================================
// Test 9: TestAMIDelete_DryRunDoesNotCallDelete
// ============================================================

func TestAMIDelete_DryRunDoesNotCallDelete(t *testing.T) {
	cfg := amiTestConfig(t)

	img := ec2types.Image{
		ImageId:      awssdk.String("ami-0dryrun00000"),
		Name:         awssdk.String("dry-run-ami"),
		CreationDate: awssdk.String(time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)),
		BlockDeviceMappings: []ec2types.BlockDeviceMapping{
			{Ebs: &ec2types.EbsBlockDevice{
				VolumeSize: awssdk.Int32(20),
				SnapshotId: awssdk.String("snap-abc123"),
			}},
		},
		Tags: []ec2types.Tag{
			{Key: awssdk.String("km:sandbox-id"), Value: awssdk.String("sb-aaaa1111")},
		},
	}

	mockClient := &mockEC2AMI{
		describeImagesOut: &ec2.DescribeImagesOutput{Images: []ec2types.Image{img}},
	}
	factory := func(_ string) kmaws.EC2AMIAPI { return mockClient }

	stdout, _, err := executeAMICmd(t, cfg, factory, nil, nil, []string{"delete", "ami-0dryrun00000", "--dry-run", "--yes"})
	if err != nil {
		t.Fatalf("dry-run should not error; got: %v", err)
	}
	if mockClient.deregisterCalled {
		t.Error("DeregisterImage should NOT be called with --dry-run")
	}
	if !strings.Contains(stdout, "would delete") {
		t.Errorf("expected 'would delete' in stdout; got: %q", stdout)
	}
	if !strings.Contains(stdout, "snap-abc123") {
		t.Errorf("expected snapshot ID in dry-run preview; got: %q", stdout)
	}
}

// ============================================================
// Test 10: TestAMIDelete_ConfirmPromptHonored
// ============================================================

func TestAMIDelete_ConfirmPromptHonored(t *testing.T) {
	cfg := amiTestConfig(t)

	img := ec2types.Image{
		ImageId:      awssdk.String("ami-0confirm0000"),
		Name:         awssdk.String("confirm-ami"),
		CreationDate: awssdk.String(time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)),
		BlockDeviceMappings: []ec2types.BlockDeviceMapping{
			{Ebs: &ec2types.EbsBlockDevice{
				VolumeSize: awssdk.Int32(20),
				SnapshotId: awssdk.String("snap-confirm"),
			}},
		},
		Tags: []ec2types.Tag{
			{Key: awssdk.String("km:sandbox-id"), Value: awssdk.String("sb-aaaa1111")},
		},
	}

	mockClient := &mockEC2AMI{
		describeImagesOut: &ec2.DescribeImagesOutput{Images: []ec2types.Image{img}},
	}
	factory := func(_ string) kmaws.EC2AMIAPI { return mockClient }

	// Build command manually so we can inject stdin with "n\n".
	var stdout bytes.Buffer
	amiCmd := NewAMICmdWithDeps(cfg, factory, nil, nil)
	amiCmd.SetOut(&stdout)
	amiCmd.SetIn(bytes.NewBufferString("n\n"))
	amiCmd.SetArgs([]string{"delete", "ami-0confirm0000"})
	err := amiCmd.Execute()

	if err == nil {
		t.Fatal("expected non-zero exit when user responds 'n' to prompt")
	}
	if mockClient.deregisterCalled {
		t.Error("DeregisterImage should NOT be called when user declines")
	}
}

// ============================================================
// Test 11: TestAMICopy_CallsCopyImageThenRetags
// ============================================================

func TestAMICopy_CallsCopyImageThenRetags(t *testing.T) {
	cfg := amiTestConfig(t)

	srcAMIID := "ami-0source000000"
	dstAMIID := "ami-0dest00000000"
	snapID := "snap-dst123"

	// src client: only DescribeImages is needed (to get source image name/tags).
	srcClient := &mockEC2AMI{
		describeImagesOut: &ec2.DescribeImagesOutput{
			Images: []ec2types.Image{
				{
					ImageId: awssdk.String(srcAMIID),
					Name:    awssdk.String("km-test-src"),
					Tags: []ec2types.Tag{
						{Key: awssdk.String("km:sandbox-id"), Value: awssdk.String("sb-src11111")},
						{Key: awssdk.String("km:profile"), Value: awssdk.String("dev")},
					},
				},
			},
		},
	}

	// dst client: CopyImage, then DescribeImages (waiter), then DescribeImages (tag discovery), CreateTags.
	dstClient := &mockEC2AMI{
		copyImageOut: &ec2.CopyImageOutput{ImageId: awssdk.String(dstAMIID)},
		// DescribeImages for waiter + describe returns the new AMI as available.
		describeImagesOut: &ec2.DescribeImagesOutput{
			Images: []ec2types.Image{
				{
					ImageId: awssdk.String(dstAMIID),
					State:   ec2types.ImageStateAvailable,
					BlockDeviceMappings: []ec2types.BlockDeviceMapping{
						{Ebs: &ec2types.EbsBlockDevice{SnapshotId: awssdk.String(snapID)}},
					},
				},
			},
		},
	}

	regionClients := map[string]*mockEC2AMI{
		"us-east-1": srcClient,
		"us-west-2": dstClient,
	}
	factory := func(r string) kmaws.EC2AMIAPI { return regionClients[r] }

	stdout, _, err := executeAMICmd(t, cfg, factory, nil, nil, []string{
		"copy", srcAMIID,
		"--from-region", "us-east-1",
		"--to-region", "us-west-2",
	})
	if err != nil {
		t.Fatalf("copy returned error: %v", err)
	}

	if !strings.Contains(stdout, dstAMIID) {
		t.Errorf("expected destination AMI ID in output; got: %q", stdout)
	}

	// CreateTags should have been called on the dst client (re-tagging).
	if !dstClient.createTagsCalled {
		t.Error("CreateTags should be called on the destination client after copy")
	}

	// Verify CopyImage was called before CreateTags on dst client.
	copyIdx := -1
	tagsIdx := -1
	for i, call := range dstClient.callOrder {
		if call == "CopyImage" {
			copyIdx = i
		}
		if call == "CreateTags" {
			tagsIdx = i
		}
	}
	if copyIdx == -1 || tagsIdx == -1 {
		t.Fatalf("expected both CopyImage and CreateTags calls; order: %v", dstClient.callOrder)
	}
	if copyIdx > tagsIdx {
		t.Errorf("CopyImage must be called before CreateTags; order: %v", dstClient.callOrder)
	}

	// Verify CreateTags includes source tags.
	if dstClient.createTagsInput != nil {
		var foundSandboxTag bool
		for _, tag := range dstClient.createTagsInput.Tags {
			if awssdk.ToString(tag.Key) == "km:sandbox-id" {
				foundSandboxTag = true
			}
		}
		if !foundSandboxTag {
			t.Error("CreateTags should include km:sandbox-id tag from source AMI")
		}
	}
}

// ============================================================
// Test 12: TestBakeFromSandbox_NonEC2Substrate_Errors
// ============================================================

func TestBakeFromSandbox_NonEC2Substrate_Errors(t *testing.T) {
	cfg := amiTestConfig(t)
	rec := kmaws.SandboxRecord{
		SandboxID: "sb-ecs11111",
		Substrate: "ecs",
		Region:    "us-east-1",
		Profile:   "dev",
	}

	_, err := BakeFromSandbox(context.Background(), cfg, rec, "sb-ecs11111", "dev", "v1.0.0")
	if err == nil {
		t.Fatal("expected error for non-EC2 substrate")
	}
	if !strings.Contains(err.Error(), "ec2") {
		t.Errorf("error should mention ec2; got: %v", err)
	}
}

// ============================================================
// Test 13: TestBakeFromSandbox_HappyPath
// ============================================================

func TestBakeFromSandbox_HappyPath(t *testing.T) {
	cfg := amiTestConfig(t)

	expectedAMIID := "ami-0baked99999"
	mockClient := &mockEC2AMI{
		createImageOut: &ec2.CreateImageOutput{ImageId: awssdk.String(expectedAMIID)},
		// DescribeImages for waiter — return available state immediately.
		describeImagesOut: &ec2.DescribeImagesOutput{
			Images: []ec2types.Image{
				{
					ImageId: awssdk.String(expectedAMIID),
					State:   ec2types.ImageStateAvailable,
				},
			},
		},
	}
	factory := func(_ string) kmaws.EC2AMIAPI { return mockClient }

	rec := kmaws.SandboxRecord{
		SandboxID: "sb-ec2aaaa",
		Substrate: "ec2",
		Region:    "us-east-1",
		Profile:   "dev",
		Resources: []string{"arn:aws:ec2:us-east-1:123456789012:instance/i-0abc123def456"},
	}

	amiID, err := bakeFromSandboxInternal(context.Background(), cfg, rec, "sb-ec2aaaa", "dev", "v1.0.0", "", 0, factory)
	if err != nil {
		t.Fatalf("bakeFromSandboxInternal returned error: %v", err)
	}
	if amiID != expectedAMIID {
		t.Errorf("expected AMI ID %q, got %q", expectedAMIID, amiID)
	}
}

// ============================================================
// Additional: TestParseAge
// ============================================================

func TestParseAge(t *testing.T) {
	cases := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"7d", 7 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"168h", 168 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"0d", 0, true},    // 0d is not > 0
		{"bad", 0, true},
		{"-1d", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			d, err := parseAge(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseAge(%q) expected error, got duration %v", tc.input, d)
				}
				return
			}
			if err != nil {
				t.Errorf("parseAge(%q) unexpected error: %v", tc.input, err)
				return
			}
			if d != tc.expected {
				t.Errorf("parseAge(%q) = %v, want %v", tc.input, d, tc.expected)
			}
		})
	}
}

// ============================================================
// Additional: TestFindProfilesReferencingAMI
// ============================================================

func TestFindProfilesReferencingAMI(t *testing.T) {
	targetAMI := "ami-0findme0000000"
	otherAMI := "ami-0other0000000"

	refProfile := fmt.Sprintf(`apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: dev
spec:
  lifecycle:
    ttl: 24h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.medium
    region: us-east-1
    ami: %s
  execution: {}
  sourceAccess: {}
  network: {}
  identity: {}
  sidecars: {}
  observability: {}
  agent: {}
`, targetAMI)

	otherProfile := fmt.Sprintf(`apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: prod
spec:
  lifecycle:
    ttl: 24h
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    instanceType: t3.large
    region: us-east-1
    ami: %s
  execution: {}
  sourceAccess: {}
  network: {}
  identity: {}
  sidecars: {}
  observability: {}
  agent: {}
`, otherAMI)

	dir := tempProfileDir(t, map[string]string{
		"dev-refs-ami": refProfile,
		"prod-other":   otherProfile,
	})

	refs, err := FindProfilesReferencingAMI([]string{dir}, targetAMI)
	if err != nil {
		t.Fatalf("FindProfilesReferencingAMI error: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 reference, got %d: %v", len(refs), refs)
	}
	if !strings.Contains(refs[0], "dev-refs-ami") {
		t.Errorf("expected reference to dev-refs-ami profile; got: %v", refs)
	}

	// No references to the other AMI.
	refs2, _ := FindProfilesReferencingAMI([]string{dir}, "ami-0notpresent")
	if len(refs2) != 0 {
		t.Errorf("expected 0 references for absent AMI, got %d", len(refs2))
	}
}
