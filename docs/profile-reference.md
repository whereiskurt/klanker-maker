# SandboxProfile YAML Reference

Complete schema reference for the `SandboxProfile` YAML format used by the `km` CLI.

SandboxProfiles follow a Kubernetes-style `apiVersion`/`kind`/`metadata`/`spec` structure. They are validated against a JSON Schema (Draft 2020-12) and additional semantic rules at parse time.

---

## Document Structure

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: my-profile
  labels:
    tier: development
extends: hardened          # optional parent profile
spec:
  lifecycle: { ... }
  runtime: { ... }
  execution: { ... }
  sourceAccess: { ... }
  network: { ... }
  identity: { ... }
  sidecars: { ... }
  observability: { ... }
  agent: { ... }
  artifacts: { ... }       # optional
  budget: { ... }          # optional
  email: { ... }           # optional
  otp: { ... }             # optional
  cli: { ... }             # optional
```

---

## Duration Format

Duration fields accept Go-style duration strings with an extension for days:

| Suffix | Meaning  | Examples               |
|--------|----------|------------------------|
| `s`    | seconds  | `30s`, `90s`           |
| `m`    | minutes  | `15m`, `30m`           |
| `h`    | hours    | `1h`, `4h`, `24h`     |
| `d`    | days     | `1d`, `7d`             |

Each duration value must match the pattern `^[0-9]+(s|m|h|d)$` (a single integer followed by one unit suffix). Compound durations like `4h30m` are **not** supported by the schema regex.

---

## Top-Level Fields

### `apiVersion`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `apiVersion`                   |
| Type       | string                         |
| Required   | Yes                            |
| Default    | --                             |
| Validation | Must be exactly `klankermaker.ai/v1alpha1` |

The API version of the SandboxProfile resource. Currently only `v1alpha1` is supported.

```yaml
apiVersion: klankermaker.ai/v1alpha1
```

### `kind`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `kind`                         |
| Type       | string                         |
| Required   | Yes                            |
| Default    | --                             |
| Validation | Must be exactly `SandboxProfile` |

The resource kind. Always `SandboxProfile`.

```yaml
kind: SandboxProfile
```

### `extends`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `extends`                      |
| Type       | string                         |
| Required   | No                             |
| Default    | -- (no parent)                 |
| Validation | Must reference an existing profile name; max inheritance depth is 3; cycles are rejected |

Name of a parent profile to inherit from. See [Profile Inheritance](#profile-inheritance) for merge rules.

```yaml
extends: hardened
```

---

## `metadata`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `metadata`                     |
| Type       | object                         |
| Required   | Yes                            |
| Validation | No additional properties allowed |

### `metadata.name`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `metadata.name`                |
| Type       | string                         |
| Required   | Yes                            |
| Default    | --                             |
| Validation | `minLength: 1`                 |

Unique name for this profile. Used in `km create <name>` and as the `extends` target.

```yaml
metadata:
  name: my-custom-profile
```

### `metadata.labels`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `metadata.labels`              |
| Type       | map[string]string              |
| Required   | No                             |
| Default    | -- (empty)                     |
| Validation | All values must be strings     |

Arbitrary key-value labels for organization and filtering. During inheritance, labels are the **one exception** to the replacement rule -- they are merged additively (child labels override same-key parent labels, but parent-only labels are preserved).

```yaml
metadata:
  labels:
    tier: development
    builtin: "true"
```

### `metadata.prefix`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `metadata.prefix`              |
| Type       | string                         |
| Required   | No                             |
| Default    | `"sb"` (generates `sb-{8hex}`) |
| Validation | Pattern `^[a-z][a-z0-9]{0,11}$` (lowercase alphanumeric, 1-12 chars, starts with letter) |

Custom prefix for the sandbox ID. Replaces the default `sb-` prefix.

```yaml
metadata:
  prefix: goose    # generates goose-{8hex}
```

---

## `spec.lifecycle`

Controls sandbox lifetime and teardown behavior.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.lifecycle`               |
| Type       | object                         |
| Required   | Yes                            |

### `spec.lifecycle.ttl`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.lifecycle.ttl`           |
| Type       | duration string                |
| Required   | Yes                            |
| Default    | --                             |
| Validation | Pattern `^(0\|[0-9]+(s\|m\|h\|d))$`; must be >= `idleTimeout` (semantic rule). Use `"0"` to disable auto-destroy. |

Maximum lifetime of the sandbox. When the TTL expires, the `teardownPolicy` is applied. Set to `"0"` to disable automatic expiration.

```yaml
spec:
  lifecycle:
    ttl: "24h"
```

### `spec.lifecycle.idleTimeout`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.lifecycle.idleTimeout`   |
| Type       | duration string                |
| Required   | Yes                            |
| Default    | --                             |
| Validation | Pattern `^[0-9]+(s\|m\|h\|d)$`; must be <= `ttl` (semantic rule) |

Duration after which an idle sandbox (no active tasks or connections) is torn down.

```yaml
spec:
  lifecycle:
    idleTimeout: "4h"
```

### `spec.lifecycle.teardownPolicy`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.lifecycle.teardownPolicy`|
| Type       | string (enum)                  |
| Required   | Yes                            |
| Default    | --                             |
| Validation | One of: `destroy`, `stop`, `retain` |

What happens when the sandbox expires or idles out:

- **`destroy`** -- Terminate and delete all resources.
- **`stop`** -- Stop the instance but retain its storage (EC2 only).
- **`retain`** -- Keep the instance running (manual cleanup required).

```yaml
spec:
  lifecycle:
    teardownPolicy: destroy
```

### `spec.lifecycle.maxLifetime`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.lifecycle.maxLifetime`   |
| Type       | duration string                |
| Required   | No                             |
| Default    | -- (no cap)                    |
| Validation | Pattern `^[0-9]+(s\|m\|h\|d)$`; must be >= `ttl` if set |

Absolute maximum lifetime from sandbox creation. `km extend` will not extend beyond this cap. If unset, there is no limit on extensions.

```yaml
spec:
  lifecycle:
    maxLifetime: "72h"
```

---

## `spec.runtime`

Controls the compute substrate and instance configuration.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.runtime`                 |
| Type       | object                         |
| Required   | Yes                            |

### `spec.runtime.substrate`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.runtime.substrate`       |
| Type       | string (enum)                  |
| Required   | Yes                            |
| Default    | --                             |
| Validation | One of: `ec2`, `ecs`, `docker`  |

Compute backend for the sandbox:

- **`ec2`** -- Provisions a dedicated EC2 instance.
- **`ecs`** -- Provisions an ECS Fargate task.
- **`docker`** -- Runs a local Docker container.

```yaml
spec:
  runtime:
    substrate: ec2
```

### `spec.runtime.spot`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.runtime.spot`            |
| Type       | bool                           |
| Required   | No                             |
| Default    | `false`                        |
| Validation | Boolean                        |

Whether to use spot pricing. For `ec2`, requests a spot instance. For `ecs`, uses the `FARGATE_SPOT` capacity provider.

```yaml
spec:
  runtime:
    spot: true
```

### `spec.runtime.instanceType`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.runtime.instanceType`    |
| Type       | string                         |
| Required   | Yes                            |
| Default    | --                             |
| Validation | `minLength: 1`                 |

EC2 instance type (e.g. `t3.medium`, `t3.small`, `t3.micro`) or ECS task size descriptor.

```yaml
spec:
  runtime:
    instanceType: t3.medium
```

### `spec.runtime.region`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.runtime.region`          |
| Type       | string                         |
| Required   | Yes                            |
| Default    | --                             |
| Validation | `minLength: 1`                 |

AWS region in which to provision the sandbox.

```yaml
spec:
  runtime:
    region: us-east-1
```

### `spec.runtime.rootVolumeSize`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.runtime.rootVolumeSize`  |
| Type       | integer                        |
| Required   | No                             |
| Default    | 0 (AMI default, typically 8 GB)|
| Validation | Must be >= 0                   |

Root EBS volume size in GB. When 0 or omitted, the AMI default size is used.

```yaml
spec:
  runtime:
    rootVolumeSize: 30
```

### `spec.runtime.additionalVolume`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.runtime.additionalVolume`|
| Type       | object                         |
| Required   | No                             |
| Default    | -- (no additional volume)      |
| Validation | `size` must be >= 1; EC2 only  |

Attaches and auto-mounts an extra EBS volume. Useful for separating data from the root volume.

| Field | Type | Description |
|-------|------|-------------|
| `size` | integer | Volume size in GB (required, >= 1) |
| `mountPoint` | string | Filesystem path to mount the volume (required) |
| `encrypted` | boolean | Whether the EBS volume should be encrypted at rest (optional, default `false`) |

```yaml
spec:
  runtime:
    additionalVolume:
      size: 20
      mountPoint: /data
      encrypted: true
```

The compiler attaches the volume at `/dev/sdf` by default. When `spec.runtime.ami` references a baked AMI whose own block device mappings already declare `/dev/sdf`, the compiler queries the AMI's BDMs via `DescribeImages` at compile time and rotates onto the next free slot in `/dev/sd[g-p]` (NVMe aliases `/dev/xvdf` are normalized to `/dev/sdf` for collision detection). This makes baked-AMI relaunches transparent — no profile change required.

### `spec.runtime.hibernation`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.runtime.hibernation`     |
| Type       | bool                           |
| Required   | No                             |
| Default    | `false`                        |
| Validation | Incompatible with `spot: true`; EC2 only |

Enables EC2 hibernation. When `km pause` is called, the instance's RAM state is persisted to EBS and the instance resumes exactly where it left off. Requires on-demand instances (spot instances cannot hibernate).

```yaml
spec:
  runtime:
    hibernation: true
    spot: false       # required — spot instances cannot hibernate
```

### `spec.runtime.ami`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.runtime.ami`             |
| Type       | string (slug or AMI ID)        |
| Required   | No                             |
| Default    | `""` (Amazon Linux 2023)       |
| Validation | One of the slugs below, OR a raw AMI ID matching `^ami-[0-9a-f]+$` |

Either a slug (resolved per-region by Terraform's `data.aws_ami` lookup) or a raw AMI ID owned by the application AWS account. When empty or omitted, defaults to Amazon Linux 2023.

**Supported slugs:**

- **`"amazon-linux-2023"`** -- Amazon Linux 2023 (default)
- **`"ubuntu-24.04"`** -- Ubuntu 24.04 LTS
- **`"ubuntu-22.04"`** -- Ubuntu 22.04 LTS
- **`""`** -- Empty string, same as `amazon-linux-2023`

**Raw AMI IDs** (`ami-xxxxxxxx`) skip the slug-to-AMI lookup entirely and pass the ID straight through to the EC2 launch. Use this with AMIs you've baked yourself via `km shell --learn --ami` or `km ami bake` — the generated `learned.*.yaml` profile already includes the right value here. Raw IDs are region-specific: an AMI baked in `us-east-1` cannot be referenced from a profile that compiles for `eu-west-1` until you run `km ami copy --region eu-west-1`.

```yaml
spec:
  runtime:
    ami: "ubuntu-24.04"            # slug — resolved per-region
```

```yaml
spec:
  runtime:
    ami: "ami-0abc123def456"       # raw AMI ID — exact, region-locked
```

### `spec.runtime.mountEFS`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.runtime.mountEFS`        |
| Type       | bool                           |
| Required   | No                             |
| Default    | `false`                        |
| Validation | EC2 only; requires EFS provisioned via `km init` |

Mounts the regional EFS shared filesystem into the sandbox. The EFS filesystem ID is read from `infra/live/<region>/efs/outputs.json` (provisioned by `km init`). Enables cross-sandbox data sharing within a region.

```yaml
spec:
  runtime:
    mountEFS: true
    efsMountPoint: /shared
```

### `spec.runtime.efsMountPoint`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.runtime.efsMountPoint`   |
| Type       | string                         |
| Required   | No                             |
| Default    | `"/shared"`                    |
| Validation | String                         |

Filesystem path where EFS is mounted. Only used when `mountEFS: true`.

```yaml
spec:
  runtime:
    efsMountPoint: /shared
```

---

## `spec.execution`

Controls the shell environment within the sandbox.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.execution`               |
| Type       | object                         |
| Required   | Yes                            |

### `spec.execution.shell`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.execution.shell`         |
| Type       | string                         |
| Required   | Yes                            |
| Default    | --                             |
| Validation | `minLength: 1`                 |

Path to the shell executable used inside the sandbox.

```yaml
spec:
  execution:
    shell: /bin/bash
```

### `spec.execution.workingDir`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.execution.workingDir`    |
| Type       | string                         |
| Required   | Yes                            |
| Default    | --                             |
| Validation | `minLength: 1`                 |

Initial working directory when the sandbox starts.

```yaml
spec:
  execution:
    workingDir: /workspace
```

### `spec.execution.env`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.execution.env`           |
| Type       | map[string]string              |
| Required   | No                             |
| Default    | -- (empty)                     |
| Validation | All values must be strings     |

Additional environment variables injected into the sandbox shell environment.

```yaml
spec:
  execution:
    env:
      SANDBOX_MODE: my-profile
      MY_VAR: my-value
```

### `spec.execution.initCommands`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.execution.initCommands`  |
| Type       | list of strings                |
| Required   | No                             |
| Default    | -- (empty)                     |
| Validation | Array of strings               |

Shell commands executed at sandbox boot time (as root). Run in order before the sandbox user session starts.

```yaml
spec:
  execution:
    initCommands:
      - "yum install -y git nodejs npm python3"
      - "npm install -g @anthropic-ai/claude-code"
      - "mkdir -p /workspace && chown sandbox:sandbox /workspace"
```

### `spec.execution.useBedrock`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.execution.useBedrock`    |
| Type       | bool                           |
| Required   | No                             |
| Default    | `false`                        |
| Validation | Boolean                        |

Routes Anthropic API calls through AWS Bedrock instead of `api.anthropic.com`. When true, the compiler injects `CLAUDE_CODE_USE_BEDROCK=1`, `ANTHROPIC_BASE_URL` (Bedrock endpoint), and model ID mappings (Sonnet/Opus/Haiku) as environment variables. No `ANTHROPIC_API_KEY` is needed -- authentication uses the sandbox's AWS credentials via SigV4.

Can be overridden at create time with `km create --no-bedrock`, which strips all Bedrock-related environment variables and sets `useBedrock: false`.

```yaml
spec:
  execution:
    useBedrock: true
```

### `spec.execution.initScripts`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.execution.initScripts`   |
| Type       | list of strings                |
| Required   | No                             |
| Default    | -- (empty)                     |
| Validation | Array of strings (local file paths) |

Local script files to upload to the sandbox and execute at boot time.

```yaml
spec:
  execution:
    initScripts:
      - "./scripts/setup-agent.sh"
```

### `spec.execution.rsync`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.execution.rsync`         |
| Type       | string                         |
| Required   | No                             |
| Default    | -- (no snapshot restore)       |
| Validation | String                         |

Name of a previously saved home directory snapshot to restore at sandbox boot. Created via `km rsync save`.

```yaml
spec:
  execution:
    rsync: checkpoint-1
```

### `spec.execution.rsyncPaths`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.execution.rsyncPaths`    |
| Type       | list of strings                |
| Required   | No                             |
| Default    | -- (empty, meaning save entire home directory) |
| Validation | Array of relative paths        |

Relative paths (from the sandbox user's home) to include in rsync snapshots. When set, only these paths are saved/restored instead of the full home directory.

```yaml
spec:
  execution:
    rsyncPaths:
      - ".claude"
      - ".bashrc"
      - ".gitconfig"
```

### `spec.execution.rsyncFileList`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.execution.rsyncFileList` |
| Type       | string                         |
| Required   | No                             |
| Default    | -- (no external file list)     |
| Validation | String (path to YAML file)     |

Path to a YAML file containing additional rsync paths. Merged with `rsyncPaths` at save time. Supports wildcards.

```yaml
spec:
  execution:
    rsyncFileList: "./rsync-paths.yaml"
```

### `spec.execution.privileged`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.execution.privileged`    |
| Type       | boolean                        |
| Required   | No                             |
| Default    | `false`                        |

Grants the sandbox user wheel group membership and passwordless sudo access. When `false` (default), the sandbox user has no root capability. Operators who want to fully remove sudo from the instance can use a custom AMI without sudo installed.

```yaml
spec:
  execution:
    privileged: true
```

**Effect:** On EC2, the sandbox user is created with `-G wheel` and a `/etc/sudoers.d/sandbox` entry granting `NOPASSWD:ALL`. On Docker, the container already runs as root.

### `spec.execution.configFiles`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.execution.configFiles`   |
| Type       | map[string]string              |
| Required   | No                             |
| Default    | -- (empty)                     |
| Validation | Keys must be absolute file paths; values are file contents |

Map of absolute file paths to their contents. Each entry is written to the sandbox filesystem during bootstrap, owned by the sandbox user. Use this to pre-seed tool configuration files (e.g. Claude settings.json, Goose config, .gitconfig). Written after `initCommands`.

```yaml
spec:
  execution:
    configFiles:
      "/home/sandbox/.claude/settings.json": |
        {"trustedDirectories":["/home/sandbox","/workspace"]}
      "/home/sandbox/.gitconfig": |
        [user]
          name = Sandbox
          email = sandbox@klankermaker.ai
```

---

## `spec.sourceAccess`

Controls access to source code repositories.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.sourceAccess`            |
| Type       | object                         |
| Required   | Yes                            |

### `spec.sourceAccess.mode`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.sourceAccess.mode`       |
| Type       | string (enum)                  |
| Required   | Yes                            |
| Default    | --                             |
| Validation | One of: `allowlist`, `none`    |

Access mode for source code:

- **`allowlist`** -- Only repositories matching the `github` rules are accessible.
- **`none`** -- No source access.

```yaml
spec:
  sourceAccess:
    mode: allowlist
```

### `spec.sourceAccess.github`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.sourceAccess.github`     |
| Type       | object                         |
| Required   | No (but required if `mode: allowlist` and you want GitHub access) |
| Default    | -- (nil)                       |

GitHub repository access controls.

### `spec.sourceAccess.github.allowedRepos`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.sourceAccess.github.allowedRepos` |
| Type       | list of strings                |
| Required   | Yes (when `github` is present) |
| Default    | --                             |
| Validation | Array of strings               |

List of allowed GitHub repository patterns. Supports wildcards and org-level
access. The `github.com/` prefix is optional — `my-org/my-repo` and
`github.com/my-org/my-repo` are equivalent.

When this list is non-empty, the HTTP proxy **implicitly allows** GitHub hosts
(`github.com`, `api.github.com`, `*.githubusercontent.com`) and enforces
repo-level access via MITM interception. You do **not** need to add GitHub
hosts to `network.egress.allowedHosts` or `allowedDNSSuffixes`.

```yaml
spec:
  sourceAccess:
    github:
      allowedRepos:
        - "my-org/*"               # all repos in the org
        - "other-org/specific-repo" # single repo
```

### `spec.sourceAccess.github.allowedRefs`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.sourceAccess.github.allowedRefs` |
| Type       | list of strings                |
| Required   | No                             |
| Default    | -- (empty)                     |
| Validation | Array of strings               |

List of allowed git refs (branches, tags). Supports wildcards.

```yaml
spec:
  sourceAccess:
    github:
      allowedRefs:
        - "main"
        - "develop"
        - "feature/*"
```

---

## `spec.network`

Controls egress network policy.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.network`                 |
| Type       | object                         |
| Required   | Yes                            |

### `spec.network.egress`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.network.egress`          |
| Type       | object                         |
| Required   | Yes                            |

### `spec.network.egress.allowedDNSSuffixes`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.network.egress.allowedDNSSuffixes` |
| Type       | list of strings                |
| Required   | Yes                            |
| Default    | --                             |
| Validation | Array of strings; use `[]` for no DNS access |

DNS suffix patterns the sandbox is allowed to resolve. The DNS proxy sidecar enforces this list. Prefixed with `.` by convention.

```yaml
spec:
  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
        - ".github.com"
        - ".npmjs.org"
```

### `spec.network.egress.allowedHosts`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.network.egress.allowedHosts` |
| Type       | list of strings                |
| Required   | Yes                            |
| Default    | --                             |
| Validation | Array of strings; use `[]` for no host access |

Explicit hostnames allowed for outbound HTTP/HTTPS traffic. Enforced by the HTTP proxy sidecar.

```yaml
spec:
  network:
    egress:
      allowedHosts:
        - "api.github.com"
        - "registry.npmjs.org"
        - "pypi.org"
```

---

## `spec.identity`

Controls AWS IAM identity and session configuration.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.identity`                |
| Type       | object                         |
| Required   | Yes                            |

### `spec.identity.roleSessionDuration`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.identity.roleSessionDuration` |
| Type       | duration string                |
| Required   | Yes                            |
| Default    | --                             |
| Validation | Pattern `^[0-9]+(s\|m\|h)$` (days not allowed) |

Maximum duration for AWS STS assumed role sessions.

```yaml
spec:
  identity:
    roleSessionDuration: "1h"
```

### `spec.identity.allowedRegions`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.identity.allowedRegions` |
| Type       | list of strings                |
| Required   | Yes                            |
| Default    | --                             |
| Validation | `minItems: 1`                  |

AWS regions the sandbox IAM session is permitted to access. At least one region is required.

```yaml
spec:
  identity:
    allowedRegions:
      - us-east-1
      - us-west-2
```

### `spec.identity.sessionPolicy`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.identity.sessionPolicy`  |
| Type       | string (enum)                  |
| Required   | Yes                            |
| Default    | --                             |
| Validation | One of: `minimal`, `standard`, `elevated` |

IAM session policy scope that determines the breadth of permissions available within the sandbox.

```yaml
spec:
  identity:
    sessionPolicy: minimal
```

### `spec.identity.allowedSecretPaths`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.identity.allowedSecretPaths` |
| Type       | list of strings                |
| Required   | No                             |
| Default    | -- (empty)                     |
| Validation | Array of strings               |

Allowlist of AWS SSM Parameter Store paths the sandbox may read at boot time. Secrets matching these paths are injected as environment variables via user-data.

```yaml
spec:
  identity:
    allowedSecretPaths:
      - "/klankrmkr/sandbox/api-key"
      - "/klankrmkr/sandbox/db-password"
```

---

## `spec.sidecars`

Defines the sidecar processes that run alongside the sandbox. All four sidecars are required in the schema.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.sidecars`                |
| Type       | object                         |
| Required   | Yes                            |

Each sidecar (`dnsProxy`, `httpProxy`, `auditLog`, `tracing`) shares the same structure:

### Sidecar Config Fields

#### `enabled`

| Property   | Value                          |
|------------|--------------------------------|
| Type       | bool                           |
| Required   | Yes                            |
| Validation | Boolean                        |

Whether this sidecar is active.

#### `image`

| Property   | Value                          |
|------------|--------------------------------|
| Type       | string                         |
| Required   | Yes                            |
| Validation | `minLength: 1`                 |

Container image reference for this sidecar.

### `spec.sidecars.dnsProxy`

DNS filtering proxy. Enforces `network.egress.allowedDNSSuffixes`.

### `spec.sidecars.httpProxy`

HTTP filtering proxy. Enforces `network.egress.allowedHosts`.

### `spec.sidecars.auditLog`

Captures a full audit trail of all sandbox activity.

### `spec.sidecars.tracing`

Distributed tracing collector for sandbox operations.

```yaml
spec:
  sidecars:
    dnsProxy:
      enabled: true
      image: km-dns-proxy:latest
    httpProxy:
      enabled: true
      image: km-http-proxy:latest
    auditLog:
      enabled: true
      image: km-audit-log:latest
    tracing:
      enabled: true
      image: km-tracing:latest
```

---

## `spec.observability`

Controls logging and observability destinations.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.observability`           |
| Type       | object                         |
| Required   | Yes                            |

Each log destination (`commandLog`, `networkLog`) shares the same structure:

### Log Destination Fields

#### `destination`

| Property   | Value                          |
|------------|--------------------------------|
| Type       | string (enum)                  |
| Required   | Yes                            |
| Validation | One of: `cloudwatch`, `s3`, `stdout` |

Log backend destination.

#### `logGroup`

| Property   | Value                          |
|------------|--------------------------------|
| Type       | string                         |
| Required   | No                             |
| Default    | -- (empty)                     |
| Validation | String                         |

CloudWatch log group name or S3 prefix. Relevant when `destination` is `cloudwatch` or `s3`.

### `spec.observability.commandLog`

Captures all commands executed within the sandbox.

### `spec.observability.networkLog`

Captures all network egress events from the sandbox.

```yaml
spec:
  observability:
    commandLog:
      destination: cloudwatch
      logGroup: /klankrmkr/sandboxes
    networkLog:
      destination: cloudwatch
      logGroup: /klankrmkr/network
```

### `spec.observability.claudeTelemetry`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.observability.claudeTelemetry` |
| Type       | object                         |
| Required   | No                             |
| Default    | -- (nil, telemetry **enabled** by default -- `IsEnabled()` returns `true` when nil) |

Controls Claude Code OpenTelemetry data collection within the sandbox. When the entire `claudeTelemetry` object is omitted (nil), telemetry is **enabled** by default.

### `spec.observability.claudeTelemetry.enabled`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.observability.claudeTelemetry.enabled` |
| Type       | bool                           |
| Required   | No                             |
| Default    | `true` (telemetry enabled when omitted) |
| Validation | Boolean                        |

Master switch for Claude Code OTEL telemetry. Defaults to `true` when omitted (`IsEnabled()` returns `true` when `nil`).

### `spec.observability.claudeTelemetry.logPrompts`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.observability.claudeTelemetry.logPrompts` |
| Type       | bool                           |
| Required   | No                             |
| Default    | `false`                        |
| Validation | Boolean                        |

Include actual user prompt text in OTEL data. Maps to `OTEL_LOG_USER_PROMPTS` environment variable.

### `spec.observability.claudeTelemetry.logToolDetails`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.observability.claudeTelemetry.logToolDetails` |
| Type       | bool                           |
| Required   | No                             |
| Default    | `false`                        |
| Validation | Boolean                        |

Include tool call parameters (bash commands, file paths) in OTEL data. Maps to `OTEL_LOG_TOOL_DETAILS` environment variable.

```yaml
spec:
  observability:
    claudeTelemetry:
      enabled: true
      logPrompts: true
      logToolDetails: true
```

### `spec.observability.tlsCapture`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.observability.tlsCapture`|
| Type       | object                         |
| Required   | No                             |
| Default    | -- (disabled)                  |
| Validation | EC2 only; requires eBPF or both enforcement mode |

Controls TLS/SSL plaintext capture via eBPF uprobes (Phase 41). When enabled, uprobes attach to TLS library functions (e.g. `SSL_read`/`SSL_write`) to capture plaintext before encryption / after decryption. Provides an audit trail independent of the MITM proxy.

### `spec.observability.tlsCapture.enabled`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.observability.tlsCapture.enabled` |
| Type       | bool                           |
| Required   | Yes (if `tlsCapture` is specified) |
| Default    | `false`                        |

Master switch for TLS plaintext capture.

### `spec.observability.tlsCapture.libraries`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.observability.tlsCapture.libraries` |
| Type       | list of strings                |
| Required   | No                             |
| Default    | `["openssl"]`                  |
| Validation | Allowed values: `openssl`, `gnutls`, `nss`, `go`, `rustls`, `all` |

TLS libraries to hook into. Currently only `openssl` is fully implemented; others are accepted by the schema but are no-ops at runtime.

### `spec.observability.tlsCapture.capturePayloads`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.observability.tlsCapture.capturePayloads` |
| Type       | bool                           |
| Required   | No                             |
| Default    | `false`                        |
| Validation | Boolean                        |

Capture full payload content. When `false` (default), only metadata (sizes, directions) are logged. When `true`, the full plaintext of TLS traffic is captured for audit purposes.

```yaml
spec:
  observability:
    tlsCapture:
      enabled: true
      libraries: [openssl]
      capturePayloads: false
```

### `spec.observability.learnMode`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.observability.learnMode` |
| Type       | boolean                        |
| Required   | No                             |
| Default    | `false`                        |

Enables traffic observation recording on the eBPF enforcer. When `true`, the enforcer starts with `--observe`, recording all DNS queries and TLS connections in memory. The recorded traffic is flushed to S3 on SIGUSR1 (triggered by `km shell --learn`) or on shutdown, enabling `km shell --learn` to generate a minimal SandboxProfile from observed traffic.

```yaml
spec:
  observability:
    learnMode: true
```

**Typical workflow:**

```bash
km create profiles/learn.yaml         # learnMode: true, privileged: true, wide-open DNS
km shell --learn <sandbox-id>         # run workload, exit → learned.YYYYMMDDHHMMSS.yaml
cat learned.*.yaml                    # review annotated profile (includes initCommands)
km validate learned.*.yaml            # validate before use
```

---

---

## `spec.agent`

Controls behavior of the AI agent workload running in the sandbox.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.agent`                   |
| Type       | object                         |
| Required   | Yes                            |

### `spec.agent.maxConcurrentTasks`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.agent.maxConcurrentTasks`|
| Type       | int                            |
| Required   | Yes                            |
| Default    | --                             |
| Validation | `minimum: 1`                   |

Maximum number of parallel tasks the agent may run simultaneously.

```yaml
spec:
  agent:
    maxConcurrentTasks: 4
```

### `spec.agent.taskTimeout`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.agent.taskTimeout`       |
| Type       | duration string                |
| Required   | Yes                            |
| Default    | --                             |
| Validation | Pattern `^[0-9]+(s\|m\|h)$` (days not allowed) |

Maximum duration for a single agent task. The task is terminated if it exceeds this duration.

```yaml
spec:
  agent:
    taskTimeout: "30m"
```

### `spec.agent.allowedTools`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.agent.allowedTools`      |
| Type       | list of strings                |
| Required   | No                             |
| Default    | -- (empty, meaning no tool restrictions) |
| Validation | Array of strings               |

Tool names the agent is permitted to use. When omitted, no tool-level restrictions are enforced.

```yaml
spec:
  agent:
    allowedTools:
      - bash
      - read_file
      - write_file
      - list_files
```

---

## `spec.artifacts`

Optional artifact collection and S3 upload settings. When omitted entirely (`nil`), artifact collection is disabled.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.artifacts`               |
| Type       | object                         |
| Required   | No                             |

### `spec.artifacts.paths`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.artifacts.paths`         |
| Type       | list of strings                |
| Required   | No                             |
| Default    | -- (empty)                     |
| Validation | Array of strings               |

Glob patterns or directory paths to collect as artifacts when the sandbox tears down.

```yaml
spec:
  artifacts:
    paths:
      - "/workspace/output/**"
      - "/workspace/reports/*.html"
```

### `spec.artifacts.maxSizeMB`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.artifacts.maxSizeMB`     |
| Type       | int                            |
| Required   | No                             |
| Default    | `0`                            |
| Validation | `minimum: 0`; `0` means unlimited |

Maximum individual file size in megabytes to upload. Files exceeding this limit are skipped.

```yaml
spec:
  artifacts:
    maxSizeMB: 100
```

### `spec.artifacts.replicationRegion`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.artifacts.replicationRegion` |
| Type       | string                         |
| Required   | No                             |
| Default    | -- (no replication)            |
| Validation | String                         |

Optional secondary AWS region to replicate artifacts to via S3 cross-region replication.

```yaml
spec:
  artifacts:
    replicationRegion: us-west-2
```

---

## `spec.email`

Controls inter-sandbox email policy. Each sandbox gets a unique email address derived from its ID (e.g., `sb-a1b2c3d4@sandboxes.klankermaker.ai`).

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.email`                   |
| Type       | object                         |
| Required   | No                             |

### `spec.email.signing`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.email.signing`           |
| Type       | string (enum)                  |
| Required   | No                             |
| Default    | `"optional"`                   |
| Validation | One of: `required`, `optional`, `off` |

Ed25519 signing policy for outbound email.

### `spec.email.verifyInbound`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.email.verifyInbound`     |
| Type       | string (enum)                  |
| Required   | No                             |
| Default    | `"optional"`                   |
| Validation | One of: `required`, `optional`, `off` |

Signature verification policy for inbound email. When `required`, unsigned or invalid emails are rejected.

### `spec.email.encryption`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.email.encryption`        |
| Type       | string (enum)                  |
| Required   | No                             |
| Default    | `"off"`                        |
| Validation | One of: `required`, `optional`, `off` |

NaCl box encryption policy for email body content.

### `spec.email.alias`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.email.alias`             |
| Type       | string                         |
| Required   | No                             |
| Default    | -- (none)                      |
| Validation | String (dot-notation)          |

Dot-notation email alias for the sandbox (e.g., `research.team-a`).

### `spec.email.allowedSenders`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.email.allowedSenders`    |
| Type       | list of strings                |
| Required   | No                             |
| Default    | -- (empty)                     |
| Validation | Array of strings; special values: `"self"`, `"*"`, sandbox IDs, wildcards |

Allowlist of sandbox IDs or patterns permitted to send email to this sandbox.

```yaml
spec:
  email:
    signing: required
    verifyInbound: required
    encryption: off
    alias: research.team-a
    allowedSenders:
      - "self"
      - "sb-*"
```

---

## `spec.budget`

Controls per-sandbox spend limits for compute and AI API usage.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.budget`                  |
| Type       | object                         |
| Required   | No                             |

### `spec.budget.compute.maxSpendUSD`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.budget.compute.maxSpendUSD` |
| Type       | float                          |
| Required   | No                             |
| Default    | `0` (no limit)                 |
| Validation | `minimum: 0`                   |

Maximum compute spend in USD. Tracks spot rate x elapsed minutes. At exhaustion, the instance is suspended (not destroyed).

### `spec.budget.ai.maxSpendUSD`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.budget.ai.maxSpendUSD`   |
| Type       | float                          |
| Required   | No                             |
| Default    | `0` (no limit)                 |
| Validation | `minimum: 0`                   |

Maximum AI API spend in USD. Tracks Bedrock/Anthropic/OpenAI token usage via the HTTP proxy. At exhaustion, proxy returns 403 and IAM Bedrock policy is revoked.

### `spec.budget.warningThreshold`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.budget.warningThreshold` |
| Type       | float                          |
| Required   | No                             |
| Default    | `0.80`                         |
| Validation | `minimum: 0`, `maximum: 1`    |

Fraction of budget at which a warning email is sent to the operator.

```yaml
spec:
  budget:
    compute:
      maxSpendUSD: 2.00
    ai:
      maxSpendUSD: 5.00
    warningThreshold: 0.80
```

---

## `spec.otp`

One-time password/secret injection.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.otp`                     |
| Type       | object                         |
| Required   | No                             |

### `spec.otp.secrets`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.otp.secrets`             |
| Type       | list of strings                |
| Required   | No                             |
| Default    | -- (empty)                     |
| Validation | Array of SSM parameter paths   |

SSM Parameter Store paths read once at sandbox boot and then deleted. Provides one-time secret injection that leaves no persistent credential in SSM.

```yaml
spec:
  otp:
    secrets:
      - "/km/sandbox/one-time-api-key"
```

---

## `spec.cli`

Operator-side defaults for `km shell` / `km agent` commands. These settings do not affect sandbox provisioning -- only CLI behavior when connecting to or running agents in the sandbox.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.cli`                     |
| Type       | object                         |
| Required   | No                             |

### `spec.cli.noBedrock`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.cli.noBedrock`           |
| Type       | bool                           |
| Required   | No                             |
| Default    | `false`                        |
| Validation | Boolean                        |

Makes `--no-bedrock` the default for `km shell` and `km agent run`. The sandbox is still provisioned with Bedrock environment variables; this only affects the operator's connection. Override on the CLI with `--bedrock`.

```yaml
spec:
  cli:
    noBedrock: true
```

---

## Profile Inheritance

A profile can extend a parent profile using the `extends` field. Inheritance is resolved at load time by the `Resolve()` function.

### Rules

1. **Section-level replacement** -- If a child profile defines any field within a spec section (e.g. `spec.lifecycle`), the child's entire section replaces the parent's. Fields are not merged at the individual-field level within a section.

2. **Zero-value fallback** -- If a child profile leaves an entire spec section as the zero value (all fields empty/unset), the parent's section is used.

3. **Labels are the exception** -- `metadata.labels` is the only field that merges additively. Child labels override same-key parent labels, but parent-only labels are preserved.

4. **List replacement** -- For allowlist arrays (`allowedDNSSuffixes`, `allowedHosts`, `allowedRepos`, etc.), if the child specifies them at all, the child's array replaces the parent's entirely. There is no array merging.

5. **Max depth** -- Inheritance chains are limited to 3 levels. A profile extending a profile extending a profile is the maximum.

6. **Cycle detection** -- Circular inheritance (A extends B extends A) is detected and rejected.

7. **Resolution order** -- Built-in profiles are checked first, then search paths on disk.

8. **Extends is cleared** -- The resolved (merged) profile has its `extends` field set to empty string.

### Example

```yaml
# my-profile.yaml -- extends hardened, opens network access
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: my-profile
  labels:
    team: platform
extends: hardened

spec:
  # Only override the sections you want to change.
  # All other sections (runtime, execution, sidecars, etc.) are inherited from hardened.
  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
        - ".github.com"
      allowedHosts:
        - "api.github.com"
  lifecycle:
    ttl: "8h"
    idleTimeout: "2h"
    teardownPolicy: destroy
```

---

## Semantic Validation Rules

Beyond JSON Schema structural validation, the following semantic rules are enforced:

| Rule | Path | Description |
|------|------|-------------|
| TTL >= idleTimeout | `spec.lifecycle.ttl` | The TTL must not be shorter than the idle timeout. A sandbox cannot idle out after it has already expired. |
| Valid substrate | `spec.runtime.substrate` | Must be `ec2`, `ecs`, or `docker` (also enforced by schema enum). |
| Valid enforcement | `spec.network.enforcement` | Must be `proxy`, `ebpf`, or `both` (also enforced by schema enum). |
| eBPF is EC2-only | `spec.network.enforcement` | eBPF enforcement (`ebpf` or `both`) is EC2-only. On ECS or Docker substrates, proxy enforcement is used regardless. |

---

## Built-in Profiles

Seven built-in profiles ship with Klanker Maker: `open-dev`, `restricted-dev`, `hardened`, `sealed`, `goose`, `ao`, and `codex`. These range from permissive development (`open-dev`) to maximum containment (`sealed`), plus tool-specific agent profiles. The `learn` profile (in `profiles/learn.yaml`) is not a built-in but is documented separately below for traffic observation workflows.

### `open-dev`

Permissive development profile. Broad package registry and GitHub access, wide ref patterns, full agent tooling.

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: open-dev
  labels:
    tier: development
    builtin: "true"
spec:
  lifecycle:
    ttl: "24h"
    idleTimeout: "4h"
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
    env:
      SANDBOX_MODE: open-dev
  sourceAccess:
    mode: allowlist
    github:
      allowedRepos:
        - "github.com/*"
      allowedRefs:
        - "main"
        - "develop"
        - "feature/*"
        - "fix/*"
  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
        - ".github.com"
        - ".githubusercontent.com"
        - ".npmjs.org"
        - ".pypi.org"
        - ".golang.org"
        - ".docker.io"
        - ".registry.hub.docker.com"
      allowedHosts:
        - "api.github.com"
        - "github.com"
        - "registry.npmjs.org"
        - "pypi.org"
        - "pkg.go.dev"
        - "sum.golang.org"
  identity:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1, us-west-2]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:  { enabled: true, image: "km-dns-proxy:latest" }
    httpProxy: { enabled: true, image: "km-http-proxy:latest" }
    auditLog:  { enabled: true, image: "km-audit-log:latest" }
    tracing:   { enabled: true, image: "km-tracing:latest" }
  observability:
    commandLog: { destination: cloudwatch, logGroup: "/klankrmkr/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klankrmkr/network" }
    claudeTelemetry:
      enabled: true
      logPrompts: true
      logToolDetails: true
  agent:
    maxConcurrentTasks: 4
    taskTimeout: "30m"
    allowedTools: [bash, read_file, write_file, list_files]
```

### `restricted-dev`

Restricted development profile. Organization-scoped repos, limited refs, reduced agent concurrency.

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: restricted-dev
  labels:
    tier: development
    builtin: "true"
spec:
  lifecycle:
    ttl: "8h"
    idleTimeout: "2h"
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
    env:
      SANDBOX_MODE: restricted-dev
  sourceAccess:
    mode: allowlist
    github:
      allowedRepos:
        - "github.com/whereiskurt/*"
      allowedRefs:
        - "main"
        - "develop"
  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
        - ".github.com"
        - ".githubusercontent.com"
        - ".npmjs.org"
        - ".pypi.org"
        - ".golang.org"
      allowedHosts:
        - "api.github.com"
        - "registry.npmjs.org"
        - "pypi.org"
        - "pkg.go.dev"
        - "sum.golang.org"
  identity:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:  { enabled: true, image: "km-dns-proxy:latest" }
    httpProxy: { enabled: true, image: "km-http-proxy:latest" }
    auditLog:  { enabled: true, image: "km-audit-log:latest" }
    tracing:   { enabled: true, image: "km-tracing:latest" }
  observability:
    commandLog: { destination: cloudwatch, logGroup: "/klankrmkr/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klankrmkr/network" }
    claudeTelemetry:
      enabled: true
      logPrompts: false
      logToolDetails: true
  agent:
    maxConcurrentTasks: 2
    taskTimeout: "20m"
    allowedTools: [bash, read_file, write_file]
```

### `hardened`

Production-grade profile. Minimal network access, single command, read-only agent tooling.

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: hardened
  labels:
    tier: production
    security: hardened
spec:
  lifecycle:
    ttl: "4h"
    idleTimeout: "1h"
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.small
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: [".amazonaws.com"]
      allowedHosts:
        - "sts.us-east-1.amazonaws.com"
        - "ssm.us-east-1.amazonaws.com"
  identity:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:  { enabled: true, image: "km-dns-proxy:latest" }
    httpProxy: { enabled: true, image: "km-http-proxy:latest" }
    auditLog:  { enabled: true, image: "km-audit-log:latest" }
    tracing:   { enabled: true, image: "km-tracing:latest" }
  observability:
    commandLog: { destination: cloudwatch, logGroup: "/klankrmkr/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klankrmkr/network" }
    claudeTelemetry:
      enabled: true
      logPrompts: false
      logToolDetails: false
    tlsCapture:
      enabled: true
      libraries: [openssl]
      capturePayloads: false
  agent:
    maxConcurrentTasks: 2
    taskTimeout: "30m"
    allowedTools: [read_file]
```

### `sealed`

Maximum restriction. No network egress, no source access, no commands, single-task agent.

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: sealed
  labels:
    tier: production
    security: sealed
spec:
  lifecycle:
    ttl: "1h"
    idleTimeout: "30m"
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.micro
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
  sourceAccess:
    mode: allowlist
  network:
    egress:
      allowedDNSSuffixes: []
      allowedHosts: []
  identity:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:  { enabled: true, image: "km-dns-proxy:latest" }
    httpProxy: { enabled: true, image: "km-http-proxy:latest" }
    auditLog:  { enabled: true, image: "km-audit-log:latest" }
    tracing:   { enabled: true, image: "km-tracing:latest" }
  observability:
    commandLog: { destination: cloudwatch, logGroup: "/klankrmkr/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klankrmkr/network" }
    claudeTelemetry:
      enabled: true
      logPrompts: false
      logToolDetails: false
    tlsCapture:
      enabled: true
      libraries: [openssl]
      capturePayloads: false
  agent:
    maxConcurrentTasks: 1
    taskTimeout: "15m"
```

### `goose`

Goose AI agent (Block) with Bedrock access, Claude Code, Codex, MCP extensions, OTEL telemetry, EFS shared storage, eBPF gatekeeper enforcement, email, and hibernation support.

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: goose
  labels:
    tier: development
    tool: goose
  prefix: gebpfgk
spec:
  lifecycle:
    ttl: "1h"
    idleTimeout: "15m"
    teardownPolicy: stop
  runtime:
    substrate: ec2
    spot: false
    instanceType: t3.medium
    region: us-east-1
    hibernation: true
    mountEFS: true
    efsMountPoint: /shared
    additionalVolume:
      size: 20
      mountPoint: /data
  execution:
    shell: /bin/bash
    workingDir: /workspace
    useBedrock: true
    env:
      SANDBOX_MODE: goose-ebpf-gatekeeper
      GOOSE_PROVIDER: aws_bedrock
      GOOSE_MODEL: us.anthropic.claude-opus-4-6-v1
      GOOSE_MODE: auto
      GOOSE_TELEMETRY_ENABLED: "false"
      CODEX_CA_CERTIFICATE: /usr/local/share/ca-certificates/km-proxy-ca.crt
      OPENAI_API_KEY: ""
    rsyncPaths:
      - ".gitconfig"
      - ".config/goose"
      - ".claude"
      - ".claude.json"
      - ".codex"
    initCommands:
      - "yum install -y git nodejs npm python3 python3-pip bzip2 jq tar gzip unzip"
      - "HOME=/root curl -fsSL https://github.com/block/goose/releases/download/stable/download_cli.sh | HOME=/root CONFIGURE=false bash"
      - "npm install -g @anthropic-ai/claude-code"
      # ... additional setup commands for goose, codex, CA certs
  sourceAccess:
    mode: allowlist
    github:
      allowedRepos: ["whereiskurt/meshtk", "whereiskurt/defcon.run.34"]
      allowedRefs: ["main", "develop", "feature/*", "fix/*"]
  network:
    enforcement: both
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
        - ".anthropic.com"
        - ".claude.ai"
        - ".claude.com"
        - ".sentry.io"
        - ".cloudfront.net"
        - ".github.com"
        - ".githubusercontent.com"
        - ".npmjs.org"
        - ".npmjs.com"
        - ".nodejs.org"
        - ".npmmirror.com"
        - ".openai.com"
        - ".chatgpt.com"
        - ".pypi.org"
        - ".pythonhosted.org"
        - ".pulsemcp.com"
        - ".google.com"
        - ".google-analytics.com"
        - ".googletagmanager.com"
      allowedHosts:
        - "github.com"
        - "api.anthropic.com"
        - "statsig.anthropic.com"
        - "statsig.com"
        - "api.statsig.com"
        - "featuregates.org"
        - "api.featuregates.org"
        - "registry.npmjs.org"
        - "nodejs.org"
        - "api.openai.com"
        - "chatgpt.com"
        - "pypi.org"
        - "files.pythonhosted.org"
        - "pulsemcp.com"
        - "google.com"
  budget:
    compute: { maxSpendUSD: 0.50 }
    ai: { maxSpendUSD: 1.00 }
    warningThreshold: 0.80
  identity:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:  { enabled: true, image: "km-dns-proxy:latest" }
    httpProxy: { enabled: true, image: "km-http-proxy:latest" }
    auditLog:  { enabled: true, image: "km-audit-log:latest" }
    tracing:   { enabled: true, image: "km-tracing:latest" }
  observability:
    commandLog: { destination: cloudwatch, logGroup: "/klankrmkr/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klankrmkr/network" }
    claudeTelemetry:
      enabled: true
      logPrompts: true
      logToolDetails: true
    tlsCapture:
      enabled: true
      libraries: [openssl]
      capturePayloads: false
  email:
    signing: required
    verifyInbound: required
    encryption: required
    allowedSenders: ["self"]
  agent:
    maxConcurrentTasks: 1
    taskTimeout: "30m"
    allowedTools: [bash, read_file, write_file, list_files]
```

### `codex`

OpenAI Codex agent sandbox with proxy enforcement, hibernation, email, and OTEL telemetry.

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: codex
  labels:
    tier: development
    tool: codex
    builtin: "true"
  prefix: codex
spec:
  lifecycle:
    ttl: "4h"
    idleTimeout: "30m"
    teardownPolicy: stop
  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.medium
    region: us-east-1
    hibernation: true
    additionalVolume:
      size: 20
      mountPoint: /data
  execution:
    shell: /bin/bash
    workingDir: /workspace
    env:
      SANDBOX_MODE: codex
      CODEX_CA_CERTIFICATE: /usr/local/share/ca-certificates/km-proxy-ca.crt
      OPENAI_API_KEY: ""
    rsyncPaths: [".gitconfig", ".codex"]
    initCommands:
      - "yum install -y git nodejs npm jq tar gzip unzip"
      - "curl -fsSL https://github.com/openai/codex/releases/download/rust-v0.117.0/codex-x86_64-unknown-linux-musl.tar.gz -o /tmp/codex.tar.gz"
      - "tar -xzf /tmp/codex.tar.gz -C /tmp && install -m 755 /tmp/codex-x86_64-unknown-linux-musl /usr/local/bin/codex"
      # ... additional setup commands
  sourceAccess:
    mode: allowlist
    github:
      allowedRepos: ["whereiskurt/meshtk", "whereiskurt/defcon.run.34"]
      allowedRefs: ["main", "develop", "feature/*", "fix/*"]
  network:
    enforcement: proxy
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
        - ".openai.com"
        - ".chatgpt.com"
        - ".github.com"
        - ".githubusercontent.com"
        - ".sentry.io"
        - ".cloudfront.net"
      allowedHosts:
        - "api.openai.com"
        - "chatgpt.com"
        - "github.com"
        - "sentry.io"
  budget:
    compute: { maxSpendUSD: 2.00 }
    ai: { maxSpendUSD: 5.00 }
    warningThreshold: 0.80
  identity:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:  { enabled: true, image: "km-dns-proxy:latest" }
    httpProxy: { enabled: true, image: "km-http-proxy:latest" }
    auditLog:  { enabled: true, image: "km-audit-log:latest" }
    tracing:   { enabled: true, image: "km-tracing:latest" }
  observability:
    commandLog: { destination: cloudwatch, logGroup: "/klankrmkr/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klankrmkr/network" }
    claudeTelemetry:
      enabled: true
      logPrompts: true
      logToolDetails: true
  email:
    signing: required
    verifyInbound: required
    encryption: required
    allowedSenders: ["self"]
  agent:
    maxConcurrentTasks: 1
    taskTimeout: "60m"
    allowedTools: [bash, read_file, write_file, list_files]
```

### `ao`

Multi-agent orchestration sandbox with Claude Code, Codex, Composio's agent-orchestrator, eBPF gatekeeper enforcement, email, and hibernation support.

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: ao
  labels:
    tier: development
    tool: agent-orchestrator
    builtin: "true"
  prefix: ao
spec:
  lifecycle:
    ttl: "8h"
    idleTimeout: "1h"
    teardownPolicy: stop
  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.large
    region: us-east-1
    hibernation: true
    additionalVolume:
      size: 20
      mountPoint: /data
  execution:
    shell: /bin/bash
    workingDir: /workspace
    env:
      SANDBOX_MODE: agent-orchestrator
      GITHUB_TOKEN: ""
      ANTHROPIC_BASE_URL: "https://bedrock-runtime.us-east-1.amazonaws.com"
      CLAUDE_CODE_USE_BEDROCK: "1"
      OPENAI_API_KEY: ""
      CODEX_CA_CERTIFICATE: /usr/local/share/ca-certificates/km-proxy-ca.crt
    rsyncPaths: [".gitconfig", ".agent-orchestrator", ".claude", ".claude.json", ".codex"]
    initCommands:
      - "yum install -y git tmux jq tar gzip unzip"
      - "curl -fsSL https://rpm.nodesource.com/setup_20.x | bash - && yum install -y nodejs"
      - "npm install -g @composio/ao @anthropic-ai/claude-code"
      # ... additional setup commands for codex, gh, etc.
  sourceAccess:
    mode: allowlist
    github:
      allowedRepos: ["whereiskurt/meshtk", "whereiskurt/defcon.run.34"]
      allowedRefs: ["main", "develop", "feature/*", "fix/*"]
  network:
    enforcement: both
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
        - ".anthropic.com"
        - ".claude.ai"
        - ".claude.com"
        - ".sentry.io"
        - ".cloudfront.net"
        - ".github.com"
        - ".githubusercontent.com"
        - ".npmjs.org"
        - ".npmjs.com"
        - ".nodejs.org"
        - ".npmmirror.com"
        - ".openai.com"
        - ".chatgpt.com"
        - ".pypi.org"
        - ".pythonhosted.org"
        - ".pulsemcp.com"
        - ".google.com"
        - ".google-analytics.com"
        - ".googletagmanager.com"
      allowedHosts:
        - "github.com"
        - "api.anthropic.com"
        - "statsig.anthropic.com"
        - "statsig.com"
        - "api.statsig.com"
        - "featuregates.org"
        - "api.featuregates.org"
        - "registry.npmjs.org"
        - "nodejs.org"
        - "api.openai.com"
        - "chatgpt.com"
        - "pypi.org"
        - "files.pythonhosted.org"
        - "pulsemcp.com"
        - "google.com"
  budget:
    compute: { maxSpendUSD: 4.00 }
    ai: { maxSpendUSD: 10.00 }
    warningThreshold: 0.80
  identity:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
    sessionPolicy: minimal
  sidecars:
    dnsProxy:  { enabled: true, image: "km-dns-proxy:latest" }
    httpProxy: { enabled: true, image: "km-http-proxy:latest" }
    auditLog:  { enabled: true, image: "km-audit-log:latest" }
    tracing:   { enabled: true, image: "km-tracing:latest" }
  observability:
    commandLog: { destination: cloudwatch, logGroup: "/klankrmkr/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klankrmkr/network" }
    claudeTelemetry:
      enabled: true
      logPrompts: true
      logToolDetails: true
  email:
    signing: required
    verifyInbound: required
    encryption: required
    allowedSenders: ["self"]
  agent:
    maxConcurrentTasks: 4
    taskTimeout: "120m"
    allowedTools: [bash, read_file, write_file, list_files]
```

### `learn` (not a built-in -- lives in `profiles/learn.yaml`)

Permissive profile designed for traffic observation. Wide-open DNS suffixes covering common TLDs, `enforcement: both` for eBPF + proxy capture, `privileged: true` for sudo access, and `learnMode: true` to record traffic. Use with `km shell --learn` to generate a minimal SandboxProfile from observed traffic.

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: learn
  prefix: learn
  labels:
    tier: development
    tool: traffic-observation
    builtin: "true"
spec:
  lifecycle:
    ttl: "2h"
    idleTimeout: "30m"
    teardownPolicy: destroy
  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.medium
    region: us-east-1
  execution:
    shell: /bin/bash
    workingDir: /workspace
    privileged: true
  network:
    enforcement: both
    egress:
      allowedDNSSuffixes:
        - ".com"
        - ".org"
        - ".net"
        - ".io"
        - ".dev"
        - ".ai"
        - ".co"
        - ".app"
        - ".cloud"
        - ".sh"
        - ".me"
        - ".info"
        - ".edu"
        - ".gov"
        - ".amazonaws.com"
      allowedHosts: []
  observability:
    commandLog: { destination: cloudwatch, logGroup: "/klankrmkr/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klankrmkr/network" }
    tlsCapture:
      enabled: true
    learnMode: true
  budget:
    compute:
      maxSpendUSD: 2.00
    ai:
      maxSpendUSD: 0.00
  agent:
    maxConcurrentTasks: 1
    taskTimeout: "30m"
```

**Workflow:**

```bash
km create profiles/learn.yaml         # spin up permissive sandbox
km shell --learn <sandbox-id>         # install packages, clone repos, curl APIs
# ... exit shell → learned.YYYYMMDDHHMMSS.yaml
cat learned.*.yaml                    # annotated profile with DNS suffixes + initCommands
km validate learned.*.yaml            # validate, then use for production sandboxes
```

---

## Built-in Profile Comparison

| Field | open-dev | restricted-dev | hardened | sealed | goose | codex | ao | learn* |
|-------|----------|----------------|----------|--------|-------|-------|----|--------|
| `lifecycle.ttl` | 24h | 8h | 4h | 1h | 1h | 4h | 8h | 2h |
| `lifecycle.idleTimeout` | 4h | 2h | 1h | 30m | 15m | 30m | 1h | 30m |
| `lifecycle.teardownPolicy` | destroy | destroy | destroy | destroy | stop | stop | stop | destroy |
| `runtime.instanceType` | t3.medium | t3.medium | t3.small | t3.micro | t3.medium | t3.medium | t3.large | t3.medium |
| `runtime.spot` | true | true | true | true | false | true | true | true |
| `runtime.hibernation` | -- | -- | -- | -- | true | true | true | -- |
| `runtime.mountEFS` | -- | -- | -- | -- | true | -- | -- | -- |
| `runtime.additionalVolume` | -- | -- | -- | -- | 20 GB | 20 GB | 20 GB | -- |
| `network.enforcement` | proxy | proxy | proxy | proxy | both | proxy | both | both |
| `execution.privileged` | -- | -- | -- | -- | -- | -- | -- | true |
| `execution.useBedrock` | -- | -- | -- | -- | true | -- | -- | -- |
| `observability.learnMode` | -- | -- | -- | -- | -- | -- | -- | true |
| `observability.tlsCapture` | -- | -- | true | true | true | -- | -- | true |
| `metadata.prefix` | sb | sb | sb | sb | gebpfgk | codex | ao | learn |
| `budget.compute.maxSpendUSD` | -- | -- | -- | -- | $0.50 | $2.00 | $4.00 | $2.00 |
| `budget.ai.maxSpendUSD` | -- | -- | -- | -- | $1.00 | $5.00 | $10.00 | $0.00 |
| `email` | -- | -- | -- | -- | required | required | required | -- |
| `agent.maxConcurrentTasks` | 4 | 2 | 2 | 1 | 1 | 1 | 4 | 1 |
| `agent.taskTimeout` | 30m | 20m | 30m | 15m | 30m | 60m | 120m | 30m |

*\* `learn` is not a built-in profile; it lives in `profiles/learn.yaml`.*

---

## Common Patterns

### Adding a DNS suffix to an inherited profile

Because inheritance replaces entire sections, you must include all parent DNS suffixes plus your addition:

```yaml
extends: hardened
spec:
  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
        - ".github.com"
        - ".githubusercontent.com"
        - ".npmjs.org"
        - ".pypi.org"
        - ".golang.org"
        - ".my-internal-registry.com"    # added
      allowedHosts:
        - "api.github.com"
        - "registry.npmjs.org"
        - "pypi.org"
        - "pkg.go.dev"
        - "sum.golang.org"
        - "my-internal-registry.com"     # added
```

### Pinning to a specific git ref

Lock the sandbox to a single branch of a single repo:

```yaml
spec:
  sourceAccess:
    mode: allowlist
    github:
      allowedRepos:
        - "github.com/my-org/my-repo"
      allowedRefs:
        - "release/v2.0"
```

### Enabling artifact collection

```yaml
spec:
  artifacts:
    paths:
      - "/workspace/output/**"
      - "/workspace/logs/*.log"
    maxSizeMB: 50
    replicationRegion: us-west-2
```

### Creating a minimal air-gapped profile

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: air-gapped
extends: sealed
spec:
  # sealed already has empty network egress lists.
  # Override only what you need:
  lifecycle:
    ttl: "2h"
    idleTimeout: "1h"
    teardownPolicy: destroy
  agent:
    maxConcurrentTasks: 1
    taskTimeout: "30m"
    allowedTools:
      - read_file
      - write_file
```

### Injecting secrets via SSM Parameter Store

```yaml
spec:
  identity:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
    sessionPolicy: minimal
    allowedSecretPaths:
      - "/klankrmkr/sandbox/api-keys/github"
      - "/klankrmkr/sandbox/api-keys/npm"
```
