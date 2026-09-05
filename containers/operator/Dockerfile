# km operator client image.
#
# Packages a published km release (github.com/whereiskurt/klanker-maker/releases)
# together with the tools km shells out to but does not bundle: aws CLI v2,
# session-manager-plugin, sops, git/jq/ssh. Drops you into a shell at
# /klanker-maker with `km`, km-config.yaml, and profiles/ already in place.
#
# AWS credentials are NEVER baked in — mount ~/.aws at runtime:
#   docker build -f containers/operator/Dockerfile -t km .
#   docker run --rm -it \
#     -v "$HOME/.aws:/root/.aws" \
#     -v "$HOME/.km:/root/.km" \
#     -v "$PWD/profiles:/root/.km/profiles" \
#     -v "$PWD:/work" \
#     -v "$PWD/km-config.yaml:/klanker-maker/km-config.yaml" \
#     km
#
# Only ~/.aws is required. ~/.km carries the per-sandbox ed25519 keypairs that
# km vscode / km desktop / km tunnel reuse (without it every container start
# rekeys); $PWD/profiles layers your profiles over the baked library; $PWD as
# /work gives output somewhere that outlives the container.
#
# `make operator-image` / `make operator-shell` are aliases for exactly those
# two commands — convenience only, never required. operator-shell guards each
# mount's source for existence, because docker silently creates a DIRECTORY at
# a missing bind source.
#
# Scope: an operator client for --remote sandbox work — create, inspect,
# connect, destroy — plus READ-ONLY platform inspection: `km init --dry-run`
# and `km init --plan` / `km bootstrap --plan`, which run real terragrunt plans
# against a real AWS account and never write.
#
# Applying is still out of scope, and not by policy — `km init --dry-run=false`
# shells out to `go build` at runtime (buildAndUploadSidecars,
# uploadCreateHandlerToolchain) and pushes container images to ECR. Supporting
# it would mean a Go toolchain, the full source tree, and a mounted docker
# socket, at which point this stops being a client and becomes a dev
# environment. Apply from a native install.
#
# Three things make the plan tier work, and all three are baked below: the
# terragrunt tree at /klanker-maker/infra, terraform + terragrunt on PATH, and
# prebuilt Lambda zips at /klanker-maker/build (the Lambda-owning modules call
# filebase64sha256() on those, which terraform evaluates at PLAN time).
#
# The --remote create path never touches local terragrunt: runCreateRemote
# returns before the local runner, and network outputs fall back to reading the
# S3 tfstate when infra/live/<region>/network/outputs.json is absent.

# ---------------------------------------------------------------------------
# Stage 1 — fetch and verify every external artifact
# ---------------------------------------------------------------------------
FROM debian:bookworm-slim AS fetch

# latest = resolve the newest published release at build time.
# Pin a specific tag for a reproducible image: --build-arg KM_VERSION=v0.7.0
ARG KM_VERSION=latest
ARG SOPS_VERSION=v3.9.4
ARG AWSCLI_VERSION=2.24.0
# Supplied by buildx: amd64 | arm64
ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl xz-utils unzip \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /fetch

# km release: resolve the tag, verify the tarball against the published
# checksums file, then extract. sha256sum -c is the gate — a truncated or
# tampered download fails the build instead of producing a broken image.
RUN set -eux; \
    repo="whereiskurt/klanker-maker"; \
    if [ "${KM_VERSION}" = "latest" ]; then \
      tag="$(curl -fsSL "https://api.github.com/repos/${repo}/releases/latest" \
             | grep -m1 '"tag_name"' | cut -d'"' -f4)"; \
    else \
      tag="${KM_VERSION}"; \
    fi; \
    [ -n "${tag}" ] || { echo "could not resolve km release tag" >&2; exit 1; }; \
    echo "km release: ${tag} (linux/${TARGETARCH})"; \
    base="https://github.com/${repo}/releases/download/${tag}"; \
    tarball="km_${tag}_linux_${TARGETARCH}.tar.xz"; \
    curl -fsSL -o "${tarball}"                 "${base}/${tarball}"; \
    curl -fsSL -o "km_${tag}_checksums.txt"    "${base}/km_${tag}_checksums.txt"; \
    sha256sum -c --ignore-missing "km_${tag}_checksums.txt"; \
    mkdir -p /fetch/km; \
    tar -xJf "${tarball}" -C /fetch/km; \
    echo "${tag}" > /fetch/km/.km-release-tag; \
    test -x /fetch/km/km; \
    test -x /fetch/km/bin/terraform; \
    test -x /fetch/km/bin/terragrunt

# Lambda deployment zips (linux/arm64). The nine Lambda-owning terragrunt
# modules read theirs via filebase64sha256("${repo_root}/build/<name>.zip"), and
# terraform evaluates that during PLAN — so without these, `km init --plan`
# fails on the first Lambda module rather than degrading.
#
# Two sources, because neither alone covers both callers:
#
#   release  the km_<tag>_lambdas.tar asset built by
#            scripts/build-release-lambdas.sh. The only source available from an
#            extracted tarball, and the only one whose zips provably match the
#            tag being installed.
#   local    build/*.zip from the build context, i.e. what `make build-lambdas`
#            just produced. Needed so a working clone can build a plan-capable
#            image without cutting a release first — and so this Dockerfile does
#            not break every `make operator-image` in the window between a
#            change landing and the next tag.
#
# `auto` (the default) prefers local zips when the context has them and falls
# back to the release. It always announces which it took: a local build/ left
# over from an unrelated experiment silently overriding the pinned release is
# exactly the kind of thing that produces a plan nobody can explain.
#
# Both path components are bracket-globbed, and BOTH are load-bearing.
#
# `*.zi[p]` alone is not enough — that is the km-config.yam[l] trick below, and
# it only tolerates a missing FILE. The parent directory is still lstat'd, so on
# a clean checkout (build/ is gitignored and therefore absent entirely, not
# merely empty) the build dies with:
#
#     ERROR: failed to solve: lstat /build: no such file or directory
#
# Globbing the directory too makes buildkit match a pattern instead of stat'ing
# a literal path, so zero matches is an empty result rather than an error.
# Verified against a `git archive HEAD` context, which is what CI actually sees
# — a working clone has build/ lying around and hides this completely.
ARG KM_LAMBDAS=auto
COPY buil[d]/*.zi[p] /fetch/local-lambdas/

RUN set -eux; \
    mkdir -p /fetch/lambdas /fetch/local-lambdas; \
    have_local=0; \
    if ls /fetch/local-lambdas/*.zip >/dev/null 2>&1; then have_local=1; fi; \
    case "${KM_LAMBDAS}" in \
      local) [ "${have_local}" = 1 ] || { echo "KM_LAMBDAS=local but the build context has no build/*.zip — run 'make build-lambdas' first" >&2; exit 1; }; \
             src=local ;; \
      release) src=release ;; \
      auto) if [ "${have_local}" = 1 ]; then src=local; else src=release; fi ;; \
      *) echo "KM_LAMBDAS must be auto, local, or release (got '${KM_LAMBDAS}')" >&2; exit 1 ;; \
    esac; \
    if [ "${src}" = local ]; then \
      echo "lambda zips: local build/ ($(ls -1 /fetch/local-lambdas/*.zip | wc -l) found)"; \
      cp /fetch/local-lambdas/*.zip /fetch/lambdas/; \
    else \
      tag="$(cat /fetch/km/.km-release-tag)"; \
      asset="km_${tag}_lambdas.tar"; \
      echo "lambda zips: release ${tag} (${asset})"; \
      : "sha256sum -c --ignore-missing passes VACUOUSLY for a file with no line"; \
      : "in the checksums file, so verifying presence there is what turns a"; \
      : "release that predates this asset into a legible build failure."; \
      grep -q " ${asset}$" "km_${tag}_checksums.txt" \
        || { echo "release ${tag} publishes no ${asset} — it predates the plan-capable image. Use a newer --build-arg KM_VERSION, or build the zips locally: make build-lambdas && make operator-image" >&2; exit 1; }; \
      curl -fsSL -o "${asset}" "https://github.com/whereiskurt/klanker-maker/releases/download/${tag}/${asset}"; \
      sha256sum -c --ignore-missing "km_${tag}_checksums.txt"; \
      tar -xf "${asset}" -C /fetch/lambdas; \
    fi; \
    ls /fetch/lambdas/*.zip >/dev/null

# aws CLI v2 — required for SSO login, STS, and the AWS calls km shells out to.
# Needs glibc, which is why this image is Debian and not Alpine.
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) awsarch="x86_64"; awssha="4e3c39d9881cb6f893ea93219d971390864b1f7e3756197413a7de38ce059609" ;; \
      arm64) awsarch="aarch64"; awssha="bd5c3d645fa92624c0cb85f0bda0a373e5354faac99f21f244b2813dbdea3c3d" ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o awscli.zip \
      "https://awscli.amazonaws.com/awscli-exe-linux-${awsarch}-${AWSCLI_VERSION}.zip"; \
    echo "${awssha}  awscli.zip" | sha256sum -c -; \
    unzip -q awscli.zip

# session-manager-plugin — required by km shell / agent / vscode / desktop,
# all of which tunnel over SSM.
#
# Pinned to an explicit version, NOT plugin/latest/. AWS publishes no checksum
# file for this artifact, so the digests below are recorded by hand — bumping
# SSM_PLUGIN_VERSION means re-recording both. `latest` cannot be pinned this way
# at all, which is the reason it is no longer used.
ARG SSM_PLUGIN_VERSION=1.2.835.0
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) ssmdir="ubuntu_64bit"; ssmsha="7c6dcad12518571cc7959a713e6a8ae1bdf6ed66fd9bee37dc189e39ca58ae03" ;; \
      arm64) ssmdir="ubuntu_arm64"; ssmsha="0add94c4c8b6ca63f26e44fd655d662b0f6455a268b5b9ebebee0f462214e928" ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o session-manager-plugin.deb \
      "https://s3.amazonaws.com/session-manager-downloads/plugin/${SSM_PLUGIN_VERSION}/${ssmdir}/session-manager-plugin.deb"; \
    echo "${ssmsha}  session-manager-plugin.deb" | sha256sum -c -

# sops — for editing SOPS-encrypted secret files (docs/sandbox-secrets.md).
#
# Verified against the checksums file the release publishes, so bumping
# SOPS_VERSION needs no hash edit here. grep first: `sha256sum -c
# --ignore-missing` passes VACUOUSLY when no line matches, so without it a
# renamed upstream asset would silently skip verification instead of failing.
RUN set -eux; \
    asset="sops-${SOPS_VERSION}.linux.${TARGETARCH}"; \
    curl -fsSL -o "${asset}" \
      "https://github.com/getsops/sops/releases/download/${SOPS_VERSION}/${asset}"; \
    curl -fsSL -o sops-checksums.txt \
      "https://github.com/getsops/sops/releases/download/${SOPS_VERSION}/sops-${SOPS_VERSION}.checksums.txt"; \
    grep -q " ${asset}$" sops-checksums.txt; \
    sha256sum -c --ignore-missing sops-checksums.txt; \
    mv "${asset}" sops; \
    chmod +x sops

# ---------------------------------------------------------------------------
# Stage 2 — runtime
# ---------------------------------------------------------------------------
FROM debian:bookworm-slim

LABEL org.opencontainers.image.title="km operator client" \
      org.opencontainers.image.source="https://github.com/whereiskurt/klanker-maker" \
      org.opencontainers.image.description="km operator client for --remote sandbox work: km + aws CLI + session-manager-plugin + sops"

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      git \
      jq \
      less \
      openssh-client \
      procps \
      unzip \
      vim-tiny \
    && rm -rf /var/lib/apt/lists/*

# aws CLI v2 + session-manager-plugin
COPY --from=fetch /fetch/aws /tmp/aws-installer
COPY --from=fetch /fetch/session-manager-plugin.deb /tmp/session-manager-plugin.deb
RUN set -eux; \
    /tmp/aws-installer/install; \
    dpkg -i /tmp/session-manager-plugin.deb; \
    rm -rf /tmp/aws-installer /tmp/session-manager-plugin.deb; \
    aws --version; \
    session-manager-plugin --version

# km itself, plus the terraform/terragrunt pair the release tarball already
# bundles. They cost ~152MB and were previously dropped as unusable — correct
# while no infra/ tree shipped, since every terragrunt call site in km operates
# on a directory under repoRoot/infra/live/... Now that infra/ is baked in
# below, they are what makes `km init --plan` a real terragrunt plan.
COPY --from=fetch /fetch/km/km             /usr/local/bin/km
COPY --from=fetch /fetch/km/bin/terraform  /usr/local/bin/terraform
COPY --from=fetch /fetch/km/bin/terragrunt /usr/local/bin/terragrunt
COPY --from=fetch /fetch/sops              /usr/local/bin/sops

# Docs and release metadata that shipped in the tarball.
COPY --from=fetch /fetch/km/LICENSE                  /klanker-maker/LICENSE
COPY --from=fetch /fetch/km/README.md                /klanker-maker/README.md
COPY --from=fetch /fetch/km/OPERATOR-GUIDE.md        /klanker-maker/OPERATOR-GUIDE.md
COPY --from=fetch /fetch/km/THIRD-PARTY-LICENSES.txt /klanker-maker/THIRD-PARTY-LICENSES.txt
COPY --from=fetch /fetch/km/.km-release-tag          /klanker-maker/.km-release-tag

# `./km` works from the work directory as well as a bare `km` from PATH.
# A symlink rather than a second COPY — the binary is 85MB.
RUN ln -s /usr/local/bin/km /klanker-maker/km

# Profiles are committed, so they always bake in.
COPY profiles/ /klanker-maker/profiles/

# The terragrunt tree (~1.3MB) and the prebuilt Lambda zips, together the
# working directory `km init --plan` needs. findRepoRoot() finds no go.mod in
# this image and falls back to the working directory, so /klanker-maker IS the
# repo root as far as km is concerned and these land exactly where the
# ${local.repo_root}/... references in infra/live/**/terragrunt.hcl resolve.
#
# region.hcl is gitignored and therefore absent from both a fresh clone and the
# release archive; ensureRegionHCL writes it on first run.
COPY infra/ /klanker-maker/infra/
COPY --from=fetch /fetch/lambdas/ /klanker-maker/build/

# The terragrunt repo-root anchor. Every unit under infra/live resolves its
# paths through `dirname(find_in_parent_folders("CLAUDE.md"))` — root.hcl,
# site.hcl, and each terragrunt.hcl — so without a CLAUDE.md at the root of the
# tree, terragrunt aborts before it reads a single module.
#
# A stub, NOT the repo's own CLAUDE.md: terragrunt cares only that the file
# exists (it takes the dirname and discards the contents), while the real one is
# ~90KB of internal phase history that has no business in a published image.
RUN printf '%s\n' \
      '# Terragrunt repo-root anchor.' \
      '#' \
      '# infra/live/**/terragrunt.hcl resolves paths via' \
      '# dirname(find_in_parent_folders("CLAUDE.md")), so this file marks the root' \
      '# of the tree. Its contents are never read.' \
      '#' \
      '# This is the km operator container image, not a source checkout. See' \
      '# containers/operator/README.md for what the image can and cannot do.' \
      > /klanker-maker/CLAUDE.md

# km-config.yaml is gitignored (per-operator, holds account IDs), so a fresh
# clone does not have one. The bracket glob makes the COPY optional: it matches
# zero files and succeeds when the file is absent, instead of failing the build
# for everyone but its author. The example always ships as a starting point.
COPY docs/km-config.example.yaml /klanker-maker/km-config.example.yaml
COPY km-config.yam[l]            /klanker-maker/

# Either way, a runtime bind-mount overrides the baked copy, so host edits take
# effect without a rebuild. km's config loader searches the working directory.

WORKDIR /klanker-maker
ENV HOME=/root

# Profile overlay. Mount your own profiles at /root/.km/profiles and they LAYER
# over the baked library rather than replacing it: a leaf of yours resolves, and
# an `extends: base/safenetwork` inside it still finds the shipped fragment.
#
# Set explicitly rather than relying on the built-in default
# ["./profiles", "~/.km/profiles"] because the first entry is relative to the
# process working directory — so the moment you `cd /work`, "./profiles" stops
# meaning the baked library and fragment resolution breaks. Absolute paths are
# cwd-proof.
#
# WHITESPACE-separated, not comma. viper's AutomaticEnv binds this var and
# GetStringSlice splits a string value on whitespace; a comma-separated value
# arrives as one long path that matches nothing. Pinned by
# TestProfileSearchPaths_EnvWhitespaceSeparated.
#
# /work is listed so a profile in a mounted working directory can be extended
# by name too; it is the last entry, so the shipped library wins a name tie.
ENV KM_PROFILE_SEARCH_PATHS="/root/.km/profiles /klanker-maker/profiles /work"

# /work is the mount point for the host working directory: output that must
# outlive the container (km rsync, artifacts, a generated learned.*.yaml)
# belongs there rather than in the image's own filesystem.
RUN mkdir -p /root/.aws /root/.km/profiles /work

COPY containers/operator/entrypoint.sh /usr/local/bin/km-entrypoint
RUN chmod +x /usr/local/bin/km-entrypoint

ENTRYPOINT ["/usr/local/bin/km-entrypoint"]
CMD ["bash"]
