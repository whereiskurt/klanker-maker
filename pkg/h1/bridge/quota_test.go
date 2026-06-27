// Package bridge_test — quota_test.go
// Tests for H1-01: H1 bridge calls quota.Record for h1_comment dispatch.
// Mirrors the Slack BRG-01 tests: BLOCK → no enqueue + notice (internal); WARN → enqueue + notice.
package bridge_test

import (
	"context"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

// fakeH1QuotaClient is a test double for bridge.H1QuotaAPI.
// Returns a configurable count in the UpdateItem response.
type fakeH1QuotaClient struct {
	count     int64
	updateErr error
}

func (f *fakeH1QuotaClient) UpdateItem(ctx context.Context, in interface{}, _ ...func(interface{})) (interface{}, error) {
	return nil, nil
}

// fakeH1ActionLimits is a test double for bridge.H1ActionLimitsFetcher.
type fakeH1ActionLimits struct {
	limitsJSON string
	err        error
}

func (f *fakeH1ActionLimits) FetchLimits(ctx context.Context, sandboxID string) (string, error) {
	return f.limitsJSON, f.err
}

// TestQuotaRecord_H1_BlockTrip (H1-01) — when quota.Record returns a BLOCK decision
// for h1_comment, the bridge must:
//  1. NOT enqueue to SQS.
//  2. Post an INTERNAL control-plane notice via H1Commenter.
//  3. Return 200 (the bridge always returns 200 on internal errors).
func TestQuotaRecord_H1_BlockTrip(t *testing.T) {
	fakes := newFakes()

	// Limits JSON: h1_comment perHour=1 onBreach=block.
	limitsJSON := `{"h1_comment":{"perHour":1,"onBreach":"block"}}`
	quotaClient := bridge.NewFakeH1QuotaClient(2) // count=2 > limit=1 → BLOCK

	programs := singleProgram("km-sandbox", []string{"alice"}, nil)
	h := baseHandler(programs, fakes)
	h.Quota = quotaClient
	h.QuotaTable = "km-action-quota"
	h.Limits = &fakeH1ActionLimits{limitsJSON: limitsJSON}

	body := h1Body("km-sandbox", "100", "alice", "@km please triage", false)
	req := newRequest(body, "report_comment_created", "guid-block-h1")

	resp := h.Handle(context.Background(), req)

	// Always 200 (H1 bridge never returns 5xx).
	if resp.StatusCode != 200 {
		t.Errorf("BLOCK trip: want 200, got %d", resp.StatusCode)
	}

	// No SQS enqueue.
	if len(fakes.sqs.sends) != 0 {
		t.Errorf("BLOCK trip: SQS must not be called; got %d sends", len(fakes.sqs.sends))
	}

	// Control-plane notice posted INTERNALLY (internal=true, never researcher-visible).
	fakes.commenter.mu.Lock()
	posts := fakes.commenter.posts
	fakes.commenter.mu.Unlock()

	foundNotice := false
	for _, p := range posts {
		if (strings.Contains(p.body, "🛑") || strings.Contains(p.body, "Quota")) && p.internal {
			foundNotice = true
		}
	}
	if !foundNotice {
		t.Errorf("BLOCK trip: expected an internal quota notice; got posts=%v", posts)
	}
}

// TestQuotaRecord_H1_WarnTrip (H1-01) — WARN trip → SQS dispatch still fires + internal notice.
func TestQuotaRecord_H1_WarnTrip(t *testing.T) {
	fakes := newFakes()

	limitsJSON := `{"h1_comment":{"perHour":1,"onBreach":"warn"}}`
	quotaClient := bridge.NewFakeH1QuotaClient(2) // count=2 > limit=1 → WARN

	programs := singleProgram("km-sandbox", []string{"alice"}, nil)
	h := baseHandler(programs, fakes)
	h.Quota = quotaClient
	h.QuotaTable = "km-action-quota"
	h.Limits = &fakeH1ActionLimits{limitsJSON: limitsJSON}

	body := h1Body("km-sandbox", "100", "alice", "@km please triage", false)
	req := newRequest(body, "report_comment_created", "guid-warn-h1")

	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Errorf("WARN trip: want 200, got %d", resp.StatusCode)
	}

	// SQS dispatch fires (WARN doesn't block).
	if len(fakes.sqs.sends) == 0 {
		t.Error("WARN trip: SQS dispatch must fire")
	}

	// Notice posted (internal).
	fakes.commenter.mu.Lock()
	posts := fakes.commenter.posts
	fakes.commenter.mu.Unlock()

	foundNotice := false
	for _, p := range posts {
		if (strings.Contains(p.body, "⚠") || strings.Contains(p.body, "Quota")) && p.internal {
			foundNotice = true
		}
	}
	if !foundNotice {
		t.Errorf("WARN trip: expected an internal quota notice; got posts=%v", posts)
	}
}

// TestQuotaRecord_H1_NoLimits (H1-01) — no configured limits → byte-identical to today.
func TestQuotaRecord_H1_NoLimits(t *testing.T) {
	fakes := newFakes()

	programs := singleProgram("km-sandbox", []string{"alice"}, nil)
	h := baseHandler(programs, fakes)
	// No Quota/Limits wired → dormant.

	body := h1Body("km-sandbox", "100", "alice", "@km please triage", false)
	req := newRequest(body, "report_comment_created", "guid-nolimits-h1")

	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Errorf("no limits: want 200, got %d", resp.StatusCode)
	}
	if len(fakes.sqs.sends) == 0 {
		t.Error("no limits: SQS dispatch must fire")
	}
}

// TestQuotaRecord_H1_FrozenDispatch (H1-01) — when the sandbox is frozen, the H1 bridge
// must refuse dispatch (no SQS) and post the internal frozen notice.
func TestQuotaRecord_H1_FrozenDispatch(t *testing.T) {
	fakes := newFakes()

	programs := singleProgram("km-sandbox", []string{"alice"}, nil)
	h := baseHandler(programs, fakes)
	// Wire a frozen fetcher: sandbox is frozen.
	h.FrozenCheck = &fakeH1FrozenCheck{frozen: true, reason: "quota: h1_comment lifetime exceeded"}

	body := h1Body("km-sandbox", "100", "alice", "@km please triage", false)
	req := newRequest(body, "report_comment_created", "guid-frozen-h1")

	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Errorf("frozen: want 200, got %d", resp.StatusCode)
	}

	// No SQS.
	if len(fakes.sqs.sends) != 0 {
		t.Errorf("frozen: SQS must not be called; got %d sends", len(fakes.sqs.sends))
	}

	// Internal frozen notice.
	fakes.commenter.mu.Lock()
	posts := fakes.commenter.posts
	fakes.commenter.mu.Unlock()

	foundFrozenNotice := false
	for _, p := range posts {
		if (strings.Contains(p.body, "frozen") || strings.Contains(p.body, "🛑")) && p.internal {
			foundFrozenNotice = true
		}
	}
	if !foundFrozenNotice {
		t.Errorf("frozen: expected an internal frozen notice; got posts=%v", posts)
	}
}

// fakeH1FrozenCheck is a test double for bridge.H1FrozenChecker.
type fakeH1FrozenCheck struct {
	frozen bool
	reason string
}

func (f *fakeH1FrozenCheck) IsFrozen(ctx context.Context, sandboxID string) (bool, string, error) {
	return f.frozen, f.reason, nil
}

// fakeH1Freezer records FreezeSandbox calls for assertion.
type fakeH1Freezer struct {
	calls []struct{ sandboxID, reason, by string }
}

func (f *fakeH1Freezer) FreezeSandbox(_ context.Context, sandboxID, reason, by string) error {
	f.calls = append(f.calls, struct{ sandboxID, reason, by string }{sandboxID, reason, by})
	return nil
}

// TestQuotaRecord_H1_FreezeTrip_AutoLatches (GAP-2) — when quota.Record returns a FREEZE
// decision for h1_comment, the bridge must:
//  1. NOT enqueue to SQS (action blocked).
//  2. Call h.Freezer.FreezeSandbox exactly once with by="auto:h1_comment:hour".
//  3. Return 200 (H1 bridge always returns 200).
func TestQuotaRecord_H1_FreezeTrip_AutoLatches(t *testing.T) {
	fakes := newFakes()
	ff := &fakeH1Freezer{}

	limitsJSON := `{"h1_comment":{"perHour":1,"onBreach":"freeze"}}`
	quotaClient := bridge.NewFakeH1QuotaClient(2) // count=2 > limit=1 → FREEZE

	programs := singleProgram("km-sandbox", []string{"alice"}, nil)
	h := baseHandler(programs, fakes)
	h.Quota = quotaClient
	h.QuotaTable = "km-action-quota"
	h.Limits = &fakeH1ActionLimits{limitsJSON: limitsJSON}
	h.Freezer = ff

	body := h1Body("km-sandbox", "100", "alice", "@km please triage", false)
	req := newRequest(body, "report_comment_created", "guid-freeze-h1")

	resp := h.Handle(context.Background(), req)

	// Always 200 (H1 bridge never returns 5xx).
	if resp.StatusCode != 200 {
		t.Errorf("FREEZE trip: want 200, got %d", resp.StatusCode)
	}

	// No SQS enqueue (action blocked on FREEZE).
	if len(fakes.sqs.sends) != 0 {
		t.Errorf("FREEZE trip: SQS must not be called; got %d sends", len(fakes.sqs.sends))
	}

	// FreezeSandbox called exactly once.
	if len(ff.calls) != 1 {
		t.Errorf("FREEZE trip: want exactly 1 FreezeSandbox call, got %d", len(ff.calls))
	} else {
		call := ff.calls[0]
		if call.by != "auto:h1_comment:hour" {
			t.Errorf("FREEZE trip: FreezeSandbox by=%q, want %q", call.by, "auto:h1_comment:hour")
		}
		if call.reason == "" {
			t.Error("FREEZE trip: FreezeSandbox reason must be non-empty")
		}
	}
}

// TestQuotaRecord_H1_BlockTrip_NoFreeze — BLOCK trip must NOT call FreezeSandbox.
func TestQuotaRecord_H1_BlockTrip_NoFreeze(t *testing.T) {
	fakes := newFakes()
	ff := &fakeH1Freezer{}

	limitsJSON := `{"h1_comment":{"perHour":1,"onBreach":"block"}}`
	quotaClient := bridge.NewFakeH1QuotaClient(2) // count=2 > limit=1 → BLOCK

	programs := singleProgram("km-sandbox", []string{"alice"}, nil)
	h := baseHandler(programs, fakes)
	h.Quota = quotaClient
	h.QuotaTable = "km-action-quota"
	h.Limits = &fakeH1ActionLimits{limitsJSON: limitsJSON}
	h.Freezer = ff

	body := h1Body("km-sandbox", "100", "alice", "@km please triage", false)
	req := newRequest(body, "report_comment_created", "guid-block-nofr-h1")

	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Errorf("BLOCK trip: want 200, got %d", resp.StatusCode)
	}

	// No SQS enqueue.
	if len(fakes.sqs.sends) != 0 {
		t.Errorf("BLOCK trip: SQS must not be called; got %d sends", len(fakes.sqs.sends))
	}

	// FreezeSandbox must NOT be called for BLOCK.
	if len(ff.calls) != 0 {
		t.Errorf("BLOCK trip: FreezeSandbox must NOT be called; got %d calls", len(ff.calls))
	}
}
