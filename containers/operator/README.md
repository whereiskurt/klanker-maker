# km operator client image

A container with a published `km` release plus the tools it shells out to but
does not bundle: aws CLI v2, `session-manager-plugin`, `sops`, git/jq/ssh.
Drops you into a shell at `/klanker-maker` with `km`, `km-config.yaml`, and
`profiles/` in place.

## Quick start — from a release tarball (no git clone)

Every published release archive carries this Dockerfile, the profile library,
and `km-config.example.yaml` at the same paths they have in the repo, so the
build works straight out of the extracted tarball:

```bash
mkdir -p km-v0.7.0
tar -xJf km_v0.7.0_linux_amd64.tar.xz -C km-v0.7.0
cd km-v0.7.0

docker build -f containers/operator/Dockerfile \
  --build-arg KM_VERSION=v0.7.0 -t km-operator .

docker run --rm -it -v "$HOME/.aws:/root/.aws" km-operator
```

Pass `--build-arg KM_VERSION=v0.7.0` so the image matches the archive you
extracted; omit it to track the newest release instead. The build downloads km
from the GitHub release rather than using the binary sitting next to it — the
darwin archives carry a darwin binary, which cannot go into a linux image.

## Quick start — from a git clone

No `make` required — two plain docker commands:

```bash
git clone https://github.com/whereiskurt/klanker-maker.git
cd klanker-maker

docker build -f containers/operator/Dockerfile -t km-operator .

docker run --rm -it \
  -v "$HOME/.aws:/root/.aws" \
  -v "$PWD/km-config.yaml:/klanker-maker/km-config.yaml" \
  km-operator
```

`make operator-image` and `make operator-shell` are aliases for exactly those
two commands. They are convenience only.

The build step is not optional: no image is published to a registry, so every
machine builds its own. The build itself needs no AWS access — it only downloads
the public GitHub release.

## The two mounts

**`~/.aws`** — credentials are never baked into the image. Mounting read-write
means the SSO token cache is shared with the host, so if you are already logged
in on the host, you are logged in in the container. If not, run `aws sso login`
inside it. Set `-e AWS_PROFILE=<profile>` to pick a profile.

**`km-config.yaml`** — optional, but you almost certainly want it.
`km-config.yaml` is gitignored (it holds your account IDs), so a fresh clone
has none and the image ships only `km-config.example.yaml`. Mounting the file
also means host edits take effect immediately, with no rebuild. If you already
have one from `km configure`, mount it; otherwise run `km configure` inside the
container and copy the result out.

Mount it only when the host file exists: docker silently creates a *directory*
at a missing bind-mount source, which shadows the baked file with something km
cannot read. `make operator-shell` guards against this; a hand-written
`docker run` does not.

The startup banner reports the state of both mounts, so a missing one is
visible before a command fails.

## Choosing a release

Default is the newest published GitHub release, resolved at build time. The
tarball is verified against the release's `checksums.txt` before extraction.

Pin a specific release for a reproducible image:

```bash
docker build -f containers/operator/Dockerfile \
  --build-arg KM_VERSION=v0.7.0 -t km-operator:v0.7.0 .
# or: make operator-image KM_RELEASE=v0.7.0
```

Architecture follows the build host (`TARGETARCH`); amd64 and arm64 both build.

## Scope

An operator **client for `--remote` sandbox work**. Verified working:
`km create --remote`, `km list`, `km status`, `km shell`, `km agent`, `km logs`,
`km otel`, `km destroy --remote`, `km doctor`, `km capacity`, `km validate`.

Platform provisioning is deliberately **out of scope**: `km init`,
`km bootstrap`, and any `--local` create/destroy call `findRepoRoot()` and apply
terragrunt against an `infra/live/...` tree that no release archive contains.
Run those from a native install of km, where you have the repo.

That scope is why `terraform` and `terragrunt` are **not** installed in the
image even though the release archive bundles them — they could not act on
anything here, and they cost 152MB. The native install path in the release notes
still installs both.

Why `--remote` needs no repo: `runCreateRemote` returns before km's local
terragrunt runner is ever constructed, and network config falls back to reading
the S3 tfstate when `infra/live/<region>/network/outputs.json` is absent (the
local cache write it then attempts is non-fatal).

## Files

| Path | Purpose |
|---|---|
| `Dockerfile` | Two-stage build: verified fetch, then runtime |
| `Dockerfile.dockerignore` | Build context allowlist — needed because the repo-root `.dockerignore` excludes `profiles/` |
| `entrypoint.sh` | Startup banner reporting release + mount state |
