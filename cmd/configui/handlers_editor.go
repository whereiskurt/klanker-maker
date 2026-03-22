// handlers_editor.go adds profile editor HTTP handlers to the Handler struct.
// These methods provide CRUD operations on YAML profiles stored in h.profilesDir,
// plus a live validation endpoint that calls profile.Validate().
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// maxProfileBodyBytes is the maximum size of a profile YAML body accepted from the client.
const maxProfileBodyBytes = 64 * 1024 // 64 KB

// ProfileEntry is a single entry in the /api/profiles list response.
type ProfileEntry struct {
	Name       string `json:"name"`
	HasExtends bool   `json:"hasExtends"`
}

// validateResponse is a single entry in the /api/validate response array.
// It mirrors profile.ValidationError for JSON serialization.
type validateResponse struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// EditorData is the template data for the editor page.
type EditorData struct {
	ActivePage     string
	ProfileContent string
	ProfileName    string
}

// handleEditorPage serves GET /editor — the Monaco YAML profile editor page.
// If ?profile=name is provided it pre-loads that profile's content for editing.
func (h *Handler) handleEditorPage(w http.ResponseWriter, r *http.Request) {
	data := EditorData{
		ActivePage: "editor",
	}

	// Pre-load a profile if ?profile= query param is present.
	if name := r.URL.Query().Get("profile"); name != "" {
		if !isSafeName(name) {
			http.Error(w, "invalid profile name", http.StatusBadRequest)
			return
		}
		content, err := os.ReadFile(filepath.Join(h.profilesDir, name))
		if err == nil {
			data.ProfileContent = string(content)
			data.ProfileName = name
		}
	}

	h.render(w, "editor.html", data)
}

// handleValidate serves POST /api/validate.
// Reads the request body (max 64 KB), runs profile.Validate(), and returns a JSON
// array of ValidationError objects. Returns an empty array when the profile is valid.
// Returns 400 if the body is empty.
func (h *Handler) handleValidate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxProfileBodyBytes))
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusInternalServerError)
		return
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		http.Error(w, "request body is empty", http.StatusBadRequest)
		return
	}

	validationErrs := profile.Validate(body)

	// Convert to JSON-serializable slice. Always return a JSON array (never null).
	resp := make([]validateResponse, 0, len(validationErrs))
	for _, ve := range validationErrs {
		resp = append(resp, validateResponse{
			Path:    ve.Path,
			Message: ve.Message,
		})
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Error().Err(err).Msg("configui: failed to encode validate response")
	}
}

// handleProfileList serves GET /api/profiles.
// If called by HTMX (HX-Request header present), returns the profile_list HTML partial.
// Otherwise returns a JSON array of ProfileEntry objects for each .yaml file in h.profilesDir.
// Checks each file for an "extends:" line to populate HasExtends.
func (h *Handler) handleProfileList(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(h.profilesDir)
	if err != nil {
		log.Error().Err(err).Str("dir", h.profilesDir).Msg("configui: read profiles dir")
		http.Error(w, "failed to read profiles directory", http.StatusInternalServerError)
		return
	}

	profiles := make([]ProfileEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		hasExtends := false
		if data, readErr := os.ReadFile(filepath.Join(h.profilesDir, name)); readErr == nil {
			hasExtends = strings.Contains(string(data), "extends:")
		}
		profiles = append(profiles, ProfileEntry{
			Name:       name,
			HasExtends: hasExtends,
		})
	}

	// HTMX requests get the HTML partial; plain API requests get JSON.
	if isHTMXRequest(r) {
		h.render(w, "profile_list", profiles)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(profiles); err != nil {
		log.Error().Err(err).Msg("configui: failed to encode profiles list")
	}
}

// handleProfileGet serves GET /api/profiles/{name}.
// Sanitizes the name (rejects path traversal), reads the file from h.profilesDir,
// and returns the raw YAML bytes with Content-Type: text/yaml.
// Returns 400 if the name is invalid, 404 if the file does not exist.
func (h *Handler) handleProfileGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !isSafeName(name) {
		http.Error(w, "invalid profile name", http.StatusBadRequest)
		return
	}

	data, err := os.ReadFile(filepath.Join(h.profilesDir, name))
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "profile not found", http.StatusNotFound)
			return
		}
		log.Error().Err(err).Str("name", name).Msg("configui: read profile")
		http.Error(w, "failed to read profile", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleProfileSave serves PUT /api/profiles/{name}.
// Sanitizes the name, reads the body (max 64 KB), runs profile.Validate() to warn
// on validation errors (save proceeds regardless), then writes the file with 0644 perms.
// Returns 400 if name is invalid, 200 on success (with optional warnings in response).
func (h *Handler) handleProfileSave(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !isSafeName(name) {
		http.Error(w, "invalid profile name", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxProfileBodyBytes))
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusInternalServerError)
		return
	}

	// Run validation — errors are warnings; save still proceeds.
	validationErrs := profile.Validate(body)
	warnings := make([]validateResponse, 0, len(validationErrs))
	for _, ve := range validationErrs {
		warnings = append(warnings, validateResponse{
			Path:    ve.Path,
			Message: ve.Message,
		})
	}

	destPath := filepath.Join(h.profilesDir, name)
	if err := os.WriteFile(destPath, body, 0644); err != nil { //nolint:gosec
		log.Error().Err(err).Str("name", name).Msg("configui: save profile")
		http.Error(w, "failed to save profile", http.StatusInternalServerError)
		return
	}

	type saveResponse struct {
		Saved    string             `json:"saved"`
		Warnings []validateResponse `json:"warnings"`
	}
	resp := saveResponse{
		Saved:    name,
		Warnings: warnings,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Error().Err(err).Msg("configui: failed to encode save response")
	}
}

// isSafeName returns true if name is safe for use as a profile filename.
// A safe name has no path separators or ".." components.
func isSafeName(name string) bool {
	if name == "" {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	if strings.ContainsAny(name, "/\\") {
		return false
	}
	return true
}
