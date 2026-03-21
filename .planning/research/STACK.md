# Stack Research

**Domain:** Policy-driven sandbox/execution environment platform (Go CLI + Terraform provisioning + TypeScript web UI)
**Researched:** 2026-03-21
**Confidence:** MEDIUM-HIGH (core libraries verified via pkg.go.dev and official docs; some version pins from live sources)

---

## Recommended Stack

### Go CLI Layer

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| Go | 1.23+ | Language runtime | Project already Go-first; slog, range-over-func, and toolchain management stabilized in 1.21-1.23; use latest stable |
| github.com/spf13/cobra | v1.10.2 | CLI command tree (`km create/destroy/list/validate`) | De facto standard for production Go CLIs; used by kubectl, hugo, docker CLI; provides persistent flags, completion, and nested subcommand structure that tiogo already patterns against |
| github.com/spf13/viper | v1.21.0 | Config file + env var binding | Hierarchical config (file → env → flags) with zero boilerplate; integrates with cobra via `BindPFlags`; maintenance-mode means no breaking changes |
| github.com/rs/zerolog | v1.34.0 | Structured, JSON-first logging for CLI and sidecars | Zero-allocation JSON output; slog is stdlib but zerolog is better for sidecar audit logs that ship to CloudWatch/S3 because its stream-based API produces clean JSON lines without the slog text prefix noise; imported by 28k+ Go projects |
| github.com/goccy/go-yaml | v1.x | YAML parsing for SandboxProfile | go-yaml/yaml (gopkg.in) is now archived; goccy/go-yaml is the actively maintained replacement, passes 60+ more YAML spec test cases, and provides path-based error messages which are required for `km validate` to give useful schema errors |
| github.com/santhosh-tekuri/jsonschema/v6 | v6.0.2 | SandboxProfile schema validation | JSON Schema Draft 2020-12 compliance; validates YAML via JSON conversion; `jv` CLI tool ships with the library for offline schema testing; preferred over go-playground/validator because the profile schema is declarative (apiVersion/kind/spec), not struct-tag-based |
| github.com/aws/aws-sdk-go-v2 | v1.41.4 | AWS API calls (EC2, IAM, SSM, STS) | v2 is the current AWS SDK for Go; context-propagating, modular (import only `service/ec2`, `service/iam`, etc.); v1 is EOL |

### Infrastructure Provisioning Layer

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| OpenTofu | 1.9.1 | IaC engine (replaces Terraform OSS) | HashiCorp declared Terraform OSS BSL end-of-life for July 2025; OpenTofu is the MPL 2.0 community fork under Linux Foundation governance; drop-in replacement with identical HCL and provider compatibility; Terragrunt fully supports it via `terraform_binary = "tofu"` |
| Terragrunt | 0.77.x | Orchestrates OpenTofu modules, DRY config | Project's existing defcon.run.34 patterns use Terragrunt; `km create` compiles SandboxProfile to Terragrunt inputs and runs `terragrunt apply`; `site.hcl` / service-level `terragrunt.hcl` hierarchy is proven |
| tenv | latest | Version manager for OpenTofu + Terragrunt | Successor to tfenv/tofuenv; manages multiple OpenTofu/Terragrunt versions per project; install via `brew install tenv` on macOS |
| AWS Provider (OpenTofu Registry) | ~5.x | EC2, VPC, IAM, SSM resources | Standard provider; lock via `.terraform.lock.hcl` equivalent |

### Sidecar Enforcement Layer

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| github.com/miekg/dns | v1.1.72 | DNS proxy sidecar (allowlist DNS filtering) | The canonical Go DNS library; full server + client API; every DNS-in-Go project builds on it; v2 is in development at codeberg.org/miekg/dns but v1.1.72 (Jan 2026) is the stable production choice for v1 of Klanker Maker; migrate to v2 post-MVP |
| github.com/elazarl/goproxy | v1.8.2 | HTTP/HTTPS proxy sidecar (allowlist HTTP filtering) | Provides `ConnectAction` (Accept/Reject/MITM/Hijack) for HTTP CONNECT interception; `DstHostIs` and regex condition API enables clean allowlist implementation; the MITM mode with custom CA cert is how you inspect HTTPS traffic for allowlist enforcement |
| github.com/rs/zerolog | v1.34.0 | Audit log sidecar (command + network log, JSON lines) | Same library as CLI; produces newline-delimited JSON that CloudWatch Logs Insights and S3 Select can query natively; zero-allocation path keeps sidecar overhead minimal |

### TypeScript Web UI (ConfigUI)

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| React | 19.x | UI component model | Current stable; shadcn/ui and TanStack are React 19-ready |
| Vite | 8.x | Build tool + dev server | Vite 8 ships Rolldown (Rust bundler), 10-30x faster builds; `@vitejs/plugin-react` v6 drops Babel for Oxc; Node.js 20.19+ required |
| TypeScript | 5.x | Type safety | Standard; Vite scaffolds TS by default |
| shadcn/ui | latest (Tailwind v4 branch) | Component library | Components are copied into the project (not a dependency), enabling full customization without fighting library constraints; built on Radix UI primitives for accessibility; Tailwind v4 support is shipped and documented |
| Tailwind CSS | v4.x | Utility-first styling | v4 uses PostCSS plugin model, ships as `@tailwindcss/vite` for Vite projects; lighter config file than v3 |
| TanStack Query | v5.x | Server state management (AWS API polling) | Correct tool for sandbox status polling, profile list fetching, CloudWatch log streaming; handles loading/error/stale states without Redux boilerplate |
| TanStack Table | v8.x | Table component for sandbox list + profile list | Headless; pairs naturally with shadcn/ui Data Table pattern documented on shadcn.com |
| React Router | v7.x | Client-side routing | Standard choice; TanStack Router is an alternative but React Router v7 is simpler for a two-route dashboard (profiles / sandboxes) |

---

## Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| github.com/google/uuid | v1.x | Generate sandbox IDs | Every `km create` needs a stable, unique sandbox ID |
| github.com/hashicorp/hcl/v2 | v2.x | Parse/write Terragrunt HCL inputs | When compiling SandboxProfile → Terragrunt input files programmatically |
| github.com/zclconf/go-cty | v1.x | HCL type system (required by hcl/v2) | Automatically required when using hcl/v2 |
| golang.org/x/sys | latest | Low-level syscalls for sidecar process isolation | When implementing filesystem path enforcement or namespace operations in sidecars |
| github.com/aws/aws-sdk-go-v2/service/ec2 | module version | EC2 describe/status calls | `km list` / `km status` — querying running sandbox instances |
| github.com/aws/aws-sdk-go-v2/service/ssm | module version | SSM Parameter Store for secrets injection | Secrets allowlist resolution at sandbox create time |
| github.com/aws/aws-sdk-go-v2/service/sts | module version | IAM role assumption (scoped sessions) | Identity management — `AssumeRole` with session duration limits |
| github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs | module version | CloudWatch log shipping from sidecars | Observability destination when profile specifies CloudWatch |

---

## Development Tools

| Tool | Purpose | Notes |
|------|---------|-------|
| tenv | OpenTofu + Terragrunt version management | `brew install tenv`; replaces tfenv, tofuenv; supports `.opentofu-version` file |
| golangci-lint | Go linter aggregator | Pin version in `.golangci.yml`; run in CI |
| cobra-cli | Cobra scaffold generator | `go install github.com/spf13/cobra-cli@latest`; generates boilerplate command files |
| pnpm | Fast Node package manager for ConfigUI | Preferred over npm for monorepo-style workspaces if ConfigUI is in the same repo |
| vitest | Unit testing for TypeScript | Bundled well with Vite 8; faster than Jest for Vite projects |
| Playwright | E2E testing for ConfigUI | When ConfigUI needs integration tests against real AWS status responses |

---

## Installation

```bash
# Go CLI — core
go get github.com/spf13/cobra@v1.10.2
go get github.com/spf13/viper@v1.21.0
go get github.com/rs/zerolog@v1.34.0
go get github.com/goccy/go-yaml@latest
go get github.com/santhosh-tekuri/jsonschema/v6@v6.0.2

# Go CLI — AWS
go get github.com/aws/aws-sdk-go-v2/config@latest
go get github.com/aws/aws-sdk-go-v2/service/ec2@latest
go get github.com/aws/aws-sdk-go-v2/service/iam@latest
go get github.com/aws/aws-sdk-go-v2/service/ssm@latest
go get github.com/aws/aws-sdk-go-v2/service/sts@latest
go get github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs@latest

# Go CLI — utilities
go get github.com/google/uuid@latest
go get github.com/hashicorp/hcl/v2@latest
go get github.com/zclconf/go-cty@latest

# Sidecar — DNS proxy
go get github.com/miekg/dns@v1.1.72

# Sidecar — HTTP proxy
go get github.com/elazarl/goproxy@v1.8.2

# Infrastructure tooling (macOS)
brew install tenv
tenv tofu install 1.9.1
tenv tg install latest

# ConfigUI
pnpm create vite@latest configui -- --template react-ts
cd configui
pnpm add @tanstack/react-query @tanstack/react-table react-router-dom
pnpm add -D tailwindcss @tailwindcss/vite vitest @testing-library/react
# then follow shadcn/ui init: https://ui.shadcn.com/docs/installation/vite
pnpm dlx shadcn@latest init
```

---

## Alternatives Considered

| Category | Recommended | Alternative | When to Use Alternative |
|----------|-------------|-------------|-------------------------|
| IaC engine | OpenTofu 1.9.1 | Terraform 1.x (BSL) | If your org has a HashiCorp enterprise agreement and needs Terraform Cloud integration; BSL Terraform OSS is EOL July 2025 |
| Go logging | zerolog v1.34.0 | stdlib `log/slog` | slog is fine for the CLI where output is human-readable; zerolog is better for sidecars writing machine-parseable JSON audit lines to CloudWatch/S3 |
| Go logging | zerolog v1.34.0 | logrus | Logrus is in maintenance mode, no new features; the tiogo reference architecture uses logrus but zerolog is the current recommendation for new Go projects |
| YAML parsing | goccy/go-yaml | gopkg.in/yaml.v3 | Never — go-yaml/yaml is archived; cobra v1.10.2 already migrated away from it |
| Schema validation | santhosh-tekuri/jsonschema/v6 | go-playground/validator | Use validator only for struct-tag validation (REST request bodies); SandboxProfile needs JSON Schema because the schema is external/user-facing, not embedded in struct tags |
| HTTP proxy | elazarl/goproxy | Build from net/http ReverseProxy | goproxy handles CONNECT tunneling and MITM certificate generation; building this from scratch would take weeks; goproxy v1.8.2 is production-proven |
| DNS proxy | miekg/dns v1 | Build from scratch / dnsproxy | miekg/dns is the Go DNS foundation; dnsproxy (AdguardTeam) is a higher-level tool but less suitable for embedding in a sidecar process you control fully |
| ConfigUI framework | React 19 + Vite 8 | Next.js | Next.js adds SSR/edge complexity unnecessary for a single-operator dashboard; Vite 8 + React 19 is lighter and the Go HTTP server (from defcon.run.34) handles API routing |
| ConfigUI state | TanStack Query v5 | Redux Toolkit / Zustand | This is a status dashboard, not a complex app; TanStack Query handles server state (polling, caching, invalidation) and nothing else is needed |
| ConfigUI components | shadcn/ui | Ant Design / Material UI | shadcn/ui copies components into the project — no upstream breaking changes; Ant Design and MUI impose opinionated themes that require significant overriding for a custom look |

---

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| gopkg.in/yaml.v3 (go-yaml/yaml) | Archived/unmaintained as of 2025; even cobra migrated away from it | github.com/goccy/go-yaml |
| github.com/aws/aws-sdk-go (v1) | EOL; no new service features added; context-propagation missing | github.com/aws/aws-sdk-go-v2 |
| sirupsen/logrus | Maintenance mode — no new features will be added; tiogo used it but it's no longer the recommendation | github.com/rs/zerolog (or stdlib slog for simple cases) |
| Terraform OSS (BSL) | HashiCorp declared BSL Terraform OSS EOL July 2025; open source projects should not build on it | OpenTofu 1.9.1 |
| OPA / Rego policy engine | Explicitly out of scope for v1 per PROJECT.md; schema-level allowlists are sufficient | SandboxProfile allowlist fields in spec |
| ECS/Fargate as substrate | Out of scope for v1 | EC2 only |
| miekg/dns v2 (codeberg) | December 2025 release — too new, still recommending migration; API differences from v1 are documented but ecosystem hasn't caught up | miekg/dns v1.1.72 (stable, Jan 2026) |
| gojsonschema (xeipuuv) | Only supports JSON Schema draft v4/v6/v7; does not support Draft 2020-12; last updated 2021 | santhosh-tekuri/jsonschema/v6 |
| Webpack / Create React App | CRA is dead (unmaintained); Webpack is replaced by Vite 8 + Rolldown for new projects | Vite 8 |

---

## Stack Patterns by Variant

**For the DNS proxy sidecar:**
- Use `miekg/dns` `ServeMux` with a custom `Handler` that checks `msg.Question[0].Name` against the allowlist
- Run as a separate binary injected via cloud-init user data, listening on `127.0.0.1:53`
- Redirect all DNS traffic to sidecar via `/etc/resolv.conf` rewrite or iptables REDIRECT rule
- Log every query (allowed/denied) via zerolog to stdout → CloudWatch agent

**For the HTTP proxy sidecar:**
- Use `elazarl/goproxy` with `ConnectAction = ConnectReject` as default, then `ConnectAccept` for allowlisted hosts
- For HTTPS inspection: generate a per-sandbox CA cert, install it in the EC2 instance trust store at provision time, use `ConnectMitm` mode
- Enforce via `http_proxy` / `https_proxy` environment variables set in the sandbox user's shell profile
- Log every request host + response code via zerolog

**For the audit log sidecar:**
- Use a lightweight Go binary that tails shell history or wraps the user shell (`HISTFILE` mode)
- For network audit: hook into the HTTP proxy and DNS proxy log streams rather than duplicating capture
- Emit structured zerolog JSON to stdout → captured by CloudWatch agent → shipped to configured destination

**For the Go CLI (`km`) command structure:**
- Follow tiogo pattern: `cmd/km/main.go` → `internal/app/cmd/` (one file per subcommand) → `pkg/` (reusable libs)
- Central `Config` struct with Viper binding, passed by pointer via cobra `PersistentPreRunE`
- Schema validation (`km validate`) runs before any AWS API calls in `km create`

**For the ConfigUI:**
- Serve the Vite-built static bundle from the Go HTTP server (embed with `//go:embed dist/*`)
- The Go server acts as BFF (backend-for-frontend): proxies AWS API calls so the frontend never holds AWS credentials
- Two main routes: `/profiles` (list + editor) and `/sandboxes` (live status + logs)

---

## Version Compatibility

| Package A | Compatible With | Notes |
|-----------|-----------------|-------|
| cobra v1.10.2 | viper v1.21.0 | Both use pflag; bind with `viper.BindPFlags(cmd.Flags())` |
| OpenTofu 1.9.1 | Terragrunt 0.77.x | Official compatibility table at docs.terragrunt.com confirms support |
| React 19 | Vite 8 + shadcn/ui Tailwind v4 | Confirmed compatible; shadcn/ui updated all components for React 19 |
| Vite 8 | Node.js 20.19+ or 22.12+ | Vite 8 dropped older Node; verify CI/CD node version |
| aws-sdk-go-v2 v1.41.4 | All service modules (ec2, ssm, sts, etc.) | Service modules version independently; use `go get` per service |
| miekg/dns v1.1.72 | Go 1.21+ | Confirmed; v1 stable, not v2 (codeberg) |
| elazarl/goproxy v1.8.2 | Go 1.20+ | Standard `net/http` dependencies |

---

## Sources

- https://pkg.go.dev/github.com/spf13/cobra — v1.10.2, verified live
- https://pkg.go.dev/github.com/spf13/viper — v1.21.0, verified live
- https://pkg.go.dev/github.com/rs/zerolog — v1.34.0, verified live
- https://pkg.go.dev/github.com/miekg/dns — v1.1.72 (Jan 22 2026), v2 migration notice
- https://pkg.go.dev/github.com/elazarl/goproxy — v1.8.2, verified
- https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6 — v6.0.2 (May 2025), verified
- https://github.com/aws/aws-sdk-go-v2/releases — v1.41.4 (Mar 2026), verified live
- https://docs.terragrunt.com/reference/supported-versions — OpenTofu 1.9.1 + Terragrunt 0.77.x compatibility
- https://vite.dev/blog/announcing-vite8 — Vite 8 Rolldown bundler announcement
- https://ui.shadcn.com/docs/tailwind-v4 — shadcn/ui Tailwind v4 + React 19 compatibility confirmed
- https://spacelift.io/blog/opentofu-vs-terraform — OpenTofu vs Terraform BSL analysis (MEDIUM confidence, blog source)
- https://controlmonkey.io/resource/terraform-license-change-impact-2025/ — Terraform OSS BSL EOL July 2025 (MEDIUM confidence)
- https://github.com/goccy/go-yaml — goccy/go-yaml as go-yaml/yaml replacement (HIGH confidence, confirmed across multiple migration issues)

---

*Stack research for: Klanker Maker — policy-driven sandbox platform*
*Researched: 2026-03-21*
