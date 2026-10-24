package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	slackbridge "github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

type fakeHistoryMeta struct {
	rows []slackbridge.ChannelMeta
	err  error
}

func (f *fakeHistoryMeta) ListMeta(ctx context.Context) ([]slackbridge.ChannelMeta, error) {
	return f.rows, f.err
}

type fakeDynamoTTL struct {
	ttlAttr, ttlStatus, pitr string
	err                      error
}

func (f *fakeDynamoTTL) DescribeTimeToLive(ctx context.Context, in *dynamodb.DescribeTimeToLiveInput, _ ...func(*dynamodb.Options)) (*dynamodb.DescribeTimeToLiveOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &dynamodb.DescribeTimeToLiveOutput{TimeToLiveDescription: &types.TimeToLiveDescription{
		AttributeName: aws.String(f.ttlAttr), TimeToLiveStatus: types.TimeToLiveStatus(f.ttlStatus)}}, nil
}

func (f *fakeDynamoTTL) DescribeContinuousBackups(ctx context.Context, in *dynamodb.DescribeContinuousBackupsInput, _ ...func(*dynamodb.Options)) (*dynamodb.DescribeContinuousBackupsOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &dynamodb.DescribeContinuousBackupsOutput{ContinuousBackupsDescription: &types.ContinuousBackupsDescription{
		PointInTimeRecoveryDescription: &types.PointInTimeRecoveryDescription{
			PointInTimeRecoveryStatus: types.PointInTimeRecoveryStatus(f.pitr)}}}, nil
}

type countingDescribe struct{ calls int }

func (c *countingDescribe) DescribeTable(ctx context.Context, in *dynamodb.DescribeTableInput, _ ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error) {
	c.calls++
	return &dynamodb.DescribeTableOutput{}, nil
}

func historyDoctorCfg(t *testing.T, routes ...config.SlackRoute) *config.Config {
	return historyCfg(t, routes...)
}

func globRoute() config.SlackRoute {
	return config.SlackRoute{Channel: "#inc_*", Alias: "ir",
		History: &config.SlackRouteHistory{Query: true, Mirror: true, Backfill: "3d", MaxMessages: 50}}
}

func byName(rs []CheckResult, name string) CheckResult {
	for _, r := range rs {
		if r.Name == name {
			return r
		}
	}
	return CheckResult{Name: "MISSING:" + name}
}

func goodTTL() *fakeDynamoTTL {
	return &fakeDynamoTTL{ttlAttr: "expires_at", ttlStatus: "ENABLED", pitr: "DISABLED"}
}

func scopesWith(s ...string) func(context.Context) ([]string, error) {
	return func(context.Context) ([]string, error) { return s, nil }
}

func TestCheckSlackHistory_SkippedWithoutHistoryRoutes(t *testing.T) {
	cfg := historyDoctorCfg(t, config.SlackRoute{Channel: "#plain", Alias: "p"})
	d := &countingDescribe{}
	res := checkSlackHistory(context.Background(), cfg, nil, d, goodTTL(), &fakeHistoryMeta{}, nil, "km")
	if len(res) != 1 || res[0].Status != CheckSkipped {
		t.Fatalf("want one SKIPPED, got %+v", res)
	}
	if d.calls != 0 {
		t.Errorf("describe called %d times; want zero AWS calls", d.calls)
	}
}

func TestCheckSlackHistory_GlobWithNoCurrentMatchIsOKWithNote(t *testing.T) {
	cfg := historyDoctorCfg(t, globRoute())
	lister := &fakeLister{}
	res := checkSlackHistory(context.Background(), cfg, lister, &mockDynamoClient{output: &dynamodb.DescribeTableOutput{}}, goodTTL(),
		&fakeHistoryMeta{}, scopesWith("groups:read"), "km")
	if len(res) != 4 {
		t.Fatalf("want 4 results, got %d", len(res))
	}
	r := byName(res, "Slack history routes")
	if r.Status != CheckOK || !strings.Contains(r.Message, "0 current channel") {
		t.Errorf("routes = %+v", r)
	}
}

func TestCheckSlackHistory_GlobCountsItsPatternChannels(t *testing.T) {
	cfg := historyDoctorCfg(t, globRoute())
	res := checkSlackHistory(context.Background(), cfg, &fakeLister{channels: historyChannels}, &mockDynamoClient{output: &dynamodb.DescribeTableOutput{}}, goodTTL(),
		&fakeHistoryMeta{}, scopesWith("groups:read"), "km")
	if r := byName(res, "Slack history routes"); r.Status != CheckOK || !strings.Contains(r.Message, "#inc_* → ir (2 current channel(s))") {
		t.Errorf("routes = %+v", r)
	}
}

func TestCheckSlackHistory_CountsUnboundStaticMirrorChannels(t *testing.T) {
	cfg := historyDoctorCfg(t, globRoute(), config.SlackRoute{Channel: "#sec-1", Alias: "sec", History: &config.SlackRouteHistory{Query: true, Mirror: true}})
	meta := &fakeHistoryMeta{rows: []slackbridge.ChannelMeta{{Channel: "C1", Name: "inc_2026_a", Route: slackbridge.RouteEntry{Alias: "ir", Pattern: "inc_*"}, BackfillState: "done"}}}
	res := checkSlackHistory(context.Background(), cfg, &fakeLister{channels: historyChannels}, &mockDynamoClient{output: &dynamodb.DescribeTableOutput{}}, goodTTL(),
		meta, scopesWith("groups:read"), "km")
	r := byName(res, "Slack history channels")
	// C2 (glob match) and C3 (#sec-1) mirror statically with no meta row; C1 has one.
	if !strings.Contains(r.Message, "1 matched channel(s); 2 static mirror channel(s) not yet bound") || !strings.Contains(r.Message, "meta rows only") {
		t.Errorf("channels = %+v", r)
	}
	allBound := &fakeHistoryMeta{rows: []slackbridge.ChannelMeta{{Channel: "C1"}, {Channel: "C2"}, {Channel: "C3"}}}
	res = checkSlackHistory(context.Background(), cfg, &fakeLister{channels: historyChannels}, &mockDynamoClient{output: &dynamodb.DescribeTableOutput{}}, goodTTL(),
		allBound, scopesWith("groups:read"), "km")
	if r := byName(res, "Slack history channels"); strings.Contains(r.Message, "not yet bound") {
		t.Errorf("every channel bound ⇒ no note: %+v", r)
	}
}

func TestCheckSlackHistory_GroupsReadMissingWarnsOnlyWithGlobs(t *testing.T) {
	ok := &mockDynamoClient{output: &dynamodb.DescribeTableOutput{}}
	glob := historyDoctorCfg(t, globRoute())
	res := checkSlackHistory(context.Background(), glob, &fakeLister{}, ok, goodTTL(), &fakeHistoryMeta{}, scopesWith("chat:write"), "km")
	if s := byName(res, "Slack history scopes"); s.Status != CheckWarn {
		t.Errorf("glob without groups:read = %+v; want WARN", s)
	}
	exact := historyDoctorCfg(t, config.SlackRoute{Channel: "#one", Alias: "o",
		History: &config.SlackRouteHistory{Query: true}})
	res = checkSlackHistory(context.Background(), exact, &fakeLister{}, ok, goodTTL(), &fakeHistoryMeta{}, scopesWith("chat:write"), "km")
	if s := byName(res, "Slack history scopes"); s.Status != CheckOK {
		t.Errorf("no glob = %+v; want OK", s)
	}
}

func TestCheckSlackHistory_TableTTLAndPITR(t *testing.T) {
	cfg := historyDoctorCfg(t, globRoute())
	ok := &mockDynamoClient{output: &dynamodb.DescribeTableOutput{}}
	res := checkSlackHistory(context.Background(), cfg, &fakeLister{}, ok, goodTTL(), &fakeHistoryMeta{}, scopesWith("groups:read"), "km")
	if r := byName(res, "Slack history table"); r.Status != CheckOK {
		t.Errorf("good table = %+v", r)
	}
	pitr := &fakeDynamoTTL{ttlAttr: "expires_at", ttlStatus: "ENABLED", pitr: "ENABLED"}
	res = checkSlackHistory(context.Background(), cfg, &fakeLister{}, ok, pitr, &fakeHistoryMeta{}, scopesWith("groups:read"), "km")
	if r := byName(res, "Slack history table"); r.Status != CheckWarn || !strings.Contains(r.Message, "point-in-time recovery must be off") {
		t.Errorf("pitr on = %+v", r)
	}
	noTTL := &fakeDynamoTTL{ttlAttr: "expires_at", ttlStatus: "DISABLED", pitr: "DISABLED"}
	res = checkSlackHistory(context.Background(), cfg, &fakeLister{}, ok, noTTL, &fakeHistoryMeta{}, scopesWith("groups:read"), "km")
	if r := byName(res, "Slack history table"); r.Status != CheckWarn || !strings.Contains(r.Message, "TTL") {
		t.Errorf("ttl off = %+v", r)
	}
}

func TestCheckSlackHistory_ChannelsReport(t *testing.T) {
	cfg := historyDoctorCfg(t, globRoute())
	ok := &mockDynamoClient{output: &dynamodb.DescribeTableOutput{}}
	mk := func(rows ...slackbridge.ChannelMeta) []CheckResult {
		return checkSlackHistory(context.Background(), cfg, &fakeLister{}, ok, goodTTL(), &fakeHistoryMeta{rows: rows}, scopesWith("groups:read"), "km")
	}
	r := byName(mk(slackbridge.ChannelMeta{Channel: "C1", Name: "inc_a", Route: slackbridge.RouteEntry{Alias: "ir", Pattern: "inc_*"}, BackfillState: "done", BackfillWritten: 200}), "Slack history channels")
	if r.Status != CheckOK || !strings.Contains(r.Message, "inc_a") || !strings.Contains(r.Message, "done") || !strings.Contains(r.Message, "rows=200") {
		t.Errorf("done row = %+v", r)
	}
	r = byName(mk(slackbridge.ChannelMeta{Channel: "C2", Name: "inc_b", BackfillState: "rate_limited"}), "Slack history channels")
	if r.Status != CheckWarn || !strings.Contains(r.Message, "rate_limited") {
		t.Errorf("rate_limited row = %+v", r)
	}
	r = byName(mk(slackbridge.ChannelMeta{Channel: "C3", Name: "inc_c", BackfillState: "done", Overlap: "inc_*x"}), "Slack history channels")
	if r.Status != CheckWarn || !strings.Contains(r.Message, "OVERLAP") {
		t.Errorf("overlap row = %+v", r)
	}
}

func TestCheckSlackHistory_MetaListErrorIsWarn(t *testing.T) {
	cfg := historyDoctorCfg(t, globRoute())
	ok := &mockDynamoClient{output: &dynamodb.DescribeTableOutput{}}
	res := checkSlackHistory(context.Background(), cfg, &fakeLister{}, ok, goodTTL(), &fakeHistoryMeta{err: errors.New("boom")}, scopesWith("groups:read"), "km")
	if r := byName(res, "Slack history channels"); r.Status != CheckWarn || !strings.Contains(r.Message, "boom") {
		t.Errorf("meta err = %+v", r)
	}
}
