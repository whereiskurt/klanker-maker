package bridge

import (
	"testing"

	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// threadBypassItem builds the minimal row routingInfoFromItem needs.
func threadBypassItem(id string) map[string]dynamodbtypes.AttributeValue {
	return map[string]dynamodbtypes.AttributeValue{
		"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: id},
	}
}

func TestRoutingInfoFromItem_ThreadBypass(t *testing.T) {
	item := threadBypassItem("sb-1")
	if got := routingInfoFromItem(item); got.ThreadBypass != nil {
		t.Fatal("absent attribute must be nil")
	}
	item["slack_thread_bypass"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: false}
	got := routingInfoFromItem(item)
	if got.ThreadBypass == nil || *got.ThreadBypass || threadBypassAllowed(got) {
		t.Fatal("BOOL false must read as strict")
	}
	item["slack_thread_bypass"] = &dynamodbtypes.AttributeValueMemberS{Value: "false"}
	if got := routingInfoFromItem(item); got.ThreadBypass == nil || *got.ThreadBypass {
		t.Fatal("string \"false\" (UpdateSandboxAttr writer) must read as strict")
	}
	item["slack_thread_bypass"] = &dynamodbtypes.AttributeValueMemberS{Value: "true"}
	if got := routingInfoFromItem(item); got.ThreadBypass == nil || !*got.ThreadBypass {
		t.Fatal("string \"true\" must read as bypass")
	}
	if !threadBypassAllowed(SandboxRoutingInfo{}) {
		t.Fatal("nil => bypass allowed")
	}
}

func TestRoutingInfoFromItem_AllowEmailsAndReactQuiet(t *testing.T) {
	item := threadBypassItem("sb-1")
	got := routingInfoFromItem(item)
	if got.AllowEmails != nil || got.ReactQuietSeconds != nil {
		t.Fatal("absent attributes must be nil")
	}
	item["slack_allow_emails"] = &dynamodbtypes.AttributeValueMemberS{Value: "kurt@example.com,*@example.com"}
	item["slack_react_quiet_seconds"] = &dynamodbtypes.AttributeValueMemberN{Value: "180"}
	got = routingInfoFromItem(item)
	if len(got.AllowEmails) != 2 || got.AllowEmails[0] != "kurt@example.com" || got.AllowEmails[1] != "*@example.com" {
		t.Fatalf("AllowEmails = %v", got.AllowEmails)
	}
	if got.ReactQuietSeconds == nil || *got.ReactQuietSeconds != 180 {
		t.Fatalf("ReactQuietSeconds = %v", got.ReactQuietSeconds)
	}
	// UpdateSandboxAttr writes S.
	item["slack_react_quiet_seconds"] = &dynamodbtypes.AttributeValueMemberS{Value: "60"}
	if got := routingInfoFromItem(item); got.ReactQuietSeconds == nil || *got.ReactQuietSeconds != 60 {
		t.Fatalf("S-typed quiet: %v", got.ReactQuietSeconds)
	}
	for _, bad := range []dynamodbtypes.AttributeValue{
		&dynamodbtypes.AttributeValueMemberS{Value: "soon"},
		&dynamodbtypes.AttributeValueMemberN{Value: "1.5"},
		&dynamodbtypes.AttributeValueMemberS{Value: "-5"},
	} {
		item["slack_react_quiet_seconds"] = bad
		if got := routingInfoFromItem(item); got.ReactQuietSeconds != nil {
			t.Fatalf("%#v must read nil, got %d", bad, *got.ReactQuietSeconds)
		}
	}
}
