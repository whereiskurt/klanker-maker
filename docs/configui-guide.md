# Klanker Maker ConfigUI Guide

## Overview

ConfigUI is a Go HTTP server with an embedded web UI that provides a browser-based
alternative to the `km` CLI. It serves as a dashboard for managing sandbox profiles
and monitoring running sandboxes.

Core capabilities:

- **Profile editing** — load, edit, and validate SandboxProfile YAML in a Monaco
  editor with schema-aware autocompletion.
- **Live sandbox status** — view all running sandboxes with real-time status updates
  via HTMX polling.
- **AWS resource discovery** — inspect EC2 instances, ECS tasks, VPCs, security
  groups, and IAM roles associated with each sandbox, discovered through the AWS
  Resource Groups Tagging API.
- **SOPS secrets management** — encrypt, decrypt, and rotate secrets backed by the
  `alias/km-sops` KMS key without touching the CLI.

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

### AWS credentials

ConfigUI requires the same AWS credentials as the CLI. It uses the
`klanker-terraform` AWS profile. Make sure your credentials are configured before
starting the server:

```bash
export AWS_PROFILE=klanker-terraform
go run ./cmd/configui/
```

---

## Dashboard

The main page at `/` displays all running sandboxes in a table. Each row shows:

| Column | Description |
|---|---|
| Status | Current sandbox state (running, provisioning, destroying, error) |
| Profile | Name of the SandboxProfile used to create the sandbox |
| Substrate | EC2 or ECS |
| Region | AWS region |
| TTL remaining | Time left before the sandbox expires |

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
- **Substrate** — EC2 or ECS
- **Region** — AWS region
- **Status** — current state
- **Created at** — timestamp
- **TTL expiry** — when the sandbox will be automatically destroyed
- **Resources**:
  - EC2 instance ID or ECS task ARN (depending on substrate)
  - VPC ID
  - Security groups
  - IAM role ARN

Resource information is discovered live from AWS using the Resource Groups Tagging
API. Tags applied during sandbox creation (`km:sandbox-id`, `km:profile`, etc.) are
used to locate associated resources.

---

## Sandbox Logs

Available at `GET /api/sandboxes/{id}/logs`.

Streams audit and network logs from CloudWatch for the selected sandbox. Updates
arrive in real time so you can watch sandbox activity as it happens. This is
equivalent to tailing the CloudWatch log group associated with the sandbox.

---

## Profile Editor

> Phase 5 feature (stub route available now).

The profile editor is served at `/editor` and provides a Monaco-based code editor
for SandboxProfile YAML files.

Features:

- **Load** — open any SandboxProfile YAML from the profiles directory.
- **Edit** — full Monaco editor with YAML syntax highlighting.
- **Save** — write changes back to disk.
- **Real-time validation** — as you type, the editor posts the YAML to
  `POST /api/validate` (which calls `km validate` under the hood) and displays
  errors inline.
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

## SOPS Secrets Management

> Phase 5 feature (stub route available now).

The secrets management page at `/secrets` provides a UI for working with
SOPS-encrypted secrets without needing the CLI.

Capabilities:

- **List** — view all encrypted secrets.
- **Encrypt** — encrypt a new plaintext value using the `alias/km-sops` KMS key.
- **Decrypt** — decrypt an existing secret (requires appropriate KMS permissions).
- **Rotate** — re-encrypt secrets with the current KMS key.

All encryption and decryption operations use the `alias/km-sops` KMS key alias.
The AWS credentials used by ConfigUI must have `kms:Encrypt` and `kms:Decrypt`
permissions on that key.

---

## Routes Reference

| Method | Path | Description |
|---|---|---|
| GET | `/` | Dashboard — sandbox overview |
| GET | `/api/sandboxes` | Sandbox list HTML partial (HTMX) |
| GET | `/api/sandboxes/{id}` | Sandbox detail |
| GET | `/api/sandboxes/{id}/logs` | Sandbox logs (streaming) |
| GET | `/api/schema` | SandboxProfile JSON Schema |
| GET | `/editor` | Profile editor (stub) |
| GET | `/secrets` | Secrets management (stub) |
| POST | `/api/validate` | Validate profile YAML |
| GET | `/static/*` | Embedded static assets (CSS, JS) |

---

## Architecture

ConfigUI is a single Go binary with no external runtime dependencies.

- **Embedded assets** — HTML templates, CSS, and JavaScript are embedded into the
  binary at compile time using Go 1.22+ `embed` directives. No separate file
  serving or asset pipeline is needed.
- **HTMX** — live updates are driven by HTMX attributes in the HTML templates.
  The server returns HTML partials rather than JSON, keeping the frontend simple
  and JavaScript-minimal.
- **Zerolog middleware** — all HTTP requests are logged through zerolog structured
  logging middleware, producing JSON log lines with method, path, status, and
  latency.
- **AWS adapters**:
  - **S3 lister** — reads SandboxProfile YAML files and sandbox state from S3.
  - **Resource Groups Tagging API** — discovers AWS resources (EC2, ECS, VPC, SG,
    IAM) associated with each sandbox by querying tags.

---

## Deployment

### Local development

Run ConfigUI directly from the repository root:

```bash
export AWS_PROFILE=klanker-terraform
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

ConfigUI is **read-only** with respect to sandbox lifecycle. It does not create or
destroy sandboxes. That remains the responsibility of the `km` CLI (or automation
that calls it).

The one exception is **secrets management**, which performs write operations
(encrypt, re-encrypt) against the KMS-backed SOPS secrets.
