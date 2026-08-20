package cmd

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// The archive/reuse trap, end to end at the resolver:
//
// archiveOnDestroy defaults to true, and the alias→channel mapping is durable by
// design (there is no DeleteByAlias on the destroy path). So destroying any
// per-sandbox-channel sandbox and creating again on the same alias resolved to an
// archived channel — which conversations.info reports as perfectly healthy — and
// the create then died at conversations.join with is_archived. No in-product
// remedy existed; the documented escape was a raw `aws dynamodb delete-item`.

// TestResolveSlackChannel_UnarchivesStoredArchivedChannel is the fix: a stored,
// archived channel is lifted back out and reused, not fatal.
func TestResolveSlackChannel_UnarchivesStoredArchivedChannel(t *testing.T) {
	api := &fakeSlackAPI{
		channelInfoMember:      true,
		channelDetailsName:     "sb-xyz-github-bot",
		channelDetailsArchived: true,
	}
	store := &fakeChannelStore{m: map[string]string{"github-bot": "C0BAPD4TCDB"}}

	id, _, err := resolvePerSandboxChannelForTest(t, api, store, "github-bot", "sb-xyz-github-bot")
	if err != nil {
		t.Fatalf("resolveSlackChannel on an archived stored channel: %v (want reuse, not failure)", err)
	}
	if id != "C0BAPD4TCDB" {
		t.Errorf("channel = %q; want the stored C0BAPD4TCDB (reused, not recreated)", id)
	}
	if !api.unarchiveCalled {
		t.Error("UnarchiveChannel was not called — the archived channel was not recovered")
	}
	if api.createCalls > 0 {
		t.Error("CreateChannel was called; an archived channel must be reused, not duplicated")
	}
}

// TestResolveSlackChannel_LiveChannelIsNotUnarchived guards against calling
// conversations.unarchive on every reuse — it must fire only when archived.
func TestResolveSlackChannel_LiveChannelIsNotUnarchived(t *testing.T) {
	api := &fakeSlackAPI{
		channelInfoMember:      true,
		channelDetailsName:     "sb-xyz-github-bot",
		channelDetailsArchived: false,
	}
	store := &fakeChannelStore{m: map[string]string{"github-bot": "C0LIVE"}}

	id, _, err := resolvePerSandboxChannelForTest(t, api, store, "github-bot", "sb-xyz-github-bot")
	if err != nil {
		t.Fatalf("resolveSlackChannel: %v", err)
	}
	if id != "C0LIVE" {
		t.Errorf("channel = %q; want C0LIVE", id)
	}
	if api.unarchiveCalled {
		t.Error("UnarchiveChannel called on a live channel; it must fire only for archived ones")
	}
}

// TestResolveSlackChannel_UnarchiveFailureFallsThrough proves a failed unarchive
// (e.g. missing_scope) does not silently pretend success — the create still
// surfaces the actionable join error rather than continuing into a broken channel.
func TestResolveSlackChannel_UnarchiveFailureFallsThrough(t *testing.T) {
	api := &fakeSlackAPI{
		channelInfoMember:      true,
		channelDetailsName:     "sb-xyz-github-bot",
		channelDetailsArchived: true,
		unarchiveErr:           &slack.SlackAPIError{Method: "conversations.unarchive", Code: "missing_scope"},
		joinChannelErr:         &slack.SlackAPIError{Method: "conversations.join", Code: "is_archived"},
	}
	store := &fakeChannelStore{m: map[string]string{"github-bot": "C0BAPD4TCDB"}}

	_, _, err := resolvePerSandboxChannelForTest(t, api, store, "github-bot", "sb-xyz-github-bot")
	if err == nil {
		t.Fatal("expected an error when unarchive fails and the channel stays archived")
	}
	if !api.unarchiveCalled {
		t.Error("UnarchiveChannel was not attempted")
	}
	// The old message interpolated the DERIVED name with the STORED id, naming a
	// channel that never existed. It must now name the real one.
	if !strings.Contains(err.Error(), "sb-xyz-github-bot") {
		t.Errorf("error %q does not name the channel's real name", err.Error())
	}
	if !strings.Contains(err.Error(), "km slack forget-channel") {
		t.Errorf("error %q does not point at the in-product remedy (km slack forget-channel)", err.Error())
	}
}

// TestResolveSlackChannel_ChannelNotFoundStillRecreates pins that the ONLY
// definitive-dead signal is unchanged: archived is recoverable, missing is not.
func TestResolveSlackChannel_ChannelNotFoundStillRecreates(t *testing.T) {
	api := &fakeSlackAPI{
		channelInfoErr:      &slack.SlackAPIError{Method: "conversations.info", Code: "channel_not_found"},
		createChannelResult: "C0NEW",
		channelInfoMember:   true,
	}
	store := &fakeChannelStore{m: map[string]string{"github-bot": "C0DEAD"}}

	id, _, err := resolvePerSandboxChannelForTest(t, api, store, "github-bot", "sb-xyz-github-bot")
	if err != nil {
		t.Fatalf("resolveSlackChannel: %v", err)
	}
	if api.createCalls == 0 {
		t.Error("CreateChannel not called; channel_not_found must still invalidate and recreate")
	}
	if id != "C0NEW" {
		t.Errorf("channel = %q; want the freshly created C0NEW", id)
	}
	if api.unarchiveCalled {
		t.Error("UnarchiveChannel called for a channel_not_found; nothing to unarchive")
	}
}
