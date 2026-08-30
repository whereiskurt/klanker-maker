# km operator client image

A container with a published `km` release plus the tools it shells out to but
does not bundle: aws CLI v2, `session-manager-plugin`, `sops`, git/jq/ssh.
Drops you into a shell at `/klanker-maker` with `km`, `km-config.yaml`, and
`profiles/` in place.

## Quick start — pull and run (no build)

Published to GHCR on every release, multi-arch (amd64 + arm64), so `docker pull`
picks the right one automatically:

```bash
docker run --rm -it \
  -v "$HOME/.aws:/root/.aws" \
  -v "$PWD/km-config.yaml:/klanker-maker/km-config.yaml" \
  ghcr.io/whereiskurt/klanker-maker/km:latest
```

That lands you in a shell at `/klanker-maker`, where `./km ...` works directly.
Pin a release with `:v0.7.0` instead of `:latest`.

### Two ways to run it

**Interactive** — the default. `-it` with no command gives you a shell in the
work directory:

```bash
docker run --rm -it -v "$HOME/.aws:/root/.aws" ghcr.io/.../km
[sandbox]$ ./km create profiles/spot.yaml --remote
```

**One-shot** — append the command; it runs and exits with km's own exit code:

```bash
docker run --rm -v "$HOME/.aws:/root/.aws" ghcr.io/.../km ./km list
docker run --rm -v "$HOME/.aws:/root/.aws" ghcr.io/.../km km validate profiles/spot.yaml
```

`./km` and a bare `km` both work — `/klanker-maker/km` is a symlink onto PATH.
The startup banner is suppressed when output is not a TTY, so one-shot output
pipes cleanly into `jq` and friends.

## Quick start — from a release tarball (no git clone)

Every published release archive carries this Dockerfile, the profile library,
and `km-config.example.yaml` at the same paths they have in the repo, so the
build works straight out of the extracted tarball:

```bash
mkdir -p km-v0.7.0
tar -xJf km_v0.7.0_linux_amd64.tar.xz -C km-v0.7.0
cd km-v0.7.0

docker build -f containers/operator/Dockerfile \
  --build-arg KM_VERSION=v0.7.0 -t km .

docker run --rm -it -v "$HOME/.aws:/root/.aws" km:latest
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

docker build -f containers/operator/Dockerfile -t km .

docker run --rm -it \
  -v "$HOME/.aws:/root/.aws" \
  -v "$PWD/km-config.yaml:/klanker-maker/km-config.yaml" \
  km
```

`make operator-image` and `make operator-shell` are aliases for exactly those
two commands. They are convenience only.

The build step is not optional: no image is published to a registry, so every
machine builds its own. The build itself needs no AWS access — it only downloads
the public GitHub release.

## The mounts

Only `~/.aws` is required. The rest each buy one specific thing.

| Host | Container | What it buys |
|---|---|---|
| `~/.aws` | `/root/.aws` | credentials — never baked in |
| `km-config.yaml` | `/klanker-maker/km-config.yaml` | your account IDs and prefix |
| `~/.km` | `/root/.km` | per-sandbox SSH keys, so `km vscode`/`desktop`/`tunnel` don't rekey every run |
| `./profiles` | `/root/.km/profiles` | your profiles, **layered over** the baked library |
| `$PWD` | `/work` | somewhere for output to outlive the container |

```bash
docker run --rm -it \
  -v "$HOME/.aws:/root/.aws" \
  -v "$HOME/.km:/root/.km" \
  -v "$PWD/profiles:/root/.km/profiles" \
  -v "$PWD:/work" \
  -v "$PWD/km-config.yaml:/klanker-maker/km-config.yaml" \
  ghcr.io/whereiskurt/klanker-maker/km:latest
```

**Mount a path only when it exists on the host.** Docker silently creates a
*directory* at a missing bind-mount source — which shadows `km-config.yaml`
with something km cannot read, and shadows the profile library with an empty
directory. `make operator-shell` guards each mount; a hand-written `docker run`
does not. The startup banner reports the state of every mount, so a missing one
is visible before a command fails.

**`~/.aws`** — mounting read-write shares the SSO token cache with the host, so
if you are logged in on the host you are logged in in the container. If not, run
`aws sso login` inside it. `-e AWS_PROFILE=<profile>` picks a profile.

**`~/.km`** — holds the per-sandbox ed25519 keypairs at `~/.km/keys/<id>`.
Without this mount every container start mints a fresh keypair that dies with
the container, leaving the sandbox holding an `authorized_keys` entry for a key
nobody has. Recover with `km vscode rekey <id>`, or just mount it.

**`km-config.yaml`** — gitignored (it holds your account IDs), so a fresh clone
has none and the image ships only `km-config.example.yaml`. Mounting the file
means host edits take effect with no rebuild. If you have one from
`km configure`, mount it; otherwise run `km configure` inside the container and
copy the result out.

### Profiles layer, they don't replace

Mounting at `/root/.km/profiles` **adds** to the baked library rather than
replacing it. A leaf of yours resolves, and an `extends: base/safenetwork`
inside it still finds the shipped fragment — which is what makes a two-line
profile of your own usable at all, since almost every leaf extends something
under `profiles/base/`.

The image sets this up with

```
KM_PROFILE_SEARCH_PATHS="/root/.km/profiles /klanker-maker/profiles /work"
```

Two things about that variable are worth knowing before you override it:

- **It is whitespace-separated, not comma-separated.** viper binds it via
  `AutomaticEnv`, and `GetStringSlice` on a string value splits on whitespace. A
  comma-separated value arrives as one long path that matches nothing, and the
  failure looks like a missing profile rather than a malformed variable.
- **The paths are absolute on purpose.** km's built-in default is
  `["./profiles", "~/.km/profiles"]`, whose first entry is relative to the
  process working directory — so the moment you `cd /work`, `./profiles` stops
  meaning the baked library and every `extends:` breaks.

Search order is first-match, and your overlay is first, so a profile of yours
shadows a built-in of the same name. To go the other way and replace the library
outright, mount over `/klanker-maker/profiles` instead — but then you must carry
your own `base/` fragments.

## Choosing a release

Default is the newest published GitHub release, resolved at build time. The
tarball is verified against the release's `checksums.txt` before extraction.

Pin a specific release for a reproducible image:

```bash
docker build -f containers/operator/Dockerfile \
  --build-arg KM_VERSION=v0.7.0 -t km:v0.7.0 .
# or: make operator-image KM_RELEASE=v0.7.0
```

Architecture follows the build host (`TARGETARCH`); amd64 and arm64 both build.

## Scope

An operator **client for `--remote` sandbox work**, plus **read-only platform
inspection**.

Verified working: `km create --remote`, `km list`, `km status`, `km shell`,
`km agent`, `km logs`, `km otel`, `km destroy --remote`, `km doctor`,
`km capacity`, `km validate`, `km init --dry-run`, and `km init --plan` /
`km bootstrap --plan`.

**Applying is out of scope — a decision (2026-08-29), not a limitation to be
fixed.** Please do not re-open it without reading this first.

The mechanical obstacle is real but tractable: `km init --dry-run=false` shells
out to `go build` at run time — once in `buildAndUploadSidecars` for the
sandbox-side helper binaries, again in `uploadCreateHandlerToolchain` for the
Lambda's bundled `toolchain/km` — and pushes images to ECR. Prebuilt release
artifacts would solve it the same way they solved the Lambda zips, and that
pattern is already proven in this repo: `cmd/create-handler` runs real
`terragrunt apply` for every remote `km create` with no Go toolchain, off
`toolchain/{km,terraform,terragrunt,infra.tar.gz}` in S3.

**It is not a permissions boundary either.** The image holds whatever `~/.aws`
carries, which is the same credential a native apply uses, and plan is not even
read-only at the AWS level — nothing passes `-lock=false`, so it takes and
releases DynamoDB state locks. Apply would succeed on authorization today.

The reason it stays out is semantic. **A native apply deploys your working
tree; a container apply could only ever deploy the release.**
`buildAndUploadSidecars` compiles with `Dir = repoRoot`, so a native
`km init --dry-run=false` ships whatever is on disk, uncommitted edits
included — that is the normal development loop. A container has no source and
can only ship what the release built. Those are two materially different
operations, and giving them one command name means someone eventually runs it
expecting the first and silently gets the second. If it is ever wanted, it
needs its own verb — *deploy this release*, not *deploy what I have*.

The same applies to `--local` create/destroy.

Why the plan tier did not face this problem: plan only needs an artifact to
*exist* so `filebase64sha256` can hash it. A hash that does not match what is
deployed is honest noise in the diff. Apply has no such tolerance — the
artifact it uploads becomes the deployed Lambda.

### What makes the plan tier work

Three things are baked in, and `km init --plan` needs all three:

1. **`terraform` + `terragrunt` on PATH.** The release archive has always
   bundled both; this image used to discard them (~152MB) as unusable, which was
   correct while no `infra/` tree shipped.
2. **The terragrunt tree at `/klanker-maker/infra`** (~1.3MB), plus a stub
   `CLAUDE.md` at `/klanker-maker`. That stub is not decoration: every unit under
   `infra/live` resolves its paths through
   `dirname(find_in_parent_folders("CLAUDE.md"))`, so without a file by that
   name at the root of the tree, terragrunt aborts before reading a single
   module. Only its location matters; the contents are never read.
3. **Prebuilt Lambda zips at `/klanker-maker/build`.** The nine Lambda-owning
   modules read theirs via
   `filebase64sha256("${repo_root}/build/<name>.zip")`, and **terraform
   evaluates that during plan, not apply** — so without the zips, `km init
   --plan` fails outright on the first Lambda module instead of degrading. They
   ship as a separate `km_<tag>_lambdas.tar` release asset (~217MB, linux/arm64)
   built by `scripts/build-release-lambdas.sh`.

`findRepoRoot()` finds no `go.mod` here and falls back to the working directory,
so `/klanker-maker` *is* the repo root as far as km is concerned, and all of the
above land where the `${local.repo_root}/...` references expect them.
`region.hcl` is gitignored and absent from both a clone and the archive;
`ensureRegionHCL` writes it on first run.

**Reading a plan from the image:** Lambda `source_code_hash` diffs are expected
noise whenever the image's release differs from what is currently deployed —
they say "this release's code is not what is live", which is true and rarely
what you are looking at the plan for. The signal is everything else: IAM
statements, environment blocks, tables, queues.

**Run `km init --plan`, not terragrunt by hand.** `eval $(km env)` does not
reproduce the environment km itself applies with — `KM_PLATFORM_KMS_KEY_ARN` is
one it sets programmatically and `km env` does not emit. Several modules gate
resources on `count = var.x != "" ? 1 : 0` reading exactly those variables, so a
hand-run `terragrunt plan` reports phantom destroys ("index [0] is out of range
for count") for resources that are in no danger at all. Observed with
`aws_iam_role_policy.kms_decrypt[0]` in `lambda-quota-alerter`: `km init --plan`
correctly reports 0 destroys for that module; a bare `terragrunt plan` in the
same container reports 1.

Why `--remote` needs no repo: `runCreateRemote` returns before km's local
terragrunt runner is ever constructed, and network config falls back to reading
the S3 tfstate when `infra/live/<region>/network/outputs.json` is absent (the
local cache write it then attempts is non-fatal).

## Files

| Path | Purpose |
|---|---|
| `Dockerfile` | Two-stage build: verified fetch, then runtime |
| `Dockerfile.dockerignore` | Build context allowlist — needed because the repo-root `.dockerignore` excludes `profiles/` and `infra/` |
| `entrypoint.sh` | Startup banner reporting release, mount state, and plan readiness |
| `../../scripts/build-release-lambdas.sh` | Builds the `km_<tag>_lambdas.tar` release asset the image unpacks into `build/` |

The image is published by `.github/workflows/publish-image.yml`, which triggers
on `release: published` — not on tag push. goreleaser cuts a **draft** release,
and a draft's assets are not publicly downloadable, so building at tag time
would 404 or silently package the previous release.
