# Phase 37: docker-compose-local-substrate - Research

**Researched:** 2026-03-30
**Domain:** Docker Compose substrate integration into existing Go CLI / compiler pipeline
**Confidence:** HIGH

## Summary

Phase 37 adds a third substrate — `docker` — to the existing `ec2` / `ecs` pair. The codebase already has a clean three-layer architecture that makes this a bounded addition: the JSON schema and semantic validator gate the substrate enum; the compiler `Compile()` dispatcher switches on substrate and calls substrate-specific generators; and the CLI commands (`create`, `destroy`, `shell`, `stop`, `pause`) branch on `rec.Substrate` from S3 metadata.

Phase 36 (completed) already built the `km-sandbox` base container image with a working `entrypoint.sh` that consumes all the same environment variables this phase needs. The Docker substrate does not require any new container images — the four sidecar images (`km-dns-proxy`, `km-http-proxy`, `km-audit-log`, `km-tracing`) built by `km init` work unchanged.

The biggest design decision already locked in the DESCRIPTION.md is the **credential refresh via shared tmpfs volume** (Option C from the document): a `km-cred-refresh` sidecar container holds the operator's `sts:AssumeRole` credentials; all other containers get `AWS_SHARED_CREDENTIALS_FILE` pointing at a tmpfs volume. This isolates operator credentials cleanly.

**Primary recommendation:** Add a `compileDocker()` path in `pkg/compiler/compose.go` that generates `docker-compose.yml` using `text/template` (matching existing pattern). Extend `runCreate()` in `internal/app/cmd/create.go` with a `case "docker":` branch that skips Terragrunt, runs `docker compose up -d`, and writes S3 metadata. All CLI commands (`destroy`, `shell`, `stop`, `pause`) branch the same way.

---

## Standard Stack

### Core
| Library / Tool | Version | Purpose | Why Standard |
|----------------|---------|---------|--------------|
| `docker compose` CLI | V2 (plugin `docker compose`) | Lifecycle management | Already used for sidecar images; operator has Docker Desktop |
| `text/template` (stdlib) | Go 1.21+ | `docker-compose.yml` generation | Same pattern as `ec2ServiceHCLTemplate` and `ecsServiceHCLTemplate` — zero new deps |
| `encoding/base64` (stdlib) | — | Encode `KM_INIT_COMMANDS`, `KM_PROFILE_ENV` | Same pattern as ECS task definition generator |
| `encoding/json` (stdlib) | — | Marshal sidecar environment maps | Existing pattern |
| `aws-sdk-go-v2/service/sts` | already in go.mod | `AssumeRole` for scoped session credentials | Already imported in create.go for other paths |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `os/exec` (stdlib) | — | `docker compose up/down/stop/pause` subprocess calls | Same pattern as `execSSMSession`, `execECSCommand` |
| `aws-sdk-go-v2/service/iam` | already in go.mod | Create sidecar IAM role | Already used in create.go |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `docker compose` V2 CLI subprocess | Docker SDK for Go | SDK adds a dependency, subprocess is consistent with how the codebase already shells out to `aws`, `terraform`, `terragrunt` |
| tmpfs shared credentials volume | `.env` file with static creds | `.env` file requires disk secrets; tmpfs keeps them in memory, consistent with security model |

**Installation:** No new Go dependencies required. Docker Desktop (or Docker Engine + Compose V2) must be present on the operator's machine — documented, not checked at compile time.

---

## Architecture Patterns

### Recommended Project Structure

New files follow the compiler's existing sub-file pattern:

```
pkg/compiler/
├── compiler.go          # existing — add "docker" case to Compile()
├── compose.go           # NEW — compileDocker() + generateDockerCompose()
├── compose_test.go      # NEW — unit tests for compose generation
└── testdata/
    ├── docker-basic.yaml  # NEW — minimal docker profile
    └── docker-with-budget.yaml  # NEW — docker + budget profile

internal/app/cmd/
├── create.go            # existing — add runCreateDocker() or inline case "docker":
└── destroy.go           # existing — add case "docker": for runDestroyDocker()
```

No Terraform modules or Terragrunt HCL. No `infra/live/` changes.

### Pattern 1: Compile() Dispatcher Extension

**What:** Add `"docker"` case to the `switch substrate` in `compiler.Compile()`.
**When to use:** Consistent with how `"ecs"` was added alongside `"ec2"`.

```go
// Source: pkg/compiler/compiler.go (existing pattern — ECS was added this way)
case "docker":
    return compileDocker(p, sandboxID, network)
```

`compileDocker()` returns a `*CompiledArtifacts` where only `DockerComposeYAML` is populated (new field), all Terragrunt fields are empty strings.

### Pattern 2: DockerComposeYAML Field in CompiledArtifacts

**What:** Add a single new field to `CompiledArtifacts`.
**When to use:** Keeps the compiler's output struct as the canonical artifact carrier — same as how `ServiceHCL` carries EC2/ECS output.

```go
// Source: pkg/compiler/compiler.go
type CompiledArtifacts struct {
    // ... existing fields ...

    // DockerComposeYAML is the generated docker-compose.yml content.
    // Only populated when substrate == "docker". Empty for EC2/ECS.
    DockerComposeYAML string
}
```

### Pattern 3: Template-Based Compose Generation

**What:** `generateDockerCompose()` uses `text/template` to produce the `docker-compose.yml`.
**When to use:** Mirrors `ec2ServiceHCLTemplate` and `ecsServiceHCLTemplate` in `service_hcl.go` exactly — Go's `text/template` (not `html/template`) for non-HTML output.

```go
// Source: pkg/compiler/compose.go (new, mirrors service_hcl.go pattern)
const dockerComposeTemplate = `# Generated by km for sandbox {{ .SandboxID }}
# DO NOT EDIT — managed by km create/destroy
services:
  main:
    image: {{ .MainImage }}
    ...
`
```

### Pattern 4: create.go Substrate Branch

**What:** `runCreate()` branches after `compiler.Compile()` on `substrate == "docker"`.
**When to use:** The Docker path skips all Terragrunt code — no `runner.Apply()`, no `terragrunt.CreateSandboxDir()`.

```go
// Source: internal/app/cmd/create.go (extend existing switch logic)
if substrate == "docker" {
    return runCreateDocker(ctx, cfg, awsCfg, resolvedProfile, sandboxID, artifacts, verbose)
}
// ... existing EC2/ECS Terragrunt path follows ...
```

`runCreateDocker()`:
1. Calls `sts.AssumeRole` for the sandbox role (or creates the role inline — see pitfall 4).
2. Calls `sts.AssumeRole` for the sidecar role.
3. Writes `~/.km/sandboxes/{id}/docker-compose.yml` from `artifacts.DockerComposeYAML`.
4. Writes credentials to tmpfs credential volume config.
5. Runs `docker compose up -d`.
6. Writes S3 metadata (same `awspkg.WriteMetadata()` call as EC2/ECS).
7. Writes `.km-ttl` for local TTL watcher.

### Pattern 5: S3 Metadata Written Same Way

**What:** Docker sandboxes write the same `metadata.json` to S3 as EC2/ECS.
**When to use:** Ensures `km list`, `km status`, `km otel` work without substrate-specific code — they read from S3.

The `SandboxRecord.Substrate` field stores `"docker"` so `km shell` and `km destroy` can branch.

### Pattern 6: shell.go Docker Dispatch

**What:** `runShell()` adds `case "docker":` that runs `docker exec`.
**When to use:** Consistent with `case "ec2":` (SSM) and `case "ecs":` (ECS Exec).

```go
// Source: internal/app/cmd/shell.go (extend existing switch)
case "docker":
    return execDockerShell(ctx, sandboxID, root, execFn)
```

```go
func execDockerShell(ctx context.Context, sandboxID string, root bool, execFn ShellExecFunc) error {
    containerName := fmt.Sprintf("km-%s-main", sandboxID)
    args := []string{"exec", "-it"}
    if root {
        args = append(args, "-u", "root")
    }
    args = append(args, containerName, "/bin/bash")
    c := exec.CommandContext(ctx, "docker", args...)
    c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
    return execFn(c)
}
```

### Pattern 7: destroy.go Docker Dispatch

**What:** `runDestroy()` detects docker substrate from S3 metadata and runs `docker compose down -v`.
**When to use:** The Docker path skips Terragrunt destroy and EventBridge schedule cancellation (no Lambda TTL for Docker).

Key: `findSandboxDir()` must also look in `~/.km/sandboxes/{id}/` for the compose file.

### Pattern 8: IAM Role Strategy for Docker

**What:** The DESCRIPTION.md specifies two IAM roles: a sandbox role and a sidecar role. The sandbox role trusts the operator's AWS principal (via STS) in addition to `ec2.amazonaws.com`.
**When to use:** The EC2/ECS IAM module already creates the sandbox role — for Docker, this same module is reused with an additional trust statement, OR the Docker path creates roles via the AWS SDK directly (without Terraform).

**Recommended approach for v1:** Create sandbox and sidecar IAM roles directly via the AWS SDK in `runCreateDocker()`. This avoids a partial Terraform apply just for IAM. The roles are tagged with `km:sandbox-id` and cleaned up during `runDestroyDocker()` via IAM SDK calls.

This is analogous to how the budget Lambda's `perSandboxLambda` logic creates per-sandbox resources at create time.

### Pattern 9: Network Configuration — Docker Does Not Need VPC

**What:** `NetworkConfig` carries VPC/subnet info for EC2/ECS. Docker needs none of this. Pass a minimal `NetworkConfig` with just `EmailDomain` and `ArtifactsBucket`.
**When to use:** The `create.go` network loading step (`LoadNetworkOutputs()`) must be skipped or made optional for the docker path, since there may not be a VPC module deployed.

```go
// For docker substrate, skip LoadNetworkOutputs()
if substrate == "docker" {
    network = &compiler.NetworkConfig{
        EmailDomain:     "sandboxes." + networkDomain,
        ArtifactsBucket: artifactsBucket,
    }
} else {
    // existing path: LoadNetworkOutputs()
}
```

### Pattern 10: Sandbox Directory for Docker

**What:** Docker sandbox artifacts live at `~/.km/sandboxes/{id}/` (not in the repo's `infra/live/` tree like Terragrunt sandboxes).
**When to use:** Always for docker substrate.

```
~/.km/sandboxes/sb-a1b2c3d4/
├── docker-compose.yml     # generated
├── .km-ttl                # expiry timestamp
└── metadata.json          # copy (real source is S3)
```

`destroy.go` must know to look here, not in `infra/live/`.

### Anti-Patterns to Avoid

- **Running Terraform at all for docker substrate:** No VPC, no security groups, no EC2 spot request. The Docker substrate's security model is proxy-only (acknowledged trade-off in DESCRIPTION.md).
- **Storing operator credentials in the docker-compose.yml:** Only the `km-cred-refresh` container gets AssumeRole-scoped operator credentials, and those are in a separate env section that the cred-refresh container consumes — never in the main service's `environment:` block.
- **Blocking on TTL in the foreground:** The local TTL watcher must be a detached background process or a short-lived process that writes a PID file and exits, not a goroutine in the `km create` process (which exits after provisioning).
- **Using docker compose V1 syntax (`docker-compose` hyphenated):** Use `docker compose` (space, V2 plugin). The Makefile already uses this for sidecar builds — stay consistent.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| YAML generation for docker-compose | Custom string builder | `text/template` | Existing pattern; handles escaping, whitespace, conditionals |
| Credential refresh logic | Custom STS loop | AWS SDK standard credential file format (`[profile]` sections) + cred-refresh shell script | AWS SDK reads `AWS_SHARED_CREDENTIALS_FILE` natively; no custom provider needed |
| Container health checking | Polling loop in Go | `docker compose ps --format json` + `docker inspect` | `docker inspect` returns structured JSON; no need to parse custom output |
| S3 metadata write | New code | Existing `awspkg.WriteMetadata()` | Already handles the `metadata.json` format `km list`/`km status` read |
| IAM policy compilation | Custom JSON builder | Existing `compileIAMPolicy()` + `compileSGRules()` | These return structured Go types already wired to the EC2/ECS paths |

**Key insight:** The Docker substrate is intentionally "dumb infrastructure" — no Terraform, no EventBridge, no Lambda. The value comes from reusing the existing sidecar images, S3 metadata, and entrypoint.sh unchanged.

---

## Common Pitfalls

### Pitfall 1: `docker compose` vs `docker-compose`
**What goes wrong:** `docker-compose` (hyphenated, V1, Python) may not be installed or may behave differently from `docker compose` (V2 plugin).
**Why it happens:** Older systems have V1; V2 ships as a Docker Desktop plugin.
**How to avoid:** Always invoke `docker` + `compose` as two args: `exec.Command("docker", "compose", "up", "-d", ...)`. Document V2 as a prerequisite.
**Warning signs:** `docker-compose: command not found` or V2 features not working.

### Pitfall 2: Static IP Assignment on Mac vs Linux
**What goes wrong:** Docker bridge networks with static IPs (`ipv4_address: 172.20.0.10`) work on Linux but require the `bridge` driver with explicit `ipam` config on macOS via Docker Desktop.
**Why it happens:** Docker Desktop on macOS uses a Linux VM for the network stack; subnet conflicts with existing Docker networks cause silent failures.
**How to avoid:** Always specify both `driver: bridge` and the full `ipam.config` block with a non-conflicting subnet (e.g. `172.30.0.0/24` — avoid `172.17.0.0/16` which is Docker's default bridge). Check for subnet conflicts at create time: `docker network ls`.
**Warning signs:** DNS proxy container fails to start with network bind error.

### Pitfall 3: tmpfs Volume on macOS
**What goes wrong:** Docker Desktop on macOS does not support `driver_opts.type: tmpfs` for named volumes (it's Linux-specific).
**Why it happens:** macOS Docker runs containers in a Linux VM, but named volume tmpfs is not exposed via the desktop's volume API.
**How to avoid:** Use a regular Docker named volume (not tmpfs) for the credentials file: `cred-vol:` with no `driver_opts`. Credentials are in the Docker VM's overlay FS, not operator's macOS disk. This is acceptable for a local dev substrate. Add a comment in the generated compose file explaining this.
**Warning signs:** `docker compose up` fails with `invalid volume spec`.

### Pitfall 4: IAM Role Creation Before AssumeRole
**What goes wrong:** Creating an IAM role and immediately calling `sts:AssumeRole` fails with `InvalidClientTokenId` or `NoSuchEntity` because IAM changes take ~10 seconds to propagate globally.
**Why it happens:** IAM is eventually consistent across global endpoints.
**How to avoid:** After creating IAM roles, poll `iam.GetRole()` until the role is available, then wait an additional 5 seconds before calling `sts:AssumeRole`. Or reuse an existing per-operator "docker sandbox" IAM role and just issue a session-scoped policy at AssumeRole time (avoids the creation latency on every sandbox).
**Warning signs:** `create.go` step succeeds but `sts:AssumeRole` returns `NoSuchEntity`.

### Pitfall 5: Network Validation in `create.go` for Docker
**What goes wrong:** `runCreate()` calls `LoadNetworkOutputs()` before the substrate check — this fails for docker because there are no Terragrunt network outputs (the operator may not have run `km init --region`).
**Why it happens:** The existing code path assumes EC2/ECS which always needs VPC outputs.
**How to avoid:** Move the substrate check earlier in `runCreate()` and short-circuit the `LoadNetworkOutputs()` call for docker. The `NetworkConfig` for docker only needs `EmailDomain` and `ArtifactsBucket`.
**Warning signs:** `km create --substrate docker` fails with `network not initialized for region`.

### Pitfall 6: Schema Validation Rejects `substrate: docker`
**What goes wrong:** `km validate` fails because the JSON schema `enum: ["ec2", "ecs"]` rejects `docker`.
**Why it happens:** Two places gate the substrate: the JSON schema (`schemas/sandbox_profile.schema.json`) and the Go semantic validator (`pkg/profile/validate.go` Rule 2).
**How to avoid:** Update BOTH: (1) add `"docker"` to the schema `enum`, (2) update the Go validator's condition `substrate != "ec2" && substrate != "ecs"` to also allow `"docker"`. The test `validate_test.go:190` (line 183 in the grep output) already asserts that `docker` is invalid — that test must be updated.
**Warning signs:** `km validate profiles/claude-dev.yaml --substrate docker` returns substrate error.

### Pitfall 7: `km destroy` Looking for Terragrunt State
**What goes wrong:** `runDestroy()` tries to discover the sandbox via `awspkg.FindSandboxByID()` (tag-based AWS lookup) and run `runner.Output()` before `docker compose down`. Docker sandboxes have no AWS-tagged resources (no VPC, no EC2, no ECS cluster).
**Why it happens:** The destroy path assumes tagged AWS resources exist for every sandbox.
**How to avoid:** Check S3 metadata for `substrate == "docker"` early in `runDestroy()` and route to `runDestroyDocker()`. The docker destroy path: (1) read compose file from `~/.km/sandboxes/{id}/`, (2) `docker compose down -v`, (3) delete IAM roles via SDK, (4) delete S3 metadata, (5) remove `~/.km/sandboxes/{id}/`.
**Warning signs:** `km destroy sb-xxxxx` fails with "no AWS resources tagged".

### Pitfall 8: TTL Enforcement Across `km create` Process Exit
**What goes wrong:** A goroutine-based TTL watcher dies when the `km create` process exits.
**Why it happens:** Go goroutines are process-scoped; `km create` exits after provisioning.
**How to avoid:** Write the TTL expiry to `~/.km/sandboxes/{id}/.km-ttl` (ISO8601 timestamp). A separate `km ttl-watcher` command (or a small shell script) runs as a detached process: `nohup km ttl-watch {id} &`. `km create` spawns it with `cmd.Start()` (not `cmd.Run()`) so it outlives the parent. Alternatively, print a cron-install hint to the operator at create time for v1.

---

## Code Examples

### Extending the Compile() dispatcher
```go
// Source: pkg/compiler/compiler.go — mirror the ecs case added in Phase 2
func Compile(p *profile.SandboxProfile, sandboxID string, onDemand bool, network *NetworkConfig) (*CompiledArtifacts, error) {
    switch p.Spec.Runtime.Substrate {
    case "ec2":
        return compileEC2(p, sandboxID, onDemand, network)
    case "ecs":
        return compileECS(p, sandboxID, onDemand, network)
    case "docker":
        return compileDocker(p, sandboxID, network)
    default:
        return nil, fmt.Errorf("unknown substrate %q: must be \"ec2\", \"ecs\", or \"docker\"", p.Spec.Runtime.Substrate)
    }
}
```

### compileDocker() skeleton
```go
// Source: pkg/compiler/compose.go (new file)
func compileDocker(p *profile.SandboxProfile, sandboxID string, network *NetworkConfig) (*CompiledArtifacts, error) {
    secretPaths := compileSecrets(p)
    iamPolicy := compileIAMPolicy(p)  // reused for session duration / region lock

    composeYAML, err := generateDockerCompose(p, sandboxID, network, secretPaths)
    if err != nil {
        return nil, fmt.Errorf("generate docker-compose.yml: %w", err)
    }

    return &CompiledArtifacts{
        SandboxID:         sandboxID,
        DockerComposeYAML: composeYAML,
        SecretPaths:       secretPaths,
        IAMPolicy:         iamPolicy,
        // SGEgressRules: nil — no Security Groups for docker substrate
        // ServiceHCL: ""    — no Terragrunt HCL
        // UserData: ""       — entrypoint.sh handles boot from env vars
    }, nil
}
```

### docker exec for km shell
```go
// Source: internal/app/cmd/shell.go — add after case "ecs":
case "docker":
    return execDockerShell(ctx, sandboxID, root, execFn)
```

```go
func execDockerShell(ctx context.Context, sandboxID string, root bool, execFn ShellExecFunc) error {
    // Container naming: km-{sandbox-id}-main (matches generated compose)
    containerName := fmt.Sprintf("km-%s-main", sandboxID)
    args := []string{"exec", "-it"}
    if root {
        args = append(args, "-u", "root")
    }
    args = append(args, containerName, "/bin/bash")
    c := exec.CommandContext(ctx, "docker", args...)
    c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
    return execFn(c)
}
```

### Schema enum update (two-place change)
```json
// Source: schemas/sandbox_profile.schema.json
"substrate": {
  "type": "string",
  "enum": ["ec2", "ecs", "docker"],
  "description": "Compute backend: ec2, ecs (Fargate), or docker (local)"
}
```

```go
// Source: pkg/profile/validate.go Rule 2
if substrate != "" && substrate != "ec2" && substrate != "ecs" && substrate != "docker" {
    errs = append(errs, ValidationError{
        Path:    "spec.runtime.substrate",
        Message: fmt.Sprintf("substrate %q is not supported; must be one of: ec2, ecs, docker", substrate),
    })
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `docker-compose` V1 (Python, hyphenated) | `docker compose` V2 (plugin) | Docker Desktop 3.x+ | Use `docker` + `compose` as separate args; V2 is GA standard |
| Per-container IAM credentials as env vars | Shared credentials file via tmpfs volume | This phase | Single cred-refresh sidecar; operator creds isolated |
| Static compose file in repo | Generated compose from profile compiler | This phase | Same pattern as `service.hcl` — profile-driven |

---

## Open Questions

1. **IAM role creation strategy: SDK vs re-use**
   - What we know: EC2/ECS create roles via Terraform modules. Docker skips Terraform.
   - What's unclear: Should each docker sandbox get its own IAM role (created via SDK, tagged, deleted on destroy), or should there be a single reusable "km-docker-sandbox" role with session policies?
   - Recommendation: Per-sandbox roles via SDK is cleanest and consistent with the existing pattern. Session duration from `roleSessionDuration`. The IAM propagation delay (Pitfall 4) is a known issue — a 10s retry with backoff is sufficient.

2. **TTL watcher implementation depth for v1**
   - What we know: EC2/ECS use EventBridge + Lambda. Docker has no cloud scheduler.
   - What's unclear: How much TTL enforcement is required for the v1 cut of this phase vs a printed reminder?
   - Recommendation: Write `.km-ttl` file and spawn a detached `km ttl-watch {id}` background process at create time. If `km ttl-watch` is too much work for v1, print a clear TTL expiry time to stdout and document manual destroy. The DESCRIPTION.md marks this as "Option 1 for v1."

3. **Subnet conflict detection**
   - What we know: Static IP `172.20.0.0/24` is specified in DESCRIPTION.md.
   - What's unclear: Will this conflict with other Docker networks on the operator's machine?
   - Recommendation: In `runCreateDocker()`, call `docker network ls --format json` and check for `172.20.0.0/24` overlap. If conflict, generate a non-overlapping subnet from a configurable pool (e.g. increment the third octet: `172.20.{N}.0/24`).

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (`go test`) |
| Config file | none (standard Go test runner) |
| Quick run command | `go test ./pkg/compiler/... -run TestDocker -v` |
| Full suite command | `go test ./pkg/compiler/... ./internal/app/cmd/... ./pkg/profile/... -v` |

### Phase Requirements → Test Map

No formal requirement IDs were assigned to this phase. Tests derived from DESCRIPTION.md testing section:

| Behavior | Test Type | Automated Command |
|----------|-----------|-------------------|
| `km validate` accepts `substrate: docker` | unit | `go test ./pkg/profile/... -run TestValidate.*Docker` |
| `compileDocker()` produces valid compose YAML | unit | `go test ./pkg/compiler/... -run TestCompileDocker` |
| Compose YAML contains all 5 service entries | unit | `go test ./pkg/compiler/... -run TestDockerComposeContainers` |
| DNS proxy gets static IP in compose YAML | unit | `go test ./pkg/compiler/... -run TestDockerComposeDNS` |
| Budget fields included when profile has budget | unit | `go test ./pkg/compiler/... -run TestDockerComposeWithBudget` |
| `shell.go` routes `substrate=docker` to `docker exec` | unit | `go test ./internal/app/cmd/... -run TestShellDocker` |
| `destroy.go` routes `substrate=docker` without Terragrunt | unit | `go test ./internal/app/cmd/... -run TestDestroyDocker` |
| Credential refresh sidecar is the only container with operator creds | unit | `go test ./pkg/compiler/... -run TestDockerComposeCredIsolation` |

### Sampling Rate
- **Per task commit:** `go test ./pkg/compiler/... -run TestDocker -v`
- **Per wave merge:** `go test ./pkg/compiler/... ./internal/app/cmd/... ./pkg/profile/... -v`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/compiler/compose_test.go` — covers `TestCompileDocker*` (new file alongside `compose.go`)
- [ ] `pkg/compiler/testdata/docker-basic.yaml` — minimal docker profile for compiler tests
- [ ] `pkg/compiler/testdata/docker-with-budget.yaml` — docker + budget for budget field tests
- [ ] Update `pkg/profile/validate_test.go:190` — change assertion that `docker` is invalid to assert it is now valid

---

## Sources

### Primary (HIGH confidence)
- Codebase analysis: `pkg/compiler/compiler.go` — confirmed Compile() dispatcher pattern
- Codebase analysis: `pkg/compiler/service_hcl.go` — confirmed text/template pattern and NetworkConfig struct
- Codebase analysis: `internal/app/cmd/shell.go` — confirmed substrate switch pattern for km shell
- Codebase analysis: `internal/app/cmd/destroy.go` — confirmed destroy flow and S3-first lookup
- Codebase analysis: `pkg/profile/validate.go:230-238` — confirmed exact lines to change for substrate validation
- Codebase analysis: `schemas/sandbox_profile.schema.json:170-173` — confirmed enum location
- Codebase analysis: `containers/sandbox/entrypoint.sh` — confirmed env var contract (all needed vars already defined)
- Phase 36 DESCRIPTION.md — base image is already built and pushed by `km init`; entrypoint.sh is production-ready

### Secondary (MEDIUM confidence)
- Docker Compose V2 spec: `driver_opts.type: tmpfs` on named volumes is Linux-only — macOS Docker Desktop does not support it for named volumes (confirmed by community knowledge, macOS Docker Desktop behavior)
- IAM eventual consistency: ~10s propagation delay for `sts:AssumeRole` after role creation — well-known AWS behavior

### Tertiary (LOW confidence)
- Subnet conflict avoidance (`172.20.0.0/24`) — based on common Docker subnet allocation; operator-specific conflicts are possible

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all Go stdlib, existing patterns, no new deps
- Architecture: HIGH — all patterns directly extrapolated from existing `ec2`/`ecs` substrate additions
- Pitfalls: HIGH for schema/validator (confirmed in source); MEDIUM for macOS Docker behavior; LOW for subnet conflicts (environment-specific)

**Research date:** 2026-03-30
**Valid until:** 2026-04-30 (stable Go stdlib and Docker Compose V2 spec)
