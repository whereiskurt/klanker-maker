// Package quota tests — quota_test.go
// Wave 0 test stubs for QUO-01..05 turned GREEN in Wave 1 plan 02.
// QUO-01: bucket math (was already asserting, not skipped)
// QUO-02: ResolveLimits per-(action,window) precedence
// QUO-03: Decision breach detection (any window trips ⇒ Tripped=true)
// QUO-04: Record uses atomic ADD (not read-modify-write)
// QUO-05: lifetime rows carry no TTL; hour/day rows carry TTL ~2h/~2d
package quota

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeQuotaClient is a test double for the QuotaAPI interface.
type fakeQuotaClient struct {
	updateItemCalls []*dynamodb.UpdateItemInput
	// countReturn is the count value returned in ALL_NEW attributes.
	countReturn int64
}

func (f *fakeQuotaClient) UpdateItem(_ context.Context, input *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updateItemCalls = append(f.updateItemCalls, input)
	return &dynamodb.UpdateItemOutput{
		Attributes: map[string]dynamodbtypes.AttributeValue{
			"count": &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", f.countReturn)},
		},
	}, nil
}

// TestRecord (QUO-01) — fixed-window bucket math.
// Asserts hourBucket uses epoch/3600 and dayBucket uses epoch/86400 for a
// known, deterministic Unix timestamp. This test was already asserting in
// plan 01 (skeleton); retained here for completeness.
func TestRecord(t *testing.T) {
	// Known timestamp: 2024-01-15 12:30:00 UTC = Unix 1705318200
	ts := time.Unix(1705318200, 0).UTC()

	wantHour := int64(1705318200 / 3600) // 473699
	wantDay := int64(1705318200 / 86400) // 19736

	gotHour := hourBucket(ts)
	gotDay := dayBucket(ts)

	if gotHour != wantHour {
		t.Errorf("hourBucket: got %d, want %d (epoch/3600)", gotHour, wantHour)
	}
	if gotDay != wantDay {
		t.Errorf("dayBucket: got %d, want %d (epoch/86400)", gotDay, wantDay)
	}

	// Verify bucket boundary: two timestamps in the same hour share the same bucket.
	ts2 := time.Unix(1705318200+1799, 0).UTC() // 29m59s later — still same hour
	if hourBucket(ts) != hourBucket(ts2) {
		t.Errorf("timestamps in the same hour should share the same hourBucket")
	}

	// Verify bucket boundary: one more second crosses into the next hour.
	ts3 := time.Unix(1705318200+1800, 0).UTC() // 30m00s later — next bucket
	if hourBucket(ts) == hourBucket(ts3) {
		t.Errorf("timestamps in different hours should have different hourBuckets")
	}
}

// TestResolveLimits (QUO-02) — per-(action,window) resolution: profile →
// install default → unlimited.
func TestResolveLimits(t *testing.T) {
	installDefaults := Limits{
		ActionGithubPR: {
			Lifetime: ptr64(100),
			PerHour:  ptr64(15),
			PerDay:   ptr64(50),
			OnBreach: BreachFreeze,
		},
		ActionGithubComment: {
			PerHour:  ptr64(60),
			PerDay:   ptr64(300),
			OnBreach: BreachWarn,
		},
	}

	// Profile overrides only perHour for github_pr; lifetime+perDay should be inherited.
	profileLimits := Limits{
		ActionGithubPR: {
			PerHour:  ptr64(5), // tighter than install default (15)
			OnBreach: BreachBlock,
		},
		// github_comment not in profile — fully inherits from installDefaults
	}

	resolved := ResolveLimits(profileLimits, installDefaults)

	// 1. Profile value wins over install default for the same action+window.
	pr := resolved[ActionGithubPR]
	if pr.PerHour == nil || *pr.PerHour != 5 {
		t.Errorf("github_pr.perHour: got %v, want 5 (profile wins)", pr.PerHour)
	}
	// 2. A profile that sets only perHour still inherits install-level lifetime and perDay.
	if pr.Lifetime == nil || *pr.Lifetime != 100 {
		t.Errorf("github_pr.lifetime: got %v, want 100 (inherited from install default)", pr.Lifetime)
	}
	if pr.PerDay == nil || *pr.PerDay != 50 {
		t.Errorf("github_pr.perDay: got %v, want 50 (inherited from install default)", pr.PerDay)
	}
	// 3. OnBreach: profile wins.
	if pr.OnBreach != BreachBlock {
		t.Errorf("github_pr.onBreach: got %q, want %q (profile wins)", pr.OnBreach, BreachBlock)
	}

	// 4. When action is absent from profile, fully inherit from install defaults.
	comment := resolved[ActionGithubComment]
	if comment.PerHour == nil || *comment.PerHour != 60 {
		t.Errorf("github_comment.perHour: got %v, want 60 (from install default)", comment.PerHour)
	}
	if comment.PerDay == nil || *comment.PerDay != 300 {
		t.Errorf("github_comment.perDay: got %v, want 300 (from install default)", comment.PerDay)
	}
	// Lifetime was not set in installDefaults for github_comment ⇒ nil (unlimited).
	if comment.Lifetime != nil {
		t.Errorf("github_comment.lifetime: got %v, want nil (unlimited — not set in either)", comment.Lifetime)
	}

	// 5. When neither profile nor install sets a window, that window returns nil (unlimited).
	email := resolved[ActionEmailSend]
	if email.PerHour != nil || email.PerDay != nil || email.Lifetime != nil {
		t.Errorf("email_send: got %+v, want all-nil (unlimited — not set in either)", email)
	}

	// 6. OnBreach default when neither profile nor install sets it.
	if email.OnBreach != "" {
		// ResolveLimits returns empty string (caller falls back to BreachWarn at runtime).
		t.Errorf("email_send.onBreach: got %q, want empty string (unlimited action)", email.OnBreach)
	}
}

// TestDecision (QUO-03) — Tripped=true when ANY window exceeds its limit.
func TestDecision(t *testing.T) {
	// Case 1: hour count (16) exceeds limit (15), day count (40) is under limit (50).
	// Tripped=true, WorstWindow="hour" (hour is worst when it's the only exceeded window).
	fake := &fakeQuotaClient{}
	callNum := 0
	// We need different count returns per call: hour call returns 16, day call returns 40.
	// Wrap with a custom client that returns different counts.
	multi := &multiCountClient{
		counts: []int64{16, 40}, // first call (hour) returns 16; second call (day) returns 40
	}
	_ = fake

	ctx := context.Background()
	limit := ActionLimit{
		PerHour:  ptr64(15),
		PerDay:   ptr64(50),
		OnBreach: BreachBlock,
	}

	d, err := Record(ctx, multi, "test-table", "sb-abc", ActionGithubPR, limit)
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if !d.Tripped {
		t.Error("Decision.Tripped: got false, want true (hour count 16 > limit 15)")
	}
	if d.WorstWindow != "hour" {
		t.Errorf("Decision.WorstWindow: got %q, want %q", d.WorstWindow, "hour")
	}
	if d.OnBreach != BreachBlock {
		t.Errorf("Decision.OnBreach: got %q, want %q", d.OnBreach, BreachBlock)
	}

	// Case 2: all counts within limits ⇒ Tripped=false.
	_ = callNum
	multi2 := &multiCountClient{counts: []int64{14, 49}} // hour=14≤15, day=49≤50
	limit2 := ActionLimit{PerHour: ptr64(15), PerDay: ptr64(50)}
	d2, err2 := Record(ctx, multi2, "test-table", "sb-abc", ActionGithubPR, limit2)
	if err2 != nil {
		t.Fatalf("Record returned error: %v", err2)
	}
	if d2.Tripped {
		t.Error("Decision.Tripped: got true, want false (all counts within limits)")
	}

	// Case 3: no configured windows ⇒ no DDB calls, dormant Decision.
	fakeNoDDB := &fakeQuotaClient{}
	limitDormant := ActionLimit{} // no windows configured
	d3, err3 := Record(ctx, fakeNoDDB, "test-table", "sb-xyz", ActionSlackPost, limitDormant)
	if err3 != nil {
		t.Fatalf("Record (dormant) returned error: %v", err3)
	}
	if d3.Tripped {
		t.Error("Decision.Tripped: got true for dormant action, want false")
	}
	if len(fakeNoDDB.updateItemCalls) != 0 {
		t.Errorf("dormant action: expected 0 UpdateItem calls, got %d", len(fakeNoDDB.updateItemCalls))
	}
}

// TestAtomicADD (QUO-04) — Record uses atomic ADD (not read-modify-write).
// Asserts the UpdateItem expression contains "ADD" (not GetItem+PutItem).
func TestAtomicADD(t *testing.T) {
	fake := &fakeQuotaClient{countReturn: 1}
	ctx := context.Background()
	limit := ActionLimit{PerHour: ptr64(15)}
	_, err := Record(ctx, fake, "test-table", "sb-123", ActionGithubPR, limit)
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if len(fake.updateItemCalls) == 0 {
		t.Fatal("expected UpdateItem to be called for atomic ADD")
	}
	call := fake.updateItemCalls[0]
	if call.UpdateExpression == nil {
		t.Fatal("expected UpdateExpression to be set")
	}
	expr := *call.UpdateExpression
	if !strings.Contains(expr, "ADD") {
		t.Errorf("UpdateExpression should contain ADD, got: %q", expr)
	}
	// Verify ReturnValues is ALL_NEW.
	if call.ReturnValues != dynamodbtypes.ReturnValueAllNew {
		t.Errorf("ReturnValues: got %v, want ALL_NEW", call.ReturnValues)
	}
	// Verify :one = 1 in ExpressionAttributeValues.
	oneAV, ok := call.ExpressionAttributeValues[":one"]
	if !ok {
		t.Fatal("expected :one in ExpressionAttributeValues")
	}
	oneN, ok := oneAV.(*dynamodbtypes.AttributeValueMemberN)
	if !ok {
		t.Fatalf("expected :one to be a Number, got %T", oneAV)
	}
	if oneN.Value != "1" {
		t.Errorf(":one value: got %q, want %q", oneN.Value, "1")
	}
}

// TestRecord_ZeroLimit_HardDeny verifies the zero/hard-deny semantic: a window
// limit of 0 trips on the very first attempt (post-increment count 1 > 0). This
// is the enforcement half of the >= 0 validation floor — a true hard deny when
// paired with onBreach:block/freeze. The breach-write fires too (count exceeds).
func TestRecord_ZeroLimit_HardDeny(t *testing.T) {
	fake := &fakeQuotaClient{countReturn: 1} // first attempt: ALL_NEW count = 1
	ctx := context.Background()
	limit := ActionLimit{Lifetime: ptr64(0), OnBreach: BreachBlock}

	d, err := Record(ctx, fake, "test-table", "sb-123", ActionGithubPR, limit)
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if !d.Tripped {
		t.Errorf("limit 0 should trip on the first attempt (count 1 > 0); got Tripped=false")
	}
	if d.WorstWindow != "lifetime" {
		t.Errorf("WorstWindow: got %q, want %q", d.WorstWindow, "lifetime")
	}
	// Two UpdateItem calls: the atomic ADD, then the breach-write (breached_at/on_breach).
	if len(fake.updateItemCalls) != 2 {
		t.Errorf("expected 2 UpdateItem calls (ADD + breach-write), got %d", len(fake.updateItemCalls))
	}
}

// TestTTL (QUO-05) — lifetime rows carry no TTL; hour/day rows carry TTL ~2h/~2d.
func TestTTL(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	// All three windows configured; capture all three UpdateItem calls.
	fake := &fakeQuotaClient{countReturn: 1}
	limit := ActionLimit{
		Lifetime: ptr64(100),
		PerHour:  ptr64(15),
		PerDay:   ptr64(50),
	}
	_, err := Record(ctx, fake, "test-table", "sb-ttl", ActionGithubPR, limit)
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if len(fake.updateItemCalls) != 3 {
		t.Fatalf("expected 3 UpdateItem calls (lifetime+hour+day), got %d", len(fake.updateItemCalls))
	}

	// Find each call by SK value.
	var lifetimeCall, hourCall, dayCall *dynamodb.UpdateItemInput
	for _, c := range fake.updateItemCalls {
		skAV, ok := c.Key["SK"]
		if !ok {
			t.Fatal("UpdateItem call missing SK key")
		}
		sk := skAV.(*dynamodbtypes.AttributeValueMemberS).Value
		switch {
		case sk == "lifetime":
			lifetimeCall = c
		case strings.HasPrefix(sk, "hour#"):
			hourCall = c
		case strings.HasPrefix(sk, "day#"):
			dayCall = c
		}
	}
	if lifetimeCall == nil {
		t.Fatal("missing UpdateItem call for lifetime SK")
	}
	if hourCall == nil {
		t.Fatal("missing UpdateItem call for hour SK")
	}
	if dayCall == nil {
		t.Fatal("missing UpdateItem call for day SK")
	}

	// 1. Lifetime: no "ttl" in ExpressionAttributeValues.
	if _, hasTTL := lifetimeCall.ExpressionAttributeValues[":ttl"]; hasTTL {
		t.Error("lifetime UpdateItem should NOT have :ttl in ExpressionAttributeValues")
	}
	if lifetimeCall.UpdateExpression != nil && strings.Contains(*lifetimeCall.UpdateExpression, "ttl") {
		t.Error("lifetime UpdateExpression should NOT reference ttl")
	}

	// 2. Hour row: TTL ≈ now + 2h (within 5 minutes tolerance).
	checkTTL(t, "hour", hourCall, now.Add(2*time.Hour), 5*time.Minute)

	// 3. Day row: TTL ≈ now + 2d (within 5 minutes tolerance).
	checkTTL(t, "day", dayCall, now.Add(48*time.Hour), 5*time.Minute)
}

// checkTTL asserts that the :ttl ExpressionAttributeValue in the given UpdateItem call
// is within tolerance of expectedTime (as a Unix epoch seconds number).
func checkTTL(t *testing.T, windowName string, call *dynamodb.UpdateItemInput, expectedTime time.Time, tolerance time.Duration) {
	t.Helper()
	ttlAV, ok := call.ExpressionAttributeValues[":ttl"]
	if !ok {
		t.Errorf("%s UpdateItem: expected :ttl in ExpressionAttributeValues", windowName)
		return
	}
	ttlN, ok := ttlAV.(*dynamodbtypes.AttributeValueMemberN)
	if !ok {
		t.Errorf("%s: expected :ttl to be a Number, got %T", windowName, ttlAV)
		return
	}
	ttlEpoch, err := strconv.ParseInt(ttlN.Value, 10, 64)
	if err != nil {
		t.Errorf("%s: failed to parse :ttl value %q: %v", windowName, ttlN.Value, err)
		return
	}
	got := time.Unix(ttlEpoch, 0).UTC()
	diff := got.Sub(expectedTime)
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		t.Errorf("%s TTL: got %v (%d), want ~%v (within %v)", windowName, got, ttlEpoch, expectedTime, tolerance)
	}
}

// multiCountClient returns sequential counts for each UpdateItem call.
type multiCountClient struct {
	counts  []int64
	callIdx int
}

func (m *multiCountClient) UpdateItem(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	count := int64(0)
	if m.callIdx < len(m.counts) {
		count = m.counts[m.callIdx]
	}
	m.callIdx++
	return &dynamodb.UpdateItemOutput{
		Attributes: map[string]dynamodbtypes.AttributeValue{
			"count": &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", count)},
		},
	}, nil
}

// ptr64 is a test helper that returns a pointer to an int64.
func ptr64(v int64) *int64 { return &v }

// i64 is a test helper that returns a pointer to an int64 (alias for ptr64).
func i64(v int64) *int64 { return &v }

// findBreachWrite searches updateItemCalls for an UpdateItem that writes breached_at
// and on_breach. Because the implementation uses ExpressionAttributeNames placeholders
// (#ba → "breached_at", #ob → "on_breach"), this function checks the attribute name
// MAP values rather than the UpdateExpression string (which only contains the placeholders).
func findBreachWrite(calls []*dynamodb.UpdateItemInput) (*dynamodb.UpdateItemInput, bool) {
	for _, c := range calls {
		hasBreachedAt := false
		hasOnBreach := false
		for _, attrName := range c.ExpressionAttributeNames {
			if attrName == "breached_at" {
				hasBreachedAt = true
			}
			if attrName == "on_breach" {
				hasOnBreach = true
			}
		}
		if hasBreachedAt && hasOnBreach {
			return c, true
		}
	}
	return nil, false
}

// TestRecord_WritesBreachedAt_OnTrip (ALR-01 production path) — when the post-increment
// count exceeds the window limit, Record must issue an additional UpdateItem that writes
// breached_at and on_breach (first-breach idempotency via if_not_exists).
func TestRecord_WritesBreachedAt_OnTrip(t *testing.T) {
	fake := &fakeQuotaClient{countReturn: 2} // count 2 > limit 1 → exceeded
	ctx := context.Background()
	limit := ActionLimit{
		PerHour:  i64(1),
		OnBreach: BreachWarn,
	}
	_, err := Record(ctx, fake, "test-table", "sb-breach", ActionGithubComment, limit)
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}

	breachCall, found := findBreachWrite(fake.updateItemCalls)
	if !found {
		t.Fatalf("expected a breach-write UpdateItem containing 'breached_at' and 'on_breach' in UpdateExpression; calls: %d", len(fake.updateItemCalls))
	}

	// Verify if_not_exists is used (first-breach idempotency).
	expr := *breachCall.UpdateExpression
	if !strings.Contains(expr, "if_not_exists") {
		t.Errorf("breach-write UpdateExpression must use if_not_exists; got: %q", expr)
	}

	// Verify :now is present and is a number attribute.
	nowAV, ok := breachCall.ExpressionAttributeValues[":now"]
	if !ok {
		t.Fatal("breach-write UpdateItem must include :now in ExpressionAttributeValues")
	}
	nowN, ok := nowAV.(*dynamodbtypes.AttributeValueMemberN)
	if !ok {
		t.Fatalf(":now must be a Number attribute, got %T", nowAV)
	}
	if nowN.Value == "" {
		t.Error(":now must be a non-empty number (Unix seconds)")
	}

	// Verify :policy is "warn" (matching BreachWarn).
	policyAV, ok := breachCall.ExpressionAttributeValues[":policy"]
	if !ok {
		t.Fatal("breach-write UpdateItem must include :policy in ExpressionAttributeValues")
	}
	policyS, ok := policyAV.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatalf(":policy must be a String attribute, got %T", policyAV)
	}
	if policyS.Value != string(BreachWarn) {
		t.Errorf(":policy value: got %q, want %q", policyS.Value, string(BreachWarn))
	}

	// Verify the breach-write targets the same row key (PK={sandbox}#{action}, SK=hour#...).
	pkAV, ok := breachCall.Key["PK"]
	if !ok {
		t.Fatal("breach-write UpdateItem must have PK key")
	}
	pkS := pkAV.(*dynamodbtypes.AttributeValueMemberS)
	wantPK := "sb-breach#github_comment"
	if pkS.Value != wantPK {
		t.Errorf("breach-write PK: got %q, want %q", pkS.Value, wantPK)
	}
	skAV, ok := breachCall.Key["SK"]
	if !ok {
		t.Fatal("breach-write UpdateItem must have SK key")
	}
	sk := skAV.(*dynamodbtypes.AttributeValueMemberS).Value
	if !strings.HasPrefix(sk, "hour#") {
		t.Errorf("breach-write SK: got %q, want prefix 'hour#'", sk)
	}
}

// TestRecord_NoBreachWrite_WhenUnderLimit — when the post-increment count does NOT
// exceed the window limit, Record must NOT issue any UpdateItem that writes breached_at.
func TestRecord_NoBreachWrite_WhenUnderLimit(t *testing.T) {
	fake := &fakeQuotaClient{countReturn: 1} // count 1 <= limit 5 → not exceeded
	ctx := context.Background()
	limit := ActionLimit{
		PerHour:  i64(5),
		OnBreach: BreachWarn,
	}
	_, err := Record(ctx, fake, "test-table", "sb-under", ActionGithubComment, limit)
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}

	_, found := findBreachWrite(fake.updateItemCalls)
	if found {
		t.Errorf("expected NO breach-write UpdateItem when count (%d) <= limit (%d); "+
			"found one: %q", 1, 5, *fake.updateItemCalls[len(fake.updateItemCalls)-1].UpdateExpression)
	}
}

// TestRecord_OnBreachPolicyPropagates — when OnBreach is BreachFreeze, the breach-write
// UpdateItem must carry :policy = "freeze" (not "warn" or "block").
func TestRecord_OnBreachPolicyPropagates(t *testing.T) {
	fake := &fakeQuotaClient{countReturn: 2} // count 2 > limit 1 → exceeded
	ctx := context.Background()
	limit := ActionLimit{
		PerHour:  i64(1),
		OnBreach: BreachFreeze,
	}
	_, err := Record(ctx, fake, "test-table", "sb-freeze", ActionSlackPost, limit)
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}

	breachCall, found := findBreachWrite(fake.updateItemCalls)
	if !found {
		t.Fatalf("expected a breach-write UpdateItem; calls=%d", len(fake.updateItemCalls))
	}

	policyAV, ok := breachCall.ExpressionAttributeValues[":policy"]
	if !ok {
		t.Fatal("breach-write must include :policy in ExpressionAttributeValues")
	}
	policyS, ok := policyAV.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatalf(":policy must be a String, got %T", policyAV)
	}
	if policyS.Value != string(BreachFreeze) {
		t.Errorf(":policy: got %q, want %q (BreachFreeze)", policyS.Value, string(BreachFreeze))
	}
}
