// Package main is the KlankerMaker ConfigUI dashboard server.
// handlers.go defines all HTTP handler types and methods.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/rs/zerolog/log"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// ErrDestroyNotFound is returned by Destroyer.Destroy when the target sandbox does not exist.
// Handlers map this to 404.
var ErrDestroyNotFound = errors.New("sandbox not found for destroy")

// Destroyer is the narrow interface for sandbox destruction.
// In production, this shells out to `km destroy <id>` as a subprocess.
// In tests, use mockDestroyer.
type Destroyer interface {
	Destroy(ctx context.Context, sandboxID string) error
}

// TTLExtender is the narrow interface for extending a sandbox TTL.
// It deletes the existing EventBridge schedule and creates a new one.
type TTLExtender interface {
	ExtendTTL(ctx context.Context, sandboxID string, duration time.Duration) error
}

// SandboxCreator is the narrow interface for quick-creating a sandbox from a profile.
// In production, this shells out to `km create profiles/<profile>` as a subprocess.
type SandboxCreator interface {
	Create(ctx context.Context, profilePath string) error
}

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
	tmpl        *template.Template // default (dashboard) template set — used for partials
	editorTmpl  *template.Template // editor page template set
	secretsTmpl *template.Template // secrets page template set
	lister      SandboxLister
	finder      SandboxFinder
	cwClient    CWLogsFilterAPI
	ssmClient   SSMAPI // Added by Plan 03: secrets management
	destroyer   Destroyer
	ttlExtender TTLExtender
	creator     SandboxCreator
	profilesDir string
	bucket      string
	domain      string // base domain for branding (e.g. "klankermaker.ai"); from KM_DOMAIN env
}

// BasePage provides fields common to all full-page templates (nav active state).
type BasePage struct {
	ActivePage string
}

// DashboardData is the template data for the dashboard page.
type DashboardData struct {
	BasePage
	Sandboxes []kmaws.SandboxRecord
	Count     int
	Error     string
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
	var errMsg string
	records, err := h.lister.ListSandboxes(r.Context(), h.bucket)
	if err != nil {
		log.Error().Err(err).Msg("configui: list sandboxes failed")
		errMsg = "Unable to list sandboxes: " + err.Error()
		records = nil
	}

	data := DashboardData{
		BasePage:  BasePage{ActivePage: "dashboard"},
		Sandboxes: records,
		Count:     len(records),
		Error:     errMsg,
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

// handleDestroy handles POST /api/sandboxes/{id}/destroy.
// Calls Destroyer.Destroy with the sandbox ID from the path. Returns 200 on success
// with an HTMX HX-Trigger header to signal the dashboard to refresh. Returns 404 if the
// sandbox is not found (ErrDestroyNotFound), 500 on other errors.
func (h *Handler) handleDestroy(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")
	if sandboxID == "" {
		http.Error(w, "sandbox id required", http.StatusBadRequest)
		return
	}

	if err := h.destroyer.Destroy(r.Context(), sandboxID); err != nil {
		if errors.Is(err, ErrDestroyNotFound) {
			http.Error(w, "sandbox not found", http.StatusNotFound)
			return
		}
		log.Error().Err(err).Str("sandbox_id", sandboxID).Msg("configui: destroy failed")
		http.Error(w, "destroy failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "sandbox-destroyed")
	w.WriteHeader(http.StatusOK)
}

// ttlExtendRequest is the JSON body for PUT /api/sandboxes/{id}/ttl.
type ttlExtendRequest struct {
	Duration string `json:"duration"`
}

// handleExtendTTL handles PUT /api/sandboxes/{id}/ttl.
// Reads {"duration":"2h"} from the request body, parses the Go duration string,
// and calls TTLExtender.ExtendTTL. Returns 200 on success with the new TTL remaining,
// 400 for unparseable duration, 500 on other errors.
func (h *Handler) handleExtendTTL(w http.ResponseWriter, r *http.Request) {
	sandboxID := r.PathValue("id")
	if sandboxID == "" {
		http.Error(w, "sandbox id required", http.StatusBadRequest)
		return
	}

	var req ttlExtendRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	dur, err := time.ParseDuration(req.Duration)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid duration %q: %v", req.Duration, err), http.StatusBadRequest)
		return
	}

	if err := h.ttlExtender.ExtendTTL(r.Context(), sandboxID, dur); err != nil {
		log.Error().Err(err).Str("sandbox_id", sandboxID).Msg("configui: extend TTL failed")
		http.Error(w, "extend TTL failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"sandbox_id":    sandboxID,
		"ttl_remaining": dur.String(),
	})
}

// quickCreateRequest is the JSON body for POST /api/sandboxes/create.
type quickCreateRequest struct {
	Profile string `json:"profile"`
}

// handleQuickCreate handles POST /api/sandboxes/create.
// Reads {"profile":"filename.yaml"} from the body, validates the profile file exists
// in profilesDir, then calls SandboxCreator.Create. Returns 202 with a status message
// on success. Returns 400 if the profile file is not found.
func (h *Handler) handleQuickCreate(w http.ResponseWriter, r *http.Request) {
	var req quickCreateRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 4096))
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.Profile == "" {
		http.Error(w, "profile is required", http.StatusBadRequest)
		return
	}

	profilePath := filepath.Join(h.profilesDir, req.Profile)
	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		http.Error(w, fmt.Sprintf("profile %q not found", req.Profile), http.StatusBadRequest)
		return
	}

	if err := h.creator.Create(r.Context(), profilePath); err != nil {
		log.Error().Err(err).Str("profile", req.Profile).Msg("configui: quick-create failed")
		http.Error(w, "sandbox creation failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "Sandbox creation started",
		"profile": req.Profile,
	})
}

// isHTMXRequest reports whether the request was made by HTMX (has HX-Request header).
func isHTMXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") != ""
}

// render executes the named template from h.tmpl with the given data.
// On error it logs and writes a 500 response.
func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	h.renderWith(h.tmpl, w, name, data)
}

func (h *Handler) renderWith(t *template.Template, w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, name, data); err != nil {
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

{{define "secrets.html"}}<!DOCTYPE html><html><body>
<h1>Secrets Management</h1>
<table><tbody id="secrets-table-body">
{{range .}}<tr><td class="secret-name">{{.Name}}</td><td>{{.Type}}</td><td>{{.LastModified}}</td><td>{{.Version}}</td></tr>
{{end}}
</tbody></table>
</body></html>{{end}}

{{define "secret_rows"}}
{{range .}}<tr><td class="secret-name">{{.Name}}</td><td>{{.Type}}</td><td>{{.LastModified}}</td><td>{{.Version}}</td></tr>
{{end}}
{{end}}

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
