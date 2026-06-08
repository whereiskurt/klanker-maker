// Package bridge — Phase 101 orphan-repo guidance comment.
//
// maybePostGitHubOrphanComment is called by Handle() on the front-door
// !matched/non-relayed path ONLY when h.DefaultRouter is true and the
// Broadcast tally yielded zero claims (no peer owns the repo).
//
// It implements the Slack Phase-96 analog for GitHub, minus running-channels:
// post ONE threaded comment naming github.repos: + km init when the bot was
// @-mentioned in an unowned repo. Suppressed by a per-(repo,number) cooldown
// of 3600 s backed by the shared nonces DynamoDB table.
package bridge

import (
	"context"
	"fmt"
	"time"
)

// maybePostGitHubOrphanComment posts a single guidance comment when all four
// gates pass (in order):
//
//  1. h.DefaultRouter is true (Phase 101 front-door feature enabled).
//  2. The comment @-mentions the bot (ContainsMention re-check — the Phase-100
//     Resolve() reorder skipped the mention filter on the !matched path; Pitfall 2).
//  3. h.Commenter != nil AND payload.Installation.ID != 0 (prevents nil-deref
//     and unroutable API calls; Pitfall 1).
//  4. Cooldown: OrphanCooldown.CheckAndStore("gh-router-cooldown:{owner}/{repo}#{number}",
//     3600) returns seen==false (first time in the 3600 s window).
//
// On all-pass, ONE PostComment is fired under a 5 s bounded context so the
// Lambda does not freeze (RESEARCH Pitfall 3). Any gate failure silently returns
// and Handle still returns 200 — the orphan comment is advisory, never fatal.
func (h *WebhookHandler) maybePostGitHubOrphanComment(ctx context.Context, payload IssueCommentPayload, botLogin string) {
	// Gate 1: feature must be enabled.
	if !h.DefaultRouter {
		return
	}

	// Gate 2: comment must @-mention the bot.
	// The Phase-100 reorder moved Resolve() before the mention filter on the
	// matched path; on the !matched path the mention filter is never reached.
	// Re-check here so we only reply when the comment was genuinely directed
	// at the bot (not e.g. a drive-by comment that happens to be in an unowned repo).
	if !ContainsMention(payload.Comment.Body, botLogin) {
		return
	}

	// Gate 3: Commenter and Installation.ID must be set.
	if h.Commenter == nil || payload.Installation.ID == 0 {
		return
	}

	owner := OwnerFromFullName(payload.Repository.FullName)
	repo := RepoFromFullName(payload.Repository.FullName)

	// Gate 4: cooldown — suppress repeated orphan comments on the same PR/issue.
	// Key format: gh-router-cooldown:{owner}/{repo}#{number}
	// TTL: 3600 s (1 hour). Reuses OrphanCooldown (a DeliveryNonceStore, backed by
	// the shared DynamoDB nonces table). Nil OrphanCooldown skips this gate.
	if h.OrphanCooldown != nil {
		key := fmt.Sprintf("gh-router-cooldown:%s/%s#%d", owner, repo, payload.Issue.Number)
		seen, err := h.OrphanCooldown.CheckAndStore(ctx, key, 3600)
		if err != nil {
			h.log().Warn("github-bridge: orphan cooldown check failed; skipping",
				"err", err, "repo", payload.Repository.FullName)
			return
		}
		if seen {
			h.log().Debug("github-bridge: orphan cooldown active; suppressing comment",
				"repo", payload.Repository.FullName,
				"number", payload.Issue.Number)
			return
		}
	}

	// All gates passed — build and post the guidance comment.
	body := fmt.Sprintf(
		"No klanker sandbox is bound to `%s`. "+
			"To enable the bot here, an operator must add this repository under "+
			"`github.repos:` in `km-config.yaml` and run `km init`. "+
			"See the github-bridge runbook (`docs/github-bridge.md`) for setup instructions.",
		payload.Repository.FullName,
	)

	// Bounded context: 5 s max. Lambda freezes when Handle() returns; synchronous
	// completion is required (RESEARCH Pitfall 3, same as the 👀 reactor).
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := h.Commenter.PostComment(
		cctx,
		InstallIDString(payload.Installation.ID),
		owner,
		repo,
		payload.Issue.Number,
		body,
	); err != nil {
		h.log().Warn("github-bridge: orphan comment post failed (non-fatal)",
			"repo", payload.Repository.FullName,
			"number", payload.Issue.Number,
			"err", err)
	}
}
