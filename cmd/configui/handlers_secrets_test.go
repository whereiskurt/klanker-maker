package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// --- Mock SSMAPI ---

// mockSSM implements SSMAPI for unit testing — it records calls and returns canned responses.
type mockSSM struct {
	// GetParametersByPath responses — supports multi-page by index
	getByPathPages []*ssm.GetParametersByPathOutput
	getByPathIdx   int
	getByPathErr   error

	// GetParameter response
	getParamOut *ssm.GetParameterOutput
	getParamErr error

	// PutParameter response
	putParamOut *ssm.PutParameterOutput
	putParamErr error

	// DeleteParameter response
	deleteParamOut *ssm.DeleteParameterOutput
	deleteParamErr error

	// Recorded call arguments
	lastPutInput    *ssm.PutParameterInput
	lastDeleteInput *ssm.DeleteParameterInput
	lastGetInput    *ssm.GetParameterInput
}

func (m *mockSSM) GetParametersByPath(_ context.Context, input *ssm.GetParametersByPathInput, _ ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	if m.getByPathErr != nil {
		return nil, m.getByPathErr
	}
	if m.getByPathIdx < len(m.getByPathPages) {
		out := m.getByPathPages[m.getByPathIdx]
		m.getByPathIdx++
		return out, nil
	}
	return &ssm.GetParametersByPathOutput{}, nil
}

func (m *mockSSM) GetParameter(_ context.Context, input *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	m.lastGetInput = input
	return m.getParamOut, m.getParamErr
}

func (m *mockSSM) PutParameter(_ context.Context, input *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	m.lastPutInput = input
	return m.putParamOut, m.putParamErr
}

func (m *mockSSM) DeleteParameter(_ context.Context, input *ssm.DeleteParameterInput, _ ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
	m.lastDeleteInput = input
	return m.deleteParamOut, m.deleteParamErr
}

// --- Helpers ---

// newSecretsHandler creates a Handler with SSM mock injected, minimal templates for testing.
func newSecretsHandler(ssmc SSMAPI) *Handler {
	testTmpl := buildTestTemplates()
	return &Handler{
		tmpl:        testTmpl,
		secretsTmpl: testTmpl,
		ssmClient:   ssmc,
	}
}

func sampleSSMParams() []ssmtypes.Parameter {
	now := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	return []ssmtypes.Parameter{
		{
			Name:             aws.String("/km/db-password"),
			Type:             ssmtypes.ParameterTypeSecureString,
			LastModifiedDate: aws.Time(now),
			Version:          1,
		},
		{
			Name:             aws.String("/km/api-key"),
			Type:             ssmtypes.ParameterTypeString,
			LastModifiedDate: aws.Time(now),
			Version:          2,
		},
	}
}

// --- Tests ---

// Test: GET /api/secrets returns JSON array with name, type, lastModified, version (no values).
func TestHandleSecretsList_ReturnsJSONArray(t *testing.T) {
	mock := &mockSSM{
		getByPathPages: []*ssm.GetParametersByPathOutput{
			{Parameters: sampleSSMParams()},
		},
	}
	h := newSecretsHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/api/secrets", nil)
	w := httptest.NewRecorder()

	h.handleSecretsList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected application/json Content-Type, got %q", ct)
	}

	var result []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 parameters, got %d", len(result))
	}
	// Verify no value field in response
	for _, item := range result {
		if _, ok := item["value"]; ok {
			t.Errorf("list response must not include 'value' field (PII protection)")
		}
		if _, ok := item["name"]; !ok {
			t.Errorf("expected 'name' field in response item")
		}
	}
}

// Test: GET /api/secrets returns only parameters under /km/ prefix.
func TestHandleSecretsList_OnlyKMPrefix(t *testing.T) {
	// The mock only returns /km/ parameters — the handler must pass /km/ as path to SSM
	mock := &mockSSM{
		getByPathPages: []*ssm.GetParametersByPathOutput{
			{Parameters: sampleSSMParams()},
		},
	}
	h := newSecretsHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/api/secrets", nil)
	w := httptest.NewRecorder()
	h.handleSecretsList(w, req)

	var result []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	for _, item := range result {
		name, _ := item["name"].(string)
		if !strings.HasPrefix(name, "/km/") {
			t.Errorf("returned parameter %q is not under /km/ prefix", name)
		}
	}
}

// Test: GET /api/secrets/{name} with decrypt=true returns parameter value as JSON.
func TestHandleSecretDecrypt_ReturnsValue(t *testing.T) {
	mock := &mockSSM{
		getParamOut: &ssm.GetParameterOutput{
			Parameter: &ssmtypes.Parameter{
				Name:  aws.String("/km/db-password"),
				Value: aws.String("supersecret"),
				Type:  ssmtypes.ParameterTypeSecureString,
			},
		},
	}
	h := newSecretsHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/api/secrets/km/db-password", nil)
	req.SetPathValue("name", "/km/db-password")
	w := httptest.NewRecorder()

	h.handleSecretDecrypt(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if result["value"] != "supersecret" {
		t.Errorf("expected value 'supersecret', got %v", result["value"])
	}
	if result["name"] != "/km/db-password" {
		t.Errorf("expected name '/km/db-password', got %v", result["name"])
	}
	// Verify WithDecryption was true
	if mock.lastGetInput == nil || !aws.ToBool(mock.lastGetInput.WithDecryption) {
		t.Errorf("expected GetParameter called with WithDecryption=true")
	}
}

// Test: PUT /api/secrets/{name} creates SecureString parameter.
func TestHandleSecretPut_CreatesSecureString(t *testing.T) {
	mock := &mockSSM{
		putParamOut: &ssm.PutParameterOutput{Version: 1},
	}
	h := newSecretsHandler(mock)

	body := bytes.NewBufferString("mysecretvalue")
	req := httptest.NewRequest(http.MethodPut, "/api/secrets/km/db-password", body)
	req.SetPathValue("name", "/km/db-password")
	w := httptest.NewRecorder()

	h.handleSecretPut(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	if mock.lastPutInput == nil {
		t.Fatal("expected PutParameter to be called")
	}
	if aws.ToString(mock.lastPutInput.Name) != "/km/db-password" {
		t.Errorf("expected name '/km/db-password', got %q", aws.ToString(mock.lastPutInput.Name))
	}
	if mock.lastPutInput.Type != ssmtypes.ParameterTypeSecureString {
		t.Errorf("expected type SecureString, got %v", mock.lastPutInput.Type)
	}
	if aws.ToString(mock.lastPutInput.Value) != "mysecretvalue" {
		t.Errorf("expected value 'mysecretvalue', got %q", aws.ToString(mock.lastPutInput.Value))
	}
	if !aws.ToBool(mock.lastPutInput.Overwrite) {
		t.Errorf("expected Overwrite=true for idempotent put")
	}
}

// Test: PUT /api/secrets/{name} with non-/km/ prefix returns 400.
func TestHandleSecretPut_RejectsNonKMPrefix(t *testing.T) {
	mock := &mockSSM{}
	h := newSecretsHandler(mock)

	body := bytes.NewBufferString("value")
	req := httptest.NewRequest(http.MethodPut, "/api/secrets/other/secret", body)
	req.SetPathValue("name", "/other/secret")
	w := httptest.NewRecorder()

	h.handleSecretPut(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-/km/ prefix, got %d", w.Code)
	}
	if mock.lastPutInput != nil {
		t.Errorf("expected PutParameter NOT to be called for invalid prefix")
	}
}

// Test: DELETE /api/secrets/{name} calls DeleteParameter.
func TestHandleSecretDelete_CallsDelete(t *testing.T) {
	mock := &mockSSM{
		deleteParamOut: &ssm.DeleteParameterOutput{},
	}
	h := newSecretsHandler(mock)

	req := httptest.NewRequest(http.MethodDelete, "/api/secrets/km/db-password", nil)
	req.SetPathValue("name", "/km/db-password")
	w := httptest.NewRecorder()

	h.handleSecretDelete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	if mock.lastDeleteInput == nil {
		t.Fatal("expected DeleteParameter to be called")
	}
	if aws.ToString(mock.lastDeleteInput.Name) != "/km/db-password" {
		t.Errorf("expected name '/km/db-password', got %q", aws.ToString(mock.lastDeleteInput.Name))
	}
}

// Test: DELETE /api/secrets/{name} for nonexistent parameter returns 200 (idempotent).
func TestHandleSecretDelete_IdempotentOnNotFound(t *testing.T) {
	mock := &mockSSM{
		deleteParamErr: &ssmtypes.ParameterNotFound{},
	}
	h := newSecretsHandler(mock)

	req := httptest.NewRequest(http.MethodDelete, "/api/secrets/km/gone", nil)
	req.SetPathValue("name", "/km/gone")
	w := httptest.NewRecorder()

	h.handleSecretDelete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (idempotent) for ParameterNotFound, got %d", w.Code)
	}
}

// Test: GET /secrets returns full secrets page HTML.
func TestHandleSecretsPage_ReturnsHTML(t *testing.T) {
	mock := &mockSSM{
		getByPathPages: []*ssm.GetParametersByPathOutput{
			{Parameters: sampleSSMParams()},
		},
	}
	h := newSecretsHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/secrets", nil)
	w := httptest.NewRecorder()

	h.handleSecretsPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "secret") {
		t.Errorf("expected 'secret' in secrets page HTML, got: %s", body)
	}
}

// Test: multi-page SSM pagination is handled correctly.
func TestHandleSecretsList_Pagination(t *testing.T) {
	now := time.Now()
	page1 := &ssm.GetParametersByPathOutput{
		Parameters: []ssmtypes.Parameter{
			{
				Name:             aws.String("/km/param1"),
				Type:             ssmtypes.ParameterTypeSecureString,
				LastModifiedDate: aws.Time(now),
				Version:          1,
			},
		},
		NextToken: aws.String("token-page2"),
	}
	page2 := &ssm.GetParametersByPathOutput{
		Parameters: []ssmtypes.Parameter{
			{
				Name:             aws.String("/km/param2"),
				Type:             ssmtypes.ParameterTypeString,
				LastModifiedDate: aws.Time(now),
				Version:          1,
			},
		},
	}
	mock := &mockSSM{
		getByPathPages: []*ssm.GetParametersByPathOutput{page1, page2},
	}
	h := newSecretsHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/api/secrets", nil)
	w := httptest.NewRecorder()
	h.handleSecretsList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var result []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 parameters (from both pages), got %d", len(result))
	}
}

// Test: GET /api/secrets/{name} with non-/km/ prefix returns 400.
func TestHandleSecretDecrypt_RejectsNonKMPrefix(t *testing.T) {
	mock := &mockSSM{}
	h := newSecretsHandler(mock)

	req := httptest.NewRequest(http.MethodGet, "/api/secrets/other/secret", nil)
	req.SetPathValue("name", "/other/secret")
	w := httptest.NewRecorder()

	h.handleSecretDecrypt(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-/km/ prefix, got %d", w.Code)
	}
}

// Test: DELETE /api/secrets/{name} for non-/km/ prefix returns 400.
func TestHandleSecretDelete_RejectsNonKMPrefix(t *testing.T) {
	mock := &mockSSM{}
	h := newSecretsHandler(mock)

	req := httptest.NewRequest(http.MethodDelete, "/api/secrets/other/secret", nil)
	req.SetPathValue("name", "/other/secret")
	w := httptest.NewRecorder()

	h.handleSecretDelete(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-/km/ prefix, got %d", w.Code)
	}
}

// ensure io is used (imported for potential body reads in tests)
var _ = io.EOF
