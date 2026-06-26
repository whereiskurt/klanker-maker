# Phase 120: Profiles reset and OS-layered fragment library - Context

**Gathered:** 2026-06-25
**Status:** Ready for planning
**Source:** Approved brainstorm design spec — `docs/superpowers/specs/2026-06-25-profiles-reset-fragment-library-design.md`

<domain>
## Phase Boundary

Reset `profiles/` to 3 operator-facing demo profiles (`learner`, `desktop`, `github`),
each composed from an expanded `profiles/base/` fragment library. Layer the high-churn
toolchain install block (claude-code/codex version pins) into a single fragment. Archive
every retired demo + frozen byte-identity fixture into the existing `testdata/profiles/`
directory, updating hard-coded test path constants in lockstep so byte-identity + golden
contracts stay green. Rewrite the validate-all-profiles inventory.

**In scope:** new fragments, 3 new leaves, file moves into testdata/profiles/, test path
constant updates, validate-all-profiles.sh rewrite, verification.

**Out of scope (this phase):** top-level folder reduction (separate follow-on, tracked in
`.planning/todos/pending/2026-06-25-lean-top-level-folder-reduction.md`); `!replace` list
narrowing; per-thread worktree isolation; touching `pkg/profile/builtins/**`; relocating
`profiles/checks/**` or `profiles/secrets/**`; re-capturing any frozen golden baseline.

**Deploy class:** `make build` ONLY. No Lambda rebuild, no schema/DDB change, no `km init`,
no sandbox recreate (Phase 117 `extends:` resolves at `km validate`/`km create` time).
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### End-state layout
- `profiles/` keeps only: `learner.yaml`, `desktop.yaml`, `github.yaml`, `base/**`,
  `checks/**`, `secrets/**`, and the `*.prompt.txt` files.
- Retired demos + frozen fixtures move to `testdata/profiles/` (ARCHIVED, not deleted):
  `learn.v2.yaml` + all `learn.v2.*` variants, `dc34.yaml`, `dc34.ami.yaml`, `codex.yaml`,
  `locked.yaml`, `locked.ami.yaml`, `github-review.yaml` + `github-review/` subdir,
  `ao.yaml`, `goose.yaml`, `example-additional-snapshots.yaml`, `h1-triage.yaml`,
  `check-triage.yaml`. Old `desktop.yaml` → `testdata/profiles/desktop.legacy.yaml`.
- `testdata/profiles/` already exists (holds `invalid-*`/`valid-*` fixtures) — fixtures join it.

### Fragment library (all `metadata.abstract: true`)
- `base/os/redhat.yaml`: `spec.runtime.ami: amazon-linux-2023` (string — safe in fragment)
  + RH-family init (yum packages, RH cert trust path `/etc/pki/tls/certs/ca-bundle.crt`,
  crond, SSL_CERT_FILE export).
- `base/os/debian.yaml`: `spec.runtime.ami: ubuntu-24.04` + Debian init (apt-over-443,
  ForceIPv4, python3 AWS-CLI, ssh.service, systemd-resolved stop, update-ca-certificates).
- `base/toolchain-agents.yaml`: OS-agnostic install block — goose, `claude-code@2.1.132`
  (SINGLE pin site), codex `rust-v0.133.0` (SINGLE pin site), nvm + node 22, gsd, herdr,
  goose OTEL export; `rsyncPaths`; string-only `env` (GOOSE_*, CODEX_CA_CERTIFICATE,
  OPENAI_API_KEY="").
- `base/plugin-klanker.yaml`: known_marketplaces.json configFile + installed_plugins seed
  one-liner + enabledPlugins settings.
- `base/slack-persandbox.yaml`: `notification.slack` per-sandbox block
  (`channelName: "sb-{profile}-{alias}"`, archiveOnDestroy:false, inbound.enabled, invites).
- Existing 8 knob fragments unchanged: safenetwork, sidecars-all, observability-learn,
  budget-standard, artifacts-workspace, iam-us-east-1, agent-claude-all-tools, email-strict.

### Constraints (load-bearing)
- **Bool zero-value trap:** mixed-bool blocks (`spec.runtime` spot/hibernation/mountEFS)
  STAY IN THE LEAF. Only string `runtime.ami` lives in the OS fragment.
- **List-union only:** slice fragments so no leaf needs a narrower list than its base.
- **`extends:` order is left→right; initCommands union is concat-in-order, first-wins** →
  list `base/os/*` FIRST in each leaf so OS package/cert steps precede toolchain steps.
- **Byte-identity:** archived input profiles keep identical bytes; only the test path
  constant changes. `userdata_learn_v2_pre92_baseline.golden.sh` must NOT be re-captured.
  Golden OUTPUT files (`pkg/compiler/testdata/*.golden.{sh,json,toml}`) do NOT move.

### Leaf composition
- `learner.yaml` extends `[os/redhat, toolchain-agents, plugin-klanker, safenetwork,
  sidecars-all, observability-learn, budget-standard, artifacts-workspace, iam-us-east-1,
  agent-claude-all-tools, email-strict, slack-persandbox]` + leaf runtime (t3.2xlarge,
  EFS, /data vol, bools), lifecycle 8h/1h/stop, github.inbound.enabled, cli.noBedrock.
  MUST functionally match today's `learn.v2.yaml`.
- `github.yaml`: same base stack minus desktop, lean runtime, github.inbound.enabled.
- `desktop.yaml`: `base/os/debian` + toolchain + plugin + slack + leaf `runtime.desktop`.

### Stays untouched
- `profiles/checks/**`, `profiles/secrets/**`, `profiles/*.prompt.txt`,
  `pkg/profile/builtins/**`.
</decisions>

<specifics>
## Specific Ideas

- Test path constants to update (`../../profiles/X` → `../../testdata/profiles/X`):
  `pkg/compiler/*_test.go` for `codex.yaml`, `dc34.yaml`, `learn.v2.yaml`, `locked.yaml`;
  `pkg/compiler/userdata_h1_byte_identity_test.go` (github-review/h1 fixture);
  `pkg/profile/github_review_secrets_test.go` (`github-review/` paths); any `dc34.ami.yaml`.
  PLAN MUST include a `grep -rn 'profiles/' --include='*_test.go'` audit step to reconcile
  EVERY literal pointing at a moved file (the ~15 phantom filenames like `backend.yaml`,
  `onboard.yaml`, `sealed.yaml` are tempdir-created by tests — NOT real files, ignore).
- `scripts/validate-all-profiles.sh`: rewrite the `PROFILES=(...)` inventory → the 3 new
  leaves + unchanged `pkg/profile/builtins/*`; update header comment file count; keep the
  `profiles/base/` skip loop.
- Use `git mv` for all moves so history follows.

## Verification gates
- `go test ./pkg/compiler/... ./pkg/profile/... ./internal/app/cmd/... -count=1 -timeout 600s`
  GREEN (capture command's own exit, not a piped `tail`). Byte-identity + goldens intact.
- `make build && bash scripts/validate-all-profiles.sh` exit 0.
- `km validate profiles/{learner,desktop,github}.yaml` exit 0, NO WARN.
- Functional-match REVIEW (not byte-identity): diff compiled userdata of new
  `profiles/learner.yaml` vs archived `testdata/profiles/learn.v2.yaml`; differences must be
  explainable (fragment ordering, plugin-enable choice), not accidental toolchain drift.
</specifics>

<deferred>
## Deferred Ideas

- Top-level folder reduction (separate phase; todo logged).
- `!replace` list-narrowing directive (Phase 117 v2 follow-up).
- Per-thread `/workspace` git-worktree isolation.

## Open item to resolve in planning
- `learner` plugin-enable: enable the klanker plugin (match chatty/polite headless
  behavior) vs. installed-but-disabled (match frozen `learn.v2.yaml`). Pick one, document
  in the leaf. (Recommendation: enable, since the functional target is a working headless
  `claude -p` learner; the frozen `learn.v2.yaml` left it disabled only to protect the
  byte-identity fixture, which is now decoupled from the live profile.)
</deferred>

---

*Phase: 120-profiles-reset-and-os-layered-fragment-library*
*Context gathered: 2026-06-25 via approved brainstorm design spec*
