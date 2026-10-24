package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	slackbridge "github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

type historyPurgeTarget struct {
	channelID, name, alias string
	all                    bool
}

// purgeBackend supplies the store and (only when a #name must be resolved) the
// channel lister. Production builds both from operator credentials; tests
// inject fakes.
type purgeBackend func(ctx context.Context, needLister bool) (slackbridge.HistoryStore, SlackRouteChannelLister, error)

// newSlackHistoryCmd is `km slack history`. Operator-only: it talks to DynamoDB
// with the operator policy; there is deliberately no bridge action and no
// km-slack form, so a sandbox can never purge (spec §6.4).
func newSlackHistoryCmd(cfg *config.Config, sharedDeps *SlackCmdDeps) *cobra.Command {
	hist := &cobra.Command{Use: "history", Short: "Manage the Slack channel history mirror"}
	backend := func(ctx context.Context, needLister bool) (slackbridge.HistoryStore, SlackRouteChannelLister, error) {
		deps := sharedDeps
		if deps == nil {
			var err error
			if deps, err = buildSlackCmdDeps(cfg); err != nil {
				return nil, nil, err
			}
		}
		if deps.DDB == nil {
			return nil, nil, fmt.Errorf("km slack history purge: no DynamoDB client available")
		}
		store := &slackbridge.DDBHistoryStore{Client: deps.DDB, TableName: cfg.GetResourcePrefix() + "-slack-history"}
		var lister SlackRouteChannelLister
		if needLister {
			l, err := newSlackRouteLister(ctx, cfg, deps.SSM)
			if err != nil {
				return nil, nil, err
			}
			lister = l
		}
		return store, lister, nil
	}
	hist.AddCommand(newSlackHistoryPurgeCmd(backend, os.Stdout))
	return hist
}

func newSlackHistoryPurgeCmdWithStore(store slackbridge.HistoryStore, lister SlackRouteChannelLister, out io.Writer) *cobra.Command {
	return newSlackHistoryPurgeCmd(func(context.Context, bool) (slackbridge.HistoryStore, SlackRouteChannelLister, error) {
		return store, lister, nil
	}, out)
}

func newSlackHistoryPurgeCmd(backend purgeBackend, out io.Writer) *cobra.Command {
	var alias string
	var all, keepMeta, yes bool
	purge := &cobra.Command{
		Use:   "purge [<#name>|<C…>]",
		Short: "Delete a channel's mirrored history (rows and, unless --keep-meta, its route binding)",
		Long: `Removes everything the bridge mirrored for a channel from {prefix}-slack-history.
Target one channel by name or id, every channel bound to an alias (--alias), or every
mirrored channel (--all). --keep-meta leaves the route binding so live mirroring continues
from now with no new backfill; without it the channel is re-matched (and re-backfilled,
bounded by the route) only on the bot's next join. Operator credentials only.`,
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
			store, lister, err := backend(ctx, strings.HasPrefix(arg, "#"))
			if err != nil {
				return err
			}
			targets, err := resolvePurgeTargets(ctx, store, lister, arg, alias, all)
			if err != nil {
				return err
			}
			if !yes {
				if !term.IsTerminal(int(os.Stdin.Fd())) { // not isTerminal: it is true for /dev/null
					return fmt.Errorf("km slack history purge: %d channel(s) would be purged; pass --yes to confirm non-interactively", len(targets))
				}
				fmt.Fprintf(os.Stderr, "Purge mirrored history for %d channel(s)? [y/N] ", len(targets))
				var ans string
				fmt.Fscanln(os.Stdin, &ans)
				if !strings.EqualFold(ans, "y") {
					return fmt.Errorf("aborted")
				}
			}
			return runSlackHistoryPurge(ctx, store, targets, keepMeta, out)
		},
	}
	purge.Flags().StringVar(&alias, "alias", "", "Purge every mirrored channel bound to this alias")
	purge.Flags().BoolVar(&all, "all", false, "Purge every mirrored channel")
	purge.Flags().BoolVar(&keepMeta, "keep-meta", false, "Keep the route binding; mirroring continues live, no new backfill")
	purge.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation")
	return purge
}

func resolvePurgeTargets(ctx context.Context, store slackbridge.HistoryStore, lister SlackRouteChannelLister, arg, alias string, all bool) ([]historyPurgeTarget, error) {
	given := 0
	for _, b := range []bool{arg != "", alias != "", all} {
		if b {
			given++
		}
	}
	if given != 1 {
		return nil, fmt.Errorf("km slack history purge: give exactly one of <#name|C…>, --alias, or --all")
	}
	metas, err := store.ListMeta(ctx)
	if err != nil {
		return nil, fmt.Errorf("km slack history purge: list mirrored channels: %w", err)
	}
	var out []historyPurgeTarget
	add := func(m slackbridge.ChannelMeta) {
		out = append(out, historyPurgeTarget{channelID: m.Channel, name: m.Name, alias: m.Route.Alias})
	}
	switch {
	case all:
		for _, m := range metas {
			add(m)
		}
	case alias != "":
		for _, m := range metas {
			if m.Route.Alias == alias {
				add(m)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("km slack history purge: no mirrored channel is bound to alias %q", alias)
		}
	default:
		id := arg
		if strings.HasPrefix(arg, "#") {
			if lister == nil {
				return nil, fmt.Errorf("km slack history purge: a #name needs the Slack bot token to resolve")
			}
			chans, err := lister.ListBotChannels(ctx)
			if err != nil {
				return nil, err
			}
			id = ""
			for _, c := range chans {
				if c.Name == strings.TrimPrefix(arg, "#") {
					id = c.ID
				}
			}
			if id == "" {
				return nil, fmt.Errorf("km slack history purge: no channel named %s among the bot's channels: %s", arg, botChannelNames(chans))
			}
		}
		for _, m := range metas {
			if m.Channel == id {
				add(m)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("km slack history purge: channel %s is not mirrored (no meta row)", id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].channelID < out[j].channelID })
	return out, nil
}

func runSlackHistoryPurge(ctx context.Context, store slackbridge.HistoryStore, targets []historyPurgeTarget, keepMeta bool, out io.Writer) error {
	total := 0
	for _, t := range targets {
		var kept *slackbridge.ChannelMeta
		if keepMeta {
			var err error
			if kept, err = store.GetMeta(ctx, t.channelID); err != nil {
				return fmt.Errorf("km slack history purge: #%s (%s): read meta: %w", t.name, t.channelID, err)
			}
		}
		n, err := store.PurgeChannel(ctx, t.channelID)
		if err != nil {
			return fmt.Errorf("km slack history purge: #%s (%s): %w", t.name, t.channelID, err)
		}
		total += n
		state := "meta removed"
		if kept != nil {
			kept.BackfillState, kept.BackfillWritten, kept.BackfillCursor = "off", 0, ""
			if err := store.PutMeta(ctx, *kept); err != nil {
				return fmt.Errorf("km slack history purge: #%s: rewrite meta: %w", t.name, err)
			}
			state = "meta kept (mirroring continues live, no backfill)"
		}
		fmt.Fprintf(out, "#%s (%s, alias %s): %d row(s) purged, %s\n", t.name, t.channelID, t.alias, n, state)
	}
	fmt.Fprintf(out, "%d channel(s), %d row(s) purged\n", len(targets), total)
	return nil
}
