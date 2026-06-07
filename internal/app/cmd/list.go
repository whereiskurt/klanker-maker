package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
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
		ec2Client := ec2.NewFromConfig(awsCfg)
		for i := range records {
			if strings.HasPrefix(records[i].Substrate, "ec2") {
				if records[i].Status == "running" {
					records[i].Status = checkEC2InstanceStatus(ctx, ec2Client, records[i].SandboxID)
				}
				records[i].Hibernation = checkEC2Hibernation(ctx, ec2Client, records[i].SandboxID)
			}
		}
	}

	// Compute idle remaining for running sandboxes (wide mode or JSON)
	if wide || jsonOutput {
		for i := range records {
			if records[i].Status == "running" && records[i].IdleTimeout != "" {
				remaining := computeIdleRemaining(ctx, records[i].SandboxID, records[i].IdleTimeout, records[i].CreatedAt, nil, cfg.GetResourcePrefix())
				if remaining >= 0 {
					records[i].IdleRemaining = formatIdleLabel(remaining, false)
				}
			}
		}
	}

	// Load active thread counts for --wide display (km-slack-threads, grouped by channel_id).
	// Only attempted when at least one sandbox has SlackInboundQueueURL set.
	if wide && ec2Err == nil {
		hasInbound := false
		for _, r := range records {
			if r.SlackInboundQueueURL != "" {
				hasInbound = true
				break
			}
		}
		if hasInbound {
			ddbClient := dynamodb.NewFromConfig(awsCfg)
			threadsTable := cfg.GetSlackThreadsTableName()
			for i := range records {
				if records[i].SlackChannelID == "" {
					continue
				}
				count, countErr := countActiveThreads(ctx, ddbClient, threadsTable, records[i].SlackChannelID)
				if countErr == nil {
					records[i].ActiveThreads = count
				}
			}
		}
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
	const substrateColW = 10 // fits "SUBSTRATE" header and "🐳  dock" values
	const regionColW = 7     // fits "REGION" header and "apse1" values

	if wide {
		if showThreads {
			if showAuth {
				fmt.Fprintf(out, "%-*s %-*s  %-*s %-16s %s %s %-10s %-6s %-6s %-7s %-5s %-9s %s\n",
					numWidth, "#", aliasWidth, "ALIAS", idWidth, "SANDBOX ID", "PROFILE",
					padVis("SUBSTRATE", substrateColW), padVis("REGION", regionColW),
					"STATUS", "TTL", "IDLE", "UP", "💬", "AUTH", "CLONED FROM")
			} else {
				fmt.Fprintf(out, "%-*s %-*s  %-*s %-16s %s %s %-10s %-6s %-6s %-7s %-5s %s\n",
					numWidth, "#", aliasWidth, "ALIAS", idWidth, "SANDBOX ID", "PROFILE",
					padVis("SUBSTRATE", substrateColW), padVis("REGION", regionColW),
					"STATUS", "TTL", "IDLE", "UP", "💬", "CLONED FROM")
			}
		} else {
			if showAuth {
				fmt.Fprintf(out, "%-*s %-*s  %-*s %-16s %s %s %-10s %-6s %-6s %-7s %-9s %s\n",
					numWidth, "#", aliasWidth, "ALIAS", idWidth, "SANDBOX ID", "PROFILE",
					padVis("SUBSTRATE", substrateColW), padVis("REGION", regionColW),
					"STATUS", "TTL", "IDLE", "UP", "AUTH", "CLONED FROM")
			} else {
				fmt.Fprintf(out, "%-*s %-*s  %-*s %-16s %s %s %-10s %-6s %-6s %-7s %s\n",
					numWidth, "#", aliasWidth, "ALIAS", idWidth, "SANDBOX ID", "PROFILE",
					padVis("SUBSTRATE", substrateColW), padVis("REGION", regionColW),
					"STATUS", "TTL", "IDLE", "UP", "CLONED FROM")
			}
		}
	} else {
		if showAuth {
			fmt.Fprintf(out, "%-*s %-*s  %-*s %-10s %-8s %-7s %s\n",
				numWidth, "#", aliasWidth, "ALIAS", idWidth, "SANDBOX ID", "STATUS", "TTL", "UP", "AUTH")
		} else {
			fmt.Fprintf(out, "%-*s %-*s  %-*s %-10s %-8s %s\n",
				numWidth, "#", aliasWidth, "ALIAS", idWidth, "SANDBOX ID", "STATUS", "TTL", "UP")
		}
	}
	for i, r := range records {
		ttl := r.TTLRemaining
		if ttl == "" {
			ttl = "-"
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

		// UP column: uptime for running rows, "-" for all others.
		uptime := "-"
		if r.Status == "running" {
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
			idle := r.IdleRemaining
			if idle == "" {
				idle = "-"
			}
			// Strip " remaining" suffix for compact display
			idle = strings.TrimSuffix(idle, " remaining")
			clonedFrom := r.ClonedFrom
			if clonedFrom == "" {
				clonedFrom = "-"
			}
			substrate := padVis(substrateDisplay(r.Substrate), substrateColW)
			region := padVis(shortRegion(r.Region), regionColW)
			if showThreads {
				threads := "-"
				if r.SlackChannelID != "" {
					threads = fmt.Sprintf("%d", r.ActiveThreads)
				}
				if showAuth {
					fmt.Fprintf(out, "%s %s  %s %s %s %s %s %-6s %-6s %-7s %-5s %-9s %s%s\n",
						num, bw(fmt.Sprintf("%-*s", aliasWidth, alias)), bw(fmt.Sprintf("%-*s", idWidth, r.SandboxID)),
						bw(fmt.Sprintf("%-16s", profile)), bw(substrate),
						bw(region), colorStatus, bw(ttl), bw(idle),
						bw(uptime), bw(threads), bw(authStr), bw(truncCol(clonedFrom, 14)), lock)
				} else {
					fmt.Fprintf(out, "%s %s  %s %s %s %s %s %-6s %-6s %-7s %-5s %s%s\n",
						num, bw(fmt.Sprintf("%-*s", aliasWidth, alias)), bw(fmt.Sprintf("%-*s", idWidth, r.SandboxID)),
						bw(fmt.Sprintf("%-16s", profile)), bw(substrate),
						bw(region), colorStatus, bw(ttl), bw(idle),
						bw(uptime), bw(threads), bw(truncCol(clonedFrom, 14)), lock)
				}
			} else {
				if showAuth {
					fmt.Fprintf(out, "%s %s  %s %s %s %s %s %-6s %-6s %-7s %-9s %s%s\n",
						num, bw(fmt.Sprintf("%-*s", aliasWidth, alias)), bw(fmt.Sprintf("%-*s", idWidth, r.SandboxID)),
						bw(fmt.Sprintf("%-16s", profile)), bw(substrate),
						bw(region), colorStatus, bw(ttl), bw(idle),
						bw(uptime), bw(authStr), bw(truncCol(clonedFrom, 14)), lock)
				} else {
					fmt.Fprintf(out, "%s %s  %s %s %s %s %s %-6s %-6s %-7s %s%s\n",
						num, bw(fmt.Sprintf("%-*s", aliasWidth, alias)), bw(fmt.Sprintf("%-*s", idWidth, r.SandboxID)),
						bw(fmt.Sprintf("%-16s", profile)), bw(substrate),
						bw(region), colorStatus, bw(ttl), bw(idle),
						bw(uptime), bw(truncCol(clonedFrom, 14)), lock)
				}
			}
		} else {
			if showAuth {
				fmt.Fprintf(out, "%s %s  %s %s %-8s %-7s %s%s\n",
					num, bw(fmt.Sprintf("%-*s", aliasWidth, alias)), bw(fmt.Sprintf("%-*s", idWidth, r.SandboxID)),
					colorStatus, bw(ttl), bw(uptime), bw(authStr), lock)
			} else {
				fmt.Fprintf(out, "%s %s  %s %s %-8s %s%s\n",
					num, bw(fmt.Sprintf("%-*s", aliasWidth, alias)), bw(fmt.Sprintf("%-*s", idWidth, r.SandboxID)),
					colorStatus, bw(ttl), bw(uptime), lock)
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

// checkEC2Hibernation looks up the EC2 instance for a sandbox by tag and returns
// whether hibernation is configured on the instance.
func checkEC2Hibernation(ctx context.Context, client *ec2.Client, sandboxID string) bool {
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   awssdk.String("tag:km:sandbox-id"),
				Values: []string{sandboxID},
			},
		},
	})
	if err != nil || len(out.Reservations) == 0 {
		return false
	}
	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			if inst.HibernationOptions != nil && inst.HibernationOptions.Configured != nil {
				return *inst.HibernationOptions.Configured
			}
		}
	}
	return false
}

// checkEC2InstanceStatus looks up the EC2 instance for a sandbox by tag and returns
// the live status: "running", "stopped", "terminated" (shown as "killed"), etc.
func checkEC2InstanceStatus(ctx context.Context, client *ec2.Client, sandboxID string) string {
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   awssdk.String("tag:km:sandbox-id"),
				Values: []string{sandboxID},
			},
		},
	})
	if err != nil || len(out.Reservations) == 0 {
		return "killed" // can't find instance — likely terminated and gone
	}

	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			switch inst.State.Name {
			case ec2types.InstanceStateNameRunning:
				return "running"
			case ec2types.InstanceStateNameStopped:
				return "stopped"
			case ec2types.InstanceStateNameTerminated, ec2types.InstanceStateNameShuttingDown:
				if inst.StateReason != nil && inst.StateReason.Code != nil &&
					*inst.StateReason.Code == "Server.SpotInstanceTermination" {
					return "reaped"
				}
				return "killed"
			case ec2types.InstanceStateNamePending:
				return "starting"
			default:
				return string(inst.State.Name)
			}
		}
	}
	return "killed"
}
