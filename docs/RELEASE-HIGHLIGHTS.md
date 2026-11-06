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

  Curated by hand for v0.10.0: PR #164 (km hermes + the http-proxy SigV4
  Accept-Encoding fix). v0.9.4's notes (Slack channel history) are in that
  release on GitHub.
-->

## ⚠️ Upgrade notes — read before deploying

- **Deploy:** `make build` → `km init --sidecars`.
  - `make build` alone ships the `km hermes` verbs, which work on every running sandbox
    over SSM.
  - `--sidecars` ships km-presence signal 10 and the http-proxy fix.
  - No Terraform, IAM, Lambda, userdata, schema or Slack App change.
- **Existing sandboxes get the sidecars only on `km destroy && km create`.** Until then,
  a Bedrock turn from Hermes on that box fails with a 403 "signature … does not match"
  (the old proxy), and an open Hermes tab does not keep the box awake. `km hermes status`
  warns about the second.

## 🪽 Hermes Agent + WebUI on a sandbox — `km hermes`

```
km hermes start  <sb>   install the pinned pair (agent v0.22.4 + webui v0.52.544), start in the background
km hermes        <sb>   forward localhost:8788 over SSM and print the login password
km hermes status <sb>   versions, server, provider, what the idle timer sees
km hermes stop   <sb>   stop it, and keep it stopped across resume
```

- **Loopback only, and a password is always set.** The password lives in a root-only
  file on the box, and `--rotate-password` replaces it.
- **Bedrock without a key.** On a `useBedrock: true` profile, `start` configures Bedrock
  through the instance role and installs its dependencies. Spend shows in `km status`.
  Any other provider uses the WebUI's onboarding wizard or `hermes model` in `km shell`.
- **Pinned on purpose.** The WebUI's "Update Now" offer is switched off once, so the
  agent and WebUI stay a tested pair. Upgrade with
  `km hermes start --agent-version … --webui-version …`.
- **km-presence signal 10** keeps the box awake only while a browser is attached or a
  turn is running. A started but unused server does not. Signal 5 now also ignores the
  claude/codex subagents Hermes starts. Its exclusion is now an exact match on the
  system unit's cgroup, which also fixes a false match for t3.
- `profiles/hermes.yaml` is a ready profile: unprivileged, with Bedrock and open egress.
  Locked-egress profiles need the host list in `docs/hermes.md` § Egress.
- Live UAT passed 14 of 14 checks. See `docs/hermes.md`.

## 🔏 Python clients on Bedrock work through km's proxy

km's http-proxy dropped every client's `Accept-Encoding` header, which is goproxy's
default. A client that signs that header into its SigV4 signature then got a 403. The
Anthropic Python SDK (`AnthropicBedrock`) does, while botocore and Claude Code do not,
which is why nobody had noticed.

- The header is now kept only when the signature covers it; every other request is
  unchanged.
- Metering decodes a compressed copy of the response for parsing; the client's bytes are
  untouched.

## ✨ Small things

- The operator container image now publishes `km t3`'s port 3774 (previously missing)
  and `km hermes`'s 8788.
- Known, not fixed: the Bedrock meter does not count prompt-cache tokens, so cached
  prompts on Bedrock are under-counted for every client.
