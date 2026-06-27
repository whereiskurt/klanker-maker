package main

// main_quota_test.go — Phase 121 follow-up: wire the Handler quota/freeze fields
// from env. WireActionQuota gates on KM_QUOTA_TABLE (empty ⇒ dormant, the
// pre-follow-up behavior; set ⇒ Quota/Limits/Freezer/QuotaTable all populated).

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

func TestWireActionQuota_DormantWhenUnset(t *testing.T) {
	t.Setenv("KM_QUOTA_TABLE", "")
	h := &bridge.Handler{}
	if WireActionQuota(h, &dynamodb.Client{}, "km-sandboxes") {
		t.Error("expected dormant (not wired) when KM_QUOTA_TABLE is empty")
	}
	if h.Quota != nil || h.Limits != nil || h.Freezer != nil || h.QuotaTable != "" {
		t.Errorf("fields must stay nil/empty when dormant: Quota=%v Limits=%v Freezer=%v Table=%q",
			h.Quota, h.Limits, h.Freezer, h.QuotaTable)
	}
}

func TestWireActionQuota_WiresAllFields(t *testing.T) {
	t.Setenv("KM_QUOTA_TABLE", "km-action-quota")
	h := &bridge.Handler{}
	if !WireActionQuota(h, &dynamodb.Client{}, "km-sandboxes") {
		t.Fatal("expected wired=true when KM_QUOTA_TABLE is set")
	}
	if h.Quota == nil {
		t.Error("Handler.Quota must be set")
	}
	if h.Limits == nil {
		t.Error("Handler.Limits must be set")
	}
	if h.Freezer == nil {
		t.Error("Handler.Freezer must be set")
	}
	if h.QuotaTable != "km-action-quota" {
		t.Errorf("Handler.QuotaTable: got %q, want km-action-quota", h.QuotaTable)
	}
}
