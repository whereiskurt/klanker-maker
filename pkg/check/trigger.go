package check

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// TriggerJSON is the canonical KM_CHECK_TRIGGER JSON schema (Phase 116 Plan 04).
// All fields are snake_case to match the viper/operator config convention and the
// bootstrap's json.loads() expectations at runtime.
type TriggerJSON struct {
	// Check is the check name; redundant with KM_CHECK_NAME but self-contained.
	Check string `json:"check,omitempty"`
	// WhenPy is the resolved Python predicate body (inline, never an @ref).
	WhenPy string `json:"when_py,omitempty"`
	// Alias is the target sandbox alias for resume-or-cold-create dispatch.
	Alias string `json:"alias,omitempty"`
	// Prompt is the resolved prompt template (inline, never an @ref).
	Prompt string `json:"prompt,omitempty"`
	// Profile is the SandboxProfile slug used for cold-create when on_absent=cold-create.
	Profile string `json:"profile,omitempty"`
	// OnAbsent controls behavior when the alias resolves to no sandbox.
	// Values: "cold-create" (default) or "skip".
	OnAbsent string `json:"on_absent,omitempty"`
	// CooldownSeconds suppresses repeated dispatch within a window. 0 = no cooldown.
	CooldownSeconds int `json:"cooldown_seconds,omitempty"`
}

// resolveAtFile resolves an @file reference: if val starts with "@", the
// remainder is treated as a filesystem path and the file contents are returned.
// Otherwise, the value is returned unchanged (inline text).
func resolveAtFile(val string) (string, error) {
	if !strings.HasPrefix(val, "@") {
		return val, nil
	}
	path := val[1:]
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("resolving @file %q: %w", path, err)
	}
	return string(data), nil
}

// BakeTrigger resolves @file references in WhenPy and Prompt, marshals the
// trigger to the canonical KM_CHECK_TRIGGER JSON schema (116-04), and computes
// a sourceHash = SHA-256 hex of the resolved JSON. The resolved JSON and hash
// are stable: identical input → identical output (map fields ordered by JSON
// marshaler's struct field order).
//
// on_absent defaults to "cold-create" when empty.
//
// Returns (jsonBytes, sourceHash, err).
func BakeTrigger(t config.CheckTrigger) ([]byte, string, error) {
	whenPy, err := resolveAtFile(t.WhenPy)
	if err != nil {
		return nil, "", fmt.Errorf("BakeTrigger: when_py: %w", err)
	}
	prompt, err := resolveAtFile(t.Prompt)
	if err != nil {
		return nil, "", fmt.Errorf("BakeTrigger: prompt: %w", err)
	}

	onAbsent := t.OnAbsent
	if onAbsent == "" {
		onAbsent = "cold-create"
	}

	tj := TriggerJSON{
		Check:           t.Check,
		WhenPy:          whenPy,
		Alias:           t.Alias,
		Prompt:          prompt,
		OnAbsent:        onAbsent,
		CooldownSeconds: t.CooldownSeconds,
	}

	jsonBytes, err := json.Marshal(tj)
	if err != nil {
		return nil, "", fmt.Errorf("BakeTrigger: marshal: %w", err)
	}

	h := sha256.Sum256(jsonBytes)
	sourceHash := fmt.Sprintf("%x", h)

	return jsonBytes, sourceHash, nil
}

// TriggerSummary returns a short human-readable string describing a trigger.
// Used for display in km check ls / km check get.
func TriggerSummary(t config.CheckTrigger) string {
	parts := []string{fmt.Sprintf("alias=%s", t.Alias)}
	if t.CooldownSeconds > 0 {
		parts = append(parts, fmt.Sprintf("cooldown=%ds", t.CooldownSeconds))
	}
	if t.OnAbsent != "" && t.OnAbsent != "cold-create" {
		parts = append(parts, fmt.Sprintf("on_absent=%s", t.OnAbsent))
	}
	return strings.Join(parts, " ")
}
