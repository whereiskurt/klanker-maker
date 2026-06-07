// aws_adapters_test.go — Phase 98 tests for EventBridgeAdapter cold-create fixes.
//
// BUILD TAG: phase98_wave0
// This file tests the BROKEN behavior in EventBridgeAdapter.PutSandboxCreate
// which 98-04 will fix:
//
//   TODAY (BROKEN):
//     detail.SandboxID = "" (missing)
//     detail.ArtifactPrefix = a.ArtifactPrefix + "/profiles/" + profile + ".yaml" (doubled path)
//
//   AFTER 98-04 (CORRECT):
//     detail.sandbox_id = "gh-" + 8 hex chars (e.g. "gh-a1b2c3d4")
//     detail.artifact_prefix = "github-profiles/" + profileSlug (no doubling)
//     detail.artifact_bucket = non-empty
//
// These tests will PASS once 98-04 fixes EventBridgeAdapter.PutSandboxCreate.
// Until then they are RED (the asserted contract is violated).
//
// HANDOFF TO 98-04:
//   1. Fix EventBridgeAdapter.PutSandboxCreate to generate sandbox_id = "gh-"+8hex.
//   2. Fix ArtifactPrefix to use "github-profiles/"+profileSlug (not the doubled path).
//   3. Set detail.ArtifactBucket from the EventBridgeAdapter.ArtifactBucket field.
//   4. Remove the `//go:build phase98_wave0` constraint from THIS file.
package bridge_test

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// ============================================================
// Fake EventBridge client
// ============================================================

// fakeEventBridgeClient captures the most recent PutEvents input.
type fakeEventBridgeClient struct {
	lastInput *eventbridge.PutEventsInput
	err       error
}

func (f *fakeEventBridgeClient) PutEvents(_ context.Context, params *eventbridge.PutEventsInput, _ ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error) {
	f.lastInput = params
	if f.err != nil {
		return nil, f.err
	}
	return &eventbridge.PutEventsOutput{FailedEntryCount: 0}, nil
}

// capturedDetail extracts the SandboxCreateDetail JSON from the last PutEvents call.
func capturedDetail(t *testing.T, fake *fakeEventBridgeClient) map[string]any {
	t.Helper()
	if fake.lastInput == nil || len(fake.lastInput.Entries) == 0 {
		t.Fatal("PutEvents was not called or has no entries")
	}
	detailStr := *fake.lastInput.Entries[0].Detail
	var detail map[string]any
	if err := json.Unmarshal([]byte(detailStr), &detail); err != nil {
		t.Fatalf("detail JSON is malformed: %v\nbody: %s", err, detailStr)
	}
	return detail
}

// ============================================================
// TestEventBridgeAdapter_SandboxID (GH-COLD-CREATE)
// ============================================================

// TestEventBridgeAdapter_SandboxID verifies that PutSandboxCreate emits a
// sandbox_id matching ^gh-[0-9a-f]{8}$ in the EventBridge detail JSON.
//
// TODAY: detail.sandbox_id = "" → this test is RED.
// AFTER 98-04: detail.sandbox_id = "gh-" + 8 hex chars → GREEN.
func TestEventBridgeAdapter_SandboxID(t *testing.T) {
	fake := &fakeEventBridgeClient{}
	adapter := &bridge.EventBridgeAdapter{
		Client:         fake,
		ArtifactBucket: "my-artifacts-bucket",
		ArtifactPrefix: "github-profiles",
	}

	err := adapter.PutSandboxCreate(context.Background(), "gh-shared", "github-review", `{"source":"github"}`)
	if err != nil {
		t.Fatalf("PutSandboxCreate returned error: %v", err)
	}

	detail := capturedDetail(t, fake)

	sandboxID, _ := detail["sandbox_id"].(string)
	if sandboxID == "" {
		t.Fatal("detail.sandbox_id is empty; want 'gh-' + 8 hex chars (98-04 must set this)")
	}

	pattern := regexp.MustCompile(`^gh-[0-9a-f]{8}$`)
	if !pattern.MatchString(sandboxID) {
		t.Errorf("detail.sandbox_id = %q; want pattern ^gh-[0-9a-f]{8}$", sandboxID)
	}
}

// ============================================================
// TestEventBridgeAdapter_ArtifactPrefix (GH-COLD-CREATE)
// ============================================================

// TestEventBridgeAdapter_ArtifactPrefix verifies that PutSandboxCreate sets
// detail.artifact_prefix = "github-profiles/" + profileSlug (no doubled path)
// and detail.artifact_bucket is non-empty.
//
// TODAY:
//   artifact_prefix = ArtifactPrefix + "/profiles/" + profile + ".yaml"
//   e.g. "github-profiles/profiles/github-review.yaml" — DOUBLED PATH (broken)
//
// AFTER 98-04:
//   artifact_prefix = "github-profiles/" + profileSlug
//   e.g. "github-profiles/github-review" — CORRECT.
func TestEventBridgeAdapter_ArtifactPrefix(t *testing.T) {
	fake := &fakeEventBridgeClient{}
	adapter := &bridge.EventBridgeAdapter{
		Client:         fake,
		ArtifactBucket: "my-artifacts-bucket",
		ArtifactPrefix: "github-profiles",
	}

	err := adapter.PutSandboxCreate(context.Background(), "gh-shared", "github-review", `{"source":"github"}`)
	if err != nil {
		t.Fatalf("PutSandboxCreate returned error: %v", err)
	}

	detail := capturedDetail(t, fake)

	// artifact_prefix must NOT contain the doubled "/profiles/" path segment.
	prefix, _ := detail["artifact_prefix"].(string)
	if prefix == "" {
		t.Fatal("detail.artifact_prefix is empty; want 'github-profiles/github-review'")
	}

	// The buggy path is "github-profiles/profiles/github-review.yaml".
	// The correct path is "github-profiles/github-review".
	if prefix == "github-profiles/profiles/github-review.yaml" || prefix == "" {
		t.Errorf("detail.artifact_prefix = %q; want 'github-profiles/github-review' (no /profiles/ doubling)", prefix)
	}

	// Must not end with .yaml (the prefix is a directory, not a file path).
	if len(prefix) > 5 && prefix[len(prefix)-5:] == ".yaml" {
		t.Errorf("detail.artifact_prefix = %q; must not end with .yaml (it is a prefix, not a file)", prefix)
	}

	// artifact_bucket must be non-empty.
	bucket, _ := detail["artifact_bucket"].(string)
	if bucket == "" {
		t.Error("detail.artifact_bucket is empty; want non-empty bucket name from EventBridgeAdapter.ArtifactBucket")
	}
	if bucket != "my-artifacts-bucket" {
		t.Errorf("detail.artifact_bucket = %q; want 'my-artifacts-bucket'", bucket)
	}
}
