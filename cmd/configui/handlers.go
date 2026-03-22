// Package main is the KlankerMaker ConfigUI dashboard server.
// handlers.go defines all HTTP handler types and methods.
package main

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/rs/zerolog/log"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// SandboxLister is the narrow interface for listing sandboxes via S3.
// Wraps kmaws.ListAllSandboxesByS3 for dependency injection in tests.
type SandboxLister interface {
	ListSandboxes(ctx context.Context, bucket string) ([]kmaws.SandboxRecord, error)
}

// SandboxFinder is the narrow interface for finding a sandbox by ID via the tagging API.
// Wraps kmaws.FindSandboxByID for dependency injection in tests.
type SandboxFinder interface {
	FindSandbox(ctx context.Context, sandboxID string) (*kmaws.SandboxLocation, error)
}

// CWLogsFilterAPI is the narrow CloudWatch Logs interface needed by the audit log handler.
// Only FilterLogEvents is required; the real *cloudwatchlogs.Client satisfies this.
type CWLogsFilterAPI interface {
	FilterLogEvents(ctx context.Context, input *cloudwatchlogs.FilterLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error)
}

// Handler holds all dependencies for the ConfigUI HTTP handlers.
type Handler struct {
	tmpl        *template.Template
	lister      SandboxLister
	finder      SandboxFinder
	cwClient    CWLogsFilterAPI
	profilesDir string
	bucket      string
}

// DashboardData is the template data for the dashboard page.
type DashboardData struct {
	Sandboxes []kmaws.SandboxRecord
	Count     int
}

// DetailData is the template data for the sandbox detail partial.
type DetailData struct {
	Record      kmaws.SandboxRecord
	Location    *kmaws.SandboxLocation
	ProfileYAML string
	ProfileNote string
}

// LogEntry is a single audit log entry for display.
type LogEntry struct {
	Timestamp string
	Message   string
}

// handleDashboard serves the full dashboard page or an HTMX partial depending on the
// HX-Request header. Full-page requests get base.html + dashboard content; HTMX requests
// get only the sandbox rows partial for tbody innerHTML swapping.
func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	records, err := h.lister.ListSandboxes(r.Context(), h.bucket)
	if err != nil {
		log.Error().Err(err).Msg("configui: list sandboxes failed")
		http.Error(w, "failed to list sandboxes", http.StatusInternalServerError)
		return
	}

	data := DashboardData{
		Sandboxes: records,
		Count:     len(records),
	}

	if isHTMXRequest(r) {
		h.render(w, "sandbox_rows", data)
		return
	}
	h.render(w, "dashboard.html", data)
}

// handleSandboxesPartial always serves the sandbox rows partial (used by HTMX polling endpoint
// GET /api/sandboxes). Delegates to handleDashboard which checks HX-Request header.
func (h *Handler) handleSandboxesPartial(w http.ResponseWriter, r *http.Request) {
	h.handleDashboard(w, r)
}

// handleSandboxDetail serves the sandbox detail partial for GET /api/sandboxes/{id}.
// It calls the finder to get AWS resource ARNs, reads the profile YAML from profilesDir,
// and renders the sandbox_detail partial. Returns 404 if the sandbox is not found.
func (h *Handler) handleSandboxDetail(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")
	if sandboxID == "" {
		http.Error(w, "sandbox id required", http.StatusBadRequest)
		return
	}

	location, err := h.finder.FindSandbox(r.Context(), sandboxID)
	if err != nil {
		if errors.Is(err, kmaws.ErrSandboxNotFound) {
			http.Error(w, "sandbox not found", http.StatusNotFound)
			return
		}
		log.Error().Err(err).Str("sandbox_id", sandboxID).Msg("configui: find sandbox failed")
		http.Error(w, "failed to find sandbox", http.StatusInternalServerError)
		return
	}

	// Look up the SandboxRecord for metadata (profile name, substrate, etc.)
	var record kmaws.SandboxRecord
	if h.lister != nil {
		records, listErr := h.lister.ListSandboxes(r.Context(), h.bucket)
		if listErr == nil {
			for _, rec := range records {
				if rec.SandboxID == sandboxID {
					record = rec
					break
				}
			}
		}
	}
	if record.SandboxID == "" {
		record = kmaws.SandboxRecord{
			SandboxID: sandboxID,
			Status:    "unknown",
		}
	}

	// Read profile YAML from profilesDir if available
	profileYAML := ""
	profileNote := ""
	if record.Profile != "" && h.profilesDir != "" {
		profilePath := filepath.Join(h.profilesDir, record.Profile)
		data, readErr := os.ReadFile(profilePath)
		if readErr == nil {
			profileYAML = string(data)
		} else {
			profileNote = "Profile file not found on disk"
		}
	}

	detail := DetailData{
		Record:      record,
		Location:    location,
		ProfileYAML: profileYAML,
		ProfileNote: profileNote,
	}

	h.render(w, "sandbox_detail", detail)
}

// handleSandboxLogs serves the audit log entries partial for GET /api/sandboxes/{id}/logs.
// Fetches recent CloudWatch log events filtered by sandbox ID. If cwClient is nil or the
// call fails, returns a graceful placeholder — logs are informational, not critical.
func (h *Handler) handleSandboxLogs(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")

	if h.cwClient == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<p>No audit logs available</p>`)
		return
	}

	output, err := h.cwClient.FilterLogEvents(r.Context(), &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName:  ptrStr("/km/sandboxes"),
		FilterPattern: ptrStr(sandboxID),
		Limit:         ptrInt32(20),
	})
	if err != nil {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).Msg("configui: fetch audit logs failed")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<p>No audit logs available</p>`)
		return
	}

	entries := make([]LogEntry, 0, len(output.Events))
	for _, ev := range output.Events {
		ts := ""
		if ev.Timestamp != nil {
			t := time.UnixMilli(*ev.Timestamp).UTC()
			ts = t.Format("2006-01-02 15:04:05 UTC")
		}
		msg := ""
		if ev.Message != nil {
			msg = *ev.Message
		}
		entries = append(entries, LogEntry{Timestamp: ts, Message: msg})
	}

	h.render(w, "sandbox_logs", entries)
}

// handleSchema serves the embedded JSON schema bytes as application/json.
func (h *Handler) handleSchema(w http.ResponseWriter, _ *http.Request) {
	data := profile.SchemaJSON()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// isHTMXRequest reports whether the request was made by HTMX (has HX-Request header).
func isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") != ""
}

// render executes the named template from h.tmpl with the given data.
// On error it logs and writes a 500 response.
func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Error().Err(err).Str("template", name).Msg("configui: template render error")
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// ptrStr returns a pointer to s.
func ptrStr(s string) *string { return &s }

// ptrInt32 returns a pointer to i.
func ptrInt32(i int32) *int32 { return &i }

// buildTestTemplates creates minimal Go templates for unit testing without loading
// files from disk. All template names used by handlers must be present.
// It registers the same template functions as the production main.go.
func buildTestTemplates() *template.Template {
	const tmplStr = `
{{define "dashboard.html"}}<!DOCTYPE html><html><body>
<h1>Sandbox Dashboard</h1>
<p>Total sandboxes: {{.Count}}</p>
{{template "sandbox_rows" .}}
</body></html>{{end}}

{{define "sandbox_rows"}}
{{range .Sandboxes}}<tr><td>{{.SandboxID}}</td><td>{{.Profile}}</td><td>{{.Substrate}}</td><td>{{.Status}}</td><td>{{.TTLRemaining}}</td></tr>
{{end}}
{{end}}

{{define "sandbox_detail"}}
<div class="sandbox-detail">
  <h2>Sandbox: {{.Record.SandboxID}}</h2>
  <p>Profile: {{.Record.Profile}}</p>
  <p>Substrate: {{.Record.Substrate}}</p>
  <p>Status: {{.Record.Status}}</p>
  <h3>AWS Resources</h3>
  <ul>
  {{range .Location.ResourceARNs}}<li>{{.}}</li>{{end}}
  </ul>
  {{if .ProfileYAML}}
  <pre><code>{{.ProfileYAML}}</code></pre>
  {{else}}
  <p>{{.ProfileNote}}</p>
  {{end}}
</div>
{{end}}

{{define "sandbox_logs"}}
<ul>
{{range .}}<li><span>{{.Timestamp}}</span> {{.Message}}</li>
{{end}}
</ul>
{{end}}
`
	funcs := template.FuncMap{
		"truncateID": func(id string) string { return id },
	}
	return template.Must(template.New("").Funcs(funcs).Parse(tmplStr))
}
