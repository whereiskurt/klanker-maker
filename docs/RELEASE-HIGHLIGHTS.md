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

  Drafted from CLAUDE.md phase blocks since v0.8.16 via
  scripts/draft-release-highlights.sh, then curated.
-->

## 🐳 Port-forwards from the operator container now reach your host

`session-manager-plugin` hard-codes its listener to `localhost` with no bind option, so
from inside the container every `km vscode`, `km herdr`, `km desktop`, `km model` and
`km tunnel` forward (and `km shell --ports`, codex OAuth) landed on the *container's*
loopback — which `docker run -p` can never reach. The "connect to `localhost:2222`" line
was silently false.

`KM_FORWARD_BIND` makes km start the plugin on a private loopback port and relay
`<addr>:<local-port>` onto it. The image sets `0.0.0.0` and `EXPOSE`s the default ports;
`make operator-shell` publishes each on the **host's loopback only** — never a bare `-p`,
which would offer an SSH tunnel into a sandbox to your LAN. Unset, nothing changes.

## 🧱 Sidecar images no longer compile Go under Rosetta

`--platform linux/amd64` cascades to every Dockerfile stage, so on Apple Silicon the
`golang` builder ran the whole toolchain under emulation and segfaulted intermittently in
the scheduler — a `[warn]` partway through `km init`. The builder stage is now pinned to
`$BUILDPLATFORM` and the existing `GOARCH=amd64` cross-compiles; the published manifest
is unchanged and a guard test covers every Dockerfile that runs `go build`.

## 🗺️ Ten security and IaC diagrams

`docs/diagrams/security/` — containment boundaries, compensating layers, brokered secret
unsealing, the bridge Lambda trust boundary, the IMDS fence as paired policy traces, how
terragrunt is actually invoked and which process authors every file it touches, the YAML →
HCL compiler, what `km bootstrap` vs `km init` apply, and every SSM parameter path with
the KMS key that seals it — including the two keys that are not where you would assume.
Single-file HTML; open any of them in a browser.
