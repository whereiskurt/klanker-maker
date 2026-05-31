#!/usr/bin/env bash
# Fetch terraform + terragrunt for all goreleaser target platforms.
# Invoked by goreleaser `before.hooks`. Idempotent / cache-aware.
#
# Usage: fetch-bundled-tools.sh <terraform_ver> <terragrunt_ver>
set -euo pipefail

TF_VER="${1:?terraform version required}"
TG_VER="${2:?terragrunt version required}"

EXTRAS_DIR="dist/extras"
CACHE_DIR="${KM_BUNDLE_CACHE:-$HOME/.cache/km-bundle}"

mkdir -p "$EXTRAS_DIR" "$CACHE_DIR"

# Platform matrix matches .goreleaser.yaml builds.{goos,goarch}.
PLATFORMS=(
  "darwin amd64"
  "darwin arm64"
  "linux  amd64"
  "linux  arm64"
)

fetch() {
  local url="$1" dest="$2"
  if [[ -f "$dest" ]]; then
    return 0
  fi
  echo "  → fetching $(basename "$dest")"
  curl --fail --silent --show-error --location --output "$dest" "$url"
}

for entry in "${PLATFORMS[@]}"; do
  read -r os arch <<<"$entry"
  out="$EXTRAS_DIR/${os}_${arch}"
  mkdir -p "$out"

  tf_zip="$CACHE_DIR/terraform_${TF_VER}_${os}_${arch}.zip"
  fetch "https://releases.hashicorp.com/terraform/${TF_VER}/terraform_${TF_VER}_${os}_${arch}.zip" "$tf_zip"
  unzip -o -q "$tf_zip" terraform -d "$out"
  chmod 0755 "$out/terraform"

  fetch "https://github.com/gruntwork-io/terragrunt/releases/download/v${TG_VER}/terragrunt_${os}_${arch}" "$out/terragrunt"
  chmod 0755 "$out/terragrunt"
done

cat > "$EXTRAS_DIR/THIRD-PARTY-LICENSES.txt" <<EOF
This archive bundles the following third-party binaries:

terraform v${TF_VER}
  © HashiCorp, Inc. Licensed under the Business Source License 1.1.
  Source: https://github.com/hashicorp/terraform
  License: https://github.com/hashicorp/terraform/blob/v${TF_VER}/LICENSE

terragrunt v${TG_VER}
  © Gruntwork, Inc. Licensed under the MIT License.
  Source: https://github.com/gruntwork-io/terragrunt
  License: https://github.com/gruntwork-io/terragrunt/blob/v${TG_VER}/LICENSE.txt
EOF

echo "✓ bundled tools fetched into $EXTRAS_DIR/"
