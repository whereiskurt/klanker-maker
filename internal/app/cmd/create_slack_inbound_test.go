package cmd

// create_slack_inbound_test.go — Phase 67 Plan 06 tests
//
// Exercises provisionSlackInboundQueue via local mocks — no real AWS connection.
// Covers: happy path, disabled no-op, DDB persist failure, SSM inject failure.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// ============================================================
// Mocks
// ============================================================

// fakeSQS is an in-memory implementation of awspkg.SQSClient for tests.
type fakeSQS struct {
	createCalled int
	createName   string
	createAttrs  map[string]string
	createErr    error

	deleteCalled int
	deleteURL    string
	deleteErr    error

	// getAttrsErr controls the error returned by GetQueueAttributes (for doctor checks).
	getAttrsErr error
	// listResult controls the queue URLs returned by ListQueues (for doctor checks).
	listResult []string
	// depthByName, when non-nil, lets a test set a per-queue
	// ApproximateNumberOfMessages keyed by the trailing queue-name segment of the
	// GetQueueAttributes QueueUrl (used by the inbound-DLQ depth doctor check to
	// distinguish the OK (all-zero) and WARN (non-empty) branches). Absent ⇒ depth 0.
	depthByName map[string]string
	// listByPrefix, when true, filters listResult so only URLs whose trailing
	// queue-name segment carries the requested QueueNamePrefix are returned —
	// lets a single fakeSQS resolve distinct github/slack DLQ URLs by prefix.
	listByPrefix bool
}

func (f *fakeSQS) CreateQueue(_ context.Context, in *sqs.CreateQueueInput, _ ...func(*sqs.Options)) (*sqs.CreateQueueOutput, error) {
	f.createCalled++
	if in.QueueName != nil {
		f.createName = *in.QueueName
	}
	f.createAttrs = in.Attributes
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &sqs.CreateQueueOutput{
		QueueUrl: awssdk.String("https://sqs.us-east-1.amazonaws.com/123456789012/" + *in.QueueName),
	}, nil
}

func (f *fakeSQS) DeleteQueue(_ context.Context, in *sqs.DeleteQueueInput, _ ...func(*sqs.Options)) (*sqs.DeleteQueueOutput, error) {
	f.deleteCalled++
	if in.QueueUrl != nil {
		f.deleteURL = *in.QueueUrl
	}
	return &sqs.DeleteQueueOutput{}, f.deleteErr
}

func (f *fakeSQS) GetQueueAttributes(_ context.Context, in *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	if f.getAttrsErr != nil {
		return nil, f.getAttrsErr
	}
	depth := "0"
	if f.depthByName != nil && in != nil && in.QueueUrl != nil {
		url := *in.QueueUrl
		name := url
		if i := strings.LastIndex(url, "/"); i >= 0 {
			name = url[i+1:]
		}
		if v, ok := f.depthByName[name]; ok {
			depth = v
		}
	}
	return &sqs.GetQueueAttributesOutput{
		Attributes: map[string]string{"ApproximateNumberOfMessages": depth},
	}, nil
}

func (f *fakeSQS) ListQueues(_ context.Context, in *sqs.ListQueuesInput, _ ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error) {
	if !f.listByPrefix || in == nil || in.QueueNamePrefix == nil || *in.QueueNamePrefix == "" {
		return &sqs.ListQueuesOutput{QueueUrls: f.listResult}, nil
	}
	prefix := *in.QueueNamePrefix
	var matched []string
	for _, u := range f.listResult {
		name := u
		if i := strings.LastIndex(u, "/"); i >= 0 {
			name = u[i+1:]
		}
		if strings.HasPrefix(name, prefix) {
			matched = append(matched, u)
		}
	}
	return &sqs.ListQueuesOutput{QueueUrls: matched}, nil
}

// ============================================================
// Test helpers
// ============================================================

// testState captures DDB attribute writes and SSM Parameter Store writes.
type testState struct {
	ddbAttrs  map[string]string // attr → value
	ssmParams map[string]string // parameter name → value
}

// makeDeps builds a slackInboundDeps wired to the given fakeSQS and error
// controls. inboundEnabled controls NotifySlackInboundEnabled on the profile.
func makeDeps(t *testing.T, inboundEnabled bool, fSQS *fakeSQS,
	ddbErr, ssmErr error) (slackInboundDeps, *testState) {
	t.Helper()

	state := &testState{
		ddbAttrs:  make(map[string]string),
		ssmParams: make(map[string]string),
	}

	t.Helper()
	p := &profile.SandboxProfile{}
	p.Spec.Notification = &profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Inbound: &profile.NotificationSlackInboundSpec{Enabled: &inboundEnabled},
		},
	}

	return slackInboundDeps{
		Profile:   p,
		Cfg:       &config.Config{ResourcePrefix: "km"},
		SandboxID: "sb-abc123",
		SQS:       fSQS,
		UpdateSandboxAttr: func(_ context.Context, _, attr, val string) error {
			if ddbErr != nil {
				return ddbErr
			}
			state.ddbAttrs[attr] = val
			return nil
		},
		PutSSMParameter: func(_ context.Context, name, val string) error {
			if ssmErr != nil {
				return ssmErr
			}
			state.ssmParams[name] = val
			return nil
		},
	}, state
}

// ============================================================
// Tests
// ============================================================

// TestCreate_SlackInboundQueueProvisioned verifies the happy path:
// - profile has notifySlackInboundEnabled=true
// - CreateQueue is called exactly once with correct FIFO attributes
// - DDB attr slack_inbound_queue_url is written with the returned URL
// - SSM parameter /sandbox/{id}/slack-inbound-queue-url is written with the same URL
// - provisionSlackInboundQueue returns the non-empty queue URL
func TestCreate_SlackInboundQueueProvisioned(t *testing.T) {
	fs := &fakeSQS{}
	deps, state := makeDeps(t, true, fs, nil, nil)

	url, err := provisionSlackInboundQueue(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url == "" {
		t.Fatal("expected non-empty queue URL on success")
	}
	if fs.createCalled != 1 {
		t.Fatalf("expected 1 CreateQueue call, got %d", fs.createCalled)
	}
	// Queue name must follow {prefix}-slack-inbound-{sandbox-id}.fifo
	expectedName := "km-slack-inbound-sb-abc123.fifo"
	if fs.createName != expectedName {
		t.Fatalf("queue name: got %q, want %q", fs.createName, expectedName)
	}
	// Verify CONTEXT.md-mandated FIFO attributes
	if got := fs.createAttrs["FifoQueue"]; got != "true" {
		t.Errorf("FifoQueue attr: got %q, want %q", got, "true")
	}
	if got := fs.createAttrs["ContentBasedDeduplication"]; got != "false" {
		t.Errorf("ContentBasedDeduplication attr: got %q, want %q", got, "false")
	}
	// Phase 119: base VisibilityTimeout raised from 30s to 1800s so a long-running
	// concurrent agent turn is not redelivered mid-flight (the on-box poller also
	// heartbeats ChangeMessageVisibility for existing 30s queues).
	if got := fs.createAttrs["VisibilityTimeout"]; got != "1800" {
		t.Errorf("VisibilityTimeout attr: got %q, want %q", got, "1800")
	}
	if got := fs.createAttrs["MessageRetentionPeriod"]; got != "1209600" {
		t.Errorf("MessageRetentionPeriod attr: got %q, want %q", got, "1209600")
	}
	// DDB must have the queue URL persisted
	if got := state.ddbAttrs["slack_inbound_queue_url"]; got != url {
		t.Fatalf("DDB slack_inbound_queue_url: got %q, want %q", got, url)
	}
	// SSM Parameter Store must have the queue URL written under the
	// /{prefix}/sandbox/{id}/slack-inbound-queue-url path so the sandbox
	// poller can read it on boot. Pre-prefix-migration this assertion was
	// /sandbox/{id}/... (no prefix); commit 26dd788 scoped the path under
	// /{prefix}/ but this test wasn't updated.
	expectedParam := "/km/sandbox/sb-abc123/slack-inbound-queue-url"
	if got := state.ssmParams[expectedParam]; got != url {
		t.Fatalf("SSM param %s: got %q, want %q", expectedParam, got, url)
	}
}

// TestCreate_SlackInboundEnvVarInjection verifies the no-op path:
// - profile has notifySlackInboundEnabled=false
// - provisionSlackInboundQueue returns ("", nil)
// - zero SQS API calls
// - zero DDB or SSM mutations
func TestCreate_SlackInboundEnvVarInjection(t *testing.T) {
	fs := &fakeSQS{}
	deps, state := makeDeps(t, false /* inbound off */, fs, nil, nil)

	url, err := provisionSlackInboundQueue(context.Background(), deps)
	if err != nil {
		t.Fatalf("inbound off: unexpected error: %v", err)
	}
	if url != "" {
		t.Fatalf("inbound off: expected empty URL, got %q", url)
	}
	if fs.createCalled != 0 {
		t.Fatalf("inbound off: expected 0 SQS calls, got %d", fs.createCalled)
	}
	if len(state.ddbAttrs) != 0 {
		t.Fatalf("inbound off: expected 0 DDB writes, got %v", state.ddbAttrs)
	}
	if len(state.ssmParams) != 0 {
		t.Fatalf("inbound off: expected 0 SSM parameter writes, got %v", state.ssmParams)
	}
}

// TestCreate_SlackInboundQueueRollback verifies SSM Parameter Store write
// failure triggers rollback:
// - CreateQueue succeeds (1 call)
// - DDB UpdateAttr succeeds
// - PutSSMParameter fails
// - DeleteQueue is called exactly once (best-effort rollback)
// - provisionSlackInboundQueue returns an error with empty URL
func TestCreate_SlackInboundQueueRollback(t *testing.T) {
	fs := &fakeSQS{}
	ssmErr := errors.New("ssm put-parameter timeout")
	deps, _ := makeDeps(t, true, fs, nil, ssmErr)

	url, err := provisionSlackInboundQueue(context.Background(), deps)
	if err == nil {
		t.Fatal("expected error from SSM Parameter Store write failure")
	}
	if url != "" {
		t.Fatalf("expected empty URL on failure, got %q", url)
	}
	if fs.createCalled != 1 {
		t.Fatalf("expected 1 CreateQueue call before failure, got %d", fs.createCalled)
	}
	if fs.deleteCalled != 1 {
		t.Fatalf("expected 1 DeleteQueue rollback call, got %d", fs.deleteCalled)
	}
}

// TestCreate_SlackInboundDDBPersistFailure verifies DDB write failure triggers rollback:
// - CreateQueue succeeds (1 call)
// - UpdateSandboxAttr fails
// - DeleteQueue is called exactly once (rollback delete)
// - provisionSlackInboundQueue returns a wrapped error
func TestCreate_SlackInboundDDBPersistFailure(t *testing.T) {
	fs := &fakeSQS{}
	ddbErr := errors.New("ddb conditional check failed")
	deps, _ := makeDeps(t, true, fs, ddbErr, nil)

	_, err := provisionSlackInboundQueue(context.Background(), deps)
	if err == nil {
		t.Fatal("expected error from DDB write failure")
	}
	if fs.createCalled != 1 {
		t.Fatalf("expected 1 CreateQueue before DDB failure, got %d", fs.createCalled)
	}
	if fs.deleteCalled != 1 {
		t.Fatalf("expected 1 DeleteQueue rollback call after DDB failure, got %d", fs.deleteCalled)
	}
}

// ============================================================
// Phase 67-07 tests — postReadyAnnouncement
// ============================================================

// profileWithInbound builds a minimal SandboxProfile with the given
// notifySlackInboundEnabled value. Used by the ready-announcement tests.
func profileWithInbound(on bool) *profile.SandboxProfile {
	p := &profile.SandboxProfile{}
	p.Spec.Notification = &profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Inbound: &profile.NotificationSlackInboundSpec{Enabled: &on},
		},
	}
	return p
}

// TestCreate_SlackInboundReadyAnnouncement verifies the happy path:
//   - PostOperatorSigned is called exactly once with the correct channel
//   - Body contains the sandbox ID
//   - UpsertSlackThread is called with the returned ts
func TestCreate_SlackInboundReadyAnnouncement(t *testing.T) {
	type postRecord struct{ ch, body string }
	type upsertRecord struct{ ch, ts, sb string }
	var posted []postRecord
	var upserted []upsertRecord
	deps := slackInboundDeps{
		Profile:   profileWithInbound(true),
		Cfg:       &config.Config{ResourcePrefix: "km", PrimaryRegion: "us-east-1"},
		SandboxID: "sb-abc123",
		PostOperatorSigned: func(ctx context.Context, ch, body string) (string, error) {
			posted = append(posted, postRecord{ch, body})
			return "1714280400.001", nil
		},
		UpsertSlackThread: func(ctx context.Context, ch, ts, sb string) error {
			upserted = append(upserted, upsertRecord{ch, ts, sb})
			return nil
		},
	}
	if err := postReadyAnnouncement(context.Background(), deps, "C1"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(posted) != 1 || posted[0].ch != "C1" {
		t.Fatalf("posted: %+v", posted)
	}
	if !strings.Contains(posted[0].body, "sb-abc123") {
		t.Fatalf("body missing sandbox id: %q", posted[0].body)
	}
	if len(upserted) != 1 || upserted[0].ts != "1714280400.001" {
		t.Fatalf("upsert: %+v", upserted)
	}
}

// TestCreate_SlackInboundReadyAnnouncement_Disabled verifies that when
// notifySlackInboundEnabled is false, postReadyAnnouncement is a no-op.
func TestCreate_SlackInboundReadyAnnouncement_Disabled(t *testing.T) {
	deps := slackInboundDeps{Profile: profileWithInbound(false)}
	if err := postReadyAnnouncement(context.Background(), deps, "C1"); err != nil {
		t.Fatalf("disabled inbound should be silent no-op, got %v", err)
	}
}

// TestCreate_SlackInboundReadyAnnouncement_PostFailureNonFatal verifies
// that a bridge post failure does NOT bubble up as an error.
func TestCreate_SlackInboundReadyAnnouncement_PostFailureNonFatal(t *testing.T) {
	deps := slackInboundDeps{
		Profile:   profileWithInbound(true),
		Cfg:       &config.Config{ResourcePrefix: "km"},
		SandboxID: "sb-abc",
		PostOperatorSigned: func(ctx context.Context, ch, body string) (string, error) {
			return "", errors.New("bridge unavailable")
		},
		UpsertSlackThread: func(ctx context.Context, ch, ts, sb string) error { return nil },
	}
	if err := postReadyAnnouncement(context.Background(), deps, "C1"); err != nil {
		t.Fatalf("post failure must not bubble up: got %v", err)
	}
}

// TestCreate_SlackInboundReactAlwaysOverride — Phase 91.5. When the profile
// sets notification.slack.inbound.reactAlways explicitly, provisionSlackInboundQueue
// MUST write slack_react_always="true"|"false" to the km-sandboxes row. When
// the field is nil (omitted), the attribute MUST NOT be written so the bridge
// falls back to the install-level KM_SLACK_REACT_ALWAYS default.
func TestCreate_SlackInboundReactAlwaysOverride(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	tests := []struct {
		name      string
		reactAlw  *bool
		wantAttr  bool   // true = expect write
		wantValue string // when wantAttr=true
	}{
		{"nil → no write (install default applies)", nil, false, ""},
		{"&true → writes slack_react_always=true", boolPtr(true), true, "true"},
		{"&false → writes slack_react_always=false", boolPtr(false), true, "false"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeSQS{}
			deps, state := makeDeps(t, true, fs, nil, nil)
			deps.Profile.Spec.Notification.Slack.Inbound.ReactAlways = tc.reactAlw

			if _, err := provisionSlackInboundQueue(context.Background(), deps); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, present := state.ddbAttrs["slack_react_always"]
			if tc.wantAttr && !present {
				t.Fatalf("expected slack_react_always write; attrs=%v", state.ddbAttrs)
			}
			if !tc.wantAttr && present {
				t.Fatalf("expected NO slack_react_always write (install default); got %q", got)
			}
			if tc.wantAttr && got != tc.wantValue {
				t.Errorf("slack_react_always: got %q want %q", got, tc.wantValue)
			}
		})
	}
}

// TestCreate_SlackInboundMentionOnlyOverride — per-sandbox mention_only wiring.
// When the profile sets notification.slack.inbound.mentionOnly explicitly,
// provisionSlackInboundQueue MUST write slack_mention_only="true"|"false" to the
// km-sandboxes row; when nil the attribute MUST NOT be written so the bridge
// falls back to the install-level KM_SLACK_MENTION_ONLY default.
func TestCreate_SlackInboundMentionOnlyOverride(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	tests := []struct {
		name      string
		mentionMO *bool
		wantAttr  bool
		wantValue string
	}{
		{"nil → no write (install default applies)", nil, false, ""},
		{"&true → writes slack_mention_only=true", boolPtr(true), true, "true"},
		{"&false → writes slack_mention_only=false", boolPtr(false), true, "false"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeSQS{}
			deps, state := makeDeps(t, true, fs, nil, nil)
			deps.Profile.Spec.Notification.Slack.Inbound.MentionOnly = tc.mentionMO

			if _, err := provisionSlackInboundQueue(context.Background(), deps); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, present := state.ddbAttrs["slack_mention_only"]
			if tc.wantAttr && !present {
				t.Fatalf("expected slack_mention_only write; attrs=%v", state.ddbAttrs)
			}
			if !tc.wantAttr && present {
				t.Fatalf("expected NO slack_mention_only write (install default); got %q", got)
			}
			if tc.wantAttr && got != tc.wantValue {
				t.Errorf("slack_mention_only: got %q want %q", got, tc.wantValue)
			}
		})
	}
}

// TestCreate_SlackInboundAllowOverride — Phase 118. When the profile sets a
// non-empty notification.slack.inbound.allow, provisionSlackInboundQueue MUST
// write the per-sandbox allow list as a comma-joined "slack_allow" S attribute
// on the km-sandboxes row. When the list is nil/empty the attribute MUST NOT be
// written so the bridge falls back to the install-level KM_SLACK_ALLOW default.
func TestCreate_SlackInboundAllowOverride(t *testing.T) {
	tests := []struct {
		name      string
		allow     []string
		wantAttr  bool
		wantValue string
	}{
		{"nil → no write (install default applies)", nil, false, ""},
		{"empty → no write (install default applies)", []string{}, false, ""},
		{"single → writes slack_allow=U1", []string{"U1AAA"}, true, "U1AAA"},
		{"multi → writes comma-joined", []string{"U1AAA", "U2BBB"}, true, "U1AAA,U2BBB"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeSQS{}
			deps, state := makeDeps(t, true, fs, nil, nil)
			deps.Profile.Spec.Notification.Slack.Inbound.Allow = tc.allow

			if _, err := provisionSlackInboundQueue(context.Background(), deps); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, present := state.ddbAttrs["slack_allow"]
			if tc.wantAttr && !present {
				t.Fatalf("expected slack_allow write; attrs=%v", state.ddbAttrs)
			}
			if !tc.wantAttr && present {
				t.Fatalf("expected NO slack_allow write (install default); got %q", got)
			}
			if tc.wantAttr && got != tc.wantValue {
				t.Errorf("slack_allow: got %q want %q", got, tc.wantValue)
			}
		})
	}
}

// ============================================================
// Phase 99.1 Plan 02 — DLQ-ARN threading + teardown guard
// ============================================================

const testSlackDLQArn = "arn:aws:sqs:us-east-1:123456789012:km-slack-inbound-dlq.fifo"

// TestCreate_SlackInboundQueueWithDLQ verifies that a non-empty DLQArn on the
// deps struct injects a RedrivePolicy (maxReceiveCount=3 + the exact
// deadLetterTargetArn) into the CreateQueue Attributes map.
func TestCreate_SlackInboundQueueWithDLQ(t *testing.T) {
	fs := &fakeSQS{}
	deps, _ := makeDeps(t, true, fs, nil, nil)
	deps.DLQArn = testSlackDLQArn

	if _, err := provisionSlackInboundQueue(context.Background(), deps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rp, ok := fs.createAttrs["RedrivePolicy"]
	if !ok {
		t.Fatalf("expected RedrivePolicy attribute; attrs=%v", fs.createAttrs)
	}
	var got struct {
		DeadLetterTargetArn string `json:"deadLetterTargetArn"`
		MaxReceiveCount     int    `json:"maxReceiveCount"`
	}
	if err := json.Unmarshal([]byte(rp), &got); err != nil {
		t.Fatalf("RedrivePolicy is not valid JSON: %v (%q)", err, rp)
	}
	if got.MaxReceiveCount != 3 {
		t.Errorf("maxReceiveCount: got %d, want 3", got.MaxReceiveCount)
	}
	if got.DeadLetterTargetArn != testSlackDLQArn {
		t.Errorf("deadLetterTargetArn: got %q, want %q", got.DeadLetterTargetArn, testSlackDLQArn)
	}
}

// TestCreate_SlackInboundQueueNoDLQ verifies that an empty DLQArn leaves NO
// RedrivePolicy key (dormancy invariant — byte-identical to pre-99.1).
func TestCreate_SlackInboundQueueNoDLQ(t *testing.T) {
	fs := &fakeSQS{}
	deps, _ := makeDeps(t, true, fs, nil, nil)
	deps.DLQArn = "" // explicit: no shared DLQ resolvable

	if _, err := provisionSlackInboundQueue(context.Background(), deps); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := fs.createAttrs["RedrivePolicy"]; ok {
		t.Fatalf("expected NO RedrivePolicy when DLQArn empty (dormancy); attrs=%v", fs.createAttrs)
	}
}

// TestDrainSlackInbound_NoSharedDLQDelete verifies km destroy deletes ONLY the
// per-sandbox source queue and never a *-dlq.fifo (shared DLQ is install-scoped).
func TestDrainSlackInbound_NoSharedDLQDelete(t *testing.T) {
	fs := &fakeSQS{}
	sourceURL := "https://sqs.us-east-1.amazonaws.com/123456789012/km-slack-inbound-sb-abc123.fifo"
	deps := destroyInboundDeps{
		SandboxID:      "sb-abc123",
		ResourcePrefix: "km",
		QueueURL:       sourceURL,
		SQS:            fs,
	}
	drainSlackInbound(context.Background(), deps)

	if fs.deleteCalled != 1 {
		t.Fatalf("expected exactly 1 DeleteQueue (source only), got %d", fs.deleteCalled)
	}
	if fs.deleteURL != sourceURL {
		t.Fatalf("deleted queue URL: got %q, want per-sandbox source %q", fs.deleteURL, sourceURL)
	}
	if strings.Contains(fs.deleteURL, "-dlq.fifo") {
		t.Fatalf("km destroy deleted the shared DLQ %q — it must be install-scoped", fs.deleteURL)
	}
}
