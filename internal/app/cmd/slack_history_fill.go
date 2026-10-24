package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	slackbridge "github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// fillTarget is one channel `fill` will bind: the route entry exactly as the
// bridge's bindChannel would record it.
type fillTarget struct {
	channelID, name string
	entry           slackbridge.RouteEntry
}

// fillDeps are the collaborators `fill` needs. Queue is lazy so the queue URL
// lookup (and its AWS config) happens only when a backfill is actually queued.
type fillDeps struct {
	Store  slackbridge.HistoryStore
	Lister SlackRouteChannelLister
	Routes purgeRouteTable
	Queue  func(ctx context.Context) (slackbridge.BackfillEnqueuer, error)
}

type fillBackend func(ctx context.Context) (*fillDeps, error)

var fillChannelID = regexp.MustCompile(`^[CG][A-Z0-9]+$`)

// buildFillDeps wires production collaborators from operator credentials.
func buildFillDeps(ctx context.Context, cfg *config.Config, sharedDeps *SlackCmdDeps) (*fillDeps, error) {
	deps := sharedDeps
	if deps == nil {
		var err error
		if deps, err = buildSlackCmdDeps(cfg); err != nil {
			return nil, err
		}
	}
	if deps.DDB == nil || deps.SSM == nil {
		return nil, fmt.Errorf("km slack history fill: no DynamoDB/SSM client available")
	}
	lister, err := newSlackRouteLister(ctx, cfg, deps.SSM)
	if err != nil {
		return nil, err
	}
	queueName := cfg.GetResourcePrefix() + "-slack-history-backfill"
	return &fillDeps{
		Store:  &slackbridge.DDBHistoryStore{Client: deps.DDB, TableName: cfg.GetResourcePrefix() + "-slack-history"},
		Lister: lister,
		Routes: readLiveRoutesDocument(deps.SSM, slackRoutesParam(cfg)),
		Queue: func(ctx context.Context) (slackbridge.BackfillEnqueuer, error) {
			region := cfg.PrimaryRegion
			if region == "" {
				region = "us-east-1"
			}
			awsCfg, err := kmaws.LoadAWSConfigInRegion(ctx, cfg.AWSProfile, region)
			if err != nil {
				return nil, fmt.Errorf("load AWS config: %w", err)
			}
			client := sqs.NewFromConfig(awsCfg)
			u, err := client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(queueName)})
			if err != nil {
				return nil, fmt.Errorf("find queue %s (run km init --dry-run=false first): %w", queueName, err)
			}
			return &slackbridge.SQSBackfillQueue{Client: client, QueueURL: aws.ToString(u.QueueUrl)}, nil
		},
	}, nil
}

func newSlackHistoryFillCmd(backend fillBackend, out io.Writer) *cobra.Command {
	var alias string
	var purgeFirst, yes bool
	fill := &cobra.Command{
		Use:   "fill (<#name>|<C…>|--alias <alias>) [--purge-first] [--yes]",
		Short: "Bind a channel the bot is already in and queue its history backfill",
		Long: `Forces what the bridge does on member_joined_channel: writes the channel's meta row and
queues one backfill job on {prefix}-slack-history-backfill. Use it for a channel the bot sat in
before its route existed, a quiet channel the bridge has not seen a message in, or to reload
after a purge (--purge-first deletes the channel's rows and meta first). The channel must
resolve to a route with history.mirror; fill never widens policy. It never posts the
announcement: the row is written announced. A channel that already has a meta row is refused
unless --purge-first is given. Operator credentials only.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			if (arg == "") == (alias == "") {
				return fmt.Errorf("km slack history fill: give exactly one of <#name|C…> or --alias")
			}
			d, err := backend(ctx)
			if err != nil {
				return err
			}
			targets, err := resolveFillTargets(ctx, d.Lister, d.Routes, arg, alias, out)
			if err != nil {
				return err
			}
			if !yes {
				if !term.IsTerminal(int(os.Stdin.Fd())) {
					return fmt.Errorf("km slack history fill: %d channel(s) would be bound; pass --yes to confirm non-interactively", len(targets))
				}
				fmt.Fprintf(os.Stderr, "Bind and backfill %d channel(s)? [y/N] ", len(targets))
				var ans string
				fmt.Fscanln(os.Stdin, &ans)
				if !strings.EqualFold(ans, "y") {
					return fmt.Errorf("aborted")
				}
			}
			var queue slackbridge.BackfillEnqueuer
			if d.Queue != nil {
				if queue, err = d.Queue(ctx); err != nil {
					return fmt.Errorf("km slack history fill: %w", err)
				}
			}
			return runSlackHistoryFill(ctx, d.Store, queue, targets, purgeFirst, time.Now(), out)
		},
	}
	fill.Flags().StringVar(&alias, "alias", "", "Fill every bot channel routed to this alias with history.mirror")
	fill.Flags().BoolVar(&purgeFirst, "purge-first", false, "Delete the channel's mirrored rows and meta before binding")
	fill.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation")
	return fill
}

// resolveFillTargets maps the CLI target to channels with their route entry.
// Resolution per channel matches the bridge: a static entry beats a glob, the
// first glob (config order) matching the channel's name wins. A channel with
// no route, or one without history.mirror, is an error for an explicit target
// and (the latter with a warning line) skipped for --alias.
func resolveFillTargets(ctx context.Context, lister SlackRouteChannelLister, routes purgeRouteTable, arg, alias string, warn io.Writer) ([]fillTarget, error) {
	if (arg == "") == (alias == "") {
		return nil, fmt.Errorf("km slack history fill: give exactly one of <#name|C…> or --alias")
	}
	if routes == nil {
		return nil, fmt.Errorf("km slack history fill: no channel route table is readable; configure slack.routes and run km init --slack")
	}
	doc, err := routes(ctx)
	if err != nil {
		return nil, fmt.Errorf("km slack history fill: %w", err)
	}
	if doc == nil {
		return nil, fmt.Errorf("km slack history fill: no channel route table is published; configure slack.routes and run km init --slack")
	}
	if lister == nil {
		return nil, fmt.Errorf("km slack history fill: the Slack bot token is needed to list the bot's channels")
	}
	chans, err := lister.ListBotChannels(ctx)
	if err != nil {
		return nil, err
	}
	quiet := func(string, ...any) {}
	table := slackbridge.BuildRouteTable(doc, quiet)
	patterns := slackbridge.BuildPatternTable(doc, quiet)
	// entryFor is the route a bind would record, nil when the channel has none.
	entryFor := func(id, name string) *slackbridge.RouteEntry {
		if table[id] != nil {
			for _, e := range doc.Routes {
				if e.Channel == id {
					e.Name = name
					return &e
				}
			}
		}
		for _, p := range patterns {
			if slackbridge.MatchChannelGlob(p.Glob, name) {
				e := slackbridge.RouteFromPattern(p, id, name)
				return &e
			}
		}
		return nil
	}
	mirrors := func(e *slackbridge.RouteEntry) bool { return e.History != nil && e.History.Mirror }

	var out []fillTarget
	if alias != "" {
		for _, c := range chans {
			e := entryFor(c.ID, c.Name)
			if e == nil || e.Alias != alias {
				continue
			}
			if !mirrors(e) {
				fmt.Fprintf(warn, "skipping #%s (%s): routed to %s without history.mirror\n", c.Name, c.ID, e.Alias)
				continue
			}
			out = append(out, fillTarget{channelID: c.ID, name: c.Name, entry: *e})
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("km slack history fill: no channel the bot is in resolves to alias %q with history.mirror", alias)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].channelID < out[j].channelID })
		return out, nil
	}

	var id, name string
	switch {
	case strings.HasPrefix(arg, "#"):
		for _, c := range chans {
			if c.Name == strings.TrimPrefix(arg, "#") {
				id, name = c.ID, c.Name
			}
		}
		if id == "" {
			return nil, fmt.Errorf("km slack history fill: no channel named %s among the bot's channels: %s", arg, botChannelNames(chans))
		}
	case fillChannelID.MatchString(arg):
		for _, c := range chans {
			if c.ID == arg {
				id, name = c.ID, c.Name
			}
		}
		if id == "" {
			return nil, fmt.Errorf("km slack history fill: the bot is not in channel %s; invite it first", arg)
		}
	default:
		return nil, fmt.Errorf("km slack history fill: %q is neither a #name nor a channel id", arg)
	}
	e := entryFor(id, name)
	if e == nil {
		return nil, fmt.Errorf("km slack history fill: channel #%s (%s) has no route; add it under slack.routes and run km init --slack", name, id)
	}
	if !mirrors(e) {
		return nil, fmt.Errorf("km slack history fill: channel #%s is routed to %s without history.mirror — add mirror: true and km init --slack", name, e.Alias)
	}
	return []fillTarget{{channelID: id, name: name, entry: *e}}, nil
}

// runSlackHistoryFill binds each target as the bridge's bindChannel would and
// queues one backfill job per pending channel. The row is written Announced:
// fill never posts the announcement, and marking it announced keeps the bridge
// from posting one later. Without purgeFirst every target is checked for an
// existing meta row BEFORE anything is written, so a refusal leaves no partial
// state.
func runSlackHistoryFill(ctx context.Context, store slackbridge.HistoryStore, queue slackbridge.BackfillEnqueuer, targets []fillTarget, purgeFirst bool, now time.Time, out io.Writer) error {
	if !purgeFirst {
		for _, t := range targets {
			m, err := store.GetMeta(ctx, t.channelID)
			if err != nil {
				return fmt.Errorf("km slack history fill: #%s (%s): read meta: %w", t.name, t.channelID, err)
			}
			if m != nil {
				return fmt.Errorf("km slack history fill: #%s (%s) already bound (backfill_state=%s); use --purge-first to clear and reload", t.name, t.channelID, m.BackfillState)
			}
		}
	}
	for _, t := range targets {
		if purgeFirst {
			n, err := store.PurgeChannel(ctx, t.channelID)
			if err != nil {
				return fmt.Errorf("km slack history fill: #%s (%s): purge: %w", t.name, t.channelID, err)
			}
			fmt.Fprintf(out, "#%s (%s): %d row(s) purged\n", t.name, t.channelID, n)
		}
		meta := slackbridge.NewBoundChannelMeta(t.channelID, t.name, t.entry, "", now)
		meta.Announced = true
		if err := store.PutMeta(ctx, meta); err != nil {
			return fmt.Errorf("km slack history fill: #%s (%s): write meta: %w", t.name, t.channelID, err)
		}
		if meta.BackfillState != "pending" {
			fmt.Fprintf(out, "#%s (%s, %s): bound, no backfill (route has backfill 0)\n", t.name, t.channelID, t.entry.Alias)
			continue
		}
		if queue == nil {
			return fmt.Errorf("km slack history fill: #%s (%s): bound, but no backfill queue is available", t.name, t.channelID)
		}
		job := slackbridge.NewBackfillJob(meta)
		if err := queue.Enqueue(ctx, job, 0); err != nil {
			return fmt.Errorf("km slack history fill: #%s (%s): bound, but the backfill enqueue failed: %w", t.name, t.channelID, err)
		}
		fmt.Fprintf(out, "#%s (%s, %s): bound, backfill queued (back %s, ≤%d msgs)\n", t.name, t.channelID, t.entry.Alias,
			slackbridge.HumanizeSeconds(t.entry.History.BackfillSeconds), job.Remaining)
	}
	return nil
}
