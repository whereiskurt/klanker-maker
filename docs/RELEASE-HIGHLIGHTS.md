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

  Drafted from CLAUDE.md phase blocks since v0.8.20 via
  scripts/draft-release-highlights.sh, then curated.
-->

## 🧊 A HackerOne cold-create now delivers the prompt

Since Phase 103 the H1 bridge's absent-sandbox path published `SandboxCreate` with
`h1_envelope`, but the create-handler only ever drained `github_envelope` — a
`report_created` arriving with no `h1-<handle>` box provisioned one that never received
the triage. `drainInboundEnvelope` now serves both bridges from one function. All three
states work: running (immediate), stopped/paused (bridge wakes it, prompt drains on boot),
absent (cold-create, then drain). Pre-creating the box is still the faster first triage.

## 🔑 Remote `km create` of an H1 or webhook profile no longer 403s

The create-handler role had SQS lifecycle grants for `slack-inbound-*` and
`github-inbound-*` only. A remote `km create` runs *inside* that Lambda and the H1 /
webhook queue provisioning is fatal, so the very first `km create profiles/h1.yaml` would
have died on `sqs:CreateQueue AccessDenied` — unnoticed because Phase 103's UAT was never
run. `km-operator-policy` now grants `h1-inbound-*` (with `SendMessage` for the drain)
and `webhook-inbound-*`. A name-agnostic guard derives the queue kinds from `pkg/aws`'s
`*InboundQueueName` helpers, so the next bridge cannot ship without its grant.

## 📖 The bundled operator guide has the HackerOne quick-start

The release tarball ships `OPERATOR-GUIDE.md` but not `docs/`, so the guide now carries
the whole sequence — `km h1 init` → `km-config.yaml` (`reply: none`, `debug_capture`) →
full-apply deploy → create the box → the first-event capture check with the exact `jq`.
`docs/h1-bridge.md`'s example also stops pointing at `profiles/h1-triage.yaml` (never
existed) and the pre-Phase-120 prompt paths.

**Deploy:** `make build` → `make build-lambdas` → `km init --dry-run=false` (create-handler
zip + IAM; not `--sidecars`). No sandbox recreate for this release.
