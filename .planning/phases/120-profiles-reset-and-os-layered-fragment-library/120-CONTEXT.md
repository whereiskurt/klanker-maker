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
- **Canonical leaves = 4** (UPDATED 2026-06-26 from operator decision): `learner.yaml`,
  `desktop.yaml`, `github.yaml`, **`h1.yaml`** — all composed from `base/**`. The H1 bridge
  is actively routed in `km-config.yaml`, so it gets a clean composed home rather than a
  testdata path. `check-triage` is NOT routed in km-config → archives cleanly.
- `profiles/` keeps only: the 4 leaves + `base/**` + `checks/**` + `secrets/**` + the
  `*.prompt.txt` files.
- Retired demos + frozen fixtures move to `testdata/profiles/` (ARCHIVED via `git mv`, not
  deleted): `learn.v2.yaml` + all `learn.v2.*` variants, `dc34.yaml`, `dc34.ami.yaml`,
  `codex.yaml`, `locked.yaml`, `locked.ami.yaml`, `github-review.yaml`, `ao.yaml`,
  `goose.yaml`, `example-additional-snapshots.yaml`, `h1-triage.yaml` (old monolith —
  superseded by composed `h1.yaml`, archived for reference), `check-triage.yaml`. Old
  `desktop.yaml` → `testdata/profiles/desktop.legacy.yaml`.
- **CORRECTION (research):** `profiles/github-review/` subdir does NOT exist — the
  `github-review/.km-*` paths in `github_review_secrets_test.go` are tempdir-created. Only
  the single file `profiles/github-review.yaml` moves.
- `testdata/profiles/` already exists (holds `invalid-*`/`valid-*` fixtures) — fixtures join it.

### km-config.yaml lockstep updates (IN SCOPE — tracked file, live routing)
- `profiles/github-review.yaml` → `profiles/github.yaml` (lines ~41, ~51).
- `profiles/learn.v2.yaml` → `profiles/learner.yaml` (line ~60).
- `profiles/h1-triage.yaml` → `profiles/h1.yaml` (lines ~63, ~81).
- `*.prompt.txt` references unchanged (those files stay in `profiles/`).
- Note in plan: operator re-runs `km init --github` / `--h1` to push the env-block change
  (these are config values; no Lambda rebuild needed for the path swap itself).

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
- `github.yaml`: `base/os/redhat` + `toolchain-agents` (full — single pin site is the
  point) + plugin + the knob fragments; lean runtime; `notification.github.inbound.enabled:
  true` ONLY (NO slack-persandbox fragment, to avoid the validate WARN). Replaces
  `github-review.yaml`.
- `desktop.yaml`: `base/os/debian` + toolchain + plugin + slack + leaf `runtime.desktop`.
- `h1.yaml` (NEW canonical leaf): `base/os/redhat` + `toolchain-agents` + plugin + knob
  fragments; `notification` wired for the H1 bridge inbound (compose from old
  `h1-triage.yaml` — read it for the exact notification/inbound shape; functionally match).
  Replaces `h1-triage.yaml` in km-config H1 routing.

### Stays untouched
- `profiles/checks/**`, `profiles/secrets/**`, `profiles/*.prompt.txt`,
  `pkg/profile/builtins/**`.
</decisions>

<specifics>
## Specific Ideas

- Test path constants to update — **EXACTLY SIX, verified by research** (`../../profiles/X`
  → `../../testdata/profiles/X`, or the `filepath.Join` equivalent):
  1. `pkg/compiler/userdata_phase92_byte_identity_test.go:34` — `filepath.Join(repoRoot,
     "profiles")` → `filepath.Join(repoRoot, "testdata", "profiles")`.
  2-5. `pkg/compiler/agent_claude_golden_test.go:41-44` — four strings
     `"../../profiles/{learn.v2,dc34,locked,codex}.yaml"` → `"../../testdata/profiles/..."`.
  6. `pkg/compiler/agent_codex_golden_test.go:32` — `const profilePath =
     "../../profiles/codex.yaml"` → `"../../testdata/profiles/codex.yaml"`.
  7. `pkg/profile/github_review_secrets_test.go:32` — `filepath.Join(repoRoot, "profiles",
     "github-review.yaml")` → `filepath.Join(repoRoot, "testdata", "profiles",
     "github-review.yaml")`.
  (That's 6 files / 7 edit sites.) **`userdata_h1_byte_identity_test.go` needs NO change** —
  it loads `ec2-basic.yaml` from `pkg/compiler/testdata/`, not a top-level profile.
  PLAN MUST STILL include a `grep -rn 'profiles/' --include='*_test.go'` audit as a
  belt-and-suspenders check (phantom filenames like `backend.yaml`/`onboard.yaml`/
  `sealed.yaml` are tempdir-created — NOT real files, ignore).
- `scripts/validate-all-profiles.sh`: rewrite the `PROFILES=(...)` inventory → the **4** new
  leaves (`learner`/`desktop`/`github`/`h1`) + unchanged `pkg/profile/builtins/*`; update
  header comment file count. **FIX the base skip loop:** the current glob `profiles/base/*.yaml`
  does NOT match the new `profiles/base/os/*.yaml` subdir — change to a recursive `find
  profiles/base -name '*.yaml'` (or add an explicit `profiles/base/os/*.yaml` loop) so the
  new OS fragments are skipped, not validated standalone.
- Use `git mv` for all moves so history follows.
- **km-config.yaml** path swaps (4 references) are part of this phase (tracked file).

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

## Resolved (was open)
- `learner` plugin-enable: **ENABLE** the klanker plugin in the new `learner.yaml` (via
  `plugin-klanker` fragment's `enabledPlugins` settings). Rationale: the functional target
  is a working headless `claude -p` learner; `learn.v2.yaml` left it disabled ONLY to
  protect the frozen byte-identity fixture, which is now decoupled (the fixture stays in
  testdata, the live leaf is free to enable). This is the one intentional, documented
  functional delta from `learn.v2.yaml` — call it out in the functional-match review.
</deferred>

---

*Phase: 120-profiles-reset-and-os-layered-fragment-library*
*Context gathered: 2026-06-25 via approved brainstorm design spec*
