---
phase: 37-docker-compose-local-substrate
verified: 2026-03-30T00:00:00Z
status: passed
score: 15/15 must-haves verified
re_verification: false
---

# Phase 37: Docker Compose Local Substrate Verification Report

**Phase Goal:** `km create --substrate docker` provisions a local sandbox using Docker Compose with the same 5-container topology (main + 4 sidecars), connected to the existing AWS platform — SSM for secrets, SES for email, DynamoDB for budget tracking, S3 for artifacts/OTEL. Same enforcement as EC2, faster iteration (~5s up vs ~60s), runs on the operator's laptop
**Verified:** 2026-03-30
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| #  | Truth                                                                                          | Status     | Evidence                                                                                        |
|----|-----------------------------------------------------------------------------------------------|------------|-------------------------------------------------------------------------------------------------|
| 1  | `km validate` accepts `substrate: docker` in a profile YAML                                  | ✓ VERIFIED | `validate.go:232` accepts ec2/ecs/docker; embedded schema `enum:["ec2","ecs","docker"]`; `TestValidateDockerSubstrate` passes |
| 2  | `compileDocker()` produces docker-compose.yml with all 6 services                            | ✓ VERIFIED | `compose.go:29` defines all 6 services in template; `TestDockerComposeContainers` passes       |
| 3  | Credential isolation: only `km-cred-refresh` has operator creds; others use shared creds file | ✓ VERIFIED | Template line 176–178: AWS_ACCESS_KEY_ID only in km-cred-refresh; all others use AWS_SHARED_CREDENTIALS_FILE; `TestDockerComposeCredIsolation` passes |
| 4  | DNS proxy gets static IP 172.20.0.10; main container uses `dns: [172.20.0.10]`               | ✓ VERIFIED | Template lines 83/115: `dns: [172.20.0.10]` on main; `ipv4_address: 172.20.0.10` on dns-proxy; `TestDockerComposeDNS` passes |
| 5  | Budget fields conditionally included in http-proxy when profile has budget section             | ✓ VERIFIED | Template lines 129–132: `{{- if .BudgetEnabled }}` block; `TestDockerComposeWithBudget` passes |
| 6  | `km create` with docker substrate skips Terragrunt, skips LoadNetworkOutputs, runs docker compose up -d | ✓ VERIFIED | `create.go:253–308`: docker NetworkConfig from cfg fields, early dispatch before AZ retry loop; `TestCreateDockerSubstrateRouting` passes |
| 7  | `km create` docker writes S3 metadata with `substrate=docker`                                | ✓ VERIFIED | `create.go:1160`: `Substrate: "docker"` in WriteMetadata call; `TestCreateDockerSubstrateRouting` confirms routing |
| 8  | `km create` docker writes docker-compose.yml to `~/.km/sandboxes/{id}/`                     | ✓ VERIFIED | `create.go:1099–1104`: writes to `sandboxLocalDir/docker-compose.yml`; `TestCreateDockerWritesComposeFile` passes |
| 9  | `km create` docker creates per-sandbox IAM roles via SDK (not Terraform)                     | ✓ VERIFIED | `create.go:914–960`: `iamClient.CreateRole()` for km-docker-{id}-{region} and km-sidecar-{id}-{region} |
| 10 | `km destroy` with docker substrate runs docker compose down -v, cleans up, no Terragrunt     | ✓ VERIFIED | `destroy.go:149–150`: routes on `meta.Substrate=="docker"`; `runDestroyDocker()` runs compose down -v; `TestDestroyDockerSubstrateRouting` and `TestDestroyDockerIdempotent` pass |
| 11 | `km shell` routes docker substrate to `docker exec -it km-{id}-main /bin/bash`              | ✓ VERIFIED | `shell.go:175–176`: case "docker" calls `execDockerShell()`; `TestShellDockerContainerName` confirms args |
| 12 | `km shell --root` on docker passes `-u root` to docker exec                                  | ✓ VERIFIED | `shell.go:385–397`: `-u root` appended when root=true; `TestShellDockerRootFlag` passes        |
| 13 | `km stop` routes docker substrate to `docker compose stop`                                   | ✓ VERIFIED | `stop.go:78–82`: S3 metadata check + `runDockerCompose(ctx, sandboxID, "stop")`; `TestStopDockerRouting` passes |
| 14 | `km pause` routes docker substrate to `docker compose pause`                                 | ✓ VERIFIED | `pause.go:85–89`: S3 metadata check + `runDockerCompose(ctx, sandboxID, "pause")`; `TestPauseDockerRouting` passes |
| 15 | `km roll proxies` skips docker substrate sandboxes with a warning                            | ✓ VERIFIED | `roll.go:562–564`: case "docker" in substrate switch with skip message                         |

**Score:** 15/15 truths verified

### Required Artifacts

| Artifact                                              | Expected                                   | Status     | Details                              |
|-------------------------------------------------------|--------------------------------------------|------------|--------------------------------------|
| `pkg/compiler/compose.go`                             | compileDocker() + generateDockerCompose()  | ✓ VERIFIED | 284 lines, substantive template impl |
| `pkg/compiler/compose_test.go`                        | Unit tests for compose generation          | ✓ VERIFIED | 158 lines, 6 tests all passing       |
| `pkg/compiler/testdata/docker-basic.yaml`             | Minimal docker profile fixture             | ✓ VERIFIED | Exists                               |
| `pkg/compiler/testdata/docker-with-budget.yaml`       | Docker profile with budget fixture         | ✓ VERIFIED | Exists                               |
| `internal/app/cmd/create.go`                          | runCreateDocker() + docker branch          | ✓ VERIFIED | Contains runCreateDocker at line 884 |
| `internal/app/cmd/destroy.go`                         | runDestroyDocker() + docker branch         | ✓ VERIFIED | Contains runDestroyDocker at line 444|
| `internal/app/cmd/docker_helpers.go`                  | dockerComposePath() + runDockerCompose()   | ✓ VERIFIED | Both functions present               |
| `internal/app/cmd/create_docker_test.go`              | TestCreateDocker* tests                    | ✓ VERIFIED | 3 tests all passing                  |
| `internal/app/cmd/destroy_docker_test.go`             | TestDestroyDocker* tests                   | ✓ VERIFIED | 4 tests all passing                  |
| `internal/app/cmd/shell.go`                           | case docker + execDockerShell()            | ✓ VERIFIED | Both present at lines 175, 382       |
| `internal/app/cmd/stop.go`                            | docker compose stop path                   | ✓ VERIFIED | S3 metadata routing at line 78       |
| `internal/app/cmd/pause.go`                           | docker compose pause path                  | ✓ VERIFIED | S3 metadata routing at line 85       |
| `internal/app/cmd/shell_docker_test.go`               | TestShellDocker* tests                     | ✓ VERIFIED | 4 tests all passing                  |
| `internal/app/cmd/stop_pause_docker_test.go`          | TestStopDocker/TestPauseDocker* tests      | ✓ VERIFIED | 4 tests all passing                  |

### Key Link Verification

| From                              | To                              | Via                                      | Status     | Details                                                        |
|-----------------------------------|---------------------------------|------------------------------------------|------------|----------------------------------------------------------------|
| `pkg/compiler/compiler.go`        | `pkg/compiler/compose.go`       | `case "docker":` in Compile() switch     | ✓ WIRED    | Line 89: `case "docker": return compileDocker(...)`            |
| `pkg/profile/validate.go`         | schemas (embedded)              | Both accept docker as valid substrate    | ✓ WIRED    | validate.go:232 + embedded schema enum["ec2","ecs","docker"]   |
| `internal/app/cmd/create.go`      | `pkg/compiler/compose.go`       | DockerComposeYAML field consumed         | ✓ WIRED    | Line 1088: `composeYAML := artifacts.DockerComposeYAML`        |
| `internal/app/cmd/create.go`      | `pkg/aws` (WriteMetadata)       | S3 metadata written with substrate=docker| ✓ WIRED    | Line 1160: `Substrate: "docker"` in metadata struct            |
| `internal/app/cmd/destroy.go`     | S3 metadata                     | Reads substrate to route to docker path  | ✓ WIRED    | Line 149: `if meta.Substrate == "docker"`                      |
| `internal/app/cmd/shell.go`       | docker exec                     | os/exec.Command with docker exec args    | ✓ WIRED    | execDockerShell: exec.CommandContext(ctx, "docker", args...)   |
| `internal/app/cmd/stop.go`        | `docker_helpers.go`             | runDockerCompose("stop")                 | ✓ WIRED    | Line 79: `runDockerCompose(ctx, sandboxID, "stop")`            |
| `internal/app/cmd/pause.go`       | `docker_helpers.go`             | runDockerCompose("pause")                | ✓ WIRED    | Line 86: `runDockerCompose(ctx, sandboxID, "pause")`           |

### Requirements Coverage

No requirement IDs were declared in any plan's frontmatter for this phase (`requirements: []` in all three plans). No REQUIREMENTS.md entries are mapped to phase 37. Requirements coverage: N/A.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| None found | — | — | — | — |

Note: PLACEHOLDER_* strings in `compose.go` are intentional design sentinels replaced at runtime by `create.go:1089–1093` via `strings.ReplaceAll`. Not a stub — by design.

### Human Verification Required

#### 1. End-to-end `km create --substrate docker` on operator laptop

**Test:** Run `km create testdata/profiles/valid-docker-substrate.yaml --substrate docker` (or a profile with `substrate: docker`) against a real AWS account.
**Expected:** IAM roles created, scoped credentials generated, `~/.km/sandboxes/{id}/docker-compose.yml` written, `docker compose up -d` starts 6 containers (main + 4 sidecars + cred-refresh), S3 metadata shows substrate=docker.
**Why human:** Requires live AWS account with sufficient IAM permissions, Docker Desktop running, and valid km-config.yaml. Cannot verify AWS API calls or container startup in unit tests.

#### 2. Container isolation enforcement verified at runtime

**Test:** Inside a running docker sandbox, attempt to make an outbound DNS/HTTP request to an unauthorized domain (not in profile allowlist).
**Expected:** DNS proxy blocks unauthorized DNS lookups; HTTP proxy blocks unauthorized HTTPS connections. Same enforcement as EC2 substrate.
**Why human:** Requires running containers and network traffic testing. Cannot be verified programmatically via code inspection.

#### 3. `km shell {docker-sandbox-id}` attaches interactive terminal

**Test:** Run `km shell {sandbox-id}` for a running docker sandbox.
**Expected:** Interactive bash session inside the `km-{id}-main` container opens in the terminal.
**Why human:** Interactive TTY behavior cannot be verified from code inspection; requires a running container and terminal.

### Gaps Summary

No gaps found. All 15 observable truths are verified, all artifacts exist and are substantive, all key links are wired, the project builds cleanly, and all 14 docker-specific unit tests pass (6 compiler + 3 create + 4 destroy/helper + 4 shell + 4 stop/pause = 21 tests total via multiple test files, all PASS). The full test suite passes for all three affected packages.

---

_Verified: 2026-03-30_
_Verifier: Claude (gsd-verifier)_
