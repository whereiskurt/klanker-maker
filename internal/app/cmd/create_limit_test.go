package cmd

// checkSandboxLimit unit tests live in package cmd (not cmd_test) so they can access
// the unexported checkSandboxLimit helper directly using the mockS3ForLimit below.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// sandboxMeta is a minimal SandboxMetadata-compatible struct for test JSON payloads.
// Using the real pkg/aws.SandboxMetadata fields that ListAllSandboxesByS3 reads.
type sandboxMeta struct {
	SandboxID   string    `json:"sandbox_id"`
	ProfileName string    `json:"profile_name"`
	Substrate   string    `json:"substrate"`
	Region      string    `json:"region"`
	Status      string    `json:"status,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// mockS3ForLimit implements aws.S3ListAPI for checkSandboxLimit unit tests.
// ListObjectsV2 returns CommonPrefixes (as used by ListAllSandboxesByS3 with Delimiter="/").
// GetObject returns the JSON metadata for the matched sandbox.
type mockS3ForLimit struct {
	metas []sandboxMeta
}

func (m *mockS3ForLimit) ListObjectsV2(ctx context.Context, input *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	out := &s3.ListObjectsV2Output{}
	for i := range m.metas {
		prefix := "tf-km/sandboxes/" + m.metas[i].SandboxID + "/"
		pfxCopy := prefix
		out.CommonPrefixes = append(out.CommonPrefixes, s3types.CommonPrefix{Prefix: &pfxCopy})
	}
	return out, nil
}

func (m *mockS3ForLimit) GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := ""
	if input.Key != nil {
		key = *input.Key
	}
	for i := range m.metas {
		if strings.Contains(key, m.metas[i].SandboxID) {
			data, _ := json.Marshal(m.metas[i])
			return &s3.GetObjectOutput{
				Body: io.NopCloser(strings.NewReader(string(data))),
			}, nil
		}
	}
	// Unknown sandbox — return empty JSON so it defaults to "running" (backward compat)
	return &s3.GetObjectOutput{
		Body: io.NopCloser(strings.NewReader("{}")),
	}, nil
}

// makeMetas returns n sandbox metadata records all with the given status.
func makeMetas(n int, status string) []sandboxMeta {
	metas := make([]sandboxMeta, n)
	for i := range metas {
		metas[i] = sandboxMeta{
			SandboxID:   fmt.Sprintf("sb-test%04d", i),
			ProfileName: "test-profile",
			Substrate:   "ec2spot",
			Region:      "us-east-1",
			Status:      status,
			CreatedAt:   time.Now(),
		}
	}
	return metas
}

// TestCheckSandboxLimit_AtLimit verifies that 10 active sandboxes with max=10 returns an error.
func TestCheckSandboxLimit_AtLimit(t *testing.T) {
	mock := &mockS3ForLimit{metas: makeMetas(10, "running")}

	count, err := checkSandboxLimit(context.Background(), mock, "test-bucket", 10)
	if err == nil {
		t.Fatal("expected error when sandbox limit reached (10/10), got nil")
	}
	if !strings.Contains(err.Error(), "sandbox limit reached") {
		t.Errorf("error should contain 'sandbox limit reached', got: %v", err)
	}
	if !strings.Contains(err.Error(), "10/10") {
		t.Errorf("error should contain '10/10', got: %v", err)
	}
	if count != 10 {
		t.Errorf("expected count=10, got %d", count)
	}
}

// TestCheckSandboxLimit_BelowLimit verifies that 9 active sandboxes with max=10 returns nil.
func TestCheckSandboxLimit_BelowLimit(t *testing.T) {
	mock := &mockS3ForLimit{metas: makeMetas(9, "running")}

	_, err := checkSandboxLimit(context.Background(), mock, "test-bucket", 10)
	if err != nil {
		t.Errorf("expected nil error when below limit (9/10), got: %v", err)
	}
}

// TestCheckSandboxLimit_Unlimited verifies that max=0 skips the check entirely.
func TestCheckSandboxLimit_Unlimited(t *testing.T) {
	mock := &mockS3ForLimit{metas: makeMetas(100, "running")}

	_, err := checkSandboxLimit(context.Background(), mock, "test-bucket", 0)
	if err != nil {
		t.Errorf("expected nil error when max=0 (unlimited), got: %v", err)
	}
}

// TestCheckSandboxLimit_DestroyedNotCounted verifies that destroyed sandboxes
// are not counted toward the active limit.
func TestCheckSandboxLimit_DestroyedNotCounted(t *testing.T) {
	metas := []sandboxMeta{
		{SandboxID: "sb-active01", ProfileName: "test", Status: "running", CreatedAt: time.Now(), Substrate: "ec2spot", Region: "us-east-1"},
		{SandboxID: "sb-active02", ProfileName: "test", Status: "running", CreatedAt: time.Now(), Substrate: "ec2spot", Region: "us-east-1"},
		{SandboxID: "sb-gone0001", ProfileName: "test", Status: "destroyed", CreatedAt: time.Now(), Substrate: "ec2spot", Region: "us-east-1"},
		{SandboxID: "sb-gone0002", ProfileName: "test", Status: "destroyed", CreatedAt: time.Now(), Substrate: "ec2spot", Region: "us-east-1"},
	}
	mock := &mockS3ForLimit{metas: metas}

	// 4 total sandboxes, 2 active, 2 destroyed — max=2 should return nil (2 active, not >= 3)
	_, err := checkSandboxLimit(context.Background(), mock, "test-bucket", 3)
	if err != nil {
		t.Errorf("expected nil when active(2) < max(3) after excluding destroyed, got: %v", err)
	}
}

// Ensure the mock satisfies the S3ListAPI interface at compile time.
var _ awspkg.S3ListAPI = (*mockS3ForLimit)(nil)
