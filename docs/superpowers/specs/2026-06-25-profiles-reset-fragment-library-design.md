# Profiles reset + OS-layered fragment library — design

**Date:** 2026-06-25
**Status:** Approved (brainstorm) — pending spec review → writing-plans
**Deploy class:** `make build` only. No Lambda rebuild, no schema-on-write DDB change, no `km init`, no sandbox recreate. Identical to Phase 117 (inheritance resolves at `km validate` / `km create` time).

## Goal

Reset `profiles/` to a small, clean set of **3 operator-facing demo profiles** — `learner`, `desktop`, `github` — each composed from a richer `profiles/base/` fragment library. The high-churn toolchain install block (currently copy-pasted across ~6 files; chatty differs from base by one line) is layered out into reusable fragments so a version bump (`claude-code@2.1.132`, `codex v0.133.0`) touches **one** file. Every retired demo and frozen test fixture is **archived** into the existing `testdata/profiles/` directory (not deleted), with test paths updated in lockstep so byte-identity and golden contracts stay green.

The new `learner` profile must **functionally match today's `learn.v2.yaml`** (same toolchain, same per-sandbox Slack, same GitHub inbound) — composed, not trimmed.

## Constraints (load-bearing — from CLAUDE.md / memory)

1. **Byte-identity contract.** `learn.v2.yaml`, `dc34.yaml`, `codex.yaml`, `locked.yaml` (and `github-review.yaml`/`dc34.ami.yaml` for the h1 + secrets tests) are loaded by `pkg/compiler/*_test.go` and `pkg/profile/*_test.go` as inputs. `userdata_learn_v2_pre92_baseline.golden.sh` is a **frozen** baseline the test strips `SubagentStop` from — it must **never** be re-captured. Moving an input profile is allowed only if the test's path constant is updated to match; the bytes do not change.
2. **Golden outputs do not move.** `pkg/compiler/testdata/*.golden.{sh,json,toml}` stay co-located with the compiler test (idiomatic Go). Only **input profiles** relocate.
3. **`extends:` is list-union only** (Phase 117). A child cannot shrink a base's list. Slice fragments so no leaf needs a *narrower* list than its base provides.
4. **Bool zero-value trap** (Phase 117). A fragment that writes a full block containing non-pointer `bool` fields pushes their zero-value (`false`) onto children. `spec.runtime` mixes bools (`spot`/`hibernation`/`mountEFS`) → the **bool fields stay in the leaf**. `spec.runtime.ami` is a **string** and is safe to set in an OS fragment.
5. **`profiles/checks/**` and `profiles/secrets/**` are not SandboxProfiles** (km-check demo assets + example SOPS). `pkg/check/bootstrap.go` references `profiles/checks/_bootstrap/...`. They **stay put**, untouched.
6. **Prompt `.txt` files stay in `profiles/`** (`default.github.prompt.txt`, `h1.*.prompt.txt`) — they are `@`-file prompt assets referenced by tests and the operator bridge config; moving them adds churn for no cleanup benefit.

## Target end-state layout

```
profiles/                         # operator-facing demos only
├── learner.yaml                  # NEW, composed — functionally == old learn.v2.yaml
├── desktop.yaml                  # NEW, composed — Ubuntu (base/os/debian)
├── github.yaml                   # NEW, composed — was github-review.yaml
├── *.prompt.txt                  # STAY (default.github / h1.* prompt assets)
├── base/
│   ├── os/
│   │   ├── redhat.yaml           # NEW: ami amazon-linux-2023, yum pkgs, RH cert path, RH init steps
│   │   └── debian.yaml           # NEW: ami ubuntu-24.04, apt-over-443, ssh.service, Debian cert path
│   ├── toolchain-agents.yaml     # NEW: OS-agnostic claude-code@PIN + codex@PIN + nvm + gsd + herdr + goose
│   ├── plugin-klanker.yaml       # NEW: known_marketplaces.json + installed_plugins seed + enabledPlugins
│   ├── slack-persandbox.yaml     # NEW: notification.slack per-sandbox block
│   ├── safenetwork.yaml          # existing (unchanged)
│   ├── sidecars-all.yaml         # existing
│   ├── observability-learn.yaml  # existing
│   ├── budget-standard.yaml      # existing
│   ├── artifacts-workspace.yaml  # existing
│   ├── iam-us-east-1.yaml        # existing
│   ├── agent-claude-all-tools.yaml  # existing
│   └── email-strict.yaml         # existing
├── checks/                       # STAY (km-check demos)
└── secrets/                      # STAY (example SOPS)

testdata/profiles/                # retired fixtures join the existing invalid-*/valid-*
├── (existing) invalid-bad-substrate.yaml, invalid-missing-spec.yaml,
│              invalid-unknown-field.yaml, valid-docker-substrate.yaml, valid-minimal.yaml
├── ao.yaml, codex.yaml, goose.yaml
├── dc34.yaml, dc34.ami.yaml
├── learn.v2.yaml, learn.v2.{chatty,codex,polite,parallel,private-allow,desktop}.yaml
├── locked.yaml, locked.ami.yaml
├── github-review.yaml, github-review/{.km-profile.yaml,.km-secrets-bundle.enc.yaml}
├── example-additional-snapshots.yaml
├── h1-triage.yaml, check-triage.yaml
└── desktop.legacy.yaml           # old desktop.yaml, archived for reference
```

## Fragment library design

### `base/os/redhat.yaml` (abstract)
- `spec.runtime.ami: amazon-linux-2023` (string — safe in fragment).
- `spec.execution.initCommands` (RH-family, OS-specific, run first):
  - `yum install -y git nodejs npm python3 python3-pip bzip2 jq tar gzip unzip tmux cronie`
  - `systemctl enable crond; systemctl start crond`
  - RH cert trust: `cat /usr/local/share/ca-certificates/km-proxy-ca.crt >> /etc/pki/tls/certs/ca-bundle.crt`
  - `SSL_CERT_FILE=/etc/pki/tls/certs/ca-bundle.crt` profile.d export
- `metadata.abstract: true`.

### `base/os/debian.yaml` (abstract)
- `spec.runtime.ami: ubuntu-24.04`.
- Debian-family init: apt-over-HTTPS (SG is 443-only), `ForceIPv4`, python3 AWS-CLI install, `ssh.service`, `systemd-resolved` stop for the eBPF resolver, Debian cert path (`/usr/local/share/ca-certificates` + `update-ca-certificates`). (Mirrors the OS-aware bootstrap already in `pkg/compiler/userdata.go`; the fragment supplies the *profile-level* init steps, not the compiler stub.)
- `metadata.abstract: true`.

> **Ordering:** `extends:` resolves left→right, and `initCommands` union is concat-in-order, first-occurrence-kept. Listing `base/os/*` **first** in each leaf's `extends:` guarantees OS package + cert steps precede toolchain steps.

### `base/toolchain-agents.yaml` (abstract, OS-agnostic)
- `spec.execution.initCommands` — the install block shared by all three leaves, *minus* OS package-manager and cert-bundle steps:
  - goose CLI install + copy to `/usr/local/bin` + sandbox `~/.local/bin`
  - `npm install -g @anthropic-ai/claude-code@2.1.132`  ← **single pin site**
  - codex `rust-v0.133.0` download + install  ← **single pin site**
  - nvm install + `nvm install 22`
  - `npx -y get-shit-done-cc --claude --codex --global`
  - `curl -fsSL https://herdr.dev/install.sh | sh`
  - goose OTEL profile.d export
- `spec.execution.rsyncPaths: [.gitconfig, .config/goose, .claude, .claude.json, .codex]`
- `spec.execution.env` agent-related keys (GOOSE_*, CODEX_CA_CERTIFICATE, OPENAI_API_KEY="") — **string env only, no bools**.
- `metadata.abstract: true`.

### `base/plugin-klanker.yaml` (abstract)
- `spec.execution.configFiles["/home/sandbox/.claude/plugins/known_marketplaces.json"]` (marketplace seed).
- `spec.execution.initCommands`: the `installed_plugins.json` git-clone + symlink + SHA-stamp one-liner.
- `enabledPlugins` settings (for variants that enable the klanker plugin in headless `claude -p`). **Note:** the frozen `learn.v2.yaml` deliberately did *not* enable the plugin; the new `learner` **does** enable it (functional match to the chatty/polite variants' headless behavior — confirm during planning whether learner should enable or stay plugin-installed-but-disabled). Resolve before freezing the leaf.

### `base/slack-persandbox.yaml` (abstract)
- `spec.notification.slack` per-sandbox block: `enabled/perSandbox/channelName: "sb-{profile}-{alias}"/archiveOnDestroy:false/inbound.enabled/invites.emails`.

### Leaf composition

```yaml
# profiles/learner.yaml  (== learn.v2.yaml functionally)
extends:
  - base/os/redhat
  - base/toolchain-agents
  - base/plugin-klanker
  - base/safenetwork
  - base/sidecars-all
  - base/observability-learn
  - base/budget-standard
  - base/artifacts-workspace
  - base/iam-us-east-1
  - base/agent-claude-all-tools
  - base/email-strict
  - base/slack-persandbox
spec:
  lifecycle: { ttl: 8h, idleTimeout: 1h, teardownPolicy: stop }
  runtime:                     # bool-trap block — stays in leaf
    substrate: ec2
    instanceType: t3.2xlarge
    region: us-east-1
    spot: false
    hibernation: false
    rootVolumeSize: 80
    mountEFS: true
    efsMountPoint: /shared
    additionalVolume: { size: 30, mountPoint: /data }
  execution: { privileged: true, useBedrock: false, workingDir: /workspace, shell: /bin/bash }
  cli: { noBedrock: true }
  agent: { default: claude }
  notification: { github: { inbound: { enabled: true } }, events: {...}, email: { enabled: false } }
```

`github.yaml` = same base stack minus desktop, lean runtime (t3.medium, 2h/20m), `notification.github.inbound.enabled: true`.
`desktop.yaml` = `base/os/debian` + toolchain + plugin + slack + leaf `spec.runtime.desktop` block + Ubuntu runtime.

## Move mechanics

1. `mkdir -p testdata/profiles/github-review`.
2. `git mv` retired profiles into `testdata/profiles/` (full list in layout above). Old `desktop.yaml` → `testdata/profiles/desktop.legacy.yaml`.
3. Author the new fragments + 3 leaves under `profiles/`.
4. **Update hard-coded test path constants** (`../../profiles/X` → `../../testdata/profiles/X`):
   - `pkg/compiler/*_test.go`: `codex.yaml`, `dc34.yaml`, `learn.v2.yaml`, `locked.yaml` (byte-identity + claude/codex goldens).
   - `pkg/compiler/userdata_h1_byte_identity_test.go`: `github-review.yaml` / h1 fixture.
   - `pkg/profile/github_review_secrets_test.go`: `github-review/` paths.
   - Any `dc34.ami.yaml` reference.
   - (Audit step in plan: `grep -rn 'profiles/' --include='*_test.go'` and reconcile every literal that points at a moved file.)
5. **Rewrite `scripts/validate-all-profiles.sh`** inventory → `profiles/{learner,desktop,github}.yaml` + unchanged `pkg/profile/builtins/*` + optional pass over `testdata/profiles/*.yaml`. Update the header comment's file count.
6. Leave `profiles/checks/**`, `profiles/secrets/**`, `profiles/*.prompt.txt`, `pkg/profile/builtins/**` untouched.

## Verification gates

- `go test ./pkg/compiler/... ./pkg/profile/... ./internal/app/cmd/... -count=1 -timeout 600s` — **green**, capturing the command's own exit code (not a piped `tail`). Byte-identity + goldens intact proves the move preserved bytes.
- `make build && bash scripts/validate-all-profiles.sh` — exit 0.
- `km validate profiles/learner.yaml`, `km validate profiles/desktop.yaml`, `km validate profiles/github.yaml` — exit 0, **no WARN**.
- **Functional-match check for `learner`:** diff the compiled userdata of the new `profiles/learner.yaml` against the archived `testdata/profiles/learn.v2.yaml`. Differences must be explainable (fragment ordering, plugin-enable decision) and intentional — not accidental toolchain drift. (This is a *review* gate, not a byte-identity assertion; the new leaf is deliberately not under the frozen contract.)

## Out of scope

- `!replace` list-narrowing directive (deferred Phase 117 v2 item) — not needed; fragments are sliced so no leaf narrows a base list.
- Per-thread `/workspace` worktree isolation (the `learn.v2.parallel` caveat) — archived as a fixture, not reauthored.
- Re-capturing any frozen golden baseline.
- Touching `pkg/profile/builtins/**` (embedded built-ins are a separate inventory).
- Relocating `profiles/checks/**` or `profiles/secrets/**`.

## Open items to resolve during planning

1. **`learner` plugin-enable:** enable the klanker plugin (match chatty/polite headless behavior) vs. installed-but-disabled (match frozen `learn.v2.yaml`). Pick one; document in the leaf.
2. **Exact archived-fixture set:** confirm via the `grep` audit that every test-referenced profile that is moving has its path updated; confirm no *other* caller (docs, skills, `at.go` help text mentioning `profiles/goose.yaml`) hard-codes a moved path that needs a doc update.
3. **Fragment granularity final check:** confirm `slack-persandbox` is worth a fragment vs. inlined per-leaf (it differs in `invites.emails` per audience).
