# Deferred Items — Phase 17

## GitHub App Permission Refinement (moved from Phase 17 requirements)

**Reason:** This is a Phase 13 (GitHub App) refinement, not related to email mailbox/access control. Incorrectly placed in Phase 17 scope.
**Description:** Tighten GitHub App manifest from `contents: write` to `contents: read` + `pull_requests: write` — sandboxes should create PRs and feature branches, not push directly to protected branches. Update `BuildManifestJSON()` in `internal/app/cmd/configure_github.go` and `spec.sourceAccess.github.permissions` schema to reflect the narrower scope.
**Remediation:** Create a Phase 13 gap closure plan or a standalone refinement phase to address this.
