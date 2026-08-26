package webhook

import (
	"context"
	"errors"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

type fakeNonce struct {
	seen map[string]bool
	err  error
}

func (f *fakeNonce) CheckAndStore(_ context.Context, key string, _ int) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if f.seen[key] {
		return true, nil
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	f.seen[key] = true
	return false, nil
}

type fakeRate struct {
	n   int64
	err error
}

func (f *fakeRate) Increment(_ context.Context, _ string, _ int) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.n++
	return f.n, nil
}

func TestReplayKey_UsesDeliveryKey(t *testing.T) {
	env := envFixture(t)
	got := ReplayKey("wiz", env)
	want := "wh:wiz:iss-1:CREATED:2026-08-25T10:00:00Z"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCooldownKey_ExpandsGroupBy(t *testing.T) {
	env := envFixture(t)
	got := CooldownKey("wiz", 0, "{{entity.cloud_id}}", env)
	want := "wh-cd:wiz:0:arn:aws:s3:::logs"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A group_by naming a missing field must NOT collapse distinct entities onto one
// key — that would suppress unrelated alerts for the whole window.
func TestCooldownKey_MissingFieldStaysDistinct(t *testing.T) {
	env := envFixture(t)
	a := CooldownKey("wiz", 0, "{{entity.nope}}", env)

	other, err := ParseEnvelope([]byte(`{"km_schema":"v1","id":"iss-2","severity":"HIGH","entity":{"cloud_id":"x"}}`),
		config.WebhookSource{Name: "wiz"})
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	b := CooldownKey("wiz", 0, "{{entity.nope}}", other)

	if a == b {
		t.Fatalf("distinct payloads collapsed onto one cooldown key: %q", a)
	}
}

func TestCooldownKey_EmptyGroupByFallsBackToDeliveryKey(t *testing.T) {
	env := envFixture(t)
	if got, want := CooldownKey("wiz", 2, "", env), "wh-cd:wiz:2:"+env.DeliveryKey; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRateKey_BucketsByWindow(t *testing.T) {
	if a, b := RateKey(1000, 600), RateKey(1100, 600); a != b {
		t.Errorf("same window must share a bucket: %q vs %q", a, b)
	}
	if a, b := RateKey(1000, 600), RateKey(1700, 600); a == b {
		t.Errorf("different windows must not share a bucket: %q", a)
	}
}

func TestCheckRate(t *testing.T) {
	ctx := context.Background()
	limit := &config.WebhookRateLimit{MaxDispatches: 2, WindowSeconds: 600}

	rc := &fakeRate{}
	if !CheckRate(ctx, rc, limit, 1000) {
		t.Error("1st dispatch must be allowed")
	}
	if !CheckRate(ctx, rc, limit, 1000) {
		t.Error("2nd dispatch must be allowed (at the cap)")
	}
	if CheckRate(ctx, rc, limit, 1000) {
		t.Error("3rd dispatch must be blocked (over the cap)")
	}
}

func TestCheckRate_NilLimitAllows(t *testing.T) {
	if !CheckRate(context.Background(), &fakeRate{}, nil, 1000) {
		t.Error("absent rate_limit must allow (dormant)")
	}
}

// Fail-open: a counter error must never strand a real alert.
func TestCheckRate_ErrorFailsOpen(t *testing.T) {
	rc := &fakeRate{err: errors.New("ddb down")}
	limit := &config.WebhookRateLimit{MaxDispatches: 1, WindowSeconds: 600}
	if !CheckRate(context.Background(), rc, limit, 1000) {
		t.Error("rate counter error must fail OPEN")
	}
}
