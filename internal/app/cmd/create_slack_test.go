package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// ────────────────────────────────────────────────────────────────────────────
// Fake implementations
// ────────────────────────────────────────────────────────────────────────────

// fakeSlackAPI implements SlackAPI for tests.
type fakeSlackAPI struct {
	createChannelResult string
	createChannelErr    error
	findChannelResult   string // returned by FindChannelByName when name_taken triggers lookup
	findChannelErr      error
	joinChannelErr      error
	inviteSharedErr     error
	channelInfoMember   bool
	channelInfoCount    int
	channelInfoErr      error

	// Phase 104: additional fields for lookup-first resolver tests.
	findShouldPanic bool // if true, FindChannelByName panics (asserts it was never called)
	createCalls     int  // counts CreateChannel invocations

	// capture calls
	createChannelName  string
	createPrivate      bool // Phase 118: records the private arg for Plan 02 assertion
	findChannelCalled  bool
	joinChannelCalled  bool
	inviteSharedCalled bool
	channelInfoCalled  bool
}

func (f *fakeSlackAPI) CreateChannel(_ context.Context, name string, private bool) (string, error) {
	f.createChannelName = name
	f.createPrivate = private
	f.createCalls++
	return f.createChannelResult, f.createChannelErr
}

func (f *fakeSlackAPI) FindChannelByName(_ context.Context, _ string, _ int) (string, error) {
	f.findChannelCalled = true
	if f.findShouldPanic {
		panic("FindChannelByName must NOT be called in this scenario")
	}
	return f.findChannelResult, f.findChannelErr
}

func (f *fakeSlackAPI) JoinChannel(_ context.Context, _ string) error {
	f.joinChannelCalled = true
	return f.joinChannelErr
}

func (f *fakeSlackAPI) InviteShared(_ context.Context, _, _ string) error {
	f.inviteSharedCalled = true
	return f.inviteSharedErr
}

func (f *fakeSlackAPI) ChannelInfo(_ context.Context, _ string) (int, bool, error) {
	f.channelInfoCalled = true
	return f.channelInfoCount, f.channelInfoMember, f.channelInfoErr
}

// Phase 72 InviteAPI methods — required by the extended SlackAPI interface.
// fakeSlackAPI stubs these with no-op behavior (lookup always misses,
// InviteUserToChannelStrict always succeeds) so pre-Phase-72 tests that
// don't need these methods continue to compile and pass unmodified.
func (f *fakeSlackAPI) LookupUserByEmail(_ context.Context, _ string) (string, bool, error) {
	return "", false, nil // lookup miss → triggers external/Connect path
}

func (f *fakeSlackAPI) InviteUserToChannelStrict(_ context.Context, _, _ string) error {
	return nil
}

// fakeSSMParamStore implements SSMParamStore (and the optional ssmParamWriter)
// for tests. Shared across create_slack_test.go and destroy_slack_test.go.
type fakeSSMParamStore struct {
	params map[string]string
	puts   []struct{ Name, Value string }
	putErr error
}

func (f *fakeSSMParamStore) Get(_ context.Context, name string, _ bool) (string, error) {
	if v, ok := f.params[name]; ok {
		return v, nil
	}
	return "", nil
}

func (f *fakeSSMParamStore) Put(_ context.Context, name, value string, _ bool) error {
	if f.putErr != nil {
		return f.putErr
	}
	if f.params == nil {
		f.params = map[string]string{}
	}
	f.params[name] = value
	f.puts = append(f.puts, struct{ Name, Value string }{name, value})
	return nil
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

func boolPtrCreate(b bool) *bool { return &b }

// ─── Phase 104: fakeChannelStore + lookup-first resolver helpers ─────────────

// fakeChannelStore is a map-backed SlackChannelStore for tests.
type fakeChannelStore struct {
	m map[string]string
}

func (s *fakeChannelStore) GetByAlias(_ context.Context, alias string) (string, error) {
	return s.m[alias], nil
}

func (s *fakeChannelStore) UpsertByAlias(_ context.Context, alias, channelID string) error {
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[alias] = channelID
	return nil
}

// resolvePerSandboxChannelForTest builds a minimal per-sandbox profile and calls
// resolveSlackChannel with an empty SSM store + the provided fake store.
// sandboxID is used as both the sandbox ID and the channel name (e.g. "sb-github-bot").
func resolvePerSandboxChannelForTest(t *testing.T, api *fakeSlackAPI, store SlackChannelStore, alias, sandboxID string) (string, bool, error) {
	t.Helper()
	return resolvePerSandboxChannelForTestWithSSM(t, api, store, alias, sandboxID, nil)
}

// resolvePerSandboxChannelForTestWithSSM is like resolvePerSandboxChannelForTest but
// pre-seeds the fake SSM by-name cache with the provided map (keyed by channel name).
func resolvePerSandboxChannelForTestWithSSM(t *testing.T, api *fakeSlackAPI, store SlackChannelStore, alias, sandboxID string, ssmByName map[string]string) (string, bool, error) {
	t.Helper()
	// Shrink retry delay so tests run fast.
	old := slackInfoRetryDelay
	slackInfoRetryDelay = time.Millisecond
	defer func() { slackInfoRetryDelay = old }()

	p := profileWithSlack(boolPtrCreate(true), true, "")
	params := map[string]string{
		"/km/slack/invite-email": "invite@example.com",
	}
	for k, v := range ssmByName {
		params["/km/slack/channel-id-by-name/"+k] = v
	}
	ssmStore := &fakeSSMParamStore{params: params}
	return resolveSlackChannel(context.Background(), p, sandboxID, alias, api, store, ssmStore, "/km/")
}

func profileWithSlack(enabled *bool, perSandbox bool, override string) *profile.SandboxProfile {
	p := &profile.SandboxProfile{}
	p.Spec.Notification = &profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:         enabled,
			PerSandbox:      boolPtrCreate(perSandbox),
			ChannelOverride: override,
		},
	}
	return p
}

// ────────────────────────────────────────────────────────────────────────────
// sanitizeChannelName tests
// ────────────────────────────────────────────────────────────────────────────

func TestSanitizeChannelName_Cases(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"research.team-A", "research-team-a"},
		{"ALL CAPS WITH SPACES", "all-caps-with-spaces"},
		{"  -leading-and-trailing-  ", "leading-and-trailing"},
		{"demo", "demo"},
		{"sb-abc-123", "sb-abc-123"},
		{"", ""},
		{"__underscores__", "__underscores__"},
		{strings.Repeat("a", 100), strings.Repeat("a", 80)},
	}

	for _, tc := range cases {
		got := sanitizeChannelName(tc.input)
		if got != tc.want {
			t.Errorf("sanitizeChannelName(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}

func TestSanitizeChannelName_CapAt80(t *testing.T) {
	input := strings.Repeat("a", 100)
	got := sanitizeChannelName(input)
	if len(got) != 80 {
		t.Errorf("sanitizeChannelName(100 chars) length = %d; want 80", len(got))
	}
}

func TestSanitizeChannelName_EmptyInputReturnsEmpty(t *testing.T) {
	if got := sanitizeChannelName(""); got != "" {
		t.Errorf("sanitizeChannelName(%q) = %q; want %q", "", got, "")
	}
}

func TestDeriveSandboxChannelName(t *testing.T) {
	cases := []struct {
		name    string
		custom  string
		profile string
		alias   string
		id      string
		want    string
		wantErr bool
	}{
		// Default derivation: sb- prefix forced.
		{name: "default_with_alias", custom: "", profile: "desktop", alias: "myml", id: "sb-abc123", want: "sb-myml"},
		{name: "default_no_alias_uses_id", custom: "", profile: "desktop", alias: "", id: "sb-abc123", want: "sb-abc123"},
		{name: "default_alias_with_dots", custom: "", profile: "desktop", alias: "my.box.1", id: "sb-x", want: "sb-my-box-1"},
		// Custom name: verbatim, NO sb- prefix.
		{name: "custom_literal", custom: "acme-desktops", profile: "desktop", alias: "myml", id: "sb-x", want: "acme-desktops"},
		{name: "custom_no_forced_prefix", custom: "team", profile: "desktop", alias: "myml", id: "sb-x", want: "team"},
		// Token substitution.
		{name: "custom_alias_token", custom: "proj-{alias}", profile: "desktop", alias: "myml", id: "sb-x", want: "proj-myml"},
		{name: "custom_id_token", custom: "box-{id}", profile: "desktop", alias: "myml", id: "sb-abc123", want: "box-sb-abc123"},
		{name: "custom_alias_token_falls_back_to_id", custom: "proj-{alias}", profile: "desktop", alias: "", id: "sb-abc", want: "proj-sb-abc"},
		// {profile} token (the shipped default scheme).
		{name: "custom_profile_alias", custom: "sb-{profile}-{alias}", profile: "desktop", alias: "myml", id: "sb-x", want: "sb-desktop-myml"},
		{name: "custom_profile_alias_falls_back_to_id", custom: "sb-{profile}-{alias}", profile: "learn.v2", alias: "", id: "sb-abc", want: "sb-learn-v2-sb-abc"},
		{name: "custom_profile_only", custom: "sb-{profile}", profile: "Learn V2", alias: "x", id: "sb-x", want: "sb-learn-v2"},
		// Sanitization of custom names (spaces/case/symbols → hyphens/lowercase).
		{name: "custom_sanitized", custom: "Acme Team!!", profile: "desktop", alias: "x", id: "sb-x", want: "acme-team"},
		// Empty sanitized custom → error.
		{name: "custom_all_symbols_errors", custom: "@@@", profile: "desktop", alias: "x", id: "sb-x", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deriveSandboxChannelName(tc.custom, tc.profile, tc.alias, tc.id)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for custom=%q, got name %q", tc.custom, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("deriveSandboxChannelName(%q,%q,%q,%q) = %q; want %q", tc.custom, tc.profile, tc.alias, tc.id, got, tc.want)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// resolveSlackChannel tests
// ────────────────────────────────────────────────────────────────────────────

func TestResolveSlack_NotEnabled_Skipped(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(false), false, "")
	api := &fakeSlackAPI{}
	ssmStore := &fakeSSMParamStore{}

	chID, perSb, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "myalias", api, nil, ssmStore, "/km/")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if chID != "" || perSb {
		t.Errorf("expected empty channelID and perSandbox=false, got %q/%v", chID, perSb)
	}
}

func TestResolveSlack_NilEnabled_Skipped(t *testing.T) {
	p := profileWithSlack(nil, false, "")
	api := &fakeSlackAPI{}
	ssmStore := &fakeSSMParamStore{}

	chID, perSb, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "", api, nil, ssmStore, "/km/")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if chID != "" || perSb {
		t.Errorf("expected empty channelID and perSandbox=false, got %q/%v", chID, perSb)
	}
}

func TestResolveSlack_SharedMode_HappyPath(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), false, "")
	api := &fakeSlackAPI{}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{
			"/km/slack/shared-channel-id": "C0SHARED",
		},
	}

	chID, perSb, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "", api, nil, ssmStore, "/km/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chID != "C0SHARED" {
		t.Errorf("channelID = %q; want %q", chID, "C0SHARED")
	}
	if perSb {
		t.Error("perSandbox = true; want false for shared mode")
	}
	if api.createChannelName != "" {
		t.Error("CreateChannel was called for shared mode; should not be")
	}
}

func TestResolveSlack_SharedMode_NotConfigured_Error(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), false, "")
	api := &fakeSlackAPI{}
	ssmStore := &fakeSSMParamStore{} // no /km/slack/shared-channel-id

	_, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "", api, nil, ssmStore, "/km/")
	if err == nil {
		t.Fatal("expected error when shared channel not configured")
	}
	if !strings.Contains(err.Error(), "km slack init") {
		t.Errorf("error %q should mention 'km slack init'", err.Error())
	}
}

func TestResolveSlack_PerSandbox_HappyPath_WithAlias(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{
		createChannelResult: "CNEWCHANNEL",
	}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{
			"/km/slack/invite-email": "invite@example.com",
		},
	}

	chID, perSb, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "demo", api, nil, ssmStore, "/km/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chID != "CNEWCHANNEL" {
		t.Errorf("channelID = %q; want %q", chID, "CNEWCHANNEL")
	}
	if !perSb {
		t.Error("perSandbox = false; want true for per-sandbox mode")
	}
	if api.createChannelName != "sb-demo" {
		t.Errorf("CreateChannel called with %q; want %q", api.createChannelName, "sb-demo")
	}
	if !api.inviteSharedCalled {
		t.Error("InviteShared was not called")
	}
}

// TestResolveSlack_PerSandbox_PrivateChannel — AC1 assertion (Phase 118).
// Verifies that notification.slack.private:true causes CreateChannel to be
// called with private=true (is_private:true in the Slack API payload), and
// that the default (private:false) results in private=false.
func TestResolveSlack_PerSandbox_PrivateChannel(t *testing.T) {
	t.Run("private:true → createPrivate=true", func(t *testing.T) {
		// Build a per-sandbox profile with Private=true.
		p := &profile.SandboxProfile{}
		p.Spec.Notification = &profile.NotificationSpec{
			Slack: &profile.NotificationSlackSpec{
				Enabled:    boolPtrCreate(true),
				PerSandbox: boolPtrCreate(true),
				Private:    true,
			},
		}
		api := &fakeSlackAPI{
			createChannelResult: "CPRIVATE",
		}
		ssmStore := &fakeSSMParamStore{
			params: map[string]string{
				"/km/slack/invite-email": "invite@example.com",
			},
		}

		_, _, err := resolveSlackChannel(context.Background(), p, "sb-private01", "priv-demo", api, nil, ssmStore, "/km/")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// AC1: the private flag must be threaded through to the Slack API call.
		if !api.createPrivate {
			t.Errorf("createPrivate = false; want true when notification.slack.private:true")
		}
	})

	t.Run("private:false (default) → createPrivate=false", func(t *testing.T) {
		// Build a per-sandbox profile with Private omitted (default false).
		p := profileWithSlack(boolPtrCreate(true), true, "")
		api := &fakeSlackAPI{
			createChannelResult: "CPUBLIC",
		}
		ssmStore := &fakeSSMParamStore{
			params: map[string]string{
				"/km/slack/invite-email": "invite@example.com",
			},
		}

		_, _, err := resolveSlackChannel(context.Background(), p, "sb-public01", "pub-demo", api, nil, ssmStore, "/km/")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// AC1: default (private:false) must NOT set is_private in the Slack API call.
		if api.createPrivate {
			t.Errorf("createPrivate = true; want false when notification.slack.private is not set (default)")
		}
	})
}

func TestResolveSlack_PerSandbox_HappyPath_NoAlias(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{
		createChannelResult: "CNEWCHANNEL2",
	}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{
			"/km/slack/invite-email": "invite@example.com",
		},
	}

	chID, perSb, err := resolveSlackChannel(context.Background(), p, "sb-abc12345", "", api, nil, ssmStore, "/km/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chID != "CNEWCHANNEL2" {
		t.Errorf("channelID = %q; want %q", chID, "CNEWCHANNEL2")
	}
	if !perSb {
		t.Error("perSandbox = false; want true")
	}
	// channel name should be sb-<sanitized sandboxID>
	if !strings.HasPrefix(api.createChannelName, "sb-") {
		t.Errorf("channel name %q should start with sb-", api.createChannelName)
	}
}

func TestResolveSlack_PerSandbox_AliasWithDots(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{
		createChannelResult: "CNEWCHANNEL3",
	}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{
			"/km/slack/invite-email": "invite@example.com",
		},
	}

	_, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "research.team-A", api, nil, ssmStore, "/km/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if api.createChannelName != "sb-research-team-a" {
		t.Errorf("CreateChannel called with %q; want %q", api.createChannelName, "sb-research-team-a")
	}
}

// TestResolveSlack_PerSandbox_NameTaken_AutoRecoversViaLookup verifies that
// name_taken auto-recovers via FindChannelByName when KM_SLACK_MAX_SCAN_PAGES>0
// (scan opt-in). Phase 104: scan is disabled by default; this test enables it.
func TestResolveSlack_PerSandbox_NameTaken_AutoRecoversViaLookup(t *testing.T) {
	old := SlackMaxScanPages
	SlackMaxScanPages = 100 // opt-in to scan
	defer func() { SlackMaxScanPages = old }()

	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{
		createChannelErr:  &slack.SlackAPIError{Method: "conversations.create", Code: "name_taken"},
		findChannelResult: "CEXISTING42", // simulates lookup finding the existing channel
	}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{
			"/km/slack/invite-email": "invite@example.com",
		},
	}

	chID, perSb, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "demo", api, nil, ssmStore, "/km/")
	if err != nil {
		t.Fatalf("expected name_taken to auto-recover, got error: %v", err)
	}
	if chID != "CEXISTING42" {
		t.Errorf("expected reused channel ID CEXISTING42, got %q", chID)
	}
	if !perSb {
		t.Error("perSandbox should still be true on the reuse path")
	}
	if !api.findChannelCalled {
		t.Error("FindChannelByName must be called when name_taken triggers with scan opt-in")
	}
	if !api.joinChannelCalled {
		t.Error("JoinChannel must be called after channel resolution to ensure bot membership")
	}
}

// TestResolveSlack_PerSandbox_NameTaken_ArchivedReservation verifies that
// name_taken + empty scan result (archived reservation) errors appropriately.
// Requires scan opt-in (SlackMaxScanPages>0) to exercise the empty-scan path.
func TestResolveSlack_PerSandbox_NameTaken_ArchivedReservation(t *testing.T) {
	old := SlackMaxScanPages
	SlackMaxScanPages = 100 // opt-in to scan
	defer func() { SlackMaxScanPages = old }()

	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{
		createChannelErr:  &slack.SlackAPIError{Method: "conversations.create", Code: "name_taken"},
		findChannelResult: "", // lookup returns no match — archived-reservation case
	}
	ssmStore := &fakeSSMParamStore{}

	_, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "demo", api, nil, ssmStore, "/km/")
	if err == nil {
		t.Fatal("expected error when name_taken AND lookup returns no match (archived reservation)")
	}
	if !strings.Contains(err.Error(), "archived") || !strings.Contains(err.Error(), "--alias") {
		t.Errorf("error should mention archived state and --alias remediation, got: %v", err)
	}
}

// TestResolveSlack_PerSandbox_NameTaken_UsesByNameCache verifies the O(1)
// reuse path: when the by-name SSM cache holds the channel ID and
// conversations.info (ChannelInfo) confirms it still resolves, the scan
// (FindChannelByName / conversations.list) is NEVER invoked.
func TestResolveSlack_PerSandbox_NameTaken_UsesByNameCache(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{
		createChannelErr: &slack.SlackAPIError{Method: "conversations.create", Code: "name_taken"},
		// channelInfoErr defaults to nil → cached ID validates successfully.
		// findChannelErr intentionally set so the test FAILS loudly if the scan runs.
		findChannelErr: fmt.Errorf("FindChannelByName must NOT be called on a cache hit"),
	}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{
			"/km/slack/invite-email":               "invite@example.com",
			"/km/slack/channel-id-by-name/sb-demo": "C0CACHED99",
		},
	}

	chID, perSb, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "demo", api, nil, ssmStore, "/km/")
	if err != nil {
		t.Fatalf("expected cache hit to succeed, got: %v", err)
	}
	if chID != "C0CACHED99" {
		t.Errorf("channelID = %q; want C0CACHED99 (from by-name cache)", chID)
	}
	if !perSb {
		t.Error("perSandbox should be true on the cache-reuse path")
	}
	if api.findChannelCalled {
		t.Error("FindChannelByName (conversations.list scan) must NOT be called on a cache hit")
	}
	if !api.channelInfoCalled {
		t.Error("ChannelInfo must be called to validate the cached ID")
	}
	if !api.joinChannelCalled {
		t.Error("JoinChannel must still run to ensure bot membership")
	}
}

// TestResolveSlack_PerSandbox_NameTaken_CacheMiss_FallsBackToScan verifies that
// with no stored mapping, name_taken recovery falls back to the scan when
// KM_SLACK_MAX_SCAN_PAGES>0 (scan opt-in). Phase 104: scan disabled by default.
func TestResolveSlack_PerSandbox_NameTaken_CacheMiss_FallsBackToScan(t *testing.T) {
	old := SlackMaxScanPages
	SlackMaxScanPages = 100 // opt-in to scan
	defer func() { SlackMaxScanPages = old }()

	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{
		createChannelErr:  &slack.SlackAPIError{Method: "conversations.create", Code: "name_taken"},
		findChannelResult: "CSCANNED",
	}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{"/km/slack/invite-email": "invite@example.com"},
	}

	chID, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "demo", api, nil, ssmStore, "/km/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chID != "CSCANNED" {
		t.Errorf("channelID = %q; want CSCANNED (scan fallback)", chID)
	}
	if !api.findChannelCalled {
		t.Error("FindChannelByName must be called on a cache miss with scan opt-in")
	}
	// The scan result should be written back to the cache for next time.
	cacheKey := "/km/slack/channel-id-by-name/sb-demo"
	if got := ssmStore.params[cacheKey]; got != "CSCANNED" {
		t.Errorf("scan result not cached: %s = %q; want CSCANNED", cacheKey, got)
	}
}

// TestResolveSlack_PerSandbox_NameTaken_StaleCache_FallsBackToScan verifies that
// a cached ID that definitively no longer exists (channel_not_found) causes the
// resolver to fall through to create and then scan when KM_SLACK_MAX_SCAN_PAGES>0.
// Phase 104: stored ID validated before create; gone → recreate attempt; name_taken
// again → scan (with opt-in).
func TestResolveSlack_PerSandbox_NameTaken_StaleCache_FallsBackToScan(t *testing.T) {
	old := SlackMaxScanPages
	SlackMaxScanPages = 100 // opt-in to scan
	defer func() { SlackMaxScanPages = old }()

	// Shrink retry delay so test doesn't pause.
	oldDelay := slackInfoRetryDelay
	slackInfoRetryDelay = time.Millisecond
	defer func() { slackInfoRetryDelay = oldDelay }()

	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{
		createChannelErr:  &slack.SlackAPIError{Method: "conversations.create", Code: "name_taken"},
		channelInfoErr:    &slack.SlackAPIError{Method: "conversations.info", Code: "channel_not_found"},
		findChannelResult: "CFRESH",
	}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{
			"/km/slack/invite-email":               "invite@example.com",
			"/km/slack/channel-id-by-name/sb-demo": "C0STALE",
		},
	}

	chID, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "demo", api, nil, ssmStore, "/km/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chID != "CFRESH" {
		t.Errorf("channelID = %q; want CFRESH (stale cache → recreate → scan)", chID)
	}
	if !api.channelInfoCalled {
		t.Error("ChannelInfo must be called to validate the stored ID")
	}
	if !api.findChannelCalled {
		t.Error("FindChannelByName must be called after the stored ID fails to resolve and create returns name_taken")
	}
}

// TestResolveSlack_PerSandbox_NameTaken_RateLimited_ErrorMessage verifies the
// error-message fix: when the scan is rate-limited (with scan opt-in), the error
// must mention rate limiting + channelOverride and must NOT advise granting channels:read.
func TestResolveSlack_PerSandbox_NameTaken_RateLimited_ErrorMessage(t *testing.T) {
	old := SlackMaxScanPages
	SlackMaxScanPages = 100 // opt-in to scan so the rate-limit error is exercised
	defer func() { SlackMaxScanPages = old }()

	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{
		createChannelErr: &slack.SlackAPIError{Method: "conversations.create", Code: "name_taken"},
		findChannelErr:   &slack.SlackAPIError{Method: "conversations.list", Code: "ratelimited"},
	}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{"/km/slack/invite-email": "invite@example.com"},
	}

	_, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "demo", api, nil, ssmStore, "/km/")
	if err == nil {
		t.Fatal("expected an error when the scan is rate-limited")
	}
	msg := err.Error()
	if strings.Contains(msg, "channels:read") {
		t.Errorf("rate-limit error must NOT mention channels:read; got: %v", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "rate") {
		t.Errorf("rate-limit error should mention rate limiting; got: %v", msg)
	}
	if !strings.Contains(msg, "channelOverride") {
		t.Errorf("rate-limit error should mention channelOverride; got: %v", msg)
	}
}

// TestResolveSlack_PerSandbox_CreateSuccess_CachesByName verifies a freshly
// created channel is written to the by-name cache for future O(1) reuse.
func TestResolveSlack_PerSandbox_CreateSuccess_CachesByName(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{createChannelResult: "CNEWLY"}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{"/km/slack/invite-email": "invite@example.com"},
	}

	_, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "demo", api, nil, ssmStore, "/km/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cacheKey := "/km/slack/channel-id-by-name/sb-demo"
	if got := ssmStore.params[cacheKey]; got != "CNEWLY" {
		t.Errorf("created channel not cached: %s = %q; want CNEWLY", cacheKey, got)
	}
}

// Renamed from TestResolveSlack_PerSandbox_InviteFails_Error — invite failure
// is now non-fatal so a healthy channel + bot doesn't get blocked by a
// transient cross-workspace invite glitch. Operator can re-invite manually.
func TestResolveSlack_PerSandbox_InviteFails_NonFatal(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), true, "")
	api := &fakeSlackAPI{
		createChannelResult: "CNEW",
		inviteSharedErr:     fmt.Errorf("invite failed"),
	}
	ssmStore := &fakeSSMParamStore{
		params: map[string]string{
			"/km/slack/invite-email": "invite@example.com",
		},
	}

	chID, perSb, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "demo", api, nil, ssmStore, "/km/")
	if err != nil {
		t.Fatalf("invite failure should be non-fatal, got error: %v", err)
	}
	if chID != "CNEW" {
		t.Errorf("expected channel ID CNEW (channel was created), got %q", chID)
	}
	if !perSb {
		t.Error("perSandbox should still be true even when invite fails")
	}
	if !api.inviteSharedCalled {
		t.Error("InviteShared should still be attempted (just non-fatal on failure)")
	}
}

func TestResolveSlack_Override_HappyPath_BotIsMember(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), false, "C0OVERRIDE")
	api := &fakeSlackAPI{
		channelInfoMember: true,
		channelInfoCount:  5,
	}
	ssmStore := &fakeSSMParamStore{}

	chID, perSb, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "", api, nil, ssmStore, "/km/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chID != "C0OVERRIDE" {
		t.Errorf("channelID = %q; want %q", chID, "C0OVERRIDE")
	}
	if perSb {
		t.Error("perSandbox = true; want false for override mode")
	}
	if api.createChannelName != "" {
		t.Error("CreateChannel should not be called for override mode")
	}
	if api.inviteSharedCalled {
		t.Error("InviteShared should not be called for override mode")
	}
}

func TestResolveSlack_Override_BotNotMember_Error(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), false, "C0OVERRIDE")
	api := &fakeSlackAPI{
		channelInfoMember: false,
		channelInfoCount:  3,
	}
	ssmStore := &fakeSSMParamStore{}

	_, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "", api, nil, ssmStore, "/km/")
	if err == nil {
		t.Fatal("expected error when bot is not a member")
	}
	if !strings.Contains(err.Error(), "not a member") {
		t.Errorf("error %q should mention 'not a member'", err.Error())
	}
}

func TestResolveSlack_Override_ChannelNotFound_Error(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), false, "C0NOTFOUND")
	api := &fakeSlackAPI{
		channelInfoErr: &slack.SlackAPIError{Method: "conversations.info", Code: "channel_not_found"},
	}
	ssmStore := &fakeSSMParamStore{}

	_, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "", api, nil, ssmStore, "/km/")
	if err == nil {
		t.Fatal("expected error when channel not found")
	}
	if !strings.Contains(err.Error(), "C0NOTFOUND") {
		t.Errorf("error %q should mention the channel ID", err.Error())
	}
}

// TestResolveSlack_InvalidOverrideID_Error verifies defense-in-depth regex check.
func TestResolveSlack_InvalidOverrideID_Error(t *testing.T) {
	p := profileWithSlack(boolPtrCreate(true), false, "not-a-channel-id")
	api := &fakeSlackAPI{}
	ssmStore := &fakeSSMParamStore{}

	_, _, err := resolveSlackChannel(context.Background(), p, "sb-abc123", "", api, nil, ssmStore, "/km/")
	if err == nil {
		t.Fatal("expected error for invalid channel ID format")
	}
}

// ─────────────────────────────────────────────────────────────────────────
// runStep11dInject tests — Phase 67 SSM Parameter Store path
//
// Replaces the prior SendCommand tests after the SCP-bypass refactor. The new
// step writes /km/sandbox/{id}/slack-channel-id and reads /km/slack/bridge-url
// from SSM Parameter Store; the sandbox bootstrap (pkg/compiler/userdata.go)
// is responsible for picking these up at boot.
// ─────────────────────────────────────────────────────────────────────────

type fakePutParam struct {
	calls []struct{ Name, Value string }
	err   error
}

func (f *fakePutParam) put(_ context.Context, name, value string) error {
	f.calls = append(f.calls, struct{ Name, Value string }{name, value})
	return f.err
}

func TestStep11d_NoBridgeURL_SkipsPut(t *testing.T) {
	store := &fakeSSMParamStore{params: map[string]string{}}
	put := &fakePutParam{}
	got := captureStderr(t, func() {
		runStep11dInject(context.Background(), store, put.put, "sb-test", "CTEST", 1, time.Microsecond, "/km/")
	})
	if !strings.Contains(got, "⚠ Slack: /km/slack/bridge-url not configured") {
		t.Errorf("stderr %q should contain bridge-url not-configured message", got)
	}
	if len(put.calls) != 0 {
		t.Errorf("expected no PutParameter calls when bridge URL is missing, got %d", len(put.calls))
	}
}

func TestStep11d_PutFailure_LogsWarn(t *testing.T) {
	store := &fakeSSMParamStore{params: map[string]string{"/km/slack/bridge-url": "https://bridge.example.com"}}
	put := &fakePutParam{err: errors.New("ssm putparam failed")}
	got := captureStderr(t, func() {
		runStep11dInject(context.Background(), store, put.put, "sb-test", "CTEST", 1, time.Microsecond, "/km/")
	})
	if !strings.Contains(got, "⚠ Slack: SSM PutParameter failed") {
		t.Errorf("stderr %q should contain SSM PutParameter failed warn", got)
	}
	if len(put.calls) != 1 {
		t.Errorf("expected exactly 1 PutParameter attempt, got %d", len(put.calls))
	}
}

func TestStep11d_Success_WritesChannelIDParam(t *testing.T) {
	store := &fakeSSMParamStore{params: map[string]string{"/km/slack/bridge-url": "https://bridge.example.com"}}
	put := &fakePutParam{}
	got := captureStderr(t, func() {
		runStep11dInject(context.Background(), store, put.put, "sb-test", "C-TEST", 1, time.Microsecond, "/km/")
	})
	if !strings.Contains(got, "✓ Slack: channel C-TEST published to SSM Parameter Store") {
		t.Errorf("stderr %q should announce successful publish", got)
	}
	if len(put.calls) != 1 {
		t.Fatalf("expected 1 PutParameter call, got %d", len(put.calls))
	}
	wantName := "/km/sandbox/sb-test/slack-channel-id"
	if put.calls[0].Name != wantName {
		t.Errorf("PutParameter Name: got %q, want %q", put.calls[0].Name, wantName)
	}
	if put.calls[0].Value != "C-TEST" {
		t.Errorf("PutParameter Value: got %q, want %q", put.calls[0].Value, "C-TEST")
	}
}

// ─── Phase 104: TestResolvePerSandbox_* — lookup-first budgeted resolver ─────

// StoredID_Live_NoScan: DDB store has ID + info() succeeds → O(1), no create, no scan.
func TestResolvePerSandbox_StoredID_Live_NoScan(t *testing.T) {
	api := &fakeSlackAPI{
		channelInfoErr:  nil, // info(cachedID) succeeds
		findShouldPanic: true,
	}
	store := &fakeChannelStore{m: map[string]string{"github-bot": "C0LIVE"}}
	id, per, err := resolvePerSandboxChannelForTest(t, api, store, "github-bot", "sb-github-bot")
	if err != nil || id != "C0LIVE" || !per {
		t.Fatalf("want C0LIVE/true/nil, got %q/%v/%v", id, per, err)
	}
	if api.createCalls != 0 {
		t.Fatalf("stored-live path must not call conversations.create, got %d", api.createCalls)
	}
}

// StoredID_TransientInfo_NoScan: transient ratelimited → bounded-retry then optimistic-use, NO scan.
func TestResolvePerSandbox_StoredID_TransientInfo_NoScan(t *testing.T) {
	api := &fakeSlackAPI{
		channelInfoErr:  &slack.SlackAPIError{Method: "conversations.info", Code: "ratelimited"},
		findShouldPanic: true, // must never enumerate
	}
	store := &fakeChannelStore{m: map[string]string{"github-bot": "C0OPT"}}
	id, _, err := resolvePerSandboxChannelForTest(t, api, store, "github-bot", "sb-github-bot")
	if err != nil || id != "C0OPT" {
		t.Fatalf("transient info must optimistically use stored ID, got %q/%v", id, err)
	}
}

// StoredID_NotFound_Recreates: definitive channel_not_found → invalidate + recreate.
func TestResolvePerSandbox_StoredID_NotFound_Recreates(t *testing.T) {
	api := &fakeSlackAPI{
		channelInfoErr:      &slack.SlackAPIError{Method: "conversations.info", Code: "channel_not_found"},
		createChannelResult: "C0NEW",
	}
	store := &fakeChannelStore{m: map[string]string{"github-bot": "C0DEAD"}}
	id, _, err := resolvePerSandboxChannelForTest(t, api, store, "github-bot", "sb-github-bot")
	if err != nil || id != "C0NEW" {
		t.Fatalf("dead stored ID must recreate, got %q/%v", id, err)
	}
	if store.m["github-bot"] != "C0NEW" {
		t.Fatalf("mapping must be rewritten to C0NEW, got %q", store.m["github-bot"])
	}
}

// NameTaken_NoMapping_FailFast: no stored mapping + create→name_taken + scan disabled → fail fast.
func TestResolvePerSandbox_NameTaken_NoMapping_FailFast(t *testing.T) {
	old := SlackMaxScanPages
	SlackMaxScanPages = 0
	defer func() { SlackMaxScanPages = old }()
	api := &fakeSlackAPI{
		createChannelErr: &slack.SlackAPIError{Method: "conversations.create", Code: "name_taken"},
		findShouldPanic:  true, // scan disabled ⇒ never called
	}
	store := &fakeChannelStore{m: map[string]string{}}
	_, _, err := resolvePerSandboxChannelForTest(t, api, store, "github-bot", "sb-github-bot")
	if err == nil || !strings.Contains(err.Error(), "km slack adopt") {
		t.Fatalf("want fail-fast adopt guidance, got %v", err)
	}
}

// FreshCreate_WritesStore: no stored mapping, name free → create + write-through to store.
func TestResolvePerSandbox_FreshCreate_WritesStore(t *testing.T) {
	api := &fakeSlackAPI{createChannelResult: "C0FRESH"}
	store := &fakeChannelStore{m: map[string]string{}}
	id, _, err := resolvePerSandboxChannelForTest(t, api, store, "github-bot", "sb-github-bot")
	if err != nil || id != "C0FRESH" {
		t.Fatalf("want C0FRESH, got %q/%v", id, err)
	}
	if store.m["github-bot"] != "C0FRESH" {
		t.Fatalf("fresh create must write store, got %q", store.m["github-bot"])
	}
}

// StoredID_SSMOnly_BackfillsDDB: DDB store empty, SSM by-name cache has ID, channel live →
// resolves O(1) AND migrates the mapping into the DDB store on first touch.
func TestResolvePerSandbox_StoredID_SSMOnly_BackfillsDDB(t *testing.T) {
	api := &fakeSlackAPI{
		channelInfoErr:  nil, // info(SSM-sourced ID) succeeds
		findShouldPanic: true,
	}
	store := &fakeChannelStore{m: map[string]string{}} // DDB empty
	id, _, err := resolvePerSandboxChannelForTestWithSSM(t, api, store,
		"github-bot", "sb-github-bot", map[string]string{"sb-github-bot": "C0SSM"})
	if err != nil || id != "C0SSM" {
		t.Fatalf("SSM-sourced hit must resolve O(1), got %q/%v", id, err)
	}
	if store.m["github-bot"] != "C0SSM" {
		t.Fatalf("SSM-sourced hit must back-fill the DDB store, got %q", store.m["github-bot"])
	}
	if api.createCalls != 0 {
		t.Fatalf("back-fill path must not create a channel, got %d", api.createCalls)
	}
}
