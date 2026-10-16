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
