package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- Mock implementations for action handlers ---

// mockDestroyer satisfies the Destroyer interface.
type mockDestroyer struct {
	calledWith string
	err        error
}

func (m *mockDestroyer) Destroy(_ context.Context, sandboxID string) error {
	m.calledWith = sandboxID
	return m.err
}

// mockTTLExtender satisfies the TTLExtender interface.
type mockTTLExtender struct {
	calledSandboxID string
	calledDuration  time.Duration
	err             error
}

func (m *mockTTLExtender) ExtendTTL(_ context.Context, sandboxID string, duration time.Duration) error {
	m.calledSandboxID = sandboxID
	m.calledDuration = duration
	return m.err
}

// mockSandboxCreator satisfies the SandboxCreator interface.
type mockSandboxCreator struct {
	calledProfile string
	err           error
}

func (m *mockSandboxCreator) Create(_ context.Context, profilePath string) error {
	m.calledProfile = profilePath
	return m.err
}

// --- Helper: build test handler with action interfaces ---

func newActionTestHandler(destroyer Destroyer, extender TTLExtender, creator SandboxCreator, profilesDir string) *Handler {
	return &Handler{
		tmpl:        buildTestTemplates(),
		profilesDir: profilesDir,
		destroyer:   destroyer,
		ttlExtender: extender,
		creator:     creator,
	}
}

// --- Destroy handler tests ---

// TestHandleDestroy_Success: POST /api/sandboxes/{id}/destroy returns 200 and calls
// Destroyer with the correct sandbox ID; HX-Trigger header is set.
func TestHandleDestroy_Success(t *testing.T) {
	mock := &mockDestroyer{}
	h := newActionTestHandler(mock, nil, nil, t.TempDir())

	req := httptest.NewRequest(http.MethodPost, "/api/sandboxes/sbx-destroy-me/destroy", nil)
	req.SetPathValue("id", "sbx-destroy-me")
	w := httptest.NewRecorder()

	h.handleDestroy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	if mock.calledWith != "sbx-destroy-me" {
		t.Errorf("expected Destroy called with 'sbx-destroy-me', got %q", mock.calledWith)
	}
	trigger := w.Header().Get("HX-Trigger")
	if trigger != "sandbox-destroyed" {
		t.Errorf("expected HX-Trigger: sandbox-destroyed, got %q", trigger)
	}
}

// TestHandleDestroy_NotFound: POST /api/sandboxes/{id}/destroy when Destroyer returns
// ErrDestroyNotFound should yield a 404.
func TestHandleDestroy_NotFound(t *testing.T) {
	mock := &mockDestroyer{err: ErrDestroyNotFound}
	h := newActionTestHandler(mock, nil, nil, t.TempDir())

	req := httptest.NewRequest(http.MethodPost, "/api/sandboxes/sbx-missing/destroy", nil)
	req.SetPathValue("id", "sbx-missing")
	w := httptest.NewRecorder()

	h.handleDestroy(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d; body: %s", w.Code, w.Body.String())
	}
}

// --- ExtendTTL handler tests ---

// TestHandleExtendTTL_Valid: PUT /api/sandboxes/{id}/ttl with valid {"duration":"2h"} returns 200.
func TestHandleExtendTTL_Valid(t *testing.T) {
	mock := &mockTTLExtender{}
	h := newActionTestHandler(nil, mock, nil, t.TempDir())

	body := bytes.NewBufferString(`{"duration":"2h"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/sandboxes/sbx-abc/ttl", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "sbx-abc")
	w := httptest.NewRecorder()

	h.handleExtendTTL(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	if mock.calledSandboxID != "sbx-abc" {
		t.Errorf("expected ExtendTTL called with 'sbx-abc', got %q", mock.calledSandboxID)
	}
	if mock.calledDuration != 2*time.Hour {
		t.Errorf("expected duration 2h, got %v", mock.calledDuration)
	}
}

// TestHandleExtendTTL_InvalidDuration: PUT with unparseable duration returns 400.
func TestHandleExtendTTL_InvalidDuration(t *testing.T) {
	mock := &mockTTLExtender{}
	h := newActionTestHandler(nil, mock, nil, t.TempDir())

	body := bytes.NewBufferString(`{"duration":"notaduration"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/sandboxes/sbx-abc/ttl", body)
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "sbx-abc")
	w := httptest.NewRecorder()

	h.handleExtendTTL(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

// --- QuickCreate handler tests ---

// TestHandleQuickCreate_Success: POST /api/sandboxes/create with a known profile returns 202.
func TestHandleQuickCreate_Success(t *testing.T) {
	profilesDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(profilesDir, "base.yaml"), []byte("spec:\n  name: base\n"), 0644); err != nil {
		t.Fatal(err)
	}

	mock := &mockSandboxCreator{}
	h := newActionTestHandler(nil, nil, mock, profilesDir)

	body := bytes.NewBufferString(`{"profile":"base.yaml"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sandboxes/create", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleQuickCreate(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d; body: %s", w.Code, w.Body.String())
	}
	if mock.calledProfile == "" {
		t.Error("expected Create to be called")
	}
}

// TestHandleQuickCreate_ProfileNotFound: POST with a profile file that doesn't exist returns 400.
func TestHandleQuickCreate_ProfileNotFound(t *testing.T) {
	profilesDir := t.TempDir()
	// No files — missing.yaml doesn't exist

	mock := &mockSandboxCreator{}
	h := newActionTestHandler(nil, nil, mock, profilesDir)

	body := bytes.NewBufferString(`{"profile":"missing.yaml"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sandboxes/create", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.handleQuickCreate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

// Ensure errors package import is used.
var _ = errors.New
