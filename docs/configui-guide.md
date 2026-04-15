# Klanker Maker ConfigUI Guide

## Overview

ConfigUI is a Go HTTP server with an embedded web UI that provides a browser-based
alternative to the `km` CLI. It serves as a dashboard for managing sandbox profiles
and monitoring running sandboxes.

Core capabilities:

- **Profile editing** — load, edit, and validate SandboxProfile YAML in a Monaco
  editor with schema-aware autocompletion.
- **Live sandbox status** — view all running sandboxes with periodic status updates
  via HTMX polling.
- **Sandbox lifecycle actions** — create sandboxes from profiles, destroy sandboxes,
  and extend sandbox TTLs directly from the UI.
- **AWS resource discovery** — inspect resource ARNs associated with each sandbox,
  discovered through the AWS Resource Groups Tagging API.
- **SSM secrets management** — list, create, update, reveal (decrypt), and delete
  secrets stored as SecureString parameters in AWS SSM Parameter Store under the
  `/km/` prefix.

The source lives at `cmd/configui/`.

---

## Running ConfigUI

### From source

```bash
go run ./cmd/configui/
```

### Build and run

```bash
go build -o configui ./cmd/configui/
./configui
```

### Configuration

| Environment variable | Default | Description |
|---|---|---|
| `CONFIGUI_ADDR` | `:8080` | Listen address and port |
| `KM_BUCKET` | `tf-km` | S3 state bucket name |
| `KM_PROFILES_DIR` | `profiles` | Path to profiles directory on disk |
| `KM_BUDGET_TABLE` | `km-budgets` | DynamoDB table for budget data |
| `KM_DOMAIN` | `klankermaker.ai` | Base domain for branding |

### AWS credentials

ConfigUI requires the same AWS credentials as the CLI. It uses the standard AWS
credential chain (environment variables, shared credentials file, instance
metadata, etc.). Make sure your credentials are configured before starting the
server:

```bash
go run ./cmd/configui/
```

---

## Dashboard

The main page at `/` displays all running sandboxes in a table. Each row shows:

| Column | Description |
|---|---|
| Sandbox ID | Sandbox identifier (truncated for display) |
| Profile | Name of the SandboxProfile used to create the sandbox |
| Substrate | EC2, ECS, or Docker |
| Status | Current sandbox state (running, provisioning, destroying, error) |
| TTL Remaining | Time left before the sandbox expires |
| Created At | Timestamp when the sandbox was created |
| Compute Budget | Compute spend vs. limit |
| AI Budget | AI spend vs. limit |

The table auto-refreshes via HTMX polling. The browser issues periodic
`GET /api/sandboxes` requests which return an HTML partial that replaces the table
body. No full-page reload is required.

Click any sandbox row to open the detail sidebar.

---

## Sandbox Detail

The detail sidebar loads from `GET /api/sandboxes/{id}` and displays full
information about a single sandbox:

- **Sandbox ID**
- **Profile** — the SandboxProfile name
- **Substrate** — EC2, ECS, or Docker
- **Status** — current state
- **AWS Resources** — a flat list of Resource ARNs associated with the sandbox
- **Profile YAML** — the full profile content if the file exists on disk

Resource information is discovered live from AWS using the Resource Groups Tagging
API. Tags applied during sandbox creation (`km:sandbox-id`, `km:profile`, etc.) are
used to locate associated resources.

---

## Sandbox Logs

Available at `GET /api/sandboxes/{id}/logs`.

Fetches recent audit and network log entries from CloudWatch for the selected
sandbox. The handler calls `FilterLogEvents` with a limit of 20 entries from the
`/km/sandboxes` log group, filtered by sandbox ID. This is a one-shot fetch, not a
streaming or real-time tail.

---

## Profile Editor

The profile editor is served at `/editor` and provides a Monaco-based code editor
for SandboxProfile YAML files.

Features:

- **Load** — open any SandboxProfile YAML from the profiles directory
  (`GET /api/profiles` lists files, `GET /api/profiles/{name}` fetches content).
  Pre-load a profile via the `?profile=name` query parameter.
- **Edit** — full Monaco editor with YAML syntax highlighting.
- **Save** — write changes back to disk via `PUT /api/profiles/{name}`. The save
  response includes any validation warnings (save proceeds regardless of warnings).
- **Real-time validation** — as you type, the editor posts the YAML to
  `POST /api/validate` (which calls `profile.Validate()` directly as a Go function,
  not a subprocess) and displays errors inline.
- **Schema hints** — the editor fetches the SandboxProfile JSON Schema from
  `GET /api/schema` and uses it to power autocompletion and inline validation
  markers.

---

## Schema Endpoint

```
GET /api/schema
```

Returns the JSON Schema definition for a SandboxProfile. The response content type
is `application/json`.

This endpoint is consumed by the profile editor for autocompletion and validation.
It can also be used by external tools or CI pipelines that need the schema
definition.

---

## SSM Secrets Management

The secrets management page at `/secrets` provides a UI for working with secrets
stored as SSM Parameter Store SecureString parameters under the `/km/` prefix.

Capabilities:

- **List** — view all parameters under `/km/` with metadata (name, type, last
  modified, version). Values are not returned in the list for safety.
  Uses `ssm:GetParametersByPath` with `Recursive: true`.
- **Create / Update** — store a new secret or update an existing one via
  `PUT /api/secrets/{name...}`. Uses `ssm:PutParameter` with type `SecureString`
  and `Overwrite: true`.
- **Reveal (Decrypt)** — fetch and display the decrypted value of a single parameter
  via `GET /api/secrets/{name...}`. Uses `ssm:GetParameter` with
  `WithDecryption: true`. HTMX requests get an inline HTML span with a PII-blur
  toggle.
- **Delete** — remove a parameter via `DELETE /api/secrets/{name...}`. Uses
  `ssm:DeleteParameter`. Deleting a non-existent parameter is treated as a
  successful no-op (idempotent).

All parameter names must start with `/km/`. The AWS credentials used by ConfigUI
must have `ssm:GetParametersByPath`, `ssm:GetParameter`, `ssm:PutParameter`, and
`ssm:DeleteParameter` permissions on the `/km/*` path.

---

## Routes Reference

| Method | Path | Description |
|---|---|---|
| GET | `/` | Dashboard — sandbox overview |
| GET | `/api/sandboxes` | Sandbox list HTML partial (HTMX) |
| GET | `/api/sandboxes/{id}` | Sandbox detail |
| GET | `/api/sandboxes/{id}/logs` | Sandbox logs (recent entries) |
| POST | `/api/sandboxes/{id}/destroy` | Destroy a sandbox |
| PUT | `/api/sandboxes/{id}/ttl` | Extend sandbox TTL |
| POST | `/api/sandboxes/create` | Quick-create a sandbox from a profile |
| GET | `/api/schema` | SandboxProfile JSON Schema |
| GET | `/editor` | Profile editor page |
| POST | `/api/validate` | Validate profile YAML |
| GET | `/api/profiles` | List available profiles |
| GET | `/api/profiles/{name}` | Get profile YAML content |
| PUT | `/api/profiles/{name}` | Save profile YAML to disk |
| GET | `/secrets` | Secrets management page |
| GET | `/api/secrets` | List SSM parameters under /km/ |
| GET | `/api/secrets/{name...}` | Decrypt/reveal a secret value |
| PUT | `/api/secrets/{name...}` | Create or update a secret |
| DELETE | `/api/secrets/{name...}` | Delete a secret |
| GET | `/static/*` | Embedded static assets (CSS, JS) |

---

## Architecture

ConfigUI is a single Go binary with no external runtime dependencies.

- **Embedded assets** — HTML templates, CSS, and JavaScript are embedded into the
  binary at compile time using Go `embed` directives (available since Go 1.16).
  No separate file serving or asset pipeline is needed.
- **HTMX** — live updates are driven by HTMX attributes in the HTML templates.
  The server returns HTML partials rather than JSON, keeping the frontend simple
  and JavaScript-minimal.
- **Zerolog middleware** — all HTTP requests are logged through zerolog middleware
  using `zerolog.ConsoleWriter` for human-readable output (method, path, and
  duration).
- **AWS adapters**:
  - **S3 lister** — reads SandboxProfile YAML files and sandbox state from S3.
  - **Resource Groups Tagging API** — discovers AWS resource ARNs associated with
    each sandbox by querying tags.
  - **SSM Parameter Store** — CRUD operations on `/km/` prefixed SecureString
    parameters for secrets management.
  - **DynamoDB** — reads budget data from the budgets table for dashboard display.
  - **EventBridge Scheduler** — manages TTL schedules for sandbox expiry extension.
  - **CloudWatch Logs** — fetches recent audit log entries via FilterLogEvents.

---

## Deployment

### Local development

Run ConfigUI directly from the repository root:

```bash
go run ./cmd/configui/
```

Then open `http://localhost:8080` in your browser.

### Container

ConfigUI can be deployed as a container alongside the CLI. Build the binary and
package it into a container image:

```bash
go build -o configui ./cmd/configui/
```

The container needs AWS credentials (via environment variables or an IAM role) and
network access to the AWS APIs used by the platform.

### Access model

ConfigUI supports both read and write operations on sandboxes and secrets:

- **Create** — `POST /api/sandboxes/create` shells out to `km create` with the
  selected profile.
- **Destroy** — `POST /api/sandboxes/{id}/destroy` shells out to `km destroy` with
  the sandbox ID.
- **Extend TTL** — `PUT /api/sandboxes/{id}/ttl` reschedules the sandbox expiry via
  EventBridge Scheduler.
- **Secrets CRUD** — create, update, reveal, and delete SSM Parameter Store
  SecureString parameters under `/km/`.

The AWS credentials used by ConfigUI must have permissions for all the above
operations. Routes are registered using Go 1.22+ method+path mux patterns.
