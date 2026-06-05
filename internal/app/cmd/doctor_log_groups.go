package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwlogstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// logGroupFamily describes a named family of per-sandbox CWL log groups.
// Each family has a prefix filter; sandbox IDs are extracted by stripPrefix.
type logGroupFamily struct {
	name        string
	legacyFilter string // always built from literal "km"
	// stripSandboxID extracts the sandbox ID from a fully-qualified log-group name.
	// Returns "" when the name is not a per-sandbox group (should be skipped).
	stripSandboxID func(logGroupName, filter string) string
}

// logGroupFilterEntry pairs a DescribeLogGroups prefix filter with
// a function that extracts the sandbox ID from a matched log-group name.
type logGroupFilterEntry struct {
	filter         string
	extractSandbox func(name string) string
}

// perSandboxFamilies returns the four per-sandbox log-group families,
// each in two variants: legacy (literal "km") and dynamic prefix.
// The returned slice already has both variants interleaved. Callers must
// dedup by filter string (identical when prefix=="km").
func perSandboxFamilies(prefix string) []logGroupFilterEntry {
	// Helper: strip a known prefix from name → remaining is sandbox ID.
	stripPrefix := func(filter string) func(name string) string {
		return func(name string) string {
			return strings.TrimPrefix(name, filter)
		}
	}
	// Helper: 3rd path component after splitting on "/".
	// e.g. "/km/sandboxes/sb-abc/stream" → "sb-abc"
	thirdComponent := func(name string) string {
		parts := strings.SplitN(strings.TrimPrefix(name, "/"), "/", 3)
		if len(parts) < 3 {
			return ""
		}
		// parts[0]=prefix/km, parts[1]=sandboxes|sidecars, parts[2]=sb-id[/...]
		id := parts[2]
		// Trim trailing path beyond sandbox ID
		if slash := strings.Index(id, "/"); slash >= 0 {
			id = id[:slash]
		}
		return id
	}

	var out []logGroupFilterEntry

	// Build each (legacy, prefixed) pair and append both.
	// Lambda: budget-enforcer
	legacyBudget := "/aws/lambda/km-budget-enforcer-"
	prefixedBudget := "/aws/lambda/" + prefix + "-budget-enforcer-"
	out = append(out, logGroupFilterEntry{legacyBudget, stripPrefix(legacyBudget)})
	out = append(out, logGroupFilterEntry{prefixedBudget, stripPrefix(prefixedBudget)})

	// Lambda: github-token-refresher
	legacyGH := "/aws/lambda/km-github-token-refresher-"
	prefixedGH := "/aws/lambda/" + prefix + "-github-token-refresher-"
	out = append(out, logGroupFilterEntry{legacyGH, stripPrefix(legacyGH)})
	out = append(out, logGroupFilterEntry{prefixedGH, stripPrefix(prefixedGH)})

	// Audit-log sidecar: /km/sandboxes/ → /{prefix}/sandboxes/
	legacySandboxes := "/km/sandboxes/"
	prefixedSandboxes := "/" + prefix + "/sandboxes/"
	out = append(out, logGroupFilterEntry{legacySandboxes, thirdComponent})
	out = append(out, logGroupFilterEntry{prefixedSandboxes, thirdComponent})

	// ECS sidecars: /km/sidecars/ → /{prefix}/sidecars/
	legacySidecars := "/km/sidecars/"
	prefixedSidecars := "/" + prefix + "/sidecars/"
	out = append(out, logGroupFilterEntry{legacySidecars, thirdComponent})
	out = append(out, logGroupFilterEntry{prefixedSidecars, thirdComponent})

	return out
}

// managementLogGroupPrefixes returns the exact log-group names for the four
// management Lambdas. These are {prefix}-scoped (not legacy km-hardcoded),
// and should have retention set but must NEVER be deleted.
func managementLogGroupPrefixes(prefix string) []string {
	return []string{
		"/aws/lambda/" + prefix + "-create-handler",
		"/aws/lambda/" + prefix + "-ttl-handler",
		"/aws/lambda/" + prefix + "-email-handler",
		"/aws/lambda/" + prefix + "-slack-bridge",
	}
}

// describeAllLogGroupsForFilter runs a paginated DescribeLogGroups for filter
// and accumulates results into the provided map[logGroupName]LogGroup.
// It is safe to call for overlapping filters; the map deduplicates.
func describeAllLogGroupsForFilter(
	ctx context.Context,
	client CWLogsCleanupAPI,
	filter string,
	out map[string]cwlogstypes.LogGroup,
) error {
	var nextToken *string
	for {
		resp, err := client.DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
			LogGroupNamePrefix: awssdk.String(filter),
			NextToken:          nextToken,
		})
		if err != nil {
			return err
		}
		for _, g := range resp.LogGroups {
			name := awssdk.ToString(g.LogGroupName)
			if _, seen := out[name]; !seen {
				out[name] = g
			}
		}
		if resp.NextToken == nil {
			break
		}
		nextToken = resp.NextToken
	}
	return nil
}

// checkStaleLogGroups detects per-sandbox CloudWatch log groups that outlived
// their sandbox (orphaned) across four families, matching both the historical
// literal "km-"/"/ km/" prefix AND the dynamic {prefix} prefix. It warns and
// optionally deletes orphans (--delete-logs), and optionally applies a
// retention policy (--set-log-retention).
//
// Nil client → CheckSkipped.
func checkStaleLogGroups(
	ctx context.Context,
	client CWLogsCleanupAPI,
	lister SandboxLister,
	dryRun bool,
	deleteLogs bool,
	setLogRetention bool,
	retentionDays int32,
	prefix string,
) CheckResult {
	name := "Stale Log Groups"
	if client == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "CloudWatch Logs client not available"}
	}

	// --- Step 1: build deduped filter list ---
	families := perSandboxFamilies(prefix)

	// Dedup filters; preserve extraction function per filter.
	seenFilters := map[string]bool{}
	var distinctFilters []logGroupFilterEntry
	for _, f := range families {
		if seenFilters[f.filter] {
			continue
		}
		seenFilters[f.filter] = true
		distinctFilters = append(distinctFilters, f)
	}

	// --- Step 2: paginated scan — accumulate all per-sandbox groups ---
	// allGroups maps full log-group name → LogGroup (deduped by name).
	allGroups := map[string]cwlogstypes.LogGroup{}
	// For each group, record its sandbox ID (extracted by whichever filter matched).
	groupSandboxID := map[string]string{} // logGroupName → sandboxID

	for _, fe := range distinctFilters {
		err := describeAllLogGroupsForFilter(ctx, client, fe.filter, allGroups)
		if err != nil {
			return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list log groups (filter %s): %v", fe.filter, err)}
		}
	}

	// Assign sandbox IDs for each accumulated group using matched filter extractor.
	// We rerun the same paginated scan but only to build groupSandboxID — re-use allGroups instead.
	// More efficient: re-derive sandbox IDs from the group name by trying each filter in order.
	for groupName := range allGroups {
		for _, fe := range distinctFilters {
			if strings.HasPrefix(groupName, fe.filter) {
				id := fe.extractSandbox(groupName)
				if id != "" {
					groupSandboxID[groupName] = id
					break
				}
			}
		}
	}

	// --- Step 3: group by sandbox ID ---
	groupsBySandbox := map[string][]string{} // sandboxID → []logGroupName
	for groupName, sbID := range groupSandboxID {
		if sbID == "" {
			continue
		}
		groupsBySandbox[sbID] = append(groupsBySandbox[sbID], groupName)
	}

	// --- Step 4: list active sandboxes ---
	if lister == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "sandbox lister not available (state bucket not configured)"}
	}
	records, err := lister.ListSandboxes(ctx, false)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list sandboxes: %v", err)}
	}
	activeSandboxes := map[string]bool{}
	for _, r := range records {
		activeSandboxes[r.SandboxID] = true
	}

	// --- Step 5: find orphaned groups ---
	var orphanedGroups []string
	orphanedSandboxCount := 0
	for sbID, groups := range groupsBySandbox {
		if !activeSandboxes[sbID] {
			orphanedSandboxCount++
			orphanedGroups = append(orphanedGroups, groups...)
		}
	}

	// --- Step 6: retention pass (management + per-sandbox groups) ---
	var retentionMsg string
	if setLogRetention && !dryRun {
		retSet, retFailed := applyLogRetention(ctx, client, retentionDays, prefix, allGroups)
		if retSet > 0 || retFailed > 0 {
			retentionMsg = fmt.Sprintf("; set retention on %d groups", retSet)
			if retFailed > 0 {
				retentionMsg += fmt.Sprintf(" (%d failed)", retFailed)
			}
		}
	}

	// --- Step 7: report or delete ---
	total := len(allGroups)

	if len(orphanedGroups) == 0 {
		msg := fmt.Sprintf("%d log group(s), all active", total)
		if retentionMsg != "" {
			msg += retentionMsg
		}
		return CheckResult{Name: name, Status: CheckOK, Message: msg}
	}

	if dryRun || !deleteLogs {
		hint := "use --dry-run=false --delete-logs to delete"
		if !dryRun && !deleteLogs {
			hint = "use --delete-logs to delete"
		}
		msg := fmt.Sprintf("found %d orphaned log group(s) across %d sandbox(es) (%s)",
			len(orphanedGroups), orphanedSandboxCount, hint)
		if retentionMsg != "" {
			msg += retentionMsg
		}
		return CheckResult{Name: name, Status: CheckWarn, Message: msg}
	}

	// Delete orphaned groups
	var deleted, failed int
	for _, groupName := range orphanedGroups {
		_, delErr := client.DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{
			LogGroupName: awssdk.String(groupName),
		})
		if delErr != nil {
			var notFound *cwlogstypes.ResourceNotFoundException
			if errors.As(delErr, &notFound) {
				deleted++ // already gone → treat as success
			} else {
				failed++
			}
		} else {
			deleted++
		}
	}

	msg := fmt.Sprintf("deleted %d orphaned log group(s)", deleted)
	if failed > 0 {
		msg += fmt.Sprintf(", %d failed", failed)
	}
	if retentionMsg != "" {
		msg += retentionMsg
	}

	if failed == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: msg}
	}
	return CheckResult{Name: name, Status: CheckWarn, Message: msg}
}

// applyLogRetention sets retentionInDays on any log group (per-sandbox OR management)
// whose current RetentionInDays is nil. Already-set groups are skipped (idempotent).
// Returns (set, failed) counts.
func applyLogRetention(
	ctx context.Context,
	client CWLogsCleanupAPI,
	retentionDays int32,
	prefix string,
	perSandboxGroups map[string]cwlogstypes.LogGroup, // already-fetched per-sandbox groups
) (set, failed int) {
	// Collect all groups that need retention: per-sandbox + management.
	candidates := map[string]cwlogstypes.LogGroup{}
	for name, g := range perSandboxGroups {
		candidates[name] = g
	}

	// Fetch management groups fresh (they are exact names, not prefix-filtered per-sandbox).
	mgmtPrefixes := managementLogGroupPrefixes(prefix)
	for _, mgmtPrefix := range mgmtPrefixes {
		mgmtAll := map[string]cwlogstypes.LogGroup{}
		if err := describeAllLogGroupsForFilter(ctx, client, mgmtPrefix, mgmtAll); err == nil {
			for n, g := range mgmtAll {
				candidates[n] = g
			}
		}
	}

	for _, g := range candidates {
		if g.RetentionInDays != nil {
			continue // already set → idempotent skip
		}
		groupName := awssdk.ToString(g.LogGroupName)
		if groupName == "" {
			continue
		}
		_, err := client.PutRetentionPolicy(ctx, &cloudwatchlogs.PutRetentionPolicyInput{
			LogGroupName:    awssdk.String(groupName),
			RetentionInDays: awssdk.Int32(retentionDays),
		})
		if err != nil {
			failed++
		} else {
			set++
		}
	}
	return set, failed
}
