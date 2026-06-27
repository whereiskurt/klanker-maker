package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/quota"
)

// windowOrder is the stable render order for windows within an action.
var quotaWindowOrder = map[string]int{"lifetime": 0, "hour": 1, "day": 2}

// RenderQuotas writes the "Quotas:" section to out, one aligned line per
// (action, window) configured in actionLimitsJSON. It shows used/limit, the
// onBreach policy, and appends "(hard-deny)" when limit==0. When usage is nil
// (live fetch unavailable), the used count renders as "?". An empty or blank
// actionLimitsJSON omits the section entirely (no output).
//
// This is a pure rendering helper (no AWS calls) so it can be unit-tested with a
// fake usage slice; the live DDB fetch happens at the call site.
func RenderQuotas(out io.Writer, actionLimitsJSON string, usage []quota.UsageRow) {
	if strings.TrimSpace(actionLimitsJSON) == "" {
		return
	}

	var limits quota.Limits
	if err := json.Unmarshal([]byte(actionLimitsJSON), &limits); err != nil {
		// Malformed action_limits — surface a single diagnostic line rather than
		// silently dropping the section, then bail.
		fmt.Fprintf(out, "Quotas:\n  <unparseable action_limits: %v>\n", err)
		return
	}
	if len(limits) == 0 {
		return
	}

	// Index live usage by (action, window) for O(1) lookup. usage==nil ⇒ unavailable.
	usageAvailable := usage != nil
	used := make(map[string]int64, len(usage))
	for _, u := range usage {
		used[string(u.Action)+"/"+u.Window] = u.Used
	}

	// Build the rows in deterministic order: actions in the canonical const order,
	// windows lifetime→hour→day. Reuse quota.FetchUsage's expansion via a fake-less
	// path here — we expand the limits ourselves so the section renders even when the
	// live fetch failed.
	type line struct {
		action  string
		window  string
		usedStr string
		limit   int64
		policy  string
	}
	var lines []line

	for _, action := range quota.AllActionsOrder() {
		al, ok := limits[action]
		if !ok {
			continue
		}
		policy := string(al.OnBreach)
		if policy == "" {
			policy = string(quota.BreachWarn)
		}
		windows := []struct {
			name  string
			limit *int64
		}{
			{"lifetime", al.Lifetime},
			{"perHour", al.PerHour},
			{"perDay", al.PerDay},
		}
		for _, w := range windows {
			if w.limit == nil {
				continue
			}
			// Map the display window name to the usage window key.
			usageWindow := map[string]string{"lifetime": "lifetime", "perHour": "hour", "perDay": "day"}[w.name]
			usedStr := "?"
			if usageAvailable {
				usedStr = fmt.Sprintf("%d", used[string(action)+"/"+usageWindow])
			}
			lines = append(lines, line{
				action:  string(action),
				window:  w.name,
				usedStr: usedStr,
				limit:   *w.limit,
				policy:  policy,
			})
		}
	}

	if len(lines) == 0 {
		return
	}

	// Column widths for alignment.
	actionW, windowW, countW := 0, 0, 0
	type rendered struct {
		action, window, count, policy, suffix string
	}
	rows := make([]rendered, 0, len(lines))
	for _, l := range lines {
		count := fmt.Sprintf("%s/%d", l.usedStr, l.limit)
		suffix := ""
		if l.limit == 0 {
			suffix = "(hard-deny)"
		}
		r := rendered{action: l.action, window: l.window, count: count, policy: l.policy, suffix: suffix}
		rows = append(rows, r)
		if len(r.action) > actionW {
			actionW = len(r.action)
		}
		if len(r.window) > windowW {
			windowW = len(r.window)
		}
		if len(r.count) > countW {
			countW = len(r.count)
		}
	}

	fmt.Fprintf(out, "Quotas:\n")
	for _, r := range rows {
		line := fmt.Sprintf("  %-*s  %-*s  %-*s  %s", actionW, r.action, windowW, r.window, countW, r.count, r.policy)
		if r.suffix != "" {
			line += "  " + r.suffix
		}
		fmt.Fprintf(out, "%s\n", strings.TrimRight(line, " "))
	}
}

// fetchQuotaUsage is the live DDB call site for the Quotas section. It parses the
// resolved action_limits JSON and reads current counters from the {prefix}-action-quota
// table. Fail-soft: any error (parse, AWS config, GetItem) returns nil usage so the
// caller still renders the configured limits with "?" counts.
func fetchQuotaUsage(ctx context.Context, sandboxID, actionLimitsJSON, resourcePrefix string) []quota.UsageRow {
	if strings.TrimSpace(actionLimitsJSON) == "" {
		return nil
	}
	var limits quota.Limits
	if err := json.Unmarshal([]byte(actionLimitsJSON), &limits); err != nil || len(limits) == 0 {
		return nil
	}
	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return nil
	}
	client := dynamodb.NewFromConfig(awsCfg)
	rows, err := quota.FetchUsage(ctx, client, resourcePrefix+"-action-quota", sandboxID, limits)
	if err != nil {
		return nil
	}
	return rows
}
