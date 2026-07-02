# GitHub App permissions — what each scope is for

> Reference for the GitHub App that backs the km-github comment-trigger bridge
> (Phases 97–102). Explains every permission and webhook event km requests, **why**
> it needs it, and **what breaks** without it.
>
> The authoritative source is the manifest generator `km github manifest`
> (`internal/app/cmd/github.go`) and the per-sandbox token compiler
> `CompilePermissions` (`pkg/github/token.go`). Keep this doc in sync with those.

## TL;DR

```bash
km github manifest > app.json   # generate the manifest
# GitHub → Settings → Developer settings → GitHub Apps → New → "From a manifest"
```

`km github manifest` declares this `default_permissions` block + a single webhook event:

| Permission | Level (manifest) | One-liner |
|------------|------------------|-----------|
| `issues` | **write** | post/edit PR & issue comments, add the 👀 reaction |
| `pull_requests` | **write** | read PR metadata, submit PR reviews |
| `contents` | **write** | clone/fetch repo contents **and** push commits/branches |
| `checks` | **write** | post check runs (pass/fail/neutral status) |
| `actions` | **write** | dispatch/rerun Actions workflow runs |
| `workflows` | **write** | create/edit `.github/workflows/*` files |
| `metadata` | **read** (implicit) | GitHub auto-adds this to every App; read repo metadata |
| **event** `issue_comment` | — | the only webhook: fires on PR **and** issue comments |

> The minted per-sandbox installation token requests the full write set above
> (`pkg/github.GitHubInboundWritePerms`). The manifest must declare at least
> what the token requests, or the mint 403s with
> `X-Accepted-Github-Permissions: workflows=write` (the bug this set closes).
> `actions`/`workflows` were added so inbound agents can manage CI.

---

## Why each permission

### `issues: write`
The bridge and the sandbox agent post their output as **comments**, and GitHub's
comment API for both PRs and issues lives under the `issues` permission
(`POST /repos/{o}/{r}/issues/{n}/comments`). This scope covers:

- The agent's reply (`km-github comment`).
- The bridge's **👀 ACK reaction** on the triggering comment — the Reactions API on
  a comment is gated by `issues: write`, so there is **no separate reactions
  permission** on the GitHub side (unlike Slack). See `InstallationReactor`,
  `pkg/github/bridge/aws_adapters.go`.
- Phase 102 reply paths: the two-verb conflict reply, the codex-missing helpful
  comment, and the `/help` listing.

**Without it:** the bot can never speak — no ACK, no reply, no error messages. The
bridge would 403 on the reaction and the agent's `km-github comment` would fail.

### `pull_requests: write`
Needed to **read PR metadata** (head SHA, branch, base) and to submit **formal PR
reviews** via the Pull Request Reviews API (`km-github review --event
COMMENT|APPROVE|REQUEST_CHANGES`). The default `/review` flow uses plain comments
(`issues: write`), but `km-github review` — an APPROVE/REQUEST_CHANGES verdict —
requires this scope.

**Without it:** `km-github review` fails; the bot falls back to plain comments only,
and PR-context lookups degrade.

### `contents: read` — and when you need `contents: write`
`contents` governs the repo's **git data** (files, commits, branches):

- `contents: read` → **clone / fetch** the repo so the agent can read code. This is
  the manifest default and is enough for review/triage/comment workflows.
- `contents: write` → **push** commits/branches. Required only when you want the bot
  to *write code back* — i.e. the dispatch prompt's `git push origin HEAD:<branch>`
  or `km-github pr create` (which pushes a branch, then opens the PR).

The per-sandbox token's permissions are compiled from the profile's declared GitHub
capabilities (`CompilePermissions`, `pkg/github/token.go`):

| Profile capability | GitHub permission |
|--------------------|-------------------|
| `clone` / `fetch` | `contents: read` |
| **`push`** | **`contents: write`** (supersedes read) |
| `comment` | `issues: write` |
| `review` | `pull_requests: write` |
| `checks` | `checks: write` |

**To enable push you must bump the App** (Settings → Permissions → Repository →
Contents → **Read & Write**) **and** re-grant the installation. Until then, the token
mint for a push-capable profile would 422 — so km **degrades gracefully**: it retries
the mint with `contents: write` dropped and logs a WARN
(`create.go` `generateAndStoreGitHubToken`), keeping review-only sandboxes working
against a `contents: read` install. So a comment-only bot never strictly needs write.

**Without `contents: read`:** the agent cannot clone the repo — no code to review.
**Without `contents: write`:** the bot can review/comment but cannot push fixes or
open PRs.

### `checks: write`
Lets the bot post a **check run** (a green/red/neutral status box on the PR) via
`km-github check --conclusion success|failure|neutral`. Used to surface a
machine-readable review verdict next to CI.

**Without it:** `km-github check` fails; reviews are comment-only (no status box).

### `metadata: read` (implicit)
GitHub **automatically** adds `metadata: read` to every App — it is the baseline
needed to resolve repository metadata. You do not declare it; GitHub grants it. It is
listed here only so the set is complete.

---

## Webhook event: `issue_comment` (baseline)

By default km subscribes to exactly **one** event (`default_events: ["issue_comment"]`). GitHub's
`issue_comment` event fires for comments on **both** issues and pull requests (a PR is
an issue with code), so this single subscription covers every comment-trigger surface.
The bridge HMAC-verifies the delivery, dedupes by `X-GitHub-Delivery`, checks the
@-mention + allowlist, parses Phase 99 commands / Phase 102 agent verbs, ACKs with 👀,
and dispatches to the per-repo sandbox.

**Why minimal by default?** km is comment-triggered by design — it reacts to a human
@-mentioning the bot, not to every push/PR-open. Subscribing to fewer events means a
smaller attack surface and no wasted Lambda invocations.

**Without it:** no webhooks arrive — the bridge never fires.

## Additional webhook events: `github.events:` autonomous router (Phase 115)

When you configure a `github.events:` block in `km-config.yaml`, `km github manifest`
emits `default_events` as the **union** of `issue_comment` plus every distinct `on:`
type referenced in your rules (e.g. `["issue_comment", "repository"]`). This is opt-in:
absent `github.events:` → still the single-event baseline above. See
`docs/github-bridge.md` § Phase 115 for the router itself.

- **`repository`** (and any event whose payload you template on) requires **`metadata: read`** —
  GitHub auto-grants this, and the manifest declares it explicitly when `repository` is present.
- **Install-scope gotcha (critical):** a **`repository`/`created`** webhook is delivered **only**
  to an App installed on the **organization with "All repositories"** access. A brand-new repo
  cannot be in a "selected repositories" set at creation time, so a selected-repos install gets
  **nothing**. This is the **opposite** of the comment-trigger recommendation (select-repos for
  least privilege) — if you use the event router for repo-create, you must install org-wide.

**After adding a new `on:` type:** re-run `km github manifest`, update the App's subscribed
events + permissions, and **re-install** the App so the new subscription + scopes take effect.

---

## How permissions flow end to end

```
App manifest (km github manifest)         ← declares the MAX the App may request
  → installed on repos/org                ← the install GRANTS a subset
    → bridge mints a per-sandbox token    ← CompilePermissions narrows to the profile's
      scoped to {issues,pull_requests,       declared github capabilities, intersected
      contents,checks} as the profile needs  with what the install granted (422→drop write)
```

The App declares the ceiling; the installation grants the actual set; each sandbox
token is the **narrowest** slice its profile needs. Least privilege at every hop.

---

## Verifying & updating

- **Inspect what's stored:** `km github status` (bot-login, bridge-url, repos, commands).
- **Regenerate the manifest** after changing scopes: `km github manifest > app.json`,
  then paste into the App's settings (or recreate from manifest) and re-grant the install.
- **Enable push** (opt-in): bump Contents to *Read & Write* on the App, re-grant, and
  give the relevant profile the `push` capability. Otherwise km auto-drops `contents:write`.

See `docs/github-bridge.md` for the full operator runbook, deploy sequence, and
federation/orphan-reply behavior (Phases 100/101).
