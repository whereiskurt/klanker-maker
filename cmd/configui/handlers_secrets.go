// Package main — secrets handler for ConfigUI.
// handlers_secrets.go provides SSM parameter CRUD via narrow SSMAPI interface.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/rs/zerolog/log"
)

// SSMAPI is the narrow AWS SSM interface used by secrets handlers.
// Only the four operations required for /km/ parameter CRUD are needed.
// The real *ssm.Client satisfies this interface directly.
type SSMAPI interface {
	GetParametersByPath(ctx context.Context, input *ssm.GetParametersByPathInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
	GetParameter(ctx context.Context, input *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
	PutParameter(ctx context.Context, input *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
	DeleteParameter(ctx context.Context, input *ssm.DeleteParameterInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error)
}

// secretRecord is the JSON representation of a single SSM parameter in list responses.
// Values are intentionally omitted — use the decrypt endpoint to fetch values.
type secretRecord struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	LastModified string `json:"lastModified"`
	Version      int64  `json:"version"`
}

// secretValueRecord is the JSON response for a decrypt (reveal) request.
type secretValueRecord struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

const kmPrefix = "/km/"

// validateKMPrefix returns an error if name does not start with /km/.
func validateKMPrefix(name string) error {
	if !strings.HasPrefix(name, kmPrefix) {
		return fmt.Errorf("parameter name %q must start with %q", name, kmPrefix)
	}
	return nil
}

// listKMSecrets fetches all SSM parameters under /km/ using pagination.
// WithDecryption is false — list returns metadata only for PII safety.
func (h *Handler) listKMSecrets(ctx context.Context) ([]secretRecord, error) {
	path := kmPrefix
	var records []secretRecord

	var nextToken *string
	for {
		input := &ssm.GetParametersByPathInput{
			Path:           aws.String(path),
			Recursive:      aws.Bool(true),
			WithDecryption: aws.Bool(false),
			NextToken:      nextToken,
		}
		out, err := h.ssmClient.GetParametersByPath(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("GetParametersByPath: %w", err)
		}
		for _, p := range out.Parameters {
			lastMod := ""
			if p.LastModifiedDate != nil {
				lastMod = p.LastModifiedDate.UTC().Format(time.RFC3339)
			}
			records = append(records, secretRecord{
				Name:         aws.ToString(p.Name),
				Type:         string(p.Type),
				LastModified: lastMod,
				Version:      p.Version,
			})
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	return records, nil
}

// handleSecretsPage renders the full secrets management page.
// It pre-fetches the parameter list to populate the initial table.
func (h *Handler) handleSecretsPage(w http.ResponseWriter, r *http.Request) {
	records, err := h.listKMSecrets(r.Context())
	if err != nil {
		log.Warn().Err(err).Msg("configui: list secrets failed for page render")
		records = []secretRecord{}
	}
	h.render(w, "secrets.html", records)
}

// handleSecretsList handles GET /api/secrets.
// Returns JSON array of parameters (no values) or HTMX partial rows.
func (h *Handler) handleSecretsList(w http.ResponseWriter, r *http.Request) {
	records, err := h.listKMSecrets(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("configui: list secrets failed")
		http.Error(w, "failed to list secrets", http.StatusInternalServerError)
		return
	}

	if isHTMXRequest(r) {
		h.render(w, "secret_rows", records)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(records); err != nil {
		log.Error().Err(err).Msg("configui: encode secrets failed")
	}
}

// handleSecretDecrypt handles GET /api/secrets/{name...}.
// Validates /km/ prefix, fetches the decrypted value, returns JSON.
func (h *Handler) handleSecretDecrypt(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		// Fall back: extract from URL path after /api/secrets
		name = strings.TrimPrefix(r.URL.Path, "/api/secrets")
	}
	// Ensure leading slash
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}

	if err := validateKMPrefix(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	out, err := h.ssmClient.GetParameter(r.Context(), &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		log.Error().Err(err).Str("name", name).Msg("configui: decrypt secret failed")
		http.Error(w, "failed to decrypt secret", http.StatusInternalServerError)
		return
	}

	value := aws.ToString(out.Parameter.Value)

	// HTMX requests from the "Reveal" button get inline HTML with pii-blur toggle.
	if isHTMXRequest(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Escape value for safe HTML embedding
		safeVal := strings.ReplaceAll(value, "&", "&amp;")
		safeVal = strings.ReplaceAll(safeVal, "<", "&lt;")
		safeVal = strings.ReplaceAll(safeVal, ">", "&gt;")
		safeVal = strings.ReplaceAll(safeVal, `"`, "&#34;")
		_, _ = fmt.Fprintf(w,
			`<span class="pii-blur" onclick="this.classList.toggle('pii-blur')" title="Click to toggle blur">%s</span>`,
			safeVal,
		)
		return
	}

	rec := secretValueRecord{
		Name:  aws.ToString(out.Parameter.Name),
		Value: value,
		Type:  string(out.Parameter.Type),
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(rec); err != nil {
		log.Error().Err(err).Msg("configui: encode decrypt response failed")
	}
}

// handleSecretPut handles PUT /api/secrets/{name...}.
// Validates /km/ prefix, reads body as secret value, creates/updates SecureString.
func (h *Handler) handleSecretPut(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		name = strings.TrimPrefix(r.URL.Path, "/api/secrets")
	}
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}

	if err := validateKMPrefix(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	value := string(bodyBytes)

	input := &ssm.PutParameterInput{
		Name:      aws.String(name),
		Value:     aws.String(value),
		Type:      ssmtypes.ParameterTypeSecureString,
		Overwrite: aws.Bool(true),
	}

	if _, err := h.ssmClient.PutParameter(r.Context(), input); err != nil {
		log.Error().Err(err).Str("name", name).Msg("configui: put secret failed")
		http.Error(w, "failed to create/update secret", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// handleSecretDelete handles DELETE /api/secrets/{name...}.
// Validates /km/ prefix, deletes the parameter. ParameterNotFound is swallowed (idempotent).
func (h *Handler) handleSecretDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		name = strings.TrimPrefix(r.URL.Path, "/api/secrets")
	}
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}

	if err := validateKMPrefix(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_, err := h.ssmClient.DeleteParameter(r.Context(), &ssm.DeleteParameterInput{
		Name: aws.String(name),
	})
	if err != nil {
		var notFound *ssmtypes.ParameterNotFound
		if errors.As(err, &notFound) {
			// Idempotent — already gone is fine
			w.WriteHeader(http.StatusOK)
			return
		}
		log.Error().Err(err).Str("name", name).Msg("configui: delete secret failed")
		http.Error(w, "failed to delete secret", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
