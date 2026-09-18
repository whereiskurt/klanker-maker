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

  Drafted from CLAUDE.md phase blocks since v0.8.19 via
  scripts/draft-release-highlights.sh, then curated.
-->

## 🤫 HackerOne auto-triage can now run without touching the report

`h1.programs[].events.<event>.reply: none` makes an auto-triage event write-free toward
HackerOne: no bridge "On it" ack, no Phase 121 frozen/quota notices, no Phase 106
resume-hint comment, and the sandbox poller's preamble stops telling the agent to post —
it now says *do NOT post; a human will decide*. The agent runs whatever your `@file` prompt
names (your own triage skill, Slack, PDFs) and an analyst blesses the result later with an
internal `@km /triage`, which is the unchanged comment-keyword flow and is never silenced.
Per-event, absent/`internal` ⇒ byte-identical to before.

## 🎥 Capture what HackerOne actually sends

`h1.debug_capture: true` writes every raw delivery — `{received_at, headers, body}` — to
`s3://<artifacts>/h1-captures/<X-H1-Delivery>.json` as step 0, **before** signature
verification, so a mis-pasted secret still yields a capture. Phase 103 pinned the parser
against a synthetic payload; the routing key
(`data.report.relationships.program.data.attributes.handle`) has never been seen in a live
webhook. Turn it on for the first real `report_created`, confirm the path exists, turn it
off (captures hold full vulnerability reports). The drop-path log lines also now carry
`top_level_keys`, so a wrong wrapper is visible in CloudWatch without capture.

## ⚠️ Known: an H1 cold-create loses the prompt (pre-existing)

The create-handler drains only `github_envelope`, never `h1_envelope`, so an auto-triage
event arriving with no `h1-<handle>` sandbox row cold-creates a box that never receives the
prompt — and under `reply: none` nothing on HackerOne reveals the loss. Keep the target
sandbox created (stopped or paused resumes fine). Draining `h1_envelope` is the next
fast-follow.

**Deploy:** `make build` → `make build-lambdas` → `km init --dry-run=false` (not `--sidecars`),
then `km destroy && km create` the `h1-<handle>` sandbox. See `docs/h1-bridge.md`
§ Silent auto-triage and § Capturing the real payload.
