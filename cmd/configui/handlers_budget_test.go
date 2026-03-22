package main

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// mockBudgetFetcher satisfies BudgetFetcher.
type mockBudgetFetcher struct {
	data *BudgetDisplayData
	err  error
}

func (m *mockBudgetFetcher) GetBudget(_ context.Context, _ string) (*BudgetDisplayData, error) {
	return m.data, m.err
}

// --- Unit tests for BuildBudgetDisplayData ---

// TestBuildBudgetDisplayData_WithBudget verifies formatted output when limits are set.
func TestBuildBudgetDisplayData_WithBudget(t *testing.T) {
	summary := &kmaws.BudgetSummary{
		ComputeSpent: 1.50,
		ComputeLimit: 5.00,
		AISpent:      0.25,
		AILimit:      10.00,
	}
	d := BuildBudgetDisplayData(summary)

	if !d.HasBudget {
		t.Fatal("expected HasBudget=true when limits are set")
	}
	if d.ComputeSpent != "$1.50" {
		t.Errorf("ComputeSpent: got %q, want %q", d.ComputeSpent, "$1.50")
	}
	if d.ComputeLimit != "$5.00" {
		t.Errorf("ComputeLimit: got %q, want %q", d.ComputeLimit, "$5.00")
	}
	if d.ComputePct != 30 {
		t.Errorf("ComputePct: got %d, want 30", d.ComputePct)
	}
	if d.AISpent != "$0.25" {
		t.Errorf("AISpent: got %q, want %q", d.AISpent, "$0.25")
	}
	if d.AILimit != "$10.00" {
		t.Errorf("AILimit: got %q, want %q", d.AILimit, "$10.00")
	}
	if d.AIPct != 2 {
		t.Errorf("AIPct: got %d, want 2", d.AIPct)
	}
	if d.CSSClass != "budget-ok" {
		t.Errorf("CSSClass: got %q, want %q", d.CSSClass, "budget-ok")
	}
}

// TestBuildBudgetDisplayData_NoBudget verifies HasBudget=false when no limits exist.
func TestBuildBudgetDisplayData_NoBudget(t *testing.T) {
	summary := &kmaws.BudgetSummary{
		ComputeSpent: 0,
		ComputeLimit: 0,
		AISpent:      0,
		AILimit:      0,
	}
	d := BuildBudgetDisplayData(summary)

	if d.HasBudget {
		t.Error("expected HasBudget=false when no limits are configured")
	}
}

// TestBuildBudgetDisplayData_NilSummary verifies graceful handling of nil input.
func TestBuildBudgetDisplayData_NilSummary(t *testing.T) {
	d := BuildBudgetDisplayData(nil)
	if d.HasBudget {
		t.Error("expected HasBudget=false for nil summary")
	}
}

// --- CSS class boundary tests ---

// TestBudgetCSSClass_Boundaries verifies CSS class assignment at all boundary values.
func TestBudgetCSSClass_Boundaries(t *testing.T) {
	tests := []struct {
		pct  int
		want string
	}{
		{0, "budget-ok"},
		{79, "budget-ok"},
		{80, "budget-warn"},
		{99, "budget-warn"},
		{100, "budget-exceeded"},
		{150, "budget-exceeded"},
	}
	for _, tc := range tests {
		got := budgetCSSClass(tc.pct)
		if got != tc.want {
			t.Errorf("budgetCSSClass(%d) = %q, want %q", tc.pct, got, tc.want)
		}
	}
}

// TestBuildBudgetDisplayData_CSSClass_Warn verifies warn class at 80% compute.
func TestBuildBudgetDisplayData_CSSClass_Warn(t *testing.T) {
	summary := &kmaws.BudgetSummary{
		ComputeSpent: 4.00,
		ComputeLimit: 5.00, // 80%
		AISpent:      0,
		AILimit:      10.00,
	}
	d := BuildBudgetDisplayData(summary)
	if d.CSSClass != "budget-warn" {
		t.Errorf("expected budget-warn at 80%%, got %q", d.CSSClass)
	}
	if d.ComputePct != 80 {
		t.Errorf("expected ComputePct=80, got %d", d.ComputePct)
	}
}

// TestBuildBudgetDisplayData_CSSClass_Exceeded verifies exceeded class at 100%+ spend.
func TestBuildBudgetDisplayData_CSSClass_Exceeded(t *testing.T) {
	summary := &kmaws.BudgetSummary{
		ComputeSpent: 5.00,
		ComputeLimit: 5.00, // 100%
		AISpent:      0,
		AILimit:      10.00,
	}
	d := BuildBudgetDisplayData(summary)
	if d.CSSClass != "budget-exceeded" {
		t.Errorf("expected budget-exceeded at 100%%, got %q", d.CSSClass)
	}
}

// TestBuildBudgetDisplayData_CSSClass_WorstWins verifies AI exceeded overrides compute-ok.
func TestBuildBudgetDisplayData_CSSClass_WorstWins(t *testing.T) {
	summary := &kmaws.BudgetSummary{
		ComputeSpent: 0.50,
		ComputeLimit: 5.00, // 10% — ok
		AISpent:      11.00,
		AILimit:      10.00, // 110% — exceeded
	}
	d := BuildBudgetDisplayData(summary)
	if d.CSSClass != "budget-exceeded" {
		t.Errorf("expected budget-exceeded when AI is exceeded, got %q", d.CSSClass)
	}
}

// --- Handler integration test: dashboard includes budget fields ---

// TestHandleDashboard_WithBudget verifies dashboard renders budget data when fetcher present.
func TestHandleDashboard_WithBudget(t *testing.T) {
	lister := &mockLister{records: sampleRecords()}
	budgetFetcher := &mockBudgetFetcher{
		data: &BudgetDisplayData{
			ComputeSpent: "$1.50",
			ComputeLimit: "$5.00",
			ComputePct:   30,
			AISpent:      "$0.25",
			AILimit:      "$10.00",
			AIPct:        2,
			CSSClass:     "budget-ok",
			HasBudget:    true,
		},
	}
	h := newTestHandlerWithBudget(lister, nil, nil, t.TempDir(), budgetFetcher)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.handleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "$1.50") {
		t.Errorf("expected compute spent '$1.50' in body, got: %s", body)
	}
}

// TestHandleDashboard_NoBudgetFetcher verifies dashboard renders without budget fetcher.
func TestHandleDashboard_NoBudgetFetcher(t *testing.T) {
	lister := &mockLister{records: sampleRecords()}
	h := newTestHandlerWithBudget(lister, nil, nil, t.TempDir(), nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.handleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 without budget fetcher, got %d", w.Code)
	}
}

// newTestHandlerWithBudget constructs a Handler with an optional BudgetFetcher.
func newTestHandlerWithBudget(lister SandboxLister, finder SandboxFinder, cw CWLogsFilterAPI, profilesDir string, bf BudgetFetcher) *Handler {
	return &Handler{
		tmpl:          buildTestTemplatesWithBudget(),
		lister:        lister,
		finder:        finder,
		cwClient:      cw,
		profilesDir:   profilesDir,
		budgetFetcher: bf,
	}
}

// buildTestTemplatesWithBudget creates inline templates that include budget fields.
func buildTestTemplatesWithBudget() *template.Template {
	const tmplStr = `
{{define "dashboard.html"}}<!DOCTYPE html><html><body>
<h1>Sandbox Dashboard</h1>
<p>Total sandboxes: {{.Count}}</p>
{{template "sandbox_rows" .}}
</body></html>{{end}}

{{define "secrets.html"}}<!DOCTYPE html><html><body>
<h1>Secrets Management</h1>
</body></html>{{end}}

{{define "secret_rows"}}
{{range .}}<tr><td class="secret-name">{{.Name}}</td></tr>{{end}}
{{end}}

{{define "sandbox_rows"}}
{{range .Sandboxes}}
<tr>
  <td>{{.SandboxID}}</td>
  <td>{{.Profile}}</td>
  <td>{{.Substrate}}</td>
  <td>{{.Status}}</td>
  <td>{{.TTLRemaining}}</td>
  {{if .Budget}}
    {{if .Budget.HasBudget}}
    <td class="{{.Budget.CSSClass}}">{{.Budget.ComputeSpent}} / {{.Budget.ComputeLimit}}</td>
    <td class="{{.Budget.CSSClass}}">{{.Budget.AISpent}} / {{.Budget.AILimit}}</td>
    {{else}}
    <td>&#8212;</td><td>&#8212;</td>
    {{end}}
  {{else}}
  <td>&#8212;</td><td>&#8212;</td>
  {{end}}
</tr>
{{end}}
{{end}}

{{define "sandbox_detail"}}
<div class="sandbox-detail">
  <h2>Sandbox: {{.Record.SandboxID}}</h2>
</div>
{{end}}

{{define "sandbox_logs"}}
<ul>{{range .}}<li>{{.Timestamp}} {{.Message}}</li>{{end}}</ul>
{{end}}
`
	funcs := template.FuncMap{
		"truncateID": func(id string) string { return id },
	}
	return template.Must(template.New("").Funcs(funcs).Parse(tmplStr))
}
