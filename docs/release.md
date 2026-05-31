# Release Runbook

Tag-driven releases via [goreleaser](https://goreleaser.com) + GitHub Actions. Cutting a release means: pick a version, tag it, push the tag — the rest is automated until the Draft-release publish step.

## Versioning model

| What | Where | Who bumps it |
|---|---|---|
| Dev build counter | `VERSION` file | Auto-bumped by every `make build` (see `scripts/bump-version.sh`) — currently ~v0.3.7xx |
| Release identity | git tag `vX.Y.Z` | You, when cutting a release |
| Plugin version | `skills/plugin.json`, `skills/marketplace.json` | You, in lockstep with km releases when skill content changes |

The `VERSION` file is intentionally a build counter — it ticks every time you `make build`, so its value is meaningless as a release marker. Release tags are the source of truth.

**Version scheme:** semver. `vX.Y.Z` for stable, `vX.Y.Z-rc1` / `-beta1` for prereleases (goreleaser auto-marks anything with a hyphen as prerelease).

## Artifacts

Each release produces:

- `km_vX.Y.Z_darwin_amd64.tar.gz`
- `km_vX.Y.Z_darwin_arm64.tar.gz`
- `km_vX.Y.Z_linux_amd64.tar.gz`
- `km_vX.Y.Z_linux_arm64.tar.gz`
- `km_vX.Y.Z_checksums.txt` (SHA256)

Each tarball contains:

```
km                              # the CLI binary, host-built for this platform
bin/terraform                   # v1.9.8, bundled
bin/terragrunt                  # v0.99.1, bundled
LICENSE
README.md
OPERATOR-GUIDE.md
THIRD-PARTY-LICENSES.txt        # terraform BUSL + terragrunt MIT attribution
```

Operators still need to install themselves: `aws` CLI, `session-manager-plugin`, and `docker` (Docker-substrate sandboxes only).

## Cut-a-release procedure

### 1. Pre-flight

- `main` branch is green; no in-flight Phase work uncommitted.
- `.planning/STATE.md` reflects a clean milestone checkpoint (the operative milestone is `v1.0` — see `.planning/v1.0-MILESTONE-AUDIT.md`).
- Recent commits since the last tag use [conventional commit](https://www.conventionalcommits.org/) prefixes (`feat:`, `fix:`, `docs:`) so goreleaser's changelog grouping works.
- Decide the version. Rules of thumb:
  - First time we ship publicly: `v1.0.0` (when the v1.0 milestone fully completes) or `v1.0.0-rc1` if it's an RC.
  - Subsequent: bump per semver — breaking CLI/profile change → major, new feature → minor, bugfix → patch.

### 2. Local sanity check (no tag required)

```bash
brew install goreleaser/tap/goreleaser
goreleaser check
goreleaser release --snapshot --clean
```

This runs the full pipeline against `HEAD` without tagging. Inspect:

```bash
ls dist/
tar -tzf dist/km_v*_darwin_arm64.tar.gz
sha256sum dist/km_v*_checksums.txt
```

You should see four tarballs + the checksums file, and `tar -tzf` should list `km`, `bin/terraform`, `bin/terragrunt`, plus the doc files.

### 3. Tag and push

```bash
git tag v1.0.0                  # or v1.0.0-rc1
git push origin v1.0.0
```

The tag push triggers `.github/workflows/release.yml`.

### 4. Watch the workflow

```bash
gh run watch                    # or open GH Actions tab
```

The job:
- Checks out at the tag with full history (`fetch-depth: 0`) so the changelog can compute.
- Sets up Go from `go.mod` `go-version`.
- Restores the `~/.cache/km-bundle/` cache keyed on `(tf_version, tg_version)`. First run for a new toolchain pin: full ~600MB download. Subsequent runs: instant.
- Runs `goreleaser release --clean`.
- Posts the result as a **Draft** GitHub release.

### 5. Publish

Open the Draft release in the GH UI. Verify:

- Title is `km vX.Y.Z`
- Body has the install snippet + changelog grouped by Features / Bug fixes / Documentation
- All 5 assets are attached (4 tarballs + checksums)
- Prerelease checkbox matches your intent

Click **Publish release**.

### 6. Post-release

- If any `skills/` content changed in this release, bump `skills/plugin.json` `version` AND `skills/marketplace.json` plugin entry, then commit. Clients cache the old plugin version otherwise. See memory `project_plugin_version_gates_cache`.
- Update `CHANGELOG.md` if you maintain one (not currently committed).
- Announce in the team Slack `#km-notifications` (or wherever).

## Bumping bundled tool versions

To pin a new `terraform` or `terragrunt`:

1. Edit `.goreleaser.yaml`:
   ```yaml
   before:
     hooks:
       - bash scripts/fetch-bundled-tools.sh 1.10.0 0.100.0   # was 1.9.8 / 0.99.1
   ```
2. Edit `.github/workflows/release.yml` cache key:
   ```yaml
   key: km-bundle-tf-1.10.0-tg-0.100.0
   ```
3. (Optional, for symmetry) Edit `internal/app/cmd/init.go` `tfDesiredVersion` and the terragrunt download line if the *Lambda-side* toolchain should match. Note this is independent — the bundled-archive toolchain and the Lambda-side toolchain are two different consumers.
4. Run `goreleaser release --snapshot --clean` locally to verify the new versions fetch + zip correctly.

## Troubleshooting

### `goreleaser check` fails with template error

Most templates use `{{ .Os }}` / `{{ .Arch }}` / `{{ .Version }}` / `{{ .ShortCommit }}`. If you added a new field, run `goreleaser check` and check the [template variable reference](https://goreleaser.com/customization/templates/).

### Per-platform `files:` not bundling the right binary

The `dist/extras/{{ .Os }}_{{ .Arch }}/` path is evaluated **per archive** by goreleaser. If a tarball is missing its bundled tools, the script likely failed silently — run `bash scripts/fetch-bundled-tools.sh 1.9.8 0.99.1` standalone and inspect `dist/extras/`.

### Workflow fails on tag push: `Error: 403 Resource not accessible by integration`

The workflow needs `contents: write` permission. Verify the `permissions:` block in `.github/workflows/release.yml` is intact. If your org has restricted default `GITHUB_TOKEN` permissions org-wide, add a PAT as a secret and reference it.

### Cache miss on every run (slow)

Check the cache key in `.github/workflows/release.yml` — it must contain the same tool versions as the `before.hooks` line in `.goreleaser.yaml`. Mismatch → eternal cache miss.

### License concern about bundling terraform

`terraform` is BUSL-1.1 (since Aug 2023). Bundling the binary in an operational tool that orchestrates terraform is widely practiced (Atlantis, Spacelift, Terragrunt itself). The `THIRD-PARTY-LICENSES.txt` attribution is the minimum hygiene. If legal asks for distance: swap to [OpenTofu](https://opentofu.org/) (BSL-free, drop-in compatible) by changing the fetch URL in `scripts/fetch-bundled-tools.sh` to:

```
https://github.com/opentofu/opentofu/releases/download/v1.8.X/tofu_1.8.X_${os}_${arch}.tar.gz
```

…and renaming `terraform` → `tofu` in the archive `files:` list.

### Need to delete and re-cut a tag

```bash
git tag -d vX.Y.Z
git push --delete origin vX.Y.Z
# delete the Draft release in the GH UI
# fix whatever, re-tag, push
```

Only safe while the release is still a Draft. Once published, cut a new patch version instead — never republish over an existing tag.

## Files

- `.goreleaser.yaml` — release pipeline config
- `scripts/fetch-bundled-tools.sh` — per-platform tool downloader (cache: `~/.cache/km-bundle/`)
- `.github/workflows/release.yml` — tag-triggered CI workflow
- `CLAUDE.md` § Releases — concise summary for Claude

## Future enhancements (not in current scope)

- Homebrew tap publishing (`brews:` block in `.goreleaser.yaml`, requires PAT)
- SBOM generation (`sboms:` block, syft-based)
- Docker image publishing (`dockers:` block)
- Lambda zip publishing alongside the operator bundle (separate artifact channel — these are operator-uploaded today, not end-user-consumed)
- Code signing / notarization for macOS (`signs:` block, requires Apple Developer cert)
