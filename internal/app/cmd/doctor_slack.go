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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	sqssvc "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	slackpkg "github.com/whereiskurt/klanker-maker/pkg/slack"
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
	ssmPrefix string,
) CheckResult {
	botToken, _ := ssmStore.Get(ctx, ssmPrefix+"slack/bot-token", true)
	if botToken == "" {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckSkipped,
			Message: ssmPrefix + "slack/bot-token not configured — Slack integration not set up",
		}
	}

	bridgeURL, _ := ssmStore.Get(ctx, ssmPrefix+"slack/bridge-url", false)
	if bridgeURL == "" {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckWarn,
			Message: ssmPrefix + "slack/bridge-url not configured — run km slack init to deploy the bridge Lambda",
		}
	}

	channelID, _ := ssmStore.Get(ctx, ssmPrefix+"slack/shared-channel-id", false)
	if channelID == "" {
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckWarn,
			Message: ssmPrefix + "slack/shared-channel-id not configured — run km slack init",
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
		message := fmt.Sprintf("bridge returned not-OK: %s", errMsg)
		// unknown_sender (missing operator public-key row) → name the exact problem and
		// the fix instead of a generic not-OK (the incident-2026-06-14 surface).
		if hint := slackpkg.ExplainBridgeError(errMsg, strings.Trim(ssmPrefix, "/")); hint != "" {
			message = "bridge rejected operator signature: " + hint
		}
		return CheckResult{
			Name:    "Slack bot token",
			Status:  CheckWarn,
			Message: message,
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
// Cleanup gate: deletion happens only when both dryRun is false AND
// deleteSQS is true. Without --delete-sqs, the check stays report-only
// even when --dry-run=false is set globally — same explicit-opt-in pattern
// as --delete-ebs. Queues themselves are cheap (no idle cost), but a
// rogue delete during sandbox provisioning races with the 60-second SQS
// creation cooldown, so we require the operator to commit explicitly.
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
	dryRun bool,
	deleteSQS bool,
	ssmDeleter SSMDeleterAPI,
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

	// 10-minute provisioning cutoff: skip queues younger than 10 minutes since
	// km create provisions the SQS queue BEFORE the DDB sandbox row is written.
	// Without this guard, a doctor run between those two writes flags an
	// in-flight queue as orphan and (with --delete-sqs) deletes it. The 60-
	// second SQS create-cooldown then blocks the operator from recreating it
	// for the next minute. Cutoff comfortably exceeds the worst-case
	// km create gap.
	provisioningCutoff := time.Now().Add(-10 * time.Minute)

	// The shared per-install Slack-inbound DLQ ({prefix}-slack-inbound-dlq.fifo)
	// is matched by the "{prefix}-slack-inbound-" ListQueues prefix but is
	// install-scoped and intentionally has NO DDB row — sibling sandboxes redrive
	// poison envelopes into it (Phase 99.1). Exclude it so it is never reported as
	// a stale orphan and, with --delete-sqs, never deleted out from under live
	// sandboxes. km uninit owns the shared DLQ's lifecycle. See
	// destroy_slack_inbound.go CONTEXT D5.
	dlqSuffix := "/" + kmaws.SlackInboundDLQName(resourcePrefix)

	var stale []string
	skippedYoung := 0
	for _, qURL := range listOut.QueueUrls {
		if strings.HasSuffix(qURL, dlqSuffix) {
			continue
		}
		if _, ok := known[qURL]; ok {
			continue
		}
		// Fetch CreatedTimestamp; skip if too fresh. Errors (e.g. queue just
		// got deleted between ListQueues and GetQueueAttributes) — treat as
		// stale-eligible to avoid masking real orphans.
		attrs, attrErr := sqsClient.GetQueueAttributes(ctx, &sqssvc.GetQueueAttributesInput{
			QueueUrl:       awssdk.String(qURL),
			AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameCreatedTimestamp},
		})
		if attrErr == nil && attrs != nil {
			if ts, ok := attrs.Attributes[string(sqstypes.QueueAttributeNameCreatedTimestamp)]; ok {
				if epoch, err := strconv.ParseInt(ts, 10, 64); err == nil {
					createdAt := time.Unix(epoch, 0)
					if createdAt.After(provisioningCutoff) {
						skippedYoung++
						continue
					}
				}
			}
		}
		stale = append(stale, qURL)
	}
	if len(stale) == 0 {
		msg := fmt.Sprintf("all %d inbound queue(s) accounted for in DDB", len(listOut.QueueUrls))
		if skippedYoung > 0 {
			msg = fmt.Sprintf("%s (skipped %d queue(s) <10min old; possible in-flight km create)", msg, skippedYoung)
		}
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: msg,
		}
	}

	// Report-only path. Triggered when --dry-run is true (the default), OR when
	// --dry-run=false is set without the --delete-sqs opt-in. The two cases
	// produce different remediation hints so the operator knows which flag to
	// add — point dry-run users at the full --dry-run=false --delete-sqs pair,
	// and point --dry-run=false-without-opt-in users at just --delete-sqs.
	if dryRun || !deleteSQS {
		remediation := "Re-run with --dry-run=false --delete-sqs to delete the orphan queues + their SSM parameters"
		if !dryRun && !deleteSQS {
			remediation = "Add --delete-sqs to also delete the orphan queues + their SSM parameters"
		}
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("%d stale inbound queue(s) without DDB record: %s", len(stale), strings.Join(stale, ", ")),
			Remediation: remediation,
		}
	}

	// Destructive cleanup path. Best-effort per orphan; failures increment
	// skipped and the loop continues to the next queue.
	prefixSegment := resourcePrefix + "-slack-inbound-"
	deleted, skipped := 0, 0
	for _, qURL := range stale {
		if delErr := kmaws.DeleteSlackInboundQueue(ctx, sqsClient, qURL); delErr != nil {
			skipped++
			continue
		}
		deleted++
		// Best-effort SSM cleanup of the matching /sandbox/{id}/slack-inbound-queue-url
		// parameter. Only attempted after a successful queue delete so we don't
		// orphan the SSM param when the queue delete fails (the next doctor run
		// will retry both).
		if ssmDeleter == nil {
			continue
		}
		lastSlash := strings.LastIndex(qURL, "/")
		if lastSlash < 0 {
			continue
		}
		queueName := qURL[lastSlash+1:]
		sbID := strings.TrimSuffix(strings.TrimPrefix(queueName, prefixSegment), ".fifo")
		// Guard: TrimPrefix is a no-op when the prefix doesn't match — detect that
		// case (sbID == queueName) and skip the SSM step rather than build a
		// malformed parameter name.
		if sbID == "" || sbID == queueName {
			continue
		}
		paramName := kmaws.SandboxParameterPath(resourcePrefix, sbID, "slack-inbound-queue-url")
		_, ssmErr := ssmDeleter.DeleteParameter(ctx, &ssm.DeleteParameterInput{
			Name: awssdk.String(paramName),
		})
		if ssmErr != nil {
			var notFound *ssmtypes.ParameterNotFound
			if errors.As(ssmErr, &notFound) {
				// Param already gone — treat as success. No state change.
				continue
			}
			// Other SSM errors are swallowed: queue is already deleted, the
			// orphan SSM param can be reaped on the next doctor run.
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckWarn,
		Message: fmt.Sprintf("%d stale inbound queue(s) without DDB record (%d deleted, %d skipped)", len(stale), deleted, skipped),
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

	required := []string{"channels:history", "groups:history", "reactions:write", "files:read"}
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
			Remediation: "Add scopes via Slack App config → OAuth & Permissions → Bot Token Scopes, then reinstall the app to your workspace (bot token is unchanged by reinstall — no 'km slack rotate-token' needed). Run 'km doctor' again to verify.",
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Message: "Slack App has all required inbound scopes (channels:history, groups:history, reactions:write, files:read)",
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

// checkSlackPeerBridges validates the Phase 95 federated relay peer list.
//
// Returns:
//   - SKIPPED: peerBridges is nil or empty — federation is not configured.
//   - WARN:    any entry is malformed (url.Parse error or empty scheme/host).
//   - WARN:    any entry equals ownBridgeURL — self-loop detected.
//   - OK:      all entries are well-formed and none is a self-loop.
//
// ownBridgeURL is the install's own bridge /events endpoint (from SSM
// {prefix}slack/bridge-url). Pass "" when the bridge URL is unavailable —
// the self-loop check is simply skipped in that case.
func checkSlackPeerBridges(peerBridges []string, ownBridgeURL string) CheckResult {
	name := "Slack peer bridges"
	if len(peerBridges) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "slack.peer_bridges not set — federation off",
		}
	}

	var malformed, selfLoop []string
	for _, raw := range peerBridges {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Host == "" {
			malformed = append(malformed, raw)
			continue
		}
		if ownBridgeURL != "" && raw == ownBridgeURL {
			selfLoop = append(selfLoop, raw)
		}
	}

	if len(malformed) > 0 {
		return CheckResult{
			Name:   name,
			Status: CheckWarn,
			Message: fmt.Sprintf("malformed peer_bridges URL(s): %s — check km-config.yaml slack.peer_bridges",
				strings.Join(malformed, ", ")),
			Remediation: "Edit km-config.yaml slack.peer_bridges to list well-formed https:// /events URLs for each sibling km install. Run `km init --dry-run=false` to deploy the updated Lambda env.",
		}
	}
	if len(selfLoop) > 0 {
		return CheckResult{
			Name:   name,
			Status: CheckWarn,
			Message: fmt.Sprintf("self-loop detected in slack.peer_bridges: %s is this install's own bridge URL — remove it",
				strings.Join(selfLoop, ", ")),
			Remediation: "Remove this install's own bridge URL from km-config.yaml slack.peer_bridges. Each entry should be a SIBLING install's /events URL. Run `km init --dry-run=false` after fixing.",
		}
	}

	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Message: fmt.Sprintf("slack.peer_bridges configured: %d peer(s); no malformed URLs or self-loops detected", len(peerBridges)),
	}
}
