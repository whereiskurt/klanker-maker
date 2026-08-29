# SandboxProfile YAML Reference

Complete schema reference for the `SandboxProfile` YAML format used by the `km` CLI.

SandboxProfiles follow a Kubernetes-style `apiVersion`/`kind`/`metadata`/`spec` structure. They are validated against a JSON Schema (Draft 2020-12) and additional semantic rules at parse time.

---

## Document Structure

```yaml
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: my-profile
  labels:
    tier: development
extends: hardened          # optional; a string or an ordered list of parents
spec:
  # --- required blocks ---
  lifecycle: { ... }
  runtime: { ... }
  execution: { ... }
  sourceAccess: { ... }
  network: { ... }
  iam: { ... }             # renamed from identity: in Phase 92
  sidecars: { ... }
  observability: { ... }
  # --- optional blocks ---
  agent: { ... }           # default agent + Claude/Codex tool gating
  notification: { ... }    # event gates + email/Slack/bridge delivery
  artifacts: { ... }
  budget: { ... }
  email: { ... }
  secrets: { ... }         # SOPS-encrypted env bundle
  limits: { ... }          # per-action outbound quotas
  cli: { ... }
```

Every block above is accepted by the schema. `spec` and each sub-block are declared
`additionalProperties: false`, so an unknown or misspelled key is a hard `km validate`
error rather than a silently ignored one. One field documented below — `spec.otp` — is
implemented in the compiler but has never been in the schema, and is rejected today; see
its own section.

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
| Validation | Must be exactly `klankermaker.ai/v1alpha2` |

The API version of the SandboxProfile resource. Currently only `v1alpha2` is
supported (bumped from `v1alpha1` in Phase 92; `v1alpha1` is now rejected).

```yaml
apiVersion: klankermaker.ai/v1alpha2
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

### `metadata.abstract`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `metadata.abstract`            |
| Type       | bool                           |
| Required   | No                             |
| Default    | `false`                        |
| Validation | Boolean                        |

Marks the file as an **abstract fragment** — a partial profile meant only to be
pulled in by another profile's `extends:`, never launched on its own. `km validate`
skips abstract fragments (exit 0 with a `SKIP` message) instead of failing them for
the required fields they deliberately omit, and `km create` refuses them outright.
The shipped fragment library under `profiles/base/` sets this on every file;
`scripts/validate-all-profiles.sh` excludes that directory automatically.

```yaml
metadata:
  name: safenetwork
  abstract: true
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

Absolute maximum lifetime, measured from sandbox creation. `km extend` refuses to push
expiry past `createdAt + maxLifetime`. Omit it for no cap.

`ttl` is the *current* expiry and is what `km extend` moves; `maxLifetime` is the ceiling
that movement cannot cross. Setting the two equal is the "no extensions permitted"
configuration and is explicitly allowed. Setting `maxLifetime` **shorter** than `ttl` is a
validation error — it would make the sandbox un-extendable from the moment it booted,
which is never the intent.

The cap is evaluated against the sandbox's recorded `createdAt`, not against wall-clock
time at the moment you run `km extend`, so pausing or stopping a sandbox does not buy back
lifetime.

```yaml
spec:
  lifecycle:
    ttl: "4h"
    idleTimeout: "1h"
    teardownPolicy: destroy
    maxLifetime: "3d"     # extend freely within 3 days of creation, never past it
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

### `spec.runtime.additionalSnapshots`

| Property   | Value                                  |
|------------|----------------------------------------|
| YAML path  | `spec.runtime.additionalSnapshots`     |
| Type       | list of objects                        |
| Required   | No                                     |
| Default    | -- (empty)                             |
| Validation | EC2 only; each entry needs `snapshotId` and `mountPoint` |

Materialises one fresh `aws_ebs_volume` per entry from an existing EBS snapshot,
attaches it on `/dev/sd[f-p]`, and mounts it with a userdata-detected filesystem.
Coexists with `additionalVolume` — both may be set. Volume lifetime is the sandbox's
lifetime: the volumes are created at `km create` and destroyed at `km destroy`.

Per-entry fields: `snapshotId` (required), `mountPoint` (required), `device`
(optional — auto-assigned from the free `/dev/sd[f-p]` range when omitted),
`encrypted`, `size` (optional override; must be >= the snapshot's own size).

```yaml
spec:
  runtime:
    additionalSnapshots:
      - snapshotId: snap-0123456789abcdef0
        mountPoint: /data
      - snapshotId: snap-0fedcba9876543210
        mountPoint: /models
        size: 500
```

See [`OPERATOR-GUIDE.md` § additionalSnapshots](../OPERATOR-GUIDE.md) for the full
authoring guide.

### `spec.runtime.desktop`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.runtime.desktop`         |
| Type       | object                         |
| Required   | No                             |
| Default    | disabled                       |
| Validation | Ubuntu 24.04/22.04 AMIs only   |

Provisions a KasmVNC graphical session reachable with `km desktop start <id>` over an
SSM port-forward (loopback only — no security-group or public-IP change). Fields:

| Field      | Type            | Default   | Meaning |
|------------|-----------------|-----------|---------|
| `enabled`  | bool            | `false`   | Turn the desktop on. |
| `mode`     | `kiosk`/`full`  | `kiosk`   | `kiosk` runs a single browser fullscreen; `full` runs an XFCE desktop. |
| `browsers` | list of strings | `[firefox]` | Subset of `firefox`, `chromium`, `chrome`, `brave`. |
| `geometry` | string          | `1920x1080` | Initial framebuffer size. |

`km validate` errors when `desktop.enabled: true` is combined with a non-Ubuntu
`spec.runtime.ami`. The desktop packages install **before** network enforcement is
applied, so `spec.network.egress` does not need to allowlist their download hosts.

```yaml
spec:
  runtime:
    ami: ubuntu-24.04
    desktop:
      enabled: true
      mode: full
      browsers: [firefox, chromium]
      geometry: "2560x1440"
```

See [`docs/desktop.md`](desktop.md).

### `spec.runtime.azPreference`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.runtime.azPreference`    |
| Type       | list of strings                |
| Required   | No                             |
| Default    | -- (ranked automatically)      |
| Validation | Availability-zone names in `spec.runtime.region` |

Preferred availability zones, most-preferred first. `km create` sweeps AZs with
classify-and-retry: an `InsufficientInstanceCapacity` / spot-price / spot-limit /
waiter-timeout failure rotates to the next AZ, while a quota / auth / invalid-parameter
failure fails fast (no AZ rotation helps a quota wall). When omitted, `capacity.RankAZs`
orders the region's AZs itself using offering data, GPU quota headroom, and the
`{prefix}-capacity` table's history. Preview the ranking with `km capacity`.

```yaml
spec:
  runtime:
    azPreference:
      - us-east-1d
      - us-east-1b
```

### `spec.runtime.launchAccount`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.runtime.launchAccount`   |
| Type       | string                         |
| Required   | No                             |
| Default    | -- (launch in the home account) |
| Validation | Must name a link in `launch_accounts:` in `km-config.yaml` |

Launches this profile's EC2 instance into a **different, pre-enrolled AWS account** to
borrow that account's vCPU quota, while the whole km control plane — DynamoDB state,
budget, `km list`, every bridge — stays in the home account. Enroll a target account
with `km account add` (target-account admin credentials) followed by
`km account register` (home credentials); `km account list` shows what is wired.

`km create` fails fast when the named link is absent or its external id cannot be read
— it never silently falls back to the home account. Teardown is account-aware on both
the `km destroy` and TTL-expiry paths.

```yaml
spec:
  runtime:
    launchAccount: gpu-capacity
```

See [`docs/cross-account-capacity-borrowing.md`](cross-account-capacity-borrowing.md).

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

Map of absolute file paths to their contents. Each entry is written to the sandbox filesystem during bootstrap, owned by the sandbox user. Use this to pre-seed tool configuration files (e.g. Goose config, `.gitconfig`). Written after `initCommands`.

> **Phase 92:** the `"/home/sandbox/.claude/settings.json"` key is **forbidden** here — `~/.claude/settings.json` is synthesized from `spec.agent.claude.tools.*` + `trustedDirectories` (see [`docs/agent-tool-gating.md`](agent-tool-gating.md)). Inlining it alongside the typed `spec.agent.claude` block is a hard `km validate` error.

```yaml
spec:
  execution:
    configFiles:
      "/home/sandbox/.gitconfig": |
        [user]
          name = Sandbox
          email = sandbox@klankermaker.ai
```

---

### `spec.execution.initCommandsAppend`

| Property   | Value                                 |
|------------|---------------------------------------|
| YAML path  | `spec.execution.initCommandsAppend`   |
| Type       | list of strings                       |
| Required   | No                                    |
| Default    | -- (empty)                            |
| Validation | Array of shell commands               |

Leaf-specific install steps appended **after** the merged `initCommands`. This exists
because inheritance unions list fields: a child's `initCommands` merges with every
base's `initCommands` rather than replacing them, so there is no way to express "run
these last" through that field alone. `initCommandsAppend` is the ordering escape
hatch — use it for the one or two steps that must follow everything a base installed.

```yaml
spec:
  execution:
    initCommandsAppend:
      - "npm install -g @my-org/internal-cli"
```

See [`OPERATOR-GUIDE.md` § Composable inheritance](../OPERATOR-GUIDE.md).

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

### `spec.network.egress.deniedDNSSuffixes`

| Property   | Value                                    |
|------------|------------------------------------------|
| YAML path  | `spec.network.egress.deniedDNSSuffixes`  |
| Type       | list of strings                          |
| Required   | No                                       |
| Default    | -- (empty)                               |
| Validation | Array of DNS suffixes                    |

Destinations the sandbox may never resolve. **A deny beats every allow** — including
the `*` wildcard, the GitHub repo-filter carve-out, the OpenAI budget path, and the
Bedrock/SES/Anthropic MITM interceptors. Deny matching is deliberately **broader** than
allow matching: a bare entry also covers its subdomains, so `evil.example.com` blocks
`api.evil.example.com` too. (Strictness on an allowlist permits less; the same
strictness on a denylist would fail open.)

```yaml
spec:
  network:
    egress:
      deniedDNSSuffixes:
        - "pastebin.com"
        - ".ngrok.io"
```

### `spec.network.egress.deniedHosts`

| Property   | Value                              |
|------------|------------------------------------|
| YAML path  | `spec.network.egress.deniedHosts`  |
| Type       | list of strings                    |
| Required   | No                                 |
| Default    | -- (empty)                         |
| Validation | Array of hostnames                 |

The HTTP/HTTPS counterpart of `deniedDNSSuffixes`, enforced by the http-proxy. The deny
gate is registered **first** in the proxy's handler chain, because goproxy dispatches
first-match and every later handler is a carve-out — a deny evaluated after them would
be silently bypassable. Same broader-than-allow subdomain matching.

```yaml
spec:
  network:
    egress:
      deniedHosts:
        - "pastebin.com"
        - "files.example-exfil.net"
```

### `spec.network.egress.runtimeDeny`

| Property   | Value                              |
|------------|------------------------------------|
| YAML path  | `spec.network.egress.runtimeDeny`  |
| Type       | bool                               |
| Required   | No                                 |
| Default    | `false`                            |
| Validation | Boolean                            |

> **Deprecated and a no-op since v0.8.8.** The `km-netpolicy` mechanism it used to gate
> now ships on **every** sandbox. The key is still accepted so existing profiles keep
> validating; it is deliberately not removed from the schema.

`km-netpolicy deny <host>` / `km-netpolicy list` (in `/opt/km/bin`) let a **running**
sandbox append denies to its own policy from user-land, effective within ~1s and with no
restart. Narrow-only is enforced twice over: append is the only operation there is —
there is no removal verb — and the deny file carries `chattr +a`, so the kernel refuses
truncate, unlink, rename, and attribute-clear. The file lives under `/var/lib`, not
`/run`, because a reboot that dropped accumulated denies would *widen* the policy.

The gate was removed because it was backwards: `km-netpolicy` can only ever add denies,
so its presence can never widen a policy, while the boxes where narrowing matters most
are exactly the wide-open ones (`allowedDNSSuffixes: ["*"]`, learn mode) that no profile
would have thought to opt in.

On `privileged: true` the sandbox has sudo and can clear `+a` — but it can equally stop
the proxies, so the guarantee is meaningful on unprivileged boxes.

See [`docs/egress-deny-lists.md`](egress-deny-lists.md).

### `spec.network.httpsOnly`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.network.httpsOnly`       |
| Type       | bool                           |
| Required   | No                             |
| Default    | `true`                         |
| Validation | Boolean                        |

Restricts outbound HTTP traffic to TLS. Plain `http://` requests are rejected by the
http-proxy. Set to `false` only for a sandbox that must reach a legacy plaintext
endpoint.

```yaml
spec:
  network:
    httpsOnly: true
```

### `spec.network.enforcement`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.network.enforcement`     |
| Type       | string                         |
| Required   | No                             |
| Default    | `proxy`                        |
| Validation | One of `proxy`, `ebpf`, `both`; `ebpf`/`both` are EC2-only |

Selects how egress policy is enforced:

| Mode    | Mechanism |
|---------|-----------|
| `proxy` | iptables DNAT redirects DNS/HTTP/HTTPS into the userspace proxy sidecars. Root is exempt from the DNAT, so this mode does not constrain a privileged process. |
| `ebpf`  | cgroup BPF programs (`connect4`, `sendmsg4`, `sockops`, `cgroup_skb/egress`) with an LPM-trie allowlist. Applies to every process in the cgroup regardless of privilege. The bootstrap leaves `km-dns-proxy` disabled and the eBPF resolver serves DNS. |
| `both`  | eBPF as the primary gatekeeper **plus** the proxy for L7 inspection — Bedrock/Anthropic/OpenAI budget metering, GitHub repo filtering, and MITM intercepts all need the proxy to see the request. |

On the ECS and Docker substrates, `proxy` is used regardless of what is declared.

```yaml
spec:
  network:
    enforcement: both
```

See [`docs/ebpf.md`](ebpf.md).

### `spec.network.privateSubnet`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.network.privateSubnet`   |
| Type       | bool                           |
| Required   | No                             |
| Default    | `false`                        |
| Validation | Requires `network.nat_gateway: true` in `km-config.yaml` |

Places this sandbox's ENI in a **private** subnet with no public IPv4, egressing through
the per-AZ NAT gateway. The two toggles are decoupled on purpose: the install-level
`network.nat_gateway` key controls whether the NAT/EIP infrastructure *exists*, and this
per-profile field controls whether *this* sandbox uses it.

`km create` fails fast — before compiling or uploading any artifact — when a private
profile meets a NAT-less install, naming the config key and the fix command. AZs with no
NAT gateway are dropped from the capacity ranking for private sandboxes.

NAT is not free: roughly $132/month for four AZs plus $0.045/GB processed, so a GPU
profile pulling 300GB of weights adds about $13.50 in NAT processing alone.

```yaml
spec:
  network:
    privateSubnet: true
```

See [`docs/private-subnet-nat.md`](private-subnet-nat.md).

### `spec.network.mitm.intercepts`

| Property   | Value                              |
|------------|------------------------------------|
| YAML path  | `spec.network.mitm.intercepts`     |
| Type       | list of objects                    |
| Required   | No                                 |
| Default    | -- (empty; no interception)        |
| Validation | `name` and `hosts` required; exactly one of `action.redirect` / `action.respond` |

Operator-declared host→action rules the http-proxy applies to intercepted TLS traffic.
Two actions are available: `redirect` (a 301 with a `Location`) and `respond` (a canned
status/body/contentType). Off by default — a profile with no `mitm:` block gets zero
interception.

Per-entry fields: `name` (required, the override key), `enabled` (default `true`),
`hosts` (required), and `action` with either `redirect: <url>` or
`respond: {status, contentType, body}`.

**Precedence is unconditional and cannot be overridden by a profile:** deny gate →
Bedrock/Anthropic/OpenAI metering and the GitHub repo filter → operator intercepts →
general allowlist. An intercept naming a metering or GitHub host is therefore silently
dead (`km validate` WARNs); an intercept for a host absent from the allowlist still
fires, which is the useful case for a canned error on a host the sandbox cannot
otherwise reach.

There is deliberately **no `block` action** — `deniedHosts` already blocks with strictly
stronger semantics (ahead of everything, broader subdomain matching, appendable at
runtime with `km-netpolicy`), and a second weaker way to block would be a footgun.

Host matching reuses `IsHostAllowed` semantics: case-insensitive, port-stripped, leading
dot for a subdomain match, no regex and no `*`.

Inheritance resolves intercepts **by name, last-wins, whole-entry** — not a field merge —
so a leaf can turn an inherited rule off with `- name: <inherited>` / `enabled: false`.

```yaml
spec:
  network:
    mitm:
      intercepts:
        - name: internal-docs
          hosts: ["docs.example.com"]
          action:
            redirect: "https://intranet.example.com/docs"
        - name: blocked-notice
          hosts: ["files.example.com"]
          action:
            respond:
              status: 403
              contentType: "text/plain"
              body: "Blocked by policy — ask #platform for an exception."
```

Under `enforcement: ebpf`/`both`, intercept hosts are threaded into the enforcer's
`--proxy-hosts`, but DNS is deliberately **not** widened: a host outside
`allowedDNSSuffixes` never resolves and the rule silently never fires (`km validate`
warns about this).

See [`docs/mitm-intercepts.md`](mitm-intercepts.md).

---

## `spec.iam`

Controls AWS IAM identity and session configuration.

> **Phase 92 (2026-05-31):** renamed from `spec.identity:`. The `sessionPolicy`
> field was removed (never read by any code path). `allowedSecretPaths` is now
> declared in the JSON schema (Phase 89 drift fix). Requires
> `apiVersion: klankermaker.ai/v1alpha2`.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.iam`                     |
| Type       | object                         |
| Required   | Yes                            |

### `spec.iam.roleSessionDuration`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.iam.roleSessionDuration` |
| Type       | duration string                |
| Required   | Yes                            |
| Default    | --                             |
| Validation | Pattern `^[0-9]+(s\|m\|h)$` (days not allowed) |

Maximum duration for AWS STS assumed role sessions.

```yaml
spec:
  iam:
    roleSessionDuration: "1h"
```

### `spec.iam.allowedRegions`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.iam.allowedRegions`      |
| Type       | list of strings                |
| Required   | Yes                            |
| Default    | --                             |
| Validation | `minItems: 1`                  |

AWS regions the sandbox IAM session is permitted to access. At least one region is required.

```yaml
spec:
  iam:
    allowedRegions:
      - us-east-1
      - us-west-2
```

### `spec.iam.allowedSecretPaths`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.iam.allowedSecretPaths`  |
| Type       | list of strings                |
| Required   | No                             |
| Default    | -- (empty)                     |
| Validation | Array of strings               |

Allowlist of AWS SSM Parameter Store paths the sandbox may read at boot time. Secrets matching these paths are injected as environment variables via user-data.

```yaml
spec:
  iam:
    allowedSecretPaths:
      - "/klanker-maker/sandbox/api-key"
      - "/klanker-maker/sandbox/db-password"
```

---

### `spec.iam.allowBedrock`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.iam.allowBedrock`        |
| Type       | bool                           |
| Required   | No                             |
| Default    | `false`                        |
| Validation | Boolean                        |

Grants the sandbox role Bedrock IAM (`bedrock:InvokeModel` plus `bedrock-mantle`)
**without** `spec.execution.useBedrock`'s agent-environment injection. The distinction
matters for GPU serving profiles: the on-box Bifrost gateway routes `bedrock/...` model
strings with SigV4 against the instance role, so it needs the IAM grant, while the
agents on the box stay pointed at the local vLLM endpoint rather than at Bedrock.

```yaml
spec:
  iam:
    allowBedrock: true
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
      logGroup: /klanker-maker/sandboxes
    networkLog:
      destination: cloudwatch
      logGroup: /klanker-maker/network
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

Controls TLS/SSL plaintext capture via eBPF uprobes. When enabled, uprobes attach to TLS library functions (e.g. `SSL_read`/`SSL_write`) to capture plaintext before encryption / after decryption. Provides an audit trail independent of the MITM proxy.

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

Selects the default agent and declares structured Claude/Codex tool gating. The
compiler synthesizes `~/.claude/settings.json` (canonical `permissions.allow` /
`permissions.deny`) and `~/.codex/config.toml` from this block.

```yaml
spec:
  agent:
    default: claude          # claude | codex (absence ≡ claude)
    claude:
      trustedDirectories: [/home/sandbox, /workspace]
      tools:
        autoApprove: [Bash, Read, Write, Edit, Glob, Grep]
        deny: []
      args: []               # extra `claude` CLI args
      permissions: {}        # raw settings.json permissions passthrough
    codex:
      tools:
        autoApprove: []
        deny: []
      args: []               # extra `codex` CLI args
      localBaseURL: ""       # point codex at an on-box model gateway
      localModel: ""         # model string sent to that gateway
```

- `agent.codex.localBaseURL` repoints the Codex CLI at an OpenAI-compatible endpoint on
  the box — in practice the Bifrost gateway in front of a locally served vLLM model
  (`http://localhost:8001/openai/v1`). It is emitted as a `[model_providers.local]`
  entry in the synthesized `~/.codex/config.toml`, and it is box-global: on-box terminal
  Codex, Slack-inbound Codex turns, and VS Code all follow it. See
  [`docs/gpu-model-serving.md`](gpu-model-serving.md).
- `agent.default` drives `km shell` / `km agent run` / Slack-inbound dispatch and writes `KM_AGENT`.
- Inlining `configFiles["/home/sandbox/.claude/settings.json"]` alongside `agent.claude.*` is supported: the compiler deep-merges the synthesized typed keys (permissions/trustedDirectories win) ON TOP of the inlined file, preserving operator keys like `enabledPlugins`/`env`/`model`. (Earlier builds rejected this as "mixed mode".)
- Full field reference and the Codex asymmetry note: [`docs/agent-tool-gating.md`](agent-tool-gating.md).

> **Phase 92 (2026-05-31):** the dead top-level `spec.agent:` block
> (`maxConcurrentTasks`, `taskTimeout`, `allowedTools`) was **removed**; this is
> the structured tool-gating block that replaced it (Waves 4/5).

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

> **Not currently settable — schema drift.** The compiler and userdata generator both
> honour this block (`pkg/compiler/userdata.go`, `pkg/compiler/service_hcl.go`), but
> `otp` is absent from `pkg/profile/schemas/sandbox_profile.schema.json` and `spec` is
> declared `additionalProperties: false`, so a profile that sets it fails validation
> with `spec: additional properties 'otp' not allowed`. Use
> [`spec.secrets.sopsFile`](#specsecretssopsfile) or
> [`spec.iam.allowedSecretPaths`](#speciamallowedsecretpaths) instead until the schema
> entry is restored.

```yaml
spec:
  otp:
    secrets:
      - "/km/sandbox/one-time-api-key"
```

---

## `spec.secrets`

Optional SOPS-encrypted secret bundle injected as environment variables at boot.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.secrets`                 |
| Type       | object                         |
| Required   | No                             |

### `spec.secrets.sopsFile`

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.secrets.sopsFile`        |
| Type       | string                         |
| Required   | No                             |
| Default    | -- (no bundle)                 |
| Validation | Path relative to the profile YAML; must be a SOPS-encrypted YAML file |

Path to a SOPS-encrypted YAML bundle (`.enc.yaml`), resolved relative to the profile
file. Its top-level keys become environment variables in `/etc/sandbox-secrets.env`
(mode `0440`, owner `root:sandbox`) at boot. Decryption uses the shared KMS key
provisioned once per install by `km bootstrap --shared-secrets-key`.

```yaml
spec:
  secrets:
    sopsFile: secrets/my-agent.enc.yaml
```

See [`docs/sandbox-secrets.md`](sandbox-secrets.md).

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

Makes `--no-bedrock` the default for `km shell` and `km agent run`. The sandbox is still provisioned with Bedrock environment variables; this only affects the operator's connection. Pass `--no-bedrock` explicitly per-invocation when this is unset (there is no positive `--bedrock` flag).

```yaml
spec:
  cli:
    noBedrock: true
```

### `spec.runtime.vscode.enabled`

| Property   | Value                                |
|------------|--------------------------------------|
| YAML path  | `spec.runtime.vscode.enabled`        |
| Type       | bool (pointer; omit = true)          |
| Required   | No                                   |
| Default    | `true` when omitted                  |
| Validation | Boolean                              |

Provisions sshd and writes the operator's pubkey to `/home/sandbox/.ssh/authorized_keys`
at boot so VS Code Remote-SSH (over SSM port-forward) can land in `/workspace`. When
true, `km create` also generates a per-sandbox ed25519 keypair at `~/.km/keys/<id>` on
the operator's laptop. Set to `false` for sandboxes that should not accept SSH
connections of any kind. See [`docs/vscode.md`](vscode.md) for the full operator guide.

> **Phase 92:** moved from `spec.cli.vscodeEnabled` to `spec.runtime.vscode.enabled`; the old path is rejected by `km validate`.

```yaml
spec:
  runtime:
    vscode:
      enabled: false   # opt out — no sshd, no authorized_keys, no keypair
```

---

## `spec.notification`

Operator notification policy: which agent events fire a notification, and where each one
is delivered. All sub-blocks are optional and every one of them is dormant by default.

> **Phase 92 (2026-05-31):** this block replaces the old `spec.cli.notify*` fields.
> Sandbox-side environment variable names (`KM_NOTIFY_*`, `KM_SLACK_*`) are
> **unchanged** — only the YAML surface moved.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.notification`            |
| Type       | object                         |
| Required   | No                             |

### `spec.notification.events`

Gates which Claude Code hook events produce a notification at all. Delivery is then
decided independently by the `email` and `slack` blocks.

| Field             | Type | Default | Meaning |
|-------------------|------|---------|---------|
| `onPermission`    | bool | `false` | Notify on a Claude `Notification` hook (a permission prompt is waiting). |
| `onIdle`          | bool | `false` | Notify on a Claude `Stop` hook (turn complete / idle). |
| `cooldownSeconds` | int  | `0`     | Suppress notifications within N seconds of the last successful send. Per-sandbox and shared across event types. |

```yaml
spec:
  notification:
    events:
      onPermission: true
      onIdle: true
      cooldownSeconds: 300
```

### `spec.notification.email`

| Field     | Type   | Default | Meaning |
|-----------|--------|---------|---------|
| `enabled` | bool   | `true` behaviourally | Explicit `false` skips email dispatch from the notify-hook. |
| `address` | string | operator inbox from `km-config.yaml` | Override recipient. |

```yaml
spec:
  notification:
    email:
      enabled: true
      address: oncall@example.com
```

### `spec.notification.slack`

Slack delivery, plus the inbound chat, transcript-streaming, and auto-invite features
that hang off the per-sandbox channel.

| Field              | Type   | Default | Meaning |
|--------------------|--------|---------|---------|
| `enabled`          | bool   | `false` | Enable Slack delivery for events that pass the `events.*` gates. Requires `km slack init` to have provisioned the bridge. |
| `perSandbox`       | bool   | `false` | Create a `#sb-{id}` channel at `km create`. Without it, the platform-wide shared channel is used. |
| `channelOverride`  | string | --      | Hard-pin to a Slack channel ID (`^C[A-Z0-9]+$`). Mutually exclusive with `perSandbox: true`. |
| `channelName`      | string | --      | Custom name for the auto-created channel. Used verbatim (sanitized to Slack's rules) with **no** forced `sb-` prefix; supports `{profile}`, `{alias}`, and `{id}` tokens. Requires `perSandbox: true`; mutually exclusive with `channelOverride`. |
| `archiveOnDestroy` | bool   | `true`  | Archive the per-sandbox channel at `km destroy`. Set `false` to reuse the channel across an alias's lifetimes. |
| `private`          | bool   | `false` | Create the channel as private (`is_private: true`). Channel membership becomes the read-and-trigger boundary. Takes effect at **creation only** — a reused channel is never converted public→private. |

#### `spec.notification.slack.inbound`

Bidirectional chat: a message in the channel dispatches an agent turn.

| Field                  | Type | Default | Meaning |
|------------------------|------|---------|---------|
| `enabled`              | bool | `false` | Provision the per-sandbox inbound FIFO queue and poller. Requires `slack.enabled` and `slack.perSandbox`; incompatible with `channelOverride`. |
| `mentionOnly`          | bool | per-channel-mode | Polite-bot mode — act only on messages that @-mention the bot. Omit for the smart default (shared and operator-override channels → `true`; per-sandbox channels → `false`). Once the bot has dispatched a turn in a thread, later replies in that thread bypass the mention requirement. |
| `reactAlways`          | bool | `true`  | Post the 👀 reaction on every dispatched message. `false` reacts only on top-level engagement messages, so thread replies dispatch silently. |
| `allow`                | list | --      | Per-sandbox trigger allowlist of Slack user IDs (`^[UW][A-Z0-9]+$`). When non-empty it **replaces** the install-level `slack.allow` for this sandbox. Empty falls back to install level; empty at both levels means everyone may trigger. Enforced ahead of the mention filter and the thread bypass; a rejected user is silently ignored — no reaction, no reply. |
| `maxConcurrentThreads` | int  | `1`     | Distinct Slack threads this sandbox's poller dispatches in parallel. Turns **within** one thread stay strictly serial and ordered. Note that parallel turns share `/workspace`. |

#### `spec.notification.slack.transcript`

| Field     | Type | Default | Meaning |
|-----------|------|---------|---------|
| `enabled` | bool | `false` | Stream the agent's transcript into the channel thread as the turn runs. |

#### `spec.notification.slack.invites`

| Field        | Type | Default | Meaning |
|--------------|------|---------|---------|
| `emails`     | list | --      | Additional people invited to the per-sandbox channel after `km create` succeeds, beyond the always-invited primary operator. Native vs Slack Connect is auto-detected. |
| `useConnect` | bool | `true`  | Gates the Slack Connect fallback for the `emails` loop only. `false` skips external addresses with a warning and a follow-up command. Does not affect the primary operator invite or `km slack invite`. |

```yaml
spec:
  notification:
    slack:
      enabled: true
      perSandbox: true
      private: true
      archiveOnDestroy: false
      inbound:
        enabled: true
        mentionOnly: true
        maxConcurrentThreads: 3
        allow: ["U01ABCDEFGH"]
      transcript:
        enabled: true
      invites:
        emails: ["teammate@example.com"]
```

See [`docs/slack-notifications.md`](slack-notifications.md).

### `spec.notification.github` / `.h1` / `.webhook`

Each provisions the per-sandbox inbound FIFO queue (and, for `h1`, the poller userdata)
for one bridge. All three take the same single field and are dormant when absent — no
SQS queue, no DynamoDB row, no SSM parameter, no poller.

| YAML path                                    | Default | Enables |
|----------------------------------------------|---------|---------|
| `spec.notification.github.inbound.enabled`   | `false` | GitHub PR/issue comment triggers → [`docs/github-bridge.md`](github-bridge.md) |
| `spec.notification.h1.inbound.enabled`       | `false` | HackerOne report/comment triggers → [`docs/h1-bridge.md`](h1-bridge.md) |
| `spec.notification.webhook.inbound.enabled`  | `false` | Generic push-webhook ingress (Wiz first source) → [`docs/webhook-ingress.md`](webhook-ingress.md) |

```yaml
spec:
  notification:
    github:
      inbound:
        enabled: true
```

---

## `spec.limits`

Per-action outbound quotas — a circuit breaker for an agent that has started looping,
not a throttle for normal work. Absent block means no counting at all.

| Property   | Value                          |
|------------|--------------------------------|
| YAML path  | `spec.limits`                  |
| Type       | object                         |
| Required   | No                             |
| Default    | -- (no quotas)                 |

Six actions can be metered, each taking the same shape:

| Action key       | Counts |
|------------------|--------|
| `github_pr`      | GitHub pull request creates |
| `github_comment` | GitHub issue/PR comments |
| `github_review`  | GitHub pull request review submissions |
| `email_send`     | Outbound SES emails |
| `slack_post`     | Slack posts and uploads via the bridge |
| `h1_comment`     | HackerOne report comments via the bridge |

Each entry accepts:

| Field      | Type | Default | Meaning |
|------------|------|---------|---------|
| `lifetime` | int  | --      | Maximum over the whole sandbox lifetime; never resets. |
| `perHour`  | int  | --      | Maximum per fixed hourly UTC bucket. |
| `perDay`   | int  | --      | Maximum per fixed UTC calendar day. |
| `onBreach` | enum | `warn`  | `warn` (alert only, action still flows), `block` (deny the action), or `freeze` (latch the sandbox into quarantine). |

An absent window is not enforced. A window set to **`0` is a hard-deny floor** — it
trips on the first attempt, which is how you turn an action off entirely when paired
with `onBreach: block` or `freeze`. Install-wide defaults live under `limits:` in
`km-config.yaml`; a profile's block overrides them. A frozen sandbox is released with
`km unlock`, and `km status` shows a Quotas section.

```yaml
spec:
  limits:
    github_comment:
      perHour: 20
      lifetime: 200
      onBreach: freeze
    email_send:
      perDay: 50
      onBreach: block
```

See [`docs/action-quotas.md`](action-quotas.md).

---

## Profile Inheritance

A profile can build on one or more parents with `extends`. Inheritance is resolved at
load time by `Resolve()`, and both `km validate` and `km create` validate the **merged**
result rather than the raw child — a partial child alone would fail the required-field
checks.

> **Phase 117 (2026-06-24) rewrote these semantics.** `extends` gained the list form and
> merging became a uniform deep merge. Most importantly, **list fields now union**
> instead of being replaced. Guidance written against the older single-parent,
> replace-everything model no longer describes what happens.

### The `extends` field

Accepts either a single parent name (the original form, still supported) or an **ordered
list**. Bases resolve left to right and the child applies last, so a later parent wins a
scalar conflict against an earlier one, and the child wins against all of them.

```yaml
extends: hardened                    # single parent

extends:                             # ordered list — later entries win
  - base/safenetwork
  - base/sidecars-all
  - base/budget-standard
```

### Merge rules

1. **Maps recurse at every depth.** Keys are unioned; there is no section-level
   replacement. Setting one field in `spec.lifecycle` no longer discards the parent's
   other lifecycle fields.

2. **Scalars: the child (or the rightmost parent) wins.**

3. **Lists concatenate and de-duplicate**, order-preserving, first occurrence kept. This
   applies to every list field — `initCommands`, `allowedDNSSuffixes`, `allowedHosts`,
   `allowedRepos`, `allowedRefs`, `email.allowedSenders`,
   `agent.claude.tools.autoApprove`, `rsyncPaths`, `artifacts.paths`. Object lists such
   as `additionalSnapshots` de-duplicate by deep equality.

4. **`metadata.labels` merges additively**, like any other map: child labels override
   same-key parent labels and parent-only labels survive.

5. **Max depth is 10**, and cycles are rejected. Diamond inheritance is fine — each node
   is resolved once and memoized.

6. **Resolution order** — built-in profiles first, then `profile_search_paths` on disk.

7. **`extends` is cleared** on the resolved profile.

### v1 narrowing limitation

Because lists union, **a child cannot shrink a base's list.** Extending a base with ten
allowed DNS suffixes and declaring two of your own gives you twelve, not two. To run with
a narrower allowlist, compose from a narrower base or keep that field in the leaf profile
entirely. A `!replace` directive is a deferred follow-up.

### Bool zero-value trap

A fragment that writes a whole block containing non-pointer bools pushes their
zero values (`false`) onto every child. Keep mixed-bool blocks such as `spec.runtime` in
the leaf profile rather than in a shared fragment.

### Abstract fragments

Files under `profiles/base/` set [`metadata.abstract: true`](#metadataabstract) and are
partial by design: `km validate` skips them and `km create` refuses them. Use
[`spec.execution.initCommandsAppend`](#specexecutioninitcommandsappend) when a leaf needs
install steps to run *after* everything its bases installed.

### Example

```yaml
# my-profile.yaml — composes three shipped fragments, then narrows what it can
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: my-profile
  labels:
    team: platform
extends:
  - base/safenetwork
  - base/sidecars-all
  - base/budget-standard

spec:
  # Scalars below override whatever the bases set.
  lifecycle:
    ttl: "8h"
    idleTimeout: "2h"
    teardownPolicy: destroy

  network:
    egress:
      # These are ADDED to the suffixes base/safenetwork already allows.
      allowedDNSSuffixes:
        - ".internal.example.com"
      allowedHosts:
        - "artifacts.internal.example.com"

  execution:
    # Runs after every initCommands entry merged from the bases.
    initCommandsAppend:
      - "npm install -g @my-org/internal-cli"
```

See [`OPERATOR-GUIDE.md` § Composable inheritance](../OPERATOR-GUIDE.md) for the full
authoring guide and the shipped fragment library.

---

## Semantic Validation Rules

Beyond JSON Schema structural validation, the following semantic rules are enforced:

| Rule | Path | Description |
|------|------|-------------|
| TTL >= idleTimeout | `spec.lifecycle.ttl` | The TTL must not be shorter than the idle timeout. A sandbox cannot idle out after it has already expired. |
| maxLifetime >= TTL | `spec.lifecycle.maxLifetime` | When both are set, the lifetime cap must not be shorter than the TTL — otherwise the sandbox is un-extendable the moment it boots. Equal is allowed and means "no extensions". Skipped when `ttl` is the `0` sentinel. |
| Valid substrate | `spec.runtime.substrate` | Must be `ec2`, `ecs`, or `docker` (also enforced by schema enum). |
| Valid enforcement | `spec.network.enforcement` | Must be `proxy`, `ebpf`, or `both` (also enforced by schema enum). |
| eBPF is EC2-only | `spec.network.enforcement` | eBPF enforcement (`ebpf` or `both`) is EC2-only. On ECS or Docker substrates, proxy enforcement is used regardless. |

---

## Built-in Profiles

Seven built-in profiles ship with Klanker Maker: `open-dev`, `restricted-dev`, `hardened`, `sealed`, `goose`, `ao`, and `codex`. These range from permissive development (`open-dev`) to maximum containment (`sealed`), plus tool-specific agent profiles. The `learn` profile (in `profiles/learn.yaml`) is not a built-in but is documented separately below for traffic observation workflows.

### `open-dev`

Permissive development profile. Broad package registry and GitHub access, wide ref patterns, full agent tooling.

```yaml
apiVersion: klankermaker.ai/v1alpha2
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
  iam:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1, us-west-2]
  sidecars:
    dnsProxy:  { enabled: true, image: "km-dns-proxy:latest" }
    httpProxy: { enabled: true, image: "km-http-proxy:latest" }
    auditLog:  { enabled: true, image: "km-audit-log:latest" }
    tracing:   { enabled: true, image: "km-tracing:latest" }
  observability:
    commandLog: { destination: cloudwatch, logGroup: "/klanker-maker/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klanker-maker/network" }
    claudeTelemetry:
      enabled: true
      logPrompts: true
      logToolDetails: true
```

### `restricted-dev`

Restricted development profile. Organization-scoped repos, limited refs, reduced agent concurrency.

```yaml
apiVersion: klankermaker.ai/v1alpha2
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
  iam:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
  sidecars:
    dnsProxy:  { enabled: true, image: "km-dns-proxy:latest" }
    httpProxy: { enabled: true, image: "km-http-proxy:latest" }
    auditLog:  { enabled: true, image: "km-audit-log:latest" }
    tracing:   { enabled: true, image: "km-tracing:latest" }
  observability:
    commandLog: { destination: cloudwatch, logGroup: "/klanker-maker/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klanker-maker/network" }
    claudeTelemetry:
      enabled: true
      logPrompts: false
      logToolDetails: true
```

### `hardened`

Production-grade profile. Minimal network access, single command, read-only agent tooling.

```yaml
apiVersion: klankermaker.ai/v1alpha2
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
  iam:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
  sidecars:
    dnsProxy:  { enabled: true, image: "km-dns-proxy:latest" }
    httpProxy: { enabled: true, image: "km-http-proxy:latest" }
    auditLog:  { enabled: true, image: "km-audit-log:latest" }
    tracing:   { enabled: true, image: "km-tracing:latest" }
  observability:
    commandLog: { destination: cloudwatch, logGroup: "/klanker-maker/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klanker-maker/network" }
    claudeTelemetry:
      enabled: true
      logPrompts: false
      logToolDetails: false
    tlsCapture:
      enabled: true
      libraries: [openssl]
      capturePayloads: false
```

### `sealed`

Maximum restriction. No network egress, no source access, no commands, single-task agent.

```yaml
apiVersion: klankermaker.ai/v1alpha2
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
  iam:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
  sidecars:
    dnsProxy:  { enabled: true, image: "km-dns-proxy:latest" }
    httpProxy: { enabled: true, image: "km-http-proxy:latest" }
    auditLog:  { enabled: true, image: "km-audit-log:latest" }
    tracing:   { enabled: true, image: "km-tracing:latest" }
  observability:
    commandLog: { destination: cloudwatch, logGroup: "/klanker-maker/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klanker-maker/network" }
    claudeTelemetry:
      enabled: true
      logPrompts: false
      logToolDetails: false
    tlsCapture:
      enabled: true
      libraries: [openssl]
      capturePayloads: false
```

### `goose`

Goose AI agent (Block) with Bedrock access, Claude Code, Codex, MCP extensions, OTEL telemetry, EFS shared storage, eBPF gatekeeper enforcement, email, and hibernation support.

```yaml
apiVersion: klankermaker.ai/v1alpha2
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
  iam:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
  sidecars:
    dnsProxy:  { enabled: true, image: "km-dns-proxy:latest" }
    httpProxy: { enabled: true, image: "km-http-proxy:latest" }
    auditLog:  { enabled: true, image: "km-audit-log:latest" }
    tracing:   { enabled: true, image: "km-tracing:latest" }
  observability:
    commandLog: { destination: cloudwatch, logGroup: "/klanker-maker/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klanker-maker/network" }
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
```

### `codex`

OpenAI Codex agent sandbox with proxy enforcement, hibernation, email, and OTEL telemetry.

```yaml
apiVersion: klankermaker.ai/v1alpha2
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
  iam:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
  sidecars:
    dnsProxy:  { enabled: true, image: "km-dns-proxy:latest" }
    httpProxy: { enabled: true, image: "km-http-proxy:latest" }
    auditLog:  { enabled: true, image: "km-audit-log:latest" }
    tracing:   { enabled: true, image: "km-tracing:latest" }
  observability:
    commandLog: { destination: cloudwatch, logGroup: "/klanker-maker/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klanker-maker/network" }
    claudeTelemetry:
      enabled: true
      logPrompts: true
      logToolDetails: true
  email:
    signing: required
    verifyInbound: required
    encryption: required
    allowedSenders: ["self"]
```

### `ao`

Multi-agent orchestration sandbox with Claude Code, Codex, Composio's agent-orchestrator, eBPF gatekeeper enforcement, email, and hibernation support.

```yaml
apiVersion: klankermaker.ai/v1alpha2
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
  iam:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
  sidecars:
    dnsProxy:  { enabled: true, image: "km-dns-proxy:latest" }
    httpProxy: { enabled: true, image: "km-http-proxy:latest" }
    auditLog:  { enabled: true, image: "km-audit-log:latest" }
    tracing:   { enabled: true, image: "km-tracing:latest" }
  observability:
    commandLog: { destination: cloudwatch, logGroup: "/klanker-maker/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klanker-maker/network" }
    claudeTelemetry:
      enabled: true
      logPrompts: true
      logToolDetails: true
  email:
    signing: required
    verifyInbound: required
    encryption: required
    allowedSenders: ["self"]
```

### `learn` (not a built-in -- lives in `profiles/learn.yaml`)

Permissive profile designed for traffic observation. Wide-open DNS suffixes covering common TLDs, `enforcement: both` for eBPF + proxy capture, `privileged: true` for sudo access, and `learnMode: true` to record traffic. Use with `km shell --learn` to generate a minimal SandboxProfile from observed traffic.

```yaml
apiVersion: klankermaker.ai/v1alpha2
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
    commandLog: { destination: cloudwatch, logGroup: "/klanker-maker/sandboxes" }
    networkLog: { destination: cloudwatch, logGroup: "/klanker-maker/network" }
    tlsCapture:
      enabled: true
    learnMode: true
  budget:
    compute:
      maxSpendUSD: 2.00
    ai:
      maxSpendUSD: 0.00
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
apiVersion: klankermaker.ai/v1alpha2
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
```

### Injecting secrets via SSM Parameter Store

```yaml
spec:
  iam:
    roleSessionDuration: "1h"
    allowedRegions: [us-east-1]
    allowedSecretPaths:
      - "/klanker-maker/sandbox/api-keys/github"
      - "/klanker-maker/sandbox/api-keys/npm"
```
