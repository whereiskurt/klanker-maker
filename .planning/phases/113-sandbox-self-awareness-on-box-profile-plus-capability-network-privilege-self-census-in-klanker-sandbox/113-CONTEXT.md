# Phase 113: Sandbox self-awareness — on-box profile + capability/network/privilege self-census in klanker:sandbox - Context

**Gathered:** 2026-06-14
**Status:** Ready for planning
**Source:** Brainstorming session (superpowers:brainstorming). Backing spec: `docs/superpowers/specs/2026-06-14-sandbox-self-awareness-design.md`

<domain>
## Phase Boundary

**Delivers:** An on-box agent can run a single self-census to learn *who it is, what it can
do, where it sits on the network, and what is locked down and why*. Two coordinated changes:
1. **Platform** — `pkg/compiler/userdata.go` writes the rendered profile to
   `/opt/km/.km-profile.yaml` at boot (mode 0644, owned by the sandbox user), threading the
   **same** `remoteProfileYAML` string `km create` already uploads to S3 (no re-marshal drift).
2. **Skill** — `klanker:sandbox` SKILL.md grows from "detect env + email tooling" into a
   structured self-census (identity, capability census, network position, privilege/restrictions,
   Slack publish-back readiness, posture summary) AND fixes its currently-broken
   `cat /opt/km/.km-profile.yaml` reference (the file now exists).

**Does NOT deliver (this phase):**
- A new sidecar binary (`km-whoami`) — the census is an inline bash block in the skill.
- An IAM grant for the sandbox role to read `artifacts/{id}/*` from S3 (explicitly rejected in
  favor of writing the profile to the box; default role reads S3 only under `mail/{id}/*`).
- Any change to `klanker:slack` content (Part 2 cross-links it, does not duplicate).
- Full active network mapping (traceroute / port-scan). Network probing is passive + exactly
  one safe-active allowed-vs-blocked egress pair.

**Surface:** `pkg/compiler/userdata.go` (+ its template-data struct) and `skills/sandbox/SKILL.md`.
Create-handler-compiled userdata change + skill content change. NO SandboxProfile schema change,
NO new Terraform resource, NO new DDB column, NO bridge Lambda change.
</domain>

<decisions>
## Implementation Decisions (LOCKED 2026-06-14)

### Profile reachability
- **Write the profile to the box** at `/opt/km/.km-profile.yaml` (chosen over an S3-read IAM grant
  and over deferring profile reflection). Works even when egress is fully locked down; needs no
  IAM module bump; directly fixes the stale skill reference.
- Source the **identical** rendered string already uploaded to S3 as
  `artifacts/{sandbox-id}/.km-profile.yaml` (`internal/app/cmd/create.go` `remoteProfileYAML`).
  Thread it through the userdata template-data struct as a new `ProfileYAML` field — guarantees the
  on-box copy and the S3 copy never diverge.

### Redaction
- **Default: write verbatim.** The agent runs as the sandbox user and can already read everything
  the profile's `configFiles` materialize on disk; secrets are injected from SSM at runtime (profile
  carries SSM *paths*, not values; the role already grants those reads). So the profile body exposes
  nothing new.
- **Spec-review checkpoint:** if review identifies an embeddable-secret field, redact
  `spec.execution.configFiles` *bodies* only (keep keys); leave all other fields intact.

### Network probing
- **Passive + safe-active.** Passive: enforcement mode + egress allowlist from the profile; eBPF
  cgroup / iptables DNAT / proxy-sidecar / custom-CA presence; `/etc/resolv.conf`.
- Safe-active: **exactly two** curls — one to a profile-declared **allowed** host, one to a
  **known-blocked** host — to confirm the boundary empirically. The only traffic-generating step,
  labeled as such (operators expect it in `km otel`).

### Capability census shape
- A **self-contained bash block** in the skill (no new binary). Enumerates: sidecar helpers present
  (`km-send`/`km-recv`/`km-slack`/`km-github`/`km-h1` → which bridges this box serves), channels
  wired (email policy, Slack post-back/inbound/transcript, GitHub/H1 inbound pollers via
  `systemctl is-active`), runtime features from the profile (VS Code, desktop, budget/TTL/idle,
  additionalVolume/additionalSnapshots).

### Privilege diagnosis
- `sudo -n true` probe → has/no passwordless sudo, cross-checked against `spec.execution.privileged`.
  Surface filesystem enforcement, git-ref enforcement (`allowedRefs`), injected secret paths — and
  **explain why** each restriction exists so the agent stops fighting locked-down behavior.

### Graceful degradation
- On a pre-Phase-113 sandbox (`/opt/km/.km-profile.yaml` absent), the skill falls back to env-var
  census + live probes only — never errors on the missing file.
</decisions>

<constraints>
## Deploy & coordination

- **Deploy:** `make build-lambdas` + `km init --dry-run=false` (create-handler carries the new
  userdata). Plugin version bump (`plugin.json` + `marketplace.json`) for the skill content change
  (`project_plugin_version_gates_cache`). Existing sandboxes gain the profile file only on
  `km destroy && km create`.
- **Numbering:** slotted as Phase 113, after the parallel Slack-rendering planning (Phases 110/111/112)
  that was in flight 2026-06-14. STATE.md intentionally left for that planner to reconcile on commit.
- **Tests:** `pkg/compiler` userdata golden/unit test asserting the profile-write block renders and the
  embedded YAML round-trips; `scripts/validate-all-profiles.sh` stays green (no schema change).
</constraints>

## Likely plan breakdown (run /gsd:plan-phase 113)

- **113-01** — userdata: thread `ProfileYAML` into the template-data struct + write
  `/opt/km/.km-profile.yaml` block; golden/unit test for the round-trip.
- **113-02** — rewrite `skills/sandbox/SKILL.md` into the self-census (sections A–F), fix the broken
  profile path + graceful fallback; plugin version bump.
- **113-03** — docs + live UAT (km create → km shell → run census end-to-end), redaction
  spec-review checkpoint sign-off.
