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

  Drafted from CLAUDE.md phase blocks since v0.8.18 via
  scripts/draft-release-highlights.sh, then curated.
-->

## ⌨️ Port-forwards no longer leave you with a dead keyboard

`km herdr start` (and every other SSM forward — `vscode`, `desktop`, `model`, `tunnel`,
`shell --ports`) handed `session-manager-plugin` the terminal's stdin. A port-forward
carries no input, but given the tty the plugin set its own modes, and when the liveness
watcher killed a hung plugin nothing restored them: echo off, keyboard dead, fixed only by
killing the tab. The plugin now gets `/dev/null`, and the terminal is snapshotted before the
first spawn and restored after every exit. If you hit it on an older binary: blind-type
`stty sane`.

## 📄 `.yml` works everywhere `.yaml` does

`km validate spot.yml` failed with "profile not found": the leaf name kept its extension
and the resolver only ever looked for `name.yaml`. One extension list now feeds the resolver
and every CLI site that turns a path into a profile name; a `.yaml` still wins a name tie.

## ⏳ The idle countdown joins the TTL ladder

v0.8.18 fixed `86000h` in the SHUTDOWN and TTL columns; the **IDLE** column and `km status`'s
`Idle Stop:` line were a separate path and still printed `86000h0m0s`. They — and
`km extend`'s "new expiry in" — now use the same ladder: `23m` → `6d23h` → `2y364d` → `∞`.
`--json` keeps parseable strings.

## 🪣 Diagram: the three S3 buckets and what a presign really carries

`docs/diagrams/security/11-s3-buckets-and-presign.html`. Its focal finding, read from
source: `ec2spot_region_lock` grants `Action "*" Resource "*"` conditioned only on
`aws:RequestedRegion`, and every shipped profile carries it via `base/platform` — so the
scoped S3 statements describe the intent, not what AWS evaluates, and a sandbox can presign
any key in the artifacts bucket with no km record. Documented, not yet changed.
