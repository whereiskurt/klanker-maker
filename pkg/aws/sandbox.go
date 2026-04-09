// Package aws — sandbox metadata types and S3/tag-based list functions.
// SandboxMetadata is written by km create (Plan 03-04).
// SandboxRecord is the unified view used by km list and km status.
package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	tagtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

// SandboxRecord is the unified view of a sandbox used by km list and km status output.
type SandboxRecord struct {
	SandboxID    string     `json:"sandbox_id"`
	Profile      string     `json:"profile"`
	Substrate    string     `json:"substrate"`
	Region       string     `json:"region"`
	Status       string     `json:"status"`                    // "running", "stopped", "unknown"
	CreatedAt    time.Time  `json:"created_at"`
	TTLExpiry    *time.Time `json:"ttl_expiry,omitempty"`
	TTLRemaining string     `json:"ttl_remaining,omitempty"` // "1h23m" or "expired"
	IdleTimeout  string     `json:"idle_timeout,omitempty"`  // e.g. "15m", from profile
	Alias        string     `json:"alias,omitempty"`         // human-friendly alias (e.g. "orc", "wrkr-1")
	IdleRemaining string     `json:"idle_remaining,omitempty"` // "23m10s remaining" or "imminent"
	Locked       bool       `json:"locked,omitempty"`        // true if sandbox is locked against destroy/stop/pause
	Hibernation  bool       `json:"hibernation,omitempty"`   // true if EC2 instance has hibernation configured
	Resources    []string   `json:"resources,omitempty"`     // ARNs, populated in status output only
}

// S3ListAPI is the narrow interface for S3 operations needed by list functions.
// The real *s3.Client satisfies this interface directly.
type S3ListAPI interface {
	ListObjectsV2(ctx context.Context, input *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// metadataKey returns the S3 object key for a sandbox's metadata.json.
// Format: tf-km/sandboxes/<sandbox-id>/metadata.json
func metadataKey(sandboxID string) string {
	return "tf-km/sandboxes/" + sandboxID + "/metadata.json"
}

// sandboxPrefixKey returns the S3 prefix for a sandbox's state directory.
func sandboxPrefixKey(sandboxID string) string {
	return "tf-km/sandboxes/" + sandboxID + "/"
}

// computeTTLRemaining returns a human-readable TTL remaining string or "expired".
func computeTTLRemaining(ttlExpiry *time.Time) string {
	if ttlExpiry == nil {
		return ""
	}
	remaining := time.Until(*ttlExpiry)
	if remaining <= 0 {
		return "expired"
	}
	// Round to seconds, format as human-readable duration
	remaining = remaining.Round(time.Second)
	h := int(remaining.Hours())
	m := int(remaining.Minutes()) % 60
	s := int(remaining.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// ListAllSandboxesByS3 scans S3 tf-km/sandboxes/ prefix for sandbox directories,
// reads metadata.json for each, and returns a SandboxRecord list.
// If metadata.json is missing for a sandbox, the record is populated with "unknown" defaults.
func ListAllSandboxesByS3(ctx context.Context, client S3ListAPI, bucket string) ([]SandboxRecord, error) {
	const prefix = "tf-km/sandboxes/"

	output, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:    awssdk.String(bucket),
		Prefix:    awssdk.String(prefix),
		Delimiter: awssdk.String("/"),
	})
	if err != nil {
		return nil, fmt.Errorf("list S3 sandbox prefixes in %s: %w", bucket, err)
	}

	var records []SandboxRecord
	for _, cp := range output.CommonPrefixes {
		if cp.Prefix == nil {
			continue
		}
		// Extract sandbox-id from "tf-km/sandboxes/<sandbox-id>/"
		trimmed := strings.TrimPrefix(*cp.Prefix, prefix)
		sandboxID := strings.TrimSuffix(trimmed, "/")
		if sandboxID == "" {
			continue
		}

		rec := readMetadataRecord(ctx, client, bucket, sandboxID)
		// Skip sandboxes with no metadata.json — they've been destroyed
		// but have orphaned state files (e.g. github-token/terraform.tfstate).
		if rec.Status == "unknown" && rec.Profile == "unknown" {
			continue
		}
		records = append(records, rec)
	}

	return records, nil
}

// readMetadataRecord reads tf-km/sandboxes/<sandbox-id>/metadata.json from S3
// and returns a populated SandboxRecord. Falls back to "unknown" defaults on error.
func readMetadataRecord(ctx context.Context, client S3ListAPI, bucket, sandboxID string) SandboxRecord {
	key := metadataKey(sandboxID)
	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		return SandboxRecord{
			SandboxID: sandboxID,
			Profile:   "unknown",
			Substrate: "unknown",
			Region:    "unknown",
			Status:    "unknown",
		}
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return SandboxRecord{
			SandboxID: sandboxID,
			Profile:   "unknown",
			Substrate: "unknown",
			Region:    "unknown",
			Status:    "unknown",
		}
	}

	var meta SandboxMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return SandboxRecord{
			SandboxID: sandboxID,
			Profile:   "unknown",
			Substrate: "unknown",
			Region:    "unknown",
			Status:    "unknown",
		}
	}

	status := meta.Status
	if status == "" {
		status = "running" // backward compat: old metadata without status field
	}

	return SandboxRecord{
		SandboxID:    meta.SandboxID,
		Profile:      meta.ProfileName,
		Substrate:    meta.Substrate,
		Region:       meta.Region,
		Status:       status,
		CreatedAt:    meta.CreatedAt,
		TTLExpiry:    meta.TTLExpiry,
		TTLRemaining: computeTTLRemaining(meta.TTLExpiry),
		IdleTimeout:  meta.IdleTimeout,
		Alias:        meta.Alias,
		Locked:       meta.Locked,
	}
}

// ListAllSandboxesByTags uses the ResourceGroupsTagging API to find all resources
// tagged km:sandbox-id, deduplicate by sandbox ID, and return SandboxRecord stubs.
// The returned records have Resources populated but metadata fields may be "unknown"
// (no S3 metadata.json read is performed).
func ListAllSandboxesByTags(ctx context.Context, client TagAPI, bucket string) ([]SandboxRecord, error) {
	input := &resourcegroupstaggingapi.GetResourcesInput{
		TagFilters: []tagtypes.TagFilter{
			{
				Key: awssdk.String("km:sandbox-id"),
			},
		},
	}

	output, err := client.GetResources(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes by tags: %w", err)
	}

	// Deduplicate by sandbox ID and collect ARNs per sandbox
	byID := make(map[string]*SandboxRecord)
	for _, r := range output.ResourceTagMappingList {
		if r.ResourceARN == nil {
			continue
		}
		sandboxID := extractSandboxIDFromTags(r.Tags)
		if sandboxID == "" {
			continue
		}
		rec, ok := byID[sandboxID]
		if !ok {
			rec = &SandboxRecord{
				SandboxID: sandboxID,
				Profile:   "unknown",
				Substrate: "unknown",
				Region:    "unknown",
				Status:    "running",
			}
			byID[sandboxID] = rec
		}
		rec.Resources = append(rec.Resources, *r.ResourceARN)
	}

	records := make([]SandboxRecord, 0, len(byID))
	for _, rec := range byID {
		records = append(records, *rec)
	}

	return records, nil
}

// extractSandboxIDFromTags returns the value of the km:sandbox-id tag from a tag list.
func extractSandboxIDFromTags(tags []tagtypes.Tag) string {
	for _, tag := range tags {
		if tag.Key != nil && *tag.Key == "km:sandbox-id" && tag.Value != nil {
			return *tag.Value
		}
	}
	return ""
}

// ReadSandboxMetadata reads the metadata.json for a specific sandbox from S3.
// Returns ErrSandboxNotFound if the object does not exist.
func ReadSandboxMetadata(ctx context.Context, client S3ListAPI, bucket, sandboxID string) (*SandboxMetadata, error) {
	key := metadataKey(sandboxID)
	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: no metadata.json for sandbox %s: %v", ErrSandboxNotFound, sandboxID, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read metadata for sandbox %s: %w", sandboxID, err)
	}

	var meta SandboxMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse metadata for sandbox %s: %w", sandboxID, err)
	}

	return &meta, nil
}

// S3DeleteAPI is the narrow interface for deleting S3 objects.
type S3DeleteAPI interface {
	DeleteObject(ctx context.Context, input *s3.DeleteObjectInput, opts ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

// ResolveSandboxAlias scans S3 metadata to find a sandbox with the given alias.
// Returns the sandbox ID or an error if not found or if multiple sandboxes share the alias.
// TODO: For O(1) lookup, add alias to DynamoDB km-identities table with a GSI.
func ResolveSandboxAlias(ctx context.Context, client S3ListAPI, bucket, alias string) (string, error) {
	records, err := ListAllSandboxesByS3(ctx, client, bucket)
	if err != nil {
		return "", fmt.Errorf("resolve alias %q: %w", alias, err)
	}

	var matches []string
	for _, r := range records {
		if r.Alias == alias {
			matches = append(matches, r.SandboxID)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("alias %q not found: no active sandbox with that alias", alias)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("alias %q is ambiguous: matched %d sandboxes (%s)", alias, len(matches), strings.Join(matches, ", "))
	}
}

// DeleteSandboxMetadata removes the metadata.json for a sandbox from S3.
// Idempotent — does not error if the object does not exist.
func DeleteSandboxMetadata(ctx context.Context, client S3DeleteAPI, bucket, sandboxID string) error {
	key := metadataKey(sandboxID)
	_, err := client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete metadata for sandbox %s: %w", sandboxID, err)
	}
	return nil
}
