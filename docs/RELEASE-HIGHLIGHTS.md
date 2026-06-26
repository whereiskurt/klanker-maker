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
-->
## ✨ Major additions highlighted

1. **🧬 Composable multi-parent profile inheritance (Phase 117)** — `extends:` list + DAG `deepMerge`, `profiles/base/` fragment library, `initCommandsAppend`.
2. **🔐 Slack trigger allowlist + private channels (Phase 118)** — `slack.allow` / `notification.slack.inbound.allow` + `notification.slack.private`; dormant by default.
3. **⚡ Slack inbound per-thread parallelism (Phase 119)** — threadTS FIFO grouping + bounded-concurrent ack-after poller + `maxConcurrentThreads` cap; live-E2E verified.
4. **🎨 Richer Slack rendering** — blocks-rich steering + `invalid_blocks` reply-drop fix.
5. **🛡️ eBPF resolver IP-lifetime floor** — 10m min retention, fixes premature eviction.
