<!--
  MAINTAINER NOTE — update this file BEFORE tagging each release.

  Its contents are injected VERBATIM into every GitHub release's notes,
  between the install header and goreleaser's auto-generated changelog.
  Wiring: .github/workflows/release.yml ("Load release highlights" step) →
  $KM_RELEASE_HIGHLIGHTS → .goreleaser.yaml `release.header` template.

  Keep it to the few MAJOR, human-curated additions for THIS release (the
  auto-changelog already lists every commit). HTML comments like this one
  are hidden in GitHub's rendered view. If this file is empty/absent the
  section is omitted gracefully.
-->
## ✨ Major additions highlighted

v0.8.2 let the outside world **wake** a sandbox. This release lets it **watch** one.

A Wiz Automation Rule can already fire an alert at a webhook and start an agent working. What
was missing was the other direction: nothing reported what that agent actually *did* once it was
running. This release closes the loop.

### 🛡️ Wiz Runtime Sensor on EC2 sandboxes

A new opt-in profile fragment installs the Wiz Runtime Sensor as a root systemd daemon, so an
autonomous agent's process, file, and network activity shows up in your Wiz tenant.

```yaml
extends:
  - base/platform
  - base/os/debian
  - base/network/locked
  - base/security/wiz      # <- that's the whole opt-in
```

Dormant by default: a profile that doesn't extend it is byte-identical to v0.8.2.

**It is deliberately not tamper-proof, and the runbook says so.** On `privileged: true` the agent
has sudo and can stop any daemon. On-box hardening is a systemd drop-in plus `chattr +i` — a
speed bump, not a control. The real control is `privileged: false`, and detection belongs off-box
in your Wiz tenant.

There are now three Wiz integrations pointing in three directions — km **pulls** from Wiz
(`checks.triggers`), Wiz **pushes** to km (`webhooks:`), and a sandbox **reports to** Wiz (this
one). They share no code or config; `docs/wiz-sensor.md` § 7 tells them apart.

### 🔐 `iam.allowedSecretPaths` now actually works — and never did before

Shipping the sensor meant getting a credential onto the box, which surfaced a latent platform
bug worth its own headline.

`iam.allowedSecretPaths` is a documented SandboxProfile field. It rendered an
`aws ssm get-parameter` loop into EC2 user-data — but was **never plumbed into the sandbox role's
IAM**. And it did not degrade quietly: the bootstrap runs under `set -euo pipefail` and the fetch
is a standalone assignment, so the denied call **aborted the boot** before reaching the guard
that was supposed to tolerate it. That guard was dead code.

New `ec2spot/v1.4.0` grants `ssm:GetParameter` on exactly the declared paths. Two shipped
profiles that declared a path no module version ever granted have been cleaned up.

### 🧭 `{{prefix}}` paths — a security guard, not ergonomics

Secret paths are now written prefix-relative:

```yaml
iam:
  allowedSecretPaths:
    - "{{prefix}}/wiz/wiz-api-client-id"
    - "{{prefix}}/wiz/wiz-api-client-secret"
```

These paths compile straight into the sandbox role's IAM grant, so the token is what makes it
*structurally impossible* for a profile to reach another install's SSM namespace — an absolute
path or a stray `/*` can't be expressed. Validation is shape-only, so `km validate` still works
with no configured install, and expansion **fails loud**: an unset `KM_RESOURCE_PREFIX` is an
error, never a silent default to `km`.

---

**Upgrading:** `make build` → `make build-lambdas` → `km init --dry-run=false` (not `--sidecars`
— the new IAM statement needs a full apply). Existing sandboxes keep their current role until
`km destroy && km create`.

**If you enable the sensor:** create the two SSM parameters **before** any profile extends the
fragment, and make sure `.wiz.io` is DNS-allowlisted. Both are covered in `docs/wiz-sensor.md`
§ 2 — get the ordering wrong and sandboxes using the fragment won't boot.

**Not yet proven:** live UAT is outstanding, most notably Wiz-sensor/km-eBPF coexistence. Treat
the sensor as ready to try, not as battle-tested.
