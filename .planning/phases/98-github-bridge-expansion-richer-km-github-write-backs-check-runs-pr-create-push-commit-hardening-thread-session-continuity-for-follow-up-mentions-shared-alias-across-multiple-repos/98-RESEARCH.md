# Phase 98: GitHub Bridge Expansion — Research

**Researched:** 2026-06-07
**Domain:** GitHub App bridge extension — write-backs, continuity, cold-create fix, auto-resume
**Confidence:** HIGH (primary evidence from in-tree code and Phase 97 live UAT findings)

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| GH-X-CHECK | `km-github check` posts a check run (name + conclusion success/failure/neutral + summary) on the PR | GitHub Checks API: `POST /repos/{owner}/{repo}/check-runs`. Existing `km-github` dispatch table in `cmd/km-github/main.go`; extend `dispatch()` + `usage()`. Token already has `checks:write` via `GitHubInboundWritePerms()` in `pkg/github/token.go:228`. |
| GH-X-PRCREATE | `km-github pr create` opens a PR from a new branch (`--title/--base/--head/--body`) and returns its URL | GitHub PRs API: `POST /repos/{owner}/{repo}/pulls`. Returns `{html_url, number}`. Same token + same helper pattern as `review`/`comment`. |
| GH-X-PUSH | Push-commit path hardened end-to-end (App write scopes from Phase 97); worktree-per-PR commit/push verified | `contents:write` already requested since Phase 97. Git credential helper (`km-git-askpass`, `km-git-credential-helper`) already in userdata. Verification: test that `git push` inside a worktree uses the token. Preamble update needed (add explicit worktree-per-PR instructions). |
| GH-X-CONTINUITY | `(repo, number) → {sandbox_id, agent_session_id}` mapping so follow-up @-mentions in the same PR/issue continue the same agent session | New DDB table `km-github-threads` (twin of `km-slack-threads`) with hash `repo` (S) + range `number` (N). Poller writes `agent_session_id` after each turn. Bridge reads it on warm path before dispatching. |
| GH-X-THREADBYPASS | Replies in a known PR/issue thread dispatch without requiring a re-@-mention | Bridge step-5 gets a short-circuit: if `km-github-threads` has a row for `(repo, number)`, skip the mention check. Mirrors Phase 91.3 `LookupSandbox` pattern in `pkg/slack/bridge/events_handler.go:323-329`. |
| GH-X-SHARED | Multiple `github.repos:` entries may point at one shared alias (single larger sandbox), with worktree-per-PR isolation; `km doctor` warns on match overlap / alias collisions | Phase 97 already resolves alias from config (`pkg/github/bridge/resolve.go`). The shared-alias feature only needs: (a) preamble tells agent to use worktree-per-PR, (b) `km doctor` multi-repo alias collision check (same alias → different repos → WARN). No new infra. |
| GH-X-RESUME | Warm-path alias lookup that finds a stopped/paused sandbox auto-resumes it; bridge gains `ec2:StartInstances` IAM | `DynamoAliasResolver.ResolveByAlias` currently returns error only for "not found". Extend: on found, check DDB `status` field; if stopped/paused, call `ec2:StartInstances` on the instance, update DDB status to "resuming", enqueue to the SQS FIFO. Instance starts async; sandbox poller starts when box is up. Bridge IAM needs new `ec2:DescribeInstances` + `ec2:StartInstances` policy in `lambda-github-bridge/v1.0.0/main.tf`. |
| GH-COLD-CREATE | Fix broken cold-create path (sandbox_id missing, artifact_bucket/prefix malformed, no profile staged, no Claude creds) | Four concrete fixes: (1) bridge generates `sandbox_id` via `compiler.GenerateSandboxID` before publishing; (2) bridge reads `KM_ARTIFACTS_BUCKET` env var + uses proper prefix `sandboxes/{sandbox_id}`; (3) `km init` pre-stages each `github.repos` profile to S3 at `{bucket}/github-profiles/{profile-slug}/.km-profile.yaml`; (4) `github-review.yaml` gets `spec.secrets.sopsFile` pointing to operator-encrypted SOPS bundle containing Claude credentials; `patchProfileForSops` in create-handler already handles this. |
| GH-X-E2E | Follow-up @-mention continues the session; check run + opened PR visible; shared-alias dispatch across two repos to one sandbox; stopped-alias @-mention auto-resumes and processes | Manual UAT: real AWS + GitHub. All sub-requirements above individually verifiable by unit/integration tests; the full E2E needs a live setup. |
</phase_requirements>

---

## Summary

Phase 98 is primarily **assembly and repair** rather than novel invention. Every building block exists; the work is wiring them together correctly, fixing the broken cold-create path, and adding a new DDB table for GitHub thread continuity.

The broken cold-create path (GH-COLD-CREATE) has four distinct defects revealed by Phase 97 UAT: missing `sandbox_id` in the EventBridge payload, malformed `artifact_prefix`, no profile staged in S3, and no Claude authentication on a fresh box. Each has a straightforward fix using in-tree patterns. GH-X-RESUME is the natural companion: the bridge currently treats any "alias found but box is stopped" the same as "cold create", which silently enqueues to a dead queue. Auto-resume requires a status check + `ec2:StartInstances` call in the bridge.

GH-X-CONTINUITY and GH-X-THREADBYPASS follow the exact pattern of Phase 91.3 Slack thread-bypass. A new `km-github-threads` DDB table (twin of `km-slack-threads`) needs a new TF module + live unit, wiring into the bridge handler, IAM grant to the bridge Lambda, and a write in the sandbox-side poller after each agent turn. GH-X-CHECK and GH-X-PRCREATE are additions to `cmd/km-github/main.go` — the dispatch table already shows the pattern.

**Primary recommendation:** Plan in seven waves: (1) km-github check + pr create verbs, (2) push hardening + preamble update, (3) GH-COLD-CREATE fix, (4) GH-X-RESUME auto-resume, (5) km-github-threads table + GH-X-CONTINUITY, (6) GH-X-THREADBYPASS + shared-alias doctor check, (7) deploy-surface verification + E2E UAT gate.

---

## Standard Stack

### Core (all in-tree, Phase 97 established)

| Component | Location | Purpose | Phase 98 Change |
|-----------|----------|---------|-----------------|
| `cmd/km-github/main.go` | `cmd/km-github/` | Sandbox-side GitHub helper | Add `check` + `pr create` verbs |
| `pkg/github/bridge/webhook_handler.go` | `pkg/github/bridge/` | 11-step Handle() dispatch | Add thread-bypass step + resume branch |
| `pkg/github/bridge/aws_adapters.go` | `pkg/github/bridge/` | DynamoAliasResolver, reactor | Add `ResolveWithStatus`, `Resumer` interface |
| `pkg/github/bridge/interfaces.go` | `pkg/github/bridge/` | Bridge interfaces | Add `SandboxResumer`, `GitHubThreadStore` |
| `pkg/github/token.go` | `pkg/github/` | JWT + token exchange | No change needed (write perms already set) |
| `internal/app/cmd/create.go` | `internal/app/cmd/` | Sandbox ID gen, S3 staging | Add `km init` profile pre-stage helper |
| `cmd/create-handler/main.go` | `cmd/create-handler/` | EventBridge cold-create | Validate non-empty sandbox_id (already does); no change if bridge sends correctly |
| `pkg/github/bridge/resolve.go` | `pkg/github/bridge/` | Config → alias resolution | No change (shared-alias already works by config) |

### New Infrastructure

| Component | Type | Purpose |
|-----------|------|---------|
| `infra/modules/dynamodb-github-threads/v1.0.0/` | New TF module | `km-github-threads` DDB table: hash=`repo` (S), range=`number` (N) |
| `infra/live/use1/dynamodb-github-threads/terragrunt.hcl` | New live unit | Deploy the table |
| EC2 IAM in `lambda-github-bridge/v1.0.0/main.tf` | IAM policy addition | `ec2:DescribeInstances` + `ec2:StartInstances` for GH-X-RESUME |
| DDB `km-github-threads` IAM in `lambda-github-bridge/v1.0.0/main.tf` | IAM policy addition | `dynamodb:GetItem` + `dynamodb:PutItem` on threads table |

**Installation (already in go.mod — no new deps):**
```bash
# No new Go dependencies needed; all AWS SDK, GitHub API, DDB already in use.
make build-lambdas  # after all code changes
km init --dry-run=false  # new DDB table + bridge IAM + env block
km init --sidecars  # poller change needs create-handler refresh if schema changes
```

---

## Architecture Patterns

### Pattern 1: km-github verb extension (GH-X-CHECK, GH-X-PRCREATE)

Verbatim pattern from `cmd/km-github/main.go` — every verb is a `run<Verb>` + `run<Verb>With` pair:

```go
// Source: cmd/km-github/main.go — dispatch pattern
case "check":
    return runCheck(args[1:], stderr)
case "pr":
    if len(args) < 2 { usage(stderr); return 2 }
    switch args[1] {
    case "create":
        return runPRCreate(args[2:], stderr)
    }
```

**Check run API shape** (GitHub Checks v3, `checks:write` required):
```go
// POST /repos/{owner}/{repo}/check-runs
type checkRunPayload struct {
    Name       string `json:"name"`
    HeadSHA    string `json:"head_sha"`   // required
    Status     string `json:"status"`     // "completed"
    Conclusion string `json:"conclusion"` // "success"|"failure"|"neutral"
    Output     struct {
        Title   string `json:"title"`
        Summary string `json:"summary"`
    } `json:"output"`
}
```

**PR create API shape** (pull_requests:write):
```go
// POST /repos/{owner}/{repo}/pulls
type prCreatePayload struct {
    Title string `json:"title"`
    Head  string `json:"head"`  // branch to merge
    Base  string `json:"base"`  // target branch
    Body  string `json:"body,omitempty"`
}
// Response: {html_url, number} — print to stdout for agent to read
```

### Pattern 2: Thread-bypass (GH-X-THREADBYPASS)

Mirrors `pkg/slack/bridge/events_handler.go:311-339`. Insert before the mention check in `webhook_handler.go`:

```go
// Step 4b: Thread-bypass for known PR/issue threads.
// If (repo, number) is already tracked in km-github-threads, skip
// the @-mention requirement — the conversation is already in progress.
if h.Threads != nil {
    sandboxID, lookupErr := h.Threads.LookupSandbox(ctx, payload.Repository.FullName, payload.Issue.Number)
    if lookupErr == nil && sandboxID != "" {
        // Known thread — skip step 5 (mention check), go straight to dispatch.
        goto dispatch
    }
}
```

### Pattern 3: GH-COLD-CREATE fix — bridge generates sandbox_id

The bridge's `EventBridgeAdapter.PutSandboxCreate` in `aws_adapters.go:324` must be extended:

```go
// Source: pkg/github/bridge/aws_adapters.go — EventBridgeAdapter fix
func (a *EventBridgeAdapter) PutSandboxCreate(ctx context.Context, alias, profile, githubEnvelopeJSON string) error {
    // Generate a valid sandbox_id (mirrors compiler.GenerateSandboxID("gh")).
    sandboxID := generateGitHubSandboxID()  // "gh-" + 8 hex chars
    // Artifact prefix follows the canonical pattern: "sandboxes/{sandbox_id}"
    detail := sandboxCreateDetail{
        SandboxID:      sandboxID,
        ArtifactBucket: a.ArtifactBucket,
        ArtifactPrefix: "sandboxes/" + sandboxID,
        Alias:          alias,
        GithubEnvelope: githubEnvelopeJSON,
    }
    // ...
}
```

The bridge also needs `ArtifactBucket` from env (`KM_ARTIFACTS_BUCKET`) — add to Lambda env block in `main.tf`.

### Pattern 4: km init profile pre-staging (GH-COLD-CREATE)

Add a `preStageGitHubProfiles` step to `internal/app/cmd/init.go` that runs after the env export block:

```go
// For each github.repos entry, compile the profile and stage to S3:
// s3://{artifactBucket}/sandboxes/{alias-derived-prefix}/.km-profile.yaml
// The bridge's artifact_prefix "sandboxes/{sandbox_id}" resolves to this key.
// Note: sandbox_id is unknown at pre-stage time; bridge generates it at cold-create.
// Solution: stage under a per-alias key, NOT per-sandbox-id key.
// REVISED: stage as github-profiles/{profile-slug}/.km-profile.yaml
// and have the bridge set artifact_prefix = "github-profiles/{profile-slug}"
```

**IMPORTANT DESIGN DECISION:** The standard `runCreateRemote` in `create.go` uploads artifacts to `sandboxes/{sandboxID}/` including `service.hcl`, `user-data.sh`, `.km-profile.yaml`. The create-handler expects all three. The bridge can only pre-stage the profile. Options:
1. **Option A (preferred):** Bridge generates full sandbox_id, then runs a mini create-prepare step (calls a new operator-like helper inside the bridge Lambda). Rejected — bridge Lambda can't run `km create` compile (no Terraform).
2. **Option B (adopted):** Bridge emits a `SandboxCreate` event where the profile is embedded inline (not fetched from S3), and create-handler compiles from the inline profile. Requires create-handler change.
3. **Option C (simplest, recommended):** `km init` pre-stages the profile + a minimal compile to S3 at a per-profile slug path. Bridge sets `artifact_prefix = "github-profiles/{profile-slug}"`. The create-handler's `km create` subprocess receives the pre-staged profile path and completes compilation locally. This works because create-handler's `km create` subprocess re-compiles from the profile YAML — it only needs the profile, not pre-compiled service.hcl.

Option C is confirmed correct: `create-handler/main.go:7` says "Downloads the sandbox profile from S3 at `{artifact_prefix}/.km-profile.yaml`" and runs `km create` which recompiles from it. So pre-staging `github-profiles/{slug}/.km-profile.yaml` works if bridge sets `artifact_prefix = "github-profiles/" + profileSlug`.

### Pattern 5: GH-X-RESUME — stopped sandbox detection + StartInstances

Extend `DynamoAliasResolver` to also return the sandbox status:

```go
// pkg/github/bridge/aws_adapters.go — new method
func (r *DynamoAliasResolver) ResolveByAliasWithStatus(ctx context.Context, alias string) (sandboxID, status string, err error) {
    // Query alias-index GSI; project both sandbox_id AND status fields.
    // Returns status="" if attribute absent (backward compat).
}
```

In `WebhookHandler.Handle`, the warm dispatch branch becomes:
```go
sandboxID, status, resolveErr := h.Resolver.ResolveByAliasWithStatus(ctx, alias)
if resolveErr != nil {
    // truly cold (not found)
    h.Publisher.PutSandboxCreate(...)
} else if status == "stopped" || status == "paused" {
    // resume path
    h.Resumer.StartSandbox(ctx, sandboxID)
    // still enqueue — poller will start when box comes up
    h.SQS.Send(ctx, queueURL, ...)
} else {
    // warm path (running)
    h.SQS.Send(ctx, queueURL, ...)
}
```

The `SandboxResumer` interface needs `StartSandbox(ctx, sandboxID)` backed by an EC2 `StartInstances` call (requires instance ID from DDB `instance_id` attribute).

### Recommended Project Structure (new files)

```
cmd/km-github/
├── main.go                     # add check + pr create verbs
infra/modules/
├── dynamodb-github-threads/
│   └── v1.0.0/
│       ├── main.tf             # NEW: km-github-threads table
│       ├── variables.tf
│       └── outputs.tf
infra/live/use1/
├── dynamodb-github-threads/
│   └── terragrunt.hcl          # NEW: live unit
│   lambda-github-bridge/       # existing: bump module to v1.1.0 for IAM additions
pkg/github/bridge/
├── webhook_handler.go          # extend: thread-bypass step, resume branch
├── interfaces.go               # extend: GitHubThreadStore, SandboxResumer
├── aws_adapters.go             # extend: DynamoAliasResolver.ResolveByAliasWithStatus,
│                               #         DynamoGitHubThreadStore, EC2Resumer,
│                               #         EventBridgeAdapter sandbox_id gen
internal/app/cmd/
├── init.go                     # extend: preStageGitHubProfiles() step
├── doctor.go                   # extend: alias collision check across repos
```

### Anti-Patterns to Avoid

- **Don't generate sandbox_id with a fixed prefix that conflicts.** Use `"gh-" + 8 hex` (same as `compiler.GenerateSandboxID("gh")`). Avoid `sb-` prefix for github-created boxes so they're identifiable in `km list`.
- **Don't route the resume path through cold-create.** A stopped alias row in DDB exists, so `ResolveByAlias` returns it — calling `PutSandboxCreate` would create a SECOND sandbox with the same alias. The resume path must call `StartInstances` on the existing sandbox.
- **Don't build a new DDB table for github-threads that reuses km-slack-threads' schema verbatim.** Slack uses `channel_id + thread_ts` (both strings). GitHub uses `repo + number` (string + int). New table needs `repo` (S) hash + `number` (N) range.
- **Don't forget SandboxMetadata lossy round-trip.** Any new DDB attrs (e.g. `github_thread_session_id`) must be added to `metadata.go` struct + all four spots in `sandbox_dynamo.go` (copy/unmarshal/marshal/UpdateItem) — else pause/resume/ttl-handler strips them on full-row PutItem.
- **Don't deploy with `km init --sidecars` when adding new IAM or env vars** to `lambda-github-bridge`. IAM + env block changes require `km init --dry-run=false` (full terragrunt apply).

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Sandbox ID generation | Custom UUID/random string | `compiler.GenerateSandboxID("gh")` | Canonical `{prefix}-{8hex}` format expected by all tooling |
| GitHub App JWT | Custom RSA signing | `pkg/github.GenerateGitHubAppJWT` | PKCS#1/PKCS#8 dual-format, correct iat/exp claims |
| Installation token exchange | Raw HTTP POST | `pkg/github.ExchangeForInstallationToken` | Handles repo short-name stripping, wildcard, perms map |
| SSM token read on sandbox | Custom SDK call | `loadToken()` in `cmd/km-github/main.go` | Already handles KM_SANDBOX_ID/KM_RESOURCE_PREFIX env |
| DDB conditional write for dedup | Custom atomic check | `DynamoGitHubNonceStore.CheckAndStore` | Uses `attribute_not_exists` conditional expression correctly |
| Thread ID lookup | Scan table | GSI on `alias-index` / new `repo-number-index` | Scan is unbounded; GSI is O(1) |
| EC2 StartInstances call | Custom EC2 client | Narrow interface pattern (see `resume.go:105`) | Testable without real EC2 |

---

## Common Pitfalls

### Pitfall 1: GH-COLD-CREATE — artifact_prefix semantics
**What goes wrong:** Bridge sets `artifact_prefix = "profiles/" + profile + ".yaml"` (doubled path). `create-handler` then tries to fetch `.km-profile.yaml` at `profiles/github-review.yaml/.km-profile.yaml` — doesn't exist.
**Why it happens:** The bridge's `EventBridgeAdapter` was written with a wrong assumption about the prefix convention. `create-handler/main.go:7` says the prefix is the DIRECTORY, and the profile file is always `.km-profile.yaml` within it.
**How to avoid:** Set `ArtifactPrefix = "github-profiles/" + profileSlug` and pre-stage the profile at `github-profiles/{slug}/.km-profile.yaml` during `km init`.
**Warning signs:** `create-handler` logs `"download profile"` with a path that ends in `.yaml/.km-profile.yaml`.

### Pitfall 2: GH-X-RESUME — enqueue to a stopped sandbox's queue
**What goes wrong:** Bridge finds the alias (box stopped), enqueues to the FIFO queue URL from DDB, then... nothing. The poller only runs when the box is running. Message sits in queue until SQS visibility/retention expires.
**Why it happens:** The warm path in Phase 97 is status-agnostic — it just checks if `alias` resolves to a `sandbox_id` and sends to the queue.
**How to avoid:** Check DDB `status` field after alias resolution. If `stopped` or `paused`, call EC2 StartInstances THEN enqueue. The poller starts on box boot and drains the queue.
**Warning signs:** Enqueuing succeeds (SQS returns 200) but no agent run appears; `km list` shows the sandbox as `stopped`.

### Pitfall 3: km-github-threads table — must be in init.go's module list
**What goes wrong:** New TF module created in `infra/modules/dynamodb-github-threads/v1.0.0/`, live unit in `infra/live/use1/dynamodb-github-threads/terragrunt.hcl`, but `km init` never applies it because it's not in `regionalModules()` in `init.go`.
**Why it happens:** The "New Lambda needs live unit + init.go list" memory footgun — applies to all managed modules, not just Lambdas.
**How to avoid:** Add `dynamodb-github-threads` to `regionalModules()` in `init.go` after `dynamodb-slack-threads`. Add a guard test `TestRegionalModulesIncludesGitHubThreads`.

### Pitfall 4: Bridge Lambda module bump required for new IAM
**What goes wrong:** Adding EC2 IAM + DDB threads IAM + `KM_ARTIFACTS_BUCKET` env to `lambda-github-bridge` without bumping the module version creates a Terraform drift issue (existing state was v1.0.0).
**How to avoid:** Create `lambda-github-bridge/v1.1.0/` with all additions. Update the live `terragrunt.hcl` to source v1.1.0. Full apply required.

### Pitfall 5: SandboxMetadata lossy round-trip for github_session_id
**What goes wrong:** The sandbox poller writes `agent_session_id` to the `km-github-threads` table (not to `km-sandboxes`). But if continuity data lands in `km-sandboxes` instead, it must be in all four marshal/unmarshal spots.
**How to avoid:** Keep agent session continuity in the `km-github-threads` table (not in `km-sandboxes`). The bridge reads from `km-github-threads`; `km-sandboxes` stays as-is. This avoids the SandboxMetadata lossy round-trip footgun entirely.

### Pitfall 6: Cold-create SOPS bundle not staged
**What goes wrong:** `github-review.yaml` profile references `spec.secrets.sopsFile: "github-review-secrets.enc.yaml"`. `km init` pre-stages the profile YAML to S3, but the SOPS bundle file is NOT uploaded. The create-handler's `patchProfileForSops` then fails to find the bundle and rejects the create.
**How to avoid:** `preStageGitHubProfiles()` in `km init` must ALSO upload the SOPS bundle to `github-profiles/{slug}/.km-secrets-bundle.enc.yaml` (mirrors `create.go:2261`). The operator must have the SOPS bundle on their workstation at the relative path declared in the profile.

### Pitfall 7: km-github check verb requires head_sha
**What goes wrong:** `POST /repos/{owner}/{repo}/check-runs` with a missing or wrong `head_sha` returns 422. The bridge envelope already carries `HeadSHA` (`head_sha` field in `GitHubEnvelope`); the poller must pass it via the preamble.
**How to avoid:** Confirm `GitHubEnvelope.HeadSHA` is populated by the bridge (check `payload.go` — it's set from `payload.Comment.CommitID` if present, else from the PR's `head.sha` which requires an extra API call). For the MVP, pass `--head-sha` as optional and fall back to `UNKNOWN` with a warning.

### Pitfall 8: Deploy-surface verification (from Phase 97 lessons)
**What goes wrong:** Code passes `go build ./...` and unit tests, but deploy is broken because of IAM gaps, missing module entries, artifact lockstep issues.
**How to avoid:** For each new Lambda/queue/IAM/prompt change, add a deploy-surface verification pass:
- New DDB table: IAM cross-check (bridge + bridge Lambda can read/write it)
- EC2 resume: IAM cross-check (bridge Lambda has `ec2:DescribeInstances` + `ec2:StartInstances`)
- `KM_ARTIFACTS_BUCKET` env: check it's in `lambda-github-bridge`'s env block and exported by `km init`
- Module list: `km init --plan` should enumerate `dynamodb-github-threads`
- Sidecar/Lambda lists: km-github is already in `sidecarBuilds()` — no change needed

---

## Code Examples

### Check run (GH-X-CHECK)
```go
// Source: GitHub REST API docs — check runs endpoint (verified against Phase 97 pattern)
// POST /repos/{owner}/{repo}/check-runs
// Required: checks:write permission in the installation token.
type checkRunPayload struct {
    Name       string            `json:"name"`
    HeadSHA    string            `json:"head_sha"`
    Status     string            `json:"status"`     // "completed" for immediate result
    Conclusion string            `json:"conclusion"` // "success", "failure", "neutral"
    Output     checkRunOutput    `json:"output"`
}
type checkRunOutput struct {
    Title   string `json:"title"`
    Summary string `json:"summary"`
}
// Returns 201 Created with {id, html_url, ...}.
// Mirrors runReviewWith() pattern — same loadToken() + addGitHubHeaders() + POST.
```

### PR create (GH-X-PRCREATE)
```go
// Source: GitHub REST API docs — pulls endpoint (verified against Phase 97 pattern)
// POST /repos/{owner}/{repo}/pulls
// Required: pull_requests:write (already in GitHubInboundWritePerms()).
type prCreatePayload struct {
    Title string `json:"title"`
    Head  string `json:"head"`  // branch with changes
    Base  string `json:"base"`  // target branch
    Body  string `json:"body,omitempty"`
}
// Returns 201 with {html_url, number}. Print html_url to stdout for agent to read.
```

### km-github-threads DDB table (GH-X-CONTINUITY)
```hcl
# Source: infra/modules/dynamodb-slack-threads/v1.0.0/main.tf — adapted pattern
resource "aws_dynamodb_table" "github_threads" {
  name         = var.table_name  # default: "km-github-threads"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "repo"    # "owner/repo" string
  range_key    = "number"  # PR/issue number — N type (not S)

  attribute { name = "repo";   type = "S" }
  attribute { name = "number"; type = "N" }

  ttl { attribute_name = "ttl_expiry"; enabled = true }
  server_side_encryption { enabled = true }
}
# Row attributes (not declared as DDB attributes — just JSON items):
# repo (S), number (N), sandbox_id (S), agent_session_id (S), ttl_expiry (N)
```

### Thread store for GitHub threads (GH-X-CONTINUITY, GH-X-THREADBYPASS)
```go
// Source: pkg/slack/bridge/aws_adapters.go:861 — DDBThreadStore pattern
type DynamoGitHubThreadStore struct {
    Client    DynamoQueryPutter
    TableName string // e.g. "km-github-threads"
}

// LookupSandbox returns {sandbox_id, agent_session_id} for (repo, number), or "" if absent.
func (s *DynamoGitHubThreadStore) LookupSandbox(ctx context.Context, repo string, number int) (sandboxID, sessionID string, err error) {
    // GetItem with hash=repo, range=number (N); ProjectionExpression: sandbox_id, agent_session_id
}

// Upsert creates a row for (repo, number) → sandbox_id IF none exists.
// attribute_not_exists(repo) prevents overwriting a live session.
func (s *DynamoGitHubThreadStore) Upsert(ctx context.Context, repo string, number int, sandboxID string) error { ... }

// UpdateSession sets agent_session_id for (repo, number) — called by the poller after each turn.
func (s *DynamoGitHubThreadStore) UpdateSession(ctx context.Context, repo string, number int, sessionID string) error { ... }
```

### Sandbox resume in bridge (GH-X-RESUME)
```go
// Source: internal/app/cmd/resume.go:105 — StartInstances pattern
type EC2Resumer struct {
    EC2Client EC2StartAPI
    DDBClient DynamoQueryPutter
    TableName string
}

// StartSandbox looks up the EC2 instance_id from DDB and calls StartInstances.
func (r *EC2Resumer) StartSandbox(ctx context.Context, sandboxID string) error {
    instanceID, err := r.getInstanceID(ctx, sandboxID)  // DDB GetItem → ec2_instance_id attr
    if err != nil { return err }
    _, err = r.EC2Client.StartInstances(ctx, &ec2.StartInstancesInput{
        InstanceIds: []string{instanceID},
    })
    return err
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact for Phase 98 |
|--------------|------------------|--------------|---------------------|
| Cold-create: bridge sends incomplete SandboxCreate | Cold-create: bridge generates sandbox_id + artifact_prefix, km init pre-stages profile | Phase 98 (GH-COLD-CREATE) | Enables cold-create for the first time |
| km-github: only `comment` + `review` | km-github: adds `check` + `pr create` + `pr-files` | Phase 98 (GH-X-CHECK, GH-X-PRCREATE) | Agent can post check runs and open PRs |
| No thread continuity (every @-mention = new session) | Thread continuity via `km-github-threads` DDB | Phase 98 (GH-X-CONTINUITY) | Follow-up @-mentions resume existing Claude session |
| Thread-bypass: none (always require @-mention) | Thread-bypass: known `(repo, number)` skips mention check | Phase 98 (GH-X-THREADBYPASS) | Natural back-and-forth without re-mentioning bot |
| Stopped sandbox: warm path enqueues to dead queue | Stopped sandbox: auto-resumes then enqueues | Phase 98 (GH-X-RESUME) | "configure once, stop, GitHub wakes it" workflow |

**Nothing deprecated in Phase 98.** Phase 97 patterns are all extended, not replaced.

---

## Open Questions

1. **Head SHA for check runs — where does it come from?**
   - What we know: `GitHubEnvelope` has a `HeadSHA` field; Phase 97's `payload.go` populates it.
   - What's unclear: Does the `issue_comment` webhook payload include the PR's current head SHA directly, or does it need an extra `GET /repos/{owner}/{repo}/pulls/{n}` call?
   - Recommendation: Check `pkg/github/bridge/payload.go` IssueCommentPayload. If `Issue.PullRequest.Head.SHA` is in the payload, use it. Otherwise make `--head-sha` optional and have the agent fetch it via `km-github pr-files` output before calling `km-github check`.

2. **EC2 instance_id in DDB — is it already there?**
   - What we know: `SandboxMetadata` has `InstanceID string` (`dynamodbav:"ec2_instance_id"`) at `metadata.go`.
   - What's unclear: Is `ec2_instance_id` reliably populated for `ec2spot` sandboxes after Phase 97?
   - Recommendation: `grep -n "ec2_instance_id\|InstanceID" pkg/aws/sandbox_dynamo.go` to confirm. If not set, resume falls back to a `DescribeInstances` filter-by-tag.

3. **Profile pre-staging key format — per-alias or per-profile-slug?**
   - What we know: `km init` must upload to a deterministic key the bridge can reference. The bridge knows the `profile` field from config but not the sandbox_id at pre-stage time.
   - What's unclear: Should the key be `github-profiles/{profile-name}/.km-profile.yaml` or `github-profiles/{alias}/.km-profile.yaml`? Multiple aliases can share a profile.
   - Recommendation: Use profile slug (e.g. `github-review`) → `github-profiles/github-review/.km-profile.yaml`. Bridge sets `ArtifactPrefix = "github-profiles/" + profileSlug`. One upload per unique profile, not one per alias.

4. **SOPS bundle for cold-box auth — operator workflow?**
   - What we know: `spec.secrets.sopsFile` (Phase 89) injects secrets into sandbox via SOPS. `create-handler` already has `patchProfileForSops`. Claude creds (OAuth token) need to be in the bundle.
   - What's unclear: Is there an established pattern for SOPS-encrypting Claude credentials (`~/.claude/credentials.json`)? The Phase 89 SOPS bundle format is documented in `docs/sandbox-secrets.md`.
   - Recommendation: Document in `docs/github-bridge.md` § Cold-create auth: encrypt a bundle containing `ANTHROPIC_API_KEY` or Claude OAuth token; the sandbox's `/etc/km/notify.env` already injects env vars. The `github-review.yaml` profile gets `spec.secrets.sopsFile: github-review-secrets.enc.yaml`.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (`go test ./...`), `testify` assertions |
| Config file | none (standard Go test runner) |
| Quick run command | `go test ./cmd/km-github/... ./pkg/github/... ./pkg/github/bridge/... -count=1 -timeout 60s` |
| Full suite command | `go test ./... -count=1 -timeout 120s` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | Notes |
|--------|----------|-----------|-------------------|-------|
| GH-X-CHECK | `km-github check` posts check run, returns 0; bad conclusion → non-zero | Unit | `go test ./cmd/km-github/... -run TestCheck -count=1` | httptest server stub. File exists: ❌ Wave 0 |
| GH-X-PRCREATE | `km-github pr create` opens PR, prints html_url to stdout, returns 0 | Unit | `go test ./cmd/km-github/... -run TestPRCreate -count=1` | httptest server stub. File exists: ❌ Wave 0 |
| GH-X-PUSH | `git push` inside worktree uses km-git-credential-helper; no manual token required | Integration | `go test ./pkg/compiler/... -run TestUserdataGitHubCredentialHelper -count=1` | Compiler test verifies credential helper present in userdata. Existing test may cover. |
| GH-X-CONTINUITY | Bridge Upsert row on first dispatch; Get returns sandbox_id+sessionID on follow-up | Unit | `go test ./pkg/github/bridge/... -run TestGitHubThreadStore -count=1` | DynamoGitHubThreadStore with fake DDB client. File exists: ❌ Wave 0 |
| GH-X-THREADBYPASS | Handle() skips mention check when (repo, number) found in threads table | Unit | `go test ./pkg/github/bridge/... -run TestHandle_ThreadBypass -count=1` | WebhookHandler with stub Threads. File exists: ❌ Wave 0 |
| GH-X-SHARED | Multiple repo entries → same alias dispatched to same queue | Unit | `go test ./pkg/github/bridge/... -run TestResolve_SharedAlias -count=1` | `resolve_test.go` already tests alias resolution; add shared-alias case |
| GH-X-SHARED doctor | `km doctor` WARN on alias collision across repos | Unit | `go test ./internal/app/cmd/... -run TestDoctorGitHubAliasCollision -count=1` | Extend existing doctor tests. File exists: ❌ Wave 0 |
| GH-X-RESUME | Handle() calls Resumer.StartSandbox when alias found with status=stopped | Unit | `go test ./pkg/github/bridge/... -run TestHandle_AutoResume -count=1` | Stub EC2Resumer + alias resolver returning "stopped". File exists: ❌ Wave 0 |
| GH-X-RESUME IAM | Bridge Lambda IAM has ec2:StartInstances; module list guard | Unit | `go test ./internal/app/cmd/... -run TestRegionalModulesIncludesGitHubThreads -count=1` | Init module list guard test — add alongside existing TestRegionalModulesIncludesGitHubBridge |
| GH-COLD-CREATE sandbox_id | EventBridgeAdapter generates valid sandbox_id (gh- prefix, 8 hex) | Unit | `go test ./pkg/github/bridge/... -run TestEventBridgeAdapter_SandboxID -count=1` | Verify format: `^gh-[0-9a-f]{8}$`. File exists: ❌ Wave 0 |
| GH-COLD-CREATE artifact_prefix | EventBridgeAdapter sets artifact_prefix = "github-profiles/{slug}" | Unit | `go test ./pkg/github/bridge/... -run TestEventBridgeAdapter_ArtifactPrefix -count=1` | Verify no doubled path. File exists: ❌ Wave 0 |
| GH-COLD-CREATE profile staging | `km init` pre-stages profile to S3 for each github.repos entry | Unit | `go test ./internal/app/cmd/... -run TestPreStageGitHubProfiles -count=1` | Mock S3 PutObject calls; verify keys. File exists: ❌ Wave 0 |
| DDB threads module | `dynamodb-github-threads` in `regionalModules()` | Unit | `go test ./internal/app/cmd/... -run TestRegionalModulesIncludesGitHubThreads -count=1` | Guard test. File exists: ❌ Wave 0 |
| GH-X-E2E | Full chain: follow-up → session continued; check run visible on PR; shared-alias dispatch; stopped sandbox resumes | Manual UAT | — | Real AWS + GitHub App required |

### Wave 0 Gaps

- [ ] `cmd/km-github/check_test.go` — unit tests for `runCheck` + `runCheckWith` (GH-X-CHECK)
- [ ] `cmd/km-github/prcreate_test.go` — unit tests for `runPRCreate` + `runPRCreateWith` (GH-X-PRCREATE)
- [ ] `pkg/github/bridge/thread_store_test.go` — DynamoGitHubThreadStore unit tests (GH-X-CONTINUITY)
- [ ] `pkg/github/bridge/webhook_handler_test.go` — add TestHandle_ThreadBypass + TestHandle_AutoResume (GH-X-THREADBYPASS, GH-X-RESUME)
- [ ] `pkg/github/bridge/aws_adapters_test.go` — add TestEventBridgeAdapter_SandboxID + TestEventBridgeAdapter_ArtifactPrefix (GH-COLD-CREATE)
- [ ] `internal/app/cmd/init_github_prestage_test.go` — TestPreStageGitHubProfiles (GH-COLD-CREATE)
- [ ] `internal/app/cmd/init_test.go` — add TestRegionalModulesIncludesGitHubThreads + TestDoctorGitHubAliasCollision
- [ ] `infra/modules/dynamodb-github-threads/v1.0.0/` — TF module (GH-X-CONTINUITY infra)
- [ ] `infra/live/use1/dynamodb-github-threads/terragrunt.hcl` — live unit

None — existing test infrastructure does NOT cover Phase 98 requirements. All test files listed above are new (Wave 0 must create them).

### Sampling Rate
- **Per task commit:** `go test ./cmd/km-github/... ./pkg/github/... ./pkg/github/bridge/... -count=1 -timeout 60s`
- **Per wave merge:** `go test ./... -count=1 -timeout 120s`
- **Phase gate:** Full suite green (`go build ./...` + `go test ./...`) + deploy-surface checklist before `/gsd:verify-work`

---

## Sources

### Primary (HIGH confidence)

- In-tree: `cmd/km-github/main.go` — existing verb dispatch pattern (read 2026-06-07)
- In-tree: `pkg/github/bridge/webhook_handler.go` — 11-step Handle() ordering (read 2026-06-07)
- In-tree: `pkg/github/bridge/aws_adapters.go` — EventBridgeAdapter broken cold-create (read 2026-06-07)
- In-tree: `pkg/github/bridge/interfaces.go` — bridge interface contracts (read 2026-06-07)
- In-tree: `pkg/github/token.go` — JWT, token exchange, `GitHubInboundWritePerms()` (read 2026-06-07)
- In-tree: `pkg/slack/bridge/events_handler.go:311-339` — Phase 91.3 thread-bypass pattern (read 2026-06-07)
- In-tree: `pkg/slack/bridge/aws_adapters.go:861-930` — DDBThreadStore schema + Upsert pattern (read 2026-06-07)
- In-tree: `internal/app/cmd/create.go:2200-2328` — S3 artifact staging + SandboxCreate event (read 2026-06-07)
- In-tree: `cmd/create-handler/main.go:44-170` — CreateEvent struct, sandbox_id requirement (read 2026-06-07)
- In-tree: `internal/app/cmd/resume.go:105` — StartInstances call (read 2026-06-07)
- In-tree: `internal/app/cmd/init.go:1741-1960` — lambdaBuilds + sidecarBuilds lists (read 2026-06-07)
- In-tree: `infra/modules/lambda-github-bridge/v1.0.0/main.tf` — existing Lambda IAM (read 2026-06-07)
- In-tree: `infra/modules/dynamodb-slack-threads/v1.0.0/main.tf` — threads table TF pattern (read 2026-06-07)
- Phase 97 VERIFICATION.md — cold path findings + 7 deploy gaps (read 2026-06-07)
- Design spec: `docs/superpowers/specs/2026-06-06-github-app-bridge-pr-review-design.md` (read 2026-06-07)
- REQUIREMENTS.md Phase 98 section (lines 594-616) — 9 requirement IDs (read 2026-06-07)

### Secondary (MEDIUM confidence)

- GitHub REST API docs: Check Runs (`POST /repos/{owner}/{repo}/check-runs`) — verified against existing `review` endpoint pattern in km-github; API shape consistent with project's existing usage
- GitHub REST API docs: Pull Requests (`POST /repos/{owner}/{repo}/pulls`) — consistent with `review` + `comment` verb shapes

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all components are in-tree, verified against running code
- Architecture: HIGH — patterns are direct clones of Phase 97 / Phase 91.3 (in-tree)
- Pitfalls: HIGH — all 8 pitfalls derive from Phase 97 UAT findings or known project footguns (MEMORY.md)
- Cold-create fix: HIGH — all four defects identified in Phase 97 VERIFICATION.md with exact line references
- GH-X-RESUME: HIGH — resume.go pattern is in-tree; IAM gap identified from lambda-github-bridge/main.tf audit

**Research date:** 2026-06-07
**Valid until:** 2026-07-07 (stable domain; GitHub API shapes change rarely)
