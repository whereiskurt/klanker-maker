# Deferred Items -- Phase 13

## Ref Enforcement (ROADMAP requirement: credential helper rejects git push to non-allowedRepos refs)

**Status:** Explicitly deferred
**ROADMAP text:** "Ref enforcement: credential helper or proxy rejects git push to refs not in sourceAccess.github.allowedRepos[].refs (defense in depth -- GitHub App scoping is primary control)"
**Research reference:** 13-RESEARCH.md, Pattern 8

**Rationale:** Per RESEARCH.md Pattern 8, ref enforcement is a secondary/defense-in-depth control. The primary control is GitHub App token scoping -- tokens are scoped to specific repos at issuance time, so operations on repos not in allowedRepos are rejected by GitHub itself. The research recommends deferring local ref checking for v1:

> "Simpler approach (recommended for v1): Ref enforcement via git config receive.denyNonFastForwards and branch protection at the GitHub level. The per-sandbox token's repo scoping is the primary control. Ref checking in the credential helper adds complexity for limited security gain if GitHub App permissions are tightly scoped."

**Future implementation path:** If needed, a wrapper script around `git` (not GIT_ASKPASS itself, which only provides credentials) that validates target refs before calling the real `git` binary. Could be added as a Phase 15+ enhancement.

## ECS GIT_ASKPASS Credential Helper Injection

**Status:** Explicitly deferred
**ROADMAP text:** "Sandbox boots with GIT_ASKPASS credential helper that reads token from SSM"
**Research reference:** 13-RESEARCH.md, Pattern 4 (ECS containers section) and Open Question 2

**Rationale:** EC2 sandboxes receive the GIT_ASKPASS script via userdata.go injection. ECS containers do not use userdata -- the script must be baked into the sandbox image or delivered via a sidecar volume mount. Per RESEARCH.md Open Question 2:

> "For Phase 13 v1, the credential helper is EC2-only (injected via userdata). ECS sandboxes use GIT_ASKPASS set in the ECS container environment pointing to a script in the image."

The github_token_inputs block IS emitted for ECS substrates (so the Lambda/EventBridge token refresh infrastructure is deployed), but the in-sandbox credential helper script delivery mechanism for ECS is deferred. The sidecar build pipeline (Phase 8) can include the km-git-askpass script in a future update.

**Impact:** ECS sandboxes will have tokens refreshed in SSM but no automated credential helper injection. Manual workaround: bake km-git-askpass into the sandbox container image.

**Future implementation path:** Add km-git-askpass to the sidecar image build or as a shared volume mount in the ECS task definition compiler output.
