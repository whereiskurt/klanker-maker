# Sandbox self-awareness & diagnostics — design

**Date:** 2026-06-14
**Status:** Approved (brainstorming) → GSD Phase 113
**Author:** brainstorming session (superpowers:brainstorming)

## Problem

An AI agent running inside a Klanker Maker sandbox has poor self-knowledge:

- It can post to Slack (`klanker:slack` is thorough) but the **publish-back readiness**
  is not surfaced as part of a single self-check.
- It is **unaware of its full capability set** — which sidecar helpers exist, which
  bridges it serves, what runtime features (VS Code, desktop, budget, TTL) are on.
- It cannot **diagnose its network position** — enforcement mode, egress allowlist,
  whether it sits behind the MITM proxy or eBPF cgroup, why a host is unreachable.
- It cannot reason about **privilege restrictions** — passwordless sudo, `privileged`,
  filesystem/git-ref enforcement — so it wastes turns fighting locked-down behavior.

A compounding bug: the current `klanker:sandbox` skill instructs the agent to
`cat /opt/km/.km-profile.yaml`, **but nothing writes that file.** The compiler only
uploads the profile to S3 (`artifacts/{sandbox-id}/.km-profile.yaml`), and the default
sandbox IAM role can read S3 **only** under `mail/{sandbox-id}/*` — so the profile is
reachable by neither path on-box today.

## Goal

Give an on-box agent a single, reliable way to answer: *who am I, what can I do, where
do I sit on the network, and what is locked down and why* — grounded in both the
declarative profile and live runtime probes.

## Non-goals

- No new sidecar binary (`km-whoami`) — the census is a self-contained bash block in the
  skill. (Revisit only if the inline block proves unwieldy.)
- No IAM change to grant S3 read of the artifacts prefix (rejected in favor of writing the
  profile to the box).
- No change to `klanker:slack` content — this skill cross-links it, it does not duplicate it.
- No full active network mapping (traceroute/port-scan). Probes are passive + a single
  safe-active allowed-vs-blocked egress pair.

## Approach

Two coordinated changes.

### Part 1 — Platform: profile-on-box

`pkg/compiler/userdata.go` gains a bootstrap section (adjacent to the existing
`2.8 Profile environment variables` block) that writes the rendered profile to
`/opt/km/.km-profile.yaml`:

```bash
mkdir -p /opt/km
cat > /opt/km/.km-profile.yaml << 'KM_PROFILE_EOF'
{{ .ProfileYAML }}
KM_PROFILE_EOF
chmod 0644 /opt/km/.km-profile.yaml
chown {{ .SandboxUser }}:{{ .SandboxUser }} /opt/km/.km-profile.yaml
```

- `ProfileYAML` is produced by `yaml.Marshal(p)` on the **same parsed `*SandboxProfile`**
  that renders the rest of the userdata, inside `generateUserData()` (avoids a
  `Compile()`/`compileEC2()` signature change — the raw `remoteProfileYAML` string is
  computed in `create.go` *after* `Compile()` returns and is not reachable inside the
  compiler). **Semantic-equivalence, not byte-identity:** the on-box copy is re-serialized
  canonical YAML (formatting/ordering/comments may differ from the raw S3 upload), but it
  is arguably *more* faithful to what was provisioned because it reflects post-resolution
  mutations (e.g. `--no-bedrock`). UAT compares the two by round-trip parse
  (`apiVersion`/`kind`/`metadata.name`/key spec values), not `diff`.
- **Redaction (spec-review checkpoint):** default is to write the profile **verbatim**.
  Rationale: the agent runs *as* the sandbox user and can already read everything the
  profile's `configFiles` materialize on disk; secrets are injected from SSM at runtime
  (the profile carries SSM *paths*, not values, and the role already grants those reads).
  If review identifies an embeddable-secret field, redact `spec.execution.configFiles`
  *bodies* only (keep keys), leaving every other field intact.
- **Deploy surface:** create-handler-compiled change → `make build-lambdas` +
  `km init --dry-run=false`. Existing sandboxes need `km destroy && km create` to gain
  the file. (Matches the Phase 106/108 userdata-change deploy pattern.)

### Part 2 — Skill: `klanker:sandbox` self-census

The skill grows from "detect env + email tooling" into a structured self-census. The
existing identity/email/signing-key steps are preserved; the broken
`cat /opt/km/.km-profile.yaml` reference becomes real (with a graceful
"profile not present → pre-Phase-113 sandbox, fall back to env + probes" path).

New / expanded sections:

**A. Identity & agent** *(kept)* — id, alias, emails, `KM_AGENT` default.

**B. Capability census** *(new)* — one self-contained bash block, no new binary:
- Tooling present: `km-send`, `km-recv`, `km-slack`, `km-github`, `km-h1`
  (presence of each `km-{bridge}` helper → which bridges this box serves).
- Channels wired: email policy; Slack post-back / inbound / transcript; GitHub & H1
  inbound pollers via `systemctl is-active`.
- Runtime features read from `/opt/km/.km-profile.yaml`: VS Code, desktop, budget/TTL/idle,
  `additionalVolume` / `additionalSnapshots`.

**C. Network position** *(new — passive + safe-active)*:
- Passive: enforcement mode + egress allowlist (`spec.network.*` from profile); presence
  of eBPF cgroup (`/sys/fs/cgroup/**/km*`), iptables DNAT, proxy sidecar processes,
  custom CA bundle; `/etc/resolv.conf`.
- Safe-active: exactly two curls — one to a **profile-declared allowed** host (expect
  connect/200) and one to a **known-blocked** host (expect block) — to empirically confirm
  the boundary. Clearly labeled as the only traffic-generating step.

**D. Privilege & restrictions** *(new)*:
- `sudo -n true` → has/no passwordless sudo; cross-check `spec.execution.privileged`.
- Filesystem enforcement, git ref enforcement (`allowedRefs`), injected secret paths.
- Each restriction is explained ("no sudo because `privileged: false`") so the agent
  stops fighting it.

**E. Slack publish-back** *(new, concise)* — confirms post-back readiness, cross-links
`klanker:slack` for the how-to (no content duplication).

**F. Self-diagnosis summary** — the agent ends with a one-paragraph posture statement:
who am I, what can I do, where do I sit, what is locked and why.

## Components & boundaries

| Unit | Responsibility | Depends on |
|---|---|---|
| `userdata.go` profile-write block | Render profile to `/opt/km/.km-profile.yaml` at boot | `ProfileYAML` template field threaded from `create.go` |
| `userdata.go` template-data struct | Carry `ProfileYAML` (verbatim S3 string) | create flow's `remoteProfileYAML` |
| `klanker:sandbox` SKILL.md | Probe-based self-census + posture summary | on-box profile (Part 1) + runtime probes |
| golden/userdata tests | Assert the profile-write block renders with the expected content | existing `pkg/compiler` test harness |

## Testing

- `pkg/compiler` userdata golden/unit test: the rendered userdata contains the
  profile-write block and the embedded YAML matches the input profile (round-trip).
- `scripts/validate-all-profiles.sh` stays green (no schema change).
- Manual UAT on a live sandbox: `km create` a Slack-enabled profile, `km shell`, confirm
  `/opt/km/.km-profile.yaml` exists and the skill's census block runs clean end-to-end
  (capabilities enumerated, network boundary confirmed, sudo state correct).

## Deploy

- `make build-lambdas` + `km init --dry-run=false` (create-handler carries new userdata).
- Plugin version bump (`plugin.json` + `marketplace.json`) for the skill content change —
  clients cache the old skill otherwise.
- Existing sandboxes: `km destroy && km create` to gain `/opt/km/.km-profile.yaml`. The
  skill degrades gracefully on pre-Phase-113 sandboxes (env + probes, no profile file).

## Risks

- **Secret leakage via profile body** — mitigated by the redaction checkpoint; default
  verbatim is safe because the agent already has sandbox-user read of materialized config.
- **Safe-active probe audit noise** — bounded to two curls; documented as the only
  traffic-generating step so operators expect it in `km otel`.
- **Plugin cache** — covered by the version bump (see `project_plugin_version_gates_cache`).
