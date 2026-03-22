# Phase 5: ConfigUI - Research

**Researched:** 2026-03-22
**Domain:** Go web server, html/template, HTMX, Monaco Editor, AWS SSM, SOPS
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

#### Profile Editor
- YAML text editor using Monaco (VS Code's editor) — full language server experience with syntax highlighting, JSON Schema-backed autocomplete, and inline error markers
- Live as-you-type validation — debounced (~500ms), red squiggles on error lines, hover for details. Calls `km validate` under the hood
- Filesystem-backed — profiles live in the `profiles/` directory on disk, editor reads/writes files directly. Same files the CLI uses
- Show parent profile + diff when a profile uses `extends` — resolved parent values visible, overrides highlighted. Helps operator understand what they're changing

#### Dashboard Layout & Polling
- Table layout — sortable columns: sandbox ID, profile, substrate, status, TTL remaining, created at. Dense, efficient for scanning many sandboxes
- 10-second polling interval for status updates — low AWS API cost since `km list` uses single tag-based discovery call
- Sandbox detail view shows: AWS resources (instance ID/task ARN, VPC, SGs, IAM role), the profile YAML that created it, recent audit log entries, and budget spend (when Phase 6 is done)
- Full actions supported — destroy, extend TTL, quick-create from profile. ConfigUI is a full alternative to the CLI, not just a viewer

#### SOPS Secrets Management
- List + create/edit/decrypt interface — table of SSM parameters with name, type, last modified. Click to view decrypted, create new, edit inline, delete with confirmation
- PII blur by default — secret values are blurred/masked on screen, click to reveal temporarily. Reuse the CSS blur pattern from defcon.run.34
- Auto-detect KMS key from `.sops.yaml` config — same key the CLI uses, no manual selection
- Scoped to `/km/` prefix — only show parameters under `/km/*` path. Prevents accidental exposure of non-KM secrets

#### Build Approach
- Fresh build inspired by defcon.run.34 — not a copy. Use defcon.run.34 as reference for Go HTTP server patterns, template structure, and AWS discovery approach, but write from scratch
- Go `html/template` + HTMX for frontend — server-rendered HTML with HTMX for dynamic updates (polling, partial page swaps). Single binary, no Node build step, no JS framework
- Monaco editor loaded from CDN or embedded — the one JS dependency for the YAML editor
- Binary lives at `cmd/configui/` — consistent with `cmd/km/` and `cmd/ttl-handler/`. Shares packages from `pkg/` (profile, aws, compiler)

### Claude's Discretion
- Page navigation structure (tabs vs sidebar vs single page with sections)
- CSS framework choice (Tailwind, vanilla CSS, or minimal utility classes)
- HTMX polling patterns and partial swap strategy
- Error handling and toast/notification UI
- How Monaco is embedded or loaded (CDN vs go:embed)
- Exact table styling, responsive breakpoints

### Deferred Ideas (OUT OF SCOPE)
None — discussion stayed within phase scope
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| CFUI-01 | Web-based profile editor for creating/editing SandboxProfile YAML | Monaco editor via CDN ESM; validation backend imports `pkg/profile.Validate()`; filesystem reads/writes `profiles/` dir |
| CFUI-02 | Live sandbox status dashboard showing running sandboxes | `pkg/aws.ListAllSandboxesByS3()` already returns `[]SandboxRecord`; HTMX `hx-trigger="every 10s"` polls `/api/sandboxes` partial; table row swap with `hx-swap="outerHTML"` |
| CFUI-03 | AWS resource discovery showing what each sandbox provisioned | `pkg/aws.FindSandboxByID()` returns `SandboxLocation.ResourceARNs`; detail page fetches full ARN list via HTMX request on row click |
| CFUI-04 | SOPS secrets management UI for encrypt/decrypt operations | AWS SDK v2 `ssm.GetParametersByPath` with `WithDecryption: true`; `ssm.PutParameter` for create/update; `ssm.DeleteParameter` for delete; PII blur via CSS `filter: blur()` toggle |
</phase_requirements>

## Summary

Phase 5 builds `cmd/configui/` — a Go HTTP server that serves a dashboard for the KlankerMaker sandbox platform. The stack is Go `html/template` + HTMX 2.x for server-rendered HTML with partial page swaps, Monaco Editor loaded from CDN (no Node build step), and standard `net/http` + `go:embed` for a single-binary deployment.

The server reuses all existing `pkg/` packages directly: `pkg/profile` for validation and schema serving, `pkg/aws` for sandbox listing and resource discovery, and the project's AWS SDK v2 config patterns for SSM/KMS operations. The UI has four main sections: profile editor (Monaco + JSON Schema validation), sandbox dashboard (10s polling table), sandbox detail (resource ARNs, profile YAML, audit log), and SOPS secrets manager (SSM GetParametersByPath scoped to `/km/`).

The PII blur pattern for secrets is pure CSS — `filter: blur(4px)` on the value cell, removed via `onclick` or a small HTMX class-swap, with no server round-trip needed for reveal. HTMX 2.0 ships as a single JS file loadable from CDN (jsDelivr/unpkg), keeping Monaco as the only dependency that requires special ESM loading.

**Primary recommendation:** Use `net/http` (no external router needed for this surface area), `html/template` with `go:embed` for templates and static assets, HTMX 2.x from CDN for all dynamic updates, and Monaco Editor from CDN ESM for the YAML editor. Import `pkg/profile`, `pkg/aws`, and `pkg/compiler` directly — no subprocess invocation.

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `net/http` (stdlib) | Go 1.25 | HTTP server, routing | Zero deps; Go 1.22+ ServeMux supports method+path patterns |
| `html/template` (stdlib) | Go 1.25 | HTML rendering | XSS-safe; already used in project for HCL templates (via `text/template`) |
| `go:embed` (stdlib) | Go 1.25 | Embed templates + static assets | Already used in `pkg/profile` for JSON schema embedding |
| HTMX | 2.0.x | DOM updates without full JS | Single .js file; CDN load; no build step |
| Monaco Editor | 0.52.x | YAML/code editor | VS Code's editor; CDN ESM available via esm.sh or monaco-editor-esm-cdn |
| `pkg/profile` (internal) | — | Validation, schema, inheritance | Direct import; no subprocess needed |
| `pkg/aws` (internal) | — | Sandbox list, discovery, SES | Direct import of `ListAllSandboxesByS3`, `FindSandboxByID` |
| AWS SDK v2 `service/ssm` | v1.68.3 (already in go.mod) | SSM parameter CRUD | Already an indirect dependency; `GetParametersByPath` + `WithDecryption` |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `zerolog` | v1.33.0 (already in go.mod) | Structured request logging | Already project standard; use in HTTP middleware |
| `monaco-yaml` (CDN) | 5.x | YAML language support for Monaco | Loaded alongside Monaco from CDN; enables JSON Schema autocomplete in YAML mode |
| `github.com/go-chi/chi/v5` | v5.x (add if needed) | URL parameter routing `/sandboxes/{id}` | Add only if stdlib ServeMux path params feel unwieldy; chi is 100% net/http compatible |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `net/http` ServeMux | `go-chi/chi` | chi gives cleaner `{id}` URL params; stdlib fine for < 20 routes; add chi if routes grow |
| CDN Monaco | `go:embed` Monaco | Embedded Monaco is ~3MB; CDN is zero binary size; CDN requires internet access at runtime — acceptable for operator-facing tool |
| html/template | `a-h/templ` | templ gives compile-time type safety; html/template is the locked decision and simpler setup |
| Direct SSM SDK | SOPS decrypt library | SOPS library adds heavy dependency tree; SSM SDK v2 already in go.mod; secrets are in SSM not SOPS-encrypted files |

**Installation (new deps only):**
```bash
# Only if chi router is chosen — not required with Go 1.22+ ServeMux
go get github.com/go-chi/chi/v5
```

## Architecture Patterns

### Recommended Project Structure
```
cmd/configui/
├── main.go              # HTTP server entrypoint, flag parsing, listen/serve
internal/configui/       # (optional) handler package if logic is complex
pkg/configui/            # NOT needed — reuse pkg/aws and pkg/profile directly
cmd/configui/
├── main.go
├── handlers/
│   ├── dashboard.go     # GET /  — sandbox table
│   ├── sandboxes.go     # GET /api/sandboxes, GET /api/sandboxes/{id}
│   ├── profiles.go      # GET/PUT /api/profiles, GET /api/profiles/{name}
│   ├── validate.go      # POST /api/validate — profile validation endpoint
│   ├── secrets.go       # GET/POST/DELETE /api/secrets
│   └── schema.go        # GET /api/schema — serves JSON Schema for Monaco
├── templates/
│   ├── base.html        # Base layout with nav, HTMX script tag
│   ├── dashboard.html   # Sandbox table partial + full page
│   ├── editor.html      # Monaco editor page
│   ├── secrets.html     # SSM secrets table page
│   └── partials/
│       ├── sandbox_row.html   # Single table row (HTMX swap target)
│       ├── sandbox_detail.html
│       └── secret_row.html
└── static/
    └── style.css        # Minimal utility CSS
```

The `//go:embed templates` and `//go:embed static` directives in `main.go` bundle all assets.

### Pattern 1: HTMX Polling for Dashboard
**What:** Table rows update every 10 seconds via HTMX polling. Server returns only changed partial HTML.
**When to use:** Dashboard page — avoid full page reload; preserve sort state.
**Example:**
```html
<!-- Source: https://htmx.org/attributes/hx-trigger/ -->
<tbody id="sandbox-table-body"
       hx-get="/api/sandboxes"
       hx-trigger="every 10s"
       hx-swap="innerHTML">
  {{range .Sandboxes}}
  {{template "sandbox_row" .}}
  {{end}}
</tbody>
```

```go
// Source: standard net/http + html/template pattern
func (h *Handler) handleSandboxesPartial(w http.ResponseWriter, r *http.Request) {
    records, err := h.lister.ListSandboxes(r.Context(), false)
    if err != nil {
        http.Error(w, "failed to list sandboxes", http.StatusInternalServerError)
        return
    }
    // Detect HTMX request — render partial only
    if r.Header.Get("HX-Request") == "true" {
        h.renderPartial(w, "sandbox_row", records)
        return
    }
    h.renderPage(w, "dashboard", records)
}
```

### Pattern 2: go:embed for Templates and Static Assets
**What:** Bundle all HTML templates and CSS into the binary at build time.
**When to use:** Always — single binary deployment requirement.
**Example:**
```go
// Source: https://pkg.go.dev/embed (stdlib, Go 1.16+)
import "embed"

//go:embed templates
var templates embed.FS

//go:embed static
var static embed.FS

func main() {
    tmpl := template.Must(template.ParseFS(templates, "templates/*.html", "templates/partials/*.html"))
    http.Handle("/static/", http.FileServer(http.FS(static)))
    // ...
}
```

### Pattern 3: Validation API Endpoint
**What:** POST `/api/validate` accepts raw YAML bytes, returns JSON array of validation errors.
**When to use:** Monaco editor calls this on debounced keypress to get inline error markers.
**Example:**
```go
// Source: pkg/profile/validate.go (existing Validate() function)
func (h *Handler) handleValidate(w http.ResponseWriter, r *http.Request) {
    raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16)) // 64KB limit
    if err != nil {
        http.Error(w, "read error", http.StatusBadRequest)
        return
    }
    errs := profile.Validate(raw)
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(errs)
}
```

### Pattern 4: SSM Parameter List + Decrypt
**What:** List all `/km/` parameters, return metadata table. Decrypt on demand.
**When to use:** Secrets management page.
**Example:**
```go
// Source: https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/ssm
type SSMListAPI interface {
    GetParametersByPath(ctx context.Context, input *ssm.GetParametersByPathInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
    PutParameter(ctx context.Context, input *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
    DeleteParameter(ctx context.Context, input *ssm.DeleteParameterInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error)
}

func listKMParameters(ctx context.Context, client SSMListAPI) ([]ssm.Parameter, error) {
    output, err := client.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
        Path:           aws.String("/km/"),
        Recursive:      aws.Bool(true),
        WithDecryption: aws.Bool(false), // metadata only; decrypt on demand
    })
    // paginate if needed — output.NextToken
    return output.Parameters, err
}
```

### Pattern 5: PII Blur CSS Toggle
**What:** Secret values rendered blurred by default; click toggles reveal class.
**When to use:** Secrets table — every value cell.
**Example:**
```html
<!-- No HTMX needed — pure CSS + onclick -->
<style>
.pii-blur  { filter: blur(4px); cursor: pointer; user-select: none; }
.pii-blur:hover { opacity: 0.8; }
</style>

<span class="pii-blur" onclick="this.classList.toggle('pii-blur')">
  {{.Value}}
</span>
```
The server renders the decrypted value in the HTML (fetched only when user clicks "reveal" via an HTMX GET that returns the span). The blur is a visual mask, not a security boundary — the value is fetched only on reveal click.

### Pattern 6: Monaco Editor via CDN ESM (No Build Step)
**What:** Load Monaco and monaco-yaml from CDN using ES module import map.
**When to use:** Profile editor page — the only page requiring Monaco.
**Example:**
```html
<!-- Source: https://github.com/remcohaszing/monaco-yaml + esm.sh CDN -->
<script type="importmap">
{
  "imports": {
    "monaco-editor": "https://esm.sh/monaco-editor@0.52.0",
    "monaco-yaml": "https://esm.sh/monaco-yaml@5.2.0"
  }
}
</script>
<script type="module">
import * as monaco from 'monaco-editor';
import { configureMonacoYaml } from 'monaco-yaml';

configureMonacoYaml(monaco, {
  enableSchemaRequest: false,
  schemas: [{
    uri: '/api/schema',        // served by Go handler
    fileMatch: ['*.yaml'],
    schema: await fetch('/api/schema').then(r => r.json())
  }]
});

const editor = monaco.editor.create(document.getElementById('editor'), {
  value: document.getElementById('profile-content').textContent,
  language: 'yaml',
  theme: 'vs-dark',
  automaticLayout: true,
});

// Debounced validation
let debounce;
editor.onDidChangeModelContent(() => {
  clearTimeout(debounce);
  debounce = setTimeout(async () => {
    const yaml = editor.getValue();
    const resp = await fetch('/api/validate', {
      method: 'POST', body: yaml, headers: {'Content-Type': 'text/yaml'}
    });
    const errs = await resp.json();
    const markers = errs.map(e => ({
      severity: monaco.MarkerSeverity.Error,
      message: e.Message,
      startLineNumber: 1, endLineNumber: 1, // TODO: parse line from Path
      startColumn: 1, endColumn: 1,
    }));
    monaco.editor.setModelMarkers(editor.getModel(), 'km-validate', markers);
  }, 500);
});
</script>
```

### Pattern 7: Serving JSON Schema to Monaco
**What:** GET `/api/schema` serves the embedded `sandbox_profile.schema.json` as JSON.
**When to use:** Monaco configureMonacoYaml call on editor page load.
**Example:**
```go
// Source: pkg/profile/schema.go (sandboxProfileSchemaJSON already embedded)
func (h *Handler) handleSchema(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    w.Header().Set("Cache-Control", "public, max-age=3600")
    w.Write(profile.SchemaJSON()) // expose the embedded []byte
}
```
Add `SchemaJSON() []byte` accessor to `pkg/profile` — returns `sandboxProfileSchemaJSON` directly.

### Anti-Patterns to Avoid
- **Subprocess invocation for validate:** Don't `exec.Command("km", "validate")` — import `pkg/profile.Validate()` directly. Subprocess adds latency and process spawning complexity.
- **Full page response to HTMX requests:** Always check `r.Header.Get("HX-Request") == "true"` and return partial HTML only. Full page responses cause HTMX to swap entire `<body>`.
- **Polling on every page:** Only the dashboard table polls. Editor and secrets pages are request-response only. Over-polling increases AWS API costs.
- **Storing decrypted secrets in template variables long-term:** Decrypt on demand only. Never cache decrypted values server-side.
- **Global template parse at every request:** Parse templates once at startup (`template.Must(template.ParseFS(...))`), not per-request.
- **Using `text/template` for HTML:** Use `html/template` — it auto-escapes HTML and prevents XSS. The compiler uses `text/template` for HCL; the web layer must use `html/template`.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| YAML editor with autocomplete | Custom CodeMirror integration | Monaco + monaco-yaml | Monaco is VS Code's engine; monaco-yaml already implements JSON Schema YAML completion |
| Secret value masking | Custom JS encryption | CSS `filter: blur()` | Proven pattern; zero JS; matches defcon.run.34 UX |
| Sandbox list caching | Custom TTL cache struct | Direct `ListAllSandboxesByS3` per poll | 10s interval is already low-cost; single S3 ListObjects + parallel GetObject; cache adds invalidation complexity |
| Partial HTML rendering | Custom diff/patch logic | HTMX `hx-swap="innerHTML"` | HTMX handles all swap strategies; no JS needed |
| JSON Schema serving | JSON Schema re-compilation | Expose `sandboxProfileSchemaJSON` from `pkg/profile` | Already embedded; just return the bytes |
| SSM pagination | Manual pagination cursor | `ssm.NewGetParametersByPathPaginator` | AWS SDK v2 provides a paginator; `/km/` namespace should fit in one page but paginator is safe |

**Key insight:** Every data-fetch operation this UI needs already exists as a function in `pkg/aws` or `pkg/profile`. The server layer is thin glue between HTTP requests and existing package calls.

## Common Pitfalls

### Pitfall 1: HTMX Polling Triggers Full Page Replace
**What goes wrong:** HTMX poll request returns full HTML page, replaces entire `<body>` content, breaking navigation state.
**Why it happens:** Handler doesn't distinguish HTMX requests from full page loads.
**How to avoid:** Check `r.Header.Get("HX-Request") == "true"` in every polled handler. Return only the partial template when true.
**Warning signs:** Dashboard tab disappears after first poll cycle.

### Pitfall 2: Monaco Import Maps Browser Compatibility
**What goes wrong:** Import maps work in Chrome/Firefox/Safari modern but fail in older browsers; also, some CDN ESM packages need cross-origin headers.
**Why it happens:** Monaco's ESM build uses dynamic worker loading that needs a trusted CDN.
**How to avoid:** Use esm.sh as CDN (handles CORS properly); add `<script type="importmap">` before any module script; test in target browsers early.
**Warning signs:** `Failed to resolve module specifier` console errors.

### Pitfall 3: Monaco Worker Cross-Origin Issues
**What goes wrong:** Monaco requires Web Workers for language services; CDN workers fail with cross-origin policy errors.
**Why it happens:** Browser restricts Worker instantiation from different origins.
**How to avoid:** `monaco-editor-esm-cdn` package on jsDelivr pre-bundles workers. Alternatively, the `MonacoEnvironment.getWorkerUrl` override can proxy workers through the Go server.
**Warning signs:** Syntax highlighting works but autocomplete/error markers don't.

### Pitfall 4: SSM WithDecryption Requires IAM kms:Decrypt
**What goes wrong:** `GetParametersByPath` with `WithDecryption: true` returns AccessDeniedException even with SSM permissions.
**Why it happens:** SecureString params require separate `kms:Decrypt` permission on the KMS key, not just SSM permissions.
**How to avoid:** Operator running configui needs both `ssm:GetParametersByPath` and `kms:Decrypt` on the `/km/` KMS key in their IAM policy. Document this in startup error message.
**Warning signs:** List works (shows parameter names) but decrypt returns 403.

### Pitfall 5: Profile File Write Race Conditions
**What goes wrong:** Two browser tabs writing to the same profile YAML simultaneously corrupt the file.
**Why it happens:** `os.WriteFile` is not atomic for concurrent writers.
**How to avoid:** Use `os.WriteFile` which is atomic on most POSIX systems (writes temp then renames). Add a simple mutex per file path in the handler for v1. Single-operator model means this is low risk.
**Warning signs:** Profile YAML becomes malformed after concurrent edits.

### Pitfall 6: html/template Escapes Monaco Editor Content
**What goes wrong:** Profile YAML content injected into the Monaco editor textarea gets HTML-escaped, breaking indentation and special characters.
**Why it happens:** `html/template` auto-escapes `<`, `>`, `&`, `"`.
**How to avoid:** Use `template.JS()` type for JavaScript string literals or base64-encode the YAML content and decode in JS. Preferred: use a hidden `<textarea>` to hold raw content, read it with JS `textContent`, then call `editor.setValue()`.
**Warning signs:** YAML with `<` or `>` in values appears as `&lt;` in the editor.

### Pitfall 7: SSM GetParametersByPath Pagination
**What goes wrong:** Only first 10 parameters returned when more than 10 exist under `/km/`.
**Why it happens:** SSM paginates at 10 results by default.
**How to avoid:** Use `ssm.NewGetParametersByPathPaginator` (AWS SDK v2 paginator) to iterate all pages.
**Warning signs:** Secrets table shows fewer parameters than expected.

## Code Examples

Verified patterns from official sources:

### Server Entry Point (cmd/configui/main.go skeleton)
```go
// Source: stdlib net/http + go:embed pattern
package main

import (
    "embed"
    "html/template"
    "log"
    "net/http"
    "os"

    kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
    "github.com/whereiskurt/klankrmkr/pkg/profile"
)

//go:embed templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

func main() {
    addr := os.Getenv("CONFIGUI_ADDR")
    if addr == "" {
        addr = ":8080"
    }

    tmpl := template.Must(template.ParseFS(templateFS,
        "templates/*.html",
        "templates/partials/*.html",
    ))

    h := &Handler{tmpl: tmpl, profilesDir: "profiles/"}
    mux := http.NewServeMux()

    // Static assets
    mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))

    // Pages
    mux.HandleFunc("GET /", h.handleDashboard)
    mux.HandleFunc("GET /editor", h.handleEditorPage)
    mux.HandleFunc("GET /secrets", h.handleSecretsPage)

    // API — HTMX partials + actions
    mux.HandleFunc("GET /api/sandboxes", h.handleSandboxesPartial)
    mux.HandleFunc("GET /api/sandboxes/{id}", h.handleSandboxDetail)
    mux.HandleFunc("POST /api/sandboxes/{id}/destroy", h.handleDestroy)
    mux.HandleFunc("GET /api/profiles", h.handleProfileList)
    mux.HandleFunc("GET /api/profiles/{name}", h.handleProfileGet)
    mux.HandleFunc("PUT /api/profiles/{name}", h.handleProfileSave)
    mux.HandleFunc("POST /api/validate", h.handleValidate)
    mux.HandleFunc("GET /api/schema", h.handleSchema)
    mux.HandleFunc("GET /api/secrets", h.handleSecretsList)
    mux.HandleFunc("GET /api/secrets/{name...}", h.handleSecretDecrypt)
    mux.HandleFunc("PUT /api/secrets/{name...}", h.handleSecretPut)
    mux.HandleFunc("DELETE /api/secrets/{name...}", h.handleSecretDelete)

    log.Printf("configui listening on %s", addr)
    log.Fatal(http.ListenAndServe(addr, mux))
}
```

### SSM Paginator Pattern
```go
// Source: https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/ssm
import (
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/ssm"
)

func listKMSecrets(ctx context.Context, client *ssm.Client) ([]ssm.Parameter, error) {
    paginator := ssm.NewGetParametersByPathPaginator(client, &ssm.GetParametersByPathInput{
        Path:           aws.String("/km/"),
        Recursive:      aws.Bool(true),
        WithDecryption: aws.Bool(false), // metadata listing only
    })

    var params []ssm.Parameter
    for paginator.HasMorePages() {
        page, err := paginator.NextPage(ctx)
        if err != nil {
            return nil, fmt.Errorf("list /km/ parameters: %w", err)
        }
        params = append(params, page.Parameters...)
    }
    return params, nil
}
```

### HTMX Polling Partial Check
```go
// Pattern: detect HTMX vs full page request
func isHTMXRequest(r *http.Request) bool {
    return r.Header.Get("HX-Request") == "true"
}

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
    data := h.loadDashboardData(r.Context())
    if isHTMXRequest(r) {
        h.tmpl.ExecuteTemplate(w, "sandbox_rows", data)
        return
    }
    h.tmpl.ExecuteTemplate(w, "dashboard.html", data)
}
```

### Narrow SSM Interface (Testability Pattern)
```go
// Source: established pkg/aws narrow interface pattern
type SSMAPI interface {
    GetParametersByPath(ctx context.Context, input *ssm.GetParametersByPathInput, optFns ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error)
    GetParameter(ctx context.Context, input *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
    PutParameter(ctx context.Context, input *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
    DeleteParameter(ctx context.Context, input *ssm.DeleteParameterInput, optFns ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error)
}
// *ssm.Client satisfies SSMAPI directly.
```

### Profile Validation Handler
```go
// Source: pkg/profile/validate.go — Validate() already exists
func (h *Handler) handleValidate(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "POST only", http.StatusMethodNotAllowed)
        return
    }
    raw, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
    if err != nil || len(raw) == 0 {
        http.Error(w, "invalid body", http.StatusBadRequest)
        return
    }
    errs := profile.Validate(raw)
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(errs)
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Monaco loaded via RequireJS AMD | Monaco loaded via ESM from CDN | 2024 | No webpack/rollup needed; `<script type="importmap">` in modern browsers |
| `gorilla/mux` for URL params | stdlib `net/http` ServeMux with `{param}` | Go 1.22 (2024) | Zero dependency; `r.PathValue("id")` |
| HTMX 1.x separate extensions | HTMX 2.0 + extensions at extensions.htmx.org | June 2024 | Extensions versioned separately; class-tools extension for CSS class manipulation |
| go.mozilla.org/sops/v3 | github.com/getsops/sops/v3 | 2023 | Mozilla handed off to getsops org; use getsops import path for new code |

**Deprecated/outdated:**
- `go.mozilla.org/sops/v3`: Replaced by `github.com/getsops/sops/v3` — but since we use SSM SDK directly (not SOPS file decryption), this is not a blocker.
- `gorilla/mux`: Use stdlib ServeMux 1.22+ instead — `r.PathValue("name")` replaces `mux.Vars(r)["name"]`.
- HTMX CDN via unpkg: Prefer jsDelivr (`cdn.jsdelivr.net/npm/htmx.org@2`) for reliability.

## Open Questions

1. **Monaco Worker Loading in Production**
   - What we know: esm.sh CDN handles CORS; monaco-editor-esm-cdn on jsDelivr bundles workers
   - What's unclear: Whether esm.sh worker bundling works for monaco-yaml workers specifically without a build step
   - Recommendation: Use `monaco-editor-esm-cdn` package via jsDelivr as primary path; fall back to proxying workers through Go server if CDN worker loading fails in testing

2. **Profile YAML Line Numbers in Validation Errors**
   - What we know: `pkg/profile.ValidationError` has `Path` (JSON path like `spec.runtime.substrate`) but no line number
   - What's unclear: Monaco markers require `startLineNumber`/`startColumn`; mapping JSON path → line number requires YAML parse tree inspection
   - Recommendation: For v1, show errors as Monaco "info" decorations on line 1 with the full path message; add path-to-line mapping as a follow-on improvement

3. **Extend TTL Action**
   - What we know: Dashboard context menu includes "extend TTL"; EventBridge scheduler manages TTL
   - What's unclear: `pkg/aws` scheduler API has `CreateTTLSchedule` and `DeleteTTLSchedule` but no update — extending TTL requires delete+recreate
   - Recommendation: Implement as delete old schedule + create new schedule at (now + extension_duration); expose as `PUT /api/sandboxes/{id}/ttl`

4. **CSS Framework Decision (Claude's Discretion)**
   - What we know: Options are Tailwind via CDN, vanilla CSS, or minimal utility classes
   - Recommendation: Minimal vanilla CSS with utility classes (no Tailwind build step, no CDN needed for basic operational dashboard); ~200 lines of CSS covers table, blur, layout, and status badge colors

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing + `net/http/httptest` (stdlib) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./cmd/configui/... -count=1` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| CFUI-01 | Profile editor validation endpoint returns correct errors | unit | `go test ./cmd/configui/... -run TestHandleValidate -v` | ❌ Wave 0 |
| CFUI-01 | Profile editor save writes file to profiles/ directory | unit | `go test ./cmd/configui/... -run TestHandleProfileSave -v` | ❌ Wave 0 |
| CFUI-01 | Schema endpoint serves embedded JSON schema bytes | unit | `go test ./cmd/configui/... -run TestHandleSchema -v` | ❌ Wave 0 |
| CFUI-02 | Dashboard handler returns sandbox table rows partial | unit | `go test ./cmd/configui/... -run TestHandleDashboard -v` | ❌ Wave 0 |
| CFUI-02 | HTMX request returns partial template not full page | unit | `go test ./cmd/configui/... -run TestHTMXPartialSwap -v` | ❌ Wave 0 |
| CFUI-03 | Sandbox detail handler returns ResourceARNs from FindSandboxByID | unit | `go test ./cmd/configui/... -run TestHandleSandboxDetail -v` | ❌ Wave 0 |
| CFUI-04 | Secrets list handler calls GetParametersByPath with /km/ path | unit | `go test ./cmd/configui/... -run TestHandleSecretsList -v` | ❌ Wave 0 |
| CFUI-04 | Secret decrypt endpoint calls GetParameter with WithDecryption=true | unit | `go test ./cmd/configui/... -run TestHandleSecretDecrypt -v` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./cmd/configui/... -count=1`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `cmd/configui/handlers_test.go` — covers CFUI-01 through CFUI-04
- [ ] `cmd/configui/main.go` — server skeleton with `http.NewServeMux()` and route registration
- [ ] `cmd/configui/templates/` — base.html, dashboard.html, editor.html, secrets.html, partials/
- [ ] `cmd/configui/static/style.css` — minimal utility CSS

## Sources

### Primary (HIGH confidence)
- [htmx.org/attributes/hx-trigger](https://htmx.org/attributes/hx-trigger/) — polling `every 10s` syntax confirmed
- [htmx.org 2.0.0 release](https://htmx.org/posts/2024-06-17-htmx-2-0-0-is-released/) — HTMX 2.x shipping status, extension split
- [pkg.go.dev/embed](https://pkg.go.dev/embed) — `go:embed` FS API
- [pkg.go.dev/html/template](https://pkg.go.dev/html/template) — template.ParseFS stdlib
- [pkg.go.dev/net/http](https://pkg.go.dev/net/http) — ServeMux path param patterns (Go 1.22+)
- [pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/ssm](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/ssm) — GetParametersByPath, paginator
- `pkg/profile/validate.go` (internal) — Validate() function signature confirmed
- `pkg/aws/sandbox.go` (internal) — ListAllSandboxesByS3, SandboxRecord struct confirmed
- `pkg/aws/discover.go` (internal) — FindSandboxByID, SandboxLocation.ResourceARNs confirmed

### Secondary (MEDIUM confidence)
- [github.com/remcohaszing/monaco-yaml](https://github.com/remcohaszing/monaco-yaml) — JSON Schema YAML autocomplete plugin, configureMonacoYaml API
- [github.com/hatemhosny/monaco-editor-esm-cdn](https://github.com/hatemhosny/monaco-editor-esm-cdn) — CDN ESM bundle for Monaco without build step
- [esm.sh](https://esm.sh/) — ES module CDN; supports monaco-editor ESM imports
- [go-chi/chi v5](https://pkg.go.dev/github.com/go-chi/chi/v5) — 100% net/http compatible router; URL params via `chi.URLParam(r, "id")`

### Tertiary (LOW confidence)
- [dev.to: Build Web App with HTMX and Go](https://dev.to/calvinmclean/how-to-build-a-web-application-with-htmx-and-go-3183) — html/template + HTMX pattern examples

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all core libraries are stdlib or already in go.mod; HTMX and Monaco versions verified from official release pages
- Architecture: HIGH — patterns directly derived from existing codebase conventions (narrow interfaces, go:embed, internal/app/cmd structure)
- Pitfalls: MEDIUM — Monaco CDN worker pitfall based on known ESM cross-origin behavior; SSM IAM pitfall from official AWS docs
- Validation map: HIGH — existing `pkg/profile.Validate()` and `pkg/aws` functions confirmed via direct code reading

**Research date:** 2026-03-22
**Valid until:** 2026-06-22 (stable libraries; HTMX 2.x and Monaco 0.52.x API stable)
