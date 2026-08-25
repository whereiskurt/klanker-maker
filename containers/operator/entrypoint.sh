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

  echo "  try    : km list · km validate profiles/spot.yaml · km doctor"
  echo
fi

exec "$@"
