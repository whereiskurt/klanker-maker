// sandbox_dynamo_launch_account_test.go — Phase 126 round-trip tests for the
// SandboxMetadata.LaunchAccount field's DynamoDB marshal/unmarshal chokepoint.
//
// Pattern mirrors sandbox_dynamo_network_test.go (NetworkPlacement): a
// dedicated per-concern test file for one attribute's round trip.
//
// These tests exist to lock down the exact footgun this repo has hit before
// (project_sandboxmetadata_lossy_roundtrip): a per-sandbox DynamoDB attribute
// that is wired into only some of the marshal/unmarshal chokepoints silently
// disappears on the next read-modify-write lifecycle write (pause, resume,
// extend, ttl-handler). Both `km destroy` and the ttl-handler read
// launch_account to know which account's launcher role to assume before they
// can even find the linked-account instance, let alone reap it — a dropped
// attribute leaves a running GPU instance billing in the linked account with
// nothing in the home account able to reach it.
package aws_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// TestLaunchAccount_RoundTrip verifies a non-empty launch account survives a
// full marshal → unmarshal cycle through ReadSandboxMetadataDynamo, and that
// the DynamoDB attribute type is specifically S (a wrong type reads back as
// absent rather than erroring — the exact silent-failure mode these tests
// exist to catch).
func TestLaunchAccount_RoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := &kmaws.SandboxMetadata{
		SandboxID:     "sb-launch-account",
		ProfileName:   "dev",
		Substrate:     "ec2",
		Region:        "us-east-1",
		CreatedAt:     now,
		LaunchAccount: "gpu-partner",
	}

	item := mustMarshalSandboxItemFull(t, meta)

	attr, ok := item["launch_account"]
	if !ok {
		t.Fatalf("marshalSandboxItem did not emit 'launch_account' key for LaunchAccount=%q", "gpu-partner")
	}
	sAttr, isS := attr.(*dynamodbtypes.AttributeValueMemberS)
	if !isS {
		t.Fatalf("expected launch_account to be AttributeValueMemberS, got %T", attr)
	}
	if sAttr.Value != "gpu-partner" {
		t.Errorf("launch_account S value: got %q, want %q", sAttr.Value, "gpu-partner")
	}

	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(),
		&mockSandboxMetadataAPI{getItemOutput: &dynamodb.GetItemOutput{Item: item}},
		"km-sandboxes", "sb-launch-account")
	if err != nil {
		t.Fatalf("ReadSandboxMetadataDynamo: %v", err)
	}
	if got.LaunchAccount != "gpu-partner" {
		t.Errorf("LaunchAccount round-trip: got %q, want %q", got.LaunchAccount, "gpu-partner")
	}
}

// TestLaunchAccount_UnmarshalFromItem verifies an item carrying launch_account
// unmarshals into the field directly (isolating the unmarshal helper from
// the marshal path exercised by TestLaunchAccount_RoundTrip).
func TestLaunchAccount_UnmarshalFromItem(t *testing.T) {
	now := time.Now().UTC()
	item := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":     &dynamodbtypes.AttributeValueMemberS{Value: "sb-launch-unmarshal"},
		"profile_name":   &dynamodbtypes.AttributeValueMemberS{Value: "dev"},
		"substrate":      &dynamodbtypes.AttributeValueMemberS{Value: "ec2"},
		"region":         &dynamodbtypes.AttributeValueMemberS{Value: "us-east-1"},
		"created_at":     &dynamodbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339)},
		"launch_account": &dynamodbtypes.AttributeValueMemberS{Value: "gpu-partner"},
	}

	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(),
		&mockSandboxMetadataAPI{getItemOutput: &dynamodb.GetItemOutput{Item: item}},
		"km-sandboxes", "sb-launch-unmarshal")
	if err != nil {
		t.Fatalf("ReadSandboxMetadataDynamo: %v", err)
	}
	if got.LaunchAccount != "gpu-partner" {
		t.Errorf("LaunchAccount: got %q, want %q", got.LaunchAccount, "gpu-partner")
	}
}

// TestLaunchAccount_OmittedWhenEmpty verifies an empty LaunchAccount produces
// NO launch_account key in the marshalled item, so home-account sandbox rows
// stay byte-identical.
func TestLaunchAccount_OmittedWhenEmpty(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := &kmaws.SandboxMetadata{
		SandboxID:     "sb-launch-empty",
		ProfileName:   "dev",
		Substrate:     "ec2",
		Region:        "us-east-1",
		CreatedAt:     now,
		LaunchAccount: "", // explicit empty (home account)
	}

	item := mustMarshalSandboxItemFull(t, meta)
	if _, ok := item["launch_account"]; ok {
		t.Error("launch_account should be omitted when LaunchAccount is empty")
	}
}

// TestLaunchAccount_AbsentAttributeIsEmpty is the footgun guard: a pre-126
// DynamoDB row has no launch_account attribute at all. It must unmarshal
// cleanly to an empty LaunchAccount (treated as the home account), never a
// panic or an error.
func TestLaunchAccount_AbsentAttributeIsEmpty(t *testing.T) {
	now := time.Now().UTC()
	// Build a pre-126 item directly (no launch_account key at all) rather
	// than marshalling, to simulate a row written before this field existed.
	item := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":   &dynamodbtypes.AttributeValueMemberS{Value: "sb-launch-pre126"},
		"profile_name": &dynamodbtypes.AttributeValueMemberS{Value: "dev"},
		"substrate":    &dynamodbtypes.AttributeValueMemberS{Value: "ec2"},
		"region":       &dynamodbtypes.AttributeValueMemberS{Value: "us-east-1"},
		"created_at":   &dynamodbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339)},
	}

	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(),
		&mockSandboxMetadataAPI{getItemOutput: &dynamodb.GetItemOutput{Item: item}},
		"km-sandboxes", "sb-launch-pre126")
	if err != nil {
		t.Fatalf("ReadSandboxMetadataDynamo on a pre-126 row must not error: %v", err)
	}
	if got.LaunchAccount != "" {
		t.Errorf("LaunchAccount on a pre-126 row: got %q, want empty string", got.LaunchAccount)
	}
}

// TestLaunchAccount_SurvivesDoubleRoundTrip is the important one — it
// simulates the resume/extend/ttl-handler full-row PutItem cycle that has
// silently dropped fields in this codebase before
// (project_sandboxmetadata_lossy_roundtrip). marshal → unmarshal → marshal
// again: the launch_account attribute must still be present with the same
// value after the SECOND marshal. If it disappeared here, `km destroy` and
// the ttl-handler would stop being able to reach the linked account on the
// next lifecycle write, leaving a running GPU instance billing with nothing
// able to reap it.
func TestLaunchAccount_SurvivesDoubleRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := &kmaws.SandboxMetadata{
		SandboxID:     "sb-launch-remarshal",
		ProfileName:   "dev",
		Substrate:     "ec2",
		Region:        "us-east-1",
		CreatedAt:     now,
		LaunchAccount: "gpu-partner",
	}

	// First marshal (simulates km create's initial WriteSandboxMetadataDynamo).
	firstItem := mustMarshalSandboxItemFull(t, meta)

	// Read it back (simulates resume.go / extend.go / ttl-handler reading the
	// row before mutating unrelated fields).
	roundTripped, err := kmaws.ReadSandboxMetadataDynamo(context.Background(),
		&mockSandboxMetadataAPI{getItemOutput: &dynamodb.GetItemOutput{Item: firstItem}},
		"km-sandboxes", "sb-launch-remarshal")
	if err != nil {
		t.Fatalf("ReadSandboxMetadataDynamo: %v", err)
	}
	if roundTripped.LaunchAccount != "gpu-partner" {
		t.Fatalf("after first round-trip: LaunchAccount = %q, want %q", roundTripped.LaunchAccount, "gpu-partner")
	}

	// Second marshal (simulates the full-row PutItem a lifecycle path issues
	// after mutating some unrelated field, e.g. TTLExpiry on extend).
	secondItem := mustMarshalSandboxItemFull(t, roundTripped)

	attr, ok := secondItem["launch_account"]
	if !ok {
		t.Fatal("launch_account attribute was dropped on the SECOND marshal (the lossy-round-trip footgun)")
	}
	sAttr, isS := attr.(*dynamodbtypes.AttributeValueMemberS)
	if !isS {
		t.Fatalf("expected launch_account to be AttributeValueMemberS after remarshal, got %T", attr)
	}
	if sAttr.Value != "gpu-partner" {
		t.Errorf("launch_account after remarshal: got %q, want %q", sAttr.Value, "gpu-partner")
	}
}

// TestLaunchAccount_ListAllSandboxMetadataDynamo_RoundTrip covers the
// list-path unmarshal entry point (ListAllSandboxMetadataDynamo), not just
// the single-item read path exercised by TestLaunchAccount_RoundTrip.
func TestLaunchAccount_ListAllSandboxMetadataDynamo_RoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := &kmaws.SandboxMetadata{
		SandboxID:     "sb-launch-scan",
		ProfileName:   "dev",
		Substrate:     "ec2",
		Region:        "us-east-1",
		CreatedAt:     now,
		LaunchAccount: "gpu-partner",
	}
	item := mustMarshalSandboxItemFull(t, meta)

	mock := &mockSandboxMetadataAPI{
		scanOutputs: []*dynamodb.ScanOutput{
			{Items: []map[string]dynamodbtypes.AttributeValue{item}},
		},
	}

	metas, err := kmaws.ListAllSandboxMetadataDynamo(context.Background(), mock, "km-sandboxes")
	if err != nil {
		t.Fatalf("ListAllSandboxMetadataDynamo: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("expected 1 metadata record, got %d", len(metas))
	}
	if metas[0].LaunchAccount != "gpu-partner" {
		t.Errorf("ListAllSandboxMetadataDynamo LaunchAccount: got %q, want %q", metas[0].LaunchAccount, "gpu-partner")
	}
}
