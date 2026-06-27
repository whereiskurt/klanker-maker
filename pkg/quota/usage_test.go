// Package quota tests — usage_test.go
// FetchUsage: read live action-quota counters for km status display (Phase 121 follow-up).
package quota

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeGetItemClient is a test double for the GetItemAPI interface. It returns a
// count for any row key present in `counts` (keyed by "PK|SK"), a missing row
// (nil Item) otherwise, and an error when the key is in `errKeys`.
type fakeGetItemClient struct {
	counts  map[string]int64
	errKeys map[string]bool
	calls   []string
}

func (f *fakeGetItemClient) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	pk := in.Key["PK"].(*dynamodbtypes.AttributeValueMemberS).Value
	sk := in.Key["SK"].(*dynamodbtypes.AttributeValueMemberS).Value
	key := pk + "|" + sk
	f.calls = append(f.calls, key)
	if f.errKeys[key] {
		return nil, errors.New("boom")
	}
	if c, ok := f.counts[key]; ok {
		return &dynamodb.GetItemOutput{
			Item: map[string]dynamodbtypes.AttributeValue{
				"count": &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", c)},
			},
		}, nil
	}
	// Missing row → nil Item → count 0.
	return &dynamodb.GetItemOutput{}, nil
}

func TestFetchUsage(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	hourSK := fmt.Sprintf("hour#%d", hourBucket(now))

	limits := Limits{
		ActionSlackPost: {
			Lifetime: ptr64(100),
			OnBreach: BreachBlock,
		},
		ActionEmailSend: {
			PerHour:  ptr64(10),
			PerDay:   ptr64(50),
			OnBreach: BreachWarn,
		},
		// github_pr has NO configured windows → produces no rows.
		ActionGithubPR: {OnBreach: BreachBlock},
	}

	fake := &fakeGetItemClient{
		counts: map[string]int64{
			"sb-abc#slack_post|lifetime": 3,
			"sb-abc#email_send|" + hourSK: 2,
			// email_send day row missing → count 0.
		},
	}

	rows, err := FetchUsage(ctx, fake, "test-table", "sb-abc", limits)
	if err != nil {
		t.Fatalf("FetchUsage returned error: %v", err)
	}

	// slack_post lifetime + email_send hour + email_send day = 3 rows.
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d: %+v", len(rows), rows)
	}

	byKey := map[string]UsageRow{}
	for _, r := range rows {
		byKey[string(r.Action)+"/"+r.Window] = r
	}

	sp := byKey["slack_post/lifetime"]
	if sp.Used != 3 || sp.Limit != 100 || sp.OnBreach != BreachBlock {
		t.Errorf("slack_post/lifetime: got Used=%d Limit=%d OnBreach=%q, want 3/100/block", sp.Used, sp.Limit, sp.OnBreach)
	}

	eh := byKey["email_send/hour"]
	if eh.Used != 2 || eh.Limit != 10 || eh.OnBreach != BreachWarn {
		t.Errorf("email_send/hour: got Used=%d Limit=%d OnBreach=%q, want 2/10/warn", eh.Used, eh.Limit, eh.OnBreach)
	}

	ed := byKey["email_send/day"]
	if ed.Used != 0 || ed.Limit != 50 {
		t.Errorf("email_send/day (missing row): got Used=%d Limit=%d, want 0/50", ed.Used, ed.Limit)
	}

	// Determinism: rows for the same action are ordered lifetime, hour, day.
	// email_send only has hour, day — verify hour precedes day.
	var hourIdx, dayIdx = -1, -1
	for i, r := range rows {
		if r.Action == ActionEmailSend && r.Window == "hour" {
			hourIdx = i
		}
		if r.Action == ActionEmailSend && r.Window == "day" {
			dayIdx = i
		}
	}
	if hourIdx < 0 || dayIdx < 0 || hourIdx > dayIdx {
		t.Errorf("window order: hour (%d) should precede day (%d)", hourIdx, dayIdx)
	}
}

// TestFetchUsage_NoWindows — an action with no configured windows yields no rows.
func TestFetchUsage_NoWindows(t *testing.T) {
	ctx := context.Background()
	limits := Limits{
		ActionGithubReview: {OnBreach: BreachBlock}, // no Lifetime/PerHour/PerDay
	}
	fake := &fakeGetItemClient{}
	rows, err := FetchUsage(ctx, fake, "test-table", "sb-1", limits)
	if err != nil {
		t.Fatalf("FetchUsage returned error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows for action with no windows, got %d", len(rows))
	}
	if len(fake.calls) != 0 {
		t.Errorf("expected 0 GetItem calls, got %d", len(fake.calls))
	}
}

// TestFetchUsage_Error — a GetItem error on any row surfaces as an error.
func TestFetchUsage_Error(t *testing.T) {
	ctx := context.Background()
	limits := Limits{
		ActionSlackPost: {Lifetime: ptr64(5), OnBreach: BreachBlock},
	}
	fake := &fakeGetItemClient{
		errKeys: map[string]bool{"sb-x#slack_post|lifetime": true},
	}
	_, err := FetchUsage(ctx, fake, "test-table", "sb-x", limits)
	if err == nil {
		t.Fatal("expected FetchUsage to return an error when GetItem fails")
	}
}

// compile-time assertion that *dynamodb.Client satisfies GetItemAPI.
var _ GetItemAPI = (*dynamodb.Client)(nil)
var _ = awssdk.String
