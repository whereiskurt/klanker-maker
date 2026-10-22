package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmslack "github.com/whereiskurt/klanker-maker/pkg/slack"
	slackbridge "github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// envSlackRoutesSHA is exported for infra/live/use1/lambda-slack-bridge: the
// hex SHA-256 of the SSM table bytes. It is only a cold-start trigger — the
// bridge reads the table from SSM — so a wrong or empty value never misroutes;
// it only decides whether the next apply restarts the Lambda.
const envSlackRoutesSHA = "KM_SLACK_ROUTES_SHA256"

// SlackRouteChannelLister is the one Slack call channel-route resolution makes;
// *kmslack.Client satisfies it.
type SlackRouteChannelLister interface {
	ListBotChannels(ctx context.Context) ([]kmslack.SlackChannelSummary, error)
}

// slackRoutesPlan is a resolved table: the entries to write, the distinct
// profiles to pre-stage, and the canonical bytes + hash.
type slackRoutesPlan struct {
	Entries  []slackbridge.RouteEntry
	Profiles []*config.SlackDefaultRoute // one per distinct pre-stage slug
	JSON     []byte
	SHA      string
}

// effectiveSlackChannelRoutes resolves slack.routes against the bot's channel
// list. Fail-closed: any route that cannot be resolved, gated, or whose profile
// cannot answer stops the caller before any apply.
func effectiveSlackChannelRoutes(cfg *config.Config, channels []kmslack.SlackChannelSummary) (*slackRoutesPlan, error) {
	if err := cfg.Slack.ValidateRoutes(); err != nil {
		return nil, err
	}
	byName := map[string]kmslack.SlackChannelSummary{}
	byID := map[string]kmslack.SlackChannelSummary{}
	for _, ch := range channels {
		byName[ch.Name] = ch
		byID[ch.ID] = ch
	}
	installList := cfg.Slack.DefaultRoute.NormalizedEmails()
	plan := &slackRoutesPlan{}
	seen := map[string]int{}    // channel id → route index
	staged := map[string]bool{} // pre-stage slug
	// slug → (label, located profile path). The slug is the lowercased base name,
	// so two different files named bot.yaml would share one staged object and
	// the second channel would cold-create the wrong profile. Seeded with the
	// default route's profile so a channel route cannot collide with it either.
	type slugOwner struct{ label, path string }
	slugPaths := map[string]slugOwner{}
	profilePath := func(prof string) string {
		v := &config.SlackDefaultRoute{Profile: prof}
		if p, err := locateSlackDefaultRouteProfile(v, cfg.ProfileSearchPaths); err == nil {
			return filepath.Clean(p)
		}
		return "builtin:" + SlackDefaultRouteProfileSlug(prof)
	}
	if dr := cfg.Slack.DefaultRoute; dr != nil && strings.TrimSpace(dr.Profile) != "" {
		slugPaths[SlackDefaultRouteProfileSlug(dr.Profile)] = slugOwner{"slack.default_route", profilePath(dr.Profile)}
	}
	for i := range cfg.Slack.Routes {
		r := &cfg.Slack.Routes[i]
		label := fmt.Sprintf("slack.routes[%d] (%s)", i, strings.TrimSpace(r.Channel))

		var ch kmslack.SlackChannelSummary
		var ok bool
		if name, isName := r.ChannelName(); isName {
			if ch, ok = byName[name]; !ok {
				return nil, fmt.Errorf("%s: no channel named #%s among the channels the bot is a member of (invite the bot, or the channel was renamed); bot channels: %s", label, name, botChannelNames(channels))
			}
		} else if id, isID := r.ChannelID(); isID {
			if ch, ok = byID[id]; !ok {
				return nil, fmt.Errorf("%s: the bot is not a member of channel %s (invite it first)", label, id)
			}
		}
		if j, dup := seen[ch.ID]; dup {
			return nil, fmt.Errorf("%s: resolves to channel %s (#%s), which slack.routes[%d] already names — a channel is listed twice", label, ch.ID, ch.Name, j)
		}
		seen[ch.ID] = i

		view := &config.SlackDefaultRoute{Alias: r.Alias, Profile: r.Profile}
		profileAllow, err := routeProfileInboundAllow(label+".profile", view, cfg.ProfileSearchPaths)
		if err != nil {
			return nil, err
		}
		allow := r.NormalizedEmails()
		source := "route allow_emails"
		if len(allow) == 0 {
			allow, source = installList, "slack.default_route.allow_emails"
		}
		if len(allow) == 0 {
			allow, source = profileAllow, "profile allowEmails"
		}
		if len(allow) == 0 {
			return nil, fmt.Errorf("%s has no allowlist: set allow_emails on the route, slack.default_route.allow_emails, or notification.slack.inbound.allowEmails in %q — a route is deny-by-default", label, r.Profile)
		}
		fmt.Fprintf(os.Stderr, "[info] slack.routes: #%s (%s) → alias %q, %d allowed (%s)\n", ch.Name, ch.ID, r.Alias, len(allow), source)

		slug := SlackDefaultRouteProfileSlug(r.Profile)
		pp := profilePath(r.Profile)
		if prev, ok := slugPaths[slug]; ok && prev.path != pp {
			return nil, fmt.Errorf("%s: profile %q (%s) has the same name %q as the profile of %s (%s); both would stage to slack-profiles/%s/ and one channel would cold-create the wrong profile — rename one file", label, r.Profile, pp, slug, prev.label, prev.path, slug)
		} else if !ok {
			slugPaths[slug] = slugOwner{label, pp}
		}
		if !staged[slug] {
			staged[slug] = true
			plan.Profiles = append(plan.Profiles, view)
		}
		plan.Entries = append(plan.Entries, slackbridge.RouteEntry{
			Channel: ch.ID, Name: ch.Name, Alias: r.Alias, Profile: slug,
			Allow: allow, MentionOnly: r.MentionRequired(),
		})
	}
	b, err := slackbridge.CanonicalRoutesJSON(plan.Entries)
	if err != nil {
		return nil, fmt.Errorf("slack.routes: encode: %w", err)
	}
	plan.JSON, plan.SHA = b, slackbridge.RoutesSHA256(b)
	return plan, nil
}

func botChannelNames(channels []kmslack.SlackChannelSummary) string {
	names := make([]string, 0, len(channels))
	for _, ch := range channels {
		names = append(names, "#"+ch.Name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// slackRoutesParam is the SSM path for this install's table.
func slackRoutesParam(cfg *config.Config) string {
	return slackbridge.RoutesParamPath(strings.Trim(cfg.GetSsmPrefix(), "/"))
}

// prepareSlackChannelRoutes is the FAIL-CLOSED apply-time form (full km init,
// km init --slack, km slack init): resolve, pre-stage every distinct route
// profile, write the table, export the hash. No routes ⇒ no Slack call; a
// table left by an earlier config is overwritten with the empty document so a
// removed block cannot keep routing; hash "" when nothing is written.
func prepareSlackChannelRoutes(ctx context.Context, cfg *config.Config, lister SlackRouteChannelLister, store SSMParamStore, up S3ProfileUploader) error {
	param := slackRoutesParam(cfg)
	writer, canWrite := store.(ssmParamWriter)
	if len(cfg.Slack.Routes) == 0 {
		os.Setenv(envSlackRoutesSHA, "") //nolint:errcheck
		existing, gerr := store.Get(ctx, param, false)
		if gerr != nil {
			// ParameterNotFound is already ("", nil) in the production store, so an
			// error here is real; proceeding would leave a removed table routing.
			return fmt.Errorf("km init: slack.routes removed but the old table at %s could not be read: %w", param, gerr)
		}
		if existing != "" && canWrite {
			empty, _ := slackbridge.CanonicalRoutesJSON(nil)
			if string(existing) == string(empty) {
				os.Setenv(envSlackRoutesSHA, slackbridge.RoutesSHA256(empty)) //nolint:errcheck
				return nil
			}
			if err := writer.Put(ctx, param, string(empty), false); err != nil {
				return fmt.Errorf("km init: slack.routes removed but the old table at %s could not be emptied: %w", param, err)
			}
			os.Setenv(envSlackRoutesSHA, slackbridge.RoutesSHA256(empty)) //nolint:errcheck
			fmt.Fprintf(os.Stderr, "[info] slack.routes: no routes configured; emptied %s\n", param)
		}
		return nil
	}
	if !canWrite {
		return fmt.Errorf("km init: slack.routes configured but the SSM store is read-only")
	}
	channels, err := lister.ListBotChannels(ctx)
	if err != nil {
		return fmt.Errorf("km init: slack.routes: list the bot's channels (users.conversations): %w", err)
	}
	plan, err := effectiveSlackChannelRoutes(cfg, channels)
	if err != nil {
		return fmt.Errorf("km init: %w", err)
	}
	if up != nil && cfg.ArtifactsBucket != "" {
		for _, view := range plan.Profiles {
			if err := PreStageSlackDefaultRouteProfile(ctx, view, cfg.ArtifactsBucket, cfg.ProfileSearchPaths, up); err != nil {
				return fmt.Errorf("km init: slack.routes: %w", err)
			}
		}
	}
	if err := writer.Put(ctx, param, string(plan.JSON), false); err != nil {
		return fmt.Errorf("km init: slack.routes: write %s: %w", param, err)
	}
	os.Setenv(envSlackRoutesSHA, plan.SHA) //nolint:errcheck
	fmt.Fprintf(os.Stderr, "[info] slack.routes: %d route(s) written to %s (sha256 %s…)\n", len(plan.Entries), param, plan.SHA[:12])
	return nil
}

// exportSlackRoutesHashFromSSM is the BEST-EFFORT plan-time form (km init
// --plan, ExportTerragruntEnvVars callers that never apply the bridge): no
// Slack call, no write; the hash of whatever table is live, "" when none, so
// a plan after a config edit shows no spurious env diff. km doctor reports the
// drift instead.
func exportSlackRoutesHashFromSSM(ctx context.Context, cfg *config.Config, store SSMParamStore) {
	val, err := store.Get(ctx, slackRoutesParam(cfg), false)
	if err != nil || val == "" {
		os.Setenv(envSlackRoutesSHA, "") //nolint:errcheck
		return
	}
	os.Setenv(envSlackRoutesSHA, slackbridge.RoutesSHA256([]byte(val))) //nolint:errcheck
}

// newSlackRouteLister builds the Slack client channel-route resolution needs
// from the bot token in SSM. No token ⇒ error naming km slack init.
func newSlackRouteLister(ctx context.Context, cfg *config.Config, store SSMParamStore) (SlackRouteChannelLister, error) {
	tok, err := store.Get(ctx, cfg.GetSsmPrefix()+"slack/bot-token", true)
	if err != nil || strings.TrimSpace(tok) == "" {
		return nil, fmt.Errorf("slack.routes is configured but %sslack/bot-token is not set — run `km slack init` first (%v)", cfg.GetSsmPrefix(), err)
	}
	return kmslack.NewClient(strings.TrimSpace(tok), nil), nil
}

// prepareSlackChannelRoutesWithAWS is prepareSlackChannelRoutes with production
// deps: SSM for the table and the bot token, a Slack client built from that
// token (only when routes are configured), S3 for pre-stage. Every command that
// applies lambda-slack-bridge calls it right after prepareSlackDefaultRoute.
func prepareSlackChannelRoutesWithAWS(ctx context.Context, cfg *config.Config, awsCfg aws.Config, up S3ProfileUploader) error {
	store := &productionSSMParamStore{client: ssm.NewFromConfig(awsCfg)}
	var lister SlackRouteChannelLister
	if len(cfg.Slack.Routes) > 0 {
		l, err := newSlackRouteLister(ctx, cfg, store)
		if err != nil {
			return err
		}
		lister = l
	}
	return prepareSlackChannelRoutes(ctx, cfg, lister, store, up)
}
