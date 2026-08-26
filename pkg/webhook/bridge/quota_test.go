// quota_test.go — Task 9A (Phase 121 follow-up): the webhook bridge's
// action-quota gate. Package bridge (internal) so these tests can reuse the
// stubs/newHandler/authedReq helpers from handler_test.go directly.
//
// Every test asserts on Response.Log (not merely "did not enqueue"), per the
// task's stated quality bar: a broken gate that fails to enqueue for the
// wrong reason must not pass silently.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeQuotaClient is a test double for quota.QuotaAPI (UpdateItem-only). Every
// UpdateItem call returns the configured count in Attributes["count"], or the
// configured error.
type fakeQuotaClient struct {
	count int64
	err   error
	calls int
}

func (f *fakeQuotaClient) UpdateItem(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &dynamodb.UpdateItemOutput{
		Attributes: map[string]dynamodbtypes.AttributeValue{
			"count": &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", f.count)},
		},
	}, nil
}

// fakeActionLimits is a test double for ActionLimitsFetcher.
type fakeActionLimits struct {
	json string
	err  error
}

func (f *fakeActionLimits) FetchLimits(_ context.Context, _ string) (string, error) {
	return f.json, f.err
}

// fakeQuotaFreezer records FreezeSandbox calls for assertion.
type fakeQuotaFreezer struct {
	calls []struct{ sandboxID, reason, by string }
}

func (f *fakeQuotaFreezer) FreezeSandbox(_ context.Context, sandboxID, reason, by string) error {
	f.calls = append(f.calls, struct{ sandboxID, reason, by string }{sandboxID, reason, by})
	return nil
}

// warmStubs returns a *stubs configured for a warm (running) dispatch to
// sandbox "sb-1", matching the fixture rule in newHandler (alias "ir-bot",
// severity CRITICAL).
func warmStubs() *stubs {
	return &stubs{secret: "tok", sandboxID: "sb-1", status: "running"}
}

// ============================================================
// Dormancy — the most important test in this file.
// ============================================================

func TestCheckQuota_DormantWhenFieldsUnset(t *testing.T) {
	s := warmStubs()
	h := newHandler(t, s)
	// Quota/Limits/Freezer left nil (zero value) — must be byte-identical to
	// pre-Task-9A warm-enqueue behaviour.
	resp := h.Handle(context.Background(), authedReq())

	if resp.Log != "dispatched" {
		t.Errorf("Log: got %q, want %q", resp.Log, "dispatched")
	}
	if len(s.enqueued) != 1 {
		t.Errorf("enqueued: got %d, want 1", len(s.enqueued))
	}
}

func TestCheckQuota_DormantWhenQuotaTableEmpty(t *testing.T) {
	s := warmStubs()
	h := newHandler(t, s)
	h.Quota = &fakeQuotaClient{count: 99} // would trip if consulted
	h.Limits = &fakeActionLimits{json: `{"webhook_dispatch":{"perHour":1,"onBreach":"block"}}`}
	// h.QuotaTable left empty — this alone must keep the gate dormant.
	resp := h.Handle(context.Background(), authedReq())

	if resp.Log != "dispatched" {
		t.Errorf("Log: got %q, want %q (QuotaTable empty must stay dormant)", resp.Log, "dispatched")
	}
	if len(s.enqueued) != 1 {
		t.Errorf("enqueued: got %d, want 1", len(s.enqueued))
	}
}

// ============================================================
// The four decision outcomes
// ============================================================

func TestCheckQuota_NotTripped_Dispatches(t *testing.T) {
	s := warmStubs()
	h := newHandler(t, s)
	h.Quota = &fakeQuotaClient{count: 1} // 1 <= limit 10 → not tripped
	h.QuotaTable = "km-action-quota"
	h.Limits = &fakeActionLimits{json: `{"webhook_dispatch":{"perHour":10,"onBreach":"block"}}`}

	resp := h.Handle(context.Background(), authedReq())

	if resp.Log != "dispatched" {
		t.Errorf("Log: got %q, want %q", resp.Log, "dispatched")
	}
	if len(s.enqueued) != 1 {
		t.Errorf("enqueued: got %d, want 1", len(s.enqueued))
	}
}

func TestCheckQuota_Warn_DispatchesAnyway(t *testing.T) {
	s := warmStubs()
	h := newHandler(t, s)
	h.Quota = &fakeQuotaClient{count: 2} // 2 > limit 1 → tripped, warn
	h.QuotaTable = "km-action-quota"
	h.Limits = &fakeActionLimits{json: `{"webhook_dispatch":{"perHour":1,"onBreach":"warn"}}`}

	resp := h.Handle(context.Background(), authedReq())

	if resp.Log != "dispatched" {
		t.Errorf("Log: got %q, want %q (warn must still dispatch)", resp.Log, "dispatched")
	}
	if len(s.enqueued) != 1 {
		t.Errorf("enqueued: got %d, want 1", len(s.enqueued))
	}
}

func TestCheckQuota_Block_StopsDispatch(t *testing.T) {
	s := warmStubs()
	h := newHandler(t, s)
	h.Quota = &fakeQuotaClient{count: 2} // 2 > limit 1 → tripped, block
	h.QuotaTable = "km-action-quota"
	h.Limits = &fakeActionLimits{json: `{"webhook_dispatch":{"perHour":1,"onBreach":"block"}}`}

	resp := h.Handle(context.Background(), authedReq())

	if resp.Log != "quota_blocked" {
		t.Errorf("Log: got %q, want %q", resp.Log, "quota_blocked")
	}
	if len(s.enqueued) != 0 {
		t.Errorf("enqueued: got %d, want 0 (blocked)", len(s.enqueued))
	}
	if resp.Status != 200 {
		t.Errorf("Status: got %d, want 200", resp.Status)
	}
}

func TestCheckQuota_Freeze_StopsDispatchAndFreezes(t *testing.T) {
	s := warmStubs()
	h := newHandler(t, s)
	h.Quota = &fakeQuotaClient{count: 2} // 2 > limit 1 → tripped, freeze
	h.QuotaTable = "km-action-quota"
	h.Limits = &fakeActionLimits{json: `{"webhook_dispatch":{"perHour":1,"onBreach":"freeze"}}`}
	fz := &fakeQuotaFreezer{}
	h.Freezer = fz

	resp := h.Handle(context.Background(), authedReq())

	if resp.Log != "quota_frozen" {
		t.Errorf("Log: got %q, want %q", resp.Log, "quota_frozen")
	}
	if len(s.enqueued) != 0 {
		t.Errorf("enqueued: got %d, want 0 (frozen)", len(s.enqueued))
	}
	if len(fz.calls) != 1 {
		t.Fatalf("FreezeSandbox calls: got %d, want 1", len(fz.calls))
	}
	if fz.calls[0].sandboxID != "sb-1" {
		t.Errorf("FreezeSandbox sandboxID: got %q, want sb-1", fz.calls[0].sandboxID)
	}
	if fz.calls[0].by != "auto:webhook_dispatch:hour" {
		t.Errorf("FreezeSandbox by: got %q, want auto:webhook_dispatch:hour", fz.calls[0].by)
	}
}

func TestCheckQuota_Block_DoesNotCallFreezer(t *testing.T) {
	s := warmStubs()
	h := newHandler(t, s)
	h.Quota = &fakeQuotaClient{count: 2}
	h.QuotaTable = "km-action-quota"
	h.Limits = &fakeActionLimits{json: `{"webhook_dispatch":{"perHour":1,"onBreach":"block"}}`}
	fz := &fakeQuotaFreezer{}
	h.Freezer = fz

	h.Handle(context.Background(), authedReq())

	if len(fz.calls) != 0 {
		t.Errorf("FreezeSandbox must not be called on a block trip: got %d calls", len(fz.calls))
	}
}

// ============================================================
// Fail-open on store errors
// ============================================================

func TestCheckQuota_RecordError_FailsOpen(t *testing.T) {
	s := warmStubs()
	h := newHandler(t, s)
	h.Quota = &fakeQuotaClient{err: errors.New("ddb unavailable")}
	h.QuotaTable = "km-action-quota"
	h.Limits = &fakeActionLimits{json: `{"webhook_dispatch":{"perHour":1,"onBreach":"block"}}`}

	resp := h.Handle(context.Background(), authedReq())

	if resp.Log != "dispatched" {
		t.Errorf("Log: got %q, want %q (a Record error must fail open)", resp.Log, "dispatched")
	}
	if len(s.enqueued) != 1 {
		t.Errorf("enqueued: got %d, want 1 (fail-open)", len(s.enqueued))
	}
}

func TestCheckQuota_LimitsFetchError_FailsOpen(t *testing.T) {
	s := warmStubs()
	h := newHandler(t, s)
	h.Quota = &fakeQuotaClient{count: 99} // would trip if reached
	h.QuotaTable = "km-action-quota"
	h.Limits = &fakeActionLimits{err: errors.New("ddb unavailable")}

	resp := h.Handle(context.Background(), authedReq())

	if resp.Log != "dispatched" {
		t.Errorf("Log: got %q, want %q (a limits-fetch error must fail open)", resp.Log, "dispatched")
	}
	if len(s.enqueued) != 1 {
		t.Errorf("enqueued: got %d, want 1 (fail-open)", len(s.enqueued))
	}
}

// ============================================================
// Cold path is unaffected even when quota fields are wired
// ============================================================

func TestCheckQuota_ColdPathUnaffectedWhenWired(t *testing.T) {
	s := &stubs{secret: "tok", resolveErr: errors.New("not found")}
	h := newHandler(t, s)
	quotaClient := &fakeQuotaClient{count: 99} // would trip and block if consulted
	h.Quota = quotaClient
	h.QuotaTable = "km-action-quota"
	h.Limits = &fakeActionLimits{json: `{"webhook_dispatch":{"perHour":1,"onBreach":"block"}}`}

	resp := h.Handle(context.Background(), authedReq())

	if resp.Log != "cold_created" {
		t.Errorf("Log: got %q, want %q", resp.Log, "cold_created")
	}
	if len(s.created) != 1 || s.created[0] != "ir-bot" {
		t.Errorf("created: %v", s.created)
	}
	// The cold path has no sandbox id to gate against — the quota store must
	// never be consulted for it.
	if quotaClient.calls != 0 {
		t.Errorf("quota.Record must not be called on the cold path: got %d calls", quotaClient.calls)
	}
}
