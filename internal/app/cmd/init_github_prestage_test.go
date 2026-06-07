// init_github_prestage_test.go — Unit tests for km init GitHub profile pre-staging.
//
// Tests: TestPreStageGitHubProfiles_* (GH-COLD-CREATE defect 3).
package cmd_test

import (
	"context"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

// ============================================================
// Mock S3 PutObject client
// ============================================================

// mockS3PutObject records every S3 PutObject call.
type mockS3PutObject struct {
	puts []s3PutCall
	err  error
}

type s3PutCall struct {
	bucket string
	key    string
	body   []byte
}

func (m *mockS3PutObject) PutObject(_ context.Context, bucket, key string, body []byte) error {
	m.puts = append(m.puts, s3PutCall{bucket: bucket, key: key, body: body})
	return m.err
}

// Compile-time check: mockS3PutObject must satisfy cmd.S3ProfileUploader.
// cmd.S3ProfileUploader is NOT yet defined → RED.
var _ cmd.S3ProfileUploader = &mockS3PutObject{}

// ============================================================
// TestPreStageGitHubProfiles (GH-COLD-CREATE)
// ============================================================

// TestPreStageGitHubProfiles_DedupsProfileSlug verifies that when two github.repos
// entries share the same profile slug, preStageGitHubProfiles uploads exactly ONE
// copy of "github-profiles/{slug}/.km-profile.yaml" (dedup by slug).
func TestPreStageGitHubProfiles_DedupsProfileSlug(t *testing.T) {
	s3 := &mockS3PutObject{}

	// Two repos pointing at the same profile slug.
	repos := []cmd.GitHubRepoConfig{
		{Match: "myorg/frontend", Alias: "gh-shared", Profile: "github-review"},
		{Match: "myorg/backend", Alias: "gh-shared", Profile: "github-review"},
	}

	err := cmd.PreStageGitHubProfiles(context.Background(), repos, "my-artifacts-bucket", s3)
	if err != nil {
		t.Fatalf("PreStageGitHubProfiles returned error: %v", err)
	}

	// Count uploads of the profile YAML for "github-review".
	profileKey := "github-profiles/github-review/.km-profile.yaml"
	count := 0
	for _, p := range s3.puts {
		if p.key == profileKey {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 upload for %q; got %d (dedup failed)", profileKey, count)
	}

	// Total uploads: 1 profile YAML (no sops file in this config).
	if len(s3.puts) != 1 {
		t.Errorf("expected 1 total S3 PutObject call; got %d: %v",
			len(s3.puts), putKeys(s3.puts))
	}
}

// TestPreStageGitHubProfiles_CorrectBucketAndKey verifies the upload target uses
// the correct bucket and key pattern.
func TestPreStageGitHubProfiles_CorrectBucketAndKey(t *testing.T) {
	s3 := &mockS3PutObject{}

	repos := []cmd.GitHubRepoConfig{
		{Match: "myorg/repo", Alias: "gh-myrepo", Profile: "github-review"},
	}

	err := cmd.PreStageGitHubProfiles(context.Background(), repos, "my-artifacts-123", s3)
	if err != nil {
		t.Fatalf("PreStageGitHubProfiles returned error: %v", err)
	}

	if len(s3.puts) == 0 {
		t.Fatal("expected at least 1 S3 PutObject call; got 0")
	}

	// Verify bucket.
	if s3.puts[0].bucket != "my-artifacts-123" {
		t.Errorf("bucket = %q; want my-artifacts-123", s3.puts[0].bucket)
	}

	// Verify key prefix pattern.
	want := "github-profiles/github-review/.km-profile.yaml"
	if s3.puts[0].key != want {
		t.Errorf("key = %q; want %q", s3.puts[0].key, want)
	}
}

// TestPreStageGitHubProfiles_SOPSBundle verifies that when the profile specifies
// spec.secrets.sopsFile, preStageGitHubProfiles also uploads the SOPS bundle at
// "github-profiles/{slug}/.km-secrets-bundle.enc.yaml".
//
// NOTE: This test requires cmd.GitHubRepoConfig to have a SOPSFile field (or
// preStageGitHubProfiles to accept a map of profile-to-sops-path). The exact
// interface is for 98-04 to determine. For now, the test uses a SOPSFile field
// on the config struct.
func TestPreStageGitHubProfiles_SOPSBundle(t *testing.T) {
	s3 := &mockS3PutObject{}

	repos := []cmd.GitHubRepoConfig{
		{
			Match:    "myorg/repo",
			Alias:    "gh-myrepo",
			Profile:  "github-review",
			SOPSFile: "/tmp/secrets.enc.yaml", // triggers SOPS bundle upload
		},
	}

	err := cmd.PreStageGitHubProfiles(context.Background(), repos, "my-artifacts-123", s3)
	if err != nil {
		t.Fatalf("PreStageGitHubProfiles returned error: %v", err)
	}

	// Expect both the profile YAML and the SOPS bundle.
	keys := putKeys(s3.puts)
	if !containsKey(keys, "github-profiles/github-review/.km-profile.yaml") {
		t.Errorf("expected profile YAML upload; got keys: %v", keys)
	}
	if !containsKey(keys, "github-profiles/github-review/.km-secrets-bundle.enc.yaml") {
		t.Errorf("expected SOPS bundle upload; got keys: %v", keys)
	}
}

// TestPreStageGitHubProfiles_EmptyRepos verifies no uploads when repos list is empty.
func TestPreStageGitHubProfiles_EmptyRepos(t *testing.T) {
	s3 := &mockS3PutObject{}
	err := cmd.PreStageGitHubProfiles(context.Background(), nil, "my-bucket", s3)
	if err != nil {
		t.Fatalf("PreStageGitHubProfiles(nil) returned error: %v", err)
	}
	if len(s3.puts) != 0 {
		t.Errorf("expected 0 S3 calls for empty repos; got %d", len(s3.puts))
	}
}

// putKeys extracts the S3 key from each put call.
func putKeys(puts []s3PutCall) []string {
	keys := make([]string, len(puts))
	for i, p := range puts {
		keys[i] = p.key
	}
	return keys
}

func containsKey(haystack []string, needle string) bool {
	for _, s := range haystack {
		if strings.EqualFold(s, needle) {
			return true
		}
	}
	return false
}

