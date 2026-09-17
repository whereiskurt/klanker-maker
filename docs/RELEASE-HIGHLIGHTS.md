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

  Drafted from CLAUDE.md phase blocks since v0.8.17 via
  scripts/draft-release-highlights.sh, then curated.
-->

## 🔇 A failed initCommand is no longer silent

`/tmp/km-init.sh` runs every `initCommands` entry under one `set -e` — correct, and kept.
What was wrong was that the abort was invisible: an unpublished npm pin killed step 3 of 13,
the ten commands after it never ran, and the boot printed `Init complete` anyway (errexit is
suspended inside an `&&` list), went on to `SANDBOX_READY`, and `km list` was green.

The script now numbers its steps and traps `ERR`, so the log reads
`[km-init] FAILED (exit 1) at step 3/13: npm install -g …`; the bootstrap prints a WARNING
with the count of commands skipped and emits an `init_failed` audit event; `km status` shows
an `Init: FAILED …` line and `km doctor` gains a **Sandbox profile init** check. Still
non-fatal — a box that is up and honestly labelled beats an aborted boot.

## 🔑 The secret shims were losing the PATH race after all

Both PATH hooks guarded with "already on PATH → do nothing". profile.d adds `/opt/km/shims`
first, so the `~/.bashrc` block — the one that exists to beat nvm — always found it already
there, took the no-op arm, and left nvm's bin ahead. Profiles escaped only because claude was
npm-installed as root outside nvm; the first `claude` self-update as the sandbox user landed
in nvm's prefix and ended the accident. And the shim fell back to a PATH search only if its
baked target had *vanished*, so it kept wrapping the stale copy.

Both hooks now strip-then-prepend, the shim prefers whatever `command -v` finds with the
shim dir removed, and the pollers' own `~/.local/bin` prepend re-asserts the shims after it —
a third way to lose the same race, found while confirming Slack-dispatched turns are shimmed.
Tests execute the real hook text and the real shim, not a string-presence check.

## ∞ `km list` no longer overflows on a huge TTL

`ttl: 86000h` rendered as `85999h42m ttl`, three characters wider than the column. Detail
now matters more the closer the deadline is: `46m` → `1h30m` → `6d23h` → `364d` → `2y364d`,
and past three years simply `∞`. `--json` keeps a numeric string.

All three are create-time: existing sandboxes keep the old behaviour until
`km destroy && km create`.
