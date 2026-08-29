#!/usr/bin/env bash
# Entrypoint for the km operator client image.
#
# Prints a short banner (release, mounted config, credential state) so a fresh
# shell tells you immediately whether the runtime mounts landed, then execs the
# command — bash by default.
set -euo pipefail

if [ -t 1 ]; then
  tag="$(cat /klanker-maker/.km-release-tag 2>/dev/null || echo unknown)"
  echo "klanker-maker operator client — km ${tag} ($(uname -m))"

  if [ -s /klanker-maker/km-config.yaml ]; then
    prefix="$(grep -E '^[[:space:]]*resource_prefix:' /klanker-maker/km-config.yaml | head -1 | awk '{print $2}')"
    region="$(grep -E '^[[:space:]]*region:' /klanker-maker/km-config.yaml | head -1 | awk '{print $2}')"
    echo "  config : km-config.yaml (prefix=${prefix:-km} region=${region:-unset})"
  else
    # km-config.yaml is gitignored, so an image built from a fresh clone has
    # none. Point at the two ways out rather than letting km fail obscurely.
    echo "  config : MISSING — mount yours with"
    echo "           -v \"\$PWD/km-config.yaml:/klanker-maker/km-config.yaml\""
    echo "           or run 'km configure' (see km-config.example.yaml)"
  fi

  # ~/.aws is created empty by the image; a non-empty dir means the host mount
  # landed. Distinguishing the two is the whole point of the banner.
  if [ -n "$(ls -A /root/.aws 2>/dev/null)" ]; then
    echo "  aws    : ~/.aws mounted${AWS_PROFILE:+ (AWS_PROFILE=${AWS_PROFILE})}"
  else
    echo "  aws    : NOT MOUNTED — re-run with -v \"\$HOME/.aws:/root/.aws\""
  fi

  # Per-sandbox ed25519 keys live at ~/.km/keys/<id>. Unmounted, km vscode /
  # desktop / tunnel mint a fresh keypair that dies with the container, and the
  # sandbox is left holding an authorized_keys entry for a key nobody has.
  if [ -n "$(ls -A /root/.km/keys 2>/dev/null)" ]; then
    echo "  keys   : ~/.km mounted ($(ls -1 /root/.km/keys 2>/dev/null | wc -l | tr -d ' ') sandbox keypair(s))"
  else
    echo "  keys   : no ~/.km mount — km vscode/desktop/tunnel will rekey per run"
  fi

  # Overlay, not replacement: KM_PROFILE_SEARCH_PATHS lists the mount ahead of
  # the baked library, so an `extends:` still finds profiles/base/** fragments.
  if [ -n "$(ls -A /root/.km/profiles 2>/dev/null)" ]; then
    echo "  profile: overlay at ~/.km/profiles + $(ls -1 /klanker-maker/profiles/*.yaml 2>/dev/null | wc -l | tr -d ' ') built-in"
  else
    echo "  profile: $(ls -1 /klanker-maker/profiles/*.yaml 2>/dev/null | wc -l | tr -d ' ') built-in (overlay: -v \"\$PWD/profiles:/root/.km/profiles\")"
  fi

  # The plan tier needs all three: terragrunt on PATH, the infra tree, and the
  # prebuilt Lambda zips the Lambda modules filebase64sha256() at plan time.
  # Reporting them together means a partial image says so up front, rather than
  # at the first module that trips over the missing piece.
  if command -v terragrunt >/dev/null 2>&1 \
     && [ -d /klanker-maker/infra/live ] \
     && [ -n "$(ls -A /klanker-maker/build/*.zip 2>/dev/null)" ]; then
    echo "  plan   : ready — km init --plan (read-only; apply needs a native install)"
  else
    echo "  plan   : unavailable — image lacks terragrunt, infra/, or build/*.zip"
  fi

  echo "  try    : km list · km validate profiles/spot.yaml · km init --dry-run"
  echo
fi

exec "$@"
