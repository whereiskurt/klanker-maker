package aws_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ---- realMockS3 is a proper implementation of S3ListAPI for testing ----

type realMockS3 struct {
	objects  map[string][]byte
	prefixes []string
	listErr  error
}

func (m *realMockS3) ListObjectsV2(
	_ context.Context,
	input *s3.ListObjectsV2Input,
	_ ...func(*s3.Options),
) (*s3.ListObjectsV2Output, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	out := &s3.ListObjectsV2Output{}
	for _, p := range m.prefixes {
		pp := p // capture loop var
		out.CommonPrefixes = append(out.CommonPrefixes, s3types.CommonPrefix{Prefix: awssdk.String(pp)})
	}
	return out, nil
}

func (m *realMockS3) GetObject(
	_ context.Context,
	input *s3.GetObjectInput,
	_ ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	if input.Key == nil {
		return nil, io.ErrUnexpectedEOF
	}
	data, ok := m.objects[*input.Key]
	if !ok {
		return nil, &s3NotFoundError{key: *input.Key}
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(data)),
	}, nil
}

// s3NotFoundError simulates S3 NoSuchKey.
type s3NotFoundError struct{ key string }

func (e *s3NotFoundError) Error() string { return "NoSuchKey: " + e.key }

// makeMockS3WithSandboxes builds a mock S3 containing the given sandbox metadata objects.
func makeMockS3WithSandboxes(sandboxes []kmaws.SandboxMetadata) *realMockS3 {
	objects := make(map[string][]byte)
	prefixes := make([]string, 0, len(sandboxes))

	for _, m := range sandboxes {
		data, _ := json.Marshal(m)
		key := "tf-km/sandboxes/" + m.SandboxID + "/metadata.json"
		objects[key] = data
		prefixes = append(prefixes, "tf-km/sandboxes/"+m.SandboxID+"/")
	}

	return &realMockS3{objects: objects, prefixes: prefixes}
}

// ---- SandboxMetadata marshaling tests ----

func TestSandboxMetadata_WithAlias_MarshalUnmarshal(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	meta := kmaws.SandboxMetadata{
		SandboxID:   "sb-a1b2c3d4",
		ProfileName: "claude-dev",
		Substrate:   "ec2",
		Region:      "us-east-1",
		Status:      "running",
		CreatedAt:   now,
		Alias:       "orc",
	}

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var got kmaws.SandboxMetadata
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if got.Alias != "orc" {
		t.Errorf("Alias = %q, want %q", got.Alias, "orc")
	}
	if got.SandboxID != "sb-a1b2c3d4" {
		t.Errorf("SandboxID = %q, want %q", got.SandboxID, "sb-a1b2c3d4")
	}
}

func TestSandboxMetadata_WithoutAlias_BackwardsCompat(t *testing.T) {
	// Old metadata JSON without alias field
	raw := `{"sandbox_id":"sb-old12345","profile_name":"dev","substrate":"ec2","region":"us-east-1","created_at":"2025-01-01T00:00:00Z"}`
	var meta kmaws.SandboxMetadata
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if meta.Alias != "" {
		t.Errorf("expected empty Alias for old metadata, got %q", meta.Alias)
	}
	if meta.SandboxID != "sb-old12345" {
		t.Errorf("SandboxID = %q, want %q", meta.SandboxID, "sb-old12345")
	}
}

// ---- ResolveSandboxAlias tests ----

func TestResolveSandboxAlias_Found(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	mock := makeMockS3WithSandboxes([]kmaws.SandboxMetadata{
		{SandboxID: "sb-a1b2c3d4", ProfileName: "dev", Substrate: "ec2", Region: "us-east-1", Status: "running", CreatedAt: now, Alias: "orc"},
		{SandboxID: "sb-b2c3d4e5", ProfileName: "dev", Substrate: "ec2", Region: "us-east-1", Status: "running", CreatedAt: now, Alias: "wrkr-1"},
	})

	got, err := kmaws.ResolveSandboxAlias(context.Background(), mock, "test-bucket", "orc")
	if err != nil {
		t.Fatalf("ResolveSandboxAlias returned error: %v", err)
	}
	if got != "sb-a1b2c3d4" {
		t.Errorf("resolved = %q, want %q", got, "sb-a1b2c3d4")
	}
}

func TestResolveSandboxAlias_NotFound(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	mock := makeMockS3WithSandboxes([]kmaws.SandboxMetadata{
		{SandboxID: "sb-a1b2c3d4", ProfileName: "dev", Substrate: "ec2", Region: "us-east-1", Status: "running", CreatedAt: now, Alias: "orc"},
	})

	_, err := kmaws.ResolveSandboxAlias(context.Background(), mock, "test-bucket", "nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent alias, got nil")
	}
}

func TestResolveSandboxAlias_Duplicate(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	// Two sandboxes with same alias (shouldn't happen in practice but defensive)
	mock := makeMockS3WithSandboxes([]kmaws.SandboxMetadata{
		{SandboxID: "sb-a1b2c3d4", ProfileName: "dev", Substrate: "ec2", Region: "us-east-1", Status: "running", CreatedAt: now, Alias: "orc"},
		{SandboxID: "sb-b2c3d4e5", ProfileName: "dev", Substrate: "ec2", Region: "us-east-1", Status: "running", CreatedAt: now, Alias: "orc"},
	})

	_, err := kmaws.ResolveSandboxAlias(context.Background(), mock, "test-bucket", "orc")
	if err == nil {
		t.Fatal("expected error for duplicate alias, got nil")
	}
}

// ---- NextAliasFromTemplate tests ----

func TestNextAliasFromTemplate_Empty(t *testing.T) {
	got := kmaws.NextAliasFromTemplate("wrkr", []string{})
	if got != "wrkr-1" {
		t.Errorf("NextAliasFromTemplate(%q, []) = %q, want %q", "wrkr", got, "wrkr-1")
	}
}

func TestNextAliasFromTemplate_Gap(t *testing.T) {
	// Has wrkr-1 and wrkr-3, should return wrkr-4 (max+1)
	got := kmaws.NextAliasFromTemplate("wrkr", []string{"wrkr-1", "wrkr-3"})
	if got != "wrkr-4" {
		t.Errorf("NextAliasFromTemplate = %q, want %q", got, "wrkr-4")
	}
}

func TestNextAliasFromTemplate_NoMatch(t *testing.T) {
	// Existing aliases don't match template, starts at 1
	got := kmaws.NextAliasFromTemplate("orc", []string{"wrkr-1", "dev-2"})
	if got != "orc-1" {
		t.Errorf("NextAliasFromTemplate = %q, want %q", got, "orc-1")
	}
}

func TestNextAliasFromTemplate_Sequential(t *testing.T) {
	// Has wrkr-1, wrkr-2, should return wrkr-3
	got := kmaws.NextAliasFromTemplate("wrkr", []string{"wrkr-1", "wrkr-2"})
	if got != "wrkr-3" {
		t.Errorf("NextAliasFromTemplate = %q, want %q", got, "wrkr-3")
	}
}
