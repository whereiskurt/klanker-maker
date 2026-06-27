# Phase 120: Profiles Reset and OS-Layered Fragment Library — Research

**Researched:** 2026-06-25
**Domain:** Profile repo refactor — file moves, test path updates, fragment authoring
**Confidence:** HIGH (all findings verified by direct code inspection)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- `profiles/` keeps only: `learner.yaml`, `desktop.yaml`, `github.yaml`, `base/**`,
  `checks/**`, `secrets/**`, and the `*.prompt.txt` files.
- Retired demos + frozen fixtures move to `testdata/profiles/` (ARCHIVED, not deleted):
  `learn.v2.yaml` + all `learn.v2.*` variants, `dc34.yaml`, `dc34.ami.yaml`, `codex.yaml`,
  `locked.yaml`, `locked.ami.yaml`, `github-review.yaml` + `github-review/` subdir,
  `ao.yaml`, `goose.yaml`, `example-additional-snapshots.yaml`, `h1-triage.yaml`,
  `check-triage.yaml`. Old `desktop.yaml` → `testdata/profiles/desktop.legacy.yaml`.
- `testdata/profiles/` already exists (holds `invalid-*`/`valid-*` fixtures) — fixtures join it.
- Fragment library (all `metadata.abstract: true`): 5 NEW fragments (`os/redhat`, `os/debian`,
  `toolchain-agents`, `plugin-klanker`, `slack-persandbox`) + 8 existing unchanged.
- Bool zero-value trap: mixed-bool blocks (`spec.runtime` spot/hibernation/mountEFS) STAY IN
  THE LEAF. Only string `runtime.ami` lives in the OS fragment.
- List-union only: slice fragments so no leaf needs a narrower list than its base.
- `extends:` order is left→right; initCommands union is concat-in-order, first-wins → list
  `base/os/*` FIRST in each leaf so OS package/cert steps precede toolchain steps.
- Byte-identity: archived input profiles keep identical bytes; only the test path constant
  changes. `userdata_learn_v2_pre92_baseline.golden.sh` must NOT be re-captured.
  Golden OUTPUT files (`pkg/compiler/testdata/*.golden.{sh,json,toml}`) do NOT move.
- Leaf composition locked (see CONTEXT.md § Leaf composition).
- STAYS untouched: `profiles/checks/**`, `profiles/secrets/**`, `profiles/*.prompt.txt`,
  `pkg/profile/builtins/**`.

### Claude's Discretion
- `learner` plugin-enable: enable the klanker plugin (match chatty/polite headless behavior)
  vs. installed-but-disabled (match frozen `learn.v2.yaml`). Pick one, document in the leaf.
  (Recommendation: enable, since the functional target is a working headless `claude -p`
  learner; the frozen `learn.v2.yaml` left it disabled only to protect the byte-identity
  fixture, which is now decoupled from the live profile.)

### Deferred Ideas (OUT OF SCOPE)
- Top-level folder reduction (separate phase; todo logged).
- `!replace` list-narrowing directive (Phase 117 v2 follow-up).
- Per-thread `/workspace` git-worktree isolation.
</user_constraints>

---

## Summary

Phase 120 is a pure file-move + test-path-update + fragment-authoring refactor. No Lambda,
schema, DDB, or binary change. All findings below are ground-truth from direct code inspection.

The key risks are: (1) the test path constants that MUST be updated alongside the file moves
or tests go red; (2) the `validate-all-profiles.sh` skip loop that only covers `base/*.yaml`
but not the new `base/os/*.yaml` subdirectory; (3) the live `km-config.yaml` that references
`profiles/learn.v2.yaml` and `profiles/h1-triage.yaml` by path — operator must update these
after the move; (4) the `github_review_secrets_test.go` that reads a specific file by
absolute path constructed from repo root.

**Primary recommendation:** Execute file moves with `git mv`, update the six hard-coded test
path constants in lockstep, rewrite `validate-all-profiles.sh`, author the five new fragments
and three leaves, then verify with the test suite.

---

## Task 1: Exact Test-Path Audit

### Files that MUST move and the test constants that reference them

#### A. `profiles/learn.v2.yaml` → `testdata/profiles/learn.v2.yaml`

| File:Line | Constant / Expression | Change Required |
|-----------|----------------------|-----------------|
| `pkg/compiler/userdata_phase92_byte_identity_test.go:34` | `profilesDir := filepath.Join(repoRoot, "profiles")` | Change `"profiles"` → `"testdata", "profiles"` |
| `pkg/compiler/agent_claude_golden_test.go:41` | `"../../profiles/learn.v2.yaml"` | Change → `"../../testdata/profiles/learn.v2.yaml"` |

**Details on phase92 test:** `repoRoot` is computed as `filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))` where `thisFile = .../pkg/compiler/userdata_phase92_byte_identity_test.go`. Three `filepath.Dir` calls yield the repo root. `Resolve("learn.v2", []string{profilesDir})` searches `profilesDir/learn.v2.yaml`. After the move, `profilesDir` must be `filepath.Join(repoRoot, "testdata", "profiles")`. The profile name `"learn.v2"` is unchanged.

**Details on claude golden test:** `profilesDir` is derived from the path string: `filepath.Join(repoRoot, filepath.Dir(f.profilePath[len("../../"):]))`. For `"../../profiles/learn.v2.yaml"`, the dir is `"profiles"` → joined with repoRoot. After change to `"../../testdata/profiles/learn.v2.yaml"`, the dir becomes `"testdata/profiles"`.

#### B. `profiles/dc34.yaml` → `testdata/profiles/dc34.yaml`

| File:Line | Constant / Expression | Change Required |
|-----------|----------------------|-----------------|
| `pkg/compiler/agent_claude_golden_test.go:42` | `"../../profiles/dc34.yaml"` | Change → `"../../testdata/profiles/dc34.yaml"` |

#### C. `profiles/locked.yaml` → `testdata/profiles/locked.yaml`

| File:Line | Constant / Expression | Change Required |
|-----------|----------------------|-----------------|
| `pkg/compiler/agent_claude_golden_test.go:43` | `"../../profiles/locked.yaml"` | Change → `"../../testdata/profiles/locked.yaml"` |

#### D. `profiles/codex.yaml` → `testdata/profiles/codex.yaml`

| File:Line | Constant / Expression | Change Required |
|-----------|----------------------|-----------------|
| `pkg/compiler/agent_claude_golden_test.go:44` | `"../../profiles/codex.yaml"` | Change → `"../../testdata/profiles/codex.yaml"` |
| `pkg/compiler/agent_codex_golden_test.go:32` | `const profilePath = "../../profiles/codex.yaml"` | Change → `"../../testdata/profiles/codex.yaml"` |

Note: `agent_codex_golden_test.go` uses `profile.Parse(raw)` (not `profile.Resolve`), so no `searchPaths` change needed — just the file path constant.

#### E. `profiles/github-review.yaml` → `testdata/profiles/github-review.yaml`

| File:Line | Constant / Expression | Change Required |
|-----------|----------------------|-----------------|
| `pkg/profile/github_review_secrets_test.go:32` | `filepath.Join(repoRoot, "profiles", "github-review.yaml")` | Change `"profiles"` → `"testdata", "profiles"` |

**Details:** The test uses `runtime.Caller(0)` to locate `thisFile = .../pkg/profile/github_review_secrets_test.go`. `repoRoot = filepath.Join(filepath.Dir(thisFile), "..", "..")` = two levels up from `pkg/profile/` = repo root. Path becomes `filepath.Join(repoRoot, "testdata", "profiles", "github-review.yaml")`.

#### F. `profiles/dc34.ami.yaml` → `testdata/profiles/dc34.ami.yaml`

No direct test file reference found via `grep -rn '"../../profiles/dc34.ami'`. Only appears in: `scripts/validate-all-profiles.sh:43` (handled by rewrite) and `internal/app/cmd/validate.go:95` (just a comment example — safe, no change needed).

#### G. All other moves (confirmed NO test file references)

The following profiles are referenced only in `scripts/validate-all-profiles.sh` (handled by rewrite) and docs/CLAUDE.md/OPERATOR-GUIDE.md (stale but lower priority):
- `profiles/ao.yaml`
- `profiles/goose.yaml`
- `profiles/h1-triage.yaml`
- `profiles/check-triage.yaml`
- `profiles/example-additional-snapshots.yaml`
- `profiles/locked.ami.yaml`
- `profiles/learn.v2.chatty.yaml`
- `profiles/learn.v2.codex.yaml`
- `profiles/learn.v2.polite.yaml`
- `profiles/learn.v2.parallel.yaml`
- `profiles/learn.v2.private-allow.yaml`
- `profiles/learn.v2.desktop.yaml`
- `profiles/desktop.yaml` (→ `desktop.legacy.yaml`)

### Files confirmed as tempdir-created (NOT real files, ignore)

From `internal/app/cmd/at_e2e_test.go:141`: `profiles/sealed.yaml` — this is a BUILTIN (`pkg/profile/builtins/sealed.yaml`), not a top-level file. The `at_e2e_test.go` reference builds an in-memory temp profile and passes it to the km binary; no file at `profiles/sealed.yaml` exists (confirmed: not present in `profiles/`).

From `internal/app/cmd/github_repos_export_test.go:28-39`: `profiles/review.yaml`, `profiles/frontend.yaml`, `profiles/backend.yaml` — these are synthetic config strings in mock data, NOT files read from disk. Confirmed: none exist in `profiles/`.

### github-review/ subdirectory

The design references `github-review/` subdirectory. **Confirmed: this directory does NOT exist** in `profiles/` (`ls /Users/khundeck/working/klankrmkr/profiles/github-review/` → not found). The design's `testdata/profiles/github-review/` target is for files referenced by tests. The current `init_github_prestage_test.go` references `"github-profiles/github-review/.km-profile.yaml"` which is an S3 key string (slug-derived), not a filesystem path. No `profiles/github-review/` directory to move.

### Configs with DATA references to moved profiles (NOT test code, but still stale)

These are string values in config/docs that will be stale after the move but do NOT cause test failures:

| File | Reference | Status After Move |
|------|-----------|-------------------|
| `km-config.yaml:60` | `profile: profiles/learn.v2.yaml` | STALE — operator must update to `profiles/learner.yaml` |
| `km-config.yaml:63` | `default_profile: profiles/h1-triage.yaml` | STALE — operator must decide (h1-triage archived; new `github.yaml` is the github profile) |
| `internal/app/cmd/at.go:89-90` | help text `profiles/goose.yaml` | Stale CLI help text — update to `profiles/learner.yaml` or remove |
| `docs/github-bridge.md` (many) | `profiles/github-review.yaml` | Stale docs — update to `profiles/github.yaml` |
| `docs/h1-bridge.md` (many) | `profiles/h1-triage.yaml` | Stale docs |
| `docs/desktop.md` (many) | `profiles/desktop.yaml` | Stale docs — new `profiles/desktop.yaml` replaces old |
| `docs/user-manual.md` | `profiles/goose.yaml` | Stale docs |
| `OPERATOR-GUIDE.md:1870` | `km validate profiles/dc34.yaml` | Stale doc example |
| `OPERATOR-GUIDE.md:1639,1670` | `profiles/desktop.yaml` | Still valid (new desktop.yaml takes the name) |
| `CLAUDE.md:34` | `profiles/learn.v2.parallel.yaml` | Stale (parallel moves to testdata) |
| `docs/slack-notifications.md:2975,2992` | `profiles/learn.v2.parallel.yaml` | Stale docs |

**Doctor_github tests and webhook_handler_phase115_test.go:** These use `"profiles/github-review.yaml"` as a **config string value** (not a file path). `checkGitHubReposResolvable` only checks that the string is non-empty — it does NOT open any file. These tests do NOT need updating.

**init_github_prestage_test.go:** Uses `"github-review"` as a profile slug and checks S3 key strings. `PreStageGitHubProfiles` in `init.go:773` calls `readFileOrEmpty("profiles/" + slug + ".yaml")` — this is a production code path, not test. It fails soft (empty placeholder) if the file is missing, so no test failure. The test uses a mock S3 client and does not exercise the real disk read.

---

## Task 2: Golden Output Co-location

**Confirmed:** The `.golden.{sh,json,toml}` files in `pkg/compiler/testdata/` are keyed by their own names, independent of input profile paths.

Golden files (stay in `pkg/compiler/testdata/`, NOT moved):
- `userdata_learn_v2_pre92_baseline.golden.sh` — frozen baseline
- `claude_settings_learn_v2.golden.json`
- `claude_settings_dc34.golden.json`
- `claude_settings_locked.golden.json`
- `claude_settings_codex.golden.json`
- `codex_config_codex.golden.toml`
- `h1_byte_identity_golden.txt`
- `userdata_additional_volume_only.golden.sh`

**How they are loaded (confirmed from test code):**

`agent_claude_golden_test.go:73-74`:
```go
want, err := os.ReadFile(filepath.Clean(f.goldenPath))
// f.goldenPath = "testdata/claude_settings_learn_v2.golden.json" (relative to test file dir)
```

`agent_codex_golden_test.go:49-52`:
```go
want, err := os.ReadFile(filepath.Clean(goldenPath))
// goldenPath = "testdata/codex_config_codex.golden.toml"
```

`userdata_phase92_byte_identity_test.go:57-63`:
```go
func goldenPath92(t *testing.T, name string) string {
    _, thisFile, _, ok := runtime.Caller(0)
    return filepath.Join(filepath.Dir(thisFile), "testdata", name)
}
// name = "userdata_learn_v2_pre92_baseline.golden.sh"
```

**Conclusion:** Goldens use relative paths anchored at the test file's directory (`pkg/compiler/`) and will NOT be affected by moving input profiles. Moving the INPUT profile and updating the path constant in the test is sufficient — no golden needs to change bytes or move.

**H1 byte identity test** (`userdata_h1_byte_identity_test.go`): uses `h1BaselineProfile = "ec2-basic.yaml"` loaded from `pkg/compiler/testdata/ec2-basic.yaml` (already in testdata). Not affected by any profile move.

---

## Task 3: Toolchain initCommands Slicing

Based on direct read of `profiles/learn.v2.yaml` and variant profiles:

### learn.v2.yaml initCommands (ground truth)

```
1.  yum install -y git nodejs npm python3 python3-pip bzip2 jq tar gzip unzip tmux cronie
2.  systemctl enable crond; systemctl start crond
3.  HOME=/root curl -fsSL https://github.com/block/goose/releases/download/stable/download_cli.sh | HOME=/root CONFIGURE=false bash
4.  cp -r /root/.local/bin/goose /usr/local/bin/goose-bin || true
5.  printf 'export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf\nexport OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318\n' > /etc/profile.d/km-zz-goose-otel.sh
6.  cat /usr/local/share/ca-certificates/km-proxy-ca.crt >> /etc/pki/tls/certs/ca-bundle.crt
7.  echo export SSL_CERT_FILE=/etc/pki/tls/certs/ca-bundle.crt >> /etc/profile.d/km-zz-goose-otel.sh
8.  mkdir -p /home/sandbox/.local/bin && cp /usr/local/bin/goose-bin /usr/local/bin/goose && cp /usr/local/bin/goose /home/sandbox/.local/bin/goose || true
9.  npm install -g @anthropic-ai/claude-code@2.1.132
10. curl -fsSL https://github.com/openai/codex/releases/download/rust-v0.133.0/codex-x86_64-unknown-linux-musl.tar.gz -o /tmp/codex.tar.gz
11. tar -xzf /tmp/codex.tar.gz -C /tmp && install -m 755 /tmp/codex-x86_64-unknown-linux-musl /usr/local/bin/codex
12. mkdir -p /home/sandbox/.config/goose /home/sandbox/.codex && chown -R sandbox:sandbox /home/sandbox/.codex
13. mkdir -p /workspace && chown -R sandbox:sandbox /workspace /home/sandbox/.config /home/sandbox/.local
14. su - sandbox -c 'cd /workspace && git config --global user.name "Learner Sandbox" && git config --global user.email "sandbox@klankermaker.ai"'
15. git clone --depth 1 ... (klanker plugin installed_plugins.json one-liner)
16. su - sandbox -c 'cd /workspace && curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.4/install.sh | bash'
17. su - sandbox -c 'nvm install 22'
18. su - sandbox -c 'npx -y get-shit-done-cc --claude --codex --global'
19. su - sandbox -c 'curl -fsSL https://herdr.dev/install.sh | sh'
```

### learn.v2.chatty.yaml initCommands — difference from learn.v2

learn.v2.chatty is MISSING line 19 (`herdr` install). In every other way identical.
Additionally, `chatty` has `configFiles["/home/sandbox/.claude/settings.json"]` with `enabledPlugins`.

### learn.v2.codex.yaml initCommands — difference from learn.v2

`codex` variant adds one extra line after line 11:
```
printf 'model = "gpt-4o-mini"\n...' > /home/sandbox/.codex/config.toml && chown ...
```
(Explicit codex config.toml write — in toolchain-agents fragment this should be omitted since the compiler synthesizes it.)

### Fragment assignment table

| initCommands Line(s) | Fragment | Rationale |
|---------------------|----------|-----------|
| 1 (yum install) | `base/os/redhat.yaml` | OS-specific package manager |
| 2 (crond enable) | `base/os/redhat.yaml` | RH-specific cron daemon name |
| 6 (cert bundle RH path) | `base/os/redhat.yaml` | `/etc/pki/tls/certs/ca-bundle.crt` is RH-only |
| 7 (SSL_CERT_FILE export) | `base/os/redhat.yaml` | Appends to goose otel profile.d (RH cert path) |
| 3-5, 8 (goose install + local bin + OTEL) | `base/toolchain-agents.yaml` | OS-agnostic; goose binary from GitHub releases |
| 9 (npm install claude-code@PIN) | `base/toolchain-agents.yaml` | Single pin site |
| 10-11 (codex download + install) | `base/toolchain-agents.yaml` | Single pin site; codex.toml synthesized by compiler |
| 12-13 (mkdir goose/codex + workspace chown) | `base/toolchain-agents.yaml` | OS-agnostic setup |
| 14 (git config sandbox user) | `base/toolchain-agents.yaml` | OS-agnostic |
| 15 (plugin git clone + installed_plugins) | `base/plugin-klanker.yaml` | Plugin-specific |
| 16-18 (nvm + node 22 + gsd) | `base/toolchain-agents.yaml` | OS-agnostic node toolchain |
| 19 (herdr) | `base/toolchain-agents.yaml` | OS-agnostic; chatty variant omits this |

**herdr decision:** `learn.v2.yaml` includes herdr (line 19); `learn.v2.chatty.yaml` omits it. The `learner` leaf should include herdr (matching `learn.v2`). Put herdr in `toolchain-agents`; chatty is now archived and not a live profile.

### execution.env keys

All in `base/toolchain-agents.yaml` (string-only — no bools):
```yaml
GOOSE_MODE: auto
GOOSE_TELEMETRY_ENABLED: "false"
CODEX_CA_CERTIFICATE: /usr/local/share/ca-certificates/km-proxy-ca.crt
OPENAI_API_KEY: ""
```

`SANDBOX_MODE: learn-v2-direct-api` — leaf-specific, stays in leaf.
`GOOSE_PROVIDER` / `GOOSE_MODEL` — dc34-specific, stays in archived dc34.yaml.

### configFiles

| Key | Fragment |
|-----|----------|
| `"/home/sandbox/.claude/plugins/known_marketplaces.json"` | `base/plugin-klanker.yaml` |
| `"/home/sandbox/.claude/settings.json"` (enabledPlugins) | `base/plugin-klanker.yaml` (if learner enables plugin) |

### rsyncPaths

All in `base/toolchain-agents.yaml`:
```yaml
rsyncPaths:
  - ".gitconfig"
  - ".config/goose"
  - ".claude"
  - ".claude.json"
  - ".codex"
```

---

## Task 4: deepMerge / extends Mechanics Verification

### Function citations (from `pkg/profile/inherit.go`)

| Function | File:Line | Behavior |
|----------|-----------|----------|
| `deepMerge(dst, src)` | `inherit.go:27` | Keys only in dst → keep; only in src → add; both maps → recurse; both slices → `concatDedup`; scalar collision → src wins |
| `concatDedup(a, b)` | `inherit.go:69` | Concat b after a; drop elements of b already in result by `reflect.DeepEqual`; first-occurrence kept |
| `resolveMap(...)` | `inherit.go:113` | Folds bases left→right via `deepMerge(acc, parentMap)`; then merges child LAST (`deepMerge(acc, rawMap)`); child wins scalars |
| `Resolve(name, searchPaths)` | `inherit.go:97` | Public entry point; calls `resolveMap`, then `fromMap` |
| `clearAbstractFromMetadata(acc)` | `inherit.go:260` | Removes `abstract` from merged map's `metadata` so concrete leaf is never marked abstract |
| `applyInitCommandsAppend(acc)` | `inherit.go:236` | Post-merge: moves `execution.initCommandsAppend` onto tail of `execution.initCommands` via `concatDedup`, then deletes the append key |

### initCommands union ordering

With `extends: [base/os/redhat, base/toolchain-agents, base/plugin-klanker, ...]`:
1. Fold base/os/redhat → acc (OS yum + cert lines first)
2. Fold base/toolchain-agents → acc via `concatDedup` (appends new lines, deduplicates already-present)
3. Fold base/plugin-klanker → acc (plugin clone line)
4. ... (remaining bases contribute no new initCommands)
5. Merge child leaf → acc (child adds nothing to initCommands unless leaf has its own)

**Result:** OS lines precede toolchain lines precede plugin lines. First-occurrence-kept means no duplication even if two bases share a line.

### Bool zero-value trap (confirmed)

From `inherit.go:330` comment:
> Note on non-pointer bool zero-value (Pitfall 2): base FRAGMENTS must only declare fields they intend to set. A fragment that writes a full spec.runtime block forces spot:false etc. onto children.

The mechanism: when `goyaml.Marshal` serializes a `SandboxProfile` struct with `spot: false`, the YAML contains `spot: false` explicitly. When this is decoded into `map[string]any` by `goyaml.Unmarshal`, the key `"spot"` is present with value `false`. When the leaf's own map is merged on top, `deepMerge`'s scalar-collision path (`src wins`) applies — but if the LEAF also writes `spot: false`, it stays false. The problem is if the leaf OMITS `spot` but the fragment sets it to `false`: the fragment's `false` survives in `acc` and cannot be overridden to `true` without the leaf explicitly setting `spot: true`.

**Consequence for fragments:** `base/os/redhat.yaml` should ONLY declare `spec.runtime.ami: amazon-linux-2023` — it must NOT declare `spot`, `hibernation`, or `mountEFS`. The leaf owns all bool fields in `spec.runtime`.

### metadata.abstract skip in km validate

From `validate.go:788-816` (`IsAbstractFragment`): if `metadata.abstract: true`, `km validate` returns immediately with a SKIP message and exit 0. The fragment is not passed through the full validation rules. This is why fragments with missing required fields (like `spec.runtime.substrate`) do not fail `km validate` standalone.

---

## Task 5: validate-all-profiles.sh Current Inventory + Builtins

### Current `PROFILES=()` array in `scripts/validate-all-profiles.sh`

```
profiles/ao.yaml
profiles/codex.yaml
profiles/dc34.yaml
profiles/desktop.yaml
profiles/dc34.ami.yaml
profiles/example-additional-snapshots.yaml
profiles/github-review.yaml
profiles/goose.yaml
profiles/learn.v2.yaml
profiles/learn.v2.chatty.yaml
profiles/learn.v2.codex.yaml
profiles/learn.v2.polite.yaml
profiles/locked.yaml
profiles/locked.ami.yaml
pkg/profile/builtins/ao.yaml
pkg/profile/builtins/codex.yaml
pkg/profile/builtins/goose.yaml
pkg/profile/builtins/hardened.yaml
pkg/profile/builtins/learn.yaml
pkg/profile/builtins/open-dev.yaml
pkg/profile/builtins/restricted-dev.yaml
pkg/profile/builtins/sealed.yaml
```
(22 total; script header says "21-file" — minor count drift in the comment)

Profiles NOT in the array (confirmed omitted): `learn.v2.parallel.yaml`, `learn.v2.private-allow.yaml`, `learn.v2.desktop.yaml`, `locked.ami.yaml` is listed.

### Builtins list (from `pkg/profile/builtins.go:14`)

```go
var builtinNames = []string{"open-dev", "restricted-dev", "hardened", "sealed", "goose", "ao", "codex", "learn"}
```

Builtins are embedded via `//go:embed builtins` and are UNTOUCHED by this phase.

### New `PROFILES=()` after Phase 120

```
profiles/learner.yaml
profiles/desktop.yaml
profiles/github.yaml
pkg/profile/builtins/ao.yaml
pkg/profile/builtins/codex.yaml
pkg/profile/builtins/goose.yaml
pkg/profile/builtins/hardened.yaml
pkg/profile/builtins/learn.yaml
pkg/profile/builtins/open-dev.yaml
pkg/profile/builtins/restricted-dev.yaml
pkg/profile/builtins/sealed.yaml
```
(11 total)

### Critical: validate-all-profiles.sh skip loop does NOT cover `base/os/`

Current loop (line 31): `for frag in profiles/base/*.yaml` — matches only direct children of `base/`, NOT `base/os/*.yaml` (no recursive glob in bash).

**Required fix:** Extend the skip block to also cover `profiles/base/os/*.yaml`:
```bash
if [ -d profiles/base ]; then
  for frag in profiles/base/*.yaml profiles/base/os/*.yaml; do
    [ -e "$frag" ] || continue
    printf '  skip  %s (base fragment)\n' "$frag"
  done
fi
```
Or use `find profiles/base -name '*.yaml'` for a more generic solution.

---

## Task 6: Desktop + GitHub Fresh-Leaf Inputs

### Current `profiles/desktop.yaml` — key fields for new `desktop.yaml` leaf

- `spec.runtime.ami: ubuntu-24.04` → goes in `base/os/debian.yaml`
- `spec.runtime.desktop.{enabled:true, mode:kiosk, browsers:[firefox], geometry:1920x1080}` → stays in leaf
- `spec.runtime.{spot:false, hibernation:false, rootVolumeSize:30, substrate:ec2, instanceType:t3.large, region:us-east-1}` → stays in leaf (bool trap)
- `spec.execution.{useBedrock:false, privileged:false}` → stays in leaf
- No `initCommands` in current desktop.yaml — Ubuntu bootstrap is handled by the compiler stub in `pkg/compiler/userdata.go` (OS-aware since Phase 93). The `base/os/debian.yaml` fragment supplies the PROFILE-LEVEL init steps (apt install + cert path) that run alongside the compiler stub.
- `notification.email.enabled: true` — desktop notifies via email; `notification.slack.enabled: false` — no Slack. This avoids Rule S5 WARN (both channels off).

### Current `profiles/github-review.yaml` — key fields for new `github.yaml` leaf

- Lean: `ttl:2h`, `idleTimeout:20m`, `instanceType:t3.medium`, `spot:true`
- `spec.execution.initCommands` (RH-specific): yum + cert + npm install claude-code. These would split across `base/os/redhat` (yum+cert) + `base/toolchain-agents` (npm install). The github-review profile installs ONLY claude-code (no codex, no goose, no nvm) — so `base/toolchain-agents` with full toolchain would be heavier than current.
  - **Design decision for planner:** The github.yaml leaf can use `base/os/redhat` + a minimal toolchain, or use the full `base/toolchain-agents`. Per the design spec, github.yaml uses "same base stack minus desktop" which implies the full stack. Planner to decide whether toolchain-agents is appropriate for the lean github leaf.
- `spec.network` explicit block: `.github.com`, `.githubusercontent.com`, `.amazonaws.com`, `api.anthropic.com`, `.npmjs.org`. This is narrower than `base/safenetwork`. Since lists union, including `base/safenetwork` adds more domains. The design shows github.yaml using same base stack — this is a narrowing tradeoff to flag.
- `spec.secrets.sopsFile: github-review-secrets.enc.yaml` — this file path is relative; after github-review.yaml moves to `testdata/profiles/`, the new `github.yaml` leaf needs its own secrets block pointing to the right path.
- `notification.github.inbound.enabled: true` — stays in leaf.

---

## Task 7: km validate WARN Sources

The following conditions trigger `IsWarning: true` in `pkg/profile/validate.go`:

| Rule | Trigger | Avoid in new leaves by |
|------|---------|----------------------|
| S2 | `notification.slack.perSandbox:true` + `notification.slack.enabled:false` | Always set `enabled:true` when using `base/slack-persandbox` |
| S3 | `notification.slack.archiveOnDestroy` set without `perSandbox:true` | `base/slack-persandbox.yaml` must set `perSandbox:true` |
| S-private | `notification.slack.private:true` without `perSandbox:true` | N/A (new leaves won't set `private:true` without perSandbox) |
| S-allow | `notification.slack.inbound.allow` non-empty without `perSandbox:true` | N/A |
| S-maxconcurrency | `maxConcurrentThreads > 1` without both `perSandbox:true` AND `inbound.enabled:true` | N/A for standard learner/github leaves |
| S-channelname | `channelName` set without `perSandbox:true` | `base/slack-persandbox` must include `perSandbox:true` |
| S5 | Both `slack.enabled:false` AND `email.enabled:false` | At least one notification channel enabled in each leaf |
| desktop+AMI | `spec.runtime.desktop.enabled:true` on non-Ubuntu AMI | `desktop.yaml` leaf uses `base/os/debian` (ubuntu-24.04) |
| raw AMI ID | `spec.runtime.ami` matches `^ami-` | OS fragments use slug names (`amazon-linux-2023`, `ubuntu-24.04`) — safe |

**Learner leaf:** Uses `base/slack-persandbox` (sets `perSandbox:true`, `enabled:true`, `inbound.enabled:true`). `notification.email.enabled:false` + `notification.slack.enabled:true` → Rule S5 does NOT fire. No WARNs expected.

**Desktop leaf:** `notification.email.enabled:true`, `notification.slack.enabled:false` → Rule S5 does NOT fire (email on). No perSandbox slack → no S2/S3/S-channelname WARNs. AMI is `ubuntu-24.04` (slug) → no raw-AMI WARN.

**GitHub leaf:** Needs at least one notification channel. Current `github-review.yaml` has no `notification` block — this would trigger S5 (`emailOn` defaults to `true` when `notification.email` is nil in validate.go:312 — `emailOn := true` by default). The new `github.yaml` should include at minimum `notification.github.inbound.enabled:true` — this alone does NOT trigger S5 (the email default is `true`). But to be explicit and operator-friendly, recommend including `notification.email.enabled:true` or the slack-persandbox fragment.

---

## Standard Stack

| Mechanism | Version | Status |
|-----------|---------|--------|
| `profile.Resolve(name, searchPaths)` | Phase 117 | Searches `searchPaths` for `name.yaml`; resolves DAG; memoizes |
| `metadata.abstract: true` | Phase 117 | Abstract fragments skip `km validate` (exit 0 + SKIP message) |
| `extends: [list]` | Phase 117 | Left→right base fold; child wins scalars; lists concat+dedup |
| `deepMerge` | Phase 117 (`inherit.go:27`) | Maps recurse; slices concat+dedup; scalars src-wins |
| `concatDedup` | Phase 117 (`inherit.go:69`) | Order-preserving, first-occurrence kept, `reflect.DeepEqual` |
| `clearAbstractFromMetadata` | Phase 117 (`inherit.go:260`) | Strips abstract from resolved leaf |

No new Go packages needed. No Lambda change. Deploy = `make build` only.

---

## Architecture Patterns

### Fragment search path resolution

When `Resolve` walks a leaf in `profiles/learner.yaml` with `extends: [base/os/redhat, ...]`:
1. `resolveMap("learner", "", [repoRoot/profiles], ...)` loads `profiles/learner.yaml`
2. For parent `"base/os/redhat"`, effective search = `[resolvedDir, searchPaths]` = `[profiles/, profiles/]`
3. Tries `profiles/base/os/redhat.yaml` — found.

The `base/os/` nesting resolves because `loadRaw` constructs `filepath.Join(dir, name+".yaml")` and `"base/os/redhat" + ".yaml"` = `"base/os/redhat.yaml"` under the first searchPath. No special handling needed.

### Fragment authoring rules

1. Declare `metadata.abstract: true` — required.
2. Only set fields the fragment OWNS. Do NOT set `spec.runtime.spot`, `spec.runtime.hibernation`, `spec.runtime.mountEFS`.
3. `spec.runtime.ami` is a string — safe to set in OS fragments.
4. `spec.execution.env` entries are scalar string maps — safe in toolchain fragment (no bool values).
5. `spec.execution.configFiles` entries are string maps — safe in plugin fragment.

---

## Common Pitfalls

### Pitfall 1: validate-all-profiles.sh `base/os/` not skipped
**What goes wrong:** The skip loop uses `profiles/base/*.yaml` (single-level glob). `profiles/base/os/redhat.yaml` is NOT matched. `km validate` is called on it standalone; it fails with missing required fields.
**Prevention:** Extend the skip block to cover `profiles/base/os/*.yaml` (or use `find`).

### Pitfall 2: phase92 test's profilesDir not updated
**What goes wrong:** `userdata_phase92_byte_identity_test.go:34` constructs `profilesDir = filepath.Join(repoRoot, "profiles")`. After move, `profiles/learn.v2.yaml` does not exist; `profile.Resolve("learn.v2", []string{profilesDir})` returns error; test fails with "profile not found".
**Prevention:** Update to `filepath.Join(repoRoot, "testdata", "profiles")`.

### Pitfall 3: github_review_secrets_test reads from absolute path
**What goes wrong:** `github_review_secrets_test.go:32` constructs `filepath.Join(repoRoot, "profiles", "github-review.yaml")`. File not found after move.
**Prevention:** Update to `filepath.Join(repoRoot, "testdata", "profiles", "github-review.yaml")`.

### Pitfall 4: Bool zero-value pollution from OS fragment
**What goes wrong:** `base/os/redhat.yaml` declares `spec.runtime: {ami: amazon-linux-2023, spot: false, hibernation: false}`. Child leaf that wants `spot: true` gets `false` from fragment even if leaf sets `spot: true` — NO, actually scalar `src wins` in deepMerge means child wins. The real problem is if the leaf OMITS the bool and the fragment sets it false.
**Prevention:** OS fragments declare ONLY `spec.runtime.ami`. All bools (`spot`, `hibernation`, `mountEFS`) stay exclusively in the leaf.

### Pitfall 5: Agent golden test uses Resolve + profilesDir derived from path string
**What goes wrong:** `agent_claude_golden_test.go:58` derives `profilesDir` from the path constant string. If path is updated but the derivation logic is not matched exactly, `Resolve` will search the wrong directory.
**Prevention:** After changing `"../../profiles/dc34.yaml"` → `"../../testdata/profiles/dc34.yaml"`, verify that `filepath.Dir(f.profilePath[len("../../"):])` = `"testdata/profiles"` not `"profiles"`.

### Pitfall 6: `base/toolchain-agents` codex config.toml line
**What goes wrong:** `learn.v2.codex.yaml` has an initCommands line that writes `/home/sandbox/.codex/config.toml` manually. If this line is included in `toolchain-agents`, the compiler's `synthesizeCodexConfig` would be overwritten at boot by the initCommands line — or vice versa. The compiler synthesizes codex config from `spec.agent.codex`; the initCommands line should NOT write it.
**Prevention:** Exclude the manual `codex/config.toml` write from `toolchain-agents`. The compiler handles it.

---

## Code Examples

### Resolve with testdata searchPath (post-move pattern)

```go
// pkg/compiler/userdata_phase92_byte_identity_test.go — after update
repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
profilesDir := filepath.Join(repoRoot, "testdata", "profiles")  // CHANGED
p, err := profile.Resolve("learn.v2", []string{profilesDir})
```

### agent_claude_golden_test.go fixtures — after update

```go
// Source: pkg/compiler/agent_claude_golden_test.go:41-44
{"learn.v2", "../../testdata/profiles/learn.v2.yaml", "testdata/claude_settings_learn_v2.golden.json"},
{"dc34",     "../../testdata/profiles/dc34.yaml",     "testdata/claude_settings_dc34.golden.json"},
{"locked",   "../../testdata/profiles/locked.yaml",   "testdata/claude_settings_locked.golden.json"},
{"codex",    "../../testdata/profiles/codex.yaml",    "testdata/claude_settings_codex.golden.json"},
```

### github_review_secrets_test.go path — after update

```go
// Source: pkg/profile/github_review_secrets_test.go:32
profilePath := filepath.Join(repoRoot, "testdata", "profiles", "github-review.yaml")  // CHANGED
```

### Fragment skeleton (base/os/redhat.yaml)

```yaml
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  abstract: true
  name: os-redhat
spec:
  runtime:
    ami: amazon-linux-2023
  execution:
    initCommands:
      - "yum install -y git nodejs npm python3 python3-pip bzip2 jq tar gzip unzip tmux cronie"
      - "systemctl enable crond; systemctl start crond"
      - "cat /usr/local/share/ca-certificates/km-proxy-ca.crt >> /etc/pki/tls/certs/ca-bundle.crt"
      - "echo export SSL_CERT_FILE=/etc/pki/tls/certs/ca-bundle.crt >> /etc/profile.d/km-zz-goose-otel.sh"
```

---

## State of the Art

| Old Approach | Current Approach | Notes |
|--------------|------------------|-------|
| 6 copies of toolchain initCommands | Single `base/toolchain-agents.yaml` | Phase 120 |
| Per-profile `profiles/` directory with 20+ files | `profiles/` with 3 leaves + fragments | Phase 120 |
| Phase 117: single-level `base/*.yaml` glob in validate-all-profiles.sh | Extended to `base/**/*.yaml` | Phase 120 fix required |

---

## Validation Architecture

### Test framework

| Property | Value |
|----------|-------|
| Framework | Go test (stdlib) |
| Config file | none needed |
| Quick run (compiler + profile) | `go test ./pkg/compiler/... ./pkg/profile/... -count=1 -timeout 600s` |
| Full suite | `go test ./... -count=1 -timeout 600s` |
| Validate script | `make build && bash scripts/validate-all-profiles.sh` |

### Phase Requirements → Test Map

| Assertion | Behavior | Test / Command | File Exists? |
|-----------|----------|----------------|-------------|
| File moves preserve bytes | Archived profiles are byte-identical | `git diff testdata/profiles/learn.v2.yaml` (after move, no content change) | ✅ (after move) |
| Byte-identity: learn.v2 golden | Compiled userdata for `learn.v2` unchanged | `TestUserdataLearnV2Phase92ByteIdentity` | ✅ `pkg/compiler/userdata_phase92_byte_identity_test.go` |
| Byte-identity: claude goldens | `synthesizeClaudeSettings` output unchanged for learn.v2/dc34/locked/codex | `TestSynthesizeClaudeSettingsGolden` | ✅ `pkg/compiler/agent_claude_golden_test.go` |
| Byte-identity: codex golden | `synthesizeCodexConfig` output unchanged for codex | `TestSynthesizeCodexConfigGolden` | ✅ `pkg/compiler/agent_codex_golden_test.go` |
| github-review secrets intact | Moved profile still has spec.secrets.sopsFile | `TestGitHubReviewProfileSecrets` | ✅ `pkg/profile/github_review_secrets_test.go` |
| H1 byte-identity | H1-free profile userdata unchanged | `TestUserdataH1ByteIdentity` | ✅ uses ec2-basic.yaml from testdata (no change needed) |
| All base fragments are abstract | `km validate profiles/base/*.yaml` exits 0 (SKIP) | `km validate profiles/base/os/redhat.yaml` etc. | ❌ Wave 0: new fragments not yet authored |
| All 3 leaves validate clean | `km validate profiles/{learner,desktop,github}.yaml` exit 0, no WARN | Leaf validation | ❌ Wave 0: new leaves not yet authored |
| validate-all-profiles covers new inventory | Script exits 0 | `make build && bash scripts/validate-all-profiles.sh` | ❌ Wave 0: script not yet rewritten |
| learner functionally matches learn.v2 | Diff compiled userdata; differences explainable | `km shell --ami $LEARNER_ID` vs `km shell --ami $LEARNV2_ID` diff (review gate, not automated) | N/A (review gate) |

### Sampling Rate

- **Per task commit:** `go test ./pkg/compiler/... ./pkg/profile/... -count=1 -timeout 300s`
- **Per wave merge:** `go test ./... -count=1 -timeout 600s && make build && bash scripts/validate-all-profiles.sh`
- **Phase gate:** Full suite green + validate-all-profiles.sh exit 0 + `km validate profiles/{learner,desktop,github}.yaml` no WARN

### Wave 0 Gaps

- [ ] `testdata/profiles/` needs to be the target for git-moved profiles (directory exists, just needs files)
- [ ] `profiles/base/os/redhat.yaml` — new abstract fragment
- [ ] `profiles/base/os/debian.yaml` — new abstract fragment
- [ ] `profiles/base/toolchain-agents.yaml` — new abstract fragment
- [ ] `profiles/base/plugin-klanker.yaml` — new abstract fragment
- [ ] `profiles/base/slack-persandbox.yaml` — new abstract fragment
- [ ] `profiles/learner.yaml` — new leaf
- [ ] `profiles/desktop.yaml` — new leaf (replaces archived `desktop.legacy.yaml`)
- [ ] `profiles/github.yaml` — new leaf (replaces archived `github-review.yaml`)
- [ ] `scripts/validate-all-profiles.sh` rewrite
- [ ] 6 test path constant updates (see Task 1 table)

---

## Open Questions

1. **github.yaml toolchain weight**
   - What we know: current github-review.yaml installs only `yum + cert + claude-code`; full `toolchain-agents` adds goose + codex + nvm + gsd + herdr
   - What's unclear: design says "same base stack minus desktop" but the lean runtime makes the full stack odd
   - Recommendation: For Phase 120, use the full `base/toolchain-agents` (design-spec says same stack); operator can customize later via a leaner fragment.

2. **github.yaml notification block and WARN avoidance**
   - What we know: current github-review.yaml has no `notification` block; `emailOn` defaults to `true` in validate.go (line 312)
   - What's unclear: design says `notification.github.inbound.enabled:true`; should Slack also be included?
   - Recommendation: Add `notification.email.enabled: false` + `notification.slack.enabled: true` + `base/slack-persandbox`, or just explicitly set `notification.email.enabled: true` (operator email notification). The latter avoids needing a per-sandbox Slack channel for a lean github bot.

3. **learner plugin-enable (open item from CONTEXT.md)**
   - Recommendation (from CONTEXT.md): Enable the klanker plugin (`enabledPlugins: {klanker@klanker-maker: true}` in `base/plugin-klanker.yaml` via `configFiles["/home/sandbox/.claude/settings.json"]`). This is safe because the frozen `learn.v2.yaml` byte-identity fixture is now decoupled from the live profile.

4. **km-config.yaml runtime update**
   - What we know: `km-config.yaml:60` references `profiles/learn.v2.yaml`; after move this file doesn't exist at that path
   - Recommendation: Update `km-config.yaml` as part of Phase 120 (either in a final plan or as a note for the operator). The code change is minor but the operator must also re-run `km init --github` to republish the SSM command map.

---

## Sources

### Primary (HIGH confidence)
- Direct code inspection — `pkg/profile/inherit.go` (deepMerge, resolveMap, concatDedup)
- Direct code inspection — `pkg/profile/validate.go` (IsAbstractFragment, WARN rules)
- Direct code inspection — `pkg/compiler/userdata_phase92_byte_identity_test.go`
- Direct code inspection — `pkg/compiler/agent_claude_golden_test.go`
- Direct code inspection — `pkg/compiler/agent_codex_golden_test.go`
- Direct code inspection — `pkg/compiler/userdata_h1_byte_identity_test.go`
- Direct code inspection — `pkg/profile/github_review_secrets_test.go`
- Direct code inspection — `scripts/validate-all-profiles.sh`
- Direct code inspection — `pkg/profile/builtins.go`
- File read — `profiles/learn.v2.yaml`, `profiles/learn.v2.chatty.yaml`, `profiles/learn.v2.codex.yaml`
- File read — `profiles/github-review.yaml`, `profiles/desktop.yaml`, `profiles/dc34.yaml`

### Secondary (MEDIUM confidence)
- Grep results — `internal/app/cmd/doctor_github_test.go`, `webhook_handler_phase115_test.go` (confirmed config-string only, no file reads)
- Grep results — `internal/app/cmd/init_github_prestage_test.go` (confirmed S3 slug strings, not filesystem paths)
- Grep results — `internal/app/cmd/init.go:773` (confirmed runtime `readFileOrEmpty("profiles/"+slug+".yaml")` soft-fail behavior)

---

## Metadata

**Confidence breakdown:**
- File move targets: HIGH — confirmed by direct `ls` + `find`
- Test path constants: HIGH — confirmed by direct code read, line-by-line
- deepMerge mechanics: HIGH — confirmed from `inherit.go` source
- Validate WARN rules: HIGH — confirmed from `validate.go` source
- Fragment slicing: MEDIUM — derived from profile content; final authoring judgment stays with planner
- Docs staleness: HIGH (confirmed stale) — docs update scope is LOW priority vs test fixes

**Research date:** 2026-06-25
**Valid until:** 90 days (stable domain; no external APIs)

---

## RESEARCH COMPLETE
