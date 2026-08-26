# Wiz Sensor on EC2 sandboxes — design

**Date:** 2026-08-25
**Status:** Draft — awaiting review
**Scope:** Runtime Wiz Sensor on EC2 sandboxes, delivered as a composable profile
fragment, plus the platform fix that makes its credential delivery possible.

---

## 1. Goal

Run the Wiz Sensor as a root systemd daemon on opted-in EC2 sandboxes so the
operator's Wiz tenant (in a separate AWS account) gets runtime visibility into
what an autonomous agent actually does inside a sandbox: process execution, file
activity, and network behaviour.

Opt-in per profile via `extends:`, not fleet-mandatory.

## 2. Non-goals

- **Agentless account posture.** The Wiz Cloud Connector is a cross-account IAM
  role Wiz assumes into the sandbox account. One-time, out-of-band, no km change.
- **Image vulnerability scanning.** Wants the baked AMI, not ephemeral instances.
- **Docker/ECS substrates.** EC2 only. A host-level sensor's eBPF hooks already
  observe containers running on that host.
- **Tamper *prevention*.** See §6 — explicitly out of reach, by design.
- **Automated reaction to a disabled sensor.** Deferred; see §9.

---

## 3. Findings that shape the design

These were established by reading the code, and each one removed or redirected a
chunk of the obvious design.

### 3.1 Root traffic bypasses both enforcement layers — by design

```
pkg/compiler/userdata.go:4532   iptables -t nat -A OUTPUT -m owner --uid-owner 0 -j RETURN
pkg/compiler/userdata.go:178    # root processes are never enrolled into the sandbox cgroup
```

A root systemd unit is neither DNAT'd into the MITM proxy nor subject to the eBPF
cgroup allowlist. This is the same mechanism by which km's own sidecars reach AWS.

**Consequences:** the fragment needs no `allowedHosts` entry, no CA trust work,
and there is no cert-pinning risk. Even if the sensor were proxied, MITM is
selective: only bedrock / anthropic / openai / github / google hosts get
`MitmConnect`; everything else is a plain `OkConnect` tunnel
(`sidecars/http-proxy/httpproxy/proxy.go:751-753`).

*(Corrected 2026-08-26. This section previously claimed the fragment needs
**no** `spec.network` change at all — that is wrong for DNS.)* Root bypasses
the iptables DNAT and the eBPF cgroup, but **not** DNS resolution. On
`ebpf`/`both` profiles, `userdata.go:4737` rewrites `/etc/resolv.conf` to
`nameserver 127.0.0.1` system-wide (no uid exemption), and
`pkg/ebpf/resolver/resolver.go:272-276` returns NXDOMAIN for any
non-allowlisted name with no uid or cgroup check either. So on
`profiles/wiz-demo.yaml`, which inherits `base/network/locked`
(`enforcement: both`), root's `curl https://downloads.wiz.io/...` fails to
resolve unless the fragment adds a `.wiz.io` entry to
`spec.network.egress.allowedDNSSuffixes` — which it now does. The
connections themselves still go out unproxied as root; only the name lookup
needs the allowlist entry.

`initCommands` run at userdata §4930 — *after* enforcement comes up, but as root,
so the install-time download is likewise unconstrained.

### 3.2 `iam.allowedSecretPaths` is compiled but never granted on EC2

The field renders an `aws ssm get-parameter --with-decryption` loop into userdata
§3 (`pkg/compiler/userdata.go:495-518`), but the ec2spot role grants
`ssm:GetParameter` on exactly two resources, neither driven by the field:

```
infra/modules/ec2spot/v1.3.0/main.tf:428   .../${resource_prefix}/sandbox/${sandbox_id}/*
infra/modules/ec2spot/v1.3.0/main.tf:441   .../${resource_prefix}/slack/bridge-url
```

`compileSecrets` (`pkg/compiler/security.go:65`) flows into `CompiledSandbox.SecretPaths`
and reaches the Docker (`compose.go:131`) and ECS (`service_hcl.go:263`) substrates —
never the ec2spot IAM policy.

**So today, on the only substrate in real use, declaring a path yields
`AccessDenied` — and that ABORTS THE BOOT.**

*(Corrected 2026-08-26. This section previously said the loop "warns and
continues". It does not.)* The bootstrap runs under `set -euo pipefail`
(`userdata.go:36`) and the fetch loop sits at line 495, inside that scope.
`SECRET_VALUE=$(aws ssm get-parameter …)` is a standalone assignment, so a
non-zero exit terminates the script **before** the `if [ $? -eq 0 ]` guard is
ever reached — that guard is dead code on the failure path. The repo already
records this empirically at `profiles/base/gpu/serve.yaml:214`: *"a
bad/non-existent path makes the boot's `aws ssm get-parameter` fail under
`set -e` and BRICKS the boot (validated on the CPU rehearsal)."*

This is a latent platform bug independent of Wiz, and a more severe one than a
silent no-op: a documented profile field that **bricks any EC2 sandbox
declaring it**. Two shipped profiles — `gpu-llama-12x.yaml` and
`gpu-llama-48x.yaml` — carry an absolute `allowedSecretPaths` entry that no
`ec2spot` version has ever granted; they are removed as part of the
implementation.

Two consequences for this design:

1. The `ec2spot/v1.4.0` grant in §4 Part 1 is a **boot fix**, not merely a
   feature enabler.
2. **Creating the SSM parameters is a hard prerequisite**, not a setup nicety.
   An operator who extends the Wiz fragment before creating them bricks every
   sandbox that uses it. Boot-abort is a defensible fail-closed posture for a
   security control, so this design keeps it rather than softening shared boot
   semantics — but the operator runbook must state the ordering explicitly and
   give a verification command. Revisit if the footgun proves worse than the
   fail-closed property is worth.

### 3.3 `spec.secrets.sopsFile` is a scalar, and its output is agent-readable

`SopsFile string` (`pkg/profile/types.go:752`). Phase 117 deep-merge resolves
scalars as child/right-parent wins, so a base fragment setting `sopsFile:` would
silently clobber — or be clobbered by — the ten GPU profiles that declare their
own bundle.

Worse for this use case: the bundle lands at `/etc/sandbox-secrets.env`,
`chmod 0440 root:sandbox`, sourced into every login shell. **The agent being
monitored could read the sensor's credentials** and use them to deregister its own
sensor through the Wiz API — inverting the control.

By contrast, userdata §3 `export`s SSM-fetched values into the *bootstrap* shell.
They are visible to `initCommands` (which is what the fragment needs) but are
never written to `/etc/sandbox-secrets.env` and never reach an agent login shell.

**SOPS is therefore the wrong transport for this credential.** SSM is right — once
§3.2 is fixed.

---

## 4. Design

Three parts. Parts 1 and 2 are platform work; part 3 is the actual Wiz fragment
and is pure YAML once the first two land.

### Part 1 — Make `iam.allowedSecretPaths` real on EC2

- New immutable module dir `infra/modules/ec2spot/v1.4.0/` (copy of v1.3.0) with a
  new `secret_paths` variable, `list(string)`, default `[]`.
- A conditional IAM statement granting `ssm:GetParameter` on exactly the listed
  paths — least privilege, no wildcard expansion beyond what the operator wrote.
- Verify the existing `kms:Decrypt` statement (guarded by
  `kms:ViaService = ssm.<region>.amazonaws.com`, ~`main.tf:415`) covers the key
  used to encrypt these SecureStrings. **Verification item, not an assumption.**
- `infra/templates/sandbox/terragrunt.hcl` `locals.substrate_module_versions`:
  `ec2spot = "v1.4.0"` (REQ-125-SUBPIN — the per-substrate map, not a shared literal).
- `pkg/compiler/service_hcl.go` emits `secret_paths` into `module_inputs`, which
  `terragrunt.hcl` already merges (line 82).

### Part 2 — `{{prefix}}` interpolation, as the security guard

Profiles write SSM paths prefix-relative:

```yaml
spec:
  iam:
    allowedSecretPaths:
      - "{{prefix}}/wiz/wiz-client-id"
      - "{{prefix}}/wiz/wiz-client-secret"
```

- **Closed token set.** `{{prefix}}` is the only recognised token. An unknown
  token is a `km validate` **error**, not a passthrough.
- **Every entry MUST start with `{{prefix}}/`.** An absolute path is a validation
  error. This is the point: it makes it structurally impossible for a profile to
  grant the sandbox role access to SSM outside its own install's namespace. A
  profile cannot write `/*` and read the whole account.
- **Validation is shape-only.** `km validate` checks the token set and the leading
  segment without needing to know the prefix's value, so it works on a workstation
  with no configured install.
- **Interpolation happens at compile time**, reusing the pattern already at
  `service_hcl.go:964`. Remote create is covered: `create-handler/v1.0.0/main.tf:111`
  already sets `KM_RESOURCE_PREFIX = var.resource_prefix`.
- **Fail loud, do not default.** The existing sites fall back to `"km"` when the
  env var is empty. For an IAM grant that is unacceptable — on a non-default
  install it would silently render a policy pointing at another install's
  namespace. When any `{{prefix}}` path is present and `KM_RESOURCE_PREFIX` is
  unset, compilation **errors**.

Scope fence: interpolation applies to `iam.allowedSecretPaths` only in v1. Widening
it to arbitrary profile strings is a separate decision.

### Part 3 — `profiles/base/security/wiz.yaml`

Wiz's public installer (`https://downloads.wiz.io/sensor/sensor_install.sh`) settles
most of the mechanics — see §10. Two of its properties shape this fragment.

**The installer creates and enables `wiz-sensor.service` itself.** So we do NOT
author a unit. km's L0 hardening goes into a systemd **drop-in override**, which
has the side benefit of surviving a package upgrade:

```
/etc/systemd/system/wiz-sensor.service.d/10-km-hardening.conf
  [Service]
  Restart=always
  RestartSec=5
  StartLimitIntervalSec=0
```

**The credential env var names line up exactly, with no glue.** The installer reads
`WIZ_API_CLIENT_ID` / `WIZ_API_CLIENT_SECRET`; userdata §3 derives an env var name
as `basename | upper | '-'→'_'`. So parameters named
`{{prefix}}/wiz/wiz-api-client-id` and `{{prefix}}/wiz/wiz-api-client-secret`
inject as precisely the names the installer expects. Nothing has to translate
between the SSM fetch and the install, and no credential is ever written to a file
km owns.

```yaml
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: base-security-wiz
  abstract: true
spec:
  network:
    egress:
      allowedDNSSuffixes:           # DNS is not bypassed by root — see §3.1 correction
        - ".wiz.io"
  iam:
    allowedSecretPaths:            # list -> unions cleanly under Phase 117 merge
      - "{{prefix}}/wiz/wiz-api-client-id"
      - "{{prefix}}/wiz/wiz-api-client-secret"
  execution:
    initCommandsAppend:
      - "curl -fsSL https://downloads.wiz.io/sensor/sensor_install.sh | WIZ_SENSOR_VERSION=<PINNED> sh"
      - "install -d /etc/systemd/system/wiz-sensor.service.d && <write 10-km-hardening.conf>"
      - "systemctl daemon-reload && systemctl restart wiz-sensor"
      - "chattr +i /etc/systemd/system/wiz-sensor.service.d/10-km-hardening.conf"
```

**`WIZ_SENSOR_VERSION` must be pinned.** The installer defaults to latest. In a
fleet that recreates constantly, an unpinned installer means two sandboxes created
an hour apart can run different sensor builds, and a bad upstream release reaches
every new sandbox immediately. Pin it, and treat the bump as a normal profile change.

**Deliberate omissions:**

| Omitted | Why |
|---|---|
| `spec.network.egress.allowedHosts` | Root bypasses DNAT and the cgroup (§3.1). Also, the installer honours its own `WIZ_HTTP_PROXY_URL`, not `HTTPS_PROXY`, so nothing routes through km's proxy by accident. `allowedDNSSuffixes` is NOT omitted — see the §3.1 correction: DNS is not bypassed by root, so the fragment adds a `.wiz.io` suffix entry |
| `spec.secrets` | Scalar — would clobber the GPU profiles (§3.3) |
| `spec.runtime` | Phase 117 bool zero-value trap — mixed-bool blocks stay in the leaf |
| Arch guard | The installer supports `x86_64`, `aarch64`, `s390x`, so both km AMI families are covered |
| Kernel guard | Installer minimum is 4.18; Ubuntu 24.04 ships 6.8 and AL2023 ships 6.1 |

---

## 5. Deploy surface

`make build` (compiler carries the new `secret_paths` emission and the
interpolation) → `make build-lambdas` (create-handler compiles profiles on the
remote path) → `km init --dry-run=false`.

**NOT `--sidecars`** — no sidecar binary changes, and the new IAM statement plus
the module version bump need a full terragrunt apply.

Existing sandboxes are unaffected and gain nothing until `km destroy && km create`.
The fragment is dormant for every profile that does not `extends:` it; a profile
that does not use `{{prefix}}` paths compiles byte-identically to today.

---

## 6. Threat model — what this does and does not guarantee

**It does not make the sensor tamper-proof.** On a `privileged: true` sandbox the
agent has sudo and can `systemctl stop`, `mask` the unit, `chattr -i` the binary,
or `iptables -A OUTPUT -d <wiz> -j DROP`. No on-box measure survives root.

What L0 buys:

- `Restart=always` self-heals a `kill -9` or a crash.
- `chattr +i` on the km drop-in blocks modification and deletion of the hardening
  override — and because the installer owns the unit itself, a package upgrade
  cannot quietly drop the `Restart=always` policy either. On a
  `privileged: false` box this is a real kernel guarantee; on a privileged box it
  is a speed bump that root can clear.
- Installing in `initCommandsAppend` means the sensor enrolls and reports **before
  any agent turn executes**, so tampering is always visible as a
  healthy-then-silent transition rather than an absence.

This is the same tradeoff CLAUDE.md already records for `km-netpolicy`: *"on
`privileged: true` the sandbox has sudo and can clear `+a` — but it can equally
stop the proxies, so the guarantee is meaningful on unprivileged boxes."*

**The actual control is `privileged: false`.** Profiles that want a meaningful
runtime-monitoring guarantee should not grant the sandbox user sudo.

**Detection is the operator's Wiz tenant**, which already flags a sensor that
stops phoning home. That alerting lives in the separate Wiz account and needs no
km code.

---

## 7. Testing

**Unit / local:**
- `km validate` on a leaf wired to the fragment (validates *merged* bytes).
- Validation rejects: absolute path, unknown token, missing leading `{{prefix}}/`.
- Compilation errors when a `{{prefix}}` path is present and `KM_RESOURCE_PREFIX`
  is unset.
- `scripts/validate-all-profiles.sh` — `profiles/base/` is auto-excluded.
- Golden check: a profile with no `{{prefix}}` paths compiles unchanged.

**Live UAT on one sandbox:**
1. `systemctl is-active wiz-sensor` → active, and the km drop-in is in
   effect (`systemctl show wiz-sensor -p Restart` → `always`).
2. Sensor appears as an asset in the Wiz tenant.
3. `/etc/km/wiz.env` is `0400 root`; the credential does **not** appear in
   `/etc/sandbox-secrets.env` nor in a `sandbox` login shell's env.
4. km's own `km-ebpf-enforcer` is still active — **coexistence is the highest-risk
   item**: km loads cgroup BPF (`connect4`/`sendmsg4`/`sockops`/`egress`) plus SSL
   uprobes; the sensor loads its own kprobes/tracepoints.
5. Sandbox egress policy still holds for the sandbox user: an allowed host
   connects, `evil.example.com` is blocked.
6. `kill -9` the sensor → it comes back within ~5s.
7. Non-default `resource_prefix` install renders IAM against that prefix.

---

## 8. Risks

| Risk | Handling |
|---|---|
| Wiz Sensor eBPF conflicts with km's programs | Live UAT item 4; no way to settle from code |
| Asset churn — every `km create` is a new asset, TTL-reaped boxes never shut down cleanly, so ghosts accumulate | Accepted. No per-seat cost (unlimited seats), so this is hygiene and correlation only |
| A sandbox shorter-lived than the connector's inventory sync never becomes a tagged cloud asset, so its sensor telemetry cannot auto-correlate | Bounded by the sync cadence (§10); falls back to manual lookup by instance ID. Sensor-side labels would close it if supported |
| Install adds boot time to every create | Accepted; AMI bake is the follow-up (§9) |
| Widening the sandbox role's SSM reach | Bounded structurally by the `{{prefix}}/` rule (§4 Part 2) |
| `ec2spot` version bump touches every new sandbox launch | Additive variable, default `[]`; profiles not using the field are unaffected |

## 9. Follow-ups (explicitly out of scope)

- **AMI bake** (`km ami bake`) — sensor pre-installed and enrolled in the image;
  removes per-boot install time and the "profile forgot the fragment" failure mode.
- **`wiz-sensor-health` check** on the Phase 116 runner — poll the Wiz API for
  sandboxes whose sensor went unhealthy, dispatch to Slack. Same shape as the
  existing `profiles/checks/wiz-intel` snippet.
- **Fatal reaction** — "sensor unhealthy → freeze or destroy that sandbox."
  Requires a new action type in `ttl-handler`; today check triggers can only
  dispatch a sandbox prompt.
- **`sopsFiles: []`** — make the SOPS field a list so base fragments can carry
  bundles without clobbering. Orthogonal to this work, but the same class of bug.

---

## 10. Inputs required from the Wiz tenant

Everything under **Established** was read from Wiz's public installer and public
docs on 2026-08-25 and needs no tenant access. Everything under **Still needed** is
tenant- or contract-specific and has to come from the operator.

### Established from public sources

| Fact | Value | Consequence for this design |
|---|---|---|
| Install entry point | `https://downloads.wiz.io/sensor/sensor_install.sh` | Single `initCommandsAppend` line |
| Package / repos | `wiz-sensor`, from `https://dpkg.${WIZ_DOMAIN}/projects/sensor-repos…` (apt) and `https://rpm.${WIZ_DOMAIN}/…` (yum/dnf) | Both km AMI families are covered |
| Systemd unit | `wiz-sensor.service`, created and enabled by the installer | **km writes a drop-in override, not a unit** (§4 Part 3) |
| Credential env vars | `WIZ_API_CLIENT_ID`, `WIZ_API_CLIENT_SECRET` | Two SSM SecureStrings; names chosen so userdata §3 derives these exactly |
| Service-account types | `THIRD_PARTY`, **`SENSOR`**, `KUBERNETES_ADMISSION_CONTROLLER`, `BROKER` | A purpose-scoped `SENSOR` account exists — use it, not a general API client (§6) |
| Version pinning | `WIZ_SENSOR_VERSION` env var; mapping at `sensor_linux_version_mapping.yaml` | Pin it; unpinned is a fleet-wide supply-chain exposure |
| Architectures | `x86_64`, `aarch64`, `s390x` | No arch guard needed — arm64/Graviton is supported |
| Minimum kernel | 4.18 (3.10 with `WIZ_DISABLE_RUNTIME_DETECTION=1`) | Ubuntu 24.04 (6.8) and AL2023 (6.1) both clear it |
| Supported distros | incl. Debian, Ubuntu, RedHat, CentOS, Amazon, Rocky, AlmaLinux | Both km AMI families supported |
| Reboot | Not required | Safe inside userdata |
| Proxy vars | `WIZ_HTTP_PROXY_URL` / `_USERNAME` / `_PASSWORD` / cert vars — **not** `HTTPS_PROXY` | Confirms §3.1: nothing routes through km's proxy implicitly |
| Config paths | `engine.env`, `config.yaml`, `persistent_id.yml`, `installer.env` under the sensor host store | `persistent_id.yml` implies enrollment persists across restarts |
| Auth endpoint | `https://auth.app.wiz.io` | Needed only if the sensor is ever de-privileged out of the root bypass |
| API endpoint | `https://api.<region>.app.wiz.io/graphql` (e.g. `us20`) | Region-specific; feeds the deferred health check (§9) |

### Still needed from the operator

**Blocking — these change what gets built:**

1. **The tenant's `WIZ_DOMAIN` and region** — needed to resolve the repo and API
   hostnames concretely.
2. **The Wiz cloud connector's inventory sync cadence for the sandbox account** —
   see *Correlation* below. This decides whether tag-based correlation actually
   works for short-lived sandboxes or is a race they usually lose.

**Correlation: use EC2 tags, not sensor labels.**

km already tags the instance and its volumes `km:sandbox-id`
(`infra/modules/ec2spot/v1.3.0/main.tf:617-626`), along with every other resource
it creates. Wiz's cloud connector ingests instance tags into the Security Graph,
and the sensor identifies its own instance via IMDS — so Wiz joins
sensor → instance → tags without km doing anything. **This is the primary path and
it costs nothing.**

The caveat is ephemerality, not mechanism. The connector syncs inventory on a
schedule rather than in real time, so a sandbox can be created, monitored, and
reaped between two syncs. When that happens the instance never becomes a cloud
asset, and the sensor's telemetry has no tagged asset to join against — the
instance ID is still there, so correlation is possible manually via `km list`, but
not automatic.

Sensor-side enrollment labels would be immune, since enrollment happens in the
first minute of boot. If the sensor supports custom labels, set `km:sandbox-id`
there as belt-and-braces. If it does not, tags plus the connector remain the
answer, and the residual gap is bounded by the sync cadence — which is why that
cadence is worth knowing (blocking item 2).

**Answered (2026-08-25):**

- **Licensing — not a constraint.** The operator's tenant has unlimited seats, so
  ephemeral-sandbox churn carries no per-seat cost. Ghost assets remain a
  *hygiene and correlation* problem, not a billing one.
- **Credential scoping — use a `SENSOR` service account.** Wiz's public docs list
  `SENSOR` as a distinct service-account type alongside `THIRD_PARTY` and `BROKER`.
  Enrollment therefore does not require a general-purpose API client. Confirm the
  exact permission set with the Wiz tenant admin, but the design should assume the
  narrow type. This is what makes §6 defensible: root on the box can read the
  injected credential, but a `SENSOR`-scoped one is of little use beyond enrolling.
- **Outpost — orthogonal, not blocking.** Wiz Outpost relocates *agentless workload
  and snapshot scanning* into the customer's own environment, sending only metadata
  to the Wiz backend. It is not a telemetry broker for VM runtime sensors — the
  `wiz-broker` tunnel component belongs to the Kubernetes connector chart, which is
  a different path. **Runtime sensors talk to Wiz SaaS either way**, so §3.1 holds
  whether or not an Outpost is later deployed. If one is deployed, it serves the
  agentless-posture goal already scoped out in §2, and would be its own piece of
  work — most likely touching VPC reachability and Phase 125 private-subnet
  placement rather than this fragment.

**Non-blocking — fill in before rollout:**

3. **Deregistration:** the installer ships no uninstall verb. Is there an API call
   or command suitable for `ExecStop`? (TTL-reaped and spot-interrupted boxes never
   run it, so ghosts are unavoidable regardless.)
4. **Stale-asset policy:** do assets auto-expire on last-seen, and after how long?
5. **Documented conflicts with other eBPF agents** — retires the highest-risk UAT
   item (§7 item 4) before implementation rather than during.
6. **Resource footprint** (CPU/RAM) — some profiles run on `t3.medium`.
7. **Sensor health / last-seen API query** — feeds the deferred `wiz-sensor-health`
   check (§9).

### Sources

- <https://downloads.wiz.io/sensor/sensor_install.sh> — the installer itself
- <https://www.wiz.io/blog/wiz-runtime-sensor-for-linux> — product overview
- <https://aws.amazon.com/marketplace/pp/prodview-debmfdjvlvfvo> — AWS Marketplace listing
- <https://deepwiki.com/wiz-sec/charts/11.1-standard-kubernetes-deployment> — service-account types, `wiz-broker` scope
- <https://aws.amazon.com/marketplace/pp/prodview-f5su6hitnszcu> — Wiz Outpost Workload Scanner (scope of Outpost)
