# Generic Webhook Ingress Bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A third-party SaaS POSTs a payload to a km-owned Lambda, which authenticates it, drops replays, matches operator-declared rules, and dispatches a prompt to a named sandbox alias — warm via a per-sandbox SQS FIFO, cold via EventBridge. First source: Wiz → `ir-bot` runs `/triage`.

**Architecture:** One `km-webhook-bridge` Lambda behind a Function URL, with the source resolved from the URL path (`POST /wiz`). A `webhooks:` block in `km-config.yaml` declares N sources over one Lambda, one queue, one poller, and one profile field. Dormant when the block is absent.

**Tech Stack:** Go 1.x (module `github.com/whereiskurt/klanker-maker`), AWS SDK v2 (Lambda Function URL, SQS FIFO, DynamoDB, SSM, EventBridge, EC2), Terraform/Terragrunt, standard-library `testing`.

**Spec:** `docs/superpowers/specs/2026-08-25-webhook-ingress-bridge-design.md`

## Global Constraints

- **Dormant by default.** Absent `webhooks:` ⇒ `KM_WEBHOOK_SOURCES` unset ⇒ the bridge drops everything, `km doctor` skips the group with zero AWS calls.
- **Always return HTTP 200**, including on internal errors. A non-200 makes the sender redeliver; for sources with a delivery id that redelivery walks past dedup. This is the H1 Pitfall-2 rule and is not optional.
- **Fail-open:** cooldown and rate ceiling (never strand a real alert on a transient DynamoDB error).
- **Fail-closed:** auth, replay nonce, unparseable payloads, and any `match` naming a field the envelope lacks.
- **No CLI verb.** Configuration is `km-config.yaml` only. Do not add a `km webhook` command.
- Config keys are **snake_case** (viper lowercases at load). Every config struct field needs a **`mapstructure` tag** or `UnmarshalKey` silently ignores it.
- The Terraform module declares **no `required_providers`** (they live in `root.hcl`).
- Module path for all imports: `github.com/whereiskurt/klanker-maker`.
- Run tests with an explicit exit-code check — `go test ./... | tail` returns tail's 0 and masks a FAIL.

---

### Task 1: `webhooks:` config block

**Files:**
- Modify: `internal/app/config/config.go` (add structs; merge-list entry at ~line 1067; `UnmarshalKey` near line 1249)
- Test: `internal/app/config/config_webhooks_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.WebhooksConfig{RateLimit *WebhookRateLimit, Sources []WebhookSource}`, `config.WebhookSource{Name, Auth WebhookAuth, ReplayTTLSeconds int, FieldPaths map[string]string, Rules []WebhookRule}`, `config.WebhookAuth{Type, Header, SecretPath string}`, `config.WebhookRule{Match map[string][]string, Alias, Profile, OnAbsent string, CooldownSeconds int, GroupBy, Prompt string}`, `config.WebhookRateLimit{MaxDispatches, WindowSeconds int}`, and field `Config.Webhooks WebhooksConfig`.

- [ ] **Step 1: Write the failing test**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The whole webhooks: block must survive Load(). This is the
// project_config_key_merge_list footgun: without a "webhooks" entry in the
// v2->v merge-list, the block is silently dropped and every field stays zero.
func TestLoad_WebhooksBlockSurvivesMergeList(t *testing.T) {
	dir := t.TempDir()
	yaml := `
domain: example.com
resource_prefix: km
webhooks:
  rate_limit:
    max_dispatches: 20
    window_seconds: 600
  sources:
    - name: wiz
      auth:
        type: bearer
        header: Authorization
        secret_path: /km/config/webhooks/wiz/token
      replay_ttl_seconds: 900
      field_paths:
        id: "$.id"
      rules:
        - match:
            type: [issue, threat]
            severity: [CRITICAL, HIGH]
          alias: ir-bot
          profile: ir-bot
          on_absent: cold-create
          cooldown_seconds: 900
          group_by: "{{entity.cloud_id}}"
          prompt: "triage this"
`
	path := filepath.Join(dir, "km-config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Chdir(dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := len(cfg.Webhooks.Sources); got != 1 {
		t.Fatalf("Sources: got %d, want 1 (merge-list entry missing?)", got)
	}
	src := cfg.Webhooks.Sources[0]
	if src.Name != "wiz" {
		t.Errorf("Name: got %q, want %q", src.Name, "wiz")
	}
	if src.Auth.Type != "bearer" {
		t.Errorf("Auth.Type: got %q, want %q", src.Auth.Type, "bearer")
	}
	if src.Auth.SecretPath != "/km/config/webhooks/wiz/token" {
		t.Errorf("Auth.SecretPath: got %q", src.Auth.SecretPath)
	}
	if src.ReplayTTLSeconds != 900 {
		t.Errorf("ReplayTTLSeconds: got %d, want 900", src.ReplayTTLSeconds)
	}
	if src.FieldPaths["id"] != "$.id" {
		t.Errorf("FieldPaths[id]: got %q", src.FieldPaths["id"])
	}
	if len(src.Rules) != 1 {
		t.Fatalf("Rules: got %d, want 1", len(src.Rules))
	}
	r := src.Rules[0]
	if r.Alias != "ir-bot" || r.Profile != "ir-bot" {
		t.Errorf("alias/profile: got %q/%q", r.Alias, r.Profile)
	}
	if r.OnAbsent != "cold-create" {
		t.Errorf("OnAbsent: got %q", r.OnAbsent)
	}
	if r.CooldownSeconds != 900 {
		t.Errorf("CooldownSeconds: got %d", r.CooldownSeconds)
	}
	if r.GroupBy != "{{entity.cloud_id}}" {
		t.Errorf("GroupBy: got %q", r.GroupBy)
	}
	if got := r.Match["severity"]; len(got) != 2 || got[0] != "CRITICAL" {
		t.Errorf("Match[severity]: got %v", got)
	}
	if cfg.Webhooks.RateLimit == nil || cfg.Webhooks.RateLimit.MaxDispatches != 20 {
		t.Errorf("RateLimit: got %+v", cfg.Webhooks.RateLimit)
	}
}

// Absent webhooks: block must yield the zero value with no error — dormancy.
func TestLoad_WebhooksAbsentIsDormant(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "km-config.yaml")
	if err := os.WriteFile(path, []byte("domain: example.com\nresource_prefix: km\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Chdir(dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Webhooks.Sources) != 0 {
		t.Errorf("Sources: got %d, want 0", len(cfg.Webhooks.Sources))
	}
	if cfg.Webhooks.RateLimit != nil {
		t.Errorf("RateLimit: got %+v, want nil", cfg.Webhooks.RateLimit)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/config/ -run TestLoad_Webhooks -v`
Expected: FAIL — `cfg.Webhooks` undefined.

- [ ] **Step 3: Add the config structs**

Add to `internal/app/config/config.go`, near the `ChecksConfig` block:

```go
// WebhookAuth declares how a webhook source authenticates to the bridge.
// All fields carry mapstructure tags — untagged fields are silently ignored by
// viper's UnmarshalKey (project_config_key_merge_list pitfall 1).
type WebhookAuth struct {
	// Type is "bearer" (constant-time compare of a shared token) or "hmac"
	// (HMAC-SHA256 over the raw body). Wiz offers no signature, so Wiz uses bearer.
	Type string `mapstructure:"type" yaml:"type" json:"type"`

	// Header is the HTTP header carrying the credential. Defaults to
	// "Authorization" when empty.
	Header string `mapstructure:"header" yaml:"header,omitempty" json:"header,omitempty"`

	// SecretPath is the SSM SecureString parameter holding the token or HMAC key.
	// Required — a source with an empty SecretPath fails closed on every request.
	SecretPath string `mapstructure:"secret_path" yaml:"secret_path" json:"secret_path"`
}

// WebhookRule maps a matched payload to a sandbox dispatch. The key names are
// lifted verbatim from checks.triggers (Phase 116) so operators learn one
// vocabulary for the pull ingress and the push ingress.
type WebhookRule struct {
	// Match is a field -> allowed-values map evaluated against the parsed
	// envelope. All named fields must match (AND); within a field any value
	// matches (OR). A field the envelope does not carry is a NON-match, never a
	// wildcard — a typo'd path must dispatch nothing, not everything.
	// An empty/absent Match matches every payload.
	Match map[string][]string `mapstructure:"match" yaml:"match,omitempty" json:"match,omitempty"`

	// Alias is the target sandbox alias (km create --alias).
	Alias string `mapstructure:"alias" yaml:"alias" json:"alias"`

	// Profile is the SandboxProfile used for cold-create when the alias is absent.
	Profile string `mapstructure:"profile" yaml:"profile,omitempty" json:"profile,omitempty"`

	// OnAbsent is "cold-create" (default) or "skip".
	OnAbsent string `mapstructure:"on_absent" yaml:"on_absent,omitempty" json:"on_absent,omitempty"`

	// CooldownSeconds suppresses repeat dispatch of the same GroupBy key within
	// the window. 0 = no cooldown.
	CooldownSeconds int `mapstructure:"cooldown_seconds" yaml:"cooldown_seconds,omitempty" json:"cooldown_seconds,omitempty"`

	// GroupBy is a {{field}} template expanded against the envelope to form the
	// cooldown key. This is the coalescing lever: group on an entity id and the
	// first alert of a burst wins.
	GroupBy string `mapstructure:"group_by" yaml:"group_by,omitempty" json:"group_by,omitempty"`

	// Prompt is the agent's first turn. Supports @file loading and {{field}}
	// expansion against the envelope, including {{raw}}.
	Prompt string `mapstructure:"prompt" yaml:"prompt" json:"prompt"`
}

// WebhookSource is one named ingress. Name is the URL path segment, so
// POST /{name} routes here.
type WebhookSource struct {
	Name string      `mapstructure:"name" yaml:"name" json:"name"`
	Auth WebhookAuth `mapstructure:"auth" yaml:"auth" json:"auth"`

	// ReplayTTLSeconds bounds the replay-nonce window. 0 => DefaultReplayTTLSeconds.
	ReplayTTLSeconds int `mapstructure:"replay_ttl_seconds" yaml:"replay_ttl_seconds,omitempty" json:"replay_ttl_seconds,omitempty"`

	// FieldPaths is the escape hatch used ONLY when the payload carries no
	// km_schema. Keys: "id" (doubles as the replay key), "type", "severity",
	// "group". Values are dotted JSON paths with an optional leading "$.".
	FieldPaths map[string]string `mapstructure:"field_paths" yaml:"field_paths,omitempty" json:"field_paths,omitempty"`

	Rules []WebhookRule `mapstructure:"rules" yaml:"rules,omitempty" json:"rules,omitempty"`
}

// WebhookRateLimit is the install-wide storm breaker, shared across ALL sources:
// one fleet of sandboxes and one AI budget, so every source draws the same
// allowance. Counted AFTER the replay and cooldown gates, so suppressed
// duplicates cost nothing.
type WebhookRateLimit struct {
	MaxDispatches int `mapstructure:"max_dispatches" yaml:"max_dispatches" json:"max_dispatches"`
	WindowSeconds int `mapstructure:"window_seconds" yaml:"window_seconds" json:"window_seconds"`
}

// WebhooksConfig maps to km-config.yaml key `webhooks`.
// CRITICAL: "webhooks" must be in the v2->v merge-list in Load() or this block is
// silently dropped (project_config_key_merge_list). It is a NEW top-level key and
// does not piggyback on any parent's entry. Decoded atomically by
// v.UnmarshalKey("webhooks", &cfg.Webhooks).
type WebhooksConfig struct {
	RateLimit *WebhookRateLimit `mapstructure:"rate_limit" yaml:"rate_limit,omitempty" json:"rate_limit,omitempty"`
	Sources   []WebhookSource   `mapstructure:"sources" yaml:"sources,omitempty" json:"sources,omitempty"`
}

// DefaultReplayTTLSeconds is the replay-nonce window when a source omits
// replay_ttl_seconds.
const DefaultReplayTTLSeconds = 900
```

Add the field to the `Config` struct alongside `Checks`:

```go
	// Webhooks is the Phase 127 generic webhook ingress block (km-config.yaml
	// key `webhooks`). Absent => zero value => dormant.
	Webhooks WebhooksConfig `mapstructure:"webhooks" yaml:"webhooks,omitempty"`
```

- [ ] **Step 4: Add the merge-list entry and the UnmarshalKey call**

In the merge-list slice in `Load()` (after the `"launch_accounts",` entry):

```go
			// Phase 127: webhooks block (sources list-of-objects + rate_limit).
			// CRITICAL: without this entry the entire webhooks: block is silently
			// dropped regardless of km-config.yaml content
			// (project_config_key_merge_list footgun). NEW top-level key — it does
			// not piggyback on any parent's entry. Decoded atomically by
			// v.UnmarshalKey("webhooks", &cfg.Webhooks) below — do NOT add sibling
			// "webhooks.*" entries (mirrors the github, h1, and checks precedent).
			"webhooks",
```

Next to the `UnmarshalKey("checks", ...)` call near line 1249:

```go
	if err := v.UnmarshalKey("webhooks", &cfg.Webhooks); err != nil {
		return nil, fmt.Errorf("decode webhooks config: %w", err)
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/app/config/ -run TestLoad_Webhooks -v; echo "EXIT=$?"`
Expected: PASS, `EXIT=0`.

- [ ] **Step 6: Commit**

```bash
git add internal/app/config/config.go internal/app/config/config_webhooks_test.go
git commit -m "feat(config): add webhooks: block with merge-list entry"
```

---

### Task 2: Envelope parsing

**Files:**
- Create: `pkg/webhook/envelope.go`
- Test: `pkg/webhook/envelope_test.go`

**Interfaces:**
- Consumes: `config.WebhookSource` (Task 1).
- Produces: `webhook.Envelope{Schema, Source, DeliveryKey, Type, ID, Severity, Status, Title string; Entity map[string]string; URL string; Raw string}`, `webhook.ParseEnvelope(body []byte, src config.WebhookSource) (*Envelope, error)`, `webhook.Decompress(body []byte, contentEncoding string) ([]byte, error)`, `(*Envelope).Field(name string) (string, bool)`, `webhook.ErrUnparseable`.

- [ ] **Step 1: Write the failing test**

```go
package webhook

import (
	"bytes"
	"compress/gzip"
	"errors"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

const wizV1Body = `{
  "km_schema": "v1",
  "source": "wiz",
  "delivery_key": "iss-1:CREATED:2026-08-25T10:00:00Z",
  "type": "issue",
  "id": "iss-1",
  "severity": "CRITICAL",
  "status": "OPEN",
  "title": "Public S3 bucket",
  "entity": {"type":"BUCKET","name":"logs","cloud_platform":"AWS","cloud_id":"arn:aws:s3:::logs"},
  "url": "https://app.wiz.io/issues#~(issue~'iss-1)"
}`

func TestParseEnvelope_CanonicalV1(t *testing.T) {
	env, err := ParseEnvelope([]byte(wizV1Body), config.WebhookSource{Name: "wiz"})
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	if env.Schema != "v1" {
		t.Errorf("Schema: got %q, want v1", env.Schema)
	}
	if env.DeliveryKey != "iss-1:CREATED:2026-08-25T10:00:00Z" {
		t.Errorf("DeliveryKey: got %q", env.DeliveryKey)
	}
	if env.Severity != "CRITICAL" || env.Type != "issue" || env.ID != "iss-1" {
		t.Errorf("typed fields: %+v", env)
	}
	if env.Entity["cloud_id"] != "arn:aws:s3:::logs" {
		t.Errorf("Entity[cloud_id]: got %q", env.Entity["cloud_id"])
	}
	if env.Raw == "" {
		t.Error("Raw must always be populated")
	}
}

// Field() is what match and group_by read. Dotted entity access must work.
func TestEnvelope_Field(t *testing.T) {
	env, err := ParseEnvelope([]byte(wizV1Body), config.WebhookSource{Name: "wiz"})
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	cases := []struct {
		name string
		want string
		ok   bool
	}{
		{"severity", "CRITICAL", true},
		{"type", "issue", true},
		{"id", "iss-1", true},
		{"entity.cloud_id", "arn:aws:s3:::logs", true},
		{"entity.name", "logs", true},
		{"nope", "", false},
		{"entity.nope", "", false},
	}
	for _, c := range cases {
		got, ok := env.Field(c.name)
		if got != c.want || ok != c.ok {
			t.Errorf("Field(%q): got (%q,%v), want (%q,%v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

// No km_schema => the declared field_paths drive routing, and field_paths.id
// doubles as the replay key.
func TestParseEnvelope_FieldPathsFallback(t *testing.T) {
	body := `{"objectType":"issue","id":"abc","severity":"HIGH","entity":{"cloud_id":"i-1"}}`
	src := config.WebhookSource{
		Name: "generic",
		FieldPaths: map[string]string{
			"id":       "$.id",
			"type":     "$.objectType",
			"severity": "$.severity",
			"group":    "$.entity.cloud_id",
		},
	}
	env, err := ParseEnvelope([]byte(body), src)
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	if env.Schema != "" {
		t.Errorf("Schema: got %q, want empty", env.Schema)
	}
	if env.ID != "abc" || env.Type != "issue" || env.Severity != "HIGH" {
		t.Errorf("path-extracted fields: %+v", env)
	}
	if env.DeliveryKey != "abc" {
		t.Errorf("DeliveryKey must fall back to field_paths.id: got %q", env.DeliveryKey)
	}
	if got, _ := env.Field("group"); got != "i-1" {
		t.Errorf("Field(group): got %q, want i-1", got)
	}
}

// No km_schema and no field_paths: still parses, carries Raw, but exposes no
// routing fields — only an empty-match rule can fire.
func TestParseEnvelope_NoSchemaNoPaths(t *testing.T) {
	env, err := ParseEnvelope([]byte(`{"anything":1}`), config.WebhookSource{Name: "x"})
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	if _, ok := env.Field("severity"); ok {
		t.Error("Field(severity) must not resolve without schema or field_paths")
	}
	if env.Raw == "" {
		t.Error("Raw must always be populated")
	}
	if env.DeliveryKey == "" {
		t.Error("DeliveryKey must fall back to a body hash")
	}
}

func TestParseEnvelope_Unparseable(t *testing.T) {
	_, err := ParseEnvelope([]byte(`{not json`), config.WebhookSource{Name: "x"})
	if !errors.Is(err, ErrUnparseable) {
		t.Fatalf("want ErrUnparseable, got %v", err)
	}
}

func TestDecompress_Gzip(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(`{"a":1}`)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	got, err := Decompress(buf.Bytes(), "gzip")
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if string(got) != `{"a":1}` {
		t.Errorf("got %q", got)
	}

	// Absent/!gzip encoding is a passthrough.
	plain := []byte(`{"b":2}`)
	got, err = Decompress(plain, "")
	if err != nil || string(got) != `{"b":2}` {
		t.Errorf("passthrough: got %q err %v", got, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/webhook/ -run 'TestParseEnvelope|TestEnvelope_Field|TestDecompress' -v`
Expected: FAIL — package `pkg/webhook` does not exist.

- [ ] **Step 3: Write the implementation**

Create `pkg/webhook/envelope.go`:

```go
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

// canonical mirrors the km_schema v1 template shipped in docs/webhook-templates/.
type canonical struct {
	Schema      string            `json:"km_schema"`
	Source      string            `json:"source"`
	DeliveryKey string            `json:"delivery_key"`
	Type        string            `json:"type"`
	ID          string            `json:"id"`
	Severity    string            `json:"severity"`
	Status      string            `json:"status"`
	Title       string            `json:"title"`
	Entity      map[string]string `json:"entity"`
	URL         string            `json:"url"`
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

	var c canonical
	if err := json.Unmarshal(body, &c); err == nil && c.Schema == "v1" {
		env.Schema = c.Schema
		env.DeliveryKey = c.DeliveryKey
		env.Type = c.Type
		env.ID = c.ID
		env.Severity = c.Severity
		env.Status = c.Status
		env.Title = c.Title
		env.URL = c.URL
		if c.Entity != nil {
			env.Entity = c.Entity
		}
		if c.Source != "" {
			env.Source = c.Source
		}
	} else if len(src.FieldPaths) > 0 {
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/webhook/ -v; echo "EXIT=$?"`
Expected: PASS, `EXIT=0`.

- [ ] **Step 5: Commit**

```bash
git add pkg/webhook/envelope.go pkg/webhook/envelope_test.go
git commit -m "feat(webhook): parse canonical v1 envelope with field_paths fallback"
```

---

### Task 3: Authentication verifiers

**Files:**
- Create: `pkg/webhook/auth.go`
- Test: `pkg/webhook/auth_test.go`

**Interfaces:**
- Consumes: `config.WebhookAuth` (Task 1).
- Produces: `webhook.Authenticate(auth config.WebhookAuth, secret string, headers map[string]string, body []byte) error`, `webhook.ErrUnauthorized`.

- [ ] **Step 1: Write the failing test**

```go
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

func TestAuthenticate_Bearer(t *testing.T) {
	auth := config.WebhookAuth{Type: "bearer", Header: "Authorization"}
	body := []byte(`{}`)

	t.Run("exact match with Bearer prefix", func(t *testing.T) {
		hdrs := map[string]string{"authorization": "Bearer s3cret"}
		if err := Authenticate(auth, "s3cret", hdrs, body); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("bare token without prefix", func(t *testing.T) {
		hdrs := map[string]string{"authorization": "s3cret"}
		if err := Authenticate(auth, "s3cret", hdrs, body); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		hdrs := map[string]string{"authorization": "Bearer nope"}
		if !errors.Is(Authenticate(auth, "s3cret", hdrs, body), ErrUnauthorized) {
			t.Fatal("want ErrUnauthorized")
		}
	})

	t.Run("missing header", func(t *testing.T) {
		if !errors.Is(Authenticate(auth, "s3cret", map[string]string{}, body), ErrUnauthorized) {
			t.Fatal("want ErrUnauthorized")
		}
	})

	// The critical negative: an empty configured secret must NEVER authenticate.
	// A naive constant-time compare of "" vs "" succeeds, which would leave the
	// endpoint wide open whenever SSM returned an empty parameter.
	t.Run("empty configured secret fails closed", func(t *testing.T) {
		hdrs := map[string]string{"authorization": ""}
		if !errors.Is(Authenticate(auth, "", hdrs, body), ErrUnauthorized) {
			t.Fatal("empty secret must fail closed")
		}
	})

	t.Run("custom header name", func(t *testing.T) {
		a := config.WebhookAuth{Type: "bearer", Header: "X-Km-Token"}
		hdrs := map[string]string{"x-km-token": "s3cret"}
		if err := Authenticate(a, "s3cret", hdrs, body); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})
}

func TestAuthenticate_HMAC(t *testing.T) {
	body := []byte(`{"a":1}`)
	secret := "key"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))

	auth := config.WebhookAuth{Type: "hmac", Header: "X-Signature"}

	t.Run("valid signature", func(t *testing.T) {
		hdrs := map[string]string{"x-signature": sig}
		if err := Authenticate(auth, secret, hdrs, body); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("sha256= prefix accepted", func(t *testing.T) {
		hdrs := map[string]string{"x-signature": "sha256=" + sig}
		if err := Authenticate(auth, secret, hdrs, body); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("tampered body", func(t *testing.T) {
		hdrs := map[string]string{"x-signature": sig}
		if !errors.Is(Authenticate(auth, secret, hdrs, []byte(`{"a":2}`)), ErrUnauthorized) {
			t.Fatal("want ErrUnauthorized")
		}
	})

	t.Run("empty secret fails closed", func(t *testing.T) {
		hdrs := map[string]string{"x-signature": sig}
		if !errors.Is(Authenticate(auth, "", hdrs, body), ErrUnauthorized) {
			t.Fatal("empty secret must fail closed")
		}
	})
}

func TestAuthenticate_UnknownTypeFailsClosed(t *testing.T) {
	auth := config.WebhookAuth{Type: "magic", Header: "Authorization"}
	hdrs := map[string]string{"authorization": "anything"}
	if !errors.Is(Authenticate(auth, "s", hdrs, []byte(`{}`)), ErrUnauthorized) {
		t.Fatal("unknown auth type must fail closed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/webhook/ -run TestAuthenticate -v`
Expected: FAIL — `Authenticate` undefined.

- [ ] **Step 3: Write the implementation**

Create `pkg/webhook/auth.go`:

```go
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ErrUnauthorized is returned for every authentication failure. The caller logs
// it and returns 200 with no dispatch — never a 401, which tells a prober
// exactly what it wants to know and makes some senders retry.
var ErrUnauthorized = errors.New("webhook: unauthorized")

// DefaultAuthHeader is used when a source omits auth.header.
const DefaultAuthHeader = "Authorization"

// Authenticate verifies an inbound request against a source's auth config.
// headers MUST be lowercase-keyed (Lambda Function URL delivers them that way).
//
// Fails closed on every ambiguity: unknown type, empty configured secret, and
// missing header. The empty-secret case is the one worth naming — a bare
// constant-time compare of "" against "" SUCCEEDS, so an SSM parameter that
// resolved to empty would silently open the endpoint to anyone.
func Authenticate(auth config.WebhookAuth, secret string, headers map[string]string, body []byte) error {
	if secret == "" {
		return ErrUnauthorized
	}

	name := auth.Header
	if name == "" {
		name = DefaultAuthHeader
	}
	provided, ok := headers[strings.ToLower(name)]
	if !ok || provided == "" {
		return ErrUnauthorized
	}

	switch strings.ToLower(auth.Type) {
	case "bearer":
		token := strings.TrimSpace(strings.TrimPrefix(provided, "Bearer "))
		if subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1 {
			return nil
		}
		return ErrUnauthorized

	case "hmac":
		sig := strings.TrimSpace(strings.TrimPrefix(provided, "sha256="))
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		if subtle.ConstantTimeCompare([]byte(sig), []byte(want)) == 1 {
			return nil
		}
		return ErrUnauthorized

	default:
		return ErrUnauthorized
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/webhook/ -run TestAuthenticate -v; echo "EXIT=$?"`
Expected: PASS, `EXIT=0`.

- [ ] **Step 5: Commit**

```bash
git add pkg/webhook/auth.go pkg/webhook/auth_test.go
git commit -m "feat(webhook): bearer and hmac verifiers, fail-closed on empty secret"
```

---

### Task 4: Rule matching and template expansion

**Files:**
- Create: `pkg/webhook/match.go`
- Test: `pkg/webhook/match_test.go`

**Interfaces:**
- Consumes: `webhook.Envelope` (Task 2), `config.WebhookSource`/`config.WebhookRule` (Task 1).
- Produces: `webhook.MatchRule(env *Envelope, rules []config.WebhookRule) (rule *config.WebhookRule, idx int)`, `webhook.ExpandTemplate(tmpl string, env *Envelope) string`.

- [ ] **Step 1: Write the failing test**

```go
package webhook

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

func envFixture(t *testing.T) *Envelope {
	t.Helper()
	env, err := ParseEnvelope([]byte(wizV1Body), config.WebhookSource{Name: "wiz"})
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	return env
}

func TestMatchRule_FirstMatchWins(t *testing.T) {
	env := envFixture(t)
	rules := []config.WebhookRule{
		{Match: map[string][]string{"severity": {"LOW"}}, Alias: "no"},
		{Match: map[string][]string{"severity": {"CRITICAL", "HIGH"}}, Alias: "yes"},
		{Match: map[string][]string{"severity": {"CRITICAL"}}, Alias: "also-yes"},
	}
	got, idx := MatchRule(env, rules)
	if got == nil {
		t.Fatal("expected a match")
	}
	if got.Alias != "yes" || idx != 1 {
		t.Errorf("got alias=%q idx=%d, want yes/1", got.Alias, idx)
	}
}

// All named fields must match (AND); values within a field are OR.
func TestMatchRule_AndAcrossFields(t *testing.T) {
	env := envFixture(t)

	both := []config.WebhookRule{{Match: map[string][]string{
		"type": {"issue"}, "severity": {"CRITICAL"},
	}, Alias: "hit"}}
	if got, _ := MatchRule(env, both); got == nil {
		t.Error("both fields matching should hit")
	}

	one := []config.WebhookRule{{Match: map[string][]string{
		"type": {"issue"}, "severity": {"LOW"},
	}, Alias: "miss"}}
	if got, _ := MatchRule(env, one); got != nil {
		t.Error("one field failing must not hit")
	}
}

// A field the envelope does not carry is a NON-match, never a wildcard.
// A typo'd path must dispatch nothing rather than everything.
func TestMatchRule_UnknownFieldFailsClosed(t *testing.T) {
	env := envFixture(t)
	rules := []config.WebhookRule{
		{Match: map[string][]string{"sevrity": {"CRITICAL"}}, Alias: "typo"},
	}
	if got, _ := MatchRule(env, rules); got != nil {
		t.Fatal("a match on an unknown field must fail closed")
	}
}

func TestMatchRule_EmptyMatchIsWildcard(t *testing.T) {
	env := envFixture(t)
	rules := []config.WebhookRule{{Alias: "catch-all"}}
	got, _ := MatchRule(env, rules)
	if got == nil || got.Alias != "catch-all" {
		t.Fatal("an empty match block must match everything")
	}
}

func TestMatchRule_NoRulesNoMatch(t *testing.T) {
	if got, idx := MatchRule(envFixture(t), nil); got != nil || idx != -1 {
		t.Errorf("got (%v,%d), want (nil,-1)", got, idx)
	}
}

func TestExpandTemplate(t *testing.T) {
	env := envFixture(t)

	got := ExpandTemplate("sev={{severity}} entity={{entity.name}} type={{type}}", env)
	want := "sev=CRITICAL entity=logs type=issue"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// Unknown vars are left verbatim — same decision as ExpandEventTemplate in
	// the GitHub bridge. Silent blanking hides typos.
	if got := ExpandTemplate("x={{nope}}", env); got != "x={{nope}}" {
		t.Errorf("unknown var: got %q", got)
	}

	// {{raw}} carries the whole payload into the prompt.
	if got := ExpandTemplate("{{raw}}", env); got != env.Raw {
		t.Error("{{raw}} must expand to the full body")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/webhook/ -run 'TestMatchRule|TestExpandTemplate' -v`
Expected: FAIL — `MatchRule` undefined.

- [ ] **Step 3: Write the implementation**

Create `pkg/webhook/match.go`:

```go
package webhook

import (
	"regexp"
	"strings"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// templateVar matches {{name}} and {{dotted.name}}.
var templateVar = regexp.MustCompile(`\{\{([a-zA-Z0-9_.]+)\}\}`)

// MatchRule returns the first rule whose Match block passes, and its index.
// Returns (nil, -1) when nothing matches.
//
// Semantics: all named fields must match (AND); within a field any listed value
// matches (OR); an empty Match matches everything. A field ABSENT from the
// envelope is a non-match, never a wildcard — that is what makes a typo'd field
// name dispatch nothing instead of everything.
func MatchRule(env *Envelope, rules []config.WebhookRule) (*config.WebhookRule, int) {
	for i := range rules {
		if ruleMatches(env, rules[i]) {
			return &rules[i], i
		}
	}
	return nil, -1
}

func ruleMatches(env *Envelope, r config.WebhookRule) bool {
	for field, allowed := range r.Match {
		val, ok := env.Field(field)
		if !ok {
			return false // fail closed on an unknown/absent field
		}
		if !containsFold(allowed, val) {
			return false
		}
	}
	return true
}

func containsFold(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

// ExpandTemplate replaces {{field}} references with envelope values. Unknown
// variables are left VERBATIM (matching ExpandEventTemplate in the GitHub
// bridge): a silently blanked variable hides an operator typo, whereas a
// literal {{nope}} in a prompt is self-diagnosing.
//
// Note for readers: Wiz templates also use {{...}}, but Wiz renders its own
// variables before sending. By the time this runs, the body is already
// rendered — there is no collision.
func ExpandTemplate(tmpl string, env *Envelope) string {
	return templateVar.ReplaceAllStringFunc(tmpl, func(m string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(m, "{{"), "}}")
		if v, ok := env.Field(name); ok {
			return v
		}
		return m
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/webhook/ -v; echo "EXIT=$?"`
Expected: PASS, `EXIT=0`.

- [ ] **Step 5: Commit**

```bash
git add pkg/webhook/match.go pkg/webhook/match_test.go
git commit -m "feat(webhook): first-match rule engine and template expansion"
```

---

### Task 5: Storm control — replay, cooldown, rate ceiling

**Files:**
- Create: `pkg/webhook/storm.go`
- Test: `pkg/webhook/storm_test.go`

**Interfaces:**
- Consumes: `webhook.Envelope` (Task 2).
- Produces: `webhook.NonceStore` interface (`CheckAndStore(ctx, key string, ttlSeconds int) (bool, error)`), `webhook.RateCounter` interface (`Increment(ctx, key string, ttlSeconds int) (int64, error)`), `webhook.ReplayKey(source string, env *Envelope) string`, `webhook.CooldownKey(source string, ruleIdx int, groupBy string, env *Envelope) string`, `webhook.RateKey(nowUnix int64, windowSeconds int) string`, `webhook.CheckRate(ctx, rc RateCounter, limit *config.WebhookRateLimit, nowUnix int64) (allowed bool)`.

- [ ] **Step 1: Write the failing test**

```go
package webhook

import (
	"context"
	"errors"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

type fakeNonce struct {
	seen map[string]bool
	err  error
}

func (f *fakeNonce) CheckAndStore(_ context.Context, key string, _ int) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if f.seen[key] {
		return true, nil
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	f.seen[key] = true
	return false, nil
}

type fakeRate struct {
	n   int64
	err error
}

func (f *fakeRate) Increment(_ context.Context, _ string, _ int) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.n++
	return f.n, nil
}

func TestReplayKey_UsesDeliveryKey(t *testing.T) {
	env := envFixture(t)
	got := ReplayKey("wiz", env)
	want := "wh:wiz:iss-1:CREATED:2026-08-25T10:00:00Z"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCooldownKey_ExpandsGroupBy(t *testing.T) {
	env := envFixture(t)
	got := CooldownKey("wiz", 0, "{{entity.cloud_id}}", env)
	want := "wh-cd:wiz:0:arn:aws:s3:::logs"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A group_by naming a missing field must NOT collapse distinct entities onto one
// key — that would suppress unrelated alerts for the whole window.
func TestCooldownKey_MissingFieldStaysDistinct(t *testing.T) {
	env := envFixture(t)
	a := CooldownKey("wiz", 0, "{{entity.nope}}", env)

	other, err := ParseEnvelope([]byte(`{"km_schema":"v1","id":"iss-2","severity":"HIGH","entity":{"cloud_id":"x"}}`),
		config.WebhookSource{Name: "wiz"})
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}
	b := CooldownKey("wiz", 0, "{{entity.nope}}", other)

	if a == b {
		t.Fatalf("distinct payloads collapsed onto one cooldown key: %q", a)
	}
}

func TestCooldownKey_EmptyGroupByFallsBackToDeliveryKey(t *testing.T) {
	env := envFixture(t)
	if got, want := CooldownKey("wiz", 2, "", env), "wh-cd:wiz:2:"+env.DeliveryKey; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRateKey_BucketsByWindow(t *testing.T) {
	if a, b := RateKey(1000, 600), RateKey(1100, 600); a != b {
		t.Errorf("same window must share a bucket: %q vs %q", a, b)
	}
	if a, b := RateKey(1000, 600), RateKey(1700, 600); a == b {
		t.Errorf("different windows must not share a bucket: %q", a)
	}
}

func TestCheckRate(t *testing.T) {
	ctx := context.Background()
	limit := &config.WebhookRateLimit{MaxDispatches: 2, WindowSeconds: 600}

	rc := &fakeRate{}
	if !CheckRate(ctx, rc, limit, 1000) {
		t.Error("1st dispatch must be allowed")
	}
	if !CheckRate(ctx, rc, limit, 1000) {
		t.Error("2nd dispatch must be allowed (at the cap)")
	}
	if CheckRate(ctx, rc, limit, 1000) {
		t.Error("3rd dispatch must be blocked (over the cap)")
	}
}

func TestCheckRate_NilLimitAllows(t *testing.T) {
	if !CheckRate(context.Background(), &fakeRate{}, nil, 1000) {
		t.Error("absent rate_limit must allow (dormant)")
	}
}

// Fail-open: a counter error must never strand a real alert.
func TestCheckRate_ErrorFailsOpen(t *testing.T) {
	rc := &fakeRate{err: errors.New("ddb down")}
	limit := &config.WebhookRateLimit{MaxDispatches: 1, WindowSeconds: 600}
	if !CheckRate(context.Background(), rc, limit, 1000) {
		t.Error("rate counter error must fail OPEN")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/webhook/ -run 'TestReplayKey|TestCooldownKey|TestRateKey|TestCheckRate' -v`
Expected: FAIL — `ReplayKey` undefined.

- [ ] **Step 3: Write the implementation**

Create `pkg/webhook/storm.go`:

```go
package webhook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// NonceStore is the atomic check-and-store primitive backing both the replay
// nonce and the cooldown gate. Satisfied by the shared km nonces DynamoDB table
// (a conditional PutItem with a TTL attribute).
type NonceStore interface {
	// CheckAndStore returns (true, nil) when the key was already present
	// (replay/suppressed), (false, nil) on first insertion, (false, err) on
	// storage failure.
	CheckAndStore(ctx context.Context, key string, ttlSeconds int) (alreadySeen bool, err error)
}

// RateCounter is the atomic counter backing the install-wide rate ceiling.
// Increment returns the post-increment value for the bucket.
type RateCounter interface {
	Increment(ctx context.Context, key string, ttlSeconds int) (int64, error)
}

// ReplayKey is the dedup key for an inbound delivery.
func ReplayKey(source string, env *Envelope) string {
	return fmt.Sprintf("wh:%s:%s", source, env.DeliveryKey)
}

// CooldownKey is the suppression key for a (source, rule, group) triple.
//
// When groupBy expands to a template that still contains an unresolved variable,
// the raw expansion is hashed together with the delivery key so distinct
// payloads keep distinct keys. Collapsing them would suppress unrelated alerts
// for the whole window — the opposite of what a cooldown is for.
func CooldownKey(source string, ruleIdx int, groupBy string, env *Envelope) string {
	if groupBy == "" {
		return fmt.Sprintf("wh-cd:%s:%d:%s", source, ruleIdx, env.DeliveryKey)
	}
	expanded := ExpandTemplate(groupBy, env)
	if expanded == groupBy {
		// Nothing resolved — fall back to per-delivery uniqueness.
		sum := sha256.Sum256([]byte(groupBy + "\x00" + env.DeliveryKey))
		expanded = "unresolved:" + hex.EncodeToString(sum[:8])
	}
	return fmt.Sprintf("wh-cd:%s:%d:%s", source, ruleIdx, expanded)
}

// RateKey buckets a timestamp into a fixed window.
func RateKey(nowUnix int64, windowSeconds int) string {
	if windowSeconds <= 0 {
		windowSeconds = 1
	}
	return fmt.Sprintf("wh-rate:%d", nowUnix/int64(windowSeconds))
}

// CheckRate reports whether a dispatch is within the install-wide ceiling.
//
// The ceiling is install-wide across ALL sources on purpose: one fleet of
// sandboxes and one AI budget, so every source draws down the same allowance.
// Callers MUST invoke this only after the replay and cooldown gates, so
// suppressed duplicates never consume budget.
//
// Fails OPEN on counter errors — matching the cooldown gate and the existing
// bridges. A transient DynamoDB error must never strand a real alert.
func CheckRate(ctx context.Context, rc RateCounter, limit *config.WebhookRateLimit, nowUnix int64) bool {
	if limit == nil || limit.MaxDispatches <= 0 {
		return true
	}
	key := RateKey(nowUnix, limit.WindowSeconds)
	n, err := rc.Increment(ctx, key, limit.WindowSeconds*2)
	if err != nil {
		slog.Warn("webhook_rate_counter_error", "key", key, "error", err.Error())
		return true
	}
	if n > int64(limit.MaxDispatches) {
		slog.Warn("webhook_rate_ceiling_tripped",
			"key", key, "count", n, "max", limit.MaxDispatches)
		return false
	}
	return true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/webhook/ -v; echo "EXIT=$?"`
Expected: PASS, `EXIT=0`.

- [ ] **Step 5: Commit**

```bash
git add pkg/webhook/storm.go pkg/webhook/storm_test.go
git commit -m "feat(webhook): replay, cooldown, and install-wide rate ceiling"
```

---

### Task 6: Bridge handler and dispatch decision

**Files:**
- Create: `pkg/webhook/bridge/interfaces.go`
- Create: `pkg/webhook/bridge/handler.go`
- Test: `pkg/webhook/bridge/handler_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–5.
- Produces: `bridge.Handler` struct with `Handle(ctx context.Context, req bridge.Request) bridge.Response`; `bridge.Request{Path, ContentEncoding string; Headers map[string]string; Body []byte}`; `bridge.Response{Status int; Log string}`; interfaces `SecretFetcher`, `AliasResolver`, `QueueSender`, `Resumer`, `StatusWriter`, `ColdCreator`; sentinel `bridge.ErrNoResumableInstance`.

- [ ] **Step 1: Write the failing test**

```go
package bridge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

const v1Body = `{"km_schema":"v1","source":"wiz","delivery_key":"iss-1:CREATED:t0",
"type":"issue","id":"iss-1","severity":"CRITICAL","status":"OPEN","title":"Public bucket",
"entity":{"type":"BUCKET","name":"logs","cloud_id":"arn:1"},"url":"https://app.wiz.io/x"}`

type stubs struct {
	secret     string
	sandboxID  string
	status     string
	resolveErr error
	resumeErr  error

	enqueued  []string
	resumed   []string
	created   []string
	rowsKilled []string
}

func (s *stubs) Fetch(_ context.Context, _ string) (string, error) { return s.secret, nil }
func (s *stubs) ResolveByAliasWithStatus(_ context.Context, _ string) (string, string, error) {
	return s.sandboxID, s.status, s.resolveErr
}
func (s *stubs) QueueURL(_ context.Context, _ string) (string, error) { return "https://q", nil }
func (s *stubs) Send(_ context.Context, _, groupID, body string) error {
	s.enqueued = append(s.enqueued, groupID+"|"+body)
	return nil
}
func (s *stubs) StartSandbox(_ context.Context, id string) error {
	s.resumed = append(s.resumed, id)
	return s.resumeErr
}
func (s *stubs) DeleteSandboxRow(_ context.Context, id string) error {
	s.rowsKilled = append(s.rowsKilled, id)
	return nil
}
func (s *stubs) ColdCreate(_ context.Context, alias, _, _ string) error {
	s.created = append(s.created, alias)
	return nil
}

func newHandler(t *testing.T, s *stubs) *Handler {
	t.Helper()
	return &Handler{
		Sources: []config.WebhookSource{{
			Name: "wiz",
			Auth: config.WebhookAuth{Type: "bearer", Header: "Authorization", SecretPath: "/p"},
			Rules: []config.WebhookRule{{
				Match:    map[string][]string{"severity": {"CRITICAL"}},
				Alias:    "ir-bot",
				Profile:  "ir-bot",
				OnAbsent: "cold-create",
				GroupBy:  "{{entity.cloud_id}}",
				Prompt:   "Triage {{title}} on {{entity.name}}",
			}},
		}},
		Secrets:  s,
		Resolver: s,
		Queue:    s,
		Resumer:  s,
		Status:   s,
		Cold:     s,
		Nonces:   &fakeNonce{},
		Rates:    &fakeRate{},
		Now:      func() int64 { return 1000 },
	}
}

func authedReq() Request {
	return Request{
		Path:    "/wiz",
		Headers: map[string]string{"authorization": "Bearer tok"},
		Body:    []byte(v1Body),
	}
}

func TestHandle_WarmEnqueue(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "running"}
	resp := newHandler(t, s).Handle(context.Background(), authedReq())

	if resp.Status != 200 {
		t.Fatalf("status: got %d, want 200", resp.Status)
	}
	if len(s.enqueued) != 1 {
		t.Fatalf("enqueued: got %d, want 1", len(s.enqueued))
	}
	// MessageGroupId is the sandbox id => fully serial per box.
	if !strings.HasPrefix(s.enqueued[0], "sb-1|") {
		t.Errorf("MessageGroupId must be the sandbox id: %q", s.enqueued[0])
	}
	if !strings.Contains(s.enqueued[0], "Triage Public bucket on logs") {
		t.Errorf("prompt not expanded: %q", s.enqueued[0])
	}
	if len(s.created) != 0 {
		t.Errorf("must not cold-create when warm: %v", s.created)
	}
}

func TestHandle_StoppedResumesThenEnqueues(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "stopped"}
	newHandler(t, s).Handle(context.Background(), authedReq())

	if len(s.resumed) != 1 || s.resumed[0] != "sb-1" {
		t.Errorf("resumed: %v", s.resumed)
	}
	if len(s.enqueued) != 1 {
		t.Errorf("must still enqueue after resume: %v", s.enqueued)
	}
}

func TestHandle_AbsentAliasColdCreates(t *testing.T) {
	s := &stubs{secret: "tok", resolveErr: errors.New("not found")}
	newHandler(t, s).Handle(context.Background(), authedReq())

	if len(s.created) != 1 || s.created[0] != "ir-bot" {
		t.Errorf("created: %v", s.created)
	}
	if len(s.enqueued) != 0 {
		t.Errorf("cold path must not enqueue: %v", s.enqueued)
	}
}

// Phase 109 self-heal: a terminal resume failure means the row is orphaned.
// Delete it, then cold-create — otherwise the stale row shadows creation forever.
func TestHandle_TerminalResumeFailureSelfHeals(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "stopped",
		resumeErr: ErrNoResumableInstance}
	newHandler(t, s).Handle(context.Background(), authedReq())

	if len(s.rowsKilled) != 1 || s.rowsKilled[0] != "sb-1" {
		t.Errorf("stale row must be deleted: %v", s.rowsKilled)
	}
	if len(s.created) != 1 {
		t.Errorf("must fall through to cold-create: %v", s.created)
	}
	if len(s.enqueued) != 0 {
		t.Errorf("must not enqueue to a dead box: %v", s.enqueued)
	}
}

func TestHandle_OnAbsentSkip(t *testing.T) {
	s := &stubs{secret: "tok", resolveErr: errors.New("not found")}
	h := newHandler(t, s)
	h.Sources[0].Rules[0].OnAbsent = "skip"
	h.Handle(context.Background(), authedReq())

	if len(s.created) != 0 {
		t.Errorf("on_absent: skip must not cold-create: %v", s.created)
	}
}

func TestHandle_BadAuthDropsWith200(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "running"}
	req := authedReq()
	req.Headers["authorization"] = "Bearer wrong"

	resp := newHandler(t, s).Handle(context.Background(), req)
	if resp.Status != 200 {
		t.Errorf("must still return 200: got %d", resp.Status)
	}
	if len(s.enqueued) != 0 {
		t.Errorf("unauthorized must not dispatch: %v", s.enqueued)
	}
}

func TestHandle_UnknownSourcePathDrops(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "running"}
	req := authedReq()
	req.Path = "/nope"

	resp := newHandler(t, s).Handle(context.Background(), req)
	if resp.Status != 200 || len(s.enqueued) != 0 {
		t.Errorf("unknown source must drop: status=%d enqueued=%v", resp.Status, s.enqueued)
	}
}

func TestHandle_ReplayDropped(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "running"}
	h := newHandler(t, s)
	h.Handle(context.Background(), authedReq())
	h.Handle(context.Background(), authedReq())

	if len(s.enqueued) != 1 {
		t.Errorf("replay must be dropped: %d dispatches", len(s.enqueued))
	}
}

func TestHandle_UnparseableReturns200NoDispatch(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "running"}
	req := authedReq()
	req.Body = []byte(`{not json`)

	resp := newHandler(t, s).Handle(context.Background(), req)
	if resp.Status != 200 {
		t.Errorf("status: got %d, want 200", resp.Status)
	}
	if len(s.enqueued) != 0 {
		t.Errorf("must not dispatch: %v", s.enqueued)
	}
}

func TestHandle_NoMatchingRuleDrops(t *testing.T) {
	s := &stubs{secret: "tok", sandboxID: "sb-1", status: "running"}
	h := newHandler(t, s)
	h.Sources[0].Rules[0].Match = map[string][]string{"severity": {"LOW"}}
	h.Handle(context.Background(), authedReq())

	if len(s.enqueued) != 0 {
		t.Errorf("non-matching payload must not dispatch: %v", s.enqueued)
	}
}
```

Add the `fakeNonce`/`fakeRate` helpers to this package too (the ones in Task 5 live in `pkg/webhook`):

```go
// fakes.go equivalents, in handler_test.go
type fakeNonce struct{ seen map[string]bool }

func (f *fakeNonce) CheckAndStore(_ context.Context, key string, _ int) (bool, error) {
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[key] {
		return true, nil
	}
	f.seen[key] = true
	return false, nil
}

type fakeRate struct{ n int64 }

func (f *fakeRate) Increment(_ context.Context, _ string, _ int) (int64, error) {
	f.n++
	return f.n, nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/webhook/bridge/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the interfaces**

Create `pkg/webhook/bridge/interfaces.go`:

```go
// Package bridge implements the webhook ingress dispatch decision. It is the
// generic analog of pkg/github/bridge and pkg/h1/bridge, minus the pieces those
// need for two-way conversations: there is NO thread store, NO reaction poster,
// and NO comment back-channel, because a webhook source is one-way.
package bridge

import (
	"context"
	"errors"
)

// ErrNoResumableInstance signals that an alias resolved to a DynamoDB row whose
// EC2 instance no longer exists — terminated out from under km. It is TERMINAL:
// the caller deletes the stale row and cold-creates. Transient DescribeInstances
// or StartInstances failures must NOT be wrapped in this, or a blip would delete
// a live sandbox's row. Mirrors the Phase 109 GitHub-bridge sentinel.
var ErrNoResumableInstance = errors.New("webhook-bridge: no resumable instance")

// SecretFetcher reads a source's shared secret from SSM (cached per cold start).
type SecretFetcher interface {
	Fetch(ctx context.Context, ssmPath string) (string, error)
}

// AliasResolver resolves a sandbox alias to its id, status, and inbound queue URL.
type AliasResolver interface {
	// ResolveByAliasWithStatus returns (sandboxID, status, nil) when the alias
	// exists. status "" is treated as absent. An error means the alias is absent
	// and the caller takes the cold path.
	ResolveByAliasWithStatus(ctx context.Context, alias string) (sandboxID, status string, err error)

	// QueueURL returns the webhook_inbound_queue_url attribute for a sandbox.
	QueueURL(ctx context.Context, sandboxID string) (string, error)
}

// QueueSender enqueues an envelope onto a per-sandbox FIFO queue.
type QueueSender interface {
	// Send posts body to queueURL with the given MessageGroupId. The group id is
	// the SANDBOX ID, making delivery fully serial per box: two triage turns
	// racing in one /workspace is a bug, not throughput.
	Send(ctx context.Context, queueURL, groupID, body string) error
}

// Resumer starts a stopped or paused sandbox.
type Resumer interface {
	StartSandbox(ctx context.Context, sandboxID string) error
}

// StatusWriter clears an orphaned sandbox row (UpdateItem/DeleteItem only —
// never PutItem, which strips un-marshalled attributes).
type StatusWriter interface {
	DeleteSandboxRow(ctx context.Context, sandboxID string) error
}

// ColdCreator publishes a SandboxCreate event carrying the expanded prompt.
type ColdCreator interface {
	ColdCreate(ctx context.Context, alias, profile, prompt string) error
}
```

- [ ] **Step 4: Write the handler**

Create `pkg/webhook/bridge/handler.go`:

```go
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/webhook"
)

// Request is the transport-neutral view of an inbound Function URL request.
type Request struct {
	Path            string
	ContentEncoding string
	Headers         map[string]string // MUST be lowercase-keyed
	Body            []byte
}

// Response carries the HTTP status and a diagnostic log tag.
type Response struct {
	Status int
	Log    string
}

// Envelope is what lands on the sandbox queue and what the poller renders.
type QueueEnvelope struct {
	Source   string `json:"source"`
	Type     string `json:"type"`
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Prompt   string `json:"prompt"`
	Raw      string `json:"raw"`
}

// Handler owns the dispatch decision for every configured source.
type Handler struct {
	Sources   []config.WebhookSource
	RateLimit *config.WebhookRateLimit

	Secrets  SecretFetcher
	Resolver AliasResolver
	Queue    QueueSender
	Resumer  Resumer
	Status   StatusWriter
	Cold     ColdCreator
	Nonces   webhook.NonceStore
	Rates    webhook.RateCounter

	// Now returns the current unix time; injected for deterministic tests.
	Now func() int64
}

// ok is the single response constructor. EVERY path returns 200, including
// internal errors: a non-200 makes the sender redeliver, and for a source with
// a delivery id that redelivery walks straight past dedup.
func ok(tag string) Response { return Response{Status: 200, Log: tag} }

// Handle runs the full pipeline for one inbound request.
func (h *Handler) Handle(ctx context.Context, req Request) Response {
	src := h.findSource(req.Path)
	if src == nil {
		slog.WarnContext(ctx, "webhook_unknown_source", "path", req.Path)
		return ok("unknown_source")
	}

	body, err := webhook.Decompress(req.Body, req.ContentEncoding)
	if err != nil {
		slog.WarnContext(ctx, "webhook_decompress_failed", "source", src.Name, "error", err.Error())
		return ok("decompress_failed")
	}

	secret, err := h.Secrets.Fetch(ctx, src.Auth.SecretPath)
	if err != nil {
		slog.ErrorContext(ctx, "webhook_secret_fetch_failed", "source", src.Name, "error", err.Error())
		return ok("secret_fetch_failed")
	}
	if err := webhook.Authenticate(src.Auth, secret, req.Headers, body); err != nil {
		slog.WarnContext(ctx, "webhook_unauthorized", "source", src.Name)
		return ok("unauthorized")
	}

	env, err := webhook.ParseEnvelope(body, *src)
	if err != nil {
		slog.WarnContext(ctx, "webhook_unparseable", "source", src.Name, "error", err.Error())
		return ok("unparseable")
	}

	// Replay gate — fail CLOSED on a storage error would strand real alerts, so
	// this follows the bridges: an error logs and proceeds.
	replayKey := webhook.ReplayKey(src.Name, env)
	ttl := src.ReplayTTLSeconds
	if ttl <= 0 {
		ttl = config.DefaultReplayTTLSeconds
	}
	if seen, nerr := h.Nonces.CheckAndStore(ctx, replayKey, ttl); nerr != nil {
		slog.WarnContext(ctx, "webhook_replay_nonce_error", "source", src.Name, "error", nerr.Error())
	} else if seen {
		slog.DebugContext(ctx, "webhook_replay_dropped", "source", src.Name, "key", replayKey)
		return ok("replay")
	}

	rule, idx := webhook.MatchRule(env, src.Rules)
	if rule == nil {
		slog.DebugContext(ctx, "webhook_no_rule_match", "source", src.Name, "id", env.ID)
		return ok("no_match")
	}

	// Cooldown gate — fail OPEN.
	if rule.CooldownSeconds > 0 {
		key := webhook.CooldownKey(src.Name, idx, rule.GroupBy, env)
		if seen, cerr := h.Nonces.CheckAndStore(ctx, key, rule.CooldownSeconds); cerr != nil {
			slog.WarnContext(ctx, "webhook_cooldown_nonce_error", "source", src.Name, "error", cerr.Error())
		} else if seen {
			slog.DebugContext(ctx, "webhook_cooldown_suppressed", "source", src.Name, "key", key)
			return ok("cooldown")
		}
	}

	// Rate ceiling — AFTER replay and cooldown, so suppressed duplicates cost
	// nothing. Fails OPEN.
	if !webhook.CheckRate(ctx, h.Rates, h.RateLimit, h.Now()) {
		return ok("rate_limited")
	}

	prompt := webhook.ExpandTemplate(rule.Prompt, env)
	return h.dispatch(ctx, src.Name, rule, env, prompt)
}

func (h *Handler) findSource(path string) *config.WebhookSource {
	name := strings.Trim(path, "/")
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}
	for i := range h.Sources {
		if strings.EqualFold(h.Sources[i].Name, name) {
			return &h.Sources[i]
		}
	}
	return nil
}

// dispatch resolves the alias and routes warm / resume / cold.
func (h *Handler) dispatch(ctx context.Context, source string, rule *config.WebhookRule,
	env *webhook.Envelope, prompt string) Response {

	sandboxID, status, rerr := h.Resolver.ResolveByAliasWithStatus(ctx, rule.Alias)
	if rerr != nil || status == "" {
		return h.coldPath(ctx, rule, env, prompt, "absent")
	}

	if isStopped(status) {
		if serr := h.Resumer.StartSandbox(ctx, sandboxID); serr != nil {
			if errors.Is(serr, ErrNoResumableInstance) {
				// Terminal: the row is orphaned. Clear it so the alias reads as
				// absent, then cold-create. Leaving it would let the stale row
				// shadow creation forever (Phase 109).
				if derr := h.Status.DeleteSandboxRow(ctx, sandboxID); derr != nil {
					slog.ErrorContext(ctx, "webhook_stale_row_delete_failed",
						"sandbox_id", sandboxID, "error", derr.Error())
				}
				return h.coldPath(ctx, rule, env, prompt, "orphaned_row")
			}
			// Transient: log and still enqueue — the prompt drains when the box
			// comes back rather than being lost.
			slog.WarnContext(ctx, "webhook_resume_transient_error",
				"sandbox_id", sandboxID, "error", serr.Error())
		}
	}

	queueURL, qerr := h.Resolver.QueueURL(ctx, sandboxID)
	if qerr != nil {
		slog.ErrorContext(ctx, "webhook_queue_url_missing",
			"sandbox_id", sandboxID, "error", qerr.Error())
		return ok("queue_url_missing")
	}

	payload, merr := json.Marshal(QueueEnvelope{
		Source: source, Type: env.Type, ID: env.ID, Severity: env.Severity,
		Title: env.Title, URL: env.URL, Prompt: prompt, Raw: env.Raw,
	})
	if merr != nil {
		slog.ErrorContext(ctx, "webhook_marshal_failed", "error", merr.Error())
		return ok("marshal_failed")
	}

	// MessageGroupId = sandbox id => strictly serial per box.
	if serr := h.Queue.Send(ctx, queueURL, sandboxID, string(payload)); serr != nil {
		slog.ErrorContext(ctx, "webhook_enqueue_failed",
			"sandbox_id", sandboxID, "error", serr.Error())
		return ok("enqueue_failed")
	}

	slog.InfoContext(ctx, "webhook_dispatched",
		"source", source, "sandbox_id", sandboxID, "id", env.ID, "severity", env.Severity)
	return ok("dispatched")
}

func (h *Handler) coldPath(ctx context.Context, rule *config.WebhookRule,
	env *webhook.Envelope, prompt, reason string) Response {

	if strings.EqualFold(rule.OnAbsent, "skip") {
		slog.InfoContext(ctx, "webhook_skipped_absent", "alias", rule.Alias, "reason", reason)
		return ok("skipped_absent")
	}
	if err := h.Cold.ColdCreate(ctx, rule.Alias, rule.Profile, prompt); err != nil {
		slog.ErrorContext(ctx, "webhook_cold_create_failed",
			"alias", rule.Alias, "error", err.Error())
		return ok("cold_create_failed")
	}
	slog.InfoContext(ctx, "webhook_cold_created",
		"alias", rule.Alias, "profile", rule.Profile, "reason", reason, "id", env.ID)
	return ok("cold_created")
}

func isStopped(status string) bool {
	switch strings.ToLower(status) {
	case "stopped", "paused", "stopping":
		return true
	}
	return false
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./pkg/webhook/... -v; echo "EXIT=$?"`
Expected: PASS, `EXIT=0`.

- [ ] **Step 6: Commit**

```bash
git add pkg/webhook/bridge/
git commit -m "feat(webhook): dispatch handler with warm/resume/cold paths and self-heal"
```

---

### Task 7: SQS queue helpers and per-sandbox lifecycle

**Files:**
- Modify: `pkg/aws/sqs.go`
- Create: `internal/app/cmd/create_webhook_inbound.go`
- Create: `internal/app/cmd/destroy_webhook_inbound.go`
- Modify: `pkg/profile/types.go` (add `NotificationWebhookSpec` / `NotificationWebhookInboundSpec`, wire into `NotificationSpec`)
- Modify: the SandboxProfile JSON schema (search: `grep -rl '"inbound"' --include=*.json .`)
- Modify: `pkg/aws/` SandboxMetadata struct + marshal/unmarshal chokepoint (search: `grep -rn "github_inbound_queue_url" pkg/aws/`)
- Test: `pkg/aws/sqs_webhook_test.go`, `internal/app/cmd/create_webhook_inbound_test.go`, `pkg/profile/webhook_notification_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `awspkg.WebhookInboundQueueName(prefix, sandboxID) string` (format `{prefix}-webhook-inbound-{id}.fifo`), `awspkg.WebhookInboundDLQName(prefix) string`, `awspkg.CreateWebhookInboundQueue(ctx, c, queueName, dlqARN) (string, error)`, `awspkg.DeleteWebhookInboundQueue(ctx, c, queueURL) error`, `profile.NotificationWebhookInboundSpec{Enabled *bool}`, DDB attribute `webhook_inbound_queue_url`, SSM suffix `webhook-inbound-queue-url`.

- [ ] **Step 1: Write the failing tests**

```go
package aws

import "testing"

func TestWebhookInboundQueueName(t *testing.T) {
	got := WebhookInboundQueueName("km", "sb-abc123")
	want := "km-webhook-inbound-sb-abc123.fifo"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWebhookInboundDLQName(t *testing.T) {
	got := WebhookInboundDLQName("km")
	want := "km-webhook-inbound-dlq.fifo"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Visibility MUST be the long (1800s) H1 value, not the 30s Slack/GitHub value:
// a triage turn routinely outlives 30s, and a redelivered in-flight message
// would run the same triage twice.
func TestWebhookInboundQueueAttrs_LongVisibility(t *testing.T) {
	attrs, err := webhookInboundQueueAttrs("")
	if err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs["VisibilityTimeout"] != h1InboundVisibilityTimeout {
		t.Errorf("VisibilityTimeout: got %q, want %q",
			attrs["VisibilityTimeout"], h1InboundVisibilityTimeout)
	}
	if attrs["FifoQueue"] != "true" {
		t.Error("must be a FIFO queue")
	}
	if _, ok := attrs["RedrivePolicy"]; ok {
		t.Error("empty dlqARN must not attach a RedrivePolicy (dormancy)")
	}
}

func TestWebhookInboundQueueAttrs_RedrivePolicy(t *testing.T) {
	attrs, err := webhookInboundQueueAttrs("arn:aws:sqs:us-east-1:1:km-webhook-inbound-dlq.fifo")
	if err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs["RedrivePolicy"] == "" {
		t.Error("non-empty dlqARN must attach a RedrivePolicy")
	}
}
```

And the profile round-trip test in `pkg/profile/webhook_notification_test.go`:

```go
package profile

import "testing"

func TestNotificationWebhookInbound_RoundTrip(t *testing.T) {
	raw := []byte("apiVersion: klankermaker.ai/v1alpha2\nkind: SandboxProfile\n" +
		"metadata:\n  name: t\nspec:\n  notification:\n    webhook:\n      inbound:\n        enabled: true\n")
	p, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Spec.Notification == nil || p.Spec.Notification.Webhook == nil ||
		p.Spec.Notification.Webhook.Inbound == nil {
		t.Fatal("notification.webhook.inbound missing after parse")
	}
	if e := p.Spec.Notification.Webhook.Inbound.Enabled; e == nil || !*e {
		t.Errorf("Enabled: got %v, want true", e)
	}
}

func TestNotificationWebhookInbound_AbsentIsNil(t *testing.T) {
	raw := []byte("apiVersion: klankermaker.ai/v1alpha2\nkind: SandboxProfile\n" +
		"metadata:\n  name: t\nspec:\n  notification: {}\n")
	p, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Spec.Notification != nil && p.Spec.Notification.Webhook != nil {
		t.Error("absent webhook block must stay nil (dormant)")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pkg/aws/ -run TestWebhookInbound -v; go test ./pkg/profile/ -run TestNotificationWebhookInbound -v`
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Add the SQS helpers**

Append to `pkg/aws/sqs.go`:

```go
// WebhookInboundDLQName returns the shared per-install webhook-inbound DLQ name.
func WebhookInboundDLQName(resourcePrefix string) string {
	return fmt.Sprintf("%s-webhook-inbound-dlq.fifo", resourcePrefix)
}

// WebhookInboundQueueName returns the per-sandbox webhook inbound FIFO name.
// Format: {resource_prefix}-webhook-inbound-{sandbox-id}.fifo
func WebhookInboundQueueName(resourcePrefix, sandboxID string) string {
	return fmt.Sprintf("%s-webhook-inbound-%s.fifo", resourcePrefix, sandboxID)
}

// webhookInboundQueueAttrs mirrors h1InboundQueueAttrs — VisibilityTimeout is the
// LONG (1800s) value, not the 30s Slack/GitHub one. A triage turn routinely runs
// past 30s, and a redelivered in-flight message would run the same triage twice.
func webhookInboundQueueAttrs(dlqARN string) (map[string]string, error) {
	attrs := map[string]string{
		string(sqstypes.QueueAttributeNameFifoQueue):                 "true",
		string(sqstypes.QueueAttributeNameContentBasedDeduplication): "false",
		string(sqstypes.QueueAttributeNameVisibilityTimeout):         h1InboundVisibilityTimeout,
		string(sqstypes.QueueAttributeNameMessageRetentionPeriod):    "1209600",
	}
	if dlqARN != "" {
		rp, err := redrivePolicyJSON(dlqARN)
		if err != nil {
			return nil, err
		}
		attrs["RedrivePolicy"] = rp
	}
	return attrs, nil
}

// CreateWebhookInboundQueue creates a per-sandbox webhook inbound FIFO queue.
// Idempotent: QueueNameExists resolves to the existing URL.
func CreateWebhookInboundQueue(ctx context.Context, c SQSClient, queueName, dlqARN string) (string, error) {
	attrs, err := webhookInboundQueueAttrs(dlqARN)
	if err != nil {
		return "", fmt.Errorf("create webhook queue %s: %w", queueName, err)
	}
	return createFifoQueue(ctx, c, queueName, attrs)
}

// DeleteWebhookInboundQueue deletes a per-sandbox webhook inbound FIFO queue.
func DeleteWebhookInboundQueue(ctx context.Context, c SQSClient, queueURL string) error {
	return deleteQueue(ctx, c, queueURL)
}
```

If `createFifoQueue` / `deleteQueue` helpers do not already exist, copy the bodies of `CreateH1InboundQueue` and `DeleteH1InboundQueue` verbatim, substituting `webhookInboundQueueAttrs` and the "webhook" label in error strings.

- [ ] **Step 4: Add the profile schema field**

In `pkg/profile/types.go`, next to `NotificationGitHubSpec`:

```go
// NotificationWebhookSpec configures the per-sandbox generic webhook inbound queue.
type NotificationWebhookSpec struct {
	Inbound *NotificationWebhookInboundSpec `json:"inbound,omitempty" yaml:"inbound,omitempty"`
}

// NotificationWebhookInboundSpec gates provisioning of the per-sandbox
// webhook-inbound FIFO queue at km create. Mirrors NotificationGitHubInboundSpec.
// nil = default false (dormant — zero SQS/DDB/SSM artifacts).
type NotificationWebhookInboundSpec struct {
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}
```

Add to `NotificationSpec`:

```go
	// Webhook configures the generic webhook inbound queue (Phase 127).
	Webhook *NotificationWebhookSpec `json:"webhook,omitempty" yaml:"webhook,omitempty"`
```

Add the matching `webhook` property to the SandboxProfile JSON schema under `notification` (the schema uses `additionalProperties: false`, so omitting this makes every profile using the field fail validation).

- [ ] **Step 5: Add the queue lifecycle and the DDB attribute**

Create `internal/app/cmd/create_webhook_inbound.go` and `destroy_webhook_inbound.go` by mirroring `create_github_inbound.go` and `destroy_github_inbound.go`, substituting throughout:

| GitHub | Webhook |
|---|---|
| `githubInboundDeps` | `webhookInboundDeps` |
| `notificationGitHubInbound` | `notificationWebhookInbound` |
| `awspkg.GitHubInboundQueueName` | `awspkg.WebhookInboundQueueName` |
| `awspkg.CreateGitHubInboundQueue` | `awspkg.CreateWebhookInboundQueue` |
| `awspkg.GitHubInboundDLQName` | `awspkg.WebhookInboundDLQName` |
| `"github-inbound-queue-url"` | `"webhook-inbound-queue-url"` |
| `"github_inbound_queue_url"` | `"webhook_inbound_queue_url"` |

Then call `provisionWebhookInboundQueue` from `create.go` at the same site as the GitHub block, gated on `notification.webhook.inbound.enabled`.

**Critical:** add `webhook_inbound_queue_url` to the `SandboxMetadata` struct **and** to both sides of the marshal/unmarshal chokepoint. Without this, `km pause` / `resume` / `extend` strip the attribute and the poller loses its queue URL after the first resume (`project_sandboxmetadata_lossy_roundtrip`).

- [ ] **Step 6: Write the round-trip regression test**

```go
package aws

import "testing"

// The lossy-round-trip footgun: a new per-sandbox attribute must survive
// marshal -> unmarshal or km pause/resume/extend silently drops it.
func TestSandboxMetadata_WebhookQueueURLRoundTrip(t *testing.T) {
	in := SandboxMetadata{
		SandboxID:               "sb-1",
		WebhookInboundQueueURL:  "https://sqs.us-east-1.amazonaws.com/1/km-webhook-inbound-sb-1.fifo",
	}
	item, err := MarshalSandboxMetadata(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := UnmarshalSandboxMetadata(item)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.WebhookInboundQueueURL != in.WebhookInboundQueueURL {
		t.Errorf("dropped in round-trip: got %q, want %q",
			out.WebhookInboundQueueURL, in.WebhookInboundQueueURL)
	}
}
```

Adjust the marshal/unmarshal function names to whatever the chokepoint actually exports (find with `grep -rn "func.*SandboxMetadata" pkg/aws/`).

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./pkg/aws/ ./pkg/profile/ ./internal/app/cmd/ -run 'Webhook' -v; echo "EXIT=$?"`
Expected: PASS, `EXIT=0`.

- [ ] **Step 8: Commit**

```bash
git add pkg/aws/ pkg/profile/ internal/app/cmd/create_webhook_inbound.go internal/app/cmd/destroy_webhook_inbound.go
git commit -m "feat(webhook): per-sandbox inbound FIFO lifecycle and profile field"
```

---

### Task 8: Sandbox-side poller

**Files:**
- Modify: `pkg/compiler/userdata.go` (new poller heredoc + env emission + systemd unit)
- Create: `profiles/prompts/wiz.triage.prompt.txt`
- Test: `pkg/compiler/userdata_webhook_inbound_test.go`

**Interfaces:**
- Consumes: the `QueueEnvelope` JSON shape from Task 6 (`source`, `type`, `id`, `severity`, `title`, `url`, `prompt`, `raw`).
- Produces: `/opt/km/bin/km-webhook-inbound-poller` on the box; env var `KM_WEBHOOK_INBOUND_QUEUE_URL`; SSM fallback `{ssmPrefix}sandbox/${SANDBOX_ID}/webhook-inbound-queue-url`.

- [ ] **Step 1: Write the failing test**

```go
package compiler

import (
	"strings"
	"testing"
)

func TestUserData_WebhookPollerEmittedWhenEnabled(t *testing.T) {
	p := profileWithWebhookInbound(t, true)
	out, err := generateUserData(userDataParamsFor(t, p))
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	if !strings.Contains(out, "km-webhook-inbound-poller") {
		t.Fatal("poller must be installed when webhook.inbound.enabled")
	}
	if !strings.Contains(out, "webhook-inbound-queue-url") {
		t.Error("poller must carry the SSM fallback path")
	}
	if !strings.Contains(out, "KM_WEBHOOK_INBOUND_QUEUE_URL") {
		t.Error("env file must export KM_WEBHOOK_INBOUND_QUEUE_URL")
	}
	if !strings.Contains(out, "[Webhook Trigger]") {
		t.Error("poller must render the source-aware preamble")
	}
}

func TestUserData_WebhookPollerAbsentWhenDisabled(t *testing.T) {
	p := profileWithWebhookInbound(t, false)
	out, err := generateUserData(userDataParamsFor(t, p))
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	if strings.Contains(out, "km-webhook-inbound-poller") {
		t.Fatal("disabled webhook inbound must emit no poller (dormancy)")
	}
	if strings.Contains(out, "KM_WEBHOOK_INBOUND_QUEUE_URL") {
		t.Error("disabled webhook inbound must not export the queue URL")
	}
}
```

Write `profileWithWebhookInbound` and `userDataParamsFor` by copying the equivalent helpers from `pkg/compiler/userdata_github_inbound_test.go`, changing the notification block to `webhook`.

**Note:** the profile fixture MUST include a non-nil `spec.cli:` block. Poller and `notify.env` emission is gated on `Spec.CLI != nil`; a fixture without it will make this test fail for a reason unrelated to the feature (`project_notify_setup_gated_on_spec_cli`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/compiler/ -run TestUserData_WebhookPoller -v`
Expected: FAIL — no poller in output.

- [ ] **Step 3: Add the poller to userdata**

In `pkg/compiler/userdata.go`, after the `km-h1-inbound-poller` block, add a
`{{ if .WebhookInboundEnabled }}`-gated section that writes
`/opt/km/bin/km-webhook-inbound-poller`, `chmod +x` it, and installs a systemd
unit named `km-webhook-inbound-poller.service` — mirroring the h1 poller's unit
exactly. The poller script:

```sh
#!/bin/bash
# km-webhook-inbound-poller: drain the per-sandbox webhook-inbound FIFO and run
# one agent turn per envelope. One-way source: there is no reply-back leg.
set -uo pipefail

. /etc/km/notify.env 2>/dev/null || true
AGENT="${KM_AGENT:-claude}"
command -v "$AGENT" >/dev/null 2>&1 || \
  echo "[km-webhook-inbound-poller] WARNING: agent '$AGENT' not on PATH — dispatched turns will NOT run" >&2

[ -z "${KM_SANDBOX_ID:-}" ] && echo "[km-webhook-inbound-poller] KM_SANDBOX_ID not set, exiting" && exit 0

QUEUE_URL="${KM_WEBHOOK_INBOUND_QUEUE_URL:-}"
PARAM_NAME="{{ .SsmPrefix }}sandbox/${KM_SANDBOX_ID}/webhook-inbound-queue-url"
if [ -z "$QUEUE_URL" ]; then
  echo "[km-webhook-inbound-poller] KM_WEBHOOK_INBOUND_QUEUE_URL empty, reading $PARAM_NAME from SSM"
  attempt=1
  while [ $attempt -le 10 ]; do
    QUEUE_URL=$(aws ssm get-parameter --name "$PARAM_NAME" --region "$REGION" \
      --query 'Parameter.Value' --output text 2>/dev/null) && [ -n "$QUEUE_URL" ] && break
    echo "[km-webhook-inbound-poller] $PARAM_NAME not yet available (attempt $attempt/10), sleeping 30s"
    sleep 30
    attempt=$((attempt + 1))
  done
fi
[ -z "$QUEUE_URL" ] && echo "[km-webhook-inbound-poller] queue URL unavailable after retries, exiting" && exit 0

echo "[km-webhook-inbound-poller] Starting — queue=$QUEUE_URL region=$REGION"

while true; do
  MSG=$(aws sqs receive-message --queue-url "$QUEUE_URL" --region "$REGION" \
    --wait-time-seconds 20 --max-number-of-messages 1 --output json 2>/dev/null)
  [ -z "$MSG" ] && continue

  BODY=$(echo "$MSG" | jq -r '.Messages[0].Body // empty')
  RECEIPT=$(echo "$MSG" | jq -r '.Messages[0].ReceiptHandle // empty')
  [ -z "$BODY" ] && continue

  SOURCE=$(echo "$BODY" | jq -r '.source // "unknown"')
  WTYPE=$(echo "$BODY" | jq -r '.type // "event"')
  PROMPT=$(echo "$BODY" | jq -r '.prompt // empty')
  if [ -z "$PROMPT" ]; then
    echo "[km-webhook-inbound-poller] WARN: envelope has no prompt, acking: $BODY"
    aws sqs delete-message --queue-url "$QUEUE_URL" --region "$REGION" \
      --receipt-handle "$RECEIPT" >/dev/null 2>&1
    continue
  fi

  RUN_ID=$(date -u +%Y%m%dT%H%M%SZ)
  TURN="[Webhook Trigger] ${SOURCE} / ${WTYPE}

${PROMPT}"

  echo "[km-webhook-inbound-poller] Dispatching turn — source=$SOURCE type=$WTYPE run=$RUN_ID"
  sudo -u sandbox -i env KM_WEBHOOK_RUN_ID="$RUN_ID" \
    km-agent-run "$TURN" >/dev/null 2>&1
  RUN_EXIT=$?

  if [ $RUN_EXIT -eq 0 ]; then
    # Ack AFTER completion so a crash redelivers the turn rather than losing it.
    aws sqs delete-message --queue-url "$QUEUE_URL" --region "$REGION" \
      --receipt-handle "$RECEIPT" >/dev/null 2>&1
    echo "[km-webhook-inbound-poller] Turn complete — source=$SOURCE run=$RUN_ID"
  else
    echo "[km-webhook-inbound-poller] WARN: agent run failed (exit $RUN_EXIT), message returns to queue"
  fi
done
```

Replace `km-agent-run "$TURN"` with whatever the h1 poller uses to invoke the agent — copy that invocation verbatim, including its `--resume`-less first-turn form, since a webhook turn is always fresh (no thread continuity).

Emit `KM_WEBHOOK_INBOUND_QUEUE_URL` into `/etc/km/notify.env` only when the queue is enabled, mirroring the `KM_GITHUB_INBOUND_QUEUE_URL` emission.

- [ ] **Step 4: Add the Wiz prompt file**

Create `profiles/prompts/wiz.triage.prompt.txt`:

```
A Wiz {{type}} fired: {{title}}
Severity: {{severity}}   Status: {{status}}
Entity: {{entity.name}} ({{entity.type}}, {{entity.cloud_platform}})
{{url}}

Run the /triage skill with this payload in mind.

Raw payload:
{{raw}}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./pkg/compiler/ -run TestUserData_WebhookPoller -v; echo "EXIT=$?"`
Expected: PASS, `EXIT=0`.

- [ ] **Step 6: Regenerate goldens and confirm dormant-path byte-identity**

Run: `go test ./pkg/compiler/ -v; echo "EXIT=$?"`
Expected: PASS. Any golden diff on a profile WITHOUT `webhook.inbound.enabled` is a dormancy bug — fix the gating rather than re-recording the golden. Do **not** re-capture `userdata_learn_v2_pre92_baseline.golden.sh` (`project_frozen_byte_identity_golden_capture_trap`).

- [ ] **Step 7: Commit**

```bash
git add pkg/compiler/ profiles/prompts/wiz.triage.prompt.txt
git commit -m "feat(webhook): sandbox-side km-webhook-inbound-poller"
```

---

### Task 9: Lambda entrypoint and AWS adapters

**Files:**
- Create: `cmd/km-webhook-bridge/main.go`
- Create: `pkg/webhook/bridge/aws_adapters.go`
- Test: `cmd/km-webhook-bridge/main_test.go`

**Interfaces:**
- Consumes: `bridge.Handler` (Task 6), `awspkg` helpers (Task 7).
- Produces: Lambda entrypoint reading `KM_WEBHOOK_SOURCES`, `KM_WEBHOOK_RATE_LIMIT`, `KM_NONCE_TABLE`, `KM_SANDBOX_TABLE_NAME`, `KM_ARTIFACTS_BUCKET`, `KM_ARTIFACTS_PREFIX`, `KM_RESOURCE_PREFIX`, `KM_QUOTA_TABLE`; plus `WireActionQuota(h *bridge.Handler, ddb *dynamodb.Client, sandboxesTable string) bool`.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestParseSourcesEnv(t *testing.T) {
	raw := `{"sources":[{"name":"wiz","auth":{"type":"bearer","secret_path":"/p"},
"rules":[{"alias":"ir-bot","prompt":"go"}]}],"rate_limit":{"max_dispatches":5,"window_seconds":60}}`
	cfg, err := parseSourcesEnv(raw)
	if err != nil {
		t.Fatalf("parseSourcesEnv: %v", err)
	}
	if len(cfg.Sources) != 1 || cfg.Sources[0].Name != "wiz" {
		t.Fatalf("sources: %+v", cfg.Sources)
	}
	if cfg.RateLimit == nil || cfg.RateLimit.MaxDispatches != 5 {
		t.Errorf("rate_limit: %+v", cfg.RateLimit)
	}
}

// An absent env var must leave the bridge dormant, not crash the cold start.
func TestParseSourcesEnv_EmptyIsDormant(t *testing.T) {
	cfg, err := parseSourcesEnv("")
	if err != nil {
		t.Fatalf("empty env must not error: %v", err)
	}
	if len(cfg.Sources) != 0 {
		t.Errorf("sources: got %d, want 0", len(cfg.Sources))
	}
}

// Malformed JSON must warn and stay dormant rather than panic the Lambda.
func TestParseSourcesEnv_MalformedIsDormant(t *testing.T) {
	cfg, err := parseSourcesEnv(`{not json`)
	if err == nil {
		t.Fatal("malformed JSON should report an error to the caller")
	}
	if len(cfg.Sources) != 0 {
		t.Errorf("sources: got %d, want 0", len(cfg.Sources))
	}
}

func TestLowercaseHeaders(t *testing.T) {
	got := lowercaseHeaders(map[string]string{"Authorization": "Bearer x", "X-Foo": "y"})
	if got["authorization"] != "Bearer x" || got["x-foo"] != "y" {
		t.Errorf("got %+v", got)
	}
}

func TestEnvOr(t *testing.T) {
	os.Setenv("KM_TEST_X", "set") //nolint:errcheck
	defer os.Unsetenv("KM_TEST_X")
	if envOr("KM_TEST_X", "fallback") != "set" {
		t.Error("set var must win")
	}
	if envOr("KM_TEST_UNSET", "fallback") != "fallback" {
		t.Error("unset var must fall back")
	}
}

var _ = json.Marshal
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/km-webhook-bridge/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the entrypoint**

Create `cmd/km-webhook-bridge/main.go` modelled on `cmd/km-h1-bridge/main.go`. Key
differences: no threads table, no API back-channel, no bot handle. Core pieces:

```go
// Command km-webhook-bridge is the generic webhook ingress Lambda.
//
// It receives a POST on a Lambda Function URL, resolves the source from the URL
// PATH segment (POST /wiz), authenticates via the source's configured scheme,
// drops replays, matches operator-declared rules, and dispatches a prompt to a
// sandbox alias — warm via the per-sandbox webhook-inbound FIFO, cold via an
// EventBridge SandboxCreate.
//
// Returns 200 on EVERY path including internal errors, so a sender never
// redelivers with a fresh id that would bypass dedup.
//
// Environment variables:
//
//	KM_RESOURCE_PREFIX     — resource_prefix (default "km")
//	KM_WEBHOOK_SOURCES     — JSON {sources:[...], rate_limit:{...}}; absent ⇒ dormant
//	KM_NONCE_TABLE         — shared nonces table (default {prefix}-slack-bridge-nonces)
//	KM_SANDBOX_TABLE_NAME  — {prefix}-sandboxes
//	KM_ARTIFACTS_BUCKET    — S3 artifacts bucket for the cold-create event
//	KM_ARTIFACTS_PREFIX    — S3 artifacts prefix
//	KM_QUOTA_TABLE         — Phase 121 action-quota table; absent ⇒ quota unwired
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/webhook/bridge"
)

var handler *bridge.Handler

// sourcesEnv is the shape of KM_WEBHOOK_SOURCES.
type sourcesEnv struct {
	Sources   []config.WebhookSource   `json:"sources"`
	RateLimit *config.WebhookRateLimit `json:"rate_limit"`
}

func parseSourcesEnv(raw string) (sourcesEnv, error) {
	var cfg sourcesEnv
	if strings.TrimSpace(raw) == "" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return sourcesEnv{}, err
	}
	return cfg, nil
}

func lowercaseHeaders(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[strings.ToLower(k)] = v
	}
	return out
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func handleRequest(ctx context.Context, req events.LambdaFunctionURLRequest) (
	events.LambdaFunctionURLResponse, error) {

	body := []byte(req.Body)
	if req.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(req.Body)
		if err == nil {
			body = decoded
		}
	}
	hdrs := lowercaseHeaders(req.Headers)

	resp := handler.Handle(ctx, bridge.Request{
		Path:            req.RawPath,
		ContentEncoding: hdrs["content-encoding"],
		Headers:         hdrs,
		Body:            body,
	})

	return events.LambdaFunctionURLResponse{
		StatusCode: resp.Status,
		Body:       `{"ok":true,"result":"` + resp.Log + `"}`,
		Headers:    map[string]string{"Content-Type": "application/json"},
	}, nil
}

func main() { lambda.Start(handleRequest) }
```

Add an `init()` that constructs the adapters (SSM secret fetcher with a cold-start
cache, DynamoDB alias resolver against the `alias-index` GSI, SQS sender, EC2
resumer, DynamoDB status writer, EventBridge cold-creator, nonce store, rate
counter) and calls `WireActionQuota` gated on `KM_QUOTA_TABLE`, exactly as
`cmd/km-h1-bridge/main.go:230` does.

- [ ] **Step 4: Write the AWS adapters**

Create `pkg/webhook/bridge/aws_adapters.go`. Copy `DynamoAliasResolver`,
`DynamoGitHubNonceStore`, the SQS sender, the EC2 resumer, the status writer, and
the EventBridge publisher from `pkg/github/bridge/aws_adapters.go`, renaming for
this package and substituting the `webhook_inbound_queue_url` attribute. The one
genuinely new adapter is the rate counter:

```go
// DynamoRateCounter implements webhook.RateCounter with an atomic ADD on the
// shared nonces table. The bucket row carries a TTL so it self-reaps.
type DynamoRateCounter struct {
	Client    DynamoUpdateItemClient
	TableName string
}

func (c *DynamoRateCounter) Increment(ctx context.Context, key string, ttlSeconds int) (int64, error) {
	expiry := time.Now().Unix() + int64(ttlSeconds)
	out, err := c.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(c.TableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"nonce": &dynamodbtypes.AttributeValueMemberS{Value: key},
		},
		UpdateExpression: awssdk.String("ADD hit_count :one SET ttl_expiry = if_not_exists(ttl_expiry, :exp)"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":one": &dynamodbtypes.AttributeValueMemberN{Value: "1"},
			":exp": &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(expiry, 10)},
		},
		ReturnValues: dynamodbtypes.ReturnValueUpdatedNew,
	})
	if err != nil {
		return 0, fmt.Errorf("webhook-bridge: rate counter: %w", err)
	}
	n, ok := out.Attributes["hit_count"].(*dynamodbtypes.AttributeValueMemberN)
	if !ok {
		return 0, fmt.Errorf("webhook-bridge: rate counter: missing hit_count")
	}
	return strconv.ParseInt(n.Value, 10, 64)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./cmd/km-webhook-bridge/ ./pkg/webhook/... -v; echo "EXIT=$?"`
Expected: PASS, `EXIT=0`.

- [ ] **Step 6: Commit**

```bash
git add cmd/km-webhook-bridge/ pkg/webhook/bridge/aws_adapters.go
git commit -m "feat(webhook): Lambda entrypoint and AWS adapters"
```

---

### Task 10: Terraform module, live unit, and init registration

**Files:**
- Create: `infra/modules/lambda-webhook-bridge/v1.0.0/{main.tf,variables.tf,outputs.tf}`
- Create: `infra/live/use1/lambda-webhook-bridge/terragrunt.hcl`
- Modify: `internal/app/cmd/init.go` (`regionalModules()` ~line 525; `buildLambdaZips` list ~line 3165; scoped-init alias list ~line 133; destroy-class gate ~line 506)
- Test: `internal/app/cmd/init_webhook_test.go`; update the count in `TestRunInitPlan_ModuleOrder`

**Interfaces:**
- Consumes: `cmd/km-webhook-bridge` (Task 9).
- Produces: module `lambda-webhook-bridge`, sugar flag `km init --webhooks`, Function URL output recorded at SSM `{prefix}config/webhooks/bridge-url`.

- [ ] **Step 1: Write the failing test**

```go
package cmd

import "testing"

// Four registration points each fail SILENTLY if missed. This test pins all of
// them so a future refactor cannot quietly un-deploy the bridge.
func TestWebhookBridge_RegisteredEverywhere(t *testing.T) {
	t.Run("regionalModules includes lambda-webhook-bridge", func(t *testing.T) {
		mods := regionalModules("infra/live/use1")
		for _, m := range mods {
			if m.name == "lambda-webhook-bridge" {
				return
			}
		}
		t.Fatal("lambda-webhook-bridge missing from regionalModules(): the module " +
			"would never be applied and km doctor would stay green")
	})

	t.Run("lambda zip build list includes km-webhook-bridge", func(t *testing.T) {
		if !lambdaZipBuildListContains("km-webhook-bridge") {
			t.Fatal("km-webhook-bridge missing from buildLambdaZips: the zip is " +
				"never built and the deploy ships stale or absent code")
		}
	})

	t.Run("--webhooks resolves to the module", func(t *testing.T) {
		if got := resolveScopedInitAlias("webhooks"); got != "lambda-webhook-bridge" {
			t.Fatalf("resolveScopedInitAlias(webhooks): got %q", got)
		}
	})
}
```

Add whatever tiny test seam is needed (`lambdaZipBuildListContains`,
`resolveScopedInitAlias`) if the existing code does not already expose one — find
the current shape with `grep -n "func resolveOnlyAlias\|candidate = " internal/app/cmd/init.go`
and reuse the real function names rather than inventing new ones.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/cmd/ -run TestWebhookBridge_RegisteredEverywhere -v`
Expected: FAIL on all three subtests.

- [ ] **Step 3: Create the Terraform module**

Copy `infra/modules/lambda-h1-bridge/v1.0.0/` to
`infra/modules/lambda-webhook-bridge/v1.0.0/` and then:

- **Drop** `h1_threads_table_name`, `h1_threads_table_arn`, `api_username_path`,
  `api_token_path`, `h1_api_base_url`, `h1_bot_handle`, `commands_path`, and the
  DynamoDB IAM statements for the threads table.
- **Rename** `h1_programs_json` → `webhook_sources_json`, `h1_default_profile` →
  removed (rules carry their own profile).
- **Add** `webhook_rate_limit_json`.
- **Keep** the Function URL (`auth_type = "NONE"` — our auth is in-Lambda), the
  nonces-table IAM (`GetItem`/`PutItem`/`UpdateItem`), the sandboxes-table IAM
  (`Query` on `alias-index`, `UpdateItem`, `DeleteItem` — the Phase 109 self-heal
  needs `DeleteItem`, still never `PutItem`), `sqs:SendMessage`,
  `ec2:DescribeInstances`/`StartInstances`, `events:PutEvents`,
  `ssm:GetParameter` + `kms:Decrypt` for the SecureString, and the quota-table
  grant.
- **Declare no `required_providers`** — they live in `root.hcl`.

Create `infra/live/use1/lambda-webhook-bridge/terragrunt.hcl` mirroring the h1
unit, with `get_env("KM_WEBHOOK_SOURCES", "")` and
`get_env("KM_WEBHOOK_RATE_LIMIT", "")`, and including `"show"` in
`mock_outputs_allowed_terraform_commands` on every `dependency` block
(`project_terragrunt_show_needs_mocks`).

- [ ] **Step 4: Register in init.go**

Add the `regionalModule` entry (after `lambda-h1-bridge`), the
`{name: "km-webhook-bridge", srcDir: "cmd/km-webhook-bridge"}` zip entry, the
`"webhooks"` → `lambda-webhook-bridge` sugar alias, and `lambda-webhook-bridge`
to the destroy-class gate `case` list at line ~506.

- [ ] **Step 5: Bump the module-order count**

`TestRunInitPlan_ModuleOrder` hardcodes the `regionalModules()` length
(`project_module_order_test_count_debt`). Run it, read the expected-vs-actual, and
bump the constant by one.

Run: `go test ./internal/app/cmd/ -run TestRunInitPlan_ModuleOrder -v`

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/app/cmd/ -run 'TestWebhookBridge|TestRunInitPlan_ModuleOrder' -v; echo "EXIT=$?"`
Expected: PASS, `EXIT=0`.

- [ ] **Step 7: Commit**

```bash
git add infra/modules/lambda-webhook-bridge/ infra/live/use1/lambda-webhook-bridge/ internal/app/cmd/init.go internal/app/cmd/init_webhook_test.go
git commit -m "feat(webhook): terraform module, live unit, and init registration"
```

---

### Task 11: Env export and one-time token minting

**Files:**
- Modify: `internal/app/cmd/init.go` (`ExportTerragruntEnvVars` at line 1537; add a mint step in `runInit`)
- Test: `internal/app/cmd/init_webhook_export_test.go`

**Interfaces:**
- Consumes: `config.WebhooksConfig` (Task 1).
- Produces: env vars `KM_WEBHOOK_SOURCES`, `KM_WEBHOOK_RATE_LIMIT`; function `mintWebhookSecretIfAbsent(ctx, ssm SSMPutClient, path string) (minted bool, token string, err error)`.

- [ ] **Step 1: Write the failing test**

```go
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

func TestExportTerragruntEnvVars_WebhookSources(t *testing.T) {
	os.Unsetenv("KM_WEBHOOK_SOURCES") //nolint:errcheck
	defer os.Unsetenv("KM_WEBHOOK_SOURCES")

	cfg := &config.Config{Webhooks: config.WebhooksConfig{
		RateLimit: &config.WebhookRateLimit{MaxDispatches: 20, WindowSeconds: 600},
		Sources: []config.WebhookSource{{
			Name: "wiz",
			Auth: config.WebhookAuth{Type: "bearer", SecretPath: "/km/config/webhooks/wiz/token"},
			Rules: []config.WebhookRule{{Alias: "ir-bot", Prompt: "go"}},
		}},
	}}

	ExportTerragruntEnvVars(cfg)

	raw := os.Getenv("KM_WEBHOOK_SOURCES")
	if raw == "" {
		t.Fatal("KM_WEBHOOK_SOURCES not exported")
	}
	var out struct {
		Sources []config.WebhookSource `json:"sources"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("exported value is not valid JSON: %v", err)
	}
	if len(out.Sources) != 1 || out.Sources[0].Name != "wiz" {
		t.Errorf("round-trip mismatch: %s", raw)
	}
}

// Dormancy: no sources => the env var stays unset so the module reads "".
func TestExportTerragruntEnvVars_WebhooksDormant(t *testing.T) {
	os.Unsetenv("KM_WEBHOOK_SOURCES") //nolint:errcheck
	ExportTerragruntEnvVars(&config.Config{})
	if os.Getenv("KM_WEBHOOK_SOURCES") != "" {
		t.Error("absent webhooks: must leave KM_WEBHOOK_SOURCES unset")
	}
}

var errNotFound = errors.New("parameter not found")

type fakeSSM struct {
	existing map[string]string
	puts     map[string]string
}

func (f *fakeSSM) GetParameterValue(_ context.Context, name string) (string, error) {
	v, ok := f.existing[name]
	if !ok {
		return "", errNotFound
	}
	return v, nil
}

func (f *fakeSSM) PutSecureString(_ context.Context, name, value string) error {
	if f.puts == nil {
		f.puts = map[string]string{}
	}
	f.puts[name] = value
	return nil
}

func TestMintWebhookSecretIfAbsent(t *testing.T) {
	t.Run("mints when absent", func(t *testing.T) {
		s := &fakeSSM{existing: map[string]string{}}
		minted, token, err := mintWebhookSecretIfAbsent(context.Background(), s, "/km/config/webhooks/wiz/token")
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if !minted || token == "" {
			t.Fatalf("expected a minted token, got minted=%v token=%q", minted, token)
		}
		if len(token) < 32 {
			t.Errorf("token too short: %d chars", len(token))
		}
		if s.puts["/km/config/webhooks/wiz/token"] != token {
			t.Error("token was not written to SSM")
		}
	})

	// Idempotent: re-running km init must NOT rotate a live token out from under
	// a configured Wiz integration.
	t.Run("never overwrites an existing token", func(t *testing.T) {
		s := &fakeSSM{existing: map[string]string{"/p": "already-set"}}
		minted, _, err := mintWebhookSecretIfAbsent(context.Background(), s, "/p")
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if minted {
			t.Fatal("must not mint over an existing parameter")
		}
		if len(s.puts) != 0 {
			t.Errorf("must not write: %v", s.puts)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/cmd/ -run 'TestExportTerragruntEnvVars_Webhook|TestMintWebhookSecret' -v`
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Add the export block**

In `ExportTerragruntEnvVars`, after the `KM_H1_PROGRAMS` block, mirroring its
env-wins-with-drift-WARN shape:

```go
	// Phase 127: KM_WEBHOOK_SOURCES — JSON-encoded webhook source routing.
	// Consumed by infra/live/use1/lambda-webhook-bridge/terragrunt.hcl
	// get_env("KM_WEBHOOK_SOURCES") and parsed by cmd/km-webhook-bridge.
	// Gate on len>0: an absent webhooks: block leaves the var unset (dormant).
	// env-block change => needs a full `km init --dry-run=false`, NOT --sidecars.
	if len(cfg.Webhooks.Sources) > 0 {
		type webhookExportPayload struct {
			Sources   []config.WebhookSource   `json:"sources"`
			RateLimit *config.WebhookRateLimit `json:"rate_limit,omitempty"`
		}
		payload := webhookExportPayload{
			Sources:   cfg.Webhooks.Sources,
			RateLimit: cfg.Webhooks.RateLimit,
		}
		if jsonBytes, err := json.Marshal(payload); err == nil {
			yamlWebhooks := string(jsonBytes)
			if envVal := os.Getenv("KM_WEBHOOK_SOURCES"); envVal != "" && envVal != yamlWebhooks {
				fmt.Fprintf(os.Stderr,
					"WARN: KM_WEBHOOK_SOURCES=%s (env) overrides km-config.yaml webhooks=%s\n",
					envVal, yamlWebhooks)
			} else if envVal == "" {
				os.Setenv("KM_WEBHOOK_SOURCES", yamlWebhooks) //nolint:errcheck
			}
		}
	}
```

- [ ] **Step 4: Add the token minting**

```go
// mintWebhookSecretIfAbsent writes a fresh 32-byte random token to the SSM
// SecureString at path when — and only when — the parameter does not yet exist.
//
// Idempotent by design: an existing parameter is NEVER overwritten, so
// re-running `km init` cannot rotate a token out from under a live Wiz
// integration. Deliberate rotation is an explicit
// `aws ssm put-parameter --overwrite` plus a Wiz-side paste.
//
// Returns minted=true and the token ONLY on first creation, so the caller can
// print it once for the operator to paste into the Wiz Authorization header.
func mintWebhookSecretIfAbsent(ctx context.Context, ssm SSMSecretClient, path string) (bool, string, error) {
	if _, err := ssm.GetParameterValue(ctx, path); err == nil {
		return false, "", nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return false, "", fmt.Errorf("mint webhook secret: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	if err := ssm.PutSecureString(ctx, path, token); err != nil {
		return false, "", fmt.Errorf("mint webhook secret: write %s: %w", path, err)
	}
	return true, token, nil
}
```

Call it from `runInit` for each configured source's `auth.secret_path`, printing
on `minted == true`:

```
  Minted webhook token for source "wiz".
  Paste this into the Wiz integration's Authorization header — it is shown ONCE:

      Bearer <token>

  Stored at SSM /km/config/webhooks/wiz/token
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/app/cmd/ -run 'Webhook' -v; echo "EXIT=$?"`
Expected: PASS, `EXIT=0`.

- [ ] **Step 6: Commit**

```bash
git add internal/app/cmd/init.go internal/app/cmd/init_webhook_export_test.go
git commit -m "feat(webhook): export KM_WEBHOOK_SOURCES and mint the bearer token once"
```

---

### Task 12: `km doctor` checks

**Files:**
- Create: `internal/app/cmd/doctor_webhooks.go`
- Modify: `internal/app/cmd/doctor.go` (register the new check group)
- Test: `internal/app/cmd/doctor_webhooks_test.go`

**Interfaces:**
- Consumes: `config.WebhooksConfig` (Task 1).
- Produces: `checkWebhookSources(cfg *config.Config) []checkResult` (match the existing `checkResult` shape — find it with `grep -n "type checkResult" internal/app/cmd/doctor.go`).

- [ ] **Step 1: Write the failing test**

```go
package cmd

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// Dormancy: no webhooks: block => zero checks, zero AWS calls.
func TestCheckWebhookSources_SkipsWhenDormant(t *testing.T) {
	if got := checkWebhookSources(&config.Config{}); len(got) != 0 {
		t.Fatalf("dormant install must emit no checks, got %d", len(got))
	}
}

func TestCheckWebhookSources_StructuralValidation(t *testing.T) {
	cases := []struct {
		name    string
		src     config.WebhookSource
		wantSub string
	}{
		{
			name:    "unknown auth type",
			src:     config.WebhookSource{Name: "wiz", Auth: config.WebhookAuth{Type: "magic", SecretPath: "/p"}, Rules: []config.WebhookRule{{Alias: "a", Prompt: "p"}}},
			wantSub: "auth.type",
		},
		{
			name:    "empty secret_path",
			src:     config.WebhookSource{Name: "wiz", Auth: config.WebhookAuth{Type: "bearer"}, Rules: []config.WebhookRule{{Alias: "a", Prompt: "p"}}},
			wantSub: "secret_path",
		},
		{
			name:    "no rules",
			src:     config.WebhookSource{Name: "wiz", Auth: config.WebhookAuth{Type: "bearer", SecretPath: "/p"}},
			wantSub: "no rules",
		},
		{
			name:    "name not URL-path-safe",
			src:     config.WebhookSource{Name: "wiz/prod", Auth: config.WebhookAuth{Type: "bearer", SecretPath: "/p"}, Rules: []config.WebhookRule{{Alias: "a", Prompt: "p"}}},
			wantSub: "path-safe",
		},
		{
			name:    "rule missing alias",
			src:     config.WebhookSource{Name: "wiz", Auth: config.WebhookAuth{Type: "bearer", SecretPath: "/p"}, Rules: []config.WebhookRule{{Prompt: "p"}}},
			wantSub: "alias",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &config.Config{Webhooks: config.WebhooksConfig{Sources: []config.WebhookSource{c.src}}}
			results := checkWebhookSources(cfg)

			var found bool
			for _, r := range results {
				if r.status != statusOK && strings.Contains(strings.ToLower(r.detail), strings.ToLower(c.wantSub)) {
					found = true
				}
			}
			if !found {
				t.Errorf("expected a non-OK result mentioning %q; got %+v", c.wantSub, results)
			}
		})
	}
}

func TestCheckWebhookSources_ValidConfigIsOK(t *testing.T) {
	cfg := &config.Config{Webhooks: config.WebhooksConfig{
		RateLimit: &config.WebhookRateLimit{MaxDispatches: 20, WindowSeconds: 600},
		Sources: []config.WebhookSource{{
			Name:  "wiz",
			Auth:  config.WebhookAuth{Type: "bearer", SecretPath: "/km/config/webhooks/wiz/token"},
			Rules: []config.WebhookRule{{Alias: "ir-bot", Prompt: "go", OnAbsent: "cold-create"}},
		}},
	}}
	for _, r := range checkWebhookSources(cfg) {
		if r.status != statusOK {
			t.Errorf("valid config produced %v: %s", r.status, r.detail)
		}
	}
}

func TestCheckWebhookSources_BadRateLimit(t *testing.T) {
	cfg := &config.Config{Webhooks: config.WebhooksConfig{
		RateLimit: &config.WebhookRateLimit{MaxDispatches: 5, WindowSeconds: 0},
		Sources: []config.WebhookSource{{
			Name:  "wiz",
			Auth:  config.WebhookAuth{Type: "bearer", SecretPath: "/p"},
			Rules: []config.WebhookRule{{Alias: "a", Prompt: "p"}},
		}},
	}}
	var found bool
	for _, r := range checkWebhookSources(cfg) {
		if r.status != statusOK && strings.Contains(strings.ToLower(r.detail), "window_seconds") {
			found = true
		}
	}
	if !found {
		t.Error("window_seconds: 0 must be flagged")
	}
}
```

Adjust `statusOK` / `r.status` / `r.detail` to the real field names in
`internal/app/cmd/doctor.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/cmd/ -run TestCheckWebhookSources -v`
Expected: FAIL — `checkWebhookSources` undefined.

- [ ] **Step 3: Write the implementation**

Create `internal/app/cmd/doctor_webhooks.go`:

```go
package cmd

import (
	"fmt"
	"strings"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// checkWebhookSources validates the webhooks: block structurally. Returns an
// EMPTY slice when no sources are configured — a dormant install must cost zero
// checks and zero AWS calls, matching the launch_accounts precedent.
//
// This is deliberately offline/structural. The SSM-parameter-exists and
// Function-URL-reachable probes belong with the other network-touching doctor
// checks so they honour --all-regions and the AWS-call budget.
func checkWebhookSources(cfg *config.Config) []checkResult {
	if cfg == nil || len(cfg.Webhooks.Sources) == 0 {
		return nil
	}

	var results []checkResult
	seen := map[string]bool{}

	for _, src := range cfg.Webhooks.Sources {
		label := fmt.Sprintf("webhook source %q", src.Name)

		switch {
		case src.Name == "":
			results = append(results, warnResult(label, "source has an empty name; it can never be routed"))
			continue
		case strings.ContainsAny(src.Name, "/?#% "):
			results = append(results, warnResult(label,
				"name is not URL-path-safe; it is the POST path segment, so it must contain no /, ?, #, %, or spaces"))
		}

		if seen[strings.ToLower(src.Name)] {
			results = append(results, warnResult(label, "duplicate source name; only the first is reachable"))
		}
		seen[strings.ToLower(src.Name)] = true

		switch strings.ToLower(src.Auth.Type) {
		case "bearer", "hmac":
		default:
			results = append(results, warnResult(label,
				fmt.Sprintf("unknown auth.type %q (want bearer or hmac); every request will fail closed", src.Auth.Type)))
		}

		if src.Auth.SecretPath == "" {
			results = append(results, warnResult(label,
				"auth.secret_path is empty; every request fails closed until it names an SSM SecureString"))
		}

		if len(src.Rules) == 0 {
			results = append(results, warnResult(label, "source has no rules; every payload is dropped"))
		}

		for i, r := range src.Rules {
			rl := fmt.Sprintf("%s rule[%d]", label, i)
			if r.Alias == "" {
				results = append(results, warnResult(rl, "rule has no alias; nothing can be dispatched"))
			}
			if r.Prompt == "" {
				results = append(results, warnResult(rl, "rule has no prompt; the agent turn would be empty"))
			}
			if !strings.EqualFold(r.OnAbsent, "skip") && r.Profile == "" {
				results = append(results, warnResult(rl,
					"on_absent is cold-create (the default) but no profile is set; an absent alias cannot be created"))
			}
			if r.CooldownSeconds > 0 && r.GroupBy == "" {
				results = append(results, warnResult(rl,
					"cooldown_seconds is set without group_by; suppression falls back to per-delivery keys and will rarely fire"))
			}
		}

		results = append(results, okResult(label, fmt.Sprintf("%d rule(s) configured", len(src.Rules))))
	}

	if rl := cfg.Webhooks.RateLimit; rl != nil {
		switch {
		case rl.MaxDispatches <= 0:
			results = append(results, warnResult("webhook rate_limit",
				"max_dispatches must be > 0; the ceiling is disabled as configured"))
		case rl.WindowSeconds <= 0:
			results = append(results, warnResult("webhook rate_limit",
				"window_seconds must be > 0; the ceiling is disabled as configured"))
		default:
			results = append(results, okResult("webhook rate_limit",
				fmt.Sprintf("%d dispatches per %ds", rl.MaxDispatches, rl.WindowSeconds)))
		}
	}

	return results
}
```

Add `okResult` / `warnResult` shims if the file's existing helpers differ — reuse
the real constructors from `doctor.go` rather than inventing parallel ones.
Register `checkWebhookSources` in the doctor's check list.

- [ ] **Step 4: Add the four AWS-touching probes**

The spec lists four checks that need live AWS calls, so they belong with the
other network-touching doctor checks (which honour `--all-regions` and the
AWS-call budget) rather than in the structural function above. Add
`checkWebhookSourcesAWS(ctx, cfg, ssmClient, sqsClient) []checkResult`, gated on
the same `len(cfg.Webhooks.Sources) == 0` early return so a dormant install still
makes **zero** AWS calls:

1. **Secret present** — `ssm:GetParameter` on each source's `auth.secret_path`.
   Missing ⇒ WARN naming the path and the fact that every request fails closed
   until it exists.
2. **Function URL recorded** — read SSM `{prefix}config/webhooks/bridge-url`
   (written by `km init`, mirroring `{prefix}config/github/bridge-url`). Absent
   ⇒ WARN that the operator has no URL to paste into the source's integration.
3. **Rule profiles exist** — for each rule with `on_absent != "skip"`, stat the
   `profile` path relative to the km-config.yaml directory. Missing ⇒ WARN that
   cold-create will fail when the alias is absent.
4. **DLQ depth** — `awspkg.QueueDepth` on `awspkg.WebhookInboundDLQName(prefix)`.
   Depth > 0 ⇒ WARN with the count; this is the early warning on a poison wedge
   (`project_inbound_poller_fifo_poison_wedge`). A missing DLQ is SKIP, not WARN
   — it simply has not been provisioned yet.

Mirror the existing AWS-check registration and error handling exactly; find the
pattern with `grep -n "func checkInboundDLQ" internal/app/cmd/doctor_inbound_dlq.go`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/app/cmd/ -run TestCheckWebhookSources -v; echo "EXIT=$?"`
Expected: PASS, `EXIT=0`.

- [ ] **Step 6: Commit**

```bash
git add internal/app/cmd/doctor_webhooks.go internal/app/cmd/doctor.go internal/app/cmd/doctor_webhooks_test.go
git commit -m "feat(webhook): km doctor structural checks, silent when dormant"
```

---

### Task 13: Wiz payload templates and operator documentation

**Files:**
- Create: `docs/webhook-templates/wiz.issue.v1.json`
- Create: `docs/webhook-templates/wiz.threat.v1.json`
- Create: `docs/webhook-ingress.md`
- Modify: `CLAUDE.md` (phase block + "Where to look" row)

**Interfaces:**
- Consumes: the envelope contract from Task 2 and the config surface from Task 1.
- Produces: operator-pasteable Wiz Automation Rule bodies.

- [ ] **Step 1: Write the Issue template**

Create `docs/webhook-templates/wiz.issue.v1.json`:

```json
{
  "km_schema": "v1",
  "source": "wiz",
  "delivery_key": "{{issue.id}}:{{triggerType}}:{{issue.updatedAt}}",
  "type": "issue",
  "id": "{{issue.id}}",
  "severity": "{{issue.severity}}",
  "status": "{{issue.status}}",
  "title": "{{issue.control.name}}",
  "trigger": { "type": "{{triggerType}}", "rule": "{{ruleName}}" },
  "entity": {
    "type": "{{issue.entitySnapshot.type}}",
    "name": "{{issue.entitySnapshot.name}}",
    "cloud_platform": "{{issue.entitySnapshot.cloudPlatform}}",
    "cloud_id": "{{issue.entitySnapshot.externalId}}"
  },
  "url": "https://{{wizDomain}}/issues#~(issue~'{{issue.id}})"
}
```

- [ ] **Step 2: Write the Threat template**

Create `docs/webhook-templates/wiz.threat.v1.json`:

```json
{
  "km_schema": "v1",
  "source": "wiz",
  "delivery_key": "{{issue.id}}:{{triggerType}}:{{issue.updatedAt}}",
  "type": "threat",
  "id": "{{issue.id}}",
  "severity": "{{issue.severity}}",
  "status": "{{issue.status}}",
  "title": "{{issue.enrichedMainDetection.rule.name}}",
  "trigger": { "type": "{{triggerType}}", "rule": "{{ruleName}}" },
  "entity": {
    "type": "{{issue.entitySnapshot.type}}",
    "name": "{{#issue.enrichedThreatResources}}{{name}} {{/issue.enrichedThreatResources}}{{^issue.enrichedThreatResources}}N/A{{/issue.enrichedThreatResources}}",
    "cloud_platform": "{{issue.entitySnapshot.cloudPlatform}}",
    "cloud_id": "{{issue.entitySnapshot.externalId}}"
  },
  "actors": "{{#issue.enrichedThreatActors}}{{name}}, {{/issue.enrichedThreatActors}}",
  "detections": "{{#issue.enrichedDetections}}{{rule.name}}, {{/issue.enrichedDetections}}",
  "url": "https://{{wizDomain}}/threats#~(issue~'{{issue.id}})"
}
```

- [ ] **Step 3: Write the operator runbook**

Create `docs/webhook-ingress.md` covering, in this order:

1. **What it is** — push ingress; the pull counterpart is `checks.triggers`.
2. **Wiz setup** — Settings → Integrations → Add Integration → SIEM & Automation
   Tools → Webhook; URL is the Function URL plus `/wiz`; Authentication = Token
   (Bearer) with the value `km init` printed once; payload format JSON: Generic;
   paste the template from `docs/webhook-templates/`.
3. **Automation Rule** — Policies → Automation Rules → New Rule; set the trigger
   and filters (severity, project, cloud platform) **here**, because filtering on
   the Wiz side means the request is never sent at all.
4. **km-config.yaml** — the full `webhooks:` block with every key explained.
5. **Storm control** — the four layers, which fail open and which fail closed,
   and the fact that `group_by` coalesces rather than batches.
6. **The `{{...}}` non-collision** — Wiz renders its variables before sending;
   km expands `{{severity}}`/`{{raw}}` against the already-rendered envelope.
   Say this explicitly or someone will chase it for an hour.
7. **Deploy surface** — `make build` FIRST (new `regionalModules()` entries; a
   stale binary silently skips them), then `make build-lambdas`, then
   `km init --dry-run=false` (**not** `--sidecars`), then one
   `km destroy && km create` on ir-bot to gain the queue and poller.
8. **Token rotation** — `aws ssm put-parameter --overwrite` plus a Wiz-side
   paste; `km init` never overwrites an existing token.
9. **Troubleshooting** — `webhook_unauthorized`, `webhook_unparseable`,
   `webhook_no_rule_match`, `webhook_replay_dropped`, `webhook_cooldown_suppressed`,
   `webhook_rate_ceiling_tripped`, `webhook_queue_url_missing`, plus DLQ depth.
10. **Limits** — a static bearer token is replayable beyond the nonce TTL; Wiz
    offers no signature; mTLS is unavailable on a Function URL and is not planned.

- [ ] **Step 4: Update CLAUDE.md**

Add a phase block in the established house style (what shipped, what is dormant,
the deploy surface, the traps) and a "Where to look" row:

```markdown
| Push webhook ingress — `webhooks:` block, Wiz Automation Rule setup, canonical km payload template, storm control (replay/cooldown/group_by/rate ceiling), deploy surface | `docs/webhook-ingress.md` |
```

- [ ] **Step 5: Verify the templates are valid JSON once rendered**

Run:

```bash
for f in docs/webhook-templates/*.json; do
  sed -E 's/\{\{[^}]*\}\}/X/g' "$f" | python3 -m json.tool >/dev/null \
    && echo "OK $f" || echo "INVALID $f"
done
```

Expected: `OK` for both. (The `sed` stands in placeholder values so the structure
alone is validated; Mustache braces are not JSON.)

- [ ] **Step 6: Commit**

```bash
git add docs/webhook-templates/ docs/webhook-ingress.md CLAUDE.md
git commit -m "docs(webhook): Wiz payload templates and operator runbook"
```

---

### Task 14: Full-suite verification

**Files:** none — verification only.

- [ ] **Step 1: Run the full test suite with an explicit exit code**

Run:

```bash
go test ./... -timeout 900s > /tmp/km-test.out 2>&1
echo "EXIT=$?"
grep -E '^(FAIL|ok  )' /tmp/km-test.out | grep -c '^FAIL'
```

Expected: `EXIT=0` and a FAIL count of `0`. Do **not** pipe `go test` into `tail`
and read that exit code — it returns tail's 0 and masks a FAIL
(`feedback_check_go_test_exit_not_pipe`).

- [ ] **Step 2: Build the binary and the Lambda zips**

Run: `make build && make build-lambdas; echo "EXIT=$?"`
Expected: `EXIT=0`, and `km-webhook-bridge` appears among the built zips.

- [ ] **Step 3: Confirm dormancy against a config with no webhooks: block**

Run: `go test ./pkg/compiler/ ./internal/app/config/ ./internal/app/cmd/ -v; echo "EXIT=$?"`
Expected: `EXIT=0`. Any golden diff on a profile without
`webhook.inbound.enabled` is a dormancy bug in the userdata gating, not a golden
that needs re-recording.

- [ ] **Step 4: Validate every shipped profile still parses**

Run: `./scripts/validate-all-profiles.sh; echo "EXIT=$?"`
Expected: `EXIT=0`. This catches a JSON-schema edit that forgot
`additionalProperties` handling for the new `notification.webhook` block.

- [ ] **Step 5: Commit any fixes**

```bash
git add -A
git commit -m "test: full-suite verification for the webhook ingress bridge"
```

---

## Live UAT (post-merge, against a real install)

Not a code task — record results in `.planning/phases/<phase>/UAT.md`. Prior
phases are unambiguous that live UAT finds classes of bug the unit suite cannot;
Phase 126 found twelve.

1. `make build && make build-lambdas && km init --dry-run=false` — confirm the
   token is minted and printed exactly once.
2. Configure the Wiz integration and Automation Rule with the shipped template.
3. `km doctor` — the webhook group reports green.
4. Drive a synthetic authenticated POST at the Function URL; confirm the queue
   receives the envelope and the poller runs a turn.
5. Repeat the identical POST; confirm `webhook_replay_dropped`.
6. Fire two payloads sharing a `group_by` value; confirm the second is
   `webhook_cooldown_suppressed`.
7. Exceed `max_dispatches`; confirm `webhook_rate_ceiling_tripped`.
8. Trigger a real Wiz Issue; confirm ir-bot runs `/triage`.
9. `km pause ir-bot && km resume ir-bot`; confirm the poller still finds its
   queue URL (the `SandboxMetadata` round-trip).
10. Record answers to the four spec open items: delivery-id header, gzip, the
    real `entitySnapshot` leaf names, and whether `/triage` exists on ir-bot.
