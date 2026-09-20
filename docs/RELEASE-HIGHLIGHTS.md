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

  Drafted from CLAUDE.md phase blocks since v0.8.22 via
  scripts/draft-release-highlights.sh, then curated.
-->

## ⚡ `km list` is ~8× faster and flat in the number of sandboxes

It made 4–5 serial AWS calls per sandbox — three of them the identical
`DescribeInstances`-by-tag, plus a per-row AWS config reload — so six boxes cost ~30 round
trips. Now one `DescribeInstances` per launch account covers the whole list (status,
hibernation, the idle floor, and the instance id SSM needs), and the remaining per-row
lookups fan out on a bounded pool. **Measured live on 7 sandboxes: 11.5 s → 1.5 s**, tables
identical. `km status` drops from three describes to one for free. Cross-account boxes are
still described where they live; a transient EC2 error still never mislabels a live box.

## 🩺 km-presence signal 5 had been dead on every sandbox since it shipped

The "headless claude/codex/km-agent-run is running" signal ran `pgrep -afE` — and procps-ng
has no `-E` flag at all. It exited 2 with a usage error that the code read as "no matches",
so a detached `km agent run` with nobody attached could be reaped mid-turn by `idleTimeout`,
silently. Verified on four boxes across AL2023 and Ubuntu; two had a live `claude` it should
have seen. Fixed, with a guard test that fails on any pgrep flag carrying an `E`. Existing
sandboxes keep the dead signal until recreate or a hot swap of `/opt/km/bin/km-presence`.

## 🔁 Back-port the Slack @-mention fixes onto a running sandbox

`scripts/backport-slack-mentions.sh <sandbox-id> [--restart]` re-fetches the `km-slack`
sidecar and patches the inbound poller in place (byte-identical to what v0.8.22 renders,
idempotent, `bash -n`-checked), so an already-created box can "@ you back" without
`km destroy && km create`. It refuses with a diagnosis if the install's `sidecars/` copy
predates the fix.

**Deploy:** `make build` (for `km list`) + `km init --sidecars` (for `km-presence`). No
Lambda, Terraform, schema or userdata change; no sandbox recreate for `km list`.
