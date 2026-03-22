# Phase 5: ConfigUI - Context

**Gathered:** 2026-03-22
**Status:** Ready for planning

<domain>
## Phase Boundary

Web dashboard for managing sandbox profiles and monitoring live sandboxes — profile editor with inline validation, live sandbox status dashboard, AWS resource discovery, and SOPS secrets management. Built as a fresh Go application at `cmd/configui/` inspired by defcon.run.34's ConfigUI but not copied from it.

</domain>

<decisions>
## Implementation Decisions

### Profile Editor
- YAML text editor using Monaco (VS Code's editor) — full language server experience with syntax highlighting, JSON Schema-backed autocomplete, and inline error markers
- Live as-you-type validation — debounced (~500ms), red squiggles on error lines, hover for details. Calls `km validate` under the hood
- Filesystem-backed — profiles live in the `profiles/` directory on disk, editor reads/writes files directly. Same files the CLI uses
- Show parent profile + diff when a profile uses `extends` — resolved parent values visible, overrides highlighted. Helps operator understand what they're changing

### Dashboard Layout & Polling
- Table layout — sortable columns: sandbox ID, profile, substrate, status, TTL remaining, created at. Dense, efficient for scanning many sandboxes
- 10-second polling interval for status updates — low AWS API cost since `km list` uses single tag-based discovery call
- Sandbox detail view shows: AWS resources (instance ID/task ARN, VPC, SGs, IAM role), the profile YAML that created it, recent audit log entries, and budget spend (when Phase 6 is done)
- Full actions supported — destroy, extend TTL, quick-create from profile. ConfigUI is a full alternative to the CLI, not just a viewer

### SOPS Secrets Management
- List + create/edit/decrypt interface — table of SSM parameters with name, type, last modified. Click to view decrypted, create new, edit inline, delete with confirmation
- PII blur by default — secret values are blurred/masked on screen, click to reveal temporarily. Reuse the CSS blur pattern from defcon.run.34
- Auto-detect KMS key from `.sops.yaml` config — same key the CLI uses, no manual selection
- Scoped to `/km/` prefix — only show parameters under `/km/*` path. Prevents accidental exposure of non-KM secrets

### Build Approach
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

</decisions>

<specifics>
## Specific Ideas

- defcon.run.34's PII blur pattern with `pii-blur` / `pii-revealed` CSS classes — proven UX for secrets display
- defcon.run.34's discovery refresh pattern — periodic AWS API calls with visual refresh indicator
- Dashboard should feel operational — like a lightweight AWS console for sandboxes, not a marketing page
- Budget spend columns in the sandbox table should be ready as placeholder columns even before Phase 6 ships

</specifics>

<code_context>
## Existing Code Insights

### Reusable Assets
- `pkg/aws/discover.go`: Tag-based sandbox discovery — powers `km list`, directly usable for dashboard polling
- `pkg/aws/spot.go`: Spot instance helpers — useful for sandbox detail view
- `pkg/profile/`: Schema types, validation, inheritance resolver — profile editor validation backend
- `pkg/aws/ses.go`: SES helpers — could power notification preferences from the UI
- `internal/app/cmd/create.go`, `destroy.go`: Create/destroy logic — dashboard actions call the same code paths

### Established Patterns
- Cobra CLI commands in `internal/app/cmd/` — ConfigUI handler structure can mirror this
- `go:embed` for schemas (already used for JSON Schema) — same pattern for HTML templates and static assets
- Narrow AWS interfaces (`S3PutAPI`, `SESV2API`) — ConfigUI should follow this for testability

### Integration Points
- `km validate` called from editor validation — either as subprocess or by importing `pkg/profile` directly
- `km list` / `km status` data feeds the dashboard — import `pkg/aws` discovery functions
- `km create` / `km destroy` called from dashboard actions — either subprocess or import `internal/app/cmd`
- SOPS CLI or `go.mozilla.org/sops/v3` library for encrypt/decrypt operations

</code_context>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 05-configui*
*Context gathered: 2026-03-22*
