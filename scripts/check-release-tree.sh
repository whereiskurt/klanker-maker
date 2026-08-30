#!/usr/bin/env bash
#
# Release pre-flight: refuse to build an archive from a tree that carries local
# Terraform/Terragrunt caches.
#
# WHY THIS EXISTS
#
# .goreleaser.yaml archives the terragrunt tree with:
#
#     - infra/**/*.hcl
#     - infra/**/*.tf
#
# Those globs are extension-scoped, which correctly keeps __pycache__ and
# .DS_Store out of a public release. It does NOT keep .terragrunt-cache/ or
# .terraform/ out, because those directories are full of exactly .hcl and .tf
# files — a rendered backend.tf, main.tf, terragrunt.hcl per cached unit.
#
# goreleaser's file globs have no negation, and tightening the patterns by
# directory depth would be worse: a tracked file added at an unexpected depth
# would then vanish from the archive silently, which is a harder failure to
# notice than this one. So the tree is checked instead of the pattern.
#
# WHAT IT PREVENTS
#
# A local `goreleaser release` from a working tree where anyone has run
# `km init` or `km create --local` would publish rendered backend config as a
# public release asset — state bucket name, DynamoDB lock table, and the state
# key path for every unit. Observed: 528 such files in a local snapshot.
#
# CI is unaffected. actions/checkout gives a clean tree and both directories are
# gitignored, so they never exist there — which is why every published release
# to date is clean. This guard is for the local path that the release runbook in
# CLAUDE.md documents as a sanity check.
set -uo pipefail

found=$(find infra -type d \( -name .terragrunt-cache -o -name .terraform \) 2>/dev/null || true)

if [ -n "$found" ]; then
  count=$(printf '%s\n' "$found" | wc -l | tr -d ' ')
  {
    echo "ERROR: $count local Terraform/Terragrunt cache director(ies) under infra/."
    echo
    printf '%s\n' "$found" | head -10 | sed 's/^/  /'
    [ "$count" -gt 10 ] && echo "  … and $((count - 10)) more"
    echo
    echo "The archive globs infra/**/*.hcl and infra/**/*.tf match the .hcl and .tf"
    echo "files inside these directories, so releasing from this tree would publish"
    echo "your own state bucket, lock table and state keys as a public asset."
    echo
    echo "Clear them and re-run:"
    echo "  find infra -type d \\( -name .terragrunt-cache -o -name .terraform \\) \\"
    echo "    -prune -exec rm -rf {} +"
  } >&2
  exit 1
fi
