package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/localnumber"
)

// NewListCmd creates the "km list" subcommand.
// Usage: km list [--json] [--tags]
//
// Scans S3 (default) or AWS resource tags for running sandboxes and prints
// a table of sandbox ID, profile, substrate, region, status, and TTL remaining.
func NewListCmd(cfg *config.Config) *cobra.Command {
	return NewListCmdWithLister(cfg, nil)
}

// NewListCmdWithLister builds the list command with an optional custom lister.
// If lister is nil, the real AWS-backed lister is used. This overload is used
// in tests to inject fake lister implementations.
func NewListCmdWithLister(cfg *config.Config, lister SandboxLister) *cobra.Command {
	return NewListCmdWithCheckers(cfg, lister, nil)
}

// NewListCmdWithCheckers builds the list command with an optional lister AND an
// optional AgentAuthChecker. Pass nil for real AWS-backed clients. This is the
// widest overload; all narrower constructors delegate to it.
func NewListCmdWithCheckers(cfg *config.Config, lister SandboxLister, checker AgentAuthChecker) *cobra.Command {
	var jsonOutput bool
	var useTagScan bool
	var wide bool
	var reset bool
	var auth bool

	cmd := &cobra.Command{
		Use:          "list",
		Aliases:      []string{"ls"},
		Short:        "List all running sandboxes",
		Long:         helpText("list"),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if reset {
				return runListReset(cmd)
			}
			return runList(cmd, cfg, lister, checker, jsonOutput, useTagScan, wide, auth)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON array")
	cmd.Flags().BoolVar(&useTagScan, "tags", false, "Use AWS tag scan instead of S3 state scan")
	cmd.Flags().BoolVar(&wide, "wide", false, "Show all columns (profile, substrate, region)")
	cmd.Flags().BoolVar(&reset, "reset", false, "Reset local sandbox numbering so the next created sandbox is #1")
	cmd.Flags().BoolVar(&auth, "auth", false, "Check agent (claude/codex) login state per running sandbox via SSM")
	return cmd
}

// runListReset sets the local-number counter back to 1 without touching the
// existing sandbox→number map. The next newly created sandbox will be assigned
// #1; if an existing sandbox already holds that number the display will show
// a collision until reconciliation rotates it out.
func runListReset(cmd *cobra.Command) error {
	state, err := localnumber.Load()
	if err != nil {
		return fmt.Errorf("load local numbers: %w", err)
	}
	if state == nil {
		state = &localnumber.State{Next: 1, Map: map[string]int{}}
	}
	state.Next = 1
	if err := localnumber.Save(state); err != nil {
		return fmt.Errorf("save local numbers: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Local sandbox counter reset; next created sandbox will be #1.")
	return nil
}

// SandboxLister abstracts the sandbox discovery mechanism for testability.
type SandboxLister interface {
	ListSandboxes(ctx context.Context, useTagScan bool) ([]kmaws.SandboxRecord, error)
}

// runList is the command RunE logic, accepting an explicit lister and checker for testability.
func runList(cmd *cobra.Command, cfg *config.Config, lister SandboxLister, checker AgentAuthChecker, jsonOutput, useTagScan, wide, auth bool) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	var awsCfg awssdk.Config
	var ec2Err error

	if lister == nil {
		awsProfile := "klanker-terraform"
		awsCfg, ec2Err = kmaws.LoadAWSConfig(ctx, awsProfile)
		if ec2Err != nil {
			return fmt.Errorf("load AWS config: %w", ec2Err)
		}
		lister = newRealLister(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())
	}

	records, err := lister.ListSandboxes(ctx, useTagScan)
	if err != nil {
		return fmt.Errorf("list sandboxes: %w", err)
	}

	// Banner: print version+timestamp header for all non-JSON output.
	// Emit BEFORE any tabular content so it's the first visible line.
	if !jsonOutput {
		n := len(records)
		noun := "sandboxes"
		if n == 1 {
			noun = "sandbox"
		}
		fprintBanner(cmd.OutOrStdout(), "km list", fmt.Sprintf("%d %s", n, noun))
	}

	if len(records) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No running sandboxes.")
		return nil
	}

	// Check live instance status for EC2 sandboxes to detect spot reclamation / termination.
	// Re-use awsCfg loaded above (real path); if lister was injected, try loading now for
	// EC2/DDB enrichment (best-effort — test injections set awsCfg to zero value).
	if ec2Err == nil && awsCfg.Region == "" {
		// lister was injected but we still need an awsCfg for enrichment; attempt load.
		awsCfg, ec2Err = kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	}

	if ec2Err == nil {
		// Status/hibernation reconcile (bidirectional — a box restarted after an
		// idle-stop, or a resume whose status write raced, leaves DDB "stopped"
		// while the instance is running), the idle countdown the default view's
		// SHUTDOWN column needs (TTL alone is misleading: a box with 19h of TTL
		// is routinely reaped in 2h by the idle timer), and --wide's thread
		// counts — all from one batched describe plus a bounded fan-out; see
		// list_enrich.go for why it is shaped that way.
		//
		// A cross-account sandbox's instance does NOT exist in the home account,
		// so describing it there returns nothing and the reconcile downgrades a
		// RUNNING GPU box to "killed" — the expensive direction to be wrong in.
		// Linked boxes are described where they live, one AssumeRole per link.
		linkClients := newLaunchAccountEC2Cache(cfg, awsCfg)
		enrichRecords(ctx, listEnrichDeps{
			ec2: ec2.NewFromConfig(awsCfg),
			linkEC2: func(ctx context.Context, link string) ec2DescribeInstancesAPI {
				if lc := linkClients.clientFor(ctx, link); lc != nil {
					return lc
				}
				return nil
			},
			cw:  cloudwatchlogs.NewFromConfig(awsCfg),
			ssm: ssm.NewFromConfig(awsCfg),
			ddb: dynamodb.NewFromConfig(awsCfg),
		}, records, cfg.GetResourcePrefix(), cfg.GetSlackThreadsTableName(), wide)
	}

	// Reconcile local sandbox numbers with live DynamoDB state.
	lnState, _ := localnumber.Load()
	if lnState == nil {
		lnState = &localnumber.State{Next: 1, Map: map[string]int{}}
	}
	liveIDs := make([]string, len(records))
	for i, r := range records {
		liveIDs[i] = r.SandboxID
	}
	localnumber.Reconcile(lnState, liveIDs)
	_ = localnumber.Save(lnState) // best-effort

	// Sort records by local number (ascending).
	numbers := lnState.Map
	sort.Slice(records, func(i, j int) bool {
		ni := numbers[records[i].SandboxID]
		nj := numbers[records[j].SandboxID]
		return ni < nj
	})

	if jsonOutput {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(records)
	}

	// Auth fan-out: only when --auth is set. Build a real checker from the loaded
	// awsCfg when none is injected (real path). Fan out concurrently over running
	// sandboxes with a bounded goroutine pool (semaphore of 8).
	var authResults map[string]string
	if auth {
		if checker == nil && ec2Err == nil {
			checker = &ssmAgentAuthChecker{
				ssmClient: ssm.NewFromConfig(awsCfg),
				ec2Client: ec2.NewFromConfig(awsCfg),
			}
		}
		if checker != nil {
			authResults = make(map[string]string, len(records))
			var mu sync.Mutex
			var wg sync.WaitGroup
			sem := make(chan struct{}, 8) // bounded concurrency

			for i := range records {
				if records[i].Status != "running" {
					continue
				}
				wg.Add(1)
				rec := &records[i]
				go func() {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()

					cl, cx, authErr := checker.CheckAuth(ctx, rec)
					var label string
					if authErr != nil {
						label = "?"
					} else {
						clS := "cl✗"
						if cl {
							clS = "cl✓"
						}
						cxS := "cx✗"
						if cx {
							cxS = "cx✓"
						}
						label = clS + " " + cxS
					}
					mu.Lock()
					authResults[rec.SandboxID] = label
					mu.Unlock()
				}()
			}
			wg.Wait()
		}
	}

	return printSandboxTable(cmd, records, wide, lnState.Map, authResults)
}

// awsSandboxLister is the real AWS-backed SandboxLister implementation.
type awsSandboxLister struct {
	s3Client     kmaws.S3ListAPI
	tagClient    kmaws.TagAPI
	dynamoClient kmaws.SandboxMetadataAPI
	bucket       string
	tableName    string
}

// newRealLister creates an awsSandboxLister from an AWS config.
func newRealLister(awsCfg awssdk.Config, bucket, tableName string) *awsSandboxLister {
	return &awsSandboxLister{
		s3Client:     s3.NewFromConfig(awsCfg),
		tagClient:    resourcegroupstaggingapi.NewFromConfig(awsCfg),
		dynamoClient: dynamodb.NewFromConfig(awsCfg),
		bucket:       bucket,
		tableName:    tableName,
	}
}

// ListSandboxes implements SandboxLister using real AWS clients.
// Primary: DynamoDB Scan (O(1) per page, no N GetObject calls).
// Fallback to S3 on ResourceNotFoundException (table not yet provisioned).
func (l *awsSandboxLister) ListSandboxes(ctx context.Context, useTagScan bool) ([]kmaws.SandboxRecord, error) {
	if useTagScan {
		return kmaws.ListAllSandboxesByTags(ctx, l.tagClient, l.bucket)
	}
	records, err := kmaws.ListAllSandboxesByDynamo(ctx, l.dynamoClient, l.tableName)
	if err != nil {
		var rnf *dynamodbtypes.ResourceNotFoundException
		if errors.As(err, &rnf) {
			// Table doesn't exist — fall back to S3
			if l.bucket == "" {
				return nil, fmt.Errorf("state bucket not configured: set KM_STATE_BUCKET or state_bucket in km-config.yaml")
			}
			return kmaws.ListAllSandboxesByS3(ctx, l.s3Client, l.bucket)
		}
		return nil, err
	}
	return records, nil
}

// printSandboxTable writes a human-readable tab-aligned table to cmd.OutOrStdout.
// Each row is numbered using persistent local numbers from the numbers map (falling back
// to positional i+1 if no local number is available). Pass nil for numbers to use positional.
// Status is color-coded: red for "failed", yellow for "partial"/"killed", green for "running".
// Locked sandboxes are shown in bold white with a lock icon.
// When wide=false, profile/substrate/region columns are hidden for a narrower display.
// authResults maps sandbox ID → compact auth string (e.g. "cl✓ cx✗"); nil means no AUTH column.
func printSandboxTable(cmd *cobra.Command, records []kmaws.SandboxRecord, wide bool, numbers map[string]int, authResults map[string]string) error {
	out := cmd.OutOrStdout()
	showAuth := authResults != nil
	// Use fixed-width printf instead of tabwriter to avoid ANSI color codes
	// breaking column alignment (tabwriter counts bytes, not visible chars).
	// Compute max sandbox ID width for dynamic column sizing
	idWidth := len("SANDBOX ID")
	for _, r := range records {
		if len(r.SandboxID) > idWidth {
			idWidth = len(r.SandboxID)
		}
	}
	idWidth += 2 // padding

	// Compute max alias width so the full alias is always visible.
	aliasWidth := len("ALIAS")
	for _, r := range records {
		if len(r.Alias) > aliasWidth {
			aliasWidth = len(r.Alias)
		}
	}
	aliasWidth += 2 // padding

	// Compute the # column width. Default min 2 (typical case); grow when the
	// persistent counter has climbed into 3+ digits so rows don't bleed into
	// the ALIAS column.
	numWidth := 2
	for i, r := range records {
		n := 0
		if numbers != nil {
			n = numbers[r.SandboxID]
		}
		if n == 0 {
			n = i + 1
		}
		if w := len(fmt.Sprintf("%d", n)); w > numWidth {
			numWidth = w
		}
	}

	// truncCol truncates a string to maxLen, adding ".." suffix if truncated.
	truncCol := func(s string, maxLen int) string {
		if len(s) <= maxLen {
			return s
		}
		if maxLen <= 2 {
			return s[:maxLen]
		}
		return s[:maxLen-2] + ".."
	}

	// Determine whether to show 💬 column: only when at least one sandbox has
	// inbound enabled (SlackInboundQueueURL set) to avoid an empty column.
	showThreads := false
	if wide {
		for _, r := range records {
			if r.SlackInboundQueueURL != "" {
				showThreads = true
				break
			}
		}
	}

	// Substrate and region columns use icon+word and AWS short codes. Pad by
	// visual width so emoji-bearing rows align with ASCII rows. Widths must
	// also fit the human-readable header strings.
	const regionColW = 7 // fits "REGION" header and "apse1" values

	if wide {
		if showThreads {
			if showAuth {
				fmt.Fprintf(out, "%-*s %-*s  %-*s %-16s %s %-10s %-6s %-6s %-7s %-5s %-9s\n",
					numWidth, "#", aliasWidth, "ALIAS", idWidth, "SANDBOX ID", "PROFILE",
					padVis("REGION", regionColW),
					"STATUS", "TTL", "IDLE", "UP", "💬", "AUTH")
			} else {
				fmt.Fprintf(out, "%-*s %-*s  %-*s %-16s %s %-10s %-6s %-6s %-7s %-5s\n",
					numWidth, "#", aliasWidth, "ALIAS", idWidth, "SANDBOX ID", "PROFILE",
					padVis("REGION", regionColW),
					"STATUS", "TTL", "IDLE", "UP", "💬")
			}
		} else {
			if showAuth {
				fmt.Fprintf(out, "%-*s %-*s  %-*s %-16s %s %-10s %-6s %-6s %-7s %-9s\n",
					numWidth, "#", aliasWidth, "ALIAS", idWidth, "SANDBOX ID", "PROFILE",
					padVis("REGION", regionColW),
					"STATUS", "TTL", "IDLE", "UP", "AUTH")
			} else {
				fmt.Fprintf(out, "%-*s %-*s  %-*s %-16s %s %-10s %-6s %-6s %-7s\n",
					numWidth, "#", aliasWidth, "ALIAS", idWidth, "SANDBOX ID", "PROFILE",
					padVis("REGION", regionColW),
					"STATUS", "TTL", "IDLE", "UP")
			}
		}
	} else {
		if showAuth {
			fmt.Fprintf(out, "%-*s %-*s  %-*s %-10s %-11s %-7s %s\n",
				numWidth, "#", aliasWidth, "ALIAS", idWidth, "SANDBOX ID", "STATUS", "SHUTDOWN", "UP", "AUTH")
		} else {
			fmt.Fprintf(out, "%-*s %-*s  %-*s %-10s %-11s %s\n",
				numWidth, "#", aliasWidth, "ALIAS", idWidth, "SANDBOX ID", "STATUS", "SHUTDOWN", "UP")
		}
	}
	for i, r := range records {
		ttl := r.TTLRemaining
		switch {
		case ttl == "":
			ttl = "-"
		case ttl == "expired":
			// computeTTLRemaining's "expired" is 7 chars in a %-6s column and
			// pushed IDLE/UP/💬 right on that one row. Display-only: --json
			// keeps "expired", and the narrow SHUTDOWN column (11 wide) is
			// untouched.
			ttl = "exp."
		case r.TTLExpiry != nil && time.Until(*r.TTLExpiry) >= 3*365*24*time.Hour:
			ttl = "∞" // same rung as compactDuration; --json keeps the numeric string
		}
		alias := r.Alias
		if alias == "" {
			alias = "-"
		}
		profile := truncCol(r.Profile, 16)
		// Pad status to fixed width BEFORE adding color codes. Visual-width
		// padding so emoji-prefixed labels align with ASCII rows.
		statusLabel := statusDisplay(r.Status)
		if wide && r.Hibernation {
			statusLabel += "(h)"
		}
		paddedStatus := padVis(statusLabel, 10)
		colorStatus := colorizeRaw(r.Status, false, paddedStatus)
		lock := ""
		if r.Locked {
			lock = " 🔒"
		}
		if r.ActionFrozen {
			lock += " 🧊FROZEN"
		}
		// Phase 121 — quota marker: surface that the sandbox has action limits
		// configured (independent of whether it is currently frozen).
		if r.ActionLimits != "" {
			lock += " ⚖Q"
		}
		bw := func(s string) string {
			if r.Locked {
				return ansiBoldWhite + s + ansiReset
			}
			return s
		}
		localNum := 0
		if numbers != nil {
			localNum = numbers[r.SandboxID]
		}
		if localNum == 0 {
			localNum = i + 1 // fallback to positional if no local number
		}
		num := bw(fmt.Sprintf("%-*d", numWidth, localNum))

		// UP column: uptime for running rows, "-" for all others. The --tags
		// (tag-scan) path leaves CreatedAt zero, which would otherwise render a
		// garbage "106751d23h"; guard on IsZero so those rows show "-".
		uptime := "-"
		if r.Status == "running" && !r.CreatedAt.IsZero() {
			uptime = formatUptime(r.CreatedAt)
		}

		// AUTH column: result from fan-out map, "-" if not present/running.
		authStr := "-"
		if showAuth {
			if v, ok := authResults[r.SandboxID]; ok {
				authStr = v
			}
		}

		if wide {
			idle := idleLabelForDisplay(r.IdleRemaining)
			region := padVis(shortRegion(r.Region), regionColW)
			if showThreads {
				threads := "-"
				if r.SlackChannelID != "" {
					threads = fmt.Sprintf("%d", r.ActiveThreads)
				}
				if showAuth {
					fmt.Fprintf(out, "%s %s  %s %s %s %s %-6s %-6s %-7s %-5s %-9s%s\n",
						num, bw(fmt.Sprintf("%-*s", aliasWidth, alias)), bw(fmt.Sprintf("%-*s", idWidth, r.SandboxID)),
						bw(fmt.Sprintf("%-16s", profile)),
						bw(region), colorStatus, bw(ttl), bw(idle),
						bw(uptime), bw(threads), bw(authStr), lock)
				} else {
					fmt.Fprintf(out, "%s %s  %s %s %s %s %-6s %-6s %-7s %-5s%s\n",
						num, bw(fmt.Sprintf("%-*s", aliasWidth, alias)), bw(fmt.Sprintf("%-*s", idWidth, r.SandboxID)),
						bw(fmt.Sprintf("%-16s", profile)),
						bw(region), colorStatus, bw(ttl), bw(idle),
						bw(uptime), bw(threads), lock)
				}
			} else {
				if showAuth {
					fmt.Fprintf(out, "%s %s  %s %s %s %s %-6s %-6s %-7s %-9s%s\n",
						num, bw(fmt.Sprintf("%-*s", aliasWidth, alias)), bw(fmt.Sprintf("%-*s", idWidth, r.SandboxID)),
						bw(fmt.Sprintf("%-16s", profile)),
						bw(region), colorStatus, bw(ttl), bw(idle),
						bw(uptime), bw(authStr), lock)
				} else {
					fmt.Fprintf(out, "%s %s  %s %s %s %s %-6s %-6s %-7s%s\n",
						num, bw(fmt.Sprintf("%-*s", aliasWidth, alias)), bw(fmt.Sprintf("%-*s", idWidth, r.SandboxID)),
						bw(fmt.Sprintf("%-16s", profile)),
						bw(region), colorStatus, bw(ttl), bw(idle),
						bw(uptime), lock)
				}
			}
		} else {
			if showAuth {
				// %-11s pads by rune, and "∞" is one rune one column wide, so the
				// SHUTDOWN cell needs no padVis (unlike the two-column STATUS emoji).
				fmt.Fprintf(out, "%s %s  %s %s %-11s %-7s %s%s\n",
					num, bw(fmt.Sprintf("%-*s", aliasWidth, alias)), bw(fmt.Sprintf("%-*s", idWidth, r.SandboxID)),
					colorStatus, bw(shutdownLabel(r)), bw(uptime), bw(authStr), lock)
			} else {
				fmt.Fprintf(out, "%s %s  %s %s %-11s %s%s\n",
					num, bw(fmt.Sprintf("%-*s", aliasWidth, alias)), bw(fmt.Sprintf("%-*s", idWidth, r.SandboxID)),
					colorStatus, bw(shutdownLabel(r)), bw(uptime), lock)
			}
		}
	}
	return nil
}

// statusDisplay returns an icon + short-word label for a sandbox status.
// Unknown statuses pass through unchanged so new states stay visible while
// being added to this mapping.
func statusDisplay(status string) string {
	switch status {
	case "running":
		return "🟢 run"
	case "starting":
		return "🟡 strt"
	case "failed":
		return "🔴 fail"
	case "nocap":
		return "🔴 nocap"
	case "paused":
		return "⏸  paus"
	case "stopped":
		return "⏹  stop"
	case "killed":
		return "☠️  kill"
	case "partial":
		return "⚠️  part"
	case "reaped":
		return "👻 reap"
	default:
		return status
	}
}

// substrateDisplay returns an icon + short-word label for a substrate kind.
// Unknown substrates pass through unchanged so downstream tooling stays
// debuggable when new kinds land.
func substrateDisplay(s string) string {
	switch s {
	case "ec2", "ec2demand":
		return "🖥️  ec2"
	case "ec2spot":
		return "⚡  spot"
	case "ecs":
		return "📦  ecs"
	case "docker":
		return "🐳  dock"
	case "k8s":
		return "☸️  k8s"
	default:
		return s
	}
}

// shortRegion abbreviates an AWS region code (e.g. "us-east-1" → "use1",
// "ap-southeast-2" → "apse2"). It takes the prefix as-is (us, ap, eu, ca,
// sa, me, af), replaces directional words with their initials (north→n,
// south→s, east→e, west→w, central→c, northeast→ne, etc.), and appends
// the trailing zone number. Regions that don't match the standard
// "<prefix>-<word>-<digit>" shape pass through unchanged.
func shortRegion(r string) string {
	parts := strings.Split(r, "-")
	if len(parts) < 3 {
		return r
	}
	abbrev := map[string]string{
		"north":     "n",
		"south":     "s",
		"east":      "e",
		"west":      "w",
		"central":   "c",
		"northeast": "ne",
		"southeast": "se",
		"northwest": "nw",
		"southwest": "sw",
	}
	var sb strings.Builder
	sb.WriteString(parts[0])
	for _, p := range parts[1 : len(parts)-1] {
		if a, ok := abbrev[p]; ok {
			sb.WriteString(a)
		} else if len(p) > 0 {
			sb.WriteString(p[:1])
		}
	}
	sb.WriteString(parts[len(parts)-1])
	return sb.String()
}

// visualWidth approximates the number of terminal columns a string occupies.
// It treats variation selectors as zero-width, common emoji ranges as 2 cols,
// and everything else as 1 col. Good enough for list-table alignment; not a
// full Unicode wcwidth.
func visualWidth(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case r == 0xFE0E || r == 0xFE0F:
			// variation selectors: no display width
		case r >= 0x1F000,
			r >= 0x2600 && r <= 0x27BF,
			r >= 0x2300 && r <= 0x23FF:
			w += 2
		default:
			w++
		}
	}
	return w
}

// padVis right-pads s with spaces until its visual width reaches n. If s is
// already wider, it is returned unchanged.
func padVis(s string, n int) string {
	vw := visualWidth(s)
	if vw >= n {
		return s
	}
	return s + strings.Repeat(" ", n-vw)
}

// colorizeListStatus returns the status string wrapped in ANSI color codes for display.
// "failed"  → red
// "partial" → yellow
// "killed"  → yellow (unexpected termination, needs attention)
// "reaped"  → yellow (spot instance reclaimed by AWS)
// "paused"  → magenta (hibernated or stopped, can resume)
// "stopped" → magenta (stopped, can resume)
// "running" → green
// others    → no color
func colorizeListStatus(status string) string {
	switch status {
	case "failed", "nocap":
		return ansiRed + status + ansiReset
	case "partial", "killed", "reaped", "starting":
		return ansiYellow + status + ansiReset
	case "paused", "stopped":
		return ansiMagenta + status + ansiReset
	case "running":
		return ansiGreen + status + ansiReset
	default:
		return status
	}
}

// colorizeRaw wraps a pre-padded display string with ANSI color based on the raw status value.
func colorizeRaw(status string, _ bool, display string) string {
	switch status {
	case "failed", "nocap":
		return ansiRed + display + ansiReset
	case "partial", "killed", "reaped", "starting":
		return ansiYellow + display + ansiReset
	case "paused", "stopped":
		return ansiMagenta + display + ansiReset
	case "running":
		return ansiGreen + display + ansiReset
	default:
		return display
	}
}

// reconcileSandboxStatus cross-checks the stored DDB status against the live EC2
// instances (found by the km:sandbox-id tag) and returns the status to display.
//
// It is BIDIRECTIONAL, unlike the old checkEC2InstanceStatus which callers only
// consulted when the DDB status was already "running". A box the DDB believes is
// "stopped" but which is actually running — an idle-stop followed by a restart,
// or a resume whose best-effort status write raced — now reconciles to "running"
// instead of looking terminated. On any AWS error the stored status is returned
// unchanged: a transient DescribeInstances blip must never mislabel a live box.
func reconcileSandboxStatus(ctx context.Context, client *ec2.Client, sandboxID, stored string) string {
	// "failed" is a create-time verdict, not an instance state — never override it.
	if stored == "failed" {
		return stored
	}
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: awssdk.String("tag:km:sandbox-id"), Values: []string{sandboxID}},
		},
	})
	if err != nil {
		return stored
	}
	var all []ec2types.Instance
	for _, res := range out.Reservations {
		all = append(all, res.Instances...)
	}
	return reconcileStatusFromInstances(stored, all)
}

// reconcileStatusFromInstances is the pure decision core of reconcileSandboxStatus,
// split out so the reconciliation rules are unit-tested without AWS. It selects the
// most-recently-launched non-terminated instance (so a fresh instance wins over a
// lingering terminated one after a replace) and maps its state to a display status,
// preserving "paused" (a hibernated instance reads as EC2 "stopped").
func reconcileStatusFromInstances(stored string, instances []ec2types.Instance) string {
	// "failed" is a create-time verdict, not an instance state — never override it.
	if stored == "failed" {
		return stored
	}
	var best *ec2types.Instance
	sawTerminated, sawSpotReaped := false, false
	for i := range instances {
		inst := &instances[i]
		if inst.State == nil {
			continue
		}
		switch inst.State.Name {
		case ec2types.InstanceStateNameTerminated, ec2types.InstanceStateNameShuttingDown:
			sawTerminated = true
			if inst.StateReason != nil && inst.StateReason.Code != nil &&
				*inst.StateReason.Code == "Server.SpotInstanceTermination" {
				sawSpotReaped = true
			}
			continue
		}
		if best == nil || (inst.LaunchTime != nil &&
			(best.LaunchTime == nil || inst.LaunchTime.After(*best.LaunchTime))) {
			best = inst
		}
	}

	if best == nil {
		// No live instance. A definitively terminated instance is authoritative;
		// an empty result only downgrades a "running" claim (an empty describe can
		// be eventual consistency, so a stopped/paused box keeps its stored label).
		switch {
		case sawSpotReaped:
			return "reaped"
		case sawTerminated:
			return "killed"
		case stored == "running":
			return "killed"
		default:
			return stored
		}
	}

	switch best.State.Name {
	case ec2types.InstanceStateNameRunning:
		return "running"
	case ec2types.InstanceStateNamePending:
		return "starting"
	case ec2types.InstanceStateNameStopped, ec2types.InstanceStateNameStopping:
		if stored == "paused" {
			return "paused" // hibernated instance reads as EC2 "stopped"
		}
		return "stopped"
	default:
		return string(best.State.Name)
	}
}

// shutdownLabel answers the question km list is actually asked: how long until
// this box goes away on its own.
//
// A sandbox dies at whichever comes first, TTL expiry or the idle reaper, so
// showing TTL alone is misleading rather than merely incomplete — a box with
// 19h of TTL left is routinely reaped in 2h by the idle timer. The label names
// which of the two is biting, because the operator's response differs: an idle
// reap is averted by using the box, a TTL expiry only by km extend.
//
// Idle applies to running sandboxes only. A stopped or paused box is not being
// idle-reaped (it is already stopped); only its TTL still runs.
func shutdownLabel(r kmaws.SandboxRecord) string {
	var (
		ttlLeft  time.Duration
		hasTTL   bool
		idleLeft time.Duration
		hasIdle  bool
	)

	if r.TTLExpiry != nil {
		ttlLeft = time.Until(*r.TTLExpiry)
		if ttlLeft <= 0 {
			return "expired"
		}
		hasTTL = true
	}

	if r.Status == "running" && r.IdleRemaining != "" {
		if r.IdleRemaining == "imminent" {
			return "imminent idle"
		}
		if d, err := time.ParseDuration(strings.TrimSuffix(r.IdleRemaining, " remaining")); err == nil {
			idleLeft, hasIdle = d, true
		}
	}

	switch {
	case hasIdle && (!hasTTL || idleLeft < ttlLeft):
		return compactDuration(idleLeft) + " idle"
	case hasTTL:
		return compactDuration(ttlLeft) + " ttl"
	default:
		return "-"
	}
}

// idleLabelForDisplay turns the stored IdleRemaining string — Go's
// Duration.String() plus " remaining", or "imminent" — into the same ladder
// the SHUTDOWN and TTL columns use. The stored form stays a parseable
// duration on purpose: shutdownLabel re-parses it and it reaches --json; only
// the human-facing cell changes. A large idleTimeout used to print as
// "86000h0m0s" here.
func idleLabelForDisplay(stored string) string {
	switch stored {
	case "":
		return "-"
	case "imminent":
		return stored
	}
	d, err := time.ParseDuration(strings.TrimSuffix(stored, " remaining"))
	if err != nil {
		return stored // not ours to guess at; show what we have
	}
	return compactDuration(d)
}

// compactDuration renders a duration at the precision the SHUTDOWN column can
// afford: "46m", "1h30m", "6d23h", "364d", "2y364d", "∞". The rule is that
// detail matters more the closer the deadline is: two adjacent units at most,
// and each rung drops its smaller unit once the value is comfortably past it —
// minutes go past a day, hours past a week, and past three years the number
// itself: that is a "never expire" ttl, and "∞" says so. (Human-facing only;
// --json keeps computeTTLRemaining's numeric string.) A "never
// expire" ttl of 86000h used to render as "85999h42m ttl", three characters
// wider than the column, and pushed UP and AUTH off their headers on every
// row. A year is 365 days here; this is a countdown, not a calendar. Rounded to the minute (a
// label computed from time.Until is a few hundred ms short of the whole minute
// it means), with a "<1m" floor so a box about to go is never shown as "1m".
func compactDuration(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	d = d.Round(time.Minute)
	m := int(d.Minutes())
	h := m / 60
	days := h / 24
	years := days / 365
	switch {
	case years >= 3:
		return "∞" // effectively never; nobody plans around "9y"
	case years > 0 && days%365 == 0:
		return fmt.Sprintf("%dy", years)
	case years > 0:
		return fmt.Sprintf("%dy%dd", years, days%365)
	case days >= 7:
		return fmt.Sprintf("%dd", days)
	case days > 0:
		if h%24 == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd%dh", days, h%24)
	case h > 0:
		if m%60 == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m%60)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	default:
		return "<1m"
	}
}
