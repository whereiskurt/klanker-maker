#!/usr/bin/env bash
# Build every Lambda deployment zip and package them as a single release asset.
#
# Invoked by goreleaser `before.hooks`; the resulting tarball is attached to the
# GitHub release as km_v<version>_lambdas.tar.xz and consumed by
# containers/operator/Dockerfile.
#
# WHY this exists: the nine Lambda-owning terragrunt modules (create-handler,
# ttl-handler, the four bridges, quota-alerter, email-create-handler,
# budget-enforcer) read their zip via
#
#     source_code_hash = filebase64sha256("${repo_root}/build/<name>.zip")
#
# and terraform evaluates that at PLAN time, not apply time. So `km init --plan`
# needs real zips on disk. Building them requires a Go toolchain and the full
# source tree, neither of which belongs in an operator client image — hence
# building them here, once, at release time, from the tagged commit.
#
# Deliberately does NOT depend on the `build-lambdas` make target: that target
# runs `clean` and `bump-version`, and mutating VERSION mid-release dirties the
# tree goreleaser is building from.
#
# The Lambda list below is pinned to internal/app/cmd/init.go's lambdaBuilds()
# by TestReleaseLambdaScript_TracksLambdaBuilds. Adding a Lambda there without
# adding it here fails that test rather than silently shipping an asset that is
# missing a zip — the same class of footgun as km init skipping an unlisted zip.
#
# Usage: build-release-lambdas.sh <terraform_version> [output_tarball]
set -euo pipefail

TF_VER="${1:?terraform version required}"
# Deliberately uncompressed, unlike the platform archives. The payload is ten
# already-deflated zips, so xz cannot shrink them — measured, it produced a
# 225MB file from 217MB of input — while costing minutes of CI time.
OUT_TARBALL="${2:-.extras/lambdas.tar}"

# Keep in lockstep with lambdaBuilds() in internal/app/cmd/init.go.
LAMBDAS=(
  ttl-handler
  budget-enforcer
  github-token-refresher
  email-create-handler
  create-handler
  km-slack-bridge
  km-github-bridge
  km-h1-bridge
  km-webhook-bridge
  km-quota-alerter
)

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="$REPO_ROOT/build"
CACHE_DIR="${KM_BUNDLE_CACHE:-$HOME/.cache/km-bundle}"

mkdir -p "$BUILD_DIR" "$CACHE_DIR" "$(dirname "$OUT_TARBALL")"

GIT_COMMIT="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
KM_VERSION="$(git -C "$REPO_ROOT" describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")"
LDFLAGS="-X github.com/whereiskurt/klanker-maker/pkg/version.Number=${KM_VERSION}"
LDFLAGS="${LDFLAGS} -X github.com/whereiskurt/klanker-maker/pkg/version.GitCommit=${GIT_COMMIT}"

# ---------------------------------------------------------------------------
# terraform for the ttl-handler zip.
#
# The ttl-handler shells out to terraform to destroy an expired sandbox, so the
# binary and the ec2spot module travel INSIDE its zip. This mirrors
# buildLambdaZips() — omitting it would produce a zip that plans cleanly and
# then degrades the handler to SDK-only teardown once applied.
# ---------------------------------------------------------------------------
tf_zip="$CACHE_DIR/terraform_${TF_VER}_linux_arm64.zip"
if [[ ! -f "$tf_zip" ]]; then
  echo "  → fetching terraform ${TF_VER} (linux/arm64)"
  curl --fail --silent --show-error --location --output "$tf_zip" \
    "https://releases.hashicorp.com/terraform/${TF_VER}/terraform_${TF_VER}_linux_arm64.zip"
fi
unzip -o -q "$tf_zip" terraform -d "$BUILD_DIR"
chmod 0755 "$BUILD_DIR/terraform"
printf '%s\n' "$TF_VER" > "$BUILD_DIR/terraform.version"

for name in "${LAMBDAS[@]}"; do
  src="$REPO_ROOT/cmd/$name"
  if [[ ! -d "$src" ]]; then
    echo "ERROR: $name listed here but cmd/$name does not exist" >&2
    exit 1
  fi

  echo "  → building $name (linux/arm64)"
  rm -f "$BUILD_DIR/$name.zip"
  # AWS's provided.al2023 runtime requires the handler binary to be named
  # `bootstrap`, so every zip carries one and they are built one at a time.
  ( cd "$REPO_ROOT" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
      go build -ldflags "$LDFLAGS" -o "$BUILD_DIR/bootstrap" "./cmd/$name/" )

  if [[ "$name" == "ttl-handler" ]]; then
    ( cd "$BUILD_DIR" && zip -q -j "$name.zip" bootstrap terraform )
    # The ec2spot module travels with its repo-relative directory structure —
    # terraform resolves it by path once the zip is unpacked in /var/task.
    modsrc="$REPO_ROOT/infra/modules/ec2spot/v1.0.0"
    if [[ -d "$modsrc" ]]; then
      staging="$BUILD_DIR/lambda-modules/infra/modules/ec2spot/v1.0.0"
      mkdir -p "$staging"
      cp "$modsrc"/*.tf "$staging/"
      ( cd "$BUILD_DIR/lambda-modules" && zip -q -r "$BUILD_DIR/$name.zip" infra/ )
      rm -rf "$BUILD_DIR/lambda-modules"
    fi
  else
    ( cd "$BUILD_DIR" && zip -q -j "$name.zip" bootstrap )
  fi
  rm -f "$BUILD_DIR/bootstrap"
done

rm -f "$BUILD_DIR/terraform" "$BUILD_DIR/terraform.version"

# Flat archive of just the zips: the Dockerfile extracts it straight into
# /klanker-maker/build/, which is where terragrunt's ${local.repo_root}/build
# resolves to inside the image.
rm -f "$OUT_TARBALL"
tar -cf "$OUT_TARBALL" -C "$BUILD_DIR" $(printf '%s.zip ' "${LAMBDAS[@]}")

echo "  ✓ $(basename "$OUT_TARBALL") ($(du -h "$OUT_TARBALL" | cut -f1))"
