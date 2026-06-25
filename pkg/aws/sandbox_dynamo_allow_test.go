// sandbox_dynamo_allow_test.go — Phase 118 Wave-0 RED test for the
// SandboxMetadata.SlackAllow field DynamoDB round-trip (AC7).
//
// This test is written BEFORE the marshal/unmarshal logic exists (Plan 03).
// It is EXPECTED to be RED until Plan 03 adds slack_allow handling to
// marshalSandboxItem and unmarshalSlackFields.
//
// Pattern mirrors the Phase 91.5 slack_mention_only / slack_react_always
// round-trip tests in sandbox_dynamo_test.go (same package: aws_test).
package aws_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// TestSandboxMetadata_SlackAllow_RoundTrip — AC7.
//
// Verifies three sub-cases for the slack_allow DynamoDB attribute:
//
//  1. Non-empty SlackAllow → marshalSandboxItem emits slack_allow as an
//     *AttributeValueMemberS with comma-joined value "U0OPERATOR,U0XUSER";
//     unmarshal → SlackAllow == ["U0OPERATOR","U0XUSER"].
//
//  2. Nil SlackAllow → item has NO "slack_allow" key (absence = fall-back signal;
//     mirrors how nil SlackMentionOnly / SlackReactAlways are omitted).
//
//  3. Empty-slice SlackAllow → item has NO "slack_allow" key (same as nil).
//
// Cases 1 fails RED until Plan 03 adds the marshal/unmarshal logic.
// Cases 2 and 3 pass immediately (zero-value omitempty behavior).
func TestSandboxMetadata_SlackAllow_RoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	t.Run("non-empty SlackAllow round-trips as comma-joined S attribute", func(t *testing.T) {
		meta := &kmaws.SandboxMetadata{
			SandboxID:    "sb-allow-set",
			ProfileName:  "dev",
			Substrate:    "ec2",
			Region:       "us-east-1",
			CreatedAt:    now,
			SlackAllow:   []string{"U0OPERATOR", "U0XUSER"},
		}

		// Step 1: marshal → assert item["slack_allow"] is S with comma-joined value.
		// mustMarshalSandboxItemFull is defined in sandbox_dynamo_test.go.
		item := mustMarshalSandboxItemFull(t, meta)

		attr, ok := item["slack_allow"]
		if !ok {
			// RED: until Plan 03 adds marshalSandboxItem slack_allow handling.
			t.Fatalf("[RED until Plan 03] marshalSandboxItem did not emit 'slack_allow' key for non-empty SlackAllow")
		}
		sAttr, isS := attr.(*dynamodbtypes.AttributeValueMemberS)
		if !isS {
			t.Fatalf("expected slack_allow to be AttributeValueMemberS, got %T", attr)
		}
		wantVal := "U0OPERATOR,U0XUSER"
		if sAttr.Value != wantVal {
			t.Errorf("slack_allow S value: got %q, want %q", sAttr.Value, wantVal)
		}
		// Confirm it is NOT an SS (StringSet) attribute.
		if _, isSS := attr.(*dynamodbtypes.AttributeValueMemberSS); isSS {
			t.Error("slack_allow must be S (comma-joined string), not SS (StringSet)")
		}

		// Step 2: unmarshal the item back → assert SlackAllow round-trips.
		got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(),
			&mockSandboxMetadataAPI{getItemOutput: &dynamodb.GetItemOutput{Item: item}},
			"km-sandboxes", "sb-allow-set")
		if err != nil {
			t.Fatalf("ReadSandboxMetadataDynamo: %v", err)
		}
		if len(got.SlackAllow) != 2 {
			t.Fatalf("SlackAllow round-trip: got len=%d (%v), want 2 elements", len(got.SlackAllow), got.SlackAllow)
		}
		if got.SlackAllow[0] != "U0OPERATOR" || got.SlackAllow[1] != "U0XUSER" {
			t.Errorf("SlackAllow round-trip: got %v, want [U0OPERATOR U0XUSER]", got.SlackAllow)
		}
	})

	t.Run("nil SlackAllow → slack_allow attribute omitted", func(t *testing.T) {
		meta := &kmaws.SandboxMetadata{
			SandboxID:   "sb-allow-nil",
			ProfileName: "dev",
			Substrate:   "ec2",
			Region:      "us-east-1",
			CreatedAt:   now,
			SlackAllow:  nil, // explicit nil
		}
		item := mustMarshalSandboxItemFull(t, meta)
		if _, ok := item["slack_allow"]; ok {
			t.Error("slack_allow should be omitted when SlackAllow is nil (fall-back signal)")
		}
	})

	t.Run("empty-slice SlackAllow → slack_allow attribute omitted", func(t *testing.T) {
		meta := &kmaws.SandboxMetadata{
			SandboxID:   "sb-allow-empty",
			ProfileName: "dev",
			Substrate:   "ec2",
			Region:      "us-east-1",
			CreatedAt:   now,
			SlackAllow:  []string{}, // empty slice
		}
		item := mustMarshalSandboxItemFull(t, meta)
		if _, ok := item["slack_allow"]; ok {
			t.Error("slack_allow should be omitted when SlackAllow is empty (fall-back signal)")
		}
	})

	t.Run("single-element SlackAllow round-trips without trailing comma", func(t *testing.T) {
		meta := &kmaws.SandboxMetadata{
			SandboxID:   "sb-allow-one",
			ProfileName: "dev",
			Substrate:   "ec2",
			Region:      "us-east-1",
			CreatedAt:   now,
			SlackAllow:  []string{"U0OPERATOR"},
		}
		item := mustMarshalSandboxItemFull(t, meta)
		attr, ok := item["slack_allow"]
		if !ok {
			// RED: until Plan 03.
			t.Fatalf("[RED until Plan 03] marshalSandboxItem did not emit 'slack_allow' for single-element SlackAllow")
		}
		sAttr, isS := attr.(*dynamodbtypes.AttributeValueMemberS)
		if !isS {
			t.Fatalf("expected S attribute, got %T", attr)
		}
		if sAttr.Value != "U0OPERATOR" {
			t.Errorf("single-element value: got %q, want %q", sAttr.Value, "U0OPERATOR")
		}
		if strings.Contains(sAttr.Value, ",") {
			t.Errorf("single-element value must not contain comma, got %q", sAttr.Value)
		}
	})
}
