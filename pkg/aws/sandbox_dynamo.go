// Package aws — sandbox_dynamo.go
// DynamoDB CRUD layer for sandbox metadata.
//
// This file provides the data access layer for the km-sandbox-metadata DynamoDB table.
// All CLI commands and Lambdas call these functions after the DynamoDB switchover.
// S3 artifacts (Terraform state, etc.) remain in S3 — only the metadata JSON record
// moves to DynamoDB for O(1) reads, atomic locking, and GSI alias resolution.
//
// Table key design:
//
//	sandbox_id (S) — hash key (no sort key)
//	alias-index GSI: alias (S) → sandbox_id, for O(1) alias resolution
//	TTL attribute: ttl_expiry (N, Unix epoch seconds) — native DynamoDB TTL
package aws

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// SandboxMetadataAPI is the narrow DynamoDB interface for sandbox metadata operations.
// Implemented by *dynamodb.Client.
type SandboxMetadataAPI interface {
	GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, input *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(ctx context.Context, input *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(ctx context.Context, input *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Scan(ctx context.Context, input *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	Query(ctx context.Context, input *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// ============================================================
// Internal DynamoDB item representation
// ============================================================

// sandboxItemDynamo is the internal struct for marshalling/unmarshalling DynamoDB items.
// Uses explicit dynamodbav tags — does NOT rely on json tag fallback (research Pitfall 1).
// TTLExpiry is stored as an int64 epoch (Number type) for native DynamoDB TTL support.
type sandboxItemDynamo struct {
	SandboxID      string `dynamodbav:"sandbox_id"`
	ProfileName    string `dynamodbav:"profile_name"`
	Substrate      string `dynamodbav:"substrate"`
	Region         string `dynamodbav:"region"`
	Status         string `dynamodbav:"status,omitempty"`
	CreatedAt      string `dynamodbav:"created_at"`
	IdleTimeout    string `dynamodbav:"idle_timeout,omitempty"`
	MaxLifetime    string `dynamodbav:"max_lifetime,omitempty"`
	CreatedBy      string `dynamodbav:"created_by,omitempty"`
	Alias          string `dynamodbav:"alias,omitempty"`
	ClonedFrom     string `dynamodbav:"cloned_from,omitempty"`
	Locked         bool   `dynamodbav:"locked,omitempty"`
	LockedAt       string `dynamodbav:"locked_at,omitempty"`
	TeardownPolicy string `dynamodbav:"teardown_policy,omitempty"`
	ExpiresAt      string `dynamodbav:"expires_at,omitempty"` // RFC3339 display-only expiry, always set when TTL configured
	// TTLExpiryEpoch is int64 so attributevalue.Marshal gives a Number.
	// However we override this with a manual AttributeValueMemberN in WriteSandboxMetadataDynamo
	// to guarantee Number type (research Pitfall: zero int64 marshals as N "0", so we manage TTL manually).
	TTLExpiryEpoch int64 `dynamodbav:"ttl_expiry,omitempty"`

	// Phase 121 — action quota + freeze quarantine.
	// Stored as flat string/bool DynamoDB attributes — mirrors the existing
	// locked/locked_at pattern used above.
	ActionLimits string `dynamodbav:"action_limits,omitempty"`
	ActionFrozen bool   `dynamodbav:"action_frozen,omitempty"`
	FrozenReason string `dynamodbav:"frozen_reason,omitempty"`
	FrozenAt     string `dynamodbav:"frozen_at,omitempty"` // RFC3339 string; *time.Time in SandboxMetadata
	FrozenBy     string `dynamodbav:"frozen_by,omitempty"`
}

// toSandboxMetadata converts an internal DynamoDB item to the public SandboxMetadata type.
func (d *sandboxItemDynamo) toSandboxMetadata() (*SandboxMetadata, error) {
	createdAt, err := time.Parse(time.RFC3339, d.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at %q: %w", d.CreatedAt, err)
	}

	meta := &SandboxMetadata{
		SandboxID:      d.SandboxID,
		ProfileName:    d.ProfileName,
		Substrate:      d.Substrate,
		Region:         d.Region,
		Status:         d.Status,
		CreatedAt:      createdAt,
		IdleTimeout:    d.IdleTimeout,
		MaxLifetime:    d.MaxLifetime,
		CreatedBy:      d.CreatedBy,
		Alias:          d.Alias,
		ClonedFrom:     d.ClonedFrom,
		Locked:         d.Locked,
		TeardownPolicy: d.TeardownPolicy,
	}

	if d.TTLExpiryEpoch != 0 {
		t := time.Unix(d.TTLExpiryEpoch, 0).UTC()
		meta.TTLExpiry = &t
	}

	if d.ExpiresAt != "" {
		if ea, err := time.Parse(time.RFC3339, d.ExpiresAt); err == nil {
			meta.ExpiresAt = &ea
			// Backfill TTLExpiry from ExpiresAt when ttl_expiry was omitted
			// (teardownPolicy=stop/retain skips the DynamoDB TTL attribute).
			if meta.TTLExpiry == nil {
				meta.TTLExpiry = meta.ExpiresAt
			}
		}
	}

	if d.LockedAt != "" {
		lockedAt, err := time.Parse(time.RFC3339, d.LockedAt)
		if err == nil {
			meta.LockedAt = &lockedAt
		}
	}

	// Phase 121 — action quota + freeze quarantine.
	meta.ActionLimits = d.ActionLimits
	meta.ActionFrozen = d.ActionFrozen
	meta.FrozenReason = d.FrozenReason
	meta.FrozenBy = d.FrozenBy
	if d.FrozenAt != "" {
		if ft, err := time.Parse(time.RFC3339, d.FrozenAt); err == nil {
			meta.FrozenAt = &ft
		}
	}

	return meta, nil
}

// metadataToRecord converts a SandboxMetadata to a SandboxRecord.
// Mirrors the conversion logic in readMetadataRecord (sandbox.go).
func metadataToRecord(meta *SandboxMetadata) SandboxRecord {
	status := meta.Status
	if status == "" {
		status = "running" // backward compat: old metadata without status field
	}
	return SandboxRecord{
		SandboxID:             meta.SandboxID,
		Profile:               meta.ProfileName,
		Substrate:             meta.Substrate,
		Region:                meta.Region,
		Status:                status,
		CreatedAt:             meta.CreatedAt,
		TTLExpiry:             meta.TTLExpiry,
		TTLRemaining:          computeTTLRemaining(meta.TTLExpiry),
		IdleTimeout:           meta.IdleTimeout,
		Alias:                 meta.Alias,
		ClonedFrom:            meta.ClonedFrom,
		Locked:                meta.Locked,
		TeardownPolicy:        meta.TeardownPolicy,
		SlackChannelID:        meta.SlackChannelID,
		SlackInboundQueueURL:  meta.SlackInboundQueueURL,
		GithubInboundQueueURL: meta.GithubInboundQueueURL,
		FailureReason:         meta.FailureReason,
		FailedAt:              meta.FailedAt,
		ActionLimits:          meta.ActionLimits,
		ActionFrozen:          meta.ActionFrozen,
		FrozenReason:          meta.FrozenReason,
		FrozenAt:              meta.FrozenAt,
		FrozenBy:              meta.FrozenBy,
	}
}

// unmarshalSandboxItem extracts a sandboxItemDynamo from a raw DynamoDB item map.
// We do this manually to handle the ttl_expiry Number attribute correctly.
func unmarshalSandboxItem(item map[string]dynamodbtypes.AttributeValue) (*sandboxItemDynamo, error) {
	d := &sandboxItemDynamo{}

	if v, ok := item["sandbox_id"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.SandboxID = sv.Value
		}
	}
	if v, ok := item["profile_name"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.ProfileName = sv.Value
		}
	}
	if v, ok := item["substrate"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.Substrate = sv.Value
		}
	}
	if v, ok := item["region"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.Region = sv.Value
		}
	}
	if v, ok := item["status"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.Status = sv.Value
		}
	}
	if v, ok := item["created_at"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.CreatedAt = sv.Value
		}
	}
	if v, ok := item["idle_timeout"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.IdleTimeout = sv.Value
		}
	}
	if v, ok := item["max_lifetime"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.MaxLifetime = sv.Value
		}
	}
	if v, ok := item["created_by"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.CreatedBy = sv.Value
		}
	}
	if v, ok := item["alias"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.Alias = sv.Value
		}
	}
	if v, ok := item["cloned_from"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.ClonedFrom = sv.Value
		}
	}
	if v, ok := item["locked"]; ok {
		if bv, ok := v.(*dynamodbtypes.AttributeValueMemberBOOL); ok {
			d.Locked = bv.Value
		}
	}
	if v, ok := item["locked_at"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.LockedAt = sv.Value
		}
	}
	if v, ok := item["teardown_policy"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.TeardownPolicy = sv.Value
		}
	}
	if v, ok := item["expires_at"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.ExpiresAt = sv.Value
		}
	}
	// ttl_expiry is stored as Number (epoch seconds)
	if v, ok := item["ttl_expiry"]; ok {
		if nv, ok := v.(*dynamodbtypes.AttributeValueMemberN); ok {
			epoch, err := strconv.ParseInt(nv.Value, 10, 64)
			if err == nil {
				d.TTLExpiryEpoch = epoch
			}
		}
	}
	// Phase 121 — action quota + freeze quarantine.
	if v, ok := item["action_limits"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.ActionLimits = sv.Value
		}
	}
	if v, ok := item["action_frozen"]; ok {
		if bv, ok := v.(*dynamodbtypes.AttributeValueMemberBOOL); ok {
			d.ActionFrozen = bv.Value
		}
	}
	if v, ok := item["frozen_reason"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.FrozenReason = sv.Value
		}
	}
	if v, ok := item["frozen_at"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.FrozenAt = sv.Value
		}
	}
	if v, ok := item["frozen_by"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			d.FrozenBy = sv.Value
		}
	}

	return d, nil
}

// unmarshalSlackFields reads Phase 63 Slack fields from a raw DynamoDB item into SandboxMetadata.
// Called by ReadSandboxMetadataDynamo and ListAllSandboxesByDynamo after toSandboxMetadata().
func unmarshalSlackFields(item map[string]dynamodbtypes.AttributeValue, meta *SandboxMetadata) {
	if v, ok := item["slack_channel_id"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			meta.SlackChannelID = sv.Value
		}
	}
	if v, ok := item["slack_per_sandbox"]; ok {
		if bv, ok := v.(*dynamodbtypes.AttributeValueMemberBOOL); ok {
			meta.SlackPerSandbox = bv.Value
		}
	}
	if v, ok := item["slack_archive_on_destroy"]; ok {
		if bv, ok := v.(*dynamodbtypes.AttributeValueMemberBOOL); ok {
			val := bv.Value
			meta.SlackArchiveOnDestroy = &val
		}
	}
	if v, ok := item["slack_inbound_queue_url"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			meta.SlackInboundQueueURL = sv.Value
		}
	}
	// Phase 91.5 per-sandbox overrides. create_slack_inbound.go writes these as
	// strings ("true"/"false") via UpdateSandboxAttr; marshalSandboxItem writes
	// BOOL. Tolerate both so the round-trip survives regardless of the writer.
	meta.SlackMentionOnly = readTriStateBool(item, "slack_mention_only")
	meta.SlackReactAlways = readTriStateBool(item, "slack_react_always")
	// Phase 118: per-sandbox inbound allow-list. Stored as comma-joined S attribute.
	// Absent or empty → meta.SlackAllow remains nil (fall-back signal: use install-level
	// default). This path tolerates both BOOL-typed stray writes and S (the canonical
	// writer).
	if v, ok := item["slack_allow"].(*dynamodbtypes.AttributeValueMemberS); ok && v.Value != "" {
		meta.SlackAllow = strings.Split(v.Value, ",")
	}
}

// readTriStateBool reads a tri-state *bool DynamoDB attribute that may have been
// written either as a native BOOL (marshalSandboxItem) or as a string
// "true"/"false" (UpdateSandboxAttr in create_slack_inbound.go). Returns nil when
// the attribute is absent or unrecognised — nil signals "fall back to default".
func readTriStateBool(item map[string]dynamodbtypes.AttributeValue, key string) *bool {
	switch v := item[key].(type) {
	case *dynamodbtypes.AttributeValueMemberBOOL:
		b := v.Value
		return &b
	case *dynamodbtypes.AttributeValueMemberS:
		switch v.Value {
		case "true":
			b := true
			return &b
		case "false":
			b := false
			return &b
		}
	}
	return nil
}

// unmarshalGitHubFields reads Phase 97 GitHub inbound fields from a raw DynamoDB
// item into SandboxMetadata. Called by ReadSandboxMetadataDynamo and
// ListAllSandboxesByDynamo after toSandboxMetadata() + unmarshalSlackFields().
func unmarshalGitHubFields(item map[string]dynamodbtypes.AttributeValue, meta *SandboxMetadata) {
	if v, ok := item["github_inbound_queue_url"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			meta.GithubInboundQueueURL = sv.Value
		}
	}
}

// unmarshalFrozenFields reads Phase 121 action-quota / freeze-quarantine fields from
// a raw DynamoDB item into SandboxMetadata. Called by ReadSandboxMetadataDynamo and
// ListAllSandboxesByDynamo (and ListAllSandboxMetadataDynamo) after toSandboxMetadata().
// The core struct fields (ActionLimits, ActionFrozen, FrozenReason, FrozenAt, FrozenBy)
// are already populated by unmarshalSandboxItem → toSandboxMetadata; this helper exists
// as the canonical call-site annotation mirroring unmarshalSlackFields / unmarshalGitHubFields
// so that future callers have a single, named hook to extend. It is a no-op for attrs
// already handled by toSandboxMetadata (avoiding duplicate assignment).
//
// NOTE: The actual parsing happens inside unmarshalSandboxItem + toSandboxMetadata
// because action_frozen/frozen_* are declared on sandboxItemDynamo (unlike the Phase 63/97
// out-of-band fields that bypass the struct). This function therefore performs a
// defensive pass for any call-site that skips sandboxItemDynamo entirely.
func unmarshalFrozenFields(item map[string]dynamodbtypes.AttributeValue, meta *SandboxMetadata) {
	// action_limits (S, omit when empty)
	if meta.ActionLimits == "" {
		if v, ok := item["action_limits"].(*dynamodbtypes.AttributeValueMemberS); ok {
			meta.ActionLimits = v.Value
		}
	}
	// action_frozen (BOOL, omit when false)
	if !meta.ActionFrozen {
		if bv, ok := item["action_frozen"].(*dynamodbtypes.AttributeValueMemberBOOL); ok {
			meta.ActionFrozen = bv.Value
		}
	}
	// frozen_reason (S)
	if meta.FrozenReason == "" {
		if v, ok := item["frozen_reason"].(*dynamodbtypes.AttributeValueMemberS); ok {
			meta.FrozenReason = v.Value
		}
	}
	// frozen_at (S, RFC3339 → *time.Time)
	if meta.FrozenAt == nil {
		if v, ok := item["frozen_at"].(*dynamodbtypes.AttributeValueMemberS); ok && v.Value != "" {
			if t, err := time.Parse(time.RFC3339, v.Value); err == nil {
				meta.FrozenAt = &t
			}
		}
	}
	// frozen_by (S)
	if meta.FrozenBy == "" {
		if v, ok := item["frozen_by"].(*dynamodbtypes.AttributeValueMemberS); ok {
			meta.FrozenBy = v.Value
		}
	}
}

// unmarshalFailureFields reads Phase 77 failure-discoverability fields from a raw
// DynamoDB item into SandboxMetadata. Called by ReadSandboxMetadataDynamo and
// ListAllSandboxesByDynamo after toSandboxMetadata().
func unmarshalFailureFields(item map[string]dynamodbtypes.AttributeValue, meta *SandboxMetadata) {
	if v, ok := item["failure_reason"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			meta.FailureReason = sv.Value
		}
	}
	if v, ok := item["failed_at"]; ok {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok && sv.Value != "" {
			t, err := time.Parse(time.RFC3339, sv.Value)
			if err == nil {
				meta.FailedAt = &t
			}
		}
	}
}

// marshalSandboxItem converts a SandboxMetadata to a raw DynamoDB item map.
// Manually builds the item to guarantee correct attribute types — in particular:
//   - ttl_expiry: AttributeValueMemberN (Number, Unix epoch) for DynamoDB TTL
//   - alias: omitted entirely when empty (prevents GSI pollution — research Pitfall 5)
func marshalSandboxItem(meta *SandboxMetadata) map[string]dynamodbtypes.AttributeValue {
	item := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":   &dynamodbtypes.AttributeValueMemberS{Value: meta.SandboxID},
		"profile_name": &dynamodbtypes.AttributeValueMemberS{Value: meta.ProfileName},
		"substrate":    &dynamodbtypes.AttributeValueMemberS{Value: meta.Substrate},
		"region":       &dynamodbtypes.AttributeValueMemberS{Value: meta.Region},
		"created_at":   &dynamodbtypes.AttributeValueMemberS{Value: meta.CreatedAt.UTC().Format(time.RFC3339)},
	}

	if meta.Status != "" {
		item["status"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.Status}
	}
	if meta.IdleTimeout != "" {
		item["idle_timeout"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.IdleTimeout}
	}
	if meta.MaxLifetime != "" {
		item["max_lifetime"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.MaxLifetime}
	}
	if meta.CreatedBy != "" {
		item["created_by"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.CreatedBy}
	}
	// alias: omit entirely when empty to prevent GSI index from storing empty-string projections
	if meta.Alias != "" {
		item["alias"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.Alias}
	}
	// cloned_from: omit when empty (no GSI, but keeps items clean — same pattern as alias)
	if meta.ClonedFrom != "" {
		item["cloned_from"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.ClonedFrom}
	}
	if meta.Locked {
		item["locked"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: true}
	}
	if meta.LockedAt != nil {
		item["locked_at"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.LockedAt.UTC().Format(time.RFC3339)}
	}
	if meta.TeardownPolicy != "" {
		item["teardown_policy"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.TeardownPolicy}
	}
	// expires_at: always store when TTL is configured (display-only, not used by DynamoDB native TTL).
	if meta.ExpiresAt != nil {
		item["expires_at"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.ExpiresAt.UTC().Format(time.RFC3339)}
	} else if meta.TTLExpiry != nil {
		// Backward compat: derive expires_at from TTLExpiry if not explicitly set.
		item["expires_at"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.TTLExpiry.UTC().Format(time.RFC3339)}
	}
	// ttl_expiry: store as Number (N) type for DynamoDB TTL — must NOT be a String.
	// Omit when teardownPolicy is "stop" or "retain" so DynamoDB native TTL never
	// auto-deletes the record — the EventBridge schedule handles lifecycle actions.
	if meta.TTLExpiry != nil && meta.TeardownPolicy != "stop" && meta.TeardownPolicy != "retain" {
		item["ttl_expiry"] = &dynamodbtypes.AttributeValueMemberN{
			Value: strconv.FormatInt(meta.TTLExpiry.Unix(), 10),
		}
	}

	// Phase 63 — Slack notification metadata.
	if meta.SlackChannelID != "" {
		item["slack_channel_id"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.SlackChannelID}
	}
	if meta.SlackPerSandbox {
		item["slack_per_sandbox"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: true}
	}
	// slack_archive_on_destroy: only store when explicitly set (nil = default, omit).
	// This preserves round-trip semantics: nil in → nil out, &bool in → &bool out.
	if meta.SlackArchiveOnDestroy != nil {
		item["slack_archive_on_destroy"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: *meta.SlackArchiveOnDestroy}
	}

	// Phase 67 — Slack-inbound metadata. WriteSandboxMetadataDynamo uses PutItem
	// (full replace), so any read-modify-write path (resume.go TTL recreation,
	// extend.go, ttl-handler Lambda, etc.) silently dropped this field if it
	// wasn't included here. The bridge Lambda then warned "unknown channel or
	// inbound disabled" and dropped Slack messages — observed on l11 after a
	// TTL/extend write between successful turns. Symmetric with unmarshal at
	// line ~254.
	if meta.SlackInboundQueueURL != "" {
		item["slack_inbound_queue_url"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.SlackInboundQueueURL}
	}

	// Phase 91.5 per-sandbox overrides. Only written when explicitly set (nil =
	// omit = "fall back to install default"). Same nil/&true/&false round-trip
	// semantics as slack_archive_on_destroy above. Written as BOOL; the bridge's
	// FetchByChannel and unmarshalSlackFields both tolerate BOOL or string. Must
	// be emitted here or read-modify-write paths (resume/extend/ttl-handler)
	// silently strip the override on the next full-row PutItem.
	if meta.SlackMentionOnly != nil {
		item["slack_mention_only"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: *meta.SlackMentionOnly}
	}
	if meta.SlackReactAlways != nil {
		item["slack_react_always"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: *meta.SlackReactAlways}
	}
	// Phase 118: per-sandbox inbound allow-list. Stored as comma-joined S attribute
	// (NOT StringSet/SS) so UpdateSandboxAttr (string-only) can write it and the
	// bridge's FetchByChannel can read it uniformly. Absent (len==0) signals "use
	// install-level default". Must be emitted here so read-modify-write paths
	// (resume/extend/ttl-handler) do NOT strip it on the next full-row PutItem.
	if len(meta.SlackAllow) > 0 {
		item["slack_allow"] = &dynamodbtypes.AttributeValueMemberS{Value: strings.Join(meta.SlackAllow, ",")}
	}

	// Phase 97 — GitHub inbound metadata. Symmetric with unmarshalGitHubFields.
	// Must be emitted here or read-modify-write paths (resume/extend/ttl-handler)
	// silently strip the queue URL on the next full-row PutItem (the
	// project_sandboxmetadata_lossy_roundtrip footgun).
	if meta.GithubInboundQueueURL != "" {
		item["github_inbound_queue_url"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.GithubInboundQueueURL}
	}

	// Phase 77 — failure discoverability. Only written when failure_reason is
	// non-empty (i.e. sandbox failed). Symmetric with unmarshalFailureFields.
	if meta.FailureReason != "" {
		item["failure_reason"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.FailureReason}
	}
	if meta.FailedAt != nil {
		item["failed_at"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.FailedAt.UTC().Format(time.RFC3339)}
	}

	// Phase 121 — action quota + freeze quarantine.
	// Must be emitted here so read-modify-write paths (resume/extend/ttl-handler
	// full-row PutItem) do NOT silently strip them on the next write
	// (project_sandboxmetadata_lossy_roundtrip footgun).
	if meta.ActionLimits != "" {
		item["action_limits"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.ActionLimits}
	}
	if meta.ActionFrozen {
		item["action_frozen"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: true}
	}
	if meta.FrozenReason != "" {
		item["frozen_reason"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.FrozenReason}
	}
	if meta.FrozenAt != nil {
		item["frozen_at"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.FrozenAt.UTC().Format(time.RFC3339)}
	}
	if meta.FrozenBy != "" {
		item["frozen_by"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.FrozenBy}
	}

	return item
}

// ============================================================
// Exported CRUD functions
// ============================================================

// ReadSandboxMetadataDynamo retrieves a sandbox metadata record from DynamoDB by sandbox_id.
// Returns ErrSandboxNotFound when the item does not exist (0 attributes in response).
func ReadSandboxMetadataDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID string) (*SandboxMetadata, error) {
	out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get sandbox metadata for %s: %w", sandboxID, err)
	}
	if len(out.Item) == 0 {
		return nil, fmt.Errorf("%w: no DynamoDB record for sandbox %s", ErrSandboxNotFound, sandboxID)
	}

	d, err := unmarshalSandboxItem(out.Item)
	if err != nil {
		return nil, fmt.Errorf("unmarshal sandbox metadata for %s: %w", sandboxID, err)
	}

	meta, err := d.toSandboxMetadata()
	if err != nil {
		return nil, fmt.Errorf("convert sandbox metadata for %s: %w", sandboxID, err)
	}

	unmarshalSlackFields(out.Item, meta)
	unmarshalGitHubFields(out.Item, meta)
	unmarshalFailureFields(out.Item, meta)
	unmarshalFrozenFields(out.Item, meta)
	return meta, nil
}

// WriteSandboxMetadataDynamo stores or replaces a sandbox metadata record in DynamoDB.
// ttl_expiry is stored as Number (Unix epoch seconds) for native DynamoDB TTL.
// alias is omitted from the item when empty to prevent GSI pollution.
func WriteSandboxMetadataDynamo(ctx context.Context, client SandboxMetadataAPI, tableName string, meta *SandboxMetadata) error {
	item := marshalSandboxItem(meta)

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: awssdk.String(tableName),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("write sandbox metadata for %s: %w", meta.SandboxID, err)
	}
	return nil
}

// DeleteSandboxMetadataDynamo removes a sandbox metadata record from DynamoDB.
// Idempotent — DynamoDB DeleteItem is a no-op when the key does not exist.
func DeleteSandboxMetadataDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID string) error {
	_, err := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
	})
	if err != nil {
		return fmt.Errorf("delete sandbox metadata for %s: %w", sandboxID, err)
	}
	return nil
}

// ListAllSandboxesByDynamo scans the km-sandbox-metadata table and returns all sandbox records.
// Paginates using LastEvaluatedKey until all pages are consumed (research Pitfall 7).
func ListAllSandboxesByDynamo(ctx context.Context, client SandboxMetadataAPI, tableName string) ([]SandboxRecord, error) {
	var records []SandboxRecord
	var lastKey map[string]dynamodbtypes.AttributeValue

	for {
		input := &dynamodb.ScanInput{
			TableName: awssdk.String(tableName),
		}
		if len(lastKey) > 0 {
			input.ExclusiveStartKey = lastKey
		}

		out, err := client.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("scan sandbox metadata table %s: %w", tableName, err)
		}

		for _, item := range out.Items {
			d, err := unmarshalSandboxItem(item)
			if err != nil {
				// Skip malformed items rather than aborting the whole list
				continue
			}
			meta, err := d.toSandboxMetadata()
			if err != nil {
				continue
			}
			unmarshalSlackFields(item, meta)
			unmarshalGitHubFields(item, meta)
			unmarshalFailureFields(item, meta)
			unmarshalFrozenFields(item, meta)
			records = append(records, metadataToRecord(meta))
		}

		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		lastKey = out.LastEvaluatedKey
	}

	return records, nil
}

// ListAllSandboxMetadataDynamo scans the km-sandbox-metadata table and returns
// all records as the richer SandboxMetadata type (preserving Slack fields that
// SandboxRecord does not carry). Used by Plan 63-09 doctor Slack checks.
func ListAllSandboxMetadataDynamo(ctx context.Context, client SandboxMetadataAPI, tableName string) ([]SandboxMetadata, error) {
	var metas []SandboxMetadata
	var lastKey map[string]dynamodbtypes.AttributeValue

	for {
		input := &dynamodb.ScanInput{
			TableName: awssdk.String(tableName),
		}
		if len(lastKey) > 0 {
			input.ExclusiveStartKey = lastKey
		}

		out, err := client.Scan(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("scan sandbox metadata table %s: %w", tableName, err)
		}

		for _, item := range out.Items {
			d, err := unmarshalSandboxItem(item)
			if err != nil {
				continue
			}
			meta, err := d.toSandboxMetadata()
			if err != nil {
				continue
			}
			unmarshalSlackFields(item, meta)
			unmarshalGitHubFields(item, meta)
			unmarshalFailureFields(item, meta)
			unmarshalFrozenFields(item, meta)
			metas = append(metas, *meta)
		}

		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		lastKey = out.LastEvaluatedKey
	}

	return metas, nil
}

// ResolveSandboxAliasDynamo queries the alias-index GSI for O(1) alias resolution.
// Returns the sandbox_id of the matching sandbox, or an error if not found.
func ResolveSandboxAliasDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, alias string) (string, error) {
	out, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:              awssdk.String(tableName),
		IndexName:              awssdk.String("alias-index"),
		KeyConditionExpression: awssdk.String("alias = :alias"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":alias": &dynamodbtypes.AttributeValueMemberS{Value: alias},
		},
		Limit: awssdk.Int32(2), // fetch 2 to detect duplicates
	})
	if err != nil {
		return "", fmt.Errorf("resolve alias %q via GSI: %w", alias, err)
	}
	if len(out.Items) == 0 {
		return "", fmt.Errorf("alias %q not found: no active sandbox with that alias", alias)
	}
	if len(out.Items) > 1 {
		return "", fmt.Errorf("alias %q is ambiguous: matched multiple sandboxes", alias)
	}

	item := out.Items[0]
	sandboxIDAttr, ok := item["sandbox_id"]
	if !ok {
		return "", fmt.Errorf("alias %q: GSI item missing sandbox_id", alias)
	}
	sv, ok := sandboxIDAttr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		return "", fmt.Errorf("alias %q: sandbox_id is not a String attribute", alias)
	}
	return sv.Value, nil
}

// LockSandboxDynamo atomically locks a sandbox using a DynamoDB ConditionExpression.
// Uses a conditional UpdateItem — no read-modify-write race condition.
// Returns an "already locked" error if the sandbox is already locked (ConditionalCheckFailedException).
func LockSandboxDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		UpdateExpression: awssdk.String("SET locked = :t, locked_at = :now"),
		ConditionExpression: awssdk.String(
			"attribute_exists(sandbox_id) AND (attribute_not_exists(locked) OR locked = :f)",
		),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":t":   &dynamodbtypes.AttributeValueMemberBOOL{Value: true},
			":f":   &dynamodbtypes.AttributeValueMemberBOOL{Value: false},
			":now": &dynamodbtypes.AttributeValueMemberS{Value: now},
		},
	})
	if err != nil {
		var condFailed *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &condFailed) {
			return fmt.Errorf("sandbox %s is already locked", sandboxID)
		}
		return fmt.Errorf("lock sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// UnlockSandboxDynamo atomically unlocks a sandbox using a DynamoDB ConditionExpression.
// Returns an error if the sandbox is not locked (ConditionalCheckFailedException).
func UnlockSandboxDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID string) error {
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		UpdateExpression:    awssdk.String("SET locked = :f REMOVE locked_at"),
		ConditionExpression: awssdk.String("attribute_exists(sandbox_id) AND locked = :t"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":t": &dynamodbtypes.AttributeValueMemberBOOL{Value: true},
			":f": &dynamodbtypes.AttributeValueMemberBOOL{Value: false},
		},
	})
	if err != nil {
		var condFailed *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &condFailed) {
			return fmt.Errorf("sandbox %s is not locked", sandboxID)
		}
		return fmt.Errorf("unlock sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// FreezeSandboxDynamo atomically latches action_frozen=true on a sandbox and records
// the reason, timestamp, and agent that triggered the freeze. Idempotent: re-freezing
// an already-frozen sandbox updates reason/timestamp/by without error (no frozen-state
// guard in the ConditionExpression — mirrors the design intent where auto-freeze can
// refresh the reason on repeated quota violations). Returns an error only when the
// sandbox row does not exist.
//
// reason: human-readable description, e.g. "quota:push:daily:10"
// by: "auto:{action}:{window}" for quota-triggered freezes; "operator:{id}" for manual.
//
// Mirrors LockSandboxDynamo for client type, error-handling, and ConditionExpression style.
func FreezeSandboxDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID, reason, by string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		UpdateExpression:    awssdk.String("SET action_frozen = :t, frozen_reason = :reason, frozen_at = :now, frozen_by = :by"),
		ConditionExpression: awssdk.String("attribute_exists(sandbox_id)"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":t":      &dynamodbtypes.AttributeValueMemberBOOL{Value: true},
			":reason": &dynamodbtypes.AttributeValueMemberS{Value: reason},
			":now":    &dynamodbtypes.AttributeValueMemberS{Value: now},
			":by":     &dynamodbtypes.AttributeValueMemberS{Value: by},
		},
	})
	if err != nil {
		var condFailed *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &condFailed) {
			return fmt.Errorf("%w: no DynamoDB record for sandbox %s", ErrSandboxNotFound, sandboxID)
		}
		return fmt.Errorf("freeze sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// UnfreezeSandboxDynamo clears the action_frozen latch and removes the associated
// frozen_reason, frozen_at, frozen_by attributes in a single atomic UpdateItem.
// Idempotent: REMOVE of absent attributes is a DynamoDB no-op. Returns an error only
// when the sandbox row does not exist.
//
// Mirrors UnlockSandboxDynamo for client type, error-handling, and ConditionExpression style.
func UnfreezeSandboxDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID string) error {
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		UpdateExpression:    awssdk.String("SET action_frozen = :f REMOVE frozen_reason, frozen_at, frozen_by"),
		ConditionExpression: awssdk.String("attribute_exists(sandbox_id)"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":f": &dynamodbtypes.AttributeValueMemberBOOL{Value: false},
		},
	})
	if err != nil {
		var condFailed *dynamodbtypes.ConditionalCheckFailedException
		if errors.As(err, &condFailed) {
			return fmt.Errorf("%w: no DynamoDB record for sandbox %s", ErrSandboxNotFound, sandboxID)
		}
		return fmt.Errorf("unfreeze sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// UpdateSandboxStatusDynamo updates only the status field of a sandbox record.
// Used by pause/resume/stop/destroy for lightweight status transitions without a full PutItem.
func UpdateSandboxStatusDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID, status string) error {
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		UpdateExpression: awssdk.String("SET #s = :status"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":status": &dynamodbtypes.AttributeValueMemberS{Value: status},
		},
	})
	if err != nil {
		return fmt.Errorf("update status for sandbox %s to %q: %w", sandboxID, status, err)
	}
	return nil
}

// UpdateSandboxStatusAndReasonDynamo updates status, failure_reason, and failed_at
// in a single UpdateItem call. Used by the create-handler failure branch (Phase 77).
// reason MUST already be trimmed to ≤1024 chars by the caller.
// failedAt MUST be a UTC time.Time (caller passes time.Now().UTC()).
//
// Mirrors UpdateSandboxStatusDynamo for the same `#s = :status` aliasing reason
// (status is a DynamoDB reserved word; failure_reason and failed_at are not, per
// 77-RESEARCH.md § Pitfall 4).
func UpdateSandboxStatusAndReasonDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID, status, reason string, failedAt time.Time) error {
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		UpdateExpression: awssdk.String("SET #s = :status, failure_reason = :reason, failed_at = :ts"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":status": &dynamodbtypes.AttributeValueMemberS{Value: status},
			":reason": &dynamodbtypes.AttributeValueMemberS{Value: reason},
			":ts":     &dynamodbtypes.AttributeValueMemberS{Value: failedAt.UTC().Format(time.RFC3339)},
		},
	})
	if err != nil {
		return fmt.Errorf("update status+reason for sandbox %s to %q: %w", sandboxID, status, err)
	}
	return nil
}

// UpdateSandboxStatusAndClearTTL updates the status field AND removes the ttl_expiry
// attribute so DynamoDB's native TTL doesn't auto-delete the record. Used when
// teardownPolicy=stop to preserve the record for later resume or explicit destroy.
func UpdateSandboxStatusAndClearTTL(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID, status string) error {
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		UpdateExpression: awssdk.String("SET #s = :status REMOVE ttl_expiry"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":status": &dynamodbtypes.AttributeValueMemberS{Value: status},
		},
	})
	if err != nil {
		return fmt.Errorf("update status and clear TTL for sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// UpdateSandboxTTLDynamo bumps ONLY the TTL-related attributes (ttl_expiry,
// expires_at) on an existing km-sandboxes row via a targeted UpdateItem, leaving
// every other attribute untouched.
//
// This replaces the historical "ReadSandboxMetadataDynamo → mutate →
// WriteSandboxMetadataDynamo (full-row PutItem)" pattern used by resume's TTL
// recreation, which silently clobbered any attribute not carried by
// SandboxMetadata (e.g. the Phase 91.5 per-sandbox Slack overrides) — observed as
// a sandbox losing `mentionOnly: true` and reverting to 👀-on-everything after a
// pause/resume cycle.
//
// ttl_expiry (native DynamoDB TTL, Number) is SET only when teardownPolicy is
// neither "stop" nor "retain" — mirroring marshalSandboxItem — and REMOVEd
// otherwise so a previously-set value can't auto-reap a stop/retain sandbox.
// expires_at (display-only String) is always SET. Neither attribute name is a
// DynamoDB reserved word, so no ExpressionAttributeNames aliasing is needed.
func UpdateSandboxTTLDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID string, newExpiry time.Time, teardownPolicy string) error {
	eav := map[string]dynamodbtypes.AttributeValue{
		":ea": &dynamodbtypes.AttributeValueMemberS{Value: newExpiry.UTC().Format(time.RFC3339)},
	}
	expr := "SET expires_at = :ea"
	if teardownPolicy != "stop" && teardownPolicy != "retain" {
		expr += ", ttl_expiry = :te"
		eav[":te"] = &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(newExpiry.Unix(), 10)}
	} else {
		expr += " REMOVE ttl_expiry"
	}

	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		UpdateExpression:          awssdk.String(expr),
		ExpressionAttributeValues: eav,
	})
	if err != nil {
		return fmt.Errorf("update TTL for sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// UpdateSandboxStringAttrDynamo sets a single string attribute on an existing
// km-sandboxes DynamoDB row. Used by Phase 67 Plan 06 to write
// slack_inbound_queue_url after the SQS queue is created.
//
// When value is empty, the attribute is REMOVED from the item (cleans up
// rollback leftovers without leaving empty-string entries in DynamoDB).
func UpdateSandboxStringAttrDynamo(ctx context.Context, client SandboxMetadataAPI, tableName, sandboxID, attr, value string) error {
	if value == "" {
		// Remove the attribute entirely — matches "absent = not set" convention
		// used by last_pause_hint_ts and other optional Phase 67 attributes.
		_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: awssdk.String(tableName),
			Key: map[string]dynamodbtypes.AttributeValue{
				"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
			},
			UpdateExpression: awssdk.String("REMOVE #a"),
			ExpressionAttributeNames: map[string]string{
				"#a": attr,
			},
		})
		if err != nil {
			return fmt.Errorf("remove attr %q for sandbox %s: %w", attr, sandboxID, err)
		}
		return nil
	}
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		},
		UpdateExpression: awssdk.String("SET #a = :val"),
		ExpressionAttributeNames: map[string]string{
			"#a": attr,
		},
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":val": &dynamodbtypes.AttributeValueMemberS{Value: value},
		},
	})
	if err != nil {
		return fmt.Errorf("set attr %q for sandbox %s: %w", attr, sandboxID, err)
	}
	return nil
}
