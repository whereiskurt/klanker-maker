package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	slackbridge "github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// checkSlackChannelRoutes validates slack.routes. Read-only: one
// users.conversations listing, one SSM read, no writes. One SKIPPED result
// when the block is absent; otherwise three results (profiles, channels,
// table), evaluated once. The profiles result never depends on the channel
// resolution.
func checkSlackChannelRoutes(ctx context.Context, cfg *config.Config, lister SlackRouteChannelLister, store SSMParamStore) []CheckResult {
	if cfg == nil || len(cfg.Slack.Routes) == 0 {
		return []CheckResult{{Name: "Slack channel routes", Status: CheckSkipped, Message: "slack.routes not set — no channel routes"}}
	}
	remediation := "Fix km-config.yaml slack.routes (and the profiles), then `km init --slack`."
	profiles := CheckResult{Name: "Slack channel routes profiles", Status: CheckOK}
	channels := CheckResult{Name: "Slack channel routes channels", Status: CheckOK}
	table := CheckResult{Name: "Slack channel routes table", Status: CheckOK}

	// 1. Profiles: shape + each route's profile resolves with Slack inbound.
	var profileErrs []string
	if err := cfg.Slack.ValidateRoutes(); err != nil {
		profileErrs = append(profileErrs, err.Error())
	}
	for i := range cfg.Slack.Routes {
		r := &cfg.Slack.Routes[i]
		view := &config.SlackDefaultRoute{Alias: r.Alias, Profile: r.Profile}
		if _, err := routeProfileInboundAllow(fmt.Sprintf("slack.routes[%d].profile", i), view, cfg.ProfileSearchPaths); err != nil {
			profileErrs = append(profileErrs, err.Error())
		}
	}
	if len(profileErrs) > 0 {
		profiles.Status, profiles.Message, profiles.Remediation = CheckWarn, strings.Join(profileErrs, "; "), remediation
	} else {
		profiles.Message = fmt.Sprintf("%d route(s), every profile resolves with Slack inbound enabled", len(cfg.Slack.Routes))
	}

	// 2. Channels: every route resolves among the bot's channels; 3. Table:
	// the live parameter equals what the config produces now.
	if lister == nil || store == nil {
		channels.Status, channels.Message = CheckSkipped, "no Slack client (bot token not configured)"
		table.Status, table.Message = CheckSkipped, "not evaluated without a Slack client"
		return []CheckResult{profiles, channels, table}
	}
	list, err := lister.ListBotChannels(ctx)
	if err != nil {
		channels.Status, channels.Message = CheckWarn, "users.conversations failed: "+err.Error()
		table.Status, table.Message = CheckSkipped, "not evaluated: channel listing failed"
		return []CheckResult{profiles, channels, table}
	}
	plan, err := effectiveSlackChannelRoutes(cfg, list)
	if err != nil {
		channels.Status, channels.Message, channels.Remediation = CheckWarn, err.Error(), remediation
		table.Status, table.Message = CheckSkipped, "not evaluated: routes do not resolve"
		return []CheckResult{profiles, channels, table}
	}
	names := make([]string, 0, len(plan.Entries))
	for _, e := range plan.Entries {
		names = append(names, fmt.Sprintf("#%s→%s", e.Name, e.Alias))
	}
	for _, p := range plan.Patterns {
		names = append(names, fmt.Sprintf("#%s→%s (%d current)", p.Glob, p.Alias, len(p.Channels)))
	}
	channels.Message = strings.Join(names, ", ")

	param := slackRoutesParam(cfg)
	live, gerr := store.Get(ctx, param, false)
	switch {
	case gerr != nil || live == "":
		table.Status, table.Message = CheckWarn, param+" is absent — the bridge has no channel routes (run `km init --slack`)"
		table.Remediation = "Run `km init --slack`."
	case slackbridge.RoutesSHA256([]byte(live)) != plan.SHA:
		table.Status, table.Message = CheckWarn, "the live table differs from km-config.yaml slack.routes (drift; run `km init --slack`)"
		table.Remediation = "Run `km init --slack` to publish the current routes."
	default:
		table.Message = fmt.Sprintf("%s matches km-config.yaml (sha256 %s…)", param, plan.SHA[:12])
	}
	return []CheckResult{profiles, channels, table}
}
