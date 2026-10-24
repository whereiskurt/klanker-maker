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
	noMeta                 bool // a static mirror channel the bridge has not bound yet
}

// purgeRouteTable reads the live channel route table (/{prefix}/slack/routes),
// nil when none is published. A channel a static route mirrors may have rows
// but no meta row (the bridge binds it on first sight), so purge consults it.
type purgeRouteTable func(ctx context.Context) (*slackbridge.RoutesDocument, error)

// purgeBackend supplies the store, (only when a #name must be resolved) the
// channel lister, and the live route table reader. Production builds them from
// operator credentials; tests inject fakes.
type purgeBackend func(ctx context.Context, needLister bool) (slackbridge.HistoryStore, SlackRouteChannelLister, purgeRouteTable, error)

// staticMirror is a channel the live route table mirrors (a named route or a
// glob match recorded at km init).
type staticMirror struct{ channel, name, alias string }

// staticMirrorChannels is every channel the table routes with mirror: true.
func staticMirrorChannels(doc *slackbridge.RoutesDocument) map[string]staticMirror {
	out := map[string]staticMirror{}
	if doc == nil {
		return out
	}
	names := map[string]string{}
	for _, r := range doc.Routes {
		if _, ok := names[r.Channel]; !ok {
			names[r.Channel] = r.Name
		}
	}
	for _, p := range doc.Patterns {
		for _, ch := range p.Channels {
			if _, ok := names[ch.ID]; !ok {
				names[ch.ID] = ch.Name
			}
		}
	}
	for id, r := range slackbridge.BuildRouteTable(doc, func(string, ...any) {}) {
		if r.History != nil && r.History.Mirror {
			out[id] = staticMirror{channel: id, name: names[id], alias: r.Alias}
		}
	}
	return out
}

// readLiveRoutesDocument is the production purgeRouteTable.
func readLiveRoutesDocument(store SSMParamStore, param string) purgeRouteTable {
	return func(ctx context.Context) (*slackbridge.RoutesDocument, error) {
		val, err := store.Get(ctx, param, false)
		if err != nil {
			return nil, fmt.Errorf("read the channel route table %s: %w", param, err)
		}
		if val == "" {
			return nil, nil
		}
		doc, err := slackbridge.ParseRoutesDocument([]byte(val))
		if err != nil {
			return nil, fmt.Errorf("channel route table %s: %w", param, err)
		}
		return doc, nil
	}
}

// newSlackHistoryCmd is `km slack history`. Operator-only: it talks to DynamoDB
// with the operator policy; there is deliberately no bridge action and no
// km-slack form, so a sandbox can never purge (spec §6.4).
func newSlackHistoryCmd(cfg *config.Config, sharedDeps *SlackCmdDeps) *cobra.Command {
	hist := &cobra.Command{Use: "history", Short: "Manage the Slack channel history mirror"}
	backend := func(ctx context.Context, needLister bool) (slackbridge.HistoryStore, SlackRouteChannelLister, purgeRouteTable, error) {
		deps := sharedDeps
		if deps == nil {
			var err error
			if deps, err = buildSlackCmdDeps(cfg); err != nil {
				return nil, nil, nil, err
			}
		}
		if deps.DDB == nil {
			return nil, nil, nil, fmt.Errorf("km slack history purge: no DynamoDB client available")
		}
		store := &slackbridge.DDBHistoryStore{Client: deps.DDB, TableName: cfg.GetResourcePrefix() + "-slack-history"}
		var lister SlackRouteChannelLister
		if needLister {
			l, err := newSlackRouteLister(ctx, cfg, deps.SSM)
			if err != nil {
				return nil, nil, nil, err
			}
			lister = l
		}
		var routes purgeRouteTable
		if deps.SSM != nil {
			routes = readLiveRoutesDocument(deps.SSM, slackRoutesParam(cfg))
		}
		return store, lister, routes, nil
	}
	hist.AddCommand(newSlackHistoryPurgeCmd(backend, os.Stdout))
	hist.AddCommand(newSlackHistoryFillCmd(func(ctx context.Context) (*fillDeps, error) {
		return buildFillDeps(ctx, cfg, sharedDeps)
	}, os.Stdout))
	return hist
}

func newSlackHistoryPurgeCmdWithStore(store slackbridge.HistoryStore, lister SlackRouteChannelLister, routes purgeRouteTable, out io.Writer) *cobra.Command {
	return newSlackHistoryPurgeCmd(func(context.Context, bool) (slackbridge.HistoryStore, SlackRouteChannelLister, purgeRouteTable, error) {
		return store, lister, routes, nil
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
mirrored channel (--all). A channel the live route table mirrors but the bridge has not
bound yet (no meta row) is included and reported as "(no meta row)". --keep-meta leaves the
route binding so live mirroring continues from now with no new backfill; without it a glob
channel is re-matched (and re-backfilled, bounded by the route) only on the bot's next join,
and a statically routed channel on the next message the bridge sees there. Operator
credentials only.`,
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
			store, lister, routes, err := backend(ctx, strings.HasPrefix(arg, "#"))
			if err != nil {
				return err
			}
			targets, err := resolvePurgeTargets(ctx, store, lister, routes, arg, alias, all)
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

func resolvePurgeTargets(ctx context.Context, store slackbridge.HistoryStore, lister SlackRouteChannelLister, routes purgeRouteTable, arg, alias string, all bool) ([]historyPurgeTarget, error) {
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
	hasMeta := map[string]bool{}
	for _, m := range metas {
		hasMeta[m.Channel] = true
	}
	add := func(m slackbridge.ChannelMeta) {
		out = append(out, historyPurgeTarget{channelID: m.Channel, name: m.Name, alias: m.Route.Alias})
	}
	// statics are the live table's mirror channels with no meta row yet.
	statics := func() (map[string]staticMirror, error) {
		if routes == nil {
			return nil, nil
		}
		doc, err := routes(ctx)
		if err != nil {
			return nil, fmt.Errorf("km slack history purge: %w", err)
		}
		found := staticMirrorChannels(doc)
		for id := range found {
			if hasMeta[id] {
				delete(found, id)
			}
		}
		return found, nil
	}
	addStatic := func(sm staticMirror) {
		out = append(out, historyPurgeTarget{channelID: sm.channel, name: sm.name, alias: sm.alias, noMeta: true})
	}
	switch {
	case all:
		for _, m := range metas {
			add(m)
		}
		st, err := statics()
		if err != nil {
			return nil, err
		}
		for _, sm := range st {
			addStatic(sm)
		}
	case alias != "":
		for _, m := range metas {
			if m.Route.Alias == alias {
				add(m)
			}
		}
		st, err := statics()
		if err != nil {
			return nil, err
		}
		for _, sm := range st {
			if sm.alias == alias {
				addStatic(sm)
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
			st, err := statics()
			if err != nil {
				return nil, err
			}
			if sm, ok := st[id]; ok {
				if sm.name == "" && strings.HasPrefix(arg, "#") {
					sm.name = strings.TrimPrefix(arg, "#")
				}
				addStatic(sm)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("km slack history purge: channel %s is not mirrored (no meta row, and the live route table does not mirror it)", id)
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
		if t.noMeta {
			state = "(no meta row)"
		}
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
