# Wiz Runtime Sensor on EC2 sandboxes

Opt-in runtime visibility for the operator's Wiz tenant into what an
autonomous agent actually does inside an EC2 sandbox: process execution, file
activity, and network behaviour. Pure profile composition — no
`SandboxProfile` schema change, no per-sandbox default.

Full design and rationale: `docs/superpowers/specs/2026-08-25-wiz-sensor-design.md`.
This doc is the operator runbook.

## 1. What it is

`profiles/base/security/wiz.yaml` is an abstract fragment (`metadata.abstract:
true`) that a leaf profile opts into via `extends:`. It does two things:

- Declares two SSM SecureString paths in `spec.iam.allowedSecretPaths` so the
  sandbox role can fetch the Wiz sensor's enrollment credential.
- Appends `initCommandsAppend` steps that download and run Wiz's public
  installer, then lay a hardening drop-in over the `wiz-sensor.service` unit
  the installer creates.

Demo leaf: `profiles/wiz-demo.yaml` (`extends: [base/platform,
base/os/debian, base/network/locked, base/security/wiz]`). It is a working
example to validate and copy from, not something to run as-is in production
without filling in the version pin below.

Nothing here is fleet-mandatory. A profile that does not `extends:
base/security/wiz` is unaffected.

## 2. Prerequisite: DNS allowlist + create the two SSM parameters — BEFORE any profile extends the fragment

**DNS allowlist (already handled, read this anyway).** Root traffic bypasses
the iptables DNAT and the eBPF cgroup, but NOT DNS resolution. On `ebpf`/`both`
enforcement (e.g. `base/network/locked`, which `profiles/wiz-demo.yaml`
inherits), `userdata.go:4737` routes ALL DNS — root's included — through the
enforcer's resolver at `127.0.0.1`, and the resolver returns NXDOMAIN for
anything not in `spec.network.egress.allowedDNSSuffixes`, with no uid or
cgroup exemption. `profiles/base/security/wiz.yaml` already adds a `.wiz.io`
suffix entry to cover the installer, package hosts, enrollment, and telemetry
endpoints, so you don't need to add this yourself — but be aware it's there
if you're composing your own fragment from scratch, or if you're on
`proxy`-only enforcement (where DNS isn't rewritten, so this doesn't apply).

**This ordering is not a nicety. Get it wrong and every sandbox that extends
the fragment fails to boot.**

The sandbox bootstrap runs under `set -euo pipefail`
(`pkg/compiler/userdata.go:36`). The SSM-secret fetch is a plain assignment —
`SECRET_VALUE=$(aws ssm get-parameter …)` — inside that scope, so a missing or
inaccessible parameter makes `aws ssm get-parameter` exit non-zero and the
**entire userdata script terminates right there**, before the script ever
reaches the `if [ $? -eq 0 ]` guard that would otherwise log a soft warning.
There is no partial boot, no sandbox to `km shell` into to fix it after the
fact — cloud-init just stops. `profiles/base/gpu/serve.yaml:214` records the
identical failure mode from an earlier CPU rehearsal.

So: create both parameters first, confirm they read back, and only then
`km create` (or `extends:`) anything that pulls in `base/security/wiz`.

```bash
PREFIX=$(yq -r .resource_prefix km-config.yaml)

aws ssm put-parameter --type SecureString \
  --name "/$PREFIX/wiz/wiz-api-client-id" \
  --value "<SENSOR service-account client id>"

aws ssm put-parameter --type SecureString \
  --name "/$PREFIX/wiz/wiz-api-client-secret" \
  --value "<SENSOR service-account client secret>"

# Verify both are readable before creating any sandbox that uses the fragment:
aws ssm get-parameter --name "/$PREFIX/wiz/wiz-api-client-id" --with-decryption --query Parameter.Value --output text
aws ssm get-parameter --name "/$PREFIX/wiz/wiz-api-client-secret" --with-decryption --query Parameter.Value --output text
```

**Use a `SENSOR`-type Wiz service account for this credential, not a general
API client.** Wiz's public docs list `SENSOR` as a distinct service-account
type alongside `THIRD_PARTY`, `KUBERNETES_ADMISSION_CONTROLLER`, and `BROKER`.
This matters for the threat model in §5 below: root on the box can read the
injected credential, and a narrowly `SENSOR`-scoped account limits what that
credential is worth if it leaks or is reused.

**KMS needs no extra setup.** The sandbox role's `kms:Decrypt` grant is an
account-wide `key/*` statement scoped by `kms:ViaService =
ssm.<region>.amazonaws.com`, created unconditionally for every install. A
SecureString encrypted with any key — including the SSM default
`alias/aws/ssm` — decrypts fine. There is no `--key-id` caveat to worry about
here.

The parameter names above are deliberate: userdata derives the sandbox's
exported env var name from the SSM parameter's basename
(`basename | upper | '-'→'_'`), so `wiz-api-client-id` becomes
`WIZ_API_CLIENT_ID` and `wiz-api-client-secret` becomes
`WIZ_API_CLIENT_SECRET` — exactly the two variables Wiz's installer reads. Do
not rename them unless you also update the fragment.

## 3. Pin `WIZ_SENSOR_VERSION`

`profiles/base/security/wiz.yaml` ships with:

```yaml
- "WIZ_SENSOR_VERSION=REPLACE-ME sh /tmp/wiz_install.sh && rm -f /tmp/wiz_install.sh"
```

`REPLACE-ME` is deliberate — this doc does not invent a version number.
Before using the fragment, replace it with a real value from Wiz's
`sensor_linux_version_mapping.yaml`.

Pinning is mandatory, not optional, because of how km sandboxes work: they
are created and destroyed constantly, and the installer defaults to
"latest" when no version is given. Left unpinned, two sandboxes created an
hour apart can silently run different sensor builds, and a bad upstream Wiz
release reaches every new sandbox in the fleet immediately, with no km-side
gate to catch it. Pinning turns a version bump into a normal, reviewable
profile change instead of an uncontrolled supply-chain surface.

## 4. Deploy surface

```bash
make build            # compiler carries {{prefix}} interpolation + secret_paths emission
make build-lambdas     # create-handler compiles profiles on the remote-create path
km init --dry-run=false   # ec2spot v1.4.0 IAM grant + create-handler need a full terragrunt apply
```

**NOT `km init --sidecars`** — no sidecar binary changed, and the new IAM
statement plus the `ec2spot` module version bump both require a full
terragrunt apply.

Existing sandboxes are unaffected and gain nothing retroactively — they need
`km destroy && km create` to pick up the fragment. A profile that does not
`extends: base/security/wiz` (and does not use any `{{prefix}}` secret path)
compiles byte-identically to before this feature existed.

## 5. Threat model

**This does not make the sensor tamper-proof.** On a `spec.execution.privileged:
true` sandbox the agent has sudo and can `systemctl stop` or `mask` the unit,
`chattr -i` the hardening drop-in, or firewall off Wiz's endpoints outright.
No on-box measure survives root — this is the same tradeoff CLAUDE.md already
documents for `km-netpolicy`: *"on `privileged: true` the sandbox has sudo and
can clear `+a` — but it can equally stop the proxies, so the guarantee is
meaningful on unprivileged boxes."*

What the fragment's L0 hardening does buy:

- `Restart=always` (via the systemd drop-in) self-heals a `kill -9` or a
  crash.
- `chattr +i` on the km drop-in blocks modification or deletion of the
  hardening override — and because Wiz's installer owns the unit file itself,
  a package upgrade can't quietly drop `Restart=always` either. On an
  unprivileged sandbox this is a real kernel guarantee; on a privileged one
  it's a speed bump root can clear.
- Installing via `initCommandsAppend` means the sensor enrolls and starts
  reporting before any agent turn executes, so tampering shows up as a
  healthy-then-silent transition rather than a sensor that was simply never
  there.

**The real control is `spec.execution.privileged: false`.** A profile that
wants runtime monitoring to mean something should not also grant the sandbox
user sudo — that's why the demo leaf pins `privileged: false` explicitly and
comments on why.

**Detection belongs off-box, in the Wiz tenant.** Wiz already alerts when a
previously-healthy sensor stops phoning home. That alerting lives entirely in
the separate Wiz account and needs no km code or additional wiring here.

## 6. Correlation

km already tags every resource it creates with `km:sandbox-id`, including the
instance itself. Wiz's cloud connector ingests EC2 instance tags into its
Security Graph, and the sensor identifies its own host via IMDS — so Wiz can
join sensor → instance → tags without any extra work on km's side.

The gap is ephemerality, not mechanism: the connector syncs inventory on its
own schedule rather than in real time. A sandbox that is created, observed,
and torn down entirely within one sync interval never becomes a tagged cloud
asset, so its sensor telemetry has nothing to auto-correlate against — the
instance ID is still visible, so a manual `km list` / instance-ID lookup
still works, it just isn't automatic. How often this actually bites depends
on the connector's sync cadence for this account, which is one of the open
items below.

## 7. Troubleshooting

**The sensor never installs at all.** A likely cause is a missing `.wiz.io`
DNS allowlist entry (see §2) — `profiles/base/security/wiz.yaml` ships one,
so this usually means a custom fragment or a stale `extends:` resolution
dropped it. The symptom is misleading: `initCommandsAppend` runs under
`set -e` inside `km-init.sh`, so the very first line —
`curl -fsSL https://downloads.wiz.io/... -o /tmp/wiz_install.sh` — fails to
resolve, aborts the rest of `km-init.sh`, and `userdata.go:4941` reports it
as `No init script found in S3 (skipped)`. That message looks like the init
script never made it to S3, not like a blocked DNS lookup — if you see it,
check DNS resolution before you go chasing an S3 upload problem:

```bash
km shell <sandbox-id>
systemctl status km-ebpf-enforcer   # confirm enforcement mode
getent hosts downloads.wiz.io       # NXDOMAIN here = missing allowlist entry
```

**Is the sensor running and hardened?**

```bash
km shell <sandbox-id>
systemctl status wiz-sensor
systemctl show wiz-sensor -p Restart     # must print: Restart=always
```

If `Restart` is not `always`, the km drop-in
(`/etc/systemd/system/wiz-sensor.service.d/10-km-hardening.conf`) either
didn't apply or `chattr +i` was cleared by a privileged agent — see §5.

**Sensor never enrolls / no data in the Wiz tenant.**

Check cloud-init's output for the sandbox. A line like:

```
WARNING: failed to fetch secret ...
```

means the boot's SSM fetch failed — either the parameter is missing (see §2)
or the IAM grant for it never landed. The most common cause of the latter is
timing: confirm the sandbox was created **after** the `km init
--dry-run=false` that applied `ec2spot` v1.4.0. A sandbox created against an
older `ec2spot` pin has no `ssm:GetParameter` statement for these paths at
all, regardless of whether the parameters themselves exist.

Remember also: per §2, if the SSM fetch fails outright the boot doesn't just
warn — it **aborts**, so "the sensor never enrolled" and "the sandbox never
finished booting" may be the same underlying failure. Check `km list` /
`km status <id>` for a stuck/failed create before assuming the sensor alone
is at fault.

## 8. Open items

Two questions from the design spec (§10) are still unanswered and are not
blocking this fragment's existence, but do matter before wider rollout:

1. **The Wiz tenant's `WIZ_DOMAIN` and region.** Needed to resolve the
   tenant's repo and API hostnames concretely. This doc does not guess or
   invent a value — get it from the operator's Wiz tenant admin.
2. **The Wiz cloud connector's inventory sync cadence for this AWS account.**
   Determines how well tag-based correlation (§6) actually works for
   short-lived sandboxes versus how often it loses the race against teardown.

See the design spec's §10 for the full list of established-vs-still-needed
facts, including sources.
