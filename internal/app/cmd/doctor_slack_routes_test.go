package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

func TestCheckSlackChannelRoutes_SkippedWhenAbsent(t *testing.T) {
	rs := checkSlackChannelRoutes(context.Background(), &config.Config{}, nil, nil)
	if len(rs) != 1 || rs[0].Status != CheckSkipped || rs[0].Name != "Slack channel routes" {
		t.Fatalf("want one SKIPPED, got %+v", rs)
	}
}

func TestCheckSlackChannelRoutes_GreenAndDrift(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	cfg := routesCfg(t, []config.SlackRoute{{Channel: "#sec", Alias: "ir-bot", Profile: p, AllowEmails: []string{"k@example.com"}}}, nil)
	plan, err := effectiveSlackChannelRoutes(cfg, botChannels)
	if err != nil {
		t.Fatal(err)
	}
	param := slackRoutesParam(cfg)
	ssm := &fakeRoutesSSM{m: map[string]string{param: string(plan.JSON)}}
	rs := checkSlackChannelRoutes(context.Background(), cfg, &fakeLister{channels: botChannels}, ssm)
	if len(rs) != 3 {
		t.Fatalf("want 3 results, got %+v", rs)
	}
	for _, name := range []string{"Slack channel routes profiles", "Slack channel routes channels", "Slack channel routes table"} {
		if st, msg := statusOf(rs, name); st != CheckOK {
			t.Errorf("%s: %s (%s)", name, st, msg)
		}
	}
	// Drift: the live table differs from what the config produces now.
	ssm.m[param] = `{"v":1,"routes":[]}`
	rs = checkSlackChannelRoutes(context.Background(), cfg, &fakeLister{channels: botChannels}, ssm)
	if st, msg := statusOf(rs, "Slack channel routes table"); st != CheckWarn || !strings.Contains(msg+rsRemediation(rs, "Slack channel routes table"), "km init --slack") {
		t.Errorf("drift must WARN naming km init --slack: %s %s", st, msg)
	}
	// Absent table.
	delete(ssm.m, param)
	rs = checkSlackChannelRoutes(context.Background(), cfg, &fakeLister{channels: botChannels}, ssm)
	if st, _ := statusOf(rs, "Slack channel routes table"); st != CheckWarn {
		t.Error("absent table must WARN")
	}
	if !strings.Contains(rsRemediation(rs, "Slack channel routes table"), "km init --slack") {
		t.Error("absent table must name km init --slack")
	}
}

func TestCheckSlackChannelRoutes_UnknownChannelAndBadProfile(t *testing.T) {
	dir := t.TempDir()
	noIn := writeProfile(t, dir, "noin.yaml", routeProfileNoInbound)
	cfg := routesCfg(t, []config.SlackRoute{{Channel: "#nope", Alias: "a", Profile: noIn, AllowEmails: []string{"k@example.com"}}}, nil)
	rs := checkSlackChannelRoutes(context.Background(), cfg, &fakeLister{channels: botChannels}, &fakeRoutesSSM{m: map[string]string{}})
	if st, msg := statusOf(rs, "Slack channel routes profiles"); st != CheckWarn || !strings.Contains(msg, "inbound") {
		t.Errorf("profile: %s %s", st, msg)
	}
	if st, msg := statusOf(rs, "Slack channel routes channels"); st != CheckWarn || !strings.Contains(msg, "#nope") {
		t.Errorf("channels: %s %s", st, msg)
	}
}

func TestCheckSlackChannelRoutes_NilListerSkipsChannelsAndTable(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	cfg := routesCfg(t, []config.SlackRoute{{Channel: "#sec", Alias: "ir-bot", Profile: p, AllowEmails: []string{"k@example.com"}}}, nil)
	rs := checkSlackChannelRoutes(context.Background(), cfg, nil, nil)
	if len(rs) != 3 {
		t.Fatalf("want 3, got %+v", rs)
	}
	for _, n := range []string{"Slack channel routes channels", "Slack channel routes table"} {
		if st, _ := statusOf(rs, n); st != CheckSkipped {
			t.Errorf("%s: want SKIPPED, got %s", n, st)
		}
	}
	if st, _ := statusOf(rs, "Slack channel routes profiles"); st != CheckOK {
		t.Error("profiles result must not depend on the lister")
	}
}

func rsRemediation(rs []CheckResult, name string) string {
	for _, r := range rs {
		if r.Name == name {
			return r.Remediation
		}
	}
	return ""
}
