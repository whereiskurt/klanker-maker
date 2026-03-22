package main

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- helpers ---

// newEditorTestHandler creates a Handler backed by buildEditorTestTemplates() and a real temp profilesDir.
func newEditorTestHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	dir := t.TempDir()

	// Write sample profiles into temp dir
	validYAML := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: test-profile
spec:
  lifecycle:
    ttl: "24h"
    idleTimeout: "4h"
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    spot: false
    instanceType: t3.medium
    region: us-east-1
`
	profileWithExtends := `extends: open-dev.yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: child-profile
spec:
  runtime:
    instanceType: t3.large
`

	if err := os.WriteFile(filepath.Join(dir, "test-profile.yaml"), []byte(validYAML), 0644); err != nil {
		t.Fatalf("write test-profile.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child-profile.yaml"), []byte(profileWithExtends), 0644); err != nil {
		t.Fatalf("write child-profile.yaml: %v", err)
	}

	testTmpl := buildEditorTestTemplates()
	h := &Handler{
		tmpl:        testTmpl,
		editorTmpl:  testTmpl,
		lister:      &mockLister{},
		finder:      nil,
		cwClient:    nil,
		profilesDir: dir,
	}
	return h, dir
}

// --- POST /api/validate tests ---

// Test: POST /api/validate with valid YAML returns 200 + empty error array.
func TestHandleValidate_ValidYAML(t *testing.T) {
	h, _ := newEditorTestHandler(t)

	// Full valid profile with all required spec fields.
	validYAML := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: test
spec:
  lifecycle:
    ttl: "24h"
    idleTimeout: "4h"
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    spot: false
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
    github:
      allowedRepos:
        - "github.com/*"
      allowedRefs:
        - "main"
      permissions:
        - read
  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
      allowedHosts:
        - "api.github.com"
      allowedMethods:
        - GET
  identity:
    roleSessionDuration: "1h"
    allowedRegions:
      - us-east-1
    sessionPolicy: minimal
  sidecars:
    dnsProxy:
      enabled: true
      image: km-dns-proxy:latest
    httpProxy:
      enabled: true
      image: km-http-proxy:latest
    auditLog:
      enabled: true
      image: km-audit-log:latest
    tracing:
      enabled: true
      image: km-tracing:latest
  observability:
    commandLog:
      destination: cloudwatch
      logGroup: /km/sandboxes
    networkLog:
      destination: cloudwatch
      logGroup: /km/network
  policy:
    allowShellEscape: false
    allowedCommands:
      - bash
    filesystemPolicy:
      readOnlyPaths: []
      writablePaths:
        - /workspace
  agent:
    maxConcurrentTasks: 4
    taskTimeout: "30m"
    allowedTools:
      - bash
`
	req := httptest.NewRequest(http.MethodPost, "/api/validate", strings.NewReader(validYAML))
	w := httptest.NewRecorder()

	h.handleValidate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var errs []map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errs); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(errs) != 0 {
		t.Errorf("expected empty error array for valid YAML, got %d errors: %v", len(errs), errs)
	}
}

// Test: POST /api/validate with invalid YAML returns 200 + non-empty error array.
func TestHandleValidate_InvalidYAML(t *testing.T) {
	h, _ := newEditorTestHandler(t)

	invalidYAML := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: test
spec:
  runtime:
    substrate: notavalidsubstrate
`
	req := httptest.NewRequest(http.MethodPost, "/api/validate", strings.NewReader(invalidYAML))
	w := httptest.NewRecorder()

	h.handleValidate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var errs []map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errs); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(errs) == 0 {
		t.Error("expected non-empty error array for invalid YAML")
	}
	// Each error should have path and message fields
	for _, e := range errs {
		if _, ok := e["message"]; !ok {
			t.Errorf("error missing 'message' field: %v", e)
		}
	}
}

// Test: POST /api/validate with empty body returns 400.
func TestHandleValidate_EmptyBody(t *testing.T) {
	h, _ := newEditorTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/validate", bytes.NewReader([]byte{}))
	w := httptest.NewRecorder()

	h.handleValidate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

// --- GET /api/profiles tests ---

// Test: GET /api/profiles returns JSON array of profile filenames.
func TestHandleProfileList_ReturnsList(t *testing.T) {
	h, _ := newEditorTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/profiles", nil)
	w := httptest.NewRecorder()

	h.handleProfileList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected application/json, got %q", ct)
	}

	var profiles []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&profiles); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(profiles) != 2 {
		t.Errorf("expected 2 profiles, got %d", len(profiles))
	}

	// Verify that child-profile.yaml has hasExtends=true
	for _, p := range profiles {
		name, _ := p["name"].(string)
		if name == "child-profile.yaml" {
			he, _ := p["hasExtends"].(bool)
			if !he {
				t.Errorf("expected child-profile.yaml to have hasExtends=true")
			}
		}
		if name == "test-profile.yaml" {
			he, _ := p["hasExtends"].(bool)
			if he {
				t.Errorf("expected test-profile.yaml to have hasExtends=false, got true")
			}
		}
	}
}

// --- GET /api/profiles/{name} tests ---

// Test: GET /api/profiles/{name} returns raw YAML content.
func TestHandleProfileGet_Found(t *testing.T) {
	h, _ := newEditorTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/profiles/test-profile.yaml", nil)
	req.SetPathValue("name", "test-profile.yaml")
	w := httptest.NewRecorder()

	h.handleProfileGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "yaml") {
		t.Errorf("expected text/yaml content type, got %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "SandboxProfile") {
		t.Errorf("expected YAML content in body, got: %s", body)
	}
}

// Test: GET /api/profiles/{name} for nonexistent profile returns 404.
func TestHandleProfileGet_NotFound(t *testing.T) {
	h, _ := newEditorTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/profiles/nonexistent.yaml", nil)
	req.SetPathValue("name", "nonexistent.yaml")
	w := httptest.NewRecorder()

	h.handleProfileGet(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d; body: %s", w.Code, w.Body.String())
	}
}

// --- PUT /api/profiles/{name} tests ---

// Test: PUT /api/profiles/{name} writes YAML body to profiles/{name} file.
func TestHandleProfileSave_WritesFile(t *testing.T) {
	h, dir := newEditorTestHandler(t)

	newContent := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: new-profile
spec:
  lifecycle:
    ttl: "8h"
    idleTimeout: "2h"
    teardownPolicy: destroy
  runtime:
    substrate: ecs
    spot: true
    region: us-east-1
`
	req := httptest.NewRequest(http.MethodPut, "/api/profiles/new-profile.yaml", strings.NewReader(newContent))
	req.SetPathValue("name", "new-profile.yaml")
	w := httptest.NewRecorder()

	h.handleProfileSave(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	// Verify file was written
	written, err := os.ReadFile(filepath.Join(dir, "new-profile.yaml"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if !strings.Contains(string(written), "new-profile") {
		t.Errorf("written file does not contain expected content")
	}
}

// Test: PUT /api/profiles/{name} with path traversal (../) returns 400.
func TestHandleProfileSave_PathTraversal(t *testing.T) {
	h, _ := newEditorTestHandler(t)

	req := httptest.NewRequest(http.MethodPut, "/api/profiles/../../etc/passwd", strings.NewReader("bad content"))
	req.SetPathValue("name", "../../etc/passwd")
	w := httptest.NewRecorder()

	h.handleProfileSave(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path traversal, got %d", w.Code)
	}
}

// Test: GET /api/profiles/{name} with path traversal returns 400.
func TestHandleProfileGet_PathTraversal(t *testing.T) {
	h, _ := newEditorTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/profiles/../etc/passwd", nil)
	req.SetPathValue("name", "../etc/passwd")
	w := httptest.NewRecorder()

	h.handleProfileGet(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path traversal, got %d", w.Code)
	}
}

// --- GET /editor tests ---

// Test: GET /editor returns 200 with full editor page.
func TestHandleEditorPage(t *testing.T) {
	h, _ := newEditorTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/editor", nil)
	w := httptest.NewRecorder()

	h.handleEditorPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "editor") {
		t.Errorf("expected 'editor' in body, got: %s", body)
	}
}

// buildEditorTestTemplates creates minimal templates for editor handler tests.
func buildEditorTestTemplates() *template.Template {
	const tmplStr = `
{{define "dashboard.html"}}dashboard{{end}}
{{define "sandbox_rows"}}rows{{end}}
{{define "sandbox_detail"}}detail{{end}}
{{define "sandbox_logs"}}logs{{end}}
{{define "editor.html"}}<!DOCTYPE html><html><body>editor page {{if .ProfileContent}}{{.ProfileContent}}{{end}}</body></html>{{end}}
{{define "profile_list"}}{{range .}}<div class="profile-item" data-name="{{.Name}}">{{.Name}}</div>{{end}}{{end}}
`
	funcs := template.FuncMap{
		"truncateID": func(id string) string { return id },
	}
	return template.Must(template.New("").Funcs(funcs).Parse(tmplStr))
}
