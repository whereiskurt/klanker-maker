package bridge_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// happy path: files.info returns a populated file
func TestSlackFilesInfoAdapter_OK_ReturnsEnrichedFile(t *testing.T) {
	var captured *http.Request
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"ok": true,
			"file": {
				"id": "F0B43BJ6D3Q",
				"name": "screenshot.png",
				"mimetype": "image/png",
				"url_private_download": "https://files.slack.com/files-pri/T0/F0B43BJ6D3Q/download/screenshot.png",
				"size": 1024
			}
		}`))
	}))
	defer srv.Close()

	a := &bridge.SlackFilesInfoAdapter{
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
		Tokens:     &mockTokenFetcher{token: "xoxb-test"},
	}
	got, err := a.FilesInfo(context.Background(), "F0B43BJ6D3Q")
	if err != nil {
		t.Fatalf("FilesInfo error: %v", err)
	}
	if got.ID != "F0B43BJ6D3Q" {
		t.Errorf("ID=%q want F0B43BJ6D3Q", got.ID)
	}
	if got.URLPrivateDownload == "" || !strings.Contains(got.URLPrivateDownload, "download") {
		t.Errorf("URLPrivateDownload=%q must contain download/", got.URLPrivateDownload)
	}
	if got.Name != "screenshot.png" {
		t.Errorf("Name=%q want screenshot.png", got.Name)
	}
	if got.Mimetype != "image/png" {
		t.Errorf("Mimetype=%q want image/png", got.Mimetype)
	}
	if captured.Header.Get("Authorization") != "Bearer xoxb-test" {
		t.Errorf("Authorization=%q want Bearer xoxb-test", captured.Header.Get("Authorization"))
	}
	if !strings.Contains(capturedBody, "file=F0B43BJ6D3Q") {
		t.Errorf("body=%q must contain file=F0B43BJ6D3Q", capturedBody)
	}
}

func TestSlackFilesInfoAdapter_SlackError_PropagatesReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok": false, "error": "file_not_found"}`))
	}))
	defer srv.Close()

	a := &bridge.SlackFilesInfoAdapter{
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
		Tokens:     &mockTokenFetcher{token: "xoxb-test"},
	}
	_, err := a.FilesInfo(context.Background(), "FDOESNOTEXIST")
	if err == nil {
		t.Fatal("expected error for slack ok:false, got nil")
	}
	if !strings.Contains(err.Error(), "file_not_found") {
		t.Errorf("error=%q must contain file_not_found", err.Error())
	}
}

func TestSlackFilesInfoAdapter_Non2xxStatus_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`unavailable`))
	}))
	defer srv.Close()

	a := &bridge.SlackFilesInfoAdapter{
		HTTPClient: srv.Client(),
		BaseURL:    srv.URL,
		Tokens:     &mockTokenFetcher{token: "xoxb-test"},
	}
	_, err := a.FilesInfo(context.Background(), "F1")
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("expected HTTP 503 error, got %v", err)
	}
}

func TestSlackFilesInfoAdapter_EmptyFileID_FailsFast(t *testing.T) {
	a := &bridge.SlackFilesInfoAdapter{
		HTTPClient: http.DefaultClient,
		Tokens:     &mockTokenFetcher{token: "xoxb-test"},
	}
	_, err := a.FilesInfo(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty file ID, got nil")
	}
}

func TestSlackFilesInfoAdapter_TokenFetchFail_PropagatesError(t *testing.T) {
	a := &bridge.SlackFilesInfoAdapter{
		HTTPClient: http.DefaultClient,
		Tokens:     &mockTokenFetcher{err: fmt.Errorf("ssm denied")},
	}
	_, err := a.FilesInfo(context.Background(), "F1")
	if err == nil || !strings.Contains(err.Error(), "ssm denied") {
		t.Errorf("expected token-fetch error, got %v", err)
	}
}
