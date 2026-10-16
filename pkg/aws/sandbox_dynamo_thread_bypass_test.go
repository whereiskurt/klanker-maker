package aws_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

func roundTripThreadBypass(t *testing.T, v *bool) *bool {
	t.Helper()
	meta := &kmaws.SandboxMetadata{
		SandboxID: "sb-tb", ProfileName: "dev", Substrate: "ec2", Region: "us-east-1",
		CreatedAt: time.Now().UTC().Truncate(time.Second), SlackChannelID: "C0TB",
		SlackThreadBypass: v,
	}
	item := mustMarshalSandboxItemFull(t, meta)
	if v == nil {
		if _, ok := item["slack_thread_bypass"]; ok {
			t.Fatal("slack_thread_bypass must be omitted when nil")
		}
	}
	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(),
		&mockSandboxMetadataAPI{getItemOutput: &dynamodb.GetItemOutput{Item: item}}, "km-sandboxes", "sb-tb")
	if err != nil {
		t.Fatal(err)
	}
	return got.SlackThreadBypass
}

func TestSlackThreadBypass_RoundTrip(t *testing.T) {
	if got := roundTripThreadBypass(t, nil); got != nil {
		t.Errorf("nil: got %v", *got)
	}
	f := false
	if got := roundTripThreadBypass(t, &f); got == nil || *got {
		t.Errorf("&false: got %v", got)
	}
	tr := true
	if got := roundTripThreadBypass(t, &tr); got == nil || !*got {
		t.Errorf("&true: got %v", got)
	}
}
