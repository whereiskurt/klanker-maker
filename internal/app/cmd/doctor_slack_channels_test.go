package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

func mappingLister(ms ...SlackChannelMapping) func(context.Context) ([]SlackChannelMapping, error) {
	return func(context.Context) ([]SlackChannelMapping, error) { return ms, nil }
}

func derive(alias string) string { return "sb-" + alias }

func TestCheckSlackChannelMappings_HealthyIsOK(t *testing.T) {
	resolve := func(_ context.Context, id string) (string, bool, error) {
		return "sb-github-bot", false, nil
	}
	r := checkSlackChannelMappings(context.Background(),
		mappingLister(SlackChannelMapping{Alias: "github-bot", ChannelID: "C0AAA"}),
		resolve, derive)

	if r.Status != CheckOK {
		t.Errorf("status = %v, want OK (message: %s)", r.Status, r.Message)
	}
}

// The finding that does NOT self-heal: the stored row wins over the derived name
// forever, so renaming a profile's channel template silently does nothing.
func TestCheckSlackChannelMappings_NameMismatchWarns(t *testing.T) {
	resolve := func(_ context.Context, id string) (string, bool, error) {
		return "sec-github-bot", false, nil // old naming convention
	}
	r := checkSlackChannelMappings(context.Background(),
		mappingLister(SlackChannelMapping{Alias: "github-bot", ChannelID: "C0BAPD4TCDB"}),
		resolve, derive)

	if r.Status != CheckWarn {
		t.Fatalf("status = %v, want WARN (message: %s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "sec-github-bot") || !strings.Contains(r.Message, "sb-github-bot") {
		t.Errorf("message %q must name BOTH the stored and the derived channel", r.Message)
	}
	if !strings.Contains(r.Remediation, "km slack forget-channel") {
		t.Errorf("remediation %q does not name the escape hatch", r.Remediation)
	}
}

func TestCheckSlackChannelMappings_DeadChannelWarns(t *testing.T) {
	resolve := func(_ context.Context, id string) (string, bool, error) {
		return "", true, &slack.SlackAPIError{Method: "conversations.info", Code: "channel_not_found"}
	}
	r := checkSlackChannelMappings(context.Background(),
		mappingLister(SlackChannelMapping{Alias: "gone", ChannelID: "C0DEAD"}),
		resolve, derive)

	if r.Status != CheckWarn {
		t.Fatalf("status = %v, want WARN", r.Status)
	}
	if !strings.Contains(r.Message, "C0DEAD") {
		t.Errorf("message %q does not name the dead channel", r.Message)
	}
}

// A ratelimited / missing_scope probe means "unprobed", never a finding — the same
// rule the resolver follows, so doctor cannot invent problems from a flaky call.
func TestCheckSlackChannelMappings_ProbeFailureIsNotAFinding(t *testing.T) {
	resolve := func(_ context.Context, id string) (string, bool, error) {
		return "", false, errors.New("ratelimited")
	}
	r := checkSlackChannelMappings(context.Background(),
		mappingLister(SlackChannelMapping{Alias: "flaky", ChannelID: "C0AAA"}),
		resolve, derive)

	if r.Status != CheckOK {
		t.Errorf("status = %v, want OK — an unprobed channel is not a finding (message: %s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "unprobed") {
		t.Errorf("message %q should disclose that a channel could not be probed", r.Message)
	}
}

// Archived is deliberately NOT a finding: km unarchives and reuses on the next
// create, so warning would be noise. The resolver reports the name normally.
func TestCheckSlackChannelMappings_ArchivedIsNotAFinding(t *testing.T) {
	resolve := func(_ context.Context, id string) (string, bool, error) {
		return "sb-github-bot", false, nil // archived, but name matches
	}
	r := checkSlackChannelMappings(context.Background(),
		mappingLister(SlackChannelMapping{Alias: "github-bot", ChannelID: "C0AAA"}),
		resolve, derive)

	if r.Status != CheckOK {
		t.Errorf("status = %v, want OK — archived channels self-heal via unarchive-on-reuse", r.Status)
	}
}

func TestCheckSlackChannelMappings_NoStoreSkips(t *testing.T) {
	r := checkSlackChannelMappings(context.Background(), nil, nil, derive)
	if r.Status != CheckSkipped {
		t.Errorf("status = %v, want SKIP when the store/client is unavailable", r.Status)
	}
}

func TestCheckSlackChannelMappings_EmptyTableIsOK(t *testing.T) {
	resolve := func(_ context.Context, id string) (string, bool, error) { return "", false, nil }
	r := checkSlackChannelMappings(context.Background(), mappingLister(), resolve, derive)
	if r.Status != CheckOK {
		t.Errorf("status = %v, want OK for an empty table", r.Status)
	}
}
