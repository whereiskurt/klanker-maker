#!/usr/bin/env bash
# bump-version.sh — increment patch version in VERSION file.
# Rolls v0.0.999 -> v0.1.0 and v0.255.999 -> v1.0.0 (though unlikely).
# Prints the new version to stdout.
set -euo pipefail

VERSION_FILE="${1:-VERSION}"

if [ ! -f "$VERSION_FILE" ]; then
  echo "0.0.1" > "$VERSION_FILE"
  echo "v0.0.1"
  exit 0
fi

current=$(cat "$VERSION_FILE" | tr -d '[:space:]')
IFS='.' read -r major minor patch <<< "$current"

patch=$((patch + 1))
if [ "$patch" -ge 1000 ]; then
  patch=0
  minor=$((minor + 1))
fi
if [ "$minor" -ge 256 ]; then
  minor=0
  major=$((major + 1))
fi

new="${major}.${minor}.${patch}"
echo "$new" > "$VERSION_FILE"
echo "v${new}"
