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
## 🐳 The container can now show you what `km init` would change

The operator image was a client for `--remote` sandbox work. It now also runs **real,
read-only terragrunt plans** against your account:

```bash
docker run --rm -it \
  -v "$HOME/.aws:/root/.aws" \
  -v "$PWD/km-config.yaml:/klanker-maker/km-config.yaml" \
  ghcr.io/whereiskurt/klanker-maker/km:latest km init --plan
```

Verified against a live install: 27 modules, `0 to add, 9 to change, 0 to destroy` — the
nine being Lambda `source_code_hash` diffs, which is simply the image's release not being
what is currently deployed.

`terraform` and `terragrunt` now install from the release tarball instead of being
discarded, the terragrunt tree ships in the image, and a new `km_vX.Y.Z_lambdas.tar` release
asset carries the Lambda zips — the nine Lambda-owning modules call `filebase64sha256()` on
those, and terraform evaluates that at **plan** time, so without them a plan fails outright
rather than degrading.

**Applying is still a native-install job**, and for a mechanical reason rather than a policy
one: `km init --dry-run=false` shells out to `go build` at run time and pushes ECR images.
Supporting it would mean a Go toolchain, the full source tree and a docker socket in the
image — at which point it stops being a client.

## 🧬 Your profiles layer over the built-in library

Mount your own profiles and they **add to** the shipped library rather than replacing it, so
a two-line profile of your own can still `extends: base/safenetwork`:

```bash
-v "$HOME/.km:/root/.km" \
-v "$PWD/profiles:/root/.km/profiles" \
-v "$PWD:/work"
```

`~/.km` also carries your per-sandbox SSH keys, so `km vscode` / `km desktop` / `km tunnel`
stop re-keying on every container start. `make operator-shell` wires all of it up, each
mount guarded on its source existing — docker silently creates a *directory* at a missing
bind source, which shadows the thing you meant to mount.

## 🐛 Three fixes that apply to native installs too

**`km init` could delete the Lambda zips it then skipped.** The "always rebuild" removal ran
*before* the source-existence check, so any install without the Go source next to it lost
`build/*.zip` on the first run and failed on every run after. The caller only warned, so
nothing surfaced it.

**`km init --dry-run` printed module paths that don't exist** — `infra/live/us-east-1/…`
where the apply uses `infra/live/use1/…`, contradicting its own banner one line above.

**`profile_search_paths` was broken at both ends.** The shipped default's `~/.km/profiles`
never resolved (nothing expanded the `~`, so it looked for a literal `~` directory), *and*
the key was missing from the config merge-list, so setting it in `km-config.yaml` was
silently ignored. Both fixed — if you have been wondering why a profile in `~/.km/profiles`
was never found, that is why.

Also: `km init` no longer downloads ~90MB of terraform on runs where nothing needs it.

### Deploy surface — lighter than v0.8.8

Nothing here touches a sandbox, a Lambda, or any AWS resource. The operator binary is a
plain `make build`; the container image is rebuilt by CI when this release is published.

See `containers/operator/README.md`.
