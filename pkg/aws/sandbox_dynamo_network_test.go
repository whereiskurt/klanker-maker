// sandbox_dynamo_network_test.go — Phase 125 round-trip tests for the
// SandboxMetadata.NetworkPlacement field's DynamoDB marshal/unmarshal chokepoint.
//
// Pattern mirrors sandbox_dynamo_allow_test.go (SlackAllow): a dedicated
// per-concern test file for one attribute's round trip.
//
// These tests exist to lock down the exact footgun this repo has hit before
// (project_sandboxmetadata_lossy_roundtrip): a per-sandbox DynamoDB attribute
// that is wired into only some of the marshal/unmarshal chokepoints silently
// disappears on the next read-modify-write lifecycle write (pause, resume,
// extend, ttl-handler). The `km init` refuse-to-disable-NAT guard (Plan 06)
// finds running private sandboxes by reading network_placement, so a dropped
// attribute would let the operator remove NAT out from under a live private
// sandbox with no warning.
package aws_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// TestNetworkPlacement_PrivateRoundTrip verifies "private" survives a full
// marshal → unmarshal cycle through ReadSandboxMetadataDynamo, and that the
// DynamoDB attribute type is specifically S (a wrong type reads back as
// absent rather than erroring — the exact silent-failure mode these tests
// exist to catch).
func TestNetworkPlacement_PrivateRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := &kmaws.SandboxMetadata{
		SandboxID:        "sb-net-private",
		ProfileName:      "dev",
		Substrate:        "ec2",
		Region:           "us-east-1",
		CreatedAt:        now,
		NetworkPlacement: "private",
	}

	item := mustMarshalSandboxItemFull(t, meta)

	attr, ok := item["network_placement"]
	if !ok {
		t.Fatalf("marshalSandboxItem did not emit 'network_placement' key for NetworkPlacement=%q", "private")
	}
	sAttr, isS := attr.(*dynamodbtypes.AttributeValueMemberS)
	if !isS {
		t.Fatalf("expected network_placement to be AttributeValueMemberS, got %T", attr)
	}
	if sAttr.Value != "private" {
		t.Errorf("network_placement S value: got %q, want %q", sAttr.Value, "private")
	}

	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(),
		&mockSandboxMetadataAPI{getItemOutput: &dynamodb.GetItemOutput{Item: item}},
		"km-sandboxes", "sb-net-private")
	if err != nil {
		t.Fatalf("ReadSandboxMetadataDynamo: %v", err)
	}
	if got.NetworkPlacement != "private" {
		t.Errorf("NetworkPlacement round-trip: got %q, want %q", got.NetworkPlacement, "private")
	}
}

// TestNetworkPlacement_PublicRoundTrip verifies "public" survives the same
// cycle as TestNetworkPlacement_PrivateRoundTrip.
func TestNetworkPlacement_PublicRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := &kmaws.SandboxMetadata{
		SandboxID:        "sb-net-public",
		ProfileName:      "dev",
		Substrate:        "ec2",
		Region:           "us-east-1",
		CreatedAt:        now,
		NetworkPlacement: "public",
	}

	item := mustMarshalSandboxItemFull(t, meta)

	attr, ok := item["network_placement"]
	if !ok {
		t.Fatalf("marshalSandboxItem did not emit 'network_placement' key for NetworkPlacement=%q", "public")
	}
	sAttr, isS := attr.(*dynamodbtypes.AttributeValueMemberS)
	if !isS {
		t.Fatalf("expected network_placement to be AttributeValueMemberS, got %T", attr)
	}
	if sAttr.Value != "public" {
		t.Errorf("network_placement S value: got %q, want %q", sAttr.Value, "public")
	}

	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(),
		&mockSandboxMetadataAPI{getItemOutput: &dynamodb.GetItemOutput{Item: item}},
		"km-sandboxes", "sb-net-public")
	if err != nil {
		t.Fatalf("ReadSandboxMetadataDynamo: %v", err)
	}
	if got.NetworkPlacement != "public" {
		t.Errorf("NetworkPlacement round-trip: got %q, want %q", got.NetworkPlacement, "public")
	}
}

// TestNetworkPlacement_OmittedWhenEmpty verifies an empty NetworkPlacement
// produces NO network_placement key in the marshalled item, so rows for
// sandboxes that never set it stay byte-identical.
func TestNetworkPlacement_OmittedWhenEmpty(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := &kmaws.SandboxMetadata{
		SandboxID:        "sb-net-empty",
		ProfileName:      "dev",
		Substrate:        "ec2",
		Region:           "us-east-1",
		CreatedAt:        now,
		NetworkPlacement: "", // explicit empty
	}

	item := mustMarshalSandboxItemFull(t, meta)
	if _, ok := item["network_placement"]; ok {
		t.Error("network_placement should be omitted when NetworkPlacement is empty")
	}
}

// TestNetworkPlacement_AbsentAttributeIsEmpty is the footgun guard: a
// pre-125 DynamoDB row has no network_placement attribute at all. It must
// unmarshal cleanly to an empty NetworkPlacement (treated as public by
// callers), never an error.
func TestNetworkPlacement_AbsentAttributeIsEmpty(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	// Build a pre-125 item directly (no network_placement key at all) rather
	// than marshalling, to simulate a row written before this field existed.
	item := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":   &dynamodbtypes.AttributeValueMemberS{Value: "sb-net-pre125"},
		"profile_name": &dynamodbtypes.AttributeValueMemberS{Value: "dev"},
		"substrate":    &dynamodbtypes.AttributeValueMemberS{Value: "ec2"},
		"region":       &dynamodbtypes.AttributeValueMemberS{Value: "us-east-1"},
		"created_at":   &dynamodbtypes.AttributeValueMemberS{Value: now.Format(time.RFC3339)},
	}

	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(),
		&mockSandboxMetadataAPI{getItemOutput: &dynamodb.GetItemOutput{Item: item}},
		"km-sandboxes", "sb-net-pre125")
	if err != nil {
		t.Fatalf("ReadSandboxMetadataDynamo on a pre-125 row must not error: %v", err)
	}
	if got.NetworkPlacement != "" {
		t.Errorf("NetworkPlacement on a pre-125 row: got %q, want empty string", got.NetworkPlacement)
	}
}

// TestNetworkPlacement_SurvivesRemarshal is the important one — it simulates
// the pause/resume/extend/ttl-handler full-row PutItem cycle that has
// silently dropped fields in this codebase before
// (project_sandboxmetadata_lossy_roundtrip). marshal → unmarshal → marshal
// again: the network_placement attribute must still be present with the same
// value after the SECOND marshal. If it disappeared here, the `km init`
// refuse-to-disable-NAT guard (Plan 06) would see no private sandboxes on the
// next lifecycle write and let an operator tear NAT out from under a running
// private box.
func TestNetworkPlacement_SurvivesRemarshal(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := &kmaws.SandboxMetadata{
		SandboxID:        "sb-net-remarshal",
		ProfileName:      "dev",
		Substrate:        "ec2",
		Region:           "us-east-1",
		CreatedAt:        now,
		NetworkPlacement: "private",
	}

	// First marshal (simulates km create's initial WriteSandboxMetadataDynamo).
	firstItem := mustMarshalSandboxItemFull(t, meta)

	// Read it back (simulates resume.go / extend.go / ttl-handler reading the
	// row before mutating unrelated fields).
	roundTripped, err := kmaws.ReadSandboxMetadataDynamo(context.Background(),
		&mockSandboxMetadataAPI{getItemOutput: &dynamodb.GetItemOutput{Item: firstItem}},
		"km-sandboxes", "sb-net-remarshal")
	if err != nil {
		t.Fatalf("ReadSandboxMetadataDynamo: %v", err)
	}
	if roundTripped.NetworkPlacement != "private" {
		t.Fatalf("after first round-trip: NetworkPlacement = %q, want %q", roundTripped.NetworkPlacement, "private")
	}

	// Second marshal (simulates the full-row PutItem a lifecycle path issues
	// after mutating some unrelated field, e.g. TTLExpiry on extend).
	secondItem := mustMarshalSandboxItemFull(t, roundTripped)

	attr, ok := secondItem["network_placement"]
	if !ok {
		t.Fatal("network_placement attribute was dropped on the SECOND marshal (the lossy-round-trip footgun)")
	}
	sAttr, isS := attr.(*dynamodbtypes.AttributeValueMemberS)
	if !isS {
		t.Fatalf("expected network_placement to be AttributeValueMemberS after remarshal, got %T", attr)
	}
	if sAttr.Value != "private" {
		t.Errorf("network_placement after remarshal: got %q, want %q", sAttr.Value, "private")
	}
}

// TestNetworkPlacement_ListAllSandboxMetadataDynamo_RoundTrip covers the third
// unmarshal entry point (ListAllSandboxMetadataDynamo, used by km doctor-style
// scans per Plan 06/07) that ReadSandboxMetadataDynamo's Scan-based sibling
// ListAllSandboxesByDynamo does not exercise for this field — SandboxRecord
// deliberately does not carry NetworkPlacement (out of scope for this plan;
// consumers needing it use the richer SandboxMetadata-returning scan).
func TestNetworkPlacement_ListAllSandboxMetadataDynamo_RoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := &kmaws.SandboxMetadata{
		SandboxID:        "sb-net-scan",
		ProfileName:      "dev",
		Substrate:        "ec2",
		Region:           "us-east-1",
		CreatedAt:        now,
		NetworkPlacement: "private",
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
	if metas[0].NetworkPlacement != "private" {
		t.Errorf("ListAllSandboxMetadataDynamo NetworkPlacement: got %q, want %q", metas[0].NetworkPlacement, "private")
	}
}
