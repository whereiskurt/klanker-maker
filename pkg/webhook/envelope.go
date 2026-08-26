// Package webhook implements the generic webhook ingress bridge: envelope
// parsing, authentication, rule matching, and storm control. The AWS wiring
// lives in pkg/webhook/bridge; this package is pure and dependency-light so it
// is fully unit-testable without a network.
package webhook

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ErrUnparseable is returned when a body is not valid JSON. Callers log it and
// return 200 without dispatching — never a 4xx, which would make the sender
// redeliver the same broken body forever.
var ErrUnparseable = errors.New("webhook: unparseable payload")

// Envelope is the normalized view of an inbound payload. Typed fields are
// populated from the canonical km_schema v1 shape when present, otherwise from
// the source's declared field_paths. Raw is ALWAYS populated — the agent gets
// the full payload regardless of how much of it we understood.
type Envelope struct {
	Schema      string
	Source      string
	DeliveryKey string
	Type        string
	ID          string
	Severity    string
	Status      string
	Title       string
	Entity      map[string]string
	URL         string
	Raw         string

	// extra holds field_paths-derived values that have no typed home, notably
	// "group" for the fallback path.
	extra map[string]string
}

// stringifyScalar converts a JSON scalar to its string form, returning ok=false for
// non-scalars (objects, arrays, null).
func stringifyScalar(v any) (string, bool) {
	switch val := v.(type) {
	case string:
		return val, true
	case float64:
		return fmt.Sprintf("%v", val), true
	case bool:
		return fmt.Sprintf("%v", val), true
	default:
		return "", false
	}
}

// Decompress transparently gunzips a body when contentEncoding is "gzip".
// Any other encoding (including empty) is a passthrough. One source reports Wiz
// sending gzip-compressed bodies; this is cheap insurance and harmless if never
// triggered.
func Decompress(body []byte, contentEncoding string) ([]byte, error) {
	if !strings.EqualFold(strings.TrimSpace(contentEncoding), "gzip") {
		return body, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("webhook: gzip reader: %w", err)
	}
	defer zr.Close() //nolint:errcheck
	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("webhook: gzip read: %w", err)
	}
	return out, nil
}

// ParseEnvelope normalizes body according to the precedence:
//
//	km_schema == "v1"  -> typed fields
//	source.FieldPaths  -> declared JSON paths; FieldPaths["id"] doubles as the
//	                      replay key
//	otherwise          -> no routing fields; only an empty-match rule can fire
//
// Replay key precedence: delivery_key -> field_paths.id -> sha256(raw body).
func ParseEnvelope(body []byte, src config.WebhookSource) (*Envelope, error) {
	var generic map[string]any
	if err := json.Unmarshal(body, &generic); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnparseable, err)
	}

	env := &Envelope{
		Raw:    string(body),
		Source: src.Name,
		Entity: map[string]string{},
		extra:  map[string]string{},
	}

	// Check for canonical km_schema v1 directly from generic map.
	// This allows field-by-field degradation: numeric values stringify instead
	// of failing the entire parse. Reuse stringifyScalar for consistency.
	if schema, ok := generic["km_schema"]; ok {
		if schemaStr, ok := stringifyScalar(schema); ok && schemaStr == "v1" {
			env.Schema = "v1"
			if dk, ok := stringifyScalar(generic["delivery_key"]); ok {
				env.DeliveryKey = dk
			}
			if typ, ok := stringifyScalar(generic["type"]); ok {
				env.Type = typ
			}
			if id, ok := stringifyScalar(generic["id"]); ok {
				env.ID = id
			}
			if sev, ok := stringifyScalar(generic["severity"]); ok {
				env.Severity = sev
			}
			if status, ok := stringifyScalar(generic["status"]); ok {
				env.Status = status
			}
			if title, ok := stringifyScalar(generic["title"]); ok {
				env.Title = title
			}
			if url, ok := stringifyScalar(generic["url"]); ok {
				env.URL = url
			}
			if src, ok := stringifyScalar(generic["source"]); ok {
				env.Source = src
			}
			// Entity: extract map and stringify scalar values, skip non-scalars.
			if entityVal, ok := generic["entity"]; ok {
				if entityMap, ok := entityVal.(map[string]any); ok {
					for k, v := range entityMap {
						if s, ok := stringifyScalar(v); ok {
							env.Entity[k] = s
						}
					}
				}
			}
		}
	}

	if env.Schema == "" && len(src.FieldPaths) > 0 {
		for key, path := range src.FieldPaths {
			val, ok := lookupPath(generic, path)
			if !ok {
				continue
			}
			switch key {
			case "id":
				env.ID = val
			case "type":
				env.Type = val
			case "severity":
				env.Severity = val
			case "status":
				env.Status = val
			case "title":
				env.Title = val
			default:
				env.extra[key] = val
			}
		}
		env.DeliveryKey = env.ID
	}

	if env.DeliveryKey == "" {
		sum := sha256.Sum256(body)
		env.DeliveryKey = "sha256:" + hex.EncodeToString(sum[:])
	}
	return env, nil
}

// Field resolves a match/group_by field name against the envelope. Dotted names
// address the entity map ("entity.cloud_id"). The bool reports whether the field
// exists at all: a missing field is a NON-match, never a wildcard.
func (e *Envelope) Field(name string) (string, bool) {
	if rest, ok := strings.CutPrefix(name, "entity."); ok {
		v, found := e.Entity[rest]
		return v, found
	}
	switch name {
	case "type":
		return e.Type, e.Type != ""
	case "id":
		return e.ID, e.ID != ""
	case "severity":
		return e.Severity, e.Severity != ""
	case "status":
		return e.Status, e.Status != ""
	case "title":
		return e.Title, e.Title != ""
	case "url":
		return e.URL, e.URL != ""
	case "source":
		return e.Source, e.Source != ""
	case "raw":
		return e.Raw, true
	}
	v, ok := e.extra[name]
	return v, ok
}

// lookupPath resolves a dotted JSON path (optional leading "$.") against a
// decoded object, stringifying scalars. Returns ok=false for any missing or
// non-scalar leaf.
func lookupPath(obj map[string]any, path string) (string, bool) {
	path = strings.TrimPrefix(path, "$.")
	if path == "" {
		return "", false
	}
	var cur any = obj
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = m[seg]
		if !ok {
			return "", false
		}
	}
	switch v := cur.(type) {
	case string:
		return v, true
	case float64:
		return fmt.Sprintf("%v", v), true
	case bool:
		return fmt.Sprintf("%v", v), true
	default:
		return "", false
	}
}
