# Phase 120 — Profiles reset + OS-layered fragment library — SUMMARY

**Status:** implementation complete + all gates green (pending phase verifier)
**Branch:** `phase-120-profiles-reset` (not yet merged)
**Deploy class:** `make build` only — no Lambda redeploy, no schema/DDB change, no `km init`, no sandbox recreate. (Exception: the remote-create fix below is also operator-binary only.)

> This phase grew well past the original 4-plan scope through live operator testing.
> This summary supersedes the per-plan SUMMARYs (120-01/02/03) as the phase record.

---

## What shipped

### 1. `profiles/` reset to 5 composed leaves (was ~20 monolithic files)
`learner`, `desktop`, `github`, `h1`, `spot` — each composed from `profiles/base/` fragments.
Every retired demo + frozen byte-identity fixture archived (via `git mv`, byte-identical) into
`testdata/profiles/`. Non-profile assets (`checks/`, `secrets/`) left in place; live bridge
prompt files relocated to `profiles/prompts/`.

| Leaf | Substrate | Spot | Includes | Notes |
|------|-----------|------|----------|-------|
| `learner` | t3.2xlarge, EFS, /data | on-demand | 5 | learnMode (in-leaf), full Slack |
| `github` | t3.medium | on-demand | 4 | github.inbound + Slack + email; in-leaf narrow net |
| `h1` | t3.medium | on-demand | 4 | h1.inbound + Slack + email; tracing off in-leaf |
| `desktop` | t3.large, Ubuntu | on-demand | 4 | KasmVNC kiosk; Slack + email; in-leaf narrow net |
| `spot` | t3.large | **spot** | 4 | the single dedicated spot demo (hibernation:false) |

### 2. Fragment library — grouped by what actually varies (5 include concepts)
- `base/os/{redhat,debian}` — ami + OS-specific init (yum/apt, cert paths). String `runtime.ami`
  only; mixed-bool runtime fields stay in-leaf (bool zero-value trap).
- `base/network/{safenetwork,locked}` — wide-open vs. curated comprehensive egress allowlist.
- `base/userinit` — agent toolchain (goose/claude-code@2.1.132/codex rust-v0.133.0/nvm/gsd/herdr,
  **single version-pin site**) + klanker plugin install/enable. (Merged toolchain-agents + plugin.)
- `base/platform` — the 7 uniform blocks (sidecars, observability logs, budget, artifacts, iam,
  agent-claude tools, email). learnMode deliberately excluded (learner-only).
- `base/slack-persandbox` — per-sandbox `#sb-{profile}-{alias}` channel + transcript + invite.

Leaves dropped from ~12 includes → **4-5**. The granular pre-consolidation fragments were
relocated to `testdata/profiles/base/` (used only by the frozen fixtures — decoupled from the
live library forever).

### 3. Live-operator-driven changes (beyond original plan)
- **On-demand by default:** `github`/`h1` flipped `spot:true → false`; `spot.yaml` is the one
  profile that exercises spot.
- **Secret-free leaves:** removed the inherited `secrets.sopsFile` from `github`/`h1` (pointed at
  an operator-provided gitignored bundle that broke `km create`). SOPS injection still demoed via
  `profiles/secrets/` + docs.
- **Per-sandbox Slack everywhere:** all 5 leaves extend `base/slack-persandbox` (invites
  `whereiskurt@gmail.com`).
- **Network posture fix (broken-shell incident):** the consolidated `base/userinit` toolchain
  pulls from `github.com/block/goose`, `nvm-sh/nvm`, `openai/codex` — but the bridge leaves'
  tight `sourceAccess` (github `whereiskurt/*`, h1 `mode:none`, desktop `allowedRepos:[]`) made
  the GitHub MITM proxy return `error:repo_not_allowed`, which piped into `sh` and aborted
  cloud-init under `set -e` in ~11s, BEFORE `/usr/local/bin/km-session-entry` was written →
  `km shell` exec'd a missing file. **Fix:** all 5 leaves now use the proven `learn.v2` posture
  — `enforcement: both`, DNS/hosts `*`, `sourceAccess.allowedRepos: *`. Verified live:
  `gh-a04f85c2` boots in 154s (full toolchain), `km-session-entry` present, 0 repo blocks,
  shell + `km agent auth` working.
- **Comments + Phase refs stripped** from all leaves + fragments for a lean surface.

### 4. Bug fix — remote-create flatten (the important one)
`km create --remote` uploaded the RAW child profile (`extends:` intact); the create-handler Lambda
(no `profiles/base/**` fragments) failed to resolve it → `profile "base/os/redhat" not found`,
status=failed, EventBridge retry-dups. **Latent since Phase 117**; Phase 120 made every leaf
extends-based, so remote create always failed. Fix: `selectRemoteProfileYAML` uploads the
**resolved/flattened** profile (extends cleared) when extends is set. Operator-binary fix only
(`make build` + re-create). Regression tests added. **Verified live: `gh-8bd5851e` reached
`running` on-demand.** See memory `project_remote_create_flattens_extends`.

### 5. Plumbing
- 6 test-path constants repointed `profiles/` → `testdata/profiles/` (byte-identity green).
- `scripts/validate-all-profiles.sh` inventory → 5 leaves + 8 builtins (13); recursive `base/`
  skip (covers `base/os/`, `base/network/`).
- `km-config.yaml` (gitignored) repointed: github-review→github, learn.v2→learner, h1-triage→h1,
  prompt paths → `profiles/prompts/`.

---

## Verification

| Gate | Result |
|------|--------|
| `go test ./pkg/compiler/... ./pkg/profile/...` (byte-identity + goldens) | ✅ green |
| `km validate profiles/{learner,desktop,github,h1,spot}.yaml` | ✅ exit 0, no WARN |
| `scripts/validate-all-profiles.sh` | ✅ 13 profiles valid |
| learner functional-match vs learn.v2 | ✅ identical (learnMode + 19 initCommands + sidecars + email + budget); 1 documented delta (plugin enabled) |
| Live remote create + shell + auth (`gh-a04f85c2`) | ✅ running, on-demand; cloud-init done (154s), km-session-entry present, shell + claude auth working |
| `go test ./internal/app/cmd/...` (full cmd suite) | ✅ green (483s, exit 0) |

---

## Out of scope / deferred
- Top-level folder reduction (separate phase — `.planning/todos/pending/2026-06-25-lean-top-level-folder-reduction.md`).
- Re-running github bridge E2E against `githubber` with the new Slack-enabled profile (the live
  `gh-8bd5851e` was created from the pre-Slack github.yaml; recreate to see the channel).
- Bridge cold-create (`PutSandboxCreate`) extends-flattening — verify if a bridge ever dispatches
  an extends-based profile (noted in memory).

## Commits (branch `phase-120-profiles-reset`)
Network folder → consolidation → comment strip, plus the live-driven fixes (remote-create flatten,
on-demand/spot, secret-free, per-sandbox Slack). ~20 commits; see `git log main..HEAD`.
