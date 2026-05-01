// Package cmd — destroy_slack_test.go
// Unit tests for destroySlackChannel covering the 9-case behavior matrix
// from Plan 63-09.
package cmd

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/slack"
)

// ---- Mocks ----
// Note: fakeSSMParamStore is declared in create_slack_test.go (same package).

// fakeBridgePosterState tracks calls made through the bridge poster.
type fakeBridgePosterState struct {
	calls    []string // action per call
	finalErr error
	archErr  error
	finalOK  bool
	archOK   bool
}

func (f *fakeBridgePosterState) post(ctx context.Context, url string, env *slack.SlackEnvelope, sig []byte) (*slack.PostResponse, error) {
	f.calls = append(f.calls, env.Action)
	switch env.Action {
	case slack.ActionPost:
		return &slack.PostResponse{OK: f.finalOK}, f.finalErr
	case slack.ActionArchive:
		return &slack.PostResponse{OK: f.archOK}, f.archErr
	}
	return &slack.PostResponse{OK: true}, nil
}

// genTestKey creates a fresh Ed25519 private key for tests.
func genTestKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return priv
}

// ---- Test helper builders ----

func metaWithSlack(channelID string, perSandbox bool, archiveOnDestroy *bool) *kmaws.SandboxMetadata {
	return &kmaws.SandboxMetadata{
		SandboxID:             "sb-test01",
		SlackChannelID:        channelID,
		SlackPerSandbox:       perSandbox,
		SlackArchiveOnDestroy: archiveOnDestroy,
	}
}

// ---- Test cases ----

// Case A — SlackChannelID is empty: no SSM reads, no Slack calls, nil returned.
func TestDestroySlackChannel_CaseA_NoSlack(t *testing.T) {
	fp := &fakeBridgePosterState{}
	ssm := &fakeSSMParamStore{params: map[string]string{
		"/km/slack/bridge-url": "https://bridge.example.com",
	}}
	priv := genTestKey(t)
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) { return priv, nil }

	m := metaWithSlack("", false, nil)
	err := destroySlackChannel(context.Background(), m, "us-east-1", ssm, keyLoader, fp.post)
	if err != nil {
		t.Errorf("case A: expected nil error, got %v", err)
	}
	if len(fp.calls) != 0 {
		t.Errorf("case A: expected 0 bridge calls, got %d", len(fp.calls))
	}
}

// Case B — SlackPerSandbox=false (shared channel): no Slack calls, nil returned.
func TestDestroySlackChannel_CaseB_SharedMode(t *testing.T) {
	fp := &fakeBridgePosterState{}
	ssm := &fakeSSMParamStore{params: map[string]string{
		"/km/slack/bridge-url": "https://bridge.example.com",
	}}
	priv := genTestKey(t)
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) { return priv, nil }

	m := metaWithSlack("C0SHARED", false, nil)
	err := destroySlackChannel(context.Background(), m, "us-east-1", ssm, keyLoader, fp.post)
	if err != nil {
		t.Errorf("case B: expected nil error, got %v", err)
	}
	if len(fp.calls) != 0 {
		t.Errorf("case B: expected 0 bridge calls, got %d", len(fp.calls))
	}
}

// Case C — per-sandbox + archive (default nil): 2 POSTs (final + archive), nil returned.
func TestDestroySlackChannel_CaseC_ArchiveDefault(t *testing.T) {
	fp := &fakeBridgePosterState{finalOK: true, archOK: true}
	ssm := &fakeSSMParamStore{params: map[string]string{
		"/km/slack/bridge-url": "https://bridge.example.com",
	}}
	priv := genTestKey(t)
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) { return priv, nil }

	m := metaWithSlack("C0PERSB", true, nil) // nil = default archive
	err := destroySlackChannel(context.Background(), m, "us-east-1", ssm, keyLoader, fp.post)
	if err != nil {
		t.Errorf("case C: expected nil error, got %v", err)
	}
	if len(fp.calls) != 2 {
		t.Errorf("case C: expected 2 bridge calls (post+archive), got %d: %v", len(fp.calls), fp.calls)
	}
	if fp.calls[0] != slack.ActionPost {
		t.Errorf("case C: first call must be %q, got %q", slack.ActionPost, fp.calls[0])
	}
	if fp.calls[1] != slack.ActionArchive {
		t.Errorf("case C: second call must be %q, got %q", slack.ActionArchive, fp.calls[1])
	}
}

// Case D — per-sandbox + archive explicit true: 2 POSTs.
func TestDestroySlackChannel_CaseD_ArchiveExplicitTrue(t *testing.T) {
	fp := &fakeBridgePosterState{finalOK: true, archOK: true}
	ssm := &fakeSSMParamStore{params: map[string]string{
		"/km/slack/bridge-url": "https://bridge.example.com",
	}}
	priv := genTestKey(t)
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) { return priv, nil }

	tru := true
	m := metaWithSlack("C0PERSB", true, &tru)
	err := destroySlackChannel(context.Background(), m, "us-east-1", ssm, keyLoader, fp.post)
	if err != nil {
		t.Errorf("case D: expected nil error, got %v", err)
	}
	if len(fp.calls) != 2 {
		t.Errorf("case D: expected 2 bridge calls, got %d: %v", len(fp.calls), fp.calls)
	}
}

// Case E — per-sandbox + archive=false: 1 POST (final only), nil returned.
func TestDestroySlackChannel_CaseE_NoArchive(t *testing.T) {
	fp := &fakeBridgePosterState{finalOK: true}
	ssm := &fakeSSMParamStore{params: map[string]string{
		"/km/slack/bridge-url": "https://bridge.example.com",
	}}
	priv := genTestKey(t)
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) { return priv, nil }

	fls := false
	m := metaWithSlack("C0PERSB", true, &fls)
	err := destroySlackChannel(context.Background(), m, "us-east-1", ssm, keyLoader, fp.post)
	if err != nil {
		t.Errorf("case E: expected nil error, got %v", err)
	}
	if len(fp.calls) != 1 {
		t.Errorf("case E: expected 1 bridge call (final post only), got %d: %v", len(fp.calls), fp.calls)
	}
	if fp.calls[0] != slack.ActionPost {
		t.Errorf("case E: call must be %q, got %q", slack.ActionPost, fp.calls[0])
	}
}

// Case F — bridge-url SSM unset: 0 bridge POSTs, nil returned (WARN logged).
func TestDestroySlackChannel_CaseF_NoBridgeURL(t *testing.T) {
	fp := &fakeBridgePosterState{}
	ssm := &fakeSSMParamStore{params: map[string]string{}} // no bridge-url
	priv := genTestKey(t)
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) { return priv, nil }

	m := metaWithSlack("C0PERSB", true, nil)
	err := destroySlackChannel(context.Background(), m, "us-east-1", ssm, keyLoader, fp.post)
	if err != nil {
		t.Errorf("case F: expected nil error, got %v", err)
	}
	if len(fp.calls) != 0 {
		t.Errorf("case F: expected 0 bridge calls, got %d", len(fp.calls))
	}
}

// Case G — operator key load fails: 0 bridge POSTs, nil returned (WARN logged).
func TestDestroySlackChannel_CaseG_KeyLoadFail(t *testing.T) {
	fp := &fakeBridgePosterState{}
	ssm := &fakeSSMParamStore{params: map[string]string{
		"/km/slack/bridge-url": "https://bridge.example.com",
	}}
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) {
		return nil, errors.New("SSM unavailable")
	}

	m := metaWithSlack("C0PERSB", true, nil)
	err := destroySlackChannel(context.Background(), m, "us-east-1", ssm, keyLoader, fp.post)
	if err != nil {
		t.Errorf("case G: expected nil error, got %v", err)
	}
	if len(fp.calls) != 0 {
		t.Errorf("case G: expected 0 bridge calls, got %d", len(fp.calls))
	}
}

// Case H — bridge final-post returns 502 (err path): archive NOT attempted, nil returned.
func TestDestroySlackChannel_CaseH_FinalPostFails(t *testing.T) {
	fp := &fakeBridgePosterState{
		finalOK:  false,
		finalErr: errors.New("502 Bad Gateway"),
	}
	ssm := &fakeSSMParamStore{params: map[string]string{
		"/km/slack/bridge-url": "https://bridge.example.com",
	}}
	priv := genTestKey(t)
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) { return priv, nil }

	m := metaWithSlack("C0PERSB", true, nil)
	err := destroySlackChannel(context.Background(), m, "us-east-1", ssm, keyLoader, fp.post)
	if err != nil {
		t.Errorf("case H: expected nil error, got %v", err)
	}
	// Only the final-post attempt was made; archive was NOT attempted.
	if len(fp.calls) != 1 {
		t.Errorf("case H: expected 1 call (final post), got %d: %v", len(fp.calls), fp.calls)
	}
	if fp.calls[0] != slack.ActionPost {
		t.Errorf("case H: call must be %q, got %q", slack.ActionPost, fp.calls[0])
	}
}

// Case I — final-post OK, archive returns 502: nil returned (WARN logged).
func TestDestroySlackChannel_CaseI_ArchiveFails(t *testing.T) {
	fp := &fakeBridgePosterState{
		finalOK: true,
		archOK:  false,
		archErr: errors.New("502 Bad Gateway"),
	}
	ssm := &fakeSSMParamStore{params: map[string]string{
		"/km/slack/bridge-url": "https://bridge.example.com",
	}}
	priv := genTestKey(t)
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) { return priv, nil }

	m := metaWithSlack("C0PERSB", true, nil) // nil = archive enabled
	err := destroySlackChannel(context.Background(), m, "us-east-1", ssm, keyLoader, fp.post)
	if err != nil {
		t.Errorf("case I: expected nil error, got %v", err)
	}
	// Both calls were attempted.
	if len(fp.calls) != 2 {
		t.Errorf("case I: expected 2 calls, got %d: %v", len(fp.calls), fp.calls)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Visibility output tests — Plan 63.1-02 Task 1 (Wave 0 RED)
// Assert that each early-return branch emits the correct stderr line.
// captureStderr is defined in testhelpers_test.go (Plan 63.1-01 Task 1).
// ────────────────────────────────────────────────────────────────────────────

// TestDestroySlackChannel_VisibleOutput_CaseA: empty SlackChannelID → informational stderr.
func TestDestroySlackChannel_VisibleOutput_CaseA(t *testing.T) {
	fp := &fakeBridgePosterState{}
	ssmStore := &fakeSSMParamStore{params: map[string]string{
		"/km/slack/bridge-url": "https://bridge.example.com",
	}}
	priv := genTestKey(t)
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) { return priv, nil }
	m := metaWithSlack("", false, nil)

	var ret error
	captured := captureStderr(t, func() {
		ret = destroySlackChannel(context.Background(), m, "us-east-1", ssmStore, keyLoader, fp.post)
	})
	if ret != nil {
		t.Errorf("case A: expected nil error, got %v", ret)
	}
	if !strings.Contains(captured, "Slack: no channel configured — teardown notification skipped") {
		t.Errorf("case A: expected stderr substring; got %q", captured)
	}
	if len(fp.calls) != 0 {
		t.Errorf("case A: expected 0 bridge calls, got %d", len(fp.calls))
	}
}

// TestDestroySlackChannel_VisibleOutput_CaseB: per-sandbox=false → informational stderr.
func TestDestroySlackChannel_VisibleOutput_CaseB(t *testing.T) {
	fp := &fakeBridgePosterState{}
	ssmStore := &fakeSSMParamStore{params: map[string]string{
		"/km/slack/bridge-url": "https://bridge.example.com",
	}}
	priv := genTestKey(t)
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) { return priv, nil }
	m := metaWithSlack("C-TEST", false, nil)

	var ret error
	captured := captureStderr(t, func() {
		ret = destroySlackChannel(context.Background(), m, "us-east-1", ssmStore, keyLoader, fp.post)
	})
	if ret != nil {
		t.Errorf("case B: expected nil error, got %v", ret)
	}
	if !strings.Contains(captured, "Slack: shared/override mode — teardown notification skipped") {
		t.Errorf("case B: expected stderr substring; got %q", captured)
	}
	if len(fp.calls) != 0 {
		t.Errorf("case B: expected 0 bridge calls, got %d", len(fp.calls))
	}
}

// TestDestroySlackChannel_VisibleOutput_CaseE: archiveOnDestroy=false → informational stderr with channel ID.
func TestDestroySlackChannel_VisibleOutput_CaseE(t *testing.T) {
	fp := &fakeBridgePosterState{finalOK: true}
	ssmStore := &fakeSSMParamStore{params: map[string]string{
		"/km/slack/bridge-url": "https://bridge.example.com",
	}}
	priv := genTestKey(t)
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) { return priv, nil }
	fls := false
	m := metaWithSlack("C-TEST", true, &fls)

	var ret error
	captured := captureStderr(t, func() {
		ret = destroySlackChannel(context.Background(), m, "us-east-1", ssmStore, keyLoader, fp.post)
	})
	if ret != nil {
		t.Errorf("case E: expected nil error, got %v", ret)
	}
	if !strings.Contains(captured, "Slack: archive disabled (slackArchiveOnDestroy=false) — channel C-TEST kept") {
		t.Errorf("case E: expected stderr substring; got %q", captured)
	}
}

// TestDestroySlackChannel_VisibleOutput_CaseF: bridge-url SSM unset → warning stderr.
func TestDestroySlackChannel_VisibleOutput_CaseF(t *testing.T) {
	fp := &fakeBridgePosterState{}
	ssmStore := &fakeSSMParamStore{params: map[string]string{}} // no bridge-url
	priv := genTestKey(t)
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) { return priv, nil }
	m := metaWithSlack("C-TEST", true, nil)

	var ret error
	captured := captureStderr(t, func() {
		ret = destroySlackChannel(context.Background(), m, "us-east-1", ssmStore, keyLoader, fp.post)
	})
	if ret != nil {
		t.Errorf("case F: expected nil error, got %v", ret)
	}
	if !strings.Contains(captured, "⚠ Slack: /km/slack/bridge-url not configured") {
		t.Errorf("case F: expected stderr substring; got %q", captured)
	}
	if len(fp.calls) != 0 {
		t.Errorf("case F: expected 0 bridge calls, got %d", len(fp.calls))
	}
}

// TestDestroySlackChannel_VisibleOutput_CaseG: keyLoader returns error → warning stderr with error %v.
func TestDestroySlackChannel_VisibleOutput_CaseG(t *testing.T) {
	fp := &fakeBridgePosterState{}
	ssmStore := &fakeSSMParamStore{params: map[string]string{
		"/km/slack/bridge-url": "https://bridge.example.com",
	}}
	keyErr := errors.New("AccessDeniedException: ssm:GetParameter denied")
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) {
		return nil, keyErr
	}
	m := metaWithSlack("C-TEST", true, nil)

	var ret error
	captured := captureStderr(t, func() {
		ret = destroySlackChannel(context.Background(), m, "us-east-1", ssmStore, keyLoader, fp.post)
	})
	if ret != nil {
		t.Errorf("case G: expected nil error, got %v", ret)
	}
	if !strings.Contains(captured, "⚠ Slack: failed to load operator signing key") {
		t.Errorf("case G: expected stderr substring 'failed to load operator signing key'; got %q", captured)
	}
	if !strings.Contains(captured, keyErr.Error()) {
		t.Errorf("case G: expected error %%v passthrough containing %q; got %q", keyErr.Error(), captured)
	}
	if len(fp.calls) != 0 {
		t.Errorf("case G: expected 0 bridge calls, got %d", len(fp.calls))
	}
}

// TestDestroySlackChannel_VisibleOutput_CaseH: bridgePoster final-post returns error → warning stderr with error %v; archive skipped.
func TestDestroySlackChannel_VisibleOutput_CaseH(t *testing.T) {
	fp := &fakeBridgePosterState{finalErr: errors.New("bridge HTTP 403: bad_signature")}
	ssmStore := &fakeSSMParamStore{params: map[string]string{
		"/km/slack/bridge-url": "https://bridge.example.com",
	}}
	priv := genTestKey(t)
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) { return priv, nil }
	m := metaWithSlack("C-TEST", true, nil)

	var ret error
	captured := captureStderr(t, func() {
		ret = destroySlackChannel(context.Background(), m, "us-east-1", ssmStore, keyLoader, fp.post)
	})
	if ret != nil {
		t.Errorf("case H: expected nil error, got %v", ret)
	}
	if !strings.Contains(captured, "⚠ Slack: final-post bridge call failed — archive skipped:") {
		t.Errorf("case H: expected stderr substring; got %q", captured)
	}
	if !strings.Contains(captured, "bad_signature") {
		t.Errorf("case H: expected error %%v passthrough containing 'bad_signature'; got %q", captured)
	}
	if len(fp.calls) != 1 || fp.calls[0] != slack.ActionPost {
		t.Errorf("case H: expected single 'post' call, got %v", fp.calls)
	}
}

// TestDestroySlackChannel_VisibleOutput_Success: both posts ok → TWO stderr lines (final-post + archive).
func TestDestroySlackChannel_VisibleOutput_Success(t *testing.T) {
	fp := &fakeBridgePosterState{finalOK: true, archOK: true}
	ssmStore := &fakeSSMParamStore{params: map[string]string{
		"/km/slack/bridge-url": "https://bridge.example.com",
	}}
	priv := genTestKey(t)
	keyLoader := func(_ context.Context, _ string) (ed25519.PrivateKey, error) { return priv, nil }
	m := metaWithSlack("C-TEST", true, nil) // nil = archive enabled (default)

	var ret error
	captured := captureStderr(t, func() {
		ret = destroySlackChannel(context.Background(), m, "us-east-1", ssmStore, keyLoader, fp.post)
	})
	if ret != nil {
		t.Errorf("success: expected nil error, got %v", ret)
	}
	if !strings.Contains(captured, "✓ Slack: posted teardown message to C-TEST") {
		t.Errorf("success: expected 'posted teardown message to C-TEST' in stderr; got %q", captured)
	}
	if !strings.Contains(captured, "✓ Slack: archived channel C-TEST") {
		t.Errorf("success: expected 'archived channel C-TEST' in stderr; got %q", captured)
	}
	if len(fp.calls) != 2 {
		t.Errorf("success: expected 2 bridge calls, got %d: %v", len(fp.calls), fp.calls)
	}
}
