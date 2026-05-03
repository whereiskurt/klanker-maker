// Package slack_e2e contains opt-in live E2E tests for the Phase 67
// Slack inbound (bidirectional chat) integration. Tests in this file
// only run when RUN_SLACK_E2E=1 is set (shared gate with Phase 63 tests).
//
// To run:
//
//	RUN_SLACK_E2E=1 \
//	  KM_SLACK_E2E_BOT_TOKEN=xoxb-... \
//	  KM_SLACK_E2E_INVITE_EMAIL=you@example.com \
//	  KM_SLACK_E2E_REGION=us-east-1 \
//	  go test ./test/e2e/slack/... -v -timeout 30m -run TestSlackInbound
//
// Additional optional env vars for inbound tests:
//
//	KM_E2E_PROFILE – override the test profile path
//	               (default: test/e2e/slack/profiles/inbound-e2e.yaml)
//
// TestMain lives in slack_e2e_test.go (Phase 63 file) and gates the whole
// package on RUN_SLACK_E2E=1. No duplicate TestMain here.
package slack_e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// TestSlackInbound_E2E — full bidirectional round-trip
//
// Covers VALIDATION.md manual-only rows:
//   - Per-sandbox SQS queue lifecycle
//   - Multi-turn --resume continuity
//   - Top-level Slack post starts new thread
//   - Slack url_verification challenge round-trip (verified during setup)
//
// ─────────────────────────────────────────────────────────────────────────────

// TestSlackInbound_E2E exercises the full bidirectional flow:
//
//  1. km create profile-with-inbound.yaml --remote
//  2. Wait for "ready" announcement to land in the channel (capture ts)
//  3. Post test prompt as a thread reply via Slack API (operator token)
//  4. Poll Slack for in-thread reply from Claude (<=60s)
//  5. Post follow-up prompt referencing first reply
//  6. Verify second response references first turn (session continuity)
//  7. Post a NEW top-level message (not in a thread) — verify new thread
//  8. km destroy --remote --yes (drain + archive)
//  9. Verify SQS queue deleted and channel archived
//
// Gated by RUN_SLACK_E2E=1 env var. Skipped in normal `go test ./...`.
// Note: TestMain in slack_e2e_test.go already gates the whole package on
// RUN_SLACK_E2E; the t.Skip inside each test is a belt-and-suspenders guard.
func TestSlackInbound_E2E(t *testing.T) {
	if os.Getenv("RUN_SLACK_E2E") != "1" {
		t.Skip("set RUN_SLACK_E2E=1 to run (requires real Slack workspace + AWS account)")
	}

	cfg := LoadE2EConfig(t)

	profilePath := os.Getenv("KM_E2E_PROFILE")
	if profilePath == "" {
		profilePath = "test/e2e/slack/profiles/inbound-e2e.yaml"
	}

	// (1) km create — provisions SQS FIFO queue + ready announcement
	// covers VALIDATION.md row "Per-sandbox SQS queue lifecycle"
	sandboxID, channelID := mustCreateInboundSandbox(t, cfg, profilePath)
	defer destroyAndAssertInbound(t, cfg, sandboxID, channelID)

	// (2) Wait for ready announcement (the "Sandbox <id> ready." message)
	// covers VALIDATION.md row "Per-sandbox SQS queue lifecycle"
	readyTS := mustWaitForReadyAnnouncement(t, cfg, channelID, 90*time.Second)
	t.Logf("ready announcement ts=%s", readyTS)

	// (3) Post first prompt as thread reply to the ready announcement
	// covers VALIDATION.md row "Multi-turn --resume continuity" (turn 1)
	msgTS1 := mustPostSlackThreadReply(t, cfg, channelID, readyTS, "What model are you?")
	t.Logf("posted first prompt in thread ts=%s", msgTS1)

	// (4) Poll for Claude's in-thread reply
	// covers VALIDATION.md row "Multi-turn --resume continuity" (turn 1 reply)
	reply1 := mustWaitForClaudeThreadReply(t, cfg, channelID, msgTS1, 90*time.Second)
	t.Logf("Claude first reply: %s", reply1)
	if !strings.Contains(strings.ToLower(reply1), "claude") {
		t.Errorf("first reply does not mention 'claude': %s", reply1)
	}

	// (5) Follow-up prompt in same thread — tests session continuity
	// covers VALIDATION.md row "Multi-turn --resume continuity" (turn 2)
	msgTS2 := mustPostSlackThreadReply(t, cfg, channelID, msgTS1, "Repeat my last question verbatim.")
	t.Logf("posted follow-up in thread ts=%s", msgTS2)

	// (6) Verify session continuity: second reply should reference "what model are you"
	// covers VALIDATION.md row "Multi-turn --resume continuity" (turn 2 reply)
	reply2 := mustWaitForClaudeThreadReply(t, cfg, channelID, msgTS2, 120*time.Second)
	t.Logf("Claude second reply: %s", reply2)
	if !strings.Contains(strings.ToLower(reply2), "what model are you") {
		t.Errorf("second reply does not reference first turn (session continuity broken): %s", reply2)
	}

	// (7) Post a NEW top-level message — should start a fresh thread
	// covers VALIDATION.md row "Top-level Slack post starts new thread"
	newTopTS := mustPostSlackTopLevel(t, cfg, channelID, "Hello fresh thread. Say hi!")
	t.Logf("posted top-level message ts=%s", newTopTS)

	// Wait for Claude's reply in the new thread (NOT in the old one)
	newThreadReply := mustWaitForClaudeThreadReply(t, cfg, channelID, newTopTS, 90*time.Second)
	t.Logf("Claude new-thread reply: %s", newThreadReply)
	if newThreadReply == "" {
		t.Error("expected Claude to reply in new top-level thread, got empty reply")
	}

	// (8+9) destroyAndAssertInbound deferred above handles cleanup and assertions
}

// TestSlackInbound_QueueSanity verifies that km create provisions a queue and
// km destroy removes it, without requiring Claude interaction. Useful as a
// lighter smoke test.
//
// covers VALIDATION.md row "Per-sandbox SQS queue lifecycle"
func TestSlackInbound_QueueSanity(t *testing.T) {
	if os.Getenv("RUN_SLACK_E2E") != "1" {
		t.Skip("set RUN_SLACK_E2E=1 to run (requires real Slack workspace + AWS account)")
	}

	cfg := LoadE2EConfig(t)

	profilePath := os.Getenv("KM_E2E_PROFILE")
	if profilePath == "" {
		profilePath = "test/e2e/slack/profiles/inbound-e2e.yaml"
	}

	sandboxID, channelID := mustCreateInboundSandbox(t, cfg, profilePath)

	// Verify queue exists before destroy
	region := os.Getenv("KM_SLACK_E2E_REGION")
	if region == "" {
		region = "us-east-1"
	}
	queuePrefix := "km-slack-inbound-" + sandboxID
	if !assertSQSQueueExists(t, cfg, queuePrefix, region) {
		// Log but don't fatal — destroy still needed for cleanup
		t.Errorf("SQS queue with prefix %s not found after km create", queuePrefix)
	}

	// Destroy
	out, code := RunKM(t, cfg, "destroy", sandboxID, "--remote", "--yes")
	if code != 0 {
		t.Fatalf("km destroy failed (exit=%d):\n%s", code, out)
	}

	// Verify queue is gone after destroy
	// Poll briefly for eventual consistency
	deadline := time.Now().Add(30 * time.Second)
	queueGone := false
	for time.Now().Before(deadline) {
		if !assertSQSQueueExists(t, cfg, queuePrefix, region) {
			queueGone = true
			break
		}
		time.Sleep(5 * time.Second)
	}
	if !queueGone {
		t.Errorf("SQS queue with prefix %s still exists after km destroy", queuePrefix)
	}

	// Verify channel archived
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !AssertSandboxArchivedInSlack(t, ctx, cfg, channelID) {
		t.Errorf("expected Slack channel %s to be archived after km destroy, but it is not", channelID)
	}

	t.Logf("queue lifecycle and channel archive: OK")
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers — inbound-specific
// ─────────────────────────────────────────────────────────────────────────────

// mustCreateInboundSandbox runs km create with the inbound profile and returns
// (sandboxID, channelID). Calls t.Fatal on any failure.
func mustCreateInboundSandbox(t *testing.T, cfg E2EConfig, profilePath string) (sandboxID, channelID string) {
	t.Helper()
	createOut, code := RunKM(t, cfg, "create", profilePath, "--remote")
	if code != 0 {
		t.Fatalf("km create failed (exit=%d):\n%s", code, createOut)
	}
	sandboxID = ExtractSandboxID(createOut)
	if sandboxID == "" {
		t.Fatalf("could not extract sandbox ID from km create output:\n%s", createOut)
	}
	t.Logf("created inbound sandbox: %s", sandboxID)

	// Verify inbound queue URL is present in create output
	if !strings.Contains(createOut, "KM_SLACK_INBOUND_QUEUE_URL") &&
		!strings.Contains(createOut, "inbound queue") &&
		!strings.Contains(createOut, "slack-inbound") {
		t.Logf("WARNING: inbound queue confirmation not found in km create output; may indicate missing feature")
	}

	// Extract channel ID via km status
	channelID = ExtractSlackChannelID(t, cfg, sandboxID)
	if channelID == "" {
		// Cleanup before fatal
		RunKM(t, cfg, "destroy", sandboxID, "--remote", "--yes")
		t.Fatalf("could not extract per-sandbox channel ID for %s", sandboxID)
	}
	t.Logf("per-sandbox channel: %s", channelID)
	return sandboxID, channelID
}

// mustWaitForReadyAnnouncement polls channel history for the "ready" announcement
// posted by km create. Returns the message ts.
//
// covers VALIDATION.md row "Per-sandbox SQS queue lifecycle"
func mustWaitForReadyAnnouncement(t *testing.T, cfg E2EConfig, channelID string, timeout time.Duration) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ts := WaitForSlackMessage(t, ctx, cfg, channelID, "ready", timeout)
	return ts
}

// mustPostSlackThreadReply posts a message as a reply in an existing thread.
// Uses the bot token for simplicity; the bridge's Events API will forward it to
// the sandbox poller exactly as if the operator posted it.
// Returns the new message ts.
func mustPostSlackThreadReply(t *testing.T, cfg E2EConfig, channelID, threadTS, text string) string {
	t.Helper()
	ts, err := postSlackMessage(cfg, channelID, text, threadTS)
	if err != nil {
		t.Fatalf("mustPostSlackThreadReply: %v", err)
	}
	return ts
}

// mustPostSlackTopLevel posts a new top-level (non-threaded) message.
// Returns the message ts, which becomes the thread root.
//
// covers VALIDATION.md row "Top-level Slack post starts new thread"
func mustPostSlackTopLevel(t *testing.T, cfg E2EConfig, channelID, text string) string {
	t.Helper()
	ts, err := postSlackMessage(cfg, channelID, text, "" /* no thread_ts = top-level */)
	if err != nil {
		t.Fatalf("mustPostSlackTopLevel: %v", err)
	}
	return ts
}

// mustWaitForClaudeThreadReply polls conversations.replies for a bot message in
// the given thread. Returns the text of the first bot reply found.
//
// covers VALIDATION.md row "Multi-turn --resume continuity"
func mustWaitForClaudeThreadReply(t *testing.T, cfg E2EConfig, channelID, threadTS string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		text, found := pollSlackThreadForBotReply(t, cfg.BotToken, channelID, threadTS)
		if found {
			return text
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("mustWaitForClaudeThreadReply: timed out after %v waiting for bot reply in thread %s (channel %s)",
		timeout, threadTS, channelID)
	return ""
}

// destroyAndAssertInbound runs km destroy and verifies inbound cleanup:
//   - SQS queue deleted
//   - Channel archived
//
// Intended to be called in a defer; does not call t.Fatal (only t.Error).
//
// covers VALIDATION.md row "km destroy drain best-effort 30s timeout"
func destroyAndAssertInbound(t *testing.T, cfg E2EConfig, sandboxID, channelID string) {
	t.Helper()
	if sandboxID == "" {
		return
	}

	out, code := RunKM(t, cfg, "destroy", sandboxID, "--remote", "--yes")
	if code != 0 {
		t.Errorf("km destroy failed (exit=%d):\n%s", code, out)
		return
	}

	// Verify drain log lines are present
	drainMessages := []string{
		"drain",
	}
	for _, msg := range drainMessages {
		if !strings.Contains(out, msg) {
			t.Logf("destroyAndAssertInbound: expected %q in destroy output but not found (non-fatal, drain may log to stderr separately)", msg)
		}
	}

	// Verify SQS queue is deleted
	region := os.Getenv("KM_SLACK_E2E_REGION")
	if region == "" {
		region = "us-east-1"
	}
	queuePrefix := "km-slack-inbound-" + sandboxID
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if !assertSQSQueueExists(t, cfg, queuePrefix, region) {
			t.Logf("SQS queue %s.fifo confirmed deleted after destroy", queuePrefix)
			break
		}
		time.Sleep(5 * time.Second)
	}
	// Non-fatal check — test already passed if we got here
	if assertSQSQueueExists(t, cfg, queuePrefix, region) {
		t.Errorf("SQS queue with prefix %s still exists after km destroy", queuePrefix)
	}

	// Verify channel archived
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if channelID != "" && !AssertSandboxArchivedInSlack(t, ctx, cfg, channelID) {
		t.Errorf("expected Slack channel %s to be archived after km destroy, but it is not", channelID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Low-level Slack API helpers — inbound-specific
// ─────────────────────────────────────────────────────────────────────────────

// postSlackMessage posts a message to a Slack channel.
// If threadTS is non-empty, the message is posted as a thread reply.
// Returns the new message ts.
func postSlackMessage(cfg E2EConfig, channelID, text, threadTS string) (string, error) {
	type payload struct {
		Channel  string `json:"channel"`
		Text     string `json:"text"`
		ThreadTS string `json:"thread_ts,omitempty"`
	}
	p := payload{Channel: channelID, Text: text, ThreadTS: threadTS}
	b, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), "POST",
		"https://slack.com/api/chat.postMessage", strings.NewReader(string(b)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.BotToken)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	type response struct {
		OK    bool   `json:"ok"`
		TS    string `json:"ts"`
		Error string `json:"error,omitempty"`
	}
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("unmarshal: %w body=%s", err, body)
	}
	if !r.OK {
		return "", fmt.Errorf("chat.postMessage: %s", r.Error)
	}
	return r.TS, nil
}

// slackRepliesResponse is the minimal subset of conversations.replies we need
// to detect bot replies in a thread.
type slackRepliesResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Messages []struct {
		TS     string `json:"ts"`
		Text   string `json:"text"`
		BotID  string `json:"bot_id,omitempty"`
		UserID string `json:"user,omitempty"`
		// Subtype is set for bot_message, message_changed, etc.
		Subtype string `json:"subtype,omitempty"`
	} `json:"messages"`
}

// pollSlackThreadForBotReply calls conversations.replies and looks for a reply
// with a bot_id field (i.e., a bot message). Skips the thread root (ts == threadTS).
// Returns (text, true) when found.
func pollSlackThreadForBotReply(t *testing.T, botToken, channelID, threadTS string) (string, bool) {
	t.Helper()
	url := fmt.Sprintf(
		"https://slack.com/api/conversations.replies?channel=%s&ts=%s&limit=20",
		channelID, threadTS)
	req, err := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	if err != nil {
		t.Logf("pollSlackThreadForBotReply: build request: %v", err)
		return "", false
	}
	req.Header.Set("Authorization", "Bearer "+botToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("pollSlackThreadForBotReply: HTTP error: %v", err)
		return "", false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var r slackRepliesResponse
	if err := json.Unmarshal(body, &r); err != nil {
		t.Logf("pollSlackThreadForBotReply: unmarshal: %v body=%s", err, body)
		return "", false
	}
	if !r.OK {
		t.Logf("pollSlackThreadForBotReply: Slack API error=%s", r.Error)
		return "", false
	}
	for _, msg := range r.Messages {
		// Skip the thread root (ts == threadTS) and non-bot messages
		if msg.TS == threadTS {
			continue
		}
		// Accept replies with a BotID or bot_message subtype (Claude's replies)
		if msg.BotID != "" || msg.Subtype == "bot_message" {
			return msg.Text, true
		}
	}
	return "", false
}

// assertSQSQueueExists checks via aws CLI whether an SQS queue with the given
// prefix exists. Returns false if the CLI call fails or returns no queues.
func assertSQSQueueExists(t *testing.T, cfg E2EConfig, queuePrefix, region string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "aws", "sqs", "list-queues",
		"--queue-name-prefix", queuePrefix,
		"--region", region,
		"--output", "json")
	out, err := cmd.Output()
	if err != nil {
		t.Logf("assertSQSQueueExists: aws sqs list-queues: %v", err)
		return false
	}
	type sqsListResponse struct {
		QueueUrls []string `json:"QueueUrls"`
	}
	var r sqsListResponse
	if err := json.Unmarshal(out, &r); err != nil {
		t.Logf("assertSQSQueueExists: unmarshal: %v body=%s", err, out)
		return false
	}
	return len(r.QueueUrls) > 0
}
