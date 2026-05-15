package bridge

// file_downloader.go — Phase 75 S3FileDownloader implementation.
//
// Pitfall 1: Go stdlib http.Client strips the Authorization header on cross-host
// 302 redirects (https://github.com/golang/go/issues/28546). Mitigation: set
// CheckRedirect = ErrUseLastResponse on the HTTPClient field, then manually
// re-issue a GET to the redirect Location with the Authorization header re-attached.
// The test TestFileDownloader_FilesSlackComRedirect_PreservesAuthHeader is a
// regression guard for this.
//
// Pitfall 2: S3 PutObject requires a re-readable io.Reader when the SDK retries.
// Mitigation: io.ReadAll the HTTP response body into a []byte buffer first, then
// wrap with bytes.NewReader for the PutObjectInput.Body.
//
// Sanitization rules for sanitizeFilename:
//   1. Replace ".." with "_" (parent-dir traversal prevention).
//   2. Replace "/" with "_" and "\" with "_" (path separator neutralization).
//   3. Strip non-printable runes (unicode.IsPrint == false).
//   4. Truncate to 255 bytes, rune-aligned (no split multi-byte sequences).
//   5. Replace empty result with "_".

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	// MaxFilesPerMessage is the maximum number of files processed per Slack message.
	// Files beyond this limit are skipped with a FileError describing the truncation.
	MaxFilesPerMessage = 25

	// MaxFileSizeBytes is the per-file size cap checked before issuing any HTTP request.
	// Files exceeding this are skipped with a FileError; no network call is made.
	MaxFileSizeBytes = 100 * 1024 * 1024 // 100 MB

	// DownloadTimeoutTotal is the total context budget for the fire-and-forget
	// goroutine in EventsHandler.Handle. Covers download + S3 put + SQS write.
	DownloadTimeoutTotal = 90 * time.Second
)

// S3PutObjectAPI is the narrow S3 interface used by S3FileDownloader.
// Mirrors S3GetObjectAPI (aws_adapters.go) for consistency.
// Both *s3.Client and mock implementations satisfy it.
type S3PutObjectAPI interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// FileDownloader is the interface EventsHandler depends on for file attachment
// handling. Allows mocking in tests without touching real S3 or Slack HTTP.
type FileDownloader interface {
	// Download downloads all files (subject to per-file and count caps), PUTs them
	// to S3, and returns the per-file Attachment records and any per-file errors.
	// The function-level error is nil even when individual files fail (failures are
	// reported in the []FileError slice). A non-nil function-level error indicates
	// a fatal precondition failure (e.g., token fetch failed).
	Download(ctx context.Context, files []SlackFile, sandboxID, threadTS string) ([]Attachment, []FileError, error)
}

// FileError records a per-file failure with a human-readable reason for the
// thread-reply warning posted before the agent sees the message.
type FileError struct {
	OriginalName string // Slack-supplied filename, used in the warning text.
	Reason       string // Human-readable description, e.g. "Skipped big.zip (101 MB > 100 MB cap)".
}

// S3FileDownloader is the production FileDownloader implementation.
// It downloads Slack file attachments from files.slack.com using the bot token
// (preserving auth across 302 redirects) and buffers + PUTs them to S3 under
// the key prefix: slack-inbound/<sandboxID>/<threadTS>/<fileID>-<sanitizedName>.
type S3FileDownloader struct {
	// HTTPClient is configured by the caller with CheckRedirect=ErrUseLastResponse
	// so that the downloader can manually re-attach the Authorization header on
	// cross-host 302 redirects (Pitfall 1 mitigation).
	HTTPClient *http.Client

	// S3 is the narrow S3 write interface. Production: *s3.Client.
	S3 S3PutObjectAPI

	// Bucket is the S3 bucket name (KM_ARTIFACTS_BUCKET).
	Bucket string

	// Tokens fetches the Slack bot token (shared with SlackPosterAdapter to
	// reuse the 15-min SSM cache). The token is fetched once per Download call.
	Tokens BotTokenFetcher
}

// Download implements FileDownloader.
// Processing order:
//  1. Enforce 25-file count cap (FIFO truncation).
//  2. For each file: enforce 100 MB size cap, download, PUT to S3.
//  3. Per-file failures are collected and returned; processing continues.
//  4. Returns (attachments, errors, nil) — function-level error is nil on
//     per-file failures because CONTEXT.md mandates "continue with what succeeded".
func (d *S3FileDownloader) Download(ctx context.Context, files []SlackFile, sandboxID, threadTS string) ([]Attachment, []FileError, error) {
	var fileErrs []FileError

	// Enforce 25-file count cap before any processing.
	if len(files) > MaxFilesPerMessage {
		total := len(files)
		fileErrs = append(fileErrs, FileError{
			Reason: fmt.Sprintf("Only first %d of %d files attached; rest skipped", MaxFilesPerMessage, total),
		})
		files = files[:MaxFilesPerMessage]
	}

	// Fetch the bot token once for all files in this batch.
	token, err := d.Tokens.Fetch(ctx)
	if err != nil {
		return []Attachment{}, fileErrs, fmt.Errorf("file_downloader: fetch bot token: %w", err)
	}

	var attachments []Attachment

	for _, file := range files {
		// Enforce per-file size cap before any HTTP call.
		fileSizeMB := file.Size / (1024 * 1024)
		if file.Size > MaxFileSizeBytes {
			fileErrs = append(fileErrs, FileError{
				OriginalName: file.Name,
				Reason:       fmt.Sprintf("Skipped %s (%d MB > 100 MB cap)", file.Name, fileSizeMB),
			})
			continue
		}

		bodyBytes, dlErr := d.downloadOneFile(ctx, token, file)
		if dlErr != nil {
			fileErrs = append(fileErrs, FileError{
				OriginalName: file.Name,
				Reason:       dlErr.Error(),
			})
			continue
		}

		key := "slack-inbound/" + sandboxID + "/" + threadTS + "/" + file.ID + "-" + sanitizeFilename(file.Name)

		// PutObject with a re-readable bytes.Reader (Pitfall 2 mitigation).
		_, putErr := d.S3.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      awssdk.String(d.Bucket),
			Key:         awssdk.String(key),
			Body:        bytes.NewReader(bodyBytes),
			ContentType: awssdk.String(file.Mimetype),
		})
		if putErr != nil {
			logger.Error("file_downloader: s3 put failed", "file_id", file.ID, "name", file.Name, "err", putErr)
			fileErrs = append(fileErrs, FileError{
				OriginalName: file.Name,
				Reason:       fmt.Sprintf("S3 upload failed for %s: %v", file.Name, putErr),
			})
			continue
		}

		attachments = append(attachments, Attachment{
			S3Key:        key,
			OriginalName: file.Name,
			Mimetype:     file.Mimetype,
		})
	}

	if attachments == nil {
		attachments = []Attachment{}
	}

	return attachments, fileErrs, nil
}

// downloadOneFile downloads a single file from Slack, handling 302 redirects
// by manually re-issuing the request with the Authorization header preserved.
//
// Pitfall 1 mitigation: the HTTPClient field has CheckRedirect=ErrUseLastResponse
// so Go's stdlib does NOT follow redirects. We detect a 301/302 response and
// manually issue a second GET to the Location URL with the Authorization header
// re-attached. Only one redirect hop is supported; a second redirect logs and fails.
func (d *S3FileDownloader) downloadOneFile(ctx context.Context, token string, file SlackFile) ([]byte, error) {
	issueGET := func(url string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return d.HTTPClient.Do(req)
	}

	resp, err := issueGET(file.URLPrivateDownload)
	if err != nil {
		return nil, fmt.Errorf("download %s (%s): %w", file.Name, file.ID, err)
	}
	defer resp.Body.Close()

	// Manual redirect handling (Pitfall 1): re-issue with Authorization header.
	if resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound {
		loc := resp.Header.Get("Location")
		if loc == "" {
			return nil, fmt.Errorf("download %s (%s): redirect with no Location header", file.Name, file.ID)
		}
		// Drain and close first response body.
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		resp, err = issueGET(loc)
		if err != nil {
			return nil, fmt.Errorf("download %s (%s): redirect follow: %w", file.Name, file.ID, err)
		}
		defer resp.Body.Close()

		// If there's another redirect, fail cleanly rather than loop.
		if resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound {
			return nil, fmt.Errorf("download %s (%s): too many redirects", file.Name, file.ID)
		}
	}

	// 401 / 403: likely missing scope. Log at Error with hint; return FileError reason.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		logger.Error("file_downloader: auth failure — check files:read scope",
			"file_id", file.ID, "name", file.Name, "status", resp.StatusCode)
		return nil, fmt.Errorf("download %s (%s): HTTP status %d (files:read scope may be missing)", file.Name, file.ID, resp.StatusCode)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download %s (%s): HTTP status %d", file.Name, file.ID, resp.StatusCode)
	}

	// Buffer the body into memory (Pitfall 2: S3 SDK retries need a re-readable Body).
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download %s (%s): read body: %w", file.Name, file.ID, err)
	}
	return buf, nil
}

// sanitizeFilename returns a safe filename component for use in S3 keys.
//
// Sanitization rules (applied in order):
//  1. Replace ".." with "_" — prevent parent-directory traversal in S3 keys.
//  2. Replace "/" with "_" and "\" with "_" — neutralize path separators.
//  3. Strip non-printable runes (unicode.IsPrint(r) == false).
//  4. Truncate to 255 bytes, rune-aligned — no split multi-byte sequences.
//  5. Return "_" if the result is empty — empty keys are unsafe.
//
// Notes:
//   - Multi-byte Unicode runes (e.g. CJK characters) are PRESERVED as long as
//     they are printable (unicode.IsPrint). They count toward the 255-byte limit.
//   - Windows reserved names (con, prn, aux, ...) are NOT filtered — only relevant
//     on NTFS/FAT, not on Linux EXT4 or in S3 object keys.
//   - Space is printable and preserved. Dotfiles (.hidden) are preserved.
//   - Only ".." (the two-dot parent-dir traversal pattern) is replaced; single
//     dots are left alone.
func sanitizeFilename(name string) string {
	// Step 1: Replace ".." with "_".
	name = strings.ReplaceAll(name, "..", "_")
	// Step 2: Replace "/" and "\" with "_".
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	// Step 3: Strip non-printable runes.
	var b strings.Builder
	for _, r := range name {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	name = b.String()
	// Step 4: Truncate to 255 bytes, rune-aligned.
	if len(name) > 255 {
		name = truncateRunesToByteLimit(name, 255)
	}
	// Step 5: Empty result → "_".
	if name == "" {
		name = "_"
	}
	return name
}

// truncateRunesToByteLimit walks the string rune-by-rune and returns the longest
// prefix whose UTF-8 byte-length does not exceed limit. No rune is split mid-byte.
func truncateRunesToByteLimit(s string, limit int) string {
	total := 0
	for i, r := range s {
		runeLen := len(string(r))
		if total+runeLen > limit {
			return s[:i]
		}
		total += runeLen
	}
	return s
}

// SanitizeFilenameForTest exports sanitizeFilename for use in package-external tests.
// This is a thin shim used exclusively by file_downloader_test.go; the production
// code only calls sanitizeFilename directly.
func SanitizeFilenameForTest(name string) string {
	return sanitizeFilename(name)
}
