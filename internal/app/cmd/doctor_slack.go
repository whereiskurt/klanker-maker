// Package cmd — doctor_slack.go
// Slack health checks for km doctor.
//
// Plan 63-09:
//   checkSlackTokenValidity: validates the bot token via a test message through
//   the bridge Lambda.
//   checkStaleSlackChannels: warns about per-sandbox Slack channels for sandboxes
//   that no longer have active EC2 instances.
//
// Plan 67-08:
//   checkSlackInboundQueueExists: verifies every inbound-enabled sandbox has an
//   accessible SQS queue.
//   checkSlackInboundStaleQueues: detects orphaned SQS queues (queue exists but no
//   DDB sandbox row).
//   checkSlackAppEventsScopes: verifies the Slack bot token has the required inbound
//   scopes (channels:history, groups:history).
package cmd

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	sqssvc "github.com/aws/aws-sdk-go-v2/service/sqs"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	slackpkg "github.com/whereiskurt/klankrmkr/pkg/slack"
)

// inboundRow is a lightweight record returned by the listSandboxesWithInbound
// helper — used by the checkSlackInbound* doctor checks.
type inboundRow struct {
	SandboxID string
	QueueURL  string
}

// SlackMetadataScanner returns sandbox metadata records that have Slack enabled.
// Implemented by a thin wrapper around ListAllSandboxesByDynamo; injected for tests.
type SlackMetadataScanner interface {
	ListSlackEnabled(ctx context.Context) ([]kmaws.SandboxMetadata, error)
}

// EC2InstanceLister checks whether an EC2 instance (sandbox) is still active.
// Injected for tests; production uses an EC2 DescribeInstances call.
type EC2InstanceLister interface {
	InstanceExists(ctx context.Context, sandboxID string) (bool, error)
}

// checkSlackTokenValidity validates the Slack bot token by sending a test
// message through the bridge Lambda. Returns:
//
//   - SKIPPED: /km/slack/bot-token not set → Slack not configured.
//   - WARN: bot-token set but /km/slack/bridge-url missing.
//   - OK: test envelope posted successfully.
//   - WARN: bridge returned non-OK response (invalid_auth etc.).
//   - ERROR: bridge network error.
func checkSlackTokenValidity(
	ctx context.Context,
	ssmStore SSMParamStore,
	region string,
	keyLoader PrivKeyLoaderFunc,
	poster BridgePosterFunc,
) CheckResult {
	botToken, _ := ssmStore.Get(ctx, "/km/slack/bot-token", true)
	if botToken == "" {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckSkipped,
			Message: "/km/slack/bot-token not configured — Slack integration not set up",
		}
	}

	bridgeURL, _ := ssmStore.Get(ctx, "/km/slack/bridge-url", false)
	if bridgeURL == "" {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckWarn,
			Message: "/km/slack/bridge-url not configured — run km slack init to deploy the bridge Lambda",
		}
	}

	channelID, _ := ssmStore.Get(ctx, "/km/slack/shared-channel-id", false)
	if channelID == "" {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckWarn,
			Message: "/km/slack/shared-channel-id not configured — run km slack init",
		}
	}

	priv, err := keyLoader(ctx, region)
	if err != nil {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckWarn,
			Message: fmt.Sprintf("operator signing key unavailable: %v — km slack test may fail", err),
		}
	}

	env, envErr := slackpkg.BuildEnvelope(slackpkg.ActionTest, slackpkg.SenderOperator,
		channelID, "km doctor", "Platform health check", "")
	if envErr != nil {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckError,
			Message: fmt.Sprintf("failed to build test envelope: %v", envErr),
		}
	}

	_, sig, sigErr := slackpkg.SignEnvelope(env, priv)
	if sigErr != nil {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckError,
			Message: fmt.Sprintf("failed to sign test envelope: %v", sigErr),
		}
	}

	resp, postErr := poster(ctx, bridgeURL, env, sig)
	if postErr != nil {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckError,
			Message: fmt.Sprintf("bridge request failed: %v", postErr),
		}
	}
	if resp == nil || !resp.OK {
		errMsg := ""
		if resp != nil {
			errMsg = resp.Error
		}
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckWarn,
			Message: fmt.Sprintf("bridge returned not-OK: %s", errMsg),
		}
	}

	return CheckResult{
		Name:    "Slack bot token",
		Status:  CheckOK,
		Message: fmt.Sprintf("test message delivered (ts=%s)", resp.TS),
	}
}

// checkStaleSlackChannels scans for per-sandbox Slack channels that belong to
// sandboxes without active EC2 instances (stale channels). Returns:
//
//   - OK: no Slack-enabled records found, or all sandboxes are active.
//   - WARN: N stale per-sandbox channels found.
func checkStaleSlackChannels(
	ctx context.Context,
	scanner SlackMetadataScanner,
	ec2 EC2InstanceLister,
) CheckResult {
	records, err := scanner.ListSlackEnabled(ctx)
	if err != nil {
		return CheckResult{
			Name:    "Stale Slack channels",
			Status:  CheckWarn,
			Message: fmt.Sprintf("failed to list Slack-enabled sandboxes: %v", err),
		}
	}

	var stale []string
	for _, meta := range records {
		if !meta.SlackPerSandbox || meta.SlackChannelID == "" {
			continue // skip shared/override channels — they're operator-managed
		}
		exists, existErr := ec2.InstanceExists(ctx, meta.SandboxID)
		if existErr != nil {
			continue // skip on error; don't false-alarm
		}
		if !exists {
			stale = append(stale, meta.SlackChannelID)
		}
	}

	if len(stale) == 0 {
		return CheckResult{
			Name:    "Stale Slack channels",
			Status:  CheckOK,
			Message: "no stale per-sandbox channels",
		}
	}

	return CheckResult{
		Name:   "Stale Slack channels",
		Status: CheckWarn,
		Message: fmt.Sprintf(
			"%d stale per-sandbox channel(s) with no active sandbox: %s — run km destroy to archive",
			len(stale),
			strings.Join(stale, ", "),
		),
	}
}

// =============================================================================
// Plan 67-08 — Slack inbound diagnostic checks
// =============================================================================

// checkSlackInboundQueueExists verifies that every km-sandboxes row with a
// non-empty slack_inbound_queue_url has a reachable SQS queue. Uses
// QueueDepth as the liveness probe (GetQueueAttributes call).
//
// Returns:
//   - SKIPPED: no ListSandboxesWithInbound func or no SQS client configured.
//   - OK: no inbound-enabled sandboxes, or all queues are reachable.
//   - FAIL: one or more queues are missing or unreachable.
func checkSlackInboundQueueExists(
	ctx context.Context,
	listInbound func(context.Context) ([]inboundRow, error),
	sqsClient kmaws.SQSClient,
) CheckResult {
	name := "Slack inbound queue exists"
	if listInbound == nil || sqsClient == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "Slack inbound deps not configured",
		}
	}

	rows, err := listInbound(ctx)
	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: fmt.Sprintf("failed to list inbound-enabled sandboxes: %v", err),
		}
	}
	if len(rows) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: "no sandboxes have inbound enabled",
		}
	}

	var missing []string
	for _, r := range rows {
		if _, qErr := kmaws.QueueDepth(ctx, sqsClient, r.QueueURL); qErr != nil {
			missing = append(missing, fmt.Sprintf("%s (%s)", r.SandboxID, r.QueueURL))
		}
	}
	if len(missing) > 0 {
		return CheckResult{
			Name:        name,
			Status:      CheckError,
			Message:     fmt.Sprintf("inbound queue missing or unreachable for: %s", strings.Join(missing, ", ")),
			Remediation: "Queue was likely deleted manually. Run 'km destroy <sandbox-id> --remote --yes' to clean up the DDB record, then 'km create' to reprovision.",
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Message: fmt.Sprintf("%d sandbox(es) have healthy inbound queues", len(rows)),
	}
}

// checkSlackInboundStaleQueues lists all SQS queues matching
// {prefix}-slack-inbound-*.fifo and warns when any have no corresponding DDB
// sandbox row. These are orphans from failed km destroy runs.
//
// Returns:
//   - SKIPPED: no SQS client or listInbound func configured.
//   - OK: no inbound queues found, or all are accounted for in DDB.
//   - WARN: one or more orphan queues found.
func checkSlackInboundStaleQueues(
	ctx context.Context,
	listInbound func(context.Context) ([]inboundRow, error),
	sqsClient kmaws.SQSClient,
	resourcePrefix string,
) CheckResult {
	name := "Slack inbound stale queues"
	if listInbound == nil || sqsClient == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "Slack inbound deps not configured",
		}
	}

	if resourcePrefix == "" {
		resourcePrefix = "km"
	}

	// List all queues with the inbound prefix.
	listOut, listErr := sqsClient.ListQueues(ctx, &sqssvc.ListQueuesInput{
		QueueNamePrefix: awssdk.String(resourcePrefix + "-slack-inbound-"),
	})
	if listErr != nil {
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: fmt.Sprintf("list queues failed: %v", listErr),
		}
	}
	if len(listOut.QueueUrls) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: "no inbound queues exist",
		}
	}

	// Build set of known queue URLs from DDB.
	rows, err := listInbound(ctx)
	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: fmt.Sprintf("failed to list inbound-enabled sandboxes: %v", err),
		}
	}
	known := make(map[string]string, len(rows))
	for _, r := range rows {
		known[r.QueueURL] = r.SandboxID
	}

	var stale []string
	for _, qURL := range listOut.QueueUrls {
		if _, ok := known[qURL]; !ok {
			stale = append(stale, qURL)
		}
	}
	if len(stale) > 0 {
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("%d stale inbound queue(s) without DDB record: %s", len(stale), strings.Join(stale, ", ")),
			Remediation: "Run: aws sqs delete-queue --queue-url <url> for each stale queue listed above",
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Message: fmt.Sprintf("all %d inbound queue(s) accounted for in DDB", len(listOut.QueueUrls)),
	}
}

// checkSlackAppEventsScopes verifies that the Slack bot token has the required
// scopes for inbound event processing (channels:history, groups:history) AND
// for the Phase 67.1 ACK reaction (reactions:write). These are required in
// addition to the Phase 63 outbound scopes.
//
// Returns:
//   - SKIPPED: no SlackAuthTestScopes func configured.
//   - OK: all required scopes present.
//   - FAIL: one or more required scopes missing.
func checkSlackAppEventsScopes(
	ctx context.Context,
	getScopes func(context.Context) ([]string, error),
) CheckResult {
	name := "Slack App events scopes"
	if getScopes == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "Slack auth-test scopes func not configured",
		}
	}

	scopes, err := getScopes(ctx)
	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: fmt.Sprintf("could not check Slack scopes: %v", err),
		}
	}

	required := []string{"channels:history", "groups:history", "reactions:write"}
	scopeSet := make(map[string]bool, len(scopes))
	for _, s := range scopes {
		scopeSet[s] = true
	}

	var missing []string
	for _, r := range required {
		if !scopeSet[r] {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		return CheckResult{
			Name:        name,
			Status:      CheckError,
			Message:     fmt.Sprintf("Slack App missing required scopes for inbound: %s", strings.Join(missing, ", ")),
			Remediation: "Add scopes via Slack App config → OAuth & Permissions → Bot Token Scopes, then run 'km slack rotate-token' to refresh the SSM-cached token",
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Message: "Slack App has all required inbound scopes (channels:history, groups:history, reactions:write)",
	}
}

// =============================================================================
// Plan 67-08 — production helpers (used by initRealDepsWithExisting)
// =============================================================================

// listSandboxesWithInboundImpl scans the km-sandboxes DDB table and returns
// rows that have a non-empty slack_inbound_queue_url. Used as the production
// implementation of DoctorDeps.SlackListSandboxesWithInbound.
//
// Uses ListAllSandboxMetadataDynamo (Plan 63-09) which already pulls the full
// SandboxMetadata including SlackInboundQueueURL — no separate scan needed.
func listSandboxesWithInboundImpl(ctx context.Context, client kmaws.SandboxMetadataAPI, tableName string) ([]inboundRow, error) {
	metas, err := kmaws.ListAllSandboxMetadataDynamo(ctx, client, tableName)
	if err != nil {
		return nil, fmt.Errorf("list sandbox metadata: %w", err)
	}
	var rows []inboundRow
	for _, m := range metas {
		if m.SlackInboundQueueURL == "" {
			continue
		}
		rows = append(rows, inboundRow{SandboxID: m.SandboxID, QueueURL: m.SlackInboundQueueURL})
	}
	return rows, nil
}

// fetchSlackBotScopes calls Slack's auth.test endpoint with the given bot token
// and returns the OAuth scopes attached to the token. Slack returns scopes via
// the X-OAuth-Scopes response header (a comma-separated list).
//
// Used as the production implementation of DoctorDeps.SlackAuthTestScopes.
// Errors when the request fails; returns an empty slice when the header is
// absent (older Slack accounts may not emit it).
func fetchSlackBotScopes(ctx context.Context, botToken string) ([]string, error) {
	if botToken == "" {
		return nil, fmt.Errorf("bot token is empty")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", slackpkg.SlackAPIBase+"/auth.test", nil)
	if err != nil {
		return nil, fmt.Errorf("build auth.test request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+botToken)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth.test request failed: %w", err)
	}
	defer resp.Body.Close()

	header := resp.Header.Get("X-OAuth-Scopes")
	if header == "" {
		return nil, nil
	}
	var scopes []string
	for _, s := range strings.Split(header, ",") {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			scopes = append(scopes, trimmed)
		}
	}
	return scopes, nil
}
