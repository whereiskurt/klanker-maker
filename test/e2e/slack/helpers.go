// Package slack_e2e provides helpers for opt-in live Slack E2E tests.
// Tests in this package only run when RUN_SLACK_E2E=1 is set in the environment.
//
// Required environment variables (all tests):
//
//	KM_SLACK_E2E_BOT_TOKEN    – Slack bot token (xoxb-...) for polling Slack history
//	KM_SLACK_E2E_INVITE_EMAIL – email address that receives Slack Connect invites
//	KM_SLACK_E2E_REGION       – AWS region (e.g. us-east-1)
//
// Optional:
//
//	KM_SLACK_E2E_SHARED_CHANNEL – pre-existing shared channel ID (skips km slack status poll)
//	KM_SLACK_E2E_RATELIMIT      – set to 1 to enable rate-limit burst test
//	KM_KM_BINARY                – path to km binary (default: ./km)
package slack_e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// E2EConfig holds the resolved configuration for a live E2E test run.
type E2EConfig struct {
	BotToken      string // xoxb-... for Slack API polling in tests
	InviteEmail   string // email for Slack Connect invites
	Region        string // AWS region
	SharedChannel string // optional pre-seeded shared channel ID
	KMBinary      string // path to km binary (default: ./km)
}

// LoadE2EConfig reads required env vars; calls t.Skip if any are missing.
func LoadE2EConfig(t *testing.T) E2EConfig {
	t.Helper()
	cfg := E2EConfig{
		BotToken:      os.Getenv("KM_SLACK_E2E_BOT_TOKEN"),
		InviteEmail:   os.Getenv("KM_SLACK_E2E_INVITE_EMAIL"),
		Region:        os.Getenv("KM_SLACK_E2E_REGION"),
		SharedChannel: os.Getenv("KM_SLACK_E2E_SHARED_CHANNEL"),
		KMBinary:      os.Getenv("KM_KM_BINARY"),
	}
	if cfg.KMBinary == "" {
		cfg.KMBinary = "./km"
	}
	var missing []string
	if cfg.BotToken == "" {
		missing = append(missing, "KM_SLACK_E2E_BOT_TOKEN")
	}
	if cfg.InviteEmail == "" {
		missing = append(missing, "KM_SLACK_E2E_INVITE_EMAIL")
	}
	if cfg.Region == "" {
		missing = append(missing, "KM_SLACK_E2E_REGION")
	}
	if len(missing) > 0 {
		t.Skipf("skipping live Slack E2E test: missing env vars: %s", strings.Join(missing, ", "))
	}
	return cfg
}

// RunKM executes the km binary with the given args, returning combined stdout+stderr
// and the exit code. Never calls t.Fatal — callers decide how to assert.
func RunKM(t *testing.T, cfg E2EConfig, args ...string) (output string, exitCode int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, cfg.KMBinary, args...)
	out, err := cmd.CombinedOutput()
	output = string(out)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// context deadline or similar
			exitCode = -1
		}
	}
	t.Logf("RunKM %v → exit=%d\n%s", args, exitCode, output)
	return output, exitCode
}

// sandboxIDRe matches Klanker Maker sandbox identifiers.
var sandboxIDRe = regexp.MustCompile(`\bsb-[a-f0-9]{8}\b`)

// ExtractSandboxID parses the first sb-XXXXXXXX identifier from km create output.
// Returns "" if none found.
func ExtractSandboxID(output string) string {
	m := sandboxIDRe.FindString(output)
	return m
}

// channelIDRe matches Slack channel IDs (C followed by uppercase letters/digits).
var channelIDRe = regexp.MustCompile(`\bC[A-Z0-9]{6,}\b`)

// ExtractSlackChannelID polls km status <sandboxID> and extracts the first
// Slack channel ID from the output. Retries for up to 30 seconds.
func ExtractSlackChannelID(t *testing.T, cfg E2EConfig, sandboxID string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := RunKM(t, cfg, "status", sandboxID)
		if m := channelIDRe.FindString(out); m != "" {
			return m
		}
		time.Sleep(2 * time.Second)
	}
	return ""
}

// GetSharedChannelID calls km slack status and extracts the shared-channel-id row.
// Falls back to cfg.SharedChannel if the SSM row is empty.
func GetSharedChannelID(t *testing.T, cfg E2EConfig) string {
	t.Helper()
	if cfg.SharedChannel != "" {
		return cfg.SharedChannel
	}
	out, _ := RunKM(t, cfg, "slack", "status")
	// Look for the /km/slack/shared-channel-id row: "key   value"
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "shared-channel-id") {
			parts := strings.Fields(line)
			for _, p := range parts {
				if channelIDRe.MatchString(p) {
					return p
				}
			}
		}
	}
	t.Logf("GetSharedChannelID: could not parse channel from km slack status output:\n%s", out)
	return ""
}

// slackConversationsHistoryResponse is the minimal subset of
// conversations.history we need for polling.
type slackConversationsHistoryResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Messages []struct {
		TS   string `json:"ts"`
		Text string `json:"text"`
	} `json:"messages"`
}

// WaitForSlackMessage polls conversations.history for a message whose text
// contains bodySubstr. Returns the message ts when found. Calls t.Fatal on
// unrecoverable errors (auth failure, timeout).
func WaitForSlackMessage(t *testing.T, ctx context.Context, cfg E2EConfig, channelID, bodySubstr string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			t.Fatalf("WaitForSlackMessage: context cancelled: %v", ctx.Err())
		}
		ts, found := pollSlackHistory(t, cfg.BotToken, channelID, bodySubstr)
		if found {
			return ts
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("WaitForSlackMessage: timed out after %v waiting for %q in channel %s", timeout, bodySubstr, channelID)
	return ""
}

// pollSlackHistory is the inner non-fatal poll. Returns (ts, true) when found.
func pollSlackHistory(t *testing.T, botToken, channelID, bodySubstr string) (string, bool) {
	t.Helper()
	url := fmt.Sprintf("https://slack.com/api/conversations.history?channel=%s&limit=20", channelID)
	req, err := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	if err != nil {
		t.Logf("pollSlackHistory: build request: %v", err)
		return "", false
	}
	req.Header.Set("Authorization", "Bearer "+botToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("pollSlackHistory: HTTP error: %v", err)
		return "", false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var r slackConversationsHistoryResponse
	if err := json.Unmarshal(body, &r); err != nil {
		t.Logf("pollSlackHistory: unmarshal: %v body=%s", err, body)
		return "", false
	}
	if !r.OK {
		t.Logf("pollSlackHistory: Slack API error=%s", r.Error)
		return "", false
	}
	for _, msg := range r.Messages {
		if strings.Contains(msg.Text, bodySubstr) {
			return msg.TS, true
		}
	}
	return "", false
}

// slackConversationsInfoResponse is the minimal subset of conversations.info
// we need for archive checking.
type slackConversationsInfoResponse struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Channel struct {
		IsArchived bool `json:"is_archived"`
	} `json:"channel"`
}

// AssertSandboxArchivedInSlack returns true when the Slack channel is archived.
// Polls for up to 30 seconds to allow eventual consistency.
func AssertSandboxArchivedInSlack(t *testing.T, ctx context.Context, cfg E2EConfig, channelID string) bool {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		if isArchived, err := queryChannelArchived(cfg.BotToken, channelID); err == nil && isArchived {
			return true
		}
		time.Sleep(3 * time.Second)
	}
	return false
}

func queryChannelArchived(botToken, channelID string) (bool, error) {
	url := fmt.Sprintf("https://slack.com/api/conversations.info?channel=%s", channelID)
	req, err := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+botToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var r slackConversationsInfoResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return false, err
	}
	if !r.OK {
		return false, fmt.Errorf("conversations.info: %s", r.Error)
	}
	return r.Channel.IsArchived, nil
}

// CleanupSandbox calls km destroy on a sandbox. Logs failures but never fails
// the test — cleanup runs in defer and must not mask real failures.
func CleanupSandbox(t *testing.T, cfg E2EConfig, sandboxID string) {
	t.Helper()
	if sandboxID == "" {
		return
	}
	out, code := RunKM(t, cfg, "destroy", sandboxID, "--remote", "--yes")
	if code != 0 {
		t.Logf("CleanupSandbox %s: exit=%d output=%s (non-fatal)", sandboxID, code, out)
	}
}

// SendSlackMessageDirect posts a message directly to a Slack channel using the
// bot token. Used by the rate-limit burst test to generate rapid posts.
// Returns the message ts on success.
func SendSlackMessageDirect(t *testing.T, cfg E2EConfig, channelID, text string) (string, error) {
	t.Helper()
	url := "https://slack.com/api/chat.postMessage"
	payload := fmt.Sprintf(`{"channel":%q,"text":%q}`, channelID, text)
	req, err := http.NewRequestWithContext(context.Background(), "POST", url, strings.NewReader(payload))
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

	// If rate-limited, surface the status and Retry-After
	if resp.StatusCode == 429 {
		retryAfter := resp.Header.Get("Retry-After")
		return "", fmt.Errorf("rate limited: status=429 Retry-After=%s body=%s", retryAfter, body)
	}

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
