# km operator client image.
#
# Packages a published km release (github.com/whereiskurt/klanker-maker/releases)
# together with the tools km shells out to but does not bundle: aws CLI v2,
# session-manager-plugin, sops, git/jq/ssh. Drops you into a shell at
# /klanker-maker with `km`, km-config.yaml, and profiles/ already in place.
#
# AWS credentials are NEVER baked in — mount ~/.aws at runtime:
#   docker build -f containers/operator/Dockerfile -t km-operator .
#   docker run --rm -it \
#     -v "$HOME/.aws:/root/.aws" \
#     -v "$PWD/km-config.yaml:/klanker-maker/km-config.yaml" \
#     km-operator
#
# `make operator-image` / `make operator-shell` are aliases for exactly those
# two commands — convenience only, never required.
#
# Scope: an operator *client* for --remote sandbox work — create, inspect,
# connect, destroy. Platform provisioning (km init, km bootstrap, --local
# create/destroy) is deliberately out of scope: those call findRepoRoot() and
# apply terragrunt against infra/live/..., which no release archive contains.
# Run those from a native install.
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
    test -x /fetch/km/km

# aws CLI v2 — required for SSO login, STS, and the AWS calls km shells out to.
# Needs glibc, which is why this image is Debian and not Alpine.
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) awsarch="x86_64" ;; \
      arm64) awsarch="aarch64" ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o awscli.zip \
      "https://awscli.amazonaws.com/awscli-exe-linux-${awsarch}-${AWSCLI_VERSION}.zip"; \
    unzip -q awscli.zip

# session-manager-plugin — required by km shell / agent / vscode / desktop,
# all of which tunnel over SSM.
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) ssmdir="ubuntu_64bit" ;; \
      arm64) ssmdir="ubuntu_arm64" ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL -o session-manager-plugin.deb \
      "https://s3.amazonaws.com/session-manager-downloads/plugin/latest/${ssmdir}/session-manager-plugin.deb"

# sops — for editing SOPS-encrypted secret files (docs/sandbox-secrets.md).
RUN set -eux; \
    curl -fsSL -o sops \
      "https://github.com/getsops/sops/releases/download/${SOPS_VERSION}/sops-${SOPS_VERSION}.linux.${TARGETARCH}"; \
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

# km itself. NOTE the release tarball's bin/terraform and bin/terragrunt are
# deliberately NOT installed (~152MB saved): this image is scoped to --remote
# creates, and every terragrunt call site in km operates on a directory under
# repoRoot/infra/live/..., a tree no release archive contains. Shipping them
# would add weight that cannot be used. Platform provisioning (km init,
# km bootstrap, --local create/destroy) belongs on a native install.
COPY --from=fetch /fetch/km/km /usr/local/bin/km
COPY --from=fetch /fetch/sops  /usr/local/bin/sops

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
RUN mkdir -p /root/.aws /root/.km

COPY containers/operator/entrypoint.sh /usr/local/bin/km-entrypoint
RUN chmod +x /usr/local/bin/km-entrypoint

ENTRYPOINT ["/usr/local/bin/km-entrypoint"]
CMD ["bash"]
