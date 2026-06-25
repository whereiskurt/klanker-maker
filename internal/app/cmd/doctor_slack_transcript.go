// Package cmd — doctor_slack_transcript.go
// Plan 68-11 — km doctor checks for Phase 68 transcript streaming health.
//
//   checkSlackTranscriptTableExists: DescribeTable on the stream-messages DDB
//     table; OK if exists + ACTIVE, WARN otherwise. Catches operators who
//     deployed Phase 68 sandboxes before running km init to provision the table.
//
//   checkSlackFilesWriteScope: probes Slack auth.test directly with the
//     operator's bot token from SSM /km/slack/bot-token, captures
//     X-OAuth-Scopes response header, returns OK if files:write present,
//     WARN otherwise. Mirrors Plan 08's cold-start logic but operator-side
//     (not Lambda-side).
//
//   checkSlackTranscriptStaleObjects: S3 ListObjectsV2 on transcripts/ prefix,
//     scans km-sandboxes DDB for live sandbox IDs, computes the difference;
//     WARN listing transcripts/{sandbox_id}/ prefixes whose sandbox no longer
//     exists. Cleanup advisory, not a failure.
//
// All checks follow the Phase 67 doctor_slack.go pattern: closure-based deps,
// nil deps → SKIPPED, never FAIL (Phase 68 is opt-in; missing transcript
// table is not a hard failure for non-opted-in deployments).
package cmd

import (
	"context"
	"fmt"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// =============================================================================
// Plan 68-11 — Slack transcript-streaming diagnostic checks
// =============================================================================

// checkSlackTranscriptTableExists verifies the Phase 68 stream-messages
// DynamoDB table is provisioned and ACTIVE. Returns:
//
//   - SKIPPED: no DDB client configured.
//   - OK: table exists with status ACTIVE.
//   - WARN: DescribeTable failed (table missing or inaccessible) — likely the
//     operator hasn't run km init since upgrading to Phase 68.
//   - WARN: table exists but is not ACTIVE (CREATING/UPDATING/DELETING).
func checkSlackTranscriptTableExists(
	ctx context.Context,
	client DynamoDescribeAPI,
	tableName string,
) CheckResult {
	name := "Slack transcript table exists"
	if client == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "DynamoDB client not configured",
		}
	}
	if tableName == "" {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "stream-messages table name not configured",
		}
	}
	out, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: awssdk.String(tableName),
	})
	if err != nil {
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("DescribeTable %s failed: %v", tableName, err),
			Remediation: "Run 'km init' to provision the Phase 68 transcript-streaming DynamoDB table",
		}
	}
	if out.Table == nil || out.Table.TableStatus != ddbtypes.TableStatusActive {
		gotStatus := "<unknown>"
		if out.Table != nil {
			gotStatus = string(out.Table.TableStatus)
		}
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: fmt.Sprintf("table %s status=%s (expected ACTIVE)", tableName, gotStatus),
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Message: fmt.Sprintf("table %s ACTIVE", tableName),
	}
}

// checkSlackFilesWriteScope probes the Slack auth.test endpoint via the
// injected getScopes callback (same callback used by checkSlackAppEventsScopes
// for Phase 67 inbound) and reports whether the bot token has files:write.
// Required by Phase 68 transcript upload (bridge ActionUpload calls
// files.upload + files.completeUploadExternal).
//
// Returns:
//   - SKIPPED: getScopes is nil (bot token not configured / Slack not set up).
//   - OK: files:write present in scopes.
//   - WARN: files:write missing (transcript upload would 400 from bridge).
//   - WARN: getScopes returned an error (do not fail doctor on auth.test outage).
func checkSlackFilesWriteScope(
	ctx context.Context,
	getScopes func(context.Context) ([]string, error),
) CheckResult {
	name := "Slack files:write scope"
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
	for _, s := range scopes {
		if s == "files:write" {
			return CheckResult{
				Name:    name,
				Status:  CheckOK,
				Message: "Slack bot has files:write scope (transcript upload supported)",
			}
		}
	}
	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     "Slack bot is missing files:write scope — transcript upload via bridge ActionUpload will fail",
		Remediation: "Add files:write via Slack App config → OAuth & Permissions → Bot Token Scopes, then reinstall the app to your workspace (bot token unchanged — no 'km slack rotate-token' needed). Run 'km doctor' again to verify.",
	}
}

// checkSlackTranscriptStaleObjects lists S3 objects under the transcripts/
// prefix, derives the unique sandbox-id sub-prefixes, intersects them with
// the live sandboxes from DDB, and warns about any orphan transcript prefix
// whose sandbox row no longer exists.
//
// Deletion is gated on both dryRun==false AND deleteS3==true — same explicit
// opt-in pattern shared with checkOrphanedArtifacts. Without --delete-s3 the
// check stays report-only even when --dry-run=false is set globally;
// transcripts can hold conversation history operators may want to retain.
//
// Cleanup advisory — never fails the doctor run. Returns:
//
//   - SKIPPED: deps are nil (no S3 client / no listSandboxes func / no bucket).
//   - OK: no transcript prefixes, or every prefix matches a live sandbox.
//   - WARN: one or more orphan prefixes detected.
func checkSlackTranscriptStaleObjects(
	ctx context.Context,
	s3Client kmaws.S3CleanupAPI,
	bucket string,
	listSandboxIDs func(context.Context) ([]string, error),
	dryRun bool,
	deleteS3 bool,
) CheckResult {
	name := "Slack transcript stale objects"
	if s3Client == nil || listSandboxIDs == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "S3 client or sandbox-list func not configured",
		}
	}
	if bucket == "" {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "artifacts bucket not configured",
		}
	}

	// List unique sandbox-id prefixes under transcripts/.
	var prefixes []string
	var continuationToken *string
	for {
		out, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            awssdk.String(bucket),
			Prefix:            awssdk.String("transcripts/"),
			Delimiter:         awssdk.String("/"),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return CheckResult{
				Name:    name,
				Status:  CheckWarn,
				Message: fmt.Sprintf("S3 ListObjectsV2 transcripts/: %v", err),
			}
		}
		for _, cp := range out.CommonPrefixes {
			if cp.Prefix != nil {
				prefixes = append(prefixes, *cp.Prefix)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		continuationToken = out.NextContinuationToken
	}

	if len(prefixes) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: "no transcript prefixes in S3",
		}
	}

	// Build set of live sandbox IDs.
	liveIDs, err := listSandboxIDs(ctx)
	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: fmt.Sprintf("list sandboxes failed: %v", err),
		}
	}
	liveSet := make(map[string]struct{}, len(liveIDs))
	for _, id := range liveIDs {
		liveSet[id] = struct{}{}
	}

	var stale []string
	for _, p := range prefixes {
		// p is "transcripts/sb-abc/", extract the sandbox ID.
		trimmed := strings.TrimPrefix(p, "transcripts/")
		sid := strings.TrimSuffix(trimmed, "/")
		if sid == "" {
			continue
		}
		if _, alive := liveSet[sid]; !alive {
			stale = append(stale, p)
		}
	}
	if len(stale) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("%d transcript prefix(es); none stale", len(prefixes)),
		}
	}

	// Report-only path. Triggered when --dry-run is true, OR when
	// --dry-run=false is set without the --delete-s3 opt-in. Two-flavored
	// remediation: dry-run users get the full pair; --dry-run=false-without-
	// opt-in users only get told to add --delete-s3.
	if dryRun || !deleteS3 {
		remediation := "Re-run with --dry-run=false --delete-s3 to delete the orphan transcript objects"
		if !dryRun && !deleteS3 {
			remediation = "Add --delete-s3 to also delete the orphan transcript objects"
		}
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("%d stale transcript prefix(es): %s", len(stale), strings.Join(stale, ", ")),
			Remediation: remediation,
		}
	}

	// Destructive cleanup path. Per stale prefix, paginate
	// ListObjectsV2 (no delimiter) to collect every object key, then batch
	// DeleteObjects in groups of 1000 (S3 API limit). Per-prefix failures
	// don't abort the loop.
	deleted, skipped, objectsDeleted := 0, 0, 0
	for _, p := range stale {
		keys, listErr := listAllKeysUnderPrefix(ctx, s3Client, bucket, p)
		if listErr != nil {
			skipped++
			continue
		}
		if len(keys) == 0 {
			// Empty prefix (no objects to delete) — count as cleaned.
			deleted++
			continue
		}
		prefixOK := true
		for batchStart := 0; batchStart < len(keys); batchStart += 1000 {
			end := batchStart + 1000
			if end > len(keys) {
				end = len(keys)
			}
			objs := make([]s3types.ObjectIdentifier, 0, end-batchStart)
			for _, k := range keys[batchStart:end] {
				objs = append(objs, s3types.ObjectIdentifier{Key: awssdk.String(k)})
			}
			_, delErr := s3Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: awssdk.String(bucket),
				Delete: &s3types.Delete{
					Objects: objs,
					Quiet:   awssdk.Bool(true),
				},
			})
			if delErr != nil {
				prefixOK = false
				break
			}
			objectsDeleted += end - batchStart
		}
		if prefixOK {
			deleted++
		} else {
			skipped++
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckWarn,
		Message: fmt.Sprintf("%d stale transcript prefix(es) (%d deleted, %d skipped, %d objects total)", len(stale), deleted, skipped, objectsDeleted),
	}
}

// checkSlackUsersReadEmailScope verifies the bot has the users:read.email scope
// required by the EnsureMemberByEmail orchestrator (Phase 72). Without this
// scope, `km slack invite` and `km create` auto-invite both fail with cryptic
// `missing_scope` errors. This check pre-empts that by surfacing scope drift at
// `km doctor` time.
//
// Mirrors checkSlackFilesWriteScope verbatim — same closure-injection pattern,
// same dep shape (getScopes func), same status semantics.
//
// Returns:
//   - SKIPPED: getScopes is nil (bot token not configured / Slack not set up).
//   - OK: users:read.email present in scopes.
//   - WARN: users:read.email missing (auto-invite and km slack invite would fail
//     with missing_scope at runtime).
//   - WARN: getScopes returned an error (do not fail doctor on auth.test outage).
func checkSlackUsersReadEmailScope(
	ctx context.Context,
	getScopes func(context.Context) ([]string, error),
) CheckResult {
	name := "Slack users:read.email scope"
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
	for _, s := range scopes {
		if s == "users:read.email" {
			return CheckResult{
				Name:    name,
				Status:  CheckOK,
				Message: "Slack bot has users:read.email scope (auto-invite supported)",
			}
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckWarn,
		Message: "Slack bot is missing users:read.email scope — `km slack invite` and `km create` auto-invite will fail with missing_scope",
		Remediation: "Run `km slack manifest > app.json`, update the Slack App's bot scopes from app.json " +
			"(Slack Admin → Apps → your app → OAuth & Permissions → Bot Token Scopes → add users:read.email), " +
			"reinstall the app to your workspace, then `km slack rotate-token --bot-token <new-token>`. " +
			"Run `km doctor` again to verify.",
	}
}

// checkSlackUsersReadScope verifies the bot has the base users:read scope, the
// REQUIRED companion of users:read.email. Slack treats the .email variant as an
// add-on to users:read — the email field is only readable when both are granted,
// and users.lookupByEmail (the EnsureMemberByEmail invite path) needs it. A bot
// that somehow carries users:read.email WITHOUT users:read still fails invites, so
// this check catches that drift independently of checkSlackUsersReadEmailScope.
//
// Mirrors checkSlackUsersReadEmailScope verbatim — same closure-injection pattern,
// same dep shape (getScopes func), same status semantics.
//
// Returns:
//   - SKIPPED: getScopes is nil (bot token not configured / Slack not set up).
//   - OK: users:read present in scopes.
//   - WARN: users:read missing (email-based invites fail even if users:read.email
//     appears granted).
//   - WARN: getScopes returned an error (do not fail doctor on auth.test outage).
func checkSlackUsersReadScope(
	ctx context.Context,
	getScopes func(context.Context) ([]string, error),
) CheckResult {
	name := "Slack users:read scope"
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
	for _, s := range scopes {
		if s == "users:read" {
			return CheckResult{
				Name:    name,
				Status:  CheckOK,
				Message: "Slack bot has users:read scope (companion of users:read.email)",
			}
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckWarn,
		Message: "Slack bot is missing users:read scope — the required companion of users:read.email; email-based invites fail even when users:read.email is granted",
		Remediation: "Run `km slack manifest > app.json`, update the Slack App's bot scopes from app.json " +
			"(Slack Admin → Apps → your app → OAuth & Permissions → Bot Token Scopes → add users:read), " +
			"then reinstall the app to your workspace (the bot token is unchanged — no `km slack rotate-token` needed). " +
			"Run `km doctor` again to verify.",
	}
}

// checkSlackGroupsReadScope verifies the bot has groups:read — the scope that
// gates reading PRIVATE channel metadata (conversations.info/.list/.members on a
// private channel). Phase 118 made private per-sandbox channels first-class
// (notification.slack.private), so without groups:read the bridge can create and
// post to a private channel but km can no longer INSPECT it: the dead-channel
// doctor checks, `km slack repair`, `km slack adopt`, and Phase 104 alias-reuse
// validation all blind-spot or fail-soft on private channels. It is in the
// rendered `km slack manifest`, so an install that predates it just needs a
// reinstall (the bot token is unchanged — no `km slack rotate-token` needed).
//
// Mirrors checkSlackUsersReadScope verbatim — same closure-injection pattern,
// same dep shape (getScopes func), same status semantics.
//
// Returns:
//   - SKIPPED: getScopes is nil (bot token not configured / Slack not set up).
//   - OK: groups:read present in scopes.
//   - WARN: groups:read missing (private-channel inspection blind; reinstall needed).
//   - WARN: getScopes returned an error (do not fail doctor on auth.test outage).
func checkSlackGroupsReadScope(
	ctx context.Context,
	getScopes func(context.Context) ([]string, error),
) CheckResult {
	name := "Slack groups:read scope"
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
	for _, s := range scopes {
		if s == "groups:read" {
			return CheckResult{
				Name:    name,
				Status:  CheckOK,
				Message: "Slack bot has groups:read scope (private-channel inspection supported)",
			}
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckWarn,
		Message: "Slack bot is missing groups:read scope — private channels (notification.slack.private, Phase 118) cannot be inspected: dead-channel checks, km slack repair/adopt, and alias-reuse validation are blind to them",
		Remediation: "Run `km slack manifest > app.json`, update the Slack App's bot scopes from app.json " +
			"(Slack Admin → Apps → your app → OAuth & Permissions → Bot Token Scopes → add groups:read), " +
			"then reinstall the app to your workspace (the bot token is unchanged — no `km slack rotate-token` needed). " +
			"Run `km doctor` again to verify.",
	}
}

// checkSlackBotUserIDCached verifies the {prefix}slack/bot-user-id SSM cache
// is populated when mention-only mode is effective for at least one local
// profile (Phase 91). Without the cached value, the bridge's mention-scan
// falls back to a live auth.test call on cold-start, which is less reliable
// and adds latency.
//
// Mirrors checkSlackUsersReadEmailScope verbatim — same closure-injection
// pattern, same dep shape (getUID func), same status semantics.
//
// Returns:
//   - SKIPPED: getUID is nil (Slack not configured OR no profile has mention-only effective).
//   - OK: {prefix}slack/bot-user-id is set to a non-empty user_id.
//   - WARN: parameter is empty (km slack init must be re-run to cache it).
//   - WARN: SSM read failed transiently (do NOT fail doctor on a fetch error).
func checkSlackBotUserIDCached(
	ctx context.Context,
	ssmPrefix string,
	getUID func(context.Context) (string, error),
) CheckResult {
	name := "Slack bot-user-id cache"
	if getUID == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "no local profile has mention-only effective, or Slack not configured",
		}
	}
	uid, err := getUID(ctx)
	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: fmt.Sprintf("could not read %sslack/bot-user-id: %v", ssmPrefix, err),
		}
	}
	if uid == "" {
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("%sslack/bot-user-id not cached — bridge mention-scan will fall back to live auth.test on every cold-start", ssmPrefix),
			Remediation: "Run `km slack init --force` (or `km slack rotate-token --bot-token <token>`) to re-capture and cache the bot user_id.",
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Message: fmt.Sprintf("Slack bot user_id cached at %sslack/bot-user-id (%s)", ssmPrefix, uid),
	}
}

// listAllKeysUnderPrefix paginates ListObjectsV2 (no delimiter) and returns
// every object key under the given prefix. Used by the Plan quick-7 cleanup
// path to enumerate keys before batched DeleteObjects.
func listAllKeysUnderPrefix(ctx context.Context, c kmaws.S3CleanupAPI, bucket, prefix string) ([]string, error) {
	var keys []string
	var token *string
	for {
		out, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            awssdk.String(bucket),
			Prefix:            awssdk.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, obj := range out.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		token = out.NextContinuationToken
	}
	return keys, nil
}

