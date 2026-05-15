package bridge_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// ============================================================
// Mock S3PutObjectAPI — records all PutObject calls.
// ============================================================

type mockS3Put struct {
	calls []s3PutCall
	err   error
}

type s3PutCall struct {
	bucket      string
	key         string
	contentType string
	body        []byte
}

func (m *mockS3Put) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	var ct string
	if params.ContentType != nil {
		ct = *params.ContentType
	}
	var bkt string
	if params.Bucket != nil {
		bkt = *params.Bucket
	}
	var key string
	if params.Key != nil {
		key = *params.Key
	}
	var body []byte
	if params.Body != nil {
		body, _ = io.ReadAll(params.Body)
	}
	// Record the call first, then return any configured error.
	m.calls = append(m.calls, s3PutCall{bucket: bkt, key: key, contentType: ct, body: body})
	if m.err != nil {
		return nil, m.err
	}
	return &s3.PutObjectOutput{}, nil
}

// ============================================================
// Mock BotTokenFetcher — returns a fixed token.
// ============================================================

type mockTokenFetcher struct {
	token string
	err   error
}

func (m *mockTokenFetcher) Fetch(ctx context.Context) (string, error) {
	return m.token, m.err
}

// ============================================================
// Helper: build an S3FileDownloader wired to test doubles.
// ============================================================

func newTestDownloader(t *testing.T, rt http.RoundTripper, s3mock *mockS3Put) *bridge.S3FileDownloader {
	t.Helper()
	return &bridge.S3FileDownloader{
		HTTPClient: &http.Client{
			// Disable automatic redirect following — the downloader manually
			// re-issues requests with the Authorization header preserved.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: rt,
		},
		S3:     s3mock,
		Bucket: "test-bucket",
		Tokens: &mockTokenFetcher{token: "xoxb-test"},
	}
}

// ============================================================
// Test 1: Happy path — two files downloaded and PUT to S3.
// ============================================================

func TestFileDownloader_HappyPath(t *testing.T) {
	body1 := []byte("PNG file body")
	body2 := []byte("PDF file body")
	rt := &recordingTransport{
		responses: []func(*http.Request) *http.Response{
			canned(200, nil, string(body1)),
			canned(200, nil, string(body2)),
		},
	}
	s3mock := &mockS3Put{}
	d := newTestDownloader(t, rt, s3mock)

	files := []bridge.SlackFile{
		{ID: "F001", Name: "cat.png", Mimetype: "image/png", URLPrivateDownload: "https://files.slack.com/files-pri/T0/F001/cat.png", Size: int64(len(body1))},
		{ID: "F002", Name: "dog.pdf", Mimetype: "application/pdf", URLPrivateDownload: "https://files.slack.com/files-pri/T0/F002/dog.pdf", Size: int64(len(body2))},
	}

	atts, errs, err := d.Download(context.Background(), files, "sb-test", "1700000000.000000")
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("expected no FileErrors, got %+v", errs)
	}
	if len(atts) != 2 {
		t.Fatalf("expected 2 Attachments, got %d: %+v", len(atts), atts)
	}

	// Check S3 keys.
	wantKey0 := "slack-inbound/sb-test/1700000000.000000/F001-cat.png"
	wantKey1 := "slack-inbound/sb-test/1700000000.000000/F002-dog.pdf"
	if s3mock.calls[0].key != wantKey0 {
		t.Errorf("key[0]=%q want %q", s3mock.calls[0].key, wantKey0)
	}
	if s3mock.calls[1].key != wantKey1 {
		t.Errorf("key[1]=%q want %q", s3mock.calls[1].key, wantKey1)
	}

	// Check ContentType.
	if s3mock.calls[0].contentType != "image/png" {
		t.Errorf("contentType[0]=%q want image/png", s3mock.calls[0].contentType)
	}
	if s3mock.calls[1].contentType != "application/pdf" {
		t.Errorf("contentType[1]=%q want application/pdf", s3mock.calls[1].contentType)
	}

	// Check body byte-for-byte.
	if !bytes.Equal(s3mock.calls[0].body, body1) {
		t.Errorf("body[0] mismatch: got %q want %q", s3mock.calls[0].body, body1)
	}
	if !bytes.Equal(s3mock.calls[1].body, body2) {
		t.Errorf("body[1] mismatch: got %q want %q", s3mock.calls[1].body, body2)
	}

	// Check Authorization header on both GETs.
	for i, req := range rt.requests {
		auth := req.Header.Get("Authorization")
		if auth != "Bearer xoxb-test" {
			t.Errorf("request[%d] Authorization=%q want 'Bearer xoxb-test'", i, auth)
		}
	}

	// Check Attachment fields.
	if atts[0].S3Key != wantKey0 || atts[0].OriginalName != "cat.png" || atts[0].Mimetype != "image/png" {
		t.Errorf("Attachment[0] wrong: %+v", atts[0])
	}
	if atts[1].S3Key != wantKey1 || atts[1].OriginalName != "dog.pdf" || atts[1].Mimetype != "application/pdf" {
		t.Errorf("Attachment[1] wrong: %+v", atts[1])
	}
}

// ============================================================
// Test 2: Pitfall 1 regression — redirect preserves Authorization.
// ============================================================

func TestFileDownloader_FilesSlackComRedirect_PreservesAuthHeader(t *testing.T) {
	edgeURL := "https://files-edge.slack-edge.com/secret/screenshot.png"
	body := []byte("screenshot bytes")

	rt := &recordingTransport{
		responses: []func(*http.Request) *http.Response{
			// First GET → 302 redirect to edge URL
			canned(302, map[string]string{"Location": edgeURL}, ""),
			// Second GET (to edge URL) → 200 with body
			canned(200, nil, string(body)),
		},
	}

	s3mock := &mockS3Put{}
	d := newTestDownloader(t, rt, s3mock)

	files := []bridge.SlackFile{
		{ID: "F001", Name: "screenshot.png", Mimetype: "image/png", URLPrivateDownload: "https://files.slack.com/files-pri/T0/F001/screenshot.png", Size: int64(len(body))},
	}

	atts, errs, err := d.Download(context.Background(), files, "sb-test", "1700000001.000000")
	if err != nil {
		t.Fatalf("Download error: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected FileErrors: %+v", errs)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 Attachment, got %d", len(atts))
	}

	// Both GETs must carry the Authorization header.
	if len(rt.requests) != 2 {
		t.Fatalf("expected 2 GETs (original + redirect), got %d", len(rt.requests))
	}
	for i, req := range rt.requests {
		auth := req.Header.Get("Authorization")
		if auth != "Bearer xoxb-test" {
			t.Errorf("GET[%d] to %s: Authorization=%q want 'Bearer xoxb-test' — redirect stripped auth (Pitfall 1 regression)", i, req.URL, auth)
		}
	}
}

// ============================================================
// Test 3: File > 100 MB → dropped before any HTTP call.
// ============================================================

func TestFileDownloader_Over100MB_Dropped(t *testing.T) {
	rt := &recordingTransport{}
	s3mock := &mockS3Put{}
	d := newTestDownloader(t, rt, s3mock)

	files := []bridge.SlackFile{
		{ID: "F001", Name: "big.zip", Mimetype: "application/zip", URLPrivateDownload: "https://files.slack.com/big.zip", Size: 101 * 1024 * 1024},
	}

	atts, errs, err := d.Download(context.Background(), files, "sb-test", "1700000002.000000")
	if err != nil {
		t.Fatalf("Download error: %v", err)
	}
	if len(rt.requests) != 0 {
		t.Errorf("expected 0 HTTP GETs for oversize file, got %d (cap must fire before network)", len(rt.requests))
	}
	if len(s3mock.calls) != 0 {
		t.Errorf("expected 0 S3 PutObjects for oversize file, got %d", len(s3mock.calls))
	}
	if len(atts) != 0 {
		t.Errorf("expected 0 Attachments for oversize file, got %d", len(atts))
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 FileError for oversize file, got %d: %+v", len(errs), errs)
	}
	if errs[0].OriginalName != "big.zip" {
		t.Errorf("FileError.OriginalName=%q want big.zip", errs[0].OriginalName)
	}
	want := "Skipped big.zip (101 MB > 100 MB cap)"
	if errs[0].Reason != want {
		t.Errorf("FileError.Reason=%q want %q", errs[0].Reason, want)
	}
}

// ============================================================
// Test 4: > 25 files → first 25 downloaded, rest skipped.
// ============================================================

func TestFileDownloader_Over25Files_Truncated(t *testing.T) {
	const total = 30
	// Provide 30 canned 200 responses — only the first 25 should be consumed.
	responses := make([]func(*http.Request) *http.Response, total)
	for i := range responses {
		body := fmt.Sprintf("file%d content", i)
		responses[i] = canned(200, nil, body)
	}
	rt := &recordingTransport{responses: responses}
	s3mock := &mockS3Put{}
	d := newTestDownloader(t, rt, s3mock)

	files := make([]bridge.SlackFile, total)
	for i := range files {
		files[i] = bridge.SlackFile{
			ID:                 fmt.Sprintf("F%03d", i),
			Name:               fmt.Sprintf("file%d.txt", i),
			Mimetype:           "text/plain",
			URLPrivateDownload: fmt.Sprintf("https://files.slack.com/file%d.txt", i),
			Size:               100,
		}
	}

	atts, errs, err := d.Download(context.Background(), files, "sb-test", "1700000003.000000")
	if err != nil {
		t.Fatalf("Download error: %v", err)
	}

	if len(rt.requests) != 25 {
		t.Errorf("expected 25 HTTP GETs, got %d", len(rt.requests))
	}
	if len(s3mock.calls) != 25 {
		t.Errorf("expected 25 S3 PutObjects, got %d", len(s3mock.calls))
	}
	if len(atts) != 25 {
		t.Errorf("expected 25 Attachments, got %d", len(atts))
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 FileError (count-cap), got %d", len(errs))
	}
	wantReason := "Only first 25 of 30 files attached; rest skipped"
	if errs[0].Reason != wantReason {
		t.Errorf("FileError.Reason=%q want %q", errs[0].Reason, wantReason)
	}

	// Verify FIFO order: attachment[0] corresponds to files[0].
	if atts[0].OriginalName != "file0.txt" {
		t.Errorf("Attachment[0].OriginalName=%q want file0.txt", atts[0].OriginalName)
	}
	if atts[24].OriginalName != "file24.txt" {
		t.Errorf("Attachment[24].OriginalName=%q want file24.txt", atts[24].OriginalName)
	}
}

// ============================================================
// Test 5: One file fails download → continues with remaining.
// ============================================================

func TestFileDownloader_DownloadFails_Continues(t *testing.T) {
	rt := &recordingTransport{
		responses: []func(*http.Request) *http.Response{
			canned(200, nil, "file0 body"),
			canned(500, nil, "internal server error"),
			canned(200, nil, "file2 body"),
		},
	}
	s3mock := &mockS3Put{}
	d := newTestDownloader(t, rt, s3mock)

	files := []bridge.SlackFile{
		{ID: "F000", Name: "file0.txt", Mimetype: "text/plain", URLPrivateDownload: "https://files.slack.com/f0", Size: 9},
		{ID: "F001", Name: "file1.txt", Mimetype: "text/plain", URLPrivateDownload: "https://files.slack.com/f1", Size: 9},
		{ID: "F002", Name: "file2.txt", Mimetype: "text/plain", URLPrivateDownload: "https://files.slack.com/f2", Size: 9},
	}

	atts, errs, err := d.Download(context.Background(), files, "sb-test", "1700000004.000000")
	if err != nil {
		t.Fatalf("Download error: %v", err)
	}

	// All 3 GETs attempted.
	if len(rt.requests) != 3 {
		t.Errorf("expected 3 GETs, got %d", len(rt.requests))
	}
	// Only 2 PutObjects (files[0] and files[2]).
	if len(s3mock.calls) != 2 {
		t.Errorf("expected 2 S3 PutObjects (successes only), got %d", len(s3mock.calls))
	}
	// 2 Attachments, 1 FileError.
	if len(atts) != 2 {
		t.Errorf("expected 2 Attachments, got %d", len(atts))
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 FileError (file1 failed), got %d", len(errs))
	}
	if errs[0].OriginalName != "file1.txt" {
		t.Errorf("FileError.OriginalName=%q want file1.txt", errs[0].OriginalName)
	}
	if !strings.Contains(errs[0].Reason, "status 500") {
		t.Errorf("FileError.Reason=%q should contain 'status 500'", errs[0].Reason)
	}
}

// ============================================================
// Test 6: All files fail → empty success list, errors for all.
// ============================================================

func TestFileDownloader_AllFail_ReturnsEmpty(t *testing.T) {
	rt := &recordingTransport{
		responses: []func(*http.Request) *http.Response{
			canned(500, nil, ""),
			canned(500, nil, ""),
		},
	}
	s3mock := &mockS3Put{}
	d := newTestDownloader(t, rt, s3mock)

	files := []bridge.SlackFile{
		{ID: "F001", Name: "a.png", Mimetype: "image/png", URLPrivateDownload: "https://files.slack.com/a", Size: 100},
		{ID: "F002", Name: "b.png", Mimetype: "image/png", URLPrivateDownload: "https://files.slack.com/b", Size: 100},
	}

	atts, errs, err := d.Download(context.Background(), files, "sb-test", "1700000005.000000")
	if err != nil {
		t.Fatalf("Download error: %v", err)
	}
	if atts == nil {
		t.Error("expected non-nil empty slice for Attachments, got nil")
	}
	if len(atts) != 0 {
		t.Errorf("expected 0 Attachments, got %d", len(atts))
	}
	if len(errs) != 2 {
		t.Errorf("expected 2 FileErrors, got %d", len(errs))
	}
	if len(s3mock.calls) != 0 {
		t.Errorf("expected 0 S3 PutObjects when all downloads fail, got %d", len(s3mock.calls))
	}
}

// ============================================================
// Test 7: S3 PutObject fails → treated as download failure.
// ============================================================

func TestFileDownloader_S3PutFails_TreatedAsDownloadFail(t *testing.T) {
	rt := &recordingTransport{
		responses: []func(*http.Request) *http.Response{
			canned(200, nil, "file content"),
		},
	}
	s3mock := &mockS3Put{err: fmt.Errorf("AccessDenied")}
	d := newTestDownloader(t, rt, s3mock)

	files := []bridge.SlackFile{
		{ID: "F001", Name: "photo.jpg", Mimetype: "image/jpeg", URLPrivateDownload: "https://files.slack.com/photo.jpg", Size: 12},
	}

	atts, errs, err := d.Download(context.Background(), files, "sb-test", "1700000006.000000")
	if err != nil {
		t.Fatalf("Download error: %v", err)
	}

	// HTTP GET succeeded.
	if len(rt.requests) != 1 {
		t.Errorf("expected 1 HTTP GET, got %d", len(rt.requests))
	}
	// PutObject was attempted.
	if len(s3mock.calls) != 1 {
		t.Errorf("expected 1 S3 PutObject attempt, got %d", len(s3mock.calls))
	}
	// No Attachment on S3 failure.
	if len(atts) != 0 {
		t.Errorf("expected 0 Attachments on S3 failure, got %d", len(atts))
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 FileError on S3 failure, got %d", len(errs))
	}
	if errs[0].OriginalName != "photo.jpg" {
		t.Errorf("FileError.OriginalName=%q want photo.jpg", errs[0].OriginalName)
	}
	if !strings.Contains(strings.ToLower(errs[0].Reason), "s3") {
		t.Errorf("FileError.Reason=%q should mention S3", errs[0].Reason)
	}
}

// ============================================================
// Test 8: 403 → logs Error with files:read hint, no S3 PutObject.
// ============================================================

func TestFileDownloader_403_LogsErrorAndDrops(t *testing.T) {
	logBuf, restore := captureBridgeLogger(t)
	defer restore()

	rt := &recordingTransport{
		responses: []func(*http.Request) *http.Response{
			canned(403, nil, "Forbidden"),
		},
	}
	s3mock := &mockS3Put{}
	d := newTestDownloader(t, rt, s3mock)

	files := []bridge.SlackFile{
		{ID: "F001", Name: "secret.pdf", Mimetype: "application/pdf", URLPrivateDownload: "https://files.slack.com/secret.pdf", Size: 1024},
	}

	atts, errs, err := d.Download(context.Background(), files, "sb-test", "1700000007.000000")
	if err != nil {
		t.Fatalf("Download error: %v", err)
	}

	// HTTP GET was attempted.
	if len(rt.requests) != 1 {
		t.Errorf("expected 1 HTTP GET, got %d", len(rt.requests))
	}
	// No S3 PutObject.
	if len(s3mock.calls) != 0 {
		t.Errorf("expected 0 S3 PutObjects on 403, got %d", len(s3mock.calls))
	}
	// 0 Attachments, 1 FileError.
	if len(atts) != 0 {
		t.Errorf("expected 0 Attachments on 403, got %d", len(atts))
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 FileError on 403, got %d", len(errs))
	}

	// Logger must have captured an Error-level entry mentioning files:read scope.
	logOutput := logBuf.String()
	assertContains(t, logOutput, "files:read")
}

// ============================================================
// Test 9: Filename sanitization (table-driven).
// ============================================================

func TestFileDownloader_FilenameSanitization(t *testing.T) {
	// Build a 300-byte ASCII string for truncation tests.
	longName := strings.Repeat("a", 300) + ".txt"
	// Ensure the 255-byte truncation leaves a clean result.
	// The first 255 bytes of "aaa...aaa.txt" (300+'a's) is "aaa...aaa" (255 'a's) — no dot.

	// Build a multi-byte name that fits within 255 bytes.
	// "日本語.png" = 3 runes × 3 bytes each + ".png" = 13 bytes — well within limit.
	multiByteOK := "日本語.png"

	// Build a string where truncation must not split a UTF-8 multi-byte sequence.
	// 'a' repeated 253 times + "日" (3 bytes) = 256 bytes → should be truncated to 255,
	// but cutting after byte 255 would split "日". The result should be 253 'a's only.
	almostFull := strings.Repeat("a", 253) + "日" + ".png"

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"passthrough", "screenshot.png", "screenshot.png"},
		{"parent_dir_traversal", "../etc/passwd", "__etc_passwd"},
		{"slash_replaced", "foo/bar/baz.png", "foo_bar_baz.png"},
		{"backslash_replaced", `foo\bar.png`, "foo_bar.png"},
		{"nul_stripped", "name\x00.png", "name.png"},
		{"bel_stripped", "name\x07.png", "name.png"},
		{"unit_sep_stripped", "name\x1f.png", "name.png"},
		{"truncate_300_bytes", longName, strings.Repeat("a", 255)},
		{"multibyte_preserved", multiByteOK, multiByteOK},
		{"truncate_no_split_multibyte", almostFull, strings.Repeat("a", 253)},
		{"dotdot_only", "..", "_"},
		{"empty", "", "_"},
		{"dotfile", ".hidden", ".hidden"},
		{"windows_reserved", "con.txt", "con.txt"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := bridge.SanitizeFilenameForTest(tc.input)
			if got != tc.want {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
