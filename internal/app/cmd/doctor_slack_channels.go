// Package cmd — doctor_slack_channels.go
// Surfaces alias→channel mappings that will not behave the way the profile says.
//
// The km-slack-channels rows are durable by design: they are what make alias reuse
// resolve in O(1), and nothing on the destroy path clears them. The cost of that
// durability is that a row can quietly diverge from the profile that created it,
// and resolution never notices — the stored row is consulted before the derived
// channel name and the two are never compared.
//
// Scope note: this check READS the table. It must never delete a row (alias rows
// are not per-sandbox and must survive destroy — see checkOrphanedDDBRows), and it
// deliberately does not flag archived channels, which km now unarchives and reuses
// automatically.
package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// SlackChannelMapping is one row of the km-slack-channels table.
type SlackChannelMapping struct {
	Alias     string
	ChannelID string
}

// SlackChannelResolver reports a channel's current name. dead=true means the
// channel is definitively gone (channel_not_found); any other error is a probe
// failure and must not be reported as a finding.
type SlackChannelResolver func(ctx context.Context, channelID string) (name string, dead bool, err error)

// checkSlackChannelMappings WARNs on alias rows whose channel no longer matches
// what the install would derive today.
//
// Two findings, both of which resolution silently ignores:
//
//	dead      — the row points at a deleted channel.
//	mismatch  — the row points at a channel whose name differs from the derived
//	            one. This is the one that does not self-heal: renaming the
//	            channel template in a profile has NO effect while a mapping
//	            exists, because the stored row wins.
//
// Archived channels are NOT a finding: km unarchives and reuses them on the next
// create, so warning about them would be noise.
func checkSlackChannelMappings(
	ctx context.Context,
	listMappings func(context.Context) ([]SlackChannelMapping, error),
	resolve SlackChannelResolver,
	deriveName func(alias string) string,
) CheckResult {
	const name = "Slack channel mappings"

	if listMappings == nil || resolve == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "slack channel store or Slack client unavailable"}
	}

	mappings, err := listMappings(ctx)
	if err != nil {
		// A table that does not exist is not a failure: installs without
		// per-sandbox Slack never create one (same posture as the table's own
		// existence check, which is WARN-demoted for exactly this reason).
		return CheckResult{Name: name, Status: CheckSkipped, Message: fmt.Sprintf("could not read mappings: %v", err)}
	}
	if len(mappings) == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: "no alias→channel mappings stored"}
	}

	var dead, mismatched []string
	probeFailures := 0

	for _, m := range mappings {
		actual, isDead, resolveErr := resolve(ctx, m.ChannelID)
		switch {
		case isDead:
			dead = append(dead, fmt.Sprintf("%s→%s", m.Alias, m.ChannelID))
		case resolveErr != nil:
			// Ratelimited / transient / missing_scope: unprobed, never a finding.
			probeFailures++
		case deriveName != nil && actual != "" && actual != deriveName(m.Alias):
			mismatched = append(mismatched, fmt.Sprintf("%s→#%s (profile would derive #%s)",
				m.Alias, actual, deriveName(m.Alias)))
		}
	}

	sort.Strings(dead)
	sort.Strings(mismatched)

	if len(dead) == 0 && len(mismatched) == 0 {
		msg := fmt.Sprintf("%d mapping(s) healthy", len(mappings))
		if probeFailures > 0 {
			msg += fmt.Sprintf(" (%d unprobed)", probeFailures)
		}
		return CheckResult{Name: name, Status: CheckOK, Message: msg}
	}

	var parts []string
	if len(dead) > 0 {
		parts = append(parts, "dead channel: "+strings.Join(dead, ", "))
	}
	if len(mismatched) > 0 {
		parts = append(parts, "name mismatch: "+strings.Join(mismatched, ", "))
	}

	return CheckResult{
		Name:    name,
		Status:  CheckWarn,
		Message: strings.Join(parts, "; "),
		Remediation: "The stored mapping wins over the profile's derived channel name, so a renamed " +
			"template has no effect while the row exists. Drop it with `km slack forget-channel <alias>` " +
			"to let the next km create re-derive, or `km slack adopt <alias> <channelID>` to repoint it.",
	}
}
