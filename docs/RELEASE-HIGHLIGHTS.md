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

For a long time, every klanker-maker sandbox quietly rickrolled `google.com`. It was a one-line
joke compiled into the HTTP proxy, and it was on for everybody, always, with no way to turn it
off short of editing Go.

**This release deletes it.** Nothing is intercepted by default any more.

### 🎭 Declarative MITM intercepts

What replaces it is the thing the easter egg should have been all along: a profile-declared
host→action surface. If you want an intercept, you say so.

```yaml
spec:
  network:
    mitm:
      intercepts:
        - name: maintenance
          hosts: ["api.example.com"]
          action:
            respond:
              status: 503
              body: "maintenance window in progress"
```

Two actions ship: `redirect` and `respond`. Rules are matched by host with allowlist semantics,
carried to the box as a base64 JSON blob in a systemd drop-in, and read by the proxy at start.

**Dormant by default.** A profile with no `spec.network.mitm` block produces byte-identical
user-data to v0.8.3 — the drop-in isn't written and the proxy never wires an intercept handler.

### 🔌 The easter egg is now opt-in, in one line

The Rickroll survives as an abstract fragment, so the joke is preserved for anyone who wants it —
as a choice rather than a default:

```yaml
extends:
  - base/mitm-rickroll      # <- that's the whole opt-in
```

`profiles/mitm-demo.yaml` is the worked example: it inherits the Rickroll through that one line,
adds a `respond` rule of its own, and ships a commented-out off-switch showing how a child
profile turns an *inherited* rule back off:

```yaml
        - name: rickroll
          enabled: false     # switches off a rule inherited via extends:
```

That off-switch is load-bearing. Phase 117 merges profile lists by **union**, so without a
by-name collapse a child could never remove what a base declared — it could only ever add.

### 🛡️ A user intercept cannot shadow platform metering

The one genuinely dangerous shape here is a profile declaring `api.anthropic.com` or
`bedrock-runtime.*.amazonaws.com` and silently swallowing the traffic that AI budget metering
depends on — or `github.com`, shadowing the repo filter.

goproxy's handler chain does **not** stop at a handler returning `nil`, so registration order
alone does not protect those paths. An `isPlatformOwnedHost` guard does, applied identically on
the goproxy path and the transparent (eBPF) path. Platform-owned hosts always win.

### ⚠️ Deploy surface — `--sidecars` alone is actively harmful here

This release changes `pkg/compiler/userdata.go`, so the create-handler zip that renders user-data
must be refreshed. Run the full sequence:

```bash
make build
make build-lambdas
km init --dry-run=false
```

`km init --sidecars` on its own uploads the new proxy binary but **not** the user-data that feeds
it — which drops the built-in Rickroll on a live box while leaving profiles unable to declare it
back until the full sequence completes. See `docs/mitm-intercepts.md` § Deploy surface.

Existing sandboxes keep their current behaviour until `km destroy && km create`.

📖 Full runbook: `docs/mitm-intercepts.md`
